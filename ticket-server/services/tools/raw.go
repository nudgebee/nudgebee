package tools

import (
	"encoding/json"
	"log/slog"
)

// marshalToMap renders a typed SDK value (Jira/GitHub/GitLab/PagerDuty/ZenDuty issue
// struct) into a generic map[string]any by round-tripping through JSON. Each SDK
// already controls which fields land in the marshalled output via its own JSON
// tags, so this preserves exactly what the connector fetched without re-asking
// the upstream API.
//
// Used by per-platform Get methods to populate Ticket.Raw for workflow consumers
// that need fields beyond the normalized Ticket struct (assignees, custom Jira
// fields, GitHub labels & PR links, GitLab milestone, PD service / urgency, etc.).
//
// Returns nil when v is nil or marshalling fails. Failure is logged at debug —
// callers should treat Raw as optional and never block on its absence.
func marshalToMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		slog.Debug("marshalToMap: marshal failed", "error", err)
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		slog.Debug("marshalToMap: unmarshal failed", "error", err)
		return nil
	}
	return out
}

// rawWithUnmodeled is the full upstream response with the typed value marshalled
// over it. Every key the typed value already carried keeps exactly the value it
// had, and the keys the typed struct does not model (custom fields among them)
// are added. Falls back to the typed value alone when full is nil.
func rawWithUnmodeled(typed any, full map[string]any) map[string]any {
	if len(full) == 0 {
		return marshalToMap(typed)
	}
	out := make(map[string]any, len(full))
	for k, v := range full {
		out[k] = v
	}
	for k, v := range marshalToMap(typed) {
		out[k] = v
	}
	return out
}
