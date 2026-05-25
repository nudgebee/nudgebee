package playbooks

import (
	"errors"
	"fmt"
)

// node_status_enricher renders the node's status.conditions[] as a markdown
// table — Ready / MemoryPressure / DiskPressure / PIDPressure / NetworkUnavailable.
type nodeStatusAction struct{}

func (a *nodeStatusAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "node_not_ready" {
		return false
	}
	return subjectNodeName(ctx.GetEvent()) != ""
}

func (a *nodeStatusAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	return a.Execute(ctx, map[string]any{"node_name": subjectNodeName(ctx.GetEvent())})
}

func (a *nodeStatusAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	nodeName, _ := rawParams["node_name"].(string)
	if nodeName == "" {
		return nil, errors.New("node_status_enricher: node_name required")
	}
	data, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "nodes",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{},
		"all_namespaces": false,
		"name":           []string{nodeName},
	})
	if err != nil {
		return nil, fmt.Errorf("node_status_enricher: %w", err)
	}
	node := firstResourceDict(data)
	if node == nil {
		return nil, errors.New("node_status_enricher: node not found")
	}
	status, _ := node["status"].(map[string]any)
	headers := []string{"Type", "Status", "LastTransition", "Reason", "Message"}
	rows := [][]any{}
	if conds := getArrayField(status, "conditions"); conds != nil {
		for _, c := range conds {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			t, _ := cm["type"].(string)
			st, _ := cm["status"].(string)
			lt := firstStringField(cm, "last_transition_time", "lastTransitionTime")
			r, _ := cm["reason"].(string)
			msg, _ := cm["message"].(string)
			rows = append(rows, []any{t, st, lt, r, msg})
		}
	}
	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              fmt.Sprintf("Node conditions: %s", nodeName),
			"action_name":        "node_status_enricher",
			"actual_action_name": "node_status_enricher",
			"node_name":          nodeName,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}
