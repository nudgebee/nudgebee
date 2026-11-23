package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/knowledge"
)

// A search that panics must not take down the process, and the other searches'
// results still come back without waiting for the deadline.
func TestRetrieveKnowledgeDocs_SearchPanicIsRecovered(t *testing.T) {
	oldAccount, oldCollection := queryAccountKnowledgeFn, queryCollectionKnowledgeFn
	t.Cleanup(func() { queryAccountKnowledgeFn, queryCollectionKnowledgeFn = oldAccount, oldCollection })
	queryAccountKnowledgeFn = func(_ context.Context, _, _, _, _ string, _ int, _, _, _ string, _ bool) knowledge.RAGSearchResults {
		return knowledge.RAGSearchResults{{Document: "account"}}
	}
	queryCollectionKnowledgeFn = func(_ context.Context, _, _, _, _, _ string, _ int, _, _, _ string, _ bool) knowledge.RAGSearchResults {
		panic("collection search failed")
	}
	kbs := []knowledge.Knowledgebase{{Id: "mapped", Name: "memory runbook", Status: "active", Enabled: true, KBType: "manual"}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	docs, _ := retrieveKnowledgeDocs(ctx, NBAgentRequest{}, "memory runbook", kbs)

	var got []string
	for _, doc := range docs {
		got = append(got, doc.Document)
	}
	assert.Equal(t, []string{"account"}, got)
	assert.Less(t, time.Since(start), 5*time.Second, "the collector waited for the deadline instead of the recovered search")
}
