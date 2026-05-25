package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	_ "nudgebee/llm/agents/signoz"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// eventAnalysisWorkerPool handles asynchronous event analysis tasks
var eventAnalysisWorkerPool *common.WorkerPool
var eventAnalysisWorkerPoolOnce sync.Once

// maxAnnotationRCAFormatBytes caps the length of an `rca_format` value pulled
// from an alert annotation (#29896). A misconfigured rule could otherwise
// inject an arbitrarily large blob into the RCA prompt, blowing token cost
// and pushing other context out of the window. Account-level `rca_format`
// (set via the admin API) is unaffected.
const maxAnnotationRCAFormatBytes = 4096

func getEventAnalysisWorkerPool() *common.WorkerPool {
	eventAnalysisWorkerPoolOnce.Do(func() {
		eventAnalysisWorkerPool = common.NewWorkerPool("event_analysis", config.Config.EventAnalysisWorkerCount, config.Config.EventAnalysisQueueSize)
	})
	return eventAnalysisWorkerPool
}

func init() {
	getEventAnalysisWorkerPool()
}

type EventAnalysisRequest struct {
	EventId         string `json:"event_id" mapstructure:"required" validate:"required"`
	AccountId       string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId          string `json:"user_id"`
	Regenerate      bool   `json:"regenerate"`
	UpdateEvidences bool   `json:"update_evidences"`
	Source          string `json:"source"`
	// TaskToken, when non-empty, identifies a Temporal workflow activity
	// (in runbook-server) that is suspended waiting for this investigation
	// to finish. After the analysis pipeline reaches a terminal state,
	// processTroubleshootingEventFromMq publishes a completion envelope
	// carrying this token so runbook-server can resume the activity via
	// CompleteActivity. Empty when the request did not originate from a
	// workflow.
	TaskToken string `json:"task_token,omitempty"`
}

type EventRCAAnalysisRequest struct {
	EventId    string `json:"event_id" mapstructure:"required" validate:"required"`
	AccountId  string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId     string `json:"user_id"`
	Regenerate bool   `json:"regenerate"`
	Generate   bool   `json:"generate"`
}

type GetRCAFormatRequest struct {
	AccountId string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId    string `json:"user_id"`
}

type SetRCAFormatRequest struct {
	AccountId string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId    string `json:"user_id"`
	Format    string `json:"format"`
}

type RCAFormatResponse struct {
	Format    string `json:"format"`
	IsDefault bool   `json:"is_default"`
}

type EventAnalysisResponse struct {
	EventFingerprint    string `json:"event_fingerprint"`
	RelatedEventId      string `json:"related_event_id"`
	EventId             string `json:"event_id"`
	EventAggregationKey string `json:"event_aggregation_key"`
	Analysis            string `json:"analysis"`
	Summary             string `json:"summary"`
	Investigation       string `json:"investigation"`
	Status              string `json:"status"`
	// StatusReason carries the failure detail for a FAILED analysis. Empty
	// for non-failed states. UI uses this to render an inline reason next to
	// the "Failed" badge instead of leaving users guessing why a run failed.
	StatusReason        string              `json:"status_reason,omitempty"`
	TaskStatuses        map[string]string   `json:"task_statuses,omitempty"`
	CodeAnalysisEnabled bool                `json:"code_analysis_enabled"`
	RcaEnabled          bool                `json:"rca_enabled"`
	FileDetails         EventLogFileDetails `json:"file_details"`
	SourceDetails       map[string]any      `json:"source_details"`
	SourceUpdates       map[string]any      `json:"source_updates"`

	// Additional fields for code analysis
	Title          string            `json:"title,omitempty"`
	Description    string            `json:"description,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	OriginalCode   string            `json:"original_code,omitempty"`
	FixedCode      string            `json:"fixed_code,omitempty"`
	GitDiff        string            `json:"git_diff,omitempty"`
	CommitHash     string            `json:"commit_hash,omitempty"`
	Author         string            `json:"author,omitempty"`
	CommitDate     string            `json:"commit_date,omitempty"`
	PRList         []PullRequestInfo `json:"pr_list,omitempty"`
	Commits        []CommitInfo      `json:"commits,omitempty"`
	AutomatedFixPR PullRequestInfo   `json:"automated_fix_pr,omitempty"`

	// DetailedResponse is an enriched markdown summary combining summary + investigation + log analysis.
	// Falls back to Summary when empty.
	DetailedResponse string `json:"detailed_response,omitempty"`

	// Pipeline status fields — expose review/build/fix details
	RootCauseAnalysis  string         `json:"root_cause_analysis,omitempty"`
	ConfidenceScore    string         `json:"confidence_score,omitempty"`
	ExecutionStatus    string         `json:"execution_status,omitempty"`
	ExecutionSummary   string         `json:"execution_summary,omitempty"`
	FilesModified      any            `json:"files_modified,omitempty"`
	VerificationPassed any            `json:"verification_passed,omitempty"`
	PRCreationStatus   string         `json:"pr_creation_status,omitempty"`
	PRCreationReason   string         `json:"pr_creation_reason,omitempty"`
	Review             map[string]any `json:"review,omitempty"`
	BuildVerification  map[string]any `json:"build_verification,omitempty"`
	FailureSummary     string         `json:"failure_summary,omitempty"`
}

type PullRequestInfo struct {
	Number    int    `json:"number,omitempty"`
	Title     string `json:"title,omitempty"`
	Author    string `json:"author,omitempty"`
	URL       string `json:"url,omitempty"`
	State     string `json:"state,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	MergedAt  string `json:"merged_at,omitempty"`
}

type CommitInfo struct {
	Hash    string `json:"hash,omitempty"`
	Author  string `json:"author,omitempty"`
	Date    string `json:"date,omitempty"`
	Message string `json:"message,omitempty"`
	Changes string `json:"changes,omitempty"`
}

type EventLogFileDetail struct {
	FilePath   string `json:"file_path,omitempty"`
	Filename   string `json:"file_name,omitempty"`
	LineNumber int    `json:"line_number,omitempty"`
}
type EventLogFileDetails struct {
	Files []EventLogFileDetail `json:"files,omitempty"`
}

// CleanupEventAnalysisResources stops the event analysis worker pool gracefully
func CleanupEventAnalysisResources() {
	if eventAnalysisWorkerPool != nil {
		eventAnalysisWorkerPool.Stop()
	}
}

func handleAnalysisApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/analyze")

	groupV2.POST("/event/log", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("event_analyzer")
		processEventAnalysis(c, tracer, meter)
	})

	groupV2.POST("/event", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("event_analyzer")
		processEventAnalysis(c, tracer, meter)
	})

	groupV2.POST("/event/rca", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("event_analyzer")
		var request EventRCAAnalysisRequest
		var actionRequest ActionRequest
		err := c.ShouldBindJSON(&actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if v, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = v
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "event_id", request.EventId, "user_id", request.UserId)
		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = context.GetSecurityContext().GetUserId()
		}
		if context.GetSecurityContext().IsSuperAdmin() {
			request.UserId = security.GetSystemUserId()
		}

		// Check if user has access to account
		if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{
				errors.New(errorUserAccessMessage),
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = context.GetSecurityContext().GetUserId()
		}
		executeEventRCAAnalysis(context, request, c)
	})

	groupV2.POST("/event/rca/format_get", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("get_rca_format")
		var request GetRCAFormatRequest
		var actionRequest ActionRequest
		if err := c.ShouldBindJSON(&actionRequest); err != nil {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if v, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = v
		}
		if err := common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		logger := slog.With("account_id", request.AccountId)
		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)
		format, err := eventAnalysisRepo.GetAccountRCAFormat(context, request.AccountId)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		isDefault := false
		if format == "" {
			format = agents.DefaultRCAFormat
			isDefault = true
		}

		c.JSON(200, buildApiResponse(RCAFormatResponse{
			Format:    format,
			IsDefault: isDefault,
		}, nil))
	})

	groupV2.POST("/event/rca/format_save", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("save_rca_format")
		var request SetRCAFormatRequest
		var actionRequest ActionRequest
		if err := c.ShouldBindJSON(&actionRequest); err != nil {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if v, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = v
		}
		if err := common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		logger := slog.With("account_id", request.AccountId)
		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		// Require write access to update format
		if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)
		err = eventAnalysisRepo.SetAccountRCAFormat(context, request.AccountId, request.Format)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}

		responseFormat := request.Format
		isDefault := false
		if responseFormat == "" {
			responseFormat = agents.DefaultRCAFormat
			isDefault = true
		}

		c.JSON(200, buildApiResponse(RCAFormatResponse{
			Format:    responseFormat,
			IsDefault: isDefault,
		}, nil))
	})

}

func processEventAnalysis(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request EventAnalysisRequest
	var actionRequest ActionRequest
	err := c.ShouldBindJSON(&actionRequest)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	actionRequestPayload := actionRequest.Input
	if v, ok := actionRequestPayload["request"].(map[string]any); ok {
		actionRequestPayload = v
	}
	err = common.DecodeMapToStruct(actionRequestPayload, &request)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId, "user_id", request.UserId)
	context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{
				Message: err.Error(),
			},
		}))
		return
	}

	if request.UserId == "" {
		request.UserId = context.GetSecurityContext().GetUserId()
	}
	if context.GetSecurityContext().IsSuperAdmin() {
		request.UserId = security.GetSystemUserId()
	}

	if request.UserId == "" || request.AccountId == "" || request.EventId == "" {
		slog.Error("analyzer: userId, accountId and eventId are required", "payload", slog.AnyValue(actionRequest), "headers", slog.AnyValue(c.Request.Header))
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: "userId, accountId and eventId are required",
			},
		}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{
			errors.New(errorUserAccessMessage),
		}))
		return
	}

	if request.UserId == "" {
		request.UserId = context.GetSecurityContext().GetUserId()
	}
	executeEventInvestigation(context, request, c)
}

