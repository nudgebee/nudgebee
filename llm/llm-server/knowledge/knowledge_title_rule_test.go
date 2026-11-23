package knowledge

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
)

func useDefaultTitleRule(t *testing.T) {
	t.Helper()
	old := config.Config
	config.Config.KBTitleSOPRuleEnabled = true
	config.Config.KBTitleSOPWords = config.DefaultKBTitleSOPWords
	config.Config.KBTitleSOPExclusions = config.DefaultKBTitleSOPExclusions
	t.Cleanup(func() { config.Config = old })
}

func TestTitleNamesRunbook(t *testing.T) {
	useDefaultTitleRule(t)
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"alert-high-latency. Disk Warning Alarm Runbook", true},
		{"Kubernetes Pod OOMKilled — Runbook", true},
		{"SOP-Nudgebee-EventBridge-SQS-Queue-Backlog", true},
		{"SOP-02 — TLS Certificate Failure", true},
		{"High HTTP Error Rate (5xx) — Runbook", true},
		{"Disk Full Playbook", true},
		{"Run Book: restore a database", true},
		{"run-book for failover", true},
		{"Standard Operating Procedure for backups", true},
		{"runbook", true},
		// a list of runbooks, a part of a word, or a page about runbooks is not one
		{"Runbooks", false},
		{"Sopranos fan club", false},
		{"Playbooks overview", false},
		{"Runbook template", false},
		{"Runbook index", false},
		{"Runbooks index", false},
		{"Runbook templates", false},
		{"Index of runbooks and playbooks", false},
		{"Table of contents: SOP library", false},
		// an exclusion word inside a longer word, or as a real subject of the runbook, does not exclude
		{"Elasticsearch Reindex Runbook", true},
		{"Postgres Index Bloat Runbook", true},
		{"Runbook: Templated alerts", true},
		{"What issues does this SOP cover", true},
		// real runbooks that merely mention a word the exclusions also use
		{"Helm Template Rendering Runbook", true},
		{"Runbook: Terraform Template Drift", true},
		{"Code Review Process Runbook", true},
		{"Kafka Runbook Overview", true},
		{"DB Failover Runbook: What is broken", true},
		{"Alarm Runbook Template", false},
		{"SOP Template", false},
		// dashes of every kind separate words the same way
		{"Runbook \u2013 Index", false},
		{"Runbook\u2014index", false},
		{"Post\u2011mortem Runbook", false},
		{"Post mortem of a runbook", false},
		{"Post_mortem runbook", false},
		{"Runbook \u2013 Redis failover", true},
		{"Postmortem of the runbook outage", false},
		{"Escalation policy (see SOP)", false},
		{"Runbook Home", false},
		{"Runbook review process", false},
		{"How to write a runbook", false},
		{"What is a runbook", false},
		{"Quickstart Migrations & Metadata", false},
		{"", false},
	} {
		t.Run(tc.title, func(t *testing.T) {
			require.Equal(t, tc.want, TitleNamesRunbook(tc.title))
		})
	}
}

func TestTitleRuleIsConfigurable(t *testing.T) {
	useDefaultTitleRule(t)
	config.Config.KBTitleSOPWords = "checklist"
	config.Config.KBTitleSOPExclusions = ""
	require.True(t, TitleNamesRunbook("Release Checklist"))
	require.False(t, TitleNamesRunbook("Alarm Runbook"), "default words are replaced, not extended")
	config.Config.KBTitleSOPWords = ""
	require.False(t, TitleNamesRunbook("Alarm Runbook"), "no words means the rule never fires")
}

func TestHitPageTitle(t *testing.T) {
	require.Equal(t, "From metadata", hitPageTitle(RAGSearchResult{Metadata: map[string]any{"title": " From metadata "}, Document: "Title: ignored"}))
	require.Equal(t, "Disk Alarm Runbook", hitPageTitle(RAGSearchResult{Document: "Title: Disk Alarm Runbook\n\nSteps follow."}))
	require.Equal(t, "Single line", hitPageTitle(RAGSearchResult{Document: "Title: Single line"}))
	require.Equal(t, "", hitPageTitle(RAGSearchResult{Document: "mos DB accoun and more text"}))
	require.Equal(t, "", hitPageTitle(RAGSearchResult{}))
}

