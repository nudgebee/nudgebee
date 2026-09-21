package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildKBSearchQuery(t *testing.T) {
	tests := []struct {
		name    string
		request NBAgentRequest
		check   func(t *testing.T, got string)
	}{
		{
			name:    "plain query, no hints, returned as-is",
			request: NBAgentRequest{OriginalQuery: "what does this error mean?"},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "what does this error mean?", got)
			},
		},
		{
			name: "subject_name label is appended",
			request: NBAgentRequest{
				OriginalQuery: "investigate the alert",
				QueryConfig:   toolcore.NBQueryConfig{Labels: map[string]any{"subject_name": "payments-worker"}},
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "investigate the alert")
				assert.Contains(t, got, "payments-worker")
			},
		},
		{
			name: "hint already present in the question is not duplicated",
			request: NBAgentRequest{
				OriginalQuery: "restart the orders-api pod",
				QueryConfig:   toolcore.NBQueryConfig{Labels: map[string]any{"subject_name": "orders-api"}},
			},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "restart the orders-api pod", got)
			},
		},
		{
			name: "namespace and workload are appended",
			request: NBAgentRequest{
				OriginalQuery: "why is it crashing",
				QueryConfig:   toolcore.NBQueryConfig{Namespace: "production", Workload: "checkout-api"},
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "why is it crashing")
				assert.Contains(t, got, "production")
				assert.Contains(t, got, "checkout-api")
			},
		},
		{
			name: "list-valued label appends each element",
			request: NBAgentRequest{
				OriginalQuery: "root cause analysis",
				QueryConfig:   toolcore.NBQueryConfig{Labels: map[string]any{"services": []any{"billing", "shipping"}}},
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "billing")
				assert.Contains(t, got, "shipping")
			},
		},
		{
			name:    "falls back to Query when OriginalQuery is empty",
			request: NBAgentRequest{Query: "fallback question"},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "fallback question", got)
			},
		},
		{
			name: "delegated task augments original user intent",
			request: NBAgentRequest{
				OriginalQuery: "why is checkout failing",
				Query:         "inspect prometheus histogram checkout_latency_seconds",
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "why is checkout failing")
				assert.Contains(t, got, "inspect prometheus histogram checkout_latency_seconds")
			},
		},
		{
			name:    "empty when there is no question at all",
			request: NBAgentRequest{},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "", got)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, buildKBSearchQuery(tt.request))
		})
	}
}

func TestBuildKBSearchQueryCappedLength(t *testing.T) {
	long := strings.Repeat("a", kbPrestepMaxQueryLen+200)
	got := buildKBSearchQuery(NBAgentRequest{OriginalQuery: long})
	assert.LessOrEqual(t, len(got), kbPrestepMaxQueryLen)
}

