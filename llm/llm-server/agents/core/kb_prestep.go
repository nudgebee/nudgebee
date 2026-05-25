package core

import (
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const (
	// kbPrestepTopK is how many documents the pre-step's account-wide search
	// retrieves before the relevance cutoff is applied.
	kbPrestepTopK = 8
	// kbPrestepScoreRatio is the relative relevance cutoff: a document is kept
	// only when it scores within this fraction of the strongest hit. References
	// are then attributed only from the kept documents.
	kbPrestepScoreRatio = 0.7
	// kbPrestepTimeout bounds the pre-step so a slow RAG call never stalls planning.
	kbPrestepTimeout = 5 * time.Second
	// kbPrestepMaxQueryLen caps the enriched search query so resource hints
	// never dominate the user's actual question for semantic matching.
	kbPrestepMaxQueryLen = 400
)

// kbPrestepHintKeys are the QueryConfig.Labels keys, in priority order, used to
// enrich the KB search query with the focused resource / event context.
var kbPrestepHintKeys = []string{
	"subject_name", "subject_namespace", "subject_type", "subject_node",
	"service", "services", "alertname", "aggregation_key",
}

// kbAssemblyResult carries the output of the executor's KB-assembly goroutine.
// Legacy path (LlmServerKBPrestepEnabled off): prompt holds the system prompt
// with the `<skill-lists>` block prepended; menu and prestepBlock are empty.
// Pre-step path (on): prompt is unchanged, menu holds the `<skill-lists>`
// discovery block, and prestepBlock holds the retrieved KB content — both
// destined for the human message.
type kbAssemblyResult struct {
	prompt       NBAgentPrompt
	menu         string
	prestepBlock string
	// kbRefs are knowledge_base references for the KBs in scope for the
	// pre-step retrieval — persisted so the UI shows which KBs informed the
	// conversation. Empty on the legacy path and when the pre-step retrieved
	// nothing.
	kbRefs []AgentReference
}

// fetchAgentKBs aggregates the active+inactive KB rows mapped to the agent's own
// name plus any inherited ancestor names, de-duplicated by id. The question-aware
// selection (selectedIds) filters inherited KBs only — KBs mapped directly to the
// agent's own name (agentNames[0]) are always retained.
func fetchAgentKBs(ctx *security.RequestContext, accountId string, agentNames []string, selectedIds []string) []toolcore.Knowledgebase {
	if accountId == "" || len(agentNames) == 0 {
		return nil
	}

	var selectedSet map[string]struct{}
	if selectedIds != nil {
		selectedSet = make(map[string]struct{}, len(selectedIds))
		for _, id := range selectedIds {
			selectedSet[id] = struct{}{}
		}
	}

	seen := make(map[string]bool)
	var kbs []toolcore.Knowledgebase
	for i, name := range agentNames {
		if name == "" {
			continue
		}
		isOwnName := i == 0
		fetched, err := toolcore.ListAgentKBs(ctx, accountId, name)
		if err != nil {
			ctx.GetLogger().Warn("agentexecutor: unable to fetch agent KBs", "error", err, "agent", name)
			continue
		}
		for _, kb := range fetched {
			if seen[kb.Id] {
				continue
			}
			if !isOwnName && selectedSet != nil {
				if _, keep := selectedSet[kb.Id]; !keep {
					continue
				}
			}
			seen[kb.Id] = true
			kbs = append(kbs, kb)
		}
	}
	return kbs
}

// buildSkillListsMenu renders a `<skill-lists>` discovery block (names +
// descriptions only — no bodies, no RAG previews) from the given KBs. It is
// placed in the planner's human message (not the cacheable system prefix) when
// the KB pre-step is enabled. Returns "" when no active KB exists.
func buildSkillListsMenu(kbs []toolcore.Knowledgebase) string {
	var sb strings.Builder
	sb.WriteString("<skill-lists>\n")
	sb.WriteString("Additional knowledge bases available for this account. Relevant knowledge has already been retrieved for you above; use the load_skills tool to load one of these by name ONLY if you need expert guidance the retrieved knowledge does not cover.\n")
	active := 0
	for _, kb := range kbs {
		if kb.Status != "active" {
			continue
		}
		active++
		fmt.Fprintf(&sb, "name: %s - description: %s\n",
			escapeTemplateSyntax(kb.Name), escapeTemplateSyntax(kb.Description))
	}
	sb.WriteString("</skill-lists>")
	if active == 0 {
		return ""
	}
	return sb.String()
}

// buildKBSearchQuery derives the RAG search query for the pre-step: the user's
// verbatim question, enriched with high-signal resource/event identifiers from
// QueryConfig so a generic prompt ("have you checked the knowledge bases?")
// still retrieves resource-specific articles. Hints already present in the
// question are skipped to avoid redundancy; the result is capped.
func buildKBSearchQuery(request NBAgentRequest) string {
	base := strings.TrimSpace(request.OriginalQuery)
	if base == "" {
		base = strings.TrimSpace(request.Query)
	}

	lowerBase := strings.ToLower(base)
	seen := make(map[string]struct{})
	var hints []string
	addHint := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		key := strings.ToLower(v)
		if _, dup := seen[key]; dup {
			return
		}
		if strings.Contains(lowerBase, key) {
			seen[key] = struct{}{}
			return
		}
		seen[key] = struct{}{}
		hints = append(hints, v)
	}

	addHint(request.QueryConfig.Namespace)
	addHint(request.QueryConfig.Workload)
	for _, k := range kbPrestepHintKeys {
		switch v := request.QueryConfig.Labels[k].(type) {
		case string:
			addHint(v)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					addHint(s)
				}
			}
		}
	}

	query := base
	if len(hints) > 0 {
		query = strings.TrimSpace(base + " " + strings.Join(hints, " "))
	}
	return TruncateHead(query, kbPrestepMaxQueryLen)
}

