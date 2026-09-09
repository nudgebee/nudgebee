package aws

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"

	"github.com/aws/aws-sdk-go-v2/aws"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/stretchr/testify/require"
)

type targetedFetchTestCtx struct{ ctx context.Context }

func (c *targetedFetchTestCtx) GetContext() context.Context                   { return c.ctx }
func (c *targetedFetchTestCtx) GetLogger() *slog.Logger                       { return slog.Default() }
func (c *targetedFetchTestCtx) GetSecurityContext() *security.SecurityContext { return nil }

// Services whose EventBridge rules run update_cloud_resource must resolve a
// resource by id without scanning the whole region.
//
// ListResources only calls GetResourcesByIds when ResourceIds is non-empty, and
// treats ErrUnsupported as "no targeted path — do a full GetResources sweep".
// For a service that emits one event per resource change, that fallback means one
// whole-region inventory scan per delivered SQS message. Lambda had no targeted
// path, and on 2026-09-04 a bulk delete of 109 functions turned into 109 full
// us-west-2 ListFunctions sweeps at ~100s each: processBatchConcurrent waits for
// the slowest message in a batch before deleting and receiving again, so the
// EventBridge queue stalled for 57 minutes and 30 events aged past the 1h
// staleness guard and were dropped.
//
// An empty id list is the cheap probe for "is the targeted path implemented" —
// every implementation returns before touching AWS, and only the inherited
// DefaultAwsServiceImpl reports ErrUnsupported.
func TestResourceSyncServicesImplementTargetedFetch(t *testing.T) {
	ctx := &targetedFetchTestCtx{ctx: context.Background()}

	services := map[string]awsService{
		"lambda": &awsLambda{},
		"ecs":    &amazonEcs{},
		"ec2":    &amazonEc2{},
	}

	for name, svc := range services {
		t.Run(name, func(t *testing.T) {
			_, err := svc.GetResourcesByIds(ctx, providers.Account{}, "us-east-1", nil)
			require.False(t, errors.Is(err, errors.ErrUnsupported),
				"%s must implement GetResourcesByIds; inheriting the default sends every "+
					"per-resource event back to a full-region scan", name)
		})
	}

	// Control: the inherited default still reports unsupported, so the assertion
	// above is actually discriminating and not vacuously true.
	_, err := (&DefaultAwsServiceImpl{}).GetResourcesByIds(ctx, providers.Account{}, "us-east-1", nil)
	require.ErrorIs(t, err, errors.ErrUnsupported)
}

// The targeted path (GetFunction) and the bulk path (ListFunctions) write the
// same cloud_resourses row, so they must build it from the same fields.
// GetFunction returns seven fields ListFunctions omits; structToMap marshals the
// whole struct into Meta with no omitempty, and the UPSERT merges Meta with `||`,
// so any field left populated sticks to the row as a key the periodic sync never
// writes or refreshes. State counts twice: lambdaStatusToNbStatus maps empty to
// Active deliberately, so leaving it set also flips the stored status for
// Inactive/Failed functions depending on which path wrote last.
//
// The field list is the SDK's own, from the ListFunctions doc comment. If a newer
// SDK adds another GetFunction-only field, this test will not catch it — re-read
// that comment on upgrade.
func TestClearGetFunctionOnlyFieldsLeavesListFunctionsShape(t *testing.T) {
	reason := "because"
	populated := lambdatypes.FunctionConfiguration{
		FunctionName:               aws.String("fn"),
		State:                      lambdatypes.StateInactive,
		StateReason:                &reason,
		StateReasonCode:            lambdatypes.StateReasonCodeIdle,
		LastUpdateStatus:           lambdatypes.LastUpdateStatusFailed,
		LastUpdateStatusReason:     &reason,
		LastUpdateStatusReasonCode: lambdatypes.LastUpdateStatusReasonCodeInvalidConfiguration,
		RuntimeVersionConfig:       &lambdatypes.RuntimeVersionConfig{RuntimeVersionArn: aws.String("arn")},
	}

	clearGetFunctionOnlyFields(&populated)

	require.Equal(t, lambdatypes.FunctionConfiguration{FunctionName: aws.String("fn")}, populated,
		"every GetFunction-only field must be zeroed; anything left diverges from the bulk sync")

	// The status half of the same property: a cleared State derives Active, which
	// is what ListFunctions-sourced rows derive.
	require.Equal(t, providers.ResourceStatusActive, lambdaStatusToNbStatus((*string)(&populated.State)))
}
