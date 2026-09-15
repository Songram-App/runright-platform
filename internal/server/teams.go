package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Team represents an organization/team.
type Team struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Slug         string         `json:"slug"`
	Description  string         `json:"description,omitempty"`
	AvatarURL    string         `json:"avatar_url,omitempty"`
	BillingEmail string         `json:"billing_email,omitempty"`
	Plan         string         `json:"plan"`
	Settings     map[string]any `json:"settings,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	MemberCount  int            `json:"member_count,omitempty"`
}

// TeamMember represents a member of a team.
type TeamMember struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	UserEmail   string    `json:"user_email"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions,omitempty"`
	InvitedBy   string    `json:"invited_by,omitempty"`
	InvitedAt   time.Time `json:"invited_at,omitempty"`
	JoinedAt    time.Time `json:"joined_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// TeamInvitation represents a pending invitation.
type TeamInvitation struct {
	ID         string     `json:"id"`
	TeamID     string     `json:"team_id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Token      string     `json:"-"`
	InvitedBy  string     `json:"invited_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// --- Team Handlers ---

func (s *Server) listTeams(c *gin.Context) {
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.avatar_url, t.plan, t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM team_members WHERE team_id = t.id) as member_count
		FROM teams t
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_email = $1
		ORDER BY t.name
	`, userEmail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list teams"})
		return
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.AvatarURL, &t.Plan, &t.CreatedAt, &t.UpdatedAt, &t.MemberCount); err != nil {
			continue
		}
		teams = append(teams, t)
	}

	c.JSON(http.StatusOK, gin.H{"teams": teams})
}

