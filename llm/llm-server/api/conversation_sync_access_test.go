package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
)

// restartAccessDao records the writes a restart decision makes.
type restartAccessDao struct {
	core.IConversationDao
	conversation   core.Conversation
	messageUpdates []core.ConversationStatus
	statusUpdates  []core.ConversationStatus
}

func (d *restartAccessDao) GetConversation(string) (core.Conversation, error) {
	return d.conversation, nil
}

func (d *restartAccessDao) UpdateConversationMessage(_ string, _ string, status core.ConversationStatus) error {
	d.messageUpdates = append(d.messageUpdates, status)
	return nil
}

func (d *restartAccessDao) UpdateConversationStatus(_ string, status core.ConversationStatus) error {
	d.statusUpdates = append(d.statusUpdates, status)
	return nil
}

// servicesServerAnswering points the security-context lookup at a stub that
// always answers with status.
func servicesServerAnswering(t *testing.T, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	prev := config.Config
	t.Cleanup(func() { config.Config = prev })
	config.Config.ServiceEndpoint = srv.URL
	config.Config.LlmServerSecurityMode = ""
	config.Config.SecurityContextRetryAttempts = 0
}

func restartFixture() (*restartAccessDao, core.ConversationMessage) {
	account := uuid.New()
	conv := core.Conversation{ID: uuid.New(), TenantID: uuid.New(), AccountID: account, Status: core.ConversationStatusInProgress}
	msg := core.ConversationMessage{ID: uuid.New(), ConversationID: conv.ID, AccountID: account, UserID: uuid.New(), Status: core.ConversationStatusInProgress}
	return &restartAccessDao{conversation: conv}, msg
}

func TestRestart_UnreachableServicesServerLeavesTheMessageForTheNextSweep(t *testing.T) {
	servicesServerAnswering(t, http.StatusServiceUnavailable)
	dao, msg := restartFixture()

	ctx, ok := checkBudgetAndRestartMessage(dao, msg)

	assert.False(t, ok)
	assert.Nil(t, ctx)
	assert.Empty(t, dao.messageUpdates, "an outage must not claim or fail the message")
	assert.Empty(t, dao.statusUpdates)
}

func TestRestart_RejectedSenderFailsTheMessage(t *testing.T) {
	servicesServerAnswering(t, http.StatusBadRequest)
	dao, msg := restartFixture()

	ctx, ok := checkBudgetAndRestartMessage(dao, msg)

	assert.False(t, ok)
	assert.Nil(t, ctx)
	require.Equal(t, []core.ConversationStatus{core.ConversationStatusFailed}, dao.messageUpdates)
	require.Equal(t, []core.ConversationStatus{core.ConversationStatusFailed}, dao.statusUpdates)
}
