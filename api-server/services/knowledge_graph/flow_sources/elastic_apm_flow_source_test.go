package flow_sources

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	apmAcctA = "acct-a"
	apmAcctB = "acct-b"
)

var apmNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// apmFixture is a two-account tenant: both accounts run demo/frontend and
// demo/product-catalog (the dev/test-identical-workloads case), account A also
// runs the nudgebee namespace and a ClusterIP Service for frontend.
type apmFixture struct {
	nodes    []*core.DbNode
	byID     map[string]*core.DbNode
	accounts []elasticAPMAccount
}

func apmWorkload(id, account, name, namespace string) *core.DbNode {
	return workloadNodeInAccount(id, account, "Deployment", name, namespace, account+"-cluster")
}

func newAPMFixture() *apmFixture {
	f := &apmFixture{byID: map[string]*core.DbNode{}}
	add := func(n *core.DbNode) *core.DbNode {
		f.nodes = append(f.nodes, n)
		f.byID[n.ID] = n
		return n
	}
	aFrontend := add(apmWorkload("a-frontend", apmAcctA, "frontend", "demo"))
	aCatalog := add(apmWorkload("a-catalog", apmAcctA, "product-catalog", "demo"))
	aProxy := add(apmWorkload("a-proxy", apmAcctA, "frontend-proxy", "demo"))
	aCart := add(apmWorkload("a-cart", apmAcctA, "cart", "demo"))
	aMl := add(apmWorkload("a-ml", apmAcctA, "ml-k8s-server", "nudgebee"))
	add(apmWorkload("a-relay", apmAcctA, "relay-server", "nudgebee"))
	a2Frontend := add(apmWorkload("a2-frontend", apmAcctA, "frontend", "demo2"))
	a2Catalog := add(apmWorkload("a2-catalog", apmAcctA, "product-catalog", "demo2"))
	svc := makeServiceNode("frontend", apmAcctA+"-cluster", "34.118.232.107")
	svc.ID, svc.CloudAccountID = "a-frontend-svc", apmAcctA
	svc.Properties["namespace"] = "demo"
	add(svc)
	bFrontend := add(apmWorkload("b-frontend", apmAcctB, "frontend", "demo"))
	bCatalog := add(apmWorkload("b-catalog", apmAcctB, "product-catalog", "demo"))

	f.accounts = []elasticAPMAccount{
		{id: apmAcctA, pods: apmPods(map[[2]string]*core.DbNode{
			{"demo", "frontend-aaa-1"}:         aFrontend,
			{"demo", "product-catalog-aaa-1"}:  aCatalog,
			{"demo", "frontend-proxy-aaa-1"}:   aProxy,
			{"demo", "cart-aaa-1"}:             aCart,
			{"nudgebee", "ml-k8s-server-a-1"}:  aMl,
			{"demo2", "frontend-ddd-1"}:        a2Frontend,
			{"demo2", "product-catalog-ddd-1"}: a2Catalog,
		})},
		{id: apmAcctB, pods: apmPods(map[[2]string]*core.DbNode{
			{"demo", "frontend-bbb-1"}:        bFrontend,
			{"demo", "product-catalog-bbb-1"}: bCatalog,
		})},
	}
	return f
}

// apmPods builds an account's pod-name index directly (the production path fills
// it from kube_pod_info and k8s_pods).
func apmPods(pods map[[2]string]*core.DbNode) *PodIPResolver {
	r := &PodIPResolver{
		byClusterIP:        map[clusterIPKey]*core.DbNode{},
		byIPAcrossClusters: map[string][]*core.DbNode{},
		byNamespaceName:    map[nsNameKey]*core.DbNode{},
		byNameGlobal:       map[string][]*core.DbNode{},
	}
	for k, n := range pods {
		r.byNamespaceName[nsNameKey{k[0], k[1]}] = n
	}
	r.rebuildGlobalNameIndex()
	return r
}

