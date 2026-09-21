package agents

import (
	"regexp"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

// finOpsPromptToolTokenRe matches tool-name-shaped tokens in the rendered
// prompt (snake_case identifiers ending in _execute plus the known families).
var finOpsPromptToolTokenRe = regexp.MustCompile(`\b(?:[a-z0-9_]+_execute|[a-z0-9_]+_execute_cli|spend_[a-z_]+|recommendation_[a-z_]+|propose_[a-z_]+|ticket_master_v2|delegate_agent|anomaly_execute)\b`)

// TestFinOpsPrompt_MentionsOnlyRealTools fails when the FinOps system prompt
// tells the model to call a tool the agent does not actually expose. The
// motivating regression: the metrics routing moved from the raw
// prometheus_execute tool to the metrics agent, the Go side was updated, and
// the prompt kept ordering prometheus_execute in five places — every
// utilization question burned an iteration on tool-not-found before
// recovering. Any future rename must update prompt and tool list together.
func TestFinOpsPrompt_MentionsOnlyRealTools(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &FinOpsAgent{accountId: "test-finops-prompt"}
	prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{})
	flat := flattenAgentPrompt(prompt)

	// The exact tool/agent names FinOps exposes (mirrors GetSupportedTools'
	// static list; MCP and think are dynamic extras the prompt never names).
	exposed := map[string]bool{
		tools.ToolSpendSummary:                         true,
		tools.ToolSpendForecast:                        true,
		tools.ToolSpendAllocation:                      true,
		tools.ToolRecommendationExecuteSql:             true,
		tools.ToolRecommendationResolutionExecuteSql:   true,
		DelegateAgentToolName:                          true,
		MetricsAgentName:                               true,
		tools.ToolExecuteKubectlCommand:                true,
		tools.ToolCloudResourceSearch:                  true,
		tools.ToolAnomalyExecuteSql:                    true,
		tools.ToolProposeRecommendationApply:           true,
		tools.ToolRecommendationApply:                  true,
		tools.ToolRecommendationExecuteCli:             true,
		tools.ToolRecommendationRecordTicketResolution: true,
		tools.TicketMasterToolNameV2:                   true,
	}
	// Referenced in prose as data fields / sub-agent internals, not as tools
	// FinOps calls itself.
	allowedNonTools := map[string]bool{
		// Views and columns from the recommendation_execute ToolPrompt — SQL
		// identifiers, not callable tools.
		"recommendation_view":            true,
		"recommendation_resolution_view": true,
		"recommendation_count":           true,
		"recommendation_id":              true,
		"recommendation_action":          true,
		"recommendation_category":        true,
		"recommendation_data":            true,
		"recommendation_status":          true,
	}

	seen := map[string]bool{}
	for _, tok := range finOpsPromptToolTokenRe.FindAllString(flat, -1) {
		seen[tok] = true
	}
	assert.NotEmpty(t, seen, "prompt should reference its tools by name")

	for tok := range seen {
		if allowedNonTools[tok] || exposed[tok] {
			continue
		}
		// Allow prose plurals/fragments of exposed names (e.g. "spend_summary's").
		base := strings.TrimSuffix(tok, "s")
		if exposed[base] {
			continue
		}
		assert.Fail(t, "prompt references a tool FinOps does not expose",
			"token %q appears in the FinOps system prompt but is not in its tool list — either add the tool or fix the prompt", tok)
	}

	// The historical regression, pinned explicitly.
	assert.NotContains(t, flat, "prometheus_execute",
		"FinOps routes metrics through the metrics agent; prometheus_execute is not in its tool list")
}

// TestFinOpsPrompt_SafetyBandContract pins the safety-band data contract: the
// prompt must direct the model to the recommendations tool's safety_band
// output and forbid substituting other data when it is missing — the live
// failure this fixes was KRR strategy settings presented as a "safety band".
func TestFinOpsPrompt_SafetyBandContract(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &FinOpsAgent{accountId: "test-finops-prompt"}
	flat := flattenAgentPrompt(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))

	assert.Contains(t, flat, "safety_band")
	assert.Contains(t, flat, "production_dependents")
	assert.Contains(t, flat, "Savings sanity check",
		"the savings-vs-spend cross-check constraint must stay in the prompt")
}

// TestFinOpsPrompt_SavingsDedupeContract pins the two rules that keep an
// account-level savings total defensible: alternative purchase options for one
// commitment must be deduplicated (11 EC2 variants summed to $1,257.94 where
// the best single purchase saves $174.89), and the total must be split into
// workload optimizations vs commitment purchases, which are not additive.
func TestFinOpsPrompt_SavingsDedupeContract(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &FinOpsAgent{accountId: "test-finops-prompt"}
	flat := flattenAgentPrompt(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))

	assert.Contains(t, flat, "is_primary_recommendation",
		"savings totals must be filtered to primary rows")
	assert.Contains(t, flat, "dedupe_group",
		"the prompt must explain why alternatives collapse")
	assert.Contains(t, flat, "not additive",
		"the commitment-vs-rightsizing non-additivity caveat must stay")
	assert.Contains(t, flat, "savings_exceeds_spend",
		"the tool-emitted impossible-savings flag must be honoured")
}

