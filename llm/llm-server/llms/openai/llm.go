package openai

import (
	"errors"
	"os"

	"github.com/tmc/langchaingo/httputil"
	"nudgebee/llm/llms/openai/internal/openaiclient"
)

var (
	ErrEmptyResponse              = errors.New("no response")
	ErrMissingToken               = errors.New("missing the OpenAI API key, set it in the OPENAI_API_KEY environment variable") //nolint:lll
	ErrMissingAzureModel          = errors.New("model needs to be provided when using Azure API")
	ErrMissingAzureEmbeddingModel = errors.New("embeddings model needs to be provided when using Azure API")

	ErrUnexpectedResponseLength = errors.New("unexpected length of response")
)

// newClient creates an instance of the internal client.
func newClient(opts ...Option) (*options, *openaiclient.Client, error) {
	options := &options{
		token:        os.Getenv(tokenEnvVarName),
		model:        os.Getenv(modelEnvVarName),
		baseURL:      getEnvs(baseURLEnvVarName, baseAPIBaseEnvVarName),
		organization: os.Getenv(organizationEnvVarName),
		apiType:      APIType(openaiclient.APITypeOpenAI),
		httpClient:   httputil.DefaultClient,
	}

	for _, opt := range opts {
		opt(options)
	}
	// set of options needed for Azure client
	if openaiclient.IsAzure(openaiclient.APIType(options.apiType)) {
		if options.apiVersion == "" {
			options.apiVersion = DefaultAPIVersion
		}
		// NUDGEBEE: this check was nested inside the apiVersion default above, so
		// supplying an apiVersion — the normal Azure case — skipped it entirely.
		if options.model == "" {
			return options, nil, ErrMissingAzureModel
		}
		// The matching embeddingModel check stays out of the unconditional path on
		// purpose: chat-only Azure deployments legitimately configure no embedding
		// model, and failing construction here would take their chat down too. It
		// still fails at embedding time, where it is actually needed.
	}

	if len(options.token) == 0 {
		return options, nil, ErrMissingToken
	}

	var clientOptions []openaiclient.Option
	if options.embeddingDimensions != 0 {
		clientOptions = append(clientOptions, openaiclient.WithEmbeddingDimensions(options.embeddingDimensions))
	}
	if options.chatTemplateKwargs != nil {
		clientOptions = append(clientOptions, openaiclient.WithChatTemplateKwargs(options.chatTemplateKwargs))
	}
	cli, err := openaiclient.New(options.token, options.model, options.baseURL, options.organization,
		openaiclient.APIType(options.apiType), options.apiVersion, options.httpClient, options.embeddingModel,
		options.responseFormat, clientOptions...,
	)
	return options, cli, err
}

func getEnvs(keys ...string) string {
	for _, key := range keys {
		val, ok := os.LookupEnv(key)
		if ok {
			return val
		}
	}
	return ""
}
