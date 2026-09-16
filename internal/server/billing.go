package server

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	stripe "github.com/stripe/stripe-go/v81"
	billingportalsession "github.com/stripe/stripe-go/v81/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/webhook"
)

// planLimits describes the usage caps for a subscription tier. A limit of 0
// means unlimited.
type planLimits struct {
	Name            string
	MaxMembers      int
	MaxJobsPerMonth int // distinct CI jobs analyzed per calendar month
	MaxRepos        int // distinct repositories with at least one job
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// plans is the fixed catalog of subscription tiers offered to customers.
// "free" requires no Stripe price — it's the default for every new team, and
// the default plan assigned to every new RunRight Cloud tenant on signup.
// Limits are overridable via env vars (this file is public open-source
// source, so the mechanism is public but the actual configured numbers on
// any given deployment don't have to match these defaults).
var plans = map[string]planLimits{
	"free": {
		Name:            "Free",
		MaxMembers:      envInt("RUNRIGHT_FREE_MAX_MEMBERS", 3),
		MaxJobsPerMonth: envInt("RUNRIGHT_FREE_MAX_JOBS", 500),
		MaxRepos:        envInt("RUNRIGHT_FREE_MAX_REPOS", 5),
	},
	"pro": {
		Name:            "Pro",
		MaxMembers:      envInt("RUNRIGHT_PRO_MAX_MEMBERS", 25),
		MaxJobsPerMonth: envInt("RUNRIGHT_PRO_MAX_JOBS", 10000),
		MaxRepos:        envInt("RUNRIGHT_PRO_MAX_REPOS", 50),
	},
	"enterprise": {Name: "Enterprise", MaxMembers: 0, MaxJobsPerMonth: 0, MaxRepos: 0},
}

// billingManager wires the Stripe SDK to team subscription state stored on the
// teams table. It's nil-safe: when no Stripe secret key is configured (typical
// for self-hosted deployments), billing endpoints respond that billing is
// disabled rather than erroring.
type billingManager struct {
	db            *sql.DB
	enabled       bool
	webhookSecret string
	baseURL       string
	priceToPlan   map[string]string // Stripe Price ID -> plan slug
	planToPrice   map[string]string // plan slug -> Stripe Price ID
}

func newBillingManager(db *sql.DB, cfg Config) *billingManager {
	b := &billingManager{
		db:            db,
		enabled:       cfg.StripeSecretKey != "",
		webhookSecret: cfg.StripeWebhookSecret,
		baseURL:       cfg.BaseURL,
		priceToPlan:   map[string]string{},
		planToPrice:   map[string]string{},
	}
	if cfg.StripePricePro != "" {
		b.priceToPlan[cfg.StripePricePro] = "pro"
		b.planToPrice["pro"] = cfg.StripePricePro
	}
	if cfg.StripePriceEnterprise != "" {
		b.priceToPlan[cfg.StripePriceEnterprise] = "enterprise"
		b.planToPrice["enterprise"] = cfg.StripePriceEnterprise
	}
	if b.enabled {
		stripe.Key = cfg.StripeSecretKey
	}
	return b
}

// checkMemberLimit reports whether a team on the given plan can add one more
// member on top of currentCount. Called before creating invitations.
func checkMemberLimit(plan string, currentCount int) bool {
	limit, ok := plans[plan]
	if !ok || limit.MaxMembers == 0 {
		return true
	}
	return currentCount < limit.MaxMembers
}

// getTeamBilling returns the team's current plan, subscription status, and usage.
func (s *Server) getTeamBilling(c *gin.Context) {
	teamID := c.Param("teamId")
	ctx := c.Request.Context()

	var plan, subStatus string
	var stripeCustomerID sql.NullString
	var currentPeriodEnd sql.NullTime
	var cancelAtPeriodEnd bool
	var memberCount int
	err := s.db.QueryRowContext(ctx, `
		SELECT t.plan, COALESCE(t.subscription_status, 'active'), t.stripe_customer_id,
		       t.current_period_end, t.cancel_at_period_end,
		       (SELECT COUNT(*) FROM team_members WHERE team_id = t.id)
		FROM teams t WHERE t.id = $1
	`, teamID).Scan(&plan, &subStatus, &stripeCustomerID, &currentPeriodEnd, &cancelAtPeriodEnd, &memberCount)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load billing"})
		return
	}

	limits := plans[plan]
	resp := gin.H{
		"plan":                 plan,
		"plan_name":            limits.Name,
		"subscription_status":  subStatus,
		"cancel_at_period_end": cancelAtPeriodEnd,
		"member_count":         memberCount,
		"max_members":          limits.MaxMembers,
		"billing_enabled":      s.billing != nil && s.billing.enabled,
		"has_payment_method":   stripeCustomerID.Valid && stripeCustomerID.String != "",
	}
	if currentPeriodEnd.Valid {
		resp["current_period_end"] = currentPeriodEnd.Time
	}
	c.JSON(http.StatusOK, resp)
}

