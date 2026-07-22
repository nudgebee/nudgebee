package tools

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

type GHTool struct {
	workspaceDir         string
	githubToken          string
	restrictPROperations bool
}

func NewGHTool(workspaceDir string) *GHTool {
	return &GHTool{workspaceDir: workspaceDir, githubToken: ""}
}

// NewGHToolWithToken creates a GHTool with a GitHub token for authenticated API calls
func NewGHToolWithToken(workspaceDir string, githubToken string) *GHTool {
	return &GHTool{workspaceDir: workspaceDir, githubToken: githubToken}
}

// SetGitHubToken sets the GitHub token for authenticated API calls
func (t *GHTool) SetGitHubToken(token string) {
	t.githubToken = token
}

// SetRestrictPROperations blocks PR creation commands.
// Used for agent-facing instances so only the orchestrator creates PRs with proper formatting.
func (t *GHTool) SetRestrictPROperations(restrict bool) {
	t.restrictPROperations = restrict
}

func (t *GHTool) Name() string {
	return "gh"
}

func (t *GHTool) Description() string {
	return `Executes GitHub CLI (gh) commands. Usage: gh <command> <subcommand> [flags]

CORE COMMANDS:
  pr:           Manage pull requests
  issue:        Manage issues
  repo:         Manage repositories
  search:       Search for repositories, issues, and pull requests
  run:          View workflow runs
  release:      Manage releases

COMMON EXAMPLES:
  List PRs:        ["pr", "list"]
  List PRs (limit): ["pr", "list", "--limit", "5"]
  List all PRs:    ["pr", "list", "--state", "all", "--limit", "10"]
  View PR:         ["pr", "view", "123"]
  Create PR:       ["pr", "create", "--title", "My PR", "--body", "Description"]

  List issues:     ["issue", "list"]
  View issue:      ["issue", "view", "456"]

  Search repos:    ["search", "repos", "nudgebee"]
  Search PRs:      ["search", "prs", "bug", "--repo", "owner/repo"]

  View repo:       ["repo", "view"]
  List releases:   ["release", "list"]

IMPORTANT:
- Each argument/flag/value must be a separate array element
- Example: ["pr", "list", "--limit", "10"] NOT ["pr list --limit 10"]
- For flags with values: ["--limit", "5"] NOT ["--limit=5"]
- gh pr list does NOT support --sort flag (GitHub limitation)
- Use --state for filtering: "open" | "closed" | "merged" | "all"
- By default, gh pr list shows recent PRs (no sorting needed)`
}

func (t *GHTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Parameters for executing GitHub CLI commands",
		map[string]any{
			"args": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Command arguments as separate strings. Example: ['pr', 'list', '--limit', '5', '--state', 'all'] executes 'gh pr list --limit 5 --state all'",
			},
		},
		[]string{"args"},
	)
}

func (t *GHTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var args []string
	if argsInput, ok := input["args"].([]any); ok {
		for _, arg := range argsInput {
			if argStr, ok := arg.(string); ok {
				args = append(args, argStr)
			}
		}
	} else {
		return core.CreateErrorResponse(
			"invalid input: 'args' is not a list of strings",
			"Input parameter validation failed",
		)
	}

	if len(args) == 0 {
		return core.CreateErrorResponse(
			"no arguments provided to gh tool",
			"Missing required parameters",
		)
	}

	// Block PR creation for agent-facing instances — orchestrator handles PR creation
	if t.restrictPROperations && len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
		msg := "PR creation is handled by the orchestrator. Use submit_analysis to report your findings — " +
			"the orchestrator will create the PR with proper formatting, template compliance, and repository branding."
		return core.CreateSuccessResponse(msg, msg, map[string]any{"blocked": true, "reason": msg})
	}

	// Use repository helper for working directory injection and discovery
	repoHelper := NewRepositoryHelper()
	repoDir := repoHelper.GetWorkingDirectoryWithInjection(input, t.workspaceDir)

	// `gh pr edit` resolves the PR via a GraphQL query that includes Projects
	// (classic). On orgs where classic projects are disabled this query returns
	// a fatal "Projects (classic) is being deprecated" error and gh exits 1 —
	// even when the title/body edit itself is valid. For title/body-only edits,
	// route through the REST API (PATCH /repos/{repo}/pulls/{n}), which never
	// touches projects. Anything more exotic (labels, assignees, base, etc.) is
	// left untouched so gh handles it.
	if rewritten, ok := rewritePREditToRESTAPI(args, repoDir, t.githubToken); ok {
		args = rewritten
	}

	cmd := exec.Command("gh", args...)
	cmd.Dir = repoDir

	// Set GITHUB_TOKEN environment variable for authenticated API calls
	if t.githubToken != "" {
		env := os.Environ()
		env = append(env, fmt.Sprintf("GITHUB_TOKEN=%s", t.githubToken))
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("gh command failed: %v\nStderr: %s", err, stderr.String()),
			"GitHub CLI command execution failed",
		)
	}

	result := map[string]any{
		"stdout":  stdout.String(),
		"stderr":  stderr.String(),
		"command": fmt.Sprintf("gh %v", args),
	}

	observation := fmt.Sprintf("Successfully executed: gh %v", args)
	if stdout.Len() > 0 {
		observation += fmt.Sprintf("\nOutput: %s", stdout.String())
	}

	return core.CreateSuccessResponse(
		"GitHub CLI command executed successfully",
		observation,
		result,
	)
}

