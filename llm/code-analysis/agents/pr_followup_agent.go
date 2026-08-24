package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/gitprovider"
	"nudgebee/code-analysis-agent/llm"
	"nudgebee/code-analysis-agent/planners"
	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

// autoCommitMaxFiles bounds how many files the deterministic auto-commit
// (see the commit-enforcement block in Execute) will stage. A legitimate
// PR-followup fix touches a handful of files; a working tree with more than
// this is treated as the agent having gone off the rails — e.g. a git
// reset/checkout to an unrelated ref — rather than a real fix, and gets
// discarded instead of committed. Observed live: a run left the sandbox with
// hundreds of unrelated files deleted after an apparent bad git operation;
// auto-committing that verbatim would have pushed it straight to the PR.
const autoCommitMaxFiles = 15

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

// AddressedComment is one PR comment a followup run has already answered
// (#36865). It travels both ways: in on the request as the durable record of
// what earlier runs did, and out on the result as what this run just did.
//
// Source is part of the identity alongside CommentID because issue comments and
// review submissions are separate GitHub id spaces and can collide — the same
// reason answeredCommentKey already keys on both.
type AddressedComment struct {
	Source      string    `json:"source"`     // "inline" | "issue_comment" | "review_body"
	CommentID   int64     `json:"comment_id"` // GitHub's numeric comment id
	Action      string    `json:"action"`     // "fixed" | "acknowledged" | "wont_fix"
	AddressedAt time.Time `json:"addressed_at"`
}

// PRFollowupRequest contains all info needed for a followup.
type PRFollowupRequest struct {
	RepoURL  string
	Branch   string
	PRNumber int
	PRURL    string
	Provider string // "github" or "gitlab"
	// AddressedComments is what previous runs already answered on this PR, read
	// from pr_followup.addressed_comments by api-server. A FALLBACK skip source
	// only — GitHub's resolved-thread and reply-marker state is checked first and
	// stays authoritative. Empty for a PR with no followup history, or when an
	// older api-server does not send it.
	AddressedComments []AddressedComment
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
	// AddressedComments is the structured counterpart of ReviewsAddressed
	// (which is display text), recorded so api-server can persist it on
	// pr_followup (#36865). Appended only after a reply actually landed on
	// GitHub, so it records what happened rather than what the agent claimed.
	AddressedComments []AddressedComment `json:"addressed_comments,omitempty"`
	Error             string             `json:"error,omitempty"`
	// NoOp signals that the run found nothing actionable (no unaddressed
	// comments, no CI failures) or the planner ran but produced no observable
	// change (no commit, no metadata edit, no comment reply sent). The cron
	// uses this to avoid burning the retry budget on healthy idle iterations
	// — without it, a PR that gets reviewed >2h after creation can hit the
	// iteration cap before the reviewer ever shows up.
	NoOp bool `json:"no_op,omitempty"`
	// Unresolved narrows NoOp: the run had actionable input (review comments or a
	// CI failure) but couldn't apply a change — the non-convergence case, as
	// opposed to a genuine "nothing to do". It is observability-only (the cron
	// still treats the run as a no_op, counter-neutral); the followup handler
	// surfaces it as followup_unresolved so the otherwise-invisible "couldn't
	// apply" share of no_ops is measurable. Only meaningful when NoOp is true.
	Unresolved bool `json:"unresolved,omitempty"`
	// BaseSynced reports that this run merged the base branch into the PR branch
	// and pushed it. Observability-only: the cron still classifies the run by
	// execution_status like any other, this only tags the outcome metric so
	// base syncs are separable from real fixes.
	BaseSynced bool `json:"base_synced,omitempty"`
}

