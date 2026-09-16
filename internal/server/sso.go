package server

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/crewjam/saml/samlsp"
	"github.com/gin-gonic/gin"
	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/azureadv2"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
	"github.com/markbates/goth/providers/okta"
	"github.com/markbates/goth/providers/openidConnect"
)

// SSOProviderType represents supported SSO provider types.
type SSOProviderType string

const (
	SSOProviderGoogle   SSOProviderType = "google"
	SSOProviderGitHub   SSOProviderType = "github"
	SSOProviderAzureAD  SSOProviderType = "azuread"
	SSOProviderOkta     SSOProviderType = "okta"
	SSOProviderOIDC     SSOProviderType = "oidc"
	SSOProviderSAML     SSOProviderType = "saml"
	SSOProviderDemo     SSOProviderType = "demo" // Built-in demo provider for testing
)

// SSOConfig holds the configuration for an SSO provider.
type SSOConfig struct {
	ID           int             `json:"id"`
	ProviderType SSOProviderType `json:"provider_type"`
	Name         string          `json:"name"`
	Enabled      bool            `json:"enabled"`

	// OAuth2/OIDC settings
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"-"` // Never expose in JSON
	AuthURL      string `json:"auth_url,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	IssuerURL    string `json:"issuer_url,omitempty"` // For OIDC auto-discovery
	Scopes       string `json:"scopes,omitempty"`

	// SAML settings
	IDPMetadataURL string `json:"idp_metadata_url,omitempty"`
	IDPMetadata    string `json:"-"` // Raw XML metadata
	SPEntityID     string `json:"sp_entity_id,omitempty"`

	// Common settings
	AllowedDomains string `json:"allowed_domains,omitempty"` // Comma-separated list
	DefaultRole    string `json:"default_role,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SSOUser represents an authenticated SSO user.
type SSOUser struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	Provider      string    `json:"provider"`
	ProviderID    string    `json:"provider_id"`
	Groups        []string  `json:"groups,omitempty"`
	Role          string    `json:"role"`
	LastLoginAt   time.Time `json:"last_login_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// SSOSession stores session data for SSO-authenticated users.
type SSOSession struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Provider  string    `json:"provider"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ssoManager handles SSO provider registration and authentication.
type ssoManager struct {
	db          *sql.DB
	baseURL     string
	samlSP      *samlsp.Middleware
	initialized bool
}

// newSSOManager creates a new SSO manager.
func newSSOManager(db *sql.DB, baseURL string) *ssoManager {
	return &ssoManager{
		db:      db,
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}
}

// Initialize sets up all configured SSO providers.
func (m *ssoManager) Initialize(ctx context.Context) error {
	configs, err := m.loadConfigs(ctx)
	if err != nil {
		return fmt.Errorf("loading SSO configs: %w", err)
	}

	var providers []goth.Provider
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		provider, err := m.createProvider(cfg)
		if err != nil {
			fmt.Printf("warning: failed to initialize SSO provider %s: %v\n", cfg.Name, err)
			continue
		}
		if provider != nil {
			providers = append(providers, provider)
		}

		// Initialize SAML separately
		if cfg.ProviderType == SSOProviderSAML {
			if err := m.initializeSAML(cfg); err != nil {
				fmt.Printf("warning: failed to initialize SAML provider %s: %v\n", cfg.Name, err)
			}
		}
	}

	if len(providers) > 0 {
		goth.UseProviders(providers...)
	}

	m.initialized = true
	return nil
}

// loadConfigs loads all SSO configurations from the database.
func (m *ssoManager) loadConfigs(ctx context.Context) ([]SSOConfig, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, provider_type, name, enabled, client_id, client_secret,
		       auth_url, token_url, issuer_url, scopes,
		       idp_metadata_url, idp_metadata, sp_entity_id,
		       allowed_domains, default_role, created_at, updated_at
		FROM sso_providers
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []SSOConfig
	for rows.Next() {
		var cfg SSOConfig
		var clientSecret, idpMetadata sql.NullString
		err := rows.Scan(
			&cfg.ID, &cfg.ProviderType, &cfg.Name, &cfg.Enabled,
			&cfg.ClientID, &clientSecret, &cfg.AuthURL, &cfg.TokenURL,
			&cfg.IssuerURL, &cfg.Scopes, &cfg.IDPMetadataURL, &idpMetadata,
			&cfg.SPEntityID, &cfg.AllowedDomains, &cfg.DefaultRole,
			&cfg.CreatedAt, &cfg.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		cfg.ClientSecret = clientSecret.String
		cfg.IDPMetadata = idpMetadata.String
		configs = append(configs, cfg)
	}
	return configs, rows.Err()
}

// createProvider creates a goth provider from configuration.
func (m *ssoManager) createProvider(cfg SSOConfig) (goth.Provider, error) {
	callbackURL := fmt.Sprintf("%s/api/v1/sso/callback/%s", m.baseURL, cfg.ProviderType)
	scopes := strings.Split(cfg.Scopes, ",")
	if len(scopes) == 1 && scopes[0] == "" {
		scopes = []string{"email", "profile"}
	}

	switch cfg.ProviderType {
	case SSOProviderGoogle:
		return google.New(cfg.ClientID, cfg.ClientSecret, callbackURL, scopes...), nil

	case SSOProviderGitHub:
		return github.New(cfg.ClientID, cfg.ClientSecret, callbackURL, scopes...), nil

	case SSOProviderAzureAD:
		// Convert string scopes to Azure AD scope types
		azureScopes := make([]azureadv2.ScopeType, len(scopes))
		for i, s := range scopes {
			azureScopes[i] = azureadv2.ScopeType(s)
		}
		return azureadv2.New(cfg.ClientID, cfg.ClientSecret, callbackURL,
			azureadv2.ProviderOptions{Scopes: azureScopes}), nil

	case SSOProviderOkta:
		if cfg.IssuerURL == "" {
			return nil, fmt.Errorf("okta requires issuer_url")
		}
		return okta.New(cfg.ClientID, cfg.ClientSecret, cfg.IssuerURL, callbackURL, scopes...), nil

	case SSOProviderOIDC:
		if cfg.IssuerURL == "" {
			return nil, fmt.Errorf("oidc requires issuer_url")
		}
		provider, err := openidConnect.New(cfg.ClientID, cfg.ClientSecret, callbackURL, cfg.IssuerURL, scopes...)
		if err != nil {
			return nil, fmt.Errorf("creating OIDC provider: %w", err)
		}
		return provider, nil

	case SSOProviderSAML:
		// SAML is handled separately
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown provider type: %s", cfg.ProviderType)
	}
}

// initializeSAML sets up SAML service provider.
func (m *ssoManager) initializeSAML(cfg SSOConfig) error {
	// Load or generate X.509 certificate for SAML
	certPath := os.Getenv("RUNRIGHT_SAML_CERT")
	keyPath := os.Getenv("RUNRIGHT_SAML_KEY")

	if certPath == "" || keyPath == "" {
		return fmt.Errorf("SAML requires RUNRIGHT_SAML_CERT and RUNRIGHT_SAML_KEY environment variables")
	}

	keyPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("loading SAML certificate: %w", err)
	}

	keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parsing SAML certificate: %w", err)
	}

	idpMetadataURL, err := url.Parse(cfg.IDPMetadataURL)
	if err != nil {
		return fmt.Errorf("parsing IDP metadata URL: %w", err)
	}

	idpMetadata, err := samlsp.FetchMetadata(context.Background(), http.DefaultClient, *idpMetadataURL)
	if err != nil {
		return fmt.Errorf("fetching IDP metadata: %w", err)
	}

	rootURL, err := url.Parse(m.baseURL)
	if err != nil {
		return fmt.Errorf("parsing base URL: %w", err)
	}

	samlSP, err := samlsp.New(samlsp.Options{
		URL:         *rootURL,
		Key:         keyPair.PrivateKey.(*rsa.PrivateKey),
		Certificate: keyPair.Leaf,
		IDPMetadata: idpMetadata,
		EntityID:    cfg.SPEntityID,
	})
	if err != nil {
		return fmt.Errorf("creating SAML SP: %w", err)
	}

	m.samlSP = samlSP
	return nil
}

// --- HTTP Handlers ---

// ssoListProviders returns available SSO providers.
func (s *Server) ssoListProviders(c *gin.Context) {
	ctx := c.Request.Context()

	rows, err := s.db.QueryContext(ctx, `
		SELECT provider_type, name, enabled 
		FROM sso_providers 
		WHERE enabled = true
		ORDER BY name
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list providers"})
		return
	}
	defer rows.Close()

	var providers []gin.H
	for rows.Next() {
		var providerType, name string
		var enabled bool
		if err := rows.Scan(&providerType, &name, &enabled); err != nil {
			continue
		}
		providers = append(providers, gin.H{
			"provider_type": providerType,
			"name":          name,
			"login_url":     fmt.Sprintf("/api/v1/sso/login/%s", providerType),
		})
	}

	// If no providers configured, offer the demo provider for easy local
	// testing — but never on a managed Cloud tenant, where it would be an
	// unauthenticated instant-admin backdoor on a real customer's instance.
	if len(providers) == 0 && os.Getenv("RUNRIGHT_TENANT_SLUG") == "" {
		providers = append(providers, gin.H{
			"provider_type": "demo",
			"name":          "SSO",
			"login_url":     "/api/v1/sso/login/demo",
		})
	}

	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

