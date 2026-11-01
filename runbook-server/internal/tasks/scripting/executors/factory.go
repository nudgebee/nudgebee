package executors

import (
	"errors"
	"fmt"
	"nudgebee/runbook/config"
	"slices"
	"strings"
)

// ResolveExecutorType names the executor NewExecutor builds for a config: the
// explicit type, else the account provider's default, else the system config,
// else "agent". Pure, so callers can check the pairing before building anything.
func ResolveExecutorType(execConfig ExecutionConfig) string {
	// 1. Explicit executor type takes priority
	if mode := execConfig.ExecutorType; mode != "" {
		return strings.ToLower(mode)
	}
	// 2. If no explicit type, route based on account provider
	switch strings.ToLower(execConfig.AccountProvider) {
	case "k8s", "kubernetes":
		return "agent"
	case "aws", "azure", "gcp":
		return "kubernetes"
	}
	// 3. Fall back to system config
	if mode := config.Config.TaskScriptExecutionModel; mode != "" {
		return strings.ToLower(mode)
	}
	return "agent"
}

// NewExecutor creates a new ScriptExecutor based on configuration.
// It accepts an ExecutionConfig to determine the mode.
func NewExecutor(execConfig ExecutionConfig) (ScriptExecutor, error) {
	switch mode := ResolveExecutorType(execConfig); mode {
	case "kubernetes":
		return NewKubernetesExecutor()
	case "agent":
		return NewAgentExecutor(), nil
	case "local":
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

// accountProvidersFor names the account providers each executor can run a
// script for. The Kubernetes executor runs on Nudgebee's cluster with
// credentials minted for the account, so it needs a cloud account; each cloud
// run-command executor needs an account of its own cloud. Executors missing
// from the map (agent, ssh, local) are not restricted here.
var accountProvidersFor = map[string][]string{
	"kubernetes":        {"aws", "azure", "gcp"},
	"aws_ssm":           {"aws"},
	"azure_run_command": {"azure"},
	"gcp_compute_ssh":   {"gcp"},
}

var accountNoun = map[string]string{"aws": "an AWS", "azure": "an Azure", "gcp": "a GCP"}

func normalizeAccountProvider(provider string) string {
	switch p := strings.ToLower(strings.TrimSpace(provider)); p {
	case "k8s", "kubernetes":
		return "k8s"
	case "gcp", "gcloud", "google":
		return "gcp"
	default:
		return p
	}
}

// CheckAccountSupports reports why a script step whose account has the given
// provider cannot run on executorType, in words that tell the user what to
// change. It returns nil when the pair works, or when either side is unknown.
func CheckAccountSupports(executorType, provider string) error {
	executorType = strings.ToLower(strings.TrimSpace(executorType))
	wanted, restricted := accountProvidersFor[executorType]
	p := normalizeAccountProvider(provider)
	if !restricted || p == "" || slices.Contains(wanted, p) {
		return nil
	}
	switch {
	case p == "k8s" && executorType == "kubernetes":
		return errors.New("this step's account is a Kubernetes cluster, which has no cloud credentials for the Kubernetes executor to use: set Executor Type to Agent to run the script inside that cluster")
	case p == "k8s":
		return fmt.Errorf("this step's account is a Kubernetes cluster, but the %s executor needs %s account: choose that account, or set Executor Type to Agent to run the script inside the cluster", executorType, accountNoun[wanted[0]])
	case executorType == "kubernetes":
		return fmt.Errorf("a %s account has no cloud credentials for the Kubernetes executor to use: choose an AWS, Azure or GCP account, or another executor", provider)
	default:
		return fmt.Errorf("the %s executor needs %s account, and this step's account is %s", executorType, accountNoun[wanted[0]], provider)
	}
}
