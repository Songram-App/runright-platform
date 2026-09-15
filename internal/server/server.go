package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/sgbudje/runright-platform/internal/assistant"
	"github.com/sgbudje/runright-platform/internal/catalog"
	"github.com/sgbudje/runright-platform/internal/embeddings"
	"github.com/sgbudje/runright-platform/internal/notification"
	"github.com/sgbudje/runright-platform/internal/types"
)

// Server wraps the Gin engine and database connection.
type Server struct {
	router          *gin.Engine
	db              *sql.DB
	slackWebhook    string
	alertWebhookURL string
	ssoMgr          *ssoManager
	assistant       *assistant.Assistant
	embeddings      *embeddings.Service
	wsHub           *WSHub
	githubApp       *GitHubApp
	billing         *billingManager
	// SMTP config for email notifications
	smtpHost string
	smtpUser string
	smtpPass string
	smtpFrom string
}

// Config holds server configuration.
type Config struct {
	Port            int
	DSN             string // Postgres DSN, e.g. postgres://user:pass@localhost/scalecidb
	APIKey          string
	DisableAuth     bool
	SlackWebhook    string // optional; if set, weekly savings digests are posted here
	AlertWebhookURL string // optional; if set, fired when a job consistently wastes >80% of its machine
	BaseURL         string // Base URL for SSO callbacks, e.g. https://runright.example.com
	SSOEnabled      bool   // Enable SSO authentication
	// SMTP config for email notifications
	SMTPHost string // SMTP server host:port, e.g. smtp.example.com:587
	SMTPUser string // SMTP username
	SMTPPass string // SMTP password
	SMTPFrom string // From address, e.g. alerts@runright.io
	// RAG / Embeddings config
	UseRAG          bool   // Enable RAG for AI assistant
	EmbeddingURL    string // Ollama URL for embeddings, e.g. http://localhost:11434
	EmbeddingModel  string // Model for embeddings, e.g. nomic-embed-text
	EmbeddingAPIKey string // API key for OpenAI embeddings (if using OpenAI instead of Ollama)
	// Stripe billing config — optional; billing endpoints respond "disabled" when unset.
	StripeSecretKey       string
	StripeWebhookSecret   string
	StripePricePro        string // Stripe Price ID for the "pro" plan
	StripePriceEnterprise string // Stripe Price ID for the "enterprise" plan
}

// New creates a Server, runs migrations, and wires up routes.
func New(cfg Config) (*Server, error) {
	db, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migration: %w", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	s := &Server{
		router:          r,
		db:              db,
		slackWebhook:    cfg.SlackWebhook,
		alertWebhookURL: cfg.AlertWebhookURL,
		smtpHost:        cfg.SMTPHost,
		smtpUser:        cfg.SMTPUser,
		smtpPass:        cfg.SMTPPass,
		smtpFrom:        cfg.SMTPFrom,
		wsHub:           NewWSHub(),
	}
	s.billing = newBillingManager(db, cfg)
	go s.wsHub.Run()

	// Initialize SSO manager if enabled
	if cfg.SSOEnabled && cfg.BaseURL != "" {
		s.ssoMgr = newSSOManager(db, cfg.BaseURL)
		if err := s.ssoMgr.Initialize(context.Background()); err != nil {
			fmt.Printf("warning: SSO initialization failed: %v\n", err)
		}
	}

	// Initialize embeddings service for RAG if configured
	if cfg.UseRAG && (cfg.EmbeddingURL != "" || cfg.EmbeddingAPIKey != "") {
		embCfg := embeddings.Config{
			BaseURL: cfg.EmbeddingURL,
			Model:   cfg.EmbeddingModel,
			APIKey:  cfg.EmbeddingAPIKey,
		}
		s.embeddings = embeddings.New(db, embCfg)
		if s.embeddings.IsConfigured() {
			fmt.Println("RAG embeddings service initialized")
		}
	}

	// AI assistant — always non-nil (NewFromEnv reports "not configured"
	// gracefully via IsConfigured() when no API key is set).
	s.assistant = assistant.NewFromEnv(db)
	if s.embeddings != nil {
		s.assistant.SetEmbeddingService(s.embeddings)
	}
	if s.assistant.IsConfigured() {
		fmt.Println("AI assistant initialized")
	}

	// SSO endpoints — no auth required for login/callback
	sso := r.Group("/api/v1/sso")
	{
		sso.GET("/providers", s.ssoListProviders)
		sso.GET("/login/:provider", s.ssoLogin)
		sso.GET("/callback/:provider", s.ssoCallback)
		sso.POST("/callback/:provider", s.ssoCallback) // SAML uses POST
		sso.POST("/logout", s.ssoLogout)
	}

	// GitHub App — initialize and register routes
	s.githubApp = NewGitHubApp(db, s)
	gh := r.Group("/api/v1/github")
	{
		// Webhook endpoint — no auth, uses signature verification
		gh.POST("/webhook", s.githubApp.HandleWebhook)
		// OAuth callback for user login via GitHub App
		gh.GET("/callback", s.handleGitHubOAuthCallback)
		// App status — public
		gh.GET("/status", s.githubApp.GetAppStatus)
	}

	// RunRight Cloud signup — public. Handles both sides of the flow: the
	// control-plane instance issues auth + provisions tenants, and every
	// tenant instance (including this one, if it happens to be one) exposes
	// /claim to accept the one-time handoff token from signup.
	cloud := r.Group("/api/v1/cloud")
	{
		cloud.GET("/auth/github", s.cloudAuthGitHub)
		cloud.GET("/auth/google", s.cloudAuthGoogle)
		cloud.GET("/auth/google/callback", s.cloudAuthGoogleCallback)
		cloud.GET("/status", s.cloudTenantStatus)
		cloud.GET("/wait", s.cloudWaitPage)
		cloud.GET("/claim", s.cloudClaim)
	}

	// Auth endpoint — no middleware applied here.
	r.POST("/api/v1/auth", authLogin(cfg.APIKey, cfg.DisableAuth))
	r.POST("/api/v1/auth/logout", authLogout())

	// Stripe webhook — no auth, verified via signature instead.
	r.POST("/api/v1/billing/webhook", s.billing.HandleWebhook)

	v1 := r.Group("/api/v1")
	v1.Use(s.authMiddleware(cfg.APIKey, cfg.DisableAuth))
	{
		v1.POST("/jobs", s.createJob)
		v1.GET("/jobs", s.listJobs)
		// Identity & permissions — used by the frontend to gate write actions.
		v1.GET("/me", s.getMe)
		// User management (role assignment) — admin/owner only.
		v1.GET("/users", s.requirePermission(PermTeamManage), s.listUsers)
		v1.PUT("/users/:email/role", s.requirePermission(PermTeamManage), s.updateUserRole)
		// Role management — read is open; mutations require PermTeamManage.
		v1.GET("/roles", s.listRoles)
		v1.POST("/roles", s.requirePermission(PermTeamManage), s.createRole)
		v1.PUT("/roles/:roleId", s.requirePermission(PermTeamManage), s.updateRole)
		v1.DELETE("/roles/:roleId", s.requirePermission(PermTeamManage), s.deleteRole)
		v1.GET("/jobs/:id", s.getJob)
		v1.GET("/jobs/:id/trend", s.getJobTrend)
		v1.GET("/catalog", s.getCatalog)
		v1.GET("/savings", s.getSavings)
		v1.GET("/savings/history", s.getSavingsHistory)
		v1.GET("/policies", s.listPolicies)
		v1.PUT("/policies", s.requirePermission(PermPoliciesManage), s.upsertPolicy)
		v1.DELETE("/policies", s.requirePermission(PermPoliciesManage), s.deletePolicy)
		v1.POST("/policies/evaluate", s.evaluatePolicy)
		// Repository-centric and job-management routes.
		v1.GET("/repos", s.getRepos)
		v1.GET("/repo-jobs", s.getRepoJobs)                                          // ?repository=owner%2Frepo
		v1.GET("/isolated-jobs", s.getIsolatedJobs)                                  // jobs without a repository
		v1.PUT("/job-meta", s.requirePermission(PermJobsManage), s.upsertJobMeta)    // snooze / archive / stale_days
		v1.DELETE("/job-runs", s.requirePermission(PermJobsManage), s.deleteJobRuns) // hard-delete all runs for a job
		v1.GET("/notifications/settings", s.getNotificationSettings)
		v1.PUT("/notifications/settings", s.requirePermission(PermAlertsManage), s.upsertNotificationSettings)
		v1.POST("/notifications/test", s.sendNotificationTest)
		v1.GET("/notifications/deliveries", s.listNotificationDeliveries)
		// User settings
		v1.GET("/user-settings", s.getUserSettings)
		v1.PUT("/user-settings", s.upsertUserSettings)
		// Ownership routing.
		v1.GET("/ownership", s.listOwnership)
		v1.PUT("/ownership", s.requirePermission(PermOwnershipManage), s.upsertOwnership)
		v1.DELETE("/ownership", s.requirePermission(PermOwnershipManage), s.deleteOwnership)
		// SSO management (admin only)
		v1.GET("/sso/me", s.ssoMe)
		v1.GET("/sso/configs", s.ssoListConfigs)
		v1.PUT("/sso/configs", s.requirePermission(PermTeamManage), s.ssoUpsertConfig)
		v1.DELETE("/sso/configs", s.requirePermission(PermTeamManage), s.ssoDeleteConfig)
		v1.POST("/sso/configs/test", s.requirePermission(PermTeamManage), s.ssoTestConfig)

		// Teams & Organizations
		v1.GET("/teams", s.listTeams)
		v1.POST("/teams", s.requirePermission(PermTeamManage), s.createTeam)
		v1.GET("/teams/:teamId", s.getTeam)
		v1.PUT("/teams/:teamId", s.requirePermission(PermTeamManage), s.updateTeam)
		v1.GET("/teams/:teamId/members", s.listTeamMembers)
		v1.POST("/teams/:teamId/members/invite", s.requirePermission(PermTeamManage), s.inviteTeamMember)
		v1.PUT("/teams/:teamId/members/:memberId", s.requirePermission(PermTeamManage), s.updateTeamMember)
		v1.DELETE("/teams/:teamId/members/:memberId", s.requirePermission(PermTeamManage), s.removeTeamMember)

		// Billing (Stripe) — read is open to team members, mutations require billing:manage.
		v1.GET("/teams/:teamId/billing", s.getTeamBilling)
		v1.POST("/teams/:teamId/billing/checkout", s.requirePermission(PermBillingManage), s.createBillingCheckout)
		v1.POST("/teams/:teamId/billing/portal", s.requirePermission(PermBillingManage), s.createBillingPortal)
		v1.DELETE("/api-keys/:keyId", s.requirePermission(PermAPIKeysManage), s.revokeAPIKey)

		// Audit Logs
		v1.GET("/audit-logs", s.requirePermission(PermAuditView), s.listAuditLogs)
		v1.GET("/audit-logs/:logId", s.requirePermission(PermAuditView), s.getAuditLog)
		v1.GET("/audit-logs/export", s.requirePermission(PermAuditView), s.exportAuditLogs)

		// Analytics & Reporting
		v1.GET("/analytics/summary", s.getAnalyticsSummary)
		v1.GET("/analytics/cost-breakdown", s.getCostBreakdown)
		v1.GET("/analytics/carbon", s.getCarbonFootprint)

		// Run History & Comparison
		v1.GET("/runs/history", s.getRunHistory)
		v1.GET("/runs/compare", s.compareRuns)

		// Scheduled Reports
		v1.GET("/reports", s.listScheduledReports)
		v1.POST("/reports", s.requirePermission(PermReportsManage), s.createScheduledReport)
		v1.PUT("/reports/:reportId", s.requirePermission(PermReportsManage), s.updateScheduledReport)
		v1.DELETE("/reports/:reportId", s.requirePermission(PermReportsManage), s.deleteScheduledReport)
		v1.POST("/reports/:reportId/run", s.requirePermission(PermReportsManage), s.runReportNow)

		// Label Mappings & Auto-PR
		v1.GET("/labels", s.listLabelMappings)
		v1.PUT("/labels", s.requirePermission(PermPoliciesManage), s.upsertLabelMapping)
		v1.DELETE("/labels/:id", s.requirePermission(PermPoliciesManage), s.deleteLabelMapping)

		v1.GET("/auto-pr/settings", s.getAutoPRSettings)
		v1.PUT("/auto-pr/settings", s.requirePermission(PermPoliciesManage), s.upsertAutoPRSettings)

		v1.GET("/auto-pr/recommendations", s.listPRRecommendations)
		v1.POST("/auto-pr/recommendations", s.createPRRecommendation)
		v1.POST("/auto-pr/recommendations/:id/approve", s.requirePermission(PermPoliciesManage), s.approvePRRecommendation)
		v1.POST("/auto-pr/recommendations/:id/dismiss", s.requirePermission(PermPoliciesManage), s.dismissPRRecommendation)

		v1.GET("/auto-pr/history", s.listPRHistory)
		v1.POST("/auto-pr/scan", s.requirePermission(PermPoliciesManage), s.triggerAutoPRScan)

		// GPU Analysis
		v1.GET("/gpu/tiers", s.listGPUTiers)
		v1.POST("/gpu/recommendation", s.getGPURecommendation)

		// GitHub App Management (authenticated)
		v1.GET("/github/installations", s.githubApp.ListInstallations)
		v1.GET("/github/installations/:installationId/repos", s.githubApp.GetInstallationRepos)
		v1.GET("/github/workflow-runs", s.listWorkflowRuns)
		v1.POST("/github/inject-workflow", s.requirePermission(PermPoliciesManage), s.injectWorkflowAction)
	}

	// Register AI Assistant routes
	s.registerAssistantRoutes(v1)

	// Register Embedding routes for RAG
	s.registerEmbeddingRoutes(v1)

	// WebSocket for realtime updates (no auth for now, token in query param)
	r.GET("/api/v1/ws", s.HandleWebSocket)

	// Badge endpoint — intentionally unauthenticated for embedding in READMEs.
	r.GET("/badge/:jobId", s.getBadge)

	// Public stats — intentionally unauthenticated for public dashboards and widgets.
	r.GET("/api/v1/public/stats", s.getPublicStats)

	// Health check — no auth required.
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// Serve the built dashboard for anything else — lets a single Fly app
	// (e.g. a self-serve cloud tenant) serve both API and UI.
	s.registerStaticUI(r)

	// Start weekly Slack digest if a webhook is configured.
	if cfg.SlackWebhook != "" {
		go s.weeklyDigestLoop()
	}

	// Start rule-driven daily summaries (settings/rules determine whether any send).
	go s.dailySummaryLoop()

	// Start retry loop for failed notification deliveries.
	go s.retryLoop()

	return s, nil
}

