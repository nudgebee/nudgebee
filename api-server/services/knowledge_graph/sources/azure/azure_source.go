package azure

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/lib/pq"
)

func init() {
	sources.RegisterSourceFactory("azure", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewAzureSource(AzureSourceConfig{ServiceTypeFilter: DefaultAzureServiceTypeFilter}, logger)
	}, "Azure cloud resources source (VMs, AKS, VNets, etc.)")
}

// DefaultAzureServiceTypeFilter defines per-service-name type allowlists.
// An entry with an empty []string{} means "drop all types under this service_name"
// (see shouldIncludeResource). Used here to exclude governance, IAM, compliance,
// and observability metadata that adds no topology value.
var DefaultAzureServiceTypeFilter = map[string][]string{
	// Governance / IAM — not topology
	"microsoft.authorization/policyassignments": {},
	"microsoft.authorization/roleassignments":   {},

	// Security plan & findings — compliance, not topology
	"microsoft.security/pricings": {},

	// Observability config — alerting/monitoring metadata, not topology
	"microsoft.insights":                       {},
	"microsoft.insights/metricalerts":          {},
	"microsoft.insights/actiongroups":          {},
	"microsoft.insights/scheduledqueryrules":   {},
	"microsoft.network/networkwatchers":        {},
	"microsoft.operationalinsights/workspaces": {},

	// Niche / non-resources
	"microsoft.eventgrid/extensiontopics": {}, // helper records, not actual topics
	"microsoft.compute/locations":         {}, // location-level diagnostics rows

	// Compute storage artifacts — not topology nodes. Without an explicit entry in
	// azureTypeToNodeType these fall through determineNodeType to the coarse
	// "microsoft.compute" → ComputeInstance prefix fallback, polluting the VM set.
	// Worse, AKS/Databricks per-pod volumes (osdisk / containerrootvolume /
	// scratchvolume) are ephemeral: each build sees fresh GUIDs, so the old set is
	// tombstoned and a new one created, churning thousands of dead rows. They carry
	// no dependency meaning (the KG has no disk node type or attachment edges), so
	// drop them here.
	"microsoft.compute/disks":     {},
	"microsoft.compute/snapshots": {},
	"microsoft.compute/images":    {},
	"microsoft.compute/galleries": {},
}

// AzureSource implements the Source interface for Azure cloud resources
type AzureSource struct {
	sources.BaseSource
	config  AzureSourceConfig
	logger  *slog.Logger
	enabled bool
}

// AzureSourceConfig holds configuration for Azure source
type AzureSourceConfig struct {
	ResourceTypes     []string            // Filter by resource types
	IncludeInactive   bool                // Include inactive resources (default: false)
	ServiceTypeFilter map[string][]string // Filter by service name -> allowed types
}

// NewAzureSource creates a new Azure source
func NewAzureSource(config AzureSourceConfig, logger *slog.Logger) (*AzureSource, error) {
	if logger == nil {
		logger = slog.Default()
	}

	return &AzureSource{
		BaseSource: sources.NewBaseSource("azure"),
		config:     config,
		logger:     logger,
		enabled:    true,
	}, nil
}

// GetName returns the name of the source
func (s *AzureSource) GetName() string {
	return "azure"
}

// IsEnabled checks if the source is enabled
func (s *AzureSource) IsEnabled() bool {
	return s.enabled
}

// Validate validates the source configuration
func (s *AzureSource) Validate() error {
	return nil
}

// GenerateUniqueKey generates a unique key for an Azure node
// Format: azure:{account}:{region}:{NodeType}:{vnet_id}:{name}
func (s *AzureSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	keyComponents := core.NewUniqueKeyComponents("azure", node.NodeType)

	// Extract name
	name, _ := core.GetNodePropertyString(node, "name")
	if name == "" {
		name, _ = core.GetNodePropertyString(node, "resource_id")
	}
	if name == "" {
		name, _ = core.GetNodePropertyString(node, "id")
	}
	keyComponents.Name = name
	keyComponents.Account = node.CloudAccountID

	// Extract region (location)
	region, _ := core.GetNodePropertyString(node, "region")
	if region != "" {
		keyComponents.Location = region
	}

	// Extract hierarchy (VNet name for network resources)
	switch node.NodeType {
	case core.NodeTypeVPC:
		// For VNet nodes themselves, leave hierarchy empty
		keyComponents.Hierarchy = ""
	case core.NodeTypeManagedCluster:
		// Use service identifier
		serviceName, _ := core.GetNodePropertyString(node, "service_name")
		if serviceName == "Microsoft.ContainerService" {
			keyComponents.Hierarchy = "AKS"
		} else if serviceName != "" {
			keyComponents.Hierarchy = serviceName
		}
	default:
		vnetNameHierarchy, _ := core.GetNodePropertyString(node, "vnet_name_hierarchy")
		if vnetNameHierarchy != "" {
			keyComponents.Hierarchy = vnetNameHierarchy
		} else {
			vnetID, _ := core.GetNodePropertyString(node, "vnet_id")
			if vnetID != "" {
				keyComponents.Hierarchy = vnetID
			}
		}
	}

	if err := keyComponents.Validate(); err != nil {
		return s.BaseSource.GenerateUniqueKey(node)
	}

	return keyComponents.Build()
}

