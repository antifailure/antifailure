-- Telling a run that was taken away from a run whose engine died.
--
-- WHAT HAPPENED, AND WHY ONLY THIS SIDE CAN FIX IT.
--
-- An engine claims a run and takes a lease on it. The lease is fifteen minutes
-- and a heartbeat extends it, so a runner with bad connectivity can miss
-- several in a row and keep the run. If it misses enough of them the lease
-- expires and a SECOND engine polling the same environment may claim the run
-- and start doing the work.
--
-- The first engine used to keep talking. Its terminal event ended the run,
-- because the statement that ends a run was gated on the run's STATE and not on
-- who was holding it, and the second engine's report then arrived against a row
-- that was already terminal and was refused as a note. The measurements of the
-- engine that actually did the work were destroyed by the engine that had lost
-- it. That is real data loss and the engine side has closed it: an engine that
-- is told 409 by the heartbeat now stops reporting entirely.
--
-- The cost of that fix, stated plainly by the person who made it: a run whose
-- lease was taken and a run whose engine simply died are now INDISTINGUISHABLE.
-- Both go quiet, both hit the deadline, and both end as `abandoned` carrying
-- the same sentence. Only the control plane can tell them apart, because only
-- the control plane holds `lease_holder` and `lease_expires_at`, and it was
-- writing neither of those facts down anywhere a person could read.
--
-- WHY COUNTS AND NOT A VERDICT.
--
-- The obvious column is one enum: taken_over, engine_died, never_claimed. It is
-- rejected because it is an interpretation computed at one moment, and the
-- question people actually ask about an abandoned run is not the question that
-- was anticipated. These three columns are FACTS, each written at the instant
-- it becomes true, and every story below is read off them rather than decided
-- in advance:
--
--   accepted_at IS NULL                      nobody ever claimed it. The
--                                            dispatch never reached an engine.
--   accepted_at set, lease_takeovers = 0     one engine took it and went
--                                            silent. It died, or it was killed.
--   lease_takeovers > 0, unheld_reports = 0  a second engine took it over and
--                                            also went silent.
--   lease_takeovers > 0, unheld_reports > 0  a second engine took it over, and
--                                            the FIRST engine is alive and
--                                            tried to end the run. The plumbing
--                                            worked. Whoever holds it now is
--                                            the one that vanished.
--
-- That last row is the one worth having. An engine that stood down correctly
-- and was refused is proof the mechanism did its job, and without a column it
-- would be a line in an HTTP response nobody kept.
--
-- WHAT A TAKEOVER IS NOT. An expired lease that nobody reclaimed is NOT a
-- takeover. The original engine still holds the run in every sense that
-- matters: it can still heartbeat (the heartbeat matches on the holder, not on
-- the expiry), it can still report, and its report is still the only one there
-- will be. A takeover is another engine actually taking it, which is the only
-- moment at which the first engine's word stops being the truth.

BEGIN;

ALTER TABLE workload_runs
  -- How many times the lease moved to a DIFFERENT engine after a first claim.
  -- An ordinary claim of a `requested` run leaves this at zero; so does a
  -- re-claim by the same holder, which is a runner that restarted rather than a
  -- run that changed hands.
  ADD COLUMN lease_takeovers integer NOT NULL DEFAULT 0,
  -- When the last takeover happened, so a reader can put it next to the
  -- deadline and see how much of the run the second engine had.
  ADD COLUMN lease_lost_at timestamptz,
  -- How many terminal events arrived from an engine that does not hold this
  -- run and were therefore refused. The event itself is still stored in
  -- `events` with its whole payload, so nothing an engine said is thrown away:
  -- what is refused is the right to END the run, not the right to speak.
  ADD COLUMN unheld_reports integer NOT NULL DEFAULT 0,
  -- When the last one arrived. Separate from the count because "an engine tried
  -- to end this forty seconds ago" and "an engine tried to end this twice" are
  -- different sentences and a console may want either.
  ADD COLUMN unheld_report_at timestamptz;

-- Both are counts of things that happen and neither can go backwards. Cheap,
-- and it is the shape a bad UPDATE takes: a decrement here would be a lost
-- takeover, which is the exact fact this migration exists to keep.
ALTER TABLE workload_runs
  ADD CONSTRAINT workload_runs_lease_takeovers CHECK (lease_takeovers >= 0),
  ADD CONSTRAINT workload_runs_unheld_reports CHECK (unheld_reports >= 0);

-- A takeover is recorded, so a takeover must have a time, and a time must have
-- a takeover. Written as a constraint rather than left to the two statements
-- that maintain them, because they are maintained in two different files and a
-- count without its timestamp reads as "this happened, at no particular
-- moment", which is worse than either fact alone.
ALTER TABLE workload_runs
  ADD CONSTRAINT workload_runs_lease_lost_at CHECK (
    (lease_takeovers = 0) = (lease_lost_at IS NULL)),
  ADD CONSTRAINT workload_runs_unheld_report_at CHECK (
    (unheld_reports = 0) = (unheld_report_at IS NULL));

-- No new grant and no new policy. These are columns on a table that already has
-- both: `workload_runs` is already SELECT, INSERT, UPDATE to antifailure_app
-- and already carries tenant_isolation with FORCE ROW LEVEL SECURITY. A column
-- added to an RLS table inherits the table's policy, so there is nothing here
-- for the cross-tenant suite to newly attack. Said out loud rather than left
-- implicit, because the failure this repository has already shipped is a table
-- added WITHOUT a policy, and the reflex that prevents it should not stop at
-- "there is no CREATE TABLE here".

COMMIT;