// retrieveRelevantKB is the KB pre-step. It runs before planning, does ONE
// account-wide RAG search on the knowledge_base module, keeps the documents
// whose score is competitive with the strongest hit, and attributes those
// documents back to the agent's mapped KBs so references reflect what was
// actually retrieved — not every mapped KB. It returns the formatted
// <retrieved_knowledge> block and one reference per attributed KB. FAILS OPEN:
// empty query, errors, or timeout return ("", nil); planning then proceeds
// without KB content.
func retrieveRelevantKB(ctx *security.RequestContext, request NBAgentRequest, kbs []toolcore.Knowledgebase) (string, []AgentReference) {
	query := buildKBSearchQuery(request)
	if query == "" {
		return "", nil
	}

	// One account-wide search — bounded so a slow RAG call never stalls planning.
	// The channel is buffered so the goroutine always sends and exits once
	// QueryRAG returns (its HTTP client caps the dial and response-header wait),
	// even when this function has already returned on the timeout below.
	ch := make(chan toolcore.RAGSearchResults, 1)
	go func() {
		ch <- toolcore.QueryRAG(request.UserId, request.AccountId, query, "knowledge_base",
			kbPrestepTopK, request.ConversationId, request.MessageId, request.AgentId, true)
	}()
	var docs toolcore.RAGSearchResults
	select {
	case docs = <-ch:
	case <-time.After(kbPrestepTimeout):
		ctx.GetLogger().Warn("kb_prestep: retrieval timed out", "timeout", kbPrestepTimeout)
		return "", nil
	}
	if len(docs) == 0 {
		return "", nil
	}

	// Relative relevance cutoff: keep only documents scoring within
	// kbPrestepScoreRatio of the strongest hit. The top document always
	// survives, so kept is never empty.
	topScore := docs[0].SimilarityScore
	for _, d := range docs {
		if d.SimilarityScore > topScore {
			topScore = d.SimilarityScore
		}
	}
	var kept toolcore.RAGSearchResults
	if topScore <= 0 {
		// A relative ratio is meaningless once the strongest score is not
		// positive (cosine similarity can be negative): topScore*ratio would
		// exceed topScore and drop every hit. Keep them all instead.
		kept = docs
	} else {
		cutoff := topScore * kbPrestepScoreRatio
		for _, d := range docs {
			if d.SimilarityScore >= cutoff {
				kept = append(kept, d)
			}
		}
	}

	refs := attributeKBReferences(ctx, request.AccountId, kept, kbs)
	ctx.GetLogger().Info("kb_prestep: retrieval complete",
		"result_count", len(docs), "kept", len(kept),
		"kbs_matched", len(refs), "query_chars", len(query))

	return formatRetrievedKBBlock(kept), refs
}

