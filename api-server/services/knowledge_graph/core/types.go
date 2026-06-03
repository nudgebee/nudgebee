package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// NodeType represents the type of entity in the knowledge graph
type NodeType string

const (
	// Service and Infrastructure nodes
	NodeTypeService             NodeType = "Service"
	NodeTypeWorkload            NodeType = "Workload"
	NodeTypeDatabase            NodeType = "Database"
	NodeTypeMessageQueue        NodeType = "MessageQueue"
	NodeTypeQueue               NodeType = "Queue"
	NodeTypeTopic               NodeType = "Topic"
	NodeTypeCache               NodeType = "Cache"
	NodeTypeExternalService     NodeType = "ExternalService"
	NodeTypeComputeInstance     NodeType = "ComputeInstance"
	NodeTypeComputeInstancePool NodeType = "ComputeInstancePool" // GKE Node Pool, EKS Node Group, AKS Node Pool, Karpenter NodePool

	// Kubernetes nodes
	NodeTypeCluster   NodeType = "Cluster"
	NodeTypeNamespace NodeType = "Namespace"
	NodeTypePod       NodeType = "Pod"
	NodeTypeNode      NodeType = "Node"
	NodeTypeJob       NodeType = "Job"            // K8s batch Job
	NodeTypeCronJob   NodeType = "CronJob"        // K8s scheduled CronJob
	NodeTypeCRD       NodeType = "CustomResource" // Kubernetes Custom Resource (operator CRDs — eg Karpenter NodeClaim, cert-manager Certificate)

	// Cloud resource node types (Cloud-Agnostic)
	NodeTypeLoadBalancer     NodeType = "LoadBalancer"
	NodeTypeBackendPool      NodeType = "BackendPool" // AWS Target Group, Azure Backend Pool, GCP Backend Service/NEG
	NodeTypeStorage          NodeType = "Storage"
	NodeTypeVPC              NodeType = "VPC"
	NodeTypeSecurityGroup    NodeType = "SecurityGroup"
	NodeTypeSubnet           NodeType = "Subnet"
	NodeTypeNetworkInterface NodeType = "NetworkInterface"
	NodeTypeRouteTable       NodeType = "RouteTable" // AWS Route Table, Azure Route Table, GCP Routes
	NodeTypeCloudResource    NodeType = "CloudResource"
	NodeTypeInfraStack       NodeType = "InfraStack" // Infrastructure-as-code stacks (CloudFormation, ARM, Terraform)

	// Generic Cloud Services (Multi-Cloud Support)
	NodeTypeContainerRegistry  NodeType = "ContainerRegistry"  // ECR, ACR, GCR, Harbor, Docker Hub
	NodeTypeContainerImage     NodeType = "ContainerImage"     // Individual container image
	NodeTypeArtifact           NodeType = "Artifact"           // Generic build artifact
	NodeTypeDNSZone            NodeType = "DNSZone"            // Route53, Azure DNS, Cloud DNS
	NodeTypeDNSRecord          NodeType = "DNSRecord"          // Individual DNS record
	NodeTypeCDN                NodeType = "CDN"                // CloudFront, Azure CDN, Cloud CDN
	NodeTypeNetworkGateway     NodeType = "NetworkGateway"     // NAT Gateway, Azure NAT, Cloud NAT
	NodeTypePrivateEndpoint    NodeType = "PrivateEndpoint"    // VPC Endpoint (AWS), Private Endpoint (Azure), Private Service Connect (GCP)
	NodeTypeAPIGateway         NodeType = "APIGateway"         // API Gateway services
	NodeTypeSecretVault        NodeType = "SecretVault"        // Secrets Manager, Key Vault, Secret Manager
	NodeTypeEncryptionKey      NodeType = "EncryptionKey"      // KMS, Key Vault keys, Cloud KMS
	NodeTypeMonitoringService  NodeType = "MonitoringService"  // CloudWatch, Azure Monitor, Cloud Monitoring
	NodeTypeLogAggregator      NodeType = "LogAggregator"      // CloudWatch Logs, Log Analytics, Cloud Logging
	NodeTypeServerlessFunction NodeType = "ServerlessFunction" // Lambda, Azure Functions, Cloud Functions
	NodeTypeManagedCluster     NodeType = "ManagedCluster"     // EKS, AKS, GKE, ECS (managed container orchestration)
	NodeTypeBackupVault        NodeType = "BackupVault"        // AWS Backup Vault, Azure Recovery Services Vault, GCP Backup Vault
	NodeTypeBackupPolicy       NodeType = "BackupPolicy"       // AWS Backup Plan, Azure Backup Policy, GCP Backup Plan
	NodeTypePublicIP           NodeType = "PublicIP"           // AWS Elastic IP, Azure Public IP, GCP External IP
	NodeTypeSecurityService    NodeType = "SecurityService"    // SecurityHub, GuardDuty, Azure Security Center, Security Command Center
	NodeTypeEmailService       NodeType = "EmailService"       // SES, Azure Communication Services
	NodeTypeAIService          NodeType = "AIService"          // Bedrock, SageMaker, Azure OpenAI, Vertex AI
	NodeTypeServiceIdentity    NodeType = "ServiceIdentity"    // AWS IAM Role, Azure Managed Identity, GCP Service Account

	// Kubernetes Resource Types
	NodeTypeK8sService        NodeType = "K8sService"            // K8s Service
	NodeTypeIngress           NodeType = "Ingress"               // K8s Ingress
	NodeTypeNetworkPolicy     NodeType = "NetworkPolicy"         // K8s NetworkPolicy
	NodeTypeConfigMap         NodeType = "ConfigMap"             // K8s ConfigMap
	NodeTypeK8sSecret         NodeType = "K8sSecret"             // K8s Secret
	NodeTypeK8sServiceAccount NodeType = "K8sServiceAccount"     // K8s ServiceAccount (carries IRSA annotation)
	NodeTypePVC               NodeType = "PersistentVolumeClaim" // K8s PVC
	NodeTypePV                NodeType = "PersistentVolume"      // K8s PV

	// Helm & GitOps Resources
	NodeTypeHelmChart     NodeType = "HelmChart"     // Helm chart package
	NodeTypeHelmRelease   NodeType = "HelmRelease"   // Deployed Helm release
	NodeTypeConfiguration NodeType = "Configuration" // Values.yaml or config files
	NodeTypeRepository    NodeType = "Repository"    // Git repository
)

// NodeCategory represents the category of a node (infrastructure vs non-infrastructure)
type NodeCategory string

