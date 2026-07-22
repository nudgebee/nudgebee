package flow_sources

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"nudgebee/services/traces"
)

// GCPTracesFlowSource derives service-to-service and service-to-resource CALLS edges
// for the Knowledge Graph from GCP Cloud Trace spans.
//
// Flow: per GCP cloud account, fetch spans via cloud.QueryTraces (the cloud-collector's
// /v1/cloud/query_traces → Cloud Trace v1), build a normalized traces.ServiceMap from
// them (the only GCP-specific logic), then hand it to the shared ConvertServiceMapToGraph
// which owns node matching (services → existing Cloud Run ServerlessFunction nodes; managed
// dependencies → ExternalService leaves resolved later by the centralized GCP enricher),
// edge creation, and source/priority tagging.
//
// Gated off by default (KG_GCP_TRACES_FLOW_SOURCE_ENABLED) so it can be rolled out per
// deployment once validated. Coverage is bounded by Cloud Trace's per-query cap, but the
// hourly KG build unions ~24 windows/day via idempotent upsert.
type GCPTracesFlowSource struct {
	*BaseFlowSource
}

const (
	gcpTracesSourceName = "gcp-cloud-traces"
	gcpTracesEnvEnabled = "KG_GCP_TRACES_FLOW_SOURCE_ENABLED"
	// Cloud Trace v1 caps a single query at 100 traces / 800 spans.
	gcpTracesQueryLimit = int64(100)
	// Fallback window when the build request carries no explicit TimeRange.
	gcpTracesDefaultWindow = 2 * time.Hour
)

// OTel/Cloud Trace span label keys read by this source.
const (
	lblFaasName      = "faas.name"
	lblDBSystem      = "db.system"
	lblRPCSystem     = "rpc.system"
	lblRPCService    = "rpc.service"
	lblRPCGrpcStatus = "rpc.grpc.status_code"
)

func init() {
	RegisterFlowSourceFactory(
		gcpTracesSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewGCPTracesFlowSource(logger), nil
		},
		"GCP Cloud Trace flow source: derives service→service and service→resource CALLS edges from Cloud Trace spans",
		string(core.FlowSourceCategoryTracing),
	)
}

// NewGCPTracesFlowSource constructs the source. It is disabled unless the
// KG_GCP_TRACES_FLOW_SOURCE_ENABLED env var is "true" — a disabled source is excluded
// from defaultEnabledFlowSources(), so it never runs until explicitly turned on.
func NewGCPTracesFlowSource(logger *slog.Logger) *GCPTracesFlowSource {
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv(gcpTracesEnvEnabled)), "true")
	return &GCPTracesFlowSource{
		BaseFlowSource: NewBaseFlowSource(gcpTracesSourceName, core.FlowSourceCategoryTracing, enabled, logger),
	}
}

// GetSourceCategory returns the source category.
func (s *GCPTracesFlowSource) GetSourceCategory() core.FlowSourceCategory {
	return core.FlowSourceCategoryTracing
}

