package api

import (
	"log/slog"
	"strconv"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// adminAuthMiddleware validates admin token from request header
func adminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader(config.Config.LlmServerTokenHeader)
		if token == "" {
			c.JSON(401, gin.H{"error": "Missing authentication token"})
			c.Abort()
			return
		}

		if token != config.Config.LlmServerToken {
			c.JSON(401, gin.H{"error": "Invalid authentication token"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// getUserFromContext extracts user identifier from context for audit
func getUserFromContext(c *gin.Context) string {
	if userID := c.GetHeader("x-user-id"); userID != "" {
		return userID
	}
	return "admin"
}

// handlePromptsApis registers all prompt management endpoints
func handlePromptsApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	adminGroup := r.Group("/api/admin/prompts")
	adminGroup.Use(adminAuthMiddleware())

	// Experiment endpoints
	adminGroup.POST("/experiments", createExperiment)
	adminGroup.PATCH("/experiments/:name/accounts", updateExperimentAccounts)
	adminGroup.GET("/experiments/active", listActiveExperiments)
	adminGroup.POST("/experiments/:name/disable", disableExperiment)
	adminGroup.GET("/experiments/:name/metrics", getExperimentMetrics)

	// Configuration endpoints
	adminGroup.POST("/config/version", updateActiveVersion)
	adminGroup.GET("/config", getPromptConfig)

	// Audit endpoints
	adminGroup.GET("/audit", getAuditLogs)

	// Utility endpoints
	adminGroup.POST("/cache/clear", clearCache)

	// Image attachment management lives under its own /api/admin/attachments
	// namespace so attachment endpoints don't share an RBAC scope with prompts.
	attachmentsGroup := r.Group("/api/admin/attachments")
	attachmentsGroup.Use(adminAuthMiddleware())
	attachmentsGroup.POST("/purge", purgeExpiredAttachments)
}

// purgeExpiredAttachments manually triggers cleanup of expired image attachment data.
func purgeExpiredAttachments(c *gin.Context) {
	count, err := core.PurgeExpiredImageAttachments()
	if err != nil {
		slog.Error("admin: attachment purge failed", "error", err)
		c.JSON(500, gin.H{"error": "purge failed"})
		return
	}

	slog.Info("admin: attachment purge completed", "count", count, "retention_days", core.GetImageRetentionDays())
	c.JSON(200, gin.H{
		"purged":         count,
		"retention_days": core.GetImageRetentionDays(),
	})
}

// createExperiment creates a new prompt experiment
func createExperiment(c *gin.Context) {
	var req prompts.ExperimentCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Error("prompts api: failed to bind create experiment request", "error", err)
		c.JSON(400, gin.H{"error": "Invalid request payload", "details": err.Error()})
		return
	}

	// Validate dates if provided
	var startDate, endDate *time.Time
	if req.StartDate != nil {
		t, err := time.Parse(time.RFC3339, *req.StartDate)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid start_date format, use RFC3339"})
			return
		}
		startDate = &t
	}
	if req.EndDate != nil {
		t, err := time.Parse(time.RFC3339, *req.EndDate)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid end_date format, use RFC3339"})
			return
		}
		endDate = &t
	}

	// Validate dates
	if startDate != nil && endDate != nil && endDate.Before(*startDate) {
		c.JSON(400, gin.H{"error": "end_date must be after start_date"})
		return
	}

	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Check if experiment already exists
	existing, err := db.GetExperiment(ctx, req.Name)
	if err != nil {
		slog.Error("prompts api: failed to check existing experiment", "error", err)
		c.JSON(500, gin.H{"error": "Failed to check existing experiment"})
		return
	}
	if existing != nil {
		c.JSON(400, gin.H{"error": "Experiment with this name already exists"})
		return
	}

	// Create experiment
	user := getUserFromContext(c)
	exp := &prompts.DBExperiment{
		Name:           req.Name,
		PromptName:     req.PromptName,
		Category:       prompts.PromptCategory(req.Category),
		TestVersion:    req.TestVersion,
		ControlVersion: req.ControlVersion,
		TargetAccounts: req.TargetAccounts,
		Providers:      req.Providers,
		StartDate:      startDate,
		EndDate:        endDate,
		Enabled:        true,
		Description:    &req.Description,
		CreatedBy:      &user,
		UpdatedBy:      &user,
	}

	if err := db.CreateExperiment(ctx, exp); err != nil {
		slog.Error("prompts api: failed to create experiment", "error", err)
		c.JSON(500, gin.H{"error": "Failed to create experiment"})
		return
	}

	// Create audit log
	_ = db.CreateAuditLog(ctx, &prompts.DBAuditLog{
		PromptName:   req.PromptName,
		Category:     prompts.PromptCategory(req.Category),
		Action:       "EXPERIMENT_CREATED",
		NewVersion:   &req.TestVersion,
		ExperimentID: &exp.ID,
		ChangedBy:    user,
		Reason:       &req.Description,
	})

	// Clear cache for affected prompts
	loader.ClearCacheForPrompt(req.PromptName, prompts.PromptCategory(req.Category))

	slog.Info("prompts api: experiment created",
		"name", req.Name,
		"prompt", req.PromptName,
		"test_version", req.TestVersion,
		"accounts", len(req.TargetAccounts))

	c.JSON(200, gin.H{
		"success":               true,
		"experiment_id":         exp.ID,
		"message":               "Experiment created successfully",
		"target_accounts_count": len(req.TargetAccounts),
	})
}

