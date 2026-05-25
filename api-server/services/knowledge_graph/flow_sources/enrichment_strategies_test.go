package flow_sources

import (
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

const (
	testLBDNS    = "prod-alb-1234567890.us-east-1.elb.amazonaws.com"
	testRDSEndpt = "mydb.abc123.us-east-1.rds.amazonaws.com"
)

// TestDirectEndpointMatchStrategy covers the new Strategy 0 in the chain.
// It must (a) skip when EndpointIndex is empty, (b) return a MATCH with
// RelationshipHint = RoutesThrough on a direct hit, and (c) return NoMatch
// when the hostname isn't in the index — leaving downstream strategies to
// produce the looser RelationshipResolvesTo via the existing chain.
func TestDirectEndpointMatchStrategy(t *testing.T) {
	lbNode := makeNode("lb-1", core.NodeTypeLoadBalancer, map[string]interface{}{"dns_name": testLBDNS})
	rdsNode := makeNode("rds-1", core.NodeTypeDatabase, map[string]interface{}{
		"dns_name":         testRDSEndpt,
		"endpoint_address": testRDSEndpt,
	})
	idx := buildCloudEndpointIndex(nil, "", []*core.DbNode{lbNode, rdsNode}, silentLogger())

	strategy := NewDirectEndpointMatchStrategy()

	cases := []struct {
		name         string
		ctx          *MatchingContext
		hostname     string
		wantMatched  bool
		wantNode     *core.DbNode
		wantHint     core.RelationshipType
		wantMatchSub string // substring expected in MatchedBy
	}{
		{
			name:        "nil_ctx_returns_NoMatch",
			ctx:         nil,
			hostname:    testLBDNS,
			wantMatched: false,
		},
		{
			name:        "empty_index_returns_NoMatch",
			ctx:         &MatchingContext{},
			hostname:    testLBDNS,
			wantMatched: false,
		},
		{
			name:         "lb_dns_name_hit_emits_RoutesThrough",
			ctx:          &MatchingContext{EndpointIndex: idx},
			hostname:     testLBDNS,
			wantMatched:  true,
			wantNode:     lbNode,
			wantHint:     core.RelationshipRoutesThrough,
			wantMatchSub: "graph_endpoint_index:dns_name",
		},
		{
			name:         "rds_endpoint_hit_emits_RoutesThrough",
			ctx:          &MatchingContext{EndpointIndex: idx},
			hostname:     testRDSEndpt,
			wantMatched:  true,
			wantNode:     rdsNode,
			wantHint:     core.RelationshipRoutesThrough,
			wantMatchSub: "graph_endpoint_index:",
		},
		{
			name:        "miss_falls_through",
			ctx:         &MatchingContext{EndpointIndex: idx},
			hostname:    "not-in-graph.example.com",
			wantMatched: false,
		},
		{
			name:         "case_insensitive_match",
			ctx:          &MatchingContext{EndpointIndex: idx},
			hostname:     "PROD-ALB-1234567890.US-EAST-1.ELB.AMAZONAWS.COM",
			wantMatched:  true,
			wantNode:     lbNode,
			wantHint:     core.RelationshipRoutesThrough,
			wantMatchSub: "graph_endpoint_index:dns_name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strategy.Match(tc.hostname, tc.ctx)
			if got.Matched != tc.wantMatched {
				t.Fatalf("Matched = %v, want %v", got.Matched, tc.wantMatched)
			}
			if !tc.wantMatched {
				return
			}
			if got.Node != tc.wantNode {
				t.Errorf("Node = %v, want %v", got.Node, tc.wantNode)
			}
			if got.RelationshipHint != tc.wantHint {
				t.Errorf("RelationshipHint = %q, want %q", got.RelationshipHint, tc.wantHint)
			}
			if !strings.Contains(got.MatchedBy, tc.wantMatchSub) {
				t.Errorf("MatchedBy = %q, want substring %q", got.MatchedBy, tc.wantMatchSub)
			}
		})
	}
}

