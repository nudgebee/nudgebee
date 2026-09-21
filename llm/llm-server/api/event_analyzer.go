package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	_ "nudgebee/llm/agents/signoz"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"regexp"
	"sort"
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
	LegacyAnalysisID string `json:"-"`
	AttemptID        string `json:"-" mapstructure:"-"` // Internal claim identity; never accepted from HTTP.
	EventId          string `json:"event_id" mapstructure:"required" validate:"required"`
	AccountId        string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId           string `json:"user_id"`
	Regenerate       bool   `json:"regenerate"`
	Generate         bool   `json:"generate"`
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
	// DefaultFormat is always the built-in template, regardless of whether the
	// account has a custom format stored. The settings editor uses it to offer
	// "start from the Nudgebee default" without shipping its own copy of the text.
	DefaultFormat string `json:"default_format"`
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
	StatusReason string `json:"status_reason,omitempty"`
	// GeneratedAt is the newest write timestamp among the rows assembled into
	// this response. Nil when no stored row exists yet.
	// A pointer rather than a bare time.Time because `omitempty` does not omit
	// a zero struct, which would serialize as year 0001.
	GeneratedAt         *time.Time                `json:"generated_at,omitempty"`
	RCAVersions         []events.RCAReportVersion `json:"rca_versions,omitempty"`
	RCAReportID         string                    `json:"rca_report_id,omitempty"`
	AttemptedAt         *time.Time                `json:"attempted_at,omitempty"`
	AnalysisVersions    []EventAnalysisVersion    `json:"analysis_versions,omitempty"`
	TaskStatuses        map[string]string         `json:"task_statuses,omitempty"`
	CodeAnalysisEnabled bool                      `json:"code_analysis_enabled"`
	RcaEnabled          bool                      `json:"rca_enabled"`
	// Outdated is set on a COMPLETED RCA response when any of its input rows
	// (summary, investigation, log analysis) was updated after the RCA report
	// was generated — i.e. the report no longer reflects the latest findings.
	Outdated bool `json:"outdated,omitempty"`
	// FormatSource reports which RCA format level would apply for this event:
	// "rule" (rca_format annotation), "account" (settings editor) or "default".
	FormatSource  string              `json:"format_source,omitempty"`
	FileDetails   EventLogFileDetails `json:"file_details"`
	SourceDetails map[string]any      `json:"source_details"`
	SourceUpdates map[string]any      `json:"source_updates"`

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

// EventAnalysisVersion is one complete historical incident-analysis response
// assembled from the existing event_log_analysis rows.
type EventAnalysisVersion struct {
	ID          string          `json:"id"`
	EventID     string          `json:"event_id"`
	Status      string          `json:"status"`
	GeneratedAt time.Time       `json:"generated_at"`
	Data        json.RawMessage `json:"data,omitempty"`
}

func attachEventAnalysisVersions(ctx *security.RequestContext, repo *events.EventAnalysisRepository, accountID string, response *EventAnalysisResponse) {
	storedVersions, err := repo.ListCompletedEventAnalysisVersions(ctx, response.EventFingerprint, accountID, response.EventAggregationKey)
	if err != nil || len(storedVersions) == 0 {
		return
	}
	response.AnalysisVersions = make([]EventAnalysisVersion, 0, len(storedVersions))
	for _, version := range storedVersions {
		analysisVersion, buildErr := buildStoredEventAnalysisVersion(response, version)
		if buildErr != nil {
			ctx.GetLogger().Warn("analyzer: failed to marshal mapped analysis version", "error", buildErr, "event_id", version.EventID)
			continue
		}
		response.AnalysisVersions = append(response.AnalysisVersions, analysisVersion)
	}
}

func buildStoredEventAnalysisVersion(current *EventAnalysisResponse, version events.EventAnalysisEventVersion) (EventAnalysisVersion, error) {
	mappedResponse := EventAnalysisResponse{}
	if version.Analysis != "" {
		// Log-analysis rows already contain the structured metadata used by the
		// complete incident view. Hydrate it without calling the live generation
		// endpoint, which could regenerate historical events.
		_ = json.Unmarshal([]byte(version.Analysis), &mappedResponse)
	}
	mappedResponse.EventId = version.EventID
	mappedResponse.RelatedEventId = version.RelatedEventID
	mappedResponse.EventFingerprint = current.EventFingerprint
	mappedResponse.EventAggregationKey = current.EventAggregationKey
	if mappedResponse.Analysis == "" {
		mappedResponse.Analysis = version.Analysis
	}
	mappedResponse.Summary = version.Summary
	mappedResponse.Investigation = version.Investigation
	mappedResponse.DetailedResponse = version.DetailedResponse
	mappedResponse.Status = string(events.AnalysisStatusCompleted)
	mappedResponse.GeneratedAt = &version.GeneratedAt
	mappedResponse.AnalysisVersions = nil
	mappedResponse.TaskStatuses = map[string]string{
		string(events.AnalysisTypeSummary):          string(events.AnalysisStatusCompleted),
		string(events.AnalysisTypeInvestigation):    string(events.AnalysisStatusCompleted),
		string(events.AnalysisTypeLog):              string(events.AnalysisStatusCompleted),
		string(events.AnalysisTypeDetailedResponse): string(events.AnalysisStatusCompleted),
	}
	payload, err := json.Marshal(mappedResponse)
	if err != nil {
		return EventAnalysisVersion{}, err
	}
	return EventAnalysisVersion{
		ID:      fmt.Sprintf("event-%s-%d", version.EventID, version.VersionRank),
		EventID: version.EventID, Status: string(events.AnalysisStatusCompleted), GeneratedAt: version.GeneratedAt, Data: payload,
	}, nil
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

	groupV2.POST("/remediation/generate", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("remediation_generate")
		processRemediationGenerate(c, tracer, meter)
	})

	groupV2.POST("/remediation/get", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("remediation_get")
		processRemediationGet(c, tracer, meter)
	})

	groupV2.POST("/remediation/execute", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("remediation_execute")
		processRemediationExecute(c, tracer, meter)
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
		if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!granted(context.GetSecurityContext(), request.AccountId, moduleAiRca, "Read", "Write") {
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

		if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!granted(context.GetSecurityContext(), request.AccountId, moduleAiRca, "Read", "Write") {
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
			Format:        format,
			IsDefault:     isDefault,
			DefaultFormat: agents.DefaultRCAFormat,
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
		if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) &&
			!grantedWrite(context.GetSecurityContext(), request.AccountId, moduleAiRca) {
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
			Format:        responseFormat,
			IsDefault:     isDefault,
			DefaultFormat: agents.DefaultRCAFormat,
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
		slog.Error("analyzer: userId, accountId and eventId are required", "user_id", request.UserId, "account_id", request.AccountId, "event_id", request.EventId)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{
				Message: "userId, accountId and eventId are required",
			},
		}))
		return
	}

	// Check if user has access to account
	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!granted(context.GetSecurityContext(), request.AccountId, moduleAiGeneration, "Read", "Write") {
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

// shouldAttributeToSystemUser reports whether an event analysis run must be
// attributed to the system user rather than the caller's own identity.
// regenerate is the one signal that reliably means a human asked for this
// (set only by the UI's "Regenerate" action, see RCACard.js). Everything
// else — first-time analyses auto-triggered on event ingestion, and any
// implicit re-trigger, e.g. a plain page view silently retrying a previously
// failed analysis that produced no output — is automated, not user-driven,
// and must not get tagged with whichever operator's session happened to be
// open when it ran (#35805).
func shouldAttributeToSystemUser(existingAnalysis *events.EventAnalysis, regenerate bool) bool {
	return existingAnalysis == nil || !regenerate
}

// isLiveFailure reports whether an existing analysis is a FAILED verdict still
// inside the freshness bound, and therefore worth serving to the caller as-is.
//
// Freshness-gated for the same reason the COMPLETED check is. A FAILED row past
// the bound is a verdict nobody should still be shown: ungated, the `anyFailed`
// early return in executeEventInvestigation fires on every subsequent request
// forever, and the only way to clear a stale failure is an explicit regenerate.
//
// The retry this enables cannot run hot. Every writer that records a failure
// sets updated_at=NOW() (event_analyzer_repository.go:676, :689, :733), so a
// re-failed run immediately reads as fresh and the attempt after it has to wait
// out the whole window.
func isLiveFailure(repo *events.EventAnalysisRepository, analysis *events.EventAnalysis) bool {
	return analysis != nil &&
		analysis.Status == string(events.AnalysisStatusFailed) &&
		!repo.IsAnalysisStale(analysis.UpdatedAt)
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
		existingAnalysis, err := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId, aType)
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
	// Newest completed stage's write time; the post-loop staleness check ages
	// it against the event's created_at.
	var latestAnalysisAt time.Time

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
			if existingAnalysis.UpdatedAt.After(latestAnalysisAt) {
				latestAnalysisAt = existingAnalysis.UpdatedAt
			}
			// #37005: the code-fix stage (log_analysis) is excluded from the
			// failure rollup — a declined or failed code fix is not a failure of
			// the investigation itself; the summary and root cause can be complete
			// and correct. Its status still lands in TaskStatuses above.
			if aType != events.AnalysisTypeLog && isLiveFailure(eventAnalysisRepo, existingAnalysis) {
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
	if !latestAnalysisAt.IsZero() {
		generatedAt := latestAnalysisAt
		finalResponse.GeneratedAt = &generatedAt
	}

	// One post-loop check on the newest stage (not per-stage in the loop) so it
	// stays identical to the events-list "View Analysis" test in
	// services/query is_investigated: completion_ts >= event.created_at - window.
	if allCompleted && eventAnalysisRepo.IsAnalysisStaleForEvent(latestAnalysisAt, eventInfo.CreatedAt) {
		allCompleted = false
	}

	if anyStarted && !request.Regenerate {
		if allCompleted {
			finalResponse.Status = string(events.AnalysisStatusCompleted)
			attachEventAnalysisVersions(ctx, eventAnalysisRepo, request.AccountId, &finalResponse)
			c.JSON(200, buildApiResponse(finalResponse, nil))
			return
		} else if anyInProgress {
			finalResponse.Status = string(events.AnalysisStatusInProgress)
			attachEventAnalysisVersions(ctx, eventAnalysisRepo, request.AccountId, &finalResponse)
			c.JSON(200, buildApiResponse(finalResponse, nil))
			return
		} else if anyFailed {
			finalResponse.Status = string(events.AnalysisStatusFailed)
			attachEventAnalysisVersions(ctx, eventAnalysisRepo, request.AccountId, &finalResponse)
			c.JSON(200, buildApiResponse(finalResponse, nil))
			return
		}
	}

	// Check budget limits
	if budget.CheckBudgetAndRespond(c, ctx.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleInvestigation, ctx.GetLogger()) {
		return
	}

	// Atomically claim log_analysis as the lead row before dispatching. This is
	// the same concurrency gate getOrCreateEventAnalysisStatus uses for the MQ
	// path: the HTTP endpoint and the MQ consumers can fire for one event in the
	// same window, all having read a non-terminal status above. Only the winner of
	// the atomic claim dispatches the pipeline; a lost claim reports IN_PROGRESS so
	// the caller doesn't run a duplicate summary → investigation → log →
	// detailed-response cycle (the observed duplicate-task bug).
	//
	// Partial-completion carve-out: when log_analysis is already COMPLETED and fresh
	// but the pipeline is partial (reached here because some other type is not COMPLETED,
	// and not regenerating), skip the claim gate and dispatch. Leaving fresh log_analysis
	// COMPLETED lets Step 3's cache skip it and avoids the redundant log + synthesis
	// re-run that resetting it would force. (The mark-remaining loop below still
	// resets the other types to IN_PROGRESS, matching the pre-existing HTTP behavior
	// of re-running summary/investigation/detailed_response on a partial state; only
	// log_analysis is preserved.)
	logCompleted := dbAnalyses[events.AnalysisTypeLog] != nil &&
		dbAnalyses[events.AnalysisTypeLog].Status == string(events.AnalysisStatusCompleted) &&
		!eventAnalysisRepo.IsAnalysisStale(dbAnalyses[events.AnalysisTypeLog].UpdatedAt)
	claimedLog := false
	if request.Regenerate || !logCompleted {
		claimed, claimErr := eventAnalysisRepo.ClaimEventAnalysis(ctx, request.EventId, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, events.AnalysisTypeLog, request.Regenerate)
		if claimErr != nil {
			// Fail closed: a claim DB error means we can't tell whether we own
			// the run, and subsequent DB writes would fail too. Return 500 like
			// the other DB-error paths in this handler rather than reporting a
			// misleading IN_PROGRESS.
			ctx.GetLogger().Error("analyzer: failed to claim analysis", "error", claimErr, "event_id", request.EventId)
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: "analyzer: failed to claim analysis: " + claimErr.Error()}}))
			return
		}
		if !claimed {
			ctx.GetLogger().Info("analyzer: lost analysis claim, another dispatcher owns this run", "event_id", request.EventId)
			finalResponse.Status = string(events.AnalysisStatusInProgress)
			for _, aType := range analysisTypes {
				finalResponse.TaskStatuses[string(aType)] = string(events.AnalysisStatusInProgress)
			}
			attachEventAnalysisVersions(ctx, eventAnalysisRepo, request.AccountId, &finalResponse)
			c.JSON(200, buildApiResponse(finalResponse, nil))
			return
		}
		claimedLog = true
	}

	// Dispatching. Mark every type IN_PROGRESS so the UI reflects a running
	// pipeline. log_analysis is IN_PROGRESS only when we actually claimed/reset
	// it above; in the preserved-COMPLETED partial case it stays COMPLETED, so
	// reporting IN_PROGRESS here would flicker back to COMPLETED on the next poll.
	for _, aType := range analysisTypes {
		if aType == events.AnalysisTypeLog {
			if claimedLog {
				finalResponse.TaskStatuses[string(aType)] = string(events.AnalysisStatusInProgress)
			}
			continue
		}
		if err := eventAnalysisRepo.UpsertEventAnalysisInProgress(ctx, request.EventId, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, aType); err != nil {
			ctx.GetLogger().Warn("failed to upsert event analysis in progress", "error", err, "analysis_type", aType)
		}
		finalResponse.TaskStatuses[string(aType)] = string(events.AnalysisStatusInProgress)
	}

	finalResponse.Status = string(events.AnalysisStatusInProgress)
	attachEventAnalysisVersions(ctx, eventAnalysisRepo, request.AccountId, &finalResponse)

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
				markAllAnalysisFailed(newCtx, events.NewEventAnalysisRepository(newDbManager), request.EventId, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, err.Error())
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

	existingAnalysis, err := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId, events.AnalysisTypeLog)
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
	if !request.Regenerate && allEventAnalysisTypesCompleted(ctx, eventAnalysisRepo, request.EventId, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId) {
		ctx.GetLogger().Debug("analyzer: returning existing completed analysis", "analysis", slog.AnyValue(response.Analysis), "event_id", request.EventId)
		response.Status = string(core.ConversationStatusCompleted)
		if response.Analysis != "" {
			var logResp EventAnalysisResponse
			if err = common.UnmarshalJson([]byte(response.Analysis), &logResp); err != nil {
				ctx.GetLogger().Warn("analyzer: failed to unmarshal analysis from database", "error", err, "event_id", request.EventId)
			} else {
				copyLogAnalysisFields(&response, logResp)
			}
			if response.Status == "" {
				response.Status = string(core.ConversationStatusCompleted)
			}
		}
		populateCompletedEventAnalysis(ctx, eventAnalysisRepo, request.EventId, eventInfo.Fingerprint, eventInfo.AggregationKey, request.AccountId, &response)
		return response, nil
	}

	if (strings.EqualFold(response.Status, string(core.ConversationStatusWaiting)) || strings.EqualFold(response.Status, string(core.ConversationStatusWaitingForClientTool))) && !request.Regenerate {
		ctx.GetLogger().Debug("analyzer: returning existing analysis from database", "analysis", slog.AnyValue(response.Analysis), "status", response.Status, "event_id", request.EventId)
		return response, nil
	}

	if createAnalysis {
		// Atomically claim log_analysis as the pipeline's lead row. This is the
		// concurrency gate: multiple dispatchers (the two MQ consumers, the HTTP
		// endpoint, sync-recovery) can reach this point for the same event in the
		// same window, having all read a non-terminal status above. ClaimEventAnalysis
		// transitions to IN_PROGRESS in a single statement and reports whether THIS
		// caller won; a lost claim means another dispatcher already owns the run, so
		// we report IN_PROGRESS and let the caller skip its own pipeline instead of
		// running a duplicate summary → investigation → log → detailed-response cycle.
		//
		// A COMPLETED row is never re-claimed (ClaimEventAnalysis's WHERE excludes it),
		// so completed log analysis keeps its cached result and the per-step caches in
		// analyzeEventUsingAgentsAndUpdateDb still skip finished steps and only re-run
		// what's missing.
		// Partial-completion carve-out (non-regenerate only): when log_analysis is
		// already COMPLETED but the overall pipeline is not (e.g. detailed_response
		// missing after a Step-4 crash), do NOT reset it to IN_PROGRESS — that would
		// force Step 3 to re-run log analysis and unconditionally re-run Step 4
		// synthesis. Proceed to dispatch instead; the per-step caches in
		// analyzeEventUsingAgentsAndUpdateDb skip the completed steps and only re-run
		// what's missing. (This narrow path is not atomically gated, but the per-step
		// caches dedupe it; the fresh-event race below is the one that produced the
		// observed duplicate-pipeline bug.)
		// Stale rows do not qualify for the carve-out: preserving a COMPLETED
		// log_analysis is only worth doing while it is still worth reusing.
		if !request.Regenerate && existingAnalysis != nil && existingAnalysis.Status == string(events.AnalysisStatusCompleted) &&
			!eventAnalysisRepo.IsAnalysisStale(existingAnalysis.UpdatedAt) {
			response.Status = string(events.AnalysisStatusCreated)
			response.RelatedEventId = request.EventId
			return response, nil
		}
		claimed, err := eventAnalysisRepo.ClaimEventAnalysis(ctx, request.EventId, eventInfo.Fingerprint, request.AccountId, eventInfo.AggregationKey, events.AnalysisTypeLog, request.Regenerate)
		if err != nil {
			return EventAnalysisResponse{}, err
		}
		if !claimed {
			// Another dispatcher won the claim between our status read and here.
			ctx.GetLogger().Info("analyzer: lost analysis claim, another dispatcher owns this run", "event_id", request.EventId)
			response.Status = string(core.ConversationStatusInProgress)
			return response, nil
		}
		response.Status = string(events.AnalysisStatusCreated)
		response.RelatedEventId = request.EventId
	} else if strings.EqualFold(response.Status, string(events.AnalysisStatusCompleted)) {
		// When createAnalysis == false and full pipeline completion check returned false,
		// the lead log stage row may already be COMPLETED but downstream stages
		// (investigation or synthesis) are still running. Keep overall status IN_PROGRESS.
		response.Status = string(core.ConversationStatusInProgress)
	}
	return response, nil
}

