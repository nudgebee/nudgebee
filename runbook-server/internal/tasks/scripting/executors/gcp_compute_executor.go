package executors

import (
	"fmt"
	"log/slog"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	"strings"
)

type GcpComputeExecutor struct{}

func NewGcpComputeExecutor() *GcpComputeExecutor {
	return &GcpComputeExecutor{}
}

// Execute runs a script on a GCP Compute Engine instance via gcloud compute ssh.
//
// TargetID format:
//   - "instance-name"            — uses the default gcloud project
//   - "project-id/instance-name" — explicitly sets --project
//
// Region must be the full zone (e.g. "us-central1-a").
// IAP tunneling is always used; ensure the service account has roles/iap.tunnelResourceAccessor.
func (e *GcpComputeExecutor) Execute(taskCtx types.TaskContext, execConfig ExecutionConfig) (string, error) {
	if execConfig.TargetID == "" {
		return "", fmt.Errorf("target_id is required for gcp_compute executor (instance name, or project/instance)")
	}
	if execConfig.Region == "" {
		return "", fmt.Errorf("zone is required for gcp_compute executor (e.g. us-central1-a)")
	}

	// Parse optional "project/instance" format from TargetID
	instance := execConfig.TargetID
	var projectFlag string
	if idx := strings.IndexByte(execConfig.TargetID, '/'); idx != -1 {
		projectFlag = execConfig.TargetID[:idx]
		instance = execConfig.TargetID[idx+1:]
	}

	requestContext := taskCtx.GetNewRequestContextForAccount(execConfig.AccountID)

	var remoteCommand string
	var err error

	if strings.ToLower(execConfig.OSType) == "windows" {
		var scriptLine string
		scriptLine, err = BuildWindowsScriptWrapper(execConfig)
		if err == nil {
			encodedPS := EncodePowerShellCommand(scriptLine)
			remoteCommand = fmt.Sprintf("powershell -NonInteractive -EncodedCommand %s", encodedPS)
		}
	} else {
		remoteCommand, err = BuildLinuxScriptWrapper(execConfig)
	}

	if err != nil {
		return "", err
	}

	slog.Info("GCP compute SSH preparing command",
		"language", execConfig.Language,
		"instance", instance,
		"zone", execConfig.Region,
		"project", projectFlag,
	)

	// StrictHostKeyChecking=no prevents interactive prompts in automation.
	// IAP tunnel avoids the need for a public IP on the instance.
	cmd := fmt.Sprintf(
		"gcloud compute ssh %s --zone=%s --command=%s --tunnel-through-iap --quiet --ssh-flag=%s",
		shellQuote(instance),
		shellQuote(execConfig.Region),
		shellQuote(remoteCommand),
		"-oStrictHostKeyChecking=no",
	)
	if projectFlag != "" {
		cmd += " --project=" + shellQuote(projectFlag)
	}

	type result struct {
		output string
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := cloud.ExecuteCli(requestContext, cloud.CloudExecuteCliCommandRequest{
			AccountID: execConfig.AccountID,
			Command:   cmd,
		})
		resultCh <- result{resp, err}
	}()

	select {
	case <-taskCtx.GetContext().Done():
		return "", taskCtx.GetContext().Err()
	case r := <-resultCh:
		if r.err != nil {
			slog.Error("GCP compute SSH command failed", "instance", instance, "zone", execConfig.Region, "error", r.err)
			return "", fmt.Errorf("gcp compute ssh command failed: %w", r.err)
		}
		slog.Info("GCP compute SSH command completed", "instance", instance, "zone", execConfig.Region)
		return r.output, nil
	}
}
