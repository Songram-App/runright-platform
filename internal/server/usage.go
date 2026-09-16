package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// errSeatLimitReached is returned by user-creation paths (SSO login, GitHub
// login) when adding a genuinely new user would exceed this plan's seat cap.
var errSeatLimitReached = errors.New("seat limit reached for the current plan")

// getUsageSummary reports this instance's current metered usage against its
// plan limits. Self-hosted deployments (s.plan == "") always report
// metered=false so the frontend never shows upgrade/quote prompts there —
// caps only apply to RunRight Cloud tenants.
func (s *Server) getUsageSummary(c *gin.Context) {
	ctx := c.Request.Context()

	if s.plan == "" {
		c.JSON(http.StatusOK, gin.H{"metered": false})
		return
	}

	limit, ok := plans[s.plan]
	if !ok {
		limit = plans["free"]
	}

	jobsThisMonth, reposConnected := s.currentJobAndRepoCounts(ctx)
	memberCount := s.currentMemberCount(ctx)

	c.JSON(http.StatusOK, gin.H{
		"metered":            true,
		"plan":               s.plan,
		"plan_name":          limit.Name,
		"jobs_this_month":    jobsThisMonth,
		"max_jobs_per_month": limit.MaxJobsPerMonth,
		"repos_connected":    reposConnected,
		"max_repos":          limit.MaxRepos,
		"members_count":      memberCount,
		"max_members":        limit.MaxMembers,
		"jobs_at_cap":        limit.MaxJobsPerMonth > 0 && jobsThisMonth >= limit.MaxJobsPerMonth,
		"repos_at_cap":       limit.MaxRepos > 0 && reposConnected >= limit.MaxRepos,
		"members_at_cap":     limit.MaxMembers > 0 && memberCount >= limit.MaxMembers,
	})
}

// currentJobAndRepoCounts returns the distinct job count for the current
// calendar month and the all-time distinct repository count. Errors are
// swallowed and reported as 0 — usage reporting must never break the
// dashboard or job ingestion.
func (s *Server) currentJobAndRepoCounts(ctx context.Context) (jobsThisMonth, reposConnected int) {
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT job_id) FROM jobs WHERE created_at >= date_trunc('month', now())`,
	).Scan(&jobsThisMonth)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT repository) FROM jobs WHERE repository IS NOT NULL AND repository != ''`,
	).Scan(&reposConnected)
	return
}

// currentMemberCount returns the number of distinct dashboard users
// (sso_users) — the actual user roster for a RunRight Cloud tenant, which
// does not use the separate teams/team_members system.
func (s *Server) currentMemberCount(ctx context.Context) (count int) {
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sso_users`).Scan(&count)
	return
}

// checkSeatCap returns errSeatLimitReached if creating a brand-new dashboard
// user for this email would exceed the plan's seat cap. An email that
// already has an sso_users row (someone logging back in) is never blocked.
// Fails open on any lookup error — a seat-cap bug must never lock everyone
// out of their own dashboard.
func (s *Server) checkSeatCap(ctx context.Context, email string) error {
	if s.plan == "" || email == "" {
		return nil
	}
	limit, ok := plans[s.plan]
	if !ok || limit.MaxMembers == 0 {
		return nil
	}

	var alreadyExists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM sso_users WHERE email = $1)`, email,
	).Scan(&alreadyExists); err != nil || alreadyExists {
		return nil
	}

	count := s.currentMemberCount(ctx)
	if count >= limit.MaxMembers {
		return errSeatLimitReached
	}
	return nil
}

// planCapReason checks whether ingesting a job with the given job_id /
// repository would exceed this instance's plan limits. It only ever blocks
// *new* distinct jobs/repos for the month — heartbeats/updates to a job
// already counted this month are never rejected mid-flight. Returns "" when
// the request is allowed (including on any lookup error — fail open, since
// this must never be the reason a customer's CI build breaks).
func (s *Server) planCapReason(ctx context.Context, jobID, repository string) string {
	if s.plan == "" {
		return ""
	}
	limit, ok := plans[s.plan]
	if !ok {
		return ""
	}

	if limit.MaxJobsPerMonth > 0 {
		var alreadyCounted bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM jobs WHERE job_id = $1 AND created_at >= date_trunc('month', now()))`,
			jobID,
		).Scan(&alreadyCounted); err == nil && !alreadyCounted {
			var count int
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(DISTINCT job_id) FROM jobs WHERE created_at >= date_trunc('month', now())`,
			).Scan(&count); err == nil && count >= limit.MaxJobsPerMonth {
				return "jobs_per_month"
			}
		}
	}

	if limit.MaxRepos > 0 && repository != "" {
		var alreadyCounted bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM jobs WHERE repository = $1)`,
			repository,
		).Scan(&alreadyCounted); err == nil && !alreadyCounted {
			var count int
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(DISTINCT repository) FROM jobs WHERE repository IS NOT NULL AND repository != ''`,
			).Scan(&count); err == nil && count >= limit.MaxRepos {
				return "repos"
			}
		}
	}

	return ""
}