// BuildGraph builds a knowledge graph from Azure resources
func (s *AzureSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	ctx := reqCtx.GetContext()
	s.logger.Info("building knowledge graph from Azure resources",
		"tenant_id", req.TenantID,
		"cloud_account_id", req.CloudAccountID,
		"service_type_filter_enabled", len(s.config.ServiceTypeFilter) > 0)

	startTime := time.Now()

	resources, err := s.fetchAzureResources(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Azure resources: %w", err)
	}

	s.logger.Info("fetched Azure resources", "count", len(resources))

	nodes, edges := s.convertResourcesToGraph(reqCtx, resources, req)

	nodes = core.DeduplicateNodes(nodes)
	edges = core.DeduplicateEdges(edges)

	graph := &core.Graph{
		Nodes:          nodes,
		Edges:          edges,
		TenantID:       req.TenantID,
		CloudAccountID: req.CloudAccountID,
		GeneratedAt:    time.Now(),
	}

	s.logger.Info("successfully built knowledge graph from Azure resources",
		"nodes", len(nodes),
		"edges", len(edges),
		"duration", time.Since(startTime).Seconds())

	return graph, nil
}

// fetchAzureResources queries Azure resources from the cloud_resourses table
func (s *AzureSource) fetchAzureResources(ctx context.Context, req *core.SourceBuildRequest) ([]sources.CloudResourceRow, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, COALESCE(cr.arn, '') AS arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.cloud_provider = 'Azure'
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

	s.logger.Info("queried Azure cloud resources from database",
		"count", len(resources),
		"tenant_id", req.TenantID)

	return resources, nil
}

