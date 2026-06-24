package huggingfaceclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"strings"
)

var ErrUnexpectedStatusCode = errors.New("unexpected status code")

// InferenceTask is the type of inference task to run.
type InferenceTask string

const (
	InferenceTaskTextGeneration      InferenceTask = "text-generation"
	InferenceTaskText2TextGeneration InferenceTask = "text2text-generation"
)

type inferencePayload struct {
	Model      string        `json:"model,omitempty"`
	Inputs     string        `json:"inputs"`
	Messages   []ChatMessage `json:"-"`
	Parameters parameters    `json:"parameters,omitempty"`
}

type parameters struct {
	Adapter_id        string  `json:"adapter_id,omitempty"`
	Temperature       float64 `json:"temperature"`
	TopP              float64 `json:"top_p,omitempty"`
	TopK              int     `json:"top_k,omitempty"`
	MinLength         int     `json:"min_length,omitempty"`
	MaxLength         int     `json:"max_length,omitempty"`
	RepetitionPenalty float64 `json:"repetition_penalty,omitempty"`
	Seed              int     `json:"seed,omitempty"`
}

type (
	inferenceResponsePayload []inferenceResponse
	inferenceResponse        struct {
		Text             string `json:"generated_text"`
		PromptTokens     int    `json:"-"`
		CompletionTokens int    `json:"-"`
		TotalTokens      int    `json:"-"`
	}
)

func (c *Client) runInference(ctx context.Context, payload *inferencePayload) (inferenceResponsePayload, error) {
	if c.APIType == "openai" {
		return c.runInferenceOpenAI(ctx, payload)
	}

	payload2 := inferencePayload{
		Model:      payload.Model,
		Inputs:     payload.Inputs,
		Parameters: payload.Parameters,
	}

	url := c.url
	if !strings.Contains(c.url, "endpoints.huggingface.cloud") {
		url = fmt.Sprintf("%s/models/%s/infer", c.url, payload.Model)
		payload2.Model = ""
	}

	payloadBytes, err := common.MarshalJson(payload2)
	if err != nil {
		return nil, err
	}
	body := bytes.NewReader(payloadBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	r, err := common.HttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			// Log the error, but don't return it as it's a defer call
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()

	if r.StatusCode != http.StatusOK {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if len(b) > 0 {
			err = fmt.Errorf("%w: %d, body: %s", ErrUnexpectedStatusCode, r.StatusCode, string(b))
		} else {
			err = fmt.Errorf("%w: %d", ErrUnexpectedStatusCode, r.StatusCode)
		}
		return nil, err
	}

	// debug print the http response with httputil:
	// resDump, err := httputil.DumpResponse(r, true)
	// if err != nil {
	// 	return nil, err
	// }
	// fmt.Fprintf(os.Stderr, "%s", resDump)

	var response inferenceResponsePayload
	err = json.NewDecoder(r.Body).Decode(&response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// OpenAI-compatible chat completions (vLLM, TGI 3.x, Ollama, SGLang, LM Studio).
// Wraps the legacy text-generation payload into a single user message — the host
// model's chat template handles the rest.
type openAIChatRequest struct {
	Model              string         `json:"model"`
	Messages           []ChatMessage  `json:"messages"`
	Temperature        float64        `json:"temperature,omitempty"`
	TopP               float64        `json:"top_p,omitempty"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	Seed               int            `json:"seed,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type openAIChatChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type openAIChatResponse struct {
	Choices []openAIChatChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *Client) runInferenceOpenAI(ctx context.Context, payload *inferencePayload) (inferenceResponsePayload, error) {
	model := payload.Model
	if model == "" {
		model = c.Model
	}
	messages := payload.Messages
	if len(messages) == 0 {
		messages = []ChatMessage{{Role: "user", Content: payload.Inputs}}
	}
	req := openAIChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: payload.Parameters.Temperature,
		TopP:        payload.Parameters.TopP,
		MaxTokens:   payload.Parameters.MaxLength,
		Seed:        payload.Parameters.Seed,
	}
	// Qwen3-family chat templates default enable_thinking=true; for retrieval/summary
	// tier work that pollutes content with chain-of-thought and blows per-call timeouts.
	// Default false; LLM_HF_ENABLE_THINKING=true opts in.
	if !config.Config.LlmHFEnableThinking {
		req.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
	}
	bodyBytes, err := common.MarshalJson(req)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(c.url, "/") + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	r, err := common.HttpClient().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			fmt.Printf("Error closing response body: %v\n", err)
		}
	}()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			return nil, fmt.Errorf("%w: %d, body: %s", ErrUnexpectedStatusCode, r.StatusCode, string(b))
		}
		return nil, fmt.Errorf("%w: %d", ErrUnexpectedStatusCode, r.StatusCode)
	}
	var resp openAIChatResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned in OpenAI response")
	}
	return inferenceResponsePayload{{
		Text:             resp.Choices[0].Message.Content,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}}, nil
}
