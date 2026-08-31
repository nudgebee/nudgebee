package traces

import (
	"testing"
	"time"
)

// span builds a minimal TraceSpan for a service in a namespace.
func span(traceID, spanID, parentSpanID, service, namespace string) TraceSpan {
	return TraceSpan{
		TraceID:           traceID,
		SpanID:            spanID,
		ParentSpanID:      parentSpanID,
		SpanName:          service + " handler",
		WorkloadName:      service,
		WorkloadNamespace: namespace,
		DurationNs:        1_000_000,
		Timestamp:         "2026-08-30T10:00:00Z",
		SpanAttributes: map[string]string{
			"service.name": service,
		},
	}
}

// appFor finds the application for a (namespace, name) pair.
func appFor(sm *ServiceMap, namespace, name string) *ServiceApplication {
	for i := range sm.Applications {
		if sm.Applications[i].Id.Name == name && sm.Applications[i].Id.Namespace == namespace {
			return &sm.Applications[i]
		}
	}
	return nil
}

func upstreamIDs(app *ServiceApplication) []string {
	ids := make([]string, 0, len(app.Upstreams))
	for _, u := range app.Upstreams {
		ids = append(ids, u.Id)
	}
	return ids
}

// TestBuildServiceMap_SameNameDifferentNamespaces is the regression test for the
// reported bug: a caller in one namespace got a CALLS edge to a same-named service
// in a different namespace.
//
// The ns-staging spans are deliberately first, because the old name-keyed
// serviceStats froze a service's namespace to whichever span was seen first — so
// this ordering is what made the caller in "ns-prod" resolve to "ns-staging".
func TestBuildServiceMap_SameNameDifferentNamespaces(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		// ns-staging first: probe -> notification-server
		span("t2", "s2-parent", "", "probe", "ns-staging"),
		span("t2", "s2-child", "s2-parent", "notification-server", "ns-staging"),
		// ns-prod: checkout -> notification-server
		span("t1", "s1-parent", "", "checkout", "ns-prod"),
		span("t1", "s1-child", "s1-parent", "notification-server", "ns-prod"),
	})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	// Both namespaces' notification-server must survive as separate applications.
	if got := appFor(sm, "ns-prod", "notification-server"); got == nil {
		t.Fatalf("no notification-server application in namespace ns-prod; applications = %v", allIDs(sm))
	}
	if got := appFor(sm, "ns-staging", "notification-server"); got == nil {
		t.Fatalf("no notification-server application in namespace ns-staging; applications = %v", allIDs(sm))
	}

	checkout := appFor(sm, "ns-prod", "checkout")
	if checkout == nil {
		t.Fatalf("no checkout application in namespace ns-prod; applications = %v", allIDs(sm))
	}

	const want = "ns-prod:Service:notification-server"
	ids := upstreamIDs(checkout)
	for _, id := range ids {
		if id == want {
			return
		}
		if id == "ns-staging:Service:notification-server" {
			t.Fatalf("checkout in namespace ns-prod got an upstream link to notification-server in ns-staging; upstreams = %v", ids)
		}
	}
	t.Fatalf("checkout upstreams = %v, want one equal to %q", ids, want)
}

// TestBuildServiceMap_BareNameResolvesToCallerNamespace covers the DNS-semantics
// rule: a bare single-label hostname is resolved by Kubernetes inside the caller's
// own namespace, so it must never adopt another namespace's same-named service —
// even when that is the only one observed.
func TestBuildServiceMap_BareNameResolvesToCallerNamespace(t *testing.T) {
	caller := span("t1", "s1", "", "checkout", "ns-prod")
	caller.SpanAttributes["http.host"] = "notification-server"
	caller.SpanAttributes["http.method"] = "GET"

	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		// notification-server is observed ONLY in ns-staging.
		span("t2", "s2", "", "notification-server", "ns-staging"),
		caller,
	})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	checkout := appFor(sm, "ns-prod", "checkout")
	if checkout == nil {
		t.Fatalf("no checkout application in namespace ns-prod; applications = %v", allIDs(sm))
	}
	for _, id := range upstreamIDs(checkout) {
		if id == "ns-staging:Service:notification-server" {
			t.Errorf("bare hostname from namespace ns-prod resolved to ns-staging; upstreams = %v", upstreamIDs(checkout))
		}
	}
}

