package core

import (
	"nudgebee/llm/config"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
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

func TestBuildSkillListsMenu(t *testing.T) {
	t.Run("active KBs render with names and descriptions when hasRetrievedKnowledge is true", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{
			{Name: "Pod Restart Runbook", Description: "Steps to safely restart a crashlooping pod", Status: "active"},
			{Name: "Database Troubleshooting", Description: "Common database connection issues", Status: "active"},
		}
		got := BuildSkillListsMenu(kbs, true)
		assert.Contains(t, got, "<skill-lists>")
		assert.Contains(t, got, "</skill-lists>")
		assert.Contains(t, got, "Additional knowledge bases available for this account. Relevant knowledge has already been retrieved for you above; use the load_skills tool to load one of these by name ONLY if you need expert guidance the retrieved knowledge does not cover.")
		assert.Contains(t, got, "name: Pod Restart Runbook - description: Steps to safely restart a crashlooping pod")
		assert.Contains(t, got, "name: Database Troubleshooting - description: Common database connection issues")
	})

	t.Run("sub-agent / no retrieved knowledge renders proactive lazy load instructions", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{
			{Name: "es_metrics_discovery", Description: "Elasticsearch metrics discovery runbook", Status: "active"},
		}
		got := BuildSkillListsMenu(kbs, false)
		assert.Contains(t, got, "<skill-lists>")
		assert.Contains(t, got, "</skill-lists>")
		assert.Contains(t, got, "The following skills are available. If any skill is relevant to the current task, load it using the load_skills tool BEFORE running other tools — skills contain expert guidance that improves your analysis.")
		assert.NotContains(t, got, "already been retrieved for you above")
		assert.Contains(t, got, "name: es_metrics_discovery - description: Elasticsearch metrics discovery runbook")
	})

	t.Run("empty description does not emit dangling description suffix", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{
			{Name: "unannotated_skill", Description: "", Status: "active"},
			{Name: "whitespace_desc_skill", Description: "   ", Status: "active"},
		}
		got := BuildSkillListsMenu(kbs, false)
		assert.Contains(t, got, "name: unannotated_skill\n")
		assert.Contains(t, got, "name: whitespace_desc_skill\n")
		assert.NotContains(t, got, "description:")
	})

	t.Run("inactive KBs are excluded", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{
			{Name: "Active KB", Description: "d", Status: "active"},
			{Name: "Processing KB", Description: "d", Status: "processing"},
		}
		got := BuildSkillListsMenu(kbs, false)
		assert.Contains(t, got, "Active KB")
		assert.NotContains(t, got, "Processing KB")
	})

	t.Run("no active KBs returns empty string", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{{Name: "x", Status: "processing"}}
		assert.Equal(t, "", BuildSkillListsMenu(kbs, true))
		assert.Equal(t, "", BuildSkillListsMenu(kbs, false))
	})

	t.Run("empty slice returns empty string", func(t *testing.T) {
		assert.Equal(t, "", BuildSkillListsMenu(nil, true))
		assert.Equal(t, "", BuildSkillListsMenu(nil, false))
	})
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
				{Id: "kb-conf", Name: "dev-confluence", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
				{Id: "kb-arch", Name: "old-confluence", Status: "archived", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
				{Id: "kb-man", Name: "manual-notes", Status: "active", KBType: toolcore.KBTypeManual},
			}, nil
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-mapped", Name: "mapped", Status: "active", KBType: toolcore.KBTypeManual}}
		got := mergeAccountIntegrationKBs(ctx, "acct", mapped)
		ids := make([]string, 0, len(got))
		for _, kb := range got {
			ids = append(ids, kb.Id)
		}
		// Mapped KB retained, active integration KB added; archived and
		// account-wide manual KBs excluded.
		assert.Equal(t, []string{"kb-mapped", "kb-conf"}, ids)
	})

	t.Run("duplicate ids are not added twice", func(t *testing.T) {
		listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
			return []toolcore.Knowledgebase{
				{Id: "kb-conf", Name: "dev-confluence", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
			}, nil
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-conf", Name: "dev-confluence", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")}}
		got := mergeAccountIntegrationKBs(ctx, "acct", mapped)
		assert.Len(t, got, 1)
	})

	t.Run("listing failure falls back to mapped KBs only", func(t *testing.T) {
		listAccountKBsFn = func(_ *security.RequestContext, _ string) ([]toolcore.Knowledgebase, error) {
			return nil, assert.AnError
		}
		mapped := []toolcore.Knowledgebase{{Id: "kb-mapped", Status: "active", KBType: toolcore.KBTypeManual}}
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
			{Id: "kb-conf", Name: "dev-confluence", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
		}
		refs := attributeKBReferences(ctx, "acct", docs, kbs)
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
			{Id: "kb-conf", Name: "dev-confluence", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("confluence")},
		}
		refs := attributeKBReferences(ctx, "acct", docs, kbs)
		assert.Len(t, refs, 1)
	})

	t.Run("doc without url dedups by content and still credits the KB", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "kb article body", Metadata: map[string]any{"source": "servicenow"}, SimilarityScore: 0.9},
			{Document: "kb article body", Metadata: map[string]any{"source": "servicenow"}, SimilarityScore: 0.88},
		}
		kbs := []toolcore.Knowledgebase{
			{Id: "kb-snow", Name: "snow", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("servicenow")},
		}
		refs := attributeKBReferences(ctx, "acct", docs, kbs)
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
			{Id: "kb-snow", Name: "snow", Status: "active", KBType: toolcore.KBTypeIntegration, KBSource: strPtr("servicenow")},
		}
		refs := attributeKBReferences(ctx, "acct", docs, kbs)
		assert.Empty(t, refs)
	})
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
	assert.Contains(t, got, "FOLLOW its steps in order")
}

func TestKBPrestepTimeoutConfigurable(t *testing.T) {
	prev := config.Config.LlmServerKBPrestepTimeoutSeconds
	defer func() { config.Config.LlmServerKBPrestepTimeoutSeconds = prev }()

	config.Config.LlmServerKBPrestepTimeoutSeconds = 5
	assert.Equal(t, 5*time.Second, kbPrestepTimeout())

	// Unset / invalid values fall back to the default instead of a zero
	// timeout (which would make every retrieval fail open instantly).
	config.Config.LlmServerKBPrestepTimeoutSeconds = 0
	assert.Equal(t, 12*time.Second, kbPrestepTimeout())
	config.Config.LlmServerKBPrestepTimeoutSeconds = -3
	assert.Equal(t, 12*time.Second, kbPrestepTimeout())
}
