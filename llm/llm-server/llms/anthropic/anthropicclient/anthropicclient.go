package anthropicclient

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL          = "https://api.anthropic.com/v1"
	defaultAnthropicVersion = "2023-06-01"
	defaultHTTPTimeout      = 5 * time.Minute
)

var (
	ErrEmptyResponse = errors.New("empty response")
	ErrMissingToken  = errors.New("missing the Anthropic API key")
)

// Doer performs an HTTP request.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a client for the Anthropic Messages API.
type Client struct {
	token            string
	Model            string
	baseURL          string
	anthropicVersion string
	anthropicBeta    string
	httpClient       Doer
}

// Option is an option for configuring the Anthropic client.
type Option func(*Client) error

// New creates a new Anthropic client.
func New(token string, model string, baseURL string, httpClient Doer, opts ...Option) (*Client, error) {
	if token == "" {
		token = os.Getenv("ANTHROPIC_API_KEY")
	}

	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: defaultHTTPTimeout,
		}
	}

	c := &Client{
		token:            token,
		Model:            model,
		baseURL:          strings.TrimSuffix(baseURL, "/"),
		anthropicVersion: defaultAnthropicVersion,
		httpClient:       httpClient,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if c.token == "" {
		return nil, ErrMissingToken
	}

	return c, nil
}

// WithAnthropicVersion sets the Anthropic API version header.
func WithAnthropicVersion(version string) Option {
	return func(c *Client) error {
		c.anthropicVersion = version
		return nil
	}
}

// WithAnthropicBeta sets beta headers (e.g., prompt-caching, output-128k).
func WithAnthropicBeta(beta string) Option {
	return func(c *Client) error {
		c.anthropicBeta = beta
		return nil
	}
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("x-api-key", c.token)
	}
	if c.anthropicVersion != "" {
		req.Header.Set("anthropic-version", c.anthropicVersion)
	}
	if c.anthropicBeta != "" {
		req.Header.Set("anthropic-beta", c.anthropicBeta)
	}
}
