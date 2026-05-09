package servicemap

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// VPCFlowLogsSource queries VPC Flow Logs to discover network-level service dependencies
type VPCFlowLogsSource struct {
	provider      AWSProviderInterface // Interface to access AWS provider methods
	logger        *slog.Logger
	ipCache       map[string]*providers.ServiceApplicationId // IP to resource mapping cache
	cacheDuration time.Duration
}

// AWSProviderInterface defines the methods we need from awsProvider
// This allows for better testability and decoupling
type AWSProviderInterface interface {
	QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error)
	GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string, serviceName string) (string, error)
	GetResourceIPAddress(ctx providers.CloudProviderContext, account providers.Account, serviceApplicationId providers.ServiceApplicationId) (string, int, error)
	MapIPToAWSResource(ctx providers.CloudProviderContext, account providers.Account, cfg aws.Config, ip string, region string) (*providers.ServiceApplicationId, error)
	// MapIPsToAWSResources maps multiple IPs to resources in a single database query (bulk optimization)
	MapIPsToAWSResources(ctx providers.CloudProviderContext, account providers.Account, cfg aws.Config, ips []string, region string) (map[string]*providers.ServiceApplicationId, error)
	// DescribeResourceByService calls DescribeResource on the appropriate service
	DescribeResourceByService(ctx providers.CloudProviderContext, account providers.Account, region, resourceId, serviceName string) (*ResourceMetadata, error)
}

// ResourceMetadata mirrors the ResourceMetadata from aws package
// This avoids circular dependency while allowing us to use the same type
type ResourceMetadata struct {
	ResourceID     string
	ResourceARN    string
	VpcID          string
	PrivateIP      string
	PublicIP       string
	Port           int
	SecurityGroups []string
	Subnets        []string
	Status         string
	Tags           map[string]string
	Metadata       map[string]any
}

// NewVPCFlowLogsSource creates a new VPC Flow Logs relationship source
func NewVPCFlowLogsSource(provider AWSProviderInterface, logger *slog.Logger) *VPCFlowLogsSource {
	return &VPCFlowLogsSource{
		provider:      provider,
		logger:        logger,
		ipCache:       make(map[string]*providers.ServiceApplicationId),
		cacheDuration: 5 * time.Minute,
	}
}

// GetRelationships queries VPC Flow Logs for network-level relationships
// IMPORTANT: This always returns VPC-wide traffic, not just for the requested resources
func (v *VPCFlowLogsSource) GetRelationships(ctx context.Context, request QueryRequest) (QueryResponse, error) {
	startTime := time.Now()

	// Convert providers.CloudProviderContext from context if available
	providerCtx, ok := ctx.(providers.CloudProviderContext)
	if !ok {
		// Create a basic context wrapper
		providerCtx = providers.NewCloudProviderContext(ctx)
	}

	var applications []providers.ServiceMapApplication
	var errors []error

	// Get AWS config from request (passed via context or request metadata)
	cfg, account, err := v.extractConfigFromRequest(ctx, request)
	if err != nil {
		return QueryResponse{
			Applications: []providers.ServiceMapApplication{},
			Errors:       []error{fmt.Errorf("failed to extract AWS config: %w", err)},
			Metadata: SourceMetadata{
				Source:        v.Name(),
				QueriedAt:     startTime,
				ExecutionTime: time.Since(startTime),
			},
		}, err
	}

	// Collect unique VPCs from all requested resources
	vpcIDs := make(map[string]string) // vpcID -> region
	for _, resourceReq := range request.Resources {
		// Get VPC ID for this resource
		vpcID, err := v.getVPCIDForResource(providerCtx, account, resourceReq)
		if err != nil {
			v.logger.Warn("failed to get VPC ID for resource, skipping",
				"resource", resourceReq.ResourceID,
				"error", err)
			errors = append(errors, fmt.Errorf("failed to get VPC ID for %s: %w", resourceReq.ResourceID, err))
			continue
		}
		vpcIDs[vpcID] = resourceReq.Region
	}

	// Query VPC-wide traffic for each unique VPC
	for vpcID, region := range vpcIDs {
		v.logger.Info("querying VPC-wide traffic via VPC Flow Logs",
			"vpcID", vpcID,
			"region", region)

		apps, err := v.queryVPCWideRelationships(providerCtx, cfg, account, vpcID, region, request.TimeRange)
		if err != nil {
			v.logger.Warn("failed to query VPC-wide traffic",
				"vpcID", vpcID,
				"error", err)
			errors = append(errors, fmt.Errorf("failed to query VPC %s: %w", vpcID, err))
			continue
		}

		applications = append(applications, apps...)
		v.logger.Info("VPC-wide query successful",
			"vpcID", vpcID,
			"applicationsFound", len(apps))
	}

	return QueryResponse{
		Applications: applications,
		Errors:       errors,
		Metadata: SourceMetadata{
			Source:           v.Name(),
			QueriedAt:        startTime,
			ExecutionTime:    time.Since(startTime),
			ResourcesQueried: len(applications),
		},
	}, nil
}

