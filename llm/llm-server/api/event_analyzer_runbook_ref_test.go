package api

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

// runbookRefDao embeds IConversationDao so it satisfies the interface while
// overriding only SaveAgentReferences. Any other method would nil-panic —
// intentional: saveEventRunbookReference must not touch anything else.
type runbookRefDao struct {
	core.IConversationDao
	saved                          []core.AgentReference
	accountID, convID, msgID, agID string
}

func (d *runbookRefDao) SaveAgentReferences(accountId, conversationId, messageId, agentId string, references []core.AgentReference) error {
	d.accountID, d.convID, d.msgID, d.agID = accountId, conversationId, messageId, agentId
	d.saved = append(d.saved, references...)
	return nil
}

func TestSaveEventRunbookReference(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	setDao := func(t *testing.T) *runbookRefDao {
		t.Helper()
		dao := &runbookRefDao{}
		core.SetConversationDao(dao)
		// nil resets the singleton; the next GetConversationDao rebuilds the
		// real one, so other tests are unaffected.
		t.Cleanup(func() { core.SetConversationDao(nil) })
		return dao
	}

	resp := core.NBAgentResponse{ConversationId: "conv-1", MessageId: "msg-1", AgentId: "agent-1"}
	ref := &tools.RunbookRef{
		Title:  "NBLLM Agent Latency P95 High — Runbook",
		URL:    "https://example.atlassian.net/wiki/pages/113836034",
		Source: "confluence",
		Text:   "Step 1 — Rule out the histogram artifact.",
	}

	t.Run("resolved runbook is saved as a knowledge_base reference", func(t *testing.T) {
		dao := setDao(t)
		saveEventRunbookReference(ctx, "acct-1", resp, ref)
		assert.Len(t, dao.saved, 1)
		got := dao.saved[0]
		assert.Equal(t, core.AgentReferenceTypeKB, got.Type)
		assert.True(t, strings.HasPrefix(got.ReferenceID, "runbook:"))
		assert.Equal(t, ref.URL, got.Metadata["url"])
		assert.Equal(t, ref.Title, got.Metadata["subject"])
		assert.Equal(t, ref.Title, got.Metadata["name"])
		assert.Equal(t, "confluence", got.Metadata["source"])
		assert.Equal(t, "event_runbook", got.Metadata["via"])
		assert.Equal(t, "Step 1 — Rule out the histogram artifact.", got.Metadata["content"])
		assert.Equal(t, "acct-1", dao.accountID)
		assert.Equal(t, "conv-1", dao.convID)
		assert.Equal(t, "msg-1", dao.msgID)
		assert.Equal(t, "agent-1", dao.agID)
	})

	t.Run("same url produces the same reference id across saves", func(t *testing.T) {
		dao := setDao(t)
		saveEventRunbookReference(ctx, "acct-1", resp, ref)
		saveEventRunbookReference(ctx, "acct-1", resp, ref)
		assert.Len(t, dao.saved, 2)
		assert.Equal(t, dao.saved[0].ReferenceID, dao.saved[1].ReferenceID)
	})

	t.Run("title falls back to the url when empty", func(t *testing.T) {
		dao := setDao(t)
		saveEventRunbookReference(ctx, "acct-1", resp, &tools.RunbookRef{URL: "https://runbooks.example.com/x", Source: "public"})
		assert.Len(t, dao.saved, 1)
		assert.Equal(t, "https://runbooks.example.com/x", dao.saved[0].Metadata["name"])
	})

	t.Run("nil ref, empty url, and missing conversation are all no-ops", func(t *testing.T) {
		dao := setDao(t)
		saveEventRunbookReference(ctx, "acct-1", resp, nil)
		saveEventRunbookReference(ctx, "acct-1", resp, &tools.RunbookRef{Title: "t", URL: "  "})
		saveEventRunbookReference(ctx, "acct-1", core.NBAgentResponse{}, ref)
		assert.Empty(t, dao.saved)
	})
}
