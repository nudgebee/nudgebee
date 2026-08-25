package agents

import (
	"nudgebee/code-analysis-agent/internal/session"
)

// githubTokenSetter and gitlabTokenSetter are the narrow slices of the gh / glab
// / cli tools this file needs. Interfaces rather than concrete types so the
// injection can be tested without reaching into another package's unexported
// fields.
type githubTokenSetter interface{ SetGitHubToken(token string) }

type gitlabTokenSetter interface{ SetGitLabToken(token string) }

// specialistProviderTools holds the credential-bearing tools a specialist owns.
// They are kept on the agent (like repoCloneTool) because the provider token
// only exists once a session is created, which is after construction — the same
// reason the repo-clone default branch is seeded at Execute time.
//
// Construct only with real tools; a typed-nil assigned to one of these
// interfaces would read as non-nil and panic on use.
type specialistProviderTools struct {
	gh   githubTokenSetter
	glab gitlabTokenSetter
	cli  githubTokenSetter
}

// applySessionCredentials injects the session's provider token into the tools
// that need it.
//
// Without this, a specialist's `gh` runs unauthenticated. Every `gh` call made
// by a specialist in a 30-day sample failed — all 44 of them, all the same
// `gh pr list --search SHA:<sha>` duplicate-PR pre-check, because the search API
// refuses unauthenticated requests. That guard has therefore never worked, which
// is a likely contributor to the duplicate PRs tracked in #35202.
//
// The token is the one the clone already uses. It does not widen what a
// specialist may do: PR/MR creation stays blocked by SetRestrictPROperations,
// so this only makes the read paths that were already offered actually work.
//
// An absent token leaves the tools untouched rather than clearing one that was
// set another way.
func (p specialistProviderTools) applySessionCredentials(sessionCtx *session.SessionContext) {
	if sessionCtx == nil || sessionCtx.Credentials == nil {
		return
	}
	token := sessionCtx.Credentials.Token
	if token == "" {
		return
	}
	if p.gh != nil {
		p.gh.SetGitHubToken(token)
	}
	if p.glab != nil {
		p.glab.SetGitLabToken(token)
	}
	if p.cli != nil {
		p.cli.SetGitHubToken(token)
	}
}
