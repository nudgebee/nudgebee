package flow_sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Provenance values stamped on edges constructed directly here (i.e. without
// going through BaseFlowSource.CreateEdge). The string "cloud_enrichment"
// matches the source-name keys in EdgeTypePriorities so log/metric correlation
// uses a single canonical name across stamping and priority-resolution.
const (
	cloudEnrichmentSourceName = "cloud_enrichment"
	cloudEnrichmentCategory   = "cloud_enrichment"
)

// CloudResourceRow represents a row from the cloud_resources table
type CloudResourceRow struct {
	ID                 string          `db:"id"`
	ResourceID         string          `db:"resourse_id"` // Note: typo in DB column name
	Name               string          `db:"name"`
	Type               string          `db:"type"`
	Status             string          `db:"status"`
	Account            string          `db:"account"` // nb_account_id (cloud_accounts.id)
	Tenant             string          `db:"tenant"`
	CloudProvider      string          `db:"cloud_provider"`
	Region             string          `db:"region"`
	ARN                string          `db:"arn"`
	Tags               json.RawMessage `db:"tags"`
	Meta               json.RawMessage `db:"meta"`
	ServiceName        string          `db:"service_name"`
	IsActive           bool            `db:"is_active"`
	ExternalResourceID string          `db:"external_resource_id"`
	AccountNumber      string          `db:"account_number"` // aws_account_id (cloud_accounts.account_number)
}

// CloudEnricher handles enriching knowledge graph nodes with cloud resource information
type CloudEnricher struct {
	logger      *slog.Logger
	nodeMatcher *NodeMatcher
}

// NewCloudEnricher creates a new cloud enricher
func NewCloudEnricher(logger *slog.Logger) *CloudEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &CloudEnricher{
		logger: logger,
	}
}

