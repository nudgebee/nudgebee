package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/triage"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// TriageResponse represents the combined triage information for an event
type TriageResponse struct {
	EventID          string                   `json:"event_id"`
	IsDuplicate      bool                     `json:"is_duplicate"`
	DuplicateInfo    *DuplicateInfo           `json:"duplicate_info,omitempty"`
	CorrelatedEvents []triage.CorrelatedEvent `json:"correlated_events"`
	HistoricalStats  *triage.HistoricalStats  `json:"historical_stats"`
	HourlyTrend      []triage.HourlyBucket    `json:"hourly_trend"`
	CorrelationCount int                      `json:"correlation_count"`
}

// DuplicateInfo contains information about duplicate events
type DuplicateInfo struct {
	FirstEventID     string                  `json:"first_event_id"`
	OccurrenceNumber int                     `json:"occurrence_number"`
	DuplicateChain   []triage.DuplicateEvent `json:"duplicate_chain"`
	TotalOccurrences int                     `json:"total_occurrences"`
}

// handleHasuraTriageApis registers the Hasura triage action endpoint
func handleHasuraTriageApis(router *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := router.Group("/hasura")

	groupV2.POST("/triage", func(c *gin.Context) {
		var hasuraPayload HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}

		handleHasuraTriageAction(&hasuraPayload, c, tracer, meter, logger)
	})
}

// handleHasuraTriageAction routes triage actions to their handlers
func handleHasuraTriageAction(h *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	actionName := h.Action.Name

	ctx, err := buildContextFromHasuraPayload(c, h, tracer, meter, logger)
	if err != nil {
		logger.Error("Failed to build context from hasura payload", "error", err, "action", actionName)
		c.JSON(400, common.ErrorHasuraActionBadRequest("failed to build request context"))
		return
	}

	switch actionName {
	case "event_get_triage":
		handleEventGetTriage(h, c, ctx)
	case "event_get_duplicates":
		handleEventGetDuplicates(h, c, ctx)
	case "event_get_correlations":
		handleEventGetCorrelations(h, c, ctx)
	case "event_get_timeline":
		handleEventGetTimeline(h, c, ctx)
	case "event_backfill_triage":
		handleEventBackfillTriage(h, c, ctx)
	case "event_deduplicate_correlations":
		handleEventDeduplicateCorrelations(h, c, ctx)
	// Classification and Rules actions
	case "event_classify_preview":
		handleEventClassifyPreview(h, c, ctx)
	case "event_classify":
		handleEventClassify(h, c, ctx)
	case "event_get_duplicate_suggestions":
		handleEventGetDuplicateSuggestions(h, c, ctx)
	case "event_bulk_operation_status":
		handleEventBulkOperationStatus(h, c, ctx)
	case "event_create_triage_rule":
		handleEventCreateTriageRule(h, c, ctx)
	case "event_get_triage_rules":
		handleEventGetTriageRules(h, c, ctx)
	case "event_preview_triage_rule":
		handleEventPreviewTriageRule(h, c, ctx)
	case "event_delete_triage_rule":
		handleEventDeleteTriageRule(h, c, ctx)
	case "event_update_triage_rule":
		handleEventUpdateTriageRule(h, c, ctx)
	case "event_update_nb_status":
		handleEventUpdateNBStatus(h, c, ctx)
	case "event_get_classification":
		handleEventGetClassification(h, c, ctx)
	case "event_toggle_system_rule_override":
		handleEventToggleSystemRuleOverride(h, c, ctx)
	case "event_get_triage_rule_events":
		handleEventGetTriageRuleEvents(h, c, ctx)
	case "event_get_threshold_suggestion":
		handleEventGetThresholdSuggestion(h, c, ctx)
	case "event_list_threshold_suggestions":
		handleEventListThresholdSuggestions(h, c, ctx)
	case "event_get_recurrence_info":
		handleEventGetRecurrenceInfo(h, c, ctx)
	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest(fmt.Sprintf("unknown action: %s", actionName)))
	}
}

