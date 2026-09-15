package server

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// registerEmbeddingRoutes adds embedding-related API endpoints.
func (s *Server) registerEmbeddingRoutes(rg *gin.RouterGroup) {
	emb := rg.Group("/embeddings")
	{
		emb.GET("/status", s.handleEmbeddingStatus)
		emb.POST("/index", s.handleEmbeddingIndex)
		emb.POST("/search", s.handleEmbeddingSearch)
		emb.GET("/stats", s.handleEmbeddingStats)
	}
}

// handleEmbeddingStatus returns the embedding service configuration status.
func (s *Server) handleEmbeddingStatus(c *gin.Context) {
	if s.embeddings == nil {
		c.JSON(http.StatusOK, gin.H{
			"configured":  false,
			"description": "Embedding service not initialized",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"configured":  s.embeddings.IsConfigured(),
		"rag_enabled": s.getAssistant() != nil,
	})
}

// handleEmbeddingIndex triggers indexing of jobs that don't have embeddings.
func (s *Server) handleEmbeddingIndex(c *gin.Context) {
	if s.embeddings == nil || !s.embeddings.IsConfigured() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Embedding service not configured"})
		return
	}

	batchSize := 50
	if bs := c.Query("batch_size"); bs != "" {
		if parsed, err := strconv.Atoi(bs); err == nil && parsed > 0 {
			batchSize = parsed
		}
	}

	indexed, err := s.embeddings.IndexAllJobs(c.Request.Context(), batchSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"indexed": indexed,
		"message": "Indexing complete",
	})
}

// handleEmbeddingSearch performs a semantic search across indexed jobs.
func (s *Server) handleEmbeddingSearch(c *gin.Context) {
	if s.embeddings == nil || !s.embeddings.IsConfigured() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Embedding service not configured"})
		return
	}

	var req struct {
		Query      string `json:"query" binding:"required"`
		Limit      int    `json:"limit"`
		Repository string `json:"repository"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Limit == 0 {
		req.Limit = 10
	}

	results, err := s.embeddings.Search(c.Request.Context(), req.Query, req.Limit, req.Repository)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"results": results,
		"count":   len(results),
	})
}

// handleEmbeddingStats returns statistics about the embedding index.
func (s *Server) handleEmbeddingStats(c *gin.Context) {
	if s.embeddings == nil {
		c.JSON(http.StatusOK, gin.H{
			"configured": false,
		})
		return
	}

	stats, err := s.embeddings.GetIndexStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	stats["configured"] = s.embeddings.IsConfigured()
	c.JSON(http.StatusOK, stats)
}
