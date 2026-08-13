package aws

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// lambdaFunctionSchema — concrete schema for Lambda functions. Fields written by
// extractLambdaMetadata.
var lambdaFunctionSchema = core.SpecificTypeSchema{
	SpecificType: "LambdaFunction",
	NodeType:     core.NodeTypeServerlessFunction,
	Properties: []core.PropertyDef{
		{Name: "runtime", Indexed: true},
		{Name: "memory_mb", Indexed: true},
		{Name: "timeout_seconds", Indexed: true},
		{Name: "handler", Indexed: true},
		{Name: "role_arn"},
		{Name: "environment_variables"},
		{Name: "dynamodb_table_name"},
		{Name: "queue_url"},
		{Name: "sns_topic_arn"},
		{Name: "event_source_arns"},
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "function_name"},
		{Name: "last_modified"},
		{Name: "description"},
		{Name: "timeout"},
		{Name: "memory_size"},
		{Name: "code_size"},
		{Name: "version"},
		{Name: "tracing_config_mode"},
		{Name: "revision_id"},
		{Name: "state_reason"},
		{Name: "state_reason_code"},
		{Name: "last_update_status"},
		{Name: "last_update_status_reason"},
		{Name: "last_update_status_reason_code"},
		{Name: "package_type"},
		{Name: "image_uri"},
		{Name: "image_digest"},
		{Name: "signing_profile_version_arn"},
		{Name: "signing_job_arn"},
		{Name: "code_sha256"},
		{Name: "architectures"},
		{Name: "architecture_normalized"},
		{Name: "master_arn"},
		{Name: "kms_key_arn"},
		{Name: "anonymous_access"},
		{Name: "anonymous_actions"},
	},
}

func init() { core.RegisterSpecificTypeSchema(lambdaFunctionSchema) }

// extractLambdaMetadata extracts essential fields for Lambda functions
func (s *AWSSource) extractLambdaMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Runtime (important for compatibility)
	if runtime, ok := metaMap["Runtime"].(string); ok && runtime != "" {
		properties["runtime"] = runtime
	}

	// Container-package image reference (Code.ImageUri) — drives the ContainerImage
	// node + USES_IMAGE edge (createLambdaImageNodesAndEdges).
	if code, ok := metaMap["Code"].(map[string]interface{}); ok {
		if imageURI, ok := code["ImageUri"].(string); ok && imageURI != "" {
			properties["image_uri"] = imageURI
		}
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

// createLambdaEdges creates edges for Lambda functions
func (s *AWSSource) createLambdaEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := s.getLambdaMetaFromCache(node)
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
						if sourceNode, exists := lookup.ByARN[eventSourceArn]; exists {
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
				if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
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
						if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
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
						if sgNode, exists := lookup.ByResourceID[sgID]; exists {
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

// createLambdaImageNodesAndEdges synthesizes ContainerImage nodes + LambdaFunction →
// ContainerImage (USES_IMAGE) edges for container-package Lambdas (image_uri set by
// extractLambdaMetadata), via the shared core helper. Zip-package Lambdas have no
// image_uri and produce nothing.
func (s *AWSSource) createLambdaImageNodesAndEdges(nodes []*core.DbNode, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	return core.SynthesizeContainerImages(nodes, lambdaImageRefs, core.CloudProviderAWS, req.TenantID, req.CloudAccountID, "aws")
}

// lambdaImageRefs returns the container image ref for a Lambda node (nil for zip-package).
func lambdaImageRefs(node *core.DbNode) []string {
	if uri, ok := node.Properties["image_uri"].(string); ok && uri != "" {
		return []string{uri}
	}
	return nil
}

// getLambdaMetaFromCache reads Lambda-function meta from the per-build cache.
// Used by createLambdaEdges for the EventSourceMappings → SUBSCRIBES_TO path.
func (s *AWSSource) getLambdaMetaFromCache(node *core.DbNode) (map[string]interface{}, bool) {
	return s.metaFromCache(node, "function")
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
