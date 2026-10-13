package core

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/llms/decision"
	"nudgebee/llm/security"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func pilotTask() ClassificationTask {
	t := intentTask()
	t.Name = "workflow_turn_intent"
	return t
}

func decisionSlot(minConfidence string) map[string]string {
	return map[string]string{
		"llm_tier_provider_decision":     decision.ProviderCloudflare,
		"llm_tier_model_decision":        "clef-flash",
		"llm_tier_api_key_decision":      "token",
		"llm_tier_api_endpoint_decision": "acct",
		llmDecisionMinConfidenceKey:      minConfidence,
	}
}

// stubDecisionConfig makes decision settings resolve to cfg.
func stubDecisionConfig(t *testing.T, cfg map[string]string) {
	t.Helper()
	old := decisionConfigSource
	decisionConfigSource = func(*security.RequestContext, string, string, ModelTier) (map[string]string, string) {
		return cfg, "db"
	}
	t.Cleanup(func() { decisionConfigSource = old })
}

// stubDecisionChooser scripts the decision model and records the state it was sent.
func stubDecisionChooser(t *testing.T, choice decision.Choice, err error) *[]string {
	t.Helper()
	states := &[]string{}
	old := decisionChooser
	decisionChooser = func(_ context.Context, _ *decisionBackend, state, _ string, _ []decision.Option) (decision.Choice, error) {
		*states = append(*states, state)
		return choice, err
	}
	t.Cleanup(func() { decisionChooser = old })
	return states
}

func conf(v float64) *float64 { return &v }

func TestParseDecisionMinConfidence(t *testing.T) {
	v, err := ParseDecisionMinConfidence("")
	require.NoError(t, err)
	assert.Equal(t, defaultDecisionMinConfidence, v, "empty uses the default")
	v, err = ParseDecisionMinConfidence(" 0.75 ")
	require.NoError(t, err)
	assert.Equal(t, 0.75, v)
	for _, raw := range []string{"0", "1.5", "-0.1", "high"} {
		_, err := ParseDecisionMinConfidence(raw)
		assert.ErrorContains(t, err, "above 0 and at most 1", raw)
	}
}

func TestDecisionBackendFor(t *testing.T) {
	ctx := testRequestContext()

	stubDecisionConfig(t, decisionSlot(""))
	b, ok := decisionBackendFor(ctx, ClassificationCall{AccountId: "acct"}, pilotTask())
	require.True(t, ok, "a configured decision model is used without further settings")
	assert.Equal(t, "clef-flash", b.model)
	assert.Equal(t, defaultDecisionMinConfidence, b.minConfidence)

	stubDecisionConfig(t, decisionSlot("0.8"))
	b, ok = decisionBackendFor(ctx, ClassificationCall{}, pilotTask())
	require.True(t, ok)
	assert.Equal(t, 0.8, b.minConfidence)

	chat := decisionSlot("")
	chat["llm_tier_provider_decision"] = "googleai"
	stubDecisionConfig(t, chat)
	_, ok = decisionBackendFor(ctx, ClassificationCall{}, pilotTask())
	assert.False(t, ok, "a chat provider in the decision slot is not a decision model")

	openaiChat := decisionSlot("")
	openaiChat["llm_tier_provider_decision"], openaiChat["llm_tier_model_decision"] = decision.ProviderOpenAI, "gpt-4o"
	stubDecisionConfig(t, openaiChat)
	_, ok = decisionBackendFor(ctx, ClassificationCall{}, pilotTask())
	assert.False(t, ok, "an OpenAI chat model in the decision slot is not a decision model")

	for provider, model := range map[string]string{decision.ProviderTypeSafe: "jev-latest", decision.ProviderOpenAI: "gpt-6-luna"} {
		slot := decisionSlot("")
		slot["llm_tier_provider_decision"], slot["llm_tier_model_decision"] = provider, model
		stubDecisionConfig(t, slot)
		b, ok = decisionBackendFor(ctx, ClassificationCall{}, pilotTask())
		require.True(t, ok, provider)
		assert.Equal(t, model, b.model)
	}

	stubDecisionConfig(t, decisionSlot("2"))
	_, ok = decisionBackendFor(ctx, ClassificationCall{}, pilotTask())
	assert.False(t, ok, "an invalid threshold switches the decision model off")

	stubDecisionConfig(t, map[string]string{"llm_provider": "googleai"})
	_, ok = decisionBackendFor(ctx, ClassificationCall{}, pilotTask())
	assert.False(t, ok, "no decision model configured")
}

