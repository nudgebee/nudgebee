package agents

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"
)

// TestEventSummaryToolAcceptsCallWithoutCommand pins the fix for the summary
// that read, in full:
//
//	Invalid tool input for "event_summary":
//	  - missing required field "command"
//
// Call() never reads "command" — it summarises input.Context / QueryContext —
// so requiring it could only ever reject valid calls.
func TestEventSummaryToolAcceptsCallWithoutCommand(t *testing.T) {
	tool := EventSummaryTool{}

	if got := toolcore.ValidateToolInput(tool, `{}`); got != nil {
		t.Errorf("empty input rejected: %s", *got)
	}
	if got := toolcore.ValidateToolInput(tool, `{"events": "[]"}`); got != nil {
		t.Errorf("input naming the field the way the description implies was rejected: %s", *got)
	}
	if got := toolcore.ValidateToolInput(tool, `{"command": "some events"}`); got != nil {
		t.Errorf("legacy command input rejected: %s", *got)
	}
	if len(tool.InputSchema().Required) != 0 {
		t.Errorf("schema declares required fields %v; Call() reads none of them", tool.InputSchema().Required)
	}
}
