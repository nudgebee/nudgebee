package flow_sources

import (
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/traces"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ConvertServiceMapToGraph converts a traces.ServiceMap to knowledge graph nodes and edges
// This directly converts traces.ServiceMap → core.DbNode/DbEdge without intermediate types
// Based on traces.ConvertServiceMapToKnowledgeGraph but creates core types directly
// Now supports matching against existing nodes before creating new ones
func ConvertServiceMapToGraph(
	serviceMap *traces.ServiceMap,
	cloudAccountID string,
	tenantID string,
	baseFlowSource *BaseFlowSource,
	logger *slog.Logger,
) ([]*core.DbNode, []*core.DbEdge, error) {

	if serviceMap == nil {
		return []*core.DbNode{}, []*core.DbEdge{}, nil
	}

	edges := make([]*core.DbEdge, 0)
	now := time.Now()

	// Track created nodes by their unique key to avoid duplicates
	nodeMap := make(map[string]*core.DbNode)

	// Track external services and unmatched services
	externalServices := make(map[string]bool)
	unmatchedServices := make(map[string]string) // name -> kind

	// Get node matcher if available (will be nil if not initialized)
	var nodeMatcher *NodeMatcher
	if baseFlowSource != nil {
		nodeMatcher = baseFlowSource.GetNodeMatcher()
	}

	// Build a lookup map for applications by name to find their actual properties
	appLookup := make(map[string]*traces.ServiceApplication)
	for i := range serviceMap.Applications {
		app := &serviceMap.Applications[i]
		appLookup[fmt.Sprintf("%s:%s", app.Id.Kind, app.Id.Name)] = app
	}

	// Helper function to create or get a node
	createOrGetNode := func(name, kind, namespace, environment string, appType []string, labels map[string]string, isHealthy bool, healthReason string) *core.DbNode {
		// First, check if this service exists in applications list and use its actual properties
		var nodeStats *traces.NodeStats
		lookupKey := fmt.Sprintf("%s:%s", kind, name)
		existsInAppList := false
		if actualApp, exists := appLookup[lookupKey]; exists {
			existsInAppList = true
			// Use the actual application's properties
			name = actualApp.Id.Name
			kind = actualApp.Id.Kind
			namespace = actualApp.Id.Namespace
			if env, ok := actualApp.Labels["environment"]; ok {
				environment = env
			}
			appType = actualApp.Type
			labels = actualApp.Labels
			isHealthy = actualApp.IsHealthy
			healthReason = actualApp.HealthReason
			nodeStats = actualApp.NodeStats
		}

		// Determine node type
		nodeType := core.NodeTypeService
		if kind == "ExternalService" {
			nodeType = core.NodeTypeExternalService
		}

		// Check application type for more specific classification
		// BUT: Don't override ExternalService - they should stay as ExternalService
		// regardless of their underlying technology (redis, postgres, etc.)
		if kind != "ExternalService" {
			for _, t := range appType {
				switch strings.ToLower(t) {
				case "database", "postgres", "postgresql", "mysql", "mongodb", "elasticsearch":
					nodeType = core.NodeTypeDatabase
				case "cache", "redis":
					nodeType = core.NodeTypeCache
				case "messaging", "kafka", "rabbitmq", "sqs", "amqp":
					nodeType = core.NodeTypeMessageQueue
				}
			}
		}

		uniqueKey := fmt.Sprintf("%s:%s:%s", nodeType, name, environment)

		// Return existing node if already created in this conversion
		if existing, ok := nodeMap[uniqueKey]; ok {
			return existing
		}

		// Try to match against existing nodes from the knowledge graph
		if nodeMatcher != nil {
			matchedNode, err := matchServiceToNode(nodeMatcher, name, kind, namespace, labels, cloudAccountID, logger)
			if err == nil && matchedNode != nil {
				// Found a matching node - enrich it with new metadata and return it
				enrichNodeWithMetadata(matchedNode, appType, labels, isHealthy, healthReason)

				// Add node stats if available
				if nodeStats != nil {
					if _, hasReqRate := matchedNode.Properties["request_count_per_second"]; !hasReqRate {
						matchedNode.Properties["request_count_per_second"] = nodeStats.RequestsPerSecond
					}
					if _, hasFailCount := matchedNode.Properties["failure_count"]; !hasFailCount {
						matchedNode.Properties["failure_count"] = nodeStats.FailureCount
					}
					if _, hasLatency := matchedNode.Properties["latency"]; !hasLatency {
						matchedNode.Properties["latency"] = nodeStats.Latency
					}
				}

				// Cache the matched node in nodeMap to avoid repeated matching
				nodeMap[uniqueKey] = matchedNode

				if logger != nil {
					logger.Debug("reusing existing node from knowledge graph",
						"name", name,
						"kind", kind,
						"matched_node", matchedNode.UniqueKey)
				}

				return matchedNode
			} else {
				// No match found in existing nodes
				// If this service is NOT in the applications list (only appears in upstream/downstream),
				// mark it as an external service
				if !existsInAppList && kind != "ExternalService" {
					if logger != nil {
						logger.Debug("service not found in applications list and not matched to existing node, marking as external",
							"name", name,
							"original_kind", kind,
							"namespace", namespace)
					}
					// Track as unmatched and mark as external
					unmatchedServices[name] = kind
					externalServices[name] = true

					kind = "ExternalService"
					nodeType = core.NodeTypeExternalService
					// Recalculate unique key with new node type
					uniqueKey = fmt.Sprintf("%s:%s:%s", nodeType, name, environment)

					// Check again if this external service node was already created
					if existing, ok := nodeMap[uniqueKey]; ok {
						return existing
					}
				}
			}
		}

		// Track external services (already marked as ExternalService)
		if kind == "ExternalService" {
			externalServices[name] = true
		}

		// Create new node
		properties := make(map[string]interface{})
		// Set core properties first (these should never be overwritten)
		properties["name"] = name
		properties["environment"] = environment
		properties["namespace"] = namespace
		properties["kind"] = kind
		properties["is_healthy"] = isHealthy
		properties["health_reason"] = healthReason
		if nodeStats != nil {
			properties["request_count_per_second"] = nodeStats.RequestsPerSecond
			properties["failure_count"] = nodeStats.FailureCount
			properties["latency"] = nodeStats.Latency
		}

		// Store labels in separate sub-object to avoid conflicts with core properties
		if len(labels) > 0 {
			properties["labels"] = labels
		}

		if len(appType) > 0 {
			properties["types"] = appType
		}

		// Extract commonly-used fields from labels to top-level for convenience
		if labels != nil {
			if sdk, ok := labels["telemetry.sdk.language"]; ok {
				properties["programming_language"] = sdk
			}
			if runtime, ok := labels["process.runtime.version"]; ok {
				properties["runtime_version"] = runtime
			}
			if cluster, ok := labels["k8s_cluster"]; ok {
				properties["cluster"] = cluster
			}
		}
		nodeID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(uniqueKey)).String() // UUIDv5 style
		properties["node_id"] = nodeID

		node := &core.DbNode{
			ID:              nodeID,
			NodeType:        nodeType,
			UniqueKey:       uniqueKey,
			Properties:      properties,
			Labels:          extractLabelsForDbNode(labels),
			QueryAttributes: map[string]interface{}{},
			CloudAccountID:  cloudAccountID,
			TenantID:        tenantID,
			Level:           "Account",
			Source:          "traces",
			CreatedAt:       now,
			UpdatedAt:       now,
		}

		nodeMap[uniqueKey] = node
		return node
	}

	// First pass: Create nodes for all applications
	for _, app := range serviceMap.Applications {
		environment := ""
		if env, ok := app.Labels["environment"]; ok {
			environment = env
		}

		// Create node using helper (will be added to nodeMap)
		createOrGetNode(app.Id.Name, app.Id.Kind, app.Id.Namespace, environment, app.Type, app.Labels, app.IsHealthy, app.HealthReason)
	}

	// Second pass: Create edges and ensure all referenced nodes exist
	for _, app := range serviceMap.Applications {
		environment := ""
		if env, ok := app.Labels["environment"]; ok {
			environment = env
		}

		// Get the source node
		sourceNode := createOrGetNode(app.Id.Name, app.Id.Kind, app.Id.Namespace, environment, app.Type, app.Labels, app.IsHealthy, app.HealthReason)

		// Convert upstream links to edges (this service depends on upstream)
		for _, upstream := range app.Upstreams {
			// Parse upstream ID to get target service name and kind
			// Format is ":Kind:Name"
			targetName, targetKind := traces.ParseUpstreamId(upstream.Id)
			if targetName == "" {
				continue
			}

			// Infer upstream type from the protocol and upstream properties
			upstreamType := []string{}
			if upstream.Protocol != "" {
				protocol := strings.ToLower(upstream.Protocol)
				switch protocol {
				case "redis":
					upstreamType = []string{"redis", "cache"}
				case "postgresql", "postgres", "mysql", "mongodb":
					upstreamType = []string{protocol, "database"}
				case "elasticsearch":
					upstreamType = []string{"elasticsearch", "database", "search"}
				case "kafka", "rabbitmq", "sqs", "amqp":
					upstreamType = []string{protocol, "messaging"}
				default:
					upstreamType = []string{protocol}
				}
			}

			// Create or get the upstream node
			upstreamNode := createOrGetNode(targetName, targetKind, "", environment, upstreamType, nil, true, "")

			edgeID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("%v:%v:%v", sourceNode.UniqueKey, upstreamNode.UniqueKey, core.RelationshipCalls))).String() // UUIDv5 style
			// Create edge: this service -> upstream service
			edge := &core.DbEdge{
				ID:                edgeID,
				SourceNodeID:      sourceNode.ID,
				DestinationNodeID: upstreamNode.ID,
				RelationshipType:  core.RelationshipCalls,
				Properties: map[string]interface{}{
					"protocol":        upstream.Protocol,
					"latency_ms":      upstream.Latency,
					"request_count":   upstream.RequestCount,
					"failure_count":   upstream.FailureCount,
					"bytes_sent":      upstream.BytesSent,
					"bytes_received":  upstream.BytesReceived,
					"status":          upstream.Status,
					"first_seen":      now,
					"last_seen":       now,
					"connection_type": "service",
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				Level:          "Account",
				Source:         "traces",
				CreatedAt:      now,
				UpdatedAt:      now,
			}

			edges = append(edges, edge)
		}

		// Convert downstream links to edges (downstream services depend on this service)
		for _, downstream := range app.Downstreams {
			downstreamName := downstream.Id.Name
			downstreamKind := downstream.Id.Kind
			downstreamNamespace := downstream.Id.Namespace

			// Use downstream's actual environment/namespace if different
			downstreamEnv := environment
			if downstreamNamespace != "" && downstreamNamespace != app.Id.Namespace {
				downstreamEnv = downstreamNamespace
			}

			// Infer downstream type from protocol
			downstreamType := []string{}
			if downstream.Protocol != "" {
				protocol := strings.ToLower(downstream.Protocol)
				switch protocol {
				case "redis":
					downstreamType = []string{"redis", "cache"}
				case "postgresql", "postgres", "mysql", "mongodb":
					downstreamType = []string{protocol, "database"}
				case "elasticsearch":
					downstreamType = []string{"elasticsearch", "database", "search"}
				case "kafka", "rabbitmq", "sqs", "amqp":
					downstreamType = []string{protocol, "messaging"}
				default:
					downstreamType = []string{protocol}
				}
			}

			// Create or get the downstream node
			downstreamNode := createOrGetNode(downstreamName, downstreamKind, downstreamNamespace, downstreamEnv, downstreamType, nil, true, "")

			// Create edge: downstream service -> this service
			edge := &core.DbEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      downstreamNode.ID,
				DestinationNodeID: sourceNode.ID,
				RelationshipType:  core.RelationshipCalls,
				Properties: map[string]interface{}{
					"protocol":        downstream.Protocol,
					"latency_ms":      downstream.Latency,
					"request_count":   downstream.RequestCount,
					"failure_count":   downstream.FailureCount,
					"bytes_sent":      downstream.BytesSent,
					"bytes_received":  downstream.BytesReceived,
					"status":          downstream.Status,
					"first_seen":      now,
					"last_seen":       now,
					"connection_type": "service",
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				Level:          "Account",
				Source:         "traces",
				CreatedAt:      now,
				UpdatedAt:      now,
			}

			edges = append(edges, edge)
		}
	}

	// Convert nodeMap to slice
	nodes := make([]*core.DbNode, 0, len(nodeMap))
	for _, node := range nodeMap {
		nodes = append(nodes, node)
	}

	// Add K8s infrastructure nodes and edges if available
	if serviceMap.K8sMetadata != nil {
		k8sNodes, k8sEdges := convertK8sMetadataToGraph(serviceMap.K8sMetadata, cloudAccountID, tenantID)
		nodes = append(nodes, k8sNodes...)
		edges = append(edges, k8sEdges...)

		if logger != nil {
			logger.Info("added K8s infrastructure to knowledge graph",
				"k8s_nodes", len(k8sNodes),
				"k8s_edges", len(k8sEdges))
		}
	}

	// Log unmatched services and external services summary
	if len(unmatchedServices) > 0 || len(externalServices) > 0 {
		if logger != nil {
			logger.Info("external services detection summary",
				"external_services_count", len(externalServices),
				"unmatched_services_count", len(unmatchedServices))

			// Log details of unmatched services
			if len(unmatchedServices) > 0 {
				logger.Debug("unmatched services marked as external",
					"services", unmatchedServices)
			}
		}
	}

	if logger != nil {
		logger.Info("converted service map to knowledge graph",
			"applications_count", len(serviceMap.Applications),
			"nodes_created", len(nodes),
			"edges_created", len(edges),
			"external_services", len(externalServices))
	}

	return nodes, edges, nil
}

