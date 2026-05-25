package api

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

// TestReproStaleFollowupAfterDeadWorkerCleanup demonstrates the production
// failure where a followup message ends up pointing at a deleted agent row.
//
// The race:
//  1. Prod pod processes a message, creates an agent row, saves a followup
//     pointing at the agent, then dies before flipping the human-message
//     status from IN_PROGRESS to WAITING (the two writes in
//     followup.go:287-291 are not in a transaction).
//  2. Dead-worker recovery (syncDeadWorkerMessages) scoops the IN_PROGRESS
//     message and runs HandleConversationMessageRequest, which calls
//     CleanupConversationMessage and deletes every llm_conversation_agent row
//     for that message_id — including the one the followup still references.
//  3. UI replies to the followup with agent_id = followup.parent_agent_id;
//     chains.go's ListConversationAgents returns 0 rows; user sees
//     "api: agent not found".
//
// Run:
//
//	TEST_REPRO_TENANT_ID=<uuid> TEST_REPRO_ACCOUNT_ID=<uuid> TEST_REPRO_USER_ID=<uuid> \
//	  go test -v -run TestReproStaleFollowupAfterDeadWorkerCleanup ./api
func TestReproStaleFollowupAfterDeadWorkerCleanup(t *testing.T) {
	tenantId := os.Getenv("TEST_REPRO_TENANT_ID")
	accountId := os.Getenv("TEST_REPRO_ACCOUNT_ID")
	userId := os.Getenv("TEST_REPRO_USER_ID")
	if tenantId == "" || accountId == "" || userId == "" {
		t.Skip("set TEST_REPRO_TENANT_ID, TEST_REPRO_ACCOUNT_ID, TEST_REPRO_USER_ID")
	}

	dao := core.GetConversationDao()
	if dao == nil {
		t.Fatal("conversation dao not initialized — run with .env loaded")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	assert.NoError(t, err)

	sessionId := "repro-stale-followup-" + uuid.NewString()
	fakeProdWorker := "fake-prod-pod-" + uuid.NewString()[:8]

	t.Cleanup(func() {
		_ = core.DeleteConversationBySession(sessionId, accountId, userId)
	})

	// ---- stage the "dead prod" state ------------------------------------

	convId, err := dao.SaveConversation("", sessionId, tenantId, accountId, userId, "",
		"repro", core.ConversationStatusInProgress,
		core.ConversationSourceUserInvestigation, "", "")
	assert.NoError(t, err)

	humanMsgId, err := dao.SaveConversationMessage("", convId.String(), accountId, userId,
		core.MessageRoleHuman, core.MessageTypeGeneration,
		"please pick a repo", "", "agent_code_2", uuid.Nil,
		nil, "", "", "")
	assert.NoError(t, err)

	agentId, err := dao.SaveConversationAgentCall(convId.String(), humanMsgId.String(),
		accountId, userId, "agent_code_2", "",
		"please pick a repo", "", "", "", toolcore.NBQueryConfig{})
	assert.NoError(t, err)

	followupMsgId, err := dao.SaveConversationMessage("", convId.String(), accountId, userId,
		core.MessageRoleAI, core.MessageTypeFollowup,
		"Which repository?", "", "agent_code_2", agentId,
		map[string]any{"question": "Which repository?", "followupType": "tool_config"},
		"", "", "")
	assert.NoError(t, err)

	// Force human message to IN_PROGRESS with a worker that's not in nb_workers.
	// This is the exact state syncDeadWorkerMessages would see if a prod pod
	// died after saving the followup but before flipping status to WAITING.
	_, err = dbms.Db.Exec(
		`UPDATE llm_conversation_messages SET status=$1, worker_name=$2, updated_at=now() WHERE id=$3`,
		string(core.ConversationStatusInProgress), fakeProdWorker, humanMsgId.String())
	assert.NoError(t, err)

	// ---- ASSERTION 1: sync would pick this up ---------------------------
	//
	// Replicates the eligibility check in syncDeadWorkerMessages: query
	// IN_PROGRESS messages from dead workers, then drop followups and
	// localhost-named workers.
	candidates, err := dao.ListConversationMessages(core.ConversationStatusInProgress, "", "", true)
	assert.NoError(t, err)
	candidates = lo.Filter(candidates, func(m core.ConversationMessage, _ int) bool {
		if m.MessageType == string(core.MessageTypeFollowup) {
			return false
		}
		if m.WorkerName != nil && (*m.WorkerName == "localhost" || *m.WorkerName == "127.0.0.1" ||
			*m.WorkerName == "0.0.0.0" || *m.WorkerName == "::" ||
			strings.Contains(*m.WorkerName, ".local")) {
			return false
		}
		return true
	})
	picked := lo.ContainsBy(candidates, func(m core.ConversationMessage) bool { return m.ID == humanMsgId })
	assert.True(t, picked, "dead-worker sync would scoop our staged 'dead prod' message")

	// ---- ASSERTION 2: cleanup refuses to delete the agent ---------------
	//
	// With the fix in CleanupConversationMessage, the active followup
	// pointing at the agent triggers a sentinel error and the deletion is
	// refused. Without the fix, this call would delete the agent row and
	// orphan the followup.
	err = dao.CleanupConversationMessage(humanMsgId.String(), accountId)
	assert.ErrorIs(t, err, core.ErrCleanupRefusedActiveFollowup,
		"cleanup must refuse and signal the caller; nil would let the caller proceed and duplicate state")

	agents, err := dao.ListConversationAgents("", agentId.String())
	assert.NoError(t, err)
	assert.Len(t, agents, 1,
		"cleanup must preserve agent rows referenced by active followups (regression: this used to be 0 → orphan)")

	// ---- ASSERTION 3: followup pointer stays valid ----------------------
	var followupParentAgent uuid.UUID
	err = dbms.Db.Get(&followupParentAgent,
		`SELECT parent_agent_id FROM llm_conversation_messages WHERE id=$1`,
		followupMsgId.String())
	assert.NoError(t, err)
	assert.Equal(t, agentId, followupParentAgent,
		"followup still points at the preserved agent")

	// ---- ASSERTION 4: chains.go's lookup now succeeds -------------------
	//
	// This is the exact lookup chains.go:437 performs on followup resume.
	// Before the fix it returned 0 rows → "api: agent not found"; with the
	// fix it returns the preserved row and the resume proceeds.
	lookup, err := dao.ListConversationAgents("", followupParentAgent.String())
	assert.NoError(t, err)
	assert.Len(t, lookup, 1,
		"chains.go would now resolve the agent successfully on followup resume")

	t.Logf("fix verified: conv=%s human_msg=%s preserved_agent=%s followup=%s",
		convId, humanMsgId, agentId, followupMsgId)
}
