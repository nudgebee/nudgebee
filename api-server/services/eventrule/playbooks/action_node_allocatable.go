package playbooks

import (
	"errors"
	"fmt"
)

// node_allocatable_resources_enricher renders a table of the node's
// capacity vs allocatable for CPU + memory.
type nodeAllocatableAction struct{}

func (a *nodeAllocatableAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "node_not_ready" {
		return false
	}
	return subjectNodeName(ctx.GetEvent()) != ""
}

func (a *nodeAllocatableAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	return a.Execute(ctx, map[string]any{"node_name": subjectNodeName(ctx.GetEvent())})
}

func (a *nodeAllocatableAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	nodeName, _ := rawParams["node_name"].(string)
	if nodeName == "" {
		return nil, errors.New("node_allocatable_resources_enricher: node_name required")
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
		return nil, fmt.Errorf("node_allocatable_resources_enricher: %w", err)
	}
	node := firstResourceDict(data)
	if node == nil {
		return nil, errors.New("node_allocatable_resources_enricher: node not found")
	}

	rows := [][]any{{"Node", nodeName}}
	status, _ := node["status"].(map[string]any)
	if cap, ok := status["capacity"].(map[string]any); ok {
		if v, ok := cap["cpu"].(string); ok {
			rows = append(rows, []any{"CPU capacity", v})
		}
		if v, ok := cap["memory"].(string); ok {
			rows = append(rows, []any{"Memory capacity", v})
		}
		if v, ok := cap["pods"].(string); ok {
			rows = append(rows, []any{"Pods capacity", v})
		}
	}
	if alloc, ok := status["allocatable"].(map[string]any); ok {
		if v, ok := alloc["cpu"].(string); ok {
			rows = append(rows, []any{"CPU allocatable", v})
		}
		if v, ok := alloc["memory"].(string); ok {
			rows = append(rows, []any{"Memory allocatable", v})
		}
		if v, ok := alloc["pods"].(string); ok {
			rows = append(rows, []any{"Pods allocatable", v})
		}
	}

	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: []string{"Field", "Value"},
		AdditionalInfo: map[string]any{
			"title":              "Node capacity & allocatable",
			"action_name":        "node_allocatable_resources_enricher",
			"actual_action_name": "node_allocatable_resources_enricher",
			"node_name":          nodeName,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}