func TestRetrieveKnowledgeDocsPartialTimeout(t *testing.T) {
	for _, tc := range []struct {
		name        string
		slowAccount bool
		slowMapped  bool
		slowSibling bool
		want        []string
	}{
		{"slow account", true, false, false, []string{"mapped"}},
		{"slow mapped", false, true, false, []string{"account"}},
		{"all slow", true, true, false, nil},
		{"all complete", false, false, false, []string{"mapped", "account"}},
		{"one mapped sibling slow", false, false, true, []string{"mapped", "account"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldAccount, oldCollection := queryAccountKnowledgeFn, queryCollectionKnowledgeFn
			release := make(chan struct{})
			kbs := []toolcore.Knowledgebase{{Id: "mapped", Name: "memory runbook", Status: "active", Enabled: true, KBType: "manual"}}
			if tc.slowSibling {
				kbs = append(kbs, toolcore.Knowledgebase{Id: "sibling", Name: "memory runbook sibling", Status: "active", Enabled: true, KBType: "manual"})
			}
			finished := make(chan struct{}, len(kbs)+1)
			t.Cleanup(func() {
				close(release)
				for range len(kbs) + 1 {
					<-finished
				}
				queryAccountKnowledgeFn, queryCollectionKnowledgeFn = oldAccount, oldCollection
			})
			queryAccountKnowledgeFn = func(_ context.Context, _, _, _, _ string, _ int, _, _, _ string, _ bool) toolcore.RAGSearchResults {
				defer func() { finished <- struct{}{} }()
				if tc.slowAccount {
					<-release
				}
				return toolcore.RAGSearchResults{{Document: "account"}}
			}
			queryCollectionKnowledgeFn = func(_ context.Context, _, _, _, _, collection string, _ int, _, _, _ string, _ bool) toolcore.RAGSearchResults {
				defer func() { finished <- struct{}{} }()
				if tc.slowMapped || collection == "kb_sibling" {
					<-release
				}
				return toolcore.RAGSearchResults{{Document: "mapped"}}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			docs := retrieveKnowledgeDocs(ctx, NBAgentRequest{}, "memory runbook", kbs)
			var got []string
			for _, doc := range docs {
				got = append(got, doc.Document)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRetrieveKnowledgeDocsExcludesDisabledMappedKB(t *testing.T) {
	previousAccount := queryAccountKnowledgeFn
	previousCollection := queryCollectionKnowledgeFn
	t.Cleanup(func() {
		queryAccountKnowledgeFn = previousAccount
		queryCollectionKnowledgeFn = previousCollection
	})
	queryAccountKnowledgeFn = func(_ context.Context, _, _, _, _ string, _ int, _, _, _ string, _ bool) toolcore.RAGSearchResults {
		return nil
	}
	collectionCalled := false
	queryCollectionKnowledgeFn = func(_ context.Context, _, _, _, _, _ string, _ int, _, _, _ string, _ bool) toolcore.RAGSearchResults {
		collectionCalled = true
		return nil
	}
	disabled := toolcore.Knowledgebase{Id: "disabled", Name: "disabled runbook", Status: "active", Enabled: false}
	retrieveKnowledgeDocs(context.Background(), NBAgentRequest{}, "disabled runbook", []toolcore.Knowledgebase{disabled})
	assert.False(t, collectionCalled, "a user-disabled mapped KB must not be searched")
}

func TestRetrieveRelevantKB_DelegatedAgentReservesMappedCandidate(t *testing.T) {
	oldResolver := resolveManualKnowledgeFn
	resolveManualKnowledgeFn = func(_ *security.RequestContext, account string, docs toolcore.RAGSearchResults) toolcore.RAGSearchResults {
		assert.Equal(t, "account-36421", account)
		for i := range docs {
			if docs[i].Metadata["collection"] == "kb_mapped-runbook" {
				docs[i].Metadata["kb_id"] = "mapped-runbook"
				docs[i].Metadata["kb_name"] = "es_metrics_discovery"
			}
		}
		return docs
	}
	t.Cleanup(func() { resolveManualKnowledgeFn = oldResolver })

	previousAccountQuery := queryAccountKnowledgeFn
	previousCollectionQuery := queryCollectionKnowledgeFn
	previousListAccountKBs := listAccountKBsFn
	t.Cleanup(func() {
		queryAccountKnowledgeFn = previousAccountQuery
		queryCollectionKnowledgeFn = previousCollectionQuery
		listAccountKBsFn = previousListAccountKBs
	})

	mapped := toolcore.Knowledgebase{
		Id:          "mapped-runbook",
		Name:        "es_metrics_discovery",
		Description: "Discover Elasticsearch metric field names before querying",
		Status:      "active",
		Enabled:     true,
		KBType:      "manual",
	}
	queryAccountKnowledgeFn = func(_ context.Context, _, _, _, _ string, _ int, _, _, _ string, _ bool) toolcore.RAGSearchResults {
		docs := make(toolcore.RAGSearchResults, 0, kbPrestepTopK)
		for i := range kbPrestepTopK {
			docs = append(docs, toolcore.RAGSearchResult{
				Document: fmt.Sprintf("Generic Confluence page %d", i),
				Metadata: map[string]any{"title": fmt.Sprintf("Generic page %d", i), "url": fmt.Sprintf("https://confluence/page/%d", i), "collection": "global_docs"},
			})
		}
		return docs
	}
	queryCollectionKnowledgeFn = func(_ context.Context, _, _, _, _, collection string, _ int, _, _, _ string, _ bool) toolcore.RAGSearchResults {
		assert.Equal(t, "kb_mapped-runbook", collection)
		return toolcore.RAGSearchResults{{
			Document: "First discover fields such as service_name before issuing the Elasticsearch metrics query.",
			Metadata: map[string]any{"title": "es_metrics_discovery", "collection": collection},
		}}
	}
	listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
		return []toolcore.Knowledgebase{mapped}, nil
	}

	request := NBAgentRequest{
		AccountId:      "account-36421",
		ConversationId: "conversation-36421",
		MessageId:      "message-36421",
		AgentId:        "elastic-search-metrics-agent",
		OriginalQuery:  "why are Elasticsearch metrics missing",
		Query:          "inspect available metric fields",
	}
	result := retrieveRelevantKB(security.NewRequestContextForSuperAdmin(), request, []toolcore.Knowledgebase{mapped})

	require.Contains(t, result.menu, "es_metrics_discovery")
	require.Contains(t, result.menu, "knowledge:")
	start := strings.Index(result.menu, "knowledge:")
	candidate, ok := toolcore.LoadKnowledgeCandidate(request.AccountId, request.ConversationId, request.MessageId, result.menu[start:start+len("knowledge:")+16])
	require.True(t, ok)
	assert.Equal(t, "mapped-runbook", candidate.KBID)
	assert.Empty(t, candidate.Content, "manual bodies load by canonical ID")
	require.NotEmpty(t, result.references)
	assert.Contains(t, result.references[0].ReferenceID, "mapped-runbook")
}

func TestBuildKnowledgeCandidateMenuIsCompactAndLoadable(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := NBAgentRequest{
		AccountId:      "account-index-test",
		ConversationId: "conversation-index-test",
		MessageId:      "message-index-test",
	}
	fullBody := "Tenant convention: query checkout logs with service_name=checkout-api. " + strings.Repeat("detail ", 2000)
	docs := toolcore.RAGSearchResults{{
		Document: fullBody,
		Metadata: map[string]any{
			"title":  "Checkout logging conventions",
			"source": "servicenow",
			"url":    "https://servicenow.example/kb/KB001",
		},
	}}

	menu := buildKnowledgeCandidateMenu(ctx, request, docs, nil)
	assert.Contains(t, menu, "<skill-lists>")
	assert.Contains(t, menu, "source: servicenow")
	assert.Contains(t, menu, "id: knowledge:")
	assert.NotContains(t, menu, strings.Repeat("detail ", 100), "the full article must stay out of the prompt index")

	idStart := strings.Index(menu, "knowledge:")
	require.NotEqual(t, -1, idStart)
	id := menu[idStart : idStart+len("knowledge:")+16]
	candidate, ok := toolcore.LoadKnowledgeCandidate(request.AccountId, request.ConversationId, request.MessageId, id)
	require.True(t, ok)
	assert.Equal(t, TruncateHead(strings.TrimSpace(fullBody), toolcore.KnowledgeExcerptBytes), candidate.Content)
	assert.True(t, candidate.ExcerptOnly)
}

func TestBuildKnowledgeCandidateMenuKeepsSectionsFromSameURLLoadable(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := NBAgentRequest{
		AccountId:      "account-section-test",
		ConversationId: "conversation-section-test",
		MessageId:      "message-section-test",
	}
	docs := toolcore.RAGSearchResults{
		{Document: "Section one procedure", Metadata: map[string]any{"url": "https://docs.example/runbook"}},
		{Document: "Section two procedure", Metadata: map[string]any{"url": "https://docs.example/runbook"}},
	}

	menu := buildKnowledgeCandidateMenu(ctx, request, docs, nil)
	var ids []string
	for _, line := range strings.Split(menu, "\n") {
		if _, rest, ok := strings.Cut(line, "id: "); ok {
			ids = append(ids, strings.Fields(rest)[0])
		}
	}
	require.Len(t, ids, 2)
	assert.NotEqual(t, ids[0], ids[1])

	first, ok := toolcore.LoadKnowledgeCandidate(request.AccountId, request.ConversationId, request.MessageId, ids[0])
	require.True(t, ok)
	second, ok := toolcore.LoadKnowledgeCandidate(request.AccountId, request.ConversationId, request.MessageId, ids[1])
	require.True(t, ok)
	assert.Equal(t, "Section one procedure", first.Content)
	assert.Equal(t, "Section two procedure", second.Content)
}

func TestFormatRetrievedKBBlock(t *testing.T) {
	t.Run("empty docs return empty string", func(t *testing.T) {
		assert.Equal(t, "", formatRetrievedKBBlock(nil))
		assert.Equal(t, "", formatRetrievedKBBlock(toolcore.RAGSearchResults{}))
	})

	t.Run("docs render inside a retrieved_knowledge block", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "Scale the deployment to add more replicas when CPU is saturated."},
		}
		got := formatRetrievedKBBlock(docs)
		assert.Contains(t, got, "<retrieved_knowledge>")
		assert.Contains(t, got, "</retrieved_knowledge>")
		assert.Contains(t, got, "Scale the deployment to add more replicas when CPU is saturated.")
	})

	t.Run("source url is included when present in metadata", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "content", Metadata: map[string]any{"url": "https://wiki.example.com/runbooks/scaling"}},
		}
		got := formatRetrievedKBBlock(docs)
		assert.Contains(t, got, "Source: https://wiki.example.com/runbooks/scaling")
	})

	t.Run("an oversized document is truncated", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{{Document: strings.Repeat("x", 20000)}}
		got := formatRetrievedKBBlock(docs)
		assert.Contains(t, got, "[truncated]")
		assert.Less(t, len(got), 20000)
	})
}

