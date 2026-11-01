package agents

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

const (
	supersededNoticeMarker       = "<!-- nudgebee-superseded -->"
	supersededReviewNoticeMarker = "<!-- nudgebee:pr-superseded-review -->"
)

// freshFetchBase refreshes origin/base for this assessment. A stale remote
// tracking ref must never be used to retire a PR.
func (a *PRFollowupAgent) freshFetchBase(base string) bool {
	if base == "" || !isPlainBranchName(base) {
		return false
	}
	refspec := fmt.Sprintf("%s:refs/remotes/origin/%s", base, base)
	if _, err := a.runCommandInDir("git", "fetch", "--no-tags", "--depth", "50", "origin", refspec); err == nil {
		return true
	}
	if _, err := a.runCommandInDir("git", "fetch", "--no-tags", "origin", refspec); err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to freshly fetch base for superseded assessment", map[string]any{"base": base, "error": err.Error()})
		return false
	}
	return true
}

// assessContained proves that every bot-authored, non-merge PR commit can be
// reversed from a scratch index seeded with origin/base. It deliberately does
// not use a net patch: a resolving merge can drop an earlier change, which a
// later commit otherwise masks.
//
// absorbed reports that the PR head is a descendant of the fresh base with an
// identical tree and a clean worktree, i.e. the PR looks fully absorbed. Only
// then is "contained could not be proven" worth telling a human about; an
// ordinary PR whose tree still differs from base is left alone silently.
func (a *PRFollowupAgent) assessContained(ctx context.Context, base string) (contained bool, head string, absorbed bool) {
	if !a.freshFetchBase(base) {
		return false, "", false
	}
	headOut, err := a.runCommandInDir("git", "rev-parse", "HEAD")
	if err != nil {
		return false, "", false
	}
	head = strings.TrimSpace(headOut)
	if !a.gitSucceeds(ctx, "merge-base", "--is-ancestor", "origin/"+base, "HEAD") ||
		!a.gitSucceeds(ctx, "diff", "--quiet", "origin/"+base, "HEAD") {
		return false, head, false
	}
	if !a.gitSucceeds(ctx, "diff", "--quiet") || !a.gitSucceeds(ctx, "diff", "--cached", "--quiet") {
		return false, head, false
	}
	status, err := a.gitOutput(ctx, "status", "--porcelain")
	if err != nil || len(status) != 0 {
		return false, head, false
	}
	absorbed = true

	authors, err := a.runCommandInDir("git", "log", "--format=%ae", "origin/"+base+"..HEAD")
	if err != nil {
		return false, head, absorbed
	}
	emails := strings.Fields(authors)
	if len(emails) == 0 {
		return false, head, absorbed
	}
	for _, email := range emails {
		if email != "bot@nudgebee.com" {
			return false, head, absorbed
		}
	}

	commitsOut, err := a.runCommandInDir("git", "rev-list", "--no-merges", "origin/"+base+"..HEAD")
	if err != nil {
		return false, head, absorbed
	}
	commits := strings.Fields(commitsOut) // rev-list is newest first.
	if len(commits) == 0 || len(commits) > 20 {
		return false, head, absorbed
	}

	index, err := os.CreateTemp("", "nudgebee-superseded-index-*")
	if err != nil {
		return false, head, absorbed
	}
	indexPath := index.Name()
	defer func() { _ = os.Remove(indexPath) }()
	if err := index.Close(); err != nil {
		return false, head, absorbed
	}

	env := append(a.buildCLIEnv(), "GIT_INDEX_FILE="+indexPath)
	if !a.gitWithEnv(ctx, env, "read-tree", "origin/"+base) {
		return false, head, absorbed
	}
	proven := 0
	for _, commit := range commits {
		patch, err := a.gitOutput(ctx, "diff", "--binary", "--full-index", commit+"^", commit)
		if err != nil {
			return false, head, absorbed
		}
		if len(patch) == 0 {
			// An empty commit changes nothing, and `git apply` exits 128 on an
			// empty patch. It proves nothing either, so it is skipped, not counted.
			continue
		}
		if !a.gitApplyReverse(ctx, env, patch) {
			return false, head, absorbed
		}
		proven++
	}
	return proven > 0, head, absorbed
}