// TestDirectEndpointMatchStrategy_Name asserts the registered strategy name —
// downstream log/metric correlation will pivot on this string.
func TestDirectEndpointMatchStrategyName(t *testing.T) {
	if got := NewDirectEndpointMatchStrategy().Name(); got != "direct_endpoint_match" {
		t.Errorf("Name() = %q, want %q", got, "direct_endpoint_match")
	}
}

// TestAWSHostnamePatternStrategy_ResourceNameFallback covers the
// fallback added in this PR: when neither dns_name nor full-hostname name
// match hits, extract the bare resource id from the hostname and look up
// across accounts. Repro: nudgebee-emails.s3.us-east-1.amazonaws.com on the
// k8s-prod ExternalService node should resolve to the Storage node named
// "nudgebee-emails" sitting in aws-prod.
func TestAWSHostnamePatternStrategy_ResourceNameFallback(t *testing.T) {
	awsAccount := "aws-acct-uuid"
	k8sAccount := "k8s-acct-uuid"

	// AWS-source node: bucket lives in the AWS account, only the bare bucket
	// name in `name`. dns_name intentionally NOT set to exercise the fallback.
	bucket := makeNode("storage-1", core.NodeTypeStorage, map[string]interface{}{
		"name":         "nudgebee-emails",
		"service_name": "AmazonS3",
	})
	bucket.CloudAccountID = awsAccount

	// Empty matcher context — no nodes carry the full hostname.
	ctx := &MatchingContext{
		CloudAccountID: k8sAccount, // ExternalService account, NOT the bucket's
		NodeMatcher:    NewNodeMatcher([]*core.DbNode{bucket}),
	}

	strategy := NewAWSHostnamePatternStrategy()
	got := strategy.Match("nudgebee-emails.s3.us-east-1.amazonaws.com", ctx)

	if !got.Matched {
		t.Fatalf("expected match via resource-name fallback, got NoMatch")
	}
	if got.Node != bucket {
		t.Errorf("matched wrong node: got %v, want %v", got.Node, bucket)
	}
	if !strings.Contains(got.MatchedBy, "aws_resource_name") {
		t.Errorf("MatchedBy = %q, want substring aws_resource_name", got.MatchedBy)
	}
}

// TestAWSHostnamePatternStrategy_ResourceNameFallback_DoesNotLoop ensures the
// fallback skips when the extracted name equals the input hostname (e.g. for
// service-endpoint hostnames where there's no per-resource label to extract).
// Without the resName != name guard, this would re-scan with the same input
// and waste a NodeMatcher call.
func TestAWSHostnamePatternStrategy_ResourceNameFallback_DoesNotLoop(t *testing.T) {
	ctx := &MatchingContext{
		CloudAccountID: "k8s-acct-uuid",
		NodeMatcher:    NewNodeMatcher(nil),
	}
	strategy := NewAWSHostnamePatternStrategy()
	// Service endpoint — extractResourceNameFromEndpoint returns "" for these.
	if got := strategy.Match("sqs.us-east-1.amazonaws.com", ctx); got.Matched {
		t.Errorf("expected no match for service-endpoint hostname, got %+v", got)
	}
}

// TestMatchWithHint covers the new constructor that callers (strategies) use
// to attach a relationship-type override to a successful match.
func TestMatchWithHint(t *testing.T) {
	node := makeNode("n1", core.NodeTypeLoadBalancer, nil)

	got := MatchWithHint(node, "test", core.RelationshipRoutesThrough)
	if !got.Matched {
		t.Errorf("Matched = false, want true")
	}
	if got.Node != node {
		t.Errorf("Node = %v, want %v", got.Node, node)
	}
	if got.MatchedBy != "test" {
		t.Errorf("MatchedBy = %q, want %q", got.MatchedBy, "test")
	}
	if got.RelationshipHint != core.RelationshipRoutesThrough {
		t.Errorf("RelationshipHint = %q, want %q", got.RelationshipHint, core.RelationshipRoutesThrough)
	}

	// Plain Match() must leave RelationshipHint zero so createLinkEdge falls
	// back to RelationshipResolvesTo.
	plain := Match(node, "test")
	if plain.RelationshipHint != "" {
		t.Errorf("Match().RelationshipHint = %q, want empty", plain.RelationshipHint)
	}
}

