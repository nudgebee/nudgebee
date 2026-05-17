package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/gitprovider"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/planners"
	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

// PRFollowupAgent addresses CI failures and review comments on existing PRs/MRs.
type PRFollowupAgent struct {
	llmClient    *llm.Client
	config       *config.Config
	logger       *common.Logger
	workspaceDir string
	toolTracker  *common.ToolInvocationTracker
	gitToken     string
	provider     gitprovider.GitProvider
}

// PRFollowupRequest contains all info needed for a followup.
type PRFollowupRequest struct {
	RepoURL  string
	Branch   string
	PRNumber int
	PRURL    string
	Provider string // "github" or "gitlab"
}

// PRFollowupResult is the structured output from a followup execution.
type PRFollowupResult struct {
	Success          bool     `json:"success"`
	Summary          string   `json:"summary"`
	FilesModified    []string `json:"files_modified"`
	CommitHash       string   `json:"commit_hash"`
	CommentPosted    bool     `json:"comment_posted"`
	CIIssuesFixed    []string `json:"ci_issues_fixed"`
	ReviewsAddressed []string `json:"reviews_addressed"`
	Error            string   `json:"error,omitempty"`
	// NoOp signals that the run found nothing actionable (no unaddressed
	// comments, no CI failures) or the planner ran but produced no observable
	// change (no commit, no metadata edit, no comment reply sent). The cron
	// uses this to avoid burning the retry budget on healthy idle iterations
	// — without it, a PR that gets reviewed >2h after creation can hit the
	// iteration cap before the reviewer ever shows up.
	NoOp bool `json:"no_op,omitempty"`
}

// reviewComment represents a single review comment that needs to be addressed.
type reviewComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	User      string `json:"user"`
	CreatedAt string `json:"created_at"`
	Source    string `json:"source"` // "inline", "review_body", "issue_comment"
}

// commentResponse maps a review comment ID to the agent's response.
type commentResponse struct {
	CommentID   int64  `json:"comment_id"`
	Action      string `json:"action"` // "fixed", "acknowledged", "wont_fix"
	ShouldReply bool   `json:"should_reply"`
	Reply       string `json:"reply"` // The inline reply text
}

func NewPRFollowupAgent(cfg *config.Config, llmClient *llm.Client, logger *common.Logger, workspaceDir string, gitToken string, provider string) *PRFollowupAgent {
	return &PRFollowupAgent{
		llmClient:    llmClient,
		config:       cfg,
		logger:       logger,
		workspaceDir: workspaceDir,
		toolTracker:  common.NewToolInvocationTracker("pr_followup_agent"),
		gitToken:     gitToken,
		provider:     gitprovider.ParseProvider(provider),
	}
}

