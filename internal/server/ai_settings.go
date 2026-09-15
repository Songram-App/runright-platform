package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sgbudje/runright-platform/internal/assistant"
)

// AISettingsResponse is what the frontend gets back. The API key itself is
// never returned once saved — only whether one is set — so it can't leak
// over the wire after the initial save.
type AISettingsResponse struct {
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKeySet bool   `json:"api_key_set"`
}

// getAISettings handles GET /api/v1/ai-settings (admin-only).
func (s *Server) getAISettings(c *gin.Context) {
	ctx := c.Request.Context()
	var provider string
	var model, baseURL, apiKey sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT provider, model, base_url, api_key FROM ai_settings WHERE singleton = true`,
	).Scan(&provider, &model, &baseURL, &apiKey)
	if err != nil {
		c.JSON(http.StatusOK, AISettingsResponse{})
		return
	}
	c.JSON(http.StatusOK, AISettingsResponse{
		Provider:  provider,
		Model:     model.String,
		BaseURL:   baseURL.String,
		APIKeySet: apiKey.Valid && apiKey.String != "",
	})
}

// upsertAISettings handles PUT /api/v1/ai-settings (admin-only). A blank
// api_key in the request leaves the previously stored key untouched, so the
// frontend never needs to re-submit a secret just to change the model name.
func (s *Server) upsertAISettings(c *gin.Context) {
	ctx := c.Request.Context()
	userEmail := getUserEmail(c)

	var body struct {
		Provider string `json:"provider" binding:"required"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
		Model    string `json:"model"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
		return
	}
	switch body.Provider {
	case "openai", "anthropic", "ollama":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider must be one of: openai, anthropic, ollama"})
		return
	}

	storedKey := ""
	if body.APIKey != "" {
		enc, err := encryptPAT(body.APIKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt API key"})
			return
		}
		storedKey = enc
	} else {
		var existing sql.NullString
		_ = s.db.QueryRowContext(ctx, `SELECT api_key FROM ai_settings WHERE singleton = true`).Scan(&existing)
		storedKey = existing.String
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_settings (singleton, provider, api_key, base_url, model, updated_at)
		VALUES (true, $1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NOW())
		ON CONFLICT (singleton) DO UPDATE SET
			provider = EXCLUDED.provider, api_key = EXCLUDED.api_key,
			base_url = EXCLUDED.base_url, model = EXCLUDED.model, updated_at = NOW()
	`, body.Provider, storedKey, body.BaseURL, body.Model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save AI settings"})
		return
	}

	s.reloadAssistant(ctx)
	s.logAudit(ctx, userEmail, c, "ai_settings.update", "ai_settings", "singleton", body.Provider, gin.H{"model": body.Model})
	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

// deleteAISettings handles DELETE /api/v1/ai-settings (admin-only). Falls
// back to whatever RUNRIGHT_AI_* env vars are set (or "not configured").
func (s *Server) deleteAISettings(c *gin.Context) {
	ctx := c.Request.Context()
	userEmail := getUserEmail(c)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ai_settings WHERE singleton = true`); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clear AI settings"})
		return
	}
	s.reloadAssistant(ctx)
	s.logAudit(ctx, userEmail, c, "ai_settings.delete", "ai_settings", "singleton", "", nil)
	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

// loadAssistant builds the Assistant from the ai_settings table (takes
// precedence when a provider is configured there) or falls back to
// RUNRIGHT_AI_* env vars, so self-hosted admins who only set env vars keep
// working exactly as before.
func (s *Server) loadAssistant(ctx context.Context) *assistant.Assistant {
	var provider string
	var model, baseURL, apiKeyEnc sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT provider, model, base_url, api_key FROM ai_settings WHERE singleton = true`,
	).Scan(&provider, &model, &baseURL, &apiKeyEnc)
	if err != nil || provider == "" {
		return assistant.NewFromEnv(s.db)
	}

	apiKey := ""
	if apiKeyEnc.Valid && apiKeyEnc.String != "" {
		decrypted, decErr := decryptPAT(apiKeyEnc.String)
		if decErr != nil {
			fmt.Printf("[ai] failed to decrypt stored API key, falling back to env: %v\n", decErr)
			return assistant.NewFromEnv(s.db)
		}
		apiKey = decrypted
	}

	return assistant.New(s.db, assistant.Config{
		Provider: assistant.LLMProvider(provider),
		APIKey:   apiKey,
		BaseURL:  baseURL.String,
		Model:    model.String,
	})
}

// reloadAssistant rebuilds the active assistant (see loadAssistant) and
// atomically swaps it in, so in-flight requests always see a consistent
// assistant instance.
func (s *Server) reloadAssistant(ctx context.Context) {
	a := s.loadAssistant(ctx)
	if s.embeddings != nil && s.embeddings.IsConfigured() {
		a.SetEmbeddingService(s.embeddings)
	}
	s.assistantPtr.Store(a)
}