func TestClassify_DecisionBackend(t *testing.T) {
	cases := []struct {
		name        string
		choice      decision.Choice
		chooseErr   error
		wantBackend string
		wantLabel   string
		wantLLM     bool
		wantConf    float64
	}{
		{"confident answer is used", decision.Choice{Choice: "EDIT", Confidence: conf(0.95), Model: "clef-flash-1"}, nil, "decision", "EDIT", false, 0.95},
		{"probability stands in for a missing confidence", decision.Choice{Choice: "edit", Probabilities: map[string]float64{"edit": 0.9, "ANSWER": 0.1}}, nil, "decision", "EDIT", false, 0.9},
		{"low confidence goes to the LLM", decision.Choice{Choice: "EDIT", Confidence: conf(0.6)}, nil, "llm", "ANSWER", true, 0.6},
		{"an answer that is not an option goes to the LLM", decision.Choice{Choice: "DELETE", Confidence: conf(0.99)}, nil, "llm", "ANSWER", true, 0.99},
		{"a provider error goes to the LLM", decision.Choice{}, errors.New("HTTP 401"), "llm", "ANSWER", true, 0},
		{"a refusal goes to the LLM", decision.Choice{Refused: true, Model: "gpt-6-luna"}, nil, "llm", "ANSWER", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubDecisionConfig(t, decisionSlot("0.8"))
			stubDecisionChooser(t, tc.choice, tc.chooseErr)
			llmCalls := stubGenerate(t, []string{"ANSWER"}, nil)
			d, err := Classify(testRequestContext(), ClassificationCall{AccountId: "acct"}, pilotTask(), "fix it")
			require.NoError(t, err)
			assert.Equal(t, tc.wantBackend, d.Backend)
			assert.Equal(t, tc.wantLabel, d.Label())
			assert.Equal(t, tc.wantLLM, len(*llmCalls) == 1)
			assert.Equal(t, tc.wantLLM, d.FellBack)
			if tc.chooseErr == nil {
				require.NotNil(t, d.Confidence)
				assert.InDelta(t, tc.wantConf, *d.Confidence, 1e-9)
			}
		})
	}
}

// The decision model is never asked when nothing is configured, so the LLM
// path is unchanged.
func TestClassify_NoDecisionConfigured(t *testing.T) {
	stubDecisionConfig(t, nil)
	states := stubDecisionChooser(t, decision.Choice{Choice: "EDIT", Confidence: conf(1)}, nil)
	stubGenerate(t, []string{"CREATE"}, nil)
	d, err := Classify(testRequestContext(), ClassificationCall{}, pilotTask(), "make one")
	require.NoError(t, err)
	assert.Equal(t, "llm", d.Backend)
	assert.Equal(t, "CREATE", d.Label())
	assert.False(t, d.FellBack)
	assert.Nil(t, d.Confidence)
	assert.Empty(t, *states)
}

// The decision call goes through the same outbound decorators as an LLM call,
// so the scrub/egress layers see and can rewrite its text.
func TestClassify_DecisionInputIsDecorated(t *testing.T) {
	old := LLMModelDecorator
	LLMModelDecorator = func(m llms.Model) llms.Model { return scrubbingModel{inner: m} }
	t.Cleanup(func() { LLMModelDecorator = old })
	stubDecisionConfig(t, decisionSlot("0.8"))
	states := stubDecisionChooser(t, decision.Choice{Choice: "EDIT", Confidence: conf(0.9)}, nil)

	_, err := Classify(testRequestContext(), ClassificationCall{}, pilotTask(), "token PLAIN-SECRET-VALUE is failing")
	require.NoError(t, err)
	require.Len(t, *states, 1)
	assert.Equal(t, "token [REDACTED] is failing", (*states)[0])
}

type scrubbingModel struct{ inner llms.Model }

func (s scrubbingModel) GenerateContent(ctx context.Context, msgs []llms.MessageContent, opts ...llms.CallOption) (*llms.ContentResponse, error) {
	out := make([]llms.MessageContent, len(msgs))
	for i, m := range msgs {
		parts := make([]llms.ContentPart, len(m.Parts))
		for j, p := range m.Parts {
			if t, ok := p.(llms.TextContent); ok {
				p = llms.TextContent{Text: strings.ReplaceAll(t.Text, "PLAIN-SECRET-VALUE", "[REDACTED]")}
			}
			parts[j] = p
		}
		out[i] = llms.MessageContent{Role: m.Role, Parts: parts}
	}
	return s.inner.GenerateContent(ctx, out, opts...)
}

func (s scrubbingModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("not used")
}

func TestParseConfigSourceId_RejectsDecisionSlot(t *testing.T) {
	for _, id := range []string{"env:tier:decision", "db:11111111-1111-1111-1111-111111111111:tier:decision"} {
		_, err := parseConfigSourceId(id)
		assert.ErrorContains(t, err, "cannot be pinned", id)
	}
	_, err := parseConfigSourceId("env:tier:classification")
	assert.NoError(t, err)
}

