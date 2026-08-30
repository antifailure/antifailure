-- An egress rule nobody approved was already enforcing.
--
-- The permission model separates proposing a policy change from approving one,
-- and says why: masking rules and egress rules are the two settings where a
-- mistake is a data incident rather than an inconvenience. `masking_rules`
-- honours that with its `confirmed` column. `network_rules` did not honour it
-- at all. There was no column to hold the distinction, so `network.propose`
-- inserted a row that `effectiveEgress` read back immediately, and a member --
-- the least privileged role that holds `network.edit` -- could add an ALLOW for
-- any host and the control plane would report it as the policy in force.
--
-- The permission that was supposed to stop that, `network.approve`, guarded no
-- route. Nobody could approve, which read like a missing feature and was in
-- fact a missing gate: the approval was not pending, it was skipped.
--
-- A single timestamp rather than a boolean plus a timestamp. `approved_at IS
-- NULL` is the whole of "pending", and two columns that encode one fact
-- eventually disagree.
--
-- Both actors are recorded, not just the approver. The community edition lets
-- one person propose and approve, which is correct for a team of three, and the
-- enterprise approval policies refuse exactly that. Neither can be enforced
-- later against rows that never said who did what.
ALTER TABLE network_rules
  ADD COLUMN proposed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN approved_by uuid REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN approved_at timestamptz;

-- Existing rows keep enforcing. They were effective the moment they were
-- written, every environment that read them has been applying them, and
-- retiring them here would loosen a live egress policy during a migration,
-- which is the exact accident this file exists to prevent. They are marked
-- approved at the time they were created and with no approver, which is the
-- true statement: nobody approved them, because there was nothing to approve
-- with.
UPDATE network_rules SET approved_at = created_at WHERE approved_at IS NULL;

-- Finding the queue must not scan the table. Pending rules are the small set
-- and the one a person waits on.
CREATE INDEX network_rules_pending_idx ON network_rules (org_id, created_at)
  WHERE approved_at IS NULL;