// copyLogAnalysisFields transfers log investigation and code fix fields from
// a serialized log response into the target EventAnalysisResponse without
// overwriting identity (EventId, Fingerprint), stage, or status fields.
func copyLogAnalysisFields(target *EventAnalysisResponse, src EventAnalysisResponse) {
	if target == nil {
		return
	}
	target.FileDetails = src.FileDetails
	target.SourceDetails = src.SourceDetails
	target.SourceUpdates = src.SourceUpdates
	target.Title = src.Title
	target.Description = src.Description
	target.ErrorMessage = src.ErrorMessage
	target.OriginalCode = src.OriginalCode
	target.FixedCode = src.FixedCode
	target.GitDiff = src.GitDiff
	target.CommitHash = src.CommitHash
	target.Author = src.Author
	target.CommitDate = src.CommitDate
	target.PRList = src.PRList
	target.Commits = src.Commits
	target.AutomatedFixPR = src.AutomatedFixPR
}

// populateCompletedEventAnalysis enriches an EventAnalysisResponse with outputs
// from all four pipeline stages (summary, investigation, log, detailed_response)
// when an analysis has fully completed.
func populateCompletedEventAnalysis(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventId, fingerprint, aggKey, accountId string, response *EventAnalysisResponse) {
	if repo == nil || response == nil {
		return
	}
	// Decode log fields into an intermediate struct and copy specific fields so
	// identity, status, and authoritative stage outputs are never corrupted.
	logRow, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, events.AnalysisTypeLog)
	if err == nil && logRow != nil {
		if response.Analysis == "" && logRow.Analysis != "" {
			response.Analysis = logRow.Analysis
			var logResp EventAnalysisResponse
			if err := common.UnmarshalJson([]byte(logRow.Analysis), &logResp); err == nil {
				copyLogAnalysisFields(response, logResp)
			}
		}
		if response.Summary == "" {
			response.Summary = logRow.Summary
		}
		if logRow.RelatedEventId != "" {
			response.RelatedEventId = logRow.RelatedEventId
		}
	}

	summaryRow, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, events.AnalysisTypeSummary)
	if err == nil && summaryRow != nil && summaryRow.Summary != "" {
		response.Summary = summaryRow.Summary
	}
	invRow, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, events.AnalysisTypeInvestigation)
	if err == nil && invRow != nil && invRow.Summary != "" {
		response.Investigation = invRow.Summary
	}
	drRow, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, events.AnalysisTypeDetailedResponse)
	if err == nil && drRow != nil && drRow.Summary != "" {
		response.DetailedResponse = drRow.Summary
	}
	if response.DetailedResponse == "" {
		response.DetailedResponse = response.Summary
	}
}

// getConfirmedTerminalAnalysis verifies whether an event analysis has reached a
// confirmed terminal state (COMPLETED across all 4 stages, or FAILED).
//
// For COMPLETED: requires confirmed completion and freshness of all four stages
// (summary, investigation, log, detailed_response) via allEventAnalysisTypesCompleted,
// and loads the full persisted report (including DetailedResponse).
//
// For FAILED: returns true if any stage is confirmed FAILED and no stage is
// IN_PROGRESS or WAITING.
//
// Returns (response, true) if confirmed terminal, or (EventAnalysisResponse{}, false) otherwise.
func getConfirmedTerminalAnalysis(ctx *security.RequestContext, request EventAnalysisRequest, dbManager *common.DatabaseManager) (EventAnalysisResponse, bool) {
	if request.EventId == "" || request.AccountId == "" || dbManager == nil {
		return EventAnalysisResponse{}, false
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	eventInfo, err := repo.GetEventInfo(ctx, request.EventId, request.AccountId)
	if err != nil {
		return EventAnalysisResponse{}, false
	}
	fp := eventInfo.Fingerprint
	if fp == "" {
		fp = request.EventId
	}
	aggKey := eventInfo.AggregationKey

	if allEventAnalysisTypesCompleted(ctx, repo, request.EventId, fp, aggKey, request.AccountId) {
		resp := EventAnalysisResponse{
			EventId:             request.EventId,
			RelatedEventId:      request.EventId,
			EventFingerprint:    fp,
			EventAggregationKey: aggKey,
			Status:              string(events.AnalysisStatusCompleted),
		}
		populateCompletedEventAnalysis(ctx, repo, request.EventId, fp, aggKey, request.AccountId, &resp)
		resp.Status = string(events.AnalysisStatusCompleted)
		return resp, true
	}

	analysisTypes := []events.EventAnalysisType{
		events.AnalysisTypeLog,
		events.AnalysisTypeSummary,
		events.AnalysisTypeInvestigation,
		events.AnalysisTypeDetailedResponse,
	}
	var failedReason string
	hasFailed := false
	for _, aType := range analysisTypes {
		row, err := repo.GetEventAnalysis(ctx, request.EventId, fp, aggKey, request.AccountId, aType)
		if err != nil {
			// DB read error means stage status cannot be verified. Return false
			// to prevent falsely confirming a terminal failure.
			return EventAnalysisResponse{}, false
		}
		if row != nil {
			if strings.EqualFold(row.Status, string(events.AnalysisStatusInProgress)) ||
				strings.EqualFold(row.Status, string(core.ConversationStatusWaiting)) ||
				strings.EqualFold(row.Status, string(core.ConversationStatusWaitingForClientTool)) {
				return EventAnalysisResponse{}, false
			}
			if strings.EqualFold(row.Status, string(events.AnalysisStatusFailed)) {
				hasFailed = true
				if failedReason == "" {
					failedReason = row.StatusReason
				}
			}
		}
	}
	if hasFailed {
		return EventAnalysisResponse{
			EventId:             request.EventId,
			RelatedEventId:      request.EventId,
			EventFingerprint:    fp,
			EventAggregationKey: aggKey,
			Status:              string(events.AnalysisStatusFailed),
			StatusReason:        failedReason,
		}, true
	}

	return EventAnalysisResponse{}, false
}

// allEventAnalysisTypesCompleted returns true when every analysis type for an
// event (summary, investigation, log, detailed_response) is in COMPLETED state
// and recent enough to reuse. A DB read error, any non-COMPLETED row, or a stage
// past the freshness bound is treated as "not completed" — the safe fallback that
// lets the pipeline proceed rather than incorrectly skipping.
// This mirrors the defense-in-depth check inside analyzeEventUsingAgentsAndUpdateDb.
//
// This is the gate that actually decides reuse for the common case: a new event
// whose fingerprint is already fully analysed. It returns before the claim path,
// so without the staleness term here the freshness bound is never consulted for
// exactly the events it exists to protect.
func allEventAnalysisTypesCompleted(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventId, fingerprint, aggKey, accountId string) bool {
	for _, aType := range []events.EventAnalysisType{
		events.AnalysisTypeSummary,
		events.AnalysisTypeInvestigation,
		events.AnalysisTypeLog,
		events.AnalysisTypeDetailedResponse,
	} {
		analysis, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, aType)
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to read analysis type for completion check, treating as incomplete", "error", err, "analysis_type", aType, "event_id", eventId, "fingerprint", fingerprint)
			return false
		}
		if analysis == nil || analysis.Status != string(events.AnalysisStatusCompleted) ||
			repo.IsAnalysisStale(analysis.UpdatedAt) {
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
	return latestAgentGenerationResponse(messages, agentName)
}

// latestAgentGenerationResponse returns the latest generation's answer only
// when that generation completed with content. Messages are ordered oldest
// first by ListConversationMessages. Never skip a newer unfinished/failed
// generation to recover an older answer for the same agent (#37865).
// Only `generation` rows carry an agent answer: a `followup` row is
// the tool-approval prompt the agent raised, and its Response column holds the
// *user's* reply ("yes"/"no"), not analysis. Because the followup row is created
// after the generation row it answers, an unfiltered backwards scan picks it
// first — that is how event a1ffed9c stored "yes" as its whole investigation and
// synthesised a "root cause undetermined" report on top of a completed RCA.
func latestAgentGenerationResponse(messages []core.ConversationMessage, agentName string) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.MessageType != string(core.MessageTypeGeneration) {
			continue
		}
		if msg.AgentName == nil || *msg.AgentName != agentName {
			continue
		}
		if msg.Status != core.ConversationStatusCompleted || strings.TrimSpace(msg.Response) == "" {
			return "", false
		}
		return msg.Response, true
	}
	return "", false
}

// shouldRecoverStageFromConversation limits conversation-history recovery to
// interrupted writes and still-fresh stages. Once the analysis row is stale,
// its agent response is stale too: recovering it would refresh the database
// timestamp without collecting current evidence, while only synthesis reruns.
func shouldRecoverStageFromConversation(repo *events.EventAnalysisRepository, analysis *events.EventAnalysis, regenerate bool) bool {
	if regenerate {
		return false
	}
	if analysis == nil {
		return true
	}
	// An IN_PROGRESS row is actively awaiting completion/recovery; its age reflects
	// the wait/approval duration, not obsolete completed evidence (#37865).
	if strings.EqualFold(analysis.Status, string(events.AnalysisStatusInProgress)) {
		return true
	}
	return !repo.IsAnalysisStale(analysis.UpdatedAt)
}