// Execute gathers PR/MR context, runs a ReAct planner to fix issues, commits, pushes, and comments.
func (a *PRFollowupAgent) Execute(ctx context.Context, req PRFollowupRequest) (*PRFollowupResult, error) {
	mrTerm := gitprovider.GetMergeRequestTerminology(a.provider)

	a.logger.Log(common.EventStepStart, "PRFollowupAgent starting", map[string]any{
		"pr_number": req.PRNumber,
		"pr_url":    req.PRURL,
		"branch":    req.Branch,
		"provider":  req.Provider,
	})

	// Parse repo info using provider-aware parser
	repoInfo, err := a.parseRepoFromURL(req.PRURL, req.RepoURL)
	if err != nil {
		a.logger.Error(common.EventAnalysisFailure, fmt.Sprintf("Failed to parse %s URL", mrTerm), err, nil)
		return &PRFollowupResult{Success: false, Error: fmt.Sprintf("failed to parse %s URL: %v", mrTerm, err)}, err
	}

	prNumber := strconv.Itoa(req.PRNumber)

	// Resolve the PR's actual head branch from the provider — never trust
	// the caller's value. CLI defaults to "main" when --branch isn't passed,
	// which silently breaks downstream queries (CI checks, branch-scoped
	// log lookups) by pointing them at the wrong ref.
	if resolved := a.resolveHeadBranch(repoInfo, prNumber); resolved != "" {
		req.Branch = resolved
	}

	// --- Step 1: Gather PR/MR context ---
	a.logger.Log(common.EventStepStart, fmt.Sprintf("Gathering %s context", mrTerm), map[string]any{"repo": repoInfo.FullPath, "pr_number": req.PRNumber, "branch": req.Branch})

	prDetails := a.gatherPRDetails(ctx, repoInfo, prNumber)
	inlineComments, inlineText := a.gatherInlineComments(ctx, repoInfo, prNumber)
	issueComments, issueText := a.gatherIssueComments(ctx, repoInfo, prNumber)
	reviewBodyComments, reviewBodyText := a.gatherReviewBodyComments(ctx, repoInfo, prNumber)
	prDiff := a.gatherDiff(ctx, repoInfo, prNumber)
	ciFailureLogs := a.gatherCIFailureLogs(ctx, repoInfo, prNumber, req.Branch)

	// Merge all pending comments into one slice
	pendingComments := append(inlineComments, issueComments...)
	pendingComments = append(pendingComments, reviewBodyComments...)

	// Build combined comments text for the prompt
	commentsText := ""
	if inlineText != "" || issueText != "" || reviewBodyText != "" {
		var combined strings.Builder
		if inlineText != "" {
			combined.WriteString(inlineText)
		}
		if reviewBodyText != "" {
			combined.WriteString(reviewBodyText)
		}
		if issueText != "" {
			combined.WriteString(issueText)
		}
		commentsText = combined.String()
	}

	if len(pendingComments) == 0 && ciFailureLogs == "" {
		a.logger.Log(common.EventStepComplete, "No unaddressed comments or CI failures — nothing to do", nil)
		return &PRFollowupResult{
			Success: true,
			NoOp:    true,
			Summary: "No unaddressed review comments or CI failures found",
		}, nil
	}

	a.logger.Log(common.EventStepComplete, "Gathered PR context", map[string]any{
		"inline_comments":      len(inlineComments),
		"issue_comments":       len(issueComments),
		"review_body_comments": len(reviewBodyComments),
		"has_ci_failures":      ciFailureLogs != "",
	})

	// --- Step 2: Build system prompt ---
	systemPrompt := a.buildSystemPrompt(repoInfo.FullPath, prNumber, prDetails, prDiff, commentsText, ciFailureLogs, pendingComments)

	// --- Step 3: Create tools and ReAct planner ---
	replaceTool := tools.NewReplaceToolWithWorkspace(a.workspaceDir)
	replaceTool.SetEditCorrectionService(tools.NewEditCorrectionService(a.llmClient))

	rawTools := []core.NBTool{
		tools.NewFileViewTool(a.workspaceDir),
		tools.NewFileFindTool(a.workspaceDir),
		replaceTool,
		tools.NewGrepTool(a.workspaceDir),
		tools.NewRipgrepTool(a.workspaceDir),
		tools.NewCLITool(a.workspaceDir),
		tools.NewGitTool(a.workspaceDir),
		a.newProviderCLITool(),
		tools.NewSubmitAnalysisTool(),
	}

	trackedTools := make([]core.NBTool, len(rawTools))
	for i, tool := range rawTools {
		trackedTools[i] = tools.NewTrackedToolWrapper(tool, a.toolTracker, a.logger)
	}

	planner := planners.NewReActPlanner(a.llmClient, trackedTools, a.config.Agent.ReActMaxIterations)
	planner.SetLogger(a.logger)
	planner.SetRepositoryContext(&planners.RepositoryContext{
		URL:        req.RepoURL,
		Branch:     req.Branch,
		LocalPath:  a.workspaceDir,
		GitHubRepo: repoInfo.FullPath,
	})

	// --- Step 4: Execute the planner ---
	userPrompt := fmt.Sprintf(
		"Address ALL issues on %s #%s in %s. Fix CI failures, address review comments, and ensure the code is correct. "+
			"After making changes, use submit_analysis to report what you fixed.",
		mrTerm, prNumber, repoInfo.FullPath,
	)

	// Snapshot state before the planner runs so we can detect whether the
	// agent actually changed anything. The agent owns its own git/metadata
	// ops; we only observe the outcome and refuse to amplify false claims
	// (e.g. "fixed" replies when nothing was pushed).
	preHead, _ := a.runCommandInDir("git", "rev-parse", "HEAD")
	preHead = strings.TrimSpace(preHead)
	prePRMeta := a.fetchPRMetaSnapshot(repoInfo, prNumber)

	a.logger.Log(common.EventStepStart, fmt.Sprintf("Executing ReAct planner for %s followup", mrTerm), nil)
	planResult, err := planner.Plan(ctx, userPrompt, systemPrompt)
	if err != nil {
		return a.failResult(fmt.Sprintf("planner execution failed: %v", err)), err
	}

	a.logger.Log(common.EventStepComplete, "ReAct planner completed", map[string]any{
		"status":     planResult.Status,
		"iterations": planResult.Iterations,
	})

	// --- Step 5: Observe outcome and reply to comments ---
	result := &PRFollowupResult{
		Success: planResult.Status == "completed",
		Summary: planResult.FinalAnswer,
	}

	// Extract structured data from submit_analysis if available
	if submitData := planner.GetSubmitAnalysisData(); submitData != nil {
		if data, ok := submitData.(map[string]any); ok {
			if fm, ok := data["files_modified"]; ok {
				if files, ok := fm.([]any); ok {
					for _, f := range files {
						if s, ok := f.(string); ok {
							result.FilesModified = append(result.FilesModified, s)
						}
					}
				}
			}
			if es, ok := data["execution_summary"].(string); ok {
				result.Summary = es
			}
		}
	}

	// Did the agent commit (and presumably push)? If HEAD moved, yes.
	// Require both pre and post HEADs to be non-empty: an empty preHead means
	// the workspace had no commits before the planner ran (defensive — Execute
	// is normally invoked on a populated clone, but the agent could in principle
	// initialize a repo via cli_tool and we don't want to count that as "fixed").
	postHead, _ := a.runCommandInDir("git", "rev-parse", "HEAD")
	postHead = strings.TrimSpace(postHead)
	if preHead != "" && postHead != "" && postHead != preHead {
		result.CommitHash = postHead
		a.logger.Log(common.EventStepComplete, "Agent committed", map[string]any{
			"pre_head":  preHead,
			"post_head": postHead,
		})
	} else if dirty, _ := a.runCommandInDir("git", "status", "--porcelain"); strings.TrimSpace(dirty) != "" {
		a.logger.Log(common.EventStepFailure, "Agent left uncommitted changes in workspace", map[string]any{
			"head": preHead,
		})
	}

	// Did the agent edit PR metadata (title/body)? Compare snapshot.
	postPRMeta := a.fetchPRMetaSnapshot(repoInfo, prNumber)
	metaChanged := prePRMeta != "" && postPRMeta != "" && prePRMeta != postPRMeta
	if metaChanged {
		a.logger.Log(common.EventStepComplete, "Agent edited PR metadata", nil)
	}

	// Truth-check: a "fixed" claim asserts a real change. If nothing
	// observable changed (no commit, no metadata edit), don't amplify the
	// claim — skip those replies. The next run, on fresh state, can retry.
	agentChangedSomething := result.CommitHash != "" || metaChanged

	// --- Step 6: Reply to individual comments ---
	// Route replies based on comment source: inline → thread reply, issue/review_body → issue comment
	var responses []commentResponse
	if len(pendingComments) > 0 {
		responses = a.extractCommentResponses(planner.GetSubmitAnalysisData(), result.Summary, pendingComments)
		// Build a source lookup from pending comments
		commentSource := make(map[int64]string)
		for _, c := range pendingComments {
			commentSource[c.ID] = c.Source
		}

		repliedCount := 0
		skippedUnverified := 0
		skippedByAgent := 0
		for _, resp := range responses {
			if !resp.ShouldReply {
				skippedByAgent++
				continue
			}
			if resp.Action == "fixed" && !agentChangedSomething {
				skippedUnverified++
				a.logger.Log(common.EventStepFailure, "Skipping unverified 'fixed' reply — no commit or metadata change observed", map[string]any{
					"comment_id": resp.CommentID,
				})
				continue
			}
			replyBody := fmt.Sprintf("**Automated Followup**\n\n%s", resp.Reply)
			var replyErr error
			switch commentSource[resp.CommentID] {
			case "inline":
				replyErr = a.replyToComment(repoInfo, prNumber, resp.CommentID, replyBody)
			default:
				// issue_comment and review_body: post a top-level issue comment as reply
				replyErr = a.postIssueComment(repoInfo, prNumber, replyBody)
			}
			if replyErr != nil {
				a.logger.Error(common.EventStepFailure, "Failed to reply to comment", replyErr, map[string]any{"comment_id": resp.CommentID, "source": commentSource[resp.CommentID]})
			} else {
				repliedCount++
				result.ReviewsAddressed = append(result.ReviewsAddressed, fmt.Sprintf("#%d: %s", resp.CommentID, resp.Action))
			}
		}
		a.logger.Log(common.EventStepComplete, "Posted replies", map[string]any{
			"replied":            repliedCount,
			"skipped_unverified": skippedUnverified,
			"skipped_by_agent":   skippedByAgent,
			"total":              len(pendingComments),
		})
		result.CommentPosted = repliedCount > 0
	}

	// --- Step 7: Post summary comment (only when code was changed) ---
	if result.CommitHash != "" {
		summaryBody := a.buildSummaryComment(result, responses)
		if err := a.postIssueComment(repoInfo, prNumber, summaryBody); err != nil {
			a.logger.Error(common.EventStepFailure, "Failed to post summary comment", err, nil)
		}
	}

	// Mark as no-op if the planner ran but produced no observable change.
	// This happens when (a) the agent explored without converging on a fix,
	// (b) the only signal was a CI failure the agent couldn't address, or
	// (c) the agent decided every comment needed no reply. The cron treats
	// no-ops as "nothing happened, try again later" rather than failures
	// that count toward the iteration budget.
	if !agentChangedSomething && !result.CommentPosted {
		result.NoOp = true
	}

	return result, nil
}

