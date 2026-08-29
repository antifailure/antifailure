-- Single sign-on and SCIM provisioning: the tables, and the four unauthenticated
-- lookups that have to determine a tenant without being able to enumerate one.
--
-- Everything that reads this schema lives in ee/, under the Antifailure
-- Enterprise License. The schema itself is here, and MIT, for the same reason
-- the extension points are: a migration is the shape of the database an
-- operator runs, and an operator who is not paying for the enterprise edition
-- still has to be able to apply it, read it, and see that it isolates them.
--
-- ---------------------------------------------------------------------------
-- The one problem this file is mostly about
-- ---------------------------------------------------------------------------
--
-- Row-level security keys off the tenant, and single sign-on is a sequence of
-- requests that arrive with no tenant at all. An identity provider POSTs an
-- assertion; a browser comes back from an authorization endpoint; a
-- provisioning client presents a bearer token; somebody types an email address
-- on the sign-in page. Every one of those has to find out which organization it
-- concerns, and the table holding the answer is isolated by organization, so
-- the lookup that determines the tenant cannot itself be tenant-scoped.
--
-- The repository has hit this five times and reached for the same wrong fix
-- each time: a policy that opens up when nothing is set. That is always a hole
-- the size of the table, because the unauthenticated request is precisely the
-- one with nothing set. Migrations 0003, 0004 and 0006 record the right fix and
-- this file applies it four more times: the caller declares the one value it is
-- already holding, and the policy returns the row that value names and nothing
-- else. Declaring a value you guessed returns nothing.
--
-- Which value, per entry point, and why it is safe to hold:
--
--   antifailure.sso_handle       The 256-bit identifier in the ACS URL. The
--                                identity provider holds it because an
--                                administrator pasted our URL into it.
--   antifailure.sso_entity_id    The Issuer of an identity-provider-initiated
--                                assertion, which arrives with no URL to carry
--                                a handle. Its own setting rather than a second
--                                meaning for sso_handle, because one setting
--                                compared against two columns means declaring
--                                either value silently matches on the other.
--   antifailure.sso_state        The 256-bit value the browser round-trips
--                                through the identity provider. Same shape as
--                                oauth_states, and the same reason.
--   antifailure.sso_domain       An email domain, which is not a secret. See
--                                the note on sso_domains for why that is still
--                                sound and what it deliberately does not
--                                expose.
--   antifailure.scim_token_hash  The hash of the bearer token the provisioning
--                                client presents. Identical to engine_tokens.
--
-- ---------------------------------------------------------------------------
-- The second rule: a policy exposes a row, so a row must not mix secrets with
-- routing
-- ---------------------------------------------------------------------------
--
-- Row-level security is row level. There is no column-level clause, so any
-- policy that lets an unauthenticated caller reach a row lets it read every
-- column of that row. An OIDC client secret and the SAML service provider's
-- private key therefore do not live on sso_connections, which is the table the
-- handle lookup reaches. They live in sso_connection_secrets, which has exactly
-- one policy and it requires the tenant. The callback resolves the tenant from
-- the routing row first and reads the secret second, scoped.
--
-- That split is the only reason the handle lookup is safe to have at all, and
-- it is the thing to check first if anybody ever adds a column here.

BEGIN;

-- ---------------------------------------------------------------------------
-- Identity that did not come from GitHub
-- ---------------------------------------------------------------------------

-- users.github_id was NOT NULL, which quietly made GitHub the only way an
-- account could exist. A member provisioned by SCIM or created just-in-time by
-- an assertion has no GitHub account and there is no value to invent: 0 is a
-- real user id, and a synthetic negative number is a lie that some later join
-- believes.
--
-- UNIQUE is kept and keeps working, because a unique index in Postgres permits
-- any number of NULLs. Every existing ON CONFLICT (github_id) upsert is
-- unaffected: those paths always have an id.
--
-- github_login goes with it. It is GitHub's name for the account and an account
-- that is not GitHub's does not have one; leaving it NOT NULL would force the
-- email address in there, which is the sort of thing that reads fine until
-- somebody displays it as a GitHub handle or links to github.com/<it>.
ALTER TABLE users ALTER COLUMN github_id DROP NOT NULL;
ALTER TABLE users ALTER COLUMN github_login DROP NOT NULL;

