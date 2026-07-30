package proxy

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/routing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
)

// resolveSession returns the session/round correlation id for a request, with its
// source. Resolution order, most-authoritative first: the configured correlation
// HEADER; then a real session_id nested inside Anthropic's metadata.user_id (Claude
// Code sends user_id as a JSON blob {device_id, account_uuid, session_id} — the
// session_id there is the true per-conversation id); then the raw metadata.user_id
// (other clients that send a plain identifier); then the INFERRED prefix fingerprint
// (a stable hash of system+tools+first-user — see prefixFingerprint; deterministic,
// so a conversation's turns share it). Empty only when there is no prefix to
// fingerprint either. `fingerprint` is precomputed by the caller (it also feeds
// cache-affinity routing). New signals slot in above `inferred` later.
func resolveSession(c *gin.Context, body []byte, fingerprint string) (id, source string) {
	if h := config.Config.SessionHeader; h != "" {
		if v := strings.TrimSpace(c.GetHeader(h)); v != "" {
			return v, "header"
		}
	}
	if uid := strings.TrimSpace(bodyMetadataUserID(body)); uid != "" {
		if strings.HasPrefix(uid, "{") {
			// A JSON blob (Claude Code): use the nested session_id if present,
			// otherwise fall through to the inferred fingerprint — never store the
			// raw blob as a session id.
			if sid := nestedSessionID(uid); sid != "" {
				return sid, "metadata.session_id"
			}
		} else {
			return uid, "metadata.user_id" // a plain client-supplied identifier
		}
	}
	if fingerprint != "" {
		return fingerprint, "inferred"
	}
	return "", ""
}

// nestedSessionID pulls a real session_id out of a metadata.user_id value when the
// client encodes one as JSON (Claude Code: {"device_id":…,"account_uuid":…,
// "session_id":"<uuid>"}). Returns "" for a plain-string user_id (no JSON object),
// so callers fall back to the raw value.
func nestedSessionID(uid string) string {
	if !strings.HasPrefix(strings.TrimSpace(uid), "{") {
		return ""
	}
	var m struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal([]byte(uid), &m)
	return strings.TrimSpace(m.SessionID)
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

// respondedModel sniffs the model id the PROVIDER echoed in its response — the
// "responded" model (e.g. a dated snapshot like gpt-4o-mini-2024-07-18), completing
// the requested→resolved→responded audit trail and exposing silent snapshot/alias
// drift. Handles a raw JSON body (unary) and an SSE first chunk (streaming):
// OpenAI/Anthropic top-level `model`, Anthropic's stream `message.model`
// (message_start), and Gemini's `modelVersion`. Empty when not found.
func respondedModel(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	b := bytes.TrimSpace(body)
	// SSE frame (streaming chunk): take the JSON from the first line that STARTS with
	// "data:". Scanning line-by-line with HasPrefix (not a substring search) avoids
	// matching "data:" inside a comment line (": ...") or an event line.
	if !bytes.HasPrefix(b, []byte("{")) {
		found := false
		for rest := b; len(rest) > 0; {
			var line []byte
			if i := bytes.IndexByte(rest, '\n'); i >= 0 {
				line, rest = rest[:i], rest[i+1:]
			} else {
				line, rest = rest, nil
			}
			if line = bytes.TrimSpace(line); bytes.HasPrefix(line, []byte("data:")) {
				b = bytes.TrimSpace(line[len("data:"):])
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	var r struct {
		Model        string `json:"model"`        // OpenAI, Anthropic (unary + OpenAI stream)
		ModelVersion string `json:"modelVersion"` // Gemini
		Message      struct {
			Model string `json:"model"` // Anthropic stream: message_start.message.model
		} `json:"message"`
	}
	if json.Unmarshal(b, &r) != nil {
		return ""
	}
	switch {
	case r.Model != "":
		return r.Model
	case r.Message.Model != "":
		return r.Message.Model
	case r.ModelVersion != "":
		return r.ModelVersion
	}
	return ""
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

// firstMessageMaxLen caps the stored opening-message preview (a short recognizer for
// the Sessions tab, not the full prompt — keeps the structure-only spirit + the column small).
const firstMessageMaxLen = 200

// firstUserMessage extracts a short RAW preview of the request's first user message —
// the opening question shown on the Sessions tab. Handles Anthropic/OpenAI string or
// array (text-block) content and Gemini parts. Whitespace is collapsed to one line and
// the text is length-capped; wrapper tokens (e.g. <session>) are deliberately NOT
// stripped (they are client scaffolding, tool-specific). Empty when none is found.
func firstUserMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var r struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if json.Unmarshal(body, &r) != nil {
		return ""
	}
	var text string
	for _, m := range r.Messages {
		if m.Role == "user" {
			text = messageContentText(m.Content)
			break
		}
	}
	if text == "" {
		for _, ct := range r.Contents {
			if ct.Role == "user" || ct.Role == "" {
				for _, p := range ct.Parts {
					if p.Text != "" {
						text = p.Text
						break
					}
				}
				if text != "" {
					break
				}
			}
		}
	}
	text = strings.Join(strings.Fields(text), " ") // collapse whitespace to one line
	if rs := []rune(text); len(rs) > firstMessageMaxLen {
		text = string(rs[:firstMessageMaxLen])
	}
	return text
}

// messageContentText renders message content — a plain string, or an array of
// {type,text} blocks (Anthropic) — to text.
func messageContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
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
