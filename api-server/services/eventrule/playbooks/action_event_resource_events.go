package playbooks

import (
	"errors"
	"fmt"
	"strings"
)

// event_resource_events_enricher fires on Kubernetes Warning Event findings:
// the trigger engine emits the *event itself* as subject, but the actionable
// data is the involved-object's sibling events. This enricher fetches events
// for that involved-object so the UI shows the surrounding chatter.
//
// The trigger-engine pre-fills SubjectName with `<kind>/<involvedObject.name>`
// and SubjectNamespace with the involvedObject namespace (see
// nudgebee-agent/pkg/triggers/predicates.go warningEventMatcher). We split the
// `kind/name` form here.
type eventResourceEventsAction struct{}

func (a *eventResourceEventsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "Kubernetes Warning Event" {
		return false
	}
	return ctx.GetEvent().SubjectName != ""
}

func (a *eventResourceEventsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	return a.Execute(ctx, map[string]any{
		"subject":   ctx.GetEvent().SubjectName,
		"namespace": ctx.GetEvent().SubjectNamespace,
		"kind":      ctx.GetEvent().SubjectType,
	})
}

func (a *eventResourceEventsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	subject, _ := rawParams["subject"].(string)
	namespace, _ := rawParams["namespace"].(string)
	kind, _ := rawParams["kind"].(string)
	if subject == "" {
		return nil, errors.New("event_resource_events_enricher: subject required")
	}
	// Split "kind/name" if present — only if both halves are non-empty.
	// Cases to defend against:
	//   "Pod/foo"  -> kind=Pod, name=foo  (canonical case)
	//   "foo"      -> kind=<rawParams kind or "Pod">, name=foo
	//   "Pod/"     -> reject (empty name would broaden filterEventsByInvolvedObject)
	//   "/foo"     -> idx==0 left untouched by `idx > 0`; keep as name=subject
	name := subject
	if idx := strings.Index(subject, "/"); idx > 0 {
		if idx == len(subject)-1 {
			return nil, fmt.Errorf("event_resource_events_enricher: subject %q has empty name after '/'", subject)
		}
		kind = subject[:idx]
		name = subject[idx+1:]
	}
	if name == "" {
		return nil, errors.New("event_resource_events_enricher: name required")
	}
	if kind == "" {
		kind = "Pod"
	}
	data, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "events",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": namespace == "",
		"name":           []string{},
	})
	if err != nil {
		return nil, fmt.Errorf("event_resource_events_enricher: %w", err)
	}

	rows, headers := eventListToTable(filterEventsByInvolvedObject(data, kind, name, namespace))
	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              fmt.Sprintf("Recent events for %s/%s", kind, name),
			"action_name":        "event_resource_events_enricher",
			"actual_action_name": "event_resource_events_enricher",
			"subject":            name,
			"namespace":          namespace,
			"kind":               kind,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}
