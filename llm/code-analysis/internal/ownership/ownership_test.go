package ownership

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(t *testing.T, provider gitprovider.GitProvider, handler func(*http.Request) (int, string)) *Client {
	t.Helper()
	c, err := New("https://example.com/acme/repo.git", "test-token", provider)
	require.NoError(t, err)
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		status, body := handler(r)
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	return c
}

func ghCommit(sha, login, kind, date string) string {
	return fmt.Sprintf(`{"sha":%q,"author":{"login":%q,"type":%q},"commit":{"author":{"email":"private@example.com"},"committer":{"date":%q}}}`, sha, login, kind, date)
}

func TestResolveNewestEligibleHumanAcrossPaths(t *testing.T) {
	checks := map[string]int{}
	c := testClient(t, gitprovider.GitProviderGitHub, func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/commits") {
			require.Equal(t, "before-fix", r.URL.Query().Get("sha"))
			switch r.URL.Query().Get("path") {
			case "new.go":
				return 200, `[]`
			case "old.go":
				return 200, "[" + ghCommit("z", "old", "User", "2026-01-01T00:00:00Z") + "]"
			default:
				return 200, "[" + strings.Join([]string{
					ghCommit("f", "robot[bot]", "Bot", "2026-01-05T00:00:00Z"),
					ghCommit("e", "", "", "2026-01-04T00:00:00Z"),
					ghCommit("d", "ineligible", "User", "2026-01-03T00:00:00Z"),
					ghCommit("b", "bob", "User", "2026-01-02T00:00:00Z"),
					ghCommit("a", "alice", "User", "2026-01-02T00:00:00Z"),
				}, ",") + "]"
			}
		}
		checks[r.URL.Path]++
		if strings.HasSuffix(r.URL.Path, "/alice") {
			return 204, ""
		}
		return 404, ""
	})
	r := c.Resolve(context.Background(), "before-fix", []string{"old.go", "new.go", "changed.go"})
	require.Equal(t, "alice", r.Handle)
	require.Equal(t, "resolved", r.Status)
	require.Len(t, checks, 2)
}

func TestUnresolvedDoesNotRequirePublicationFailure(t *testing.T) {
	for _, status := range []int{200, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := testClient(t, gitprovider.GitProviderGitHub, func(*http.Request) (int, string) { return status, `[]` })
			r := c.Resolve(context.Background(), "base", []string{"new-file"})
			require.Empty(t, r.Handle)
			require.Equal(t, "unresolved", r.Status)
			require.NotEmpty(t, r.Warning)
		})
	}
}

func TestGitLabExactEmailAndMembership(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitLab, func(r *http.Request) (int, string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/repository/commits"):
			require.Equal(t, "base", r.URL.Query().Get("ref_name"))
			return 200, `[{"id":"one","author_email":"alice@example.com","committed_date":"2026-01-01T00:00:00Z"}]`
		case r.URL.Path == "/api/v4/users":
			require.Equal(t, "alice@example.com", r.URL.Query().Get("public_email"))
			return 200, `[{"id":7,"username":"alice","state":"active"}]`
		case r.URL.Path == "/api/v4/users/7":
			return 200, `{"id":7,"username":"alice","state":"active","public_email":"alice@example.com","bot":false}`
		case strings.HasSuffix(r.URL.Path, "/members/all/7"):
			return 200, `{"id":7,"username":"alice","state":"active"}`
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
			return 500, ""
		}
	})
	r := c.Resolve(context.Background(), "base", []string{"a"})
	require.Equal(t, "alice", r.Handle)
	require.Equal(t, 7, r.UserID)
}

func TestGitLabDoesNotGuessPrivateEmail(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitLab, func(*http.Request) (int, string) {
		return 200, `[{"id":7,"username":"alice","public_email":"different@example.com"}]`
	})
	require.Empty(t, c.gitlabAuthor(context.Background(), "alice@example.com").handle())
}

func TestGitLabValidatesNoreplyIdentity(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitLab, func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/users/7") {
			return 200, `{"id":7,"username":"alice","state":"active"}`
		}
		return 200, `[]`
	})
	require.Equal(t, "alice", c.gitlabAuthor(context.Background(), "7-alice@users.noreply.example.com").handle())
	require.Empty(t, c.gitlabAuthor(context.Background(), "7-impostor@users.noreply.example.com").handle())
	require.Empty(t, c.gitlabAuthor(context.Background(), "7-alice@users.noreply.other.com").handle())
}

