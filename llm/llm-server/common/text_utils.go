package common

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
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

// StripLeadingAgentMention removes a leading "@<token>" mention from a user
// query (e.g. "@aws_debug check pods" -> "check pods"). Used wherever the
// raw user query is consumed (title generation, executor input) so the agent
// selector doesn't leak into user-visible titles, LLM prompts, or downstream
// agent reasoning. Also trims surrounding whitespace from the result.
func StripLeadingAgentMention(query string) string {
	trimmed := strings.TrimSpace(query)
	if !strings.HasPrefix(trimmed, "@") {
		return trimmed
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, strings.Fields(trimmed)[0]))
}
