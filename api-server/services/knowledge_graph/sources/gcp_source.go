package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/cloud"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// GKE instance naming pattern: gke-{cluster-name}-{node-pool}-{hash}
var gkeInstanceNameRegex = regexp.MustCompile(`^gke-(.+?)-[a-z0-9]+-[a-z0-9]{4}$`)

func init() {
	RegisterSourceFactory("gcp", func(config SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewGCPSource(GCPSourceConfig{ServiceTypeFilter: GCPDefaultServiceTypeFilter}, logger)
	}, "GCP cloud resources source (Compute Engine, Cloud SQL, GKE, BigQuery, etc.)")
}

// GCPSource implements the SourceInterface for GCP cloud resources
type GCPSource struct {
	BaseSource
	config  GCPSourceConfig
	logger  *slog.Logger
	enabled bool
}

// GCPSourceConfig holds configuration for GCP source
type GCPSourceConfig struct {
	ResourceTypes     []string            // Filter by resource types
	IncludeInactive   bool                // Include inactive resources (default: false)
	ServiceTypeFilter map[string][]string // Filter by service name -> allowed types
}

// GCPDefaultServiceTypeFilter provides a predefined service-to-type mapping for GCP
var GCPDefaultServiceTypeFilter = map[string][]string{
	"Compute Engine":       {"compute-engine", "compute.googleapis.com/Instance"},
	"Cloud SQL":            {"cloud-sql", "sqladmin.googleapis.com/Instance/POSTGRES_17", "sqladmin.googleapis.com/Instance"},
	"Kubernetes Engine":    {"kubernetes-engine", "container.googleapis.com/Cluster"},
	"Networking":           {"networking", "subnet", "firewall-rule", "vpc-network"},
	"Cloud Load Balancing": {"forwarding-rule", "backend-service", "target-pool", "url-map", "target-http-proxy", "target-https-proxy", "health-check"},
	"BigQuery":             {"bigquery", "bigquery.googleapis.com/Dataset", "bigquery.googleapis.com/Table", "bigquery.googleapis.com/View"},
	"Cloud Storage":        {"storage.googleapis.com/Bucket"},
	"Cloud Filestore":      {"cloud-filestore"},
	"Cloud Logging":        {"cloud-logging"},
	"Cloud Monitoring":     {"cloud-monitoring"},
	"Vertex AI":            {"vertex-ai", "vertex-ai-model", "vertex-ai-endpoint"},
	"Gemini API":           {"gemini-api"},
	"Cloud Pub/Sub":        {"pubsub.googleapis.com/Topic"},
	"Artifact Registry":    {"artifact-registry"},
	"Cloud Run":            {"run.googleapis.com/Service"},
}

// gcpResourceTypeMap maps (type, service_name) combinations to NodeTypes
var gcpResourceTypeMap = map[string]map[string]core.NodeType{
	"compute-engine": {
		"Compute Engine": core.NodeTypeComputeInstance,
	},
	"compute.googleapis.com/instance": {
		"Compute Engine": core.NodeTypeComputeInstance,
	},
	"cloud-sql": {
		"Cloud SQL": core.NodeTypeDatabase,
	},
	"sqladmin.googleapis.com/instance/postgres_17": {
		"Cloud SQL": core.NodeTypeDatabase,
	},
	"kubernetes-engine": {
		"Kubernetes Engine": core.NodeTypeManagedCluster,
	},
	"container.googleapis.com/cluster": {
		"Kubernetes Engine": core.NodeTypeManagedCluster,
	},
	"bigquery": {
		"BigQuery": core.NodeTypeDatabase,
	},
	"bigquery.googleapis.com/dataset": {
		"BigQuery":                core.NodeTypeDatabase,
		"bigquery.googleapis.com": core.NodeTypeDatabase,
	},
	"bigquery.googleapis.com/table": {
		"BigQuery":                core.NodeTypeDatabase,
		"bigquery.googleapis.com": core.NodeTypeDatabase,
	},
	"bigquery.googleapis.com/view": {
		"BigQuery": core.NodeTypeDatabase,
	},
	"storage.googleapis.com/bucket": {
		"Cloud Storage": core.NodeTypeStorage,
	},
	"cloud-filestore": {
		"Cloud Filestore": core.NodeTypeStorage,
	},
	"cloud-logging": {
		"Cloud Logging": core.NodeTypeLogAggregator,
	},
	"cloud-monitoring": {
		"Cloud Monitoring": core.NodeTypeMonitoringService,
	},
	"networking": {
		"Networking": core.NodeTypeVPC,
	},
	"subnet": {
		"Networking": core.NodeTypeSubnet,
	},
	"firewall-rule": {
		"Networking": core.NodeTypeSecurityGroup,
	},
	"vpc-network": {
		"Networking": core.NodeTypeVPC,
	},
	"sqladmin.googleapis.com/instance": {
		"Cloud SQL": core.NodeTypeDatabase,
	},
	"pubsub.googleapis.com/topic": {
		"Cloud Pub/Sub": core.NodeTypeTopic,
	},
	"artifact-registry": {
		"Artifact Registry": core.NodeTypeContainerRegistry,
	},
	"run.googleapis.com/service": {
		"Cloud Run": core.NodeTypeServerlessFunction,
	},
	"vertex-ai": {
		"Vertex AI": core.NodeTypeAIService,
	},
	"vertex-ai-model": {
		"Vertex AI": core.NodeTypeAIService,
	},
	"vertex-ai-endpoint": {
		"Vertex AI": core.NodeTypeAIService,
	},
	"gemini-api": {
		"Gemini API": core.NodeTypeAIService,
	},
	"claude-sonnet-4.5": {
		"Claude Sonnet 4.5": core.NodeTypeAIService,
	},
	"vm-manager": {
		"VM Manager": core.NodeTypeCloudResource,
	},
	// Load Balancer types
	"forwarding-rule": {
		"Cloud Load Balancing": core.NodeTypeLoadBalancer,
	},
	"backend-service": {
		"Cloud Load Balancing": core.NodeTypeBackendPool,
	},
	"url-map": {
		"Cloud Load Balancing": core.NodeTypeCloudResource,
	},
	"target-http-proxy": {
		"Cloud Load Balancing": core.NodeTypeCloudResource,
	},
	"target-https-proxy": {
		"Cloud Load Balancing": core.NodeTypeCloudResource,
	},
	"health-check": {
		"Cloud Load Balancing": core.NodeTypeCloudResource,
	},
	"target-pool": {
		"Cloud Load Balancing": core.NodeTypeBackendPool,
	},
}

// gcpServiceFallbackMap maps service names to NodeTypes when type-based mapping is insufficient
var gcpServiceFallbackMap = map[string]core.NodeType{
	"Compute Engine":       core.NodeTypeComputeInstance,
	"Cloud SQL":            core.NodeTypeDatabase,
	"Kubernetes Engine":    core.NodeTypeManagedCluster,
	"BigQuery":             core.NodeTypeDatabase,
	"Cloud Storage":        core.NodeTypeStorage,
	"Cloud Filestore":      core.NodeTypeStorage,
	"Cloud Logging":        core.NodeTypeLogAggregator,
	"Cloud Monitoring":     core.NodeTypeMonitoringService,
	"Networking":           core.NodeTypeVPC,
	"Cloud Load Balancing": core.NodeTypeLoadBalancer,
	"Vertex AI":            core.NodeTypeAIService,
	"Gemini API":           core.NodeTypeAIService,
	"Claude Sonnet 4.5":    core.NodeTypeAIService,
	"VM Manager":           core.NodeTypeCloudResource,
	"Cloud Pub/Sub":        core.NodeTypeTopic,
	"Artifact Registry":    core.NodeTypeContainerRegistry,
	"Cloud Run":            core.NodeTypeServerlessFunction,
	// googleapis.com service names (alternative format)
	"bigquery.googleapis.com": core.NodeTypeDatabase,
}

// ========================================================================
// GCP CLI Data Structs
// ========================================================================

// GCPComputeInstance represents a GCP Compute Engine instance from gcloud CLI
type GCPComputeInstance struct {
	Name              string                `json:"name"`
	Zone              string                `json:"zone"`
	Status            string                `json:"status"`
	MachineType       string                `json:"machineType"`
	NetworkInterfaces []GCPNetworkInterface `json:"networkInterfaces"`
	Labels            map[string]string     `json:"labels"`
	Disks             []GCPDisk             `json:"disks"`
}

// GCPDisk represents an attached disk on a GCP compute instance
type GCPDisk struct {
	Source     string `json:"source"`
	Boot       bool   `json:"boot"`
	DiskSizeGb string `json:"diskSizeGb"`
	Type       string `json:"type"`
	Mode       string `json:"mode"`
}

// GCPNetworkInterface represents a network interface on a GCP instance
type GCPNetworkInterface struct {
	Network       string            `json:"network"`
	Subnetwork    string            `json:"subnetwork"`
	NetworkIP     string            `json:"networkIP"`
	AccessConfigs []GCPAccessConfig `json:"accessConfigs"`
}

// GCPAccessConfig represents an access config (external IP) on a network interface
type GCPAccessConfig struct {
	NatIP string `json:"natIP"`
}

// GCPCloudSQLInstance represents a Cloud SQL instance from gcloud CLI
type GCPCloudSQLInstance struct {
	Name               string `json:"name"`
	Region             string `json:"region"`
	State              string `json:"state"`
	DatabaseVersion    string `json:"databaseVersion"`
	ConnectionName     string `json:"connectionName"`
	InstanceType       string `json:"instanceType"`       // "CLOUD_SQL_INSTANCE" or "READ_REPLICA_INSTANCE"
	MasterInstanceName string `json:"masterInstanceName"` // "project:instance-name" for replicas
	IpAddresses        []struct {
		Type      string `json:"type"`
		IpAddress string `json:"ipAddress"`
	} `json:"ipAddresses"`
	Settings struct {
		Tier             string `json:"tier"`
		AvailabilityType string `json:"availabilityType"`
		DataDiskSizeGb   string `json:"dataDiskSizeGb"`
		DataDiskType     string `json:"dataDiskType"`
		IpConfiguration  struct {
			PrivateNetwork string `json:"privateNetwork"`
			SslMode        string `json:"sslMode"`
			Ipv4Enabled    bool   `json:"ipv4Enabled"`
		} `json:"ipConfiguration"`
		BackupConfiguration struct {
			Enabled   bool   `json:"enabled"`
			StartTime string `json:"startTime"`
		} `json:"backupConfiguration"`
	} `json:"settings"`
}

// GCPGKECluster represents a GKE cluster from gcloud CLI
type GCPGKECluster struct {
	Name                 string           `json:"name"`
	Location             string           `json:"location"`
	Status               string           `json:"status"`
	Endpoint             string           `json:"endpoint"`
	Network              string           `json:"network"`
	Subnetwork           string           `json:"subnetwork"`
	CurrentMasterVersion string           `json:"currentMasterVersion"`
	CurrentNodeVersion   string           `json:"currentNodeVersion"`
	NodePools            []GCPGKENodePool `json:"nodePools"`
}

// GCPGKENodePool represents a node pool in a GKE cluster
type GCPGKENodePool struct {
	Name             string `json:"name"`
	InitialNodeCount int    `json:"initialNodeCount"`
	Version          string `json:"version"`
	Status           string `json:"status"`
	Config           struct {
		MachineType string `json:"machineType"`
		DiskSizeGb  int    `json:"diskSizeGb"`
		DiskType    string `json:"diskType"`
	} `json:"config"`
	Autoscaling struct {
		Enabled      bool `json:"enabled"`
		MinNodeCount int  `json:"minNodeCount"`
		MaxNodeCount int  `json:"maxNodeCount"`
	} `json:"autoscaling"`
}

// GCPVPCNetwork represents a VPC network from gcloud CLI
type GCPVPCNetwork struct {
	Name                  string   `json:"name"`
	SelfLink              string   `json:"selfLink"`
	AutoCreateSubnetworks bool     `json:"autoCreateSubnetworks"`
	Subnetworks           []string `json:"subnetworks"`
	RoutingConfig         struct {
		RoutingMode string `json:"routingMode"`
	} `json:"routingConfig"`
}

// GCPSubnetData represents a subnet from gcloud CLI
type GCPSubnetData struct {
	Name           string `json:"name"`
	Region         string `json:"region"`
	Network        string `json:"network"`
	IpCidrRange    string `json:"ipCidrRange"`
	GatewayAddress string `json:"gatewayAddress"`
	SelfLink       string `json:"selfLink"`
}

// GCPForwardingRule represents a forwarding rule (load balancer frontend) from gcloud CLI
type GCPForwardingRule struct {
	Name                string            `json:"name"`
	Region              string            `json:"region"`
	IPAddress           string            `json:"IPAddress"`
	IPProtocol          string            `json:"IPProtocol"`
	PortRange           string            `json:"portRange"`
	Ports               []string          `json:"ports"`
	Target              string            `json:"target"`
	BackendService      string            `json:"backendService"`
	LoadBalancingScheme string            `json:"loadBalancingScheme"`
	Network             string            `json:"network"`
	Subnetwork          string            `json:"subnetwork"`
	SelfLink            string            `json:"selfLink"`
	NetworkTier         string            `json:"networkTier"`
	Labels              map[string]string `json:"labels"`
}

// GCPBackendService represents a backend service from gcloud CLI
type GCPBackendService struct {
	Name                string   `json:"name"`
	Region              string   `json:"region"`
	Protocol            string   `json:"protocol"`
	Port                int      `json:"port"`
	PortName            string   `json:"portName"`
	TimeoutSec          int      `json:"timeoutSec"`
	LoadBalancingScheme string   `json:"loadBalancingScheme"`
	HealthChecks        []string `json:"healthChecks"`
	Backends            []struct {
		Group          string  `json:"group"`
		BalancingMode  string  `json:"balancingMode"`
		MaxUtilization float64 `json:"maxUtilization"`
		CapacityScaler float64 `json:"capacityScaler"`
	} `json:"backends"`
	SelfLink           string `json:"selfLink"`
	SessionAffinity    string `json:"sessionAffinity"`
	ConnectionDraining struct {
		DrainingTimeoutSec int `json:"drainingTimeoutSec"`
	} `json:"connectionDraining"`
}

// GCPHealthCheck represents a health check from gcloud CLI
type GCPHealthCheck struct {
	Name               string `json:"name"`
	Type               string `json:"type"`
	CheckIntervalSec   int    `json:"checkIntervalSec"`
	TimeoutSec         int    `json:"timeoutSec"`
	HealthyThreshold   int    `json:"healthyThreshold"`
	UnhealthyThreshold int    `json:"unhealthyThreshold"`
	SelfLink           string `json:"selfLink"`
	HttpHealthCheck    *struct {
		Port        int    `json:"port"`
		RequestPath string `json:"requestPath"`
	} `json:"httpHealthCheck,omitempty"`
	HttpsHealthCheck *struct {
		Port        int    `json:"port"`
		RequestPath string `json:"requestPath"`
	} `json:"httpsHealthCheck,omitempty"`
	TcpHealthCheck *struct {
		Port int `json:"port"`
	} `json:"tcpHealthCheck,omitempty"`
}

