package llm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/bedrock"
	"github.com/tmc/langchaingo/llms/googleai"
	"github.com/tmc/langchaingo/llms/openai"
	"google.golang.org/genai"
)

type Client struct {
	llm         llms.Model
	genaiClient *genai.Client // Direct genai client for proper tool schema support
	config      *config.Config
	logger      *common.Logger
	tokenUsage  *TokenUsage
	tokenMu     sync.Mutex // protects tokenUsage, calls, callsDropped
	// usageParent, when set, receives a copy of every usage delta so a derived
	// per-role client's spend still lands in the run client's cumulative totals
	// (the analysis handler snapshots those for the final response and billing).
	usageParent *Client
	// calls retains one record per LLM API call so billing can be priced
	// per call (Gemini's >200K long-context surcharge applies per request —
	// pricing a run's aggregate would tier almost every run into it).
	calls        []TokenUsageCall
	callsDropped int
	// tier is the ModelTier this client's model was resolved for. Set by the
	// orchestrator when it derives the per-role clients; stamped onto every
	// call so spend can be attributed per tier.
	tier string
}

type TokenUsage struct {
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CachedContentTokens int    `json:"cached_content_tokens"`
	ThinkingTokens      int    `json:"thinking_tokens"`
	Model               string `json:"model"`
	Provider            string `json:"provider"`
}

// TokenUsageCall is one LLM API call's usage, stamped with the model/provider
// of the client that made the call — tier clients (router/fixer/review) differ
// from the run model, and per-call attribution must keep the real one.
type TokenUsageCall struct {
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	CachedContentTokens int     `json:"cached_content_tokens,omitempty"`
	ThinkingTokens      int     `json:"thinking_tokens,omitempty"`
	LatencySeconds      float64 `json:"latency_seconds,omitempty"`
	Model               string  `json:"model"`
	Provider            string  `json:"provider"`
	// Estimated marks char/4 fallback numbers (non-genai providers) as opposed
	// to provider-reported usage.
	Estimated bool `json:"estimated,omitempty"`
	// ModelTier is the tier this call actually ran on (reasoning/retrieval/
	// summary), taken from the client that made it. Empty on clients that were
	// never stamped, which persists as NULL rather than a guess.
	ModelTier string `json:"model_tier,omitempty"`
	// TaskType labels non-main-loop calls by phase (reflection, compaction,
	// summary) so overhead spend is separable from the ReAct loop. Empty for
	// main-loop calls, which fall back to llm-server's turn-level
	// query/investigation classification.
	TaskType string `json:"task_type,omitempty"`
}

// maxCallRecords bounds the per-run call slice; runs are ≤ ~150 calls, the cap
// is a defensive backstop. Oldest records are dropped and counted so the
// consumer can reconcile against the aggregate.
const maxCallRecords = 1000

type Provider string

const (
	ProviderBedrock     Provider = "bedrock"
	ProviderOpenAI      Provider = "openai"
	ProviderGoogleAI    Provider = "googleai"
	ProviderHuggingFace Provider = "huggingface"
)

