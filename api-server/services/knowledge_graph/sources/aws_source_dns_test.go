package sources

import (
	"testing"

	"nudgebee/services/internal/testenv"
)

// TestSynthesizeAWSEndpointDNS pins what the AWS-source extractor pipeline
// stamps onto `properties["dns_name"]` (and `dns_aliases`) for resource types
// that AWS doesn't emit a DNS endpoint for. Without this, the cross-account
// match `<bucket>.s3.<region>.amazonaws.com` ExternalService → Storage(name=
// "<bucket>") never fires.
func TestSynthesizeAWSEndpointDNS(t *testing.T) {
	cases := []struct {
		name        string
		props       map[string]interface{}
		wantDNS     string
		wantAliases []string
	}{
		{
			name: "s3_uses_bucket_region_when_present",
			props: map[string]interface{}{
				"service_name":  "AmazonS3",
				"bucket_name":   "nudgebee-emails",
				"bucket_region": "us-east-1",
				"region":        "global", // ignored when bucket_region is set
			},
			wantDNS: "nudgebee-emails.s3.us-east-1.amazonaws.com",
			wantAliases: []string{
				"nudgebee-emails.s3.dualstack.us-east-1.amazonaws.com",
				"nudgebee-emails.s3-website-us-east-1.amazonaws.com",
				"nudgebee-emails.s3.amazonaws.com",
			},
		},
		{
			name: "s3_falls_back_to_row_region",
			props: map[string]interface{}{
				"service_name": "AmazonS3",
				"name":         "my-bucket",
				"region":       "eu-west-2",
			},
			wantDNS: "my-bucket.s3.eu-west-2.amazonaws.com",
			wantAliases: []string{
				"my-bucket.s3.dualstack.eu-west-2.amazonaws.com",
				"my-bucket.s3-website-eu-west-2.amazonaws.com",
				"my-bucket.s3.amazonaws.com",
			},
		},
		// Region-/account-scoped service endpoints are intentionally NOT
		// synthesized — see flow_sources.AwsServiceDNS doc for rationale.
		// These cases assert the no-op (no dns_name written, no aliases).
		{
			name: "sqs_not_synthesized",
			props: map[string]interface{}{
				"service_name": "AWSQueueService",
				"name":         "my-queue",
				"region":       "us-west-2",
			},
			wantDNS: "",
		},
		{
			name: "sns_not_synthesized",
			props: map[string]interface{}{
				"service_name": "AmazonSNS",
				"name":         "my-topic",
				"region":       "eu-west-1",
			},
			wantDNS: "",
		},
		{
			name: "dynamodb_not_synthesized",
			props: map[string]interface{}{
				"service_name":       "AmazonDynamoDB",
				"name":               "my-table",
				"region":             "us-east-1",
				"aws_account_number": testenv.FakeAWSAccountID,
			},
			wantDNS: "",
		},
		{
			name: "ecr_not_synthesized",
			props: map[string]interface{}{
				"service_name":       "AmazonECR",
				"repository_name":    "myapp",
				"region":             "us-east-1",
				"aws_account_number": testenv.FakeAWSAccountID,
				"repository_uri":     testenv.FakeAWSAccountID + ".dkr.ecr.us-east-1.amazonaws.com/myapp",
			},
			wantDNS: "",
		},
		{
			name: "lambda_not_synthesized",
			props: map[string]interface{}{
				"service_name": "AWSLambda",
				"name":         "my-function",
				"region":       "us-east-1",
			},
			wantDNS: "",
		},
		{
			name: "apigateway_uses_resource_id_as_subdomain",
			props: map[string]interface{}{
				"service_name": "AmazonAPIGateway",
				"name":         "my-api",
				"resource_id":  "abcd1234",
				"region":       "us-east-1",
			},
			wantDNS: "abcd1234.execute-api.us-east-1.amazonaws.com",
		},
		{
			name: "preserves_existing_dns_name",
			props: map[string]interface{}{
				"service_name": "AmazonS3",
				"bucket_name":  "preset",
				"region":       "us-east-1",
				"dns_name":     "already-set.example.com",
			},
			wantDNS: "already-set.example.com", // synthesizer is a no-op
		},
		{
			name: "unknown_service_no_op",
			props: map[string]interface{}{
				"service_name": "AmazonRDS",
				"region":       "us-east-1",
				"name":         "mydb",
			},
			wantDNS: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synthesizeAWSEndpointDNS(tc.props)

			gotDNS, _ := tc.props["dns_name"].(string)
			if gotDNS != tc.wantDNS {
				t.Errorf("dns_name = %q, want %q", gotDNS, tc.wantDNS)
			}

			if len(tc.wantAliases) > 0 {
				gotAliases, _ := tc.props["dns_aliases"].([]string)
				if !equalStringSlices(gotAliases, tc.wantAliases) {
					t.Errorf("dns_aliases = %v, want %v", gotAliases, tc.wantAliases)
				}
			} else if _, has := tc.props["dns_aliases"]; has {
				t.Errorf("expected no dns_aliases, got %v", tc.props["dns_aliases"])
			}
		})
	}
}

func TestRepoURIHost(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{"with_scheme", "https://" + testenv.FakeAWSAccountID + ".dkr.ecr.us-east-1.amazonaws.com/foo", testenv.FakeAWSAccountID + ".dkr.ecr.us-east-1.amazonaws.com"},
		{"without_scheme", testenv.FakeAWSAccountID + ".dkr.ecr.us-east-1.amazonaws.com/foo", testenv.FakeAWSAccountID + ".dkr.ecr.us-east-1.amazonaws.com"},
		{"public_ecr", "public.ecr.aws/abc/myrepo", "public.ecr.aws"},
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"eks_endpoint", "https://EXAMPLE0.gr7.us-east-1.eks.amazonaws.com", "EXAMPLE0.gr7.us-east-1.eks.amazonaws.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoURIHost(tc.uri); got != tc.want {
				t.Errorf("repoURIHost(%q) = %q, want %q", tc.uri, got, tc.want)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
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
