-- Lets a workspace admin configure the AI assistant provider/API key from the
-- Settings UI instead of only via RUNRIGHT_AI_* environment variables. The
-- API key is encrypted at rest (see internal/server/autopr.go's
-- encryptPAT/decryptPAT, reused here) when RUNRIGHT_ENCRYPTION_KEY is set.

CREATE TABLE IF NOT EXISTS ai_settings (
    singleton  BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    provider   TEXT NOT NULL DEFAULT '',  -- 'openai' | 'anthropic' | 'ollama'
    api_key    TEXT,                      -- encrypted ("enc:v1:...") or plaintext if no encryption key is set
    base_url   TEXT,                      -- required for Ollama
    model      TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
