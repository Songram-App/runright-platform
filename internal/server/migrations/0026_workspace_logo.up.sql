-- Lets workspace branding include a custom logo image, alongside the name
-- and accent color from migration 0023. Stored as a data: URL (small raster/
-- svg logos only) so no external object storage is required.
ALTER TABLE workspace_settings ADD COLUMN IF NOT EXISTS logo_url TEXT;
