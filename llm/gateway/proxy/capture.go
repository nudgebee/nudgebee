package proxy

import (
	"encoding/json"
	"maps"
	"strings"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/routing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
)

// resolveSession returns the session/round correlation id for a request. LLM APIs
// are stateless, so this is client-supplied only: the configured correlation
// header first, then Anthropic's body metadata.user_id. Empty when neither is set
// — we never fabricate one.
func resolveSession(c *gin.Context, body []byte) (id, source string) {
	if h := config.Config.SessionHeader; h != "" {
		if v := strings.TrimSpace(c.GetHeader(h)); v != "" {
			return v, "header"
		}
	}
	if uid := bodyMetadataUserID(body); uid != "" {
		return uid, "metadata.user_id"
	}
	return "", ""
}

// extractRequestAttributes pulls structure-only metadata from the provider-native
// request body — never message content or tool arguments. Returns nil when
// capture is disabled or nothing is extractable. Anthropic/OpenAI-shaped today;
// other providers degrade to the common fields.
func extractRequestAttributes(_ schemas.ModelProvider, body []byte) map[string]any {
	if !config.Config.CaptureAttributes || len(body) == 0 {
		return nil
	}
	var r struct {
		Model       string            `json:"model"`
		MaxTokens   *int              `json:"max_tokens"`
		Temperature *float64          `json:"temperature"`
		TopP        *float64          `json:"top_p"`
		Stream      bool              `json:"stream"`
		System      json.RawMessage   `json:"system"`
		Messages    []json.RawMessage `json:"messages"`
		Tools       []struct {
			Name     string   `json:"name"` // Anthropic
			Function struct { // OpenAI
				Name string `json:"name"`
			} `json:"function"`
			FunctionDeclarations []struct { // Gemini
				Name string `json:"name"`
			} `json:"functionDeclarations"`
		} `json:"tools"`
		ToolChoice json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}

	attrs := map[string]any{}
	if r.MaxTokens != nil {
		attrs["max_tokens"] = *r.MaxTokens
	}
	if r.Temperature != nil {
		attrs["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		attrs["top_p"] = *r.TopP
	}
	attrs["stream"] = r.Stream
	if len(r.Messages) > 0 {
		attrs["message_count"] = len(r.Messages)
	}
	if len(r.System) > 0 {
		attrs["has_system"] = true
	}
	if names := toolNames(r.Tools); len(names) > 0 {
		attrs["tool_names"] = names
		attrs["tool_count"] = len(names)
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

func toolNames(tools []struct {
	Name     string `json:"name"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
	FunctionDeclarations []struct {
		Name string `json:"name"`
	} `json:"functionDeclarations"`
}) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		switch {
		case t.Name != "":
			names = append(names, t.Name) // Anthropic
		case t.Function.Name != "":
			names = append(names, t.Function.Name) // OpenAI
		case len(t.FunctionDeclarations) > 0: // Gemini: one entry declares many functions
			for _, fd := range t.FunctionDeclarations {
				if fd.Name != "" {
					names = append(names, fd.Name)
				}
			}
		}
	}
	return names
}

// extractResponseAttributes pulls structure-only metadata from a unary response
// body (stop_reason / finish_reason, whether a tool was invoked). Streaming
// responses are not parsed here (the final reason lives in a trailing SSE event);
// deferred to a later pass.
func extractResponseAttributes(body []byte) map[string]any {
	if !config.Config.CaptureAttributes || len(body) == 0 {
		return nil
	}
	var r struct {
		StopReason string `json:"stop_reason"` // Anthropic
		Content    []struct {
			Type string `json:"type"`
		} `json:"content"`
		Choices []struct { // OpenAI
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}
	attrs := map[string]any{}
	if r.StopReason != "" {
		attrs["stop_reason"] = r.StopReason
	}
	if len(r.Choices) > 0 && r.Choices[0].FinishReason != "" {
		attrs["stop_reason"] = r.Choices[0].FinishReason
	}
	for _, blk := range r.Content {
		if blk.Type == "tool_use" {
			attrs["used_tool"] = true
			break
		}
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// deriveAttributes computes NB's normalized view from the request context. Kept
// separate from `actual` so it can be re-derived/backfilled as normalization
// improves.
func deriveAttributes(provider schemas.ModelProvider, model, sessionSource string, usage *schemas.BifrostPassthroughUsage) map[string]any {
	d := map[string]any{
		"provider":     string(provider),
		"model_family": modelFamily(model),
	}
	if sessionSource != "" {
		d["session_source"] = sessionSource
	}
	// Long-context tier: input tokens beyond the common 200k boundary bill at the
	// long-context rate (see pricing catalog). Best-effort from extracted usage.
	if usage != nil && usage.LLMUsage != nil && usage.LLMUsage.PromptTokens > longContextThreshold {
		d["long_context"] = true
	}
	return d
}

const longContextThreshold = 200000

// modelFamily normalizes a provider model id to a coarse family for grouping,
// e.g. "claude-haiku-4-5-20251001" -> "claude-haiku", "gpt-4o-mini" -> "gpt-4o".
func modelFamily(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "claude-"):
		parts := strings.Split(m, "-")
		if len(parts) >= 2 {
			return parts[0] + "-" + parts[1] // claude-haiku / claude-opus / claude-sonnet
		}
	case strings.HasPrefix(m, "gpt-"):
		if i := strings.Index(m, "-mini"); i > 0 {
			return m[:i]
		}
	case strings.HasPrefix(m, "gemini-"):
		parts := strings.Split(m, "-")
		if len(parts) >= 2 {
			return parts[0] + "-" + parts[1]
		}
	}
	return m
}

// buildAttributes assembles the actual+derived attributes for one request.
// Returns nil when capture is disabled and there is nothing derived to record.
func buildAttributes(provider schemas.ModelProvider, model, sessionSource string, reqAttrs, respAttrs map[string]any, usage *schemas.BifrostPassthroughUsage) *metering.Attributes {
	actual := map[string]any{}
	maps.Copy(actual, reqAttrs)
	maps.Copy(actual, respAttrs)
	derived := deriveAttributes(provider, model, sessionSource, usage)
	if len(actual) == 0 && len(derived) == 0 {
		return nil
	}
	return &metering.Attributes{Actual: actual, Derived: derived}
}

// routingAttr renders a routing Decision as the derived.routing attribute detail.
func routingAttr(d routing.Decision) map[string]any {
	m := map[string]any{
		"reason":             string(d.Reason),
		"rule":               d.RuleID,
		"requested_model":    d.RequestedModel,
		"resolved_model":     d.ResolvedModel,
		"requested_provider": d.RequestedProvider,
		"resolved_provider":  d.ResolvedProvider,
	}
	if d.Strategy != "" {
		m["strategy"] = d.Strategy
	}
	if len(d.Fallbacks) > 0 {
		m["fallback_count"] = len(d.Fallbacks)
	}
	return m
}

// rewriteModel replaces the model in a request when routing resolved a different
// one (e.g. an alias/tier). Only the model field (body) or segment (Gemini path)
// changes; all other bytes are preserved — this is not a schema translation.
func rewriteModel(provider schemas.ModelProvider, body []byte, path, resolved string) ([]byte, string) {
	if resolved == "" {
		return body, path
	}
	if provider == schemas.Gemini {
		return body, rewriteGeminiModelPath(path, resolved)
	}
	// Anthropic/OpenAI carry the model in the body. Preserve every other field
	// verbatim by decoding into raw messages and replacing only "model".
	if len(body) == 0 {
		return body, path
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body, path // not JSON we understand — leave untouched
	}
	nm, err := json.Marshal(resolved)
	if err != nil {
		return body, path
	}
	m["model"] = nm
	nb, err := json.Marshal(m)
	if err != nil {
		return body, path
	}
	return nb, path
}

// rewriteGeminiModelPath replaces the model segment in a Gemini path, e.g.
// /models/{old}:generateContent -> /models/{resolved}:generateContent.
func rewriteGeminiModelPath(path, resolved string) string {
	const marker = "/models/"
	i := strings.Index(path, marker)
	if i < 0 {
		return path
	}
	head := path[:i+len(marker)]
	rest := path[i+len(marker):]
	if end := strings.IndexAny(rest, ":/"); end >= 0 {
		return head + resolved + rest[end:]
	}
	return head + resolved
}

// modelFromPath extracts the model for providers that carry it in the URL rather
// than the body (Gemini: /models/{model}:generateContent or /models/{model}).
// isAdminCall reports whether a forwarded path is a non-inference auxiliary call —
// model caching (cachedContents), token counting (:countTokens), or model listing
// (…/models) — which produce no model and no billable usage. Metered into the usage
// dashboard only when gateway_capture_admin_calls is on; otherwise skipped so these
// don't clutter the Requests tab. They still hit the provider, so rate limits and the
// quota reconcile are unaffected.
func isAdminCall(path string) bool {
	// Trim a trailing slash so a model-list call with one (e.g. /v1/models/) still
	// matches the "/models" suffix rather than slipping through as inference.
	p := strings.TrimSuffix(strings.ToLower(path), "/")
	switch {
	case strings.Contains(p, "/cachedcontents"):
		return true
	case strings.Contains(p, ":counttokens"):
		return true
	case strings.HasSuffix(p, "/models"):
		return true
	}
	return false
}

func modelFromPath(provider schemas.ModelProvider, path string) string {
	if provider != schemas.Gemini {
		return ""
	}
	i := strings.Index(path, "/models/")
	if i < 0 {
		return ""
	}
	rest := path[i+len("/models/"):]
	if c := strings.IndexAny(rest, ":/"); c >= 0 {
		rest = rest[:c]
	}
	return rest
}

// bodyMetadataUserID extracts Anthropic's metadata.user_id from a request body.
func bodyMetadataUserID(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var m struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Metadata.UserID
}