func (f *apmFixture) build(t *testing.T, data *observability.ElasticAPMDependencies) ([]*core.DbEdge, []*core.DbNode) {
	t.Helper()
	s := NewElasticAPMFlowSource(slog.Default())
	s.InitializeNodeMatcher(f.nodes)
	return s.buildGroupGraph(data, f.accounts, apmNow.Add(-time.Hour), "tenant",
		NewK8sServiceIPResolver(f.nodes), NewK8sNodeIPResolver(f.nodes))
}

func recentPod(service, namespace, pod string) observability.ElasticAPMServicePod {
	return observability.ElasticAPMServicePod{Service: service, Namespace: namespace, Pod: pod, LastSeen: apmNow.Add(-5 * time.Minute)}
}

// edgeIndex maps "src->dst" (node ids) to the edge.
func edgeIndex(edges []*core.DbEdge) map[string]*core.DbEdge {
	out := map[string]*core.DbEdge{}
	for _, e := range edges {
		out[e.SourceNodeID+"->"+e.DestinationNodeID] = e
	}
	return out
}

func TestElasticAPM_ReceiverJoinBuildsWorkloadEdgeWithMetrics(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Dependencies: []observability.ElasticAPMDependency{
			{CallerService: "frontend", Resource: "product-catalog:8080", TargetType: "grpc", Calls: 7320, Failures: 12, LatencySumUs: 7320 * 62400},
		},
		Calls: []observability.ElasticAPMCall{{
			CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: "product-catalog:8080",
			ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1",
		}},
		Pods: []observability.ElasticAPMServicePod{recentPod("frontend", "demo", "frontend-aaa-1")},
	}
	edges, nodes := f.build(t, data)
	require.Len(t, edges, 1)
	assert.Empty(t, nodes)
	e := edges[0]
	assert.Equal(t, "a-frontend", e.SourceNodeID)
	assert.Equal(t, "a-catalog", e.DestinationNodeID)
	assert.Equal(t, core.RelationshipCalls, e.RelationshipType)
	assert.Equal(t, apmAcctA, e.CloudAccountID)
	assert.Equal(t, "receiver", e.Properties["dependency_type"])
	assert.Equal(t, "grpc", e.Properties["protocol"])
	assert.Equal(t, elasticAPMScopeAccount, e.Properties["apm_metrics_scope"])
	assert.Equal(t, int64(7320), e.Properties["request_count"])
	assert.Equal(t, int64(12), e.Properties["failure_count"])
	assert.InDelta(t, 62.4, e.Properties["latency_ms"], 0.001)
	assert.Equal(t, "elastic-apm", e.Properties["created_by_flow_source"])
}

func TestElasticAPM_BareIPResolvedThroughReceiverOrClusterIP(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Dependencies: []observability.ElasticAPMDependency{{CallerService: "frontend-proxy", Resource: "34.118.232.107:8080", Calls: 10}},
		// No receiver sampled: the ClusterIP resolver has to place the IP.
		Calls: []observability.ElasticAPMCall{{
			CallerService: "frontend-proxy", CallerNamespace: "demo", CallerPod: "frontend-proxy-aaa-1", Resource: "34.118.232.107:8080",
		}},
	}
	edges, nodes := f.build(t, data)
	require.Len(t, edges, 1)
	assert.Empty(t, nodes, "an IP must never become an ExternalService node")
	assert.Equal(t, "a-frontend-svc", edges[0].DestinationNodeID)
	assert.Equal(t, "ip", edges[0].Properties["dependency_type"])
	assert.Equal(t, IPResolutionSourceClusterIP, edges[0].Properties["resolution_source"])
}

func TestElasticAPM_UnresolvableIPAndJunkResourceAreDropped(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Calls: []observability.ElasticAPMCall{
			{CallerService: "ml-k8s-server", CallerNamespace: "nudgebee", CallerPod: "ml-k8s-server-a-1", Resource: "10.64.3.22:8080"},
			{CallerService: "ml-k8s-server", CallerNamespace: "nudgebee", CallerPod: "ml-k8s-server-a-1", Resource: "169.254.169.254:80"},
			{CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: ":0"},
		},
	}
	edges, nodes := f.build(t, data)
	assert.Empty(t, edges)
	assert.Empty(t, nodes)
}

