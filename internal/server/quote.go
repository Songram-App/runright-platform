package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// QuoteRequest is a "please give us pricing" lead — captured when a free-tier
// customer hits a usage cap, or submitted proactively from Settings.
type QuoteRequest struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Company   string    `json:"company,omitempty"`
	Message   string    `json:"message,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// createQuoteRequest stores a pricing lead. Any authenticated team member can
// submit one — there's no self-serve upgrade flow for Cloud plans yet, so
// this is the whole "ask us for pricing" path.
func (s *Server) createQuoteRequest(c *gin.Context) {
	ctx := c.Request.Context()
	userEmail := getUserEmail(c)

	var body struct {
		Name    string `json:"name" binding:"required"`
		Email   string `json:"email" binding:"required,email"`
		Company string `json:"company"`
		Message string `json:"message"`
		Reason  string `json:"reason"` // 'jobs_per_month' | 'repos' | 'proactive'
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and a valid email are required"})
		return
	}

	jobsThisMonth, reposConnected := s.currentJobAndRepoCounts(ctx)
	usageJSON, _ := json.Marshal(gin.H{
		"plan":            s.plan,
		"jobs_this_month": jobsThisMonth,
		"repos_connected": reposConnected,
	})

	var id string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO quote_requests (name, email, company, message, reason, requested_by, usage_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, body.Name, body.Email, body.Company, body.Message, body.Reason, userEmail, usageJSON).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to submit request"})
		return
	}

	s.logAudit(ctx, userEmail, c, "quote.request", "quote_request", id, body.Email, gin.H{"reason": body.Reason})
	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "received"})
}

// listQuoteRequests is an admin-only view of submitted pricing leads.
func (s *Server) listQuoteRequests(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, email, company, message, reason, created_at
		FROM quote_requests ORDER BY created_at DESC LIMIT 200
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list quote requests"})
		return
	}
	defer rows.Close()

	var out []QuoteRequest
	for rows.Next() {
		var q QuoteRequest
		var company, message, reason sql.NullString
		if err := rows.Scan(&q.ID, &q.Name, &q.Email, &company, &message, &reason, &q.CreatedAt); err != nil {
			continue
		}
		q.Company = company.String
		q.Message = message.String
		q.Reason = reason.String
		out = append(out, q)
	}
	c.JSON(http.StatusOK, gin.H{"quote_requests": out})
}
