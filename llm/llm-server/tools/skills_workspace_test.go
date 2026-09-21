package tools

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

func TestKnowledgeSelectiveLargeDocument(t *testing.T) {
	body := strings.Repeat("ordinary line\n", 200000) + "FINAL-CANARY Ω quasar escalation\n"
	version := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	text, err := readKnowledgeLines(strings.NewReader(body), version, "FINAL-CANARY", 1)
	require.NoError(t, err)
	require.Contains(t, text, "200001: FINAL-CANARY")
	require.Less(t, len(text), knowledgeReadBytes)
	text, err = readKnowledgeLines(strings.NewReader(body), version, "", 200001)
	require.NoError(t, err)
	require.Contains(t, text, "FINAL-CANARY")
	_, err = readKnowledgeLines(strings.NewReader(body+"tampered"), version, "FINAL-CANARY", 1)
	require.ErrorContains(t, err, "changed")
}

func TestKnowledgeLongAndBoundaryLines(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 2*1024*1024) + "QUASAR", strings.Repeat("x", 32765) + "QUASAR\n", strings.Repeat("İ", 16380) + "QUASAR"} {
		version := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		text, err := readKnowledgeLines(strings.NewReader(body), version, "QUASAR", 1)
		require.NoError(t, err)
		require.Contains(t, text, "QUASAR")
		require.Less(t, len(text), knowledgeReadBytes)
	}
}

func TestKnowledgeWorkspaceToolRoundTrip(t *testing.T) {
	old := config.Config
	t.Cleanup(func() { config.Config = old })
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	body := strings.Repeat("reference text\n", 200000) + "FINAL-CANARY\n"
	version := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	downloads, uploads, validations := 0, 0, 0
	allowed := true
	var saved string
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "account", req["account_id"])
		require.Equal(t, "docs", req["collection_name"])
		require.Equal(t, "point", req["document_id"])
		if !allowed {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Content-SHA256", version)
		if req["validate_only"] == true {
			validations++
			w.WriteHeader(204)
			return
		}
		downloads++
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	defer rag.Close()
	config.Config.RAGServerUrl = rag.URL + "/"
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			require.Equal(t, "/api/v1/files/save", r.URL.Path)
			uploads++
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "conv", body["conversation_id"])
			require.Regexp(t, `^knowledge/[a-f0-9]{64}\.txt$`, body["path"])
			saved = body["content"]
			_, _ = io.WriteString(w, `{}`)
			return
		}
		require.Equal(t, "conv", r.URL.Query().Get("conversation_id"))
		_, _ = io.WriteString(w, saved)
	}))
	defer ws.Close()
	config.Config.LlmServerWorkspaceLocalUrl = ws.URL
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "account", ConversationId: "conv", MessageId: t.Name()}
	c := core.KnowledgeCandidate{ID: "knowledge:large", Title: "Article", ReferenceID: "https://source/article", Collection: "docs", DocumentID: "point", Version: version, Bytes: int64(len(body))}
	require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c))
	call := func() core.NBToolResponse {
		resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": c.ID, "keyword": "FINAL-CANARY"}})
		require.NoError(t, err)
		return resp
	}
	first := call()
	require.Equal(t, core.NBToolResponseStatusSuccess, first.Status)
	require.Contains(t, fmt.Sprint(first.Data), "FINAL-CANARY")
	require.Len(t, first.References, 2)
	cached, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c.ID)
	require.True(t, ok)
	require.Empty(t, cached.Content)
	require.NotEmpty(t, cached.Path)
	require.Equal(t, core.NBToolResponseStatusSuccess, call().Status)
	require.Equal(t, 1, downloads)
	require.Equal(t, 1, uploads)
	require.Equal(t, 1, validations)
	allowed = false
	require.Equal(t, core.NBToolResponseStatusError, call().Status)
	allowed = true
	saved += "changed"
	require.Equal(t, core.NBToolResponseStatusError, call().Status)
	ctx.AccountId = "other"
	require.Equal(t, core.NBToolResponseStatusError, call().Status)
}

func TestKnowledgeIdenticalExcerptsRetainDistinctHandles(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "c", MessageId: "m"}
	docs := core.RAGSearchResults{}
	for _, id := range []string{"one", "two"} {
		docs = append(docs, core.RAGSearchResult{Document: "same excerpt", Metadata: map[string]any{"collection": "docs", "url": "same", "retrieval_id": id, "content_sha256": strings.Repeat("a", 64), "kb_id": "external-owner"}})
	}
	menu := strings.Join(cacheSearchKnowledgeCandidates(ctx, docs), "\n")
	ids := regexp.MustCompile(`id="(knowledge:[^"]+)"`).FindAllStringSubmatch(menu, -1)
	require.Len(t, ids, 2)
	require.NotEqual(t, ids[0][1], ids[1][1])
	for _, id := range ids {
		candidate, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, id[1])
		require.True(t, ok)
		require.Empty(t, candidate.KBID)
	}
}