func TestElasticAPM_JunkResourceStillResolvesThroughItsReceiver(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Calls: []observability.ElasticAPMCall{{
			CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: ":0",
			ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1",
		}},
	}
	edges, _ := f.build(t, data)
	require.Len(t, edges, 1)
	assert.Equal(t, "a-catalog", edges[0].DestinationNodeID)
}

func TestElasticAPM_TechnologyOnlyResourceIsNamespaceScopedAmbiguousStub(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Dependencies: []observability.ElasticAPMDependency{{CallerService: "cart", Resource: "redis", TargetType: "redis", Calls: 5}},
		Calls: []observability.ElasticAPMCall{
			{CallerService: "cart", CallerNamespace: "demo", CallerPod: "cart-aaa-1", Resource: "redis"},
			{CallerService: "frontend", CallerNamespace: "demo2", CallerPod: "frontend-ddd-1", Resource: "redis"},
			{CallerService: "cart", CallerNamespace: "demo", CallerPod: "cart-aaa-1", Resource: "kafka/orders"},
		},
	}
	edges, nodes := f.build(t, data)
	require.Len(t, edges, 3)
	require.Len(t, nodes, 3, "redis in demo, redis in demo2, and kafka/orders are three nodes")

	byName := map[string][]*core.DbNode{}
	for _, n := range nodes {
		assert.Equal(t, core.NodeTypeExternalService, n.NodeType)
		byName[n.Properties["name"].(string)] = append(byName[n.Properties["name"].(string)], n)
	}
	require.Len(t, byName["redis"], 2)
	assert.NotEqual(t, byName["redis"][0].ID, byName["redis"][1].ID, "namespaces must not share one redis stub")
	for _, n := range byName["redis"] {
		assert.Equal(t, true, n.Properties["ambiguous"])
	}
	require.Len(t, byName["kafka/orders"], 1)
	assert.Nil(t, byName["kafka/orders"][0].Properties["ambiguous"], "a named resource is not ambiguous")
	for _, e := range edges {
		assert.Equal(t, "external", e.Properties["dependency_type"])
	}
}

func TestElasticAPM_UnresolvedCallerIsSkipped(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Calls: []observability.ElasticAPMCall{{
			// A pod from a cluster that is not connected: no account knows it.
			CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-zzz-9", Resource: "product-catalog:8080",
			ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-zzz-9",
		}},
	}
	edges, nodes := f.build(t, data)
	assert.Empty(t, edges, "an unknown pod must not fall back to the same-named workload of another cluster")
	assert.Empty(t, nodes)
}

