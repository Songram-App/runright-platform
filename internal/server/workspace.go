package server

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// WorkspaceSettings is the per-instance branding shown in the sidebar/login
// screen — lets a self-serve cloud customer (or a self-hoster) give their
// deployment its own identity instead of always saying "RunRight".
type WorkspaceSettings struct {
	Name        string `json:"name"`
	AccentColor string `json:"accent_color,omitempty"`
	LogoURL     string `json:"logo_url,omitempty"`
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// allowedLogoDataURLRe matches small raster/SVG logos uploaded as data: URLs.
var allowedLogoDataURLRe = regexp.MustCompile(`^data:image/(png|jpeg|jpg|webp|svg\+xml);base64,[A-Za-z0-9+/=]+$`)

// maxLogoDataURLLen caps stored logo size (~350KB of image data, base64-inflated).
const maxLogoDataURLLen = 500_000

// getWorkspaceSettings is intentionally public (no auth) — the login screen
// needs it before anyone has signed in.
func (s *Server) getWorkspaceSettings(c *gin.Context) {
	var ws WorkspaceSettings
	var accent, logo sql.NullString
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT name, accent_color, logo_url FROM workspace_settings WHERE singleton = true`,
	).Scan(&ws.Name, &accent, &logo)
	if err != nil {
		// No row yet (pre-migration edge case) — fall back to the default
		// rather than erroring out the login screen.
		ws.Name = "RunRight"
	}
	if accent.Valid {
		ws.AccentColor = accent.String
	}
	if logo.Valid {
		ws.LogoURL = logo.String
	}
	c.JSON(http.StatusOK, ws)
}

func (s *Server) upsertWorkspaceSettings(c *gin.Context) {
	var body struct {
		Name        string `json:"name" binding:"required"`
		AccentColor string `json:"accent_color"`
		LogoURL     string `json:"logo_url"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if len(body.Name) > 60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 60 characters or fewer"})
		return
	}
	if body.AccentColor != "" && !hexColorRe.MatchString(body.AccentColor) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "accent_color must be a hex color like #B8860B"})
		return
	}
	if body.LogoURL != "" {
		isExternal := strings.HasPrefix(body.LogoURL, "http://") || strings.HasPrefix(body.LogoURL, "https://")
		if len(body.LogoURL) > maxLogoDataURLLen {
			c.JSON(http.StatusBadRequest, gin.H{"error": "logo is too large (max ~350KB)"})
			return
		}
		if !isExternal && !allowedLogoDataURLRe.MatchString(body.LogoURL) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "logo must be a PNG/JPEG/WebP/SVG image or an https:// URL"})
			return
		}
	}
	_, err := s.db.ExecContext(c.Request.Context(), `
		INSERT INTO workspace_settings (singleton, name, accent_color, logo_url, updated_at)
		VALUES (true, $1, NULLIF($2, ''), NULLIF($3, ''), NOW())
		ON CONFLICT (singleton) DO UPDATE SET
			name = EXCLUDED.name, accent_color = EXCLUDED.accent_color, logo_url = EXCLUDED.logo_url, updated_at = NOW()
	`, body.Name, body.AccentColor, body.LogoURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save workspace settings"})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "workspace.update", "workspace_settings", "singleton", body.Name, nil)
	c.JSON(http.StatusOK, gin.H{"name": body.Name, "accent_color": body.AccentColor, "logo_url": body.LogoURL})
}