// reviewComment represents a single review comment that needs to be addressed.
type reviewComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	Path string `json:"path"`
	Line int    `json:"line"`
	User string `json:"user"`
	// AuthorType is GitHub's user type — "Bot" for any GitHub App, "User"
	// otherwise. Recorded so the reply step can tell whether anyone would read
	// a reply. It is deliberately NOT used to filter what the agent sees.
	AuthorType string `json:"author_type"`
	CreatedAt  string `json:"created_at"`
	Source     string `json:"source"` // "inline", "review_body", "issue_comment"
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

	// Make origin/<base> resolvable before the agent runs. The system prompt
	// directs it to run `git log origin/<base>..HEAD` (the non-bot-author guard)
	// and base-relative diffs, but a single-branch followup clone usually lacks
	// the base ref — without this the agent burns iterations on "unknown
	// revision" errors. Best-effort; resolveBaseBranch falls back to nothing.
	baseBranch := a.resolveBaseBranch(repoInfo, prNumber)
	if baseBranch != "" {
		a.ensureBaseRefFetched(baseBranch)
	}

	// --- Step 0: Bring the branch up to date with base ---
	// Runs before the gatherers so a clean merge can short-circuit the whole
	// run below without paying for a planner pass, and so a conflicted merge is
	// already staged as work by the time the "is anything actionable?" check
	// happens. See syncWithBase for why this is a merge and not a rebase.
	sync := a.syncWithBase(baseBranch, req.Branch)

	// --- Step 1: Gather PR/MR context ---
	a.logger.Log(common.EventStepStart, fmt.Sprintf("Gathering %s context", mrTerm), map[string]any{"repo": repoInfo.FullPath, "pr_number": req.PRNumber, "branch": req.Branch})

	// What earlier runs already answered, per our own durable record (#36865).
	// A fallback only: each gatherer still consults GitHub's reply markers and
	// resolved-thread state first, and those remain authoritative — so a human
	// who un-resolves a thread gets the comment re-raised, and this set only
	// covers what GitHub could not tell us (markers past the per_page=100
	// window, or a lookup that failed open).
	dbAddressed := addressedCommentKeys(req.AddressedComments)
	if len(dbAddressed) > 0 {
		a.logger.Log(common.EventStepStart, "Loaded previously-addressed comments", map[string]any{
			"count": len(dbAddressed),
		})
	}

	prDetails := a.gatherPRDetails(ctx, repoInfo, prNumber)
	inlineComments, inlineText := a.gatherInlineComments(ctx, repoInfo, prNumber, dbAddressed)
	issueComments, issueText, answeredComments := a.gatherIssueComments(ctx, repoInfo, prNumber, dbAddressed)
	reviewBodyComments, reviewBodyText := a.gatherReviewBodyComments(ctx, repoInfo, prNumber, answeredComments)
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

	// Nothing for the planner to do. A conflicted base merge is the exception:
	// it is actionable work even with no comments and no CI failures, and it is
	// already open in the workspace, so it must fall through to the planner
	// rather than short-circuit (which would strand a half-merged tree).
	if len(pendingComments) == 0 && ciFailureLogs == "" && !sync.conflicted {
		if sync.merged {
			// The branch was behind and the merge landed cleanly. That is a real
			// change to the PR, so this is a success rather than a no_op — but it
			// needed no reasoning, so the planner never runs and the whole run
			// costs zero LLM tokens. The push re-triggers CI; if CI then fails,
			// the existing check_run webhook dispatches a fresh followup for it.
			a.logger.Log(common.EventStepComplete, "Base sync was the only work — skipping the planner", map[string]any{
				"behind_by": sync.behindBy, "commit": sync.commitHash,
			})
			return &PRFollowupResult{
				Success:    true,
				BaseSynced: true,
				CommitHash: sync.commitHash,
				Summary:    fmt.Sprintf("Merged origin/%s into the PR branch (was %d commits behind)", baseBranch, sync.behindBy),
			}, nil
		}
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
	systemPrompt := a.buildSystemPrompt(repoInfo.FullPath, prNumber, prDetails, prDiff, commentsText, ciFailureLogs, pendingComments, baseBranch, sync)

	// --- Step 3: Create tools and ReAct planner ---
	replaceTool := tools.NewReplaceToolWithWorkspace(a.workspaceDir)
	replaceTool.SetEditCorrectionService(tools.NewEditCorrectionService(a.llmClient))

	// The generic CLI tool is the agent's fallback for `gh` commands (e.g. when
	// it shells out `gh pr edit ...` directly instead of using the dedicated gh
	// tool). Without the token it runs unauthenticated and fails with
	// "gh auth login / populate GH_TOKEN". Inject the GitHub token so that
	// fallback path works. (GitLab uses glab via the provider tool below; the
	// CLI tool only injects GITHUB_TOKEN, so this is GitHub-only.)
	cliTool := tools.NewCLITool(a.workspaceDir)
	if a.provider != gitprovider.GitProviderGitLab {
		cliTool.SetGitHubToken(a.gitToken)
	}

	rawTools := []core.NBTool{
		tools.NewFileViewTool(a.workspaceDir),
		tools.NewFileFindTool(a.workspaceDir),
		replaceTool,
		tools.NewGrepTool(a.workspaceDir),
		tools.NewRipgrepTool(a.workspaceDir),
		cliTool,
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

	// Run the planner in "followup" mode so BuildGoal's termination criterion
	// is "changes committed and pushed" rather than the generic "report
	// produced". Without this the goal block (read-only/report-oriented)
	// contradicts this agent's own system prompt ("commit your work"), and the
	// goal wins — the agent investigates, writes a report, and stops without
	// ever running git commit. See planners/goal.go ("followup" case).
	ctx = tools.WithMode(ctx, "followup")

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
	// Summary deliberately does NOT default to planResult.FinalAnswer: for a
	// submit_analysis step, FinalAnswer is the planner's raw JSON dump of the
	// entire payload (see ReActPlanner.extractFinalAnswer), never a sentence
	// fit for a commit message or a PR comment. Left empty unless
	// parsedSubmit below finds something readable.
	result := &PRFollowupResult{
		Success: planResult.Status == "completed",
	}

	parsedSubmit, submitErr := parseSubmitAnalysisData(planner.GetSubmitAnalysisData())
	switch {
	case submitErr != nil:
		a.logger.Error(common.EventStepFailure, "Failed to parse submit_analysis data", submitErr, nil)
	case parsedSubmit == nil:
		a.logger.Log(common.EventStepFailure, "Planner produced no submit_analysis data", nil)
	default:
		if len(parsedSubmit.FilesModified) == 0 {
			a.logger.Log(common.EventStepComplete, "submit_analysis reported no files_modified", map[string]any{"execution_status": parsedSubmit.ExecutionStatus})
		}
		result.FilesModified = parsedSubmit.FilesModified
		result.Summary = parsedSubmit.humanSummary()
	}

	// Did the agent commit AND push? HEAD moving locally is not enough proof —
	// a local-only operation (e.g. `git merge origin/main`, or a commit that
	// was never pushed) also moves HEAD, and gets silently discarded with the
	// rest of the ephemeral workspace. Confirm the commit actually reached the
	// remote before treating the run as a success. Issue #36634 follow-up: a
	// run that locally merged origin/main (moving HEAD) without ever pushing
	// was misclassified as committed, and posted a PR comment describing a
	// commit that only ever existed in the torn-down workspace.
	// Require both pre and post HEADs to be non-empty: an empty preHead means
	// the workspace had no commits before the planner ran (defensive — Execute
	// is normally invoked on a populated clone, but the agent could in principle
	// initialize a repo via cli_tool and we don't want to count that as "fixed").
	postHead, _ := a.runCommandInDir("git", "rev-parse", "HEAD")
	postHead = strings.TrimSpace(postHead)
	committed := preHead != "" && postHead != "" && postHead != preHead && a.headPushedToRemote(postHead, req.Branch)

	// Commit-enforcement: if the agent left real edits uncommitted, commit and
	// push them deterministically instead of re-prompting the LLM to run git
	// itself. Live testing repeatedly showed the LLM does not reliably execute
	// "git add && git commit && git push" even with explicit instructions and
	// a focused retry pass — in the worst observed case it ran some other git
	// operation instead and left the sandbox with hundreds of unrelated files
	// deleted and a corrupted shallow-clone object, never committing anything.
	// The git mechanics themselves are simple and deterministic (verified by
	// hand, outside the LLM loop entirely: clone, identity, add, commit, push
	// all work every time), so the code does them directly. This also removes
	// the LLM's opportunity to reach for a destructive git command while
	// trying to recover from its own stuck commit attempt.
	// A base merge left open by syncWithBase is completed — or abandoned — here,
	// and deliberately NOT through autoCommitOrDiscard below. That helper
	// hard-resets any working tree touching more than autoCommitMaxFiles (15)
	// files; a base merge routinely exceeds that with content git itself
	// produced, so routing one through it would silently discard the merge and
	// abort it, leaving the PR as stuck as before.
	mergeAbandoned, mergeCommitted := false, false
	if a.mergeInProgress() {
		var mergeHead string
		mergeCommitted, mergeHead = a.finalizeMerge(req.Branch)
		if mergeCommitted {
			committed = true
			postHead = mergeHead
			result.Success = true
			sync.merged = true
		} else {
			mergeAbandoned = true
		}
	} else if !committed {
		var autoCommitted bool
		autoCommitted, postHead = a.autoCommitOrDiscard(preHead, req.Branch, result.Summary, mrTerm, prNumber)
		if autoCommitted {
			committed = true
			result.Success = true
		}
	}
	result.BaseSynced = sync.merged

	if committed {
		// Never sanitize a merge commit. sanitizeCommitMessage amends and
		// force-pushes, and a base sync must never force-push (that is the whole
		// reason this merges instead of rebasing). Git's own "Merge branch ..."
		// message is also already a real sentence, so there is nothing to fix.
		if !mergeCommitted {
			postHead = a.sanitizeCommitMessage(postHead, result.Summary, mrTerm, prNumber)
		}
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
		responses = a.extractCommentResponses(parsedSubmit)
		// Build source and automation lookups from pending comments
		commentSource := make(map[int64]string)
		commentIsAutomation := make(map[int64]bool)
		hasInline := false
		for _, c := range pendingComments {
			commentSource[c.ID] = c.Source
			commentIsAutomation[c.ID] = a.isAutomationComment(c.AuthorType, c.Body)
			if c.Source == "inline" {
				hasInline = true
			}
		}
		// Fetched once per run, only if needed: maps an inline comment to its
		// review thread's node ID, so a successful reply can also resolve the
		// thread on GitHub (see the "inline" case below).
		var commentThreadID map[int64]string
		if hasInline {
			commentThreadID = a.reviewThreadIDsByComment(repoInfo, prNumber)
		}

		repliedCount := 0
		skippedUnverified := 0
		skippedByAgent := 0
		skippedAutomation := 0
		for _, resp := range responses {
			if !resp.ShouldReply {
				skippedByAgent++
				continue
			}
			// Don't open a new top-level comment just to answer a tool.
			//
			// Scoped to non-inline sources on purpose. An inline reply is
			// threaded under the review comment: GitHub tracks its resolution,
			// and the human reading the PR sees whether each point was fixed or
			// declined — so replying to gemini-code-assist or coderabbitai there
			// is worth doing, and the InReplyToID check already stops repeats.
			// A reply to a top-level automation comment is a brand-new comment in
			// the conversation that no one asked for; PR #35094 collected nine of
			// them reading "Acknowledged." and "Automated labeler comment."
			//
			// The comment is still gathered and acted on either way — only this
			// reply is dropped.
			if commentSource[resp.CommentID] != "inline" && commentIsAutomation[resp.CommentID] {
				skippedAutomation++
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
			switch source := commentSource[resp.CommentID]; source {
			case "inline":
				replyErr = a.replyToComment(repoInfo, prNumber, resp.CommentID, replyBody)
				if replyErr == nil {
					if threadID, ok := commentThreadID[resp.CommentID]; ok {
						if rerr := a.resolveReviewThread(threadID); rerr != nil {
							a.logger.Error(common.EventStepFailure, "Failed to resolve review thread", rerr, map[string]any{"comment_id": resp.CommentID})
						}
					}
				}
			default:
				// issue_comment and review_body: post a top-level issue comment as
				// reply. Stamp which comment it answers — a standalone comment has
				// no thread linkage, so this marker is the only record that stops
				// the next run from replying to the same comment again.
				//
				// An empty source means the agent answered a comment id we never
				// gathered (extractCommentResponses does not validate ids against
				// the pending set). Post the reply as before, but without a marker:
				// a marker naming no source cannot be parsed back, and writing one
				// would put unmatchable noise in the PR.
				body := replyBody
				if source != "" {
					body += "\n\n" + followupReplyMarker(source, resp.CommentID)
				}
				replyErr = a.postIssueComment(repoInfo, prNumber, body)
			}
			if replyErr != nil {
				a.logger.Error(common.EventStepFailure, "Failed to reply to comment", replyErr, map[string]any{"comment_id": resp.CommentID, "source": commentSource[resp.CommentID]})
			} else {
				repliedCount++
				result.ReviewsAddressed = append(result.ReviewsAddressed, fmt.Sprintf("#%d: %s", resp.CommentID, resp.Action))
				// Record it durably (#36865). Deliberately inside the success
				// branch: a reply that failed to post leaves the comment genuinely
				// unanswered, and recording it would suppress the retry. Skipped
				// for a comment we never gathered (empty source) — an entry with no
				// source cannot be matched back to anything on a later run.
				if source := commentSource[resp.CommentID]; source != "" {
					result.AddressedComments = append(result.AddressedComments, AddressedComment{
						Source:      source,
						CommentID:   resp.CommentID,
						Action:      resp.Action,
						AddressedAt: time.Now().UTC(),
					})
				}
			}
		}
		a.logger.Log(common.EventStepComplete, "Posted replies", map[string]any{
			"replied":            repliedCount,
			"skipped_unverified": skippedUnverified,
			"skipped_by_agent":   skippedByAgent,
			"skipped_automation": skippedAutomation,
			"total":              len(pendingComments),
		})
		result.CommentPosted = repliedCount > 0
	}

	// --- Step 6.5: Don't leave a human reviewer in silence ---
	// If there were open comments but the agent produced NO structured
	// comment_responses at all (the force-submit / ran-out-of-steps path — it
	// never got to triage), and it changed nothing, the reviewer otherwise gets
	// zero signal that the bot even looked. Post ONE honest status notice.
	//
	// This is deliberately NOT a fabricated per-comment "fixed" reply (we never
	// claim a change we didn't make — see extractCommentResponses): it's a
	// truthful top-level "couldn't auto-resolve this run". It does NOT set
	// CommentPosted, so the run still counts as a no_op and the cron keeps
	// retrying; an idempotency marker stops that retry loop from re-posting.
	if len(pendingComments) > 0 && len(responses) == 0 && !agentChangedSomething {
		if a.hasExistingFollowupNotice(repoInfo, prNumber, pendingComments) {
			a.logger.Log(common.EventStepComplete, "Non-convergence notice already present for this pending comment set — skipping", nil)
		} else if err := a.postIssueComment(repoInfo, prNumber, a.buildNonConvergenceNotice(result.Summary, pendingComments)); err != nil {
			a.logger.Error(common.EventStepFailure, "Failed to post non-convergence notice", err, nil)
		} else {
			a.logger.Log(common.EventStepComplete, "Posted non-convergence notice", nil)
		}
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
		// Distinguish "couldn't apply" from "nothing to do": if there was
		// actionable input (review comments, a CI failure, or a base merge the
		// agent failed to resolve) yet we produced no change, this no_op is
		// really a non-convergence. Observability-only — the cron still treats
		// it as a counter-neutral no_op, so the PR stays eligible for a retry
		// once the conflict is easier or a human resolves it.
		result.Unresolved = len(pendingComments) > 0 || ciFailureLogs != "" || mergeAbandoned
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

// headPushedToRemote reports whether head is actually visible on origin's
// branch, not just moved locally. Queries the remote directly with
// `git ls-remote` rather than trusting the local remote-tracking ref, so it
// can't be fooled by a stale cache — the PR's real state is only what's on
// the remote. Fails closed (false) on any lookup error.
func (a *PRFollowupAgent) headPushedToRemote(head, branch string) bool {
	out, err := a.runCommandInDir("git", "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		a.logger.Error(common.EventStepFailure, "Failed to verify push reached remote", err, map[string]any{"branch": branch})
		return false
	}
	fields := strings.Fields(out)
	return len(fields) > 0 && fields[0] == head
}

// autoCommitOrDiscard deterministically commits and pushes a dirty working
// tree, or discards it back to preHead if the diff is too large to trust as
// a real followup fix (see autoCommitMaxFiles). Returns whether the commit
// landed on the remote, and the resulting HEAD (unchanged from preHead if
// discarded or if nothing was pushed).
func (a *PRFollowupAgent) autoCommitOrDiscard(preHead, branch, summary, mrTerm, prNumber string) (committed bool, head string) {
	dirty, _ := a.runCommandInDir("git", "status", "--porcelain")
	dirty = strings.TrimSpace(dirty)
	if dirty == "" {
		return false, preHead
	}

	changedFiles := len(strings.Split(dirty, "\n"))
	if changedFiles > autoCommitMaxFiles {
		a.logger.Error(common.EventStepFailure, "Refusing to auto-commit — working tree touches too many files to be a real followup fix; discarding", nil, map[string]any{
			"changed_files": changedFiles,
			"limit":         autoCommitMaxFiles,
			"status":        dirty,
		})
		if _, err := a.runCommandInDir("git", "reset", "--hard", preHead); err != nil {
			a.logger.Error(common.EventStepFailure, "Failed to reset workspace after refusing an oversized diff", err, nil)
		}
		if _, err := a.runCommandInDir("git", "clean", "-fd"); err != nil {
			a.logger.Error(common.EventStepFailure, "Failed to clean workspace after refusing an oversized diff", err, nil)
		}
		return false, preHead
	}

	msg := commitSubject(summary, mrTerm, prNumber)
	if _, err := a.runCommandInDir("git", "add", "-A"); err != nil {
		a.logger.Error(common.EventStepFailure, "Auto-commit: git add failed", err, map[string]any{"pr_number": prNumber})
	} else if _, err := a.runCommandInDir("git", "commit", "-m", msg); err != nil {
		a.logger.Error(common.EventStepFailure, "Auto-commit: git commit failed", err, map[string]any{"pr_number": prNumber})
	} else if _, err := a.runCommandInDir("git", "push"); err != nil {
		a.logger.Error(common.EventStepFailure, "Auto-commit: git push failed", err, map[string]any{"pr_number": prNumber})
	}

	postHead, _ := a.runCommandInDir("git", "rev-parse", "HEAD")
	postHead = strings.TrimSpace(postHead)
	if preHead != "" && postHead != "" && postHead != preHead && a.headPushedToRemote(postHead, branch) {
		return true, postHead
	}
	return false, postHead
}

// commitSubject builds a one-line, human-readable commit subject from the
// best available run summary: the first sentence, capped to a reasonable
// subject-line length — never the raw multi-paragraph description crammed in
// whole, and never empty.
func commitSubject(summary, mrTerm, prNumber string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Sprintf("fix: automated followup on %s #%s", mrTerm, prNumber)
	}
	if idx := strings.IndexAny(summary, ".\n"); idx > 0 && idx < len(summary)-1 {
		summary = summary[:idx+1]
	}
	const maxSubjectLen = 100
	if len(summary) > maxSubjectLen {
		summary = strings.TrimSpace(summary[:maxSubjectLen]) + "…"
	}
	return summary
}

// commitMessageLooksBad reports whether a commit message looks like a raw
// data dump rather than a human sentence. Observed live: the LLM sometimes
// commits with the entire submit_analysis JSON payload as -m instead of
// composing a real message.
func commitMessageLooksBad(msg string) bool {
	msg = strings.TrimSpace(msg)
	return msg == "" || strings.HasPrefix(msg, "{") || strings.HasPrefix(msg, "[")
}

// sanitizeCommitMessage rewrites HEAD's commit message if it looks like a raw
// data dump, and re-pushes the amended commit. A no-op (cheap: one git log
// call) when the message already looks like a real sentence — this exists
// for commits the LLM made on its own during the main pass, before
// autoCommitOrDiscard (which already composes a clean message) ever runs.
// Returns the possibly-updated HEAD.
func (a *PRFollowupAgent) sanitizeCommitMessage(head, summary, mrTerm, prNumber string) string {
	msg, err := a.runCommandInDir("git", "log", "-1", "--format=%B")
	if err != nil || !commitMessageLooksBad(msg) {
		return head
	}
	clean := commitSubject(summary, mrTerm, prNumber)
	if _, err := a.runCommandInDir("git", "commit", "--amend", "-m", clean); err != nil {
		a.logger.Error(common.EventStepFailure, "Failed to amend a malformed commit message", err, nil)
		return head
	}
	newHead, _ := a.runCommandInDir("git", "rev-parse", "HEAD")
	newHead = strings.TrimSpace(newHead)
	if _, err := a.runCommandInDir("git", "push", "--force-with-lease"); err != nil {
		a.logger.Error(common.EventStepFailure, "Failed to push amended commit message", err, nil)
		return head
	}
	a.logger.Log(common.EventStepComplete, "Rewrote a malformed commit message", map[string]any{"old_head": head, "new_head": newHead})
	return newHead
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

// resolveBaseBranch returns the PR/MR's base (target) ref name from the
// provider. Returns "" if the lookup fails.
func (a *PRFollowupAgent) resolveBaseBranch(repoInfo *gitprovider.RepoInfo, prNumber string) string {
	return a.providerJQQuery(repoInfo, prNumber, "baseRefName", ".baseRefName", ".target_branch", "resolve base branch")
}

// ensureBaseRefFetched makes `origin/<base>` resolvable in the workspace clone.
// The followup workspace is often a single-branch / shallow clone of the head
// branch, so `origin/<base>` does not exist — yet the system prompt directs the
// agent to run `git log origin/<base>..HEAD` (the non-bot-author safety check)
// before rewriting history, and base-relative diffs. Without the ref those
// commands fail with "unknown revision", and the agent burns iterations on the
// error instead of doing the work. Fetching the base ref up front is cheap and
// best-effort: a failure here just leaves the agent where it was.
func (a *PRFollowupAgent) ensureBaseRefFetched(base string) {
	if base == "" {
		return
	}
	if !isPlainBranchName(base) {
		a.logger.Log(common.EventStepFailure, "Skipping base-ref fetch: unsafe branch name", map[string]any{"base": base})
		return
	}
	refspec := fmt.Sprintf("%s:refs/remotes/origin/%s", base, base)
	if _, err := a.runCommandInDir("git", "fetch", "--no-tags", "--depth", "50", "origin", refspec); err != nil {
		// Retry without --depth in case the clone is already complete (a shallow
		// fetch onto a full clone can error on some git versions).
		if _, err2 := a.runCommandInDir("git", "fetch", "--no-tags", "origin", refspec); err2 != nil {
			a.logger.Log(common.EventStepFailure, "Failed to fetch base ref (non-fatal)", map[string]any{"base": base, "error": err2.Error()})
			return
		}
	}
	a.logger.Log(common.EventStepComplete, "Fetched base ref for safety checks", map[string]any{"base": base})
}

// safeBranchNameRe allowlists the characters real git ref names use. An
// allowlist is the right shape here (denying a fixed set of "bad" characters
// always misses the next one); requiring alnum at both ends also matches
// git's own ref-format rules (a ref may not start or end with '.', and can
// never start with '-').
var safeBranchNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._/-]*[A-Za-z0-9])?$`)

// isPlainBranchName rejects anything that isn't a plain branch name, so a value
// resolved from the provider can't be coerced into a flag (a leading "-") or an
// extra refspec argument when it is passed to git. There is no shell involved —
// runCommandInDir uses exec.Command with an argument slice — so this guards
// git's own argument parsing, not shell metacharacters. ".." is rejected
// explicitly even though the character class allows it standalone: git
// forbids it anywhere in a ref name (it collides with range syntax), and the
// allowlist alone can't express "no consecutive dots."
func isPlainBranchName(name string) bool {
	return safeBranchNameRe.MatchString(name) && !strings.Contains(name, "..")
}

// baseSyncResult reports what syncWithBase did to the PR branch.
//
// Exactly one of merged / conflicted / skipReason is meaningful on any given
// run; behindBy is set whenever the count was readable, including when the
// answer was zero.
type baseSyncResult struct {
	behindBy int
	// merged means the base merged cleanly AND the merge commit reached the
	// remote. A local-only merge is never reported as merged — the workspace is
	// ephemeral, so an unpushed commit is indistinguishable from no commit
	// (the #36634 lesson that headPushedToRemote exists for).
	merged     bool
	commitHash string
	// conflicted means the merge is still IN PROGRESS in the workspace, with
	// unmerged paths for the agent to resolve. The caller must reach
	// finalizeMerge before returning, or the run leaves a half-merged tree.
	conflicted    bool
	conflictFiles []string
	// skipReason names why no merge was attempted (or why one was rolled back).
	// Empty on both the clean-merge and the conflicted path.
	skipReason string
}

// hasMergeBase reports whether HEAD and ref share an ancestor the workspace can
// actually see. On a shallow clone this is the question that matters: `git
// merge` fails outright with "refusing to merge unrelated histories" when the
// common ancestor sits outside the shallow window.
func (a *PRFollowupAgent) hasMergeBase(ref string) bool {
	_, err := a.runCommandInDir("git", "merge-base", "HEAD", ref)
	return err == nil
}

// ensureMergeBase widens the shallow window until HEAD and origin/<base> share
// a visible ancestor, and reports whether they do.
//
// This is not a defensive nicety — it is load-bearing. The followup workspace is
// cloned `--depth 50 --branch <head>` (performFollowupAnalysis), and
// ensureBaseRefFetched brings the base ref in at depth 50 as well. When a PR's
// fork point is further back than that on either side, no merge base is visible
// and every merge attempt fails. Deepening is skipped entirely on a complete
// clone, where a missing merge base means the histories really are unrelated
// and no amount of fetching will help.
func (a *PRFollowupAgent) ensureMergeBase(base, branch string) bool {
	ref := "origin/" + base
	if a.hasMergeBase(ref) {
		return true
	}
	if shallow, err := a.runCommandInDir("git", "rev-parse", "--is-shallow-repository"); err != nil ||
		strings.TrimSpace(shallow) != "true" {
		a.logger.Log(common.EventStepFailure, "No merge base with the base branch and the clone is complete — not a shallow-window problem", map[string]any{"base": base})
		return false
	}

	baseRefspec := fmt.Sprintf("%s:refs/remotes/origin/%s", base, base)

	// Round 1: widen both sides to a deeper window. Cheap relative to full
	// history and sufficient for any PR forked within the last few hundred
	// commits, which is effectively all of them.
	_, _ = a.runCommandInDir("git", "fetch", "--no-tags", "--depth", "500", "origin", baseRefspec)
	_, _ = a.runCommandInDir("git", "fetch", "--no-tags", "--depth", "500", "origin", branch)
	if a.hasMergeBase(ref) {
		a.logger.Log(common.EventStepComplete, "Deepened shallow clone to expose a merge base", map[string]any{"base": base, "depth": 500})
		return true
	}

	// Round 2: full history. Only reached when the fork point is more than 500
	// commits back, so the cost is paid on a genuinely long-lived PR.
	if _, err := a.runCommandInDir("git", "fetch", "--no-tags", "--unshallow", "origin"); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to unshallow clone while looking for a merge base", map[string]any{"base": base, "error": err.Error()})
		return false
	}
	_, _ = a.runCommandInDir("git", "fetch", "--no-tags", "origin", baseRefspec)
	if a.hasMergeBase(ref) {
		a.logger.Log(common.EventStepComplete, "Unshallowed clone to expose a merge base", map[string]any{"base": base})
		return true
	}
	a.logger.Log(common.EventStepFailure, "No merge base with the base branch even after unshallowing", map[string]any{"base": base})
	return false
}

// syncWithBase brings the PR branch up to date with its base branch when it has
// fallen behind, and reports what happened.
//
// Why this exists: being out-of-date was not an actionable signal anywhere in
// the followup subsystem (#36864). extractGithubPRSignal ignores `push`, so a
// base branch moving forward fires no webhook; and while the hourly cron does
// still dispatch a followup for the PR, Execute short-circuited to a no_op
// whenever there were no open comments and no CI failures. An auto-PR with
// clean CI therefore drifted behind base until it conflicted, and nothing
// touched it until the 3-day stale retirement.
//
// Merge, never rebase. A rebase would need --force-with-lease on every sync,
// which this agent's own non-negotiable safety rule already forbids whenever a
// non-bot author appears in origin/<base>..HEAD — i.e. on exactly the PRs a
// human has pushed to. A merge commit needs no force push and is always safe.
//
// Deterministic and outside the LLM loop, for the same reason
// autoCommitOrDiscard is: the git mechanics are simple and always work, while
// an LLM asked to run them reaches for destructive recovery commands when its
// first attempt sticks.
func (a *PRFollowupAgent) syncWithBase(base, branch string) baseSyncResult {
	if base == "" || branch == "" {
		return baseSyncResult{skipReason: "unknown_ref"}
	}
	// Both refs reach git as arguments below (fetch refspecs, a push target).
	// Guard them the same way ensureBaseRefFetched guards the base ref, so a
	// branch name that looks like a flag can never be parsed as one.
	if !isPlainBranchName(base) || !isPlainBranchName(branch) {
		a.logger.Log(common.EventStepFailure, "Skipping base sync: unsafe branch name", map[string]any{"base": base, "branch": branch})
		return baseSyncResult{skipReason: "unsafe_ref"}
	}
	ref := "origin/" + base

	if !a.ensureMergeBase(base, branch) {
		return baseSyncResult{skipReason: "no_merge_base"}
	}

	out, err := a.runCommandInDir("git", "rev-list", "--count", "HEAD.."+ref)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to count commits behind base", map[string]any{"base": base, "error": err.Error()})
		return baseSyncResult{skipReason: "behind_count_failed"}
	}
	behind, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		return baseSyncResult{skipReason: "behind_count_unparseable"}
	}
	if behind == 0 {
		return baseSyncResult{}
	}

	a.logger.Log(common.EventStepStart, "PR branch is behind its base — merging base in", map[string]any{
		"base": base, "behind_by": behind,
	})

	if _, mergeErr := a.runCommandInDir("git", "merge", "--no-edit", ref); mergeErr != nil {
		conflicts := a.unmergedPaths()
		if len(conflicts) == 0 {
			// Failed for a reason other than conflicts (a dirty tree, a hook, a
			// bad ref). Leave nothing half-done — a merge state the agent was
			// never told about would be finalized blindly by finalizeMerge.
			_, _ = a.runCommandInDir("git", "merge", "--abort")
			a.logger.Log(common.EventStepFailure, "Base merge failed with no conflicts to resolve — rolled back", map[string]any{
				"base": base, "error": mergeErr.Error(),
			})
			return baseSyncResult{behindBy: behind, skipReason: "merge_failed"}
		}
		a.logger.Log(common.EventStepComplete, "Base merge conflicts — handing resolution to the agent", map[string]any{
			"base": base, "conflict_files": conflicts,
		})
		return baseSyncResult{behindBy: behind, conflicted: true, conflictFiles: conflicts}
	}

	head, pushed := a.pushCurrentHead(branch)
	if !pushed {
		return baseSyncResult{behindBy: behind, skipReason: "push_failed"}
	}
	a.logger.Log(common.EventStepComplete, "Merged base into the PR branch", map[string]any{
		"base": base, "behind_by": behind, "commit": head,
	})
	return baseSyncResult{behindBy: behind, merged: true, commitHash: head}
}