// huggingFaceBaseURL adapts a HuggingFace endpoint host to the base URL the
// OpenAI client expects. langchaingo posts to "{baseURL}/chat/completions",
// while a HuggingFace dedicated endpoint serves the OpenAI-compatible route at
// "{host}/v1/chat/completions" — so the "/v1" has to be part of the base.
func huggingFaceBaseURL(endpoint string) string {
	base := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

func NewClient(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	var llm llms.Model
	var err error

	switch Provider(cfg.LLM.Provider) {
	case ProviderBedrock:
		// Pass the region via an explicitly-configured client rather than
		// os.Setenv("AWS_REGION", ...). NewClient is built per request, so
		// mutating process-global env would be a data race across concurrent
		// requests (and would also leak the region into child commands).
		bedrockOpts := []bedrock.Option{bedrock.WithModel(cfg.LLM.Model)}
		if cfg.LLM.Region != "" {
			// Bound the AWS config load so a stuck IMDS/STS lookup fails fast
			// instead of blocking client construction indefinitely.
			awsCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			awsCfg, cerr := awsconfig.LoadDefaultConfig(awsCtx, awsconfig.WithRegion(cfg.LLM.Region))
			if cerr != nil {
				return nil, fmt.Errorf("failed to load AWS config for region %q: %w", cfg.LLM.Region, cerr)
			}
			bedrockOpts = append(bedrockOpts, bedrock.WithClient(bedrockruntime.NewFromConfig(awsCfg)))
		}
		llm, err = bedrock.New(bedrockOpts...)
	case ProviderOpenAI, ProviderHuggingFace:
		opts := []openai.Option{
			openai.WithModel(cfg.LLM.Model),
		}
		if cfg.LLM.ApiKey != "" {
			opts = append(opts, openai.WithToken(cfg.LLM.ApiKey))
		}
		baseURL := cfg.LLM.ApiEndpoint
		// HuggingFace deployments here are dedicated endpoints fronted by an
		// OpenAI-compatible API (llm-server resolves LLM_PROVIDER_API_TYPE=openai
		// for them), so they run through the same client rather than a second SDK.
		// Reject the native HuggingFace inference protocol up front instead of
		// silently sending it OpenAI-shaped requests that only fail once the agent
		// is already mid-run.
		if Provider(cfg.LLM.Provider) == ProviderHuggingFace {
			if baseURL == "" {
				return nil, errors.New("LLM_PROVIDER_API_ENDPOINT is required for the huggingface provider")
			}
			if !strings.EqualFold(cfg.LLM.ApiType, "openai") {
				return nil, fmt.Errorf("huggingface provider requires an OpenAI-compatible endpoint (api_type %q, want \"openai\")", cfg.LLM.ApiType)
			}
			baseURL = huggingFaceBaseURL(baseURL)
		}
		if baseURL != "" {
			opts = append(opts, openai.WithBaseURL(baseURL))
		}
		llm, err = openai.New(opts...)
	case "googleai":
		if cfg.LLM.ApiKey == "" {
			return nil, errors.New("LLM_PROVIDER_API_KEY environment variable is required for GoogleAI provider")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		llm, err = googleai.New(ctx, googleai.WithAPIKey(cfg.LLM.ApiKey), googleai.WithDefaultModel(cfg.LLM.Model))
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.LLM.Provider)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}

	return &Client{
		llm:    llm,
		config: cfg,
	}, nil
}

// isTransientError checks if the error is transient and worth retrying.
// Covers rate limits (429), server errors (500/503/504), and connectivity issues.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Rate limit errors
	if strings.Contains(errStr, "Error 429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "quota exceeded") ||
		strings.Contains(errStr, "too many requests") {
		return true
	}
	// Server-side transient errors
	if strings.Contains(errStr, "Error 504") ||
		strings.Contains(errStr, "Error 503") ||
		strings.Contains(errStr, "Error 500") ||
		strings.Contains(errStr, "DEADLINE_EXCEEDED") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "internal server error") ||
		strings.Contains(errStr, "connection reset") {
		return true
	}
	return false
}

// NewUsageRecorder returns a Client with no provider backend — only the
// usage-recording plumbing works. For tests of usage collection outside this
// package; any generation call on it fails with "not initialized".
func NewUsageRecorder(model, provider string) *Client {
	cfg := &config.Config{}
	cfg.LLM.Model = model
	cfg.LLM.Provider = provider
	return &Client{config: cfg}
}

// estimateUsage char/4-estimates prompt and completion tokens for providers
// whose responses carry no usage metadata (langchaingo paths). Counts text,
// tool-call arguments AND tool observations — in a ReAct conversation the
// observations dominate the prompt, so text-only counting severely
// undercounts (mirrors planners' estimateMessageTokens).
func estimateUsage(messages []llms.MessageContent, resp *llms.ContentResponse) (prompt, completion int) {
	var totalInputLength int
	for _, msg := range messages {
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				totalInputLength += len(p.Text)
			case llms.ToolCall:
				if p.FunctionCall != nil {
					totalInputLength += len(p.FunctionCall.Name) + len(p.FunctionCall.Arguments)
				}
			case llms.ToolCallResponse:
				totalInputLength += len(p.Content)
			}
		}
	}
	prompt = totalInputLength / 4
	if resp != nil && len(resp.Choices) > 0 {
		completion = len(resp.Choices[0].Content) / 4
	}
	return prompt, completion
}

