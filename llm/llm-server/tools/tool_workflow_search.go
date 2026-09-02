package tools

import (
	"fmt"
	"net/url"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// ToolWorkflowSearch finds automations by what they DO, so the assistant can
// reach for a customer's own runbook when a user describes a problem.
const ToolWorkflowSearch = "workflow_search"

func init() {
	core.RegisterNBToolFactory(ToolWorkflowSearch, func(accountId string) (core.NBTool, error) {
		return WorkflowSearchTool{}, nil
	})
}

// WorkflowSearchTool searches the automations this account has opted in to AI
// invocation, ranked against a free-text description of the problem.
//
// It exists because workflow_list answers the wrong question: it returns
// automations by name and status, which only helps when you already know which
// one you want. The whole value here is going from "payment pods are
// crashlooping" to the automation someone wrote for exactly that.
//
// Ranking is done by the automation service rather than here, because that is
// where the published llm_description lives, and because
// scoping the search to workflows keeps a customer's automations from competing
// for slots against every other tool in the catalog.
type WorkflowSearchTool struct{}

func (t WorkflowSearchTool) Name() string             { return ToolWorkflowSearch }
func (t WorkflowSearchTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t WorkflowSearchTool) Description() string {
	return "Finds automations this account has approved for you to run, matched against a " +
		"description of the problem rather than an automation name. Use it before answering " +
		"with manual steps: the team may already have an automation for this. " +
		"Returns each automation's id, what it does, when to use it, and its input parameters — " +
		"pass the id to workflow_trigger to run it. " +
		"Call with no query to list everything you are allowed to run. " +
		"Args: query (string, optional), limit (number, optional, default 5)."
}

// InferToolRequestType marks the search as a read so it runs without a
// confirmation prompt. Only the subsequent workflow_trigger has effects.
func (t WorkflowSearchTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t WorkflowSearchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"query": {
				Type: core.ToolSchemaTypeString,
				Description: "What you are trying to do or the problem you are solving, in plain words " +
					"(e.g. 'payment pods are crashlooping'). Omit to list every automation you may run.",
			},
			"limit": {
				Type:        core.ToolSchemaTypeNumber,
				Description: "Maximum automations to return. Defaults to 5.",
			},
		},
	}
}

func (t WorkflowSearchTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	query, _ := input.Arguments["query"].(string)

	params := url.Values{}
	if q := strings.TrimSpace(query); q != "" {
		params.Set("query", q)
	}
	// JSON numbers decode as float64; accept a string too, since models are
	// inconsistent about quoting numeric arguments.
	switch limit := input.Arguments["limit"].(type) {
	case float64:
		if limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", int(limit)))
		}
	case string:
		if trimmed := strings.TrimSpace(limit); trimmed != "" {
			params.Set("limit", trimmed)
		}
	}

	path := "workflows/ai-search"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}

	// Carry the caller's context so an abandoned chat turn does not leave this
	// request running. workflow_trigger already threads it through for the wait;
	// a search that outlives the conversation asking for it is the same waste.
	resp, err := DoRunbookRequestWithContext(ctx.GoContext(), "GET", path, nil, ctx.AccountId,
		ctx.Ctx.GetSecurityContext().GetTenantId(), ctx.Ctx.GetSecurityContext().GetUserId())
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("failed to search automations: %w", err)
	}

	return core.NBToolResponse{Data: string(resp), Type: core.NBToolResponseTypeJson}, nil
}
