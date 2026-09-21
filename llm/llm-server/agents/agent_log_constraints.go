package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/common"
	"regexp"
	"strconv"
	"strings"
)

var (
	// tailFlagRegex matches standalone --tail 25, --tail=25, -n 25, -n=25, --limit 25, --limit=25
	tailFlagRegex = regexp.MustCompile(`(?i)(?:^|\s)(?:--tail|-n|--limit)[=\s]+(\d+)\b`)
	// wordLimitRegex matches "limit 25", "limit: 25", "tail: 25", "tail 25"
	wordLimitRegex = regexp.MustCompile(`(?i)\b(?:limit|tail)[:\s=]+(\d+)\b`)
	// linesLimitRegex matches "last 25 lines", "first 25 lines"
	linesLimitRegex = regexp.MustCompile(`(?i)\b(?:last|first)\s+(\d+)\s+lines?\b`)

	// sinceFlagRegex matches standalone --since 15m, --since=15m, --last 15m, --last=15m
	sinceFlagRegex = regexp.MustCompile(`(?i)(?:^|\s)(?:--since|--last)[=\s]+([0-9]+[smhd])\b`)
	// wordTimeWindowRegex matches "last 15m", "past 15m", "last 15 min", "last 15 minutes", "last 1 hour", etc.
	wordTimeWindowRegex = regexp.MustCompile(`(?i)\b(?:last|past)\s+([0-9]+)\s*(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days)\b`)
	// compactWindowRegex matches "last 15m", "past 24h"
	compactWindowRegex = regexp.MustCompile(`(?i)\b(?:last|past)\s+([0-9]+[smhd])\b`)
)

func normalizeTimeUnit(numStr, unitStr string) string {
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return ""
	}
	unit := strings.ToLower(unitStr)
	switch {
	case strings.HasPrefix(unit, "s"):
		return fmt.Sprintf("%ds", n)
	case strings.HasPrefix(unit, "m"):
		return fmt.Sprintf("%dm", n)
	case strings.HasPrefix(unit, "h"):
		return fmt.Sprintf("%dh", n)
	case strings.HasPrefix(unit, "d"):
		return fmt.Sprintf("%dd", n)
	default:
		return ""
	}
}

// parseExplicitLogQueryConstraints inspects a query string (natural language or
// canonical JSON) and extracts explicit limit and time window constraints.
// Returns hasLimit/hasRange true only when the caller explicitly stated them.
func parseExplicitLogQueryConstraints(query string) (limit int, timeRange string, hasLimit bool, hasRange bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return 0, "", false, false
	}

	// 1. Check if the query is already JSON (canonical query)
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			if l, ok := parsed["limit"]; ok {
				switch v := l.(type) {
				case float64:
					if v > 0 {
						limit = int(v)
						hasLimit = true
					}
				case int:
					if v > 0 {
						limit = v
						hasLimit = true
					}
				case string:
					if iv, err := strconv.Atoi(v); err == nil && iv > 0 {
						limit = iv
						hasLimit = true
					}
				}
			}
			if tr, ok := parsed["time_range"].(string); ok && strings.TrimSpace(tr) != "" {
				timeRange = strings.TrimSpace(tr)
				hasRange = true
			} else if r, ok := parsed["range"].(string); ok && strings.TrimSpace(r) != "" {
				timeRange = strings.TrimSpace(r)
				hasRange = true
			}
			if hasLimit || hasRange {
				return limit, timeRange, hasLimit, hasRange
			}
		}
	}

	// 2. Extract explicit limits from text
	if m := tailFlagRegex.FindStringSubmatch(query); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
			limit = v
			hasLimit = true
		}
	} else if m := linesLimitRegex.FindStringSubmatch(query); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
			limit = v
			hasLimit = true
		}
	} else if m := wordLimitRegex.FindStringSubmatch(query); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
			limit = v
			hasLimit = true
		}
	}

	// 3. Extract explicit time range from text
	if m := sinceFlagRegex.FindStringSubmatch(query); len(m) > 1 {
		timeRange = strings.ToLower(m[1])
		hasRange = true
	} else if m := wordTimeWindowRegex.FindStringSubmatch(query); len(m) > 2 {
		if norm := normalizeTimeUnit(m[1], m[2]); norm != "" {
			timeRange = norm
			hasRange = true
		}
	} else if m := compactWindowRegex.FindStringSubmatch(query); len(m) > 1 {
		timeRange = strings.ToLower(m[1])
		hasRange = true
	}

	return limit, timeRange, hasLimit, hasRange
}

// enforceCanonicalLogConstraints ensures that explicit limit and time window
// constraints from the request query override any widened defaults emitted by
// the translation LLM.
func enforceCanonicalLogConstraints(jsonQuery string, requestQuery string) string {
	limit, timeRange, hasLimit, hasRange := parseExplicitLogQueryConstraints(requestQuery)
	if !hasLimit && !hasRange {
		return jsonQuery
	}

	var parsed map[string]any
	if err := common.ExtractAndUnmarshalJSON([]byte(jsonQuery), &parsed); err != nil || parsed == nil {
		return jsonQuery
	}

	if hasLimit {
		parsed["limit"] = limit
	}
	if hasRange {
		parsed["time_range"] = timeRange
		delete(parsed, "range")
	}

	out, err := json.Marshal(parsed)
	if err != nil {
		return jsonQuery
	}
	return string(out)
}