func (s *Server) createTeam(c *gin.Context) {
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	var body struct {
		Name        string `json:"name" binding:"required"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}

	// Generate slug if not provided
	slug := body.Slug
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(body.Name, " ", "-"))
	}

	// Create team and add creator as owner
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}
	defer tx.Rollback()

	var teamID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO teams (name, slug, description)
		VALUES ($1, $2, $3)
		RETURNING id
	`, body.Name, slug, body.Description).Scan(&teamID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "team slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}

	// Add creator as owner
	_, err = tx.ExecContext(ctx, `
		INSERT INTO team_members (team_id, user_email, role, joined_at)
		VALUES ($1, $2, 'owner', NOW())
	`, teamID, userEmail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add team owner"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}

	s.logAudit(ctx, userEmail, c, "team.create", "team", teamID, body.Name, nil)

	c.JSON(http.StatusCreated, gin.H{
		"id":   teamID,
		"slug": slug,
	})
}

func (s *Server) getTeam(c *gin.Context) {
	teamID := c.Param("teamId")
	ctx := c.Request.Context()

	var t Team
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, description, avatar_url, billing_email, plan, settings, created_at, updated_at
		FROM teams WHERE id = $1
	`, teamID).Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.AvatarURL, &t.BillingEmail, &t.Plan, &t.Settings, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get team"})
		return
	}

	c.JSON(http.StatusOK, t)
}

func (s *Server) updateTeam(c *gin.Context) {
	teamID := c.Param("teamId")
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	var body struct {
		Name         *string        `json:"name"`
		Description  *string        `json:"description"`
		AvatarURL    *string        `json:"avatar_url"`
		BillingEmail *string        `json:"billing_email"`
		Settings     map[string]any `json:"settings"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Build dynamic update
	updates := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	if body.Name != nil {
		updates = append(updates, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *body.Name)
		argIdx++
	}
	if body.Description != nil {
		updates = append(updates, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *body.Description)
		argIdx++
	}
	if body.AvatarURL != nil {
		updates = append(updates, fmt.Sprintf("avatar_url = $%d", argIdx))
		args = append(args, *body.AvatarURL)
		argIdx++
	}
	if body.BillingEmail != nil {
		updates = append(updates, fmt.Sprintf("billing_email = $%d", argIdx))
		args = append(args, *body.BillingEmail)
		argIdx++
	}

	args = append(args, teamID)
	query := fmt.Sprintf("UPDATE teams SET %s WHERE id = $%d", strings.Join(updates, ", "), argIdx)

	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update team"})
		return
	}

	s.logAudit(ctx, userEmail, c, "team.update", "team", teamID, "", body)
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (s *Server) listTeamMembers(c *gin.Context) {
	teamID := c.Param("teamId")
	ctx := c.Request.Context()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, team_id, user_email, role, invited_by, invited_at, joined_at, created_at
		FROM team_members
		WHERE team_id = $1
		ORDER BY role, user_email
	`, teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list members"})
		return
	}
	defer rows.Close()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		var invitedBy, invitedAt sql.NullString
		var joinedAt sql.NullTime
		if err := rows.Scan(&m.ID, &m.TeamID, &m.UserEmail, &m.Role, &invitedBy, &invitedAt, &joinedAt, &m.CreatedAt); err != nil {
			continue
		}
		if invitedBy.Valid {
			m.InvitedBy = invitedBy.String
		}
		if joinedAt.Valid {
			m.JoinedAt = joinedAt.Time
		}
		members = append(members, m)
	}

	c.JSON(http.StatusOK, gin.H{"members": members})
}

func (s *Server) inviteTeamMember(c *gin.Context) {
	teamID := c.Param("teamId")
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	var body struct {
		Email string `json:"email" binding:"required,email"`
		Role  string `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid email required"})
		return
	}

	if body.Role == "" {
		body.Role = "member"
	}

	// Enforce plan seat limits before creating the invitation.
	var plan string
	var memberCount int
	if err := s.db.QueryRowContext(ctx, `
		SELECT t.plan, (SELECT COUNT(*) FROM team_members WHERE team_id = t.id)
		FROM teams t WHERE t.id = $1
	`, teamID).Scan(&plan, &memberCount); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if !checkMemberLimit(plan, memberCount) {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "member limit reached for the " + plan + " plan — upgrade to invite more people"})
		return
	}

	// Generate invitation token
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO team_invitations (team_id, email, role, token, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, NOW() + INTERVAL '7 days')
	`, teamID, body.Email, body.Role, token, userEmail)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invitation"})
		return
	}

	s.logAudit(ctx, userEmail, c, "team.invite", "team_invitation", "", body.Email, map[string]any{"role": body.Role})

	// Return invitation link (frontend should send email)
	c.JSON(http.StatusCreated, gin.H{
		"invitation_url": fmt.Sprintf("/invite/%s", token),
		"expires_in":     "7 days",
	})
}

func (s *Server) updateTeamMember(c *gin.Context) {
	teamID := c.Param("teamId")
	memberID := c.Param("memberId")
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	var body struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role required"})
		return
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE team_members SET role = $1 WHERE id = $2 AND team_id = $3
	`, body.Role, memberID, teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update member"})
		return
	}

	s.logAudit(ctx, userEmail, c, "team.member.update", "team_member", memberID, "", map[string]any{"role": body.Role})
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (s *Server) removeTeamMember(c *gin.Context) {
	teamID := c.Param("teamId")
	memberID := c.Param("memberId")
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	// Get member email for audit
	var memberEmail string
	s.db.QueryRowContext(ctx, "SELECT user_email FROM team_members WHERE id = $1", memberID).Scan(&memberEmail)

	_, err := s.db.ExecContext(ctx, `
		DELETE FROM team_members WHERE id = $1 AND team_id = $2
	`, memberID, teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove member"})
		return
	}

	s.logAudit(ctx, userEmail, c, "team.member.remove", "team_member", memberID, memberEmail, nil)
	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

// Helper to get user email from context (set by auth middleware)
func getUserEmail(c *gin.Context) string {
	if email, ok := c.Get("user_email"); ok {
		return email.(string)
	}
	// Fallback to SSO session
	if email, ok := c.Get("sso_email"); ok {
		return email.(string)
	}
	return "system"
}
