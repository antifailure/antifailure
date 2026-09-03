-- Somebody asking to buy, written down where a person can find it.
--
-- WHAT THIS REPLACES, because the failure it replaces is instructive and it was
-- not a bug in any file.
--
-- The site's only commercial route was a waitlist. It posted an address to an
-- Azure Function, which wrote one row into a Cosmos DB table. Nothing in the
-- repository read that table back, deliberately: an anonymous endpoint that can
-- enumerate its own signups is how a list becomes a leaked mailing list. And
-- nothing mailed anybody, because antifailure.dev publishes no mail exchanger,
-- an SPF policy of `v=spf1 -all` and a DMARC policy of reject, so the domain
-- authorizes no sender at all.
--
-- The result was a store that only somebody with the Azure subscription could
-- read and that nothing would ever prompt them to. Every one of those decisions
-- was defensible on its own. Together they made a form whose entire behaviour
-- was to consume what somebody typed.
--
-- So the three properties this table is arranged around are each an answer to
-- one of those:
--
-- IT IS IN THE PRODUCT'S OWN DATABASE, which is backed up, restored, drilled
-- and audited, rather than in a store beside it that nothing here knows about.
--
-- IT HAS A READER. `af-control-plane-backup leads` lists it through the same
-- privileged connection break-glass uses, so the list is reachable by whoever
-- runs the control plane without an Azure console and without a bespoke client.
-- A row nobody can read is a row that did not need writing.
--
-- IT HAS A STATE. `handled_at` and `handled_note` are what turn a list into a
-- queue. A lead nobody has answered and a lead somebody answered last week look
-- identical without them, and the practical consequence of that is answering
-- one person twice and another never.
--
-- ---------------------------------------------------------------------------
-- WHY IT IS WRITE ONLY FOR THE APPLICATION
-- ---------------------------------------------------------------------------
--
-- `antifailure_app` gets INSERT and nothing else. Not SELECT, not UPDATE, not
-- DELETE, and there is no policy that would let it read a row back even by
-- guessing an id.
--
-- The endpoint that writes here is anonymous by necessity: somebody asking to
-- buy has no account yet, and requiring one to ask would be the waitlist's
-- shape again. An anonymous endpoint on a role that could also read this table
-- is one query bug away from publishing the name, company and address of every
-- prospect. Insert-only is not a precaution here, it is the whole boundary, and
-- it is the property that lets the endpoint stay anonymous safely.
--
-- Reading happens through `antifailure_admin`, which holds BYPASSRLS, or
-- through the migration role. Both are credentials the serving process does not
-- have and cannot acquire, which is the same boundary migration 0029 draws for
-- the operator portal and for the same reason.

BEGIN;

CREATE TABLE enterprise_leads (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- Everything a person typed. Bounded by CHECK rather than by varchar(n),
  -- because a length limit that a caller can hit has to produce a message the
  -- caller can act on, and the application validates first; these are the
  -- backstop that keeps a bug from writing a megabyte.
  --
  -- `email` is NOT unique and NOT normalised into an identity. It is a way to
  -- reply, not a key: the same person asking twice from two companies is two
  -- leads, and deduplicating on the address would silently drop the second
  -- request. `name` and `company` are what somebody typed about themselves and
  -- are never matched against anything.
  email        text NOT NULL,
  name         text NOT NULL,
  company      text NOT NULL,
  -- Optional, because a person who does not know yet must not be stopped by a
  -- required field. Zero is refused rather than stored, so "unknown" has one
  -- representation instead of two.
  seats        integer,
  message      text NOT NULL,

  -- Which page it came from, so that a route producing nothing is visible as a
  -- route producing nothing rather than as an absence.
  source       text NOT NULL,

  -- Kept for exactly one purpose, abuse, and that is why they are here rather
  -- than in a log: a flood of leads is answered by looking at where they came
  -- from, and a log with a retention window shorter than the leads themselves
  -- cannot answer it. Both are nullable, because a request behind a proxy that
  -- strips them is still a real request from a real person.
  ip           inet,
  user_agent   text,

  created_at   timestamptz NOT NULL DEFAULT now(),

  -- The queue. Set when somebody has dealt with it, by the operator who did.
  handled_at   timestamptz,
  handled_by   uuid REFERENCES admin_users(id) ON DELETE SET NULL,
  handled_note text,

  CONSTRAINT enterprise_leads_email_shape CHECK (
    email = lower(email) AND length(email) BETWEEN 3 AND 320 AND position('@' IN email) > 1
  ),
  CONSTRAINT enterprise_leads_name_bounded CHECK (length(name) BETWEEN 1 AND 200),
  CONSTRAINT enterprise_leads_company_bounded CHECK (length(company) BETWEEN 1 AND 200),
  CONSTRAINT enterprise_leads_message_bounded CHECK (length(message) BETWEEN 1 AND 4000),
  CONSTRAINT enterprise_leads_source_bounded CHECK (length(source) BETWEEN 1 AND 64),
  CONSTRAINT enterprise_leads_seats_positive CHECK (seats IS NULL OR seats > 0),
  -- A note without a time would be a decision nobody can date, and a time
  -- without an operator would be a decision nobody made.
  CONSTRAINT enterprise_leads_handled_whole CHECK (
    (handled_at IS NULL) = (handled_by IS NULL)
  )
);

-- The order the queue is read in: what is unanswered, oldest first.
--
-- A partial index rather than one over the whole table, because the query that
-- matters asks for the rows where handled_at IS NULL, and that set stays small
-- while the table only grows.
CREATE INDEX enterprise_leads_unhandled
  ON enterprise_leads (created_at)
  WHERE handled_at IS NULL;

ALTER TABLE enterprise_leads ENABLE ROW LEVEL SECURITY;
ALTER TABLE enterprise_leads FORCE ROW LEVEL SECURITY;

-- Insert, and only insert.
--
-- Two policies rather than one FOR ALL, and the split is the point. A single
-- `FOR ALL ... USING (false) WITH CHECK (true)` would work today and would
-- silently start permitting reads the moment somebody widened the USING clause
-- while thinking about updates. Naming the insert on its own means the read
-- path is not something a future edit can loosen: it does not exist.
CREATE POLICY anybody_may_ask ON enterprise_leads
  FOR INSERT TO antifailure_app
  WITH CHECK (true);

-- Said out loud rather than left implicit. Without a SELECT policy the table is
-- unreadable to this role anyway; with this one, somebody reading the schema
-- can see that it is deliberate and see why beside it.
CREATE POLICY the_application_never_reads_leads ON enterprise_leads
  FOR SELECT TO antifailure_app
  USING (false);

-- No SELECT in the grant either, so the refusal happens at the privilege level
-- before any policy is consulted. Belt and braces on the one table whose rows
-- are other people's contact details.
GRANT INSERT ON enterprise_leads TO antifailure_app;

COMMIT;