const (
	NodeCategoryInfrastructure    NodeCategory = "Infrastructure"
	NodeCategoryNonInfrastructure NodeCategory = "NonInfrastructure"
)

// nodeCategoryMap maps each NodeType to its category
var nodeCategoryMap = map[NodeType]NodeCategory{
	// Non-Infrastructure (application-level services)
	NodeTypeService:            NodeCategoryNonInfrastructure,
	NodeTypeWorkload:           NodeCategoryNonInfrastructure,
	NodeTypeExternalService:    NodeCategoryNonInfrastructure,
	NodeTypeServerlessFunction: NodeCategoryNonInfrastructure,
	NodeTypeRepository:         NodeCategoryNonInfrastructure,
	NodeTypeJob:                NodeCategoryNonInfrastructure, // Batch workloads, run user code
	NodeTypeCronJob:            NodeCategoryNonInfrastructure, // Scheduled workloads

	// Infrastructure - Data Layer
	NodeTypeDatabase:     NodeCategoryInfrastructure,
	NodeTypeMessageQueue: NodeCategoryInfrastructure,
	NodeTypeQueue:        NodeCategoryInfrastructure,
	NodeTypeTopic:        NodeCategoryInfrastructure,
	NodeTypeCache:        NodeCategoryInfrastructure,

	// Infrastructure - Kubernetes
	NodeTypePod:               NodeCategoryInfrastructure,
	NodeTypeNode:              NodeCategoryInfrastructure,
	NodeTypeCRD:               NodeCategoryInfrastructure, // Operator CRDs are infrastructure
	NodeTypeCluster:           NodeCategoryInfrastructure,
	NodeTypeNamespace:         NodeCategoryInfrastructure,
	NodeTypeManagedCluster:    NodeCategoryInfrastructure,
	NodeTypeK8sService:        NodeCategoryInfrastructure,
	NodeTypeIngress:           NodeCategoryInfrastructure,
	NodeTypeNetworkPolicy:     NodeCategoryInfrastructure,
	NodeTypeConfigMap:         NodeCategoryInfrastructure,
	NodeTypeK8sSecret:         NodeCategoryInfrastructure,
	NodeTypeK8sServiceAccount: NodeCategoryInfrastructure,
	NodeTypePVC:               NodeCategoryInfrastructure,
	NodeTypePV:                NodeCategoryInfrastructure,

	// Infrastructure - Helm & Configuration
	NodeTypeHelmChart:     NodeCategoryInfrastructure,
	NodeTypeHelmRelease:   NodeCategoryInfrastructure,
	NodeTypeConfiguration: NodeCategoryInfrastructure,

	// Infrastructure - Cloud Resources
	NodeTypeStorage:             NodeCategoryInfrastructure,
	NodeTypeComputeInstance:     NodeCategoryInfrastructure,
	NodeTypeComputeInstancePool: NodeCategoryInfrastructure,
	NodeTypeVPC:                 NodeCategoryInfrastructure,
	NodeTypeLoadBalancer:        NodeCategoryInfrastructure,
	NodeTypeBackendPool:         NodeCategoryInfrastructure,
	NodeTypeSecurityGroup:       NodeCategoryInfrastructure,
	NodeTypeSubnet:              NodeCategoryInfrastructure,
	NodeTypeNetworkInterface:    NodeCategoryInfrastructure,
	NodeTypeRouteTable:          NodeCategoryInfrastructure,
	NodeTypeCloudResource:       NodeCategoryInfrastructure,
	NodeTypeNetworkGateway:      NodeCategoryInfrastructure,
	NodeTypePrivateEndpoint:     NodeCategoryInfrastructure,
	NodeTypeAPIGateway:          NodeCategoryInfrastructure,
	NodeTypeContainerRegistry:   NodeCategoryInfrastructure,
	NodeTypeContainerImage:      NodeCategoryInfrastructure,
	NodeTypeArtifact:            NodeCategoryInfrastructure,
	NodeTypeDNSZone:             NodeCategoryInfrastructure,
	NodeTypeDNSRecord:           NodeCategoryInfrastructure,
	NodeTypeCDN:                 NodeCategoryInfrastructure,
	NodeTypeSecretVault:         NodeCategoryInfrastructure,
	NodeTypeEncryptionKey:       NodeCategoryInfrastructure,
	NodeTypeMonitoringService:   NodeCategoryInfrastructure,
	NodeTypeLogAggregator:       NodeCategoryInfrastructure,
	NodeTypeBackupVault:         NodeCategoryInfrastructure,
	NodeTypeBackupPolicy:        NodeCategoryInfrastructure,
	NodeTypePublicIP:            NodeCategoryInfrastructure,
	NodeTypeSecurityService:     NodeCategoryInfrastructure,
	NodeTypeEmailService:        NodeCategoryInfrastructure,
	NodeTypeAIService:           NodeCategoryInfrastructure,
	NodeTypeServiceIdentity:     NodeCategoryInfrastructure,
}

// GetCategory returns the category for this node type
func (nt NodeType) GetCategory() NodeCategory {
	if category, exists := nodeCategoryMap[nt]; exists {
		return category
	}
	// Default to Infrastructure for unknown types
	return NodeCategoryInfrastructure
}

// IsInfrastructure returns true if this node type is categorized as infrastructure
func (nt NodeType) IsInfrastructure() bool {
	return nt.GetCategory() == NodeCategoryInfrastructure
}

// InfraAuthoritativeNodeTypes is the set of node types for which static infrastructure
// sources (k8s / aws / azure / gcp) are the ground-truth oracle. Flow sources may
// synthesize these nodes from observed traffic, but a row of one of these types must
// participate in sync-version tombstoning rather than being permanently protected by
// the flow-source exclusion in Service.markInactiveNodes.
//
// Keep this set conservative and K8s-shaped. Do NOT add types with legitimate flow-only
// origins (LoadBalancer with Cloudflare, Database with external managed services, Cache,
// ComputeInstance, etc.) — those rows must remain protected when only a flow source
// observes them.
var InfraAuthoritativeNodeTypes = map[NodeType]bool{
	NodeTypeCronJob:           true,
	NodeTypeJob:               true,
	NodeTypePod:               true,
	NodeTypeK8sService:        true,
	NodeTypeIngress:           true,
	NodeTypeNamespace:         true,
	NodeTypeNode:              true,
	NodeTypeWorkload:          true,
	NodeTypeK8sServiceAccount: true,
	NodeTypeCRD:               true,
}