// decisionConfigSource follows ResolveLLMConfig's pin precedence for the
// fallback's tier, so a pinned conversation's text never reaches a decision
// model configured on another integration.
func TestDecisionConfigSource_FollowsPins(t *testing.T) {
	const pinned = "11111111-1111-1111-1111-111111111111"
	oldPin, oldAccount := decisionPinConfig, decisionAccountConfig
	t.Cleanup(func() { decisionPinConfig, decisionAccountConfig = oldPin, oldAccount })
	decisionPinConfig = func(_ *security.RequestContext, _, _, uuid string) (map[string]string, error) {
		return map[string]string{"from": "pin:" + uuid}, nil
	}
	decisionAccountConfig = func(*security.RequestContext, string) (map[string]string, error) {
		return map[string]string{"from": "account"}, nil
	}
	ctxWith := func(key, value any) *security.RequestContext {
		goCtx := context.Background()
		if key != nil {
			goCtx = context.WithValue(goCtx, key, value)
		}
		return security.NewRequestContext(goCtx, security.NewRequestContextForSuperAdmin().GetSecurityContext(), slog.Default(), nil, nil)
	}
	byTask := ConversationTierOverrides{Picks: map[string]TierModelPick{
		string(ModelTierRetrieval): {Model: "m", ConfigSource: "db:" + pinned + ":tier:retrieval"},
	}}

	cases := []struct {
		name string
		ctx  *security.RequestContext
		tier ModelTier
		want string
	}{
		{"per-task pick for the fallback's tier", ctxWith(ContextKeyLlmTierModelOverrides, byTask), ModelTierRetrieval, "pin:" + pinned},
		{"a pick for another tier does not apply", ctxWith(ContextKeyLlmTierModelOverrides, byTask), ModelTierSummary, "account"},
		{"request pin", ctxWith(ContextKeyLlmConfigSourceOverride, "db:"+pinned), ModelTierRetrieval, "pin:" + pinned},
		{"no pin uses the account integration", ctxWith(nil, nil), ModelTierRetrieval, "account"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := decisionConfigSource(tc.ctx, "acct", "", tc.tier)
			assert.Equal(t, tc.want, cfg["from"])
		})
	}

	cfg, source := decisionConfigSource(ctxWith(ContextKeyLlmConfigSourceOverride, "db:"+pinned+":tier:decision"), "acct", "", ModelTierRetrieval)
	assert.Nil(t, cfg, "an invalid pin turns decisions off rather than falling back to the account")
	assert.Equal(t, "pin-invalid", source)

	decisionPinConfig = func(*security.RequestContext, string, string, string) (map[string]string, error) {
		return nil, errors.New("not linked")
	}
	cfg, source = decisionConfigSource(ctxWith(ContextKeyLlmConfigSourceOverride, "db:"+pinned), "acct", "", ModelTierRetrieval)
	assert.Nil(t, cfg, "an unavailable pinned integration turns decisions off")
	assert.Equal(t, "pin-unavailable", source)
}

// A decision-only provider can never serve an LLM call.
func TestGetLLMModel_RefusesDecisionProvider(t *testing.T) {
	for _, pm := range [][2]string{
		{decision.ProviderCloudflare, "clef"},
		{decision.ProviderTypeSafe, "jev-latest"},
	} {
		_, err := GetLLMModel(pm[0], pm[1], "", false, "")
		assert.ErrorContains(t, err, "decision model and cannot serve LLM calls", pm[0])
	}
}

func approvalTask() ClassificationTask {
	return ClassificationTask{
		Name:         "tool_request_type",
		Instructions: "Classify the command.",
		Options:      []ClassificationOption{{Label: "read"}, {Label: "create"}, {Label: "update"}, {Label: "delete"}},
	}
}

// setDecisionMode sets the server setting llm_decision_mode_<task> for a test.
func setDecisionMode(t *testing.T, task, mode string) {
	t.Helper()
	viper.Set("llm_decision_mode_"+task, mode)
	t.Cleanup(func() { viper.Set("llm_decision_mode_"+task, "") })
}

func TestDecisionModeFor(t *testing.T) {
	ctx := testRequestContext()
	assert.Equal(t, decisionModeOn, decisionModeFor(ctx, pilotTask()), "the builder defaults to on")
	assert.Equal(t, decisionModeOn, decisionModeFor(ctx, approvalTask()), "the approval check defaults to on")
	assert.Equal(t, decisionModeOff, decisionModeFor(ctx, intentTask()), "a decision not listed is off")
	multi := pilotTask()
	multi.Multi = true
	assert.Equal(t, decisionModeOff, decisionModeFor(ctx, multi), "decision models answer single-choice questions only")

	setDecisionMode(t, "tool_request_type", " SHADOW ")
	assert.Equal(t, decisionModeShadow, decisionModeFor(ctx, approvalTask()), "the server setting overrides the default")
	setDecisionMode(t, "workflow_turn_intent", "off")
	assert.Equal(t, decisionModeOff, decisionModeFor(ctx, pilotTask()))
	setDecisionMode(t, "workflow_turn_intent", "sometimes")
	assert.Equal(t, decisionModeOn, decisionModeFor(ctx, pilotTask()), "an invalid setting keeps the default")
}

