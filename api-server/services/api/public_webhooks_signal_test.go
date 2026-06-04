package api

import (
	"testing"

	"github.com/google/go-github/v61/github"
	"github.com/stretchr/testify/assert"
)

// TestExtractGithubPRSignal_IssueComment covers the issue_comment branch added
// for plain PR conversation comments (issue #689 follow-up). Key cases:
//   - human comment on a PR    → dispatch
//   - bot comment on a PR      → ignored (loop prevention: the agent's own
//     followup replies post as a bot and must not re-trigger)
//   - comment on a plain issue → ignored (not a PR)
//   - edited/deleted actions   → ignored (only "created" is actionable)
func TestExtractGithubPRSignal_IssueComment(t *testing.T) {
	const prURL = "https://github.com/nudgebee/nudgebee-infra/pull/689"

	prIssue := &github.Issue{
		HTMLURL:          github.String(prURL),
		PullRequestLinks: &github.PullRequestLinks{URL: github.String(prURL)},
	}
	plainIssue := &github.Issue{HTMLURL: github.String("https://github.com/nudgebee/nudgebee-infra/issues/12")}

	humanComment := &github.IssueComment{User: &github.User{Type: github.String("User")}}
	botComment := &github.IssueComment{User: &github.User{Type: github.String("Bot")}}

	tests := []struct {
		name            string
		event           any
		wantURL         string
		wantInteresting bool
	}{
		{
			name:            "human comment on PR dispatches",
			event:           &github.IssueCommentEvent{Action: github.String("created"), Issue: prIssue, Comment: humanComment},
			wantURL:         prURL,
			wantInteresting: true,
		},
		{
			name:            "bot comment on PR is ignored (loop prevention)",
			event:           &github.IssueCommentEvent{Action: github.String("created"), Issue: prIssue, Comment: botComment},
			wantURL:         "",
			wantInteresting: false,
		},
		{
			name:            "comment on plain issue is ignored",
			event:           &github.IssueCommentEvent{Action: github.String("created"), Issue: plainIssue, Comment: humanComment},
			wantURL:         "",
			wantInteresting: false,
		},
		{
			name:            "edited comment is ignored",
			event:           &github.IssueCommentEvent{Action: github.String("edited"), Issue: prIssue, Comment: humanComment},
			wantURL:         "",
			wantInteresting: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, _, terminal, interesting := extractGithubPRSignal(tt.event)
			assert.Equal(t, tt.wantURL, url)
			assert.Equal(t, tt.wantInteresting, interesting)
			assert.False(t, terminal, "issue_comment is never terminal")
		})
	}
}
