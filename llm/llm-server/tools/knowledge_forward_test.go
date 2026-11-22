package tools

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/knowledge"

	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func TestKnowledgeForwardSmallCompleteBody(t *testing.T) {
	old := config.Config
	t.Cleanup(func() { config.Config = old })
	config.Config.LlmServerKnowledgeWorkspaceEnabled = true
	// More than the loader's 100-line response limit, but small enough to inline.
	body := strings.Repeat("step\n", 200) + "FINAL-STEP\n"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	rag := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-SHA256", digest)
		_, _ = io.WriteString(w, body)
	}))
	defer rag.Close()
	config.Config.RAGServerUrl = rag.URL + "/"
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: t.Name(), ConversationId: "knowledge-conv", MessageId: "m"}
	candidate := knowledge.KnowledgeCandidate{ID: "knowledge:small", Title: "Steps", Collection: "docs", DocumentID: "point", Version: digest, Bytes: int64(len(body)), Purpose: knowledge.KnowledgePurposeProcedure}
	require.NoError(t, knowledge.StoreKnowledgeCandidate(ctx.AccountId, ctx.ConversationId, ctx.MessageId, candidate))
	inline, ref, err := PrepareKnowledgeForAnalysis(ctx, candidate)
	require.NoError(t, err)
	require.Nil(t, ref)
	require.Contains(t, inline, body)
	require.Contains(t, inline, knowledge.KnowledgePurposeGuidance(knowledge.KnowledgePurposeProcedure))
	config.Config.LlmServerKnowledgeWorkspaceEnabled = false
	_, _, err = PrepareKnowledgeForAnalysis(ctx, candidate)
	require.ErrorContains(t, err, "disabled")
}
