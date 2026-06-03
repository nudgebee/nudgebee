package sagemaker

import (
	"context"
	"fmt"
	"nudgebee/llm/common"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sagemakerruntime"
	"github.com/tmc/langchaingo/llms"
)

// SagemakerLLM represents a SageMaker LLM client.
type SagemakerLLM struct {
	EndpointName string
	Region       string
	ModelKwargs  map[string]any
	Client       *sagemakerruntime.Client
}

// LLMResult represents the result of an LLM generation.
type LLMResult struct {
	Generations [][]Generation `json:"generations"`
}

// Generation represents a single generation from the LLM.
type Generation struct {
	Text string `json:"text"`
}

// New creates a new SageMaker LLM client.
func New(endpoinName string, region string, modelKwargs map[string]any) (*SagemakerLLM, error) {
	if endpoinName == "" {
		return nil, fmt.Errorf("endpointName is required")
	}
	if region == "" {
		region = "us-east-1"
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	client := sagemakerruntime.NewFromConfig(cfg)

	if modelKwargs == nil {
		modelKwargs = make(map[string]any)
	}

	return &SagemakerLLM{
		EndpointName: endpoinName,
		Region:       region,
		ModelKwargs:  modelKwargs,
		Client:       client,
	}, nil
}

// Call calls the SageMaker endpoint with a single prompt.
func (s *SagemakerLLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {

	opts := &llms.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}

	modelKwargs := make(map[string]any)
	for k, v := range s.ModelKwargs {
		modelKwargs[k] = v
	}
	if len(opts.StopWords) > 0 {
		modelKwargs["stop_sequences"] = opts.StopWords
	}
	if opts.MaxTokens != 0 {
		modelKwargs["max_new_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != 0 {
		modelKwargs["temperature"] = opts.Temperature
	}
	if opts.TopP != 0 {
		modelKwargs["top_p"] = opts.TopP
	}
	if opts.TopK != 0 {
		modelKwargs["top_k"] = opts.TopK
	}
	if opts.RepetitionPenalty != 0 {
		modelKwargs["repetition_penalty"] = opts.RepetitionPenalty
	}
	if opts.Seed != 0 {
		modelKwargs["seed"] = opts.Seed
	}

	payload := map[string]any{
		"inputs":     prompt,
		"parameters": modelKwargs,
	}

	payloadBytes, err := common.MarshalJson(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	input := &sagemakerruntime.InvokeEndpointInput{
		EndpointName: aws.String(s.EndpointName),
		ContentType:  aws.String("application/json"),
		Body:         payloadBytes,
	}

	output, err := s.Client.InvokeEndpoint(context.TODO(), input)
	if err != nil {
		return "", fmt.Errorf("failed to invoke endpoint: %w", err)
	}

	var response []map[string]any
	err = common.UnmarshalJson(output.Body, &response)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response) == 0 {
		return "", fmt.Errorf("unable to generate text")
	}

	firstResponse := response[0]

	// Check for different response formats
	var generatedResponse string
	if generatedResponse1, ok := firstResponse["generated_text"].(string); ok {
		generatedResponse = generatedResponse1
	} else if generatedTexts, ok := firstResponse["generated_texts"].([]any); ok {
		if len(generatedTexts) > 0 {
			if firstText, ok := generatedTexts[0].(string); ok {
				generatedResponse = firstText
			}
		}
	}

	if generatedResponse == "" {
		return "", fmt.Errorf("unable to generate text")
	}

	generatedResponse = extractMessageResponse(generatedResponse)

	return generatedResponse, fmt.Errorf("unexpected response format: %v", response)
}

// Generate calls the SageMaker endpoint with multiple prompts.
func (o *SagemakerLLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {

	// Convert MessageContent to a single prompt
	prompt := generateMessageContent(messages)

	// Call the SageMaker endpoint
	text, err := o.Call(ctx, prompt, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to call endpoint: %w", err)
	}

	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{
				Content: text,
			},
		},
	}, nil
}

func generateMessageContent(msges []llms.MessageContent) string {
	builder := strings.Builder{}
	for _, msg := range msges {
		switch msg.Role {
		case llms.ChatMessageTypeAI:
			builder.WriteString("<|assistant|>\n")
		case llms.ChatMessageTypeHuman:
			builder.WriteString("<|user|>\n")
		case llms.ChatMessageTypeSystem:
			builder.WriteString("<|system|>\n")
		}
		builder.WriteString(msg.Parts[0].(llms.TextContent).Text)
		builder.WriteString("<|end|>\n")
	}
	builder.WriteString("<|assistant|>")
	return builder.String()
}

func extractMessageResponse(msges string) string {
	if msges == "" {
		return msges
	}
	splits := strings.Split(msges, "<|assistant|>")
	response := strings.TrimSpace(splits[len(splits)-1])
	response = strings.TrimSuffix(response, "<|end|>")
	return response
}
