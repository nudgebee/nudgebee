package traces

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/services/internal/database"
	"strings"
	"time"
)

const (
	// DefaultKafkaClusterName is the fallback name for Kafka clusters when a specific name cannot be determined
	DefaultKafkaClusterName = "kafka-cluster"
)

// APM Entities API structures

// APMEntitiesResponse represents the APM entities API response from Datadog
type APMEntitiesResponse struct {
	Data []APMEntity `json:"data"`
}

// APMGraphResponse represents the combined response from the graph API
// The graph endpoint returns BOTH entities and edges in the same response
type APMGraphResponse struct {
	Data     []json.RawMessage `json:"data"`     // Can be either APMEntity or APMEntityEdge
	Included []json.RawMessage `json:"included"` // Can be either APMEntity or APMEntityEdge
}

// APMGraphData holds both entities and edges parsed from the graph response
type APMGraphData struct {
	Entities []APMEntity
	Edges    []APMEntityEdge
}

type APMEntity struct {
	ID            string                 `json:"id"`
	Type          string                 `json:"type"`
	Attributes    APMEntityAttributes    `json:"attributes"`
	Relationships APMEntityRelationships `json:"relationships"`
}

type APMEntityAttributes struct {
	IDTags        map[string]string `json:"id_tags"`
	Metadata      APMMetadata       `json:"metadata"`
	ServiceHealth ServiceHealth     `json:"service_health"`
	Stats         *APMStats         `json:"stats,omitempty"`
}

type APMMetadata struct {
	IsTraced     bool     `json:"is_traced"`
	IsUSM        bool     `json:"is_usm"`
	Languages    []string `json:"languages,omitempty"`
	ProductAreas []string `json:"product_areas"`
	Apdex        string   `json:"apdex_threshold,omitempty"`
}

type ServiceHealth struct {
	Status string `json:"status"`
}

type APMStats struct {
	Operation         string   `json:"operation"`
	SpanKind          string   `json:"span.kind"`
	RequestsPerSecond float64  `json:"requests_per_second"`
	LatencyAvg        float64  `json:"latency_avg"`
	ErrorsPercentage  *float64 `json:"errors_percentage,omitempty"`
	OperationMode     string   `json:"operation_mode,omitempty"`
}

type APMEntityRelationships struct {
	Type RelationshipDataWrapper `json:"type"`
}

type RelationshipDataWrapper struct {
	Data RelationshipData `json:"data"`
}

type RelationshipData struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// APM Entity Edge API structures

type APMEntityEdge struct {
	ID            string                     `json:"id"`
	Type          string                     `json:"type"` // "apm-entity-edge"
	Attributes    APMEntityEdgeAttributes    `json:"attributes"`
	Relationships APMEntityEdgeRelationships `json:"relationships"`
}

type APMEntityEdgeAttributes struct {
	APMFilter map[string]string `json:"apm_filter"`
	Operation string            `json:"operation"`
	SpanKind  string            `json:"span.kind"`
}

type APMEntityEdgeRelationships struct {
	Source EntityReference `json:"source"`
	Target EntityReference `json:"target"`
}

type EntityReference struct {
	Data EntityData `json:"data"`
}

type EntityData struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "apm-entity"
}

// EdgeInfo holds information about an edge relationship between services
type EdgeInfo struct {
	Edge       *APMEntityEdge
	SourceID   string
	TargetID   string
	TargetName string
	Protocol   string
}

// APMEntitiesGraphParams holds parameters for fetching APM entities graph
type APMEntitiesGraphParams struct {
	FromTimestamp        int64    // Unix timestamp for start time
	ToTimestamp          int64    // Unix timestamp for end time
	Environment          string   // Environment filter (e.g., "none", "production")
	Columns              []string // Columns to include
	Include              []string // Related entities to include
	Datastore            string   // Datastore type (e.g., "metrics")
	PageSize             int      // Page size (0 for all)
	ReturnLegacyFields   bool     // Return legacy fields
	MetadataFilter       string   // Metadata filter (e.g., "color")
	HideServiceOverrides bool     // Hide service overrides
}

// ParseAPMGraphResponse parses the graph API response which contains both entities and edges
func ParseAPMGraphResponse(body []byte) (*APMGraphData, error) {
	var graphResp APMGraphResponse
	if err := json.Unmarshal(body, &graphResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal graph response: %w", err)
	}
	entities := []json.RawMessage{}

	graphData := &APMGraphData{
		Entities: make([]APMEntity, 0),
		Edges:    make([]APMEntityEdge, 0),
	}

	// Parse each item and determine if it's an entity or an edge based on the "type" field
	entities = append(entities, graphResp.Data...)
	entities = append(entities, graphResp.Included...)
	for _, rawItem := range entities {
		// First, peek at the type field
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawItem, &typeCheck); err != nil {
			slog.Warn("Failed to unmarshal type field, skipping item", "error", err)
			continue
		}

		switch typeCheck.Type {
		case "apm-entity", "apm-entity-type":
			var entity APMEntity
			if err := json.Unmarshal(rawItem, &entity); err != nil {
				slog.Warn("Failed to unmarshal APM entity", "error", err, "type", typeCheck.Type)
				continue
			}
			graphData.Entities = append(graphData.Entities, entity)

		case "apm-entity-edge":
			var edge APMEntityEdge
			if err := json.Unmarshal(rawItem, &edge); err != nil {
				slog.Warn("Failed to unmarshal APM entity edge", "error", err)
				continue
			}
			graphData.Edges = append(graphData.Edges, edge)

		default:
			slog.Debug("Unknown type in graph response", "type", typeCheck.Type)
		}
	}

	slog.Info("Parsed APM graph response",
		"entities_count", len(graphData.Entities),
		"edges_count", len(graphData.Edges))

	return graphData, nil
}

