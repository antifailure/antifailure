-- The audit log the control plane forwarded nowhere.
--
-- THE FAILURE THIS EXISTS FOR, and it is a compliance control that reported
-- itself as held while holding nothing.
--
-- `ee/README.md` sold "SIEM streaming with a tamper evident hash chain". Both
-- halves were real and neither was joined to the other. The hash chain is
-- `audit_entries.prev_hash` and `audit_entries.entry_hash`, written by the
-- control plane in 0001. The streaming is `ee/engine/auditsink`, which forwards
-- five ENGINE actions from a machine that has no database connection. So single
-- sign on logins, directory provisioning, operator impersonation and every
-- admin action were written into `audit_entries` and forwarded to nothing at
-- all, and the sentence that said otherwise could point at a real half whenever
-- it was questioned.
--
-- The forwarder that carries the chain to a sink already existed, in
-- `ee/web/audit`, with a bounded queue, four sinks and signed batch manifests
-- over the chain head. Nothing imported it. It was not a declared dependency of
-- any package in the enterprise workspace, so it could not be imported without
-- a package.json change, while `npm --prefix ee/web test` ran its suite green on
-- every pull request. Tested, green and unreachable is the most convincing
-- disguise dead code can wear.
--
-- What was missing from the database, and is here, is the two rows of state
-- that let a poll loop say where it got to.
--
-- ---------------------------------------------------------------------------
-- THE PROBLEM THIS FILE IS MOSTLY ABOUT: forwarding is a cross tenant read
-- ---------------------------------------------------------------------------
--
-- Every policy on `audit_entries` today keys on the tenant, and that is right
-- for every reader it has: a customer reads their own audit log. A forwarder
-- does not. An operator who has configured a SIEM has asked for EVERY
-- organization's privileged actions, in one stream, in sequence order, because
-- that is what a security team's alerts run against. There is no tenant to
-- scope the read to and pretending otherwise would mean the feature cannot
-- exist.
--
-- Postgres ORs permissive policies, so a policy whose USING clause names no
-- tenant WIDENS every other policy on the table rather than narrowing anything.
-- 0021 records the cross tenant suite catching exactly that: "bob can read 1 of
-- alice's teardown_requests rows". So the widening has to be confined to the
-- one caller that needs it, and the mechanism is the one 0021, 0013 and 0014
-- already use: the caller declares a value, and the policy is false for
-- everybody who has not declared it.
--
-- `antifailure.audit_forwarder` is that declaration. It is not a credential and
-- it does not pretend to be one: it is a boolean, and what makes it believable
-- is that only `Pool.withAuditForwarder` sets it and every other scope in
-- client.ts clears it by name, which admin.test.ts checks by reading the source
-- rather than by trusting the comment.
--
-- WHY NOT `current_sweeper()`, WHICH ALREADY EXISTS AND IS THE SAME SHAPE. It
-- is entered by the session sweep, the device sweep, the sign in state sweep
-- and the pull request lifecycle sweep. Keying the audit read on it would hand
-- every one of those the whole audit log of every tenant to buy one fewer
-- setting. A declaration that is reused is a declaration that grants more than
-- the thing that asked for it.
--
-- ---------------------------------------------------------------------------
-- The second decision: one cursor, not one per organization
-- ---------------------------------------------------------------------------
--
-- `audit_entries.seq` comes from ONE sequence for the whole installation, not
-- one per tenant, so a single number is a complete description of where the
-- forwarder got to. A cursor per organization would need the list of
-- organizations before it could read anything, which is a second cross tenant
-- read to avoid a number.
--
-- The cursor advances past an entry that was deliberately NOT forwarded, which
-- is the unlicensed case, and that is a decision rather than an oversight. It
-- is the behaviour `ee/engine/auditsink` already has, written down on
-- docs/enterprise/audit-stream.md as "accepts every entry and writes none". It
-- is also forced by the cursor being global: one organization that never buys
-- audit streaming would otherwise stall the stream for every organization that
-- did, forever.
--
-- The cursor is advanced only past entries a sink ACCEPTED, so a sink that is
-- down costs forwarding lag and never an entry. Delivery is therefore at least
-- once and a duplicate is detectable by `seq`, which is the trade every audit
-- forwarder has to pick a side of, and losing an entry is the wrong side.

BEGIN;

-- ---------------------------------------------------------------------------
-- The declaration
-- ---------------------------------------------------------------------------

