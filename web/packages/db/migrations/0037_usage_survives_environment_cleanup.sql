/* Environment cleanup used to erase consumption. Keep only the interval and
 * tenant key, without a foreign key to the disposable environment. Deleting
 * the organization still erases its ledger and aggregates. Existing rows are
 * backfilled; already deleted history cannot be reconstructed. */
BEGIN;

CREATE TABLE environment_usage (
  environment_id uuid PRIMARY KEY,
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  env_id text NOT NULL,
  created_at timestamptz NOT NULL,
  torn_down_at timestamptz,
  CHECK (torn_down_at IS NULL OR torn_down_at >= created_at)
);
CREATE INDEX environment_usage_org_idx ON environment_usage(org_id, created_at);
CREATE INDEX environment_usage_open_idx ON environment_usage(org_id, created_at)
  WHERE torn_down_at IS NULL;

CREATE TABLE usage_rollup_state (
  org_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  dirty_from timestamptz,
  rolled_through timestamptz
);

CREATE TABLE environment_usage_daily (
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  day date NOT NULL,
  hours numeric NOT NULL CHECK (hours >= 0),
  environments bigint NOT NULL CHECK (environments >= 0),
  measured_at timestamptz NOT NULL,
  PRIMARY KEY (org_id, day)
);
CREATE INDEX environment_usage_daily_day_idx ON environment_usage_daily(day, org_id);

CREATE FUNCTION retain_environment_usage() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
  source environments;
  ended timestamptz;
BEGIN
  IF TG_OP = 'DELETE' THEN
    source := OLD;
    /* An ordinary deletion closes a still-open interval. The enclosing
     * organization deletion must not recreate a child of a missing parent. */
    IF NOT EXISTS (SELECT 1 FROM organizations WHERE id = source.org_id) THEN
      RETURN OLD;
    END IF;
    ended := GREATEST(source.created_at, COALESCE(source.torn_down_at, statement_timestamp()));
  ELSE
    source := NEW;
    ended := CASE WHEN source.torn_down_at IS NULL THEN NULL
      ELSE GREATEST(source.created_at, source.torn_down_at) END;
    IF TG_OP = 'UPDATE' AND NEW.created_at = OLD.created_at
      AND NEW.torn_down_at IS NOT DISTINCT FROM OLD.torn_down_at THEN
      RETURN NEW;
    END IF;
  END IF;
  /* Queue first: the rollup takes the same row lock before reading intervals.
   * A concurrent close either precedes the snapshot or remains dirty. */
  INSERT INTO usage_rollup_state(org_id, dirty_from)
    VALUES (source.org_id, source.created_at)
    ON CONFLICT (org_id) DO UPDATE
      SET dirty_from = LEAST(usage_rollup_state.dirty_from, EXCLUDED.dirty_from);
  INSERT INTO environment_usage(environment_id, org_id, env_id, created_at, torn_down_at)
    VALUES (source.id, source.org_id, source.env_id, source.created_at, ended)
    ON CONFLICT (environment_id) DO UPDATE
      SET created_at = LEAST(environment_usage.created_at, EXCLUDED.created_at),
          torn_down_at = COALESCE(EXCLUDED.torn_down_at, environment_usage.torn_down_at);
  RETURN source;
END
$$;
REVOKE ALL ON FUNCTION retain_environment_usage() FROM PUBLIC;
CREATE TRIGGER retain_environment_usage
  AFTER INSERT OR UPDATE OR DELETE ON environments
  FOR EACH ROW EXECUTE FUNCTION retain_environment_usage();

INSERT INTO environment_usage(environment_id, org_id, env_id, created_at, torn_down_at)
  SELECT id, org_id, env_id, created_at,
    CASE WHEN torn_down_at IS NULL THEN NULL ELSE GREATEST(created_at, torn_down_at) END
  FROM environments;
INSERT INTO usage_rollup_state(org_id, dirty_from)
  SELECT org_id, min(created_at) FROM environment_usage GROUP BY org_id;

CREATE FUNCTION roll_up_environment_usage(as_of timestamptz) RETURNS bigint
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp SET timezone = 'UTC' AS $$
DECLARE
  target record;
  first_day timestamptz;
  measured_to timestamptz;
  written bigint := 0;
BEGIN
  FOR target IN SELECT * FROM usage_rollup_state
    WHERE dirty_from IS NOT NULL OR EXISTS (
      SELECT 1 FROM environment_usage u
      WHERE u.org_id = usage_rollup_state.org_id AND u.torn_down_at IS NULL)
    ORDER BY org_id FOR UPDATE
  LOOP
    measured_to := GREATEST(as_of, target.rolled_through);
    first_day := date_trunc('day', LEAST(target.dirty_from, target.rolled_through), 'UTC');
    IF first_day IS NULL OR first_day > measured_to THEN CONTINUE; END IF;
    DELETE FROM environment_usage_daily
      WHERE org_id = target.org_id AND day >= (first_day AT TIME ZONE 'UTC')::date;
    INSERT INTO environment_usage_daily(org_id, day, hours, environments, measured_at)
      SELECT target.org_id, (d AT TIME ZONE 'UTC')::date,
        SUM(EXTRACT(EPOCH FROM (
          LEAST(COALESCE(u.torn_down_at, measured_to), d + interval '1 day', measured_to)
          - GREATEST(u.created_at, d))) / 3600.0), count(*), measured_to
      FROM generate_series(first_day, measured_to, interval '1 day') d
      JOIN environment_usage u ON u.org_id = target.org_id
        AND u.created_at < LEAST(d + interval '1 day', measured_to)
        AND COALESCE(u.torn_down_at, measured_to) > d
      GROUP BY d;
    UPDATE usage_rollup_state SET dirty_from = (
      SELECT min(created_at) FROM environment_usage
      WHERE org_id = target.org_id AND (created_at >= measured_to OR torn_down_at > measured_to)
    ), rolled_through = measured_to
      WHERE org_id = target.org_id;
    written := written + 1;
  END LOOP;
  RETURN written;
END
$$;
REVOKE ALL ON FUNCTION roll_up_environment_usage(timestamptz) FROM PUBLIC;

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['environment_usage', 'environment_usage_daily', 'usage_rollup_state']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('GRANT SELECT ON %I TO antifailure_app, antifailure_admin', t);
    EXECUTE format('CREATE POLICY tenant_reads ON %I FOR SELECT TO antifailure_app USING (org_id = current_org())', t);
  END LOOP;
END
$$;
COMMIT;