// BuildFlowRelationships fetches GCP traces per account, converts them to graph edges/nodes,
// and returns the accumulated set. Accounts with no service attribution (uninstrumented)
// or no traces are skipped without error.
func (s *GCPTracesFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	startTime := time.Now()
	defer s.TrackBuildTime(startTime)

	s.InitializeNodeMatcher(req.ExistingNodes)

	accounts, err := core.GetGCPAccountsForTenant(req.TenantID, req.CloudAccountIDs)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get GCP accounts: %w", err)
	}
	if len(accounts) == 0 {
		s.logger.Info("gcp-cloud-traces: no active GCP accounts for tenant", "tenant_id", req.TenantID)
		return []*core.DbEdge{}, []*core.DbNode{}, nil
	}

	start, end := s.resolveWindow(req)
	limit := gcpTracesQueryLimit

	allEdges := make([]*core.DbEdge, 0)
	allNodes := make([]*core.DbNode, 0)

	for _, acct := range accounts {
		// Cloud trace fetch needs a tenant-admin context (mirrors cloud_traces.AutoExecute).
		traceCtx := security.NewRequestContextForTenantAdmin(req.TenantID, s.logger, nil, nil)
		resp, qErr := cloud.QueryTraces(traceCtx, cloud.QueryTracesRequest{
			AccountId: acct.CloudAccountID,
			Query: cloud.TracesQuery{
				StartTime: &start,
				EndTime:   &end,
				Limit:     &limit,
			},
		})
		if qErr != nil {
			s.logger.Warn("gcp-cloud-traces: query_traces failed",
				"account_id", acct.CloudAccountID, "error", qErr)
			s.IncrementErrorCount()
			continue
		}
		if len(resp.Spans) == 0 {
			continue
		}

		// Copy into a local span slice — cloud.QueryTracesResponse.Spans has an
		// unexported element type that cannot be named in another package's signature.
		spans := make([]gcpTraceSpan, 0, len(resp.Spans))
		for _, sp := range resp.Spans {
			spans = append(spans, gcpTraceSpan{
				TraceID:      sp.TraceId,
				SpanID:       sp.SpanId,
				ParentSpanID: sp.ParentSpanId,
				Name:         sp.Name,
				DurationMs:   sp.DurationMs,
				Labels:       sp.Labels,
			})
		}

		serviceMap := buildGCPServiceMap(spans)
		if serviceMap == nil || len(serviceMap.Applications) == 0 {
			s.logger.Info("gcp-cloud-traces: spans carry no service attribution (uninstrumented account?)",
				"account_id", acct.CloudAccountID, "spans", len(spans))
			continue
		}

		nodes, edges, cErr := ConvertServiceMapToGraph(
			serviceMap, acct.CloudAccountID, req.TenantID, s.BaseFlowSource, s.logger, gcpTracesSourceName,
		)
		if cErr != nil {
			s.logger.Warn("gcp-cloud-traces: service map conversion failed",
				"account_id", acct.CloudAccountID, "error", cErr)
			s.IncrementErrorCount()
			continue
		}

		allEdges = append(allEdges, edges...)
		allNodes = append(allNodes, nodes...)
		// The shared converter builds edges directly (not via CreateEdge), so account
		// for them here to keep LogMetrics() accurate.
		s.AddRelationshipsCreated(len(edges))
		s.logger.Info("gcp-cloud-traces: built relationships for account",
			"account_id", acct.CloudAccountID,
			"applications", len(serviceMap.Applications),
			"nodes", len(nodes), "edges", len(edges))
	}

	s.LogMetrics()
	return allEdges, allNodes, nil
}

// resolveWindow returns the trace query window from the request, or the default lookback.
func (s *GCPTracesFlowSource) resolveWindow(req *core.FlowSourceBuildRequest) (time.Time, time.Time) {
	if req.TimeRange != nil && !req.TimeRange.StartTime.IsZero() && !req.TimeRange.EndTime.IsZero() {
		return req.TimeRange.StartTime, req.TimeRange.EndTime
	}
	end := time.Now()
	return end.Add(-gcpTracesDefaultWindow), end
}

// ---- span → service map ----

// gcpTraceSpan is a local, package-owned projection of a Cloud Trace span (the cloud
// package's wire type is unexported).
type gcpTraceSpan struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	DurationMs   float64
	Labels       map[string]string
}

// linkAgg accumulates per-edge metrics across the spans contributing to one caller→target link.
type linkAgg struct {
	count    float64
	latSum   float64
	failures float64
	protocol string
}

func (a *linkAgg) add(latencyMs float64, failed bool, protocol string) {
	a.count++
	a.latSum += latencyMs
	if failed {
		a.failures++
	}
	if a.protocol == "" && protocol != "" {
		a.protocol = protocol
	}
}