// GCPURLMap represents a URL map from gcloud CLI
type GCPURLMap struct {
	Name           string `json:"name"`
	DefaultService string `json:"defaultService"`
	SelfLink       string `json:"selfLink"`
	HostRules      []struct {
		Hosts       []string `json:"hosts"`
		PathMatcher string   `json:"pathMatcher"`
	} `json:"hostRules"`
	PathMatchers []struct {
		Name           string `json:"name"`
		DefaultService string `json:"defaultService"`
	} `json:"pathMatchers"`
}

// GCPTargetProxy represents a target HTTP/HTTPS proxy from gcloud CLI
type GCPTargetProxy struct {
	Name     string `json:"name"`
	UrlMap   string `json:"urlMap"`
	SelfLink string `json:"selfLink"`
	// For HTTPS proxies
	SslCertificates []string `json:"sslCertificates,omitempty"`
	// Type: "HTTP" or "HTTPS"
	ProxyType string `json:"-"` // Set by fetch method, not from JSON
}

// GCPFirewallRule represents a firewall rule from gcloud CLI
type GCPFirewallRule struct {
	Name         string   `json:"name"`
	Network      string   `json:"network"` // VPC self-link URL
	Direction    string   `json:"direction"`
	Priority     int      `json:"priority"`
	Disabled     bool     `json:"disabled"`
	SelfLink     string   `json:"selfLink"`
	SourceRanges []string `json:"sourceRanges"`
	TargetTags   []string `json:"targetTags"`
	Allowed      []struct {
		Protocol string   `json:"IPProtocol"`
		Ports    []string `json:"ports"`
	} `json:"allowed"`
}

// GCPDNSManagedZone represents a Cloud DNS managed zone from gcloud CLI.
type GCPDNSManagedZone struct {
	Name        string                    `json:"name"`
	DnsName     string                    `json:"dnsName"`
	Description string                    `json:"description"`
	Visibility  string                    `json:"visibility"` // "public" or "private"
	NameServers []string                  `json:"nameServers"`
	Records     []GCPDNSResourceRecordSet `json:"-"` // populated by fetchDNSRecordSetsFromGCP
}

// GCPDNSResourceRecordSet represents a Cloud DNS record set within a managed zone.
type GCPDNSResourceRecordSet struct {
	Name    string   `json:"name"` // FQDN of the record (with trailing dot)
	Type    string   `json:"type"` // A, AAAA, CNAME, MX, etc.
	TTL     int      `json:"ttl"`
	Rrdatas []string `json:"rrdatas"` // record values (IPs for A, hostnames for CNAME)
}

// GCPCDNBackendService represents a backend service with Cloud CDN enabled (subset of GCPBackendService).
type GCPCDNBackendService struct {
	Name        string `json:"name"`
	EnableCDN   bool   `json:"enableCDN"`
	Description string `json:"description"`
	SelfLink    string `json:"selfLink"`
	CdnPolicy   *struct {
		CacheMode  string `json:"cacheMode"`
		ClientTtl  int    `json:"clientTtl"`
		DefaultTtl int    `json:"defaultTtl"`
	} `json:"cdnPolicy,omitempty"`
}

// gcpCLIData holds all CLI-fetched data for a GCP account, used during graph enrichment
type gcpCLIData struct {
	computeInstances map[string]*GCPComputeInstance  // name → instance
	sqlInstances     map[string]*GCPCloudSQLInstance // name → instance
	gkeClusters      map[string]*GCPGKECluster       // name → cluster
	vpcNetworks      map[string]*GCPVPCNetwork       // name → network
	subnets          map[string]*GCPSubnetData       // selfLink or name → subnet
	firewallRules    map[string]*GCPFirewallRule     // name → firewall rule
	// Load Balancer components
	forwardingRules map[string]*GCPForwardingRule // name → forwarding rule
	backendServices map[string]*GCPBackendService // name → backend service
	healthChecks    map[string]*GCPHealthCheck    // name → health check
	urlMaps         map[string]*GCPURLMap         // name → URL map
	targetProxies   map[string]*GCPTargetProxy    // name → target proxy
	// DNS + CDN
	dnsZones    map[string]*GCPDNSManagedZone    // zone name → zone (with records)
	cdnBackends map[string]*GCPCDNBackendService // backend service name → CDN-enabled backend
}

// NewGCPSource creates a new GCP source
func NewGCPSource(config GCPSourceConfig, logger *slog.Logger) (*GCPSource, error) {
	if logger == nil {
		logger = slog.Default()
	}

	return &GCPSource{
		BaseSource: NewBaseSource("gcp"),
		config:     config,
		logger:     logger,
		enabled:    true,
	}, nil
}

// GetName returns the name of the source
func (s *GCPSource) GetName() string {
	return "gcp"
}

// IsEnabled checks if the source is enabled
func (s *GCPSource) IsEnabled() bool {
	return s.enabled
}

// Validate validates the source configuration
func (s *GCPSource) Validate() error {
	return nil
}

// GenerateUniqueKey generates a unique key for a GCP node
// Format: gcp:{account}:{region}:{NodeType}:{project_id}:{short_name}
func (s *GCPSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	keyComponents := core.NewUniqueKeyComponents("gcp", node.NodeType)

	// Extract name
	name, _ := core.GetNodePropertyString(node, "name")

	// For GCP resources, use the short name (last segment after /)
	shortName := extractGCPShortName(name)
	if shortName != "" {
		keyComponents.Name = shortName
	} else if name != "" {
		keyComponents.Name = name
	}

	// Extract account
	if node.CloudAccountID != "" {
		keyComponents.Account = node.CloudAccountID
	}

	// Extract region
	if region, ok := core.GetNodePropertyString(node, "region"); ok {
		keyComponents.Location = region
	}

	// Extract project ID as hierarchy
	projectID := extractGCPProjectID(name)
	if projectID != "" {
		keyComponents.Hierarchy = projectID
	}

	if err := keyComponents.Validate(); err != nil {
		return fmt.Sprintf("gcp:%s:%s:%s:%s:%s", "", "", node.NodeType, "", keyComponents.Name)
	}

	return keyComponents.Build()
}

// BuildGraph builds a knowledge graph from GCP resources
func (s *GCPSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	ctx := reqCtx.GetContext()
	s.logger.Info("building knowledge graph from GCP resources",
		"tenant_id", req.TenantID,
		"cloud_account_id", req.CloudAccountID,
		"service_type_filter_enabled", len(s.config.ServiceTypeFilter) > 0)

	startTime := time.Now()

	// Fetch GCP resources from database
	resources, err := s.fetchGCPResources(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GCP resources: %w", err)
	}

	s.logger.Info("fetched GCP resources", "count", len(resources))

	// Convert resources to nodes and edges
	nodes, edges := s.convertResourcesToGraph(reqCtx, resources, req)

	// Deduplicate
	nodes = core.DeduplicateNodes(nodes)
	edges = core.DeduplicateEdges(edges)

	graph := &core.Graph{
		Nodes:          nodes,
		Edges:          edges,
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
		GeneratedAt:    time.Now(),
	}

	s.logger.Info("successfully built knowledge graph from GCP resources",
		"nodes", len(nodes),
		"edges", len(edges),
		"duration", time.Since(startTime).Seconds())

	return graph, nil
}

