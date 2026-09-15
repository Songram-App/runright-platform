package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v62/github"
	"golang.org/x/oauth2"
	oauth2gh "golang.org/x/oauth2/github"
)

// GitHubOAuthConfig holds OAuth configuration for user authentication
type GitHubOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func getGitHubOAuthConfig() *GitHubOAuthConfig {
	return &GitHubOAuthConfig{
		ClientID:     os.Getenv("GITHUB_APP_CLIENT_ID"),
		ClientSecret: os.Getenv("GITHUB_APP_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("RUNRIGHT_BASE_URL") + "/api/v1/github/callback",
	}
}

// handleGitHubLoginStart kicks off the "sign back into this instance" GitHub
// OAuth flow (as opposed to cloudAuthGitHub, which is for creating a brand
// new RunRight Cloud workspace). The UI (LoginPage) links here directly —
// this endpoint only ever issues a redirect, never renders HTML itself.
func (s *Server) handleGitHubLoginStart(c *gin.Context) {
	cfg := getGitHubOAuthConfig()
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "GitHub login is not configured"})
		return
	}
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     oauth2gh.Endpoint,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{"read:user", "user:email"},
	}
	state, err := randomToken(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start login"})
		return
	}
	c.Redirect(http.StatusFound, oauth2Cfg.AuthCodeURL(state))
}

// handleGitHubOAuthCallback processes the OAuth callback after user authorizes the app
func (s *Server) handleGitHubOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	setupAction := c.Query("setup_action") // "install" or "update" for app installation flow

	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code parameter"})
		return
	}

	cfg := getGitHubOAuthConfig()
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "GitHub OAuth not configured"})
		return
	}

	// Exchange code for token
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     oauth2gh.Endpoint,
		RedirectURL:  cfg.RedirectURL,
	}

	token, err := oauth2Cfg.Exchange(context.Background(), code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to exchange code: %v", err)})
		return
	}

	// Get user info
	client := github.NewClient(nil).WithAuthToken(token.AccessToken)
	user, _, err := client.Users.Get(context.Background(), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get user: %v", err)})
		return
	}

	// RunRight Cloud signup rides the same registered callback URL as
	// normal dashboard login (GitHub Apps only allow one exact callback);
	// it's distinguished by a "cloud:" prefix on the state param.
	if strings.HasPrefix(c.Query("state"), cloudStatePrefix) {
		if !s.verifyCloudOAuthState(c) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired login attempt, please try again"})
			return
		}
		email := user.GetEmail()
		if email == "" {
			// GitHub hides the primary email by default unless user:email scope
			// AND a public/verified email exists; fall back to a stable address.
			email = fmt.Sprintf("%d+%s@users.noreply.github.com", user.GetID(), user.GetLogin())
		}
		s.cloudCompleteSignup(c, email, user.GetName(), user.GetAvatarURL(), "github", fmt.Sprintf("%d", user.GetID()))
		return
	}

	// Handle app installation flow
	if setupAction == "install" {
		installationID := c.Query("installation_id")
		// Return JSON success for headless/self-hosted mode
		c.JSON(http.StatusOK, gin.H{
			"status":          "installed",
			"installation_id": installationID,
			"user":            user.GetLogin(),
			"message":         "GitHub App installed successfully. You can close this page.",
		})
		return
	}

	// For regular OAuth login, create a session
	// Store the user info and token in the session
	sessionToken, err := s.createGitHubSession(user, token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create session: %v", err)})
		return
	}

	// Set session cookie
	c.SetCookie("session", sessionToken, 86400*7, "/", "", true, true)

	// Return JSON success for headless/self-hosted mode
	c.JSON(http.StatusOK, gin.H{
		"status":  "authenticated",
		"user":    user.GetLogin(),
		"message": "Login successful. You can close this page.",
	})
}

// createGitHubSession creates a session for a GitHub user
func (s *Server) createGitHubSession(user *github.User, token *oauth2.Token) (string, error) {
	// Check if user exists in sso_users
	var userID string
	err := s.db.QueryRow(`
		SELECT id FROM sso_users WHERE provider = 'github' AND provider_id = $1`,
		fmt.Sprintf("%d", user.GetID())).Scan(&userID)

	if err != nil {
		// Create new user
		email := user.GetEmail()
		if email == "" {
			email = fmt.Sprintf("%s@users.noreply.github.com", user.GetLogin())
		}

		err = s.db.QueryRow(`
			INSERT INTO sso_users (provider, provider_id, email, name, avatar_url, role)
			VALUES ('github', $1, $2, $3, $4, 'viewer')
			RETURNING id`,
			fmt.Sprintf("%d", user.GetID()), email, user.GetLogin(), user.GetAvatarURL()).Scan(&userID)
		if err != nil {
			return "", fmt.Errorf("failed to create user: %w", err)
		}
	}

	// Create session token
	sessionToken, err := generateSessionToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	// Get user email for session
	email := user.GetEmail()
	if email == "" {
		email = fmt.Sprintf("%s@users.noreply.github.com", user.GetLogin())
	}

	_, err = s.db.Exec(`
		INSERT INTO sso_sessions (token, user_id, email, provider, expires_at)
		VALUES ($1, $2, $3, 'github', NOW() + INTERVAL '7 days')`,
		sessionToken, userID, email)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return sessionToken, nil
}

// listWorkflowRuns is a wrapper that calls GitHubApp.ListWorkflowRuns
func (s *Server) listWorkflowRuns(c *gin.Context) {
	s.githubApp.ListWorkflowRuns(c)
}

// injectWorkflowAction handles requests to inject RunRight into a workflow
func (s *Server) injectWorkflowAction(c *gin.Context) {
	var req InjectWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := s.githubApp.InjectWorkflow(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Log audit event
	s.logAudit(context.Background(), "system", c, "github_workflow_inject", "workflow", req.Repository, req.Repository, map[string]interface{}{
		"pr_url":    result.PRURL,
		"pr_number": result.PRNumber,
	})

	c.JSON(http.StatusOK, result)
}

// GetGitHubLoginURL returns the OAuth URL for GitHub login
func (s *Server) GetGitHubLoginURL(c *gin.Context) {
	cfg := getGitHubOAuthConfig()
	if cfg.ClientID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "GitHub OAuth not configured"})
		return
	}

	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     oauth2gh.Endpoint,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{"user:email", "read:org"},
	}

	state := c.Query("redirect")
	if state == "" {
		state = "login"
	}

	url := oauth2Cfg.AuthCodeURL(state)
	c.JSON(http.StatusOK, gin.H{"url": url})
}
