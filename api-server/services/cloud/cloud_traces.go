package cloud

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
	"strconv"
	"time"
)

// ---- model ----

type TracesQuery struct {
	ServiceName string     `json:"service_name"`
	Filter      string     `json:"filter"`
	StartTime   *time.Time `json:"start_time"`
	EndTime     *time.Time `json:"end_time"`
	Limit       *int64     `json:"limit"`
}

type QueryTracesRequest struct {
	AccountId string      `json:"account_id" validate:"required"`
	Query     TracesQuery `json:"query"`
}

// collectorTraceSpan mirrors the cloud-collector's TraceSpanItem (its wire shape).
type collectorTraceSpan struct {
	TraceId      string            `json:"trace_id"`
	SpanId       string            `json:"span_id"`
	ParentSpanId string            `json:"parent_span_id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	StartTime    int64             `json:"start_time"` // unix ms
	DurationMs   float64           `json:"duration_ms"`
	Labels       map[string]string `json:"labels"`
}

type QueryTracesResponse struct {
	Spans      []collectorTraceSpan `json:"spans"`
	TraceCount int                  `json:"trace_count"`
	Status     string               `json:"status"`
}

// ---- collector client ----

type queryTracesResponseWrap struct {
	Data QueryTracesResponse `json:"data"`
}

// QueryTraces fetches distributed traces for an account from the cloud-collector
// (Cloud Trace for GCP). Mirrors QueryLogs.
func QueryTraces(ctx *security.RequestContext, req QueryTracesRequest) (QueryTracesResponse, error) {
	out := QueryTracesResponse{}
	if req.AccountId == "" {
		return out, errors.New("account_id is required")
	}
	if ctx.GetSecurityContext().GetTenantId() == "" {
		return out, errors.New("tenant_id is required")
	}
	if config.Config.CloudCollectorServerUrl == "" {
		return out, errors.New("cloud: cloud collector server url not set")
	}

	headersMap := map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	if config.Config.CloudCollectorServerToken != "" {
		headersMap["X-ACTION-TOKEN"] = config.Config.CloudCollectorServerToken
	}
	headersMap["x-tenant-id"] = ctx.GetSecurityContext().GetTenantId()
	headersMap["x-user-id"] = ctx.GetSecurityContext().GetUserId()

	resp, err := common.HttpPost(fmt.Sprintf("%s/v1/cloud/query_traces", config.Config.CloudCollectorServerUrl),
		common.HttpWithTimeout(30*time.Second), common.HttpWithHeaders(headersMap),
		common.HttpWithJsonBody(map[string]any{"account_id": req.AccountId, "query": req.Query}))
	if err != nil {
		slog.Error("unable to access cloud server", "error", err)
		return out, fmt.Errorf("unable to access cloud server %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("cloud: failed to close response body", "error", cerr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, err
	}
	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("failed to fetch traces from cloud", "status", resp.StatusCode, "account_id", req.AccountId, "data", string(body))
		return out, fmt.Errorf("cloud collector returned status %d: %s", resp.StatusCode, string(body))
	}
	wrap := queryTracesResponseWrap{}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return out, err
	}
	return wrap.Data, nil
}

// ---- enricher ----

type cloudTracesResponse struct {
	Data           map[string]any                            `json:"data"` // {data: [spans]} — the TracesCard envelope
	AdditionalInfo map[string]any                            `json:"additional_info"`
	Insight        []playbooks.PlaybookActionResponseInsight `json:"insight"`
	Metadata       map[string]any                            `json:"metadata"`
}

func (r cloudTracesResponse) GetFormatName() string { return "json" }
func (r cloudTracesResponse) GetData() any          { return r.Data }
func (r cloudTracesResponse) GetAdditionalInfo() map[string]any {
	return r.AdditionalInfo
}
func (r cloudTracesResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return r.Insight
}

// cloudTracesAction attaches distributed traces (Cloud Trace) as evidence, rendered by
// the existing TracesCard. Generic: scopes by the resource + the incident window, no
// incident-type filter. Gated to Cloud Run (the validated trace-exporting case).
type cloudTracesAction struct{}

func init() {
	playbooks.RegisterAction("cloud_traces", &cloudTracesAction{})
}

func (a *cloudTracesAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	if labels["gcp_account"] == "" && labels["gcp_project_id"] == "" {
		return false
	}
	return labels["gcp_event_resource_type"] == "cloud_run_revision"
}

func (a *cloudTracesAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	serviceName := cloudRunServiceFromLabels(labels)

	accountId := ""
	if labels["gcp_account"] != "" {
		id, err := getCloudAccountIdByNumber(labels["gcp_account"], ctx.GetTenantId())
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				ctx.GetLogger().Warn("cloud_traces: could not find cloud account", "account_number", labels["gcp_account"], "error", err)
			}
			return nil, nil
		}
		accountId = id
	}
	if accountId == "" {
		return nil, nil
	}

	return a.Execute(ctx, map[string]any{
		"account_id":   accountId,
		"service_name": serviceName,
		// Carried onto each span so the frontend can build the Cloud Logging
		// trace filter (trace="projects/<project>/traces/<trace_id>") for a
		// per-trace log drill-down.
		"project": labels["gcp_project_id"],
		"region":  labels["gcp_region"],
	})
}

func (a *cloudTracesAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	accountId, _ := rawParams["account_id"].(string)
	serviceName, _ := rawParams["service_name"].(string)
	project, _ := rawParams["project"].(string)
	region, _ := rawParams["region"].(string)
	if accountId == "" {
		return nil, errors.New("account_id is required")
	}

	// Window: around the incident; fall back to the collector's default (last hour).
	var start, end *time.Time
	if s := ctx.GetEvent().StartedAt; s != nil {
		start = s
		if e := ctx.GetEvent().EndedAt; e != nil {
			end = e
		} else {
			t := s.Add(15 * time.Minute)
			end = &t
		}
	}
	limit := int64(20)

	resp, err := QueryTraces(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), QueryTracesRequest{
		AccountId: accountId,
		Query:     TracesQuery{ServiceName: serviceName, StartTime: start, EndTime: end, Limit: &limit},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Spans) == 0 {
		return nil, nil
	}

	spans := mapCloudTraceSpans(resp.Spans, serviceName, project, region)
	return cloudTracesResponse{
		Data:           map[string]any{"data": spans},
		AdditionalInfo: map[string]any{"action_name": "cloud_traces", "title": "Traces"},
		Insight:        generateTraceInsights(resp.Spans, resp.TraceCount),
		Metadata:       map[string]any{"service_name": serviceName, "trace_count": resp.TraceCount},
	}, nil
}

// generateTraceInsights surfaces the RCA-relevant signals from the captured spans:
// the slowest span (the bottleneck) and any 5xx errors, plus a coverage summary.
func generateTraceInsights(spans []collectorTraceSpan, traceCount int) []playbooks.PlaybookActionResponseInsight {
	insights := []playbooks.PlaybookActionResponseInsight{}
	if len(spans) == 0 {
		return insights
	}

	slowestName := ""
	slowestMs := 0.0
	errorCount := 0
	for _, s := range spans {
		if s.DurationMs > slowestMs {
			slowestMs = s.DurationMs
			slowestName = s.Name
		}
		if status := firstNonEmpty(s.Labels["/http/status_code"], s.Labels["http.status_code"]); status != "" {
			if code, err := strconv.Atoi(status); err == nil && code >= 500 {
				errorCount++
			}
		}
	}

	insights = append(insights, playbooks.PlaybookActionResponseInsight{
		Message:  fmt.Sprintf("Captured %d traces (%d spans) around the incident window.", traceCount, len(spans)),
		Severity: "info",
	})
	if slowestName != "" {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Slowest span: %s (%.0f ms).", slowestName, slowestMs),
			Severity: "info",
		})
	}
	if errorCount > 0 {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%d span(s) returned a 5xx error.", errorCount),
			Severity: "warning",
		})
	}
	return insights
}

// cloudRunServiceFromLabels resolves the Cloud Run service name (not the GCP product
// label). Local copy — traces-wt predates the shared helper.
func cloudRunServiceFromLabels(labels map[string]string) string {
	if v := labels["resource_service_name"]; v != "" {
		return v
	}
	if v := labels["gcp_event_instance"]; v != "" && v != labels["gcp_incident_id"] {
		return v
	}
	return ""
}

// mapCloudTraceSpans translates the collector's span shape into the snake_case shape
// the TracesCard renders (timestamp ISO8601, duration in ns, HTTP fields from the
// Cloud Trace "/http/*" labels, all labels preserved in span_attributes).
func mapCloudTraceSpans(spans []collectorTraceSpan, serviceName, project, region string) []map[string]any {
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		labels := s.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		httpStatus := firstNonEmpty(labels["/http/status_code"], labels["http.status_code"])
		resource := firstNonEmpty(labels["/http/url"], labels["/http/route"], labels["/http/path"], labels["http.url"])

		// span_attributes/resource_attributes are JSON strings — the canonical trace
		// shape every consumer (the gantt chart, the server-side gateway) parses. The
		// Span Attributes display tab parses the string itself.
		attrsJSON, _ := json.Marshal(labels)

		out = append(out, map[string]any{
			"trace_id":            s.TraceId,
			"span_id":             s.SpanId,
			"parent_span_id":      s.ParentSpanId,
			"span_name":           s.Name,
			"span_kind":           s.Kind,
			"timestamp":           time.UnixMilli(s.StartTime).UTC().Format("2006-01-02T15:04:05.000Z07:00"),
			"duration_ns":         int64(s.DurationMs * 1e6),
			"status_code":         traceStatusFromHTTP(httpStatus),
			"http_status_code":    httpStatus,
			"resource":            resource,
			"service_name":        serviceName,
			"workload_name":       serviceName,
			"span_attributes":     string(attrsJSON),
			"resource_attributes": "{}",
			// Marks these as Cloud Trace spans so the frontend renders the gantt from
			// this evidence (Cloud Trace isn't in the K8s trace store, so a backend
			// re-fetch by trace_id would return unrelated data).
			"trace_source": "gcp",
			// For the per-trace log drill-down: the frontend builds the Cloud Logging
			// filter trace="projects/<project>/traces/<trace_id>" from these.
			"project": project,
			"region":  region,
		})
	}
	return out
}

// traceStatusFromHTTP derives an OTel-style status from the HTTP status code (Cloud
// Trace v1 spans carry no explicit status). >=500 is an error.
func traceStatusFromHTTP(httpStatus string) string {
	if httpStatus == "" {
		return "STATUS_CODE_UNSET"
	}
	if code, err := strconv.Atoi(httpStatus); err == nil && code >= 500 {
		return "STATUS_CODE_ERROR"
	}
	return "STATUS_CODE_OK"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
