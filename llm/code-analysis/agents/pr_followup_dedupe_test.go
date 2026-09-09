package agents

import (
	"encoding/json"
	"fmt"
	"strings"
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

// The non-convergence notice marker must be scoped to the pending comments a
// run actually saw, not a bare constant. A bare constant means "has any notice
// ever been posted on this PR" — true forever after the first one, which
// silently suppresses the notice for every later run too, even one triggered
// by a brand-new, unrelated unresolved comment. This is issue #36625,
// reproduced live twice against PR #36518 before this fix.
func TestNonConvergenceNoticeMarkerIsScopedToPendingComments(t *testing.T) {
	firstRun := []reviewComment{{ID: 1001}}
	laterRunWithNewComment := []reviewComment{{ID: 1001}, {ID: 2002}}

	if nonConvergenceNoticeMarker(firstRun) == nonConvergenceNoticeMarker(laterRunWithNewComment) {
		t.Fatal("marker did not change when a new pending comment joined the set — the notice would stay suppressed forever after the first post")
	}
}

// A retry on the exact same still-unresolved comment(s) must reuse the same
// marker — that is the guard's original purpose: don't spam the same notice
// every cron cycle while nothing has changed. Order of discovery must not
// matter, since gatherers don't guarantee a stable order.
func TestNonConvergenceNoticeMarkerStableForSamePendingSet(t *testing.T) {
	a := nonConvergenceNoticeMarker([]reviewComment{{ID: 1001}, {ID: 2002}})
	b := nonConvergenceNoticeMarker([]reviewComment{{ID: 2002}, {ID: 1001}})
	if a != b {
		t.Fatalf("marker depends on discovery order: %q vs %q", a, b)
	}
}

// The marker embedded in the posted comment must be exactly what
// hasExistingFollowupNotice searches for, or the dedup check can never match
// what was actually posted.
func TestNonConvergenceNoticeBodyContainsItsOwnMarker(t *testing.T) {
	agent := &PRFollowupAgent{}
	pending := []reviewComment{{ID: 42}}
	body := agent.buildNonConvergenceNotice("looked at the failing check", pending)
	if want := nonConvergenceNoticeMarker(pending); !strings.Contains(body, want) {
		t.Fatalf("notice body %q does not contain its own marker %q", body, want)
	}
}

// resolvedCommentIDsFromThreads must treat GitHub's real thread-resolution
// state as authoritative, independent of what any reply said (or whether one
// exists at all). This is issue #36629, reproduced live on PR #36518: five
// comments were fixed and resolved on 2026-08-19 via a reply that predates
// the "Automated Followup" marker convention ("Fixed in efb60abb42."), and a
// sixth comment was left genuinely unresolved. gatherInlineComments's
// text-marker check alone could not tell these apart; resolution state can.
// Built from a real GraphQL response shape (not hand-built structs) so this
// also pins the JSON tags — a field-name typo there fails silently (empty
// results, not a decode error).
func TestResolvedCommentIDsFromThreads(t *testing.T) {
	const raw = `{
		"data": {
			"repository": {
				"pullRequest": {
					"reviewThreads": {
						"nodes": [
							{"isResolved": true, "comments": {"nodes": [{"databaseId": 3805255283}, {"databaseId": 3805255292}]}},
							{"isResolved": false, "comments": {"nodes": [{"databaseId": 3819075107}]}}
						]
					}
				}
			}
		}
	}`

	var resp reviewThreadsGraphQLResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	resolved := resolvedCommentIDsFromThreads(resp)
	if len(resolved) != 2 || !resolved[3805255283] || !resolved[3805255292] {
		t.Fatalf("resolved set = %v, want {3805255283, 3805255292}", resolved)
	}
	if resolved[3819075107] {
		t.Error("unresolved comment incorrectly present in resolved set")
	}
}

// unresolvedThreadIDsByComment must map an unresolved comment to its thread's
// node ID (so a successful reply can resolve it — the user's ask: "if a
// comment is resolved, it should also resolve it on GitHub"), and must omit
// already-resolved threads entirely (so resolving is idempotent — no repeat
// mutation call once a thread is already resolved). Thread ID is a real one
// fetched live from PR #36518 while verifying the resolveReviewThread mutation.
func TestUnresolvedThreadIDsByComment(t *testing.T) {
	const raw = `{
		"data": {
			"repository": {
				"pullRequest": {
					"reviewThreads": {
						"nodes": [
							{"id": "PRRT_kwDOIJg5ds6aJ57B", "isResolved": true, "comments": {"nodes": [{"databaseId": 3805255283}]}},
							{"id": "PRRT_kwDOIJg5ds6bXk9Z", "isResolved": false, "comments": {"nodes": [{"databaseId": 3819075107}]}}
						]
					}
				}
			}
		}
	}`

	var resp reviewThreadsGraphQLResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	ids := unresolvedThreadIDsByComment(resp)
	if got, want := ids[3819075107], "PRRT_kwDOIJg5ds6bXk9Z"; got != want {
		t.Fatalf("thread id for unresolved comment = %q, want %q", got, want)
	}
	if _, ok := ids[3805255283]; ok {
		t.Error("already-resolved comment's thread should be omitted, not re-resolved")
	}
}

// parseSubmitAnalysisData must round-trip the planner's dynamically-typed
// submit_analysis payload (a map[string]any from decoded JSON) into a typed
// struct without losing fields, including should_reply's absent-vs-false
// distinction that the old map[string]any code path handled by hand.
func TestParseSubmitAnalysisData(t *testing.T) {
	t.Run("nil submitData is not an error", func(t *testing.T) {
		parsed, err := parseSubmitAnalysisData(nil)
		if err != nil || parsed != nil {
			t.Fatalf("parseSubmitAnalysisData(nil) = (%v, %v), want (nil, nil)", parsed, err)
		}
	})

	t.Run("round-trips a real followup-mode payload", func(t *testing.T) {
		raw := map[string]any{
			"execution_status":  "success",
			"execution_summary": "created the model file",
			"files_modified":    []any{"api-server/services/internal/database/models/pr_followup.go"},
			"comment_responses": []any{
				map[string]any{"comment_id": float64(3819075107), "action": "fixed", "reply": "done"},
			},
		}
		parsed, err := parseSubmitAnalysisData(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsed.ExecutionStatus != "success" || parsed.ExecutionSummary != "created the model file" {
			t.Fatalf("unexpected parsed data: %+v", parsed)
		}
		if len(parsed.FilesModified) != 1 || parsed.FilesModified[0] != "api-server/services/internal/database/models/pr_followup.go" {
			t.Fatalf("files_modified not parsed: %v", parsed.FilesModified)
		}
		if len(parsed.CommentResponses) != 1 || parsed.CommentResponses[0].CommentID != 3819075107 {
			t.Fatalf("comment_responses not parsed: %+v", parsed.CommentResponses)
		}
	})
}

// extractCommentResponses' should_reply default (absent -> true only when
// there's reply text) must survive the switch to a typed struct — a comment
// response with reply text but no explicit should_reply must still reply.
func TestExtractCommentResponses_ShouldReplyDefault(t *testing.T) {
	a := &PRFollowupAgent{}
	trueVal := true
	falseVal := false

	parsed := &submitAnalysisData{
		CommentResponses: []rawCommentResponse{
			{CommentID: 1, Action: "fixed", Reply: "done", ShouldReply: nil},        // absent + has text -> reply
			{CommentID: 2, Action: "acknowledged", Reply: "", ShouldReply: nil},     // absent + no text -> skip
			{CommentID: 3, Action: "wont_fix", Reply: "no", ShouldReply: &falseVal}, // explicit false -> skip
			{CommentID: 4, Action: "fixed", Reply: "yes", ShouldReply: &trueVal},    // explicit true -> reply
			{CommentID: 0, Action: "fixed", Reply: "ignored"},                       // zero id -> dropped
		},
	}

	got := a.extractCommentResponses(parsed)
	want := map[int64]bool{1: true, 2: false, 3: false, 4: true}
	if len(got) != len(want) {
		t.Fatalf("got %d responses, want %d: %+v", len(got), len(want), got)
	}
	for _, r := range got {
		if want[r.CommentID] != r.ShouldReply {
			t.Errorf("comment %d: ShouldReply = %v, want %v", r.CommentID, r.ShouldReply, want[r.CommentID])
		}
	}
}

// humanSummary must never fall through to nothing when the agent used the
// explore/fix-mode field names (description/answer/title) instead of
// followup mode's execution_summary — that schema mismatch is what produced
// the raw-JSON commit message and PR comment on PR #36518.
func TestSubmitAnalysisData_HumanSummary(t *testing.T) {
	cases := []struct {
		name string
		data *submitAnalysisData
		want string
	}{
		{"nil data", nil, ""},
		{"nothing set", &submitAnalysisData{}, ""},
		{"execution_summary wins over description", &submitAnalysisData{ExecutionSummary: "summary", Description: "desc"}, "summary"},
		{"falls back to description", &submitAnalysisData{Description: "moved the model to shared package"}, "moved the model to shared package"},
		{"falls back to answer when no description", &submitAnalysisData{Answer: "answer text"}, "answer text"},
		{"falls back to title as last resort", &submitAnalysisData{Title: "Address review comments"}, "Address review comments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.data.humanSummary(); got != c.want {
				t.Errorf("humanSummary() = %q, want %q", got, c.want)
			}
		})
	}
}