// ssoLogin initiates SSO login flow.
func (s *Server) ssoLogin(c *gin.Context) {
	providerName := c.Param("provider")

	// Handle demo provider - instant login without OAuth, self-hosted only
	// (see ssoListProviders — a managed Cloud tenant must never expose this).
	if providerName == string(SSOProviderDemo) {
		if os.Getenv("RUNRIGHT_TENANT_SLUG") != "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider: demo"})
			return
		}
		s.ssoDemoLogin(c)
		return
	}

	// Store return URL in session
	returnURL := c.Query("return_url")
	if returnURL == "" {
		returnURL = "/"
	}

	// For SAML, redirect to SAML handler
	if providerName == string(SSOProviderSAML) {
		if s.ssoMgr != nil && s.ssoMgr.samlSP != nil {
			s.ssoMgr.samlSP.HandleStartAuthFlow(c.Writer, c.Request)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "SAML not configured"})
		return
	}

	// Get goth provider
	provider, err := goth.GetProvider(providerName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown provider: %s", providerName)})
		return
	}

	// Start OAuth flow
	sess, err := provider.BeginAuth(generateState())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start auth"})
		return
	}

	authURL, err := sess.GetAuthURL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get auth URL"})
		return
	}

	// Store session for callback
	sessionStoreMu.Lock()
	sessionStore[sess.Marshal()] = returnURL
	sessionStoreMu.Unlock()

	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// ssoDemoLogin handles demo/test login without external OAuth.