func TestElasticAPM_EachAccountGetsOnlyItsOwnPodsEdges(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Dependencies: []observability.ElasticAPMDependency{{CallerService: "frontend", Resource: "product-catalog:8080", Calls: 100}},
		Calls: []observability.ElasticAPMCall{
			{CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: "product-catalog:8080",
				ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1"},
			{CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-bbb-1", Resource: "product-catalog:8080",
				ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-bbb-1"},
		},
		Pods: []observability.ElasticAPMServicePod{
			recentPod("frontend", "demo", "frontend-aaa-1"),
			recentPod("frontend", "demo", "frontend-bbb-1"),
		},
	}
	edges, _ := f.build(t, data)
	idx := edgeIndex(edges)
	require.Len(t, edges, 2)
	require.Contains(t, idx, "a-frontend->a-catalog")
	require.Contains(t, idx, "b-frontend->b-catalog")
	for _, e := range edges {
		assert.Equal(t, elasticAPMScopeShared, e.Properties["apm_metrics_scope"],
			"frontend runs in two accounts: the service-level total belongs to neither")
		assert.NotContains(t, e.Properties, "request_count")
		assert.NotContains(t, e.Properties, "latency_ms")
	}
}

func TestElasticAPM_MetricOwnershipIgnoresRetiredPodsButNotUnknownLivePods(t *testing.T) {
	f := newAPMFixture()
	base := func(pods ...observability.ElasticAPMServicePod) *observability.ElasticAPMDependencies {
		return &observability.ElasticAPMDependencies{
			Dependencies: []observability.ElasticAPMDependency{{CallerService: "frontend", Resource: "product-catalog:8080", Calls: 100}},
			Calls: []observability.ElasticAPMCall{{
				CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: "product-catalog:8080",
				ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1",
			}},
			Pods: pods,
		}
	}
	retired := observability.ElasticAPMServicePod{Service: "frontend", Namespace: "demo", Pod: "frontend-old-1", LastSeen: apmNow.Add(-20 * time.Hour)}
	unknownLive := recentPod("frontend", "demo", "frontend-zzz-9")

	t.Run("pod retired by a rollout earlier in the window", func(t *testing.T) {
		edges, _ := f.build(t, base(recentPod("frontend", "demo", "frontend-aaa-1"), retired))
		require.Len(t, edges, 1)
		assert.Equal(t, elasticAPMScopeAccount, edges[0].Properties["apm_metrics_scope"])
	})
	t.Run("live pod in an unconnected cluster", func(t *testing.T) {
		edges, _ := f.build(t, base(recentPod("frontend", "demo", "frontend-aaa-1"), unknownLive))
		require.Len(t, edges, 1)
		assert.Equal(t, elasticAPMScopeShared, edges[0].Properties["apm_metrics_scope"])
	})
}

func TestElasticAPM_SameServiceNameInTwoNamespacesNeverCrossesOver(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Dependencies: []observability.ElasticAPMDependency{{CallerService: "frontend", Resource: "product-catalog:8080", Calls: 100}},
		Calls: []observability.ElasticAPMCall{
			{CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: "product-catalog:8080",
				ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1"},
			{CallerService: "frontend", CallerNamespace: "demo2", CallerPod: "frontend-ddd-1", Resource: "product-catalog:8080",
				ReceiverService: "product-catalog", ReceiverNamespace: "demo2", ReceiverPod: "product-catalog-ddd-1"},
		},
		Pods: []observability.ElasticAPMServicePod{
			recentPod("frontend", "demo", "frontend-aaa-1"),
			recentPod("frontend", "demo2", "frontend-ddd-1"),
		},
	}
	edges, _ := f.build(t, data)
	idx := edgeIndex(edges)
	require.Len(t, edges, 2)
	assert.Contains(t, idx, "a-frontend->a-catalog")
	assert.Contains(t, idx, "a2-frontend->a2-catalog")
	assert.NotContains(t, idx, "a-frontend->a2-catalog")
	assert.NotContains(t, idx, "a2-frontend->a-catalog")
	for _, e := range edges {
		assert.Equal(t, elasticAPMScopeShared, e.Properties["apm_metrics_scope"], "one service name across two namespaces")
	}
}

func TestElasticAPM_ShortHostnameResolvesOnlyInCallersNamespace(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Calls: []observability.ElasticAPMCall{
			// The receiver reported service.name "unknown", so the read path left it
			// empty: only the hostname is left to go on.
			{CallerService: "ml-k8s-server", CallerNamespace: "nudgebee", CallerPod: "ml-k8s-server-a-1", Resource: "relay-server:8080"},
			// Same short name from a namespace with no relay-server.
			{CallerService: "cart", CallerNamespace: "demo", CallerPod: "cart-aaa-1", Resource: "relay-server:8080"},
		},
	}
	edges, nodes := f.build(t, data)
	idx := edgeIndex(edges)
	require.Len(t, edges, 2)
	require.Contains(t, idx, "a-ml->a-relay")
	assert.Equal(t, "hostname", idx["a-ml->a-relay"].Properties["dependency_type"])
	require.Len(t, nodes, 1)
	assert.Equal(t, "relay-server:8080", nodes[0].Properties["name"],
		"from demo, a short name must not resolve to nudgebee/relay-server")
	assert.Contains(t, idx, "a-cart->"+nodes[0].ID)
}

func TestElasticAPM_K8sDNSNameUsesItsOwnNamespace(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		Calls: []observability.ElasticAPMCall{{
			CallerService: "cart", CallerNamespace: "demo", CallerPod: "cart-aaa-1", Resource: "relay-server.nudgebee.svc.cluster.local:8080",
		}},
	}
	edges, _ := f.build(t, data)
	require.Len(t, edges, 1)
	assert.Equal(t, "a-relay", edges[0].DestinationNodeID)
}

