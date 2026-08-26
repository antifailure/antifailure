-- Plans, and the switch that stops one organization during an incident.
--
-- The kill switch is the part worth explaining. During an incident the useful
-- action is almost never "take the whole instance down": it is "stop this one
-- organization, or this one runtime, while we work out what it is doing". Doing
-- that by revoking their tokens is destructive and hard to undo; doing it by a
-- deploy is slow and takes everybody else with it.
--
-- So it is a row, it names a reason and who set it, and every use of it is
-- audited. It stops new work and deliberately does not tear down what is
-- already running, because an incident is the worst possible moment to
-- discover that the mitigation destroyed the evidence.

BEGIN;

ALTER TABLE organizations
  ADD COLUMN plan text NOT NULL DEFAULT 'free',
  -- Set during an incident to stop this organization creating anything new.
  -- Existing environments keep running.
  ADD COLUMN suspended_at timestamptz,
  ADD COLUMN suspended_reason text,
  ADD COLUMN suspended_by text;

-- Quota accounting reads these constantly, and both are counts over a tenant's
-- rows filtered by state.
CREATE INDEX environments_live_idx ON environments (org_id)
  WHERE state <> 'torn_down';

COMMIT;