func (t *GHTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

// Require owner/repo segments before /pull/ so branch names that happen to
// contain "/pull/" (e.g. feature/pull/3) aren't misread as a PR number.
var prURLNumberRe = regexp.MustCompile(`/[^/]+/[^/]+/pull/(\d+)(?:/|$)`)

// rewritePREditToRESTAPI translates a title/body-only `gh pr edit <ref>` into an
// equivalent `gh api --method PATCH repos/<repo>/pulls/<n>` invocation, which
// avoids the Projects (classic) GraphQL query that makes `gh pr edit` fail on
// orgs with classic projects disabled. It returns (newArgs, true) only when the
// command can be faithfully expressed via the REST API — i.e. the only mutating
// flags are --title/--body. For any other flag (labels, assignees, reviewers,
// milestone, base, --body-file, projects, …) it returns (nil, false) so the
// caller runs the original command unchanged.
func rewritePREditToRESTAPI(args []string, repoDir, token string) ([]string, bool) {
	if len(args) < 3 || args[0] != "pr" || args[1] != "edit" {
		return nil, false
	}

	var prRef, repo, title, body string
	var haveTitle, haveBody bool

	i := 2
	for i < len(args) {
		a := args[i]
		switch a {
		case "--title", "-t":
			if i+1 >= len(args) {
				return nil, false
			}
			title, haveTitle = args[i+1], true
			i += 2
		case "--body", "-b":
			if i+1 >= len(args) {
				return nil, false
			}
			body, haveBody = args[i+1], true
			i += 2
		case "--repo", "-R":
			if i+1 >= len(args) {
				return nil, false
			}
			repo = args[i+1]
			i += 2
		default:
			if strings.HasPrefix(a, "-") {
				// Unsupported flag — let gh handle the original command.
				return nil, false
			}
			if prRef != "" {
				return nil, false
			}
			prRef = a
			i++
		}
	}

	if !haveTitle && !haveBody {
		return nil, false
	}

	num := extractPRNumber(prRef)
	if num == "" && prRef != "" {
		// A non-empty ref we couldn't parse (a branch name, an unexpected URL
		// shape, …) — don't guess which PR it means; let gh run as-is.
		return nil, false
	}

	if repo == "" {
		repo = resolveNameWithOwner(repoDir, token)
		if repo == "" {
			return nil, false
		}
	}

	if num == "" {
		// prRef omitted (`gh pr edit --title …` while checked out on the PR
		// branch — the common form). Resolve the PR number from the current
		// branch via REST, which is safe from the projects GraphQL error.
		num = resolvePRNumberFromBranch(repoDir, repo, token)
		if num == "" {
			return nil, false
		}
	}

	out := []string{"api", "--method", "PATCH", fmt.Sprintf("repos/%s/pulls/%s", repo, num)}
	if haveTitle {
		out = append(out, "-f", "title="+title)
	}
	if haveBody {
		out = append(out, "-f", "body="+body)
	}
	return out, true
}

// extractPRNumber returns the numeric PR id from a bare number or a PR URL, or
// "" when the reference is a branch name or otherwise non-numeric (in which case
// we don't rewrite, since the REST path needs a number).
func extractPRNumber(ref string) string {
	if ref == "" {
		return ""
	}
	if isAllDigits(ref) {
		return ref
	}
	if m := prURLNumberRe.FindStringSubmatch(ref); len(m) == 2 {
		return m[1]
	}
	return ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// resolveNameWithOwner returns "owner/repo" for the repository checked out in
// repoDir, or "" if it can't be determined. Used when `gh pr edit` was invoked
// without an explicit --repo (it infers the repo from the cwd).
func resolveNameWithOwner(repoDir, token string) string {
	cmd := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	cmd.Dir = repoDir
	if token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("GITHUB_TOKEN=%s", token))
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolvePRNumberFromBranch returns the open PR number whose head is the branch
// currently checked out in repoDir, or "" if it can't be determined. Used when
// `gh pr edit` is invoked without an explicit PR ref. The lookup goes through
// the REST API (GET /repos/{owner}/{repo}/pulls?head=…), which — unlike
// `gh pr edit`/`gh pr view` — never issues the projects GraphQL query.
func resolvePRNumberFromBranch(repoDir, repo, token string) string {
	owner, repoName, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || repoName == "" {
		return ""
	}

	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = repoDir
	branchOut, err := branchCmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return ""
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls?head=%s:%s",
		url.PathEscape(owner), url.PathEscape(repoName),
		url.QueryEscape(owner), url.QueryEscape(branch))
	cmd := exec.Command("gh", "api", apiPath, "--jq", ".[0].number")
	cmd.Dir = repoDir
	if token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("GITHUB_TOKEN=%s", token))
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	num := strings.TrimSpace(string(out))
	if !isAllDigits(num) {
		return ""
	}
	return num
}
