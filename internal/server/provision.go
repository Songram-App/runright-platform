package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lib/pq"
)

// This file provisions a dedicated Fly.io app + Postgres database for each
// self-serve "RunRight Cloud" customer. It shells out to flyctl (bundled in
// the backend image, see Dockerfile.backend) rather than reimplementing
// Fly's API, so provisioning behaves the same way `fly deploy` would.

// flyBinary is the absolute path to the bundled flyctl binary. The backend
// image is built FROM scratch (no shell, no PATH), so PATH-based lookup
// isn't available — must reference by absolute path.
const flyBinary = "/usr/local/bin/fly"

type provisionEnv struct {
	flyAPIToken string
	flyOrgSlug  string
	region      string
	adminDSN    string
	image       string
}

func loadProvisionEnv() (*provisionEnv, error) {
	token := os.Getenv("FLY_PROVISION_TOKEN")
	org := os.Getenv("FLY_ORG_SLUG")
	dsn := os.Getenv("DATABASE_URL")
	image := os.Getenv("RUNRIGHT_TENANT_IMAGE")
	if token == "" || org == "" || dsn == "" || image == "" {
		return nil, fmt.Errorf("cloud provisioning is not fully configured (need FLY_PROVISION_TOKEN, FLY_ORG_SLUG, DATABASE_URL, RUNRIGHT_TENANT_IMAGE)")
	}
	region := os.Getenv("FLY_PROVISION_REGION")
	if region == "" {
		region = "iad"
	}
	return &provisionEnv{flyAPIToken: token, flyOrgSlug: org, region: region, adminDSN: dsn, image: image}, nil
}

// provisionTenantAsync runs provisioning in the background so the signup
// HTTP request can return immediately; the browser polls /cloud/status.
// Errors are recorded on the row rather than propagated anywhere else.
func (s *Server) provisionTenantAsync(tenantID, slug string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	if err := s.provisionTenant(ctx, slug); err != nil {
		fmt.Printf("[cloud] provisioning failed for %s: %v\n", slug, err)
		s.db.ExecContext(context.Background(),
			`UPDATE cloud_tenants SET status = 'failed', error_message = $2 WHERE id = $1`,
			tenantID, err.Error())
		return
	}
	s.db.ExecContext(context.Background(),
		`UPDATE cloud_tenants SET status = 'active', activated_at = NOW() WHERE id = $1`,
		tenantID)
}

func (s *Server) provisionTenant(ctx context.Context, slug string) error {
	env, err := loadProvisionEnv()
	if err != nil {
		return err
	}
	cfg := getCloudConfig()
	if cfg.controlPlaneSecret == "" {
		return fmt.Errorf("CLOUD_CONTROL_PLANE_SECRET is not set")
	}

	appName := "rr-" + slug
	dbName := "rr_" + strings.ReplaceAll(slug, "-", "_")
	baseURL := fmt.Sprintf("https://%s.fly.dev", appName)

	// 1. Dedicated Postgres database on the shared cluster — full data
	// isolation without needing a whole extra Postgres app per customer.
	tenantDSN, err := createTenantDatabase(ctx, env.adminDSN, dbName)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	// 2. Dedicated Fly app for this customer's compute. Tolerate "already
	// exists" so a retry after a partial failure doesn't blow up here.
	if _, err := runFly(ctx, env.flyAPIToken, "apps", "create", appName, "--org", env.flyOrgSlug); err != nil {
		if !strings.Contains(err.Error(), "already been taken") {
			return fmt.Errorf("create app: %w", err)
		}
	}

	// A freshly created app has no public IP/DNS at all — <app>.fly.dev
	// won't resolve until one is allocated. This is a separate step from
	// creating the app or a Machine; `fly launch`/`fly deploy` normally do
	// it implicitly, but `fly apps create` + `fly machine run` don't.
	// --shared v4 is free; v6 is required alongside it and also free.
	if _, err := runFly(ctx, env.flyAPIToken, "ips", "allocate-v6", "--app", appName); err != nil {
		if !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("allocate ipv6: %w", err)
		}
	}
	if _, err := runFly(ctx, env.flyAPIToken, "ips", "allocate-v4", "--app", appName, "--shared"); err != nil {
		if !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("allocate ipv4: %w", err)
		}
	}

	// 3. Stage secrets before any Machine exists; a Machine created after
	// this point inherits them automatically as env vars.
	apiKey, _, _ := generateAPIKey()
	secretArgs := []string{
		"secrets", "set", "--app", appName, "--stage",
		"DATABASE_URL=" + tenantDSN,
		"RUNRIGHT_API_KEY=" + apiKey,
		"RUNRIGHT_BASE_URL=" + baseURL,
		"RUNRIGHT_TENANT_SLUG=" + slug,
		"RUNRIGHT_SSO_ENABLED=true",
		"RUNRIGHT_ALLOWED_ORIGINS=" + baseURL,
		"CLOUD_CONTROL_PLANE_SECRET=" + cfg.controlPlaneSecret,
		// Tenants can't register their own GitHub OAuth callback (GitHub Apps
		// only support a small fixed list), so returning login routes
		// through the control plane's one registered callback instead.
		"RUNRIGHT_CONTROL_PLANE_URL=" + os.Getenv("RUNRIGHT_BASE_URL"),
		"GIN_MODE=release",
	}
	if ghID := os.Getenv("GITHUB_APP_ID"); ghID != "" {
		secretArgs = append(secretArgs,
			"GITHUB_APP_ID="+ghID,
			"GITHUB_APP_SLUG="+os.Getenv("GITHUB_APP_SLUG"),
			"GITHUB_APP_CLIENT_ID="+os.Getenv("GITHUB_APP_CLIENT_ID"),
			"GITHUB_APP_CLIENT_SECRET="+os.Getenv("GITHUB_APP_CLIENT_SECRET"),
			"GITHUB_APP_PRIVATE_KEY="+os.Getenv("GITHUB_APP_PRIVATE_KEY"),
			"GITHUB_APP_WEBHOOK_SECRET="+os.Getenv("GITHUB_APP_WEBHOOK_SECRET"),
		)
	}
	if _, err := runFly(ctx, env.flyAPIToken, secretArgs...); err != nil {
		return fmt.Errorf("stage secrets: %w", err)
	}

	// 4. Boot the Machine (or reuse+restart one left over from a prior
	// partial attempt, so a retry doesn't pile up duplicate Machines).
	machineID, err := ensureTenantMachine(ctx, env, appName)
	if err != nil {
		return fmt.Errorf("boot machine: %w", err)
	}

	// 5. Don't declare victory until it actually answers requests. Check over
	// Fly's private network (<machine>.vm.<app>.internal) rather than the
	// public https://<app>.fly.dev hostname — a brand-new app's public DNS
	// can take a bit to propagate, but 6PN/internal addressing is live the
	// instant the Machine exists.
	internalHealthURL := fmt.Sprintf("http://%s.vm.%s.internal:8080/healthz", machineID, appName)
	if err := waitForHealth(ctx, internalHealthURL, 90*time.Second); err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	return nil
}

