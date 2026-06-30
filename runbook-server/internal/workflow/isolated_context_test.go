package workflow

import (
	"testing"

	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
)

// newIsolatedTaskContext backs the "Run Task" tester and ListMCPTools. It must
// NOT fabricate a workflow identity: a leaked unit-test mock previously stamped
// a random uuid workflow id and the literal name "trigger-task" onto every
// isolated run, which surfaced as Slack/Teams notification footers linking to a
// bogus /workflow/<random-uuid> URL. The id, name and run id must stay empty so
// downstream consumers (notification footers, GitOps PR links) omit the link.
func TestNewIsolatedTaskContext_CarriesNoWorkflowIdentity(t *testing.T) {
	service := newVersioningService(new(MockWorkflowStore), &MockTemporalClient{})
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	taskCtx := service.newIsolatedTaskContext(sc, "test-account")

	assert.Equal(t, "", taskCtx.GetWorkflowID(), "isolated runs must not fabricate a workflow id")
	assert.Equal(t, "", taskCtx.GetWorkflowName(), "isolated runs must not fabricate a workflow name")
	assert.Equal(t, "", taskCtx.GetWorkflowRunID(), "isolated runs have no workflow run")
	// Real identity that the isolated run legitimately carries is preserved.
	assert.Equal(t, "test-account", taskCtx.GetAccountID())
	assert.Equal(t, "test-tenant", taskCtx.GetTenantID())
}
