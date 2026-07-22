package tools

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// ToolProposeRecommendationApply lets the FinOps agent help a user ACT on a cost
// recommendation without itself mutating cloud resources. It returns a deep link
// to the optimise UI's review-and-apply flow — which builds the resolution payload
// correctly and runs its own confirmation — so the human applies the change. The
// agent surfaces the recommendation's safety band + savings alongside the link.
//
// This is deliberately a read-only hand-off rather than a direct apply: the apply
// backend needs a recommendation-type-specific resolution payload that the UI
// constructs, so routing the user there keeps the agent out of that business and
// avoids duplicating (or mis-building) it.
const ToolProposeRecommendationApply = "propose_recommendation_apply"

func init() {
	core.RegisterNBToolFactory(ToolProposeRecommendationApply, func(accountId string) (core.NBTool, error) {
		return ProposeRecommendationApplyTool{}, nil
	})
}

// ProposeRecommendationApplyTool builds a deep link to the optimise UI for a
// recommendation so the user can review its blast radius and apply it. Read-only:
// it performs no mutation and calls no apply backend.
type ProposeRecommendationApplyTool struct{}

func (t ProposeRecommendationApplyTool) Name() string { return ToolProposeRecommendationApply }

func (t ProposeRecommendationApplyTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t ProposeRecommendationApplyTool) Description() string {
	return "Returns a deep link to the optimise page where the user can review a cost recommendation's blast radius and apply it. Use this to hand an apply off to the user AFTER presenting the recommendation's safety band (safe / review / risky / unknown), blast radius, and estimated savings. This does NOT apply the change — the user reviews and applies in the UI. Never claim a recommendation has been applied."
}

func (t ProposeRecommendationApplyTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"recommendation_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "ID of the recommendation to hand off for review and apply.",
			},
			"account_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cloud account the recommendation belongs to. Defaults to the conversation's account when omitted.",
			},
		},
		Required: []string{"recommendation_id"},
	}
}

// InferToolRequestType is read-only: the tool only produces a link and never
// mutates anything, so it doesn't engage the executor's write-confirmation gate
// (the apply itself is confirmed in the UI).
func (t ProposeRecommendationApplyTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t ProposeRecommendationApplyTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	recID, _ := input.Arguments["recommendation_id"].(string)
	recID = strings.TrimSpace(recID)
	if recID == "" {
		return core.NBToolResponse{}, errors.New("propose_recommendation_apply: recommendation_id is required")
	}

	accountID, _ := input.Arguments["account_id"].(string)
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		accountID = ctx.AccountId
	}
	if accountID == "" {
		return core.NBToolResponse{}, errors.New("propose_recommendation_apply: account_id is required")
	}

	link := buildRecommendationApplyLink(recID, accountID)
	data := fmt.Sprintf(
		"Hand off to the user to review and apply this recommendation: %s\n"+
			"Present the recommendation's safety band, blast radius, and estimated savings before sharing the link. "+
			"The user reviews and applies in the optimise UI; do not state that the recommendation has been applied.",
		link,
	)
	return core.NBToolResponse{
		Data:   data,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{
			{Text: "Review and apply", Url: link, Type: "link", Description: "Optimise page for this recommendation"},
		},
	}, nil
}

// buildRecommendationApplyLink builds the optimise deep link that opens a specific
// recommendation. Mirrors the format used by proactive nudges and digests
// (config base URL + /optimise?id=...).
func buildRecommendationApplyLink(recID, accountID string) string {
	base := strings.TrimRight(config.Config.BaseUrl, "/")
	return fmt.Sprintf("%s/optimise?id=%s&accountId=%s#recommendations", base, url.QueryEscape(recID), url.QueryEscape(accountID))
}
