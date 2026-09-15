-- Stripe subscription state, stored per team (tenant).
ALTER TABLE teams ADD COLUMN IF NOT EXISTS stripe_customer_id TEXT;
ALTER TABLE teams ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT;
ALTER TABLE teams ADD COLUMN IF NOT EXISTS subscription_status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE teams ADD COLUMN IF NOT EXISTS current_period_end TIMESTAMPTZ;
ALTER TABLE teams ADD COLUMN IF NOT EXISTS cancel_at_period_end BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_teams_stripe_customer ON teams(stripe_customer_id);

-- Grant the new billing:manage permission to roles that should be able to
-- manage a team's subscription (owner already has "*").
UPDATE roles SET permissions = permissions || '["billing:manage"]'::jsonb
WHERE name = 'admin' AND is_system = true AND NOT permissions @> '["billing:manage"]'::jsonb;

UPDATE roles SET permissions = permissions || '["billing:manage"]'::jsonb
WHERE name = 'billing' AND is_system = true AND NOT permissions @> '["billing:manage"]'::jsonb;
