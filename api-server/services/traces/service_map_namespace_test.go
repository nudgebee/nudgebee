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

// TestResolveEndpointNamespace_BarePeerNamespaceFlag covers the rules
// TRACES_SERVICE_MAP_BARE_PEER_NAMESPACE_ENABLED adds: with it on, a bare
// net_peer follows the caller's search path and loopback / metadata hosts stay
// external; with it off, every case resolves exactly as before the flag.
func TestResolveEndpointNamespace_BarePeerNamespaceFlag(t *testing.T) {
	serviceStats := map[string]*serviceMetrics{
		serviceKey("payments", "ledger"): {ServiceName: "ledger", Namespace: "payments"},
	}

	type want struct{ namespace, kind string }
	tests := []struct {
		name          string
		target        string
		peerNamespace string
		depType       string
		on, off       want
	}{
		{"bare net_peer target follows the caller's search path", "ledger", "demo", "net_peer", want{"demo", "Service"}, want{"", "ExternalService"}},
		{"dotted net_peer target is external", "api.stripe.com", "demo", "net_peer", want{"", "ExternalService"}, want{"", "ExternalService"}},
		{"net_peer IP address is external", "10.0.0.5", "demo", "net_peer", want{"", "ExternalService"}, want{"", "ExternalService"}},
		{"localhost is never a namespaced service", "localhost", "demo", "http_client", want{"", "ExternalService"}, want{"demo", "Service"}},
		{"localhost with a port is never a namespaced service", "localhost:8080", "demo", "http_client", want{"", "ExternalService"}, want{"demo", "Service"}},
		{"cloud metadata host is never a namespaced service", "metadata", "demo", "net_peer", want{"", "ExternalService"}, want{"", "ExternalService"}},
		{"IPv6 loopback is never a namespaced service", "::1", "demo", "http_client", want{"", "ExternalService"}, want{"demo", "Service"}},
		{"net_peer from a node-level caller stays external", "collector", "node", "net_peer", want{"", "ExternalService"}, want{"", "ExternalService"}},
	}

	for _, flag := range []string{"true", "false"} {
		t.Run("flag="+flag, func(t *testing.T) {
			t.Setenv(barePeerNamespaceEnvEnabled, flag)
			builder := NewTraceServiceMapBuilder()
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					w := tt.off
					if flag == "true" {
						w = tt.on
					}
					ns, kind := builder.resolveEndpointNamespace(tt.target, tt.peerNamespace, tt.depType, serviceStats, namespacesByName(serviceStats), map[string]*ExternalServiceInfo{})
					if ns != w.namespace || kind != w.kind {
						t.Errorf("resolveEndpointNamespace(%q, %q, %q) = (%q, %q), want (%q, %q)",
							tt.target, tt.peerNamespace, tt.depType, ns, kind, w.namespace, w.kind)
					}
				})
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

// clientSpan builds an OTel HTTP-client span the way the Tempo converter
// delivers it: the callee is named only by net.peer.name, which the converter
// also copies into DestinationName.
func clientSpan(traceID, spanID, parentSpanID, service, namespace, peer string) TraceSpan {
	s := span(traceID, spanID, parentSpanID, service, namespace)
	s.SpanName = "POST"
	s.DestinationName = peer
	s.SpanAttributes["net.peer.name"] = peer
	s.SpanAttributes["http.method"] = "POST"
	return s
}

// linkID renders an application id the way an UpstreamLink id is minted.
func linkID(id ServiceApplicationId) string {
	return id.Namespace + ":" + id.Kind + ":" + id.Name
}

// assertLinksResolve fails for any link whose target id no application carries:
// KubernetesServiceMap draws a phantom node for each one.
func assertLinksResolve(t *testing.T, sm *ServiceMap) {
	t.Helper()
	ids := make(map[string]bool, len(sm.Applications))
	for _, app := range sm.Applications {
		ids[linkID(app.Id)] = true
	}
	for _, app := range sm.Applications {
		for _, u := range app.Upstreams {
			if !ids[u.Id] {
				t.Errorf("%s has an upstream link to %q, which no application carries; applications = %v", linkID(app.Id), u.Id, allIDs(sm))
			}
		}
		for _, d := range app.Downstreams {
			if !ids[linkID(d.Id)] {
				t.Errorf("%s has a downstream link to %q, which no application carries; applications = %v", linkID(app.Id), linkID(d.Id), allIDs(sm))
			}
		}
	}
}

// appsNamed returns every application with the given name, whatever its namespace.
func appsNamed(sm *ServiceMap, name string) []*ServiceApplication {
	var out []*ServiceApplication
	for i := range sm.Applications {
		if sm.Applications[i].Id.Name == name {
			out = append(out, &sm.Applications[i])
		}
	}
	return out
}

// TestBuildServiceMap_WorkloadFilterKeepsCalleeNamespace reproduces the Tempo
// service map under a workload filter. frontend -> payments -> ledger all run in
// "demo". Unfiltered, ledger's own spans are loaded. Filtered by "payments", the
// focused queries load only spans whose source or destination is payments, so
// ledger is known solely as net.peer.name on payments' client span. Both maps
// must show one demo/ledger that payments' link points at.
func TestBuildServiceMap_WorkloadFilterKeepsCalleeNamespace(t *testing.T) {
	t.Setenv(barePeerNamespaceEnvEnabled, "true")
	frontendServer := span("t1", "s1", "", "frontend", "demo")
	frontendClient := clientSpan("t1", "s2", "s1", "frontend", "demo", "payments")
	paymentsServer := span("t1", "s3", "s2", "payments", "demo")
	paymentsClient := clientSpan("t1", "s4", "s3", "payments", "demo", "ledger")
	ledgerServer := span("t1", "s5", "s4", "ledger", "demo")

	shapes := map[string][]TraceSpan{
		"unfiltered": {frontendServer, frontendClient, paymentsServer, paymentsClient, ledgerServer},
		// workload_name = payments, plus destination_workload_name = payments.
		"filtered by payments": {frontendClient, paymentsServer, paymentsClient},
	}

	for name, spans := range shapes {
		t.Run(name, func(t *testing.T) {
			builder := NewTraceServiceMapBuilder()
			builder.AddSpans(spans)
			sm, err := builder.BuildServiceMap()
			if err != nil {
				t.Fatalf("BuildServiceMap() error = %v", err)
			}

			ledgers := appsNamed(sm, "ledger")
			if len(ledgers) != 1 {
				t.Fatalf("got %d ledger applications, want exactly 1; applications = %v", len(ledgers), allIDs(sm))
			}
			ledger := ledgers[0]
			if ledger.Id.Namespace != "demo" || ledger.Id.Kind != "Service" {
				t.Errorf("ledger application id = %s, want demo:Service:ledger", linkID(ledger.Id))
			}

			payments := appFor(sm, "demo", "payments")
			if payments == nil {
				t.Fatalf("no payments application in demo; applications = %v", allIDs(sm))
			}
			found := false
			for _, id := range upstreamIDs(payments) {
				if id == "demo:Service:ledger" {
					found = true
				}
			}
			if !found {
				t.Errorf("payments upstreams = %v, want one equal to demo:Service:ledger", upstreamIDs(payments))
			}

			backToPayments := false
			for _, d := range ledger.Downstreams {
				if d.Id == (ServiceApplicationId{Name: "payments", Kind: "Service", Namespace: "demo"}) {
					backToPayments = true
				}
			}
			if !backToPayments {
				t.Errorf("ledger has no downstream link to demo:Service:payments; downstreams = %v", ledger.Downstreams)
			}

			// The KG merges per-service maps and must tell ledger's synthesized
			// copy (payments' map) from its observed one (ledger's own map).
			if got, want := IsSynthesizedApplication(ledger), name == "filtered by payments"; got != want {
				t.Errorf("IsSynthesizedApplication(ledger) = %v, want %v", got, want)
			}
			if IsSynthesizedApplication(payments) {
				t.Errorf("IsSynthesizedApplication(payments) = true, want false: payments emitted its own spans")
			}

			assertLinksResolve(t, sm)
		})
	}
}

// TestBuildServiceMap_UnobservedTargetIdentity covers targets that emit no spans
// of their own: exactly one application must carry each, and the caller's link
// must point at that application's id. localhost is the exception to "exactly
// one": it is a real host, and the resolver may report it as its own CNAME,
// which mints a second :ExternalService:localhost node, so that case only
// asserts it never becomes a namespaced service.
func TestBuildServiceMap_UnobservedTargetIdentity(t *testing.T) {
	t.Setenv(barePeerNamespaceEnvEnabled, "true")
	httpClient := func(host string) TraceSpan {
		s := span("t1", "s1", "", "checkout", "demo")
		s.SpanAttributes["http.host"] = host
		s.SpanAttributes["http.method"] = "GET"
		return s
	}
	dbClient := clientSpan("t1", "s1", "", "checkout", "demo", "redis")
	dbClient.SpanAttributes["db.system"] = "redis"

	tests := []struct {
		name        string
		span        TraceSpan
		target      string
		wantID      string
		specialHost bool
	}{
		{"bare http host is the caller's namespace", httpClient("inventory"), "inventory", "demo:Service:inventory", false},
		{"localhost is never a namespaced service", httpClient("localhost"), "localhost", ":ExternalService:localhost", true},
		{"dotted external host stays external", httpClient("api.stripe.invalid"), "api.stripe.invalid", ":ExternalService:api.stripe.invalid", false},
		{"database peer named by net.peer.name stays external", dbClient, "redis", ":ExternalService:redis", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewTraceServiceMapBuilder()
			builder.AddSpans([]TraceSpan{tt.span})
			sm, err := builder.BuildServiceMap()
			if err != nil {
				t.Fatalf("BuildServiceMap() error = %v", err)
			}

			checkout := appFor(sm, "demo", "checkout")
			if checkout == nil {
				t.Fatalf("no checkout application; applications = %v", allIDs(sm))
			}

			if tt.specialHost {
				// Independent of the resolver's CNAME answer for the host.
				for _, app := range appsNamed(sm, tt.target) {
					if app.Id.Namespace != "" || app.Id.Kind != "ExternalService" {
						t.Errorf("%s became %s, want only %s", tt.target, linkID(app.Id), tt.wantID)
					}
				}
				ids := upstreamIDs(checkout)
				if len(ids) != 1 || ids[0] != tt.wantID {
					t.Errorf("checkout upstreams = %v, want [%s]", ids, tt.wantID)
				}
				return
			}

			apps := appsNamed(sm, tt.target)
			if len(apps) != 1 {
				t.Fatalf("got %d %s applications, want exactly 1; applications = %v", len(apps), tt.target, allIDs(sm))
			}
			if got := linkID(apps[0].Id); got != tt.wantID {
				t.Errorf("%s application id = %s, want %s", tt.target, got, tt.wantID)
			}
			if ids := upstreamIDs(checkout); len(ids) != 1 || ids[0] != tt.wantID {
				t.Errorf("checkout upstreams = %v, want [%s]", ids, tt.wantID)
			}
			assertLinksResolve(t, sm)
		})
	}
}

func TestDetectDependencyType_DatabasePeerWinsOverNetPeer(t *testing.T) {
	t.Setenv(barePeerNamespaceEnvEnabled, "true")
	builder := NewTraceServiceMapBuilder()
	s := clientSpan("t1", "s1", "", "checkout", "demo", "redis")
	s.SpanAttributes["db.system"] = "redis"
	parsed, _ := builder.parseSpanAttributes(s.SpanAttributes)
	if got := builder.detectDependencyType(s, parsed.Structured); got != "db_connection" {
		t.Errorf("detectDependencyType(net.peer.name=redis, db.system=redis) = %q, want db_connection", got)
	}
}

// A span whose only hint is a database-looking destination name (no attributes
// at all, as some eBPF-fed providers deliver it) must stay a namespace-less
// ExternalService with its database/cache identity, as it was while every
// net_peer target was namespace-opaque.
func TestBuildServiceMap_BareDatabaseNamedPeerStaysExternal(t *testing.T) {
	t.Setenv(barePeerNamespaceEnvEnabled, "true")
	s := span("t1", "s1", "", "checkout", "demo")
	s.SpanName = "redis GET"
	s.DestinationName = "redis-master"
	s.SpanAttributes = map[string]string{}

	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{s})
	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	apps := appsNamed(sm, "redis-master")
	if len(apps) != 1 {
		t.Fatalf("got %d redis-master applications, want exactly 1; applications = %v", len(apps), allIDs(sm))
	}
	redis := apps[0]
	if got := linkID(redis.Id); got != ":ExternalService:redis-master" {
		t.Errorf("redis-master application id = %s, want :ExternalService:redis-master", got)
	}
	if got := redis.Labels["protocol"]; got != "Redis" {
		t.Errorf("redis-master protocol label = %q, want Redis", got)
	}
	hasType := func(want string) bool {
		for _, typ := range redis.Type {
			if typ == want {
				return true
			}
		}
		return false
	}
	if !hasType("redis") || !hasType("cache") {
		t.Errorf("redis-master types = %v, want redis and cache", redis.Type)
	}
	assertLinksResolve(t, sm)
}

