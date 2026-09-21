package common

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"
)

// List of common English stop words
var StopWords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true, "against": true, "all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "arent": true, "as": true, "at": true, "be": true, "because": true, "been": true, "before": true, "being": true, "below": true, "between": true, "both": true, "but": true, "by": true, "cant": true, "cannot": true, "could": true, "couldnt": true, "did": true, "didnt": true, "do": true, "does": true, "doesnt": true, "doing": true, "dont": true, "down": true, "during": true, "each": true, "few": true, "for": true, "from": true, "further": true, "had": true, "hadnt": true, "has": true, "hasnt": true, "have": true, "havent": true, "having": true, "he": true, "hed": true, "hell": true, "hes": true, "her": true, "here": true, "here's": true, "hers": true, "herself": true, "him": true, "himself": true, "his": true, "how": true, "hows": true, "i": true, "ill": true, "im": true, "ive": true, "if": true, "in": true, "into": true, "is": true, "isnt": true, "it": true, "its": true, "itself": true, "lets": true, "me": true, "more": true, "most": true, "mustnt": true, "my": true, "myself": true, "no": true, "nor": true, "not": true, "of": true, "off": true, "on": true, "once": true, "only": true, "or": true, "other": true, "ought": true, "our": true, "ours": true, "ourselves": true, "out": true, "over": true, "own": true, "same": true, "shant": true, "she": true, "shed": true, "shes": true, "should": true, "shouldnt": true, "so": true, "some": true, "such": true, "than": true, "that": true, "thats": true, "the": true, "their": true, "theirs": true, "them": true, "themselves": true, "then": true, "there": true, "theres": true, "these": true, "they": true, "theyd": true, "theyll": true, "theyre": true, "theyve": true, "this": true, "those": true, "through": true, "to": true, "too": true, "under": true, "until": true, "up": true, "very": true, "was": true, "wasnt": true, "we": true, "wed": true, "were": true, "weve": true, "werent": true, "what": true, "whats": true, "when": true, "whens": true, "where": true, "wheres": true, "which": true, "while": true, "who": true, "whos": true, "whom": true, "why": true, "whys": true, "with": true, "wont": true, "would": true, "wouldnt": true, "you": true, "youd": true, "youll": true, "youre": true, "youve": true, "your": true, "yours": true, "yourself": true, "yourselves": true, "must": true, "can": true, "please": true, "kindly": true, "shall": true, "may": true,
}

// Punctuation that serves as word separators (replaced with space)
// We keep hyphens (-) and dots (.) inside words (e.g. "my-pod", "v1.2.3")
// but treat others like commas, colons, brackets as separators.
// The regex matches anything that is NOT:
// - a word character (\w includes alphanumeric + underscore)
// - whitespace (\s)
// - hyphen (-)
// - dot (.)
var separatorPunctuationRegex = regexp.MustCompile(`[^\w\s\-\.]`)

// Punctuation to trim from the start/end of words (e.g., "end." -> "end")
// This handles sentence endings or trailing punctuation.
var trimPunctuationRegex = regexp.MustCompile(`^[\-\.]+|[\-\.]+$`)

// ShortQueryWordCountThreshold defines the threshold for considering a query "short"
// and skipping LLM-based processing (e.g., using random acknowledgment or query as title).
const ShortQueryWordCountThreshold = 10

// GetWordCount counts the number of words in a text after removing punctuation and stop words.
func GetWordCount(text string) int {
	// Normalize text: lowercase
	text = strings.ToLower(text)

	// Remove apostrophes first to handle contractions (e.g. "don't" -> "dont")
	// and possessives (e.g. "users'" -> "users").
	text = strings.ReplaceAll(text, "'", "")

	// Replace separator punctuation with space (e.g. "hello,world" -> "hello world")
	// This preserves intra-word hyphens and dots.
	text = separatorPunctuationRegex.ReplaceAllString(text, " ")

	// Split into words
	words := strings.Fields(text)

	count := 0
	for _, word := range words {
		// Trim leading/trailing hyphens/dots (e.g. "end." -> "end", "-flag" -> "flag")
		// This ensures sentence endings don't create unique "words" and flags are counted cleanly.
		word = trimPunctuationRegex.ReplaceAllString(word, "")

		if word != "" && !StopWords[word] {
			count++
		}
	}
	return count
}