// FetchDatadogAPMGraphData fetches both entities and edges from the graph API
// This is the recommended function to use as it returns complete graph data including edges
func FetchDatadogAPMGraphData(config *DatadogAPIConfig, params APMEntitiesGraphParams) (*APMGraphData, error) {
	domain := strings.TrimPrefix(config.Site, "api.")

	// Construct URL with appropriate scheme
	var url string
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		// Domain already has scheme (for testing)
		url = fmt.Sprintf("%s/api/unstable/apm/entities/graph", domain)
	} else {
		// Add https:// for production Datadog endpoints
		url = fmt.Sprintf("https://%s/api/unstable/apm/entities/graph", domain)
	}

	slog.Info("Fetching Datadog APM graph data (entities + edges)", "url", url, "from", params.FromTimestamp, "to", params.ToTimestamp)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set query parameters based on the curl request
	q := req.URL.Query()

	// Time range filters
	q.Set("filter[from]", fmt.Sprintf("%d", params.FromTimestamp))
	q.Set("filter[to]", fmt.Sprintf("%d", params.ToTimestamp))
	// q.Set("filter[env]", fmt.Sprintf("%v", "eu-production"))

	// Environment filter
	if params.Environment != "" {
		q.Set("filter[env]", params.Environment)
	}

	// Columns filter
	if len(params.Columns) > 0 {
		q.Set("filter[columns]", strings.Join(params.Columns, ","))
	}

	// Include parameter
	if len(params.Include) > 0 {
		q.Set("include", strings.Join(params.Include, ","))
	}

	// Datastore
	if params.Datastore != "" {
		q.Set("datastore", params.Datastore)
	}

	// Page size
	q.Set("page[size]", fmt.Sprintf("%d", params.PageSize))

	// Return legacy fields
	if params.ReturnLegacyFields {
		q.Set("return_legacy_fields", "true")
	} else {
		q.Set("return_legacy_fields", "false")
	}

	// Metadata filter
	if params.MetadataFilter != "" {
		q.Set("filter[metadata]", params.MetadataFilter)
	}

	// Hide service overrides
	if params.HideServiceOverrides {
		q.Set("graph.hide_service_overrides", "true")
	} else {
		q.Set("graph.hide_service_overrides", "false")
	}

	req.URL.RawQuery = q.Encode()

	// Set headers
	req.Header.Set("DD-API-KEY", config.APIKey)
	req.Header.Set("DD-APPLICATION-KEY", config.ApplicationKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch APM graph data: %w", err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status code %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the response to extract both entities and edges
	graphData, err := ParseAPMGraphResponse(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse graph response: %w", err)
	}

	slog.Info("Successfully fetched Datadog APM graph data",
		"entities_count", len(graphData.Entities),
		"edges_count", len(graphData.Edges),
		"response_size", len(body))

	return graphData, nil
}

// DatadogAPIConfig holds configuration for Datadog API calls
type DatadogAPIConfig struct {
	APIKey         string
	ApplicationKey string
	Site           string // e.g., "datadoghq.com", "datadoghq.eu", "us5.datadoghq.com"
}

// NewDatadogAPIConfig creates a new Datadog API configuration
// If site is empty, defaults to "datadoghq.com"
func NewDatadogAPIConfig(apiKey, applicationKey, site string) *DatadogAPIConfig {
	if site == "" {
		site = "datadoghq.com"
	}
	return &DatadogAPIConfig{
		APIKey:         apiKey,
		ApplicationKey: applicationKey,
		Site:           site,
	}
}

// DatadogServiceDependencyResponse represents the service dependency API response from Datadog
type DatadogServiceDependencyResponse map[string]DatadogServiceCalls

type DatadogServiceCalls struct {
	Calls []string `json:"calls"`
}