-- Which is now a real possibility worth constraining: an account has to be
-- reachable by something. Email is already NOT NULL, so this is about not
-- ending up with a row that is neither a GitHub identity nor a directory one.
ALTER TABLE users ADD COLUMN identity_source text NOT NULL DEFAULT 'github'
  CONSTRAINT users_identity_source_known
  CHECK (identity_source IN ('github', 'sso', 'scim'));

-- A directory owns the profile of the accounts it provisions.
--
-- Without this, SCIM cannot write. The only UPDATE policy on users is
-- `id = current_actor()`, which is right for a person editing their own
-- profile and useless for a provisioning client, which acts as no user at all.
-- An UPDATE that matches no row does not raise; it reports success having
-- changed nothing. So a userName change in the directory would return 200,
-- appear applied at both ends, and leave the address here as it was, which then
-- silently breaks the email match that links an assertion to an account.
--
-- The policy is deliberately narrow in two ways.
--
-- It never touches a GitHub identity. That row belongs to a person's GitHub
-- account, which they may use at more than one organization, and letting an
-- administrator here rewrite its email address would be a way to redirect
-- somebody else's identity.
--
-- It requires that the account belongs to THIS organization and to no other.
-- That is true by construction today, because an sso or scim account is only
-- ever created by the organization provisioning it and matched by email only
-- within that organization, and stating it as a constraint means it stays true
-- rather than being an invariant somebody has to remember.
--
-- The exclusivity check has to be SECURITY DEFINER. A subquery inside a policy
-- has row-level security applied to it in turn, so `NOT EXISTS (SELECT 1 FROM
-- members WHERE org_id <> current_org())` would be checking a table the caller
-- cannot see and would be true for everybody: a guard that reads correctly and
-- enforces nothing. This function runs as its owner so that it can actually
-- see, and it returns one boolean about one user rather than any rows.
CREATE OR REPLACE FUNCTION user_belongs_only_to(target uuid, org uuid) RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = public, pg_temp
  AS $$
    SELECT org IS NOT NULL
       AND EXISTS (SELECT 1 FROM members m WHERE m.user_id = target AND m.org_id = org)
       AND NOT EXISTS (SELECT 1 FROM members m WHERE m.user_id = target AND m.org_id <> org)
  $$;
