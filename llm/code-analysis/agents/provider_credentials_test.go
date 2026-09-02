package agents

import (
	"testing"

	"nudgebee/code-analysis-agent/internal/credentials"
	"nudgebee/code-analysis-agent/internal/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTokenTool struct{ github, gitlab string }

func (f *fakeTokenTool) SetGitHubToken(t string) { f.github = t }
func (f *fakeTokenTool) SetGitLabToken(t string) { f.gitlab = t }

// Every `gh` call made by a specialist in a 30-day sample failed — all 44, all
// the same duplicate-PR pre-check — because the tool was constructed without a
// token and the search API refuses unauthenticated requests. The token lives on
// the session, so it can only be applied once a session exists.
func TestApplySessionCredentials_AuthenticatesProviderTools(t *testing.T) {
	gh, glab, cli := &fakeTokenTool{}, &fakeTokenTool{}, &fakeTokenTool{}

	specialistProviderTools{gh: gh, glab: glab, cli: cli}.
		applySessionCredentials(&session.SessionContext{
			Credentials: &credentials.ResolvedCredentials{Token: "ghs_exampletoken"},
		})

	assert.Equal(t, "ghs_exampletoken", gh.github, "gh must be authenticated")
	assert.Equal(t, "ghs_exampletoken", glab.gitlab, "glab must get the gitlab setter")
	assert.Equal(t, "ghs_exampletoken", cli.github, "cli runs gh subcommands and needs the token too")
}

// Missing or empty credentials must leave the tools untouched rather than
// clearing a token that was set some other way.
func TestApplySessionCredentials_NoCredentialsLeavesToolsAlone(t *testing.T) {
	for name, sc := range map[string]*session.SessionContext{
		"nil session":     nil,
		"nil credentials": {},
		"empty token":     {Credentials: &credentials.ResolvedCredentials{Token: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			gh := &fakeTokenTool{github: "preset"}

			require.NotPanics(t, func() {
				specialistProviderTools{gh: gh}.applySessionCredentials(sc)
			})
			assert.Equal(t, "preset", gh.github,
				"an absent session token must not clear an existing one")
		})
	}
}

// A specialist holding no provider tools must not panic.
func TestApplySessionCredentials_NoToolsIsSafe(t *testing.T) {
	require.NotPanics(t, func() {
		specialistProviderTools{}.applySessionCredentials(&session.SessionContext{
			Credentials: &credentials.ResolvedCredentials{Token: "ghs_exampletoken"},
		})
	})
}
