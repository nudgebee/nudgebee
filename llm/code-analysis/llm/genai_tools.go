package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/common"

	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

// ToolDefinition represents a tool that can be used for native function calling.
// This is a provider-agnostic representation used by the planner.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema as map: {type, properties, required}
}

// GenAISession holds per-analysis recording state for the genai
// function-calling round-trip: the original model responses (with
// ThoughtSignatures) that must be spliced back into subsequent requests.
//
// Recording state is per-conversation, not per-Client. Sharing it on the
// long-lived *Client caused thought_signature drift when two analyses ran
// concurrently on the same workspace pod (e.g., an in-flight code-analysis
// run plus a PR-lifecycle followup): each Plan() call's ResetChat wiped the
// other's recordings, and concurrent appends interleaved entries from
// different conversations. By the time either analysis reached its 5th FC,
// the splicer had run out of (or replaced with the wrong) recorded responses,
// and Gemini rejected the request.
//
// Construct one with NewGenAISession at the start of an analysis and pass
// it through every GenerateContentWithTools call in that analysis. Never
// reuse across analyses.
type GenAISession struct {
	responses []*genai.Content
	// callCount is the number of GenerateContent calls made in this analysis.
	// Used only to label the per-call token-usage diagnostic so the ReAct
	// context-growth curve can be read straight from the logs.
	callCount int
	// prevHashes is the per-message fingerprint of the history sent on the
	// previous call. Comparing it to the current call reveals whether the
	// prefix stayed byte-stable (append-only → cache-friendly) or a prior
	// message mutated (→ busts Gemini's implicit prompt cache). Diagnostic only.
	prevHashes []string
	// prevSysHash / prevToolsHash fingerprint the system instruction and tool
	// schema (the large, separately-passed part of the cacheable prefix) so we
	// can confirm the WHOLE prefix — not just the turn history — is byte-stable.
	prevSysHash   string
	prevToolsHash string
	// sigByCall maps a function call's identity (name + canonical args) to the
	// thought_signature gemini returned for it. It is an identity-keyed backstop
	// to the positional splice: it re-attaches signatures onto reconstructed
	// functionCall parts that the splice did not cover (parallel-call grouping,
	// order drift, synthesized calls). Gemini 3 hard-rejects any functionCall in
	// the replayed history whose part is missing its signature.
	sigByCall map[string][]byte
}

// NewGenAISession returns a fresh recording session for one analysis.
func NewGenAISession() *GenAISession {
	return &GenAISession{sigByCall: map[string][]byte{}}
}

// callSigKey is the stable identity of a function call used to key its recorded
// thought_signature. Go marshals map keys in sorted order, so the same call
// reconstructed from langchaingo (which round-trips Args through JSON) produces
// the same key — as long as its Args were not truncated by window compaction.
func callSigKey(fc *genai.FunctionCall) string {
	if fc == nil {
		return ""
	}
	args, _ := json.Marshal(fc.Args)
	return fc.Name + "\x00" + string(args)
}

// recordIfFC appends content if it contains a FunctionCall part. Text- or
// thought-only model responses don't carry signatures Gemini requires on
// replay, so skipping them keeps responses in lockstep with FC-containing
// model contents the planner will rebuild in future turns.
func (s *GenAISession) recordIfFC(content *genai.Content) {
	if s == nil || content == nil || !contentHasFunctionCall(content) {
		return
	}
	s.responses = append(s.responses, content)
	if s.sigByCall == nil {
		s.sigByCall = map[string][]byte{}
	}
	for _, p := range content.Parts {
		if p.FunctionCall != nil && len(p.ThoughtSignature) > 0 {
			s.sigByCall[callSigKey(p.FunctionCall)] = p.ThoughtSignature
		}
	}
}

