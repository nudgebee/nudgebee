package traces

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// NodeType represents the type of entity in the knowledge graph
type NodeType string

const (
	NodeTypeService         NodeType = "Service"
	NodeTypeDatabase        NodeType = "Database"
	NodeTypeMessageQueue    NodeType = "MessageQueue"
	NodeTypeQueue           NodeType = "Queue"
	NodeTypeCache           NodeType = "Cache"
	NodeTypeExternalService NodeType = "ExternalService"
	NodeTypeCluster         NodeType = "Cluster"
	NodeTypeNamespace       NodeType = "Namespace"
	NodeTypePod             NodeType = "Pod"
	NodeTypeNode            NodeType = "Node"
	// Cloud resource node types
	NodeTypeLoadBalancer   NodeType = "LoadBalancer"
	NodeTypeRDS            NodeType = "RDS"
	NodeTypeS3             NodeType = "S3"
	NodeTypeLambda         NodeType = "Lambda"
	NodeTypeEC2            NodeType = "EC2"
	NodeTypeDynamoDB       NodeType = "DynamoDB"
	NodeTypeVPC            NodeType = "VPC"
	NodeTypeSecurityGroup  NodeType = "SecurityGroup"
	NodeTypeRoute53        NodeType = "Route53"
	NodeTypeCloudFront     NodeType = "CloudFront"
	NodeTypeECR            NodeType = "ECR"
	NodeTypeSecretsManager NodeType = "SecretsManager"
	NodeTypeCloudWatch     NodeType = "CloudWatch"
	NodeTypeNATGateway     NodeType = "NATGateway"
	NodeTypeEKSCluster     NodeType = "EKSCluster"
	NodeTypeCloudResource  NodeType = "CloudResource"
)

// RelationshipType represents the type of relationship between nodes
type RelationshipType string

const (
	RelationshipCalls        RelationshipType = "CALLS"
	RelationshipPublishesTo  RelationshipType = "PUBLISHES_TO"
	RelationshipSubscribesTo RelationshipType = "SUBSCRIBES_TO"
	RelationshipRunsOn       RelationshipType = "RUNS_ON"
	// Cloud resource relationships
	RelationshipRoutesThrough RelationshipType = "ROUTES_THROUGH"
	RelationshipHostedOn      RelationshipType = "HOSTED_ON"
	RelationshipResolvesTo    RelationshipType = "RESOLVES_TO"
	RelationshipRoutesTo      RelationshipType = "ROUTES_TO"
)

// KnowledgeGraphNode represents a node in the knowledge graph
type KnowledgeGraphNode struct {
	ID             string         `json:"id"`
	NodeType       NodeType       `json:"node_type"`
	UniqueKey      string         `json:"unique_key"`
	Properties     map[string]any `json:"properties"`
	CloudAccountID string         `json:"cloud_account_id"`
	TenantID       string         `json:"tenant_id"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// KnowledgeGraphEdge represents an edge in the knowledge graph
type KnowledgeGraphEdge struct {
	ID                string           `json:"id"`
	SourceNodeID      string           `json:"source_node_id"`
	DestinationNodeID string           `json:"destination_node_id"`
	RelationshipType  RelationshipType `json:"relationship_type"`
	Properties        map[string]any   `json:"properties"`
	CloudAccountID    string           `json:"cloud_account_id"`
	TenantID          string           `json:"tenant_id"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

// TraceToKnowledgeGraphExtractor extracts nodes and edges from trace spans
type TraceToKnowledgeGraphExtractor struct {
	accountID string
	tenantID  string
}

// NewTraceToKnowledgeGraphExtractor creates a new extractor
func NewTraceToKnowledgeGraphExtractor(accountID, tenantID string) *TraceToKnowledgeGraphExtractor {
	return &TraceToKnowledgeGraphExtractor{
		accountID: accountID,
		tenantID:  tenantID,
	}
}

// ExtractFromTrace extracts nodes and edges from a single OpenTelemetryTrace
func (e *TraceToKnowledgeGraphExtractor) ExtractFromTrace(trace common.OpenTelemetryTrace) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {
	// Parse span attributes with resource attributes fallback
	attrs, err := e.parseSpanAttributes(trace.SpanAttributes, trace.ResourceAttributes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse span attributes: %w", err)
	}

	// Use trace.SpanKind if attrs.SpanKind is not set from attributes
	if attrs.SpanKind == "" && trace.SpanKind != "" {
		attrs.SpanKind = trace.SpanKind
	}

	var nodes []*KnowledgeGraphNode
	var edges []*KnowledgeGraphEdge

	// Extract source node (the calling service)
	sourceNode, err := e.extractSourceNodeFromTrace(trace, attrs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to extract source node: %w", err)
	}
	nodes = append(nodes, sourceNode)

	// Extract Kubernetes infrastructure nodes
	infraNodes, infraEdges := e.extractKubernetesInfrastructure(sourceNode, attrs)
	nodes = append(nodes, infraNodes...)
	edges = append(edges, infraEdges...)

	// Extract destination node (the called service/database/etc) - optional for inbound requests
	destNode, err := e.extractDestinationNodeFromTrace(trace, attrs, nil)
	if err == nil {
		nodes = append(nodes, destNode)

		// Create edge between source and destination
		edge, err := e.createEdgeFromTrace(sourceNode, destNode, trace, attrs)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create edge: %w", err)
		}
		edges = append(edges, edge)
	}

	return nodes, edges, nil
}

// ExtractFromTraceWithKnownServices extracts knowledge graph entities using known services context
func (e *TraceToKnowledgeGraphExtractor) ExtractFromTraceWithKnownServices(trace common.OpenTelemetryTrace, knownServices map[string]bool) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {
	// Parse span attributes with resource attributes fallback
	attrs, err := e.parseSpanAttributes(trace.SpanAttributes, trace.ResourceAttributes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse span attributes: %w", err)
	}

	// Use trace.SpanKind if attrs.SpanKind is not set from attributes
	if attrs.SpanKind == "" && trace.SpanKind != "" {
		attrs.SpanKind = trace.SpanKind
	}

	var nodes []*KnowledgeGraphNode
	var edges []*KnowledgeGraphEdge

	// Extract source node (the calling service)
	sourceNode, err := e.extractSourceNodeFromTrace(trace, attrs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to extract source node: %w", err)
	}
	nodes = append(nodes, sourceNode)

	// Extract Kubernetes infrastructure nodes
	infraNodes, infraEdges := e.extractKubernetesInfrastructure(sourceNode, attrs)
	nodes = append(nodes, infraNodes...)
	edges = append(edges, infraEdges...)

	// Extract destination node with known services context
	destNode, err := e.extractDestinationNodeFromTrace(trace, attrs, knownServices)
	if err == nil {
		nodes = append(nodes, destNode)

		// Create edge between source and destination
		edge, err := e.createEdgeFromTrace(sourceNode, destNode, trace, attrs)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create edge: %w", err)
		}
		edges = append(edges, edge)
	}

	return nodes, edges, nil
}

// extractSourceNodeFromTrace extracts the source node from trace
func (e *TraceToKnowledgeGraphExtractor) extractSourceNodeFromTrace(trace common.OpenTelemetryTrace, attrs *SpanAttributes) (*KnowledgeGraphNode, error) {
	serviceName := attrs.ServiceName
	if serviceName == "" {
		serviceName = trace.WorkloadName
	}
	if serviceName == "" {
		return nil, fmt.Errorf("no service name found in trace")
	}

	// Prefer k8s.deployment.name from trace attributes (most accurate)
	// This avoids creating separate nodes for individual pod replicas
	if attrs.RawAttributes != nil {
		// Priority 1: k8s.deployment.name (for Deployments)
		if deploymentName := attrs.RawAttributes["k8s.deployment.name"]; deploymentName != "" {
			serviceName = deploymentName
		} else if statefulsetName := attrs.RawAttributes["k8s.statefulset.name"]; statefulsetName != "" {
			// Priority 2: k8s.statefulset.name (for StatefulSets)
			serviceName = statefulsetName
		} else if daemonsetName := attrs.RawAttributes["k8s.daemonset.name"]; daemonsetName != "" {
			// Priority 3: k8s.daemonset.name (for DaemonSets)
			serviceName = daemonsetName
		} else if replicasetName := attrs.RawAttributes["k8s.replicaset.name"]; replicasetName != "" {
			// Priority 4: k8s.replicaset.name (for ReplicaSets, though usually part of Deployment)
			serviceName = replicasetName
		} else if podName := attrs.RawAttributes["k8s.pod.name"]; podName != "" && podName == serviceName {
			// Priority 5: Fallback to regex-based extraction from pod name
			serviceName = extractWorkloadFromPodName(serviceName)
		}
	}

	environment := attrs.DeploymentEnv
	// Keep environment empty if not found

	uniqueKey := fmt.Sprintf("Service:%s:%s", serviceName, environment)

	properties := map[string]any{
		"name":        serviceName,
		"environment": environment,
	}

	// Add cluster information if available
	if attrs.K8sCluster != "" {
		properties["cluster"] = attrs.K8sCluster
	}

	return &KnowledgeGraphNode{
		NodeType:       NodeTypeService,
		UniqueKey:      uniqueKey,
		Properties:     properties,
		CloudAccountID: e.accountID,
		TenantID:       e.tenantID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}, nil
}

// extractDestinationNodeFromTrace extracts the destination node from trace
// If knownServices is provided, it uses enhanced classification; otherwise uses pattern-based detection
func (e *TraceToKnowledgeGraphExtractor) extractDestinationNodeFromTrace(trace common.OpenTelemetryTrace, attrs *SpanAttributes, knownServices map[string]bool) (*KnowledgeGraphNode, error) {
	// Only process CLIENT spans (outbound calls) to avoid duplicate relationships
	// If span.kind is missing, we rely on the isInboundRequest check below
	if attrs.SpanKind != "" && attrs.SpanKind != "CLIENT" && attrs.SpanKind != "PRODUCER" {
		return nil, fmt.Errorf("skipping non-client span (kind: %s) - not an outbound call", attrs.SpanKind)
	}

	// Skip inbound HTTP requests - they don't have outbound dependencies
	if e.isInboundRequest(attrs) {
		return nil, fmt.Errorf("skipping inbound request - no outbound dependency")
	}

	nodeType := e.classifyDestinationNodeFromTrace(attrs, knownServices)

	var name, uniqueKey string
	properties := make(map[string]any)

	switch nodeType {
	case NodeTypeDatabase, NodeTypeCache:
		// Database or cache system
		if attrs.DBSystem == "" {
			return nil, fmt.Errorf("db.system attribute missing for database span")
		}

		name = attrs.NetPeerName
		if name == "" {
			name = attrs.DBHost
		}
		if name == "" {
			// Fallback to net.sock.peer.name (used by some instrumentation libraries)
			name = attrs.RawAttributes["net.sock.peer.name"]
		}
		if name == "" {
			return nil, fmt.Errorf("no database name found")
		}

		uniqueKey = fmt.Sprintf("%s:%s", nodeType, name)
		properties["name"] = name
		properties["system"] = attrs.DBSystem

		if portStr := attrs.RawAttributes["net.peer.port"]; portStr != "" {
			properties["port"] = portStr
		} else if portStr := attrs.RawAttributes["net.sock.peer.port"]; portStr != "" {
			properties["port"] = portStr
		}

	case NodeTypeMessageQueue:
		// Message queue system
		// For message queues, prefer messaging.destination (topic/queue name)
		name = attrs.MessagingDestination
		if name == "" {
			name = attrs.NetPeerName
		}
		if name == "" {
			return nil, fmt.Errorf("no message queue name found")
		}

		uniqueKey = fmt.Sprintf("MessageQueue:%s", name)
		properties["name"] = name
		// messaging systems can be in messaging.system or db.system
		if attrs.MessagingSystem != "" {
			properties["system"] = attrs.MessagingSystem
		} else if attrs.DBSystem != "" {
			properties["system"] = attrs.DBSystem
		}

	case NodeTypeExternalService:
		// External service
		name = attrs.NetPeerName
		if name == "" {
			// Try other fields for external service identification
			if httpHost := attrs.RawAttributes["net.host.name"]; httpHost != "" {
				name = httpHost
			} else if httpTarget := attrs.RawAttributes["http.target"]; httpTarget != "" {
				// For inbound requests, use the target path as identifier
				name = "inbound:" + httpTarget
			}
		}

		if name == "" {
			return nil, fmt.Errorf("no external service name found")
		}

		uniqueKey = fmt.Sprintf("ExternalService:%s", name)
		properties["name"] = name
		if attrs.HTTPMethod != "" {
			properties["protocol"] = "http"
		}

	case NodeTypeService:
		// Internal service call
		name = attrs.NetPeerName
		if name == "" {
			name = trace.DestinationName
		}
		if name == "" {
			return nil, fmt.Errorf("no destination service name found")
		}

		environment := attrs.DeploymentEnv
		// Keep environment empty if not specified

		uniqueKey = fmt.Sprintf("Service:%s:%s", name, environment)
		properties["name"] = name
		properties["environment"] = environment

	default:
		return nil, fmt.Errorf("unsupported node type: %s", nodeType)
	}

	return &KnowledgeGraphNode{
		NodeType:       nodeType,
		UniqueKey:      uniqueKey,
		Properties:     properties,
		CloudAccountID: e.accountID,
		TenantID:       e.tenantID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}, nil
}

// classifyDestinationNodeFromTrace determines the node type for the destination
// If knownServices is provided, it uses that for enhanced classification; otherwise falls back to pattern-based detection
func (e *TraceToKnowledgeGraphExtractor) classifyDestinationNodeFromTrace(attrs *SpanAttributes, knownServices map[string]bool) NodeType {
	// Database systems - same logic regardless of knownServices
	if attrs.DBSystem != "" {
		switch strings.ToLower(attrs.DBSystem) {
		case "redis":
			return NodeTypeCache
		case "postgresql", "mysql", "mongodb", "elasticsearch", "sqlite":
			return NodeTypeDatabase
		case "kafka", "rabbitmq", "sqs", "amqp":
			return NodeTypeMessageQueue
		default:
			return NodeTypeDatabase
		}
	}

	// HTTP calls - use enhanced logic if knownServices is provided
	if attrs.HTTPMethod != "" || attrs.NetPeerName != "" {
		peerName := attrs.NetPeerName

		// If we have known services context, use it for more accurate classification
		if knownServices != nil {
			// First, check if this is a known service from our trace analysis
			if peerName != "" && knownServices[peerName] {
				return NodeTypeService
			}

			// Then check internal domain patterns as backup
			if e.isInternalDomain(peerName) {
				return NodeTypeService
			}

			// If we reach here, it's likely external
			return NodeTypeExternalService
		} else {
			// Fallback to pattern-based detection when no known services provided
			if e.isInternalDomain(peerName) {
				return NodeTypeService
			}
			return NodeTypeExternalService
		}
	}

	// Default to service for internal calls
	return NodeTypeService
}

// isInternalDomain checks if a hostname is internal to the organization
func (e *TraceToKnowledgeGraphExtractor) isInternalDomain(hostname string) bool {
	if hostname == "" {
		return false
	}

	internalSuffixes := []string{
		".internal",
		".local",
		".svc.cluster.local",
		".fourkites.internal",
		".nudgebee.internal",
	}

	hostname = strings.ToLower(hostname)
	for _, suffix := range internalSuffixes {
		if strings.HasSuffix(hostname, suffix) {
			return true
		}
	}

	// Check for localhost and internal IP ranges
	if strings.HasPrefix(hostname, "localhost") ||
		strings.HasPrefix(hostname, "127.") ||
		strings.HasPrefix(hostname, "10.") ||
		strings.HasPrefix(hostname, "192.168.") ||
		strings.Contains(hostname, "172.") {
		return true
	}

	// Check for internal Kubernetes service patterns
	// Services like "relay-server", "services-server" without domain should be internal
	if e.isLikelyKubernetesService(hostname) {
		return true
	}

	return false
}

// isLikelyKubernetesService checks if a hostname looks like a Kubernetes service
// This helps avoid creating duplicate Service/ExternalService nodes
func (e *TraceToKnowledgeGraphExtractor) isLikelyKubernetesService(hostname string) bool {
	if hostname == "" {
		return false
	}

	// Skip if it looks like an external domain (contains dots and TLD)
	if strings.Contains(hostname, ".") {
		// Check if it's a real domain (ends with common TLD)
		commonTLDs := []string{".com", ".org", ".net", ".io", ".co", ".dev", ".app"}
		for _, tld := range commonTLDs {
			if strings.HasSuffix(hostname, tld) {
				return false // This looks like an external domain
			}
		}
	}

	// If it's a simple name without dots or a local k8s FQDN, treat as internal
	if !strings.Contains(hostname, ".") || strings.Contains(hostname, ".svc.cluster") {
		return true
	}

	// Check for service naming patterns common in microservices
	servicePatterns := []string{
		"-server", "-service", "-api", "-app", "-worker",
		"server-", "service-", "api-", "app-", "worker-",
	}

	for _, pattern := range servicePatterns {
		if strings.Contains(hostname, pattern) {
			return true
		}
	}

	return false
}

// isInboundRequest checks if this trace represents an inbound HTTP request (not an outbound call)
func (e *TraceToKnowledgeGraphExtractor) isInboundRequest(attrs *SpanAttributes) bool {
	// If it has net.host.name but no net.peer.name, it's likely an inbound request
	if attrs.RawAttributes["net.host.name"] != "" && attrs.NetPeerName == "" {
		return true
	}

	// If it has http.target but no database or external service indicators, it's inbound
	if attrs.RawAttributes["http.target"] != "" && attrs.DBSystem == "" && attrs.NetPeerName == "" {
		return true
	}

	return false
}

