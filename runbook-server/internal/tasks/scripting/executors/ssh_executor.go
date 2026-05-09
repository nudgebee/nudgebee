package executors

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
)

type SshExecutor struct{}

func NewSshExecutor() *SshExecutor {
	return &SshExecutor{}
}

func (e *SshExecutor) Execute(taskCtx types.TaskContext, execConfig ExecutionConfig) (string, error) {
	if execConfig.IntegrationID == "" {
		return "", fmt.Errorf("integration_id is required for ssh executor")
	}
	if execConfig.Script == "" {
		return "", fmt.Errorf("script (command) is required")
	}

	requestContext := taskCtx.GetNewRequestContext()

	// Build env prefix
	envPrefix, err := BuildShellEnvPrefix(execConfig.Env)
	if err != nil {
		return "", err
	}

	// Build args string
	argsStr := BuildShellArgsStr(execConfig.Args)

	// Use base64 to safely pass the script through relay and local shell
	encodedScript := base64.StdEncoding.EncodeToString([]byte(execConfig.Script))
	var finalCommand string

	switch execConfig.Language {
	case "powershell":
		psEnvPrefix, psErr := BuildPowerShellEnvPrefix(execConfig.Env)
		if psErr != nil {
			return "", psErr
		}
		psArgsStr := BuildPowerShellArgsStr(execConfig.Args)
		psConfigPrefix := BuildPowerShellConfigPrefix()
		combinedScript := psConfigPrefix + psEnvPrefix + psArgsStr + execConfig.Script
		encoded := EncodePowerShellCommand(combinedScript)
		finalCommand = fmt.Sprintf("powershell -EncodedCommand %s", encoded)
	case "python":
		finalCommand = fmt.Sprintf("%secho %s | base64 -d | python3 -%s", envPrefix, encodedScript, argsStr)
	case "javascript":
		finalCommand = fmt.Sprintf("%secho %s | base64 -d | node -%s", envPrefix, encodedScript, argsStr)
	default:
		// Default to bash
		finalCommand = fmt.Sprintf("%secho %s | base64 -d | bash -s --%s", envPrefix, encodedScript, argsStr)
	}

	resp, err := relay.ExecuteRelayJob(requestContext, execConfig.AccountID, relay.RelayJobSSH, execConfig.IntegrationID, finalCommand, nil)
	if err != nil {
		return "", err
	}

	if respStr, ok := resp.(string); ok {
		sshResp := map[string]any{}
		if err = common.UnmarshalJson([]byte(respStr), &sshResp); err != nil {
			return respStr, nil
		}

		if stderr, ok := sshResp["stderr"].(string); ok && stderr != "" {
			stdout, _ := sshResp["stdout"].(string)
			return fmt.Sprintf("%s\n%s", stdout, stderr), nil
		}

		if stdout, ok := sshResp["stdout"].(string); ok {
			return stdout, nil
		}

		return respStr, nil
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}
	return string(respBytes), nil
}