// updateExperimentAccounts updates the target accounts for an experiment
func updateExperimentAccounts(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(400, gin.H{"error": "Experiment name is required"})
		return
	}

	var req prompts.ExperimentUpdateAccountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Error("prompts api: failed to bind update accounts request", "error", err)
		c.JSON(400, gin.H{"error": "Invalid request payload", "details": err.Error()})
		return
	}

	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Update accounts
	if err := db.UpdateExperimentAccounts(ctx, name, req.Action, req.Accounts); err != nil {
		slog.Error("prompts api: failed to update experiment accounts",
			"name", name,
			"action", req.Action,
			"error", err)
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Get updated experiment
	exp, err := db.GetExperiment(ctx, name)
	if err != nil || exp == nil {
		c.JSON(500, gin.H{"error": "Failed to retrieve updated experiment"})
		return
	}

	// Clear cache for affected accounts
	for _, accountID := range req.Accounts {
		loader.ClearCacheForAccount(accountID)
	}

	slog.Info("prompts api: experiment accounts updated",
		"name", name,
		"action", req.Action,
		"total_accounts", len(exp.TargetAccounts))

	c.JSON(200, gin.H{
		"success":         true,
		"message":         "Accounts updated",
		"total_accounts":  len(exp.TargetAccounts),
		"experiment_name": name,
	})
}

// listActiveExperiments lists all currently active experiments
func listActiveExperiments(c *gin.Context) {
	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Build filters
	filters := make(map[string]string)
	if promptName := c.Query("prompt_name"); promptName != "" {
		filters["prompt_name"] = promptName
	}
	if category := c.Query("category"); category != "" {
		filters["category"] = category
	}
	if accountID := c.Query("account_id"); accountID != "" {
		filters["account_id"] = accountID
	}

	experiments, err := db.ListActiveExperiments(ctx, filters)
	if err != nil {
		slog.Error("prompts api: failed to list experiments", "error", err)
		c.JSON(500, gin.H{"error": "Failed to list experiments"})
		return
	}

	c.JSON(200, gin.H{
		"experiments": experiments,
		"count":       len(experiments),
	})
}

// disableExperiment disables an experiment
func disableExperiment(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(400, gin.H{"error": "Experiment name is required"})
		return
	}

	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Get experiment before disabling
	exp, err := db.GetExperiment(ctx, name)
	if err != nil {
		slog.Error("prompts api: failed to get experiment", "name", name, "error", err)
		c.JSON(500, gin.H{"error": "Failed to get experiment"})
		return
	}
	if exp == nil {
		c.JSON(404, gin.H{"error": "Experiment not found"})
		return
	}

	// Disable experiment
	if err := db.DisableExperiment(ctx, name); err != nil {
		slog.Error("prompts api: failed to disable experiment", "name", name, "error", err)
		c.JSON(500, gin.H{"error": "Failed to disable experiment"})
		return
	}

	// Create audit log
	user := getUserFromContext(c)
	_ = db.CreateAuditLog(ctx, &prompts.DBAuditLog{
		PromptName:   exp.PromptName,
		Category:     exp.Category,
		Action:       "EXPERIMENT_DISABLED",
		ExperimentID: &exp.ID,
		ChangedBy:    user,
	})

	// Clear cache for affected accounts
	for _, accountID := range exp.TargetAccounts {
		loader.ClearCacheForAccount(accountID)
	}

	slog.Info("prompts api: experiment disabled", "name", name)

	c.JSON(200, gin.H{
		"success":         true,
		"message":         "Experiment disabled",
		"experiment_name": name,
	})
}

