package playbooks

import (
	"errors"
	"fmt"
)

// pod_enricher returns the full Pod object (spec + status) as a JSON evidence
// block so the UI can render container details, image, restart count, owner
// references, QoS class, etc.
//
// Fires for any pod-subject event with a resolvable name+namespace — OOM,
// crash, image_pull_backoff. Skipped when the agent already provided a
// `pod_enricher` block (see agentToServerActionMap).
type podEnricherAction struct{}

var podEnricherAggKeys = map[string]bool{
	"pod_oom_killer_enricher":     true,
	"report_crash_loop":           true,
	"image_pull_backoff_reporter": true,
}

func (a *podEnricherAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if !podEnricherAggKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	name, ns := SubjectPodNamespace(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *podEnricherAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := SubjectPodNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"pod_name": name, "namespace": ns})
}

func (a *podEnricherAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	podName, _ := rawParams["pod_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if podName == "" || namespace == "" {
		return nil, errors.New("pod_enricher: pod_name + namespace required")
	}

	data, additionalInfo, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "pods",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{podName},
	})
	if err != nil {
		return nil, fmt.Errorf("pod_enricher: %w", err)
	}
	// get_resource does not reliably honor the name/namespace filter for pods
	// (observed returning hundreds of unrelated pods for a single-pod query).
	data = filterPodsByNameNamespace(data, podName, namespace)

	if additionalInfo == nil {
		additionalInfo = map[string]any{}
	}
	additionalInfo["title"] = "Pod details"
	additionalInfo["action_name"] = "pod_enricher"
	additionalInfo["actual_action_name"] = "pod_enricher"
	additionalInfo["pod_name"] = podName
	additionalInfo["namespace"] = namespace

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return NewPlaybookActionResponseJson(data, additionalInfo, []PlaybookActionResponseInsight{}, metadata), nil
}

// filterPodsByNameNamespace narrows a get_resource "pods" response down to
// the pod(s) matching name+namespace.
//
// If the response isn't a pod list at all, data is left unchanged (fail
// open) — an unrecognized shape is worth seeing raw rather than guessing.
//
// If it IS a pod list (empty or not) but none of its entries match, the
// target pod almost always no longer exists — observed on real dev-cluster
// traffic: ephemeral scan-job pods (gone by the time this enricher runs) and
// OOMKilled pods Kubernetes had already rescheduled under a new name.
// Falling back to the full unfiltered list in that case (the original
// behavior) returns hundreds of unrelated pods and megabytes of noise
// instead of the single requested one — measured up to 525 pods / 5.8MB for
// one missing pod. Returns a small "not found" marker instead, carrying the
// requested name/namespace so downstream consumers see why nothing came
// back rather than just an empty result.
func filterPodsByNameNamespace(data any, name, namespace string) any {
	list, ok := data.([]any)
	if !ok {
		return data
	}
	matched := make([]any, 0, 1)
	for _, item := range list {
		pod, ok := item.(map[string]any)
		if !ok {
			continue
		}
		meta, ok := pod["metadata"].(map[string]any)
		if !ok {
			continue
		}
		podName, _ := meta["name"].(string)
		podNamespace, _ := meta["namespace"].(string)
		if podName == name && podNamespace == namespace {
			matched = append(matched, item)
		}
	}
	if len(matched) == 0 {
		return []any{map[string]any{
			"pod_not_found":       true,
			"requested_name":      name,
			"requested_namespace": namespace,
			"note":                "no pod matching this name/namespace exists in the cluster — it was likely already deleted or rescheduled under a different name",
		}}
	}
	return matched
}
