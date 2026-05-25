package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"regexp"
	"strings"
)

// awsRegionPattern matches AWS region identifiers as they appear inside
// service hostnames. Covers standard (us-east-1), GovCloud (us-gov-east-1),
// China (cn-north-1), and ISO (us-iso-east-1, us-isob-east-1) regions. Used
// by IsBareAWSServiceEndpoint to distinguish `<service>.<region>` (bare,
// e.g. ec2.us-east-1) from `<bucket>.<service>` (per-resource, e.g.
// my-bucket.s3) when the host has the same dot-count.
var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}-[-a-z]+-[0-9]+$`)

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
		strings.Contains(hostnameLower, CloudfrontHostSuffix) ||
		strings.HasPrefix(hostnameLower, ECRPublicHost)
}

// IsBareAWSServiceEndpoint reports whether hostname names an AWS service-API
// endpoint that is shared across every customer in a region/account, rather
// than a specific customer-owned resource. Examples:
//
//	ec2.us-east-2.amazonaws.com               EC2 control plane
//	sqs.us-east-1.amazonaws.com               SQS regional API
//	dynamodb.us-east-1.amazonaws.com          DynamoDB regional API
//	api.sagemaker.us-east-1.amazonaws.com     SageMaker regional API
//	cloudfront.amazonaws.com                  global service API
//	public.ecr.aws                            ECR Public global host
//
// These hosts appear as outbound destinations from any workload using the
// AWS SDK, but the per-resource identity (which instance, queue, table, …)
// lives in the request path/body, not the hostname. Synthesizing an inferred
// CloudResource (or MessageQueue / SecretVault / etc.) for them produces a
// phantom node that pollutes "which resources does this workload use"
// queries — see createInferredNodeIfAWS for the synthesis site.
//
// Per-resource hostnames (e.g. <bucket>.s3.<region>.amazonaws.com,
// <id>.execute-api.<region>.amazonaws.com, <account>.dkr.ecr.<region>.amazonaws.com)
// have a customer-specific identifier as the leftmost label and return false
// here so the existing inference path keeps materializing them.
//
// Structural rule (intentionally not a service-token allow-list — service
// names change, the host shape doesn't):
//
//	host == "public.ecr.aws"                                    → bare
//	strip ".amazonaws.com", split rest by ".":
//	    1 part                                                  → bare    (global API: cloudfront, s3)
//	    2 parts AND parts[1] looks like a region                → bare    (<service>.<region>)
//	    3 parts AND parts[0] == "api" AND parts[2] is a region  → bare    (api.<service>.<region>)
//	    otherwise                                               → per-resource
//
// The region check is what stops `<bucket>.s3.amazonaws.com` (2 parts, but
// parts[1] = "s3" is not a region) from being treated as bare. Region
// matching is delegated to awsRegionPattern so the rule covers GovCloud /
// China / ISO regions, not just the standard `us-east-1` form.
//
// Cloudfront.net hosts are always per-resource (<distribution-id>.cloudfront.net)
// and are not handled here.
func (c *AWSClassifier) IsBareAWSServiceEndpoint(hostname string) bool {
	h := strings.ToLower(strings.TrimSpace(hostname))
	if h == "" {
		return false
	}
	if h == ECRPublicHost {
		return true
	}
	if !strings.HasSuffix(h, AWSHostnameSuffix) {
		return false
	}
	rest := strings.TrimSuffix(h, AWSHostnameSuffix)
	if rest == "" {
		return false
	}
	parts := strings.Split(rest, ".")
	switch {
	case len(parts) == 1:
		return true
	case len(parts) == 2 && awsRegionPattern.MatchString(parts[1]):
		return true
	case len(parts) == 3 && parts[0] == "api" && awsRegionPattern.MatchString(parts[2]):
		return true
	}
	return false
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

	// DynamoDB patterns: regional service endpoint AND the account-scoped
	// SDK form `<account>.ddb.<region>.amazonaws.com` emitted by newer AWS
	// SDKs (Go v2, Rust). Older clients only used `dynamodb.<region>...`.
	case strings.Contains(hostname, "dynamodb.") || strings.Contains(hostname, ".ddb."):
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

	// Lambda patterns: per-region SDK endpoint `lambda.<region>.amazonaws.com`
	// (matched as a prefix), per-function URL `<id>.lambda-url.<region>.on.aws`,
	// and the legacy `<x>.lambda.<region>...` form (kept as a substring).
	case strings.HasPrefix(hostname, "lambda.") || strings.Contains(hostname, ".lambda.") || strings.Contains(hostname, ".lambda-url."):
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

	// ECR Public — global, has its own host. Match before private ECR so the
	// `.ecr.` substring in `.ecr-public.` doesn't get classified as private.
	case strings.Contains(hostname, "api.ecr-public.") || strings.HasPrefix(hostname, "public.ecr.aws"):
		return core.NodeTypeContainerRegistry, "ecr-public"

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
