package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// ThemePalette is one mode's (light or dark) set of themeable colors. Any
// empty field falls back to the built-in vintage default for that mode.
type ThemePalette struct {
	Background  string `json:"background,omitempty"`
	Surface     string `json:"surface,omitempty"`
	Text        string `json:"text,omitempty"`
	SidebarBg   string `json:"sidebar_bg,omitempty"`
	SidebarText string `json:"sidebar_text,omitempty"`
	Accent      string `json:"accent,omitempty"`
}

// ThemeSettings is the full customizable theme: independent light/dark
// palettes plus an optional font family applied across the whole UI.
type ThemeSettings struct {
	Light      *ThemePalette `json:"light,omitempty"`
	Dark       *ThemePalette `json:"dark,omitempty"`
	FontFamily string        `json:"font_family,omitempty"`
}

// WorkspaceSettings is the per-instance branding shown in the sidebar/login
// screen — lets a self-serve cloud customer (or a self-hoster) give their
// deployment its own identity instead of always saying "RunRight".
type WorkspaceSettings struct {
	Name string `json:"name"`
	// AccentColor mirrors theme.light.accent for older clients/integrations
	// that only read this flat field; kept in sync automatically on save.
	AccentColor string         `json:"accent_color,omitempty"`
	LogoURL     string         `json:"logo_url,omitempty"`
	Theme       *ThemeSettings `json:"theme,omitempty"`
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// fontFamilyRe allows CSS font-family lists like "Inter, system-ui, sans-serif".
var fontFamilyRe = regexp.MustCompile(`^[A-Za-z0-9 ,'"\-]{1,100}$`)

// allowedLogoDataURLRe matches small raster/SVG logos uploaded as data: URLs.
var allowedLogoDataURLRe = regexp.MustCompile(`^data:image/(png|jpeg|jpg|webp|svg\+xml);base64,[A-Za-z0-9+/=]+$`)

// maxLogoDataURLLen caps stored logo size (~350KB of image data, base64-inflated).
const maxLogoDataURLLen = 500_000

// maxThemeJSONLen is a sanity cap on the serialized theme blob.
const maxThemeJSONLen = 20_000

func validateThemePalette(p *ThemePalette) error {
	if p == nil {
		return nil
	}
	for _, v := range []string{p.Background, p.Surface, p.Text, p.SidebarBg, p.SidebarText, p.Accent} {
		if v != "" && !hexColorRe.MatchString(v) {
			return errInvalidThemeColor
		}
	}
	return nil
}

var errInvalidThemeColor = errors.New("theme colors must be hex values like #2C1A0E")

// getWorkspaceSettings is intentionally public (no auth) — the login screen
// needs it before anyone has signed in.
func (s *Server) getWorkspaceSettings(c *gin.Context) {
	var ws WorkspaceSettings
	var accent, logo, theme sql.NullString
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT name, accent_color, logo_url, theme FROM workspace_settings WHERE singleton = true`,
	).Scan(&ws.Name, &accent, &logo, &theme)
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
	if theme.Valid && theme.String != "" {
		var t ThemeSettings
		if json.Unmarshal([]byte(theme.String), &t) == nil {
			ws.Theme = &t
		}
	}
	c.JSON(http.StatusOK, ws)
}

func (s *Server) upsertWorkspaceSettings(c *gin.Context) {
	var body struct {
		Name        string         `json:"name" binding:"required"`
		AccentColor string         `json:"accent_color"`
		LogoURL     string         `json:"logo_url"`
		Theme       *ThemeSettings `json:"theme"`
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

	var themeJSON sql.NullString
	if body.Theme != nil {
		if err := validateThemePalette(body.Theme.Light); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := validateThemePalette(body.Theme.Dark); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if body.Theme.FontFamily != "" && !fontFamilyRe.MatchString(body.Theme.FontFamily) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "font_family contains invalid characters"})
			return
		}
		// Keep the flat accent_color field in sync for anything that only reads it.
		if body.Theme.Light != nil && body.Theme.Light.Accent != "" {
			body.AccentColor = body.Theme.Light.Accent
		}
		raw, err := json.Marshal(body.Theme)
		if err != nil || len(raw) > maxThemeJSONLen {
			c.JSON(http.StatusBadRequest, gin.H{"error": "theme is invalid or too large"})
			return
		}
		themeJSON = sql.NullString{String: string(raw), Valid: true}
	}

	_, err := s.db.ExecContext(c.Request.Context(), `
		INSERT INTO workspace_settings (singleton, name, accent_color, logo_url, theme, updated_at)
		VALUES (true, $1, NULLIF($2, ''), NULLIF($3, ''), $4, NOW())
		ON CONFLICT (singleton) DO UPDATE SET
			name = EXCLUDED.name, accent_color = EXCLUDED.accent_color, logo_url = EXCLUDED.logo_url,
			theme = EXCLUDED.theme, updated_at = NOW()
	`, body.Name, body.AccentColor, body.LogoURL, themeJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save workspace settings"})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "workspace.update", "workspace_settings", "singleton", body.Name, nil)
	c.JSON(http.StatusOK, gin.H{"name": body.Name, "accent_color": body.AccentColor, "logo_url": body.LogoURL, "theme": body.Theme})
}
