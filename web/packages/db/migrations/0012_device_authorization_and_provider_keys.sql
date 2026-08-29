-- Signing in from a terminal, and holding somebody else's provider key.
--
-- Two features, one migration, because they arrived together and share the
-- pattern that decides both of their policies: a row that has no tenant until
-- somebody approves it, and a row whose whole value is a secret the database
-- must never hand back.
--
-- ---------------------------------------------------------------------------
-- Device authorization (RFC 8628)
-- ---------------------------------------------------------------------------
--
-- A terminal has no browser and no cookie. It asks for a pair of codes, prints
-- the short one, and polls with the long one while a person approves it in a
-- browser that does have a session.
--
-- The awkward part is the same one the session and engine-token lookups have:
-- the polling request has no organization and no user, so it cannot be scoped
-- by current_org(), and the tempting fix -- a policy that opens up when nothing
-- is set -- is the whole table, because "nothing is set" is exactly the
-- unauthenticated request. So the caller declares the secret it already holds
-- and the policy returns that row and nothing else. There are two such secrets
-- here because there are two callers: the terminal holds the device code, and
-- the browser holds the user code the person typed in.
--
-- The user code is short enough to be typed, which means it is short enough to
-- be guessed. Three things stop that being a hole and all three are required:
-- the alphabet excludes the characters people confuse (no O/0, no I/1), the row
-- expires in fifteen minutes, and POST /auth/device/approve carries a rate
-- limit in the endpoint table. A guess also has to arrive while a real one is
-- outstanding.

BEGIN;

CREATE TABLE device_authorizations (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Hashed for the same reason the session token is: a database backup, a log
  -- of a slow query, or a replica should not contain anything that can be
  -- replayed. 256 bits of randomness needs no slow hash.
  device_code_hash  bytea NOT NULL UNIQUE,
  -- Not hashed. It is displayed to the person approving so they can check it
  -- matches their terminal, which means the server has to be able to show it.
  user_code         text NOT NULL UNIQUE,
  -- What the terminal asked for, recorded before anyone approves. Approval
  -- cannot widen it: the token is minted from this column, never from anything
  -- in the approve request.
  scopes            text[] NOT NULL,
  -- Shown on the approval screen. A person approving a terminal should be told
  -- which machine is asking.
  client_label      text NOT NULL,
  approved_at       timestamptz,
  approved_user_id  uuid REFERENCES users(id) ON DELETE CASCADE,
  approved_org_id   uuid REFERENCES organizations(id) ON DELETE CASCADE,
  denied_at         timestamptz,
  -- Single use. Without this a device code that was polled once could be
  -- polled again from anywhere and mint a second token.
  redeemed_at       timestamptz,
  -- For slow_down. RFC 8628 says a client polling faster than the interval
  -- gets told to back off rather than being refused outright.
  last_polled_at    timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  expires_at        timestamptz NOT NULL,

  -- Approved means approved by somebody, for some organization. A row with an
  -- approval timestamp and no user is a row that would mint an unattributable
  -- token, so it cannot exist.
  CONSTRAINT device_approval_is_complete CHECK (
    (approved_at IS NULL AND approved_user_id IS NULL AND approved_org_id IS NULL)
    OR (approved_at IS NOT NULL AND approved_user_id IS NOT NULL AND approved_org_id IS NOT NULL)
  ),
  -- Approved and denied are exclusive. Both set is a state no code reads, and
  -- an unread state is one that quietly resolves whichever way the query
  -- happens to check first.
  CONSTRAINT device_not_both CHECK (approved_at IS NULL OR denied_at IS NULL)
);

