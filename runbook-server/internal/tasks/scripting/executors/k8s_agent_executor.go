package executors

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	nudgebeeConfig "nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var envVarRegex = regexp.MustCompile("^[a-zA-Z_][a-zA-Z0-9_]*$")

// AgentExecutor implements ScriptExecutor using os/exec.
// Note: This executor runs scripts on the host machine and is not recommended for untrusted input.
type AgentExecutor struct{}

func NewAgentExecutor() *AgentExecutor {
	return &AgentExecutor{}
}

func (e *AgentExecutor) Execute(taskCtx types.TaskContext, config ExecutionConfig) (string, error) {
	slog.Debug("AgentExecutor: Starting execution", "language", config.Language, "accountId", config.AccountID)
	securityCtx := taskCtx.GetNewRequestContext()

	accountId := taskCtx.GetAccountID()
	if config.AccountID != "" {
		accountId = config.AccountID
	}

	var finalScriptCommand string

	// Prepare arguments string
	var argsBuilder strings.Builder
	for _, arg := range config.Args {
		// Basic quoting to handle spaces.
		// For more complex cases (quotes in args), more robust escaping might be needed.
		// Assuming simple args for now or user handles escaping if needed.
		// Ideally, we should shell-escape this.
		fmt.Fprintf(&argsBuilder, " '%s'", strings.ReplaceAll(arg, "'", "'\\''"))
	}
	argsStr := argsBuilder.String()

	scriptContent := config.Script
	if config.Language == "powershell" {
		scriptContent = BuildPowerShellConfigPrefix() + "\n" + config.Script
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(scriptContent))

	var envPrefix string
	if len(config.Env) > 0 {
		var sb strings.Builder
		for k, v := range config.Env {
			if !envVarRegex.MatchString(k) {
				return "", fmt.Errorf("invalid environment variable name: %s", k)
			}
			fmt.Fprintf(&sb, "export %s='%s'; ", k, strings.ReplaceAll(v, "'", "'\\''"))
		}
		envPrefix = sb.String()
	}

	switch config.Language {
	case "bash":
		// bash -s -- arg1 arg2 ... reads script from stdin
		finalScriptCommand = fmt.Sprintf("%secho %s | base64 -d | bash -s --%s", envPrefix, encoded, argsStr)
	case "python":
		// python3 - arg1 arg2 ... reads script from stdin
		finalScriptCommand = fmt.Sprintf("%secho %s | base64 -d | python3 -%s", envPrefix, encoded, argsStr)
	case "javascript":
		// node - arg1 arg2 ... reads script from stdin
		finalScriptCommand = fmt.Sprintf("%secho %s | base64 -d | node -%s", envPrefix, encoded, argsStr)
	case "powershell":
		// pwsh doesn't read scripts from stdin, so decode to a unique temp file and execute
		tmpFile := fmt.Sprintf("/tmp/_nb_script_%s.ps1", uuid.New().String())
		finalScriptCommand = fmt.Sprintf("%secho %s | base64 -d > %s && pwsh -NoProfile -File %s%s; rm -f %s", envPrefix, encoded, tmpFile, tmpFile, argsStr, tmpFile)
	default:
		return "", fmt.Errorf("unsupported language for AgentExecutor: %s", config.Language)
	}

	// Select the right container image for the relay pod based on language
	relayConfigs := map[string]any{}
	switch config.Language {
	case "javascript":
		relayConfigs["image"] = nudgebeeConfig.Config.ScriptExecutorNodeImage
	case "powershell":
		relayConfigs["image"] = nudgebeeConfig.Config.ScriptExecutorPowerShellImage
	}

	// Derive timeout from the task context deadline so the relay pod respects
	// the per-task timeout rather than the global config default.
	if deadline, ok := taskCtx.GetContext().Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		relayConfigs["timeout_ms"] = float64(remaining.Milliseconds())
	}

	resp, err := relay.ExecuteRelayJob(securityCtx, accountId, relay.RelayJobShell, "", finalScriptCommand, relayConfigs)
	if err != nil {
		// Provide actionable hints for interpreter-not-found errors
		errMsg := err.Error()
		if (config.Language == "powershell" || config.Language == "javascript") &&
			(strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "No such file")) {
			interpreter := "pwsh"
			if config.Language == "javascript" {
				interpreter = "node"
			}
			return "", fmt.Errorf("agent execution failed: %q is not installed on the relay agent. "+
				"Install %s on the agent node or use the Kubernetes executor instead: %w", interpreter, config.Language, err)
		}
		return "", fmt.Errorf("agent execution failed: %w", err)
	}

	respStr, ok := resp.(string)
	if !ok {
		return "", fmt.Errorf("agent execution failed: unexpected response type %T", resp)
	}

	if strings.Contains(respStr, "stdout") {
		respMap := map[string]any{}
		err := json.Unmarshal([]byte(respStr), &respMap)
		if err == nil {
			if stdout, ok := respMap["stdout"].(string); ok {
				return strings.TrimSpace(stdout), nil
			}
		}
	}

	return "", fmt.Errorf("agent execution failed: %s", respStr)
}