// handleEventGetTriage returns comprehensive triage information for an event
func handleEventGetTriage(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("event_id is required"))
		return
	}

	// Get event to extract fingerprint and account_id
	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(400, common.ErrorHasuraActionBadRequest("event not found"))
		return
	}

	if ev.CloudAccountId == nil || *ev.CloudAccountId == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("event has no account ID"))
		return
	}

	if ev.Fingerprint == nil || *ev.Fingerprint == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("event has no fingerprint"))
		return
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("database connection failed"))
		return
	}
	response := TriageResponse{
		EventID: eventID,
	}

	// Get tenant ID for tenant isolation
	tenantID := ctx.GetSecurityContext().GetTenantId()

	// 1. Check if this is a duplicate
	duplicates, err := triage.GetDuplicateChain(ctx.GetContext(), dbms.Db, eventID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get duplicate chain", "error", err, "event_id", eventID)
	} else if len(duplicates) > 0 {
		response.IsDuplicate = true
		response.DuplicateInfo = &DuplicateInfo{
			FirstEventID:     duplicates[0].FirstEventID,
			OccurrenceNumber: duplicates[len(duplicates)-1].OccurrenceNumber,
			DuplicateChain:   duplicates,
			TotalOccurrences: len(duplicates),
		}
	}

	// 2. Get correlated events
	correlations, err := triage.GetCorrelatedEvents(ctx.GetContext(), dbms.Db, eventID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get correlated events", "error", err, "event_id", eventID)
	} else {
		response.CorrelatedEvents = correlations
		response.CorrelationCount = len(correlations)
	}

	// 3. Get historical stats
	historicalStats, err := triage.ComputeHistoricalStats(ctx.GetContext(), dbms.Db, *ev.Fingerprint, *ev.CloudAccountId)
	if err != nil {
		ctx.GetLogger().Error("Failed to compute historical stats", "error", err, "event_id", eventID)
	} else {
		response.HistoricalStats = historicalStats
	}

	// 4. Get hourly trend
	hourlyTrend, err := triage.ComputeHourlyTrend(ctx.GetContext(), dbms.Db, *ev.Fingerprint, *ev.CloudAccountId)
	if err != nil {
		ctx.GetLogger().Error("Failed to compute hourly trend", "error", err, "event_id", eventID)
	} else {
		response.HourlyTrend = hourlyTrend
	}

	c.JSON(http.StatusOK, response)
}

// handleEventGetDuplicates returns the duplicate chain for an event
func handleEventGetDuplicates(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("event_id is required"))
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("database connection failed"))
		return
	}

	// Get tenant ID for tenant isolation
	tenantID := ctx.GetSecurityContext().GetTenantId()

	duplicates, err := triage.GetDuplicateChain(ctx.GetContext(), dbms.Db, eventID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get duplicate chain", "error", err, "event_id", eventID)
		c.JSON(400, common.ErrorHasuraActionBadRequest("failed to retrieve duplicates"))
		return
	}

	response := gin.H{
		"event_id":          eventID,
		"is_duplicate":      len(duplicates) > 0,
		"duplicate_chain":   duplicates,
		"total_occurrences": len(duplicates),
	}

	if len(duplicates) > 0 {
		response["first_event_id"] = duplicates[0].FirstEventID
		response["occurrence_number"] = duplicates[len(duplicates)-1].OccurrenceNumber
	}

	c.JSON(http.StatusOK, response)
}

// handleEventGetCorrelations returns correlated events for an event
func handleEventGetCorrelations(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("event_id is required"))
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("database connection failed"))
		return
	}

	// Get tenant ID for tenant isolation
	tenantID := ctx.GetSecurityContext().GetTenantId()

	correlations, err := triage.GetCorrelatedEvents(ctx.GetContext(), dbms.Db, eventID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get correlated events", "error", err, "event_id", eventID)
		c.JSON(400, common.ErrorHasuraActionBadRequest("failed to retrieve correlations"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"event_id":          eventID,
		"correlated_events": correlations,
		"correlation_count": len(correlations),
	})
}

