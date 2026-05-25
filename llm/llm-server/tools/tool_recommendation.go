package tools

import (
	"fmt"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationExecuteSql, func(accountId string) (core.NBTool, error) {
		return RecommendationExecuteTool{}, nil
	})
}

const recommendationView = `
		SELECT r.id::text as id,
			t.name AS tenant,
			ca.account_name AS account,
			ca.id::text AS cloud_account_id,
			(
				CASE
					WHEN cr.meta ->> 'namespace' IS NOT NULL THEN cr.meta ->> 'namespace'
					WHEN cr.meta -> 'config' ->> 'namespace' IS NOT NULL THEN cr.meta -> 'config' ->> 'namespace'
					WHEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace'
					WHEN r.recommendation -> 'metadata' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'metadata' ->> 'namespace'
					ELSE r.recommendation ->> 'namespace'
				END
        	) AS namespace,
			cr.name AS resource_name,
			r.recommendation ->> 'controller_name'::text AS controller_name,
			(r.recommendation ->> 'estimated_saving'::text)::numeric AS estimated_saving,
			r.created_at,
			r.updated_at,
			r.recommendation::text AS recommendation,
			r.recommendation_action,
			r.category,
			r.note,
			r.severity,
			r.status,
			r.rule_name,
			r.dismissed_reason,
			r.is_dismissed,
			r.account_object_id,
			r.updated_by::text
		FROM recommendation r
		LEFT JOIN cloud_resourses cr ON r.resource_id = cr.id
		JOIN tenant t ON r.tenant_id = t.id
		JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
	`

const ToolRecommendationExecuteSql = "recommendation_execute"

type RecommendationExecuteTool struct {
}

func (m RecommendationExecuteTool) Name() string {
	return ToolRecommendationExecuteSql
}

func (m RecommendationExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m RecommendationExecuteTool) Description() string {
	return "Executes a SQL query for recommendation_view and returns the result."
}

func (m RecommendationExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "recommendation_view SQL Query to execute",
			},
		},
		Required: []string{"command"},
	}
}

// recommendationMaxJSONChars caps the recommendation JSON field in tool
// responses to prevent token bloat in the ReAct scratchpad. The full JSON
// can exceed 100K chars; truncating to 500 keeps context small while
// preserving the most relevant leading content.
const recommendationMaxJSONChars = 500

// truncateRecommendationJSON caps the "recommendation" field in each row
// so that oversized JSON blobs do not inflate the LLM context window.
func truncateRecommendationJSON(r map[string]any, _ int, _ int) map[string]any {
	v, ok := r["recommendation"]
	if !ok || v == nil {
		return r
	}
	var s string
	switch val := v.(type) {
	case string:
		s = val
	case []byte:
		s = string(val)
	default:
		s = fmt.Sprintf("%v", val)
	}

	const suffix = "...(truncated)"
	if len(s) > recommendationMaxJSONChars {
		maxContentLen := recommendationMaxJSONChars - len(suffix)
		if maxContentLen < 0 {
			maxContentLen = 0
		}
		s = s[:maxContentLen] + suffix
	}
	r["recommendation"] = s
	return r
}

func (m RecommendationExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	resp, _, err := sqlToolCall(nbRequestContext, input.Command, "recommendation_view", recommendationView, 10, truncateRecommendationJSON)
	if err == nil {
		resp.References = []core.NBToolResponseReference{
			core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"optimize", "summary"}, "Recommendation Details", nil, ""),
		}
	}
	return resp, err
}
