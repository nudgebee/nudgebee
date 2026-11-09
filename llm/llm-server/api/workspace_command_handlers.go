package api

import (
	"fmt"
	"nudgebee/llm/cloud"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"nudgebee/llm/tools/core"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
)

type workspaceCommandPlan struct {
	Request         WorkspaceExecuteRequest
	TargetAccountID string
	VirtualResult   any
	Virtual         bool
}

type workspaceCommandHandler interface {
	RegisteredToolName() string
	Prepare(ctx *security.RequestContext, toolCtx core.NbToolContext, req WorkspaceExecuteRequest) (workspaceCommandPlan, error)
	Execute(ctx *security.RequestContext, toolCtx core.NbToolContext, plan workspaceCommandPlan) (any, error)
}

var workspaceCommandHandlers = map[string]workspaceCommandHandler{
	"kubectl": kubernetesWorkspaceCommandHandler{},
	"aws":     awsWorkspaceCommandHandler{},
	"gcloud":  cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "gcloud"},
	"gsutil":  cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "gsutil"},
	"bq":      cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteGcpCliCommand, executable: "bq"},
	"az":      cloudWorkspaceCommandHandler{toolName: tools.ToolExecuteAzureCliCommand, executable: "az"},
}

type kubernetesWorkspaceCommandHandler struct{}

func (kubernetesWorkspaceCommandHandler) RegisteredToolName() string {
	return tools.ToolExecuteKubectlCommand
}

func (kubernetesWorkspaceCommandHandler) Prepare(ctx *security.RequestContext, toolCtx core.NbToolContext, req WorkspaceExecuteRequest) (workspaceCommandPlan, error) {
	if err := normalizeKubectlProxyRequest(&req); err != nil {
		return workspaceCommandPlan{}, err
	}
	if result, handled, err := virtualKubectlConfig(ctx, req, toolCtx.ToolConfig.Name); handled {
		if err != nil {
			return workspaceCommandPlan{}, err
		}
		return workspaceCommandPlan{Request: req, TargetAccountID: req.AccountId, VirtualResult: result, Virtual: true}, nil
	}
	requestedTarget := req.TargetName
	if requestedTarget == "" {
		requestedTarget = req.ConfigName
	}
	targetAccountID, err := workspaceRelayTarget(toolCtx, tools.RelayJobKubectl, requestedTarget, req.Command)
	if err != nil {
		return workspaceCommandPlan{}, err
	}
	return workspaceCommandPlan{Request: req, TargetAccountID: targetAccountID}, nil
}

func (kubernetesWorkspaceCommandHandler) Execute(_ *security.RequestContext, toolCtx core.NbToolContext, plan workspaceCommandPlan) (any, error) {
	return tools.ExecuteContainerJob(toolCtx, tools.RelayJobKubectl, plan.Request.Command, plan.TargetAccountID, plan.Request.Arguments, true)
}

var executeCloudCliV2 = cloud.ExecuteV2

type cloudWorkspaceCommandHandler struct {
	toolName   string
	executable string
}

func (h cloudWorkspaceCommandHandler) RegisteredToolName() string { return h.toolName }

func (h cloudWorkspaceCommandHandler) Prepare(ctx *security.RequestContext, toolCtx core.NbToolContext, req WorkspaceExecuteRequest) (workspaceCommandPlan, error) {
	args, err := commandArgs(h.executable, req.Command)
	if err != nil {
		return workspaceCommandPlan{}, err
	}
	req.Command = buildProxyCommand(h.executable, args)
	requested := req.TargetName
	if requested == "" {
		requested = req.ConfigName
	}
	target, err := resolveCloudWorkspaceTarget(ctx, req.AccountId, h.toolName, toolCtx.ToolConfig, requested)
	if err != nil {
		return workspaceCommandPlan{}, err
	}
	targetAccountID := tools.TargetAccountID(target)
	if targetAccountID == "" {
		return workspaceCommandPlan{}, fmt.Errorf("selected cloud account has no account id")
	}
	return workspaceCommandPlan{Request: req, TargetAccountID: targetAccountID}, nil
}