func (a *PRFollowupAgent) failResult(msg string) *PRFollowupResult {
	return &PRFollowupResult{Success: false, Error: msg}
}

// buildCLIEnv returns an environment slice for exec'd CLI tools (gh, glab, git)
// that carries the git provider auth token and a fixed nudgebee-bot identity
// for git commits. The workspace pod has neither by default, which causes:
//   - gh/glab: "To get started with GitHub CLI, please run: gh auth login"
//   - git commit: "Author identity unknown ... unable to auto-detect email"
//
// Callers should assign the result to cmd.Env before cmd.Run().
func (a *PRFollowupAgent) buildCLIEnv() []string {
	env := os.Environ()
	if a.gitToken != "" {
		switch a.provider {
		case gitprovider.GitProviderGitLab:
			env = append(env, "GITLAB_TOKEN="+a.gitToken)
		default:
			env = append(env, "GITHUB_TOKEN="+a.gitToken, "GH_TOKEN="+a.gitToken)
		}
	}
	// Match the identity used by orchestrator_agent.go:commitChanges so all
	// agent-authored commits on a PR appear under a single author.
	env = append(env,
		"GIT_AUTHOR_NAME=nudgebee-bot",
		"GIT_AUTHOR_EMAIL=bot@nudgebee.com",
		"GIT_COMMITTER_NAME=nudgebee-bot",
		"GIT_COMMITTER_EMAIL=bot@nudgebee.com",
	)
	return env
}

