package knowledge

import (
	"encoding/json"
	"strings"

	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// KnowledgeSearchDocument is one passage a search scored, and how far it got.
type KnowledgeSearchDocument struct {
	Title   string `json:"title"`
	Url     string `json:"url,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	// Stage is how far down the loading pipeline this passage got. It reports
	// a position, not a judgement: a passage dropped by search ranking was
	// never assessed for relevance by anything, and saying "a closer match was
	// used instead" invented a decision that never happened.
	Stage string `json:"stage"`
	// Why explains the stage in the reader's terms, not the pipeline's.
	Why string `json:"why,omitempty"`
}

// KnowledgeSearch is one search of the knowledge base, as a funnel.
type KnowledgeSearch struct {
	Query string `json:"query"`
	// Scored is every passage the search considered; Returned is what survived
	// its ranking; Added is what the agent was actually given.
	Scored    int                       `json:"scored"`
	Returned  int                       `json:"returned"`
	Added     int                       `json:"added"`
	Outcome   string                    `json:"outcome"`
	Summary   string                    `json:"summary"`
	Documents []KnowledgeSearchDocument `json:"documents"`
}

// KnowledgeActivity is every knowledge-base search behind one answer.
type KnowledgeActivity struct {
	Searches []KnowledgeSearch `json:"searches"`
	Scored   int               `json:"scored"`
	Returned int               `json:"returned"`
	Added    int               `json:"added"`
	Summary  string            `json:"summary"`
}

// Where a passage stopped. Mirrors the trace's own candidate statuses so the
// panel reports the pipeline rather than reinterpreting it.
const (
	stageAdded          = "added"           // the agent was given this
	stageReturned       = "returned"        // search returned it; it was not added
	stageDroppedSibling = "dropped_sibling" // a better passage of the same document won
	stageDroppedRank    = "dropped_rank"    // search ranking cut it before anything saw it
)

// Outcomes, in the order a reader would ask about them.
const (
	outcomeUsed        = "used"                   // a document answered the question
	outcomeNotRelevant = "not_relevant"           // documents were found, none answered it
	outcomeNothing     = "nothing_found"          // the search matched nothing
	outcomeUnchecked   = "unchecked"              // relevance was never decided
	outcomeElsewhere   = "closer_match_elsewhere" // another search supplied the answer
)

// Plain-language explanations. These are the whole point of the panel: a reader
// asking "why didn't it load my runbook" needs the difference between "search
// never returned it" and "it was returned and something else was picked".
const (
	whyPicked        = "Picked as the best match and given to the agent."
	whyUnchecked     = "Given to the agent without a relevance check."
	whyOtherPicked   = "Returned by search, but a different document was picked."
	whyNotAnswer     = "Returned by search, but judged not to answer this question."
	whySamePage      = "Another passage of the same document scored higher."
	whyOutranked     = "Ranked below the passages that were returned."
	summaryNone      = "Nothing the search returned answered the question."
	summaryEmpty     = "The search matched no documents."
	summaryNoKBs     = "No knowledge base was available to search."
	summaryElsewhere = "A document from another search was picked instead."
	summaryUnsure    = "Relevance was not checked — everything the search returned was passed on."
)

type traceCandidate struct {
	Ref     string `json:"ref"`
	Snippet string `json:"snippet"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Url     string `json:"url"`
}

// GetKnowledgeActivity reports what the knowledge base did for each message.
//
// Batched over message ids, like ai_list_references, because the caller renders a
// whole conversation: one call per message would be one round trip per turn on
// every page load.
//
// Reads llm_retrieval_trace, which rag-server writes per search and the judge
// updates with its verdict. Scoped by account as well as conversation so a
// conversation id alone cannot read another account's rows.
func GetKnowledgeActivity(rc *security.RequestContext, accountID, conversationID string, messageIDs []string) (map[string]KnowledgeActivity, error) {
	byMessage := map[string]KnowledgeActivity{}
	if conversationID == "" || len(messageIDs) == 0 {
		return byMessage, nil
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return byMessage, err
	}

	const q = `SELECT message_id, query, coalesce(n_retrieved, 0), coalesce(judge_verdict, ''),
		       coalesce(judge_kept_ref, ''), coalesce(fallback_reason, ''), coalesce(candidates, '[]'::jsonb)
		FROM llm_retrieval_trace
		WHERE conversation_id = $1 AND message_id = ANY($2) AND cloud_account_id = $3
		  AND module = 'knowledge_base'
		ORDER BY created_at`
	rows, err := dbms.Db.Query(q, conversationID, pq.Array(messageIDs), accountID)
	if err != nil {
		return byMessage, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var messageID, query, verdict, keptRef, fallback string
		var retrieved int
		var candidatesJSON []byte
		if err := rows.Scan(&messageID, &query, &retrieved, &verdict, &keptRef, &fallback, &candidatesJSON); err != nil {
			return byMessage, err
		}
		var candidates []traceCandidate
		if err := json.Unmarshal(candidatesJSON, &candidates); err != nil {
			// A row whose candidates cannot be read still tells the reader a
			// search happened; dropping it would under-report the activity.
			rc.GetLogger().Warn("knowledge activity: unreadable candidates", "error", err)
			candidates = nil
		}
		activity := byMessage[messageID]
		if activity.Searches == nil {
			activity.Searches = []KnowledgeSearch{}
		}
		activity.Searches = append(activity.Searches, buildKnowledgeSearch(query, retrieved, verdict, keptRef, fallback, candidates))
		byMessage[messageID] = activity
	}
	if err := rows.Err(); err != nil {
		return byMessage, err
	}

	for messageID, activity := range byMessage {
		byMessage[messageID] = withActivityTotals(activity)
	}
	return byMessage, nil
}

// withActivityTotals fills the per-message rollup.
//
// Counted by distinct document, not by search: the pre-step fans out over
// several collections and the same page comes back from more than one of them,
// so summing per-search counts reported one document added three times.
func withActivityTotals(activity KnowledgeActivity) KnowledgeActivity {
	scored, returned, added := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, s := range activity.Searches {
		for _, doc := range s.Documents {
			key := doc.Url
			if key == "" {
				key = doc.Title + "\x00" + doc.Snippet
			}
			scored[key] = struct{}{}
			if doc.Stage == stageAdded {
				added[key] = struct{}{}
			}
			if doc.Stage == stageAdded || doc.Stage == stageReturned {
				returned[key] = struct{}{}
			}
		}
	}
	activity.Scored = len(scored)
	activity.Returned = len(returned)
	activity.Added = len(added)
	activity.Summary = activitySummary(activity)
	return activity
}

func buildKnowledgeSearch(query string, retrieved int, verdict, keptRef, fallback string, candidates []traceCandidate) KnowledgeSearch {
	search := KnowledgeSearch{Query: query, Scored: retrieved, Documents: []KnowledgeSearchDocument{}}

	for _, c := range candidates {
		doc := KnowledgeSearchDocument{Title: candidateTitle(c), Url: c.Url, Snippet: c.Snippet}
		switch {
		// Dropped by search ranking, before the judge existed for this passage.
		// Checked first: these never reached any relevance decision, so any
		// verdict-derived wording would describe something that never happened.
		case c.Status == stageDroppedSibling:
			doc.Stage, doc.Why = stageDroppedSibling, whySamePage
		case c.Status == stageDroppedRank:
			doc.Stage, doc.Why = stageDroppedRank, whyOutranked
		case verdict == judgeVerdictKept && c.Ref != "" && c.Ref == keptRef:
			doc.Stage, doc.Why = stageAdded, whyPicked
		case verdict == judgeVerdictKept:
			doc.Stage, doc.Why = stageReturned, whyOtherPicked
		case verdict == judgeVerdictNone:
			doc.Stage, doc.Why = stageReturned, whyNotAnswer
		case verdict == "" || verdict == judgeVerdictUnavailable:
			// No judge ran, or it could not run and failed open. Either way the
			// whole shortlist went to the agent unfiltered, so a returned
			// passage WAS added — and the reader is told it was never checked.
			doc.Stage, doc.Why = stageAdded, whyUnchecked
		default:
			doc.Stage, doc.Why = stageReturned, whyOtherPicked
		}

		if doc.Stage == stageAdded {
			search.Added++
		}
		if doc.Stage == stageAdded || doc.Stage == stageReturned {
			search.Returned++
		}
		search.Documents = append(search.Documents, doc)
	}

	switch {
	case search.Added > 0:
		search.Outcome = outcomeUsed
	case retrieved == 0 && fallback == "no_collections":
		search.Outcome, search.Summary = outcomeNothing, summaryNoKBs
	case retrieved == 0:
		search.Outcome, search.Summary = outcomeNothing, summaryEmpty
	case verdict == judgeVerdictNone:
		search.Outcome, search.Summary = outcomeNotRelevant, summaryNone
	case verdict == judgeVerdictKept:
		// The judge kept a document, but not one of this search's. The pre-step
		// judges several searches as one set, so this is the ordinary outcome
		// for the searches that did not supply the winner.
		search.Outcome, search.Summary = outcomeElsewhere, summaryElsewhere
	case verdict == judgeVerdictUnavailable:
		search.Outcome, search.Summary = outcomeUnchecked, summaryUnsure
	default:
		search.Outcome = outcomeUnchecked
	}
	return search
}

// candidateTitle names the document for a reader.
//
// The first line is used only when it reads like one: chunks start wherever the
// splitter cut, so the fallback otherwise produced titles like "he" and
// "tion." — worse than no title, because the reader takes them for the
// document's name. With no usable title the caller renders the snippet instead.
func candidateTitle(c traceCandidate) string {
	if c.Title != "" {
		return c.Title
	}
	line := strings.TrimSpace(firstLineOf(c.Snippet, 90))
	if len(line) < 12 {
		return ""
	}
	first := rune(line[0])
	if first == '#' || (first >= 'A' && first <= 'Z') {
		return line
	}
	return ""
}

func activitySummary(a KnowledgeActivity) string {
	switch {
	case len(a.Searches) == 0:
		return ""
	case a.Added > 0:
		return ""
	case a.Scored == 0:
		return summaryEmpty
	default:
		return summaryNone
	}
}

func firstLineOf(text string, limit int) string {
	for i, r := range text {
		if r == '\n' {
			text = text[:i]
			break
		}
	}
	if len(text) > limit {
		return text[:limit]
	}
	return text
}
