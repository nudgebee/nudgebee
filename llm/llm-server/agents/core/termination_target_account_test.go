package core

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A metrics/traces sub-agent runs under a delegated target's account, so the
// account-scoped message lookup finds no row; stopping the conversation must
// still stop it, and the lookup must not fail the sub-agent.
type targetAccountMessageDao struct {
	IConversationDao
	status ConversationStatus
}

func (targetAccountMessageDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{}, fmt.Errorf("history: failed to scan message: %w", sql.ErrNoRows)
}

func (d targetAccountMessageDao) GetConversationMessageStatus(_, _ string) (ConversationStatus, error) {
	return d.status, nil
}

func TestTerminationCheckUnderTargetAccount(t *testing.T) {
	previous := GetConversationDao()
	t.Cleanup(func() { SetConversationDao(previous) })

	SetConversationDao(targetAccountMessageDao{status: ConversationStatusInProgress})
	terminated, err := CheckMessageTerminationStatus(uuid.NewString(), "target-account", "conversation")
	require.NoError(t, err, "a sub-agent under the target's account must not fail on the lookup")
	require.False(t, terminated)

	SetConversationDao(targetAccountMessageDao{status: ConversationStatusTerminated})
	terminated, err = CheckMessageTerminationStatus(uuid.NewString(), "target-account", "conversation")
	require.NoError(t, err)
	require.True(t, terminated, "stopping the conversation still stops the sub-agent")
}
