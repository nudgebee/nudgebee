package core

import (
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

func TestBuildEvidenceIndex(t *testing.T) {
	// Empty → renders nothing.
	assert.Empty(t, buildEvidenceIndex(nil))
	assert.Empty(t, buildEvidenceIndex([]FileEvidenceRef{}))

	idx := buildEvidenceIndex([]FileEvidenceRef{
		{ToolName: "fetch_logs", File: "logs_kubectl_1786013376013173000.txt", Description: "Raw log data from kubectl"},
		{ToolName: "fetch_metrics", File: "metrics_1786013419661713000.json"},
	})
	// Names the EXACT files (the fix for hallucinated filenames).
	assert.Contains(t, idx, "logs_kubectl_1786013376013173000.txt")
	assert.Contains(t, idx, "metrics_1786013419661713000.json")
	assert.Contains(t, idx, "Raw log data from kubectl")
	// Carries the imperative not to guess / re-run.
	assert.Contains(t, idx, "do NOT guess")
}

// fakeFileRefDao satisfies IConversationDao by embedding it and overrides only
// ListConversationFileRefs — the cross-message evidence source.
type fakeFileRefDao struct {
	IConversationDao
	refs []FileEvidenceRef
}

func (d *fakeFileRefDao) ListConversationFileRefs(accountId, conversationId string, limit int) ([]FileEvidenceRef, error) {
	return d.refs, nil
}

// TestFetchEvidenceIndex_CrossMessage validates use case #2: a new message in a
// conversation is handed the exact files earlier messages saved. Deterministic —
// a fake DAO stands in for the DB, so no live backend is needed.
func TestFetchEvidenceIndex_CrossMessage(t *testing.T) {
	origDao := GetConversationDao()
	origFlag := config.Config.LlmServerFsEvidenceRecallEnabled
	t.Cleanup(func() {
		SetConversationDao(origDao)
		config.Config.LlmServerFsEvidenceRecallEnabled = origFlag
	})

	SetConversationDao(&fakeFileRefDao{refs: []FileEvidenceRef{
		{ToolName: "fetch_logs", File: "logs_kubectl_1786013376013173000.txt", Description: "Raw log data from kubectl"},
	}})

	// A follow-up message in an existing conversation.
	req := NBAgentRequest{AccountId: "acc-1", ConversationId: "11111111-1111-1111-1111-111111111111"}

	// Flag ON → the prior message's file is surfaced by its exact name.
	config.Config.LlmServerFsEvidenceRecallEnabled = true
	idx := fetchEvidenceIndex(nil, req)
	assert.Contains(t, idx, "logs_kubectl_1786013376013173000.txt",
		"a new message must see the exact file an earlier message saved")

	// Flag OFF → nothing injected.
	config.Config.LlmServerFsEvidenceRecallEnabled = false
	assert.Empty(t, fetchEvidenceIndex(nil, req), "flag off must inject no index")

	// Flag ON but no conversation (first message) → nothing to recall.
	config.Config.LlmServerFsEvidenceRecallEnabled = true
	assert.Empty(t, fetchEvidenceIndex(nil, NBAgentRequest{AccountId: "acc-1"}),
		"first message (no conversation id) has no prior evidence")
}

// sanity: the index block is compact enough to ride the uncached human message.
func TestBuildEvidenceIndex_Bounded(t *testing.T) {
	refs := make([]FileEvidenceRef, fsEvidenceIndexMaxFiles)
	for i := range refs {
		refs[i] = FileEvidenceRef{ToolName: "fetch_logs", File: "logs_x.txt"}
	}
	idx := buildEvidenceIndex(refs)
	assert.LessOrEqual(t, strings.Count(idx, "\n"), fsEvidenceIndexMaxFiles+2,
		"one line per file plus the header")
}
