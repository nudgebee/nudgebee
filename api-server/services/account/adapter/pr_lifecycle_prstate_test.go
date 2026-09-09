package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGithubPRStateFromBody pins the GitHub PR response → observedPRState map
// used by the followup cron to retire a PR that was closed while the webhook was
// not delivering (#37472). merged_at present means merged; state "closed" with
// no merged_at means closed-without-merge; "open" is still open.
func TestGithubPRStateFromBody(t *testing.T) {
	cases := []struct {
		name       string
		body       map[string]any
		wantClosed bool
		wantMerged bool
	}{
		{"open", map[string]any{"state": "open", "merged_at": nil}, false, false},
		{"closed_unmerged", map[string]any{"state": "closed", "merged_at": nil}, true, false},
		{"merged", map[string]any{"state": "closed", "merged_at": "2026-09-01T10:00:00Z"}, true, true},
		{"missing_state", map[string]any{}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := githubPRStateFromBody(c.body)
			assert.Equal(t, c.wantClosed, got.closed, "closed")
			assert.Equal(t, c.wantMerged, got.merged, "merged")
		})
	}
}

// TestGitlabMRStateFromBody pins the GitLab MR state map. "locked" and unknown
// values are treated as still-open so the followup is never retired on an
// ambiguous signal.
func TestGitlabMRStateFromBody(t *testing.T) {
	cases := []struct {
		name       string
		state      string
		wantClosed bool
		wantMerged bool
	}{
		{"opened", "opened", false, false},
		{"closed", "closed", true, false},
		{"merged", "merged", true, true},
		{"locked", "locked", false, false},
		{"unknown", "something_else", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gitlabMRStateFromBody(map[string]any{"state": c.state})
			assert.Equal(t, c.wantClosed, got.closed, "closed")
			assert.Equal(t, c.wantMerged, got.merged, "merged")
		})
	}
}

// TestPRCoordinatesFromMeta checks that org/repo/number come from prMetadata
// when set and fall back to parsing the PR URL otherwise (the case for older
// resolution rows that only stored pr_url).
func TestPRCoordinatesFromMeta(t *testing.T) {
	t.Run("from metadata", func(t *testing.T) {
		org, repo, num := prCoordinatesFromMeta(prMetadata{
			Org: "acme", Repo: "infra", PRNumber: float64(42),
			PRURL: "https://github.com/other/other/pull/1",
		})
		assert.Equal(t, "acme", org)
		assert.Equal(t, "infra", repo)
		assert.Equal(t, "42", num)
	})

	t.Run("fallback to URL", func(t *testing.T) {
		org, repo, num := prCoordinatesFromMeta(prMetadata{
			PRURL: "https://github.com/acme/infra/pull/42",
		})
		assert.Equal(t, "acme", org)
		assert.Equal(t, "infra", repo)
		assert.Equal(t, "42", num)
	})

	t.Run("trailing slash", func(t *testing.T) {
		org, repo, num := prCoordinatesFromMeta(prMetadata{
			PRURL: "https://github.com/acme/infra/pull/42/",
		})
		assert.Equal(t, "acme", org)
		assert.Equal(t, "infra", repo)
		assert.Equal(t, "42", num)
	})

	t.Run("string pr_number from metadata wins", func(t *testing.T) {
		org, repo, num := prCoordinatesFromMeta(prMetadata{
			Org: "acme", Repo: "infra", PRNumber: "7",
			PRURL: "https://github.com/acme/infra/pull/42",
		})
		assert.Equal(t, "acme", org)
		assert.Equal(t, "infra", repo)
		assert.Equal(t, "7", num)
	})

	t.Run("garbage url yields empties", func(t *testing.T) {
		org, repo, num := prCoordinatesFromMeta(prMetadata{PRURL: "not-a-url"})
		assert.Empty(t, org)
		assert.Empty(t, repo)
		assert.Empty(t, num)
	})
}

func TestPRNumberString(t *testing.T) {
	assert.Equal(t, "42", prNumberString(float64(42)))
	assert.Equal(t, "42", prNumberString("42"))
	assert.Equal(t, "42", prNumberString(42))
	assert.Equal(t, "42", prNumberString(int64(42)))
	assert.Equal(t, "", prNumberString(nil))
	assert.Equal(t, "", prNumberString(map[string]any{}))
}
