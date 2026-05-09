package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Validation patterns for AWS CLI argument sanitization
var (
	// CloudFormation stack names: alphanumeric + hyphens, starts with letter, max 128 chars
	validStackNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,127}$`)
	// AWS regions: e.g., us-east-1, eu-west-2, ap-southeast-1
	validAWSRegionRegex = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)
)

// validateStackName validates that a CloudFormation stack name is safe for CLI usage
func validateStackName(stackName string) error {
	if stackName == "" {
		return fmt.Errorf("stack name cannot be empty")
	}
	if !validStackNameRegex.MatchString(stackName) {
		return fmt.Errorf("invalid stack name format: must start with a letter and contain only alphanumeric characters and hyphens (max 128 chars)")
	}
	return nil
}

// validateAWSRegion validates that an AWS region is safe for CLI usage
func validateAWSRegion(region string) error {
	if region == "" {
		return nil // Empty region is allowed (uses default)
	}
	if !validAWSRegionRegex.MatchString(region) {
		return fmt.Errorf("invalid AWS region format: %s", region)
	}
	return nil
}

func init() {
	// Register AWS source factory with the global registry
	RegisterSourceFactory("aws", func(config SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewAWSSource(AWSSourceConfig{ServiceTypeFilter: DefaultServiceTypeFilter}, logger)
	}, "AWS cloud resources source (RDS, ElastiCache, S3, EC2, etc.)")

	// Cache CloudFormation stack resources for 2 hours (stacks rarely change,
	// and the KG cron runs daily at 23:30 UTC)
	common.CacheCreateNamespace("cfn_stack_resources",
		common.CacheNamespaceWithExpiration(2*time.Hour),
		common.CacheNamespaceWithMaxEntries(500),
	)
}

// AWSSource implements the Source interface for AWS cloud resources
type AWSSource struct {
	BaseSource
	config  AWSSourceConfig
	logger  *slog.Logger
	enabled bool

	// Per-build meta cache populated from already-loaded cloud_resourses rows.
	// Keyed by resource type, then by resource ID. Avoids re-querying the DB
	// for the same data in downstream fetch functions.
	metaByType      map[string][]CloudResourceRow
	metaByTypeAndID map[string]map[string]CloudResourceRow
}

// AWSSourceConfig holds configuration for AWS source
type AWSSourceConfig struct {
	ResourceTypes     []string            // Filter by resource types (e.g., "rds_instance", "elasticache_cluster")
	IncludeInactive   bool                // Include inactive resources (default: false)
	ServiceTypeFilter map[string][]string // Filter by service name -> allowed types. Use DefaultServiceTypeFilter or custom mapping
	// When ServiceTypeFilter is set:
	// - Only resources matching the service+type combinations will be processed
	// - Services not in the map will process all their types (no filtering)
	// - Empty filter map means no filtering (all resources processed)
}

// CloudResourceRow represents a row from the cloud_resourses table
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

// CloudFormationStackResource represents a resource managed by a CloudFormation stack
// Used when fetching stack resources via AWS CLI list-stack-resources
type CloudFormationStackResource struct {
	LogicalResourceId  string `json:"LogicalResourceId"`
	PhysicalResourceId string `json:"PhysicalResourceId"`
	ResourceType       string `json:"ResourceType"`
	ResourceStatus     string `json:"ResourceStatus"`
}

// NewAWSSource creates a new AWS source
func NewAWSSource(config AWSSourceConfig, logger *slog.Logger) (*AWSSource, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// TenantID and CloudAccountID are optional at creation time
	// They will be provided in the SourceBuildRequest when BuildGraph is called

	return &AWSSource{
		BaseSource: NewBaseSource("aws"),
		config:     config,
		logger:     logger,
		enabled:    true,
	}, nil
}

// GetName returns the name of the source
func (s *AWSSource) GetName() string {
	return "aws"
}

// IsEnabled checks if the source is enabled
func (s *AWSSource) IsEnabled() bool {
	return s.enabled
}

// Validate validates the source configuration
func (s *AWSSource) Validate() error {
	// TenantID and CloudAccountID are not required at source creation time
	// They are provided in the SourceBuildRequest when BuildGraph is called
	return nil
}

// GenerateUniqueKey generates a unique key for an AWS node
// Overrides BaseSource.GenerateUniqueKey with AWS-specific logic
// Format: aws:{account}:{region}:{NodeType}:{vpc_id}:{name}
func (s *AWSSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	// Create key components
	keyComponents := core.NewUniqueKeyComponents("aws", node.NodeType)

	// Extract name
	name, _ := core.GetNodePropertyString(node, "name")
	if name == "" {
		// Try resource_id as fallback
		name, _ = core.GetNodePropertyString(node, "resource_id")
	}
	if name == "" {
		// Try id as last fallback
		name, _ = core.GetNodePropertyString(node, "id")
	}
	keyComponents.Name = name
	keyComponents.Account = node.CloudAccountID

	// Extract region (location)
	region, _ := core.GetNodePropertyString(node, "region")
	if region != "" {
		keyComponents.Location = region
	}

	// Extract hierarchy (VPC name for network resources, or resource group)
	// For VPC nodes themselves, leave hierarchy empty
	switch node.NodeType {
	case core.NodeTypeVPC:
		keyComponents.Hierarchy = ""
	case core.NodeTypeManagedCluster, core.NodeTypeWorkload:
		// For ManagedCluster (EKS/ECS clusters) and Workload (ECS services), use service_name
		// to differentiate between different cloud services with the same name
		// e.g., EKS cluster "my-cluster" vs ECS cluster "my-cluster"
		serviceName, _ := core.GetNodePropertyString(node, "service_name")
		if serviceName != "" {
			// Use short service identifier: AmazonEKS -> EKS, AmazonECS -> ECS
			switch serviceName {
			case "AmazonEKS":
				keyComponents.Hierarchy = "EKS"
			case "AmazonECS":
				keyComponents.Hierarchy = "ECS"
			default:
				// Use service name directly for other services
				keyComponents.Hierarchy = serviceName
			}
		}
	default:
		// For resources in a VPC, try to use VPC name first
		vpcNameHierarchy, _ := core.GetNodePropertyString(node, "vpc_name_hierarchy")
		if vpcNameHierarchy != "" {
			// Use the VPC name from the propagated property
			keyComponents.Hierarchy = vpcNameHierarchy
		} else {
			// Fallback to vpc_id for backwards compatibility or if propagation hasn't happened yet
			vpcID, _ := core.GetNodePropertyString(node, "vpc_id")
			if vpcID != "" {
				// For resources in a VPC, use VPC ID as hierarchy (will be updated later)
				keyComponents.Hierarchy = vpcID
			} else {
				// For global services (S3, IAM, etc.), leave hierarchy blank
				// For other resources, try to extract from metadata
				if metaVPC, ok := node.Properties["vpc"]; ok {
					if vpcStr, ok := metaVPC.(string); ok && vpcStr != "" {
						keyComponents.Hierarchy = vpcStr
					}
				}
			}
		}
	}

	// Validate and build
	if err := keyComponents.Validate(); err != nil {
		// Fallback to base implementation
		return s.BaseSource.GenerateUniqueKey(node)
	}

	return keyComponents.Build()
}

// BuildGraph builds a knowledge graph from AWS resources
func (s *AWSSource) BuildGraph(reqCtx *security.RequestContext, req *core.SourceBuildRequest) (*core.Graph, error) {
	ctx := reqCtx.GetContext()
	s.logger.Info("building knowledge graph from AWS resources",
		"tenant_id", req.TenantID,
		"cloud_account_id", req.CloudAccountID,
		"service_type_filter_enabled", len(s.config.ServiceTypeFilter) > 0)

	startTime := time.Now()

	// Fetch AWS resources from database
	resources, err := s.fetchAWSResources(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch AWS resources: %w", err)
	}

	s.logger.Info("fetched AWS resources", "count", len(resources))

	// Build in-memory meta cache so downstream fetch functions can read
	// already-loaded rows instead of making redundant DB or CLI calls.
	s.metaByType = make(map[string][]CloudResourceRow)
	s.metaByTypeAndID = make(map[string]map[string]CloudResourceRow)
	for _, row := range resources {
		s.metaByType[row.Type] = append(s.metaByType[row.Type], row)
		if _, ok := s.metaByTypeAndID[row.Type]; !ok {
			s.metaByTypeAndID[row.Type] = make(map[string]CloudResourceRow)
		}
		s.metaByTypeAndID[row.Type][row.ResourceID] = row
	}

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

	s.logger.Info("successfully built knowledge graph from AWS resources",
		"nodes", len(nodes),
		"edges", len(edges),
		"duration", time.Since(startTime).Seconds())

	return graph, nil
}

// fetchAWSResources queries AWS resources from the cloud_resourses table
func (s *AWSSource) fetchAWSResources(ctx context.Context, req *core.SourceBuildRequest) ([]CloudResourceRow, error) {
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// Build query
	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			ca.account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.cloud_provider = 'AWS'
	`

	args := []interface{}{req.TenantID}
	argIndex := 2

	// Filter by cloud account if specified
	if req.CloudAccountID != "" {
		query += fmt.Sprintf(" AND cr.account = $%d", argIndex)
		args = append(args, req.CloudAccountID)
		argIndex++
	}

	// Filter by region if specified
	if req.Region != "" {
		query += fmt.Sprintf(" AND cr.region = $%d", argIndex)
		args = append(args, req.Region)
		argIndex++
	}

	// Filter by resource types if specified
	if len(s.config.ResourceTypes) > 0 {
		query += fmt.Sprintf(" AND cr.type = ANY($%d)", argIndex)
		args = append(args, pq.Array(s.config.ResourceTypes))
	}

	// Filter by active status
	if !s.config.IncludeInactive {
		query += " AND cr.is_active = true"
	}

	query += " ORDER BY cr.type, cr.name"

	var resources []CloudResourceRow
	err = dbManager.Db.Select(&resources, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud_resourses: %w", err)
	}

	s.logger.Info("queried cloud resources from database",
		"count", len(resources),
		"tenant_id", req.TenantID)

	return resources, nil
}

// NodeLookup provides efficient lookup of nodes by various identifiers
type NodeLookup struct {
	byResourceID map[string]*core.DbNode // resource_id -> node
	byNodeType   map[core.NodeType][]*core.DbNode
	byARN        map[string]*core.DbNode // arn -> node
}

// newNodeLookup creates lookup maps from a list of nodes
func newNodeLookup(nodes []*core.DbNode) *NodeLookup {
	lookup := &NodeLookup{
		byResourceID: make(map[string]*core.DbNode),
		byNodeType:   make(map[core.NodeType][]*core.DbNode),
		byARN:        make(map[string]*core.DbNode),
	}

	for _, node := range nodes {
		// Index by resource_id
		if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID != "" {
			lookup.byResourceID[resourceID] = node
		}

		// Index by node type
		lookup.byNodeType[node.NodeType] = append(lookup.byNodeType[node.NodeType], node)

		// Index by ARN
		if arn, ok := node.Properties["arn"].(string); ok && arn != "" {
			lookup.byARN[arn] = node
		}
	}

	return lookup
}

// shouldIncludeResource checks if a resource should be included based on ServiceTypeFilter
func (s *AWSSource) shouldIncludeResource(resource *CloudResourceRow) bool {
	// If no filter is configured, include all resources
	if len(s.config.ServiceTypeFilter) == 0 {
		return true
	}

	// Check if this service has a type filter
	allowedTypes, serviceHasFilter := s.config.ServiceTypeFilter[resource.ServiceName]
	if !serviceHasFilter {
		// If service is not in the filter map, include the resource (filter only applies to specified services)
		return true
	}

	// Check if the resource type is in the allowed types for this service
	resourceTypeLower := strings.ToLower(resource.Type)
	for _, allowedType := range allowedTypes {
		if strings.ToLower(allowedType) == resourceTypeLower {
			return true
		}
	}

	// Resource type not in allowed list for this service
	return false
}

// convertResourcesToGraph converts AWS resources to knowledge graph nodes and edges
func (s *AWSSource) convertResourcesToGraph(reqCtx *security.RequestContext, resources []CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Step 1: Create all nodes (with service-type filtering)
	nodes := make([]*core.DbNode, 0, len(resources))
	for _, resource := range resources {
		// Apply service-type filter
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

	// Step 2: Build lookup maps for efficient edge creation
	lookup := newNodeLookup(nodes)

	// Step 2.5: Ensure all infrastructure nodes exist BEFORE creating edges
	// This allows edge creation to find VPC and Subnet nodes in the lookup
	nodes, _ = s.ensureVPCNodes(reqCtx, nodes, []*core.DbEdge{}, lookup, req)
	nodes, _ = s.ensureSubnetNodes(nodes, []*core.DbEdge{}, lookup, req)
	nodes, _ = s.ensureRouteTableNodes(reqCtx, nodes, []*core.DbEdge{}, lookup, req)

	// Step 2.6: Propagate VPC names to all resources and update their unique keys
	// This ensures resources use VPC name in their hierarchy instead of VPC ID
	s.propagateVPCNamesToResources(nodes, lookup)

	// Step 3: Create edges by processing each node type
	edges := make([]*core.DbEdge, 0)

	// Process each node type to create its relationships
	for nodeType, nodeList := range lookup.byNodeType {
		switch nodeType {
		case core.NodeTypeVPC:
			// VPC nodes don't have outgoing edges to other resources
			continue

		case core.NodeTypeMessageQueue:
			// SQS queues can have Dead Letter Queue relationships and receive from Lambda event sources
			edges = append(edges, s.createSQSEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypeTopic:
			// SNS topics can publish to SQS queues, Lambda functions, and other endpoints via subscriptions
			edges = append(edges, s.createSNSEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypeStorage:
			// S3 buckets can send event notifications to SNS, SQS, and Lambda
			edges = append(edges, s.createS3Edges(nodeList, lookup, req)...)
			// EBS volumes can be attached to EC2 instances
			edges = append(edges, s.createEBSEdges(nodeList, lookup, req)...)
			// EFS file systems connect to VPC, subnets, and ENIs via mount targets
			edges = append(edges, s.createEFSEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypeComputeInstance:
			edges = append(edges, s.createEC2Edges(nodeList, lookup, req)...)

		case core.NodeTypeDatabase:
			// Filter database nodes - DynamoDB doesn't have VPC/subnet relationships
			rdsNodes := make([]*core.DbNode, 0)
			for _, node := range nodeList {
				if serviceName, ok := node.Properties["service_name"].(string); ok {
					if serviceName != "AmazonDynamoDB" {
						rdsNodes = append(rdsNodes, node)
					}
				}
			}
			if len(rdsNodes) > 0 {
				edges = append(edges, s.createRDSEdges(rdsNodes, lookup, req)...)
			}

		case core.NodeTypeCache:
			edges = append(edges, s.createElastiCacheEdges(nodeList, lookup, req)...)

		case core.NodeTypeLoadBalancer:
			// createLoadBalancerEdges builds edges from DB metadata and returns all LB nodes.
			// The re-add below is a slice rebuild (no filtering occurs).
			validLBNodes, backendPoolNodes, lbEdges := s.createLoadBalancerEdges(reqCtx, nodeList, lookup, req)

			// Rebuild the nodes slice, replacing the LB section with the returned set.
			filteredNodes := make([]*core.DbNode, 0, len(nodes))
			for _, node := range nodes {
				if node.NodeType != core.NodeTypeLoadBalancer {
					filteredNodes = append(filteredNodes, node)
				}
			}
			// Re-add all LB nodes (same set returned from createLoadBalancerEdges).
			nodes = append(filteredNodes, validLBNodes...)

			// Update lookup with valid LB nodes
			lookup.byNodeType[core.NodeTypeLoadBalancer] = validLBNodes
			for _, validNode := range validLBNodes {
				if resourceID, ok := validNode.Properties["resource_id"].(string); ok && resourceID != "" {
					lookup.byResourceID[resourceID] = validNode
				}
				if arn, ok := validNode.Properties["arn"].(string); ok && arn != "" {
					lookup.byARN[arn] = validNode
				}
			}

			// Add backend pool nodes and edges
			nodes = append(nodes, backendPoolNodes...)
			edges = append(edges, lbEdges...)

		case core.NodeTypeServerlessFunction:
			edges = append(edges, s.createLambdaEdges(nodeList, lookup, req)...)

		case core.NodeTypeManagedCluster:
			edges = append(edges, s.createEKSEdges(nodeList, lookup, req)...)

		case core.NodeTypeWorkload:
			// Handle ECS Services connecting to ECS Clusters
			edges = append(edges, s.createECSServiceEdges(nodeList, lookup, req)...)

		case core.NodeTypeSecurityGroup:
			edges = append(edges, s.createSecurityGroupEdges(nodeList, lookup, req)...)

		case core.NodeTypeNetworkGateway:
			edges = append(edges, s.createNATGatewayEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypePrivateEndpoint:
			edges = append(edges, s.createPrivateEndpointEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypeNetworkInterface:
			// Handle ENI (network-interface) nodes
			// Note: createENIEdges returns only valid ENI nodes (those present in AWS CLI)
			// DB-only ENIs (not in CLI) are filtered out as they may be stale/inactive
			validENINodes, eniEdges := s.createENIEdges(reqCtx, nodeList, lookup, req)

			// Remove old ENI nodes from the nodes slice (they may include stale DB-only nodes)
			filteredNodes := make([]*core.DbNode, 0, len(nodes))
			for _, node := range nodes {
				if node.NodeType != core.NodeTypeNetworkInterface {
					filteredNodes = append(filteredNodes, node)
				}
			}
			// Add only valid ENI nodes (those present in CLI)
			nodes = append(filteredNodes, validENINodes...)

			// Update lookup with valid ENI nodes
			// First, clear the old ENI entries from lookup
			lookup.byNodeType[core.NodeTypeNetworkInterface] = validENINodes
			for _, validNode := range validENINodes {
				if resourceID, ok := validNode.Properties["resource_id"].(string); ok && resourceID != "" {
					lookup.byResourceID[resourceID] = validNode
				}
			}
			edges = append(edges, eniEdges...)

		case core.NodeTypeLogAggregator:
			// Handle CloudWatch resources, including VPC Flow Logs
			edges = append(edges, s.createCloudWatchEdges(reqCtx, nodeList, lookup, req)...)
			// Handle CloudTrail resources (Trails and Event Data Stores)
			edges = append(edges, s.createCloudTrailEdges(nodeList, lookup, req)...)

		case core.NodeTypeBackupVault:
			// AWS Backup Vault → KMS encryption key relationship
			edges = append(edges, s.createBackupVaultEdges(nodeList, lookup, req)...)

		case core.NodeTypeBackupPolicy:
			// AWS Backup Plan → Backup Vault relationship
			edges = append(edges, s.createBackupPolicyEdges(nodeList, lookup, req)...)

		case core.NodeTypePublicIP:
			// Elastic IP → EC2 Instance and ENI relationships
			edges = append(edges, s.createPublicIPEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypeRouteTable:
			// Route Table → VPC, Subnet, NAT Gateway, VPC Endpoint relationships
			edges = append(edges, s.createRouteTableEdges(nodeList, lookup, req)...)

		case core.NodeTypeAPIGateway:
			// API Gateway → Lambda, VPC Endpoint relationships
			edges = append(edges, s.createAPIGatewayEdges(nodeList, lookup, req)...)

		case core.NodeTypeInfraStack:
			// CloudFormation stacks manage their created resources
			edges = append(edges, s.createCloudFormationEdges(reqCtx, nodeList, lookup, req)...)

		case core.NodeTypeEmailService:
			// SES resources can publish to SNS topics for notifications
			edges = append(edges, s.createSESEdges(nodeList, lookup, req)...)

		case core.NodeTypeSecurityService:
			// SecurityHub standards belong to the hub
			edges = append(edges, s.createSecurityHubEdges(nodeList, lookup, req)...)

		case core.NodeTypeAIService:
			// Bedrock resources - no edges needed (catalog entries), just VPC if available
			edges = append(edges, s.createDefaultVPCEdges(nodeList, lookup, req)...)

		default:
			// For other node types, create basic VPC relationship if vpc_id exists
			edges = append(edges, s.createDefaultVPCEdges(nodeList, lookup, req)...)
		}
	}

	// Handle KMS key relationships across all resource types
	edges = append(edges, s.createKMSEdges(lookup, req)...)

	// Fetch and ingest IAM Roles as ServiceIdentity nodes, then create RUNS_AS / ASSUMES edges.
	// Guard against nil reqCtx (consistent with other AWS fetch calls in this file).
	if req.CloudAccountID != "" && reqCtx != nil {
		iamRoles, err := s.fetchIAMRolesFromAWS(reqCtx, req, req.CloudAccountID)
		if err != nil {
			s.logger.Warn("Failed to fetch IAM roles; ServiceIdentity nodes will be skipped", "error", err)
		} else {
			serviceIdentityNodes := s.buildServiceIdentityNodes(iamRoles, req)
			nodes = append(nodes, serviceIdentityNodes...)
			// Register ServiceIdentity nodes in the lookup so edge builders can cross-reference them
			for _, n := range serviceIdentityNodes {
				lookup.byNodeType[core.NodeTypeServiceIdentity] = append(lookup.byNodeType[core.NodeTypeServiceIdentity], n)
				if arn, ok := n.Properties["arn"].(string); ok && arn != "" {
					lookup.byARN[arn] = n
				}
			}
			// Fetch instance profile → role ARN map so EC2 RUNS_AS edges are resolved correctly
			// even when the profile name differs from the role name.
			profileRoleMap, err := s.fetchInstanceProfileRoleMapFromAWS(reqCtx, req.CloudAccountID)
			if err != nil {
				s.logger.Warn("Failed to fetch instance profiles; EC2→ServiceIdentity edges may be incomplete", "error", err)
				profileRoleMap = nil
			}
			edges = append(edges, s.buildServiceIdentityEdges(serviceIdentityNodes, lookup, profileRoleMap, req)...)
		}
	}

	return nodes, edges
}

// createNodeFromResource creates a knowledge graph node from a cloud resource
func (s *AWSSource) createNodeFromResource(resource *CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	// Determine node type
	source := "aws"
	nodeType := s.determineNodeType(resource.Type, resource.ServiceName)

	// Build properties first (needed for unique key generation)
	properties := make(map[string]interface{})
	properties["name"] = resource.Name
	properties["type"] = resource.Type
	properties["status"] = resource.Status
	properties["cloud_provider"] = resource.CloudProvider
	properties["region"] = resource.Region
	properties["labels"] = resource.Tags

	// Set ARN with validation (do not fall back to external_resource_id as it often contains malformed ARNs)
	arn := resource.ARN

	// Validate ARN format before using it (prevent malformed ARNs from bad data)
	// ARN format: arn:partition:service:region:account-id:resource-type/resource-id
	// SNS ARN format: arn:aws:sns:region:account-id:topic-name (exactly 6 parts)
	if arn != "" {
		arnParts := strings.Split(arn, ":")
		// Basic validation: ARN should have at least 6 parts
		if len(arnParts) < 6 {
			s.logger.Warn("Invalid ARN format (too few parts), skipping ARN",
				"resource_name", resource.Name,
				"arn", arn,
				"parts", len(arnParts))
			arn = "" // Clear invalid ARN
		} else if resource.ServiceName == "AmazonSNS" && len(arnParts) != 6 {
			// SNS ARNs must have exactly 6 parts
			s.logger.Warn("Invalid SNS ARN format (expected 6 parts), skipping ARN",
				"resource_name", resource.Name,
				"arn", arn,
				"parts", len(arnParts))
			arn = "" // Clear invalid SNS ARN
		}
	}

	properties["arn"] = arn
	properties["resource_id"] = resource.ResourceID
	properties["service_name"] = resource.ServiceName
	properties["is_active"] = resource.IsActive
	properties["external_resource_id"] = resource.ExternalResourceID

	if resource.Type == "managedinstance" {
		properties["managed"] = true
	}

	// Add subtype for MessageQueue nodes based on service name
	if nodeType == core.NodeTypeMessageQueue {
		switch resource.ServiceName {
		case "AWSQueueService":
			properties["subtype"] = "SQS"
		case "AmazonMSK":
			properties["subtype"] = "MSK"
		default:
			properties["subtype"] = "MessageQueue"
		}
	}

	// Add subtype for Topic nodes based on service name
	if nodeType == core.NodeTypeTopic {
		switch resource.ServiceName {
		case "AmazonSNS":
			properties["subtype"] = "SNS"
		default:
			properties["subtype"] = "Topic"
		}
	}

	// Add subtype for Cache nodes based on service name
	if nodeType == core.NodeTypeCache {
		switch resource.ServiceName {
		case "AmazonElastiCache":
			properties["subtype"] = "ElastiCache"
		default:
			properties["subtype"] = "Cache"
		}
	}

	// Store identifiers
	properties["nb_resource_id"] = resource.ID
	properties["nb_account_id"] = resource.Account
	properties["aws_account_number"] = resource.AccountNumber

	// Parse and extract only essential metadata fields (node-type specific)
	// DO NOT store raw metadata to save space - only extract what's needed
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
			// Extract only relevant fields based on node type
			s.extractEssentialMetadataByNodeType(properties, metaMap, nodeType, resource.ServiceName)
		}
	}

	// Parse and add ALL tags (keep all tags for filtering and organization)
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			properties["labels"] = tagsMap
		}
	}

	// Add cache_type property for ElastiCache resources
	if resource.ServiceName == "AmazonElastiCache" {
		properties["cache_type"] = "elasticache"
	}

	// Add queue_type property for SQS resources
	if resource.ServiceName == "AWSQueueService" {
		properties["queue_type"] = "sqs"
	}

	// Add subtype property for all AWS resources (only if not already set)
	if _, exists := properties["subtype"]; !exists {
		properties["subtype"] = resource.Type
	}

	// Store AWS account number in properties for unique key generation
	properties["account_number"] = resource.AccountNumber

	// Build unique key using new 6-part format: aws:account:region:NodeType:hierarchy:name
	// Create a temporary node to use GenerateUniqueKey
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, source)
}

// extractDNSNameFromMetadata extracts DNS name/endpoint from metadata with comprehensive fallback logic
func (s *AWSSource) extractDNSNameFromMetadata(metaMap map[string]interface{}) string {
	// LoadBalancer: DNSName
	if dnsName, ok := metaMap["DNSName"].(string); ok && dnsName != "" {
		return dnsName
	}

	// RDS Instance: Endpoint.Address
	if endpoint, ok := metaMap["Endpoint"].(map[string]interface{}); ok {
		if address, ok := endpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: ConfigurationEndpoint.Address
	if configEndpoint, ok := metaMap["ConfigurationEndpoint"].(map[string]interface{}); ok {
		if address, ok := configEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: ReaderEndpoint (can be string or object)
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(string); ok && readerEndpoint != "" {
		return readerEndpoint
	}
	if readerEndpoint, ok := metaMap["ReaderEndpoint"].(map[string]interface{}); ok {
		if address, ok := readerEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: PrimaryEndpoint (can be string or object)
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(string); ok && primaryEndpoint != "" {
		return primaryEndpoint
	}
	if primaryEndpoint, ok := metaMap["PrimaryEndpoint"].(map[string]interface{}); ok {
		if address, ok := primaryEndpoint["Address"].(string); ok && address != "" {
			return address
		}
	}

	// ElastiCache: NodeGroups array (for replication groups)
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

	// ElastiCache: CacheNodes array (for single cluster)
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

// extractEssentialMetadataByNodeType extracts only essential metadata fields based on node type
// This prevents storing large metadata blobs and keeps only what's needed for each node type
func (s *AWSSource) extractEssentialMetadataByNodeType(properties map[string]interface{}, metaMap map[string]interface{}, nodeType core.NodeType, serviceName string) {
	// Common fields extracted for all node types
	s.extractCommonMetadataFields(properties, metaMap)

	// Node-type specific extraction
	switch nodeType {
	case core.NodeTypeDatabase:
		s.extractDatabaseMetadata(properties, metaMap)
	case core.NodeTypeCache:
		s.extractCacheMetadata(properties, metaMap)
	case core.NodeTypeComputeInstance:
		s.extractComputeMetadata(properties, metaMap)
	case core.NodeTypeMessageQueue:
		s.extractQueueMetadata(properties, metaMap)
	case core.NodeTypeTopic:
		s.extractTopicMetadata(properties, metaMap)
	case core.NodeTypeStorage:
		s.extractStorageMetadata(properties, metaMap)
		// EFS-specific metadata extraction
		if serviceName == "AmazonEFS" {
			s.extractEFSMetadata(properties, metaMap)
		}
	case core.NodeTypeLoadBalancer:
		s.extractLoadBalancerMetadata(properties, metaMap)
	case core.NodeTypeManagedCluster:
		s.extractClusterMetadata(properties, metaMap)
	case core.NodeTypeServerlessFunction:
		s.extractLambdaMetadata(properties, metaMap)
	case core.NodeTypeCDN:
		s.extractCDNMetadata(properties, metaMap)
	case core.NodeTypeDNSZone:
		s.extractDNSZoneMetadata(properties, metaMap)
	case core.NodeTypeContainerRegistry:
		s.extractContainerRegistryMetadata(properties, metaMap)
	case core.NodeTypeNetworkGateway:
		s.extractNetworkGatewayMetadata(properties, metaMap)
	case core.NodeTypeBackupVault:
		s.extractBackupVaultMetadata(properties, metaMap)
	case core.NodeTypeBackupPolicy:
		s.extractBackupPolicyMetadata(properties, metaMap)
	case core.NodeTypeInfraStack:
		// CloudFormation stack metadata (RoleARN, Parameters, Outputs)
		s.extractCloudFormationMetadata(properties, metaMap)
	case core.NodeTypeLogAggregator:
		// CloudTrail-specific metadata extraction
		if serviceName == "AWSCloudTrail" {
			s.extractCloudTrailMetadata(properties, metaMap)
		}
	case core.NodeTypeCloudResource:
		// Generic cloud resource - add specific extractors as needed
	}
}

// extractCommonMetadataFields extracts fields common to all node types
func (s *AWSSource) extractCommonMetadataFields(properties map[string]interface{}, metaMap map[string]interface{}) {
	// DNS name (important for connectivity)
	dnsName := s.extractDNSNameFromMetadata(metaMap)
	if dnsName != "" {
		properties["dns_name"] = dnsName
	}

	// VPC ID (important for network topology)
	// Note: AWS uses "VPCId" (uppercase) in LoadBalancer API responses
	if vpcID, ok := metaMap["VPCId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}

	// Security Groups (important for security analysis)
	if secGroups, ok := metaMap["SecurityGroups"].([]interface{}); ok && len(secGroups) > 0 {
		properties["security_groups"] = secGroups
	}

	// Availability Zones (important for HA analysis)
	if azs, ok := metaMap["AvailabilityZones"].([]interface{}); ok && len(azs) > 0 {
		properties["availability_zone"] = azs
	}

	// KMS Key ID (important for encryption relationships)
	if kmsKeyId, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyId != "" {
		properties["kms_key_id"] = kmsKeyId
	}

	// Performance Insights KMS Key ID (RDS specific)
	if piKmsKeyId, ok := metaMap["PerformanceInsightsKMSKeyId"].(string); ok && piKmsKeyId != "" {
		properties["performance_insights_kms_key_id"] = piKmsKeyId
	}

	// Encrypted flag (important for encryption analysis)
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// Subnet ID (important for network topology)
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}

	// VpcId (alternative casing for some AWS resources)
	if vpcID, ok := metaMap["VpcId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}
}

// extractDatabaseMetadata extracts essential fields for database nodes (RDS, Aurora)
func (s *AWSSource) extractDatabaseMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Endpoint (critical for connections)
	if endpoint, ok := metaMap["Endpoint"].(map[string]interface{}); ok {
		if address, ok := endpoint["Address"].(string); ok && address != "" {
			properties["endpoint_address"] = address
		}
		if port, ok := endpoint["Port"].(float64); ok {
			properties["endpoint_port"] = int(port)
		}
	}

	// Engine info (important for compatibility)
	if engine, ok := metaMap["Engine"].(string); ok && engine != "" {
		properties["engine"] = engine
	}
	if engineVersion, ok := metaMap["EngineVersion"].(string); ok && engineVersion != "" {
		properties["engine_version"] = engineVersion
	}

	// Instance type (important for capacity planning)
	if instanceType, ok := metaMap["DBInstanceClass"].(string); ok && instanceType != "" {
		properties["instance_type"] = instanceType
	}

	// Multi-AZ (important for HA)
	if multiAZ, ok := metaMap["MultiAZ"].(bool); ok {
		properties["multi_az"] = multiAZ
	}

	// Storage info (important for capacity)
	if allocatedStorage, ok := metaMap["AllocatedStorage"].(float64); ok {
		properties["allocated_storage_gb"] = int(allocatedStorage)
	}

	// VPC ID, subnet IDs, and security group IDs from DBSubnetGroup (needed for edge creation)
	if dbSubnetGroup, ok := metaMap["DBSubnetGroup"].(map[string]interface{}); ok {
		if vpcID, ok := dbSubnetGroup["VpcId"].(string); ok && vpcID != "" {
			properties["vpc_id"] = vpcID
		}
		if subnets, ok := dbSubnetGroup["Subnets"].([]interface{}); ok && len(subnets) > 0 {
			subnetIDs := make([]string, 0, len(subnets))
			for _, s := range subnets {
				if sm, ok := s.(map[string]interface{}); ok {
					if sid, ok := sm["SubnetIdentifier"].(string); ok && sid != "" {
						subnetIDs = append(subnetIDs, sid)
					}
				}
			}
			if len(subnetIDs) > 0 {
				properties["subnet_ids"] = subnetIDs
			}
		}
	}

	// VPC security group IDs (needed for security group edge creation)
	if vpcSGs, ok := metaMap["VpcSecurityGroups"].([]interface{}); ok && len(vpcSGs) > 0 {
		sgIDs := make([]string, 0, len(vpcSGs))
		for _, sg := range vpcSGs {
			if sgMap, ok := sg.(map[string]interface{}); ok {
				if sgID, ok := sgMap["VpcSecurityGroupId"].(string); ok && sgID != "" {
					sgIDs = append(sgIDs, sgID)
				}
			}
		}
		if len(sgIDs) > 0 {
			properties["vpc_security_group_ids"] = sgIDs
		}
	}
}

// extractCacheMetadata extracts essential fields for cache nodes (ElastiCache)
func (s *AWSSource) extractCacheMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Endpoints (critical for connections). AWS sometimes returns these as a
	// bare string and sometimes as {"Address": "...", "Port": ...} — accept both.
	if addr := extractEndpointAddress(metaMap["ConfigurationEndpoint"]); addr != "" {
		properties["configuration_endpoint"] = addr
	}
	if addr := extractEndpointAddress(metaMap["PrimaryEndpoint"]); addr != "" {
		properties["primary_endpoint"] = addr
	}
	if addr := extractEndpointAddress(metaMap["ReaderEndpoint"]); addr != "" {
		properties["reader_endpoint"] = addr
	}

	// Cluster-mode (replication group) per-shard endpoints. Flatten so the
	// cloud-enrichment endpoint index can match by string membership.
	if nodeGroups, ok := metaMap["NodeGroups"].([]interface{}); ok {
		var ngEndpoints []string
		for _, ng := range nodeGroups {
			ngMap, ok := ng.(map[string]interface{})
			if !ok {
				continue
			}
			if addr := extractEndpointAddress(ngMap["PrimaryEndpoint"]); addr != "" {
				ngEndpoints = append(ngEndpoints, addr)
			}
			if addr := extractEndpointAddress(ngMap["ReaderEndpoint"]); addr != "" {
				ngEndpoints = append(ngEndpoints, addr)
			}
		}
		if len(ngEndpoints) > 0 {
			properties["node_group_endpoints"] = ngEndpoints
		}
	}

	// Memcached / non-cluster-mode per-node endpoints.
	if cacheNodes, ok := metaMap["CacheNodes"].([]interface{}); ok {
		var cnEndpoints []string
		for _, cn := range cacheNodes {
			cnMap, ok := cn.(map[string]interface{})
			if !ok {
				continue
			}
			if addr := extractEndpointAddress(cnMap["Endpoint"]); addr != "" {
				cnEndpoints = append(cnEndpoints, addr)
			}
		}
		if len(cnEndpoints) > 0 {
			properties["cache_node_endpoints"] = cnEndpoints
		}
	}

	// Engine info
	if engine, ok := metaMap["Engine"].(string); ok && engine != "" {
		properties["engine"] = engine
	}
	if engineVersion, ok := metaMap["EngineVersion"].(string); ok && engineVersion != "" {
		properties["engine_version"] = engineVersion
	}

	// Instance type
	if nodeType, ok := metaMap["CacheNodeType"].(string); ok && nodeType != "" {
		properties["instance_type"] = nodeType
	}

	// Number of nodes (important for capacity)
	if numNodes, ok := metaMap["NumCacheNodes"].(float64); ok {
		properties["num_cache_nodes"] = int(numNodes)
	}

	// Preferred Availability Zone (for single-AZ clusters, may be "Multiple" for multi-AZ)
	if preferredAZ, ok := metaMap["PreferredAvailabilityZone"].(string); ok && preferredAZ != "" {
		properties["availability_zone"] = preferredAZ
	}
}

// extractComputeMetadata extracts essential fields for EC2 instances
func (s *AWSSource) extractComputeMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Instance type (important for capacity)
	if instanceType, ok := metaMap["InstanceType"].(string); ok && instanceType != "" {
		properties["instance_type"] = instanceType
	}

	// Private/Public IPs (important for connectivity)
	if privateIP, ok := metaMap["PrivateIpAddress"].(string); ok && privateIP != "" {
		properties["private_ip"] = privateIP
	}
	if publicIP, ok := metaMap["PublicIpAddress"].(string); ok && publicIP != "" {
		properties["public_ip"] = publicIP
	}

	// Private DNS Name (important for internal hostname resolution)
	if privateDnsName, ok := metaMap["PrivateDnsName"].(string); ok && privateDnsName != "" {
		properties["private_dns_name"] = privateDnsName
	}

	// State (important for operational status)
	if state, ok := metaMap["State"].(map[string]interface{}); ok {
		if stateName, ok := state["Name"].(string); ok && stateName != "" {
			properties["instance_state"] = stateName
		}
	}

	// Subnet ID (important for network topology)
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}

	// Availability Zone (from Placement - important for data locality and HA)
	if placement, ok := metaMap["Placement"].(map[string]interface{}); ok {
		if az, ok := placement["AvailabilityZone"].(string); ok && az != "" {
			properties["availability_zone"] = az
		}
	}

	// IAM instance profile ARN (needed for ServiceIdentity relationship)
	if instanceProfile, ok := metaMap["IamInstanceProfile"].(map[string]interface{}); ok {
		if profileARN, ok := instanceProfile["Arn"].(string); ok && profileARN != "" {
			properties["iam_instance_profile_arn"] = profileARN
		}
	}
}

// extractQueueMetadata extracts essential fields for SQS queues
func (s *AWSSource) extractQueueMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Queue URL (critical for operations)
	if queueURL, ok := metaMap["QueueUrl"].(string); ok && queueURL != "" {
		properties["queue_url"] = queueURL
	}
	if queueArn, ok := metaMap["QueueArn"].(string); ok && queueArn != "" {
		properties["queue_arn"] = queueArn
	}

	// Message retention (important for data retention)
	if retention, ok := metaMap["MessageRetentionPeriod"].(string); ok && retention != "" {
		properties["message_retention_period"] = retention
	}

	// Visibility timeout (important for processing)
	if timeout, ok := metaMap["VisibilityTimeout"].(string); ok && timeout != "" {
		properties["visibility_timeout"] = timeout
	}

	// Delay seconds (important for delayed processing)
	if delay, ok := metaMap["DelaySeconds"].(string); ok && delay != "" {
		properties["delay_seconds"] = delay
	}

	// DLQ configuration (needed for dead letter queue relationship)
	if redrivePolicy, ok := metaMap["RedrivePolicy"].(string); ok && redrivePolicy != "" {
		// RedrivePolicy is a JSON string, parse it to extract the deadLetterTargetArn
		var policy map[string]interface{}
		if err := json.Unmarshal([]byte(redrivePolicy), &policy); err == nil {
			if dlqArn, ok := policy["deadLetterTargetArn"].(string); ok && dlqArn != "" {
				properties["dead_letter_target_arn"] = dlqArn
			}
		}
	}
}

// extractTopicMetadata extracts essential fields for SNS topics
func (s *AWSSource) extractTopicMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Topic ARN (critical for operations)
	if topicArn, ok := metaMap["TopicArn"].(string); ok && topicArn != "" {
		properties["topic_arn"] = topicArn
	}

	// Display name (useful for UI)
	if displayName, ok := metaMap["DisplayName"].(string); ok && displayName != "" {
		properties["display_name"] = displayName
	}

	// Subscriptions count (important for fan-out analysis) - extracted during edge creation
}

// extractCloudFormationMetadata extracts essential fields for CloudFormation stacks
func (s *AWSSource) extractCloudFormationMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// RoleARN (IAM role used by CloudFormation)
	if roleARN, ok := metaMap["RoleARN"].(string); ok && roleARN != "" {
		properties["role_arn"] = roleARN
	}

	// Parameters (stack input parameters)
	if params, ok := metaMap["Parameters"].([]interface{}); ok && len(params) > 0 {
		if paramsJSON, err := json.Marshal(params); err == nil {
			properties["parameters"] = string(paramsJSON)
		}
	}

	// Outputs (stack output values)
	if outputs, ok := metaMap["Outputs"].([]interface{}); ok && len(outputs) > 0 {
		if outputsJSON, err := json.Marshal(outputs); err == nil {
			properties["outputs"] = string(outputsJSON)
		}
	}
}