// extractKubernetesInfrastructure extracts K8s infrastructure nodes and relationships from trace data
func (e *TraceToKnowledgeGraphExtractor) extractKubernetesInfrastructure(serviceNode *KnowledgeGraphNode, attrs *SpanAttributes) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge) {
	var nodes []*KnowledgeGraphNode
	var edges []*KnowledgeGraphEdge

	serviceName, ok := serviceNode.Properties["name"].(string)
	if !ok || serviceName == "" {
		slog.Warn("Skipping Kubernetes infrastructure extraction for service node with invalid name",
			"node_id", serviceNode.ID, "properties", serviceNode.Properties)
		return nodes, edges
	}

	// Extract namespace from span attributes
	namespace := "default" // Default Kubernetes namespace

	// Try to get namespace from span attributes
	if attrs.RawAttributes != nil {
		if ns, exists := attrs.RawAttributes["k8s.namespace.name"]; exists && ns != "" {
			namespace = ns
		}
	}

	// Create Namespace node using actual trace data
	namespaceNode := &KnowledgeGraphNode{
		NodeType:  NodeTypeNamespace,
		UniqueKey: fmt.Sprintf("Namespace:%s", namespace),
		Properties: map[string]any{
			"name": namespace,
		},
		CloudAccountID: e.accountID,
		TenantID:       e.tenantID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	nodes = append(nodes, namespaceNode)

	// Extract cluster from span attributes
	clusterName := attrs.K8sCluster
	if clusterName == "" {
		// Extract from raw attributes if structured field is empty
		if rawCluster, exists := attrs.RawAttributes["k8s.cluster.name"]; exists {
			clusterName = rawCluster
		}
	}
	// Create Cluster node only if we have cluster information
	var clusterNode *KnowledgeGraphNode
	if clusterName != "" {
		clusterNode = &KnowledgeGraphNode{
			NodeType:  NodeTypeCluster,
			UniqueKey: fmt.Sprintf("Cluster:%s", clusterName),
			Properties: map[string]any{
				"name": clusterName,
			},
			CloudAccountID: e.accountID,
			TenantID:       e.tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		nodes = append(nodes, clusterNode)
	}

	// Extract Pod information from span attributes
	podName := ""
	if attrs.RawAttributes != nil {
		if pod, exists := attrs.RawAttributes["k8s.pod.name"]; exists && pod != "" {
			podName = pod
		}
	}

	// NOTE: Pod nodes are no longer created here to avoid graph explosion
	// Since we now use k8s.deployment.name for service nodes (see extractSourceNodeFromTrace),
	// individual pod replicas are already aggregated at the workload level
	// Pod metadata is still accessible through service node properties if needed
	var podNode *KnowledgeGraphNode
	// Disabled pod node creation - keeping var for compatibility with edge creation below
	_ = podName // Mark as used to avoid compiler warning

	// Extract Node information from resource attributes
	var nodeNode *KnowledgeGraphNode
	nodeName := ""
	if attrs.RawAttributes != nil {
		if node, exists := attrs.RawAttributes["k8s.node.name"]; exists && node != "" {
			nodeName = node
		}
	}

	if nodeName != "" {
		nodeNode = &KnowledgeGraphNode{
			NodeType:  NodeTypeNode,
			UniqueKey: fmt.Sprintf("Node:%s", nodeName),
			Properties: map[string]any{
				"name": nodeName,
			},
			CloudAccountID: e.accountID,
			TenantID:       e.tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		nodes = append(nodes, nodeNode)

		// Pod RUNS_ON Node (only if both pod and node exist)
		if podNode != nil {
			podToNode := &KnowledgeGraphEdge{
				SourceNodeID:      podNode.UniqueKey,
				DestinationNodeID: nodeNode.UniqueKey,
				RelationshipType:  RelationshipRunsOn,
				Properties: map[string]any{
					"connection_type": "infrastructure",
					"first_seen":      time.Now(),
					"last_seen":       time.Now(),
				},
				CloudAccountID: e.accountID,
				TenantID:       e.tenantID,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			edges = append(edges, podToNode)
		}

		// Node RUNS_ON Cluster (only if both node and cluster exist)
		if clusterNode != nil {
			nodeToCluster := &KnowledgeGraphEdge{
				SourceNodeID:      nodeNode.UniqueKey,
				DestinationNodeID: clusterNode.UniqueKey,
				RelationshipType:  RelationshipRunsOn,
				Properties: map[string]any{
					"connection_type": "infrastructure",
					"first_seen":      time.Now(),
					"last_seen":       time.Now(),
				},
				CloudAccountID: e.accountID,
				TenantID:       e.tenantID,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			edges = append(edges, nodeToCluster)
		}
	}

	// Service RUNS_ON Namespace
	serviceToNamespace := &KnowledgeGraphEdge{
		SourceNodeID:      serviceNode.UniqueKey,
		DestinationNodeID: namespaceNode.UniqueKey,
		RelationshipType:  RelationshipRunsOn,
		Properties: map[string]any{
			"connection_type": "infrastructure",
			"first_seen":      time.Now(),
			"last_seen":       time.Now(),
		},
		CloudAccountID: e.accountID,
		TenantID:       e.tenantID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	edges = append(edges, serviceToNamespace)

	// Service RUNS_ON Pod (only if pod exists)
	if podNode != nil {
		serviceToPod := &KnowledgeGraphEdge{
			SourceNodeID:      serviceNode.UniqueKey,
			DestinationNodeID: podNode.UniqueKey,
			RelationshipType:  RelationshipRunsOn,
			Properties: map[string]any{
				"connection_type": "infrastructure",
				"first_seen":      time.Now(),
				"last_seen":       time.Now(),
			},
			CloudAccountID: e.accountID,
			TenantID:       e.tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, serviceToPod)

		// Pod RUNS_ON Namespace
		podToNamespace := &KnowledgeGraphEdge{
			SourceNodeID:      podNode.UniqueKey,
			DestinationNodeID: namespaceNode.UniqueKey,
			RelationshipType:  RelationshipRunsOn,
			Properties: map[string]any{
				"connection_type": "infrastructure",
				"first_seen":      time.Now(),
				"last_seen":       time.Now(),
			},
			CloudAccountID: e.accountID,
			TenantID:       e.tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, podToNamespace)

		// Pod RUNS_ON Cluster (only if cluster exists)
		if clusterNode != nil {
			podToCluster := &KnowledgeGraphEdge{
				SourceNodeID:      podNode.UniqueKey,
				DestinationNodeID: clusterNode.UniqueKey,
				RelationshipType:  RelationshipRunsOn,
				Properties: map[string]any{
					"connection_type": "infrastructure",
					"first_seen":      time.Now(),
					"last_seen":       time.Now(),
				},
				CloudAccountID: e.accountID,
				TenantID:       e.tenantID,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			edges = append(edges, podToCluster)
		}
	}

	// Service RUNS_ON Cluster (only if cluster exists and no pod)
	if clusterNode != nil && podNode == nil {
		serviceToCluster := &KnowledgeGraphEdge{
			SourceNodeID:      serviceNode.UniqueKey,
			DestinationNodeID: clusterNode.UniqueKey,
			RelationshipType:  RelationshipRunsOn,
			Properties: map[string]any{
				"connection_type": "infrastructure",
				"first_seen":      time.Now(),
				"last_seen":       time.Now(),
			},
			CloudAccountID: e.accountID,
			TenantID:       e.tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, serviceToCluster)
	}

	return nodes, edges
}

// Package-level compiled regexes for pod-name workload extraction.
// Hoisting these out of extractWorkloadFromPodName avoids re-compiling 4 regexes
// on every call — that function is on the hot path for trace-to-knowledge-graph
// extraction (once per trace span) and service-map builds. Each MustCompile
// allocates the NFA/DFA and supporting data structures; benchmark shows
// ~12.7µs / 14KB / 120 allocs per call before, which collapses to ~1µs /
// 25B / 0 allocs per call after this change (~12× faster, alloc-free).
var (
	// StatefulSet pattern: name-0, name-1, etc.
	podStatefulSetRegex = regexp.MustCompile(`^(.+)-\d+$`)
	// Deployment/ReplicaSet pattern: name-hash-hash
	podDeploymentRegex = regexp.MustCompile(`^(.+)-[a-z0-9]{5,10}-[a-z0-9]{5}$`)
	// Job pattern: name-hash (5-10 chars)
	podJobRegex = regexp.MustCompile(`^(.+)-[a-z0-9]{5,10}$`)
	// CronJob pattern: name-timestamp-hash
	podCronJobRegex = regexp.MustCompile(`^(.+)-\d{8,10}-[a-z0-9]{5}$`)
)

// extractWorkloadFromPodName extracts the workload name from a Kubernetes pod name
// Common patterns:
// - Deployment: deployment-name-7b7d7f9f9d-abcde -> deployment-name
// - ReplicaSet: replicaset-name-abcde -> replicaset-name
// - StatefulSet: statefulset-name-0 -> statefulset-name
// - DaemonSet: daemonset-name-abcde -> daemonset-name
// - Job: job-name-12345 -> job-name
// - CronJob: cronjob-name-123456789-abcde -> cronjob-name
func extractWorkloadFromPodName(podName string) string {
	if podName == "" {
		return ""
	}

	// StatefulSet pattern: name-0, name-1, etc.
	if matches := podStatefulSetRegex.FindStringSubmatch(podName); len(matches) > 1 {
		return matches[1]
	}

	// Deployment/ReplicaSet pattern: name-hash-hash
	if matches := podDeploymentRegex.FindStringSubmatch(podName); len(matches) > 1 {
		return matches[1]
	}

	// Job pattern: name-hash (5-10 chars)
	if matches := podJobRegex.FindStringSubmatch(podName); len(matches) > 1 {
		return matches[1]
	}

	// CronJob pattern: name-timestamp-hash
	if matches := podCronJobRegex.FindStringSubmatch(podName); len(matches) > 1 {
		return matches[1]
	}

	// If no pattern matches, return the original pod name
	return podName
}

// createEdgeFromTrace creates an edge between source and destination nodes with trace metrics
func (e *TraceToKnowledgeGraphExtractor) createEdgeFromTrace(sourceNode, destNode *KnowledgeGraphNode, trace common.OpenTelemetryTrace, attrs *SpanAttributes) (*KnowledgeGraphEdge, error) {
	relationshipType := e.determineRelationshipType(destNode.NodeType, trace, attrs)

	properties := map[string]any{
		"first_seen": time.Now(),
		"last_seen":  time.Now(),
	}

	// Add connection type for better categorization
	switch destNode.NodeType {
	case NodeTypeDatabase:
		properties["connection_type"] = "database"
	case NodeTypeCache:
		properties["connection_type"] = "cache"
	case NodeTypeMessageQueue:
		properties["connection_type"] = "messaging"
	case NodeTypeExternalService:
		properties["connection_type"] = "external_api"
	case NodeTypeService:
		properties["connection_type"] = "service"
	}

	// Add trace-specific metrics
	properties["call_count"] = int64(1)  // Each edge represents one trace call
	properties["error_count"] = int64(0) // Default to no errors

	// Add duration if available
	if trace.DurationNs > 0 {
		properties["total_duration_ns"] = float64(trace.DurationNs)
		properties["avg_duration_ms"] = float64(trace.DurationNs) / 1000000.0
	}

	// Add protocol information
	protocol := "Unknown"
	if attrs.HTTPMethod != "" {
		protocol = "HTTP"
	} else if attrs.DBSystem != "" {
		protocol = strings.ToUpper(attrs.DBSystem)
	} else if attrs.MessagingSystem != "" {
		protocol = strings.ToUpper(attrs.MessagingSystem)
	}
	properties["protocol"] = protocol

	// Add operation/span name
	if trace.SpanName != "" {
		properties["operation"] = trace.SpanName
	}

	// Add HTTP status code if available
	if attrs.RawAttributes != nil {
		if statusCode, exists := attrs.RawAttributes["http.status_code"]; exists && statusCode != "" {
			properties["http_status_code"] = statusCode
			// Check for errors based on status code
			if statusCodeInt, err := strconv.Atoi(statusCode); err == nil {
				if statusCodeInt >= 400 {
					properties["error_count"] = int64(1)
					if statusCodeInt >= 500 {
						properties["error_type"] = "HTTP_5XX_ERROR"
					} else {
						properties["error_type"] = "HTTP_4XX_ERROR"
					}
				}
			}
		}
	}

	// Check span status for errors
	if trace.StatusCode != "" && trace.StatusCode != "OK" && trace.StatusCode != "UNSET" {
		properties["error_count"] = int64(1)
		properties["status_code"] = trace.StatusCode
		if trace.StatusMessage != "" {
			properties["status_message"] = trace.StatusMessage
		}
	}

	// Add trace ID for drill-down capability
	properties["trace_id"] = trace.TraceID

	// For consumer operations (SUBSCRIBES_TO), reverse the edge direction
	// Consumer: Topic → Service (topic triggers service)
	// Producer: Service → Topic (service calls topic)
	var edgeSource, edgeDest string
	if relationshipType == RelationshipSubscribesTo {
		// Consumer: edge goes from topic to service
		edgeSource = destNode.UniqueKey
		edgeDest = sourceNode.UniqueKey
	} else {
		// Normal case: edge goes from service to destination
		edgeSource = sourceNode.UniqueKey
		edgeDest = destNode.UniqueKey
	}

	return &KnowledgeGraphEdge{
		SourceNodeID:      edgeSource,
		DestinationNodeID: edgeDest,
		RelationshipType:  relationshipType,
		Properties:        properties,
		CloudAccountID:    e.accountID,
		TenantID:          e.tenantID,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}, nil
}

// isConsumerOperation detects if a span represents a message consumer/processor operation
// Returns true for consumers (topic triggers service), false for producers (service sends to topic)
func (e *TraceToKnowledgeGraphExtractor) isConsumerOperation(trace common.OpenTelemetryTrace, attrs *SpanAttributes) bool {
	// Check if this is a messaging system operation
	if attrs.MessagingSystem == "" && attrs.DBSystem == "" {
		return false
	}

	// Check span.kind: CONSUMER indicates the service is consuming from the topic
	if strings.EqualFold(attrs.SpanKind, "CONSUMER") {
		return true
	}

	// Check span.kind: PRODUCER/CLIENT indicates the service is sending to the topic
	if strings.EqualFold(attrs.SpanKind, "PRODUCER") || strings.EqualFold(attrs.SpanKind, "CLIENT") {
		return false
	}

	// Fallback: Check span name patterns
	// "Kafka topic XXX send" = producer
	// "Kafka topic XXX process" / "Kafka topic XXX receive" = consumer
	spanName := strings.ToLower(trace.SpanName)
	if strings.Contains(spanName, "process") || strings.Contains(spanName, "receive") || strings.Contains(spanName, "consume") {
		return true
	}
	if strings.Contains(spanName, "send") || strings.Contains(spanName, "produce") || strings.Contains(spanName, "publish") {
		return false
	}

	// Check messaging.operation attribute if available
	if msgOp := attrs.RawAttributes["messaging.operation"]; msgOp != "" {
		msgOpLower := strings.ToLower(msgOp)
		if msgOpLower == "receive" || msgOpLower == "process" || msgOpLower == "consume" {
			return true
		}
		if msgOpLower == "send" || msgOpLower == "publish" || msgOpLower == "create" {
			return false
		}
	}

	// Default: treat as producer (service calling destination)
	return false
}

// determineRelationshipType determines the relationship type between nodes
func (e *TraceToKnowledgeGraphExtractor) determineRelationshipType(destType NodeType, trace common.OpenTelemetryTrace, attrs *SpanAttributes) RelationshipType {
	switch destType {
	case NodeTypeDatabase, NodeTypeCache:
		return RelationshipCalls // Database/cache interactions are calls
	case NodeTypeMessageQueue:
		// Determine if publishing or subscribing based on span attributes
		if e.isConsumerOperation(trace, attrs) {
			return RelationshipSubscribesTo
		}
		return RelationshipPublishesTo
	case NodeTypeService, NodeTypeExternalService:
		return RelationshipCalls
	case NodeTypeCluster, NodeTypeNamespace:
		return RelationshipRunsOn
	default:
		return RelationshipCalls
	}
}

// parseSpanAttributes parses the span attributes with resource attributes fallback
func (e *TraceToKnowledgeGraphExtractor) parseSpanAttributes(spanAttrs any, resourceAttrs any) (*SpanAttributes, error) {
	// Start with resource attributes as base (fallback)
	attrMap := make(map[string]string)

	// First, parse resource attributes if available
	if resourceAttrs != nil {
		resourceMap, err := e.convertToStringMap(resourceAttrs)
		if err == nil {
			// Copy resource attributes to the base map
			for k, v := range resourceMap {
				attrMap[k] = v
			}
		}
	}

	// Then parse span attributes (they take precedence over resource attributes)
	if spanAttrs != nil {
		spanMap, err := e.convertToStringMap(spanAttrs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse span attributes: %w", err)
		}

		// Override/add span attributes on top of resource attributes
		for k, v := range spanMap {
			attrMap[k] = v
		}
	}

	// Create a flexible attributes structure that includes extra fields
	attrs := &SpanAttributes{
		ServiceName:          attrMap["service.name"],
		SpanKind:             attrMap["span.kind"],
		DBSystem:             attrMap["db.system"],
		DBHost:               attrMap["db.host"],
		DBName:               attrMap["db.name"],
		MessagingSystem:      attrMap["messaging.system"],
		MessagingDestination: attrMap["messaging.destination"],
		NetPeerName:          attrMap["net.peer.name"],
		HTTPMethod:           attrMap["http.method"],
		HTTPRoute:            attrMap["http.route"],
		DeploymentEnv:        attrMap["deployment.environment"],
		K8sCluster:           getClusterName(attrMap),
	}

	// Store the raw attribute map for access to fields we don't have in the struct
	attrs.RawAttributes = attrMap

	return attrs, nil
}

// convertToStringMap converts various attribute formats to map[string]string
func (e *TraceToKnowledgeGraphExtractor) convertToStringMap(attrs any) (map[string]string, error) {
	if attrs == nil {
		return make(map[string]string), nil
	}

	// Try direct cast first
	if strMap, ok := attrs.(map[string]string); ok {
		return strMap, nil
	}

	// Try to convert through JSON marshaling
	jsonBytes, err := json.Marshal(attrs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal attributes: %w", err)
	}

	var tempMap map[string]any
	if err := json.Unmarshal(jsonBytes, &tempMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to temp map: %w", err)
	}

	// Convert all values to strings
	stringMap := make(map[string]string)
	for k, v := range tempMap {
		stringMap[k] = fmt.Sprintf("%v", v)
	}

	return stringMap, nil
}

// getClusterName extracts cluster name from various possible attribute keys
func getClusterName(attrMap map[string]string) string {
	// Try different possible cluster attribute keys in order of preference
	clusterKeys := []string{
		"k8s.cluster.name",
		"cluster.name",
		"cluster",
		"k8s_cluster",
	}

	for _, key := range clusterKeys {
		if cluster := attrMap[key]; cluster != "" {
			return cluster
		}
	}

	return ""
}

// BuildKnowledgeGraphFromTraces queries traces and builds knowledge graph using service map logic
func BuildKnowledgeGraphFromTraces(requestContext *security.RequestContext, serviceName, cloudAccountID, tenantID string) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {
	startTime := time.Now()
	slog.Info("Starting BuildKnowledgeGraphFromTraces",
		"service_name", serviceName,
		"account_id", cloudAccountID,
		"tenant_id", tenantID)

	if requestContext == nil {
		return nil, nil, fmt.Errorf("request context is required")
	}

	if cloudAccountID == "" {
		return nil, nil, fmt.Errorf("cloud account ID is required")
	}

	if tenantID == "" {
		return nil, nil, fmt.Errorf("tenant ID is required")
	}

	// Set time range for trace query (last 15 minutes by default)
	endTime := time.Now()
	startTimeQuery := endTime.Add(-15 * time.Minute)

	// Create trace query parameters
	params := TraceQueryParams{
		AccountID:         cloudAccountID,
		StartTime:         startTimeQuery,
		EndTime:           endTime,
		WorkloadName:      serviceName, // Filter by specific service if provided
		WorkloadNamespace: "",          // Could be parameterized later
		LabelFilters: []LabelFilter{
			{
				Key:      "deployment.environment",
				Operator: query.Eq,
				Value:    "staging",
			},
		},
	}

	// Step 1: Use existing service map logic to discover all service relationships
	// This correctly handles parent-child relationships, external services, etc.
	serviceMap, err := FetchTracesAndBuildServiceMap(requestContext, params)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build service map: %w", err)
	}

	// Step 2: Convert service map to knowledge graph nodes and edges
	// This now includes K8s infrastructure extracted during service map building
	nodes, edges := ConvertServiceMapToKnowledgeGraph(serviceMap, cloudAccountID, tenantID)

	// Step 3: Enrich with cloud resources (ALB, RDS, ElastiCache, etc.)
	enrichedNodes, enrichedEdges, err := EnrichWithCloudResources(requestContext, nodes, edges, cloudAccountID, tenantID)
	if err != nil {
		slog.Warn("Failed to enrich with cloud resources", "error", err)
		// Continue with original nodes/edges if enrichment fails
		enrichedNodes = nodes
		enrichedEdges = edges
	}

	// Step 4: Link LoadBalancers to backend services using DNS and http.host matching
	finalNodes, finalEdges := LinkLoadBalancersToBackendServices(enrichedNodes, enrichedEdges, cloudAccountID, tenantID)

	elapsed := time.Since(startTime)
	slog.Info("Completed BuildKnowledgeGraphFromTraces",
		"duration_ms", elapsed.Milliseconds(),
		"nodes_count", len(finalNodes),
		"edges_count", len(finalEdges))

	return finalNodes, finalEdges, nil
}

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

// EnrichWithCloudResources matches external services with cloud resources and adds cloud resource nodes
func EnrichWithCloudResources(requestContext *security.RequestContext, nodes []*KnowledgeGraphNode, edges []*KnowledgeGraphEdge, cloudAccountID, tenantID string) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {
	slog.Info("Starting cloud resource enrichment", "external_services_count", countExternalServices(nodes))
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nodes, edges, fmt.Errorf("failed to get database manager: %w", err)
	}
	db := dbManager.Db

	// Collect all external service names that might be DNS names
	externalServiceNames := make([]string, 0)
	externalServiceNodeMap := make(map[string]*KnowledgeGraphNode)

	for _, node := range nodes {
		if node.NodeType == NodeTypeExternalService {
			if name, ok := node.Properties["name"].(string); ok && name != "" {
				externalServiceNames = append(externalServiceNames, name)
				externalServiceNodeMap[name] = node
			}
		}
	}

	slog.Info("External services found for potential cloud enrichment",
		"count", len(externalServiceNames),
		"services", externalServiceNames)

	if len(externalServiceNames) == 0 {
		slog.Info("No external services to enrich")
		return nodes, edges, nil
	}

	// Step 2.5: Resolve DNS names via Route 53 to discover AWS endpoints BEFORE querying cloud_resources
	// This allows us to query cloud_resources with the actual AWS endpoints (e.g., staging-redis-cache-v6.xxx.cache.amazonaws.com)
	// instead of just the DNS names (e.g., staging-redis.fourkites.internal)
	dnsResolutions := make(map[string]string) // Map: DNS name -> AWS endpoint
	awsAccountIDs, err := GetAWSAccountsForTenant(tenantID)
	if err != nil {
		slog.Warn("Failed to get AWS accounts for DNS resolution", "error", err)
		awsAccountIDs = []string{}
	}

	if len(awsAccountIDs) > 0 {
		slog.Info("Resolving DNS names via Route 53 before cloud resource lookup",
			"hostnames", len(externalServiceNames),
			"aws_accounts", len(awsAccountIDs))

		resolvedHostnames := make([]string, 0)
		unresolvedHostnames := make([]string, 0)

		for _, hostname := range externalServiceNames {
			// Skip Kubernetes internal DNS names (they're not external services)
			if isKubernetesInternalDNS(hostname) {
				slog.Debug("Skipping Kubernetes internal DNS - not an external service",
					"hostname", hostname)
				continue
			}

			resolved := false
			for _, awsAccountID := range awsAccountIDs {
				endpoint, err := ResolveRoute53DNS(requestContext, hostname, awsAccountID)
				if err != nil {
					slog.Info("Route 53 resolution failed",
						"hostname", hostname,
						"aws_account", awsAccountID,
						"error", err)
					continue
				}
				if endpoint != "" {
					dnsResolutions[hostname] = endpoint
					resolvedHostnames = append(resolvedHostnames, hostname)
					slog.Info("Route 53 DNS resolved successfully",
						"hostname", hostname,
						"endpoint", endpoint,
						"aws_account", awsAccountID)
					resolved = true
					break // Stop trying other accounts once we find a match
				} else {
					slog.Info("Route 53 resolution returned empty endpoint",
						"hostname", hostname,
						"aws_account", awsAccountID)
				}
			}
			if !resolved {
				unresolvedHostnames = append(unresolvedHostnames, hostname)
			}
		}

		slog.Info("Route 53 DNS resolution completed",
			"total_hostnames", len(externalServiceNames),
			"resolved_count", len(dnsResolutions),
			"resolved_hostnames", resolvedHostnames,
			"unresolved_count", len(unresolvedHostnames),
			"unresolved_hostnames", unresolvedHostnames)
	}

	// Build combined list of names to query: original DNS names + discovered AWS endpoints
	namesToQuery := make([]string, 0, len(externalServiceNames)+len(dnsResolutions))
	namesToQuery = append(namesToQuery, externalServiceNames...)
	for _, endpoint := range dnsResolutions {
		namesToQuery = append(namesToQuery, endpoint)
	}

	slog.Info("Querying cloud_resources with DNS names and discovered endpoints",
		"original_names", len(externalServiceNames),
		"discovered_endpoints", len(dnsResolutions),
		"total_to_query", len(namesToQuery))

	// Query cloud_resources table to find matching resources
	// Join with cloud_accounts to get the AWS account number
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
			AND (
				-- Match LoadBalancers by DNSName
				(cr.type IN ('application_loadbalancer', 'network_loadbalancer', 'classic_loadbalancer')
					AND cr.meta->>'DNSName' = ANY($2))
				-- Match RDS instances by Endpoint Address
				OR (cr.type = 'rds_instance'
					AND cr.meta->'Endpoint'->>'Address' = ANY($2))
				-- Match ElastiCache clusters by ConfigurationEndpoint or ReaderEndpoint
				-- Also check nested NodeGroups and CacheNodes for replica endpoints
				OR (cr.type IN ('elasticache_cluster', 'elasticache_replication_group')
					AND (
						cr.meta->'ConfigurationEndpoint'->>'Address' = ANY($2)
						OR cr.meta->'ReaderEndpoint'->>'Address' = ANY($2)
						OR cr.meta->'PrimaryEndpoint'->>'Address' = ANY($2)
						OR cr.meta->>'ReaderEndpoint' = ANY($2)
						OR cr.meta->>'PrimaryEndpoint' = ANY($2)
						OR EXISTS (
							SELECT 1 FROM jsonb_array_elements(cr.meta->'NodeGroups') AS ng
							WHERE ng->'PrimaryEndpoint'->>'Address' = ANY($2)
								OR ng->'ReaderEndpoint'->>'Address' = ANY($2)
						)
						OR EXISTS (
							SELECT 1 FROM jsonb_array_elements(cr.meta->'CacheNodes') AS cn
							WHERE cn->'Endpoint'->>'Address' = ANY($2)
						)
					))
		)
	`

	var cloudResources []CloudResourceRow
	err = db.Select(&cloudResources, query, tenantID, pq.Array(namesToQuery))
	if err != nil {
		return nodes, edges, fmt.Errorf("failed to query cloud_resources: %w", err)
	}

	// Log detailed cloud resources found
	resourceDetails := make([]map[string]string, 0)
	for _, resource := range cloudResources {
		resourceDetails = append(resourceDetails, map[string]string{
			"name":     resource.Name,
			"type":     resource.Type,
			"endpoint": extractDNSName(&resource),
		})
	}
	slog.Info("Found cloud resources",
		"count", len(cloudResources),
		"resources", resourceDetails)

	// Build reverse map: AWS endpoint -> original DNS name (for resources found via DNS resolution)
	endpointToDNSName := make(map[string]string)
	for dnsName, endpoint := range dnsResolutions {
		endpointToDNSName[endpoint] = dnsName
	}

	// Create cloud resource nodes and link them to external services
	newNodes := make([]*KnowledgeGraphNode, 0)
	newEdges := make([]*KnowledgeGraphEdge, 0)

	for _, resource := range cloudResources {
		// Determine the node type based on resource type
		nodeType := determineCloudResourceNodeType(resource.Type)

		// Create cloud resource node
		cloudNode := &KnowledgeGraphNode{
			ID:             uuid.New().String(),
			NodeType:       nodeType,
			UniqueKey:      fmt.Sprintf("%s:%s:%s", nodeType, resource.Name, resource.Region),
			CloudAccountID: cloudAccountID,
			TenantID:       tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Properties:     make(map[string]any),
		}

		// Populate properties
		cloudNode.Properties["name"] = resource.Name
		cloudNode.Properties["type"] = resource.Type
		cloudNode.Properties["status"] = resource.Status
		cloudNode.Properties["cloud_provider"] = resource.CloudProvider
		cloudNode.Properties["region"] = resource.Region
		cloudNode.Properties["arn"] = resource.ARN
		cloudNode.Properties["resource_id"] = resource.ResourceID

		// Store identifiers for tracking and correlation
		cloudNode.Properties["nb_resource_id"] = resource.ID                // cloud_resourses.id
		cloudNode.Properties["nb_account_id"] = resource.Account            // cloud_accounts.id (our internal UUID)
		cloudNode.Properties["aws_account_id"] = resource.Account           // cloud_accounts.id (for backward compatibility - UUID)
		cloudNode.Properties["aws_account_number"] = resource.AccountNumber // cloud_accounts.account_number (actual 12-digit AWS account number)

		// Parse and add meta fields
		if len(resource.Meta) > 0 {
			var metaMap map[string]interface{}
			if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
				cloudNode.Properties["meta"] = metaMap

				// Extract commonly used fields to top-level
				if dnsName, ok := metaMap["DNSName"].(string); ok && dnsName != "" {
					cloudNode.Properties["dns_name"] = dnsName
				}
				if vpcID, ok := metaMap["VpcId"].(string); ok && vpcID != "" {
					cloudNode.Properties["vpc_id"] = vpcID
				}
				if secGroups, ok := metaMap["SecurityGroups"].([]interface{}); ok && len(secGroups) > 0 {
					cloudNode.Properties["security_groups"] = secGroups
				}
				if azs, ok := metaMap["AvailabilityZones"].([]interface{}); ok && len(azs) > 0 {
					cloudNode.Properties["availability_zones"] = azs
				}
			}
		}

		// Parse and add tags
		if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
			var tagsMap map[string]interface{}
			if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
				cloudNode.Properties["tags"] = tagsMap
			}
		}

		newNodes = append(newNodes, cloudNode)

		// Find the matching external service node and create an edge
		// Try two approaches:
		// 1. Direct DNS name match (for LoadBalancers with DNSName)
		// 2. Endpoint match via Route53 resolution (for ElastiCache/RDS found via DNS)

		var externalServiceNode *KnowledgeGraphNode
		var matchedBy string
		var matchedValue string

		// Try direct DNS name match first
		dnsName := extractDNSName(&resource)
		if dnsName != "" {
			if node, exists := externalServiceNodeMap[dnsName]; exists {
				externalServiceNode = node
				matchedBy = "dns_name"
				matchedValue = dnsName
				slog.Info("Matched cloud resource via direct DNS name",
					"resource_name", resource.Name,
					"resource_type", resource.Type,
					"dns_name", dnsName,
					"external_service", node.Properties["name"])
			} else {
				slog.Info("Cloud resource DNS name not in external services",
					"resource_name", resource.Name,
					"dns_name", dnsName)
			}
		}

		// Try endpoint match via Route53 resolution
		if externalServiceNode == nil {
			// Check if this resource's endpoint matches a resolved DNS name
			// extractDNSName returns the endpoint for all resource types (LoadBalancer DNSName, RDS/ElastiCache endpoints)
			resourceEndpoint := extractDNSName(&resource)
			if resourceEndpoint != "" {
				if originalDNSName, exists := endpointToDNSName[resourceEndpoint]; exists {
					if node, exists := externalServiceNodeMap[originalDNSName]; exists {
						externalServiceNode = node
						matchedBy = "route53_resolution"
						matchedValue = fmt.Sprintf("%s -> %s", originalDNSName, resourceEndpoint)

						// Also store the original DNS name in cloud node properties
						cloudNode.Properties["dns_name"] = originalDNSName
						slog.Info("Matched cloud resource via Route53 resolution",
							"resource_name", resource.Name,
							"resource_type", resource.Type,
							"resource_endpoint", resourceEndpoint,
							"resolved_from", originalDNSName,
							"external_service", node.Properties["name"])
					} else {
						slog.Info("Route53 resolved endpoint found but external service missing",
							"resource_name", resource.Name,
							"resource_endpoint", resourceEndpoint,
							"original_dns_name", originalDNSName)
					}
				} else {
					slog.Info("Cloud resource endpoint not in Route53 resolutions",
						"resource_name", resource.Name,
						"resource_endpoint", resourceEndpoint,
						"available_resolutions", dnsResolutions)
				}
			}
		}

		// Create edge if we found a match
		if externalServiceNode != nil {
			edge := &KnowledgeGraphEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      externalServiceNode.UniqueKey,
				DestinationNodeID: cloudNode.UniqueKey,
				RelationshipType:  RelationshipRoutesThrough,
				Properties: map[string]any{
					"discovered_from": "cloud_resources_table",
					"matched_by":      matchedBy,
					"match_value":     matchedValue,
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			newEdges = append(newEdges, edge)

			slog.Info("Linked external service to cloud resource",
				"external_service", externalServiceNode.Properties["name"],
				"cloud_resource", resource.Name,
				"type", resource.Type,
				"matched_by", matchedBy)
		}
	}

	// Step 2.6: Create inferred cloud resource nodes for DNS resolutions even if not found in database
	// This ensures we have nodes for AWS endpoints discovered via DNS
	inferredCloudNodes := make(map[string]*KnowledgeGraphNode) // Map: endpoint -> node
	for dnsName, awsEndpoint := range dnsResolutions {
		// Determine resource type from endpoint pattern
		var nodeType NodeType
		var resourceType string

		if strings.Contains(awsEndpoint, ".cache.amazonaws.com") {
			nodeType = NodeTypeCache
			resourceType = "elasticache_inferred"
		} else if strings.Contains(awsEndpoint, ".rds.amazonaws.com") {
			nodeType = NodeTypeDatabase
			resourceType = "rds_inferred"
		} else {
			// Unknown AWS service, create as generic external service
			nodeType = NodeTypeExternalService
			resourceType = "aws_service_inferred"
		}

		// Create inferred cloud resource node
		inferredNode := &KnowledgeGraphNode{
			ID:             uuid.New().String(),
			NodeType:       nodeType,
			UniqueKey:      fmt.Sprintf("%s:%s:%s", nodeType, awsEndpoint, "inferred"),
			CloudAccountID: cloudAccountID,
			TenantID:       tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Properties:     make(map[string]any),
		}

		// Populate properties
		inferredNode.Properties["name"] = awsEndpoint
		inferredNode.Properties["type"] = resourceType
		inferredNode.Properties["dns_name"] = dnsName
		inferredNode.Properties["inferred"] = true
		inferredNode.Properties["discovery_method"] = "route53_dns_resolution"

		inferredCloudNodes[awsEndpoint] = inferredNode
		newNodes = append(newNodes, inferredNode)

		// Create edge from external service (DNS name) to inferred cloud resource (AWS endpoint)
		if externalServiceNode, exists := externalServiceNodeMap[dnsName]; exists {
			edge := &KnowledgeGraphEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      externalServiceNode.UniqueKey,
				DestinationNodeID: inferredNode.UniqueKey,
				RelationshipType:  RelationshipResolvesTo,
				Properties: map[string]any{
					"discovered_from": "route53_dns_resolution",
					"dns_name":        dnsName,
					"aws_endpoint":    awsEndpoint,
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			newEdges = append(newEdges, edge)

			slog.Info("Created inferred cloud resource from DNS resolution",
				"dns_name", dnsName,
				"aws_endpoint", awsEndpoint,
				"resource_type", resourceType,
				"node_type", nodeType)
		}
	}

	// Step 4: Enrich LoadBalancer nodes with backend pod targets
	// This maps LoadBalancer IPs to K8s pods using kube_pod_info metrics
	slog.Info("Starting LoadBalancer target enrichment", "loadbalancer_count", len(newNodes))
	podEnrichmentNodes := make([]*KnowledgeGraphNode, 0)
	podEnrichmentEdges := make([]*KnowledgeGraphEdge, 0)

	for _, cloudNode := range newNodes {
		if cloudNode.NodeType == NodeTypeLoadBalancer {
			// Get AWS account ID from the cloud resource properties
			awsAccountID, ok := cloudNode.Properties["aws_account_id"].(string)
			if !ok || awsAccountID == "" {
				slog.Debug("Skipping LoadBalancer enrichment: no AWS account ID",
					"lb_name", cloudNode.Properties["name"])
				continue
			}

			// Enrich this LoadBalancer with its pod targets
			// cloudAccountID is the K8s account ID, awsAccountID is the AWS account
			podNodes, podEdges, err := EnrichLoadBalancerWithTargets(
				requestContext,
				cloudNode,
				nodes, // Pass existing nodes to find matching services
				awsAccountID,
				cloudAccountID, // This is the K8s account ID
				tenantID,
			)

			if err != nil {
				slog.Warn("Failed to enrich LoadBalancer with targets",
					"lb_name", cloudNode.Properties["name"],
					"error", err)
				continue
			}

			podEnrichmentNodes = append(podEnrichmentNodes, podNodes...)
			podEnrichmentEdges = append(podEnrichmentEdges, podEdges...)

			slog.Info("LoadBalancer enriched with pod targets",
				"lb_name", cloudNode.Properties["name"],
				"pods_added", len(podNodes))
		}
	}

	// Combine all nodes and edges
	enrichedNodes := append(nodes, newNodes...)
	enrichedNodes = append(enrichedNodes, podEnrichmentNodes...)
	enrichedEdges := append(edges, newEdges...)
	enrichedEdges = append(enrichedEdges, podEnrichmentEdges...)

	slog.Info("Cloud resource enrichment completed",
		"added_cloud_resource_nodes", len(newNodes),
		"added_pod_nodes", len(podEnrichmentNodes),
		"added_edges", len(newEdges)+len(podEnrichmentEdges))

	return enrichedNodes, enrichedEdges, nil
}

// GetAWSAccountsForTenant retrieves all active AWS cloud account IDs for a tenant
// This is used to query Route 53 across multiple AWS accounts
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

	slog.Info("Retrieved AWS accounts for tenant",
		"tenant", tenantID,
		"aws_account_count", len(accountIDs))

	return accountIDs, nil
}

// EnrichLoadBalancerWithTargets enriches LoadBalancer nodes with their backend pod targets
// This handles cross-account mapping between AWS LoadBalancers and K8s pods/services
func EnrichLoadBalancerWithTargets(
	requestContext *security.RequestContext,
	lbNode *KnowledgeGraphNode,
	existingNodes []*KnowledgeGraphNode,
	awsAccountID string,
	k8sAccountID string,
	tenantID string,
) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {

	podNodes := make([]*KnowledgeGraphNode, 0)
	edges := make([]*KnowledgeGraphEdge, 0)

	// Build a map of existing Service nodes for quick lookup by name+namespace+cluster
	serviceNodeMap := make(map[string]*KnowledgeGraphNode)
	// Build a map to track pod owner nodes (Deployment/StatefulSet/DaemonSet)
	ownerNodeMap := make(map[string]*KnowledgeGraphNode)
	for _, node := range existingNodes {
		if node.NodeType == NodeTypeService {
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
		slog.Debug("Skipping LoadBalancer enrichment: missing ARN or region",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1: Query AWS for target groups
	tgCommand := fmt.Sprintf(
		"aws elbv2 describe-target-groups --region %s --load-balancer-arn %s --output json",
		region, arn,
	)

	tgResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   tgCommand,
	})
	if err != nil {
		slog.Warn("Failed to query LoadBalancer target groups",
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
			slog.Warn("Failed to parse target groups", "error", err)
			return podNodes, edges, nil
		}
		targetGroups = tgData.TargetGroups
	}

	if len(targetGroups) == 0 {
		slog.Debug("No target groups found for LoadBalancer",
			"lb_name", lbNode.Properties["name"])
		return podNodes, edges, nil
	}

	// Step 1.5: Query LoadBalancer tags to check for Kubernetes service mapping
	tagsCommand := fmt.Sprintf(
		"aws elbv2 describe-tags --resource-arns %s --output json",
		arn,
	)

	tagsResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
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
							slog.Info("Found Kubernetes service for LoadBalancer",
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

		ingressNode := &KnowledgeGraphNode{
			ID:        uuid.New().String(),
			NodeType:  NodeTypeService,
			UniqueKey: fmt.Sprintf("Service:%s:%s", k8sServiceName, k8sNamespace),
			Properties: map[string]interface{}{
				"name":         k8sServiceName,
				"namespace":    k8sNamespace,
				"environment":  environment,
				"type":         "nginx",
				"service.name": k8sServiceName,
			},
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		edge := &KnowledgeGraphEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.UniqueKey,
			DestinationNodeID: ingressNode.UniqueKey,
			RelationshipType:  RelationshipRoutesTo,
			Properties: map[string]interface{}{
				"discovered_from": "aws_lb_tags",
				"service_name":    fmt.Sprintf("%s/%s", k8sNamespace, k8sServiceName),
			},
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		slog.Info("Created ingress controller node for LoadBalancer",
			"lb_name", lbNode.Properties["name"],
			"ingress_service", k8sServiceName,
			"namespace", k8sNamespace)

		// Collect initial nodes and edges
		nodes := []*KnowledgeGraphNode{ingressNode}
		edges := []*KnowledgeGraphEdge{edge}

		// Step 1.5: Resolve Ingress resources to backend services
		// Query for Ingress resources across all namespaces to find backend services
		ingressBackendNodes, ingressBackendEdges, err := resolveIngressBackendServices(requestContext, k8sAccountID, tenantID, environment, ingressNode)
		if err != nil {
			slog.Warn("Failed to resolve Ingress backend services",
				"error", err,
				"ingress_service", k8sServiceName)
			// Continue without backend resolution - this is not a fatal error
		} else if ingressBackendNodes != nil {
			nodes = append(nodes, ingressBackendNodes...)
			edges = append(edges, ingressBackendEdges...)
			slog.Info("Resolved Ingress backend services",
				"ingress_service", k8sServiceName,
				"backend_services_count", len(ingressBackendNodes))
		}

		return nodes, edges, nil
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

		healthResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   healthCommand,
		})
		if err != nil {
			slog.Warn("Failed to query target health", "target_group", tgArn, "error", err)
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
		slog.Info("Resolving EC2 instance IDs to private IPs",
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

		ec2Resp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
			AccountID: awsAccountID,
			Command:   ec2Command,
		})
		if err != nil {
			slog.Warn("Failed to query EC2 instances",
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
								slog.Debug("Resolved EC2 instance to private IP",
									"instance_id", instanceID,
									"private_ip", privateIP,
									"lb_name", lbNode.Properties["name"])
							}
						}
					}
					slog.Info("EC2 instance resolution completed",
						"lb_name", lbNode.Properties["name"],
						"instances_resolved", len(instances),
						"total_ips", len(uniqueIPs))
				} else {
					slog.Warn("Failed to parse EC2 response", "error", err)
				}
			}
		}
	}

	if len(uniqueIPs) == 0 {
		slog.Info("No target IPs found for LoadBalancer",
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

	endTime := time.Now()
	startTime := endTime.Add(-5 * time.Minute)

	podInfoResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		slog.Warn("Failed to query kube_pod_info",
			"lb_name", lbNode.Properties["name"],
			"error", err)
		return podNodes, edges, nil
	}

	// Step 4: Create Pod nodes and edges from query results
	// The response structure from relay.ExecutePrometheus can be:
	// {"pod_info": [{"metric": {...}, "value": [...]}, ...]} - query name as key
	// {"data": [{"metric": ..., "value": ...}, ...]} - data wrapper
	// {"data": {"pod_info": {"result": [...]}}} - nested structure
	var resultArray []interface{}

	// Try to get the array from the response
	if podInfoData, ok := podInfoResp["pod_info"].([]interface{}); ok {
		// Response is: {"pod_info": [{"metric": ..., "value": ...}, ...]}
		resultArray = podInfoData
		slog.Debug("Found pod info with query name key", "count", len(podInfoData))
	} else if data, ok := podInfoResp["data"].([]interface{}); ok {
		// Response is: {"data": [{"metric": ..., "value": ...}, ...]}
		resultArray = data
		slog.Debug("Found pod info in data array", "count", len(data))
	} else if data, ok := podInfoResp["data"].(map[string]interface{}); ok {
		// Response is: {"data": {"pod_info": {"result": [...]}}}
		if podInfoData, ok := data["pod_info"].(map[string]interface{}); ok {
			if result, ok := podInfoData["result"].([]interface{}); ok {
				resultArray = result
				slog.Debug("Found pod info in nested structure", "count", len(result))
			}
		}
	}

	if len(resultArray) == 0 {
		slog.Warn("No pod info results found in Prometheus response",
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
			slog.Debug("Skipping pod without owner info",
				"pod_name", podName,
				"namespace", namespace)
			continue
		}

		// Try to find matching Service node using owner name
		var targetNode *KnowledgeGraphNode
		var targetNodeType = NodeTypePod // Default to Pod owner

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
				targetNodeType = NodeTypeService
				slog.Info("Found matching Service for LoadBalancer target",
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
				properties := map[string]any{
					"name":       ownerName,
					"namespace":  namespace,
					"owner_kind": ownerKind,
					"pods":       []string{podName},
				}

				// Extract commonly-used K8s fields from labels to top-level for easy access
				// This standardizes access pattern: check top-level first, fall back to labels
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

				targetNode = &KnowledgeGraphNode{
					ID:             uuid.New().String(),
					UniqueKey:      fmt.Sprintf("%s:%s:%s", ownerKind, ownerName, namespace),
					NodeType:       NodeTypePod, // Still use Pod type for infrastructure
					CloudAccountID: k8sAccountID,
					TenantID:       tenantID,
					Properties:     properties,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}
				ownerNodeMap[ownerKey] = targetNode
				podNodes = append(podNodes, targetNode)
			}
		}

		// Create edge: LoadBalancer -> Service/Owner
		edge := &KnowledgeGraphEdge{
			ID:                uuid.New().String(),
			SourceNodeID:      lbNode.UniqueKey,
			DestinationNodeID: targetNode.UniqueKey,
			RelationshipType:  RelationshipRoutesTo,
			Properties: map[string]any{
				"discovered_from": "aws_target_health",
				"target_ip":       podIP,
				"pod_name":        podName,
				"owner_kind":      ownerKind,
				"owner_name":      ownerName,
			},
			CloudAccountID: awsAccountID,
			TenantID:       tenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		edges = append(edges, edge)

		slog.Info("Linked LoadBalancer to target",
			"lb_name", lbNode.Properties["name"],
			"target_type", targetNodeType,
			"target_name", targetNode.Properties["name"],
			"pod_name", podName,
			"owner_kind", ownerKind,
			"owner_name", ownerName,
			"namespace", namespace,
			"pod_ip", podIP)
	}

	slog.Info("LoadBalancer target enrichment completed",
		"lb_name", lbNode.Properties["name"],
		"target_ips", len(uniqueIPs),
		"pods_discovered", len(podNodes))

	return podNodes, edges, nil
}

// isKubernetesInternalDNS checks if a hostname is a Kubernetes internal DNS name
// Patterns: *.svc.cluster.local, *.*.svc.cluster.local, etc.
func isKubernetesInternalDNS(hostname string) bool {
	if hostname == "" {
		return false
	}
	// Check for cluster-local DNS patterns
	return strings.HasSuffix(hostname, ".svc.cluster.local") ||
		strings.HasSuffix(hostname, ".pod.cluster.local") ||
		hostname == "localhost" ||
		hostname == "127.0.0.1"
}

// resolveIngressBackendServices queries Kubernetes Ingress resources and creates Service nodes for backend services
// This resolves: nginx-ingress → Ingress Resource → Backend Services
func resolveIngressBackendServices(
	requestContext *security.RequestContext,
	k8sAccountID string,
	tenantID string,
	environment string,
	ingressControllerNode *KnowledgeGraphNode,
) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {

	// Execute kubectl command via relay server to get all Ingress resources
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  k8sAccountID,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": "kubectl get ingress --all-namespaces -o json",
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, _, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute kubectl command: %w", err)
	}

	// Extract stdout from relay response
	var relayData struct {
		Stdout string `json:"stdout"`
	}
	dataStr, ok := relayResponse["data"].(string)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected data format in relay response: %T", relayResponse["data"])
	}
	if err := json.Unmarshal([]byte(dataStr), &relayData); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal relay data: %w", err)
	}

	if relayData.Stdout == "" {
		slog.Debug("No Ingress resources found")
		return nil, nil, nil // No ingress resources found
	}

	// Parse the Ingress list response
	type IngressBackend struct {
		Service struct {
			Name string `json:"name"`
			Port struct {
				Number int `json:"number"`
			} `json:"port"`
		} `json:"service"`
	}

	type IngressPath struct {
		Path    string         `json:"path"`
		Backend IngressBackend `json:"backend"`
	}

	type IngressRule struct {
		Host string `json:"host"`
		HTTP struct {
			Paths []IngressPath `json:"paths"`
		} `json:"http"`
	}

	type IngressSpec struct {
		IngressClassName string        `json:"ingressClassName"`
		Rules            []IngressRule `json:"rules"`
	}

	type IngressResource struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec IngressSpec `json:"spec"`
	}

	type IngressList struct {
		Items []IngressResource `json:"items"`
	}

	var ingressList IngressList
	if err := json.Unmarshal([]byte(relayData.Stdout), &ingressList); err != nil {
		return nil, nil, fmt.Errorf("failed to parse ingress list: %w", err)
	}

	var backendNodes []*KnowledgeGraphNode
	var backendEdges []*KnowledgeGraphEdge

	// Track unique backend services to avoid duplicates
	uniqueBackends := make(map[string]bool) // key: namespace:serviceName

	for _, ingress := range ingressList.Items {
		// Only process ingresses that use nginx ingress controller
		if ingress.Spec.IngressClassName != "nginx" {
			continue
		}

		for _, rule := range ingress.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				backendServiceName := path.Backend.Service.Name
				namespace := ingress.Metadata.Namespace

				if backendServiceName == "" {
					continue
				}

				backendKey := fmt.Sprintf("%s:%s", namespace, backendServiceName)
				if uniqueBackends[backendKey] {
					continue // Already processed this backend
				}
				uniqueBackends[backendKey] = true

				// Create Service node for the backend service
				backendNode := &KnowledgeGraphNode{
					ID:        uuid.New().String(),
					NodeType:  NodeTypeService,
					UniqueKey: fmt.Sprintf("Service:%s:%s", backendServiceName, namespace),
					Properties: map[string]interface{}{
						"name":          backendServiceName,
						"namespace":     namespace,
						"environment":   environment,
						"service.name":  backendServiceName,
						"ingress_host":  rule.Host,
						"ingress_path":  path.Path,
						"ingress_name":  ingress.Metadata.Name,
						"backend_port":  path.Backend.Service.Port.Number,
						"exposed_via":   "ingress",
						"ingress_class": ingress.Spec.IngressClassName,
					},
					CloudAccountID: k8sAccountID,
					TenantID:       tenantID,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}

				// Create edge from ingress controller to backend service
				backendEdge := &KnowledgeGraphEdge{
					ID:                uuid.New().String(),
					SourceNodeID:      ingressControllerNode.UniqueKey,
					DestinationNodeID: backendNode.UniqueKey,
					RelationshipType:  RelationshipRoutesTo,
					Properties: map[string]interface{}{
						"discovered_from": "ingress_resource",
						"ingress_name":    ingress.Metadata.Name,
						"ingress_host":    rule.Host,
						"ingress_path":    path.Path,
						"backend_port":    path.Backend.Service.Port.Number,
					},
					CloudAccountID: k8sAccountID,
					TenantID:       tenantID,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}

				backendNodes = append(backendNodes, backendNode)
				backendEdges = append(backendEdges, backendEdge)

				slog.Debug("Resolved Ingress backend service",
					"ingress", ingress.Metadata.Name,
					"namespace", namespace,
					"backend_service", backendServiceName,
					"host", rule.Host,
					"path", path.Path)
			}
		}
	}

	return backendNodes, backendEdges, nil
}

// ResolveRoute53DNS resolves a hostname via Route 53 and returns the AWS endpoint (if it's an AWS service)
// Returns empty string if not found or not an AWS service
func ResolveRoute53DNS(requestContext *security.RequestContext, hostname string, awsAccountID string) (string, error) {
	if hostname == "" {
		return "", nil
	}

	// Step 1: List hosted zones
	zonesCommand := "aws route53 list-hosted-zones-by-name --output json"
	zonesResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   zonesCommand,
	})
	if err != nil {
		return "", err
	}

	var hostedZones struct {
		HostedZones []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"HostedZones"`
	}

	if data, ok := zonesResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &hostedZones); err != nil {
			return "", err
		}
	} else {
		return "", nil
	}

	// Step 2: Find matching zone
	var matchingZoneID string
	for _, zone := range hostedZones.HostedZones {
		zoneName := strings.TrimSuffix(zone.Name, ".")
		if strings.HasSuffix(hostname, zoneName) {
			matchingZoneID = zone.Id
			break
		}
	}

	if matchingZoneID == "" {
		return "", nil // No matching zone found
	}

	// Step 3: Query DNS records
	recordCommand := fmt.Sprintf("aws route53 list-resource-record-sets --hosted-zone-id %s --output json", matchingZoneID)
	recordResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   recordCommand,
	})
	if err != nil {
		return "", err
	}

	var recordSets struct {
		ResourceRecordSets []struct {
			Name            string `json:"Name"`
			Type            string `json:"Type"`
			ResourceRecords []struct {
				Value string `json:"Value"`
			} `json:"ResourceRecords"`
		} `json:"ResourceRecordSets"`
	}

	if data, ok := recordResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &recordSets); err != nil {
			return "", err
		}
	} else {
		return "", nil
	}

	// Step 4: Find the record for this hostname
	hostnameWithDot := hostname + "."
	for _, record := range recordSets.ResourceRecordSets {
		if record.Name == hostnameWithDot || record.Name == hostname {
			// Handle CNAME records
			if record.Type == "CNAME" && len(record.ResourceRecords) > 0 {
				cname := strings.TrimSuffix(record.ResourceRecords[0].Value, ".")

				// Only return if it's an AWS service endpoint
				if strings.Contains(cname, ".elb.amazonaws.com") ||
					strings.Contains(cname, ".cache.amazonaws.com") ||
					strings.Contains(cname, ".rds.amazonaws.com") {
					return cname, nil
				}
			}
			break
		}
	}

	return "", nil // Not found or not an AWS service
}