// handleEventGetTimeline returns a chronological timeline of events related to the given event
func handleEventGetTimeline(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("event_id is required"))
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("database connection failed"))
		return
	}

	// Get tenant ID for tenant isolation
	tenantID := ctx.GetSecurityContext().GetTenantId()

	timeline, err := triage.BuildEventTimeline(ctx.GetContext(), dbms.Db, eventID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to build timeline", "error", err, "event_id", eventID)
		c.JSON(400, common.ErrorHasuraActionBadRequest("failed to build timeline: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, timeline)
}

// handleEventBackfillTriage runs the triage backfill process
func handleEventBackfillTriage(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	// Extract cloud_account_id (required)
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("cloud_account_id is required"))
		return
	}

	// Get database connection early (needed for event_id lookup)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("database connection failed"))
		return
	}

	// Build backfill options
	opts := triage.BackfillOptions{
		CloudAccountID: cloudAccountID,
	}

	// Extract optional event_id to backfill that specific event
	if eventID, ok := h.Input["event_id"].(string); ok && eventID != "" {
		// Process the specific event_id provided by the user
		ctx.GetLogger().Info("Backfilling specific event",
			"event_id", eventID)
		opts.FirstEventID = &eventID
	}

	// Extract optional fingerprint
	if fingerprint, ok := h.Input["fingerprint"].(string); ok && fingerprint != "" {
		opts.Fingerprint = &fingerprint
	}

	// Extract optional batch_size
	if batchSize, ok := h.Input["batch_size"].(float64); ok {
		opts.BatchSize = int(batchSize)
	}

	// Extract optional dry_run
	if dryRun, ok := h.Input["dry_run"].(bool); ok {
		opts.DryRun = dryRun
	}

	// Parse start time if provided
	if startTimeStr, ok := h.Input["start_time"].(string); ok && startTimeStr != "" {
		t, err := parseTime(startTimeStr)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid start_time format: "+err.Error()))
			return
		}
		opts.StartTime = &t
	}

	// Parse end time if provided
	if endTimeStr, ok := h.Input["end_time"].(string); ok && endTimeStr != "" {
		t, err := parseTime(endTimeStr)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid end_time format: "+err.Error()))
			return
		}
		opts.EndTime = &t
	}

	ctx.GetLogger().Info("Starting triage backfill",
		"cloud_account_id", cloudAccountID,
		"fingerprint", opts.Fingerprint,
		"first_event_id", opts.FirstEventID,
		"dry_run", opts.DryRun,
	)

	// Run backfill
	result, err := triage.BackfillTriage(ctx.GetContext(), dbms.Db, opts)
	if err != nil {
		ctx.GetLogger().Error("Backfill failed", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("backfill failed: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":               "completed",
		"total_events":         result.TotalEvents,
		"duplicates_detected":  result.DuplicatesDetected,
		"correlations_created": result.CorrelationsCreated,
		"errors":               result.Errors,
		"duration_seconds":     result.Duration.Seconds(),
	})
}

// handleEventDeduplicateCorrelations removes duplicate correlations where events correlate
// multiple times to events with the same fingerprint
func handleEventDeduplicateCorrelations(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	// Extract cloud_account_id (required)
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(400, common.ErrorHasuraActionBadRequest("cloud_account_id is required"))
		return
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("database connection failed"))
		return
	}

	ctx.GetLogger().Info("Starting correlation deduplication",
		"cloud_account_id", cloudAccountID,
	)

	// Run deduplication
	err = triage.DeduplicateCorrelations(ctx.GetContext(), dbms.Db, cloudAccountID)
	if err != nil {
		ctx.GetLogger().Error("Deduplication failed", "error", err)
		c.JSON(400, common.ErrorHasuraActionBadRequest("deduplication failed: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":           "completed",
		"cloud_account_id": cloudAccountID,
	})
}

// parseTime parses ISO 8601 time string
func parseTime(s string) (time.Time, error) {
	// Try multiple formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse time: %s", s)
}

// -------------------- Classification Handlers --------------------

