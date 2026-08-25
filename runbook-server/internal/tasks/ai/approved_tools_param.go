package ai

import (
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"strings"
)

// approvedToolsParamFieldName is the schema key for the optional list of tools
// the calling workflow has already established authority to use for this run.
const approvedToolsParamFieldName = "approved_tools"

// approvedToolsInputSchemaProperty exposes that list on LLM tasks that hand a
// request to an agent.
//
// llm-server stops an agent and asks a human to confirm before every
// create/update/delete tool call. A person answers that in chat; a workflow
// cannot, so the agent sits in WAITING until the run times out - which makes
// any agent step that writes something unusable from a workflow.
//
// A workflow that has already established authority for the run - an approved
// ticket, an upstream core.approval - names the tools that authority covers.
// It is deliberately a list and not a boolean: the workflow states what it is
// vouching for, and anything it did not name still stops and asks.
func approvedToolsInputSchemaProperty(order int) types.Property {
	return types.Property{
		Type:     types.PropertyTypeArray,
		SubType:  "string",
		Required: false,
		Order:    order,
		Description: "Tools the agent may use without stopping to ask a human, because this workflow " +
			"has already established authority for them (e.g. an approved ticket). Anything not listed " +
			"still asks. Leave empty unless approval has genuinely already happened.",
	}
}

// parseApprovedToolsParam turns the parameter into the tool-name -> "yes" map
// llm-server expects. Accepts a list, or a comma-separated string for templated
// values. Empty / nil returns nil, leaving every tool to prompt as before.
func parseApprovedToolsParam(raw any) (map[string]string, error) {
	var names []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		names = strings.Split(v, ",")
	case []string:
		names = v
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a list of tool names, got %T in the list", approvedToolsParamFieldName, item)
			}
			names = append(names, s)
		}
	default:
		return nil, fmt.Errorf("%s must be a list of tool names, got %T", approvedToolsParamFieldName, raw)
	}

	confirmations := map[string]string{}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			confirmations[name] = "yes"
		}
	}
	if len(confirmations) == 0 {
		return nil, nil
	}
	return confirmations, nil
}
