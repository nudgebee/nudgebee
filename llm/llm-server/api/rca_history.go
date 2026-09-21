package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"time"
)

func applyRCAHistory(response *EventAnalysisResponse, reports []events.RCAReportVersion, attempt *events.RCAAttempt) {
	response.RCAVersions = reports
	if len(reports) > 0 {
		report := reports[0]
		response.RCAReportID = report.ID
		response.Analysis = rcaReportText(report.Analysis)
		response.GeneratedAt = report.GeneratedAt
		response.Status = string(events.AnalysisStatusCompleted)
	}
	if attempt != nil && (response.GeneratedAt == nil || attempt.UpdatedAt.After(*response.GeneratedAt)) {
		response.Status = attempt.Status
		response.AttemptedAt = &attempt.UpdatedAt
		if attempt.Status == string(events.AnalysisStatusFailed) {
			response.StatusReason = "RCA could not complete. Please retry."
		}
	}
	for i := range response.RCAVersions {
		response.RCAVersions[i].Analysis = rcaReportText(response.RCAVersions[i].Analysis)
	}
}

func executeEventRCAAnalysis(ctx *security.RequestContext, request EventRCAAnalysisRequest, c *gin.Context) {
	if request.UserId == "" || request.AccountId == "" || request.EventId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("userId, accountId and eventId are required")}))
		return
	}
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	repo := events.NewEventAnalysisRepository(db)
	info, err := repo.GetEventInfo(ctx, request.EventId, request.AccountId)
	if err != nil {
		c.JSON(404, buildApiResponse(nil, []error{errors.New("event not found")}))
		return
	}
	if info.Fingerprint == "" {
		info.Fingerprint = request.EventId
	}
	response := EventAnalysisResponse{EventId: request.EventId, RelatedEventId: request.EventId, EventFingerprint: info.Fingerprint, EventAggregationKey: info.AggregationKey}
	reports, attempt, err := repo.GetRCAHistory(ctx, request.EventId, info.Fingerprint, request.AccountId, info.AggregationKey)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	applyRCAHistory(&response, reports, attempt)
	if response.GeneratedAt != nil {
		enrichRCAResponseMetadata(ctx, repo, info.Fingerprint, info.AggregationKey, request.AccountId, *response.GeneratedAt, &response)
	}
	if !request.Generate || response.Status == string(events.AnalysisStatusInProgress) || (!request.Regenerate && len(reports) > 0 && (response.GeneratedAt == nil || !repo.IsAnalysisStale(*response.GeneratedAt))) {
		c.JSON(200, buildApiResponse(response, nil))
		return
	}
	// Rollout kill switch only blocks new generation. Reads and recovery remain
	// version-aware so disabling generation cannot revert to overwriting reports.
	disabled, flagErr := common.IsFeatureEnabledForAccount("RCA_REGENERATION_DISABLED", ctx.GetSecurityContext().GetTenantId(), request.AccountId)
	if flagErr != nil || disabled {
		c.JSON(503, buildApiResponse(response, []error{errors.New("RCA generation is temporarily unavailable")}))
		return
	}
	if budget.CheckBudgetAndRespond(c, ctx.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleInvestigation, ctx.GetLogger()) {
		return
	}
	if len(reports) == 0 || !request.Regenerate {
		request.UserId = security.GetSystemUserId()
	}
	id, claimed, err := repo.ClaimRCAAttempt(ctx, request.EventId, info.Fingerprint, request.AccountId, info.AggregationKey, response.RCAReportID)
	if err != nil {
		c.JSON(500, buildApiResponse(response, []error{err}))
		return
	}
	if !claimed {
		reports, attempt, err = repo.GetRCAHistory(ctx, request.EventId, info.Fingerprint, request.AccountId, info.AggregationKey)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{err}))
			return
		}
		response.StatusReason = ""
		response.AttemptedAt = nil
		applyRCAHistory(&response, reports, attempt)
		c.JSON(200, buildApiResponse(response, nil))
		return
	}
	request.AttemptID = id
	submissionCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = getEventAnalysisWorkerPool().Submit(submissionCtx, func() {
		workerCtx := security.NewRequestContext(context.Background(), ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
		start := time.Now()
		_, runErr := analyzeEventRCAUsingAgentsAndUpdateDb(workerCtx, request)
		outcome := "success"
		if runErr != nil {
			outcome = "fail"
			workerCtx.GetLogger().Error("RCA attempt failed", "error", runErr, "attempt_id", id)
		}
		common.MetricsEventAnalysisOperationsTotal(string(events.AnalysisTypeRCA), outcome, request.AccountId)
		common.MetricsEventAnalysisLatencySeconds(string(events.AnalysisTypeRCA), request.AccountId, time.Since(start).Seconds())
	})
	if err != nil {
		_ = repo.UpdateRCAAttemptStatus(ctx, id, request.EventId, request.AccountId, string(events.AnalysisStatusFailed), "queue full, please retry")
		c.JSON(503, buildApiResponse(response, []error{errors.New("unable to queue RCA; please retry")}))
		return
	}
	response.Status = string(events.AnalysisStatusInProgress)
	response.StatusReason = ""
	c.JSON(200, buildApiResponse(response, nil))
}

func rcaRecoverySession(a events.InProgressAnalysis) string {
	if a.AnalysisType == events.AnalysisTypeRCAAttempt {
		return events.RCAAttemptSessionID(a.ID)
	}
	return events.SessionIdPrefixEventRCA + a.EventFingerprint
}

// Preserve the old endpoint's compatibility with JSON-wrapped legacy reports.
func rcaReportText(stored string) string {
	var legacy struct {
		Analysis string `json:"analysis"`
	}
	if json.Unmarshal([]byte(stored), &legacy) == nil && legacy.Analysis != "" {
		stored = legacy.Analysis
	}
	return stripRCAFormatScaffolding(stored)
}
