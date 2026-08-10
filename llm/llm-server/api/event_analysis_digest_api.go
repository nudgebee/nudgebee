package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// digestHistoryLimit caps the history tab. Weekly rows accrue at 52/year per
// tenant, so this is roughly two years — far past the point anyone scrolls.
const digestHistoryLimit = 104

// ListEventAnalysisDigestsRequest backs events_list_analysis_digests.
//
// No account_id: the review is tenant-wide and the tenant comes from the
// session. Account attribution lives on each finding instead.
type ListEventAnalysisDigestsRequest struct {
	Limit int `json:"limit" mapstructure:"limit"`
}

// GetEventAnalysisDigestRequest backs events_get_analysis_digest. period_start
// is the week's Monday in YYYY-MM-DD — the same key the generator writes.
type GetEventAnalysisDigestRequest struct {
	PeriodStart string `json:"period_start" mapstructure:"period_start"`
}

// GenerateEventAnalysisDigestRequest backs events_generate_analysis_digest.
type GenerateEventAnalysisDigestRequest struct {
	PeriodStart string `json:"period_start" mapstructure:"period_start"`
}

// HandleListEventAnalysisDigestsApi returns the tenant's digest history, newest
// first, for the digest tab's period list.
func HandleListEventAnalysisDigestsApi(
	ctx *security.RequestContext,
	request ListEventAnalysisDigestsRequest,
) ([]events.Digest, error) {
	tenantID, err := assertDigestReadAccess(ctx)
	if err != nil {
		return nil, err
	}

	limit := request.Limit
	if limit <= 0 || limit > digestHistoryLimit {
		limit = digestHistoryLimit
	}

	digests, err := events.ListDigests(ctx, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("HandleListEventAnalysisDigestsApi: %w", err)
	}
	return digests, nil
}

// HandleGetEventAnalysisDigestApi returns one week's digest including its
// per-class findings, which the list endpoint omits.
func HandleGetEventAnalysisDigestApi(
	ctx *security.RequestContext,
	request GetEventAnalysisDigestRequest,
) (events.Digest, error) {
	tenantID, err := assertDigestReadAccess(ctx)
	if err != nil {
		return events.Digest{}, err
	}

	periodStart, err := time.Parse(time.DateOnly, request.PeriodStart)
	if err != nil {
		return events.Digest{}, fmt.Errorf(
			"HandleGetEventAnalysisDigestApi: period_start must be YYYY-MM-DD: %w", err)
	}

	digest, err := events.GetDigest(ctx, tenantID, periodStart)
	if err != nil {
		return events.Digest{}, fmt.Errorf("HandleGetEventAnalysisDigestApi: %w", err)
	}
	return digest, nil
}

// assertDigestReadAccess rejects a caller who cannot see the whole tenant, and
// returns the tenant the digest belongs to.
//
// Tenant-wide access, not per-account: the review spans every account in the
// tenant and names them individually, so an account-level grant would leak the
// incidents, owners and service names of accounts the caller cannot otherwise
// see. Filtering the digest per viewer was the alternative and was rejected —
// the same week would then render differently for different people.
func assertDigestReadAccess(ctx *security.RequestContext) (string, error) {
	sec := ctx.GetSecurityContext()
	if sec == nil {
		return "", fmt.Errorf("digest: missing security context")
	}
	if !sec.HasTenantAccess(security.SecurityAccessTypeRead) {
		return "", fmt.Errorf("digest: forbidden, the weekly review covers every account in the tenant")
	}
	tenantID := sec.GetTenantId()
	if tenantID == "" {
		return "", fmt.Errorf("digest: no tenant on the request")
	}
	return tenantID, nil
}