func TestKnowledgeSmallManualHandleRejectsChangedVersion(t *testing.T) {
	old := config.Config.LlmServerKnowledgeWorkspaceEnabled
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	t.Cleanup(func() { config.Config.LlmServerKnowledgeWorkspaceEnabled = old })
	db, mock := skillFetchingDB(t)
	defer func() { _ = db.Close() }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "conv", MessageId: "msg"}
	c := core.KnowledgeCandidate{ID: "knowledge:manual", KBID: "manual", Version: "old", FileSHA256: strings.Repeat("a", 64), Content: "old body"}
	require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c))
	mock.ExpectQuery(`SELECT id, name, COALESCE`).WithArgs(ctx.AccountId, "manual", c.ID).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "kb_type", "integration_id", "md5", "bytes", "note_category"}).AddRow("manual", "Manual", "manual", nil, "new", 8, "sop"))
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": c.ID}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusError, resp.Status)
	require.Contains(t, resp.Data, "changed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Optional local cross-service check using the real RAG controller and workspace
// file handler. Only the indexed source store is a fixture; no LLM is required.
func TestKnowledgeLocalServices(t *testing.T) {
	ragURL, workspaceURL := os.Getenv("KNOWLEDGE_E2E_RAG_URL"), os.Getenv("KNOWLEDGE_E2E_WORKSPACE_URL")
	if ragURL == "" || workspaceURL == "" {
		t.Skip("local knowledge services not configured")
	}
	old := config.Config
	t.Cleanup(func() { config.Config = old })
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	config.Config.RAGServerUrl = ragURL
	config.Config.LlmServerWorkspaceLocalUrl = workspaceURL
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "account", ConversationId: "knowledge-local-e2e", MessageId: t.Name()}
	docs := core.QueryRAG("user", ctx.AccountId, "QUASAR escalation", ragKBModule, 1, ctx.ConversationId, ctx.MessageId, "", false)
	require.Len(t, docs, 1)
	require.LessOrEqual(t, len(docs[0].Document), core.KnowledgeExcerptBytes)
	menu := strings.Join(cacheSearchKnowledgeCandidates(ctx, docs), "\n")
	id := regexp.MustCompile(`knowledge:[a-f0-9]+`).FindString(menu)
	require.NotEmpty(t, id)
	resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": id, "keyword": "QUASAR"}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, fmt.Sprint(resp.Data))
	require.Contains(t, resp.Data, "PAY-ESC-2")
	require.Less(t, len(fmt.Sprint(resp.Data)), knowledgeReadBytes)
	require.Len(t, resp.References, 2)
	cached, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, id)
	require.True(t, ok)
	require.Empty(t, cached.Content)
	again, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": id, "start_line": 150001}})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, again.Status)
	require.Contains(t, again.Data, "PAY-ESC-2")
	t.Logf("indexed bytes=%d discovery bytes=%d response bytes=%d cached body bytes=%d file=%s", cached.Bytes, len(docs[0].Document), len(fmt.Sprint(resp.Data)), len(cached.Content), cached.Path)
}

type failedKnowledgeWorkspace struct{ workspace.WorkspaceManager }

func (failedKnowledgeWorkspace) CallAPIOrLazyCreate(*security.RequestContext, string, string, string, map[string]string, any) ([]byte, error) {
	return nil, fmt.Errorf("workspace upload unavailable")
}
func TestKnowledgeFailedUploadDoesNotPublishHandle(t *testing.T) {
	old := config.Config.RAGServerUrl
	t.Cleanup(func() { config.Config.RAGServerUrl = old })
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	body := strings.Repeat("x", core.KnowledgeExcerptBytes+1)
	version := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-SHA256", version)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	config.Config.RAGServerUrl = server.URL + "/"
	c := core.KnowledgeCandidate{Collection: "docs", DocumentID: "point", Version: version, Bytes: int64(len(body))}
	err := materializeKnowledge(core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: "account", ConversationId: "conv"}, failedKnowledgeWorkspace{}, &c)
	require.ErrorContains(t, err, "upload unavailable")
	require.Empty(t, c.Path)
	files, err := os.ReadDir(temp)
	require.NoError(t, err)
	require.Empty(t, files)
}

