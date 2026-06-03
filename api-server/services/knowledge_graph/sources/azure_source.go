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
	"strings"
	"time"

	"github.com/lib/pq"
)

func init() {
	RegisterSourceFactory("azure", func(config SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
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
}

// AzureSource implements the Source interface for Azure cloud resources
type AzureSource struct {
	BaseSource
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
		BaseSource: NewBaseSource("azure"),
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
func (s *AzureSource) fetchAzureResources(ctx context.Context, req *core.SourceBuildRequest) ([]CloudResourceRow, error) {
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

	var resources []CloudResourceRow
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
func (s *AzureSource) shouldIncludeResource(resource *CloudResourceRow) bool {
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
func (s *AzureSource) convertResourcesToGraph(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
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
		node := s.createNodeFromResource(&resource, req)
		nodes = append(nodes, node)
	}

	// Step 2: Build lookup maps
	lookup := newNodeLookup(nodes)

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

	for nodeType, nodeList := range lookup.byNodeType {
		switch nodeType {
		case core.NodeTypeVPC:
			// VNet nodes: create edges to subnets
			edges = append(edges, s.createVNetEdges(nodeList, lookup, req)...)

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

		default:
			edges = append(edges, s.createDefaultVNetEdges(nodeList, lookup, req)...)
		}
	}

	return nodes, edges
}

// createNodeFromResource creates a knowledge graph node from an Azure cloud resource
func (s *AzureSource) createNodeFromResource(resource *CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	source := "azure"
	nodeType := s.determineNodeType(resource.Type, resource.ServiceName)

	properties := make(map[string]interface{})
	properties["name"] = resource.Name
	properties["type"] = resource.Type
	properties["status"] = resource.Status
	properties["cloud_provider"] = resource.CloudProvider
	properties["region"] = resource.Region
	properties["arn"] = resource.ARN // Azure Resource ID stored in arn column
	properties["resource_id"] = resource.ResourceID
	properties["service_name"] = resource.ServiceName
	properties["is_active"] = resource.IsActive
	properties["external_resource_id"] = resource.ExternalResourceID
	properties["labels"] = resource.Tags

	// Store identifiers
	properties["nb_resource_id"] = resource.ID
	properties["nb_account_id"] = resource.Account
	properties["account_number"] = resource.AccountNumber // Azure subscription ID

	// Add subtype
	properties["subtype"] = resource.Type

	// Parse metadata
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
			s.extractEssentialMetadataByNodeType(properties, metaMap, nodeType, resource.ServiceName)
		}
		// Store raw meta for VNet nodes so ensureSubnetNodes can extract embedded subnets
		if nodeType == core.NodeTypeVPC {
			properties["_raw_meta"] = resource.Meta
		}
	}

	// Parse tags
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			properties["labels"] = tagsMap
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
}

// extractServicePrefix extracts the provider prefix from a full service_name path.
// e.g., "microsoft.compute/virtualmachines" → "microsoft.compute"
func extractServicePrefix(serviceName string) string {
	if idx := strings.Index(serviceName, "/"); idx > 0 {
		return serviceName[:idx]
	}
	return serviceName
}

// determineNodeType maps Azure resource types to knowledge graph node types.
// It handles both formats:
//   - Original format: type="VirtualMachine", service_name="Microsoft.Compute"
//   - DB format:       type="virtualmachines", service_name="microsoft.compute/virtualmachines"
func (s *AzureSource) determineNodeType(resourceType, serviceName string) core.NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	// Step 1: Direct type lookup (handles both singular and plural lowercase)
	if nodeType, exists := azureTypeToNodeType[resourceTypeLower]; exists {
		return nodeType
	}

	// Step 2: Some collectors store the full provider-qualified form
	// (e.g. "Microsoft.Network/routeTables") instead of the bare type segment.
	// Try the last "/"-delimited segment as a fallback.
	if idx := strings.LastIndex(resourceTypeLower, "/"); idx >= 0 && idx < len(resourceTypeLower)-1 {
		if nodeType, exists := azureTypeToNodeType[resourceTypeLower[idx+1:]]; exists {
			return nodeType
		}
	}

	// Step 3: Service prefix fallback
	servicePrefix := strings.ToLower(extractServicePrefix(serviceName))
	if nodeType, exists := azureServicePrefixToNodeType[servicePrefix]; exists {
		return nodeType
	}

	return core.NodeTypeCloudResource
}

// extractEssentialMetadataByNodeType extracts Azure-specific metadata fields based on node type.
// Azure Resource Graph returns meta in a nested format:
//
//	{ "id": "...", "name": "...", "properties": { ...actual resource data... } }
//
// This method normalizes by checking both top-level and nested "properties" keys.
func (s *AzureSource) extractEssentialMetadataByNodeType(properties map[string]interface{}, metaMap map[string]interface{}, nodeType core.NodeType, serviceName string) {
	// Azure Resource Graph nests actual data under "properties" key
	propsMap := metaMap
	if nested, ok := metaMap["properties"].(map[string]interface{}); ok {
		propsMap = nested
	}

	// Extract resource group from top-level meta (Azure Resource Graph puts it at top level)
	if rg, ok := metaMap["resourceGroup"].(string); ok && rg != "" {
		properties["resource_group"] = rg
	}

	// Extract location from top-level meta
	if loc, ok := metaMap["location"].(string); ok && loc != "" {
		// Override region with the location from meta if present (more accurate)
		properties["region"] = loc
	}

	// Extract VNet/Subnet IDs common to many resource types (check both levels)
	s.extractCommonNetworkIDs(properties, metaMap, propsMap)

	switch nodeType {
	case core.NodeTypeComputeInstance:
		s.extractVMMetadata(properties, propsMap)
	case core.NodeTypeManagedCluster:
		s.extractAKSMetadata(properties, propsMap)
	case core.NodeTypeVPC:
		s.extractVNetMetadata(properties, propsMap)
	case core.NodeTypeSubnet:
		s.extractSubnetMetadata(properties, propsMap)
	case core.NodeTypeDatabase:
		s.extractDatabaseMetadata(properties, propsMap)
	case core.NodeTypeLoadBalancer:
		s.extractLoadBalancerMetadata(properties, propsMap)
	case core.NodeTypeCache:
		s.extractCacheMetadata(properties, propsMap)
	case core.NodeTypeNetworkInterface:
		s.extractNICMetadata(properties, propsMap)
	case core.NodeTypeCDN:
		s.extractCDNMetadata(properties, propsMap)
	case core.NodeTypeDNSZone:
		s.extractDNSZoneMetadata(properties, propsMap)
	}
}

// extractCommonNetworkIDs extracts vnet_id, subnet_id, nsg_id from metadata.
// Checks both flat keys (vnetId) and nested Azure Resource Graph structures (networkProfile.networkInterfaces).
func (s *AzureSource) extractCommonNetworkIDs(properties map[string]interface{}, metaMap, propsMap map[string]interface{}) {
	// Flat keys (used in simplified/mock data)
	if vnetID, ok := metaMap["vnetId"].(string); ok && vnetID != "" {
		properties["vnet_id"] = vnetID
	}
	if subnetID, ok := metaMap["subnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if nsgID, ok := metaMap["nsgId"].(string); ok && nsgID != "" {
		properties["nsg_id"] = nsgID
	}
	if rg, ok := metaMap["resourceGroup"].(string); ok && rg != "" {
		properties["resource_group"] = rg
	}

	// Also check propsMap (nested properties)
	if vnetID, ok := propsMap["vnetId"].(string); ok && vnetID != "" {
		properties["vnet_id"] = vnetID
	}
	if subnetID, ok := propsMap["subnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if nsgID, ok := propsMap["nsgId"].(string); ok && nsgID != "" {
		properties["nsg_id"] = nsgID
	}
}

func (s *AzureSource) extractVMMetadata(properties map[string]interface{}, propsMap map[string]interface{}) {
	// Flat keys (simplified/mock data)
	if vmSize, ok := propsMap["vmSize"].(string); ok && vmSize != "" {
		properties["vm_size"] = vmSize
	}
	if osType, ok := propsMap["osType"].(string); ok && osType != "" {
		properties["os_type"] = osType
	}
	if privateIP, ok := propsMap["privateIpAddress"].(string); ok && privateIP != "" {
		properties["private_ip"] = privateIP
	}
	if publicIP, ok := propsMap["publicIpAddress"].(string); ok && publicIP != "" {
		properties["public_ip"] = publicIP
	}
	if nicIDs, ok := propsMap["networkInterfaceIds"].([]interface{}); ok {
		properties["network_interface_ids"] = nicIDs
	}

	// Azure Resource Graph nested format: properties.hardwareProfile.vmSize
	if hw, ok := propsMap["hardwareProfile"].(map[string]interface{}); ok {
		if vmSize, ok := hw["vmSize"].(string); ok && vmSize != "" {
			properties["vm_size"] = vmSize
		}
	}

	// properties.storageProfile.osDisk.osType
	if sp, ok := propsMap["storageProfile"].(map[string]interface{}); ok {
		if osDisk, ok := sp["osDisk"].(map[string]interface{}); ok {
			if osType, ok := osDisk["osType"].(string); ok && osType != "" {
				properties["os_type"] = osType
			}
		}
	}

	// properties.networkProfile.networkInterfaces[].id
	if np, ok := propsMap["networkProfile"].(map[string]interface{}); ok {
		if nics, ok := np["networkInterfaces"].([]interface{}); ok {
			nicIDs := make([]interface{}, 0, len(nics))
			for _, nic := range nics {
				if nicMap, ok := nic.(map[string]interface{}); ok {
					if id, ok := nicMap["id"].(string); ok && id != "" {
						nicIDs = append(nicIDs, id)
					}
				}
			}
			if len(nicIDs) > 0 {
				properties["network_interface_ids"] = nicIDs
			}
		}
	}

	// properties.provisioningState
	if state, ok := propsMap["provisioningState"].(string); ok && state != "" {
		properties["provisioning_state"] = state
	}
}

func (s *AzureSource) extractAKSMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if kubeVersion, ok := metaMap["kubernetesVersion"].(string); ok && kubeVersion != "" {
		properties["kubernetes_version"] = kubeVersion
	}
	if fqdn, ok := metaMap["fqdn"].(string); ok && fqdn != "" {
		properties["dns_name"] = fqdn
	}
	if nodePoolCount, ok := metaMap["nodePoolCount"]; ok {
		properties["node_pool_count"] = nodePoolCount
	}
}

