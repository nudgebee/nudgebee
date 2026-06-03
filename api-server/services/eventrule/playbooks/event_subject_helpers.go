package playbooks

import (
	"nudgebee/services/common"
	"nudgebee/services/relay"
	"strings"
	"time"
)

// Shared resolvers for the trigger-matched enricher chain. They read the
// canonical fields the trigger engine fills in (SubjectName / SubjectNamespace
// / SubjectType / Labels) and fall back to a relay `get_resource` lookup when
// the agent-side payload didn't carry enough context.

// subjectPodNamespace extracts (name, namespace) for a pod-subject event.
// Returns empty strings when the event's subject is not a pod.
func subjectPodNamespace(event PlaybookEvent) (string, string) {
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

// subjectNodeName extracts the node name for an event. Order:
//  1. canonical event.SubjectNode field — populated by the collector from
//     the kubewatch payload (events.subject_node column).
//  2. SubjectName when SubjectType=="node" (the subject IS the node).
//  3. label fallback ("node" / "instance") for alert-driven events that
//     don't have a subject_node column.
//
// Enrichers that need the host node should always call this rather than
// re-fetching the Pod via relay — the data is already in hand.
func subjectNodeName(event PlaybookEvent) string {
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
	return event.Labels["instance"]
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

// promRangeQueries fires N named range queries against prometheus_queries_enricher
// over the given lookback duration (minutes). Returns the per-key payload —
// each value is the {result_type, series_list_result, vector_result, …}
// envelope the relay produces.
//
// Window resolution: when the event carries timestamps (StartedAt / EndedAt
// set by the trigger engine for k8s events, or by the AlertManager webhook
// for alerts) we centre the window on the event so the graph reflects
// cluster state AT incident time, not at investigation time. Falls back to
// (now - lookback, now) for ad-hoc / manual playbook runs.
func promRangeQueries(ctx PlaybookActionContext, queries []NamedQuery, lookbackMinutes int) (map[string]any, error) {
	if lookbackMinutes <= 0 {
		lookbackMinutes = 60
	}
	end := time.Now().UTC()
	if t := ctx.GetEvent().EndedAt; t != nil && !t.IsZero() {
		end = t.UTC()
	} else if t := ctx.GetEvent().StartedAt; t != nil && !t.IsZero() {
		end = t.UTC()
	}
	start := end.Add(-time.Duration(lookbackMinutes) * time.Minute)
	rel := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "prometheus_queries_enricher",
			ActionParams: map[string]any{
				"duration": map[string]any{
					"starts_at": start.Format("2006-01-02 15:04:05 UTC"),
					"ends_at":   end.Format("2006-01-02 15:04:05 UTC"),
				},
				"instant":        false,
				"step":           "30s",
				"promql_queries": queries,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	resp, _, err := relay.ExecuteAndExtractResponse(rel)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	switch d := resp["data"].(type) {
	case map[string]any:
		result = d
	case string:
		if err := common.UnmarshalJson([]byte(d), &result); err != nil {
			return nil, err
		}
	}
	return result, nil
}
