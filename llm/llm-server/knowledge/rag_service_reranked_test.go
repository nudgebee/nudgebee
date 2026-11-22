package knowledge

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	toolcore "nudgebee/llm/tools/core"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// retrievalOverrideFields are the only inputs BuildRetrievalQuery honours ahead
// of the subject. A query the model wrote beats anything inferred from labels.
func TestBuildRetrievalQueryPrefersModelSuppliedQuery(t *testing.T) {
	got := BuildRetrievalQuery("connection pool saturation in checkout",
		toolcore.NBQueryConfig{Workload: "llm-server", Labels: map[string]any{"alertname": "PodOOMKilled"}},
		"a very long task prompt")
	assert.Equal(t, "connection pool saturation in checkout", got)
}

// The subject must survive the bound: on automated paths the question is a task
// prompt whose opening is shared by every copy, so a question-first query that
// gets truncated keeps boilerplate and drops what identifies the turn.
func TestBuildRetrievalQueryLeadsWithSubject(t *testing.T) {
	got := BuildRetrievalQuery("",
		toolcore.NBQueryConfig{
			Workload:  "llm-server",
			Namespace: "nudgebee",
			Labels:    map[string]any{"alertname": "PodOOMKilled"},
		},
		strings.Repeat("## Troubleshooting Steps For Investigation (CRITICAL) ", 400))

	require.LessOrEqual(t, len(got), RetrievalQueryMaxBytes)
	assert.Contains(t, got, "llm-server")
	assert.Contains(t, got, "nudgebee")
	assert.Contains(t, got, "PodOOMKilled")
	assert.NotContains(t, got, "Troubleshooting Steps")
}

// Hints are deduplicated against each other so the bound is not spent repeating
// the same identifier. A hint that also appears inside the question is left
// alone: removing words from the question would mangle it, and the repetition
// costs a few bytes at worst.
func TestBuildRetrievalQueryLeavesAFittingQuestionAlone(t *testing.T) {
	// The question already fits the embedder, so it is the query. Prepending
	// labels would rewrite a query that was never the problem.
	got := BuildRetrievalQuery("",
		toolcore.NBQueryConfig{Workload: "relay-server", Labels: map[string]any{"service": "relay-server", "alertname": "TargetDown"}},
		"why is it flapping?")
	assert.Equal(t, "why is it flapping?", got)
}

// Nothing to build from: the caller keeps its existing behaviour rather than
// embedding something worse.
func TestBuildRetrievalQueryEmptyWhenNothingToBuildFrom(t *testing.T) {
	assert.Empty(t, BuildRetrievalQuery("", toolcore.NBQueryConfig{},
		strings.Repeat("You are a classification system. ", 200)))
}

// A rendered task prompt mentions every label it was rendered from. Skipping
// hints it "already contains" would leave nothing to embed, because the prompt
// itself is far too long to be carried.
func TestBuildRetrievalQueryKeepsHintsWhenQuestionIsATaskPrompt(t *testing.T) {
	prompt := "Investigate alert TargetDown on workload relay-server in namespace nudgebee. " +
		strings.Repeat("Prior context and instructions follow. ", 40)
	require.Greater(t, len(prompt), RetrievalQueryMaxBytes)

	got := BuildRetrievalQuery("", toolcore.NBQueryConfig{
		Workload:  "relay-server",
		Namespace: "nudgebee",
		Labels:    map[string]any{"alertname": "TargetDown"},
	}, prompt)

	assert.Equal(t, "relay-server nudgebee TargetDown", got)
}

// Labels is map[string]any: JSON decoding yields []any, a Go caller may pass
// []string. Both must contribute hints.
func TestBuildRetrievalQueryAcceptsBothSliceShapes(t *testing.T) {
	long := strings.Repeat("task prompt boilerplate ", 40)
	require.Greater(t, len(long), RetrievalQueryMaxBytes)

	fromJSON := BuildRetrievalQuery("", toolcore.NBQueryConfig{
		Labels: map[string]any{"services": []any{"orders-api", "payments-api"}},
	}, long)
	fromGo := BuildRetrievalQuery("", toolcore.NBQueryConfig{
		Labels: map[string]any{"services": []string{"orders-api", "payments-api"}},
	}, long)

	assert.Equal(t, "orders-api payments-api", fromJSON)
	assert.Equal(t, fromJSON, fromGo, "both slice shapes must yield the same query")
}