func (a *PRFollowupAgent) runCommandInDir(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = a.workspaceDir
	cmd.Env = a.buildCLIEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s failed: %w\nstderr: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func (a *PRFollowupAgent) parseRepoFromURL(prURL, repoURL string) (*gitprovider.RepoInfo, error) {
	// repoURL may be a local filesystem path (CLI usage with --repo /some/path)
	// rather than a git URL — in that case fall back to the PR URL, which is
	// always a real provider URL.
	targetURL := repoURL
	if !looksLikeRemoteURL(targetURL) {
		targetURL = prURL
	}
	if targetURL == "" {
		return nil, fmt.Errorf("no URL provided")
	}

	// PR/MR URLs end with `/pull/<N>` (GitHub) or `/-/merge_requests/<N>`
	// (GitLab). ExtractRepoInfo expects a bare repo URL, so strip the suffix.
	targetURL = stripPRSuffix(targetURL)

	provider := a.provider
	if provider == gitprovider.GitProviderUnknown {
		provider = gitprovider.DetectProvider(targetURL)
	}

	return gitprovider.ExtractRepoInfo(targetURL, provider)
}

func looksLikeRemoteURL(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}

var prSuffixPattern = regexp.MustCompile(`/(pull|issues|-/merge_requests|-/issues)/\d+(/.*)?$`)

func stripPRSuffix(s string) string {
	return prSuffixPattern.ReplaceAllString(s, "")
}

func (a *PRFollowupAgent) newProviderCLITool() core.NBTool {
	if a.provider == gitprovider.GitProviderGitLab {
		return tools.NewGLabToolWithToken(a.workspaceDir, a.gitToken)
	}
	return tools.NewGHToolWithToken(a.workspaceDir, a.gitToken)
}

// providerJQQuery runs the provider-appropriate `gh`/`glab` invocation to
// fetch a PR/MR field via a jq filter. Returns trimmed output, or "" on
// error (caller should treat empty as "unknown"). Arguments are passed
// directly to exec — no shell interpretation, so PR-derived strings like
// repoInfo.FullPath cannot be treated as shell metacharacters.
func (a *PRFollowupAgent) providerJQQuery(repoInfo *gitprovider.RepoInfo, prNumber string, ghJSONFields, ghJQ, glabJQ, errLabel string) string {
	var (
		out string
		err error
	)
	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		out, err = a.runCommandInDir("glab", "api",
			fmt.Sprintf("projects/%s/merge_requests/%s", encodedPath, prNumber),
			"--jq", glabJQ,
		)
	} else {
		out, err = a.runCommandInDir("gh", "pr", "view", prNumber,
			"--repo", repoInfo.FullPath,
			"--json", ghJSONFields,
			"--jq", ghJQ,
		)
	}
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to "+errLabel, map[string]any{"error": err.Error()})
		return ""
	}
	return strings.TrimSpace(out)
}

// resolveHeadBranch returns the PR/MR's actual head ref name from the
// provider. Returns "" if the lookup fails so the caller can keep whatever
// value it has.
func (a *PRFollowupAgent) resolveHeadBranch(repoInfo *gitprovider.RepoInfo, prNumber string) string {
	return a.providerJQQuery(repoInfo, prNumber, "headRefName", ".headRefName", ".source_branch", "resolve head branch")
}

// fetchPRMetaSnapshot returns a string fingerprint of the PR's title and body,
// used to detect whether the agent edited PR metadata during its run. Returns
// empty string on error so callers treat it as "unknown" (no claim of change).
func (a *PRFollowupAgent) fetchPRMetaSnapshot(repoInfo *gitprovider.RepoInfo, prNumber string) string {
	return a.providerJQQuery(repoInfo, prNumber, "title,body", `.title + " " + .body`, `.title + " " + .description`, "snapshot PR metadata")
}

// gatherPRDetails fetches PR/MR description and metadata
func (a *PRFollowupAgent) gatherPRDetails(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string) string {
	var out string
	var err error
	if a.provider == gitprovider.GitProviderGitLab {
		out, err = a.runCommandInDir("glab", "mr", "view", prNumber, "--repo", repoInfo.FullPath)
	} else {
		out, err = a.runCommandInDir("gh", "pr", "view", prNumber, "--repo", repoInfo.FullPath, "--json", "title,body,state,reviews,statusCheckRollup")
	}
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to gather PR details", map[string]any{"error": err.Error()})
		return ""
	}
	return out
}