// HashString returns a hex-encoded SHA256 hash of the input string.
func HashString(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// agentMentionRegex matches the leading run of "@name" mentions; group 1 = FIRST name.
// \w+ matches the validated name grammar (^[a-zA-Z]\w*$); "-"/"/" excluded so flags/paths survive.
var agentMentionRegex = regexp.MustCompile(`^@(\w+)(?:[\s,;:.!?]*@\w+)*[\s,;:.!?]*`)

// agentSingleMentionRegex matches only the FIRST "@name" + trailing separators (not the run).
var agentSingleMentionRegex = regexp.MustCompile(`^@\w+[\s,;:.!?]*`)

// ParseAgentMention splits a leading run of "@agent" mentions off a query -> (firstName, rest).
// First mention wins, extras swallowed; ("", trimmed) when none. E.g. "@a @b x" -> ("a", "x").
func ParseAgentMention(query string) (agent, rest string) {
	query = strings.TrimSpace(query)
	m := agentMentionRegex.FindStringSubmatch(query)
	if m == nil {
		return "", query
	}
	return m[1], strings.TrimSpace(query[len(m[0]):])
}

// StripLeadingAgentMention drops the leading "@<name>" mention(s) from a query
// (e.g. "@aws_debug check pods" -> "check pods"). Used for titles + executor input.
func StripLeadingAgentMention(query string) string {
	_, rest := ParseAgentMention(query)
	return rest
}

// StripFirstAgentMention drops only the FIRST leading "@<name>" mention, keeping any
// further mentions: "@a @b q" -> "@b q", "@a@b q" -> "@b q". Used when the extra
// mentions should reach the agent (DropExtraAgentMentions=false).
func StripFirstAgentMention(query string) string {
	query = strings.TrimSpace(query)
	if loc := agentSingleMentionRegex.FindStringIndex(query); loc != nil {
		return strings.TrimSpace(query[loc[1]:])
	}
	return query
}

var titleFieldKeys = []string{"title", "subject", "summary", "name", "alertname", "description", "message", "text"}

// maxDerivedTitleRunes caps a JSON-derived title. Aligned with the short-query
// title path's 100-char cap so both title routes bound length the same way.
const maxDerivedTitleRunes = 100

// leadingMarkdownHeadingRegex matches a leading markdown heading marker ("#" ..
// "######") plus its trailing space, so "# grafana Alert" renders as "grafana Alert".
var leadingMarkdownHeadingRegex = regexp.MustCompile(`^\s*#{1,6}\s+`)

// whitespaceRunRegex collapses any run of whitespace (incl. newlines/tabs) to one space.
var whitespaceRunRegex = regexp.MustCompile(`\s+`)

func DeriveTitleFromJSONQuery(query string) (string, bool) {
	trimmed := strings.TrimSpace(query)
	// Only object payloads — arrays / scalars / plain text are left to the caller.
	if !strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	var obj map[string]any
	if err := UnmarshalJson([]byte(trimmed), &obj); err != nil {
		return "", false
	}
	// Case-insensitive key resolution, preferring titleFieldKeys order.
	byLowerKey := make(map[string]string, len(obj))
	for k := range obj {
		byLowerKey[strings.ToLower(k)] = k
	}
	for _, want := range titleFieldKeys {
		actual, ok := byLowerKey[want]
		if !ok {
			continue
		}
		val, ok := obj[actual].(string)
		if !ok {
			continue
		}
		if cleaned := cleanTitleLine(val); cleaned != "" {
			return cleaned, true
		}
	}
	return "", false
}

// cleanTitleLine reduces a (possibly multi-line, markdown) field value to one
// title line, capped at maxDerivedTitleRunes runes. Returns "" when the value has
// no non-empty line.
func cleanTitleLine(s string) string {
	for rest := s; rest != ""; {
		line := rest
		if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
			line, rest = rest[:idx], rest[idx+1:]
		} else {
			rest = ""
		}
		line = leadingMarkdownHeadingRegex.ReplaceAllString(line, "")
		line = whitespaceRunRegex.ReplaceAllString(line, " ")
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateAtWord(line, maxDerivedTitleRunes)
		}
	}
	return ""
}

// truncateAtWord shortens s to at most maxRunes runes, preferring to cut on the
// last word boundary, and appends a single-character ellipsis when it truncates.
func truncateAtWord(s string, maxRunes int) string {
	if len(s) <= maxRunes {
		return s
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	cut := string([]rune(s)[:maxRunes])
	if idx := strings.LastIndexByte(cut, ' '); idx > 0 {
		cut = cut[:idx]
	}
	return strings.TrimRight(cut, " ") + "…"
}

// canonicalModelRegionPrefixes and canonicalModelVendorPrefixes are stripped, in
// order, by CanonicalModelID. Each list is checked with HasPrefix and stops at
// the first match.
var canonicalModelRegionPrefixes = []string{"us.", "eu.", "apac.", "jp.", "au.", "ca.", "global."}
var canonicalModelVendorPrefixes = []string{"anthropic.", "amazon.", "meta.", "google.", "vertex.", "vertex/", "openai.", "azure.", "mistral.", "cohere.", "ai21.", "models/"}

var trailingInvocationVersionRE = regexp.MustCompile(`-v\d+$`)
var versionSeparatorRE = regexp.MustCompile(`(\d)-(\d)`)

// CanonicalModelID reduces a provider-qualified, deployment-specific model ID
// to a bare, comparable form.
func CanonicalModelID(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, ':'); i >= 0 {
		m = m[:i]
	}
	for _, p := range canonicalModelRegionPrefixes {
		if strings.HasPrefix(m, p) {
			m = strings.TrimPrefix(m, p)
			break
		}
	}
	for _, p := range canonicalModelVendorPrefixes {
		if strings.HasPrefix(m, p) {
			m = strings.TrimPrefix(m, p)
			break
		}
	}
	m = trailingInvocationVersionRE.ReplaceAllString(m, "")
	m = versionSeparatorRE.ReplaceAllString(m, "$1.$2")
	return m
}

// TruncateHead truncates s to at most maxBytes from the start, ensuring the cut
// does not split a multi-byte UTF-8 character.
func TruncateHead(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes to find a valid rune boundary
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
