package adapter

// GitProvider is the seam over the git-mutating steps of applying a code
// recommendation: cloning the target repo (Checkout), editing the manifests
// and committing them on a new branch (Commit), and opening the pull request
// (RaisePR). Production wires realGitProvider, which delegates to the existing
// package-level implementations and adds no behavior. Tests inject a fake so
// the suite needs no live repo, no token, and never mutates an external repo
// (see #31114, Class B1).
//
// Scope: this seam currently covers the GitHub flow only (it speaks in
// gitDetailFromDeployment). Routing the production ApplyRecommendation /
// commitCodeGithub call sites through it, and unifying it with the GitLab
// flow, are the immediate follow-ups in the #31114 rollout — not part of this
// pilot, which only proves the pattern and makes TestUpdateCodeCommit
// self-contained.
type GitProvider interface {
	// Checkout clones the repository to a fresh temp directory and returns its
	// path. The caller owns the directory and must remove it.
	Checkout(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment) (string, error)
	// Commit applies the recommendation's edits in dir, commits them on a new
	// branch, and returns the branch name. It performs no network I/O when
	// updateExistingPR is false.
	Commit(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment, updateExistingPR bool) (string, error)
	// RaisePR pushes branchName and opens (or updates) the pull request,
	// returning its URL.
	RaisePR(ctx AccountAdapterContext, dir, branchName string, gitDetail gitDetailFromDeployment, updateExistingPR bool, existingPRNumber int, githubBody, resolverType, githubTitle string) (string, error)
}

// realGitProvider is the production GitProvider. Each method is a thin pass
// through to the corresponding package function; it exists only to expose them
// through the seam so tests can substitute a fake.
type realGitProvider struct{}

func (realGitProvider) Checkout(ctx AccountAdapterContext, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment) (string, error) {
	return checkoutCodeRepo(ctx, request, gitDetails)
}

func (realGitProvider) Commit(ctx AccountAdapterContext, dir string, request ApplyRecommendationRequest, gitDetails gitDetailFromDeployment, updateExistingPR bool) (string, error) {
	return commitCode(ctx, dir, request, gitDetails, updateExistingPR)
}

func (realGitProvider) RaisePR(ctx AccountAdapterContext, dir, branchName string, gitDetail gitDetailFromDeployment, updateExistingPR bool, existingPRNumber int, githubBody, resolverType, githubTitle string) (string, error) {
	return raisePrForCodeRepo(ctx, dir, branchName, gitDetail, updateExistingPR, existingPRNumber, githubBody, resolverType, githubTitle)
}

// NewGitProvider returns the production GitProvider backed by real git/GitHub
// operations.
func NewGitProvider() GitProvider { return realGitProvider{} }
