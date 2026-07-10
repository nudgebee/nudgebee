package aws

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
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
	sources.RegisterSourceFactory("aws", func(config sources.SourceConfig, logger *slog.Logger) (core.SourceInterface, error) {
		return NewAWSSource(AWSSourceConfig{ServiceTypeFilter: DefaultServiceTypeFilter}, logger)
	}, "AWS cloud resources source (RDS, ElastiCache, S3, EC2, etc.)")

	// Cache CloudFormation stack resources for 2 hours (stacks rarely change,
	// and the KG cron runs hourly at :30 UTC)
	common.CacheCreateNamespace("cfn_stack_resources",
		common.CacheNamespaceWithExpiration(2*time.Hour),
		common.CacheNamespaceWithMaxEntries(500),
	)
}

// AWSSource implements the Source interface for AWS cloud resources
type AWSSource struct {
	sources.BaseSource
	config  AWSSourceConfig
	logger  *slog.Logger
	enabled bool

	// Per-build meta cache populated from already-loaded cloud_resourses rows.
	// Keyed by resource type, then by resource ID. Avoids re-querying the DB
	// for the same data in downstream fetch functions.
	metaByType      map[string][]sources.CloudResourceRow
	metaByTypeAndID map[string]map[string]sources.CloudResourceRow
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

// sources.CloudResourceRow represents a row from the cloud_resourses table
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
		BaseSource: sources.NewBaseSource("aws"),
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
// Overrides sources.BaseSource.GenerateUniqueKey with AWS-specific logic
// Format: aws:{account}:{region}:{NodeType}:{vpc_id}:{name}
func (s *AWSSource) GenerateUniqueKey(node *core.DbNode) string {
	if node == nil {
		return ""
	}

	// Create key components
	keyComponents := core.NewUniqueKeyComponents("aws", node.NodeType)

	// Extract name. For NetworkInterface nodes the `name` property is the
	// ENI's free-form Description, which AWS reuses across multiple physical
	// ENIs (ELB AZ replicas, EKS control-plane ENIs, K8s pod ENIs on the same
	// node). Keying by description collides multiple ENIs onto one UUID and
	// DeduplicateNodes drops the extras. Always prefer the unique eni-id
	// (stored in resource_id) for this node type.
	var name string
	if node.NodeType == core.NodeTypeNetworkInterface {
		name, _ = core.GetNodePropertyString(node, "resource_id")
	}
	if name == "" {
		name, _ = core.GetNodePropertyString(node, "name")
	}
	if name == "" {
		// Try resource_id as fallback (for non-NIC types that hit the empty-name path)
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
	s.metaByType = make(map[string][]sources.CloudResourceRow)
	s.metaByTypeAndID = make(map[string]map[string]sources.CloudResourceRow)
	for _, row := range resources {
		s.metaByType[row.Type] = append(s.metaByType[row.Type], row)
		if _, ok := s.metaByTypeAndID[row.Type]; !ok {
			s.metaByTypeAndID[row.Type] = make(map[string]sources.CloudResourceRow)
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
func (s *AWSSource) fetchAWSResources(ctx context.Context, req *core.SourceBuildRequest) ([]sources.CloudResourceRow, error) {
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
			AND cr.cloud_provider = 'AWS' AND cr.status = 'Active'
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

	var resources []sources.CloudResourceRow
	err = dbManager.Db.Select(&resources, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud_resourses: %w", err)
	}

	s.logger.Info("queried cloud resources from database",
		"count", len(resources),
		"tenant_id", req.TenantID)

	return resources, nil
}

// shouldIncludeResource checks if a resource should be included based on ServiceTypeFilter
// plus a small universal blacklist of rows that AWS auto-creates per region
// and that carry no query value (eg AWS X-Ray's "Default" sampling-rule).
func (s *AWSSource) shouldIncludeResource(resource *sources.CloudResourceRow) bool {
	// Universal noise filter — applies regardless of ServiceTypeFilter config.
	// These resource patterns produce per-region duplicates with no
	// distinguishable identity and no usefulness for traversal. Dropping
	// them at the source removes ~12 orphan CloudResource entries per
	// AWS account in production (see #31016 audit).

	// AmazonInspector emits a synthetic per-region "account" row to mark
	// "Inspector is enabled in this region" — same resource_id across all
	// regions, empty ARN, no traversal target. We lose nothing by skipping.
	if resource.ServiceName == "AmazonInspector" && strings.EqualFold(resource.Type, "account") {
		return false
	}

	// AWS X-Ray auto-creates a "Default" sampling-rule AND a "Default"
	// trace-group in every enabled region. Users don't operate on either
	// default directly — only custom sampling-rules and groups carry user
	// intent. Drop the default copies (both lowercase "group" and any
	// case variant the collector might surface).
	if resource.ServiceName == "AWSXRay" && resource.ResourceID == "Default" {
		switch strings.ToLower(resource.Type) {
		case "sampling-rule", "group":
			return false
		}
	}

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
func (s *AWSSource) convertResourcesToGraph(reqCtx *security.RequestContext, resources []sources.CloudResourceRow, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
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
		if node == nil {
			// Resource was intentionally suppressed (eg IAM Role emitted as ServiceIdentity).
			continue
		}
		nodes = append(nodes, node)
	}

	// Step 2: Build lookup maps for efficient edge creation
	lookup := sources.NewNodeLookup(nodes)

	// Step 2.5: Ensure all infrastructure nodes exist BEFORE creating edges
	// This allows edge creation to find VPC and Subnet nodes in the lookup
	nodes, _ = s.ensureVPCNodes(reqCtx, nodes, []*core.DbEdge{}, lookup, req)
	nodes, _ = s.ensureSubnetNodes(nodes, []*core.DbEdge{}, lookup, req)
	nodes, _ = s.ensureRouteTableNodes(reqCtx, nodes, []*core.DbEdge{}, lookup, req)

	// Step 2.6: Propagate VPC names to all resources and update their unique keys
	// This ensures resources use VPC name in their hierarchy instead of VPC ID
	s.propagateVPCNamesToResources(nodes, lookup)

	// Step 2.7: Fetch + register IAM Roles as ServiceIdentity nodes BEFORE the
	// edge dispatch loop so edge builders that resolve roles via lookup.ByARN
	// (eg the EKS RUNS_AS edge in createEKSEdges) can find them. The
	// ServiceIdentity-emitted edges themselves (ASSUMES chains, EC2/Lambda
	// RUNS_AS via buildServiceIdentityEdges) are still appended after the
	// dispatch loop below.
	var serviceIdentityNodes []*core.DbNode
	var profileRoleMap map[string]string
	if req.CloudAccountID != "" && reqCtx != nil {
		iamRoles, err := s.fetchIAMRolesFromAWS(reqCtx, req, req.CloudAccountID)
		if err != nil {
			s.logger.Warn("Failed to fetch IAM roles; ServiceIdentity nodes will be skipped", "error", err)
		} else {
			serviceIdentityNodes = s.buildServiceIdentityNodes(iamRoles, req)
			nodes = append(nodes, serviceIdentityNodes...)
			for _, n := range serviceIdentityNodes {
				lookup.ByNodeType[core.NodeTypeServiceIdentity] = append(lookup.ByNodeType[core.NodeTypeServiceIdentity], n)
				if arn, ok := n.Properties["arn"].(string); ok && arn != "" {
					lookup.ByARN[arn] = n
				}
			}
			if pm, err := s.fetchInstanceProfileRoleMapFromAWS(reqCtx, req.CloudAccountID); err != nil {
				s.logger.Warn("Failed to fetch instance profiles; EC2→ServiceIdentity edges may be incomplete", "error", err)
			} else {
				profileRoleMap = pm
			}
		}
	}

	// Step 3: Create edges by processing each node type
	edges := make([]*core.DbEdge, 0)

	// Process each node type to create its relationships
	for nodeType, nodeList := range lookup.ByNodeType {
		switch nodeType {
		case core.NodeTypeVPC:
			// VPC↔VPC peering (fetched live per region via the ec2 CLI); VPCs otherwise
			// have no outgoing edges.
			edges = append(edges, s.createVPCPeeringEdges(reqCtx, nodeList, lookup, req)...)

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
			// Declarative-engine path (EC2). Byte-identical to createEC2Edges;
			// see aws_ec2_module.go + fidelity test.
			edges = append(edges, s.createEC2EdgesViaEngine(nodeList, lookup, req)...)
			// AutoScalingGroup nodes + EC2 -BELONGS_TO-> ASG (fetched live per region).
			asgNodes, asgEdges := s.createASGNodesAndEdges(reqCtx, nodeList, lookup, req)
			nodes = append(nodes, asgNodes...)
			edges = append(edges, asgEdges...)

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
				// Declarative-engine path (RDS vertical slice). Emits byte-identical
				// edges to createRDSEdges; see aws_rds_module.go + the fidelity test.
				edges = append(edges, s.createRDSEdgesViaEngine(rdsNodes, lookup, req)...)
				// Aurora cluster-membership + read-replica topology (identifier-matched,
				// so orthogonal to the fidelity-tested attachment edges above).
				edges = append(edges, s.createRDSTopologyEdges(rdsNodes, lookup, req)...)
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
			lookup.ByNodeType[core.NodeTypeLoadBalancer] = validLBNodes
			for _, validNode := range validLBNodes {
				if resourceID, ok := validNode.Properties["resource_id"].(string); ok && resourceID != "" {
					lookup.ByResourceID[resourceID] = validNode
				}
				if arn, ok := validNode.Properties["arn"].(string); ok && arn != "" {
					lookup.ByARN[arn] = validNode
				}
			}

			// Add backend pool nodes and edges
			nodes = append(nodes, backendPoolNodes...)
			edges = append(edges, lbEdges...)

		case core.NodeTypeServerlessFunction:
			edges = append(edges, s.createLambdaEdges(nodeList, lookup, req)...)
			// Container-package Lambdas → ContainerImage nodes + USES_IMAGE edges.
			imgNodes, imgEdges := s.createLambdaImageNodesAndEdges(nodeList, req)
			nodes = append(nodes, imgNodes...)
			edges = append(edges, imgEdges...)

		case core.NodeTypeManagedCluster:
			edges = append(edges, s.createEKSEdges(nodeList, lookup, req)...)

		case core.NodeTypeComputeInstancePool:
			edges = append(edges, s.createEKSNodeGroupEdges(nodeList, lookup, req)...)

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
			lookup.ByNodeType[core.NodeTypeNetworkInterface] = validENINodes
			for _, validNode := range validENINodes {
				if resourceID, ok := validNode.Properties["resource_id"].(string); ok && resourceID != "" {
					lookup.ByResourceID[resourceID] = validNode
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

	// ServiceIdentity edges (ASSUMES chains, EC2/Lambda RUNS_AS). Nodes were
	// already created and registered in step 2.7 above so dispatch-loop edge
	// builders could resolve roles via lookup.ByARN; these edges originate
	// from the ServiceIdentity nodes themselves and don't need to be inside
	// the dispatch loop.
	if len(serviceIdentityNodes) > 0 {
		edges = append(edges, s.buildServiceIdentityEdges(serviceIdentityNodes, lookup, profileRoleMap, req)...)
	}

	return nodes, edges
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
	"AmazonVPC":         {"elastic-ip", "network-interface", "security_group", "subnet", "vpc", "vpc-endpoint", "natgateway", "internet-gateway"},
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
		"AmazonVPC": core.NodeTypeNetworkGateway, // collector writes new rows under AmazonVPC; legacy rows still use AmazonEC2
	},
	"internet-gateway": {
		"AmazonVPC": core.NodeTypeNetworkGateway,
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
	"nodegroup": {
		"AmazonEKS": core.NodeTypeComputeInstancePool,
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
	"AmazonGuardDuty":       core.NodeTypeSecurityService, // threat-detection service; aligns with SecurityHub at the same fallback table
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

// ========================================================================
// Edge Creation Methods - Each method handles edge creation for a specific node type
// ========================================================================

// ENINetworkInterface represents an AWS ENI from describe-network-interfaces
type ENINetworkInterface struct {
	NetworkInterfaceId string `json:"NetworkInterfaceId"`
	SubnetId           string `json:"SubnetId"`
	VpcId              string `json:"VpcId"`
	AvailabilityZone   string `json:"AvailabilityZone"`
	Description        string `json:"Description"`
	InterfaceType      string `json:"InterfaceType"`
	PrivateIpAddress   string `json:"PrivateIpAddress"`
	PrivateIpAddresses []struct {
		PrivateIpAddress string `json:"PrivateIpAddress"`
		Primary          bool   `json:"Primary"`
	} `json:"PrivateIpAddresses"`
	RequesterId string `json:"RequesterId"`
	Status      string `json:"Status"`
	Groups      []struct {
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

// MonitoredResource represents a resource identified from log group name parsing
type MonitoredResource struct {
	node    *core.DbNode // The monitored resource node
	logType string       // Type of logs (e.g., "postgresql", "error", "slow_query")
	pattern string       // The pattern that matched
}

// ========================================================================
// Helper Methods for Edge Creation
// ========================================================================

// LoadBalancerTagDescription represents the tag description from describe-tags API
type LoadBalancerTagDescription struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// extractEndpointAddress accepts the polymorphic shapes AWS returns for
// endpoint fields — either a bare hostname string, or an object of the form
// {"Address": "...", "Port": ...} — and returns the address. Returns "" when
// the input is nil, the wrong shape, or has no usable address.

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

// truncateString truncates a string to the specified length for logging

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
