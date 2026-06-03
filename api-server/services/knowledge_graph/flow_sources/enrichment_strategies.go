package flow_sources

import (
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// =============================================================================
// Strategy 0: Direct Endpoint Match (fast path)
// =============================================================================

// DirectEndpointMatchStrategy looks up an external-service hostname against an
// in-memory index of LB / Database / Cache endpoints already in the graph.
// Sits first in the chain so the common "ExternalService.name == LB.dns_name"
// case short-circuits before we run pattern strategies or hit Route53.
//
// Hits emit RelationshipRoutesThrough — the hostname is the public face of
// an owned cloud resource, which is a stronger semantic claim than the
// fallback RelationshipResolvesTo other strategies emit.
type DirectEndpointMatchStrategy struct{}

// NewDirectEndpointMatchStrategy constructs the strategy. The endpoint index
// itself lives on MatchingContext and is populated by prepareMatchingContext.
func NewDirectEndpointMatchStrategy() *DirectEndpointMatchStrategy {
	return &DirectEndpointMatchStrategy{}
}

func (s *DirectEndpointMatchStrategy) Name() string {
	return "direct_endpoint_match"
}

func (s *DirectEndpointMatchStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	if ctx == nil || len(ctx.EndpointIndex) == 0 {
		return NoMatch()
	}
	node, field := findCloudResourceByEndpoint(ctx.EndpointIndex, name)
	if node == nil {
		return NoMatch()
	}
	return MatchWithHint(node, "graph_endpoint_index:"+field, core.RelationshipRoutesThrough)
}

// =============================================================================
// Strategy 1: K8s Internal DNS Matching
// =============================================================================

// K8sInternalDNSStrategy matches K8s internal DNS names to existing nodes
type K8sInternalDNSStrategy struct {
	parser     *K8sDNSParser
	classifier *AWSClassifier
}

// NewK8sInternalDNSStrategy creates a new K8s internal DNS matching strategy
func NewK8sInternalDNSStrategy() *K8sInternalDNSStrategy {
	return &K8sInternalDNSStrategy{
		parser:     NewK8sDNSParser(),
		classifier: NewAWSClassifier(),
	}
}

func (s *K8sInternalDNSStrategy) Name() string {
	return "k8s_internal_dns"
}

func (s *K8sInternalDNSStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	// Skip if not K8s internal DNS pattern
	if !s.parser.IsK8sInternalDNS(name) && !s.parser.LooksLikeK8sServiceName(name) {
		return NoMatch()
	}

	// Parse K8s DNS name
	info := s.parser.Parse(name)
	if !info.IsValid {
		return NoMatch()
	}

	// Node types to search (in priority order)
	nodeTypesToTry := []core.NodeType{
		core.NodeTypeK8sService,
		core.NodeTypeService,
		core.NodeTypeWorkload,
		core.NodeTypePod,
	}

	for _, nodeType := range nodeTypesToTry {
		// Try with namespace if available
		if info.Namespace != "" {
			if result := s.matchWithNamespace(ctx, nodeType, info); result.Matched {
				return result
			}
		}

		// Try without namespace (same account)
		if result := s.matchByNameOnly(ctx, nodeType, info.ServiceName, true); result.Matched {
			return result
		}

		// Try any account
		if result := s.matchByNameOnly(ctx, nodeType, info.ServiceName, false); result.Matched {
			return result
		}
	}

	return NoMatch()
}

func (s *K8sInternalDNSStrategy) matchWithNamespace(ctx *MatchingContext, nodeType core.NodeType, info K8sServiceInfo) EnrichmentMatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		AccountID: ctx.CloudAccountID,
		NodeType:  nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: info.ServiceName, MatchType: core.MatchTypeExact, CaseSensitive: false},
			{PropertyPath: "namespace", Value: info.Namespace, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return Match(result.Node, fmt.Sprintf("k8s_dns:%s/%s", info.Namespace, info.ServiceName))
	}
	return NoMatch()
}

