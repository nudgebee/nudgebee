package flow_sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// AWS hostname pattern constants
const (
	AWSHostnameSuffix    = ".amazonaws.com"
	CloudfrontHostSuffix = ".cloudfront.net"
)

// EnrichmentMatchResult holds the result of matching an external service.
// RelationshipHint, when set, overrides the default RelationshipResolvesTo
// when createLinkEdge constructs the resulting edge — used by strategies that
// match on direct endpoint identity (e.g. ExternalService hostname == LB
// dns_name) and want to assert RelationshipRoutesThrough semantics rather
// than the looser RelationshipResolvesTo.
type EnrichmentMatchResult struct {
	Matched          bool
	Node             *core.DbNode
	MatchedBy        string
	RelationshipHint core.RelationshipType
}

// NoMatch returns an empty/unmatched result
func NoMatch() EnrichmentMatchResult {
	return EnrichmentMatchResult{Matched: false}
}

// Match returns a successful match result with no relationship-type override
// (createLinkEdge will use the default RelationshipResolvesTo).
func Match(node *core.DbNode, matchedBy string) EnrichmentMatchResult {
	return EnrichmentMatchResult{
		Matched:   true,
		Node:      node,
		MatchedBy: matchedBy,
	}
}

// MatchWithHint returns a successful match that also asks createLinkEdge to
// emit the given RelationshipType instead of the default RelationshipResolvesTo.
func MatchWithHint(node *core.DbNode, matchedBy string, hint core.RelationshipType) EnrichmentMatchResult {
	return EnrichmentMatchResult{
		Matched:          true,
		Node:             node,
		MatchedBy:        matchedBy,
		RelationshipHint: hint,
	}
}

// MatchingStrategy defines the interface for node matching strategies
type MatchingStrategy interface {
	Name() string
	Match(name string, ctx *MatchingContext) EnrichmentMatchResult
}

// MatchingContext holds context needed for matching.
// EndpointIndex is an O(1) endpoint→node lookup over LB/RDS/Cache nodes
// already in the graph; populated once per build by prepareMatchingContext
// and consumed by DirectEndpointMatchStrategy as the fast-path first
// strategy in the chain.
type MatchingContext struct {
	CloudAccountID    string
	EndpointIndex     map[string]endpointHit
	NodeMatcher       *NodeMatcher
	CloudResourcesMap map[string]*CloudResourceRow
	AWSAccountIDs     []string
	ZoneCache         *Route53ZoneCache
	RecordCache       *Route53RecordCache
	ReqCtx            *security.RequestContext
	Logger            *slog.Logger
}
