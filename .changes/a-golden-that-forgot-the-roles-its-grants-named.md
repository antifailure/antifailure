# fixed

An environment branched from a golden no longer fails its first migration with
"role does not exist" for a role the source knew only through a GRANT.

Antifailure's own twin had not come up since migration 0037 landed. The copy
succeeded, the branch was made, and the migrate step died on `GRANT SELECT ON
environment_usage TO antifailure_app, antifailure_admin` with `role
"antifailure_admin" does not exist`. The golden is made with owners and
privileges dropped, so the restore never mentions a role that is only granted
to and never fails on one. The migration ledger travels with the copy, so the
migration that created the role is recorded as already applied and never runs
again. The first later migration to grant to the role then fails in every
branch, and it fails with a message that points at the migration rather than
at the copy. Nobody saw it because the dogfood workflows do not run in CI.

The copy created only the roles that row level security policies name, on the
reasoning that ownership and grants are dropped so the roles they mention are
not needed. That is true during the restore and false one step after it. The
role that broke the twin is one no policy names on purpose, because it exists
to bypass them, so it had no other way to arrive. The same was true of the
sweeper's role for any golden made before the migration that created it.

The copy now also creates every role the source names in a grant on a table,
sequence, view, function, type, or schema being copied, or in a default
privilege. Each arrives as it always did: NOLOGIN, with no attributes, no
memberships and no password, because its only job is to be a name a later
statement can resolve. A role with BYPASSRLS in the source arrives without it.
Owners are still left out, since ownership is dropped and nothing that runs
later needs an owner's name the way it needs a grantee's, and grants in a
schema the copy excludes do not bring that schema's roles along.

With that fixed, the twin got four migrations further and stopped on 0041 with
`antifailure_app cannot SET ROLE antifailure_sweeper, so the workflow token
sweep cannot run`. That migration checks an arrangement 0024 made and the
ledger says is done: the application's role is a NOINHERIT member of the
sweeper's. The copy carried both roles and none of the membership between
them. Memberships now travel too, with their INHERIT, SET and ADMIN options,
and only between roles the copy itself creates. A membership between two
NOLOGIN shells is not a credential, since neither can be connected as and
neither holds a privilege until a migration grants one, but it is the shape a
later migration checks. A membership in a role the copy does not carry, such
as the source's superuser, stays behind.

Rehearsed against this repository: a golden refreshed from staging, `af up`,
migrations 0037 through 0041 applied in the branch, the api reached ready, and
all six declared workflows and four invariants passed.
