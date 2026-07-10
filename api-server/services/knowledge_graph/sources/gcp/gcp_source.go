package gcp

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// GKE instance naming pattern: gke-{cluster-name}-{node-pool}-{hash}
var gkeInstanceNameRegex = regexp.MustCompile(`^gke-(.+?)-[a-z0-9]+-[a-z0-9]{4}$`)

func init() {
	sources.RegisterSourceFactory("gcp", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewGCPSource(GCPSourceConfig{ServiceTypeFilter: GCPDefaultServiceTypeFilter}, logger)
	}, "GCP cloud resources source (Compute Engine, Cloud SQL, GKE, BigQuery, etc.)")
}

// GCPSource implements the SourceInterface for GCP cloud resources
type GCPSource struct {
	sources.BaseSource
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
	"IAM":                  {"iam.googleapis.com/ServiceAccount"},
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
	// Persistent Disks: collector emits these with type="storage" + service_name="compute.googleapis.com/Disk".
	// Without this entry they fall through to NodeTypeCloudResource (issue #31101 gap #7).
	"storage": {
		"compute.googleapis.com/Disk": core.NodeTypeStorage,
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
		// URL maps are the routing layer of an HTTP(S) LB. The CLI enrichment path
		// (ensureGCPURLMapNodes) and the LB-chain edge builder both treat them as
		// RouteTable; typing the resource-table copy as RouteTable too keeps them on
		// one node instead of leaving an orphaned CloudResource duplicate.
		"Cloud Load Balancing": core.NodeTypeRouteTable,
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
	"Cloud Functions":      core.NodeTypeServerlessFunction,
	// App Engine is GCP's managed serverless app platform — same compute class as
	// Cloud Run / Cloud Functions, so it shares the ServerlessFunction node type
	// (one node per app today; per-service modelling needs richer collection).
	"App Engine": core.NodeTypeServerlessFunction,
	// Secret Manager secrets (collected by the cloud-collector secretManagerService).
	"Secret Manager": core.NodeTypeSecretVault,
	// Cloud Tasks queues and Memorystore Redis (cloud-collector cloudTasksService /
	// memorystoreService).
	"Cloud Tasks": core.NodeTypeQueue,
	"Memorystore": core.NodeTypeCache,
	// Firestore / Datastore databases (operational DB + CDC source) and Cloud
	// Scheduler cron jobs (cloud-collector firestoreService / schedulerService).
	"Firestore":       core.NodeTypeDatabase,
	"Cloud Scheduler": core.NodeTypeCronJob,
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

// GCPServerlessNEG is a serverless Network Endpoint Group — the indirection that
// connects an LB backend service to the Cloud Run / App Engine service it fronts.
// cloudRun.service / appEngine.service name the actual backing service.
type GCPServerlessNEG struct {
	Name                string `json:"name"`
	Region              string `json:"region"`
	NetworkEndpointType string `json:"networkEndpointType"`
	CloudRun            *struct {
		Service string `json:"service"`
	} `json:"cloudRun"`
	AppEngine *struct {
		Service string `json:"service"`
	} `json:"appEngine"`
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
	// Serverless NEGs: resolve a backend service to the Cloud Run / App Engine service behind it
	serverlessNEGs map[string]*GCPServerlessNEG // NEG name → serverless NEG
}

// NewGCPSource creates a new GCP source
func NewGCPSource(config GCPSourceConfig, logger *slog.Logger) (*GCPSource, error) {
	if logger == nil {
		logger = slog.Default()
	}

	return &GCPSource{
		BaseSource: sources.NewBaseSource("gcp"),
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
func (s *GCPSource) fetchGCPResources(ctx context.Context, req *core.SourceBuildRequest) ([]sources.CloudResourceRow, error) {
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

	var resources []sources.CloudResourceRow
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
func (s *GCPSource) shouldIncludeResource(resource *sources.CloudResourceRow) bool {
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
func (s *GCPSource) convertResourcesToGraph(reqCtx *security.RequestContext, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
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
		if node == nil {
			// createNodeFromResource returns nil to suppress emission
			// (e.g. NICs and vm-manager billing stubs — see issue #31101 gaps #8/#9).
			continue
		}
		nodes = append(nodes, node)
	}

	// Step 2: Build lookup maps for efficient edge creation
	lookup := sources.NewNodeLookup(nodes)

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
	lookup = sources.NewNodeLookup(nodes)

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

	// Persistent Disk → ComputeInstance attachment edges (issue #31101 gap #7)
	edges = append(edges, s.createPersistentDiskAttachmentEdges(lookup, req)...)

	// CDN → BackendPool edges (Cloud CDN fronts a backend service)
	edges = append(edges, s.createCDNEdges(lookup, req)...)

	// BackendPool → Cloud Run / App Engine service edges (via serverless NEG), and the
	// backend→service map used to resolve LB-fronted Pub/Sub push endpoints below.
	backendEdges, backendTargets := s.createServerlessBackendEdges(cliData, lookup, req)
	edges = append(edges, backendEdges...)

	// Consumer → Topic edges (Pub/Sub push subscriptions), resolving custom-domain and
	// appspot push endpoints to the Cloud Run / App Engine service behind the LB.
	edges = append(edges, s.createPubSubSubscriptionEdges(resources, cliData, lookup, backendTargets, req)...)

	// Cloud Scheduler CronJob → target (Topic it publishes to, or the service it invokes)
	edges = append(edges, s.createCronJobEdges(cliData, lookup, backendTargets, req)...)

	// ServerlessFunction → ServiceIdentity (RUNS_AS) via the runtime service account
	edges = append(edges, s.createRunsAsEdges(lookup, req)...)

	// ServerlessFunction → SecretVault (USES_SECRET) via referenced Secret Manager secrets
	edges = append(edges, s.createUsesSecretEdges(lookup, req)...)

	// ServiceIdentity → data resource (HAS_ACCESS_TO / PUBLISHES_TO / SUBSCRIBES_TO) via
	// project IAM policy role bindings. Connects the data tier (Firestore/BigQuery/Storage/
	// Pub-Sub) that traces don't observe, through the SA that runs each service (RUNS_AS).
	edges = append(edges, s.createHasAccessEdges(resources, lookup, req)...)

	// ServerlessFunction → Cache (CALLS) via a plain-value Redis endpoint in the
	// service's env matching a Memorystore instance host. Connects app→cache where
	// the endpoint isn't hidden in a secret.
	edges = append(edges, s.createCacheEndpointEdges(lookup, req)...)

	return nodes, edges
}

// gcpIAMPolicyServiceName / gcpIAMBindingType mirror the collector's
// gcloud.ServiceNameIAMPolicy / gcloud.IAMBindingType. Kept as local literals so
// the KG source doesn't import the collector package.
const (
	gcpIAMPolicyServiceName = "IAM Policy"
	gcpIAMBindingType       = "cloudresourcemanager.googleapis.com/IamBinding"
)

// maxIAMAccessFanout caps how many data nodes a single (identity, role) grant may
// connect to. Project-level IAM roles grant access to *every* resource of a type
// in the project; a broad grant (e.g. storage.admin over a project with hundreds
// of buckets) would flood the graph with low-signal edges. Above this many
// candidates we skip the grant and log it rather than guess which are relevant.
const maxIAMAccessFanout = 30

// iamAccessTarget describes what a mapped IAM role grants access to.
type iamAccessTarget struct {
	nodeType core.NodeType
	rel      core.RelationshipType
	// dbConstraint, for NodeTypeDatabase targets, narrows the candidate set to a
	// GCP database sub-kind ("firestore" or "bigquery-dataset"). Empty means the
	// node type alone is sufficient (Storage, Topic).
	dbConstraint string
}

// ========================================================================
// Metadata Extraction from Existing Meta (asset-inventory resources)
// ========================================================================

// ========================================================================
// CLI Fetch Functions
// ========================================================================

// ========================================================================
// Load Balancer CLI Fetch Functions
// ========================================================================

// ========================================================================
// Node Enrichment from CLI Data
// ========================================================================

// ========================================================================
// Edge Creation Methods
// ========================================================================

// ========================================================================
// Helper Functions
// ========================================================================

// truncateString truncates a string to maxLen characters (reuse from aws_source if available)

// ========================================================================
// BigQuery Hierarchy Edges
// ========================================================================

// ========================================================================
// Cloud SQL Replica Edges
// ========================================================================

// ========================================================================
// LB Chain Node Ensure Functions
// ========================================================================

// ========================================================================
// Load Balancer Chain Edges
// ========================================================================

// ========================================================================
// Firewall Rule Edges
// ========================================================================

// Persistent Disk → ComputeInstance Edges
// ========================================================================

// ========================================================================
// GCP IAM ServiceAccount → ServiceIdentity
// ========================================================================

// ========================================================================
// Cloud Run → Artifact Registry Edges
// ========================================================================