// Creates a demo user session for easy local testing.
func (s *Server) ssoDemoLogin(c *gin.Context) {
	ctx := c.Request.Context()

	// Create demo user
	demoUser := &SSOUser{
		Email:       "demo@runright.local",
		Name:        "Demo User",
		AvatarURL:   "",
		Provider:    "demo",
		ProviderID:  "demo-user-1",
		Role:        "admin", // Give demo user full access
		LastLoginAt: time.Now(),
	}

	// Upsert demo user in database
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO sso_users (email, name, avatar_url, provider, provider_id, role, last_login_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			last_login_at = EXCLUDED.last_login_at
		RETURNING id
	`, demoUser.Email, demoUser.Name, demoUser.AvatarURL, demoUser.Provider, demoUser.ProviderID, demoUser.Role, demoUser.LastLoginAt).Scan(&demoUser.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create demo user"})
		return
	}

	// Generate session token
	sessionToken, err := generateSessionToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	// Store SSO session
	ssoSession := SSOSession{
		Token:     sessionToken,
		UserID:    demoUser.ID,
		Email:     demoUser.Email,
		Provider:  "demo",
		ExpiresAt: time.Now().Add(24 * time.Hour * 30),
	}
	if err := s.saveSSOSession(ctx, ssoSession); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	// Set session cookie
	secure := isSecureContext(c)
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookie, sessionToken, 86400*30, "/", "", secure, true)

	// Redirect to dashboard
	c.Redirect(http.StatusTemporaryRedirect, "/")
}

// ssoCallback handles OAuth2/OIDC callback.
func (s *Server) ssoCallback(c *gin.Context) {
	providerName := c.Param("provider")

	// Handle SAML ACS
	if providerName == string(SSOProviderSAML) {
		if s.ssoMgr != nil && s.ssoMgr.samlSP != nil {
			s.ssoMgr.samlSP.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "SAML not configured"})
		return
	}

	provider, err := goth.GetProvider(providerName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown provider: %s", providerName)})
		return
	}

	// Exchange code for token
	sess, err := provider.BeginAuth(c.Query("state"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "auth session error"})
		return
	}

	_, err = sess.Authorize(provider, c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authorization failed"})
		return
	}

	// Fetch user info
	gothUser, err := provider.FetchUser(sess)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user info"})
		return
	}

	// Validate domain if configured
	if err := s.validateSSODomain(c.Request.Context(), providerName, gothUser.Email); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	// Create or update user in database
	user, err := s.upsertSSOUser(c.Request.Context(), providerName, gothUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save user"})
		return
	}

	// Generate session token
	sessionToken, err := generateSessionToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	// Store SSO session
	ssoSession := SSOSession{
		Token:     sessionToken,
		UserID:    user.ID,
		Email:     user.Email,
		Provider:  providerName,
		ExpiresAt: time.Now().Add(24 * time.Hour * 30), // 30 days
	}
	if err := s.saveSSOSession(c.Request.Context(), ssoSession); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	// Set session cookie
	secure := isSecureContext(c)
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookie, sessionToken, 86400*30, "/", "", secure, true)

	// Redirect to return URL or dashboard
	returnURL := "/"
	c.Redirect(http.StatusTemporaryRedirect, returnURL)
}

// validateSSODomain checks if the user's email domain is allowed.
func (s *Server) validateSSODomain(ctx context.Context, provider, email string) error {
	var allowedDomains sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT allowed_domains FROM sso_providers 
		WHERE provider_type = $1 AND enabled = true
	`, provider).Scan(&allowedDomains)

	if err != nil {
		return nil // No restriction if not found
	}

	if !allowedDomains.Valid || allowedDomains.String == "" {
		return nil // No domain restriction
	}

	emailParts := strings.Split(email, "@")
	if len(emailParts) != 2 {
		return fmt.Errorf("invalid email format")
	}
	userDomain := strings.ToLower(emailParts[1])

	domains := strings.Split(allowedDomains.String, ",")
	for _, d := range domains {
		if strings.TrimSpace(strings.ToLower(d)) == userDomain {
			return nil
		}
	}

	return fmt.Errorf("email domain not allowed")
}