func (a *linkAgg) toUpstream(id string) traces.UpstreamLink {
	avg := 0.0
	if a.count > 0 {
		avg = a.latSum / a.count
	}
	return traces.UpstreamLink{
		Id:           id,
		Latency:      avg,
		RequestCount: a.count,
		FailureCount: a.failures,
		Protocol:     a.protocol,
	}
}

// buildGCPServiceMap turns a flat span list into a traces.ServiceMap of Cloud Run service
// applications, each carrying Upstreams for the other services it calls (service→service)
// and the managed dependencies it calls (service→resource). Direction: a child span whose
// owning service differs from its parent's means parent→child (parent calls child).
func buildGCPServiceMap(spans []gcpTraceSpan) *traces.ServiceMap {
	services := make(map[string]map[string]string) // service name -> representative labels
	// caller -> callee -> agg
	serviceLinks := make(map[string]map[string]*linkAgg)
	// caller -> resource name -> agg
	resourceLinks := make(map[string]map[string]*linkAgg)

	noteService := func(name string, labels map[string]string) {
		if _, ok := services[name]; !ok {
			services[name] = labels
		}
	}

	// Group spans by trace so parent_span_id → span_id lookups stay scoped to a
	// single trace (span IDs are only unique within a trace).
	byTrace := make(map[string][]gcpTraceSpan)
	for _, sp := range spans {
		byTrace[sp.TraceID] = append(byTrace[sp.TraceID], sp)
	}

	for _, traceSpans := range byTrace {
		accumulateTraceLinks(traceSpans, noteService, serviceLinks, resourceLinks)
	}

	if len(services) == 0 {
		return nil
	}

	apps := make([]traces.ServiceApplication, 0, len(services))
	for name, labels := range services {
		upstreams := make([]traces.UpstreamLink, 0)
		for callee, agg := range serviceLinks[name] {
			upstreams = append(upstreams, agg.toUpstream(formatUpstreamID("ServerlessFunction", callee)))
		}
		for resource, agg := range resourceLinks[name] {
			upstreams = append(upstreams, agg.toUpstream(formatUpstreamID("ExternalService", resource)))
		}
		apps = append(apps, traces.ServiceApplication{
			Id:        traces.ServiceApplicationId{Name: name, Kind: "ServerlessFunction"},
			Labels:    labels,
			Upstreams: upstreams,
			IsHealthy: true,
		})
	}

	return &traces.ServiceMap{Applications: apps, GeneratedAt: time.Now()}
}

// accumulateTraceLinks folds one trace's spans into the service/resource link maps.
// Parent→child lookups use a span index local to this trace (span IDs are only unique
// within a trace).
func accumulateTraceLinks(
	traceSpans []gcpTraceSpan,
	noteService func(string, map[string]string),
	serviceLinks, resourceLinks map[string]map[string]*linkAgg,
) {
	byID := make(map[string]gcpTraceSpan, len(traceSpans))
	for _, sp := range traceSpans {
		byID[sp.SpanID] = sp
	}

	// resolveService returns the owning Cloud Run service for a span: its own faas.name,
	// else the nearest ancestor's faas.name within the trace. service.name is deliberately
	// NOT used — on GCP it is frequently a shared app-level label (e.g. "app" reported by
	// oauth, api AND webapp), so falling back to it mints phantom service nodes and
	// self-loop edges from a service's own faas.name-less internal spans. Spans with no
	// faas.name anywhere in their ancestry are unattributable and skipped.
	resolveService := func(sp gcpTraceSpan) string {
		// `visited` guards against cyclic/self-parent spans in malformed telemetry, which
		// would otherwise loop forever (Cloud Trace should be a tree, but we don't trust it).
		visited := make(map[string]bool)
		cur, ok := sp, true
		for ok {
			if v := cur.Labels[lblFaasName]; v != "" {
				return v
			}
			if cur.ParentSpanID == "" || visited[cur.SpanID] {
				break
			}
			visited[cur.SpanID] = true
			cur, ok = byID[cur.ParentSpanID]
		}
		return ""
	}

	for _, sp := range traceSpans {
		svc := resolveService(sp)
		if isNoiseServiceName(svc) { // also covers svc == "" (unattributable)
			continue
		}
		noteService(svc, sp.Labels)

		// service → service: parent (caller) calls this span's service (callee).
		// Internal spans resolve to their own service, so same-service hops collapse.
		if parent, ok := byID[sp.ParentSpanID]; ok {
			parentSvc := resolveService(parent)
			if !isNoiseServiceName(parentSvc) && parentSvc != svc {
				addLink(serviceLinks, parentSvc, svc, sp.DurationMs, spanFailed(sp.Labels), spanProtocol(sp.Labels))
			}
		}

		// service → resource: managed dependency this service calls
		if resource, ok := resolveGCPResource(sp.Labels); ok {
			addLink(resourceLinks, svc, resource, sp.DurationMs, spanFailed(sp.Labels), spanProtocol(sp.Labels))
		}
	}
}

