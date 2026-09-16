-- Full theme customization: separate light/dark palettes (background,
-- surface, text, sidebar colors, accent) plus an optional font family,
-- stored as one JSONB blob alongside the existing accent_color/logo_url.
ALTER TABLE workspace_settings ADD COLUMN IF NOT EXISTS theme JSONB;