// A bare net.peer.name called from a node-level caller (host network, kubelet)
// has no namespace search path to follow, so it stays namespace-opaque as it was
// before net_peer followed the search path: one ExternalService application,
// which the caller's link points at (not a synthesized node:Node:<name>, which
// the KG would read as a worker node).
func TestBuildServiceMap_NodeCallerTargetIsNotSynthesized(t *testing.T) {
	t.Setenv(barePeerNamespaceEnvEnabled, "true")
	s := clientSpan("t1", "s1", "", "gke-pool-node-1", workloadNamespaceSentinelNode, "collector")

	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{s})
	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatalf("BuildServiceMap() error = %v", err)
	}

	apps := appsNamed(sm, "collector")
	if len(apps) != 1 {
		t.Fatalf("got %d collector applications, want exactly 1; applications = %v", len(apps), allIDs(sm))
	}
	if got := linkID(apps[0].Id); got != ":ExternalService:collector" {
		t.Errorf("collector application id = %s, want :ExternalService:collector", got)
	}

	caller := appFor(sm, workloadNamespaceSentinelNode, "gke-pool-node-1")
	if caller == nil {
		t.Fatalf("no node-level caller application; applications = %v", allIDs(sm))
	}
	if ids := upstreamIDs(caller); len(ids) != 1 || ids[0] != ":ExternalService:collector" {
		t.Errorf("caller upstreams = %v, want [:ExternalService:collector]", ids)
	}
	assertLinksResolve(t, sm)
}

