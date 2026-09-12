-- The sink a hosted customer could not choose.
--
-- THE FAILURE THIS EXISTS FOR, and it is one layer along from 0043's.
--
-- 0043 joined the audit chain to a sink and the forwarder that carries it. The
-- destination it carries to is read from AF_AUDIT_STREAM_SINK and its siblings,
-- which is one destination per PROCESS. That is exactly right for a self hosted
-- installation, where the operator and the customer are the same person and
-- their collector is the only collector there is.
--
-- It is wrong for the hosted control plane, and `web/apps/api/src/entitlements.ts`
-- sells it anyway: `audit_stream` is true on the hosted enterprise plan, so an
-- organization is told its audit log can reach its own security information and
-- event management system, and there was no way for that organization to say
-- which one. Every entitled organization would have received the operator's
-- collector and nothing else. A control that names the wrong destination is not
-- a weaker control, it is somebody else's log.
--
-- So a destination belongs to an organization, and this is the table it belongs
-- to. One per organization: a second would be ambiguity about which delivery a
-- position had recorded, and the honest way to reach two collectors is one
-- collector that fans out after receiving.
--
-- ---------------------------------------------------------------------------
-- The credential, and why it is not stored anywhere new
-- ---------------------------------------------------------------------------
--
-- A Splunk token, an Event Hubs shared access signature and a webhook secret
-- are the customer's, and the threat model is the one 0012 already wrote down
-- for `provider_keys`: it is not an attacker holding a database dump, it is us.
-- The value must not be readable by anything that renders a page, appears in an
-- event, or is written to a log, and the only code that ever holds the plaintext
-- is the code that puts it in a request header.
--
-- So the shape here is `provider_keys`, deliberately and column for column:
-- ciphertext and nonce under AES-256-GCM, a key version so the sealing key can
-- be rotated without a migration, a fingerprint so a rotation can prove the new
-- value differs from the old one without either being displayed, and a last four
-- so somebody can confirm they pasted the credential they meant to. The sealing
-- key is `AF_PROVIDER_KEY_SECRET`, read from the environment and never present
-- in Postgres, so a database dump on its own decrypts nothing.
--
-- The associated data binds each ciphertext to the organization AND to the kind
-- of destination it was sealed for. Without it a ciphertext is a portable blob,
-- and anybody who can write a row can copy another tenant's sealed credential
-- into their own destination and have this control plane decrypt it for them by
-- delivering to a collector they chose. That is the attack the binding exists to
-- stop and it is why the additional data is not optional.
--
-- ---------------------------------------------------------------------------
-- Two readers, one table, and they are not the same reader
-- ---------------------------------------------------------------------------
--
-- The tenant reads and writes its own row, under the ordinary policy every
-- other organization owned table carries. The forwarder reads EVERY row,
-- because that is what forwarding is, and it reads them under the declaration
-- 0043 introduced: `current_audit_forwarder()`, which only
-- `Pool.withAuditForwarder` sets and which every other scope in client.ts
-- clears by name.
--
-- The forwarder gets SELECT and nothing else, and that asymmetry is the point.
-- Postgres has no column level row security, so a forwarder with UPDATE on this
-- table would be a cross tenant write path over a customer's destination: a bug
-- in a poll loop could point one organization's audit log at another
-- organization's collector. It cannot, because it holds no policy that permits
-- an UPDATE, an INSERT or a DELETE here. Delivery bookkeeping lives on
-- `audit_stream_positions`, which the forwarder does write, and which carries no
-- destination.
--
-- ---------------------------------------------------------------------------
-- Where delivery starts, which is a decision and not a default
-- ---------------------------------------------------------------------------
--
-- `from_seq` is the sequence number the organization's audit log had reached at
-- the moment the destination was saved, so delivery begins with the entries
-- written after that. Two orderings make this load bearing rather than tidy.
--
-- Entries arrive and THEN a destination is configured: without `from_seq` the
-- first pass would read from the organization's position, which for an
-- installation whose forwarder has never run is zero, and stream the entire
-- audit history into a collector somebody configured a moment ago. Six months
-- of entries arriving as a surprise is not the behaviour anybody asked for.
--
-- A destination is configured and THEN entries arrive: this is the ordinary
-- case, and it works with no event at all. The row is written with `from_seq`
-- already set, so the next pass of a forwarder that was already running picks it
-- up. Nothing waits for a notification that may have fired before the row
-- existed, because nothing notifies.
--
-- The configuration change is itself an audit entry, and it is written AFTER
-- `from_seq` is read, so the first thing a new destination receives is the
-- record of its own creation. That is what makes "did my collector work" a
-- question the next pass answers rather than a test button with an outbound
-- request behind it.
--
-- ---------------------------------------------------------------------------
-- What a customer may name, which is narrower than what an operator may
-- ---------------------------------------------------------------------------
--
-- `validateDestination` in sinks.ts requires HTTPS without URL credentials and
-- permits HTTP to a loopback address, because an operator running a collector
-- beside their own control plane is the case it was written for.
--
-- A hosted customer's destination is an untrusted string arriving from outside,
-- and loopback then means THIS control plane's own loopback. So the check
-- applied to a customer supplied destination is a stricter one, in
-- `destinations.ts`, and the constraint below is the database's half of it:
-- HTTPS only, so every plaintext service inside this deployment including the
-- control plane's own port is unreachable by construction, and no URL
-- credentials. The address rules that cannot be expressed as a CHECK live in the
-- validator, and what neither can see is a public hostname whose DNS resolves
-- into a private network. That is named as a limit in
-- docs/enterprise/audit-stream.md rather than implied by its absence.