// =============================================================================
// K8sServiceIPMatchStrategy tests (Strategy 1 in chain, backstop for raw-IP ESes)
// =============================================================================

// k8sIPStrategyCtx constructs a MatchingContext wired with the K8sServiceIP
// resolver and a caller-cluster index built from the supplied edges + nodes.
// The strategy's behavior depends entirely on these two fields; everything
// else on ctx is irrelevant for these tests.
func k8sIPStrategyCtx(svcNodes []*core.DbNode, callerVotes map[string][]string) *MatchingContext {
	index := make(map[string][]string, len(callerVotes))
	for k, v := range callerVotes {
		index[k] = v
	}
	return &MatchingContext{
		K8sServiceIPResolver: NewK8sServiceIPResolver(svcNodes),
		CallerClusterIndex:   index,
		Logger:               silentLogger(),
	}
}

func TestK8sServiceIPMatchStrategy_SameClusterHit(t *testing.T) {
	const targetIP = "34.118.228.207"
	svc := makeServiceNode("services-server", "k8s-dev", targetIP)
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{svc},
		map[string][]string{targetIP: {"k8s-dev", "k8s-dev"}},
	)

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if !got.Matched {
		t.Fatal("expected match")
	}
	if got.Node.Properties["name"] != "services-server" {
		t.Errorf("matched wrong node: %v", got.Node.Properties["name"])
	}
	if !strings.HasPrefix(got.MatchedBy, "k8s_cluster_ip:same_cluster") {
		t.Errorf("MatchedBy = %q, want prefix k8s_cluster_ip:same_cluster", got.MatchedBy)
	}
}

func TestK8sServiceIPMatchStrategy_GlobalUniqueFallback(t *testing.T) {
	const targetIP = "10.0.0.99"
	// Only one Service globally with this IP — callers absent, resolver still
	// resolves via the global-unique path.
	svc := makeServiceNode("only", "any", targetIP)
	ctx := k8sIPStrategyCtx([]*core.DbNode{svc}, map[string][]string{})

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if !got.Matched {
		t.Fatal("expected global-unique match")
	}
	if !strings.HasPrefix(got.MatchedBy, "k8s_cluster_ip:global_unique") {
		t.Errorf("MatchedBy = %q, want prefix k8s_cluster_ip:global_unique", got.MatchedBy)
	}
}

func TestK8sServiceIPMatchStrategy_MultiClusterAmbiguousNoCaller(t *testing.T) {
	const targetIP = "10.0.0.1"
	// Same IP in two clusters and no caller context → resolver must refuse.
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{
			makeServiceNode("svc-a", "cluster-a", targetIP),
			makeServiceNode("svc-b", "cluster-b", targetIP),
		},
		map[string][]string{},
	)

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if got.Matched {
		t.Errorf("ambiguous IP without caller cluster must not match, got %v", got.Node)
	}
}

func TestK8sServiceIPMatchStrategy_PluralityWinner(t *testing.T) {
	const targetIP = "10.0.0.1"
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{
			makeServiceNode("svc-a", "cluster-a", targetIP),
			makeServiceNode("svc-b", "cluster-b", targetIP),
		},
		map[string][]string{
			// 3 cluster-a + 2 cluster-b → plurality wins for cluster-a.
			targetIP: {"cluster-a", "cluster-a", "cluster-a", "cluster-b", "cluster-b"},
		},
	)

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if !got.Matched || got.Node.Properties["name"] != "svc-a" {
		t.Errorf("plurality should pick svc-a, got matched=%v node=%v", got.Matched, got.Node)
	}
}