// extractStorageMetadata extracts essential fields for S3 buckets, EBS volumes, and EBS snapshots
func (s *AWSSource) extractStorageMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Check service_name and subtype to differentiate between S3, EBS volumes, and snapshots
	serviceName, _ := properties["service_name"].(string)
	subtype, _ := properties["subtype"].(string)

	if serviceName == "AmazonEC2" {
		if subtype == "snapshot" {
			// EBS snapshot metadata extraction
			s.extractSnapshotMetadata(properties, metaMap)
			return
		}
		// EBS volume metadata extraction
		s.extractEBSMetadata(properties, metaMap)
		return
	}

	// S3 bucket metadata extraction (default)
	// Bucket name (critical identifier)
	if bucketName, ok := metaMap["Name"].(string); ok && bucketName != "" {
		properties["bucket_name"] = bucketName
	}

	// Region (important for data locality)
	if region, ok := metaMap["Region"].(string); ok && region != "" {
		properties["bucket_region"] = region
	}

	// Versioning (important for data protection)
	if versioning, ok := metaMap["VersioningConfiguration"].(map[string]interface{}); ok {
		if status, ok := versioning["Status"].(string); ok && status != "" {
			properties["versioning_enabled"] = (status == "Enabled")
		}
	}

	// Encryption (important for security)
	if _, ok := metaMap["ServerSideEncryptionConfiguration"].(map[string]interface{}); ok {
		properties["encryption_enabled"] = true
	}

	// Notification configurations (needed for S3->SNS, S3->SQS, S3->Lambda relationships)
	if notifConfig, ok := metaMap["NotificationConfiguration"].(map[string]interface{}); ok {
		// Extract SNS topic ARNs
		if topicConfigs, ok := notifConfig["TopicConfigurations"].([]interface{}); ok && len(topicConfigs) > 0 {
			var topicARNs []string
			for _, config := range topicConfigs {
				if cfg, ok := config.(map[string]interface{}); ok {
					if topicArn, ok := cfg["TopicArn"].(string); ok && topicArn != "" {
						topicARNs = append(topicARNs, topicArn)
					}
				}
			}
			if len(topicARNs) > 0 {
				if arnsJSON, err := json.Marshal(topicARNs); err == nil {
					properties["notification_topic_arns"] = string(arnsJSON)
				}
			}
		}

		// Extract SQS queue ARNs
		if queueConfigs, ok := notifConfig["QueueConfigurations"].([]interface{}); ok && len(queueConfigs) > 0 {
			var queueARNs []string
			for _, config := range queueConfigs {
				if cfg, ok := config.(map[string]interface{}); ok {
					if queueArn, ok := cfg["QueueArn"].(string); ok && queueArn != "" {
						queueARNs = append(queueARNs, queueArn)
					}
				}
			}
			if len(queueARNs) > 0 {
				if arnsJSON, err := json.Marshal(queueARNs); err == nil {
					properties["notification_queue_arns"] = string(arnsJSON)
				}
			}
		}

		// Extract Lambda function ARNs
		if lambdaConfigs, ok := notifConfig["LambdaFunctionConfigurations"].([]interface{}); ok && len(lambdaConfigs) > 0 {
			var lambdaARNs []string
			for _, config := range lambdaConfigs {
				if cfg, ok := config.(map[string]interface{}); ok {
					if lambdaArn, ok := cfg["LambdaFunctionArn"].(string); ok && lambdaArn != "" {
						lambdaARNs = append(lambdaARNs, lambdaArn)
					}
				}
			}
			if len(lambdaARNs) > 0 {
				if arnsJSON, err := json.Marshal(lambdaARNs); err == nil {
					properties["notification_lambda_arns"] = string(arnsJSON)
				}
			}
		}
	}
}

// extractEBSMetadata extracts essential fields for EBS volumes
func (s *AWSSource) extractEBSMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Volume ID (critical identifier)
	if volumeID, ok := metaMap["VolumeId"].(string); ok && volumeID != "" {
		properties["volume_id"] = volumeID
	}

	// Size in GB (important for capacity planning)
	if size, ok := metaMap["Size"].(float64); ok {
		properties["size_gb"] = int(size)
	} else if size, ok := metaMap["Size"].(int); ok {
		properties["size_gb"] = size
	}

	// Volume type (gp2, gp3, io1, io2, st1, sc1, standard)
	if volumeType, ok := metaMap["VolumeType"].(string); ok && volumeType != "" {
		properties["volume_type"] = volumeType
	}

	// IOPS (important for performance)
	if iops, ok := metaMap["Iops"].(float64); ok {
		properties["iops"] = int(iops)
	} else if iops, ok := metaMap["Iops"].(int); ok {
		properties["iops"] = iops
	}

	// Throughput in MiB/s (for gp3 and io2 volumes)
	if throughput, ok := metaMap["Throughput"].(float64); ok {
		properties["throughput_mibs"] = int(throughput)
	} else if throughput, ok := metaMap["Throughput"].(int); ok {
		properties["throughput_mibs"] = throughput
	}

	// Encryption status (important for security)
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// KMS Key ID (for encrypted volumes)
	if kmsKeyID, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyID != "" {
		properties["kms_key_id"] = kmsKeyID
	}

	// Availability Zone (important for data locality)
	if az, ok := metaMap["AvailabilityZone"].(string); ok && az != "" {
		properties["availability_zone"] = az
	}

	// State (in-use, available, creating, deleting, etc.)
	if state, ok := metaMap["State"].(string); ok && state != "" {
		properties["volume_state"] = state
	}

	// Snapshot ID (if volume was created from a snapshot)
	if snapshotID, ok := metaMap["SnapshotId"].(string); ok && snapshotID != "" {
		properties["snapshot_id"] = snapshotID
	}

	// Multi-attach enabled (for io1/io2 volumes)
	if multiAttach, ok := metaMap["MultiAttachEnabled"].(bool); ok {
		properties["multi_attach_enabled"] = multiAttach
	}

	// Attachment information (needed for EBS->EC2 relationship)
	if attachments, ok := metaMap["Attachments"].([]interface{}); ok && len(attachments) > 0 {
		// Extract first attachment details (most volumes have single attachment)
		if attachment, ok := attachments[0].(map[string]interface{}); ok {
			if instanceID, ok := attachment["InstanceId"].(string); ok && instanceID != "" {
				properties["attached_instance_id"] = instanceID
			}
			if device, ok := attachment["Device"].(string); ok && device != "" {
				properties["device"] = device
			}
			if attachState, ok := attachment["State"].(string); ok && attachState != "" {
				properties["attachment_state"] = attachState
			}
			if deleteOnTerm, ok := attachment["DeleteOnTermination"].(bool); ok {
				properties["delete_on_termination"] = deleteOnTerm
			}
		}
	}
}

// extractSnapshotMetadata extracts essential fields for EBS snapshots
func (s *AWSSource) extractSnapshotMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Set storage_type to distinguish from EBS volumes
	properties["storage_type"] = "snapshot"

	// Snapshot ID (critical identifier)
	if snapshotID, ok := metaMap["SnapshotId"].(string); ok && snapshotID != "" {
		properties["snapshot_id"] = snapshotID
	}

	// Volume ID (the source volume this snapshot was created from)
	if volumeID, ok := metaMap["VolumeId"].(string); ok && volumeID != "" {
		properties["volume_id"] = volumeID
	}

	// Size in GB
	if size, ok := metaMap["VolumeSize"].(float64); ok {
		properties["size_gb"] = int(size)
	} else if size, ok := metaMap["VolumeSize"].(int); ok {
		properties["size_gb"] = size
	}

	// State (pending, completed, error, recoverable, recovering)
	if state, ok := metaMap["State"].(string); ok && state != "" {
		properties["snapshot_state"] = state
	}

	// Progress (percentage complete, e.g., "100%")
	if progress, ok := metaMap["Progress"].(string); ok && progress != "" {
		properties["progress"] = progress
	}

	// Encryption status
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// KMS Key ID (for encrypted snapshots)
	if kmsKeyID, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyID != "" {
		properties["kms_key_id"] = kmsKeyID
	}

	// Owner ID (AWS account that owns the snapshot)
	if ownerID, ok := metaMap["OwnerId"].(string); ok && ownerID != "" {
		properties["owner_id"] = ownerID
	}

	// Description
	if description, ok := metaMap["Description"].(string); ok && description != "" {
		properties["description"] = description
	}

	// Start time (when the snapshot was initiated)
	if startTime, ok := metaMap["StartTime"].(string); ok && startTime != "" {
		properties["start_time"] = startTime
	}
}

// extractNetworkGatewayMetadata extracts essential fields for NAT Gateways
func (s *AWSSource) extractNetworkGatewayMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// NAT Gateway ID (critical identifier)
	if natGatewayID, ok := metaMap["NatGatewayId"].(string); ok && natGatewayID != "" {
		properties["nat_gateway_id"] = natGatewayID
	}

	// State (available, pending, deleting, deleted, failed)
	if state, ok := metaMap["State"].(string); ok && state != "" {
		properties["nat_state"] = state
	}

	// Connectivity Type (public or private)
	if connectivityType, ok := metaMap["ConnectivityType"].(string); ok && connectivityType != "" {
		properties["connectivity_type"] = connectivityType
	}

	// Subnet ID (where NAT Gateway is deployed)
	if subnetID, ok := metaMap["SubnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}

	// VPC ID (parent VPC)
	if vpcID, ok := metaMap["VpcId"].(string); ok && vpcID != "" {
		properties["vpc_id"] = vpcID
	}

	// NAT Gateway Addresses (includes ENI, EIP, private/public IPs)
	if addresses, ok := metaMap["NatGatewayAddresses"].([]interface{}); ok && len(addresses) > 0 {
		// Extract primary address info
		if primaryAddr, ok := addresses[0].(map[string]interface{}); ok {
			// Network Interface ID
			if eniID, ok := primaryAddr["NetworkInterfaceId"].(string); ok && eniID != "" {
				properties["network_interface_id"] = eniID
			}

			// Allocation ID (Elastic IP allocation)
			if allocationID, ok := primaryAddr["AllocationId"].(string); ok && allocationID != "" {
				properties["allocation_id"] = allocationID
			}

			// Public IP
			if publicIP, ok := primaryAddr["PublicIp"].(string); ok && publicIP != "" {
				properties["public_ip"] = publicIP
			}

			// Private IP
			if privateIP, ok := primaryAddr["PrivateIp"].(string); ok && privateIP != "" {
				properties["private_ip"] = privateIP
			}

			// Association ID (EIP association)
			if associationID, ok := primaryAddr["AssociationId"].(string); ok && associationID != "" {
				properties["association_id"] = associationID
			}

			// Is Primary address
			if isPrimary, ok := primaryAddr["IsPrimary"].(bool); ok {
				properties["is_primary"] = isPrimary
			}

			// Address Status
			if status, ok := primaryAddr["Status"].(string); ok && status != "" {
				properties["address_status"] = status
			}
		}

		// Store all addresses for reference
		properties["nat_gateway_addresses"] = addresses
	}

	// Create Time
	if createTime, ok := metaMap["CreateTime"].(string); ok && createTime != "" {
		properties["create_time"] = createTime
	}

	// Failure Code and Message (if failed)
	if failureCode, ok := metaMap["FailureCode"].(string); ok && failureCode != "" {
		properties["failure_code"] = failureCode
	}
	if failureMessage, ok := metaMap["FailureMessage"].(string); ok && failureMessage != "" {
		properties["failure_message"] = failureMessage
	}
}

// extractBackupVaultMetadata extracts essential fields for AWS Backup Vaults
func (s *AWSSource) extractBackupVaultMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Vault locked status
	if locked, ok := metaMap["Locked"].(bool); ok {
		properties["locked"] = locked
	}

	// Vault type (BACKUP_VAULT, LOGICALLY_AIR_GAPPED_BACKUP_VAULT)
	if vaultType, ok := metaMap["VaultType"].(string); ok && vaultType != "" {
		properties["vault_type"] = vaultType
	}

	// Vault state
	if vaultState, ok := metaMap["VaultState"].(string); ok && vaultState != "" {
		properties["vault_state"] = vaultState
	}

	// Encryption key ARN (for IS_ENCRYPTED_BY edge)
	if encryptionKeyArn, ok := metaMap["EncryptionKeyArn"].(string); ok && encryptionKeyArn != "" {
		properties["encryption_key_arn"] = encryptionKeyArn
	}

	// Number of recovery points
	if numRecoveryPoints, ok := metaMap["NumberOfRecoveryPoints"].(float64); ok {
		properties["number_of_recovery_points"] = int(numRecoveryPoints)
	}

	// Creation date
	if creationDate, ok := metaMap["CreationDate"].(string); ok && creationDate != "" {
		properties["creation_date"] = creationDate
	}
}

// extractBackupPolicyMetadata extracts essential fields for AWS Backup Plans
func (s *AWSSource) extractBackupPolicyMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Version ID
	if versionId, ok := metaMap["VersionId"].(string); ok && versionId != "" {
		properties["version_id"] = versionId
	}

	// Plan details contain the backup rules
	if planDetails, ok := metaMap["PlanDetails"].(map[string]interface{}); ok {
		// Extract rules
		if rules, ok := planDetails["Rules"].([]interface{}); ok && len(rules) > 0 {
			properties["rule_count"] = len(rules)

			// Extract target vault from first rule
			if firstRule, ok := rules[0].(map[string]interface{}); ok {
				if targetVaultName, ok := firstRule["TargetBackupVaultName"].(string); ok && targetVaultName != "" {
					properties["target_backup_vault_name"] = targetVaultName
				}
				if ruleName, ok := firstRule["RuleName"].(string); ok && ruleName != "" {
					properties["primary_rule_name"] = ruleName
				}
				// Extract lifecycle info
				if lifecycle, ok := firstRule["Lifecycle"].(map[string]interface{}); ok {
					if deleteAfter, ok := lifecycle["DeleteAfterDays"].(float64); ok {
						properties["delete_after_days"] = int(deleteAfter)
					}
					if moveToCold, ok := lifecycle["MoveToColdStorageAfterDays"].(float64); ok {
						properties["move_to_cold_storage_after_days"] = int(moveToCold)
					}
				}
			}
		}
	}

	// Creation date
	if creationDate, ok := metaMap["CreationDate"].(string); ok && creationDate != "" {
		properties["creation_date"] = creationDate
	}

	// Last execution date
	if lastExecutionDate, ok := metaMap["LastExecutionDate"].(string); ok && lastExecutionDate != "" {
		properties["last_execution_date"] = lastExecutionDate
	}
}

// extractEFSMetadata extracts essential fields for EFS file systems
func (s *AWSSource) extractEFSMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// File system ID (fs-xxxxx)
	if fsId, ok := metaMap["FileSystemId"].(string); ok && fsId != "" {
		properties["filesystem_id"] = fsId
	}

	// Performance mode (generalPurpose or maxIO)
	if perfMode, ok := metaMap["PerformanceMode"].(string); ok && perfMode != "" {
		properties["performance_mode"] = perfMode
	}

	// Throughput mode (bursting or provisioned)
	if throughputMode, ok := metaMap["ThroughputMode"].(string); ok && throughputMode != "" {
		properties["throughput_mode"] = throughputMode
	}

	// Provisioned throughput (only for provisioned mode)
	if provisionedThroughput, ok := metaMap["ProvisionedThroughputInMibps"].(float64); ok && provisionedThroughput > 0 {
		properties["provisioned_throughput_mibps"] = provisionedThroughput
	}

	// Encryption
	if encrypted, ok := metaMap["Encrypted"].(bool); ok {
		properties["encrypted"] = encrypted
	}

	// KMS Key ID (if encrypted)
	if kmsKeyId, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyId != "" {
		properties["kms_key_id"] = kmsKeyId
	}

	// Lifecycle state
	if lifecycleState, ok := metaMap["LifeCycleState"].(string); ok && lifecycleState != "" {
		properties["lifecycle_state"] = lifecycleState
	}

	// Size in bytes
	if sizeInBytes, ok := metaMap["SizeInBytes"].(map[string]interface{}); ok {
		if value, ok := sizeInBytes["Value"].(float64); ok {
			properties["size_in_bytes"] = int64(value)
		}
	}

	// Number of mount targets
	if numMountTargets, ok := metaMap["NumberOfMountTargets"].(float64); ok {
		properties["number_of_mount_targets"] = int(numMountTargets)
	}

	// Creation time
	if creationTime, ok := metaMap["CreationTime"].(string); ok && creationTime != "" {
		properties["creation_time"] = creationTime
	}

	// Generate DNS name from filesystem ID and region
	// Format: fs-xxxxx.efs.{region}.amazonaws.com
	if fsId, ok := properties["filesystem_id"].(string); ok && fsId != "" {
		if region, ok := properties["region"].(string); ok && region != "" {
			properties["dns_name"] = fmt.Sprintf("%s.efs.%s.amazonaws.com", fsId, region)
		}
	}
}

// extractLoadBalancerMetadata extracts essential fields for load balancers
func (s *AWSSource) extractLoadBalancerMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// DNS name (critical for routing)
	if dnsName, ok := metaMap["DNSName"].(string); ok && dnsName != "" {
		properties["dns_name"] = dnsName
	}

	// Scheme (important for accessibility)
	if scheme, ok := metaMap["Scheme"].(string); ok && scheme != "" {
		properties["scheme"] = scheme
	}

	// Type (important for capabilities) - ELBv2 only
	if lbType, ok := metaMap["Type"].(string); ok && lbType != "" {
		properties["load_balancer_type"] = lbType
	}

	// State code (active, provisioning, failed)
	if state, ok := metaMap["State"].(map[string]interface{}); ok {
		if code, ok := state["Code"].(string); ok && code != "" {
			properties["state"] = code
		}
	}

	// Subnets for edge creation:
	// Classic ELB: direct "Subnets" list of IDs
	// NLB/ALB: "AvailabilityZones" array of objects with "SubnetId"
	if subnets, ok := metaMap["Subnets"].([]interface{}); ok && len(subnets) > 0 {
		properties["subnets"] = subnets
	} else if azs, ok := metaMap["AvailabilityZones"].([]interface{}); ok && len(azs) > 0 {
		subnetList := make([]interface{}, 0, len(azs))
		for _, az := range azs {
			if azMap, ok := az.(map[string]interface{}); ok {
				if subnetID, ok := azMap["SubnetId"].(string); ok && subnetID != "" {
					subnetList = append(subnetList, subnetID)
				}
			}
		}
		if len(subnetList) > 0 {
			properties["subnets"] = subnetList
		}
	}

	// Instances (Classic ELB) — stored for edge creation without CLI
	if instances, ok := metaMap["Instances"].([]interface{}); ok && len(instances) > 0 {
		properties["instances"] = instances
	}
}

