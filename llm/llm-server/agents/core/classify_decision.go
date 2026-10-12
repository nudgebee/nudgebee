package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/decision"
	"nudgebee/llm/security"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

// Decision-model backend for Classify. A decision model picks one option and
// returns a probability for each; when it is confident enough its answer is
// used, otherwise the decision goes to the LLM backend as before.
//
// Configuration lives in the LLM configuration (integration or ENV), in the
// "decision" slot: llm_tier_provider_decision (e.g. cloudflare),
// llm_tier_model_decision, llm_tier_api_key_decision,
// llm_tier_api_endpoint_decision (Cloudflare account id or base URL) and
// llm_tier_min_confidence_decision (default 0.9). A configured decision model
// is used by every decision in decisionPilotTasks.
//
// It is read from the configuration the call's LLM fallback would use: the
// conversation's pin, otherwise the account's LLM integration, otherwise ENV.

// ModelTierDecision is the configuration slot of the decision model. It is not
// a model tier: no chat call resolves through it and it cannot be pinned.
const ModelTierDecision ModelTier = "decision"

const (
	classificationBackendDecision = "decision"
	llmDecisionMinConfidenceKey   = "llm_tier_min_confidence_decision"
	// defaultDecisionMinConfidence applies when no minimum confidence is set.
	defaultDecisionMinConfidence = 0.9
	// decisionTimeout bounds the decision call so a slow provider does not add
	// much latency before the LLM fallback.
	decisionTimeout = 2 * time.Second
)

// decisionPilotTasks are the decisions that use a configured decision model.
// The tool approval check (tool_request_type) is deliberately absent: a
// confident wrong "read" would skip user approval, so it stays on the LLM until
// a labelled set with injection cases shows no false "read" at a very high
// threshold (#40902). Adding a decision here turns it on for every
// configuration that has a decision model, so a riskier decision needs its own
// opt-in first.
var decisionPilotTasks = map[string]bool{
	"workflow_turn_intent": true,
}

// ParseDecisionMinConfidence reads llm_tier_min_confidence_decision: empty means
// the default; anything else must be above 0 and at most 1.
func ParseDecisionMinConfidence(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultDecisionMinConfidence, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || v > 1 {
		return 0, fmt.Errorf("%s must be a number above 0 and at most 1", llmDecisionMinConfidenceKey)
	}
	return v, nil
}

// decisionBackend is a configured decision model for one task.
type decisionBackend struct {
	provider      string
	model         string
	apiKey        string
	endpoint      string
	minConfidence float64
	source        string // "db", "db:<integration>" or "env", for logs
}

// decisionConfigSource returns the configuration the decision settings are read
// from, and a label for logs. It follows the same pin precedence as
// ResolveLLMConfig for the tier the LLM fallback runs on: that tier's per-task
// pick, then the request pin, then the conversation pin. A pinned call uses that
// integration only, so its text never reaches a decision model configured
// elsewhere; an unpinned call uses the account's LLM integration, or ENV.
var decisionConfigSource = func(ctx *security.RequestContext, accountId, conversationId string, tier ModelTier) (map[string]string, string) {
	pin, _ := effectivePin(ctx, conversationId, tier)
	if pin != "" {
		p, err := parseConfigSourceId(pin)
		if err != nil {
			return nil, "pin-invalid"
		}
		if p.Layer == "env" {
			return envDecisionConfig(), "env"
		}
		cfg, err := decisionPinConfig(ctx, accountId, "", p.IntegrationUuid)
		if err != nil {
			return nil, "pin-unavailable"
		}
		return cfg, "db:" + p.IntegrationUuid
	}
	if accountId != "" {
		if cfg, err := decisionAccountConfig(ctx, accountId); err == nil && cfg != nil {
			return cfg, "db"
		}
	}
	return envDecisionConfig(), "env"
}

// Config readers behind decisionConfigSource; variables so tests can stub the DB.
var (
	decisionPinConfig     = integrationConfigForPin
	decisionAccountConfig = func(ctx *security.RequestContext, accountId string) (map[string]string, error) {
		return getLLMIntegrationConfig(ctx, accountId)
	}
)

func envDecisionConfig() map[string]string {
	cfg := map[string]string{}
	for _, key := range []string{
		fmt.Sprintf(llmTierProviderFormat, ModelTierDecision),
		fmt.Sprintf(llmTierModelFormat, ModelTierDecision),
		fmt.Sprintf(llmTierApiKeyFormat, ModelTierDecision),
		fmt.Sprintf(llmTierApiEndpointFormat, ModelTierDecision),
		llmDecisionMinConfidenceKey,
	} {
		if v := config.Config.GetString(key, ""); v != "" {
			cfg[key] = v
		}
	}
	return cfg
}

