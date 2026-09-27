-- Source defaults remain in code. Only explicit edits live here. Drafts,
-- history, idempotency receipts and signing keys are operator-only; the serving
-- credential can read the published document and public media, and nothing else.
BEGIN;
SET LOCAL lock_timeout = '3s';

CREATE TABLE website_draft (
  id text PRIMARY KEY CHECK (id = 'homepage'),
  revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
  document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by text
);
CREATE TABLE website_published (
  id text PRIMARY KEY CHECK (id = 'homepage'),
  revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
  document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
  content_hash text NOT NULL
);
CREATE TABLE website_history (
  revision bigint PRIMARY KEY CHECK (revision > 0),
  draft_revision bigint NOT NULL CHECK (draft_revision >= 0),
  document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
  content_hash text NOT NULL,
  source_version text,
  restored_from bigint REFERENCES website_history(revision),
  created_at timestamptz NOT NULL DEFAULT now(),
  created_by text NOT NULL
);
CREATE TABLE website_mutations (
  request_id uuid PRIMARY KEY,
  actor_id uuid NOT NULL,
  operation text NOT NULL CHECK (operation IN ('save', 'publish', 'restore')),
  digest text NOT NULL,
  response jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE website_assets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sha256 text NOT NULL UNIQUE CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 180),
  kind text NOT NULL CHECK (kind IN ('image', 'video', 'font')),
  mime_type text NOT NULL CHECK (mime_type IN ('image/png','image/jpeg','image/webp','image/gif','video/mp4','video/webm','font/woff2')),
  size_bytes integer NOT NULL CHECK (size_bytes BETWEEN 1 AND 67108864),
  width integer CHECK (width BETWEEN 1 AND 16384),
  height integer CHECK (height BETWEEN 1 AND 16384),
  bytes bytea NOT NULL CHECK (octet_length(bytes) = size_bytes),
  is_public boolean NOT NULL DEFAULT false,
  archived boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  created_by text NOT NULL,
  CONSTRAINT website_asset_size_by_kind CHECK (
    (kind = 'image' AND size_bytes <= 12582912 AND width IS NOT NULL AND height IS NOT NULL)
    OR (kind = 'video' AND size_bytes <= 67108864)
    OR (kind = 'font' AND size_bytes <= 4194304)
  ),
  -- This table is born empty in this transaction. The constraint creates the
  -- browse-order index before another session can reach the table.
  CONSTRAINT website_assets_created_idx UNIQUE (created_at, id)
);
CREATE TABLE website_secrets (
  key text PRIMARY KEY,
  value bytea NOT NULL CHECK (octet_length(value) = 32)
);
CREATE TABLE website_refresh_jobs (
  revision bigint PRIMARY KEY REFERENCES website_history(revision),
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','dispatching','waiting','deployed','failed','superseded')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  lease_until timestamptz,
  lease_token uuid,
  dispatched_at timestamptz,
  last_error text,
  deployed_at timestamptz,
  -- An index created with the empty table supports due-job claims without a
  -- later blocking index build. Revision is already unique by primary key.
  CONSTRAINT website_refresh_due_idx UNIQUE (status, next_attempt_at, revision)
);

INSERT INTO website_draft(id, document) VALUES ('homepage', '{"schemaVersion":1,"fields":{},"styles":{},"sections":{"hidden":[],"moves":[],"custom":[]},"collections":{}}');
INSERT INTO website_published(id, document, content_hash) SELECT id, document,
  encode(sha256(convert_to('{"collections":{},"fields":{},"schemaVersion":1,"sections":{"custom":[],"hidden":[],"moves":[]},"styles":{}}','UTF8')), 'hex')
  FROM website_draft;

ALTER TABLE website_draft ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_draft FORCE ROW LEVEL SECURITY;
ALTER TABLE website_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_history FORCE ROW LEVEL SECURITY;
ALTER TABLE website_mutations ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_mutations FORCE ROW LEVEL SECURITY;
ALTER TABLE website_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE website_refresh_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_refresh_jobs FORCE ROW LEVEL SECURITY;
ALTER TABLE website_published ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_published FORCE ROW LEVEL SECURITY;
ALTER TABLE website_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE website_assets FORCE ROW LEVEL SECURITY;

REVOKE ALL ON website_draft, website_history, website_mutations, website_secrets, website_refresh_jobs, website_published, website_assets FROM PUBLIC, antifailure_app;
GRANT SELECT, UPDATE ON website_draft, website_published TO antifailure_admin;
GRANT SELECT, INSERT ON website_history, website_mutations, website_secrets TO antifailure_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON website_assets TO antifailure_admin;
GRANT SELECT, INSERT, UPDATE ON website_refresh_jobs TO antifailure_admin;
GRANT SELECT ON website_published TO antifailure_app;
CREATE POLICY website_published_read ON website_published FOR SELECT TO antifailure_app USING (id = 'homepage');
GRANT SELECT (id, sha256, mime_type, size_bytes, bytes, is_public) ON website_assets TO antifailure_app;
CREATE POLICY website_assets_public_read ON website_assets FOR SELECT TO antifailure_app USING (is_public);

-- Bytes and public visibility are immutable once published, even if a future
-- handler accidentally issues a broader UPDATE. Historical restores keep working.
CREATE FUNCTION website_asset_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id <> OLD.id OR NEW.sha256 <> OLD.sha256 OR NEW.bytes <> OLD.bytes
    OR NEW.mime_type <> OLD.mime_type OR NEW.size_bytes <> OLD.size_bytes
    OR NEW.kind <> OLD.kind OR NEW.width IS DISTINCT FROM OLD.width
    OR NEW.height IS DISTINCT FROM OLD.height OR (OLD.is_public AND NOT NEW.is_public)
  THEN RAISE EXCEPTION 'Website media content is immutable'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER website_asset_immutable BEFORE UPDATE ON website_assets FOR EACH ROW EXECUTE FUNCTION website_asset_immutable();

COMMIT;
