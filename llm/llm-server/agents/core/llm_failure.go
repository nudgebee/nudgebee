package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
)

// LLMFailureKind is why a model call failed, in terms of what the user can do
// about it.
type LLMFailureKind string

const (
	LLMFailureUnavailable       LLMFailureKind = "unavailable"
	LLMFailureRateLimited       LLMFailureKind = "rate_limited"
	LLMFailureGarbledReply      LLMFailureKind = "garbled_reply"
	LLMFailureTooLarge          LLMFailureKind = "too_large"
	LLMFailureOutOfCredits      LLMFailureKind = "out_of_credits"
	LLMFailureInvalidKey        LLMFailureKind = "invalid_key"
	LLMFailureMissingCredential LLMFailureKind = "missing_credential"
	LLMFailureUnknownModel      LLMFailureKind = "unknown_model"
	LLMFailureDailyCap          LLMFailureKind = "daily_cap"
	LLMFailureContentBlocked    LLMFailureKind = "content_blocked"
	LLMFailureUnknown           LLMFailureKind = "unknown"
)

// NeedsSettingsFix reports whether retrying the same model cannot help.
func (k LLMFailureKind) NeedsSettingsFix() bool {
	switch k {
	case LLMFailureOutOfCredits, LLMFailureInvalidKey, LLMFailureMissingCredential, LLMFailureUnknownModel:
		return true
	}
	return false
}

// failsOnSameKey reports whether a fallback model on the same credential is
// bound to fail the same way. An unknown model or a per-model daily cap can
// still succeed on a different model behind the same key.
func (k LLMFailureKind) failsOnSameKey() bool {
	switch k {
	case LLMFailureOutOfCredits, LLMFailureInvalidKey, LLMFailureMissingCredential:
		return true
	}
	return false
}

// LLMFailure is a model call that failed after retries and fallbacks, with what
// is needed to tell the user why. Error() is the provider's own error so logs,
// token-usage rows and the text-matching retry classifiers see exactly what
// they saw before; UserMessage() is what the user is shown.
type LLMFailure struct {
	Kind LLMFailureKind
	// CustomerConfig is true when the failing model came from the customer's own
	// LLM config. Only then are config, provider and model named to the user.
	CustomerConfig bool
	ConfigName     string
	Provider       string
	Model          string
	Cause          error
}

func (f *LLMFailure) Error() string {
	if f == nil || f.Cause == nil {
		return "llm failure"
	}
	return f.Cause.Error()
}

