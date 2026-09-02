package workflow

import (
	"sort"
	"strings"
	"unicode"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

// defaultAISearchLimit caps how many candidates come back. The result is read by
// a model inside a chat turn, where a long list is worse than a short one: it
// costs tokens and invites picking something plausible-but-wrong. Five is enough
// for the model to choose, or to tell the user nothing fits.
const defaultAISearchLimit = 5

// maxAISearchLimit bounds what a caller can ask for.
//
// llm-server's automation tool source fetches one past its own per-account cap
// so it can tell "exactly at the cap" from "overflowing" and log what it
// dropped. That only works while its cap stays strictly below this clamp — set
// them equal and the +1 is clamped away and the overflow is invisible. If this
// ever needs lowering, check maxAutomationTools in
// llm/llm-server/tools/tool_workflow_automation.go first.
const maxAISearchLimit = 25

// Field weights for ranking. The name outranks the description because it is
// what an author picks deliberately, whereas a description is prose and matches
// more loosely. Mirrors llm-server's search_tools weighting so discovery behaves
// consistently wherever it happens.
const (
	weightName        = 3
	weightDescription = 1

	// A token that matches exactly earns exactMatchFactor x the field weight; one
	// that only shares a prefix earns 1 x. Expressed as a multiplier rather than a
	// second weight table so the 3:3:1 ratio between fields stays visible and an
	// exact hit always outranks a prefix hit in the same field.
	exactMatchFactor = 2
)

// minPrefixMatchLength is how much of a word two tokens must share before a
// prefix match counts. Three is too little: it would let "aws" match "awsome".
const minPrefixMatchLength = 4

// SearchAIInvocableWorkflows ranks the automations an AI caller may run against
// a free-text query — typically a symptom the user described rather than an
// automation name.
//
// Ranking happens here rather than in llm-server's generic capability search for
// two reasons. It is scoped to workflows, so a customer's automations do not
// compete for slots against every other tool in the catalog. And it reads the
// LIVE version's definition, which the workflow listing — returning the draft —
// cannot supply.
//
// An empty query is not an error: it returns the eligible automations
// unfiltered, which is the natural answer to "what can you run for me?".
func (s *Service) SearchAIInvocableWorkflows(ctx *security.RequestContext, accountID, query string, limit int) (model.AIWorkflowSearchResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeRead) &&
		!canInspectWorkflows(ctx.GetSecurityContext(), accountID) {
		return model.AIWorkflowSearchResponse{}, common.ErrorUnauthorized("account not accessible")
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()

	// Same gate as execution: a tenant that has not enrolled sees nothing, so the
	// AI cannot even learn which automations exist.
	enabled, err := featureEnabledForAccount(common.FeatureAIWorkflowTools, tenantID, accountID)
	if err != nil || !enabled {
		if err != nil {
			ctx.GetLogger().Warn("ai search: feature flag lookup failed, returning no automations",
				"account_id", accountID, "error", err)
		}
		return model.AIWorkflowSearchResponse{Workflows: []model.AIWorkflowCandidate{}}, nil
	}

	candidates, err := s.store.ListAIInvocableWorkflows(ctx.GetContext(), tenantID, accountID)
	if err != nil {
		return model.AIWorkflowSearchResponse{}, err
	}

	// Drop anything the run-time gate would refuse, so search never advertises an
	// automation that cannot actually be run.
	eligible := make([]model.AIInvocableWorkflow, 0, len(candidates))
	for _, c := range candidates {
		if c.Definition.HasManualTrigger() && strings.TrimSpace(c.Definition.LLMDescription) != "" {
			eligible = append(eligible, c)
		}
	}

	if limit <= 0 {
		limit = defaultAISearchLimit
	}
	if limit > maxAISearchLimit {
		limit = maxAISearchLimit
	}

	ranked := rankAIWorkflows(eligible, query, limit)

	out := make([]model.AIWorkflowCandidate, 0, len(ranked))
	for _, c := range ranked {
		out = append(out, model.AIWorkflowCandidate{
			ID:             c.ID,
			Name:           c.Name,
			Description:    c.Description,
			LLMDescription: c.Definition.LLMDescription,
			Inputs:         c.Definition.Inputs,
		})
	}

	return model.AIWorkflowSearchResponse{
		Workflows:      out,
		TotalInvocable: len(eligible),
	}, nil
}

// rankAIWorkflows orders candidates by how well they match query and returns the
// top `limit`. Candidates matching nothing are dropped — returning them anyway
// would invite the model to run an unrelated automation just because it was the
// only thing on offer.
func rankAIWorkflows(candidates []model.AIInvocableWorkflow, query string, limit int) []model.AIInvocableWorkflow {
	queryTokens := tokenize(query)

	// No query means "show me what's available" rather than "match nothing".
	if len(queryTokens) == 0 {
		if len(candidates) > limit {
			return candidates[:limit]
		}
		return candidates
	}

	type scored struct {
		candidate model.AIInvocableWorkflow
		score     int
		name      string
	}

	matches := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		if score := scoreAIWorkflow(c, queryTokens); score > 0 {
			matches = append(matches, scored{candidate: c, score: score, name: c.Name})
		}
	}

	// Score desc, then name asc so equal scores come back in a stable order
	// rather than whatever the database happened to return.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].name < matches[j].name
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]model.AIInvocableWorkflow, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.candidate)
	}
	return out
}

