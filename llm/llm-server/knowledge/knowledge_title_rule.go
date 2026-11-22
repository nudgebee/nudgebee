package knowledge

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"nudgebee/llm/config"
)

// integrationCollectionSuffix is shared by three writers: Confluence and ServiceNow knowledge
// bases, the tenant's own uploaded knowledge base, and legacy per-account collections renamed
// from "<account>_docs". Only the first holds scraped pages, so the suffix alone is not enough
// (see isIntegrationPage).
const integrationCollectionSuffix = "_knowledge_base"

// titleRule is the compiled form of the runbook words and exclusions in config.
type titleRule struct {
	words      *regexp.Regexp
	exclusions *regexp.Regexp
}

// titleRuleCache keeps one compiled rule per distinct configuration, because purpose is
// decided for every search hit.
var titleRuleCache sync.Map

func currentTitleRule() *titleRule {
	words, exclusions := config.Config.KBTitleSOPWords, config.Config.KBTitleSOPExclusions
	key := words + "\x00" + exclusions
	if cached, ok := titleRuleCache.Load(key); ok {
		return cached.(*titleRule)
	}
	rule := buildTitleRule(words, exclusions)
	titleRuleCache.Store(key, rule)
	return rule
}

func splitList(list string) []string {
	var out []string
	for _, item := range strings.Split(list, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// phraseSeparator stands for any run of spaces, underscores or dashes (hyphen, en dash, em
// dash, non-breaking hyphen) between the words of a configured phrase.
const phraseSeparator = `[\s_\p{Pd}]+`

// phraseAlternation joins configured phrases into one case-insensitive alternation in which
// the words of a phrase may be separated by any phraseSeparator, so "post-mortem" matches
// "Post mortem" and "Post‑mortem".
func phraseAlternation(list string) string {
	var alternatives []string
	for _, phrase := range splitList(list) {
		parts := strings.FieldsFunc(phrase, func(r rune) bool { return unicode.IsSpace(r) || r == '_' || unicode.Is(unicode.Pd, r) })
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		if len(parts) > 0 {
			alternatives = append(alternatives, strings.Join(parts, phraseSeparator))
		}
	}
	return strings.Join(alternatives, "|")
}

// wholeWord matches the alternation only where letters and digits do not touch it, so
// "SOP-02" and "Warning Alarm Runbook" match while "Runbooks" and "Sopranos" do not.
func wholeWord(alternation string) *regexp.Regexp {
	if alternation == "" {
		return nil
	}
	return regexp.MustCompile(`(?i)(?:^|[^\pL\pN])(?:` + alternation + `)(?:[^\pL\pN]|$)`)
}

func buildTitleRule(words, exclusions string) *titleRule {
	// A plural is usually a list of runbooks, not a runbook, so it is not a match. Exclusions
	// use the same boundaries: "Reindex Runbook" is not excluded by "index".
	return &titleRule{words: wholeWord(phraseAlternation(words)), exclusions: wholeWord(phraseAlternation(exclusions))}
}

// TitleNamesRunbook reports whether a page title names the page a runbook, by the configured
// words and exclusions.
func TitleNamesRunbook(title string) bool {
	rule := currentTitleRule()
	if rule.words == nil {
		return false
	}
	if rule.exclusions != nil && rule.exclusions.MatchString(title) {
		return false
	}
	return rule.words.MatchString(title)
}

// hitPageTitle is the page title of a search hit. Confluence stamps it on every chunk. A
// ServiceNow article carries it as the first line of its text ("Title: ..."), so a hit that
// is not the first chunk has none until rag-server has filled the title into its metadata.
func hitPageTitle(doc RAGSearchResult) string {
	if title, _ := doc.Metadata["title"].(string); strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	first := doc.Document
	if end := strings.IndexByte(first, '\n'); end >= 0 {
		first = first[:end]
	}
	if rest, ok := strings.CutPrefix(strings.TrimSpace(first), "Title:"); ok {
		return strings.TrimSpace(rest)
	}
	return ""
}

// isIntegrationPage reports whether a hit is a scraped Confluence or ServiceNow page. The
// scrapers stamp their name as the chunk's source; points indexed before provenance was
// stamped have none, and are recognised by the scraper's own page id instead. The tenant's
// uploaded knowledge base (source "user_kb") and legacy account collections are neither.
func isIntegrationPage(doc RAGSearchResult) bool {
	collection, _ := doc.Metadata["collection"].(string)
	if !strings.HasSuffix(collection, integrationCollectionSuffix) {
		return false
	}
	source, _ := doc.Metadata["source"].(string)
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "confluence", "servicenow":
		return true
	case "":
		return documentSourceID(doc.Metadata) != ""
	}
	return false
}

// titleMarksProcedure reports whether the title rule alone promotes this hit to a procedure.
func titleMarksProcedure(doc RAGSearchResult) bool {
	if !config.Config.KBTitleSOPRuleEnabled || !isIntegrationPage(doc) {
		return false
	}
	return TitleNamesRunbook(hitPageTitle(doc))
}

// titleRuleLogLimit bounds how many distinct pages are logged, so a busy server logs each
// page the rule promotes once and a pathological one cannot grow without limit.
const titleRuleLogLimit = 2000

var (
	titleRuleLogged  sync.Map
	titleRuleLoggedN atomic.Int64
)

// firstTitleRulePromotion reports true the first time a page is seen, up to the log limit.
func firstTitleRulePromotion(doc RAGSearchResult) bool {
	key := hitPageTitle(doc)
	if collection, _ := doc.Metadata["collection"].(string); collection != "" {
		key = collection + "|" + key
	}
	if _, seen := titleRuleLogged.Load(key); seen || titleRuleLoggedN.Load() >= titleRuleLogLimit {
		return false
	}
	if _, loaded := titleRuleLogged.LoadOrStore(key, struct{}{}); loaded {
		return false
	}
	titleRuleLoggedN.Add(1)
	return true
}