func executeEventAnalysis(ctx *security.RequestContext, c *gin.Context, request any, analysisType events.EventAnalysisType, analysisFunc func(ctx *security.RequestContext, request any) (any, error)) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)

	var eventId, accountId, userId string
	var regenerate, generate bool

	switch r := request.(type) {
	case EventRCAAnalysisRequest:
		eventId = r.EventId
		accountId = r.AccountId
		userId = r.UserId
		regenerate = r.Regenerate
		generate = r.Generate
	case EventAnalysisRequest:
		eventId = r.EventId
		accountId = r.AccountId
		userId = r.UserId
		regenerate = r.Regenerate
		generate = true // Always generate for EventAnalysisRequest
	default:
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "invalid request type"}}))
		return
	}

	if userId == "" || accountId == "" || eventId == "" {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "userId, accountId and eventId are required"}}))
		return
	}

	ctx.GetLogger().Info("analyzer: fetching event info from database", "event_id", eventId)
	eventInfo, err := eventAnalysisRepo.GetEventInfo(ctx, eventId, accountId)
	if err != nil {
		if strings.Contains(err.Error(), "event not found") {
			c.JSON(404, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		} else {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		}
		return
	}

	if eventInfo.Fingerprint == "" {
		ctx.GetLogger().Warn("analyzer: event fingerprint is empty, using event_id as fingerprint", "event_id", eventId)
		eventInfo.Fingerprint = eventId
	}

	existingAnalysis, err := eventAnalysisRepo.GetEventAnalysis(ctx, eventInfo.Fingerprint, eventInfo.AggregationKey, accountId, analysisType)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}

	// First-time analyses are system-initiated (auto-triggered on event
	// ingestion), not user-driven. Attribute them to the system user so
	// token-usage, audit, and conversation rows don't get tagged with
	// whichever operator happened to hit the API first. Re-analyses
	// (existingAnalysis != nil) keep the caller's identity.
	if existingAnalysis == nil {
		userId = security.GetSystemUserId()
		switch r := request.(type) {
		case EventRCAAnalysisRequest:
			r.UserId = userId
			request = r
		case EventAnalysisRequest:
			r.UserId = userId
			request = r
		}
	}

	var response EventAnalysisResponse
	response.EventId = eventId
	response.RelatedEventId = eventId
	response.EventFingerprint = eventInfo.Fingerprint
	response.EventAggregationKey = eventInfo.AggregationKey
	if existingAnalysis != nil {
		if existingAnalysis.RelatedEventId != "" {
			response.RelatedEventId = existingAnalysis.RelatedEventId
		}
		response.Analysis = existingAnalysis.Analysis
		response.Status = existingAnalysis.Status
		response.Summary = existingAnalysis.Summary
		response.StatusReason = existingAnalysis.StatusReason
		if response.Analysis != "" {
			response2 := EventAnalysisResponse{}
			err = json.Unmarshal([]byte(response.Analysis), &response2)
			if err == nil {
				response.Summary = response2.Summary
				response.Analysis = response2.Analysis
				response.FileDetails = response2.FileDetails
				response.SourceDetails = response2.SourceDetails
				response.SourceUpdates = response2.SourceUpdates
				response.Title = response2.Title
				response.Description = response2.Description
				response.ErrorMessage = response2.ErrorMessage
				response.OriginalCode = response2.OriginalCode
				response.FixedCode = response2.FixedCode
				response.GitDiff = response2.GitDiff
				response.CommitHash = response2.CommitHash
				response.Author = response2.Author
				response.CommitDate = response2.CommitDate
				response.PRList = response2.PRList
				response.Commits = response2.Commits
				response.AutomatedFixPR = response2.AutomatedFixPR
			}
		}
	}

	if !generate {
		c.JSON(200, buildApiResponse(response, nil))
		return
	}

	if strings.EqualFold(response.Status, string(events.AnalysisStatusInProgress)) {
		c.JSON(200, buildApiResponse(response, nil))
		return
	}

	if strings.EqualFold(response.Status, string(events.AnalysisStatusCompleted)) && !regenerate {
		if response.Status == "" {
			response.Status = string(events.AnalysisStatusCompleted)
		}
		c.JSON(200, buildApiResponse(response, nil))
		return
	}

	if (strings.EqualFold(response.Status, "FAILED") && response.Analysis != "" && response.Summary != "") && !regenerate {
		c.JSON(200, buildApiResponse(response, nil))
		return
	}

	// Check budget limits ONLY when generating new analysis (not when retrieving existing)
	if budget.CheckBudgetAndRespond(c, ctx.GetSecurityContext().GetTenantId(), accountId, budget.ModuleInvestigation, ctx.GetLogger()) {
		return
	}

	err = eventAnalysisRepo.UpsertEventAnalysisInProgress(ctx, eventId, eventInfo.Fingerprint, accountId, eventInfo.AggregationKey, analysisType)
	if err != nil {
		c.JSON(500, buildApiResponse(response, []error{errors.New("analyzer: unable to process request")}))
		return
	}

	// Use the worker pool instead of spawning a new goroutine for each request
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncApiTimeoutSeconds)*time.Second)
	defer cancel()
	analysisTypeStr := string(analysisType)
	common.MetricsEventAnalysisOperationsTotal(analysisTypeStr, "queued", accountId)
	analysisStart := time.Now()
	err = eventAnalysisWorkerPool.Submit(submissionCtx, func() {
		newCtx := security.NewRequestContext(context.Background(), ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		_, err := analysisFunc(newCtx, request)
		if err != nil {
			newCtx.GetLogger().Error("unable to process analysis", "error", err, "event_id", eventId)
			common.MetricsEventAnalysisOperationsTotal(analysisTypeStr, "fail", accountId)
		} else {
			newCtx.GetLogger().Info("analysis completed successfully", "event_id", eventId)
			common.MetricsEventAnalysisOperationsTotal(analysisTypeStr, "success", accountId)
		}
		common.MetricsEventAnalysisLatencySeconds(analysisTypeStr, accountId, time.Since(analysisStart).Seconds())
	})
	if err != nil {
		common.MetricsApiRequestsFailedTotal("event_analyzer", "timedout")
		common.MetricsEventAnalysisOperationsTotal(analysisTypeStr, "queue_full", accountId)
		// Reset status so the next request can retry instead of getting stuck as IN_PROGRESS
		if statusErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventInfo.Fingerprint, accountId, eventInfo.AggregationKey, string(events.AnalysisStatusFailed), "queue full, please retry", analysisType); statusErr != nil {
			ctx.GetLogger().Warn("failed to update event analysis status on queue timeout", "error", statusErr, "analysis_type", analysisType)
		}
		c.JSON(503, buildApiResponse(response, []error{common.Error{Message: "analyzer: unable to queue analysis request, please try again later"}}))
		return
	}

	response.Analysis = ""
	response.RelatedEventId = eventId
	response.Status = string(events.AnalysisStatusInProgress)

	c.JSON(200, buildApiResponse(response, nil))
}

func executeEventRCAAnalysis(ctx *security.RequestContext, request EventRCAAnalysisRequest, c *gin.Context) {
	executeEventAnalysis(ctx, c, request, events.AnalysisTypeRCA, func(ctx *security.RequestContext, request any) (any, error) {
		return analyzeEventRCAUsingAgentsAndUpdateDb(ctx, request.(EventRCAAnalysisRequest))
	})
}

func executeEventInvestigation(ctx *security.RequestContext, request EventAnalysisRequest, c *gin.Context) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)

	if request.UserId == "" || request.AccountId == "" || request.EventId == "" {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "userId, accountId and eventId are required"}}))
		return
	}

	ctx.GetLogger().Info("analyzer: fetching event info from database", "event_id", request.EventId)
	eventInfo, err := eventAnalysisRepo.GetEventInfo(ctx, request.EventId, request.AccountId)
	if err != nil {
		if strings.Contains(err.Error(), "event not found") {
			c.JSON(404, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		} else {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		}
		return
	}

	if eventInfo.Fingerprint == "" {
		ctx.GetLogger().Warn("analyzer: event fingerprint is empty, using event_id as fingerprint", "event_id", request.EventId)
		eventInfo.Fingerprint = request.EventId
	}

	analysisTypes := []events.EventAnalysisType{events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeLog, events.AnalysisTypeDetailedResponse}
	var finalResponse EventAnalysisResponse
	finalResponse.EventId = request.EventId
	finalResponse.RelatedEventId = request.EventId
	finalResponse.EventFingerprint = eventInfo.Fingerprint
	finalResponse.EventAggregationKey = eventInfo.AggregationKey
	finalResponse.TaskStatuses = make(map[string]string)

	// Feature flags for frontend - debug analysis enabled by default, unless explicitly disabled
	codeAnalysisDisabled, _ := common.IsFeatureEnabledForAccount("EVENT_DEBUG_ANALYSIS_DISABLED", ctx.GetSecurityContext().GetTenantId(), request.AccountId)
	finalResponse.CodeAnalysisEnabled = !codeAnalysisDisabled
	finalResponse.RcaEnabled = true // Assuming RCA is generally enabled

	dbAnalyses := make(map[events.EventAnalysisType]*events.EventAnalysis)
	for _, aType := range analysisTypes {
		existingAnalysis, err := eventAnalysisRepo.GetEventAnalysis(ctx, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId, aType)
		if err == nil && existingAnalysis != nil {
			dbAnalyses[aType] = existingAnalysis
		}
	}

	// Backwards compatibility for legacy single-row data
	if dbAnalyses[events.AnalysisTypeLog] != nil && dbAnalyses[events.AnalysisTypeSummary] == nil && dbAnalyses[events.AnalysisTypeInvestigation] == nil {
		legacyStatus := dbAnalyses[events.AnalysisTypeLog].Status
		dbAnalyses[events.AnalysisTypeSummary] = &events.EventAnalysis{
			Status:  legacyStatus,
			Summary: dbAnalyses[events.AnalysisTypeLog].Summary,
		}
		dbAnalyses[events.AnalysisTypeInvestigation] = &events.EventAnalysis{
			Status:  legacyStatus,
			Summary: "", // Leave empty for legacy data to prevent duplicating the summary content
		}
		// DetailedResponse is not available for legacy data; mark it completed with empty content so
		// allCompleted fires correctly. Frontend falls back to Summary when DetailedResponse is empty.
		dbAnalyses[events.AnalysisTypeDetailedResponse] = &events.EventAnalysis{
			Status:  legacyStatus,
			Summary: "",
		}
	}

	allCompleted := true
	anyFailed := false
	anyInProgress := false
	anyStarted := len(dbAnalyses) > 0

	// First-time analyses are system-initiated (auto-triggered on event
	// ingestion), not user-driven. Attribute them to the system user so
	// token-usage, audit, and conversation rows don't get tagged with
	// whichever operator happened to hit the API first. Re-analyses
	// (anyStarted == true) keep the caller's identity.
	if !anyStarted {
		request.UserId = security.GetSystemUserId()
	}

	for _, aType := range analysisTypes {
		existingAnalysis := dbAnalyses[aType]
		if existingAnalysis != nil {
			if existingAnalysis.RelatedEventId != "" {
				finalResponse.RelatedEventId = existingAnalysis.RelatedEventId
			}
			finalResponse.TaskStatuses[string(aType)] = existingAnalysis.Status
			if existingAnalysis.Status != string(events.AnalysisStatusCompleted) {
				allCompleted = false
			}
			if existingAnalysis.Status == string(events.AnalysisStatusFailed) {
				anyFailed = true
				// Surface the first non-empty failure reason to the UI. If
				// multiple analysis types fail with different reasons, the
				// first one wins — operators can dig into per-task statuses
				// for the rest.
				if finalResponse.StatusReason == "" && existingAnalysis.StatusReason != "" {
					finalResponse.StatusReason = existingAnalysis.StatusReason
				}
			}
			if existingAnalysis.Status == string(events.AnalysisStatusInProgress) {
				anyInProgress = true
			}

			// Aggregate data
			if aType == events.AnalysisTypeSummary && existingAnalysis.Summary != "" {
				finalResponse.Summary = existingAnalysis.Summary
			}
			if aType == events.AnalysisTypeInvestigation && existingAnalysis.Summary != "" {
				finalResponse.Investigation = existingAnalysis.Summary
			}
			if aType == events.AnalysisTypeDetailedResponse && existingAnalysis.Summary != "" {
				finalResponse.DetailedResponse = existingAnalysis.Summary
			}
			if aType == events.AnalysisTypeLog && existingAnalysis.Analysis != "" {
				finalResponse.Analysis = existingAnalysis.Analysis
				response2 := EventAnalysisResponse{}
				err = json.Unmarshal([]byte(existingAnalysis.Analysis), &response2)
				if err == nil {
					finalResponse.FileDetails = response2.FileDetails
					finalResponse.SourceDetails = response2.SourceDetails
					finalResponse.SourceUpdates = response2.SourceUpdates
					finalResponse.Title = response2.Title
					finalResponse.Description = response2.Description
					finalResponse.ErrorMessage = response2.ErrorMessage
					finalResponse.OriginalCode = response2.OriginalCode
					finalResponse.FixedCode = response2.FixedCode
					finalResponse.GitDiff = response2.GitDiff
					finalResponse.CommitHash = response2.CommitHash
					finalResponse.Author = response2.Author
					finalResponse.CommitDate = response2.CommitDate
					finalResponse.PRList = response2.PRList
					finalResponse.Commits = response2.Commits
					finalResponse.AutomatedFixPR = response2.AutomatedFixPR
				} else {
					ctx.GetLogger().Warn("failed to unmarshal existing log analysis", "error", err)
				}
			}
		} else {
			allCompleted = false
		}
	}

	if anyStarted && !request.Regenerate {
		if allCompleted {
			finalResponse.Status = string(events.AnalysisStatusCompleted)
		} else if anyInProgress {
			finalResponse.Status = string(events.AnalysisStatusInProgress)
		} else if anyFailed {
			finalResponse.Status = string(events.AnalysisStatusFailed)
		} else {
			finalResponse.Status = string(events.AnalysisStatusInProgress)
		}
		c.JSON(200, buildApiResponse(finalResponse, nil))
		return
	}

	// Check budget limits
	if budget.CheckBudgetAndRespond(c, ctx.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleInvestigation, ctx.GetLogger()) {
		return
	}

	// Mark all as in progress
	for _, aType := range analysisTypes {
		if err := eventAnalysisRepo.UpsertEventAnalysisInProgress(ctx, request.EventId, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, aType); err != nil {
			ctx.GetLogger().Warn("failed to upsert event analysis in progress", "error", err, "analysis_type", aType)
		}
		finalResponse.TaskStatuses[string(aType)] = string(events.AnalysisStatusInProgress)
	}

	finalResponse.Status = string(events.AnalysisStatusInProgress)

	common.MetricsEventAnalysisOperationsTotal("investigation", "queued", request.AccountId)
	investigationStart := time.Now()
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncApiTimeoutSeconds)*time.Second)
	defer cancel()
	err = eventAnalysisWorkerPool.Submit(submissionCtx, func() {
		// Use a bounded context to prevent worker goroutines from running indefinitely
		// (e.g., code analysis poll loops stuck on a missing workspace pod).
		execCtx, execCancel := context.WithTimeout(context.Background(), 35*time.Minute)
		defer execCancel()
		newCtx := security.NewRequestContext(execCtx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		_, err := analyzeEventUsingAgentsAndUpdateDb(newCtx, request)
		if err != nil {
			newCtx.GetLogger().Error("unable to process analysis", "error", err, "event_id", request.EventId)
			common.MetricsEventAnalysisOperationsTotal("investigation", "fail", request.AccountId)
			// Mark all analysis types as FAILED to prevent stuck IN_PROGRESS state
			if newDbManager, dbErr := common.GetDatabaseManager(common.Metastore); dbErr == nil {
				markAllAnalysisFailed(newCtx, events.NewEventAnalysisRepository(newDbManager), eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, err.Error())
			}
		} else {
			newCtx.GetLogger().Info("analysis completed successfully", "event_id", request.EventId)
			common.MetricsEventAnalysisOperationsTotal("investigation", "success", request.AccountId)
		}
		common.MetricsEventAnalysisLatencySeconds("investigation", request.AccountId, time.Since(investigationStart).Seconds())
	})
	if err != nil {
		common.MetricsApiRequestsFailedTotal("event_analyzer", "timedout")
		common.MetricsEventAnalysisOperationsTotal("investigation", "queue_full", request.AccountId)
		// Mark tasks as FAILED if submission fails to prevent stuck IN_PROGRESS state
		for _, aType := range analysisTypes {
			if statusErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, string(events.AnalysisStatusFailed), "unable to queue analysis - "+err.Error(), aType); statusErr != nil {
				ctx.GetLogger().Warn("failed to update event analysis status on submission failure", "error", statusErr, "analysis_type", aType)
			}
		}
		c.JSON(503, buildApiResponse(finalResponse, []error{common.Error{Message: "analyzer: unable to queue analysis request, please try again later"}}))
		return
	}

	c.JSON(200, buildApiResponse(finalResponse, nil))
}