// updateActiveVersion updates the active version for a prompt
func updateActiveVersion(c *gin.Context) {
	var req prompts.ConfigVersionUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Error("prompts api: failed to bind update version request", "error", err)
		c.JSON(400, gin.H{"error": "Invalid request payload", "details": err.Error()})
		return
	}

	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Get existing config to track old version
	provider := req.Provider
	if provider == "" {
		provider = "default"
	}

	accountID := ""
	if req.AccountID != nil {
		accountID = *req.AccountID
	}

	oldConfig, _ := db.GetConfig(ctx, req.PromptName, prompts.PromptCategory(req.Category), provider, accountID)
	var oldVersion *string
	if oldConfig != nil {
		oldVersion = &oldConfig.ActiveVersion
	}

	// Upsert new config
	user := getUserFromContext(c)
	config := &prompts.DBConfig{
		PromptName:    req.PromptName,
		Category:      prompts.PromptCategory(req.Category),
		Provider:      provider,
		ActiveVersion: req.NewVersion,
		AccountID:     req.AccountID,
		Enabled:       true,
		Priority:      0,
		UpdatedBy:     &user,
	}

	if err := db.UpsertConfig(ctx, config); err != nil {
		slog.Error("prompts api: failed to update config", "error", err)
		c.JSON(500, gin.H{"error": "Failed to update version"})
		return
	}

	// Create audit log
	_ = db.CreateAuditLog(ctx, &prompts.DBAuditLog{
		PromptName: req.PromptName,
		Category:   prompts.PromptCategory(req.Category),
		Provider:   &provider,
		AccountID:  req.AccountID,
		Action:     "VERSION_CHANGE",
		OldVersion: oldVersion,
		NewVersion: &req.NewVersion,
		ChangedBy:  user,
		Reason:     &req.Reason,
	})

	// Clear cache
	if req.AccountID != nil {
		loader.ClearCacheForAccount(*req.AccountID)
	} else {
		loader.ClearCacheForPrompt(req.PromptName, prompts.PromptCategory(req.Category))
	}

	slog.Info("prompts api: version updated",
		"prompt", req.PromptName,
		"old_version", oldVersion,
		"new_version", req.NewVersion)

	response := gin.H{
		"success":       true,
		"message":       "Version updated successfully",
		"prompt_name":   req.PromptName,
		"new_version":   req.NewVersion,
		"cache_cleared": true,
	}
	if oldVersion != nil {
		response["old_version"] = *oldVersion
	}

	c.JSON(200, response)
}

// getPromptConfig retrieves the configuration for a prompt
func getPromptConfig(c *gin.Context) {
	promptName := c.Query("name")
	category := c.Query("category")

	if promptName == "" || category == "" {
		c.JSON(400, gin.H{"error": "name and category query parameters are required"})
		return
	}

	provider := c.Query("provider")
	if provider == "" {
		provider = "default"
	}

	accountID := c.Query("account_id")
	if accountID == "" {
		accountID = ""
	}

	loader := prompts.GetLoader()

	// Get the prompt to see what version would be used
	req := prompts.PromptRequest{
		Name:      promptName,
		Category:  prompts.PromptCategory(category),
		Provider:  provider,
		AccountID: accountID,
	}

	resp, err := loader.GetPrompt(c.Request.Context(), req)
	if err != nil {
		slog.Error("prompts api: failed to get prompt", "error", err)
		c.JSON(500, gin.H{"error": "Failed to get prompt configuration"})
		return
	}

	// Get available versions
	versions := loader.GetAvailableVersions(promptName, prompts.PromptCategory(category), provider)

	// Get preview of content (first 100 chars)
	contentPreview := resp.Content
	if len(contentPreview) > 100 {
		contentPreview = contentPreview[:100] + "..."
	}

	response := gin.H{
		"prompt_name":        promptName,
		"category":           category,
		"provider":           provider,
		"version":            resp.Metadata.Version,
		"config_source":      resp.Metadata.ConfigSource,
		"available_versions": versions,
		"content_preview":    contentPreview,
		"cache_hit":          resp.Metadata.CacheHit,
	}

	if accountID != "" {
		response["account_id"] = accountID
	}

	if resp.Metadata.ExperimentID != nil {
		response["experiment_id"] = resp.Metadata.ExperimentID.String()
	}

	c.JSON(200, response)
}

