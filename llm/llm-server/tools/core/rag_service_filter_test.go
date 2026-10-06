package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryRAGRerankedFilteredContextSendsFilter(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		// FailNow must run on the test goroutine, so the handler only reports.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	previousURL := config.Config.RAGServerUrl
	config.Config.RAGServerUrl = server.URL
	t.Cleanup(func() { config.Config.RAGServerUrl = previousURL })

	filter := map[string]any{"must_not": []any{map[string]any{"field": "source", "value": "nudgebee_docs"}}}
	QueryRAGRerankedFilteredContext(context.Background(), "user", "account", "q", "knowledge_base", 8, "", "", "", false, filter)
	QueryRAGRerankedContext(context.Background(), "user", "account", "q", "knowledge_base", 8, "", "", "", false)

	require.Len(t, bodies, 2)
	assert.Equal(t, filter, bodies[0]["metadata_filter"])
	assert.Equal(t, true, bodies[0]["use_reranking"])
	_, hasFilter := bodies[1]["metadata_filter"]
	assert.False(t, hasFilter, "the unfiltered search sends no metadata_filter")
}