// reattachSignatures fills in the thought_signature on any functionCall part in
// history that is still missing one, looking it up by call identity. It runs
// after spliceModelResponses as a backstop: the splice restores signatures by
// replacing whole FC-containing model contents positionally, but positional
// alignment can drift (parallel-call grouping, synthesized submit calls), which
// would leave a reconstructed functionCall part without its signature and make
// Gemini 3 reject the entire request. Recorded-original parts already carry a
// signature, so they are left untouched; only signatureless reconstructed parts
// are mutated, and those are never shared with the recordings.
func (s *GenAISession) reattachSignatures(history []*genai.Content) []*genai.Content {
	if s == nil || len(s.sigByCall) == 0 {
		return history
	}
	for _, content := range history {
		if content == nil || content.Role != "model" {
			continue
		}
		for _, p := range content.Parts {
			if p.FunctionCall != nil && len(p.ThoughtSignature) == 0 {
				if sig, ok := s.sigByCall[callSigKey(p.FunctionCall)]; ok {
					p.ThoughtSignature = sig
				}
			}
		}
	}
	return history
}

// spliceModelResponses replaces reconstructed FC-containing model contents
// in history with the originally recorded responses (which carry
// ThoughtSignatures). Matching is positional among FC-containing contents
// only — text/thought-only model messages pass through unchanged.
func (s *GenAISession) spliceModelResponses(history []*genai.Content) []*genai.Content {
	if s == nil || len(s.responses) == 0 {
		return history
	}
	result := make([]*genai.Content, 0, len(history))
	fcIdx := 0
	for _, content := range history {
		if content.Role == "model" && contentHasFunctionCall(content) && fcIdx < len(s.responses) {
			result = append(result, s.responses[fcIdx])
			fcIdx++
			continue
		}
		result = append(result, content)
	}
	return result
}

// numRecordedFCTurns reports how many FC-containing model responses have been
// recorded this session. Used only for diagnostics (recordings vs. history
// alignment).
func (s *GenAISession) numRecordedFCTurns() int {
	if s == nil {
		return 0
	}
	return len(s.responses)
}

// unsignedFunctionCalls returns "name@<position>" for every model functionCall
// part in history whose thought_signature is missing. Position is 1-based over
// all contents to match the index Gemini reports in its 400 error. A non-empty
// result predicts a Gemini 3 "missing thought_signature" rejection.
func unsignedFunctionCalls(history []*genai.Content) []string {
	var out []string
	for i, content := range history {
		if content == nil || content.Role != "model" {
			continue
		}
		for _, p := range content.Parts {
			if p.FunctionCall != nil && len(p.ThoughtSignature) == 0 {
				out = append(out, fmt.Sprintf("%s@%d", p.FunctionCall.Name, i+1))
			}
		}
	}
	return out
}

// GenerateContentWithTools calls the LLM with native function calling support.
// For GoogleAI, this bypasses langchaingo's limited convertTools and uses the
// genai SDK directly with proper nested schema support. The session captures
// per-analysis ThoughtSignature recordings and MUST be unique to one analysis.
// For other providers, the session is unused and may be nil.
func (c *Client) GenerateContentWithTools(
	ctx context.Context,
	messages []llms.MessageContent,
	tools []ToolDefinition,
	session *GenAISession,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	if Provider(c.config.LLM.Provider) == ProviderGoogleAI {
		return c.generateContentWithGenAI(ctx, messages, tools, session)
	}
	// Fallback for other providers: convert to langchaingo format
	return c.GenerateContent(ctx, messages, append(options, llms.WithTools(convertToLlmsTools(tools)))...)
}