// EnrichExternalServices matches external services with cloud resources and adds cloud resource nodes
// This is the main enrichment function that:
// 1. Queries cloud_resources table
// 2. Matches external service hostnames to cloud resources
// 3. Creates cloud resource nodes
// 4. Creates edges between services and cloud resources
// 5. Enriches load balancers with backend targets
//
// Parameters:
//   - externalServices: nodes that need to be resolved (external service nodes to enrich)
//   - nodes: existing nodes already in the graph
//   - edges: existing edges already in the graph
//
// Returns:
//   - enrichedNodes: all nodes including existing nodes, external services, and newly created cloud resource nodes
//   - enrichedEdges: all edges including existing edges and new edges linking to cloud resources
func (e *CloudEnricher) EnrichExternalServices(
	reqCtx *security.RequestContext,
	externalServices []*core.DbNode,
	nodes []*core.DbNode,
	edges []*core.DbEdge,
	cloudAccountID, tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	// Initialize NodeMatcher with existing nodes
	e.nodeMatcher = NewNodeMatcher(nodes)
	e.logger.Info("Initialized NodeMatcher for cloud enrichment",
		"existing_nodes_count", len(nodes))

	e.logger.Info("Starting cloud resource enrichment", "external_services_count", countExternalServices(nodes))

	// Collect all external service names that might be DNS names
	externalServiceNames := make([]string, 0)
	externalServiceNodeMap := make(map[string]*core.DbNode)

	for _, node := range externalServices {
		if name, ok := node.Properties["name"].(string); ok && name != "" {
			externalServiceNames = append(externalServiceNames, name)
			externalServiceNodeMap[name] = node
		}
	}

	e.logger.Info("External services found for potential cloud enrichment",
		"count", len(externalServiceNames),
		"services", externalServiceNames)

	if len(externalServiceNames) == 0 {
		e.logger.Info("No external services to enrich")
		// Return nodes with externalServices included
		enrichedNodes := append(nodes, externalServices...)
		return enrichedNodes, edges, nil
	}

	// Step 2.5: Resolve DNS names via Route 53 to discover AWS endpoints BEFORE querying cloud_resources
	// This allows us to query cloud_resources with the actual AWS endpoints
	dnsResolutions := make(map[string]string)        // Map: DNS name -> AWS endpoint
	dnsResolutionAccounts := make(map[string]string) // Map: DNS name -> AWS account that resolved it (for metrics)

	// Get all AWS accounts for the tenant
	awsAccountIDs, err := core.GetAWSAccountsForTenant(tenantID)
	if err != nil {
		e.logger.Warn("Failed to get AWS accounts for DNS resolution", "error", err)
		awsAccountIDs = []string{}
	}

	// Get K8s to AWS account mapping (one K8s account can map to multiple AWS accounts)
	// Filter by AWS since we're doing Route53 DNS resolution
	k8sToAwsAccountMap, err := core.GetK8sCloudAccountMapping(reqCtx, tenantID, "AWS")
	if err != nil {
		e.logger.Warn("Failed to get K8s to AWS account mapping", "error", err)
		k8sToAwsAccountMap = make(map[string][]string)
	} else {
		e.logger.Info("K8s to AWS account mapping retrieved",
			"k8s_accounts_count", len(k8sToAwsAccountMap))
	}

	// Get AWS account IDs mapped to this K8s account (if any)
	// If mapping exists, use mapped AWS accounts instead of tenant-wide AWS accounts
	if mappedAwsAccounts, exists := k8sToAwsAccountMap[cloudAccountID]; exists && len(mappedAwsAccounts) > 0 {
		e.logger.Info("Found AWS accounts mapped to K8s account",
			"k8s_account_id", cloudAccountID,
			"aws_accounts", mappedAwsAccounts)
		awsAccountIDs = mappedAwsAccounts
	}

	if len(awsAccountIDs) > 0 {
		e.logger.Info("Resolving DNS names via Route 53 before cloud resource lookup",
			"hostnames", len(externalServiceNames),
			"aws_accounts", len(awsAccountIDs))

		// Pre-fetch hosted zones once per AWS account (optimization: avoids N*M calls)
		zoneCache := NewRoute53ZoneCache()
		// Cache for record sets per zone (optimization: avoids repeated API calls for same zone)
		recordCache := NewRoute53RecordCache()

		for _, awsAccountID := range awsAccountIDs {
			zones, err := FetchHostedZones(reqCtx, awsAccountID)
			if err != nil {
				e.logger.Warn("Failed to fetch hosted zones for account",
					"aws_account", awsAccountID,
					"error", err)
				continue
			}
			zoneCache.SetZones(awsAccountID, zones)
		}

		resolvedHostnames := make([]string, 0)
		unresolvedHostnames := make([]string, 0)

		for _, hostname := range externalServiceNames {
			// Skip Kubernetes internal DNS names (they're not external services)
			if core.IsKubernetesInternalDNS(hostname) {
				e.logger.Debug("Skipping Kubernetes internal DNS - not an external service",
					"hostname", hostname)
				continue
			}

			resolved := false
			for _, awsAccountID := range awsAccountIDs {
				// Use pre-fetched zones for efficient resolution
				zones := zoneCache.GetZones(awsAccountID)
				if len(zones) == 0 {
					continue // Skip accounts where we couldn't fetch zones or have no hosted zones
				}

				endpoint, err := ResolveRoute53DNSWithCache(reqCtx, hostname, awsAccountID, zones, recordCache)
				if err != nil {
					e.logger.Info("Route 53 resolution failed",
						"hostname", hostname,
						"aws_account", awsAccountID,
						"error", err)
					continue
				}
				if endpoint != "" {
					dnsResolutions[hostname] = endpoint
					dnsResolutionAccounts[hostname] = awsAccountID
					resolvedHostnames = append(resolvedHostnames, hostname)
					e.logger.Info("Route 53 DNS resolved successfully",
						"hostname", hostname,
						"endpoint", endpoint,
						"aws_account", awsAccountID)
					resolved = true
					break // Stop trying other accounts once we find a match
				} else {
					e.logger.Debug("Route 53 resolution returned empty endpoint",
						"hostname", hostname,
						"aws_account", awsAccountID)
				}
			}
			if !resolved {
				unresolvedHostnames = append(unresolvedHostnames, hostname)
			}
		}

		e.logger.Info("Route 53 DNS resolution completed",
			"total_hostnames", len(externalServiceNames),
			"resolved_count", len(dnsResolutions),
			"resolved_hostnames", resolvedHostnames,
			"unresolved_count", len(unresolvedHostnames),
			"unresolved_hostnames", unresolvedHostnames)
	}

	// Build an in-memory endpoint -> node index over the active LB / RDS /
	// ElastiCache nodes already in the graph (created earlier this build cycle
	// by aws_source). This replaces a JSONB query against cloud_resourses; the
	// AWS source flattens every endpoint we used to match with SQL into
	// top-level node properties.
	endpointIdx := buildCloudEndpointIndex(reqCtx, tenantID, nodes, e.logger)
	e.logger.Info("Built cloud resource endpoint index from existing nodes",
		"indexed_endpoints", len(endpointIdx),
		"existing_nodes", len(nodes))

	// Match each candidate name (external service hostname or Route53-resolved
	// AWS endpoint) against the index, and link to the matching cloud resource.
	newNodes := make([]*core.DbNode, 0)
	newEdges := make([]*core.DbEdge, 0)
	matchedCloudNodes := make([]*core.DbNode, 0)
	seenMatched := make(map[string]bool)

	// Iterate the external-service map in sorted order so build artifacts are
	// reproducible across runs (Go map iteration is non-deterministic).
	esNames := make([]string, 0, len(externalServiceNodeMap))
	for esName := range externalServiceNodeMap {
		esNames = append(esNames, esName)
	}
	sort.Strings(esNames)

	for _, esName := range esNames {
		esNode := externalServiceNodeMap[esName]

		// Direct: external-service hostname is itself the cloud resource endpoint.
		if cloudNode, field := findCloudResourceByEndpoint(endpointIdx, esName); cloudNode != nil {
			newEdges = append(newEdges, makeRoutesThroughEdge(esNode, cloudNode, "dns_name", esName, "", cloudAccountID, tenantID))
			if !seenMatched[cloudNode.ID] {
				seenMatched[cloudNode.ID] = true
				matchedCloudNodes = append(matchedCloudNodes, cloudNode)
			}
			e.logger.Info("Matched external service to existing cloud resource node",
				"external_service", esName,
				"cloud_resource", cloudNode.Properties["name"],
				"node_type", cloudNode.NodeType,
				"matched_field", field,
				"unique_key", cloudNode.UniqueKey)
			continue
		}

		// Indirect: external-service hostname resolved via Route53 to an AWS
		// endpoint that matches a cloud resource node.
		awsEndpoint, hasResolution := dnsResolutions[esName]
		if !hasResolution {
			continue
		}
		cloudNode, field := findCloudResourceByEndpoint(endpointIdx, awsEndpoint)
		if cloudNode == nil {
			// We resolved DNS but couldn't find a node — the inferred-node
			// fallback below will handle this case. Surface as a warning so we
			// can spot AWS source coverage gaps in production.
			e.logger.Warn("Route53 resolved AWS endpoint but no cloud resource node in graph",
				"external_service", esName,
				"aws_endpoint", awsEndpoint,
				"hint", "AWS source may have skipped this resource; falling back to inferred node")
			common.MetricsKGRoute53Unmatched(reqCtx.GetContext(), tenantID, dnsResolutionAccounts[esName])
			continue
		}
		matchValue := fmt.Sprintf("%s -> %s", esName, awsEndpoint)
		newEdges = append(newEdges, makeRoutesThroughEdge(esNode, cloudNode, "route53_resolution", matchValue, esName, cloudAccountID, tenantID))
		if !seenMatched[cloudNode.ID] {
			seenMatched[cloudNode.ID] = true
			matchedCloudNodes = append(matchedCloudNodes, cloudNode)
		}
		e.logger.Info("Matched external service to existing cloud resource node via Route53",
			"external_service", esName,
			"aws_endpoint", awsEndpoint,
			"cloud_resource", cloudNode.Properties["name"],
			"node_type", cloudNode.NodeType,
			"matched_field", field,
			"unique_key", cloudNode.UniqueKey)
	}

	// Step: Create inferred cloud resource nodes for DNS resolutions not found in database
	inferredCloudNodes := make(map[string]*core.DbNode)
	for dnsName, awsEndpoint := range dnsResolutions {
		nodeType, resourceType := classifyAWSEndpoint(awsEndpoint)

		// Try to find existing node with this DNS name or endpoint
		var inferredNode *core.DbNode
		result, err := e.nodeMatcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "dns_name",
					Value:         dnsName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			inferredNode = result.Node
			e.logger.Debug("Found existing inferred node by DNS name",
				"dns_name", dnsName,
				"node_type", nodeType,
				"unique_key", inferredNode.UniqueKey)
		} else {
			// Try matching by name (endpoint)
			result, err = e.nodeMatcher.FindNode(MatchCriteria{
				NodeType: nodeType,
				PropertyMatches: []PropertyMatch{
					{
						PropertyPath:  "name",
						Value:         awsEndpoint,
						MatchType:     core.MatchTypeExact,
						CaseSensitive: false,
					},
				},
			})
			if err == nil && result.Matched {
				inferredNode = result.Node
				e.logger.Debug("Found existing inferred node by endpoint",
					"aws_endpoint", awsEndpoint,
					"node_type", nodeType,
					"unique_key", inferredNode.UniqueKey)
			}
		}

		// Try matching by resource name extracted from the endpoint hostname
		// (e.g. bucket name from S3, DB identifier from RDS, distribution ID from
		// CloudFront). Static AWS sources store the bare resource identifier in
		// `name` or `resource_id` rather than the full endpoint, so without this
		// pass we'd create duplicate inferred nodes alongside the real ones.
		if inferredNode == nil {
			if resName := extractResourceNameFromEndpoint(awsEndpoint); resName != "" {
				for _, prop := range []string{"name", "resource_id"} {
					result, err = e.nodeMatcher.FindNode(MatchCriteria{
						NodeType: nodeType,
						PropertyMatches: []PropertyMatch{
							{
								PropertyPath:  prop,
								Value:         resName,
								MatchType:     core.MatchTypeExact,
								CaseSensitive: false,
							},
						},
					})
					if err == nil && result.Matched {
						inferredNode = result.Node
						// Stamp the DNS name on the matched node so future runs
						// match by the cheaper dns_name path above.
						if inferredNode.Properties == nil {
							inferredNode.Properties = map[string]interface{}{}
						}
						if existing, _ := inferredNode.Properties["dns_name"].(string); existing == "" {
							inferredNode.Properties["dns_name"] = dnsName
						}
						e.logger.Debug("Found existing node by extracted resource name",
							"matched_property", prop,
							"resource_name", resName,
							"aws_endpoint", awsEndpoint,
							"node_type", nodeType,
							"unique_key", inferredNode.UniqueKey)
						break
					}
				}
			}
		}

		// If no existing node found, create a new inferred node
		if inferredNode == nil {
			inferredNode = &core.DbNode{
				ID:             uuid.New().String(),
				NodeType:       nodeType,
				UniqueKey:      fmt.Sprintf("%s:%s:%s", nodeType, awsEndpoint, "inferred"),
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				Level:          "Tenant",
				Source:         "cloud",
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
				Properties: map[string]interface{}{
					"name":             awsEndpoint,
					"type":             resourceType,
					"dns_name":         dnsName,
					"inferred":         true,
					"discovery_method": "route53_dns_resolution",
				},
				Labels:          map[string]string{},
				QueryAttributes: map[string]interface{}{},
			}
			newNodes = append(newNodes, inferredNode)
			e.logger.Debug("Created new inferred cloud resource node",
				"dns_name", dnsName,
				"aws_endpoint", awsEndpoint,
				"resource_type", resourceType,
				"node_type", nodeType)
		}

		inferredCloudNodes[awsEndpoint] = inferredNode

		// Create edge from external service to inferred cloud resource.
		// This RESOLVES_TO edge is the *signal* consumed by
		// core.CollapseEnrichedExternalServices in BuildGraphs Phase 3.5:
		// the collapse pass walks bridge edges, repoints inbound CALLS to
		// the target, and removes both this edge and the ES node. If you add
		// a new enrichment branch, make sure it also emits a bridge edge or
		// the collapse will leave the ES intact.
		if externalServiceNode, exists := externalServiceNodeMap[dnsName]; exists {
			edge := &core.DbEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      externalServiceNode.ID,
				DestinationNodeID: inferredNode.ID,
				RelationshipType:  core.RelationshipResolvesTo,
				Properties: map[string]interface{}{
					"discovered_from":        "route53_dns_resolution",
					"dns_name":               dnsName,
					"aws_endpoint":           awsEndpoint,
					"created_by_flow_source": cloudEnrichmentSourceName,
					"flow_source_category":   cloudEnrichmentCategory,
					"source_priority":        int(GetEdgeSourcePriority(cloudEnrichmentSourceName, core.RelationshipResolvesTo)),
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				Level:          "Tenant",
				Source:         "cloud",
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			newEdges = append(newEdges, edge)

			e.logger.Info("Linked external service to cloud resource (DNS resolution)",
				"dns_name", dnsName,
				"aws_endpoint", awsEndpoint,
				"resource_type", resourceType,
				"node_type", nodeType,
				"existing_node", inferredNode.UniqueKey != fmt.Sprintf("%s:%s:%s", nodeType, awsEndpoint, "inferred"))
		}
	}

	// Step: Enrich LoadBalancer nodes with backend pod targets. Iterate the
	// union of LBs we matched against existing nodes (the common case after
	// the SQL→index refactor) and any LBs minted by the inferred-node fallback,
	// deduped by node ID so a node present in both lists is enriched once.
	lbCandidates := make([]*core.DbNode, 0, len(matchedCloudNodes)+len(newNodes))
	lbSeen := make(map[string]bool)
	for _, n := range matchedCloudNodes {
		if !lbSeen[n.ID] {
			lbSeen[n.ID] = true
			lbCandidates = append(lbCandidates, n)
		}
	}
	for _, n := range newNodes {
		if !lbSeen[n.ID] {
			lbSeen[n.ID] = true
			lbCandidates = append(lbCandidates, n)
		}
	}

	e.logger.Info("Starting LoadBalancer target enrichment", "loadbalancer_count", len(lbCandidates))
	podEnrichmentNodes := make([]*core.DbNode, 0)
	podEnrichmentEdges := make([]*core.DbEdge, 0)

	for _, cloudNode := range lbCandidates {
		if cloudNode.NodeType == core.NodeTypeLoadBalancer {
			// Get AWS account ID from the cloud resource properties
			awsAccountID, ok := cloudNode.Properties["aws_account_id"].(string)
			if !ok || awsAccountID == "" {
				e.logger.Debug("Skipping LoadBalancer enrichment: no AWS account ID",
					"lb_name", cloudNode.Properties["name"])
				continue
			}

			// Enrich this LoadBalancer with its pod targets
			podNodes, podEdges, err := e.EnrichLoadBalancers(
				reqCtx,
				[]*core.DbNode{cloudNode},
				nodes,
				awsAccountID,
				cloudAccountID,
				tenantID,
			)

			if err != nil {
				e.logger.Warn("Failed to enrich LoadBalancer with targets",
					"lb_name", cloudNode.Properties["name"],
					"error", err)
				continue
			}

			podEnrichmentNodes = append(podEnrichmentNodes, podNodes...)
			podEnrichmentEdges = append(podEnrichmentEdges, podEdges...)

			e.logger.Info("LoadBalancer enriched with pod targets",
				"lb_name", cloudNode.Properties["name"],
				"pods_added", len(podNodes))
		}
	}

	// Combine all nodes and edges
	// enrichedNodes includes: existing nodes + external services + new cloud resource nodes + pod nodes
	enrichedNodes := append(nodes, externalServices...)
	enrichedNodes = append(enrichedNodes, newNodes...)
	enrichedNodes = append(enrichedNodes, podEnrichmentNodes...)
	enrichedEdges := append(edges, newEdges...)
	enrichedEdges = append(enrichedEdges, podEnrichmentEdges...)

	e.logger.Info("Cloud resource enrichment completed",
		"added_cloud_resource_nodes", len(newNodes),
		"added_pod_nodes", len(podEnrichmentNodes),
		"added_edges", len(newEdges)+len(podEnrichmentEdges))

	return enrichedNodes, enrichedEdges, nil
}