// upsertSSOUser creates or updates an SSO user.
func (s *Server) upsertSSOUser(ctx context.Context, provider string, gothUser goth.User) (*SSOUser, error) {
	var defaultRole string
	err := s.db.QueryRowContext(ctx, `
		SELECT default_role FROM sso_providers 
		WHERE provider_type = $1 AND enabled = true
	`, provider).Scan(&defaultRole)
	if err != nil || defaultRole == "" {
		defaultRole = "viewer"
	}

	user := &SSOUser{
		Email:       gothUser.Email,
		Name:        gothUser.Name,
		AvatarURL:   gothUser.AvatarURL,
		Provider:    provider,
		ProviderID:  gothUser.UserID,
		Role:        defaultRole,
		LastLoginAt: time.Now(),
	}

	// Upsert user
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO sso_users (email, name, avatar_url, provider, provider_id, role, last_login_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			avatar_url = EXCLUDED.avatar_url,
			last_login_at = EXCLUDED.last_login_at
		RETURNING id, created_at
	`, user.Email, user.Name, user.AvatarURL, user.Provider, user.ProviderID,
		user.Role, user.LastLoginAt).Scan(&user.ID, &user.CreatedAt)

	if err != nil {
		return nil, err
	}

	return user, nil
}

// saveSSOSession stores an SSO session.
func (s *Server) saveSSOSession(ctx context.Context, sess SSOSession) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sso_sessions (token, user_id, email, provider, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (token) DO UPDATE SET expires_at = EXCLUDED.expires_at
	`, sess.Token, sess.UserID, sess.Email, sess.Provider, sess.ExpiresAt)
	return err
}