// BuildServiceMapFromDatadogAPIs fetches data from Datadog APIs and builds a service map
// This function now uses the graph API which returns both entities and edges in a single call
func BuildServiceMapFromDatadogAPIs(config *DatadogAPIConfig, cloudAccountID, tenantID string) (*ServiceMap, error) {
	slog.Info("Building service map from Datadog graph API (entities + edges)")
	now := time.Now().Unix()
	fromTimestamp := now - 86400

	// Fetch graph data which includes both entities and edges
	params := APMEntitiesGraphParams{
		FromTimestamp: int64(fromTimestamp),
		ToTimestamp:   int64(now),
		Environment:   "",
		Columns: []string{
			"OPERATION_NAME",
			"REQUESTS_PER_SECOND",
			"LATENCY_AVG",
			"ERRORS_PERCENTAGE",
		},
		Include: []string{
			"entity.catalog_definition",
			"entity.service_health",
			"entity.service_health.watchdog_third_party_alerts",
			"inferred_entities",
		},
		Datastore:            "metrics",
		PageSize:             0,
		ReturnLegacyFields:   false,
		MetadataFilter:       "color",
		HideServiceOverrides: false,
	}

	graphData, err := FetchDatadogAPMGraphData(config, params)
	if err != nil || graphData == nil {
		return nil, fmt.Errorf("failed to fetch APM graph data: %w", err)
	}

	slog.Info("Fetched graph data",
		"entities_count", len(graphData.Entities),
		"edges_count", len(graphData.Edges))

	// Build service map from graph data (entities + edges)
	serviceMap, err := BuildServiceMapFromGraphData(graphData, cloudAccountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to build service map from graph data: %w", err)
	}

	slog.Info("Successfully built service map from Datadog graph API",
		"total_applications", len(serviceMap.Applications))

	return serviceMap, nil
}

// extractKafkaClusterName extracts a readable cluster name from Kafka bootstrap servers
// e.g., "kafka-logs.us-central1.production.project44.com:9094" -> "kafka-logs"
// e.g., "kafka1.example.com:9092,kafka2.example.com:9092" -> "kafka1"
func extractKafkaClusterName(bootstrapServers string) string {
	if bootstrapServers == "" {
		return DefaultKafkaClusterName
	}

	// Split by comma if multiple servers are provided
	servers := strings.Split(bootstrapServers, ",")
	if len(servers) == 0 {
		return DefaultKafkaClusterName
	}

	// Take the first server
	firstServer := strings.TrimSpace(servers[0])

	// Remove port (everything after :)
	if idx := strings.Index(firstServer, ":"); idx != -1 {
		firstServer = firstServer[:idx]
	}

	// Extract first part of hostname (before first dot)
	// e.g., "kafka-logs.us-central1.production.project44.com" -> "kafka-logs"
	if idx := strings.Index(firstServer, "."); idx != -1 {
		return firstServer[:idx]
	}

	// Return the hostname as-is if no dots found
	if firstServer != "" {
		return firstServer
	}

	return DefaultKafkaClusterName
}

// resolveEntityNameAndKind extracts the display name and kind from an entity's ID tags
// Returns the resolved name, kind, and whether resolution was successful
func resolveEntityNameAndKind(entity *APMEntity) (name string, kind string, found bool) {
	if entity == nil {
		return "", "", false
	}

	// Check for service tag (normal service)
	if svc, ok := entity.Attributes.IDTags["service"]; ok {
		return svc, "Service", true
	}

	// Check for database peer
	if dbSystem, ok := entity.Attributes.IDTags["peer.db.system"]; ok {
		// Check if we have a specific database system type (e.g., redis, postgres, mysql)
		if db, hasSystem := entity.Attributes.IDTags["peer.db.name"]; hasSystem && db != "" {
			return db, dbSystem, true
		}
		// Fallback to generic Database if no system specified
		return dbSystem, dbSystem, true
	}

	// Check for RPC service peer
	if rpc, ok := entity.Attributes.IDTags["peer.rpc.service"]; ok {
		return rpc, "Service", true
	}

	// Check for hostname peer
	if host, ok := entity.Attributes.IDTags["peer.hostname"]; ok {
		return host, "ExternalService", true
	}

	// Check for Kafka messaging destination (topics)
	if topic, ok := entity.Attributes.IDTags["peer.messaging.destination"]; ok {
		if entity.Attributes.Stats != nil && (entity.Attributes.Stats.Operation == "kafka.consume" || entity.Attributes.Stats.Operation == "kafka.produce") {
			return topic, "kafka", true
		}
		return topic, "kafka", true
	}

	// Check for Kafka messaging system (without specific destination/topic)
	// This handles Kafka producers/consumers that connect to a cluster but don't specify a topic
	if messagingSystem, ok := entity.Attributes.IDTags["peer.messaging.system"]; (ok && messagingSystem == "kafka") || entity.Attributes.IDTags["peer.kafka.bootstrap.servers"] != "" {
		// Try to use bootstrap servers as the name (extract cluster name)
		if bootstrapServers, hasServers := entity.Attributes.IDTags["peer.kafka.bootstrap.servers"]; hasServers && bootstrapServers != "" {
			// Extract a readable cluster name from bootstrap servers
			// e.g., "kafka-logs.us-central1.production.project44.com:9094" -> "kafka-logs"
			clusterName := extractKafkaClusterName(bootstrapServers)
			return clusterName, "kafka", true
		}
		// Fallback to generic kafka name
		return DefaultKafkaClusterName, "kafka", true
	}

	// Check for AWS service tags (e.g., peer.aws.s3.bucket, peer.aws.dynamodb.table_name)
	for key, value := range entity.Attributes.IDTags {
		if strings.HasPrefix(key, "peer.aws.") && value != "" {
			// Extract service name from key (e.g., "peer.aws.s3.bucket" -> "s3")
			parts := strings.Split(key, ".")
			if len(parts) >= 3 {
				awsService := parts[2] // The service name is the 3rd part
				// Map AWS service names to proper display names
				serviceType := mapAWSServiceToType(awsService)
				// Return consolidated AWS service name (e.g., "aws:s3", "aws:dynamodb")
				// This ensures all resources of the same AWS service point to a single node
				consolidatedName := "aws"
				return consolidatedName, serviceType, true
			}
			// Fallback: use generic aws:unknown node
			return "aws", "ExternalService", true
		}

		// Check for AWS hostnames in peer.hostname values
		// Example: "prod-1031-images.s3.us-west-2.amazonaws.com" -> extract "s3"
		if key == "peer.hostname" && value != "" {
			// Parse AWS service from hostname value (not key)
			if strings.Contains(value, ".amazonaws.com") {
				parts := strings.Split(value, ".")
				var awsService string

				// Check common AWS hostname patterns:
				// Pattern 1: bucket.s3.region.amazonaws.com (s3 at index 1)
				if len(parts) >= 4 && parts[1] == "s3" {
					awsService = "s3"
				} else if len(parts) >= 3 {
					// Pattern 2: service.region.amazonaws.com (service at index 0)
					// Check if first part matches known AWS services
					knownServices := map[string]bool{
						"s3": true, "dynamodb": true, "sqs": true, "sns": true,
						"lambda": true, "rds": true, "ec2": true, "elasticache": true,
						"kinesis": true, "apigateway": true, "elasticloadbalancing": true,
					}
					if knownServices[parts[0]] {
						awsService = parts[0]
					}
				}

				if awsService != "" {
					serviceType := mapAWSServiceToType(awsService)
					// Return consolidated AWS service name
					return "aws", serviceType, true
				}
			}
			// Fallback for non-AWS hostnames: use the hostname as external service
			return value, "ExternalService", true
		}

	}

	return "", "", false
}

