package k8s

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"strings"
)

// K8sCliTask defines a task for executing kubectl commands.
type K8sCliTask struct{}

func (t *K8sCliTask) GetName() string {
	return "k8s.cli"
}

// GetDescription returns a brief description of the task.
func (t *K8sCliTask) GetDescription() string {
	return "Run kubectl commands against your Kubernetes cluster."
}

// GetDisplayName returns a human-readable name for the task.
func (t *K8sCliTask) GetDisplayName() string {
	return "Kubectl"
}

func (t *K8sCliTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing K8sCliTask", "params", params)
	if params["command"] == nil || params["command"] == "" {
		return nil, errors.New("command is requried")
	}

	accountId := taskCtx.GetAccountID()
	if params["account_id"] != nil && params["account_id"] != "" {
		accountId = params["account_id"].(string)
	}

	command := strings.TrimSpace(params["command"].(string))
	if command != "kubectl" && !strings.HasPrefix(command, "kubectl ") {
		command = "kubectl " + command
	}

	requestContext := taskCtx.GetNewRequestContext()
	resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", command, nil)

	if err != nil {
		return nil, err
	}

	if respStr, ok := resp.(string); ok {
		return interpretKubectlResponse(respStr)
	}

	return nil, errors.New("unable to process request")
}

// interpretKubectlResponse turns the agent's kubectl response (JSON with
// stdout/stderr/exit_code) into a task result.
//
// A non-zero exit code is a task failure. The agent returns the exit code as
// data (a failed kubectl is not a transport error), so relying on stderr
// substrings alone silently passes real failures — e.g. "Error from server
// (Forbidden/NotFound)", a write rejected by the agent's read-only verb guard,
// or "no matches for kind". The stderr heuristic is kept only as a fallback for
// the rare exit-0-with-error case, or an older agent that omits exit_code.
func interpretKubectlResponse(respStr string) (map[string]any, error) {
	kubectlResp := map[string]any{}
	if err := common.UnmarshalJson([]byte(respStr), &kubectlResp); err != nil {
		return nil, errors.New(respStr)
	}

	stdout, _ := kubectlResp["stdout"].(string)
	stderr, _ := kubectlResp["stderr"].(string)

	exitCode, hasExit := kubectlExitCode(kubectlResp["exit_code"])
	if (hasExit && exitCode != 0) ||
		strings.Contains(strings.ToLower(stderr), "error:") ||
		strings.Contains(strings.ToLower(stderr), "fail") {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if detail == "" {
			detail = "unknown error"
		}
		if hasExit {
			return nil, fmt.Errorf("kubectl exited %d: %s", exitCode, detail)
		}
		return nil, errors.New(detail)
	}

	// Success: return stdout and any non-error stderr as part of the data.
	output := map[string]any{
		"data": stdout,
	}
	if stderr != "" {
		output["stderr"] = stderr
	}
	return output, nil
}

// kubectlExitCode extracts the kubectl exit code from the agent's response.
// JSON decodes numbers as float64; a few code paths may hand back int/int64.
// Returns (code, true) only when an exit code is actually present.
func kubectlExitCode(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	}
	return 0, false
}

func (t *K8sCliTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Title:       "Account",
				Description: "NB Account Id",
				Required:    false,
				Order:       1,
			},
			"command": {
				Type:        types.PropertyTypeString,
				Title:       "Command",
				Description: "Kubectl Command",
				Required:    true,
				Order:       2,
				// Intentionally no DependsOn: the kubectl command is free-text
				// and has no semantic dependency on which account it targets,
				// so changing the account (typing a new template, picking a
				// different option, switching modes) must NOT wipe what the
				// user has typed. depends_on is reserved for fields whose
				// option list / validity changes with the parent — like
				// namespace → kind → name on workload tasks.
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *K8sCliTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"data": {
				Type:        types.PropertyTypeString,
				Description: "The output (stdout) of the K8s CLI command.",
				Required:    true,
			},
			"stderr": {
				Type:        types.PropertyTypeString,
				Description: "The error output (stderr) of the K8s CLI command, if any.",
			},
		},
	}
}