func (s *AzureSource) extractVNetMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if addressSpace, ok := metaMap["addressSpace"].(map[string]interface{}); ok {
		if prefixes, ok := addressSpace["addressPrefixes"].([]interface{}); ok {
			properties["cidr_blocks"] = prefixes
		}
	}
	if vnetID, ok := metaMap["vnetId"].(string); ok && vnetID != "" {
		properties["vnet_id"] = vnetID
	}
	// Also use resource_id as vnet_id for VNet nodes
	if resourceID, ok := properties["resource_id"].(string); ok && resourceID != "" {
		properties["vnet_id"] = resourceID
	}
}

func (s *AzureSource) extractSubnetMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if addressPrefix, ok := metaMap["addressPrefix"].(string); ok && addressPrefix != "" {
		properties["cidr_block"] = addressPrefix
	}
	if subnetID, ok := metaMap["subnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if resourceID, ok := properties["resource_id"].(string); ok && resourceID != "" {
		properties["subnet_id"] = resourceID
	}
}

func (s *AzureSource) extractDatabaseMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if fqdn, ok := metaMap["fullyQualifiedDomainName"].(string); ok && fqdn != "" {
		properties["dns_name"] = fqdn
	}
	if endpoint, ok := metaMap["endpoint"].(string); ok && endpoint != "" {
		properties["dns_name"] = endpoint
	}
	if port, ok := metaMap["port"]; ok {
		properties["port"] = port
	}
}

