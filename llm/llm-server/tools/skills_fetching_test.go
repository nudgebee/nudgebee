package tools

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func TestSkillFetching_IntegrationIsolationAndQueryCache(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "conv", MessageId: "msg"}
	a, b := "integration-a", "integration-b"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Account    string `json:"account_id"`
			Collection string `json:"collection_name"`
			Restrict   bool   `json:"restrict_to_collection"`
			Query      string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		assert.Equal(t, ctx.AccountId, req.Account)
		assert.True(t, req.Restrict)
		_ = json.NewEncoder(w).Encode(core.RAGSearchResults{
			{Document: req.Collection + ":" + req.Query, Metadata: map[string]any{"collection": req.Collection}},
			{Document: "WRONG-COLLECTION", Metadata: map[string]any{"collection": "foreign_knowledge_base"}},
		})
	}))
	defer server.Close()
	old := config.Config.RAGServerUrl
	config.Config.RAGServerUrl = server.URL
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	for _, query := range []string{"first question", "second question", "first question"} {
		ctx.Query = query
		results := map[string]skillData{"a": {ID: "kb-a", KBType: "integration", IntegrationID: &a}, "b": {ID: "kb-b", KBType: "integration", IntegrationID: &b}}
		enrichIntegrationSkillsFromRAG(ctx, results)
		require.Len(t, results, 2)
		assert.Equal(t, "integration-a_knowledge_base:"+query, results["a"].Data)
		assert.Equal(t, "integration-b_knowledge_base:"+query, results["b"].Data)
		_, cached := common.CacheGet(core.CacheNamespaceLlmSkillContent, "skill:"+ctx.AccountId+":a")
		assert.False(t, cached, "query output must not enter name cache")
	}
	assert.Equal(t, int32(6), calls.Load(), "legacy loads recheck scoped retrieval instead of retaining query snapshots")
}

func TestSkillFetching_IntegrationMissingIsNotEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	old := config.Config.RAGServerUrl
	config.Config.RAGServerUrl = server.URL
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name()}
	results := map[string]skillData{"empty": {ID: "empty", KBType: "integration"}, "manual": {ID: "manual", Data: "keep"}}
	enrichIntegrationSkillsFromRAG(ctx, results)
	assert.NotContains(t, results, "empty")
	assert.Equal(t, "keep", results["manual"].Data)
}

func TestSkillFetching_LegacyExcerptIsBoundedAndHonest(t *testing.T) {
	useLegacySkillLoading(t)
	old := config.Config.LlmServerMaxSkillContentLength
	config.Config.LlmServerMaxSkillContentLength = 31
	t.Cleanup(func() { config.Config.LlmServerMaxSkillContentLength = old })
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "conv", MessageId: "msg"}
	body := strings.Repeat("診断<&> step\n", 30)
	menu := cacheSearchKnowledgeCandidates(ctx, core.RAGSearchResults{{Document: body, Metadata: map[string]any{"url": "https://example.test/runbook"}}})
	id := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(menu[0])[1]
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": id}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	page := regexp.MustCompile(`(?s)<content>\n(.*?)\n</content>`).FindStringSubmatch(resp.Data)
	require.Len(t, page, 2)
	text := html.UnescapeString(page[1])
	require.True(t, utf8.ValidString(text))
	require.LessOrEqual(t, len(text), 31)
	require.Contains(t, resp.Data, "Only a bounded excerpt")
	require.NotContains(t, resp.Data, "next_offset")
}