func TestK8sServiceIPMatchStrategy_PluralityStrongWinner(t *testing.T) {
	const targetIP = "10.0.0.1"
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{
			makeServiceNode("svc-a", "cluster-a", targetIP),
			makeServiceNode("svc-b", "cluster-b", targetIP),
		},
		map[string][]string{
			// 5 cluster-a + 1 cluster-b → plurality wins for cluster-a.
			targetIP: {"cluster-a", "cluster-a", "cluster-a", "cluster-a", "cluster-a", "cluster-b"},
		},
	)

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if !got.Matched || got.Node.Properties["name"] != "svc-a" {
		t.Errorf("plurality should pick svc-a, got matched=%v node=%v", got.Matched, got.Node)
	}
}

func TestK8sServiceIPMatchStrategy_PluralityTieFallsThroughToGlobal(t *testing.T) {
	const targetIP = "10.0.0.1"
	// Tie → caller cluster "" → ambiguous IP → no match.
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{
			makeServiceNode("svc-a", "cluster-a", targetIP),
			makeServiceNode("svc-b", "cluster-b", targetIP),
		},
		map[string][]string{
			targetIP: {"cluster-a", "cluster-a", "cluster-b", "cluster-b"},
		},
	)

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if got.Matched {
		t.Errorf("tie should fall through to ambiguous global lookup, got %v", got.Node)
	}
}

func TestK8sServiceIPMatchStrategy_PluralityZeroCallers(t *testing.T) {
	const targetIP = "10.0.0.99"
	// No callers → "" → global lookup succeeds because IP is globally unique.
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{makeServiceNode("only", "any", targetIP)},
		map[string][]string{targetIP: nil},
	)

	got := NewK8sServiceIPMatchStrategy().Match(targetIP, ctx)
	if !got.Matched {
		t.Errorf("zero callers + globally unique IP should still resolve")
	}
}

func TestK8sServiceIPMatchStrategy_SpecialIPsRejected(t *testing.T) {
	// Even when the resolver index contains these IPs, the helper's skip list
	// must reject them before delegating to Resolve.
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{
			makeServiceNode("loop", "c", "127.0.0.1"),
			makeServiceNode("metadata", "c", "169.254.169.254"),
		},
		map[string][]string{},
	)
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "::1"} {
		got := NewK8sServiceIPMatchStrategy().Match(ip, ctx)
		if got.Matched {
			t.Errorf("special IP %q must not match", ip)
		}
	}
}

func TestK8sServiceIPMatchStrategy_NonIPSkipped(t *testing.T) {
	// DNS names must fall through cleanly so K8sInternalDNSStrategy etc. get
	// their turn.
	ctx := k8sIPStrategyCtx(
		[]*core.DbNode{makeServiceNode("svc", "c", "10.0.0.5")},
		map[string][]string{},
	)
	got := NewK8sServiceIPMatchStrategy().Match("services-server.nudgebee.svc.cluster.local", ctx)
	if got.Matched {
		t.Errorf("DNS name must not match K8sServiceIP strategy, got %v", got.Node)
	}
}

func TestK8sServiceIPMatchStrategy_NoResolverNoMatch(t *testing.T) {
	// Defensive: a ctx without the resolver field set must return NoMatch
	// without panicking. Can happen if a future code path forgets to wire
	// MatchingContext fully.
	ctx := &MatchingContext{Logger: silentLogger()}
	got := NewK8sServiceIPMatchStrategy().Match("10.0.0.1", ctx)
	if got.Matched {
		t.Errorf("nil resolver must produce no match")
	}
}

// =============================================================================
// buildCallerClusterIndex tests
// =============================================================================

