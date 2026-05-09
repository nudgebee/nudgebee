package core

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// SkillCandidate is the minimum metadata needed to score a skill against a query.
// Body is intentionally NOT included — selection runs over name + description so
// we can score thousands of mapped skills without paying the cost of fetching
// the bodies first. The selected IDs are then loaded eagerly via a follow-up
// query.
type SkillCandidate struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
}

// stopWords are filtered before scoring. Kept tiny on purpose — over-aggressive
// stopword removal hurts short technical descriptions ("how to scale a
// deployment" → "scale deployment" is fine but losing "how" is OK; losing
// "deployment" would be terrible).
var skillSelectionStopWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"to": {}, "of": {}, "in": {}, "on": {}, "at": {}, "by": {}, "for": {}, "with": {},
	"from": {}, "as": {}, "into": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"it": {}, "its": {}, "if": {}, "then": {}, "than": {}, "so": {},
	"do": {}, "does": {}, "did": {}, "doing": {},
	"can": {}, "could": {}, "should": {}, "would": {}, "will": {}, "may": {}, "might": {},
	"i": {}, "you": {}, "we": {}, "they": {}, "he": {}, "she": {},
	"my": {}, "our": {}, "your": {}, "their": {},
}

// TokenizeForSkillSelection lowercases the input, splits on any non-alphanumeric
// rune, and drops stopwords plus single-character tokens. Pure stdlib so the
// scorer has zero dependencies and is trivially testable.
func TokenizeForSkillSelection(s string) []string {
	if s == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ToLower(f)
		if len(f) <= 1 {
			continue
		}
		if _, stop := skillSelectionStopWords[f]; stop {
			continue
		}
		out = append(out, f)
	}
	return out
}

// SelectRelevantSkills returns the top-K most query-relevant SkillCandidate IDs
// using a lightweight BM25 scorer over (name + description). The scoring corpus
// is the candidate set itself — there is no global IDF, which keeps the helper
// dependency-free and bounds memory to O(candidates). Skills with zero query
// overlap are dropped entirely.
//
// When topK <= 0 or len(candidates) <= topK every input ID is returned (no
// useful filtering possible). When the query is empty all input IDs are returned
// in their original order — the caller is expected to gate this helper behind
// a config flag and a non-empty query.
func SelectRelevantSkills(query string, candidates []SkillCandidate, topK int) []string {
	if len(candidates) == 0 {
		return nil
	}
	if topK <= 0 || len(candidates) <= topK {
		ids := make([]string, len(candidates))
		for i, c := range candidates {
			ids[i] = c.ID
		}
		return ids
	}

	queryTokens := TokenizeForSkillSelection(query)
	if len(queryTokens) == 0 {
		ids := make([]string, len(candidates))
		for i, c := range candidates {
			ids[i] = c.ID
		}
		return ids
	}

	// Tokenize each document once. Doc text is name + " " + description so a
	// matching skill name is weighted the same as a matching description term;
	// short names get a small boost from BM25's length normalisation.
	docTokens := make([][]string, len(candidates))
	totalLen := 0
	for i, c := range candidates {
		toks := TokenizeForSkillSelection(c.Name + " " + c.Description)
		docTokens[i] = toks
		totalLen += len(toks)
	}
	avgDocLen := float64(totalLen) / float64(len(candidates))
	if avgDocLen == 0 {
		// All docs empty after tokenization — keep input order, drop excess.
		ids := make([]string, 0, topK)
		for i := 0; i < topK && i < len(candidates); i++ {
			ids = append(ids, candidates[i].ID)
		}
		return ids
	}

	// Document frequency over the candidate set. Iterate docs once and bump df
	// at most once per (term, doc) by recording the terms seen in each doc in a
	// small set first. A naive "for each query token, scan every doc" loop over-
	// counts when queryTokens has duplicates (e.g. "error error") — each repeat
	// would bump df again for the same doc, inflating df and deflating IDF.
	querySet := make(map[string]struct{}, len(queryTokens))
	for _, q := range queryTokens {
		querySet[q] = struct{}{}
	}
	df := make(map[string]int, len(querySet))
	for _, toks := range docTokens {
		seenInDoc := make(map[string]struct{})
		for _, t := range toks {
			if _, ok := querySet[t]; !ok {
				continue
			}
			if _, already := seenInDoc[t]; already {
				continue
			}
			seenInDoc[t] = struct{}{}
			df[t]++
		}
	}

	// BM25 with the conventional k1=1.5, b=0.75. N is the candidate count.
	const (
		k1 = 1.5
		b  = 0.75
	)
	n := float64(len(candidates))

	type scored struct {
		idx   int
		score float64
	}
	results := make([]scored, 0, len(candidates))
	for i, toks := range docTokens {
		if len(toks) == 0 {
			continue
		}
		// Term frequency for each query term in this doc.
		tf := make(map[string]int, len(queryTokens))
		for _, t := range toks {
			tf[t]++
		}
		var score float64
		for _, q := range queryTokens {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			docFreq := float64(df[q])
			// BM25 IDF (Robertson/Sparck-Jones variant). Clamped to >= 0 to
			// avoid the negative-IDF pathology when a term hits more than half
			// of the corpus.
			idf := math.Log((n-docFreq+0.5)/(docFreq+0.5) + 1)
			if idf < 0 {
				idf = 0
			}
			norm := f * (k1 + 1) / (f + k1*(1-b+b*float64(len(toks))/avgDocLen))
			score += idf * norm
		}
		if score > 0 {
			results = append(results, scored{idx: i, score: score})
		}
	}

	if len(results) == 0 {
		// Selection ran against a non-empty candidate set and scored everything
		// to zero. Return an empty (non-nil) slice so callers can distinguish
		// "selection disabled" (nil return paths above) from "selection ran and
		// chose nothing" — the latter must suppress inherited skills downstream.
		return []string{}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = candidates[r.idx].ID
	}
	return ids
}