// getExperimentMetrics retrieves metrics for an experiment
func getExperimentMetrics(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(400, gin.H{"error": "Experiment name is required"})
		return
	}

	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Get experiment
	exp, err := db.GetExperiment(ctx, name)
	if err != nil {
		slog.Error("prompts api: failed to get experiment", "name", name, "error", err)
		c.JSON(500, gin.H{"error": "Failed to get experiment"})
		return
	}
	if exp == nil {
		c.JSON(404, gin.H{"error": "Experiment not found"})
		return
	}

	// Parse date range
	startDate := exp.CreatedAt
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		t, err := time.Parse(time.RFC3339, startDateStr)
		if err == nil {
			startDate = t
		}
	}

	endDate := time.Now()
	if endDateStr := c.Query("end_date"); endDateStr != "" {
		t, err := time.Parse(time.RFC3339, endDateStr)
		if err == nil {
			endDate = t
		}
	}

	// Get metrics
	metrics, err := db.GetExperimentMetrics(ctx, name, startDate, endDate)
	if err != nil {
		slog.Error("prompts api: failed to get experiment metrics",
			"name", name,
			"error", err)
		c.JSON(500, gin.H{"error": "Failed to get experiment metrics"})
		return
	}

	c.JSON(200, gin.H{
		"experiment_name": name,
		"test_version":    exp.TestVersion,
		"control_version": exp.ControlVersion,
		"metrics":         metrics,
		"time_range": gin.H{
			"start": startDate.Format(time.RFC3339),
			"end":   endDate.Format(time.RFC3339),
		},
	})
}

// getAuditLogs retrieves audit logs with optional filters
func getAuditLogs(c *gin.Context) {
	loader := prompts.GetLoader()
	db := loader.GetDB()
	if db == nil {
		c.JSON(503, gin.H{"error": "Database not available"})
		return
	}

	ctx := c.Request.Context()

	// Build filters
	filters := make(map[string]any)
	if promptName := c.Query("prompt_name"); promptName != "" {
		filters["prompt_name"] = promptName
	}
	if accountID := c.Query("account_id"); accountID != "" {
		filters["account_id"] = accountID
	}
	if changedBy := c.Query("changed_by"); changedBy != "" {
		filters["changed_by"] = changedBy
	}
	if startDate := c.Query("start_date"); startDate != "" {
		if t, err := time.Parse(time.RFC3339, startDate); err == nil {
			filters["start_date"] = t
		}
	}
	if endDate := c.Query("end_date"); endDate != "" {
		if t, err := time.Parse(time.RFC3339, endDate); err == nil {
			filters["end_date"] = t
		}
	}

	// Parse limit (default: 100)
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := parseInt(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	logs, err := db.GetAuditLogs(ctx, filters, limit)
	if err != nil {
		slog.Error("prompts api: failed to get audit logs", "error", err)
		c.JSON(500, gin.H{"error": "Failed to get audit logs"})
		return
	}

	c.JSON(200, gin.H{
		"audit_logs": logs,
		"total":      len(logs),
		"limit":      limit,
	})
}

// clearCache clears the prompt cache
func clearCache(c *gin.Context) {
	loader := prompts.GetLoader()

	// Check if specific prompt or account should be cleared
	promptName := c.Query("prompt_name")
	category := c.Query("category")
	accountID := c.Query("account_id")

	if accountID != "" {
		loader.ClearCacheForAccount(accountID)
		slog.Info("prompts api: cache cleared for account", "account_id", accountID)
		c.JSON(200, gin.H{
			"success":    true,
			"message":    "Cache cleared for account",
			"account_id": accountID,
		})
		return
	}

	if promptName != "" && category != "" {
		loader.ClearCacheForPrompt(promptName, prompts.PromptCategory(category))
		slog.Info("prompts api: cache cleared for prompt",
			"prompt", promptName,
			"category", category)
		c.JSON(200, gin.H{
			"success":     true,
			"message":     "Cache cleared for prompt",
			"prompt_name": promptName,
			"category":    category,
		})
		return
	}

	// Clear all cache
	loader.ClearCache()
	slog.Info("prompts api: all cache cleared")

	c.JSON(200, gin.H{
		"success": true,
		"message": "All cache cleared",
	})
}

// parseInt parses a string to int
func parseInt(s string) (int, error) {
	return strconv.Atoi(s)
}
