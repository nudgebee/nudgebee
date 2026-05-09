package servicemap

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// ServiceSpecificSource uses service-specific APIs to discover relationships
// This is the fallback when AWS Config and VPC Flow Logs are unavailable
type ServiceSpecificSource struct {
	provider ServiceSpecificProviderInterface
	logger   *slog.Logger
}

// ServiceSpecificProviderInterface defines the methods we need for service-specific queries
type ServiceSpecificProviderInterface interface {
	QueryServiceMapWithFallback(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error)
}

// NewServiceSpecificSource creates a new service-specific relationship source
func NewServiceSpecificSource(provider ServiceSpecificProviderInterface, logger *slog.Logger) *ServiceSpecificSource {
	return &ServiceSpecificSource{
		provider: provider,
		logger:   logger,
	}
}

// GetRelationships queries service-specific APIs for resource relationships
func (s *ServiceSpecificSource) GetRelationships(ctx context.Context, request QueryRequest) (QueryResponse, error) {
	startTime := time.Now()

	// Convert providers.CloudProviderContext from context if available
	providerCtx, ok := ctx.(providers.CloudProviderContext)
	if !ok {
		// Create a basic context wrapper
		providerCtx = providers.NewCloudProviderContext(ctx)
	}

	// Extract account from request/context
	account, err := s.extractAccountFromRequest(ctx, request)
	if err != nil {
		return QueryResponse{
			Applications: []providers.ServiceMapApplication{},
			Errors:       []error{fmt.Errorf("failed to extract account: %w", err)},
			Metadata: SourceMetadata{
				Source:        s.Name(),
				QueriedAt:     startTime,
				ExecutionTime: time.Since(startTime),
			},
		}, err
	}

	// Convert QueryRequest to providers.QueryServiceMapRequest
	serviceMapRequest := providers.QueryServiceMapRequest{
		Resources: make([]providers.QueryServiceMapResourceRequest, len(request.Resources)),
		Region:    "",
	}

	for i, res := range request.Resources {
		serviceMapRequest.Resources[i] = providers.QueryServiceMapResourceRequest{
			ServiceName: res.ResourceType,
			Resource:    res.ResourceID,
		}
		// Use first resource's region as query region
		if i == 0 {
			serviceMapRequest.Region = res.Region
		}
	}

	// Call the existing queryServiceMapWithFallback method
	response, err := s.provider.QueryServiceMapWithFallback(providerCtx, account, serviceMapRequest)

	var errors []error
	if err != nil {
		errors = append(errors, err)
	}

	// Add errors from response
	for _, errMsg := range response.Errors {
		errors = append(errors, fmt.Errorf("%s", errMsg))
	}

	return QueryResponse{
		Applications: response.Applications,
		Errors:       errors,
		Metadata: SourceMetadata{
			Source:           s.Name(),
			QueriedAt:        startTime,
			ExecutionTime:    time.Since(startTime),
			ResourcesQueried: len(response.Applications),
		},
	}, nil
}

// extractAccountFromRequest extracts account info from context/request
func (s *ServiceSpecificSource) extractAccountFromRequest(
	ctx context.Context,
	request QueryRequest,
) (providers.Account, error) {
	// Extract account from context.Value
	accountVal := ctx.Value("providers.Account")
	if accountVal != nil {
		if account, ok := accountVal.(providers.Account); ok {
			return account, nil
		}
	}

	return providers.Account{}, fmt.Errorf("account not found in context")
}

// SupportsResourceType checks if service-specific APIs support this resource type
func (s *ServiceSpecificSource) SupportsResourceType(resourceType string) bool {
	// Service-specific source supports all types that have GetServiceMap implementations
	// This is the ultimate fallback, so it should support anything
	return true
}

// Priority returns the priority of this source (lower = higher priority)
func (s *ServiceSpecificSource) Priority() int {
	return 4 // Lowest priority - only use when other sources fail
}

// Name returns the name of this source
func (s *ServiceSpecificSource) Name() string {
	return "service-specific-fallback"
}

// IsAvailable checks if service-specific APIs are available
func (s *ServiceSpecificSource) IsAvailable(ctx context.Context, cfg aws.Config, account providers.Account) bool {
	// Service-specific APIs are always available as long as we have credentials
	// This is the ultimate fallback
	return true
}
