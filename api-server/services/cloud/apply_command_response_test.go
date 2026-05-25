package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// parseApplyCommandResponse is the seam between the api-server's
// /rpc/cloud handler and the cloud-collector. The bug it replaces
// silently turned every provider-side failure into "apply_command failed
// with status 500" on the UI, so this test specifically pins down the
// real-world AWS / Azure / GCP error envelopes we observed in production.
func TestParseApplyCommandResponse(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantSuccess bool
		wantMessage string
		wantErr     bool
	}{
		{
			name:        "200 success",
			statusCode:  200,
			body:        `{"data":{"success":true,"message":"Successfully started instance i-0455"}}`,
			wantSuccess: true,
			wantMessage: "Successfully started instance i-0455",
		},
		{
			name:        "200 with success=false (provider returned ok-but-failed body)",
			statusCode:  200,
			body:        `{"data":{"success":false,"message":"already in target state"}}`,
			wantSuccess: false,
			wantMessage: "already in target state",
		},
		{
			name:       "500 with cloud-collector AWS UnauthorizedOperation envelope",
			statusCode: 500,
			body: `{"data":null,"errors":[{"message":"Failed to apply command: failed to stop instances: ` +
				`operation error EC2: StopInstances, https response error StatusCode: 403, ` +
				`api error UnauthorizedOperation: You are not authorized to perform this operation."}]}`,
			wantSuccess: false,
			wantMessage: "Failed to apply command: failed to stop instances: operation error EC2: StopInstances, https response error StatusCode: 403, api error UnauthorizedOperation: You are not authorized to perform this operation.",
		},
		{
			name:       "500 with cloud-collector GCP 403 envelope",
			statusCode: 500,
			body: `{"data":null,"errors":[{"message":"Failed to apply command: failed to start instance: ` +
				`googleapi: Error 403: Required 'compute.instances.start' permission for ` +
				`'projects/nudgebee-dev/zones/us-central1-c/instances/for-testing'"}]}`,
			wantSuccess: false,
			wantMessage: "Failed to apply command: failed to start instance: googleapi: Error 403: Required 'compute.instances.start' permission for 'projects/nudgebee-dev/zones/us-central1-c/instances/for-testing'",
		},
		{
			name:       "500 with cloud-collector Azure AuthorizationFailed envelope",
			statusCode: 500,
			body: `{"data":null,"errors":[{"message":"Failed to apply command: failed to deallocate VM: ` +
				`AuthorizationFailed: The client does not have authorization to perform action ` +
				`'Microsoft.Compute/virtualMachines/deallocate/action'"}]}`,
			wantSuccess: false,
			wantMessage: "Failed to apply command: failed to deallocate VM: AuthorizationFailed: The client does not have authorization to perform action 'Microsoft.Compute/virtualMachines/deallocate/action'",
		},
		{
			name:        "500 with empty errors array falls back to status + body",
			statusCode:  500,
			body:        `{"data":null,"errors":[]}`,
			wantSuccess: false,
			wantMessage: `cloud-collector returned 500: {"data":null,"errors":[]}`,
		},
		{
			name:        "502 with non-JSON body falls back to status + body",
			statusCode:  502,
			body:        `Bad Gateway`,
			wantSuccess: false,
			wantMessage: "cloud-collector returned 502: Bad Gateway",
		},
		{
			name:        "503 with empty body falls back to bare status message",
			statusCode:  503,
			body:        ``,
			wantSuccess: false,
			wantMessage: "cloud-collector returned 503",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseApplyCommandResponse(tt.statusCode, []byte(tt.body))

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantSuccess, got.Success)
			assert.Equal(t, tt.wantMessage, got.Message)
		})
	}
}

// 200-with-malformed-body is a true server bug, not a business failure —
// must surface as a non-nil error so the handler returns 500 rather than
// pretending the action succeeded with garbage data.
func TestParseApplyCommandResponse_200MalformedReturnsError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"unparseable json", `not-json`},
		{"missing data field", `{"errors":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseApplyCommandResponse(200, []byte(tt.body))
			assert.Error(t, err)
		})
	}
}