// TestBuildServiceMap_ExternalTargetHasNoNamespace guards rule 1 of
// resolveEndpointNamespace: a database host must not inherit the caller's namespace.
func TestBuildServiceMap_ExternalTargetHasNoNamespace(t *testing.T) {
	caller := span("t1", "s1", "", "checkout", "ns-prod")
	caller.DestinationName = "postgres-prod.example.com"
	caller.SpanAttributes["db.system"] = "postgresql"

	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{caller})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	checkout := appFor(sm, "ns-prod", "checkout")
	if checkout == nil {
		t.Fatalf("no checkout application; applications = %v", allIDs(sm))
	}
	for _, id := range upstreamIDs(checkout) {
		if id == "ns-prod:Service:postgres-prod.example.com" {
			t.Errorf("external database host inherited the caller's namespace; upstreams = %v", upstreamIDs(checkout))
		}
	}
}

// TestBuildServiceMap_PartialNamespaceDataDoesNotFragment guards the namespace
// backfill: a service whose spans intermittently lack workload_namespace must stay
// one application, not split into a namespaced and an unnamespaced copy.
func TestBuildServiceMap_PartialNamespaceDataDoesNotFragment(t *testing.T) {
	missing := span("t1", "s3", "", "checkout", "")

	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		span("t1", "s1", "", "checkout", "ns-prod"),
		span("t1", "s2", "", "checkout", "ns-prod"),
		missing,
	})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	count := 0
	for _, app := range sm.Applications {
		if app.Id.Name == "checkout" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("checkout split into %d applications, want 1; applications = %v", count, allIDs(sm))
	}
	if app := appFor(sm, "ns-prod", "checkout"); app == nil {
		t.Errorf("checkout did not adopt the sole observed namespace; applications = %v", allIDs(sm))
	}
}

// TestBuildServiceMap_AmbiguousNamespaceIsNotGuessed is the other half of the
// backfill contract: when a name genuinely runs in two namespaces, an unlabelled
// span must NOT be folded into either one.
func TestBuildServiceMap_AmbiguousNamespaceIsNotGuessed(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		span("t1", "s1", "", "notification-server", "ns-prod"),
		span("t2", "s2", "", "notification-server", "ns-staging"),
		span("t3", "s3", "", "notification-server", ""),
	})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	if app := appFor(sm, "", "notification-server"); app == nil {
		t.Errorf("ambiguous unlabelled span was folded into a namespace instead of staying unlabelled; applications = %v", allIDs(sm))
	}
}

// TestBuildServiceMap_UnlabelledServiceKeepsEmptyNamespace guards the resolution
// sweep: a service that is genuinely observed without a namespace (a non-K8s
// workload, or a name too ambiguous for the backfill to label) must not have its
// caller's namespace stamped onto it. If it did, the link id would disagree with
// the application's own id and the matching downstream link would be dropped.
func TestBuildServiceMap_UnlabelledServiceKeepsEmptyNamespace(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		span("t1", "s1-parent", "", "checkout", "ns-prod"),
		// The callee emits no workload_namespace, and its name is observed in two
		// different namespaces, so the backfill must decline to guess and leave
		// this copy unlabelled.
		span("t1", "s1-child", "s1-parent", "legacy-worker", ""),
		span("t2", "s2", "", "legacy-worker", "ns-a"),
		span("t3", "s3", "", "legacy-worker", "ns-b"),
	})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	callee := appFor(sm, "", "legacy-worker")
	if callee == nil {
		t.Fatalf("unlabelled legacy-worker application missing; applications = %v", allIDs(sm))
	}
	if len(callee.Downstreams) == 0 {
		t.Errorf("unlabelled callee lost its downstream link back to checkout; applications = %v", allIDs(sm))
	}

	checkout := appFor(sm, "ns-prod", "checkout")
	if checkout == nil {
		t.Fatalf("checkout application missing; applications = %v", allIDs(sm))
	}
	for _, id := range upstreamIDs(checkout) {
		if id == "ns-prod:Service:legacy-worker" {
			t.Errorf("caller namespace was stamped onto an unlabelled callee; upstreams = %v", upstreamIDs(checkout))
		}
	}
}

