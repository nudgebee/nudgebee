package tools

import (
	"fmt"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationApply, func(accountId string) (core.NBTool, error) {
		return RecommendationApplyTool{}, nil
	})
}

const ToolRecommendationApply = "recommendation_apply"

// RecommendationApplyTool executes the same resolve flow the optimise UI's
// "Deploy Fix" / "Raise PR" buttons use, through api-server's
// recommendations_apply RPC under the requesting user's role. The api-server
// side owns the resolution lifecycle: it writes the recommendation_resolution
// attempt (DeploymentChange / PullRequest / CloudResource), moves the
// recommendation's status, and reuses an already-open PR instead of raising a
// duplicate. The tool itself holds no credentials.
type RecommendationApplyTool struct{}

func (m RecommendationApplyTool) Name() string             { return ToolRecommendationApply }
func (m RecommendationApplyTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m RecommendationApplyTool) Description() string {
	return "Applies an optimisation recommendation after the user confirms: creates the deployment change, pull request, or cloud alarm through NudgeBee's apply flow, registers the resolution attempt, and returns the resulting recommendation status (plus pr_action when a PR is involved). Requires write access; the platform asks the user to approve each apply before it runs. Inputs: recommendation_id (required); data (per-rule payload — for pod_right_sizing a map of container name to {cpu:{request,limit}, memory:{request,limit}} with memory values like '512Mi'); provider ('' infers from the account; or 'git', 'github', 'gitlab', 'kubernetes', 'aws', 'azure', 'gcp'); provider_config (e.g. {\"in_place\": true} or {\"name\": \"<git integration>\"})."
}

func (m RecommendationApplyTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"recommendation_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of the recommendation to apply (recommendation.id).",
			},
			"data": {
				Type:        core.ToolSchemaTypeObject,
				Description: "Per-rule apply payload. For pod_right_sizing: {\"<container>\": {\"cpu\": {\"request\": \"0.25\", \"limit\": \"0.3\"}, \"memory\": {\"request\": \"512Mi\", \"limit\": \"600Mi\"}}}. Omit to apply the recommendation's own proposed values where the backend supports it.",
			},
			"summary": {
				Type:        core.ToolSchemaTypeString,
				Description: "One line describing the exact change, shown on the user's approval card — ALWAYS fill it from the recommendation's data, e.g. 'Right-size deployment product-catalog (demo): CPU request 250m → 100m, memory request 512Mi → 300Mi (est. $41/mo saving)'. Never leave the approver guessing what will change.",
			},
			"provider": {
				Type:        core.ToolSchemaTypeString,
				Description: "Resolution channel: '' (infer from account), 'kubernetes' (direct deployment change), 'git'/'github'/'gitlab' (pull request), or 'aws'/'azure'/'gcp' (cloud alarm apply).",
			},
			"provider_config": {
				Type:        core.ToolSchemaTypeObject,
				Description: "Channel options, e.g. {\"in_place\": true} for in-place pod resize, {\"name\": \"<git integration name>\"} to pick a git integration.",
			},
		},
		Required: []string{"recommendation_id"},
	}
}

// InferToolRequestType is static on purpose: it routes the call through the
// executor's write-confirmation gate without an LLM classification pass (which
// would force the whole plan sequential).
func (m RecommendationApplyTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeUpdate, nil
}

// ConfirmationKey makes the write confirmation per-action: each distinct apply
// input re-prompts the user instead of riding an earlier approval.
func (m RecommendationApplyTool) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(ToolRecommendationApply, toolInput)
}

// ConfirmationQuestion renders the approval card in operator terms — the
// concrete change (from the summary the model fills off the recommendation)
// and the channel, not a raw recommendation id.
func (m RecommendationApplyTool) ConfirmationQuestion(toolInput string) string {
	args := confirmationArgs(toolInput)
	recommendationId, _ := args["recommendation_id"].(string)
	if recommendationId == "" {
		return ""
	}
	channel := "the platform apply flow (deployment change, pull request, or cloud alarm)"
	switch provider, _ := args["provider"].(string); provider {
	case "git", "github", "gitlab":
		channel = "a pull request"
	case "kubernetes":
		channel = "a direct deployment change"
	case "aws", "azure", "gcp":
		channel = "the cloud alarm apply flow"
	}
	if summary, _ := args["summary"].(string); strings.TrimSpace(summary) != "" {
		return fmt.Sprintf("Apply this change via %s?\n%s\n(Recommendation %s — the change registers as its resolution attempt.) Do you want to continue?",
			channel, strings.TrimSpace(summary), recommendationId)
	}
	return fmt.Sprintf("Apply recommendation %s via %s?\nThe change registers as this recommendation's resolution attempt. Do you want to continue?",
		recommendationId, channel)
}

func (m RecommendationApplyTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	recommendationId := stringArg(input, "recommendation_id")
	if recommendationId == "" {
		return errNBLLMToolResponse(errRecommendationIdRequired), nil
	}

	data, _ := input.Arguments["data"].(map[string]any)
	if data == nil {
		// The handler casts data to a map — an absent value must still be an
		// object, not null.
		data = map[string]any{}
	}
	providerConfig, _ := input.Arguments["provider_config"].(map[string]any)

	object := map[string]any{
		"account_id":        nbCtx.AccountId,
		"recommendation_id": recommendationId,
		"data":              data,
		"provider":          stringArg(input, "provider"),
		"resolver_type":     "NBLLM",
	}
	if providerConfig != nil {
		object["provider_config"] = providerConfig
	}

	resp, err := doApiServerActionRequest(nbCtx, "/rpc/recommendation", "recommendations_apply", map[string]any{"object": object}, "recommendation_apply")
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("recommendation_apply: apply failed", "error", err, "recommendation_id", recommendationId)
		return errNBLLMToolResponse(err), nil
	}
	return core.NBToolResponse{
		Data:   resp,
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
