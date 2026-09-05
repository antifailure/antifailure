-- The tables a browser-based agent needs before it can call a tool.
--
-- WHY THIS EXISTS. `af mcp` serves the rehearsal tools over standard input and
-- output to a process the client started. That covers every coding agent and
-- it covers neither claude.ai nor chatgpt.com, because a web page cannot start
-- a process on somebody's laptop. Both connect to one thing only: an HTTPS URL
-- speaking Streamable HTTP, authorized by OAuth. This control plane already
-- knows who a person is, which organization they are in and what their role
-- lets them read; what it did not have was a way to say any of that to an
-- OAuth client it had never met.
--
-- THREE PIECES, AND EACH ONE IS A TABLE HERE OR A COLUMN ON AN EXISTING ONE:
--
--   mcp_clients               who is asking. Registered dynamically, per
--                             RFC 7591, because Claude registers itself.
--   mcp_authorization_codes   the single-use code between the consent screen
--                             and the token endpoint.
--   engine_tokens kind 'mcp'  the access token itself.
--
-- WHY THE ACCESS TOKEN IS A FOURTH KIND RATHER THAN A FOURTH TABLE. The same
-- reason 0012 gives for the second and 0025 for the third: the hashing, the
-- prefix, the revocation, the expiry and the policy that lets a bearer token
-- find its own organization are already here and already tested, and a parallel
-- implementation of exactly those is where a subtle difference becomes a
-- security bug.
--
-- WHY IT IS A DISTINCT KIND AND NOT 'cli'. This is the audience check the MCP
-- authorization specification requires, expressed as a column rather than as a
-- promise about the code. `identify()` in auth/device.ts selects kind = 'cli'
-- and the MCP endpoint selects kind = 'mcp', so an `af login` token replayed at
-- /mcp is refused and an MCP token replayed at /v1/whoami is refused, in both
-- directions, by a WHERE clause. Sharing one kind would have made every token
-- this control plane issues valid at every surface it serves, which is exactly
-- the confused-deputy shape RFC 8707 exists to prevent.
--
-- NO REFRESH TOKEN TABLE, DELIBERATELY. The authorization specification says a
-- client MUST NOT assume refresh tokens will be issued and leaves it to the
-- authorization server. This one does not issue them: the access token carries
-- the same ninety day life the device grant's CLI token has, reconnecting is
-- one click in the client that asked, and a rotation ledger is a second place
-- for a replay window to hide. That is a smaller surface, not a smaller
-- feature.

BEGIN;

-- ---------------------------------------------------------------------------
-- Who is asking
-- ---------------------------------------------------------------------------
--
-- No org_id, and it is not an oversight. A client registration belongs to the
-- MCP client rather than to a tenant: one Claude registration is used by
-- everybody in every organization who connects through it, and giving it an
-- owner would mean the second organization to connect could not read the row
-- the first one created.
--
-- What confines the application role instead is the policy below, keyed on a
-- client_id the caller has to already hold. The client_id is 32 bytes of
-- randomness, so declaring one you were not given returns nothing, which is
-- the same shape as the engine token policy in 0004 and the device code policy
-- in 0012.
CREATE TABLE mcp_clients (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- What the client sends as client_id. Opaque and not a secret in OAuth's
  -- model, and still unguessable here, because it is the key the policy below
  -- confines this table by.
  client_id           text NOT NULL UNIQUE,
  -- No client secret column, and that is a decision rather than an omission.
  -- This server registers PUBLIC clients only: the discovery document
  -- advertises token_endpoint_auth_methods_supported as ["none"] and PKCE
  -- S256 is what authenticates the exchange, which is what OAuth 2.1 asks of
  -- a client that cannot keep a secret, and a browser client cannot. A
  -- nullable secret hash nothing verifies is worse than no column: it reads
  -- as support for confidential clients that does not exist. The day one is
  -- supported it arrives with the code that checks it.
  -- What the client called itself. Shown on the consent screen, so it is the
  -- one string here a person reads, and it is written by whoever registered.
  -- Treated as untrusted text everywhere it is rendered.
  client_name         text NOT NULL,
  -- Every redirect URI the client declared. An authorization request naming
  -- anything else is refused: exact string match, no wildcards and no prefix
  -- matching, per OAuth 2.1.
  redirect_uris       text[] NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  -- No last_used_at here either. When a registration was last exercised is
  -- read off the credentials it produced, which carry engine_tokens.last_used_at
  -- and are what the operator page shows. A second copy on this row would be
  -- one more thing to write on every authenticated request and nothing reads it.
  CONSTRAINT mcp_clients_has_a_redirect CHECK (cardinality(redirect_uris) > 0)
);

