package agents

import (
	"fmt"
	"testing"

	"nudgebee/code-analysis-agent/config"
)

// shouldSuppressReply mirrors the reply-loop guard in Execute: automation only
// suppresses a reply on non-inline sources, where answering means opening a new
// top-level comment.
func shouldSuppressReply(a *PRFollowupAgent, source, authorType, body string) bool {
	return source != "inline" && a.isAutomationComment(authorType, body)
}

// An AI reviewer's inline comment must still get a threaded reply — that is how
// the human reading the PR learns whether each point was fixed or declined, and
// GitHub tracks resolution on it. Only new top-level comments are suppressed.
func TestInlineRepliesToReviewBotsAreKept(t *testing.T) {
	agent := &PRFollowupAgent{}
	const aiReview = "This drops the error on line 40; return it instead."

	if shouldSuppressReply(agent, "inline", "Bot", aiReview) {
		t.Fatal("suppressed a threaded reply to an AI reviewer's inline comment")
	}
	// The same bot's top-level walkthrough gets no new comment in return.
	if !shouldSuppressReply(agent, "issue_comment", "Bot", "## Walkthrough\nThis PR changes 2 files.") {
		t.Fatal("did not suppress a new top-level reply to a bot's summary comment")
	}
	// Humans are answered on every source.
	for _, source := range []string{"inline", "issue_comment", "review_body"} {
		if shouldSuppressReply(agent, source, "User", "please fix the nil deref") {
			t.Fatalf("suppressed a reply to a human on source %q", source)
		}
	}
}

// isAutomationComment decides whether to open a new top-level comment answering
// a comment, never whether to read it. Issue #29204 removed an author filter
// from the gatherers because it was silencing gemini-code-assist and
// coderabbitai review feedback; the gatherers must keep surfacing every comment
// regardless of what this returns.
func TestIsAutomationComment(t *testing.T) {
	tests := []struct {
		name       string
		authorType string
		body       string
		want       bool
	}{
		{
			// The comment that drove three followup runs on PR #35094. Posted
			// under a maintainer's PAT, so the author type is "User" and only
			// the body marker gives it away.
			name:       "labeler thanks under a human PAT",
			authorType: "User",
			body:       "<!-- Labeler (https://github.com/jimschubert/labeler) -->\n👍 Thanks for this!\n🏷 I have applied any labels matching special text in your issue.\n",
			want:       true,
		},
		{
			name:       "labeler validation failure",
			authorType: "User",
			body:       "<!-- Labeler (https://github.com/jimschubert/labeler) -->\nPR validation failed - please attach github issues with this PR",
			want:       true,
		},
		{
			// Every GitHub App is caught by author type alone — no marker, no
			// configuration, works on any customer's repo. This suppresses the
			// reply only; the comment is still gathered and acted on.
			name:       "dependabot gets no reply",
			authorType: "Bot",
			body:       "Superseded by #123.",
			want:       true,
		},
		{
			// Classified as automation, but that only blocks a new top-level
			// comment — this bot's INLINE feedback is still read and still gets a
			// threaded reply. See TestInlineRepliesToReviewBotsAreKept.
			name:       "ai reviewer is classified as automation",
			authorType: "Bot",
			body:       "This drops the error on line 40; return it instead.",
			want:       true,
		},
		{
			name:       "our own followup reply",
			authorType: "Bot",
			body:       "**Automated Followup**\n\nAcknowledged.",
			want:       true,
		},
		{
			name:       "human review comment",
			authorType: "User",
			body:       "This drops the error on the floor when integrationId is empty — please return it instead.",
			want:       false,
		},
		{
			// A human discussing the labeler must still be heard.
			name:       "human mentioning the labeler",
			authorType: "User",
			body:       "The labeler bot keeps failing on this PR, can you attach the issue?",
			want:       false,
		},
	}
	// No config: the agent falls back to the shipped default marker list.
	agent := &PRFollowupAgent{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agent.isAutomationComment(tc.authorType, tc.body); got != tc.want {
				t.Fatalf("isAutomationComment(%q, ...) = %v, want %v", tc.authorType, got, tc.want)
			}
		})
	}
}

// Operators whose repos run different PAT-driven automation configure their own
// markers; the shipped default must not be the only thing that can ever match.
func TestAutomationCommentMarkersAreConfigurable(t *testing.T) {
	agent := &PRFollowupAgent{config: &config.Config{
		Agent: config.AgentConfig{
			AutomationCommentMarkers: "<!-- acme-ci -->,  <!-- release-drafter -->  ",
		},
	}}

	for _, body := range []string{"<!-- acme-ci -->\nBuild queued.", "<!-- release-drafter -->\nDraft updated."} {
		if !agent.isAutomationComment("User", body) {
			t.Fatalf("configured marker did not match body %q", body)
		}
	}
	// Replacing the list drops the built-in default, which is the point: an
	// operator who does not run the labeler should not carry its marker.
	if agent.isAutomationComment("User", "<!-- Labeler (https://github.com/jimschubert/labeler) -->\nhi") {
		t.Fatal("default marker still matched after the list was overridden")
	}
	// A human is still heard regardless of configuration.
	if agent.isAutomationComment("User", "please fix the nil deref on line 40") {
		t.Fatal("human comment classified as automation")
	}
}

// The marker we stamp on a reply must be recoverable by the parser that decides
// what has already been answered. If this round-trip breaks, every run
// re-answers every comment — the PR #35094 failure.
func TestFollowupReplyMarkerRoundTrip(t *testing.T) {
	cases := []struct {
		source string
		id     int64
	}{
		{"issue_comment", 5101858261},
		{"review_body", 42},
	}

	for _, c := range cases {
		t.Run(c.source, func(t *testing.T) {
			body := fmt.Sprintf("**Automated Followup**\n\nAcknowledged.\n\n%s",
				followupReplyMarker(c.source, c.id))

			matches := followupReplyMarkerRe.FindAllStringSubmatch(body, -1)
			if len(matches) != 1 {
				t.Fatalf("got %d marker matches, want 1 (body: %q)", len(matches), body)
			}
			gotKey := matches[0][1] + ":" + matches[0][2]
			if want := answeredCommentKey(c.source, c.id); gotKey != want {
				t.Fatalf("parsed key = %q, want %q", gotKey, want)
			}
		})
	}
}

// Two different sources must not alias onto one another: GitHub issue-comment
// ids and review ids are separate spaces and can collide numerically.
func TestAnsweredCommentKeyIsSourceScoped(t *testing.T) {
	if answeredCommentKey("issue_comment", 7) == answeredCommentKey("review_body", 7) {
		t.Fatal("issue_comment and review_body with the same id produced the same key")
	}
}