-- Declared by the audit forwarder and by nothing else.
--
-- The same shape as current_sweeper() and current_github_account(): the value
-- is not a secret, and what makes it believable is that one scope sets it and
-- every other scope clears it. `true` for the third argument so that a
-- connection which has never set it reads the empty string rather than raising,
-- which is what makes the policies below deny for every ordinary request.
CREATE OR REPLACE FUNCTION current_audit_forwarder() RETURNS boolean
  LANGUAGE sql STABLE
  AS $$ SELECT coalesce(current_setting('antifailure.audit_forwarder', true), '') = 'on' $$;

-- ---------------------------------------------------------------------------
-- Where the forwarder got to
-- ---------------------------------------------------------------------------

-- One row, the same shape and for the same reason as analytics_rollup_state: a
-- forwarder that has never run and one that ran and found nothing are different
-- states, and a table with no row cannot tell them apart.
CREATE TABLE audit_stream_cursor (
  id              boolean PRIMARY KEY DEFAULT true,
  -- The highest audit_entries.seq a sink has accepted. Zero means nothing has
  -- been forwarded yet, which is the state a fresh installation is in and is
  -- not the same as "up to date".
  delivered_seq   bigint      NOT NULL DEFAULT 0,
  -- When the cursor last moved. Read by nothing today and written by the
  -- forwarder, so that an operator asking "is the stream stuck" has an answer
  -- that does not require reading the sink's own logs.
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT audit_stream_cursor_is_one_row CHECK (id)
);

INSERT INTO audit_stream_cursor (id) VALUES (true);

-- ---------------------------------------------------------------------------
-- Grants
--
-- SELECT and UPDATE, and no INSERT and no DELETE, so the application role
-- cannot create a second cursor row or remove the one there is. The row is
-- created here, once, by the migration role.
--
-- The operator credential gets SELECT so the administrative portal can show how
-- far behind the stream is, and no UPDATE: rewinding the cursor would republish
-- an organization's audit history to a sink, which is a decision nobody makes
-- from a portal by accident.
-- ---------------------------------------------------------------------------

GRANT SELECT, UPDATE ON audit_stream_cursor TO antifailure_app;
GRANT SELECT ON audit_stream_cursor TO antifailure_admin;

-- ---------------------------------------------------------------------------
-- Isolation
--
-- The cursor carries no org_id because it belongs to the installation rather
-- than to any tenant, the same classification platform_controls and
-- analytics_rollup_state carry. What confines it is the declaration: a request
-- that arrived from outside has not set it, so both policies are false and the
-- table reads as empty rather than as an error.
-- ---------------------------------------------------------------------------

ALTER TABLE audit_stream_cursor ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_stream_cursor FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_stream_cursor_is_read_by_the_forwarder ON audit_stream_cursor
  FOR SELECT TO antifailure_app
  USING (current_audit_forwarder());

-- Defence in depth that no tenant facing test can exercise, and that is
-- recorded rather than implied. Widening this one to USING (true) leaves the
-- cross tenant suite GREEN, established by mutation and not by reading: an
-- UPDATE has to satisfy the SELECT policies as well, so a caller that cannot
-- see the row cannot update it whatever this says. What actually holds the row
-- is the SELECT policy above and the grant above that. This is here so a
-- widening of the SELECT policy is not a widening of both at once.
CREATE POLICY audit_stream_cursor_is_advanced_by_the_forwarder ON audit_stream_cursor
  FOR UPDATE TO antifailure_app
  USING (current_audit_forwarder())
  WITH CHECK (current_audit_forwarder());

-- ---------------------------------------------------------------------------
-- What the forwarder may read
--
-- SELECT and nothing else. The forwarder copies; it must never be able to
-- write, amend or remove an entry, and 0002 already revokes UPDATE, DELETE and
-- TRUNCATE on this table from the application role, so this adds a read and
-- cannot add anything else.
--
-- This is the widest policy in the schema keyed on a declaration, and saying so
-- here is the point: it reads every organization's audit log. That is what an
-- audit stream IS, it is bounded by the one scope that can declare it, and the
-- cross tenant suite proves an ordinary tenant connection still reaches
-- nobody's rows but its own.
-- ---------------------------------------------------------------------------

CREATE POLICY audit_entries_are_forwarded ON audit_entries
  FOR SELECT TO antifailure_app
  USING (current_audit_forwarder());

COMMIT;
