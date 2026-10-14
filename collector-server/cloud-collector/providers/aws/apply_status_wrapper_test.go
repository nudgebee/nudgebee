package aws

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
)

// The service map holds audit wrappers, so the apply-status check must reach
// the wrapped service. Both cases below return before any AWS call.
func TestGetApplyStatusReachesWrappedService(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	p := &awsProvider{}

	// EC2 implements the check: a gp3 upgrade without a volume id is rejected by
	// the EC2 service itself, not answered "unsupported" by the wrapper.
	_, err := p.GetApplyStatus(ctx, providers.Account{}, providers.Recommendation{
		RuleName:            "aws_ec2_ebs_generation_upgrade",
		ResourceServiceName: ServiceNameEc2,
		Data:                map[string]any{},
	})
	assert.Error(t, err)
	assert.False(t, errors.Is(err, errors.ErrUnsupported), err)
	assert.Contains(t, err.Error(), "missing volume_id")

	// Other EC2 rules have no asynchronous apply.
	_, err = p.GetApplyStatus(ctx, providers.Account{}, providers.Recommendation{
		RuleName:            "aws_ec2_some_other_rule",
		ResourceServiceName: ServiceNameEc2,
	})
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestAuditWrapperApplyStatusUnsupportedWithoutChecker(t *testing.T) {
	wrapped := &auditedAwsService{inner: &awsLambda{}, serviceName: "lambda"}
	_, err := wrapped.GetApplyStatus(providers.NewCloudProviderContext(context.Background()), providers.Account{}, providers.Recommendation{})
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestIsVolumeModificationNotFound(t *testing.T) {
	notFound := &smithy.GenericAPIError{Code: "InvalidVolumeModification.NotFound", Message: "Modification for volume 'vol-0123456789abcdef0' does not exist."}
	assert.True(t, isVolumeModificationNotFound(fmt.Errorf("describe: %w", notFound)))
	assert.False(t, isVolumeModificationNotFound(&smithy.GenericAPIError{Code: "UnauthorizedOperation"}))
	assert.False(t, isVolumeModificationNotFound(errors.New("timeout")))
}
