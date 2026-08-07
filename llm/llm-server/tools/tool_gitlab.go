package tools

import (
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
)

const ToolExecuteGitlabCliCommand = "gitlab_execute"

func init() {
	core.RegisterNBToolFactory(ToolExecuteGitlabCliCommand, func(accountId string) (core.NBTool, error) {
		return GitlabCliTool{AccountId: accountId}, nil
	})
}

type GitlabCliTool struct {
	AccountId string
}

func (m GitlabCliTool) Name() string {
	return ToolExecuteGitlabCliCommand
}

func (m GitlabCliTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m GitlabCliTool) Description() string {
	return `Executes GitLab CLI ('glab') commands based on natural language queries. This tool allows you to interact with GitLab resources like projects, issues, merge requests, CI/CD pipelines and jobs, releases, etc.

		**Usage:**

		* **Prioritize this tool:** Whenever you need to interact with GitLab resources or perform GitLab operations, use this tool.
		* **Input:** Provide a valid 'glab' command as input. Include '--repo GROUP/PROJECT' when the project isn't clear from context.
		* **Output:** The tool will return the raw output of the executed 'glab' command.

		**Examples:**

		* 'glab mr list --repo my-group/my-project --per-page 20'
		* 'glab issue view 42 --repo my-group/my-project --output json'
		* 'glab ci list --repo my-group/my-project --status failed --per-page 5'
		* 'glab api "projects/my-group%2Fmy-project/pipelines?status=failed"'

		**Important Notes:**

		* Project paths are 'GROUP/PROJECT' (subgroups included, e.g. 'group/subgroup/project') or a numeric project ID.
		* Use '--output json' together with '--jq' for structured data; 'glab api' is the escape hatch for anything the porcelain commands don't cover.
		* Flag names differ from the GitHub CLI: '--per-page'/'-P' (not '--limit'), '-R'/'--repo' for project selection.
		`
}

