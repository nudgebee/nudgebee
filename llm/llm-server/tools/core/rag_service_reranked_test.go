package core

import (
	"context"
	"net/http"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

type cancelledKnowledgeTransport struct{ observed chan struct{} }

func (t cancelledKnowledgeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	close(t.observed)
	return nil, r.Context().Err()
}

func TestQueryRAGRerankedContextCancellation(t *testing.T) {
	previous := ragClient
	previousURL := config.Config.RAGServerUrl
	config.Config.RAGServerUrl = "http://rag.test/"
	t.Cleanup(func() { ragClient = previous; config.Config.RAGServerUrl = previousURL })
	observed := make(chan struct{})
	ragClient = &http.Client{Transport: cancelledKnowledgeTransport{observed: observed}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.Empty(t, QueryRAGRerankedContext(ctx, "user", "account", "question", "knowledge_base", 8, "conversation", "message", "agent", false))
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("HTTP transport did not observe discovery cancellation")
	}
}

// The per-call rerank opt-in must serialize use_reranking=true, and the plain
// request must OMIT the field entirely — an explicit false would override the
// server-side RAG_RERANKING_ENABLED default for every existing caller.
func TestRagQueryRequestRerankingSerialization(t *testing.T) {
	yes := true
	track := true
	withFlag, err := common.MarshalJson(ragQueryRequest{
		AccountID: "acct", Query: "q", Module: "knowledge_base",
		NumberOfResults: 8, TrackTokenUsage: &track, UseReranking: &yes,
	})
	assert.NoError(t, err)
	assert.Contains(t, string(withFlag), `"use_reranking":true`)

	without, err := common.MarshalJson(ragQueryRequest{
		AccountID: "acct", Query: "q", Module: "knowledge_base",
		NumberOfResults: 8, TrackTokenUsage: &track,
	})
	assert.NoError(t, err)
	assert.NotContains(t, string(without), "use_reranking")
}