// convertToLlmsTools converts ToolDefinition to langchaingo format (for non-GoogleAI providers).
func convertToLlmsTools(tools []ToolDefinition) []llms.Tool {
	result := make([]llms.Tool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return result
}

// generateContentWithGenAI uses the genai SDK directly to make function calling
// requests with properly nested tool schemas.
//
// Uses stateless GenerateContent (not Chat.Send) to avoid the persistent session
// mismatch bug where injected messages (budget warnings, circuit breakers) would
// prevent the FunctionResponse from reaching the chat session. Instead, we send
// the full conversation history each call and preserve ThoughtSignature by
// recording raw model responses on the per-analysis session and splicing them
// back into subsequent requests.
//
// This approach matches how Gemini CLI handles multi-turn function calling.
// ensureGenAIClient lazily constructs the direct genai client (once).
func (c *Client) ensureGenAIClient() error {
	if c.genaiClient != nil {
		return nil
	}
	if c.config.LLM.ApiKey == "" {
		return fmt.Errorf("LLM_PROVIDER_API_KEY environment variable is required for GoogleAI provider")
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  c.config.LLM.ApiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return fmt.Errorf("failed to create genai client: %w", err)
	}
	c.genaiClient = client
	return nil
}

// ErrStructuredOutputUnsupported is returned by GenerateStructuredJSON when the
// configured provider has no native structured-output path; callers fall back
// to plain generation plus their own text-extraction parsing.
var ErrStructuredOutputUnsupported = errors.New("structured output not supported for this provider")

// GenerateStructuredJSON forces the model to return JSON conforming to schema,
// using genai's ResponseMIMEType + ResponseSchema (GoogleAI only). Built for
// the reflection step: free-text ledger responses measurably failed to parse
// 1-2x per run (prose around the JSON, truncation), silently stalling the
// distillation watermark that observation aging and ledger carry-over depend
// on. Single attempt, no retry loop — mirrors GenerateContentNoRetry semantics
// (a failed reflection is non-fatal; the caller keeps its prior ledger).
func (c *Client) GenerateStructuredJSON(ctx context.Context, messages []llms.MessageContent, schema *genai.Schema) (string, error) {
	if Provider(c.config.LLM.Provider) != ProviderGoogleAI {
		return "", ErrStructuredOutputUnsupported
	}
	if err := c.ensureGenAIClient(); err != nil {
		return "", err
	}
	systemInstruction, history := convertMessagesToGenAI(messages)
	if len(history) == 0 {
		return "", fmt.Errorf("no messages to send")
	}
	temp := float32(0.1)
	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens:  8192,
		Temperature:      &temp,
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
	}
	if systemInstruction != nil {
		cfg.SystemInstruction = systemInstruction
	}
	resp, err := c.genaiClient.Models.GenerateContent(ctx, c.config.LLM.Model, history, cfg)
	if err != nil {
		return "", fmt.Errorf("structured generation failed (model=%s): %w", c.config.LLM.Model, err)
	}
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "", fmt.Errorf("structured generation returned no candidates")
	}
	if resp.UsageMetadata != nil {
		c.addTokenUsage(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
			int(resp.UsageMetadata.TotalTokenCount),
			int(resp.UsageMetadata.CachedContentTokenCount),
		)
	}
	var b strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if part != nil && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", fmt.Errorf("structured generation returned empty text")
	}
	return b.String(), nil
}

