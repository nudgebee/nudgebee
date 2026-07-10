package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// cloudWatchLogGroupSchema — concrete schema for CloudWatch Log Groups
// (NodeTypeLogAggregator). kms_key_id is lifted by
// the common extractor for the IS_ENCRYPTED_BY edge; retention is not extracted.
var cloudWatchLogGroupSchema = core.SpecificTypeSchema{
	SpecificType: "CloudWatchLogGroup",
	NodeType:     core.NodeTypeLogAggregator,
	Properties: []core.PropertyDef{
		{Name: "kms_key_id"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "creation_time"},
		{Name: "data_protection_status"},
		{Name: "inherited_properties"},
		{Name: "log_group_class"},
		{Name: "log_group_name"},
		{Name: "metric_filter_count"},
		{Name: "retention_in_days"},
		{Name: "stored_bytes"},
	},
}

// vpcFlowLogSchema — VPC Flow Logs (NodeTypeLogAggregator). Destination/traffic fields
// are enriched during edge building (updateVPCFlowLogNodeMetadata).
var vpcFlowLogSchema = core.SpecificTypeSchema{
	SpecificType: "VPCFlowLog",
	NodeType:     core.NodeTypeLogAggregator,
	Properties: []core.PropertyDef{
		{Name: "traffic_type"},
		{Name: "log_destination_type"},
		{Name: "log_destination"},
		{Name: "monitored_resource_id"},
		{Name: "deliver_logs_status"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(cloudWatchLogGroupSchema)
	core.RegisterSpecificTypeSchema(vpcFlowLogSchema)
}

// createCloudWatchEdges creates edges for CloudWatch resources, including VPC Flow Logs and Log Groups
func (s *AWSSource) createCloudWatchEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
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

// createVPCFlowLogEdges creates edges for VPC Flow Logs
func (s *AWSSource) createVPCFlowLogEdges(reqCtx *security.RequestContext, vpcFlowLogNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
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
			if resourceNode, exists := lookup.ByResourceID[flowLogInfo.ResourceId]; exists {
				edges = append(edges, s.createEdge(node, resourceNode, core.RelationshipEmitsLogsTo,
					map[string]interface{}{
						"connection_type": "flow_log_monitors",
						"traffic_type":    flowLogInfo.TrafficType,
					}, req))
			}
		}

		// 2. VPC Flow Log → CloudWatch Log Group (if destination is CloudWatch Logs)
		if flowLogInfo.LogDestinationType == "cloud-watch-logs" && flowLogInfo.LogGroupName != "" {
			for _, cwNode := range lookup.GetNodesByTypeAndName(core.NodeTypeLogAggregator, flowLogInfo.LogGroupName) {
				if nodeType, ok := cwNode.Properties["type"].(string); ok && nodeType == "log-group" {
					edges = append(edges, s.createEdge(node, cwNode, core.RelationshipPublishesTo,
						map[string]interface{}{
							"connection_type":  "flow_log_destination",
							"destination_type": "cloud-watch-logs",
						}, req))
					break
				}
			}
		}

		// 3. VPC Flow Log → S3 Bucket (if destination is S3)
		if flowLogInfo.LogDestinationType == "s3" && flowLogInfo.LogDestination != "" {
			// Extract bucket name from ARN (format: arn:aws:s3:::bucket-name or arn:aws:s3:::bucket-name/prefix)
			bucketARN := flowLogInfo.LogDestination
			// Try to find the S3 bucket node
			s3Node, exit := lookup.ByResourceID[sources.GetResourceIDFromARN(bucketARN)]
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
func (s *AWSSource) createLogGroupEdges(logGroupNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
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

// parseLogGroupName parses a CloudWatch log group name to identify monitored resources
// Supports common AWS service log group naming patterns
func (s *AWSSource) parseLogGroupName(logGroupName string, lookup *sources.NodeLookup) []MonitoredResource {
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

			for _, dbNode := range lookup.GetNodesByTypeAndName(core.NodeTypeDatabase, instanceName) {
				if serviceName, ok := getStringProperty(dbNode, "service_name"); ok && serviceName == "AmazonRDS" {
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

			for _, dbNode := range lookup.GetNodesByTypeAndName(core.NodeTypeDatabase, clusterName) {
				if serviceName, ok := getStringProperty(dbNode, "service_name"); ok && serviceName == "AmazonRDS" {
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

	// Pattern 3: RDS Enhanced Monitoring - RDSOSMetrics
	// This log group contains OS-level metrics for all RDS instances with enhanced monitoring
	// We match it to RDS instances that have EnhancedMonitoringResourceArn in their metadata
	if logGroupName == "RDSOSMetrics" {
		for _, dbNode := range lookup.ByNodeType[core.NodeTypeDatabase] {
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

		for _, lambdaNode := range lookup.GetNodesByTypeAndName(core.NodeTypeServerlessFunction, functionName) {
			results = append(results, MonitoredResource{
				node:    lambdaNode,
				logType: "function_logs",
				pattern: "lambda_function",
			})
			break
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

		for _, cloudNode := range lookup.GetNodesByTypeAndName(core.NodeTypeManagedCluster, resourceName) {
			if serviceName, ok := getStringProperty(cloudNode, "service_name"); ok && serviceName == "AmazonECS" {
				results = append(results, MonitoredResource{
					node:    cloudNode,
					logType: "container_logs",
					pattern: "ecs_container",
				})
				break
			}
		}
	}

	// Pattern 6: API Gateway Logs - /aws/apigateway/{api-name} or /aws/api-gateway/{api-id}/{stage}
	// Example: /aws/apigateway/my-api or /aws/api-gateway/abc123/prod
	if strings.HasPrefix(logGroupName, "/aws/apigateway/") || strings.HasPrefix(logGroupName, "/aws/api-gateway/") {
		// For CloudResource nodes that might be API Gateways
		for _, cloudNode := range lookup.ByNodeType[core.NodeTypeCloudResource] {
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

		for _, cacheNode := range lookup.GetNodesByTypeAndName(core.NodeTypeCache, clusterName) {
			results = append(results, MonitoredResource{
				node:    cacheNode,
				logType: "slow_log",
				pattern: "elasticache_cluster",
			})
			break
		}
	}

	// Pattern 8: EKS Cluster Logs - /aws/eks/{cluster-name}/cluster
	// Example: /aws/eks/nudgebee/cluster
	if strings.HasPrefix(logGroupName, "/aws/eks/") && strings.HasSuffix(logGroupName, "/cluster") {
		withoutPrefix := strings.TrimPrefix(logGroupName, "/aws/eks/")
		clusterName := strings.TrimSuffix(withoutPrefix, "/cluster")

		for _, clusterNode := range lookup.GetNodesByTypeAndName(core.NodeTypeManagedCluster, clusterName) {
			results = append(results, MonitoredResource{
				node:    clusterNode,
				logType: "cluster_logs",
				pattern: "eks_cluster",
			})
			break
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
