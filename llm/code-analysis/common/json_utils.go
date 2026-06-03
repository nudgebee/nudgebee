package common

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ExtractJSONFromLLMResponse attempts to extract valid JSON from LLM response
// Handles responses that might contain explanatory text before/after JSON
func ExtractJSONFromLLMResponse(response string, logger *Logger) (map[string]any, error) {
	// First try direct parsing
	var result map[string]any
	if err := json.Unmarshal([]byte(response), &result); err == nil {
		return result, nil
	}

	// Try to find JSON within the response using regex
	patterns := []string{
		// JSON object that might be wrapped in backticks or code blocks
		"```json\\s*\\n?({.*?})\\s*\\n?```",
		"```\\s*\\n?({.*?})\\s*\\n?```",
		// JSON object that starts and ends with braces, allowing for multiline
		"(?s)({.*})",
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(response)
		if len(matches) > 1 {
			if err := json.Unmarshal([]byte(matches[1]), &result); err == nil {
				if logger != nil {
					logger.Log(EventStepComplete, "Successfully extracted JSON using regex pattern", map[string]any{"pattern": pattern})
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
