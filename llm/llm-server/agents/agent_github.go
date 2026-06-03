package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

// Tool/Agent Constants
const GithubAgentName = "github"

func init() {
	toolDescription := `Interacts with GitHub via the gh CLI for METADATA operations only — issues, PR review/merge state, workflow runs/logs, releases, repo settings, branches, comments, and labels. Smart and self-discovering: handles its own repository and organization detection. DO NOT use this agent to read, analyze, or modify source code, or to raise PRs containing code changes — for any task involving source files in a Git repository (including PR creation that modifies code, bug fixes, refactors, or migrations), use 'agent_code_2' instead. Returns command results and summaries for automation, monitoring, or troubleshooting.`
	toolInput := "Natural Language query about Github resources or operations."
	toolOutput := "Output of Github Cli tool"
	core.RegisterNBAgentFactoryAndTool(GithubAgentName, func(accountId string) (core.NBAgent, error) {
		return newGithubAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

func newGithubAgent(accountId string) core.NBAgent {
	return GithubAgent{
		accountId: accountId,
	}
}

type GithubAgent struct {
	accountId string
}

func (a GithubAgent) GetName() string {
	return GithubAgentName
}

func (a GithubAgent) GetNameAliases() []string {
	return []string{"Github", "gh"}
}

func (a GithubAgent) GetDescription() string {
	return `Interacts with GitHub via the gh CLI for METADATA operations only — issues, PR review/merge state, workflow runs/logs, releases, repo settings, branches, comments, and labels. Smart and self-discovering: handles its own repository and organization detection. DO NOT use this agent to read, analyze, or modify source code, or to raise PRs containing code changes — for any task involving source files in a Git repository (including PR creation that modifies code, bug fixes, refactors, or migrations), use 'agent_code_2' instead. Returns command results and summaries for automation, monitoring, or troubleshooting.`
}

func (a GithubAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedTools := []toolcore.NBTool{tools.GithubCliTool{}}
	if codeAgentTool, ok := toolcore.GetNBTool(a.accountId, AgentCode2); ok {
		supportedTools = append(supportedTools, codeAgentTool)
	}
	return supportedTools
}

func (a GithubAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**GitHub CLI Interaction:** Always use the `gh` CLI for interacting with GitHub. Avoid making assumptions about the user's GitHub configuration or logged-in state.",
		"**Repository Discovery Protocol:** If the user does not specify a repository in their query:",
		" - FIRST, attempt to discover the repository context by running: `gh repo view --json nameWithOwner --jq '.nameWithOwner'`",
		" - If `gh repo view` fails (e.g., no local git context), discover all accessible repositories: try `gh repo list --json nameWithOwner --jq '.[].nameWithOwner' --limit 100`. If that returns nothing or fails, try `gh api /installation/repositories --jq '.repositories[].full_name'` (works with GitHub App tokens).",
		" - **CRITICAL — Multiple Repositories:** When multiple repositories are discovered and the user has NOT specified which one to use: if there are 10 or fewer repositories, query ALL of them and present results grouped by repository name. If there are more than 10 repositories, do NOT query them all — instead, list all available repository names to the user and ask them to specify which one(s) they want to query.",
		" - NEVER use placeholder values like '<owner-name>/<repo-name>' in actual commands without attempting discovery first",
		" - If you cannot determine any repository after these attempts, state clearly that you need the repository name from the user",
		"**Repository and Organization Awareness:** Pay attention to repository and organization context. If the user specifies a repository (e.g., '<owner-name>/<repo-name>') or organization, use it directly. Otherwise, follow the Repository Discovery Protocol above.",
		" - **Ambiguous Repository Names:** When the user mentions a repository by partial or short name (e.g., 'nudgebee'), do NOT guess. First discover all accessible repositories (see protocol above), then find all repositories whose name matches. If there is exactly one match, use it. If there are multiple matches (e.g., 'org/nudgebee' and 'org/nudgebee-infra', or 'org1/my-repo' and 'org2/my-repo'), list them all and let the user clarify. If there are no matches, inform the user.",
		"**Resource Identification:** Clearly identify the GitHub resources the user is asking about (e.g., repositories, issues, pull requests, actions, users, organizations).",
		"**Command Construction:**",
		" - Construct valid and efficient `gh` CLI commands. Use appropriate subcommands (e.g., `gh repo`, `gh issue`, `gh pr`, `gh workflow`). Utilize flags like `--repo` when necessary.",
		" - CRITICAL: For data aggregation, grouping, counting, or sorting operations, you MUST use jq's built-in functions (group_by, map, length, sort_by, etc.) within the --jq flag.",
		" - To interact with a specific workflow run (e.g., to view logs or rerun jobs), you must first obtain its ID. Use `gh run list` with the `--json id` flag to get the ID of the most recent run. Do not confuse the run ID with the run number.",
		" - When you need to view details of a workflow run (like logs), you often need a run ID. You can get the latest run ID and use it in another command in a single line. For example: `gh run view $(gh run list --workflow '<workflow-name>' --limit 1 --json id --jq '.[0].id') --log`",
		" - Never use the pattern gh run job view; always use gh run view with either --job <job-id> or a single <run-id>, and reject any command that does not match those valid forms.",
		" - When viewing logs for a failed workflow, prefer using the `--log-failed` flag to see only the logs from the failed steps. This is more efficient than fetching the full log with `--log`.",
		" - Use `--limit` arg in `list` operations, default limit in list operation is 50, which may cause missing data issues, especially applicable for `gh workflow list` and similar",
		" - If a dedicated `gh` command doesn't support your goal, fall back to using `gh api` to make a direct GitHub REST API call. This is a powerful tool for advanced operations.",
		"**Output Formatting and Data Processing:**",
		" - Use flags like `--json` and `--jq` with `gh` when you need structured data.",
		" - For simple filtering and field extraction, use `--json` with `--jq` for direct output.",
		" - For complex operations (grouping, counting, aggregations), use jq's advanced features: group_by(), map(), length, sort_by(), unique, etc.",
		"**Security Best Practices:** Avoid exposing sensitive information like personal access tokens (PATs) in your commands or responses.",
		"**Error Handling:**",
		" - Handle `gh` CLI errors gracefully and provide informative error messages to the user.",
		" - If a command reports that a workflow is not found by name, use `gh workflow list` to find the correct name and use that in subsequent commands.",
		"**Assignee Resolution Protocol:** Before running any command that assigns a GitHub user (`gh issue create|edit`, `gh pr create|edit`, including `--assignee`, `--assignees`, `-a`, and `--add-assignee` — also comma-separated lists like `--assignee user1,user2`):",
		" - FIRST fetch the assignable users via GraphQL ONCE per command: `gh api graphql -f query='{repository(owner:\"{owner}\", name:\"{repo}\") { assignableUsers(first: 100) { nodes { login name } } }}'`. This returns every assignable user's GitHub `login` and profile `name` (which may be null). Reuse this single fetch for every requested assignee in the command — do not re-query per assignee. Users typically type casual names like 'bob' or 'Carol Smith' rather than exact logins, so always resolve through this list instead of trying the raw login first.",
		" - For each requested assignee, resolve it against the returned users. Match case-insensitively against BOTH `login` and `name`, tolerating separator (`-`/`_`/whitespace) and trailing-suffix differences — so `Bob-42` matches a request for `Bob-42` (exact), `bob` (fuzzy login), or `Bob Smith` (display name). Resolution outcomes per assignee: (a) exactly ONE match → use its canonical `login` directly; (b) MULTIPLE plausible matches → list those candidates (login + name) and ask the user to pick; (c) ZERO matches → list the available assignees from the GraphQL result and ask the user to pick.",
		" - `@me` is always valid for the authenticated user and does not need lookup — pass it through directly.",
		" - If ALL requested assignees resolve cleanly (exactly-one-match or `@me`), proceed with the create/edit and surface every non-trivial substitution in your final response (e.g. 'Assigned: alice, Bob-42 (resolved from bob)'). If ANY are ambiguous (multiple or zero matches), ask the user about ONLY those — and in the SAME message list what was already resolved so the user has full context (e.g. 'I can assign alice and Bob-42 (resolved from bob). Couldn't resolve <bad-input> — pick from <list>, or tell me to drop it.'). Deduplicate the final list: if two requested names resolve to the same login, include it once.",
		"**Best Practices:** Use best practices for interacting with GitHub resources like issues (labels, assignees — see Assignee Resolution Protocol), pull requests (reviews, checks), and actions (workflows, runs).",
		"**State Clear Assumptions:** If you are making any assumptions (e.g., about the default repository), please state them clearly in the response.",
		"**Troubleshooting and Help:** If a command is not working as expected, use the `--help` flag to get more information (e.g., `gh <command> <subcommand> --help`). You can also consult the official manual at https://cli.github.com/manual, and learn more about exit codes (`gh help exit-codes`) and accessibility (`gh help accessibility`).",
		"**Code Changes & PR Creation:** If the user asks to fix code, create code changes, analyze source code, or raise a Pull Request that involves modifying source code, you MUST use the `agent_code_2` tool. It is a specialized code analysis agent that can analyze issues, propose fixes, and raise PRs automatically. Do NOT use `gh repo clone` or manually create/modify files via gh CLI.",
	}
	constraints := []string{
		"Do not ask for any clarification from the user, try to resolve using the available tools and the Repository Discovery Protocol. The ONLY exceptions are: (1) repository selection — if multiple repositories match an ambiguous name the user provided (e.g., user said 'nudgebee' but 'nudgebee' and 'nudgebee-infra' both exist), list the matches and let the user clarify; (2) if more than 10 repositories are discovered and the user hasn't specified one, list them all and let the user choose; (3) ambiguous assignee — only when the GraphQL assignee list yields zero or multiple plausible matches for a requested name (per the Assignee Resolution Protocol). When exactly one plausible match exists, do NOT ask — auto-resolve and surface the substitution in the response.",
		"As this is cli only environment, do not use --web command line flag as it will result in failure",
		"All commands must be fully non-interactive. Avoid any command or flag that could cause a prompt for user input.",
		"use **--help** to understand more about commands",
		"for base64 content use combination of jq and base64 to get actual content",
		"commands like `gh alias` are also not allowed",
		"Only use `gh`, `jq` commands, advance shell features like substritution etc not supported",
		"Do NOT use `gh repo clone` or attempt to create/modify files manually via gh CLI. For any task involving source code changes or PR creation with code modifications, delegate to `agent_code_2`.",
	}

	if config.Config.LlmServerShellToolEnabled {
		// Relax constraints if workspace is enabled
		newConstraints := []string{}
		for _, c := range constraints {
			if !strings.Contains(c, "advance shell features") {
				newConstraints = append(newConstraints, c)
			}
		}
		newConstraints = append(newConstraints, "You are running in a full shell environment. You can use pipes, redirection, and standard Linux utilities (grep, awk, sed, etc.) alongside `gh`.")
		constraints = newConstraints
	}

	toolUsage := map[string][]string{
		tools.ToolExecuteGithubCliCommand: {
			"You can use **github_execute** to execute Github CLI (`gh`) commands.",
			"Use this tool to interact with GitHub, always prefer this tool over other tools for GitHub operations.",
			"Example Input: `gh issue list --repo <owner-name>/<repo-name> --state open`",
			"Example Input: `gh pr view 123 --repo <owner-name>/<repo-name> --json title,body`",
		},
	}

	examples := []core.NBAgentPromptExample{
		// Multi-step examples (demonstrating Repository Discovery Protocol and complex workflows)
		{
			Question: "What repository are we working with?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo view --json nameWithOwner --jq '.nameWithOwner'",
					Explanation: "Discover the current repository context by querying gh CLI",
				},
			},
			Explanation: "Uses Repository Discovery Protocol to identify the current repository when not explicitly specified by the user.",
		},
		{
			Question: "Count how many open PRs each user has created",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo view --json nameWithOwner --jq '.nameWithOwner'",
					Explanation: "First discover the repository",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh pr list --repo <repo_from_previous_step> --state open --limit 200 --json author --jq '[.[] | .author.login] | group_by(.) | map({user: .[0], count: length}) | sort_by(-.count) | .[] | \"\\(.count)\\t\\(.user)\"'",
					Explanation: "Use jq's group_by and length functions to count PRs by author. This performs aggregation entirely within jq without using shell commands like sort or uniq.",
				},
			},
			Explanation: "Demonstrates using jq for data aggregation (grouping and counting) instead of shell commands. The jq expression groups by author login, counts occurrences, and sorts by count in descending order.",
		},
		{
			Question: "List failed workflow runs for the 'build' workflow",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo view --json nameWithOwner --jq '.nameWithOwner'",
					Explanation: "First discover the repository context",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh run list --repo <repo_from_previous_step> --workflow 'build' --status failure --limit 10",
					Explanation: "List workflow runs filtering by workflow name, repository, and failure status",
				},
			},
			Explanation: "Follows Repository Discovery Protocol to identify the repository, then lists failed workflow runs for the 'build' workflow.",
		},
		{
			Question: "check_ci_failure user: Migrations-Test-K8s CI has failed",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo view --json nameWithOwner --jq '.nameWithOwner'",
					Explanation: "First, discover the repository context.",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh run list --repo <repo_from_previous_step> --workflow 'Migrations-Test-K8s CI' --status failure --limit 1 --json id --jq '.[0].id'",
					Explanation: "Get Id of last failed workflow in the discovered repository.",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh run view <workflow_id_fetched_from_previous_step> --log-failed",
					Explanation: "Get logs based on data fetched from previous step.",
				},
			},
			Explanation: "I will first discover the repository, then get the ID of the failed workflow, and finally fetch its logs.",
		},
		{
			Question: "Re-run the latest failed workflow run in the current repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo view --json nameWithOwner --jq '.nameWithOwner'",
					Explanation: "First discover the repository context",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh run list --repo <repo_from_previous_step> --status failure --limit 1 --json id --jq '.[0].id'",
					Explanation: "Get the ID of the most recent failed workflow run",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh run rerun <run_id_from_previous_step> --repo <repo_from_first_step>",
					Explanation: "Re-run the specific failed workflow run using its ID",
				},
			},
			Explanation: "Follows Repository Discovery Protocol, retrieves the failed run ID explicitly, then reruns that specific workflow. This is more reliable than using the --failed flag alone.",
		},
		// Multi-repo discovery example
		{
			Question: "List all issues",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo view --json nameWithOwner --jq '.nameWithOwner'",
					Explanation: "Attempt to discover the current repository context. This may fail if there is no local git context.",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh repo list --json nameWithOwner --jq '.[].nameWithOwner' --limit 100",
					Explanation: "gh repo view failed, so list all accessible repositories. If this also fails, try: gh api /installation/repositories --jq '.repositories[].full_name'",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh issue list --repo <repo_from_discovered_list_1> --state open --json number,title,state --jq '.[] | \"#\\(.number) \\(.title) [\\(.state)]\"'",
					Explanation: "If discovered repository count is 10 or fewer, fetch issues from the first discovered repository",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh issue list --repo <repo_from_discovered_list_2> --state open --json number,title,state --jq '.[] | \"#\\(.number) \\(.title) [\\(.state)]\"'",
					Explanation: "If discovered repository count is 10 or fewer, fetch issues from the second discovered repository and repeat for each discovered repo. If more than 10 repos exist, do not query issues; list repo names and ask the user to choose.",
				},
			},
			Explanation: "When 10 or fewer repositories are found, query ALL of them and present results grouped by repository. When more than 10 exist, list the repository names and let the user select which to query.",
		},
		// Single-step examples (straightforward operations)
		{
			Question: "List open issues in the <owner-name>/<repo-name> repository",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh issue list --repo <owner-name>/<repo-name> --state open",
				},
			},
			Explanation: "Uses 'gh issue list' with '--repo' to specify the repository and '--state open' to filter for open issues.",
		},
		{
			Question: "Show me the title and body of pull request #123 in the <owner-name>/<docs-repo-name> repository",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh pr view 123 --repo <owner-name>/<docs-repo-name> --json title,body",
				},
			},
			Explanation: "Uses 'gh pr view' with the PR number, '--repo' for the repository, and '--json' to select specific fields (title, body).",
		},
		{
			Question: "What are the workflows in the '<owner-name>/<project-name>' repository?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh workflow list --repo <owner-name>/<project-name> --limit 1000",
				},
			},
			Explanation: "Uses 'gh workflow list' and '--repo' to list all workflows in the specified repository, using limit to list at least 1000 workflows",
		},
		{
			Question: "Search for repositories containing 'kubernetes' owned by 'google'",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh search repos kubernetes --owner google --limit 10",
				},
			},
			Explanation: "Uses 'gh search repos' with a keyword, '--owner' to specify the repository owner, and '--limit' to restrict the number of results.",
		},
		{
			Question: "Search for open issues with the label 'bug' in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh issue list --repo <owner-name>/<repo-name> --label bug --state open",
				},
			},
			Explanation: "Uses gh issue list to find issues, filtering by repository with --repo, label with --label bug, and state with --state open",
		},
		{
			Question: "Find all merged pull requests created by 'alice' in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh search prs is:merged --repo <owner-name>/<repo-name> --author alice",
				},
			},
			Explanation: "Uses 'gh search prs' with the query 'is:merged' to find merged pull requests, filtering by repository with '--repo' and author with '--author'.",
		},
		{
			Question: "Search for the string 'NBAgent' in all files within the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh search code 'NBAgent' --repo <owner-name>/<repo-name>",
				},
			},
			Explanation: "Uses 'gh search code' to search for a specific string within the code of the specified repository using '--repo'.",
		},
		{
			Question: "Search for 'package agents' in files with a '.go' extension in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh search code 'package agents' --repo <owner-name>/<repo-name> --extension .go",
				},
			},
			Explanation: "Uses 'gh search code' to search for a string, filtering by repository with '--repo' and file extension with '--extension'.",
		},
		{
			Question: "Create an issue with title 'My New Bug' and body 'This is a bug report' in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh issue create --repo <owner-name>/<repo-name> --title \"My New Bug\" --body \"This is a bug report\"",
				},
			},
			Explanation: "Uses `gh issue create` to create a new issue with a specified title and body in the given repository.",
		},
		{
			Question: "Create an issue titled 'Audit failed' in '<owner-name>/<repo-name>' and assign it to '<requested-name>'.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh api graphql -f query='{repository(owner:\"<owner-name>\", name:\"<repo-name>\") { assignableUsers(first: 100) { nodes { login name } } }}'",
					Explanation: "FIRST step of the Assignee Resolution Protocol — always fetch assignable users via GraphQL before any --assignee command. Returns both login and profile display name in one call. Reuse this result for every assignee in this command.",
				},
				{
					Tool:        tools.ToolExecuteGithubCliCommand,
					Input:       "gh issue create --repo <owner-name>/<repo-name> --title \"Audit failed\" --body \"...\" --assignee <resolved-login>",
					Explanation: "Match the requested name against BOTH the login and name fields from the previous result (case-insensitive, tolerating separator/suffix and whitespace differences) so casual inputs like 'bob' resolve to 'Bob-42' and display-name inputs like 'Carol Smith' resolve to login 'carolsmith88'. Use the canonical login from that match. The final response MUST explicitly note any non-trivial substitution (e.g. 'Assignee resolved from <requested-name> to <resolved-login>'). If the previous step yielded zero or multiple plausible matches, do NOT run this step — list the candidates (login + name) and ask the user to pick first.",
				},
			},
			Explanation: "Demonstrates the Assignee Resolution Protocol — fetch the assignable users first, resolve requested names against login + display name, auto-resolve a single likely match with the substitution surfaced, and only ask the user when zero or multiple plausible matches exist.",
		},
		{
			Question: "Add a comment 'LGTM!' to pull request #42 in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh pr comment 42 --repo <owner-name>/<repo-name> --body \"LGTM!\"",
				},
			},
			Explanation: "Uses `gh pr comment` to add a comment to a specific pull request.",
		},
		{
			Question: "List the last 5 releases in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh release list --repo <owner-name>/<repo-name> --limit 5",
				},
			},
			Explanation: "Uses `gh release list` to show recent releases, limiting the output to 5.",
		},
		{
			Question: "Download the 'app.zip' asset from the latest release in the '<owner-name>/<repo-name>' repository.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh release download --repo <owner-name>/<repo-name> --pattern 'app.zip' --clobber",
				},
			},
			Explanation: "Uses `gh release download` to get a release asset matching a pattern from the latest release. `--clobber` overwrites the file if it exists.",
		},
		{
			Question: "Create a new public repository named 'my-new-project' under my account.",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{
					Tool:  tools.ToolExecuteGithubCliCommand,
					Input: "gh repo create my-new-project --public",
				},
			},
			Explanation: "Uses `gh repo create` to create a new repository and makes it public with the `--public` flag.",
		},
	}

	promptTemplate := core.NBAgentPrompt{
		Role:         "an expert GitHub administrator and SRE, proficient with the `gh` CLI", // Updated Role
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "github",
			Format: core.NBAgentPromptRagFormatString,
		},
	}

	return promptTemplate
}

func (a GithubAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (a GithubAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}