REVOKE ALL ON FUNCTION user_belongs_only_to(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION user_belongs_only_to(uuid, uuid) TO antifailure_app;

-- One consequence worth stating, because it is an ordering constraint and not
-- a modelling detail: this policy is only true while the membership row exists.
-- A caller that deletes the membership and then updates the profile updates
-- nothing, silently, and a caller that creates the profile and then the
-- membership cannot read the profile back. So deprovisioning writes the profile
-- FIRST and removes access second, reprovisioning grants access first and
-- writes the profile second, and creation supplies the primary key rather than
-- asking for it back. Each of those is the difference between working and
-- appearing to work.
CREATE POLICY directory_updates ON users
  FOR UPDATE TO antifailure_app
  USING (
    identity_source <> 'github'
    AND user_belongs_only_to(users.id, current_org())
  )
  WITH CHECK (
    identity_source <> 'github'
    AND user_belongs_only_to(users.id, current_org())
  );

-- ---------------------------------------------------------------------------
-- The settings the four unauthenticated lookups declare
--
-- Each is transaction-local, set by the pool at the start of the one statement
-- that needs it and cleared by every other transaction. A request that has not
-- declared a value reads nothing new, which is what makes adding these
-- lookups a narrowing rather than a widening.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION current_sso_handle() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.sso_handle', true), '') $$;

CREATE OR REPLACE FUNCTION current_sso_entity_id() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.sso_entity_id', true), '') $$;

CREATE OR REPLACE FUNCTION current_sso_state() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.sso_state', true), '') $$;

CREATE OR REPLACE FUNCTION current_sso_domain() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.sso_domain', true), '') $$;

CREATE OR REPLACE FUNCTION current_scim_token_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.scim_token_hash', true), ''), 'hex') $$;

-- ---------------------------------------------------------------------------
-- Connections
-- ---------------------------------------------------------------------------

CREATE TABLE sso_connections (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

  -- What appears in the URLs an administrator pastes into their identity
  -- provider: the assertion consumer service, the OIDC redirect, and the
  -- service provider metadata. 256 bits of randomness rather than the
  -- organization's slug, because those URLs are handed out, and a URL built
  -- from the slug turns "who else uses this product" into a guessing game
  -- against a dictionary of company names.
  --
  -- It is not treated as a secret. Anybody who knows a verified email domain
  -- can obtain it from the discovery endpoint, which is unavoidable: that
  -- endpoint exists to send a browser to the right identity provider. Nothing
  -- reachable by presenting it is confidential, and presenting it grants no
  -- ability to sign in, because an assertion still has to carry a signature
  -- from the provider's key.
  handle                text NOT NULL UNIQUE
                        CONSTRAINT sso_connections_handle_shape
                        CHECK (handle ~ '^[A-Za-z0-9_-]{40,64}$'),

  kind                  text NOT NULL
                        CONSTRAINT sso_connections_kind_known
                        CHECK (kind IN ('saml', 'oidc')),
  display_name          text NOT NULL,

  -- Off until an administrator has finished configuring it and turned it on.
  enabled               boolean NOT NULL DEFAULT false,

  -- Enforcement is separate from being enabled, and deliberately so. Turning
  -- SSO on and turning GitHub sign-in off are two decisions, and doing them in
  -- one step is how an organization locks itself out with a bad metadata paste.
  -- The intended order is: enable, sign in through it successfully, then
  -- enforce.
  enforced              boolean NOT NULL DEFAULT false,

  -- What a member provisioned just-in-time becomes. Never owner: an assertion
  -- from a misconfigured provider must not be able to mint somebody who can
  -- change billing and remove other owners.
  default_role          member_role NOT NULL DEFAULT 'member'
                        CONSTRAINT sso_connections_default_role_not_owner
                        CHECK (default_role <> 'owner'),

  -- SAML. Certificates are the provider's public signing certificates, base64
  -- DER as they appear in metadata. Plural because certificate rotation means
  -- two are valid at once, and an implementation that holds one has a planned
  -- outage every time the provider rotates.
  idp_entity_id         text,
  idp_sso_url           text,
  idp_certificates      text[] NOT NULL DEFAULT '{}',

  -- OIDC. Endpoints are stored rather than discovered per request: discovery
  -- is a network call to somebody else's service on the critical path of every
  -- login, and a provider whose discovery document is briefly unreachable
  -- should not be an outage here.
  oidc_issuer           text,
  oidc_client_id        text,
  oidc_authorization_endpoint text,
  oidc_token_endpoint   text,
  oidc_jwks_uri         text,

  -- Group claim to role. A JSON object of claim value to member_role, applied
  -- on every sign-in so that a removal in the directory takes effect on the
  -- next login rather than never.
  group_role_map        jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- The tolerance applied to NotBefore, NotOnOrAfter, iat and exp. The spec
  -- calls for five minutes and every provider assumes something like it;
  -- configurable because one customer will have a provider that is worse.
  clock_skew_seconds    integer NOT NULL DEFAULT 300
                        CONSTRAINT sso_connections_skew_bounded
                        CHECK (clock_skew_seconds BETWEEN 0 AND 600),

  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),

  -- A connection that is switched on has to be completely configured, checked
  -- by the database rather than by whichever code path happens to run first.
  -- Half-configured rows are allowed while an administrator is still pasting
  -- metadata in, and the moment one is enabled it must be usable.
  --
  -- This is the constraint that stops the failure this repository keeps
  -- hitting: a settings row that reads as configuration and behaves as
  -- decoration, where the feature appears on and silently does nothing.
  CONSTRAINT sso_connections_enabled_is_complete CHECK (
    NOT enabled
    OR (kind = 'saml' AND idp_entity_id IS NOT NULL
                      AND idp_sso_url IS NOT NULL
                      AND array_length(idp_certificates, 1) >= 1)
    OR (kind = 'oidc' AND oidc_issuer IS NOT NULL
                      AND oidc_client_id IS NOT NULL
                      AND oidc_authorization_endpoint IS NOT NULL
                      AND oidc_token_endpoint IS NOT NULL
                      AND oidc_jwks_uri IS NOT NULL)
  ),

  -- One connection of each kind per organization. Two SAML connections would
  -- make "which provider does this person use" ambiguous on every sign-in and
  -- the answer would depend on row order; one SAML and one OIDC is a real
  -- configuration, because a migration between the two runs both for a while.
  CONSTRAINT sso_connections_entity_per_org UNIQUE (org_id, kind)
);
CREATE INDEX sso_connections_org_idx ON sso_connections (org_id);
-- IdP-initiated SAML arrives with an Issuer and no handle, so the entity id has
-- to be resolvable on its own.
CREATE UNIQUE INDEX sso_connections_entity_idx
  ON sso_connections (idp_entity_id) WHERE idp_entity_id IS NOT NULL;