func TestVerifyAndAssignBothProviders(t *testing.T) {
	for _, provider := range []gitprovider.GitProvider{gitprovider.GitProviderGitHub, gitprovider.GitProviderGitLab} {
		t.Run(string(provider), func(t *testing.T) {
			writes := 0
			c := testClient(t, provider, func(r *http.Request) (int, string) {
				if r.Method == http.MethodGet {
					if writes == 0 {
						return 200, `{"assignees":[]}`
					}
					return 200, `{"assignees":[{"login":"alice","username":"alice"}]}`
				}
				writes++
				var payload map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				if provider == gitprovider.GitProviderGitHub {
					require.Equal(t, http.MethodPost, r.Method)
					require.Equal(t, []any{"alice"}, payload["assignees"])
				} else {
					require.Equal(t, http.MethodPut, r.Method)
					require.Equal(t, []any{float64(7)}, payload["assignee_ids"])
				}
				return 200, `{}`
			})
			prURL := "https://example.com/acme/repo/pull/1"
			if provider == gitprovider.GitProviderGitLab {
				prURL = "https://example.com/acme/repo/-/merge_requests/1"
			}
			r := c.VerifyAndAssign(context.Background(), prURL, Result{Handle: "alice", UserID: 7, Status: "resolved"})
			require.Equal(t, "assigned", r.Status)
			require.Empty(t, r.Warning)
			require.Equal(t, 1, writes)
		})
	}
}

func TestAssignmentRejectionKeepsPR(t *testing.T) {
	writes := 0
	c := testClient(t, gitprovider.GitProviderGitHub, func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet {
			return 200, `{"assignees":[]}`
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.True(t, strings.HasSuffix(r.URL.Path, "/assignees"))
		writes++
		return 403, `{}`
	})
	r := c.VerifyAndAssign(context.Background(), "https://example.com/acme/repo/pull/1", Result{Handle: "alice"})
	require.Equal(t, "failed", r.Status)
	require.Contains(t, r.Warning, "published")
	require.Equal(t, 1, writes)
}

func TestFindPartialCreation(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitHub, func(r *http.Request) (int, string) {
		require.Equal(t, "acme:fix", r.URL.Query().Get("head"))
		require.Equal(t, "main", r.URL.Query().Get("base"))
		return 200, `[{"html_url":"https://example.com/acme/repo/pull/1","head":{"repo":{"full_name":"acme/repo"}}}]`
	})
	u, err := c.Find(context.Background(), "fix", "main")
	require.NoError(t, err)
	require.Contains(t, u, "/pull/1")
}

func TestCancellationDoesNotClaimAssignment(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitHub, func(*http.Request) (int, string) { return 200, `{"assignees":[]}` })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := c.VerifyAndAssign(ctx, "https://example.com/acme/repo/pull/1", Result{Handle: "alice"})
	require.Equal(t, "failed", r.Status)
}

func TestGitHubAssignmentURLIdentity(t *testing.T) {
	for _, tc := range []struct {
		url      string
		assigned bool
	}{
		{"https://EXAMPLE.com/AcMe/RePo/pull/1", true},
		{"https://example.com/other/repo/pull/1", false},
		{"https://elsewhere.com/acme/repo/pull/1", false},
		{"https://example.com/acme/repo/pull/x/1", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			c := testClient(t, gitprovider.GitProviderGitHub, func(*http.Request) (int, string) {
				require.True(t, tc.assigned, "invalid URLs must not reach provider")
				return 200, `{"assignees":[{"login":"alice"}]}`
			})
			r := c.VerifyAndAssign(context.Background(), tc.url, Result{Handle: "alice"})
			require.Equal(t, tc.assigned, r.Status == "assigned")
		})
	}
}

func TestOversizedChangeSkipsLookupWithoutBlockingPublication(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitHub, func(*http.Request) (int, string) {
		t.Fatal("oversized change should not perform partial lookup")
		return 500, ""
	})
	r := c.Resolve(context.Background(), "base", make([]string, 101))
	require.Equal(t, "unresolved", r.Status)
	require.Contains(t, r.Warning, "budget")
}

func TestRecoveryDoesNotAdoptForkPR(t *testing.T) {
	for _, provider := range []gitprovider.GitProvider{gitprovider.GitProviderGitHub, gitprovider.GitProviderGitLab} {
		t.Run(string(provider), func(t *testing.T) {
			c := testClient(t, provider, func(*http.Request) (int, string) {
				if provider == gitprovider.GitProviderGitLab {
					return 200, `[{"web_url":"https://example.com/acme/repo/-/merge_requests/1","source_project_id":9,"target_project_id":10}]`
				}
				return 200, `[{"html_url":"https://example.com/acme/repo/pull/1","head":{"repo":{"full_name":"acme/fork"}}}]`
			})
			_, err := c.Find(context.Background(), "fix", "main")
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestGitLabRecoveryUsesSameProject(t *testing.T) {
	c := testClient(t, gitprovider.GitProviderGitLab, func(r *http.Request) (int, string) {
		require.Equal(t, "fix", r.URL.Query().Get("source_branch"))
		require.Equal(t, "main", r.URL.Query().Get("target_branch"))
		return 200, `[{"web_url":"https://example.com/acme/repo/-/merge_requests/1","source_project_id":9,"target_project_id":10},{"web_url":"https://example.com/acme/repo/-/merge_requests/2","source_project_id":10,"target_project_id":10}]`
	})
	u, err := c.Find(context.Background(), "fix", "main")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/acme/repo/-/merge_requests/2", u)
}