// unmergedPaths lists the files git left with conflict markers.
func (a *PRFollowupAgent) unmergedPaths() []string {
	out, err := a.runCommandInDir("git", "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files
}

// stageResolvedConflicts stages any unmerged file whose content no longer
// carries a conflict marker. unmergedPaths reports from the git INDEX, not
// file content — the agent typically resolves conflicts by editing the file
// directly through the replace/write tools, and a resolution that never runs
// `git add` afterward is real but still shows up as unresolved. This closes
// that gap. Best-effort and conservative: a file is staged only when it is
// unambiguously clean, so a genuinely half-resolved file (still carrying a
// marker) is left alone and still aborts the merge in finalizeMerge below,
// exactly as before.
func (a *PRFollowupAgent) stageResolvedConflicts() {
	for _, file := range a.unmergedPaths() {
		content, err := os.ReadFile(filepath.Join(a.workspaceDir, file))
		if err != nil {
			continue
		}
		if bytes.Contains(content, []byte("<<<<<<<")) ||
			bytes.Contains(content, []byte("=======")) ||
			bytes.Contains(content, []byte(">>>>>>>")) {
			continue
		}
		if _, err := a.runCommandInDir("git", "add", file); err != nil {
			a.logger.Error(common.EventStepFailure, "Failed to auto-stage a resolved conflict file", err, map[string]any{"file": file})
			continue
		}
		a.logger.Log(common.EventStepComplete, "Auto-staged a resolved conflict file", map[string]any{"file": file})
	}
}

// mergeInProgress reports whether a merge started by syncWithBase is still open
// in the workspace.
func (a *PRFollowupAgent) mergeInProgress() bool {
	_, err := a.runCommandInDir("git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil
}

// pushCurrentHead pushes HEAD to the PR branch and confirms it reached the
// remote, returning the pushed SHA.
//
// A rejected push means someone else moved the PR branch under us. The recovery
// is to discard our work and let the next cron tick or webhook redo it against
// the new tip — never to force-push, which would silently drop whatever they
// pushed. The reset also matters for correctness downstream: leaving an
// unpushable commit in the workspace would make postHead != preHead and fool
// the run into reporting a change that only ever existed locally.
func (a *PRFollowupAgent) pushCurrentHead(branch string) (head string, ok bool) {
	if _, err := a.runCommandInDir("git", "push", "origin", "HEAD:"+branch); err != nil {
		a.logger.Log(common.EventStepFailure, "Push rejected — PR branch moved under us; discarding and deferring to the next run", map[string]any{
			"branch": branch, "error": err.Error(),
		})
		if _, ferr := a.runCommandInDir("git", "fetch", "--no-tags", "origin", branch); ferr == nil {
			if _, rerr := a.runCommandInDir("git", "reset", "--hard", "origin/"+branch); rerr != nil {
				a.logger.Error(common.EventStepFailure, "Failed to reset workspace after a rejected push", rerr, map[string]any{"branch": branch})
			}
		}
		return "", false
	}
	head, _ = a.runCommandInDir("git", "rev-parse", "HEAD")
	head = strings.TrimSpace(head)
	if head == "" || !a.headPushedToRemote(head, branch) {
		return head, false
	}
	return head, true
}

// finalizeMerge completes or abandons the base merge that syncWithBase left open
// for the agent to resolve, returning whether a merge commit reached the remote.
//
// This deliberately does NOT go through autoCommitOrDiscard. That helper counts
// `git status --porcelain` lines and hard-resets any tree touching more than
// autoCommitMaxFiles (15) — a guard against a runaway agent deleting hundreds of
// unrelated files. A base merge routinely touches more than 15 files with
// content git itself produced, so routing one through that path would silently
// discard the merge AND abort it, leaving the PR exactly as stuck as before.
//
// If the agent did not resolve everything, the merge is aborted rather than
// committed: a half-resolved tree still carrying conflict markers must never
// reach the PR.
func (a *PRFollowupAgent) finalizeMerge(branch string) (committed bool, head string) {
	// git reports a path as unmerged from the INDEX, not the file content — the
	// agent's conflict resolution normally goes through replace/write tools that
	// edit the file directly, and if it never runs `git add` afterward the
	// resolution is real but git still shows the path as unresolved. Stage any
	// unmerged file that no longer carries a conflict marker before the real
	// check below, so a resolution that's actually correct doesn't get thrown
	// away because of a missed `git add`.
	a.stageResolvedConflicts()

	if unresolved := a.unmergedPaths(); len(unresolved) > 0 {
		a.logger.Log(common.EventStepFailure, "Agent left merge conflicts unresolved — aborting the base merge", map[string]any{
			"unresolved_files": unresolved,
		})
		if _, err := a.runCommandInDir("git", "merge", "--abort"); err != nil {
			a.logger.Error(common.EventStepFailure, "Failed to abort the base merge", err, nil)
		}
		return false, ""
	}

	// A failed `git add` means the index itself is unhealthy (a stale
	// index.lock, a permissions problem). Committing on top of that would
	// produce a merge commit missing whatever the agent fixed outside the
	// conflicted files, so abort and let the next tick redo the merge from a
	// clean workspace rather than push a quietly incomplete merge.
	if _, err := a.runCommandInDir("git", "add", "-A"); err != nil {
		a.logger.Error(common.EventStepFailure, "Merge finalize: git add failed — aborting the base merge", err, nil)
		if _, aerr := a.runCommandInDir("git", "merge", "--abort"); aerr != nil {
			a.logger.Error(common.EventStepFailure, "Failed to abort the base merge", aerr, nil)
		}
		return false, ""
	}
	if _, err := a.runCommandInDir("git", "commit", "--no-edit"); err != nil {
		a.logger.Error(common.EventStepFailure, "Merge finalize: git commit failed — aborting the base merge", err, nil)
		if _, aerr := a.runCommandInDir("git", "merge", "--abort"); aerr != nil {
			a.logger.Error(common.EventStepFailure, "Failed to abort the base merge", aerr, nil)
		}
		return false, ""
	}

	head, pushed := a.pushCurrentHead(branch)
	if !pushed {
		return false, head
	}
	// Record the size. autoCommitMaxFiles deliberately does not apply to a merge
	// (see the doc comment), so this log is the only signal if a merge ever does
	// carry an implausible number of files.
	changed, _ := a.runCommandInDir("git", "diff", "--name-only", "HEAD^", "HEAD")
	a.logger.Log(common.EventStepComplete, "Resolved base merge conflicts and pushed", map[string]any{
		"branch":        branch,
		"commit":        head,
		"changed_files": len(strings.Fields(changed)),
	})
	return true, head
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
// dbAddressed is the fallback skip set from pr_followup.addressed_comments
// (#36865), applied alongside — never instead of — the reply-marker and
// resolved-thread checks below, both of which stay authoritative.
func (a *PRFollowupAgent) gatherInlineComments(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string, dbAddressed map[string]bool) ([]reviewComment, string) {
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

	// The reply-marker check above only catches replies WE posted in our own
	// format. It misses: a human resolving the thread without replying, a
	// human or bot reply worded differently (e.g. "Fixed in <sha>." predates
	// the "Automated Followup" convention), and any reply GitHub itself
	// doesn't surface here. GitHub's real resolution state — set by whoever
	// clicks "Resolve conversation" — is authoritative over all of that, but
	// it isn't in this REST response at all; only GraphQL exposes it. Without
	// this, already-resolved comments come back as "pending" on every run
	// forever, burning the ReAct step budget on re-litigating closed threads
	// before ever reaching a genuinely new one (issue #36629).
	resolvedThreadCommentIDs := a.fetchResolvedInlineCommentIDs(repoInfo, prNumber)

	var unaddressed []reviewComment
	for _, c := range rawComments {
		// Skip replies (we only process top-level review comments)
		if c.InReplyToID != nil {
			continue
		}
		// Skip comments we've already replied to, whose thread is resolved, or
		// that our own durable record says a previous run answered.
		if inlineCommentAddressed(c.ID, repliedCommentIDs, resolvedThreadCommentIDs, dbAddressed) {
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

// reviewThreadsGraphQLQuery fetches every review thread on a PR — its node
// ID (needed to resolve it), resolution state, and the database IDs of the
// comments in it (needed to map a REST comment ID back to its thread).
const reviewThreadsGraphQLQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          comments(first: 100) { nodes { databaseId } }
        }
      }
    }
  }
}`

// reviewThreadsGraphQLResponse is the shape of GitHub's GraphQL response for
// a PR's review threads, used by fetchResolvedInlineCommentIDs to read each
// thread's resolution state and the database IDs of the comments in it.
type reviewThreadsGraphQLResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments   struct {
							Nodes []struct {
								DatabaseID int64 `json:"databaseId"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// fetchResolvedInlineCommentIDs returns the database IDs of every inline
// review comment whose GitHub review thread is resolved. GitHub-only: the
// REST endpoint gatherInlineComments calls has no resolution field, so this
// state is only reachable via GraphQL's reviewThreads.isResolved. GitLab
// exposes `resolved` directly on each note via REST, so this doesn't apply
// there — GitLab keeps relying on the reply-marker check alone.
//
// Fails open on any error (empty map, nothing treated as resolved): the
// worst case is identical to today's behavior (a resolved comment reappears
// as pending), never a false suppression of a genuinely open one.
func (a *PRFollowupAgent) fetchResolvedInlineCommentIDs(repoInfo *gitprovider.RepoInfo, prNumber string) map[int64]bool {
	resolved := make(map[int64]bool)
	if a.provider == gitprovider.GitProviderGitLab {
		return resolved
	}
	prNum, err := strconv.Atoi(prNumber)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse PR number for resolved-thread check", map[string]any{"error": err.Error()})
		return resolved
	}

	out, err := a.runCommandInDir("gh", "api", "graphql",
		"-f", "query="+reviewThreadsGraphQLQuery,
		"-f", "owner="+repoInfo.Owner,
		"-f", "name="+repoInfo.Repo,
		"-F", fmt.Sprintf("number=%d", prNum))
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to fetch review thread resolution state — treating none as resolved", map[string]any{"error": err.Error()})
		return resolved
	}

	var resp reviewThreadsGraphQLResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse review thread resolution response — treating none as resolved", map[string]any{"error": err.Error()})
		return resolved
	}
	return resolvedCommentIDsFromThreads(resp)
}

