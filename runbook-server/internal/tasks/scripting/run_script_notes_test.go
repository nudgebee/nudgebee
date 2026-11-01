package scripting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The automation builder learns where a script runs, and as whom, from these
// notes (get_task_schema). Losing one of these facts is how it built script
// steps that could never run.
func TestRunScriptRuntimeNotes_ExecutorGuidance(t *testing.T) {
	notes := strings.Join((&RunScriptTask{}).RuntimeNotes(), "\n")
	for _, want := range []string{
		"executor_type 'agent'",
		"executor_type 'kubernetes'",
		"A Kubernetes account cannot use it",
		"'aws_ssm'",
		"Never sign in inside a script",
		"One step, one identity",
		"k8s.cli",
		"/tmp",
	} {
		assert.Contains(t, notes, want)
	}
}
