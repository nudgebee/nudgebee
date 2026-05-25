package preferences

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Render produces the <user_preferences> prompt block for a list of preferences.
// Returns empty string if the list is empty (cold-start graceful degradation).
// Caller is responsible for having already filtered by module.
func Render(prefs []Preference) string {
	if len(prefs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<user_preferences>\n")
	for _, p := range prefs {
		fmt.Fprintf(&b, "%s: %s\n", p.Key, displayValue(p.Value))
	}
	b.WriteString("</user_preferences>")
	return b.String()
}

// displayValue formats a typed value for inclusion in the prompt.
// Prefers short, flat representations; complex objects get JSON-stringified.
func displayValue(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%v", x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, displayValue(item))
		}
		return strings.Join(parts, ", ")
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
