package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// followupSyncDao captures what the update-in-place branch of
// syncAgentFollowupMessage writes, and reports one active followup for any agent.
type followupSyncDao struct {
	IConversationDao
	followupMessageID uuid.UUID
	gotRequest        FollowupRequest
	updateCalls       int
}

func (d *followupSyncDao) ListConversationAgents(_, agentId string) ([]ConversationAgent, error) {
	return []ConversationAgent{{
		ID:                uuid.MustParse(agentId),
		FollowupMessageID: d.followupMessageID,
		Status:            AgentExecutionStatusWaiting,
	}}, nil
}

func (d *followupSyncDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{ID: d.followupMessageID, Status: ConversationStatusWaiting}, nil
}

func (d *followupSyncDao) UpdateConversationMessageFollowupConfig(_ string, followupRequest FollowupRequest) error {
	d.gotRequest = followupRequest
	d.updateCalls++
	return nil
}

// The agent-level followup sync runs after the planner has already stored the
// confirmation followup's config, so it overwrites it. It must hand the DAO the
// whole request — dropping confirmationKey records a per-action approval under
// the bare tool name, and the approved apply then dies on the "config not
// resolved" fail-fast gate instead of executing.
func TestSyncAgentFollowupMessage_PreservesConfirmationKey(t *testing.T) {
	agentID := uuid.New()
	// Injected, not swapped into the package-global: SetConversationDao races
	// with fire-and-forget goroutines other tests in this package leave running.
	dao := &followupSyncDao{followupMessageID: uuid.New()}

	followUpRequest := FollowupRequest{
		Question:        "Apply this change via a direct deployment change?",
		FollowupType:    FollowupTypeToolConfirmation,
		FollowupOptions: []string{"yes", "no"},
		AgentName:       "finops",
		AgentId:         agentID,
		ToolName:        "recommendation_apply",
		ToolId:          "recommendation_apply-5e619698",
		ConfirmationKey: "recommendation_apply:0fa70de8d221",
	}

	syncAgentFollowupMessage(
		security.NewRequestContextForSuperAdmin(),
		dao,
		NBAgentRequest{AccountId: "acct-1", ConversationId: "conv-1", MessageId: "msg-1"},
		"finops",
		followUpRequest,
	)

	assert.Equal(t, 1, dao.updateCalls, "the active followup must be updated in place, not duplicated")
	assert.Equal(t, followUpRequest, dao.gotRequest,
		"the update path must hand the DAO the whole request so no field is dropped")

	// What the DAO then stores from it — the config the create path also writes.
	assert.Equal(t, "recommendation_apply:0fa70de8d221", followupMessageConfig(dao.gotRequest)["confirmationKey"])
}