// TestBuildServiceMap_WorkloadFilterBarePeerFlag is the Tempo service map
// filtered by payments (frontend -> payments -> ledger, all in "demo"), where
// ledger is known only as net.peer.name on payments' client span. Off, the map
// keeps its pre-flag shape: ledger is :ExternalService:ledger and nothing is
// synthesized into a namespace, so the KG merge's observed-over-synthesized
// preference has nothing namespaced to act on. On, ledger is one demo Service.
func TestBuildServiceMap_WorkloadFilterBarePeerFlag(t *testing.T) {
	filtered := []TraceSpan{
		clientSpan("t1", "s2", "s1", "frontend", "demo", "payments"),
		span("t1", "s3", "s2", "payments", "demo"),
		clientSpan("t1", "s4", "s3", "payments", "demo", "ledger"),
	}

	tests := []struct {
		flag   string
		wantID string
	}{
		{"false", ":ExternalService:ledger"},
		{"true", "demo:Service:ledger"},
	}
	for _, tt := range tests {
		t.Run("flag="+tt.flag, func(t *testing.T) {
			t.Setenv(barePeerNamespaceEnvEnabled, tt.flag)
			builder := NewTraceServiceMapBuilder()
			builder.AddSpans(filtered)
			sm, err := builder.BuildServiceMap()
			if err != nil {
				t.Fatalf("BuildServiceMap() error = %v", err)
			}

			ledgers := appsNamed(sm, "ledger")
			if len(ledgers) != 1 {
				t.Fatalf("got %d ledger applications, want exactly 1; applications = %v", len(ledgers), allIDs(sm))
			}
			if got := linkID(ledgers[0].Id); got != tt.wantID {
				t.Errorf("ledger application id = %s, want %s", got, tt.wantID)
			}

			payments := appFor(sm, "demo", "payments")
			if payments == nil {
				t.Fatalf("no payments application in demo; applications = %v", allIDs(sm))
			}
			found := false
			for _, id := range upstreamIDs(payments) {
				if id == tt.wantID {
					found = true
				}
			}
			if !found {
				t.Errorf("payments upstreams = %v, want one equal to %s", upstreamIDs(payments), tt.wantID)
			}

			if tt.flag == "false" {
				for i := range sm.Applications {
					app := &sm.Applications[i]
					if IsSynthesizedApplication(app) && (app.Id.Namespace != "" || app.Id.Kind != "ExternalService") {
						t.Errorf("flag off synthesized %s; only namespace-less ExternalService applications may be synthesized", linkID(app.Id))
					}
				}
			}
			assertLinksResolve(t, sm)
		})
	}
}

