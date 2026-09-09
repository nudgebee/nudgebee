package tools

import (
	"errors"
	"nudgebee/llm/workspace"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// workspaceSafePathRe mirrors safePathRe in llm/code-analysis/api/handlers/execution_handler.go,
// which is what actually rejects a bad working-directory name at the far end of this call. Kept as a
// literal copy because the two services do not share a module.
var workspaceSafePathRe = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// A cloud CLI command run from the remediation panel has no conversation behind it. Passing "" made
// the workspace reject every such run with `Conversation ID is empty`, which surfaced in the panel as
// a failed remediation on a command that never executed.
func TestDefaultCloudCliConversationId(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accountId string
		want      string
	}{
		{"uuid account id", "883efbbc-bb2c-404b-9ed9-6b7ecbf6f509", "remediation-883efbbc-bb2c-404b-9ed9-6b7ecbf6f509"},
		{"surrounding whitespace is trimmed", "  abc123  ", "remediation-abc123"},
		{"path separators cannot escape the workspace dir", "../../etc/passwd", "remediation-------etc-passwd"},
		{"other unsafe characters are replaced", "acc$id space", "remediation-acc-id-space"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultCloudCliConversationId(tc.accountId)
			assert.Equal(t, tc.want, got)
			assert.NotEmpty(t, got, "an empty id is what the workspace rejects")
			assert.Regexp(t, workspaceSafePathRe, got, "the workspace rejects any id outside ^[a-zA-Z0-9_-]+$")
		})
	}
}

// The %w in wrapCloudCliErr is load-bearing: workspaceOutcome unwraps to *workspace.CommandFailure
// to recover the exit code the operator is shown, and %v breaks that with no other symptom.
func TestWrapCloudCliErrPreservesFailure(t *testing.T) {
	wrapped := wrapCloudCliErr(ToolExecuteAwsCliCommand,
		&workspace.CommandFailure{Status: "failed", StdErr: "exit status 254"})

	var failure *workspace.CommandFailure
	assert.True(t, errors.As(wrapped, &failure), "%w must survive so the exit code can be recovered")
	assert.Equal(t, "exit status 254", failure.StdErr)

	code, ok := workspace.ExitCodeFromFailure(wrapped)
	assert.True(t, ok)
	assert.Equal(t, 254, code)

	assert.Contains(t, wrapped.Error(), ToolExecuteAwsCliCommand, "the tool name stays in the message")
}