// A manual source uses character offsets in SQL, while transfer limits and
// hashes use UTF-8 bytes. Revoke the row between chunks to verify fail-closed.
func TestKnowledgeManualChunkMaterialization(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			db, mock := skillFetchingDB(t)
			defer func() { _ = db.Close() }()
			body := strings.Repeat("reference Ω\n", 30000) + "FINAL-MANUAL-CANARY"
			c := core.KnowledgeCandidate{KBID: "manual", Collection: "kb_manual", Version: "db-version", Bytes: int64(len(body))}
			ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "conv"}
			runes := []rune(body)
			const chunk = 256 * 1024
			for offset := 0; offset <= len(runes); offset += chunk {
				expectation := mock.ExpectQuery(`SELECT substring.*status='active' AND enabled.*md5`).WithArgs(ctx.AccountId, c.KBID, offset+1, chunk, c.Version)
				if revoke && offset > 0 {
					expectation.WillReturnError(sql.ErrNoRows)
					break
				}
				expectation.WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow(string(runes[offset:min(offset+chunk, len(runes))])))
				if offset+chunk >= len(runes) {
					mock.ExpectQuery(`SELECT substring`).WithArgs(ctx.AccountId, c.KBID, offset+chunk+1, chunk, c.Version).WillReturnRows(sqlmock.NewRows([]string{"data"}).AddRow(""))
					break
				}
			}
			wm := &knowledgeUploadCapture{}
			err := materializeKnowledge(ctx, wm, &c)
			if revoke {
				require.ErrorContains(t, err, "unavailable")
				require.Empty(t, c.Path)
				require.Zero(t, wm.bytes)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(len(body)), wm.bytes)
				require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(body))), c.FileSHA256)
				require.Empty(t, c.Content)
				require.NotEmpty(t, c.Path)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

type knowledgeUploadCapture struct {
	workspace.WorkspaceManager
	bytes int64
}

func (w *knowledgeUploadCapture) CallAPIOrLazyCreate(_ *security.RequestContext, _ string, _ string, _ string, _ map[string]string, body any) ([]byte, error) {
	w.bytes = int64(len(body.(map[string]string)["content"]))
	return nil, nil
}

func TestKnowledgeWorkspaceRefreshesManualPurpose(t *testing.T) {
	old := config.Config
	t.Cleanup(func() { config.Config = old })
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	body := "Check prerequisites before proceeding.\n"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = io.WriteString(w, body)
	}))
	defer ws.Close()
	config.Config.LlmServerWorkspaceLocalUrl = ws.URL
	db, mock := skillFetchingDB(t)
	defer func() { _ = db.Close() }()
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "conv", MessageId: "msg"}
	c := core.KnowledgeCandidate{ID: "knowledge:purpose", KBID: "manual", Version: "same-body", Path: "knowledge/existing.txt", FileSHA256: digest, Purpose: core.KnowledgePurposeReference}
	require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c))
	for _, category := range []string{"sop", "fact"} {
		mock.ExpectQuery(`SELECT id, name, COALESCE`).WithArgs(ctx.AccountId, "manual", c.ID).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "kb_type", "integration_id", "md5", "bytes", "note_category"}).AddRow("manual", "Manual", "manual", nil, "same-body", len(body), category))
		resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": c.ID}})
		require.NoError(t, err)
		require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
		guidance := core.KnowledgePurposeGuidance(core.KnowledgePurpose("manual", category))
		require.Contains(t, resp.Data, guidance)
		require.Len(t, resp.References, 2)
		require.Contains(t, resp.References[1].Description, guidance)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKnowledgeProcedureReadsPastHundredLinesWithinByteBudget(t *testing.T) {
	body := strings.Repeat("step\n", 110) + "Scope: SQL only; no metrics delegation.\n"
	version := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	text, err := readKnowledgeLinesForPurpose(strings.NewReader(body), version, "", 1, core.KnowledgePurposeProcedure)
	require.NoError(t, err)
	require.Contains(t, text, "Scope: SQL only")
	require.NotContains(t, text, "Read output limited")
	reference, err := readKnowledgeLines(strings.NewReader(body), version, "", 1)
	require.NoError(t, err)
	require.NotContains(t, reference, "Scope: SQL only")
	large := strings.Repeat("a longer procedure step\n", 2000)
	text, err = readKnowledgeLinesForPurpose(strings.NewReader(large), fmt.Sprintf("%x", sha256.Sum256([]byte(large))), "", 1, core.KnowledgePurposeProcedure)
	require.NoError(t, err)
	require.LessOrEqual(t, len(text), knowledgeReadBytes)
	require.Contains(t, text, "Read output limited")
}