// extractLoadBalancerMetadata handles both Standard Load Balancer (subtype=loadbalancer) and
// Application Gateway (subtype=applicationgateway). Both expose frontend IPs and backend address
// pools; Application Gateway additionally exposes httpListeners with hostnames.
func (s *AzureSource) extractLoadBalancerMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Flat key (simplified/mock data)
	if frontendIP, ok := metaMap["frontendIpAddress"].(string); ok && frontendIP != "" {
		properties["dns_name"] = frontendIP
		properties["ip_address"] = frontendIP
	}
	if sku, ok := metaMap["sku"].(string); ok && sku != "" {
		properties["lb_sku"] = sku
	}
	s.extractAzureLBFrontendIPs(properties, metaMap)
	s.extractAzureLBBackendPools(properties, metaMap)
	s.extractAzureLBHTTPListenerHostnames(properties, metaMap)

	// Mark public entry: Application Gateway is always internet-facing unless explicitly
	// configured as Internal Load Balancer mode; Standard LB is public iff it has a publicIPAddress
	// reference on any frontend IP configuration.
	subtype, _ := properties["subtype"].(string)
	if strings.EqualFold(subtype, "applicationgateway") || strings.EqualFold(subtype, "applicationgateways") {
		properties["is_public_entry"] = true
	} else if _, hasPublicRefs := properties["public_ip_refs"]; hasPublicRefs {
		properties["is_public_entry"] = true
	}
}

// extractAzureLBFrontendIPs reads frontendIPConfigurations[].properties.privateIPAddress
// and publicIPAddress.id from an Azure LB / App Gateway, surfacing ip_address and public_ip_refs.
func (s *AzureSource) extractAzureLBFrontendIPs(properties map[string]interface{}, metaMap map[string]interface{}) {
	frontends, ok := metaMap["frontendIPConfigurations"].([]interface{})
	if !ok {
		return
	}
	var ips []interface{}
	var publicRefs []interface{}
	for _, f := range frontends {
		fProps := nestedProps(f)
		if fProps == nil {
			continue
		}
		if priv, ok := fProps["privateIPAddress"].(string); ok && priv != "" {
			ips = append(ips, priv)
		}
		if pub, ok := fProps["publicIPAddress"].(map[string]interface{}); ok {
			if id, ok := pub["id"].(string); ok && id != "" {
				publicRefs = append(publicRefs, id)
			}
		}
	}
	if len(ips) > 0 {
		properties["frontend_ip_addresses"] = ips
		if _, set := properties["ip_address"]; !set {
			properties["ip_address"] = ips[0]
		}
	}
	if len(publicRefs) > 0 {
		properties["public_ip_refs"] = publicRefs
	}
}

// extractAzureLBBackendPools reads backendAddressPools[] from an Azure LB / App Gateway,
// surfacing backend_pool_ids (always) and backend_addresses (App Gateway only).
func (s *AzureSource) extractAzureLBBackendPools(properties map[string]interface{}, metaMap map[string]interface{}) {
	pools, ok := metaMap["backendAddressPools"].([]interface{})
	if !ok {
		return
	}
	var poolIDs []interface{}
	var backendAddrs []interface{}
	for _, p := range pools {
		pMap, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := pMap["id"].(string); ok && id != "" {
			poolIDs = append(poolIDs, id)
		}
		pProps, _ := pMap["properties"].(map[string]interface{})
		if pProps == nil {
			continue
		}
		// Application Gateway shape: properties.backendAddresses[].fqdn|ipAddress
		if addrs, ok := pProps["backendAddresses"].([]interface{}); ok {
			backendAddrs = append(backendAddrs, collectAzureBackendAddresses(addrs)...)
		}
	}
	if len(poolIDs) > 0 {
		properties["backend_pool_ids"] = poolIDs
	}
	if len(backendAddrs) > 0 {
		properties["backend_addresses"] = backendAddrs
	}
}

// extractAzureLBHTTPListenerHostnames reads httpListeners[].properties.hostName (App Gateway only)
// and surfaces them as frontend_hostnames.
func (s *AzureSource) extractAzureLBHTTPListenerHostnames(properties map[string]interface{}, metaMap map[string]interface{}) {
	listeners, ok := metaMap["httpListeners"].([]interface{})
	if !ok {
		return
	}
	var hostnames []interface{}
	for _, l := range listeners {
		lProps := nestedProps(l)
		if lProps == nil {
			continue
		}
		if h, ok := lProps["hostName"].(string); ok && h != "" {
			hostnames = append(hostnames, h)
		}
	}
	if len(hostnames) > 0 {
		properties["frontend_hostnames"] = hostnames
	}
}