// executeFlowLogQuery runs the CloudWatch Logs Insights query
func (v *VPCFlowLogsSource) executeFlowLogQuery(
	ctx providers.CloudProviderContext,
	account providers.Account,
	logGroup string,
	query string,
	timeRange *TimeRange,
) ([]FlowLogConnection, error) {
	// Determine time range
	var startTime, endTime time.Time
	if timeRange != nil {
		startTime = timeRange.Start
		endTime = timeRange.End
	} else {
		// Default: last 1 hour
		endTime = time.Now()
		startTime = endTime.Add(-1 * time.Hour)
	}

	// Build QueryLogsRequest
	region := ""
	if account.Region != nil {
		region = *account.Region
	}

	logsRequest := providers.QueryLogsRequest{
		ServiceName:  "vpc",
		ResourceId:   logGroup,
		LogGroupName: logGroup,
		Region:       region,
		QueryString:  query,
		StartTime:    &startTime,
		EndTime:      &endTime,
	}

	// Execute query
	response, err := v.provider.QueryLogs(ctx, account, logsRequest)
	if err != nil {
		return nil, fmt.Errorf("CloudWatch Logs query failed: %w", err)
	}

	// Parse results and aggregate by connection (merge ACCEPT and REJECT rows)
	// Key: srcaddr:dstaddr:dstport:protocol
	connMap := make(map[string]*FlowLogConnection)

	for _, log := range response.Results {
		var srcIP, destIP, action string
		var destPort, protocol int
		var totalBytes, totalPackets int64
		var numConnections int

		// Extract fields from log labels
		for _, label := range log.Labels {
			switch label.Label {
			case "srcaddr":
				srcIP = label.Value
			case "dstaddr":
				destIP = label.Value
			case "dstport":
				if _, err := fmt.Sscanf(label.Value, "%d", &destPort); err != nil {
					v.logger.Debug("failed to parse dstport", "value", label.Value, "error", err)
				}
			case "protocol":
				if _, err := fmt.Sscanf(label.Value, "%d", &protocol); err != nil {
					v.logger.Debug("failed to parse protocol", "value", label.Value, "error", err)
				}
			case "action":
				action = label.Value
			case "total_bytes":
				if _, err := fmt.Sscanf(label.Value, "%d", &totalBytes); err != nil {
					v.logger.Debug("failed to parse total_bytes", "value", label.Value, "error", err)
				}
			case "total_packets":
				if _, err := fmt.Sscanf(label.Value, "%d", &totalPackets); err != nil {
					v.logger.Debug("failed to parse total_packets", "value", label.Value, "error", err)
				}
			case "connections":
				if _, err := fmt.Sscanf(label.Value, "%d", &numConnections); err != nil {
					v.logger.Debug("failed to parse connections", "value", label.Value, "error", err)
				}
			}
		}

		// Create connection key
		key := fmt.Sprintf("%s:%s:%d:%d", srcIP, destIP, destPort, protocol)

		// Get or create connection entry
		conn, exists := connMap[key]
		if !exists {
			conn = &FlowLogConnection{
				SourceIP:     srcIP,
				DestIP:       destIP,
				DestPort:     destPort,
				Protocol:     protocol,
				TotalBytes:   0,
				TotalPackets: 0,
				Connections:  0,
				RejectCount:  0,
				AcceptCount:  0,
			}
			connMap[key] = conn
		}

		// Aggregate bytes, packets, and connections
		conn.TotalBytes += totalBytes
		conn.TotalPackets += totalPackets
		conn.Connections += numConnections

		// Track accept vs reject counts
		switch action {
		case "ACCEPT":
			conn.AcceptCount += int64(numConnections)
		case "REJECT":
			conn.RejectCount += int64(numConnections)
		}
	}

	// Convert map to slice
	result := make([]FlowLogConnection, 0, len(connMap))
	for _, conn := range connMap {
		if conn.SourceIP != "" && conn.DestIP != "" && conn.TotalBytes > 0 {
			result = append(result, *conn)
		}
	}

	return result, nil
}

