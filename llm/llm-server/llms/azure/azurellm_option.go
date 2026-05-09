package azure

import (
	"nudgebee/llm/llms/azure/azureclient"

	"github.com/tmc/langchaingo/callbacks"
)

type APIType azureclient.APIType

const (
	APITypeAzure   = APIType(azureclient.APITypeAzure)
	APITypeAzureAD = APIType(azureclient.APITypeAzureAD)
)

const (
	DefaultAPIVersion = "2023-05-15"
)

type options struct {
	token      string
	model      string
	baseURL    string
	adapter    string
	apiType    APIType
	httpClient azureclient.Doer

	responseFormat *ResponseFormat

	// required when APIType is APITypeAzure or APITypeAzureAD
	apiVersion string
	// embeddingModel string

	callbackHandler callbacks.Handler
}

// Option is a functional option for the AzureAI client.
type Option func(*options)

// ResponseFormat is the response format for the AzureAI client.
type ResponseFormat = azureclient.ResponseFormat

// ResponseFormatJSON is the JSON response format.
var ResponseFormatJSON = &ResponseFormat{Type: "json_object"} //nolint:gochecknoglobals

// WithToken passes the OpenAI API token to the client. If not set, the token
// is read from the AZUREAI_API_KEY environment variable.
func WithToken(token string) Option {
	return func(opts *options) {
		opts.token = token
	}
}

// WithModel passes the AzureAI model to the client. If not set, the model
// is read from the AZUREAI_MODEL environment variable.
// Required when ApiType is Azure.
func WithModel(model string) Option {
	return func(opts *options) {
		opts.model = model
	}
}

func WithBaseURL(baseURL string) Option {
	return func(opts *options) {
		opts.baseURL = baseURL
	}
}

func WithAdapter(adapter string) Option {
	return func(opts *options) {
		opts.adapter = adapter
	}
}

// WithAPIVersion passes the api version to the client. If not set, the default value
// is DefaultAPIVersion.
func WithAPIVersion(apiVersion string) Option {
	return func(opts *options) {
		opts.apiVersion = apiVersion
	}
}

// WithHTTPClient allows setting a custom HTTP client. If not set, the default value
// is http.DefaultClient.
func WithHTTPClient(client azureclient.Doer) Option {
	return func(opts *options) {
		opts.httpClient = client
	}
}

// WithCallback allows setting a custom Callback Handler.
func WithCallback(callbackHandler callbacks.Handler) Option {
	return func(opts *options) {
		opts.callbackHandler = callbackHandler
	}
}

// WithResponseFormat allows setting a custom response format.
func WithResponseFormat(responseFormat *ResponseFormat) Option {
	return func(opts *options) {
		opts.responseFormat = responseFormat
	}
}
