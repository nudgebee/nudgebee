package core

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// NormalizeKnowledgeTags preserves nil (omitted) versus an explicit empty list.
func NormalizeKnowledgeTags(tags []string) ([]string, error) {
	if tags == nil {
		return nil, nil
	}
	if len(tags) > 32 {
		return nil, fmt.Errorf("knowledge tags: at most 32 tags are allowed")
	}
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > 128 {
			return nil, fmt.Errorf("knowledge tags: each tag must be valid text of at most 128 characters")
		}
		if strings.ContainsFunc(tag, unicode.IsControl) {
			return nil, fmt.Errorf("knowledge tags: control characters are not allowed")
		}
		key := strings.ToLower(tag)
		if !seen[key] {
			out = append(out, tag)
			seen[key] = true
		}
	}
	return out, nil
}
