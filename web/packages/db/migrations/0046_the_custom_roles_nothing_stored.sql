-- The custom roles nothing stored.
--
-- THE FAILURE THIS EXISTS FOR. The enterprise licence, LICENSING.md and
-- ee/README.md all sold custom roles. The mechanism was real at both ends and
-- joined at neither: the community API has asked a permission resolver on every
-- request since permissions.ts was written, and ee/web/rbac has always carried
-- a role model, a validator and a resolver that answers in exactly that
-- resolver's shape. What did not exist was anywhere for an organization's model
-- to live, so no organization could have one, and a licence naming rbac
-- verified, reported itself active, and granted nothing.
--
-- Everything that reads or writes these tables lives in ee/, under the
-- Antifailure Enterprise License. The schema is here, and MIT, for the reason
-- 0014 gives for single sign on: an operator who is not paying for the
-- enterprise edition still applies this migration and has to be able to read
-- it and see that it isolates them.
--
-- ---------------------------------------------------------------------------
-- The shape follows ee/web/rbac/src/roles.ts and nothing else
-- ---------------------------------------------------------------------------
--
-- A role has a stable identifier its author chose, a name, a description and a
-- set of permissions. A grant is one person holding one role at one scope. A
-- repository group is a named set of repositories a grant can cover. Those are
-- the Model's three lists, one table each, plus the permissions a role holds,
-- because an array column is a list nothing can index or constrain per element.
--
-- Permissions are text and are not constrained here, and that is a decision.
-- The catalogue is a TypeScript constant in web/apps/api/src/permissions.ts,
-- and a copy of it in SQL would be a fourth copy with nothing holding it. The
-- write path refuses a name that is not in the catalogue before it reaches this
-- table, and the read path can only ever grant a permission a request names,
-- which always comes from the catalogue, so a stray name here grants nothing.
--
-- ---------------------------------------------------------------------------
-- The one problem this file is mostly about: a foreign key ignores RLS
-- ---------------------------------------------------------------------------
--
-- Row level security confines what a tenant can SELECT, INSERT and UPDATE. It
-- does not confine what a foreign key accepts: the referential check runs as
-- the table owner and sees every row. So a single-column reference from a grant
-- to a role would let a tenant that learned another tenant's role uuid insert a
-- grant pointing at it, with its own org_id on the row and the policy
-- satisfied. Every reference here is therefore COMPOSITE, carrying org_id on
-- both sides, and a row can only ever point at a row of its own organization.
--
-- The grant's reference to the member is composite for the same reason and for
-- a second one that is worth more. It is to members, not to users, so a grant
-- can only name somebody who belongs to the organization, and it cascades, so
-- removing a member removes what they were granted. Re-adding them later does
-- not bring a grant back, which is the direction access should fail in.

CREATE TABLE custom_roles (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- The identifier a policy file refers to the role by. Stable across a rename
  -- because a grant names it, and shaped like a slug so it survives YAML.
  role_key              text NOT NULL
                        CONSTRAINT custom_roles_key_shape
                        CHECK (role_key ~ '^[a-z0-9][a-z0-9_-]{0,62}$'),
  name                  text NOT NULL CONSTRAINT custom_roles_name_present CHECK (name <> ''),
  -- Required, because a role nobody can review by reading is a role nobody
  -- reviews. validate() in ee/web/rbac says the same thing to a person.
  description           text NOT NULL
                        CONSTRAINT custom_roles_description_present CHECK (description <> ''),
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, role_key),
  -- The target of the composite references below.
  UNIQUE (org_id, id)
);

CREATE TABLE custom_role_permissions (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  role_id               uuid NOT NULL,
  permission            text NOT NULL,
  FOREIGN KEY (org_id, role_id) REFERENCES custom_roles (org_id, id) ON DELETE CASCADE,
  UNIQUE (role_id, permission)
);

CREATE TABLE repository_groups (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name                  text NOT NULL CONSTRAINT repository_groups_name_present CHECK (name <> ''),
  created_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name),
  UNIQUE (org_id, id)
);

CREATE TABLE repository_group_members (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  group_id              uuid NOT NULL,
  -- The full name, owner and repository, the way a request names it. Not a
  -- reference to repositories: a group may name a repository before the App is
  -- installed on it, and a grant on it then applies from the first request.
  repository            text NOT NULL
                        CONSTRAINT repository_group_members_repository_present
                        CHECK (repository <> ''),
  FOREIGN KEY (org_id, group_id) REFERENCES repository_groups (org_id, id) ON DELETE CASCADE,
  UNIQUE (group_id, repository)
);

CREATE TABLE custom_role_grants (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  role_id               uuid NOT NULL,
  user_id               uuid NOT NULL,
  scope_kind            text NOT NULL
                        CONSTRAINT custom_role_grants_scope_kind
                        CHECK (scope_kind IN ('organization', 'group', 'repository', 'environment')),
  -- The group name, repository full name or environment identifier. NULL for
  -- the organization scope, which covers everything, and required for every
  -- other, so "a repository grant naming no repository" cannot be stored.
  scope_name            text,
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT custom_role_grants_scope_named
    CHECK ((scope_kind = 'organization') = (scope_name IS NULL)),
  FOREIGN KEY (org_id, role_id) REFERENCES custom_roles (org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, user_id) REFERENCES members (org_id, user_id) ON DELETE CASCADE,
  -- NULLS NOT DISTINCT so that two organization-wide grants of the same role to
  -- the same person are one grant, which they are.
  CONSTRAINT custom_role_grants_once
    UNIQUE NULLS NOT DISTINCT (org_id, role_id, user_id, scope_kind, scope_name)
);

-- The read every request in an entitled organization makes: this person's
-- grants in this organization.
CREATE INDEX custom_role_grants_member_idx ON custom_role_grants (org_id, user_id);
CREATE INDEX custom_role_grants_role_idx ON custom_role_grants (role_id);
CREATE INDEX custom_role_permissions_org_idx ON custom_role_permissions (org_id);
CREATE INDEX repository_group_members_org_idx ON repository_group_members (org_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON
  custom_roles, custom_role_permissions, repository_groups,
  repository_group_members, custom_role_grants
TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
--
-- The plain tenant policy, in a loop for the reason 0002 and 0014 give: written
-- out five times, the one that is subtly different is invisible. Every table
-- carries org_id, so the cross-tenant suite picks each one up from the database
-- and has to be satisfied by it.
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'custom_roles', 'custom_role_permissions', 'repository_groups',
    'repository_group_members', 'custom_role_grants'
  ]
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$
      CREATE POLICY tenant_isolation ON %I
        FOR ALL TO antifailure_app
        USING (org_id = current_org())
        WITH CHECK (org_id = current_org())
    $p$, t);
  END LOOP;
END
$$;
