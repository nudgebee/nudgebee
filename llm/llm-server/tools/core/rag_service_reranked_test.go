package core

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
)

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
