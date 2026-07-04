package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// agent_cost_optimizer.go is the cost_optimizer agent — the "export & analyze"
// action behind the Cost Analyser button. It is a CUSTOM-planner agent (like
// events_rca_report): no ReWOO/ReAct pipeline. Execute() does the deterministic
// work first (profile the conversation's cost/flow + compute retry/failure waste
// in Go), then makes a single grounded LLM call for the judgment findings
// (model downgrades, redundant agents), with every dollar recomputed server-side.
// It runs through the normal /chat path; the target conversation's session id is
// passed as the query.

const AgentCostOptimizerName = "cost_optimizer"

type AgentCostOptimizer struct{}

func (a AgentCostOptimizer) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}
func (a AgentCostOptimizer) GetName() string          { return AgentCostOptimizerName }
func (a AgentCostOptimizer) GetNameAliases() []string { return []string{"Cost Optimizer"} }

func (a AgentCostOptimizer) GetDescription() string {
	return "Analyzes a finished conversation's cost and execution flow and recommends how to run it cheaper — which model calls could use a lighter model, which agents were redundant, and where retries/failures wasted spend. Provide the conversation's session id as the query."
}

func (a AgentCostOptimizer) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (a AgentCostOptimizer) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

// Execute profiles the target conversation, runs the optimizer, and returns a
// markdown report. The target session id is the query (a leading @mention, if
// any, is stripped). Account scope comes from the chat request (already authed).
func (a AgentCostOptimizer) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	sessionID := stripLeadingMention(request.Query)
	if sessionID == "" {
		return core.NBAgentResponse{
			Response:       []string{"Provide the conversation's session id to analyze (e.g. `@cost_optimizer <session_id>`)."},
			AgentName:      a.GetName(),
			SessionId:      request.SessionId,
			ConversationId: request.ConversationId,
			Status:         core.ConversationStatusCompleted,
		}, nil
	}

	userID := ctx.GetSecurityContext().GetUserId()
	// request.ConversationId / MessageId are THIS optimizer conversation
	// (session=optimizer-<target>); the analysis call's token usage is tracked
	// against them. sessionID is the TARGET conversation being analyzed.
	opt, err := core.GenerateConversationOptimization(ctx, sessionID, request.AccountId, userID, request.ConversationId, request.MessageId)
	if err != nil {
		return core.NBAgentResponse{}, err
	}

	// Return the structured result as JSON so the Cost Analyser dashboard can render
	// the report card. (These optimizer conversations are source=Optimize and hidden
	// from the analyser, so a JSON body in the transcript is fine.)
	payload, err := json.Marshal(opt)
	if err != nil {
		return core.NBAgentResponse{}, fmt.Errorf("cost_optimizer: marshal result: %w", err)
	}

	return core.NBAgentResponse{
		Response:       []string{string(payload)},
		AgentName:      a.GetName(),
		SessionId:      request.SessionId,
		ConversationId: request.ConversationId,
		Status:         core.ConversationStatusCompleted,
	}, nil
}

// stripLeadingMention drops a leading "@agent_name" token and trims the rest, so
// "@cost_optimizer <id>" and "<id>" both yield the bare id.
func stripLeadingMention(q string) string {
	q = strings.TrimSpace(q)
	if strings.HasPrefix(q, "@") {
		if i := strings.IndexAny(q, " \t\n"); i >= 0 {
			q = strings.TrimSpace(q[i+1:])
		} else {
			q = "" // only a mention, no id
		}
	}
	return q
}

func init() {
	core.RegisterNBAgentFactory(AgentCostOptimizerName, func(accountId string) (core.NBAgent, error) {
		return &AgentCostOptimizer{}, nil
	})
}