func resolveCloudWorkspaceTarget(ctx *security.RequestContext, accountID, toolName string, current core.ToolConfig, requested string) (core.ToolConfig, error) {
	if requested != "" || tools.TargetAccountID(current) != "" {
		return tools.ResolveRequestedTarget(ctx, accountID, toolName, current, requested)
	}
	targets, err := tools.ListAccountTargets(ctx, accountID, toolName)
	if err != nil {
		return core.ToolConfig{}, err
	}
	for _, target := range targets {
		if tools.TargetAccountID(target) == accountID {
			return target, nil
		}
	}
	return core.ToolConfig{}, fmt.Errorf("cloud account is not selected; choose one of: %s", workspaceTargetNames(targets))
}

func (h cloudWorkspaceCommandHandler) Execute(ctx *security.RequestContext, _ core.NbToolContext, plan workspaceCommandPlan) (any, error) {
	return executeWorkspaceCloudCli(ctx, plan)
}

type awsWorkspaceCommandHandler struct{}

func (awsWorkspaceCommandHandler) RegisteredToolName() string { return tools.ToolExecuteAwsCliCommand }

func (awsWorkspaceCommandHandler) Prepare(ctx *security.RequestContext, _ core.NbToolContext, req WorkspaceExecuteRequest) (workspaceCommandPlan, error) {
	argv, err := awsCommandArgs(req.Command)
	if err != nil {
		return workspaceCommandPlan{}, err
	}
	stripped, profile, err := extractAwsProfileArgs(argv)
	if err != nil {
		return workspaceCommandPlan{}, err
	}
	if req.TargetName != "" {
		if profile != "" && !strings.EqualFold(profile, req.TargetName) {
			return workspaceCommandPlan{}, fmt.Errorf("aws command names more than one account")
		}
		profile = req.TargetName
	}
	req.TargetName = profile
	req.Command = buildProxyCommand("aws", stripped)

	if result, handled, err := virtualAwsConfig(ctx, req, stripped); handled {
		if err != nil {
			return workspaceCommandPlan{}, err
		}
		return workspaceCommandPlan{Request: req, TargetAccountID: req.AccountId, VirtualResult: result, Virtual: true}, nil
	}
	target, err := resolveAwsWorkspaceTarget(ctx, req, profile)
	if err != nil {
		return workspaceCommandPlan{}, err
	}
	targetAccountID := tools.TargetAccountID(target)
	if targetAccountID == "" {
		return workspaceCommandPlan{}, fmt.Errorf("selected AWS account has no account id")
	}
	return workspaceCommandPlan{Request: req, TargetAccountID: targetAccountID}, nil
}

func (awsWorkspaceCommandHandler) Execute(ctx *security.RequestContext, _ core.NbToolContext, plan workspaceCommandPlan) (any, error) {
	return executeWorkspaceCloudCli(ctx, plan)
}

func executeWorkspaceCloudCli(ctx *security.RequestContext, plan workspaceCommandPlan) (any, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil {
		return nil, fmt.Errorf("cloud account access denied")
	}
	result, err := executeCloudCliV2(cloud.CloudExecuteCliCommandRequest{
		AccountID: plan.TargetAccountID,
		TenantID:  sc.GetTenantId(),
		UserID:    sc.GetUserId(),
		Command:   plan.Request.Command,
	})
	if err != nil {
		return nil, err
	}
	response := map[string]any{
		"stdout":           result.Stdout,
		"stderr":           result.Stderr,
		"exit_code":        result.ExitCode,
		"timed_out":        result.TimedOut,
		"output_truncated": result.OutputTruncated,
	}
	if result.ExitCode != 0 {
		return response, &tools.RelayCommandExitError{ExitCode: result.ExitCode}
	}
	return response, nil
}