func getOrCreateEventAnalysisStatus(ctx *security.RequestContext, request EventAnalysisRequest, dbManager *common.DatabaseManager, createAnalysis bool) (EventAnalysisResponse, error) {
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)
	if request.EventId == "" || request.AccountId == "" {
		ctx.GetLogger().Warn("analyzer: event_id/account_id is required", "event_id", request.EventId, "account_id", request.AccountId)
		return EventAnalysisResponse{}, common.Error{
			Message: "event_id is required",
		}
	}
	ctx.GetLogger().Info("analyzer: fetching existing analysis from database", "event_id", request.EventId)
	eventInfo, err := eventAnalysisRepo.GetEventInfo(ctx, request.EventId, request.AccountId)
	if err != nil {
		return EventAnalysisResponse{}, err
	}

	if eventInfo.Fingerprint == "" {
		ctx.GetLogger().Warn("analyzer: event fingerprint is empty, using event_id as fingerprint", "event_id", request.EventId)
		eventInfo.Fingerprint = request.EventId
	}

	existingAnalysis, err := eventAnalysisRepo.GetEventAnalysis(ctx, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId, events.AnalysisTypeLog)
	if err != nil {
		return EventAnalysisResponse{}, err
	}

	response := EventAnalysisResponse{
		EventId:             request.EventId,
		RelatedEventId:      request.EventId,
		EventFingerprint:    eventInfo.Fingerprint,
		EventAggregationKey: eventInfo.AggregationKey,
	}

	if existingAnalysis != nil {
		response.RelatedEventId = existingAnalysis.RelatedEventId
		response.Analysis = existingAnalysis.Analysis
		response.Status = existingAnalysis.Status
		response.Summary = existingAnalysis.Summary
		response.StatusReason = existingAnalysis.StatusReason
	}

	if strings.EqualFold(response.Status, string(core.ConversationStatusInProgress)) && !request.Regenerate {
		return response, nil
	}

	// Full-pipeline completion check across all four analysis types. The original
	// code checked only log_analysis and gated the early return on
	// response.Analysis != "". CloudWatch alarms complete log_analysis with
	// analysis="" ("skipped - no logs"), so the empty-analysis gate caused this
	// function to fall through and reset log_analysis to IN_PROGRESS on every
	// MQ re-fire — triggering a full redundant pipeline run.
	if !request.Regenerate && allEventAnalysisTypesCompleted(ctx, eventAnalysisRepo, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId) {
		ctx.GetLogger().Debug("analyzer: returning existing completed analysis", "analysis", slog.AnyValue(response.Analysis), "event_id", request.EventId)
		response.Status = string(core.ConversationStatusCompleted)
		if response.Analysis != "" {
			if err = common.UnmarshalJson([]byte(response.Analysis), &response); err != nil {
				ctx.GetLogger().Warn("analyzer: failed to unmarshal analysis from database", "error", err, "event_id", request.EventId)
			}
			if response.Status == "" {
				response.Status = string(core.ConversationStatusCompleted)
			}
		}
		return response, nil
	}

	if (strings.EqualFold(response.Status, string(core.ConversationStatusWaiting)) || strings.EqualFold(response.Status, string(core.ConversationStatusWaitingForClientTool))) && !request.Regenerate {
		ctx.GetLogger().Debug("analyzer: returning existing analysis from database", "analysis", slog.AnyValue(response.Analysis), "status", response.Status, "event_id", request.EventId)
		return response, nil
	}

	if createAnalysis {
		// Only mark log_analysis as IN_PROGRESS when it isn't already COMPLETED.
		// Resetting a COMPLETED row would force Step 3 in
		// analyzeEventUsingAgentsAndUpdateDb to bypass its cache (which gates on
		// existingLog.Status == COMPLETED) and re-run log analysis or re-emit the
		// "skipped - no logs" path, which then unconditionally re-runs Step 4
		// synthesis — the exact duplicate-LLM-call pattern this fix targets.
		//
		// When log_analysis is COMPLETED but other types are not (e.g.
		// detailed_response missing after a Step-4 crash), the per-step caches
		// in analyzeEventUsingAgentsAndUpdateDb skip completed steps and only
		// re-run what's missing.
		if existingAnalysis == nil || existingAnalysis.Status != string(events.AnalysisStatusCompleted) {
			err = eventAnalysisRepo.UpsertEventAnalysisInProgress(ctx, request.EventId, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, events.AnalysisTypeLog)
			if err != nil {
				return EventAnalysisResponse{}, err
			}
		}
		response.Status = string(events.AnalysisStatusCreated)
		response.RelatedEventId = request.EventId
	}
	return response, nil
}

// allEventAnalysisTypesCompleted returns true when every analysis type for an
// event (summary, investigation, log, detailed_response) is in COMPLETED state.
// A DB read error or any non-COMPLETED row is treated as "not completed" — the
// safe fallback that lets the pipeline proceed rather than incorrectly skipping.
// This mirrors the defense-in-depth check inside analyzeEventUsingAgentsAndUpdateDb.
func allEventAnalysisTypesCompleted(ctx *security.RequestContext, repo *events.EventAnalysisRepository, fingerprint, aggKey, accountId string) bool {
	for _, aType := range []events.EventAnalysisType{
		events.AnalysisTypeSummary,
		events.AnalysisTypeInvestigation,
		events.AnalysisTypeLog,
		events.AnalysisTypeDetailedResponse,
	} {
		analysis, err := repo.GetEventAnalysis(ctx, fingerprint, aggKey, accountId, aType)
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to read analysis type for completion check, treating as incomplete", "error", err, "analysis_type", aType, "fingerprint", fingerprint)
			return false
		}
		if analysis == nil || analysis.Status != string(events.AnalysisStatusCompleted) {
			return false
		}
	}
	return true
}

func getAgentResponseFromConversation(ctx *security.RequestContext, sessionId string, accountId string, agentName string) (string, bool) {
	dao := core.GetConversationDao()
	if dao == nil {
		return "", false
	}
	conv, err := dao.GetConversationBySession(accountId, sessionId)
	if err != nil || conv.ID == uuid.Nil {
		return "", false
	}
	messages, err := dao.ListConversationMessages("", "", conv.ID.String(), false)
	if err != nil {
		return "", false
	}
	// Search backwards for the latest COMPLETED response from this agent
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].AgentName != nil && *messages[i].AgentName == agentName && messages[i].Response != "" && messages[i].Status == core.ConversationStatusCompleted {
			return messages[i].Response, true
		}
	}
	return "", false
}