// queryVPCWideRelationships discovers ALL traffic in a VPC, not just for specific resources
// This provides complete network visibility including external connections and unknown private IPs
func (v *VPCFlowLogsSource) queryVPCWideRelationships(
	ctx providers.CloudProviderContext,
	cfg aws.Config,
	account providers.Account,
	vpcID string,
	region string,
	timeRange *TimeRange,
) ([]providers.ServiceMapApplication, error) {
	v.logger.Info("querying VPC-wide traffic", "vpcID", vpcID, "region", region)

	// Step 1: Find VPC Flow Logs log group
	logGroup, err := v.provider.GetLogGroupName(ctx, account, region, vpcID, "vpc")
	if err != nil {
		return nil, fmt.Errorf("failed to find VPC Flow Logs log group for VPC %s: %w", vpcID, err)
	}

	// Step 2: Build and execute VPC-wide query
	query := v.buildVPCWideFlowLogQuery(timeRange)
	connections, err := v.executeFlowLogQuery(ctx, account, logGroup, query, timeRange)
	if err != nil {
		return nil, fmt.Errorf("failed to execute VPC-wide flow log query: %w", err)
	}

	v.logger.Info("VPC-wide query returned connections", "count", len(connections))

	// Step 3: Collect all unique IPs and classify them
	privateIPs := make(map[string]bool) // Track unique private IPs
	publicIPExists := false             // Track if any public IPs exist

	for _, conn := range connections {
		// Classify source IP
		if classifyIP(conn.SourceIP) == "private" {
			privateIPs[conn.SourceIP] = true
		} else {
			publicIPExists = true
		}

		// Classify destination IP
		if classifyIP(conn.DestIP) == "private" {
			privateIPs[conn.DestIP] = true
		} else {
			publicIPExists = true
		}
	}

	// Step 4: Bulk resolve private IPs to AWS resources
	privateIPList := make([]string, 0, len(privateIPs))
	for ip := range privateIPs {
		privateIPList = append(privateIPList, ip)
	}

	v.logger.Debug("bulk resolving private IPs",
		"privateIPCount", len(privateIPList),
		"publicIPsExist", publicIPExists)

	ipToResource, err := v.provider.MapIPsToAWSResources(ctx, account, cfg, privateIPList, region)
	if err != nil {
		v.logger.Warn("bulk IP mapping failed, will use vpc-unknown nodes",
			"error", err,
			"queriedIPs", len(privateIPList))
		ipToResource = make(map[string]*providers.ServiceApplicationId) // Empty map
	}

	// Step 5: Build node IDs for all IPs
	internetNode := createInternetNode()
	ipToNodeID := make(map[string]providers.ServiceApplicationId)

	for _, conn := range connections {
		// Map source IP
		if _, exists := ipToNodeID[conn.SourceIP]; !exists {
			if classifyIP(conn.SourceIP) == "public" {
				ipToNodeID[conn.SourceIP] = internetNode
			} else if resourceID, found := ipToResource[conn.SourceIP]; found {
				ipToNodeID[conn.SourceIP] = *resourceID
			} else {
				ipToNodeID[conn.SourceIP] = createUnknownPrivateIPNode(conn.SourceIP, vpcID)
			}
		}

		// Map destination IP
		if _, exists := ipToNodeID[conn.DestIP]; !exists {
			if classifyIP(conn.DestIP) == "public" {
				ipToNodeID[conn.DestIP] = internetNode
			} else if resourceID, found := ipToResource[conn.DestIP]; found {
				ipToNodeID[conn.DestIP] = *resourceID
			} else {
				ipToNodeID[conn.DestIP] = createUnknownPrivateIPNode(conn.DestIP, vpcID)
			}
		}
	}

	// Step 6: Build service map applications by aggregating connections
	// Map: resource ID -> ServiceMapApplication
	appMap := make(map[string]*providers.ServiceMapApplication)

	// Track links to aggregate duplicates: appKey -> linkKey -> link
	downstreamLinkMaps := make(map[string]map[string]*providers.ServiceApplicationLink)
	upstreamLinkMaps := make(map[string]map[string]*providers.ServiceApplicationLink)

	// Debug: Log all IP to node mappings
	v.logger.Info("IP to Node ID mappings",
		"totalMappings", len(ipToNodeID))
	for ip, node := range ipToNodeID {
		v.logger.Debug("IP mapped to node",
			"ip", ip,
			"name", node.Name,
			"kind", node.Kind,
			"namespace", node.Namespace)
	}

	for _, conn := range connections {
		// Filter response traffic: Only process flows where destination port is a service port.
		// This prevents counting response traffic (e.g., EC2:ephemeral → ALB:ephemeral) as service calls.
		// For actual service calls, the destination port will be a well-known service port (e.g., :80, :443).
		if !isServicePort(conn.DestPort) {
			v.logger.Debug("skipping response traffic flow",
				"srcIP", conn.SourceIP,
				"dstIP", conn.DestIP,
				"dstPort", conn.DestPort,
				"reason", "destination port is ephemeral (not a service port)")
			continue
		}

		// Filter public IP traffic: Only include private-to-private connections
		// Skip flows where source or destination is a public IP (Internet traffic)
		srcIPClass := classifyIP(conn.SourceIP)
		dstIPClass := classifyIP(conn.DestIP)
		if srcIPClass == "public" || dstIPClass == "public" {
			v.logger.Debug("skipping public IP traffic flow",
				"srcIP", conn.SourceIP,
				"srcClass", srcIPClass,
				"dstIP", conn.DestIP,
				"dstClass", dstIPClass,
				"reason", "filtering out Internet/external traffic, focusing on internal VPC connections only")
			continue
		}

		sourceNodeID := ipToNodeID[conn.SourceIP]
		destNodeID := ipToNodeID[conn.DestIP]

		// Ensure source application exists
		sourceKey := sourceNodeID.Key()
		if _, exists := appMap[sourceKey]; !exists {
			appMap[sourceKey] = &providers.ServiceMapApplication{
				Id:          sourceNodeID,
				Upstreams:   []providers.UpstreamLink{},
				Downstreams: []providers.DownstreamLink{},
				Status:      "active",
			}
			downstreamLinkMaps[sourceKey] = make(map[string]*providers.ServiceApplicationLink)
			upstreamLinkMaps[sourceKey] = make(map[string]*providers.ServiceApplicationLink)
		}

		// Ensure destination application exists
		destKey := destNodeID.Key()
		if _, exists := appMap[destKey]; !exists {
			appMap[destKey] = &providers.ServiceMapApplication{
				Id:          destNodeID,
				Upstreams:   []providers.UpstreamLink{},
				Downstreams: []providers.DownstreamLink{},
				Status:      "active",
			}
			downstreamLinkMaps[destKey] = make(map[string]*providers.ServiceApplicationLink)
			upstreamLinkMaps[destKey] = make(map[string]*providers.ServiceApplicationLink)
		}

		// Traffic flows from source to destination (source → destination)
		// For source: destination is UPSTREAM (source calls destination)
		// For destination: source is DOWNSTREAM (source calls destination)

		// Build upstream link from source to destination (source calls destination)
		upstreamLink := buildLinkFromConnection(conn, destNodeID, "upstream")
		// Aggregate by target only (not by protocol) to avoid duplicate links
		upstreamLinkKey := destNodeID.Key()

		// Aggregate if link already exists
		if existingLink, exists := upstreamLinkMaps[sourceKey][upstreamLinkKey]; exists {
			existingLink.BytesSent += upstreamLink.BytesSent
			existingLink.RequestCount += upstreamLink.RequestCount
			existingLink.FailureCount += upstreamLink.FailureCount
			// Keep worst status
			if upstreamLink.Status > existingLink.Status {
				existingLink.Status = upstreamLink.Status
			}
			// Keep first protocol (typically TCP for most services)
			if existingLink.Protocol == "" && upstreamLink.Protocol != "" {
				existingLink.Protocol = upstreamLink.Protocol
			}
		} else {
			upstreamLinkMaps[sourceKey][upstreamLinkKey] = &upstreamLink
		}

		// Build downstream link from destination to source (source calls destination)
		downstreamLink := buildLinkFromConnection(conn, sourceNodeID, "downstream")
		// Aggregate by source only (not by protocol) to avoid duplicate links
		downstreamLinkKey := sourceNodeID.Key()

		// Aggregate if link already exists
		if existingLink, exists := downstreamLinkMaps[destKey][downstreamLinkKey]; exists {
			existingLink.BytesReceived += downstreamLink.BytesReceived
			existingLink.RequestCount += downstreamLink.RequestCount
			existingLink.FailureCount += downstreamLink.FailureCount
			// Keep worst status
			if downstreamLink.Status > existingLink.Status {
				existingLink.Status = downstreamLink.Status
			}
			// Keep first protocol (typically TCP for most services)
			if existingLink.Protocol == "" && downstreamLink.Protocol != "" {
				existingLink.Protocol = downstreamLink.Protocol
			}
		} else {
			downstreamLinkMaps[destKey][downstreamLinkKey] = &downstreamLink
		}
	}

	// Convert link maps to slices
	for appKey, app := range appMap {
		// Add aggregated downstream links (convert to DownstreamLink)
		// Filter out connections where ALL traffic is rejected from external sources only
		// (noise from port scans, etc.). Keep internal failures for debugging.
		for _, link := range downstreamLinkMaps[appKey] {
			// Skip if 100% failures from Internet/external sources - these are blocked connections
			// But KEEP 100% failures from internal sources (EC2, RDS, etc.) - these are misconfigurations
			if link.FailureCount > 0 && link.FailureCount == link.RequestCount {
				// Check if source is external (Internet)
				if link.Id.Kind == "external-ip" {
					v.logger.Debug("Skipping fully rejected downstream connection from external source",
						"appName", app.Id.Name,
						"downstreamName", link.Id.Name,
						"downstreamKind", link.Id.Kind,
						"failureCount", link.FailureCount,
						"requestCount", link.RequestCount,
						"reason", "100% failures from Internet - blocked by security groups")
					continue
				}
				// For internal sources with 100% failures, keep them - they indicate misconfigurations
				v.logger.Info("Including fully rejected internal connection (potential misconfiguration)",
					"appName", app.Id.Name,
					"downstreamName", link.Id.Name,
					"downstreamKind", link.Id.Kind,
					"failureCount", link.FailureCount,
					"requestCount", link.RequestCount)
			}
			app.Downstreams = append(app.Downstreams, link.ToDownstreamLink())
		}

		// Add aggregated upstream links (convert to UpstreamLink)
		for _, link := range upstreamLinkMaps[appKey] {
			app.Upstreams = append(app.Upstreams, link.ToUpstreamLink())
		}
	}

	// Step 7: Convert map to slice
	applications := make([]providers.ServiceMapApplication, 0, len(appMap))
	for _, app := range appMap {
		applications = append(applications, *app)
	}

	v.logger.Info("VPC-wide service map built",
		"applications", len(applications),
		"connections", len(connections))

	// Debug: Log each application's upstream/downstream counts
	for _, app := range applications {
		v.logger.Info("Application built from VPC Flow Logs",
			"name", app.Id.Name,
			"kind", app.Id.Kind,
			"upstreamCount", len(app.Upstreams),
			"downstreamCount", len(app.Downstreams),
			"status", app.Status)

		// Log first few upstreams and downstreams for debugging
		for i, upstream := range app.Upstreams {
			if i < 3 { // Log first 3
				v.logger.Debug("Upstream link",
					"appName", app.Id.Name,
					"upstreamId", upstream.Id,
					"requestCount", upstream.RequestCount,
					"failureCount", upstream.FailureCount,
					"status", upstream.Status)
			}
		}
		for i, downstream := range app.Downstreams {
			if i < 3 { // Log first 3
				v.logger.Debug("Downstream link",
					"appName", app.Id.Name,
					"downstreamName", downstream.Id.Name,
					"downstreamKind", downstream.Id.Kind,
					"requestCount", downstream.RequestCount,
					"failureCount", downstream.FailureCount,
					"status", downstream.Status)
			}
		}
	}

	return applications, nil
}