// IsInfraAuthoritative reports whether static infra sources are the ground-truth oracle
// for this node type. Deliberately distinct from IsInfrastructure — CronJob and Job are
// tagged NonInfrastructure in nodeCategoryMap for UX taxonomy reasons, but are
// authoritative for tombstoning purposes.
func (nt NodeType) IsInfraAuthoritative() bool {
	return InfraAuthoritativeNodeTypes[nt]
}

// RelationshipType represents the type of relationship between nodes
type RelationshipType string

const (
	// Service relationships
	RelationshipCalls        RelationshipType = "CALLS"
	RelationshipPublishesTo  RelationshipType = "PUBLISHES_TO"
	RelationshipSubscribesTo RelationshipType = "SUBSCRIBES_TO"
	RelationshipRunsOn       RelationshipType = "RUNS_ON"

	// Deployment & Configuration relationships
	RelationshipIsDeployedFrom  RelationshipType = "IS_DEPLOYED_FROM" // Workload → Repository
	RelationshipIsConfiguredBy  RelationshipType = "IS_CONFIGURED_BY" // Workload → Configuration/HelmChart
	RelationshipReferencesImage RelationshipType = "REFERENCES_IMAGE" // Configuration → ContainerImage
	RelationshipUsesImage       RelationshipType = "USES_IMAGE"       // Workload → ContainerImage
	RelationshipPullsFrom       RelationshipType = "PULLS_FROM"       // ContainerImage → ContainerRegistry

	// Resource ownership & grouping
	RelationshipBelongsTo RelationshipType = "BELONGS_TO" // Any → Namespace/Cluster
	RelationshipRunsIn    RelationshipType = "RUNS_IN"    // Workload → Cluster/Namespace
	RelationshipManages   RelationshipType = "MANAGES"    // Parent → Child resource (Deployment → Pod)
	RelationshipOwns      RelationshipType = "OWNS"       // Owner → Owned resource

	// Configuration & Secrets
	RelationshipUsesConfig         RelationshipType = "USES_CONFIG"          // Workload → ConfigMap
	RelationshipUsesSecret         RelationshipType = "USES_SECRET"          // Workload → K8sSecret
	RelationshipUsesServiceAccount RelationshipType = "USES_SERVICE_ACCOUNT" // Workload → ServiceAccount
	RelationshipStoresIn           RelationshipType = "STORES_IN"            // K8sSecret → SecretVault
	RelationshipIsEncryptedBy      RelationshipType = "IS_ENCRYPTED_BY"      // Secret/Storage → EncryptionKey

	// Storage
	RelationshipMounts          RelationshipType = "MOUNTS"           // Workload → PVC
	RelationshipIsBoundTo       RelationshipType = "IS_BOUND_TO"      // PVC → PV
	RelationshipProvidesStorage RelationshipType = "PROVIDES_STORAGE" // PV → Storage backend

	// Networking
	RelationshipExposes         RelationshipType = "EXPOSES"           // K8sService → Workload
	RelationshipRoutesToService RelationshipType = "ROUTES_TO_SERVICE" // Ingress → K8sService
	RelationshipRoutesToBackend RelationshipType = "ROUTES_TO_BACKEND" // LoadBalancer → Service
	RelationshipProtects        RelationshipType = "PROTECTS"          // NetworkPolicy → Workload
	RelationshipIsAccessedVia   RelationshipType = "IS_ACCESSED_VIA"   // Service → DNS/CDN

	// Cloud resource relationships (legacy, kept for compatibility)
	RelationshipRoutesThrough  RelationshipType = "ROUTES_THROUGH"
	RelationshipHostedOn       RelationshipType = "HOSTED_ON"
	RelationshipResolvesTo     RelationshipType = "RESOLVES_TO"
	RelationshipRoutesTo       RelationshipType = "ROUTES_TO"
	RelationshipAssociatedWith RelationshipType = "ASSOCIATED_WITH" // PublicIP → EC2/ENI, EFS → ENI

	// Observability relationships
	RelationshipEmitsLogsTo    RelationshipType = "EMITS_LOGS_TO"
	RelationshipEmitsMetricsTo RelationshipType = "EMITS_METRICS_TO"
	RelationshipEmitsTraceSTo  RelationshipType = "EMITS_TRACES_TO"

	// Source code relationships
	RelationshipBuiltFrom RelationshipType = "BUILT_FROM"

	// Identity relationships
	RelationshipRunsAs  RelationshipType = "RUNS_AS" // Compute resource (Lambda/EC2/ECS) uses this IAM identity
	RelationshipAssumes RelationshipType = "ASSUMES" // ServiceIdentity can assume another ServiceIdentity (trust policy)

	// Deprecated - kept for reference (removed in favor of IS_ENCRYPTED_BY)
	// RelationshipEncryptedBy RelationshipType = "ENCRYPTED_BY"
)

type KnowledgeGraph struct {
	Nodes       []KgNode  `json:"nodes"`
	Edges       []KgEdge  `json:"edges"`
	GeneratedAt time.Time `json:"generated_at"`
	TenantID    string    `json:"tenant_id"`
	AccountID   string    `json:"cloud_account_id"`
}

type KgNode struct {
	ID             string                 `json:"id"`
	NodeType       NodeType               `json:"node_type"`
	Category       NodeCategory           `json:"category"` // Infrastructure or NonInfrastructure
	UniqueKey      string                 `json:"unique_key"`
	CloudAccountID string                 `json:"cloud_account_id"`
	TenantID       string                 `json:"tenant_id"`
	Level          string                 `json:"level"`  // Tenant or Account
	Source         string                 `json:"source"` // trace, aws, k8s
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Properties     map[string]interface{} `json:"properties"`
	Labels         map[string]string      `json:"labels"`
	Language       string                 `json:"language,omitempty"` // Programming language used (e.g., Golang, Python)
	LastUpdated    time.Time              `json:"LastUpdated"`
	LogoID         string                 `json:"logo_id,omitempty"` // Icon identifier resolved by the backend for UI rendering
}