func TestElasticAPM_QuietPairFallsBackToNewestPod(t *testing.T) {
	f := newAPMFixture()
	data := &observability.ElasticAPMDependencies{
		// In read 1 (24h) but not sampled in the last hour.
		Dependencies: []observability.ElasticAPMDependency{{CallerService: "ml-k8s-server", Resource: "relay-server:8080", Calls: 8}},
		Pods: []observability.ElasticAPMServicePod{
			{Service: "ml-k8s-server", Namespace: "nudgebee", Pod: "ml-k8s-server-old-1", LastSeen: apmNow.Add(-20 * time.Hour)},
			{Service: "ml-k8s-server", Namespace: "nudgebee", Pod: "ml-k8s-server-a-1", LastSeen: apmNow.Add(-3 * time.Hour)},
		},
	}
	edges, _ := f.build(t, data)
	require.Len(t, edges, 1)
	assert.Equal(t, "a-ml", edges[0].SourceNodeID)
	assert.Equal(t, "a-relay", edges[0].DestinationNodeID)
	assert.Equal(t, elasticAPMScopeShared, edges[0].Properties["apm_metrics_scope"],
		"the retired pod is not in any account and nothing is recent, so all pods are judged")
}

func TestElasticAPM_InProcessDuplicateEdgesCollapse(t *testing.T) {
	f := newAPMFixture()
	call := observability.ElasticAPMCall{
		CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: "product-catalog:8080",
		ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1",
	}
	call2 := call
	call2.Resource = "product-catalog.demo.svc.cluster.local:8080"
	edges, _ := f.build(t, &observability.ElasticAPMDependencies{Calls: []observability.ElasticAPMCall{call, call, call2}})
	assert.Len(t, edges, 1, "one edge per (caller node, destination node)")
}

func TestElasticAPM_BuildRunsOnlyForElasticAPMSchemaAndFetchesOncePerIndex(t *testing.T) {
	f := newAPMFixture()
	origFetch, origCfg, origAccts, origPods := fetchElasticAPMDeps, getElasticAPMESConfig, getElasticAPMAccounts, newElasticAPMPodLookup
	t.Cleanup(func() {
		fetchElasticAPMDeps, getElasticAPMESConfig, getElasticAPMAccounts, newElasticAPMPodLookup = origFetch, origCfg, origAccts, origPods
	})

	getElasticAPMAccounts = func(string, []string) ([]core.K8sAccount, error) {
		return []core.K8sAccount{
			{CloudAccountID: apmAcctA}, {CloudAccountID: apmAcctB}, {CloudAccountID: "acct-no-es"},
			{CloudAccountID: "acct-otel"}, {CloudAccountID: "acct-own-index"},
		}, nil
	}
	getElasticAPMESConfig = func(_ *security.RequestContext, accountID string) (*observability.ElasticsearchConfig, error) {
		cfg := &observability.ElasticsearchConfig{IntegrationID: "es-1", Url: "https://es", TraceSchema: observability.ESTraceSchemaElasticAPM}
		switch accountID {
		case "acct-no-es":
			return nil, observability.ErrNoElasticsearchIntegration
		case "acct-otel":
			// Declares another trace layout: never queried.
			cfg.TraceSchema = observability.ESTraceSchemaOtelNative
		case "acct-own-index":
			// Same integration, per-account trace index: reads different data.
			cfg.TraceIndex = "apm-prod-traces*"
		}
		return cfg, nil
	}
	podsByAccount := map[string]*PodIPResolver{}
	for _, a := range f.accounts {
		podsByAccount[a.id] = a.pods
	}
	newElasticAPMPodLookup = func(_ *security.RequestContext, accountID string, _ []*core.DbNode, _ *slog.Logger) *PodIPResolver {
		return podsByAccount[accountID]
	}
	fetchedIndexes := []string{}
	fetchElasticAPMDeps = func(_ *security.RequestContext, cfg *observability.ElasticsearchConfig, _, _, _ time.Time) (*observability.ElasticAPMDependencies, error) {
		assert.Equal(t, observability.ESTraceSchemaElasticAPM, cfg.TraceSchema)
		fetchedIndexes = append(fetchedIndexes, cfg.TraceIndex)
		if cfg.TraceIndex != "" {
			return nil, nil // no APM data behind the custom index in this fixture
		}
		return &observability.ElasticAPMDependencies{
			Calls: []observability.ElasticAPMCall{
				{CallerService: "frontend", CallerNamespace: "demo", CallerPod: "frontend-aaa-1", Resource: "product-catalog:8080",
					ReceiverService: "product-catalog", ReceiverNamespace: "demo", ReceiverPod: "product-catalog-aaa-1"},
			},
			Pods: []observability.ElasticAPMServicePod{recentPod("frontend", "demo", "frontend-aaa-1")},
		}, nil
	}

	s := NewElasticAPMFlowSource(slog.Default())
	edges, _, err := s.BuildFlowRelationships(security.NewRequestContextForTenantAdmin("", nil, nil, nil),
		&core.FlowSourceBuildRequest{ExistingNodes: f.nodes})
	require.NoError(t, err)
	assert.Equal(t, []string{"", "apm-prod-traces*"}, fetchedIndexes,
		"accounts A and B share one fetch; the account with its own index gets its own; the otel_native account none")
	require.Len(t, edges, 1)
	assert.Equal(t, "a-frontend", edges[0].SourceNodeID)
}