-- The secrets, kept apart from the routing.
--
-- Nothing unauthenticated reaches this table. Its only policy requires the
-- tenant, and the tenant is known by the time anything here is needed: the
-- routing row named it.
--
-- Values are stored encrypted by the application under a key the database
-- never sees, so a database backup that leaks is not a working OIDC client and
-- not a signing key. Postgres holds bytea and has no opinion about the
-- contents; the format is versioned in the first byte so a key can be rotated
-- without a migration.
CREATE TABLE sso_connection_secrets (
  connection_id         uuid PRIMARY KEY REFERENCES sso_connections(id) ON DELETE CASCADE,
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  oidc_client_secret    bytea,
  -- Used to sign AuthnRequests and to decrypt encrypted assertions. Optional:
  -- a provider that requires neither does not need one.
  sp_private_key        bytea,
  sp_certificate        text,
  updated_at            timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Claimed email domains
-- ---------------------------------------------------------------------------

-- Two things happen here and only one of them is discovery.
--
-- The first is verification. An organization may not claim an email domain by
-- typing it: it proves control with a DNS TXT record, exactly as every other
-- product does, because otherwise claiming gmail.com would route every Google
-- user's sign-in through somebody else's identity provider, and just-in-time
-- provisioning would then put them in that organization.
--
-- The second is the lookup. The sign-in page takes an email address and has to
-- answer "where do I send this browser", with no session and no tenant. It
-- declares the domain and the policy returns the verified row for that domain
-- alone. The domain is not a secret and this is not pretending otherwise: what
-- it grants is the ability to learn that a domain uses SSO and to get its
-- handle, which is the same fact the redirect itself announces, and which
-- allows nothing without a signed assertion. It deliberately does not expose
-- the domains an organization has NOT verified, or any domain other than the
-- one asked about, so this cannot be turned into a customer list.
CREATE TABLE sso_domains (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  connection_id         uuid NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,
  domain                text NOT NULL
                        CONSTRAINT sso_domains_lowercase CHECK (domain = lower(domain))
                        CONSTRAINT sso_domains_shape
                        CHECK (domain ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'),
  -- What has to appear in a TXT record at _antifailure-verification.<domain>.
  verification_token    text NOT NULL,
  verified_at           timestamptz,
  created_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, domain)
);
-- A verified claim is exclusive; an unverified one is not, so that a typo in
-- one organization cannot stop the real owner from claiming their own domain.
CREATE UNIQUE INDEX sso_domains_verified_idx
  ON sso_domains (domain) WHERE verified_at IS NOT NULL;
CREATE INDEX sso_domains_org_idx ON sso_domains (org_id);

-- ---------------------------------------------------------------------------
-- Logins in flight
-- ---------------------------------------------------------------------------

-- The same shape as oauth_states and for the same reasons: stored rather than
-- signed into a cookie so that it can be deleted on use, because a signed value
-- proves it was issued and cannot prove it has not already been redeemed, and a
-- replayable callback is a session fixation primitive.
--
-- It carries the PKCE verifier, which is a secret, and that is what the
-- declared-state policy is protecting. Holding the state is what makes the row
-- visible, and the state is the value the browser round-tripped.
CREATE TABLE sso_login_states (
  state                 text PRIMARY KEY,
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  connection_id         uuid NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,
  -- OIDC.
  nonce                 text,
  code_verifier         text,
  -- SAML. request_id is the AuthnRequest ID, matched against InResponseTo so
  -- that a response to somebody else's request is refused.
  request_id            text,
  relay_state           text,
  redirect_to           text,
  created_at            timestamptz NOT NULL DEFAULT now(),
  expires_at            timestamptz NOT NULL
);
CREATE INDEX sso_login_states_expiry_idx ON sso_login_states (expires_at);

-- ---------------------------------------------------------------------------
-- Replay protection
-- ---------------------------------------------------------------------------

-- A signed assertion stays valid until it expires, so an assertion captured
-- once can be presented again inside its own validity window unless something
-- remembers it. This is that something, and the UNIQUE constraint is what makes
-- it correct rather than merely present: the check is an INSERT that either
-- takes the identifier or conflicts, so two requests racing with the same
-- assertion cannot both find it absent. A SELECT-then-INSERT would let both
-- through, which is exactly the window an attacker replaying is aiming at.
--
-- Rows are deleted once expires_at has passed, because after that the assertion
-- is refused on its own terms and remembering it proves nothing.
CREATE TABLE sso_assertions_seen (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  connection_id         uuid NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,
  assertion_id          text NOT NULL,
  seen_at               timestamptz NOT NULL DEFAULT now(),
  expires_at            timestamptz NOT NULL,
  UNIQUE (connection_id, assertion_id)
);
CREATE INDEX sso_assertions_seen_expiry_idx ON sso_assertions_seen (expires_at);

-- ---------------------------------------------------------------------------
-- Break-glass
-- ---------------------------------------------------------------------------

-- An organization that has enforced SSO and then pasted the wrong metadata has
-- locked every one of its members out of the control plane, including the
-- people who could fix it. There is no self-service recovery from that, and
-- support having a way in is not a substitute: it is a support ticket at a
-- weekend.
--
-- So enforcement is escapable by an owner holding a recovery code. Codes are
-- generated when enforcement is turned on, shown once, and stored as hashes,
-- for the same reason session tokens are.
--
-- Notice what is NOT here: a policy that lets an unauthenticated caller find a
-- code. It does not need one. Break-glass is not a second way to authenticate,
-- it is a decision not to apply enforcement to a sign-in that has already
-- happened through GitHub, and by that point the user, their memberships and
-- therefore the tenant are all known. Modelling it as its own login would have
-- meant an unauthenticated lookup keyed on a six-word code, which is a
-- guessable secret and a much worse thing to own.
CREATE TABLE sso_break_glass_codes (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  code_hash             bytea NOT NULL,
  created_at            timestamptz NOT NULL DEFAULT now(),
  -- Single use. Consuming a code records who used it and when; the audit entry
  -- is written alongside and is the thing a security review reads.
  used_at               timestamptz,
  used_by               uuid REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE (org_id, code_hash)
);

-- ---------------------------------------------------------------------------
-- SCIM
-- ---------------------------------------------------------------------------

-- Identical in shape to engine_tokens: the provisioning client presents a
-- bearer token, the control plane has to work out whose it is, and the table is
-- isolated by organization. The caller declares the hash and the policy returns
-- that row.
CREATE TABLE scim_tokens (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name                  text NOT NULL,
  token_hash            bytea NOT NULL UNIQUE,
  -- The first few characters, so the dashboard can show which token is which
  -- without holding anything that would let it act as one.
  prefix                text NOT NULL,
  created_at            timestamptz NOT NULL DEFAULT now(),
  last_used_at          timestamptz,
  -- Rotation is two live tokens for a window rather than a cutover, because a
  -- cutover means provisioning is broken for however long it takes somebody to
  -- paste the new value into the identity provider.
  expires_at            timestamptz,
  revoked_at            timestamptz
);
CREATE INDEX scim_tokens_org_idx ON scim_tokens (org_id);

-- One provisioned user, as the identity provider sees it.
--
-- Kept separate from members rather than folded into it, because the two
-- disagree on purpose. SCIM owns userName, externalId, names and the active
-- flag; this product owns the role and the membership. Folding them together
-- would mean a PATCH from the provider could write a column the provider has no
-- opinion about, and the first such write would silently overwrite something an
-- administrator set here.
CREATE TABLE scim_resources (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id               uuid REFERENCES users(id) ON DELETE SET NULL,
  -- The provider's own identifier. Optional because the specification makes it
  -- optional, and Okta and Entra ID disagree about when they send it.
  external_id           text,
  user_name             text NOT NULL
                        CONSTRAINT scim_resources_username_lowercase
                        CHECK (user_name = lower(user_name)),
  active                boolean NOT NULL DEFAULT true,
  given_name            text,
  family_name           text,
  display_name          text,
  -- The ETag. Incremented on every write so a provider using If-Match sees a
  -- conflict rather than clobbering a concurrent change.
  version               integer NOT NULL DEFAULT 1,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, user_name)
);
CREATE UNIQUE INDEX scim_resources_external_idx
  ON scim_resources (org_id, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX scim_resources_user_idx ON scim_resources (user_id);

CREATE TABLE scim_groups (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  external_id           text,
  display_name          text NOT NULL,
  -- What membership of this group grants. NULL means the group is synced and
  -- maps to nothing, which is the common case: a directory has hundreds of
  -- groups and two of them are about this product.
  role                  member_role,
  version               integer NOT NULL DEFAULT 1,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, display_name)
);
CREATE UNIQUE INDEX scim_groups_external_idx
  ON scim_groups (org_id, external_id) WHERE external_id IS NOT NULL;