// handleEventClassifyPreview returns the impact preview before classification
func handleEventClassifyPreview(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}

	classification, ok := h.Input["classification"].(string)
	if !ok || classification == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "classification is required"})
		return
	}

	applyScope, ok := h.Input["apply_scope"].(string)
	if !ok || applyScope == "" {
		applyScope = triage.ApplyScopeThisEvent
	}

	var applyUntilHours *int
	if hours, ok := h.Input["apply_until_hours"].(float64); ok {
		hoursInt := int(hours)
		applyUntilHours = &hoursInt
	}

	// Get the event to extract cloud_account_id
	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	if ev.CloudAccountId == nil || *ev.CloudAccountId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event has no account ID"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	req := triage.ClassifyPreviewRequest{
		EventID:         eventID,
		Classification:  classification,
		ApplyScope:      applyScope,
		ApplyUntilHours: applyUntilHours,
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	response, err := triage.ClassifyPreview(ctx.GetContext(), dbms.Db, req, *ev.CloudAccountId, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get classify preview", "error", err, "event_id", eventID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get classification preview: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// handleEventClassify classifies an event (TP/FP/BP/Duplicate)
func handleEventClassify(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}

	classification, ok := h.Input["classification"].(string)
	if !ok || classification == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "classification is required"})
		return
	}

	reasonCode, ok := h.Input["reason_code"].(string)
	if !ok || reasonCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_code is required"})
		return
	}

	applyScope, ok := h.Input["apply_scope"].(string)
	if !ok || applyScope == "" {
		applyScope = triage.ApplyScopeThisEvent
	}

	// Build request
	req := triage.ClassifyEventRequest{
		EventID:        eventID,
		Classification: classification,
		ReasonCode:     reasonCode,
		ApplyScope:     applyScope,
	}

	// Optional fields
	if reasonText, ok := h.Input["reason_text"].(string); ok {
		req.ReasonText = &reasonText
	}
	if priorityDirection, ok := h.Input["priority_direction"].(string); ok {
		req.PriorityDirection = &priorityDirection
	}
	if correctedPriority, ok := h.Input["corrected_priority"].(string); ok {
		req.CorrectedPriority = &correctedPriority
	}
	if hours, ok := h.Input["apply_until_hours"].(float64); ok {
		hoursInt := int(hours)
		req.ApplyUntilHours = &hoursInt
	}
	if linkedEventID, ok := h.Input["linked_event_id"].(string); ok {
		req.LinkedEventID = &linkedEventID
	}
	if applyToExisting, ok := h.Input["apply_to_existing"].(bool); ok {
		req.ApplyToExisting = applyToExisting
	}
	if confirmed, ok := h.Input["confirmed"].(bool); ok {
		req.Confirmed = confirmed
	}

	// Get the event to extract cloud_account_id
	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	if ev.CloudAccountId == nil || *ev.CloudAccountId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event has no account ID"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	response, err := triage.ClassifyEvent(ctx.GetContext(), dbms.Db, req, *ev.CloudAccountId, tenantID, userID)
	if err != nil {
		ctx.GetLogger().Error("Failed to classify event", "error", err, "event_id", eventID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to classify event: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)

	// Publish audit event
	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      tenantID,
		UserId:        userID,
		AccountId:     *ev.CloudAccountId,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageClassify,
		EventState:    req,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "event_classification",
		EventAction:   audit.EventActionCreate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"event_id": eventID, "classification": classification},
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}
}

// handleEventGetDuplicateSuggestions returns suggested original events for duplicate classification
func handleEventGetDuplicateSuggestions(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}

	// Get the event to extract cloud_account_id
	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	if ev.CloudAccountId == nil || *ev.CloudAccountId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event has no account ID"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	suggestions, err := triage.GetDuplicateSuggestions(ctx.GetContext(), dbms.Db, eventID, *ev.CloudAccountId)
	if err != nil {
		ctx.GetLogger().Error("Failed to get duplicate suggestions", "error", err, "event_id", eventID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get duplicate suggestions: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, triage.GetDuplicateSuggestionsResponse{
		Suggestions: suggestions,
	})
}

// handleEventBulkOperationStatus returns the status of a bulk operation
func handleEventBulkOperationStatus(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	jobID, ok := h.Input["job_id"].(string)
	if !ok || jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	op, err := triage.GetBulkOperationStatus(ctx.GetContext(), dbms.Db, jobID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get bulk operation status", "error", err, "job_id", jobID)
		c.JSON(http.StatusNotFound, gin.H{"error": "bulk operation not found"})
		return
	}

	c.JSON(http.StatusOK, triage.GetBulkOperationStatusResponse{
		JobID:           op.ID,
		Status:          op.Status,
		TotalEvents:     op.TotalEvents,
		ProcessedEvents: op.ProcessedEvents,
		CompletedAt:     op.CompletedAt,
		ErrorMessage:    op.ErrorMessage,
	})
}

// handleEventGetClassification returns the classification for an event
func handleEventGetClassification(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}

	// Get the event to extract cloud_account_id
	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	if ev.CloudAccountId == nil || *ev.CloudAccountId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event has no account ID"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	classification, err := triage.GetEventClassification(ctx.GetContext(), dbms.Db, eventID, *ev.CloudAccountId)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event classification", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "classification not found"})
		return
	}

	c.JSON(http.StatusOK, classification)
}

// -------------------- Triage Rules Handlers --------------------