BEGIN;

CREATE TABLE audit_stream_destinations (
  -- One per organization, so the primary key is the organization. A second row
  -- would make "where did sequence 41 go" a question with two answers and
  -- `audit_stream_positions` records one.
  org_id          uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,

  -- Which collector protocol. The same three names `configure.ts` accepts from
  -- an operator, and for the same reason: the object store sink needs a signer
  -- this side of the product does not carry, so a name accepted here that then
  -- wrote nowhere would be the identical failure one layer along.
  kind            text NOT NULL,
  url             text NOT NULL,

  -- Splunk's own index and sourcetype, so entries land where the customer's
  -- existing searches already look rather than in main. Null for the other two,
  -- enforced below, because a value nobody reads is a value somebody will
  -- eventually believe is being used.
  index_name      text,
  sourcetype      text,

  -- The sealed credential. See the argument above.
  ciphertext      bytea NOT NULL,
  nonce           bytea NOT NULL,
  key_version     text NOT NULL DEFAULT 'v1',
  fingerprint     text NOT NULL,
  last4           text NOT NULL,

  -- Whether entries go there right now. A switch rather than a delete, so
  -- turning the stream off and on again does not lose the URL and the
  -- credential, and so an organization can stop delivery without its
  -- destination becoming something somebody has to remember.
  --
  -- A disabled row still decides for its organization: it does NOT fall back to
  -- the installation sink. An organization that turned its stream off did not
  -- ask for its entries to go somewhere else instead.
  enabled         boolean NOT NULL DEFAULT true,

  -- Where delivery starts. See the argument above.
  from_seq        bigint NOT NULL DEFAULT 0 CHECK (from_seq >= 0),

  created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT audit_stream_destinations_kind
    CHECK (kind IN ('splunk', 'event_hubs', 'webhook')),

  -- Four characters, exactly as provider_keys. Fewer would be a value that
  -- identifies nothing and more would be a piece of the credential.
  CONSTRAINT audit_stream_destinations_last4 CHECK (char_length(last4) = 4),

  CONSTRAINT audit_stream_destinations_splunk_fields_are_splunk_only
    CHECK (kind = 'splunk' OR (index_name IS NULL AND sourcetype IS NULL)),

  -- The database's half of the customer destination rule. The validator refuses
  -- more than this; this refuses what no code path may write whatever the
  -- validator does, which is a plaintext destination or one carrying
  -- credentials in the URL where every proxy log on the way would keep them.
  --
  -- The `@` test reads the AUTHORITY alone, which is everything after the
  -- scheme up to the first slash, rather than the whole string: an `@` in a path
  -- or a query string is ordinary and refusing it would refuse real collector
  -- URLs, while an `@` in the authority is userinfo and is the only place it
  -- carries a credential.
  CONSTRAINT audit_stream_destinations_url_is_https
    CHECK (url LIKE 'https://%'
           AND split_part(substring(url FROM 9), '/', 1) NOT LIKE '%@%'),

  CONSTRAINT audit_stream_destinations_ciphertext_is_sealed
    CHECK (octet_length(ciphertext) > 16 AND octet_length(nonce) > 0)
);

