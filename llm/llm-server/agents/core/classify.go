package core

import (
	"context"
	"fmt"
	"nudgebee/llm/security"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
)

// Common classification API: one entry point for fixed-choice decisions. Backends
// sit behind Classify so callers do not change when a model or provider does;
// this file ships the LLM backend. See docs/classification-api.md.
//
// Classify never applies a caller's fallback. It returns an error when the backend
// fails and Valid=false when the answer is not one of the task's options; each
// caller keeps its own policy for both.

// Backend names reported on ClassificationDecision.Backend.
const (
	classificationBackendLLM = "llm"
	// classificationLLMAttempts is the first call plus two corrective retries,
	// matching ClassificationPlanner.
	classificationLLMAttempts = 3
)

// ClassificationTask describes one fixed-choice decision.
type ClassificationTask struct {
	// Name is a stable id used for config keys, logs and metrics, e.g. "workflow_turn_intent".
	Name string
	// Instructions say what to decide. The LLM backend uses them as the system prompt.
	Instructions string
	// Options are the allowed answers, in order. Matching is case-insensitive;
	// decisions always carry the canonical label.
	Options []ClassificationOption
	// Multi allows several labels in one answer.
	Multi bool
}

// ClassificationOption is one allowed answer.
type ClassificationOption struct {
	Label string
	// Description optionally says when the option applies; the LLM backend lists it next to the label.
	Description string
}

// ClassificationCall carries the attribution GenerateAndTrackLLMContent needs.
// AgentId is the calling agent's id: it drives API-key resolution and cost
// attribution, so it must not be replaced by the task name.
type ClassificationCall struct {
	UserId         string
	AccountId      string
	ConversationId string
	MessageId      string
	AgentId        string
	// Tier overrides the model tier for the LLM backend. Empty uses the
	// classification tier, or the summary tier when no classification model is
	// configured for the call's account (see classificationTier).
	Tier ModelTier
	// TrackContent persists prompt/response on the token-usage row when tracing is on.
	TrackContent bool
}

// ClassificationDecision is the outcome of Classify.
// Backends that score their answers (decision models) add confidence fields when
// they land; the LLM's served model is recorded on its token-usage row.
type ClassificationDecision struct {
	Labels  []string // canonical labels, primary first; empty when !Valid
	Valid   bool     // false: no usable answer — the caller applies its own fallback
	Raw     string   // the backend's raw answer, for logs
	Backend string   // which backend answered, e.g. "llm"
	Latency time.Duration
}

// Label returns the primary label, or "" when the decision is not valid.
func (d ClassificationDecision) Label() string {
	if !d.Valid || len(d.Labels) == 0 {
		return ""
	}
	return d.Labels[0]
}

// Classify answers task for input.
func Classify(ctx *security.RequestContext, call ClassificationCall, task ClassificationTask, input string) (ClassificationDecision, error) {
	if task.Name == "" || len(task.Options) == 0 {
		return ClassificationDecision{}, fmt.Errorf("classify: task needs a name and at least one option")
	}
	start := time.Now()
	d, err := classifyWithLLM(ctx, call, task, input)
	d.Latency = time.Since(start)
	recordClassificationDecision(ctx, task, d, err)
	if err != nil {
		return d, fmt.Errorf("classify %s: %w", task.Name, err)
	}
	return d, nil
}