// mapAWSServiceToType maps AWS service names from tags to proper display types
func mapAWSServiceToType(awsService string) string {
	// Map common AWS services to their proper names
	serviceMap := map[string]string{
		"s3":          "S3",
		"dynamodb":    "DynamoDB",
		"sqs":         "SQS",
		"sns":         "SNS",
		"kinesis":     "Kinesis",
		"lambda":      "Lambda",
		"rds":         "RDS",
		"ec2":         "EC2",
		"eks":         "EKS",
		"ecs":         "ECS",
		"elasticache": "ElastiCache",
		"redshift":    "Redshift",
		"aurora":      "Aurora",
		"cloudwatch":  "CloudWatch",
		"eventbridge": "EventBridge",
	}

	if displayName, ok := serviceMap[strings.ToLower(awsService)]; ok {
		return displayName
	}

	// If not in map, return capitalized service name
	return strings.ToUpper(awsService)
}

// BuildServiceMapFromGraphData builds a service map from graph data (entities + edges)
// This is the primary function for building service maps from Datadog's graph API
func BuildServiceMapFromGraphData(graphData *APMGraphData, cloudAccountID, tenantID string) (*ServiceMap, error) {
	slog.Info("Building service map from graph data",
		"entities_count", len(graphData.Entities),
		"edges_count", len(graphData.Edges))

	// Build a map of entities by ID for quick lookup
	entityByID := make(map[string]*APMEntity)
	awsServiceMap := map[string]ServiceApplication{}

	// // Build a map of entities by service name
	// apmEntitiesMap := make(map[string]*APMEntity)

	// Build service dependencies from edges
	serviceEdges := make(map[string]EdgeInfo) // edge ID -> edge info

	// Process all edges to build relationships
	nibours := []string{}
	operation := map[string]bool{}
	// use this for testing a single node
	testID := ""
	for i := range graphData.Edges {
		edge := &graphData.Edges[i]

		if edge.Type != "apm-entity-edge" {
			continue
		}

		sourceID := edge.Relationships.Source.Data.ID
		targetID := edge.Relationships.Target.Data.ID

		protocol := inferProtocolFromOperation(edge.Attributes.Operation)
		operation[edge.Attributes.Operation] = true

		edgeInfo := EdgeInfo{
			Edge:     edge,
			SourceID: sourceID,
			TargetID: targetID,
			Protocol: protocol,
		}
		if testID != "" && edgeInfo.TargetID == testID {
			nibours = append(nibours, edgeInfo.SourceID)
		}
		if testID != "" && edgeInfo.SourceID == testID {
			nibours = append(nibours, edgeInfo.TargetID)
		}
		if testID != "" && (edgeInfo.TargetID == testID || edgeInfo.SourceID == testID) {
			serviceEdges[edge.ID] = edgeInfo
		}
		// When testID is empty, add all edges
		if testID == "" {
			serviceEdges[edge.ID] = edgeInfo
		}
	}
	for i := range graphData.Entities {
		entity := &graphData.Entities[i]
		if entity.Type == "apm-entity-type" {
			continue
		}
		for i := range nibours {
			if entity.ID == nibours[i] {
				entityByID[entity.ID] = entity
			}
		}
		if testID != "" && entity.ID == testID {
			entityByID[entity.ID] = entity
		}
		if testID == "" {
			entityByID[entity.ID] = entity
		}

	}

	// Build applications from entities (use entity ID as unique identifier)
	applications := make([]ServiceApplication, 0, len(entityByID))

	// Pre-index edges by source/target for O(1) lookup instead of scanning all edges per entity
	edgesBySource := make(map[string][]EdgeInfo, len(entityByID))
	edgesByTarget := make(map[string][]EdgeInfo, len(entityByID))
	for _, edgeInfo := range serviceEdges {
		edgesBySource[edgeInfo.SourceID] = append(edgesBySource[edgeInfo.SourceID], edgeInfo)
		edgesByTarget[edgeInfo.TargetID] = append(edgesByTarget[edgeInfo.TargetID], edgeInfo)
	}

	// Build applications for each entity
	for entityID, entity := range entityByID {
		// Skip entity types (we only want actual entities)
		if entity.Type != "apm-entity" {
			continue
		}

		outgoingEdges := edgesBySource[entityID]
		incomingEdges := edgesByTarget[entityID]

		apps := buildServiceApplicationFromGraphData(
			entityID,
			outgoingEdges,
			incomingEdges,
			entityByID,
			cloudAccountID,
			tenantID,
			awsServiceMap,
		)
		// Append all returned applications (service + optional K8s workload)
		applications = append(applications, apps...)
	}

	serviceMap := &ServiceMap{
		Applications: applications,
		GeneratedAt:  time.Now(),
		K8sMetadata:  nil,
	}

	slog.Info("Successfully built service map from graph data",
		"total_applications", len(applications),
		"total_edges", len(graphData.Edges))

	return serviceMap, nil
}