// strPtr is a tiny helper for optional string fields on toolcore.Knowledgebase.
func strPtr(s string) *string { return &s }

func TestMergeAccountIntegrationKBs(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	restore := listAccountKBsFn
	defer func() { listAccountKBsFn = restore }()

	t.Run("active integration KBs are added to mapped candidates", func(t *testing.T) {
		listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
			return []toolcore.Knowledgebase{
				{Id: "kb-conf", Name: "dev-confluence", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
				{Id: "kb-arch", Name: "old-confluence", Status: "archived", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
				{Id: "kb-man", Name: "manual-notes", Status: "active", Enabled: true, KBType: toolcore.KBTypeManual},
				{Id: "kb-off", Name: "disabled-notes", Status: "active", Enabled: false, KBType: toolcore.KBTypeManual},
			}, nil
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-mapped", Name: "mapped", Status: "active", Enabled: true, KBType: toolcore.KBTypeManual}}
		got := mergeAccountIntegrationKBs(ctx, "acct", mapped)
		ids := make([]string, 0, len(got))
		for _, kb := range got {
			ids = append(ids, kb.Id)
		}
		// Mapped KB retained; every active account KB is eligible regardless
		// of agent mapping. Archived KBs remain excluded.
		assert.Equal(t, []string{"kb-mapped", "kb-conf", "kb-man"}, ids)
	})

	t.Run("duplicate ids are not added twice", func(t *testing.T) {
		listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
			return []toolcore.Knowledgebase{
				{Id: "kb-conf", Name: "dev-confluence", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
			}, nil
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-conf", Name: "dev-confluence", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")}}
		got := mergeAccountIntegrationKBs(ctx, "acct", mapped)
		assert.Len(t, got, 1)
	})

	t.Run("disabled account KBs are not added", func(t *testing.T) {
		listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
			return []toolcore.Knowledgebase{{Id: "kb-off", Name: "disabled", Status: "active", Enabled: false}}, nil
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-mapped", Status: "active", Enabled: true}}
		assert.Equal(t, mapped, mergeAccountIntegrationKBs(ctx, "acct", mapped))
	})

	t.Run("listing failure falls back to mapped KBs only", func(t *testing.T) {
		listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
			return nil, assert.AnError
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-mapped", Status: "active", Enabled: true, KBType: toolcore.KBTypeManual}}
		got := mergeAccountIntegrationKBs(ctx, "acct", mapped)
		assert.Len(t, got, 1)
		assert.Equal(t, "kb-mapped", got[0].Id)
	})
}

