BEGIN;

CREATE TABLE recruitment_applications (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  email text NOT NULL CHECK (length(email) BETWEEN 3 AND 320),
  role text NOT NULL CHECK (role IN ('founding_engineer', 'founding_growth')),
  project_url text NOT NULL DEFAULT '' CHECK (length(project_url) <= 2000),
  why text NOT NULL CHECK (length(why) BETWEEN 1 AND 4000),
  compensation_acknowledged boolean NOT NULL CHECK (compensation_acknowledged),
  created_at timestamptz NOT NULL DEFAULT now(),
  reviewed_at timestamptz,
  reviewed_by uuid REFERENCES admin_users(id) ON DELETE SET NULL
);
CREATE INDEX recruitment_applications_queue ON recruitment_applications(created_at, id);
ALTER TABLE recruitment_applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE recruitment_applications FORCE ROW LEVEL SECURITY;
CREATE POLICY recruitment_insert ON recruitment_applications
  FOR INSERT TO antifailure_app WITH CHECK (true);
GRANT INSERT ON recruitment_applications TO antifailure_app;
GRANT SELECT, UPDATE, DELETE ON recruitment_applications TO antifailure_admin;

COMMIT;