func analyzeEventRCAUsingAgentsAndUpdateDb(ctx *security.RequestContext, request EventRCAAnalysisRequest) (EventAnalysisResponse, error) {

	dbManager, dbErr := common.GetDatabaseManager(common.Metastore)
	if dbErr != nil {
		ctx.GetLogger().Error("unable to get db manager for rca analysis", "error", dbErr, "event_id", request.EventId)
		return EventAnalysisResponse{}, dbErr
	}
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)

	eventRequest := EventAnalysisRequest{
		EventId:    request.EventId,
		AccountId:  request.AccountId,
		UserId:     request.UserId,
		Regenerate: request.Regenerate,
	}
	eventData, err := getEventData(ctx, eventRequest)

	if err != nil {
		ctx.GetLogger().Error("unable to get event data", "event_id", request.EventId, "error", err)
		return EventAnalysisResponse{}, err
	}

	eventFingerprint := eventData.Fingerprint
	if eventFingerprint == "" {
		ctx.GetLogger().Warn("analyzer: event fingerprint is empty, using event_id as fingerprint", "event_id", request.EventId)
		eventFingerprint = request.EventId
		eventData.Fingerprint = request.EventId
	}
	eventAggregationKey := eventData.AggregationKey

	// First ensure analysis is done or in progress
	existingLogAnalysis, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeLog)
	if existingLogAnalysis == nil || (existingLogAnalysis.Status != string(events.AnalysisStatusCompleted) && existingLogAnalysis.Status != string(events.AnalysisStatusInProgress)) {
		ctx.GetLogger().Info("analyzer: log analysis not done or started, triggering it before RCA", "event_id", request.EventId)

		// Mark analysis tasks as IN_PROGRESS to prevent concurrent executions
		analysisTypes := []events.EventAnalysisType{events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeLog}
		for _, aType := range analysisTypes {
			if err := eventAnalysisRepo.UpsertEventAnalysisInProgress(ctx, request.EventId, eventFingerprint, request.AccountId, eventAggregationKey, aType); err != nil {
				ctx.GetLogger().Warn("failed to upsert event analysis in progress", "error", err, "analysis_type", aType)
			}
		}

		// Trigger the normal analysis
		eventRequestAnalysis := EventAnalysisRequest{
			EventId:    request.EventId,
			AccountId:  request.AccountId,
			UserId:     request.UserId,
			Regenerate: request.Regenerate,
		}
		_, errAnalysis := analyzeEventUsingAgentsAndUpdateDb(ctx, eventRequestAnalysis)
		if errAnalysis != nil {
			ctx.GetLogger().Error("analyzer: failed to perform prerequisite log analysis", "error", errAnalysis)
			return EventAnalysisResponse{}, errAnalysis
		}
	} else if existingLogAnalysis.Status == string(events.AnalysisStatusInProgress) {
		ctx.GetLogger().Info("analyzer: prerequisite log analysis is already in progress, waiting for it", "event_id", request.EventId)
		// We could wait or return a specific status. For now, let's wait a bit or return an error to retry.
		return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
	}

	// Check if RCA is already completed
	existingRCA, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeRCA)
	if existingRCA != nil && existingRCA.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate {
		return EventAnalysisResponse{
			RelatedEventId:   existingRCA.RelatedEventId,
			EventId:          request.EventId,
			EventFingerprint: eventFingerprint,
			Status:           string(events.AnalysisStatusCompleted),
			Summary:          existingRCA.Summary,
		}, nil
	}

	_, annotations, errRule := eventAnalysisRepo.GetEventRuleDefinition(ctx, request.AccountId, eventAggregationKey)
	if errRule != nil {
		ctx.GetLogger().Error("analyzer: unable to get rule definition", "error", errRule, "rule", eventAggregationKey)
	}

	customRCAFormat := ""
	accountFormat, errFormat := eventAnalysisRepo.GetAccountRCAFormat(ctx, request.AccountId)
	if errFormat == nil && accountFormat != "" {
		customRCAFormat = accountFormat
	}

	// Rule-level format overrides account-level format. Reject oversized
	// annotation values (#29896) — fall back to the account-level format
	// rather than letting a misconfigured rule blow up the prompt.
	if annotations != nil && annotations["rca_format"] != nil {
		if format, ok := annotations["rca_format"].(string); ok && format != "" {
			if len(format) > maxAnnotationRCAFormatBytes {
				ctx.GetLogger().Warn("analyzer: rca_format annotation exceeds size cap, ignoring override",
					"limit_bytes", maxAnnotationRCAFormatBytes,
					"actual_bytes", len(format),
					"event_id", request.EventId,
					"fingerprint", eventFingerprint)
			} else {
				customRCAFormat = format
			}
		}
	}

	parentSessionId := events.SessionIdPrefixEventRCA + eventFingerprint
	response := EventAnalysisResponse{
		RelatedEventId:   request.EventId,
		EventId:          request.EventId,
		EventFingerprint: eventFingerprint,
		Status:           string(events.AnalysisStatusCompleted),
	}

	// Check if a previous analysis is still running or waiting for a client tool response.
	// WAITING_FOR_CLIENT_TOOL means sub-agents are blocked on relay execution — restarting
	// would create a duplicate cycle that hits the same relay timeout repeatedly.
	conv, err := core.GetConversationDao().GetConversationBySession(request.AccountId, parentSessionId)
	if err == nil && conv.ID != uuid.Nil && (conv.Status == core.ConversationStatusInProgress ||
		conv.Status == core.ConversationStatusWaiting ||
		conv.Status == core.ConversationStatusWaitingForClientTool) {
		ctx.GetLogger().Info("analyzer: skipping RCA analysis, conversation still active", "session_id", parentSessionId, "status", conv.Status)
		return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
	}

	// If regenerating, or if no conversation exists, or if conversation failed, we might need to delete old conversation
	if request.Regenerate || conv.ID == uuid.Nil || conv.Status == core.ConversationStatusFailed {
		err = core.DeleteConversationBySession(parentSessionId, request.AccountId, request.UserId)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to delete conversation", "error", err)
		}
	}

	rcaAgent, ok := core.GetNBAgent(ctx, core.ToolLlm, request.AccountId, core.AgentStatusEnabled)
	if !ok || rcaAgent == nil {
		ctx.GetLogger().Error("analyzer: LLM agent not found for RCA")
		if statusErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventFingerprint, request.AccountId, eventAggregationKey, string(events.AnalysisStatusFailed), "LLM agent not found", events.AnalysisTypeRCA); statusErr != nil {
			ctx.GetLogger().Warn("failed to update RCA status", "error", statusErr)
		}
		return EventAnalysisResponse{}, errors.New("LLM agent not found")
	}

	var rcaResponse string
	var hasResponse bool

	// Try to recover from existing COMPLETED conversation if not regenerating
	if !request.Regenerate {
		rcaResponse, hasResponse = getAgentResponseFromConversation(ctx, parentSessionId, request.AccountId, rcaAgent.GetName())
	}

	if hasResponse {
		ctx.GetLogger().Info("analyzer: recovered RCA response from conversation history", "session_id", parentSessionId)
	} else {
		// Gather all available analysis data to format into RCA
		summary, errSummary := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeSummary)
		if errSummary != nil {
			ctx.GetLogger().Warn("analyzer: failed to fetch summary for RCA", "error", errSummary)
		}
		investigation, errInv := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeInvestigation)
		if errInv != nil {
			ctx.GetLogger().Warn("analyzer: failed to fetch investigation for RCA", "error", errInv)
		}
		logAnalysis, errLog := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeLog)
		if errLog != nil {
			ctx.GetLogger().Warn("analyzer: failed to fetch log analysis for RCA", "error", errLog)
		}

		var dataBuilder strings.Builder
		if summary != nil && summary.Summary != "" {
			dataBuilder.WriteString("## Event Summary\n")
			dataBuilder.WriteString(summary.Summary)
			dataBuilder.WriteString("\n\n")
		}
		if investigation != nil && investigation.Summary != "" {
			dataBuilder.WriteString("## Investigation Findings\n")
			dataBuilder.WriteString(investigation.Summary)
			dataBuilder.WriteString("\n\n")
		}
		if logAnalysis != nil && logAnalysis.Analysis != "" {
			dataBuilder.WriteString("## Log Analysis & Code Insights\n")
			dataBuilder.WriteString(logAnalysis.Analysis)
			dataBuilder.WriteString("\n\n")
		}

		rcaPrompt := "Generate a detailed Root Cause Analysis (RCA) report based on the provided event summary, investigation findings, and log analysis."
		if customRCAFormat != "" {
			rcaPrompt += "\n\nUse the following report format:\n" + customRCAFormat
		} else {
			rcaPrompt += "\n\nUse the following report format:\n" + agents.DefaultRCAFormat
		}

		resp, err := core.HandleConversationSessionRequest(ctx, rcaAgent, eventRequest.UserId, eventRequest.AccountId, parentSessionId, rcaPrompt, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation), core.ConversationSessionRequestWithEnableCritique(true), core.ConversationSessionRequestWithQueryContext(dataBuilder.String()))
		if err != nil {
			if errors.Is(err, core.ErrConversationInProgress) {
				ctx.GetLogger().Info("analyzer: RCA analysis already in progress via conversation", "session_id", parentSessionId)
				return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
			}
			ctx.GetLogger().Warn("analyzer: failed to get rca analysis", "error", err, "event_id", response.EventId)
			err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventFingerprint, request.AccountId, eventAggregationKey, string(events.AnalysisStatusFailed), "unable to get rca analysis - "+err.Error(), events.AnalysisTypeRCA)
			if err != nil {
				ctx.GetLogger().Error("unable to update status", "error", err)
			}
			return EventAnalysisResponse{}, err
		}
		if len(resp.Response) > 0 {
			rcaResponse = resp.Response[0]
			hasResponse = true
		}
	}

	if hasResponse {
		ctx.GetLogger().Debug("analyzer: saving RCA report to database")
		// Save the response to the database
		err = eventAnalysisRepo.SaveEventRCAAnalysis(ctx, response.EventId, eventFingerprint, request.AccountId, eventAggregationKey, rcaResponse)
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to insert analysis from database", "error", err, "event_id", request.EventId)
			err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventFingerprint, request.AccountId, eventAggregationKey, string(events.AnalysisStatusFailed), "unable to insert data - "+err.Error(), events.AnalysisTypeRCA)
			if err != nil {
				ctx.GetLogger().Error("unable to update status", "error", err)
			}
			return EventAnalysisResponse{}, err
		}
		response.Summary = rcaResponse
	} else {
		return EventAnalysisResponse{}, errors.New("failed to get RCA response")
	}

	return response, nil
}

func generateEventAnalysisPrompt(ctx *security.RequestContext, event events.Event, request EventAnalysisRequest, response EventAnalysisResponse, parsedLabels map[string]any, anaylsisRepo *events.EventAnalysisRepository) (string, string, bool, string, error) {
	eventDefinition, annotations, err := anaylsisRepo.GetEventRuleDefinition(ctx, request.AccountId, event.AggregationKey)
	if err != nil {
		ctx.GetLogger().Error("analyzer: unable to get rule definition", "error", err, "rule", event.AggregationKey)
		eventDefinition = "n/a" // Default value if fetching fails
	}

	// do rootcause analysis
	eventAnalsysisPrompt := prompts_repo.GetPrompt(prompts_repo.PromptEventInvestigationSummary, request.EventId, eventDefinition, event.Title, event.Description, event.Labels, common.FormatPresentationTime(event.UpdatedAt), event.Source, response.Summary)

	// Add account context (cloud provider) to help the LLM tailor its output
	if cloudProvider := agents.GetCloudProviderForAccount(request.AccountId); cloudProvider != "" {
		eventAnalsysisPrompt = eventAnalsysisPrompt + "\n\n**Account Context:** This account's infrastructure is on " + cloudProvider + ". Tailor your analysis, examples, and recommendations to this infrastructure type."
	}

	accountPrompt, _, _ := core.AgentAdditionalInstructionsAndToolsAndConfigs(ctx, request.AccountId, "event_log_analysis")
	debugAnalysisEnabled := true
	debugAnalysisSkipReason := ""
	debugAnalysisDisabled, err := common.IsFeatureEnabledForAccount("EVENT_DEBUG_ANALYSIS_DISABLED", ctx.GetSecurityContext().GetTenantId(), request.AccountId)
	if err == nil && debugAnalysisDisabled {
		debugAnalysisEnabled = false
		debugAnalysisSkipReason = "skipped - debug analysis is not enabled for this account"
	}
	if source, ok := parsedLabels["nb_webhook_source"].(string); ok && strings.HasPrefix(source, "datadog") {
		// Allow accounts to bypass the service label requirement via feature flag
		serviceCheckDisabled, _ := common.IsFeatureEnabledForAccount("EVENT_INVESTIGATION_SKIP_SERVICE_LABEL_CHECK", ctx.GetSecurityContext().GetTenantId(), request.AccountId)
		if !serviceCheckDisabled {
			// Disable debug analysis for datadog events if both services and service labels are missing or empty
			if !common.HasNonEmptyValue(parsedLabels["services"]) && !common.HasNonEmptyValue(parsedLabels["service"]) {
				debugAnalysisEnabled = false
				debugAnalysisSkipReason = "skipped - event missing 'service' or 'services' label required for investigation"
			} else {
				debugAnalysisEnabled = true
				debugAnalysisSkipReason = ""
			}
		}
	}
	if debugAnalysisEnabled {
		// check prompt instructions
		userPrompt := ""
		if annotations != nil && annotations["runbook"] != nil {
			if r, ok := annotations["runbook"].(string); ok {
				userPrompt = r
			}
		}
		// check for knowledge base articles
		if userPrompt == "" {
			if kb, found := anaylsisRepo.GetKnowledgebase(ctx, event.AggregationKey); found {
				userPrompt = "Refer to the following knowledge base article(s) while analyzing the event:\n"
				userPrompt += "**Description:**" + kb.Description + "\n"
				userPrompt += "**Diagnosis:**" + kb.Diagnosis + "\n"
				userPrompt += "**Impact:**" + kb.Impact + "\n"
				userPrompt += "**Mitigation:**" + kb.Mitigation + "\n"
			}
		}
		if userPrompt != "" {
			eventAnalsysisPrompt = "## Troubleshooting Steps For Investigation (CRITICAL) -\n" + userPrompt + "\n\n" + eventAnalsysisPrompt
		}
	}
	return eventAnalsysisPrompt, accountPrompt, debugAnalysisEnabled, debugAnalysisSkipReason, err
}

