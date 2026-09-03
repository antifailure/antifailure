-- An impersonation the schema could not record.
--
-- 0023 added four columns to `sessions` so that a session which is an operator
-- acting as a customer carries that fact on the row every request already
-- reads. The header of that file argues the point well: a marker in a side
-- table is a session that looks ordinary to every check in the product, so the
-- marker travels with the thing it describes.
--
-- Two things were wrong with it, and both only become visible when somebody
-- tries to write the first row.
--
-- ONE. `impersonated_by` references `users(id)`. An operator is a row in
-- `admin_users`, which 0029 created as a deliberately separate id space, for
-- the reason that file gives: if operator power were a flag on a product
-- session, compromising somebody's GitHub account would compromise the
-- platform. So the operator's id cannot go in that column, and the CHECK is
-- all-or-nothing across all four columns, which means it cannot be left out
-- either. Between them the two constraints make an operator impersonation
-- literally unrepresentable. Nothing in the tree had ever inserted one, so the
-- defect had no symptom until the route existed.
--
-- TWO. `ON DELETE SET NULL` on that foreign key and the CHECK cannot both hold.
-- Deleting the referenced row nulls one of the four columns and leaves the
-- other three, which is exactly the shape the CHECK refuses, so the DELETE
-- fails with a constraint violation on a table nobody was looking at. The old
-- pairing was not exercised for the same reason as the first defect.
--
-- THE FIX, and why it is keyed where it is. The predicate that says "this
-- session is an impersonation" moves from `impersonated_by` to
-- `impersonation_audit_seq`, and every partial index and every query moves with
-- it. That column is the right one to key on for a reason the original comment
-- almost states: it is the sequence number of the audit entry that authorised
-- the session, so it is the one column that must exist before the row does and
-- the one column no cascade can ever null. `impersonated_by` becomes what
-- `impersonator_label` beside it always was, a correlation handle that may be
-- lost when an operator's own account is deleted, and losing it no longer
-- destroys the record: the label is text and the audit entry is immutable.
--
-- The foreign key on the sequence number is added here rather than left
-- implicit. `admin_sessions` has carried one since 0029, and the argument for
-- it is stronger on this table, not weaker: this is the row the PRODUCT reads
-- on every request, and it is now impossible to insert one claiming an audit
-- entry that was never written.
--
-- NOTHING IS MIGRATED, because there is nothing to migrate. No code path in the
-- repository has ever set any of these four columns; `grep -rn impersonated_by`
-- over `web/apps/api/src` and `web/packages/db/src` at 0031 finds the schema
-- declaration and nothing that writes it. The assertion at the bottom of this
-- file proves that on the database it is applied to rather than trusting the
-- sentence.

BEGIN;

-- ---------------------------------------------------------------------------
-- Prove the premise before acting on it.
--
-- If some installation DOES hold impersonation rows, they were written against
-- the old foreign key and their `impersonated_by` values are user ids. Silently
-- repointing the column at another table would leave those rows pointing at
-- whichever operator happens to share a uuid, which is nobody, and the new
-- foreign key would refuse them anyway with a message about a constraint rather
-- than about the data. Refusing here says what actually happened.
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  stragglers bigint;
BEGIN
  SELECT count(*) INTO stragglers FROM sessions WHERE impersonated_by IS NOT NULL;
  IF stragglers > 0 THEN
    RAISE EXCEPTION
      'This installation holds % session rows with impersonated_by set, which no code in this '
      'repository writes. They reference users(id) and this migration repoints that column at '
      'admin_users(id). Decide what those rows are before applying it.', stragglers;
  END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- The column
-- ---------------------------------------------------------------------------

-- Dropped by name rather than by IF EXISTS on a guess: this is the name
-- Postgres gives a single-column foreign key on `sessions(impersonated_by)`,
-- and 0023 created it without naming one.
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_impersonated_by_fkey;

-- ON DELETE SET NULL, and now it is safe, because the CHECK below no longer
-- keys on this column. Deleting an operator's account leaves the session row
-- intact and still complete: the label names them, the reason says why, and the
-- audit entry is the record. That is the same trade `suspended_by text` makes
-- three tables over, and for the same reason.
ALTER TABLE sessions
  ADD CONSTRAINT sessions_impersonated_by_fkey
  FOREIGN KEY (impersonated_by) REFERENCES admin_users(id) ON DELETE SET NULL;

-- The record has to exist before the session does, structurally rather than by
-- writing the two statements in the right order and hoping nobody reorders
-- them. `admin_audit_entries` takes INSERT and SELECT and never DELETE, so this
-- reference cannot be broken later either.
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_impersonation_audit_fkey;
ALTER TABLE sessions
  ADD CONSTRAINT sessions_impersonation_audit_fkey
  FOREIGN KEY (impersonation_audit_seq) REFERENCES admin_audit_entries(seq);

-- ---------------------------------------------------------------------------
-- The constraint
--
-- Same rule as 0023 stated, keyed on the column that cannot be nulled out from
-- under it. The shape it refuses is unchanged and is the one that matters: an
-- impersonation with no reason captured. What it now permits is an
-- impersonation whose operator account has since been deleted, which is a
-- record with a gap in it rather than a record that must be destroyed.
-- ---------------------------------------------------------------------------

ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_impersonation_is_complete;
ALTER TABLE sessions ADD CONSTRAINT sessions_impersonation_is_complete CHECK (
  (impersonation_audit_seq IS NULL
     AND impersonated_by IS NULL
     AND impersonator_label IS NULL
     AND impersonation_reason IS NULL)
  OR
  (impersonation_audit_seq IS NOT NULL
     AND impersonator_label IS NOT NULL
     AND impersonation_reason IS NOT NULL
     -- An empty reason is not a reason. Enforced here rather than in the
     -- handler's validation because the handler is one caller and this is
     -- every caller.
     AND length(btrim(impersonation_reason)) > 0)
);

-- ---------------------------------------------------------------------------
-- The index
--
-- Repointed at the new predicate, because a partial index whose WHERE clause
-- does not match the query's WHERE clause is not used and is not reported as
-- unused either. The list of live impersonations is the query it exists for:
-- every open one across the installation, newest first.
-- ---------------------------------------------------------------------------

DROP INDEX IF EXISTS sessions_impersonated_idx;
CREATE INDEX IF NOT EXISTS sessions_impersonated_idx
  ON sessions (created_at DESC)
  WHERE impersonation_audit_seq IS NOT NULL;

COMMIT;
