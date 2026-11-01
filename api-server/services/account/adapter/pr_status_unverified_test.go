package adapter

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Only a 200 carrying the pull request says anything about it. Anything else —
// a 5xx, a 403, a 404, a body that does not parse — used to settle the
// resolution Failed while the pull request was still open, and the next
// scheduled run raised a second pull request next to it.
func TestGitHubPRStatusFromResponse(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		body       string
		want       RecommendationResolutionStatus
		unverified bool
	}{
		{"open", 200, `{"state":"open","merged_at":null}`, RecommendationResolutionStatusInProgress, false},
		{"merged", 200, `{"state":"closed","merged_at":"2026-09-01T00:00:00Z"}`, RecommendationResolutionStatusSuccess, false},
		{"closed unmerged", 200, `{"state":"closed","merged_at":null}`, RecommendationResolutionStatusFailed, false},
		{"provider outage", 503, `<html>No server is currently available</html>`, RecommendationResolutionStatusInProgress, true},
		{"no access", 403, `{"message":"Resource not accessible by integration"}`, RecommendationResolutionStatusInProgress, true},
		{"not found", 404, `{"message":"Not Found"}`, RecommendationResolutionStatusInProgress, true},
		{"unparseable 200", 200, `not json`, RecommendationResolutionStatusInProgress, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := githubPRStatusFromResponse(tc.code, []byte(tc.body))
			assert.Equal(t, tc.want, got.Status)
			assert.Equal(t, tc.unverified, got.Unverified)
			if tc.unverified {
				assert.True(t, strings.HasPrefix(got.StatusMessage, "Could not check the pull request: "), got.StatusMessage)
			}
		})
	}
}

func TestGitLabMRStatusFromResponse(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		body       string
		want       RecommendationResolutionStatus
		unverified bool
	}{
		{"opened", 200, `{"state":"opened"}`, RecommendationResolutionStatusInProgress, false},
		{"merged", 200, `{"state":"merged"}`, RecommendationResolutionStatusSuccess, false},
		{"closed", 200, `{"state":"closed"}`, RecommendationResolutionStatusFailed, false},
		{"server error", 502, `Bad Gateway`, RecommendationResolutionStatusInProgress, true},
		{"unauthorized", 401, `{"message":"401 Unauthorized"}`, RecommendationResolutionStatusInProgress, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gitlabMRStatusFromResponse(tc.code, []byte(tc.body))
			assert.Equal(t, tc.want, got.Status)
			assert.Equal(t, tc.unverified, got.Unverified)
		})
	}
}

// last_pr_check_at is naive UTC. The cases sit well inside, just inside and just
// past the limit: a clock skewed by a timezone offset lands in the middle, where
// a check only at the edges would miss it.
func TestPRStatusCheckExpired(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *time.Time {
		v := now.Add(-ago)
		return &v
	}

	assert.False(t, PRStatusCheckExpired(nil, now), "no successful check recorded yet: the clock starts now, it has not run out")
	assert.False(t, PRStatusCheckExpired(at(time.Hour), now))
	assert.False(t, PRStatusCheckExpired(at(84*time.Hour), now), "three and a half days in")
	assert.False(t, PRStatusCheckExpired(at(PRStatusUnverifiedLimit-time.Minute), now))
	assert.True(t, PRStatusCheckExpired(at(PRStatusUnverifiedLimit+time.Minute), now))
}

func TestPRStatusGaveUpMessageKeepsTheReason(t *testing.T) {
	msg := PRStatusGaveUpMessage("Could not check the pull request: GitHub authentication failed: GITHUB_APP_ID is not configured")
	assert.Contains(t, msg, "7 days")
	assert.Contains(t, msg, "GITHUB_APP_ID is not configured")
}