// extractCDNMetadata handles Azure Front Door (subtype=frontdoor) and CDN Profile (subtype=cdnprofile).
// Front Door exposes frontendEndpoints + backendPools inline; CDN Profile only exposes SKU
// (origin endpoints are separate child resources Microsoft.Cdn/profiles/endpoints).
// Both Front Door and CDN Profile are always internet-facing.
func (s *AzureSource) extractCDNMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	properties["is_public_entry"] = true
	s.extractAzureFrontDoorFrontends(properties, metaMap)
	s.extractAzureFrontDoorBackends(properties, metaMap)
	if sku, ok := metaMap["sku"].(map[string]interface{}); ok {
		if name, ok := sku["name"].(string); ok && name != "" {
			properties["cdn_sku"] = name
		}
	}
}

// extractAzureFrontDoorFrontends reads frontendEndpoints[].properties.hostName.
func (s *AzureSource) extractAzureFrontDoorFrontends(properties map[string]interface{}, metaMap map[string]interface{}) {
	endpoints, ok := metaMap["frontendEndpoints"].([]interface{})
	if !ok {
		return
	}
	var hostnames []interface{}
	for _, e := range endpoints {
		eProps := nestedProps(e)
		if eProps == nil {
			continue
		}
		if h, ok := eProps["hostName"].(string); ok && h != "" {
			hostnames = append(hostnames, h)
		}
	}
	if len(hostnames) > 0 {
		properties["frontend_hostnames"] = hostnames
	}
}

// extractAzureFrontDoorBackends reads backendPools[].properties.backends[].address.
func (s *AzureSource) extractAzureFrontDoorBackends(properties map[string]interface{}, metaMap map[string]interface{}) {
	pools, ok := metaMap["backendPools"].([]interface{})
	if !ok {
		return
	}
	var backendAddrs []interface{}
	for _, p := range pools {
		pProps := nestedProps(p)
		if pProps == nil {
			continue
		}
		if backends, ok := pProps["backends"].([]interface{}); ok {
			for _, b := range backends {
				bMap, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				if v, ok := bMap["address"].(string); ok && v != "" {
					backendAddrs = append(backendAddrs, v)
				}
			}
		}
	}
	if len(backendAddrs) > 0 {
		properties["backend_addresses"] = backendAddrs
	}
}

// extractDNSZoneMetadata handles Azure DNS Zone (subtype=dnszone, public) and Private DNS Zone (subtype=privatednszone).
// Note: A-records live as separate child resources (Microsoft.Network/dnszones/A); this extractor
// only captures zone-level metadata. Record extraction would require ingesting the child types.
func (s *AzureSource) extractDNSZoneMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if recordCount, ok := metaMap["numberOfRecordSets"]; ok {
		properties["record_set_count"] = recordCount
	}
	if zoneType, ok := metaMap["zoneType"].(string); ok && zoneType != "" {
		properties["zone_type"] = zoneType
	}
	subtype, _ := properties["subtype"].(string)
	properties["is_public_entry"] = !strings.HasPrefix(strings.ToLower(subtype), "privatednszone")
}

// nestedProps unwraps a map element's "properties" sub-map (Azure Resource Graph nested format),
// or returns the element itself if it's already a flat map. Returns nil if the element is not a map.
func nestedProps(v interface{}) map[string]interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	if inner, ok := m["properties"].(map[string]interface{}); ok {
		return inner
	}
	return m
}

// collectAzureBackendAddresses extracts ipAddress and fqdn values from an Application Gateway
// backendAddresses[] array.
func collectAzureBackendAddresses(addrs []interface{}) []interface{} {
	var out []interface{}
	for _, a := range addrs {
		aMap, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := aMap["ipAddress"].(string); ok && v != "" {
			out = append(out, v)
		}
		if v, ok := aMap["fqdn"].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *AzureSource) extractCacheMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if hostName, ok := metaMap["hostName"].(string); ok && hostName != "" {
		properties["dns_name"] = hostName
	}
	if port, ok := metaMap["sslPort"]; ok {
		properties["port"] = port
	}
	properties["cache_type"] = "redis"
}

func (s *AzureSource) extractNICMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if privateIP, ok := metaMap["privateIpAddress"].(string); ok && privateIP != "" {
		properties["private_ip"] = privateIP
	}
	if vmID, ok := metaMap["virtualMachineId"].(string); ok && vmID != "" {
		properties["attached_vm_id"] = vmID
	}
}

// propagateVNetNamesToResources updates resources with VNet names for hierarchy
func (s *AzureSource) propagateVNetNamesToResources(nodes []*core.DbNode, lookup *NodeLookup) {
	// Build VNet ID → name map
	vnetNameMap := make(map[string]string)
	if vnetNodes, ok := lookup.byNodeType[core.NodeTypeVPC]; ok {
		for _, vnetNode := range vnetNodes {
			if vnetID, ok := vnetNode.Properties["vnet_id"].(string); ok && vnetID != "" {
				if name, ok := vnetNode.Properties["name"].(string); ok && name != "" {
					vnetNameMap[vnetID] = name
				}
			}
			// Also map by resource_id
			if resourceID, ok := vnetNode.Properties["resource_id"].(string); ok && resourceID != "" {
				if name, ok := vnetNode.Properties["name"].(string); ok && name != "" {
					vnetNameMap[resourceID] = name
				}
			}
		}
	}

	// Propagate VNet names
	for _, node := range nodes {
		if node.NodeType == core.NodeTypeVPC {
			continue
		}
		if vnetID, ok := node.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetName, found := vnetNameMap[vnetID]; found {
				node.Properties["vnet_name_hierarchy"] = vnetName
			}
		}
	}
}

