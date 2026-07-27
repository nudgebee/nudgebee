package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The instant_message branch of Execute needs a live notification service, so
// these tests cover the contract that workflow authors actually template
// against: the declared schema. Both fields below have already caused silent
// production failures, so they are worth pinning.

func TestApprovalTask_InputSchema_SupportsThreadedPrompt(t *testing.T) {
	schema := (&ApprovalTask{}).InputSchema()
	require.NotNil(t, schema)

	prop, ok := schema.Properties["message_thread_id"]
	require.True(t, ok, "message_thread_id must be declared so the form and templating expose it")
	assert.Equal(t, "string", string(prop.Type))
	assert.False(t, prop.Required, "threading is opt-in; omitting it must keep the prompt top-level")
	require.NotNil(t, prop.VisibleWhen, "threading only applies to instant_message approvals")
	assert.Contains(t, prop.VisibleWhen.Value, "instant_message")
}

// Regression guard for a silent failure: the schema used to document status as
// "(approved/rejected)" while Execute emits the chosen approval_options value
// ("approve"/"reject"). Authors copied the doc into `if status == 'approved'`
// conditions that never fired — the approval succeeded, the workflow completed,
// and the gated task was skipped with no error raised anywhere.
func TestApprovalTask_OutputSchema_StatusDocumentsOptionValues(t *testing.T) {
	schema := (&ApprovalTask{}).OutputSchema()
	require.NotNil(t, schema)

	status, ok := schema.Properties["status"]
	require.True(t, ok)
	assert.NotContains(t, status.Description, "approved/rejected",
		"status carries the approval_options value, not a past-tense form")
	assert.Contains(t, status.Description, "approve")

	// approver is populated from the caller's security context and is empty on
	// the Slack button path, so it must not be advertised as guaranteed.
	approver, ok := schema.Properties["approver"]
	require.True(t, ok)
	assert.False(t, approver.Required, "approver is best-effort and may be empty")
}

// The default options are what `status` will contain for any workflow that does
// not override approval_options — the values conditions must be written against.
func TestApprovalTask_DefaultApprovalOptions(t *testing.T) {
	schema := (&ApprovalTask{}).InputSchema()
	opts, ok := schema.Properties["approval_options"]
	require.True(t, ok)
	assert.Equal(t, []any{"approve", "reject"}, opts.Default)
}
