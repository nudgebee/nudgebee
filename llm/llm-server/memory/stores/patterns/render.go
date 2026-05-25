package patterns

import (
	"fmt"
	"strings"
)

// Render produces the <user_patterns> prompt block for a pattern list.
// Patterns are grouped by kind for readability; empty input returns "".
func Render(pats []Pattern) string {
	if len(pats) == 0 {
		return ""
	}

	byKind := map[string][]Pattern{}
	order := []string{}
	for _, p := range pats {
		if _, seen := byKind[p.Kind]; !seen {
			order = append(order, p.Kind)
		}
		byKind[p.Kind] = append(byKind[p.Kind], p)
	}

	var b strings.Builder
	b.WriteString("<user_patterns>\n")
	for _, kind := range order {
		label := labelForKind(kind)
		subjects := make([]string, 0, len(byKind[kind]))
		for _, p := range byKind[kind] {
			subjects = append(subjects, p.Subject)
		}
		fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(subjects, ", "))
	}
	b.WriteString("</user_patterns>")
	return b.String()
}

func labelForKind(kind string) string {
	switch kind {
	case KindFrequentService:
		return "frequent_services"
	case KindFrequentNamespace:
		return "frequent_namespaces"
	case KindPreferredDiagnosticFlow:
		return "preferred_flows"
	case KindAcceptedRecommendation:
		return "accepted_recs"
	case KindDismissedRecommendation:
		return "dismissed_recs"
	case KindFrequentResourceType:
		return "frequent_resources"
	default:
		return kind
	}
}