func analyzeEventUsingAgentsAndUpdateDb(ctx *security.RequestContext, request EventAnalysisRequest) (EventAnalysisResponse, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get db", "error", err)
		return EventAnalysisResponse{}, err
	}
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)

	eventData, err := getEventData(ctx, request)
	if err != nil {
		return EventAnalysisResponse{}, err
	}

	eventFingerprint := eventData.Fingerprint
	eventAggregationKey := eventData.AggregationKey

	parsedLabels := make(map[string]any)
	if labelsStr, ok := eventData.Labels.(string); ok {
		parsedLabels = parseEventLabels(labelsStr)
	}

	if parsedLabels == nil {
		parsedLabels = make(map[string]any)
	}

	if len(parsedLabels) == 0 {
		parsedLabels["subject"] = eventData.SubjectName
		parsedLabels["subject_namespace"] = eventData.SubjectNamespace
		parsedLabels["subject_node"] = eventData.SubjectNode
		parsedLabels["subject_type"] = eventData.SubjectType
		parsedLabels["subject_owner"] = eventData.SubjectOwner
		parsedLabels["aggregation_key"] = eventData.AggregationKey
	}

	if parsedLabels["start"] == nil && eventData.StartsAt != nil {
		parsedLabels["start"] = eventData.StartsAt.UnixMilli()
	}

	if parsedLabels["end"] == nil && eventData.EndsAt != nil {
		parsedLabels["end"] = eventData.EndsAt.UnixMilli()
	} else if parsedLabels["end"] == nil {
		parsedLabels["end"] = time.Now().UnixMilli()
	}

	if eventFingerprint == "" {
		ctx.GetLogger().Warn("analyzer: event fingerprint is empty, using event_id as fingerprint", "event_id", request.EventId)
		eventFingerprint = request.EventId
		eventData.Fingerprint = request.EventId
	}

	parentConversationId := events.SessionIdPrefixEvent + eventFingerprint
	response := EventAnalysisResponse{
		RelatedEventId:   request.EventId,
		EventId:          request.EventId,
		EventFingerprint: eventFingerprint,
		Status:           string(events.AnalysisStatusCompleted),
	}

	// Skip if a previous analysis is still running or waiting for a client tool response.
	conv, err := core.GetConversationDao().GetConversationBySession(request.AccountId, parentConversationId)
	if err == nil && conv.ID != uuid.Nil && (conv.Status == core.ConversationStatusInProgress ||
		conv.Status == core.ConversationStatusWaiting ||
		conv.Status == core.ConversationStatusWaitingForClientTool) && !request.Regenerate {
		ctx.GetLogger().Info("analyzer: skipping event analysis, conversation still active", "session_id", parentConversationId, "status", conv.Status)
		return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
	}

	// Defense-in-depth: skip when every analysis type is already COMPLETED.
	// Callers (executeEventInvestigation HTTP handler, MQ consumer, sync job)
	// each have their own short-circuit for this case, but they all race on a
	// read-then-submit pattern — a late-arriving worker can reach here after
	// the first one already finished the work. Without this check the late
	// worker runs the full pipeline again, dispatching a redundant k8s_debug
	// (or whichever debug agent) sub-agent and burning LLM tokens on work
	// whose results already exist.
	if !request.Regenerate {
		allCompleted := true
		for _, aType := range []events.EventAnalysisType{
			events.AnalysisTypeSummary,
			events.AnalysisTypeInvestigation,
			events.AnalysisTypeLog,
			events.AnalysisTypeDetailedResponse,
		} {
			a, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, aType)
			if a == nil || a.Status != string(events.AnalysisStatusCompleted) {
				allCompleted = false
				break
			}
		}
		if allCompleted {
			ctx.GetLogger().Info("analyzer: skipping event analysis, all analysis types already completed",
				"session_id", parentConversationId, "event_id", request.EventId)
			return EventAnalysisResponse{Status: string(events.AnalysisStatusCompleted)}, nil
		}
	}

	// If regenerating, or if no conversation exists, or if conversation failed, we might need to delete old conversation
	if request.Regenerate || conv.ID == uuid.Nil || conv.Status == core.ConversationStatusFailed {
		err = core.DeleteConversationBySession(parentConversationId, request.AccountId, request.UserId)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to delete conversation", "error", err)
		}
	}

	// Step 1: Summary
	existingSummary, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeSummary)
	if existingSummary != nil && existingSummary.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate {
		response.Summary = existingSummary.Summary
		if existingSummary.RelatedEventId != "" {
			response.RelatedEventId = existingSummary.RelatedEventId
		}
		ctx.GetLogger().Info("analyzer: using existing summary", "event_id", request.EventId)
	} else {
		//generate initial summary if not present
		eventSummaryAgent, ok := core.GetNBAgent(ctx, agents.EventsAgentName, request.AccountId, core.AgentStatusEnabled)
		if !ok || eventSummaryAgent == nil {
			updateAllFailed(ctx, eventAnalysisRepo, eventData, request.AccountId, "summary agent not found")
			return EventAnalysisResponse{}, errors.New("summary agent not found")
		}
		summaryAgentName := eventSummaryAgent.GetName()

		var summaryResponseStr string
		var hasSummary bool

		if !request.Regenerate {
			summaryResponseStr, hasSummary = getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, summaryAgentName)
		}

		if hasSummary {
			ctx.GetLogger().Info("analyzer: recovered summary from conversation history", "session_id", parentConversationId)
		} else {
			summaryResp, err := core.HandleConversationSessionRequest(ctx, eventSummaryAgent, request.UserId, request.AccountId, parentConversationId, "Get the details of Event with id - "+eventData.Id, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation), core.ConversationSessionRequestWithEnableCritique(false), core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{Labels: parsedLabels}))
			if err != nil {
				if errors.Is(err, core.ErrConversationInProgress) {
					ctx.GetLogger().Info("analyzer: summary already in progress via conversation", "session_id", parentConversationId)
					return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
				}
				ctx.GetLogger().Error("analyzer: unable to generate summary", "error", err)
				updateAllFailed(ctx, eventAnalysisRepo, eventData, request.AccountId, err.Error())
				return EventAnalysisResponse{}, err
			}
			if len(summaryResp.Response) > 0 {
				summaryResponseStr = summaryResp.Response[0]
				hasSummary = true
			}
		}

		if hasSummary {
			response.Summary = summaryResponseStr
			// update status based on current summary
			err = eventAnalysisRepo.UpsertEventAnalysis(ctx, request.EventId, "", summaryResponseStr, string(events.AnalysisStatusCompleted), eventData.Fingerprint, request.AccountId, eventData.AggregationKey, events.AnalysisTypeSummary)
			if err != nil {
				ctx.GetLogger().Error("unable to update status after summarization", "error", err, "eventId", request.EventId)
			}
		} else {
			updateAllFailed(ctx, eventAnalysisRepo, eventData, request.AccountId, "empty summary response")
			return EventAnalysisResponse{}, errors.New("empty summary response")
		}
	}

	// Preserve the initial summary before investigation overwrites response.Summary
	initialSummary := response.Summary

	// Register or retrieve our custom event analyzer agent
	eventAnalsysisPrompt, accountPrompt, debugAnalysisEnabled, debugSkipReason, err := generateEventAnalysisPrompt(ctx, eventData, request, response, parsedLabels, eventAnalysisRepo)
	if err != nil {
		return EventAnalysisResponse{}, err
	}

	if !debugAnalysisEnabled {
		ctx.GetLogger().Info("analyzer: debug analysis is disabled, skipping log analysis", "event_id", request.EventId, "reason", debugSkipReason)
		if err := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), debugSkipReason, events.AnalysisTypeInvestigation); err != nil {
			ctx.GetLogger().Warn("failed to update event analysis status on debug skip", "error", err)
		}
		if err := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), debugSkipReason, events.AnalysisTypeLog); err != nil {
			ctx.GetLogger().Warn("failed to update event analysis status on debug skip", "error", err)
		}
		// DetailedResponse = initial summary when debug is disabled (no deeper analysis to enrich with)
		if err := eventAnalysisRepo.UpsertEventAnalysis(ctx, request.EventId, "", response.Summary, string(events.AnalysisStatusCompleted), eventData.Fingerprint, request.AccountId, eventData.AggregationKey, events.AnalysisTypeDetailedResponse); err != nil {
			ctx.GetLogger().Warn("failed to upsert detailed_response on debug skip", "error", err)
		}
		response.DetailedResponse = response.Summary
		return response, nil
	}

	// Step 2: Investigation (Root Cause Analysis Prompt)
	// investigationText is captured at function scope so Step 4 (synthesis) can use it.
	var investigationText string
	existingInvestigation, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeInvestigation)
	if existingInvestigation != nil && existingInvestigation.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate {
		response.Summary = existingInvestigation.Summary
		investigationText = existingInvestigation.Summary
		if existingInvestigation.RelatedEventId != "" {
			response.RelatedEventId = existingInvestigation.RelatedEventId
		}
		ctx.GetLogger().Info("analyzer: using existing investigation", "event_id", request.EventId)
	} else {
		rootcauseAgent, ok := core.GetNBAgent(ctx, agents.GetDebugAgentName(request.AccountId), request.AccountId, core.AgentStatusEnabled)
		if !ok || rootcauseAgent == nil {
			if updateErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusFailed), "investigation agent not found", events.AnalysisTypeInvestigation); updateErr != nil {
				ctx.GetLogger().Warn("failed to update event analysis status on investigation agent missing", "error", updateErr)
			}
			return response, errors.New("investigation agent not found")
		}
		rootcauseAgentName := rootcauseAgent.GetName()
		var investigationResponse string
		var hasInvestigation bool

		if !request.Regenerate {
			investigationResponse, hasInvestigation = getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, rootcauseAgentName)
		}

		if hasInvestigation {
			ctx.GetLogger().Info("analyzer: recovered investigation from conversation history", "session_id", parentConversationId)
		} else {
			rootcauseAnalysis, err := core.HandleConversationSessionRequest(ctx, rootcauseAgent, request.UserId, request.AccountId, parentConversationId, eventAnalsysisPrompt, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation), core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{Labels: parsedLabels}), core.ConversationSessionRequestWitAdditionalSystemPrompt(accountPrompt), core.ConversationSessionRequestWithEnableCritique(true))

			if err != nil {
				if errors.Is(err, core.ErrConversationInProgress) {
					ctx.GetLogger().Info("analyzer: investigation already in progress via conversation", "session_id", parentConversationId)
					return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
				}
				ctx.GetLogger().Error("analyzer: unable to generate rootcause analysis", "event_id", request.EventId, "session_id", parentConversationId, "error", err)
				if updateErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusFailed), "investigation failed - "+err.Error(), events.AnalysisTypeInvestigation); updateErr != nil {
					ctx.GetLogger().Warn("failed to update event analysis status on investigation failure", "error", updateErr)
				}
			} else if len(rootcauseAnalysis.Response) > 0 && rootcauseAnalysis.Status == core.ConversationStatusCompleted {
				investigationResponse = rootcauseAnalysis.Response[0]
				hasInvestigation = true
			}
		}

		if hasInvestigation {
			response.Summary = investigationResponse
			investigationText = investigationResponse
			err = eventAnalysisRepo.UpsertEventAnalysis(ctx, request.EventId, "", response.Summary, string(events.AnalysisStatusCompleted), eventData.Fingerprint, request.AccountId, eventData.AggregationKey, events.AnalysisTypeInvestigation)
			if err != nil {
				ctx.GetLogger().Error("unable to update status after root-cause", "error", err, "eventId", request.EventId)
			}
		}
	}

	// Step 3: Log Analysis
	// logAnalysisText holds the plain-text analysis used by Step 4 synthesis.
	var logAnalysisText string

	existingLog, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeLog)
	if existingLog != nil && existingLog.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate {
		response.Analysis = existingLog.Analysis
		if existingLog.RelatedEventId != "" {
			response.RelatedEventId = existingLog.RelatedEventId
		}
		ctx.GetLogger().Info("analyzer: using existing log analysis", "event_id", request.EventId)
		logAnalysisText = existingLog.Analysis

		// If DetailedResponse is also already done, return immediately.
		existingDetailed, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeDetailedResponse)
		if existingDetailed != nil && existingDetailed.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate {
			response.DetailedResponse = existingDetailed.Summary
			// Populate Investigation from DB before returning so callers have the full response.
			if response.Investigation == "" {
				if existingInv, _ := eventAnalysisRepo.GetEventAnalysis(ctx, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeInvestigation); existingInv != nil {
					response.Investigation = existingInv.Summary
				}
			}
			return response, nil
		}
	} else {
		logs := eventData.Evidences.LogData
		if logs == "" && eventData.Evidences.ErrorLogData != nil {
			logs = strings.Join(eventData.Evidences.ErrorLogData, "\n")
		}
		//pick sample data if that is sufficiently large
		if eventData.AggregationKey == "HighErrorCriticalLogs" && eventData.Evidences.AlertLabels.Data != nil {
			alerts, ok := eventData.Evidences.AlertLabels.Data.([]any)
			if !ok {
				ctx.GetLogger().Warn("analyzer: alert labels data has unexpected shape, skipping sample lookup", "event_id", request.EventId)
			}
			for _, alertAny := range alerts {
				alert, ok := alertAny.(map[string]any)
				if !ok {
					continue
				}
				if alert["label"] == "sample" {
					if sampleAlertLog, ok := alert["value"].(string); ok {
						logs = sampleAlertLog
						break
					}
				}
			}
		}

		if logs == "" {
			if err := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), "skipped - no logs", events.AnalysisTypeLog); err != nil {
				ctx.GetLogger().Warn("failed to update event analysis status on empty logs", "error", err)
			}
			// No logs — synthesis will run with only summary + investigation
		} else {
			logAnalysisResponse, err := analyzeLogsAndUpdateResponse(ctx, request, response, logs, parentConversationId, eventAnalysisRepo, eventData, parsedLabels, investigationText, dbManager)
			if err != nil {
				// Synthesis can still run with summary + investigation data.
				// For ErrConversationInProgress: another worker is actively processing the
				// logs and will eventually update the status. Do NOT overwrite IN_PROGRESS
				// with FAILED here — it would cause a race where a successful run gets
				// reported as failed in the UI. Just continue to synthesis with partial data.
				if errors.Is(err, core.ErrConversationInProgress) {
					ctx.GetLogger().Info("analyzer: log analysis already in progress in another worker, continuing synthesis with partial data", "event_id", request.EventId)
				} else {
					ctx.GetLogger().Warn("unable to analyze logs, continuing to synthesis without log data", "error", err, "event_id", request.EventId)
					if dbErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusFailed), "log analysis error: "+err.Error(), events.AnalysisTypeLog); dbErr != nil {
						ctx.GetLogger().Warn("failed to update log analysis status to failed", "error", dbErr)
					}
				}
				// logAnalysisText remains "" — synthesis proceeds with available data
			} else {
				ctx.GetLogger().Debug("analyzer: saving log analysis to database")
				jsonResponse, err := common.MarshalJson(logAnalysisResponse)
				if err != nil {
					ctx.GetLogger().Warn("analyzer: failed to marshal response to JSON", "error", err, "event_id", response.EventId)
					err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventFingerprint, request.AccountId, eventAggregationKey, string(events.AnalysisStatusFailed), "unable to serialize json - "+err.Error(), events.AnalysisTypeLog)
					if err != nil {
						ctx.GetLogger().Error("unable to update status", "error", err)
					}
					return EventAnalysisResponse{}, err
				}
				response.Analysis = string(jsonResponse)
				err = eventAnalysisRepo.UpsertEventAnalysis(ctx, response.EventId, response.Analysis, response.Summary, string(events.AnalysisStatusCompleted), eventFingerprint, request.AccountId, eventAggregationKey, events.AnalysisTypeLog)
				if err != nil {
					ctx.GetLogger().Debug("analyzer: failed to insert analysis from database, updating existing value", "event_id", request.EventId)
					err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventFingerprint, request.AccountId, eventAggregationKey, string(events.AnalysisStatusFailed), "unable to insert data - "+err.Error(), events.AnalysisTypeLog)
					if err != nil {
						ctx.GetLogger().Error("unable to update status", "error", err)
					}
					return EventAnalysisResponse{}, err
				}
				logAnalysisText = logAnalysisResponse.Analysis
			}
		}
	}

	// Step 4: Synthesize detailed response combining initial summary + investigation + log analysis.
	// This is a soft step — failure falls back to initialSummary without failing the whole analysis.
	ctx.GetLogger().Info("analyzer: synthesizing detailed response", "event_id", request.EventId)
	detailedResponse, synthErr := synthesizeDetailedResponse(ctx, request, parentConversationId, initialSummary, investigationText, logAnalysisText)
	if synthErr != nil {
		ctx.GetLogger().Warn("analyzer: failed to synthesize detailed response, falling back to initial summary", "error", synthErr, "event_id", request.EventId)
		detailedResponse = initialSummary
	}
	if err := eventAnalysisRepo.UpsertEventAnalysis(ctx, response.EventId, "", detailedResponse, string(events.AnalysisStatusCompleted), eventFingerprint, request.AccountId, eventAggregationKey, events.AnalysisTypeDetailedResponse); err != nil {
		ctx.GetLogger().Warn("analyzer: failed to save detailed response", "error", err, "event_id", request.EventId)
	}
	response.DetailedResponse = detailedResponse

	return response, nil
}