// HandleGenerateEventAnalysisDigestApi generates one week's digest immediately
// instead of waiting for the next scheduled tick, and returns the stored row.
//
// The result is written with source='on_demand', which the gap scan treats as
// still pending — so the next scheduled run regenerates the week and supersedes
// it. Scheduled output stays authoritative; on-demand is a preview that closes
// the up-to-six-hour wait.
//
// Requires tenant write access: a run costs real LLM spend across every account
// in the tenant, so an account-level grant is not enough to trigger it.
func HandleGenerateEventAnalysisDigestApi(
	ctx *security.RequestContext,
	request GenerateEventAnalysisDigestRequest,
) (events.Digest, error) {
	sec := ctx.GetSecurityContext()
	if sec == nil {
		return events.Digest{}, fmt.Errorf("digest: missing security context")
	}
	// Mutating endpoints in this repo run only on behalf of a real requesting user
	// (see tools/common_apiserver_rpc.go). EffectiveUserIdForRPC collapses the
	// system-user sentinel to "", so this rejects an unattended caller triggering
	// billable LLM generation with no one to attribute the spend to.
	if sec.EffectiveUserIdForRPC() == "" {
		return events.Digest{}, fmt.Errorf(
			"digest: on-demand generation requires a requesting user; refusing to run without one")
	}
	if !sec.HasTenantAccess(security.SecurityAccessTypeCreate) {
		return events.Digest{}, fmt.Errorf("digest: forbidden, generating the weekly review is a tenant-wide action")
	}
	tenantID := sec.GetTenantId()
	if tenantID == "" {
		return events.Digest{}, fmt.Errorf("digest: no tenant on the request")
	}

	periodStart, err := time.Parse(time.DateOnly, request.PeriodStart)
	if err != nil {
		return events.Digest{}, fmt.Errorf(
			"digest: period_start must be YYYY-MM-DD: %w", err)
	}

	// Weeks are Monday-anchored everywhere else — the gap scan only ever emits
	// Mondays — so an arbitrary weekday here would create a row nothing else can
	// match: the scheduler would never supersede it and the UI would show a range
	// overlapping its neighbours.
	if periodStart.Weekday() != time.Monday {
		return events.Digest{}, fmt.Errorf(
			"digest: period_start must be a Monday (got %s)", periodStart.Weekday())
	}

	// The scheduler only digests completed weeks, but on-demand may preview the
	// week in progress: the row is written as on_demand, so once the week closes
	// the gap scan re-queues it and the scheduled run replaces the partial view
	// with the real one. A future week has no data at all and is refused.
	if periodStart.After(currentWeekStart()) {
		return events.Digest{}, fmt.Errorf(
			"digest: %s is a future week", request.PeriodStart)
	}

	period := events.DigestPeriod{
		TenantID:    tenantID,
		PeriodStart: periodStart,
		PeriodEnd:   periodStart.AddDate(0, 0, 7),
	}

	// Detached from the request's cancellation: a run takes minutes and can reach
	// the generation bound, so a gateway or client timeout would otherwise abort
	// it mid-way and waste the LLM spend already incurred. WithoutCancel keeps the
	// request's trace and logger values; the explicit timeout still bounds it.
	detached, cancel := context.WithTimeout(
		context.WithoutCancel(ctx.GetContext()), digestGenerationTimeout)
	defer cancel()
	genCtx := security.NewRequestContext(detached, sec, ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())

	if genErr := generateDigestForPeriod(genCtx, period, events.DigestSourceOnDemand); genErr != nil {
		return events.Digest{}, fmt.Errorf("HandleGenerateEventAnalysisDigestApi: %w", genErr)
	}

	digest, err := events.GetDigest(ctx, tenantID, periodStart)
	if err != nil {
		return events.Digest{}, fmt.Errorf("HandleGenerateEventAnalysisDigestApi: reading back: %w", err)
	}
	return digest, nil
}

// bindDigestRequest decodes an RPC action envelope into target and builds the
// request context, writing the error response itself. Returns ok=false when the
// caller should stop. The two digest routes share it because their only
// difference is the payload struct.
func bindDigestRequest(
	c *gin.Context,
	tracer trace.Tracer,
	meter metric.Meter,
	target any,
) (*security.RequestContext, bool) {
	requestMap := make(map[string]any)
	if err := c.ShouldBindJSON(&requestMap); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
			common.Error{Message: "api: " + err.Error()},
		}))
		return nil, false
	}

	var actionRequest ActionRequest
	if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
			common.Error{Message: "api: " + err.Error()},
		}))
		return nil, false
	}

	payload := actionRequest.Input
	if reqVal, ok := payload["request"].(map[string]any); ok {
		payload = reqVal
	}
	if payload == nil {
		payload = requestMap
	}
	if err := common.DecodeMapToStruct(payload, target); err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
			common.Error{Message: "api: " + err.Error()},
		}))
		return nil, false
	}

	reqCtx, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
	if err != nil {
		c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
		return nil, false
	}
	return reqCtx, true
}

// currentWeekStart is the Monday of the week in progress, matching the
// date_trunc('week', now()) boundary the gap scan generates from.
func currentWeekStart() time.Time {
	now := time.Now().UTC()
	offset := (int(now.Weekday()) + 6) % 7 // Monday = 0
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
}
