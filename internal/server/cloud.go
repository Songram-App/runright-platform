package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	oauth2gh "golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

// This file implements the self-serve "RunRight Cloud" signup flow: a
// customer clicks "Start Free", authenticates with GitHub or Google, and we
// provision a dedicated Fly app + Postgres database for them (see
// provision.go). Every tenant gets fully isolated compute and data — nothing
// here shares database rows with a customer's actual CI job data.

// cloudCfg is the environment-based configuration for cloud signup. Reads are
// lazy since this only runs on signup/claim requests, not the hot path.
type cloudCfg struct {
	enabled            bool
	baseURL            string
	googleClientID     string
	googleClientSecret string
	controlPlaneSecret string
}

func getCloudConfig() cloudCfg {
	return cloudCfg{
		enabled:            strings.EqualFold(strings.TrimSpace(os.Getenv("RUNRIGHT_CLOUD_ENABLED")), "true"),
		baseURL:            strings.TrimRight(os.Getenv("RUNRIGHT_BASE_URL"), "/"),
		googleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		googleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		controlPlaneSecret: os.Getenv("CLOUD_CONTROL_PLANE_SECRET"),
	}
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- OAuth state cookie helpers ---

const cloudStateCookie = "rr_cloud_state"

func (s *Server) startCloudOAuthState(c *gin.Context) (string, bool) {
	state, err := randomToken(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start login"})
		return "", false
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(cloudStateCookie, state, 600, "/", "", isSecureContext(c), true)
	return state, true
}

func (s *Server) verifyCloudOAuthState(c *gin.Context) (desiredSlug string, ok bool) {
	cookie, err := c.Cookie(cloudStateCookie)
	c.SetCookie(cloudStateCookie, "", -1, "/", "", isSecureContext(c), true)
	if err != nil || cookie == "" {
		return "", false
	}
	queryState := strings.TrimPrefix(c.Query("state"), cloudStatePrefix)
	desiredSlug, random := decodeCloudState(queryState)
	if subtle.ConstantTimeCompare([]byte(cookie), []byte(random)) != 1 {
		return "", false
	}
	return desiredSlug, true
}

// encodeCloudState/decodeCloudState let the signup chooser (CloudStartPage)
// pass along a customer-requested workspace name (e.g. "walmart") through the
// OAuth round trip, so the resulting tenant slug can be rr-walmart instead of
// an unrelated random one — much easier to recognize operationally later.
func encodeCloudState(desiredSlug, random string) string {
	if desiredSlug == "" {
		return random
	}
	return desiredSlug + ":" + random
}

func decodeCloudState(raw string) (desiredSlug, random string) {
	if idx := strings.LastIndex(raw, ":"); idx != -1 {
		return raw[:idx], raw[idx+1:]
	}
	return "", raw
}

// slugSanitizeRe/slugCollapseHyphenRe turn a free-typed company/workspace
// name into a URL-safe slug fragment (rr-<slug>.fly.dev).
var slugSanitizeRe = regexp.MustCompile(`[^a-z0-9-]+`)
var slugCollapseHyphenRe = regexp.MustCompile(`-{2,}`)

func sanitizeSlugCandidate(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = slugSanitizeRe.ReplaceAllString(v, "-")
	v = slugCollapseHyphenRe.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-")
	if len(v) > 30 {
		v = strings.Trim(v[:30], "-")
	}
	return v
}

// reservedSlugs blocks names that would be confusing or collide with our own
// infrastructure/routes if used as a tenant slug (rr-<slug>.fly.dev).
var reservedSlugs = map[string]bool{
	"www": true, "api": true, "app": true, "admin": true, "cloud": true,
	"runright": true, "status": true, "health": true, "mail": true, "ftp": true,
	"root": true, "system": true, "internal": true, "fly": true, "billing": true,
	"support": true, "help": true, "assets": true, "static": true, "rr": true,
}

// --- GitHub signup (reuses the existing GitHub App OAuth credentials AND its
// existing callback URL — GitHub Apps only allow one exact registered
// callback, so the cloud-vs-normal-login distinction rides in the `state`
// param instead of a separate route; see handleGitHubOAuthCallback) ---

const cloudStatePrefix = "cloud:"

func (s *Server) cloudAuthGitHub(c *gin.Context) {
	cfg := getCloudConfig()
	if !cfg.enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "cloud signup is not enabled"})
		return
	}
	ghCfg := getGitHubOAuthConfig()
	if ghCfg.ClientID == "" || ghCfg.ClientSecret == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "GitHub login is not configured"})
		return
	}
	state, ok := s.startCloudOAuthState(c)
	if !ok {
		return
	}
	desiredSlug := sanitizeSlugCandidate(c.Query("slug"))
	oauth2Cfg := &oauth2.Config{
		ClientID:     ghCfg.ClientID,
		ClientSecret: ghCfg.ClientSecret,
		Endpoint:     oauth2gh.Endpoint,
		RedirectURL:  ghCfg.RedirectURL, // existing /api/v1/github/callback
		Scopes:       []string{"read:user", "user:email"},
	}
	c.Redirect(http.StatusFound, oauth2Cfg.AuthCodeURL(cloudStatePrefix+encodeCloudState(desiredSlug, state)))
}

