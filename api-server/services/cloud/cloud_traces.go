package cloud

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// ---- model ----

type TracesQuery struct {
	ServiceName string     `json:"service_name"`
	Filter      string     `json:"filter"`
	TraceId     string     `json:"trace_id"` // when set, fetch this single trace's spans (heatmap/gantt drilldown)
	StartTime   *time.Time `json:"start_time"`
	EndTime     *time.Time `json:"end_time"`
	Limit       *int64     `json:"limit"`
	OrderBy     string     `json:"order_by"`   // Cloud Trace ListTraces order_by, e.g. "start desc"; empty = provider default
	RootsOnly   bool       `json:"roots_only"` // "By Traces" view: request only root spans (ListTraces View=ROOTSPAN) instead of reducing in memory
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

// getCloudAccountNumberById resolves a cloud account's provider account number from its
// internal id. For GCP this number is the project id (the collector sets
// session.ProjectId = account.AccountNumber), which the per-span Cloud Logging drilldown
// filter (trace="projects/<project>/traces/<id>") needs.
func getCloudAccountNumberById(accountId, tenantId string) (string, error) {
	if tenantId == "" {
		return "", errors.New("tenantId cannot be empty")
	}
	// accountId may be a non-UUID sentinel (e.g. "demo"); the id column is uuid, so a
	// non-UUID value would make Postgres error ("invalid input syntax for type uuid").
	// Treat it as a normal miss instead of a wasted, erroring round-trip.
	if _, err := uuid.Parse(accountId); err != nil {
		return "", sql.ErrNoRows
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}
	var accountNumber string
	err = dbms.Db.QueryRowx(
		"SELECT account_number FROM cloud_accounts WHERE id = $1 AND tenant = $2 AND status = 'active'",
		accountId, tenantId,
	).Scan(&accountNumber)
	return accountNumber, err
}

// GetCloudAccountProvider returns the cloud provider (e.g. "GCP", "AWS", "Azure") for an
// active cloud account, or sql.ErrNoRows when the id is not an active cloud account. The
// observability trace-provider resolver uses it to route GCP cloud accounts to the Cloud
// Trace source.
func GetCloudAccountProvider(accountId, tenantId string) (string, error) {
	if tenantId == "" {
		return "", errors.New("tenantId cannot be empty")
	}
	// accountId may be a non-UUID sentinel (e.g. "demo"); the id column is uuid, so a
	// non-UUID value would make Postgres error. Treat it as "not a cloud account".
	if _, err := uuid.Parse(accountId); err != nil {
		return "", sql.ErrNoRows
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", err
	}
	var provider string
	err = dbms.Db.QueryRowx(
		"SELECT cloud_provider FROM cloud_accounts WHERE id = $1 AND tenant = $2 AND status = 'active'",
		accountId, tenantId,
	).Scan(&provider)
	return provider, err
}

// QueryTracesList fetches GCP Cloud Trace spans for a cloud account and maps them into the
// snake_case span shape the frontend trace UI (KubernetesTracesListing) renders. It powers
// the cloud-account Monitoring > Traces tab, reusing the exact same projection as the
// playbook evidence path. Scope: all in-window project traces, optionally narrowed to a
// single Cloud Run service.
func QueryTracesList(ctx *security.RequestContext, req QueryTracesRequest) ([]map[string]any, error) {
	if req.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	// Resolve the GCP project (= account number) so each span carries it for the per-trace
	// Cloud Logging drilldown. Non-fatal: an empty project only disables that drilldown.
	project, err := getCloudAccountNumberById(req.AccountId, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		// A missing/inactive account is an expected miss (the drilldown just degrades);
		// only surface genuinely unexpected lookup failures.
		if !errors.Is(err, sql.ErrNoRows) {
			ctx.GetLogger().Warn("QueryTracesList: could not resolve account project", "account_id", req.AccountId, "error", err)
		}
		project = ""
	}

	// Narrow to a service when requested and no explicit provider-native filter was
	// supplied. Filter on the OTel span label service.name — this is generic (any
	// GCP-hosted, OTel-instrumented service), unlike resource.type="cloud_run_revision"
	// which returns nothing for non-Cloud-Run traces (Cloud SQL, GKE, App Engine, …).
	if req.Query.Filter == "" && req.Query.ServiceName != "" {
		req.Query.Filter = fmt.Sprintf(`service.name:%s`, req.Query.ServiceName)
	}

	resp, err := QueryTraces(ctx, req)
	if err != nil {
		return nil, err
	}
	return MapCloudTraceSpans(resp.Spans, req.Query.ServiceName, project, ""), nil
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
// the existing TracesCard. Scopes by the incident's workload + window. Gated to the GCP
// managed-compute resource types whose requests are exported to Cloud Trace (see
// traceEmittingResourceType).
type cloudTracesAction struct{}

func init() {
	playbooks.RegisterAction("cloud_traces", &cloudTracesAction{})
}

// traceEmittingResourceType reports whether a GCP monitored-resource type is a managed
// compute workload whose requests land in Cloud Trace. Only these qualify for the
// cloud_traces enricher.
//
// Deliberately excluded:
//   - gke_* (nodepool/cluster) — GCP GKE monitoring events are cluster/nodepool-level
//     infra, not a single service, and GKE *workload* spans flow through the node-agent →
//     ClickHouse trace store, not Cloud Trace.
//   - l7_lb_rule / http_load_balancer — the LB emits no spans itself; its backend service
//     would need a separate url-map → backend resolver.
//   - gce_instance, cloudsql_database, redis, pubsub, cloud_tasks_queue, … — no traces.
func traceEmittingResourceType(rt string) bool {
	switch rt {
	case "cloud_run_revision", "gae_app", "cloud_function":
		return true
	default:
		return false
	}
}

func (a *cloudTracesAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	if labels["gcp_account"] == "" && labels["gcp_project_id"] == "" {
		return false
	}
	return traceEmittingResourceType(labels["gcp_event_resource_type"])
}

func (a *cloudTracesAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	serviceName := gcpTraceServiceFromLabels(labels)

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

	// Cloud Trace v1's ListTraces filter has no service/resource term, so the collector
	// returns the project's traces in the window. Narrow them to the incident's workload
	// here (non-destructive — keeps all if the workload can't be matched).
	scopedSpans := filterSpansByService(resp.Spans, serviceName)
	traceCount := distinctTraceCount(scopedSpans)

	spans := MapCloudTraceSpans(scopedSpans, serviceName, project, region)
	return cloudTracesResponse{
		Data:           map[string]any{"data": spans},
		AdditionalInfo: map[string]any{"action_name": "cloud_traces", "title": "Traces"},
		Insight:        generateTraceInsights(scopedSpans, traceCount),
		Metadata:       map[string]any{"service_name": serviceName, "trace_count": traceCount},
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

// gcpTraceServiceFromLabels resolves the workload identifier used to scope Cloud Trace
// spans, keyed by the monitored-resource type (GCP flattens resource.labels onto the
// event as resource_<key>):
//   - cloud_run_revision → resource_service_name  (the Cloud Run service)
//   - gae_app            → resource_module_id      (the App Engine service/module)
//   - cloud_function     → resource_function_name  (the function)
//
// Falls back to the Cloud Run heuristics for older/partial events.
func gcpTraceServiceFromLabels(labels map[string]string) string {
	switch labels["gcp_event_resource_type"] {
	case "gae_app":
		if v := labels["resource_module_id"]; v != "" {
			return v
		}
	case "cloud_function":
		if v := labels["resource_function_name"]; v != "" {
			return v
		}
	}
	return cloudRunServiceFromLabels(labels)
}

// filterSpansByService narrows project-wide Cloud Trace spans to the incident's workload.
// A trace is kept whole (any of its spans matching) so the gantt stays connected. Matching
// uses the high-confidence request-identifying labels only — for Cloud Run/GAE the service
// name is a substring of /http/host (default *.run.app / *.appspot.com hosts) or the URL.
//
// Non-destructive: if nothing matches (empty service, custom domains, or a label shape we
// don't recognize) it returns the spans unchanged, so the card degrades to the prior
// project-window behaviour rather than going empty.
func filterSpansByService(spans []collectorTraceSpan, service string) []collectorTraceSpan {
	if service == "" {
		return spans
	}
	svc := strings.ToLower(service)
	matched := make(map[string]bool)
	for _, s := range spans {
		if spanMatchesService(s, svc) {
			matched[s.TraceId] = true
		}
	}
	if len(matched) == 0 {
		return spans
	}
	out := make([]collectorTraceSpan, 0, len(spans))
	for _, s := range spans {
		if matched[s.TraceId] {
			out = append(out, s)
		}
	}
	return out
}

// spanMatchesService reports whether a span belongs to the given workload (svc already
// lower-cased). Checks the request host/url labels and the span name — the places a
// Cloud Run service / GAE module / function name reliably surfaces.
func spanMatchesService(s collectorTraceSpan, svc string) bool {
	for _, key := range []string{"/http/host", "/http/url", "/http/route", "/http/path"} {
		if v := s.Labels[key]; v != "" && strings.Contains(strings.ToLower(v), svc) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(s.Name), svc)
}

// distinctTraceCount counts unique trace ids across the given spans.
func distinctTraceCount(spans []collectorTraceSpan) int {
	seen := make(map[string]struct{}, len(spans))
	for _, s := range spans {
		seen[s.TraceId] = struct{}{}
	}
	return len(seen)
}

// MapCloudTraceSpans translates the collector's span shape into the snake_case shape
// the TracesCard renders (timestamp ISO8601, duration in ns, HTTP fields from the
// Cloud Trace "/http/*" labels, all labels preserved in span_attributes). Exported so the
// observability GcpTraceSource and the playbook evidence path share the exact same projection.
func MapCloudTraceSpans(spans []collectorTraceSpan, serviceName, project, region string) []map[string]any {
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		labels := s.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		// Cloud Trace spans carry two label conventions: the Cloud Trace agent's
		// "/http/*" keys (Cloud Run root spans) and OTel semantic conventions
		// (OTel-instrumented child spans: http.*, url.*, service.name). Read both,
		// and fall back to Cloud SQL query-plan labels so a single column set works
		// across HTTP-service and database traces.
		httpStatus := firstNonEmpty(labels["/http/status_code"], labels["http.response.status_code"], labels["http.status_code"])
		resource := firstNonEmpty(
			labels["/http/url"], labels["/http/route"], labels["/http/path"],
			labels["http.url"], labels["http.target"], labels["url.path"],
			labels["normalized_query"], labels["Relation Name"], labels["Index Name"],
		)
		// Service identity: OTel service.name, else the requested Cloud Run service,
		// else the Cloud SQL database/instance, else the HTTP host (Cloud Trace agent
		// root spans carry no service.name but do carry /http/host, so "By Traces"
		// rows still get a meaningful Service value).
		svc := firstNonEmpty(labels["service.name"], serviceName, labels["database"], labels["instance"],
			labels["server.address"], labels["/http/host"], labels["http.host"])

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
			"service_name":        svc,
			"workload_name":       svc,
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