func TestIntegrationPageWithRunbookTitleIsProcedure(t *testing.T) {
	useDefaultTitleRule(t)
	integration := func(extra map[string]any) RAGSearchResult {
		metadata := map[string]any{"collection": "int-conf_knowledge_base", "source": "confluence"}
		for k, v := range extra {
			metadata[k] = v
		}
		return RAGSearchResult{Metadata: metadata}
	}
	for _, tc := range []struct {
		name string
		doc  RAGSearchResult
		want KnowledgeContentPurpose
	}{
		{"confluence title names a runbook", integration(map[string]any{"title": "Kubernetes Pod OOMKilled — Runbook", "kind": "knowledge"}), KnowledgePurposeProcedure},
		{"unclassified page with a runbook title", integration(map[string]any{"title": "Disk Full Playbook"}), KnowledgePurposeProcedure},
		{"servicenow first chunk carries the title in its text", RAGSearchResult{Document: "Title: Disk Warning Alarm Runbook\n\nbody", Metadata: map[string]any{"collection": "int-snow_knowledge_base", "source": "servicenow", "sys_id": "abc"}}, KnowledgePurposeProcedure},
		{"title that does not name a runbook", integration(map[string]any{"title": "Notification Rules", "kind": "knowledge"}), KnowledgePurposeReference},
		{"runbook template stays reference", integration(map[string]any{"title": "Runbook template"}), KnowledgePurposeReference},
		{"human FACT mark beats the title", integration(map[string]any{"title": "Alarm Runbook", DocumentNoteCategoryKey: KBNoteCategoryFact}), KnowledgePurposeReference},
		{"human SOP mark still wins", integration(map[string]any{"title": "Notification Rules", DocumentNoteCategoryKey: KBNoteCategorySOP}), KnowledgePurposeProcedure},
		{"the tenant's own uploaded knowledge base is not an integration", RAGSearchResult{Metadata: map[string]any{"collection": "tenant-1_knowledge_base", "source": "user_kb", "title": "Alarm Runbook"}}, KnowledgePurposeReference},
		{"a legacy account collection with no scraper id is not an integration", RAGSearchResult{Metadata: map[string]any{"collection": "acct-legacy_knowledge_base", "title": "Alarm Runbook"}}, KnowledgePurposeReference},
		{"a page indexed before provenance was stamped is recognised by its scraper id", RAGSearchResult{Metadata: map[string]any{"collection": "int-conf_knowledge_base", "page_id": 12345.0, "title": "Alarm Runbook"}}, KnowledgePurposeProcedure},
		{"servicenow is recognised by its source", RAGSearchResult{Document: "Title: Alarm Runbook", Metadata: map[string]any{"collection": "int-snow_knowledge_base", "source": "servicenow"}}, KnowledgePurposeProcedure},
		{"product docs are never promoted", RAGSearchResult{Metadata: map[string]any{"collection": "nudgebee_docs", "title": "Runbooks and SOP marking"}}, KnowledgePurposeReference},
		{"a hit with no collection is never promoted", RAGSearchResult{Metadata: map[string]any{"title": "Alarm Runbook"}}, KnowledgePurposeReference},
		{"manual knowledge base keeps its own category", RAGSearchResult{Metadata: map[string]any{"collection": "kb_manual-1", "title": "Alarm Runbook", "note_category": "fact"}}, KnowledgePurposeReference},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, KnowledgeDocumentPurpose(tc.doc))
		})
	}
}

func TestTitleRuleSwitchedOffChangesNothing(t *testing.T) {
	useDefaultTitleRule(t)
	config.Config.KBTitleSOPRuleEnabled = false
	doc := RAGSearchResult{Metadata: map[string]any{"collection": "x_knowledge_base", "source": "confluence", "title": "Alarm Runbook"}}
	require.Equal(t, KnowledgePurposeReference, KnowledgeDocumentPurpose(doc))
}

func TestCandidateRecordsThePurposeTheTitleRuleChose(t *testing.T) {
	useDefaultTitleRule(t)
	var c KnowledgeCandidate
	SetKnowledgeDocumentHandle(&c, RAGSearchResult{Document: "Title: Disk Warning Alarm Runbook\n\nbody", Metadata: map[string]any{"collection": "x_knowledge_base", "source": "servicenow", "retrieval_id": "r1"}})
	require.Equal(t, KnowledgePurposeProcedure, c.Purpose)
}

// resetTitleRuleLog clears the process-wide set of pages already logged, so the test is repeatable
// (go test -count=2) and independent of other tests.
func resetTitleRuleLog() {
	titleRuleLogged.Range(func(key, _ any) bool {
		titleRuleLogged.Delete(key)
		return true
	})
	titleRuleLoggedN.Store(0)
}

func TestTitleRulePromotionIsLoggedOncePerPage(t *testing.T) {
	resetTitleRuleLog()
	t.Cleanup(resetTitleRuleLog)
	doc := RAGSearchResult{Metadata: map[string]any{"collection": "once-test_knowledge_base", "title": "Unique Once Runbook"}}
	require.True(t, firstTitleRulePromotion(doc))
	require.False(t, firstTitleRulePromotion(doc))
	other := RAGSearchResult{Metadata: map[string]any{"collection": "once-test_knowledge_base", "title": "Another Unique Once Runbook"}}
	require.True(t, firstTitleRulePromotion(other))
}
