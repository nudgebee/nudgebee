package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func TestTicketToolsClassifyByOperation(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	v2 := []struct {
		input string
		want  core.ToolRequestType
	}{
		// How the tool is called today: the operation in a `command` string.
		{`{"command":"{\"operation_type\": \"list_tickets\", \"limit\": 100}"}`, core.ToolRequestTypeRead},
		{`{"command":"{\"operation_type\": \"get_ticket\", \"ticket_id\": \"IN-1\"}"}`, core.ToolRequestTypeRead},
		{`{"command":"{\"operation_type\": \"get_create_meta\"}"}`, core.ToolRequestTypeRead},
		{`{"command":"{\"operation_type\": \"get_comments\", \"ticket_id\": \"IN-1\"}"}`, core.ToolRequestTypeRead},
		{`{"command":"{\"operation_type\": \"create_ticket\", \"title\": \"x\"}"}`, core.ToolRequestTypeCreate},
		{`{"command":"{\"operation_type\": \"add_comment\", \"ticket_id\": \"IN-1\", \"comment_text\": \"y\"}"}`, core.ToolRequestTypeCreate},
		// Other shapes the executor accepts.
		{`{"operation_type":"create_ticket","title":"x"}`, core.ToolRequestTypeCreate},
		{`{"command":{"operation_type":"create_ticket","title":"x"}}`, core.ToolRequestTypeCreate},
		{`{"command":{"type":"string","value":"{\"operation_type\":\"create_ticket\"}"}}`, core.ToolRequestTypeCreate},
		// A read beside a write in another place: the write wins.
		{`{"operation_type":"list_tickets","command":"{\"operation_type\":\"create_ticket\"}"}`, core.ToolRequestTypeCreate},
		// Unknown or missing operation: ask a person.
		{`{"command":"{\"operation_type\": \"delete_ticket\"}"}`, core.ToolRequestTypeUpdate},
		{`{"command":"not json"}`, core.ToolRequestTypeUpdate},
		{``, core.ToolRequestTypeUpdate},
	}
	for _, c := range v2 {
		got, err := TicketMasterV2{}.InferToolRequestType(ctx, "", c.input)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "v2 %s", c.input)
	}
	v1 := map[string]core.ToolRequestType{
		`{"command":"{\"operation_type\": \"search\", \"query\": \"project = X\"}"}`:                                          core.ToolRequestTypeRead,
		`{"command":"{\"operation_type\": \"update\", \"ticket_id\": \"X-1\", \"field_name\": \"a\", \"new_value\": \"b\"}"}`: core.ToolRequestTypeUpdate,
		`{"command":"{\"operation_type\": \"comment\", \"ticket_id\": \"X-1\", \"comment_text\": \"c\"}"}`:                    core.ToolRequestTypeCreate,
	}
	for input, want := range v1 {
		got, err := TicketMaster{}.InferToolRequestType(ctx, "", input)
		require.NoError(t, err)
		assert.Equal(t, want, got, "v1 %s", input)
	}
}

// One "yes" covers only that exact ticket change or automation, not the next.
func TestTicketAndAutomationApprovalsArePerCall(t *testing.T) {
	a := `{"command":"{\"operation_type\":\"create_ticket\",\"title\":\"a\"}"}`
	b := `{"command":"{\"operation_type\":\"create_ticket\",\"title\":\"b\"}"}`
	assert.NotEqual(t, TicketMasterV2{}.ConfirmationKey(a), TicketMasterV2{}.ConfirmationKey(b))
	assert.Equal(t, TicketMasterV2{}.ConfirmationKey(a), TicketMasterV2{}.ConfirmationKey(a), "resume finds the same approval")
	assert.NotEqual(t, TicketMaster{}.ConfirmationKey(a), TicketMaster{}.ConfirmationKey(b))
	assert.NotEqual(t, WorkflowCreateTool{}.ConfirmationKey(`{"definition":{"name":"a"}}`), WorkflowCreateTool{}.ConfirmationKey(`{"definition":{"name":"b"}}`))

	got, err := WorkflowCreateTool{}.InferToolRequestType(security.NewRequestContextForSuperAdmin(), "", `{"definition":{}}`)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeCreate, got)
}