func (s *Server) cloudAuthGoogle(c *gin.Context) {
	cfg := getCloudConfig()
	if !cfg.enabled || cfg.googleClientID == "" || cfg.googleClientSecret == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Google login is not configured"})
		return
	}
	state, ok := s.startCloudOAuthState(c)
	if !ok {
		return
	}
	desiredSlug := sanitizeSlugCandidate(c.Query("slug"))
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.googleClientID,
		ClientSecret: cfg.googleClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  cfg.baseURL + "/api/v1/cloud/auth/google/callback",
		Scopes:       []string{"openid", "email", "profile"},
	}
	c.Redirect(http.StatusFound, oauth2Cfg.AuthCodeURL(encodeCloudState(desiredSlug, state)))
}

func (s *Server) cloudAuthGoogleCallback(c *gin.Context) {
	cfg := getCloudConfig()
	if !cfg.enabled || cfg.googleClientID == "" || cfg.googleClientSecret == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Google login is not configured"})
		return
	}
	desiredSlug, ok := s.verifyCloudOAuthState(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired login attempt, please try again"})
		return
	}
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code parameter"})
		return
	}
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.googleClientID,
		ClientSecret: cfg.googleClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  cfg.baseURL + "/api/v1/cloud/auth/google/callback",
	}
	token, err := oauth2Cfg.Exchange(c.Request.Context(), code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to exchange code"})
		return
	}
	httpClient := oauth2Cfg.Client(c.Request.Context(), token)
	resp, err := httpClient.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch Google profile"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch Google profile"})
		return
	}
	var profile struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil || profile.Email == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse Google profile"})
		return
	}
	s.cloudCompleteSignup(c, profile.Email, profile.Name, profile.Picture, "google", profile.Sub, desiredSlug)
}

// --- Shared signup completion: upsert customer, ensure tenant, kick off provisioning ---

func (s *Server) cloudCompleteSignup(c *gin.Context, email, name, avatarURL, provider, providerUID, desiredSlug string) {
	ctx := c.Request.Context()
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "your account has no accessible email address"})
		return
	}

	var customerID string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO cloud_customers (email, name, avatar_url, auth_provider, provider_uid, last_login_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (auth_provider, provider_uid) DO UPDATE SET
			name = EXCLUDED.name, avatar_url = EXCLUDED.avatar_url, last_login_at = NOW()
		RETURNING id
	`, email, name, avatarURL, provider, providerUID).Scan(&customerID)
	if err != nil {
		fmt.Printf("[cloud] upsert customer failed for %s: %v\n", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create account"})
		return
	}

	var tenantID, slug, status string
	err = s.db.QueryRowContext(ctx,
		`SELECT id, slug, status FROM cloud_tenants WHERE customer_id = $1 ORDER BY created_at ASC LIMIT 1`,
		customerID).Scan(&tenantID, &slug, &status)
	switch {
	case err == sql.ErrNoRows:
		var slugErr error
		slug, slugErr = s.reserveTenantSlugPreferred(ctx, desiredSlug)
		if slugErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate a workspace"})
			return
		}
		appName := "rr-" + slug
		dbName := "rr_" + strings.ReplaceAll(slug, "-", "_")
		baseURL := fmt.Sprintf("https://%s.fly.dev", appName)
		insertErr := s.db.QueryRowContext(ctx, `
			INSERT INTO cloud_tenants (customer_id, slug, fly_app_name, db_name, base_url, status)
			VALUES ($1, $2, $3, $4, $5, 'provisioning')
			RETURNING id
		`, customerID, slug, appName, dbName, baseURL).Scan(&tenantID)
		if insertErr != nil {
			fmt.Printf("[cloud] insert tenant failed for %s: %v\n", email, insertErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workspace"})
			return
		}
		s.logAudit(ctx, email, c, "cloud.signup", "cloud_tenant", tenantID, slug, nil)
		go s.provisionTenantAsync(tenantID, slug)
	case err != nil:
		fmt.Printf("[cloud] look up tenant failed for %s: %v\n", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to look up workspace"})
		return
	case status == "failed":
		// A previous attempt died partway through (e.g. infra hiccup). Retry
		// using the same slug/row — createTenantDatabase and `fly apps
		// create` are both safe to re-run against partially-provisioned state.
		if _, resetErr := s.db.ExecContext(ctx,
			`UPDATE cloud_tenants SET status = 'provisioning', error_message = NULL WHERE id = $1`,
			tenantID); resetErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retry workspace setup"})
			return
		}
		go s.provisionTenantAsync(tenantID, slug)
	}

	// /cloud/wait is a UI route (React), not an API one — it polls
	// /api/v1/cloud/status itself and redirects to the claim_url when ready.
	c.Redirect(http.StatusFound, "/cloud/wait?tenant="+tenantID)
}