// EnrichLoadBalancers enriches LoadBalancer nodes with their backend pod targets
// This handles cross-account mapping between AWS LoadBalancers and K8s pods/services
func (e *CloudEnricher) EnrichLoadBalancers(
	reqCtx *security.RequestContext,
	lbNodes []*core.DbNode,
	existingNodes []*core.DbNode,
	awsAccountID, k8sAccountID, tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	allPodNodes := make([]*core.DbNode, 0)
	allEdges := make([]*core.DbEdge, 0)

	for _, lbNode := range lbNodes {
		if lbNode.NodeType != core.NodeTypeLoadBalancer {
			continue
		}

		podNodes, edges, err := e.enrichLoadBalancerWithTargets(
			reqCtx, lbNode, existingNodes, awsAccountID, k8sAccountID, tenantID)
		if err != nil {
			e.logger.Warn("Failed to enrich LoadBalancer",
				"lb_name", lbNode.Properties["name"],
				"error", err)
			continue
		}

		allPodNodes = append(allPodNodes, podNodes...)
		allEdges = append(allEdges, edges...)
	}

	return allPodNodes, allEdges, nil
}

// enrichLoadBalancerWithTargets enriches a LoadBalancer node with its backend pod/service targets
// This handles cross-account mapping between AWS LoadBalancers and K8s pods/services
// Uses DbNodes directly without type conversion
func (e *CloudEnricher) enrichLoadBalancerWithTargets(
	reqCtx *security.RequestContext,
	lbNode *core.DbNode,
	existingNodes []*core.DbNode,
	awsAccountID, k8sAccountID, tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	podNodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)

	// Build a map of existing Service nodes for quick lookup by name+namespace+cluster
	serviceNodeMap := make(map[string]*core.DbNode)
	// Build a map to track pod owner nodes (Deployment/StatefulSet/DaemonSet)
	ownerNodeMap := make(map[string]*core.DbNode)
	for _, node := range existingNodes {
		if node.NodeType == core.NodeTypeService {
			name, _ := node.Properties["name"].(string)
			namespace, _ := node.Properties["namespace"].(string)
			cluster, _ := node.Properties["cluster"].(string)
			if name != "" {
				// Key format: name:namespace:cluster (namespace and cluster can be empty)
				key := fmt.Sprintf("%s:%s:%s", name, namespace, cluster)
				serviceNodeMap[key] = node
			}
		}
	}

	// Extract LoadBalancer ARN and region from properties
	arn, arnOk := lbNode.Properties["arn"].(string)
	region, regionOk := lbNode.Properties["region"].(string)

	if !arnOk || !regionOk || arn == "" || region == "" {
		e.logger.Debug("Skipping LoadBalancer enrichment: missing ARN or region",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1: Query AWS for target groups
	tgCommand := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, arn,
	)

	tgResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tgCommand,
	})
	if err != nil {
		e.logger.Warn("Failed to query LoadBalancer target groups",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Parse target groups
	var targetGroups []map[string]interface{}
	if data, ok := tgResp["data"].(string); ok {
		var tgData struct {
			TargetGroups []map[string]interface{} `json:"TargetGroups"`
		}
		if err := json.Unmarshal([]byte(data), &tgData); err != nil {
			e.logger.Warn("Failed to parse target groups", "error", err)
			return podNodes, edges, nil
		}
		targetGroups = tgData.TargetGroups
	}

	if len(targetGroups) == 0 {
		e.logger.Debug("No target groups found for LoadBalancer",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1.5: Query LoadBalancer tags to check for Kubernetes service mapping
	tagsCommand := fmt.Sprintf(
		"aws elbv2 describe-tags --resource-arns %s --output json",
		arn,
	)

	tagsResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tagsCommand,
	})

	var k8sServiceName, k8sNamespace string
	if err == nil && tagsResp != nil {
		if data, ok := tagsResp["data"].(string); ok {
			var tagsData struct {
				TagDescriptions []struct {
					Tags []struct {
						Key   string `json:"Key"`
						Value string `json:"Value"`
					} `json:"Tags"`
				} `json:"TagDescriptions"`
			}
			if json.Unmarshal([]byte(data), &tagsData) == nil && len(tagsData.TagDescriptions) > 0 {
				for _, tag := range tagsData.TagDescriptions[0].Tags {
					if tag.Key == "kubernetes.io/service-name" {
						parts := strings.Split(tag.Value, "/")
						if len(parts) == 2 {
							k8sNamespace = parts[0]
							k8sServiceName = parts[1]
							e.logger.Info("Found Kubernetes service for LoadBalancer",
								"lb_name", lbNode.Properties["name"],
								"k8s_service", tag.Value)
						}
						break
					}
				}
			}
		}
	}

	// If this LB is for an ingress controller, create ingress node and skip pod mapping
	if k8sNamespace != "" && k8sServiceName != "" && strings.Contains(k8sServiceName, "ingress") {
		// Infer environment from LoadBalancer tags or use "inferred"
		environment := "inferred"
		if lbEnv, ok := lbNode.Properties["environment"].(string); ok && lbEnv != "" {
			environment = lbEnv
		}

		ingressNode := &core.DbNode{
			ID:             uuid.New().String(),
			NodeType:       core.NodeTypeService,
			UniqueKey:      fmt.Sprintf("Service:%s:%s", k8sServiceName, k8sNamespace),
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Properties: map[string]interface{}{
				"name":         k8sServiceName,
				"namespace":    k8sNamespace,
				"environment":  environment,
				"type":         "nginx",
				"service.name": k8sServiceName,
			},
			Labels:          map[string]string{},
			QueryAttributes: map[string]interface{}{},
		}

		edge := &core.DbEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.ID,
			DestinationNodeID: ingressNode.ID,
			RelationshipType:  core.RelationshipRoutesTo,
			Properties: map[string]interface{}{
				"discovered_from": "aws_lb_tags",
				"service_name":    fmt.Sprintf("%s/%s", k8sNamespace, k8sServiceName),
			},
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		e.logger.Info("Created ingress controller node for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"ingress_service", k8sServiceName,
			"namespace", k8sNamespace)

		// Collect initial nodes and edges
		nodes := []*core.DbNode{ingressNode}
		resultEdges := []*core.DbEdge{edge}

		// Step 1.5: Resolve Ingress resources to backend services
		// Query for Ingress resources across all namespaces to find backend services
		ingressBackendNodes, ingressBackendEdges, err := ResolveIngressBackendServices(reqCtx, k8sAccountID, tenantID, environment, ingressNode)
		if err != nil {
			e.logger.Warn("Failed to resolve Ingress backend services",
				"error", err,
				"ingress_service", k8sServiceName)
			// Continue without backend resolution - this is not a fatal error
		} else if ingressBackendNodes != nil {
			nodes = append(nodes, ingressBackendNodes...)
			resultEdges = append(resultEdges, ingressBackendEdges...)
			e.logger.Info("Resolved Ingress backend services",
				"ingress_service", k8sServiceName,
				"backend_services_count", len(ingressBackendNodes))
		}

		return nodes, resultEdges, nil
	}

	// Step 2: Collect all target IPs and instance IDs from all target groups
	uniqueIPs := make(map[string]bool)
	instanceIDs := make(map[string]bool)

	for _, tg := range targetGroups {
		tgArn, ok := tg["TargetGroupArn"].(string)
		if !ok {
			continue
		}

		healthCommand := fmt.Sprintf(
			"aws elbv2 describe-target-health --region %s --target-group-arn %s --output json",
			region, tgArn,
		)

		healthResp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   healthCommand,
		})
		if err != nil {
			e.logger.Warn("Failed to query target health", "target_group", tgArn, "error", err)
			continue
		}

		// Parse target health
		if data, ok := healthResp["data"].(string); ok {
			var healthData struct {
				TargetHealthDescriptions []map[string]interface{} `json:"TargetHealthDescriptions"`
			}
			if err := json.Unmarshal([]byte(data), &healthData); err != nil {
				continue
			}

			for _, target := range healthData.TargetHealthDescriptions {
				if targetInfo, ok := target["Target"].(map[string]interface{}); ok {
					if targetID, ok := targetInfo["Id"].(string); ok {
						// Check if this is an instance ID (starts with "i-") or an IP address
						if strings.HasPrefix(targetID, "i-") {
							// This is an EC2 instance ID - collect it for resolution
							instanceIDs[targetID] = true
						} else {
							// It's an IP address - add it directly
							uniqueIPs[targetID] = true
						}
					}
				}
			}
		}
	}

	// Step 2b: Resolve EC2 instance IDs to private IPs
	if len(instanceIDs) > 0 {
		e.logger.Info("Resolving EC2 instance IDs to private IPs",
			"lb_name", lbNode.Properties["name"],
			"instance_count", len(instanceIDs))

		// Build space-separated list of instance IDs
		instanceIDList := make([]string, 0, len(instanceIDs))
		for instanceID := range instanceIDs {
			instanceIDList = append(instanceIDList, instanceID)
		}
		instanceIDStr := strings.Join(instanceIDList, " ")

		// Query EC2 to get private IPs for all instances in one call
		ec2Command := fmt.Sprintf(
			"aws ec2 describe-instances --region %s --instance-ids %s --query 'Reservations[].Instances[].[InstanceId,PrivateIpAddress]' --output json",
			region, instanceIDStr,
		)

		ec2Resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   ec2Command,
		})
		if err != nil {
			e.logger.Warn("Failed to query EC2 instances",
				"lb_name", lbNode.Properties["name"],
				"error", err)
		} else {
			// Parse EC2 response: [[instanceID, privateIP], ...]
			if data, ok := ec2Resp["data"].(string); ok {
				var instances [][]string
				if err := json.Unmarshal([]byte(data), &instances); err == nil {
					for _, inst := range instances {
						if len(inst) == 2 {
							instanceID := inst[0]
							privateIP := inst[1]
							if privateIP != "" {
								uniqueIPs[privateIP] = true
								e.logger.Debug("Resolved EC2 instance to private IP",
									"instance_id", instanceID,
									"private_ip", privateIP,
									"lb_name", lbNode.Properties["name"])
							}
						}
					}
					e.logger.Info("EC2 instance resolution completed",
						"lb_name", lbNode.Properties["name"],
						"instances_resolved", len(instances),
						"total_ips", len(uniqueIPs))
				} else {
					e.logger.Warn("Failed to parse EC2 response", "error", err)
				}
			}
		}
	}

	if len(uniqueIPs) == 0 {
		e.logger.Info("No target IPs found for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"instance_targets_attempted", len(instanceIDs))
		return podNodes, edges, nil
	}

	// Step 3: Query kube_pod_info to map IPs to pod names (using K8s account ID)
	ipList := make([]string, 0, len(uniqueIPs))
	for ip := range uniqueIPs {
		ipList = append(ipList, ip)
	}
	ipFilter := strings.Join(ipList, "|")

	queries := map[string]string{
		"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
	}

	// Use UTC: relay.ExecutePrometheus formats the timestamp with a "UTC"
	// suffix; the value must already be UTC or Prometheus is queried at a
	// future time and returns empty. Defense in depth with the relay-side fix.
	endTime := time.Now().UTC()
	startTime := endTime.Add(-5 * time.Minute)

	podInfoResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		e.logger.Warn("Failed to query kube_pod_info",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Step 4: Create Pod nodes and edges from query results
	var resultArray []interface{}

	// Try to get the array from the response
	if podInfoData, ok := podInfoResp["pod_info"].([]interface{}); ok {
		resultArray = podInfoData
		e.logger.Debug("Found pod info with query name key", "count", len(podInfoData))
	} else if data, ok := podInfoResp["data"].([]interface{}); ok {
		resultArray = data
		e.logger.Debug("Found pod info in data array", "count", len(data))
	} else if data, ok := podInfoResp["data"].(map[string]interface{}); ok {
		if podInfoData, ok := data["pod_info"].(map[string]interface{}); ok {
			if result, ok := podInfoData["result"].([]interface{}); ok {
				resultArray = result
				e.logger.Debug("Found pod info in nested structure", "count", len(result))
			}
		}
	}

	if len(resultArray) == 0 {
		e.logger.Warn("No pod info results found in Prometheus response",
			"lb_name", lbNode.Properties["name"],
			"target_ips", len(uniqueIPs))
	}

	// Collect all ReplicaSets we need to query for owners
	replicaSetsToQuery := make(map[string]bool) // key: "namespace/replicaset-name"
	podMetrics := make([]map[string]interface{}, 0)

	for _, item := range resultArray {
		if pod, ok := item.(map[string]interface{}); ok {
			if metric, ok := pod["metric"].(map[string]interface{}); ok {
				podMetrics = append(podMetrics, metric)

				// If created by ReplicaSet, we'll need to query for its owner (Deployment)
				createdByKind, _ := metric["created_by_kind"].(string)
				createdByName, _ := metric["created_by_name"].(string)
				namespace, _ := metric["namespace"].(string)

				if createdByKind == "ReplicaSet" && createdByName != "" && namespace != "" {
					replicaSetsToQuery[fmt.Sprintf("%s/%s", namespace, createdByName)] = true
				}
			}
		}
	}

	// Step 5: Query kube_replicaset_owner to get Deployment owners for ReplicaSets
	replicaSetOwners := make(map[string]map[string]string) // key: "namespace/replicaset" -> {"kind": "Deployment", "name": "xxx"}
	if len(replicaSetsToQuery) > 0 {
		rsQueries := map[string]string{
			"rs_owner": "kube_replicaset_owner",
		}

		rsResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, rsQueries, true)
		if err == nil {
			var rsResultArray []interface{}
			if rsData, ok := rsResp["rs_owner"].([]interface{}); ok {
				rsResultArray = rsData
			} else if data, ok := rsResp["data"].([]interface{}); ok {
				rsResultArray = data
			} else if data, ok := rsResp["data"].(map[string]interface{}); ok {
				if rsData, ok := data["rs_owner"].(map[string]interface{}); ok {
					if result, ok := rsData["result"].([]interface{}); ok {
						rsResultArray = result
					}
				}
			}

			for _, item := range rsResultArray {
				if rs, ok := item.(map[string]interface{}); ok {
					if metric, ok := rs["metric"].(map[string]interface{}); ok {
						rsNamespace, _ := metric["namespace"].(string)
						rsName, _ := metric["replicaset"].(string)
						ownerKind, _ := metric["owner_kind"].(string)
						ownerName, _ := metric["owner_name"].(string)

						if rsNamespace != "" && rsName != "" {
							key := fmt.Sprintf("%s/%s", rsNamespace, rsName)
							replicaSetOwners[key] = map[string]string{
								"kind": ownerKind,
								"name": ownerName,
							}
						}
					}
				}
			}
		}
	}

	// Step 6: Process all pod metrics and create owner nodes
	for _, metric := range podMetrics {
		podIP, _ := metric["pod_ip"].(string)
		podName, _ := metric["pod"].(string)
		namespace, _ := metric["namespace"].(string)
		k8sCluster, _ := metric["k8s_cluster"].(string)
		createdByKind, _ := metric["created_by_kind"].(string)
		createdByName, _ := metric["created_by_name"].(string)

		if podName == "" || namespace == "" {
			continue
		}

		// Determine the actual owner (resolve ReplicaSet -> Deployment)
		ownerKind := createdByKind
		ownerName := createdByName

		if createdByKind == "ReplicaSet" && createdByName != "" {
			rsKey := fmt.Sprintf("%s/%s", namespace, createdByName)
			if owner, found := replicaSetOwners[rsKey]; found && owner["kind"] != "" {
				ownerKind = owner["kind"]
				ownerName = owner["name"]
			} else {
				// Fallback: extract deployment name from ReplicaSet name
				// ReplicaSet pattern: {deployment-name}-{hash}
				ownerName = extractDeploymentFromReplicaSet(createdByName)
				ownerKind = "Deployment"
			}
		}

		// If no owner info, skip this pod
		if ownerKind == "" || ownerName == "" {
			e.logger.Debug("Skipping pod without owner info",
				"pod_name", podName,
				"namespace", namespace)
			continue
		}

		// Try to find matching Service node using owner name
		var targetNode *core.DbNode
		var targetNodeType = core.NodeTypePod // Default to Pod owner

		// Try different combinations to find matching service
		serviceKeys := []string{
			fmt.Sprintf("%s:%s:%s", ownerName, namespace, k8sCluster),
			fmt.Sprintf("%s:%s:", ownerName, namespace),
			fmt.Sprintf("%s::%s", ownerName, k8sCluster),
			fmt.Sprintf("%s::", ownerName),
		}

		for _, key := range serviceKeys {
			if svcNode, found := serviceNodeMap[key]; found {
				targetNode = svcNode
				targetNodeType = core.NodeTypeService
				e.logger.Info("Found matching Service for LoadBalancer target",
					"lb_name", lbNode.Properties["name"],
					"pod_name", podName,
					"owner_name", ownerName,
					"service_key", key)
				break
			}
		}

		// If no Service match found, create owner node (Deployment/StatefulSet/DaemonSet)
		if targetNode == nil {
			// Create unique key for the owner: namespace:kind:name
			ownerKey := fmt.Sprintf("%s:%s:%s", namespace, ownerKind, ownerName)

			// Check if we already created this owner node
			if existingOwner, found := ownerNodeMap[ownerKey]; found {
				// Add this pod to the existing owner's pod list
				if pods, ok := existingOwner.Properties["pods"].([]string); ok {
					existingOwner.Properties["pods"] = append(pods, podName)
				}
				targetNode = existingOwner
			} else {
				// Create new owner node with ID format: namespace:kind:name
				// Preserve all metric labels from Prometheus first
				labels := make(map[string]string)
				for k, v := range metric {
					if strVal, ok := v.(string); ok {
						labels[k] = strVal
					}
				}

				// Build properties with standard fields extracted from labels
				properties := map[string]interface{}{
					"name":       ownerName,
					"namespace":  namespace,
					"owner_kind": ownerKind,
					"pods":       []string{podName},
				}

				// Extract commonly-used K8s fields from labels to top-level for easy access
				if k8sCluster != "" {
					properties["k8s_cluster"] = k8sCluster
				} else if cluster, ok := labels["k8s_cluster"]; ok && cluster != "" {
					properties["k8s_cluster"] = cluster
				}

				if node, ok := labels["node"]; ok && node != "" {
					properties["node"] = node
				}

				if hostIP, ok := labels["host_ip"]; ok && hostIP != "" {
					properties["host_ip"] = hostIP
				}

				// Store all labels for full context
				if len(labels) > 0 {
					properties["labels"] = labels
				}

				targetNode = &core.DbNode{
					ID:              uuid.New().String(),
					UniqueKey:       fmt.Sprintf("%s:%s:%s", ownerKind, ownerName, namespace),
					NodeType:        core.NodeTypePod, // Still use Pod type for infrastructure
					CloudAccountID:  k8sAccountID,
					TenantID:        tenantID,
					Level:           "Tenant",
					Source:          "cloud",
					Properties:      properties,
					Labels:          labels,
					QueryAttributes: map[string]interface{}{},
					CreatedAt:       time.Now(),
					UpdatedAt:       time.Now(),
				}
				ownerNodeMap[ownerKey] = targetNode
				podNodes = append(podNodes, targetNode)
			}
		}

		// Create edge: LoadBalancer -> Service/Owner
		edge := &core.DbEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.ID,
			DestinationNodeID: targetNode.ID,
			RelationshipType:  core.RelationshipRoutesTo,
			Properties: map[string]interface{}{
				"discovered_from": "aws_target_health",
				"target_ip":       podIP,
				"pod_name":        podName,
				"owner_kind":      ownerKind,
				"owner_name":      ownerName,
			},
			CloudAccountID: awsAccountID,
			TenantID:       tenantID,
			Level:          "Tenant",
			Source:         "cloud",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, edge)

		e.logger.Info("Linked LoadBalancer to target",
			"lb_name", lbNode.Properties["name"],
			"target_type", targetNodeType,
			"target_name", targetNode.Properties["name"],
			"pod_name", podName,
			"owner_kind", ownerKind,
			"owner_name", ownerName,
			"namespace", namespace,
			"pod_ip", podIP)
	}

	e.logger.Info("LoadBalancer target enrichment completed",
		"lb_name", lbNode.Properties["name"],
		"target_ips", len(uniqueIPs),
		"pods_discovered", len(podNodes))

	return podNodes, edges, nil
}