// ensureTenantMachine boots the tenant's Machine, or — if one already exists
// from a prior partial provisioning attempt — restarts it so it picks up any
// secrets staged since, rather than creating a duplicate Machine on retry.
func ensureTenantMachine(ctx context.Context, env *provisionEnv, appName string) (string, error) {
	listOut, err := runFly(ctx, env.flyAPIToken, "machine", "list", "--app", appName, "--json")
	if err == nil {
		if ids := parseMachineIDs(listOut); len(ids) > 0 {
			existing := ids[0]
			if _, err := runFly(ctx, env.flyAPIToken, "machine", "restart", existing, "--app", appName); err != nil {
				return "", fmt.Errorf("restart existing machine: %w", err)
			}
			return existing, nil
		}
	}

	// `fly machine run` has no --json flag (only `machine list`/`status` do),
	// so create it plain and then look its ID up via `machine list --json`.
	if _, err := runFly(ctx, env.flyAPIToken,
		"machine", "run", env.image,
		"--app", appName,
		"--region", env.region,
		"--port", "443:8080/tcp:tls:http",
		"--port", "80:8080/tcp:http",
		"--vm-cpu-kind", "shared",
		"--vm-cpus", "1",
		"--vm-memory", "256",
		"--autostart",
		"--autostop=suspend", // NOT "--autostop", "suspend" — autostop takes an
		// *optional* value; passed as two argv entries, flyctl's flag parser
		// treats the bare word as a positional "override container command"
		// instead, silently replacing the server's entrypoint with `suspend`.
	); err != nil {
		return "", err
	}
	listOut2, err := runFly(ctx, env.flyAPIToken, "machine", "list", "--app", appName, "--json")
	if err != nil {
		return "", fmt.Errorf("list machine after create: %w", err)
	}
	ids := parseMachineIDs(listOut2)
	if len(ids) == 0 {
		return "", fmt.Errorf("could not find created machine for %s", appName)
	}
	return ids[0], nil
}

// parseMachineIDs extracts Machine IDs from flyctl --json output, which may
// be a single object (machine run) or an array (machine list).
func parseMachineIDs(out string) []string {
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err == nil && obj.ID != "" {
		return []string{obj.ID}
	}
	var arr []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &arr); err == nil {
		var ids []string
		for _, m := range arr {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
		return ids
	}
	return nil
}

// createTenantDatabase creates (if missing) a dedicated database for a tenant
// on the shared Postgres cluster and returns a DSN pointing at it.
func createTenantDatabase(ctx context.Context, adminDSN, dbName string) (string, error) {
	// dbName comes from a randomly generated slug (lowercase words + hex,
	// '-' replaced with '_'), but re-validate defensively since it's
	// interpolated into a CREATE DATABASE statement — Postgres has no
	// parameterized DDL.
	for _, r := range dbName {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return "", fmt.Errorf("invalid database name %q", dbName)
		}
	}

	adminDB, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return "", err
	}
	defer adminDB.Close()

	var exists bool
	if err := adminDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		if _, err := adminDB.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(dbName)); err != nil {
			return "", err
		}
	}
	return swapDSNDatabase(adminDSN, dbName)
}

func swapDSNDatabase(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// runFly shells out to the bundled flyctl binary, authenticated via
// FLY_API_TOKEN so it never needs interactive `fly auth login`. The backend
// image is FROM scratch (no /etc/passwd, no shell), so flyctl has no HOME to
// find its config/cache dir in unless we set one explicitly.
func runFly(ctx context.Context, token string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, flyBinary, args...)
	cmd.Env = []string{
		"FLY_API_TOKEN=" + token,
		"FLY_NO_UPDATE_CHECK=1",
		"HOME=/tmp",
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, truncateOutput(out.String(), 800))
	}
	return out.String(), nil
}

func truncateOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func waitForHealth(ctx context.Context, healthURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
				lastErr = fmt.Errorf("status %d", resp.StatusCode)
			} else {
				lastErr = doErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for health check: %v", lastErr)
}
