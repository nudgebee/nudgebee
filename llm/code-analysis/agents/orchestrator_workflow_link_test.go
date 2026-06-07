package agents

import (
	"strings"
	"testing"
)

func TestBuildWorkflowPRLink(t *testing.T) {
	const (
		baseURL    = "https://example.com"
		workflowID = "e2bfadac-f122-4cca-91ef-f1e94da3717f"
		accountID  = "ff87fbfd-5729-4474-b9d6-96beb693e3fd"
	)

	t.Run("renders link when all fields present", func(t *testing.T) {
		got := buildWorkflowPRLink(baseURL, workflowID, accountID)
		want := "https://example.com/workflow/" + workflowID + "?accountId=" + accountID
		if !strings.Contains(got, want) {
			t.Errorf("expected workflow URL %q in output, got:\n%s", want, got)
		}
		if !strings.Contains(got, "[View Workflow]") {
			t.Errorf("expected 'View Workflow' label, got:\n%s", got)
		}
	})

	// A trailing slash on the base URL must not produce a double slash in the path.
	t.Run("trims trailing slash on base url", func(t *testing.T) {
		got := buildWorkflowPRLink(baseURL+"/", workflowID, accountID)
		if strings.Contains(got, "in//workflow") {
			t.Errorf("expected no double slash before /workflow, got:\n%s", got)
		}
		want := "https://example.com/workflow/" + workflowID + "?accountId=" + accountID
		if !strings.Contains(got, want) {
			t.Errorf("expected %q, got:\n%s", want, got)
		}
	})

	// Missing any required field must yield no link block so non-workflow PRs
	// (chat-driven, event/recommendation-driven without a workflow) are unaffected.
	t.Run("empty when a required field is missing", func(t *testing.T) {
		cases := map[string][3]string{
			"no workflow id": {baseURL, "", accountID},
			"no account id":  {baseURL, workflowID, ""},
			"no base url":    {"", workflowID, accountID},
		}
		for name, in := range cases {
			if got := buildWorkflowPRLink(in[0], in[1], in[2]); got != "" {
				t.Errorf("%s: expected empty string, got %q", name, got)
			}
		}
	})
}
