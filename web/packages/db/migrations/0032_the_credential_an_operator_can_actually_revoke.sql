-- The two write grants that make the operator portal's revoke button real.
--
-- WHY A MIGRATION IS UNAVOIDABLE HERE, rather than a nicety. 0023 gives
-- antifailure_admin SELECT on every table in the schema and enumerates its
-- writes one at a time: users, organizations, members, sessions. 0029 adds the
-- operator's own tables, 0030 adds the money ledger. Nothing anywhere grants it
-- UPDATE on engine_tokens or on oidc_repository_bindings. So an operator route
-- that revoked a credential would read the row, show it, offer the button, and
-- fail with 42501 at the instant somebody pressed it, during whatever incident
-- made them press it.
--
-- 0023 states the rule this file follows: reading every tenant is what support
-- work needs, changing every tenant's rows is not, and what is granted is
-- EXACTLY the set of actions the portal offers as buttons. These are two more
-- buttons, so these are two more grants, named one at a time.
--
-- WHY REVOKING IS A WRITE THE PLATFORM SHOULD HOLD AT ALL. A credential that
-- has leaked is the platform's problem before it is the customer's. The
-- customer can already revoke their own with `af token create` and
-- `af token revoke`, and when the report arrives from a third party at three in
-- the morning the person who can act on it is the operator on call, not
-- somebody at the customer who has not been woken up yet.
--
-- WHY UPDATE AND NOT DELETE. A revoked credential is a row that records what
-- was allowed to act as this organization and when that stopped. Deleting it
-- would destroy the evidence somebody comes looking for, which is the same
-- argument 0025 makes when it withholds DELETE on oidc_repository_bindings from
-- the application. Withheld here explicitly, so a later blanket grant has to
-- overwrite a line saying why it should not be given rather than merely forget
-- to add one.
--
-- WHAT THIS DOES NOT GRANT, and each absence is a decision:
--
--   INSERT on engine_tokens. Minting is creating a secret, and a route that
--   minted one would have to return it through the operator portal. Only the
--   hash is stored, so no route in this product can show a token after the
--   moment it is created, and the portal must not become the one exception. An
--   operator revokes; the customer creates the replacement.
--
--   Anything on scim_tokens. The table has existed since 0014 and nothing under
--   web/apps/api/src reads it, so a SCIM token authenticates nothing today.
--   Granting a write against it would be arranging for a button that stops
--   something already stopped.
--
--   Anything on provider_keys. A customer's own key to a third party is theirs,
--   the ciphertext is not ours to touch, and revoking one breaks their runs
--   without answering an operator question. If that changes, it changes with
--   its own migration and its own argument.

BEGIN;

-- Revoking an engine, cli or oidc token. authenticateEngine in
-- apps/api/src/ingest.ts reads revoked_at on every POST /v1/events before it
-- reads anything else, so this grant is what stands between a leaked credential
-- and the next event it would have been accepted for.
GRANT UPDATE ON engine_tokens TO antifailure_admin;
REVOKE INSERT, DELETE, TRUNCATE ON engine_tokens FROM antifailure_admin;

-- Revoking a GitHub OIDC repository binding. The route revokes the binding AND
-- every live token it minted in one statement, which is what apps/api/src/
-- github/exchange.ts already does for the customer's own command: a revocation
-- that stops new tokens being issued while the issued ones keep working is not
-- a revocation. That second half is why the grant above is needed for this
-- action too.
GRANT UPDATE ON oidc_repository_bindings TO antifailure_admin;
REVOKE INSERT, DELETE, TRUNCATE ON oidc_repository_bindings FROM antifailure_admin;

COMMIT;