// stripRCAFormatScaffolding removes the template's scaffolding header — a
// leading `<<...>>` marker line plus an optional dashed separator — that models
// echo verbatim from the format block. Left in place, the marker renders as a
// stray `<>` heading in the UI (the inner `<...>` is sanitized away as an
// unknown HTML tag and the dashed line promotes the leftover to a heading).
func stripRCAFormatScaffolding(report string) string {
	trimmed := strings.TrimLeft(report, " \t\r\n")
	if !strings.HasPrefix(trimmed, "<<") {
		return report
	}
	firstLine, rest, found := strings.Cut(trimmed, "\n")
	if !found || !strings.HasSuffix(strings.TrimRight(firstLine, " \t\r"), ">>") {
		return report
	}
	rest = strings.TrimLeft(rest, " \t\r\n")
	// Drop a separator line made only of dashes, if present.
	if sepLine, afterSep, sepFound := strings.Cut(rest, "\n"); sepFound {
		sep := strings.TrimRight(sepLine, " \t\r")
		if len(sep) >= 3 && strings.Count(sep, "-") == len(sep) {
			rest = strings.TrimLeft(afterSep, " \t\r\n")
		}
	}
	return rest
}

// enrichRCAResponseMetadata fills the RCA-only response fields: whether the
// completed report is stale relative to its inputs, and which format level
// (rule / account / default) applies to this event.
func enrichRCAResponseMetadata(ctx *security.RequestContext, repo *events.EventAnalysisRepository, fingerprint, aggKey, accountId string, rcaUpdatedAt time.Time, response *EventAnalysisResponse) {
	if !rcaUpdatedAt.IsZero() {
		inputTypes := []events.EventAnalysisType{events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeLog}
		latestInput, err := repo.GetLatestAnalysisUpdatedAt(ctx, fingerprint, aggKey, accountId, inputTypes)
		if err != nil {
			ctx.GetLogger().Warn("analyzer: unable to check RCA staleness", "error", err)
		} else if latestInput.After(rcaUpdatedAt) {
			response.Outdated = true
		}
	}

	response.FormatSource = "default"
	if accountFormat, err := repo.GetAccountRCAFormat(ctx, accountId); err == nil && accountFormat != "" {
		response.FormatSource = "account"
	}
	if _, annotations, err := repo.GetEventRuleDefinition(ctx, accountId, aggKey); err == nil && annotations != nil {
		if format, ok := annotations["rca_format"].(string); ok && format != "" && len(format) <= maxAnnotationRCAFormatBytes {
			response.FormatSource = "rule"
		}
	}
}

func analyzeEventRCAUsingAgentsAndUpdateDb(ctx *security.RequestContext, request EventRCAAnalysisRequest) (result EventAnalysisResponse, resultErr error) {
	dbManager, dbErr := common.GetDatabaseManager(common.Metastore)
	if dbErr != nil {
		ctx.GetLogger().Error("unable to get db manager for rca analysis", "error", dbErr, "event_id", request.EventId)
		return EventAnalysisResponse{}, dbErr
	}
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)
	defer func() {
		if request.AttemptID != "" && resultErr != nil {
			if err := eventAnalysisRepo.UpdateRCAAttemptStatus(ctx, request.AttemptID, request.EventId, request.AccountId, string(events.AnalysisStatusFailed), "RCA could not complete. Please retry."); err != nil {
				ctx.GetLogger().Error("failed to record RCA attempt failure", "error", err)
			}
		}
	}()
	updateStatus := func(status, reason string) error {
		if request.AttemptID != "" {
			return eventAnalysisRepo.UpdateRCAAttemptStatus(ctx, request.AttemptID, request.EventId, request.AccountId, status, reason)
		}
		return eventAnalysisRepo.UpdateLegacyRCAStatus(ctx, request.LegacyAnalysisID, request.EventId, request.AccountId, status, reason)
	}

	eventRequest := EventAnalysisRequest{
		EventId:    request.EventId,
		AccountId:  request.AccountId,
		UserId:     request.UserId,
		Regenerate: request.Regenerate,
	}

	// Same gate as analyzeEventUsingAgentsAndUpdateDb: skip RCA compute and the
	// event load for accounts with event debug analysis disabled, marking any
	// non-terminal rows COMPLETED so the recovery loop stops re-driving the event.
	if disabled, ffErr := common.IsFeatureEnabledForAccount("EVENT_DEBUG_ANALYSIS_DISABLED", ctx.GetSecurityContext().GetTenantId(), request.AccountId); ffErr == nil && disabled {
		ctx.GetLogger().Info("analyzer: event debug analysis disabled for account, skipping RCA compute before event load", "event_id", request.EventId, "account_id", request.AccountId)
		if fingerprint, aggKey, idErr := getEventIdentity(dbManager, eventRequest); idErr == nil {
			if request.AttemptID != "" {
				_ = updateStatus(string(events.AnalysisStatusFailed), "RCA generation is disabled for this account")
			} else {
				markAllAnalysisSkipped(ctx, eventAnalysisRepo, request.EventId, fingerprint, request.AccountId, aggKey, "skipped - debug analysis disabled for account")
			}
		} else {
			ctx.GetLogger().Warn("analyzer: unable to resolve event identity to mark RCA skipped", "event_id", request.EventId, "error", idErr)
		}
		return EventAnalysisResponse{Status: string(events.AnalysisStatusCompleted)}, nil
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

	// First ensure analysis is done or in progress and fresh
	existingLogAnalysis, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeLog)
	if existingLogAnalysis == nil || eventAnalysisRepo.IsAnalysisStale(existingLogAnalysis.UpdatedAt) || (existingLogAnalysis.Status != string(events.AnalysisStatusCompleted) && existingLogAnalysis.Status != string(events.AnalysisStatusInProgress)) {
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

	// Check if RCA is already completed and fresh
	existingRCA, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeRCA)
	if request.AttemptID == "" && existingRCA != nil && existingRCA.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate && !eventAnalysisRepo.IsAnalysisStale(existingRCA.UpdatedAt) {
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
	if request.AttemptID != "" {
		parentSessionId = events.RCAAttemptSessionID(request.AttemptID)
	}
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
	if request.AttemptID == "" && (request.Regenerate || conv.ID == uuid.Nil || conv.Status == core.ConversationStatusFailed) {
		err = core.DeleteConversationBySession(parentSessionId, request.AccountId, request.UserId)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to delete conversation", "error", err)
		}
	}

	rcaAgent, ok := core.GetNBAgent(ctx, core.ToolLlm, request.AccountId, core.AgentStatusEnabled)
	if !ok || rcaAgent == nil {
		ctx.GetLogger().Error("analyzer: LLM agent not found for RCA")
		if statusErr := updateStatus(string(events.AnalysisStatusFailed), "LLM agent not found"); statusErr != nil {
			ctx.GetLogger().Warn("failed to update RCA status", "error", statusErr)
		}
		return EventAnalysisResponse{}, errors.New("LLM agent not found")
	}

	var rcaResponse string
	var hasResponse bool

	// Try to recover from existing COMPLETED conversation if not regenerating
	if request.AttemptID != "" || !request.Regenerate {
		rcaResponse, hasResponse = getAgentResponseFromConversation(ctx, parentSessionId, request.AccountId, rcaAgent.GetName())
	}

	if hasResponse {
		ctx.GetLogger().Info("analyzer: recovered RCA response from conversation history", "session_id", parentSessionId)
	} else {
		// Gather all available analysis data to format into RCA
		summary, errSummary := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeSummary)
		if errSummary != nil {
			ctx.GetLogger().Warn("analyzer: failed to fetch summary for RCA", "error", errSummary)
		}
		investigation, errInv := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeInvestigation)
		if errInv != nil {
			ctx.GetLogger().Warn("analyzer: failed to fetch investigation for RCA", "error", errInv)
		}
		logAnalysis, errLog := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeLog)
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
		// Content, not presence: a skipped code-fix stage stores a parseable
		// empty result (events.ClearedAnalysisDoc), which is not "" and would
		// otherwise be handed to the RCA writer as findings.
		if logAnalysis != nil && events.HasStoredAnalysisContent(logAnalysis.Analysis) {
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
			statusErr := updateStatus(string(events.AnalysisStatusFailed), "RCA could not complete. Please retry.")
			if statusErr != nil {
				ctx.GetLogger().Error("unable to update status", "error", statusErr)
			}
			return EventAnalysisResponse{}, err
		}
		if len(resp.Response) > 0 && resp.Status == core.ConversationStatusCompleted {
			rcaResponse = resp.Response[0]
			hasResponse = true
		} else if resp.Status == core.ConversationStatusWaiting || resp.Status == core.ConversationStatusWaitingForClientTool {
			ctx.GetLogger().Info("analyzer: RCA analysis paused awaiting approval or client tool", "session_id", parentSessionId, "status", resp.Status)
			if updateErr := updateStatus(string(events.AnalysisStatusInProgress), "RCA paused awaiting approval"); updateErr != nil {
				ctx.GetLogger().Warn("failed to update RCA status on pause", "error", updateErr)
			}
			return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress), StatusReason: "RCA paused awaiting approval"}, nil
		} else {
			ctx.GetLogger().Warn("analyzer: RCA returned without completion", "session_id", parentSessionId, "status", resp.Status)
			if updateErr := updateStatus(string(events.AnalysisStatusFailed), "RCA returned without completion"); updateErr != nil {
				ctx.GetLogger().Warn("failed to update RCA status on failure", "error", updateErr)
			}
			return EventAnalysisResponse{Status: string(events.AnalysisStatusFailed), StatusReason: "RCA returned without completion"}, errors.New("RCA returned without completion")
		}
	}

	if hasResponse {
		rcaResponse = stripRCAFormatScaffolding(rcaResponse)
		ctx.GetLogger().Debug("analyzer: saving RCA report to database")
		// Save the response to the database
		if request.AttemptID != "" {
			err = eventAnalysisRepo.FinishRCAAttempt(ctx, request.AttemptID, request.EventId, request.AccountId, rcaResponse)
		} else {
			err = eventAnalysisRepo.SaveEventRCAAnalysis(ctx, request.LegacyAnalysisID, response.EventId, eventFingerprint, request.AccountId, eventAggregationKey, rcaResponse)
		}
		if err != nil {
			ctx.GetLogger().Warn("analyzer: failed to insert analysis from database", "error", err, "event_id", request.EventId)
			statusErr := updateStatus(string(events.AnalysisStatusFailed), "Unable to save RCA. Please retry.")
			if statusErr != nil {
				ctx.GetLogger().Error("unable to update status", "error", statusErr)
			}
			return EventAnalysisResponse{}, err
		}
		response.Summary = rcaResponse
	} else {
		return EventAnalysisResponse{}, errors.New("failed to get RCA response")
	}

	return response, nil
}

// maxInvestigationEvidenceBytes caps how much collected evidence is inlined
// into the investigation prompt. The event's evidence is already gathered on
// the Event object (populated by the events tool's evidence enrichment), so
// surfacing it lets the investigation reach a verdict instead of reporting
// "insufficient" for data the event actually carries. The cap bounds token
// cost the same way maxAnnotationRCAFormatBytes does for rca_format.
const maxInvestigationEvidenceBytes = 12000

// maxErrorLogLinesBytes bounds the extracted error/warning log lines section;
// middle-truncated so both the first and last errors of the window survive.
const maxErrorLogLinesBytes = 4096

// maxLogPatternCount bounds how many enricher log-pattern templates are
// rendered into the prompt digest.
const maxLogPatternCount = 10

// maxEventLabelValueBytes caps the rendered size of any single event-label
// value injected into the investigation prompt. This is a generic, key-agnostic
// guard: any value larger than this — from any provider, under any key — is
// moved to the conversation workspace and replaced inline with a pointer to that
// file (grep-retrievable, not discarded). It has no knowledge of which key or
// provider produced the bloat (the motivating case, an internal series array
// carrying hundreds of KB under one label, is caught purely by its size).
const maxEventLabelValueBytes = 2048

// maxInvestigationPromptBytes is a last-resort ceiling on the FULLY assembled
// event-investigation prompt. Per-field guards (sanitizeEventLabelsForPrompt,
// the 12KB evidence cap) bound the known inputs; this backstop catches ANY
// future field or provider that slips an oversized value through, so no single
// RCA prompt can ever again re-bill hundreds of KB on every planner iteration.
// Set well above a healthy RCA prompt (~20-30KB); when it fires it is a signal
// (logged) that some input needs its own per-field bound.
const maxInvestigationPromptBytes = 64000

