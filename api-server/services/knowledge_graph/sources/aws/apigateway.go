package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// apiGatewaySchemaProps — API Gateway APIs (NodeTypeAPIGateway) get a synthesized
// endpoint DNS (synthesizeAWSEndpointDNS) which the ontology maps to `endpoint`.
// Shared by the REST / HTTP / WebSocket variants.
var apiGatewaySchemaProps = []core.PropertyDef{
	{Name: "dns_name", Indexed: true},
}

// apiGatewayRestApiParityProps — Additional provider fields (not yet emitted by our
// extractor) for the REST API v1 model.
var apiGatewayRestApiParityProps = []core.PropertyDef{
	{Name: "created_date"},
	{Name: "version"},
	{Name: "minimum_compression_size"},
	{Name: "disable_execute_api_endpoint"},
	{Name: "anonymous_access"},
	{Name: "anonymous_actions"},
	{Name: "endpoint_type"},
	{Name: "exposed_internet"},
}

// apiGatewayV2ParityProps — Additional provider fields (not yet emitted by our extractor)
// for the HTTP/WebSocket API v2 model.
var apiGatewayV2ParityProps = []core.PropertyDef{
	{Name: "protocol_type"},
	{Name: "route_selection_expression"},
	{Name: "api_key_selection_expression"},
	{Name: "api_endpoint"},
	{Name: "version"},
	{Name: "created_date"},
	{Name: "description"},
}

var apiGatewayRestApiSchema = core.SpecificTypeSchema{
	SpecificType: "APIGatewayRestApi",
	NodeType:     core.NodeTypeAPIGateway,
	Properties:   append(append([]core.PropertyDef{}, apiGatewaySchemaProps...), apiGatewayRestApiParityProps...),
}

var apiGatewayHttpApiSchema = core.SpecificTypeSchema{
	SpecificType: "APIGatewayHttpApi",
	NodeType:     core.NodeTypeAPIGateway,
	Properties:   append(append([]core.PropertyDef{}, apiGatewaySchemaProps...), apiGatewayV2ParityProps...),
}

var apiGatewayWebSocketApiSchema = core.SpecificTypeSchema{
	SpecificType: "APIGatewayWebSocketApi",
	NodeType:     core.NodeTypeAPIGateway,
	Properties:   append(append([]core.PropertyDef{}, apiGatewaySchemaProps...), apiGatewayV2ParityProps...),
}

func init() {
	core.RegisterSpecificTypeSchema(apiGatewayRestApiSchema)
	core.RegisterSpecificTypeSchema(apiGatewayHttpApiSchema)
	core.RegisterSpecificTypeSchema(apiGatewayWebSocketApiSchema)
}

// createAPIGatewayEdges creates edges for API Gateway resources
// Handles Lambda integrations and VPC endpoint connections for private APIs
func (s *AWSSource) createAPIGatewayEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
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
							if lambdaNode, exists := lookup.ByARN[lambdaArn]; exists {
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
								if vpcEndpointNode, exists := lookup.ByResourceID[id]; exists {
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
