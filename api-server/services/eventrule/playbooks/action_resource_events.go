package playbooks

import (
	"errors"
	"fmt"
	"strings"
)

// resource_events_enricher fetches K8s Events for the subject resource and
// renders them as a kubectl-style table (LastSeen / Type / Reason / Object /
// Message).
//
// Fires for any pod / workload / node subject the trigger engine produced.
// Skipped when the agent already provided this block.
type resourceEventsAction struct{}

var resourceEventsAggKeys = map[string]bool{
	"pod_oom_killer_enricher":     true,
	"report_crash_loop":           true,
	"image_pull_backoff_reporter": true,
	"node_not_ready":              true,
}

func (a *resourceEventsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if !resourceEventsAggKeys[ctx.GetEvent().AggregationKey] {
		// Also fire for any pod/node-subject event from a trigger.
		st := strings.ToLower(ctx.GetEvent().SubjectType)
		if st != "pod" && st != "node" && st != "deployment" && st != "statefulset" && st != "daemonset" && st != "job" {
			return false
		}
	}
	return ctx.GetEvent().SubjectName != ""
}

func (a *resourceEventsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	subjectKind := strings.ToLower(ctx.GetEvent().SubjectType)
	if subjectKind == "" {
		subjectKind = "pod"
	}
	return a.Execute(ctx, map[string]any{
		"name":      ctx.GetEvent().SubjectName,
		"namespace": ctx.GetEvent().SubjectNamespace,
		"kind":      subjectKind,
	})
}

func (a *resourceEventsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	name, _ := rawParams["name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	kind, _ := rawParams["kind"].(string)
	if name == "" {
		return nil, errors.New("resource_events_enricher: name required")
	}
	if kind == "" {
		kind = "Pod"
	}
	kind = titleASCII(kind)

	// The agent's get_resource doesn't honor field_selector (kube/Handlers
	// only accepts resource_type/group/version/namespace/name). Pull the
	// namespace's events and filter client-side on involvedObject.name +
	// involvedObject.namespace. Namespace scope keeps the round-trip bounded
	// even for clusters with thousands of cluster-wide events.
	params := map[string]any{
		"resource_type":  "events",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": namespace == "",
		"name":           []string{},
	}
	data, additionalInfo, err := getResourceViaRelay(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("resource_events_enricher: %w", err)
	}

	rows, headers := eventListToTable(filterEventsByInvolvedObject(data, kind, name, namespace))

	if additionalInfo == nil {
		additionalInfo = map[string]any{}
	}
	additionalInfo["title"] = fmt.Sprintf("Recent %s Events", kind)
	additionalInfo["action_name"] = "resource_events_enricher"
	additionalInfo["actual_action_name"] = "resource_events_enricher"
	additionalInfo["subject_name"] = name
	additionalInfo["subject_namespace"] = namespace

	return PlaybookActionResponseTable{
		Rows:           rows,
		Headers:        headers,
		AdditionalInfo: additionalInfo,
		Insight:        []PlaybookActionResponseInsight{},
	}, nil
}

// filterEventsByInvolvedObject narrows a list of Events to those whose
// involvedObject matches (kind, name, namespace). The agent returns events
// at the namespace (or cluster) level — we trim client-side because the
// agent's get_resource doesn't support field_selector.
func filterEventsByInvolvedObject(data any, kind, name, namespace string) []any {
	arr, ok := data.([]any)
	if !ok {
		return nil
	}
	out := []any{}
	for _, item := range arr {
		ev, ok := item.(map[string]any)
		if !ok {
			continue
		}
		io := getMapField(ev, "involved_object", "involvedObject")
		if io == nil {
			continue
		}
		if name != "" {
			if n, _ := io["name"].(string); n != name {
				continue
			}
		}
		if namespace != "" {
			if ns, _ := io["namespace"].(string); ns != namespace {
				continue
			}
		}
		if kind != "" && !strings.EqualFold(kind, "Any") {
			if k, _ := io["kind"].(string); !strings.EqualFold(k, kind) {
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}

func eventListToTable(data any) ([][]any, []string) {
	headers := []string{"LastSeen", "Type", "Reason", "Object", "Message"}
	rows := [][]any{}
	walk := func(item map[string]any) {
		last := firstStringField(item, "last_timestamp", "lastTimestamp")
		if last == "" {
			last = firstStringField(item, "event_time", "eventTime")
		}
		eventType, _ := item["type"].(string)
		reason, _ := item["reason"].(string)
		obj := ""
		if io, ok := item["involved_object"].(map[string]any); ok {
			obj = involvedObjectLabel(io)
		} else if io, ok := item["involvedObject"].(map[string]any); ok {
			obj = involvedObjectLabel(io)
		}
		msg, _ := item["message"].(string)
		rows = append(rows, []any{last, eventType, reason, obj, msg})
	}
	switch d := data.(type) {
	case []any:
		for _, item := range d {
			if m, ok := item.(map[string]any); ok {
				walk(m)
			}
		}
	case map[string]any:
		walk(d)
	}
	return rows, headers
}

func involvedObjectLabel(io map[string]any) string {
	kind, _ := io["kind"].(string)
	name, _ := io["name"].(string)
	return fmt.Sprintf("%s/%s", kind, name)
}

func firstStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// titleASCII uppercases the first byte if it's a lowercase ASCII letter.
// Replaces strings.Title for the narrow case of canonicalising a K8s kind
// string (pod -> Pod, deployment -> Deployment) — those are all ASCII so
// we don't need the deprecated Unicode-aware strings.Title.
func titleASCII(s string) string {
	if s == "" {
		return s
	}
	c := s[0]
	if c >= 'a' && c <= 'z' {
		return string(c-('a'-'A')) + s[1:]
	}
	return s
}