// scoreAIWorkflow counts weighted overlap between the query and a candidate's
// searchable text. Each query token scores at most once per field, so repeating
// a word in a description cannot inflate a match.
func scoreAIWorkflow(c model.AIInvocableWorkflow, queryTokens []string) int {
	nameTokens := tokenSet(c.Name)
	descTokens := tokenSet(c.Description + " " + c.Definition.LLMDescription)

	score := 0
	for _, token := range queryTokens {
		score += fieldScore(nameTokens, token, weightName)
		score += fieldScore(descTokens, token, weightDescription)
	}
	return score
}

// fieldScore is what one query token earns against one field: the full weight for
// an exact token match, a reduced one for a prefix match.
//
// The prefix pass is what stemming alone cannot do. stem normalises each word to
// one form, so two words only meet if the same rule happens to apply to both:
// "scaling" reduces to "scal" while "scale" stays whole, and an operator
// describing a symptom as "scaling" misses the automation named "scale
// deployment" entirely. Comparing forms instead of normalising to one covers
// that whole family — including "rabbit mq" against "rabbitmq" — without a real
// stemmer's dependency or its own surprises over infrastructure vocabulary.
//
// It is scored below an exact match because it is a weaker signal, so a prefix
// hit can lift an automation into the results but never above one that genuinely
// matched. The first prefix hit wins; since every prefix hit in a field is worth
// the same, iterating the set in map order stays deterministic.
func fieldScore(tokens map[string]bool, token string, weight int) int {
	if tokens[token] {
		return weight * exactMatchFactor
	}
	for candidate := range tokens {
		if sharesPrefix(token, candidate) {
			return weight
		}
	}
	return 0
}

// sharesPrefix reports whether the shorter of two tokens is a prefix of the
// longer, once it is long enough to be worth something. Deliberately
// bidirectional: a query can be the longer side ("ingresses" against the keyword
// "ingress") as easily as the shorter one ("scal" against "scale").
func sharesPrefix(a, b string) bool {
	shorter, longer := a, b
	if len(longer) < len(shorter) {
		shorter, longer = longer, shorter
	}
	if len(shorter) < minPrefixMatchLength || shorter == longer {
		return false
	}
	return strings.HasPrefix(longer, shorter)
}

// tokenize lowercases and splits on anything that is not a letter or digit, so
// "payment-service" and "payment service" tokenize alike. Single-character
// tokens are dropped as noise. Each token is reduced to a crude stem so that
// singular and plural forms match — see stem.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		f = stem(f)
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// stem strips a few common English suffixes so that the words people actually
// type match the words authors actually write: "pod" finds "pods", "restart"
// finds "restarting", "crashloops" finds "crashloop".
//
// Deliberately crude rather than a real stemmer. Exact-token matching was the
// feature's sharpest edge — a query one letter off returned nothing at all,
// because non-matches are dropped — and singular/plural is the overwhelming
// majority of that gap. A full stemmer would bring a dependency and its own
// surprises for the infrastructure vocabulary this searches over.
//
// The same function runs over queries and over indexed text, so both sides
// collapse to the same form and the rule never has to be applied twice. Where
// they collapse to *different* forms — "scaling" to "scal" but "scale" to
// itself — fieldScore's prefix pass is what still brings them together.
func stem(token string) string {
	// The stem must keep at least this many characters, so "pods" -> "pod" but
	// "aws" is left intact rather than becoming "aw".
	const minStemLength = 3
	// Only "ing" and "s". Stripping "es" would break the -e words it looks like
	// it helps: "queues" would become "queu" while "queue" stayed whole, so the
	// two would stop matching — worse than not stemming at all.
	for _, suffix := range []string{"ing", "s"} {
		if len(token) >= minStemLength+len(suffix) && strings.HasSuffix(token, suffix) {
			token = strings.TrimSuffix(token, suffix)
			break
		}
	}
	// Collapse a trailing doubled consonant. English doubles the final consonant
	// before "ing" on short verbs, so "running" strips to "runn" and "getting" to
	// "gett" — three-letter bases the prefix pass cannot rescue either, since it
	// needs four shared characters. Both were outright misses over exactly the
	// vocabulary this searches ("pods not running").
	//
	// Applied to every token rather than only the stripped ones, which is what
	// makes it safe: "kill" and "killing" both become "kil", so words whose base
	// genuinely ends in a doubled consonant still meet each other. The one oddity
	// is that "off" collapses onto "of"; a bare preposition is noise as a search
	// token anyway.
	// Restricted to ASCII letters because this indexes and slices bytes, not
	// runes. A multi-byte character can end in two identical continuation bytes —
	// "充" is E5 85 85 — and lopping one off leaves invalid UTF-8 in a token that
	// then flows into a JSON response. The rule is about English verb forms
	// anyway, so nothing outside a-z should reach the slice.
	if n := len(token); n >= 3 && token[n-1] == token[n-2] &&
		token[n-1] >= 'a' && token[n-1] <= 'z' && !strings.ContainsRune("aeiou", rune(token[n-1])) {
		token = token[:n-1]
	}
	return token
}

func tokenSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, t := range tokenize(s) {
		set[t] = true
	}
	return set
}