// gatherInlineComments fetches review comments and returns unaddressed ones.
// Returns structured comments for reply tracking and formatted text for the LLM prompt.
func (a *PRFollowupAgent) gatherInlineComments(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string) ([]reviewComment, string) {
	var out string
	var err error
	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		out, err = a.runCommandInDir("glab", "api", fmt.Sprintf("projects/%s/merge_requests/%s/notes", encodedPath, prNumber))
	} else {
		out, err = a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/pulls/%s/comments", repoInfo.FullPath, prNumber))
	}
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to gather inline comments", map[string]any{"error": err.Error()})
		return nil, ""
	}

	// Parse the raw API response
	var rawComments []struct {
		ID           int64  `json:"id"`
		Body         string `json:"body"`
		Path         string `json:"path"`
		Line         int    `json:"line"`
		OriginalLine int    `json:"original_line"`
		User         struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt   string `json:"created_at"`
		InReplyToID *int64 `json:"in_reply_to_id"`
	}
	if err := json.Unmarshal([]byte(out), &rawComments); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse inline comments", map[string]any{"error": err.Error()})
		return nil, out // Return raw text as fallback
	}

	// Collect top-level review comments that haven't been replied to by our bot
	// A comment is "addressed" if there's a reply from us (containing "Automated Followup") in its thread
	repliedCommentIDs := make(map[int64]bool)
	for _, c := range rawComments {
		if c.InReplyToID != nil && strings.Contains(c.Body, "Automated Followup") {
			repliedCommentIDs[*c.InReplyToID] = true
		}
	}

	var unaddressed []reviewComment
	for _, c := range rawComments {
		// Skip replies (we only process top-level review comments)
		if c.InReplyToID != nil {
			continue
		}
		// Skip comments we've already replied to
		if repliedCommentIDs[c.ID] {
			continue
		}
		// Do not filter by author (e.g. "[bot]" suffix): useful review bots like
		// gemini-code-assist and coderabbitai produce actionable feedback. The
		// ReAct planner downstream triages each comment individually via the
		// fixed/acknowledged/wont_fix framework, so noisy bot comments cost at
		// most a few tokens and won't cause bogus code changes.

		line := c.Line
		if line == 0 {
			line = c.OriginalLine
		}

		unaddressed = append(unaddressed, reviewComment{
			ID:        c.ID,
			Body:      c.Body,
			Path:      c.Path,
			Line:      line,
			User:      c.User.Login,
			CreatedAt: c.CreatedAt,
			Source:    "inline",
		})
	}

	// Build formatted text for the LLM prompt
	if len(unaddressed) == 0 {
		return nil, ""
	}

	var sb strings.Builder
	sb.WriteString("#### Inline Review Comments\n\n")
	for i, c := range unaddressed {
		fmt.Fprintf(&sb, "### Comment #%d (ID: %d, source: inline) by @%s\n", i+1, c.ID, c.User)
		fmt.Fprintf(&sb, "**File:** `%s` (line %d)\n", c.Path, c.Line)
		fmt.Fprintf(&sb, "**Comment:**\n%s\n\n", c.Body)
	}

	return unaddressed, sb.String()
}

// gatherIssueComments fetches all top-level PR/issue comments (not inline review comments).
// Excludes only our own "Automated Followup" markers. The agent decides per-comment whether
// engaging adds value via should_reply — we don't pre-filter by timestamp or author, since
// coarse heuristics hide actionable signal (e.g. labeler bot comments explaining WHY a
// check failed).
func (a *PRFollowupAgent) gatherIssueComments(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string) ([]reviewComment, string) {
	if a.provider == gitprovider.GitProviderGitLab {
		// GitLab MR notes are a single API; inline comments are already covered
		return nil, ""
	}

	out, err := a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/issues/%s/comments", repoInfo.FullPath, prNumber))
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to gather issue comments", map[string]any{"error": err.Error()})
		return nil, ""
	}

	var rawComments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(out), &rawComments); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse issue comments", map[string]any{"error": err.Error()})
		return nil, ""
	}

	var visible []reviewComment
	for _, c := range rawComments {
		// Skip our own automated comments — these are state we wrote, not signal.
		if strings.Contains(c.Body, "Automated Followup") || strings.Contains(c.Body, "Nudgebee Automated Followup") {
			continue
		}
		visible = append(visible, reviewComment{
			ID:        c.ID,
			Body:      c.Body,
			User:      c.User.Login,
			CreatedAt: c.CreatedAt,
			Source:    "issue_comment",
		})
	}

	if len(visible) == 0 {
		return nil, ""
	}

	var sb strings.Builder
	sb.WriteString("#### PR Discussion Comments\n\n")
	for i, c := range visible {
		fmt.Fprintf(&sb, "### Comment #%d (ID: %d, source: issue_comment) by @%s\n", i+1, c.ID, c.User)
		fmt.Fprintf(&sb, "**Comment:**\n%s\n\n", c.Body)
	}

	return visible, sb.String()
}

// gatherReviewBodyComments fetches review submission body text (the top-level text of a review, not inline comments).
func (a *PRFollowupAgent) gatherReviewBodyComments(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string) ([]reviewComment, string) {
	if a.provider == gitprovider.GitProviderGitLab {
		return nil, ""
	}

	out, err := a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/pulls/%s/reviews", repoInfo.FullPath, prNumber))
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to gather reviews", map[string]any{"error": err.Error()})
		return nil, ""
	}

	var rawReviews []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State     string `json:"state"`
		CreatedAt string `json:"submitted_at"`
	}
	if err := json.Unmarshal([]byte(out), &rawReviews); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse reviews", map[string]any{"error": err.Error()})
		return nil, ""
	}

	var unaddressed []reviewComment
	for _, r := range rawReviews {
		// Skip reviews with no body text (just approvals or inline-only reviews)
		body := strings.TrimSpace(r.Body)
		if body == "" {
			continue
		}
		// Skip our own automated reviews
		if strings.Contains(body, "Automated Followup") || strings.Contains(body, "Nudgebee Automated Followup") {
			continue
		}
		// Do not filter by author; the ReAct planner triages each comment via
		// the fixed/acknowledged/wont_fix framework. See inline gatherer.

		unaddressed = append(unaddressed, reviewComment{
			ID:        r.ID,
			Body:      body,
			User:      r.User.Login,
			CreatedAt: r.CreatedAt,
			Source:    "review_body",
		})
	}

	if len(unaddressed) == 0 {
		return nil, ""
	}

	var sb strings.Builder
	sb.WriteString("#### Review Body Comments\n\n")
	for i, c := range unaddressed {
		fmt.Fprintf(&sb, "### Comment #%d (ID: %d, source: review_body) by @%s\n", i+1, c.ID, c.User)
		fmt.Fprintf(&sb, "**Comment:**\n%s\n\n", c.Body)
	}

	return unaddressed, sb.String()
}