// slugAdjectives/slugNouns generate a friendly, random workspace slug (e.g.
// "swift-otter-4f2a") instead of deriving one from the customer's GitHub
// handle or email — keeps their identity out of a public URL.
var slugAdjectives = []string{
	"swift", "calm", "brave", "quiet", "bold", "lucky", "bright", "gentle",
	"keen", "merry", "rapid", "sunny", "vivid", "witty", "amber", "coral",
	"ember", "frost", "onyx", "cedar",
}
var slugNouns = []string{
	"otter", "falcon", "harbor", "meadow", "comet", "heron", "tundra",
	"lagoon", "willow", "canyon", "ridge", "orchid", "summit", "brook",
	"atlas", "delta", "nova", "reef", "grove", "crest",
}

func randomSlugWord(words []string) (string, error) {
	b := make([]byte, 1)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return words[int(b[0])%len(words)], nil
}

func (s *Server) reserveTenantSlug(ctx context.Context) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		adj, err := randomSlugWord(slugAdjectives)
		if err != nil {
			return "", err
		}
		noun, err := randomSlugWord(slugNouns)
		if err != nil {
			return "", err
		}
		suffix, err := randomToken(2)
		if err != nil {
			return "", err
		}
		candidate := fmt.Sprintf("%s-%s-%s", adj, noun, suffix)
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cloud_tenants WHERE slug = $1)`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate a unique workspace slug")
}

// reserveTenantSlugPreferred tries to honor a customer-requested workspace
// name (e.g. "walmart" -> rr-walmart) before falling back to a fully random
// slug. Tries the base name, then base-2/base-3/..., then base-<random>,
// and only gives up on the request entirely (never on allocating a slug) if
// every friendly variant is somehow taken.
func (s *Server) reserveTenantSlugPreferred(ctx context.Context, desired string) (string, error) {
	base := sanitizeSlugCandidate(desired)
	if base == "" || len(base) < 2 || reservedSlugs[base] {
		return s.reserveTenantSlug(ctx)
	}

	slugTaken := func(candidate string) (bool, error) {
		var exists bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cloud_tenants WHERE slug = $1)`, candidate).Scan(&exists)
		return exists, err
	}

	candidates := []string{base}
	for i := 2; i <= 5; i++ {
		candidates = append(candidates, fmt.Sprintf("%s-%d", base, i))
	}
	for _, candidate := range candidates {
		taken, err := slugTaken(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}

	for attempt := 0; attempt < 5; attempt++ {
		suffix, err := randomToken(2)
		if err != nil {
			return "", err
		}
		candidate := fmt.Sprintf("%s-%s", base, suffix)
		taken, err := slugTaken(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	// Every friendly variant is taken — fall back rather than fail signup.
	return s.reserveTenantSlug(ctx)
}

// --- Status polling + handoff page ---

func (s *Server) cloudTenantStatus(c *gin.Context) {
	tenantID := c.Query("tenant")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing tenant"})
		return
	}
	var status, baseURL, slug string
	var errMsg sql.NullString
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT status, base_url, slug, error_message FROM cloud_tenants WHERE id = $1`,
		tenantID).Scan(&status, &baseURL, &slug, &errMsg)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load status"})
		return
	}
	resp := gin.H{"status": status, "slug": slug}
	if status == "active" {
		if claimURL, cErr := s.buildClaimURL(c.Request.Context(), tenantID, baseURL); cErr == nil {
			resp["claim_url"] = claimURL
		}
	}
	if errMsg.Valid && errMsg.String != "" {
		resp["error"] = errMsg.String
	}
	c.JSON(http.StatusOK, resp)
}

// cloudProviders tells the UI (CloudStartPage) which signup methods are
// actually configured, so it can render/disable buttons accordingly. The
// backend only ever hands back data + redirects here — no rendered HTML.
func (s *Server) cloudProviders(c *gin.Context) {
	cfg := getCloudConfig()
	ghCfg := getGitHubOAuthConfig()
	c.JSON(http.StatusOK, gin.H{
		"github": cfg.enabled && ghCfg.ClientID != "" && ghCfg.ClientSecret != "",
		"google": cfg.enabled && cfg.googleClientID != "" && cfg.googleClientSecret != "",
	})
}

// --- Claim token: lets the freshly provisioned tenant hand off a logged-in session ---

type cloudClaimPayload struct {
	Slug  string `json:"slug"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Exp   int64  `json:"exp"`
}

func signCloudClaimToken(secret, slug, email, name string, ttl time.Duration) (string, error) {
	payload := cloudClaimPayload{Slug: slug, Email: email, Name: name, Exp: time.Now().Add(ttl).Unix()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

func verifyCloudClaimToken(secret, token string) (*cloudClaimPayload, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) != 1 {
		return nil, fmt.Errorf("invalid signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid payload")
	}
	var payload cloudClaimPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload")
	}
	if time.Now().Unix() > payload.Exp {
		return nil, fmt.Errorf("token expired")
	}
	return &payload, nil
}

func (s *Server) buildClaimURL(ctx context.Context, tenantID, baseURL string) (string, error) {
	cfg := getCloudConfig()
	if cfg.controlPlaneSecret == "" {
		return "", fmt.Errorf("CLOUD_CONTROL_PLANE_SECRET is not configured")
	}
	var slug, email, name string
	err := s.db.QueryRowContext(ctx, `
		SELECT t.slug, c.email, COALESCE(c.name, '')
		FROM cloud_tenants t JOIN cloud_customers c ON c.id = t.customer_id
		WHERE t.id = $1
	`, tenantID).Scan(&slug, &email, &name)
	if err != nil {
		return "", err
	}
	token, err := signCloudClaimToken(cfg.controlPlaneSecret, slug, email, name, 10*time.Minute)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/api/v1/cloud/claim?token=%s", baseURL, token), nil
}

// cloudClaim runs on a freshly provisioned TENANT instance (every tenant runs
// the same binary). It trusts a short-lived HMAC-signed token minted by the
// control plane at signup, bound to this instance's own slug so a token
// generated for one customer can't be replayed against another's instance.
// Once an owner exists, further claims are rejected.
func (s *Server) cloudClaim(c *gin.Context) {
	cfg := getCloudConfig()
	if cfg.controlPlaneSecret == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "not a managed instance"})
		return
	}
	payload, err := verifyCloudClaimToken(cfg.controlPlaneSecret, c.Query("token"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired claim link"})
		return
	}
	expectedSlug := os.Getenv("RUNRIGHT_TENANT_SLUG")
	if expectedSlug == "" || payload.Slug != expectedSlug {
		c.JSON(http.StatusForbidden, gin.H{"error": "claim link is for a different workspace"})
		return
	}

	ctx := c.Request.Context()
	var existingOwnerEmail string
	err = s.db.QueryRowContext(ctx, `SELECT email FROM sso_users WHERE role = 'owner' LIMIT 1`).Scan(&existingOwnerEmail)
	ownerExists := err == nil
	if ownerExists && existingOwnerEmail != payload.Email {
		// Someone else already claimed this workspace — reject.
		c.JSON(http.StatusConflict, gin.H{"error": "this workspace has already been claimed"})
		return
	}
	// If the rightful owner is retrying (e.g. a cold-start hiccup interrupted
	// their first request, or they double-clicked the link), fall through
	// and just log them in again rather than erroring.

	name := payload.Name
	if name == "" {
		name = payload.Email
	}
	var userID string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO sso_users (email, name, provider, provider_id, role, last_login_at, created_at)
		VALUES ($1, $2, 'cloud', $1, 'owner', NOW(), NOW())
		ON CONFLICT (email) DO UPDATE SET role = 'owner', last_login_at = NOW()
		RETURNING id
	`, payload.Email, name).Scan(&userID)
	if err != nil {
		fmt.Printf("[cloud] claim failed for %s: %v\n", payload.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create account"})
		return
	}

	sessionToken, err := generateSessionToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}
	if err := s.saveSSOSession(ctx, SSOSession{
		Token:     sessionToken,
		UserID:    userID,
		Email:     payload.Email,
		Provider:  "cloud",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	secure := isSecureContext(c)
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookie, sessionToken, 86400*30, "/", "", secure, true)
	c.Redirect(http.StatusFound, "/")
}
