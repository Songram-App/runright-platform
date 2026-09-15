-- A single row of instance-level branding/identity, distinct from per-user
-- settings or team settings. Every deployment (self-hosted or a RunRight
-- Cloud tenant) has exactly one row here.
CREATE TABLE IF NOT EXISTS workspace_settings (
    singleton  BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    name       TEXT NOT NULL DEFAULT 'RunRight',
    accent_color TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO workspace_settings (singleton, name) VALUES (true, 'RunRight')
ON CONFLICT (singleton) DO NOTHING;
