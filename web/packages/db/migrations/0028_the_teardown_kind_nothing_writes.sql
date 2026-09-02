-- The teardown kind on runtime_commands, removed.
--
-- Two branches built a durable teardown queue in the same week and neither
-- collided with the other, because a duplicated CONCEPT is not a textual
-- conflict and no gate can see one. `teardown_requests` arrived in 0021 with
-- the pull request lifecycle and `runtime_commands` in 0026 with Load, and the
-- product ended up with two answers to "was this environment torn down".
-- Only one of them can be right and a reader has no way to tell which.
--
-- `teardown_requests` WINS, and this is the reconciliation the other way round
-- from the one first written. The reason is not seniority, it is that the two
-- ledgers disagree about WHEN an environment is gone, and only one of them can
-- be trusted by everything downstream.
--
--   `teardown_requests` moves the environments row ONLY on an acknowledgement:
--   the workflow run holding it reached a terminal state at GitHub, or the
--   engine's own `env.destroyed` arrived and `attemptTeardown` read it. Until
--   then the row says the environment is still there, because it is.
--
--   The runtime_commands version marked `state = 'torn_down'` at the moment
--   somebody pressed the button, before the dispatch had been attempted. We
--   know for a fact that dispatch can fail: the refusal is caught, recorded,
--   and reported as `dispatched: false`. So that ledger could say an
--   environment was destroyed while the request to destroy it never left. A
--   ledger that can lie about destruction is worse than no ledger, because the
--   quota, the console and the customer all believe it.
--
-- It also keeps the retry. `teardown_requests` attempts five times under a
-- lease; the other route attempted once per stop event and compensated by
-- naming runtime_commands as the durable queue behind it. That compensation
-- rested on machinery nobody wired: the `environment.teardown` kind has NO
-- production writer, which is why this file exists. The difference the retry
-- buys is the difference between a wasted runner and a leaked environment, and
-- a leaked environment is unreviewed code still running with whatever access it
-- had.
--
-- WHAT IS ACTUALLY DEAD, checked caller by caller rather than by grepping for
-- the name. The only production `createCommand` call is in
-- routers/workloads.ts and its kind is `workload.cancel`. Nothing anywhere
-- wrote `environment.teardown`. `teardownFor` read it and had zero callers in
-- the entire web/ tree including tests. `acknowledgeTeardownFromEvent`
-- acknowledged it from ingestion, which is a live call site for a row that was
-- never created.
--
-- WHAT IS NOT DEAD AND MUST NOT FOLLOW IT. `runtime_commands` itself stays, and
-- so does every function around it. `/v1/commands` and `/v1/commands/:id/ack`
-- are live in server.ts, they are kind agnostic, and `workload.cancel` is a
-- real command with a real puller: an engine claims it by polling and
-- acknowledges it when it has acted. Deleting the machinery because the
-- teardown kind is going would break the one kind that works. This migration
-- narrows a vocabulary; it does not retire a table.

BEGIN;

-- Nothing to migrate. No row of this kind has ever been written, so this is a
-- narrowing of the type rather than a data change, and the DELETE below is a
-- belt-and-braces line for a database somebody experimented against by hand.
DELETE FROM runtime_commands WHERE kind = 'environment.teardown';

-- The target CHECK named both kinds. With one kind left it says the same thing
-- in one clause: a cancel names a run and nothing else.
ALTER TABLE runtime_commands DROP CONSTRAINT runtime_commands_target;

-- The partial index enforcing one live teardown per environment goes with the
-- kind it indexed, permanently.
DROP INDEX IF EXISTS runtime_commands_one_live_teardown;

-- And the cancel index comes down too, temporarily, which running this found
-- rather than reading it. Its predicate names `kind`, so the type swap below
-- fails with "operator does not exist: runtime_command_kind =
-- runtime_command_kind_with_teardown": the column still has the old type while
-- the literal in the stored predicate resolves to the new one. It is recreated
-- below, unchanged, and it is the index that stops two cancels racing for one
-- run so it must not be left off.
DROP INDEX IF EXISTS runtime_commands_one_live_cancel;

-- Postgres cannot drop a value from an enum, so the type is replaced. The
-- column is moved across explicitly rather than by USING a cast alone, because
-- a cast through text is what makes the rewrite legible in a review.
ALTER TYPE runtime_command_kind RENAME TO runtime_command_kind_with_teardown;
CREATE TYPE runtime_command_kind AS ENUM ('workload.cancel');
ALTER TABLE runtime_commands
  ALTER COLUMN kind TYPE runtime_command_kind
  USING kind::text::runtime_command_kind;
DROP TYPE runtime_command_kind_with_teardown;

-- Restored after the type swap, because dropping the constraint above was only
-- ever about the clause naming the departing kind.
ALTER TABLE runtime_commands ADD CONSTRAINT runtime_commands_target CHECK (
  kind = 'workload.cancel' AND workload_run_id IS NOT NULL AND environment_id IS NULL
);

-- Recreated exactly as 0026 declared it. One live cancel per run, enforced by a
-- unique partial index rather than by a read and a decision, because two
-- requests landing together both read nothing and both write.
CREATE UNIQUE INDEX runtime_commands_one_live_cancel
  ON runtime_commands (workload_run_id)
  WHERE kind = 'workload.cancel' AND state IN ('pending', 'claimed');

COMMIT;
