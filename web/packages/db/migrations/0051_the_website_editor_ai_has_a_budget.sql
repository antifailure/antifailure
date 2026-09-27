BEGIN;
SET LOCAL lock_timeout = '3s';

CREATE TABLE website_ai_usage (
  actor_id uuid NOT NULL,
  usage_day date NOT NULL,
  requests integer NOT NULL DEFAULT 0 CHECK (requests >= 0),
  reserved_tokens integer NOT NULL DEFAULT 0 CHECK (reserved_tokens >= 0),
  input_tokens integer NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  PRIMARY KEY (actor_id, usage_day)
);

ALTER TABLE website_ai_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_ai_usage FORCE ROW LEVEL SECURITY;
REVOKE ALL ON website_ai_usage FROM PUBLIC, antifailure_app;
GRANT SELECT, INSERT, UPDATE ON website_ai_usage TO antifailure_admin;

COMMIT;