// gatherDiff fetches the PR/MR diff
func (a *PRFollowupAgent) gatherDiff(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string) string {
	var out string
	var err error
	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		out, err = a.runCommandInDir("glab", "api", fmt.Sprintf("projects/%s/merge_requests/%s/changes", encodedPath, prNumber))
	} else {
		out, err = a.runCommandInDir("gh", "pr", "diff", prNumber, "--repo", repoInfo.FullPath)
	}
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to gather diff", map[string]any{"error": err.Error()})
		return ""
	}
	return out
}

// gatherCIFailureLogs fetches CI/CD failure logs with actual error output.
func (a *PRFollowupAgent) gatherCIFailureLogs(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string, branch string) string {
	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		out, err := a.runCommandInDir("glab", "api", fmt.Sprintf("projects/%s/merge_requests/%s/pipelines", encodedPath, prNumber))
		if err != nil {
			a.logger.Log(common.EventStepFailure, "Failed to gather GitLab CI logs", map[string]any{"error": err.Error()})
			return ""
		}
		return out
	}

	// GitHub: two-step approach
	// 1. Get failed check run names (for context)
	// 2. Get actual workflow run logs via `gh run view --log-failed`
	ref := branch
	if ref == "" {
		ref = "HEAD"
	}

	var sb strings.Builder

	// Step 1: List failed check runs with their names
	checkNames, err := a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/commits/%s/check-runs", repoInfo.FullPath, ref), "--jq", `.check_runs[] | select(.conclusion=="failure") | .name`)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to list failed checks", map[string]any{"error": err.Error()})
		return ""
	}

	failedChecks := strings.TrimSpace(checkNames)
	if failedChecks == "" {
		return ""
	}

	fmt.Fprintf(&sb, "### Failed Checks\n%s\n\n", failedChecks)

	// Step 2: Get failed workflow run logs (GitHub Actions specific)
	runListOut, err := a.runCommandInDir("gh", "run", "list", "--branch", branch, "--status", "failure", "--limit", "5", "--json", "databaseId,name,conclusion", "--repo", repoInfo.FullPath)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to list failed runs", map[string]any{"error": err.Error()})
		return sb.String() // Return check names at minimum
	}

	var runs []struct {
		DatabaseID int64  `json:"databaseId"`
		Name       string `json:"name"`
	}
	if err := json.Unmarshal([]byte(runListOut), &runs); err != nil || len(runs) == 0 {
		return sb.String()
	}

	// Fetch logs for up to 3 failed runs (to keep context manageable)
	maxRuns := 3
	if len(runs) < maxRuns {
		maxRuns = len(runs)
	}
	for i := 0; i < maxRuns; i++ {
		run := runs[i]
		fmt.Fprintf(&sb, "### Workflow: %s (Run #%d)\n", run.Name, run.DatabaseID)

		logOut, err := a.runCommandInDir("gh", "run", "view", strconv.FormatInt(run.DatabaseID, 10), "--log-failed", "--repo", repoInfo.FullPath)
		if err != nil {
			fmt.Fprintf(&sb, "(Failed to fetch logs: %s)\n\n", err.Error())
			continue
		}
		logOut = strings.TrimSpace(logOut)
		if logOut == "" {
			fmt.Fprintf(&sb, "(No log output available)\n\n")
		} else {
			fmt.Fprintf(&sb, "```\n%s\n```\n\n", tailLines(logOut, 100))
		}
	}

	return sb.String()
}