// Node represents a node in the knowledge graph
type DbNode struct {
	ID              string                 `json:"id"`
	NodeType        NodeType               `json:"node_type"`
	UniqueKey       string                 `json:"unique_key"`
	Properties      map[string]interface{} `json:"properties"`
	Labels          map[string]string      `json:"labels"`
	QueryAttributes map[string]interface{} `json:"query_attributes"`
	CloudAccountID  string                 `json:"cloud_account_id,omitempty"`
	TenantID        string                 `json:"tenant_id"`
	Level           string                 `json:"level"`  // Tenant or Account
	Source          string                 `json:"source"` // trace, aws, k8s
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type KgEdge struct {
	ID                string                 `json:"id"`
	SourceNodeID      string                 `json:"source_node_id"`
	DestinationNodeID string                 `json:"dest_node_id"`
	RelationshipType  RelationshipType       `json:"relationship_type"`
	Properties        map[string]interface{} `json:"properties"`
	CloudAccountID    string                 `json:"cloud_account_id,omitempty"`
	TenantID          string                 `json:"tenant_id"`
	Level             string                 `json:"level"`  // Tenant or Account
	Source            string                 `json:"source"` // trace, aws, k8s
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// Edge represents an edge in the knowledge graph
type DbEdge struct {
	ID                  string                   `json:"id"`
	SourceNodeID        string                   `json:"source_node_id"`
	DestinationNodeID   string                   `json:"destination_node_id"`
	RelationshipType    RelationshipType         `json:"relationship_type"`
	Properties          map[string]interface{}   `json:"properties"`
	CloudAccountID      string                   `json:"cloud_account_id,omitempty"`
	TenantID            string                   `json:"tenant_id"`
	Level               string                   `json:"level"`  // Tenant or Account
	Source              string                   `json:"source"` // winning source after dedup: trace, aws, k8s, manual, ...
	ContributingSources []EdgeContributingSource `json:"contributing_sources,omitempty"`
	IsActive            bool                     `json:"is_active"`
	LastSyncVersion     int64                    `json:"last_sync_version"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
}

// EdgeContributingSource records that a given source asserted this edge at a
// given time. Multiple sources can corroborate one edge; dedup picks the
// highest-priority one as `Source` and unions all into `ContributingSources`.
// Correlation reads the array to decide whether a manual-only edge is alone
// or backed by an observed source.
type EdgeContributingSource struct {
	Source     string    `json:"source"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// DefaultBehavioralEdgeTypes is the built-in allowlist of relationship types that
// represent observed behaviour rather than declared infrastructure. These edges
// are emitted by flow sources (eBPF, traces, Datadog APM) and may be intermittent —
// they get a time-based grace period (KGEdgeStaleAfterDays) before being marked
// inactive. All other relationship types are infra-authoritative and are marked
// inactive immediately at end of sync if not re-stamped (mirroring node behaviour).
//
// Override at runtime via env var KG_BEHAVIORAL_EDGE_TYPES (comma-separated).
var DefaultBehavioralEdgeTypes = map[RelationshipType]bool{
	RelationshipCalls:        true,
	RelationshipPublishesTo:  true,
	RelationshipSubscribesTo: true,
}

// KgNodeSlim is a lightweight node for graph traversal API responses
type KgNodeSlim struct {
	ID        string   `json:"id"`
	Kind      NodeType `json:"kind"` // maps to node_type
	Name      string   `json:"name"` // extracted from properties["name"]
	Source    string   `json:"source"`
	AccountID string   `json:"account_id"` // maps to cloud_account_id
	TenantID  string   `json:"tenant_id"`
	UniqueKey string   `json:"unique_key"`
	LogoID    string   `json:"logo_id,omitempty"` // Icon identifier resolved by the backend for UI rendering
}

// KgEdgeSlim is a lightweight edge for graph traversal API responses
type KgEdgeSlim struct {
	ID                string           `json:"id"`
	SourceNodeID      string           `json:"source_node_id"`
	DestinationNodeID string           `json:"dest_node_id"`
	RelationshipType  RelationshipType `json:"relationship_type"`
}

// KnowledgeGraphSlim is a slim version of KnowledgeGraph with minimal node/edge fields
type KnowledgeGraphSlim struct {
	Nodes       []KgNodeSlim `json:"nodes"`
	Edges       []KgEdgeSlim `json:"edges"`
	TenantID    string       `json:"tenant_id"`
	AccountID   string       `json:"cloud_account_id"`
	GeneratedAt time.Time    `json:"generated_at"`
}

type ServiceApplicationId struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
}

type ServiceCategory struct {
	Category string `json:"category"`
}

// UnmarshalJSON accepts both shapes the eBPF / traces relay producers send:
//
//	"Category": "compute"               — raw string (current eBPF agent shape;
//	                                      hits offset-37 unmarshal error per #31101)
//	"Category": {"category": "compute"} — wrapped object (legacy / GraphQL shape;
//	                                      what consumers + the front-end still expect)
//
// Both decode to the same internal representation so downstream readers
// (app.Category.Category in flow_sources/*.go) keep working.
func (s *ServiceCategory) UnmarshalJSON(data []byte) error {
	// Try the wrapped object first — that's what trace producers + the
	// GraphQL contract emit.
	var wrapped struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil {
		s.Category = wrapped.Category
		return nil
	}
	// Fall back to a raw string — current eBPF relay payload.
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		s.Category = raw
		return nil
	}
	return fmt.Errorf("ServiceCategory: expected string or {category:string}, got %s", string(data))
}

type UpstreamLink struct {
	Id       string   `json:"Id"`
	Status   int      `json:"Status"`
	Stats    []string `json:"Stats"`
	Weight   float64  `json:"Weight"`
	Latency  float64  `json:"Latency"`
	Protocol string   `json:"Protocol"`
}

type DownstreamLink struct {
	Id       ServiceApplicationId `json:"Id"`
	Status   int                  `json:"Status"`
	Stats    []string             `json:"Stats"`
	Latency  float64              `json:"Latency"`
	Protocol string               `json:"Protocol"`
}

// langMap maps canonical language names to their various aliases
var langMap = map[string][]string{
	"Golang":     {"go", "GO", "Golang", "golang"},
	"Python":     {"python", "py", "Python", "PYTHON", "Py"},
	"JavaScript": {"javascript", "js", "JavaScript", "JS", "node", "Node.js", "nodejs"},
	"TypeScript": {"typescript", "ts", "TypeScript", "TS"},
	"Java":       {"java", "Java", "JAVA"},
	"C":          {"c", "C"},
	"C++":        {"cpp", "c++", "C++", "CPP", "cxx", "cc"},
	"C#":         {"csharp", "c#", "C#", "cs", "CSharp"},
	"Ruby":       {"ruby", "rb", "Ruby", "RUBY"},
	"PHP":        {"php", "PHP"},
	"Rust":       {"rust", "rs", "Rust", "RUST"},
	"Kotlin":     {"kotlin", "kt", "Kotlin", "KOTLIN"},
	"Swift":      {"swift", "Swift", "SWIFT"},
	"Scala":      {"scala", "Scala", "SCALA"},
	"R":          {"r", "R"},
}

// GetPrimaryLanguage returns the first language from the Type array, normalized to canonical form
// Returns empty string if array is empty or no language found
func GetPrimaryLanguage(languageTypes []string) string {
	for _, rawLang := range languageTypes {
		// Check each canonical language and its aliases
		for canonical, aliases := range langMap {
			for _, alias := range aliases {
				if alias == rawLang {
					return canonical
				}
			}
		}
		// If no match found in map, return the raw value as-is
		if rawLang != "" {
			return rawLang
		}
	}
	return ""
}

type ServiceApplication struct {
	Id                ServiceApplicationId `json:"Id"`
	Category          ServiceCategory      `json:"Category"`
	Labels            map[string]string    `json:"Labels"`
	Status            *int                 `json:"Status"`
	Indicators        []string             `json:"Indicators"`
	Upstreams         []UpstreamLink       `json:"Upstreams"`
	Downstreams       []DownstreamLink     `json:"Downstreams"`
	Instances         []Instance           `json:"Instances"`
	Type              []string             `json:"Type"`
	DesiredInstances  int                  `json:"DesiredInstances"`
	FailedInstances   int                  `json:"FailedInstances"`
	OOMKills          int                  `json:"OOMKills"`
	Restarts          int                  `json:"Restarts"`
	CPUThrottlingTime float64              `json:"CPUThrottlingTime"`
	VolumeSize        float64              `json:"VolumeSize"`
	VolumeUsed        float64              `json:"VolumeUsed"`
	IsHealthy         bool                 `json:"IsHealthy"`
	HealthReason      string               `json:"HealthReason"`
}

// Graph represents a complete knowledge graph with nodes and edges
type Graph struct {
	Nodes          []*DbNode `json:"nodes"`
	Edges          []*DbEdge `json:"edges"`
	Source         string    `json:"source"`
	TenantID       string    `json:"tenant_id"`
	CloudAccountID string    `json:"cloud_account_id,omitempty"`
	GeneratedAt    time.Time `json:"generated_at"`
	Metadata       Metadata  `json:"metadata"`
}

// Metadata contains metadata about the knowledge graph
type Metadata struct {
	NodeCount         int               `json:"node_count"`
	EdgeCount         int               `json:"edge_count"`
	NodeTypeBreakdown map[NodeType]int  `json:"node_type_breakdown"`
	Sources           []string          `json:"sources"`
	TimeRange         *TimeRange        `json:"time_range,omitempty"`
	Filters           map[string]string `json:"filters,omitempty"`
}

// TimeRange represents a time range for the knowledge graph
type TimeRange struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// Config holds configuration for knowledge graph service
type Config struct {
	TenantID       string
	CloudAccountID string
	MaxNodes       int
	MaxEdges       int
	QueryTimeout   time.Duration
}

// BuildRequest represents a request to build a knowledge graph
// Knowledge graphs are built for ALL active accounts under the tenant
type BuildRequest struct {
	TenantID                  string                     `json:"tenant_id" binding:"required"`
	Sources                   []string                   `json:"sources"`                     // ["trace", "aws", "k8s"] - Optional: if not provided, all applicable sources for each account will be used
	FlowSources               []string                   `json:"flow_sources"`                // ["datadog-apm", "ebpf", "jaeger"] - Optional: flow sources to enrich graph with flow relationships
	TimeRange                 *TimeRange                 `json:"time_range"`                  // Optional time range
	Filters                   map[string]string          `json:"filters"`                     // Optional filters
	MergeStrategy             string                     `json:"merge_strategy"`              // "separate" or "merged" (future)
	SaveToDB                  bool                       `json:"save_to_db"`                  // Whether to persist the graph to database
	CrossAccountRelationships []CrossAccountRelationship `json:"cross_account_relationships"` // Rules for creating cross-account/cross-source relationships
	AccountIDs                []string                   `json:"account_ids"`                 // tell which account ids to consider
}

// AccountGraphMetadata contains metadata about graphs built for a specific account
type AccountGraphMetadata struct {
	AccountID      string   `json:"account_id"`
	AccountName    string   `json:"account_name"`
	CloudProvider  string   `json:"cloud_provider"`
	SourcesBuilt   []string `json:"sources_built"`
	NodeCount      int      `json:"node_count"`
	EdgeCount      int      `json:"edge_count"`
	BuildSucceeded bool     `json:"build_succeeded"`
	Error          string   `json:"error,omitempty"`
}

// BuildResponse represents the response from building a knowledge graph
type BuildResponse struct {
	Success           bool                   `json:"success"`
	KnowledgeGraph    KnowledgeGraph         `json:"knowledge_graph,omitempty"` // Converted graph with KgNodes (API format)
	SavedToDB         bool                   `json:"saved_to_db,omitempty"`
	NodesSaved        int                    `json:"nodes_saved,omitempty"`
	EdgesSaved        int                    `json:"edges_saved,omitempty"`
	AccountsProcessed int                    `json:"accounts_processed,omitempty"` // Number of accounts processed
	AccountMetadata   []AccountGraphMetadata `json:"account_metadata,omitempty"`   // Per-account build metadata
	Error             string                 `json:"error,omitempty"`
}

// QueryRequest represents a request to query the knowledge graph
type QueryRequest struct {
	TenantID       string            `json:"tenant_id" binding:"required"`
	CloudAccountID string            `json:"cloud_account_id"`
	Source         string            `json:"source"` // Filter by source
	NodeType       NodeType          `json:"node_type,omitempty"`
	Filters        map[string]string `json:"filters"`
}

// QueryResponse represents the response from querying the knowledge graph
type QueryResponse struct {
	Graph          *Graph         `json:"graph,omitempty"`           // Graph with DbNodes (internal format)
	KnowledgeGraph KnowledgeGraph `json:"knowledge_graph,omitempty"` // Converted graph with KgNodes (API format)
	Error          string         `json:"error,omitempty"`
}

// GraphFilters represents filters for querying the knowledge graph
type GraphFilters struct {
	AccountIDs    []string          `json:"account_ids,omitempty"`    // Filter by cloud account IDs (empty = all accounts)
	NodeTypes     []NodeType        `json:"node_types,omitempty"`     // Filter by node types (empty = all types)
	Labels        map[string]string `json:"labels,omitempty"`         // Filter by label key-value pairs (AND logic)
	LabelKeys     []string          `json:"label_keys,omitempty"`     // Filter by label keys that must exist (regardless of value)
	Attributes    map[string]string `json:"attributes,omitempty"`     // Filter by query_attributes key-value pairs (AND logic)
	AttributeKeys []string          `json:"attribute_keys,omitempty"` // Filter by query_attributes keys that must exist (regardless of value)
}

// APMGraphResponse represents the combined response from the graph API
// The graph endpoint returns BOTH entities and edges in the same response
type APMGraphResponse struct {
	Data     []interface{} `json:"data"`     // Can be either APMEntity or APMEntityEdge
	Included []interface{} `json:"included"` // Can be either APMEntity or APMEntityEdge
}

// APMGraphData holds both entities and edges parsed from the graph response
type APMGraphData struct {
	Entities []APMEntity
	Edges    []APMEntityEdge
}

// APMEntity represents an APM entity from Datadog
type APMEntity struct {
	ID            string                 `json:"id"`
	Type          string                 `json:"type"`
	Attributes    APMEntityAttributes    `json:"attributes"`
	Relationships APMEntityRelationships `json:"relationships"`
}

// APMEntityAttributes contains attributes of an APM entity
type APMEntityAttributes struct {
	IDTags        map[string]string `json:"id_tags"`
	Metadata      APMMetadata       `json:"metadata"`
	ServiceHealth ServiceHealth     `json:"service_health"`
	Stats         *APMStats         `json:"stats,omitempty"`
}

// APMMetadata contains metadata about an APM entity
type APMMetadata struct {
	IsTraced     bool     `json:"is_traced"`
	IsUSM        bool     `json:"is_usm"`
	Languages    []string `json:"languages,omitempty"`
	ProductAreas []string `json:"product_areas"`
	Apdex        string   `json:"apdex_threshold,omitempty"`
}

// ServiceHealth represents the health status of a service
type ServiceHealth struct {
	Status string `json:"status"`
}

// APMStats contains statistics about an APM entity
type APMStats struct {
	Operation         string   `json:"operation"`
	SpanKind          string   `json:"span.kind"`
	RequestsPerSecond float64  `json:"requests_per_second"`
	LatencyAvg        float64  `json:"latency_avg"`
	ErrorsPercentage  *float64 `json:"errors_percentage,omitempty"`
	OperationMode     string   `json:"operation_mode,omitempty"`
}

// APMEntityRelationships contains relationships of an APM entity
type APMEntityRelationships struct {
	Type RelationshipDataWrapper `json:"type"`
}

// RelationshipDataWrapper wraps relationship data
type RelationshipDataWrapper struct {
	Data RelationshipData `json:"data"`
}

// RelationshipData contains relationship information
type RelationshipData struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// APMEntityEdge represents an edge between APM entities
type APMEntityEdge struct {
	ID            string                     `json:"id"`
	Type          string                     `json:"type"` // "apm-entity-edge"
	Attributes    APMEntityEdgeAttributes    `json:"attributes"`
	Relationships APMEntityEdgeRelationships `json:"relationships"`
}

// APMEntityEdgeAttributes contains attributes of an edge
type APMEntityEdgeAttributes struct {
	APMFilter map[string]string `json:"apm_filter"`
	Operation string            `json:"operation"`
	SpanKind  string            `json:"span.kind"`
}

// APMEntityEdgeRelationships contains source and target of an edge
type APMEntityEdgeRelationships struct {
	Source EntityReference `json:"source"`
	Target EntityReference `json:"target"`
}

// EntityReference references an entity
type EntityReference struct {
	Data EntityData `json:"data"`
}

// EntityData contains entity identification
type EntityData struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "apm-entity"
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

// DatadogAPIConfig holds configuration for Datadog API calls
type DatadogAPIConfig struct {
	APIKey         string
	ApplicationKey string
	Site           string // e.g., "datadoghq.com", "datadoghq.eu", "us5.datadoghq.com"
}

// ===================================================================
// Cross-Account Relationship Configuration
// ===================================================================

// MatchType defines how properties should be matched
type MatchType string

const (
	MatchTypeExact    MatchType = "exact"    // Exact match
	MatchTypeContains MatchType = "contains" // Contains substring
	MatchTypeRegex    MatchType = "regex"    // Regular expression match
)

// MatchingRule defines how to match properties between source and target nodes
type MatchingRule struct {
	SourceProperty string    `json:"source_property"` // Property path in source node (e.g., "name", "properties.service_name")
	TargetProperty string    `json:"target_property"` // Property path in target node (e.g., "labels.dd_service_name")
	MatchType      MatchType `json:"match_type"`      // Type of matching: exact, contains, regex
	CaseSensitive  bool      `json:"case_sensitive"`  // Whether matching is case-sensitive
}

// CrossAccountRelationship defines a rule for creating relationships across accounts or sources
type CrossAccountRelationship struct {
	Name             string           `json:"name"`              // Name/description of this relationship rule
	Enabled          bool             `json:"enabled"`           // Whether this rule is enabled
	SourceType       string           `json:"source_type"`       // Source type: "k8s", "aws", etc.
	TargetType       string           `json:"target_type"`       // Target type: "k8s", "aws", etc.
	SourceNodeType   NodeType         `json:"source_node_type"`  // Source node type (e.g., NodeTypeService)
	TargetNodeType   NodeType         `json:"target_node_type"`  // Target node type (e.g., NodeTypeWorkload)
	MatchingRules    []MatchingRule   `json:"matching_rules"`    // Rules to match nodes (AND logic between rules)
	RelationshipType RelationshipType `json:"relationship_type"` // Type of relationship to create
	Bidirectional    bool             `json:"bidirectional"`     // Create edge in both directions
	CrossAccount     bool             `json:"cross_account"`     // Allow matching across different cloud_account_ids
}
type Instance struct {
	Id       ServiceApplicationId `json:"id"`
	IsFailed bool                 `json:"is_failed"`
}

type EBPFServiceMap struct {
	Applications []ServiceApplication       `json:"applications"`
	GeneratedAt  time.Time                  `json:"generated_at"`
	K8sMetadata  *K8sInfrastructureMetadata `json:"k8s_metadata,omitempty"` // K8s infrastructure extracted from traces
}

// K8sInfrastructureMetadata holds K8s infrastructure information extracted from traces
type K8sInfrastructureMetadata struct {
	Clusters   map[string]*K8sClusterInfo   `json:"clusters,omitempty"`
	Namespaces map[string]*K8sNamespaceInfo `json:"namespaces,omitempty"`
	Pods       map[string]*K8sPodInfo       `json:"pods,omitempty"`
	Nodes      map[string]*K8sNodeInfo      `json:"nodes,omitempty"`
}

// K8sClusterInfo contains information about a K8s cluster
type K8sClusterInfo struct {
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
}

// K8sNamespaceInfo contains information about a K8s namespace
type K8sNamespaceInfo struct {
	Name        string `json:"name"`
	Cluster     string `json:"cluster,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// K8sPodInfo contains information about a K8s pod
type K8sPodInfo struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace,omitempty"`
	Node        string `json:"node,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// K8sNodeInfo contains information about a K8s worker node
type K8sNodeInfo struct {
	Name        string `json:"name"`
	Cluster     string `json:"cluster,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// QueryablePropertiesMap defines which properties are queryable for each node type
// These properties will be extracted from the node's Properties and stored in query_attributes
var QueryablePropertiesMap = map[NodeType][]string{
	// Service and Infrastructure nodes
	NodeTypeService: {
		"name", "environment", "namespace", "cluster", "version",
		"port", "protocol", "endpoint", "health_status",
	},
	NodeTypeWorkload: {
		"name", "environment", "namespace", "cluster", "workload_type",
		"replica_count", "image", "version", "service_account_name",
		"last_deployed_time",
	},
	NodeTypeJob: {
		"name", "environment", "namespace", "cluster",
		"completions", "parallelism", "active", "succeeded", "failed",
	},
	NodeTypeCronJob: {
		"name", "environment", "namespace", "cluster",
		"schedule", "last_schedule_time", "active",
	},
	NodeTypeCRD: {
		"name", "environment", "namespace", "cluster", "crd_kind",
		"provisioned_by", // eg "karpenter" — discriminates which operator owns this CR
	},
	NodeTypeDatabase: {
		"name", "environment", "engine", "engine_version", "instance_class",
		"allocated_storage", "storage_type", "multi_az", "publicly_accessible", "availability_zone",
		"dns_name", "private_ip", "vpc_id", "subtype", // GCP: subtype=CloudSQL/BigQuery, dns_name=connection_name
	},
	NodeTypeMessageQueue: {
		"name", "environment", "queue_type", "message_retention_period",
		"max_message_size", "visibility_timeout",
	},
	NodeTypeQueue: {
		"name", "environment", "queue_type", "message_retention_period",
	},
	NodeTypeTopic: {
		"name", "environment", "topic_type",
	},
	NodeTypeCache: {
		"name", "environment", "engine", "engine_version", "node_type",
		"num_cache_nodes", "cache_subnet_group", "availability_zone",
	},
	NodeTypeExternalService: {
		"name", "environment", "hostname", "port", "protocol",
	},
	NodeTypeComputeInstance: {
		"name", "environment", "instance_type", "instance_state",
		"availability_zone", "platform", "vpc_id",
		"gke_cluster_name", "gke_node_pool_name", "is_gke_node", // GCP GKE node properties
		"private_ip", "zone", "subtype", // GCP: subtype=ComputeEngine
	},
	NodeTypeComputeInstancePool: {
		"name", "environment", "region", "cluster_name", "machine_type",
		"initial_node_count", "min_node_count", "max_node_count",
		"autoscaling_enabled", "version", "status", "disk_size_gb",
		"provisioned_by", // eg "karpenter" — distinguishes Karpenter NodePool from EKS NodeGroup
		"cluster",        // K8s cluster name (for Karpenter pools — EKS NodeGroups use cluster_name)
	},

	// Kubernetes nodes
	NodeTypeCluster: {
		"name", "environment", "version", "platform", "region",
	},
	NodeTypeNamespace: {
		"name", "cluster", "environment",
	},
	NodeTypePod: {
		"name", "namespace", "cluster", "environment", "phase",
		"node_name", "pod_ip", "host_ip",
	},
	NodeTypeNode: {
		"name", "cluster", "environment", "instance_type",
		"availability_zone", "capacity_cpu", "capacity_memory",
	},

	// Cloud resource node types
	NodeTypeLoadBalancer: {
		"name", "environment", "scheme", "type", "state",
		"dns_name", "vpc_id", "availability_zone",
	},
	NodeTypeBackendPool: {
		"name", "environment", "protocol", "port", "target_type",
		"vpc_id", "health_check_enabled", "health_check_protocol",
	},
	NodeTypeStorage: {
		"name", "environment", "storage_type", "size", "state",
		"availability_zone", "encrypted", "iops",
		"subtype", // GCP: subtype=CloudStorage/Filestore, AWS: subtype=S3/EBS
	},
	NodeTypeVPC: {
		"name", "environment", "cidr_block", "state", "is_default",
	},
	NodeTypeSecurityGroup: {
		"name", "environment", "group_id", "vpc_id",
	},
	NodeTypeSubnet: {
		"name", "environment", "subnet_id", "vpc_id", "cidr_block",
		"availability_zone", "available_ip_address_count",
	},
	NodeTypeNetworkInterface: {
		"name", "environment", "interface_type", "status", "subnet_id",
		"vpc_id", "private_ip_address", "public_ip", "availability_zone",
	},
	NodeTypeRouteTable: {
		"name", "environment", "route_table_id", "vpc_id", "is_main",
	},
	NodeTypeCloudResource: {
		"name", "environment", "resource_type", "region", "state",
	},

	// Generic Cloud Services
	NodeTypeContainerRegistry: {
		"name", "environment", "registry_type", "region", "uri",
	},
	NodeTypeContainerImage: {
		"name", "environment", "registry", "tag", "digest",
		"size", "pushed_at",
	},
	NodeTypeArtifact: {
		"name", "environment", "artifact_type", "version",
	},
	NodeTypeDNSZone: {
		"name", "environment", "zone_type", "record_count",
	},
	NodeTypeDNSRecord: {
		"name", "environment", "type", "value", "ttl",
	},
	NodeTypeCDN: {
		"name", "environment", "status", "domain_name",
	},
	NodeTypeNetworkGateway: {
		"name", "environment", "gateway_type", "state", "vpc_id",
	},
	NodeTypeAPIGateway: {
		"name", "environment", "api_type", "protocol", "endpoint",
	},
	NodeTypeSecretVault: {
		"name", "environment", "vault_type",
	},
	NodeTypeEncryptionKey: {
		"name", "environment", "key_state", "key_usage",
	},
	NodeTypeMonitoringService: {
		"name", "environment", "service_type",
	},
	NodeTypeLogAggregator: {
		"name", "environment", "retention_days",
	},
	NodeTypeServerlessFunction: {
		"name", "environment", "runtime", "memory_size", "timeout",
		"handler", "last_modified",
	},
	NodeTypeManagedCluster: {
		"name", "environment", "version", "platform", "region", "status",
		"dns_name", "kubernetes_version", "vpc_id", "subnet_id", // GKE/EKS networking
		"node_pool_count", "subtype", // GCP: subtype=GKE, AWS: subtype=EKS
	},

	// Kubernetes Resource Types
	NodeTypeK8sService: {
		"name", "namespace", "cluster", "environment", "service_type",
		"cluster_ip", "external_ip", "port",
	},
	NodeTypeIngress: {
		"name", "namespace", "cluster", "environment", "ingress_class",
		"host", "path",
	},
	NodeTypeNetworkPolicy: {
		"name", "namespace", "cluster", "environment", "policy_types",
	},
	NodeTypeConfigMap: {
		"name", "namespace", "cluster", "environment",
	},
	NodeTypeK8sSecret: {
		"name", "namespace", "cluster", "environment", "secret_type",
	},
	NodeTypeK8sServiceAccount: {
		"name", "namespace", "cluster", "environment", "role_arn",
	},
	NodeTypePVC: {
		"name", "namespace", "cluster", "environment", "storage_class",
		"capacity", "access_modes", "status",
	},
	NodeTypePV: {
		"name", "cluster", "environment", "storage_class", "capacity",
		"access_modes", "status", "reclaim_policy",
	},

	// Helm & GitOps Resources
	NodeTypeHelmChart: {
		"name", "environment", "version", "app_version", "repository",
	},
	NodeTypeHelmRelease: {
		"name", "namespace", "cluster", "environment", "chart",
		"chart_version", "status", "revision",
	},
	NodeTypeConfiguration: {
		"name", "environment", "config_type",
	},
	NodeTypeRepository: {
		"name", "environment", "repo_type", "url", "branch",
	},
	NodeTypeSecurityService: {
		"name", "environment", "resource_type", "region", "state", "standards_arn", "hub_arn",
	},
	NodeTypeEmailService: {
		"name", "environment", "resource_type", "region", "state", "verification_status",
	},
	NodeTypeAIService: {
		"name", "environment", "resource_type", "region", "state", "model_id",
	},
	NodeTypeServiceIdentity: {
		"name", "arn", "subtype", "service_name",
		"aws_account_number", "trust_policy",
	},
}

// GetQueryableProperties returns the list of queryable properties for a given node type
func GetQueryableProperties(nodeType NodeType) []string {
	if props, ok := QueryablePropertiesMap[nodeType]; ok {
		return props
	}
	// Return common queryable properties if node type not found
	return []string{"name", "environment"}
}

// ExtractQueryAttributes extracts queryable properties from a properties map based on node type
func ExtractQueryAttributes(nodeType NodeType, properties map[string]interface{}) map[string]interface{} {
	queryableProps := GetQueryableProperties(nodeType)
	queryAttributes := make(map[string]interface{})

	for _, propKey := range queryableProps {
		if value, ok := properties[propKey]; ok {
			queryAttributes[propKey] = value
		}
	}

	return queryAttributes
}

// --- Agent Traversal API Types ---

// TraverseDirection controls which edge direction to follow during traversal.
type TraverseDirection string

const (
	TraverseDirectionDownstream TraverseDirection = "downstream" // follow source_node_id → destination_node_id
	TraverseDirectionUpstream   TraverseDirection = "upstream"   // follow destination_node_id → source_node_id
	TraverseDirectionBoth       TraverseDirection = "both"       // follow edges in both directions
)

// SearchNodesParams defines parameters for kg_list_nodes.
type SearchNodesParams struct {
	TenantID    string            `json:"-"`
	Name        string            `json:"name,omitempty"`
	NamePattern string            `json:"name_pattern,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Cluster     string            `json:"cluster,omitempty"`
	NodeTypes   []NodeType        `json:"node_types,omitempty"`
	Source      string            `json:"source,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	AccountIDs  []string          `json:"account_ids,omitempty"`
	Limit       int               `json:"limit,omitempty"` // default 20, max 100
}

// SearchNodeResult is a single result from kg_list_nodes.
type SearchNodeResult struct {
	ID             string                 `json:"id"`
	NodeType       NodeType               `json:"node_type"`
	Name           string                 `json:"name"`
	Namespace      string                 `json:"namespace,omitempty"`
	Cluster        string                 `json:"cluster,omitempty"`
	Source         string                 `json:"source"`
	CloudAccountID string                 `json:"cloud_account_id"`
	Labels         map[string]string      `json:"labels,omitempty"`
	Properties     map[string]interface{} `json:"properties,omitempty"`
}

// SearchNodesResponse is the response for kg_list_nodes.
type SearchNodesResponse struct {
	Nodes      []SearchNodeResult `json:"nodes"`
	TotalCount int                `json:"total_count"`
}

// TraverseParams defines parameters for kg_list_path.
type TraverseParams struct {
	TenantID string `json:"-"`

	// Mode 1: Start from known node IDs
	NodeIDs []string `json:"node_ids,omitempty"`

	// Mode 2: Inline search
	Name            string   `json:"name,omitempty"`
	NamePattern     string   `json:"name_pattern,omitempty"`
	Namespace       string   `json:"namespace,omitempty"`
	Cluster         string   `json:"cluster,omitempty"`
	SearchNodeTypes []string `json:"search_node_types,omitempty"`

	// Traversal parameters
	Direction         TraverseDirection `json:"direction"`
	MaxDepth          int               `json:"max_depth,omitempty"`          // 1-3, default 1
	RelationshipTypes []string          `json:"relationship_types,omitempty"` // empty = all
	NodeTypes         []NodeType        `json:"node_types,omitempty"`         // filter neighbor types
	ExcludeNodeTypes  []NodeType        `json:"exclude_node_types,omitempty"` // exclude specific types
	MaxNodes          int               `json:"max_nodes,omitempty"`          // default 500, max 500

	// InducedSubgraph, when true, returns every edge whose endpoints both lie
	// in the discovered node set (the induced subgraph). Default false: only
	// the edges actually walked by the BFS are returned (strict-tree behavior).
	InducedSubgraph bool `json:"induced_subgraph,omitempty"`
}

// TraverseResponse is the response for kg_list_path.
type TraverseResponse struct {
	Graph           KnowledgeGraphSlim `json:"data"`
	SeedNodeIDs     []string           `json:"seed_node_ids"`
	Truncated       bool               `json:"truncated"`
	TotalDiscovered int                `json:"total_discovered"`
}