func (c *Client) generateContentWithGenAI(
	ctx context.Context,
	messages []llms.MessageContent,
	tools []ToolDefinition,
	session *GenAISession,
) (*llms.ContentResponse, error) {
	// Create genai client (lazily, once)
	if err := c.ensureGenAIClient(); err != nil {
		return nil, err
	}

	// Convert langchaingo messages to genai format, extracting system instruction.
	// sanitizeFunctionCallOrdering ensures FC→FR adjacency in the history.
	systemInstruction, history := convertMessagesToGenAI(messages)
	if len(history) == 0 {
		return nil, fmt.Errorf("no messages to send")
	}

	// Splice recorded model responses (with ThoughtSignatures) into the history.
	// The planner's langchaingo messages don't carry ThoughtSignature, but we
	// recorded the raw genai model responses from previous calls in the
	// per-analysis session. Replace reconstructed "model" Content with the
	// originals to preserve ThoughtSignature.
	history = session.spliceModelResponses(history)
	// Backstop: fill any functionCall part the positional splice left without a
	// signature, keyed by call identity. Gemini 3 rejects the whole request if any
	// functionCall in the replayed history is missing its thought_signature.
	history = session.reattachSignatures(history)

	// Early-warning diagnostic: any functionCall part still lacking a signature
	// after splice+reattach is what triggers Gemini 3's "missing thought_signature"
	// 400. Log it (with the offending call names/positions) so the failure mode is
	// diagnosable from logs alone instead of requiring a live repro.
	if c.logger != nil {
		if unsigned := unsignedFunctionCalls(history); len(unsigned) > 0 {
			c.logger.Log(common.EventPlanningProgress, "functionCall parts missing thought_signature before send", map[string]any{
				"provider":          c.config.LLM.Provider,
				"model":             c.config.LLM.Model,
				"unsigned_calls":    unsigned,
				"recorded_fc_turns": session.numRecordedFCTurns(),
				"history_len":       len(history),
			})
		}
	}

	// Cache-stability diagnostic (W1a): the history sent here is final (post
	// splice + reattach). If any message BELOW the new tail changed vs the last
	// call, that mutation is what busts Gemini's implicit prompt cache. Log the
	// first divergent index so the exact mutator (signature splice vs injected
	// mid-history message) is named from logs alone.
	if c.logger != nil && session != nil {
		curHashes := hashHistoryContents(history)
		firstChanged, changedInOverlap := firstDivergence(session.prevHashes, curHashes)
		sysHash := hashOne(systemInstruction)
		toolsHash := hashOne(tools)
		c.logger.Log(common.EventPlanningProgress, "history cache-stability diff", map[string]any{
			"history_len":         len(history),
			"prev_history_len":    len(session.prevHashes),
			"first_changed_index": firstChanged,     // -1 => pure append (prefix stable, cache-friendly)
			"changed_in_overlap":  changedInOverlap, // # of pre-existing msgs that mutated (>0 => cache-busting)
			"stable_prefix_len":   stablePrefixLen(firstChanged, len(session.prevHashes)),
			"append_only":         firstChanged == -1,
			"system_changed":      session.prevSysHash != "" && session.prevSysHash != sysHash,
			"tools_changed":       session.prevToolsHash != "" && session.prevToolsHash != toolsHash,
			"prefix_fully_stable": firstChanged == -1 && (session.prevSysHash == "" || session.prevSysHash == sysHash) && (session.prevToolsHash == "" || session.prevToolsHash == toolsHash),
		})
		session.prevHashes = curHashes
		session.prevSysHash = sysHash
		session.prevToolsHash = toolsHash
	}

	// Build config
	temp := float32(0.1)
	genaiConfig := &genai.GenerateContentConfig{
		MaxOutputTokens: 16384,
		Temperature:     &temp,
		// Include thoughts so Gemini 3 returns thought_signatures on functionCall
		// parts. Without this they are not emitted, and replaying tool-call history
		// then fails with "Function call is missing a thought_signature". Older
		// thinking models that don't support thoughts ignore this (the SDK only
		// returns thoughts "if the model supports thought and thoughts are
		// available"), and convertGenAIResponse drops thought text so the planner's
		// answer parsing is unaffected.
		ThinkingConfig: &genai.ThinkingConfig{IncludeThoughts: true},
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
		},
	}

	genaiTools := convertToolDefsToGenAI(tools)
	if len(genaiTools) > 0 {
		genaiConfig.Tools = genaiTools
	}
	if systemInstruction != nil {
		genaiConfig.SystemInstruction = systemInstruction
	}

	// Retry with exponential backoff for rate limits
	const maxRetries = 5
	const baseDelay = 2 * time.Second

	var resp *genai.GenerateContentResponse
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err = c.genaiClient.Models.GenerateContent(ctx, c.config.LLM.Model, history, genaiConfig)
		if err == nil {
			break
		}

		if !isTransientError(err) {
			if c.logger != nil {
				c.logger.Error(common.EventAnalysisFailure, "GenAI generation failed", err, map[string]any{
					"provider": c.config.LLM.Provider,
					"model":    c.config.LLM.Model,
					"attempt":  attempt + 1,
					"error":    err.Error(),
				})
			}
			return nil, fmt.Errorf("LLM generation failed (provider=%s, model=%s): %w",
				c.config.LLM.Provider, c.config.LLM.Model, err)
		}

		if attempt < maxRetries {
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
			if c.logger != nil {
				c.logger.Log(common.EventStepStart, "Transient error, retrying with backoff", map[string]any{
					"provider":    c.config.LLM.Provider,
					"attempt":     attempt + 1,
					"max_retries": maxRetries,
					"retry_after": delay.String(),
				})
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}
		} else {
			return nil, fmt.Errorf("LLM generation failed after %d retries (transient errors): %w", maxRetries, err)
		}
	}

	if resp == nil || len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("empty response from genai")
	}

	// Record the raw model response (with ThoughtSignature) on the session for
	// future calls in this analysis. recordIfFC ignores text/thought-only
	// responses, which keeps the recording in lockstep with FC-containing model
	// messages the planner will rebuild in future turns' history.
	if len(resp.Candidates) > 0 {
		session.recordIfFC(resp.Candidates[0].Content)
	}

	// Convert response to langchaingo format
	contentResp := convertGenAIResponse(resp)

	// Track token usage under lock
	if resp.UsageMetadata != nil {
		c.addTokenUsage(
			int(resp.UsageMetadata.PromptTokenCount),
			int(resp.UsageMetadata.CandidatesTokenCount),
			int(resp.UsageMetadata.TotalTokenCount),
			int(resp.UsageMetadata.CachedContentTokenCount),
		)

		// Per-call token + context-accumulation breakdown. The genai path
		// captures real per-call usage but historically only the run total was
		// logged (analysis_complete), so the per-iteration context-growth curve
		// — and which role (tool observations vs system vs history) drives the
		// bloat — was invisible. Emit it here so a single run's logs show where
		// the quadratic ReAct resend accumulates.
		if c.logger != nil && session != nil {
			session.callCount++
			pt := int(resp.UsageMetadata.PromptTokenCount)
			cached := int(resp.UsageMetadata.CachedContentTokenCount)
			fields := summarizeMessageSizes(messages, tools)
			fields["call_index"] = session.callCount
			fields["prompt_tokens"] = pt
			fields["cached_content_tokens"] = cached
			fields["output_tokens"] = int(resp.UsageMetadata.CandidatesTokenCount)
			fields["total_tokens"] = int(resp.UsageMetadata.TotalTokenCount)
			fields["fresh_input_tokens"] = pt - cached
			if pt > 0 {
				fields["cache_hit_pct"] = 100 * cached / pt
			}
			fields["model"] = c.config.LLM.Model
			c.logger.Log(common.EventStepComplete, "LLM per-call token usage", fields)
		}
	}

	return contentResp, nil
}

