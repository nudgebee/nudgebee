package core

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/prompts"

	"nudgebee/llm/config"

	"nudgebee/llm/security"
)

type depthTestChildKey struct{}

var (
	testQuickDepth = &DepthPolicy{Level: "quick", MaxIterations: 35, SubAgentMaxIterations: 8, MaxRefinements: 0, Direction: BudgetLowers}
	testDeepDepth  = &DepthPolicy{Level: "deep", MaxIterations: 80, SubAgentMaxIterations: 15, MaxRefinements: 2, Direction: BudgetRaises}
)

// Without a resolver (the OSS build) no run gets a depth; with one, its
// answer is what the run gets.
func TestResolveInvestigationDepthHook(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	old := investigationDepthResolver
	t.Cleanup(func() { investigationDepthResolver = old })

	SetInvestigationDepthResolver(nil)
	assert.Nil(t, resolveInvestigationDepth(ctx, NBAgentRequest{}, &MockAgent{}))

	var got NBAgentRequest
	SetInvestigationDepthResolver(func(_ *security.RequestContext, request NBAgentRequest, _ NBAgent) *DepthPolicy {
		got = request
		return testQuickDepth
	})
	assert.Same(t, testQuickDepth, resolveInvestigationDepth(ctx, NBAgentRequest{CallerSource: ConversationSourceAutomation}, &MockAgent{}))
	assert.Equal(t, ConversationSourceAutomation, got.CallerSource)
}

func TestInvestigationDepthContext(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	assert.Same(t, ctx, WithInvestigationDepth(ctx, nil), "standard leaves ctx untouched")
	assert.Nil(t, InvestigationDepthOf(ctx))

	quick := testQuickDepth
	governed := WithInvestigationDepth(ctx, quick)
	assert.Equal(t, quick, InvestigationDepthOf(governed))
	child := governed.WithContext(context.WithValue(governed.GetContext(), depthTestChildKey{}, "x"))
	assert.Equal(t, quick, InvestigationDepthOf(child), "sub-agent contexts inherit the run's depth")
}

// TestStandardDepthChangesNothing is the guard for "Standard, and the OSS
// build, are byte-identical to today": with no policy on ctx, every knob depth touches keeps its value.
func TestStandardDepthChangesNothing(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	top := NBAgentRequest{AgentId: "a"}
	sub := NBAgentRequest{AgentId: "a", ParentAgentId: "p"}
	for _, current := range []int{1, 10, 50, 77} {
		assert.Equal(t, current, depthMaxIterations(ctx, &MockAgent{}, top, current))
		assert.Equal(t, current, depthMaxIterations(ctx, &MockAgent{}, sub, current))
	}
	assert.Equal(t, 2, depthRefinementBudget(ctx, 2))
	assert.Equal(t, 0, depthRefinementBudget(ctx, 0))
}

func TestDepthMaxIterations(t *testing.T) {
	base := security.NewRequestContextForTenantAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	quick, deep := testQuickDepth, testDeepDepth
	top := NBAgentRequest{AgentId: "a"}
	sub := NBAgentRequest{AgentId: "a", ParentAgentId: "p"}

	assert.Equal(t, 35, depthMaxIterations(WithInvestigationDepth(base, quick), &MockAgent{}, top, 50))
	assert.Equal(t, 8, depthMaxIterations(WithInvestigationDepth(base, quick), &MockAgent{}, sub, 10))
	assert.Equal(t, 80, depthMaxIterations(WithInvestigationDepth(base, deep), &MockAgent{}, top, 50), "deep raises the default budget")
	assert.Equal(t, 15, depthMaxIterations(WithInvestigationDepth(base, deep), &MockAgent{}, sub, 10))
	assert.Equal(t, 20, depthMaxIterations(WithInvestigationDepth(base, quick), &MockAgent{}, top, 20), "quick never runs longer than a lower configured default")
	assert.Equal(t, 5, depthMaxIterations(WithInvestigationDepth(base, quick), &MockAgent{}, sub, 5))
	assert.Equal(t, 120, depthMaxIterations(WithInvestigationDepth(base, deep), &MockAgent{}, top, 120), "deep never runs shorter than a higher configured default")
	assert.Equal(t, 20, depthMaxIterations(WithInvestigationDepth(base, deep), &MockAgent{}, sub, 20))
	noDirection := &DepthPolicy{Level: "custom", MaxIterations: 99, SubAgentMaxIterations: 99}
	assert.Equal(t, 50, depthMaxIterations(WithInvestigationDepth(base, noDirection), &MockAgent{}, top, 50), "a policy without a direction keeps the configured budget")

	own := &MockIterAgent{maxIterations: 12}
	assert.Equal(t, 12, depthMaxIterations(WithInvestigationDepth(base, deep), own, top, 12), "an agent's own budget is never raised")
	assert.Equal(t, 12, depthMaxIterations(WithInvestigationDepth(base, quick), own, top, 12), "or lowered")

	assert.Equal(t, 0, depthRefinementBudget(WithInvestigationDepth(base, quick), 2), "quick skips the answer check")
	assert.Equal(t, 2, depthRefinementBudget(WithInvestigationDepth(base, deep), 2))
}