func TestElasticAPM_BuildToleratesConfigAndFetchFailures(t *testing.T) {
	origFetch, origCfg, origAccts := fetchElasticAPMDeps, getElasticAPMESConfig, getElasticAPMAccounts
	t.Cleanup(func() {
		fetchElasticAPMDeps, getElasticAPMESConfig, getElasticAPMAccounts = origFetch, origCfg, origAccts
	})

	getElasticAPMAccounts = func(string, []string) ([]core.K8sAccount, error) {
		return []core.K8sAccount{{CloudAccountID: "bad-config"}, {CloudAccountID: "fetch-fails"}}, nil
	}
	getElasticAPMESConfig = func(_ *security.RequestContext, accountID string) (*observability.ElasticsearchConfig, error) {
		if accountID == "bad-config" {
			return nil, errors.New("decrypt failed")
		}
		return &observability.ElasticsearchConfig{IntegrationID: "es-2", TraceSchema: observability.ESTraceSchemaElasticAPM}, nil
	}
	fetchElasticAPMDeps = func(*security.RequestContext, *observability.ElasticsearchConfig, time.Time, time.Time, time.Time) (*observability.ElasticAPMDependencies, error) {
		return nil, errors.New("es unreachable")
	}

	s := NewElasticAPMFlowSource(slog.Default())
	edges, nodes, err := s.BuildFlowRelationships(security.NewRequestContextForTenantAdmin("", nil, nil, nil), &core.FlowSourceBuildRequest{})
	require.NoError(t, err, "one account's failure must not fail the build")
	assert.Empty(t, edges)
	assert.Empty(t, nodes)
	assert.Equal(t, 2, s.GetMetrics().ErrorCount)
}

func TestElasticAPM_EnabledByDefault(t *testing.T) {
	t.Setenv(elasticAPMEnvEnabled, "")
	assert.True(t, NewElasticAPMFlowSource(nil).IsEnabled())
	t.Setenv(elasticAPMEnvEnabled, "true")
	assert.True(t, NewElasticAPMFlowSource(nil).IsEnabled())
	// The env var is now a kill switch, so every spelling Go reads as false has to
	// disable it — an operator who writes 0 means off.
	for _, off := range []string{"false", "FALSE", "False", "0", "f"} {
		t.Setenv(elasticAPMEnvEnabled, off)
		assert.Falsef(t, NewElasticAPMFlowSource(nil).IsEnabled(), "%q should disable the source", off)
	}
	// A value that is not a boolean at all leaves it enabled, like an unset var.
	t.Setenv(elasticAPMEnvEnabled, "yes-please")
	assert.True(t, NewElasticAPMFlowSource(nil).IsEnabled())
}