// GenerateContentNoRetry performs a single LLM call with no transient-error
// retry loop. Use this for best-effort, non-fatal calls where a 30+ second
// retry chain would waste more time than retrying the whole task. The
// reflection step in the ReAct planner is the canonical caller — if it fails
// transiently, the planner just continues with its prior ledger.
func (c *Client) GenerateContentNoRetry(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	if c.llm == nil {
		return nil, errors.New("LLM client not initialized")
	}
	opts := []llms.CallOption{
		llms.WithMaxTokens(16384),
		llms.WithTemperature(0.1),
	}
	opts = append(opts, options...)
	callStart := time.Now()
	resp, err := c.llm.GenerateContent(ctx, messages, opts...)
	if err != nil {
		return nil, fmt.Errorf("LLM generation (no-retry) failed (provider=%s, model=%s): %w",
			c.config.LLM.Provider, c.config.LLM.Model, err)
	}
	prompt, completion := estimateUsage(messages, resp)
	c.RecordCallUsage(TokenUsageCall{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		LatencySeconds:   time.Since(callStart).Seconds(),
		Estimated:        true,
		TaskType:         callPhaseFromContext(ctx),
	})
	return resp, nil
}

func (c *Client) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	if c.llm == nil {
		return nil, errors.New("LLM client not initialized")
	}

	// Use a generous default token limit for all responses.
	// Gemini CLI sets no per-response limit at all — the model generates as much as needed.
	// We can't go unlimited with langchaingo, so use 16384 as a generous default that handles:
	// - ReAct steps with Thought + Action + large ActionInput (code blocks)
	// - submit_analysis with detailed implementation_instructions
	// - Code fixer generating multi-line replacements
	// Previous approach used content-sniffing heuristics (8192/12000/16000) which was fragile
	// and still caused truncation. A single high limit is simpler and more reliable.
	maxTokens := 16384

	opts := []llms.CallOption{
		llms.WithMaxTokens(maxTokens),
		llms.WithTemperature(0.1),
	}
	opts = append(opts, options...)

	// Retry configuration
	const maxRetries = 5
	const baseDelay = 2 * time.Second

	var response *llms.ContentResponse
	var err error
	var attemptStart time.Time

	for attempt := 0; attempt <= maxRetries; attempt++ {
		attemptStart = time.Now()
		response, err = c.llm.GenerateContent(ctx, messages, opts...)

		if err == nil {
			// Success!
			break
		}

		// Check if it's a transient error worth retrying
		if !isTransientError(err) {
			// Not a transient error, fail immediately
			if c.logger != nil {
				c.logger.Error(common.EventAnalysisFailure, "LLM generation failed", err, map[string]any{
					"provider":   c.config.LLM.Provider,
					"model":      c.config.LLM.Model,
					"attempt":    attempt + 1,
					"error":      err.Error(),
					"error_type": fmt.Sprintf("%T", err),
				})
			}
			// Return full error message for debugging
			return nil, fmt.Errorf("LLM generation failed (provider=%s, model=%s): %w",
				c.config.LLM.Provider, c.config.LLM.Model, err)
		}

		// Transient error - retry with exponential backoff
		if attempt < maxRetries {
			// Calculate delay with exponential backoff: 2s, 4s, 8s, 16s, 32s
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay

			if c.logger != nil {
				c.logger.Log(common.EventStepStart, "Transient error, retrying with backoff", map[string]any{
					"provider":      c.config.LLM.Provider,
					"attempt":       attempt + 1,
					"max_retries":   maxRetries,
					"retry_after":   delay.String(),
					"error_message": err.Error(),
				})
			}

			// Wait before retry
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
				// Continue to next attempt
			}
		} else {
			// Max retries exceeded
			if c.logger != nil {
				c.logger.Error(common.EventAnalysisFailure, "LLM generation failed after max retries", err, map[string]any{
					"provider":    c.config.LLM.Provider,
					"max_retries": maxRetries,
				})
			}
			return nil, fmt.Errorf("LLM generation failed after %d retries (transient errors): %w", maxRetries, err)
		}
	}

	if err != nil {
		// Should not reach here, but handle just in case
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	// Track comprehensive usage (langchaingo doesn't expose detailed token usage yet)
	// For now, we'll estimate based on request and response content
	estimatedPromptTokens, estimatedCompletionTokens := estimateUsage(messages, response)
	c.RecordCallUsage(TokenUsageCall{
		PromptTokens:     estimatedPromptTokens,
		CompletionTokens: estimatedCompletionTokens,
		LatencySeconds:   time.Since(attemptStart).Seconds(),
		Estimated:        true,
		TaskType:         callPhaseFromContext(ctx),
	})

	if c.logger != nil {
		snapshot := c.SnapshotTokenUsage()
		c.logger.Log(common.EventStepComplete, "LLM token usage tracked", map[string]any{
			"estimated_prompt_tokens":      estimatedPromptTokens,
			"estimated_completion_tokens":  estimatedCompletionTokens,
			"estimated_total_tokens":       estimatedPromptTokens + estimatedCompletionTokens,
			"cumulative_prompt_tokens":     snapshot.PromptTokens,
			"cumulative_completion_tokens": snapshot.CompletionTokens,
			"cumulative_total_tokens":      snapshot.TotalTokens,
			"provider":                     c.config.LLM.Provider,
			"model":                        c.config.LLM.Model,
		})
	}

	return response, nil
}