func TestAttributeKBReferencesUnmappedIntegrationKB(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("one reference per retrieved page, labelled by the page not the KB", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{
				Document:        "Step 1 — Rule out the histogram artifact.",
				Metadata:        map[string]any{"source": "confluence", "url": "https://example.atlassian.net/wiki/pages/113836034", "title": "NBLLM Agent Latency P95 High — Runbook"},
				SimilarityScore: 0.92,
			},
			{
				Document:        "Runbook — Event Processing Latency High. Fires when services-server is slow.",
				Metadata:        map[string]any{"source": "confluence", "url": "https://example.atlassian.net/wiki/pages/110002177"},
				SimilarityScore: 0.90,
			},
		}
		kbs := []toolcore.Knowledgebase{
			{Id: "kb-conf", Name: "dev-confluence", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
		}
		refs, _, _ := attributeKBReferences(ctx, "acct", docs, kbs)
		assert.Len(t, refs, 2)
		// Each row is the page that was used: its own url, subject, snippet.
		assert.Equal(t, "https://example.atlassian.net/wiki/pages/113836034", refs[0].Metadata["url"])
		assert.Equal(t, "NBLLM Agent Latency P95 High — Runbook", refs[0].Metadata["subject"])
		assert.Equal(t, "Step 1 — Rule out the histogram artifact.", refs[0].Metadata["content"])
		assert.Equal(t, "https://example.atlassian.net/wiki/pages/110002177", refs[1].Metadata["url"])
		// No title metadata → subject falls back to the page's first line.
		assert.Equal(t, "Runbook — Event Processing Latency High. Fires when services-server is slow.", refs[1].Metadata["subject"])
		// Both credit the same KB, with distinct reference ids so the insert
		// dedup does not collapse them back into one row.
		for _, r := range refs {
			assert.Equal(t, AgentReferenceTypeKB, r.Type)
			assert.Equal(t, "dev-confluence", r.Metadata["name"])
			assert.Equal(t, "kb-conf", r.Metadata["kb_id"])
			assert.Equal(t, "kb_prestep", r.Metadata["via"])
			assert.True(t, strings.HasPrefix(r.ReferenceID, "kb-conf:"))
		}
		assert.NotEqual(t, refs[0].ReferenceID, refs[1].ReferenceID)
	})

	t.Run("duplicate copies of the same page collapse to the best-scored one", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "SOP content.", Metadata: map[string]any{"source": "confluence", "url": "https://example.atlassian.net/wiki/pages/999"}, SimilarityScore: 0.86},
			{Document: "SOP content.", Metadata: map[string]any{"source": "confluence", "url": "https://example.atlassian.net/wiki/pages/999"}, SimilarityScore: 0.86},
			{Document: "SOP content.", Metadata: map[string]any{"source": "confluence", "url": "https://example.atlassian.net/wiki/pages/999"}, SimilarityScore: 0.86},
		}
		kbs := []toolcore.Knowledgebase{
			{Id: "kb-conf", Name: "dev-confluence", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
		}
		refs, _, _ := attributeKBReferences(ctx, "acct", docs, kbs)
		assert.Len(t, refs, 1)
	})

	t.Run("doc without url dedups by content and still credits the KB", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "kb article body", Metadata: map[string]any{"source": "servicenow"}, SimilarityScore: 0.9},
			{Document: "kb article body", Metadata: map[string]any{"source": "servicenow"}, SimilarityScore: 0.88},
		}
		kbs := []toolcore.Knowledgebase{
			{Id: "kb-snow", Name: "snow", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("servicenow")},
		}
		refs, _, _ := attributeKBReferences(ctx, "acct", docs, kbs)
		assert.Len(t, refs, 1)
		_, hasURL := refs[0].Metadata["url"]
		assert.False(t, hasURL)
		assert.Equal(t, "kb article body", refs[0].Metadata["subject"])
	})

	t.Run("source mismatch credits nothing", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "content", Metadata: map[string]any{"source": "confluence"}, SimilarityScore: 0.9},
		}
		kbs := []toolcore.Knowledgebase{
			{Id: "kb-snow", Name: "snow", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("servicenow")},
		}
		refs, _, _ := attributeKBReferences(ctx, "acct", docs, kbs)
		assert.Empty(t, refs)
	})
}