func addLink(m map[string]map[string]*linkAgg, from, to string, latencyMs float64, failed bool, protocol string) {
	if _, ok := m[from]; !ok {
		m[from] = make(map[string]*linkAgg)
	}
	agg, ok := m[from][to]
	if !ok {
		agg = &linkAgg{}
		m[from][to] = agg
	}
	agg.add(latencyMs, failed, protocol)
}

// isNoiseServiceName filters out empty / generic service names that should not become nodes.
func isNoiseServiceName(name string) bool {
	if name == "" {
		return true
	}
	if strings.EqualFold(name, "default") {
		return true
	}
	return isRawIPName(name)
}

// resolveGCPResource classifies an egress span's managed dependency (Redis/Datastore/
// Logging/Monitoring/other gRPC API). Returns ("", false) when the span is not an
// identifiable managed-resource call. Generic HTTP/peer hosts are intentionally not
// emitted as resources (avoids misclassifying internal service-to-service HTTP egress).
func resolveGCPResource(labels map[string]string) (string, bool) {
	if strings.EqualFold(labels[lblDBSystem], "redis") {
		return "Redis", true
	}
	rpcSvc := labels[lblRPCService]
	switch {
	case strings.HasPrefix(rpcSvc, "google.datastore"):
		return "GCP Datastore", true
	case strings.HasPrefix(rpcSvc, "google.logging"):
		return "GCP Cloud Logging", true
	case strings.HasPrefix(rpcSvc, "google.monitoring"):
		return "GCP Cloud Monitoring", true
	}
	if db := labels[lblDBSystem]; db != "" {
		return db, true
	}
	if labels[lblRPCSystem] != "" && rpcSvc != "" {
		return rpcSvc, true
	}
	return "", false
}

// spanFailed reports whether the span represents a failed call (HTTP 5xx or non-OK gRPC).
func spanFailed(labels map[string]string) bool {
	status := firstNonEmptyLabel(labels, "/http/status_code", "http.status_code")
	if code, err := strconv.Atoi(status); err == nil && code >= 500 {
		return true
	}
	if g := labels[lblRPCGrpcStatus]; g != "" && g != "0" {
		return true
	}
	return false
}

// spanProtocol returns a short protocol hint for the link, when derivable.
func spanProtocol(labels map[string]string) string {
	if v := labels[lblDBSystem]; v != "" {
		return v
	}
	if v := labels[lblRPCSystem]; v != "" {
		return v
	}
	if labels["/http/method"] != "" || labels["http.method"] != "" {
		return "http"
	}
	return ""
}

// formatUpstreamID builds the ":Kind:Name" id that traces.ParseUpstreamId expects.
func formatUpstreamID(kind, name string) string {
	return ":" + kind + ":" + name
}

func firstNonEmptyLabel(labels map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}
