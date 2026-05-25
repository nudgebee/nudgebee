package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeAwsService satisfies the awsService interface and records the most recent
// ApplyCommand request, so dispatch tests can verify the right service was hit.
// All other methods return ErrUnsupported — they're never called by the dispatch
// path under test.
type fakeAwsService struct {
	DefaultAwsServiceImpl
	lastCommand *providers.ApplyCommandRequest
	response    providers.ApplyCommandResponse
	returnErr   error
}

func (f *fakeAwsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, errors.ErrUnsupported
}
func (f *fakeAwsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, errors.ErrUnsupported
}
func (f *fakeAwsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	return nil, errors.ErrUnsupported
}
func (f *fakeAwsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}
func (f *fakeAwsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	cmd := command
	f.lastCommand = &cmd
	return f.response, f.returnErr
}
func (f *fakeAwsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", errors.ErrUnsupported
}
func (f *fakeAwsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, errors.ErrUnsupported
}

// swapAwsService temporarily replaces awsServiceMap[key] with the fake and
// restores the original on test cleanup. Tests that mutate awsServiceMap MUST
// use this helper to avoid leaking state into other tests in the package.
func swapAwsService(t *testing.T, key string, svc awsService) {
	t.Helper()
	original, existed := awsServiceMap[key]
	awsServiceMap[key] = svc
	t.Cleanup(func() {
		if existed {
			awsServiceMap[key] = original
		} else {
			delete(awsServiceMap, key)
		}
	})
}

// TestGetAwsService_StripsPrefix is the regression for the bug that caused
// `cloud_apply_command` with ServiceName "AmazonEC2" to fail with
// "service not found": the production code now calls GetAwsService instead
// of indexing awsServiceMap directly with strings.ToLower.
func TestGetAwsService_StripsPrefix(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectFound  bool
		expectMapKey string
	}{
		{"frontend AmazonEC2 service name", "AmazonEC2", true, "ec2"},
		{"AWS-prefixed alternate spelling", "AWSEC2", true, "ec2"},
		{"already lowercase bare name", "ec2", true, "ec2"},
		{"uppercase bare name", "EC2", true, "ec2"},
		{"AmazonRDS resolves to rds", "AmazonRDS", true, "rds"},
		{"AmazonECS resolves to ecs", "AmazonECS", true, "ecs"},
		{"AWSLambda resolves to lambda", "AWSLambda", true, "lambda"},
		{"empty input not found", "", false, ""},
		{"unknown service not found", "AmazonDoesNotExist", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, ok := GetAwsService(tt.input)
			assert.Equal(t, tt.expectFound, ok)
			if tt.expectFound {
				assert.NotNil(t, svc)
				expected, expectedOk := awsServiceMap[tt.expectMapKey]
				assert.True(t, expectedOk)
				assert.Equal(t, expected, svc, "should resolve to the same instance as direct map lookup")
			}
		})
	}
}

// TestAwsApplyCommand_RoutesToServiceByName verifies that the provider-level
// ApplyCommand dispatches to the right service implementation when given the
// frontend's "Amazon*" service names.
func TestAwsApplyCommand_RoutesToServiceByName(t *testing.T) {
	fake := &fakeAwsService{
		response: providers.ApplyCommandResponse{Success: true, Message: "fake-ok"},
	}
	swapAwsService(t, "ec2", fake)

	provider := &awsProvider{}
	resp, err := provider.ApplyCommand(nil, providers.Account{}, providers.ApplyCommandRequest{
		ServiceName: "AmazonEC2",
		ResourceId:  "i-test",
		Command:     "stop",
	})

	assert.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "fake-ok", resp.Message)
	if assert.NotNil(t, fake.lastCommand) {
		assert.Equal(t, "AmazonEC2", fake.lastCommand.ServiceName)
		assert.Equal(t, "i-test", fake.lastCommand.ResourceId)
		assert.Equal(t, "stop", fake.lastCommand.Command)
	}
}

func TestAwsApplyCommand_UnknownServiceReturnsNotFound(t *testing.T) {
	provider := &awsProvider{}
	resp, err := provider.ApplyCommand(nil, providers.Account{}, providers.ApplyCommandRequest{
		ServiceName: "AmazonDoesNotExist",
		ResourceId:  "i-test",
		Command:     "stop",
	})

	assert.Error(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "AmazonDoesNotExist")
	assert.Contains(t, resp.Message, "not found")
}
