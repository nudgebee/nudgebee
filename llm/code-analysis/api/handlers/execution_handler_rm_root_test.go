package handlers

import (
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/config"
)

// TestValidateCommand_RmRootVsSubpath verifies that the `rm -rf /` guard blocks
// deletion of the root filesystem while allowing legitimate absolute-path
// cleanups. The workspace's own temp cleanup (`rm -rf /tmp/code-analysis-<id>-*`)
// was previously false-positived by a substring match on "rm -rf /", which broke
// post-analysis cleanup and left stale clone dirs in the workspace pod.
func TestValidateCommand_RmRootVsSubpath(t *testing.T) {
	h := NewExecutionHandler(&config.Config{})
	tests := []struct {
		name        string
		cmd         string
		expectBlock bool
	}{
		// Must NOT block — absolute-path cleanups under non-sensitive roots.
		{"tmp code-analysis cleanup glob", "rm -rf /tmp/code-analysis-800d56bc-d401-4e49-bb4c-09a5de40184b-*", false},
		{"tmp subpath", "rm -rf /tmp/foo", false},
		{"tmp subpath double slash", "rm -rf //tmp/foo", false},
		{"tmp subpath flags reordered", "rm -fr /tmp/foo", false},
		{"tmp subpath capital R", "rm -R /tmp/foo", false},
		{"relative path", "rm -rf build/", false},
		{"non-recursive root (rm alone fails on dir)", "rm -v /", false},

		// Must block — the root filesystem itself, including quote/slash obfuscation.
		{"root exact", "rm -rf /", true},
		{"root trailing space", "rm -rf / ", true},
		{"root glob", "rm -rf /*", true},
		{"root double slash", "rm -rf //", true},
		{"root triple slash", "rm -rf ///", true},
		{"root double quotes", `rm -rf "/"`, true},
		{"root single quotes", `rm -rf '/'`, true},
		{"root trailing empty quotes", `rm -rf /""`, true},
		{"root glob quoted", `rm -rf "/*"`, true},
		{"root then separator", "rm -rf / && echo done", true},
		// Flag-order / variant coverage — all recursive rm of root.
		{"root flags reordered", "rm -fr /", true},
		{"root verbose recursive", "rm -rfv /", true},
		{"root split flags", "rm -r -f /", true},
		{"root capital R", "rm -R /", true},
		{"root long recursive", "rm --recursive /", true},
		{"root long recursive force", "rm --recursive --force /", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := h.validateCommand(tc.cmd, "")
			blocked := err != nil && strings.Contains(err.Error(), "is blocked")
			if blocked != tc.expectBlock {
				t.Fatalf("validateCommand(%q): blocked=%v (err=%v), want blocked=%v", tc.cmd, blocked, err, tc.expectBlock)
			}
		})
	}
}