// commitSubject must produce a real single-line subject, never the raw
// multi-paragraph description crammed in whole, and never empty.
func TestCommitSubject(t *testing.T) {
	if got := commitSubject("", "PR", "36518"); got != "fix: automated followup on PR #36518" {
		t.Errorf("empty summary: got %q", got)
	}
	if got := commitSubject("Fixed the nil check.", "PR", "36518"); got != "Fixed the nil check." {
		t.Errorf("short sentence: got %q", got)
	}
	long := strings.Repeat("a", 150)
	got := commitSubject(long, "PR", "36518")
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 101 { // 100 chars + the ellipsis rune
		t.Errorf("long summary not capped: len=%d, got %q", len([]rune(got)), got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("commit subject must be a single line, got %q", got)
	}
}

// The exact failure mode observed on PR #36518: the LLM ran git commit with
// the entire submit_analysis JSON payload as -m instead of a real message.
func TestCommitMessageLooksBad(t *testing.T) {
	bad := []string{"", "  ", `{"title": "x"}`, "[1,2,3]"}
	for _, m := range bad {
		if !commitMessageLooksBad(m) {
			t.Errorf("commitMessageLooksBad(%q) = false, want true", m)
		}
	}
	good := []string{"fix: address review comments", "Moved the model to the shared package."}
	for _, m := range good {
		if commitMessageLooksBad(m) {
			t.Errorf("commitMessageLooksBad(%q) = true, want false", m)
		}
	}
}
