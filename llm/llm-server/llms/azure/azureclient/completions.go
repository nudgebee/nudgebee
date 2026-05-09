package azureclient

import (
	"context"
)

// CompletionRequest is a request to complete a completion.
type CompletionRequest struct {
	Model            string   `json:"model"`
	Adapter          string   `json:"adapter_id,omitempty"`
	Prompt           string   `json:"prompt"`
	Temperature      float64  `json:"temperature"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
	N                int      `json:"n,omitempty"`
	FrequencyPenalty float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64  `json:"presence_penalty,omitempty"`
	TopP             float64  `json:"top_p,omitempty"`
	StopWords        []string `json:"stop,omitempty"`
	Seed             int      `json:"seed,omitempty"`

	// StreamingFunc is a function to be called for each chunk of a streaming response.
	// Return an error to stop streaming early.
	StreamingFunc func(ctx context.Context, chunk []byte) error `json:"-"`
}

type CompletionResponse struct {
	ID      string  `json:"id,omitempty"`
	Created float64 `json:"created,omitempty"`
	Choices []struct {
		FinishReason string  `json:"finish_reason,omitempty"`
		Index        float64 `json:"index,omitempty"`
		Logprobs     any     `json:"logprobs,omitempty"`
		Text         string  `json:"text,omitempty"`
	} `json:"choices,omitempty"`
	Model  string `json:"model,omitempty"`
	Object string `json:"object,omitempty"`
	Usage  struct {
		CompletionTokens float64 `json:"completion_tokens,omitempty"`
		PromptTokens     float64 `json:"prompt_tokens,omitempty"`
		TotalTokens      float64 `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

type errorMessage struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (c *Client) setCompletionDefaults(payload *CompletionRequest) {
	// Set defaults
	if payload.MaxTokens == 0 {
		payload.MaxTokens = 2048
	}

	if len(payload.StopWords) == 0 {
		payload.StopWords = nil
	}
}

// nolint:lll
func (c *Client) createCompletion(ctx context.Context, payload *CompletionRequest) (*ChatCompletionResponse, error) {
	c.setCompletionDefaults(payload)
	return c.createChat(ctx, &ChatRequest{
		Model: payload.Model,
		Messages: []*ChatMessage{
			{Role: "user", Content: payload.Prompt},
		},
		Adapter:          payload.Adapter,
		Temperature:      payload.Temperature,
		TopP:             payload.TopP,
		MaxTokens:        payload.MaxTokens,
		N:                payload.N,
		StopWords:        payload.StopWords,
		FrequencyPenalty: payload.FrequencyPenalty,
		PresencePenalty:  payload.PresencePenalty,
		StreamingFunc:    payload.StreamingFunc,
		Seed:             payload.Seed,
	})
}