// Run starts the HTTP server on the configured port.
func (s *Server) Run(port int) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.startAutoPRWorker(ctx)

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Printf("runright server listening on %s\n", addr)
	return srv.ListenAndServe()
}

// --- Route handlers ---

type jobPayload struct {
	Summary         types.MetricsSummary   `json:"summary"`
	Recommendations []types.Recommendation `json:"recommendations"`
}

func (s *Server) createJob(c *gin.Context) {
	var p jobPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	summaryJSON, _ := json.Marshal(p.Summary)
	recsJSON, _ := json.Marshal(p.Recommendations)

	// Determine run_id (may be empty for old agents or the seed script).
	var runID *string
	if p.Summary.RunID != "" {
		runID = &p.Summary.RunID
	}

	// Repository may be empty for local / non-CI runs.
	// Persist empty values as NULL so isolated-job queries can handle them consistently.
	var repository *string
	if repo := strings.TrimSpace(p.Summary.Repository); repo != "" {
		repository = &repo
	}

	// Status defaults to "completed" if not set (e.g. seed data).
	status := p.Summary.Status
	if status == "" {
		status = "completed"
	}

	var id string
	var err error

	if runID != nil {
		// Upsert by run_id: heartbeats create/update the record; the final
		// "completed" flush overwrites it. A completed record is never
		// downgraded back to "heartbeat" (WHERE clause guards this).
		err = s.db.QueryRowContext(c.Request.Context(), `
			INSERT INTO jobs (job_id, run_id, repository, start_time, end_time, duration_seconds, summary, recommendations, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $4)
			ON CONFLICT (run_id) WHERE run_id IS NOT NULL DO UPDATE SET
				end_time         = EXCLUDED.end_time,
				duration_seconds = EXCLUDED.duration_seconds,
				summary          = EXCLUDED.summary,
				recommendations  = EXCLUDED.recommendations,
				status           = EXCLUDED.status
			WHERE jobs.status != 'completed'
			RETURNING id`,
			p.Summary.JobID, runID, repository,
			p.Summary.StartTime, p.Summary.EndTime, p.Summary.DurationSeconds,
			summaryJSON, recsJSON, status,
		).Scan(&id)
	} else {
		err = s.db.QueryRowContext(c.Request.Context(),
			`INSERT INTO jobs (job_id, repository, start_time, end_time, duration_seconds, summary, recommendations, status, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $3) RETURNING id`,
			p.Summary.JobID, repository,
			p.Summary.StartTime, p.Summary.EndTime, p.Summary.DurationSeconds,
			summaryJSON, recsJSON, status,
		).Scan(&id)
	}

	if err == sql.ErrNoRows {
		// ON CONFLICT matched but WHERE blocked the update (already completed).
		c.JSON(http.StatusOK, gin.H{"status": "already completed"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Regression detection: compare the new job's duration against the rolling
	// average of the previous 10 completed runs for the same job_id. If it is
	// more than 15% slower, annotate the first recommendation with the delta.
	if status == "completed" && p.Summary.DurationSeconds > 0 {
		go s.checkDurationRegression(p.Summary.JobID, p.Summary.DurationSeconds, id)
	}

	// High-waste detection: if the last 5 completed runs for this job all had
	// p95 CPU < 20%, fire an alert webhook (if configured).
	if status == "completed" && s.alertWebhookURL != "" {
		go s.checkHighWaste(p.Summary.JobID)
	}

	// Rule-based notification routing for completed runs.
	if status == "completed" {
		go s.dispatchNotificationRules(p.Summary, p.Recommendations)
	}

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// checkDurationRegression computes the rolling avg duration and, if the new run
// is >15% slower, updates the first recommendation's duration_regression_pct field.
func (s *Server) checkDurationRegression(jobID string, newDuration float64, rowID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var avgDuration float64
	err := s.db.QueryRowContext(ctx,
		`SELECT AVG(duration_seconds) FROM (
			SELECT duration_seconds FROM jobs
			WHERE job_id = $1 AND status = 'completed' AND id::text != $2
			ORDER BY created_at DESC LIMIT 10
		) t`, jobID, rowID).Scan(&avgDuration)
	if err != nil || avgDuration <= 0 {
		return
	}
	regressionPct := ((newDuration - avgDuration) / avgDuration) * 100
	if regressionPct < 15 {
		return
	}
	// Fetch the current recommendations and annotate.
	var recsJSON json.RawMessage
	if err := s.db.QueryRowContext(ctx,
		`SELECT recommendations FROM jobs WHERE id::text = $1`, rowID).Scan(&recsJSON); err != nil {
		return
	}
	var recs []types.Recommendation
	if err := json.Unmarshal(recsJSON, &recs); err != nil || len(recs) == 0 {
		return
	}
	pct := regressionPct
	recs[0].DurationRegressionPct = &pct
	updated, err := json.Marshal(recs)
	if err != nil {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE jobs SET recommendations = $1 WHERE id::text = $2`, updated, rowID)
}

type jobRow struct {
	ID              int             `json:"id"`
	JobID           string          `json:"job_id"`
	Repository      string          `json:"repository,omitempty"`
	StartTime       time.Time       `json:"start_time"`
	EndTime         time.Time       `json:"end_time"`
	DurationSeconds float64         `json:"duration_seconds"`
	Summary         json.RawMessage `json:"summary"`
	Recommendations json.RawMessage `json:"recommendations"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
}

func (s *Server) listJobs(c *gin.Context) {
	repo := c.Query("repository")
	var (
		rows *sql.Rows
		err  error
	)
	if repo != "" {
		rows, err = s.db.QueryContext(c.Request.Context(),
			`SELECT id, job_id, COALESCE(repository,''), start_time, end_time, duration_seconds, summary, recommendations, status, created_at
			 FROM jobs WHERE repository = $1 ORDER BY created_at DESC LIMIT 500`, repo)
	} else {
		rows, err = s.db.QueryContext(c.Request.Context(),
			`SELECT id, job_id, COALESCE(repository,''), start_time, end_time, duration_seconds, summary, recommendations, status, created_at
			 FROM jobs ORDER BY created_at DESC LIMIT 500`)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var jobs []jobRow
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.ID, &j.JobID, &j.Repository, &j.StartTime, &j.EndTime, &j.DurationSeconds,
			&j.Summary, &j.Recommendations, &j.Status, &j.CreatedAt); err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	c.JSON(http.StatusOK, jobs)
}

func (s *Server) getJob(c *gin.Context) {
	id := c.Param("id")
	var j jobRow
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT id, job_id, COALESCE(repository,''), start_time, end_time, duration_seconds, summary, recommendations, status, created_at
		 FROM jobs WHERE id = $1`, id).
		Scan(&j.ID, &j.JobID, &j.Repository, &j.StartTime, &j.EndTime, &j.DurationSeconds,
			&j.Summary, &j.Recommendations, &j.Status, &j.CreatedAt)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, j)
}

func (s *Server) getCatalog(c *gin.Context) {
	provider := types.Provider(c.Query("provider"))
	machines := catalog.Query(catalog.QueryOptions{Provider: provider})
	c.JSON(http.StatusOK, machines)
}

// getSavings aggregates potential monthly savings across all completed jobs.
func (s *Server) getSavings(c *gin.Context) {
	repo := c.Query("repository")
	var (
		rows *sql.Rows
		err  error
	)
	if repo != "" {
		rows, err = s.db.QueryContext(c.Request.Context(),
			`SELECT recommendations, duration_seconds FROM jobs
			 WHERE status = 'completed' AND repository = $1
			 ORDER BY created_at DESC LIMIT 1000`, repo)
	} else {
		rows, err = s.db.QueryContext(c.Request.Context(),
			`SELECT recommendations, duration_seconds FROM jobs WHERE status = 'completed' ORDER BY created_at DESC LIMIT 1000`)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var (
		totalJobs      int
		jobsWithSaving int
		totalSavingUSD float64
		totalWastePct  float64
	)
	for rows.Next() {
		var recsJSON json.RawMessage
		var durSec float64
		if err := rows.Scan(&recsJSON, &durSec); err != nil {
			continue
		}
		var recs []types.Recommendation
		if err := json.Unmarshal(recsJSON, &recs); err != nil || len(recs) == 0 {
			continue
		}
		totalJobs++
		best := recs[0]
		if best.CostDeltaPercent < -0.5 { // cheaper recommendation exists
			jobsWithSaving++
			saving := best.CurrentMonthly - best.EstimatedMonthly
			if saving > 0 {
				totalSavingUSD += saving
			}
			totalWastePct += -best.CostDeltaPercent
		}
	}

	avgWastePct := 0.0
	if jobsWithSaving > 0 {
		avgWastePct = totalWastePct / float64(jobsWithSaving)
	}
	c.JSON(http.StatusOK, gin.H{
		"total_jobs":                totalJobs,
		"jobs_with_savings":         jobsWithSaving,
		"estimated_monthly_savings": totalSavingUSD,
		"projected_annual_savings":  totalSavingUSD * 12,
		"avg_waste_percent":         avgWastePct,
	})
}

// getSavingsHistory returns daily savings over the last 90 days for charting.
func (s *Server) getSavingsHistory(c *gin.Context) {
	rows, err := s.db.QueryContext(c.Request.Context(),
		`SELECT DATE(start_time) AS day,
		        COUNT(*) AS job_count,
		        SUM(CASE
		            WHEN (recommendations->0->>'cost_delta_percent')::float < -0.5
		            THEN (recommendations->0->>'current_monthly_usd')::float
		                 - (recommendations->0->>'estimated_monthly_usd')::float
		            ELSE 0
		        END) AS daily_saving
		 FROM jobs
		 WHERE status = 'completed'
		   AND start_time >= NOW() - INTERVAL '90 days'
		 GROUP BY day
		 ORDER BY day ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type point struct {
		Date          string  `json:"date"`
		JobCount      int     `json:"job_count"`
		MonthlySaving float64 `json:"monthly_savings"`
	}
	var history []point
	for rows.Next() {
		var p point
		if err := rows.Scan(&p.Date, &p.JobCount, &p.MonthlySaving); err != nil {
			continue
		}
		if p.MonthlySaving < 0 {
			p.MonthlySaving = 0
		}
		history = append(history, p)
	}
	c.JSON(http.StatusOK, history)
}

// getJobTrend returns aggregated p95 metrics across the last N completed runs for a job_id.
// Query param: window (default 10).
func (s *Server) getJobTrend(c *gin.Context) {
	jobID := c.Param("id")
	window := 10
	if w := c.Query("window"); w != "" {
		if n, err := fmt.Sscan(w, &window); err != nil || n == 0 || window < 1 {
			window = 10
		}
	}
	rows, err := s.db.QueryContext(c.Request.Context(),
		`SELECT summary FROM jobs
		 WHERE job_id = $1 AND status = 'completed'
		 ORDER BY created_at DESC LIMIT $2`, jobID, window)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var (
		cpuP95s   []float64
		memP95s   []float64
		durations []float64
	)
	for rows.Next() {
		var summaryJSON json.RawMessage
		if err := rows.Scan(&summaryJSON); err != nil {
			continue
		}
		var s types.MetricsSummary
		if err := json.Unmarshal(summaryJSON, &s); err != nil {
			continue
		}
		if s.CPUPercentP95 > 0 {
			cpuP95s = append(cpuP95s, s.CPUPercentP95)
		}
		if s.MemUsedGiBP95 > 0 {
			memP95s = append(memP95s, s.MemUsedGiBP95)
		}
		if s.DurationSeconds > 0 {
			durations = append(durations, s.DurationSeconds)
		}
	}
	if len(cpuP95s) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no completed runs found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"job_id":              jobID,
		"window":              len(cpuP95s),
		"cpu_p95_avg":         avg(cpuP95s),
		"mem_p95_avg_gib":     avg(memP95s),
		"duration_avg_sec":    avg(durations),
		"cpu_p95_values":      cpuP95s,
		"mem_p95_values_gib":  memP95s,
		"duration_values_sec": durations,
	})
}

// avg computes the mean of a slice. Returns 0 for empty input.
func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// checkHighWaste fires the alert webhook if the last 5 completed runs for jobID
// all have p95 CPU utilisation below 20% — a strong signal of over-provisioning.
func (s *Server) checkHighWaste(jobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx,
		`SELECT summary FROM jobs
		 WHERE job_id = $1 AND status = 'completed'
		 ORDER BY created_at DESC LIMIT 5`, jobID)
	if err != nil {
		return
	}
	defer rows.Close()

	var cpuP95s []float64
	for rows.Next() {
		var summaryJSON json.RawMessage
		if err := rows.Scan(&summaryJSON); err != nil {
			continue
		}
		var summary types.MetricsSummary
		if err := json.Unmarshal(summaryJSON, &summary); err != nil {
			continue
		}
		cpuP95s = append(cpuP95s, summary.CPUPercentP95)
	}
	// Need all 5 consecutive runs to be low-CPU before alerting.
	if len(cpuP95s) < 5 {
		return
	}
	for _, v := range cpuP95s {
		if v >= 20.0 {
			return
		}
	}

	payload := map[string]any{
		"event":               "high_waste_detected",
		"job_id":              jobID,
		"message":             fmt.Sprintf("Job '%s' has had p95 CPU < 20%% for 5 consecutive runs. Consider right-sizing.", jobID),
		"cpu_p95_last_5_runs": cpuP95s,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.alertWebhookURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// ─── Repository-centric and job-management handlers ───────────────────────────

// repoSummary is the per-repository aggregate returned by getRepos.
type repoSummary struct {
	Repository     string    `json:"repository"`
	JobCount       int       `json:"job_count"`
	StaleCount     int       `json:"stale_count"`
	SnoozedCount   int       `json:"snoozed_count"`
	MonthlySavings float64   `json:"monthly_savings_usd"`
	AnnualSavings  float64   `json:"annual_savings_usd"`
	LastSeen       time.Time `json:"last_seen"`
}

// jobSummaryRow is one row in the per-repo or isolated jobs listing.
type jobSummaryRow struct {
	JobID                 string          `json:"job_id"`
	Repository            string          `json:"repository"`
	RunCount              int             `json:"run_count"`
	LastSeen              time.Time       `json:"last_seen"`
	Stale                 bool            `json:"stale"`
	StaleDays             int             `json:"stale_days"`
	SnoozedUntil          *time.Time      `json:"snoozed_until,omitempty"`
	SnoozeReason          string          `json:"snooze_reason,omitempty"`
	Archived              bool            `json:"archived"`
	LatestSummary         json.RawMessage `json:"latest_summary"`
	LatestRecommendations json.RawMessage `json:"latest_recommendations"`
	MonthlySavingsUSD     float64         `json:"monthly_savings_usd"`
}

type policyRule struct {
	Repository     string    `json:"repository"`
	JobID          string    `json:"job_id"`
	MaxCostPerHour float64   `json:"max_cost_per_hour"`
	Enabled        bool      `json:"enabled"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type policyEvaluation struct {
	Repository              string      `json:"repository"`
	JobID                   string      `json:"job_id"`
	DetectedPricePerHour    float64     `json:"detected_price_per_hour"`
	EffectiveMaxCostPerHour float64     `json:"effective_max_cost_per_hour"`
	Violated                bool        `json:"violated"`
	MatchedPolicy           *policyRule `json:"matched_policy,omitempty"`
	SourceScope             string      `json:"source_scope"`
}

type notificationEvents struct {
	PolicyViolation bool `json:"policy_violation"`
	HighWaste       bool `json:"high_waste"`
	DailySummary    bool `json:"daily_summary"`
}

type notificationAlertRule struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Event          string   `json:"event,omitempty"`
	Scope          string   `json:"scope"`
	Repository     string   `json:"repository"`
	JobID          string   `json:"jobId"`
	Metric         string   `json:"metric"`
	Threshold      float64  `json:"threshold"`
	DestinationIDs []string `json:"destinationIds"`
	Enabled        bool     `json:"enabled"`
}

// ruleChanged returns true when any user-visible field on a rule differs between old and new.
func ruleChanged(old, nw notificationAlertRule) bool {
	if old.Name != nw.Name || old.Enabled != nw.Enabled ||
		old.Type != nw.Type || old.Event != nw.Event ||
		old.Scope != nw.Scope || old.Metric != nw.Metric ||
		old.Threshold != nw.Threshold || old.Repository != nw.Repository ||
		old.JobID != nw.JobID {
		return true
	}
	if len(old.DestinationIDs) != len(nw.DestinationIDs) {
		return true
	}
	for i := range old.DestinationIDs {
		if old.DestinationIDs[i] != nw.DestinationIDs[i] {
			return true
		}
	}
	return false
}

type slackDestination struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	WebhookURL string `json:"webhook_url"`
	HasSecret  bool   `json:"has_secret,omitempty"`
	Channel    string `json:"channel"`
	Mention    string `json:"mention"`
}

// teamsDestination is a Microsoft Teams Incoming Webhook destination.
type teamsDestination struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	WebhookURL string `json:"webhook_url"`
	HasSecret  bool   `json:"has_secret,omitempty"`
}

// webhookDestination is a generic HTTP POST destination.
type webhookDestination struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	HasSecret bool              `json:"has_secret,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type slackNotificationSettings struct {
	Enabled      bool               `json:"enabled"`
	WebhookURL   string             `json:"webhook_url"`
	Channel      string             `json:"channel"`
	Mention      string             `json:"mention"`
	Destinations []slackDestination `json:"destinations"`
}

type emailNotificationSettings struct {
	Enabled       bool     `json:"enabled"`
	Recipients    []string `json:"recipients"`
	SubjectPrefix string   `json:"subject_prefix"`
}

type notificationSettings struct {
	Enabled  bool                      `json:"enabled"`
	Events   notificationEvents        `json:"events"`
	Slack    slackNotificationSettings `json:"slack"`
	Teams    teamsSettings             `json:"teams"`
	Webhooks webhooksSettings          `json:"webhooks"`
	Rules    []notificationAlertRule   `json:"rules"`
	Email    emailNotificationSettings `json:"email"`
}

type teamsSettings struct {
	Enabled      bool               `json:"enabled"`
	Destinations []teamsDestination `json:"destinations"`
}

type webhooksSettings struct {
	Enabled      bool                 `json:"enabled"`
	Destinations []webhookDestination `json:"destinations"`
}

func defaultNotificationSettings() notificationSettings {
	return notificationSettings{
		Enabled: true,
		Events: notificationEvents{
			PolicyViolation: true,
			HighWaste:       false,
			DailySummary:    true,
		},
		Slack: slackNotificationSettings{
			Enabled:      false,
			WebhookURL:   "",
			Channel:      "",
			Mention:      "",
			Destinations: []slackDestination{},
		},
		Teams: teamsSettings{
			Enabled:      false,
			Destinations: []teamsDestination{},
		},
		Webhooks: webhooksSettings{
			Enabled:      false,
			Destinations: []webhookDestination{},
		},
		Rules: []notificationAlertRule{},
		Email: emailNotificationSettings{
			Enabled:       false,
			Recipients:    []string{},
			SubjectPrefix: "[RunRight]",
		},
	}
}

func (s *Server) getNotificationSettings(c *gin.Context) {
	var raw json.RawMessage
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT settings FROM notification_settings WHERE id = 1`).Scan(&raw)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusOK, defaultNotificationSettings())
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var settings notificationSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		c.JSON(http.StatusOK, defaultNotificationSettings())
		return
	}
	if settings.Slack.Destinations == nil {
		settings.Slack.Destinations = []slackDestination{}
	}
	if settings.Teams.Destinations == nil {
		settings.Teams.Destinations = []teamsDestination{}
	}
	if settings.Webhooks.Destinations == nil {
		settings.Webhooks.Destinations = []webhookDestination{}
	}
	if settings.Rules == nil {
		settings.Rules = []notificationAlertRule{}
	}
	if settings.Email.Recipients == nil {
		settings.Email.Recipients = []string{}
	}

	secrets, err := s.loadDestinationSecrets(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for i := range settings.Slack.Destinations {
		destination := &settings.Slack.Destinations[i]
		if webhook, ok := secrets[destination.ID]; ok && webhook != "" {
			destination.WebhookURL = maskWebhookURL(webhook)
			destination.HasSecret = true
		} else {
			destination.WebhookURL = ""
			destination.HasSecret = false
		}
	}
	for i := range settings.Teams.Destinations {
		destination := &settings.Teams.Destinations[i]
		if webhook, ok := secrets[destination.ID]; ok && webhook != "" {
			destination.WebhookURL = maskWebhookURL(webhook)
			destination.HasSecret = true
		} else {
			destination.WebhookURL = ""
			destination.HasSecret = false
		}
	}
	for i := range settings.Webhooks.Destinations {
		destination := &settings.Webhooks.Destinations[i]
		if u, ok := secrets[destination.ID]; ok && u != "" {
			destination.URL = maskWebhookURL(u)
			destination.HasSecret = true
		} else {
			destination.URL = ""
			destination.HasSecret = false
		}
	}
	if settings.Slack.WebhookURL != "" {
		settings.Slack.WebhookURL = maskWebhookURL(settings.Slack.WebhookURL)
	}
	c.JSON(http.StatusOK, settings)
}

func (s *Server) upsertNotificationSettings(c *gin.Context) {
	// Load existing rules BEFORE overwriting so we can diff and emit per-rule audit events.
	prevRules := map[string]notificationAlertRule{}
	var prevRaw json.RawMessage
	if s.db.QueryRowContext(c.Request.Context(),
		`SELECT settings FROM notification_settings WHERE id = 1`).Scan(&prevRaw) == nil {
		var prev notificationSettings
		if json.Unmarshal(prevRaw, &prev) == nil {
			for _, r := range prev.Rules {
				prevRules[r.ID] = r
			}
		}
	}

	settings := defaultNotificationSettings()
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if settings.Slack.Destinations == nil {
		settings.Slack.Destinations = []slackDestination{}
	}
	if settings.Teams.Destinations == nil {
		settings.Teams.Destinations = []teamsDestination{}
	}
	if settings.Webhooks.Destinations == nil {
		settings.Webhooks.Destinations = []webhookDestination{}
	}
	if settings.Rules == nil {
		settings.Rules = []notificationAlertRule{}
	}
	if settings.Email.Recipients == nil {
		settings.Email.Recipients = []string{}
	}

	if settings.Enabled && !settings.Slack.Enabled && !settings.Teams.Enabled && !settings.Webhooks.Enabled && !settings.Email.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one destination channel (slack, teams, webhooks, or email) must be enabled when notifications are enabled"})
		return
	}

	if settings.Email.Enabled && len(settings.Email.Recipients) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one email recipient is required when email is enabled"})
		return
	}

	if settings.Slack.Enabled {
		if len(settings.Slack.Destinations) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "at least one slack destination is required"})
			return
		}

		existingSecrets, err := s.loadDestinationSecrets(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, destination := range settings.Slack.Destinations {
			if destination.ID == "" || destination.Name == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "each slack destination requires id and name"})
				return
			}
			if isWebhookURL(destination.WebhookURL) {
				if err := s.upsertDestinationSecret(c.Request.Context(), destination.ID, destination.WebhookURL); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				existingSecrets[destination.ID] = destination.WebhookURL
			} else if _, ok := existingSecrets[destination.ID]; !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "new destination requires a valid webhook_url"})
				return
			}
		}
		for i := range settings.Slack.Destinations {
			destination := &settings.Slack.Destinations[i]
			if webhook, ok := existingSecrets[destination.ID]; ok {
				destination.WebhookURL = maskWebhookURL(webhook)
				destination.HasSecret = true
			} else {
				destination.WebhookURL = ""
				destination.HasSecret = false
			}
		}
		// Keep legacy single-destination fields synchronized for compatibility.
		if firstWebhook, ok := existingSecrets[settings.Slack.Destinations[0].ID]; ok {
			settings.Slack.WebhookURL = maskWebhookURL(firstWebhook)
		} else {
			settings.Slack.WebhookURL = ""
		}
		settings.Slack.Channel = settings.Slack.Destinations[0].Channel
		settings.Slack.Mention = settings.Slack.Destinations[0].Mention
	}

	if err := s.upsertTeamsAndWebhookSecrets(c.Request.Context(), &settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Prune secrets whose destination IDs are no longer in any channel.
	allActiveIDs := make([]string, 0)
	for _, d := range settings.Slack.Destinations {
		allActiveIDs = append(allActiveIDs, d.ID)
	}
	for _, d := range settings.Teams.Destinations {
		allActiveIDs = append(allActiveIDs, d.ID)
	}
	for _, d := range settings.Webhooks.Destinations {
		allActiveIDs = append(allActiveIDs, d.ID)
	}
	if err := s.deleteDestinationSecretsNotIn(c.Request.Context(), allActiveIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := validateNotificationRuleSchema(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	payload, err := json.Marshal(settings)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	_, err = s.db.ExecContext(c.Request.Context(), `
		INSERT INTO notification_settings (id, settings, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET
			settings = EXCLUDED.settings,
			updated_at = NOW()`, payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Diff rules: emit a specific audit event for each create / update / delete.
	actor := getUserEmail(c)
	newRuleIDs := map[string]struct{}{}
	for _, r := range settings.Rules {
		newRuleIDs[r.ID] = struct{}{}
		prev, existed := prevRules[r.ID]
		if !existed {
			s.logAudit(c.Request.Context(), actor, c, "alert.rule.create", "alert_rule",
				r.ID, r.Name, map[string]any{"type": r.Type, "metric": r.Metric, "scope": r.Scope})
		} else if ruleChanged(prev, r) {
			s.logAudit(c.Request.Context(), actor, c, "alert.rule.update", "alert_rule",
				r.ID, r.Name, map[string]any{"type": r.Type, "metric": r.Metric, "scope": r.Scope, "enabled": r.Enabled})
		}
	}
	for id, r := range prevRules {
		if _, stillExists := newRuleIDs[id]; !stillExists {
			s.logAudit(c.Request.Context(), actor, c, "alert.rule.delete", "alert_rule",
				id, r.Name, map[string]any{"type": r.Type, "scope": r.Scope})
		}
	}
	// Always log the top-level settings change too (channel toggles, destination counts, etc.).
	s.logAudit(c.Request.Context(), actor, c, "notifications.settings.update", "notification_settings",
		"1", "", map[string]any{"rules_count": len(settings.Rules),
			"slack_enabled": settings.Slack.Enabled, "teams_enabled": settings.Teams.Enabled,
			"email_enabled": settings.Email.Enabled, "webhooks_enabled": settings.Webhooks.Enabled})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// upsertTeamsAndWebhookSecrets saves Teams and generic webhook URLs into the
// shared destination-secrets table. It mirrors the Slack secret-management
// pattern so the same loadDestinationSecrets helper serves all providers.
func (s *Server) upsertTeamsAndWebhookSecrets(ctx context.Context, settings *notificationSettings) error {
	if settings.Teams.Enabled {
		for i := range settings.Teams.Destinations {
			dest := &settings.Teams.Destinations[i]
			dest.ID = strings.TrimSpace(dest.ID)
			dest.Name = strings.TrimSpace(dest.Name)
			if dest.ID == "" || dest.Name == "" {
				return fmt.Errorf("each teams destination requires id and name")
			}
			if isWebhookURL(dest.WebhookURL) {
				if err := s.upsertDestinationSecret(ctx, dest.ID, dest.WebhookURL); err != nil {
					return err
				}
			}
			dest.WebhookURL = ""
			dest.HasSecret = true
		}
	}
	if settings.Webhooks.Enabled {
		for i := range settings.Webhooks.Destinations {
			dest := &settings.Webhooks.Destinations[i]
			dest.ID = strings.TrimSpace(dest.ID)
			dest.Name = strings.TrimSpace(dest.Name)
			if dest.ID == "" || dest.Name == "" {
				return fmt.Errorf("each webhook destination requires id and name")
			}
			if isWebhookURL(dest.URL) {
				if err := s.upsertDestinationSecret(ctx, dest.ID, dest.URL); err != nil {
					return err
				}
			}
			dest.URL = ""
			dest.HasSecret = true
		}
	}
	return nil
}

func validateNotificationRuleSchema(settings *notificationSettings) error {
	destinationIDs := map[string]struct{}{}
	for i := range settings.Slack.Destinations {
		destination := &settings.Slack.Destinations[i]
		destination.ID = strings.TrimSpace(destination.ID)
		destination.Name = strings.TrimSpace(destination.Name)
		if destination.ID == "" || destination.Name == "" {
			return fmt.Errorf("each slack destination requires id and name")
		}
		if _, exists := destinationIDs[destination.ID]; exists {
			return fmt.Errorf("duplicate slack destination id: %s", destination.ID)
		}
		destinationIDs[destination.ID] = struct{}{}
	}
	for i := range settings.Teams.Destinations {
		destination := &settings.Teams.Destinations[i]
		destination.ID = strings.TrimSpace(destination.ID)
		destination.Name = strings.TrimSpace(destination.Name)
		if destination.ID == "" || destination.Name == "" {
			return fmt.Errorf("each teams destination requires id and name")
		}
		if _, exists := destinationIDs[destination.ID]; exists {
			return fmt.Errorf("duplicate teams destination id: %s", destination.ID)
		}
		destinationIDs[destination.ID] = struct{}{}
	}
	for i := range settings.Webhooks.Destinations {
		destination := &settings.Webhooks.Destinations[i]
		destination.ID = strings.TrimSpace(destination.ID)
		destination.Name = strings.TrimSpace(destination.Name)
		if destination.ID == "" || destination.Name == "" {
			return fmt.Errorf("each webhook destination requires id and name")
		}
		if _, exists := destinationIDs[destination.ID]; exists {
			return fmt.Errorf("duplicate webhook destination id: %s", destination.ID)
		}
		destinationIDs[destination.ID] = struct{}{}
	}

	validEvents := map[string]struct{}{
		"policy_violation": {},
		"high_waste":       {},
		"daily_summary":    {},
	}
	validMetrics := map[string]struct{}{
		"max_cost_per_hour":            {},
		"waste_percent":                {},
		"monthly_savings_drop_percent": {},
	}

	ruleIDs := map[string]struct{}{}
	for i := range settings.Rules {
		rule := &settings.Rules[i]
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Type = strings.TrimSpace(rule.Type)
		rule.Event = strings.TrimSpace(rule.Event)
		rule.Scope = strings.TrimSpace(rule.Scope)
		rule.Repository = strings.TrimSpace(rule.Repository)
		rule.JobID = strings.TrimSpace(rule.JobID)
		rule.Metric = strings.TrimSpace(rule.Metric)

		if rule.ID == "" || rule.Name == "" {
			return fmt.Errorf("rule %d requires id and name", i)
		}
		if _, exists := ruleIDs[rule.ID]; exists {
			return fmt.Errorf("duplicate rule id: %s", rule.ID)
		}
		ruleIDs[rule.ID] = struct{}{}

		if len(rule.DestinationIDs) == 0 {
			return fmt.Errorf("rule %s must include at least one destinationId", rule.ID)
		}
		seenDestinations := map[string]struct{}{}
		for j := range rule.DestinationIDs {
			destinationID := strings.TrimSpace(rule.DestinationIDs[j])
			if destinationID == "" {
				return fmt.Errorf("rule %s has an empty destinationId", rule.ID)
			}
			if _, exists := destinationIDs[destinationID]; !exists {
				return fmt.Errorf("rule %s references unknown destinationId: %s", rule.ID, destinationID)
			}
			if _, duplicate := seenDestinations[destinationID]; duplicate {
				return fmt.Errorf("rule %s has duplicate destinationId: %s", rule.ID, destinationID)
			}
			seenDestinations[destinationID] = struct{}{}
			rule.DestinationIDs[j] = destinationID
		}

		switch rule.Scope {
		case "global":
			if rule.Repository != "" || rule.JobID != "" {
				return fmt.Errorf("rule %s with global scope must not set repository or jobId", rule.ID)
			}
		case "repository":
			if rule.Repository == "" {
				return fmt.Errorf("rule %s with repository scope requires repository", rule.ID)
			}
			if rule.JobID != "" {
				return fmt.Errorf("rule %s with repository scope must not set jobId", rule.ID)
			}
		case "job":
			if rule.Repository == "" || rule.JobID == "" {
				return fmt.Errorf("rule %s with job scope requires repository and jobId", rule.ID)
			}
		default:
			return fmt.Errorf("rule %s has unsupported scope: %s", rule.ID, rule.Scope)
		}

		switch rule.Type {
		case "event":
			if _, ok := validEvents[rule.Event]; !ok {
				return fmt.Errorf("rule %s has unsupported event: %s", rule.ID, rule.Event)
			}
			if rule.Metric != "max_cost_per_hour" {
				return fmt.Errorf("rule %s event type must set metric to max_cost_per_hour", rule.ID)
			}
			if rule.Threshold != 0 {
				return fmt.Errorf("rule %s event type must set threshold to 0", rule.ID)
			}
		case "threshold":
			if rule.Event != "" {
				return fmt.Errorf("rule %s threshold type must not set event", rule.ID)
			}
			if _, ok := validMetrics[rule.Metric]; !ok {
				return fmt.Errorf("rule %s has unsupported metric: %s", rule.ID, rule.Metric)
			}
			if rule.Threshold <= 0 {
				return fmt.Errorf("rule %s threshold type requires threshold > 0", rule.ID)
			}
		default:
			return fmt.Errorf("rule %s has unsupported type: %s", rule.ID, rule.Type)
		}
	}

	return nil
}

type deliveryLogRow struct {
	ID            int       `json:"id"`
	RuleID        string    `json:"rule_id"`
	DestinationID string    `json:"destination_id"`
	Channel       string    `json:"channel"`
	JobID         string    `json:"job_id"`
	Repository    string    `json:"repository"`
	Status        string    `json:"status"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	SentAt        time.Time `json:"sent_at"`
}

func (s *Server) listNotificationDeliveries(c *gin.Context) {
	ruleID := c.Query("rule_id")
	limitStr := c.DefaultQuery("limit", "50")
	limit := 50
	if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil || limit < 1 || limit > 500 {
		limit = 50
	}

	var (
		rows *sql.Rows
		err  error
	)
	if ruleID != "" {
		rows, err = s.db.QueryContext(c.Request.Context(), `
			SELECT id, rule_id, destination_id, channel, job_id, COALESCE(repository,''), status, COALESCE(error_message,''), sent_at
			FROM notification_delivery_logs
			WHERE rule_id = $1
			ORDER BY sent_at DESC
			LIMIT $2`, ruleID, limit)
	} else {
		rows, err = s.db.QueryContext(c.Request.Context(), `
			SELECT id, rule_id, destination_id, channel, job_id, COALESCE(repository,''), status, COALESCE(error_message,''), sent_at
			FROM notification_delivery_logs
			ORDER BY sent_at DESC
			LIMIT $1`, limit)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var out []deliveryLogRow
	for rows.Next() {
		var row deliveryLogRow
		if err := rows.Scan(&row.ID, &row.RuleID, &row.DestinationID, &row.Channel, &row.JobID, &row.Repository, &row.Status, &row.ErrorMessage, &row.SentAt); err != nil {
			continue
		}
		out = append(out, row)
	}
	if out == nil {
		out = []deliveryLogRow{}
	}
	c.JSON(http.StatusOK, out)
}

// getUserSettings retrieves user settings from the database.
func (s *Server) getUserSettings(c *gin.Context) {
	var otelEndpoint string
	var allowedMachineIDs, allowedSeries, allowedFamilies pq.StringArray

	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(otel_endpoint,''), COALESCE(allowed_machine_ids,'{}'), COALESCE(allowed_series,'{}'), COALESCE(allowed_families,'{}')
		 FROM user_settings WHERE id = 1`).
		Scan(&otelEndpoint, &allowedMachineIDs, &allowedSeries, &allowedFamilies)

	if err != nil && err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Convert pq.StringArray to []string, handling nil cases
	machineIDs := []string{}
	series := []string{}
	families := []string{}
	if allowedMachineIDs != nil {
		machineIDs = allowedMachineIDs
	}
	if allowedSeries != nil {
		series = allowedSeries
	}
	if allowedFamilies != nil {
		families = allowedFamilies
	}

	c.JSON(http.StatusOK, types.UserSettings{
		OtelEndpoint:      otelEndpoint,
		AllowedMachineIDs: machineIDs,
		AllowedSeries:     series,
		AllowedFamilies:   families,
	})
}

// upsertUserSettings updates user settings in the database.
func (s *Server) upsertUserSettings(c *gin.Context) {
	var settings types.UserSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Convert nil slices to empty arrays for database storage
	machineIDs := settings.AllowedMachineIDs
	if machineIDs == nil {
		machineIDs = []string{}
	}
	series := settings.AllowedSeries
	if series == nil {
		series = []string{}
	}
	families := settings.AllowedFamilies
	if families == nil {
		families = []string{}
	}

	_, err := s.db.ExecContext(c.Request.Context(),
		`INSERT INTO user_settings (id, otel_endpoint, allowed_machine_ids, allowed_series, allowed_families)
		 VALUES (1, $1, $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE SET 
		   otel_endpoint = EXCLUDED.otel_endpoint,
		   allowed_machine_ids = EXCLUDED.allowed_machine_ids,
		   allowed_series = EXCLUDED.allowed_series,
		   allowed_families = EXCLUDED.allowed_families,
		   updated_at = NOW()`,
		settings.OtelEndpoint, pq.Array(machineIDs), pq.Array(series), pq.Array(families))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, settings)
}

func (s *Server) loadNotificationSettingsForDispatch(ctx context.Context) (notificationSettings, map[string]string, error) {
	settings := defaultNotificationSettings()

	var raw json.RawMessage
	err := s.db.QueryRowContext(ctx, `SELECT settings FROM notification_settings WHERE id = 1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return settings, map[string]string{}, nil
	}
	if err != nil {
		return settings, nil, err
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return settings, nil, err
	}
	if settings.Slack.Destinations == nil {
		settings.Slack.Destinations = []slackDestination{}
	}
	if settings.Rules == nil {
		settings.Rules = []notificationAlertRule{}
	}

	secrets, err := s.loadDestinationSecrets(ctx)
	if err != nil {
		return settings, nil, err
	}
	return settings, secrets, nil
}

func (s *Server) evaluateEffectivePolicy(ctx context.Context, repository, jobID string, detectedPrice float64) policyEvaluation {
	eval := policyEvaluation{
		Repository:           repository,
		JobID:                jobID,
		DetectedPricePerHour: detectedPrice,
		SourceScope:          "none",
	}

	if detectedPrice <= 0 {
		return eval
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT repository, COALESCE(job_id, ''), max_cost_per_hour, enabled, updated_at
		FROM policy_rules
		WHERE (repository = $1 AND job_id = $2)
		   OR (repository = $1 AND job_id = '')
		   OR (repository = '' AND job_id = '')
		ORDER BY CASE
			WHEN repository = $1 AND job_id = $2 THEN 0
			WHEN repository = $1 AND job_id = '' THEN 1
			ELSE 2
		END
		LIMIT 1`, repository, jobID)

	var rule policyRule
	if err := row.Scan(&rule.Repository, &rule.JobID, &rule.MaxCostPerHour, &rule.Enabled, &rule.UpdatedAt); err == nil {
		eval.MatchedPolicy = &rule
		eval.EffectiveMaxCostPerHour = rule.MaxCostPerHour
		eval.Violated = rule.Enabled && detectedPrice > rule.MaxCostPerHour
		switch {
		case rule.Repository == repository && rule.JobID == jobID:
			eval.SourceScope = "job"
		case rule.Repository == repository && rule.JobID == "":
			eval.SourceScope = "repository"
		default:
			eval.SourceScope = "global"
		}
	}

	return eval
}

func (s *Server) dailySummaryAlreadySent(ctx context.Context, ruleID, scopeKey, day string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM notification_daily_summary_dispatches
		WHERE rule_id = $1 AND scope_key = $2 AND summary_date = $3
		LIMIT 1`, ruleID, scopeKey, day).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) markDailySummarySent(ctx context.Context, ruleID, scopeKey, day string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_daily_summary_dispatches (rule_id, scope_key, summary_date, sent_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (rule_id, scope_key, summary_date) DO NOTHING`, ruleID, scopeKey, day)
	return err
}

func (s *Server) sendNotificationTest(c *gin.Context) {
	var settingsRaw json.RawMessage
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT settings FROM notification_settings WHERE id = 1`).Scan(&settingsRaw)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notification settings are not configured"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	settings := defaultNotificationSettings()
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse notification settings"})
		return
	}
	secrets, err := s.loadDestinationSecrets(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !settings.Enabled || !settings.Slack.Enabled || len(settings.Slack.Destinations) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no enabled slack destinations configured"})
		return
	}

	msg := fmt.Sprintf(":bell: RunRight test notification at %s", time.Now().UTC().Format(time.RFC3339))
	var results []notification.DeliveryResult
	for _, destination := range settings.Slack.Destinations {
		webhook := secrets[destination.ID]
		if webhook == "" {
			continue
		}
		text := msg
		if destination.Mention != "" {
			text = destination.Mention + " " + text
		}
		result := notification.DeliveryResult{
			DestinationID: destination.ID,
			Channel:       "slack",
			Status:        "success",
		}
		if err := postSlackWebhook(c.Request.Context(), webhook, text); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
		}
		results = append(results, result)
		s.writeDeliveryLog(c.Request.Context(), result, "test", "test")
	}

	failures := 0
	for _, r := range results {
		if r.Status == "failed" {
			failures++
		}
	}

	if failures > 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to send one or more test notifications"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func postSlackWebhook(ctx context.Context, webhookURL, text string) error {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}
	return nil
}

func isWebhookURL(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

func maskWebhookURL(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) <= 16 {
		return "********"
	}
	if len(v) <= 32 {
		return v[:8] + "..." + v[len(v)-4:]
	}
	return v[:26] + "..." + v[len(v)-6:]
}

func (s *Server) loadDestinationSecrets(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT destination_id, webhook_url FROM notification_destination_secrets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	secrets := map[string]string{}
	for rows.Next() {
		var destinationID, webhook string
		if err := rows.Scan(&destinationID, &webhook); err != nil {
			return nil, err
		}
		secrets[destinationID] = webhook
	}
	return secrets, rows.Err()
}

func (s *Server) upsertDestinationSecret(ctx context.Context, destinationID, webhook string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_destination_secrets (destination_id, webhook_url, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (destination_id) DO UPDATE SET
			webhook_url = EXCLUDED.webhook_url,
			updated_at = NOW()`, destinationID, webhook)
	return err
}

func (s *Server) deleteDestinationSecretsNotIn(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM notification_destination_secrets`)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_destination_secrets WHERE NOT (destination_id = ANY($1))`, pq.StringArray(ids))
	return err
}

// queryJobSummaries is a shared helper that fetches per-job-id summaries.
// When repository is non-empty it scopes to that repo; empty string = isolated (no repo).
// includeArchived controls whether archived rows are included.
func (s *Server) queryJobSummaries(ctx context.Context, repository string, isolatedOnly bool, includeArchived bool) ([]jobSummaryRow, error) {
	var repoFilter string
	var args []any

	if isolatedOnly {
		repoFilter = "AND (repository IS NULL OR repository = '')"
	} else {
		repoFilter = "AND repository = $1"
		args = append(args, repository)
	}

	archivedFilter := "AND COALESCE(m.archived, false) = false"
	if includeArchived {
		archivedFilter = ""
	}

	query := fmt.Sprintf(`
		WITH latest AS (
			SELECT DISTINCT ON (job_id)
				job_id,
				COALESCE(repository, '') AS repository,
				created_at               AS last_seen,
				summary,
				recommendations
			FROM jobs
			WHERE status = 'completed'
			%s
			ORDER BY job_id, created_at DESC
		),
		counts AS (
			SELECT job_id, COUNT(*) AS run_count
			FROM jobs
			WHERE status = 'completed'
			%s
			GROUP BY job_id
		)
		SELECT
			l.job_id,
			l.repository,
			l.last_seen,
			l.summary,
			l.recommendations,
			c.run_count,
			m.snoozed_until,
			COALESCE(m.snooze_reason, '')  AS snooze_reason,
			COALESCE(m.archived, false)    AS archived,
			COALESCE(m.stale_days, 30)     AS stale_days
		FROM latest l
		JOIN counts c ON c.job_id = l.job_id
		LEFT JOIN job_metadata m
			ON m.job_id = l.job_id
			AND m.repository = l.repository
		%s
		ORDER BY l.last_seen DESC`,
		repoFilter, repoFilter, archivedFilter)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	var result []jobSummaryRow
	for rows.Next() {
		var row jobSummaryRow
		var snoozedUntil *time.Time
		var staleDays int
		if err := rows.Scan(
			&row.JobID, &row.Repository, &row.LastSeen,
			&row.LatestSummary, &row.LatestRecommendations,
			&row.RunCount, &snoozedUntil, &row.SnoozeReason,
			&row.Archived, &staleDays,
		); err != nil {
			continue
		}
		row.StaleDays = staleDays
		row.SnoozedUntil = snoozedUntil
		snoozed := snoozedUntil != nil && snoozedUntil.After(now)
		row.Stale = !snoozed && now.Sub(row.LastSeen) > time.Duration(staleDays)*24*time.Hour

		// Compute monthly savings from latest recommendation.
		var recs []types.Recommendation
		if err := json.Unmarshal(row.LatestRecommendations, &recs); err == nil && len(recs) > 0 {
			if recs[0].CostDeltaPercent < -0.5 {
				if saving := recs[0].CurrentMonthly - recs[0].EstimatedMonthly; saving > 0 {
					row.MonthlySavingsUSD = saving
				}
			}
		}
		result = append(result, row)
	}
	return result, nil
}

// getRepos returns a per-repository summary (job count, stale count, savings, last seen).
func (s *Server) getRepos(c *gin.Context) {
	// Fetch the latest run per job_id for all repos in one pass.
	rows, err := s.db.QueryContext(c.Request.Context(), `
		WITH latest AS (
			SELECT DISTINCT ON (job_id)
				job_id, COALESCE(repository, '') AS repository,
				created_at AS last_seen, recommendations
			FROM jobs
			WHERE status = 'completed' AND COALESCE(repository, '') <> ''
			ORDER BY job_id, created_at DESC
		)
		SELECT l.job_id, l.repository, l.last_seen, l.recommendations,
		       m.snoozed_until,
		       COALESCE(m.archived, false)  AS archived,
		       COALESCE(m.stale_days, 30)   AS stale_days
		FROM latest l
		LEFT JOIN job_metadata m ON m.job_id = l.job_id AND m.repository = l.repository
		WHERE COALESCE(m.archived, false) = false
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type entry struct {
		lastSeen     time.Time
		recs         []types.Recommendation
		snoozedUntil *time.Time
		staleDays    int
	}
	repoMap := make(map[string][]entry)
	now := time.Now()

	for rows.Next() {
		var jobID, repo string
		var lastSeen time.Time
		var recsJSON json.RawMessage
		var snoozedUntil *time.Time
		var archived bool
		var staleDays int
		if err := rows.Scan(&jobID, &repo, &lastSeen, &recsJSON, &snoozedUntil, &archived, &staleDays); err != nil {
			continue
		}
		var recs []types.Recommendation
		_ = json.Unmarshal(recsJSON, &recs)
		repoMap[repo] = append(repoMap[repo], entry{lastSeen, recs, snoozedUntil, staleDays})
	}

	var summaries []repoSummary
	for repo, jobs := range repoMap {
		rs := repoSummary{Repository: repo, JobCount: len(jobs)}
		for _, j := range jobs {
			snoozed := j.snoozedUntil != nil && j.snoozedUntil.After(now)
			if snoozed {
				rs.SnoozedCount++
			} else if now.Sub(j.lastSeen) > time.Duration(j.staleDays)*24*time.Hour {
				rs.StaleCount++
			}
			if j.lastSeen.After(rs.LastSeen) {
				rs.LastSeen = j.lastSeen
			}
			if len(j.recs) > 0 && j.recs[0].CostDeltaPercent < -0.5 {
				if saving := j.recs[0].CurrentMonthly - j.recs[0].EstimatedMonthly; saving > 0 {
					rs.MonthlySavings += saving
				}
			}
		}
		rs.AnnualSavings = rs.MonthlySavings * 12
		summaries = append(summaries, rs)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].LastSeen.After(summaries[j].LastSeen)
	})
	if summaries == nil {
		summaries = []repoSummary{}
	}
	c.JSON(http.StatusOK, summaries)
}

// getRepoJobs returns per-job-id summaries scoped to a single repository.
// Query param: repository (required), include_archived (optional, default false).
func (s *Server) getRepoJobs(c *gin.Context) {
	repo := c.Query("repository")
	if repo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository query param required"})
		return
	}
	includeArchived := c.Query("include_archived") == "true"
	rows, err := s.queryJobSummaries(c.Request.Context(), repo, false, includeArchived)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []jobSummaryRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// getIsolatedJobs returns per-job-id summaries for jobs with no repository set.
func (s *Server) getIsolatedJobs(c *gin.Context) {
	includeArchived := c.Query("include_archived") == "true"
	rows, err := s.queryJobSummaries(c.Request.Context(), "", true, includeArchived)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []jobSummaryRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// listPolicies returns policy rules, optionally filtered by repository.
func (s *Server) listPolicies(c *gin.Context) {
	repository := c.Query("repository")
	rows, err := s.db.QueryContext(c.Request.Context(), `
		SELECT repository, COALESCE(job_id, ''), max_cost_per_hour, enabled, updated_at
		FROM policy_rules
		WHERE ($1 = '' OR repository = $1)
		ORDER BY repository ASC, job_id ASC`, repository)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var out []policyRule
	for rows.Next() {
		var rule policyRule
		if err := rows.Scan(&rule.Repository, &rule.JobID, &rule.MaxCostPerHour, &rule.Enabled, &rule.UpdatedAt); err != nil {
			continue
		}
		out = append(out, rule)
	}
	if out == nil {
		out = []policyRule{}
	}
	c.JSON(http.StatusOK, out)
}

// upsertPolicy creates or updates a policy rule.
func (s *Server) upsertPolicy(c *gin.Context) {
	var body struct {
		Repository     string  `json:"repository"`
		JobID          string  `json:"job_id"`
		MaxCostPerHour float64 `json:"max_cost_per_hour"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.JobID != "" && body.Repository == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository is required when job_id is set"})
		return
	}
	if body.MaxCostPerHour <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_cost_per_hour must be greater than zero"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	_, err := s.db.ExecContext(c.Request.Context(), `
		INSERT INTO policy_rules (repository, job_id, max_cost_per_hour, enabled, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (repository, job_id) DO UPDATE SET
			max_cost_per_hour = EXCLUDED.max_cost_per_hour,
			enabled = EXCLUDED.enabled,
			updated_at = NOW()`,
		body.Repository, body.JobID, body.MaxCostPerHour, enabled,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "policy.upsert", "policy",
		body.Repository+"/"+body.JobID, body.Repository,
		map[string]any{"max_cost_per_hour": body.MaxCostPerHour, "enabled": enabled, "job_id": body.JobID})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// deletePolicy removes a policy rule for a repository/job scope.
func (s *Server) deletePolicy(c *gin.Context) {
	ctx := c.Request.Context()
	userEmail := getUserEmail(c)
	repository := c.Query("repository")
	jobID := c.Query("job_id")
	_, err := s.db.ExecContext(ctx, `DELETE FROM policy_rules WHERE repository = $1 AND job_id = $2`, repository, jobID)
	if err != nil {
		s.logAuditError(ctx, userEmail, c, "policy.delete", "policy", repository+"/"+jobID, err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.logAudit(ctx, userEmail, c, "policy.delete", "policy",
		repository+"/"+jobID, repository, map[string]any{"job_id": jobID})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// evaluatePolicy finds the effective policy for a repository/job and reports whether a detected price violates it.
func (s *Server) evaluatePolicy(c *gin.Context) {
	var body struct {
		Repository           string  `json:"repository"`
		JobID                string  `json:"job_id"`
		DetectedPricePerHour float64 `json:"detected_price_per_hour"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.DetectedPricePerHour <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "detected_price_per_hour must be greater than zero"})
		return
	}

	eval := policyEvaluation{
		Repository:           body.Repository,
		JobID:                body.JobID,
		DetectedPricePerHour: body.DetectedPricePerHour,
		SourceScope:          "none",
	}

	row := s.db.QueryRowContext(c.Request.Context(), `
		SELECT repository, COALESCE(job_id, ''), max_cost_per_hour, enabled, updated_at
		FROM policy_rules
		WHERE (repository = $1 AND job_id = $2)
		   OR (repository = $1 AND job_id = '')
		   OR (repository = '' AND job_id = '')
		ORDER BY CASE
			WHEN repository = $1 AND job_id = $2 THEN 0
			WHEN repository = $1 AND job_id = '' THEN 1
			ELSE 2
		END
		LIMIT 1`, body.Repository, body.JobID)

	var rule policyRule
	if err := row.Scan(&rule.Repository, &rule.JobID, &rule.MaxCostPerHour, &rule.Enabled, &rule.UpdatedAt); err == nil {
		eval.MatchedPolicy = &rule
		eval.EffectiveMaxCostPerHour = rule.MaxCostPerHour
		eval.Violated = rule.Enabled && body.DetectedPricePerHour > rule.MaxCostPerHour
		switch {
		case rule.Repository == body.Repository && rule.JobID == body.JobID:
			eval.SourceScope = "job"
		case rule.Repository == body.Repository && rule.JobID == "":
			eval.SourceScope = "repository"
		default:
			eval.SourceScope = "global"
		}
	}

	c.JSON(http.StatusOK, eval)
}

// upsertJobMeta creates or updates snooze/archive settings for a job.
func (s *Server) upsertJobMeta(c *gin.Context) {
	var body struct {
		JobID        string     `json:"job_id" binding:"required"`
		Repository   string     `json:"repository"`
		SnoozedUntil *time.Time `json:"snoozed_until"`
		SnoozeReason string     `json:"snooze_reason"`
		Archived     *bool      `json:"archived"`
		StaleDays    *int       `json:"stale_days"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve archived_at.
	var archivedAt *time.Time
	if body.Archived != nil && *body.Archived {
		now := time.Now()
		archivedAt = &now
	}
	staleDays := 30
	if body.StaleDays != nil && *body.StaleDays > 0 {
		staleDays = *body.StaleDays
	}
	archived := false
	if body.Archived != nil {
		archived = *body.Archived
	}

	_, err := s.db.ExecContext(c.Request.Context(), `
		INSERT INTO job_metadata (job_id, repository, snoozed_until, snooze_reason, archived, archived_at, stale_days)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (job_id, repository) DO UPDATE SET
			snoozed_until = EXCLUDED.snoozed_until,
			snooze_reason = EXCLUDED.snooze_reason,
			archived      = EXCLUDED.archived,
			archived_at   = CASE WHEN EXCLUDED.archived AND NOT job_metadata.archived
			                     THEN NOW() ELSE job_metadata.archived_at END,
			stale_days    = EXCLUDED.stale_days`,
		body.JobID, body.Repository,
		body.SnoozedUntil, body.SnoozeReason,
		archived, archivedAt, staleDays,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "job.meta.update", "job",
		body.JobID, body.Repository, map[string]any{"archived": archived,
			"snoozed_until": body.SnoozedUntil, "stale_days": staleDays})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// deleteJobRuns hard-deletes ALL run records for a given job_id + repository pair,
// and removes the associated job_metadata row.
// Query params: job_id (required), repository (optional, defaults to empty string).
func (s *Server) deleteJobRuns(c *gin.Context) {
	jobID := c.Query("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id query param required"})
		return
	}
	repository := c.Query("repository")

	tx, err := s.db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = tx.Rollback() }()

	var deleted int64
	if repository == "" {
		res, err := tx.ExecContext(c.Request.Context(),
			`DELETE FROM jobs WHERE job_id = $1 AND repository IS NULL`, jobID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		deleted, _ = res.RowsAffected()
		_, _ = tx.ExecContext(c.Request.Context(),
			`DELETE FROM job_metadata WHERE job_id = $1 AND repository = ''`, jobID)
	} else {
		res, err := tx.ExecContext(c.Request.Context(),
			`DELETE FROM jobs WHERE job_id = $1 AND repository = $2`, jobID, repository)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		deleted, _ = res.RowsAffected()
		_, _ = tx.ExecContext(c.Request.Context(),
			`DELETE FROM job_metadata WHERE job_id = $1 AND repository = $2`, jobID, repository)
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.logAudit(c.Request.Context(), getUserEmail(c), c, "job.runs.delete", "job",
		jobID, repository, map[string]any{"deleted_runs": deleted, "repository": repository})
	c.JSON(http.StatusOK, gin.H{"deleted_runs": deleted})
}

// This endpoint is intentionally unauthenticated so teams can embed it in READMEs.
func (s *Server) getBadge(c *gin.Context) {
	jobId := c.Param("jobId")
	var recsJSON json.RawMessage
	err := s.db.QueryRowContext(c.Request.Context(),
		`SELECT recommendations FROM jobs WHERE job_id = $1 AND status = 'completed'
		 ORDER BY created_at DESC LIMIT 1`, jobId).Scan(&recsJSON)
	if err == sql.ErrNoRows {
		c.Header("Content-Type", "image/svg+xml")
		c.String(http.StatusOK, badgeSVG("runright", "unknown", "#9f9f9f"))
		return
	}
	if err != nil {
		c.Header("Content-Type", "image/svg+xml")
		c.String(http.StatusInternalServerError, badgeSVG("runright", "error", "#e05d44"))
		return
	}
	var recs []types.Recommendation
	if err := json.Unmarshal(recsJSON, &recs); err != nil || len(recs) == 0 {
		c.Header("Content-Type", "image/svg+xml")
		c.String(http.StatusOK, badgeSVG("runright", "unknown", "#9f9f9f"))
		return
	}
	tier := string(recs[0].Tier)
	color := map[string]string{
		"right-sized":    "#44cc11",
		"cheaper-option": "#dfb317",
		"more-headroom":  "#e05d44",
	}[tier]
	if color == "" {
		color = "#9f9f9f"
	}
	c.Header("Content-Type", "image/svg+xml")
	c.Header("Cache-Control", "no-cache, max-age=3600")
	c.String(http.StatusOK, badgeSVG("runright", tier, color))
}

// badgeSVG generates a minimal shields.io-compatible badge SVG.
func badgeSVG(label, message, color string) string {
	lw := len(label)*6 + 10
	mw := len(message)*6 + 10
	tw := lw + mw
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20">`+
		`<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`+
		`<rect rx="3" width="%d" height="20" fill="#555"/>`+
		`<rect rx="3" x="%d" width="%d" height="20" fill="%s"/>`+
		`<rect x="%d" width="4" height="20" fill="%s"/>`+
		`<rect rx="3" width="%d" height="20" fill="url(#s)"/>`+
		`<g fill="#fff" text-anchor="middle" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="11">`+
		`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>`+
		`<text x="%d" y="14">%s</text>`+
		`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>`+
		`<text x="%d" y="14">%s</text>`+
		`</g></svg>`,
		tw, tw, lw, mw, color, lw, color, tw,
		lw/2+1, label, lw/2, label,
		lw+mw/2+1, message, lw+mw/2, message,
	)
}

// weeklyDigestLoop fires every Monday at 09:00 UTC and posts a savings summary to Slack.
func (s *Server) weeklyDigestLoop() {
	for {
		now := time.Now().UTC()
		// Next Monday 09:00 UTC.
		daysUntilMonday := (int(time.Monday) - int(now.Weekday()) + 7) % 7
		if daysUntilMonday == 0 && now.Hour() >= 9 {
			daysUntilMonday = 7
		}
		next := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, 9, 0, 0, 0, time.UTC)
		time.Sleep(time.Until(next))
		s.postWeeklyDigest()
	}
}

func (s *Server) postWeeklyDigest() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx,
		`SELECT recommendations FROM jobs
		 WHERE status = 'completed' AND created_at >= NOW() - INTERVAL '7 days'`)
	if err != nil {
		return
	}
	defer rows.Close()

	var totalJobs int
	var savingJobs int
	var totalSaving float64
	for rows.Next() {
		var recsJSON json.RawMessage
		if err := rows.Scan(&recsJSON); err != nil {
			continue
		}
		var recs []types.Recommendation
		if err := json.Unmarshal(recsJSON, &recs); err != nil || len(recs) == 0 {
			continue
		}
		totalJobs++
		best := recs[0]
		if best.CostDeltaPercent < -0.5 {
			savingJobs++
			if saving := best.CurrentMonthly - best.EstimatedMonthly; saving > 0 {
				totalSaving += saving
			}
		}
	}
	if totalJobs == 0 {
		return
	}

	msg := fmt.Sprintf(
		":moneybag: *RunRight weekly digest*\n"+
			"• %d CI jobs ran this week\n"+
			"• %d are over-provisioned\n"+
			"• Potential savings: *$%.2f/month* ($%.0f/year)",
		totalJobs, savingJobs, totalSaving, totalSaving*12,
	)
	body, _ := json.Marshal(map[string]string{"text": msg})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.slackWebhook, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// --- CORS middleware ---