// Edge creation methods

// createVNetEdges creates edges for VNet → Subnet relationships
func (s *AzureSource) createVNetEdges(vnetNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	subnetNodes, hasSubnets := lookup.byNodeType[core.NodeTypeSubnet]
	if !hasSubnets {
		return edges
	}

	// Build VNet resource_id lookup (case-insensitive)
	vnetByResourceID := make(map[string]*core.DbNode)
	for _, vnetNode := range vnetNodes {
		if resourceID, ok := vnetNode.Properties["resource_id"].(string); ok && resourceID != "" {
			vnetByResourceID[strings.ToLower(resourceID)] = vnetNode
		}
	}

	for _, subnetNode := range subnetNodes {
		// Try to find parent VNet via vnet_id in subnet metadata
		if vnetID, ok := subnetNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			vnetIDLower := strings.ToLower(vnetID)
			if vnetNode, exists := vnetByResourceID[vnetIDLower]; exists {
				edges = append(edges, s.createEdge(subnetNode, vnetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vnet_subnet"}, req))
			} else if vnetNode, exists := lookup.byResourceID[vnetIDLower]; exists {
				edges = append(edges, s.createEdge(subnetNode, vnetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vnet_subnet"}, req))
			}
		}
	}

	return edges
}

// createVMEdges creates edges for VM → VNet/Subnet/NSG
func (s *AzureSource) createVMEdges(vmNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, vmNode := range vmNodes {
		// VM → Subnet
		if subnetID, ok := vmNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.byResourceID[strings.ToLower(subnetID)]; exists {
				edges = append(edges, s.createEdge(vmNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// VM → VNet
		if vnetID, ok := vmNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.byResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(vmNode, vnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vnet"}, req))
			}
		}

		// VM → NSG
		if nsgID, ok := vmNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if nsgNode, exists := lookup.byResourceID[strings.ToLower(nsgID)]; exists {
				edges = append(edges, s.createEdge(nsgNode, vmNode, core.RelationshipProtects,
					map[string]interface{}{"connection_type": "nsg"}, req))
			}
		}

		// VM → NIC
		if nicIDs, ok := vmNode.Properties["network_interface_ids"].([]interface{}); ok {
			for _, nicIDRaw := range nicIDs {
				if nicID, ok := nicIDRaw.(string); ok && nicID != "" {
					if nicNode, exists := lookup.byResourceID[strings.ToLower(nicID)]; exists {
						edges = append(edges, s.createEdge(vmNode, nicNode, core.RelationshipAssociatedWith,
							map[string]interface{}{"connection_type": "network_interface"}, req))
					}
				}
			}
		}
	}

	return edges
}

// createAKSEdges creates edges for AKS → VNet/Subnet
func (s *AzureSource) createAKSEdges(aksNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, aksNode := range aksNodes {
		// AKS → Subnet
		if subnetID, ok := aksNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.byResourceID[strings.ToLower(subnetID)]; exists {
				edges = append(edges, s.createEdge(aksNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// AKS → VNet
		if vnetID, ok := aksNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.byResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(aksNode, vnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vnet"}, req))
			}
		}
	}

	return edges
}

// createDefaultVNetEdges creates basic VNet relationship for resources with vnet_id
func (s *AzureSource) createDefaultVNetEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		if vnetID, ok := node.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.byResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(node, vnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vnet"}, req))
			}
		}
	}

	return edges
}

// createSubnetEdges creates edges for Subnet → VNet and Subnet → NSG relationships
func (s *AzureSource) createSubnetEdges(subnetNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, subnetNode := range subnetNodes {
		// Subnet → VNet (belongs_to)
		if vnetID, ok := subnetNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.byResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(subnetNode, vnetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vnet_subnet"}, req))
			}
		}

		// Subnet → NSG (protects)
		if nsgID, ok := subnetNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if nsgNode, exists := lookup.byResourceID[strings.ToLower(nsgID)]; exists {
				edges = append(edges, s.createEdge(nsgNode, subnetNode, core.RelationshipProtects,
					map[string]interface{}{"connection_type": "nsg"}, req))
			}
		}
	}

	return edges
}

// createNICEdges creates edges for NIC → VM, NIC → Subnet, NIC → NSG relationships
func (s *AzureSource) createNICEdges(nicNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build VM lookup by resource_id (case-insensitive)
	vmByID := make(map[string]*core.DbNode)
	if vmNodes, ok := lookup.byNodeType[core.NodeTypeComputeInstance]; ok {
		for _, vm := range vmNodes {
			if rid, ok := vm.Properties["resource_id"].(string); ok && rid != "" {
				vmByID[strings.ToLower(rid)] = vm
			}
			if arn, ok := vm.Properties["arn"].(string); ok && arn != "" {
				vmByID[strings.ToLower(arn)] = vm
			}
		}
	}

	for _, nicNode := range nicNodes {
		// NIC → Subnet (hosted_on)
		if subnetID, ok := nicNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.byResourceID[strings.ToLower(subnetID)]; exists {
				edges = append(edges, s.createEdge(nicNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// NIC → NSG (protects)
		if nsgID, ok := nicNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if nsgNode, exists := lookup.byResourceID[strings.ToLower(nsgID)]; exists {
				edges = append(edges, s.createEdge(nsgNode, nicNode, core.RelationshipProtects,
					map[string]interface{}{"connection_type": "nsg"}, req))
			}
		}

		// VM → NIC (associated_with)
		if vmID, ok := nicNode.Properties["attached_vm_id"].(string); ok && vmID != "" {
			if vmNode, exists := vmByID[strings.ToLower(vmID)]; exists {
				edges = append(edges, s.createEdge(vmNode, nicNode, core.RelationshipAssociatedWith,
					map[string]interface{}{"connection_type": "network_interface"}, req))
			}
		}
	}

	return edges
}

// createEdge is a helper to create an edge with Azure source
func (s *AzureSource) createEdge(sourceNode, targetNode *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	return core.NewEdge(
		sourceNode.ID,
		targetNode.ID,
		relType,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"azure",
	)
}

// ========================================================================
// Ensure Infrastructure Nodes - Create missing nodes before edge creation
// ========================================================================

// ensureSubnetNodes extracts subnets embedded in VNet metadata and creates synthetic subnet nodes.
// Azure Resource Graph stores subnets inside VNet's properties.subnets[], not as separate resources.
func (s *AzureSource) ensureSubnetNodes(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbNode {
	vnetNodes, hasVNets := lookup.byNodeType[core.NodeTypeVPC]
	if !hasVNets {
		return nodes
	}

	for _, vnetNode := range vnetNodes {
		vnetResourceID, _ := vnetNode.Properties["resource_id"].(string)
		vnetName, _ := vnetNode.Properties["name"].(string)
		region, _ := vnetNode.Properties["region"].(string)

		// Parse subnets from the raw metadata stored during node creation.
		// _raw_meta is stored as json.RawMessage; accept either that or []byte.
		var metaJSON []byte
		switch v := vnetNode.Properties["_raw_meta"].(type) {
		case json.RawMessage:
			metaJSON = []byte(v)
		case []byte:
			metaJSON = v
		}
		if len(metaJSON) == 0 {
			continue
		}

		var metaMap map[string]interface{}
		if err := json.Unmarshal(metaJSON, &metaMap); err != nil {
			continue
		}

		propsMap, _ := metaMap["properties"].(map[string]interface{})
		if propsMap == nil {
			propsMap = metaMap
		}

		subnets, ok := propsMap["subnets"].([]interface{})
		if !ok {
			continue
		}

		for _, subnetRaw := range subnets {
			subnetMap, ok := subnetRaw.(map[string]interface{})
			if !ok {
				continue
			}

			subnetID, _ := subnetMap["id"].(string)
			subnetName, _ := subnetMap["name"].(string)
			if subnetID == "" {
				continue
			}

			// Skip if already exists
			subnetIDLower := strings.ToLower(subnetID)
			if _, exists := lookup.byResourceID[subnetIDLower]; exists {
				continue
			}

			// Extract CIDR from subnet properties
			cidr := ""
			if subProps, ok := subnetMap["properties"].(map[string]interface{}); ok {
				if prefix, ok := subProps["addressPrefix"].(string); ok {
					cidr = prefix
				} else if prefixes, ok := subProps["addressPrefixes"].([]interface{}); ok && len(prefixes) > 0 {
					if p, ok := prefixes[0].(string); ok {
						cidr = p
					}
				}
			}

			// Extract NSG ID from subnet properties
			nsgID := ""
			if subProps, ok := subnetMap["properties"].(map[string]interface{}); ok {
				if nsgMap, ok := subProps["networkSecurityGroup"].(map[string]interface{}); ok {
					if id, ok := nsgMap["id"].(string); ok {
						nsgID = strings.ToLower(id)
					}
				}
			}

			properties := map[string]interface{}{
				"name":           subnetName,
				"resource_id":    subnetIDLower,
				"subnet_id":      subnetIDLower,
				"vnet_id":        strings.ToLower(vnetResourceID),
				"vnet_name":      vnetName,
				"region":         region,
				"cloud_provider": "Azure",
				"service_name":   "microsoft.network/virtualnetworks/subnets",
				"type":           "subnets",
				"subtype":        "subnets",
				"inferred":       true,
			}
			if cidr != "" {
				properties["cidr_block"] = cidr
			}
			if nsgID != "" {
				properties["nsg_id"] = nsgID
			}

			tempNode := &core.DbNode{
				NodeType:       core.NodeTypeSubnet,
				Properties:     properties,
				CloudAccountID: req.CloudAccountID,
			}
			uniqueKey := s.GenerateUniqueKey(tempNode)

			subnetNode := core.NewNode(core.NodeTypeSubnet, uniqueKey, properties, req.TenantID, req.CloudAccountID, "azure")
			nodes = append(nodes, subnetNode)
			lookup.byResourceID[subnetIDLower] = subnetNode
			lookup.byNodeType[core.NodeTypeSubnet] = append(lookup.byNodeType[core.NodeTypeSubnet], subnetNode)

			s.logger.Debug("created inferred subnet node from VNet metadata",
				"subnet_name", subnetName,
				"subnet_id", subnetIDLower,
				"vnet_name", vnetName)
		}
	}

	s.logger.Info("ensureSubnetNodes completed",
		"total_subnets", len(lookup.byNodeType[core.NodeTypeSubnet]))

	return nodes
}

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

// ensureNICNodes fetches network interfaces via Azure CLI and creates NIC nodes.
// NICs are critical for resolving VM → Subnet → VNet relationships in Azure.
func (s *AzureSource) ensureNICNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbNode {
	if reqCtx == nil || req.CloudAccountID == "" {
		s.logger.Warn("skipping NIC fetch: missing reqCtx or cloud account ID")
		return nodes
	}

	nics, err := s.fetchNICsFromAzure(reqCtx, req)
	if err != nil {
		s.logger.Warn("failed to fetch NICs from Azure CLI, edges may be incomplete",
			"error", err)
		return nodes
	}

	for _, nic := range nics {
		nicIDLower := strings.ToLower(nic.ID)

		// Build the network-relationship properties from the CLI response
		netProps := map[string]interface{}{}
		if len(nic.IPConfigurations) > 0 {
			ipConfig := nic.IPConfigurations[0]
			if ipConfig.PrivateIPAddress != "" {
				netProps["private_ip"] = ipConfig.PrivateIPAddress
			}
			if ipConfig.Subnet.ID != "" {
				subnetID := strings.ToLower(ipConfig.Subnet.ID)
				netProps["subnet_id"] = subnetID
				if vnetID := extractVNetIDFromSubnetID(subnetID); vnetID != "" {
					netProps["vnet_id"] = vnetID
				}
			}
			if ipConfig.PublicIPAddress != nil && ipConfig.PublicIPAddress.ID != "" {
				netProps["public_ip_id"] = strings.ToLower(ipConfig.PublicIPAddress.ID)
			}
		}
		if nic.NetworkSecurityGroup != nil && nic.NetworkSecurityGroup.ID != "" {
			netProps["nsg_id"] = strings.ToLower(nic.NetworkSecurityGroup.ID)
		}
		if nic.VirtualMachine != nil && nic.VirtualMachine.ID != "" {
			netProps["attached_vm_id"] = strings.ToLower(nic.VirtualMachine.ID)
		}

		// If a NIC with this resource_id is already in lookup (from cloud_resourses),
		// enrich it with the CLI-derived properties so VM<->NIC<->Subnet edges can be created.
		if existing, exists := lookup.byResourceID[nicIDLower]; exists {
			if existing.Properties == nil {
				existing.Properties = make(map[string]interface{})
			}
			for k, v := range netProps {
				if _, already := existing.Properties[k]; !already {
					existing.Properties[k] = v
				}
			}
			continue
		}

		properties := map[string]interface{}{
			"name":           nic.Name,
			"resource_id":    nicIDLower,
			"region":         nic.Location,
			"resource_group": nic.ResourceGroup,
			"cloud_provider": "Azure",
			"service_name":   "microsoft.network/networkinterfaces",
			"type":           "networkinterfaces",
			"subtype":        "networkinterfaces",
			"inferred":       true,
		}
		for k, v := range netProps {
			properties[k] = v
		}

		tempNode := &core.DbNode{
			NodeType:       core.NodeTypeNetworkInterface,
			Properties:     properties,
			CloudAccountID: req.CloudAccountID,
		}
		uniqueKey := s.GenerateUniqueKey(tempNode)

		nicNode := core.NewNode(core.NodeTypeNetworkInterface, uniqueKey, properties, req.TenantID, req.CloudAccountID, "azure")
		nodes = append(nodes, nicNode)
		lookup.byResourceID[nicIDLower] = nicNode
		lookup.byNodeType[core.NodeTypeNetworkInterface] = append(lookup.byNodeType[core.NodeTypeNetworkInterface], nicNode)
	}

	s.logger.Info("ensureNICNodes completed",
		"nics_fetched", len(nics),
		"total_nic_nodes", len(lookup.byNodeType[core.NodeTypeNetworkInterface]))

	return nodes
}

// AzureNSGData represents a network security group from Azure CLI response
type AzureNSGData struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Location      string `json:"location"`
	ResourceGroup string `json:"resourceGroup"`
}

// ensureNSGNodes fetches network security groups via Azure CLI and creates NSG nodes.
func (s *AzureSource) ensureNSGNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbNode {
	if reqCtx == nil || req.CloudAccountID == "" {
		return nodes
	}

	nsgs, err := s.fetchNSGsFromAzure(reqCtx, req)
	if err != nil {
		s.logger.Warn("failed to fetch NSGs from Azure CLI",
			"error", err)
		return nodes
	}

	for _, nsg := range nsgs {
		nsgIDLower := strings.ToLower(nsg.ID)
		if _, exists := lookup.byResourceID[nsgIDLower]; exists {
			continue
		}

		properties := map[string]interface{}{
			"name":           nsg.Name,
			"resource_id":    nsgIDLower,
			"region":         nsg.Location,
			"resource_group": nsg.ResourceGroup,
			"cloud_provider": "Azure",
			"service_name":   "microsoft.network/networksecuritygroups",
			"type":           "networksecuritygroups",
			"subtype":        "networksecuritygroups",
			"inferred":       true,
		}

		tempNode := &core.DbNode{
			NodeType:       core.NodeTypeSecurityGroup,
			Properties:     properties,
			CloudAccountID: req.CloudAccountID,
		}
		uniqueKey := s.GenerateUniqueKey(tempNode)

		nsgNode := core.NewNode(core.NodeTypeSecurityGroup, uniqueKey, properties, req.TenantID, req.CloudAccountID, "azure")
		nodes = append(nodes, nsgNode)
		lookup.byResourceID[nsgIDLower] = nsgNode
		lookup.byNodeType[core.NodeTypeSecurityGroup] = append(lookup.byNodeType[core.NodeTypeSecurityGroup], nsgNode)
	}

	s.logger.Info("ensureNSGNodes completed",
		"nsgs_fetched", len(nsgs),
		"total_nsg_nodes", len(lookup.byNodeType[core.NodeTypeSecurityGroup]))

	return nodes
}

// resolveVMNetworkRelationships uses NIC data to set vnet_id, subnet_id, nsg_id on VMs.
// In Azure, the chain is: VM → NIC → IP Config → Subnet → VNet.
// This method back-propagates network info from NICs to their attached VMs.
func (s *AzureSource) resolveVMNetworkRelationships(nodes []*core.DbNode, lookup *NodeLookup) {
	nicNodes, hasNICs := lookup.byNodeType[core.NodeTypeNetworkInterface]
	if !hasNICs {
		return
	}

	// Build VM resource_id → node map (case-insensitive)
	vmByResourceID := make(map[string]*core.DbNode)
	if vmNodes, ok := lookup.byNodeType[core.NodeTypeComputeInstance]; ok {
		for _, vm := range vmNodes {
			if rid, ok := vm.Properties["resource_id"].(string); ok && rid != "" {
				vmByResourceID[strings.ToLower(rid)] = vm
			}
			// Also index by arn (Azure resource ID stored in arn column)
			if arn, ok := vm.Properties["arn"].(string); ok && arn != "" {
				vmByResourceID[strings.ToLower(arn)] = vm
			}
		}
	}

	resolved := 0
	for _, nicNode := range nicNodes {
		attachedVMID, _ := nicNode.Properties["attached_vm_id"].(string)
		if attachedVMID == "" {
			continue
		}

		vmNode, exists := vmByResourceID[strings.ToLower(attachedVMID)]
		if !exists {
			continue
		}

		// Propagate subnet_id from NIC to VM
		if subnetID, ok := nicNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if _, alreadySet := vmNode.Properties["subnet_id"].(string); !alreadySet {
				vmNode.Properties["subnet_id"] = subnetID
			}
		}

		// Propagate vnet_id from NIC to VM
		if vnetID, ok := nicNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if _, alreadySet := vmNode.Properties["vnet_id"].(string); !alreadySet {
				vmNode.Properties["vnet_id"] = vnetID
			}
		}

		// Propagate nsg_id from NIC to VM
		if nsgID, ok := nicNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if _, alreadySet := vmNode.Properties["nsg_id"].(string); !alreadySet {
				vmNode.Properties["nsg_id"] = nsgID
			}
		}

		// Propagate private_ip from NIC to VM
		if privateIP, ok := nicNode.Properties["private_ip"].(string); ok && privateIP != "" {
			if _, alreadySet := vmNode.Properties["private_ip"].(string); !alreadySet {
				vmNode.Properties["private_ip"] = privateIP
			}
		}

		resolved++
	}

	s.logger.Info("resolved VM network relationships via NICs",
		"vms_resolved", resolved)
}

