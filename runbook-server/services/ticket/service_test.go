package ticket

import (
	"testing"

	"nudgebee/runbook/common"
)

// TestGetTicketResponse_DecodesMetadataFields locks the cross-service contract
// for issue #32155: ticket-server returns models.Ticket JSON keys (assignees,
// labels, reporter, milestone, project_key, updated_at) which must decode into
// the top-level GetTicketResponse fields via DecodeMapToStruct. The array
// fields arrive as []interface{} from JSON, so this guards that they convert
// into []string rather than silently dropping.
func TestGetTicketResponse_DecodesMetadataFields(t *testing.T) {
	data := map[string]any{
		"id":          "internal-1",
		"ticket_id":   "42",
		"title":       "Issue Title",
		"status":      "open",
		"assignee":    "rohitutekar123",
		"assignees":   []any{"Kankshit-02", "rohitutekar123"},
		"reporter":    "rohitutekar123",
		"labels":      []any{"bug", "Workflow"},
		"milestone":   "v1.0",
		"project_key": "nudgebee/demo",
		"platform":    "github",
		"url":         "https://github.com/nudgebee/demo/issues/42",
		"created_at":  "2026-06-11T06:18:55Z",
		"updated_at":  "2026-06-11T06:19:05Z",
	}

	var resp GetTicketResponse
	if err := common.DecodeMapToStruct(data, &resp); err != nil {
		t.Fatalf("DecodeMapToStruct error: %v", err)
	}

	if len(resp.Assignees) != 2 || resp.Assignees[0] != "Kankshit-02" || resp.Assignees[1] != "rohitutekar123" {
		t.Errorf("Assignees = %v, want [Kankshit-02 rohitutekar123]", resp.Assignees)
	}
	if len(resp.Labels) != 2 || resp.Labels[0] != "bug" || resp.Labels[1] != "Workflow" {
		t.Errorf("Labels = %v, want [bug Workflow]", resp.Labels)
	}
	if resp.Reporter != "rohitutekar123" {
		t.Errorf("Reporter = %q, want rohitutekar123", resp.Reporter)
	}
	if resp.Milestone != "v1.0" {
		t.Errorf("Milestone = %q, want v1.0", resp.Milestone)
	}
	if resp.ProjectKey != "nudgebee/demo" {
		t.Errorf("ProjectKey = %q, want nudgebee/demo", resp.ProjectKey)
	}
	if resp.UpdatedAt != "2026-06-11T06:19:05Z" {
		t.Errorf("UpdatedAt = %q, want 2026-06-11T06:19:05Z", resp.UpdatedAt)
	}
	// Sanity: existing fields still decode.
	if resp.Assignee != "rohitutekar123" || resp.TicketID != "42" || resp.Platform != "github" {
		t.Errorf("base fields mis-decoded: assignee=%q ticket_id=%q platform=%q", resp.Assignee, resp.TicketID, resp.Platform)
	}
}
