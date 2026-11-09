package playbooks

import (
	"net"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/relay"
)

// Shared resolvers for the trigger-matched enricher chain. They read the
// canonical fields the trigger engine fills in (SubjectName / SubjectNamespace
// / SubjectType / Labels) and fall back to a relay `get_resource` lookup when
// the agent-side payload didn't carry enough context.

// SubjectPodNamespace extracts (name, namespace) for a pod-subject event.
// Returns empty strings when the event's subject is not a pod.
func SubjectPodNamespace(event PlaybookEvent) (string, string) {
	// A node alert keeps the labels of the exporter pod that reported it, so its
	// `pod` and `namespace` labels name the reporter, not a pod the alert is about.
	if strings.EqualFold(event.SubjectType, "node") {
		return "", ""
	}
	if !strings.EqualFold(event.SubjectType, "pod") {
		// Trigger-matched k8s events set SubjectType="pod"; alertmanager alerts
		// may not, so fall back to a "pod" label when present.
		if event.Labels == nil || event.Labels["pod"] == "" {
			return "", ""
		}
	}
	name := event.SubjectName
	if name == "" && event.Labels != nil {
		name = event.Labels["pod"]
	}
	namespace := event.SubjectNamespace
	if namespace == "" && event.Labels != nil {
		namespace = event.Labels["namespace"]
	}
	return name, namespace
}

// PodNamespaceFromEvent is SubjectPodNamespace exported for the provider-agnostic
// actions that live in the observability package. Those cannot be defined here:
// observability imports this package to register actions, so a playbook action that
// needs a metrics provider has to be registered from that side of the dependency.
func PodNamespaceFromEvent(event PlaybookEvent) (string, string) {
	return SubjectPodNamespace(event)
}

// subjectJobName extracts the K8s Job name for job-subject events. Empty when
// the subject isn't a Job.
func subjectJobName(event PlaybookEvent) (string, string) {
	kind := strings.ToLower(event.SubjectType)
	if kind != "job" {
		if event.Labels == nil || event.Labels["job_name"] == "" {
			return "", ""
		}
	}
	name := event.SubjectName
	if name == "" && event.Labels != nil {
		name = event.Labels["job_name"]
	}
	namespace := event.SubjectNamespace
	if namespace == "" && event.Labels != nil {
		namespace = event.Labels["namespace"]
	}
	return name, namespace
}

// nodeNameFromPodDict extracts spec.node_name (or spec.nodeName) from a
// snake-cased Pod payload. Pure-utility — used as a fallback by oom_killer
// when event.SubjectNode wasn't populated (e.g. a manual playbook run from
// the UI that didn't carry the canonical column).
func nodeNameFromPodDict(p map[string]any) string {
	spec, ok := p["spec"].(map[string]any)
	if !ok {
		return ""
	}
	if v, ok := spec["node_name"].(string); ok && v != "" {
		return v
	}
	if v, ok := spec["nodeName"].(string); ok && v != "" {
		return v
	}
	return ""
}

// SubjectNodeName extracts the node name for an event. Order:
//  1. canonical event.SubjectNode field — populated by the collector from
//     the kubewatch payload (events.subject_node column).
//  2. SubjectName when SubjectType=="node" (the subject IS the node).
//  3. label fallback ("node", then "instance") for alert-driven events that
//     don't have a subject_node column.
//
// The `instance` fallback is only taken when the value is not a scrape target.
// Prometheus writes `instance` as `<host>:<port>` ("10.128.0.14:9100"), which is
// not a node name: passing it on sends the node enrichers to the relay with a
// name no node ever has, and they come back "node not found". A bare `instance`
// with no port is kept — some rules relabel a real node name into it.
//
// Enrichers that need the host node should always call this rather than
// re-fetching the Pod via relay — the data is already in hand.
func SubjectNodeName(event PlaybookEvent) string {
	if event.SubjectNode != "" {
		return event.SubjectNode
	}
	if strings.EqualFold(event.SubjectType, "node") && event.SubjectName != "" {
		return event.SubjectName
	}
	if event.Labels == nil {
		return ""
	}
	if v := event.Labels["node"]; v != "" {
		return v
	}
	// A value that parses as host:port is a scrape target, not a node. Checked
	// with net.SplitHostPort rather than looking for a colon, because a bare IPv6
	// address ("fe80::1") is full of colons and carries no port.
	if v := event.Labels["instance"]; v != "" {
		if _, _, err := net.SplitHostPort(v); err != nil {
			return v
		}
	}
	return ""
}

// EventIsAboutNode reports whether an event's subject is a node, so the
// node-scoped enrichers can decide to run from the event's shape rather than
// from one hardcoded aggregation key.
//
// `node_not_ready` stays named explicitly: it is the kubewatch-sourced event
// these enrichers were originally written for, and it is the one case where the
// subject can be a node without subject_type saying so.
func EventIsAboutNode(event PlaybookEvent) bool {
	if SubjectNodeName(event) == "" {
		return false
	}
	return strings.EqualFold(event.SubjectType, "node") || event.AggregationKey == "node_not_ready"
}

// getResourceViaRelay is a small wrapper around the get_resource action used
// by every enricher that fetches a K8s object. Returns the unmarshaled `data`
// payload (typically a []any of K8s objects, sometimes a single map[string]any).
//
// relay.ExecuteAndExtractResponse returns the first evidence block; for
// type=json blocks the block's `data` field is a JSON-encoded STRING (see
// relay/service.go:284 + nudgebee-agent/pkg/enrichers/finding.go:67). We decode
// it here so callers can walk maps/slices directly.
func getResourceViaRelay(ctx PlaybookActionContext, params map[string]any) (any, map[string]any, error) {
	rel := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    ctx.GetAccountId(),
			ActionName:   "get_resource",
			ActionParams: params,
			Origin:       "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	resp, additionalInfo, err := relay.ExecuteAndExtractResponse(rel)
	if err != nil {
		return nil, nil, err
	}
	data := resp["data"]
	if s, ok := data.(string); ok {
		var parsed any
		if err := common.UnmarshalJson([]byte(s), &parsed); err == nil {
			return parsed, additionalInfo, nil
		}
		// Couldn't decode — return the raw string so the caller can log it.
		return s, additionalInfo, nil
	}
	return data, additionalInfo, nil
}

// rangeQueryWindow resolves the (start, end] range-query window for an event.
// Event end times can be in the future: getPlaybookStartEndTime fabricates
// EndsAt = StartsAt + 1h for k8s events, and AlertManager sends a future
// endsAt for firing alerts. An unclamped end puts the whole window past "now"
// for short lookbacks, and Prometheus correctly returns zero series — this is
// what kept the Noisy Neighbours card empty on every OOM event.
// RangeQueryWindow is exported for the observability actions that run their
// PromQL through the metrics layer and need the same event-centred window.
func RangeQueryWindow(event PlaybookEvent, lookbackMinutes int, now time.Time) (time.Time, time.Time) {
	end := now
	if t := event.IncidentAt; t != nil && !t.IsZero() {
		// See PlaybookEvent.IncidentAt — the failure time, not the detection time.
		end = t.UTC()
	} else if t := event.EndedAt; t != nil && !t.IsZero() {
		end = t.UTC()
	} else if t := event.StartedAt; t != nil && !t.IsZero() {
		end = t.UTC()
	}
	if end.After(now) {
		end = now
	}
	return end.Add(-time.Duration(lookbackMinutes) * time.Minute), end
}
