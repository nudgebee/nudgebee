package common

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// jsonFencedRe matches a markdown code fence, optionally tagged `json`, and
// captures its inner payload. Non-greedy so each fence is captured separately.
// The `json` tag is optional because LLMs frequently omit it; the trade-off is
// that this also matches non-JSON fences (```bash, ```xml), so callers must try
// every fence and keep the first whose payload parses as JSON.
var jsonFencedRe = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")

// ExtractJSONFromLLMResponse pulls a valid JSON value (object or array) out of a
// raw LLM response that may wrap the JSON in prose, a markdown code fence, or
// both. Strategies are tried in order of confidence:
//
//  1. Parse the whole (trimmed) text as JSON.
//  2. Parse the contents of a ```json ... ``` (or bare ``` ... ```) fence.
//  3. Delimiter fallback: parse the span from the first opening delimiter to the
//     last matching closing delimiter, for both objects ({}) and arrays ([]).
//
// It returns a generic error only when every strategy fails; callers that want
// to see the unparseable payload should log the raw text themselves (the helper
// stays free of logging and response-length detail on purpose).
//
// The delimiter fallback is intentionally greedy (first-open to last-close); a
// stray brace in surrounding prose can widen the span, but json.Unmarshal
// validates the result, so an over-wide span that isn't valid JSON simply falls
// through to the error rather than returning garbage.
func ExtractJSONFromLLMResponse(text string) (any, error) {
	trimmed := strings.TrimSpace(text)

	var direct any
	if err := json.Unmarshal([]byte(trimmed), &direct); err == nil {
		return direct, nil
	}

	// Try every fence, not just the first: a response may carry an untagged
	// ```bash / ```xml fence ahead of the JSON one, and the optional `json` tag
	// means the regex matches those too. Keep the first fence that parses.
	for _, m := range jsonFencedRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &parsed); err == nil {
			return parsed, nil
		}
	}

	if parsed, ok := extractJSONByDelimiters(text); ok {
		return parsed, nil
	}

	return nil, errors.New("could not extract valid JSON from response")
}

// extractJSONByDelimiters tries the widest first-open to last-close span for
// both object and array delimiters, returning the first span that parses as
// valid JSON. Objects are tried before arrays because object-shaped responses
// are by far the common case for response_format=json.
func extractJSONByDelimiters(text string) (any, bool) {
	for _, d := range []struct{ open, close byte }{{'{', '}'}, {'[', ']'}} {
		start := strings.IndexByte(text, d.open)
		end := strings.LastIndexByte(text, d.close)
		if start == -1 || end == -1 || end <= start {
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(text[start:end+1]), &parsed); err == nil {
			return parsed, true
		}
	}
	return nil, false
}
