-- Free-tier usage caps ("things free users can do before we ask them to talk
-- pricing"). Enforcement lives in Go (see internal/server/usage.go) -- this
-- table just captures the leads generated when someone hits a cap or asks
-- for pricing proactively from Settings.

CREATE TABLE IF NOT EXISTS quote_requests (
    id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name           TEXT NOT NULL,
    email          TEXT NOT NULL,
    company        TEXT,
    message        TEXT,
    reason         TEXT,   -- 'jobs_per_month' | 'repos' | 'proactive'
    requested_by   TEXT,   -- user_email of the submitter
    usage_snapshot JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_quote_requests_created_at ON quote_requests (created_at DESC);