-- ---------------------------------------------------------------------------
-- The code between the consent screen and the token endpoint
-- ---------------------------------------------------------------------------
--
-- The columns are named approved_user_id and approved_org_id rather than
-- user_id and org_id, following device_authorizations in 0012, and the name is
-- load bearing twice over. It says the tenancy is established by an approval
-- rather than carried in by the request, and it keeps this table out of the
-- plain org_id policy loop, where a policy comparing org_id to current_org()
-- would make the row unreadable at the one moment it has to be read: the token
-- exchange, which has no session and no tenant and holds only the code.
CREATE TABLE mcp_authorization_codes (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- SHA-256 of the code. Same argument as every other credential in this
  -- schema: a database that leaks leaks nothing usable.
  code_hash            bytea NOT NULL UNIQUE,
  client_id            text NOT NULL REFERENCES mcp_clients(client_id) ON DELETE CASCADE,
  -- Recorded at the authorization request and compared again at the token
  -- request, which RFC 6749 requires and which is what stops a code issued for
  -- one registered redirect being redeemed against another.
  redirect_uri         text NOT NULL,
  -- PKCE. NOT NULL, so a code without a challenge cannot be stored at all: an
  -- authorization server that accepts one is an authorization server where
  -- interception of the code is sufficient to steal the grant.
  code_challenge       text NOT NULL,
  code_challenge_method text NOT NULL,
  -- What the token will be allowed to do, decided here and read from here when
  -- the token is minted. Never from the token request, so redemption cannot
  -- widen what approval granted.
  scopes               text[] NOT NULL,
  -- RFC 8707. The resource the client said it wanted the token for, so the
  -- token endpoint can refuse a code being exchanged for a token aimed
  -- somewhere else.
  resource             text,
  approved_user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  approved_org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  created_at           timestamptz NOT NULL DEFAULT now(),
  expires_at           timestamptz NOT NULL,
  -- Single use. A code that could be redeemed twice mints a second token from
  -- anywhere it has ever been, including a browser history and a proxy log.
  redeemed_at          timestamptz,
  CONSTRAINT mcp_codes_pkce_is_s256 CHECK (code_challenge_method = 'S256')
);

CREATE INDEX mcp_codes_expiry_idx ON mcp_authorization_codes (expires_at);

-- ---------------------------------------------------------------------------
-- The access token
-- ---------------------------------------------------------------------------

ALTER TABLE engine_tokens DROP CONSTRAINT engine_tokens_kind;
ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_kind CHECK (kind IN ('engine', 'cli', 'oidc', 'mcp'));

-- Which registration earned it, so that deleting a client reaches the
-- credentials it produced. Without this a deleted client would stop being able
-- to obtain new tokens while the ones already issued kept working, which is the
-- shape of a revocation that does not revoke.
ALTER TABLE engine_tokens
  ADD COLUMN mcp_client_id text REFERENCES mcp_clients(client_id) ON DELETE CASCADE;
ALTER TABLE engine_tokens ADD COLUMN mcp_resource text;
ALTER TABLE engine_tokens ADD CONSTRAINT engine_tokens_mcp_has_resource
  CHECK (kind <> 'mcp' OR mcp_resource IS NOT NULL);

-- An MCP token is a person, so it carries one, exactly as a 'cli' token does.
ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_mcp_has_a_user CHECK (kind <> 'mcp' OR user_id IS NOT NULL);

ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_mcp_has_a_client CHECK (kind <> 'mcp' OR mcp_client_id IS NOT NULL);

-- Short lived as a property of the schema rather than as a promise about the
-- code, the same reason 0025 gives for the OIDC kind. A future caller that
-- forgets to pass an expiry cannot mint an immortal credential from a consent
-- screen: the INSERT fails.
ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_mcp_expires CHECK (kind <> 'mcp' OR expires_at IS NOT NULL);

CREATE INDEX engine_tokens_mcp_client_idx ON engine_tokens (mcp_client_id)
  WHERE mcp_client_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Grants and policies
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON mcp_clients, mcp_authorization_codes
  TO antifailure_app;
GRANT SELECT, DELETE ON mcp_clients, mcp_authorization_codes TO antifailure_admin;

-- The settings these policies read. Declared by the caller in withoutTenant,
-- and each one is a value the caller has to already hold, so declaring one it
-- was not given returns no rows rather than somebody else's.
CREATE OR REPLACE FUNCTION current_mcp_client_id() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.mcp_client_id', true), '') $$;

CREATE OR REPLACE FUNCTION current_mcp_code_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.mcp_code_hash', true), ''), 'hex') $$;

ALTER TABLE mcp_clients ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_clients FORCE ROW LEVEL SECURITY;

-- Both halves compare against the declared value, following device_poll in
-- 0012 rather than allowing an unconstrained WITH CHECK. Registration mints the
-- client_id in the application and declares it before the INSERT, so it has the
-- value to declare; a policy that let the write through unchecked would let a
-- statement anywhere in this server create a client row under an id it does not
-- hold, which is a registration nobody consented to.
CREATE POLICY presented_client ON mcp_clients
  FOR ALL TO antifailure_app
  USING (client_id = current_mcp_client_id())
  WITH CHECK (client_id = current_mcp_client_id());

ALTER TABLE mcp_authorization_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_authorization_codes FORCE ROW LEVEL SECURITY;

-- Same shape. The consent screen generates the code, declares its hash, and
-- then inserts; redemption declares the hash it was handed and reaches that row
-- and nothing else.
CREATE POLICY presented_code ON mcp_authorization_codes
  FOR ALL TO antifailure_app
  USING (code_hash = current_mcp_code_hash())
  WITH CHECK (code_hash = current_mcp_code_hash());

-- Enforce the audience split in the database too. Rolling the application back
-- must not turn an MCP credential into an engine ingestion credential.
ALTER POLICY presented_token ON engine_tokens
  USING (kind <> 'mcp' AND token_hash = current_engine_token_hash())
  WITH CHECK (kind <> 'mcp' AND token_hash = current_engine_token_hash());

CREATE FUNCTION current_mcp_token_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.mcp_token_hash', true), ''), 'hex') $$;

CREATE POLICY presented_mcp_token ON engine_tokens
  FOR SELECT TO antifailure_app
  USING (kind = 'mcp' AND token_hash = current_mcp_token_hash());

COMMIT;