// buildServiceApplicationFromGraphData creates a ServiceApplication from graph data (edges + entity metadata)
// Returns a slice containing the service application and optionally a K8s workload node if K8s mapping exists
func buildServiceApplicationFromGraphData(
	entityID string,
	outgoingEdges,
	incomingEdges []EdgeInfo,
	entityByID map[string]*APMEntity,
	cloudAccountID, tenantID string,
	awsServiceMap map[string]ServiceApplication,
) []ServiceApplication {
	namespace := "default"
	kind := "Service"
	entity := entityByID[entityID]

	// Extract service name and kind from entity (for display/labels, not for unique identification)
	displayName := entityID // fallback to entity ID
	if resolvedName, resolvedKind, found := resolveEntityNameAndKind(entity); found {
		displayName = resolvedName
		kind = resolvedKind
	}
	appId := ServiceApplicationId{
		Name:      displayName,
		Kind:      kind,
		Namespace: namespace,
	}

	// Build upstreams from outgoing edges
	upstreams := make([]UpstreamLink, 0, len(outgoingEdges))
	downstreams := make([]DownstreamLink, 0, len(incomingEdges))
	protocolSet := make(map[string]bool)

	for _, edgeInfo := range outgoingEdges {
		protocol := edgeInfo.Protocol
		// Get target entity name and kind
		targetName := edgeInfo.TargetID // fallback to ID
		targetKind := kind              // default to current kind
		if targetEntity, ok := entityByID[edgeInfo.TargetID]; ok {
			if resolvedName, resolvedKind, found := resolveEntityNameAndKind(targetEntity); found {
				targetName = resolvedName
				targetKind = resolvedKind
			}
		}
		upstreamId := fmt.Sprintf(":%s:%s", targetKind, targetName)

		upstream := UpstreamLink{
			Id:            upstreamId,
			Status:        0,
			Stats:         []string{edgeInfo.Edge.Attributes.Operation, edgeInfo.Edge.Attributes.SpanKind},
			Weight:        1.0,
			Latency:       0,
			RequestCount:  0,
			FailureCount:  0,
			Protocol:      protocol,
			BytesSent:     0,
			BytesReceived: 0,
			DrillDown:     nil,
		}
		upstreams = append(upstreams, upstream)
	}

	// Build downstreams from incoming edges
	for _, edgeInfo := range incomingEdges {
		protocol := edgeInfo.Protocol
		// Get source entity name and kind (the service calling this service)
		sourceName := edgeInfo.SourceID // fallback to ID
		sourceKind := "Service"
		if sourceEntity, ok := entityByID[edgeInfo.SourceID]; ok {
			if resolvedName, resolvedKind, found := resolveEntityNameAndKind(sourceEntity); found {
				sourceName = resolvedName
				sourceKind = resolvedKind
			}
		}

		downstream := DownstreamLink{
			Id: ServiceApplicationId{
				Name:      sourceName,
				Kind:      sourceKind,
				Namespace: namespace,
			},
			Status:        0,
			Stats:         []string{edgeInfo.Edge.Attributes.Operation, edgeInfo.Edge.Attributes.SpanKind},
			Weight:        1.0,
			Latency:       0,
			RequestCount:  0,
			FailureCount:  0,
			Protocol:      protocol,
			BytesSent:     0,
			BytesReceived: 0,
			DrillDown:     nil,
		}
		downstreams = append(downstreams, downstream)
	}

	// Determine primary protocol
	appTypes := []string{}
	if len(protocolSet) > 0 {
		appTypes = make([]string, 0, len(protocolSet))
		for proto := range protocolSet {
			appTypes = append(appTypes, proto)
		}
	}
	appTypes = append(appTypes, appId.Kind)

	// If we have APM entity, use its protocol
	if entity != nil && entity.Attributes.Stats != nil {
		appTypes = append(appTypes, entity.Attributes.Stats.Operation)
		protocol := inferProtocol(entity)
		if protocol != "unknown" {
			appTypes = []string{protocol}
		}
	}

	// Build labels from APM entity and get K8s workload info
	labels, k8sWorkloadInfo := buildLabelsFromAPM(entity, kind, cloudAccountID, tenantID)

	// if ns, ok := labels["k8s.namespace"]; ok && ns != "" {
	// 	appId.Namespace = ns
	// }
	instances := []Instance{
		{
			Id:       appId,
			IsFailed: false,
		},
	}

	// Extract health and metrics from APM entity
	isHealthy := true
	healthReason := ""
	indicators := []string{}
	var nodeStats *NodeStats

	if entity != nil {
		// Set health status
		if entity.Attributes.ServiceHealth.Status != "" {
			healthStatus := entity.Attributes.ServiceHealth.Status
			isHealthy = (healthStatus == "healthy" || healthStatus == "ok")
			if !isHealthy {
				healthReason = fmt.Sprintf("Service health status: %s", healthStatus)
			}
			indicators = append(indicators, fmt.Sprintf("health:%s", healthStatus))
		}

		// Add APM stats indicators and populate NodeStats
		if entity.Attributes.Stats != nil {
			stats := entity.Attributes.Stats
			nodeStats = &NodeStats{}

			// Populate NodeStats from APM stats
			nodeStats.Latency = stats.LatencyAvg / 1000000.0
			nodeStats.RequestsPerSecond = stats.RequestsPerSecond
			if stats.ErrorsPercentage != nil {
				nodeStats.FailureCount = stats.RequestsPerSecond * (*stats.ErrorsPercentage / 100)
			}
			// Add indicators
			if stats.RequestsPerSecond > 0 {
				indicators = append(indicators, fmt.Sprintf("rps:%.2f", stats.RequestsPerSecond))
			}
			if stats.LatencyAvg > 0 {
				indicators = append(indicators, fmt.Sprintf("latency_ms:%.2f", stats.LatencyAvg))
			}
			if stats.ErrorsPercentage != nil && *stats.ErrorsPercentage > 0 {
				indicators = append(indicators, fmt.Sprintf("error_rate:%.2f%%", *stats.ErrorsPercentage))
			}
		}
	}

	app := ServiceApplication{
		Id:                appId,
		Category:          ServiceCategory{Category: "application"},
		Labels:            labels,
		Status:            nil,
		Indicators:        indicators,
		Upstreams:         upstreams,
		Downstreams:       downstreams,
		Instances:         instances,
		Type:              appTypes,
		DesiredInstances:  1,
		FailedInstances:   0,
		OOMKills:          0,
		Restarts:          0,
		CPUThrottlingTime: 0,
		VolumeSize:        0,
		VolumeUsed:        0,
		IsHealthy:         isHealthy,
		HealthReason:      healthReason,
		NodeStats:         nodeStats,
	}

	// Initialize result with the service application
	result := []ServiceApplication{app}

	// If K8s workload info exists, create a separate K8s workload node with unidirectional link from service to K8s workload
	if k8sWorkloadInfo != nil {
		// Create K8s workload node
		k8sWorkloadNode := buildK8sWorkloadNode(k8sWorkloadInfo, displayName, cloudAccountID, tenantID)

		// Create unidirectional link: Service -> K8s Workload (upstream link from service perspective)
		k8sUpstreamLink := UpstreamLink{
			Id:            fmt.Sprintf(":%s:%s", k8sWorkloadInfo.Kind, k8sWorkloadInfo.Name),
			Status:        0,
			Stats:         []string{"k8s_workload", "infrastructure"},
			Weight:        1.0,
			Latency:       0,
			RequestCount:  0,
			FailureCount:  0,
			Protocol:      "k8s",
			BytesSent:     0,
			BytesReceived: 0,
			DrillDown:     nil,
		}
		app.Upstreams = append(app.Upstreams, k8sUpstreamLink)

		// Update result with modified service app and add K8s workload node
		result[0] = app
		result = append(result, k8sWorkloadNode)

		slog.Info("Created unidirectional K8s workload link (service -> workload)",
			"service", displayName,
			"workload", k8sWorkloadInfo.Name,
			"kind", k8sWorkloadInfo.Kind)
	}

	return result
}