// resolvedCommentIDsFromThreads walks a parsed reviewThreadsGraphQLResponse and
// returns the database IDs of every comment belonging to a resolved thread.
// Split out from fetchResolvedInlineCommentIDs so this logic is testable
// without shelling out to gh.
func resolvedCommentIDsFromThreads(resp reviewThreadsGraphQLResponse) map[int64]bool {
	resolved := make(map[int64]bool)
	for _, thread := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if !thread.IsResolved {
			continue
		}
		for _, c := range thread.Comments.Nodes {
			resolved[c.DatabaseID] = true
		}
	}
	return resolved
}

// reviewThreadIDsByComment maps each inline review comment's database ID to
// its thread's GraphQL node ID, needed to resolve that thread. GitHub only
// (see fetchResolvedInlineCommentIDs); returns an empty map on GitLab or on
// any lookup error — the caller treats a missing entry as "can't resolve,
// skip it" rather than failing the reply itself.
func (a *PRFollowupAgent) reviewThreadIDsByComment(repoInfo *gitprovider.RepoInfo, prNumber string) map[int64]string {
	ids := make(map[int64]string)
	if a.provider == gitprovider.GitProviderGitLab {
		return ids
	}
	prNum, err := strconv.Atoi(prNumber)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse PR number for review thread lookup", map[string]any{"error": err.Error()})
		return ids
	}
	out, err := a.runCommandInDir("gh", "api", "graphql",
		"-f", "query="+reviewThreadsGraphQLQuery,
		"-f", "owner="+repoInfo.Owner,
		"-f", "name="+repoInfo.Repo,
		"-F", fmt.Sprintf("number=%d", prNum))
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to fetch review threads for resolution", map[string]any{"error": err.Error()})
		return ids
	}
	var resp reviewThreadsGraphQLResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse review threads for resolution", map[string]any{"error": err.Error()})
		return ids
	}
	return unresolvedThreadIDsByComment(resp)
}

