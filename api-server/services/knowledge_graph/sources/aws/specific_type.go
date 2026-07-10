package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// awsSpecificTypeMap maps (type, service_name) to the concrete cloud/native label
// (specific_type) — e.g. EC2Instance, RDSInstance. This is the
// per-resource declaration of the concrete cloud type; node_type stays the
// cloud-agnostic ontology. Mirrors awsResourceTypeMap; keep the two in sync
// (the specific-type coverage test enforces it).
var awsSpecificTypeMap = map[string]map[string]string{
	"cluster": {
		"AmazonRDS":         "RDSCluster",
		"AmazonEKS":         "EKSCluster",
		"AmazonElastiCache": "ElastiCacheCluster",
		"AmazonECS":         "ECSCluster",
		"AmazonMSK":         "MSKCluster",
		"AmazonRedshift":    "RedshiftCluster",
	},
	"service":                  {"AmazonECS": "ECSService"},
	"db":                       {"AmazonRDS": "RDSInstance"},
	"cluster-snapshot":         {"AmazonRDS": "RDSClusterSnapshot"},
	"function":                 {"AWSLambda": "LambdaFunction", "AmazonCloudFront": "CloudFrontFunction"},
	"queue":                    {"AWSQueueService": "SQSQueue"},
	"topic":                    {"AmazonSNS": "SNSTopic"},
	"loadbalancer":             {"AWSELB": "ELBLoadBalancer"},
	"application_loadbalancer": {"AWSELB": "ALBLoadBalancer"},
	"network_loadbalancer":     {"AWSELB": "NLBLoadBalancer"},
	"targetgroup":              {"AWSELB": "ELBTargetGroup"},
	"compute-instance":         {"AmazonEC2": "EC2Instance"},
	"managedinstance":          {"AWSSystemsManager": "SSMManagedInstance"},
	"snapshot":                 {"AmazonEC2": "EBSSnapshot"},
	"table":                    {"AmazonDynamoDB": "DynamoDBTable"},
	"distribution":             {"AmazonCloudFront": "CloudFrontDistribution"},
	"vpc":                      {"AmazonVPC": "EC2Vpc"},
	"security_group":           {"AmazonVPC": "EC2SecurityGroup"},
	"natgateway":               {"AmazonEC2": "NATGateway", "AmazonVPC": "NATGateway"},
	"internet-gateway":         {"AmazonVPC": "InternetGateway"},
	"subnet":                   {"AmazonVPC": "EC2Subnet"},
	"vpc-endpoint":             {"AmazonVPC": "VPCEndpoint"},
	"elastic-ip":               {"AmazonVPC": "ElasticIP"},
	"network-interface":        {"AmazonVPC": "NetworkInterface"},
	"hostedzone":               {"AmazonRoute53": "Route53HostedZone"},
	"repository":               {"AmazonECR": "ECRRepository", "AmazonECRPublic": "ECRPublicRepository"},
	"secret":                   {"AWSSecretsManager": "SecretsManagerSecret"},
	"log-group":                {"AmazonCloudWatch": "CloudWatchLogGroup"},
	"vpc-flow-log":             {"AmazonCloudWatch": "VPCFlowLog"},
	"trail":                    {"AWSCloudTrail": "CloudTrail"},
	"eventdatastore":           {"AWSCloudTrail": "CloudTrailEventDataStore"},
	"filesystem":               {"AmazonEFS": "EFSFileSystem"},
	"file-system":              {"AmazonEFS": "EFSFileSystem"},
	"pod":                      {"AmazonEKS": "EKSPod"},
	"nodegroup":                {"AmazonEKS": "EKSNodeGroup"},
	"storage":                  {"AmazonS3": "S3Bucket", "AmazonEC2": "EBSVolume"},
	"key":                      {"AWSKMS": "KMSKey"},
	"backup-vault":             {"AWSBackup": "BackupVault"},
	"backup-plan":              {"AWSBackup": "BackupPlan"},
	"rest-api":                 {"AmazonAPIGateway": "APIGatewayRestApi"},
	"http-api":                 {"AmazonAPIGateway": "APIGatewayHttpApi"},
	"websocket-api":            {"AmazonAPIGateway": "APIGatewayWebSocketApi"},
	"stack":                    {"AWSCloudFormation": "CloudFormationStack"},
	"configuration-set":        {"AmazonSES": "SESConfigurationSet"},
	"identity":                 {"AmazonSES": "SESIdentity"},
	"hub":                      {"AWSSecurityHub": "SecurityHub"},
	"standard":                 {"AWSSecurityHub": "SecurityHubStandard"},
}

// awsServiceSpecificTypeFallback maps service_name to a concrete label when the
// (type, service) combination is not in awsSpecificTypeMap. Mirrors
// awsServiceFallbackMap.
var awsServiceSpecificTypeFallback = map[string]string{
	"AmazonRDS":             "RDSInstance",
	"AmazonElastiCache":     "ElastiCacheCluster",
	"AmazonS3":              "S3Bucket",
	"AmazonEC2":             "EC2Instance",
	"AWSLambda":             "LambdaFunction",
	"AmazonDynamoDB":        "DynamoDBTable",
	"AWSQueueService":       "SQSQueue",
	"AmazonSNS":             "SNSTopic",
	"AmazonVPC":             "EC2Vpc",
	"AWSELB":                "ELBLoadBalancer",
	"AmazonRoute53":         "Route53HostedZone",
	"AmazonCloudFront":      "CloudFrontDistribution",
	"AmazonECR":             "ECRRepository",
	"AmazonECRPublic":       "ECRPublicRepository",
	"AWSSecretsManager":     "SecretsManagerSecret",
	"AmazonCloudWatch":      "CloudWatchLogGroup",
	"AmazonEKS":             "EKSCluster",
	"AmazonECS":             "ECSCluster",
	"AmazonMSK":             "MSKCluster",
	"AmazonRedshift":        "RedshiftCluster",
	"AmazonES":              "OpenSearchDomain",
	"AmazonSageMaker":       "SageMakerResource",
	"AWSBackup":             "BackupVault",
	"ACM":                   "ACMCertificate",
	"AmazonKinesisFirehose": "KinesisFirehoseStream",
	"AmazonGuardDuty":       "GuardDutyDetector",
	"AWSXRay":               "XRayResource",
	"AWSCloudTrail":         "CloudTrail",
	"AmazonEFS":             "EFSFileSystem",
}

// determineSpecificType resolves the concrete cloud label for a resource,
// defaulting to the ontology NodeType name when unmapped (so it's never blank).
func (s *AWSSource) determineSpecificType(resourceType, serviceName string, nodeType core.NodeType) string {
	rt := strings.ToLower(resourceType)
	if m, ok := awsSpecificTypeMap[rt]; ok {
		if label, ok := m[serviceName]; ok {
			return label
		}
	}
	if label, ok := awsServiceSpecificTypeFallback[serviceName]; ok {
		return label
	}
	return string(nodeType)
}
