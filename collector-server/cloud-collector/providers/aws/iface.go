package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
)

// ResourceMetadata contains detailed metadata about an AWS resource
// This provides a unified way to get resource details without service-specific API calls
type ResourceMetadata struct {
	ResourceID     string            // The resource identifier (instance ID, DB identifier, etc.)
	ResourceARN    string            // Full ARN of the resource
	VpcID          string            // VPC ID (if resource is in a VPC)
	PrivateIP      string            // Primary private IP address
	PublicIP       string            // Public IP address (if applicable)
	Port           int               // Primary port (for databases, load balancers, etc.)
	SecurityGroups []string          // Security group IDs
	Subnets        []string          // Subnet IDs
	Status         string            // Resource status (running, available, etc.)
	Tags           map[string]string // Resource tags
	Metadata       map[string]any    // Additional service-specific metadata
}

type awsService interface {
	QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error)
	ListMetrics(ctx providers.CloudProviderContext, account providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error)
	GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error)
	GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error)
	ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error
	ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error)
	GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error)
	GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error)
	// DescribeResource returns detailed metadata about a specific resource
	// This is the preferred way to get resource details (VPC ID, IP address, etc.)
	DescribeResource(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (*ResourceMetadata, error)
	// GetResourcesByIds fetches specific resources by their IDs using server-side filtering
	// (e.g., DescribeInstances with InstanceIds for EC2). Returns ErrUnsupported if
	// the service doesn't support targeted fetching, so the caller can fall back to GetResources.
	GetResourcesByIds(ctx providers.CloudProviderContext, account providers.Account, region string, resourceIds []string) ([]providers.Resource, error)
	// IsGlobal reports whether the service's listing API is global (returns resources
	// across all regions in a single call, e.g. S3 ListBuckets, IAM ListUsers).
	// When true, the caller invokes GetResources exactly once and the implementation
	// is responsible for setting Resource.Region to each item's actual location.
	IsGlobal() bool
}

// DefaultAwsServiceImpl provides default implementations for awsService interface
// Services can embed this to get default implementations and only override what they need
type DefaultAwsServiceImpl struct{}

func (d *DefaultAwsServiceImpl) DescribeResource(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (*ResourceMetadata, error) {
	return nil, errors.ErrUnsupported
}

func (d *DefaultAwsServiceImpl) GetResourcesByIds(ctx providers.CloudProviderContext, account providers.Account, region string, resourceIds []string) ([]providers.Resource, error) {
	return nil, errors.ErrUnsupported
}

func (d *DefaultAwsServiceImpl) ListMetrics(ctx providers.CloudProviderContext, account providers.Account, request providers.ListMetricsRequest) (providers.ListMetricsResponse, error) {
	return providers.ListMetricsResponse{Metrics: []providers.AvailableMetric{}}, nil
}

func (d *DefaultAwsServiceImpl) IsGlobal() bool {
	return false
}