// extractClusterMetadata extracts essential fields for EKS/ECS clusters
func (s *AWSSource) extractClusterMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Cluster endpoint (important for API access)
	if endpoint, ok := metaMap["Endpoint"].(string); ok && endpoint != "" {
		properties["cluster_endpoint"] = endpoint
	}

	// Version (important for compatibility)
	if version, ok := metaMap["Version"].(string); ok && version != "" {
		properties["cluster_version"] = version
	}

	// Status (important for operational state)
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["cluster_status"] = status
	}
}

// extractLambdaMetadata extracts essential fields for Lambda functions
func (s *AWSSource) extractLambdaMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Runtime (important for compatibility)
	if runtime, ok := metaMap["Runtime"].(string); ok && runtime != "" {
		properties["runtime"] = runtime
	}

	// Memory (important for capacity)
	if memory, ok := metaMap["MemorySize"].(float64); ok {
		properties["memory_mb"] = int(memory)
	}

	// Timeout (important for SLA)
	if timeout, ok := metaMap["Timeout"].(float64); ok {
		properties["timeout_seconds"] = int(timeout)
	}

	// Handler (important for invocation)
	if handler, ok := metaMap["Handler"].(string); ok && handler != "" {
		properties["handler"] = handler
	}

	// Environment variables (needed for relationship matching to DynamoDB, SQS, SNS, etc.)
	if env, ok := metaMap["Environment"].(map[string]interface{}); ok {
		if vars, ok := env["Variables"].(map[string]interface{}); ok && len(vars) > 0 {
			// Store as JSON string for flexible matching in relationship rules
			if varsJSON, err := json.Marshal(vars); err == nil {
				properties["environment_variables"] = string(varsJSON)
			}
			// Also extract commonly used variable names for direct matching
			if dynamoTable, ok := vars["DYNAMODB_TABLE"].(string); ok && dynamoTable != "" {
				properties["dynamodb_table_name"] = dynamoTable
			}
			if queueURL, ok := vars["QUEUE_URL"].(string); ok && queueURL != "" {
				properties["queue_url"] = queueURL
			}
			if topicARN, ok := vars["TOPIC_ARN"].(string); ok && topicARN != "" {
				properties["sns_topic_arn"] = topicARN
			}
		}
	}

	// Execution role ARN (needed for ServiceIdentity relationship)
	if roleARN, ok := metaMap["Role"].(string); ok && roleARN != "" {
		properties["role_arn"] = roleARN
	}

	// Event source mappings (needed for Lambda-SQS trigger relationship)
	if mappings, ok := metaMap["EventSourceMappings"].([]interface{}); ok && len(mappings) > 0 {
		var eventSourceARNs []string
		for _, mapping := range mappings {
			if m, ok := mapping.(map[string]interface{}); ok {
				if arn, ok := m["EventSourceArn"].(string); ok && arn != "" {
					eventSourceARNs = append(eventSourceARNs, arn)
				}
			}
		}
		if len(eventSourceARNs) > 0 {
			// Store as JSON array for matching
			if arnsJSON, err := json.Marshal(eventSourceARNs); err == nil {
				properties["event_source_arns"] = string(arnsJSON)
			}
		}
	}
}

// extractCDNMetadata extracts essential fields for CloudFront distributions
func (s *AWSSource) extractCDNMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Domain name (critical for CDN endpoint)
	if domainName, ok := metaMap["DomainName"].(string); ok && domainName != "" {
		properties["domain_name"] = domainName
	}

	// Status (important for operational state)
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["status"] = status
	}

	// Extract origin domains (needed for CloudFront->LoadBalancer relationship).
	// AWS API nests Origins under DistributionConfig, not at the top level.
	var originItems []interface{}
	if distConfig, ok := metaMap["DistributionConfig"].(map[string]interface{}); ok {
		if origins, ok := distConfig["Origins"].(map[string]interface{}); ok {
			if items, ok := origins["Items"].([]interface{}); ok {
				originItems = items
			}
		}
	}
	// Fallback: some responses may have Origins at top level (e.g. list-distributions summary)
	if len(originItems) == 0 {
		if origins, ok := metaMap["Origins"].(map[string]interface{}); ok {
			if items, ok := origins["Items"].([]interface{}); ok {
				originItems = items
			}
		}
	}
	if len(originItems) > 0 {
		var originDomains []string
		for _, item := range originItems {
			if originItem, ok := item.(map[string]interface{}); ok {
				if domain, ok := originItem["DomainName"].(string); ok && domain != "" {
					originDomains = append(originDomains, domain)
				}
			}
		}
		if len(originDomains) > 0 {
			if domainsJSON, err := json.Marshal(originDomains); err == nil {
				properties["origin_domains"] = string(domainsJSON)
			}
		}
	}
}

// extractDNSZoneMetadata extracts essential fields for Route53 hosted zones
func (s *AWSSource) extractDNSZoneMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Zone name (critical identifier)
	if zoneName, ok := metaMap["Name"].(string); ok && zoneName != "" {
		properties["zone_name"] = zoneName
	}

	// Private zone flag (important for access control)
	if privateZone, ok := metaMap["PrivateZone"].(bool); ok {
		properties["private_zone"] = privateZone
	}

	// Extract alias target DNS name (needed for Route53->LoadBalancer relationship)
	if aliasTarget, ok := metaMap["AliasTarget"].(map[string]interface{}); ok {
		if dnsName, ok := aliasTarget["DNSName"].(string); ok && dnsName != "" {
			properties["alias_target_dns"] = dnsName
		}
		if hostedZoneId, ok := aliasTarget["HostedZoneId"].(string); ok && hostedZoneId != "" {
			properties["alias_target_zone_id"] = hostedZoneId
		}
	}
}

// extractContainerRegistryMetadata extracts essential fields for ECR repositories
func (s *AWSSource) extractContainerRegistryMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Repository name (critical identifier)
	if repoName, ok := metaMap["RepositoryName"].(string); ok && repoName != "" {
		properties["repository_name"] = repoName
	}

	// Repository URI (needed for ECR->Workload relationship matching)
	if repoURI, ok := metaMap["RepositoryUri"].(string); ok && repoURI != "" {
		properties["repository_uri"] = repoURI
	}

	// Registry ID (AWS account)
	if registryId, ok := metaMap["RegistryId"].(string); ok && registryId != "" {
		properties["registry_id"] = registryId
	}

	// Image tag mutability
	if mutability, ok := metaMap["ImageTagMutability"].(string); ok && mutability != "" {
		properties["image_tag_mutability"] = mutability
	}
}

// extractCloudTrailMetadata extracts essential fields for CloudTrail resources (Trails and Event Data Stores)
func (s *AWSSource) extractCloudTrailMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Trail: S3 bucket destination (for PUBLISHES_TO edge)
	if s3BucketName, ok := metaMap["S3BucketName"].(string); ok && s3BucketName != "" {
		properties["s3_bucket_name"] = s3BucketName
	}
	if s3KeyPrefix, ok := metaMap["S3KeyPrefix"].(string); ok && s3KeyPrefix != "" {
		properties["s3_key_prefix"] = s3KeyPrefix
	}

	// Trail: SNS topic for notifications (for PUBLISHES_TO edge)
	if snsTopicArn, ok := metaMap["SnsTopicARN"].(string); ok && snsTopicArn != "" {
		properties["sns_topic_arn"] = snsTopicArn
	}

	// Trail: CloudWatch Logs integration (for PUBLISHES_TO edge)
	if cwLogsArn, ok := metaMap["CloudWatchLogsLogGroupArn"].(string); ok && cwLogsArn != "" {
		properties["cloudwatch_logs_log_group_arn"] = cwLogsArn
	}

	// KMS encryption (for IS_ENCRYPTED_BY edge)
	if kmsKeyId, ok := metaMap["KmsKeyId"].(string); ok && kmsKeyId != "" {
		properties["kms_key_id"] = kmsKeyId
	}

	// Trail configuration metadata
	if isMultiRegion, ok := metaMap["IsMultiRegionTrail"].(bool); ok {
		properties["is_multi_region_trail"] = isMultiRegion
	}
	if isOrgTrail, ok := metaMap["IsOrganizationTrail"].(bool); ok {
		properties["is_organization_trail"] = isOrgTrail
	}
	if homeRegion, ok := metaMap["HomeRegion"].(string); ok && homeRegion != "" {
		properties["home_region"] = homeRegion
	}
	if logValidation, ok := metaMap["LogFileValidationEnabled"].(bool); ok {
		properties["log_file_validation_enabled"] = logValidation
	}

	// Trail status (from TrailStatus sub-map)
	if trailStatus, ok := metaMap["TrailStatus"].(map[string]interface{}); ok {
		if isLogging, ok := trailStatus["IsLogging"].(bool); ok {
			properties["is_logging"] = isLogging
		}
	}

	// Event Data Store metadata
	if status, ok := metaMap["Status"].(string); ok && status != "" {
		properties["eds_status"] = status
	}
	if retentionPeriod, ok := metaMap["RetentionPeriod"].(float64); ok {
		properties["retention_period_days"] = int(retentionPeriod)
	}
	if multiRegionEnabled, ok := metaMap["MultiRegionEnabled"].(bool); ok {
		properties["multi_region_enabled"] = multiRegionEnabled
	}
	if termProtection, ok := metaMap["TerminationProtectionEnabled"].(bool); ok {
		properties["termination_protection_enabled"] = termProtection
	}
}

// DefaultServiceTypeFilter provides a predefined service-to-type mapping
// This can be used as a reference or default configuration to limit resource processing
var DefaultServiceTypeFilter = map[string][]string{
	"AmazonEC2": {
		"compute-instance",
		"natgateway",
		"snapshot",
		"storage", // EBS volumes
	},
	"AWSCloudFormation": {"stack"},
	"AmazonS3":          {"storage"},
	"AmazonCloudWatch":  {"log-group", "vpc-flow-log"},
	"AWSQueueService":   {"queue"},
	"AmazonSNS":         {"topic"},
	"AmazonVPC":         {"elastic-ip", "network-interface", "security_group", "subnet", "vpc", "vpc-endpoint"},
	"AmazonECS":         {"cluster", "service"},
	"AWSSystemsManager": {"managedinstance"},
}

// awsResourceTypeMap maps (type, service_name) combinations to NodeTypes
// This provides precise mapping for resources where type alone is ambiguous
var awsResourceTypeMap = map[string]map[string]core.NodeType{
	"cluster": {
		"AmazonRDS":         core.NodeTypeDatabase,
		"AmazonEKS":         core.NodeTypeManagedCluster,
		"AmazonElastiCache": core.NodeTypeCache,
		"AmazonECS":         core.NodeTypeManagedCluster, // ECS Cluster is a managed container orchestration cluster
		"AmazonMSK":         core.NodeTypeMessageQueue,
		"AmazonRedshift":    core.NodeTypeDatabase,
	},
	"service": {
		"AmazonECS": core.NodeTypeWorkload, // ECS Service manages running tasks, similar to K8s Deployment
	},
	"db": {
		"AmazonRDS": core.NodeTypeDatabase,
	},
	"cluster-snapshot": {
		"AmazonRDS": core.NodeTypeCloudResource,
	},
	"function": {
		"AWSLambda":        core.NodeTypeServerlessFunction,
		"AmazonCloudFront": core.NodeTypeCDN,
	},
	"queue": {
		"AWSQueueService": core.NodeTypeMessageQueue,
	},
	"topic": {
		"AmazonSNS": core.NodeTypeTopic,
	},
	"loadbalancer": {
		"AWSELB": core.NodeTypeLoadBalancer,
	},
	"application_loadbalancer": {
		"AWSELB": core.NodeTypeLoadBalancer,
	},
	"network_loadbalancer": {
		"AWSELB": core.NodeTypeLoadBalancer,
	},
	"targetgroup": {
		"AWSELB": core.NodeTypeBackendPool,
	},
	"compute-instance": {
		"AmazonEC2": core.NodeTypeComputeInstance,
	},
	"managedinstance": {
		"AWSSystemsManager": core.NodeTypeComputeInstance,
	},
	"snapshot": {
		"AmazonEC2": core.NodeTypeStorage, // EBS snapshots
	},
	"table": {
		"AmazonDynamoDB": core.NodeTypeDatabase,
	},
	"distribution": {
		"AmazonCloudFront": core.NodeTypeCDN,
	},
	"vpc": {
		"AmazonVPC": core.NodeTypeVPC,
	},
	"security_group": {
		"AmazonVPC": core.NodeTypeSecurityGroup,
	},
	"natgateway": {
		"AmazonEC2": core.NodeTypeNetworkGateway,
	},
	"subnet": {
		"AmazonVPC": core.NodeTypeSubnet,
	},
	"vpc-endpoint": {
		"AmazonVPC": core.NodeTypePrivateEndpoint,
	},
	"elastic-ip": {
		"AmazonVPC": core.NodeTypePublicIP,
	},
	"network-interface": {
		"AmazonVPC": core.NodeTypeNetworkInterface,
	},
	"hostedzone": {
		"AmazonRoute53": core.NodeTypeDNSZone,
	},
	"repository": {
		"AmazonECR":       core.NodeTypeContainerRegistry,
		"AmazonECRPublic": core.NodeTypeContainerRegistry,
	},
	"secret": {
		"AWSSecretsManager": core.NodeTypeSecretVault,
	},
	"log-group": {
		"AmazonCloudWatch": core.NodeTypeLogAggregator,
	},
	"vpc-flow-log": {
		"AmazonCloudWatch": core.NodeTypeLogAggregator,
	},
	"trail": {
		"AWSCloudTrail": core.NodeTypeLogAggregator,
	},
	"eventdatastore": {
		"AWSCloudTrail": core.NodeTypeLogAggregator,
	},
	"filesystem": {
		"AmazonEFS": core.NodeTypeStorage,
	},
	"file-system": {
		"AmazonEFS": core.NodeTypeStorage,
	},
	"pod": {
		"AmazonEKS": core.NodeTypePod,
	},
	"storage": {
		"AmazonS3":  core.NodeTypeStorage,
		"AmazonEC2": core.NodeTypeStorage, // EBS volumes
	},
	"key": {
		"AWSKMS": core.NodeTypeEncryptionKey,
	},
	"backup-vault": {
		"AWSBackup": core.NodeTypeBackupVault,
	},
	"backup-plan": {
		"AWSBackup": core.NodeTypeBackupPolicy,
	},
	"rest-api": {
		"AmazonAPIGateway": core.NodeTypeAPIGateway,
	},
	"http-api": {
		"AmazonAPIGateway": core.NodeTypeAPIGateway,
	},
	"websocket-api": {
		"AmazonAPIGateway": core.NodeTypeAPIGateway,
	},
	"stack": {
		"AWSCloudFormation": core.NodeTypeInfraStack,
	},
	// SES resources
	"configuration-set": {
		"AmazonSES": core.NodeTypeEmailService,
	},
	"identity": {
		"AmazonSES": core.NodeTypeEmailService,
	},
	// SecurityHub resources
	"hub": {
		"AWSSecurityHub": core.NodeTypeSecurityService,
	},
	"standard": {
		"AWSSecurityHub": core.NodeTypeSecurityService,
	},
	// Bedrock resources - note: types contain slashes like "foundation-model/meta.llama3-1-70b-instruct-v1"
	// These will be handled by service fallback since exact type matching won't work
}

// awsServiceFallbackMap maps service names to NodeTypes when type-based mapping is insufficient
var awsServiceFallbackMap = map[string]core.NodeType{
	"AmazonRDS":             core.NodeTypeDatabase,
	"AmazonElastiCache":     core.NodeTypeCache,
	"AmazonS3":              core.NodeTypeStorage,
	"AmazonEC2":             core.NodeTypeComputeInstance,
	"AWSLambda":             core.NodeTypeServerlessFunction,
	"AmazonDynamoDB":        core.NodeTypeDatabase,
	"AWSQueueService":       core.NodeTypeMessageQueue,
	"AmazonSNS":             core.NodeTypeTopic,
	"AmazonVPC":             core.NodeTypeVPC,
	"AWSELB":                core.NodeTypeLoadBalancer,
	"AmazonRoute53":         core.NodeTypeDNSZone,
	"AmazonCloudFront":      core.NodeTypeCDN,
	"AmazonECR":             core.NodeTypeContainerRegistry,
	"AmazonECRPublic":       core.NodeTypeContainerRegistry,
	"AWSSecretsManager":     core.NodeTypeSecretVault,
	"AmazonCloudWatch":      core.NodeTypeLogAggregator,
	"AmazonEKS":             core.NodeTypeManagedCluster,
	"AmazonECS":             core.NodeTypeCloudResource,
	"AmazonMSK":             core.NodeTypeMessageQueue,
	"AmazonRedshift":        core.NodeTypeDatabase,
	"AmazonES":              core.NodeTypeDatabase,
	"AmazonSageMaker":       core.NodeTypeCloudResource,
	"AWSBackup":             core.NodeTypeCloudResource,
	"ACM":                   core.NodeTypeCloudResource,
	"AmazonKinesisFirehose": core.NodeTypeCloudResource,
	"AmazonGuardDuty":       core.NodeTypeCloudResource,
	"AWSXRay":               core.NodeTypeCloudResource,
	"AWSCloudTrail":         core.NodeTypeLogAggregator,
	"AmazonEFS":             core.NodeTypeStorage,
	"AmazonDataZone":        core.NodeTypeCloudResource,
	"AmazonBedrock":         core.NodeTypeAIService,
	"AWSKMS":                core.NodeTypeEncryptionKey,
	"AWSSystemsManager":     core.NodeTypeCloudResource,
	"AWSEvents":             core.NodeTypeCloudResource,
	"AWSCloudFormation":     core.NodeTypeInfraStack,
	"AmazonQuickSight":      core.NodeTypeCloudResource,
	"AmazonCognito":         core.NodeTypeCloudResource,
	"AWSIAM":                core.NodeTypeCloudResource,
	"AmazonAthena":          core.NodeTypeCloudResource,
	"AmazonSES":             core.NodeTypeEmailService,
	"AWSSecurityHub":        core.NodeTypeSecurityService,
	"awswaf":                core.NodeTypeCloudResource,
	"AmazonAPIGateway":      core.NodeTypeAPIGateway,
}

// cloudFormationResourceTypeMapping defines how to find CloudFormation-managed resources in the graph
type cloudFormationResourceTypeMapping struct {
	NodeType    core.NodeType
	LookupByARN bool // true if PhysicalResourceId is an ARN, false if it's a resource ID
}

// cloudFormationResourceTypeMap maps AWS CloudFormation ResourceType to NodeType and lookup strategy
// Used when creating edges from CloudFormation stacks to their managed resources
var cloudFormationResourceTypeMap = map[string]cloudFormationResourceTypeMapping{
	// Compute
	"AWS::EC2::Instance":    {core.NodeTypeComputeInstance, false},
	"AWS::Lambda::Function": {core.NodeTypeServerlessFunction, true},

	// Storage
	"AWS::S3::Bucket":      {core.NodeTypeStorage, false}, // PhysicalResourceId is bucket name
	"AWS::EC2::Volume":     {core.NodeTypeStorage, false},
	"AWS::EFS::FileSystem": {core.NodeTypeStorage, false},

	// Database
	"AWS::RDS::DBInstance":               {core.NodeTypeDatabase, false},
	"AWS::RDS::DBCluster":                {core.NodeTypeDatabase, false},
	"AWS::DynamoDB::Table":               {core.NodeTypeDatabase, false},
	"AWS::ElastiCache::CacheCluster":     {core.NodeTypeCache, false},
	"AWS::ElastiCache::ReplicationGroup": {core.NodeTypeCache, false},

	// Networking
	"AWS::EC2::VPC":                             {core.NodeTypeVPC, false},
	"AWS::EC2::Subnet":                          {core.NodeTypeSubnet, false},
	"AWS::EC2::SecurityGroup":                   {core.NodeTypeSecurityGroup, false},
	"AWS::EC2::NatGateway":                      {core.NodeTypeNetworkGateway, false},
	"AWS::EC2::VPCEndpoint":                     {core.NodeTypePrivateEndpoint, false},
	"AWS::EC2::RouteTable":                      {core.NodeTypeRouteTable, false},
	"AWS::EC2::EIP":                             {core.NodeTypePublicIP, false},
	"AWS::EC2::NetworkInterface":                {core.NodeTypeNetworkInterface, false},
	"AWS::ElasticLoadBalancingV2::LoadBalancer": {core.NodeTypeLoadBalancer, true},
	"AWS::ElasticLoadBalancingV2::TargetGroup":  {core.NodeTypeBackendPool, true},

	// Messaging
	"AWS::SQS::Queue":   {core.NodeTypeMessageQueue, true},
	"AWS::SNS::Topic":   {core.NodeTypeTopic, true},
	"AWS::MSK::Cluster": {core.NodeTypeMessageQueue, true},

	// Container Services
	"AWS::EKS::Cluster":    {core.NodeTypeManagedCluster, true},
	"AWS::ECS::Cluster":    {core.NodeTypeManagedCluster, true},
	"AWS::ECS::Service":    {core.NodeTypeWorkload, true},
	"AWS::ECR::Repository": {core.NodeTypeContainerRegistry, true},

	// Security & Encryption
	"AWS::KMS::Key":               {core.NodeTypeEncryptionKey, true},
	"AWS::SecretsManager::Secret": {core.NodeTypeSecretVault, true},

	// Observability
	"AWS::Logs::LogGroup": {core.NodeTypeLogAggregator, true},

	// API Gateway
	"AWS::ApiGateway::RestApi": {core.NodeTypeAPIGateway, false},
	"AWS::ApiGatewayV2::Api":   {core.NodeTypeAPIGateway, false},

	// Backup
	"AWS::Backup::BackupVault": {core.NodeTypeBackupVault, true},
	"AWS::Backup::BackupPlan":  {core.NodeTypeBackupPolicy, true},

	// Nested stacks
	"AWS::CloudFormation::Stack": {core.NodeTypeInfraStack, true},

	// IAM resources (PhysicalResourceId is the role/policy name or ARN)
	"AWS::IAM::Role":            {core.NodeTypeCloudResource, false},
	"AWS::IAM::Policy":          {core.NodeTypeCloudResource, true},
	"AWS::IAM::ManagedPolicy":   {core.NodeTypeCloudResource, true},
	"AWS::IAM::InstanceProfile": {core.NodeTypeCloudResource, false},

	// EventBridge / Events
	"AWS::Events::Rule":        {core.NodeTypeCloudResource, false},
	"AWS::Events::EventBus":    {core.NodeTypeCloudResource, false},
	"AWS::Scheduler::Schedule": {core.NodeTypeCloudResource, false},
}

// determineNodeType determines the knowledge graph node type from AWS resource type and service name
func (s *AWSSource) determineNodeType(resourceType, serviceName string) core.NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	// First, try exact match with type + service_name combination
	if serviceMap, exists := awsResourceTypeMap[resourceTypeLower]; exists {
		if nodeType, found := serviceMap[serviceName]; found {
			return nodeType
		}
	}

	// Second, try service name fallback
	if nodeType, exists := awsServiceFallbackMap[serviceName]; exists {
		return nodeType
	}

	// Default fallback for unmapped resources
	return core.NodeTypeCloudResource
}

// createInferredVPCNode creates an inferred VPC node with name fetched from AWS if available
func (s *AWSSource) createInferredVPCNode(reqCtx *security.RequestContext, vpcID string, req *core.SourceBuildRequest) *core.DbNode {
	vpcName := vpcID // Default to VPC ID
	cidrBlock := ""
	state := ""
	isDefault := false

	// Try to fetch VPC metadata from AWS to get the name and other details
	if req.CloudAccountID != "" && reqCtx != nil {
		vpcData, err := s.fetchVPCDataFromAWS(reqCtx, req, req.CloudAccountID, vpcID)
		if err != nil {
			s.logger.Warn("Failed to fetch VPC data from AWS, using VPC ID as name",
				"vpc_id", vpcID,
				"error", err)
		} else {
			// Extract VPC name from tags
			for _, tag := range vpcData.Tags {
				if tag.Key == "Name" && tag.Value != "" {
					vpcName = tag.Value
					break
				}
			}
			cidrBlock = vpcData.CidrBlock
			state = vpcData.State
			isDefault = vpcData.IsDefault

			s.logger.Info("Successfully enriched inferred VPC with AWS data",
				"vpc_id", vpcID,
				"vpc_name", vpcName,
				"state", state)
		}
	}

	properties := map[string]interface{}{
		"name":           vpcName,
		"vpc_id":         vpcID,
		"resource_id":    vpcID,
		"inferred":       true,
		"type":           "vpc",
		"subtype":        "vpc",
		"service_name":   "AmazonVPC",
		"cloud_provider": "AWS",
	}

	// Add optional fields if available
	if cidrBlock != "" {
		properties["cidr_block"] = cidrBlock
	}
	if state != "" {
		properties["vpc_state"] = state
	}
	if isDefault {
		properties["is_default"] = isDefault
	}

	// Build unique key using new 6-part format
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeVPC,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeVPC, uniqueKey, properties, req.TenantID, req.CloudAccountID, "aws")
}

// createInferredSubnetNode creates an inferred subnet node when subnet is referenced but not in database
func (s *AWSSource) createInferredSubnetNode(subnetID string, req *core.SourceBuildRequest) *core.DbNode {
	properties := map[string]interface{}{
		"name":                 subnetID,
		"subnet_id":            subnetID,
		"resource_id":          subnetID,
		"inferred":             true,
		"type":                 "subnet_inferred",
		"subtype":              "subnet_inferred",
		"service_name":         "AmazonVPC",
		"cloud_provider":       "AWS",
		"external_resource_id": subnetID,
	}

	// Build unique key using new 6-part format
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeSubnet,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(core.NodeTypeSubnet, uniqueKey, properties, req.TenantID, req.CloudAccountID, "aws")
}

// ========================================================================
// Edge Creation Methods - Each method handles edge creation for a specific node type
// ========================================================================

// createEC2Edges creates edges for EC2 instances
func (s *AWSSource) createEC2Edges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use pre-extracted properties (set by extractCommonMetadataFields and extractComputeMetadata).
		// Raw meta is not stored in node.Properties, so we read the flattened fields.

		// 1. EC2 → VPC relationship
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. EC2 → Subnet relationship
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// 3. EC2 → Security Group relationships
		// security_groups is stored as []interface{} with each element being {"GroupId": ..., "GroupName": ...}
		if securityGroups, ok := node.Properties["security_groups"].([]interface{}); ok {
			for _, sg := range securityGroups {
				if sgMap, ok := sg.(map[string]interface{}); ok {
					if groupID, ok := sgMap["GroupId"].(string); ok && groupID != "" {
						if sgNode, exists := lookup.byResourceID[groupID]; exists {
							edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "security_group",
									"group_name":      sgMap["GroupName"],
								}, req))
						}
					}
				}
			}
		}

		// 4. EC2 → EKS Cluster relationship (if part of EKS node group)
		// Tags are stored in properties["labels"] as {"key": ["value"]} (values are arrays from the tags column).
		// EKS cluster tag key is "aws:eks:cluster-name" (or legacy "eks:cluster-name").
		if labels, ok := node.Properties["labels"].(map[string]interface{}); ok {
			eksClusterName := extractLabelValue(labels, "aws:eks:cluster-name")
			if eksClusterName == "" {
				eksClusterName = extractLabelValue(labels, "eks:cluster-name")
			}

			if eksClusterName != "" {
				for _, eksNode := range lookup.byNodeType[core.NodeTypeManagedCluster] {
					if clusterName, ok := getStringProperty(eksNode, "name"); ok && clusterName == eksClusterName {
						edges = append(edges, s.createEdge(node, eksNode, core.RelationshipRunsOn,
							map[string]interface{}{
								"connection_type": "eks_node",
								"cluster_name":    eksClusterName,
							}, req))
						break
					}
				}
			}
		}
	}

	return edges
}

// createRDSEdges creates edges for RDS instances/clusters
func (s *AWSSource) createRDSEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use pre-extracted properties (set by extractDatabaseMetadata and extractCommonMetadataFields).
		// Raw meta is not stored in node.Properties, so we read the flattened fields.

		// 1. RDS → VPC relationship
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. RDS → Subnet relationships (subnet_ids extracted by extractDatabaseMetadata)
		if subnetIDs, ok := node.Properties["subnet_ids"].([]string); ok {
			for _, subnetID := range subnetIDs {
				if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "subnet"}, req))
				}
			}
		}

		// 3. RDS → Security Group relationships (vpc_security_group_ids extracted by extractDatabaseMetadata)
		if sgIDs, ok := node.Properties["vpc_security_group_ids"].([]string); ok {
			for _, sgID := range sgIDs {
				if sgNode, exists := lookup.byResourceID[sgID]; exists {
					edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "security_group"}, req))
				}
			}
		}
	}

	return edges
}

// createElastiCacheEdges creates edges for ElastiCache clusters
func (s *AWSSource) createElastiCacheEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// TODO: Implement ElastiCache edge creation logic based on metadata
		// Will be implemented when metadata is provided
		_ = node
	}

	return edges
}