func (f *LLMFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

// UserMessage is the answer shown when the run ends on this failure.
func (f *LLMFailure) UserMessage() string {
	if f == nil {
		return "The AI service returned an error. Please try again."
	}
	who := "The platform AI model"
	if f.CustomerConfig {
		who = f.describeConfig()
	}
	fixIt := " An admin needs to fix it in LLM settings."

	switch f.Kind {
	case LLMFailureOutOfCredits:
		if f.CustomerConfig {
			return fmt.Sprintf("%s is out of credits. An admin needs to top up the account or change the default model in LLM settings.", who)
		}
		return "The platform AI model is out of credits. Contact Nudgebee support."
	case LLMFailureInvalidKey:
		if f.CustomerConfig {
			return fmt.Sprintf("%s rejected its API key as invalid or expired.%s", who, fixIt)
		}
		return "The platform AI model rejected its credentials. Contact Nudgebee support."
	case LLMFailureMissingCredential:
		if f.CustomerConfig {
			return fmt.Sprintf("%s has no valid credential for its provider.%s", who, fixIt)
		}
		return "The platform AI model has no valid credential. Contact Nudgebee support."
	case LLMFailureUnknownModel:
		if f.CustomerConfig {
			return fmt.Sprintf("%s uses a model name the provider doesn't recognise.%s", who, fixIt)
		}
		return "The platform AI model is misconfigured. Contact Nudgebee support."
	case LLMFailureDailyCap:
		if f.CustomerConfig {
			return fmt.Sprintf("%s has reached its daily limit, which will not clear for some time. Contact your administrator if you need more capacity.", who)
		}
		return "The platform AI model has reached its daily limit, which will not clear for some time. Contact Nudgebee support."
	case LLMFailureRateLimited:
		return fmt.Sprintf("%s is rate limited and stayed busy after several retries. Please wait a moment and try again.", who)
	case LLMFailureUnavailable:
		return fmt.Sprintf("%s could not be reached after several retries. Please try again in a few minutes.", who)
	case LLMFailureGarbledReply:
		return fmt.Sprintf("%s sent a reply that couldn't be read. Please try again.", who)
	case LLMFailureTooLarge:
		return fmt.Sprintf("This conversation has grown too large for %s, even after summarising earlier steps. Start a new conversation to continue.", lowerFirst(who))
	case LLMFailureContentBlocked:
		return fmt.Sprintf("%s refused this request under its content policy. Try rephrasing it.", who)
	}
	if !f.CustomerConfig {
		return "The AI service returned an error. Please try again, or contact Nudgebee support if it keeps happening."
	}
	errText := providerErrorForUser(f.Cause)
	if errText == "" {
		return fmt.Sprintf("%s returned an error. Please try again.", who)
	}
	return fmt.Sprintf("%s returned an error:\n\n```\n%s\n```", who, errText)
}

func (f *LLMFailure) describeConfig() string {
	provider := f.Provider
	if provider == "custom" {
		// An OpenAI-compatible endpoint; the model name says more than "custom".
		provider = ""
	}
	detail := strings.TrimSpace(strings.Join(nonEmpty(provider, f.Model), " "))
	switch {
	case f.ConfigName != "" && detail != "":
		return fmt.Sprintf("%q (%s)", f.ConfigName, detail)
	case f.ConfigName != "":
		return fmt.Sprintf("%q", f.ConfigName)
	case detail != "":
		return detail
	}
	return "Your AI model"
}

func lowerFirst(s string) string {
	for _, prefix := range []string{"The ", "Your "} {
		if strings.HasPrefix(s, prefix) {
			return strings.ToLower(prefix) + s[len(prefix):]
		}
	}
	return s
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

var (
	// Prefixes our own layers add in front of the provider's error.
	internalErrorPrefixRe = regexp.MustCompile(`(?i)^(react\d*: [a-z ]+: |error: agent unable to process request\s*|model \S+ \(\S+\) and all \d+ fallback model\(s\) are currently unavailable: )`)
	secretLikeRe          = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]{8,}|\b(sk|pk|rk)-[a-z0-9_-]{8,}|\bAIza[0-9a-z_-]{20,}|\b[a-f0-9]{40,}\b|((?:api[_-]?key|token|secret|password)\s*[=:]\s*)\S+`)
)

const providerErrorMaxLen = 600

const llmConfigNameLookupTimeout = 2 * time.Second

// providerErrorForUser is the provider's error text with our own prefixes
// removed and anything that looks like a credential masked.
func providerErrorForUser(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	for {
		trimmed := strings.TrimSpace(internalErrorPrefixRe.ReplaceAllString(text, ""))
		if trimmed == text {
			break
		}
		text = trimmed
	}
	text = secretLikeRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := secretLikeRe.FindStringSubmatch(m)
		if len(sub) > 3 {
			for _, keep := range []string{sub[1], sub[3]} {
				if keep != "" {
					return keep + "***"
				}
			}
		}
		return "***"
	})
	text = strings.ReplaceAll(text, "```", "'''")
	if len(text) > providerErrorMaxLen {
		count := 0
		for i := range text {
			if count == providerErrorMaxLen {
				text = text[:i] + "…"
				break
			}
			count++
		}
	}
	return text
}

var (
	outOfCreditsMarkers = []string{
		"no credits remaining",
		"credit balance is too low",
		"maximum cost exceeds your available credits",
		"requires more credits, or fewer max_tokens",
		"check your plan and billing details",
		"insufficient_quota",
	}
	// Mention credits but clear on their own: OpenRouter reserves credit for
	// calls still in flight.
	transientCreditMarkers = []string{
		"given your current in-flight requests",
	}
	invalidKeyMarkers = []string{
		"invalid api key",
		"incorrect api key",
		"invalid x-api-key",
		"api key not valid",
		"invalid or expired nb token",
		"invalid_api_key",
		"authentication_error",
	}
	missingCredentialMarkers = []string{
		"no vertex credential is configured",
		"credential is configured for your organization",
		"environment variable is not set",
	}
	unknownModelMarkers = []string{
		"unexpected model name format",
		"unknown or missing model",
		"model_not_found",
		"does not exist or you do not have access",
		"no such model",
	}
	garbledReplyMarkers = []string{
		"extra data: line",
		"unexpected end of json input",
	}
	contentBlockedMarkers = []string{
		"content_filter",
		"content management policy",
		"responsible ai practices",
		"blocked due to safety",
		"finish_reason: safety",
		"finishreason: safety",
	}
	modelRetiredRe       = regexp.MustCompile(`model\S* \S+ is no longer available`)
	statusUnauthorizedRe = regexp.MustCompile(`(^|status code: |error |\()401\b`)
)

func containsAny(text string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// classifyLLMFailure decides why a model call failed. Signals that a setting
// must change are checked first and only on unambiguous provider wording: a
// wrong "needs a settings fix" stops retries that would have worked.
func classifyLLMFailure(err error) LLMFailureKind {
	if err == nil {
		return LLMFailureUnknown
	}
	var existing *LLMFailure
	if errors.As(err, &existing) {
		return existing.Kind
	}
	text := strings.ToLower(safeError(err))

	switch {
	case errors.Is(err, egressfilter.ErrSecretsBlocked):
		return LLMFailureContentBlocked
	case containsAny(text, transientCreditMarkers):
		return LLMFailureRateLimited
	case containsAny(text, outOfCreditsMarkers):
		return LLMFailureOutOfCredits
	case containsAny(text, invalidKeyMarkers) || statusUnauthorizedRe.MatchString(text):
		return LLMFailureInvalidKey
	case containsAny(text, missingCredentialMarkers):
		return LLMFailureMissingCredential
	case containsAny(text, unknownModelMarkers) || modelRetiredRe.MatchString(text):
		return LLMFailureUnknownModel
	case containsAny(text, contentBlockedMarkers):
		return LLMFailureContentBlocked
	case containsAny(text, garbledReplyMarkers):
		return LLMFailureGarbledReply
	}

	// The remaining classes reuse the detectors that already drive retries, in
	// the same order classifyUserFacingLLMError uses: a Bedrock throttle reads
	// as both quota and token limit and is a capacity event.
	switch {
	case isQuotaError(err):
		if common.RateLimitDetailFromError(err).Window == common.RateLimitWindowDay {
			return LLMFailureDailyCap
		}
		return LLMFailureRateLimited
	case isTokenLimitError(err):
		return LLMFailureTooLarge
	case isTransientError(err), isCircuitTrippingError(err), isEmptyResponseError(err):
		return LLMFailureUnavailable
	}
	return LLMFailureUnknown
}

// isCustomerLLMConfig reports whether res resolved to the customer's own LLM
// config rather than one set by our environment.
func isCustomerLLMConfig(res *LLMConfigResolution) bool {
	if res == nil {
		return false
	}
	if strings.HasPrefix(res.Source, "db-") {
		return true
	}
	if parsed, err := parseConfigSourceId(res.PinnedConfigSource); err == nil && parsed != nil {
		return parsed.Layer == "db"
	}
	return false
}

// newLLMFailure wraps the final error of a model call. The lookup of the
// config's name only runs on failure.
func newLLMFailure(ctx *security.RequestContext, err error, accountId, provider, model string, res *LLMConfigResolution) error {
	if err == nil {
		return nil
	}
	var existing *LLMFailure
	if errors.As(err, &existing) {
		return err
	}
	f := &LLMFailure{
		Kind:     classifyLLMFailure(err),
		Provider: provider,
		Model:    model,
		Cause:    err,
	}
	if isCustomerLLMConfig(res) {
		f.CustomerConfig = true
		f.ConfigName = llmConfigName(ctx, accountId, res)
	}
	logger := slog.Default()
	if ctx != nil {
		logger = ctx.GetLogger()
	}
	logger.Error("llm call failed", "kind", string(f.Kind), "customer_config", f.CustomerConfig,
		"provider", provider, "model", model, "error", safeError(err))
	return f
}

// llmConfigName is the name of the customer's LLM config the call used, or ""
// when it can't be found.
func llmConfigName(ctx *security.RequestContext, accountId string, res *LLMConfigResolution) string {
	if res == nil {
		return ""
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	integrationId := ""
	if parsed, perr := parseConfigSourceId(res.PinnedConfigSource); perr == nil && parsed != nil && parsed.Layer == "db" {
		integrationId = parsed.IntegrationUuid
	} else if accountId != "" && ctx != nil {
		integrationId, _ = selectAccountLLMIntegration(ctx, dbManager, accountId)
	}
	if integrationId == "" {
		return ""
	}
	// Keeps the request's tracing but not its deadline, which a failed call has often used up.
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = ctx.GetContext()
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), llmConfigNameLookupTimeout)
	defer cancel()
	var name string
	if err := dbManager.Db.GetContext(lookupCtx, &name, `SELECT name FROM integrations WHERE id = $1`, integrationId); err != nil {
		slog.Warn("llm failure: unable to look up config name", "integration_id", integrationId, "error", err)
		return ""
	}
	return name
}

// LLMFailureMessage returns the user message when err is a failed model call.
func LLMFailureMessage(err error) (string, bool) {
	var f *LLMFailure
	if errors.As(err, &f) {
		return f.UserMessage(), true
	}
	return "", false
}

// isAffordableWithFewerTokens matches OpenRouter's "requires more credits, or
// fewer max_tokens": the credit left can still cover a smaller request.
func isAffordableWithFewerTokens(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(safeError(err)), "or fewer max_tokens")
}

// handleSettingsFixError skips same-model retries and fallbacks that would fail
// the same way, and reports the primary model's failure if nothing works.
func handleSettingsFixError(rc *retryContext, fallbackModels []FallbackModel, kind LLMFailureKind) (*llms.ContentResponse, *LLMCallMetadata, error) {
	primaryErr := rc.lastErr
	for i, fb := range fallbackModels {
		if rc.triedModels[fb.key()] {
			continue
		}
		if kind.failsOnSameKey() && sameLLMCredential(rc.resolution, fb.Resolution) {
			continue
		}
		completion, err := tryFallbackModel(rc, fb, i)
		if err == nil {
			return completion, buildCallMetadata(rc, true), nil
		}
	}
	rc.lastErr = primaryErr
	return nil, buildCallMetadata(rc, false), ErrLlmUnableToGenerate(primaryErr)
}

// sameLLMCredential reports whether a fallback calls through the primary's
// credential. A fallback without its own resolution reuses the primary's.
func sameLLMCredential(primary, fallback *LLMConfigResolution) bool {
	if fallback == nil {
		return true
	}
	if primary == nil {
		return false
	}
	return credentialIdentity(primary) == credentialIdentity(fallback)
}
