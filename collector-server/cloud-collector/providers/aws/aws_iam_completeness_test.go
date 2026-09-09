package aws

import (
	"context"
	"errors"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
)

func okCheck(rule string) iamCheck {
	return func(IAMClientAPI, providers.CloudProviderContext, providers.Account) ([]providers.Recommendation, error) {
		return []providers.Recommendation{{RuleName: rule}}, nil
	}
}

func failingCheck(msg string) iamCheck {
	return func(IAMClientAPI, providers.CloudProviderContext, providers.Account) ([]providers.Recommendation, error) {
		return nil, errors.New(msg)
	}
}

// TestRunIAMChecksReportsIncompleteScanAsError is the reason this seam exists.
// A producer that returns success has its result treated as the complete truth
// for the service, and every Open recommendation absent from it is archived —
// so a check that fails quietly retires security findings that are still true.
func TestRunIAMChecksReportsIncompleteScanAsError(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())

	recs, err := runIAMChecks(
		[]iamCheck{okCheck("mfa"), failingCheck("throttled: Rate exceeded"), okCheck("password_policy")},
		nil, ctx, providers.Account{},
	)

	assert.Error(t, err, "a partial scan must not be reported as a successful one")
	assert.Contains(t, err.Error(), "1 of 3")
	assert.Contains(t, err.Error(), "Rate exceeded", "the underlying cause must survive for the operator")

	// The successful checks' findings still come back; the caller discards them
	// because of the error, but the helper must not silently drop them.
	assert.Len(t, recs, 2)
}

func TestRunIAMChecksSucceedsWhenEveryCheckCompletes(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())

	recs, err := runIAMChecks(
		[]iamCheck{okCheck("mfa"), okCheck("root_account")},
		nil, ctx, providers.Account{},
	)

	assert.NoError(t, err, "a complete scan must stay a success, or IAM findings would never refresh")
	assert.Len(t, recs, 2)
}

// TestRunIAMChecksErrorsWhenEveryCheckFails guards the worst case: without the
// error, an empty result would archive every Open IAM finding at once.
func TestRunIAMChecksErrorsWhenEveryCheckFails(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())

	recs, err := runIAMChecks(
		[]iamCheck{failingCheck("denied"), failingCheck("denied")},
		nil, ctx, providers.Account{},
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "2 of 2")
	assert.Empty(t, recs)
}

// TestRunIAMChecksStopsOnCancelledContext: once the context is done every
// remaining check is a doomed API call and a duplicate warning, so the loop
// stops and still reports the scan as incomplete.
func TestRunIAMChecksStopsOnCancelledContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := providers.NewCloudProviderContext(cancelled)

	ran := 0
	counting := iamCheck(func(IAMClientAPI, providers.CloudProviderContext, providers.Account) ([]providers.Recommendation, error) {
		ran++
		return []providers.Recommendation{{RuleName: "should_not_run"}}, nil
	})

	recs, err := runIAMChecks([]iamCheck{counting, counting}, nil, ctx, providers.Account{})

	assert.Error(t, err, "a cancelled scan is incomplete, not an empty success")
	assert.Zero(t, ran, "no check should run once the context is dead")
	assert.Empty(t, recs)
}