// TestDetectDependencyType_BarePeerFlag pins the dependency-type ordering to
// the flag: off, net.peer.name wins over db.system and a redis-/postgres-/
// mysql- peer is net_peer, as before the flag; on, both are db_connection.
func TestDetectDependencyType_BarePeerFlag(t *testing.T) {
	redisMaster := span("t1", "s1", "", "checkout", "demo")
	redisMaster.SpanName = "redis GET"
	redisMaster.DestinationName = "redis-master"
	redisMaster.SpanAttributes = map[string]string{}

	dbClient := clientSpan("t1", "s1", "", "checkout", "demo", "redis")
	dbClient.SpanAttributes["db.system"] = "redis"

	tests := []struct {
		name    string
		span    TraceSpan
		on, off string
	}{
		{"redis-master peer", redisMaster, "db_connection", "net_peer"},
		{"net.peer.name with db.system", dbClient, "db_connection", "net_peer"},
	}
	for _, flag := range []string{"true", "false"} {
		t.Run("flag="+flag, func(t *testing.T) {
			t.Setenv(barePeerNamespaceEnvEnabled, flag)
			builder := NewTraceServiceMapBuilder()
			for _, tt := range tests {
				want := tt.off
				if flag == "true" {
					want = tt.on
				}
				parsed, _ := builder.parseSpanAttributes(tt.span.SpanAttributes)
				if got := builder.detectDependencyType(tt.span, parsed.Structured); got != want {
					t.Errorf("%s: detectDependencyType = %q, want %q", tt.name, got, want)
				}
			}
		})
	}
}
