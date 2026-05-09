package flow_sources

import (
	"context"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FlowSourceInterface defines the interface that all flow sources must implement
// Flow sources are different from resource sources:
// - Resource sources (K8s, AWS): Build resource graphs from external infrastructure APIs
// - Flow sources (Traces, APM, eBPF): Read existing resource graph and enrich it with flow relationships
//
// Flow sources only create edges/relationships, not nodes.
// They read the existing graph from the database and create CALLS, PUBLISHES_TO, etc. edges.
type FlowSourceInterface interface {
	// GetName returns the name of the flow source (e.g., "datadog-apm", "jaeger-trace", "ebpf")
	GetName() string

	// BuildFlowRelationships reads the existing graph nodes and creates flow relationships
	// Returns a list of edges to be added to the graph
	// The edges should reference existing nodes by their ID or UniqueKey
	BuildFlowRelationships(ctx context.Context, req *FlowSourceBuildRequest) ([]*core.DbEdge, []*core.DbNode, error)

	// IsEnabled checks if the flow source is enabled and configured
	IsEnabled() bool

	// Validate validates the flow source configuration
	Validate() error

	// GetSourceCategory returns the category of this flow source
	// Used to determine execution order and dependencies
	GetSourceCategory() core.FlowSourceCategory
}

// FlowSourcePriority defines execution priority for flow sources
// Lower number = higher priority (executes first)
type FlowSourcePriority int

const (
	PriorityHigh   FlowSourcePriority = 1
	PriorityMedium FlowSourcePriority = 5
	PriorityLow    FlowSourcePriority = 10
)

// CentralizedExternalServiceEnricher implements core.ExternalServiceEnricherInterface
// It uses modular matching strategies for enriching external services with cloud resources
type CentralizedExternalServiceEnricher struct {
	logger        *slog.Logger
	strategies    []MatchingStrategy
	awsClassifier *AWSClassifier
	k8sDNSParser  *K8sDNSParser
}

// NewCentralizedExternalServiceEnricher creates a new centralized enricher
func NewCentralizedExternalServiceEnricher(logger *slog.Logger) *CentralizedExternalServiceEnricher {
	if logger == nil {
		logger = slog.Default()
	}

	// Initialize matching strategies in priority order. DirectEndpointMatch
	// runs first so the common ExternalService→LB/RDS/Cache case is an O(1)
	// index lookup that emits ROUTES_THROUGH; remaining strategies handle
	// pattern-based and Route53-resolved matches with the looser RESOLVES_TO.
	strategies := []MatchingStrategy{
		NewDirectEndpointMatchStrategy(),
		NewK8sInternalDNSStrategy(),
		NewCloudResourceDNSStrategy(),
		NewAWSHostnamePatternStrategy(),
		NewAzureHostnamePatternStrategy(),
		NewGCPHostnamePatternStrategy(),
		NewRoute53ResolutionStrategy(),
		NewGenericNameStrategy(),
	}

	return &CentralizedExternalServiceEnricher{
		logger:        logger,
		strategies:    strategies,
		awsClassifier: NewAWSClassifier(),
		k8sDNSParser:  NewK8sDNSParser(),
	}
}

// EnrichExternalServices implements core.ExternalServiceEnricherInterface
// It uses modular matching strategies to enrich external services with cloud resources
func (e *CentralizedExternalServiceEnricher) EnrichExternalServices(
	reqCtx *security.RequestContext,
	externalServices []*core.DbNode,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	if len(externalServices) == 0 {
		e.logger.Info("no external services to enrich centrally")
		return allNodes, allEdges, nil
	}

	e.logger.Info("starting centralized external service enrichment with strategies",
		"external_services_count", len(externalServices),
		"total_nodes", len(allNodes),
		"total_edges", len(allEdges),
		"tenant_id", tenantID,
		"strategies_count", len(e.strategies))

	// Group external services by CloudAccountID
	externalServicesByAccount := make(map[string][]*core.DbNode)
	for _, node := range externalServices {
		accountID := node.CloudAccountID
		if accountID == "" {
			accountID = "unknown"
		}
		externalServicesByAccount[accountID] = append(externalServicesByAccount[accountID], node)
	}

	e.logger.Info("grouped external services by account",
		"accounts_count", len(externalServicesByAccount))

	// Track enriched results
	newNodes := make([]*core.DbNode, 0)
	newEdges := make([]*core.DbEdge, 0)
	matchedCount := 0
	unmatchedNames := make([]string, 0)

	// Enrich external services for each account using strategies
	for accountID, accountExternalServices := range externalServicesByAccount {
		if accountID == "unknown" {
			e.logger.Warn("skipping external services with unknown account",
				"count", len(accountExternalServices))
			continue
		}

		e.logger.Info("enriching external services for account using strategies",
			"cloud_account_id", accountID,
			"external_services_count", len(accountExternalServices))

		// Prepare matching context for this account
		ctx, err := e.prepareMatchingContext(reqCtx, accountID, tenantID, allNodes, externalServices)
		if err != nil {
			e.logger.Warn("failed to prepare matching context for account",
				"cloud_account_id", accountID,
				"error", err)
			continue
		}

		// Process each external service using strategies
		for _, extSvc := range accountExternalServices {
			name, ok := extSvc.Properties["name"].(string)
			if !ok || name == "" {
				continue
			}

			// Try all matching strategies
			matchResult := e.findMatchingNode(name, ctx)

			if matchResult.Matched {
				matchedCount++
				edge := e.createLinkEdge(extSvc, matchResult.Node, matchResult.MatchedBy, matchResult.RelationshipHint, accountID, tenantID)
				newEdges = append(newEdges, edge)

				e.logger.Info("Matched external service to existing node",
					"external_service", name,
					"matched_node", matchResult.Node.UniqueKey,
					"matched_by", matchResult.MatchedBy,
					"node_type", matchResult.Node.NodeType)
			} else {
				unmatchedNames = append(unmatchedNames, name)

				// Create inferred node only for AWS patterns
				if inferredNode := e.createInferredNodeIfAWS(name, accountID, tenantID); inferredNode != nil {
					newNodes = append(newNodes, inferredNode)
					edge := e.createLinkEdge(extSvc, inferredNode, "inferred_from_pattern", "", accountID, tenantID)
					newEdges = append(newEdges, edge)

					e.logger.Info("Created inferred node for unmatched AWS service",
						"external_service", name,
						"inferred_node_type", inferredNode.NodeType)
				}
			}
		}

		e.logger.Info("completed enrichment for account",
			"cloud_account_id", accountID,
			"matched_count", matchedCount,
			"unmatched_count", len(unmatchedNames))
	}

	// Combine results
	enrichedNodes := make([]*core.DbNode, 0, len(allNodes)+len(externalServices)+len(newNodes))
	enrichedNodes = append(enrichedNodes, allNodes...)
	enrichedNodes = append(enrichedNodes, externalServices...)
	enrichedNodes = append(enrichedNodes, newNodes...)

	enrichedEdges := make([]*core.DbEdge, 0, len(allEdges)+len(newEdges))
	enrichedEdges = append(enrichedEdges, allEdges...)
	enrichedEdges = append(enrichedEdges, newEdges...)

	e.logger.Info("completed centralized external service enrichment with strategies",
		"total_external_services", len(externalServices),
		"matched_count", matchedCount,
		"unmatched_count", len(unmatchedNames),
		"new_nodes_created", len(newNodes),
		"new_edges_created", len(newEdges))

	return enrichedNodes, enrichedEdges, nil
}

// prepareMatchingContext prepares the context needed for matching strategies
func (e *CentralizedExternalServiceEnricher) prepareMatchingContext(
	reqCtx *security.RequestContext,
	cloudAccountID, tenantID string,
	existingNodes []*core.DbNode,
	externalServices []*core.DbNode,
) (*MatchingContext, error) {
	// Initialize NodeMatcher with ALL nodes
	allNodes := make([]*core.DbNode, 0, len(existingNodes)+len(externalServices))
	allNodes = append(allNodes, existingNodes...)
	allNodes = append(allNodes, externalServices...)
	nodeMatcher := NewNodeMatcher(allNodes)

	ctx := &MatchingContext{
		CloudAccountID: cloudAccountID,
		NodeMatcher:    nodeMatcher,
		EndpointIndex:  buildCloudEndpointIndex(reqCtx, tenantID, allNodes, e.logger),
		ReqCtx:         reqCtx,
		Logger:         e.logger,
	}

	// Get AWS accounts for Route53 resolution
	awsAccountIDs, err := core.GetAWSAccountsForTenant(tenantID)
	if err != nil {
		e.logger.Warn("Failed to get AWS accounts for tenant", "error", err)
		awsAccountIDs = []string{}
	}

	// Get K8s to AWS account mapping
	k8sToAwsMap, err := core.GetK8sCloudAccountMapping(reqCtx, tenantID, "AWS")
	if err != nil {
		e.logger.Warn("Failed to get K8s to AWS account mapping", "error", err)
		k8sToAwsMap = make(map[string][]string)
	}

	// Use mapped accounts if available
	if mappedAccounts, exists := k8sToAwsMap[cloudAccountID]; exists && len(mappedAccounts) > 0 {
		e.logger.Debug("Using mapped AWS accounts for K8s account",
			"k8s_account", cloudAccountID,
			"aws_accounts", mappedAccounts)
		awsAccountIDs = mappedAccounts
	}

	ctx.AWSAccountIDs = awsAccountIDs

	// Pre-fetch Route53 data if we have AWS accounts
	if len(awsAccountIDs) > 0 {
		ctx.ZoneCache, ctx.RecordCache = e.prefetchRoute53Data(reqCtx, awsAccountIDs)
	}

	// Fetch cloud resources from database
	ctx.CloudResourcesMap = e.fetchCloudResourcesMap(tenantID)

	return ctx, nil
}

// findMatchingNode tries all strategies to find a matching node
func (e *CentralizedExternalServiceEnricher) findMatchingNode(name string, ctx *MatchingContext) EnrichmentMatchResult {
	if ctx.NodeMatcher == nil {
		e.logger.Warn("NodeMatcher not initialized")
		return NoMatch()
	}

	for _, strategy := range e.strategies {
		if result := strategy.Match(name, ctx); result.Matched {
			e.logger.Debug("Strategy matched",
				"strategy", strategy.Name(),
				"name", name,
				"matched_by", result.MatchedBy)
			return result
		}
	}

	return NoMatch()
}

// createLinkEdge creates an edge linking external service to matched node.
// hint, when non-empty, overrides the default RelationshipResolvesTo — used
// by DirectEndpointMatchStrategy to assert RelationshipRoutesThrough on
// direct endpoint hits. source_priority is recomputed against the actual
// relationship type so dedup picks the right priority for ROUTES_THROUGH.
func (e *CentralizedExternalServiceEnricher) createLinkEdge(
	extSvc, matchedNode *core.DbNode,
	matchedBy string,
	hint core.RelationshipType,
	cloudAccountID, tenantID string,
) *core.DbEdge {
	relType := core.RelationshipResolvesTo
	if hint != "" {
		relType = hint
	}
	return &core.DbEdge{
		ID:                newUUID(),
		SourceNodeID:      extSvc.ID,
		DestinationNodeID: matchedNode.ID,
		RelationshipType:  relType,
		Properties: map[string]interface{}{
			"discovered_from":        "external_service_enrichment",
			"matched_by":             matchedBy,
			"created_by_flow_source": cloudEnrichmentSourceName,
			"flow_source_category":   cloudEnrichmentCategory,
			"source_priority":        int(GetEdgeSourcePriority(cloudEnrichmentSourceName, relType)),
		},
		CloudAccountID: cloudAccountID,
		TenantID:       tenantID,
		Level:          "Tenant",
		Source:         "cloud",
		CreatedAt:      timeNow(),
		UpdatedAt:      timeNow(),
	}
}

// createInferredNodeIfAWS creates an inferred cloud resource node only for AWS patterns
func (e *CentralizedExternalServiceEnricher) createInferredNodeIfAWS(
	name, cloudAccountID, tenantID string,
) *core.DbNode {
	if !e.awsClassifier.IsAWSHostname(name) {
		return nil
	}

	nodeType, awsService := e.awsClassifier.ClassifyHostname(name)
	if nodeType == "" {
		nodeType = core.NodeTypeCloudResource
		awsService = "aws"
	}

	// AWS-specific inference path: cloud_provider is always "aws" so the node
	// merges with the AWS static source's view of the same resource. Observer
	// remains "cloud" on node.Source.
	keyComponents := core.NewUniqueKeyComponents(core.CloudProviderAWS, nodeType)
	keyComponents.Account = cloudAccountID
	keyComponents.Name = name
	uniqueKey := keyComponents.Build()

	properties := map[string]interface{}{
		"name":             name,
		"dns_name":         name,
		"type":             awsService + "_inferred",
		"inferred":         true,
		"discovery_method": "aws_hostname_pattern",
		"aws_service":      awsService,
	}

	return core.NewNode(nodeType, uniqueKey, properties, tenantID, cloudAccountID, "cloud")
}

// prefetchRoute53Data pre-fetches Route53 hosted zones for all AWS accounts
func (e *CentralizedExternalServiceEnricher) prefetchRoute53Data(
	reqCtx *security.RequestContext,
	awsAccountIDs []string,
) (*Route53ZoneCache, *Route53RecordCache) {
	zoneCache := NewRoute53ZoneCache()
	recordCache := NewRoute53RecordCache()

	for _, awsAccountID := range awsAccountIDs {
		zones, err := FetchHostedZones(reqCtx, awsAccountID)
		if err != nil {
			e.logger.Warn("Failed to fetch hosted zones",
				"aws_account", awsAccountID,
				"error", err)
			continue
		}
		zoneCache.SetZones(awsAccountID, zones)
	}

	return zoneCache, recordCache
}

// fetchCloudResourcesMap fetches all cloud resources and indexes by DNS name
func (e *CentralizedExternalServiceEnricher) fetchCloudResourcesMap(tenantID string) map[string]*CloudResourceRow {
	result := make(map[string]*CloudResourceRow)

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		e.logger.Warn("Failed to get database manager for cloud resources", "error", err)
		return result
	}

	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.is_active = true
			AND cr.type IN (
				'application_loadbalancer', 'network_loadbalancer', 'classic_loadbalancer',
				'rds_instance', 'elasticache_cluster', 'elasticache_replication_group',
				's3_bucket', 'sqs_queue', 'sns_topic', 'dynamodb_table',
				'api_gateway', 'cloudfront_distribution', 'opensearch_domain'
			)
	`

	var resources []CloudResourceRow
	if err := dbManager.Db.Select(&resources, query, tenantID); err != nil {
		e.logger.Warn("Failed to query cloud resources", "error", err)
		return result
	}

	// Index by DNS name (lowercase)
	for i := range resources {
		resource := &resources[i]
		dnsName := ExtractDNSNameFromResource(resource)
		if dnsName != "" {
			result[strings.ToLower(dnsName)] = resource
		}
	}

	e.logger.Debug("Fetched cloud resources for enrichment", "count", len(result))
	return result
}

// Helper functions for time and UUID
func timeNow() time.Time {
	return time.Now()
}

func newUUID() string {
	return uuid.New().String()
}