// inferProtocolFromOperation infers the protocol from the operation name
func inferProtocolFromOperation(operation string) string {
	operation = strings.ToLower(operation)
	protocolMap := map[string]string{
		"http.request":                 "http",
		"grpc":                         "grpc",
		"tcp.connect":                  "tcp",
		"dns.lookup":                   "dns",
		"postgres.query":               "postgres",
		"postgresql.query":             "postgres",
		"postgres.connection.commit":   "postgres",
		"postgres.connection.rollback": "postgres",
		"postgres.connect":             "postgres",
		"sqlite.connection.commit":     "sqlite",
		"sqlite.query":                 "sqlite",
		"redis.command":                "redis",
		"redis.query":                  "redis",
		"s3.command":                   "s3",
		"requests.request":             "http",
		"universal.http.client":        "http",
		"flask.request":                "flask",
		"web.request":                  "http",
		"http.client.request":          "http",
		"fastapi.request":              "fastapi",
		"kafka.consume":                "kafka",
		"kafka.produce":                "kafka",
		"pymongo.checkout":             "mongo",
		"pymongo.get_socket":           "mongo",
		"pymongo.cmd":                  "mongo",
		"mongo.query":                  "mongo",
		"grpc.client":                  "grpc",
	}

	for pattern, protocol := range protocolMap {
		if strings.Contains(operation, pattern) {
			return protocol
		}
	}

	return "unknown"
}