// getVPCIDForResource extracts VPC ID from a resource using the service's DescribeResource method
func (v *VPCFlowLogsSource) getVPCIDForResource(
	ctx providers.CloudProviderContext,
	account providers.Account,
	resource ResourceRequest,
) (string, error) {
	// Use the provider interface to call DescribeResource on the appropriate service
	// This delegates to the service-specific implementation (RDS, EC2, Lambda, etc.)

	resourceType := strings.ToLower(resource.ResourceType)

	// Call DescribeResourceByService via the provider interface
	metadata, err := v.provider.DescribeResourceByService(ctx, account, resource.Region, resource.ResourceID, resourceType)
	if err != nil {
		return "", fmt.Errorf("failed to describe %s resource %s: %w", resourceType, resource.ResourceID, err)
	}

	// Extract VPC ID from metadata
	if metadata.VpcID == "" {
		return "", fmt.Errorf("%s resource %s has no VPC ID (not in VPC)", resourceType, resource.ResourceID)
	}

	v.logger.Debug("extracted VPC ID from resource",
		"resourceType", resourceType,
		"resourceId", resource.ResourceID,
		"vpcId", metadata.VpcID)

	return metadata.VpcID, nil
}

// buildVPCWideFlowLogQuery constructs a VPC-wide traffic discovery query
// Discovers ALL traffic in the VPC, not just for a specific resource
func (v *VPCFlowLogsSource) buildVPCWideFlowLogQuery(timeRange *TimeRange) string {
	// Parse VPC Flow Log format
	query := `fields @timestamp, @message
| parse @message /(?<version>\d+) (?<account_id>\d+) (?<interface_id>\S+) (?<srcaddr>\S+) (?<dstaddr>\S+) (?<srcport>\d+) (?<dstport>\d+) (?<protocol>\d+) (?<packets>\d+) (?<bytes>\d+) (?<start>\d+) (?<end>\d+) (?<action>\S+) (?<log_status>\S+)/`

	// Filter only valid log entries
	query += `
| filter log_status = "OK"`

	// Aggregate traffic by connection pairs AND action (to separate ACCEPT from REJECT)
	// We'll aggregate by action first, then merge in the application layer
	query += `
| stats
    sum(bytes) as total_bytes,
    sum(packets) as total_packets,
    count(*) as connections
  by srcaddr, dstaddr, dstport, protocol, action`

	// Sort by total bytes and limit to top connections
	query += `
| sort total_bytes desc
| limit 500`

	return query
}

