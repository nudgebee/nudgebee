package core

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// GenerateNodeID generates a unique ID for a node based on its unique key
func GenerateNodeID(uniqueKey string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(uniqueKey)).String()
}

// GenerateEdgeID generates a unique ID for an edge based on source, destination, and relationship
func GenerateEdgeID(sourceNodeID, destinationNodeID string, relationshipType RelationshipType) string {
	key := fmt.Sprintf("%s->%s:%s", sourceNodeID, destinationNodeID, relationshipType)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

// NewNode creates a new node with generated ID and timestamps.
//
// uniqueKey must be a fully-built 6-part key whose position-0 is the
// cloud_provider (aws/k8s/gcp/azure/external) — NOT the observer. The
// observer is recorded separately as `source` on the node and in
// properties["source"]; it is not part of the key.
func NewNode(nodeType NodeType, uniqueKey string, properties map[string]interface{}, tenantID, cloudAccountID, source string) *DbNode {
	now := time.Now()
	properties["source"] = source

	// Extract labels from properties to populate the dedicated Labels field
	labels := make(map[string]string)
	if labelsInterface, ok := properties["labels"]; ok {
		if labelsMap, ok := labelsInterface.(map[string]interface{}); ok {
			for k, v := range labelsMap {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// Extract queryable properties based on node type to populate QueryAttributes field
	queryAttributes := ExtractQueryAttributes(nodeType, properties)

	return &DbNode{
		ID:              GenerateNodeID(uniqueKey + tenantID + cloudAccountID),
		NodeType:        nodeType,
		UniqueKey:       uniqueKey,
		Properties:      properties,
		Labels:          labels,
		QueryAttributes: queryAttributes,
		TenantID:        tenantID,
		CloudAccountID:  cloudAccountID,
		Source:          source,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// NewEdge creates a new edge with generated ID and timestamps
func NewEdge(sourceNodeID, destinationNodeID string, relationshipType RelationshipType,
	properties map[string]interface{}, tenantID, cloudAccountID, source string) *DbEdge {
	now := time.Now()
	return &DbEdge{
		ID:                GenerateEdgeID(sourceNodeID, destinationNodeID, relationshipType),
		SourceNodeID:      sourceNodeID,
		DestinationNodeID: destinationNodeID,
		RelationshipType:  relationshipType,
		Properties:        properties,
		TenantID:          tenantID,
		CloudAccountID:    cloudAccountID,
		Source:            source,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// MergeProperties merges two property maps, with new properties overwriting existing ones
func MergeProperties(existing, new map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})

	// Copy existing properties
	for k, v := range existing {
		merged[k] = v
	}

	// Overwrite with new properties
	for k, v := range new {
		merged[k] = v
	}

	return merged
}

// DeduplicateNodes removes duplicate nodes based on unique key
func DeduplicateNodes(nodes []*DbNode) []*DbNode {
	seen := make(map[string]*DbNode, len(nodes))
	result := make([]*DbNode, 0, len(nodes))

	for _, node := range nodes {
		if existing, exists := seen[node.UniqueKey]; exists {
			// Merge properties from duplicate node
			existing.Properties = MergeProperties(existing.Properties, node.Properties)
			// Update timestamp to the latest
			if node.UpdatedAt.After(existing.UpdatedAt) {
				existing.UpdatedAt = node.UpdatedAt
			}
		} else {
			seen[node.UniqueKey] = node
			result = append(result, node)
		}
	}

	return result
}

// DeduplicateNodesWithIDMapping removes duplicate nodes based on unique key and returns
// a mapping from all node IDs (including duplicates) to the surviving node ID.
// This is useful when edges reference node IDs that may be deduplicated away.
func DeduplicateNodesWithIDMapping(nodes []*DbNode) ([]*DbNode, map[string]string) {
	seen := make(map[string]*DbNode, len(nodes))     // UniqueKey -> surviving node
	idMapping := make(map[string]string, len(nodes)) // any node ID -> surviving node ID
	result := make([]*DbNode, 0, len(nodes))

	for _, node := range nodes {
		if existing, exists := seen[node.UniqueKey]; exists {
			// Map duplicate node's ID to the surviving node's ID
			idMapping[node.ID] = existing.ID
			// Merge properties from duplicate node
			existing.Properties = MergeProperties(existing.Properties, node.Properties)
			// Update timestamp to the latest
			if node.UpdatedAt.After(existing.UpdatedAt) {
				existing.UpdatedAt = node.UpdatedAt
			}
		} else {
			seen[node.UniqueKey] = node
			// idMapping[node.ID] = node.ID // Self-mapping for surviving nodes
			result = append(result, node)
		}
	}

	return result, idMapping
}

// DeduplicateEdges removes duplicate edges based on edge ID
func DeduplicateEdges(edges []*DbEdge) []*DbEdge {
	seen := make(map[string]*DbEdge)
	result := make([]*DbEdge, 0)

	for _, edge := range edges {
		if existing, exists := seen[edge.ID]; exists {
			// Merge properties from duplicate edge
			existing.Properties = MergeProperties(existing.Properties, edge.Properties)
			// Update timestamp to the latest
			if edge.UpdatedAt.After(existing.UpdatedAt) {
				existing.UpdatedAt = edge.UpdatedAt
			}
		} else {
			seen[edge.ID] = edge
			result = append(result, edge)
		}
	}

	return result
}

// EdgeSourcePriority defines the priority level for edge sources.
// Lower number = higher priority (wins in conflicts when same edge is created by multiple sources).
type EdgeSourcePriority int

const (
	EdgePriority1 EdgeSourcePriority = 1 // Highest priority (k8s)
	EdgePriority2 EdgeSourcePriority = 2 // aws
	EdgePriority3 EdgeSourcePriority = 3 // ebpf
	EdgePriority4 EdgeSourcePriority = 4 // traces
	EdgePriority5 EdgeSourcePriority = 5 // datadog-apm
	EdgePriority6 EdgeSourcePriority = 6 // newrelic-apm
	EdgePriority7 EdgeSourcePriority = 7 // Lowest priority (unknown sources)
)

// Aliases for readability
const (
	EdgePriorityHighest = EdgePriority1
	EdgePriorityLowest  = EdgePriority7
)

// edgeTypePriorities defines source priority for each edge type.
// When multiple flow sources create the same edge (same source node, dest node, and edge type),
// the source with the highest priority (lowest number) becomes the primary source.
//
// Priority order: k8s > aws > ebpf > traces > datadog-apm > newrelic-apm
// Rationale: Infrastructure sources (k8s, aws) have most authoritative data about
// their own resources, followed by observability sources (ebpf, traces, datadog, newrelic).
// Datadog APM ranks above newrelic-apm because Datadog's service map carries
// instrumentation-derived edges directly, whereas newrelic-apm is reconstructed
// via NRQL Span aggregation. Distinct (not tied) priority is required: tied
// priority makes provenance non-deterministic under the strict-< dedup at L296.
var edgeTypePriorities = map[RelationshipType]map[string]EdgeSourcePriority{
	RelationshipCalls: {
		"k8s":          EdgePriority1, // K8s has authoritative service-to-service data
		"aws":          EdgePriority2, // AWS has authoritative cloud resource data
		"ebpf":         EdgePriority3, // eBPF has accurate network-level data
		"traces":       EdgePriority4, // Traces has rich application-level data
		"datadog-apm":  EdgePriority5, // External APM source (instrumentation-derived)
		"newrelic-apm": EdgePriority6, // External APM source (NRQL Span aggregation)
	},
	RelationshipResolvesTo: {
		"k8s":              EdgePriority1, // K8s DNS resolution
		"aws":              EdgePriority2, // AWS Route53/DNS
		"dns_resolver":     EdgePriority3, // DNS resolution
		"cloud_enrichment": EdgePriority4, // Cloud API-based resolution
		"ip_mapper":        EdgePriority5, // IP-based resolution
	},
	RelationshipRoutesTo: {
		"k8s":              EdgePriority1, // K8s ingress/service routing
		"aws":              EdgePriority2, // AWS ALB/NLB routing
		"cloud_enrichment": EdgePriority3, // Cloud API routing data
		"dns_resolver":     EdgePriority4, // DNS-based discovery
	},
	RelationshipRoutesToBackend: {
		"k8s":              EdgePriority1,
		"aws":              EdgePriority2,
		"cloud_enrichment": EdgePriority3,
		"dns_resolver":     EdgePriority4,
	},
	RelationshipRoutesToService: {
		"k8s":              EdgePriority1,
		"aws":              EdgePriority2,
		"cloud_enrichment": EdgePriority3,
		"dns_resolver":     EdgePriority4,
	},
	RelationshipRoutesThrough: {
		"k8s":              EdgePriority1,
		"aws":              EdgePriority2,
		"cloud_enrichment": EdgePriority3,
	},
	RelationshipPublishesTo: {
		"k8s":          EdgePriority1,
		"aws":          EdgePriority2, // AWS SNS/SQS/Kinesis
		"ebpf":         EdgePriority3,
		"traces":       EdgePriority4,
		"datadog-apm":  EdgePriority5,
		"newrelic-apm": EdgePriority6,
	},
	RelationshipSubscribesTo: {
		"k8s":          EdgePriority1,
		"aws":          EdgePriority2, // AWS SQS/Kinesis consumers
		"ebpf":         EdgePriority3,
		"traces":       EdgePriority4,
		"datadog-apm":  EdgePriority5,
		"newrelic-apm": EdgePriority6,
	},
}

// metricsToMerge defines which edge properties should be merged with source prefix
// when edges from multiple sources are deduplicated.
var metricsToMerge = []string{
	"latency_ms",
	"request_count",
	"failure_count",
	"bytes_sent",
	"bytes_received",
	"error_rate",
	"throughput",
	"response_time",
}

// GetEdgeSourcePriority returns the priority for a source creating a specific edge type.
// If the source or edge type is not in the priority map, returns EdgePriorityLowest.
func GetEdgeSourcePriority(source string, edgeType RelationshipType) EdgeSourcePriority {
	if priorities, ok := edgeTypePriorities[edgeType]; ok {
		if priority, ok := priorities[source]; ok {
			return priority
		}
	}
	return EdgePriorityLowest
}

// DeduplicateEdgesWithPriority deduplicates edges using source priority and composite key.
// When multiple sources create the same edge (same source node, dest node, and edge type),
// the source with the highest priority becomes the primary source.
// Properties from lower priority sources are merged with source prefix (e.g., traces_latency_ms).
//
// This ensures:
// 1. Only one edge exists per (source_node, dest_node, tenant) tuple
// 2. The primary source is determined by priority (eBPF > Traces > Datadog for CALLS)
// 3. Metrics from all sources are preserved with source prefixes
// 4. All contributing sources are tracked in the "contributing_sources" property
func DeduplicateEdgesWithPriority(edges []*DbEdge) []*DbEdge {
	edgeMap := make(map[string]*DbEdge)

	for _, edge := range edges {
		// Create composite key for deduplication
		compositeKey := buildEdgeCompositeKey(edge)

		if existing, exists := edgeMap[compositeKey]; exists {
			existingPriority := GetEdgeSourcePriority(existing.Source, existing.RelationshipType)
			newPriority := GetEdgeSourcePriority(edge.Source, edge.RelationshipType)

			if newPriority < existingPriority { // Lower number = higher priority
				// New edge has higher priority - it becomes primary
				mergeEdgePropertiesWithSourcePrefix(edge, existing)
				edgeMap[compositeKey] = edge
			} else {
				// Existing edge has higher or equal priority - merge new into existing
				mergeEdgePropertiesWithSourcePrefix(existing, edge)
			}
		} else {
			// Initialize contributing_sources for new edges
			if edge.Properties == nil {
				edge.Properties = make(map[string]interface{})
			}
			if _, ok := edge.Properties["contributing_sources"]; !ok {
				edge.Properties["contributing_sources"] = []string{edge.Source}
			}
			edgeMap[compositeKey] = edge
		}
	}

	// Convert map back to slice
	result := make([]*DbEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		result = append(result, edge)
	}

	return result
}

// buildEdgeCompositeKey builds the composite key for edge deduplication
func buildEdgeCompositeKey(edge *DbEdge) string {
	return edge.SourceNodeID + ":" + edge.DestinationNodeID + ":" + edge.TenantID
}

// mergeEdgePropertiesWithSourcePrefix merges properties from a secondary edge into the primary edge.
// Metrics from the secondary edge are prefixed with its source name to preserve data from both sources.
func mergeEdgePropertiesWithSourcePrefix(primary, secondary *DbEdge) {
	if primary.Properties == nil {
		primary.Properties = make(map[string]interface{})
	}

	sourcePrefix := secondary.Source + "_"

	// Merge metrics with source prefix
	for _, metric := range metricsToMerge {
		if val, ok := secondary.Properties[metric]; ok {
			primary.Properties[sourcePrefix+metric] = val
		}
	}

	// Track contributing sources
	sources := getContributingSources(primary)
	if !containsString(sources, secondary.Source) {
		sources = append(sources, secondary.Source)
	}
	primary.Properties["contributing_sources"] = sources

	primary.UpdatedAt = time.Now()
}

// getContributingSources extracts the contributing_sources slice from edge properties
func getContributingSources(edge *DbEdge) []string {
	if edge.Properties == nil {
		return []string{edge.Source}
	}

	if sources, ok := edge.Properties["contributing_sources"].([]string); ok {
		return sources
	}

	// Handle []interface{} case (from JSON unmarshaling)
	if sourcesInterface, ok := edge.Properties["contributing_sources"].([]interface{}); ok {
		sources := make([]string, 0, len(sourcesInterface))
		for _, s := range sourcesInterface {
			if str, ok := s.(string); ok {
				sources = append(sources, str)
			}
		}
		if len(sources) > 0 {
			return sources
		}
	}

	return []string{edge.Source}
}

// containsString checks if a string slice contains a specific string
func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// ValidateNode validates that a node has required fields
func ValidateNode(node *DbNode) error {
	if node == nil {
		return fmt.Errorf("node is nil")
	}
	if node.NodeType == "" {
		return fmt.Errorf("node type is required")
	}
	if node.UniqueKey == "" {
		return fmt.Errorf("unique key is required")
	}
	if node.TenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	return nil
}

// ValidateEdge validates that an edge has required fields
func ValidateEdge(edge *DbEdge) error {
	if edge == nil {
		return fmt.Errorf("edge is nil")
	}
	if edge.SourceNodeID == "" {
		return fmt.Errorf("source node ID is required")
	}
	if edge.DestinationNodeID == "" {
		return fmt.Errorf("destination node ID is required")
	}
	if edge.RelationshipType == "" {
		return fmt.Errorf("relationship type is required")
	}
	if edge.TenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	return nil
}

// GetNodeProperty safely retrieves a property from a node
func GetNodeProperty(node *DbNode, key string) (interface{}, bool) {
	if node == nil || node.Properties == nil {
		return nil, false
	}
	val, ok := node.Properties[key]
	return val, ok
}

// GetNodePropertyString retrieves a string property from a node
func GetNodePropertyString(node *DbNode, key string) (string, bool) {
	val, ok := GetNodeProperty(node, key)
	if !ok {
		return "", false
	}
	str, ok := val.(string)
	return str, ok
}

// GetEdgeProperty safely retrieves a property from an edge
func GetEdgeProperty(edge *DbEdge, key string) (interface{}, bool) {
	if edge == nil || edge.Properties == nil {
		return nil, false
	}
	val, ok := edge.Properties[key]
	return val, ok
}

// SetNodeProperty safely sets a property on a node
func SetNodeProperty(node *DbNode, key string, value interface{}) {
	if node == nil {
		return
	}
	if node.Properties == nil {
		node.Properties = make(map[string]interface{})
	}
	node.Properties[key] = value
}

// SetEdgeProperty safely sets a property on an edge
func SetEdgeProperty(edge *DbEdge, key string, value interface{}) {
	if edge == nil {
		return
	}
	if edge.Properties == nil {
		edge.Properties = make(map[string]interface{})
	}
	edge.Properties[key] = value
}

// nodeTypeLogoMap maps NodeType to its logo identifier (fallback when no property-based match is found).
var nodeTypeLogoMap = map[NodeType]string{
	NodeTypeExternalService:    "externalservice",
	NodeTypeHelmChart:          "helmchart",
	NodeTypeHelmRelease:        "helmchart",
	NodeTypeRepository:         "repository",
	NodeTypeCluster:            "cluster",
	NodeTypeManagedCluster:     "cluster",
	NodeTypeNamespace:          "namespace",
	NodeTypeServerlessFunction: "serverlessfunction",
	NodeTypeCDN:                "cdn",
	NodeTypeRouteTable:         "routetable",
	NodeTypeK8sService:         "k8sservice",
	NodeTypePVC:                "persistentvolumeclaim",
	NodeTypePV:                 "persistentvolume",
	NodeTypeLoadBalancer:       "loadbalancer",
	NodeTypeNode:               "node",
}

// getNodeProp safely retrieves a string property value from a node's properties map.
// Returns "" if the map is nil, the key is absent, or the value is nil.
func getNodeProp(properties map[string]interface{}, key string) string {
	if properties == nil {
		return ""
	}
	if v, ok := properties[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// languageLogoID normalizes a backend canonical language name to the logo_id expected by LangTypeIcon.jsx.
// LangTypeIcon lowercases before matching, so keys here must align with its switch cases.
func languageLogoID(lang string) string {
	switch strings.ToLower(lang) {
	case "javascript", "typescript", "js", "ts":
		return "nodejs" // LangTypeIcon has 'nodejs', not 'javascript'
	case "c#", "csharp":
		return "dotnet" // LangTypeIcon has 'dotnet', not 'c#'
	default:
		return strings.ToLower(lang) // golang, python, java, ruby, php pass through as-is
	}
}

// resolveServiceLogoID resolves the logo_id from service_name, handling AWS special cases where
// multiple node types share the same service_name or where the LangTypeIcon key differs.
// Returns "" when serviceName is empty so callers can fall through to the next priority.
func resolveServiceLogoID(nodeType NodeType, source, serviceName, propType string) string {
	// EC2-based resources that need a different icon than the generic EC2 one
	if serviceName == "AmazonEC2" && propType == "storage" {
		return "AmazonEBS"
	}
	if serviceName == "AmazonEC2" && propType == "natgateway" {
		return "natgateway"
	}
	// AWS VPC: use source field (top-level) because service_name may be absent for VPC nodes
	if source == "aws" && nodeType == NodeTypeVPC {
		return "AmazonVPC"
	}
	// ELB: AWSELB is the service_name but LangTypeIcon expects 'aws-elb'
	if nodeType == NodeTypeLoadBalancer && serviceName == "AWSELB" {
		return "aws-elb"
	}
	// ElastiCache: backend sets "AmazonElastiCache"; LangTypeIcon expects 'aws-elasticache'
	if serviceName == "AmazonElastiCache" {
		return "aws-elasticache"
	}
	return serviceName // "" when absent; all other service_name values pass through to LangTypeIcon
}

// ComputeLogoID returns the icon identifier for a KG node so the frontend can render the correct logo.
// source is the node's top-level source field (e.g. "aws", "gcp", "k8s", "trace") — not from properties.
func ComputeLogoID(nodeType NodeType, source string, properties map[string]interface{}) string {
	// 1. Database engine (e.g. "POSTGRES_17", "MYSQL_8_0")
	engine := strings.ToLower(getNodeProp(properties, "engine"))
	if strings.Contains(engine, "postgres") {
		return "postgres"
	}
	if strings.Contains(engine, "mysql") {
		return "mysql"
	}
	if strings.Contains(engine, "sqlserver") || strings.Contains(engine, "sql_server") {
		return "sqlserver"
	}

	// 2. Node-type overrides — checked before service_name because RouteTable and SecurityGroup
	// both carry service_name="AmazonVPC" and need their own distinct icons.
	switch nodeType {
	case NodeTypeSecurityGroup:
		return "securitygroup"
	case NodeTypeRouteTable:
		return "routetable"
	case NodeTypeNetworkGateway:
		return "natgateway"
	}

	// 3. Service name (AWS / GCP / Azure) with special-case normalization
	propType := strings.ToLower(getNodeProp(properties, "type"))
	if id := resolveServiceLogoID(nodeType, source, getNodeProp(properties, "service_name"), propType); id != "" {
		return id
	}

	// 4. Programming language — normalize to LangTypeIcon-compatible keys
	if lang := getNodeProp(properties, "language"); lang != "" {
		return languageLogoID(lang)
	}

	// 5. K8s workload kind
	if nodeType == NodeTypeWorkload {
		if kind := getNodeProp(properties, "kind"); kind != "" {
			return strings.ToLower(kind)
		}
	}

	// 6. Node-type fallback
	return nodeTypeLogoMap[nodeType]
}

// ConvertDbNodeToKgNode converts a DbNode to a KgNode
func ConvertDbNodeToKgNode(dbNode *DbNode) KgNode {
	if dbNode == nil {
		return KgNode{}
	}

	// Check if Properties contains a "labels" field
	labels := make(map[string]string)
	properties := dbNode.Properties

	if dbNode.Properties != nil {
		if labelsValue, exists := dbNode.Properties["labels"]; exists {
			// Try to convert labels to map[string]string
			if labelsMap, ok := labelsValue.(map[string]string); ok {
				labels = labelsMap
			} else if labelsInterface, ok := labelsValue.(map[string]interface{}); ok {
				// Convert map[string]interface{} to map[string]string
				for k, v := range labelsInterface {
					labels[k] = fmt.Sprintf("%v", v)
				}
			}

			// Remove labels from properties to avoid duplication
			properties = make(map[string]interface{})
			for k, v := range dbNode.Properties {
				if k != "labels" {
					properties[k] = v
				}
			}
		} else {
			// If no labels property, convert all properties to labels
			labels = map[string]string{}
		}
	}

	return KgNode{
		ID:             dbNode.ID,
		NodeType:       dbNode.NodeType,
		Category:       dbNode.NodeType.GetCategory(),
		UniqueKey:      dbNode.UniqueKey,
		CloudAccountID: dbNode.CloudAccountID,
		TenantID:       dbNode.TenantID,
		Level:          dbNode.Level,
		Source:         dbNode.Source,
		CreatedAt:      dbNode.CreatedAt,
		UpdatedAt:      dbNode.UpdatedAt,
		Properties:     properties,
		Labels:         labels,
		LastUpdated:    dbNode.UpdatedAt,
		LogoID:         ComputeLogoID(dbNode.NodeType, dbNode.Source, properties),
	}
}

// ConvertDbEdgeToKgEdge converts a DbEdge to a KgEdge
func ConvertDbEdgeToKgEdge(dbEdge *DbEdge) KgEdge {
	if dbEdge == nil {
		return KgEdge{}
	}
	return KgEdge{
		ID:                dbEdge.ID,
		SourceNodeID:      dbEdge.SourceNodeID,
		DestinationNodeID: dbEdge.DestinationNodeID,
		RelationshipType:  dbEdge.RelationshipType,
		Properties:        dbEdge.Properties,
		CloudAccountID:    dbEdge.CloudAccountID,
		TenantID:          dbEdge.TenantID,
		Level:             dbEdge.Level,
		CreatedAt:         dbEdge.CreatedAt,
		UpdatedAt:         dbEdge.UpdatedAt,
	}
}

// ConvertDbNodesToKgNodes converts a slice of DbNode pointers to a slice of KgNodes
func ConvertDbNodesToKgNodes(dbNodes []*DbNode) []KgNode {
	kgNodes := make([]KgNode, 0, len(dbNodes))
	for _, dbNode := range dbNodes {
		kgNodes = append(kgNodes, ConvertDbNodeToKgNode(dbNode))
	}
	return kgNodes
}

// ConvertDbNodesToKgNodesWithAccountNames converts DbNodes to KgNodes and replaces
// account_id (UUID) with account_name in unique_key for user-readable display
func ConvertDbNodesToKgNodesWithAccountNames(dbNodes []*DbNode, accountMappings map[string]string) []KgNode {
	kgNodes := make([]KgNode, 0, len(dbNodes))
	for _, dbNode := range dbNodes {
		kgNode := ConvertDbNodeToKgNode(dbNode)

		// Replace account_id with account_name in unique_key for UI display
		// Unique key format: {source}:{account}:{location}:{NodeType}:{hierarchy}:{name}
		if len(accountMappings) > 0 && kgNode.UniqueKey != "" {
			parts := strings.Split(kgNode.UniqueKey, ":")
			if len(parts) == 6 { // Ensure it's the 6-part format
				accountID := parts[1]
				if accountName, exists := accountMappings[accountID]; exists && accountName != "" {
					// Replace account_id with account_name
					parts[1] = accountName
					kgNode.UniqueKey = strings.Join(parts, ":")
				}
			}
		}

		kgNodes = append(kgNodes, kgNode)
	}
	return kgNodes
}

// ConvertDbEdgesToKgEdges converts a slice of DbEdge pointers to a slice of KgEdges
func ConvertDbEdgesToKgEdges(dbEdges []*DbEdge) []KgEdge {
	kgEdges := make([]KgEdge, 0, len(dbEdges))
	for _, dbEdge := range dbEdges {
		kgEdges = append(kgEdges, ConvertDbEdgeToKgEdge(dbEdge))
	}
	return kgEdges
}

// ConvertKgNodeToKgNodeSlim converts a KgNode to KgNodeSlim with only essential fields
func ConvertKgNodeToKgNodeSlim(kgNode KgNode) KgNodeSlim {
	name := ""
	if kgNode.Properties != nil {
		if nameVal, ok := kgNode.Properties["name"]; ok {
			name = fmt.Sprintf("%v", nameVal)
		}
	}
	return KgNodeSlim{
		ID:        kgNode.ID,
		Kind:      kgNode.NodeType,
		Name:      name,
		Source:    kgNode.Source,
		AccountID: kgNode.CloudAccountID,
		TenantID:  kgNode.TenantID,
		UniqueKey: kgNode.UniqueKey,
		LogoID:    kgNode.LogoID,
	}
}

// ConvertKgEdgeToKgEdgeSlim converts a KgEdge to KgEdgeSlim with only essential fields
func ConvertKgEdgeToKgEdgeSlim(kgEdge KgEdge) KgEdgeSlim {
	return KgEdgeSlim{
		ID:                kgEdge.ID,
		SourceNodeID:      kgEdge.SourceNodeID,
		DestinationNodeID: kgEdge.DestinationNodeID,
		RelationshipType:  kgEdge.RelationshipType,
	}
}

// ConvertKnowledgeGraphToSlim converts a full KnowledgeGraph to KnowledgeGraphSlim
func ConvertKnowledgeGraphToSlim(kg KnowledgeGraph) KnowledgeGraphSlim {
	nodes := make([]KgNodeSlim, 0, len(kg.Nodes))
	for _, n := range kg.Nodes {
		nodes = append(nodes, ConvertKgNodeToKgNodeSlim(n))
	}
	edges := make([]KgEdgeSlim, 0, len(kg.Edges))
	for _, e := range kg.Edges {
		edges = append(edges, ConvertKgEdgeToKgEdgeSlim(e))
	}
	return KnowledgeGraphSlim{
		Nodes:       nodes,
		Edges:       edges,
		TenantID:    kg.TenantID,
		AccountID:   kg.AccountID,
		GeneratedAt: kg.GeneratedAt,
	}
}

// ConvertGraphToKnowledgeGraph converts a Graph to a KnowledgeGraph
func ConvertGraphToKnowledgeGraph(graph *Graph) KnowledgeGraph {
	if graph == nil {
		return KnowledgeGraph{}
	}
	return KnowledgeGraph{
		Nodes:       ConvertDbNodesToKgNodes(graph.Nodes),
		Edges:       ConvertDbEdgesToKgEdges(graph.Edges),
		GeneratedAt: graph.GeneratedAt,
		TenantID:    graph.TenantID,
		AccountID:   graph.CloudAccountID,
	}
}

// GetK8sCloudAccountMapping maps K8s account IDs to their corresponding cloud account IDs.
// One K8s account can be mapped to multiple cloud accounts.
// Returns a map where k8s_account_id -> []cloud_account_id
// cloudProvider: optional filter (e.g., "AWS", "Azure", "GCP"). Pass empty string for no filter.
func GetK8sCloudAccountMapping(ctx *security.RequestContext, tenantID string, cloudProvider string) (map[string][]string, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Query to get all K8s accounts with k8s_provider_account_number for the tenant
	// and their corresponding cloud account IDs
	// Optionally filter by cloud provider
	query := `
		SELECT
			caa.cloud_account_id as k8s_account_id,
			ca.id as cloud_account_id
		FROM
			cloud_account_attrs caa
		INNER JOIN
			cloud_accounts ca ON ca.account_number = caa.value
		WHERE
			caa.name = 'k8s_provider_account_number'
			AND ca.tenant = $1
			AND ca.status = 'active'
	`

	args := []interface{}{tenantID}

	// Add optional cloud provider filter
	if cloudProvider != "" {
		query += `
			AND ca.cloud_provider = $2`
		args = append(args, cloudProvider)
	}

	rows, err := databaseManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud account mappings: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close rows", "error", err)
		}
	}()

	// Build the mapping: k8s_account_id -> []cloud_account_id
	// One K8s account can be mapped to multiple cloud accounts
	mapping := make(map[string][]string)
	for rows.Next() {
		var k8sAccountID, cloudAccountID string
		err := rows.Scan(&k8sAccountID, &cloudAccountID)
		if err != nil {
			return nil, fmt.Errorf("failed to scan cloud account mapping: %w", err)
		}
		mapping[k8sAccountID] = append(mapping[k8sAccountID], cloudAccountID)
	}

	return mapping, nil
}

// GetAWSAccountsForTenant retrieves all active AWS cloud account IDs for a tenant
// This is used to query Route 53 across multiple AWS accounts
// Native implementation that queries the database directly
func GetAWSAccountsForTenant(tenantID string) ([]string, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		SELECT id
		FROM cloud_accounts
		WHERE tenant = $1
		  AND cloud_provider = 'AWS'
		  AND status = 'active'
	`

	var accountIDs []string
	err = dbManager.Db.Select(&accountIDs, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query AWS accounts: %w", err)
	}

	return accountIDs, nil
}

// K8sAccount represents a Kubernetes cloud account
type K8sAccount struct {
	CloudAccountID string `db:"cloud_account_id"`
	Name           string `db:"name"`
	Tenant         string `db:"tenant"`
}

// GetK8sAccountsForTenant retrieves all K8s accounts with connected agents for a tenant
// Optionally filters by specific cloud account IDs
func GetK8sAccountsForTenant(tenantID string, cloudAccountIDs []string) ([]K8sAccount, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Build query with optional cloud account IDs filter
	query := `
		SELECT ca.id as cloud_account_id, ca.account_name as name, ca.tenant as tenant
		FROM cloud_accounts ca
		INNER JOIN agent a ON ca.id = a.cloud_account_id
		WHERE ca.cloud_provider = 'K8s'
		  AND a.status = 'CONNECTED'
		  AND ca.tenant = ?`

	args := []interface{}{tenantID}

	// Add cloud account IDs filter if provided
	if len(cloudAccountIDs) > 0 {
		query += `
		  AND ca.id IN (?)`
		args = append(args, cloudAccountIDs)
	}

	query += `
		GROUP BY ca.tenant, ca.id`

	// Expand the query using sqlx.In to handle the slice
	var queryErr error
	query, args, queryErr = sqlx.In(query, args...)
	if queryErr != nil {
		return nil, fmt.Errorf("failed to expand query with IN clause: %w", queryErr)
	}

	// Rebind the query to use PostgreSQL's $1, $2, ... placeholder format
	query = dbManager.Db.Rebind(query)

	rows, err := dbManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query K8s accounts with connected agents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var k8sAccounts []K8sAccount
	for rows.Next() {
		var account K8sAccount
		if err := rows.StructScan(&account); err != nil {
			continue
		}
		k8sAccounts = append(k8sAccounts, account)
	}

	return k8sAccounts, nil
}

// ExtractDeploymentFromReplicaSet extracts the Deployment name from a ReplicaSet name
// ReplicaSet naming pattern: {deployment-name}-{hash}
// Example: manoj-shipper-759b8c597f → manoj-shipper
func ExtractDeploymentFromReplicaSet(replicaSetName string) string {
	parts := strings.Split(replicaSetName, "-")
	if len(parts) < 2 {
		return replicaSetName
	}

	// Remove last part if it looks like a ReplicaSet hash (typically 9-10 alphanumeric chars)
	lastPart := parts[len(parts)-1]
	if len(lastPart) >= 8 && len(lastPart) <= 10 && IsAlphanumeric(lastPart) {
		return strings.Join(parts[:len(parts)-1], "-")
	}

	return replicaSetName
}

// ExtractPodOwner extracts the owner kind and name from a Kubernetes pod name
//
// Returns (ownerKind, ownerName)
// Pod naming patterns:
//   - Deployment: {name}-{deployment-hash}-{pod-id}
//     Example: tracking-service-external-5844c67545-kgqsg → ("Deployment", "tracking-service-external")
//   - Job/CronJob: {name}-{job-hash}-{pod-id} (both hashes are 5 chars)
//     Example: k8s-action-runner-nudgebee-brdzm-29kp9 → ("Job", "k8s-action-runner-nudgebee")
//   - StatefulSet: {name}-{ordinal}
//     Example: kafka-0 → ("StatefulSet", "kafka")
//   - DaemonSet: {name}-{pod-id}
//     Example: fluentd-abc12 → ("DaemonSet", "fluentd")
func ExtractPodOwner(podName string) (string, string) {
	parts := strings.Split(podName, "-")
	if len(parts) < 2 {
		return "Deployment", podName
	}

	// Check if last part is a number (StatefulSet pattern)
	lastPart := parts[len(parts)-1]
	if _, err := strconv.Atoi(lastPart); err == nil {
		// StatefulSet: remove last part (ordinal number)
		ownerName := strings.Join(parts[:len(parts)-1], "-")
		return "StatefulSet", ownerName
	}

	// Check if last part is a 5-char alphanumeric (pod ID)
	if len(lastPart) == 5 && IsAlphanumeric(lastPart) {
		parts = parts[:len(parts)-1]
	}

	// Check if new last part is a hash (deployment or job)
	if len(parts) > 1 {
		lastPart = parts[len(parts)-1]

		// Deployment hash: 8-10 char alphanumeric
		if len(lastPart) >= 8 && len(lastPart) <= 10 && IsAlphanumeric(lastPart) {
			ownerName := strings.Join(parts[:len(parts)-1], "-")
			return "Deployment", ownerName
		}

		// Job/CronJob hash: 5 char alphanumeric (same as pod ID)
		// e.g., k8s-action-runner-nudgebee-brdzm-29kp9 → k8s-action-runner-nudgebee
		if len(lastPart) == 5 && IsAlphanumeric(lastPart) {
			ownerName := strings.Join(parts[:len(parts)-1], "-")
			return "Job", ownerName
		}
	}

	// Fallback: assume DaemonSet (or unknown pattern)
	ownerName := strings.Join(parts, "-")
	return "Deployment", ownerName
}

// IsAlphanumeric checks if a string contains only lowercase alphanumeric characters
func IsAlphanumeric(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// IsKubernetesInternalDNS checks if a hostname is a Kubernetes internal DNS name
func IsKubernetesInternalDNS(hostname string) bool {
	if hostname == "" {
		return false
	}
	return strings.HasSuffix(hostname, ".svc.cluster.local") ||
		strings.HasSuffix(hostname, ".pod.cluster.local") ||
		hostname == "localhost" ||
		hostname == "127.0.0.1"
}
