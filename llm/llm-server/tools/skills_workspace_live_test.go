package tools

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// Reads existing dev knowledge into an isolated conversation on the local workspace.
func TestKnowledgeExistingWorkspaceLive(t *testing.T) {
	if os.Getenv("RUN_KNOWLEDGE_EXISTING_WORKSPACE") != "true" {
		t.Skip("local workspace and forwarded RAG required")
	}
	old := config.Config
	defer func() { config.Config = old }()
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), AccountId: os.Getenv("TEST_ACCOUNT"), UserId: os.Getenv("TEST_USER"), ConversationId: uuid.NewString(), MessageId: uuid.NewString()}
	require.NotEmpty(t, ctx.AccountId)
	wm := workspace.NewWorkspaceManagerWithTimeout(30 * time.Second)
	// Create only the test-owned conversation directory through the old execution API.
	_, err := wm.ExecuteCommand(ctx.Ctx, ctx.AccountId, ctx.ConversationId, "pwd", nil)
	require.NoError(t, err)
	t.Run("manual", func(t *testing.T) {
		resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": "payments_oom_runbook"}})
		require.NoError(t, err)
		require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, fmt.Sprint(resp.Data))
		require.Contains(t, fmt.Sprint(resp.Data), "QUASAR")
		t.Logf("manual load passed, response bytes=%d", len(fmt.Sprint(resp.Data)))
	})
	docs := core.QueryRAG(ctx.UserId, ctx.AccountId, "Confluence Create Ticket Action runbook", ragKBModule, 5, ctx.ConversationId, ctx.MessageId, "", false)
	require.NotEmpty(t, docs)
	for _, doc := range docs {
		menu := strings.Join(cacheSearchKnowledgeCandidates(ctx, core.RAGSearchResults{doc}), "\n")
		id := regexp.MustCompile(`knowledge:[a-f0-9]+`).FindString(menu)
		candidate, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, id)
		if !ok || candidate.KBID != "" || candidate.Bytes <= core.KnowledgeExcerptBytes {
			continue
		}
		resp, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": id}})
		require.NoError(t, err)
		require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, fmt.Sprint(resp.Data))
		saved, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, id)
		require.True(t, ok)
		require.NotEmpty(t, saved.Path)
		defer func() { require.NoError(t, wm.DeleteFile(ctx.Ctx, ctx.AccountId, ctx.ConversationId, saved.Path)) }()
		require.Empty(t, saved.Content)
		require.Len(t, resp.References, 2)
		shell, err := wm.ExecuteCommand(ctx.Ctx, ctx.AccountId, ctx.ConversationId, "sha256sum '"+saved.Path+"'; tail -n 3 '"+saved.Path+"'", nil)
		require.NoError(t, err)
		require.Contains(t, shell, saved.FileSHA256)
		t.Logf("indexed bytes=%d response bytes=%d cached bytes=%d; source and file references retained; shell hash matched; tail=%s", saved.Bytes, len(fmt.Sprint(resp.Data)), len(saved.Content), shell)
		return
	}
	t.Fatal("no indexed candidate above 4 KiB found")
}

// Synthetic source supplies a known tail; the workspace is the real existing pod.
func TestKnowledgeLargeExistingWorkspaceLive(t *testing.T) {
	if os.Getenv("RUN_KNOWLEDGE_EXISTING_WORKSPACE") != "true" {
		t.Skip("local workspace required")
	}
	old := config.Config
	defer func() { config.Config = old }()
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")}), AccountId: os.Getenv("TEST_ACCOUNT"), ConversationId: uuid.NewString(), MessageId: uuid.NewString()}
	wm := workspace.NewWorkspaceManagerWithTimeout(60 * time.Second)
	_, err := wm.ExecuteCommand(ctx.Ctx, ctx.AccountId, ctx.ConversationId, "pwd", nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := wm.DeleteFile(ctx.Ctx, ctx.AccountId, ctx.ConversationId, "knowledge")
		if err != nil && !strings.Contains(err.Error(), "404") {
			t.Errorf("test workspace cleanup: %v", err)
		}
	})
	require.ErrorContains(t, materializeKnowledge(ctx, wm, &core.KnowledgeCandidate{Bytes: core.MaxKnowledgeDocumentBytes + 1}), "32 MiB transfer limit")
	for _, size := range []int{3600019, core.MaxKnowledgeDocumentBytes} {
		tail := "FINAL-CANARY-73629\n"
		padding := size - len(tail)
		body := strings.Repeat("界🙂\n", padding/8) + strings.Repeat("x", padding%8) + tail
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-SHA256", digest)
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(400)
				return
			}
			if req["validate_only"] == true {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = io.WriteString(w, body)
		}))
		defer source.Close()
		config.Config.RAGServerUrl = source.URL + "/"
		c := core.KnowledgeCandidate{ID: core.NewKnowledgeCandidateID(ctx.AccountId, ctx.ConversationId, ctx.MessageId, digest), Collection: "fixture", DocumentID: "point", Version: digest, Bytes: int64(len(body)), Title: "Large fixture", ReferenceID: "fixture-source"}
		require.NoError(t, core.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c))
		response, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": c.ID, "keyword": "FINAL-CANARY"}})
		require.NoError(t, err)
		require.Equal(t, core.NBToolResponseStatusSuccess, response.Status, fmt.Sprint(response.Data))
		require.Contains(t, fmt.Sprint(response.Data), "FINAL-CANARY-73629")
		require.Less(t, len(fmt.Sprint(response.Data)), knowledgeReadBytes)
		cached, ok := core.LoadKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, c.ID)
		require.True(t, ok)
		require.Empty(t, cached.Content)
		require.Len(t, response.References, 2)
		shell, err := wm.ExecuteCommand(ctx.Ctx, ctx.AccountId, ctx.ConversationId, "sha256sum '"+cached.Path+"'; tail -n 1 '"+cached.Path+"'", nil)
		require.NoError(t, err)
		require.Contains(t, shell, digest)
		require.Contains(t, shell, "FINAL-CANARY-73629")

		require.NoError(t, wm.SaveFile(ctx.Ctx, ctx.AccountId, ctx.ConversationId, cached.Path, "tampered test content"))
		rejected, err := (LoadSkillsTool{}).Call(ctx, core.NBToolCallRequest{Arguments: map[string]any{"skill_name": c.ID, "keyword": "FINAL-CANARY"}})
		require.NoError(t, err)
		require.Equal(t, core.NBToolResponseStatusError, rejected.Status)
		require.Contains(t, fmt.Sprint(rejected.Data), "changed or is incomplete")
		require.NoError(t, wm.DeleteFile(ctx.Ctx, ctx.AccountId, ctx.ConversationId, cached.Path))
		t.Logf("bytes=%d selective response=%d cache body=0; shell tail and SHA256 passed", len(body), len(fmt.Sprint(response.Data)))
	}
}