// Quick's zero refinement budget must skip the answer check even when the
// final answer comes from the summary tool, which the regular budget gate
// lets through to the critique when claim feedback is off.
func TestReAct3ZeroRefinementBudgetSkipsCritiqueOnSummaryAnswer(t *testing.T) {
	previous := config.Config.ClaimCritiqueFeedbackEnabled
	config.Config.ClaimCritiqueFeedbackEnabled = false
	t.Cleanup(func() { config.Config.ClaimCritiqueFeedbackEnabled = previous })
	fake := &fakeLLMModel{turns: []fakeTurn{
		{content: "<decision>refine</decision><feedback>check more</feedback>"},
	}}
	withFakeLLMModel(t, fake)
	ctx := llmOverrideContext()
	p := &NBReActPlanner3{ctx: ctx, request: NBAgentRequest{Query: "why is CPU high"}, nbAgent: notebookOptOutAgent{},
		enableCritique: true, summaryToolName: "summary", maxRefinementAttempts: 0,
		prompt: prompts.NewPromptTemplate("{{.input}}", []string{"input"})}
	steps := []NBAgentPlannerToolActionStep{{Action: NBAgentPlannerToolAction{Tool: "summary"}, Observation: "CPU high"}}

	actions, finish, err := p.Plan(ctx.GetContext(), steps, "why is CPU high")
	require.NoError(t, err)
	require.Empty(t, actions)
	require.NotNil(t, finish)
	assert.Equal(t, "CPU high", finish.Data, "the summary's output is the answer")
	assert.Zero(t, fake.callCount(), "no critique call with a zero budget")
	assert.Zero(t, p.refinementAttempts)
}

type messageConfigDAO struct {
	IConversationDao
	config string
}

func (d *messageConfigDAO) GetLatestMessageConfig(string) (string, error) {
	return d.config, nil
}

// The chat's depth pick is read back from the latest message config, for the
// resume path and for the picker on reopen.
func TestLatestMessageConfigCarriesDepthPick(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	previous := GetConversationDao()
	t.Cleanup(func() { SetConversationDao(previous) })

	SetConversationDao(&messageConfigDAO{config: `{"investigation_depth":"deep","read_only_run":true}`})
	got, ok := LatestMessageConfig(ctx, "conv")
	require.True(t, ok)
	assert.Equal(t, "deep", got.InvestigationDepth)
	assert.False(t, got.ReadOnlyRun, "per-turn flags are not inherited")

	SetConversationDao(&messageConfigDAO{})
	_, ok = LatestMessageConfig(ctx, "conv")
	assert.False(t, ok, "no saved config, nothing to carry over")
}

// One row from the latest non-queued generation message, not the whole
// conversation: this runs on every chat open.
func TestGetLatestMessageConfigQuery(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	const query = `SELECT message_config FROM llm_conversation_messages\s+WHERE conversation_id = \$1 AND message_type = \$2 AND status IS DISTINCT FROM \$3\s+AND message_config IS NOT NULL AND message_config <> ''\s+ORDER BY created_at DESC LIMIT 1`

	mock.ExpectQuery(query).WithArgs("conv", MessageTypeGeneration, ConversationStatusQueued).
		WillReturnRows(sqlmock.NewRows([]string{"message_config"}).AddRow(`{"investigation_depth":"quick"}`))
	got, err := dao.GetLatestMessageConfig("conv")
	require.NoError(t, err)
	assert.Equal(t, `{"investigation_depth":"quick"}`, got)

	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"message_config"}))
	got, err = dao.GetLatestMessageConfig("conv")
	require.NoError(t, err, "no row is not an error")
	assert.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}