// handleEventCreateTriageRule creates a new triage rule
func handleEventCreateTriageRule(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id is required"})
		return
	}

	ruleType, ok := h.Input["rule_type"].(string)
	if !ok || ruleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_type is required"})
		return
	}

	action, ok := h.Input["action"].(string)
	if !ok || action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action is required"})
		return
	}

	req := triage.CreateTriageRuleRequest{
		RuleType: ruleType,
		Action:   action,
	}

	// Optional match criteria
	if v, ok := h.Input["match_source"].(string); ok {
		req.MatchSource = &v
	}
	if v, ok := h.Input["match_alertname"].(string); ok {
		req.MatchAlertname = &v
	}
	if v, ok := h.Input["match_namespace"].(string); ok {
		req.MatchNamespace = &v
	}
	if v, ok := h.Input["match_service"].(string); ok {
		req.MatchService = &v
	}
	if v, ok := h.Input["match_fingerprint"].(string); ok {
		req.MatchFingerprint = &v
	}
	if v, ok := h.Input["match_labels"].(string); ok {
		req.MatchLabels = &v
	}
	if v, ok := h.Input["match_priority"].(string); ok {
		req.MatchPriority = &v
	}
	if v, ok := h.Input["match_finding_type"].(string); ok {
		req.MatchFindingType = &v
	}
	if v, ok := h.Input["action_value"].(string); ok {
		req.ActionValue = &v
	}
	if v, ok := h.Input["priority"].(float64); ok {
		p := int(v)
		req.Priority = &p
	}
	if v, ok := h.Input["effective_until"].(string); ok {
		req.EffectiveUntil = &v
	}
	if v, ok := h.Input["name"].(string); ok {
		req.Name = &v
	}
	if v, ok := h.Input["description"].(string); ok {
		req.Description = &v
	}
	if v, ok := h.Input["apply_to_existing"].(bool); ok {
		req.ApplyToExisting = v
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	rule, err := triage.CreateTriageRule(ctx.GetContext(), dbms.Db, req, cloudAccountID, tenantID, userID)
	if err != nil {
		ctx.GetLogger().Error("Failed to create triage rule", "error", err)
		errStr := err.Error()
		c.JSON(http.StatusInternalServerError, triage.CreateTriageRuleResponse{
			Success: false,
			Error:   &errStr,
		})
		return
	}

	// Apply to existing events if requested
	var eventsUpdated int
	if req.ApplyToExisting {
		eventsUpdated, err = triage.ApplyRuleToExistingEvents(ctx.GetContext(), dbms.Db, rule, cloudAccountID, userID)
		if err != nil {
			ctx.GetLogger().Error("Failed to apply rule to existing events", "error", err, "rule_id", rule.ID)
			// Don't fail the request, rule was created successfully
		}
	}

	response := triage.CreateTriageRuleResponse{
		Success: true,
		Rule:    rule,
	}
	if eventsUpdated > 0 {
		response.BulkOperation = &triage.BulkOperationResponse{
			EventsToUpdate: eventsUpdated,
			Status:         "completed",
		}
	}

	c.JSON(http.StatusOK, response)

	// Publish audit event
	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      tenantID,
		UserId:        userID,
		AccountId:     cloudAccountID,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageRuleCreate,
		EventState:    req,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "triage_rule",
		EventAction:   audit.EventActionCreate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"rule_id": rule.ID, "rule_type": req.RuleType},
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}
}

// handleEventGetTriageRules returns triage rules for the account (or all accounts if cloud_account_id is empty)
func handleEventGetTriageRules(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	// cloud_account_id is optional - if not provided, returns rules for all accounts in tenant
	cloudAccountID, _ := h.Input["cloud_account_id"].(string)

	req := triage.GetTriageRulesRequest{}

	if ruleType, ok := h.Input["rule_type"].(string); ok && ruleType != "" {
		req.RuleType = &ruleType
	}
	if enabled, ok := h.Input["enabled"].(bool); ok {
		req.Enabled = &enabled
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	rules, err := triage.GetTriageRules(ctx.GetContext(), dbms.Db, req, cloudAccountID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get triage rules", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get triage rules: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, triage.GetTriageRulesResponse{
		Rules: rules,
	})
}