func (c *Client) GetModel() llms.Model {
	return c.llm
}

func (c *Client) GetProvider() string {
	return c.config.LLM.Provider
}

// GetTokenUsage returns the cumulative token usage. Kept for backward compatibility.
func (c *Client) GetTokenUsage() *TokenUsage {
	return c.SnapshotTokenUsage()
}

// SnapshotTokenUsage returns a thread-safe copy of the current cumulative token usage.
func (c *Client) SnapshotTokenUsage() *TokenUsage {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.tokenUsage == nil {
		return &TokenUsage{Model: c.config.LLM.Model, Provider: c.config.LLM.Provider}
	}
	return &TokenUsage{
		PromptTokens:        c.tokenUsage.PromptTokens,
		CompletionTokens:    c.tokenUsage.CompletionTokens,
		TotalTokens:         c.tokenUsage.TotalTokens,
		CachedContentTokens: c.tokenUsage.CachedContentTokens,
		ThinkingTokens:      c.tokenUsage.ThinkingTokens,
		Model:               c.config.LLM.Model,
		Provider:            c.config.LLM.Provider,
	}
}

// Model tiers, mirroring llm-server's core.ModelTier values. They land verbatim
// in llm_conversation_token_usage.model_tier, so the two vocabularies must not
// drift.
const (
	ModelTierReasoning = "reasoning"
	ModelTierRetrieval = "retrieval"
	ModelTierSummary   = "summary"
)

// SetModelTier records which tier this client's model was resolved for, so
// every call it makes is attributed to that tier. Set once at construction by
// the orchestrator; not safe to change while calls are in flight.
func (c *Client) SetModelTier(tier string) {
	if c == nil {
		return
	}
	c.tier = tier
}

// callPhaseKey types the context value carrying a call's phase label.
type callPhaseKey struct{}

// Call phases for non-main-loop LLM calls. These are structurally uncacheable
// one-shot prompts (fresh system instruction, no tools), and together they are
// a fifth of a long run's calls — so they need to be separable from the ReAct
// loop when reading cost.
const (
	CallPhaseReflection  = "reflection"
	CallPhaseCompaction  = "compaction"
	CallPhaseFinalAnswer = "final_answer"
)

// WithCallPhase labels every LLM call made with the returned context. Threading
// it through the context rather than the call signatures keeps the label
// available on all four recording paths (retry, no-retry, structured, genai)
// without changing any of them.
func WithCallPhase(ctx context.Context, phase string) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, callPhaseKey{}, phase)
}

// callPhaseFromContext returns the phase label, or "" for main-loop calls.
func callPhaseFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	phase, _ := ctx.Value(callPhaseKey{}).(string)
	return phase
}