func (s *K8sInternalDNSStrategy) matchByNameOnly(ctx *MatchingContext, nodeType core.NodeType, serviceName string, sameAccount bool) EnrichmentMatchResult {
	criteria := MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: serviceName, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	}
	if sameAccount {
		criteria.AccountID = ctx.CloudAccountID
	}

	result, err := ctx.NodeMatcher.FindNode(criteria)
	if err == nil && result.Matched {
		matchedBy := fmt.Sprintf("k8s_name:%s", serviceName)
		if !sameAccount {
			matchedBy = fmt.Sprintf("k8s_name_any_account:%s", serviceName)
		}
		return Match(result.Node, matchedBy)
	}
	return NoMatch()
}

// =============================================================================
// Strategy 2: Cloud Resource DNS Matching
// =============================================================================

// CloudResourceDNSStrategy matches against cloud resources by DNS endpoint
type CloudResourceDNSStrategy struct{}

func NewCloudResourceDNSStrategy() *CloudResourceDNSStrategy {
	return &CloudResourceDNSStrategy{}
}

func (s *CloudResourceDNSStrategy) Name() string {
	return "cloud_resource_dns"
}

func (s *CloudResourceDNSStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	nameLower := strings.ToLower(name)

	// Check if this DNS name exists in our cloud resources
	if _, exists := ctx.CloudResourcesMap[nameLower]; !exists {
		return NoMatch()
	}

	// Found in cloud resources - now find the corresponding node in the graph
	nodeTypesToTry := []core.NodeType{
		core.NodeTypeLoadBalancer,
		core.NodeTypeDatabase,
		core.NodeTypeCache,
		core.NodeTypeStorage,
		core.NodeTypeMessageQueue,
		core.NodeTypeCloudResource,
	}

	for _, nodeType := range nodeTypesToTry {
		// Match by dns_name property
		if result := s.matchByProperty(ctx, nodeType, "dns_name", name, core.MatchTypeExact); result.Matched {
			return Match(result.Node, "cloud_resource_dns")
		}

		// Match by endpoint property
		if result := s.matchByProperty(ctx, nodeType, "endpoint", name, core.MatchTypeContains); result.Matched {
			return Match(result.Node, "cloud_resource_endpoint")
		}
	}

	return NoMatch()
}

func (s *CloudResourceDNSStrategy) matchByProperty(ctx *MatchingContext, nodeType core.NodeType, property, value string, matchType core.MatchType) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: property, Value: value, MatchType: matchType, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

// =============================================================================
// Strategy 3: AWS Hostname Pattern Matching
// =============================================================================

// AWSHostnamePatternStrategy matches AWS service hostnames to existing nodes
type AWSHostnamePatternStrategy struct {
	classifier *AWSClassifier
}

func NewAWSHostnamePatternStrategy() *AWSHostnamePatternStrategy {
	return &AWSHostnamePatternStrategy{
		classifier: NewAWSClassifier(),
	}
}

func (s *AWSHostnamePatternStrategy) Name() string {
	return "aws_hostname_pattern"
}

