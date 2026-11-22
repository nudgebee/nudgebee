package planners

import (
	"context"
	"testing"

	"nudgebee/code-analysis-agent/tools"
)

// A comment_responses that submit_analysis rejects must be retried, not end the
// run: a failed submit leaves the PR followup unsettled, so the same PR is
// re-run on the next cycle with nothing recorded (#39995).
func TestIsRetriableSubmitError_InvalidCommentResponses(t *testing.T) {
	resp := tools.NewSubmitAnalysisTool().Execute(context.Background(), map[string]any{
		"execution_status":  "success",
		"execution_summary": "Review comments addressed.",
		"comment_responses": "All comments addressed.",
	})
	if resp.Status != "error" {
		t.Fatalf("submit_analysis accepted a prose comment_responses: status %q", resp.Status)
	}
	if !(&ReActPlanner{}).isRetriableSubmitError(resp.Error) {
		t.Fatalf("error %q is not retriable; the planner would end the run instead of resending", resp.Error)
	}
}