func (a *PRFollowupAgent) gitSucceeds(ctx context.Context, args ...string) bool {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.workspaceDir
	cmd.Env = a.buildCLIEnv()
	return cmd.Run() == nil
}

func (a *PRFollowupAgent) gitWithEnv(ctx context.Context, env []string, args ...string) bool {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.workspaceDir
	cmd.Env = env
	return cmd.Run() == nil
}

func (a *PRFollowupAgent) gitOutput(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.workspaceDir
	cmd.Env = a.buildCLIEnv()
	return cmd.Output()
}

func (a *PRFollowupAgent) gitApplyReverse(ctx context.Context, env []string, patch []byte) bool {
	cmd := exec.CommandContext(ctx, "git", "apply", "--cached", "-R")
	cmd.Dir = a.workspaceDir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(patch)
	return cmd.Run() == nil
}

// hasSupersededNotice reports whether the marker is already in the PR's
// comment history. known is false when the lookup itself failed: callers must
// then neither post nor report a verdict, since "unknown" is not "present".
// Both CLIs are asked to --paginate, which prints the pages back to back, so a
// plain substring search covers every page.
func (a *PRFollowupAgent) hasSupersededNotice(repoInfo *gitprovider.RepoInfo, prNumber, marker string) (found, known bool) {
	var out string
	var err error
	if a.provider == gitprovider.GitProviderGitLab {
		out, err = a.runCommandInDir("glab", "api", "--paginate", fmt.Sprintf("projects/%s/merge_requests/%s/notes?per_page=100", url.PathEscape(repoInfo.FullPath), prNumber))
	} else {
		out, err = a.runCommandInDir("gh", "api", "--paginate", fmt.Sprintf("repos/%s/issues/%s/comments?per_page=100", repoInfo.FullPath, prNumber))
	}
	if err != nil {
		a.logger.Log(common.EventStepFailure, "Failed to check for existing superseded notice — no verdict this run", map[string]any{"error": err.Error()})
		return false, false
	}
	// Some JSON encoders escape angle brackets, which would hide the marker.
	escaped := strings.NewReplacer("<", `\u003c`, ">", `\u003e`).Replace(marker)
	return strings.Contains(out, marker) || strings.Contains(out, escaped), true
}

// shouldAssessSuperseded is the hook's precondition: an instruction run, a run
// with comments to answer, or an open conflicted merge all have real work.
func shouldAssessSuperseded(instruction string, pendingComments int, sync baseSyncResult) bool {
	return instruction == "" && pendingComments == 0 && !sync.conflicted
}

// retireIfSuperseded only reports a successful supersession after its marker
// comment landed. Provider-side closing is intentionally left to api-server.
func (a *PRFollowupAgent) retireIfSuperseded(ctx context.Context, repoInfo *gitprovider.RepoInfo, prNumber, base string) *PRFollowupResult {
	contained, head, absorbed := a.assessContained(ctx, base)
	if contained {
		found, known := a.hasSupersededNotice(repoInfo, prNumber, supersededNoticeMarker)
		if !known {
			return nil
		}
		if !found {
			body := supersededNoticeMarker + "\n### Nudgebee Automated Followup\n\nThe bot-authored changes on this PR are already contained in the current base branch. No code was changed in this followup run."
			if err := a.postIssueComment(repoInfo, prNumber, body); err != nil {
				a.logger.Error(common.EventStepFailure, "Failed to post superseded PR notice", err, nil)
				return nil
			}
		}
		return &PRFollowupResult{Success: true, NoOp: true, Superseded: true, AssessedHeadSHA: head, Summary: "PR changes are already contained in the base branch"}
	}
	if absorbed {
		if found, known := a.hasSupersededNotice(repoInfo, prNumber, supersededReviewNoticeMarker); known && !found {
			body := supersededReviewNoticeMarker + "\n### Nudgebee Automated Followup\n\nThis PR was checked against the current base branch, but its changes could not be proven to be fully contained there. No code was changed in this followup run."
			if err := a.postIssueComment(repoInfo, prNumber, body); err != nil {
				a.logger.Error(common.EventStepFailure, "Failed to post superseded review notice", err, nil)
			}
		}
	}
	return nil
}
