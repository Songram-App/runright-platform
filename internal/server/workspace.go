package server

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WorkspaceSettings is the per-instance branding shown in the sidebar/login
// screen — lets a self-serve cloud customer (or a self-hoster) give their
// deployment its own identity instead of always saying "RunRight".
type WorkspaceSettings struct {
	Name        string `json:"name"`
	AccentColor string `json:"accent_color,omitempty"`
}

// getWorkspaceSettings is intentionally public (no auth) — the login screen
// needs it before anyone has signed in.
func (s *Server) getWorkspaceSettings(c *gin.Context) {
	var ws WorkspaceSettings
	var accent sql.NullString
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT name, accent_color FROM workspace_settings WHERE singleton = true`,
	).Scan(&ws.Name, &accent)
	if err != nil {
		// No row yet (pre-migration edge case) — fall back to the default
		// rather than erroring out the login screen.
		ws.Name = "RunRight"
	}
	if accent.Valid {
		ws.AccentColor = accent.String
	}
	c.JSON(http.StatusOK, ws)
}

func (s *Server) upsertWorkspaceSettings(c *gin.Context) {
	var body struct {
		Name        string `json:"name" binding:"required"`
		AccentColor string `json:"accent_color"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if len(body.Name) > 60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 60 characters or fewer"})
		return
	}
	_, err := s.db.ExecContext(c.Request.Context(), `
		INSERT INTO workspace_settings (singleton, name, accent_color, updated_at)
		VALUES (true, $1, NULLIF($2, ''), NOW())
		ON CONFLICT (singleton) DO UPDATE SET
			name = EXCLUDED.name, accent_color = EXCLUDED.accent_color, updated_at = NOW()
	`, body.Name, body.AccentColor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save workspace settings"})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "workspace.update", "workspace_settings", "singleton", body.Name, nil)
	c.JSON(http.StatusOK, gin.H{"name": body.Name, "accent_color": body.AccentColor})
}