// buildInvestigationEvidenceContext renders the evidence already collected on
// the event into a compact markdown block for the investigation prompt.
// Sections are ordered dense-signal-first with per-section budgets: insights,
// then the enricher's pattern digest, then extracted error lines, and the raw
// log body only with whatever budget remains. Previously the raw body came
// first under a single head-truncation cap, which silently deleted the
// insights whenever logs were large — the failure behind the wrong RCA on
// event 15d3e867, where 481KB of access-log noise consumed the entire budget
// and the "deployment 35 minutes before the event" insight never reached the
// agent. Returns "" when no usable evidence is present.
func buildInvestigationEvidenceContext(ev events.InvestigateData) string {
	var b strings.Builder

	// 1. Insight messages summarise the metric / trace / alert / dependency
	// findings the enrichers attached to the event. Short, pre-ranked,
	// highest signal per byte — always first so a large log body can never
	// push them past the cap.
	if insights := collectEvidenceInsights(ev); len(insights) > 0 {
		b.WriteString("### Collected Insights\n")
		for _, msg := range insights {
			b.WriteString("- ")
			b.WriteString(msg)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// 2. The log enricher's pattern digest: top templates with counts plus
	// the severity breakdown.
	if digest := renderLogPatternDigest(ev.LogSummary); digest != "" {
		b.WriteString("### Log Patterns\n")
		b.WriteString(digest)
		b.WriteString("\n")
	}

	// 3. Extracted error/warning lines (see formatErrorLogLine in the events
	// tool). Middle-truncated so the start and end of the window both survive.
	if len(ev.ErrorLogData) > 0 {
		if errText := strings.TrimSpace(strings.Join(ev.ErrorLogData, "\n")); errText != "" {
			b.WriteString("### Error Log Lines\n```\n")
			b.WriteString(core.TruncateMiddle(errText, maxErrorLogLinesBytes/2, maxErrorLogLinesBytes/2))
			b.WriteString("\n```\n\n")
		}
	}

	// 4. The raw log body, only if it fits the remaining budget. When it does
	// not, the caller saves it to the conversation workspace instead and the
	// prompt points the agent at that file.
	if logText := strings.TrimSpace(ev.LogData); logText != "" {
		remaining := maxInvestigationEvidenceBytes - b.Len()
		if remaining > 512 {
			b.WriteString("### Collected Logs\n```\n")
			if len(logText) > remaining {
				b.WriteString(core.TruncateMiddle(logText, remaining/2, remaining/2))
			} else {
				b.WriteString(logText)
			}
			b.WriteString("\n```\n\n")
		}
	}

	return strings.TrimSpace(b.String())
}

// saveEvidenceLogsToWorkspace persists the event's collected log evidence to
// the analysis conversation's workspace when it is too large to inline, and
// returns the prompt note pointing the agent at the file. Best-effort: returns
// "" for small/absent logs or on save failure (the inline digest built by
// buildInvestigationEvidenceContext still covers the evidence). Mirrors
// saveLogsToWorkspace in the log-fetch agent; local because the evidence body
// is already plain line-per-message text.
//
// sessionId is the analysis session ("event-<fingerprint>"), not the
// conversation UUID. The workspace scopes both saved files and shell execution
// to a per-conversation directory keyed by the conversation UUID, so the file
// must be saved under the UUID the agent's shell_execute will run in — saving
// under the session string lands it in a directory the agent never sees. The
// conversation row exists by this point: Step 1 (summary) created it.
func saveEvidenceLogsToWorkspace(ctx *security.RequestContext, accountId, sessionId, eventId string, event events.Event) string {
	logText := strings.TrimSpace(event.Evidences.LogData)
	if len(logText) <= maxInvestigationEvidenceBytes {
		return ""
	}
	conv, convErr := core.GetConversationDao().GetConversationBySession(accountId, sessionId)
	if convErr != nil || conv.ID == uuid.Nil {
		ctx.GetLogger().Warn("analyzer: cannot resolve conversation for evidence log save", "error", convErr, "session_id", sessionId)
		return ""
	}
	filename := fmt.Sprintf("evidence_logs_%s.txt", eventId)
	wm := workspace.NewWorkspaceManager()
	if err := wm.SaveFile(ctx, accountId, conv.ID.String(), filename, logText); err != nil {
		ctx.GetLogger().Warn("analyzer: failed to save evidence logs to workspace", "error", err, "file", filename, "event_id", eventId)
		return ""
	}
	ctx.GetLogger().Info("analyzer: evidence logs saved to workspace", "file", filename, "bytes", len(logText), "event_id", eventId)
	window := ""
	if event.StartsAt != nil {
		window = fmt.Sprintf(" for the incident window (%s → %s)", common.FormatPresentationTime(event.StartsAt), formatWindowEnd(event.EndsAt))
	}
	return fmt.Sprintf("## Stored Log Evidence\nThe full logs already collected%s are saved in your workspace at `%s` (one log line per row). Grep this file first (e.g. `grep -iE \"error|timeout|fail\" %s | head -40`) before fetching live logs — logs fetched live at analysis time include activity from after the incident window.", window, filename, filename)
}

// formatWindowEnd renders an incident-window end for the prompt: a still-firing
// occurrence has no end yet, and "ongoing" reads better to the model than the
// generic "unknown" the shared formatter falls back to.
func formatWindowEnd(t *time.Time) string {
	if t == nil {
		return "ongoing"
	}
	return common.FormatPresentationTime(t)
}

// renderLogPatternDigest renders the log enricher's pattern summary (top
// templates with counts and the severity-level breakdown) as markdown.
// Returns "" when the summary is absent or not the expected shape.
func renderLogPatternDigest(summary any) string {
	m, ok := summary.(map[string]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	if lb, ok := m["level_breakdown"].(map[string]any); ok && len(lb) > 0 {
		levels := make([]string, 0, len(lb))
		for level := range lb {
			levels = append(levels, level)
		}
		sort.Strings(levels)
		b.WriteString("Level breakdown:")
		for _, level := range levels {
			fmt.Fprintf(&b, " %s=%v", level, lb[level])
		}
		b.WriteString("\n")
	}
	patterns, _ := m["log_patterns"].([]any)
	for i, pAny := range patterns {
		if i >= maxLogPatternCount {
			break
		}
		p, ok := pAny.(map[string]any)
		if !ok {
			continue
		}
		template, _ := p["template"].(string)
		if strings.TrimSpace(template) == "" {
			continue
		}
		fmt.Fprintf(&b, "- %v× `%s`\n", p["count"], template)
		if example, ok := p["example"].(string); ok && example != "" {
			b.WriteString("  e.g. `")
			b.WriteString(core.TruncateHead(example, 300))
			b.WriteString("`\n")
		}
	}
	return b.String()
}

// collectEvidenceInsights gathers de-duplicated, non-empty insight messages from
// every insight-bearing field of the collected evidence, preserving encounter
// order.
func collectEvidenceInsights(ev events.InvestigateData) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(insights []events.Insight) {
		for _, in := range insights {
			msg := strings.TrimSpace(in.Message)
			if msg == "" || seen[msg] {
				continue
			}
			seen[msg] = true
			out = append(out, msg)
		}
	}
	for _, d := range []events.InvestigateDataInsight{
		ev.PodData, ev.NodeData, ev.Deployment, ev.AlertLabels, ev.JobInformation,
		ev.JobEvents, ev.JobPodEvents, ev.RelatedEvents, ev.ContainerMetrics,
		ev.Traces, ev.AlertData, ev.ServiceMap,
	} {
		add(d.Insight)
	}
	for _, list := range [][]events.InvestigateDataInsight{
		ev.PodMetrics, ev.NodeMetrics, ev.NoisyNeighbours, ev.ApiFailures,
		ev.PodEvents, ev.NodeEvents, ev.Markdowns, ev.UserActions,
		ev.RDBMSQueryData, ev.MetricsData, ev.Others,
	} {
		for _, d := range list {
			add(d.Insight)
		}
	}
	return out
}

// saveOverflowToWorkspace persists content too large to inline into the analysis
// conversation's workspace and returns the filename to point the agent at (""=
// disabled or on failure). The event-RCA orchestrator has shell_execute in this
// same workspace, so capped content is offloaded and grep-retrievable rather
// than discarded. Mirrors saveEvidenceLogsToWorkspace. sessionId is the analysis
// session ("event-<fingerprint>"); the conversation (and its workspace) exists
// by prompt-build time (Step 1 created it). An empty sessionId disables offload.
func saveOverflowToWorkspace(ctx *security.RequestContext, accountId, sessionId, filename, content string) string {
	if sessionId == "" {
		return ""
	}
	// This is a best-effort offload; degrade gracefully instead of panicking if
	// the DAO is unavailable (GetConversationDao returns nil when the metastore
	// DB manager can't be resolved).
	dao := core.GetConversationDao()
	if dao == nil {
		ctx.GetLogger().Warn("analyzer: conversation DAO unavailable for workspace overflow", "session_id", sessionId)
		return ""
	}
	conv, err := dao.GetConversationBySession(accountId, sessionId)
	if err != nil || conv.ID == uuid.Nil {
		ctx.GetLogger().Warn("analyzer: cannot resolve conversation for workspace overflow", "error", err, "session_id", sessionId)
		return ""
	}
	if err := workspace.NewWorkspaceManager().SaveFile(ctx, accountId, conv.ID.String(), filename, content); err != nil {
		ctx.GetLogger().Warn("analyzer: failed to save overflow to workspace", "error", err, "file", filename)
		return ""
	}
	return filename
}

// sanitizeEventLabelsForPrompt returns a copy of the event's labels safe to
// render into the investigation prompt: any single value larger than
// maxEventLabelValueBytes is replaced inline with a pointer to a workspace file,
// and its full content is recorded in pending (filename -> content) for the
// caller to persist ON THE RUN BRANCH ONLY. Deferring the write keeps this
// function pure (no I/O) and ensures recovered / re-polled investigations —
// which build the prompt but never run the agent — do not touch the workspace,
// mirroring saveEvidenceLogsToWorkspace. The guard is generic and key-agnostic:
// it bounds values by size alone, with no knowledge of which key or provider
// produced them. It never mutates the stored event or the parsedLabels map used
// for workload resolution. Input is event.Labels (an `any` normally a
// JSON-object string); non-string or unparseable input is returned unchanged.
func sanitizeEventLabelsForPrompt(ctx *security.RequestContext, labels any, eventId string, pending map[string]string) any {
	labelsStr, ok := labels.(string)
	if !ok || labelsStr == "" {
		return labels
	}
	// Decode only the top level — values stay as raw JSON bytes (json.RawMessage),
	// so a huge nested value (e.g. a 460KB series array) is never recursively
	// decoded into Go structures just to measure and re-emit it. len(raw) is the
	// serialized size directly, and re-marshaling a RawMessage emits it verbatim
	// (no double-encoding).
	parsed := map[string]json.RawMessage{}
	if err := common.UnmarshalJson([]byte(labelsStr), &parsed); err != nil {
		// Not a JSON object we can filter key-by-key; bound the whole blob and
		// offload the full value.
		if len(labelsStr) > maxEventLabelValueBytes {
			file := fmt.Sprintf("event_labels_%s.txt", eventId)
			pending[file] = labelsStr
			return core.TruncateHead(labelsStr, maxEventLabelValueBytes) + fmt.Sprintf("\n…[%d bytes truncated — full labels in workspace file %s; if relevant, grep it, e.g. grep -i \"<keyword>\" %s | head -40]", len(labelsStr), file, file)
		}
		return labels
	}
	overflow := map[string]json.RawMessage{}
	sizes := map[string]int{}
	for k, raw := range parsed {
		if len(raw) > maxEventLabelValueBytes {
			overflow[k] = raw
			sizes[k] = len(raw)
		}
	}
	if len(overflow) == 0 {
		return labels
	}
	// Collect all oversized values into one workspace file, keyed by label name;
	// point each label at a bounded grep of it. The write happens on the run branch.
	blob, err := common.MarshalJsonIndent(overflow, "", "  ")
	if err != nil {
		return labels // can't offload safely; the total-prompt backstop still bounds it
	}
	file := fmt.Sprintf("event_labels_overflow_%s.txt", eventId)
	pending[file] = string(blob)
	for k := range overflow {
		ptr := fmt.Sprintf("[%d bytes — moved to workspace file %s (label %q); if relevant, inspect with a bounded grep, e.g. grep -i \"<keyword>\" %s | head -40]", sizes[k], file, k, file)
		if b, err := common.MarshalJson(ptr); err == nil {
			parsed[k] = b // replace the oversized value with the pointer string
		}
	}
	ctx.GetLogger().Warn("analyzer: offloaded oversized event-label values", "labels", sizes, "workspace_file", file)
	cleaned, err := common.MarshalJson(parsed)
	if err != nil {
		return labels
	}
	return string(cleaned)
}

// capInvestigationPrompt bounds the fully assembled investigation prompt to
// maxInvestigationPromptBytes. When over budget the FULL text is recorded in
// pending (for the caller to persist on the run branch) and the inline prompt is
// condensed to the head (event context) and tail (required output-section
// instructions) with a pointer to that file — nothing is lost, the agent can
// grep the full context on demand, yet the per-iteration prompt stays bounded.
// Returns the input unchanged when within budget; logs when it fires.
func capInvestigationPrompt(ctx *security.RequestContext, prompt, eventId string, pending map[string]string) string {
	if len(prompt) <= maxInvestigationPromptBytes {
		return prompt
	}
	file := fmt.Sprintf("event_investigation_context_%s.txt", eventId)
	pending[file] = prompt
	ctx.GetLogger().Warn("analyzer: assembled investigation prompt exceeded cap",
		"original_bytes", len(prompt), "cap", maxInvestigationPromptBytes, "workspace_file", file)
	truncated := core.TruncateMiddle(prompt, maxInvestigationPromptBytes*3/4, maxInvestigationPromptBytes/4)
	return truncated + fmt.Sprintf("\n\n[The investigation context above was condensed from %d bytes; the full context is in workspace file %s. If you need a detail truncated above, grep it, e.g. grep -in \"<keyword>\" %s | head -40.]", len(prompt), file, file)
}

// buildCodeCapabilityBlock is the guidance appended to the investigation prompt
// (#37005): it tells the debug agent which repository / values file is reachable
// for this workload, that it should localise a concluded change with
// code_analyzer, and to end with a <change_plan> block the pipeline can act on.
// Returns "" when nothing is reachable.
func buildCodeCapabilityBlock(caps *services_server.EventCodeCapabilities, namespace, workload string) string {
	if !caps.HasAny() {
		return "\n\n## Code Analysis\nNo source repository or deployment values file is mapped for `" + workload +
			"`. If your investigation concludes a code defect, state it in your findings and stop there — do not name a file you cannot verify, and do not emit a <change_plan> block."
	}

	var b strings.Builder
	b.WriteString("\n\n## Code Analysis\n")
	if caps.Source != nil {
		fmt.Fprintf(&b, "The application source for `%s` in `%s` is `%s`", workload, namespace, caps.Source.Repo)
		if caps.Source.Commit != "" {
			fmt.Fprintf(&b, " at commit `%s`", caps.Source.Commit)
		}
		b.WriteString(". `code_analyzer` is available.\n")
		b.WriteString("- When your causality chain lands on a code defect, OR your Next Steps call for a code change (error handling, retry/backoff, a hardening change) even if the root cause is external/config, call `code_analyzer` in **explore mode** with the distinctive strings from the logs — the exact error message text and any `filename:lineno` — so it clones the repo and greps for where that code lives. Use what it returns to name the exact file(s).\n")
	}
	if caps.Deployment != nil {
		fmt.Fprintf(&b, "The deployment values for `%s` are in `%s`: `%s`.\n", workload, caps.Deployment.Repo, caps.Deployment.ValuesPath)
		b.WriteString("- If you conclude a resource limit is genuinely too low, that is an actionable change: name the values file and the exact target value.\n")
	}
	b.WriteString("\nIf — and ONLY if — you concluded an actionable change, end your response with this block (omit it entirely for an intentional change, an infra fault, an inconclusive result, or a purely operational remedy such as restarting or adding an integration):\n")
	b.WriteString("<change_plan>\n")
	b.WriteString("kind: source | deployment\n")
	b.WriteString("files: <repo-relative path(s) code_analyzer identified, comma-separated; or the values file>\n")
	b.WriteString("change: <what to change and why, as direct instructions to a code-fixing agent>\n")
	b.WriteString("target_value: <for a deployment change only: the exact value, e.g. 512Mi>\n")
	b.WriteString("</change_plan>\n")
	return b.String()
}

func generateEventAnalysisPrompt(ctx *security.RequestContext, event events.Event, request EventAnalysisRequest, response EventAnalysisResponse, parsedLabels map[string]any, anaylsisRepo *events.EventAnalysisRepository, pending map[string]string) (string, string, bool, string, *tools.RunbookRef, *services_server.EventCodeCapabilities, error) {
	// runbookRef holds the runbook resolved from the event's runbook_url label
	// (if any), so the caller can cite it in the synthesized analysis.
	var runbookRef *tools.RunbookRef
	eventDefinition, annotations, err := anaylsisRepo.GetEventRuleDefinition(ctx, request.AccountId, event.AggregationKey)
	if err != nil {
		ctx.GetLogger().Error("analyzer: unable to get rule definition", "error", err, "rule", event.AggregationKey)
		eventDefinition = "n/a" // Default value if fetching fails
	}

	// do rootcause analysis
	// Arg order must match the %s placeholders in event_investigation.txt:
	// id, definition, title, description, labels, time, window start, window
	// end, source, summary.
	eventAnalsysisPrompt := prompts.GetPrompt(ctx.GetContext(), prompts.PromptEventInvestigation, request.AccountId, request.EventId, eventDefinition, event.Title, event.Description, sanitizeEventLabelsForPrompt(ctx, event.Labels, request.EventId, pending), common.FormatPresentationTime(event.UpdatedAt), common.FormatPresentationTime(event.StartsAt), formatWindowEnd(event.EndsAt), event.Source, response.Summary)

	// Add account context (cloud provider) to help the LLM tailor its output
	if cloudProvider := agents.GetCloudProviderForAccount(request.AccountId); cloudProvider != "" {
		eventAnalsysisPrompt = eventAnalsysisPrompt + "\n\n**Account Context:** This account's infrastructure is on " + cloudProvider + ". Tailor your analysis, examples, and recommendations to this infrastructure type."
	}

	// #37005: resolve which repository / deployment values file this workload maps
	// to BEFORE the investigation runs, tell the debug agent so it can localise a
	// concluded change with code_analyzer, and require a <change_plan> block the
	// pipeline gates on. Only appended when a git integration is configured — with
	// no integration there is nothing to act on.
	var codeCapabilities *services_server.EventCodeCapabilities
	if isGitIntegrationConfigured(request.AccountId) {
		capNamespace, capWorkload := resolveEventWorkload(ctx, event, parsedLabels)
		if capNamespace != "" && capWorkload != "" {
			if dbm, dbErr := common.GetDatabaseManager(common.Metastore); dbErr == nil {
				if resolved, capErr := services_server.ResolveEventCodeCapabilities(ctx, dbm, request.AccountId, services_server.SourceCodeAnnotationOptions{
					EventId:      request.EventId,
					WorkloadName: capWorkload,
					Namespace:    capNamespace,
				}); capErr == nil {
					codeCapabilities = resolved
				} else {
					ctx.GetLogger().Warn("analyzer: unable to resolve code capabilities for investigation prompt", "error", capErr, "event_id", request.EventId)
				}
			}
			eventAnalsysisPrompt = eventAnalsysisPrompt + buildCodeCapabilityBlock(codeCapabilities, capNamespace, capWorkload)
		}
	}

	// Surface the evidence already collected on this event (logs plus metric /
	// trace / alert insights) into the investigation prompt. The investigation
	// prompt is an audit of "what is provided" — but previously it only received
	// the event definition, labels and preliminary summary, never the enriched
	// evidence the events tool attaches to the event. That made thin-payload
	// alerts (cloud metric alerts especially) report "Data Assessment:
	// Insufficient" for logs the event actually carried (e.g. a GCP load-balancer
	// 429 event whose access logs — source IP, URI, Cloud Armor policy — sat in
	// ErrorLogData but never reached the agent). Treat it as provided data.
	if evidenceContext := buildInvestigationEvidenceContext(event.Evidences); evidenceContext != "" {
		eventAnalsysisPrompt = eventAnalsysisPrompt +
			"\n\n## Collected Evidence\nThe following evidence was already gathered for this event. Treat it as part of the provided data and use it in your assessment before judging whether data is sufficient:\n\n" +
			core.TruncateHead(evidenceContext, maxInvestigationEvidenceBytes)
	}

	// Explain how to interpret related-alert candidates. The preliminary summary
	// and collected evidence may already contain the assembly, so fetching it
	// again is a gap-filling fallback rather than mandatory first-turn work.
	eventAnalsysisPrompt = eventAnalsysisPrompt +
		"\n\n## Related-Alert Candidates\nReuse related-alert candidates already present in the preliminary summary or collected evidence. " +
		"Only when that information is absent or incomplete, call get_incident_assembly with event_id=" + request.EventId +
		". It returns the alerts around this event grouped by timing and topology: " +
		"same_incident (this alert's other firings and cross-source copies), cause (config changes and " +
		"upstream-dependency alerts shortly before it), impact (dependent services alerting after it) and " +
		"chronic (background noise for that subject). These are candidates only — they may or may not be " +
		"related. Verify each against evidence before using it in your root-cause reasoning, and distinguish " +
		"active causes from chronic background noise.\n" +
		"Do not reacquire event details or triage explanation while filling this gap. REQUIRED: end your analysis with a '### Related Alerts Check' section — one line per cause/impact " +
		"candidate the tool returned, each marked confirmed (with the evidence), ruled out (with the reason), " +
		"or not assessed. Render each candidate's alert name as a markdown link to its event page using that " +
		"candidate's event_id from the tool output: [<alert title>](/investigate?id=<event_id>&accountId=" +
		request.AccountId + "). If the tool returned no candidates, say so in one line."

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
			// The gate is "we cannot tell which service this is about", but it was
			// asking only whether Datadog happened to send a service/services
			// label. By this point the pipeline has already resolved the subject
			// itself — and the log query that ran for this very event used it.
			// Without this fallback, an event with subject_owner=workflow-server,
			// subject_owner_kind=Deployment, subject_namespace=nudgebee and 1000
			// collected log lines had its log analysis AND its investigation
			// written as COMPLETED with an empty body, so the investigate page
			// showed nothing at all.
			hasServiceLabel := common.HasNonEmptyValue(parsedLabels["services"]) || common.HasNonEmptyValue(parsedLabels["service"])
			hasResolvedSubject := event.SubjectNamespace != "" &&
				(event.SubjectOwner != "" || event.SubjectName != "")
			if !hasServiceLabel && !hasResolvedSubject {
				debugAnalysisEnabled = false
				debugAnalysisSkipReason = "skipped - event identifies no service: no 'service'/'services' label and no resolved subject"
			} else {
				debugAnalysisEnabled = true
				debugAnalysisSkipReason = ""
			}
		}
	}
	if debugAnalysisEnabled {
		// check prompt instructions
		userPrompt := ""
		// Prometheus/Alertmanager and NewRelic alerts carry a `runbook_url`
		// label. Resolve it to real runbook *content* — Confluence and
		// ServiceNow are fetched with the account's integration credentials,
		// any other host is fetched as a public page — and inject the body so
		// automatic RCA actually reads the runbook instead of the operator's
		// curated steps never reaching the LLM. Fetch is bounded and fails
		// open: on any error we fall through to the runbook annotation / DB
		// knowledge_base below.
		if rbURL, ok := parsedLabels["runbook_url"].(string); ok && strings.TrimSpace(rbURL) != "" {
			if ref, rbErr := tools.ResolveRunbook(request.AccountId, rbURL); rbErr != nil {
				ctx.GetLogger().Debug("analyzer: unable to resolve runbook_url", "error", rbErr, "runbook_url", rbURL)
			} else {
				runbookRef = &ref
				userPrompt = "Refer to the following runbook while analyzing the event:\n" + ref.Text
			}
		}
		if userPrompt == "" && annotations != nil && annotations["runbook"] != nil {
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
	return capInvestigationPrompt(ctx, eventAnalsysisPrompt, request.EventId, pending), accountPrompt, debugAnalysisEnabled, debugAnalysisSkipReason, runbookRef, codeCapabilities, err
}

// saveEventRunbookReference persists the runbook resolved for this event as a
// knowledge_base conversation reference, so it appears in the Additional
// Contexts panel alongside every other context source — not only as a prose
// footer inside the analysis text. Keyed to the investigation's message, so a
// regenerate writes its own row and a re-served analysis writes none.
// Best-effort: a failed insert must not fail the analysis.
func saveEventRunbookReference(ctx *security.RequestContext, accountId string, resp core.NBAgentResponse, ref *tools.RunbookRef) {
	if ref == nil || resp.ConversationId == "" {
		return
	}
	url := strings.TrimSpace(ref.URL)
	if url == "" {
		return
	}
	title := strings.TrimSpace(ref.Title)
	if title == "" {
		title = url
	}
	metadata := map[string]any{
		"name":    title,
		"subject": title,
		"url":     url,
		"source":  ref.Source,
		"via":     "event_runbook",
	}
	// Snippet of the fetched runbook body so the Additional Contexts row shows
	// what was actually injected — there is no llm_knowledgebases row to pull
	// content from for a fetched page.
	if snippet := strings.TrimSpace(ref.Text); snippet != "" {
		metadata["content"] = core.TruncateHead(snippet, 700)
	}
	err := core.GetConversationDao().SaveAgentReferences(accountId, resp.ConversationId, resp.MessageId, resp.AgentId, []core.AgentReference{{
		Type: core.AgentReferenceTypeKB,
		// The runbook is a fetched page, not an llm_knowledgebases row — use a
		// stable URL-derived id so repeated saves of the same runbook dedupe.
		ReferenceID: fmt.Sprintf("runbook:%x", sha256.Sum256([]byte(url))),
		Metadata:    metadata,
	}})
	if err != nil {
		ctx.GetLogger().Warn("analyzer: unable to save runbook reference", "error", err, "conversation_id", resp.ConversationId)
	}
}

// appendRunbookReference adds a deterministic, clickable citation for the
// resolved runbook to the synthesized analysis, so the source stays visible in
// the UI even when the model doesn't echo it in its own output. No-op when no
// runbook was resolved from the event's runbook_url label.
func appendRunbookReference(detailed string, ref *tools.RunbookRef) string {
	if ref == nil || strings.TrimSpace(ref.URL) == "" {
		return detailed
	}
	label := strings.TrimSpace(ref.Title)
	if label == "" {
		label = ref.URL
	}
	src := ""
	if ref.Source != "" {
		src = " (" + ref.Source + ")"
	}
	return strings.TrimRight(detailed, "\n") + "\n\n---\n\n**📖 Runbook reference" + src + ":** [" + label + "](" + ref.URL + ")\n"
}

func analyzeEventUsingAgentsAndUpdateDb(ctx *security.RequestContext, request EventAnalysisRequest) (EventAnalysisResponse, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("unable to get db", "error", err)
		return EventAnalysisResponse{}, err
	}
	eventAnalysisRepo := events.NewEventAnalysisRepository(dbManager)

	// Gate before getEventData (which `select *`s the event, materialising its
	// potentially multi-MB evidences column) and before any agent runs. For
	// accounts with event debug analysis disabled, skip all compute and mark any
	// non-terminal analysis rows COMPLETED so syncStuckEventAnalyses stops
	// re-driving the event — this is what breaks the OOM crash loop on oversized
	// events. Already-completed analyses are preserved and still served by the
	// read path (getOrCreateEventAnalysisStatus).
	if disabled, ffErr := common.IsFeatureEnabledForAccount("EVENT_DEBUG_ANALYSIS_DISABLED", ctx.GetSecurityContext().GetTenantId(), request.AccountId); ffErr == nil && disabled {
		ctx.GetLogger().Info("analyzer: event debug analysis disabled for account, skipping compute before event load", "event_id", request.EventId, "account_id", request.AccountId)
		if fingerprint, aggKey, idErr := getEventIdentity(dbManager, request); idErr == nil {
			markAllAnalysisSkipped(ctx, eventAnalysisRepo, request.EventId, fingerprint, request.AccountId, aggKey, "skipped - debug analysis disabled for account")
		} else {
			ctx.GetLogger().Warn("analyzer: unable to resolve event identity to mark skipped", "event_id", request.EventId, "error", idErr)
		}
		return EventAnalysisResponse{Status: string(events.AnalysisStatusCompleted)}, nil
	}

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
			a, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, aType)
			if a == nil || a.Status != string(events.AnalysisStatusCompleted) || eventAnalysisRepo.IsAnalysisStale(a.UpdatedAt) {
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
	// Each per-step cache carries the same staleness term as the outer gates. They
	// have to agree: a gate that rejects a stale stage dispatches this pipeline,
	// and a cache here that still considers it fresh would skip the regeneration,
	// write nothing, and leave the timestamp unchanged — so every later event for
	// the fingerprint would repeat the round forever.
	existingSummary, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeSummary)
	if existingSummary != nil && existingSummary.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate &&
		!eventAnalysisRepo.IsAnalysisStale(existingSummary.UpdatedAt) {
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

		if shouldRecoverStageFromConversation(eventAnalysisRepo, existingSummary, request.Regenerate) {
			summaryResponseStr, hasSummary = getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, summaryAgentName)
		}

		if hasSummary {
			ctx.GetLogger().Info("analyzer: recovered summary from conversation history", "session_id", parentConversationId)
		} else {
			// Beyond fetching the event's details, ask the events agent to explain
			// Nudgebee's auto-triage decision. Step 1 is the only stage of the
			// automatic pipeline that runs the events agent, so it is where the
			// triage tools (get_triage_explanation) get exercised — recording *why*
			// an event was suppressed/duplicated/scored into the summary (and thus
			// event_log_analysis and the synthesized detailed response), not just in
			// interactive chat. Degrades to a plain summary when no triage data is
			// recorded yet.
			summaryQuery := "Get the details of Event with id - " + eventData.Id +
				". Also explain how Nudgebee auto-triaged this event: its triage status (nb_status), " +
				"computed priority and the score_factors that produced it, and the deduplication chain and " +
				"firing history behind the decision. Use get_triage_explanation for the dedup chain and " +
				"firing history, and get_incident_assembly for what else is involved in the same incident."
			summaryResp, err := core.HandleConversationSessionRequest(ctx, eventSummaryAgent, request.UserId, request.AccountId, parentConversationId, summaryQuery, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation), core.ConversationSessionRequestWithEnableCritique(false), core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{Labels: parsedLabels}))
			if err != nil {
				if errors.Is(err, core.ErrConversationInProgress) {
					ctx.GetLogger().Info("analyzer: summary already in progress via conversation", "session_id", parentConversationId)
					return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress)}, nil
				}
				ctx.GetLogger().Error("analyzer: unable to generate summary", "error", err)
				updateAllFailed(ctx, eventAnalysisRepo, eventData, request.AccountId, err.Error())
				return EventAnalysisResponse{}, err
			}
			if len(summaryResp.Response) > 0 && summaryResp.Status == core.ConversationStatusCompleted {
				summaryResponseStr = summaryResp.Response[0]
				hasSummary = true
			} else if summaryResp.Status == core.ConversationStatusWaiting || summaryResp.Status == core.ConversationStatusWaitingForClientTool {
				ctx.GetLogger().Info("analyzer: summary paused awaiting approval or client tool", "session_id", parentConversationId, "status", summaryResp.Status)
				if upErr := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusInProgress), "summary paused awaiting approval", events.AnalysisTypeSummary, false); upErr != nil {
					ctx.GetLogger().Warn("failed to update summary status on pause", "error", upErr)
				}
				return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress), StatusReason: "summary paused awaiting approval"}, nil
			} else {
				ctx.GetLogger().Warn("analyzer: summary returned without completion", "event_id", request.EventId, "session_id", parentConversationId, "status", summaryResp.Status)
				if upErr := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusFailed), "summary returned without completion", events.AnalysisTypeSummary, false); upErr != nil {
					ctx.GetLogger().Warn("failed to update summary status on failure", "error", upErr)
				}
				return EventAnalysisResponse{Status: string(events.AnalysisStatusFailed), StatusReason: "summary returned without completion"}, errors.New("summary returned without completion")
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
	// pendingWorkspaceWrites collects oversized prompt inputs (labels / the whole
	// assembled prompt) that the guardrail condensed to a workspace-file pointer.
	// They are persisted below only on the branch that actually runs the agent, so
	// recovered / re-polled investigations never touch the workspace.
	pendingWorkspaceWrites := map[string]string{}
	eventAnalsysisPrompt, accountPrompt, debugAnalysisEnabled, debugSkipReason, runbookRef, codeCapabilities, err := generateEventAnalysisPrompt(ctx, eventData, request, response, parsedLabels, eventAnalysisRepo, pendingWorkspaceWrites)
	if err != nil {
		return EventAnalysisResponse{}, err
	}

	if !debugAnalysisEnabled {
		ctx.GetLogger().Info("analyzer: debug analysis is disabled, skipping log analysis", "event_id", request.EventId, "reason", debugSkipReason)
		// Upsert, not update: these stages are being marked COMPLETED without ever
		// having run, so there is usually no row to update. A plain UPDATE would
		// match nothing and leave the pipeline permanently unable to report itself
		// complete.
		if err := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), debugSkipReason, events.AnalysisTypeInvestigation, false); err != nil {
			ctx.GetLogger().Warn("failed to update event analysis status on debug skip", "error", err)
		}
		if err := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), debugSkipReason, events.AnalysisTypeLog, true); err != nil {
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
	// changePlan is the <change_plan> block the debug agent emits when it concludes
	// an actionable code change (#37005); it is lifted out of the investigation
	// text here and drives Step 3.
	var investigationText string
	var changePlan *codeChangePlan
	existingInvestigation, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeInvestigation)
	if existingInvestigation != nil && existingInvestigation.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate &&
		!eventAnalysisRepo.IsAnalysisStale(existingInvestigation.UpdatedAt) {
		response.Summary = existingInvestigation.Summary
		investigationText = existingInvestigation.Summary
		// The stripped investigation text no longer carries the block; the plan
		// was stashed as JSON in the investigation row's analysis column so a
		// log-only regenerate re-gates without re-running the agent.
		changePlan = unmarshalCodeChangePlan(existingInvestigation.Analysis)
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

		if shouldRecoverStageFromConversation(eventAnalysisRepo, existingInvestigation, request.Regenerate) {
			investigationResponse, hasInvestigation = getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, rootcauseAgentName)
		}

		if hasInvestigation {
			ctx.GetLogger().Info("analyzer: recovered investigation from conversation history", "session_id", parentConversationId)
		} else {
			// When the collected log evidence is too large to inline, persist
			// it to the analysis conversation's workspace so the agent can
			// grep the full incident-window logs instead of re-fetching live
			// logs at analysis time (live fetches see the analyzer's own
			// runtime, which is how event 15d3e867 blamed post-window errors
			// the pipeline itself produced). Done only on this branch — the
			// one that actually runs the agent — so re-polls and recovered
			// investigations never touch the workspace.
			if fileNote := saveEvidenceLogsToWorkspace(ctx, request.AccountId, parentConversationId, request.EventId, eventData); fileNote != "" {
				eventAnalsysisPrompt = eventAnalsysisPrompt + "\n\n" + fileNote
			}
			// Persist any oversized prompt inputs the guardrail condensed to a
			// workspace-file pointer. Best-effort: on failure the pointer simply
			// resolves to a missing file (an empty grep), never a hard error.
			for fn, content := range pendingWorkspaceWrites {
				saveOverflowToWorkspace(ctx, request.AccountId, parentConversationId, fn, content)
			}
			// #37005: record what the debug agent was told about code before it
			// runs — whether a repo/values file resolved and whether the prompt
			// carries the code_analyzer offer it needs to localise a change.
			ctx.GetLogger().Info("analyzer: dispatching investigation to debug agent",
				"event_id", request.EventId, "session_id", parentConversationId,
				"debug_agent", rootcauseAgentName,
				"code_capabilities_present", codeCapabilities.HasAny(),
				"has_source_repo", codeCapabilities != nil && codeCapabilities.Source != nil,
				"has_deployment_values", codeCapabilities != nil && codeCapabilities.Deployment != nil,
				"prompt_offers_code_analyzer", strings.Contains(eventAnalsysisPrompt, "`code_analyzer` is available"),
				"prompt_bytes", len(eventAnalsysisPrompt))
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
				// Attach the resolved runbook to the investigation conversation
				// as a context reference (Additional Contexts panel), mirroring
				// the prose footer appendRunbookReference adds to the text.
				saveEventRunbookReference(ctx, request.AccountId, rootcauseAnalysis, runbookRef)
				// #37005: record whether the debug agent reached code_analyzer to
				// localise the change (the gate in Step 3 depends on it).
				logInvestigationToolUsage(ctx, dbManager, rootcauseAnalysis.ConversationId, request.EventId)
			} else if rootcauseAnalysis.Status == core.ConversationStatusWaiting || rootcauseAnalysis.Status == core.ConversationStatusWaitingForClientTool {
				ctx.GetLogger().Info("analyzer: investigation paused awaiting approval or client tool", "event_id", request.EventId, "session_id", parentConversationId, "status", rootcauseAnalysis.Status)
				if updateErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusInProgress), "investigation paused awaiting tool approval", events.AnalysisTypeInvestigation); updateErr != nil {
					ctx.GetLogger().Warn("failed to update event analysis status on investigation pause", "error", updateErr)
				}
				return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress), StatusReason: "investigation paused awaiting tool approval"}, nil
			} else {
				ctx.GetLogger().Warn("analyzer: investigation returned without findings or completion", "event_id", request.EventId, "session_id", parentConversationId, "status", rootcauseAnalysis.Status)
				if updateErr := eventAnalysisRepo.UpdateEventAnalysisStatus(ctx, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusFailed), "investigation returned without completion", events.AnalysisTypeInvestigation); updateErr != nil {
					ctx.GetLogger().Warn("failed to update event analysis status on empty investigation", "error", updateErr)
				}
				return EventAnalysisResponse{Status: string(events.AnalysisStatusFailed), StatusReason: "investigation returned without completion"}, errors.New("investigation returned without completion")
			}
		}

		if hasInvestigation {
			// #37005: lift the <change_plan> block out of the investigation text
			// before it is stored / synthesised / shown, and stash the parsed plan
			// as JSON in this row's analysis column for Step 3 and log-only reruns.
			changePlan = parseChangePlanBlock(investigationResponse)
			if changePlan != nil {
				ctx.GetLogger().Info("analyzer: debug agent emitted a change plan",
					"event_id", request.EventId, "kind", changePlan.Kind,
					"files", strings.Join(changePlan.Files, ","),
					"target_value", changePlan.TargetValue, "change_bytes", len(changePlan.Change))
			} else {
				ctx.GetLogger().Info("analyzer: no <change_plan> block in investigation",
					"event_id", request.EventId, "investigation_bytes", len(investigationResponse))
			}
			cleaned := stripChangePlanBlock(investigationResponse)
			response.Summary = cleaned
			investigationText = cleaned
			err = eventAnalysisRepo.UpsertEventAnalysis(ctx, request.EventId, marshalCodeChangePlan(changePlan), cleaned, string(events.AnalysisStatusCompleted), eventData.Fingerprint, request.AccountId, eventData.AggregationKey, events.AnalysisTypeInvestigation)
			if err != nil {
				ctx.GetLogger().Error("unable to update status after root-cause", "error", err, "eventId", request.EventId)
			}
		}
	}

	// Step 3: Log Analysis
	// codeFixText holds the plain-text analysis used by Step 4 synthesis.
	var codeFixText string

	existingLog, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeLog)
	if existingLog != nil && existingLog.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate &&
		!eventAnalysisRepo.IsAnalysisStale(existingLog.UpdatedAt) {
		response.Analysis = existingLog.Analysis
		if existingLog.RelatedEventId != "" {
			response.RelatedEventId = existingLog.RelatedEventId
		}
		if cached := (EventAnalysisResponse{}); json.Unmarshal([]byte(existingLog.Analysis), &cached) == nil {
			response.SourceUpdates = cached.SourceUpdates
		}
		ctx.GetLogger().Info("analyzer: using existing log analysis", "event_id", request.EventId)
		codeFixText = existingLog.Analysis

		// If DetailedResponse is also already done, return immediately.
		existingDetailed, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeDetailedResponse)
		if existingDetailed != nil && existingDetailed.Status == string(events.AnalysisStatusCompleted) && !request.Regenerate &&
			!eventAnalysisRepo.IsAnalysisStale(existingDetailed.UpdatedAt) {
			response.DetailedResponse = existingDetailed.Summary
			// Populate Investigation from DB before returning so callers have the full response.
			if response.Investigation == "" {
				if existingInv, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeInvestigation); existingInv != nil {
					response.Investigation = existingInv.Summary
				}
			}
			return response, nil
		}
	} else {
		// #37005: Step 3 no longer investigates. It acts on the <change_plan> the
		// debug agent produced in Step 2 — handing the localised change to
		// code_analyzer in fix mode (or diff-only when auto-raise is off).
		codeFixResponse, err := runCodeFixStage(ctx, request, response, parentConversationId, eventAnalysisRepo, eventData, parsedLabels, codeCapabilities, changePlan, shouldRecoverStageFromConversation(eventAnalysisRepo, existingLog, request.Regenerate))
		if err != nil {
			if errors.Is(err, core.ErrConversationInProgress) {
				ctx.GetLogger().Info("analyzer: code analysis paused awaiting approval or in progress", "event_id", request.EventId)
				if dbErr := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusInProgress), "code analysis paused awaiting approval", events.AnalysisTypeLog, false); dbErr != nil {
					ctx.GetLogger().Warn("failed to update log analysis status on pause", "error", dbErr)
				}
				return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress), StatusReason: "code analysis paused awaiting approval"}, nil
			} else if errors.Is(err, errCodeFixSkipped) {
				// The row is already written COMPLETED with a status_reason — do
				// NOT fall through to the re-marshal below, which would overwrite it.
				ctx.GetLogger().Info("analyzer: code analysis produced no fix", "event_id", request.EventId, "reason", codeFixResponse.StatusReason)
			} else {
				// Unexpected error. Keep the pipeline completable — write the row
				// COMPLETED empty with the reason rather than FAILED (a code-stage
				// failure must not fail the whole investigation or trip a
				// re-dispatch loop, #37005 §6).
				ctx.GetLogger().Warn("analyzer: code analysis errored, completing empty", "error", err, "event_id", request.EventId)
				if dbErr := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), "code analysis error: "+err.Error(), events.AnalysisTypeLog, true); dbErr != nil {
					ctx.GetLogger().Warn("failed to update log analysis status", "error", dbErr)
				}
			}
			// codeFixText remains "" — synthesis proceeds with available data
		} else {
			ctx.GetLogger().Debug("analyzer: saving code analysis result to database")
			jsonResponse, marshalErr := common.MarshalJson(codeFixResponse)
			if marshalErr != nil {
				ctx.GetLogger().Warn("analyzer: failed to marshal response to JSON", "error", marshalErr, "event_id", response.EventId)
				if dbErr := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), "unable to serialize result - "+marshalErr.Error(), events.AnalysisTypeLog, true); dbErr != nil {
					ctx.GetLogger().Error("unable to update status", "error", dbErr)
				}
			} else {
				response.Analysis = string(jsonResponse)
				response.SourceUpdates = codeFixResponse.SourceUpdates
				if upErr := eventAnalysisRepo.UpsertEventAnalysis(ctx, response.EventId, response.Analysis, response.Summary, string(events.AnalysisStatusCompleted), eventFingerprint, request.AccountId, eventAggregationKey, events.AnalysisTypeLog); upErr != nil {
					ctx.GetLogger().Warn("analyzer: failed to store code analysis result", "error", upErr, "event_id", request.EventId)
				}
				codeFixText = codeFixResponse.Analysis
			}
		}
	}

	// Step 4: synthesize. Skip when COMPLETED unless Regenerate — without
	// this gate every re-dispatch inserts a duplicate user message (#31422).
	existingDR, _ := eventAnalysisRepo.GetEventAnalysis(ctx, request.EventId, eventFingerprint, eventAggregationKey, request.AccountId, events.AnalysisTypeDetailedResponse)
	if existingDR != nil && existingDR.Status == string(events.AnalysisStatusCompleted) && existingDR.Summary != "" && !request.Regenerate &&
		!eventAnalysisRepo.IsAnalysisStale(existingDR.UpdatedAt) {
		ctx.GetLogger().Info("analyzer: detailed response already completed, skipping synth", "event_id", request.EventId)
		response.DetailedResponse = existingDR.Summary
		if response.Investigation == "" {
			response.Investigation = investigationText
		}
		return response, nil
	}

	var detailedResponse string
	var hasSynth bool
	if shouldRecoverStageFromConversation(eventAnalysisRepo, existingDR, request.Regenerate) {
		detailedResponse, hasSynth = getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, "event_detailed_response")
	}

	if hasSynth && strings.TrimSpace(detailedResponse) != "" {
		ctx.GetLogger().Info("analyzer: recovered detailed response from conversation history", "session_id", parentConversationId)
	} else {
		ctx.GetLogger().Info("analyzer: synthesizing detailed response", "event_id", request.EventId)
		synthResp, synthErr := synthesizeDetailedResponse(ctx, request, parentConversationId, initialSummary, investigationText, codeFixText)
		if synthErr != nil {
			ctx.GetLogger().Warn("analyzer: failed to synthesize detailed response, falling back to initial summary", "error", synthErr, "event_id", request.EventId)
			detailedResponse = initialSummary
		} else {
			detailedResponse = synthResp
		}
	}
	// Cite the resolved runbook so its source stays visible in the UI.
	detailedResponse = appendRunbookReference(detailedResponse, runbookRef)
	if err := eventAnalysisRepo.UpsertEventAnalysis(ctx, response.EventId, "", detailedResponse, string(events.AnalysisStatusCompleted), eventFingerprint, request.AccountId, eventAggregationKey, events.AnalysisTypeDetailedResponse); err != nil {
		ctx.GetLogger().Warn("analyzer: failed to save detailed response", "error", err, "event_id", request.EventId)
	}
	response.DetailedResponse = detailedResponse

	return response, nil
}

