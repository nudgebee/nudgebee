package executors

import (
	"fmt"
	"nudgebee/runbook/config"
	"os"
	"strings"
)

// NewExecutor creates a new ScriptExecutor based on configuration.
// It accepts an ExecutionConfig to determine the mode.
func NewExecutor(execConfig ExecutionConfig) (ScriptExecutor, error) {
	// 1. Explicit executor type takes priority
	mode := execConfig.ExecutorType

	// 2. If no explicit type, route based on account provider
	if mode == "" && execConfig.AccountProvider != "" {
		switch strings.ToLower(execConfig.AccountProvider) {
		case "k8s", "kubernetes":
			return NewAgentExecutor(), nil
		case "aws", "azure", "gcp":
			return NewKubernetesExecutor()
		}
	}

	// 3. Fall back to system config
	if mode == "" {
		mode = config.Config.TaskScriptExecutionModel
	}
	if mode == "" {
		mode = "agent"
	}

	switch strings.ToLower(mode) {
	case "kubernetes":
		return NewKubernetesExecutor()
	case "agent":
		return NewAgentExecutor(), nil
	case "local":
		if os.Getenv("ALLOW_LOCAL_EXECUTOR") != "true" {
			return nil, fmt.Errorf("local executor is disabled for security; set ALLOW_LOCAL_EXECUTOR=true to enable in development")
		}
		return NewLocalExecutor(), nil
	case "aws_ssm":
		return NewAwsSsmExecutor(), nil
	case "azure_run_command":
		return NewAzureRunCommandExecutor(), nil
	case "gcp_compute_ssh":
		return NewGcpComputeExecutor(), nil
	case "ssh":
		return NewSshExecutor(), nil
	default:
		return nil, fmt.Errorf("unknown script execution mode: %s", mode)
	}
}
