package azureclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrEmptyResponse is returned when the OpenAI API returns an empty response.
var ErrEmptyResponse = errors.New("empty response")

type APIType string

const (
	APITypeAzure   APIType = "AZURE"
	APITypeAzureAD APIType = "AZURE_AD"
)

// Client is a client for the OpenAI API.
type Client struct {
	token      string
	Model      string
	Adapter    string
	baseURL    string
	apiType    APIType
	httpClient Doer

	EmbeddingModel string
	// required when APIType is APITypeAzure or APITypeAzureAD
	apiVersion string
}

// Option is an option for the OpenAI client.
type Option func(*Client) error

// Doer performs a HTTP request.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// New returns a new OpenAI client.
func New(token string, model string, adapter string, baseURL string,
	apiType APIType, apiVersion string, httpClient Doer,
	opts ...Option,
) (*Client, error) {
	c := &Client{
		token:      token,
		Model:      model,
		Adapter:    adapter,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiType:    apiType,
		apiVersion: apiVersion,
		httpClient: httpClient,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	return c, nil
}

// Completion is a completion.
type Completion struct {
	Text string `json:"text"`
}

// CreateCompletion creates a completion.
func (c *Client) CreateCompletion(ctx context.Context, r *CompletionRequest) (*Completion, error) {
	resp, err := c.createCompletion(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, ErrEmptyResponse
	}
	return &Completion{
		Text: resp.Choices[0].Message.Content,
	}, nil
}

// EmbeddingRequest is a request to create an embedding.
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// CreateEmbedding creates embeddings.
func (c *Client) CreateEmbedding(ctx context.Context, r *EmbeddingRequest) ([][]float32, error) {
	if r.Model == "" {
		r.Model = defaultEmbeddingModel
	}

	resp, err := c.createEmbedding(ctx, &embeddingPayload{
		Model: r.Model,
		Input: r.Input,
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, ErrEmptyResponse
	}

	embeddings := make([][]float32, 0)
	for i := 0; i < len(resp.Data); i++ {
		embeddings = append(embeddings, resp.Data[i].Embedding)
	}

	return embeddings, nil
}

// CreateChat creates chat request.
func (c *Client) CreateChat(ctx context.Context, r *ChatRequest) (*ChatCompletionResponse, error) {
	if r.Model == "" {
		if c.Model == "" {
			return nil, fmt.Errorf("model needs to be provided when using Azure API")
		} else {
			r.Model = c.Model
		}
	}
	resp, err := c.createChat(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, ErrEmptyResponse
	}
	return resp, nil
}

func IsAzure(apiType APIType) bool {
	return apiType == APITypeAzure || apiType == APITypeAzureAD
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiType == APITypeAzureAD {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("api-key", c.token)
	}
}

func (c *Client) buildURL(suffix string) string {
	baseURL := c.baseURL
	baseURL = strings.TrimRight(baseURL, "/")
	suffix = strings.TrimLeft(suffix, "/")
	return fmt.Sprintf("%s/models/%s?api-version=%s",
		baseURL, suffix, c.apiVersion,
	)
}