// TestBuildServiceMap_NodeSentinelPreserved guards the "node" workload_namespace
// sentinel, which marks cluster-scoped K8s Node traffic rather than a real
// namespace. Namespace-qualifying identity must not disturb it.
func TestBuildServiceMap_NodeSentinelPreserved(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{span("t1", "s1", "", "gke-pool-node-1", "node")})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	app := appFor(sm, "node", "gke-pool-node-1")
	if app == nil {
		t.Fatalf("node application missing; applications = %v", allIDs(sm))
	}
	if app.Id.Kind != "Node" {
		t.Errorf("Kind = %q, want %q", app.Id.Kind, "Node")
	}
}

// TestBuildServiceLinks_DoesNotCrossNamespaces guards the link-attribution fix:
// two same-named services in different namespaces must not absorb each other's
// dependency links.
func TestBuildServiceLinks_DoesNotCrossNamespaces(t *testing.T) {
	builder := NewTraceServiceMapBuilder()

	serviceStats := map[string]*serviceMetrics{
		serviceKey("ns-prod", "api"):     {ServiceName: "api", Namespace: "ns-prod"},
		serviceKey("ns-staging", "api"):  {ServiceName: "api", Namespace: "ns-staging"},
		serviceKey("ns-prod", "billing"): {ServiceName: "billing", Namespace: "ns-prod"},
		serviceKey("ns-staging", "audit"): {
			ServiceName: "audit", Namespace: "ns-staging",
		},
	}
	dependencyMap := map[string]*ServiceDependency{
		depKey("ns-prod", "api", "ns-prod", "billing"): {
			Source: "api", SourceNamespace: "ns-prod",
			Target: "billing", TargetNamespace: "ns-prod",
			CallCount: 10, DependencyType: "trace_relationship",
		},
		depKey("ns-staging", "api", "ns-staging", "audit"): {
			Source: "api", SourceNamespace: "ns-staging",
			Target: "audit", TargetNamespace: "ns-staging",
			CallCount: 10, DependencyType: "trace_relationship",
		},
	}

	links := builder.buildServiceLinks(dependencyMap, serviceStats, map[string]*ExternalServiceInfo{},
		"api", "ns-prod", 1.0, time.Time{}, time.Time{}, true)

	upstreams, ok := links.([]UpstreamLink)
	if !ok {
		t.Fatalf("buildServiceLinks returned %T, want []UpstreamLink", links)
	}
	if len(upstreams) != 1 {
		t.Fatalf("api in ns-prod got %d upstreams, want 1 (it must not absorb the ns-staging copy's links): %v", len(upstreams), upstreams)
	}
	if want := "ns-prod:Service:billing"; upstreams[0].Id != want {
		t.Errorf("upstream Id = %q, want %q", upstreams[0].Id, want)
	}
}