// handleEventPreviewTriageRule previews how many existing events would match a rule's criteria
func handleEventPreviewTriageRule(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id is required"})
		return
	}

	ruleType, ok := h.Input["rule_type"].(string)
	if !ok || ruleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_type is required"})
		return
	}

	action, ok := h.Input["action"].(string)
	if !ok || action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action is required"})
		return
	}

	req := triage.RulePreviewRequest{
		RuleType: ruleType,
		Action:   action,
	}

	// Optional match criteria
	if v, ok := h.Input["match_source"].(string); ok {
		req.MatchSource = &v
	}
	if v, ok := h.Input["match_alertname"].(string); ok {
		req.MatchAlertname = &v
	}
	if v, ok := h.Input["match_namespace"].(string); ok {
		req.MatchNamespace = &v
	}
	if v, ok := h.Input["match_service"].(string); ok {
		req.MatchService = &v
	}
	if v, ok := h.Input["match_fingerprint"].(string); ok {
		req.MatchFingerprint = &v
	}
	if v, ok := h.Input["match_labels"].(string); ok {
		req.MatchLabels = &v
	}
	if v, ok := h.Input["match_priority"].(string); ok {
		req.MatchPriority = &v
	}
	if v, ok := h.Input["match_finding_type"].(string); ok {
		req.MatchFindingType = &v
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	preview, err := triage.PreviewTriageRule(ctx.GetContext(), dbms.Db, req, cloudAccountID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to preview triage rule", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to preview rule: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, preview)
}

// handleEventDeleteTriageRule deletes or disables a triage rule
func handleEventDeleteTriageRule(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id is required"})
		return
	}

	ruleID, ok := h.Input["rule_id"].(string)
	if !ok || ruleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id is required"})
		return
	}

	hardDelete := false
	if v, ok := h.Input["hard_delete"].(bool); ok {
		hardDelete = v
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	err = triage.DeleteTriageRule(ctx.GetContext(), dbms.Db, ruleID, hardDelete, cloudAccountID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to delete triage rule", "error", err, "rule_id", ruleID)
		errStr := err.Error()
		c.JSON(http.StatusInternalServerError, triage.DeleteTriageRuleResponse{
			Success: false,
			Error:   &errStr,
		})
		return
	}

	c.JSON(http.StatusOK, triage.DeleteTriageRuleResponse{
		Success: true,
	})

	// Publish audit event
	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      tenantID,
		UserId:        userID,
		AccountId:     cloudAccountID,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageRuleDelete,
		EventState:    map[string]any{"rule_id": ruleID, "hard_delete": hardDelete},
		EventActor:    audit.EventActorApiService,
		EventTarget:   "triage_rule",
		EventAction:   audit.EventActionDelete,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"rule_id": ruleID},
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}
}

// handleEventUpdateTriageRule updates an existing triage rule
func handleEventUpdateTriageRule(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id is required"})
		return
	}

	ruleID, ok := h.Input["rule_id"].(string)
	if !ok || ruleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id is required"})
		return
	}

	ruleType, ok := h.Input["rule_type"].(string)
	if !ok || ruleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_type is required"})
		return
	}

	action, ok := h.Input["action"].(string)
	if !ok || action == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action is required"})
		return
	}

	req := triage.UpdateTriageRuleRequest{
		RuleID:   ruleID,
		RuleType: ruleType,
		Action:   action,
	}

	// Optional match criteria
	if v, ok := h.Input["match_source"].(string); ok {
		req.MatchSource = &v
	}
	if v, ok := h.Input["match_alertname"].(string); ok {
		req.MatchAlertname = &v
	}
	if v, ok := h.Input["match_namespace"].(string); ok {
		req.MatchNamespace = &v
	}
	if v, ok := h.Input["match_service"].(string); ok {
		req.MatchService = &v
	}
	if v, ok := h.Input["match_fingerprint"].(string); ok {
		req.MatchFingerprint = &v
	}
	if v, ok := h.Input["match_labels"].(string); ok {
		req.MatchLabels = &v
	}
	if v, ok := h.Input["match_priority"].(string); ok {
		req.MatchPriority = &v
	}
	if v, ok := h.Input["match_finding_type"].(string); ok {
		req.MatchFindingType = &v
	}
	if v, ok := h.Input["action_value"].(string); ok {
		req.ActionValue = &v
	}
	if v, ok := h.Input["priority"].(float64); ok {
		p := int(v)
		req.Priority = &p
	}
	if v, ok := h.Input["effective_until"].(string); ok {
		req.EffectiveUntil = &v
	}
	if v, ok := h.Input["name"].(string); ok {
		req.Name = &v
	}
	if v, ok := h.Input["description"].(string); ok {
		req.Description = &v
	}
	if v, ok := h.Input["apply_to_existing"].(bool); ok {
		req.ApplyToExisting = v
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	rule, err := triage.UpdateTriageRule(ctx.GetContext(), dbms.Db, req, cloudAccountID, tenantID, userID)
	if err != nil {
		ctx.GetLogger().Error("Failed to update triage rule", "error", err)
		errStr := err.Error()
		c.JSON(http.StatusInternalServerError, triage.UpdateTriageRuleResponse{
			Success: false,
			Error:   &errStr,
		})
		return
	}

	// Apply to existing events if requested
	var eventsUpdated int
	if req.ApplyToExisting {
		eventsUpdated, err = triage.ApplyRuleToExistingEvents(ctx.GetContext(), dbms.Db, rule, cloudAccountID, userID)
		if err != nil {
			ctx.GetLogger().Error("Failed to apply rule to existing events", "error", err, "rule_id", rule.ID)
			// Don't fail the request, rule was updated successfully
		}
	}

	response := triage.UpdateTriageRuleResponse{
		Success: true,
		Rule:    rule,
	}
	if eventsUpdated > 0 {
		response.BulkOperation = &triage.BulkOperationResponse{
			EventsToUpdate: eventsUpdated,
			Status:         "completed",
		}
	}

	c.JSON(http.StatusOK, response)

	// Publish audit event
	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      tenantID,
		UserId:        userID,
		AccountId:     cloudAccountID,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageRuleUpdate,
		EventState:    req,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "triage_rule",
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"rule_id": rule.ID, "rule_type": req.RuleType},
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}
}

