package servicemap

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// AWSConfigSource queries AWS Config for configuration-based resource relationships
type AWSConfigSource struct {
	provider AWSConfigProviderInterface
	logger   *slog.Logger
}

// AWSConfigProviderInterface defines the methods we need from awsProvider for AWS Config
type AWSConfigProviderInterface interface {
	QueryServiceMapWithConfig(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryServiceMapRequest) (providers.QueryServiceMapResponse, error)
}

// NewAWSConfigSource creates a new AWS Config relationship source
func NewAWSConfigSource(provider AWSConfigProviderInterface, logger *slog.Logger) *AWSConfigSource {
	return &AWSConfigSource{
		provider: provider,
		logger:   logger,
	}
}

// GetRelationships queries AWS Config for resource relationships
func (a *AWSConfigSource) GetRelationships(ctx context.Context, request QueryRequest) (QueryResponse, error) {
	startTime := time.Now()

	// Convert providers.CloudProviderContext from context if available
	providerCtx, ok := ctx.(providers.CloudProviderContext)
	if !ok {
		// Create a basic context wrapper
		providerCtx = providers.NewCloudProviderContext(ctx)
	}

	// Extract AWS config and account from request/context
	account, err := a.extractAccountFromRequest(ctx, request)
	if err != nil {
		return QueryResponse{
			Applications: []providers.ServiceMapApplication{},
			Errors:       []error{fmt.Errorf("failed to extract account: %w", err)},
			Metadata: SourceMetadata{
				Source:        a.Name(),
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

	// Call the existing queryServiceMapWithConfig method
	response, err := a.provider.QueryServiceMapWithConfig(providerCtx, account, serviceMapRequest)

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
			Source:           a.Name(),
			QueriedAt:        startTime,
			ExecutionTime:    time.Since(startTime),
			ResourcesQueried: len(response.Applications),
		},
	}, nil
}

// extractAccountFromRequest extracts account info from context/request
func (a *AWSConfigSource) extractAccountFromRequest(
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

// SupportsResourceType checks if AWS Config can query this resource type
func (a *AWSConfigSource) SupportsResourceType(resourceType string) bool {
	// AWS Config supports most AWS resource types
	// See: https://docs.aws.amazon.com/config/latest/developerguide/resource-config-reference.html
	supportedTypes := map[string]bool{
		"ec2":            true,
		"rds":            true,
		"lambda":         true,
		"ecs":            true,
		"s3":             true,
		"dynamodb":       true,
		"elb":            true,
		"alb":            true,
		"nlb":            true,
		"vpc":            true,
		"subnet":         true,
		"sg":             true, // Security Group
		"iam":            true,
		"elasticache":    true,
		"redshift":       true,
		"apigateway":     true,
		"cloudfront":     true,
		"sns":            true,
		"sqs":            true,
		"kinesis":        true,
		"cloudwatch":     true,
		"cloudtrail":     true,
		"kms":            true,
		"secretsmanager": true,
		"ssm":            true,
		"efs":            true,
		"fsx":            true,
	}

	return supportedTypes[resourceType]
}

// Priority returns the priority of this source (lower = higher priority)
func (a *AWSConfigSource) Priority() int {
	return 1 // Highest priority - AWS Config is the authoritative source for configuration
}

// Name returns the name of this source
func (a *AWSConfigSource) Name() string {
	return "aws-config"
}

// IsAvailable checks if AWS Config is enabled for the account
func (a *AWSConfigSource) IsAvailable(ctx context.Context, cfg aws.Config, account providers.Account) bool {
	// TODO: Check if AWS Config is actually enabled
	// For now, always return true and let GetRelationships handle errors gracefully
	// In production, you could:
	// 1. Call ConfigService.DescribeConfigurationRecorders()
	// 2. Check if configuration recorder is active
	// 3. Cache result to avoid repeated API calls
	return true
}