// extractLabelsForDbNode extracts labels from map for DbNode
func extractLabelsForDbNode(labels map[string]string) map[string]string {
	if labels == nil {
		return make(map[string]string)
	}
	return labels
}

// convertK8sMetadataToGraph converts K8s metadata directly to core.DbNode and core.DbEdge
// This creates knowledge graph nodes and edges for K8s infrastructure without intermediate types
func convertK8sMetadataToGraph(
	metadata *traces.K8sInfrastructureMetadata,
	cloudAccountID string,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge) {

	nodes := make([]*core.DbNode, 0)
	edges := make([]*core.DbEdge, 0)
	timestamp := time.Now()

	// Create cluster nodes
	for _, cluster := range metadata.Clusters {
		node := &core.DbNode{
			ID:        uuid.New().String(),
			NodeType:  core.NodeTypeCluster,
			UniqueKey: fmt.Sprintf("Cluster:%s:%s", cluster.Name, cluster.Environment),
			Properties: map[string]interface{}{
				"name":        cluster.Name,
				"environment": cluster.Environment,
			},
			Labels:          make(map[string]string),
			QueryAttributes: make(map[string]interface{}),
			CloudAccountID:  cloudAccountID,
			TenantID:        tenantID,
			Level:           "Account",
			Source:          "traces",
			CreatedAt:       timestamp,
			UpdatedAt:       timestamp,
		}
		nodes = append(nodes, node)
	}

	// Create namespace nodes and edges to clusters
	for _, ns := range metadata.Namespaces {
		node := &core.DbNode{
			ID:        uuid.New().String(),
			NodeType:  core.NodeTypeNamespace,
			UniqueKey: fmt.Sprintf("Namespace:%s:%s:%s", ns.Cluster, ns.Name, ns.Environment),
			Properties: map[string]interface{}{
				"name":        ns.Name,
				"cluster":     ns.Cluster,
				"environment": ns.Environment,
			},
			Labels:          make(map[string]string),
			QueryAttributes: make(map[string]interface{}),
			CloudAccountID:  cloudAccountID,
			TenantID:        tenantID,
			Level:           "Account",
			Source:          "traces",
			CreatedAt:       timestamp,
			UpdatedAt:       timestamp,
		}
		nodes = append(nodes, node)

		// Create edge: namespace -> cluster
		if ns.Cluster != "" {
			edge := &core.DbEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      node.ID,
				DestinationNodeID: fmt.Sprintf("Cluster:%s:%s", ns.Cluster, ns.Environment),
				RelationshipType:  core.RelationshipBelongsTo,
				Properties: map[string]interface{}{
					"relationship": "namespace_in_cluster",
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				Level:          "Account",
				Source:         "traces",
				CreatedAt:      timestamp,
				UpdatedAt:      timestamp,
			}
			edges = append(edges, edge)
		}
	}

	// Create worker node nodes and edges to clusters
	for _, n := range metadata.Nodes {
		node := &core.DbNode{
			ID:        uuid.New().String(),
			NodeType:  core.NodeTypeNode,
			UniqueKey: fmt.Sprintf("Node:%s:%s:%s", n.Cluster, n.Name, n.Environment),
			Properties: map[string]interface{}{
				"name":        n.Name,
				"cluster":     n.Cluster,
				"environment": n.Environment,
			},
			Labels:          make(map[string]string),
			QueryAttributes: make(map[string]interface{}),
			CloudAccountID:  cloudAccountID,
			TenantID:        tenantID,
			Level:           "Account",
			Source:          "traces",
			CreatedAt:       timestamp,
			UpdatedAt:       timestamp,
		}
		nodes = append(nodes, node)

		// Create edge: node -> cluster
		if n.Cluster != "" {
			edge := &core.DbEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      node.ID,
				DestinationNodeID: fmt.Sprintf("Cluster:%s:%s", n.Cluster, n.Environment),
				RelationshipType:  core.RelationshipBelongsTo,
				Properties: map[string]interface{}{
					"relationship": "node_in_cluster",
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				Level:          "Account",
				Source:         "traces",
				CreatedAt:      timestamp,
				UpdatedAt:      timestamp,
			}
			edges = append(edges, edge)
		}
	}

	return nodes, edges
}