-- ---------------------------------------------------------------------------
-- Delivery state a customer can actually see
-- ---------------------------------------------------------------------------
--
-- A compliance control that stops working silently is the defect this whole
-- feature exists downstream of, and a customer supplied credential is revoked
-- by the customer's own system without telling us. A collector answering 401
-- makes a PermanentError, which the queue correctly drops rather than retrying
-- forever, and before these columns the only record of that was a log line in
-- the operator's container.
--
-- So the forwarder records what happened per organization, and the organization
-- can read its own row. On `audit_stream_positions` rather than on the
-- destination, because the forwarder must not hold an UPDATE on a customer's
-- destination; see the argument above.

ALTER TABLE audit_stream_positions
  ADD COLUMN last_attempt_at       timestamptz,
  ADD COLUMN last_delivered_at     timestamptz,
  -- The sink's own words about the last failure, and null once a delivery
  -- succeeds. `send` in sinks.ts never reads a collector's response body, so
  -- what lands here is a status code and a transport message and never anything
  -- a collector chose to echo back.
  ADD COLUMN last_error            text,
  ADD COLUMN consecutive_failures  integer NOT NULL DEFAULT 0
    CHECK (consecutive_failures >= 0);

-- The tenant reads its own delivery state, and that is a widening of 0043,
-- recorded here rather than left for somebody to notice. 0043 gave this table
-- no tenant policy at all, so an organization could not tell whether its stream
-- was working; with a destination it configured itself, that is no longer an
-- operator's question. SELECT only. Advancing a position would republish an
-- organization's history to its own collector, and the forwarder is the only
-- thing that writes here.
CREATE POLICY audit_stream_positions_are_read_by_their_tenant ON audit_stream_positions
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

-- ---------------------------------------------------------------------------
-- Grants
-- ---------------------------------------------------------------------------

-- The tenant's four verbs, because an organization creates, amends, disables and
-- removes its own destination.
GRANT SELECT, INSERT, UPDATE, DELETE ON audit_stream_destinations TO antifailure_app;

-- The operator credential reads, so the portal can answer "is this customer's
-- stream configured and is it failing" without anybody opening a database
-- session. It holds BYPASSRLS, so this grant is what bounds it rather than a
-- policy, and it is SELECT alone: an operator does not change a customer's
-- destination from a portal, and a support engineer who needs the credential
-- cannot have it, because the plaintext is not in this database at all.
GRANT SELECT ON audit_stream_destinations TO antifailure_admin;

-- ---------------------------------------------------------------------------
-- Isolation
-- ---------------------------------------------------------------------------

ALTER TABLE audit_stream_destinations ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_stream_destinations FORCE ROW LEVEL SECURITY;

-- The ordinary tenant policy, the same one provider_keys carries, and it is
-- what keeps one organization's sealed credential and chosen URL away from
-- every other organization.
CREATE POLICY tenant_isolation ON audit_stream_destinations
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

-- The forwarder's read, and only its read. Keyed on the declaration, so it is
-- false for every request that arrived from outside, which is what stops a
-- policy naming no tenant from widening this table for everybody. SELECT and
-- nothing else, so the poll loop cannot write a destination it is only supposed
-- to deliver to.
CREATE POLICY audit_stream_destinations_are_read_by_the_forwarder ON audit_stream_destinations
  FOR SELECT TO antifailure_app
  USING (current_audit_forwarder());

COMMIT;
