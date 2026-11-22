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
	// kind of their own these fall through determineNodeType to the coarse
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

// GenerateUniqueKey generates a unique key for an Azure node.
// Format: azure:{account}:{region}:{NodeType}:{resource_group}:{name}
//
// The hierarchy is the resource group because that is where Azure makes a name
// unique, as the project is for GCP and the VPC for AWS; a child resource is
// named by its path below its parent ("sqlsrv1/databases/orders"), because a
// child name is unique only per parent and type (a storage account's blob, file,
// queue and table services are all "default"). Both come from the resource id
// (see azureKeyParts), so the inventory row, the single-resource patch, the
// subnets/NICs/NSGs synthesized from metadata and a declared node all key the
// same resource the same way.
func (s *AzureSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	keyComponents := core.NewUniqueKeyComponents("azure", node.NodeType)

	name, _ := core.GetNodePropertyString(node, "name")
	resourceID, _ := core.GetNodePropertyString(node, "resource_id")
	if resourceID == "" {
		resourceID, _ = core.GetNodePropertyString(node, "arn")
	}
	resourceGroup, keyName := azureKeyParts(resourceID, name)
	if resourceGroup == "" {
		rg, _ := core.GetNodePropertyString(node, "resource_group")
		resourceGroup = strings.ToLower(rg)
	}
	if keyName == "" {
		keyName = resourceID
	}
	if keyName == "" {
		keyName, _ = core.GetNodePropertyString(node, "id")
	}
	keyComponents.Name = keyName
	keyComponents.Account = node.CloudAccountID
	region, _ := core.GetNodePropertyString(node, "region")
	keyComponents.Location = normalizeAzureRegion(region)
	keyComponents.Hierarchy = resourceGroup

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

	// cr.id breaks ties so that, if two resources ever share a key, the same one
	// wins the merge on every build.
	query += " ORDER BY cr.type, cr.name, cr.id"

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

	// Ask each resource for the edges of its own nodes. Every builder is
	// registered by its resource file; see dispatch.go.
	edges = append(edges, s.buildEdges(nodes, lookup, req)...)

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

	if collided := markKeyCollisions(nodes); collided > 0 {
		s.logger.Warn("Azure resources share a unique key and will merge into one node",
			"collided_keys", collided,
			"cloud_account_id", req.CloudAccountID)
	}

	return nodes, edges
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