// summarizeMessageSizes breaks the outbound prompt down by role so the per-call
// token log reveals WHERE context accumulates. In a ReAct loop the full history
// is resent every turn, so tool-observation bytes grow each iteration and are
// re-billed O(N^2) — chars_tool_observations climbing call-over-call is the
// bloat signature. Char counts are a provider-agnostic proxy; the authoritative
// token numbers come from UsageMetadata alongside this.
func summarizeMessageSizes(messages []llms.MessageContent, tools []ToolDefinition) map[string]any {
	var sysC, humanC, aiC, toolC, maxC int
	var maxRole string
	for _, m := range messages {
		c := 0
		for _, p := range m.Parts {
			switch v := p.(type) {
			case llms.TextContent:
				c += len(v.Text)
			case llms.ToolCall:
				if v.FunctionCall != nil {
					c += len(v.FunctionCall.Name) + len(v.FunctionCall.Arguments)
				}
			case llms.ToolCallResponse:
				c += len(v.Content)
			}
		}
		switch m.Role {
		case llms.ChatMessageTypeSystem:
			sysC += c
		case llms.ChatMessageTypeHuman:
			humanC += c
		case llms.ChatMessageTypeAI:
			aiC += c
		case llms.ChatMessageTypeTool:
			toolC += c
		default:
			humanC += c
		}
		if c > maxC {
			maxC = c
			maxRole = string(m.Role)
		}
	}
	toolSchemaC := 0
	for _, t := range tools {
		toolSchemaC += len(t.Name) + len(t.Description)
		if b, err := json.Marshal(t.Parameters); err == nil {
			toolSchemaC += len(b)
		}
	}
	return map[string]any{
		"msg_count":               len(messages),
		"chars_system":            sysC,
		"chars_human":             humanC,
		"chars_ai":                aiC,
		"chars_tool_observations": toolC,
		"chars_tool_schema":       toolSchemaC,
		"biggest_msg_chars":       maxC,
		"biggest_msg_role":        maxRole,
	}
}

// hashHistoryContents fingerprints each history message so consecutive calls can
// be diffed. A json.Marshal of the *genai.Content captures every byte the request
// depends on (text, function calls/responses, thought signatures), so any change
// to a message changes its fingerprint. Diagnostic only (W1a).
func hashHistoryContents(history []*genai.Content) []string {
	out := make([]string, len(history))
	for i, ct := range history {
		b, err := json.Marshal(ct)
		if err != nil {
			out[i] = "err"
			continue
		}
		sum := sha256.Sum256(b)
		out[i] = hex.EncodeToString(sum[:8])
	}
	return out
}

