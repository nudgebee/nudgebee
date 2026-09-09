package core

import (
	"crypto/sha256"
	"fmt"
	"slices"
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

// BuildSkillListsMenu renders a `<skill-lists>` discovery block (names +
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

	// Log what RAG actually returned, per document, before attribution runs.
	// Identity lives in these fields alone (there is no kb id on a hit), so when
	// a document ends up unattributable this is the only record of what it was.
	// Content is not logged - only its size - to keep customer text out of logs.
	for i, d := range kept {
		url, _ := d.Metadata["url"].(string)
		src, _ := d.Metadata["source"].(string)
		title, _ := d.Metadata["title"].(string)
		docID, _ := d.Metadata["_id"].(string)
		ctx.GetLogger().Info("kb_prestep: rag document",
			"rank", i, "score", d.SimilarityScore, "chars", len(d.Document),
			"url", url, "source", src, "title", title, "doc_id", docID,
			"metadata_keys", metadataKeys(d.Metadata))
	}

	candidates := mergeAccountIntegrationKBs(ctx, request.AccountId, kbs)
	for _, kb := range candidates {
		src := ""
		if kb.KBSource != nil {
			src = *kb.KBSource
		}
		ctx.GetLogger().Info("kb_prestep: attribution candidate",
			"kb_id", kb.Id, "name", kb.Name, "status", kb.Status,
			"kb_type", kb.KBType, "kb_source", src)
	}
	// Only worth a WARN when the account HAS knowledge bases but none of them
	// resolved to a usable candidate. An account with no KBs configured is the
	// normal case and must not warn on every request.
	if len(kbs) > 0 && len(candidates) == 0 {
		// Un-owned documents do not need a candidate — they are recorded from
		// their own url/source — so this is not "everything is dropped".
		ctx.GetLogger().Warn("kb_prestep: knowledge bases exist but none resolved to an attribution candidate - "+
			"only un-owned documents can be kept",
			"account_id", request.AccountId, "mapped_kbs", len(kbs))
	}

	refs, attributed, dropped := attributeKBReferences(ctx, request.AccountId, kept, candidates)
	// Fail CLOSED: only content we can attribute reaches the prompt. An
	// unattributable document would otherwise influence the answer with no
	// reference row, so the user sees an answer citing a source the Additional
	// Contexts panel cannot show — and no log they can read. Dropping it keeps
	// "injected" and "recorded" the same set.
	if dropped > 0 {
		ctx.GetLogger().Warn("kb_prestep: dropping unattributable documents from the prompt",
			"kept", len(kept), "attributed", len(attributed), "dropped", dropped)
	}
	ctx.GetLogger().Info("kb_prestep: retrieval complete",
		"result_count", len(docs), "kept", len(kept),
		"kbs_matched", len(refs), "injected", len(attributed), "query_chars", len(query))

	return formatRetrievedKBBlock(attributed), refs
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
// ragDocDedupKey identifies a retrieved page: its url, else its own text.
// attributeKBReferences re-checks this key defensively, and that check is only
// dead code for as long as it derives the key the same way dedupRAGDocs does —
// so both call this rather than each spelling it out.
func ragDocDedupKey(doc toolcore.RAGSearchResult) string {
	if url, _ := doc.Metadata["url"].(string); strings.TrimSpace(url) != "" {
		return strings.TrimSpace(url)
	}
	return strings.TrimSpace(doc.Document)
}

func dedupRAGDocs(docs toolcore.RAGSearchResults) toolcore.RAGSearchResults {
	seen := make(map[string]struct{}, len(docs))
	out := make(toolcore.RAGSearchResults, 0, len(docs))
	for _, doc := range docs {
		key := ragDocDedupKey(doc)
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
// metadataKeys lists the metadata field names on a RAG hit. Logged so a miss
// shows what rag-server actually returned - the payload carries no kb id, and
// which keys are present decides which attribution rule can even apply.
func metadataKeys(md map[string]any) []string {
	if len(md) == 0 {
		return nil
	}
	keys := make([]string, 0, len(md))
	for k := range md {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// docCollection returns the Qdrant collection rag-server reports on a hit, and
// whether it was present. The collection name IS the document's identity:
//
//	kb_<kb_id>                    -> a manual knowledge base
//	<integration_id>_knowledge_base -> a synced integration knowledge base
//	anything else                 -> a global collection (product docs), which
//	                                 has no llm_knowledgebases row by design
//
// Nothing in the point payload carries a kb id, so this is the only identity
// available. Absent on hits from a rag-server predating the stamp; callers then
// fall back to the text/source rules in matchKB.
func docCollection(doc toolcore.RAGSearchResult) (string, bool) {
	name, _ := doc.Metadata["collection"].(string)
	name = strings.TrimSpace(name)
	return name, name != ""
}

// classifyCollection splits a collection name into (kbID, integrationID). Both
// empty means the collection is global — not backed by any knowledge base.
func classifyCollection(name string) (kbID, integrationID string) {
	switch {
	case strings.HasPrefix(name, "kb_"):
		return strings.TrimPrefix(name, "kb_"), ""
	case strings.HasSuffix(name, "_knowledge_base"):
		return "", strings.TrimSuffix(name, "_knowledge_base")
	default:
		return "", ""
	}
}

// unownedName labels an un-owned document's origin for the references panel.
// doc.Metadata["source"] is the natural name ("nudgebee_docs"), but not every
// indexed page carries one — a legacy per-account collection has no source tag,
// and the row then rendered with a blank name. Fall back to the scope, then the
// collection itself, so the panel always names where the content came from.
func unownedName(docSource, scope, collection string) string {
	if s := strings.TrimSpace(docSource); s != "" {
		return s
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "account":
		return "Account documents"
	case "tenant":
		return "Tenant documents"
	case "global":
		return "NudgeBee documentation"
	}
	if c := strings.TrimSpace(collection); c != "" {
		return c
	}
	return "Unknown source"
}

// unownedKindForScope names the origin of a document from a collection with no
// knowledge-base row. "External" alone was ambiguous: it covered NudgeBee's own
// product docs, a customer's tenant-level user KB, and legacy per-account
// collections, so the panel could label customer content as a NudgeBee doc.
func unownedKindForScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "account":
		return AgentReferenceKindAccountDocument
	case "tenant":
		return AgentReferenceKindTenantDocument
	default:
		return AgentReferenceKindNBDocument
	}
}

// scopeOrDefault labels a reference row with the collection scope rag-server
// reported, defaulting to "global" for older payloads that carry no scope.
func scopeOrDefault(scope string) string {
	if s := strings.TrimSpace(scope); s != "" {
		return s
	}
	return "global"
}

// The third return is the number of documents dropped as unattributable. It is
// counted at the drop site rather than inferred from len(docs)-len(attributed),
// so the caller's warning stays exact even if this is ever handed duplicates.
func attributeKBReferences(ctx *security.RequestContext, accountId string, docs toolcore.RAGSearchResults, kbs []toolcore.Knowledgebase) ([]AgentReference, toolcore.RAGSearchResults, int) {
	if len(docs) == 0 {
		return nil, nil, 0
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
		// Identity first: when rag-server reports the collection, the owning KB
		// is known exactly — no text or category guessing.
		if name, ok := docCollection(doc); ok {
			kbID, integrationID := classifyCollection(name)
			for _, kb := range kbs {
				if kb.Id == "" || kb.Status != "active" {
					continue
				}
				if kbID != "" && strings.EqualFold(kbID, kb.Id) {
					return kb, true
				}
				if integrationID != "" && kb.IntegrationId != nil &&
					strings.EqualFold(integrationID, *kb.IntegrationId) {
					return kb, true
				}
			}
		}
		owns := func(kb toolcore.Knowledgebase) bool {
			if data := dataByKB[kb.Id]; data != "" && strings.Contains(data, content) {
				return true
			}
			return kb.KBType == "integration" && kb.KBSource != nil && docSource != "" &&
				strings.EqualFold(*kb.KBSource, docSource)
		}
		// Active only. An archived KB must never credit — nor supply — content.
		for _, kb := range kbs {
			if kb.Id == "" || kb.Status != "active" {
				continue
			}
			if owns(kb) {
				return kb, true
			}
		}
		return toolcore.Knowledgebase{}, false
	}

	seenDocs := make(map[string]struct{})
	var refs []AgentReference
	var attributed toolcore.RAGSearchResults
	dropped := 0
	for _, doc := range docs {
		kb, ok := matchKB(doc)
		if !ok {
			// The doc reached the prompt budget but no KB owns it, so it is
			// dropped entirely (fail-closed). Log every input the two rules
			// consult so the miss is diagnosable without a debugger: the doc's
			// source tag, whether any candidate had a body to substring-match,
			// and how many candidates were even eligible.
			docSource, _ := doc.Metadata["source"].(string)
			// Trimmed to match the owned path's dedup key — an untrimmed url
			// here would let the same page be recorded twice.
			url, _ := doc.Metadata["url"].(string)
			url = strings.TrimSpace(url)
			activeCandidates, withBody := 0, 0
			for _, kb := range kbs {
				if kb.Status == "active" && kb.Id != "" {
					activeCandidates++
					if dataByKB[kb.Id] != "" {
						withBody++
					}
				}
			}
			// Global collections have no KB row to attribute to, so record them
			// from the document's own identity (url + source) rather than
			// dropping content the product ships on purpose. The invariant is
			// "nothing enters the prompt unrecorded" — not "everything must map
			// to a knowledge base".
			collectionName, stamped := docCollection(doc)
			// rag-server classifies the owning collection from ITS OWN metadata
			// and reports whether an llm_knowledgebases row can exist for it.
			// Not KB-backed covers three cases — the global product-docs
			// collection, the tenant-level user KB, and legacy per-account
			// collections — none of which can ever be attributed to a KB.
			// Demanding one drops content that is legitimately un-owned, so
			// record it from the document's own identity instead.
			scope, _ := doc.Metadata["collection_scope"].(string)
			kbBacked, stampedBacked := doc.Metadata["collection_kb_backed"].(bool)
			unowned := stampedBacked && !kbBacked
			if unowned && url != "" {
				// Same key as dedupRAGDocs, which already ran over these docs,
				// so this cannot drop a distinct chunk — only a re-run copy.
				if _, dup := seenDocs[ragDocDedupKey(doc)]; dup {
					continue
				}
				seenDocs[ragDocDedupKey(doc)] = struct{}{}
				collectionModule, _ := doc.Metadata["collection_module"].(string)
				metadata := map[string]any{
					// kind is what the UI switches on: these rows share
					// reference_type "knowledge_base" with attributed KB
					// documents and with loaded skills, and are otherwise
					// indistinguishable in the Additional Contexts panel.
					"kind":   unownedKindForScope(scope),
					"name":   unownedName(docSource, scope, collectionName),
					"via":    "kb_prestep",
					"scope":  scopeOrDefault(scope),
					"module": collectionModule,
					"url":    url,
				}
				if title, ok := doc.Metadata["title"].(string); ok && strings.TrimSpace(title) != "" {
					metadata["subject"] = strings.TrimSpace(title)
				} else if line := firstLine(doc.Document, kbRefSubjectMaxChars); line != "" {
					metadata["subject"] = line
				}
				if snippet := strings.TrimSpace(doc.Document); snippet != "" {
					metadata["content"] = TruncateHead(snippet, kbRefSnippetMaxChars)
				}
				attributed = append(attributed, doc)
				refs = append(refs, AgentReference{
					Type: AgentReferenceTypeKB,
					// Non-UUID by construction, so the DAO's llm_knowledgebases
					// join misses and the row renders from metadata instead.
					ReferenceID: fmt.Sprintf("global:%s:%x", docSource, sha256.Sum256([]byte(url))),
					Metadata:    metadata,
				})
				ctx.GetLogger().Info("kb_prestep: un-owned collection document recorded",
					"url", url, "doc_source", docSource, "collection", collectionName,
					"collection_scope", scope, "kb_backed", kbBacked)
				continue
			}
			dropped++
			ctx.GetLogger().Warn("kb_prestep: document not attributable - dropping",
				"url", url, "doc_source", docSource, "chars", len(strings.TrimSpace(doc.Document)),
				"candidates", len(kbs), "active_candidates", activeCandidates,
				"candidates_with_body", withBody, "collection", collectionName,
				"collection_stamped", stamped, "collection_scope", scope,
				"kb_backed", kbBacked)
			continue
		}
		ctx.GetLogger().Info("kb_prestep: document attributed",
			"kb_id", kb.Id, "kb_name", kb.Name, "kb_type", kb.KBType, "kb_status", kb.Status)
		// Already collapsed by dedupRAGDocs upstream; re-checked so this
		// function stays correct if it is ever called with raw results.
		key := ragDocDedupKey(doc)
		if _, dup := seenDocs[key]; dup {
			continue
		}
		seenDocs[key] = struct{}{}

		metadata := map[string]any{
			"kind":      AgentReferenceKindKBDocument,
			"name":      kb.Name,
			"kb_id":     kb.Id,
			"via":       "kb_prestep",
			"kb_status": kb.Status,
			// kb_type/kb_source let the panel say WHERE a knowledge base's
			// content came from — a synced Confluence space reads differently to
			// a hand-written KB, and "Knowledge Base" alone hides that.
			"kb_type": kb.KBType,
		}
		if kb.KBSource != nil && strings.TrimSpace(*kb.KBSource) != "" {
			metadata["kb_source"] = strings.TrimSpace(*kb.KBSource)
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
		attributed = append(attributed, doc)
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
	return refs, attributed, dropped
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
	sb.WriteString("The following knowledge base content was retrieved for this request. Use this content as a supporting reference while analyzing the issue — this content is guidance, not verified fact, and may be stale or only partly relevant. Prefer live evidence from tools when the retrieved content and tool evidence disagree, and report the mismatch rather than treating the mismatch as a blocker. When a retrieved document prescribes investigation steps (a runbook or SOP), FOLLOW its steps in order. When your findings rely on any of this knowledge, cite its Source url.\n")
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