func updateAllFailed(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventData events.Event, accountId, errMsg string) {
	analysisTypes := []events.EventAnalysisType{events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeLog, events.AnalysisTypeDetailedResponse}
	for _, aType := range analysisTypes {
		if updateErr := repo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, accountId, eventData.AggregationKey, string(events.AnalysisStatusFailed), "event analysis failed - "+errMsg, aType); updateErr != nil {
			ctx.GetLogger().Error("unable to update status", "error", updateErr, "analysis_type", aType)
		}
	}
}

func isGitIntegrationConfigured(accountId string) bool {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false
	}
	var count int
	err = dbms.Db.Get(&count, `
		SELECT COUNT(*)
		FROM integrations i
		WHERE i.tenant_id IN (SELECT tenant FROM cloud_accounts WHERE id = $1)
		  AND i.status = 'enabled'
		  AND i.type IN ('github', 'gitlab')
	`, accountId)
	if err != nil {
		return false
	}
	return count > 0
}

func analyzeLogsAndUpdateResponse(ctx *security.RequestContext, request EventAnalysisRequest, response EventAnalysisResponse, logs string, parentConversationId string, eventAnalysisRepo *events.EventAnalysisRepository, eventData events.Event, parsedLabels map[string]any, investigationContext string, dbManager *common.DatabaseManager) (EventAnalysisResponse, error) {
	if !isGitIntegrationConfigured(request.AccountId) {
		ctx.GetLogger().Info("analyzer: skipping code analysis - no github/gitlab integration configured", "account_id", request.AccountId)
		response.Status = string(events.AnalysisStatusCompleted)
		return response, nil
	}

	llm := agents.CodeAgent2{}

	// Include eventId in the configuration to be used by the LogAnalysisAgent
	eventConfig := toolcore.NBQueryConfig{
		EventId: request.EventId,
	}

	// Set namespace and workload from event data or labels
	var namespace, workload string
	subjectType := strings.ToLower(eventData.SubjectType)

	// Strategy 1: Use SubjectOwner and SubjectNamespace directly from event data.
	// Skip for pods — SubjectOwner may be a ReplicaSet name; later strategies resolve the stable workload name.
	if subjectType != "pod" && eventData.SubjectOwner != "" && eventData.SubjectNamespace != "" {
		workload = eventData.SubjectOwner
		namespace = eventData.SubjectNamespace
		ctx.GetLogger().Info("using subject owner and namespace from event data",
			"namespace", namespace, "workload", workload)
	}

	// Strategy 2: For deployment/statefulset events, SubjectName IS the workload name
	if workload == "" && eventData.SubjectNamespace != "" && eventData.SubjectName != "" {
		if subjectType == "deployment" || subjectType == "statefulset" || subjectType == "daemonset" {
			workload = eventData.SubjectName
			namespace = eventData.SubjectNamespace
			ctx.GetLogger().Info("using subject name as workload for workload-level event",
				"namespace", namespace, "workload", workload, "subject_type", eventData.SubjectType)
		}
	}

	// Strategy 3: Check parsedLabels (populated from event labels or subject fields)
	if workload == "" {
		if owner, ok := parsedLabels["subject_owner"].(string); ok && owner != "" {
			if ns, ok := parsedLabels["subject_namespace"].(string); ok && ns != "" {
				workload = owner
				namespace = ns
				ctx.GetLogger().Info("using subject owner and namespace from parsed labels",
					"namespace", namespace, "workload", workload)
			}
		}
	}

	// Strategy 4: Check labels for app_id pattern "/k8s/{namespace}/{workload}"
	if workload == "" {
		if labels, ok := parsedLabels["labels"].(map[string]any); ok {
			if appID, ok := labels["app_id"].(string); ok {
				parts := strings.Split(appID, "/")
				if len(parts) >= 4 && parts[1] == "k8s" {
					namespace = parts[2]
					workload = parts[3]
					ctx.GetLogger().Info("extracted namespace and workload from app_id",
						"namespace", namespace, "workload", workload, "app_id", appID)
				}
			}
		}
	}

	// Add namespace and workload to the event config if they were found
	if namespace != "" && workload != "" {
		eventConfig.Namespace = namespace
		eventConfig.Workload = workload
	}

	// Early check: verify source code annotations exist for this workload before
	// invoking CodeAgent2 (which spins up workspace pods). If we cannot resolve a
	// repository for the workload, there is nothing for the code agent to analyze.
	// dbManager is provided by the caller (resolved once at the start of the analysis flow).
	if namespace == "" || workload == "" {
		ctx.GetLogger().Info("analyzer: skipping code analysis - unable to resolve namespace/workload from event data",
			"event_id", request.EventId, "account_id", request.AccountId)
		if dbErr := eventAnalysisRepo.UpsertEventAnalysis(ctx, request.EventId, "", "", string(events.AnalysisStatusCompleted), eventData.Fingerprint, request.AccountId, eventData.AggregationKey, events.AnalysisTypeLog); dbErr != nil {
			ctx.GetLogger().Warn("analyzer: failed to persist log analysis skip status", "error", dbErr)
		}
		response.Status = string(events.AnalysisStatusCompleted)
		return response, nil
	}

	annotations, annErr := services_server.GetSourceCodeAnnotations(ctx, dbManager, request.AccountId, services_server.SourceCodeAnnotationOptions{
		EventId:      request.EventId,
		WorkloadName: workload,
		Namespace:    namespace,
	})
	if annErr != nil || !services_server.HasKnownRepoAnnotation(annotations) {
		ctx.GetLogger().Info("analyzer: skipping code analysis - no source code repository mapped for workload",
			"namespace", namespace, "workload", workload, "account_id", request.AccountId, "annotation_error", annErr)
		if dbErr := eventAnalysisRepo.UpsertEventAnalysis(ctx, request.EventId, "", "", string(events.AnalysisStatusCompleted), eventData.Fingerprint, request.AccountId, eventData.AggregationKey, events.AnalysisTypeLog); dbErr != nil {
			ctx.GetLogger().Warn("analyzer: failed to persist log analysis skip status", "error", dbErr)
		}
		response.Status = string(events.AnalysisStatusCompleted)
		return response, nil
	}

	// Build an enriched query for the code agent that includes event context,
	// investigation findings, and the actual logs. This gives the LLM much better
	// context to identify code-related root causes and propose targeted fixes.
	//
	// Apply size guards on unbounded inputs (logs and investigation context) so the
	// combined query stays well within LLM context limits regardless of payload size.
	const (
		maxLogChars                  = 16000 // ~4k tokens, sufficient for relevant error context
		maxInvestigationContextChars = 8000  // ~2k tokens, enough for the key findings
	)
	truncatedLogs := core.TruncateMiddle(logs, maxLogChars/2, maxLogChars/2)
	truncatedInvestigation := core.TruncateMiddle(investigationContext, maxInvestigationContextChars/2, maxInvestigationContextChars/2)

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString("Analyze the following event and identify code-level fixes.\n\n")
	fmt.Fprintf(&queryBuilder, "## Event\nTitle: %s\nDescription: %s\n", eventData.Title, eventData.Description)
	if truncatedInvestigation != "" {
		fmt.Fprintf(&queryBuilder, "\n## Investigation Findings\n%s\n", truncatedInvestigation)
	}
	fmt.Fprintf(&queryBuilder, "\n## Logs\n%s", truncatedLogs)

	llmParams := map[string]string{"query": queryBuilder.String()}
	llmParamsJSON, err := json.Marshal(llmParams)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to marshal labels to JSON", "error", err, "event_id", request.EventId)
	}

	var llmResponse core.NBAgentResponse
	var hasResponse bool

	if !request.Regenerate {
		respStr, found := getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, llm.GetName())
		if found {
			llmResponse = core.NBAgentResponse{
				Response: []string{respStr},
				Status:   core.ConversationStatusCompleted,
			}
			hasResponse = true
			ctx.GetLogger().Info("analyzer: recovered log analysis response from conversation history", "session_id", parentConversationId)
		}
	}

	if !hasResponse {
		llmResponse, err = core.HandleConversationSessionRequest(
			ctx,
			llm,
			request.UserId,
			request.AccountId,
			parentConversationId,
			string(llmParamsJSON),
			core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
			core.ConversationSessionRequestWithConfig(eventConfig),
			core.ConversationSessionRequestWithEnableCritique(true),
		)
	}

	if err != nil {
		// Mark analysis as failed regardless of partial response
		response.Status = string(events.AnalysisStatusFailed)
		if updateErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, response.EventFingerprint, request.AccountId, response.EventAggregationKey, string(events.AnalysisStatusFailed), "unable to execute log agent - "+err.Error(), events.AnalysisTypeLog); updateErr != nil {
			ctx.GetLogger().Error("unable to update status", "error", updateErr)
		}

		// Capture partial response if available for debugging purposes
		if len(llmResponse.Response) != 0 {
			response.Analysis = llmResponse.Response[0]
		} else {
			response.Analysis = "unable to parse response - " + err.Error()
		}
		return response, err
	}

	// Check if conversation failed (agent returned FAILED status without Go error)
	if llmResponse.Status == core.ConversationStatusFailed {
		failReason := "log analysis agent failed"
		if len(llmResponse.Response) > 0 {
			failReason = llmResponse.Response[0]
		}
		ctx.GetLogger().Error("analyzer: log analysis conversation failed", "event_id", request.EventId, "reason", failReason)
		err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, response.EventFingerprint, request.AccountId, response.EventAggregationKey, string(events.AnalysisStatusFailed), failReason, events.AnalysisTypeLog)
		if err != nil {
			ctx.GetLogger().Error("unable to update status", "error", err)
		}
		response.Analysis = failReason
		response.Status = string(events.AnalysisStatusFailed)
		return response, fmt.Errorf("log analysis failed: %s", failReason)
	}

	// Check if llmResponse.Response has at least one element before attempting to unmarshal
	if len(llmResponse.Response) == 0 {
		ctx.GetLogger().Error("llmResponse.Response is empty")
		err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, response.EventFingerprint, request.AccountId, response.EventAggregationKey, string(events.AnalysisStatusFailed), "empty response from LLM", events.AnalysisTypeLog)
		if err != nil {
			ctx.GetLogger().Error("unable to update status", "error", err)
		}
		response.Analysis = "empty response from LLM"
		response.Status = string(events.AnalysisStatusFailed)
		return response, errors.New("empty response from LLM")
	}

	logAnalysisResponse := map[string]any{}

	err = common.ExtractAndUnmarshalJSON([]byte(llmResponse.Response[0]), &logAnalysisResponse)
	if err != nil {
		ctx.GetLogger().Error("unable to parse llm response", "error", err)
		err = eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, response.EventFingerprint, request.AccountId, response.EventAggregationKey, string(events.AnalysisStatusFailed), "malformed JSON response from LLM - "+err.Error(), events.AnalysisTypeLog)
		if err != nil {
			ctx.GetLogger().Error("unable to update status", "error", err)
		}
		response.Analysis = llmResponse.Response[0]
		response.Status = string(events.AnalysisStatusFailed)
		return response, err
	}

	// Handle new format with nested structure
	var actualResponse map[string]any
	if data, ok := logAnalysisResponse["data"].(map[string]any); ok {
		if result, ok := data["result"].(map[string]any); ok {
			if agentResponse, ok := result["agent_response"].(map[string]any); ok {
				actualResponse = agentResponse
			} else if analysisResult, ok := result["analysis_result"].(map[string]any); ok {
				actualResponse = analysisResult
			}
		}
	}

	// Fallback to old format if new format not found
	if actualResponse == nil {
		actualResponse = logAnalysisResponse
	}
	response.Commits = parseGitCommits(actualResponse)
	response.SourceDetails = map[string]any{}
	response.SourceUpdates = map[string]any{}
	// Parse response field (old format) or description field (new format).
	// Use comma-ok so a non-string value (LLMs occasionally return objects/arrays)
	// is skipped instead of panicking the analysis goroutine.
	if v, ok := actualResponse["response"].(string); ok {
		response.Analysis = v
	} else if v, ok := actualResponse["description"].(string); ok {
		response.Analysis = v
		response.SourceUpdates["explanation"] = response.Analysis
	}

	// Parse summary field (new format) or title field
	if v, ok := actualResponse["title"].(string); ok {
		response.Title = v
	}

	// Parse additional fields from new format
	if actualResponse["description"] != nil {
		if desc, ok := actualResponse["description"].(string); ok {
			response.Description = desc
			// If Analysis wasn't set from response field, use description
			if response.Analysis == "" {
				response.Analysis = desc
			}
		}
	}

	if actualResponse["error_message"] != nil {
		if errMsg, ok := actualResponse["error_message"].(string); ok {
			response.ErrorMessage = errMsg
		}
	}

	if actualResponse["original_code"] != nil {
		if origCode, ok := actualResponse["original_code"].(string); ok {
			response.OriginalCode = origCode
		}
	}

	if actualResponse["fixed_code"] != nil {
		if fixedCode, ok := actualResponse["fixed_code"].(string); ok {
			response.FixedCode = fixedCode
		}
	}

	if actualResponse["git_diff"] != nil {
		if gitDiff, ok := actualResponse["git_diff"].(string); ok {
			response.GitDiff = gitDiff
			response.SourceUpdates["gitDiff"] = gitDiff
		}
	}

	if actualResponse["commit_hash"] != nil {
		if commitHash, ok := actualResponse["commit_hash"].(string); ok {
			response.CommitHash = commitHash
		} else if len(response.Commits) > 0 {
			response.CommitHash = response.Commits[0].Hash
		}
	}

	if actualResponse["author"] != nil {
		if author, ok := actualResponse["author"].(string); ok {
			response.Author = author
		} else if len(response.Commits) > 0 {
			response.Author = response.Commits[0].Author
		}
	}

	if actualResponse["commit_date"] != nil {
		if commitDate, ok := actualResponse["commit_date"].(string); ok {
			response.CommitDate = commitDate
		} else if len(response.Commits) > 0 {
			response.CommitDate = response.Commits[0].Date
		}
	}

	// Parse PR list
	if actualResponse["pr_list"] != nil {
		if prList, ok := actualResponse["pr_list"].([]any); ok {
			prs := make([]PullRequestInfo, 0, len(prList))
			for _, pr := range prList {
				if prMap, ok := pr.(map[string]any); ok {
					prInfo := PullRequestInfo{}

					if number, ok := prMap["number"].(float64); ok {
						prInfo.Number = int(number)
					}
					if title, ok := prMap["title"].(string); ok {
						prInfo.Title = title
					}
					if author, ok := prMap["author"].(string); ok {
						prInfo.Author = author
					}
					if url, ok := prMap["url"].(string); ok {
						prInfo.URL = url
					}
					if state, ok := prMap["state"].(string); ok {
						prInfo.State = state
					}
					if createdAt, ok := prMap["created_at"].(string); ok {
						prInfo.CreatedAt = createdAt
					}
					if mergedAt, ok := prMap["merged_at"].(string); ok {
						prInfo.MergedAt = mergedAt
					}

					prs = append(prs, prInfo)
				}
			}
			response.PRList = prs
		}
	}

	if actualResponse["automated_fix_pr_info"] != nil {
		if prMap, ok := actualResponse["automated_fix_pr_info"].(map[string]any); ok {
			prInfo := PullRequestInfo{}
			if number, ok := prMap["number"].(float64); ok {
				prInfo.Number = int(number)
			}
			if title, ok := prMap["title"].(string); ok {
				prInfo.Title = title
			}
			if author, ok := prMap["author"].(string); ok {
				prInfo.Author = author
			}
			if url, ok := prMap["url"].(string); ok {
				prInfo.URL = url
			}
			if state, ok := prMap["state"].(string); ok {
				prInfo.State = state
			}
			if createdAt, ok := prMap["created_at"].(string); ok {
				prInfo.CreatedAt = createdAt
			}
			if mergedAt, ok := prMap["merged_at"].(string); ok {
				prInfo.MergedAt = mergedAt
			}
			response.AutomatedFixPR = prInfo
			// Resolution row is now created centrally by agent_code_2 (trackPRInResolution)
			// with full PR metadata needed for automated follow-up.
		}
	}

	// Handle file details - support both old and new formats
	if actualResponse["files"] != nil {
		if files, ok := actualResponse["files"].([]any); ok {
			fileDetails := make([]EventLogFileDetail, 0, len(files))
			for _, f := range files {
				if fileMap, ok := f.(map[string]any); ok {
					detail := EventLogFileDetail{}
					if path, ok := fileMap["file_path"].(string); ok {
						detail.FilePath = path
					}
					if name, ok := fileMap["file_name"].(string); ok {
						detail.Filename = name
					}
					if lineNum, ok := fileMap["line_number"].(float64); ok {
						detail.LineNumber = int(lineNum)
					}
					fileDetails = append(fileDetails, detail)
				}
			}
			response.FileDetails = EventLogFileDetails{
				Files: fileDetails,
			}
		}
	} else if actualResponse["file_path"] != nil {
		// Handle single file from new format
		detail := EventLogFileDetail{}
		if path, ok := actualResponse["file_path"].(string); ok {
			detail.FilePath = path
			response.SourceUpdates["file_path"] = path
		}
		if lineNum, ok := actualResponse["line_number"].(float64); ok {
			detail.LineNumber = int(lineNum)
		}
		response.FileDetails = EventLogFileDetails{
			Files: []EventLogFileDetail{detail},
		}
	}
	if actualResponse["source_updates"] != nil {
		if sourceUpdates, ok := actualResponse["source_updates"].(map[string]any); ok {
			response.SourceUpdates = sourceUpdates
		} else {
			ctx.GetLogger().Warn("analyzer: source_updates is not a map[string]any", "type", fmt.Sprintf("%T", actualResponse["source_updates"]))
		}
	}

	// Handle source_details (keep existing source_details only)
	if actualResponse["source_details"] != nil {
		if sourceDetails, ok := actualResponse["source_details"].(map[string]any); ok {
			response.SourceDetails = sourceDetails
		}
	}

	// Parse pipeline status fields
	if v, ok := actualResponse["root_cause_analysis"].(string); ok {
		response.RootCauseAnalysis = v
	}
	if v, ok := actualResponse["confidence_score"].(string); ok {
		response.ConfidenceScore = v
	}
	if v, ok := actualResponse["execution_status"].(string); ok {
		response.ExecutionStatus = v
	}
	if v, ok := actualResponse["execution_summary"].(string); ok {
		response.ExecutionSummary = v
	}
	if v, ok := actualResponse["pr_creation_status"].(string); ok {
		response.PRCreationStatus = v
	}
	if v, ok := actualResponse["pr_creation_reason"].(string); ok {
		response.PRCreationReason = v
	}
	if v, ok := actualResponse["failure_summary"].(string); ok {
		response.FailureSummary = v
	}
	if v := actualResponse["files_modified"]; v != nil {
		response.FilesModified = v
	}
	if v := actualResponse["verification_passed"]; v != nil {
		response.VerificationPassed = v
	}
	if v, ok := actualResponse["review"].(map[string]any); ok {
		response.Review = v
	}
	if v, ok := actualResponse["build_verification"].(map[string]any); ok {
		response.BuildVerification = v
	}

	return response, nil
}

