package scan_orchestrator

import "strings"

// camelKeysDeep undoes the agent kube handler's snake_keys transform on a
// K8s object so the value stored in `recommendation.recommendation` keeps
// the original camelCase field names the UI expects (claimRef,
// creationTimestamp, …). Strings, numbers, bools, and nil are passthrough;
// maps recurse with their keys converted; slices recurse element-wise.
//
// Conversion rule: split the key on "_", lowercase the first segment, title
// the rest, join. Keys without "_" stay as-is.
func camelKeysDeep(in any) any {
	switch v := in.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[snakeToCamel(k)] = camelKeysDeep(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = camelKeysDeep(val)
		}
		return out
	default:
		return v
	}
}

func snakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	parts := strings.Split(s, "_")
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(strings.ToLower(parts[0]))
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}