func (s *AWSHostnamePatternStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	if !s.classifier.IsAWSHostname(name) {
		return NoMatch()
	}

	// Bare service-API hostnames can't legitimately resolve to a per-resource
	// node — the leftmost label names a service, not a customer identifier.
	// Bailing out here prevents Strategy 1/2 (dns_name / name property
	// lookups under the classified NodeType) from coincidence-matching a
	// CloudResource someone happened to name `dynamodb` or `sqs`. The
	// downstream createInferredNodeIfAWS also short-circuits on the same
	// predicate, so the ExternalService stub remains the visible destination.
	if s.classifier.IsBareAWSServiceEndpoint(name) {
		return NoMatch()
	}

	nodeType, awsService := s.classifier.ClassifyHostname(name)
	if nodeType == "" {
		return NoMatch()
	}

	// Strategy 1: Match by dns_name with specific node type
	if result := s.matchByDNSName(ctx, nodeType, name); result.Matched {
		return Match(result.Node, fmt.Sprintf("aws_hostname:%s", awsService))
	}

	// Strategy 2: Match by name property
	if result := s.matchByNameProperty(ctx, nodeType, name); result.Matched {
		return Match(result.Node, fmt.Sprintf("aws_hostname_name:%s", awsService))
	}

	// Strategy 3: For ELB, extract identifier and try partial match
	if nodeType == core.NodeTypeLoadBalancer {
		if identifier := s.classifier.ExtractELBIdentifier(name); identifier != "" {
			if result := s.matchByPartialDNSName(ctx, nodeType, identifier); result.Matched {
				return Match(result.Node, "aws_elb_identifier")
			}
		}
	}

	// Strategy 4: Resource-name fallback. The AWS source stores S3 buckets,
	// DynamoDB tables, ECR repos, etc. with `name = <bare-id>` (not the full
	// hostname). When dns_name still isn't populated (legacy data, or services
	// the synthesizer can't construct an endpoint for), pull the bare resource
	// id out of the hostname and look up that. matchByNameProperty already
	// runs cross-account (no AccountID in MatchCriteria) — required because
	// the AWS-source node lives in the AWS account while the eBPF/traces
	// ExternalService lives in the K8s account.
	if resName := extractResourceNameFromEndpoint(name); resName != "" && resName != name {
		if result := s.matchByNameProperty(ctx, nodeType, resName); result.Matched {
			return Match(result.Node, fmt.Sprintf("aws_resource_name:%s", awsService))
		}
	}

	// Strategy 5: Try without specific node type filter
	if result := s.matchByDNSNameAnyType(ctx, name); result.Matched {
		return Match(result.Node, "aws_hostname_any_type")
	}

	return NoMatch()
}