// unresolvedThreadIDsByComment walks a parsed reviewThreadsGraphQLResponse and
// maps each comment in a not-yet-resolved thread to that thread's node ID.
// Already-resolved threads are omitted, making a lookup miss double as "don't
// bother resolving it again." Split out from reviewThreadIDsByComment so this
// logic is testable without shelling out to gh, mirroring
// resolvedCommentIDsFromThreads above.
func unresolvedThreadIDsByComment(resp reviewThreadsGraphQLResponse) map[int64]string {
	ids := make(map[int64]string)
	for _, thread := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if thread.IsResolved {
			continue
		}
		for _, c := range thread.Comments.Nodes {
			ids[c.DatabaseID] = thread.ID
		}
	}
	return ids
}

// resolveReviewThread marks a GitHub review thread resolved via the
// resolveReviewThread GraphQL mutation — the same action as clicking
// "Resolve conversation" in the PR UI. A threaded reply alone does not do
// this: gatherInlineComments' resolved-thread check (and a human re-reading
// the PR) both rely on the real resolution state, not on reply text.
func (a *PRFollowupAgent) resolveReviewThread(threadID string) error {
	const mutation = `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { isResolved } } }`
	_, err := a.runCommandInDir("gh", "api", "graphql", "-f", "query="+mutation, "-f", "id="+threadID)
	return err
}

