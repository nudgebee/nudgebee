package tools

import (
	"encoding/json"
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

// WorkflowSearchTool searches the automations opted in to AI invocation, in
// this account and in the user's other readable accounts (each group apart, see
// tool_workflow_accounts.go), ranked against a free-text description of the
// problem.
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
	return "Finds automations you are approved to run, matched against a " +
		"description of the problem rather than an automation name. Use it before answering " +
		"with manual steps: the team may already have an automation for this. " +
		"Returns each automation's id, what it does, when to use it, and its input parameters — " +
		"pass the id to workflow_trigger to run it. Automations of this conversation's account come under " +
		"this_account; those of other accounts the user can access come under other_accounts, grouped by " +
		"account with its account_id and account_name — pass that account_id to workflow_trigger with the id. " +
		"Call with no query to list everything you are allowed to run. " +
		"Args: query (string, optional), limit (number, optional, default 5). " + automationURLToolHint
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

	own := withNoMatchNote(injectAutomationURLs(resp, ctx.AccountId))
	groups, otherTotal, problem := otherWorkflowAccounts(ctx, path, injectAutomationURLs)
	return core.NBToolResponse{Data: string(withWorkflowAccounts(own, ctx.AccountId, groups, otherTotal, problem)), Type: core.NBToolResponseTypeJson}, nil
}

// noMatchNote travels with a search that found nothing the assistant may run.
// An empty list was read as "the automation does not exist" and answered with
// the assistant's own checks titled as that automation's report; the
// automation may exist and simply not be runnable by the assistant.
const noMatchNote = "No automation the assistant may run matched. An automation of that name may still exist but not be runnable " +
	"by the assistant (paused, not enabled for the assistant, or without a manual trigger); workflow_list shows every " +
	"automation with its status. Nothing was run."

// withNoMatchNote adds noMatchNote to a search result whose workflows list is
// empty. Any other result, or one that is not the expected shape, is returned
// unchanged.
func withNoMatchNote(raw []byte) []byte {
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw
	}
	if workflows, ok := parsed["workflows"].([]any); (ok && len(workflows) > 0) || (!ok && parsed["workflows"] != nil) {
		return raw
	}
	parsed["note"] = noMatchNote
	b, err := marshalNoHTMLEscape(parsed, "")
	if err != nil {
		return raw
	}
	return b
}

// injectAutomationURLs adds the canonical `url` to each item in `workflows`.
// Returns raw unchanged on an unexpected shape or empty accountId.
func injectAutomationURLs(raw []byte, accountId string) []byte {
	if accountId == "" {
		return raw
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw
	}
	workflows, ok := parsed["workflows"].([]any)
	if !ok {
		return raw
	}
	for _, w := range workflows {
		item, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := item["id"].(string); ok {
			if u := core.BuildAutomationURL(accountId, id, nil, ""); u != "" {
				item["url"] = u
			}
		}
	}
	b, err := marshalNoHTMLEscape(parsed, "")
	if err != nil {
		return raw
	}
	return b
}