// decisionBackendFor returns the decision model configured for task, if any.
func decisionBackendFor(ctx *security.RequestContext, call ClassificationCall, task ClassificationTask) (*decisionBackend, bool) {
	if task.Multi || !decisionPilotTasks[task.Name] {
		return nil, false
	}
	cfg, source := decisionConfigSource(ctx, call.AccountId, call.ConversationId, classificationCallTier(ctx, call))
	if len(cfg) == 0 {
		return nil, false
	}
	minConfidence, err := ParseDecisionMinConfidence(cfg[llmDecisionMinConfidenceKey])
	if err != nil {
		ctx.GetLogger().Warn("classify: ignoring invalid decision settings", "source", source, "error", err)
		return nil, false
	}
	b := &decisionBackend{
		provider:      cfg[fmt.Sprintf(llmTierProviderFormat, ModelTierDecision)],
		model:         cfg[fmt.Sprintf(llmTierModelFormat, ModelTierDecision)],
		apiKey:        cfg[fmt.Sprintf(llmTierApiKeyFormat, ModelTierDecision)],
		endpoint:      cfg[fmt.Sprintf(llmTierApiEndpointFormat, ModelTierDecision)],
		minConfidence: minConfidence,
		source:        source,
	}
	if b.provider == "" {
		return nil, false
	}
	if !decision.IsProvider(b.provider) || b.model == "" {
		ctx.GetLogger().Warn("classify: the decision slot has no usable decision model", "task", task.Name, "provider", b.provider, "source", source)
		return nil, false
	}
	return b, true
}

// decisionChooser calls the decision model; a variable so tests can stub it.
var decisionChooser = func(ctx context.Context, b *decisionBackend, state, instructions string, criteria map[string]string) (decision.Choice, error) {
	client, err := decision.New(decision.Config{Provider: b.provider, Model: b.model, APIKey: b.apiKey, Endpoint: b.endpoint})
	if err != nil {
		return decision.Choice{}, fmt.Errorf("decision backend (%s): %w", b.source, err)
	}
	return client.Choose(ctx, state, instructions, criteria)
}

// decisionModel adapts one decision question to llms.Model so the call passes
// through the same egress filter and PII scrubbing as an LLM call
// (wrapOutboundModel). The filtered human text is the decision's state.
type decisionModel struct {
	backend      *decisionBackend
	instructions string
	criteria     map[string]string
	choice       decision.Choice
}

func (m *decisionModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	var state strings.Builder
	for _, msg := range messages {
		if msg.Role != llms.ChatMessageTypeHuman {
			continue
		}
		for _, part := range msg.Parts {
			if text, ok := part.(llms.TextContent); ok {
				state.WriteString(text.Text)
			}
		}
	}
	choice, err := decisionChooser(ctx, m.backend, state.String(), m.instructions, m.criteria)
	if err != nil {
		return nil, fmt.Errorf("decision model %s: %w", m.backend.model, err)
	}
	m.choice = choice
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: choice.Choice}}}, nil
}

func (m *decisionModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("decision model: Call is not supported")
}

// classifyWithDecision asks the decision model. accepted is true when its answer
// is a valid option at or above the task's minimum confidence; otherwise the
// caller falls back to the LLM and d carries what the decision model said.
func classifyWithDecision(ctx *security.RequestContext, call ClassificationCall, task ClassificationTask, input string, b *decisionBackend) (d ClassificationDecision, accepted bool, err error) {
	criteria := make(map[string]string, len(task.Options))
	for _, opt := range task.Options {
		criteria[opt.Label] = opt.Description
	}
	dm := &decisionModel{backend: b, instructions: strings.TrimSpace(task.Instructions), criteria: criteria}
	model := wrapOutboundModel(dm, b.provider, b.model)

	parent := ctx.GetContext()
	if parent == nil {
		parent = context.Background()
	}
	goCtx, cancel := context.WithTimeout(parent, decisionTimeout)
	defer cancel()
	start := time.Now()
	_, err = model.GenerateContent(goCtx, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, input)})
	latency := time.Since(start)

	d = ClassificationDecision{Backend: classificationBackendDecision, Model: b.model}
	if err != nil {
		return d, false, fmt.Errorf("classify %s with decision model: %w", task.Name, err)
	}
	choice := dm.choice
	trackDecisionUsage(ctx, call, b, choice, latency)
	d.Raw = choice.Choice
	d.Model = choice.Model
	d.Probabilities = choice.Probabilities
	conf := decisionConfidence(choice)
	d.Confidence = &conf
	labels, ok := matchClassificationLabels(task, choice.Choice)
	if !ok || conf < b.minConfidence {
		return d, false, nil
	}
	d.Labels, d.Valid = labels, true
	return d, true, nil
}

// decisionConfidence is the provider's confidence, or the chosen option's
// probability when the provider does not report one.
func decisionConfidence(c decision.Choice) float64 {
	if c.Confidence != nil {
		return *c.Confidence
	}
	if p, ok := c.Probabilities[c.Choice]; ok {
		return p
	}
	best := 0.0
	for _, p := range c.Probabilities {
		best = math.Max(best, p)
	}
	return best
}

// trackDecisionUsage records the decision call on the token-usage table so its
// volume and cost show next to the LLM calls. Cost is computed from
// llm_model_pricing when the model has a row there.
func trackDecisionUsage(ctx *security.RequestContext, call ClassificationCall, b *decisionBackend, choice decision.Choice, latency time.Duration) {
	userId := call.UserId
	if userId == "" {
		userId = security.GetSystemUserId()
	}
	bgCtx := detachedRequestContext(ctx)
	info := &TokenInfo{InputTokens: choice.InputTokens, TotalTokens: choice.InputTokens}
	source := "decision:" + b.source
	trackFn := func() {
		trackTokenUsage(bgCtx, call.ConversationId, call.MessageId, call.AgentId, call.AgentId,
			b.provider, choice.Model, call.AccountId, userId, info, latency.Seconds(), 0, nil, nil,
			false, "", nil, nil, nil, nil, false, &source)
	}
	if pool := GetMetricsWorkerPool(); pool != nil {
		if err := pool.Submit(context.Background(), trackFn); err == nil {
			return
		}
	}
	trackFn()
}
