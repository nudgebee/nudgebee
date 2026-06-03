package playbooks

import (
	"errors"
	"fmt"
)

// node_running_pods_enricher lists pods running on the node — used as
// context on KubeNodeNotReady findings.
type nodeRunningPodsAction struct{}

func (a *nodeRunningPodsAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "node_not_ready" {
		return false
	}
	return subjectNodeName(ctx.GetEvent()) != ""
}

func (a *nodeRunningPodsAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	return a.Execute(ctx, map[string]any{"node_name": subjectNodeName(ctx.GetEvent())})
}

func (a *nodeRunningPodsAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	nodeName, _ := rawParams["node_name"].(string)
	if nodeName == "" {
		return nil, errors.New("node_running_pods_enricher: node_name required")
	}
	data, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "pods",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{},
		"all_namespaces": true,
		"name":           []string{},
	})
	if err != nil {
		return nil, fmt.Errorf("node_running_pods_enricher: %w", err)
	}
	// Agent's get_resource doesn't honor field_selector — filter client-side
	// on spec.node_name. Without the filter we'd render every pod in the
	// cluster (~450 rows on the dev cluster).
	arr, _ := data.([]any)
	headers := []string{"Pod", "Namespace", "Phase", "Containers"}
	rows := [][]any{}
	for _, item := range arr {
		pod, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if nodeNameFromPodDict(pod) != nodeName {
			continue
		}
		md, _ := pod["metadata"].(map[string]any)
		status, _ := pod["status"].(map[string]any)
		spec, _ := pod["spec"].(map[string]any)
		name, _ := md["name"].(string)
		ns, _ := md["namespace"].(string)
		phase, _ := status["phase"].(string)
		containers := 0
		if c, ok := spec["containers"].([]any); ok {
			containers = len(c)
		}
		rows = append(rows, []any{name, ns, phase, containers})
	}
	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              fmt.Sprintf("Pods running on %s", nodeName),
			"action_name":        "node_running_pods_enricher",
			"actual_action_name": "node_running_pods_enricher",
			"node_name":          nodeName,
			"total":              len(rows),
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}