func (a *PRFollowupAgent) buildSystemPrompt(repo, prNumber, prDetails, diff, comments, ciLogs string, pendingComments []reviewComment) string {
	mrTerm := gitprovider.GetMergeRequestTerminology(a.provider)
	mrFullTerm := gitprovider.GetMergeRequestFullTerminology(a.provider)
	cliTool := gitprovider.GetCLIToolName(a.provider)

	var sb strings.Builder
	fmt.Fprintf(&sb, `You are an expert software engineer tasked with addressing issues on %s #%s in repository %s.

Your goal is to:
1. Fix any CI/CD failures
2. Address review comments from code reviewers
3. Ensure the code is correct and follows project conventions

You have access to the repository workspace and can read, modify, and create files.

## %s Details
%s

`, mrFullTerm, prNumber, repo, strings.ToUpper(mrTerm), prDetails)

	if diff != "" {
		fmt.Fprintf(&sb, "## Current %s Diff\n%s\n\n", mrTerm, truncateIfNeeded(diff, 10000))
	}

	if comments != "" {
		fmt.Fprintf(&sb, "## Review Comments (Unaddressed)\n%s\n\n", truncateIfNeeded(comments, 5000))
	}

	if ciLogs != "" {
		fmt.Fprintf(&sb, "## CI/CD Failure Logs\n%s\n\n", truncateIfNeeded(ciLogs, 10000))
	}

	fmt.Fprintf(&sb, `## Your job

Drive %[2]s #%[3]s in `+"`%[4]s`"+` to a mergeable state. The CI failures and review comments above are everything you need to address. The repo workspace is the cwd of every tool call.

You own the full flow: code edits, PR metadata, git history, commit, push. The framework only posts comment replies (after you call submit_analysis). It will not commit or push for you.

## Triaging CI failures

Read the failure output. There is no fixed taxonomy — classify by what the failure is actually about:

- **Source code** (build, lint, tests, types, format) → fix the code.
- **PR metadata** (missing issue link, wrong title, label rules, etc.) → fix via `+"`%[1]s pr edit`"+` or `+"`glab mr update`"+`. If a labeler check is failing and the failure message doesn't tell you the rule, read the labeler config (e.g. `+"`.github/labeler.yml`"+`) — don't guess from the failure text alone.
- **Commit history** (single-commit rules, sign-off, etc.) → fix git history (amend / soft-reset+commit / squash). See safety rules below.
- **Pipeline-only checks unrelated to the diff** (deploy gates, coverage thresholds, manual approvals) → mark wont_fix in your analysis, no code or metadata change.

Don't revert correct code changes because a failing check is unrelated.

## Committing and pushing

You commit and push your own work. The git identity (`+"`nudgebee-bot <bot@nudgebee.com>`"+`) is already configured via env vars — just run git commands.

**Two safety rules — non-negotiable:**

1. Never use `+"`git push --force`"+`. Use `+"`--force-with-lease`"+` only.
2. Never rewrite history (amend, reset, rebase, squash) if any commit between the PR base and HEAD has a non-bot author. Verify before rewriting:
   `+"`git log --format=%%ae origin/<base>..HEAD`"+`
   Every line must be `+"`bot@nudgebee.com`"+`. If any other author appears, make a regular new commit on top instead.

Pick the right strategy for the situation:
- **Amend** when there's exactly one bot commit ahead and you want to fold edits in: `+"`git add -A && git commit --amend --no-edit && git push --force-with-lease`"+`.
- **Squash** when multiple bot commits exist and a single-commit rule applies. Preserve the FIRST commit's message — it carries the PR's intent: `+"`git log -1 --format=%%B $(git log --reverse --format=%%H origin/<base>..HEAD | head -1)`"+`, then `+"`git add -A && git reset --soft origin/<base> && git commit -m \"<that message>\" && git push --force-with-lease`"+`.
- **New commit** when human commits are present in the chain, or when separate history matters: `+"`git add -A && git commit -m \"<msg>\" && git push`"+` (no force).

Resolve the PR's actual base branch with `+"`%[1]s pr view %[3]s --repo %[4]s --json baseRefName --jq .baseRefName`"+` rather than assuming `+"`main`"+`.
`, cliTool, mrTerm, prNumber, repo)

	// Add per-comment response instructions
	if len(pendingComments) > 0 {
		fmt.Fprintf(&sb, `
## Per-comment response

For each comment above, decide on an action and whether replying adds value to the conversation.

Actions:
- **"fixed"** — The comment requested a change and you made it. Reply should describe the specific change.
- **"acknowledged"** — The comment is correct/informational and no change is needed. Reply should explain why, citing specific code.
- **"wont_fix"** — The suggestion is valid but out of scope or you disagree. Reply should explain why.

Whether to reply (`+"`should_reply`"+`):
- A reply only adds value when there's a human reader who'll see it (a reviewer, the PR author, future maintainers reading history).
- Bot/CI comments — labeler validation messages, automated PR summaries from review bots that just describe the PR, GitHub Actions notifications — have no human reader for the reply. Set `+"`should_reply: false`"+`.
- Drive-by suggestions you're acknowledging without changing anything are usually noise. Set `+"`should_reply: false`"+` unless the reasoning is non-obvious enough that the reviewer would want to see it.
- For `+"`fixed`"+`, default to `+"`true`"+` — the reviewer should know their suggestion was applied.
- For `+"`wont_fix`"+`, default to `+"`true`"+` — the reviewer needs your reasoning.

You decide. Don't post replies that nobody will read.

When calling submit_analysis, include `+"`comment_responses`"+`:

{
  "execution_summary": "...",
  "files_modified": [...],
  "comment_responses": [
`)
		for i, c := range pendingComments {
			if i > 0 {
				sb.WriteString(",\n")
			}
			fmt.Fprintf(&sb, `    {"comment_id": %d, "action": "fixed|acknowledged|wont_fix", "should_reply": true|false, "reply": "..."}`, c.ID)
		}
		fmt.Fprintf(&sb, `
  ]
}

Replies should be concrete and short. Skip pleasantries.
`)
	}

	return sb.String()
}

func truncateIfNeeded(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated]"
}

// tailLines returns the last n lines of s, preserving the original behavior
// of `| tail -n` without shell execution.
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// buildSummaryComment creates a branded, bullet-point summary of what the followup did.
func (a *PRFollowupAgent) buildSummaryComment(result *PRFollowupResult, responses []commentResponse) string {
	var sb strings.Builder
	sb.WriteString("### Nudgebee Automated Followup\n\n")

	// Execution summary — the agent's own description of what it did
	if result.Summary != "" {
		fmt.Fprintf(&sb, "%s\n\n", result.Summary)
	}

	// Changes — get actual diff stats from git
	if result.CommitHash != "" {
		diffStat, err := a.runCommandInDir("git", "diff", "--stat", result.CommitHash+"~1", result.CommitHash)
		if err == nil && strings.TrimSpace(diffStat) != "" {
			sb.WriteString("**Changes:**\n```\n")
			sb.WriteString(strings.TrimSpace(diffStat))
			sb.WriteString("\n```\n\n")
		}
	}

	// Per-comment actions
	if len(responses) > 0 {
		var fixed, acked, wontfix int
		for _, r := range responses {
			switch r.Action {
			case "fixed":
				fixed++
			case "acknowledged":
				acked++
			case "wont_fix":
				wontfix++
			default:
				fixed++
			}
		}

		parts := []string{}
		if fixed > 0 {
			parts = append(parts, fmt.Sprintf("%d fixed", fixed))
		}
		if acked > 0 {
			parts = append(parts, fmt.Sprintf("%d acknowledged", acked))
		}
		if wontfix > 0 {
			parts = append(parts, fmt.Sprintf("%d declined", wontfix))
		}
		fmt.Fprintf(&sb, "**Review comments:** %s\n\n", strings.Join(parts, ", "))
	}

	// Commit reference
	if result.CommitHash != "" {
		shortHash := result.CommitHash
		if len(shortHash) > 12 {
			shortHash = shortHash[:12]
		}
		fmt.Fprintf(&sb, "**Commit:** `%s`\n", shortHash)
	}

	return sb.String()
}

