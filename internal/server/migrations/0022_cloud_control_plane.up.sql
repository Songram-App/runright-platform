-- Control-plane bookkeeping for the managed "RunRight Cloud" offering.
-- This data lives in the platform's own database (not a tenant's dedicated
-- database) and tracks which customers exist and which Fly app + Postgres
-- database was provisioned for each of them.

CREATE TABLE IF NOT EXISTS cloud_customers (
    id            TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT,
    avatar_url    TEXT,
    auth_provider TEXT NOT NULL,          -- 'github' | 'google'
    provider_uid  TEXT NOT NULL,          -- provider's stable user id
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(auth_provider, provider_uid)
);

CREATE TABLE IF NOT EXISTS cloud_tenants (
    id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    customer_id    TEXT NOT NULL REFERENCES cloud_customers(id) ON DELETE CASCADE,
    slug           TEXT NOT NULL UNIQUE,   -- used for fly app name (rr-<slug>) and db name (rr_<slug>)
    fly_app_name   TEXT NOT NULL,
    db_name        TEXT NOT NULL,
    base_url       TEXT NOT NULL,          -- e.g. https://rr-acme.fly.dev
    plan           TEXT NOT NULL DEFAULT 'free',
    status         TEXT NOT NULL DEFAULT 'provisioning', -- provisioning | active | failed | suspended
    error_message  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_cloud_tenants_customer ON cloud_tenants(customer_id);
CREATE INDEX IF NOT EXISTS idx_cloud_tenants_status ON cloud_tenants(status);