// TestResolveEndpointNamespace covers the resolver's rules directly.
func TestResolveEndpointNamespace(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	serviceStats := map[string]*serviceMetrics{
		serviceKey("ns-prod", "notification-server"):    {ServiceName: "notification-server", Namespace: "ns-prod"},
		serviceKey("ns-staging", "notification-server"): {ServiceName: "notification-server", Namespace: "ns-staging"},
		serviceKey("payments", "ledger"):                {ServiceName: "ledger", Namespace: "payments"},
	}
	externalServices := map[string]*ExternalServiceInfo{
		"redis-cluster.internal": {Name: "redis-cluster.internal"},
	}

	tests := []struct {
		name          string
		target        string
		peerNamespace string
		depType       string
		wantNamespace string
		wantKind      string
	}{
		{"sibling in caller namespace wins", "notification-server", "ns-prod", "http_client", "ns-prod", "Service"},
		{"sibling in the other namespace wins for its own caller", "notification-server", "ns-staging", "http_client", "ns-staging", "Service"},
		{"bare name never adopts another namespace", "notification-server", "checkout-ns", "http_client", "checkout-ns", "Service"},
		{"unambiguous name adopted when caller namespace unknown", "ledger", "", "http_client", "payments", "Service"},
		{"database host carries no namespace", "postgres.example.com", "ns-prod", "db_connection", "", "ExternalService"},
		{"messaging topic never inherits a namespace", "production-dlq", "ns-prod", "messaging_system", "", "ExternalService"},
		{"unknown dotted host is external", "api.github.com", "ns-prod", "http_client", "", "ExternalService"},
		{"node sentinel keeps its kind", "gke-pool-node-1", "node", "http_client", "node", "Node"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, kind := builder.resolveEndpointNamespace(tt.target, tt.peerNamespace, tt.depType, serviceStats, namespacesByName(serviceStats), externalServices)
			if ns != tt.wantNamespace || kind != tt.wantKind {
				t.Errorf("resolveEndpointNamespace(%q, %q, %q) = (%q, %q), want (%q, %q)",
					tt.target, tt.peerNamespace, tt.depType, ns, kind, tt.wantNamespace, tt.wantKind)
			}
		})
	}
}

// TestParseUpstreamId locks the namespace-returning signature.
func TestParseUpstreamId(t *testing.T) {
	tests := []struct {
		id                         string
		wantNS, wantName, wantKind string
	}{
		{"ns-prod:Service:notification-server", "ns-prod", "notification-server", "Service"},
		{":ExternalService:redis", "", "redis", "ExternalService"},
		{"Service:my-service", "", "my-service", "Service"},
		{"bare-name", "", "bare-name", "Service"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			ns, name, kind := ParseUpstreamId(tt.id)
			if ns != tt.wantNS || name != tt.wantName || kind != tt.wantKind {
				t.Errorf("ParseUpstreamId(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.id, ns, name, kind, tt.wantNS, tt.wantName, tt.wantKind)
			}
		})
	}
}

// TestResolveSpanServiceIdentity locks the shared identity derivation that both
// dependency passes now use.
func TestResolveSpanServiceIdentity(t *testing.T) {
	tests := []struct {
		name     string
		span     TraceSpan
		wantName string
		wantNS   string
		wantOK   bool
	}{
		{
			name:     "deployment attribute wins over service.name",
			span:     withAttrs(span("t", "s", "", "checkout-abc123-xyz", "ns-prod"), map[string]string{"k8s.deployment.name": "checkout"}),
			wantName: "checkout", wantNS: "ns-prod", wantOK: true,
		},
		{
			name:     "statefulset attribute",
			span:     withAttrs(span("t", "s", "", "pg-0", "data"), map[string]string{"k8s.statefulset.name": "pg"}),
			wantName: "pg", wantNS: "data", wantOK: true,
		},
		{
			name:     "plain service.name passes through",
			span:     span("t", "s", "", "checkout", "ns-prod"),
			wantName: "checkout", wantNS: "ns-prod", wantOK: true,
		},
		{
			name:     "no identifiable name",
			span:     TraceSpan{TraceID: "t", SpanID: "s"},
			wantName: "", wantNS: "", wantOK: false,
		},
	}

	builder := NewTraceServiceMapBuilder()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, _ := builder.parseSpanAttributes(tt.span.SpanAttributes)
			name, ns, ok := resolveSpanServiceIdentity(tt.span, parsed.Structured, parsed.Raw)
			if name != tt.wantName || ns != tt.wantNS || ok != tt.wantOK {
				t.Errorf("resolveSpanServiceIdentity() = (%q, %q, %v), want (%q, %q, %v)",
					name, ns, ok, tt.wantName, tt.wantNS, tt.wantOK)
			}
		})
	}
}

func withAttrs(s TraceSpan, extra map[string]string) TraceSpan {
	for k, v := range extra {
		s.SpanAttributes[k] = v
	}
	return s
}

func allIDs(sm *ServiceMap) []string {
	out := make([]string, 0, len(sm.Applications))
	for _, app := range sm.Applications {
		out = append(out, app.Id.Namespace+":"+app.Id.Kind+":"+app.Id.Name)
	}
	return out
}