// followupReplyMarkerPrefix tags a top-level reply with the id of the comment it
// answers. Inline replies are threaded, so GitHub itself records what we already
// said and gatherInlineComments can read it back. Top-level replies are
// standalone comments with no such linkage: without this marker, every run
// re-reads every open comment as unanswered and replies again. Observed on
// PR #35094, where three runs posted nine replies to the same three comments.
const followupReplyMarkerPrefix = "<!-- nb-followup-reply-to:"

// followupReplyMarkerRe parses the marker back out of one of our own comments.
var followupReplyMarkerRe = regexp.MustCompile(`<!-- nb-followup-reply-to:([a-z_]+):(\d+) -->`)

// followupReplyMarker renders the marker embedded in a top-level reply.
func followupReplyMarker(source string, commentID int64) string {
	return fmt.Sprintf("%s%s:%d -->", followupReplyMarkerPrefix, source, commentID)
}

// inlineCommentAddressed decides whether one inline review comment has already
// been dealt with, from the three independent sources that can say so.
//
// The ORDER of sources is not the point — any one of them is sufficient — but
// their RELATIONSHIP is: replied and resolved come from GitHub and are
// authoritative, while dbAddressed is our own record and is purely additive.
// It can only cause more skipping, never less, so a comment GitHub reports as
// open and unanswered still comes back as pending. That is what stops a human
// un-resolving a thread from being met with permanent silence (#36625).
//
// Split out from gatherInlineComments so this invariant is testable without
// shelling out to gh.
func inlineCommentAddressed(id int64, replied, resolved map[int64]bool, dbAddressed map[string]bool) bool {
	return replied[id] || resolved[id] || dbAddressed[answeredCommentKey("inline", id)]
}

// addressedCommentKeys turns the durable record carried on the request into the
// lookup the gatherers skip against, keyed exactly like the GitHub-derived
// answered set so the two compose (#36865).
//
// Returns nil for an empty record — a nil map reads as all-false, so every
// gatherer's skip check is a no-op when there is no history, and behaviour is
// byte-for-byte what it was before this existed.
func addressedCommentKeys(addressed []AddressedComment) map[string]bool {
	if len(addressed) == 0 {
		return nil
	}
	keys := make(map[string]bool, len(addressed))
	for _, a := range addressed {
		if a.Source == "" || a.CommentID == 0 {
			continue
		}
		keys[answeredCommentKey(a.Source, a.CommentID)] = true
	}
	return keys
}

// answeredCommentKey is the map key identifying one already-answered comment.
// Source is part of the key because issue comments and review submissions are
// separate id spaces on GitHub and can collide.
func answeredCommentKey(source string, commentID int64) string {
	return fmt.Sprintf("%s:%d", source, commentID)
}

