package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Permission constants — canonical strings used in DB and enforced by middleware.
const (
	PermPoliciesManage  = "policies:manage"
	PermAlertsManage    = "alerts:manage"
	PermJobsManage      = "jobs:manage"
	PermOwnershipManage = "ownership:manage"
	PermTeamManage      = "team:manage"
	PermAPIKeysManage   = "apikeys:manage"
	PermReportsManage   = "reports:manage"
	PermAuditView       = "audit:view"
	PermBillingManage   = "billing:manage"
)

// allPermissions is the complete list of named permissions the system knows about.
// Used to validate custom roles and to build the frontend checkbox list.
var allPermissions = []string{
	PermPoliciesManage,
	PermAlertsManage,
	PermJobsManage,
	PermOwnershipManage,
	PermTeamManage,
	PermAPIKeysManage,
	PermReportsManage,
	PermAuditView,
	PermBillingManage,
}

// anyTeamRole lists every team-scoped role, used where a route just requires
// the caller to be *some* member of the team (e.g. viewing team details).
var anyTeamRole = []string{"viewer", "member", "developer", "billing", "admin", "owner"}

// requireTeamRole returns middleware that ensures the caller is a member of the
// team identified by the :teamId route param with one of allowedRoles (owner is
// always allowed). Unlike requirePermission — which checks a user's *global*
// role — this checks membership in the *specific* team being accessed, which is
// required to prevent one tenant's user from reading or modifying another
// tenant's team by guessing/enumerating team IDs.
func (s *Server) requireTeamRole(allowedRoles ...string) gin.HandlerFunc {
	allowed := map[string]bool{"owner": true}
	for _, r := range allowedRoles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		email := getUserEmail(c)
		// Static API key / dev-bypass operator → full access (self-hosted single-tenant mode).
		if email == "system" || email == "" || email == "dev@runright.io" {
			c.Next()
			return
		}
		teamID := c.Param("teamId")
		var role string
		err := s.db.QueryRowContext(c.Request.Context(),
			`SELECT role FROM team_members WHERE team_id = $1 AND user_email = $2`, teamID, email).Scan(&role)
		switch {
		case err == sql.ErrNoRows:
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "team not found"})
		case err != nil:
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to verify team membership"})
		case !allowed[role]:
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient team role"})
		default:
			c.Next()
		}
	}
}

// Role is the API representation of a role record.
type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	IsSystem    bool      `json:"is_system"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// getUserRole resolves the role name for the user making the current request.
//   - Static API key session  → "owner" (operator who set up the system)
//   - SSO-authenticated user  → looked up from sso_users.role
//   - DB-issued API key user  → looked up from sso_users or team_members
//   - Unknown / no match      → "viewer" (deny writes by default)
func (s *Server) getUserRole(ctx context.Context, c *gin.Context) string {
	email := getUserEmail(c)
	// No email (unauthenticated), the internal "system" actor, or the dev-bypass
	// placeholder set by authMiddleware when no API key is configured → full owner access.
	if email == "system" || email == "" || email == "dev@runright.io" {
		return "owner"
	}

	var role string
	if err := s.db.QueryRowContext(ctx,
		`SELECT role FROM sso_users WHERE email = $1`, email).Scan(&role); err == nil && role != "" {
		return role
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT role FROM team_members WHERE user_email = $1 ORDER BY created_at ASC LIMIT 1`,
		email).Scan(&role); err == nil && role != "" {
		return role
	}
	return "viewer"
}