// createLoadBalancerEdges creates edges for Load Balancers (ALB/NLB/CLB)
// Uses metadata stored in cloud_resourses table (meta + tags columns) for edge creation.
// All input nodes are returned unchanged — no filtering occurs.
// Returns all LB nodes (identical to input), BackendPool nodes (for Target Groups), and edges.
func (s *AWSSource) createLoadBalancerEdges(_ *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbNode, []*core.DbEdge) {
	edges := make([]*core.DbEdge, 0)
	backendPoolNodes := make([]*core.DbNode, 0)

	for _, node := range nodes {
		// 1. LB → VPC edge
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				scheme, _ := node.Properties["scheme"].(string)
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc", "scheme": scheme}, req))
			}
		}

		// 2. LB → Subnet edges
		if subnets, ok := node.Properties["subnets"].([]interface{}); ok {
			for _, subnet := range subnets {
				if subnetID, ok := subnet.(string); ok && subnetID != "" {
					if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
						edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
							map[string]interface{}{"connection_type": "subnet"}, req))
					}
				}
			}
		}

		// 3. LB → Security Group edges
		if secGroups, ok := node.Properties["security_groups"].([]interface{}); ok {
			for _, sg := range secGroups {
				if sgID, ok := sg.(string); ok && sgID != "" {
					if sgNode, exists := lookup.byResourceID[sgID]; exists {
						edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
							map[string]interface{}{"connection_type": "security_group"}, req))
					}
				}
			}
		}

		// 4. Classic ELB: Instance edges (from meta.Instances stored in node properties)
		// Note: NLB/ALB target group edges (BackendPool nodes) require live AWS CLI access
		// because target groups are NOT embedded in the LB resource in DB meta.
		// They are kept on CLI via fetchTargetGroupsForLoadBalancer / fetchTargetHealthForTargetGroup.
		if instances, ok := node.Properties["instances"].([]interface{}); ok {
			for _, instRaw := range instances {
				instMap, ok := instRaw.(map[string]interface{})
				if !ok {
					continue
				}
				instanceID, _ := instMap["InstanceId"].(string)
				if instanceID == "" {
					continue
				}
				if instanceNode, exists := lookup.byResourceID[instanceID]; exists {
					edges = append(edges, s.createEdge(node, instanceNode, core.RelationshipRoutesTo,
						map[string]interface{}{"connection_type": "instance_target"}, req))
				}
			}
		}

		// 5. LB → EKS Cluster (via kubernetes tags stored in node.Properties["labels"])
		s.linkLoadBalancerToEKSCluster(node, map[string]interface{}{}, lookup, req, &edges)

		// 6. Extract K8s service info from tags for cross-source matching
		s.extractK8sServiceFromLBTags(node, map[string]interface{}{})
	}

	s.logger.Info("Created Load Balancer edges from metadata",
		"lb_count", len(nodes),
		"backend_pool_count", len(backendPoolNodes),
		"edges_created", len(edges))

	return nodes, backendPoolNodes, edges
}

// linkLoadBalancerToEKSCluster creates an edge from LoadBalancer to EKS cluster based on Kubernetes tags
func (s *AWSSource) linkLoadBalancerToEKSCluster(node *core.DbNode, meta map[string]interface{}, lookup *NodeLookup, req *core.SourceBuildRequest, edges *[]*core.DbEdge) {
	var clusterName string
	var ownership string // "owned" or "shared"

	// Try to get tags from node properties first (may have been stored from cloud_resources)
	tags, hasTags := node.Properties["labels"].(map[string]interface{})
	if !hasTags {
		// Try tags field
		tags, hasTags = node.Properties["tags"].(map[string]interface{})
	}

	if hasTags {
		// Check for elbv2.k8s.aws/cluster tag (set by AWS Load Balancer Controller)
		if cluster := extractTagStringValue(tags["elbv2.k8s.aws/cluster"]); cluster != "" {
			clusterName = cluster
			ownership = "owned"
		}

		// Check for eks:eks-cluster-name tag (set by EKS for NLBs)
		if clusterName == "" {
			if cluster := extractTagStringValue(tags["eks:eks-cluster-name"]); cluster != "" {
				clusterName = cluster
				ownership = "owned"
			}
		}

		// Check for kubernetes.io/cluster/{name} tags
		if clusterName == "" {
			for key, value := range tags {
				if strings.HasPrefix(key, "kubernetes.io/cluster/") {
					clusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
					ownership = extractTagStringValue(value)
					break
				}
			}
		}
	}

	// Also check metadata Tags array (from CLI fetch)
	if clusterName == "" {
		if metaTags, ok := meta["Tags"].([]interface{}); ok {
			for _, t := range metaTags {
				if tagMap, ok := t.(map[string]interface{}); ok {
					key, _ := tagMap["Key"].(string)
					value, _ := tagMap["Value"].(string)

					if key == "elbv2.k8s.aws/cluster" && value != "" {
						clusterName = value
						ownership = "owned"
						break
					}

					if strings.HasPrefix(key, "kubernetes.io/cluster/") {
						clusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
						ownership = value
						break
					}
				}
			}
		}
	}

	eksNodes, hasEKS := lookup.byNodeType[core.NodeTypeManagedCluster]
	if !hasEKS {
		return
	}

	if clusterName != "" {
		// Tag-based match (highest confidence)
		for _, eksNode := range eksNodes {
			eksName, _ := eksNode.Properties["name"].(string)
			if eksName == clusterName {
				*edges = append(*edges, s.createEdge(node, eksNode, core.RelationshipBelongsTo,
					map[string]interface{}{
						"connection_type": "kubernetes_cluster",
						"cluster_name":    clusterName,
						"ownership":       ownership,
					}, req))
				s.logger.Debug("created LoadBalancer -> EKS cluster edge via tags",
					"lb_name", node.Properties["name"],
					"cluster_name", clusterName,
					"ownership", ownership)
				return
			}
		}
		s.logger.Debug("LoadBalancer has Kubernetes tags but no matching EKS cluster found",
			"lb_name", node.Properties["name"],
			"cluster_name", clusterName)
		return
	}

	// No k8s tags — fallback: match by VPC co-location (low confidence).
	// If exactly one EKS cluster shares the same VPC as the LB, infer the relationship.
	lbVPCID, _ := node.Properties["vpc_id"].(string)
	if lbVPCID == "" {
		return
	}

	var matchedEKS *core.DbNode
	for _, eksNode := range eksNodes {
		// Only consider AWS EKS clusters
		if svc, _ := eksNode.Properties["service_name"].(string); svc != "AmazonEKS" {
			continue
		}
		eksVPCID, _ := eksNode.Properties["vpc_id"].(string)
		if eksVPCID != "" && eksVPCID == lbVPCID {
			if matchedEKS != nil {
				// Multiple EKS clusters in the same VPC — ambiguous, skip
				s.logger.Debug("Multiple EKS clusters in same VPC, skipping VPC-based LB → EKS inference",
					"lb_name", node.Properties["name"],
					"vpc_id", lbVPCID)
				return
			}
			matchedEKS = eksNode
		}
	}

	if matchedEKS != nil {
		eksName, _ := matchedEKS.Properties["name"].(string)
		*edges = append(*edges, s.createEdge(node, matchedEKS, core.RelationshipBelongsTo,
			map[string]interface{}{
				"connection_type": "vpc_inferred",
				"cluster_name":    eksName,
				"confidence":      "low",
			}, req))
		s.logger.Debug("created LoadBalancer -> EKS cluster edge via VPC co-location",
			"lb_name", node.Properties["name"],
			"cluster_name", eksName,
			"vpc_id", lbVPCID)
	}
}

// extractTagStringValue extracts a string value from a tag entry.
// Handles both plain string values and the array format used by the cloud_resourses.tags column
// (e.g. {"key": ["value"]} → "value", {"key": "value"} → "value").
func extractTagStringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
	}
	return ""
}

// extractK8sServiceFromLBTags extracts Kubernetes service info from LoadBalancer tags
// and stores k8s_service_name, k8s_service_namespace, and k8s_cluster_name properties for cross-source matching.
// AWS Load Balancer Controller sets:
// - kubernetes.io/service-name tag in format "namespace/service-name"
// - elbv2.k8s.aws/cluster or kubernetes.io/cluster/{cluster-name} tags for cluster identification
// EKS also sets:
// - eks:eks-cluster-name tag (cluster name)
// - service.eks.amazonaws.com/stack tag in format "namespace/service-name"
func (s *AWSSource) extractK8sServiceFromLBTags(node *core.DbNode, meta map[string]interface{}) {
	var k8sServiceName, k8sNamespace, k8sClusterName string

	// Try to get tags from node properties first (may have been stored from cloud_resources)
	tags, hasTags := node.Properties["labels"].(map[string]interface{})
	if !hasTags {
		tags, hasTags = node.Properties["tags"].(map[string]interface{})
	}

	if hasTags {
		// Check for kubernetes.io/service-name tag (format: "namespace/service-name")
		if serviceName := extractTagStringValue(tags["kubernetes.io/service-name"]); serviceName != "" {
			parts := strings.Split(serviceName, "/")
			if len(parts) == 2 {
				k8sNamespace = parts[0]
				k8sServiceName = parts[1]
			}
		}

		// Check for service.eks.amazonaws.com/stack tag (EKS NLB format: "namespace/service-name")
		if k8sServiceName == "" {
			if stack := extractTagStringValue(tags["service.eks.amazonaws.com/stack"]); stack != "" {
				parts := strings.Split(stack, "/")
				if len(parts) == 2 {
					k8sNamespace = parts[0]
					k8sServiceName = parts[1]
				}
			}
		}

		// Check for cluster name from elbv2.k8s.aws/cluster tag
		if cluster := extractTagStringValue(tags["elbv2.k8s.aws/cluster"]); cluster != "" {
			k8sClusterName = cluster
		}

		// Check for cluster name from eks:eks-cluster-name tag (EKS NLB format)
		if k8sClusterName == "" {
			if cluster := extractTagStringValue(tags["eks:eks-cluster-name"]); cluster != "" {
				k8sClusterName = cluster
			}
		}

		// Check for cluster name from kubernetes.io/cluster/{name} tags
		if k8sClusterName == "" {
			for key := range tags {
				if strings.HasPrefix(key, "kubernetes.io/cluster/") {
					k8sClusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
					break
				}
			}
		}
	}

	// Also check metadata Tags array (from CLI fetch)
	if metaTags, ok := meta["Tags"].([]interface{}); ok {
		for _, t := range metaTags {
			if tagMap, ok := t.(map[string]interface{}); ok {
				key, _ := tagMap["Key"].(string)
				value, _ := tagMap["Value"].(string)

				// Extract service name if not already found
				if k8sServiceName == "" && key == "kubernetes.io/service-name" && value != "" {
					parts := strings.Split(value, "/")
					if len(parts) == 2 {
						k8sNamespace = parts[0]
						k8sServiceName = parts[1]
					}
				}

				// Extract cluster name if not already found
				if k8sClusterName == "" {
					if key == "elbv2.k8s.aws/cluster" && value != "" {
						k8sClusterName = value
					} else if strings.HasPrefix(key, "kubernetes.io/cluster/") {
						k8sClusterName = strings.TrimPrefix(key, "kubernetes.io/cluster/")
					}
				}
			}
		}
	}

	if k8sServiceName != "" {
		node.Properties["k8s_service_name"] = k8sServiceName
		node.Properties["k8s_service_namespace"] = k8sNamespace
		if k8sClusterName != "" {
			node.Properties["k8s_cluster_name"] = k8sClusterName
		}
		s.logger.Debug("extracted K8s service info from LoadBalancer tags",
			"lb_name", node.Properties["name"],
			"k8s_service_name", k8sServiceName,
			"k8s_namespace", k8sNamespace,
			"k8s_cluster_name", k8sClusterName)
	}
}

// createLambdaEdges creates edges for Lambda functions
func (s *AWSSource) createLambdaEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// 1. Handle Lambda event source mappings: Lambda <- SQS, Lambda <- DynamoDB Streams, etc.
		// EventSourceMappings structure: [{"EventSourceArn": "arn:aws:sqs:...", "State": "Enabled"}]
		if eventSourceMappings, ok := meta["EventSourceMappings"].([]interface{}); ok {
			for _, mapping := range eventSourceMappings {
				if mappingMap, ok := mapping.(map[string]interface{}); ok {
					eventSourceArn, _ := mappingMap["EventSourceArn"].(string)
					state, _ := mappingMap["State"].(string)

					// Only create edges for enabled event source mappings
					if eventSourceArn != "" && state == "Enabled" {
						// Look up source by ARN (could be SQS, DynamoDB, Kinesis, etc.)
						if sourceNode, exists := lookup.byARN[eventSourceArn]; exists {
							batchSize := ""
							if size, ok := mappingMap["BatchSize"].(float64); ok {
								batchSize = fmt.Sprintf("%.0f", size)
							}
							edges = append(edges, s.createEdge(sourceNode, node, core.RelationshipPublishesTo,
								map[string]interface{}{
									"connection_type": "event_source_mapping",
									"batch_size":      batchSize,
									"state":           state,
								}, req))
							s.logger.Debug("created event source to Lambda edge",
								"source", sourceNode.Properties["name"],
								"lambda_function", node.Properties["name"],
								"state", state)
						} else {
							s.logger.Warn("Lambda event source ARN not found in lookup; edge skipped",
								"function_name", node.Properties["name"],
								"event_source_arn", eventSourceArn)
						}
					}
				}
			}
		}

		// 2. Lambda VPC Configuration: Lambda → VPC, Subnet, Security Groups
		// VpcConfig structure: {"VpcId": "vpc-123", "SubnetIds": ["subnet-1"], "SecurityGroupIds": ["sg-1"]}
		if vpcConfig, ok := meta["VpcConfig"].(map[string]interface{}); ok {
			// Lambda → VPC (HOSTED_ON)
			if vpcID, ok := vpcConfig["VpcId"].(string); ok && vpcID != "" {
				if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "vpc",
							"vpc_enabled":     true,
						}, req))
					s.logger.Debug("created Lambda -> VPC edge",
						"lambda", node.Properties["name"],
						"vpc_id", vpcID)
				}
			}

			// Lambda → Subnets (HOSTED_ON)
			if subnetIds, ok := vpcConfig["SubnetIds"].([]interface{}); ok {
				for _, sid := range subnetIds {
					if subnetID, ok := sid.(string); ok && subnetID != "" {
						if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
							edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "subnet",
								}, req))
						}
					}
				}
			}

			// Lambda → Security Groups (HOSTED_ON)
			if sgIds, ok := vpcConfig["SecurityGroupIds"].([]interface{}); ok {
				for _, sid := range sgIds {
					if sgID, ok := sid.(string); ok && sgID != "" {
						if sgNode, exists := lookup.byResourceID[sgID]; exists {
							edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "security_group",
								}, req))
						}
					}
				}
			}
		}
	}

	return edges
}

// createSNSEdges creates edges for SNS topics (Topic -> MessageQueue, ServerlessFunction, etc.)
// Fetches ALL subscription data from AWS in one call for efficiency
func (s *AWSSource) createSNSEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Fetch ALL SNS subscriptions at once (much more efficient than per-topic calls)
	allSubscriptionsMap, err := s.fetchAllSNSSubscriptions(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Warn("failed to fetch all SNS subscriptions from AWS, falling back to metadata for all topics",
			"error", err)
		// Continue with metadata fallback for each topic below
	}

	for _, node := range nodes {
		// Get topic ARN from node properties
		topicArn, hasArn := node.Properties["arn"].(string)
		if !hasArn || topicArn == "" {
			s.logger.Debug("SNS topic missing ARN, skipping",
				"topic_name", node.Properties["name"])
			continue
		}

		// Get subscriptions for this topic from the batch fetch
		var subscriptions []interface{}
		if allSubscriptionsMap != nil {
			if topicSubs, exists := allSubscriptionsMap[topicArn]; exists {
				subscriptions = topicSubs
				s.logger.Debug("Using batch-fetched subscriptions for SNS topic",
					"topic_name", node.Properties["name"],
					"subscription_count", len(subscriptions))
			}
		}

		// Fallback to metadata if no subscriptions found from AWS
		if len(subscriptions) == 0 {
			meta, hasMeta := getMetadataMap(node)
			if hasMeta {
				if metaSubscriptions, ok := meta["Subscriptions"].([]interface{}); ok {
					subscriptions = metaSubscriptions
					s.logger.Debug("Using metadata subscriptions for SNS topic",
						"topic_name", node.Properties["name"],
						"subscription_count", len(subscriptions))
				}
			}
		}

		// Process subscriptions to create edges
		for _, sub := range subscriptions {
			if subMap, ok := sub.(map[string]interface{}); ok {
				protocol, _ := subMap["Protocol"].(string)
				endpoint, _ := subMap["Endpoint"].(string)
				subscriptionArn, _ := subMap["SubscriptionArn"].(string)

				// Skip pending confirmations
				if subscriptionArn == "PendingConfirmation" {
					continue
				}

				// Handle SQS subscriptions: SNS -> SQS
				if protocol == "sqs" && endpoint != "" {
					// Look up SQS queue by ARN (endpoint is the queue ARN)
					if sqsNode, exists := lookup.byARN[endpoint]; exists {
						edges = append(edges, s.createEdge(node, sqsNode, core.RelationshipPublishesTo,
							map[string]interface{}{
								"connection_type":  "sns_subscription",
								"protocol":         protocol,
								"subscription_arn": subscriptionArn,
							}, req))
						s.logger.Debug("created SNS to SQS edge from AWS data",
							"sns_topic", node.Properties["name"],
							"sqs_queue", sqsNode.Properties["name"],
							"protocol", protocol)
					} else {
						s.logger.Debug("SQS queue not found in lookup for SNS subscription",
							"sns_topic", node.Properties["name"],
							"queue_arn", endpoint)
					}
				}

				// Handle Lambda subscriptions: SNS -> Lambda
				if protocol == "lambda" && endpoint != "" {
					// Look up Lambda function by ARN (endpoint is the function ARN)
					if lambdaNode, exists := lookup.byARN[endpoint]; exists {
						edges = append(edges, s.createEdge(node, lambdaNode, core.RelationshipPublishesTo,
							map[string]interface{}{
								"connection_type":  "sns_subscription",
								"protocol":         protocol,
								"subscription_arn": subscriptionArn,
							}, req))
						s.logger.Debug("created SNS to Lambda edge from AWS data",
							"sns_topic", node.Properties["name"],
							"lambda_function", lambdaNode.Properties["name"],
							"protocol", protocol)
					} else {
						s.logger.Debug("Lambda function not found in lookup for SNS subscription",
							"sns_topic", node.Properties["name"],
							"function_arn", endpoint)
					}
				}

				// Handle HTTP/HTTPS subscriptions (could be API Gateway or other endpoints)
				if (protocol == "http" || protocol == "https") && endpoint != "" {
					s.logger.Debug("SNS HTTP/HTTPS subscription detected",
						"sns_topic", node.Properties["name"],
						"endpoint", endpoint,
						"protocol", protocol)
					// Note: HTTP endpoints are not currently modeled as nodes in the knowledge graph
				}

				// Handle email subscriptions
				if (protocol == "email" || protocol == "email-json") && endpoint != "" {
					s.logger.Debug("SNS email subscription detected",
						"sns_topic", node.Properties["name"],
						"email", endpoint,
						"protocol", protocol)
					// Note: Email endpoints are not currently modeled as nodes in the knowledge graph
				}
			}
		}
	}

	return edges
}

// createSQSEdges creates edges for SQS queues (MessageQueue -> DLQ, receive from S3, etc.)
func (s *AWSSource) createSQSEdges(_ *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// 1. Handle Dead Letter Queue (DLQ) relationships: SQS -> DLQ
		// dead_letter_target_arn is pre-extracted by extractQueueMetadata from RedrivePolicy.
		// Raw meta is not stored in node.Properties, so we read the flattened field directly.
		if dlqArn, ok := node.Properties["dead_letter_target_arn"].(string); ok && dlqArn != "" {
			if dlqNode, exists := lookup.byARN[dlqArn]; exists {
				edges = append(edges, s.createEdge(node, dlqNode, core.RelationshipRoutesTo,
					map[string]interface{}{
						"connection_type": "dead_letter_queue",
					}, req))
				s.logger.Debug("created SQS to DLQ edge",
					"source_queue", node.Properties["name"],
					"dlq", dlqNode.Properties["name"])
			} else {
				s.logger.Debug("DLQ not found in lookup for SQS queue",
					"queue", node.Properties["name"],
					"dlq_arn", dlqArn)
			}
		}

		// Note: Lambda event source mappings (Lambda -> SQS) are handled in createLambdaEdges
		// S3 event notifications (S3 -> SQS) are handled in createS3Edges
	}

	return edges
}

// createS3Edges creates edges for S3 buckets (Storage -> SNS/SQS/Lambda for event notifications)
func (s *AWSSource) createS3Edges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// Handle S3 event notifications: S3 -> SNS/SQS/Lambda
		// NotificationConfiguration structure: {
		//   "TopicConfigurations": [{"TopicArn": "arn:aws:sns:...", "Events": ["s3:ObjectCreated:*"]}],
		//   "QueueConfigurations": [{"QueueArn": "arn:aws:sqs:...", "Events": ["s3:ObjectCreated:*"]}],
		//   "LambdaFunctionConfigurations": [{"LambdaFunctionArn": "arn:aws:lambda:...", "Events": ["s3:ObjectCreated:*"]}]
		// }
		if notificationConfig, ok := meta["NotificationConfiguration"].(map[string]interface{}); ok {
			// 1. S3 -> SNS topic configurations
			if topicConfigs, ok := notificationConfig["TopicConfigurations"].([]interface{}); ok {
				for _, config := range topicConfigs {
					if configMap, ok := config.(map[string]interface{}); ok {
						topicArn, _ := configMap["TopicArn"].(string)
						if topicArn != "" {
							if snsNode, exists := lookup.byARN[topicArn]; exists {
								events := ""
								if eventsList, ok := configMap["Events"].([]interface{}); ok && len(eventsList) > 0 {
									events = fmt.Sprintf("%v", eventsList[0])
								}
								edges = append(edges, s.createEdge(node, snsNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "s3_event_notification",
										"events":          events,
									}, req))
								s.logger.Debug("created S3 to SNS edge",
									"s3_bucket", node.Properties["name"],
									"sns_topic", snsNode.Properties["name"],
									"events", events)
							}
						}
					}
				}
			}

			// 2. S3 -> SQS queue configurations
			if queueConfigs, ok := notificationConfig["QueueConfigurations"].([]interface{}); ok {
				for _, config := range queueConfigs {
					if configMap, ok := config.(map[string]interface{}); ok {
						queueArn, _ := configMap["QueueArn"].(string)
						if queueArn != "" {
							if sqsNode, exists := lookup.byARN[queueArn]; exists {
								events := ""
								if eventsList, ok := configMap["Events"].([]interface{}); ok && len(eventsList) > 0 {
									events = fmt.Sprintf("%v", eventsList[0])
								}
								edges = append(edges, s.createEdge(node, sqsNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "s3_event_notification",
										"events":          events,
									}, req))
								s.logger.Debug("created S3 to SQS edge",
									"s3_bucket", node.Properties["name"],
									"sqs_queue", sqsNode.Properties["name"],
									"events", events)
							}
						}
					}
				}
			}

			// 3. S3 -> Lambda function configurations
			if lambdaConfigs, ok := notificationConfig["LambdaFunctionConfigurations"].([]interface{}); ok {
				for _, config := range lambdaConfigs {
					if configMap, ok := config.(map[string]interface{}); ok {
						lambdaArn, _ := configMap["LambdaFunctionArn"].(string)
						if lambdaArn != "" {
							if lambdaNode, exists := lookup.byARN[lambdaArn]; exists {
								events := ""
								if eventsList, ok := configMap["Events"].([]interface{}); ok && len(eventsList) > 0 {
									events = fmt.Sprintf("%v", eventsList[0])
								}
								edges = append(edges, s.createEdge(node, lambdaNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "s3_event_notification",
										"events":          events,
									}, req))
								s.logger.Debug("created S3 to Lambda edge",
									"s3_bucket", node.Properties["name"],
									"lambda_function", lambdaNode.Properties["name"],
									"events", events)
							}
						}
					}
				}
			}
		}
	}

	return edges
}

// createEKSEdges creates edges for EKS clusters
func (s *AWSSource) createEKSEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// Parse ResourcesVpcConfig which contains VPC, subnet, and security group information
		if resourcesVpcConfig, ok := meta["ResourcesVpcConfig"].(map[string]interface{}); ok {
			// 1. EKS → VPC relationship
			if vpcID, ok := resourcesVpcConfig["VpcId"].(string); ok && vpcID != "" {
				if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
					// Extract additional VPC config details for edge properties
					endpointPublicAccess, _ := resourcesVpcConfig["EndpointPublicAccess"].(bool)
					endpointPrivateAccess, _ := resourcesVpcConfig["EndpointPrivateAccess"].(bool)

					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type":         "vpc",
							"endpoint_public_access":  endpointPublicAccess,
							"endpoint_private_access": endpointPrivateAccess,
						}, req))
				}
			}

			// 2. EKS → Subnet relationships (EKS clusters span multiple subnets for HA)
			if subnetIds, ok := resourcesVpcConfig["SubnetIds"].([]interface{}); ok {
				for _, subnetID := range subnetIds {
					if subnetIDStr, ok := subnetID.(string); ok && subnetIDStr != "" {
						if subnetNode, exists := lookup.byResourceID[subnetIDStr]; exists {
							edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "subnet",
								}, req))
						}
					}
				}
			}

			// 3. EKS → Security Group relationships
			// Collect all security group IDs (both SecurityGroupIds array and ClusterSecurityGroupId)
			securityGroupIDs := make([]string, 0)

			// Add SecurityGroupIds array
			if sgIds, ok := resourcesVpcConfig["SecurityGroupIds"].([]interface{}); ok {
				for _, sgID := range sgIds {
					if sgIDStr, ok := sgID.(string); ok && sgIDStr != "" {
						securityGroupIDs = append(securityGroupIDs, sgIDStr)
					}
				}
			}

			// Add ClusterSecurityGroupId
			if clusterSGID, ok := resourcesVpcConfig["ClusterSecurityGroupId"].(string); ok && clusterSGID != "" {
				securityGroupIDs = append(securityGroupIDs, clusterSGID)
			}

			// Create edges for all security groups
			for _, sgID := range securityGroupIDs {
				if sgNode, exists := lookup.byResourceID[sgID]; exists {
					// Determine if this is the cluster security group
					isClusterSG := false
					if clusterSGID, ok := resourcesVpcConfig["ClusterSecurityGroupId"].(string); ok {
						isClusterSG = (sgID == clusterSGID)
					}

					edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "security_group",
							"cluster_sg":      isClusterSG,
						}, req))
				}
			}
		}
	}

	return edges
}

// createECSServiceEdges creates edges for ECS Services connecting to ECS Clusters
// ECS Service ARN format: arn:aws:ecs:{region}:{account}:service/{cluster-name}/{service-name}
func (s *AWSSource) createECSServiceEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Only process ECS services (service_name = AmazonECS)
		serviceName, ok := getStringProperty(node, "service_name")
		if !ok || serviceName != "AmazonECS" {
			continue
		}

		// Get the ARN to extract cluster name
		// ECS Service ARN format: arn:aws:ecs:{region}:{account}:service/{cluster-name}/{service-name}
		arn, ok := getStringProperty(node, "arn")
		if !ok || arn == "" {
			// Try external_resource_id as fallback
			arn, ok = getStringProperty(node, "external_resource_id")
			if !ok || arn == "" {
				continue
			}
		}

		// Parse cluster name from ARN
		// Example: arn:aws:ecs:<region>:<aws-account-id>:service/<cluster-name>/<service-name>
		clusterName := extractECSClusterNameFromServiceARN(arn)
		if clusterName == "" {
			continue
		}

		// Find the ECS cluster node by name
		clusterFound := false
		for _, clusterNode := range lookup.byNodeType[core.NodeTypeManagedCluster] {
			// Check if this is an ECS cluster (not EKS)
			clusterServiceName, ok := getStringProperty(clusterNode, "service_name")
			if !ok || clusterServiceName != "AmazonECS" {
				continue
			}

			// Match by cluster name
			clusterNodeName, ok := getStringProperty(clusterNode, "name")
			if !ok {
				continue
			}

			if clusterNodeName == clusterName {
				// Create ECS Service → ECS Cluster relationship
				clusterFound = true
				edges = append(edges, s.createEdge(node, clusterNode, core.RelationshipRunsIn,
					map[string]interface{}{
						"connection_type": "ecs_cluster",
						"cluster_name":    clusterName,
					}, req))
				break
			}
		}
		if !clusterFound {
			s.logger.Warn("ECS service cluster node not found; edge skipped",
				"service_name", node.Properties["name"],
				"cluster_name", clusterName,
				"arn", arn)
		}
	}

	return edges
}

// extractECSClusterNameFromServiceARN extracts the cluster name from an ECS service ARN
// ARN formats:
//   - With explicit cluster: arn:aws:ecs:{region}:{account}:service/{cluster-name}/{service-name}
//   - Default cluster: arn:aws:ecs:{region}:{account}:service/{service-name}
func extractECSClusterNameFromServiceARN(arn string) string {
	// Look for the pattern "service/{cluster-name}/{service-name}" or "service/{service-name}"
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return ""
	}

	// The last part contains "service/{cluster-name}/{service-name}" or "service/{service-name}"
	resourcePart := parts[len(parts)-1]
	if !strings.HasPrefix(resourcePart, "service/") {
		return ""
	}

	// Remove "service/" prefix and split by "/"
	resourcePath := strings.TrimPrefix(resourcePart, "service/")
	pathParts := strings.Split(resourcePath, "/")

	// If only one part, it's the service name and the cluster is "default"
	if len(pathParts) == 1 {
		return "default"
	}

	// First part is the cluster name
	return pathParts[0]
}