// buildLabelsFromAPM creates labels from APM data only (without catalog)
// Returns labels and K8s workload info (if found)
func buildLabelsFromAPM(apmEntity *APMEntity, kind string, cloudAccountID, tenantID string) (map[string]string, *K8sWorkloadInfo) {
	labels := make(map[string]string)
	var k8sWorkloadInfo *K8sWorkloadInfo

	if kind == "ExternalService" {
		labels["external"] = "true"
	}

	// Add APM entity metadata
	if apmEntity != nil {
		// Add languages
		if len(apmEntity.Attributes.Metadata.Languages) > 0 {
			labels["languages"] = strings.Join(apmEntity.Attributes.Metadata.Languages, ",")
		}

		// Add tracing information
		if apmEntity.Attributes.Metadata.IsTraced {
			labels["is_traced"] = "true"
		}
		if apmEntity.Attributes.Metadata.IsUSM {
			labels["is_usm"] = "true"
		}

		// Add product areas
		if len(apmEntity.Attributes.Metadata.ProductAreas) > 0 {
			labels["product_areas"] = strings.Join(apmEntity.Attributes.Metadata.ProductAreas, ",")
		}

		// Add operation info from stats
		if apmEntity.Attributes.Stats != nil {
			if apmEntity.Attributes.Stats.Operation != "" {
				labels["apm.operation"] = apmEntity.Attributes.Stats.Operation
			}
			if apmEntity.Attributes.Stats.SpanKind != "" {
				labels["apm.span_kind"] = apmEntity.Attributes.Stats.SpanKind
			}
			labels["dd_entity_ID"] = apmEntity.ID
		}

		// Enrich with k8s_workloads data if service name is available
		if serviceName, ok := apmEntity.Attributes.IDTags["service"]; ok && serviceName != "" && cloudAccountID != "" && tenantID != "" {
			k8sWorkloadInfo = enrichLabelsFromK8sWorkloads(labels, serviceName, cloudAccountID, tenantID)
		}
	}

	return labels, k8sWorkloadInfo
}

// K8sWorkloadInfo holds information about a Kubernetes workload from k8s_workloads table
type K8sWorkloadInfo struct {
	ExternalID      string
	Name            string
	Namespace       string
	Kind            string
	CloudResourceID string
	Labels          map[string]string
}

// enrichLabelsFromK8sWorkloads queries k8s_workloads table and adds relevant workload information to labels
// Returns the K8s workload info if found, nil otherwise
func enrichLabelsFromK8sWorkloads(labels map[string]string, serviceName, cloudAccountID, tenantID string) *K8sWorkloadInfo {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("Failed to get database manager for k8s_workloads enrichment", "error", err, "service", serviceName)
		return nil
	}
	slog.Info("Enriching labels from k8s_workloads", "service", serviceName)

	query := `
		SELECT
			external_id,
			name,
			namespace,
			kind,
			cloud_resource_id,
			labels
		FROM k8s_workloads
		WHERE labels ->> 'tags.datadoghq.com/service' = $1
		  AND cloud_account_id = $2
		  AND tenant_id = $3
		  AND is_active = true
		LIMIT 1
	`

	var result struct {
		ExternalID      string          `db:"external_id"`
		Name            string          `db:"name"`
		Namespace       string          `db:"namespace"`
		Kind            string          `db:"kind"`
		CloudResourceID string          `db:"cloud_resource_id"`
		Labels          json.RawMessage `db:"labels"`
	}

	err = dbManager.Db.Get(&result, query, serviceName, cloudAccountID, tenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Not finding a workload is not an error - many services may not have k8s workloads
			slog.Debug("No k8s_workload found for service", "service", serviceName, "cloud_account_id", cloudAccountID)
		} else {
			slog.Warn("Failed to query k8s_workloads for enrichment", "error", err, "service", serviceName)
		}
		return nil
	}

	k8sLabels := make(map[string]string)

	// Enrich labels with k8s workload information
	if result.ExternalID != "" {
		labels["k8s.external_id"] = result.ExternalID
	}
	if result.Name != "" {
		labels["k8s.workload_name"] = result.Name
	}
	if result.Namespace != "" {
		labels["k8s.namespace"] = result.Namespace
	}
	// Add commonly useful labels from k8s workload
	if version, ok := k8sLabels["tags.datadoghq.com/version"]; ok {
		labels["dd.version"] = version
	}
	if env, ok := k8sLabels["tags.datadoghq.com/env"]; ok {
		labels["dd.env"] = env
	}
	if team, ok := k8sLabels["team"]; ok {
		labels["dd.team"] = team
	}
	if app, ok := k8sLabels["app"]; ok {
		labels["dd.app"] = app
	}
	if component, ok := k8sLabels["component"]; ok {
		labels["dd.component"] = component
	}

	slog.Info("Enriched labels with k8s_workload data",
		"service", serviceName,
		"workload", result.Name,
		"namespace", result.Namespace,
		"kind", result.Kind)

	// Return K8s workload info for separate node creation
	return &K8sWorkloadInfo{
		ExternalID:      result.ExternalID,
		Name:            result.Name,
		Namespace:       result.Namespace,
		Kind:            result.Kind,
		CloudResourceID: result.CloudResourceID,
		Labels:          k8sLabels,
	}
}

