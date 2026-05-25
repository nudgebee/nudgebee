package llm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/config"

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
	tokenMu     sync.Mutex // protects tokenUsage
}

type TokenUsage struct {
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CachedContentTokens int    `json:"cached_content_tokens"`
	Model               string `json:"model"`
	Provider            string `json:"provider"`
}

type Provider string

const (
	ProviderBedrock  Provider = "bedrock"
	ProviderOpenAI   Provider = "openai"
	ProviderGoogleAI Provider = "googleai"
)

func NewClient(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	var llm llms.Model
	var err error

	switch Provider(cfg.LLM.Provider) {
	case ProviderBedrock:
		// Set the AWS region via environment variable if specified
		if cfg.LLM.Region != "" {
			if err := os.Setenv("AWS_REGION", cfg.LLM.Region); err != nil {
				return nil, fmt.Errorf("failed to set AWS_REGION: %w", err)
			}
		}
		llm, err = bedrock.New(
			bedrock.WithModel(cfg.LLM.Model),
		)
	case ProviderOpenAI:
		opts := []openai.Option{
			openai.WithModel(cfg.LLM.Model),
		}
		if cfg.LLM.ApiKey != "" {
			opts = append(opts, openai.WithToken(cfg.LLM.ApiKey))
		}
		if cfg.LLM.ApiEndpoint != "" {
			opts = append(opts, openai.WithBaseURL(cfg.LLM.ApiEndpoint))
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
	resp, err := c.llm.GenerateContent(ctx, messages, opts...)
	if err != nil {
		return nil, fmt.Errorf("LLM generation (no-retry) failed (provider=%s, model=%s): %w",
			c.config.LLM.Provider, c.config.LLM.Model, err)
	}
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

	for attempt := 0; attempt <= maxRetries; attempt++ {
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

	// Estimate prompt tokens from input messages
	var totalInputLength int
	for _, msg := range messages {
		for _, part := range msg.Parts {
			if textPart, ok := part.(llms.TextContent); ok {
				totalInputLength += len(textPart.Text)
			}
		}
	}
	estimatedPromptTokens := totalInputLength / 4

	// Estimate completion tokens from response content
	var estimatedCompletionTokens int
	if len(response.Choices) > 0 {
		estimatedCompletionTokens = len(response.Choices[0].Content) / 4
	}

	// Update cumulative usage under lock
	c.addTokenUsage(estimatedPromptTokens, estimatedCompletionTokens, 0, 0)

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
		Model:               c.config.LLM.Model,
		Provider:            c.config.LLM.Provider,
	}
}

// addTokenUsage adds token counts to the cumulative total under lock.
func (c *Client) addTokenUsage(promptTokens, completionTokens, totalTokens, cachedTokens int) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.tokenUsage == nil {
		c.tokenUsage = &TokenUsage{}
	}
	c.tokenUsage.PromptTokens += promptTokens
	c.tokenUsage.CompletionTokens += completionTokens
	if totalTokens > 0 {
		c.tokenUsage.TotalTokens += totalTokens
	} else {
		c.tokenUsage.TotalTokens += promptTokens + completionTokens
	}
	c.tokenUsage.CachedContentTokens += cachedTokens
}

func (c *Client) ResetTokenUsage() {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.tokenUsage = &TokenUsage{}
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