// createSecurityGroupEdges creates edges for Security Groups
func (s *AWSSource) createSecurityGroupEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			// No metadata, try basic VPC connection
			if vpcID, ok := getStringProperty(node, "vpc_id"); ok {
				if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "vpc"}, req))
				}
			}
			continue
		}

		// 1. SecurityGroup → VPC relationship
		if vpcID, ok := meta["VpcId"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "vpc",
					}, req))
			}
		}

		// 2. SecurityGroup → Other SecurityGroup relationships (from ingress rules)
		if ipPermissions, ok := meta["IpPermissions"].([]interface{}); ok {
			for _, permission := range ipPermissions {
				if permMap, ok := permission.(map[string]interface{}); ok {
					// Extract port and protocol info for edge properties
					protocol, _ := permMap["IpProtocol"].(string)
					fromPort, _ := permMap["FromPort"].(float64)
					toPort, _ := permMap["ToPort"].(float64)

					// Parse UserIdGroupPairs for security group references
					if userIdGroupPairs, ok := permMap["UserIdGroupPairs"].([]interface{}); ok {
						for _, pair := range userIdGroupPairs {
							if pairMap, ok := pair.(map[string]interface{}); ok {
								if referencedSGID, ok := pairMap["GroupId"].(string); ok && referencedSGID != "" {
									if referencedSGNode, exists := lookup.byResourceID[referencedSGID]; exists {
										edges = append(edges, s.createEdge(node, referencedSGNode, core.RelationshipHostedOn,
											map[string]interface{}{
												"connection_type": "security_group_rule",
												"rule_type":       "ingress",
												"protocol":        protocol,
												"from_port":       int(fromPort),
												"to_port":         int(toPort),
											}, req))
									}
								}
							}
						}
					}
				}
			}
		}

		// 3. SecurityGroup → Other SecurityGroup relationships (from egress rules)
		if ipPermissionsEgress, ok := meta["IpPermissionsEgress"].([]interface{}); ok {
			for _, permission := range ipPermissionsEgress {
				if permMap, ok := permission.(map[string]interface{}); ok {
					// Extract port and protocol info for edge properties
					protocol, _ := permMap["IpProtocol"].(string)
					fromPort, _ := permMap["FromPort"].(float64)
					toPort, _ := permMap["ToPort"].(float64)

					// Parse UserIdGroupPairs for security group references
					if userIdGroupPairs, ok := permMap["UserIdGroupPairs"].([]interface{}); ok {
						for _, pair := range userIdGroupPairs {
							if pairMap, ok := pair.(map[string]interface{}); ok {
								if referencedSGID, ok := pairMap["GroupId"].(string); ok && referencedSGID != "" {
									if referencedSGNode, exists := lookup.byResourceID[referencedSGID]; exists {
										edges = append(edges, s.createEdge(node, referencedSGNode, core.RelationshipHostedOn,
											map[string]interface{}{
												"connection_type": "security_group_rule",
												"rule_type":       "egress",
												"protocol":        protocol,
												"from_port":       int(fromPort),
												"to_port":         int(toPort),
											}, req))
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return edges
}

// createRouteTableEdges creates edges for Route Tables
// Handles VPC association, subnet associations, and route destinations (NAT GW, VPC Endpoints)
func (s *AWSSource) createRouteTableEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// 1. RouteTable → VPC (BELONGS_TO)
		if vpcID, ok := meta["VpcId"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. RouteTable → Subnet associations (ASSOCIATED_WITH)
		if associations, ok := meta["Associations"].([]interface{}); ok {
			for _, assoc := range associations {
				if assocMap, ok := assoc.(map[string]interface{}); ok {
					// Skip main route table associations (no subnet ID)
					if subnetID, ok := assocMap["SubnetId"].(string); ok && subnetID != "" {
						if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
							isMain, _ := assocMap["Main"].(bool)
							edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipAssociatedWith,
								map[string]interface{}{
									"connection_type": "subnet_association",
									"is_main":         isMain,
								}, req))
						}
					}
				}
			}
		}

		// 3. Routes to gateway destinations (ROUTES_THROUGH)
		if routes, ok := meta["Routes"].([]interface{}); ok {
			for _, route := range routes {
				if routeMap, ok := route.(map[string]interface{}); ok {
					destCidr, _ := routeMap["DestinationCidrBlock"].(string)
					if destCidr == "" {
						// Try IPv6 destination
						destCidr, _ = routeMap["DestinationIpv6CidrBlock"].(string)
					}

					// NAT Gateway route
					if natGwID, ok := routeMap["NatGatewayId"].(string); ok && natGwID != "" {
						if natNode, exists := lookup.byResourceID[natGwID]; exists {
							edges = append(edges, s.createEdge(node, natNode, core.RelationshipRoutesThrough,
								map[string]interface{}{
									"destination_cidr": destCidr,
									"route_type":       "nat_gateway",
								}, req))
						}
					}

					// Gateway ID can be: Internet Gateway (igw-*), VPC Endpoint (vpce-*), Virtual Private Gateway (vgw-*)
					if gwID, ok := routeMap["GatewayId"].(string); ok && gwID != "" {
						// VPC Endpoint (Gateway type)
						if strings.HasPrefix(gwID, "vpce-") {
							if vpcEndpointNode, exists := lookup.byResourceID[gwID]; exists {
								edges = append(edges, s.createEdge(node, vpcEndpointNode, core.RelationshipRoutesThrough,
									map[string]interface{}{
										"destination_cidr": destCidr,
										"route_type":       "vpc_endpoint",
									}, req))
							}
						}
						// Internet Gateway routes are logged but not linked (IGW nodes not typically created)
						// Virtual Private Gateway (vgw-*) for VPN connections - future enhancement
					}

					// Transit Gateway route
					if tgwID, ok := routeMap["TransitGatewayId"].(string); ok && tgwID != "" {
						s.logger.Debug("Route to Transit Gateway found",
							"route_table", node.Properties["name"],
							"tgw_id", tgwID,
							"destination", destCidr)
					}

					// VPC Peering connection route
					if peeringID, ok := routeMap["VpcPeeringConnectionId"].(string); ok && peeringID != "" {
						s.logger.Debug("Route to VPC Peering found",
							"route_table", node.Properties["name"],
							"peering_id", peeringID,
							"destination", destCidr)
					}
				}
			}
		}
	}

	return edges
}

// createAPIGatewayEdges creates edges for API Gateway resources
// Handles Lambda integrations and VPC endpoint connections for private APIs
func (s *AWSSource) createAPIGatewayEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// 1. APIGateway → Lambda integrations (ROUTES_TO)
		// Integrations structure: [{"IntegrationType": "AWS_PROXY", "IntegrationUri": "arn:aws:apigateway:...lambda.../functions/{arn}/invocations"}]
		if integrations, ok := meta["Integrations"].([]interface{}); ok {
			for _, integration := range integrations {
				if intMap, ok := integration.(map[string]interface{}); ok {
					intType, _ := intMap["IntegrationType"].(string)
					uri, _ := intMap["IntegrationUri"].(string)

					// Lambda integration (AWS_PROXY or AWS)
					if (intType == "AWS_PROXY" || intType == "AWS") && uri != "" && strings.Contains(uri, ":lambda:") {
						// Extract Lambda ARN from integration URI
						// Format: arn:aws:apigateway:{region}:lambda:path/2015-03-31/functions/{lambda-arn}/invocations
						lambdaArn := extractLambdaArnFromIntegration(uri)
						if lambdaArn != "" {
							if lambdaNode, exists := lookup.byARN[lambdaArn]; exists {
								httpMethod, _ := intMap["HttpMethod"].(string)
								resourcePath, _ := intMap["ResourcePath"].(string)

								edges = append(edges, s.createEdge(node, lambdaNode, core.RelationshipRoutesTo,
									map[string]interface{}{
										"connection_type":  "lambda_integration",
										"integration_type": intType,
										"http_method":      httpMethod,
										"resource_path":    resourcePath,
									}, req))
								s.logger.Debug("created APIGateway -> Lambda edge",
									"api", node.Properties["name"],
									"lambda_arn", lambdaArn,
									"method", httpMethod)
							}
						}
					}

					// HTTP integration (backend services) - log for visibility
					if (intType == "HTTP" || intType == "HTTP_PROXY") && uri != "" {
						s.logger.Debug("API Gateway HTTP integration detected",
							"api", node.Properties["name"],
							"endpoint", uri,
							"type", intType)
					}
				}
			}
		}

		// 2. Private API Gateway → VPC Endpoint (HOSTED_ON)
		// EndpointConfiguration: {"Types": ["PRIVATE"], "VpcEndpointIds": ["vpce-..."]}
		if endpointConfig, ok := meta["EndpointConfiguration"].(map[string]interface{}); ok {
			if types, ok := endpointConfig["Types"].([]interface{}); ok {
				isPrivate := false
				for _, t := range types {
					if typeStr, ok := t.(string); ok && typeStr == "PRIVATE" {
						isPrivate = true
						break
					}
				}

				if isPrivate {
					if vpcEndpointIds, ok := endpointConfig["VpcEndpointIds"].([]interface{}); ok {
						for _, vpceid := range vpcEndpointIds {
							if id, ok := vpceid.(string); ok && id != "" {
								if vpcEndpointNode, exists := lookup.byResourceID[id]; exists {
									edges = append(edges, s.createEdge(node, vpcEndpointNode, core.RelationshipHostedOn,
										map[string]interface{}{
											"connection_type": "private_api",
										}, req))
								}
							}
						}
					}
				}
			}
		}
	}

	return edges
}

// extractLambdaArnFromIntegration extracts the Lambda function ARN from an API Gateway integration URI
// URI format: arn:aws:apigateway:{region}:lambda:path/2015-03-31/functions/{lambda-arn}/invocations
func extractLambdaArnFromIntegration(uri string) string {
	if idx := strings.Index(uri, "/functions/"); idx != -1 {
		rest := uri[idx+11:] // len("/functions/") = 11
		if endIdx := strings.Index(rest, "/invocations"); endIdx != -1 {
			return rest[:endIdx]
		}
	}
	return ""
}

// createNATGatewayEdges creates edges for NAT Gateways
// Fetches NAT Gateway metadata from AWS CLI if not present in database
func (s *AWSSource) createNATGatewayEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check if we have required properties for edge creation
		vpcID, _ := node.Properties["vpc_id"].(string)
		subnetID, _ := node.Properties["subnet_id"].(string)
		natGatewayID, _ := node.Properties["nat_gateway_id"].(string)
		networkInterfaceID, _ := node.Properties["network_interface_id"].(string)

		// If properties are missing, try to fetch from AWS CLI.
		// Include networkInterfaceID in the check: NatGatewayAddresses (which contains
		// NetworkInterfaceId) may not be stored in DB meta even when vpc/subnet/nat ids are present.
		if (vpcID == "" || subnetID == "" || natGatewayID == "" || networkInterfaceID == "") && req.CloudAccountID != "" {
			// Get NAT Gateway ID from resource_id or existing property
			if natGatewayID == "" {
				if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID != "" {
					natGatewayID = resourceID
				} else if name, ok := node.Properties["name"].(string); ok && name != "" {
					natGatewayID = name
				}
			}

			if natGatewayID != "" {
				s.logger.Info("NAT Gateway properties missing, fetching from AWS",
					"node_id", node.ID,
					"nat_gateway_id", natGatewayID)

				// Fetch from AWS CLI
				natData, err := s.fetchNATGatewayDataFromAWS(reqCtx, req, req.CloudAccountID, natGatewayID)
				if err != nil {
					s.logger.Error("Failed to fetch NAT Gateway data from AWS",
						"nat_gateway_id", natGatewayID,
						"error", err)
				} else {
					// Create temp meta map for extraction
					tempMeta := map[string]interface{}{
						"NatGatewayId":        natData.NatGatewayId,
						"State":               natData.State,
						"SubnetId":            natData.SubnetId,
						"VpcId":               natData.VpcId,
						"CreateTime":          natData.CreateTime,
						"ConnectivityType":    natData.ConnectivityType,
						"NatGatewayAddresses": natData.NatGatewayAddresses,
					}

					// Extract fields to properties (without storing meta)
					s.extractNetworkGatewayMetadata(node.Properties, tempMeta)

					// Update local variables used in edge creation below
					vpcID, _ = node.Properties["vpc_id"].(string)
					subnetID, _ = node.Properties["subnet_id"].(string)
					// networkInterfaceID is re-read from node.Properties directly at edge 3, no update needed

					s.logger.Info("Successfully enriched NAT Gateway node with AWS CLI data",
						"nat_gateway_id", natGatewayID,
						"state", natData.State)
				}
			}
		}

		// 1. NAT Gateway → VPC relationship
		if vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			} else {
				s.logger.Warn("NAT Gateway VPC not found in lookup", "nat_id", node.Properties["resource_id"], "vpc_id", vpcID)
			}
		}

		// 2. NAT Gateway → Subnet relationship
		if subnetID != "" {
			if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
				edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			} else {
				s.logger.Warn("NAT Gateway Subnet not found in lookup", "nat_id", node.Properties["resource_id"], "subnet_id", subnetID)
			}
		}

		// 3. NAT Gateway → ENI (Network Interface) relationship
		if networkInterfaceID, ok := node.Properties["network_interface_id"].(string); ok && networkInterfaceID != "" {
			if eniNode, exists := lookup.byResourceID[networkInterfaceID]; exists {
				edges = append(edges, s.createEdge(node, eniNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "network_interface",
						"interface_id":    networkInterfaceID,
					}, req))
			}
		}

		// 4. NAT Gateway → Elastic IP relationship
		if allocationID, ok := node.Properties["allocation_id"].(string); ok && allocationID != "" {
			// Check if EIP node exists in lookup (elastic-ip type)
			if eipNode, exists := lookup.byResourceID[allocationID]; exists {
				edges = append(edges, s.createEdge(node, eipNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "elastic_ip",
						"allocation_id":   allocationID,
					}, req))
			} else {
				s.logger.Warn("NAT Gateway EIP not found in lookup", "nat_id", node.Properties["resource_id"], "allocation_id", allocationID)
			}
		}
	}

	return edges
}

// createPrivateEndpointEdges creates edges for VPC Endpoints (Private Endpoints)
// Handles both Interface and Gateway endpoint types
func (s *AWSSource) createPrivateEndpointEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Get VPC Endpoint ID
		vpcEndpointID, _ := node.Properties["resource_id"].(string)
		if vpcEndpointID == "" {
			if name, ok := node.Properties["name"].(string); ok {
				vpcEndpointID = name
			}
		}

		// Fetch VPC Endpoint details from AWS CLI
		var endpointData *PrivateEndpointData
		if vpcEndpointID != "" && req.CloudAccountID != "" {
			s.logger.Info("Fetching VPC Endpoint details from AWS",
				"vpc_endpoint_id", vpcEndpointID,
				"account_id", req.CloudAccountID)

			data, err := s.fetchPrivateEndpointDataFromAWS(reqCtx, req, req.CloudAccountID, vpcEndpointID)
			if err != nil {
				s.logger.Error("Failed to fetch VPC Endpoint data from AWS",
					"vpc_endpoint_id", vpcEndpointID,
					"error", err)
			} else {
				endpointData = data
				// Store useful properties in the node
				node.Properties["vpc_id"] = data.VpcId
				node.Properties["vpc_endpoint_type"] = data.VpcEndpointType
				node.Properties["target_service_name"] = data.ServiceName
				node.Properties["private_dns_enabled"] = data.PrivateDnsEnabled
				node.Properties["state"] = data.State

				s.logger.Info("Successfully enriched VPC Endpoint node with AWS CLI data",
					"vpc_endpoint_id", vpcEndpointID,
					"vpc_endpoint_type", data.VpcEndpointType,
					"target_service_name", data.ServiceName)
			}
		}

		if endpointData == nil {
			// Try to get VPC ID from existing properties
			vpcID, _ := node.Properties["vpc_id"].(string)
			if vpcID != "" {
				// Create VPC edge with just VPC ID
				if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "vpc"}, req))
				}
			}
			continue
		}

		// 1. PrivateEndpoint → VPC relationship
		if endpointData.VpcId != "" {
			if vpcNode, exists := lookup.byResourceID[endpointData.VpcId]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// For Interface endpoints only (Gateway endpoints don't have subnets, ENIs, or security groups)
		if endpointData.VpcEndpointType == "Interface" {
			// 2. PrivateEndpoint → Subnet relationships
			for _, subnetID := range endpointData.SubnetIds {
				if subnetNode, exists := lookup.byResourceID[subnetID]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "subnet",
							"subnet_id":       subnetID,
						}, req))
				}
			}

			// 3. PrivateEndpoint → ENI (Network Interface) relationships
			for _, eniID := range endpointData.NetworkInterfaceIds {
				if eniNode, exists := lookup.byResourceID[eniID]; exists {
					edges = append(edges, s.createEdge(node, eniNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type":      "network_interface",
							"network_interface_id": eniID,
						}, req))
				} else {
					s.logger.Warn("PrivateEndpoint ENI not found in lookup", "vpc_endpoint_id", node.Properties["resource_id"], "eni_id", eniID)
				}
			}

			// 4. SecurityGroup → PrivateEndpoint relationships (PROTECTS)
			for _, group := range endpointData.Groups {
				if sgNode, exists := lookup.byResourceID[group.GroupId]; exists {
					edges = append(edges, s.createEdge(sgNode, node, core.RelationshipProtects,
						map[string]interface{}{
							"security_group_id":   group.GroupId,
							"security_group_name": group.GroupName,
						}, req))
				}
			}
		}

		// 5. PrivateEndpoint → Target Service relationship (ROUTES_TO)
		// Match based on service name pattern to existing nodes
		targetServiceEdges := s.createPrivateEndpointServiceEdges(node, endpointData, lookup, req)
		edges = append(edges, targetServiceEdges...)
	}

	return edges
}

// createPrivateEndpointServiceEdges creates edges from PrivateEndpoint to target AWS services
// based on the ServiceName (e.g., com.amazonaws.us-east-1.s3)
func (s *AWSSource) createPrivateEndpointServiceEdges(node *core.DbNode, endpointData *PrivateEndpointData, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	serviceName := endpointData.ServiceName
	if serviceName == "" {
		return edges
	}

	// Parse service name pattern: com.amazonaws.{region}.{service}
	// Examples:
	// - com.amazonaws.us-east-1.s3
	// - com.amazonaws.us-east-1.dynamodb
	// - com.amazonaws.us-east-1.ecr.api
	// - com.amazonaws.us-east-1.ecr.dkr
	// - com.amazonaws.us-east-1.logs
	// - com.amazonaws.us-east-1.monitoring
	// - com.amazonaws.us-east-1.sns
	// - com.amazonaws.us-east-1.sqs
	// - com.amazonaws.us-east-1.elasticache

	var targetNodeType core.NodeType
	var serviceType string

	switch {
	case strings.Contains(serviceName, ".s3"):
		targetNodeType = core.NodeTypeStorage
		serviceType = "S3"
	case strings.Contains(serviceName, ".dynamodb"):
		targetNodeType = core.NodeTypeDatabase
		serviceType = "DynamoDB"
	case strings.Contains(serviceName, ".ecr"):
		targetNodeType = core.NodeTypeContainerRegistry
		serviceType = "ECR"
	case strings.Contains(serviceName, ".logs"):
		targetNodeType = core.NodeTypeLogAggregator
		serviceType = "CloudWatchLogs"
	case strings.Contains(serviceName, ".monitoring"):
		targetNodeType = core.NodeTypeMonitoringService
		serviceType = "CloudWatch"
	case strings.Contains(serviceName, ".sns"):
		targetNodeType = core.NodeTypeTopic
		serviceType = "SNS"
	case strings.Contains(serviceName, ".sqs"):
		targetNodeType = core.NodeTypeMessageQueue
		serviceType = "SQS"
	case strings.Contains(serviceName, ".elasticache"):
		targetNodeType = core.NodeTypeCache
		serviceType = "ElastiCache"
	case strings.Contains(serviceName, ".rds"):
		targetNodeType = core.NodeTypeDatabase
		serviceType = "RDS"
	case strings.Contains(serviceName, ".lambda"):
		targetNodeType = core.NodeTypeServerlessFunction
		serviceType = "Lambda"
	case strings.Contains(serviceName, ".secretsmanager"):
		targetNodeType = core.NodeTypeSecretVault
		serviceType = "SecretsManager"
	case strings.Contains(serviceName, ".kms"):
		targetNodeType = core.NodeTypeEncryptionKey
		serviceType = "KMS"
	default:
		// For external services (MongoDB Atlas, Datadog, etc.) or unknown services
		// Log for visibility but don't create edges to non-existent nodes
		s.logger.Debug("Unknown VPC Endpoint service type, skipping target service edge",
			"service_name", serviceName,
			"vpc_endpoint_id", endpointData.VpcEndpointId)
		return edges
	}

	// Find matching nodes of the target type in the same account
	if targetNodes, exists := lookup.byNodeType[targetNodeType]; exists {
		for _, targetNode := range targetNodes {
			// Create edge to each node of the matching type
			// The edge indicates that the VPC Endpoint provides private access to these services
			edges = append(edges, s.createEdge(node, targetNode, core.RelationshipRoutesTo,
				map[string]interface{}{
					"connection_type":     "private_endpoint",
					"target_service_type": serviceType,
					"service_name":        serviceName,
				}, req))
		}

		if len(targetNodes) > 0 {
			s.logger.Info("Created private endpoint to service edges",
				"vpc_endpoint_id", endpointData.VpcEndpointId,
				"service_type", serviceType,
				"target_node_count", len(targetNodes))
		}
	}

	return edges
}

// createENIEdges creates edges for Elastic Network Interfaces (ENI)
// Fetches ENI metadata from the in-memory DB meta cache and connects them to their cloud resources.
// Returns valid ENI nodes (ENIs present in the DB meta cache) and edges.
func (s *AWSSource) createENIEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	edges := make([]*core.DbEdge, 0)
	validENINodes := make([]*core.DbNode, 0)

	// Use the Nudgebee account ID from the request (cloud_accounts.id)
	if req.CloudAccountID == "" {
		s.logger.Warn("Cannot create ENI edges: Cloud account ID not found")
		return validENINodes, edges
	}

	// Fetch all ENIs from AWS using cloud collector CLI
	eniData, err := s.fetchENIDataFromAWS(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Error("Failed to fetch ENI data from AWS", "error", err)
		return validENINodes, edges
	}

	// Create a map of resource_id to ENI node for quick lookup (existing nodes from DB)
	eniNodeMap := make(map[string]*core.DbNode)
	for _, node := range nodes {
		if resourceID, ok := node.Properties["resource_id"].(string); ok {
			eniNodeMap[resourceID] = node
		}
	}

	// Process each ENI from DB meta cache
	for _, eniInfo := range eniData {
		var eniNode *core.DbNode

		// Check if this ENI already has a node built from the DB rows
		if existingNode, exists := eniNodeMap[eniInfo.NetworkInterfaceId]; exists {
			eniNode = existingNode
		} else {
			// ENI is in the DB meta cache but wasn't converted to a node
			// (e.g. filtered out by ServiceTypeFilter). Create one now from the cached data.
			eniNode = s.createENINodeFromAWSData(eniInfo, req)
			s.logger.Info("Created ENI node from DB meta cache (not in existing node list)",
				"eni_id", eniInfo.NetworkInterfaceId,
				"description", eniInfo.Description)
		}
		// Add to valid nodes (all ENIs present in the DB meta cache)
		validENINodes = append(validENINodes, eniNode)

		// 1. ENI → VPC relationship
		if eniInfo.VpcId != "" {
			if vpcNode, found := lookup.byResourceID[eniInfo.VpcId]; found {
				edges = append(edges, s.createEdge(eniNode, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			} else {
				s.logger.Warn("ENI VPC not found in lookup", "eni_id", eniInfo.NetworkInterfaceId, "vpc_id", eniInfo.VpcId)
			}
		}

		// 2. ENI → Subnet relationship
		if eniInfo.SubnetId != "" {
			if subnetNode, found := lookup.byResourceID[eniInfo.SubnetId]; found {
				edges = append(edges, s.createEdge(eniNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			} else {
				s.logger.Warn("ENI Subnet not found in lookup", "eni_id", eniInfo.NetworkInterfaceId, "subnet_id", eniInfo.SubnetId)
			}
		}

		// 3. ENI → Security Group relationships
		for _, group := range eniInfo.Groups {
			if sgNode, found := lookup.byResourceID[group.GroupId]; found {
				edges = append(edges, s.createEdge(eniNode, sgNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "security_group",
						"group_name":      group.GroupName,
					}, req))
			} else {
				s.logger.Warn("ENI SecurityGroup not found in lookup", "eni_id", eniInfo.NetworkInterfaceId, "sg_id", group.GroupId)
			}
		}

		// 4. ENI → Attached Resource (EC2, RDS, Lambda, etc.)
		if eniInfo.Attachment != nil && eniInfo.Attachment.InstanceId != "" {
			// Try to find the attached resource by instance ID
			if attachedNode, found := lookup.byResourceID[eniInfo.Attachment.InstanceId]; found {
				edges = append(edges, s.createEdge(attachedNode, eniNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type":       "eni_attachment",
						"status":                eniInfo.Attachment.Status,
						"device_index":          eniInfo.Attachment.DeviceIndex,
						"delete_on_termination": eniInfo.Attachment.DeleteOnTermination,
					}, req))
			}
		}

		// 5. Special handling for RDS ENIs (via requester ID and tags)
		if eniInfo.RequesterId == "amazon-rds" {
			// Try to match RDS instance by subnet and tags
			for _, rdsNode := range lookup.byNodeType[core.NodeTypeDatabase] {
				if s.matchENIToRDS(eniInfo, rdsNode) {
					edges = append(edges, s.createEdge(eniNode, rdsNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type": "rds_interface",
							"requester_id":    eniInfo.RequesterId,
						}, req))
					break
				}
			}
		}

		// 6. Special handling for Load Balancer ENIs
		if strings.Contains(eniInfo.RequesterId, "amazon-elb") || strings.Contains(eniInfo.RequesterId, "amazon-elasticloadbalancing") {
			// Try to match load balancer by description
			for _, lbNode := range lookup.byNodeType[core.NodeTypeLoadBalancer] {
				if lbName, ok := getStringProperty(lbNode, "name"); ok {
					if strings.Contains(eniInfo.Description, lbName) {
						edges = append(edges, s.createEdge(eniNode, lbNode, core.RelationshipHostedOn,
							map[string]interface{}{
								"connection_type": "load_balancer_interface",
								"description":     eniInfo.Description,
							}, req))
						break
					}
				}
			}
		}

		// 7. Special handling for NAT Gateway ENIs.
		// AWS sets Description to "Interface for NAT Gateway nat-XXXXX" for these ENIs,
		// which gives a direct ID match. Fall back to subnet_id matching only if the
		// description-based match fails (e.g. custom descriptions).
		if eniInfo.InterfaceType == "nat_gateway" {
			matched := false
			// Primary: match by NAT Gateway ID embedded in description
			for _, natNode := range lookup.byNodeType[core.NodeTypeNetworkGateway] {
				if natID, ok := getStringProperty(natNode, "nat_gateway_id"); ok && natID != "" {
					// AWS description format: "Interface for NAT Gateway nat-XXXXX"
					if strings.Contains(eniInfo.Description, natID) {
						edges = append(edges, s.createEdge(eniNode, natNode, core.RelationshipHostedOn,
							map[string]interface{}{
								"connection_type": "nat_gateway_interface",
								"interface_type":  eniInfo.InterfaceType,
							}, req))
						matched = true
						break
					}
				}
			}
			// Fallback: match by shared subnet_id (less precise)
			if !matched {
				for _, natNode := range lookup.byNodeType[core.NodeTypeNetworkGateway] {
					if natSubnetID, ok := getStringProperty(natNode, "subnet_id"); ok {
						if natSubnetID == eniInfo.SubnetId {
							edges = append(edges, s.createEdge(eniNode, natNode, core.RelationshipHostedOn,
								map[string]interface{}{
									"connection_type": "nat_gateway_interface",
									"interface_type":  eniInfo.InterfaceType,
								}, req))
							break
						}
					}
				}
			}
		}
	}

	s.logger.Info("Created ENI edges",
		"db_eni_count", len(nodes),
		"valid_eni_count", len(validENINodes),
		"edges_created", len(edges))

	return validENINodes, edges
}

