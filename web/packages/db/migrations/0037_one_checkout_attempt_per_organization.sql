BEGIN;

CREATE TABLE billing_checkout_attempts (
  org_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  attempt_id uuid NOT NULL DEFAULT gen_random_uuid(),
  stripe_customer_id text NOT NULL,
  price_id text NOT NULL,
  success_url text NOT NULL,
  cancel_url text NOT NULL,
  stripe_session_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE billing_checkout_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_checkout_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_checkout_attempts
  FOR ALL TO antifailure_app
  USING (org_id = current_org()) WITH CHECK (org_id = current_org());
GRANT SELECT, INSERT, UPDATE ON billing_checkout_attempts TO antifailure_app;
GRANT SELECT ON billing_checkout_attempts TO antifailure_admin;

COMMIT;