// EnrichRoute53DNSWithTargets enriches DNS hostname nodes with their backend targets
// This resolves DNS names through Route 53 and maps them to K8s pods or AWS resources
func EnrichRoute53DNSWithTargets(
	requestContext *security.RequestContext,
	hostname string,
	existingNodes []*KnowledgeGraphNode,
	awsAccountID string,
	k8sAccountID string,
	tenantID string,
) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {

	newNodes := make([]*KnowledgeGraphNode, 0)
	edges := make([]*KnowledgeGraphEdge, 0)

	if hostname == "" {
		return newNodes, edges, nil
	}

	slog.Info("Enriching DNS hostname with targets",
		"hostname", hostname,
		"aws_account", awsAccountID,
		"k8s_account", k8sAccountID)

	// Step 1: List hosted zones to find the matching zone
	zonesCommand := "aws route53 list-hosted-zones-by-name --output json"
	zonesResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   zonesCommand,
	})
	if err != nil {
		slog.Warn("Failed to list Route 53 hosted zones",
			"hostname", hostname,
			"error", err)
		return newNodes, edges, nil
	}

	// Parse hosted zones response
	var hostedZones struct {
		HostedZones []struct {
			Id     string `json:"Id"`
			Name   string `json:"Name"`
			Config struct {
				PrivateZone bool `json:"PrivateZone"`
			} `json:"Config"`
		} `json:"HostedZones"`
	}

	if data, ok := zonesResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &hostedZones); err != nil {
			slog.Warn("Failed to parse hosted zones", "error", err)
			return newNodes, edges, nil
		}
	} else {
		slog.Info("No data in hosted zones response")
		return newNodes, edges, nil
	}

	// Step 2: Find the zone matching our hostname
	var matchingZoneId string
	var matchingZoneName string

	slog.Info("Searching for matching hosted zone",
		"hostname", hostname,
		"zones_to_check", len(hostedZones.HostedZones))

	for _, zone := range hostedZones.HostedZones {
		zoneName := strings.TrimSuffix(zone.Name, ".")
		matches := strings.HasSuffix(hostname, zoneName)

		slog.Info("Checking zone match",
			"hostname", hostname,
			"zone", zoneName,
			"zone_with_dot", zone.Name,
			"matches", matches)

		if matches {
			matchingZoneId = zone.Id
			matchingZoneName = zone.Name
			slog.Info("Found matching Route 53 zone",
				"hostname", hostname,
				"zone", zone.Name,
				"zone_id", zone.Id,
				"private", zone.Config.PrivateZone)
			break
		}
	}

	if matchingZoneId == "" {
		slog.Warn("No hosted zone found for hostname",
			"hostname", hostname,
			"zones_checked", len(hostedZones.HostedZones))
		return newNodes, edges, nil
	}

	// Step 3: Query DNS records for the hostname
	recordsCommand := fmt.Sprintf(
		"aws route53 list-resource-record-sets --hosted-zone-id %s --output json",
		matchingZoneId,
	)

	recordsResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
		AccountID: awsAccountID,
		Command:   recordsCommand,
	})
	if err != nil {
		slog.Warn("Failed to query Route 53 DNS records",
			"hostname", hostname,
			"zone_id", matchingZoneId,
			"error", err)
		return newNodes, edges, nil
	}

	// Parse DNS records
	var recordSets struct {
		ResourceRecordSets []struct {
			Name            string `json:"Name"`
			Type            string `json:"Type"`
			TTL             int    `json:"TTL"`
			ResourceRecords []struct {
				Value string `json:"Value"`
			} `json:"ResourceRecords"`
			AliasTarget struct {
				HostedZoneId         string `json:"HostedZoneId"`
				DNSName              string `json:"DNSName"`
				EvaluateTargetHealth bool   `json:"EvaluateTargetHealth"`
			} `json:"AliasTarget"`
		} `json:"ResourceRecordSets"`
	}

	if data, ok := recordsResp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &recordSets); err != nil {
			slog.Warn("Failed to parse DNS records", "error", err)
			return newNodes, edges, nil
		}
	} else {
		slog.Info("No data in DNS records response")
		return newNodes, edges, nil
	}

	// Step 4: Find records matching our hostname
	dnsNameWithDot := hostname + "."
	var foundRecord bool

	for _, record := range recordSets.ResourceRecordSets {
		if record.Name != dnsNameWithDot {
			continue
		}

		foundRecord = true
		slog.Info("Found DNS record",
			"hostname", hostname,
			"type", record.Type,
			"ttl", record.TTL)

		switch record.Type {
		case "A", "AAAA":
			// A/AAAA records contain IP addresses - map to pods
			ips := make([]string, 0)
			for _, rr := range record.ResourceRecords {
				ips = append(ips, rr.Value)
			}

			if len(ips) > 0 {
				slog.Info("Resolving DNS A record to pods",
					"hostname", hostname,
					"ips", ips)

				// Map IPs to pods using kube_pod_info
				podNodes, podEdges, err := mapIPsToPods(
					ips,
					k8sAccountID,
					tenantID,
					hostname,
					existingNodes,
				)
				if err != nil {
					slog.Warn("Failed to map DNS IPs to pods",
						"hostname", hostname,
						"error", err)
				} else {
					newNodes = append(newNodes, podNodes...)
					edges = append(edges, podEdges...)
				}
			}

		case "CNAME":
			// CNAME points to another DNS name
			if len(record.ResourceRecords) == 0 {
				break
			}

			cname := strings.TrimSuffix(record.ResourceRecords[0].Value, ".")
			slog.Info("DNS CNAME record found",
				"hostname", hostname,
				"cname", cname)

			// Check if CNAME points to an ELB
			if strings.Contains(cname, ".elb.amazonaws.com") {
				slog.Info("DNS resolves to ELB, enriching with load balancer targets",
					"hostname", hostname,
					"elb_dns", cname)

				// Create a temporary LoadBalancer node to use existing enrichment
				lbNode := &KnowledgeGraphNode{
					NodeType: NodeTypeCloudResource,
					Properties: map[string]interface{}{
						"name":           hostname,
						"resource_type":  "LoadBalancer",
						"dns_name":       cname,
						"aws_account_id": awsAccountID,
					},
				}

				// Try to extract region and ARN from ELB DNS
				// Format: internal-{name}-{id}.{region}.elb.amazonaws.com
				parts := strings.Split(cname, ".")
				if len(parts) >= 3 {
					region := parts[len(parts)-4] // e.g., "us-east-1"
					lbNode.Properties["region"] = region

					// Try to get LoadBalancer ARN
					elbCommand := fmt.Sprintf(
						"aws elbv2 describe-load-balancers --region %s --query 'LoadBalancers[?DNSName==`%s`].LoadBalancerArn' --output json",
						region, cname,
					)

					elbResp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
						AccountID: awsAccountID,
						Command:   elbCommand,
					})
					if err == nil {
						if data, ok := elbResp["data"].(string); ok {
							var arns []string
							if err := json.Unmarshal([]byte(data), &arns); err == nil && len(arns) > 0 {
								lbNode.Properties["arn"] = arns[0]

								// Use existing LoadBalancer enrichment
								lbPodNodes, lbEdges, err := EnrichLoadBalancerWithTargets(
									requestContext,
									lbNode,
									existingNodes,
									awsAccountID,
									k8sAccountID,
									tenantID,
								)
								if err != nil {
									slog.Warn("Failed to enrich DNS via LoadBalancer",
										"hostname", hostname,
										"error", err)
								} else {
									newNodes = append(newNodes, lbPodNodes...)
									edges = append(edges, lbEdges...)
								}
							}
						}
					}
				}
			} else if strings.Contains(cname, ".cache.amazonaws.com") {
				// ElastiCache (Redis/Memcached)
				slog.Info("DNS resolves to ElastiCache",
					"hostname", hostname,
					"cache_endpoint", cname)

				// Create ElastiCache node
				cacheNode := &KnowledgeGraphNode{
					ID:             uuid.New().String(),
					UniqueKey:      fmt.Sprintf("ElastiCache:%s", cname),
					NodeType:       NodeTypeCache,
					CloudAccountID: awsAccountID,
					TenantID:       tenantID,
					Properties: map[string]interface{}{
						"name":       cname,
						"endpoint":   cname,
						"dns_name":   hostname,
						"cache_type": "elasticache",
					},
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				newNodes = append(newNodes, cacheNode)

				// Find the external service node for this hostname
				var externalServiceNode *KnowledgeGraphNode
				for _, node := range existingNodes {
					if node.NodeType == NodeTypeExternalService {
						if nodeName, ok := node.Properties["name"].(string); ok && nodeName == hostname {
							externalServiceNode = node
							break
						}
					}
				}

				// Create edge from external service to ElastiCache
				if externalServiceNode != nil {
					edge := &KnowledgeGraphEdge{
						ID:                uuid.New().String(),
						SourceNodeID:      externalServiceNode.UniqueKey,
						DestinationNodeID: cacheNode.UniqueKey,
						RelationshipType:  RelationshipRoutesTo,
						Properties: map[string]any{
							"discovered_from": "route53_dns",
							"dns_name":        hostname,
							"cache_endpoint":  cname,
						},
						CloudAccountID: awsAccountID,
						TenantID:       tenantID,
						CreatedAt:      time.Now(),
						UpdatedAt:      time.Now(),
					}
					edges = append(edges, edge)

					slog.Info("Created edge from external service to ElastiCache",
						"external_service", hostname,
						"cache_endpoint", cname,
						"edge_id", edge.ID)
				} else {
					slog.Warn("Could not find external service node for hostname",
						"hostname", hostname)
				}

			} else if strings.Contains(cname, ".rds.amazonaws.com") {
				// RDS (PostgreSQL/MySQL/etc)
				slog.Info("DNS resolves to RDS",
					"hostname", hostname,
					"rds_endpoint", cname)

				// Create RDS node
				rdsNode := &KnowledgeGraphNode{
					ID:             uuid.New().String(),
					UniqueKey:      fmt.Sprintf("RDS:%s", cname),
					NodeType:       NodeTypeDatabase,
					CloudAccountID: awsAccountID,
					TenantID:       tenantID,
					Properties: map[string]interface{}{
						"name":     cname,
						"endpoint": cname,
						"dns_name": hostname,
					},
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				newNodes = append(newNodes, rdsNode)

				// Find the external service node for this hostname
				var externalServiceNode *KnowledgeGraphNode
				for _, node := range existingNodes {
					if node.NodeType == NodeTypeExternalService {
						if nodeName, ok := node.Properties["name"].(string); ok && nodeName == hostname {
							externalServiceNode = node
							break
						}
					}
				}

				// Create edge from external service to RDS
				if externalServiceNode != nil {
					edge := &KnowledgeGraphEdge{
						ID:                uuid.New().String(),
						SourceNodeID:      externalServiceNode.UniqueKey,
						DestinationNodeID: rdsNode.UniqueKey,
						RelationshipType:  RelationshipRoutesTo,
						Properties: map[string]any{
							"discovered_from": "route53_dns",
							"dns_name":        hostname,
							"rds_endpoint":    cname,
						},
						CloudAccountID: awsAccountID,
						TenantID:       tenantID,
						CreatedAt:      time.Now(),
						UpdatedAt:      time.Now(),
					}
					edges = append(edges, edge)

					slog.Info("Created edge from external service to RDS",
						"external_service", hostname,
						"rds_endpoint", cname,
						"edge_id", edge.ID)
				} else {
					slog.Warn("Could not find external service node for hostname",
						"hostname", hostname)
				}

			} else {
				// Generic external service
				slog.Info("DNS resolves to external service",
					"hostname", hostname,
					"target", cname)

				externalNode := &KnowledgeGraphNode{
					ID:        uuid.New().String(),
					UniqueKey: fmt.Sprintf("ExternalService:%s", hostname),
					NodeType:  NodeTypeExternalService,
					TenantID:  tenantID,
					Properties: map[string]interface{}{
						"name":   hostname,
						"target": cname,
					},
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				newNodes = append(newNodes, externalNode)
			}
		}
	}

	if !foundRecord {
		slog.Info("No DNS record found for hostname",
			"hostname", hostname,
			"zone", matchingZoneName)
	}

	slog.Info("Route 53 DNS enrichment completed",
		"hostname", hostname,
		"nodes_added", len(newNodes),
		"edges_added", len(edges))

	return newNodes, edges, nil
}