// hashOne fingerprints any json-marshalable value (system instruction, tool
// defs) so its byte-stability across calls can be checked. Diagnostic only (W1a).
func hashOne(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "err"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// firstDivergence returns the first index at which cur differs from prev over
// their common prefix, or -1 if every overlapping message is identical (a pure
// append). changedInOverlap counts how many overlapping messages differ — any
// value > 0 means the prefix was mutated and the implicit cache is busted.
func firstDivergence(prev, cur []string) (firstChanged, changedInOverlap int) {
	firstChanged = -1
	n := len(prev)
	if len(cur) < n {
		n = len(cur)
	}
	for i := 0; i < n; i++ {
		if prev[i] != cur[i] {
			if firstChanged == -1 {
				firstChanged = i
			}
			changedInOverlap++
		}
	}
	return firstChanged, changedInOverlap
}

// stablePrefixLen is the number of leading messages byte-identical to the
// previous call. On a pure append (firstChanged == -1) the whole previous
// history is stable.
func stablePrefixLen(firstChanged, prevLen int) int {
	if firstChanged == -1 {
		return prevLen
	}
	return firstChanged
}

// convertToolDefsToGenAI converts ToolDefinition slice to genai Tool format
// with proper recursive schema handling.
func convertToolDefsToGenAI(tools []ToolDefinition) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		decl := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
		}
		if tool.Parameters != nil {
			decl.Parameters = convertMapToGenAISchema(tool.Parameters)
		}
		decls = append(decls, decl)
	}

	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// convertMapToGenAISchema recursively converts a JSON Schema map to genai.Schema.
func convertMapToGenAISchema(m map[string]any) *genai.Schema {
	if m == nil {
		return nil
	}

	schema := &genai.Schema{}

	// Type
	if t, ok := m["type"].(string); ok {
		schema.Type = schemaTypeFromString(t)
	}

	// Description
	if d, ok := m["description"].(string); ok {
		schema.Description = d
	}

	// Format
	if f, ok := m["format"].(string); ok {
		schema.Format = f
	}

	// Nullable
	if n, ok := m["nullable"].(bool); ok {
		schema.Nullable = &n
	}

	// Enum
	if e, ok := m["enum"]; ok {
		schema.Enum = toStringSlice(e)
	}

	// Items (for arrays)
	if items, ok := m["items"].(map[string]any); ok {
		schema.Items = convertMapToGenAISchema(items)
	}

	// Properties (for objects)
	if props, ok := m["properties"].(map[string]any); ok {
		schema.Properties = make(map[string]*genai.Schema)
		for name, val := range props {
			if valMap, ok := val.(map[string]any); ok {
				schema.Properties[name] = convertMapToGenAISchema(valMap)
			}
		}
	}

	// Required
	if req, ok := m["required"]; ok {
		schema.Required = toStringSlice(req)
	}

	return schema
}

// schemaTypeFromString converts a JSON Schema type string to genai.Type.
func schemaTypeFromString(t string) genai.Type {
	switch t {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeUnspecified
	}
}

// toStringSlice converts an interface{} to []string.
// Handles both []string and []interface{} formats.
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		result := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}

// convertMessagesToGenAI converts langchaingo messages to genai Content format.
// Returns system instruction (if any) and the conversation history.
//
// Gemini requires that a FunctionResponse content immediately follows the
// content containing the corresponding FunctionCall. This function enforces
// that invariant by post-processing the history to relocate any messages
// that were inserted between a function call and its response.
func convertMessagesToGenAI(messages []llms.MessageContent) (*genai.Content, []*genai.Content) {
	var systemInstruction *genai.Content
	history := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		parts := convertLlmsPartsToGenAI(msg.Parts)
		if len(parts) == 0 {
			continue
		}

		content := &genai.Content{Parts: parts}

		switch msg.Role {
		case llms.ChatMessageTypeSystem:
			// System instructions go to a separate field
			systemInstruction = content
			continue
		case llms.ChatMessageTypeAI:
			content.Role = "model"
		case llms.ChatMessageTypeHuman:
			content.Role = "user"
		case llms.ChatMessageTypeTool:
			// Tool responses are sent as "user" role in genai
			content.Role = "user"
		default:
			content.Role = "user"
		}

		history = append(history, content)
	}

	history = sanitizeFunctionCallOrdering(history)

	return systemInstruction, history
}

