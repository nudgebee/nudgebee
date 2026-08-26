package anthropic

import (
	"nudgebee/llm/llms/anthropic/anthropicclient"

	"github.com/tmc/langchaingo/callbacks"
)

type options struct {
	token            string
	model            string
	baseURL          string
	anthropicVersion string
	anthropicBeta    string
	httpClient       anthropicclient.Doer
	callbackHandler  callbacks.Handler
}

// Option is a functional option for the Anthropic LLM.
type Option func(*options)

// WithToken sets the Anthropic API token.
func WithToken(token string) Option {
	return func(opts *options) {
		opts.token = token
	}
}

// WithModel sets the default model.
func WithModel(model string) Option {
	return func(opts *options) {
		opts.model = model
	}
}

// WithBaseURL sets the base URL for the Anthropic API.
func WithBaseURL(baseURL string) Option {
	return func(opts *options) {
		opts.baseURL = baseURL
	}
}

// WithHTTPClient sets a custom HTTP client / Doer.
func WithHTTPClient(client anthropicclient.Doer) Option {
	return func(opts *options) {
		opts.httpClient = client
	}
}

// WithAnthropicVersion sets the anthropic-version header.
func WithAnthropicVersion(version string) Option {
	return func(opts *options) {
		opts.anthropicVersion = version
	}
}

// WithAnthropicBeta sets the anthropic-beta header.
func WithAnthropicBeta(beta string) Option {
	return func(opts *options) {
		opts.anthropicBeta = beta
	}
}

// WithCallbackHandler sets a callbacks handler.
func WithCallbackHandler(handler callbacks.Handler) Option {
	return func(opts *options) {
		opts.callbackHandler = handler
	}
}