// handleEventUpdateNBStatus updates the nb_status of an event
func handleEventUpdateNBStatus(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}

	nbStatus, ok := h.Input["nb_status"].(string)
	if !ok || nbStatus == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nb_status is required"})
		return
	}

	req := triage.UpdateNBStatusRequest{
		EventID:  eventID,
		NBStatus: nbStatus,
	}

	// Parse snoozed_until if provided
	if snoozedUntilStr, ok := h.Input["snoozed_until"].(string); ok && snoozedUntilStr != "" {
		t, err := parseTime(snoozedUntilStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid snoozed_until format: " + err.Error()})
			return
		}
		req.SnoozedUntil = &t
	}

	// Get the event to extract cloud_account_id
	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	if ev.CloudAccountId == nil || *ev.CloudAccountId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event has no account ID"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	response, err := triage.UpdateNBStatus(ctx.GetContext(), dbms.Db, req, *ev.CloudAccountId, userID)
	if err != nil {
		ctx.GetLogger().Error("Failed to update nb_status", "error", err, "event_id", eventID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update status: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)

	// Publish audit event
	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      tenantID,
		UserId:        userID,
		AccountId:     *ev.CloudAccountId,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageStatusUpdate,
		EventState:    req,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "event_status",
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"event_id": eventID, "nb_status": nbStatus},
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}
}

// handleEventToggleSystemRuleOverride enables or disables a system rule for a specific account
func handleEventToggleSystemRuleOverride(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	cloudAccountID, ok := h.Input["cloud_account_id"].(string)
	if !ok || cloudAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cloud_account_id is required"})
		return
	}

	systemRuleID, ok := h.Input["system_rule_id"].(string)
	if !ok || systemRuleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "system_rule_id is required"})
		return
	}

	disabled, ok := h.Input["disabled"].(bool)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "disabled is required"})
		return
	}

	req := triage.ToggleSystemRuleOverrideRequest{
		SystemRuleID: systemRuleID,
		Disabled:     disabled,
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := ctx.GetSecurityContext().GetUserId()

	response, err := triage.ToggleSystemRuleOverride(ctx.GetContext(), dbms.Db, req, cloudAccountID, tenantID)
	if err != nil {
		ctx.GetLogger().Error("Failed to toggle system rule override", "error", err, "rule_id", systemRuleID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to toggle override: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)

	// Publish audit event
	if err := audit.PublishAuditEvent(ctx, audit.Audit{
		TenantId:      tenantID,
		UserId:        userID,
		AccountId:     cloudAccountID,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageRuleUpdate,
		EventState:    req,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "system_rule_override",
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"system_rule_id": systemRuleID, "disabled": disabled},
	}); err != nil {
		ctx.GetLogger().Error("failed to publish audit event", "error", err)
	}
}

