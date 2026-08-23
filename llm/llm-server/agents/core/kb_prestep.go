package core

import (
	"crypto/sha256"
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
	// kbPrestepDefaultTimeoutSeconds is the fallback for
	// LlmServerKBPrestepTimeoutSeconds. Sized for the RERANKED search: the
	// server-side LLM rerank adds an LLM call (~1-3s) on top of embed+query,
	// and on timeout the pre-step fails open (no KB at all) — a budget that
	// fit only the plain search would turn reranking into silent knowledge loss.
	kbPrestepDefaultTimeoutSeconds = 12
	// kbRefSnippetMaxChars caps the retrieved-text snippet stamped onto a KB
	// reference's metadata for the Additional Contexts panel.
	kbRefSnippetMaxChars = 700
	// kbRefSubjectMaxChars caps the first-line fallback subject for documents
	// with no title metadata.
	kbRefSubjectMaxChars = 90
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
// names plus any inherited ancestor names, de-duplicated by id. The question-aware
// selection (selectedIds) filters inherited KBs only — KBs mapped directly to any of
// the agent's own names are always retained.
//
// ownNames must include the agent's canonical name AND its back-compat aliases: KB
// mappings are keyed by the name in effect when the mapping was created, so an agent
// renamed after the fact (e.g. k8s_debug → k8s_orchestrator) would otherwise never
// see runbooks users mapped under its old name.
func fetchAgentKBs(ctx *security.RequestContext, accountId string, ownNames []string, inheritedNames []string, selectedIds []string) []toolcore.Knowledgebase {
	if accountId == "" || len(ownNames) == 0 {
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
	fetchedAgents := make(map[string]bool)
	var kbs []toolcore.Knowledgebase
	fetch := func(name string, isOwnName bool) {
		if name == "" || fetchedAgents[name] {
			return
		}
		fetchedAgents[name] = true
		fetched, err := toolcore.ListAgentKBs(ctx, accountId, name)
		if err != nil {
			ctx.GetLogger().Warn("agentexecutor: unable to fetch agent KBs", "error", err, "agent", name)
			return
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
	for _, name := range ownNames {
		fetch(name, true)
	}
	for _, name := range inheritedNames {
		fetch(name, false)
	}
	return kbs
}

// buildSkillListsMenu renders a `<skill-lists>` discovery block (names +
// descriptions only — no bodies, no RAG previews) from the given KBs. It is
// placed in the planner's human message (not the cacheable system prefix) when
// the KB pre-step is enabled. Returns "" when no active KB exists.
//
// When hasRetrievedKnowledge is true (e.g. top-level invocation where RAG
// retrieval retrieved relevant knowledge into the human message above), the
// header directs the model to load skills only if the retrieved knowledge is
// insufficient.
//
// When hasRetrievedKnowledge is false (e.g. sub-agent invocations where eager
// RAG retrieval is skipped, or when no knowledge was retrieved), the header
// directs the model to load any relevant skill before running other tools.
//
// Exported so agents in the `agents` package (notably the dynamic delegate)
// can render a menu from an account-wide KB pool without duplicating the
// escape / format rules.
func BuildSkillListsMenu(kbs []toolcore.Knowledgebase, hasRetrievedKnowledge bool) string {
	var sb strings.Builder
	sb.WriteString("<skill-lists>\n")
	if hasRetrievedKnowledge {
		sb.WriteString("Additional knowledge bases available for this account. Relevant knowledge has already been retrieved for you above; use the load_skills tool to load one of these by name ONLY if you need expert guidance the retrieved knowledge does not cover.\n")
	} else {
		sb.WriteString("The following skills are available. If any skill is relevant to the current task, load it using the load_skills tool BEFORE running other tools — skills contain expert guidance that improves your analysis.\n")
	}
	active := 0
	for _, kb := range kbs {
		if kb.Status != "active" {
			continue
		}
		active++
		escapedName := escapeTemplateSyntax(kb.Name)
		escapedDesc := escapeTemplateSyntax(kb.Description)
		if strings.TrimSpace(escapedDesc) != "" {
			fmt.Fprintf(&sb, "name: %s - description: %s\n", escapedName, escapedDesc)
		} else {
			fmt.Fprintf(&sb, "name: %s\n", escapedName)
		}
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
		// Reranked per-call: an LLM judge re-orders the candidates by actual
		// relevance and drops sub-threshold junk. Raw cosine ranked sibling
		// runbooks 0.002 apart with the wrong one first — ordering decides
		// which page wins the prompt budget, so it has to be trustworthy.
		ch <- toolcore.QueryRAGReranked(request.UserId, request.AccountId, query, "knowledge_base",
			kbPrestepTopK, request.ConversationId, request.MessageId, request.AgentId, true)
	}()
	timeout := kbPrestepTimeout()
	var docs toolcore.RAGSearchResults
	select {
	case docs = <-ch:
	case <-time.After(timeout):
		ctx.GetLogger().Warn("kb_prestep: retrieval timed out", "timeout", timeout)
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

	// Collapse duplicate copies of the same page BEFORE the prompt budget is
	// split: orphaned/sibling collections routinely return the same document
	// several times (observed: 5 of 8 slots one SOP), and an even split then
	// starves the page that matters — the runbook's steps never reach the
	// model, so it cannot follow them. Best-scored copy wins; docs stay in
	// rank order.
	kept = dedupRAGDocs(kept)

	refs := attributeKBReferences(ctx, request.AccountId, kept, mergeAccountIntegrationKBs(ctx, request.AccountId, kbs))
	ctx.GetLogger().Info("kb_prestep: retrieval complete",
		"result_count", len(docs), "kept", len(kept),
		"kbs_matched", len(refs), "query_chars", len(query))

	return formatRetrievedKBBlock(kept), refs
}

// kbPrestepTimeout resolves the pre-step's RAG timeout from config, falling
// back to the default for unset/invalid values.
func kbPrestepTimeout() time.Duration {
	secs := config.Config.LlmServerKBPrestepTimeoutSeconds
	if secs <= 0 {
		secs = kbPrestepDefaultTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// dedupRAGDocs collapses duplicate documents (same source url, else identical
// text) keeping the first — highest-scored — instance, preserving rank order.
func dedupRAGDocs(docs toolcore.RAGSearchResults) toolcore.RAGSearchResults {
	seen := make(map[string]struct{}, len(docs))
	out := make(toolcore.RAGSearchResults, 0, len(docs))
	for _, doc := range docs {
		key, _ := doc.Metadata["url"].(string)
		key = strings.TrimSpace(key)
		if key == "" {
			key = strings.TrimSpace(doc.Document)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, doc)
	}
	return out
}

// listAccountKBsFn is swappable in tests; production uses the real account-wide
// KB listing.
var listAccountKBsFn = toolcore.ListKnowledgebases

// mergeAccountIntegrationKBs widens the attribution candidates beyond the
// agent's mapped KBs with the account's ACTIVE integration KBs. Retrieval is
// account-wide, so content from an integration KB nobody mapped to this agent
// (e.g. a synced Confluence space holding the alert's runbook, #34779) is
// routinely injected — but was never credited, leaving the Additional Contexts
// panel empty and users concluding the runbook "didn't load". Integration KBs
// attribute by the cheap kb_source/doc-source match, so widening costs no body
// fetches (fetchKBData skips integration KBs). Manual KBs stay mapped-only:
// content-matching them would mean loading every manual KB body on every
// conversation. Fails open to the mapped list on error.
func mergeAccountIntegrationKBs(ctx *security.RequestContext, accountId string, kbs []toolcore.Knowledgebase) []toolcore.Knowledgebase {
	acctKBs, err := listAccountKBsFn(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Debug("kb_prestep: unable to list account KBs for attribution", "error", err)
		return kbs
	}
	seen := make(map[string]struct{}, len(kbs))
	for _, kb := range kbs {
		seen[kb.Id] = struct{}{}
	}
	merged := make([]toolcore.Knowledgebase, len(kbs), len(kbs)+len(acctKBs))
	copy(merged, kbs)
	for _, kb := range acctKBs {
		if kb.KBType != "integration" || kb.Status != "active" || kb.Id == "" {
			continue
		}
		if _, dup := seen[kb.Id]; dup {
			continue
		}
		merged = append(merged, kb)
	}
	return merged
}

// attributeKBReferences maps retrieved documents back to candidate KBs (the
// agent's mapped KBs plus the account's active integration KBs — see
// mergeAccountIntegrationKBs) so a reference is recorded only for KBs whose
// content was actually fetched. A document attributes to a manual KB when its
// text is contained in that KB's stored data, and to an integration KB when
// its `source` metadata matches the KB's kb_source. Documents that match no
// candidate are simply not referenced.
func attributeKBReferences(ctx *security.RequestContext, accountId string, docs toolcore.RAGSearchResults, kbs []toolcore.Knowledgebase) []AgentReference {
	if len(docs) == 0 || len(kbs) == 0 {
		return nil
	}
	dataByKB := fetchKBData(ctx, accountId, kbs)

	// One reference PER RETRIEVED DOCUMENT, not per KB: a single "KB + top-doc
	// url" row misrepresents a retrieval that injected several pages (and its
	// link pointed at whichever page happened to score highest — misleading
	// when a lower-ranked page drove the conclusion). Docs arrive in descending
	// score order; duplicates of the same page (same url, or identical text
	// from another collection) collapse to their best-scored instance.
	matchKB := func(doc toolcore.RAGSearchResult) (toolcore.Knowledgebase, bool) {
		content := strings.TrimSpace(doc.Document)
		if content == "" {
			return toolcore.Knowledgebase{}, false
		}
		docSource, _ := doc.Metadata["source"].(string)
		for _, kb := range kbs {
			if kb.Status != "active" || kb.Id == "" {
				continue
			}
			if data := dataByKB[kb.Id]; data != "" && strings.Contains(data, content) {
				return kb, true
			}
			if kb.KBType == "integration" && kb.KBSource != nil && docSource != "" &&
				strings.EqualFold(*kb.KBSource, docSource) {
				return kb, true
			}
		}
		return toolcore.Knowledgebase{}, false
	}

	seenDocs := make(map[string]struct{})
	var refs []AgentReference
	for _, doc := range docs {
		kb, ok := matchKB(doc)
		if !ok {
			continue
		}
		// Dedup key: page url when present, else the doc text itself.
		key, _ := doc.Metadata["url"].(string)
		key = strings.TrimSpace(key)
		if key == "" {
			key = strings.TrimSpace(doc.Document)
		}
		if _, dup := seenDocs[key]; dup {
			continue
		}
		seenDocs[key] = struct{}{}

		metadata := map[string]any{
			"name":  kb.Name,
			"kb_id": kb.Id,
			"via":   "kb_prestep",
		}
		if url, ok := doc.Metadata["url"].(string); ok && strings.TrimSpace(url) != "" {
			metadata["url"] = url
		}
		// Subject: the document's own identity — its title when the source
		// provides one, else its first line — so the row names the page that
		// was used, not just the KB it came from.
		if title, ok := doc.Metadata["title"].(string); ok && strings.TrimSpace(title) != "" {
			metadata["subject"] = strings.TrimSpace(title)
		} else if line := firstLine(doc.Document, kbRefSubjectMaxChars); line != "" {
			metadata["subject"] = line
		}
		// Snippet of the retrieved text so the Additional Contexts row shows
		// what was actually injected. Integration KBs have no body in the DB
		// (content lives in the vector store), so without this the row has
		// nothing to display when expanded.
		if snippet := strings.TrimSpace(doc.Document); snippet != "" {
			metadata["content"] = TruncateHead(snippet, kbRefSnippetMaxChars)
		}
		refs = append(refs, AgentReference{
			Type: AgentReferenceTypeKB,
			// Distinct per document — SaveAgentReferences dedups on
			// reference_id, so a shared KB id would collapse the pages back
			// into one row. Non-UUID ids deliberately miss the DAO's
			// llm_knowledgebases join and render from metadata instead.
			ReferenceID: fmt.Sprintf("%s:%x", kb.Id, sha256.Sum256([]byte(key))),
			Metadata:    metadata,
		})
	}
	return refs
}

// firstLine returns the first non-empty line of s, trimmed and capped, for use
// as a display subject when the document carries no title metadata.
func firstLine(s string, maxChars int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return TruncateHead(line, maxChars)
		}
	}
	return ""
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
// block. The total size is bounded by LlmServerMaxSkillContentLength. Budget is
// allocated sequentially in rank order — each doc's cap is its even share of
// what REMAINS, so a short doc's unused budget flows to the docs after it
// instead of being thrown away (with the old fixed split, a runbook competing
// with short duplicates was cut to its intro and its steps never reached the
// model). Returns "" when there are no documents.
func formatRetrievedKBBlock(docs toolcore.RAGSearchResults) string {
	if len(docs) == 0 {
		return ""
	}

	maxLen := config.Config.LlmServerMaxSkillContentLength
	if maxLen <= 0 {
		maxLen = 5000
	}

	var sb strings.Builder
	sb.WriteString("<retrieved_knowledge>\n")
	sb.WriteString("The following knowledge base content was retrieved for this request. Use it as authoritative reference while analyzing the issue. When a retrieved document prescribes investigation steps (a runbook or SOP), FOLLOW its steps in order. When your findings rely on any of this knowledge, cite its Source url.\n")
	// Divisor counts only docs that will actually render — an empty doc
	// skipped below must not shrink the shares of the real ones.
	nonEmpty := 0
	for _, doc := range docs {
		if strings.TrimSpace(doc.Document) != "" {
			nonEmpty++
		}
	}
	remaining := maxLen
	written := 0
	for _, doc := range docs {
		content := strings.TrimSpace(doc.Document)
		if content == "" {
			continue
		}
		if remaining <= 0 {
			break
		}
		perDoc := remaining / (nonEmpty - written)
		if perDoc < 500 {
			perDoc = 500
		}
		if len(content) > perDoc {
			content = TruncateHead(content, perDoc) + "\n[truncated]"
		}
		sb.WriteString("\n")
		if written > 0 {
			sb.WriteString("---\n")
		}
		sb.WriteString(content)
		if url, ok := doc.Metadata["url"].(string); ok && url != "" {
			sb.WriteString("\nSource: ")
			sb.WriteString(url)
		}
		sb.WriteString("\n")
		remaining -= len(content)
		written++
	}
	sb.WriteString("</retrieved_knowledge>")
	return sb.String()
}
