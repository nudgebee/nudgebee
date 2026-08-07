package tools

import (
	"errors"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationExecuteCli, func(accountId string) (core.NBTool, error) {
		return RecommendationCliTool{}, nil
	})
}

const ToolRecommendationExecuteCli = "recommendation_execute_cli"

// RecommendationCliTool executes cloud CLI commands through api-server's
// cloud_execute_command RPC under the requesting user's role — the same flow
// as the UI's "Apply Mitigation". When recommendation_id is set, the api-server
// handler links the run to the recommendation's resolution history
// (CloudResource attempt, settled Success/Failed from the batch outcome) and
// the execution lands in the account's command-execution audit trail.
type RecommendationCliTool struct{}

func (m RecommendationCliTool) Name() string             { return ToolRecommendationExecuteCli }
func (m RecommendationCliTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m RecommendationCliTool) Description() string {
	return "Executes cloud CLI commands (aws / az / gcloud) against the account to resolve a recommendation, after the user confirms. Pass recommendation_id so the run registers in that recommendation's resolution history and settles it from the outcome. The batch stops at the first failing command (the rest return NOT_EXECUTED). Requires account-admin or tenant-admin; read-only accounts are rejected. Inputs: commands (required, list of full CLI commands), recommendation_id (strongly recommended)."
}

func (m RecommendationCliTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"commands": {
				Type:        core.ToolSchemaTypeArray,
				Description: "Full cloud CLI commands to run in order, e.g. [\"aws cloudwatch put-metric-alarm --alarm-name ...\"]. Execution stops at the first failure.",
			},
			"recommendation_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the recommendation being resolved — links the execution to its resolution history. Strongly recommended.",
			},
		},
		Required: []string{"commands"},
	}
}

// InferToolRequestType is static on purpose (see RecommendationApplyTool).
func (m RecommendationCliTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeUpdate, nil
}

// ConfirmationKey makes the write confirmation per-action.
func (m RecommendationCliTool) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(ToolRecommendationExecuteCli, toolInput)
}

func (m RecommendationCliTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	rawCommands, _ := input.Arguments["commands"].([]any)
	commands := make([]string, 0, len(rawCommands))
	for _, c := range rawCommands {
		if s, ok := c.(string); ok && s != "" {
			commands = append(commands, s)
		}
	}
	if len(commands) == 0 {
		return errNBLLMToolResponse(errors.New("commands is required and must be a non-empty list of strings")), nil
	}

	// cloud handlers unmarshal the input directly — no "object" wrapper.
	payload := map[string]any{
		"account_id":        nbCtx.AccountId,
		"commands":          commands,
		"recommendation_id": stringArg(input, "recommendation_id"),
	}

	resp, err := doApiServerActionRequest(nbCtx, "/rpc/cloud", "cloud_execute_command", payload, "recommendation_execute_cli")
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("recommendation_execute_cli: execution failed", "error", err)
		return errNBLLMToolResponse(err), nil
	}
	return core.NBToolResponse{
		Data:   resp,
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