// matchClassificationLabels canonicalizes raw against the task's options. A
// single-select answer must be exactly one option; a multi-select answer is
// comma-separated and rejected wholesale if any token is unknown, as in
// ClassificationPlanner.validateAnswer.
func matchClassificationLabels(task ClassificationTask, raw string) ([]string, bool) {
	canonical := func(token string) string {
		token = strings.Trim(strings.TrimSpace(token), ".\"'`*")
		for _, opt := range task.Options {
			if strings.EqualFold(opt.Label, token) {
				return opt.Label
			}
		}
		return ""
	}
	if !task.Multi {
		if label := canonical(raw); label != "" {
			return []string{label}, true
		}
		return nil, false
	}
	var labels []string
	seen := map[string]bool{}
	for _, token := range strings.Split(raw, ",") {
		if strings.TrimSpace(token) == "" {
			continue
		}
		label := canonical(token)
		if label == "" {
			return nil, false
		}
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	return labels, len(labels) > 0
}

// ---------------------------------------------------------------------------
// LLM backend

// classificationGenerate is GenerateAndTrackLLMContent; a variable so tests can stub it.
var classificationGenerate = GenerateAndTrackLLMContent

// classifyWithLLM is the LLM backend: strict label matching with corrective retries.
func classifyWithLLM(ctx *security.RequestContext, call ClassificationCall, task ClassificationTask, input string) (ClassificationDecision, error) {
	tier := call.Tier
	if tier == "" {
		tier = classificationTier(ctx, call)
	}
	llmCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), ContextKeyModelTier, tier),
		ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter(),
	)
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, classificationSystemPrompt(task)),
		llms.TextParts(llms.ChatMessageTypeHuman, input),
	}
	d := ClassificationDecision{Backend: classificationBackendLLM}
	for attempt := 1; attempt <= classificationLLMAttempts; attempt++ {
		result, err := classificationGenerate(llmCtx, call.UserId, call.AccountId, call.ConversationId, call.MessageId,
			call.AgentId, call.TrackContent, messages, true, llms.WithTemperature(0.0), WithThinkingLevel(ThinkingLevelFastTask))
		if err != nil {
			return d, fmt.Errorf("llm backend: %w", err)
		}
		if result == nil || len(result.Choices) == 0 || result.Choices[0] == nil {
			return d, fmt.Errorf("llm backend: empty response")
		}
		d.Raw = result.Choices[0].Content
		if labels, ok := matchClassificationLabels(task, d.Raw); ok {
			d.Labels, d.Valid = labels, true
			return d, nil
		}
		if attempt < classificationLLMAttempts {
			ctx.GetLogger().Warn("classify: llm returned an invalid option, retrying",
				"task", task.Name, "answer", truncateForLog(d.Raw), "attempt", attempt)
			messages = append(messages,
				llms.TextParts(llms.ChatMessageTypeAI, d.Raw),
				llms.TextParts(llms.ChatMessageTypeHuman, classificationRetryPrompt(task, d.Raw)))
		}
	}
	return d, nil // Valid=false: the caller applies its fallback
}

// classificationTierResolve resolves the LLM config for a call; a variable so
// tests can stub it.
var classificationTierResolve = ResolveLLMConfig

// classificationTierSources are the ResolveLLMConfig sources that mean the
// resolved model came from a layer defining the tier itself.
var classificationTierSources = map[string]bool{
	llmConfigSourceEnvTier: true, llmConfigSourceDBTier: true,
	llmConfigSourceContextOverrideTier: true, llmConfigSourceConversationTier: true,
}

// classificationTier returns ModelTierClassification when the classification tier
// resolves through a layer that defines it, and ModelTierSummary otherwise, so an
// unconfigured classification tier is the summary tier. Asking the resolver
// (rather than reading config keys) keeps layer precedence intact: a model set
// only in server config does not win over an account's own DB config, which
// would otherwise route classification to the account's global model.
func classificationTier(ctx *security.RequestContext, call ClassificationCall) ModelTier {
	tierCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierClassification),
		ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter(),
	)
	res, err := classificationTierResolve(tierCtx, call.AccountId, call.AgentId, call.ConversationId)
	if err == nil && res != nil && classificationTierSources[res.Source] {
		return ModelTierClassification
	}
	return ModelTierSummary
}

func classificationLabels(task ClassificationTask) []string {
	labels := make([]string, len(task.Options))
	for i, opt := range task.Options {
		labels[i] = opt.Label
	}
	return labels
}

func classificationSystemPrompt(task ClassificationTask) string {
	var options strings.Builder
	for _, opt := range task.Options {
		options.WriteString("- " + opt.Label)
		if opt.Description != "" {
			options.WriteString(": " + opt.Description)
		}
		options.WriteString("\n")
	}
	rule := "You MUST choose exactly one option from the list. Respond with ONLY the option name, with no other text, explanation or punctuation. Do not make up new options."
	if task.Multi {
		rule = "You MUST choose one or more options from the list. Respond with ONLY the option names, separated by a comma and a space, with no other text. Do not make up new options."
	}
	return fmt.Sprintf("%s\n\n%s\n\nYour options are:\n%s", strings.TrimSpace(task.Instructions), rule, strings.TrimRight(options.String(), "\n"))
}

func classificationRetryPrompt(task ClassificationTask, answer string) string {
	instruction := "Respond with ONLY one of"
	if task.Multi {
		instruction = "Respond with ONLY one or more of these, separated by a comma and a space"
	}
	return fmt.Sprintf("Your response %q is not a valid answer. %s: %s.",
		truncateForLog(answer), instruction, strings.Join(classificationLabels(task), ", "))
}

func truncateForLog(s string) string {
	return TruncateHead(strings.TrimSpace(s), 200) // UTF-8 safe
}

// ---------------------------------------------------------------------------
// Telemetry

func recordClassificationDecision(ctx *security.RequestContext, task ClassificationTask, d ClassificationDecision, err error) {
	attrs := []any{
		"task", task.Name, "labels", strings.Join(d.Labels, ","), "valid", d.Valid,
		"backend", d.Backend, "latency_ms", d.Latency.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	ctx.GetLogger().Info("classification_decision", attrs...)
	MetricsClassificationDecision(task.Name, d.Backend, d.Valid, err == nil, d.Latency.Seconds())
}
