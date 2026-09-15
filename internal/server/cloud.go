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

func (s *Server) verifyCloudOAuthState(c *gin.Context) bool {
	cookie, err := c.Cookie(cloudStateCookie)
	c.SetCookie(cloudStateCookie, "", -1, "/", "", isSecureContext(c), true)
	if err != nil || cookie == "" {
		return false
	}
	queryState := strings.TrimPrefix(c.Query("state"), cloudStatePrefix)
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(queryState)) == 1
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
	oauth2Cfg := &oauth2.Config{
		ClientID:     ghCfg.ClientID,
		ClientSecret: ghCfg.ClientSecret,
		Endpoint:     oauth2gh.Endpoint,
		RedirectURL:  ghCfg.RedirectURL, // existing /api/v1/github/callback
		Scopes:       []string{"read:user", "user:email"},
	}
	c.Redirect(http.StatusFound, oauth2Cfg.AuthCodeURL(cloudStatePrefix+state))
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
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.googleClientID,
		ClientSecret: cfg.googleClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  cfg.baseURL + "/api/v1/cloud/auth/google/callback",
		Scopes:       []string{"openid", "email", "profile"},
	}
	c.Redirect(http.StatusFound, oauth2Cfg.AuthCodeURL(state))
}

func (s *Server) cloudAuthGoogleCallback(c *gin.Context) {
	cfg := getCloudConfig()
	if !cfg.enabled || cfg.googleClientID == "" || cfg.googleClientSecret == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Google login is not configured"})
		return
	}
	if !s.verifyCloudOAuthState(c) {
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
	s.cloudCompleteSignup(c, profile.Email, profile.Name, profile.Picture, "google", profile.Sub)
}

// --- Shared signup completion: upsert customer, ensure tenant, kick off provisioning ---

func (s *Server) cloudCompleteSignup(c *gin.Context, email, name, avatarURL, provider, providerUID string) {
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
		slug, slugErr = s.reserveTenantSlug(ctx)
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workspace"})
			return
		}
		s.logAudit(ctx, email, c, "cloud.signup", "cloud_tenant", tenantID, slug, nil)
		go s.provisionTenantAsync(tenantID, slug)
	case err != nil:
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

	c.Redirect(http.StatusFound, "/api/v1/cloud/wait?tenant="+tenantID)
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

func (s *Server) cloudWaitPage(c *gin.Context) {
	tenantID := c.Query("tenant")
	html := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Setting up your RunRight workspace</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;background:#1A0F02;color:#E8C458;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.card{max-width:420px;text-align:center;padding:32px}
.spinner{width:36px;height:36px;border:3px solid #3a2510;border-top-color:#B8860B;border-radius:50%%;margin:0 auto 20px;animation:spin 1s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
.err{color:#C23B22;margin-top:16px;display:none;font-size:14px}
</style></head>
<body><div class="card">
<div class="spinner" id="spin"></div>
<h2>Setting up your workspace&hellip;</h2>
<p id="msg">This usually takes under a minute.</p>
<p class="err" id="err"></p>
</div>
<script>
var tenant = %s;
function poll() {
  fetch('/api/v1/cloud/status?tenant=' + encodeURIComponent(tenant))
    .then(function(r){ return r.json(); })
    .then(function(data){
      if (data.status === 'active' && data.claim_url) {
        document.getElementById('msg').textContent = 'Ready! Redirecting\u2026';
        window.location.href = data.claim_url;
        return;
      }
      if (data.status === 'failed') {
        document.getElementById('spin').style.display = 'none';
        document.getElementById('msg').textContent = 'Setup failed.';
        var e = document.getElementById('err');
        e.style.display = 'block';
        e.textContent = data.error || 'Please contact support.';
        return;
      }
      setTimeout(poll, 2500);
    })
    .catch(function(){ setTimeout(poll, 2500); });
}
poll();
</script></body></html>`, jsonString(tenantID))
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// cloudStartPage is the "choose how you want to sign up" landing page —
// linked from the marketing site's Cloud CTAs instead of jumping straight
// into a specific provider's OAuth flow.
func (s *Server) cloudStartPage(c *gin.Context) {
	cfg := getCloudConfig()
	if !cfg.enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "cloud signup is not enabled"})
		return
	}
	googleEnabled := cfg.googleClientID != "" && cfg.googleClientSecret != ""
	googleButton := `<button class="opt" disabled title="Coming soon">
      <svg width="18" height="18" viewBox="0 0 24 24"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/></svg>
      Continue with Google &mdash; coming soon
    </button>`
	if googleEnabled {
		googleButton = `<a class="opt" href="/api/v1/cloud/auth/google">
      <svg width="18" height="18" viewBox="0 0 24 24"><path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/><path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/><path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/><path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/></svg>
      Continue with Google
    </a>`
	}
	html := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Start your RunRight Cloud workspace</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;background:#1A0F02;color:#E8C458;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.card{max-width:400px;width:100%%;text-align:center;padding:32px}
h1{font-size:22px;margin:0 0 6px}
p{color:#C4A882;font-size:14px;margin:0 0 28px}
.opt{display:flex;align-items:center;justify-content:center;gap:10px;width:100%%;box-sizing:border-box;padding:13px 20px;margin-bottom:12px;background:#2C1A0E;border:1px solid #4a2e18;border-radius:6px;color:#FBF0DC;text-decoration:none;font-size:14px;cursor:pointer;font-family:inherit}
.opt:hover{border-color:#B8860B}
.opt:disabled{opacity:.5;cursor:not-allowed}
.opt:disabled:hover{border-color:#4a2e18}
</style></head>
<body><div class="card">
<h1>Create your RunRight Cloud workspace</h1>
<p>Pick how you'd like to sign in. We'll spin up a dedicated, isolated instance just for you.</p>
<a class="opt" href="/api/v1/cloud/auth/github">
  <svg width="18" height="18" viewBox="0 0 24 24" fill="#FBF0DC"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
  Continue with GitHub
</a>
%s
</div></body></html>`, googleButton)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
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
	var existingOwners int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sso_users WHERE role = 'owner'`).Scan(&existingOwners); err == nil && existingOwners > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "this workspace has already been claimed"})
		return
	}

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