// createENINodeFromAWSData creates a new ENI node from AWS CLI data
func (s *AWSSource) createENINodeFromAWSData(eniInfo *ENINetworkInterface, req *core.SourceBuildRequest) *core.DbNode {
	// Build properties from AWS data
	properties := make(map[string]interface{})
	properties["name"] = eniInfo.NetworkInterfaceId
	properties["type"] = "network-interface"
	properties["status"] = eniInfo.Status
	properties["cloud_provider"] = "AWS"
	properties["region"] = req.Region
	properties["resource_id"] = eniInfo.NetworkInterfaceId
	properties["service_name"] = "AmazonVPC"
	properties["is_active"] = true
	properties["external_resource_id"] = eniInfo.NetworkInterfaceId
	properties["description"] = eniInfo.Description
	properties["interface_type"] = eniInfo.InterfaceType
	properties["private_ip_address"] = eniInfo.PrivateIpAddress
	properties["availability_zone"] = eniInfo.AvailabilityZone
	properties["requester_id"] = eniInfo.RequesterId

	// Add VPC and Subnet IDs
	if eniInfo.VpcId != "" {
		properties["vpc_id"] = eniInfo.VpcId
	}
	if eniInfo.SubnetId != "" {
		properties["subnet_id"] = eniInfo.SubnetId
	}

	// Extract security groups
	if len(eniInfo.Groups) > 0 {
		securityGroups := make([]map[string]string, 0, len(eniInfo.Groups))
		for _, group := range eniInfo.Groups {
			securityGroups = append(securityGroups, map[string]string{
				"GroupId":   group.GroupId,
				"GroupName": group.GroupName,
			})
		}
		properties["security_groups"] = securityGroups
	}

	// Extract attachment info
	if eniInfo.Attachment != nil {
		properties["attachment"] = map[string]interface{}{
			"attachment_id":         eniInfo.Attachment.AttachmentId,
			"instance_id":           eniInfo.Attachment.InstanceId,
			"device_index":          eniInfo.Attachment.DeviceIndex,
			"status":                eniInfo.Attachment.Status,
			"delete_on_termination": eniInfo.Attachment.DeleteOnTermination,
		}
	}

	// Extract tags
	if len(eniInfo.TagSet) > 0 {
		tags := make(map[string]string)
		for _, tag := range eniInfo.TagSet {
			tags[tag.Key] = tag.Value
		}
		properties["tags"] = tags
	}

	// Mark this node as dynamically created from AWS
	properties["source"] = "aws_cli"
	properties["created_from_live_data"] = true

	// Build unique key using new 6-part format
	tempNode := &core.DbNode{
		NodeType:       core.NodeTypeNetworkInterface,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(
		core.NodeTypeNetworkInterface,
		uniqueKey,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"aws",
	)
}

// ENINetworkInterface represents an AWS ENI from describe-network-interfaces
type ENINetworkInterface struct {
	NetworkInterfaceId string `json:"NetworkInterfaceId"`
	SubnetId           string `json:"SubnetId"`
	VpcId              string `json:"VpcId"`
	AvailabilityZone   string `json:"AvailabilityZone"`
	Description        string `json:"Description"`
	InterfaceType      string `json:"InterfaceType"`
	PrivateIpAddress   string `json:"PrivateIpAddress"`
	RequesterId        string `json:"RequesterId"`
	Status             string `json:"Status"`
	Groups             []struct {
		GroupId   string `json:"GroupId"`
		GroupName string `json:"GroupName"`
	} `json:"Groups"`
	Attachment *struct {
		AttachmentId        string `json:"AttachmentId"`
		InstanceId          string `json:"InstanceId"`
		DeviceIndex         int    `json:"DeviceIndex"`
		Status              string `json:"Status"`
		DeleteOnTermination bool   `json:"DeleteOnTermination"`
	} `json:"Attachment"`
	TagSet []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"TagSet"`
}

// VPCFlowLog represents a VPC Flow Log from describe-flow-logs
type VPCFlowLog struct {
	FlowLogId              string                 `json:"FlowLogId"`
	FlowLogStatus          string                 `json:"FlowLogStatus"`
	ResourceId             string                 `json:"ResourceId"`
	TrafficType            string                 `json:"TrafficType"`
	LogDestinationType     string                 `json:"LogDestinationType"`
	LogDestination         string                 `json:"LogDestination"`
	LogFormat              string                 `json:"LogFormat"`
	LogGroupName           string                 `json:"LogGroupName"`
	DeliverLogsStatus      string                 `json:"DeliverLogsStatus"`
	MaxAggregationInterval int                    `json:"MaxAggregationInterval"`
	DestinationOptions     map[string]interface{} `json:"DestinationOptions"`
	Tags                   []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// NATGatewayData represents NAT Gateway information from AWS CLI
type NATGatewayData struct {
	NatGatewayId        string                   `json:"NatGatewayId"`
	State               string                   `json:"State"`
	SubnetId            string                   `json:"SubnetId"`
	VpcId               string                   `json:"VpcId"`
	CreateTime          string                   `json:"CreateTime"`
	ConnectivityType    string                   `json:"ConnectivityType"`
	NatGatewayAddresses []map[string]interface{} `json:"NatGatewayAddresses"`
	Tags                []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// VPCData represents VPC metadata from AWS CLI describe-vpcs
type VPCData struct {
	VpcId     string `json:"VpcId"`
	State     string `json:"State"`
	CidrBlock string `json:"CidrBlock"`
	IsDefault bool   `json:"IsDefault"`
	Tags      []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// RouteTableData represents Route Table information from AWS CLI describe-route-tables
type RouteTableData struct {
	RouteTableId string `json:"RouteTableId"`
	VpcId        string `json:"VpcId"`
	OwnerId      string `json:"OwnerId"`
	Associations []struct {
		RouteTableAssociationId string `json:"RouteTableAssociationId"`
		RouteTableId            string `json:"RouteTableId"`
		SubnetId                string `json:"SubnetId"`
		GatewayId               string `json:"GatewayId"`
		Main                    bool   `json:"Main"`
		AssociationState        struct {
			State string `json:"State"`
		} `json:"AssociationState"`
	} `json:"Associations"`
	Routes []struct {
		DestinationCidrBlock     string `json:"DestinationCidrBlock"`
		DestinationIpv6CidrBlock string `json:"DestinationIpv6CidrBlock"`
		GatewayId                string `json:"GatewayId"`
		NatGatewayId             string `json:"NatGatewayId"`
		TransitGatewayId         string `json:"TransitGatewayId"`
		VpcPeeringConnectionId   string `json:"VpcPeeringConnectionId"`
		NetworkInterfaceId       string `json:"NetworkInterfaceId"`
		InstanceId               string `json:"InstanceId"`
		Origin                   string `json:"Origin"`
		State                    string `json:"State"`
	} `json:"Routes"`
	Tags []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// TargetGroupData represents Target Group information from AWS CLI describe-target-groups
type TargetGroupData struct {
	TargetGroupArn             string   `json:"TargetGroupArn"`
	TargetGroupName            string   `json:"TargetGroupName"`
	Protocol                   string   `json:"Protocol"`
	Port                       int      `json:"Port"`
	VpcId                      string   `json:"VpcId"`
	HealthCheckProtocol        string   `json:"HealthCheckProtocol"`
	HealthCheckPort            string   `json:"HealthCheckPort"`
	HealthCheckEnabled         bool     `json:"HealthCheckEnabled"`
	HealthCheckPath            string   `json:"HealthCheckPath"`
	TargetType                 string   `json:"TargetType"` // "instance", "ip", "lambda", "alb"
	LoadBalancerArns           []string `json:"LoadBalancerArns"`
	HealthCheckIntervalSeconds int      `json:"HealthCheckIntervalSeconds"`
	HealthCheckTimeoutSeconds  int      `json:"HealthCheckTimeoutSeconds"`
	HealthyThresholdCount      int      `json:"HealthyThresholdCount"`
	UnhealthyThresholdCount    int      `json:"UnhealthyThresholdCount"`
}

// TargetHealthData represents Target Health information from AWS CLI describe-target-health
type TargetHealthData struct {
	Target struct {
		Id               string `json:"Id"`   // Instance ID, IP address, Lambda ARN, or ALB ARN
		Port             int    `json:"Port"` // Port number (not present for Lambda)
		AvailabilityZone string `json:"AvailabilityZone"`
	} `json:"Target"`
	HealthCheckPort string `json:"HealthCheckPort"`
	TargetHealth    struct {
		State       string `json:"State"`       // "initial", "healthy", "unhealthy", "unused", "draining", "unavailable"
		Reason      string `json:"Reason"`      // Reason code if unhealthy
		Description string `json:"Description"` // Description of health state
	} `json:"TargetHealth"`
}

// LoadBalancerData represents Load Balancer metadata from AWS CLI describe-load-balancers
type LoadBalancerData struct {
	LoadBalancerArn       string `json:"LoadBalancerArn"`
	LoadBalancerName      string `json:"LoadBalancerName"`
	DNSName               string `json:"DNSName"`
	CanonicalHostedZoneId string `json:"CanonicalHostedZoneId"`
	Scheme                string `json:"Scheme"` // "internet-facing" or "internal"
	Type                  string `json:"Type"`   // "application", "network", or "gateway"
	VpcId                 string `json:"VpcId"`
	State                 struct {
		Code string `json:"Code"` // "active", "provisioning", "active_impaired", "failed"
	} `json:"State"`
	AvailabilityZones []struct {
		ZoneName         string `json:"ZoneName"`
		SubnetId         string `json:"SubnetId"`
		LoadBalancerAddr string `json:"LoadBalancerAddr,omitempty"`
	} `json:"AvailabilityZones"`
	SecurityGroups []string `json:"SecurityGroups"`
	IpAddressType  string   `json:"IpAddressType"` // "ipv4", "dualstack", "dualstack-without-public-ipv4"
	CreatedTime    string   `json:"CreatedTime"`
	// Tags are fetched separately via describe-tags API
	Tags []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags,omitempty"`
}

// ClassicLoadBalancerData represents Classic Load Balancer metadata from AWS CLI describe-load-balancers (elb, not elbv2)
// Classic LBs use a different API than ALB/NLB and have different structure (no Target Groups, direct instance registration)
type ClassicLoadBalancerData struct {
	LoadBalancerName          string   `json:"LoadBalancerName"`
	DNSName                   string   `json:"DNSName"`
	CanonicalHostedZoneNameID string   `json:"CanonicalHostedZoneNameID"`
	Scheme                    string   `json:"Scheme"` // "internet-facing" or "internal"
	VPCId                     string   `json:"VPCId"`
	Subnets                   []string `json:"Subnets"`
	SecurityGroups            []string `json:"SecurityGroups"`
	AvailabilityZones         []string `json:"AvailabilityZones"`
	Instances                 []struct {
		InstanceId string `json:"InstanceId"`
	} `json:"Instances"`
	HealthCheck struct {
		Target             string `json:"Target"`
		Interval           int    `json:"Interval"`
		Timeout            int    `json:"Timeout"`
		UnhealthyThreshold int    `json:"UnhealthyThreshold"`
		HealthyThreshold   int    `json:"HealthyThreshold"`
	} `json:"HealthCheck"`
	ListenerDescriptions []struct {
		Listener struct {
			Protocol         string `json:"Protocol"`
			LoadBalancerPort int    `json:"LoadBalancerPort"`
			InstanceProtocol string `json:"InstanceProtocol"`
			InstancePort     int    `json:"InstancePort"`
		} `json:"Listener"`
	} `json:"ListenerDescriptions"`
	CreatedTime string `json:"CreatedTime"`
	// Tags fetched separately via elb describe-tags
	Tags []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags,omitempty"`
}

// ClassicInstanceHealthData represents instance health from Classic LB (aws elb describe-instance-health)
type ClassicInstanceHealthData struct {
	InstanceId  string `json:"InstanceId"`
	State       string `json:"State"`       // "InService", "OutOfService"
	ReasonCode  string `json:"ReasonCode"`  // e.g., "ELB", "Instance", "N/A"
	Description string `json:"Description"` // Human-readable description
}

// PrivateEndpointData represents VPC Endpoint metadata from AWS CLI describe-vpc-endpoints
// This is cloud-agnostic and can be extended for Azure Private Endpoints and GCP Private Service Connect
type PrivateEndpointData struct {
	VpcEndpointId       string   `json:"VpcEndpointId"`
	VpcEndpointType     string   `json:"VpcEndpointType"` // "Interface" or "Gateway"
	VpcId               string   `json:"VpcId"`
	ServiceName         string   `json:"ServiceName"` // e.g., "com.amazonaws.us-east-1.s3"
	State               string   `json:"State"`
	SubnetIds           []string `json:"SubnetIds"`
	NetworkInterfaceIds []string `json:"NetworkInterfaceIds"`
	Groups              []struct {
		GroupId   string `json:"GroupId"`
		GroupName string `json:"GroupName"`
	} `json:"Groups"`
	RouteTableIds     []string `json:"RouteTableIds"` // For Gateway endpoints
	PrivateDnsEnabled bool     `json:"PrivateDnsEnabled"`
	Tags              []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// PublicIPData represents Elastic IP metadata from AWS CLI describe-addresses
// Cloud-agnostic: AWS Elastic IP, Azure Public IP, GCP External IP
type PublicIPData struct {
	AllocationId            string `json:"AllocationId"`
	PublicIp                string `json:"PublicIp"`
	AssociationId           string `json:"AssociationId"`
	InstanceId              string `json:"InstanceId"`
	NetworkInterfaceId      string `json:"NetworkInterfaceId"`
	NetworkInterfaceOwnerId string `json:"NetworkInterfaceOwnerId"`
	PrivateIpAddress        string `json:"PrivateIpAddress"`
	Domain                  string `json:"Domain"` // "vpc" or "standard"
	Tags                    []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// EFSMountTargetData represents EFS mount target info from AWS CLI describe-mount-targets
type EFSMountTargetData struct {
	MountTargetId        string `json:"MountTargetId"`
	FileSystemId         string `json:"FileSystemId"`
	SubnetId             string `json:"SubnetId"`
	VpcId                string `json:"VpcId"`
	NetworkInterfaceId   string `json:"NetworkInterfaceId"`
	IpAddress            string `json:"IpAddress"`
	LifeCycleState       string `json:"LifeCycleState"`
	AvailabilityZoneId   string `json:"AvailabilityZoneId"`
	AvailabilityZoneName string `json:"AvailabilityZoneName"`
	OwnerId              string `json:"OwnerId"`
}

// iamRole represents an AWS IAM Role as returned by aws iam list-roles
type iamRole struct {
	RoleName                 string      `json:"RoleName"`
	RoleId                   string      `json:"RoleId"`
	Arn                      string      `json:"Arn"`
	Path                     string      `json:"Path"`
	Description              string      `json:"Description"`
	MaxSessionDuration       int         `json:"MaxSessionDuration"`
	AssumeRolePolicyDocument interface{} `json:"AssumeRolePolicyDocument"`
	Tags                     []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// iamInstanceProfile represents an entry in the aws iam list-instance-profiles response.
// Each instance profile contains exactly one attached role.
type iamInstanceProfile struct {
	Arn   string `json:"Arn"`
	Roles []struct {
		Arn string `json:"Arn"`
	} `json:"Roles"`
}

// listIAMInstanceProfilesResponse wraps the aws iam list-instance-profiles JSON response
type listIAMInstanceProfilesResponse struct {
	InstanceProfiles []iamInstanceProfile `json:"InstanceProfiles"`
}

// fetchIAMRolesFromAWS fetches all IAM Roles from the in-memory meta cache.
// AssumeRolePolicyDocument is stored URL-encoded in the DB; it is decoded back
// to a JSON object so extractTrustPolicyPrincipals can work correctly.
func (s *AWSSource) fetchIAMRolesFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]iamRole, error) {
	rows := s.metaByType["Role"]
	roles := make([]iamRole, 0, len(rows))
	for _, row := range rows {
		var role iamRole
		if err := unmarshalMetaInto(row, &role); err != nil {
			s.logger.Warn("Failed to parse IAM role meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		// AssumeRolePolicyDocument is URL-encoded in the DB (e.g. %7B%22Version%22%3A...).
		// Decode it back to a JSON object so trust principal extraction works.
		if docStr, ok := role.AssumeRolePolicyDocument.(string); ok && docStr != "" {
			if decoded, err := url.QueryUnescape(docStr); err == nil {
				var docObj interface{}
				if json.Unmarshal([]byte(decoded), &docObj) == nil {
					role.AssumeRolePolicyDocument = docObj
				}
			}
		}
		roles = append(roles, role)
	}
	s.logger.Info("Fetched IAM roles from DB cache", "role_count", len(roles))
	return roles, nil
}

// fetchInstanceProfileRoleMapFromAWS returns a map of instance profile ARN → attached role ARN
// by calling aws iam list-instance-profiles. Used to correctly resolve EC2 RUNS_AS edges when
// the instance profile name differs from the attached role name.
func (s *AWSSource) fetchInstanceProfileRoleMapFromAWS(reqCtx *security.RequestContext, accountID string) (map[string]string, error) {
	cmd := "aws iam list-instance-profiles --output json"

	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute AWS CLI command: %w", err)
	}

	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		return nil, fmt.Errorf("invalid response format from cloud CLI: expected 'data', 'output', or 'result' field")
	}

	var result listIAMInstanceProfilesResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse instance profiles response: %w", err)
	}

	profileRoleMap := make(map[string]string, len(result.InstanceProfiles))
	for _, profile := range result.InstanceProfiles {
		if profile.Arn == "" || len(profile.Roles) == 0 || profile.Roles[0].Arn == "" {
			continue
		}
		profileRoleMap[profile.Arn] = profile.Roles[0].Arn
	}

	s.logger.Info("Fetched instance profile → role mappings",
		"account_id", accountID,
		"count", len(profileRoleMap))

	return profileRoleMap, nil
}

// buildServiceIdentityNodes creates ServiceIdentity knowledge graph nodes from IAM roles.
// IAM is cloud-agnostic in the graph: node_type is always ServiceIdentity,
// with AWS-specific details stored in properties (subtype = "IAMRole").
func (s *AWSSource) buildServiceIdentityNodes(roles []iamRole, req *core.SourceBuildRequest) []*core.DbNode {
	var nodes []*core.DbNode

	for _, role := range roles {
		// Extract AWS account number from the role ARN:
		// arn:aws:iam::123456789012:role/MyRole → parts[4] = "123456789012"
		awsAccountNumber := ""
		if arnParts := strings.Split(role.Arn, ":"); len(arnParts) >= 5 {
			awsAccountNumber = arnParts[4]
		}

		properties := map[string]interface{}{
			"name":                 role.RoleName,
			"arn":                  role.Arn,
			"cloud_provider":       "AWS",
			"region":               "global", // IAM is a global AWS service
			"subtype":              "IAMRole",
			"service_name":         "AWSIAM",
			"role_id":              role.RoleId,
			"path":                 role.Path,
			"description":          role.Description,
			"max_session_duration": role.MaxSessionDuration,
			"nb_account_id":        req.CloudAccountID,
			"aws_account_number":   awsAccountNumber,
			"is_active":            true,
		}

		// Encode trust policy as JSON string
		if role.AssumeRolePolicyDocument != nil {
			if trustJSON, err := json.Marshal(role.AssumeRolePolicyDocument); err == nil {
				properties["trust_policy"] = string(trustJSON)
			}
		}

		// Extract principal ARNs from the trust policy for edge resolution
		trustPrincipals := extractTrustPolicyPrincipals(role.AssumeRolePolicyDocument)
		if len(trustPrincipals) > 0 {
			if principalsJSON, err := json.Marshal(trustPrincipals); err == nil {
				properties["trust_principals"] = string(principalsJSON)
			}
		}

		// Convert tags to labels map
		if len(role.Tags) > 0 {
			labelsMap := make(map[string]string, len(role.Tags))
			for _, tag := range role.Tags {
				labelsMap[tag.Key] = tag.Value
			}
			properties["labels"] = labelsMap
		}

		// Unique key: aws:{accountID}:global:ServiceIdentity::{roleName}
		uniqueKey := fmt.Sprintf("aws:%s:global:ServiceIdentity::%s", req.CloudAccountID, role.RoleName)

		node := core.NewNode(
			core.NodeTypeServiceIdentity,
			uniqueKey,
			properties,
			req.TenantID,
			req.CloudAccountID,
			"aws",
		)
		nodes = append(nodes, node)
	}

	return nodes
}

// extractTrustPolicyPrincipals parses an IAM trust policy document and returns all principal ARNs.
func extractTrustPolicyPrincipals(doc interface{}) []string {
	if doc == nil {
		return nil
	}

	docMap, ok := doc.(map[string]interface{})
	if !ok {
		return nil
	}

	var statements []interface{}
	switch v := docMap["Statement"].(type) {
	case []interface{}:
		statements = v
	case map[string]interface{}:
		statements = []interface{}{v}
	default:
		return nil
	}

	var principals []string
	for _, stmt := range statements {
		stmtMap, ok := stmt.(map[string]interface{})
		if !ok {
			continue
		}
		// Only process Allow statements — Deny statements must not create ASSUMES edges
		if effect, ok := stmtMap["Effect"].(string); !ok || effect != "Allow" {
			continue
		}
		principal, ok := stmtMap["Principal"]
		if !ok {
			continue
		}
		switch p := principal.(type) {
		case string:
			if p != "" && p != "*" {
				principals = append(principals, p)
			}
		case map[string]interface{}:
			// Principal can be {"AWS": "arn:...", "Service": "lambda.amazonaws.com"}
			for _, v := range p {
				switch vv := v.(type) {
				case string:
					if vv != "" && vv != "*" {
						principals = append(principals, vv)
					}
				case []interface{}:
					for _, item := range vv {
						if s, ok := item.(string); ok && s != "" && s != "*" {
							principals = append(principals, s)
						}
					}
				}
			}
		}
	}
	return principals
}

// buildServiceIdentityEdges creates edges between ServiceIdentity nodes and compute resources:
//   - Lambda/EC2 → ServiceIdentity (RUNS_AS): compute resources that assume this IAM role
//   - ServiceIdentity → ServiceIdentity (ASSUMES): cross-account / service trust relationships
func (s *AWSSource) buildServiceIdentityEdges(serviceIdentityNodes []*core.DbNode, lookup *NodeLookup, instanceProfileRoleMap map[string]string, req *core.SourceBuildRequest) []*core.DbEdge {
	var edges []*core.DbEdge

	// Build a local ARN index for ServiceIdentity nodes to resolve role lookups
	serviceIdentityByARN := make(map[string]*core.DbNode, len(serviceIdentityNodes))
	for _, n := range serviceIdentityNodes {
		if arn, ok := n.Properties["arn"].(string); ok && arn != "" {
			serviceIdentityByARN[arn] = n
		}
	}

	// 1. Lambda → ServiceIdentity (RUNS_AS)
	for _, lambdaNode := range lookup.byNodeType[core.NodeTypeServerlessFunction] {
		roleARN, ok := getStringProperty(lambdaNode, "role_arn")
		if !ok || roleARN == "" {
			continue
		}
		if identityNode, exists := serviceIdentityByARN[roleARN]; exists {
			edge := core.NewEdge(
				lambdaNode.ID,
				identityNode.ID,
				core.RelationshipRunsAs,
				map[string]interface{}{"source": "iam_role"},
				req.TenantID,
				req.CloudAccountID,
				"aws",
			)
			edges = append(edges, edge)
		}
	}

	// 2. EC2 → ServiceIdentity (RUNS_AS) via instance profile
	for _, ec2Node := range lookup.byNodeType[core.NodeTypeComputeInstance] {
		profileARN, ok := getStringProperty(ec2Node, "iam_instance_profile_arn")
		if !ok || profileARN == "" {
			continue
		}
		// Resolve the role ARN via the pre-fetched instance profile map.
		// Fall back to name-based ARN substitution when the map is unavailable.
		roleARN, mapped := instanceProfileRoleMap[profileARN]
		if !mapped {
			roleARN = strings.Replace(profileARN, ":instance-profile/", ":role/", 1)
		}
		if identityNode, exists := serviceIdentityByARN[roleARN]; exists {
			edge := core.NewEdge(
				ec2Node.ID,
				identityNode.ID,
				core.RelationshipRunsAs,
				map[string]interface{}{"source": "iam_instance_profile"},
				req.TenantID,
				req.CloudAccountID,
				"aws",
			)
			edges = append(edges, edge)
		}
	}

	// 3. ServiceIdentity → ServiceIdentity (ASSUMES) via trust policy
	// Only create edges where the principal is another IAM Role ARN (contains ":role/")
	for _, identityNode := range serviceIdentityNodes {
		principalsJSON, ok := getStringProperty(identityNode, "trust_principals")
		if !ok || principalsJSON == "" {
			continue
		}
		var principals []string
		if err := json.Unmarshal([]byte(principalsJSON), &principals); err != nil {
			continue
		}
		for _, principalARN := range principals {
			if !strings.Contains(principalARN, ":role/") {
				continue // Skip non-role principals (services, accounts, etc.)
			}
			if principalNode, exists := serviceIdentityByARN[principalARN]; exists {
				edge := core.NewEdge(
					principalNode.ID,
					identityNode.ID,
					core.RelationshipAssumes,
					map[string]interface{}{"source": "trust_policy"},
					req.TenantID,
					req.CloudAccountID,
					"aws",
				)
				edges = append(edges, edge)
			}
		}
	}

	return edges
}

// fetchPrivateEndpointDataFromAWS fetches VPC Endpoint metadata from the in-memory meta cache.
func (s *AWSSource) fetchPrivateEndpointDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string, vpcEndpointID string) (*PrivateEndpointData, error) {
	row, ok := s.metaByTypeAndID["vpc-endpoint"][vpcEndpointID]
	if !ok {
		return nil, fmt.Errorf("VPC Endpoint %s not found in loaded resources", vpcEndpointID)
	}
	var ep PrivateEndpointData
	if err := unmarshalMetaInto(row, &ep); err != nil {
		return nil, fmt.Errorf("failed to parse VPC Endpoint meta: %w", err)
	}
	s.logger.Info("Fetched VPC Endpoint data from DB cache",
		"vpc_endpoint_id", vpcEndpointID,
		"vpc_endpoint_type", ep.VpcEndpointType,
		"service_name", ep.ServiceName,
		"state", ep.State)
	return &ep, nil
}

// fetchNATGatewayDataFromAWS fetches NAT Gateway metadata from AWS using cloud collector CLI
func (s *AWSSource) fetchNATGatewayDataFromAWS(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string, natGatewayID string) (*NATGatewayData, error) {
	// Build AWS CLI command to describe specific NAT Gateway
	cmd := fmt.Sprintf("aws ec2 describe-nat-gateways --nat-gateway-ids %s --output json", natGatewayID)

	s.logger.Info("Fetching NAT Gateway data from AWS",
		"nat_gateway_id", natGatewayID,
		"account_id", accountID,
		"command", cmd)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute AWS CLI command: %w", err)
	}

	// Parse response
	var result struct {
		NatGateways []NATGatewayData `json:"NatGateways"`
	}

	// Log the raw response for debugging
	s.logger.Info("Cloud CLI response received", "response_keys", getMapKeys(resp))

	// Try different response formats
	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		// Try to see if the entire response is the JSON
		if respBytes, err := json.Marshal(resp); err == nil {
			s.logger.Error("Invalid response format from cloud CLI", "raw_response", string(respBytes))
		}
		return nil, fmt.Errorf("invalid response format from cloud CLI: expected 'data', 'output', or 'result' field with string value")
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.logger.Error("Failed to parse NAT Gateway JSON", "error", err, "output_preview", truncateString(output, 200))
		return nil, fmt.Errorf("failed to parse NAT Gateway response: %w", err)
	}

	if len(result.NatGateways) == 0 {
		return nil, fmt.Errorf("NAT Gateway not found: %s", natGatewayID)
	}

	s.logger.Info("Successfully fetched NAT Gateway data from AWS",
		"nat_gateway_id", natGatewayID,
		"state", result.NatGateways[0].State)

	return &result.NatGateways[0], nil
}

// fetchVPCDataFromAWS fetches VPC metadata from the in-memory meta cache.
// Note: this will return "not found" for VPCs that are referenced by resources but not stored
// in cloud_resourses themselves (e.g. cross-account VPCs, un-synced VPCs). Callers handle
// this with a graceful fallback to the VPC ID as the node name.
func (s *AWSSource) fetchVPCDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string, vpcID string) (*VPCData, error) {
	row, ok := s.metaByTypeAndID["vpc"][vpcID]
	if !ok {
		return nil, fmt.Errorf("VPC %s not found in loaded resources", vpcID)
	}
	var vpc VPCData
	if err := unmarshalMetaInto(row, &vpc); err != nil {
		return nil, fmt.Errorf("failed to parse VPC meta: %w", err)
	}
	s.logger.Info("Fetched VPC data from DB cache", "vpc_id", vpcID, "state", vpc.State)
	return &vpc, nil
}

