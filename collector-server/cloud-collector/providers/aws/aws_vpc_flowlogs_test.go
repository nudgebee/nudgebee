package aws

import (
	"testing"

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