// postIssueComment posts a top-level comment on the PR/MR (not an inline reply).
func (a *PRFollowupAgent) postIssueComment(repoInfo *gitprovider.RepoInfo, prNumber string, body string) error {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("failed to marshal comment payload: %w", err)
	}

	var args []string
	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		args = []string{"glab", "api", "--method", "POST",
			fmt.Sprintf("projects/%s/merge_requests/%s/notes", encodedPath, prNumber),
			"--input", "-"}
	} else {
		args = []string{"gh", "api", "--method", "POST",
			fmt.Sprintf("repos/%s/issues/%s/comments", repoInfo.FullPath, prNumber),
			"--input", "-"}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = a.workspaceDir
	cmd.Env = a.buildCLIEnv()
	cmd.Stdin = bytes.NewReader(payload)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to post comment: %w\nstderr: %s", err, stderr.String())
	}
	return nil
}

// replyToComment posts an inline reply to a specific review comment thread.
func (a *PRFollowupAgent) replyToComment(repoInfo *gitprovider.RepoInfo, prNumber string, commentID int64, body string) error {
	var payload []byte
	var args []string
	var err error

	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		// GitLab: reply to a note on an MR discussion
		payload, err = json.Marshal(map[string]string{"body": body})
		if err != nil {
			return fmt.Errorf("failed to marshal reply payload: %w", err)
		}
		// Find the discussion ID for this note, then reply to it
		args = []string{"glab", "api", "--method", "POST",
			fmt.Sprintf("projects/%s/merge_requests/%s/notes", encodedPath, prNumber),
			"--input", "-"}
	} else {
		// GitHub: reply in the same review comment thread using in_reply_to
		payload, err = json.Marshal(map[string]any{
			"body":        body,
			"in_reply_to": commentID,
		})
		if err != nil {
			return fmt.Errorf("failed to marshal reply payload: %w", err)
		}
		args = []string{"gh", "api", "--method", "POST",
			fmt.Sprintf("repos/%s/pulls/%s/comments", repoInfo.FullPath, prNumber),
			"--input", "-"}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = a.workspaceDir
	cmd.Env = a.buildCLIEnv()
	cmd.Stdin = bytes.NewReader(payload)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reply to comment %d: %w\nstderr: %s", commentID, err, stderr.String())
	}
	return nil
}

// extractCommentResponses parses the LLM's submit_analysis output for per-comment responses.
// Returns empty slice if the LLM didn't structure its output — we never fabricate replies,
// since posting noise (e.g. generic "Changes have been applied") on real PRs is worse than
// posting nothing.
func (a *PRFollowupAgent) extractCommentResponses(submitData any, _ string, _ []reviewComment) []commentResponse {
	if submitData == nil {
		return nil
	}
	data, ok := submitData.(map[string]any)
	if !ok {
		return nil
	}
	rawResponses, ok := data["comment_responses"]
	if !ok {
		return nil
	}
	responses, ok := rawResponses.([]any)
	if !ok {
		return nil
	}

	var result []commentResponse
	for _, r := range responses {
		rMap, ok := r.(map[string]any)
		if !ok {
			continue
		}
		// json.Unmarshal produces float64 for numbers
		var id int64
		switch v := rMap["comment_id"].(type) {
		case float64:
			id = int64(v)
		case int64:
			id = v
		}
		if id == 0 {
			continue
		}
		action, _ := rMap["action"].(string)
		reply, _ := rMap["reply"].(string)
		// should_reply: explicit bool from the agent. If absent, default
		// to true only when the agent gave us substantive reply text — an
		// empty reply with no explicit should_reply is treated as "skip".
		shouldReply := strings.TrimSpace(reply) != ""
		if v, present := rMap["should_reply"].(bool); present {
			shouldReply = v
		}
		result = append(result, commentResponse{
			CommentID:   id,
			Action:      action,
			ShouldReply: shouldReply,
			Reply:       reply,
		})
	}
	return result
}

// ParsePRNumber extracts PR/MR number from a URL.
func ParsePRNumber(prURL string) (int, error) {
	// GitHub: https://github.com/owner/repo/pull/123
	// GitLab: https://gitlab.com/group/project/-/merge_requests/456
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`/pull/(\d+)`),
		regexp.MustCompile(`/merge_requests/(\d+)`),
	}

	for _, p := range patterns {
		matches := p.FindStringSubmatch(prURL)
		if len(matches) >= 2 {
			return strconv.Atoi(matches[1])
		}
	}
	return 0, fmt.Errorf("could not extract PR/MR number from URL: %s", prURL)
}