// validateSSOSession checks if a session token is valid.
func (s *Server) validateSSOSession(ctx context.Context, token string) (*SSOSession, error) {
	var sess SSOSession
	err := s.db.QueryRowContext(ctx, `
		SELECT token, user_id, email, provider, expires_at
		FROM sso_sessions
		WHERE token = $1 AND expires_at > NOW()
	`, token).Scan(&sess.Token, &sess.UserID, &sess.Email, &sess.Provider, &sess.ExpiresAt)

	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// ssoLogout handles SSO logout.
func (s *Server) ssoLogout(c *gin.Context) {
	token, err := c.Cookie(sessionCookie)
	if err == nil && token != "" {
		// Delete SSO session
		s.db.ExecContext(c.Request.Context(), `DELETE FROM sso_sessions WHERE token = $1`, token)

		// Also remove from in-memory session store
		sessionStoreMu.Lock()
		delete(sessionStore, token)
		sessionStoreMu.Unlock()
	}

	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookie, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"status": "logged out"})
}

// ssoMe returns the current user's info.
func (s *Server) ssoMe(c *gin.Context) {
	token, err := c.Cookie(sessionCookie)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	sess, err := s.validateSSOSession(c.Request.Context(), token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	// Get full user info
	var user SSOUser
	err = s.db.QueryRowContext(c.Request.Context(), `
		SELECT id, email, name, avatar_url, provider, provider_id, role, last_login_at, created_at
		FROM sso_users WHERE id = $1
	`, sess.UserID).Scan(
		&user.ID, &user.Email, &user.Name, &user.AvatarURL,
		&user.Provider, &user.ProviderID, &user.Role,
		&user.LastLoginAt, &user.CreatedAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load user"})
		return
	}

	c.JSON(http.StatusOK, user)
}

// --- Admin endpoints for SSO configuration ---

// ssoListConfigs returns all SSO configurations (admin only).
func (s *Server) ssoListConfigs(c *gin.Context) {
	ctx := c.Request.Context()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider_type, name, enabled, client_id,
		       auth_url, token_url, issuer_url, scopes,
		       idp_metadata_url, sp_entity_id,
		       allowed_domains, default_role, created_at, updated_at
		FROM sso_providers
		ORDER BY name
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list configs"})
		return
	}
	defer rows.Close()

	var configs []SSOConfig
	for rows.Next() {
		var cfg SSOConfig
		err := rows.Scan(
			&cfg.ID, &cfg.ProviderType, &cfg.Name, &cfg.Enabled, &cfg.ClientID,
			&cfg.AuthURL, &cfg.TokenURL, &cfg.IssuerURL, &cfg.Scopes,
			&cfg.IDPMetadataURL, &cfg.SPEntityID,
			&cfg.AllowedDomains, &cfg.DefaultRole, &cfg.CreatedAt, &cfg.UpdatedAt,
		)
		if err != nil {
			continue
		}
		configs = append(configs, cfg)
	}

	c.JSON(http.StatusOK, gin.H{"configs": configs})
}

