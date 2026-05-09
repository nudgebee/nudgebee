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
