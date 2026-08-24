package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationExecuteCli, func(accountId string) (core.NBTool, error) {
		return RecommendationCliTool{}, nil
	})
}

const ToolRecommendationExecuteCli = "recommendation_execute_cli"

// destructiveCliVerb matches CLI actions that permanently remove or destroy a
// resource, across the aws/az/gcloud verb styles (delete-volume,
// terminate-instances, "vm delete", "keyvault purge", deregister-image) —
// including the abbreviated S3 verbs (rm/rb) and resource release
// (release-address), whose destructive action never spells out "delete".
var destructiveCliVerb = regexp.MustCompile(`(?i)\b(delete|terminate|purge|destroy|remove|deregister|rm|rb|release)\b`)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// commandAction returns the part of a command before its first flag — the
// binary/service/action tokens. The destructive check runs on this head only,
// so a verb inside a flag value (an alarm description, a ticket body, a JSON
// payload) does not trip the gate.
func commandAction(command string) string {
	fields := strings.Fields(command)
	for i, f := range fields {
		if strings.HasPrefix(f, "-") {
			return strings.Join(fields[:i], " ")
		}
	}
	return command
}

// destructiveCommands returns the subset of commands whose action permanently
// removes a resource.
func destructiveCommands(commands []string) []string {
	var out []string
	for _, c := range commands {
		if destructiveCliVerb.MatchString(commandAction(c)) {
			out = append(out, c)
		}
	}
	return out
}

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
			"acknowledge_risk": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Required true for destructive commands (delete/terminate/purge/destroy/remove/deregister). Set it ONLY after presenting the recommendation's safety band, blast radius and estimated savings to the user and receiving their consent to those facts — a bare 'yes' to a question that showed none of them does not qualify.",
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

// ConfirmationQuestion lists the exact commands the approval covers.
func (m RecommendationCliTool) ConfirmationQuestion(toolInput string) string {
	args := confirmationArgs(toolInput)
	rawCommands, _ := args["commands"].([]any)
	commands := make([]string, 0, len(rawCommands))
	for _, c := range rawCommands {
		if s, ok := c.(string); ok && s != "" {
			commands = append(commands, s)
		}
	}
	if len(commands) == 0 {
		return ""
	}
	const maxShown = 5
	shown := commands
	more := ""
	if len(shown) > maxShown {
		more = fmt.Sprintf("\n…and %d more", len(shown)-maxShown)
		shown = shown[:maxShown]
	}
	plural := ""
	if len(commands) > 1 {
		plural = "s"
	}
	linkage := ""
	if recommendationId, _ := args["recommendation_id"].(string); recommendationId != "" {
		linkage = fmt.Sprintf("\n(Registers as recommendation %s's resolution attempt.)", recommendationId)
	}
	warning := ""
	if destructive := destructiveCommands(commands); len(destructive) > 0 {
		warning = "\n⚠ DESTRUCTIVE: permanently removes resources — this cannot be undone."
		joined := strings.ToLower(strings.Join(destructive, " "))
		if strings.Contains(joined, "volume") || strings.Contains(joined, "disk") {
			warning += " Data on a deleted volume/disk is unrecoverable; consider a snapshot first."
		}
	}
	return fmt.Sprintf("Execute %d cloud CLI command%s against this account?\n- %s%s%s\nExecution stops at the first failure.%s Do you want to continue?",
		len(commands), plural, strings.Join(shown, "\n- "), more, warning, linkage)
}

// ToolPrompt is the usage contract agents render into their prompt (the same
// single-source pattern as RecommendationExecuteTool).
func (m RecommendationCliTool) ToolPrompt() []string {
	return []string{
		"**recommendation_execute_cli — destructive commands are gated.** A command whose action is delete/terminate/purge/destroy/remove/deregister is refused unless the call sets `acknowledge_risk: true`. Set it ONLY after you have shown the user the recommendation's safety band, blast radius (dependent_count / production_dependents — when the band is NULL, say impact analysis has not run; never treat missing data as safe) and estimated monthly savings, and they consented to those specific facts. A 'yes' to a question that showed none of them is not consent.",
		"Before deleting a volume or disk, offer a snapshot first — the data is unrecoverable after deletion.",
		"If a command fails with UnauthorizedOperation / AccessDenied: report it and give the user the exact command to run under their own credentials. NEVER advise granting the missing permission to the NudgeBee or collector role — its narrow permissions are a deliberate safety boundary, and widening them defeats the protection that just worked.",
	}
}

// safetyFactsForRefusal fetches the recommendation's safety data so the
// refusal message hands the agent the exact facts it must present, saving a
// second lookup round trip. Best-effort: any failure returns "" and the
// generic refusal still stands.
func safetyFactsForRefusal(nbCtx core.NbToolContext, recommendationId string) string {
	if !uuidPattern.MatchString(recommendationId) {
		return ""
	}
	query := fmt.Sprintf(
		"SELECT rule_name, status, safety_band, safety_reason, dependent_count, production_dependents, estimated_saving FROM recommendation_view WHERE id = '%s'",
		recommendationId)
	_, rows, err := sqlToolCall(nbCtx, query, "recommendation_view", recommendationView, 1, nil)
	if err != nil || len(rows) == 0 {
		return ""
	}
	facts, err := json.Marshal(rows[0])
	if err != nil {
		return ""
	}
	return " — its safety data: " + string(facts)
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

	if destructive := destructiveCommands(commands); len(destructive) > 0 && !boolArg(input, "acknowledge_risk") {
		return errNBLLMToolResponse(fmt.Errorf(
			"REFUSED — destructive command(s) require acknowledged risk: %s. "+
				"Present this recommendation's safety band, blast radius (dependent_count, production_dependents — if the band is NULL say impact analysis has not run) and estimated savings to the user%s, "+
				"get their consent to those specific facts, then re-call with acknowledge_risk=true. For a volume or disk, offer a snapshot before deletion",
			strings.Join(destructive, "; "), safetyFactsForRefusal(nbCtx, stringArg(input, "recommendation_id")))), nil
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