// In shadow mode the LLM decides; the decision model is asked afterwards and
// only logged, however confident it is.
func TestClassify_ShadowModeNeverChangesTheDecision(t *testing.T) {
	old := runShadow
	runShadow = func(f func()) { f() }
	t.Cleanup(func() { runShadow = old })
	setDecisionMode(t, "tool_request_type", "shadow")

	stubDecisionConfig(t, decisionSlot("0.8"))
	states := stubDecisionChooser(t, decision.Choice{Choice: "read", Confidence: conf(0.99)}, nil)
	llmCalls := stubGenerate(t, []string{"update"}, nil)
	d, err := Classify(testRequestContext(), ClassificationCall{AccountId: "acct"}, approvalTask(), "kubectl get pods")
	require.NoError(t, err)
	assert.Equal(t, "llm", d.Backend)
	assert.Equal(t, "update", d.Label(), "the LLM's answer is used, not the decision model's")
	assert.False(t, d.FellBack)
	assert.Len(t, *llmCalls, 1)
	assert.Len(t, *states, 1, "the decision model is still asked, for the log")
}

func TestClassify_ModeOff(t *testing.T) {
	setDecisionMode(t, "workflow_turn_intent", "off")
	stubDecisionConfig(t, decisionSlot("0.8"))
	states := stubDecisionChooser(t, decision.Choice{Choice: "EDIT", Confidence: conf(1)}, nil)
	stubGenerate(t, []string{"CREATE"}, nil)
	d, err := Classify(testRequestContext(), ClassificationCall{}, pilotTask(), "make one")
	require.NoError(t, err)
	assert.Equal(t, "llm", d.Backend)
	assert.Empty(t, *states, "off never asks the decision model")
}

// agentNameDAO answers the agent-name lookup for decisionAgentName.
type agentNameDAO struct {
	IConversationDao
	names map[string]string
}

func (d *agentNameDAO) GetAgentNameFromAgentId(agentId string) (string, error) {
	if name, ok := d.names[agentId]; ok {
		return name, nil
	}
	return "", errors.New("not found")
}

// The usage row is written on the decision tier even when the caller runs on
// another one, so the decision call is not counted as that tier's usage.
func TestDecisionUsageContext_UsesDecisionTier(t *testing.T) {
	base := testRequestContext()
	caller := base.WithContext(context.WithValue(base.GetContext(), ContextKeyModelTier, ModelTierReasoning))
	tier, _ := tierAttributionForRecord(decisionUsageContext(caller))
	require.NotNil(t, tier)
	assert.Equal(t, string(ModelTierDecision), *tier)
}

func TestDecisionAgentName(t *testing.T) {
	const known, unknown = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	previous := PeekConversationDao()
	SetConversationDao(&agentNameDAO{names: map[string]string{known: "k8s_orchestrator"}})
	t.Cleanup(func() { SetConversationDao(previous) })

	assert.Equal(t, "k8s_orchestrator", decisionAgentName(known))
	assert.Equal(t, unknown, decisionAgentName(unknown), "an unknown id is kept")
	assert.Equal(t, "auth_agent", decisionAgentName("auth_agent"), "a name is kept")
	assert.Equal(t, "", decisionAgentName(""))
}

// On a fallback the log reports the decision model's own time next to the total.
func TestClassify_FallbackKeepsDecisionLatency(t *testing.T) {
	stubDecisionConfig(t, decisionSlot("0.8"))
	old := decisionChooser
	decisionChooser = func(context.Context, *decisionBackend, string, string, []decision.Option) (decision.Choice, error) {
		time.Sleep(5 * time.Millisecond)
		return decision.Choice{Choice: "EDIT", Confidence: conf(0.5)}, nil
	}
	t.Cleanup(func() { decisionChooser = old })
	stubGenerate(t, []string{"ANSWER"}, nil)

	d, err := Classify(testRequestContext(), ClassificationCall{AccountId: "acct"}, pilotTask(), "fix it")
	require.NoError(t, err)
	require.True(t, d.FellBack)
	assert.GreaterOrEqual(t, d.DecisionLatency, 5*time.Millisecond)
	assert.LessOrEqual(t, d.DecisionLatency, d.Latency)
}
