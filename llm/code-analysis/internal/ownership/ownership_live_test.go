package ownership

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

// Read-only smoke test. It never creates or assigns an artifact. Use the current
// gh login only when explicitly opted in; never print the token or commit email.
func TestLiveGitHubOwnershipLookup(t *testing.T) {
	repo := os.Getenv("OWNERSHIP_TEST_REPO")
	if repo == "" {
		t.Skip("set OWNERSHIP_TEST_REPO, OWNERSHIP_TEST_SHA and OWNERSHIP_TEST_PATH for live read-only validation")
	}
	sha, path := os.Getenv("OWNERSHIP_TEST_SHA"), os.Getenv("OWNERSHIP_TEST_PATH")
	require.NotEmpty(t, sha)
	require.NotEmpty(t, path)
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		out, err := exec.Command("gh", "auth", "token").Output()
		require.NoError(t, err)
		token = strings.TrimSpace(string(out))
	}
	c, err := New(repo, token, gitprovider.GitProviderGitHub)
	require.NoError(t, err)
	r := c.Resolve(context.Background(), sha, []string{path})
	require.Equal(t, "resolved", r.Status, r.Warning)
	require.NotEmpty(t, r.Handle)
	t.Logf("provider=%s repository=%s handle=%s status=%s", r.Provider, r.Repository, r.Handle, r.Status)
}