// fetchENIDataFromAWS fetches all ENI metadata from the in-memory meta cache.
func (s *AWSSource) fetchENIDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]*ENINetworkInterface, error) {
	rows := s.metaByType["network-interface"]
	enis := make([]*ENINetworkInterface, 0, len(rows))
	for _, row := range rows {
		var eni ENINetworkInterface
		if err := unmarshalMetaInto(row, &eni); err != nil {
			s.logger.Warn("Failed to parse ENI meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		enis = append(enis, &eni)
	}
	s.logger.Info("Fetched ENI data from DB cache", "eni_count", len(enis))
	return enis, nil
}

// fetchAllSNSSubscriptions fetches ALL SNS subscriptions at once and returns them grouped by topic ARN
// This is much more efficient than calling fetchSNSSubscriptions for each topic individually
func (s *AWSSource) fetchAllSNSSubscriptions(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string) (map[string][]interface{}, error) {
	// Build AWS CLI command to list ALL subscriptions (no topic filter)
	cmd := "aws sns list-subscriptions --output json"

	// Add region filter if specified
	if req.Region != "" {
		cmd = fmt.Sprintf("aws sns list-subscriptions --region %s --output json", req.Region)
	}

	s.logger.Debug("Fetching all SNS subscriptions",
		"account_id", accountID,
		"region", req.Region)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch all SNS subscriptions: %w", err)
	}

	// Parse response
	var result struct {
		Subscriptions []map[string]interface{} `json:"Subscriptions"`
	}

	if data, ok := resp["data"].(string); ok {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, fmt.Errorf("failed to parse SNS subscriptions response: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid response format from cloud CLI")
	}

	// Group subscriptions by TopicArn
	subscriptionsByTopic := make(map[string][]interface{})
	for _, sub := range result.Subscriptions {
		if topicArn, ok := sub["TopicArn"].(string); ok && topicArn != "" {
			subscriptionsByTopic[topicArn] = append(subscriptionsByTopic[topicArn], sub)
		}
	}

	s.logger.Info("Successfully fetched all SNS subscriptions",
		"account_id", accountID,
		"total_subscriptions", len(result.Subscriptions),
		"topics_with_subscriptions", len(subscriptionsByTopic))

	return subscriptionsByTopic, nil
}

// matchENIToRDS checks if an ENI belongs to a specific RDS instance/cluster
func (s *AWSSource) matchENIToRDS(eniInfo *ENINetworkInterface, rdsNode *core.DbNode) bool {
	// Check RDS tags in ENI
	rdsDBID := ""
	rdsClusterID := ""

	for _, tag := range eniInfo.TagSet {
		if tag.Key == "aws:rds:db-id" {
			rdsDBID = tag.Value
		}
		if tag.Key == "aws:rds:cluster-id" {
			rdsClusterID = tag.Value
		}
	}

	// Match by RDS instance name or resource ID
	if rdsDBID != "" {
		if name, ok := getStringProperty(rdsNode, "name"); ok && name == rdsDBID {
			return true
		}
		if resourceID, ok := getStringProperty(rdsNode, "resource_id"); ok && resourceID == rdsDBID {
			return true
		}
	}

	// Match by RDS cluster name
	if rdsClusterID != "" {
		if name, ok := getStringProperty(rdsNode, "name"); ok && name == rdsClusterID {
			return true
		}
		if resourceID, ok := getStringProperty(rdsNode, "resource_id"); ok && resourceID == rdsClusterID {
			return true
		}
	}

	// Match by subnet (same subnet as RDS)
	if eniInfo.SubnetId != "" {
		if rdsMeta, ok := getMetadataMap(rdsNode); ok {
			if dbSubnetGroup, ok := rdsMeta["DBSubnetGroup"].(map[string]interface{}); ok {
				if subnets, ok := dbSubnetGroup["Subnets"].([]interface{}); ok {
					for _, subnet := range subnets {
						if subnetMap, ok := subnet.(map[string]interface{}); ok {
							if subnetID, ok := subnetMap["SubnetIdentifier"].(string); ok {
								if subnetID == eniInfo.SubnetId {
									return true
								}
							}
						}
					}
				}
			}
		}
	}

	return false
}

// fetchAllPublicIPDataFromAWS fetches all Elastic IP data from the in-memory meta cache.
func (s *AWSSource) fetchAllPublicIPDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) (map[string]*PublicIPData, error) {
	rows := s.metaByType["elastic-ip"]
	eipByAllocationID := make(map[string]*PublicIPData, len(rows))
	for _, row := range rows {
		var addr PublicIPData
		if err := unmarshalMetaInto(row, &addr); err != nil {
			s.logger.Warn("Failed to parse Elastic IP meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		if addr.AllocationId != "" {
			eipByAllocationID[addr.AllocationId] = &addr
		}
	}
	s.logger.Info("Fetched Elastic IP data from DB cache", "eip_count", len(eipByAllocationID))
	return eipByAllocationID, nil
}

// fetchEFSMountTargetsFromAWS fetches EFS mount target data from AWS using cloud collector CLI
func (s *AWSSource) fetchEFSMountTargetsFromAWS(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string, fileSystemID string) ([]EFSMountTargetData, error) {
	// Build AWS CLI command to describe mount targets for an EFS file system
	cmd := fmt.Sprintf("aws efs describe-mount-targets --file-system-id %s --output json", fileSystemID)

	s.logger.Info("Fetching EFS mount targets from AWS",
		"file_system_id", fileSystemID,
		"account_id", accountID,
		"command", cmd)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute AWS CLI command: %w", err)
	}

	// Parse response
	var result struct {
		MountTargets []EFSMountTargetData `json:"MountTargets"`
	}

	// Log the raw response for debugging
	s.logger.Info("Cloud CLI response received for EFS mount targets", "response_keys", getMapKeys(resp))

	// Try different response formats
	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		if respBytes, err := json.Marshal(resp); err == nil {
			s.logger.Error("Invalid response format from cloud CLI", "raw_response", string(respBytes))
		}
		return nil, fmt.Errorf("invalid response format from cloud CLI: expected 'data', 'output', or 'result' field with string value")
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		s.logger.Error("Failed to parse EFS mount targets JSON", "error", err, "output_preview", truncateString(output, 200))
		return nil, fmt.Errorf("failed to parse EFS mount targets response: %w", err)
	}

	s.logger.Info("Successfully fetched EFS mount targets from AWS",
		"file_system_id", fileSystemID,
		"mount_target_count", len(result.MountTargets))

	return result.MountTargets, nil
}

// createCloudWatchEdges creates edges for CloudWatch resources, including VPC Flow Logs and Log Groups
func (s *AWSSource) createCloudWatchEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Separate CloudWatch nodes by type
	vpcFlowLogNodes := make([]*core.DbNode, 0)
	logGroupNodes := make([]*core.DbNode, 0)

	for _, node := range nodes {
		if nodeType, ok := node.Properties["type"].(string); ok {
			switch nodeType {
			case "vpc-flow-log":
				vpcFlowLogNodes = append(vpcFlowLogNodes, node)
			case "log-group":
				logGroupNodes = append(logGroupNodes, node)
			}
		}
	}

	// Process VPC Flow Logs
	if len(vpcFlowLogNodes) > 0 {
		flowLogEdges := s.createVPCFlowLogEdges(reqCtx, vpcFlowLogNodes, lookup, req)
		edges = append(edges, flowLogEdges...)
	}

	// Process Log Groups
	if len(logGroupNodes) > 0 {
		logGroupEdges := s.createLogGroupEdges(logGroupNodes, lookup, req)
		edges = append(edges, logGroupEdges...)
	}

	return edges
}

// createCloudTrailEdges creates edges for CloudTrail Trails and Event Data Stores
// CloudTrail -> S3 Bucket (PUBLISHES_TO): Log storage destination
// CloudTrail -> KMS Key (IS_ENCRYPTED_BY): Handled by createKMSEdges
// CloudTrail -> SNS Topic (PUBLISHES_TO): Notifications
// CloudTrail -> CloudWatch Log Group (PUBLISHES_TO): Log forwarding
func (s *AWSSource) createCloudTrailEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "AWSCloudTrail" {
			continue
		}

		nodeName, _ := node.Properties["name"].(string)

		// 1. CloudTrail -> S3 Bucket (PUBLISHES_TO)
		if s3BucketName, ok := node.Properties["s3_bucket_name"].(string); ok && s3BucketName != "" {
			for _, storageNode := range lookup.byNodeType[core.NodeTypeStorage] {
				storageName, _ := storageNode.Properties["name"].(string)
				storageServiceName, _ := storageNode.Properties["service_name"].(string)
				if storageName == s3BucketName && storageServiceName == "AmazonS3" {
					edges = append(edges, s.createEdge(node, storageNode, core.RelationshipPublishesTo,
						map[string]interface{}{"connection_type": "cloudtrail_log_destination"}, req))
					s.logger.Debug("Created CloudTrail to S3 edge",
						"trail_name", nodeName,
						"s3_bucket", s3BucketName)
					break
				}
			}
		}

		// 2. CloudTrail -> KMS Key (IS_ENCRYPTED_BY)
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			// Look up KMS key by ARN or key ID
			if kmsNode, exists := lookup.byARN[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "trail",
					}, req))
				s.logger.Debug("Created CloudTrail to KMS edge",
					"trail_name", nodeName,
					"kms_key_id", kmsKeyId)
			} else if kmsNode, exists := lookup.byResourceID[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "trail",
					}, req))
				s.logger.Debug("Created CloudTrail to KMS edge",
					"trail_name", nodeName,
					"kms_key_id", kmsKeyId)
			}
		}

		// 3. CloudTrail -> SNS Topic (PUBLISHES_TO)
		if snsTopicArn, ok := node.Properties["sns_topic_arn"].(string); ok && snsTopicArn != "" {
			if snsNode, exists := lookup.byARN[snsTopicArn]; exists {
				edges = append(edges, s.createEdge(node, snsNode, core.RelationshipPublishesTo,
					map[string]interface{}{"connection_type": "cloudtrail_notification"}, req))
				s.logger.Debug("Created CloudTrail to SNS edge",
					"trail_name", nodeName,
					"sns_topic_arn", snsTopicArn)
			}
		}

		// 4. CloudTrail -> CloudWatch Log Group (PUBLISHES_TO)
		if cwLogsArn, ok := node.Properties["cloudwatch_logs_log_group_arn"].(string); ok && cwLogsArn != "" {
			if cwNode, exists := lookup.byARN[cwLogsArn]; exists {
				edges = append(edges, s.createEdge(node, cwNode, core.RelationshipPublishesTo,
					map[string]interface{}{"connection_type": "cloudtrail_cloudwatch_integration"}, req))
				s.logger.Debug("Created CloudTrail to CloudWatch edge",
					"trail_name", nodeName,
					"log_group_arn", cwLogsArn)
			}
		}
	}

	s.logger.Info("Created CloudTrail edges", "edges_created", len(edges))

	return edges
}

// createVPCFlowLogEdges creates edges for VPC Flow Logs
func (s *AWSSource) createVPCFlowLogEdges(reqCtx *security.RequestContext, vpcFlowLogNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Use the Nudgebee account ID from the request
	if req.CloudAccountID == "" {
		s.logger.Warn("Cannot create VPC Flow Log edges: Cloud account ID not found")
		return edges
	}

	// Fetch all VPC Flow Logs from AWS using cloud collector CLI
	flowLogData, err := s.fetchVPCFlowLogDataFromAWS(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Error("Failed to fetch VPC Flow Log data from AWS", "error", err)
		return edges
	}

	s.logger.Info("Fetched VPC Flow Log data from AWS",
		"account", req.CloudAccountID,
		"flow_log_count", len(flowLogData))

	// Create a map of flow log ID to flow log data
	flowLogDataMap := make(map[string]*VPCFlowLog)
	for _, flowLog := range flowLogData {
		flowLogDataMap[flowLog.FlowLogId] = flowLog
	}

	// Process each VPC Flow Log node
	for _, node := range vpcFlowLogNodes {
		// Get the flow log ID from node properties
		flowLogID, hasFlowLogID := getStringProperty(node, "resource_id")
		if !hasFlowLogID {
			flowLogID, hasFlowLogID = getStringProperty(node, "external_resource_id")
		}
		if !hasFlowLogID {
			s.logger.Debug("VPC Flow Log node missing resource_id",
				"node_name", node.Properties["name"])
			continue
		}

		// Find the flow log data from AWS CLI response
		flowLogInfo, found := flowLogDataMap[flowLogID]
		if !found {
			s.logger.Debug("VPC Flow Log not found in AWS CLI response",
				"flow_log_id", flowLogID)
			continue
		}

		// Update node properties with fresh metadata from AWS
		s.updateVPCFlowLogNodeMetadata(node, flowLogInfo)

		// 1. VPC Flow Log → Monitored Resource (VPC/Subnet/ENI)
		if flowLogInfo.ResourceId != "" {
			// Try to find the monitored resource
			if resourceNode, exists := lookup.byResourceID[flowLogInfo.ResourceId]; exists {
				edges = append(edges, s.createEdge(node, resourceNode, core.RelationshipEmitsLogsTo,
					map[string]interface{}{
						"connection_type": "flow_log_monitors",
						"traffic_type":    flowLogInfo.TrafficType,
					}, req))
			}
		}

		// 2. VPC Flow Log → CloudWatch Log Group (if destination is CloudWatch Logs)
		if flowLogInfo.LogDestinationType == "cloud-watch-logs" && flowLogInfo.LogGroupName != "" {
			// Try to find the CloudWatch Log Group node
			for _, cwNode := range lookup.byNodeType[core.NodeTypeLogAggregator] {
				if nodeType, ok := cwNode.Properties["type"].(string); ok && nodeType == "log-group" {
					if logGroupName, ok := getStringProperty(cwNode, "name"); ok && logGroupName == flowLogInfo.LogGroupName {
						edges = append(edges, s.createEdge(node, cwNode, core.RelationshipPublishesTo,
							map[string]interface{}{
								"connection_type":  "flow_log_destination",
								"destination_type": "cloud-watch-logs",
							}, req))
						break
					}
				}
			}
		}

		// 3. VPC Flow Log → S3 Bucket (if destination is S3)
		if flowLogInfo.LogDestinationType == "s3" && flowLogInfo.LogDestination != "" {
			// Extract bucket name from ARN (format: arn:aws:s3:::bucket-name or arn:aws:s3:::bucket-name/prefix)
			bucketARN := flowLogInfo.LogDestination
			// Try to find the S3 bucket node
			s3Node, exit := lookup.byResourceID[GetResourceIDFromARN(bucketARN)]
			if exit {
				edges = append(edges, s.createEdge(node, s3Node, core.RelationshipPublishesTo,
					map[string]interface{}{
						"connection_type":  "flow_log_destination",
						"destination_type": "s3",
					}, req))
			}
		}
	}

	s.logger.Info("Created VPC Flow Log edges", "edges_created", len(edges))

	return edges
}

// createLogGroupEdges creates edges for CloudWatch Log Groups
// Maps log groups to the resources they monitor based on naming patterns
func (s *AWSSource) createLogGroupEdges(logGroupNodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, logGroupNode := range logGroupNodes {
		// Get log group name from node properties
		logGroupName, hasName := getStringProperty(logGroupNode, "name")
		if !hasName {
			s.logger.Debug("Log group node missing name property",
				"node_id", logGroupNode.ID)
			continue
		}

		// Parse log group name to identify monitored resource
		monitoredResources := s.parseLogGroupName(logGroupName, lookup)

		// Create MONITORS edges to identified resources
		for _, resource := range monitoredResources {
			edges = append(edges, s.createEdge(resource.node, logGroupNode, core.RelationshipEmitsLogsTo,
				map[string]interface{}{
					"connection_type":  "log_group_monitors",
					"resource_type":    string(resource.node.NodeType),
					"log_type":         resource.logType,
					"log_group_name":   logGroupName,
					"matching_pattern": resource.pattern,
				}, req))

			s.logger.Debug("Created log group monitoring edge",
				"log_group", logGroupName,
				"monitored_resource", resource.node.Properties["name"],
				"resource_type", resource.node.NodeType,
				"log_type", resource.logType)
		}
	}

	s.logger.Info("Created CloudWatch Log Group edges",
		"log_group_count", len(logGroupNodes),
		"edges_created", len(edges))

	return edges
}

// MonitoredResource represents a resource identified from log group name parsing
type MonitoredResource struct {
	node    *core.DbNode // The monitored resource node
	logType string       // Type of logs (e.g., "postgresql", "error", "slow_query")
	pattern string       // The pattern that matched
}

// parseLogGroupName parses a CloudWatch log group name to identify monitored resources
// Supports common AWS service log group naming patterns
func (s *AWSSource) parseLogGroupName(logGroupName string, lookup *NodeLookup) []MonitoredResource {
	results := make([]MonitoredResource, 0)

	// Pattern 1: RDS Instance Logs - /aws/rds/instance/{instance-name}/{log-type}
	// Example: /aws/rds/instance/main/postgresql
	if strings.HasPrefix(logGroupName, "/aws/rds/instance/") {
		parts := strings.Split(logGroupName, "/")
		if len(parts) >= 5 {
			instanceName := parts[4]
			logType := ""
			if len(parts) >= 6 {
				logType = parts[5]
			}

			// Find RDS instance by name
			for _, dbNode := range lookup.byNodeType[core.NodeTypeDatabase] {
				if name, ok := getStringProperty(dbNode, "name"); ok {
					if name == instanceName {
						// Verify it's RDS (not DynamoDB)
						if serviceName, ok := getStringProperty(dbNode, "service_name"); ok {
							if serviceName == "AmazonRDS" {
								results = append(results, MonitoredResource{
									node:    dbNode,
									logType: logType,
									pattern: "rds_instance",
								})
								break
							}
						}
					}
				}
			}
		}
	}

	// Pattern 2: RDS Cluster Logs - /aws/rds/cluster/{cluster-name}/{log-type}
	// Example: /aws/rds/cluster/my-aurora-cluster/postgresql
	if strings.HasPrefix(logGroupName, "/aws/rds/cluster/") {
		parts := strings.Split(logGroupName, "/")
		if len(parts) >= 5 {
			clusterName := parts[4]
			logType := ""
			if len(parts) >= 6 {
				logType = parts[5]
			}

			// Find RDS cluster by name
			for _, dbNode := range lookup.byNodeType[core.NodeTypeDatabase] {
				if name, ok := getStringProperty(dbNode, "name"); ok {
					if name == clusterName {
						// Verify it's RDS
						if serviceName, ok := getStringProperty(dbNode, "service_name"); ok {
							if serviceName == "AmazonRDS" {
								results = append(results, MonitoredResource{
									node:    dbNode,
									logType: logType,
									pattern: "rds_cluster",
								})
								break
							}
						}
					}
				}
			}
		}
	}

	// Pattern 3: RDS Enhanced Monitoring - RDSOSMetrics
	// This log group contains OS-level metrics for all RDS instances with enhanced monitoring
	// We match it to RDS instances that have EnhancedMonitoringResourceArn in their metadata
	if logGroupName == "RDSOSMetrics" {
		for _, dbNode := range lookup.byNodeType[core.NodeTypeDatabase] {
			if meta, hasMeta := getMetadataMap(dbNode); hasMeta {
				if enhancedMonARN, ok := meta["EnhancedMonitoringResourceArn"].(string); ok && enhancedMonARN != "" {
					// Check if this RDS instance's enhanced monitoring ARN references this log group
					if strings.Contains(enhancedMonARN, "log-group:RDSOSMetrics") {
						results = append(results, MonitoredResource{
							node:    dbNode,
							logType: "enhanced_monitoring",
							pattern: "rds_enhanced_monitoring",
						})
					}
				}
			}
		}
	}

	// Pattern 4: Lambda Function Logs - /aws/lambda/{function-name}
	// Example: /aws/lambda/my-function
	if strings.HasPrefix(logGroupName, "/aws/lambda/") {
		functionName := strings.TrimPrefix(logGroupName, "/aws/lambda/")

		// Find Lambda function by name
		for _, lambdaNode := range lookup.byNodeType[core.NodeTypeServerlessFunction] {
			if name, ok := getStringProperty(lambdaNode, "name"); ok {
				if name == functionName {
					results = append(results, MonitoredResource{
						node:    lambdaNode,
						logType: "function_logs",
						pattern: "lambda_function",
					})
					break
				}
			}
		}
	}

	// Pattern 5: ECS Container Logs - /aws/ecs/{cluster-name} or /ecs/{service-name}
	// Example: /aws/ecs/my-cluster
	if strings.HasPrefix(logGroupName, "/aws/ecs/") || strings.HasPrefix(logGroupName, "/ecs/") {
		// Extract cluster or service name
		var resourceName string
		if strings.HasPrefix(logGroupName, "/aws/ecs/") {
			resourceName = strings.TrimPrefix(logGroupName, "/aws/ecs/")
		} else {
			resourceName = strings.TrimPrefix(logGroupName, "/ecs/")
		}

		// Try to find ECS cluster/service (ECS nodes are mapped to CloudResource)
		for _, cloudNode := range lookup.byNodeType[core.NodeTypeManagedCluster] {
			// Filter for AmazonECS resources only
			if serviceName, ok := getStringProperty(cloudNode, "service_name"); ok && serviceName == "AmazonECS" {
				if name, ok := getStringProperty(cloudNode, "name"); ok {
					// Use exact matching to avoid false positives
					if name == resourceName {
						results = append(results, MonitoredResource{
							node:    cloudNode,
							logType: "container_logs",
							pattern: "ecs_container",
						})
						break
					}
				}
			}
		}
	}

	// Pattern 6: API Gateway Logs - /aws/apigateway/{api-name} or /aws/api-gateway/{api-id}/{stage}
	// Example: /aws/apigateway/my-api or /aws/api-gateway/abc123/prod
	if strings.HasPrefix(logGroupName, "/aws/apigateway/") || strings.HasPrefix(logGroupName, "/aws/api-gateway/") {
		// For CloudResource nodes that might be API Gateways
		for _, cloudNode := range lookup.byNodeType[core.NodeTypeCloudResource] {
			if serviceName, ok := getStringProperty(cloudNode, "service_name"); ok {
				if serviceName == "AmazonAPIGateway" {
					if name, ok := getStringProperty(cloudNode, "name"); ok {
						if strings.Contains(logGroupName, name) {
							results = append(results, MonitoredResource{
								node:    cloudNode,
								logType: "api_logs",
								pattern: "api_gateway",
							})
							break
						}
					}
				}
			}
		}
	}

	// Pattern 7: ElastiCache Logs - /aws/elasticache/{cluster-id}
	// Example: /aws/elasticache/my-redis-cluster
	if strings.HasPrefix(logGroupName, "/aws/elasticache/") {
		clusterName := strings.TrimPrefix(logGroupName, "/aws/elasticache/")

		// Find ElastiCache cluster by name
		for _, cacheNode := range lookup.byNodeType[core.NodeTypeCache] {
			if name, ok := getStringProperty(cacheNode, "name"); ok {
				if name == clusterName {
					results = append(results, MonitoredResource{
						node:    cacheNode,
						logType: "slow_log",
						pattern: "elasticache_cluster",
					})
					break
				}
			}
		}
	}

	// Pattern 8: EKS Cluster Logs - /aws/eks/{cluster-name}/cluster
	// Example: /aws/eks/nudgebee/cluster
	if strings.HasPrefix(logGroupName, "/aws/eks/") && strings.HasSuffix(logGroupName, "/cluster") {
		withoutPrefix := strings.TrimPrefix(logGroupName, "/aws/eks/")
		clusterName := strings.TrimSuffix(withoutPrefix, "/cluster")

		for _, clusterNode := range lookup.byNodeType[core.NodeTypeManagedCluster] {
			if name, ok := getStringProperty(clusterNode, "name"); ok && name == clusterName {
				results = append(results, MonitoredResource{
					node:    clusterNode,
					logType: "cluster_logs",
					pattern: "eks_cluster",
				})
				break
			}
		}
	}

	return results
}

// fetchVPCFlowLogDataFromAWS fetches VPC Flow Log metadata from the in-memory meta cache.
func (s *AWSSource) fetchVPCFlowLogDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]*VPCFlowLog, error) {
	rows := s.metaByType["vpc-flow-log"]
	flowLogs := make([]*VPCFlowLog, 0, len(rows))
	for _, row := range rows {
		var fl VPCFlowLog
		if err := unmarshalMetaInto(row, &fl); err != nil {
			s.logger.Warn("Failed to parse VPC Flow Log meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		flowLogs = append(flowLogs, &fl)
	}
	s.logger.Info("Fetched VPC Flow Log data from DB cache", "flow_log_count", len(flowLogs))
	return flowLogs, nil
}

// updateVPCFlowLogNodeMetadata updates a VPC Flow Log node with fresh metadata from AWS
func (s *AWSSource) updateVPCFlowLogNodeMetadata(node *core.DbNode, flowLogInfo *VPCFlowLog) {
	// Update status
	node.Properties["status"] = flowLogInfo.FlowLogStatus

	// Create or update meta map
	meta, hasMeta := getMetadataMap(node)
	if !hasMeta {
		meta = make(map[string]interface{})
		node.Properties["meta"] = meta
	}

	// Add flow log configuration
	meta["FlowLogId"] = flowLogInfo.FlowLogId
	meta["FlowLogStatus"] = flowLogInfo.FlowLogStatus
	meta["ResourceId"] = flowLogInfo.ResourceId
	meta["TrafficType"] = flowLogInfo.TrafficType
	meta["LogDestinationType"] = flowLogInfo.LogDestinationType
	meta["LogDestination"] = flowLogInfo.LogDestination
	meta["LogFormat"] = flowLogInfo.LogFormat
	meta["LogGroupName"] = flowLogInfo.LogGroupName
	meta["DeliverLogsStatus"] = flowLogInfo.DeliverLogsStatus
	meta["MaxAggregationInterval"] = flowLogInfo.MaxAggregationInterval

	if flowLogInfo.DestinationOptions != nil {
		meta["DestinationOptions"] = flowLogInfo.DestinationOptions
	}

	// Add top-level properties for easy access
	node.Properties["traffic_type"] = flowLogInfo.TrafficType
	node.Properties["log_destination_type"] = flowLogInfo.LogDestinationType
	node.Properties["log_destination"] = flowLogInfo.LogDestination
	node.Properties["monitored_resource_id"] = flowLogInfo.ResourceId
	node.Properties["deliver_logs_status"] = flowLogInfo.DeliverLogsStatus

	// Update tags if present
	if len(flowLogInfo.Tags) > 0 {
		tags := make(map[string]string)
		for _, tag := range flowLogInfo.Tags {
			tags[tag.Key] = tag.Value
		}
		node.Properties["tags"] = tags
	}
}

// createEBSEdges creates edges for EBS volumes attached to EC2 instances
func (s *AWSSource) createEBSEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use pre-extracted attachment properties (set by extractEBSMetadata).
		// Raw meta is not stored in node.Properties, so we read the flattened fields.
		instanceID, _ := node.Properties["attached_instance_id"].(string)
		if instanceID == "" {
			continue
		}

		if ec2Node, exists := lookup.byResourceID[instanceID]; exists {
			device, _ := node.Properties["device"].(string)
			attachState, _ := node.Properties["attachment_state"].(string)
			deleteOnTermination, _ := node.Properties["delete_on_termination"].(bool)

			edges = append(edges, s.createEdge(node, ec2Node, core.RelationshipHostedOn,
				map[string]interface{}{
					"connection_type":       "ebs_attachment",
					"device":                device,
					"state":                 attachState,
					"delete_on_termination": deleteOnTermination,
				}, req))
		}
	}

	return edges
}

// createKMSEdges creates edges from resources to KMS keys they use for encryption
func (s *AWSSource) createKMSEdges(lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build a map of KMS key ARN/ID to node for quick lookup
	kmsLookup := make(map[string]*core.DbNode)
	if kmsNodes, exists := lookup.byNodeType[core.NodeTypeEncryptionKey]; exists {
		for _, node := range kmsNodes {
			// Index by ARN
			if arn, ok := node.Properties["arn"].(string); ok && arn != "" {
				kmsLookup[arn] = node
			}
			// Index by resource_id (key ID)
			if resourceID, ok := node.Properties["resource_id"].(string); ok && resourceID != "" {
				kmsLookup[resourceID] = node
			}
		}
	}

	// If no KMS keys found, return early
	if len(kmsLookup) == 0 {
		return edges
	}

	// Check all node types for KMS key references
	for nodeType, nodeList := range lookup.byNodeType {
		switch nodeType {
		case core.NodeTypeDatabase:
			// Handle RDS, Aurora, etc.
			edges = append(edges, s.createKMSEdgesForDatabases(nodeList, kmsLookup, req)...)

		case core.NodeTypeCloudResource:
			// Handle EBS volumes and other storage resources
			edges = append(edges, s.createKMSEdgesForStorage(nodeList, kmsLookup, req)...)

		case core.NodeTypeLogAggregator:
			// Handle CloudWatch Log Groups
			edges = append(edges, s.createKMSEdgesForCloudWatch(nodeList, kmsLookup, req)...)

		default:
			// Check any other resource type for KMS key references
			edges = append(edges, s.createKMSEdgesGeneric(nodeList, kmsLookup, req)...)
		}
	}

	return edges
}

// createKMSEdgesForDatabases creates edges from RDS instances/clusters to their KMS keys
func (s *AWSSource) createKMSEdgesForDatabases(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check for storage encryption KMS key
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "storage",
					}, req))
			}
		}

		// Check for Performance Insights KMS key (RDS specific)
		if piKmsKeyId, ok := node.Properties["performance_insights_kms_key_id"].(string); ok && piKmsKeyId != "" {
			if kmsNode, exists := kmsLookup[piKmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "performance_insights",
					}, req))
			}
		}
	}

	return edges
}

// createKMSEdgesForStorage creates edges from EBS volumes and other storage to their KMS keys
func (s *AWSSource) createKMSEdgesForStorage(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Only process EBS volumes (service_name = AmazonEC2, type = storage/volume)
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "AmazonEC2" && serviceName != "AWSS3" && serviceName != "AmazonEFS" {
			continue
		}

		// Check if encrypted
		encrypted, _ := node.Properties["encrypted"].(bool)
		if !encrypted {
			continue
		}

		// Get KMS key ID
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				// Determine encryption type based on service
				var encryptionType string
				switch serviceName {
				case "AWSS3":
					encryptionType = "bucket"
				case "AmazonEFS":
					encryptionType = "filesystem"
				default:
					encryptionType = "volume"
				}

				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": encryptionType,
					}, req))
			}
		}
	}

	return edges
}

// createKMSEdgesForCloudWatch creates edges from CloudWatch Log Groups to their KMS keys
func (s *AWSSource) createKMSEdgesForCloudWatch(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check for KMS key in CloudWatch Log Groups
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "logs",
					}, req))
			}
		}
	}

	return edges
}

// createKMSEdgesGeneric creates edges for any resource type with KmsKeyId in properties
func (s *AWSSource) createKMSEdgesGeneric(nodes []*core.DbNode, kmsLookup map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Check for any KmsKeyId field in properties
		if kmsKeyId, ok := node.Properties["kms_key_id"].(string); ok && kmsKeyId != "" {
			if kmsNode, exists := kmsLookup[kmsKeyId]; exists {
				// Get service name for context
				serviceName, _ := node.Properties["service_name"].(string)

				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"connection_type": "encrypted_by",
						"encryption_type": "default",
						"service":         serviceName,
					}, req))
			}
		}
	}

	return edges
}

// createDefaultVPCEdges creates basic VPC relationship for node types without specific logic
func (s *AWSSource) createDefaultVPCEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// If node has vpc_id, connect it to the VPC
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.byResourceID[vpcID]; exists {
				edge := core.NewEdge(
					node.ID,
					vpcNode.ID,
					core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "vpc",
					},
					req.TenantID,
					req.CloudAccountID,
					"aws",
				)
				edges = append(edges, edge)
			}
		}
	}

	return edges
}

// createBackupVaultEdges creates edges for AWS Backup Vault resources
// BackupVault → EncryptionKey (IS_ENCRYPTED_BY)
func (s *AWSSource) createBackupVaultEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// Check for encryption key ARN in metadata
		if encryptionKeyArn, ok := meta["EncryptionKeyArn"].(string); ok && encryptionKeyArn != "" {
			// Look up by ARN
			if kmsNode, exists := lookup.byResourceID[encryptionKeyArn]; exists {
				edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
					map[string]interface{}{
						"encryption_key_arn": encryptionKeyArn,
					}, req))
			} else {
				// Try to find by key ID (last part of ARN)
				parts := strings.Split(encryptionKeyArn, "/")
				if len(parts) > 0 {
					keyID := parts[len(parts)-1]
					if kmsNode, exists := lookup.byResourceID[keyID]; exists {
						edges = append(edges, s.createEdge(node, kmsNode, core.RelationshipIsEncryptedBy,
							map[string]interface{}{
								"encryption_key_arn": encryptionKeyArn,
								"encryption_key_id":  keyID,
							}, req))
					}
				}
			}
		}
	}

	return edges
}

