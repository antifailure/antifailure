-- Signing in with a link sent to an address, beside signing in with GitHub.
--
-- GitHub is the right front door for a human at a laptop and it is the wrong
-- one for an environment with no route to the internet. A preview copy of this
-- application cannot reach github.com by design, so a sign-in that redirects
-- there cannot complete, and an application nobody can sign into cannot be
-- exercised by anything: not an agent, not a person reviewing a branch, not a
-- self-hosted installation on an isolated network. That is the case this path
-- exists for, and it is the same case a customer on an air-gapped network has.
--
-- It grants nothing GitHub does not. An address only receives a link when it
-- already belongs to a member of an organization: there is no sign-up here,
-- and a token issued for an address with no account is never issued at all.
--
-- Three properties, and each one is enforced by a statement rather than by a
-- convention:
--
-- Single use. Consuming is an UPDATE with `consumed_at IS NULL` in its own
-- WHERE clause returning the row, so two callbacks racing on one token cannot
-- both find it. A boolean checked in the application and written afterwards is
-- the same bug with two round trips in the middle of it.
--
-- Stored as a hash. The token is in the mail and nowhere else, exactly like a
-- session token, so a leaked backup of this table is a list of hashes.
--
-- Reachable only by presenting it. The row is found by declaring the hash of
-- the token the caller holds, the same mechanism sessions use. A connection
-- that sets the setting to a value it guessed gets nothing back.

BEGIN;

CREATE TABLE email_signin_tokens (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash    bytea NOT NULL,
  -- The address the link was sent to, lowercased. The user is resolved when
  -- the token comes back rather than when it is issued: issuing needs to know
  -- only that an account exists, and reading the row that proves which one
  -- needs a secret the caller does not have yet.
  email         text NOT NULL,
  redirect_to   text,
  ip            inet,
  user_agent    text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  consumed_at   timestamptz
);

CREATE UNIQUE INDEX email_signin_tokens_hash_key ON email_signin_tokens (token_hash);
-- Swept by expiry, and the sweep is housekeeping: expiry is enforced in the
-- statement that consumes, so a sweeper that is behind costs table size only.
CREATE INDEX email_signin_tokens_expiry_idx ON email_signin_tokens (expires_at);

-- The hash of the link token the caller is presenting, in the same shape as
-- current_session_hash(). Empty when nothing was declared, which makes every
-- policy below deny rather than open.
CREATE OR REPLACE FUNCTION current_email_token_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.email_token_hash', true), ''), 'hex') $$;

ALTER TABLE email_signin_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_signin_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY presented_email_token ON email_signin_tokens
  FOR ALL TO antifailure_app
  USING (token_hash = current_email_token_hash())
  WITH CHECK (token_hash = current_email_token_hash());

GRANT SELECT, INSERT, UPDATE, DELETE ON email_signin_tokens TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Deciding whether to send, without being able to read who to.
-- ---------------------------------------------------------------------------
--
-- Issuing a link needs one bit: does this address belong to somebody who is
-- already a member of an organization. It does not need the row, and giving
-- the application role a way to read a user row by address would be a way to
-- enumerate this instance's users from an unauthenticated request.
--
-- So the bit is answered inside the database and nothing else comes back. The
-- function is SECURITY DEFINER because the policies on users and members
-- correctly refuse the application role here, and search_path is pinned
-- because a definer function that resolves its own tables through a caller's
-- search_path is a way to run somebody else's code as the owner.
--
-- The answer is not observable from outside: the HTTP response is identical
-- whether this returns true or false, and the send is started without the
-- response waiting on it, so the two cases do not differ in timing either.
CREATE OR REPLACE FUNCTION email_signin_candidate(addr text) RETURNS boolean
  LANGUAGE sql
  STABLE
  SECURITY DEFINER
  SET search_path = public, pg_temp
  AS $$
    SELECT EXISTS (
      SELECT 1 FROM users u
      JOIN members m ON m.user_id = u.id
      WHERE lower(u.email) = lower(addr)
    )
  $$;

REVOKE ALL ON FUNCTION email_signin_candidate(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION email_signin_candidate(text) TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Completing: the user row, reachable by presenting the token.
-- ---------------------------------------------------------------------------
--
-- The same shape as session_owner in 0003: holding the token is the proof, and
-- it is proof of exactly one row.
--
-- Deliberately no expiry test here, and the reason is worth writing down. A
-- policy that compares against now() reads the database server's wall clock,
-- and every deadline in this application is decided by an injected clock so
-- that the behaviour at the boundary can be asked about in a test rather than
-- waited for. A policy on one clock and a statement on another is two answers
-- to "has this expired", which is how a link that the application refuses is
-- still readable, or the reverse. Expiry is enforced once, in the UPDATE that
-- redeems, against the clock the rest of the application uses.
--
-- What this grants is unchanged either way: one user row, to somebody who
-- already holds 256 bits of randomness that was mailed to that user's address.
CREATE POLICY email_token_owner ON users
  FOR SELECT TO antifailure_app
  USING (
    EXISTS (
      SELECT 1 FROM email_signin_tokens t
      WHERE lower(t.email) = lower(users.email)
        AND t.token_hash = current_email_token_hash()
    )
  );

-- ---------------------------------------------------------------------------
-- Housekeeping.
-- ---------------------------------------------------------------------------
--
-- Expiry and single use are both enforced by the statements that redeem, so
-- this is about table size and nothing else. It has to be a definer function
-- for the same reason the candidate check is: no policy matches a row whose
-- token the caller does not hold, and the alternative would be a policy that
-- lets the application role reach every row, which is the thing being avoided.
-- The cutoff is a parameter rather than now() for the same reason the policy
-- above has no expiry test: the application's deadlines come from one clock,
-- and a sweeper reading a different one removes links that still work.
CREATE OR REPLACE FUNCTION email_signin_sweep(cutoff timestamptz) RETURNS bigint
  LANGUAGE sql
  SECURITY DEFINER
  SET search_path = public, pg_temp
  AS $$
    WITH gone AS (
      DELETE FROM email_signin_tokens
      WHERE expires_at <= cutoff OR consumed_at IS NOT NULL
      RETURNING 1
    ) SELECT count(*) FROM gone
  $$;

REVOKE ALL ON FUNCTION email_signin_sweep(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION email_signin_sweep(timestamptz) TO antifailure_app;

COMMIT;
