package huggingfaceclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"time"
)

var (
	ErrInvalidToken  = errors.New("invalid token")
	ErrEmptyResponse = errors.New("empty response")
)

type Client struct {
	Token   string
	Model   string
	url     string
	Adapter string
}

func New(token, model, url, adapter string) (*Client, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}
	return &Client{
		Token:   token,
		Model:   model,
		url:     url,
		Adapter: adapter,
	}, nil
}

type InferenceRequest struct {
	Model             string        `json:"repositoryId"`
	Adapter           string        `json:"adapter,omitempty"`
	Prompt            string        `json:"prompt"`
	Task              InferenceTask `json:"task"`
	Temperature       float64       `json:"temperature"`
	TopP              float64       `json:"top_p,omitempty"`
	TopK              int           `json:"top_k,omitempty"`
	MinLength         int           `json:"min_length,omitempty"`
	MaxLength         int           `json:"max_length,omitempty"`
	RepetitionPenalty float64       `json:"repetition_penalty,omitempty"`
	Seed              int           `json:"seed,omitempty"`
}

type InferenceResponse struct {
	Text string `json:"generated_text"`
}

func (c *Client) RunInference(ctx context.Context, request *InferenceRequest) (*InferenceResponse, error) {
	payload := &inferencePayload{
		Model:  request.Model,
		Inputs: request.Prompt,
		Parameters: parameters{
			Temperature:       request.Temperature,
			Adapter_id:        request.Adapter,
			TopP:              request.TopP,
			TopK:              request.TopK,
			MinLength:         request.MinLength,
			MaxLength:         request.MaxLength,
			RepetitionPenalty: request.RepetitionPenalty,
			Seed:              request.Seed,
		},
	}

	if request.Temperature < 0.01 {
		payload.Parameters.Temperature = 0.01
	}

	backoff := time.Duration(config.Config.LlmServerLlmInitialBackoffSeconds) * time.Second
	var hfResp inferenceResponsePayload
	var hfErr error
	startTime := time.Now()
	maxRetryDuration := time.Duration(config.Config.LlmServerGlobalRetryBudgetMinutes) * time.Minute
	individualTimeout := time.Duration(config.Config.LlmServerMaxIndividualCallTimeoutMinutes) * time.Minute

	for time.Since(startTime) < maxRetryDuration {
		// Create a per-call context with timeout
		callCtx, cancel := context.WithTimeout(ctx, individualTimeout)
		hfResp, hfErr = c.runInference(callCtx, payload)
		cancel()

		if hfErr == nil {
			break
		}
		slog.Warn("Service unavailable, retrying...", "elapsed", time.Since(startTime), "error", hfErr)
		if time.Since(startTime) > maxRetryDuration {
			slog.Error("HuggingFace inference failed after max attempts", "error", hfErr)
			return nil, fmt.Errorf("HuggingFace inference failed after max attempts: %w", hfErr)
		}
		time.Sleep(backoff)
		backoff *= 2
	}

	if len(hfResp) == 0 {
		return nil, ErrEmptyResponse
	}
	text := hfResp[0].Text
	// TODO: Add response cleaning based on Model.
	// e.g., for gpt2, text = text[len(request.Prompt)+1:]
	return &InferenceResponse{
		Text: text,
	}, nil
}

// EmbeddingRequest is a request to create an embedding.
type EmbeddingRequest struct {
	Options map[string]any `json:"options"`
	Inputs  []string       `json:"inputs"`
}

// CreateEmbedding creates embeddings.
func (c *Client) CreateEmbedding(
	ctx context.Context,
	model string,
	task string,
	r *EmbeddingRequest,
) ([][]float32, error) {
	resp, err := c.createEmbedding(ctx, model, task, &embeddingPayload{
		Inputs:  r.Inputs,
		Options: r.Options,
	})
	if err != nil {
		return nil, err
	}

	if len(resp) == 0 {
		return nil, ErrEmptyResponse
	}

	return resp, nil
}