func parseGitCommits(actualResponse map[string]any) []CommitInfo {
	var commitInfos []CommitInfo
	if actualResponse["commits"] != nil {
		if commits, ok := actualResponse["commits"].([]any); ok {
			commitInfos = make([]CommitInfo, 0, len(commits))
			for _, c := range commits {
				commitInfo := CommitInfo{}
				if commitMap, ok := c.(map[string]any); ok {

					if hash, ok := commitMap["hash"].(string); ok {
						commitInfo.Hash = hash
					}
					if author, ok := commitMap["author"].(string); ok {
						commitInfo.Author = author
					}
					if date, ok := commitMap["date"].(string); ok {
						commitInfo.Date = date
					}
					if message, ok := commitMap["message"].(string); ok {
						commitInfo.Message = message
					}
					if changes, ok := commitMap["changes"].(string); ok {
						commitInfo.Changes = changes
					}

					commitInfos = append(commitInfos, commitInfo)
				}
			}
		}
	}

	return commitInfos
}

func parseEventLabels(labelsStr string) map[string]any {
	result := map[string]any{}
	if labelsStr == "" {
		return result
	}
	if err := common.UnmarshalJson([]byte(labelsStr), &result); err != nil {
		// If unmarshaling as a map fails, try unmarshaling as an array of maps
		var list []map[string]any
		if err2 := common.UnmarshalJson([]byte(labelsStr), &list); err2 == nil {
			for _, m := range list {
				for k, v := range m {
					result[k] = v
				}
			}
			return result
		}
		slog.Error("analyzer: failed to parse event labels", "error", err, "labels", labelsStr)
	}

	return result
}

