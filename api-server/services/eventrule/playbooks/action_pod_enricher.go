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
	name, ns := subjectPodNamespace(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *podEnricherAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	name, ns := subjectPodNamespace(ctx.GetEvent())
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