// buildK8sWorkloadNode creates a separate ServiceApplication node for a Kubernetes workload
// This is used when a service has K8s workload information to create infrastructure nodes
func buildK8sWorkloadNode(workloadInfo *K8sWorkloadInfo, serviceName string, cloudAccountID, tenantID string) ServiceApplication {
	// Build the workload application ID
	appId := ServiceApplicationId{
		Name:      workloadInfo.Name,
		Kind:      workloadInfo.Kind, // Deployment, StatefulSet, DaemonSet, etc.
		Namespace: workloadInfo.Namespace,
	}

	// Build labels for the K8s workload node
	labels := make(map[string]string)
	labels["k8s.external_id"] = workloadInfo.ExternalID
	labels["k8s.workload_name"] = workloadInfo.Name
	labels["k8s.namespace"] = workloadInfo.Namespace
	labels["k8s.workload_kind"] = workloadInfo.Kind
	labels["k8s.cloud_resource_id"] = workloadInfo.CloudResourceID
	labels["associated_service"] = serviceName
	labels["cloud_account_id"] = cloudAccountID
	labels["tenant_id"] = tenantID

	// Add workload-specific labels
	for key, value := range workloadInfo.Labels {
		// Prefix workload labels with "workload." to distinguish them
		labels["workload."+key] = value
	}

	// Create instance for the K8s workload
	instances := []Instance{
		{
			Id:       appId,
			IsFailed: false,
		},
	}

	// K8s workload node
	workloadNode := ServiceApplication{
		Id:                appId,
		Category:          ServiceCategory{Category: "infrastructure"},
		Labels:            labels,
		Status:            nil,
		Indicators:        []string{"k8s_workload"},
		Upstreams:         []UpstreamLink{},   // Will be populated with service link
		Downstreams:       []DownstreamLink{}, // Will be populated with service link
		Instances:         instances,
		Type:              []string{"kubernetes"},
		DesiredInstances:  1,
		FailedInstances:   0,
		OOMKills:          0,
		Restarts:          0,
		CPUThrottlingTime: 0,
		VolumeSize:        0,
		VolumeUsed:        0,
		IsHealthy:         true,
		HealthReason:      "",
		NodeStats:         nil,
	}

	slog.Info("Created K8s workload node",
		"workload_name", workloadInfo.Name,
		"kind", workloadInfo.Kind,
		"namespace", workloadInfo.Namespace,
		"associated_service", serviceName)

	return workloadNode
}

// protocolPatterns maps protocol keywords to their protocol names
// Order matters: more specific protocols should be checked before generic ones (like HTTP)
var protocolPatterns = []struct {
	protocol string
	keywords []string
}{
	{"grpc", []string{"grpc"}},
	{"mysql", []string{"mysql", "sql.query"}},
	{"postgres", []string{"postgres", "postgresql"}},
	{"redis", []string{"redis"}},
	{"aws", []string{"aws.http"}},
	{"mongodb", []string{"mongo"}},
	{"elasticsearch", []string{"elasticsearch"}},
	{"cassandra", []string{"cassandra"}},
	{"kafka", []string{"kafka"}},
	{"amqp", []string{"rabbitmq", "amqp"}},
	{"sqs", []string{"sqs"}},
	{"sns", []string{"sns"}},
	{"dns", []string{"dns"}},
	// HTTP is more generic and should be checked after more specific protocols
	{"http", []string{"http", "web", "rest", "api"}},
}

// inferProtocol attempts to infer the protocol from APM entity data
func inferProtocol(apmEntity *APMEntity) string {
	if apmEntity == nil || apmEntity.Attributes.Stats == nil {
		return "unknown"
	}

	operation := strings.ToLower(apmEntity.Attributes.Stats.Operation)
	spanKind := strings.ToLower(apmEntity.Attributes.Stats.SpanKind)

	// Check against protocol patterns in order (more specific first)
	for _, pattern := range protocolPatterns {
		for _, keyword := range pattern.keywords {
			if strings.Contains(operation, keyword) {
				return pattern.protocol
			}
		}
	}

	// Check span kind for generic protocols
	if spanKind == "server" || spanKind == "client" {
		return "http"
	}

	return "unknown"
}
