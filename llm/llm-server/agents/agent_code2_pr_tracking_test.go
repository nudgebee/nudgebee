package agents

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// newCapturingContext builds a RequestContext whose logger writes to buf, so a
// test can assert on which log lines a call actually emitted.
func newCapturingContext(buf *bytes.Buffer) *security.RequestContext {
	logger := slog.New(slog.NewTextHandler(buf, nil))
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), logger, nil, nil)
}

// TestTrackPRInResolution_SilentPathsDoNotPanic locks in that every early-return
// branch (malformed JSON, missing automated_fix_pr_info, non-object PR info,
// missing url) still returns cleanly without reaching the DB. These are the
// branches #37022 added WARN logging to — before that change, a PR created by
// the code agent could fail to register for pr_lifecycle_cron follow-up with
// zero trace anywhere.
func TestTrackPRInResolution_SilentPathsDoNotPanic(t *testing.T) {
	query := core.NBAgentRequest{ConversationId: "conv-pr-tracking-test"}

	cases := map[string]string{
		"malformed json":                      `not json`,
		"missing automated_fix_pr_info":       `{"execution_status":"success"}`,
		"automated_fix_pr_info not an object": `{"automated_fix_pr_info":"oops"}`,
		"missing url":                         `{"automated_fix_pr_info":{"branch":"fix/x"}}`,
	}

	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := newCapturingContext(&buf)
			assert.NotPanics(t, func() {
				trackPRInResolution(ctx, query, resp, "https://github.com/nudgebee/example", "github")
			})
		})
	}
}

// TestTrackPRInResolution_MissingPRInfoOnlyWarnsWhenPRWasDue guards against the
// log-noise regression flagged in review on #37031: trackPRInResolution runs
// after every code_analyzer call, so a bare "no automated_fix_pr_info" WARN
// would fire on every explore/propose (raise_pr=false) call and every
// successful no_op — neither of which raises a PR. It must only warn when a
// PR was actually requested and due but never showed up.
func TestTrackPRInResolution_MissingPRInfoOnlyWarnsWhenPRWasDue(t *testing.T) {
	const missingPRInfoMsg = "no automated_fix_pr_info"

	tests := []struct {
		name      string
		query     string
		response  string
		wantsWarn bool
	}{
		{"explore/propose (raise_pr=false)", `{"raise_pr":false}`, `{"execution_status":"success"}`, false},
		{"raise_pr=true but successful no_op", `{"raise_pr":true}`, `{"execution_status":"no_op"}`, false},
		{"raise_pr=true, PR expected but missing", `{"raise_pr":true}`, `{"execution_status":"success"}`, true},
		{"no query payload (defaults raise_pr=false)", ``, `{"execution_status":"success"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := newCapturingContext(&buf)
			query := core.NBAgentRequest{ConversationId: "conv-pr-tracking-test", Query: tt.query}

			trackPRInResolution(ctx, query, tt.response, "https://github.com/nudgebee/example", "github")

			if tt.wantsWarn {
				assert.Contains(t, buf.String(), missingPRInfoMsg)
			} else {
				assert.NotContains(t, buf.String(), missingPRInfoMsg)
			}
		})
	}
}