CREATE INDEX device_authorizations_expiry_idx ON device_authorizations (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON device_authorizations TO antifailure_app;

CREATE OR REPLACE FUNCTION current_device_code_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.device_code_hash', true), ''), 'hex') $$;

CREATE OR REPLACE FUNCTION current_device_user_code() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.device_user_code', true), '') $$;

ALTER TABLE device_authorizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_authorizations FORCE ROW LEVEL SECURITY;

-- Creating one. There is no tenant and no user yet by definition, and the row
-- carries nothing but randomness until it is approved.
CREATE POLICY device_request ON device_authorizations
  FOR INSERT TO antifailure_app
  WITH CHECK (device_code_hash = current_device_code_hash());

-- The terminal, polling. It holds the device code.
CREATE POLICY device_poll ON device_authorizations
  FOR ALL TO antifailure_app
  USING (device_code_hash = current_device_code_hash())
  WITH CHECK (device_code_hash = current_device_code_hash());

-- The browser, approving. It holds the user code, because a person read it off
-- a terminal and typed it in.
CREATE POLICY device_approve ON device_authorizations
  FOR ALL TO antifailure_app
  USING (user_code = current_device_user_code())
  WITH CHECK (user_code = current_device_user_code());

-- ---------------------------------------------------------------------------
-- Tokens that belong to a person rather than to a machine
-- ---------------------------------------------------------------------------
--
-- Reusing engine_tokens rather than adding a second token table. The hashing,
-- the prefix, the revocation, the last-used stamp and the row-level policy that
-- lets a bearer token find its own organization are all already here and all
-- already tested. A parallel table would be a second implementation of the one
-- thing in this schema where a subtle difference is a security bug.
--
-- kind distinguishes them so that a listing can say which is which, and so that
-- revoking every machine token does not sign every human out.

ALTER TABLE engine_tokens
  ADD COLUMN kind text NOT NULL DEFAULT 'engine',
  -- Who it acts as. Null for an engine token: a machine is not a person, and
  -- pretending otherwise puts a machine's actions in a human's audit trail.
  ADD COLUMN user_id uuid REFERENCES users(id) ON DELETE CASCADE,
  ADD COLUMN scopes text[] NOT NULL DEFAULT '{}',
  -- Null never expires, which is what an engine token in CI needs. A token
  -- minted for a laptop gets a date.
  ADD COLUMN expires_at timestamptz;

ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_kind CHECK (kind IN ('engine', 'cli')),
  -- A cli token with no user is the failure this constraint exists to prevent:
  -- it would authenticate, act, and be attributed to nobody.
  ADD CONSTRAINT engine_tokens_cli_has_a_user CHECK (kind <> 'cli' OR user_id IS NOT NULL);

CREATE INDEX engine_tokens_user_idx ON engine_tokens (org_id, user_id) WHERE user_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Bring your own key
-- ---------------------------------------------------------------------------
--
-- A customer's Anthropic or OpenAI key. The threat model is not an attacker
-- with a database dump; it is us. A key here must not be readable by anything
-- that renders a page, appears in an event, or is written to a log, and the
-- only code that ever holds the plaintext is the code that hands it to the
-- provider.
--
-- So the column is ciphertext, sealed with AES-256-GCM under a key that lives
-- in Key Vault and never in Postgres. A database dump on its own decrypts
-- nothing. The three columns beside it -- fingerprint, last four, provider --
-- are everything the console needs to render, so no screen has a reason to ask
-- for the secret.

CREATE TABLE provider_keys (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider      text NOT NULL,
  -- The sealed key, and the nonce it was sealed with. Separate columns because
  -- a nonce is not a secret and concatenating them into one opaque blob is how
  -- a format ends up undocumented.
  ciphertext    bytea NOT NULL,
  nonce         bytea NOT NULL,
  -- Which sealing key was used, so a rotation of the sealing key itself can
  -- find the rows that still need re-sealing.
  key_version   text NOT NULL DEFAULT 'v1',
  -- SHA-256 of the plaintext, truncated. Lets rotation prove the new key is
  -- actually different from the old one without either being displayed.
  fingerprint   text NOT NULL,
  -- The last four characters, which is what every provider's own console shows
  -- and is what lets somebody confirm they rotated the key they meant to.
  last4         text NOT NULL,
  created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  rotated_at    timestamptz,
  revoked_at    timestamptz,

  CONSTRAINT provider_keys_provider CHECK (provider IN ('anthropic', 'openai')),
  CONSTRAINT provider_keys_last4 CHECK (char_length(last4) = 4)
);

-- One live key per provider per organization. A second live key is ambiguity
-- about which one a run charged, and that ambiguity is a billing dispute.
CREATE UNIQUE INDEX provider_keys_one_live
  ON provider_keys (org_id, provider) WHERE revoked_at IS NULL;

-- What a key is allowed to spend, and what it has spent.
--
-- The cap is per calendar month and the period is stored rather than computed,
-- so that a month rolling over is a row that does not match rather than an
-- expression that has to agree in three places about what "this month" means.
CREATE TABLE provider_budgets (
  org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  provider      text NOT NULL,
  period        date NOT NULL,
  -- Null is not "unlimited". A budget row exists because somebody set a cap;
  -- an organization with no row for a provider cannot spend on it at all,
  -- which is the safe direction for a key that belongs to somebody else.
  cap_usd       numeric(12, 4) NOT NULL,
  spent_usd     numeric(12, 4) NOT NULL DEFAULT 0,
  updated_at    timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (org_id, provider, period),
  CONSTRAINT provider_budgets_provider CHECK (provider IN ('anthropic', 'openai')),
  CONSTRAINT provider_budgets_cap_is_positive CHECK (cap_usd >= 0),
  CONSTRAINT provider_budgets_spend_is_positive CHECK (spent_usd >= 0)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON provider_keys, provider_budgets TO antifailure_app;

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['provider_keys', 'provider_budgets']
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

COMMIT;