// ========================================================================
// Azure CLI Fetch Methods
// ========================================================================

// fetchNICsFromAzure fetches all network interfaces via Azure CLI using the cloud collector
func (s *AzureSource) fetchNICsFromAzure(reqCtx *security.RequestContext, req *core.SourceBuildRequest) ([]AzureNICData, error) {
	cmd := "az network nic list --output json"

	s.logger.Debug("fetching NICs from Azure",
		"account_id", req.CloudAccountID,
		"command", cmd)

	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: req.CloudAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute Azure CLI command: %w", err)
	}

	output := extractCLIOutput(resp)
	if output == "" {
		return nil, fmt.Errorf("invalid response format from cloud CLI for NIC list")
	}

	var nics []AzureNICData
	if err := json.Unmarshal([]byte(output), &nics); err != nil {
		s.logger.Error("failed to parse NIC JSON", "error", err)
		return nil, fmt.Errorf("failed to parse NIC response: %w", err)
	}

	s.logger.Info("successfully fetched NICs from Azure",
		"count", len(nics))

	return nics, nil
}

// fetchNSGsFromAzure fetches all network security groups via Azure CLI
func (s *AzureSource) fetchNSGsFromAzure(reqCtx *security.RequestContext, req *core.SourceBuildRequest) ([]AzureNSGData, error) {
	cmd := "az network nsg list --output json"

	s.logger.Debug("fetching NSGs from Azure",
		"account_id", req.CloudAccountID,
		"command", cmd)

	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: req.CloudAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute Azure CLI command: %w", err)
	}

	output := extractCLIOutput(resp)
	if output == "" {
		return nil, fmt.Errorf("invalid response format from cloud CLI for NSG list")
	}

	var nsgs []AzureNSGData
	if err := json.Unmarshal([]byte(output), &nsgs); err != nil {
		s.logger.Error("failed to parse NSG JSON", "error", err)
		return nil, fmt.Errorf("failed to parse NSG response: %w", err)
	}

	s.logger.Info("successfully fetched NSGs from Azure",
		"count", len(nsgs))

	return nsgs, nil
}

// extractCLIOutput extracts the JSON string from cloud collector CLI response.
// The response may contain the output in "data", "output", or "result" fields.
func extractCLIOutput(resp map[string]any) string {
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		return dataStr
	}
	if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		return outputStr
	}
	if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		return resultStr
	}
	return ""
}

// extractVNetIDFromSubnetID extracts the VNet resource ID from a subnet resource ID.
// Subnet ID format: /subscriptions/.../virtualNetworks/<vnet>/subnets/<subnet>
// VNet ID format:   /subscriptions/.../virtualNetworks/<vnet>
func extractVNetIDFromSubnetID(subnetID string) string {
	lower := strings.ToLower(subnetID)
	idx := strings.Index(lower, "/subnets/")
	if idx > 0 {
		return subnetID[:idx]
	}
	return ""
}