func awsCommandArgs(command string) ([]string, error) {
	return commandArgs("aws", command)
}

func commandArgs(toolName, command string) ([]string, error) {
	parts, err := shlex.Split(strings.TrimSpace(command))
	if err != nil {
		return nil, fmt.Errorf("unable to parse %s command: %w", toolName, err)
	}
	if len(parts) == 0 || filepath.Base(parts[0]) != toolName {
		return nil, fmt.Errorf("%s command must start with %s", toolName, toolName)
	}
	return parts[1:], nil
}

func extractAwsProfileArgs(argv []string) ([]string, string, error) {
	stripped := make([]string, 0, len(argv))
	profile := ""
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		var value string
		switch {
		case arg == "--profile":
			if i+1 >= len(argv) || argv[i+1] == "" {
				return nil, "", fmt.Errorf("aws --profile requires a value")
			}
			value = argv[i+1]
			i++
		case strings.HasPrefix(arg, "--profile="):
			value = strings.TrimPrefix(arg, "--profile=")
			if value == "" {
				return nil, "", fmt.Errorf("aws --profile requires a value")
			}
		default:
			stripped = append(stripped, arg)
			continue
		}
		if profile != "" && !strings.EqualFold(profile, value) {
			return nil, "", fmt.Errorf("aws command names more than one profile")
		}
		profile = value
	}
	return stripped, profile, nil
}

func virtualAwsConfig(ctx *security.RequestContext, req WorkspaceExecuteRequest, argv []string) (string, bool, error) {
	if len(argv) < 2 || argv[0] != "configure" {
		return "", false, nil
	}
	switch argv[1] {
	case "list-profiles":
		targets, err := tools.ListAccountTargets(ctx, req.AccountId, tools.ToolExecuteAwsCliCommand)
		if err != nil {
			return "", true, fmt.Errorf("unable to list AWS profiles: %w", err)
		}
		names := make([]string, 0, len(targets))
		for _, target := range targets {
			names = append(names, target.Name)
		}
		return strings.Join(names, "\n") + trailingNewline(names), true, nil
	case "get":
		if len(argv) != 3 || argv[2] != "region" {
			return "", true, fmt.Errorf("only 'aws configure get region' is available in the virtual AWS configuration")
		}
		target, err := resolveAwsWorkspaceTarget(ctx, req, req.TargetName)
		if err != nil {
			return "", true, err
		}
		for _, value := range target.Values {
			if value.Name == "region" && value.Value != "" {
				return value.Value + "\n", true, nil
			}
		}
		return "", true, nil
	default:
		return "", true, fmt.Errorf("aws configure %s is unavailable: workspace AWS configuration is virtual and read-only", argv[1])
	}
}

func resolveAwsWorkspaceTarget(ctx *security.RequestContext, req WorkspaceExecuteRequest, requested string) (core.ToolConfig, error) {
	targets, err := tools.ListAccountTargets(ctx, req.AccountId, tools.ToolExecuteAwsCliCommand)
	if err != nil {
		return core.ToolConfig{}, err
	}
	if requested != "" {
		return tools.AccountTargetByName(ctx, req.AccountId, tools.ToolExecuteAwsCliCommand, requested)
	}
	// The shim forwards the account aws_execute chose (and bound the token to)
	// as ConfigName, so it must win over the conversation's own account even
	// when the own account sorts first.
	if req.ConfigName != "" {
		for _, target := range targets {
			if strings.EqualFold(target.Name, req.ConfigName) {
				return target, nil
			}
		}
	}
	for _, target := range targets {
		if tools.TargetAccountID(target) == req.AccountId {
			return target, nil
		}
	}
	return core.ToolConfig{}, fmt.Errorf("AWS account is not selected; pass --profile with one of: %s", workspaceTargetNames(targets))
}

func workspaceTargetNames(targets []core.ToolConfig) string {
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Name)
	}
	return strings.Join(names, ", ")
}
