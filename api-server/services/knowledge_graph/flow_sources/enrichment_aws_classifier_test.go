package flow_sources

import (
	"nudgebee/services/knowledge_graph/core"
	"testing"
)

// TestAWSClassifier_ClassifyHostname covers the hostname-pattern → NodeType
// table at flow_sources/enrichment_aws_classifier.go:classifyByPattern. The
// table has historically lost entries for new AWS form variants (e.g. the
// account-scoped DDB SDK form, ECR Public) — pinning each form here flags
// regressions when the pattern list is reordered or trimmed.
func TestAWSClassifier_ClassifyHostname(t *testing.T) {
	c := NewAWSClassifier()

	cases := []struct {
		name        string
		hostname    string
		wantType    core.NodeType
		wantService string
	}{
		// Pre-existing patterns — protect against regression in this PR.
		{"elb_alb", "my-alb-1234.us-east-1.elb.amazonaws.com", core.NodeTypeLoadBalancer, "elb"},
		{"rds", "mydb.abc.us-east-1.rds.amazonaws.com", core.NodeTypeDatabase, "rds"},
		{"s3_virtual_regional", "nudgebee-emails.s3.us-east-1.amazonaws.com", core.NodeTypeStorage, "s3"},
		{"sqs_regional", "sqs.us-east-1.amazonaws.com", core.NodeTypeMessageQueue, "sqs"},
		{"sns_regional", "sns.us-east-1.amazonaws.com", core.NodeTypeMessageQueue, "sns"},
		{"dynamodb_service_endpoint", "dynamodb.us-east-1.amazonaws.com", core.NodeTypeDatabase, "dynamodb"},
		{"lambda_service_endpoint", "lambda.us-east-1.amazonaws.com", core.NodeTypeServerlessFunction, "lambda"},

		// New patterns added in this PR.
		{
			name:        "dynamodb_account_scoped_sdk_form",
			hostname:    "331803013664.ddb.us-east-1.amazonaws.com",
			wantType:    core.NodeTypeDatabase,
			wantService: "dynamodb",
		},
		{
			name:        "ecr_public_api_endpoint",
			hostname:    "api.ecr-public.us-east-1.amazonaws.com",
			wantType:    core.NodeTypeContainerRegistry,
			wantService: "ecr-public",
		},
		{
			name:        "ecr_public_global_host",
			hostname:    "public.ecr.aws",
			wantType:    core.NodeTypeContainerRegistry,
			wantService: "ecr-public",
		},

		// Regression guard: ECR Public must classify before private ECR
		// (since `.ecr-public.` contains `.ecr.` substring).
		{
			name:        "ecr_private_unaffected",
			hostname:    "123456789012.dkr.ecr.us-east-1.amazonaws.com",
			wantType:    core.NodeTypeContainerRegistry,
			wantService: "ecr",
		},

		// Non-AWS host returns "" for both.
		{"non_aws", "example.com", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotService := c.ClassifyHostname(tc.hostname)
			if gotType != tc.wantType {
				t.Errorf("type = %q, want %q", gotType, tc.wantType)
			}
			if gotService != tc.wantService {
				t.Errorf("service = %q, want %q", gotService, tc.wantService)
			}
		})
	}
}

// TestAWSClassifier_IsBareAWSServiceEndpoint pins the structural rule that
// drives phantom-suppression in createInferredNodeIfAWS and the early-bail
// in AWSHostnamePatternStrategy.Match. Bare = host shared across every
// customer in a region/account; per-resource = leftmost label is a
// customer-specific id (bucket, db, distribution, account number, …).
//
// All "bare" cases are taken from the inferred-node rows observed in
// production (114 generic CloudResource fallbacks + the SQS/SNS/Secrets
// Manager / ECR-public specific-type fallbacks).
func TestAWSClassifier_IsBareAWSServiceEndpoint(t *testing.T) {
	c := NewAWSClassifier()

	cases := []struct {
		name     string
		hostname string
		want     bool
	}{
		// ECR Public global host — has its own non-amazonaws.com domain.
		{"ecr_public_global", "public.ecr.aws", true},
		{"ecr_public_global_mixed_case", "Public.ECR.AWS", true},

		// `<service>.amazonaws.com` (1 label before suffix, global services).
		{"cloudfront_global_api", "cloudfront.amazonaws.com", true},
		{"s3_legacy_path_style", "s3.amazonaws.com", true},

		// `<service>.<region>.amazonaws.com` (2 labels — most common bare form).
		{"ec2_regional", "ec2.us-east-2.amazonaws.com", true},
		{"ec2_other_region", "ec2.us-east-1.amazonaws.com", true},
		{"cloudformation_regional", "cloudformation.us-east-1.amazonaws.com", true},
		{"cloudtrail_regional", "cloudtrail.us-east-1.amazonaws.com", true},
		{"config_regional", "config.eu-west-1.amazonaws.com", true},
		{"cur_regional", "cur.us-east-1.amazonaws.com", true},
		{"directconnect_regional", "directconnect.us-east-1.amazonaws.com", true},
		{"sqs_regional", "sqs.us-east-1.amazonaws.com", true},
		{"sns_regional", "sns.us-east-1.amazonaws.com", true},
		{"dynamodb_regional", "dynamodb.us-east-1.amazonaws.com", true},
		{"lambda_regional", "lambda.us-east-1.amazonaws.com", true},
		{"secretsmanager_regional", "secretsmanager.us-east-1.amazonaws.com", true},
		{"kinesis_regional", "kinesis.us-east-1.amazonaws.com", true},

		// `api.<service>.<region>.amazonaws.com` (3 labels, `api` prefix —
		// newer service APIs and ECR public).
		{"api_sagemaker_regional", "api.sagemaker.us-east-1.amazonaws.com", true},
		{"api_ecr_public_regional", "api.ecr-public.us-east-1.amazonaws.com", true},

		// Non-standard region partitions — must still be flagged as bare.
		{"ec2_govcloud", "ec2.us-gov-east-1.amazonaws.com", true},
		{"ec2_china", "ec2.cn-north-1.amazonaws.com", true},
		{"ec2_iso", "ec2.us-iso-east-1.amazonaws.com", true},

		// Mixed case must not change verdict.
		{"mixed_case_bare", "EC2.us-east-2.AmazonAWS.com", true},

		// Per-resource — must NOT be flagged as bare.
		{"s3_bucket_virtual", "my-bucket.s3.us-east-1.amazonaws.com", false},
		{"s3_bucket_global", "my-bucket.s3.amazonaws.com", false},
		{"ddb_account_scoped", "123456789012.ddb.us-east-1.amazonaws.com", false},
		{"ecr_private_account", "123456789012.dkr.ecr.us-east-1.amazonaws.com", false},
		{"rds_instance", "mydb.abc123.us-east-1.rds.amazonaws.com", false},
		{"elasticache_node", "mycache.abc123.cache.amazonaws.com", false},
		{"api_gateway_id", "abc123.execute-api.us-east-1.amazonaws.com", false},
		{"elb_alb", "my-alb-1234.us-east-1.elb.amazonaws.com", false},

		// CloudFront distributions live on a different domain and are always
		// per-resource (leftmost label is the distribution id).
		{"cloudfront_distribution", "d1234567890abc.cloudfront.net", false},

		// Non-AWS / empty.
		{"non_aws", "example.com", false},
		{"empty", "", false},
		{"whitespace_only", "   ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.IsBareAWSServiceEndpoint(tc.hostname); got != tc.want {
				t.Errorf("IsBareAWSServiceEndpoint(%q) = %v, want %v", tc.hostname, got, tc.want)
			}
		})
	}
}