// extractConfigFromRequest extracts AWS config and account from context
func (v *VPCFlowLogsSource) extractConfigFromRequest(
	ctx context.Context,
	request QueryRequest,
) (aws.Config, providers.Account, error) {
	// Extract AWS config from context
	cfgVal := ctx.Value("aws.Config")
	if cfgVal == nil {
		return aws.Config{}, providers.Account{}, fmt.Errorf("AWS config not found in context")
	}

	cfg, ok := cfgVal.(aws.Config)
	if !ok {
		return aws.Config{}, providers.Account{}, fmt.Errorf("invalid AWS config type in context")
	}

	// Extract account from context
	accountVal := ctx.Value("providers.Account")
	if accountVal == nil {
		return aws.Config{}, providers.Account{}, fmt.Errorf("account not found in context")
	}

	account, ok := accountVal.(providers.Account)
	if !ok {
		return aws.Config{}, providers.Account{}, fmt.Errorf("invalid account type in context")
	}

	return cfg, account, nil
}

// SupportsResourceType checks if this source can query the given resource type
func (v *VPCFlowLogsSource) SupportsResourceType(resourceType string) bool {
	// VPC Flow Logs can show connections for any resource with a private IP
	// Note: "awselb" is the ServiceNameELB constant used for Classic ELB, ALB, and NLB
	// "elbv2" is an alias for Application/Network Load Balancers
	supportedTypes := []string{"rds", "ec2", "lambda", "ecs", "elasticache", "redshift", "elb", "alb", "nlb", "awselb", "elbv2"}
	for _, t := range supportedTypes {
		if strings.EqualFold(resourceType, t) {
			return true
		}
	}
	return false
}