func TestSkillFetching_IgnoresLegacyIntegrationNameCache(t *testing.T) {
	db, mock := skillFetchingDB(t)
	defer func() { _ = db.Close() }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name()}
	require.NoError(t, common.CacheSet(core.CacheNamespaceLlmSkillContent, "skill:"+ctx.AccountId+":legacy", []byte(`{"id":"old","kb_type":"integration","data":"WRONG-OLD-QUERY"}`)))
	mock.ExpectQuery(`SELECT kb.id, kb.name`).WithArgs(ctx.AccountId, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "description", "kb_type", "kb_source", "integration_id", "note_category"}).AddRow("fresh", "legacy", "", "", "integration", "confluence", "integration-a", "sop"))
	results, _ := (LoadSkillsTool{}).fetchSkillsBatch(ctx, &common.DatabaseManager{Db: db}, []string{"legacy"})
	assert.Empty(t, results["legacy"].Data)
	assert.Equal(t, "fresh", results["legacy"].ID)
	assert.Equal(t, core.KnowledgePurposeReference, results["legacy"].Purpose)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSkillFetching_ManualCandidateUsesIDNotConflictingName(t *testing.T) {
	useLegacySkillLoading(t)
	db, mock := skillFetchingDB(t)
	defer func() { _ = db.Close() }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "conv", MessageId: "msg"}
	require.NoError(t, common.CacheSet(core.CacheNamespaceLlmSkillContent, "skill:"+ctx.AccountId+":runbook", []byte(`{"id":"other","kb_type":"manual","data":"WRONG-NAME-CACHE"}`)))
	id := "knowledge:manual-canonical"
	require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, core.KnowledgeCandidate{ID: id, KBID: "selected", Title: "Runbook", Content: "old snapshot"}))
	mock.ExpectQuery(`SELECT id, name, LEFT.* FROM llm_knowledgebases`).WithArgs(ctx.AccountId, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "note_category"}).AddRow("selected", "Runbook", "RIGHT-CONTENT", "sop"))
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": id}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "RIGHT-CONTENT")
	assert.Contains(t, resp.Data, "Procedure: follow applicable steps")
	assert.NotContains(t, resp.Data, "WRONG-NAME-CACHE")
	require.Len(t, resp.References, 1)
	assert.Equal(t, "selected", resp.References[0].Url)
	// Disabled/deleted/foreign candidates cannot keep serving their cached bodies.
	mock.ExpectQuery(`SELECT id, name, LEFT.* FROM llm_knowledgebases`).WithArgs(ctx.AccountId, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "note_category"}))
	resp, err = (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": id}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSkillFetching_OversizedContentDoesNotPromiseContinuation(t *testing.T) {
	useLegacySkillLoading(t)
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name()}
	db, mock := skillFetchingDB(t)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT kb.id, kb.name`).WithArgs(ctx.AccountId, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "description", "kb_type", "kb_source", "integration_id", "note_category"}).AddRow("large", "large", strings.Repeat("x", core.MaxKnowledgeCandidateBytes+1), "", "manual", nil, nil, "fact"))
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": "large"}})
	require.NoError(t, err)
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	assert.Contains(t, resp.Data, "Only a bounded excerpt")
	assert.NotContains(t, resp.Data, "next_offset")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Existing service tests keep a singleton manager. Swap its DB for this test
// and restore it, rather than registering a hook ignored after initialization.
func skillFetchingDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	db := sqlx.NewDb(raw, "postgresql")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) { return &common.DatabaseManager{Db: db}, nil })
	manager, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	old := manager.Db
	manager.Db = db
	t.Cleanup(func() { manager.Db = old })
	return db, mock
}

func TestSkillFetching_ManualNameCacheCannotBypassDisabledRow(t *testing.T) {
	useLegacySkillLoading(t)
	db, mock := skillFetchingDB(t)
	defer func() { _ = db.Close() }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name()}
	require.NoError(t, common.CacheSet(core.CacheNamespaceLlmSkillContent, "skill:"+ctx.AccountId+":disabled", []byte(`{"id":"old","kb_type":"manual","data":"STALE-CONTENT"}`)))
	mock.ExpectQuery(`SELECT kb.id, kb.name`).WithArgs(ctx.AccountId, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "data", "description", "kb_type", "kb_source", "integration_id", "note_category"}))
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": "disabled"}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusError, resp.Status)
	require.NotContains(t, resp.Data, "STALE-CONTENT")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Legacy excerpt/candidate tests exercise the explicit workspace opt-out.
func useLegacySkillLoading(t *testing.T) {
	t.Helper()
	old := config.Config.LlmServerKnowledgeWorkspaceEnabled
	config.Config.LlmServerKnowledgeWorkspaceEnabled = false
	t.Cleanup(func() { config.Config.LlmServerKnowledgeWorkspaceEnabled = old })
}