// matchServiceToNode matches a service to an existing knowledge graph node
// This follows the same pattern as matchApplicationToNode in ebpf_flow_source.go
func matchServiceToNode(
	nodeMatcher *NodeMatcher,
	name, kind, namespace string,
	labels map[string]string,
	cloudAccountID string,
	logger *slog.Logger,
) (*core.DbNode, error) {
	if nodeMatcher == nil {
		return nil, fmt.Errorf("node matcher not initialized")
	}

	// Determine node type for matching
	nodeType := inferNodeTypeFromKind(kind)

	// Strategy 1: Match by namespace and name (for K8s services) (same account)
	if namespace != "" && name != "" {
		result, err := nodeMatcher.FindNode(MatchCriteria{
			AccountID: cloudAccountID,
			NodeType:  nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "namespace",
					Value:         namespace,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         name,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			if logger != nil {
				logger.Debug("matched service by namespace and name (strategy 1)",
					"namespace", namespace,
					"name", name,
					"node", result.Node.UniqueKey,
					"confidence", result.Confidence)
			}
			return result.Node, nil
		}
	}

	// Strategy 2: Exact match by name and kind (same account)
	if name != "" {
		result, err := nodeMatcher.FindNode(MatchCriteria{
			AccountID: cloudAccountID,
			NodeType:  nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         name,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			if logger != nil {
				logger.Debug("matched service by name (strategy 2)",
					"name", name,
					"node", result.Node.UniqueKey,
					"confidence", result.Confidence)
			}
			return result.Node, nil
		}
	}

	// Strategy 3: Match by namespace and name (any account)
	if namespace != "" && name != "" {
		result, err := nodeMatcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "namespace",
					Value:         namespace,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         name,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			if logger != nil {
				logger.Debug("matched service by namespace and name (strategy 3)",
					"namespace", namespace,
					"name", name,
					"node", result.Node.UniqueKey,
					"confidence", result.Confidence)
			}
			return result.Node, nil
		}
	}

	// Strategy 4: Exact match by name (any account)
	if name != "" {
		result, err := nodeMatcher.FindNode(MatchCriteria{
			NodeType: nodeType,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         name,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			if logger != nil {
				logger.Debug("matched service by name (strategy 4)",
					"name", name,
					"node", result.Node.UniqueKey,
					"confidence", result.Confidence)
			}
			return result.Node, nil
		}
	}

	// Strategy 5: Match by service name in labels
	if labels != nil {
		if serviceName, ok := labels["service"]; ok && serviceName != "" {
			result, err := nodeMatcher.FindNode(MatchCriteria{
				PropertyMatches: []PropertyMatch{
					{
						PropertyPath:  "service_name",
						Value:         serviceName,
						MatchType:     core.MatchTypeExact,
						CaseSensitive: false,
					},
				},
			})
			if err == nil && result.Matched {
				if logger != nil {
					logger.Debug("matched service by service label (strategy 5)",
						"service", serviceName,
						"node", result.Node.UniqueKey,
						"confidence", result.Confidence)
				}
				return result.Node, nil
			}
		}
	}

	return nil, fmt.Errorf("no matching node found for service: %s (kind: %s)", name, kind)
}

// inferNodeTypeFromKind infers the node type from the service kind
func inferNodeTypeFromKind(kind string) core.NodeType {
	kindMap := map[string]core.NodeType{
		"Service":         core.NodeTypeService,
		"Deployment":      core.NodeTypeWorkload,
		"StatefulSet":     core.NodeTypeWorkload,
		"DaemonSet":       core.NodeTypeWorkload,
		"Pod":             core.NodeTypeWorkload,
		"Runner":          core.NodeTypeWorkload,
		"Database":        core.NodeTypeDatabase,
		"ExternalService": core.NodeTypeExternalService,
	}

	if nodeType, ok := kindMap[kind]; ok {
		return nodeType
	}

	// Default to Service for unknown kinds
	return core.NodeTypeService
}

// enrichNodeWithMetadata adds metadata to a matched node
func enrichNodeWithMetadata(node *core.DbNode, appType []string, labels map[string]string, isHealthy bool, healthReason string) {
	if node == nil {
		return
	}

	// Add types if not present
	if _, hasTypes := node.Properties["types"]; !hasTypes && len(appType) > 0 {
		node.Properties["types"] = appType
	}

	// Add health information if not present
	if _, hasHealth := node.Properties["is_healthy"]; !hasHealth {
		node.Properties["is_healthy"] = isHealthy
	}

	if healthReason != "" {
		if _, hasReason := node.Properties["health_reason"]; !hasReason {
			node.Properties["health_reason"] = healthReason
		}
	}

	// Enrich with label information if not already present
	if labels != nil {
		if sdk, ok := labels["telemetry.sdk.language"]; ok {
			if _, hasLang := node.Properties["programming_language"]; !hasLang {
				node.Properties["programming_language"] = sdk
			}
		}
		if runtime, ok := labels["process.runtime.version"]; ok {
			if _, hasRuntime := node.Properties["runtime_version"]; !hasRuntime {
				node.Properties["runtime_version"] = runtime
			}
		}
		if cluster, ok := labels["k8s_cluster"]; ok {
			if _, hasCluster := node.Properties["cluster"]; !hasCluster {
				node.Properties["cluster"] = cluster
			}
		}
	}
}