// fetchGCPResources queries GCP resources from the cloud_resourses table
func (s *GCPSource) fetchGCPResources(ctx context.Context, req *core.SourceBuildRequest) ([]CloudResourceRow, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
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
			AND cr.cloud_provider = 'GCP'
	`

	args := []interface{}{req.TenantID}
	argIndex := 2

	if req.CloudAccountID != "" {
		query += fmt.Sprintf(" AND cr.account = $%d", argIndex)
		args = append(args, req.CloudAccountID)
		argIndex++
	}

	if req.Region != "" {
		query += fmt.Sprintf(" AND cr.region = $%d", argIndex)
		args = append(args, req.Region)
		argIndex++
	}

	if len(s.config.ResourceTypes) > 0 {
		query += fmt.Sprintf(" AND cr.type = ANY($%d)", argIndex)
		args = append(args, pq.Array(s.config.ResourceTypes))
	}

	if !s.config.IncludeInactive {
		query += " AND cr.is_active = true"
	}

	query += " ORDER BY cr.type, cr.name"

	var resources []CloudResourceRow
	err = dbManager.Db.Select(&resources, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud_resourses: %w", err)
	}

	s.logger.Info("queried GCP cloud resources from database",
		"count", len(resources),
		"tenant_id", req.TenantID)

	return resources, nil
}

// shouldIncludeResource checks if a resource should be included based on ServiceTypeFilter
func (s *GCPSource) shouldIncludeResource(resource *CloudResourceRow) bool {
	if len(s.config.ServiceTypeFilter) == 0 {
		return true
	}

	allowedTypes, serviceHasFilter := s.config.ServiceTypeFilter[resource.ServiceName]
	if !serviceHasFilter {
		return true
	}

	resourceTypeLower := strings.ToLower(resource.Type)
	for _, allowedType := range allowedTypes {
		if strings.ToLower(allowedType) == resourceTypeLower {
			return true
		}
	}

	return false
}

// convertResourcesToGraph converts GCP resources to knowledge graph nodes and edges
func (s *GCPSource) convertResourcesToGraph(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Step 1: Create all nodes (with service-type filtering)
	nodes := make([]*core.DbNode, 0, len(resources))
	for _, resource := range resources {
		if !s.shouldIncludeResource(&resource) {
			s.logger.Debug("skipping GCP resource due to service-type filter",
				"service_name", resource.ServiceName,
				"type", resource.Type,
				"name", resource.Name)
			continue
		}
		node := s.createNodeFromResource(&resource, req)
		nodes = append(nodes, node)
	}

	// Step 2: Build lookup maps for efficient edge creation
	lookup := newNodeLookup(nodes)

	// Step 3: Fetch CLI data to enrich resources missing metadata
	cliData := s.fetchAllGCPCLIData(reqCtx, req)

	// Step 4: Ensure VPC, Subnet, Node Pool, and Load Balancer nodes exist from CLI data
	nodes = s.ensureGCPVPCNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPSubnetNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPNodePoolNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPLoadBalancerNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPBackendServiceNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPHealthCheckNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPTargetProxyNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPURLMapNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPDNSZoneNodes(nodes, lookup, cliData, req)
	nodes = s.ensureGCPCDNNodes(nodes, lookup, cliData, req)

	// Rebuild lookup after adding new nodes
	lookup = newNodeLookup(nodes)

	// Step 5: Enrich nodes with CLI data (add vpc_id, subnet_id to properties)
	s.enrichNodesFromCLIData(nodes, cliData)

	// Step 6: Create edges
	edges := make([]*core.DbEdge, 0)

	// Compute Instance → VPC/Subnet edges
	edges = append(edges, s.createComputeInstanceEdges(nodes, lookup, req)...)

	// Cloud SQL → VPC edges
	edges = append(edges, s.createCloudSQLEdges(nodes, lookup, req)...)

	// Cloud SQL Replica → Primary edges
	edges = append(edges, s.createCloudSQLReplicaEdges(nodes, lookup, req)...)

	// GKE Cluster → VPC/Subnet edges
	edges = append(edges, s.createGKEClusterEdges(nodes, lookup, req)...)

	// Subnet → VPC edges
	edges = append(edges, s.createSubnetToVPCEdges(nodes, lookup, req)...)

	// Node Pool → GKE Cluster edges
	edges = append(edges, s.createNodePoolToClusterEdges(lookup, req)...)

	// GKE Compute Instance → GKE Cluster and Node Pool edges (inferred from labels/naming pattern)
	edges = append(edges, s.createGKEInstanceEdges(reqCtx, lookup, req)...)

	// Load Balancer → VPC/Subnet/BackendPool edges
	edges = append(edges, s.createLoadBalancerEdges(nodes, lookup, cliData, req)...)

	// Load Balancer chain: ForwardingRule → TargetProxy → URLMap → BackendService → HealthCheck
	edges = append(edges, s.createLoadBalancerChainEdges(lookup, cliData, req)...)

	// BigQuery Table/View → Dataset edges
	edges = append(edges, s.createBigQueryEdges(nodes, lookup, req)...)

	// Firewall Rule → VPC edges
	edges = append(edges, s.createFirewallRuleEdges(nodes, lookup, req)...)

	// Cloud Run → Artifact Registry edges
	edges = append(edges, s.createCloudRunEdges(nodes, lookup, req)...)

	return nodes, edges
}

// createNodeFromResource creates a knowledge graph node from a GCP resource row
func (s *GCPSource) createNodeFromResource(resource *CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	source := "gcp"
	nodeType := s.determineNodeType(resource.Type, resource.ServiceName)

	properties := make(map[string]interface{})
	properties["name"] = resource.Name
	properties["type"] = resource.Type
	properties["status"] = resource.Status
	properties["cloud_provider"] = resource.CloudProvider
	properties["region"] = resource.Region
	properties["labels"] = resource.Tags
	properties["arn"] = resource.ARN
	properties["resource_id"] = resource.ResourceID
	properties["service_name"] = resource.ServiceName
	properties["is_active"] = resource.IsActive
	properties["external_resource_id"] = resource.ExternalResourceID

	// Store identifiers
	properties["nb_resource_id"] = resource.ID
	properties["nb_account_id"] = resource.Account
	properties["account_number"] = resource.AccountNumber

	// Extract GCP project ID from name
	projectID := extractGCPProjectID(resource.Name)
	if projectID != "" {
		properties["gcp_project_id"] = projectID
	}

	// Add subtype for GCP resources
	switch nodeType {
	case core.NodeTypeDatabase:
		switch resource.ServiceName {
		case "Cloud SQL":
			properties["subtype"] = "CloudSQL"
		case "BigQuery", "bigquery.googleapis.com":
			properties["subtype"] = "BigQuery"
		default:
			properties["subtype"] = "Database"
		}
	case core.NodeTypeManagedCluster:
		properties["subtype"] = "GKE"
	case core.NodeTypeComputeInstance:
		properties["subtype"] = "ComputeEngine"
	case core.NodeTypeStorage:
		switch resource.ServiceName {
		case "Cloud Storage":
			properties["subtype"] = "CloudStorage"
		case "Cloud Filestore":
			properties["subtype"] = "Filestore"
		default:
			properties["subtype"] = "Storage"
		}
	case core.NodeTypeAIService:
		properties["subtype"] = resource.ServiceName
	case core.NodeTypeLogAggregator:
		properties["subtype"] = "CloudLogging"
	case core.NodeTypeMonitoringService:
		properties["subtype"] = "CloudMonitoring"
	case core.NodeTypeServerlessFunction:
		properties["subtype"] = "CloudRun"
	case core.NodeTypeContainerRegistry:
		properties["subtype"] = "ArtifactRegistry"
	case core.NodeTypeTopic:
		properties["subtype"] = "PubSubTopic"
	case core.NodeTypeSecurityGroup:
		if resource.Type == "firewall-rule" {
			properties["subtype"] = "FirewallRule"
		}
	default:
		if _, exists := properties["subtype"]; !exists {
			properties["subtype"] = resource.Type
		}
	}

	// Parse metadata if available and extract essential fields
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil && len(metaMap) > 0 {
			properties["meta"] = metaMap
			s.extractGCPMetadataByNodeType(properties, metaMap, nodeType, resource.ServiceName)
		}
	}

	// For Cloud Run: extract registry_name from container_image in meta
	if nodeType == core.NodeTypeServerlessFunction {
		if metaMap, ok := properties["meta"].(map[string]interface{}); ok {
			if containerImage, ok := metaMap["container_image"].(string); ok && containerImage != "" {
				if registryName := extractArtifactRegistryName(containerImage); registryName != "" {
					properties["registry_name"] = registryName
				}
			}
		}
	}

	// Synthesize public DNS for GCP resources whose metadata doesn't expose
	// one (Cloud Storage today). Runs unconditionally — even for rows with
	// empty `meta` — since the synthesizer reads only `name` + `service_name`
	// + `region`, all set above. No-op when dns_name is already populated by
	// Cloud SQL connectionName / GKE endpoint / Cloud Run url extractors.
	synthesizeGCPEndpointDNS(properties)

	// Parse and add tags (normalize array values to strings for GCP Asset Inventory format)
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			normalizedLabels := normalizeGCPLabels(tagsMap)
			properties["labels"] = normalizedLabels

			// Extract compute-specific properties from labels (for resources without metadata)
			if nodeType == core.NodeTypeComputeInstance {
				extractComputePropertiesFromLabels(properties, normalizedLabels)
			}
		}
	}

	// Build unique key
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, source)
}

// ========================================================================
// Metadata Extraction from Existing Meta (asset-inventory resources)
// ========================================================================

// extractGCPMetadataByNodeType extracts essential metadata fields from GCP resource meta
func (s *GCPSource) extractGCPMetadataByNodeType(properties map[string]interface{}, metaMap map[string]interface{}, nodeType core.NodeType, serviceName string) {
	switch nodeType {
	case core.NodeTypeComputeInstance:
		s.extractGCPComputeMetadata(properties, metaMap)
	case core.NodeTypeDatabase:
		if serviceName == "Cloud SQL" {
			s.extractGCPCloudSQLMetadata(properties, metaMap)
		}
	case core.NodeTypeManagedCluster:
		s.extractGCPGKEMetadata(properties, metaMap)
	case core.NodeTypeLoadBalancer:
		s.extractGCPForwardingRuleMetadata(properties, metaMap)
	case core.NodeTypeBackendPool:
		s.extractGCPTargetPoolMetadata(properties, metaMap)
	case core.NodeTypeServerlessFunction:
		// Cloud Run carries a per-service URL in `meta.url`; copy its host
		// to dns_name so DirectEndpointMatch hits when eBPF observes it.
		extractGCPCloudRunURL(properties, metaMap)
	}
}

// extractGCPForwardingRuleMetadata extracts routing info from forwarding rule meta
func (s *GCPSource) extractGCPForwardingRuleMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract target (target pool or target proxy URL) → short name for edge lookup
	if target, ok := metaMap["target"].(string); ok && target != "" {
		properties["target_name"] = extractGCPResourceNameFromURL(target)
		properties["target_url"] = target
	}

	// Extract network/subnet if present (not all forwarding rules have these in DB meta)
	if network, ok := metaMap["network"].(string); ok && network != "" {
		properties["vpc_id"] = extractGCPResourceNameFromURL(network)
		properties["vpc_network_url"] = network
	}
	if subnetwork, ok := metaMap["subnetwork"].(string); ok && subnetwork != "" {
		properties["subnet_id"] = extractGCPResourceNameFromURL(subnetwork)
		properties["subnet_url"] = subnetwork
	}
}

// extractGCPTargetPoolMetadata extracts instance references from target pool meta
func (s *GCPSource) extractGCPTargetPoolMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	instances, ok := metaMap["instances"].([]interface{})
	if !ok || len(instances) == 0 {
		return
	}

	instanceNames := make([]string, 0, len(instances))
	for _, inst := range instances {
		if url, ok := inst.(string); ok && url != "" {
			name := extractGCPResourceNameFromURL(url)
			if name != "" {
				instanceNames = append(instanceNames, name)
			}
		}
	}
	if len(instanceNames) > 0 {
		properties["instance_names"] = instanceNames
	}
}

// extractGCPComputeMetadata extracts network info from compute.googleapis.com/Instance meta
func (s *GCPSource) extractGCPComputeMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Extract zone
	if zone, ok := metaMap["zone"].(string); ok && zone != "" {
		properties["zone"] = zone
	}

	// Extract machine type
	if machineType, ok := metaMap["machine_type"].(string); ok && machineType != "" {
		properties["machine_type"] = extractGCPResourceNameFromURL(machineType)
	}

	// Extract network interfaces
	networkInterfaces, ok := metaMap["network_interfaces"].([]interface{})
	if !ok || len(networkInterfaces) == 0 {
		return
	}

	firstNI, ok := networkInterfaces[0].(map[string]interface{})
	if !ok {
		return
	}

	// VPC from network URL
	if network, ok := firstNI["network"].(string); ok && network != "" {
		vpcName := extractGCPResourceNameFromURL(network)
		properties["vpc_id"] = vpcName
		properties["vpc_network_url"] = network
	}

	// Subnet from subnetwork URL
	if subnetwork, ok := firstNI["subnetwork"].(string); ok && subnetwork != "" {
		subnetName := extractGCPResourceNameFromURL(subnetwork)
		properties["subnet_id"] = subnetName
		properties["subnet_url"] = subnetwork
	}

	// Private IP
	if networkIP, ok := firstNI["network_i_p"].(string); ok && networkIP != "" {
		properties["private_ip"] = networkIP
	}

	// External IP from access configs
	if accessConfigs, ok := firstNI["access_configs"].([]interface{}); ok && len(accessConfigs) > 0 {
		if ac, ok := accessConfigs[0].(map[string]interface{}); ok {
			if natIP, ok := ac["nat_i_p"].(string); ok && natIP != "" {
				properties["public_ip"] = natIP
			}
		}
	}
}

// extractGCPCloudSQLMetadata extracts network info from sqladmin.googleapis.com/Instance meta
func (s *GCPSource) extractGCPCloudSQLMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Database version
	if dbVersion, ok := metaMap["databaseVersion"].(string); ok && dbVersion != "" {
		properties["engine"] = dbVersion
	}

	// Connection name (used as dns_name)
	if connectionName, ok := metaMap["connectionName"].(string); ok && connectionName != "" {
		properties["dns_name"] = connectionName
	}

	// IP addresses
	if ipAddresses, ok := metaMap["ipAddresses"].([]interface{}); ok && len(ipAddresses) > 0 {
		if ip, ok := ipAddresses[0].(map[string]interface{}); ok {
			if ipAddr, ok := ip["ipAddress"].(string); ok && ipAddr != "" {
				properties["private_ip"] = ipAddr
			}
		}
	}

	// VPC from settings.ipConfiguration.privateNetwork
	if settings, ok := metaMap["settings"].(map[string]interface{}); ok {
		if ipConfig, ok := settings["ipConfiguration"].(map[string]interface{}); ok {
			if privateNetwork, ok := ipConfig["privateNetwork"].(string); ok && privateNetwork != "" {
				vpcName := extractGCPResourceNameFromURL(privateNetwork)
				properties["vpc_id"] = vpcName
				properties["vpc_network_url"] = privateNetwork
			}
		}
	}

	// Instance type and replica relationship
	if instanceType, ok := metaMap["instanceType"].(string); ok && instanceType != "" {
		properties["instance_type"] = instanceType
	}
	if masterInstanceName, ok := metaMap["masterInstanceName"].(string); ok && masterInstanceName != "" {
		// Format: "project:instance-name" → extract short name after ":"
		colonIdx := strings.LastIndex(masterInstanceName, ":")
		if colonIdx >= 0 && colonIdx < len(masterInstanceName)-1 {
			properties["master_instance_name"] = masterInstanceName[colonIdx+1:]
		} else {
			properties["master_instance_name"] = masterInstanceName
		}
	}
}

// extractGCPGKEMetadata extracts network info from container.googleapis.com/Cluster meta
func (s *GCPSource) extractGCPGKEMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Endpoint
	if endpoint, ok := metaMap["endpoint"].(string); ok && endpoint != "" {
		properties["dns_name"] = endpoint
	}

	// Network config
	if networkConfig, ok := metaMap["network_config"].(map[string]interface{}); ok {
		if network, ok := networkConfig["network"].(string); ok && network != "" {
			vpcName := extractGCPResourceNameFromURL(network)
			properties["vpc_id"] = vpcName
			properties["vpc_network_url"] = network
		}
		if subnetwork, ok := networkConfig["subnetwork"].(string); ok && subnetwork != "" {
			subnetName := extractGCPResourceNameFromURL(subnetwork)
			properties["subnet_id"] = subnetName
			properties["subnet_url"] = subnetwork
		}
	}

	// Fallback: top-level network/subnetwork fields
	if _, hasVPC := properties["vpc_id"]; !hasVPC {
		if network, ok := metaMap["network"].(string); ok && network != "" {
			properties["vpc_id"] = network
		}
	}
	if _, hasSubnet := properties["subnet_id"]; !hasSubnet {
		if subnetwork, ok := metaMap["subnetwork"].(string); ok && subnetwork != "" {
			properties["subnet_id"] = subnetwork
		}
	}

	// Current node version
	if version, ok := metaMap["current_master_version"].(string); ok && version != "" {
		properties["kubernetes_version"] = version
	}
}

// ========================================================================
// CLI Fetch Functions
// ========================================================================

// fetchAllGCPCLIData fetches all GCP metadata via gcloud CLI in bulk
func (s *GCPSource) fetchAllGCPCLIData(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (data *gcpCLIData) {
	data = &gcpCLIData{
		computeInstances: make(map[string]*GCPComputeInstance),
		sqlInstances:     make(map[string]*GCPCloudSQLInstance),
		gkeClusters:      make(map[string]*GCPGKECluster),
		vpcNetworks:      make(map[string]*GCPVPCNetwork),
		subnets:          make(map[string]*GCPSubnetData),
		firewallRules:    make(map[string]*GCPFirewallRule),
		// Load Balancer components
		forwardingRules: make(map[string]*GCPForwardingRule),
		backendServices: make(map[string]*GCPBackendService),
		healthChecks:    make(map[string]*GCPHealthCheck),
		urlMaps:         make(map[string]*GCPURLMap),
		targetProxies:   make(map[string]*GCPTargetProxy),
		dnsZones:        make(map[string]*GCPDNSManagedZone),
		cdnBackends:     make(map[string]*GCPCDNBackendService),
	}

	// Guard against panics from CLI calls (e.g., missing cloud-collector config in test environments)
	defer func() {
		if r := recover(); r != nil {
			s.logger.Warn("recovered from panic during GCP CLI data fetch", "error", fmt.Sprintf("%v", r))
		}
	}()

	if req.CloudAccountID == "" {
		s.logger.Warn("no cloud account ID, skipping GCP CLI enrichment")
		return data
	}

	accountID := req.CloudAccountID

	// Fetch VPC networks
	networks, err := s.fetchVPCNetworksFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP VPC networks via CLI", "error", err)
	} else {
		for i := range networks {
			data.vpcNetworks[networks[i].Name] = &networks[i]
		}
		s.logger.Info("fetched GCP VPC networks via CLI", "count", len(networks))
	}

	// Fetch subnets
	subnets, err := s.fetchSubnetsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP subnets via CLI", "error", err)
	} else {
		for i := range subnets {
			data.subnets[subnets[i].SelfLink] = &subnets[i]
			// Also index by name for easier lookup
			data.subnets[subnets[i].Name] = &subnets[i]
		}
		s.logger.Info("fetched GCP subnets via CLI", "count", len(subnets))
	}

	// Fetch firewall rules
	firewallRules, err := s.fetchFirewallRulesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP firewall rules via CLI", "error", err)
	} else {
		for i := range firewallRules {
			data.firewallRules[firewallRules[i].Name] = &firewallRules[i]
		}
		s.logger.Info("fetched GCP firewall rules via CLI", "count", len(firewallRules))
	}

	// Fetch compute instances
	instances, err := s.fetchComputeInstancesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP compute instances via CLI", "error", err)
	} else {
		for i := range instances {
			data.computeInstances[instances[i].Name] = &instances[i]
		}
		s.logger.Info("fetched GCP compute instances via CLI", "count", len(instances))
	}

	// Fetch Cloud SQL instances
	sqlInstances, err := s.fetchCloudSQLInstancesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP Cloud SQL instances via CLI", "error", err)
	} else {
		for i := range sqlInstances {
			data.sqlInstances[sqlInstances[i].Name] = &sqlInstances[i]
		}
		s.logger.Info("fetched GCP Cloud SQL instances via CLI", "count", len(sqlInstances))
	}

	// Fetch GKE clusters
	clusters, err := s.fetchGKEClustersFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP GKE clusters via CLI", "error", err)
	} else {
		for i := range clusters {
			data.gkeClusters[clusters[i].Name] = &clusters[i]
		}
		s.logger.Info("fetched GCP GKE clusters via CLI", "count", len(clusters))
	}

	// Fetch Load Balancer components
	// Forwarding rules (load balancer frontends)
	forwardingRules, err := s.fetchForwardingRulesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP forwarding rules via CLI", "error", err)
	} else {
		for i := range forwardingRules {
			data.forwardingRules[forwardingRules[i].Name] = &forwardingRules[i]
		}
		s.logger.Info("fetched GCP forwarding rules via CLI", "count", len(forwardingRules))
	}

	// Backend services
	backendServices, err := s.fetchBackendServicesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP backend services via CLI", "error", err)
	} else {
		for i := range backendServices {
			data.backendServices[backendServices[i].Name] = &backendServices[i]
		}
		s.logger.Info("fetched GCP backend services via CLI", "count", len(backendServices))
	}

	// Health checks
	healthChecks, err := s.fetchHealthChecksFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP health checks via CLI", "error", err)
	} else {
		for i := range healthChecks {
			data.healthChecks[healthChecks[i].Name] = &healthChecks[i]
		}
		s.logger.Info("fetched GCP health checks via CLI", "count", len(healthChecks))
	}

	// URL maps (for HTTP(S) load balancers)
	urlMaps, err := s.fetchURLMapsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP URL maps via CLI", "error", err)
	} else {
		for i := range urlMaps {
			data.urlMaps[urlMaps[i].Name] = &urlMaps[i]
		}
		s.logger.Info("fetched GCP URL maps via CLI", "count", len(urlMaps))
	}

	// Target proxies (HTTP and HTTPS)
	targetProxies, err := s.fetchTargetProxiesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP target proxies via CLI", "error", err)
	} else {
		for i := range targetProxies {
			data.targetProxies[targetProxies[i].Name] = &targetProxies[i]
		}
		s.logger.Info("fetched GCP target proxies via CLI", "count", len(targetProxies))
	}

	// Cloud DNS managed zones (and their record sets)
	dnsZones, err := s.fetchDNSZonesFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP DNS zones via CLI", "error", err)
	} else {
		for i := range dnsZones {
			data.dnsZones[dnsZones[i].Name] = &dnsZones[i]
		}
		s.logger.Info("fetched GCP DNS zones via CLI", "count", len(dnsZones))
	}

	// Cloud CDN-enabled backend services
	cdnBackends, err := s.fetchCDNBackendsFromGCP(reqCtx, accountID)
	if err != nil {
		s.logger.Warn("failed to fetch GCP Cloud CDN backends via CLI", "error", err)
	} else {
		for i := range cdnBackends {
			data.cdnBackends[cdnBackends[i].Name] = &cdnBackends[i]
		}
		s.logger.Info("fetched GCP Cloud CDN backends via CLI", "count", len(cdnBackends))
	}

	return data
}

// fetchComputeInstancesFromGCP fetches all compute instances via gcloud CLI
func (s *GCPSource) fetchComputeInstancesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPComputeInstance, error) {
	cmd := "gcloud compute instances list --format=json"

	s.logger.Info("fetching GCP compute instances via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var instances []GCPComputeInstance
	if err := json.Unmarshal([]byte(output), &instances); err != nil {
		return nil, fmt.Errorf("failed to parse compute instances response: %w", err)
	}

	return instances, nil
}

// fetchCloudSQLInstancesFromGCP fetches all Cloud SQL instances via gcloud CLI
func (s *GCPSource) fetchCloudSQLInstancesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPCloudSQLInstance, error) {
	cmd := "gcloud sql instances list --format=json"

	s.logger.Info("fetching GCP Cloud SQL instances via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var instances []GCPCloudSQLInstance
	if err := json.Unmarshal([]byte(output), &instances); err != nil {
		return nil, fmt.Errorf("failed to parse Cloud SQL instances response: %w", err)
	}

	return instances, nil
}

// fetchGKEClustersFromGCP fetches all GKE clusters via gcloud CLI
func (s *GCPSource) fetchGKEClustersFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPGKECluster, error) {
	cmd := "gcloud container clusters list --format=json"

	s.logger.Info("fetching GCP GKE clusters via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var clusters []GCPGKECluster
	if err := json.Unmarshal([]byte(output), &clusters); err != nil {
		return nil, fmt.Errorf("failed to parse GKE clusters response: %w", err)
	}

	return clusters, nil
}

// fetchVPCNetworksFromGCP fetches all VPC networks via gcloud CLI
func (s *GCPSource) fetchVPCNetworksFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPVPCNetwork, error) {
	cmd := "gcloud compute networks list --format=json"

	s.logger.Info("fetching GCP VPC networks via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var networks []GCPVPCNetwork
	if err := json.Unmarshal([]byte(output), &networks); err != nil {
		return nil, fmt.Errorf("failed to parse VPC networks response: %w", err)
	}

	return networks, nil
}

// fetchSubnetsFromGCP fetches all subnets via gcloud CLI
func (s *GCPSource) fetchSubnetsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPSubnetData, error) {
	cmd := "gcloud compute networks subnets list --format=json"

	s.logger.Info("fetching GCP subnets via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var subnets []GCPSubnetData
	if err := json.Unmarshal([]byte(output), &subnets); err != nil {
		return nil, fmt.Errorf("failed to parse subnets response: %w", err)
	}

	return subnets, nil
}

// fetchFirewallRulesFromGCP fetches all firewall rules via gcloud CLI
func (s *GCPSource) fetchFirewallRulesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPFirewallRule, error) {
	cmd := "gcloud compute firewall-rules list --format=json"

	s.logger.Info("fetching GCP firewall rules via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var rules []GCPFirewallRule
	if err := json.Unmarshal([]byte(output), &rules); err != nil {
		return nil, fmt.Errorf("failed to parse firewall rules response: %w", err)
	}

	return rules, nil
}

// ========================================================================
// Load Balancer CLI Fetch Functions
// ========================================================================

// fetchForwardingRulesFromGCP fetches all forwarding rules (load balancer frontends) via gcloud CLI
func (s *GCPSource) fetchForwardingRulesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPForwardingRule, error) {
	// Fetch both global and regional forwarding rules
	allRules := []GCPForwardingRule{}

	// Global forwarding rules
	globalCmd := "gcloud compute forwarding-rules list --global --format=json"
	s.logger.Info("fetching GCP global forwarding rules via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   globalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch global forwarding rules", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var rules []GCPForwardingRule
			if err := json.Unmarshal([]byte(output), &rules); err == nil {
				for i := range rules {
					rules[i].Region = "global"
				}
				allRules = append(allRules, rules...)
			}
		}
	}

	// Regional forwarding rules (all regions)
	regionalCmd := "gcloud compute forwarding-rules list --format=json"
	s.logger.Info("fetching GCP regional forwarding rules via CLI", "account_id", accountID)

	resp, err = cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   regionalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch regional forwarding rules", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var rules []GCPForwardingRule
			if err := json.Unmarshal([]byte(output), &rules); err == nil {
				allRules = append(allRules, rules...)
			}
		}
	}

	return allRules, nil
}

// fetchBackendServicesFromGCP fetches all backend services via gcloud CLI
func (s *GCPSource) fetchBackendServicesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPBackendService, error) {
	allServices := []GCPBackendService{}

	// Global backend services
	globalCmd := "gcloud compute backend-services list --global --format=json"
	s.logger.Info("fetching GCP global backend services via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   globalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch global backend services", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var services []GCPBackendService
			if err := json.Unmarshal([]byte(output), &services); err == nil {
				for i := range services {
					services[i].Region = "global"
				}
				allServices = append(allServices, services...)
			}
		}
	}

	// Regional backend services
	regionalCmd := "gcloud compute backend-services list --format=json"
	s.logger.Info("fetching GCP regional backend services via CLI", "account_id", accountID)

	resp, err = cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   regionalCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch regional backend services", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var services []GCPBackendService
			if err := json.Unmarshal([]byte(output), &services); err == nil {
				allServices = append(allServices, services...)
			}
		}
	}

	return allServices, nil
}

// fetchHealthChecksFromGCP fetches all health checks via gcloud CLI
func (s *GCPSource) fetchHealthChecksFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPHealthCheck, error) {
	cmd := "gcloud compute health-checks list --format=json"

	s.logger.Info("fetching GCP health checks via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var healthChecks []GCPHealthCheck
	if err := json.Unmarshal([]byte(output), &healthChecks); err != nil {
		return nil, fmt.Errorf("failed to parse health checks response: %w", err)
	}

	return healthChecks, nil
}

// fetchURLMapsFromGCP fetches all URL maps via gcloud CLI
func (s *GCPSource) fetchURLMapsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPURLMap, error) {
	cmd := "gcloud compute url-maps list --format=json"

	s.logger.Info("fetching GCP URL maps via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var urlMaps []GCPURLMap
	if err := json.Unmarshal([]byte(output), &urlMaps); err != nil {
		return nil, fmt.Errorf("failed to parse URL maps response: %w", err)
	}

	return urlMaps, nil
}

// fetchTargetProxiesFromGCP fetches all target HTTP and HTTPS proxies via gcloud CLI
func (s *GCPSource) fetchTargetProxiesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPTargetProxy, error) {
	allProxies := []GCPTargetProxy{}

	// HTTP proxies
	httpCmd := "gcloud compute target-http-proxies list --format=json"
	s.logger.Info("fetching GCP target HTTP proxies via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   httpCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch target HTTP proxies", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var proxies []GCPTargetProxy
			if err := json.Unmarshal([]byte(output), &proxies); err == nil {
				for i := range proxies {
					proxies[i].ProxyType = "HTTP"
				}
				allProxies = append(allProxies, proxies...)
			}
		}
	}

	// HTTPS proxies
	httpsCmd := "gcloud compute target-https-proxies list --format=json"
	s.logger.Info("fetching GCP target HTTPS proxies via CLI", "account_id", accountID)

	resp, err = cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   httpsCmd,
	}, 3)
	if err != nil {
		s.logger.Warn("failed to fetch target HTTPS proxies", "error", err)
	} else {
		output, err := parseGCloudCLIResponse(resp)
		if err == nil {
			var proxies []GCPTargetProxy
			if err := json.Unmarshal([]byte(output), &proxies); err == nil {
				for i := range proxies {
					proxies[i].ProxyType = "HTTPS"
				}
				allProxies = append(allProxies, proxies...)
			}
		}
	}

	return allProxies, nil
}

// fetchDNSZonesFromGCP fetches all Cloud DNS managed zones via gcloud CLI, then for each zone
// fetches its record sets and attaches them to the zone struct.
func (s *GCPSource) fetchDNSZonesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPDNSManagedZone, error) {
	cmd := "gcloud dns managed-zones list --format=json"
	s.logger.Info("fetching GCP DNS managed zones via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var zones []GCPDNSManagedZone
	if err := json.Unmarshal([]byte(output), &zones); err != nil {
		return nil, fmt.Errorf("failed to parse DNS zones response: %w", err)
	}

	// For each zone, fetch its record sets. Per-zone fetch is required because gcloud doesn't
	// expose a "list all record sets across all zones" command.
	for i := range zones {
		records, err := s.fetchDNSRecordSetsFromGCP(reqCtx, accountID, zones[i].Name)
		if err != nil {
			s.logger.Warn("failed to fetch record sets for DNS zone",
				"zone", zones[i].Name, "error", err)
			continue
		}
		zones[i].Records = records
	}

	return zones, nil
}

// fetchDNSRecordSetsFromGCP fetches all record sets for a single managed zone.
func (s *GCPSource) fetchDNSRecordSetsFromGCP(reqCtx *security.RequestContext, accountID, zoneName string) ([]GCPDNSResourceRecordSet, error) {
	cmd := fmt.Sprintf("gcloud dns record-sets list --zone=%s --format=json", zoneName)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var records []GCPDNSResourceRecordSet
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		return nil, fmt.Errorf("failed to parse DNS records response: %w", err)
	}

	return records, nil
}

// fetchCDNBackendsFromGCP fetches backend services with Cloud CDN enabled via gcloud CLI.
// Cloud CDN is configured on backend services (not as a separate resource), so we filter for
// enableCDN=true to identify CDN-fronted backends.
func (s *GCPSource) fetchCDNBackendsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPCDNBackendService, error) {
	allBackends := []GCPCDNBackendService{}

	// Global backend services (Cloud CDN is only available on global external HTTP(S) LBs).
	cmd := "gcloud compute backend-services list --global --format=json"
	s.logger.Info("fetching GCP global backend services for CDN detection via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var backends []GCPCDNBackendService
	if err := json.Unmarshal([]byte(output), &backends); err != nil {
		return nil, fmt.Errorf("failed to parse backend services response: %w", err)
	}

	for i := range backends {
		if backends[i].EnableCDN {
			allBackends = append(allBackends, backends[i])
		}
	}

	return allBackends, nil
}

// parseGCloudCLIResponse extracts the JSON string from a cloud CLI response
func parseGCloudCLIResponse(resp map[string]any) (string, error) {
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		return dataStr, nil
	}
	if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		return outputStr, nil
	}
	if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		return resultStr, nil
	}

	respBytes, _ := json.Marshal(resp)
	return "", fmt.Errorf("invalid response format from gcloud CLI: expected 'data', 'output', or 'result' field, got: %s", truncateString(string(respBytes), 200))
}

// ========================================================================
// Node Enrichment from CLI Data
// ========================================================================

// enrichNodesFromCLIData enriches nodes with VPC/subnet info and additional properties from CLI data
func (s *GCPSource) enrichNodesFromCLIData(nodes []*core.DbNode, cliData *gcpCLIData) {
	for _, node := range nodes {
		shortName := extractGCPShortName(getNodeName(node))

		switch node.NodeType {
		case core.NodeTypeComputeInstance:
			s.enrichComputeInstanceFromCLI(node, shortName, cliData)

		case core.NodeTypeDatabase:
			serviceName, _ := node.Properties["service_name"].(string)
			if serviceName == "Cloud SQL" {
				s.enrichCloudSQLFromCLI(node, shortName, cliData)
			}

		case core.NodeTypeManagedCluster:
			s.enrichGKEClusterFromCLI(node, shortName, cliData)

		case core.NodeTypeLoadBalancer:
			s.enrichLoadBalancerFromCLI(node, shortName, cliData)

		case core.NodeTypeSecurityGroup:
			s.enrichFirewallRuleFromCLI(node, shortName, cliData)
		}
	}
}

// enrichFirewallRuleFromCLI enriches a firewall rule node with VPC info from CLI data
func (s *GCPSource) enrichFirewallRuleFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	fr, ok := cliData.firewallRules[shortName]
	if !ok {
		return
	}

	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if fr.Network != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
			node.Properties["vpc_network_url"] = fr.Network
		}
	}
	if fr.Direction != "" {
		node.Properties["direction"] = fr.Direction
	}
	if fr.Priority != 0 {
		node.Properties["priority"] = fr.Priority
	}
	node.Properties["disabled"] = fr.Disabled
}

// enrichLoadBalancerFromCLI enriches a forwarding rule node with VPC/subnet/IP info from CLI data.
// When the CLI lookup misses (forwarding rule not in cliData — typically because the CLI fetch
// failed for that region or the rule was created/deleted between fetches), we fall back to
// reading the IP from the node's existing meta (which the DB-side ingestion stores under
// keys like "IPAddress" / "ip_address"). This prevents LoadBalancer nodes from sitting with
// ip_address = NULL, which would block all IP-based cross-account matching rules.
func (s *GCPSource) enrichLoadBalancerFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	fr, ok := cliData.forwardingRules[shortName]
	if !ok {
		// Fallback: try to populate ip_address from existing meta when CLI didn't return this rule.
		s.fillLoadBalancerIPFromMeta(node)
		return
	}

	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if fr.Network != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
			node.Properties["vpc_network_url"] = fr.Network
		}
	}
	if _, hasSubnet := node.Properties["subnet_id"]; !hasSubnet {
		if fr.Subnetwork != "" {
			node.Properties["subnet_id"] = extractGCPResourceNameFromURL(fr.Subnetwork)
			node.Properties["subnet_url"] = fr.Subnetwork
		}
	}

	// Fill in backend_service if not already set from meta
	if _, hasBS := node.Properties["backend_service"]; !hasBS {
		if fr.BackendService != "" {
			node.Properties["backend_service"] = extractGCPResourceNameFromURL(fr.BackendService)
			node.Properties["backend_service_url"] = fr.BackendService
		}
	}

	// Fill in ip_address if not already set — required for the cross-account rule
	// k8s_service_loadbalancer_to_gcp_loadbalancer to match.
	if _, hasIP := node.Properties["ip_address"]; !hasIP {
		if fr.IPAddress != "" {
			node.Properties["ip_address"] = fr.IPAddress
		} else {
			s.fillLoadBalancerIPFromMeta(node)
		}
	}

	// Mark public-facing LBs so the UI can render an "Internet" cap on them.
	if _, hasMarker := node.Properties["is_public_entry"]; !hasMarker && fr.LoadBalancingScheme != "" {
		node.Properties["is_public_entry"] = isGCPLoadBalancerPublic(fr.LoadBalancingScheme)
	}
}

// fillLoadBalancerIPFromMeta reads ip_address from the node's properties.meta map under any of
// several common GCP API field names, as a fallback when the CLI fetch didn't return this rule.
func (s *GCPSource) fillLoadBalancerIPFromMeta(node *core.DbNode) {
	if _, hasIP := node.Properties["ip_address"]; hasIP {
		return
	}
	meta, ok := node.Properties["meta"].(map[string]interface{})
	if !ok {
		return
	}
	for _, key := range []string{"IPAddress", "ip_address", "ipAddress", "frontend_ip", "frontendIpAddress"} {
		if v, ok := meta[key].(string); ok && v != "" {
			node.Properties["ip_address"] = v
			return
		}
	}
}

// isGCPLoadBalancerPublic reports whether a GCP forwarding-rule LoadBalancingScheme value
// indicates the LB is externally reachable from the public internet.
func isGCPLoadBalancerPublic(scheme string) bool {
	return scheme == "EXTERNAL" || scheme == "EXTERNAL_MANAGED"
}

// enrichComputeInstanceFromCLI enriches a compute instance node with CLI data
func (s *GCPSource) enrichComputeInstanceFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	inst, ok := cliData.computeInstances[shortName]
	if !ok {
		return
	}

	// Network information (only if not already set from metadata)
	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC && len(inst.NetworkInterfaces) > 0 {
		ni := inst.NetworkInterfaces[0]
		if ni.Network != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(ni.Network)
			node.Properties["vpc_network_url"] = ni.Network
		}
		if ni.Subnetwork != "" {
			node.Properties["subnet_id"] = extractGCPResourceNameFromURL(ni.Subnetwork)
			node.Properties["subnet_url"] = ni.Subnetwork
		}
		if ni.NetworkIP != "" {
			node.Properties["private_ip"] = ni.NetworkIP
		}
		if len(ni.AccessConfigs) > 0 && ni.AccessConfigs[0].NatIP != "" {
			node.Properties["public_ip"] = ni.AccessConfigs[0].NatIP
		}
	}

	// Additional properties from CLI (always enrich if available)
	if inst.Zone != "" {
		node.Properties["zone"] = extractGCPResourceNameFromURL(inst.Zone)
	}
	if inst.Status != "" {
		node.Properties["instance_state"] = inst.Status
	}
	if inst.MachineType != "" {
		// Only set if not already set from labels
		if _, exists := node.Properties["machine_type"]; !exists {
			node.Properties["machine_type"] = extractGCPResourceNameFromURL(inst.MachineType)
		}
	}

	// Merge CLI labels with existing labels
	if len(inst.Labels) > 0 {
		existingLabels, ok := node.Properties["labels"].(map[string]interface{})
		if !ok {
			existingLabels = make(map[string]interface{})
		}
		for k, v := range inst.Labels {
			// Don't overwrite existing labels
			if _, exists := existingLabels[k]; !exists {
				existingLabels[k] = v
			}
		}
		node.Properties["labels"] = existingLabels
	}

	// Disk information
	if len(inst.Disks) > 0 {
		var totalDiskSizeGb int
		var bootDiskType string
		for _, disk := range inst.Disks {
			if disk.DiskSizeGb != "" {
				// Try to parse disk size
				var size int
				if _, err := fmt.Sscanf(disk.DiskSizeGb, "%d", &size); err == nil {
					totalDiskSizeGb += size
				}
			}
			if disk.Boot && disk.Type != "" {
				bootDiskType = extractGCPResourceNameFromURL(disk.Type)
			}
		}
		if totalDiskSizeGb > 0 {
			node.Properties["total_disk_size_gb"] = totalDiskSizeGb
		}
		if bootDiskType != "" {
			node.Properties["boot_disk_type"] = bootDiskType
		}
	}
}

// enrichCloudSQLFromCLI enriches a Cloud SQL node with CLI data
func (s *GCPSource) enrichCloudSQLFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	sqlInst, ok := cliData.sqlInstances[shortName]
	if !ok {
		return
	}

	// Network information (only if not already set)
	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if sqlInst.Settings.IpConfiguration.PrivateNetwork != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(sqlInst.Settings.IpConfiguration.PrivateNetwork)
			node.Properties["vpc_network_url"] = sqlInst.Settings.IpConfiguration.PrivateNetwork
		}
	}

	// Basic properties
	if sqlInst.ConnectionName != "" {
		node.Properties["dns_name"] = sqlInst.ConnectionName
	}
	if sqlInst.DatabaseVersion != "" {
		node.Properties["engine"] = sqlInst.DatabaseVersion
	}
	if sqlInst.State != "" {
		node.Properties["instance_state"] = sqlInst.State
	}
	if sqlInst.Region != "" {
		node.Properties["region"] = sqlInst.Region
	}

	// IP addresses
	for _, ip := range sqlInst.IpAddresses {
		if ip.Type == "PRIVATE" && ip.IpAddress != "" {
			node.Properties["private_ip"] = ip.IpAddress
		} else if ip.Type == "PRIMARY" && ip.IpAddress != "" {
			node.Properties["public_ip"] = ip.IpAddress
		}
	}

	// Additional Cloud SQL settings
	if sqlInst.Settings.Tier != "" {
		node.Properties["tier"] = sqlInst.Settings.Tier
	}
	if sqlInst.Settings.AvailabilityType != "" {
		node.Properties["availability_type"] = sqlInst.Settings.AvailabilityType
	}
	if sqlInst.Settings.DataDiskSizeGb != "" {
		node.Properties["storage_size_gb"] = sqlInst.Settings.DataDiskSizeGb
	}
	if sqlInst.Settings.DataDiskType != "" {
		node.Properties["storage_type"] = sqlInst.Settings.DataDiskType
	}
	if sqlInst.Settings.IpConfiguration.SslMode != "" {
		node.Properties["ssl_mode"] = sqlInst.Settings.IpConfiguration.SslMode
	}
	node.Properties["backup_enabled"] = sqlInst.Settings.BackupConfiguration.Enabled

	// Replica relationship (for cloud-sql billing type nodes that lack meta)
	if sqlInst.InstanceType != "" {
		if _, already := node.Properties["instance_type"]; !already {
			node.Properties["instance_type"] = sqlInst.InstanceType
		}
	}
	if sqlInst.MasterInstanceName != "" {
		if _, already := node.Properties["master_instance_name"]; !already {
			colonIdx := strings.LastIndex(sqlInst.MasterInstanceName, ":")
			if colonIdx >= 0 && colonIdx < len(sqlInst.MasterInstanceName)-1 {
				node.Properties["master_instance_name"] = sqlInst.MasterInstanceName[colonIdx+1:]
			} else {
				node.Properties["master_instance_name"] = sqlInst.MasterInstanceName
			}
		}
	}
}

// enrichGKEClusterFromCLI enriches a GKE cluster node with CLI data
func (s *GCPSource) enrichGKEClusterFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	cluster, ok := cliData.gkeClusters[shortName]
	if !ok {
		return
	}

	// Network information (only if not already set)
	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if cluster.Network != "" {
			node.Properties["vpc_id"] = cluster.Network
		}
		if cluster.Subnetwork != "" {
			node.Properties["subnet_id"] = cluster.Subnetwork
		}
	}

	// Basic properties
	if cluster.Endpoint != "" {
		node.Properties["dns_name"] = cluster.Endpoint
	}
	if cluster.Status != "" {
		node.Properties["cluster_state"] = cluster.Status
	}
	if cluster.Location != "" {
		node.Properties["location"] = cluster.Location
	}

	// Kubernetes version
	if cluster.CurrentMasterVersion != "" {
		node.Properties["kubernetes_version"] = cluster.CurrentMasterVersion
	}
	if cluster.CurrentNodeVersion != "" {
		node.Properties["node_version"] = cluster.CurrentNodeVersion
	}

	// Node pools information
	if len(cluster.NodePools) > 0 {
		nodePoolsInfo := make([]map[string]interface{}, 0, len(cluster.NodePools))
		var totalNodeCount int
		for _, np := range cluster.NodePools {
			poolInfo := map[string]interface{}{
				"name":               np.Name,
				"initial_node_count": np.InitialNodeCount,
			}
			if np.Version != "" {
				poolInfo["version"] = np.Version
			}
			if np.Status != "" {
				poolInfo["status"] = np.Status
			}
			if np.Config.MachineType != "" {
				poolInfo["machine_type"] = np.Config.MachineType
			}
			if np.Config.DiskSizeGb > 0 {
				poolInfo["disk_size_gb"] = np.Config.DiskSizeGb
			}
			if np.Autoscaling.Enabled {
				poolInfo["autoscaling_enabled"] = true
				poolInfo["min_node_count"] = np.Autoscaling.MinNodeCount
				poolInfo["max_node_count"] = np.Autoscaling.MaxNodeCount
			}
			nodePoolsInfo = append(nodePoolsInfo, poolInfo)
			totalNodeCount += np.InitialNodeCount
		}
		node.Properties["node_pools"] = nodePoolsInfo
		node.Properties["node_pool_count"] = len(cluster.NodePools)
		node.Properties["total_initial_node_count"] = totalNodeCount
	}
}

// ensureGCPVPCNodes ensures VPC nodes exist for all VPC networks from CLI data
func (s *GCPSource) ensureGCPVPCNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, vpc := range cliData.vpcNetworks {
		// Check if a VPC node already exists with this name
		found := false
		if vpcNodes, ok := lookup.byNodeType[core.NodeTypeVPC]; ok {
			for _, existing := range vpcNodes {
				existingName := extractGCPShortName(getNodeName(existing))
				if existingName == name {
					// Enrich existing node
					if vpc.SelfLink != "" {
						existing.Properties["self_link"] = vpc.SelfLink
					}
					existing.Properties["auto_create_subnetworks"] = vpc.AutoCreateSubnetworks
					existing.Properties["routing_mode"] = vpc.RoutingConfig.RoutingMode
					found = true
					break
				}
			}
		}

		if !found {
			// Create new VPC node from CLI data
			properties := map[string]interface{}{
				"name":                    name,
				"type":                    "vpc",
				"subtype":                 "vpc",
				"service_name":            "Networking",
				"cloud_provider":          "GCP",
				"inferred":                false,
				"self_link":               vpc.SelfLink,
				"auto_create_subnetworks": vpc.AutoCreateSubnetworks,
				"routing_mode":            vpc.RoutingConfig.RoutingMode,
				"resource_id":             name,
				"vpc_id":                  name,
			}

			tempNode := &core.DbNode{
				NodeType:       core.NodeTypeVPC,
				Properties:     properties,
				CloudAccountID: req.CloudAccountID,
			}
			uniqueKey := s.GenerateUniqueKey(tempNode)

			node := core.NewNode(core.NodeTypeVPC, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP VPC node from CLI", "name", name)
		}
	}

	return nodes
}

// ensureGCPSubnetNodes ensures Subnet nodes exist for all subnets from CLI data
func (s *GCPSource) ensureGCPSubnetNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for _, subnet := range cliData.subnets {
		// Skip duplicates (we index by both selfLink and name)
		if subnet.SelfLink == "" {
			continue
		}

		// Check if exists
		found := false
		if subnetNodes, ok := lookup.byNodeType[core.NodeTypeSubnet]; ok {
			for _, existing := range subnetNodes {
				existingName := extractGCPShortName(getNodeName(existing))
				if existingName == subnet.Name {
					// Enrich existing
					existing.Properties["ip_cidr_range"] = subnet.IpCidrRange
					existing.Properties["vpc_id"] = extractGCPResourceNameFromURL(subnet.Network)
					existing.Properties["self_link"] = subnet.SelfLink
					found = true
					break
				}
			}
		}

		if !found {
			vpcName := extractGCPResourceNameFromURL(subnet.Network)
			properties := map[string]interface{}{
				"name":            subnet.Name,
				"type":            "subnet",
				"subtype":         "subnet",
				"service_name":    "Networking",
				"cloud_provider":  "GCP",
				"region":          subnet.Region,
				"inferred":        false,
				"ip_cidr_range":   subnet.IpCidrRange,
				"gateway_address": subnet.GatewayAddress,
				"self_link":       subnet.SelfLink,
				"vpc_id":          vpcName,
				"resource_id":     subnet.Name,
				"subnet_id":       subnet.Name,
			}

			tempNode := &core.DbNode{
				NodeType:       core.NodeTypeSubnet,
				Properties:     properties,
				CloudAccountID: req.CloudAccountID,
			}
			uniqueKey := s.GenerateUniqueKey(tempNode)

			node := core.NewNode(core.NodeTypeSubnet, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP subnet node from CLI", "name", subnet.Name, "vpc", vpcName)
		}
	}

	return nodes
}

// ensureGCPNodePoolNodes ensures Node Pool nodes exist for all GKE node pools from CLI data
func (s *GCPSource) ensureGCPNodePoolNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for clusterName, cluster := range cliData.gkeClusters {
		for _, nodePool := range cluster.NodePools {
			// Check if a Node Pool node already exists
			found := false
			if poolNodes, ok := lookup.byNodeType[core.NodeTypeComputeInstancePool]; ok {
				for _, existing := range poolNodes {
					existingName := getNodeName(existing)
					existingCluster, _ := existing.Properties["cluster_name"].(string)
					if existingName == nodePool.Name && existingCluster == clusterName {
						found = true
						break
					}
				}
			}

			if !found {
				// Create new Node Pool node from CLI data
				properties := map[string]interface{}{
					"name":           nodePool.Name,
					"cluster_name":   clusterName,
					"cloud_provider": "GCP",
					"service_name":   "Kubernetes Engine",
					"subtype":        "GKENodePool",
					"inferred":       false,
				}

				// Location from cluster
				if cluster.Location != "" {
					properties["region"] = cluster.Location
				}

				// Node pool config
				if nodePool.Config.MachineType != "" {
					properties["machine_type"] = nodePool.Config.MachineType
				}
				if nodePool.Config.DiskSizeGb > 0 {
					properties["disk_size_gb"] = nodePool.Config.DiskSizeGb
				}
				if nodePool.Config.DiskType != "" {
					properties["disk_type"] = nodePool.Config.DiskType
				}

				// Node counts
				properties["initial_node_count"] = nodePool.InitialNodeCount

				// Version and status
				if nodePool.Version != "" {
					properties["version"] = nodePool.Version
				}
				if nodePool.Status != "" {
					properties["status"] = nodePool.Status
				}

				// Autoscaling config
				properties["autoscaling_enabled"] = nodePool.Autoscaling.Enabled
				if nodePool.Autoscaling.Enabled {
					properties["min_node_count"] = nodePool.Autoscaling.MinNodeCount
					properties["max_node_count"] = nodePool.Autoscaling.MaxNodeCount
				}

				// Generate unique key: gcp:{account}:{location}:ComputeInstancePool:{cluster_name}:{node_pool_name}
				tempNode := &core.DbNode{
					NodeType:       core.NodeTypeComputeInstancePool,
					Properties:     properties,
					CloudAccountID: req.CloudAccountID,
				}
				uniqueKey := fmt.Sprintf("gcp:%s:%s:%s:%s:%s",
					req.CloudAccountID,
					cluster.Location,
					core.NodeTypeComputeInstancePool,
					clusterName,
					nodePool.Name)
				tempNode.UniqueKey = uniqueKey

				node := core.NewNode(core.NodeTypeComputeInstancePool, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
				nodes = append(nodes, node)
				s.logger.Debug("created GCP Node Pool node from CLI",
					"name", nodePool.Name,
					"cluster", clusterName,
					"machine_type", nodePool.Config.MachineType)
			}
		}
	}

	return nodes
}

// ensureGCPLoadBalancerNodes creates LoadBalancer nodes from forwarding rules (CLI data)
// In GCP, forwarding rules are the frontend/entry point of load balancers
func (s *GCPSource) ensureGCPLoadBalancerNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, fr := range cliData.forwardingRules {
		// Check if a LoadBalancer node already exists with this name
		var existingNode *core.DbNode
		if lbNodes, ok := lookup.byNodeType[core.NodeTypeLoadBalancer]; ok {
			for _, existing := range lbNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					existingNode = existing
					break
				}
			}
		}

		// Enrich existing DB node with CLI data (network/subnet not stored in DB meta)
		if existingNode != nil {
			if _, hasVPC := existingNode.Properties["vpc_id"]; !hasVPC {
				if fr.Network != "" {
					existingNode.Properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
					existingNode.Properties["vpc_network_url"] = fr.Network
				}
			}
			if _, hasSubnet := existingNode.Properties["subnet_id"]; !hasSubnet {
				if fr.Subnetwork != "" {
					existingNode.Properties["subnet_id"] = extractGCPResourceNameFromURL(fr.Subnetwork)
					existingNode.Properties["subnet_url"] = fr.Subnetwork
				}
			}
			if _, hasBS := existingNode.Properties["backend_service"]; !hasBS {
				if fr.BackendService != "" {
					existingNode.Properties["backend_service"] = extractGCPResourceNameFromURL(fr.BackendService)
					existingNode.Properties["backend_service_url"] = fr.BackendService
				}
			}
			continue
		}

		if existingNode == nil {
			// Determine load balancer type based on LoadBalancingScheme
			var lbType string
			switch fr.LoadBalancingScheme {
			case "EXTERNAL", "EXTERNAL_MANAGED":
				lbType = "External"
			case "INTERNAL", "INTERNAL_MANAGED":
				lbType = "Internal"
			default:
				lbType = "Network"
			}

			properties := map[string]interface{}{
				"name":                  name,
				"type":                  "forwarding-rule",
				"subtype":               "GCPLoadBalancer",
				"service_name":          "Cloud Load Balancing",
				"cloud_provider":        "GCP",
				"region":                fr.Region,
				"inferred":              false,
				"ip_address":            fr.IPAddress,
				"ip_protocol":           fr.IPProtocol,
				"port_range":            fr.PortRange,
				"load_balancing_scheme": fr.LoadBalancingScheme,
				"load_balancer_type":    lbType,
				"network_tier":          fr.NetworkTier,
				"self_link":             fr.SelfLink,
				"is_public_entry":       isGCPLoadBalancerPublic(fr.LoadBalancingScheme),
			}

			// Add ports if available
			if len(fr.Ports) > 0 {
				properties["ports"] = fr.Ports
			}

			// Add network/subnet info for internal load balancers
			if fr.Network != "" {
				properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
				properties["vpc_network_url"] = fr.Network
			}
			if fr.Subnetwork != "" {
				properties["subnet_id"] = extractGCPResourceNameFromURL(fr.Subnetwork)
				properties["subnet_url"] = fr.Subnetwork
			}

			// Link to backend service if available
			if fr.BackendService != "" {
				properties["backend_service"] = extractGCPResourceNameFromURL(fr.BackendService)
				properties["backend_service_url"] = fr.BackendService
			}

			// Link to target (for target-based LBs)
			if fr.Target != "" {
				targetShortName := extractGCPResourceNameFromURL(fr.Target)
				properties["target"] = targetShortName
				properties["target_name"] = targetShortName // used by createLoadBalancerEdges for edge lookup
				properties["target_url"] = fr.Target
			}

			// Add labels
			if len(fr.Labels) > 0 {
				properties["labels"] = fr.Labels
			}

			// Generate unique key
			uniqueKey := fmt.Sprintf("gcp:%s:%s:%s:%s",
				req.CloudAccountID,
				fr.Region,
				core.NodeTypeLoadBalancer,
				name)

			node := core.NewNode(core.NodeTypeLoadBalancer, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP LoadBalancer node from CLI",
				"name", name,
				"type", lbType,
				"ip", fr.IPAddress)
		}
	}

	return nodes
}

// ensureGCPBackendServiceNodes creates BackendPool nodes from backend services (CLI data)
func (s *GCPSource) ensureGCPBackendServiceNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, bs := range cliData.backendServices {
		// Check if a BackendPool node already exists with this name
		found := false
		if bpNodes, ok := lookup.byNodeType[core.NodeTypeBackendPool]; ok {
			for _, existing := range bpNodes {
				existingName := extractGCPShortName(getNodeName(existing))
				if existingName == name {
					found = true
					break
				}
			}
		}

		if !found {
			properties := map[string]interface{}{
				"name":                  name,
				"type":                  "backend-service",
				"subtype":               "GCPBackendService",
				"service_name":          "Cloud Load Balancing",
				"cloud_provider":        "GCP",
				"region":                bs.Region,
				"inferred":              false,
				"protocol":              bs.Protocol,
				"port":                  bs.Port,
				"port_name":             bs.PortName,
				"timeout_sec":           bs.TimeoutSec,
				"load_balancing_scheme": bs.LoadBalancingScheme,
				"session_affinity":      bs.SessionAffinity,
				"self_link":             bs.SelfLink,
			}

			// Add health checks
			if len(bs.HealthChecks) > 0 {
				healthCheckNames := make([]string, len(bs.HealthChecks))
				for i, hc := range bs.HealthChecks {
					healthCheckNames[i] = extractGCPResourceNameFromURL(hc)
				}
				properties["health_checks"] = healthCheckNames
			}

			// Add backend groups (instance groups or NEGs)
			if len(bs.Backends) > 0 {
				backendGroups := make([]map[string]interface{}, len(bs.Backends))
				for i, backend := range bs.Backends {
					backendGroups[i] = map[string]interface{}{
						"group":           extractGCPResourceNameFromURL(backend.Group),
						"group_url":       backend.Group,
						"balancing_mode":  backend.BalancingMode,
						"max_utilization": backend.MaxUtilization,
						"capacity_scaler": backend.CapacityScaler,
					}
				}
				properties["backends"] = backendGroups
			}

			// Connection draining
			if bs.ConnectionDraining.DrainingTimeoutSec > 0 {
				properties["draining_timeout_sec"] = bs.ConnectionDraining.DrainingTimeoutSec
			}

			// Generate unique key
			uniqueKey := fmt.Sprintf("gcp:%s:%s:%s:%s",
				req.CloudAccountID,
				bs.Region,
				core.NodeTypeBackendPool,
				name)

			node := core.NewNode(core.NodeTypeBackendPool, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP BackendService node from CLI",
				"name", name,
				"protocol", bs.Protocol,
				"backend_count", len(bs.Backends))
		}
	}

	return nodes
}

// ========================================================================
// Edge Creation Methods
// ========================================================================

// createComputeInstanceEdges creates edges from compute instances to VPCs and subnets
func (s *GCPSource) createComputeInstanceEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	computeNodes, ok := lookup.byNodeType[core.NodeTypeComputeInstance]
	if !ok {
		return edges
	}

	for _, node := range computeNodes {
		// Compute → VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// Compute → Subnet
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode := findNodeByNameAndType(lookup, core.NodeTypeSubnet, subnetID); subnetNode != nil {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}
	}

	s.logger.Info("created GCP compute instance edges", "edge_count", len(edges))
	return edges
}

// createCloudSQLEdges creates edges from Cloud SQL instances to VPCs
func (s *GCPSource) createCloudSQLEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	dbNodes, ok := lookup.byNodeType[core.NodeTypeDatabase]
	if !ok {
		return edges
	}

	for _, node := range dbNodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "Cloud SQL" {
			continue
		}

		// Cloud SQL → VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}
	}

	s.logger.Info("created GCP Cloud SQL edges", "edge_count", len(edges))
	return edges
}

// createGKEClusterEdges creates edges from GKE clusters to VPCs and subnets
func (s *GCPSource) createGKEClusterEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	clusterNodes, ok := lookup.byNodeType[core.NodeTypeManagedCluster]
	if !ok {
		return edges
	}

	for _, node := range clusterNodes {
		// GKE → VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// GKE → Subnet
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode := findNodeByNameAndType(lookup, core.NodeTypeSubnet, subnetID); subnetNode != nil {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}
	}

	s.logger.Info("created GCP GKE cluster edges", "edge_count", len(edges))
	return edges
}

// createSubnetToVPCEdges creates edges from subnets to their parent VPCs
func (s *GCPSource) createSubnetToVPCEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	subnetNodes, ok := lookup.byNodeType[core.NodeTypeSubnet]
	if !ok {
		return edges
	}

	for _, node := range subnetNodes {
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}
	}

	s.logger.Info("created GCP subnet → VPC edges", "edge_count", len(edges))
	return edges
}

// createNodePoolToClusterEdges creates edges from Node Pools to their GKE clusters
func (s *GCPSource) createNodePoolToClusterEdges(lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	poolNodes, hasPools := lookup.byNodeType[core.NodeTypeComputeInstancePool]
	if !hasPools {
		return edges
	}

	clusterNodes, hasClusters := lookup.byNodeType[core.NodeTypeManagedCluster]
	if !hasClusters {
		return edges
	}

	// Build cluster lookup by name
	clusterByName := make(map[string]*core.DbNode)
	for _, clusterNode := range clusterNodes {
		if name, ok := clusterNode.Properties["name"].(string); ok {
			shortName := extractGCPShortName(name)
			if shortName != "" {
				clusterByName[shortName] = clusterNode
			}
		}
	}

	for _, poolNode := range poolNodes {
		clusterName, ok := poolNode.Properties["cluster_name"].(string)
		if !ok || clusterName == "" {
			continue
		}

		// Find the matching cluster node
		if clusterNode, exists := clusterByName[clusterName]; exists {
			edgeProps := map[string]interface{}{
				"connection_type": "cluster_node_pool",
			}

			edges = append(edges, s.createEdge(poolNode, clusterNode, core.RelationshipBelongsTo, edgeProps, req))
			s.logger.Debug("created Node Pool → Cluster edge",
				"node_pool", getNodeName(poolNode),
				"cluster", clusterName)
		}
	}

	s.logger.Info("created GCP Node Pool → Cluster edges", "edge_count", len(edges))
	return edges
}

// createGKEInstanceEdges creates edges from GKE compute instances to their GKE clusters and Node Pools
// Uses goog-k8s-cluster-name label (primary) or GKE naming pattern (fallback): gke-{cluster-name}-{node-pool}-{hash}
// Creates two edges per GKE instance:
//   - Compute Instance → GKE Cluster (BELONGS_TO)
//   - Compute Instance → Node Pool (BELONGS_TO)
func (s *GCPSource) createGKEInstanceEdges(_ *security.RequestContext, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	computeNodes, hasCompute := lookup.byNodeType[core.NodeTypeComputeInstance]
	if !hasCompute {
		return edges
	}

	clusterNodes, hasClusters := lookup.byNodeType[core.NodeTypeManagedCluster]
	if !hasClusters {
		return edges
	}

	// Build a map of cluster short names to cluster nodes
	clusterByName := make(map[string]*core.DbNode)
	for _, clusterNode := range clusterNodes {
		if name, ok := clusterNode.Properties["name"].(string); ok {
			shortName := extractGCPShortName(name)
			if shortName != "" {
				clusterByName[shortName] = clusterNode
			}
		}
	}

	// Build a map of (cluster_name, node_pool_name) to node pool nodes
	poolNodes, hasPools := lookup.byNodeType[core.NodeTypeComputeInstancePool]
	nodePoolByKey := make(map[string]*core.DbNode)
	if hasPools {
		for _, poolNode := range poolNodes {
			clusterName, _ := poolNode.Properties["cluster_name"].(string)
			poolName := getNodeName(poolNode)
			if clusterName != "" && poolName != "" {
				key := clusterName + ":" + poolName
				nodePoolByKey[key] = poolNode
			}
		}
	}

	for _, computeNode := range computeNodes {
		name, ok := computeNode.Properties["name"].(string)
		if !ok || name == "" {
			continue
		}

		shortName := extractGCPShortName(name)
		if shortName == "" {
			continue
		}

		var clusterName string
		var nodePoolName string
		var clusterLocation string
		var provisioningModel string

		// Primary: Try to get cluster and node pool info from labels
		if labels, ok := computeNode.Properties["labels"].(map[string]interface{}); ok {
			clusterName = extractGCPLabelValue(labels, "goog-k8s-cluster-name")
			nodePoolName = extractGCPLabelValue(labels, "goog-k8s-node-pool-name")
			clusterLocation = extractGCPLabelValue(labels, "goog-k8s-cluster-location")
			provisioningModel = extractGCPLabelValue(labels, "goog-gke-node-pool-provisioning-model")
		}

		// Also check node properties (set by extractComputePropertiesFromLabels)
		if nodePoolName == "" {
			if np, ok := computeNode.Properties["gke_node_pool_name"].(string); ok {
				nodePoolName = np
			}
		}

		// Fallback: Extract cluster name from GKE instance naming pattern
		if clusterName == "" {
			matches := gkeInstanceNameRegex.FindStringSubmatch(shortName)
			if len(matches) >= 2 {
				clusterName = matches[1]
			}
		}

		if clusterName == "" {
			continue
		}

		// Edge 1: Compute Instance → GKE Cluster (BELONGS_TO)
		if clusterNode, exists := clusterByName[clusterName]; exists {
			edgeProps := map[string]interface{}{
				"connection_type": "gke_node",
				"inferred":        true,
			}
			if nodePoolName != "" {
				edgeProps["node_pool_name"] = nodePoolName
			}
			if clusterLocation != "" {
				edgeProps["cluster_location"] = clusterLocation
			}
			if provisioningModel != "" {
				edgeProps["provisioning_model"] = provisioningModel
			}

			edges = append(edges, s.createEdge(computeNode, clusterNode, core.RelationshipBelongsTo, edgeProps, req))
			s.logger.Debug("created GKE instance → cluster edge",
				"instance", shortName,
				"cluster", clusterName,
				"node_pool", nodePoolName)
		}

		// Edge 2: Compute Instance → Node Pool (BELONGS_TO)
		if nodePoolName != "" {
			poolKey := clusterName + ":" + nodePoolName
			if poolNode, exists := nodePoolByKey[poolKey]; exists {
				poolEdgeProps := map[string]interface{}{
					"connection_type": "node_pool_instance",
					"inferred":        true,
				}
				if provisioningModel != "" {
					poolEdgeProps["provisioning_model"] = provisioningModel
				}

				edges = append(edges, s.createEdge(computeNode, poolNode, core.RelationshipBelongsTo, poolEdgeProps, req))
				s.logger.Debug("created GKE instance → node pool edge",
					"instance", shortName,
					"node_pool", nodePoolName,
					"cluster", clusterName)
			}
		}
	}

	s.logger.Info("created GKE instance edges", "edge_count", len(edges))
	return edges
}

// createLoadBalancerEdges creates edges for Load Balancer components
// - LoadBalancer → VPC (HOSTED_ON)
// - LoadBalancer → Subnet (HOSTED_ON)
// - LoadBalancer → BackendPool/TargetPool (ROUTES_TO)
// - TargetPool → ComputeInstance (ROUTES_TO) - for classic TCP/UDP load balancers
// - BackendPool → GKE Cluster (ROUTES_TO) - if backend is a GKE NEG (backend-service based)
func (s *GCPSource) createLoadBalancerEdges(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Get all LoadBalancer nodes
	lbNodes, hasLBs := lookup.byNodeType[core.NodeTypeLoadBalancer]
	if !hasLBs {
		return edges
	}

	for _, lbNode := range lbNodes {
		// 1. LoadBalancer → VPC edge
		if vpcID, ok := lbNode.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(lbNode, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. LoadBalancer → Subnet edge
		if subnetID, ok := lbNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode := findNodeByNameAndType(lookup, core.NodeTypeSubnet, subnetID); subnetNode != nil {
				edges = append(edges, s.createEdge(lbNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// 3. LoadBalancer → BackendPool (backend-service based LBs)
		if backendService, ok := lbNode.Properties["backend_service"].(string); ok && backendService != "" {
			if bpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, backendService); bpNode != nil {
				edges = append(edges, s.createEdge(lbNode, bpNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "backend_service"}, req))
				s.logger.Debug("created LoadBalancer → BackendPool edge",
					"lb", getNodeName(lbNode),
					"backend_service", backendService)
			}
		}

		// 4. LoadBalancer → TargetPool (classic TCP/UDP LBs use target instead of backend_service)
		if targetName, ok := lbNode.Properties["target_name"].(string); ok && targetName != "" {
			if tpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, targetName); tpNode != nil {
				edges = append(edges, s.createEdge(lbNode, tpNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "target_pool"}, req))
				s.logger.Debug("created LoadBalancer → TargetPool edge",
					"lb", getNodeName(lbNode),
					"target_pool", targetName)
			}
		}
	}

	// BackendPool edges: target-pool → compute instances, backend-service → GKE cluster (NEG)
	bpNodes, hasBPs := lookup.byNodeType[core.NodeTypeBackendPool]
	if hasBPs {
		computeNodes, hasCompute := lookup.byNodeType[core.NodeTypeComputeInstance]
		computeByName := make(map[string]*core.DbNode)
		if hasCompute {
			for _, cn := range computeNodes {
				if n, ok := cn.Properties["name"].(string); ok && n != "" {
					computeByName[extractGCPShortName(n)] = cn
				}
			}
		}

		clusterNodes, hasClusters := lookup.byNodeType[core.NodeTypeManagedCluster]
		clusterByName := make(map[string]*core.DbNode)
		if hasClusters {
			for _, clusterNode := range clusterNodes {
				if name, ok := clusterNode.Properties["name"].(string); ok {
					shortName := extractGCPShortName(name)
					if shortName != "" {
						clusterByName[shortName] = clusterNode
					}
				}
			}
		}

		for _, bpNode := range bpNodes {
			bpType, _ := bpNode.Properties["type"].(string)

			if bpType == "target-pool" {
				// TargetPool → ComputeInstance edges (classic TCP/UDP LBs)
				if instanceNames, ok := bpNode.Properties["instance_names"].([]string); ok {
					for _, instName := range instanceNames {
						if instNode, exists := computeByName[instName]; exists {
							edges = append(edges, s.createEdge(bpNode, instNode, core.RelationshipRoutesTo,
								map[string]interface{}{"connection_type": "target_pool_instance"}, req))
						}
					}
					s.logger.Debug("created TargetPool → ComputeInstance edges",
						"target_pool", getNodeName(bpNode),
						"instance_count", len(instanceNames))
				}
			} else if bpType == "backend-service" {
				// BackendPool → GKE Cluster edges (NEG-based, for backend-service type only)
				if backends, ok := bpNode.Properties["backends"].([]map[string]interface{}); ok {
					for _, backend := range backends {
						if groupURL, ok := backend["group_url"].(string); ok {
							for clusterName, clusterNode := range clusterByName {
								if strings.Contains(groupURL, clusterName) {
									edges = append(edges, s.createEdge(bpNode, clusterNode, core.RelationshipRoutesTo,
										map[string]interface{}{
											"connection_type": "gke_neg",
											"neg_url":         groupURL,
										}, req))
									s.logger.Debug("created BackendPool → GKE Cluster edge",
										"backend_service", getNodeName(bpNode),
										"cluster", clusterName)
									break
								}
							}
						}
					}
				}
			}
		}
	}

	s.logger.Info("created GCP load balancer edges", "edge_count", len(edges))
	return edges
}

// createEdge creates a knowledge graph edge for GCP resources
func (s *GCPSource) createEdge(sourceNode, targetNode *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	return core.NewEdge(
		sourceNode.ID,
		targetNode.ID,
		relType,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"gcp",
	)
}

// determineNodeType determines the knowledge graph node type from GCP resource type and service name
func (s *GCPSource) determineNodeType(resourceType, serviceName string) core.NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	// First, try exact match with type + service_name combination
	if serviceMap, exists := gcpResourceTypeMap[resourceTypeLower]; exists {
		if nodeType, found := serviceMap[serviceName]; found {
			return nodeType
		}
	}

	// Second, try service name fallback
	if nodeType, exists := gcpServiceFallbackMap[serviceName]; exists {
		return nodeType
	}

	// Default fallback for unmapped resources
	return core.NodeTypeCloudResource
}

// ========================================================================
// Helper Functions
// ========================================================================

// extractGCPShortName extracts the short name from a GCP resource name
// GCP names follow the format: {project}/{path}/{resource-name}
// Returns the last segment after the final /
func extractGCPShortName(name string) string {
	if name == "" {
		return ""
	}

	parts := strings.Split(name, "/")
	lastPart := parts[len(parts)-1]
	if lastPart != "" {
		return lastPart
	}

	// If the last part is empty (trailing slash), try the second to last
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}

	return name
}

// extractGCPProjectID extracts the GCP project ID from a resource name
// GCP resource names typically start with {project-id}/...
func extractGCPProjectID(name string) string {
	if name == "" {
		return ""
	}

	parts := strings.Split(name, "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}

	return ""
}

// extractGCPResourceNameFromURL extracts the resource name from a GCP self-link URL
// e.g., "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default" → "default"
// e.g., "projects/my-project/regions/us-central1/subnetworks/default" → "default"
func extractGCPResourceNameFromURL(url string) string {
	if url == "" {
		return ""
	}

	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return url
}

// findNodeByNameAndType finds a node by its short name and type in the lookup
func findNodeByNameAndType(lookup *NodeLookup, nodeType core.NodeType, name string) *core.DbNode {
	nodes, ok := lookup.byNodeType[nodeType]
	if !ok {
		return nil
	}

	for _, node := range nodes {
		nodeName := getNodeName(node)
		shortName := extractGCPShortName(nodeName)

		if shortName == name || nodeName == name {
			return node
		}

		// Also check resource_id and vpc_id properties
		if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID == name {
			return node
		}
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID == name {
			return node
		}
	}

	return nil
}

// getNodeName safely gets the name property from a node
func getNodeName(node *core.DbNode) string {
	if name, ok := node.Properties["name"].(string); ok {
		return name
	}
	return ""
}

// extractComputePropertiesFromLabels extracts compute-related properties from GCP labels
// This is useful for resources without metadata (type=compute-engine from billing)
// that have labels like system:compute.googleapis.com/machine_spec
func extractComputePropertiesFromLabels(properties map[string]interface{}, labels map[string]interface{}) {
	// Extract machine spec from billing/system labels
	if machineSpec := extractGCPLabelValue(labels, "system:compute.googleapis.com/machine_spec"); machineSpec != "" {
		if _, exists := properties["machine_type"]; !exists {
			properties["machine_type"] = machineSpec
		}
	}
	if cores := extractGCPLabelValue(labels, "system:compute.googleapis.com/cores"); cores != "" {
		properties["cpu_cores"] = cores
	}
	if memory := extractGCPLabelValue(labels, "system:compute.googleapis.com/memory"); memory != "" {
		properties["memory_mb"] = memory
	}

	// GKE-specific labels for compute instances that are GKE nodes
	if clusterName := extractGCPLabelValue(labels, "goog-k8s-cluster-name"); clusterName != "" {
		properties["gke_cluster_name"] = clusterName
	}
	if nodePoolName := extractGCPLabelValue(labels, "goog-k8s-node-pool-name"); nodePoolName != "" {
		properties["gke_node_pool_name"] = nodePoolName
	}
	if clusterLocation := extractGCPLabelValue(labels, "goog-k8s-cluster-location"); clusterLocation != "" {
		properties["gke_cluster_location"] = clusterLocation
	}
	if provisioningModel := extractGCPLabelValue(labels, "goog-gke-node-pool-provisioning-model"); provisioningModel != "" {
		properties["provisioning_model"] = provisioningModel // spot, on-demand, reservation, etc.
	}

	// Check if this is a GKE node
	if gkeNode := extractGCPLabelValue(labels, "goog-gke-node"); gkeNode != "" {
		properties["is_gke_node"] = true
	}
}

// normalizeGCPLabels normalizes GCP labels from Asset Inventory format (array values) to simple strings
// GCP Asset Inventory stores labels as: {"key": ["value"]} but we want: {"key": "value"}
func normalizeGCPLabels(labels map[string]interface{}) map[string]interface{} {
	normalized := make(map[string]interface{}, len(labels))
	for key, value := range labels {
		normalized[key] = extractGCPLabelValueFromInterface(value)
	}
	return normalized
}

// extractGCPLabelValueFromInterface extracts a string value from a label that may be stored as string or array
func extractGCPLabelValueFromInterface(value interface{}) string {
	// Handle string value directly
	if strVal, ok := value.(string); ok {
		return strVal
	}

	// Handle array value (GCP Asset Inventory stores labels as arrays)
	if arrVal, ok := value.([]interface{}); ok && len(arrVal) > 0 {
		if strVal, ok := arrVal[0].(string); ok {
			return strVal
		}
	}

	// Handle []string type (in case JSON unmarshals to this)
	if arrVal, ok := value.([]string); ok && len(arrVal) > 0 {
		return arrVal[0]
	}

	return ""
}

// extractGCPLabelValue extracts a label value from GCP labels map
// GCP labels can be stored as either:
// - string: "value"
// - array: ["value"] (GCP Asset Inventory format)
func extractGCPLabelValue(labels map[string]interface{}, key string) string {
	value, exists := labels[key]
	if !exists {
		return ""
	}
	return extractGCPLabelValueFromInterface(value)
}

// truncateString truncates a string to maxLen characters (reuse from aws_source if available)

// ========================================================================
// BigQuery Hierarchy Edges
// ========================================================================

// createBigQueryEdges creates BELONGS_TO edges from BigQuery Tables/Views to their parent Datasets
// Uses the FullID field in meta: "project:dataset.table" → extract dataset name
func (s *GCPSource) createBigQueryEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	dbNodes, ok := lookup.byNodeType[core.NodeTypeDatabase]
	if !ok {
		return edges
	}

	for _, node := range dbNodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "BigQuery" && serviceName != "bigquery.googleapis.com" {
			continue
		}

		resourceType, _ := node.Properties["type"].(string)
		resourceTypeLower := strings.ToLower(resourceType)
		if resourceTypeLower != "bigquery.googleapis.com/table" && resourceTypeLower != "bigquery.googleapis.com/view" {
			continue
		}

		// Extract dataset name from meta.FullID: "project:dataset.table"
		metaMap, ok := node.Properties["meta"].(map[string]interface{})
		if !ok {
			continue
		}
		fullID, _ := metaMap["FullID"].(string)
		datasetName := extractBigQueryDatasetName(fullID)
		if datasetName == "" {
			continue
		}

		// Find dataset node by name
		if datasetNode := findNodeByNameAndType(lookup, core.NodeTypeDatabase, datasetName); datasetNode != nil {
			// Verify it's a dataset
			datasetType, _ := datasetNode.Properties["type"].(string)
			if strings.ToLower(datasetType) == "bigquery.googleapis.com/dataset" || strings.ToLower(datasetType) == "bigquery" {
				edges = append(edges, s.createEdge(node, datasetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "bigquery_dataset"}, req))
			}
		}
	}

	s.logger.Info("created GCP BigQuery hierarchy edges", "edge_count", len(edges))
	return edges
}

// extractBigQueryDatasetName extracts the dataset name from a BigQuery FullID
// Format: "project:dataset.table" or "project:dataset.view"
func extractBigQueryDatasetName(fullID string) string {
	if fullID == "" {
		return ""
	}
	// Split on ":" to get "project" and "dataset.table"
	colonIdx := strings.Index(fullID, ":")
	if colonIdx < 0 || colonIdx == len(fullID)-1 {
		return ""
	}
	datasetAndTable := fullID[colonIdx+1:]
	// Split on "." to get dataset name
	dotIdx := strings.Index(datasetAndTable, ".")
	if dotIdx <= 0 {
		return ""
	}
	return datasetAndTable[:dotIdx]
}

// ========================================================================
// Cloud SQL Replica Edges
// ========================================================================

// createCloudSQLReplicaEdges creates BELONGS_TO edges from Cloud SQL replicas to their primary instances
// Uses meta.instanceType == "READ_REPLICA_INSTANCE" and meta.masterInstanceName == "project:instance-name"
func (s *GCPSource) createCloudSQLReplicaEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	dbNodes, ok := lookup.byNodeType[core.NodeTypeDatabase]
	if !ok {
		return edges
	}

	for _, node := range dbNodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "Cloud SQL" {
			continue
		}

		instanceType, _ := node.Properties["instance_type"].(string)
		if instanceType != "READ_REPLICA_INSTANCE" {
			continue
		}

		masterInstanceName, _ := node.Properties["master_instance_name"].(string)
		if masterInstanceName == "" {
			continue
		}

		// Find the primary instance node by short name
		primaryNode := findNodeByNameAndType(lookup, core.NodeTypeDatabase, masterInstanceName)
		if primaryNode == nil {
			continue
		}

		edges = append(edges, s.createEdge(node, primaryNode, core.RelationshipBelongsTo,
			map[string]interface{}{"connection_type": "read_replica"}, req))
		s.logger.Debug("created Cloud SQL replica → primary edge",
			"replica", getNodeName(node),
			"primary", masterInstanceName)
	}

	s.logger.Info("created GCP Cloud SQL replica edges", "edge_count", len(edges))
	return edges
}

// ========================================================================
// LB Chain Node Ensure Functions
// ========================================================================

// ensureGCPHealthCheckNodes creates HealthCheck nodes from CLI data
func (s *GCPSource) ensureGCPHealthCheckNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, hc := range cliData.healthChecks {
		// Check if already exists
		found := false
		if hcNodes, ok := lookup.byNodeType[core.NodeTypeCloudResource]; ok {
			for _, existing := range hcNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					existingType, _ := existing.Properties["type"].(string)
					if existingType == "health-check" {
						found = true
						break
					}
				}
			}
		}

		if !found {
			properties := map[string]interface{}{
				"name":                name,
				"type":                "health-check",
				"subtype":             "GCPHealthCheck",
				"service_name":        "Cloud Load Balancing",
				"cloud_provider":      "GCP",
				"inferred":            false,
				"check_interval_sec":  hc.CheckIntervalSec,
				"timeout_sec":         hc.TimeoutSec,
				"healthy_threshold":   hc.HealthyThreshold,
				"unhealthy_threshold": hc.UnhealthyThreshold,
				"health_check_type":   hc.Type,
				"self_link":           hc.SelfLink,
			}

			uniqueKey := fmt.Sprintf("gcp:%s:global:%s:health-check:%s",
				req.CloudAccountID, core.NodeTypeCloudResource, name)

			node := core.NewNode(core.NodeTypeCloudResource, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP HealthCheck node from CLI", "name", name)
		}
	}
	return nodes
}

// ensureGCPTargetProxyNodes creates TargetProxy nodes from CLI data
func (s *GCPSource) ensureGCPTargetProxyNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, proxy := range cliData.targetProxies {
		found := false
		if crNodes, ok := lookup.byNodeType[core.NodeTypeCloudResource]; ok {
			for _, existing := range crNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					existingType, _ := existing.Properties["type"].(string)
					if existingType == "target-http-proxy" || existingType == "target-https-proxy" {
						found = true
						break
					}
				}
			}
		}

		if !found {
			proxyType := "target-http-proxy"
			if proxy.ProxyType == "HTTPS" {
				proxyType = "target-https-proxy"
			}

			properties := map[string]interface{}{
				"name":           name,
				"type":           proxyType,
				"subtype":        "GCPTargetProxy",
				"service_name":   "Cloud Load Balancing",
				"cloud_provider": "GCP",
				"inferred":       false,
				"proxy_type":     proxy.ProxyType,
				"url_map":        extractGCPResourceNameFromURL(proxy.UrlMap),
				"url_map_url":    proxy.UrlMap,
				"self_link":      proxy.SelfLink,
			}

			uniqueKey := fmt.Sprintf("gcp:%s:global:%s:%s:%s",
				req.CloudAccountID, core.NodeTypeCloudResource, proxyType, name)

			node := core.NewNode(core.NodeTypeCloudResource, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP TargetProxy node from CLI", "name", name, "type", proxyType)
		}
	}
	return nodes
}

// ensureGCPURLMapNodes creates URLMap nodes from CLI data (NodeTypeRouteTable — same concept as routing rules)
func (s *GCPSource) ensureGCPURLMapNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, urlMap := range cliData.urlMaps {
		found := false
		if rtNodes, ok := lookup.byNodeType[core.NodeTypeRouteTable]; ok {
			for _, existing := range rtNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					found = true
					break
				}
			}
		}

		if !found {
			defaultService := extractGCPResourceNameFromURL(urlMap.DefaultService)
			properties := map[string]interface{}{
				"name":                name,
				"type":                "url-map",
				"subtype":             "GCPURLMap",
				"service_name":        "Cloud Load Balancing",
				"cloud_provider":      "GCP",
				"inferred":            false,
				"default_service":     defaultService,
				"default_service_url": urlMap.DefaultService,
				"self_link":           urlMap.SelfLink,
			}

			uniqueKey := fmt.Sprintf("gcp:%s:global:%s:url-map:%s",
				req.CloudAccountID, core.NodeTypeRouteTable, name)

			node := core.NewNode(core.NodeTypeRouteTable, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP URLMap node from CLI", "name", name, "default_service", defaultService)
		}
	}
	return nodes
}

// ensureGCPDNSZoneNodes creates DNSZone nodes from Cloud DNS managed zones (CLI data).
// Each zone gets a flat a_record_ips list aggregating all A-record values across all record sets,
// so the cross-account rule "gcp_dns_to_loadbalancer" can match them via `contains` against
// LoadBalancer.ip_address.
func (s *GCPSource) ensureGCPDNSZoneNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, zone := range cliData.dnsZones {
		// Skip if a DNSZone node already exists with this name.
		found := false
		if dnsNodes, ok := lookup.byNodeType[core.NodeTypeDNSZone]; ok {
			for _, existing := range dnsNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}

		isPublic := strings.EqualFold(zone.Visibility, "public") || zone.Visibility == ""

		var aRecordIPs []interface{}
		var cnameRecords []interface{}
		for _, r := range zone.Records {
			switch r.Type {
			case "A", "AAAA":
				for _, v := range r.Rrdatas {
					aRecordIPs = append(aRecordIPs, v)
				}
			case "CNAME":
				for _, v := range r.Rrdatas {
					cnameRecords = append(cnameRecords, map[string]interface{}{"name": r.Name, "target": v})
				}
			}
		}

		properties := map[string]interface{}{
			"name":            name,
			"dns_name":        zone.DnsName,
			"type":            "dns-managed-zone",
			"subtype":         "GCPDNSZone",
			"service_name":    "Cloud DNS",
			"cloud_provider":  "GCP",
			"inferred":        false,
			"visibility":      zone.Visibility,
			"is_public_entry": isPublic,
		}
		if len(aRecordIPs) > 0 {
			properties["a_record_ips"] = aRecordIPs
		}
		if len(cnameRecords) > 0 {
			properties["cname_records"] = cnameRecords
		}
		if len(zone.NameServers) > 0 {
			properties["name_servers"] = zone.NameServers
		}

		uniqueKey := fmt.Sprintf("gcp:%s:global:%s:%s",
			req.CloudAccountID, core.NodeTypeDNSZone, name)
		node := core.NewNode(core.NodeTypeDNSZone, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
		nodes = append(nodes, node)
		s.logger.Debug("created GCP DNSZone node from CLI",
			"name", name, "dns_name", zone.DnsName, "a_records", len(aRecordIPs))
	}
	return nodes
}

// ensureGCPCDNNodes creates CDN nodes from Cloud CDN-enabled backend services (CLI data).
// Cloud CDN is a flag on a backend service, not a separate resource — we create a synthetic CDN
// node per CDN-enabled backend so the cross-account rule "gcp_cdn_to_loadbalancer" can match
// CDN.origin_backend_service_name ↔ LoadBalancer.backend_service.
func (s *GCPSource) ensureGCPCDNNodes(nodes []*core.DbNode, lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, cdn := range cliData.cdnBackends {
		found := false
		if cdnNodes, ok := lookup.byNodeType[core.NodeTypeCDN]; ok {
			for _, existing := range cdnNodes {
				if extractGCPShortName(getNodeName(existing)) == name {
					found = true
					break
				}
			}
		}
		if found {
			continue
		}

		properties := map[string]interface{}{
			"name":                        name,
			"type":                        "cloud-cdn",
			"subtype":                     "GCPCDN",
			"service_name":                "Cloud CDN",
			"cloud_provider":              "GCP",
			"inferred":                    false,
			"enabled":                     true,
			"is_public_entry":             true,
			"origin_backend_service_name": name,
			"self_link":                   cdn.SelfLink,
		}
		if cdn.CdnPolicy != nil {
			if cdn.CdnPolicy.CacheMode != "" {
				properties["cache_mode"] = cdn.CdnPolicy.CacheMode
			}
			if cdn.CdnPolicy.DefaultTtl > 0 {
				properties["default_ttl"] = cdn.CdnPolicy.DefaultTtl
			}
		}

		uniqueKey := fmt.Sprintf("gcp:%s:global:%s:%s",
			req.CloudAccountID, core.NodeTypeCDN, name)
		node := core.NewNode(core.NodeTypeCDN, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
		nodes = append(nodes, node)
		s.logger.Debug("created GCP CDN node from CLI", "name", name)
	}
	return nodes
}

// ========================================================================
// Load Balancer Chain Edges
// ========================================================================

// createLoadBalancerChainEdges creates the full LB chain:
// ForwardingRule → TargetProxy → URLMap → BackendService, BackendService → HealthCheck
func (s *GCPSource) createLoadBalancerChainEdges(lookup *NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build lookup maps for TargetProxy and URLMap nodes (by short name)
	targetProxyByName := make(map[string]*core.DbNode)
	if crNodes, ok := lookup.byNodeType[core.NodeTypeCloudResource]; ok {
		for _, n := range crNodes {
			t, _ := n.Properties["type"].(string)
			if t == "target-http-proxy" || t == "target-https-proxy" {
				shortName := extractGCPShortName(getNodeName(n))
				if shortName != "" {
					targetProxyByName[shortName] = n
				}
			}
		}
	}

	urlMapByName := make(map[string]*core.DbNode)
	if rtNodes, ok := lookup.byNodeType[core.NodeTypeRouteTable]; ok {
		for _, n := range rtNodes {
			shortName := extractGCPShortName(getNodeName(n))
			if shortName != "" {
				urlMapByName[shortName] = n
			}
		}
	}

	healthCheckByName := make(map[string]*core.DbNode)
	if crNodes, ok := lookup.byNodeType[core.NodeTypeCloudResource]; ok {
		for _, n := range crNodes {
			t, _ := n.Properties["type"].(string)
			if t == "health-check" {
				shortName := extractGCPShortName(getNodeName(n))
				if shortName != "" {
					healthCheckByName[shortName] = n
				}
			}
		}
	}

	// 1. ForwardingRule → TargetProxy
	if lbNodes, ok := lookup.byNodeType[core.NodeTypeLoadBalancer]; ok {
		for _, lbNode := range lbNodes {
			targetName, _ := lbNode.Properties["target_name"].(string)
			if targetName == "" {
				continue
			}
			if proxyNode, exists := targetProxyByName[targetName]; exists {
				edges = append(edges, s.createEdge(lbNode, proxyNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "target_proxy"}, req))
				s.logger.Debug("created ForwardingRule → TargetProxy edge",
					"lb", getNodeName(lbNode), "proxy", targetName)
			}
		}
	}

	// 2. TargetProxy → URLMap
	for name, proxyNode := range targetProxyByName {
		urlMapName, _ := proxyNode.Properties["url_map"].(string)
		if urlMapName == "" {
			continue
		}
		if urlMapNode, exists := urlMapByName[urlMapName]; exists {
			edges = append(edges, s.createEdge(proxyNode, urlMapNode, core.RelationshipRoutesTo,
				map[string]interface{}{"connection_type": "url_map"}, req))
			s.logger.Debug("created TargetProxy → URLMap edge",
				"proxy", name, "url_map", urlMapName)
		}
	}

	// 3. URLMap → BackendService (default service + path-matcher services)
	for name, urlMapNode := range urlMapByName {
		// Default service
		defaultService, _ := urlMapNode.Properties["default_service"].(string)
		if defaultService != "" {
			if bpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, defaultService); bpNode != nil {
				edges = append(edges, s.createEdge(urlMapNode, bpNode, core.RelationshipRoutesTo,
					map[string]interface{}{"connection_type": "default_backend"}, req))
				s.logger.Debug("created URLMap → BackendService edge",
					"url_map", name, "backend", defaultService)
			}
		}

		// Path-matcher services (path-based routing rules)
		urlMapCLI, hasCLI := cliData.urlMaps[name]
		if hasCLI {
			seenBackends := make(map[string]bool)
			if defaultService != "" {
				seenBackends[defaultService] = true
			}
			for _, pm := range urlMapCLI.PathMatchers {
				pmService := extractGCPResourceNameFromURL(pm.DefaultService)
				if pmService == "" || seenBackends[pmService] {
					continue
				}
				if bpNode := findNodeByNameAndType(lookup, core.NodeTypeBackendPool, pmService); bpNode != nil {
					edges = append(edges, s.createEdge(urlMapNode, bpNode, core.RelationshipRoutesTo,
						map[string]interface{}{
							"connection_type": "path_matcher_backend",
							"path_matcher":    pm.Name,
						}, req))
					seenBackends[pmService] = true
					s.logger.Debug("created URLMap → PathMatcher BackendService edge",
						"url_map", name, "backend", pmService, "matcher", pm.Name)
				}
			}
		}
	}

	// 4. BackendService → HealthCheck
	if bpNodes, ok := lookup.byNodeType[core.NodeTypeBackendPool]; ok {
		for _, bpNode := range bpNodes {
			bpName := extractGCPShortName(getNodeName(bpNode))
			bs, exists := cliData.backendServices[bpName]
			if !exists {
				continue
			}
			for _, hcURL := range bs.HealthChecks {
				hcName := extractGCPResourceNameFromURL(hcURL)
				if hcNode, exists := healthCheckByName[hcName]; exists {
					edges = append(edges, s.createEdge(bpNode, hcNode, core.RelationshipAssociatedWith,
						map[string]interface{}{"connection_type": "health_check"}, req))
					s.logger.Debug("created BackendService → HealthCheck edge",
						"backend", bpName, "health_check", hcName)
				}
			}
		}
	}

	s.logger.Info("created GCP LB chain edges", "edge_count", len(edges))
	return edges
}

// ========================================================================
// Firewall Rule Edges
// ========================================================================

// createFirewallRuleEdges creates PROTECTS edges from Firewall Rules to their VPCs
func (s *GCPSource) createFirewallRuleEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	sgNodes, ok := lookup.byNodeType[core.NodeTypeSecurityGroup]
	if !ok {
		return edges
	}

	for _, node := range sgNodes {
		resourceType, _ := node.Properties["type"].(string)
		if resourceType != "firewall-rule" {
			continue
		}

		vpcID, _ := node.Properties["vpc_id"].(string)
		if vpcID == "" {
			continue
		}

		if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
			direction, _ := node.Properties["direction"].(string)
			edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipProtects,
				map[string]interface{}{
					"connection_type": "firewall_vpc",
					"direction":       direction,
				}, req))
		}
	}

	s.logger.Info("created GCP firewall rule edges", "edge_count", len(edges))
	return edges
}

// ========================================================================
// Cloud Run → Artifact Registry Edges
// ========================================================================

// createCloudRunEdges creates PULLS_FROM edges from Cloud Run services to Artifact Registry
func (s *GCPSource) createCloudRunEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	crNodes, ok := lookup.byNodeType[core.NodeTypeServerlessFunction]
	if !ok {
		return edges
	}

	for _, node := range crNodes {
		registryName, _ := node.Properties["registry_name"].(string)
		if registryName == "" {
			continue
		}

		if regNode := findNodeByNameAndType(lookup, core.NodeTypeContainerRegistry, registryName); regNode != nil {
			edges = append(edges, s.createEdge(node, regNode, core.RelationshipPullsFrom,
				map[string]interface{}{"connection_type": "container_image"}, req))
			s.logger.Debug("created Cloud Run → Artifact Registry edge",
				"service", getNodeName(node), "registry", registryName)
		}
	}

	s.logger.Info("created GCP Cloud Run edges", "edge_count", len(edges))
	return edges
}

// extractArtifactRegistryName extracts the GCP project name from a container image URL.
// The artifact-registry resource in the DB is named after the GCP project, not the repository.
// e.g. "asia-south1-docker.pkg.dev/nudgebee-dev/cloud-run-source-deploy/image@sha256:..." → "nudgebee-dev"
func extractArtifactRegistryName(containerImage string) string {
	if containerImage == "" {
		return ""
	}
	idx := strings.Index(containerImage, ".pkg.dev/")
	if idx < 0 {
		return ""
	}
	rest := containerImage[idx+9:] // skip ".pkg.dev/"
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) >= 1 && parts[0] != "" {
		return parts[0] // GCP project name — matches artifact-registry node name in DB
	}
	return ""
}