// attributeKBReferences maps retrieved documents back to the agent's mapped KBs
// so a reference is recorded only for KBs whose content was actually fetched.
// A document attributes to a manual KB when its text is contained in that KB's
// stored data, and to an integration KB when its `source` metadata matches the
// KB's kb_source. Documents that match nothing (e.g. KBs not mapped to this
// agent) are simply not referenced.
func attributeKBReferences(ctx *security.RequestContext, accountId string, docs toolcore.RAGSearchResults, kbs []toolcore.Knowledgebase) []AgentReference {
	if len(docs) == 0 || len(kbs) == 0 {
		return nil
	}
	dataByKB := fetchKBData(ctx, accountId, kbs)

	matched := make(map[string]toolcore.Knowledgebase)
	for _, doc := range docs {
		content := strings.TrimSpace(doc.Document)
		if content == "" {
			continue
		}
		docSource, _ := doc.Metadata["source"].(string)
		for _, kb := range kbs {
			if kb.Status != "active" || kb.Id == "" {
				continue
			}
			if _, done := matched[kb.Id]; done {
				continue
			}
			if data := dataByKB[kb.Id]; data != "" && strings.Contains(data, content) {
				matched[kb.Id] = kb
				continue
			}
			if kb.KBType == "integration" && kb.KBSource != nil && docSource != "" &&
				strings.EqualFold(*kb.KBSource, docSource) {
				matched[kb.Id] = kb
			}
		}
	}

	refs := make([]AgentReference, 0, len(matched))
	for _, kb := range matched {
		refs = append(refs, AgentReference{
			Type:        AgentReferenceTypeKB,
			ReferenceID: kb.Id,
			Metadata: map[string]any{
				"name":        kb.Name,
				"description": kb.Description,
				"via":         "kb_prestep",
			},
		})
	}
	return refs
}

// fetchKBData loads the full `data` body for the active manual mapped KBs, keyed
// by id, for content-based attribution. Integration KBs are skipped: their
// content lives only in the vector store and they attribute via kb_source, so
// fetching them would only return empty data.
func fetchKBData(ctx *security.RequestContext, accountId string, kbs []toolcore.Knowledgebase) map[string]string {
	out := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		if kb.Id == "" || kb.Status != "active" || kb.KBType == "integration" {
			continue
		}
		full, err := toolcore.GetKnowledgebase(ctx, accountId, kb.Id)
		if err != nil {
			ctx.GetLogger().Debug("kb_prestep: could not load KB data for attribution", "error", err, "kb_id", kb.Id)
			continue
		}
		out[kb.Id] = full.Data
	}
	return out
}

// formatRetrievedKBBlock aggregates RAG documents into a `<retrieved_knowledge>`
// block. The total size is bounded by LlmServerMaxSkillContentLength and the
// budget is split evenly across documents so one large article cannot starve
// the others. Returns "" when there are no documents.
func formatRetrievedKBBlock(docs toolcore.RAGSearchResults) string {
	if len(docs) == 0 {
		return ""
	}

	maxLen := config.Config.LlmServerMaxSkillContentLength
	if maxLen <= 0 {
		maxLen = 5000
	}
	perDoc := maxLen / len(docs)
	if perDoc < 500 {
		perDoc = 500
	}

	var sb strings.Builder
	sb.WriteString("<retrieved_knowledge>\n")
	sb.WriteString("The following knowledge base content was retrieved for this request. Use it as authoritative reference while analyzing the issue.\n")
	for i, doc := range docs {
		content := strings.TrimSpace(doc.Document)
		if content == "" {
			continue
		}
		if len(content) > perDoc {
			content = TruncateHead(content, perDoc) + "\n[truncated]"
		}
		sb.WriteString("\n")
		if i > 0 {
			sb.WriteString("---\n")
		}
		sb.WriteString(content)
		if url, ok := doc.Metadata["url"].(string); ok && url != "" {
			sb.WriteString("\nSource: ")
			sb.WriteString(url)
		}
		sb.WriteString("\n")
		if sb.Len() >= maxLen {
			break
		}
	}
	sb.WriteString("</retrieved_knowledge>")
	return sb.String()
}
