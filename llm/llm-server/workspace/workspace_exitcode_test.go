package workspace

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ExitCodeFromFailure is the single site that parses the agent's stderr format, so the shapes it
// must and must not accept are pinned here rather than in each caller.
func TestExitCodeFromFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
		wantOk   bool
	}{
		{"a real non-zero exit", &CommandFailure{Status: "failed", StdErr: "exit status 254"}, 254, true},
		{"exit status 1", &CommandFailure{Status: "failed", StdErr: "exit status 1"}, 1, true},
		{"wrapped survives unwrapping", fmt.Errorf("cloud cli: aws_execute failed: %w", &CommandFailure{Status: "failed", StdErr: "exit status 2"}), 2, true},
		{"trailing detail is still an exit code", &CommandFailure{Status: "failed", StdErr: "exit status 1: something"}, 1, true},
		{"surrounding whitespace", &CommandFailure{Status: "failed", StdErr: "  exit status 7  "}, 7, true},
		// Pre-execution rejections: nothing ran, so there is no exit code to report.
		{"security rejection", &CommandFailure{Status: "failed", StdErr: "Security validation failed: absolute path"}, 0, false},
		{"empty command", &CommandFailure{Status: "failed", StdErr: "Command is empty"}, 0, false},
		{"empty message", &CommandFailure{Status: "failed", StdErr: ""}, 0, false},
		{"not a workspace failure", errors.New("connection refused"), 0, false},
		{"nil", nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, ok := ExitCodeFromFailure(tc.err)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.wantCode, code)
		})
	}
}

// IsExitStatus1Failure stays stricter: "exit status 1: something" is a richer failure that must not
// be reclassified as a grep-family no-match.
func TestIsExitStatus1FailureStaysStrict(t *testing.T) {
	assert.True(t, IsExitStatus1Failure(&CommandFailure{Status: "failed", StdErr: "exit status 1"}))
	assert.False(t, IsExitStatus1Failure(&CommandFailure{Status: "failed", StdErr: "exit status 1: something"}))
}