// isAutomationComment reports whether a PR comment was written by a tool rather
// than a person.
//
// This gates ONE thing: whether to open a *new top-level comment* answering it.
// It must never gate what the agent reads, and it does not apply to inline
// replies, which are threaded under the review comment and therefore useful
// even when the reviewer is a bot. Bot-authored comments are frequently the most
// actionable input on a PR (gemini-code-assist, coderabbitai), and a labeler
// validation failure names a concrete fix. Issue #29204 removed an author filter
// from the gatherers for precisely that reason; do not reintroduce one.
//
// The system prompt already asks for should_reply=false on automation comments,
// and the agent mostly complies — but "mostly" left PR #35094 with nine replies
// reading "Acknowledged." and "Automated labeler comment." This makes the
// instruction an invariant instead of a request.
//
// Two rules, general first:
//
//  1. authorType == "Bot" — what GitHub reports for every GitHub App, so it
//     covers dependabot, renovate, codecov, sonarcloud and github-actions on any
//     repo with nothing to configure.
//  2. A configured body marker, for automation driven by a workflow using a
//     *human* personal access token, which GitHub attributes to that human so
//     rule 1 cannot see it. See AgentConfig.AutomationCommentMarkers.
//
// Rule 2 matches on a marker the tool emits rather than on the author, who is a
// real user — so a human writing *about* that tool still gets a reply.
func (a *PRFollowupAgent) isAutomationComment(authorType, body string) bool {
	if strings.EqualFold(authorType, "Bot") {
		return true
	}
	for _, marker := range a.automationCommentMarkers() {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// automationCommentMarkers splits the configured comma-separated marker list,
// dropping blanks. Falls back to the shipped default when the agent has no
// config (unit tests, and any caller that constructs the agent directly).
func (a *PRFollowupAgent) automationCommentMarkers() []string {
	raw := config.DefaultAutomationCommentMarkers
	if a.config != nil {
		raw = a.config.Agent.AutomationCommentMarkers
	}
	var markers []string
	for _, m := range strings.Split(raw, ",") {
		if m = strings.TrimSpace(m); m != "" {
			markers = append(markers, m)
		}
	}
	return markers
}

// gatherIssueComments fetches all top-level PR/issue comments (not inline review comments).
// Excludes our own "Automated Followup" markers, comments we have already replied
// to (via followupReplyMarker), and repo-automation chatter. Beyond that the agent
// decides per-comment whether engaging adds value via should_reply — we don't
// pre-filter by timestamp or author, since coarse heuristics hide actionable signal.
//
// The third return value is the set of comment keys this PR already has a reply
// for, extracted from our own comments in the same listing. gatherReviewBodyComments
// reuses it rather than re-listing, since its replies land here too.
// dbAddressed seeds the answered set from pr_followup.addressed_comments
// (#36865) — the fallback for what the marker scan below cannot see, because
// the markers have scrolled past this endpoint's per_page=100 window or the
// lookup failed. It is a pure addition: GitHub-derived markers are still read
// and still win where they exist, and the returned map (which
// gatherReviewBodyComments consumes) carries both sources.
func (a *PRFollowupAgent) gatherIssueComments(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string, dbAddressed map[string]bool) ([]reviewComment, string, map[string]bool) {
	answered := make(map[string]bool, len(dbAddressed))
	for k := range dbAddressed {
		answered[k] = true
	}

	if a.provider == gitprovider.GitProviderGitLab {
		// GitLab MR notes are a single API; inline comments are already covered
		return nil, "", answered
	}

	out, err := a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/issues/%s/comments", repoInfo.FullPath, prNumber))
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to gather issue comments", map[string]any{"error": err.Error()})
		return nil, "", answered
	}

	var rawComments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
			// Type is "Bot" for every GitHub App — the general automation signal.
			Type string `json:"type"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(out), &rawComments); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to parse issue comments", map[string]any{"error": err.Error()})
		return nil, "", answered
	}

	// First pass: recover what we already answered, from our own comments.
	// The marker's two capture groups are the source and the comment id, which
	// is exactly answeredCommentKey's "<source>:<id>" shape.
	for _, c := range rawComments {
		for _, m := range followupReplyMarkerRe.FindAllStringSubmatch(c.Body, -1) {
			answered[m[1]+":"+m[2]] = true
		}
	}

	var visible []reviewComment
	skippedAnswered := 0
	for _, c := range rawComments {
		// Skip our own automated comments — these are state we wrote, not signal.
		if strings.Contains(c.Body, "Automated Followup") || strings.Contains(c.Body, "Nudgebee Automated Followup") {
			continue
		}
		if answered[answeredCommentKey("issue_comment", c.ID)] {
			skippedAnswered++
			continue
		}
		// Authorship is recorded, not filtered on. Bot-authored comments are
		// often the most actionable input we get (gemini-code-assist,
		// coderabbitai), and a labeler validation failure names a fix we can
		// make — see issue #29204, which removed an author filter here for
		// exactly that reason. Automation only suppresses the *reply*; see
		// isAutomationComment.
		visible = append(visible, reviewComment{
			ID:         c.ID,
			Body:       c.Body,
			User:       c.User.Login,
			AuthorType: c.User.Type,
			CreatedAt:  c.CreatedAt,
			Source:     "issue_comment",
		})
	}

	if skippedAnswered > 0 {
		a.logger.Log(common.EventStepComplete, "Filtered issue comments", map[string]any{
			"skipped_already_answered": skippedAnswered,
			"remaining":                len(visible),
		})
	}

	if len(visible) == 0 {
		return nil, "", answered
	}

	var sb strings.Builder
	sb.WriteString("#### PR Discussion Comments\n\n")
	for i, c := range visible {
		fmt.Fprintf(&sb, "### Comment #%d (ID: %d, source: issue_comment) by @%s\n", i+1, c.ID, c.User)
		fmt.Fprintf(&sb, "**Comment:**\n%s\n\n", c.Body)
	}

	return visible, sb.String(), answered
}

// gatherReviewBodyComments fetches review submission body text (the top-level text of a review, not inline comments).
// answered comes from gatherIssueComments: our replies to a review body are posted
// as top-level issue comments, so that is where the reply markers live.
func (a *PRFollowupAgent) gatherReviewBodyComments(_ context.Context, repoInfo *gitprovider.RepoInfo, prNumber string, answered map[string]bool) ([]reviewComment, string) {
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
			Type  string `json:"type"`
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
		// Skip reviews we have already replied to. Same reasoning as the issue
		// gatherer: the reply is a standalone comment, so only our own marker
		// records that this review was handled.
		if answered[answeredCommentKey("review_body", r.ID)] {
			continue
		}
		// Do not filter by author; the ReAct planner triages each comment via
		// the fixed/acknowledged/wont_fix framework. See inline gatherer.

		unaddressed = append(unaddressed, reviewComment{
			ID:         r.ID,
			Body:       body,
			User:       r.User.Login,
			AuthorType: r.User.Type,
			CreatedAt:  r.CreatedAt,
			Source:     "review_body",
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

	ref := branch
	if ref == "" {
		ref = "HEAD"
	}

	var sb strings.Builder

	// Reject branch names that could lead to gh CLI argument injection
	// (`gh run list --branch` and similar take `--` to terminate flags only at the binary level,
	// but a leading `-` would still be interpreted as a flag by some subcommands).
	if branch != "" && (strings.Contains(branch, "..") || strings.HasPrefix(branch, "-")) {
		a.logger.Log(common.EventStepFailure, "Invalid branch name format", map[string]any{"branch": branch})
		return ""
	}

	// URL-path-escape ref to prevent special characters (spaces, slashes, encoded sequences)
	// from breaking the gh-api path or being interpreted as additional path segments.
	escapedRef := url.PathEscape(ref)
	checkNames, err := a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/commits/%s/check-runs", repoInfo.FullPath, escapedRef), "--jq", `.check_runs[] | select(.conclusion=="failure") | .name`)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to list failed checks", map[string]any{"error": err.Error()})
		return ""
	}

	failedChecks := strings.TrimSpace(checkNames)
	if failedChecks == "" {
		return ""
	}

	fmt.Fprintf(&sb, "### Failed Checks\n%s\n\n", failedChecks)

	runListOut, err := a.runCommandInDir("gh", "run", "list", "--branch", branch, "--status", "failure", "--limit", "5", "--json", "databaseId,name,conclusion", "--repo", repoInfo.FullPath)
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to list failed runs", map[string]any{"error": err.Error()})
		return sb.String()
	}

	var runs []struct {
		DatabaseID int64  `json:"databaseId"`
		Name       string `json:"name"`
	}
	if err := json.Unmarshal([]byte(runListOut), &runs); err != nil || len(runs) == 0 {
		return sb.String()
	}

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
			fmt.Fprintf(&sb, "```\n%s\n```\n\n", truncateIfNeeded(logOut, 3000))
		}
	}

	return sb.String()
}

func (a *PRFollowupAgent) buildSystemPrompt(repo, prNumber, prDetails, diff, comments, ciLogs string, pendingComments []reviewComment, baseBranch string, sync baseSyncResult) string {
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

	if sync.conflicted {
		fmt.Fprintf(&sb, `## Merge conflicts with %[1]s — resolve these first

This branch had fallen %[2]d commits behind `+"`%[1]s`"+`. `+"`git merge origin/%[1]s`"+` is **already in progress** in your workspace and left conflicts in these files:

%[3]s
Resolve every one of them:

1. Open each file and reconcile the `+"`<<<<<<<` / `=======` / `>>>>>>>`"+` regions. Keep this PR's intent AND the changes that landed on `+"`%[1]s`"+` — don't resolve by blindly taking one side.
2. `+"`git add <file>`"+` each resolved file.
3. Leave it there. **Do not run `+"`git commit`"+`, `+"`git merge --continue`"+`, `+"`git merge --abort`"+`, or any push.** The framework completes the merge commit and pushes it once you finish.

If you cannot resolve a file honestly, leave it conflicted and say so in submit_analysis — the framework will abort the merge rather than push a broken tree. Never delete a file to make a conflict go away.

`, baseBranch, sync.behindBy, formatConflictFileList(sync.conflictFiles))
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

You commit and push your own work. The git identity (`+"`nudgebee-bot <bot@nudgebee.com>`"+`) is already configured for this repo — just run git commands.

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

// formatConflictFileList renders conflicted paths as a markdown bullet list for
// the system prompt.
func formatConflictFileList(files []string) string {
	var sb strings.Builder
	for _, f := range files {
		fmt.Fprintf(&sb, "- `%s`\n", f)
	}
	return sb.String()
}

func truncateIfNeeded(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated]"
}

// nonConvergenceNoticeMarkerPrefix/Suffix wrap the hidden HTML comment embedded
// in the honest "couldn't auto-resolve" notice — see nonConvergenceNoticeMarker.
const nonConvergenceNoticeMarkerPrefix = "<!-- nb-followup-notice:"
const nonConvergenceNoticeMarkerSuffix = " -->"

// nonConvergenceNoticeMarker keys the idempotency marker on the specific set of
// pending comment IDs this run saw, not a bare constant. A bare constant means
// "has any notice ever been posted on this PR" — true forever after the first
// one, which silently suppresses the notice for every later run too, even one
// triggered by a brand-new, unrelated unresolved comment (issue #36625). Keying
// on the ID set means a new unresolved comment produces a new marker (so the
// notice posts), while retrying on the same still-open comment reuses the same
// marker (so it stays deduped, which is what this guard originally intended).
func nonConvergenceNoticeMarker(pendingComments []reviewComment) string {
	ids := make([]int64, len(pendingComments))
	for i, c := range pendingComments {
		ids[i] = c.ID
	}
	slices.Sort(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return nonConvergenceNoticeMarkerPrefix + strings.Join(parts, ",") + nonConvergenceNoticeMarkerSuffix
}

// buildNonConvergenceNotice composes the one-time honest status comment posted
// when the agent reviewed open comments but applied no change and produced no
// per-comment responses. It states plainly that nothing was changed — it never
// claims a fix.
func (a *PRFollowupAgent) buildNonConvergenceNotice(summary string, pendingComments []reviewComment) string {
	var sb strings.Builder
	sb.WriteString(nonConvergenceNoticeMarker(pendingComments))
	sb.WriteString("\n")
	sb.WriteString("### Nudgebee Automated Followup\n\n")
	sb.WriteString("I reviewed the open comment(s) on this PR but couldn't automatically apply a change in this run.\n\n")
	if s := strings.TrimSpace(summary); s != "" {
		fmt.Fprintf(&sb, "**What I looked at:** %s\n\n", truncateIfNeeded(s, 1500))
	}
	sb.WriteString("No code was changed. If this needs a manual edit, please apply it directly.\n")
	return sb.String()
}

// hasExistingFollowupNotice reports whether a non-convergence notice has already
// been posted on this PR/MR for this exact set of pending comments. Fails closed
// (returns true) on lookup error so a transient API failure can never cause
// repeated notices.
func (a *PRFollowupAgent) hasExistingFollowupNotice(repoInfo *gitprovider.RepoInfo, prNumber string, pendingComments []reviewComment) bool {
	var out string
	var err error
	// per_page=100 (the API max): the idempotency marker may sit anywhere in the
	// comment history, so the default page of 30 could miss it on a busy PR and
	// let the cron post a duplicate notice. 100 covers the realistic case; PRs
	// with >100 comments are not a concern this guard needs to handle.
	if a.provider == gitprovider.GitProviderGitLab {
		encodedPath := url.PathEscape(repoInfo.FullPath)
		out, err = a.runCommandInDir("glab", "api", fmt.Sprintf("projects/%s/merge_requests/%s/notes?per_page=100", encodedPath, prNumber))
	} else {
		out, err = a.runCommandInDir("gh", "api", fmt.Sprintf("repos/%s/issues/%s/comments?per_page=100", repoInfo.FullPath, prNumber))
	}
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to check for existing followup notice — assuming present", map[string]any{"error": err.Error()})
		return true
	}
	return strings.Contains(out, nonConvergenceNoticeMarker(pendingComments))
}

// buildSummaryComment creates a branded, bullet-point summary of what the followup did.
func (a *PRFollowupAgent) buildSummaryComment(result *PRFollowupResult, responses []commentResponse) string {
	var sb strings.Builder
	sb.WriteString("### Nudgebee Automated Followup\n\n")

	// Execution summary — the agent's own description of what it did
	if result.Summary != "" {
		fmt.Fprintf(&sb, "%s\n\n", result.Summary)
	}

	// No file-list/diff-stat section here — GitHub already renders that for
	// the commit itself; repeating it in the comment was pure noise (and, for
	// a large or malformed diff, actively misleading — see issue #36634).

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

// submitAnalysisData is the followup-mode shape a submit_analysis call
// produces (see goal.go's "followup" contract): execution status/summary,
// which files changed, and per-comment dispositions. Parsed once via a JSON
// round-trip from the planner's dynamically-typed payload, so callers get
// compile-time field access instead of each repeating map[string]any type
// assertions by hand.
type submitAnalysisData struct {
	ExecutionStatus  string               `json:"execution_status"`
	ExecutionSummary string               `json:"execution_summary"`
	FilesModified    []string             `json:"files_modified"`
	CommentResponses []rawCommentResponse `json:"comment_responses"`

	// Description/Answer/Title belong to the explore/fix-mode submit_analysis
	// contract, not followup mode's. The agent sometimes uses that shape
	// anyway (a schema mismatch, tracked separately), so these are read as
	// fallbacks — see humanSummary — rather than left to fall through to
	// FinalAnswer, which is the planner's raw JSON dump of the whole payload
	// and unfit for a commit message or a PR comment.
	Description string `json:"description"`
	Answer      string `json:"answer"`
	Title       string `json:"title"`
}

// humanSummary returns the best available human-readable one-or-two-sentence
// description of what this run did, trying fields in order of how well they
// fit that shape. Empty if the agent gave us nothing usable — callers must
// not fall back to the raw submit_analysis JSON in that case.
func (d *submitAnalysisData) humanSummary() string {
	if d == nil {
		return ""
	}
	for _, s := range []string{d.ExecutionSummary, d.Description, d.Answer, d.Title} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// rawCommentResponse mirrors commentResponse but keeps ShouldReply as a
// pointer so the JSON decoder can tell "field absent" apart from "explicit
// false" — extractCommentResponses defaults absence to "reply if there's
// reply text", not to false.
type rawCommentResponse struct {
	CommentID   int64  `json:"comment_id"`
	Action      string `json:"action"`
	ShouldReply *bool  `json:"should_reply"`
	Reply       string `json:"reply"`
}

// parseSubmitAnalysisData converts the planner's dynamically-typed
// submit_analysis payload (normally a map[string]any decoded from the LLM's
// JSON tool call) into submitAnalysisData via a JSON round-trip. Returns
// nil, nil when submitData is nil — the planner never called submit_analysis
// at all, which is a normal (if unwelcome) outcome, not a parse error.
func parseSubmitAnalysisData(submitData any) (*submitAnalysisData, error) {
	if submitData == nil {
		return nil, nil
	}
	raw, err := json.Marshal(submitData)
	if err != nil {
		return nil, fmt.Errorf("re-marshal submit_analysis data: %w", err)
	}
	var parsed submitAnalysisData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal submit_analysis data: %w", err)
	}
	return &parsed, nil
}

// extractCommentResponses reads the LLM's submit_analysis output for per-comment responses.
// Returns nil if the LLM didn't structure its output — we never fabricate replies,
// since posting noise (e.g. generic "Changes have been applied") on real PRs is worse than
// posting nothing.
func (a *PRFollowupAgent) extractCommentResponses(parsed *submitAnalysisData) []commentResponse {
	if parsed == nil {
		return nil
	}

	var result []commentResponse
	for _, r := range parsed.CommentResponses {
		if r.CommentID == 0 {
			continue
		}
		action := r.Action
		reply := r.Reply
		// should_reply: explicit bool from the agent. If absent, default
		// to true only when the agent gave us substantive reply text — an
		// empty reply with no explicit should_reply is treated as "skip".
		shouldReply := strings.TrimSpace(reply) != ""
		if r.ShouldReply != nil {
			shouldReply = *r.ShouldReply
		}
		result = append(result, commentResponse{
			CommentID:   r.CommentID,
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