// ShareUsageWith makes every future usage delta on this client also accumulate
// on parent. Used by per-role tier clients so the run's reported totals include
// their spend. One-way child→parent only; self-parenting is ignored.
func (c *Client) ShareUsageWith(parent *Client) {
	if parent == c {
		return
	}
	// Refuse any link that would close a cycle (directly or through the chain):
	// appendCall recurses through usageParent, and a cycle would overflow the
	// stack. Locks are taken one node at a time, never nested — no deadlock.
	for curr := parent; curr != nil; {
		if curr == c {
			return
		}
		curr.tokenMu.Lock()
		next := curr.usageParent
		curr.tokenMu.Unlock()
		curr = next
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.usageParent = parent
}

// RecordCallUsage is the single entry point for usage capture: it stamps
// attribution from the CALLING client's config (unless already set), then
// appends the record locally and up the usageParent chain, also accumulating
// the aggregate totals on every node it visits.
func (c *Client) RecordCallUsage(call TokenUsageCall) {
	if call.Model == "" {
		call.Model = c.config.LLM.Model
	}
	if call.Provider == "" {
		call.Provider = c.config.LLM.Provider
	}
	if call.ModelTier == "" {
		call.ModelTier = c.tier
	}
	if call.TotalTokens == 0 {
		call.TotalTokens = call.PromptTokens + call.CompletionTokens + call.ThinkingTokens
	}
	c.appendCall(call)
}

// appendCall accumulates the aggregate and retains the record under lock, then
// recurses up the parent chain (record already stamped; each node applies its
// own cap independently). Locks are taken one node at a time, never nested.
func (c *Client) appendCall(call TokenUsageCall) {
	c.tokenMu.Lock()
	if c.tokenUsage == nil {
		c.tokenUsage = &TokenUsage{}
	}
	c.tokenUsage.PromptTokens += call.PromptTokens
	c.tokenUsage.CompletionTokens += call.CompletionTokens
	c.tokenUsage.TotalTokens += call.TotalTokens
	c.tokenUsage.CachedContentTokens += call.CachedContentTokens
	c.tokenUsage.ThinkingTokens += call.ThinkingTokens
	if len(c.calls) >= maxCallRecords {
		copy(c.calls, c.calls[1:])
		c.calls[len(c.calls)-1] = call
		c.callsDropped++
	} else {
		c.calls = append(c.calls, call)
	}
	parent := c.usageParent
	c.tokenMu.Unlock()

	if parent != nil {
		parent.appendCall(call)
	}
}

// SnapshotCalls returns a copy of the retained per-call records plus the
// number dropped by the cap.
func (c *Client) SnapshotCalls() ([]TokenUsageCall, int) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	out := make([]TokenUsageCall, len(c.calls))
	copy(out, c.calls)
	return out, c.callsDropped
}

// addTokenUsage adds token counts to the cumulative total. Kept as a thin
// wrapper over RecordCallUsage so legacy call sites and tests also produce
// per-call records.
func (c *Client) addTokenUsage(promptTokens, completionTokens, totalTokens, cachedTokens int) {
	c.RecordCallUsage(TokenUsageCall{
		PromptTokens:        promptTokens,
		CompletionTokens:    completionTokens,
		TotalTokens:         totalTokens,
		CachedContentTokens: cachedTokens,
	})
}

func (c *Client) ResetTokenUsage() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.tokenUsage = &TokenUsage{}
	c.calls = nil
	c.callsDropped = 0
}

// TokenUsageDelta computes the difference between the current usage and a previous snapshot.
func TokenUsageDelta(before, after *TokenUsage) *TokenUsage {
	if before == nil {
		return after
	}
	if after == nil {
		return &TokenUsage{}
	}
	return &TokenUsage{
		PromptTokens:        after.PromptTokens - before.PromptTokens,
		CompletionTokens:    after.CompletionTokens - before.CompletionTokens,
		TotalTokens:         after.TotalTokens - before.TotalTokens,
		CachedContentTokens: after.CachedContentTokens - before.CachedContentTokens,
		ThinkingTokens:      after.ThinkingTokens - before.ThinkingTokens,
		Model:               after.Model,
		Provider:            after.Provider,
	}
}

func (c *Client) GetModelName() string {
	return c.config.LLM.Model
}

// SetLogger sets the structured logger for the LLM client
func (c *Client) SetLogger(logger *common.Logger) {
	c.logger = logger
}

// Close cleans up resources held by the client.
func (c *Client) Close() error {
	return nil
}
