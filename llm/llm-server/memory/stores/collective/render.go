package collective

import (
	"fmt"
	"strings"
)

// Render produces the <tenant_knowledge> block for a list of collective entries.
func Render(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<tenant_knowledge>\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", e.EntryKind, e.Subject, firstLine(e.Body))
	}
	b.WriteString("</tenant_knowledge>")
	return b.String()
}

// firstLine trims a body to its first non-empty line for compact rendering.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "\n"); idx > 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
