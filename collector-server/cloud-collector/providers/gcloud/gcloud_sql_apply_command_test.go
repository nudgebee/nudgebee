package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveSQLInstanceName_PrefersArgs verifies that when the caller passes
// instance_name in Args, the resolver returns it directly even if ResourceId
// would have produced a different value.
func TestResolveSQLInstanceName_PrefersArgs(t *testing.T) {
	cmd := providers.ApplyCommandRequest{
		ResourceId: "nudgebee-dev:other-instance",
		Args:       map[string]any{"instance_name": "my-sql"},
	}

	assert.Equal(t, "my-sql", resolveSQLInstanceName(cmd))
}

// TestResolveSQLInstanceName_FallbackToResourceID covers the path the UI hits:
// no args, ResourceId is the "<project>:<instance>" tuple that GetResources
// stores. The resolver must strip the project prefix so the sqladmin SDK
// receives the bare instance name. This is the regression case for issue
// #31242 (UI "Stop Instance" failed with "instance_name arg required").
func TestResolveSQLInstanceName_FallbackToResourceID(t *testing.T) {
	tests := []struct {
		name     string
		cmd      providers.ApplyCommandRequest
		expected string
	}{
		{
			name: "project-qualified resource id",
			cmd: providers.ApplyCommandRequest{
				ResourceId: "nudgebee-dev:utsav-nudgebee-2026",
			},
			expected: "utsav-nudgebee-2026",
		},
		{
			name: "bare instance name without project prefix",
			cmd: providers.ApplyCommandRequest{
				ResourceId: "beehive-dev-pg",
			},
			expected: "beehive-dev-pg",
		},
		{
			name: "empty args map, project-qualified resource id",
			cmd: providers.ApplyCommandRequest{
				ResourceId: "my-project:my-instance",
				Args:       map[string]any{},
			},
			expected: "my-instance",
		},
		{
			name: "empty string instance_name falls through to ResourceId",
			cmd: providers.ApplyCommandRequest{
				ResourceId: "nudgebee-dev:beehive-test-pg",
				Args:       map[string]any{"instance_name": ""},
			},
			expected: "beehive-test-pg",
		},
		{
			name: "non-string instance_name falls through to ResourceId",
			cmd: providers.ApplyCommandRequest{
				ResourceId: "nudgebee-dev:beehive-test-pg",
				Args:       map[string]any{"instance_name": 123},
			},
			expected: "beehive-test-pg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolveSQLInstanceName(tt.cmd))
		})
	}
}

// TestResolveSQLInstanceName_EmptyWhenNothingResolves ensures the resolver
// returns an empty string (which the caller turns into "instance_name arg
// required") when neither Args nor ResourceId are usable.
func TestResolveSQLInstanceName_EmptyWhenNothingResolves(t *testing.T) {
	tests := []providers.ApplyCommandRequest{
		{},
		{Args: map[string]any{}},
		{ResourceId: "", Args: map[string]any{"instance_name": ""}},
	}

	for _, cmd := range tests {
		assert.Empty(t, resolveSQLInstanceName(cmd))
	}
}
