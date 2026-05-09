package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	aiplatform "cloud.google.com/go/aiplatform/apiv1"
	"cloud.google.com/go/aiplatform/apiv1/aiplatformpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	ServiceNameVertexAI = "Vertex AI"
)

type vertexAIService struct{}

func (s *vertexAIService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	resources := []providers.Resource{}

	// Endpoints are regional resources
	endpoints, err := s.listEndpoints(ctx, session.ProjectId, region, session.Opts)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping Vertex AI endpoints — API disabled or permission denied", "error", err, "region", region)
		} else {
			ctx.GetLogger().Error("failed to list vertex ai endpoints", "error", err, "region", region)
		}
	} else {
		resources = append(resources, endpoints...)
	}

	// Models are regional resources
	models, err := s.listModels(ctx, session.ProjectId, region, session.Opts)
	if err != nil {
		RecordGCPPermissionError(ctx, err)
		if isGCPPermissionOrNotFoundError(err) {
			ctx.GetLogger().Warn("skipping Vertex AI models — API disabled or permission denied", "error", err, "region", region)
		} else {
			ctx.GetLogger().Error("failed to list vertex ai models", "error", err, "region", region)
		}
	} else {
		resources = append(resources, models...)
	}

	return resources, nil
}

func (s *vertexAIService) listEndpoints(ctx providers.CloudProviderContext, projectID, region string, opts []option.ClientOption) ([]providers.Resource, error) {
	if region == "" || region == "global" {
		// Skip for global - endpoints are regional
		return []providers.Resource{}, nil
	}

	// Create client with regional endpoint
	// Vertex AI requires region-specific endpoints like us-central1-aiplatform.googleapis.com
	endpoint := fmt.Sprintf("%s-aiplatform.googleapis.com:443", region)
	clientOpts := append(opts, option.WithEndpoint(endpoint))
	client, err := aiplatform.NewEndpointClient(ctx.GetContext(), clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create endpoint client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close vertex ai endpoint client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}

	// Format: projects/{project}/locations/{location}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)

	req := &aiplatformpb.ListEndpointsRequest{
		Parent: parent,
	}

	it := client.ListEndpoints(ctx.GetContext(), req)
	for {
		endpoint, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list endpoints: %w", err)
		}

		resources = append(resources, providers.Resource{
			Id:          endpoint.GetName(),
			Name:        endpoint.GetDisplayName(),
			Type:        "vertex-ai-endpoint",
			Arn:         endpoint.GetName(),
			Region:      region,
			ServiceName: ServiceNameVertexAI,
			Status:      providers.ResourceStatusActive,
			Tags:        map[string][]string{},
			Meta:        map[string]any{},
			CreatedAt:   time.Now(),
		})
	}

	return resources, nil
}

func (s *vertexAIService) listModels(ctx providers.CloudProviderContext, projectID, region string, opts []option.ClientOption) ([]providers.Resource, error) {
	if region == "" || region == "global" {
		// Skip for global - models are regional
		return []providers.Resource{}, nil
	}

	// Create client with regional endpoint
	// Vertex AI requires region-specific endpoints like us-central1-aiplatform.googleapis.com
	endpoint := fmt.Sprintf("%s-aiplatform.googleapis.com:443", region)
	clientOpts := append(opts, option.WithEndpoint(endpoint))
	client, err := aiplatform.NewModelClient(ctx.GetContext(), clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create model client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close vertex ai model client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}

	// Format: projects/{project}/locations/{location}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)

	req := &aiplatformpb.ListModelsRequest{
		Parent: parent,
	}

	it := client.ListModels(ctx.GetContext(), req)
	for {
		model, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return resources, fmt.Errorf("failed to list models: %w", err)
		}

		resources = append(resources, providers.Resource{
			Id:          model.GetName(),
			Name:        model.GetDisplayName(),
			Type:        "vertex-ai-model",
			Arn:         model.GetName(),
			Region:      region,
			ServiceName: ServiceNameVertexAI,
			Status:      providers.ResourceStatusActive,
			Tags:        map[string][]string{},
			Meta:        map[string]any{},
			CreatedAt:   time.Now(),
		})
	}

	return resources, nil
}

func (s *vertexAIService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Vertex AI metrics would come from Cloud Monitoring
	return providers.QueryMetricsResponse{
		Items: []providers.MetricItem{},
	}, fmt.Errorf("vertex ai metrics not implemented")
}

func (s *vertexAIService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	// No specific recommendations for Vertex AI resources yet
	return []providers.Recommendation{}, nil
}

func (s *vertexAIService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("vertex ai service does not support recommendations")
}

func (s *vertexAIService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("vertex ai service does not support commands")
}

func (s *vertexAIService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
