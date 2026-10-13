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
// "decision" slot: llm_tier_provider_decision (cloudflare, typesafe or openai),
// llm_tier_model_decision, llm_tier_api_key_decision,
// llm_tier_api_endpoint_decision (Cloudflare account id; an optional URL for
// the others) and
// llm_tier_min_confidence_decision (default 0.9). A configured decision model
// is used by the decisions in decisionModes, each in its mode.
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

// Decision modes, per decision: how Classify uses a configured decision model.
const (
	decisionModeOff = "off" // the LLM only
	// decisionModeShadow: the LLM decides; the decision model is asked in the
	// background and only logged (classification_shadow), adding no latency.
	decisionModeShadow = "shadow"
	// decisionModeOn: the decision model decides when it is confident enough;
	// otherwise, or on an error, the LLM decides.
	decisionModeOn = "on"
)

// decisionModes are the decisions that may use a decision model, with their
// default mode; any other decision is off. A server setting
// (llm_decision_mode_<task>, e.g. LLM_DECISION_MODE_TOOL_REQUEST_TYPE=on)
// overrides the default. The tool approval check is on: a confident "read" from
// the decision model runs the command without approval, so set it to shadow
// (LLM decides, decision model only logged) wherever that risk is not wanted
// until hand-labelled data shows no more false reads than the LLM (#40902).
var decisionModes = map[string]string{
	"workflow_turn_intent": decisionModeOn,
	"tool_request_type":    decisionModeOn,
}

// decisionModeFor returns task's mode: the server setting if valid, else the default.
func decisionModeFor(ctx *security.RequestContext, task ClassificationTask) string {
	def, ok := decisionModes[task.Name]
	if !ok || task.Multi {
		return decisionModeOff
	}
	switch mode := strings.ToLower(strings.TrimSpace(config.Config.GetString("llm_decision_mode_"+task.Name, ""))); mode {
	case "":
		return def
	case decisionModeOff, decisionModeShadow, decisionModeOn:
		return mode
	default:
		ctx.GetLogger().Warn("classify: ignoring invalid decision mode", "task", task.Name, "mode", mode, "default", def)
		return def
	}
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
	if !decision.IsDecisionModel(b.provider, b.model) {
		ctx.GetLogger().Warn("classify: the decision slot has no usable decision model", "task", task.Name, "provider", b.provider, "source", source)
		return nil, false
	}
	return b, true
}

// decisionChooser calls the decision model; a variable so tests can stub it.
var decisionChooser = func(ctx context.Context, b *decisionBackend, state, instructions string, options []decision.Option) (decision.Choice, error) {
	client, err := decision.New(decision.Config{Provider: b.provider, Model: b.model, APIKey: b.apiKey, Endpoint: b.endpoint})
	if err != nil {
		return decision.Choice{}, fmt.Errorf("decision backend (%s): %w", b.source, err)
	}
	return client.Choose(ctx, state, instructions, options)
}

// decisionModel adapts one decision question to llms.Model so the call passes
// through the same egress filter and PII scrubbing as an LLM call
// (wrapOutboundModel). The filtered human text is the decision's state.
type decisionModel struct {
	backend      *decisionBackend
	instructions string
	options      []decision.Option
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
	choice, err := decisionChooser(ctx, m.backend, state.String(), m.instructions, m.options)
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
	options := make([]decision.Option, len(task.Options))
	for i, opt := range task.Options {
		options[i] = decision.Option{Label: opt.Label, Description: opt.Description}
	}
	dm := &decisionModel{backend: b, instructions: strings.TrimSpace(task.Instructions), options: options}
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
	conf := decisionConfidence(choice) // 0 for a refusal
	d.Confidence = &conf
	labels, ok := matchClassificationLabels(task, choice.Choice)
	if choice.Refused || !ok || conf < b.minConfidence {
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
// llm_model_pricing when the model has a row there. It records the configured
// model, not the version the provider reports (jev-1.13.0), so the price row
// keeps matching across versions; the version is in the decision log.
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
			b.provider, b.model, call.AccountId, userId, info, latency.Seconds(), 0, nil, nil,
			false, "", nil, nil, nil, nil, false, &source)
	}
	if pool := GetMetricsWorkerPool(); pool != nil {
		if err := pool.Submit(context.Background(), trackFn); err == nil {
			return
		}
	}
	trackFn()
}

// runShadow runs the shadow decision; a variable so tests can run it inline.
var runShadow = func(f func()) { go f() }

// shadowDecision asks the decision model after the LLM has decided and logs
// both answers as classification_shadow. It never changes the decision; it runs
// on a detached context so it outlives the request without delaying it.
func shadowDecision(ctx *security.RequestContext, call ClassificationCall, task ClassificationTask, input string, b *decisionBackend, llm ClassificationDecision) {
	bg := detachedRequestContext(ctx)
	runShadow(func() {
		// A background goroutine has no request recovery: a panic here must not
		// take the server down.
		defer func() {
			if r := recover(); r != nil {
				bg.GetLogger().Error("classify: shadow decision panicked", "task", task.Name, "panic", r)
			}
		}()
		start := time.Now()
		dd, accepted, err := classifyWithDecision(bg, call, task, input, b)
		labels, valid := matchClassificationLabels(task, dd.Raw)
		attrs := []any{
			"task", task.Name, "llm_labels", strings.Join(llm.Labels, ","),
			"decision_answer", truncateForLog(dd.Raw), "decision_model", dd.Model,
			// would_use: confident enough that mode "on" would have used it.
			"would_use", accepted, "agree", valid && labels[0] == llm.Label(),
			"latency_ms", time.Since(start).Milliseconds(),
		}
		if dd.Confidence != nil {
			attrs = append(attrs, "confidence", *dd.Confidence)
		}
		if err != nil {
			attrs = append(attrs, "error", err.Error())
		}
		bg.GetLogger().Info("classification_shadow", attrs...)
	})
}