// ssoUpsertConfig creates or updates an SSO configuration.
func (s *Server) ssoUpsertConfig(c *gin.Context) {
	var cfg struct {
		ID             int             `json:"id"`
		ProviderType   SSOProviderType `json:"provider_type"`
		Name           string          `json:"name"`
		Enabled        bool            `json:"enabled"`
		ClientID       string          `json:"client_id"`
		ClientSecret   string          `json:"client_secret"`
		AuthURL        string          `json:"auth_url"`
		TokenURL       string          `json:"token_url"`
		IssuerURL      string          `json:"issuer_url"`
		Scopes         string          `json:"scopes"`
		IDPMetadataURL string          `json:"idp_metadata_url"`
		SPEntityID     string          `json:"sp_entity_id"`
		AllowedDomains string          `json:"allowed_domains"`
		DefaultRole    string          `json:"default_role"`
	}

	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if cfg.Name == "" || cfg.ProviderType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and provider_type are required"})
		return
	}

	ctx := c.Request.Context()
	var id int

	if cfg.ID == 0 {
		// Insert new config
		err := s.db.QueryRowContext(ctx, `
			INSERT INTO sso_providers (
				provider_type, name, enabled, client_id, client_secret,
				auth_url, token_url, issuer_url, scopes,
				idp_metadata_url, sp_entity_id, allowed_domains, default_role,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())
			RETURNING id
		`, cfg.ProviderType, cfg.Name, cfg.Enabled, cfg.ClientID, cfg.ClientSecret,
			cfg.AuthURL, cfg.TokenURL, cfg.IssuerURL, cfg.Scopes,
			cfg.IDPMetadataURL, cfg.SPEntityID, cfg.AllowedDomains, cfg.DefaultRole,
		).Scan(&id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create config"})
			return
		}
	} else {
		// Update existing config
		id = cfg.ID
		query := `
			UPDATE sso_providers SET
				name = $2, enabled = $3, client_id = $4,
				auth_url = $5, token_url = $6, issuer_url = $7, scopes = $8,
				idp_metadata_url = $9, sp_entity_id = $10,
				allowed_domains = $11, default_role = $12, updated_at = NOW()
		`
		args := []any{cfg.ID, cfg.Name, cfg.Enabled, cfg.ClientID,
			cfg.AuthURL, cfg.TokenURL, cfg.IssuerURL, cfg.Scopes,
			cfg.IDPMetadataURL, cfg.SPEntityID, cfg.AllowedDomains, cfg.DefaultRole}

		// Only update secret if provided
		if cfg.ClientSecret != "" {
			query += `, client_secret = $13`
			args = append(args, cfg.ClientSecret)
		}
		query += ` WHERE id = $1`

		_, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update config"})
			return
		}
	}

	// Reinitialize SSO providers
	if s.ssoMgr != nil {
		go s.ssoMgr.Initialize(context.Background())
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "status": "ok"})
}

// ssoDeleteConfig deletes an SSO configuration.
func (s *Server) ssoDeleteConfig(c *gin.Context) {
	var req struct {
		ID int `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := s.db.ExecContext(c.Request.Context(), `DELETE FROM sso_providers WHERE id = $1`, req.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete config"})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "sso.config.delete", "sso_provider",
		fmt.Sprintf("%d", req.ID), "", nil)

	// Reinitialize SSO providers
	if s.ssoMgr != nil {
		go s.ssoMgr.Initialize(context.Background())
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ssoTestConfig tests an SSO configuration without saving.
func (s *Server) ssoTestConfig(c *gin.Context) {
	var cfg SSOConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if s.ssoMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSO is not enabled on this instance", "valid": false})
		return
	}

	testMgr := newSSOManager(s.db, s.ssoMgr.baseURL)
	provider, err := testMgr.createProvider(cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "valid": false})
		return
	}

	if provider == nil && cfg.ProviderType != SSOProviderSAML {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to create provider", "valid": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"valid": true, "message": "Configuration appears valid"})
}

// generateState generates a random state parameter for OAuth.
func generateState() string {
	token, _ := generateSessionToken()
	return token[:16]
}

// listUsers returns all users from sso_users so admins can see who has logged in and manage their roles.
func (s *Server) listUsers(c *gin.Context) {
	rows, err := s.db.QueryContext(c.Request.Context(), `
		SELECT id, email, name, avatar_url, provider, provider_id, role, last_login_at, created_at
		FROM sso_users
		ORDER BY last_login_at DESC NULLS LAST
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}
	defer rows.Close()

	var users []SSOUser
	for rows.Next() {
		var u SSOUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL,
			&u.Provider, &u.ProviderID, &u.Role, &u.LastLoginAt, &u.CreatedAt); err != nil {
			continue
		}
		users = append(users, u)
	}
	if users == nil {
		users = []SSOUser{}
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// updateUserRole changes the role of a user identified by their email.
func (s *Server) updateUserRole(c *gin.Context) {
	email := c.Param("email")
	var body struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role is required"})
		return
	}

	// Validate role exists in the roles table.
	var exists bool
	if err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM roles WHERE name = $1 AND team_id IS NULL)`, body.Role).Scan(&exists); err != nil || !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown role: " + body.Role})
		return
	}

	result, err := s.db.ExecContext(c.Request.Context(),
		`UPDATE sso_users SET role = $1 WHERE email = $2`, body.Role, email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update role"})
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	s.logAudit(c.Request.Context(), getUserEmail(c), c, "user.role.update", "user",
		email, email, map[string]any{"role": body.Role})
	c.JSON(http.StatusOK, gin.H{"status": "ok", "email": email, "role": body.Role})
}