// Priority returns the priority of this source (lower = higher priority)
func (v *VPCFlowLogsSource) Priority() int {
	return 2 // After AWS Config (1), before service-specific fallback (4)
}

// Name returns the name of this source
func (v *VPCFlowLogsSource) Name() string {
	return "vpc-flow-logs"
}

// IsAvailable checks if VPC Flow Logs are available for querying
func (v *VPCFlowLogsSource) IsAvailable(ctx context.Context, cfg aws.Config, account providers.Account) bool {
	// TODO: Check if VPC Flow Logs are enabled
	// For now, always return true and handle errors gracefully in GetRelationships
	return true
}

// FlowLogConnection represents aggregated flow log data (imported from aws_vpc_flowlogs.go)
type FlowLogConnection struct {
	SourceIP     string
	DestIP       string
	DestPort     int
	Protocol     int
	TotalBytes   int64
	TotalPackets int64
	Connections  int
	RejectCount  int64 // Count of REJECT actions (failures)
	AcceptCount  int64 // Count of ACCEPT actions (successful connections)
}

// isServicePort determines if a port number represents a service port vs ephemeral port.
// Service ports indicate the destination is providing a service (actual service call).
// Ephemeral ports (> 1023 and not in known service ports) indicate response traffic.
// This helps filter out reverse flows that are responses rather than actual service calls.
func isServicePort(port int) bool {
	// Well-known ports (0-1023) are always service ports
	if port <= 1023 {
		return true
	}

	// Common service ports above 1024
	knownServicePorts := map[int]bool{
		1433:  true, // SQL Server
		1521:  true, // Oracle
		3000:  true, // Development servers
		3306:  true, // MySQL
		5000:  true, // Development servers
		5432:  true, // PostgreSQL
		5672:  true, // RabbitMQ
		6379:  true, // Redis
		8000:  true, // Development servers
		8080:  true, // HTTP alternate
		8081:  true, // HTTP alternate
		8443:  true, // HTTPS alternate
		8888:  true, // Development servers
		9000:  true, // Development servers
		9090:  true, // Prometheus, development
		9092:  true, // Kafka
		9200:  true, // Elasticsearch
		9300:  true, // Elasticsearch
		27017: true, // MongoDB
		50051: true, // gRPC
	}

	return knownServicePorts[port]
}