// TestAttributionDropCountExcludesDuplicates pins what the caller's fail-closed
// warning reports. Duplicates are not a fail-closed drop, so counting them would
// warn that content was silently withheld from the prompt when nothing was.
func TestAttributionDropCountExcludesDuplicates(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	kbs := []toolcore.Knowledgebase{
		{Id: "kb-conf", Name: "dev-confluence", Status: "active", Enabled: true, KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
	}
	// Deliberately raw (undeduplicated) input, which is the only way the two
	// exclusion reasons can both occur in one call.
	docs := toolcore.RAGSearchResults{
		{Document: "SOP content.", Metadata: map[string]any{"source": "confluence", "url": "https://x/sop"}, SimilarityScore: 0.9},
		{Document: "SOP content.", Metadata: map[string]any{"source": "confluence", "url": "https://x/sop"}, SimilarityScore: 0.89},
		{Document: "orphan content", Metadata: map[string]any{"source": "nowhere"}, SimilarityScore: 0.5},
	}
	refs, attributed, dropped := attributeKBReferences(ctx, "acct", docs, kbs)

	assert.Len(t, refs, 1, "the duplicate collapses into one reference row")
	assert.Len(t, attributed, 1)
	assert.Equal(t, 1, dropped, "only the unattributable doc counts as dropped, not the duplicate")
	// The inference this replaced would have reported 2 here.
	assert.NotEqual(t, len(docs)-len(attributed), dropped)
}

func TestDedupRAGDocs(t *testing.T) {
	docs := toolcore.RAGSearchResults{
		{Document: "runbook A", Metadata: map[string]any{"url": "https://x/a"}, SimilarityScore: 0.9},
		{Document: "runbook A copy", Metadata: map[string]any{"url": "https://x/a"}, SimilarityScore: 0.89},
		{Document: "sop B", Metadata: map[string]any{"url": "https://x/b"}, SimilarityScore: 0.85},
		{Document: "no-url doc", Metadata: map[string]any{}, SimilarityScore: 0.84},
		{Document: "no-url doc", Metadata: map[string]any{}, SimilarityScore: 0.83},
	}
	out := dedupRAGDocs(docs)
	assert.Len(t, out, 3)
	// Rank order preserved; best-scored copy of each page kept.
	assert.Equal(t, "runbook A", out[0].Document)
	assert.Equal(t, "sop B", out[1].Document)
	assert.Equal(t, "no-url doc", out[2].Document)
}

// TestDedupRAGDocsLeavesNoDuplicateKeys pins the coupling that makes the dup
// checks inside attributeKBReferences unreachable: attribution runs over the
// output of dedupRAGDocs and re-derives the SAME key, so it can never be the
// thing that drops a distinct chunk of a page. If a future change gives the two
// different keys, that branch silently becomes live and starts deleting
// retrieved content from the prompt — this test fails first.
func TestDedupRAGDocsLeavesNoDuplicateKeys(t *testing.T) {
	docs := toolcore.RAGSearchResults{
		{Document: "chunk 1 of the runbook", Metadata: map[string]any{"url": "https://x/a"}},
		{Document: "chunk 2 of the runbook", Metadata: map[string]any{"url": "https://x/a"}},
		{Document: "  https url with spaces  ", Metadata: map[string]any{"url": "  https://x/b  "}},
		{Document: "same text", Metadata: map[string]any{}},
		{Document: "same text", Metadata: map[string]any{"url": ""}},
	}
	out := dedupRAGDocs(docs)

	seen := make(map[string]struct{}, len(out))
	for _, doc := range out {
		key := ragDocDedupKey(doc)
		_, dup := seen[key]
		assert.False(t, dup, "dedupRAGDocs emitted two docs sharing key %q", key)
		seen[key] = struct{}{}
	}
	assert.Len(t, out, 3, "same-url chunks and same-text docs collapse to one each")
	assert.Equal(t, "https://x/b", ragDocDedupKey(out[1]), "url key is trimmed")
	assert.Equal(t, "same text", ragDocDedupKey(out[2]), "empty url falls back to trimmed text")
}

func TestFormatRetrievedKBBlockSequentialBudget(t *testing.T) {
	// A short doc must hand its unused budget to the long doc after it, so the
	// runbook's steps survive instead of being cut at its even share.
	long := strings.Repeat("step ", 2000) // ~10000 chars
	docs := toolcore.RAGSearchResults{
		{Document: "short doc"},
		{Document: long},
	}
	got := formatRetrievedKBBlock(docs)
	// Even split would cap the long doc at ~2500; sequential allocation gives
	// it the short doc's leftover (~5000 - len("short doc")).
	assert.Greater(t, len(got), 4500)
	assert.Contains(t, got, "Reference: use as supporting information")
}

func TestKBPrestepTimeoutConfigurable(t *testing.T) {
	prev := config.Config.LlmServerKBPrestepTimeoutSeconds
	defer func() { config.Config.LlmServerKBPrestepTimeoutSeconds = prev }()

	config.Config.LlmServerKBPrestepTimeoutSeconds = 5
	assert.Equal(t, 5*time.Second, kbPrestepTimeout())

	// Unset / invalid values fall back to the default instead of a zero
	// timeout (which would make every retrieval fail open instantly).
	config.Config.LlmServerKBPrestepTimeoutSeconds = 0
	assert.Equal(t, 3*time.Second, kbPrestepTimeout())
	config.Config.LlmServerKBPrestepTimeoutSeconds = -3
	assert.Equal(t, 3*time.Second, kbPrestepTimeout())
}

// TestClassifyCollection pins the collection-name contract rag-server reports on
// every hit. The name is the document's only identity — nothing in the point
// payload carries a kb id — so this mapping decides whether a document is
// attributed to a KB, or treated as global (product docs, no KB row by design).
func TestClassifyCollection(t *testing.T) {
	kbID, integID := classifyCollection("kb_aeaab529-165f-4199-a030-8ea3f2302c22")
	assert.Equal(t, "aeaab529-165f-4199-a030-8ea3f2302c22", kbID)
	assert.Empty(t, integID)

	kbID, integID = classifyCollection("8693c52d-7f8b-4392-a597-db6531770e8a_knowledge_base")
	assert.Empty(t, kbID)
	assert.Equal(t, "8693c52d-7f8b-4392-a597-db6531770e8a", integID)

	// Global: neither form -> no KB owns it, so it takes the recorded-global path.
	kbID, integID = classifyCollection("nudgebee_docs")
	assert.Empty(t, kbID)
	assert.Empty(t, integID)
}

// TestDocCollectionStamp covers the rollout gap: a hit from a rag-server that
// predates the stamp reports no collection, and must fall back to the existing
// text/source rules rather than being misread as global.
func TestDocCollectionStamp(t *testing.T) {
	name, ok := docCollection(toolcore.RAGSearchResult{
		Metadata: map[string]any{"collection": " kb_abc "},
	})
	assert.True(t, ok)
	assert.Equal(t, "kb_abc", name)

	_, ok = docCollection(toolcore.RAGSearchResult{Metadata: map[string]any{}})
	assert.False(t, ok, "missing stamp must not look like a global collection")
}

func TestFormatRetrievedKBBlockPurpose(t *testing.T) {
	for _, tc := range []struct{ collection, category, want string }{
		{"kb_manual", "sop", "Procedure: follow applicable steps"},
		{"kb_manual", "fact", "Reference: use as supporting information"},
		{"confluence_knowledge_base", "sop", "Reference: use as supporting information"},
	} {
		t.Run(tc.collection+tc.category, func(t *testing.T) {
			docs := toolcore.RAGSearchResults{{Document: "Unique body", Metadata: map[string]any{"collection": tc.collection, "kb_id": "manual", "note_category": tc.category}}}
			got := formatRetrievedKBBlock(docs)
			require.Contains(t, got, tc.want)
			require.Contains(t, got, "Unique body")
		})
	}
}