// mapIPsToPods maps a list of IP addresses to Kubernetes pods
// This is extracted from EnrichLoadBalancerWithTargets for reuse
func mapIPsToPods(
	ips []string,
	k8sAccountID string,
	tenantID string,
	sourceName string,
	existingNodes []*KnowledgeGraphNode,
) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge, error) {

	podNodes := make([]*KnowledgeGraphNode, 0)
	edges := make([]*KnowledgeGraphEdge, 0)

	if len(ips) == 0 {
		return podNodes, edges, nil
	}

	// Build a map of existing Service nodes for quick lookup
	serviceNodeMap := make(map[string]*KnowledgeGraphNode)
	for _, node := range existingNodes {
		if node.NodeType == NodeTypeService {
			name, _ := node.Properties["name"].(string)
			namespace, _ := node.Properties["namespace"].(string)
			cluster, _ := node.Properties["cluster"].(string)
			if name != "" {
				key := fmt.Sprintf("%s:%s:%s", name, namespace, cluster)
				serviceNodeMap[key] = node
			}
		}
	}

	// Query kube_pod_info to map IPs to pod names
	ipFilter := strings.Join(ips, "|")
	queries := map[string]string{
		"pod_info": fmt.Sprintf(`kube_pod_info{pod_ip=~"%s"}`, ipFilter),
	}

	endTime := time.Now()
	startTime := endTime.Add(-5 * time.Minute)

	podInfoResp, err := relay.ExecutePrometheus(k8sAccountID, startTime, endTime, queries, true)
	if err != nil {
		slog.Warn("Failed to query kube_pod_info for IPs",
			"source", sourceName,
			"error", err)
		return podNodes, edges, nil
	}

	// Parse pod info response
	var resultArray []interface{}
	if podInfoData, ok := podInfoResp["pod_info"].([]interface{}); ok {
		resultArray = podInfoData
	} else if data, ok := podInfoResp["data"].([]interface{}); ok {
		resultArray = data
	} else if data, ok := podInfoResp["data"].(map[string]interface{}); ok {
		if podInfoData, ok := data["pod_info"].(map[string]interface{}); ok {
			if result, ok := podInfoData["result"].([]interface{}); ok {
				resultArray = result
			}
		}
	}

	if len(resultArray) == 0 {
		slog.Debug("No pod info results found for IPs",
			"source", sourceName,
			"ips", len(ips))
		return podNodes, edges, nil
	}

	// Collect ReplicaSets to query for owners
	replicaSetsToQuery := make(map[string]bool)
	podMetrics := make([]map[string]interface{}, 0)

	for _, item := range resultArray {
		if pod, ok := item.(map[string]interface{}); ok {
			if metric, ok := pod["metric"].(map[string]interface{}); ok {
				podMetrics = append(podMetrics, metric)

				createdByKind, _ := metric["created_by_kind"].(string)
				createdByName, _ := metric["created_by_name"].(string)
				namespace, _ := metric["namespace"].(string)

				if createdByKind == "ReplicaSet" && createdByName != "" && namespace != "" {
					replicaSetsToQuery[fmt.Sprintf("%s/%s", namespace, createdByName)] = true
				}
			}
		}
	}

	// Query kube_replicaset_owner to get Deployment owners
	replicaSetOwners := make(map[string]map[string]string)
	if len(replicaSetsToQuery) > 0 {
		// Build focused query with specific ReplicaSets
		// Group by namespace to create efficient regex filters
		namespaceReplicaSets := make(map[string][]string)
		for nsRS := range replicaSetsToQuery {
			parts := strings.SplitN(nsRS, "/", 2)
			if len(parts) == 2 {
				namespace := parts[0]
				rsName := parts[1]
				namespaceReplicaSets[namespace] = append(namespaceReplicaSets[namespace], rsName)
			}
		}

		// Build regex for ReplicaSet names per namespace
		var queryParts []string
		for namespace, rsNames := range namespaceReplicaSets {
			if len(rsNames) == 1 {
				queryParts = append(queryParts, fmt.Sprintf(`kube_replicaset_owner{namespace="%s",replicaset="%s"}`, namespace, rsNames[0]))
			} else {
				// Use regex for multiple ReplicaSets in same namespace
				rsRegex := strings.Join(rsNames, "|")
				queryParts = append(queryParts, fmt.Sprintf(`kube_replicaset_owner{namespace="%s",replicaset=~"%s"}`, namespace, rsRegex))
			}
		}

		// Combine all namespace queries with OR
		rsQuery := strings.Join(queryParts, " or ")
		rsQueries := map[string]string{
			"rs_owner": rsQuery,
		}

		slog.Info("Querying ReplicaSet owners",
			"replicasets_count", len(replicaSetsToQuery),
			"query", rsQuery)

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

	// Create pod nodes and edges
	for _, metric := range podMetrics {
		podName, _ := metric["pod"].(string)
		namespace, _ := metric["namespace"].(string)
		podIP, _ := metric["pod_ip"].(string)
		createdByKind, _ := metric["created_by_kind"].(string)
		createdByName, _ := metric["created_by_name"].(string)

		if podName == "" || podIP == "" {
			continue
		}

		// Determine the actual owner (might be Deployment via ReplicaSet)
		ownerKind := createdByKind
		ownerName := createdByName

		if createdByKind == "ReplicaSet" {
			rsKey := fmt.Sprintf("%s/%s", namespace, createdByName)
			if owner, exists := replicaSetOwners[rsKey]; exists {
				if owner["kind"] != "" {
					ownerKind = owner["kind"]
					ownerName = owner["name"]
				}
			} else {
				ownerName = extractDeploymentFromReplicaSet(createdByName)
				ownerKind = "Deployment"
			}
		}

		// Create node for the pod's owner (Deployment, StatefulSet, etc.)
		// Use NodeTypePod and store owner kind in properties
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
			"pod_name":   podName,
			"pod_ip":     podIP,
		}

		// Extract commonly-used K8s fields from labels to top-level for easy access
		// This standardizes access pattern: check top-level first, fall back to labels
		if k8sCluster, ok := labels["k8s_cluster"]; ok && k8sCluster != "" {
			properties["k8s_cluster"] = k8sCluster
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

		targetNode := &KnowledgeGraphNode{
			ID:             uuid.New().String(),
			UniqueKey:      fmt.Sprintf("%s:%s:%s", ownerKind, ownerName, namespace),
			NodeType:       NodeTypePod,
			CloudAccountID: k8sAccountID,
			TenantID:       tenantID,
			Properties:     properties,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		podNodes = append(podNodes, targetNode)

		// Try to find matching Service node to create edge to
		var targetServiceNode *KnowledgeGraphNode
		for key, svcNode := range serviceNodeMap {
			svcNamespace, _ := svcNode.Properties["namespace"].(string)
			if svcNamespace == namespace {
				targetServiceNode = svcNode
				slog.Debug("Matched pod to service",
					"pod", podName,
					"service", key)
				break
			}
		}

		// Create edge from source (DNS hostname) to target (Service or Pod)
		if targetServiceNode != nil {
			edge := &KnowledgeGraphEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      sourceName, // This should be a node ID, not a name
				DestinationNodeID: targetServiceNode.ID,
				RelationshipType:  RelationshipResolvesTo,
				TenantID:          tenantID,
				CloudAccountID:    k8sAccountID,
				Properties: map[string]interface{}{
					"discovered_from": "route53_dns",
					"pod_ip":          podIP,
					"pod_name":        podName,
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			edges = append(edges, edge)
		}
	}

	return podNodes, edges, nil
}

// extractDeploymentFromReplicaSet extracts the Deployment name from a ReplicaSet name
// ReplicaSet naming pattern: {deployment-name}-{hash}
// Example: manoj-shipper-759b8c597f → manoj-shipper
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

// extractPodOwner extracts the owner kind and name from a Kubernetes pod name
//
// Returns (ownerKind, ownerName)
// Pod naming patterns:
//   - Deployment: {name}-{deployment-hash}-{pod-id}
//     Example: tracking-service-external-5844c67545-kgqsg → ("Deployment", "tracking-service-external")
//   - StatefulSet: {name}-{ordinal}
//     Example: kafka-0 → ("StatefulSet", "kafka")
//   - DaemonSet: {name}-{pod-id}
//     Example: fluentd-abc12 → ("DaemonSet", "fluentd")
//
//nolint:unused // Used in extract_pod_owner_test.go
func extractPodOwner(podName string) (string, string) {
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
	if len(lastPart) == 5 && isAlphanumeric(lastPart) {
		parts = parts[:len(parts)-1]
	}

	// Check if new last part is a 8-10 char alphanumeric (deployment hash)
	if len(parts) > 1 {
		lastPart = parts[len(parts)-1]
		if len(lastPart) >= 8 && len(lastPart) <= 10 && isAlphanumeric(lastPart) {
			// Deployment: remove hash and pod ID
			ownerName := strings.Join(parts[:len(parts)-1], "-")
			return "Deployment", ownerName
		}
	}

	// Fallback: assume DaemonSet (or unknown pattern)
	ownerName := strings.Join(parts, "-")
	return "Deployment", ownerName
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

// determineCloudResourceNodeType maps cloud resource type to knowledge graph node type
func determineCloudResourceNodeType(resourceType string) NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	switch {
	// LoadBalancers
	case strings.Contains(resourceTypeLower, "loadbalancer"):
		return NodeTypeLoadBalancer

	// Databases
	case resourceTypeLower == "db",
		strings.Contains(resourceTypeLower, "rds"):
		return NodeTypeRDS

	// Cache
	case strings.Contains(resourceTypeLower, "elasticache"):
		return NodeTypeCache

	// Storage
	case resourceTypeLower == "storage",
		strings.Contains(resourceTypeLower, "s3"):
		return NodeTypeS3

	// Compute
	case resourceTypeLower == "compute-instance",
		strings.Contains(resourceTypeLower, "ec2"):
		return NodeTypeEC2

	// Serverless
	case resourceTypeLower == "function",
		strings.Contains(resourceTypeLower, "lambda"):
		return NodeTypeLambda

	// NoSQL
	case resourceTypeLower == "table",
		strings.Contains(resourceTypeLower, "dynamodb"):
		return NodeTypeDynamoDB

	// Queue/Messaging
	case resourceTypeLower == "queue",
		resourceTypeLower == "topic",
		resourceTypeLower == "message-delivery",
		strings.Contains(resourceTypeLower, "sqs"),
		strings.Contains(resourceTypeLower, "sns"):
		return NodeTypeQueue

	// Network
	case resourceTypeLower == "vpc":
		return NodeTypeVPC
	case resourceTypeLower == "security_group":
		return NodeTypeSecurityGroup
	case resourceTypeLower == "natgateway":
		return NodeTypeNATGateway

	// DNS
	case resourceTypeLower == "hostedzone",
		strings.Contains(resourceTypeLower, "route53"):
		return NodeTypeRoute53

	// CDN
	case resourceTypeLower == "distribution",
		strings.Contains(resourceTypeLower, "cloudfront"):
		return NodeTypeCloudFront

	// Container/Registry
	case resourceTypeLower == "repository",
		resourceTypeLower == "cluster",
		strings.Contains(resourceTypeLower, "ecr"),
		strings.Contains(resourceTypeLower, "eks"):
		return NodeTypeEKSCluster

	// Security
	case resourceTypeLower == "secret",
		strings.Contains(resourceTypeLower, "secretsmanager"):
		return NodeTypeSecretsManager

	// Monitoring/Logging
	case resourceTypeLower == "log-group",
		strings.Contains(resourceTypeLower, "cloudwatch"):
		return NodeTypeCloudWatch

	// Generic fallback
	default:
		return NodeTypeCloudResource
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

	// ElastiCache - ReaderEndpoint (can be string or object)
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(string); ok && readerEndpoint != "" {
		return readerEndpoint
	}
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(map[string]interface{}); ok {
		if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache - PrimaryEndpoint (can be string or object)
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(string); ok && primaryEndpoint != "" {
		return primaryEndpoint
	}
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(map[string]interface{}); ok {
		if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache - NodeGroups array (for replication groups)
	if nodeGroups, ok := metaMap["NodeGroups"].([]interface{}); ok && len(nodeGroups) > 0 {
		// Try first node group's PrimaryEndpoint
		if ng, ok := nodeGroups[0].(map[string]interface{}); ok {
			if primaryEndpoint, ok := ng["PrimaryEndpoint"].(map[string]interface{}); ok {
				if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
			// Try first node group's ReaderEndpoint
			if readerEndpoint, ok := ng["ReaderEndpoint"].(map[string]interface{}); ok {
				if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
		}
	}

	// ElastiCache - CacheNodes array (for single cluster)
	if cacheNodes, ok := metaMap["CacheNodes"].([]interface{}); ok && len(cacheNodes) > 0 {
		// Try first cache node's Endpoint
		if cn, ok := cacheNodes[0].(map[string]interface{}); ok {
			if endpoint, ok := cn["Endpoint"].(map[string]interface{}); ok {
				if address, ok := endpoint["Address"].(string); ok && address != "" {
					return address
				}
			}
		}
	}

	return ""
}

// countExternalServices counts the number of external service nodes
func countExternalServices(nodes []*KnowledgeGraphNode) int {
	count := 0
	for _, node := range nodes {
		if node.NodeType == NodeTypeExternalService {
			count++
		}
	}
	return count
}

// ConvertServiceMapToKnowledgeGraph converts a ServiceMap into knowledge graph nodes and edges
func ConvertServiceMapToKnowledgeGraph(serviceMap *ServiceMap, cloudAccountID, tenantID string) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge) {
	if serviceMap == nil {
		return []*KnowledgeGraphNode{}, []*KnowledgeGraphEdge{}
	}

	edges := make([]*KnowledgeGraphEdge, 0)
	now := time.Now()

	// Track created nodes by their unique key to avoid duplicates
	nodeMap := make(map[string]*KnowledgeGraphNode)

	// Build a lookup map for applications by name to find their actual properties
	appLookup := make(map[string]*ServiceApplication)
	for i := range serviceMap.Applications {
		app := &serviceMap.Applications[i]
		appLookup[fmt.Sprintf("%s:%s", app.Id.Kind, app.Id.Name)] = app
	}

	// Helper function to create or get a node
	createOrGetNode := func(name, kind, namespace, environment string, appType []string, labels map[string]string, isHealthy bool, healthReason string) *KnowledgeGraphNode {
		// First, check if this service exists in applications list and use its actual properties
		var nodeStats *NodeStats
		uniqueKey := fmt.Sprintf("%s:%s", kind, name)
		if actualApp, exists := appLookup[uniqueKey]; exists {
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
		nodeType := NodeTypeService
		if kind == "ExternalService" {
			nodeType = NodeTypeExternalService
		}

		// Check application type for more specific classification
		// BUT: Don't override ExternalService - they should stay as ExternalService
		// regardless of their underlying technology (redis, postgres, etc.)
		if kind != "ExternalService" {
			for _, t := range appType {
				switch strings.ToLower(t) {
				case "database", "postgres", "postgresql", "mysql", "mongodb", "elasticsearch":
					nodeType = NodeTypeDatabase
				case "cache", "redis":
					nodeType = NodeTypeCache
				case "messaging", "kafka", "rabbitmq", "sqs", "amqp":
					nodeType = NodeTypeMessageQueue
				}
			}
		}

		uniqueKey = fmt.Sprintf("%s:%s:%s", nodeType, name, environment)

		// Return existing node if already created
		if existing, ok := nodeMap[uniqueKey]; ok {
			return existing
		}

		// Create new node
		properties := make(map[string]any)
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

		node := &KnowledgeGraphNode{
			ID:             nodeID,
			NodeType:       nodeType,
			UniqueKey:      uniqueKey,
			Properties:     properties,
			CloudAccountID: cloudAccountID,
			TenantID:       tenantID,
			CreatedAt:      now,
			UpdatedAt:      now,
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
			targetName, targetKind := parseUpstreamId(upstream.Id)
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

			edgeID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("%v:%v:%v", sourceNode.UniqueKey, upstreamNode.UniqueKey, RelationshipCalls))).String() // UUIDv5 style
			// Create edge: this service -> upstream service
			edge := &KnowledgeGraphEdge{
				ID:                edgeID,
				SourceNodeID:      sourceNode.UniqueKey,
				DestinationNodeID: upstreamNode.UniqueKey,
				RelationshipType:  RelationshipCalls,
				Properties: map[string]any{
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
			edge := &KnowledgeGraphEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      downstreamNode.UniqueKey,
				DestinationNodeID: sourceNode.UniqueKey,
				RelationshipType:  RelationshipCalls,
				Properties: map[string]any{
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
				CreatedAt:      now,
				UpdatedAt:      now,
			}

			edges = append(edges, edge)
		}
	}

	// Convert nodeMap to slice
	nodes := make([]*KnowledgeGraphNode, 0, len(nodeMap))
	for _, node := range nodeMap {
		nodes = append(nodes, node)
	}

	// Add K8s infrastructure nodes and edges if available
	if serviceMap.K8sMetadata != nil {
		k8sNodes, k8sEdges := convertK8sMetadataToGraph(serviceMap.K8sMetadata, cloudAccountID, tenantID, now)
		nodes = append(nodes, k8sNodes...)
		edges = append(edges, k8sEdges...)

		slog.Info("Added K8s infrastructure to knowledge graph",
			"k8s_nodes", len(k8sNodes),
			"k8s_edges", len(k8sEdges))
	}

	slog.Info("Converted service map to knowledge graph",
		"applications_count", len(serviceMap.Applications),
		"nodes_created", len(nodes),
		"edges_created", len(edges))

	return nodes, edges
}

// parseUpstreamId parses the upstream ID format ":Kind:Name" and returns name and kind
func parseUpstreamId(id string) (name, kind string) {
	// Format is ":Kind:Name"
	parts := strings.Split(id, ":")
	if len(parts) >= 3 {
		kind = parts[1]
		name = parts[2]
	}
	return
}

// convertK8sMetadataToGraph converts K8sInfrastructureMetadata to knowledge graph nodes and edges
func convertK8sMetadataToGraph(metadata *K8sInfrastructureMetadata, cloudAccountID, tenantID string, timestamp time.Time) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge) {
	nodes := make([]*KnowledgeGraphNode, 0)
	edges := make([]*KnowledgeGraphEdge, 0)

	// Create cluster nodes
	for _, cluster := range metadata.Clusters {
		node := &KnowledgeGraphNode{
			ID:        uuid.New().String(),
			NodeType:  NodeTypeCluster,
			UniqueKey: fmt.Sprintf("Cluster:%s:%s", cluster.Name, cluster.Environment),
			Properties: map[string]any{
				"name":        cluster.Name,
				"environment": cluster.Environment,
			},
			CloudAccountID: cloudAccountID,
			TenantID:       tenantID,
			CreatedAt:      timestamp,
			UpdatedAt:      timestamp,
		}
		nodes = append(nodes, node)
	}

	// Create namespace nodes and edges to clusters
	for _, ns := range metadata.Namespaces {
		node := &KnowledgeGraphNode{
			ID:        uuid.New().String(),
			NodeType:  NodeTypeNamespace,
			UniqueKey: fmt.Sprintf("Namespace:%s:%s:%s", ns.Cluster, ns.Name, ns.Environment),
			Properties: map[string]any{
				"name":        ns.Name,
				"cluster":     ns.Cluster,
				"environment": ns.Environment,
			},
			CloudAccountID: cloudAccountID,
			TenantID:       tenantID,
			CreatedAt:      timestamp,
			UpdatedAt:      timestamp,
		}
		nodes = append(nodes, node)

		// Create edge: namespace -> cluster
		if ns.Cluster != "" {
			edge := &KnowledgeGraphEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      node.UniqueKey,
				DestinationNodeID: fmt.Sprintf("Cluster:%s:%s", ns.Cluster, ns.Environment),
				RelationshipType:  "BELONGS_TO",
				Properties: map[string]any{
					"relationship": "namespace_in_cluster",
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				CreatedAt:      timestamp,
				UpdatedAt:      timestamp,
			}
			edges = append(edges, edge)
		}
	}

	// Create worker node nodes and edges to clusters
	for _, n := range metadata.Nodes {
		node := &KnowledgeGraphNode{
			ID:        uuid.New().String(),
			NodeType:  NodeTypeNode,
			UniqueKey: fmt.Sprintf("Node:%s:%s:%s", n.Cluster, n.Name, n.Environment),
			Properties: map[string]any{
				"name":        n.Name,
				"cluster":     n.Cluster,
				"environment": n.Environment,
			},
			CloudAccountID: cloudAccountID,
			TenantID:       tenantID,
			CreatedAt:      timestamp,
			UpdatedAt:      timestamp,
		}
		nodes = append(nodes, node)

		// Create edge: node -> cluster
		if n.Cluster != "" {
			edge := &KnowledgeGraphEdge{
				ID:                uuid.New().String(),
				SourceNodeID:      node.UniqueKey,
				DestinationNodeID: fmt.Sprintf("Cluster:%s:%s", n.Cluster, n.Environment),
				RelationshipType:  "PART_OF",
				Properties: map[string]any{
					"relationship": "node_in_cluster",
				},
				CloudAccountID: cloudAccountID,
				TenantID:       tenantID,
				CreatedAt:      timestamp,
				UpdatedAt:      timestamp,
			}
			edges = append(edges, edge)
		}
	}

	return nodes, edges
}

// extractK8sInfrastructureFromSpans extracts K8s infrastructure nodes from trace spans
// func extractK8sInfrastructureFromSpans(spans []TraceSpan, cloudAccountID, tenantID string) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge) {
// 	if len(spans) == 0 {
// 		return []*KnowledgeGraphNode{}, []*KnowledgeGraphEdge{}
// 	}

// 	nodes := make([]*KnowledgeGraphNode, 0)
// 	edges := make([]*KnowledgeGraphEdge, 0)
// 	now := time.Now()

// 	// Track unique infrastructure nodes to avoid duplicates
// 	clusterNodes := make(map[string]*KnowledgeGraphNode)
// 	namespaceNodes := make(map[string]*KnowledgeGraphNode)
// 	podNodes := make(map[string]*KnowledgeGraphNode)
// 	nodeNodes := make(map[string]*KnowledgeGraphNode)

// 	extractor := NewTraceToKnowledgeGraphExtractor(cloudAccountID, tenantID)

// 	for _, span := range spans {
// 		// Parse span attributes
// 		attrs, err := extractor.parseSpanAttributes(span.SpanAttributes, nil)
// 		if err != nil {
// 			continue
// 		}

// 		serviceName := attrs.ServiceName
// 		if serviceName == "" {
// 			serviceName = span.WorkloadName
// 		}
// 		if serviceName == "" {
// 			continue
// 		}

// 		// Create a temporary service node for infrastructure extraction
// 		serviceNode := &KnowledgeGraphNode{
// 			NodeType:  NodeTypeService,
// 			UniqueKey: fmt.Sprintf("Service:%s:%s", serviceName, attrs.DeploymentEnv),
// 			Properties: map[string]any{
// 				"name":        serviceName,
// 				"environment": attrs.DeploymentEnv,
// 			},
// 			CloudAccountID: cloudAccountID,
// 			TenantID:       tenantID,
// 		}

// 		// Extract K8s infrastructure for this service
// 		infraNodes, infraEdges := extractor.extractKubernetesInfrastructure(serviceNode, attrs)

// 		// Deduplicate and add nodes
// 		for _, node := range infraNodes {
// 			var targetMap map[string]*KnowledgeGraphNode
// 			switch node.NodeType {
// 			case NodeTypeCluster:
// 				targetMap = clusterNodes
// 			case NodeTypeNamespace:
// 				targetMap = namespaceNodes
// 			case NodeTypePod:
// 				targetMap = podNodes
// 			case NodeTypeNode:
// 				targetMap = nodeNodes
// 			default:
// 				continue
// 			}

// 			if _, exists := targetMap[node.UniqueKey]; !exists {
// 				node.ID = uuid.New().String()
// 				node.CreatedAt = now
// 				node.UpdatedAt = now
// 				targetMap[node.UniqueKey] = node
// 			}
// 		}

// 		// Add edges (they reference unique keys, so duplicates will be handled by SaveEdges)
// 		edges = append(edges, infraEdges...)
// 	}

// 	// Collect all unique nodes
// 	for _, node := range clusterNodes {
// 		nodes = append(nodes, node)
// 	}
// 	for _, node := range namespaceNodes {
// 		nodes = append(nodes, node)
// 	}
// 	for _, node := range podNodes {
// 		nodes = append(nodes, node)
// 	}
// 	for _, node := range nodeNodes {
// 		nodes = append(nodes, node)
// 	}

// 	slog.Info("Extracted K8s infrastructure from spans",
// 		"spans_processed", len(spans),
// 		"clusters", len(clusterNodes),
// 		"namespaces", len(namespaceNodes),
// 		"pods", len(podNodes),
// 		"nodes", len(nodeNodes),
// 		"total_infra_nodes", len(nodes),
// 		"total_infra_edges", len(edges))

// 	return nodes, edges
// }

// Knowledge Graph Storage & Service Layer

// KnowledgeGraphService handles all knowledge graph persistence operations
type KnowledgeGraphService struct {
	dbManager *database.DatabaseManager
}

// NewKnowledgeGraphService creates a new knowledge graph service instance
func NewKnowledgeGraphService() (*KnowledgeGraphService, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	return &KnowledgeGraphService{
		dbManager: dbManager,
	}, nil
}

// RefreshKnowledgeGraphRequest contains parameters for refreshing the knowledge graph
type RefreshKnowledgeGraphRequest struct {
	TenantID       string `json:"tenant_id"`
	CloudAccountID string `json:"cloud_account_id"`
	ForceRefresh   bool   `json:"force_refresh,omitempty"`
}

// RefreshKnowledgeGraphResponse contains the results of the refresh operation
type RefreshKnowledgeGraphResponse struct {
	Success        bool                             `json:"success"`
	ProcessedCount int                              `json:"processed_count"`
	TotalNodes     int                              `json:"total_nodes"`
	TotalEdges     int                              `json:"total_edges"`
	ServiceResults map[string]*ServiceRefreshResult `json:"service_results"`
	ErrorMessage   string                           `json:"error_message,omitempty"`
}

// ServiceRefreshResult contains the results for a specific service
type ServiceRefreshResult struct {
	ServiceName string `json:"service_name"`
	Success     bool   `json:"success"`
	NodesCount  int    `json:"nodes_count"`
	EdgesCount  int    `json:"edges_count"`
	Error       string `json:"error,omitempty"`
}

// API request types for knowledge graph endpoints
type GetKnowledgeGraphNodesRequest struct {
	AccountId string `json:"account_id" validate:"required"`
	TenantId  string `json:"tenant_id" validate:"required"`
}

type GetKnowledgeGraphEdgesRequest struct {
	AccountId string `json:"account_id" validate:"required"`
	TenantId  string `json:"tenant_id" validate:"required"`
}

type GetServiceDependenciesRequest struct {
	AccountId   string `json:"account_id" validate:"required"`
	TenantId    string `json:"tenant_id" validate:"required"`
	ServiceName string `json:"service_name" validate:"required"`
	Environment string `json:"environment"`
}

type GetKnowledgeGraphServiceMapRequest struct {
	AccountId string `json:"account_id" validate:"required"`
	TenantId  string `json:"tenant_id" validate:"required"`
}

// SaveNodes saves or updates multiple nodes in the database using bulk upsert
func (s *KnowledgeGraphService) SaveNodes(nodes []*KnowledgeGraphNode) error {
	if len(nodes) == 0 {
		return nil
	}

	// Deduplicate nodes by unique key to avoid constraint violations
	nodeMap := make(map[string]*KnowledgeGraphNode)
	for _, node := range nodes {
		// Use a composite key to ensure uniqueness
		compositeKey := fmt.Sprintf("%s:%s:%s", node.UniqueKey, node.CloudAccountID, node.TenantID)
		if existing, exists := nodeMap[compositeKey]; exists {
			// Merge properties if the node already exists
			for k, v := range node.Properties {
				existing.Properties[k] = v
			}
			existing.UpdatedAt = time.Now()
		} else {
			nodeMap[compositeKey] = node
		}
	}

	// Convert map back to slice
	deduplicatedNodes := make([]*KnowledgeGraphNode, 0, len(nodeMap))
	for _, node := range nodeMap {
		deduplicatedNodes = append(deduplicatedNodes, node)
	}

	// Prepare data for bulk insert
	cols := []string{"id", "created_at", "updated_at", "properties", "cloud_account_id", "tenant_id", "unique_key"}
	values := make([][]any, len(deduplicatedNodes))

	now := time.Now()
	for i, node := range deduplicatedNodes {
		// Generate ID if not present
		if node.ID == "" {
			node.ID = uuid.New().String()
		}

		// Set timestamps if not present
		if node.CreatedAt.IsZero() {
			node.CreatedAt = now
		}
		node.UpdatedAt = now

		// Convert properties to JSON
		propertiesJSON, err := json.Marshal(node.Properties)
		if err != nil {
			return fmt.Errorf("failed to marshal node properties: %w", err)
		}

		values[i] = []any{
			node.ID,
			node.CreatedAt,
			node.UpdatedAt,
			propertiesJSON,
			node.CloudAccountID,
			node.TenantID,
			node.UniqueKey,
		}
	}

	// Use database manager's bulk insert with conflict resolution
	onConflict := []string{"unique_key", "cloud_account_id", "tenant_id"}
	onConflictUpdate := []string{"updated_at", "properties"}

	_, err := s.dbManager.Insert(nil, "knowledge_graph_node", onConflict, onConflictUpdate, nil, cols, values...)
	if err != nil {
		return fmt.Errorf("failed to upsert nodes: %w", err)
	}

	slog.Info("Successfully saved nodes to knowledge graph", "count", len(deduplicatedNodes))
	return nil
}

// SaveEdges saves or updates multiple edges in the database using bulk upsert
func (s *KnowledgeGraphService) SaveEdges(edges []*KnowledgeGraphEdge) error {
	if len(edges) == 0 {
		return nil
	}

	// First get node ID mappings
	nodeIDMap, err := s.getNodeIDMapping(edges)
	if err != nil {
		return fmt.Errorf("failed to get node ID mapping: %w", err)
	}

	// Filter edges to only include those with valid node mappings and deduplicate
	edgeMap := make(map[string]*KnowledgeGraphEdge)
	for _, edge := range edges {
		sourceID, sourceExists := nodeIDMap[edge.SourceNodeID]
		destID, destExists := nodeIDMap[edge.DestinationNodeID]

		if !sourceExists {
			slog.Warn("Source node not found for edge", "source", edge.SourceNodeID)
			continue
		}
		if !destExists {
			slog.Warn("Destination node not found for edge", "destination", edge.DestinationNodeID)
			continue
		}

		// Store resolved IDs temporarily
		edge.SourceNodeID = sourceID
		edge.DestinationNodeID = destID

		// Create composite key for deduplication
		compositeKey := fmt.Sprintf("%s:%s:%s:%s:%s",
			edge.SourceNodeID,
			edge.DestinationNodeID,
			edge.RelationshipType,
			edge.CloudAccountID,
			edge.TenantID)

		if existing, exists := edgeMap[compositeKey]; exists {
			// Merge properties if the edge already exists
			for k, v := range edge.Properties {
				existing.Properties[k] = v
			}
			existing.UpdatedAt = time.Now()
		} else {
			edgeMap[compositeKey] = edge
		}
	}

	// Convert map back to slice
	validEdges := make([]*KnowledgeGraphEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		validEdges = append(validEdges, edge)
	}

	if len(validEdges) == 0 {
		slog.Info("No valid edges to save after node resolution")
		return nil
	}

	// Prepare data for bulk insert
	cols := []string{"id", "created_at", "updated_at", "source_node_id", "destination_node_id", "relationship_type", "properties", "cloud_account_id", "tenant_id"}
	values := make([][]any, len(validEdges))

	now := time.Now()
	for i, edge := range validEdges {
		// Generate ID if not present
		if edge.ID == "" {
			edge.ID = uuid.New().String()
		}

		// Set timestamps if not present
		if edge.CreatedAt.IsZero() {
			edge.CreatedAt = now
		}
		edge.UpdatedAt = now

		// Convert properties to JSON
		propertiesJSON, err := json.Marshal(edge.Properties)
		if err != nil {
			return fmt.Errorf("failed to marshal edge properties: %w", err)
		}

		values[i] = []any{
			edge.ID,
			edge.CreatedAt,
			edge.UpdatedAt,
			edge.SourceNodeID,      // Now contains actual database ID
			edge.DestinationNodeID, // Now contains actual database ID
			string(edge.RelationshipType),
			propertiesJSON,
			edge.CloudAccountID,
			edge.TenantID,
		}
	}

	// Use database manager's bulk insert with conflict resolution
	onConflict := []string{"source_node_id", "destination_node_id", "relationship_type", "cloud_account_id", "tenant_id"}
	onConflictUpdate := []string{"updated_at", "properties"}

	_, err = s.dbManager.Insert(nil, "knowledge_graph_edge", onConflict, onConflictUpdate, nil, cols, values...)
	if err != nil {
		return fmt.Errorf("failed to upsert edges: %w", err)
	}

	slog.Info("Successfully saved edges to knowledge graph", "count", len(validEdges))
	return nil
}

// processServiceForKnowledgeGraph processes a single service: builds KG, saves to DB, and returns result
func (s *KnowledgeGraphService) ProcessServiceForKnowledgeGraph(
	ctxt *security.RequestContext,
	serviceName string,
	cloudAccountID string,
	tenantID string,
) *ServiceRefreshResult {
	serviceStartTime := time.Now()
	serviceResult := &ServiceRefreshResult{
		ServiceName: serviceName,
	}

	slog.Info("Processing service for knowledge graph",
		"service", serviceName,
		"service_start_time", serviceStartTime)

	// Build knowledge graph from traces for this service
	nodes, edges, err := BuildKnowledgeGraphFromTraces(ctxt, serviceName, cloudAccountID, tenantID)
	if err != nil {
		serviceResult.Error = err.Error()
		slog.Error("Failed to build knowledge graph for service",
			"service", serviceName,
			"error", err,
			"duration_ms", time.Since(serviceStartTime).Milliseconds())
		return serviceResult
	}

	serviceResult.NodesCount = len(nodes)
	serviceResult.EdgesCount = len(edges)

	// Save nodes to database
	if len(nodes) > 0 {
		err = s.SaveNodes(nodes)
		if err != nil {
			serviceResult.Error = fmt.Sprintf("Failed to save nodes: %v", err)
			slog.Error("Failed to save nodes for service",
				"service", serviceName,
				"error", err,
				"duration_ms", time.Since(serviceStartTime).Milliseconds())
			return serviceResult
		}
	}

	// Save edges to database
	if len(edges) > 0 {
		err = s.SaveEdges(edges)
		if err != nil {
			serviceResult.Error = fmt.Sprintf("Failed to save edges: %v", err)
			slog.Error("Failed to save edges for service",
				"service", serviceName,
				"error", err,
				"duration_ms", time.Since(serviceStartTime).Milliseconds())
			return serviceResult
		}
	}

	// Update last_sync_at for this service
	err = s.updateServiceLastSync(tenantID, cloudAccountID, serviceName)
	if err != nil {
		slog.Warn("Failed to update last_sync_at for service",
			"service", serviceName,
			"error", err)
	}

	serviceResult.Success = true

	serviceDuration := time.Since(serviceStartTime)
	slog.Info("Successfully processed service",
		"service", serviceName,
		"nodes", len(nodes),
		"edges", len(edges),
		"duration_ms", serviceDuration.Milliseconds(),
		"duration_sec", serviceDuration.Seconds())

	return serviceResult
}

// RefreshKnowledgeGraph refreshes the knowledge graph by fetching services from metadata
// and building knowledge graph from their traces. Processes only 10 services at a time.
func (s *KnowledgeGraphService) RefreshKnowledgeGraph(ctx context.Context, request RefreshKnowledgeGraphRequest) (*RefreshKnowledgeGraphResponse, error) {
	startTime := time.Now()
	slog.Info("Starting knowledge graph refresh",
		"tenant_id", request.TenantID,
		"cloud_account_id", request.CloudAccountID,
		"start_time", startTime)

	// Initialize response
	response := &RefreshKnowledgeGraphResponse{
		ServiceResults: make(map[string]*ServiceRefreshResult),
	}

	// Get services from metadata table that need sync (oldest first, limit 10)
	services, err := s.getServicesForSync(request.TenantID, request.CloudAccountID, 100)
	if err != nil {
		response.ErrorMessage = fmt.Sprintf("Failed to fetch services from metadata: %v", err)
		return response, err
	}

	slog.Info("Found services to process",
		"count", len(services),
		"batch_size", 10)

	// Create security context for trace queries
	ctxt := security.NewRequestContextForTenantAdmin(request.TenantID, nil, nil, nil)

	// Process each service
	var totalNodes, totalEdges int
	successCount := 0
	traceProvider, _, err := observability.GetLogsMetricsTracesProvider(ctxt, request.CloudAccountID, "", "traces", "")
	if err != nil {
		response.ErrorMessage = fmt.Sprintf("Failed to get default traces provider: %v", err)
		return response, err
	}
	if len(services) == 0 && traceProvider != "datadog" {
		slog.Info("No services found that need sync",
			"tenant_id", request.TenantID,
			"cloud_account_id", request.CloudAccountID)
		response.Success = true
		return response, nil
	}
	if traceProvider == "datadog" {
		serviceStartTime := time.Now()
		apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctxt, request.CloudAccountID)
		if err != nil {
			response.ErrorMessage = fmt.Sprintf("Failed to get Datadog configs: %v", err)
			return response, err
		}
		apiConfig := DatadogAPIConfig{
			APIKey:         apiKey,
			ApplicationKey: appKey,
			Site:           site,
		}
		serviceMap, err := BuildServiceMapFromDatadogAPIs(&apiConfig, request.CloudAccountID, request.TenantID)
		if err != nil {
			response.ErrorMessage = fmt.Sprintf("Failed to build service map from Datadog APIs: %v", err)
			slog.Error("Failed to build service map from Datadog APIs",
				"error", err,
				"duration_ms", time.Since(serviceStartTime).Milliseconds())
			return response, err
		}
		// Step 2: Convert service map to knowledge graph nodes and edges
		// This now includes K8s infrastructure extracted during service map building
		nodes, edges := ConvertServiceMapToKnowledgeGraph(serviceMap, request.CloudAccountID, request.TenantID)
		successCount = 1
		// Save nodes to database
		serviceError := ""
		if len(nodes) > 0 {
			err = s.SaveNodes(nodes)
			if err != nil {
				serviceError = fmt.Sprintf("Failed to save nodes: %v", err)
				slog.Error("Failed to save nodes for service",
					"error", err,
					"duration_ms", time.Since(serviceStartTime).Milliseconds())
			}
		}

		// Save edges to database
		if len(edges) > 0 {
			err = s.SaveEdges(edges)
			if err != nil {
				serviceError = fmt.Sprintf("Failed to save edges: %v", err)
				slog.Error("Failed to save edges for service",
					"error", err,
					"duration_ms", time.Since(serviceStartTime).Milliseconds())
			}
		}
		serviceDuration := time.Since(serviceStartTime)
		slog.Info("Successfully processed service",
			"nodes", len(nodes),
			"edges", len(edges),
			"duration_ms", serviceDuration.Milliseconds(),
			"duration_sec", serviceDuration.Seconds())
		for _, service := range serviceMap.Applications {
			serviceResult := &ServiceRefreshResult{
				ServiceName: service.Id.Name,
				Success:     true,
				NodesCount:  1,
				EdgesCount:  len(edges),
				Error:       serviceError,
			}
			response.ServiceResults[service.Id.Name] = serviceResult
		}
		totalNodes += len(nodes)
		totalEdges += len(edges)
		totalDuration := time.Since(startTime)
		response.Success = successCount > 0
		response.ProcessedCount = successCount
		response.TotalNodes = totalNodes
		response.TotalEdges = totalEdges

		if successCount == 0 {
			response.ErrorMessage = "Failed to process any services"
		}

		slog.Info("Completed knowledge graph refresh",
			"tenant_id", request.TenantID,
			"cloud_account_id", request.CloudAccountID,
			"processed_count", successCount,
			"total_services", len(services),
			"total_nodes", totalNodes,
			"total_edges", totalEdges,
			"total_duration_ms", totalDuration.Milliseconds(),
			"total_duration_sec", totalDuration.Seconds(),
			"avg_duration_per_service_ms", totalDuration.Milliseconds()/int64(max(len(serviceMap.Applications), 1)))

		slog.Info("Knowledge graph refresh completed",
			"processed_count", successCount,
			"total_services", len(services),
			"total_nodes", totalNodes,
			"total_edges", totalEdges)

		return response, nil

	}
	for _, serviceName := range services {
		serviceResult := s.ProcessServiceForKnowledgeGraph(ctxt, serviceName, request.CloudAccountID, request.TenantID)
		response.ServiceResults[serviceName] = serviceResult

		if serviceResult.Success {
			successCount++
			totalNodes += serviceResult.NodesCount
			totalEdges += serviceResult.EdgesCount
		}
	}
	totalDuration := time.Since(startTime)
	response.Success = successCount > 0
	response.ProcessedCount = successCount
	response.TotalNodes = totalNodes
	response.TotalEdges = totalEdges

	if successCount == 0 {
		response.ErrorMessage = "Failed to process any services"
	}

	slog.Info("Completed knowledge graph refresh",
		"tenant_id", request.TenantID,
		"cloud_account_id", request.CloudAccountID,
		"processed_count", successCount,
		"total_services", len(services),
		"total_nodes", totalNodes,
		"total_edges", totalEdges,
		"total_duration_ms", totalDuration.Milliseconds(),
		"total_duration_sec", totalDuration.Seconds(),
		"avg_duration_per_service_ms", totalDuration.Milliseconds()/int64(max(len(services), 1)))

	slog.Info("Knowledge graph refresh completed",
		"processed_count", successCount,
		"total_services", len(services),
		"total_nodes", totalNodes,
		"total_edges", totalEdges)

	return response, nil
}

// GetNodesByTenant retrieves all nodes for a specific tenant and account
func (s *KnowledgeGraphService) GetNodesByTenant(cloudAccountID, tenantID string) ([]*KnowledgeGraphNode, error) {
	query := `
		SELECT id, created_at, updated_at, properties, cloud_account_id, tenant_id, unique_key
		FROM knowledge_graph_node 
		WHERE cloud_account_id = $1 AND tenant_id = $2 AND level = 'Account'
		ORDER BY created_at DESC
	`

	rows, err := s.dbManager.Query(query, cloudAccountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var nodes []*KnowledgeGraphNode
	for rows.Next() {
		node := &KnowledgeGraphNode{}
		var propertiesJSON []byte

		err := rows.Scan(
			&node.ID,
			&node.CreatedAt,
			&node.UpdatedAt,
			&propertiesJSON,
			&node.CloudAccountID,
			&node.TenantID,
			&node.UniqueKey,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &node.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal node properties: %w", err)
		}

		// Extract node type from unique key (format: "NodeType:name:environment")
		if parts := strings.Split(node.UniqueKey, ":"); len(parts) > 0 {
			node.NodeType = NodeType(parts[0])
		}

		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	return nodes, nil
}

// GetEdgesByTenant retrieves all edges for a specific tenant and account
func (s *KnowledgeGraphService) GetEdgesByTenant(cloudAccountID, tenantID string) ([]*KnowledgeGraphEdge, error) {
	query := `
		SELECT 
			e.id, e.created_at, e.updated_at, e.relationship_type, e.properties, 
			e.cloud_account_id, e.tenant_id,
			sn.unique_key as source_unique_key,
			dn.unique_key as destination_unique_key
		FROM knowledge_graph_edge e
		JOIN knowledge_graph_node sn ON e.source_node_id = sn.id
		JOIN knowledge_graph_node dn ON e.destination_node_id = dn.id
		WHERE e.cloud_account_id = $1
		AND e.tenant_id = $2
		AND e.level = 'Account'
		AND sn.level = 'Account'
		AND dn.level = 'Account'
		ORDER BY e.created_at DESC
	`

	rows, err := s.dbManager.Query(query, cloudAccountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query edges: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var edges []*KnowledgeGraphEdge
	for rows.Next() {
		edge := &KnowledgeGraphEdge{}
		var propertiesJSON []byte
		var relationshipType string

		err := rows.Scan(
			&edge.ID,
			&edge.CreatedAt,
			&edge.UpdatedAt,
			&relationshipType,
			&propertiesJSON,
			&edge.CloudAccountID,
			&edge.TenantID,
			&edge.SourceNodeID,      // Actually unique_key from join
			&edge.DestinationNodeID, // Actually unique_key from join
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan edge: %w", err)
		}

		// Parse properties JSON
		if err := json.Unmarshal(propertiesJSON, &edge.Properties); err != nil {
			return nil, fmt.Errorf("failed to unmarshal edge properties: %w", err)
		}

		edge.RelationshipType = RelationshipType(relationshipType)
		edges = append(edges, edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating edge rows: %w", err)
	}

	return edges, nil
}

// GetServiceDependencies returns services and their dependencies for visualization
func (s *KnowledgeGraphService) GetServiceDependencies(cloudAccountID, tenantID string) (map[string][]string, error) {
	query := `
		SELECT 
			sn.unique_key as source_service,
			dn.unique_key as destination_service,
			e.relationship_type
		FROM knowledge_graph_edge e
		JOIN knowledge_graph_node sn ON e.source_node_id = sn.id
		JOIN knowledge_graph_node dn ON e.destination_node_id = dn.id
		WHERE e.cloud_account_id = $1 AND e.tenant_id = $2
		AND sn.unique_key LIKE 'Service:%'
		AND e.relationship_type IN ('CALLS', 'USES')
		AND e.level = 'Account'
		AND sn.level = 'Account'
		AND dn.level = 'Account'
		ORDER BY sn.unique_key
	`

	rows, err := s.dbManager.Query(query, cloudAccountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query service dependencies: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	dependencies := make(map[string][]string)
	for rows.Next() {
		var source, destination, relationshipType string
		if err := rows.Scan(&source, &destination, &relationshipType); err != nil {
			return nil, fmt.Errorf("failed to scan dependency: %w", err)
		}

		dependencies[source] = append(dependencies[source], destination)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dependency rows: %w", err)
	}

	return dependencies, nil
}

// SaveKnowledgeGraph saves both nodes and edges in a single operation
func (s *KnowledgeGraphService) SaveKnowledgeGraph(nodes []*KnowledgeGraphNode, edges []*KnowledgeGraphEdge) error {
	// Save nodes first
	if err := s.SaveNodes(nodes); err != nil {
		return fmt.Errorf("failed to save nodes: %w", err)
	}

	// Then save edges
	if err := s.SaveEdges(edges); err != nil {
		return fmt.Errorf("failed to save edges: %w", err)
	}

	slog.Info("Successfully saved complete knowledge graph",
		"nodes", len(nodes),
		"edges", len(edges))

	return nil
}

// BuildServiceMapFromNodes converts knowledge graph nodes and edges to a ServiceMap
// This is the core transformation logic that can be used independently of database access
func (s *KnowledgeGraphService) BuildServiceMapFromNodes(nodes []*KnowledgeGraphNode, edges []*KnowledgeGraphEdge) (*ServiceMap, error) {
	if len(nodes) == 0 {
		slog.Warn("BuildServiceMapFromNodes called with empty nodes")
		return &ServiceMap{
			Applications: []ServiceApplication{},
			GeneratedAt:  time.Now(),
		}, nil
	}

	slog.Info("Building service map from knowledge graph nodes",
		"nodes_count", len(nodes),
		"edges_count", len(edges))

	// Build service map structure
	serviceMap := &ServiceMap{
		Applications: []ServiceApplication{},
		GeneratedAt:  time.Now(),
	}

	// Create maps for quick lookup
	// Note: Edges can reference nodes by either ID or UniqueKey, so we index by both
	nodeMap := make(map[string]*KnowledgeGraphNode)
	for _, node := range nodes {
		nodeMap[node.ID] = node        // Index by UUID
		nodeMap[node.UniqueKey] = node // Index by UniqueKey for backward compatibility
	}

	// Build deduplication map: DNS name -> node type (prioritize LoadBalancer over ExternalService)
	dnsToNodeType := make(map[string]NodeType)
	// Also build a map from ExternalService name -> LoadBalancer node for redirect
	externalServiceToLoadBalancer := make(map[string]*KnowledgeGraphNode)

	for _, node := range nodes {
		if node.NodeType == NodeTypeLoadBalancer {
			if dnsName, ok := node.Properties["dns_name"].(string); ok && dnsName != "" {
				dnsToNodeType[dnsName] = NodeTypeLoadBalancer
				// Map this DNS name to the LoadBalancer node for redirection
				externalServiceToLoadBalancer[dnsName] = node
			}
		}
	}

	// Group nodes by service (including external services)
	serviceGroups := make(map[string]*ServiceApplication)

	for _, node := range nodes {
		// Include Service, ExternalService, LoadBalancer, Pod, and infrastructure nodes (Cache, Database, MessageQueue, ElastiCache)
		if node.NodeType == NodeTypeService ||
			node.NodeType == NodeTypeExternalService ||
			node.NodeType == NodeTypeLoadBalancer ||
			node.NodeType == NodeTypePod ||
			node.NodeType == NodeTypeCache ||
			node.NodeType == NodeTypeDatabase ||
			node.NodeType == NodeTypeMessageQueue {
			// Skip ExternalService nodes if there's a LoadBalancer with the same DNS name (deduplication)
			if node.NodeType == NodeTypeExternalService {
				if name, ok := node.Properties["name"].(string); ok && name != "" {
					if nodeType, exists := dnsToNodeType[name]; exists && nodeType == NodeTypeLoadBalancer {
						slog.Info("Skipping ExternalService node - LoadBalancer exists with same DNS",
							"external_service", name)
						continue
					}
				}
			}

			// For LoadBalancer nodes, prefer dns_name over name for consistency with external service references
			serviceName := ""
			if node.NodeType == NodeTypeLoadBalancer {
				if dnsName, ok := node.Properties["dns_name"].(string); ok && dnsName != "" {
					serviceName = dnsName
				}
			}

			// For Pod nodes, use owner_kind instead of Pod if available (group by Deployment/StatefulSet)
			// This ensures pods are grouped by their owners in the service map
			actualNodeType := node.NodeType
			if node.NodeType == NodeTypePod {
				if ownerKind, ok := node.Properties["owner_kind"].(string); ok && ownerKind != "" {
					// This is an owner node (Deployment/StatefulSet/DaemonSet), not a raw Pod
					actualNodeType = NodeType(ownerKind)
				}
			}

			// Fall back to name property for all node types
			if serviceName == "" {
				if name, ok := node.Properties["name"].(string); ok && name != "" {
					serviceName = name
				}
			}

			if serviceName == "" {
				slog.Warn("Skipping service node with invalid or missing name", "node_id", node.ID, "properties", node.Properties)
				continue
			}

			environment := ""
			if env, exists := node.Properties["environment"]; exists && env != nil {
				if envStr, ok := env.(string); ok {
					environment = envStr
				}
			}

			namespace := ""
			if ns, exists := node.Properties["namespace"]; exists && ns != nil {
				if nsStr, ok := ns.(string); ok {
					namespace = nsStr
				}
			}

			serviceKey := fmt.Sprintf("%s:%s", serviceName, environment)
			if _, exists := serviceGroups[serviceKey]; !exists {
				category := "application"
				nodeKind := "Service"
				serviceType := []string{"Service"}
				indicators := []string{}

				if node.NodeType == NodeTypeExternalService {
					category = "external"
					nodeKind = "ExternalService"
					serviceType = []string{} // Will be detected later
					indicators = []string{"external"}
				} else if node.NodeType == NodeTypeLoadBalancer {
					category = "infrastructure"
					nodeKind = "LoadBalancer"
					serviceType = []string{} // Will be detected later (e.g., aws-alb, aws-nlb)
					indicators = []string{"infrastructure", "loadbalancer"}
				} else if actualNodeType == NodeTypePod {
					category = "infrastructure"
					nodeKind = "Pod"
					serviceType = []string{} // Pods don't have application type
					indicators = []string{"infrastructure", "pod"}
				} else if actualNodeType == "Deployment" || actualNodeType == "StatefulSet" || actualNodeType == "DaemonSet" {
					// Owner-based nodes (Deployment/StatefulSet/DaemonSet) that group pods
					category = "infrastructure"
					nodeKind = string(actualNodeType)
					serviceType = []string{}
					indicators = []string{"infrastructure", strings.ToLower(string(actualNodeType))}
				} else if node.NodeType == NodeTypeCache || node.NodeType == NodeTypeDatabase || node.NodeType == NodeTypeMessageQueue {
					// Map infrastructure nodes to ExternalService kind to match original service map format
					category = "external"
					nodeKind = "ExternalService"
					serviceType = []string{} // Will be detected later (e.g., redis, postgres, kafka)
					indicators = []string{"external"}
				}

				// Extract labels from node properties
				labels := make(map[string]string)

				// If there's a labels sub-object, start with that (contains all Prometheus metric labels)
				if labelsProp, exists := node.Properties["labels"]; exists {
					if labelsMap, ok := labelsProp.(map[string]string); ok {
						for k, v := range labelsMap {
							labels[k] = v
						}
					} else if labelsIface, ok := labelsProp.(map[string]interface{}); ok {
						for k, v := range labelsIface {
							if vStr, ok := v.(string); ok {
								labels[k] = vStr
							}
						}
					}
				}

				// Add/override with commonly-used top-level properties
				// This ensures consistency: top-level properties take precedence over labels sub-object

				if lang, ok := node.Properties["programming_language"].(string); ok && lang != "" {
					labels["programming_language"] = lang
				}
				if runtime, ok := node.Properties["runtime_version"].(string); ok && runtime != "" {
					labels["runtime_version"] = runtime
				}
				if cluster, ok := node.Properties["cluster"].(string); ok && cluster != "" {
					labels["cluster"] = cluster
				}
				if k8sCluster, ok := node.Properties["k8s_cluster"].(string); ok && k8sCluster != "" {
					labels["k8s_cluster"] = k8sCluster
				}
				if nodeVal, ok := node.Properties["node"].(string); ok && nodeVal != "" {
					labels["node"] = nodeVal
				}
				if hostIP, ok := node.Properties["host_ip"].(string); ok && hostIP != "" {
					labels["host_ip"] = hostIP
				}
				if env, ok := node.Properties["environment"].(string); ok && env != "" {
					labels["environment"] = env
				}
				if ns, ok := node.Properties["namespace"].(string); ok && ns != "" {
					labels["namespace"] = ns
				}

				// Add cloud resource identifiers if available (for LoadBalancers, RDS, ElastiCache, etc.)
				if arn, ok := node.Properties["arn"].(string); ok && arn != "" {
					labels["arn"] = arn
				}
				if nbResourceID, ok := node.Properties["nb_resource_id"].(string); ok && nbResourceID != "" {
					labels["nb_resource_id"] = nbResourceID
				}
				if nbAccountID, ok := node.Properties["nb_account_id"].(string); ok && nbAccountID != "" {
					labels["nb_account_id"] = nbAccountID
				}
				if awsAccountID, ok := node.Properties["aws_account_id"].(string); ok && awsAccountID != "" {
					labels["aws_account_id"] = awsAccountID // UUID for backward compatibility
				}
				if awsAccountNumber, ok := node.Properties["aws_account_number"].(string); ok && awsAccountNumber != "" {
					labels["aws_account_number"] = awsAccountNumber // Actual 12-digit AWS account number
				}
				if region, ok := node.Properties["region"].(string); ok && region != "" {
					labels["region"] = region
				}

				// Add Ingress-related properties for services exposed via Ingress
				if ingressName, ok := node.Properties["ingress_name"].(string); ok && ingressName != "" {
					labels["ingress_name"] = ingressName
				}
				if ingressHost, ok := node.Properties["ingress_host"].(string); ok && ingressHost != "" {
					labels["ingress_host"] = ingressHost
				}
				if ingressPath, ok := node.Properties["ingress_path"].(string); ok && ingressPath != "" {
					labels["ingress_path"] = ingressPath
				}
				if backendPort, ok := node.Properties["backend_port"].(int); ok && backendPort > 0 {
					labels["backend_port"] = fmt.Sprintf("%d", backendPort)
				}
				if exposedVia, ok := node.Properties["exposed_via"].(string); ok && exposedVia != "" {
					labels["exposed_via"] = exposedVia
				}
				if ingressClass, ok := node.Properties["ingress_class"].(string); ok && ingressClass != "" {
					labels["ingress_class"] = ingressClass
				}

				// Add service type (e.g., "nginx", "kafka", "redis")
				if serviceType, ok := node.Properties["type"].(string); ok && serviceType != "" {
					labels["type"] = serviceType
				}

				// Add DNS and endpoint properties
				if dnsName, ok := node.Properties["dns_name"].(string); ok && dnsName != "" {
					labels["dns_name"] = dnsName
				}
				if endpoint, ok := node.Properties["endpoint"].(string); ok && endpoint != "" {
					labels["endpoint"] = endpoint
				}
				if cacheEndpoint, ok := node.Properties["cache_endpoint"].(string); ok && cacheEndpoint != "" {
					labels["cache_endpoint"] = cacheEndpoint
				}
				if rdsEndpoint, ok := node.Properties["rds_endpoint"].(string); ok && rdsEndpoint != "" {
					labels["rds_endpoint"] = rdsEndpoint
				}

				// Add Pod-specific properties
				if podName, ok := node.Properties["pod_name"].(string); ok && podName != "" {
					labels["pod_name"] = podName
				}
				if podIP, ok := node.Properties["pod_ip"].(string); ok && podIP != "" {
					labels["pod_ip"] = podIP
				}
				if ownerKind, ok := node.Properties["owner_kind"].(string); ok && ownerKind != "" {
					labels["owner_kind"] = ownerKind
				}
				if ownerName, ok := node.Properties["owner_name"].(string); ok && ownerName != "" {
					labels["owner_name"] = ownerName
				}

				// Add resource type for cloud resources
				if resourceType, ok := node.Properties["resource_type"].(string); ok && resourceType != "" {
					labels["resource_type"] = resourceType
				}

				// Add protocol information
				if protocol, ok := node.Properties["protocol"].(string); ok && protocol != "" {
					labels["protocol"] = protocol
				}

				// Detect application type from node properties and labels
				// This will give us specific types like "redis", "kafka", "java", "python", "aws-alb", etc.
				// Note: We skip Pods as they don't have their own application type
				if node.NodeType != NodeTypePod {
					detectedType := s.detectNodeApplicationType(node, labels)
					if len(detectedType) > 0 {
						serviceType = detectedType
					}
				}

				serviceGroups[serviceKey] = &ServiceApplication{
					Id: ServiceApplicationId{
						Name:      serviceName,
						Kind:      nodeKind,
						Namespace: namespace,
					},
					Category:         ServiceCategory{Category: category},
					Labels:           labels,
					Indicators:       indicators,
					Upstreams:        []UpstreamLink{},
					Downstreams:      []DownstreamLink{},
					Instances:        []Instance{},
					Type:             serviceType,
					IsHealthy:        true,
					DesiredInstances: 1,
				}
			}
		}
	}

	// Build a map of service dependencies with metrics from edges
	serviceDependencies := make(map[string]map[string]*ServiceDependencyMetrics)

	for _, edge := range edges {
		sourceNode := nodeMap[edge.SourceNodeID]
		destNode := nodeMap[edge.DestinationNodeID]

		if sourceNode == nil || destNode == nil {
			continue
		}

		// Process service call relationships
		// Include calls to infrastructure nodes (Cache, Database, MessageQueue)
		if edge.RelationshipType == RelationshipCalls &&
			(sourceNode.NodeType == NodeTypeService || sourceNode.NodeType == NodeTypeExternalService) &&
			(destNode.NodeType == NodeTypeService || destNode.NodeType == NodeTypeExternalService ||
				destNode.NodeType == NodeTypeCache || destNode.NodeType == NodeTypeDatabase || destNode.NodeType == NodeTypeMessageQueue) {

			// Check if dest node is an ExternalService that was replaced by a LoadBalancer
			effectiveDestNode := destNode
			if destNode.NodeType == NodeTypeExternalService {
				if destName, ok := destNode.Properties["name"].(string); ok {
					if lbNode, exists := externalServiceToLoadBalancer[destName]; exists {
						// Redirect to LoadBalancer
						slog.Info("Redirecting CALLS edge destination from ExternalService to LoadBalancer",
							"original_dest", destName,
							"loadbalancer", lbNode.Properties["dns_name"])
						effectiveDestNode = lbNode
					}
				}
			}

			sourceKey := s.getServiceKey(sourceNode)
			destKey := s.getServiceKey(effectiveDestNode)

			// Debug logging to trace the edge direction
			if sourceNode.Properties["name"] != nil && effectiveDestNode.Properties["name"] != nil {
				slog.Info("Processing service call edge",
					"source_service", sourceNode.Properties["name"],
					"dest_service", effectiveDestNode.Properties["name"],
					"source_key", sourceKey,
					"dest_key", destKey,
					"edge_id", edge.ID)
			}

			if serviceDependencies[sourceKey] == nil {
				serviceDependencies[sourceKey] = make(map[string]*ServiceDependencyMetrics)
			}

			// Extract metrics from edge properties
			metrics := s.extractMetricsFromEdge(edge)
			serviceDependencies[sourceKey][destKey] = metrics
		}

		// Process RESOLVES_TO relationships (ExternalService -> LoadBalancer)
		// Skip this because ExternalServices that resolve to LoadBalancers are deduplicated
		// The LoadBalancer node already represents the target
		if edge.RelationshipType == RelationshipResolvesTo &&
			sourceNode.NodeType == NodeTypeExternalService &&
			destNode.NodeType == NodeTypeLoadBalancer {
			// Skip - these ExternalServices are already deduplicated
			continue
		}

		// Process RESOLVES_TO relationships (ExternalService -> Cache/Database/etc.)
		// This handles DNS resolution from external services to inferred cloud resources
		if edge.RelationshipType == RelationshipResolvesTo &&
			sourceNode.NodeType == NodeTypeExternalService &&
			(destNode.NodeType == NodeTypeCache ||
				destNode.NodeType == NodeTypeDatabase ||
				destNode.NodeType == NodeTypeMessageQueue ||
				(destNode.NodeType == NodeTypeExternalService && destNode.Properties["inferred"] == true)) {

			sourceKey := s.getServiceKey(sourceNode)
			destKey := s.getServiceKey(destNode)

			if sourceNode.Properties["name"] != nil && destNode.Properties["name"] != nil {
				slog.Info("Processing DNS resolution edge (RESOLVES_TO)",
					"dns_name", sourceNode.Properties["name"],
					"resolved_endpoint", destNode.Properties["name"],
					"resource_type", destNode.NodeType,
					"source_key", sourceKey,
					"dest_key", destKey,
					"edge_id", edge.ID)
			}

			// DNS name depends on resolved endpoint (resolved endpoint is upstream)
			if serviceDependencies[sourceKey] == nil {
				serviceDependencies[sourceKey] = make(map[string]*ServiceDependencyMetrics)
			}

			// Extract metrics from edge properties or create basic metrics
			metrics := s.extractMetricsFromEdge(edge)
			if metrics.Protocol == "" {
				// Infer protocol from cloud resource type
				switch destNode.NodeType {
				case NodeTypeCache:
					metrics.Protocol = "REDIS"
				case NodeTypeDatabase:
					metrics.Protocol = "SQL"
				case NodeTypeMessageQueue:
					metrics.Protocol = "AMQP"
				}
			}
			serviceDependencies[sourceKey][destKey] = metrics
		}

		// Process ROUTES_TO relationships (LoadBalancer -> Service)
		// LoadBalancer routes traffic TO Service, so Service is LoadBalancer's upstream (dependency)
		// and LoadBalancer is Service's downstream (caller)
		if edge.RelationshipType == RelationshipRoutesTo &&
			destNode.NodeType == NodeTypeService {

			sourceKey := s.getServiceKey(sourceNode)
			destKey := s.getServiceKey(destNode)

			if sourceNode.Properties["name"] != nil && destNode.Properties["name"] != nil {
				slog.Info("Processing load balancer routing edge",
					"load_balancer", sourceNode.Properties["name"],
					"backend_service", destNode.Properties["name"],
					"source_key", sourceKey,
					"dest_key", destKey,
					"edge_id", edge.ID)
			}

			// LoadBalancer depends on Service (Service is LoadBalancer's upstream)
			if serviceDependencies[sourceKey] == nil {
				serviceDependencies[sourceKey] = make(map[string]*ServiceDependencyMetrics)
			}

			// Create basic metrics for load balancer routing
			metrics := &ServiceDependencyMetrics{
				Protocol: "HTTP",
			}
			serviceDependencies[sourceKey][destKey] = metrics
		}

		// Process ROUTES_TO relationships (LoadBalancer -> Pod)
		// LoadBalancer routes traffic TO Pod, so Pod is LoadBalancer's upstream (dependency)
		// and LoadBalancer is Pod's downstream (caller)
		if edge.RelationshipType == RelationshipRoutesTo &&
			sourceNode.NodeType == NodeTypeLoadBalancer &&
			destNode.NodeType == NodeTypePod {

			sourceKey := s.getServiceKey(sourceNode)
			destKey := s.getServiceKey(destNode)

			if sourceNode.Properties["name"] != nil && destNode.Properties["name"] != nil {
				slog.Info("Processing load balancer to pod routing edge",
					"load_balancer", sourceNode.Properties["name"],
					"backend_pod", destNode.Properties["name"],
					"source_key", sourceKey,
					"dest_key", destKey,
					"edge_id", edge.ID)
			}

			// LoadBalancer depends on Pod (Pod is LoadBalancer's upstream)
			if serviceDependencies[sourceKey] == nil {
				serviceDependencies[sourceKey] = make(map[string]*ServiceDependencyMetrics)
			}

			// Create basic metrics for load balancer routing
			metrics := &ServiceDependencyMetrics{
				Protocol: "HTTP",
			}
			serviceDependencies[sourceKey][destKey] = metrics

			// Also add pod to LoadBalancer's infrastructure labels for quick reference
			if serviceApp, exists := serviceGroups[sourceKey]; exists {
				s.addInfrastructureLabels(serviceApp, destNode)
			}
		}

		// Handle infrastructure relationships (RUNS_ON)
		if sourceNode.NodeType == NodeTypeService && edge.RelationshipType == RelationshipRunsOn {
			sourceKey := s.getServiceKey(sourceNode)
			if serviceApp, exists := serviceGroups[sourceKey]; exists {
				s.addInfrastructureLabels(serviceApp, destNode)
			}
		}

		// Process ROUTES_THROUGH relationships (ExternalService -> ElastiCache/RDS/etc. from cloud_resources)
		// ExternalService routes traffic THROUGH cloud resource, so cloud resource is ExternalService's upstream
		if edge.RelationshipType == RelationshipRoutesThrough &&
			sourceNode.NodeType == NodeTypeExternalService &&
			(destNode.NodeType == NodeTypeCache ||
				destNode.NodeType == NodeTypeDatabase ||
				destNode.NodeType == NodeTypeMessageQueue) {

			sourceKey := s.getServiceKey(sourceNode)
			destKey := s.getServiceKey(destNode)

			if sourceNode.Properties["name"] != nil && destNode.Properties["name"] != nil {
				slog.Info("Processing external service to cloud resource edge (ROUTES_THROUGH)",
					"external_service", sourceNode.Properties["name"],
					"cloud_resource", destNode.Properties["name"],
					"cloud_resource_type", destNode.NodeType,
					"source_key", sourceKey,
					"dest_key", destKey,
					"edge_id", edge.ID)
			}

			// ExternalService depends on cloud resource (cloud resource is upstream)
			if serviceDependencies[sourceKey] == nil {
				serviceDependencies[sourceKey] = make(map[string]*ServiceDependencyMetrics)
			}

			// Extract metrics from edge properties or create basic metrics
			metrics := s.extractMetricsFromEdge(edge)
			if metrics.Protocol == "" {
				// Infer protocol from cloud resource type
				switch destNode.NodeType {
				case NodeTypeCache:
					metrics.Protocol = "REDIS"
				case NodeTypeDatabase:
					metrics.Protocol = "SQL"
				case NodeTypeMessageQueue:
					metrics.Protocol = "AMQP"
				}
			}
			serviceDependencies[sourceKey][destKey] = metrics
		}
	}

	// Build upstream and downstream relationships
	for sourceKey, dependencies := range serviceDependencies {
		if sourceService, exists := serviceGroups[sourceKey]; exists {
			for destKey, metrics := range dependencies {
				if destService, exists := serviceGroups[destKey]; exists {
					// Debug logging for service map building
					slog.Info("Building service map relationships",
						"source_service", sourceService.Id.Name,
						"dest_service", destService.Id.Name,
						"adding_upstream_to_source", true,
						"adding_downstream_to_dest", true)

					// Add upstream relationship for source service (dest is a dependency of source)
					upstream := s.createUpstreamLink(destService, metrics)
					sourceService.Upstreams = append(sourceService.Upstreams, upstream)

					// Add downstream relationship for destination service (source calls dest)
					downstream := s.createDownstreamLink(sourceService, metrics)
					destService.Downstreams = append(destService.Downstreams, downstream)
				}
			}
		}
	}

	// Convert service groups to slice
	for _, service := range serviceGroups {
		serviceMap.Applications = append(serviceMap.Applications, *service)
	}

	// Post-processing: Merge duplicate services with same name and cluster
	serviceMap.Applications = s.mergeDuplicateServices(serviceMap.Applications)

	// Post-processing: Update all upstream/downstream references to merged services
	serviceMap.Applications = s.updateServiceReferences(serviceMap.Applications)

	slog.Info("Successfully built service map from knowledge graph nodes",
		"services_count", len(serviceMap.Applications),
		"total_nodes", len(nodes),
		"total_edges", len(edges),
		"dependencies_count", len(serviceDependencies))

	return serviceMap, nil
}

// mergeDuplicateServices merges duplicate service entries with the same name and cluster
func (s *KnowledgeGraphService) mergeDuplicateServices(applications []ServiceApplication) []ServiceApplication {
	// Group services by name and cluster
	type serviceKey struct {
		name    string
		kind    string
		cluster string
	}

	serviceGroups := make(map[serviceKey][]*ServiceApplication)

	for i := range applications {
		app := &applications[i]
		cluster := ""
		if clusterLabel, ok := app.Labels["cluster"]; ok {
			cluster = clusterLabel
		}

		key := serviceKey{
			name:    app.Id.Name,
			kind:    app.Id.Kind,
			cluster: cluster,
		}

		serviceGroups[key] = append(serviceGroups[key], app)
	}

	// Merge duplicates
	result := make([]ServiceApplication, 0)

	for key, group := range serviceGroups {
		if len(group) == 1 {
			// No duplicates, keep as is
			result = append(result, *group[0])
			continue
		}

		// Multiple services with same name and cluster - merge them
		slog.Info("Merging duplicate services",
			"name", key.name,
			"kind", key.kind,
			"cluster", key.cluster,
			"count", len(group))

		// Start with the first service, prefer one with non-empty namespace
		var base *ServiceApplication
		for _, app := range group {
			if app.Id.Namespace != "" {
				base = app
				break
			}
		}
		if base == nil {
			base = group[0]
		}

		merged := *base

		// Merge upstreams (deduplicate by Id string)
		upstreamMap := make(map[string]UpstreamLink)
		for _, app := range group {
			for _, upstream := range app.Upstreams {
				if _, exists := upstreamMap[upstream.Id]; !exists {
					upstreamMap[upstream.Id] = upstream
				}
			}
		}
		merged.Upstreams = make([]UpstreamLink, 0, len(upstreamMap))
		for _, upstream := range upstreamMap {
			merged.Upstreams = append(merged.Upstreams, upstream)
		}

		// Merge downstreams (deduplicate by Id struct fields)
		downstreamMap := make(map[string]DownstreamLink)
		for _, app := range group {
			for _, downstream := range app.Downstreams {
				downstreamKey := fmt.Sprintf("%s:%s:%s", downstream.Id.Name, downstream.Id.Kind, downstream.Id.Namespace)
				if _, exists := downstreamMap[downstreamKey]; !exists {
					downstreamMap[downstreamKey] = downstream
				}
			}
		}
		merged.Downstreams = make([]DownstreamLink, 0, len(downstreamMap))
		for _, downstream := range downstreamMap {
			merged.Downstreams = append(merged.Downstreams, downstream)
		}

		// Merge labels (prefer non-empty values)
		for _, app := range group {
			for k, v := range app.Labels {
				if v != "" {
					merged.Labels[k] = v
				}
			}
		}

		result = append(result, merged)
	}

	return result
}

// updateServiceReferences deduplicates upstream/downstream references after service merging
func (s *KnowledgeGraphService) updateServiceReferences(applications []ServiceApplication) []ServiceApplication {
	// Build a canonical ID map for each service (name:kind:cluster -> canonical namespace)
	canonicalMap := make(map[string]string)
	for _, app := range applications {
		cluster := ""
		if clusterLabel, ok := app.Labels["cluster"]; ok {
			cluster = clusterLabel
		}
		key := fmt.Sprintf("%s:%s:%s", app.Id.Name, app.Id.Kind, cluster)
		// Use the namespace from the merged service (prefer non-empty)
		if app.Id.Namespace != "" || canonicalMap[key] == "" {
			canonicalMap[key] = app.Id.Namespace
		}
	}

	// Update all references
	for i := range applications {
		app := &applications[i]

		// Deduplicate and update upstreams
		upstreamMap := make(map[string]UpstreamLink)
		for _, upstream := range app.Upstreams {
			// Create canonical key based on name and kind
			// Extract cluster from upstream.Id string (format: "namespace:kind:name")
			upstreamKey := upstream.Id
			if _, exists := upstreamMap[upstreamKey]; !exists {
				upstreamMap[upstreamKey] = upstream
			}
		}
		app.Upstreams = make([]UpstreamLink, 0, len(upstreamMap))
		for _, upstream := range upstreamMap {
			app.Upstreams = append(app.Upstreams, upstream)
		}

		// Deduplicate and update downstreams
		downstreamMap := make(map[string]DownstreamLink)
		for _, downstream := range app.Downstreams {
			cluster := ""
			// Try to find cluster from the applications list
			for _, otherApp := range applications {
				if otherApp.Id.Name == downstream.Id.Name && otherApp.Id.Kind == downstream.Id.Kind {
					if clusterLabel, ok := otherApp.Labels["cluster"]; ok {
						cluster = clusterLabel
					}
					break
				}
			}

			// Create key based on name, kind, and cluster
			key := fmt.Sprintf("%s:%s:%s", downstream.Id.Name, downstream.Id.Kind, cluster)
			if _, exists := downstreamMap[key]; !exists {
				// Update namespace to canonical one
				if canonicalNs, ok := canonicalMap[key]; ok {
					downstream.Id.Namespace = canonicalNs
				}
				downstreamMap[key] = downstream
			}
		}
		app.Downstreams = make([]DownstreamLink, 0, len(downstreamMap))
		for _, downstream := range downstreamMap {
			app.Downstreams = append(app.Downstreams, downstream)
		}
	}

	return applications
}

// matchesFilter checks if a service application matches a given label filter
func (s *KnowledgeGraphService) matchesFilter(app *ServiceApplication, filter LabelFilter) bool {
	// Get the label value for the filter key
	labelValue, exists := app.Labels[filter.Key]
	if !exists {
		return false
	}

	// Apply operator-based matching
	switch filter.Operator {
	case query.Eq:
		return labelValue == filter.Value
	case query.Nq:
		return labelValue != filter.Value
	case query.Contains, query.Like:
		return strings.Contains(labelValue, filter.Value)
	case query.In:
		// For IN operator, filter.Value should be comma-separated values
		values := strings.Split(filter.Value, ",")
		for _, v := range values {
			if strings.TrimSpace(v) == labelValue {
				return true
			}
		}
		return false
	case query.NotIn:
		values := strings.Split(filter.Value, ",")
		for _, v := range values {
			if strings.TrimSpace(v) == labelValue {
				return false
			}
		}
		return true
	default:
		// Default to equality check
		return labelValue == filter.Value
	}
}

// applyServiceMapFilters filters service map applications based on label filters and exclude filters
func (s *KnowledgeGraphService) applyServiceMapFilters(serviceMap *ServiceMap, labelFilters, excludeFilters []LabelFilter) *ServiceMap {
	if len(labelFilters) == 0 && len(excludeFilters) == 0 {
		return serviceMap
	}

	filteredApps := make([]ServiceApplication, 0)
	serviceKeys := make(map[string]bool) // Track which services to keep

	for _, app := range serviceMap.Applications {
		// Check exclude filters first (OR logic - exclude if ANY filter matches)
		shouldExclude := false
		for _, filter := range excludeFilters {
			if s.matchesFilter(&app, filter) {
				shouldExclude = true
				slog.Debug("Excluding service from service map",
					"service", app.Id.Name,
					"filter_key", filter.Key,
					"filter_value", filter.Value)
				break
			}
		}
		if shouldExclude {
			continue
		}

		// Check include filters (AND logic - include only if ALL filters match)
		if len(labelFilters) > 0 {
			matchesAll := true
			for _, filter := range labelFilters {
				if !s.matchesFilter(&app, filter) {
					matchesAll = false
					break
				}
			}
			if !matchesAll {
				continue
			}
		}

		// Service passed all filters
		filteredApps = append(filteredApps, app)
		serviceKey := fmt.Sprintf("%s:%s:%s", app.Id.Namespace, app.Id.Kind, app.Id.Name)
		serviceKeys[serviceKey] = true
	}

	// When filters are applied, also include upstream/downstream services
	// This creates a proper neighborhood graph showing the filtered service and its dependencies
	neighborIds := make(map[string]bool) // Track upstream/downstream IDs to include

	// Collect all upstream/downstream service identifiers from filtered apps
	for _, app := range filteredApps {
		// Collect downstream service IDs (these have full ServiceApplicationId)
		for _, downstream := range app.Downstreams {
			neighborKey := fmt.Sprintf("%s:%s:%s", downstream.Id.Namespace, downstream.Id.Kind, downstream.Id.Name)
			neighborIds[neighborKey] = true
		}

		// Collect upstream service IDs (these are strings like ":Service:name")
		for _, upstream := range app.Upstreams {
			// Parse upstream ID format: ":Kind:Name" or ":ExternalService:Name"
			// Store as a lookup key to find matching services
			neighborIds[upstream.Id] = true
		}
	}

	// Add neighbor services that aren't already in the filtered list
	for _, app := range serviceMap.Applications {
		serviceKey := fmt.Sprintf("%s:%s:%s", app.Id.Namespace, app.Id.Kind, app.Id.Name)

		// Skip if already included in filtered apps
		if serviceKeys[serviceKey] {
			continue
		}

		// Check if this service is referenced as a downstream neighbor
		if neighborIds[serviceKey] {
			filteredApps = append(filteredApps, app)
			slog.Debug("Including downstream neighbor in filtered service map",
				"service", app.Id.Name,
				"kind", app.Id.Kind)
			continue
		}

		// Check if this service is referenced as an upstream neighbor
		// Upstream IDs use format ":Kind:Name"
		upstreamKey := fmt.Sprintf(":%s:%s", app.Id.Kind, app.Id.Name)
		if neighborIds[upstreamKey] {
			filteredApps = append(filteredApps, app)
			slog.Debug("Including upstream neighbor in filtered service map",
				"service", app.Id.Name,
				"kind", app.Id.Kind)
		}
	}

	slog.Info("Applied service map filters",
		"original_count", len(serviceMap.Applications),
		"filtered_count", len(filteredApps),
		"neighbor_count", len(filteredApps)-len(serviceKeys),
		"label_filters", len(labelFilters),
		"exclude_filters", len(excludeFilters))

	return &ServiceMap{
		Applications: filteredApps,
		GeneratedAt:  serviceMap.GeneratedAt,
		K8sMetadata:  serviceMap.K8sMetadata,
	}
}

// BuildServiceMapFromKnowledgeGraph builds a service map using knowledge graph data with proper upstream/downstream relationships
func (s *KnowledgeGraphService) BuildServiceMapFromKnowledgeGraph(ctx *security.RequestContext, cloudAccountID, tenantID string, labelFilters, excludeFilters []LabelFilter) (*ServiceMap, error) {
	slog.Info("Building service map from knowledge graph",
		"cloud_account_id", cloudAccountID,
		"tenant_id", tenantID,
		"label_filters_count", len(labelFilters),
		"exclude_filters_count", len(excludeFilters))

	// Get all nodes and edges from knowledge graph
	nodes, err := s.GetNodesByTenant(cloudAccountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	edges, err := s.GetEdgesByTenant(cloudAccountID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get edges: %w", err)
	}

	// Use the extracted helper function to build service map
	serviceMap, err := s.BuildServiceMapFromNodes(nodes, edges)
	if err != nil {
		return nil, err
	}

	// Apply label filters if provided
	if len(labelFilters) > 0 || len(excludeFilters) > 0 {
		serviceMap = s.applyServiceMapFilters(serviceMap, labelFilters, excludeFilters)
	}

	return serviceMap, nil
}

// Helper methods for service map building

// ServiceDependencyMetrics holds metrics data for service dependencies
type ServiceDependencyMetrics struct {
	RequestCount   int64
	FailureCount   int64
	TotalLatency   float64
	Protocol       string
	Operations     []string
	StatusCodes    map[int]int64
	SampleTraceIds []string
	FailedTraceIds []string
}

// getServiceKey generates a consistent key for service identification
func (s *KnowledgeGraphService) getServiceKey(node *KnowledgeGraphNode) string {
	// For LoadBalancer nodes, prefer dns_name over name for consistency with external service references
	serviceName := ""
	if node.NodeType == NodeTypeLoadBalancer {
		if dnsName, ok := node.Properties["dns_name"].(string); ok && dnsName != "" {
			serviceName = dnsName
		}
	}

	// Fall back to name property for all node types
	if serviceName == "" {
		if name, ok := node.Properties["name"].(string); ok && name != "" {
			serviceName = name
		}
	}

	if serviceName == "" {
		slog.Warn("Node missing name property for service key generation", "node_id", node.ID, "node_type", node.NodeType)
		return fmt.Sprintf("unknown:%s", node.ID) // Fallback using node ID
	}

	environment := ""
	if env, exists := node.Properties["environment"]; exists && env != nil {
		if envStr, ok := env.(string); ok {
			environment = envStr
		}
	}
	return fmt.Sprintf("%s:%s", serviceName, environment)
}

// detectNodeApplicationType detects the application type from node properties and labels
// Returns a single-element array with the specific technology type, or empty array if unknown
func (s *KnowledgeGraphService) detectNodeApplicationType(node *KnowledgeGraphNode, labels map[string]string) []string {
	serviceName := strings.ToLower(fmt.Sprintf("%v", node.Properties["name"]))

	// LoadBalancer detection (AWS specific)
	if node.NodeType == NodeTypeLoadBalancer {
		if arn, ok := node.Properties["arn"].(string); ok && arn != "" {
			if strings.Contains(arn, ":loadbalancer/app/") {
				return []string{"aws-alb"}
			}
			if strings.Contains(arn, ":loadbalancer/net/") {
				return []string{"aws-nlb"}
			}
			if strings.Contains(arn, ":loadbalancer/") {
				return []string{"aws-elb"}
			}
		}
		// Generic loadbalancer without specific type
		return []string{}
	}

	// Language/Runtime detection (highest priority)
	if sdkLang, ok := labels["telemetry.sdk.language"]; ok && sdkLang != "" {
		switch strings.ToLower(sdkLang) {
		case "java":
			return []string{"java"}
		case "python":
			return []string{"python"}
		case "javascript", "nodejs", "node":
			return []string{"nodejs"}
		case "go", "golang":
			return []string{"golang"}
		case "dotnet", "csharp", "c#":
			return []string{"dotnet"}
		case "php":
			return []string{"php"}
		case "ruby":
			return []string{"ruby"}
		}
	}

	// Check process.runtime.name
	if runtimeName, ok := labels["process.runtime.name"]; ok && runtimeName != "" {
		switch strings.ToLower(runtimeName) {
		case "node", "nodejs":
			return []string{"nodejs"}
		case "python":
			return []string{"python"}
		case "go":
			return []string{"golang"}
		case "java":
			return []string{"java"}
		case "dotnet", ".net":
			return []string{"dotnet"}
		case "php":
			return []string{"php"}
		case "ruby":
			return []string{"ruby"}
		}
	}

	// Database patterns
	dbPatterns := map[string]string{
		"postgres":      "postgres",
		"postgresql":    "postgres",
		"redis":         "redis",
		"mysql":         "mysql",
		"mongodb":       "mongodb",
		"mongo":         "mongodb",
		"elasticsearch": "elasticsearch",
		"elastic":       "elasticsearch",
		"clickhouse":    "clickhouse",
		"cassandra":     "cassandra",
		"opensearch":    "opensearch",
		"memcached":     "memcached",
	}

	for pattern, appType := range dbPatterns {
		if strings.Contains(serviceName, pattern) {
			return []string{appType}
		}
	}

	// Messaging patterns
	messagingPatterns := map[string]string{
		"kafka":     "kafka",
		"rabbitmq":  "rabbitmq",
		"activemq":  "activemq",
		"nats":      "nats",
		"pulsar":    "pulsar",
		"rocketmq":  "rocketmq",
		"zookeeper": "zookeeper",
	}

	for pattern, appType := range messagingPatterns {
		if strings.Contains(serviceName, pattern) {
			return []string{appType}
		}
	}

	// AWS Services
	if strings.Contains(serviceName, "rds") || strings.Contains(serviceName, "aws-rds") {
		return []string{"aws-rds"}
	}
	if strings.Contains(serviceName, "elasticache") || strings.Contains(serviceName, "aws-elasticache") {
		return []string{"aws-elasticache"}
	}
	if strings.Contains(serviceName, "s3") {
		return []string{"aws-s3"}
	}
	if strings.Contains(serviceName, "dynamodb") {
		return []string{"aws-dynamodb"}
	}
	if strings.Contains(serviceName, "sqs") {
		return []string{"aws-sqs"}
	}

	// Check protocol label for additional hints
	if protocol, ok := labels["protocol"]; ok && protocol != "" {
		switch strings.ToUpper(protocol) {
		case "REDIS":
			return []string{"redis"}
		case "POSTGRESQL", "POSTGRES":
			return []string{"postgres"}
		case "MYSQL":
			return []string{"mysql"}
		case "MONGODB":
			return []string{"mongodb"}
		case "KAFKA":
			return []string{"kafka"}
		}
	}

	// Default based on node type - return empty array (no specific type)
	// Note: We don't return generic categories like "cache", "database" as these
	// are already captured in Category and Indicators fields
	return []string{}
}

// extractMetricsFromEdge extracts metrics from an edge's properties
func (s *KnowledgeGraphService) extractMetricsFromEdge(edge *KnowledgeGraphEdge) *ServiceDependencyMetrics {
	metrics := &ServiceDependencyMetrics{
		RequestCount:   1, // Default to 1 call
		FailureCount:   0,
		TotalLatency:   0.0,
		Protocol:       "Unknown",
		Operations:     []string{},
		StatusCodes:    make(map[int]int64),
		SampleTraceIds: []string{},
		FailedTraceIds: []string{},
	}

	// Extract metrics from edge properties if available
	if edge.Properties != nil {
		if count, exists := edge.Properties["call_count"]; exists {
			if countVal, ok := count.(int64); ok {
				metrics.RequestCount = countVal
			}
		}
		if failures, exists := edge.Properties["error_count"]; exists {
			if failVal, ok := failures.(int64); ok {
				metrics.FailureCount = failVal
			}
		}
		if latency, exists := edge.Properties["total_duration_ns"]; exists {
			if latVal, ok := latency.(float64); ok {
				metrics.TotalLatency = latVal / 1000000.0 // Convert ns to ms
			}
		}
		if protocol, exists := edge.Properties["protocol"]; exists {
			if protVal, ok := protocol.(string); ok && protVal != "" {
				metrics.Protocol = protVal
			}
		}

		// Extract operation/span name
		if operation, exists := edge.Properties["operation"]; exists {
			if opVal, ok := operation.(string); ok && opVal != "" {
				metrics.Operations = append(metrics.Operations, opVal)
			}
		}

		// Extract HTTP status code
		if statusCode, exists := edge.Properties["http_status_code"]; exists {
			if statusStr, ok := statusCode.(string); ok && statusStr != "" {
				if statusInt, err := strconv.Atoi(statusStr); err == nil {
					metrics.StatusCodes[statusInt] = 1
				}
			}
		}

		// Extract trace ID for drill-down
		if traceId, exists := edge.Properties["trace_id"]; exists {
			if traceStr, ok := traceId.(string); ok && traceStr != "" {
				metrics.SampleTraceIds = append(metrics.SampleTraceIds, traceStr)
				// If this is a failed request, add to failed traces
				if metrics.FailureCount > 0 {
					metrics.FailedTraceIds = append(metrics.FailedTraceIds, traceStr)
				}
			}
		}
	}

	return metrics
}

// createDownstreamLink creates a downstream link with metrics
func (s *KnowledgeGraphService) createDownstreamLink(downstreamService *ServiceApplication, metrics *ServiceDependencyMetrics) DownstreamLink {
	avgLatency := 0.0
	if metrics.RequestCount > 0 {
		avgLatency = metrics.TotalLatency / float64(metrics.RequestCount)
	}

	status := 0 // OK
	if metrics.FailureCount > 0 {
		status = 2 // Error
	}

	// Generate stats strings
	reqPerMin := float64(metrics.RequestCount) / 60.0 // Assume 1-minute window
	stats := []string{
		fmt.Sprintf("%.1f req/min", reqPerMin),
		fmt.Sprintf("%.1fms", avgLatency),
	}

	return DownstreamLink{
		Id:            downstreamService.Id,
		Status:        status,
		Stats:         stats,
		Latency:       avgLatency,
		FailureCount:  float64(metrics.FailureCount),
		Protocol:      metrics.Protocol,
		BytesSent:     0.0,
		BytesReceived: 0.0,
	}
}

// createUpstreamLink creates an upstream link with metrics
func (s *KnowledgeGraphService) createUpstreamLink(upstreamService *ServiceApplication, metrics *ServiceDependencyMetrics) UpstreamLink {
	avgLatency := 0.0
	if metrics.RequestCount > 0 {
		avgLatency = metrics.TotalLatency / float64(metrics.RequestCount)
	}

	status := 0 // OK
	if metrics.FailureCount > 0 {
		status = 2 // Error
	}

	// Generate stats strings
	reqPerMin := float64(metrics.RequestCount) / 60.0 // Assume 1-minute window
	stats := []string{
		fmt.Sprintf("%.1f req/min", reqPerMin),
		fmt.Sprintf("%.1fms", avgLatency),
	}

	// Create upstream ID string for external services
	upstreamId := fmt.Sprintf("%s:%s:%s", upstreamService.Id.Namespace, upstreamService.Id.Kind, upstreamService.Id.Name)

	return UpstreamLink{
		Id:            upstreamId,
		Status:        status,
		Stats:         stats,
		Latency:       avgLatency,
		FailureCount:  float64(metrics.FailureCount),
		Protocol:      metrics.Protocol,
		BytesSent:     0.0,
		BytesReceived: 0.0,
	}
}

// addInfrastructureLabels adds infrastructure information to service labels
func (s *KnowledgeGraphService) addInfrastructureLabels(serviceApp *ServiceApplication, infraNode *KnowledgeGraphNode) {
	resourceName, ok := infraNode.Properties["name"].(string)
	if !ok || resourceName == "" {
		slog.Warn("Skipping infrastructure labeling for node with invalid name",
			"node_id", infraNode.ID, "node_type", infraNode.NodeType, "properties", infraNode.Properties)
		return
	}

	switch infraNode.NodeType {
	case NodeTypePod:
		key := "infrastructure.pods"
		if existing, exists := serviceApp.Labels[key]; exists {
			if !strings.Contains(existing, resourceName) {
				serviceApp.Labels[key] = existing + "," + resourceName
			}
		} else {
			serviceApp.Labels[key] = resourceName
		}
	case NodeTypeNamespace:
		serviceApp.Labels["infrastructure.namespace"] = resourceName
	case NodeTypeCluster:
		serviceApp.Labels["infrastructure.cluster"] = resourceName
	case NodeTypeDatabase:
		key := "infrastructure.databases"
		if existing, exists := serviceApp.Labels[key]; exists {
			if !strings.Contains(existing, resourceName) {
				serviceApp.Labels[key] = existing + "," + resourceName
			}
		} else {
			serviceApp.Labels[key] = resourceName
		}
	case NodeTypeCache:
		key := "infrastructure.caches"
		if existing, exists := serviceApp.Labels[key]; exists {
			if !strings.Contains(existing, resourceName) {
				serviceApp.Labels[key] = existing + "," + resourceName
			}
		} else {
			serviceApp.Labels[key] = resourceName
		}
	case NodeTypeMessageQueue:
		key := "infrastructure.queues"
		if existing, exists := serviceApp.Labels[key]; exists {
			if !strings.Contains(existing, resourceName) {
				serviceApp.Labels[key] = existing + "," + resourceName
			}
		} else {
			serviceApp.Labels[key] = resourceName
		}
	}
}

// getServicesForSync returns services that need sync, prioritizing those that haven't been synced
// or were synced longest ago. Limits results to specified count.
func (s *KnowledgeGraphService) getServicesForSync(tenantID, cloudAccountID string, limit int) ([]string, error) {
	query := `
		SELECT DISTINCT 
			attribute_value->>'service_name' as service_name,
			MIN(last_sync_at) as min_last_sync_at
		FROM knowledge_graph_metadata 
		WHERE tenant_id = $1 
		AND cloud_account_id = $2 
		AND attribute_type = 'service'
		AND attribute_value->>'service_name' IS NOT NULL
		GROUP BY attribute_value->>'service_name'
		ORDER BY min_last_sync_at ASC NULLS FIRST, service_name
		LIMIT $3
	`

	rows, err := s.dbManager.Query(query, tenantID, cloudAccountID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query services for sync: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	var services []string
	for rows.Next() {
		var serviceName string
		var minLastSyncAt any // Can be nil or timestamp
		if err := rows.Scan(&serviceName, &minLastSyncAt); err != nil {
			return nil, fmt.Errorf("failed to scan service name: %w", err)
		}
		if serviceName != "" {
			services = append(services, serviceName)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating service rows: %w", err)
	}

	return services, nil
}

// updateServiceLastSync updates the last_sync_at timestamp for a specific service
func (s *KnowledgeGraphService) updateServiceLastSync(tenantID, cloudAccountID, serviceName string) error {
	query := `
		UPDATE knowledge_graph_metadata 
		SET last_sync_at = NOW()
		WHERE tenant_id = $1 
		AND cloud_account_id = $2 
		AND attribute_type = 'service'
		AND attribute_value->>'service_name' = $3
	`

	result, err := s.dbManager.Exec(query, tenantID, cloudAccountID, serviceName)
	if err != nil {
		return fmt.Errorf("failed to update last_sync_at for service %s: %w", serviceName, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for service %s: %w", serviceName, err)
	}

	if rowsAffected == 0 {
		slog.Warn("No metadata rows found to update last_sync_at",
			"service", serviceName,
			"tenant_id", tenantID,
			"cloud_account_id", cloudAccountID)
	}

	return nil
}

// getNodeIDMapping creates a mapping from unique_key to database ID for nodes referenced in edges
func (s *KnowledgeGraphService) getNodeIDMapping(edges []*KnowledgeGraphEdge) (map[string]string, error) {
	// Collect all unique node keys referenced in edges
	nodeKeys := make(map[string]bool)
	for _, edge := range edges {
		nodeKeys[edge.SourceNodeID] = true
		nodeKeys[edge.DestinationNodeID] = true
	}

	if len(nodeKeys) == 0 {
		return make(map[string]string), nil
	}

	// Convert to slice for query
	keySlice := make([]string, 0, len(nodeKeys))
	for key := range nodeKeys {
		keySlice = append(keySlice, key)
	}

	// Query database for node IDs
	query := `
		SELECT id, unique_key 
		FROM knowledge_graph_node 
		WHERE unique_key = ANY($1) AND level = 'Account'
	`

	rows, err := s.dbManager.Query(query, pq.Array(keySlice))
	if err != nil {
		return nil, fmt.Errorf("failed to query node IDs: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Warn("Failed to close rows", "error", closeErr)
		}
	}()

	nodeIDMap := make(map[string]string)
	for rows.Next() {
		var id, uniqueKey string
		if err := rows.Scan(&id, &uniqueKey); err != nil {
			return nil, fmt.Errorf("failed to scan node ID: %w", err)
		}
		nodeIDMap[uniqueKey] = id
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating node rows: %w", err)
	}

	return nodeIDMap, nil
}

// Legacy function wrappers for backward compatibility

// RefreshKnowledgeGraph is a legacy wrapper for the service method
func RefreshKnowledgeGraph(ctx context.Context, request RefreshKnowledgeGraphRequest) (*RefreshKnowledgeGraphResponse, error) {
	service, err := NewKnowledgeGraphService()
	if err != nil {
		return nil, err
	}
	return service.RefreshKnowledgeGraph(ctx, request)
}

// NewKnowledgeGraphRepository is a legacy wrapper that returns a service
func NewKnowledgeGraphRepository() (*KnowledgeGraphService, error) {
	return NewKnowledgeGraphService()
}

// LinkLoadBalancersToBackendServices creates edges connecting LoadBalancers to backend services
// using DNS resolution and http.host matching from trace labels
func LinkLoadBalancersToBackendServices(nodes []*KnowledgeGraphNode, edges []*KnowledgeGraphEdge, cloudAccountID, tenantID string) ([]*KnowledgeGraphNode, []*KnowledgeGraphEdge) {
	slog.Info("Starting LoadBalancer to backend service linking")

	newEdges := make([]*KnowledgeGraphEdge, 0)

	// Build indexes for faster lookup
	loadBalancersByDNS := make(map[string]*KnowledgeGraphNode)
	externalServicesByName := make(map[string]*KnowledgeGraphNode)
	servicesByHTTPHost := make(map[string][]*KnowledgeGraphNode)

	// Index all nodes
	for _, node := range nodes {
		// Index LoadBalancers by their DNS name
		if node.NodeType == NodeTypeLoadBalancer {
			if dnsName, ok := node.Properties["dns_name"].(string); ok && dnsName != "" {
				loadBalancersByDNS[dnsName] = node
			}
		}

		// Index External Services by name
		if node.NodeType == NodeTypeExternalService {
			if name, ok := node.Properties["name"].(string); ok && name != "" {
				externalServicesByName[name] = node
			}
		}

		// Index Services by http.host label
		if node.NodeType == NodeTypeService {
			if labels, ok := node.Properties["labels"].(map[string]string); ok {
				if httpHost, ok := labels["http.host"]; ok && httpHost != "" {
					servicesByHTTPHost[httpHost] = append(servicesByHTTPHost[httpHost], node)
				}
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
		if labels, ok := externalServiceNode.Properties["labels"].(map[string]string); ok {
			if dnsResolvedTo, ok := labels["dns.resolved_to"]; ok {
				resolvedTo = dnsResolvedTo
			} else if dnsCNAME, ok := labels["dns.cname"]; ok {
				resolvedTo = dnsCNAME
			}
		}

		if resolvedTo != "" {
			// Check if we have a LoadBalancer with this DNS name
			if lbNode, exists := loadBalancersByDNS[resolvedTo]; exists {
				// Create RESOLVES_TO edge: ExternalService → LoadBalancer
				edge := &KnowledgeGraphEdge{
					ID:                uuid.New().String(),
					SourceNodeID:      externalServiceNode.UniqueKey,
					DestinationNodeID: lbNode.UniqueKey,
					RelationshipType:  RelationshipResolvesTo,
					Properties: map[string]any{
						"dns_name":        resolvedTo,
						"discovered_from": "dns_resolution",
					},
					CloudAccountID: cloudAccountID,
					TenantID:       tenantID,
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
						routeEdge := &KnowledgeGraphEdge{
							ID:                uuid.New().String(),
							SourceNodeID:      lbNode.UniqueKey,
							DestinationNodeID: backendService.UniqueKey,
							RelationshipType:  RelationshipRoutesTo,
							Properties: map[string]any{
								"http_host":       externalServiceName,
								"discovered_from": "http_host_matching",
							},
							CloudAccountID: cloudAccountID,
							TenantID:       tenantID,
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
