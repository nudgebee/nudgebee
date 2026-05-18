package common

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Hoisted from ExtractJSONFromLLMResponse — called per LLM response, so compiling once saves ~3-15µs + allocs per call.
var llmJSONPatterns = []*regexp.Regexp{
	regexp.MustCompile("```json\\s*\\n?({.*?})\\s*\\n?```"),
	regexp.MustCompile("```\\s*\\n?({.*?})\\s*\\n?```"),
	regexp.MustCompile("(?s)({.*})"),
}

// ExtractJSONFromLLMResponse attempts to extract valid JSON from LLM response
// Handles responses that might contain explanatory text before/after JSON
func ExtractJSONFromLLMResponse(response string, logger *Logger) (map[string]any, error) {
	// First try direct parsing
	var result map[string]any
	if err := json.Unmarshal([]byte(response), &result); err == nil {
		return result, nil
	}

	for _, re := range llmJSONPatterns {
		matches := re.FindStringSubmatch(response)
		if len(matches) > 1 {
			if err := json.Unmarshal([]byte(matches[1]), &result); err == nil {
				if logger != nil {
					logger.Log(EventStepComplete, "Successfully extracted JSON using regex pattern", map[string]any{"pattern": re.String()})
				}
				return result, nil
			}
		}
	}

	// Try to find JSON by looking for the first { and last }
	firstBrace := strings.Index(response, "{")
	lastBrace := strings.LastIndex(response, "}")
	if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
		jsonCandidate := response[firstBrace : lastBrace+1]
		if err := json.Unmarshal([]byte(jsonCandidate), &result); err == nil {
			if logger != nil {
				logger.Log(EventStepComplete, "Successfully extracted JSON by brace matching", nil)
			}
			return result, nil
		}
	}

	return nil, fmt.Errorf("could not extract valid JSON from response: %s", response[:min(200, len(response))])
}
