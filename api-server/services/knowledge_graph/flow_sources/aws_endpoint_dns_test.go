package flow_sources

import (
	"testing"
)

// TestAwsServiceDNS pins the canonical hostname format for each AWS service
// the synthesizer covers. Both the in-graph populator (sources.synthesize-
// AWSEndpointDNS) and the cloud_resourses populator (extractDNSName) rely on
// these strings — drift here would silently break ExternalService matching
// for the affected service.
//
// Coverage is deliberately narrow: only services whose public hostname is
// per-resource (S3 bucket, API Gateway api-id). Region-/account-scoped
// service endpoints (SQS/SNS/DDB/Lambda/Kinesis/ECR) are intentionally NOT
// synthesized — see the function doc comment for the production incident
// that drove that decision.
func TestAwsServiceDNS(t *testing.T) {
	cases := []struct {
		name          string
		service       string
		region        string
		accountNumber string
		resourceID    string
		wantCanonical string
		wantAliases   []string
	}{
		// S3 — per-bucket hostname. Synthesized.
		{
			name:          "s3_with_region",
			service:       "AmazonS3",
			region:        "us-east-1",
			resourceID:    "nudgebee-emails",
			wantCanonical: "nudgebee-emails.s3.us-east-1.amazonaws.com",
			wantAliases: []string{
				"nudgebee-emails.s3.dualstack.us-east-1.amazonaws.com",
				"nudgebee-emails.s3-website-us-east-1.amazonaws.com",
				"nudgebee-emails.s3.amazonaws.com",
			},
		},
		{
			name:          "s3_without_region_falls_to_global",
			service:       "AmazonS3",
			resourceID:    "nudgebee-emails",
			wantCanonical: "nudgebee-emails.s3.amazonaws.com",
		},
		{
			name:    "s3_without_bucket_no_match",
			service: "AmazonS3",
			region:  "us-east-1",
		},

		// API Gateway — per-API hostname. Synthesized.
		{
			name:          "apigateway",
			service:       "AmazonAPIGateway",
			region:        "us-east-1",
			resourceID:    "abcd1234",
			wantCanonical: "abcd1234.execute-api.us-east-1.amazonaws.com",
		},
		{
			name:    "apigateway_missing_id",
			service: "AmazonAPIGateway",
			region:  "us-east-1",
		},

		// Region case-insensitivity / whitespace.
		{
			name:          "region_uppercased",
			service:       "AmazonS3",
			region:        "US-EAST-1",
			resourceID:    "my-bucket",
			wantCanonical: "my-bucket.s3.us-east-1.amazonaws.com",
			wantAliases: []string{
				"my-bucket.s3.dualstack.us-east-1.amazonaws.com",
				"my-bucket.s3-website-us-east-1.amazonaws.com",
				"my-bucket.s3.amazonaws.com",
			},
		},

		// Region-/account-scoped service endpoints — must NOT synthesize. See
		// function doc: shared host across all resources of that service in
		// the region/account → first-write-wins collisions and false matches.
		{name: "sqs_not_synthesized", service: "AWSQueueService", region: "us-west-2"},
		{name: "sns_not_synthesized", service: "AmazonSNS", region: "eu-west-1"},
		{name: "ddb_not_synthesized", service: "AmazonDynamoDB", region: "us-east-1", accountNumber: "331803013664"},
		{name: "ecr_private_not_synthesized", service: "AmazonECR", region: "us-east-1", accountNumber: "123456789012"},
		{name: "ecr_public_not_synthesized", service: "AmazonECRPublic"},
		{name: "lambda_not_synthesized", service: "AWSLambda", region: "us-east-1"},
		{name: "kinesis_not_synthesized", service: "AmazonKinesis", region: "us-east-1"},

		// Unknown service — null.
		{name: "unknown_service_no_match", service: "AmazonRDS", region: "us-east-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCanonical, gotAliases := AwsServiceDNS(tc.service, tc.region, tc.accountNumber, tc.resourceID)
			if gotCanonical != tc.wantCanonical {
				t.Errorf("canonical = %q, want %q", gotCanonical, tc.wantCanonical)
			}
			if !stringSlicesEqual(gotAliases, tc.wantAliases) {
				t.Errorf("aliases = %v, want %v", gotAliases, tc.wantAliases)
			}
		})
	}
}

// TestAwsServiceFromResourceType — the resource-type → service-name mapping
// is independent of whether the service is synthesizable. The mapping stays
// complete for forward-compat (e.g. if we later add a per-table DDB endpoint
// synthesis path); callers just observe AwsServiceDNS returning "" today.
func TestAwsServiceFromResourceType(t *testing.T) {
	cases := map[string]string{
		"storage":        "AmazonS3",
		"s3_bucket":      "AmazonS3",
		"queue":          "AWSQueueService",
		"sqs_queue":      "AWSQueueService",
		"topic":          "AmazonSNS",
		"sns_topic":      "AmazonSNS",
		"table":          "AmazonDynamoDB",
		"dynamodb_table": "AmazonDynamoDB",
		"db":             "",
		"function":       "",
		"unknown":        "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := awsServiceFromResourceType(in); got != want {
				t.Errorf("awsServiceFromResourceType(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
