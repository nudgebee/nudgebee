package tools

import (
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteGithubCliCommand = "github_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteGithubCliCommand, func(accountId string) (core.NBTool, error) {
		return GithubCliTool{AccountId: accountId}, nil
	})
}

type GithubCliTool struct {
	AccountId string
}

func (m GithubCliTool) Name() string {
	return ToolExecuteGithubCliCommand
}

func (m GithubCliTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m GithubCliTool) Description() string {
	return `Executes Github CLI ('gh') commands based on natural language queries. This tool allows you to interact with GitHub resources like repositories, issues, pull requests, actions, etc.

		**Usage:**

		* **Prioritize this tool:** Whenever you need to interact with GitHub resources or perform GitHub operations, use this tool.
		* **Input:** Provide a valid, 'gh' command as input. Include necessary flags like --repo owner/repo when needed.
		* **Output:** The tool will return the raw output of the executed 'gh' command.

		**Examples:**

		* 'gh issue list --repo nudgebee/llm-server --state open'
		* 'gh pr view 123 --repo nudgebee/llm-server --json title,body'
		* 'gh workflow list --repo owner/repo'
		* 'gh api repos/owner/repo/actions/runs --jq .workflow_runs[0].id'

		**Important Notes:**

		* Ensure the 'gh' command is correctly formatted with appropriate subcommands and flags.
		* Specify the repository using '--repo owner/repo' when the context isn't clear.
		* Use the output of this tool to inform your responses and suggestions to the user.
		* Consider using '--json' and '--template' flags with 'gh' or piping to 'jq' for structured data when appropriate.
		`
}

func (m GithubCliTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Github CLI command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m GithubCliTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("github: executing executeShellCommand tool call", "query", input.Command)

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	command := strings.TrimSpace(input.Command)

	// Extract auth_type, url, and password from tool config
	authType := ""
	apiUrl := ""
	password := ""
	for _, v := range nbRequestContext.ToolConfig.Values {
		if v.Name == "auth_type" {
			authType = v.Value
		}
		if v.Name == "url" {
			apiUrl = v.Value
		}
		if v.Name == "password" {
			password = v.Value
		}
	}

	// For GitHub App authentication, get installation token
	githubToken := password
	if authType == "application" {
		installationID := int64(0)
		if _, err := fmt.Sscanf(password, "%d", &installationID); err != nil {
			return core.NBToolResponse{}, fmt.Errorf("invalid installation_id in password field: %w", err)
		}

		token, err := utils.GetGithubAppInstallationToken(nbRequestContext.Ctx.GetContext(), apiUrl, installationID)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("github: unable to get installation token", "error", err.Error())
			return core.NBToolResponse{}, fmt.Errorf("failed to get GitHub App installation token: %w", err)
		}
		githubToken = token
	}

	command = strings.ReplaceAll(command, "\\n", "\n")

	var response string
	var err error

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()

		// Prepare env with GITHUB_TOKEN
		env := map[string]string{
			"GITHUB_TOKEN": githubToken,
		}
		env[workspace.ENV_NB_TOOL_CONFIG_NAME] = nbRequestContext.ToolConfig.Name

		// Use the encapsulated lazy creation/execution logic
		// Use nbRequestContext.AccountId as it is guaranteed to be populated from the session
		response, err = wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)
	} else {
		// Enforce 'gh ' prefix for local execution for security
		if !strings.HasPrefix(command, "gh ") {
			command = "gh " + command
		}
		// execute gh cli command locally
		response, err = ExecuteCliCommand(nbRequestContext, command, []string{"GITHUB_TOKEN=" + githubToken}, []string{"gh", "grep", "awk", "jq"})
	}

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("github: unable to execute shell script", "error", err.Error(), "command", command)
		if response == "" {
			response = err.Error()
		}
		return core.NBToolResponse{
			Data:   response,
			Status: core.NBToolResponseStatusError,
		}, err
	}

	isLogView := strings.Contains(command, "run view") && (strings.Contains(command, "--log") || strings.Contains(command, "--log-failed"))
	if isLogView {
		processedLines := GetErrorLinesFromLogStringOrDefault(response, true)
		response = strings.Join(processedLines, "\n")
	}

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (m GithubCliTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"id", "name", "username", "url", "password", "auth_type", "projects"},
		ConfigType:   "github",
		ConfigSource: core.ToolConfigSourceTicket,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}

func (m GithubCliTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a Github security expert. Your task is to classify a 'gh' CLI command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: gh issue list
	answer: read

	input: gh pr view 123
	answer: read

	input: gh repo view my-repo
	answer: read

	input: gh gist list
	answer: read

	input: gh issue create --title "New bug" --body "I found a bug"
	answer: create

	input: gh repo create my-new-repo --public
	answer: create

	input: gh gist create my-file.txt
	answer: create

	input: gh pr edit 123 --add-label "bug"
	answer: update

	input: gh issue comment 123 --body "This is a comment"
	answer: update

	input: gh pr merge 123 --squash
	answer: update

	input: gh secret set MY_SECRET --body "my-secret-value"
	answer: update

	input: gh workflow run my-workflow.yml
	answer: update

	input: gh issue delete 123
	answer: delete

	input: gh repo delete my-repo --yes
	answer: delete

	input: gh gist delete 123456
	answer: delete
	`
	return prompt, nil
}