// getPermissionsForRole returns the permissions for a named role by querying the DB.
// Falls back to ["*"] for "owner" if the DB row is missing.
func (s *Server) getPermissionsForRole(ctx context.Context, role string) []string {
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT permissions FROM roles WHERE name = $1 AND team_id IS NULL ORDER BY is_system DESC LIMIT 1`,
		role).Scan(&raw)
	if err != nil {
		// Fallback: owner gets everything, anything else gets nothing.
		if role == "owner" {
			return append([]string{"*"}, allPermissions...)
		}
		return nil
	}
	var perms []string
	_ = json.Unmarshal(raw, &perms)
	return perms
}

// hasPermissionInList reports whether the given permission slice grants perm.
func hasPermissionInList(perms []string, perm string) bool {
	for _, p := range perms {
		if p == "*" || p == perm {
			return true
		}
	}
	return false
}

// permissionsForRole returns the concrete named permissions for a role (expands "*").
func (s *Server) permissionsForRole(ctx context.Context, role string) []string {
	perms := s.getPermissionsForRole(ctx, role)
	for _, p := range perms {
		if p == "*" {
			return allPermissions
		}
	}
	return perms
}

// requirePermission returns a Gin middleware that enforces perm for the route.
func (s *Server) requirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := s.getUserRole(c.Request.Context(), c)
		perms := s.getPermissionsForRole(c.Request.Context(), role)
		if !hasPermissionInList(perms, perm) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":    "forbidden: insufficient permissions",
				"required": perm,
				"role":     role,
			})
			return
		}
		c.Next()
	}
}

// ── Role CRUD handlers ────────────────────────────────────────────────────────

// listRoles returns all roles (system + custom).
func (s *Server) listRoles(c *gin.Context) {
	rows, err := s.db.QueryContext(c.Request.Context(), `
		SELECT id, name, description, permissions, is_system, created_at, updated_at
		FROM roles
		WHERE team_id IS NULL
		ORDER BY is_system DESC, name ASC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list roles"})
		return
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		var rawPerms []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &rawPerms, &r.IsSystem, &r.CreatedAt, &r.UpdatedAt); err != nil {
			continue
		}
		_ = json.Unmarshal(rawPerms, &r.Permissions)
		if r.Permissions == nil {
			r.Permissions = []string{}
		}
		roles = append(roles, r)
	}
	if roles == nil {
		roles = []Role{}
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles, "available_permissions": allPermissions})
}

// createRole creates a new custom role.
func (s *Server) createRole(c *gin.Context) {
	var body struct {
		Name        string   `json:"name"        binding:"required"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate permissions are known strings.
	if err := validatePermissions(body.Permissions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	permsJSON, _ := json.Marshal(body.Permissions)

	var id string
	err := s.db.QueryRowContext(c.Request.Context(), `
		INSERT INTO roles (team_id, name, description, permissions, is_system)
		VALUES (NULL, $1, $2, $3, false)
		RETURNING id
	`, body.Name, body.Description, permsJSON).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "a role with that name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create role"})
		return
	}

	s.logAudit(c.Request.Context(), getUserEmail(c), c, "role.create", "role",
		id, body.Name, map[string]any{"permissions": body.Permissions})
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": body.Name})
}

// updateRole updates a role's description and/or permissions.
// System roles can have their permissions updated but cannot be renamed or deleted.
func (s *Server) updateRole(c *gin.Context) {
	roleID := c.Param("roleId")
	var body struct {
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validatePermissions(body.Permissions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	permsJSON, _ := json.Marshal(body.Permissions)

	res, err := s.db.ExecContext(c.Request.Context(), `
		UPDATE roles
		SET description = $1, permissions = $2, updated_at = NOW()
		WHERE id = $3 AND team_id IS NULL
	`, body.Description, permsJSON, roleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update role"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	s.logAudit(c.Request.Context(), getUserEmail(c), c, "role.update", "role",
		roleID, "", map[string]any{"permissions": body.Permissions})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// deleteRole deletes a custom (non-system) role.
func (s *Server) deleteRole(c *gin.Context) {
	roleID := c.Param("roleId")

	res, err := s.db.ExecContext(c.Request.Context(), `
		DELETE FROM roles WHERE id = $1 AND team_id IS NULL AND is_system = false
	`, roleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete role"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found or is a system role"})
		return
	}

	s.logAudit(c.Request.Context(), getUserEmail(c), c, "role.delete", "role",
		roleID, "", nil)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// validatePermissions returns an error if any permission string is unknown.
// "*" is only allowed for system roles and is rejected here.
func validatePermissions(perms []string) error {
	known := make(map[string]struct{}, len(allPermissions))
	for _, p := range allPermissions {
		known[p] = struct{}{}
	}
	for _, p := range perms {
		if p == "*" {
			return fmt.Errorf("wildcard permission '*' is reserved for system roles")
		}
		if _, ok := known[p]; !ok {
			return fmt.Errorf("unknown permission: %q", p)
		}
	}
	return nil
}

// isUniqueViolation checks whether an error is a Postgres unique constraint violation.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "unique")
}