// classifyIP determines if an IP address is private or public
// Returns "private" for RFC 1918 addresses, "public" otherwise
func classifyIP(ip string) string {
	// Parse IP address
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return "public" // Invalid format, treat as public
	}

	// Parse first octet
	var firstOctet int
	if _, err := fmt.Sscanf(parts[0], "%d", &firstOctet); err != nil {
		return "public"
	}

	// Check private ranges (RFC 1918)
	// 10.0.0.0/8
	if firstOctet == 10 {
		return "private"
	}

	// 172.16.0.0/12 (172.16.0.0 - 172.31.255.255)
	if firstOctet == 172 {
		var secondOctet int
		if _, err := fmt.Sscanf(parts[1], "%d", &secondOctet); err != nil {
			return "public"
		}
		if secondOctet >= 16 && secondOctet <= 31 {
			return "private"
		}
	}

	// 192.168.0.0/16
	if firstOctet == 192 {
		var secondOctet int
		if _, err := fmt.Sscanf(parts[1], "%d", &secondOctet); err != nil {
			return "public"
		}
		if secondOctet == 168 {
			return "private"
		}
	}

	return "public"
}

// protocolNumberToName converts IANA protocol numbers to names
func protocolNumberToName(protocol int) string {
	switch protocol {
	case 1:
		return "icmp"
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 47:
		return "gre"
	case 50:
		return "esp"
	case 51:
		return "ah"
	case 58:
		return "icmpv6"
	default:
		return fmt.Sprintf("proto-%d", protocol)
	}
}

