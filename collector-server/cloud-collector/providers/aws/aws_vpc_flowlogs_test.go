package aws

import (
	"context"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
)

func TestExtractELBIdentifierFromENI(t *testing.T) {
	tests := []struct {
		name               string
		eniDescription     string
		expectedIdentifier string
		expectedKind       string
	}{
		{
			name:               "ALB with full identifier",
			eniDescription:     "ELB app/Demo-Frontend-ALB/9ef0c75b824fa80c",
			expectedIdentifier: "app/Demo-Frontend-ALB/9ef0c75b824fa80c",
			expectedKind:       "elbv2",
		},
		{
			name:               "NLB with full identifier",
			eniDescription:     "ELB net/my-nlb/50dc6c495f0c9188",
			expectedIdentifier: "net/my-nlb/50dc6c495f0c9188",
			expectedKind:       "elbv2",
		},
		{
			name:               "Classic ELB",
			eniDescription:     "ELB my-classic-elb",
			expectedIdentifier: "my-classic-elb",
			expectedKind:       "elb",
		},
		{
			name:               "ELB with lowercase prefix",
			eniDescription:     "elb app/test-alb/abc123",
			expectedIdentifier: "app/test-alb/abc123",
			expectedKind:       "elbv2",
		},
		{
			name:               "Not an ELB ENI - Lambda",
			eniDescription:     "AWS Lambda VPC ENI-my-function",
			expectedIdentifier: "",
			expectedKind:       "",
		},
		{
			name:               "Not an ELB ENI - generic description",
			eniDescription:     "Primary network interface",
			expectedIdentifier: "",
			expectedKind:       "",
		},
		{
			name:               "Empty description",
			eniDescription:     "",
			expectedIdentifier: "",
			expectedKind:       "",
		},
		{
			name:               "ELB prefix only",
			eniDescription:     "ELB ",
			expectedIdentifier: "",
			expectedKind:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identifier, kind := extractELBIdentifierFromENI(tt.eniDescription)
			assert.Equal(t, tt.expectedIdentifier, identifier,
				"identifier mismatch for description: %s", tt.eniDescription)
			assert.Equal(t, tt.expectedKind, kind,
				"kind mismatch for description: %s", tt.eniDescription)
		})
	}
}

func TestExtractLambdaNameFromENI(t *testing.T) {
	tests := []struct {
		name           string
		eniDescription string
		expectedName   string
	}{
		{
			name:           "Lambda ENI with UUID",
			eniDescription: "AWS Lambda VPC ENI-my-function-abc12345",
			expectedName:   "my-function",
		},
		{
			name:           "Lambda ENI without UUID (short name)",
			eniDescription: "AWS Lambda VPC ENI-myfunction",
			expectedName:   "myfunction",
		},
		{
			name:           "Lambda ENI with multi-part name and UUID",
			eniDescription: "AWS Lambda VPC ENI-my-long-function-name-xyz98765",
			expectedName:   "my-long-function-name",
		},
		{
			name:           "Not a Lambda ENI - ELB",
			eniDescription: "ELB app/Demo-Frontend-ALB/9ef0c75b824fa80c",
			expectedName:   "",
		},
		{
			name:           "Not a Lambda ENI - generic",
			eniDescription: "Primary network interface",
			expectedName:   "",
		},
		{
			name:           "Empty description",
			eniDescription: "",
			expectedName:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := extractLambdaNameFromENI(tt.eniDescription)
			assert.Equal(t, tt.expectedName, name,
				"name mismatch for description: %s", tt.eniDescription)
		})
	}
}

// TestResolveRDSEndpointToIP_HonoursContext checks that endpoint resolution is
// cancellable. net.LookupIP ignores context entirely, so before this was bounded
// an unreachable resolver blocked the caller indefinitely — and callers resolve
// one endpoint per RDS instance in sequence, so a single slow lookup could stall
// a whole collection run.
func TestResolveRDSEndpointToIP_HonoursContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := ResolveRDSEndpointToIP(
		providers.NewCloudProviderContext(cancelled),
		"main.ca5yt51qtp3r.us-east-1.rds.amazonaws.com",
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("resolution succeeded on a cancelled context, want an error")
	}
	// Well under the 2s cap: cancellation must be observed rather than waited out.
	if elapsed > time.Second {
		t.Errorf("took %s to observe a cancelled context, want it to return promptly", elapsed)
	}
}
