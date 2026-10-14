package tools

import (
	"fmt"
	"strings"

	"nudgebee/llm/config"
)

const (
	// cliScriptEnv carries the wrapped script, so the model's command is never
	// re-quoted: an environment value needs no escaping.
	cliScriptEnv = "NB_CLI_SCRIPT"

	// exitSigpipe is what a pipeline stage reports when a later stage such as
	// `head` closed the pipe early. That is normal, not a failure.
	exitSigpipe = 141
)

// pipelineStatusScript runs the command in place, then reports the exit status
// of every stage of its last pipeline. A shell reports only the last stage, so
// `gh run view | grep | head` exits 0 even when gh failed. The command stays
// inline in a group: PIPESTATUS survives a group but not eval or a function.
const pipelineStatusScript = `{
%[1]s
}
nb_ps=("${PIPESTATUS[@]}")
if [ "${#nb_ps[@]}" -gt 1 ] && [ "${nb_ps[0]}" -ne 0 ] && [ "${nb_ps[0]}" -ne %[2]d ]; then
  exit "${nb_ps[0]}"
fi
exit "${nb_ps[${#nb_ps[@]}-1]}"
`

// withPipelineStatus returns the command to send to the workspace and the env to
// send with it. A pipeline whose first stage (the gh/glab call that produces
// the data) failed now exits non-zero, so the workspace records the failure;
// the last stage still decides otherwise, so `grep` finding nothing is not one.
// Sent unchanged: commands without a pipe, commands with `||` (the model asked
// to ignore a failure), and commands ending in `&` or `\` (a group cannot hold
// them).
func withPipelineStatus(command string, env map[string]string) (string, map[string]string) {
	if !config.Config.LlmServerCliPipelineStatusEnabled || !wrapsPipeline(command) {
		return command, env
	}
	wrapped := make(map[string]string, len(env)+1)
	for k, v := range env {
		wrapped[k] = v
	}
	wrapped[cliScriptEnv] = fmt.Sprintf(pipelineStatusScript, command, exitSigpipe)
	return `bash -c "$` + cliScriptEnv + `"`, wrapped
}

func wrapsPipeline(command string) bool {
	if !strings.Contains(command, "|") || strings.Contains(command, "||") {
		return false
	}
	trimmed := strings.TrimSpace(command)
	return !strings.HasSuffix(trimmed, "&") && !strings.HasSuffix(trimmed, `\`)
}