// TestFinOpsPrompt_TotalsQuestionSkipsResourceVerification pins the scope of the
// resource-verification layer. A question asking only for a savings total has no
// utilization claim to check, so verifying individual resources cannot change the
// answer — it only spends a kubectl round-trip. The layer stays available for
// questions that name a resource to act on.
func TestFinOpsPrompt_TotalsQuestionSkipsResourceVerification(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &FinOpsAgent{accountId: "test-finops-prompt"}
	flat := flattenAgentPrompt(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))

	assert.Contains(t, flat, "total savings potential",
		"a totals question needs its own layer mapping, or it inherits the optimize-my-spend path")
	assert.Contains(t, flat, "Only for specific named resources",
		"the verification layer must state when it does NOT apply")
}

// TestFinOpsPrompt_DestructiveCliContract pins that the CLI tool's destructive
// gate is explained in the rendered prompt. The tool refuses ungated
// destructive commands regardless, but without the contract in the prompt the
// agent learns the flag from the refusal text mid-conversation instead of
// presenting safety facts up front.
func TestFinOpsPrompt_DestructiveCliContract(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &FinOpsAgent{accountId: "test-finops-prompt"}
	flat := flattenAgentPrompt(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))

	assert.Contains(t, flat, "acknowledge_risk",
		"the destructive-gate flag must be documented in the prompt")
	assert.Contains(t, flat, "NEVER advise granting the missing permission",
		"the UnauthorizedOperation guidance must reach the prompt — advising IAM widening defeats the safety boundary")
	assert.Contains(t, flat, "offer a snapshot first",
		"storage deletion must carry the snapshot-first rule")
}

// TestFinOpsPrompt_CaveatTravelsInsideTheTable pins where the non-additivity
// warning is carried. An orchestrator relaying a FinOps answer keeps tables and
// drops surrounding prose, so a warning that exists only as prose never reaches
// the user — they see a commitment total and a workload total with nothing
// saying the two don't stack.
func TestFinOpsPrompt_CaveatTravelsInsideTheTable(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &FinOpsAgent{accountId: "test-finops-prompt"}
	flat := flattenAgentPrompt(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))

	assert.Contains(t, flat, "right-size first — commitments are sized against current usage",
		"the split table's commitment row must carry the warning verbatim")
	assert.Contains(t, flat, "must live inside the table",
		"the output format must say WHY the warning goes in the table, or the next edit moves it back to prose")
	assert.Contains(t, flat, "Carry that warning inside the split table",
		"the tool-strategy bullet must match the output-format rule")

	recFlat := flattenAgentPrompt(newRecommendationAgent("test-recommendations-prompt").
		GetSystemPrompt(ctx, core.NBAgentRequest{}))
	assert.Contains(t, recFlat, "in-table placement survives",
		"the recommendations agent's citation must stay in the table for the same relay reason")
}

// TestRecommendationsPrompt_AggregateIsTheAnswer pins the rule that stops the
// most expensive query this agent runs. Having computed an aggregate that
// answers the question, the agent was following it with an unlimited
// ORDER BY over the account's recommendations, which returns nothing the
// aggregate had not already stated.
func TestRecommendationsPrompt_AggregateIsTheAnswer(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newRecommendationAgent("test-recommendations-prompt")
	flat := flattenAgentPrompt(agent.GetSystemPrompt(ctx, core.NBAgentRequest{}))

	assert.Contains(t, flat, "An aggregate is the answer",
		"the agent must be told to stop once an aggregate answers the question")
	assert.Contains(t, flat, "always with a LIMIT",
		"row listings must carry a limit")
}

// TestFinOpsAndRecommendationsShareOneSQLContract pins the anti-drift property
// of the un-nesting: FinOps queries recommendation_view directly now, so the
// SQL rules and schema it renders must be the very lines the recommendations
// agent renders — both consume the tools' ToolPrompt(). If either side stops
// doing so, the two paths to the same data drift apart again.
func TestFinOpsAndRecommendationsShareOneSQLContract(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	finops := flattenAgentPrompt((&FinOpsAgent{accountId: "test-finops-prompt"}).GetSystemPrompt(ctx, core.NBAgentRequest{}))
	recs := flattenAgentPrompt(newRecommendationAgent("test-finops-prompt").GetSystemPrompt(ctx, core.NBAgentRequest{}))

	for _, line := range (tools.RecommendationExecuteTool{}).ToolPrompt() {
		assert.Contains(t, finops, line, "FinOps must render every recommendation_execute ToolPrompt line")
		assert.Contains(t, recs, line, "the recommendations agent must render every recommendation_execute ToolPrompt line")
	}
	for _, line := range (tools.RecommendationResolutionExecuteTool{}).ToolPrompt() {
		assert.Contains(t, finops, line)
		assert.Contains(t, recs, line)
	}

	// The un-nesting itself: FinOps carries the raw tools, not the sub-agent.
	names := map[string]bool{}
	for _, tool := range (&FinOpsAgent{accountId: "test-finops-prompt"}).GetSupportedTools(ctx) {
		names[tool.Name()] = true
	}
	assert.True(t, names[tools.ToolRecommendationExecuteSql])
	assert.True(t, names[tools.ToolRecommendationResolutionExecuteSql])
	assert.False(t, names[RecommendationsAgentName],
		"the nested recommendations agent must be gone from FinOps, or every savings answer is composed twice")
}
