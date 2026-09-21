package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// KBRetrievalProbeDoc is one retrieved document as the probe reports it.
// Injected mirrors whether the KB pre-step would put this document in the
// prompt; DropReason explains a false.
type KBRetrievalProbeDoc struct {
	Rank       int     `json:"rank"`
	Score      float32 `json:"score"`
	Subject    string  `json:"subject"`
	KBId       string  `json:"kb_id,omitempty"`
	KBName     string  `json:"kb_name,omitempty"`
	URL        string  `json:"url,omitempty"`
	Source     string  `json:"source,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
	Injected   bool    `json:"injected"`
	DropReason string  `json:"drop_reason,omitempty"`
}

// KBRetrievalProbeKB is one attribution candidate and whether the query
// actually pulled anything out of it. A mapped KB with Matched=false is the
// signal the operator came for: it is wired up but contributed nothing.
type KBRetrievalProbeKB struct {
	Id     string `json:"id"`
	Name   string `json:"name"`
	KBType string `json:"kb_type"`
	Status string `json:"status"`
	// Enabled is the user's switch. Reported alongside Status so the panel can
	// tell an operator WHY a knowledge base could not contribute — "switched
	// off" is a one-click fix, "failed to load" is not.
	Enabled bool `json:"enabled"`
	Matched bool `json:"matched"`
	// Eligible is false for a knowledge base attribution can never credit —
	// anything not active (archived, or a failed load left in "error"), and
	// anything the user has switched off. Reported separately because lumping
	// these in with the matched-nothing list tells an operator a KB had a fair
	// chance and lost, when in fact it was never in the running for ANY
	// question. See matchKB, which skips every candidate failing either gate.
	Eligible bool `json:"eligible"`
}

// KBRetrievalProbeResult is the whole answer to "what would the agent get?".
type KBRetrievalProbeResult struct {
	Query      string                `json:"query"`
	AgentId    string                `json:"agent_id,omitempty"`
	KBId       string                `json:"kb_id,omitempty"`
	TopK       int                   `json:"top_k"`
	Retrieved  int                   `json:"retrieved"`
	Injected   int                   `json:"injected"`
	Dropped    int                   `json:"dropped"`
	TimedOut   bool                  `json:"timed_out"`
	Documents  []KBRetrievalProbeDoc `json:"documents"`
	Candidates []KBRetrievalProbeKB  `json:"candidates"`
}

// dropReasonUnattributable is the only drop the pre-step performs: a document
// came back from RAG but no candidate KB owns it, so it is failed closed
// (retrieveRelevantKB -> attributeKBReferences). There is deliberately NO
// relative score cutoff — see the comment in retrieveRelevantKB.
const dropReasonUnattributable = "No knowledge base in scope owns this document, so the pre-step drops it (fail-closed)."

// ProbeKBRetrieval answers "what would actually be retrieved for this
// question?" by running the SAME path as the KB pre-step: a reranked search on
// the knowledge_base module, the same dedup, the same attribution.
//
// It differs from retrieveRelevantKB in exactly two ways, both deliberate:
// token usage is NOT tracked (an operator testing a query must not land on the
// account's LLM bill or skew per-agent usage attribution), and the documents
// that attribution drops are reported instead of only counted.
//
// Scope is one of three, in precedence order:
//
//   - kbId set — search ONLY that knowledge base's collection. Answers "does
//     THIS knowledge base answer the question?". Note it removes the
//     competition for the top-k slots that decides real retrieval, so a hit
//     here does NOT mean an agent would receive it.
//   - agentId set — the agent's mapped KBs, widened with the account's active
//     integration KBs, exactly as the pre-step does.
//   - neither — every KB in the account. Strictly wider than any agent sees,
//     so a document injected here but not under an agent is the KB->agent
//     mapping gate rather than a retrieval failure.
func ProbeKBRetrieval(ctx *security.RequestContext, accountId, agentId, kbId, query string) (KBRetrievalProbeResult, error) {
	query = strings.TrimSpace(query)
	result := KBRetrievalProbeResult{
		Query:      query,
		AgentId:    agentId,
		KBId:       kbId,
		TopK:       kbPrestepTopK,
		Documents:  []KBRetrievalProbeDoc{},
		Candidates: []KBRetrievalProbeKB{},
	}
	if query == "" {
		return result, nil
	}

	// Scope: one knowledge base, an agent's mapped KBs, else the whole account.
	var kbs []toolcore.Knowledgebase
	var scopedCollection string
	var err error
	switch {
	case kbId != "":
		acctKBs, listErr := listAccountKBsFn(ctx, accountId)
		if listErr != nil {
			return result, listErr
		}
		// Resolving through the account's OWN list is what stops this being a
		// read primitive for someone else's knowledge base: an id the account
		// does not own simply is not found.
		var scoped *toolcore.Knowledgebase
		for i := range acctKBs {
			if acctKBs[i].Id == kbId {
				scoped = &acctKBs[i]
				break
			}
		}
		if scoped == nil {
			return result, fmt.Errorf("kb_probe: knowledge base %s not found in this account", kbId)
		}
		collection, ok := kbCollectionName(*scoped)
		if !ok {
			return result, fmt.Errorf("kb_probe: knowledge base %q has no searchable collection", scoped.Name)
		}
		scopedCollection = collection
		kbs = []toolcore.Knowledgebase{*scoped}
	case agentId != "":
		kbs, err = toolcore.ListAgentKBs(ctx, accountId, agentId)
	default:
		kbs, err = listAccountKBsFn(ctx, accountId)
	}
	if err != nil {
		return result, err
	}

	// Same search the pre-step issues, minus token tracking. Bound retrieval by
	// both the probe deadline and request cancellation so abandoned probes do
	// not leave RAG work running in the background.
	userId := ctx.GetSecurityContext().GetUserId()
	queryCtx, cancel := context.WithTimeout(ctx.GetContext(), kbPrestepTimeout())
	defer cancel()
	ch := make(chan toolcore.RAGSearchResults, 1)
	go func() {
		if scopedCollection != "" {
			ch <- toolcore.QueryRAGCollectionReranked(queryCtx, userId, accountId, query,
				"knowledge_base", scopedCollection, kbPrestepTopK, "", "", agentId, false)
			return
		}
		ch <- toolcore.QueryRAGRerankedContext(queryCtx, userId, accountId, query,
			"knowledge_base", kbPrestepTopK, "", "", agentId, false)
	}()
	var docs toolcore.RAGSearchResults
	select {
	case docs = <-ch:
	case <-queryCtx.Done():
	}
	if queryErr := queryCtx.Err(); queryErr != nil {
		if !errors.Is(queryErr, context.DeadlineExceeded) {
			return result, queryErr
		}
		ctx.GetLogger().Warn("kb_probe: retrieval timed out",
			"account_id", accountId, "agent_id", agentId, "kb_id", kbId)
		result.TimedOut = true
		return result, nil
	}

	// Belt and braces for the scoped case. rag-server enforces the narrowing,
	// but an older rag-server that does not know restrict_to_collection treats
	// the name as ADDITIVE and answers from every collection — a panel claiming
	// to search one knowledge base while showing another's documents. Dropping
	// foreign collections here keeps the answer truthful (at worst incomplete)
	// whichever version is deployed.
	if scopedCollection != "" {
		docs = filterDocsToCollection(docs, scopedCollection)
	}

	kept := dedupRAGDocs(docs)
	result.Retrieved = len(kept)

	// A single-KB probe attributes against that KB alone; widening with the
	// account's integration KBs would credit documents the scope excluded.
	candidates := kbs
	if scopedCollection == "" {
		candidates = mergeAccountIntegrationKBs(ctx, accountId, kbs)
	}
	refs, attributed, dropped := attributeKBReferences(ctx, accountId, kept, candidates)
	result.Injected = len(attributed)
	result.Dropped = dropped

	docs2, matchedKBIds := buildProbeDocuments(kept, refs, attributed)
	result.Documents = docs2

	result.Candidates = summarizeCandidates(candidates, matchedKBIds)

	ctx.GetLogger().Info("kb_probe: retrieval complete", "account_id", accountId, "agent_id", agentId,
		"retrieved", result.Retrieved, "injected", result.Injected, "dropped", result.Dropped,
		"candidates", len(result.Candidates))
	return result, nil
}

// buildProbeDocuments pairs every retrieved document with what attribution
// decided about it. attributeKBReferences appends to refs and attributed
// together, so index i of one describes index i of the other; a document whose
// dedup key is absent from attributed is one it dropped.
//
// The second return is the set of KB ids that actually contributed a document,
// used to mark the in-scope knowledge bases that matched nothing.
func buildProbeDocuments(kept toolcore.RAGSearchResults, refs []AgentReference, attributed toolcore.RAGSearchResults) ([]KBRetrievalProbeDoc, map[string]struct{}) {
	injectedKeys := make(map[string]int, len(attributed))
	for i, doc := range attributed {
		injectedKeys[ragDocDedupKey(doc)] = i
	}
	matchedKBIds := make(map[string]struct{}, len(refs))
	out := make([]KBRetrievalProbeDoc, 0, len(kept))

	for rank, doc := range kept {
		url, _ := doc.Metadata["url"].(string)
		source, _ := doc.Metadata["source"].(string)
		entry := KBRetrievalProbeDoc{
			Rank:    rank,
			Score:   doc.SimilarityScore,
			URL:     strings.TrimSpace(url),
			Source:  source,
			Snippet: TruncateHead(strings.TrimSpace(doc.Document), kbRefSnippetMaxChars),
		}
		if title, ok := doc.Metadata["title"].(string); ok && strings.TrimSpace(title) != "" {
			entry.Subject = strings.TrimSpace(title)
		} else {
			entry.Subject = firstLine(doc.Document, kbRefSubjectMaxChars)
		}

		// index guarded: refs and attributed are built in lockstep, but a
		// future edit to attributeKBReferences must not turn a drift into a
		// panic in a read-only diagnostic.
		if i, ok := injectedKeys[ragDocDedupKey(doc)]; ok && i < len(refs) {
			entry.Injected = true
			ref := refs[i]
			if name, nameOk := ref.Metadata["name"].(string); nameOk {
				entry.KBName = name
			}
			if subject, subjOk := ref.Metadata["subject"].(string); subjOk && strings.TrimSpace(subject) != "" {
				entry.Subject = strings.TrimSpace(subject)
			}
			// ReferenceID is "<kb id>:<hash>" (distinct per document), so the
			// owning KB is read from metadata. Un-owned (global / tenant)
			// collection documents are recorded from their own identity and
			// carry no kb_id at all - they are injected but belong to no KB.
			if kbId, idOk := ref.Metadata["kb_id"].(string); idOk && kbId != "" {
				entry.KBId = kbId
				matchedKBIds[kbId] = struct{}{}
			}
		} else {
			entry.DropReason = dropReasonUnattributable
		}
		out = append(out, entry)
	}
	return out, matchedKBIds
}

// summarizeCandidates reports each attribution candidate with whether it
// contributed a document and whether it was ever eligible to. Eligibility
// mirrors matchKB's own guard (UsableForAgents: active AND not switched off)
// so the panel cannot claim a KB "contributed nothing" when attribution would
// have skipped it regardless of the question.
func summarizeCandidates(candidates []toolcore.Knowledgebase, matchedKBIds map[string]struct{}) []KBRetrievalProbeKB {
	out := make([]KBRetrievalProbeKB, 0, len(candidates))
	for _, kb := range candidates {
		_, matched := matchedKBIds[kb.Id]
		out = append(out, KBRetrievalProbeKB{
			Id:       kb.Id,
			Name:     kb.Name,
			KBType:   kb.KBType,
			Status:   kb.Status,
			Enabled:  kb.Enabled,
			Matched:  matched,
			Eligible: kb.Id != "" && kb.UsableForAgents(),
		})
	}
	return out
}

// kbCollectionName derives the vector collection holding a knowledge base's
// documents. Inverse of classifyCollection, and the naming must stay in step
// with it AND with rag-server's _is_kb_backed_collection:
//
//   - manual KB      -> "kb_<kb id>"
//   - integration KB -> "<integration id>_knowledge_base"
//
// Returns false when the knowledge base has no collection of its own — an
// integration KB with no integration id is reachable only via attribution.
func kbCollectionName(kb toolcore.Knowledgebase) (string, bool) {
	if kb.KBType == "integration" {
		if kb.IntegrationId == nil || strings.TrimSpace(*kb.IntegrationId) == "" {
			return "", false
		}
		return strings.TrimSpace(*kb.IntegrationId) + "_knowledge_base", true
	}
	if strings.TrimSpace(kb.Id) == "" {
		return "", false
	}
	return "kb_" + strings.TrimSpace(kb.Id), true
}

// filterDocsToCollection keeps only documents rag-server stamped with the given
// collection. Documents carrying NO collection tag are kept: the tag comes from
// rag-server's search layer, and dropping untagged documents would silently
// discard content on any deployment that stops stamping it.
func filterDocsToCollection(docs toolcore.RAGSearchResults, collection string) toolcore.RAGSearchResults {
	out := make(toolcore.RAGSearchResults, 0, len(docs))
	for _, doc := range docs {
		name, stamped := docCollection(doc)
		if stamped && !strings.EqualFold(name, collection) {
			continue
		}
		out = append(out, doc)
	}
	return out
}