// extractDeploymentFromReplicaSet extracts the deployment name from a ReplicaSet name
// ReplicaSet pattern: {deployment-name}-{hash}
func extractDeploymentFromReplicaSet(replicaSetName string) string {
	parts := strings.Split(replicaSetName, "-")
	if len(parts) < 2 {
		return replicaSetName
	}

	// Remove last part if it looks like a ReplicaSet hash (typically 9-10 alphanumeric chars)
	lastPart := parts[len(parts)-1]
	if len(lastPart) >= 8 && len(lastPart) <= 10 && isAlphanumeric(lastPart) {
		return strings.Join(parts[:len(parts)-1], "-")
	}

	return replicaSetName
}

// awsEndpointRule classifies a Route53-resolved AWS endpoint hostname into a
// (NodeType, resourceType) pair for inferred cloud-resource nodes. Rules are
// evaluated in order; the first match wins. `match` receives the lowercased
// endpoint.
type awsEndpointRule struct {
	resourceType string
	nodeType     core.NodeType
	match        func(endpoint string) bool
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// awsEndpointRules orders specific (longer / multi-token) suffixes ahead of
// generic ones. CloudFront is matched on `.cloudfront.net` (no `amazonaws.com`).
var awsEndpointRules = []awsEndpointRule{
	{"elasticache_inferred", core.NodeTypeCache, func(e string) bool { return strings.Contains(e, ".cache.amazonaws.com") }},
	{"rds_inferred", core.NodeTypeDatabase, func(e string) bool { return strings.Contains(e, ".rds.amazonaws.com") }},
	{"cloudfront_inferred", core.NodeTypeCDN, func(e string) bool { return strings.HasSuffix(e, ".cloudfront.net") }},
	{"apigateway_inferred", core.NodeTypeAPIGateway, func(e string) bool { return strings.Contains(e, ".execute-api.") }},
	{"lambda_inferred", core.NodeTypeServerlessFunction, func(e string) bool { return strings.Contains(e, ".lambda-url.") }},
	{"dynamodb_inferred", core.NodeTypeDatabase, func(e string) bool {
		return strings.HasPrefix(e, "dynamodb.") && strings.HasSuffix(e, ".amazonaws.com")
	}},
	{"sqs_inferred", core.NodeTypeMessageQueue, func(e string) bool { return strings.HasPrefix(e, "sqs.") && strings.HasSuffix(e, ".amazonaws.com") }},
	{"sns_inferred", core.NodeTypeMessageQueue, func(e string) bool { return strings.HasPrefix(e, "sns.") && strings.HasSuffix(e, ".amazonaws.com") }},
	{"s3_inferred", core.NodeTypeStorage, func(e string) bool {
		if !strings.Contains(e, "amazonaws.com") {
			return false
		}
		return hasAny(e, ".s3.", ".s3-") || strings.HasPrefix(e, "s3.") || strings.HasPrefix(e, "s3-")
	}},
}

// classifyAWSEndpoint maps a Route53-resolved AWS endpoint to the NodeType and
// inferred-resource-type label used when synthesising cloud-resource nodes for
// hostnames absent from the cloud_resourses table.
func classifyAWSEndpoint(endpoint string) (core.NodeType, string) {
	e := strings.ToLower(endpoint)
	for _, r := range awsEndpointRules {
		if r.match(e) {
			return r.nodeType, r.resourceType
		}
	}
	return core.NodeTypeExternalService, "aws_service_inferred"
}

// extractResourceNameFromEndpoint pulls the per-resource identifier out of an
// AWS endpoint hostname so we can match it against existing nodes whose `name`
// or `resource_id` property holds that bare identifier. Returns "" when the
// endpoint is regional/global and carries no per-resource component (DynamoDB,
// SQS, SNS — the resource name is in the request path, not the host).
func extractResourceNameFromEndpoint(endpoint string) string {
	e := strings.ToLower(endpoint)
	parts := strings.Split(e, ".")
	if len(parts) < 2 {
		return ""
	}
	first := parts[0]

	switch {
	// S3 virtual-hosted (<bucket>.s3.*) and website (<bucket>.s3-website*).
	// Path-style (s3.amazonaws.com / s3-<region>.amazonaws.com) has no bucket
	// in the host, so first == "s3" or starts with "s3-" → return "".
	case strings.Contains(e, ".s3.") || strings.Contains(e, ".s3-"):
		if first == "s3" || strings.HasPrefix(first, "s3-") {
			return ""
		}
		return first
	// RDS: <db-id>.<random>.<region>.rds.amazonaws.com
	case strings.Contains(e, ".rds.amazonaws.com"):
		return first
	// ElastiCache: <cluster-id>.<random>...cache.amazonaws.com
	case strings.Contains(e, ".cache.amazonaws.com"):
		return first
	// CloudFront: <distribution-id>.cloudfront.net (matches resource_id)
	case strings.HasSuffix(e, ".cloudfront.net"):
		return first
	// API Gateway: <api-id>.execute-api.<region>.amazonaws.com (matches resource_id)
	case strings.Contains(e, ".execute-api."):
		return first
	// Lambda Function URL: <url-id>.lambda-url.<region>.on.aws (matches resource_id)
	case strings.Contains(e, ".lambda-url."):
		return first
	}
	return ""
}

// isAlphanumeric checks if a string contains only lowercase alphanumeric characters
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// extractLabelsFromProperties extracts labels from properties map
//
//lint:ignore U1000 This function may be used in future enhancements
func extractLabelsFromProperties(properties map[string]interface{}) map[string]string {
	labels := make(map[string]string)
	if labelsAny, ok := properties["labels"]; ok {
		switch v := labelsAny.(type) {
		case map[string]string:
			return v
		case map[string]interface{}:
			for k, val := range v {
				if strVal, ok := val.(string); ok {
					labels[k] = strVal
				}
			}
		}
	}
	return labels
}

// Helper functions

// determineCloudResourceNodeType maps cloud resource type to knowledge graph node type.
// Uses generic cloud-agnostic types (not AWS-specific) per core package design.
// Returns (type, true) on a known mapping, or ("", false) when the resource type
// cannot be classified — callers should skip node creation in that case.
func determineCloudResourceNodeType(resourceType string) (core.NodeType, bool) {
	resourceTypeLower := strings.ToLower(resourceType)

	switch {
	case strings.Contains(resourceTypeLower, "loadbalancer"):
		return core.NodeTypeLoadBalancer, true
	case resourceTypeLower == "db", strings.Contains(resourceTypeLower, "rds"):
		return core.NodeTypeDatabase, true // Generic: RDS, Azure SQL, Cloud SQL
	case strings.Contains(resourceTypeLower, "elasticache"):
		return core.NodeTypeCache, true
	case resourceTypeLower == "storage", strings.Contains(resourceTypeLower, "s3"):
		return core.NodeTypeStorage, true // Generic: S3, Azure Blob, Cloud Storage
	case resourceTypeLower == "compute-instance", strings.Contains(resourceTypeLower, "ec2"):
		return core.NodeTypeComputeInstance, true // Generic: EC2, Azure VM, Compute Engine
	case resourceTypeLower == "function", strings.Contains(resourceTypeLower, "lambda"):
		return core.NodeTypeServerlessFunction, true // Generic: Lambda, Azure Functions, Cloud Functions
	case resourceTypeLower == "table", strings.Contains(resourceTypeLower, "dynamodb"):
		return core.NodeTypeDatabase, true // DynamoDB is a database
	case resourceTypeLower == "queue", resourceTypeLower == "topic",
		strings.Contains(resourceTypeLower, "sqs"), strings.Contains(resourceTypeLower, "sns"):
		return core.NodeTypeMessageQueue, true
	case resourceTypeLower == "vpc":
		return core.NodeTypeVPC, true
	case resourceTypeLower == "security_group":
		return core.NodeTypeSecurityGroup, true
	case resourceTypeLower == "natgateway":
		return core.NodeTypeNetworkGateway, true // Generic: NAT Gateway, Azure NAT, Cloud NAT
	case strings.Contains(resourceTypeLower, "route53"):
		return core.NodeTypeDNSZone, true // Generic: Route53, Azure DNS, Cloud DNS
	case strings.Contains(resourceTypeLower, "cloudfront"):
		return core.NodeTypeCDN, true // Generic: CloudFront, Azure CDN, Cloud CDN
	// secretsmanager must be matched before "ecr": the substring "ecr" appears
	// inside "s(ecr)etsmanager", so a Contains-based "ecr" case would otherwise
	// claim secretsmanager as a ContainerRegistry.
	case strings.Contains(resourceTypeLower, "secretsmanager"):
		return core.NodeTypeSecretVault, true // Generic: Secrets Manager, Key Vault, Secret Manager
	case strings.Contains(resourceTypeLower, "ecr"):
		return core.NodeTypeContainerRegistry, true // Generic: ECR, ACR, GCR
	case strings.Contains(resourceTypeLower, "eks"):
		return core.NodeTypeManagedCluster, true // Generic: EKS, AKS, GKE
	case strings.Contains(resourceTypeLower, "cloudwatch"):
		return core.NodeTypeMonitoringService, true // Generic: CloudWatch, Azure Monitor, Cloud Monitoring
	default:
		return "", false
	}
}

// extractDNSName extracts the DNS name from a cloud resource based on its type
func extractDNSName(resource *CloudResourceRow) string {
	if len(resource.Meta) == 0 {
		return ""
	}

	var metaMap map[string]interface{}
	if err := json.Unmarshal(resource.Meta, &metaMap); err != nil {
		return ""
	}

	// LoadBalancer
	if dnsName, ok := metaMap["DNSName"].(string); ok && dnsName != "" {
		return dnsName
	}

	// RDS Instance
	if endpoint, ok := metaMap["Endpoint"].(map[string]interface{}); ok {
		if address, ok := endpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache - Configuration Endpoint
	if configEndpoint, ok := metaMap["ConfigurationEndpoint"].(map[string]interface{}); ok {
		if address, ok := configEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache - ReaderEndpoint
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(string); ok && readerEndpoint != "" {
		return readerEndpoint
	}
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(map[string]interface{}); ok {
		if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache - PrimaryEndpoint
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(string); ok && primaryEndpoint != "" {
		return primaryEndpoint
	}
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(map[string]interface{}); ok {
		if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache - NodeGroups
	if nodeGroups, ok := metaMap["NodeGroups"].([]interface{}); ok && len(nodeGroups) > 0 {
		if ng, ok := nodeGroups[0].(map[string]interface{}); ok {
			if primaryEndpoint, ok := ng["PrimaryEndpoint"].(map[string]interface{}); ok {
				if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
			if readerEndpoint, ok := ng["ReaderEndpoint"].(map[string]interface{}); ok {
				if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
		}
	}

	// ElastiCache - CacheNodes
	if cacheNodes, ok := metaMap["CacheNodes"].([]interface{}); ok && len(cacheNodes) > 0 {
		if cn, ok := cacheNodes[0].(map[string]interface{}); ok {
			if endpoint, ok := cn["Endpoint"].(map[string]interface{}); ok {
				if address, ok := endpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
		}
	}

	// GCP Cloud Run — `meta.url` is the per-service URL. Strip scheme so the
	// index key matches what eBPF observes (host only).
	if url, ok := metaMap["url"].(string); ok && url != "" {
		// `url` collides with field names other services use; require the
		// value to actually look like a URL with a recognisable host.
		if host := repoURIHostLocal(url); host != "" {
			return host
		}
	}

	// GCP GKE cluster — `meta.endpoint` is a bare host or IP.
	if endpoint, ok := metaMap["endpoint"].(string); ok && endpoint != "" {
		return endpoint
	}

	// GCP Cloud SQL — `meta.connectionName` is `<project>:<region>:<instance>`,
	// not a DNS name, but the SQL Auth Proxy and several SDKs use it as the
	// connection identifier. Stamp it so any client logging traffic to that
	// string can be matched.
	if connectionName, ok := metaMap["connectionName"].(string); ok && connectionName != "" {
		return connectionName
	}

	// Synthesize a public DNS form for resources whose API metadata never
	// carries one (S3 / GCS bucket today). Same construction the in-graph
	// extractors use, so the cloud_resourses path and the in-graph path land
	// on the same string.
	if dns := synthesizeFromResource(resource); dns != "" {
		return dns
	}

	return ""
}

// repoURIHostLocal mirrors sources.repoURIHost (cross-package import would
// create a cycle). Returns the host portion of a URL string, treating
// scheme-less inputs as `https://` for url.Parse compatibility.
func repoURIHostLocal(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	if !strings.Contains(uri, "://") {
		uri = "https://" + uri
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host
}

// synthesizeFromResource derives a canonical DNS hostname from the row's
// service-name + region + account-number + resource-name, when the row's
// in-API metadata didn't expose an endpoint. Tries the AWS map first, then
// the GCP map. Returns "" if neither side recognises the type.
func synthesizeFromResource(resource *CloudResourceRow) string {
	if service := awsServiceFromResourceType(resource.Type); service != "" {
		if canonical, _ := AwsServiceDNS(service, resource.Region, resource.AccountNumber, resource.Name); canonical != "" {
			return canonical
		}
	}
	if service := gcpServiceFromResourceType(resource.Type); service != "" {
		// GCP rows don't carry a project field on CloudResourceRow today —
		// not needed for the only synthesizable case (Cloud Storage bucket).
		if canonical, _ := GcpServiceDNS(service, resource.Region, "", resource.Name); canonical != "" {
			return canonical
		}
	}
	return ""
}

// ExtractDNSNameFromResource exports the DNS name extraction logic for use by other packages
func ExtractDNSNameFromResource(resource *CloudResourceRow) string {
	return extractDNSName(resource)
}

// endpointHit records the cloud-resource node that exposes a given endpoint
// string and which property field carried the match.
type endpointHit struct {
	node         *core.DbNode
	matchedField string
}

// strProp returns a string-valued node property or "" if absent / wrong type.
func strProp(n *core.DbNode, key string) string {
	if n == nil || n.Properties == nil {
		return ""
	}
	if v, ok := n.Properties[key].(string); ok {
		return v
	}
	return ""
}

// stringSliceProp coerces a node property into []string for both the
// canonical []string shape (set in-process by aws_source) and the
// []interface{} shape (set after JSON round-trip from the DB). Returns nil
// when the property is absent or holds a non-stringy type.
func stringSliceProp(n *core.DbNode, key string) []string {
	if n == nil || n.Properties == nil {
		return nil
	}
	switch v := n.Properties[key].(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// buildCloudEndpointIndex scans the in-memory graph and indexes
// LoadBalancer / Database / Cache nodes by every endpoint they expose. The
// returned map is keyed on the lowercase + trimmed endpoint string. The
// matching field name (e.g. "dns_name", "node_group_endpoints") is recorded so
// downstream logging can attribute the match.
//
// First-write-wins on collision: LB DNS names, RDS endpoints, and ElastiCache
// primary endpoints are unique within an AWS account, so a collision is a
// signal worth surfacing rather than silently dropping. The previous SQL
// filtered `is_active = true` on the cloud_resourses row; the in-memory node
// list at this phase only contains freshly-created nodes from earlier build
// phases, so no tombstone filter is needed.
//
// reqCtx and tenantID are used only to emit the
// nb_services_kg_endpoint_collision counter on collisions. Pass nil reqCtx
// from tests that don't care about metrics.
func buildCloudEndpointIndex(reqCtx *security.RequestContext, tenantID string, nodes []*core.DbNode, logger *slog.Logger) map[string]endpointHit {
	if logger == nil {
		logger = slog.Default()
	}
	idx := make(map[string]endpointHit)

	addStr := func(n *core.DbNode, field, val string) {
		if val == "" {
			return
		}
		key := strings.ToLower(strings.TrimSpace(val))
		if key == "" {
			return
		}
		if existing, exists := idx[key]; exists {
			if existing.node.ID != n.ID {
				logger.Warn("Endpoint collision while indexing cloud resources",
					"endpoint", key,
					"existing_node", existing.node.UniqueKey,
					"existing_field", existing.matchedField,
					"new_node", n.UniqueKey,
					"new_field", field,
					"resolution", "first-write-wins")
				if reqCtx != nil {
					common.MetricsKGEndpointCollision(reqCtx.GetContext(), tenantID, string(n.NodeType))
				}
			}
			return
		}
		idx[key] = endpointHit{node: n, matchedField: field}
	}

	// indexAliases stamps every entry from the node's `dns_aliases` slice.
	// AWS source writes aliases for resources where one DNS form has multiple
	// public-resolvable variants (S3 dualstack/website forms, DDB account-scoped
	// SDK endpoint, etc.) — we want any of them to hit the same node.
	indexAliases := func(n *core.DbNode) {
		for _, a := range stringSliceProp(n, "dns_aliases") {
			addStr(n, "dns_aliases", a)
		}
	}

	for _, n := range nodes {
		if n == nil {
			continue
		}
		switch n.NodeType {
		case core.NodeTypeDatabase:
			// RDS / Redshift / DocDB / Neptune carry both forms; DDB only
			// carries the synthesized dns_name (no endpoint_address). The
			// extra addStr is a no-op when the property is empty.
			addStr(n, "dns_name", strProp(n, "dns_name"))
			addStr(n, "endpoint_address", strProp(n, "endpoint_address"))
			indexAliases(n)
		case core.NodeTypeCache:
			addStr(n, "dns_name", strProp(n, "dns_name"))
			addStr(n, "configuration_endpoint", strProp(n, "configuration_endpoint"))
			addStr(n, "primary_endpoint", strProp(n, "primary_endpoint"))
			addStr(n, "reader_endpoint", strProp(n, "reader_endpoint"))
			for _, ep := range stringSliceProp(n, "node_group_endpoints") {
				addStr(n, "node_group_endpoints", ep)
			}
			for _, ep := range stringSliceProp(n, "cache_node_endpoints") {
				addStr(n, "cache_node_endpoints", ep)
			}
			indexAliases(n)
		// Single-endpoint resource types: LB always had a real DNS;
		// Storage/MessageQueue/Topic/ContainerRegistry/ManagedCluster/CDN/
		// ServerlessFunction/APIGateway get their dns_name from
		// sources.synthesizeAWSEndpointDNS. dns_aliases covers forms with
		// multiple public variants (S3 dualstack/website, DDB SDK form).
		case core.NodeTypeLoadBalancer,
			core.NodeTypeStorage,
			core.NodeTypeMessageQueue,
			core.NodeTypeTopic,
			core.NodeTypeContainerRegistry,
			core.NodeTypeManagedCluster,
			core.NodeTypeCDN,
			core.NodeTypeServerlessFunction,
			core.NodeTypeAPIGateway:
			addStr(n, "dns_name", strProp(n, "dns_name"))
			indexAliases(n)
		}
	}
	return idx
}

// findCloudResourceByEndpoint returns the cloud-resource node (if any) that
// exposes the given endpoint, plus the property field that carried the match.
// Lookups are case-insensitive and trim-tolerant.
func findCloudResourceByEndpoint(idx map[string]endpointHit, endpoint string) (*core.DbNode, string) {
	if endpoint == "" || len(idx) == 0 {
		return nil, ""
	}
	key := strings.ToLower(strings.TrimSpace(endpoint))
	if key == "" {
		return nil, ""
	}
	if hit, ok := idx[key]; ok {
		return hit.node, hit.matchedField
	}
	return nil, ""
}

// makeRoutesThroughEdge builds a ROUTES_THROUGH edge from an external service
// node to a matched cloud-resource node, stamping match provenance on the
// edge properties. resolvedDNS is set only when the match was reached via
// Route53 resolution (the original DNS name behind the AWS endpoint).
//
// This bridge edge is the *signal* consumed by
// core.CollapseEnrichedExternalServices in BuildGraphs Phase 3.5. The collapse
// pass walks every ROUTES_THROUGH/RESOLVES_TO edge whose source is an
// ExternalService, repoints inbound CALLS edges to the matched cloud node,
// and removes both this edge and the ES node. Do not stop emitting this edge
// from a successful match path without also updating the collapse contract.
func makeRoutesThroughEdge(
	externalServiceNode, cloudNode *core.DbNode,
	matchedBy, matchValue, resolvedDNS string,
	cloudAccountID, tenantID string,
) *core.DbEdge {
	props := map[string]interface{}{
		"discovered_from":        "graph_endpoint_index",
		"matched_by":             matchedBy,
		"match_value":            matchValue,
		"created_by_flow_source": cloudEnrichmentSourceName,
		"flow_source_category":   cloudEnrichmentCategory,
		"source_priority":        int(GetEdgeSourcePriority(cloudEnrichmentSourceName, core.RelationshipRoutesThrough)),
	}
	if resolvedDNS != "" {
		props["resolved_dns_name"] = resolvedDNS
	}
	now := time.Now()
	return &core.DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      externalServiceNode.ID,
		DestinationNodeID: cloudNode.ID,
		RelationshipType:  core.RelationshipRoutesThrough,
		Properties:        props,
		CloudAccountID:    cloudAccountID,
		TenantID:          tenantID,
		Level:             "Tenant",
		Source:            "cloud",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// countExternalServices counts the number of external service nodes
func countExternalServices(nodes []*core.DbNode) int {
	count := 0
	for _, node := range nodes {
		if node.NodeType == core.NodeTypeExternalService {
			count++
		}
	}
	return count
}

// LinkLoadBalancersToBackendServices links load balancer nodes to backend service nodes
// This is called after enrichment to create final connections
func LinkLoadBalancersToBackendServices(
	nodes []*core.DbNode,
	edges []*core.DbEdge,
	cloudAccountID, tenantID string,
) ([]*core.DbNode, []*core.DbEdge) {
	slog.Info("Starting LoadBalancer to backend service linking")

	newEdges := make([]*core.DbEdge, 0)

	// Build indexes for faster lookup
	loadBalancersByDNS := make(map[string]*core.DbNode)
	externalServicesByName := make(map[string]*core.DbNode)
	servicesByHTTPHost := make(map[string][]*core.DbNode)

	// Index all nodes
	for _, node := range nodes {
		// Index LoadBalancers by their DNS name
		if node.NodeType == core.NodeTypeLoadBalancer {
			if dnsName, ok := node.Properties["dns_name"].(string); ok && dnsName != "" {
				loadBalancersByDNS[dnsName] = node
			}
		}

		// Index External Services by name
		if node.NodeType == core.NodeTypeExternalService {
			if name, ok := node.Properties["name"].(string); ok && name != "" {
				externalServicesByName[name] = node
			}
		}

		// Index Services by http.host label
		if node.NodeType == core.NodeTypeService {
			if httpHost, ok := node.Labels["http.host"]; ok && httpHost != "" {
				servicesByHTTPHost[httpHost] = append(servicesByHTTPHost[httpHost], node)
			}
		}
	}

	slog.Info("Indexed nodes for linking",
		"loadbalancers", len(loadBalancersByDNS),
		"external_services", len(externalServicesByName),
		"http_host_mappings", len(servicesByHTTPHost))

	// Link External Services to LoadBalancers and LoadBalancers to backend services
	for externalServiceName, externalServiceNode := range externalServicesByName {
		// Check if this external service has dns.resolved_to or dns.cname
		var resolvedTo string
		if dnsResolvedTo, ok := externalServiceNode.Labels["dns.resolved_to"]; ok {
			resolvedTo = dnsResolvedTo
		} else if dnsCNAME, ok := externalServiceNode.Labels["dns.cname"]; ok {
			resolvedTo = dnsCNAME
		}

		if resolvedTo != "" {
			// Check if we have a LoadBalancer with this DNS name
			if lbNode, exists := loadBalancersByDNS[resolvedTo]; exists {
				// Create RESOLVES_TO edge: ExternalService → LoadBalancer
				edge := &core.DbEdge{
					ID:                uuid.New().String(),
					SourceNodeID:      externalServiceNode.ID,
					DestinationNodeID: lbNode.ID,
					RelationshipType:  core.RelationshipResolvesTo,
					Properties: map[string]interface{}{
						"dns_name":               resolvedTo,
						"discovered_from":        "dns_resolution",
						"created_by_flow_source": cloudEnrichmentSourceName,
						"flow_source_category":   cloudEnrichmentCategory,
						"source_priority":        int(GetEdgeSourcePriority(cloudEnrichmentSourceName, core.RelationshipResolvesTo)),
					},
					CloudAccountID: cloudAccountID,
					TenantID:       tenantID,
					Level:          "Tenant",
					Source:         "cloud",
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}
				newEdges = append(newEdges, edge)

				slog.Info("Linked external service to LoadBalancer via DNS",
					"external_service", externalServiceName,
					"loadbalancer", lbNode.Properties["name"],
					"dns_name", resolvedTo)

				// Link LoadBalancer to backend services via http.host
				if backendServices, exists := servicesByHTTPHost[externalServiceName]; exists {
					for _, backendService := range backendServices {
						// Create ROUTES_TO edge: LoadBalancer → Backend Service
						routeEdge := &core.DbEdge{
							ID:                uuid.New().String(),
							SourceNodeID:      lbNode.ID,
							DestinationNodeID: backendService.ID,
							RelationshipType:  core.RelationshipRoutesTo,
							Properties: map[string]interface{}{
								"http_host":              externalServiceName,
								"discovered_from":        "http_host_matching",
								"created_by_flow_source": cloudEnrichmentSourceName,
								"flow_source_category":   cloudEnrichmentCategory,
								"source_priority":        int(GetEdgeSourcePriority(cloudEnrichmentSourceName, core.RelationshipRoutesTo)),
							},
							CloudAccountID: cloudAccountID,
							TenantID:       tenantID,
							Level:          "Tenant",
							Source:         "cloud",
							CreatedAt:      time.Now(),
							UpdatedAt:      time.Now(),
						}
						newEdges = append(newEdges, routeEdge)

						slog.Info("Linked LoadBalancer to backend service",
							"loadbalancer", lbNode.Properties["name"],
							"backend_service", backendService.Properties["name"],
							"http_host", externalServiceName)
					}
				}
			}
		}
	}

	// Combine edges
	allEdges := append(edges, newEdges...)

	slog.Info("LoadBalancer linking completed",
		"new_edges_created", len(newEdges),
		"total_edges", len(allEdges))

	return nodes, allEdges
}
