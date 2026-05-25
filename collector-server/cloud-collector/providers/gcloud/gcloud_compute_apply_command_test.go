package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveInstanceTarget_PrefersArgs verifies that when the caller passes
// instance_name and zone via Args, the resolver returns them directly without
// hitting the GCP SDK. This is important because (a) it lets recommendation
// flows that already know the name/zone skip an extra API call, and (b) it
// makes the dispatch logic testable without mocking the SDK.
//
// We deliberately pass a nil *compute.InstancesClient — the function must not
// dereference it on the args-prefer path, and a nil-pointer panic would be a
// regression worth catching.
func TestResolveInstanceTarget_PrefersArgs(t *testing.T) {
	cmd := providers.ApplyCommandRequest{
		ResourceId: "ignored-when-args-set",
		Args: map[string]any{
			"instance_name": "my-vm",
			"zone":          "us-central1-a",
		},
	}

	name, zone, err := resolveInstanceTarget(nil, nil, "test-project", cmd)

	assert.NoError(t, err)
	assert.Equal(t, "my-vm", name)
	assert.Equal(t, "us-central1-a", zone)
}

// TestResolveInstanceTarget_RejectsEmptyResourceID covers the regression case
// the frontend hits today: the caller passes neither full args nor a numeric
// resource id. The function must fail loudly rather than calling the GCP SDK
// with an empty filter (which would silently match every instance).
func TestResolveInstanceTarget_RejectsEmptyResourceID(t *testing.T) {
	cmd := providers.ApplyCommandRequest{
		ResourceId: "",
		Args:       map[string]any{},
	}

	_, _, err := resolveInstanceTarget(nil, nil, "test-project", cmd)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resource_id required")
}

// TestResolveInstanceTarget_FallsThroughOnPartialArgs covers the case where
// the caller supplies one arg but not the other — the resolver must not return
// the partial args, since calling the GCP SDK with empty zone would 404.
// Without a ResourceId set, this should surface "resource_id required" rather
// than falling back to a half-populated args path.
func TestResolveInstanceTarget_FallsThroughOnPartialArgs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{"only instance_name", map[string]any{"instance_name": "my-vm"}},
		{"only zone", map[string]any{"zone": "us-central1-a"}},
		{"empty instance_name", map[string]any{"instance_name": "", "zone": "us-central1-a"}},
		{"empty zone", map[string]any{"instance_name": "my-vm", "zone": ""}},
		{"wrong type for instance_name", map[string]any{"instance_name": 123, "zone": "us-central1-a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := providers.ApplyCommandRequest{
				ResourceId: "",
				Args:       tt.args,
			}
			_, _, err := resolveInstanceTarget(nil, nil, "test-project", cmd)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "resource_id required")
		})
	}
}
