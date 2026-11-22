package workflow

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/mocks"

	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"
)

// completedWith completes an approval through complete and returns the output
// the approval step finished with.
func completedWith(t *testing.T, complete func(s *Service, token string) error) map[string]any {
	t.Helper()
	temporal := mocks.NewClient(t)
	var output map[string]any
	temporal.On("CompleteActivity", mock.Anything, []byte("task-token"), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { output = args.Get(2).(map[string]any) }).Return(nil).Once()
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry(), temporalClient: temporal}
	require.NoError(t, complete(s, hex.EncodeToString([]byte("task-token"))))
	return output
}

func TestALinkDecisionNamesTheAddressItWasSentTo(t *testing.T) {
	output := completedWith(t, func(s *Service, token string) error {
		return s.CompleteApprovalTaskFromLink(security.NewRequestContextForSuperAdmin(), token, "approve", "dana@example.com", nil)
	})
	assert.Equal(t, "approve", output["status"])
	assert.Equal(t, "email:dana@example.com", output["approver"])
}

func TestAnInAppDecisionStillNamesTheCaller(t *testing.T) {
	output := completedWith(t, func(s *Service, token string) error {
		return s.CompleteApprovalTask(security.NewRequestContextForTenantAccountAdmin("t1", "person-1", []string{"acct"}), token, "reject", nil)
	})
	assert.Equal(t, "person-1", output["approver"])
}