// createBackupPolicyEdges creates edges for AWS Backup Plan resources
// BackupPolicy → BackupVault (STORES_IN)
func (s *AWSSource) createBackupPolicyEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use the already-extracted target_backup_vault_name from node properties
		// (extracted by extractBackupPolicyMetadata from PlanDetails.Rules)
		targetVaultName, ok := node.Properties["target_backup_vault_name"].(string)
		if !ok || targetVaultName == "" {
			continue
		}

		// Look up backup vault by name
		if vaultNodes, exists := lookup.byNodeType[core.NodeTypeBackupVault]; exists {
			for _, vaultNode := range vaultNodes {
				if name, ok := vaultNode.Properties["name"].(string); ok && name == targetVaultName {
					edges = append(edges, s.createEdge(node, vaultNode, core.RelationshipStoresIn,
						map[string]interface{}{
							"target_vault_name": targetVaultName,
						}, req))
					break
				}
			}
		}
	}

	return edges
}

// createSESEdges creates edges from SES resources to SNS topics for notifications
// EmailService → Topic (PUBLISHES_TO) for bounce/delivery/complaint notifications
func (s *AWSSource) createSESEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, ok := getMetadataMap(node)
		if !ok {
			continue
		}

		// For identities: check notification topics (BounceTopic, DeliveryTopic, ComplaintTopic)
		if notificationAttrs, ok := meta["NotificationAttributes"].(map[string]interface{}); ok {
			for _, topicKey := range []string{"BounceTopic", "DeliveryTopic", "ComplaintTopic"} {
				if topicArn, ok := notificationAttrs[topicKey].(string); ok && topicArn != "" {
					if snsNode, exists := lookup.byARN[topicArn]; exists {
						edges = append(edges, s.createEdge(node, snsNode, core.RelationshipPublishesTo,
							map[string]interface{}{
								"connection_type": "ses_notification",
								"event_type":      strings.ToLower(strings.TrimSuffix(topicKey, "Topic")),
							}, req))
					}
				}
			}
		}

		// For configuration-sets: check EventDestinations
		if eventDests, ok := meta["EventDestinations"].([]interface{}); ok {
			for _, dest := range eventDests {
				if destMap, ok := dest.(map[string]interface{}); ok {
					// Check for SNS destination
					if snsDestination, ok := destMap["SNSDestination"].(map[string]interface{}); ok {
						if topicArn, ok := snsDestination["TopicARN"].(string); ok && topicArn != "" {
							if snsNode, exists := lookup.byARN[topicArn]; exists {
								edges = append(edges, s.createEdge(node, snsNode, core.RelationshipPublishesTo,
									map[string]interface{}{
										"connection_type": "ses_event_destination",
									}, req))
							}
						}
					}
				}
			}
		}
	}

	return edges
}

// createSecurityHubEdges creates edges from SecurityHub standards to hub
// SecurityService (standard) → SecurityService (hub) (BELONGS_TO)
func (s *AWSSource) createSecurityHubEdges(nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Find the hub node(s) for this account
	hubNodes := make([]*core.DbNode, 0)
	for _, node := range lookup.byNodeType[core.NodeTypeSecurityService] {
		if serviceName, ok := node.Properties["service_name"].(string); ok && serviceName == "AWSSecurityHub" {
			if resourceType, ok := node.Properties["type"].(string); ok && resourceType == "hub" {
				hubNodes = append(hubNodes, node)
			}
		}
	}

	// Create edges from standards to their hub
	for _, node := range nodes {
		if resourceType, ok := node.Properties["type"].(string); ok && resourceType == "standard" {
			for _, hubNode := range hubNodes {
				// Match by account
				nodeAccount, _ := node.Properties["nb_account_id"].(string)
				hubAccount, _ := hubNode.Properties["nb_account_id"].(string)
				if nodeAccount == hubAccount {
					edges = append(edges, s.createEdge(node, hubNode, core.RelationshipBelongsTo,
						map[string]interface{}{
							"connection_type": "security_standard",
						}, req))
				}
			}
		}
	}

	return edges
}

// createPublicIPEdges creates edges for Elastic IP (PublicIP) resources
// PublicIP → ComputeInstance (ASSOCIATED_WITH), PublicIP → NetworkInterface (ASSOCIATED_WITH)
func (s *AWSSource) createPublicIPEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	if len(nodes) == 0 {
		return edges
	}

	// Batch fetch ALL Elastic IPs in one API call
	var eipDataMap map[string]*PublicIPData
	if req.CloudAccountID != "" {
		data, err := s.fetchAllPublicIPDataFromAWS(reqCtx, req, req.CloudAccountID)
		if err != nil {
			s.logger.Error("Failed to batch fetch Elastic IP data from AWS",
				"error", err)
		} else {
			eipDataMap = data
		}
	}

	// Build reverse index: resource_id → NetworkInterface node for O(1) fallback lookup
	// when lookup.byResourceID misses NLB-created ENIs that aren't in the standard CLI output.
	eniByResourceID := make(map[string]*core.DbNode, len(lookup.byNodeType[core.NodeTypeNetworkInterface]))
	for _, n := range lookup.byNodeType[core.NodeTypeNetworkInterface] {
		if rid, _ := n.Properties["resource_id"].(string); rid != "" {
			eniByResourceID[rid] = n
		}
	}

	// Process nodes using the pre-fetched data
	for _, node := range nodes {
		// Get allocation ID
		allocationID, _ := node.Properties["resource_id"].(string)
		if allocationID == "" {
			if name, ok := node.Properties["name"].(string); ok {
				allocationID = name
			}
		}

		// Lookup from batch-fetched data instead of making API call
		var eipData *PublicIPData
		if eipDataMap != nil && allocationID != "" {
			if data, exists := eipDataMap[allocationID]; exists {
				eipData = data
				// Enrich node with fetched data
				node.Properties["public_ip"] = data.PublicIp
				node.Properties["private_ip"] = data.PrivateIpAddress
				node.Properties["domain"] = data.Domain
			}
		}

		if eipData == nil {
			continue
		}

		// PublicIP → ComputeInstance (ASSOCIATED_WITH)
		if eipData.InstanceId != "" {
			if ec2Node, exists := lookup.byResourceID[eipData.InstanceId]; exists {
				edges = append(edges, s.createEdge(node, ec2Node, core.RelationshipAssociatedWith,
					map[string]interface{}{
						"association_id": eipData.AssociationId,
						"public_ip":      eipData.PublicIp,
						"private_ip":     eipData.PrivateIpAddress,
					}, req))
			} else {
				s.logger.Warn("PublicIP EC2 instance not found in lookup", "allocation_id", allocationID, "instance_id", eipData.InstanceId)
			}
		}

		// PublicIP → NetworkInterface (ASSOCIATED_WITH)
		if eipData.NetworkInterfaceId != "" {
			eniEdgeProps := map[string]interface{}{
				"association_id":       eipData.AssociationId,
				"public_ip":            eipData.PublicIp,
				"private_ip":           eipData.PrivateIpAddress,
				"network_interface_id": eipData.NetworkInterfaceId,
			}

			eniNode, exists := lookup.byResourceID[eipData.NetworkInterfaceId]
			if !exists {
				// ENI may have been replaced by createENIEdges (NLB-created ENIs are often
				// dropped from the in-memory lookup because they don't show in standard ENI
				// CLI output). Fall back to the pre-built resource_id index.
				eniNode, exists = eniByResourceID[eipData.NetworkInterfaceId]
			}

			if exists {
				edges = append(edges, s.createEdge(node, eniNode, core.RelationshipAssociatedWith, eniEdgeProps, req))

				// If this ENI belongs to a LoadBalancer (NLB pattern: description = "ELB {lb-name}"),
				// also create a direct PublicIP → LoadBalancer edge so the EIP → LB hop is visible.
				desc, _ := eniNode.Properties["description"].(string)
				if strings.HasPrefix(desc, "ELB ") {
					lbName := strings.TrimPrefix(desc, "ELB ")
					for _, lbNode := range lookup.byNodeType[core.NodeTypeLoadBalancer] {
						if n, _ := lbNode.Properties["name"].(string); n == lbName {
							edges = append(edges, s.createEdge(node, lbNode, core.RelationshipAssociatedWith,
								map[string]interface{}{
									"association_id":       eipData.AssociationId,
									"public_ip":            eipData.PublicIp,
									"network_interface_id": eipData.NetworkInterfaceId,
									"connection_type":      "nlb_eip",
								}, req))
							s.logger.Debug("Created direct PublicIP → LoadBalancer edge via NLB ENI",
								"public_ip", eipData.PublicIp,
								"lb_name", lbName)
							break
						}
					}
				}
			} else {
				s.logger.Warn("PublicIP ENI not found in lookup", "allocation_id", allocationID, "eni_id", eipData.NetworkInterfaceId)
			}
		}
	}

	return edges
}

// createEFSEdges creates edges for EFS (Elastic File System) resources
// EFS → VPC (BELONGS_TO), EFS → Subnet (HOSTED_ON), EFS → ENI (ASSOCIATED_WITH)
func (s *AWSSource) createEFSEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Only process AmazonEFS resources
		if service, ok := node.Properties["service"].(string); !ok || service != "AmazonEFS" {
			continue
		}

		// Get file system ID
		fileSystemID, _ := node.Properties["resource_id"].(string)
		if fileSystemID == "" {
			if name, ok := node.Properties["name"].(string); ok {
				fileSystemID = name
			}
		}

		// Fetch mount targets from AWS CLI
		var mountTargets []EFSMountTargetData
		if fileSystemID != "" && req.CloudAccountID != "" {
			targets, err := s.fetchEFSMountTargetsFromAWS(reqCtx, req, req.CloudAccountID, fileSystemID)
			if err != nil {
				s.logger.Error("Failed to fetch EFS mount targets from AWS",
					"file_system_id", fileSystemID,
					"error", err)
			} else {
				mountTargets = targets
			}
		}

		if len(mountTargets) == 0 {
			continue
		}

		// Track unique VPCs to avoid duplicate edges
		vpcEdgesCreated := make(map[string]bool)

		for _, mt := range mountTargets {
			// EFS → VPC (BELONGS_TO) - only create one edge per VPC
			if mt.VpcId != "" && !vpcEdgesCreated[mt.VpcId] {
				if vpcNode, exists := lookup.byResourceID[mt.VpcId]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipBelongsTo,
						map[string]interface{}{
							"connection_type": "vpc",
							"vpc_id":          mt.VpcId,
						}, req))
					vpcEdgesCreated[mt.VpcId] = true
				} else {
					s.logger.Warn("EFS mount target VPC not found in lookup", "fs_id", fileSystemID, "vpc_id", mt.VpcId)
				}
				// Store VPC ID in node properties
				node.Properties["vpc_id"] = mt.VpcId
			}

			// EFS → Subnet (HOSTED_ON) - one edge per mount target subnet
			if mt.SubnetId != "" {
				if subnetNode, exists := lookup.byResourceID[mt.SubnetId]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{
							"connection_type":        "mount_target",
							"mount_target_id":        mt.MountTargetId,
							"subnet_id":              mt.SubnetId,
							"availability_zone":      mt.AvailabilityZoneName,
							"availability_zone_id":   mt.AvailabilityZoneId,
							"mount_target_ip":        mt.IpAddress,
							"mount_target_lifecycle": mt.LifeCycleState,
						}, req))
				} else {
					s.logger.Warn("EFS mount target Subnet not found in lookup", "fs_id", fileSystemID, "subnet_id", mt.SubnetId)
				}
			}

			// EFS → NetworkInterface (ASSOCIATED_WITH) - mount target ENI
			if mt.NetworkInterfaceId != "" {
				if eniNode, exists := lookup.byResourceID[mt.NetworkInterfaceId]; exists {
					edges = append(edges, s.createEdge(node, eniNode, core.RelationshipAssociatedWith,
						map[string]interface{}{
							"connection_type":      "mount_target_eni",
							"mount_target_id":      mt.MountTargetId,
							"network_interface_id": mt.NetworkInterfaceId,
							"mount_target_ip":      mt.IpAddress,
						}, req))
				}
			}
		}
	}

	return edges
}

// fetchCloudFormationStackResources fetches all resources managed by a CloudFormation stack.
// Results are cached for 2 hours to avoid redundant AWS API calls across graph builds.
func (s *AWSSource) fetchCloudFormationStackResources(reqCtx *security.RequestContext, req *core.SourceBuildRequest, accountID string, stackName string) ([]CloudFormationStackResource, error) {
	// Validate inputs to prevent command injection
	if err := validateStackName(stackName); err != nil {
		return nil, fmt.Errorf("invalid stack name: %w", err)
	}
	if err := validateAWSRegion(req.Region); err != nil {
		return nil, fmt.Errorf("invalid region: %w", err)
	}

	// Check cache first
	cacheKey := fmt.Sprintf("%s:%s:%s", accountID, req.Region, stackName)
	if cached, found := common.CacheGet("cfn_stack_resources", cacheKey); found {
		var resources []CloudFormationStackResource
		if unmarshalErr := json.Unmarshal(cached, &resources); unmarshalErr == nil {
			s.logger.Debug("CloudFormation stack resources cache hit",
				"stack_name", stackName,
				"account_id", accountID,
				"resource_count", len(resources))
			return resources, nil
		}
	}

	// Build AWS CLI command to list stack resources
	// Arguments are safe after validation above
	cmd := fmt.Sprintf("aws cloudformation list-stack-resources --stack-name %s --output json", stackName)

	// Add region filter if specified
	if req.Region != "" {
		cmd = fmt.Sprintf("aws cloudformation list-stack-resources --stack-name %s --region %s --output json", stackName, req.Region)
	}

	s.logger.Debug("Fetching CloudFormation stack resources",
		"stack_name", stackName,
		"account_id", accountID,
		"region", req.Region)

	// Execute AWS CLI command via cloud collector
	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch CloudFormation stack resources: %w", err)
	}

	// Parse response
	var result struct {
		StackResourceSummaries []CloudFormationStackResource `json:"StackResourceSummaries"`
	}

	// Try different response formats
	var output string
	if dataStr, ok := resp["data"].(string); ok && dataStr != "" {
		output = dataStr
	} else if outputStr, ok := resp["output"].(string); ok && outputStr != "" {
		output = outputStr
	} else if resultStr, ok := resp["result"].(string); ok && resultStr != "" {
		output = resultStr
	} else {
		return nil, fmt.Errorf("invalid response format from cloud CLI")
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse CloudFormation stack resources response: %w", err)
	}

	// Cache the result
	if cacheData, err := json.Marshal(result.StackResourceSummaries); err == nil {
		if err := common.CacheSet("cfn_stack_resources", cacheKey, cacheData); err != nil {
			s.logger.Warn("Failed to cache CloudFormation stack resources",
				"stack_name", stackName, "error", err)
		}
	}

	s.logger.Debug("Successfully fetched CloudFormation stack resources",
		"stack_name", stackName,
		"resource_count", len(result.StackResourceSummaries))

	return result.StackResourceSummaries, nil
}

// findCloudFormationManagedResource finds a node in the lookup for a CloudFormation stack resource
func (s *AWSSource) findCloudFormationManagedResource(resource CloudFormationStackResource, lookup *NodeLookup) *core.DbNode {
	// Get mapping info for this resource type
	mapping, exists := cloudFormationResourceTypeMap[resource.ResourceType]
	if !exists {
		// Unknown resource type - try both lookup strategies
		if node, found := lookup.byARN[resource.PhysicalResourceId]; found {
			return node
		}
		return lookup.byResourceID[resource.PhysicalResourceId]
	}

	// Try ARN lookup first if PhysicalResourceId might be ARN
	if mapping.LookupByARN {
		if node, found := lookup.byARN[resource.PhysicalResourceId]; found {
			return node
		}
	}

	// Try resource ID lookup
	if node, found := lookup.byResourceID[resource.PhysicalResourceId]; found {
		return node
	}

	// For S3 buckets, the PhysicalResourceId is just the bucket name
	// Try looking up by name within the expected node type
	if mapping.NodeType == core.NodeTypeStorage && resource.ResourceType == "AWS::S3::Bucket" {
		for _, node := range lookup.byNodeType[core.NodeTypeStorage] {
			if name, ok := node.Properties["name"].(string); ok && name == resource.PhysicalResourceId {
				return node
			}
		}
	}

	// For SQS queues, CloudFormation PhysicalResourceId is a queue URL
	// (e.g. https://sqs.us-east-1.amazonaws.com/123456789/queue-name), not an ARN.
	// Extract the queue name from the URL and look up by resource_id.
	if resource.ResourceType == "AWS::SQS::Queue" && strings.HasPrefix(resource.PhysicalResourceId, "https://") {
		parts := strings.Split(resource.PhysicalResourceId, "/")
		if queueName := parts[len(parts)-1]; queueName != "" {
			if node, found := lookup.byResourceID[queueName]; found {
				return node
			}
		}
	}

	return nil
}

// createCloudFormationEdges creates edges from CloudFormation stacks to their managed resources
func (s *AWSSource) createCloudFormationEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Get stack name from properties
		stackName, ok := node.Properties["name"].(string)
		if !ok || stackName == "" {
			s.logger.Debug("CloudFormation stack missing name, skipping",
				"node_id", node.ID)
			continue
		}

		// Fetch stack resources from AWS
		stackResources, err := s.fetchCloudFormationStackResources(reqCtx, req, req.CloudAccountID, stackName)
		if err != nil {
			if strings.Contains(err.Error(), "Stack with id") && strings.Contains(err.Error(), "does not exist") {
				s.logger.Debug("CloudFormation stack not found, skipping resource processing",
					"stack_name", stackName,
					"error", err)
				continue
			}

			s.logger.Warn("failed to fetch CloudFormation stack resources",
				"stack_name", stackName,
				"error", err)
			continue
		}

		s.logger.Debug("Processing CloudFormation stack resources",
			"stack_name", stackName,
			"resource_count", len(stackResources))

		// Create edges for each managed resource
		for _, resource := range stackResources {
			// Skip resources in failed or delete states
			if strings.HasPrefix(resource.ResourceStatus, "DELETE") ||
				strings.HasSuffix(resource.ResourceStatus, "FAILED") {
				continue
			}

			// Find the target node
			targetNode := s.findCloudFormationManagedResource(resource, lookup)
			if targetNode == nil {
				s.logger.Debug("CloudFormation managed resource not found in graph",
					"stack_name", stackName,
					"logical_id", resource.LogicalResourceId,
					"physical_id", resource.PhysicalResourceId,
					"resource_type", resource.ResourceType)
				continue
			}

			// Create MANAGES edge from stack to resource
			edges = append(edges, s.createEdge(node, targetNode, core.RelationshipManages,
				map[string]interface{}{
					"connection_type":     "cloudformation_managed",
					"logical_resource_id": resource.LogicalResourceId,
					"resource_type":       resource.ResourceType,
					"resource_status":     resource.ResourceStatus,
				}, req))

			s.logger.Debug("Created CloudFormation stack to resource edge",
				"stack_name", stackName,
				"target_name", targetNode.Properties["name"],
				"target_type", targetNode.NodeType,
				"logical_id", resource.LogicalResourceId)
		}
	}

	return edges
}

// ensureVPCNodes ensures all referenced VPCs exist, creating inferred nodes if needed
func (s *AWSSource) ensureVPCNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, edges []*core.DbEdge, lookup *NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Track which VPCs are referenced but don't exist
	referencedVPCs := make(map[string]bool)

	// Collect all vpc_id references from nodes
	for _, node := range nodes {
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			referencedVPCs[vpcID] = true
		}
	}

	// Create inferred VPC nodes for missing VPCs
	for vpcID := range referencedVPCs {
		if _, exists := lookup.byResourceID[vpcID]; !exists {
			inferredVPC := s.createInferredVPCNode(reqCtx, vpcID, req)
			nodes = append(nodes, inferredVPC)
			lookup.byResourceID[vpcID] = inferredVPC
			lookup.byNodeType[core.NodeTypeVPC] = append(lookup.byNodeType[core.NodeTypeVPC], inferredVPC)
		}
	}

	return nodes, edges
}

// ensureSubnetNodes ensures all referenced subnet nodes exist, creating inferred nodes if needed
func (s *AWSSource) ensureSubnetNodes(nodes []*core.DbNode, edges []*core.DbEdge, lookup *NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Track which Subnets are referenced but don't exist
	referencedSubnets := make(map[string]bool)

	// Collect all subnet_id references from nodes
	for _, node := range nodes {
		if subnetID, ok := node.Properties["subnet_id"].(string); ok && subnetID != "" {
			referencedSubnets[subnetID] = true
		}

		// Also check in metadata for SubnetId
		if meta, ok := getMetadataMap(node); ok {
			if subnetID, ok := meta["SubnetId"].(string); ok && subnetID != "" {
				referencedSubnets[subnetID] = true
			}
		}
	}

	// Create inferred Subnet nodes for missing Subnets
	for subnetID := range referencedSubnets {
		if _, exists := lookup.byResourceID[subnetID]; !exists {
			inferredSubnet := s.createInferredSubnetNode(subnetID, req)
			nodes = append(nodes, inferredSubnet)
			lookup.byResourceID[subnetID] = inferredSubnet
			lookup.byNodeType[core.NodeTypeSubnet] = append(lookup.byNodeType[core.NodeTypeSubnet], inferredSubnet)
		}
	}

	return nodes, edges
}

// fetchRouteTablesFromAWS fetches all route tables from the in-memory meta cache.
func (s *AWSSource) fetchRouteTablesFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]RouteTableData, error) {
	rows := s.metaByType["route-table"]
	routeTables := make([]RouteTableData, 0, len(rows))
	for _, row := range rows {
		var rt RouteTableData
		if err := unmarshalMetaInto(row, &rt); err != nil {
			s.logger.Warn("Failed to parse route table meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		routeTables = append(routeTables, rt)
	}
	s.logger.Info("Fetched route tables from DB cache", "count", len(routeTables))
	return routeTables, nil
}

// ensureRouteTableNodes fetches route tables via AWS CLI and creates nodes for them
func (s *AWSSource) ensureRouteTableNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, edges []*core.DbEdge, lookup *NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Fetch route tables from AWS
	routeTables, err := s.fetchRouteTablesFromAWS(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Warn("Failed to fetch route tables from AWS, skipping route table nodes",
			"error", err,
			"account_id", req.CloudAccountID)
		return nodes, edges
	}

	// Create RouteTable nodes
	for _, rt := range routeTables {
		// Get name from tags
		name := rt.RouteTableId
		for _, tag := range rt.Tags {
			if tag.Key == "Name" && tag.Value != "" {
				name = tag.Value
				break
			}
		}

		// Check if main route table
		isMain := false
		for _, assoc := range rt.Associations {
			if assoc.Main {
				isMain = true
				break
			}
		}

		// Build labels map from tags
		labels := make(map[string]interface{})
		for _, tag := range rt.Tags {
			labels[tag.Key] = tag.Value
		}

		// Build metadata map for the route table
		metaMap := map[string]interface{}{
			"RouteTableId": rt.RouteTableId,
			"VpcId":        rt.VpcId,
			"OwnerId":      rt.OwnerId,
			"Associations": rt.Associations,
			"Routes":       rt.Routes,
		}

		// Build properties
		properties := map[string]interface{}{
			"name":           name,
			"resource_id":    rt.RouteTableId,
			"route_table_id": rt.RouteTableId,
			"vpc_id":         rt.VpcId,
			"is_main":        isMain,
			"account_number": rt.OwnerId, // AWS account number from route table data
			"region":         req.Region,
			"metadata":       metaMap,
			"labels":         labels,
		}

		// Create temp node for generating unique key
		tempNode := &core.DbNode{
			NodeType:       core.NodeTypeRouteTable,
			Properties:     properties,
			CloudAccountID: req.CloudAccountID,
		}
		uniqueKey := s.GenerateUniqueKey(tempNode)

		// Create the node using core.NewNode
		routeTableNode := core.NewNode(core.NodeTypeRouteTable, uniqueKey, properties, req.TenantID, req.CloudAccountID, "aws")

		// Add to nodes and lookup
		nodes = append(nodes, routeTableNode)
		lookup.byResourceID[rt.RouteTableId] = routeTableNode
		lookup.byNodeType[core.NodeTypeRouteTable] = append(lookup.byNodeType[core.NodeTypeRouteTable], routeTableNode)

		s.logger.Debug("Created route table node",
			"route_table_id", rt.RouteTableId,
			"name", name,
			"vpc_id", rt.VpcId,
			"is_main", isMain)
	}

	s.logger.Info("Created route table nodes from AWS CLI",
		"count", len(routeTables))

	return nodes, edges
}

// propagateVPCNamesToResources updates all resources to use VPC name in hierarchy instead of VPC ID
func (s *AWSSource) propagateVPCNamesToResources(nodes []*core.DbNode, lookup *NodeLookup) {
	for _, node := range nodes {
		// Skip VPC nodes themselves
		if node.NodeType == core.NodeTypeVPC {
			continue
		}

		// Check if this node has a vpc_id
		vpcID, ok := node.Properties["vpc_id"].(string)
		if !ok || vpcID == "" {
			continue
		}

		// Look up the VPC node to get its name
		vpcNode, exists := lookup.byResourceID[vpcID]
		if !exists {
			continue
		}

		// Get VPC name from the VPC node
		vpcName, ok := vpcNode.Properties["name"].(string)
		if !ok || vpcName == "" {
			// Fallback to VPC ID if name not available
			vpcName = vpcID
		}

		// Store VPC name in the resource node properties for hierarchy
		node.Properties["vpc_name_hierarchy"] = vpcName

		// Regenerate unique key for this node to use VPC name in hierarchy
		node.UniqueKey = s.GenerateUniqueKey(node)

		s.logger.Debug("Updated resource hierarchy to use VPC name",
			"resource_name", node.Properties["name"],
			"vpc_id", vpcID,
			"vpc_name", vpcName,
			"new_unique_key", node.UniqueKey)
	}
}

// ========================================================================
// Helper Methods for Edge Creation
// ========================================================================

// createEdge is a helper to create an edge with standard fields
func (s *AWSSource) createEdge(sourceNode, targetNode *core.DbNode, relType core.RelationshipType, properties map[string]interface{}, req *core.SourceBuildRequest) *core.DbEdge {
	return core.NewEdge(
		sourceNode.ID,
		targetNode.ID,
		relType,
		properties,
		req.TenantID,
		req.CloudAccountID,
		"aws",
	)
}

// LoadBalancerTagDescription represents the tag description from describe-tags API
type LoadBalancerTagDescription struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// getStringProperty safely extracts a string property from a node
func getStringProperty(node *core.DbNode, key string) (string, bool) {
	if val, ok := node.Properties[key].(string); ok && val != "" {
		return val, true
	}
	return "", false
}

// extractEndpointAddress accepts the polymorphic shapes AWS returns for
// endpoint fields — either a bare hostname string, or an object of the form
// {"Address": "...", "Port": ...} — and returns the address. Returns "" when
// the input is nil, the wrong shape, or has no usable address.
func extractEndpointAddress(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]interface{}:
		if addr, ok := val["Address"].(string); ok {
			return addr
		}
	}
	return ""
}

// getStringSliceProperty safely extracts a string slice from a node property
// func getStringSliceProperty(node *core.DbNode, key string) ([]string, bool) {
// 	if val, ok := node.Properties[key].([]interface{}); ok && len(val) > 0 {
// 		result := make([]string, 0, len(val))
// 		for _, item := range val {
// 			if str, ok := item.(string); ok && str != "" {
// 				result = append(result, str)
// 			}
// 		}
// 		if len(result) > 0 {
// 			return result, true
// 		}
// 	}
// 	return nil, false
// }

// getMetadataMap safely extracts the meta map from node properties
func getMetadataMap(node *core.DbNode) (map[string]interface{}, bool) {
	if meta, ok := node.Properties["meta"].(map[string]interface{}); ok {
		return meta, true
	}
	return nil, false
}

// unmarshalMetaInto unmarshals a CloudResourceRow's Meta JSON into the given target struct.
func unmarshalMetaInto(row CloudResourceRow, target interface{}) error {
	if len(row.Meta) == 0 {
		return fmt.Errorf("empty meta for resource %s", row.ResourceID)
	}
	return json.Unmarshal(row.Meta, target)
}

// getMapKeys returns the keys of a map for logging purposes
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// truncateString truncates a string to the specified length for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// SetEnabled enables or disables the source
func (s *AWSSource) SetEnabled(enabled bool) {
	s.enabled = enabled
}

// ConvertToKnowledgeGraph converts the graph from this source to KnowledgeGraph format
func (s *AWSSource) ConvertToKnowledgeGraph(graph *core.Graph) core.KnowledgeGraph {
	return core.ConvertGraphToKnowledgeGraph(graph)
}

// ConvertEdgesToKgEdges converts DbEdges to KgEdges for this source
func (s *AWSSource) ConvertEdgesToKgEdges(dbEdges []*core.DbEdge) []core.KgEdge {
	return core.ConvertDbEdgesToKgEdges(dbEdges)
}

// extractLabelValue extracts a string value from a labels map.
// The tags column stores values as string arrays (e.g. {"key": ["value"]}),
// but some callers may store plain strings. Both formats are handled.
func extractLabelValue(labels map[string]interface{}, key string) string {
	val, ok := labels[key]
	if !ok {
		return ""
	}
	// Plain string
	if s, ok := val.(string); ok {
		return s
	}
	// Array of strings (tags column format)
	if arr, ok := val.([]interface{}); ok && len(arr) > 0 {
		if s, ok := arr[0].(string); ok {
			return s
		}
	}
	return ""
}