-- Group membership, including membership of somebody who does not exist here
-- yet.
--
-- That nullable resource_id is the whole point of this table, and it is the
-- ordering that a naive implementation gets wrong. Okta and Entra ID both send
-- group membership referring to users they have not created yet, and an
-- implementation that resolves the reference at write time either drops the
-- member silently or rejects the request, and in both cases the group is
-- permanently missing somebody. So the reference is stored as it arrived, and
-- resolved when the user turns up.
CREATE TABLE scim_group_members (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  group_id              uuid NOT NULL REFERENCES scim_groups(id) ON DELETE CASCADE,
  -- The "value" the provider sent, which is our resource id when it knows one
  -- and its own identifier when it does not.
  member_ref            text NOT NULL,
  resource_id           uuid REFERENCES scim_resources(id) ON DELETE CASCADE,
  created_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (group_id, member_ref)
);
CREATE INDEX scim_group_members_resource_idx ON scim_group_members (resource_id);
-- The lookup that resolves a pending reference when a user is finally created.
CREATE INDEX scim_group_members_pending_idx
  ON scim_group_members (org_id, member_ref) WHERE resource_id IS NULL;

-- ---------------------------------------------------------------------------
-- Grants
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON
  sso_connections, sso_connection_secrets, sso_domains, sso_login_states,
  sso_assertions_seen, sso_break_glass_codes,
  scim_tokens, scim_resources, scim_groups, scim_group_members
TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
--
-- Every table above carries org_id, so every one of them is picked up by the
-- cross-tenant suite automatically and has to satisfy it. The plain policy is
-- applied in a loop for the same reason 0002 does it: written out ten times,
-- the one that is subtly different is invisible.
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'sso_connections', 'sso_connection_secrets', 'sso_domains',
    'sso_login_states', 'sso_assertions_seen', 'sso_break_glass_codes',
    'scim_tokens', 'scim_resources', 'scim_groups', 'scim_group_members'
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

-- ---------------------------------------------------------------------------
-- The four declared lookups
--
-- Each is SELECT only, and each is false when its setting is unset, because
-- nullif returns NULL and a comparison against NULL is not true. There is no
-- policy here that opens up when nothing is declared.
-- ---------------------------------------------------------------------------

-- An assertion arriving at /sso/saml/<handle>/acs, or a browser returning to
-- /sso/oidc/<handle>/callback, resolves the connection it names. No secret is
-- reachable through this: they are in sso_connection_secrets, which this does
-- not cover.
CREATE POLICY resolve_by_handle ON sso_connections
  FOR SELECT TO antifailure_app
  USING (handle = current_sso_handle());

-- IdP-initiated SAML has no handle in the URL, only an Issuer in the assertion.
-- Same disclosure as the handle: routing, no secrets. The entity id is
-- published in the provider's own metadata, so it is not a secret either, and
-- what it yields still cannot be used without a valid signature.
CREATE POLICY resolve_by_entity ON sso_connections
  FOR SELECT TO antifailure_app
  USING (idp_entity_id IS NOT NULL AND idp_entity_id = current_sso_entity_id());

-- The sign-in page asking where to send an email address. Verified claims only,
-- so an unverified typo in another organization routes nobody.
CREATE POLICY resolve_by_domain ON sso_domains
  FOR SELECT TO antifailure_app
  USING (verified_at IS NOT NULL AND domain = current_sso_domain());

-- The browser coming back from the identity provider, presenting the state it
-- was given. DELETE as well as SELECT, because consuming the state is what
-- makes it single use, and it is deleted and returned in one statement so two
-- callbacks racing cannot both find it.
CREATE POLICY resolve_by_state ON sso_login_states
  FOR SELECT TO antifailure_app
  USING (state = current_sso_state());
CREATE POLICY consume_by_state ON sso_login_states
  FOR DELETE TO antifailure_app
  USING (state = current_sso_state());

-- A provisioning client presenting its bearer token. UPDATE as well, so that
-- last_used_at can be written on the request that used it, which is what makes
-- a stale token visible in the dashboard.
CREATE POLICY presented_scim_token ON scim_tokens
  FOR SELECT TO antifailure_app
  USING (token_hash = current_scim_token_hash());
CREATE POLICY touch_presented_scim_token ON scim_tokens
  FOR UPDATE TO antifailure_app
  USING (token_hash = current_scim_token_hash())
  WITH CHECK (token_hash = current_scim_token_hash());

COMMIT;
