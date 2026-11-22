package tools

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setPipelineStatusFlag(t *testing.T, on bool) {
	t.Helper()
	old := config.Config.LlmServerCliPipelineStatusEnabled
	config.Config.LlmServerCliPipelineStatusEnabled = on
	t.Cleanup(func() { config.Config.LlmServerCliPipelineStatusEnabled = old })
}

// runLikeWorkspace runs what the tool would send, the way the workspace does:
// a shell command line plus the env map.
func runLikeWorkspace(t *testing.T, command string) (out string, exit int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	execCommand, env := withPipelineStatus(command, map[string]string{"KEEP": "1"})
	cmd := exec.Command("sh", "-c", execCommand)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(b), ee.ExitCode()
	}
	require.NoError(t, err)
	return string(b), 0
}

func TestWithPipelineStatus_ExitCodes(t *testing.T) {
	setPipelineStatusFlag(t, true)
	tests := []struct {
		name, command string
		wantExit      int
	}{
		// What the wrapper fixes: the producer failed, the last stage hid it.
		{"failed first stage behind grep and head", "sh -c 'echo gh: not found >&2; exit 1' | grep x | head -n 5", 1},
		{"failed first stage keeps its own code", "sh -c 'exit 4' | cat", 4},
		// What must not change.
		{"all stages succeed", "echo hi | cat", 0},
		{"grep finding nothing is not a failure", "echo hi | grep zzz | head -n 5", 0},
		{"last stage failing still fails, as before", "echo hi | grep -c zzz", 1},
		{"head closing the pipe early is not a failure", "yes | head -n 1", 0},
		{"a pipe inside quotes is a plain command", "echo 'a|b'", 0},
		{"a failing command with a quoted pipe still fails", "sh -c 'exit 3' 'x|y'", 3},
		{"trailing comment does not swallow the group end", "echo hi | cat # note", 0},
		{"heredoc into a pipeline", "cat <<EOF | cat\nhi\nEOF", 0},
		{"multi-line command", "echo a\nfalse | true", 1},
		// Sent as written: the model asked to ignore a failure, or a group cannot hold it.
		{"|| true is left alone", "false || true", 0},
		{"a pipeline with || true keeps the exit the model asked for", "false | cat || true", 0},
		{"a trailing & is left alone", "echo hi | cat &", 0},
		{"a trailing backslash is left alone (cat reads a file named backslash and fails, as written)", "echo hi | cat \\", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, exit := runLikeWorkspace(t, tt.command)
			assert.Equal(t, tt.wantExit, exit)
		})
	}
}

func TestWithPipelineStatus_OutputIsUnchanged(t *testing.T) {
	setPipelineStatusFlag(t, true)
	out, exit := runLikeWorkspace(t, "printf 'one\\ntwo\\n' | grep t")
	assert.Equal(t, 0, exit)
	assert.Equal(t, "two\n", out)

	out, _ = runLikeWorkspace(t, `echo "it's | quoted"`)
	assert.Equal(t, "it's | quoted\n", out)
}

func TestWithPipelineStatus_WhenItWraps(t *testing.T) {
	setPipelineStatusFlag(t, true)

	cmd, env := withPipelineStatus("gh pr list", map[string]string{"A": "1"})
	assert.Equal(t, "gh pr list", cmd, "no pipe, sent as written")
	assert.NotContains(t, env, cliScriptEnv)

	cmd, env = withPipelineStatus("gh pr list | head", map[string]string{"A": "1"})
	assert.Equal(t, `bash -c "$`+cliScriptEnv+`"`, cmd)
	assert.Equal(t, "1", env["A"], "the caller's env carries over")
	assert.Contains(t, env[cliScriptEnv], "gh pr list | head")

	original := map[string]string{"A": "1"}
	withPipelineStatus("a | b", original)
	assert.NotContains(t, original, cliScriptEnv, "the caller's map is not modified")

	for _, asWritten := range []string{"gh a | head || true", "gh a | head &", "gh a | head \\", "gh a || true"} {
		cmd, env = withPipelineStatus(asWritten, nil)
		assert.Equal(t, asWritten, cmd, "sent as written: %q", asWritten)
		assert.NotContains(t, env, cliScriptEnv)
	}

	setPipelineStatusFlag(t, false)
	cmd, _ = withPipelineStatus("gh pr list | head", nil)
	assert.Equal(t, "gh pr list | head", cmd, "flag off sends the command as written")
}

func TestWithPipelineStatus_CommandWithPercentSigns(t *testing.T) {
	setPipelineStatusFlag(t, true)
	_, env := withPipelineStatus(`gh api x --jq '.a | "%s %d"'`, nil)
	assert.True(t, strings.Contains(env[cliScriptEnv], `"%s %d"`), "a % in the command is not read as a format verb")
}