// shouldIncludeResource checks if a resource should be included based on ServiceTypeFilter
func (s *AzureSource) shouldIncludeResource(resource *sources.CloudResourceRow) bool {
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

// convertResourcesToGraph converts Azure resources to knowledge graph nodes and edges
func (s *AzureSource) convertResourcesToGraph(reqCtx *security.RequestContext, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Step 1: Create all nodes
	nodes := make([]*core.DbNode, 0, len(resources))
	for _, resource := range resources {
		if !s.shouldIncludeResource(&resource) {
			s.logger.Debug("skipping resource due to service-type filter",
				"service_name", resource.ServiceName,
				"type", resource.Type,
				"name", resource.Name)
			continue
		}
		if shouldSuppressAzureResource(&resource) {
			s.logger.Debug("suppressing Azure billing/reservation row",
				"service_name", resource.ServiceName, "type", resource.Type, "name", resource.Name)
			continue
		}
		node := s.createNodeFromResource(&resource, req)
		nodes = append(nodes, node)
	}

	// Step 2: Build lookup maps
	lookup := sources.NewNodeLookup(nodes)

	// Step 2.5: Ensure all infrastructure nodes exist BEFORE creating edges
	// Extract subnets from VNet metadata as synthetic nodes
	nodes = s.ensureSubnetNodes(nodes, lookup, req)
	// Fetch NICs via Azure CLI to resolve VM → VNet/Subnet relationships
	nodes = s.ensureNICNodes(reqCtx, nodes, lookup, req)
	// Fetch NSGs via Azure CLI
	nodes = s.ensureNSGNodes(reqCtx, nodes, lookup, req)

	// Step 2.6: Resolve VM network relationships via NIC data
	s.resolveVMNetworkRelationships(nodes, lookup)

	// Step 3: Propagate VNet names to resources
	s.propagateVNetNamesToResources(nodes, lookup)

	// Step 4: Create edges
	edges := make([]*core.DbEdge, 0)

	for nodeType, nodeList := range lookup.ByNodeType {
		switch nodeType {
		case core.NodeTypeVPC:
			// VNet nodes: create edges to subnets + VNet↔VNet peering
			edges = append(edges, s.createVNetEdges(nodeList, lookup, req)...)
			edges = append(edges, s.createVNetPeeringEdges(nodeList, req)...)

		case core.NodeTypeComputeInstance:
			edges = append(edges, s.createVMEdges(nodeList, lookup, req)...)

		case core.NodeTypeManagedCluster:
			edges = append(edges, s.createAKSEdges(nodeList, lookup, req)...)

		case core.NodeTypeSubnet:
			edges = append(edges, s.createSubnetEdges(nodeList, lookup, req)...)

		case core.NodeTypeNetworkInterface:
			edges = append(edges, s.createNICEdges(nodeList, lookup, req)...)

		case core.NodeTypeSecurityGroup:
			// NSG edges are created from the resources that reference them
			continue

		case core.NodeTypeDatabase:
			// Default VNet attachment (as the default case) + SQL server↔database containment.
			edges = append(edges, s.createDefaultVNetEdges(nodeList, lookup, req)...)
			edges = append(edges, s.createSQLServerDatabaseEdges(nodeList, req)...)

		default:
			edges = append(edges, s.createDefaultVNetEdges(nodeList, lookup, req)...)
		}
	}

	// Step 5: Derive cross-resource edges from the embedded resource IDs in each
	// resource's ARG meta (LoadBalancer frontend/backend, etc.) — connects
	// resource types the networking-only builders above leave orphaned.
	edges = append(edges, s.createReferenceEdges(resources, lookup, req)...)

	// ServiceIdentity → resource (HAS_ACCESS_TO) from RBAC role assignments —
	// connects the data tier (Key Vault / Storage / ACR / Cognitive Services) to
	// the managed identities granted access, which traces/ARM metadata don't show.
	edges = append(edges, s.createRoleAssignmentEdges(resources, lookup, req)...)

	// App Service / Function → data resource (CALLS) from app-settings references
	// the collector extracted secret-free into meta.referenced_resources.
	edges = append(edges, s.createAppReferenceEdges(resources, lookup, req)...)

	// Private DNS zone → VNet links are resolved by the azure_private_dns
	// cross-account enricher (Phase 2.1), since a zone and its linked VNet are
	// frequently in different subscriptions and only the unified graph can match
	// them.

	return nodes, edges
}

// azureTypeToNodeType maps lowercase resource type strings to NodeTypes.
// Handles both singular (VirtualMachine) and plural (virtualmachines) forms
// since the DB stores plural lowercase types from Azure Resource Graph.
var azureTypeToNodeType = map[string]core.NodeType{
	// Compute
	"virtualmachine":          core.NodeTypeComputeInstance,
	"virtualmachines":         core.NodeTypeComputeInstance,
	"virtualmachinescalesets": core.NodeTypeComputeInstance,

	// Container orchestration
	"managedcluster":  core.NodeTypeManagedCluster,
	"managedclusters": core.NodeTypeManagedCluster,

	// Networking
	"virtualnetwork":        core.NodeTypeVPC,
	"virtualnetworks":       core.NodeTypeVPC,
	"subnet":                core.NodeTypeSubnet,
	"subnets":               core.NodeTypeSubnet,
	"loadbalancer":          core.NodeTypeLoadBalancer,
	"loadbalancers":         core.NodeTypeLoadBalancer,
	"applicationgateway":    core.NodeTypeLoadBalancer,
	"applicationgateways":   core.NodeTypeLoadBalancer,
	"networksecuritygroup":  core.NodeTypeSecurityGroup,
	"networksecuritygroups": core.NodeTypeSecurityGroup,
	"publicipaddress":       core.NodeTypePublicIP,
	"publicipaddresses":     core.NodeTypePublicIP,
	"networkinterface":      core.NodeTypeNetworkInterface,
	"networkinterfaces":     core.NodeTypeNetworkInterface,
	"natgateway":            core.NodeTypeNetworkGateway,
	"natgateways":           core.NodeTypeNetworkGateway,
	"routetable":            core.NodeTypeRouteTable,
	"routetables":           core.NodeTypeRouteTable,
	"privateendpoint":       core.NodeTypePrivateEndpoint,
	"privateendpoints":      core.NodeTypePrivateEndpoint,
	"frontdoor":             core.NodeTypeCDN,
	"frontdoors":            core.NodeTypeCDN,

	// Databases
	"sqldatabase":      core.NodeTypeDatabase,
	"databases":        core.NodeTypeDatabase, // microsoft.sql/servers type=databases
	"sqlserver":        core.NodeTypeDatabase,
	"servers":          core.NodeTypeDatabase, // microsoft.sql/servers type=servers
	"cosmosdbaccount":  core.NodeTypeDatabase,
	"databaseaccounts": core.NodeTypeDatabase,
	"flexibleservers":  core.NodeTypeDatabase, // microsoft.dbforpostgresql|dbformysql|dbformariadb/flexibleServers

	// Cache
	"rediscache": core.NodeTypeCache,
	"redis":      core.NodeTypeCache,

	// Storage
	"storageaccount":  core.NodeTypeStorage,
	"storageaccounts": core.NodeTypeStorage,

	// Serverless
	"functionapp": core.NodeTypeServerlessFunction,
	"sites":       core.NodeTypeServerlessFunction, // microsoft.web/sites

	// Messaging
	"servicebusnamespace": core.NodeTypeMessageQueue,
	"namespaces":          core.NodeTypeMessageQueue, // microsoft.servicebus/namespaces
	"eventhubnamespace":   core.NodeTypeMessageQueue,

	// Security
	"vault":  core.NodeTypeSecretVault,
	"vaults": core.NodeTypeSecretVault,

	// Container Registry
	"containerregistry": core.NodeTypeContainerRegistry,
	"registries":        core.NodeTypeContainerRegistry,

	// DNS
	"dnszone":         core.NodeTypeDNSZone,
	"dnszones":        core.NodeTypeDNSZone,
	"privatednszone":  core.NodeTypeDNSZone,
	"privatednszones": core.NodeTypeDNSZone,

	// CDN
	"cdnprofile": core.NodeTypeCDN,
	"profiles":   core.NodeTypeCDN, // microsoft.cdn/profiles

	// Identity
	"userassignedidentities": core.NodeTypeServiceIdentity, // microsoft.managedidentity/userassignedidentities

	// Messaging — EventGrid
	"systemtopics": core.NodeTypeTopic, // microsoft.eventgrid/systemtopics

	// Container orchestration — Azure Container Instances
	"containergroups": core.NodeTypeWorkload, // microsoft.containerinstance/containergroups

	// AI
	"botservices": core.NodeTypeAIService, // microsoft.botservice/botservices
}

// azureServicePrefixToNodeType maps the provider prefix from service_name to NodeTypes.
// service_name in DB is like "microsoft.compute/virtualmachines" — we extract the provider prefix.
var azureServicePrefixToNodeType = map[string]core.NodeType{
	"microsoft.compute":           core.NodeTypeComputeInstance,
	"microsoft.containerservice":  core.NodeTypeManagedCluster,
	"microsoft.network":           core.NodeTypeCloudResource,
	"microsoft.sql":               core.NodeTypeDatabase,
	"microsoft.documentdb":        core.NodeTypeDatabase,
	"microsoft.cache":             core.NodeTypeCache,
	"microsoft.storage":           core.NodeTypeStorage,
	"microsoft.web":               core.NodeTypeServerlessFunction,
	"microsoft.servicebus":        core.NodeTypeMessageQueue,
	"microsoft.eventhub":          core.NodeTypeMessageQueue,
	"microsoft.keyvault":          core.NodeTypeSecretVault,
	"microsoft.containerregistry": core.NodeTypeContainerRegistry,
	"microsoft.cdn":               core.NodeTypeCDN,
	// Managed databases (flexible-server PaaS) — also caught by the "flexibleservers"
	// type entry, but the prefix keeps classification correct if the type segment shifts.
	"microsoft.dbforpostgresql": core.NodeTypeDatabase,
	"microsoft.dbformysql":      core.NodeTypeDatabase,
	"microsoft.dbformariadb":    core.NodeTypeDatabase,
	// AI / ML / analytics compute
	"microsoft.cognitiveservices":       core.NodeTypeAIService,
	"microsoft.machinelearningservices": core.NodeTypeAIService,
	"microsoft.databricks":              core.NodeTypeAIService,
	// Arc-enabled (hybrid) servers are compute hosts
	"microsoft.hybridcompute": core.NodeTypeComputeInstance,
	// Logic Apps are serverless workflow executions
	"microsoft.logic": core.NodeTypeServerlessFunction,
	// Communication Services (email/SMS/chat delivery)
	"microsoft.communication": core.NodeTypeEmailService,
}

// Edge creation methods

// ========================================================================
// Ensure Infrastructure Nodes - Create missing nodes before edge creation
// ========================================================================

// AzureIPConfiguration is the inline shape returned by `az network nic list`.
// Note: Azure CLI returns these fields at the top level of each ipConfiguration,
// NOT nested under a "properties" object.
type AzureIPConfiguration struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	PrivateIPAddress string `json:"privateIPAddress"`
	Subnet           struct {
		ID string `json:"id"`
	} `json:"subnet"`
	PublicIPAddress *struct {
		ID string `json:"id"`
	} `json:"publicIPAddress"`
}

// AzureNICData represents a network interface from Azure CLI response
type AzureNICData struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Location             string                 `json:"location"`
	ResourceGroup        string                 `json:"resourceGroup"`
	IPConfigurations     []AzureIPConfiguration `json:"ipConfigurations"`
	NetworkSecurityGroup *struct {
		ID string `json:"id"`
	} `json:"networkSecurityGroup"`
	VirtualMachine *struct {
		ID string `json:"id"`
	} `json:"virtualMachine"`
}

// AzureNSGData represents a network security group from Azure CLI response
type AzureNSGData struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Location      string `json:"location"`
	ResourceGroup string `json:"resourceGroup"`
}

// ========================================================================
// Azure CLI Fetch Methods
// ========================================================================
