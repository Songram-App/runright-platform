package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sgbudje/runright-platform/internal/assistant"
	"github.com/sgbudje/runright-platform/internal/types"
)

// assistantChat handles POST /api/v1/assistant/chat
func (s *Server) assistantChat(c *gin.Context) {
	var req types.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	if req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	userID := c.GetString("user_email")

	resp, err := s.getAssistant().Chat(c.Request.Context(), req, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// assistantConversations handles GET /api/v1/assistant/conversations
func (s *Server) assistantConversations(c *gin.Context) {
	userID := c.GetString("user_email")

	convs, err := s.getAssistant().GetConversations(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, convs)
}

// assistantGetConversation handles GET /api/v1/assistant/conversations/:id
func (s *Server) assistantGetConversation(c *gin.Context) {
	convID := c.Param("id")
	if convID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversation id required"})
		return
	}

	userID := c.GetString("user_email")

	conv, messages, err := s.getAssistant().GetConversation(c.Request.Context(), convID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"conversation": conv,
		"messages":     messages,
	})
}

// assistantDeleteConversation handles DELETE /api/v1/assistant/conversations/:id
func (s *Server) assistantDeleteConversation(c *gin.Context) {
	convID := c.Param("id")
	if convID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversation id required"})
		return
	}

	userID := c.GetString("user_email")

	if err := s.getAssistant().DeleteConversation(c.Request.Context(), convID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// assistantDeleteAllConversations handles DELETE /api/v1/assistant/conversations
func (s *Server) assistantDeleteAllConversations(c *gin.Context) {
	userID := c.GetString("user_email")

	n, err := s.getAssistant().DeleteAllConversations(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": n})
}

// assistantQuickStats handles GET /api/v1/assistant/stats
func (s *Server) assistantQuickStats(c *gin.Context) {
	stats, err := s.getAssistant().GetQuickStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// assistantSuggestedQuestions handles GET /api/v1/assistant/suggestions
func (s *Server) assistantSuggestedQuestions(c *gin.Context) {
	questions := s.getAssistant().SuggestedQuestions(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"questions": questions})
}

// assistantStatus handles GET /api/v1/assistant/status
func (s *Server) assistantStatus(c *gin.Context) {
	a := s.getAssistant()
	status := gin.H{
		"configured": a.IsConfigured(),
	}
	// Add provider info if configured
	if a.IsConfigured() {
		for k, v := range a.GetProviderInfo() {
			status[k] = v
		}
	}
	c.JSON(http.StatusOK, status)
}

// assistantListTools handles GET /api/v1/assistant/tools
func (s *Server) assistantListTools(c *gin.Context) {
	tools := s.getAssistant().AvailableTools()
	c.JSON(http.StatusOK, gin.H{"tools": tools})
}

// assistantExecuteTool handles POST /api/v1/assistant/tools/execute
func (s *Server) assistantExecuteTool(c *gin.Context) {
	var req struct {
		ToolCall       assistant.ToolCall `json:"tool_call"`
		ConversationID string             `json:"conversation_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user ID from auth context
	userID := "anonymous"
	if id, exists := c.Get("user_id"); exists {
		userID = id.(string)
	}

	result, err := s.getAssistant().ExecuteTool(c.Request.Context(), req.ToolCall, userID, req.ConversationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// assistantListActions handles GET /api/v1/assistant/actions
func (s *Server) assistantListActions(c *gin.Context) {
	// Get user ID from auth context
	userID := "anonymous"
	if id, exists := c.Get("user_id"); exists {
		userID = id.(string)
	}

	// Query recent actions for this user
	rows, err := s.db.QueryContext(c.Request.Context(), `
		SELECT id, conversation_id, tool_name, arguments, result, success, created_at
		FROM assistant_action_log
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var actions []assistant.ActionLog
	for rows.Next() {
		var a assistant.ActionLog
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.ToolName, &a.Arguments, &a.Result, &a.Success, &a.CreatedAt); err != nil {
			continue
		}
		a.UserID = userID
		actions = append(actions, a)
	}

	c.JSON(http.StatusOK, gin.H{"actions": actions})
}

// registerAssistantRoutes adds assistant endpoints to the router. The
// assistant itself is already initialized (and kept up to date by
// reloadAssistant) by the time routes are registered.
func (s *Server) registerAssistantRoutes(v1 *gin.RouterGroup) {
	ast := v1.Group("/assistant")
	{
		ast.GET("/status", s.assistantStatus)
		ast.GET("/suggestions", s.assistantSuggestedQuestions)
		ast.GET("/stats", s.assistantQuickStats)
		ast.POST("/chat", s.assistantChat)
		ast.GET("/conversations", s.assistantConversations)
		ast.DELETE("/conversations", s.assistantDeleteAllConversations)
		ast.GET("/conversations/:id", s.assistantGetConversation)
		ast.DELETE("/conversations/:id", s.assistantDeleteConversation)
		// Agentic tool endpoints
		ast.GET("/tools", s.assistantListTools)
		ast.POST("/tools/execute", s.assistantExecuteTool)
		ast.GET("/actions", s.assistantListActions)
	}
}
