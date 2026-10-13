CREATE TABLE IF NOT EXISTS organizations (
 id bigserial PRIMARY KEY, slug text UNIQUE NOT NULL CHECK(slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$' AND slug NOT IN ('www','admin','api')),
 kind text NOT NULL CHECK(kind IN ('school','preschool')), active boolean NOT NULL DEFAULT true,
 name jsonb NOT NULL, profile jsonb NOT NULL DEFAULT '{}', hidden_sections jsonb NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS users (
 id bigserial PRIMARY KEY, organization_id bigint REFERENCES organizations(id), login text UNIQUE NOT NULL,
 password_hash text NOT NULL, must_change boolean NOT NULL DEFAULT true
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash text PRIMARY KEY, user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 csrf text NOT NULL, expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS content (
 id bigserial PRIMARY KEY, organization_id bigint REFERENCES organizations(id),
 kind text NOT NULL CHECK(kind IN ('about','administration','teachers','news','documents','gallery','schedule')),
 published boolean NOT NULL DEFAULT false, position integer NOT NULL DEFAULT 0,
 translations jsonb NOT NULL DEFAULT '{}', updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE content ADD COLUMN IF NOT EXISTS schedule_date date;
CREATE INDEX IF NOT EXISTS content_site ON content(organization_id,kind,published,position);
CREATE UNIQUE INDEX IF NOT EXISTS singleton_page ON content(COALESCE(organization_id,0),kind) WHERE kind IN ('about','schedule');
CREATE TABLE IF NOT EXISTS uploads (
 id text PRIMARY KEY, organization_id bigint REFERENCES organizations(id), content_id bigint REFERENCES content(id),
 purpose text NOT NULL CHECK(purpose IN ('content','logo')), name text NOT NULL, mime text NOT NULL,
 path text NOT NULL, thumbnail text, size bigint NOT NULL, caption jsonb NOT NULL DEFAULT '{}', position integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), CHECK((purpose='content' AND content_id IS NOT NULL) OR (purpose='logo' AND content_id IS NULL))
);
ALTER TABLE uploads ADD COLUMN IF NOT EXISTS image_width integer NOT NULL DEFAULT 0;
ALTER TABLE uploads ADD COLUMN IF NOT EXISTS image_height integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS uploads_content ON uploads(content_id,position);
CREATE TABLE IF NOT EXISTS login_attempts (key text PRIMARY KEY, attempts integer NOT NULL, window_start timestamptz NOT NULL);

ALTER TABLE users ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true;
CREATE TABLE IF NOT EXISTS site_settings (
 site_id bigint PRIMARY KEY CHECK(site_id >= 0), menu jsonb NOT NULL
);

ALTER TABLE content DROP CONSTRAINT IF EXISTS content_kind_check;
ALTER TABLE content ADD CONSTRAINT content_kind_check CHECK(kind IN ('about','administration','teachers','news','documents','gallery','schedule','banners'));