// buildLinkFromConnection converts a FlowLogConnection to a ServiceApplicationLink
// with proper metadata including failure tracking
func buildLinkFromConnection(conn FlowLogConnection, targetId providers.ServiceApplicationId, direction string) providers.ServiceApplicationLink {
	link := providers.ServiceApplicationLink{
		Id:           targetId,
		Protocol:     protocolNumberToName(conn.Protocol),
		RequestCount: float64(conn.Connections),
		FailureCount: float64(conn.RejectCount),
		Latency:      0, // VPC Flow Logs don't track latency
	}

	// Set status based on reject count
	if conn.RejectCount > 0 {
		link.Status = 500 // Failures present
	} else {
		link.Status = 200 // All successful
	}

	// Set bytes based on direction
	// For upstream links (this resource → target): BytesSent = bytes going out
	// For downstream links (target → this resource): BytesReceived = bytes coming in
	switch direction {
	case "upstream":
		link.BytesSent = float64(conn.TotalBytes) // This resource sending to upstream target
	case "downstream":
		link.BytesReceived = float64(conn.TotalBytes) // This resource receiving from downstream source
	}

	return link
}

// createInternetNode creates a special "Internet" node for all public IP traffic
func createInternetNode() providers.ServiceApplicationId {
	return providers.ServiceApplicationId{
		Name:      "Internet",
		Kind:      "external-ip",
		Namespace: "internet",
	}
}

// createUnknownPrivateIPNode creates a node for unresolved private IPs
func createUnknownPrivateIPNode(ip string, vpcID string) providers.ServiceApplicationId {
	return providers.ServiceApplicationId{
		Name:      ip,
		Kind:      "vpc-unknown",
		Namespace: vpcID,
	}
}
