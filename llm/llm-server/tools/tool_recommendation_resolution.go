package tools

import (
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationResolutionExecuteSql, func(accountId string) (core.NBTool, error) {
		return RecommendationResolutionExecuteTool{}, nil
	})
}

// recommendationResolutionView exposes resolution attempts (pull requests,
// tickets, deployment changes, workflow runs) joined with their recommendation
// for context. The resolution table carries no account column, so
// cloud_account_id is derived from the joined recommendation — the SQL tool's
// account filter depends on that alias. The jsonb `data` payload is
// deliberately excluded: it holds full change specs that would bloat the
// ReAct scratchpad.
const recommendationResolutionView = `
		SELECT rr.id::text AS id,
			rr.recommendation_id::text AS recommendation_id,
			r.cloud_account_id::text AS cloud_account_id,
			ca.account_name AS account,
			rr.type,
			rr.type_reference_id,
			rr.resolver_type,
			rr.status,
			rr.status_message,
			rr.pr_lifecycle_state,
			rr.created_at,
			rr.updated_at,
			r.rule_name,
			r.category,
			r.severity,
			r.status AS recommendation_status,
			cr.name AS resource_name
		FROM recommendation_resolution rr
		JOIN recommendation r ON rr.recommendation_id = r.id
		LEFT JOIN cloud_resourses cr ON r.resource_id = cr.id
		JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
	`

// ToolPrompt implements core.NBToolPromptProvider: schema and usage for
// recommendation_resolution_view, shared by every agent that carries this tool.
func (m RecommendationResolutionExecuteTool) ToolPrompt() []string {
	return []string{
		"Use recommendation_resolution_execute for resolution history — what was attempted or done about a recommendation: pull requests, tickets, deployment changes, workflow runs, their outcomes and references. Read-only SELECTs against recommendation_resolution_view; filter by recommendation_id, resource_name, rule_name, type, resolver_type, or status.",
		"**recommendation_resolution_view:** One row per resolution attempt on a recommendation (how it is being, or was, resolved). Queried via the 'recommendation_resolution_execute' tool.",
		"- recommendation_id (STRING): The recommendation the attempt belongs to (join key with recommendation_view id)",
		"- type (ENUM): Artifact created - PullRequest, Ticket, DeploymentChange, CloudResource, WorkflowExecution, EventResolution",
		"- type_reference_id (STRING): Reference to that artifact - PR URL, ticket id, change id",
		"- resolver_type (ENUM): Who initiated it - User, AutoOptimize, AutoRunbook, NBLLM (the AI agent)",
		"- status (ENUM): Attempt state - InProgress (artifact open, work ongoing), Success (completed), Failed (rejected or errored)",
		"- status_message (STRING): Human-readable outcome detail (failure reason, close note)",
		"- pr_lifecycle_state (STRING): For PullRequest attempts, the PR's lifecycle state if tracked",
		"- recommendation_status (ENUM): Current status of the parent recommendation",
		"- resource_name, rule_name, category, severity (STRING/ENUM): Context from the parent recommendation",
		"- created_at, updated_at (TIMESTAMP): When the attempt was registered and last updated",
	}
}

const ToolRecommendationResolutionExecuteSql = "recommendation_resolution_execute"

type RecommendationResolutionExecuteTool struct {
}

func (m RecommendationResolutionExecuteTool) Name() string {
	return ToolRecommendationResolutionExecuteSql
}

func (m RecommendationResolutionExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m RecommendationResolutionExecuteTool) Description() string {
	return "Executes a SQL query for recommendation_resolution_view — the history of resolution attempts (pull requests, tickets, deployment changes, workflow runs) per recommendation. Columns: id, recommendation_id, cloud_account_id, account, type, type_reference_id, resolver_type, status, status_message, pr_lifecycle_state, created_at, updated_at, rule_name, category, severity, recommendation_status, resource_name."
}

func (m RecommendationResolutionExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "recommendation_resolution_view SQL Query to execute",
			},
		},
		Required: []string{"command"},
	}
}

func (m RecommendationResolutionExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	resp, _, err := sqlToolCall(nbRequestContext, input.Command, "recommendation_resolution_view", recommendationResolutionView, 10, nil)
	if err == nil {
		resp.References = []core.NBToolResponseReference{
			core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"optimize", "summary"}, "Recommendation Details", nil, ""),
		}
	}
	return resp, err
}