// handleEventGetThresholdSuggestion analyzes an alert and suggests a threshold adjustment.
// Checks the pre-computed cache first; falls back to live computation on cache miss.
func handleEventGetThresholdSuggestion(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}

	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}

	// Validate tenant ownership
	tenantID := ctx.GetSecurityContext().GetTenantId()
	if ev.Tenant != nil && *ev.Tenant != tenantID {
		c.JSON(http.StatusForbidden, gin.H{"error": "event does not belong to this tenant"})
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	// Try cached suggestion first (lookup by alert rule key)
	if ev.CloudAccountId != nil {
		source := ""
		if ev.Source != nil {
			source = *ev.Source
		}
		labels := triage.GetLabelsMap(ev)
		alertRuleKey := triage.ExtractAlertRuleKey(source, labels)
		if alertRuleKey != "" {
			cached, cacheErr := triage.GetCachedSuggestion(ctx.GetContext(), dbms.Db, alertRuleKey, *ev.CloudAccountId)
			if cacheErr == nil && cached != nil {
				c.JSON(http.StatusOK, cached)
				return
			}

			// Check for a skipped/error cached row — surface it so the card shows with a reason
			if skippedSource, skippedReason, err := triage.GetCachedSkippedReason(ctx.GetContext(), dbms.Db, alertRuleKey, *ev.CloudAccountId); err == nil {
				c.JSON(http.StatusOK, triage.ThresholdSuggestionResponse{
					Available: true,
					Source:    skippedSource,
					AlertDefinition: &triage.AlertDefinition{
						AlarmName: alertRuleKey,
					},
					Suggestion: &triage.ThresholdSuggestion{
						RecommendationType: "not_eligible",
						Reason:             skippedReason,
					},
				})
				return
			}
		}
	}

	// Fallback: compute live
	response := triage.GetThresholdSuggestion(ctx.GetContext(), dbms.Db, ev, tenantID)

	// Async: store in cache for next time
	if response.Available && response.Suggestion != nil && response.Suggestion.EstimatedReduction > 0 {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ctx.GetLogger().Error("panic in UpsertSuggestionCache", "panic", r)
				}
			}()
			triage.UpsertSuggestionCache(context.Background(), dbms.Db, ev, tenantID, response)
		}()
	}

	c.JSON(http.StatusOK, response)
}

// handleEventListThresholdSuggestions lists all cached threshold suggestions for a tenant.
func handleEventListThresholdSuggestions(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	var tenantID string
	if sc := ctx.GetSecurityContext(); sc != nil {
		tenantID = sc.GetTenantId()
	}
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id is required"})
		return
	}

	cloudAccountID, _ := h.Input["cloud_account_id"].(string)
	source, _ := h.Input["source"].(string)
	confidence, _ := h.Input["confidence"].(string)

	limit := 20
	if l, ok := h.Input["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	offset := 0
	if o, ok := h.Input["offset"].(float64); ok && o >= 0 {
		offset = int(o)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection error"})
		return
	}

	result, err := triage.ListThresholdSuggestions(c.Request.Context(), dbms.Db, tenantID, cloudAccountID, source, confidence, limit, offset)
	if err != nil {
		ctx.GetLogger().Error("failed to list threshold suggestions", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list threshold suggestions"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleEventGetTriageRuleEvents returns events that were classified by a specific triage rule
func handleEventGetTriageRuleEvents(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	ruleID, ok := h.Input["rule_id"].(string)
	if !ok || ruleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id is required"})
		return
	}

	// Optional account_id filter (for system rules viewed in account context)
	var accountID *string
	if v, ok := h.Input["account_id"].(string); ok && v != "" {
		accountID = &v
	}

	// Optional pagination
	limit := 20
	offset := 0
	if v, ok := h.Input["limit"].(float64); ok {
		limit = int(v)
	}
	if v, ok := h.Input["offset"].(float64); ok {
		offset = int(v)
	}

	// Optional time range
	var startDate, endDate *time.Time
	if v, ok := h.Input["start_date"].(string); ok && v != "" {
		t, err := parseTime(v)
		if err == nil {
			startDate = &t
		}
	}
	if v, ok := h.Input["end_date"].(string); ok && v != "" {
		t, err := parseTime(v)
		if err == nil {
			endDate = &t
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database connection failed"})
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	events, total, err := triage.GetEventsForTriageRule(ctx.GetContext(), dbms.Db, ruleID, tenantID, accountID, limit, offset, startDate, endDate)
	if err != nil {
		ctx.GetLogger().Error("Failed to get events for triage rule", "error", err, "rule_id", ruleID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get events: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleEventGetRecurrenceInfo returns recurrence info for an event from event_duplicates
func handleEventGetRecurrenceInfo(h *HasuraActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(http.StatusBadRequest, common.ErrorHasuraActionBadRequest("event_id is required"))
		return
	}

	resp, err := triage.GetRecurrenceInfo(ctx, triage.RecurrenceInfoRequest{EventID: eventID})
	if err != nil {
		ctx.GetLogger().Error("Failed to get recurrence info", "error", err, "event_id", eventID)
		c.JSON(http.StatusInternalServerError, common.ErrorHasuraActionBadRequest("internal server error"))
		return
	}

	c.JSON(http.StatusOK, resp)
}