func getEventData(ctx *security.RequestContext, request EventAnalysisRequest) (events.Event, error) {
	ctx.GetLogger().Info("analyzer: executing Event Log Analysis", "request", request)

	// Validate UUIDs to prevent SQL injection
	_, err := uuid.Parse(request.EventId)
	if err != nil {
		return events.Event{}, fmt.Errorf("invalid event_id format")
	}
	_, err = uuid.Parse(request.AccountId)
	if err != nil {
		return events.Event{}, fmt.Errorf("invalid account_id format")
	}

	eventTool := tools.EventsExecuteTool{}
	toolCtx := toolcore.NewNbToolContext(ctx, eventTool, request.AccountId, request.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), "", []llms.MessageContent{}, "", toolcore.NBQueryConfig{}, "")
	data, err := eventTool.Call(toolCtx, toolcore.NBToolCallRequest{
		Command: fmt.Sprintf(`select * from events where id = '%s' and cloud_account_id = '%s'`, request.EventId, request.AccountId),
	})
	if err != nil {
		return events.Event{}, err
	}
	var event []events.Event
	err = common.UnmarshalJson([]byte(data.Data), &event)
	if err != nil {
		return events.Event{}, err
	}
	if len(event) == 0 {
		return events.Event{}, errors.New("event not found")
	}
	return event[0], nil
}

// markIncompleteAnalysisFailed marks only IN_PROGRESS analysis types as FAILED
// to prevent stuck state, without overwriting already-completed results.
func markAllAnalysisFailed(ctx *security.RequestContext, repo *events.EventAnalysisRepository, fingerprint, accountId, aggKey, errMsg string) {
	// Must mark all four analysis types — the all-types-COMPLETED gate added in
	// #29838 (getOrCreateEventAnalysisStatus) and the duplicate-dispatch defense
	// in #29472 both treat a non-COMPLETED row as "still running". Omitting
	// detailed_response here leaves it stuck IN_PROGRESS forever after a
	// failure, which keeps syncStuckEventAnalyses re-submitting the worker.
	for _, aType := range []events.EventAnalysisType{
		events.AnalysisTypeSummary,
		events.AnalysisTypeInvestigation,
		events.AnalysisTypeLog,
		events.AnalysisTypeDetailedResponse,
	} {
		existing, err := repo.GetEventAnalysis(ctx, fingerprint, aggKey, accountId, aType)
		if err != nil || existing == nil {
			continue
		}
		if existing.Status == string(events.AnalysisStatusCompleted) {
			continue
		}
		if updateErr := repo.UpdateEventAnalysisStatus(ctx, fingerprint, accountId, aggKey, string(events.AnalysisStatusFailed), "analysis failed - "+errMsg, aType); updateErr != nil {
			ctx.GetLogger().Warn("failed to update analysis status on error", "error", updateErr, "analysis_type", aType)
		}
	}
}

// synthesizeDetailedResponse calls the LLM directly to produce a consolidated markdown analysis
// from the initial summary, investigation findings, and log analysis. It is a soft step — callers
// should fall back to the initial summary on failure rather than failing the whole analysis.
func synthesizeDetailedResponse(ctx *security.RequestContext, request EventAnalysisRequest, parentSessionId string, summary, investigation, logAnalysis string) (string, error) {
	if summary == "" {
		return "", errors.New("synthesizeDetailedResponse: summary is empty")
	}

	// Short-circuit: if there is nothing to enrich beyond the initial summary, return it directly
	// to avoid an unnecessary LLM call (latency + cost).
	if investigation == "" && logAnalysis == "" {
		return summary, nil
	}

	// Resolve the conversation UUID from the parent session so token usage can be
	// tracked with valid FK references. The conversation was already created during
	// Steps 1-2 (summary / investigation).
	if parentSessionId == "" {
		return "", errors.New("synthesizeDetailedResponse: parentSessionId is empty")
	}
	conv, err := core.GetConversationDao().GetConversationBySession(request.AccountId, parentSessionId)
	if err != nil {
		return "", fmt.Errorf("synthesizeDetailedResponse: unable to resolve conversation from session %s: %w", parentSessionId, err)
	}
	if conv.ID == uuid.Nil {
		return "", fmt.Errorf("synthesizeDetailedResponse: conversation not found for session %s", parentSessionId)
	}
	conversationId := conv.ID.String()

	// Create a message record so the token usage FK to llm_conversation_messages is valid.
	messageId, err := core.GetConversationDao().SaveConversationMessage(
		"", conversationId, request.AccountId, request.UserId,
		core.MessageRoleHuman, core.MessageTypeGeneration,
		"synthesize detailed response", "", "event_detailed_response",
		uuid.Nil, nil, "", "", "",
	)
	if err != nil {
		return "", fmt.Errorf("synthesizeDetailedResponse: unable to create message record: %w", err)
	}

	systemPrompt := prompts_repo.GetPrompt(prompts_repo.PromptEventDetailedResponseSynthesis)

	userPrompt := fmt.Sprintf("## Event Summary\n%s", summary)
	if investigation != "" {
		userPrompt += fmt.Sprintf("\n\n## Investigation Findings\n%s", investigation)
	}
	if logAnalysis != "" {
		userPrompt += fmt.Sprintf("\n\n## Log Analysis\n%s", logAnalysis)
	}
	userPrompt += "\n\nProduce a consolidated markdown analysis combining all of the above."

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, userPrompt),
	}

	completion, llmErr := core.GenerateAndTrackLLMContent(
		ctx,
		request.UserId,
		request.AccountId,
		conversationId,
		messageId.String(),
		"event_detailed_response",
		false,
		messages,
		false,
	)

	// Update message status regardless of outcome
	if llmErr != nil {
		_ = core.GetConversationDao().UpdateConversationMessage(messageId.String(), "", core.ConversationStatusFailed)
		return "", fmt.Errorf("synthesizeDetailedResponse: LLM call failed: %w", llmErr)
	}

	responseText := ""
	if len(completion.Choices) > 0 {
		responseText = completion.Choices[0].Content
	}

	if responseText == "" {
		if err := core.GetConversationDao().UpdateConversationMessage(messageId.String(), "", core.ConversationStatusFailed); err != nil {
			ctx.GetLogger().Warn("synthesizeDetailedResponse: failed to update message status to failed", "error", err)
		}
		return "", errors.New("synthesizeDetailedResponse: empty response from LLM")
	}

	if err := core.GetConversationDao().UpdateConversationMessage(messageId.String(), responseText, core.ConversationStatusCompleted); err != nil {
		ctx.GetLogger().Warn("synthesizeDetailedResponse: failed to update message status to completed", "error", err)
	}

	return responseText, nil
}
