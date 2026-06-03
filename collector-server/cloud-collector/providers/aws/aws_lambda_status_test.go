package aws

import (
	"testing"

	"github.com/aws/smithy-go/ptr"

	"nudgebee/collector/cloud/providers"
)

// TestLambdaStatusToNbStatus pins the State → ResourceStatus mapping.
// The empty-string case is the one that matters for #30682: AWS SDK's
// lambda.ListFunctions does not populate FunctionConfiguration.State,
// so we receive "" for every row and must treat that as Active (function
// is listed = function exists), otherwise the downstream KG fetcher's
// `cr.status = 'Active'` filter drops all functions.
func TestLambdaStatusToNbStatus(t *testing.T) {
	cases := []struct {
		name string
		in   *string
		want providers.ResourceStatus
	}{
		{"nil_status", nil, providers.ResourceStatusUnknown},
		{"empty_string_from_ListFunctions", ptr.String(""), providers.ResourceStatusActive},
		{"active", ptr.String("Active"), providers.ResourceStatusActive},
		{"active_lowercase", ptr.String("active"), providers.ResourceStatusActive},
		{"pending", ptr.String("Pending"), providers.ResourceStatusActive},
		{"inprogress", ptr.String("InProgress"), providers.ResourceStatusActive},
		{"successful", ptr.String("Successful"), providers.ResourceStatusActive},
		{"terminated", ptr.String("Terminated"), providers.ResourceStatusDeleted},
		{"inactive", ptr.String("Inactive"), providers.ResourceStatusInactive},
		{"failed", ptr.String("Failed"), providers.ResourceStatusInactive},
		{"garbage", ptr.String("garbage"), providers.ResourceStatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lambdaStatusToNbStatus(tc.in)
			if got != tc.want {
				t.Errorf("lambdaStatusToNbStatus(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