func TestBuildCallerClusterIndex_AggregatesInboundCallers(t *testing.T) {
	es := &core.DbNode{
		ID:         "es-1",
		NodeType:   core.NodeTypeExternalService,
		Properties: map[string]interface{}{"name": "10.0.0.1"},
	}
	callerA := &core.DbNode{
		ID:         "caller-a",
		NodeType:   core.NodeTypeK8sService,
		Properties: map[string]interface{}{"name": "svc-a", "cluster": "cluster-a"},
	}
	callerB := &core.DbNode{
		ID:         "caller-b",
		NodeType:   core.NodeTypeK8sService,
		Properties: map[string]interface{}{"name": "svc-b", "cluster": "cluster-b"},
	}
	otherES := &core.DbNode{
		ID:         "es-other",
		NodeType:   core.NodeTypeExternalService,
		Properties: map[string]interface{}{"name": "10.0.0.99"},
	}
	edges := []*core.DbEdge{
		{SourceNodeID: "caller-a", DestinationNodeID: "es-1", RelationshipType: core.RelationshipCalls},
		{SourceNodeID: "caller-a", DestinationNodeID: "es-1", RelationshipType: core.RelationshipCalls},
		{SourceNodeID: "caller-b", DestinationNodeID: "es-1", RelationshipType: core.RelationshipCalls},
		// CALLS edge into a different ES — must not affect es-1's bucket.
		{SourceNodeID: "caller-a", DestinationNodeID: "es-other", RelationshipType: core.RelationshipCalls},
		// Non-CALLS edge — must be ignored.
		{SourceNodeID: "caller-b", DestinationNodeID: "es-1", RelationshipType: core.RelationshipResolvesTo},
	}

	idx := buildCallerClusterIndex(
		[]*core.DbNode{es, otherES},
		edges,
		[]*core.DbNode{es, otherES, callerA, callerB},
	)

	got := idx["10.0.0.1"]
	if len(got) != 3 {
		t.Fatalf("expected 3 caller votes for 10.0.0.1, got %d: %v", len(got), got)
	}
	counts := map[string]int{}
	for _, c := range got {
		counts[c]++
	}
	if counts["cluster-a"] != 2 || counts["cluster-b"] != 1 {
		t.Errorf("expected 2x cluster-a + 1x cluster-b, got %v", counts)
	}

	// Other ES must record its single caller from cluster-a.
	if len(idx["10.0.0.99"]) != 1 || idx["10.0.0.99"][0] != "cluster-a" {
		t.Errorf("other ES bucket wrong: %v", idx["10.0.0.99"])
	}
}

func TestBuildCallerClusterIndex_SkipsCallersWithoutCluster(t *testing.T) {
	es := &core.DbNode{
		ID:         "es-1",
		NodeType:   core.NodeTypeExternalService,
		Properties: map[string]interface{}{"name": "10.0.0.1"},
	}
	caller := &core.DbNode{
		ID:         "caller-1",
		NodeType:   core.NodeTypeService,
		Properties: map[string]interface{}{"name": "no-cluster-app"}, // no "cluster" key
	}
	edges := []*core.DbEdge{
		{SourceNodeID: "caller-1", DestinationNodeID: "es-1", RelationshipType: core.RelationshipCalls},
	}

	idx := buildCallerClusterIndex(
		[]*core.DbNode{es},
		edges,
		[]*core.DbNode{es, caller},
	)

	if len(idx["10.0.0.1"]) != 0 {
		t.Errorf("caller without cluster prop should be skipped, got %v", idx["10.0.0.1"])
	}
}

func TestBuildCallerClusterIndex_EmptyInputs(t *testing.T) {
	if idx := buildCallerClusterIndex(nil, nil, nil); len(idx) != 0 {
		t.Errorf("nil inputs should return empty map, got %v", idx)
	}
	if idx := buildCallerClusterIndex([]*core.DbNode{}, []*core.DbEdge{}, []*core.DbNode{}); len(idx) != 0 {
		t.Errorf("empty inputs should return empty map, got %v", idx)
	}
}