func (s *AWSHostnamePatternStrategy) matchByDNSName(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AWSHostnamePatternStrategy) matchByNameProperty(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AWSHostnamePatternStrategy) matchByPartialDNSName(ctx *MatchingContext, nodeType core.NodeType, identifier string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: identifier, MatchType: core.MatchTypeContains, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AWSHostnamePatternStrategy) matchByDNSNameAnyType(ctx *MatchingContext, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

// =============================================================================
// Strategy 4: Azure Hostname Pattern Matching
// =============================================================================

// AzureHostnamePatternStrategy matches Azure service hostnames to existing nodes
type AzureHostnamePatternStrategy struct {
	classifier *AzureClassifier
}

func NewAzureHostnamePatternStrategy() *AzureHostnamePatternStrategy {
	return &AzureHostnamePatternStrategy{
		classifier: NewAzureClassifier(),
	}
}

func (s *AzureHostnamePatternStrategy) Name() string {
	return "azure_hostname_pattern"
}

func (s *AzureHostnamePatternStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	if !s.classifier.IsAzureHostname(name) {
		return NoMatch()
	}

	nodeType, azureService := s.classifier.ClassifyHostname(name)
	if nodeType == "" {
		return NoMatch()
	}

	// Strategy 1: Match by dns_name with specific node type
	if result := s.matchByDNSName(ctx, nodeType, name); result.Matched {
		return Match(result.Node, fmt.Sprintf("azure_hostname:%s", azureService))
	}

	// Strategy 2: Match by endpoint property
	if result := s.matchByEndpoint(ctx, nodeType, name); result.Matched {
		return Match(result.Node, fmt.Sprintf("azure_endpoint:%s", azureService))
	}

	// Strategy 3: Match by name property (extract resource name from hostname)
	resourceName := s.classifier.ExtractResourceName(name)
	if resourceName != "" {
		if result := s.matchByNameProperty(ctx, nodeType, resourceName); result.Matched {
			return Match(result.Node, fmt.Sprintf("azure_resource_name:%s", azureService))
		}
	}

	// Strategy 4: For Redis, extract identifier and try partial match
	if nodeType == core.NodeTypeCache {
		if identifier := s.classifier.ExtractRedisIdentifier(name); identifier != "" {
			if result := s.matchByPartialDNSName(ctx, nodeType, identifier); result.Matched {
				return Match(result.Node, "azure_redis_identifier")
			}
		}
	}

	// Strategy 5: Try without specific node type filter
	if result := s.matchByDNSNameAnyType(ctx, name); result.Matched {
		return Match(result.Node, "azure_hostname_any_type")
	}

	return NoMatch()
}

func (s *AzureHostnamePatternStrategy) matchByDNSName(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AzureHostnamePatternStrategy) matchByEndpoint(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "endpoint", Value: name, MatchType: core.MatchTypeContains, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AzureHostnamePatternStrategy) matchByNameProperty(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AzureHostnamePatternStrategy) matchByPartialDNSName(ctx *MatchingContext, nodeType core.NodeType, identifier string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: identifier, MatchType: core.MatchTypeContains, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *AzureHostnamePatternStrategy) matchByDNSNameAnyType(ctx *MatchingContext, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

// =============================================================================
// Strategy 5: GCP Hostname Pattern Matching
// =============================================================================

// GCPHostnamePatternStrategy matches GCP service hostnames to existing nodes
type GCPHostnamePatternStrategy struct {
	classifier *GCPClassifier
}

func NewGCPHostnamePatternStrategy() *GCPHostnamePatternStrategy {
	return &GCPHostnamePatternStrategy{
		classifier: NewGCPClassifier(),
	}
}

func (s *GCPHostnamePatternStrategy) Name() string {
	return "gcp_hostname_pattern"
}

func (s *GCPHostnamePatternStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	if !s.classifier.IsGCPHostname(name) {
		return NoMatch()
	}

	nodeType, gcpService := s.classifier.ClassifyHostname(name)
	if nodeType == "" {
		return NoMatch()
	}

	// Strategy 1: Match by dns_name with specific node type
	if result := s.matchByDNSName(ctx, nodeType, name); result.Matched {
		return Match(result.Node, fmt.Sprintf("gcp_hostname:%s", gcpService))
	}

	// Strategy 2: Match by endpoint property
	if result := s.matchByEndpoint(ctx, nodeType, name); result.Matched {
		return Match(result.Node, fmt.Sprintf("gcp_endpoint:%s", gcpService))
	}

	// Strategy 3: Match by name property (extract resource name from hostname)
	resourceName := s.classifier.ExtractResourceName(name)
	if resourceName != "" {
		if result := s.matchByNameProperty(ctx, nodeType, resourceName); result.Matched {
			return Match(result.Node, fmt.Sprintf("gcp_resource_name:%s", gcpService))
		}
	}

	// Strategy 4: Try without specific node type filter
	if result := s.matchByDNSNameAnyType(ctx, name); result.Matched {
		return Match(result.Node, "gcp_hostname_any_type")
	}

	return NoMatch()
}

func (s *GCPHostnamePatternStrategy) matchByDNSName(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *GCPHostnamePatternStrategy) matchByEndpoint(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "endpoint", Value: name, MatchType: core.MatchTypeContains, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *GCPHostnamePatternStrategy) matchByNameProperty(ctx *MatchingContext, nodeType core.NodeType, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: name, MatchType: core.MatchTypeContains, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

func (s *GCPHostnamePatternStrategy) matchByDNSNameAnyType(ctx *MatchingContext, name string) *MatchResult {
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "dns_name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		return result
	}
	return &MatchResult{Matched: false}
}

// =============================================================================
// Strategy 6: Route53 DNS Resolution
// =============================================================================

// Route53ResolutionStrategy resolves DNS via Route53 and matches the resolved endpoint
type Route53ResolutionStrategy struct {
	awsStrategy   *AWSHostnamePatternStrategy
	cloudStrategy *CloudResourceDNSStrategy
	parser        *K8sDNSParser
}

func NewRoute53ResolutionStrategy() *Route53ResolutionStrategy {
	return &Route53ResolutionStrategy{
		awsStrategy:   NewAWSHostnamePatternStrategy(),
		cloudStrategy: NewCloudResourceDNSStrategy(),
		parser:        NewK8sDNSParser(),
	}
}

func (s *Route53ResolutionStrategy) Name() string {
	return "route53_resolution"
}

func (s *Route53ResolutionStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	// Skip if no Route53 data available
	if len(ctx.AWSAccountIDs) == 0 || ctx.ZoneCache == nil {
		return NoMatch()
	}

	// Skip K8s internal DNS
	if s.parser.IsK8sInternalDNS(name) {
		return NoMatch()
	}

	// Try to resolve via Route53
	for _, awsAccountID := range ctx.AWSAccountIDs {
		zones := ctx.ZoneCache.GetZones(awsAccountID)
		if len(zones) == 0 {
			continue
		}

		endpoint, err := ResolveRoute53DNSWithCache(ctx.ReqCtx, name, awsAccountID, zones, ctx.RecordCache)
		if err != nil || endpoint == "" {
			continue
		}

		if ctx.Logger != nil {
			ctx.Logger.Debug("Route53 resolved hostname", "hostname", name, "endpoint", endpoint)
		}

		// Try to match the resolved endpoint to existing nodes
		if result := s.awsStrategy.Match(endpoint, ctx); result.Matched {
			return Match(result.Node, fmt.Sprintf("route53:%s->%s", name, endpoint))
		}

		// Try to match against cloud resources map
		if result := s.cloudStrategy.Match(endpoint, ctx); result.Matched {
			return Match(result.Node, fmt.Sprintf("route53_cloud_resource:%s->%s", name, endpoint))
		}
	}

	return NoMatch()
}

// =============================================================================
// Strategy 6: Generic Name Matching
// =============================================================================

// GenericNameStrategy tries generic name matching as last resort
type GenericNameStrategy struct {
	parser *K8sDNSParser
}

func NewGenericNameStrategy() *GenericNameStrategy {
	return &GenericNameStrategy{
		parser: NewK8sDNSParser(),
	}
}

func (s *GenericNameStrategy) Name() string {
	return "generic_name"
}

func (s *GenericNameStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	// Clean the name (remove port, protocol prefix if any)
	cleanName := s.parser.CleanServiceName(name)
	if cleanName == "" {
		return NoMatch()
	}

	// Try to find any node with this name
	result, err := ctx.NodeMatcher.FindNode(MatchCriteria{
		AccountID: ctx.CloudAccountID,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: cleanName, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	})
	if err == nil && result.Matched {
		// Avoid matching to another ExternalService
		if result.Node.NodeType != core.NodeTypeExternalService {
			return Match(result.Node, "generic_name")
		}
	}

	return NoMatch()
}

// =============================================================================
// Strategy 7: K8s Service ClusterIP Matching (backstop)
// =============================================================================

// K8sServiceIPMatchStrategy resolves raw-IP ExternalService names to the
// K8sService that owns that ClusterIP. Source-level short-circuits in
// traces/ebpf flow sources catch the common cases; this is the backstop for
// orphans that slip through (future flow sources, stale ExistingNodes,
// K8sService nodes added in the same build but after the source-level lookup).
//
// Caller-cluster scope is derived via a plurality vote across inbound CALLS
// edges in the in-memory graph: the single most-common cluster wins. Ties,
// empty slices, and zero-caller cases pass "" and rely on the resolver's
// global-unique fallback, which refuses to guess on ambiguous IPs. This
// avoids the boundary-oscillation a hard-threshold majority rule would
// produce when a tipping caller flips the resolution between builds.
type K8sServiceIPMatchStrategy struct{}

// NewK8sServiceIPMatchStrategy constructs the strategy. The resolver and
// caller-cluster index it relies on are populated on MatchingContext by
// prepareMatchingContext.
func NewK8sServiceIPMatchStrategy() *K8sServiceIPMatchStrategy {
	return &K8sServiceIPMatchStrategy{}
}

func (s *K8sServiceIPMatchStrategy) Name() string {
	return "k8s_cluster_ip"
}

func (s *K8sServiceIPMatchStrategy) Match(name string, ctx *MatchingContext) EnrichmentMatchResult {
	if ctx == nil || ctx.K8sServiceIPResolver == nil {
		return NoMatch()
	}
	callerCluster := pluralityCluster(ctx.CallerClusterIndex[name])
	node, reason, ok := ResolveIPToK8sService(name, callerCluster, ctx.K8sServiceIPResolver)
	if !ok {
		return NoMatch()
	}
	if ctx.Logger != nil {
		ctx.Logger.Debug("K8sServiceIPMatchStrategy resolved IP to K8sService",
			"name", name,
			"caller_cluster", callerCluster,
			"reason", reason,
			"matched_unique_key", node.UniqueKey)
	}
	return Match(node, "k8s_cluster_ip:"+reason)
}

// pluralityCluster returns the single most-common cluster from votes, or "" on
// tie / empty / zero. The "" return makes the caller fall through to the
// resolver's global-unique fallback path.
func pluralityCluster(votes []string) string {
	if len(votes) == 0 {
		return ""
	}
	counts := make(map[string]int, len(votes))
	for _, v := range votes {
		if v == "" {
			continue
		}
		counts[v]++
	}
	var winner string
	var winnerCount int
	var tie bool
	for cluster, n := range counts {
		switch {
		case n > winnerCount:
			winner = cluster
			winnerCount = n
			tie = false
		case n == winnerCount:
			tie = true
		}
	}
	if tie || winnerCount == 0 {
		return ""
	}
	return winner
}

// buildCallerClusterIndex returns map[extSvcName] → []callerClusters, where
// callerClusters is the list of `cluster` properties read from the source
// nodes of inbound CALLS edges. Keyed by ExternalService node `name` because
// within a per-account MatchingContext, IP-named ExternalServices are uniquely
// identified by name alone (location/hierarchy are always empty for IP-named
// nodes — verified against production data).
//
// The returned map is immutable for the lifetime of MatchingContext.
func buildCallerClusterIndex(externalServices []*core.DbNode, allEdges []*core.DbEdge, allNodes []*core.DbNode) map[string][]string {
	esIDToName := indexExternalServiceNamesByID(externalServices)
	if len(esIDToName) == 0 {
		return map[string][]string{}
	}
	nodeByID := indexNodesByID(allNodes)

	index := make(map[string][]string, len(esIDToName))
	for _, e := range allEdges {
		if e == nil || e.RelationshipType != core.RelationshipCalls {
			continue
		}
		esName, isES := esIDToName[e.DestinationNodeID]
		if !isES {
			continue
		}
		cluster := stringProp(nodeByID[e.SourceNodeID], "cluster")
		if cluster == "" {
			continue
		}
		index[esName] = append(index[esName], cluster)
	}
	return index
}

// indexExternalServiceNamesByID maps ExternalService node ID → name. Skips
// nil entries, non-ExternalService node types, and nodes with empty name.
func indexExternalServiceNamesByID(externalServices []*core.DbNode) map[string]string {
	out := make(map[string]string, len(externalServices))
	for _, es := range externalServices {
		if es == nil || es.NodeType != core.NodeTypeExternalService {
			continue
		}
		name, _ := es.Properties["name"].(string)
		if name == "" {
			continue
		}
		out[es.ID] = name
	}
	return out
}

// indexNodesByID maps node ID → node pointer for O(1) caller lookup from
// edge.SourceNodeID. stringProp tolerates nil values returned from this map,
// so missing IDs degrade gracefully.
func indexNodesByID(nodes []*core.DbNode) map[string]*core.DbNode {
	out := make(map[string]*core.DbNode, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		out[n.ID] = n
	}
	return out
}
