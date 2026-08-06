package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestWorkspaceCommandTimeoutDefault_DerivedFromHTTPClientTimeout pins the
// relationship between WorkspaceHTTPClientTimeout (the HTTP client timeout
// workspace.NewWorkspaceManager uses) and llm_server_workspace_command_timeout's
// default (58s). They're computed from the same two constants specifically so
// they can't drift apart if one changes without the other — this test fails
// loudly if that ever happens instead of silently producing a mismatched pair.
func TestWorkspaceCommandTimeoutDefault_DerivedFromHTTPClientTimeout(t *testing.T) {
	assert.Equal(t, 58*time.Second, WorkspaceHTTPClientTimeout-workspaceCommandTimeoutBuffer,
		"llm_server_workspace_command_timeout's default is WorkspaceHTTPClientTimeout minus "+
			"workspaceCommandTimeoutBuffer — if this fails, one of those constants changed and "+
			"the default moved with it (which may or may not be intended)")
}