// logInvestigationToolUsage records which tools the Step 2 investigation
// conversation invoked — in particular whether the debug agent reached
// code_analyzer and how much context that call returned. This is the observable
// half of the #37005 gate: Step 3 only produces a fix when the debug agent
// concluded (and localised) a code change here. Best-effort; never affects the
// pipeline.
func logInvestigationToolUsage(ctx *security.RequestContext, dbManager *common.DatabaseManager, conversationId, eventId string) {
	if dbManager == nil || conversationId == "" {
		return
	}
	if _, err := uuid.Parse(conversationId); err != nil {
		return
	}

	// 1) Full tool histogram for the conversation — one line, whole picture.
	var histo []struct {
		ToolName string `db:"tool_name"`
		N        int    `db:"n"`
	}
	if err := dbManager.Db.Select(&histo, `
		SELECT COALESCE(tool_name, '') AS tool_name, COUNT(*) AS n
		FROM llm_conversation_tool_calls
		WHERE conversation_id = $1
		GROUP BY 1 ORDER BY 2 DESC`, conversationId); err != nil {
		ctx.GetLogger().Warn("analyzer: investigation tool-usage probe failed", "error", err, "conversation_id", conversationId)
		return
	}
	histoPairs := make([]string, 0, len(histo))
	for _, h := range histo {
		histoPairs = append(histoPairs, fmt.Sprintf("%s=%d", h.ToolName, h.N))
	}
	ctx.GetLogger().Debug("analyzer: investigation tool histogram",
		"event_id", eventId, "conversation_id", conversationId, "tools", strings.Join(histoPairs, " "))

	// 2) Detail on the code / delegate / search calls — did the debug agent pull
	// context from code_analyzer, and did that call succeed? tool `parameters`
	// carry raw log excerpts / query strings, so the per-call line stays at Debug.
	var rows []struct {
		ToolName   string     `db:"tool_name"`
		Status     string     `db:"status"`
		ChildAgent string     `db:"child_agent_id"`
		RespLen    int        `db:"resp_len"`
		CreatedAt  time.Time  `db:"created_at"`
		UpdatedAt  *time.Time `db:"updated_at"`
	}
	if err := dbManager.Db.Select(&rows, `
		SELECT COALESCE(tool_name, '') AS tool_name,
		       COALESCE(status, '') AS status,
		       COALESCE(child_agent_id::text, '') AS child_agent_id,
		       LENGTH(COALESCE(response, '')) AS resp_len,
		       created_at, updated_at
		FROM llm_conversation_tool_calls
		WHERE conversation_id = $1
		  AND (LOWER(COALESCE(tool_name, '')) LIKE '%code_analyz%'
		       OR LOWER(COALESCE(tool_name, '')) = 'agent_code_2'
		       OR LOWER(COALESCE(tool_name, '')) IN ('delegate_agent', 'search_tools'))
		ORDER BY created_at`, conversationId); err != nil {
		ctx.GetLogger().Warn("analyzer: investigation code-tool detail probe failed", "error", err, "conversation_id", conversationId)
		return
	}

	codeCalls := 0
	for _, r := range rows {
		name := strings.ToLower(r.ToolName)
		isCode := strings.Contains(name, "code_analyz") || name == "agent_code_2"
		if isCode {
			codeCalls++
		}
		dur := 0.0
		if r.UpdatedAt != nil {
			dur = r.UpdatedAt.Sub(r.CreatedAt).Seconds()
		}
		ctx.GetLogger().Debug("analyzer: investigation tool call detail",
			"event_id", eventId, "conversation_id", conversationId,
			"tool", r.ToolName, "status", r.Status, "child_agent_id", r.ChildAgent,
			"is_code_analyzer", isCode, "duration_s", dur, "response_bytes", r.RespLen)
	}
	ctx.GetLogger().Info("analyzer: investigation code_analyzer usage summary",
		"event_id", eventId, "conversation_id", conversationId,
		"code_analyzer_calls", codeCalls, "code_analyzer_used", codeCalls > 0)
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

// workloadOwnerKinds are the subject_owner_kind values for which SubjectOwner is
// the stable workload name recorded in k8s_workloads. Excludes replicaset (the
// owner name carries a pod-template hash), pod, and runner.
var workloadOwnerKinds = map[string]bool{
	"deployment":  true,
	"statefulset": true,
	"daemonset":   true,
	"rollout":     true,
	"job":         true,
}

// resolveEventWorkload determines the namespace and owning workload for an event
// so downstream code analysis can look up source-code annotations. Returns empty
// strings when no strategy resolves.
func resolveEventWorkload(ctx *security.RequestContext, eventData events.Event, parsedLabels map[string]any) (string, string) {
	var namespace, workload string
	subjectType := strings.ToLower(eventData.SubjectType)

	// Strategy 1: Use SubjectOwner and SubjectNamespace directly from event data.
	// For pods this is only safe when SubjectOwnerKind names a stable workload
	// kind — a pod's owner may otherwise be a ReplicaSet (hash-suffixed name).
	if eventData.SubjectOwner != "" && eventData.SubjectNamespace != "" &&
		(subjectType != "pod" || workloadOwnerKinds[strings.ToLower(eventData.SubjectOwnerKind)]) {
		workload = eventData.SubjectOwner
		namespace = eventData.SubjectNamespace
		ctx.GetLogger().Info("using subject owner and namespace from event data",
			"namespace", namespace, "workload", workload, "subject_owner_kind", eventData.SubjectOwnerKind)
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

	return namespace, workload
}

// codeChangePlan is the change plan the debug agent writes at the end of its
// investigation, in a <change_plan> block, once it has localised the code with
// code_analyzer (#37005). The block is lifted out of the investigation text in
// Go (parseChangePlanBlock) and stored on the log_analysis row. A missing block
// means the investigation concluded no actionable code change — the code agent
// does not run.
type codeChangePlan struct {
	Kind        string   `json:"kind"`         // "source" | "deployment"
	Files       []string `json:"files"`        // repo-relative path(s) code_analyzer identified, or the values file
	Change      string   `json:"change"`       // instructions to a code-fixing agent
	TargetValue string   `json:"target_value"` // for a deployment/limit change: the exact value, e.g. "512Mi"
}

var (
	changePlanBlockPattern = regexp.MustCompile(`(?is)<change_plan>(.*?)</change_plan>`)
	changePlanKeyPattern   = regexp.MustCompile(`(?i)^[ \t]*(kind|files|change|target_value)[ \t]*:[ \t]*(.*)$`)
)

// parseChangePlanBlock extracts the <change_plan> block from the investigation
// text. Returns nil when the block is absent, carries no recognisable fields, or
// names neither a change nor a file — callers treat nil as "no actionable code
// change concluded". Values may span multiple lines: a line is a continuation of
// the current field until the next `key:` line.
func parseChangePlanBlock(investigationText string) *codeChangePlan {
	m := changePlanBlockPattern.FindStringSubmatch(investigationText)
	if m == nil {
		return nil
	}
	fields := map[string]string{}
	currentKey := ""
	for line := range strings.SplitSeq(m[1], "\n") {
		if kv := changePlanKeyPattern.FindStringSubmatch(line); kv != nil {
			currentKey = strings.ToLower(kv[1])
			fields[currentKey] = kv[2]
		} else if currentKey != "" {
			fields[currentKey] += "\n" + line
		}
	}
	plan := &codeChangePlan{
		Kind:        strings.ToLower(strings.TrimSpace(fields["kind"])),
		Change:      strings.TrimSpace(fields["change"]),
		TargetValue: strings.TrimSpace(fields["target_value"]),
	}
	for _, part := range strings.FieldsFunc(fields["files"], func(r rune) bool { return r == ',' || r == '\n' }) {
		p := strings.TrimSpace(part)
		p = strings.TrimPrefix(p, "-")
		p = strings.TrimPrefix(p, "*")
		p = strings.TrimSpace(p)
		if p != "" && !strings.HasPrefix(p, "<") {
			plan.Files = append(plan.Files, p)
		}
	}
	if plan.Change == "" && len(plan.Files) == 0 {
		return nil
	}
	if plan.Kind != "source" && plan.Kind != "deployment" {
		plan.Kind = "source"
	}
	return plan
}

// stripChangePlanBlock removes the <change_plan> block (and trailing blank
// space) from investigation text before it is stored in the investigation row,
// fed to synthesis, or shown in the UI. No-op when the block is absent.
func stripChangePlanBlock(s string) string {
	return strings.TrimRight(changePlanBlockPattern.ReplaceAllString(s, ""), " \t\r\n")
}

// marshalCodeChangePlan / unmarshalCodeChangePlan round-trip the plan for the
// log_analysis row. Nil-safe both ways.
func marshalCodeChangePlan(p *codeChangePlan) string {
	if p == nil {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalCodeChangePlan(s string) *codeChangePlan {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var p codeChangePlan
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil
	}
	if p.Change == "" && len(p.Files) == 0 {
		return nil
	}
	return &p
}

// errCodeFixSkipped signals that runCodeFixStage already
// persisted a terminal COMPLETED log_analysis row (with its own status_reason)
// and the caller must NOT re-marshal `response` over it.
var errCodeFixSkipped = errors.New("analyzer: code fix stage skipped - no actionable code change")

// minCodeFixDiffBytes rejects a diff too small to be a real change — the
// "Analysis Response Parse Error" rows in #37005 were 56 bytes.
const minCodeFixDiffBytes = 120

// validateFixAgainstCapabilities is the deterministic, no-LLM check on a
// produced diff (#37005 §5). The diff must be a real change; for a deployment
// change it must edit the annotated values file to the value the investigation
// asked for; for a source change it must touch a file the debug agent named in
// the plan (those paths came from code_analyzer's grep of the actual repo, not
// an LLM guess). Returns "" when the diff passes, otherwise the rejection
// reason.
func validateFixAgainstCapabilities(diff string, plan *codeChangePlan, caps *services_server.EventCodeCapabilities) string {
	if len(diff) < minCodeFixDiffBytes {
		return "skipped - proposed diff too small to be a real fix"
	}
	if plan == nil {
		return ""
	}

	if plan.Kind == "deployment" {
		if caps == nil || caps.Deployment == nil {
			return "skipped - a deployment-values change was proposed but no values file is annotated for this workload"
		}
		if !strings.Contains(diff, caps.Deployment.ValuesPath) {
			return "skipped - proposed diff does not edit the annotated deployment values file"
		}
		if plan.TargetValue != "" && !strings.Contains(diff, plan.TargetValue) {
			return "skipped - proposed diff does not set the value the investigation asked for"
		}
		return ""
	}

	// Source change: the diff must touch at least one file the plan named.
	if len(plan.Files) == 0 {
		return "" // nothing to check the diff against
	}
	changed := diffChangedFiles(diff)
	if len(changed) == 0 {
		return "skipped - proposed diff has no recognisable file headers"
	}
	for _, planned := range plan.Files {
		planned = strings.TrimSpace(planned)
		if planned == "" {
			continue
		}
		plannedBase := planned[strings.LastIndexByte(planned, '/')+1:]
		for _, c := range changed {
			if c == planned || strings.HasSuffix(c, "/"+plannedBase) || c == plannedBase {
				return ""
			}
		}
	}
	return "skipped - proposed diff does not touch the file the investigation named"
}

var diffFileHeaderPattern = regexp.MustCompile(`(?m)^\+\+\+ "?b/(.+?)"?$`)

// diffChangedFiles extracts the repo-relative paths from a unified diff's
// `+++ b/<path>` headers.
func diffChangedFiles(diff string) []string {
	var files []string
	for _, m := range diffFileHeaderPattern.FindAllStringSubmatch(diff, -1) {
		p := strings.TrimSpace(m[1])
		if p != "" && p != "/dev/null" {
			files = append(files, p)
		}
	}
	return files
}

// buildFixModeQuery turns the debug agent's <change_plan> into a short
// code_analyzer fix-mode query. The investigation already localised the change
// with code_analyzer, so this is a one-line implementation instruction plus the
// file(s) it named — not the event, the investigation text, or the raw logs.
func buildFixModeQuery(plan *codeChangePlan, caps *services_server.EventCodeCapabilities) string {
	var b strings.Builder
	b.WriteString(plan.Change)
	if len(plan.Files) > 0 {
		fmt.Fprintf(&b, "\n\nFiles located during the investigation: %s", strings.Join(plan.Files, ", "))
	}
	if plan.Kind == "deployment" && caps != nil && caps.Deployment != nil {
		fmt.Fprintf(&b, "\n\nEdit the deployment values file %s", caps.Deployment.ValuesPath)
		if plan.TargetValue != "" {
			fmt.Fprintf(&b, " to %s", plan.TargetValue)
		}
	}
	return b.String()
}

func runCodeFixStage(ctx *security.RequestContext, request EventAnalysisRequest, response EventAnalysisResponse, parentConversationId string, eventAnalysisRepo *events.EventAnalysisRepository, eventData events.Event, parsedLabels map[string]any, caps *services_server.EventCodeCapabilities, plan *codeChangePlan, recoverConversation bool) (EventAnalysisResponse, error) {
	// skipCodeFix writes the log_analysis row terminal (COMPLETED, with a
	// reason, analysis/summary blanked) and returns errCodeFixSkipped so the
	// caller leaves the row alone. Every non-fix exit of this function goes
	// through it (#37005).
	skipCodeFix := func(reason string) (EventAnalysisResponse, error) {
		if dbErr := eventAnalysisRepo.UpsertEventAnalysisStatus(ctx, request.EventId, eventData.Fingerprint, request.AccountId, eventData.AggregationKey, string(events.AnalysisStatusCompleted), reason, events.AnalysisTypeLog, true); dbErr != nil {
			ctx.GetLogger().Warn("analyzer: failed to persist code analysis skip", "error", dbErr, "reason", reason)
		}
		common.MetricsEventAnalysisOperationsTotal("code_change_plan", "declined", request.AccountId)
		response.Status = string(events.AnalysisStatusCompleted)
		response.StatusReason = reason
		return response, errCodeFixSkipped
	}

	// EVENT_AUTO_RAISE_PR_ENABLED on ⇒ code_analyzer opens the PR directly;
	// off (default) ⇒ it produces a diff only and the existing "Raise PR" card
	// lets the user open it. Fails closed to diff-only on a flag read error.
	raisePR, ffErr := common.IsFeatureEnabled("EVENT_AUTO_RAISE_PR_ENABLED", ctx.GetSecurityContext().GetTenantId())
	if ffErr != nil {
		ctx.GetLogger().Warn("analyzer: failed to read EVENT_AUTO_RAISE_PR_ENABLED, defaulting to diff-only", "error", ffErr, "event_id", request.EventId)
		raisePR = false
	}

	// #37005: what Step 3 received from the debug agent and how it will act on it.
	planKind := ""
	if plan != nil {
		planKind = plan.Kind
	}
	ctx.GetLogger().Info("analyzer: Step 3 code-fix stage entry",
		"event_id", request.EventId,
		"has_change_plan", plan != nil,
		"plan_kind", planKind,
		"git_integration_configured", isGitIntegrationConfigured(request.AccountId),
		"code_capabilities_present", caps.HasAny(),
		"auto_raise_pr_enabled", raisePR)

	if plan == nil {
		return skipCodeFix("skipped - investigation concluded no actionable code change")
	}
	if !isGitIntegrationConfigured(request.AccountId) {
		return skipCodeFix("skipped - no git integration configured")
	}
	if !caps.HasAny() {
		return skipCodeFix("skipped - no repository mapped for this workload")
	}
	// The plan names a kind ("source" | "deployment"); require the matching
	// capability. Without this a deployment plan on a source-only workload (or
	// vice versa) reaches code_analyzer pointed at the wrong repo and produces a
	// diff that can't be verified.
	if plan.Kind == "deployment" && caps.Deployment == nil {
		return skipCodeFix("skipped - a deployment-values change was proposed but no values file is annotated for this workload")
	}
	if plan.Kind == "source" && caps.Source == nil {
		return skipCodeFix("skipped - a source-code change was proposed but no source repository is mapped for this workload")
	}

	common.MetricsEventAnalysisOperationsTotal("code_change_plan", "produced", request.AccountId)

	// Hand the debug agent's localised change straight to code_analyzer in fix
	// mode. The investigation already found the file(s); this is a short
	// implementation call, not a re-investigation.
	llm := agents.CodeAgent2{}
	namespace, workload := resolveEventWorkload(ctx, eventData, parsedLabels)
	eventConfig := toolcore.NBQueryConfig{EventId: request.EventId}
	if namespace != "" && workload != "" {
		eventConfig.Namespace = namespace
		eventConfig.Workload = workload
	}

	query := buildFixModeQuery(plan, caps)
	params := map[string]any{"query": query, "mode": "fix", "raise_pr": raisePR}
	// Pin the repo to the one the plan targets. A deployment-values change lives
	// in the CI / infra repo (caps.Deployment.Repo), NOT the application source
	// repo the workload's git.repo annotation points at — and code_analyzer's
	// own auto-detection prefers the source repo, so it would clone the wrong one.
	if plan.Kind == "deployment" {
		if caps.Deployment != nil && caps.Deployment.Repo != "" {
			params["git_repo"] = caps.Deployment.Repo
			if caps.Deployment.Commit != "" {
				params["git_commit"] = caps.Deployment.Commit
			}
		}
	} else if caps.Source != nil && caps.Source.Repo != "" {
		params["git_repo"] = caps.Source.Repo
		if caps.Source.Commit != "" {
			params["git_commit"] = caps.Source.Commit
		}
	}
	llmParamsJSON, err := json.Marshal(params)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to marshal code agent params to JSON", "error", err, "event_id", request.EventId)
	}

	var llmResponse core.NBAgentResponse
	var hasResponse bool
	if recoverConversation {
		if respStr, found := getAgentResponseFromConversation(ctx, parentConversationId, request.AccountId, llm.GetName()); found {
			llmResponse = core.NBAgentResponse{Response: []string{respStr}, Status: core.ConversationStatusCompleted}
			hasResponse = true
			ctx.GetLogger().Info("analyzer: recovered code analysis response from conversation history", "session_id", parentConversationId)
		}
	}
	if !hasResponse {
		llmResponse, err = core.HandleConversationSessionRequest(
			ctx, llm, request.UserId, request.AccountId, parentConversationId, string(llmParamsJSON),
			core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
			core.ConversationSessionRequestWithConfig(eventConfig),
			core.ConversationSessionRequestWithEnableCritique(true),
		)
	}

	if err != nil {
		if errors.Is(err, core.ErrConversationInProgress) {
			return response, err
		}
		ctx.GetLogger().Warn("analyzer: code analysis fix-mode call errored", "error", err, "event_id", request.EventId)
		return skipCodeFix("code analysis failed: " + err.Error())
	}
	if llmResponse.Status == core.ConversationStatusWaiting || llmResponse.Status == core.ConversationStatusWaitingForClientTool {
		ctx.GetLogger().Info("analyzer: code analysis paused awaiting approval or client tool", "event_id", request.EventId, "status", llmResponse.Status)
		return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress), StatusReason: "code analysis paused awaiting approval"}, core.ErrConversationInProgress
	}
	if llmResponse.Status == core.ConversationStatusFailed {
		reason := "code analysis agent failed"
		if len(llmResponse.Response) > 0 {
			reason = llmResponse.Response[0]
		}
		ctx.GetLogger().Error("analyzer: code analysis conversation failed", "event_id", request.EventId, "reason", reason)
		return skipCodeFix("code analysis failed: " + reason)
	}
	if len(llmResponse.Response) == 0 {
		return skipCodeFix("skipped - code analysis found no change to make")
	}

	codeFixResponse := map[string]any{}
	if err = common.ExtractAndUnmarshalJSON([]byte(llmResponse.Response[0]), &codeFixResponse); err != nil {
		ctx.GetLogger().Error("analyzer: unable to parse code analysis response", "error", err, "event_id", request.EventId)
		return skipCodeFix("code analysis failed: malformed response")
	}

	// Handle new format with nested structure
	var actualResponse map[string]any
	if data, ok := codeFixResponse["data"].(map[string]any); ok {
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
		actualResponse = codeFixResponse
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

	// #37005 §5: deterministic, no-LLM check on the produced diff before it is
	// stored. A rejected diff completes the row empty with the reason.
	if strings.TrimSpace(response.GitDiff) == "" && response.AutomatedFixPR.URL == "" {
		return skipCodeFix("skipped - code analysis found no change to make")
	}
	if reject := validateFixAgainstCapabilities(response.GitDiff, plan, caps); reject != "" {
		// EVENT_AUTO_RAISE_PR_ENABLED opens the PR inside the code_analyzer call
		// above, before this check runs. If the PR is already on GitHub, dropping
		// the row hides it — GithubReview.js reads automated_fix_pr out of this
		// stored analysis. Keep the row and record the reservation in the reason.
		if response.AutomatedFixPR.URL != "" {
			ctx.GetLogger().Warn("analyzer: produced code fix failed consistency check but PR is already open - keeping row",
				"event_id", request.EventId, "reason", reject, "kind", plan.Kind, "pr_url", response.AutomatedFixPR.URL)
			response.StatusReason = "raised as " + response.AutomatedFixPR.URL + "; note: the diff failed a post-hoc consistency check (" + reject + ")"
			return response, nil
		}
		ctx.GetLogger().Warn("analyzer: rejecting produced code fix - failed consistency check",
			"event_id", request.EventId, "reason", reject, "kind", plan.Kind)
		return skipCodeFix(reject)
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

	// FullEvidence keeps the complete collected log body on the row — the
	// analyzer inlines a digest and saves the full body to the conversation
	// workspace, so it must not receive the scratchpad-lean truncation.
	eventTool := tools.EventsExecuteTool{FullEvidence: true}
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
func markAllAnalysisFailed(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventId, fingerprint, accountId, aggKey, errMsg string) {
	if repo == nil || accountId == "" || fingerprint == "" {
		return
	}
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
		existing, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, aType)
		if err != nil || existing == nil {
			continue
		}
		if existing.Status == string(events.AnalysisStatusCompleted) {
			continue
		}
		if updateErr := repo.UpdateEventAnalysisStatusById(ctx, existing.ID, string(events.AnalysisStatusFailed), "analysis failed - "+errMsg); updateErr != nil {
			ctx.GetLogger().Warn("failed to update analysis status on error", "error", updateErr, "analysis_type", aType)
		}
	}
}

// getEventIdentity fetches only the fingerprint and aggregation_key for an event.
// It deliberately avoids getEventData's `select *`, which materialises the
// (potentially multi-MB) evidences column and is what OOMs the process. Used by
// the debug-analysis-disabled gate, which needs the identity to mark analysis
// rows terminal but must not load the event payload.
func getEventIdentity(dbManager *common.DatabaseManager, request EventAnalysisRequest) (string, string, error) {
	if dbManager == nil || dbManager.Db == nil {
		return "", "", fmt.Errorf("database manager is not initialized")
	}
	// uuid.Parse also rejects empty strings, guarding against full-table scans / cross-tenant reads.
	if _, err := uuid.Parse(request.EventId); err != nil {
		return "", "", fmt.Errorf("invalid event_id format")
	}
	if _, err := uuid.Parse(request.AccountId); err != nil {
		return "", "", fmt.Errorf("invalid account_id format")
	}
	var row struct {
		Fingerprint    string `db:"fingerprint"`
		AggregationKey string `db:"aggregation_key"`
	}
	if err := dbManager.Db.Get(&row, `SELECT fingerprint, aggregation_key FROM events WHERE id = $1 AND cloud_account_id = $2`, request.EventId, request.AccountId); err != nil {
		return "", "", err
	}
	// Match the rest of the analyzer (e.g. analyzeEventRCAUsingAgentsAndUpdateDb): an empty
	// fingerprint falls back to the event_id, since stuck rows are keyed that way.
	fingerprint := row.Fingerprint
	if fingerprint == "" {
		fingerprint = request.EventId
	}
	return fingerprint, row.AggregationKey, nil
}

// markAllAnalysisSkipped marks every non-terminal analysis row for an event as
// COMPLETED with a skip reason, leaving already-COMPLETED results untouched.
// Used when event debug analysis is disabled for an account so the recovery loop
// (syncStuckEventAnalyses) stops re-driving the event. Mirrors markAllAnalysisFailed
// but uses a COMPLETED (skipped) terminal state instead of FAILED.
func markAllAnalysisSkipped(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventId, fingerprint, accountId, aggKey, reason string) {
	if repo == nil || accountId == "" || fingerprint == "" {
		return
	}
	for _, aType := range []events.EventAnalysisType{
		events.AnalysisTypeSummary,
		events.AnalysisTypeInvestigation,
		events.AnalysisTypeLog,
		events.AnalysisTypeDetailedResponse,
		events.AnalysisTypeRCA,
	} {
		existing, err := repo.GetEventAnalysis(ctx, eventId, fingerprint, aggKey, accountId, aType)
		if err != nil || existing == nil {
			continue
		}
		if existing.Status == string(events.AnalysisStatusCompleted) {
			continue
		}
		if updateErr := repo.UpdateEventAnalysisStatusById(ctx, existing.ID, string(events.AnalysisStatusCompleted), reason); updateErr != nil {
			ctx.GetLogger().Warn("failed to mark analysis skipped", "error", updateErr, "analysis_type", aType)
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
	if investigation == "" && !events.HasStoredAnalysisContent(logAnalysis) {
		return summary, nil
	}

	// When the investigation reports insufficient/partial evidence, the synthesis
	// prompt's "Evidence Discipline" section keeps this path honest (no fabricated
	// root cause) — we intentionally do not try to detect that verdict in Go by
	// scraping the model's prose, which is brittle and drifts with wording.

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
		core.ConversationStatusInProgress,
	)
	if err != nil {
		return "", fmt.Errorf("synthesizeDetailedResponse: unable to create message record: %w", err)
	}

	systemPrompt, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptEventDetailedResponseSynthesis, request.AccountId)
	if promptErr != nil {
		return "", fmt.Errorf("synthesizeDetailedResponse: loading prompt: %w", promptErr)
	}

	userPrompt := fmt.Sprintf("## Event Summary\n%s", summary)
	if investigation != "" {
		userPrompt += fmt.Sprintf("\n\n## Investigation Findings\n%s", investigation)
	}
	if events.HasStoredAnalysisContent(logAnalysis) {
		userPrompt += fmt.Sprintf("\n\n## Log Analysis\n%s", logAnalysis)
	}
	userPrompt += "\n\nProduce a consolidated markdown analysis combining all of the above." +
		" If the investigation findings include a 'Related Alerts Check' section, carry it into the final" +
		" report verbatim (same section heading), naming each related alert as confirmed, ruled out or not" +
		" assessed — do not summarize it away."

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
