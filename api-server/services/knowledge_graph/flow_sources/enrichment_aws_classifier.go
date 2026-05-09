package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// AWSClassifier classifies AWS hostnames to their corresponding node types
type AWSClassifier struct{}

// NewAWSClassifier creates a new AWS hostname classifier
func NewAWSClassifier() *AWSClassifier {
	return &AWSClassifier{}
}

// ClassifyHostname determines the node type and service name from an AWS hostname
// Returns (NodeType, serviceName) - NodeType will be empty string if not an AWS hostname
func (c *AWSClassifier) ClassifyHostname(hostname string) (core.NodeType, string) {
	hostnameLower := strings.ToLower(hostname)

	// Check if it's an AWS hostname
	if !c.IsAWSHostname(hostnameLower) {
		return "", ""
	}

	return c.classifyByPattern(hostnameLower)
}

// IsAWSHostname checks if the hostname is an AWS hostname
func (c *AWSClassifier) IsAWSHostname(hostname string) bool {
	hostnameLower := strings.ToLower(hostname)
	return strings.Contains(hostnameLower, AWSHostnameSuffix) ||
		strings.Contains(hostnameLower, CloudfrontHostSuffix)
}

// classifyByPattern classifies the hostname by pattern matching
func (c *AWSClassifier) classifyByPattern(hostname string) (core.NodeType, string) {
	switch {
	// LoadBalancer patterns
	case strings.Contains(hostname, ".elb.") || strings.Contains(hostname, ".elasticloadbalancing."):
		return core.NodeTypeLoadBalancer, "elb"

	// RDS patterns
	case strings.Contains(hostname, ".rds."):
		return core.NodeTypeDatabase, "rds"

	// ElastiCache patterns
	case strings.Contains(hostname, ".cache.") || strings.Contains(hostname, ".elasticache."):
		return core.NodeTypeCache, "elasticache"

	// S3 patterns
	case strings.Contains(hostname, ".s3.") || strings.HasPrefix(hostname, "s3.") || strings.HasPrefix(hostname, "s3-"):
		return core.NodeTypeStorage, "s3"

	// SQS patterns
	case strings.Contains(hostname, "sqs.") || strings.Contains(hostname, ".sqs."):
		return core.NodeTypeMessageQueue, "sqs"

	// SNS patterns
	case strings.Contains(hostname, "sns.") || strings.Contains(hostname, ".sns."):
		return core.NodeTypeMessageQueue, "sns"

	// DynamoDB patterns
	case strings.Contains(hostname, "dynamodb."):
		return core.NodeTypeDatabase, "dynamodb"

	// API Gateway patterns
	case strings.Contains(hostname, ".execute-api."):
		return core.NodeTypeAPIGateway, "apigateway"

	// CloudFront patterns
	case strings.Contains(hostname, CloudfrontHostSuffix):
		return core.NodeTypeCDN, "cloudfront"

	// OpenSearch/Elasticsearch patterns
	case strings.Contains(hostname, ".es.") || strings.Contains(hostname, ".opensearch."):
		return core.NodeTypeDatabase, "opensearch"

	// Lambda patterns
	case strings.Contains(hostname, ".lambda."):
		return core.NodeTypeServerlessFunction, "lambda"

	// Kinesis patterns
	case strings.Contains(hostname, "kinesis."):
		return core.NodeTypeMessageQueue, "kinesis"

	// EKS patterns
	case strings.Contains(hostname, ".eks."):
		return core.NodeTypeManagedCluster, "eks"

	// Secrets Manager patterns
	case strings.Contains(hostname, "secretsmanager."):
		return core.NodeTypeSecretVault, "secretsmanager"

	// ECR patterns
	case strings.Contains(hostname, ".ecr.") || strings.Contains(hostname, "dkr.ecr."):
		return core.NodeTypeContainerRegistry, "ecr"

	// Redshift patterns
	case strings.Contains(hostname, ".redshift."):
		return core.NodeTypeDatabase, "redshift"

	// DocumentDB patterns
	case strings.Contains(hostname, ".docdb."):
		return core.NodeTypeDatabase, "docdb"

	// Neptune patterns
	case strings.Contains(hostname, ".neptune."):
		return core.NodeTypeDatabase, "neptune"

	// MSK (Kafka) patterns
	case strings.Contains(hostname, ".kafka."):
		return core.NodeTypeMessageQueue, "msk"

	// Default: generic AWS cloud resource
	default:
		return core.NodeTypeCloudResource, "aws"
	}
}

// ExtractELBIdentifier extracts the ELB identifier from an ELB hostname
// ELB format: {identifier}.elb.{region}.amazonaws.com
func (c *AWSClassifier) ExtractELBIdentifier(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) >= 4 && strings.Contains(hostname, ".elb.") {
		return parts[0]
	}
	return ""
}