// sanitizeFunctionCallOrdering ensures every Content with a FunctionCall part
// is immediately followed by a Content with the corresponding FunctionResponse.
// Any non-function-response messages between them are relocated before the
// function call to preserve Gemini's strict ordering requirement.
func sanitizeFunctionCallOrdering(history []*genai.Content) []*genai.Content {
	result := make([]*genai.Content, 0, len(history))

	for i := 0; i < len(history); i++ {
		msg := history[i]

		// Check if this message contains a FunctionCall
		if !contentHasFunctionCall(msg) {
			result = append(result, msg)
			continue
		}

		// This is a model message with a FunctionCall. The next message MUST
		// be a user message containing a FunctionResponse. If there are
		// intervening messages (e.g. a separator text), collect them and
		// move them before this function call.
		var interlopers []*genai.Content
		j := i + 1
		for j < len(history) && !contentHasFunctionResponse(history[j]) {
			// Only relocate user-role messages (e.g. budget warnings, separators).
			// Never move model messages — they may contain FunctionCalls which
			// would create consecutive FCs without FRs if relocated.
			if history[j].Role == "user" {
				interlopers = append(interlopers, history[j])
			}
			j++
		}

		if len(interlopers) > 0 {
			// Move interlopers before the function call
			result = append(result, interlopers...)
		}

		// Append the function call
		result = append(result, msg)

		// Append the function response if found
		if j < len(history) {
			result = append(result, history[j])
			i = j // skip past everything we've consumed
		}
	}

	return result
}

// contentHasFunctionCall returns true if the Content contains a FunctionCall part.
func contentHasFunctionCall(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}

// contentHasFunctionResponse returns true if the Content contains a FunctionResponse part.
func contentHasFunctionResponse(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

// convertLlmsPartsToGenAI converts langchaingo content parts to genai parts.
func convertLlmsPartsToGenAI(parts []llms.ContentPart) []*genai.Part {
	result := make([]*genai.Part, 0, len(parts))

	for _, part := range parts {
		switch p := part.(type) {
		case llms.TextContent:
			if p.Text != "" {
				result = append(result, genai.NewPartFromText(p.Text))
			}
		case llms.ToolCall:
			if p.FunctionCall != nil {
				var argsMap map[string]any
				if err := json.Unmarshal([]byte(p.FunctionCall.Arguments), &argsMap); err != nil {
					argsMap = map[string]any{}
				}
				result = append(result, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   p.ID,
						Name: p.FunctionCall.Name,
						Args: argsMap,
					},
				})
			}
		case llms.ToolCallResponse:
			result = append(result, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       p.ToolCallID,
					Name:     p.Name,
					Response: map[string]any{"response": p.Content},
				},
			})
		}
	}

	return result
}

// convertGenAIResponse converts a genai response to langchaingo format.
func convertGenAIResponse(resp *genai.GenerateContentResponse) *llms.ContentResponse {
	contentResponse := &llms.ContentResponse{}

	for _, candidate := range resp.Candidates {
		var buf strings.Builder
		var toolCalls []llms.ToolCall

		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				// Skip thought parts: with ThinkingConfig.IncludeThoughts enabled the
				// model returns its reasoning as Thought parts, which must not be
				// folded into the planner's parsed answer/thought text.
				if part.Text != "" && !part.Thought {
					buf.WriteString(part.Text)
				}
				if part.FunctionCall != nil {
					b, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						continue
					}
					toolCalls = append(toolCalls, llms.ToolCall{
						ID: part.FunctionCall.ID,
						FunctionCall: &llms.FunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: string(b),
						},
					})
				}
			}
		}

		metadata := make(map[string]any)
		if resp.UsageMetadata != nil {
			metadata["input_tokens"] = resp.UsageMetadata.PromptTokenCount
			metadata["output_tokens"] = resp.UsageMetadata.CandidatesTokenCount
			metadata["total_tokens"] = resp.UsageMetadata.TotalTokenCount
		}

		contentResponse.Choices = append(contentResponse.Choices, &llms.ContentChoice{
			Content:        buf.String(),
			StopReason:     string(candidate.FinishReason),
			GenerationInfo: metadata,
			ToolCalls:      toolCalls,
		})
	}

	return contentResponse
}