func TestElasticAPM_ServiceNameSharedByTwoWorkloadsKeepsBothEdgesWithoutCounts(t *testing.T) {
	f := newAPMFixture()
	worker := apmWorkload("a-ml-worker", apmAcctA, "ml-k8s-server-worker", "nudgebee")
	f.nodes = append(f.nodes, worker)
	f.accounts[0].pods.byNamespaceName[nsNameKey{"nudgebee", "ml-k8s-server-worker-a-1"}] = worker
	f.accounts[0].pods.rebuildGlobalNameIndex()

	data := &observability.ElasticAPMDependencies{
		Dependencies: []observability.ElasticAPMDependency{{CallerService: "ml-k8s-server", Resource: "relay-server:8080", Calls: 50}},
		Calls: []observability.ElasticAPMCall{
			{CallerService: "ml-k8s-server", CallerNamespace: "nudgebee", CallerPod: "ml-k8s-server-a-1", Resource: "relay-server:8080"},
			{CallerService: "ml-k8s-server", CallerNamespace: "nudgebee", CallerPod: "ml-k8s-server-worker-a-1", Resource: "relay-server:8080"},
		},
		Pods: []observability.ElasticAPMServicePod{
			recentPod("ml-k8s-server", "nudgebee", "ml-k8s-server-a-1"),
			recentPod("ml-k8s-server", "nudgebee", "ml-k8s-server-worker-a-1"),
		},
	}
	edges, _ := f.build(t, data)
	idx := edgeIndex(edges)
	require.Len(t, edges, 2)
	require.Contains(t, idx, "a-ml->a-relay")
	require.Contains(t, idx, "a-ml-worker->a-relay")
	for _, e := range edges {
		assert.Equal(t, elasticAPMScopeShared, e.Properties["apm_metrics_scope"],
			"the service-level count covers both workloads; stamping it on each would double it")
		assert.NotContains(t, e.Properties, "request_count")
	}
}

func TestElasticAPM_TraceIndexWithoutAPMDocumentsDrawsNothing(t *testing.T) {
	f := newAPMFixture()
	origFetch, origCfg, origAccts, origPods := fetchElasticAPMDeps, getElasticAPMESConfig, getElasticAPMAccounts, newElasticAPMPodLookup
	t.Cleanup(func() {
		fetchElasticAPMDeps, getElasticAPMESConfig, getElasticAPMAccounts, newElasticAPMPodLookup = origFetch, origCfg, origAccts, origPods
	})
	getElasticAPMAccounts = func(string, []string) ([]core.K8sAccount, error) {
		return []core.K8sAccount{{CloudAccountID: apmAcctA}}, nil
	}
	getElasticAPMESConfig = func(*security.RequestContext, string) (*observability.ElasticsearchConfig, error) {
		// Schema switched to elastic_apm, Trace Index still the OTel stream.
		return &observability.ElasticsearchConfig{IntegrationID: "es-1", TraceSchema: observability.ESTraceSchemaElasticAPM,
			TraceIndex: "traces-generic.otel-default"}, nil
	}
	newElasticAPMPodLookup = func(*security.RequestContext, string, []*core.DbNode, *slog.Logger) *PodIPResolver {
		return f.accounts[0].pods
	}
	fetchElasticAPMDeps = func(*security.RequestContext, *observability.ElasticsearchConfig, time.Time, time.Time, time.Time) (*observability.ElasticAPMDependencies, error) {
		// metrics-apm* answers; the OTel trace index has no APM transactions.
		return &observability.ElasticAPMDependencies{
			Dependencies: []observability.ElasticAPMDependency{{CallerService: "frontend", Resource: "product-catalog:8080", Calls: 10}},
		}, nil
	}
	edges, nodes, err := NewElasticAPMFlowSource(slog.Default()).BuildFlowRelationships(
		security.NewRequestContextForTenantAdmin("", nil, nil, nil), &core.FlowSourceBuildRequest{ExistingNodes: f.nodes})
	require.NoError(t, err)
	assert.Empty(t, edges)
	assert.Empty(t, nodes)
}