// allowedOrigins returns the list of allowed origins from env or defaults.
func allowedOrigins() []string {
	origins := os.Getenv("RUNRIGHT_ALLOWED_ORIGINS")
	if origins == "" {
		// Default: allow localhost for development
		return []string{"http://localhost:3000", "http://localhost:5173", "http://127.0.0.1:3000", "http://127.0.0.1:5173"}
	}
	return strings.Split(origins, ",")
}

func corsMiddleware() gin.HandlerFunc {
	allowed := allowedOrigins()
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		// Check if origin is allowed
		originAllowed := false
		for _, o := range allowed {
			if strings.TrimSpace(o) == origin {
				originAllowed = true
				break
			}
		}
		if originAllowed {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// --- Auth handlers + middleware ---

// getMe returns the authenticated user's email, role, and resolved permissions.
// The frontend uses this to gate write-action buttons.
func (s *Server) getMe(c *gin.Context) {
	email := getUserEmail(c)
	role := s.getUserRole(c.Request.Context(), c)
	c.JSON(http.StatusOK, gin.H{
		"email":       email,
		"role":        role,
		"permissions": s.permissionsForRole(c.Request.Context(), role),
	})
}

const sessionCookie = "runright_session"

// sessionStore maps session tokens to API keys (simple in-memory store).
// In production, consider using Redis or database-backed sessions.
var (
	sessionStore   = make(map[string]string)
	sessionStoreMu sync.RWMutex
)

// generateSessionToken creates a cryptographically secure random session token.
func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashAPIKey creates a SHA-256 hash of the API key for session lookup.
func hashAPIKey(apiKey string) string {
	h := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(h[:])
}

// isSecureContext checks if the request is over HTTPS.
func isSecureContext(c *gin.Context) bool {
	// Check X-Forwarded-Proto (common behind reverse proxies)
	if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" {
		return true
	}
	// Check if TLS connection
	return c.Request.TLS != nil
}

// authLogin validates the API key and issues an HttpOnly session cookie.
func authLogin(apiKey string, disableAuth bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if disableAuth || apiKey == "" {
			// Auth not configured — nothing to log in to.
			c.JSON(http.StatusOK, gin.H{"status": "no auth configured"})
			return
		}
		var body struct {
			APIKey string `json:"api_key"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.APIKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_key required"})
			return
		}
		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(body.APIKey), []byte(apiKey)) != 1 {
			// Add small delay to further mitigate timing attacks
			time.Sleep(100 * time.Millisecond)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api_key"})
			return
		}
		// Generate a random session token instead of storing the raw API key
		sessionToken, err := generateSessionToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
			return
		}
		// Store the session token -> API key hash mapping
		sessionStoreMu.Lock()
		sessionStore[sessionToken] = hashAPIKey(apiKey)
		sessionStoreMu.Unlock()

		// Set HttpOnly, SameSite=Strict cookie
		// Use Secure flag when in HTTPS context
		secure := isSecureContext(c)
		c.SetSameSite(http.SameSiteStrictMode)
		c.SetCookie(sessionCookie, sessionToken, 86400*30, "/", "", secure, true /* httpOnly */)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// authLogout clears the session cookie and invalidates the session.
func authLogout() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Invalidate the session token
		if token, err := c.Cookie(sessionCookie); err == nil {
			sessionStoreMu.Lock()
			delete(sessionStore, token)
			sessionStoreMu.Unlock()
		}
		c.SetSameSite(http.SameSiteStrictMode)
		c.SetCookie(sessionCookie, "", -1, "/", "", false, true)
		c.JSON(http.StatusOK, gin.H{"status": "logged out"})
	}
}

// authMiddleware accepts either the HttpOnly cookie, a static Bearer token (CLI),
// or a DB-issued API key (created via the dashboard). DB keys are checked last
// so the hot path (cookie / static key) stays allocation-free.
func (s *Server) authMiddleware(apiKey string, disableAuth bool) gin.HandlerFunc {
	apiKeyHash := hashAPIKey(apiKey)
	return func(c *gin.Context) {
		// Dev mode / no key configured — set a dev user and skip auth.
		if disableAuth || apiKey == "" {
			c.Set("user_email", "dev@runright.io")
			c.Next()
			return
		}
		// 1. HttpOnly session cookie (dashboard).
		if token, err := c.Cookie(sessionCookie); err == nil {
			sessionStoreMu.RLock()
			storedHash, exists := sessionStore[token]
			sessionStoreMu.RUnlock()
			if exists && subtle.ConstantTimeCompare([]byte(storedHash), []byte(apiKeyHash)) == 1 {
				c.Next()
				return
			}
			// 1b. SSO session (if SSO is configured).
			if s.ssoMgr != nil {
				if sess, err := s.validateSSOSession(c.Request.Context(), token); err == nil && sess != nil {
					c.Set("sso_user_id", sess.UserID)
					c.Set("sso_email", sess.Email)
					c.Set("user_email", sess.Email)
					c.Set("sso_provider", sess.Provider)
					c.Next()
					return
				}
			}
		}
		// 2. Bearer token — static key first, then DB-issued keys.
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			providedKey := strings.TrimPrefix(authHeader, "Bearer ")
			// Static key (fast path — constant-time compare).
			if subtle.ConstantTimeCompare([]byte(providedKey), []byte(apiKey)) == 1 {
				c.Next()
				return
			}
			// DB-issued key (keys created via the dashboard UI).
			// We also set user_email so getUserEmail / RBAC lookups work downstream.
			if email, _, ok := s.validateAPIKeyFromDB(c.Request.Context(), providedKey); ok {
				c.Set("user_email", email)
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
	}
}

// --- Migrations ---

func migrate(db *sql.DB) error { return runEmbeddedMigrations(db) }

// ConfigFromEnv reads server config from environment variables.
func ConfigFromEnv() Config {
	port := 8080
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://runright:runright@localhost:5435/runright?sslmode=disable"
	}
	disableAuth := strings.EqualFold(strings.TrimSpace(os.Getenv("RUNRIGHT_DISABLE_AUTH")), "true")
	if !disableAuth {
		disableAuth = strings.EqualFold(strings.TrimSpace(os.Getenv("RUNRIGHT_DEV_MODE")), "true")
	}
	ssoEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv("RUNRIGHT_SSO_ENABLED")), "true")
	useRAG := strings.EqualFold(strings.TrimSpace(os.Getenv("RUNRIGHT_USE_RAG")), "true")
	return Config{
		Port:                  port,
		DSN:                   dsn,
		APIKey:                os.Getenv("RUNRIGHT_API_KEY"),
		DisableAuth:           disableAuth,
		SlackWebhook:          os.Getenv("RUNRIGHT_SLACK_WEBHOOK"),
		AlertWebhookURL:       os.Getenv("RUNRIGHT_ALERT_WEBHOOK"),
		BaseURL:               os.Getenv("RUNRIGHT_BASE_URL"),
		SSOEnabled:            ssoEnabled,
		SMTPHost:              os.Getenv("RUNRIGHT_SMTP_HOST"),
		SMTPUser:              os.Getenv("RUNRIGHT_SMTP_USER"),
		SMTPPass:              os.Getenv("RUNRIGHT_SMTP_PASS"),
		SMTPFrom:              os.Getenv("RUNRIGHT_SMTP_FROM"),
		UseRAG:                useRAG,
		EmbeddingURL:          os.Getenv("RUNRIGHT_EMBEDDING_URL"),
		EmbeddingModel:        os.Getenv("RUNRIGHT_EMBEDDING_MODEL"),
		EmbeddingAPIKey:       os.Getenv("RUNRIGHT_EMBEDDING_API_KEY"),
		StripeSecretKey:       os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:   os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePricePro:        os.Getenv("STRIPE_PRICE_PRO"),
		StripePriceEnterprise: os.Getenv("STRIPE_PRICE_ENTERPRISE"),
	}
}