func (m GitlabCliTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "GitLab CLI command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m GitlabCliTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {

	nbRequestContext.Ctx.GetLogger().Info("gitlab: executing glab tool call", "query", input.Command)

	if nbRequestContext.ToolConfig.Name == "" {
		return core.NBToolResponse{}, fmt.Errorf("no tool configs found for - %s, please configure", m.Name())
	}

	command := strings.TrimSpace(input.Command)

	// GITLAB_TOKEN (+ GITLAB_HOST for self-hosted) come from the tenant's gitlab
	// integration. Shared with the shell tool's injection path so both agree on
	// how a config becomes glab credentials.
	auth, err := BuildGitlabAuth(nbRequestContext.ToolConfig)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("gitlab: unable to build auth", "error", err.Error())
		return core.NBToolResponse{}, fmt.Errorf("failed to resolve GitLab credentials: %w", err)
	}

	command = strings.ReplaceAll(command, "\\n", "\n")

	var response string

	if config.Config.LlmServerWorkspaceEnabled {
		wm := workspace.NewWorkspaceManager()

		env := map[string]string{}
		for k, v := range auth.Env {
			env[k] = v
		}
		env[workspace.ENV_NB_TOOL_CONFIG_NAME] = nbRequestContext.ToolConfig.Name

		// Use the encapsulated lazy creation/execution logic
		// Use nbRequestContext.AccountId as it is guaranteed to be populated from the session
		response, err = wm.ExecuteOrLazyCreate(nbRequestContext.Ctx, nbRequestContext.AccountId, nbRequestContext.ConversationId, command, env)
	} else {
		// Enforce 'glab ' prefix for local execution for security
		if !strings.HasPrefix(command, "glab ") {
			command = "glab " + command
		}
		env := make([]string, 0, len(auth.Env))
		for k, v := range auth.Env {
			env = append(env, k+"="+v)
		}
		// execute glab cli command locally
		response, err = ExecuteCliCommand(nbRequestContext, command, env, []string{"glab", "grep", "awk", "jq"})
	}

	// In workspace mode the command reaches a real shell unfiltered, so a stray
	// `printenv` (or `glab config get token`) would otherwise echo the PAT back
	// into the model's context. Scrub before anything is returned, on both the
	// success and error paths.
	response = ScrubCredentials(response, auth.Env)

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("gitlab: unable to execute shell script", "error", err.Error(), "command", command)
		if response == "" {
			response = ScrubCredentials(err.Error(), auth.Env)
		}
		return core.NBToolResponse{
			Data:   cliRecoveryEnvelope(response, gitlabErrorHint(response), "glab", "glab <subcommand> --help"),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// `glab ci trace` streams a full job log; keep only the error lines plus
	// context, matching how the github tool trims `gh run view --log`.
	if strings.Contains(command, "ci trace") {
		processedLines := GetErrorLinesFromLogStringOrDefault(response, true)
		response = strings.Join(processedLines, "\n")
	}

	return core.NBToolResponse{
		Data:   response,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// gitlabErrorHint returns an actionable recovery hint for the three glab failure
// classes the model most often thrashes on. All three are cases where the model
// tends to abandon a nearly-correct command and switch to a different one
// instead of fixing the flag or the project path. Returns "" for unrecognized
// errors so the raw glab output is preserved unchanged (matching
// githubErrorHint's contract).
func gitlabErrorHint(rawError string) string {
	lower := strings.ToLower(rawError)
	switch {
	case strings.Contains(lower, "unknown flag"), strings.Contains(lower, "unknown shorthand flag"):
		return "That flag is not valid for this glab subcommand. glab's flags are NOT the GitHub CLI's: use `--per-page`/`-P` (there is no `--limit`), `-R`/`--repo GROUP/PROJECT` to select a project, and `--output json` with `--jq '<expr>'` for structured output. Note `-F` means `--output` on `glab mr/ci/repo list` but `--output-format` on `glab issue list`, so prefer the long `--output json` form everywhere. Run `glab <subcommand> --help` and correct THIS command rather than switching to a different one."
	case strings.Contains(lower, "404"), strings.Contains(lower, "not found"):
		return "GitLab returned 404 — the project path is wrong, or the token cannot see it. Project paths are full namespace paths (`group/project`, or `group/subgroup/project` for subgroups) or a numeric project ID — a bare project name will not resolve. Run `glab repo list --per-page 100` to list the projects this token can actually reach, then retry with an exact path. In `glab api` URLs the path must be URL-encoded (`group%2Fproject`). NOTE: on a `repository/files/...` or `repository/tree` call the 404 usually means the FILE or REF does not exist, not the project — before re-running project discovery, list the directory with `glab api \"projects/<encoded-path>/repository/tree?ref=<branch>&per_page=100\"` to see what is actually there. If the failing call was NOT project-scoped (e.g. `glab api users`, or an instance-wide list), the 404 means the endpoint needs admin rights or does not exist at all — on gitlab.com listing all users is admin-only. Do NOT re-run project discovery for those; to find one user use `glab api \"users?username=<exact-username>\"`."
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "403"), strings.Contains(lower, "forbidden"):
		return "GitLab rejected the credentials (401/403). The personal access token is missing, expired, or lacks the required scope — read-only queries need `read_api`, and anything that writes needs the full `api` scope. This is a configuration problem, not a command problem: report it rather than retrying variations of the same call."
	}
	return ""
}

func (m GitlabCliTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"id", "name", "username", "url", "password", "projects"},
		ConfigType:   "gitlab",
		ConfigSource: core.ToolConfigSourceTicket,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}

func (m GitlabCliTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt := `You are a GitLab security expert. Your task is to classify a 'glab' CLI command.

	Based on the provided command, you must categorize its intent into exactly one of the following types:
	* create
	* update
	* delete
	* read

	Your answer must be a single word without any explanations and internal thoughts added. If you cannot definitively classify the command's intent, answer 'unknown'.

	Examples:

	input: glab issue list
	answer: read

	input: glab mr view 123
	answer: read

	input: glab repo view my-group/my-project
	answer: read

	input: glab ci list --status failed
	answer: read

	input: glab ci trace 456
	answer: read

	input: glab api "projects/1/repository/files/main.go/raw?ref=main"
	answer: read

	input: glab issue create --title "New bug" --description "I found a bug"
	answer: create

	input: glab mr create --source-branch fix --target-branch main --title "Fix"
	answer: create

	input: glab repo create my-new-project --public
	answer: create

	input: glab release create v1.0.0
	answer: create

	input: glab mr update 123 --label bug
	answer: update

	input: glab issue note 123 --message "This is a comment"
	answer: update

	input: glab mr merge 123
	answer: update

	input: glab variable set MY_VAR --value "my-value"
	answer: update

	input: glab ci run --branch main
	answer: update

	input: glab ci retry 456
	answer: update

	input: glab issue delete 123
	answer: delete

	input: glab repo delete my-group/my-project --yes
	answer: delete

	input: glab ci delete 456
	answer: delete
	`
	return prompt, nil
}