// createBillingCheckout starts a Stripe Checkout session to subscribe a team to
// a paid plan (or change their existing subscription's plan).
func (s *Server) createBillingCheckout(c *gin.Context) {
	if s.billing == nil || !s.billing.enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "billing is not configured on this deployment"})
		return
	}
	teamID := c.Param("teamId")
	userEmail := getUserEmail(c)
	ctx := c.Request.Context()

	var body struct {
		Plan string `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan is required"})
		return
	}
	priceID, ok := s.billing.planToPrice[body.Plan]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown or unpriced plan: " + body.Plan})
		return
	}

	var billingEmail string
	var stripeCustomerID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT billing_email, stripe_customer_id FROM teams WHERE id = $1`, teamID,
	).Scan(&billingEmail, &stripeCustomerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if billingEmail == "" {
		billingEmail = userEmail
	}

	returnBase := s.billing.baseURL
	if returnBase == "" {
		returnBase = "https://" + c.Request.Host
	}

	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL:        stripe.String(returnBase + "/settings?billing=success"),
		CancelURL:         stripe.String(returnBase + "/settings?billing=cancelled"),
		ClientReferenceID: stripe.String(teamID),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		Metadata: map[string]string{"team_id": teamID, "plan": body.Plan},
	}
	if stripeCustomerID.Valid && stripeCustomerID.String != "" {
		params.Customer = stripe.String(stripeCustomerID.String)
	} else {
		params.CustomerEmail = stripe.String(billingEmail)
	}

	sess, err := checkoutsession.New(params)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create checkout session: " + err.Error()})
		return
	}

	s.logAudit(ctx, userEmail, c, "billing.checkout.create", "team", teamID, body.Plan, nil)
	c.JSON(http.StatusOK, gin.H{"checkout_url": sess.URL})
}

// createBillingPortal opens a Stripe customer portal session so a team owner
// can manage payment methods, invoices, or cancel their subscription.
func (s *Server) createBillingPortal(c *gin.Context) {
	if s.billing == nil || !s.billing.enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "billing is not configured on this deployment"})
		return
	}
	teamID := c.Param("teamId")
	ctx := c.Request.Context()

	var stripeCustomerID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT stripe_customer_id FROM teams WHERE id = $1`, teamID,
	).Scan(&stripeCustomerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "team not found"})
		return
	}
	if !stripeCustomerID.Valid || stripeCustomerID.String == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "team has no billing account yet — subscribe to a paid plan first"})
		return
	}

	returnBase := s.billing.baseURL
	if returnBase == "" {
		returnBase = "https://" + c.Request.Host
	}

	sess, err := billingportalsession.New(&stripe.BillingPortalSessionParams{
		Customer:  stripe.String(stripeCustomerID.String),
		ReturnURL: stripe.String(returnBase + "/settings"),
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create billing portal session: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"portal_url": sess.URL})
}

// Minimal shapes for the Stripe objects we care about in webhooks. We parse
// these by hand rather than into the full stripe-go structs because webhook
// payloads reference nested objects (customer, subscription) by ID string,
// which doesn't unmarshal cleanly into the SDK's expanded-object types.
type stripeCheckoutSessionPayload struct {
	Customer     string            `json:"customer"`
	Subscription string            `json:"subscription"`
	Metadata     map[string]string `json:"metadata"`
}

type stripeSubscriptionPayload struct {
	ID                string `json:"id"`
	Customer          string `json:"customer"`
	Status            string `json:"status"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
	CurrentPeriodEnd  int64  `json:"current_period_end"`
	Items             struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

// HandleWebhook processes Stripe subscription lifecycle events and syncs plan
// / subscription state onto the owning team.
func (b *billingManager) HandleWebhook(c *gin.Context) {
	if !b.enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "billing is not configured on this deployment"})
		return
	}
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	event, err := webhook.ConstructEvent(payload, c.GetHeader("Stripe-Signature"), b.webhookSecret)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook signature"})
		return
	}

	ctx := c.Request.Context()
	switch event.Type {
	case "checkout.session.completed":
		var sessData stripeCheckoutSessionPayload
		if err := json.Unmarshal(event.Data.Raw, &sessData); err != nil {
			break
		}
		teamID := sessData.Metadata["team_id"]
		plan := sessData.Metadata["plan"]
		if teamID == "" {
			break
		}
		_, _ = b.db.ExecContext(ctx, `
			UPDATE teams SET stripe_customer_id = $1, stripe_subscription_id = $2,
			       plan = COALESCE(NULLIF($3, ''), plan), subscription_status = 'active', updated_at = NOW()
			WHERE id = $4
		`, sessData.Customer, sessData.Subscription, plan, teamID)

	case "customer.subscription.updated", "customer.subscription.created":
		var sub stripeSubscriptionPayload
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			break
		}
		plan := ""
		if len(sub.Items.Data) > 0 {
			plan = b.priceToPlan[sub.Items.Data[0].Price.ID]
		}
		periodEnd := time.Unix(sub.CurrentPeriodEnd, 0).UTC()
		if plan != "" {
			_, _ = b.db.ExecContext(ctx, `
				UPDATE teams SET plan = $1, subscription_status = $2, current_period_end = $3,
				       cancel_at_period_end = $4, stripe_subscription_id = $5, updated_at = NOW()
				WHERE stripe_customer_id = $6
			`, plan, sub.Status, periodEnd, sub.CancelAtPeriodEnd, sub.ID, sub.Customer)
		} else {
			_, _ = b.db.ExecContext(ctx, `
				UPDATE teams SET subscription_status = $1, current_period_end = $2,
				       cancel_at_period_end = $3, stripe_subscription_id = $4, updated_at = NOW()
				WHERE stripe_customer_id = $5
			`, sub.Status, periodEnd, sub.CancelAtPeriodEnd, sub.ID, sub.Customer)
		}

	case "customer.subscription.deleted":
		var sub stripeSubscriptionPayload
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			break
		}
		_, _ = b.db.ExecContext(ctx, `
			UPDATE teams SET plan = 'free', subscription_status = 'canceled', cancel_at_period_end = false, updated_at = NOW()
			WHERE stripe_customer_id = $1
		`, sub.Customer)
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}
