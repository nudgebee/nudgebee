package agents

import "testing"

// Reproduces the live failure on the test env: tenant 4a3ea4e0 had two enabled
// GitHub integrations, and getGitCredentials returns them ordered only by
// provider — so creds[0] was a GitHub App installed on `dweley-org`, which
// covers two dummy repos. Cloning nudgebee/nudgebee-infra with that token gets a
// bare 404 "Repository not found" (GitHub hides private repos a token cannot
// see), the specialist abstains, and the run is reported as "the requested
// change is already present". The credential that does cover the repo was second
// in the slice and never tried.
func TestSelectGitCredentialPrefersTheCredentialCoveringTheRepo(t *testing.T) {
	wrongOrg := GitCredentials{
		Username: "dweley-org",
		Url:      "api.github.com",
		Password: "12345678",
		AuthType: "application",
		Provider: "github",
		Projects: []map[string]string{
			{"name": "dummy-repo", "key": "dweley-org/dummy-repo"},
			{"name": "dummy-repo1", "key": "dweley-org/dummy-repo1"},
		},
	}
	covering := GitCredentials{
		Username: "VanshikaR7",
		Url:      "api.github.com",
		Password: "ghp_token",
		AuthType: "token",
		Provider: "github",
		Projects: []map[string]string{
			{"name": "forager", "key": "nudgebee/forager"},
			{"name": "nudgebee-infra", "key": "nudgebee/nudgebee-infra"},
		},
	}

	got := selectGitCredential(nil, []GitCredentials{wrongOrg, covering}, "https://github.com/nudgebee/nudgebee-infra", "github")
	if got.Username != covering.Username {
		t.Fatalf("expected the credential listing nudgebee-infra (%s), got %s", covering.Username, got.Username)
	}
}

func TestSelectGitCredentialMatching(t *testing.T) {
	gh := func(user string, keys ...string) GitCredentials {
		projects := make([]map[string]string, 0, len(keys))
		for _, k := range keys {
			projects = append(projects, map[string]string{"key": k})
		}
		return GitCredentials{Username: user, Url: "api.github.com", AuthType: "token", Provider: "github", Projects: projects}
	}

	cases := []struct {
		name     string
		creds    []GitCredentials
		repoURL  string
		provider string
		want     string
	}{
		{
			name:     "trailing .git and case differences still match",
			creds:    []GitCredentials{gh("wrong", "other/repo"), gh("right", "NudgeBee/NudgeBee-Infra")},
			repoURL:  "https://github.com/nudgebee/nudgebee-infra.git",
			provider: "github",
			want:     "right",
		},
		{
			name:     "projects holding full URLs match too",
			creds:    []GitCredentials{gh("wrong", "other/repo"), {Username: "right", Url: "api.github.com", AuthType: "token", Provider: "github", Projects: []map[string]string{{"repository": "https://github.com/nudgebee/nudgebee-infra"}}}},
			repoURL:  "https://github.com/nudgebee/nudgebee-infra",
			provider: "github",
			want:     "right",
		},
		{
			// Same repo name under a different org must not match — that is the
			// wrong-token clone this fix exists to prevent.
			name:     "same repo name in another org does not match; falls back by provider",
			creds:    []GitCredentials{{Username: "gl", Provider: "gitlab", AuthType: "token"}, gh("gh-first", "someone-else/nudgebee-infra")},
			repoURL:  "https://github.com/nudgebee/nudgebee-infra",
			provider: "github",
			want:     "gh-first",
		},
		{
			// A stale or empty project list is common; behave exactly as before
			// rather than refusing to clone.
			name:     "no project lists at all falls back to the first credential",
			creds:    []GitCredentials{gh("first"), gh("second")},
			repoURL:  "https://github.com/nudgebee/nudgebee-infra",
			provider: "github",
			want:     "first",
		},
		{
			name:     "unparseable repo url falls back by provider",
			creds:    []GitCredentials{{Username: "gl", Provider: "gitlab", AuthType: "token"}, gh("gh", "nudgebee/nudgebee-infra")},
			repoURL:  "not-a-url",
			provider: "github",
			want:     "gh",
		},
		{
			name:     "single credential is used regardless of coverage",
			creds:    []GitCredentials{gh("only", "unrelated/repo")},
			repoURL:  "https://github.com/nudgebee/nudgebee-infra",
			provider: "github",
			want:     "only",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectGitCredential(nil, tc.creds, tc.repoURL, tc.provider); got.Username != tc.want {
				t.Errorf("selectGitCredential() = %s, want %s", got.Username, tc.want)
			}
		})
	}
}
