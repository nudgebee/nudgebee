package tasks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"nudgebee/runbook/internal/logsafe"
	"nudgebee/runbook/internal/tasks/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	temporalLog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const scrubCanary = "Zx9-canary-7Qw"

func canaryValues() []string {
	return logsafe.SecretValues(map[string]any{"k": scrubCanary})
}

func loadedSecrets(values []string) *stepSecrets {
	return newStepSecrets(context.Background(), func(context.Context, string, string) ([]string, error) { return values, nil }, "t", "a", temporalLog.NewStructuredLogger(slog.Default()))
}

func captureLogger(buf *bytes.Buffer) temporalLog.Logger {
	return temporalLog.NewStructuredLogger(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func TestScrubbingLogger_MasksMessageKeyvalsAndWith(t *testing.T) {
	var buf bytes.Buffer
	logger := newScrubbingLogger(captureLogger(&buf), loadedSecrets(canaryValues()))

	logger.Info("calling https://hooks.example.com/"+scrubCanary,
		"params", map[string]any{"url": "https://x?token=" + url.QueryEscape(scrubCanary), "list": []any{scrubCanary}},
		"error", errors.New("auth failed for "+scrubCanary),
		"headers", map[string]string{"Authorization": "Bearer " + scrubCanary},
	)
	temporalLog.With(logger, "token", scrubCanary).Warn("with")
	logger.(temporalLog.WithLogger).With("token", scrubCanary).Error("direct with")

	assert.NotContains(t, buf.String(), scrubCanary)
	assert.Contains(t, buf.String(), "auth failed for ***")
}

func TestScrubbingLogger_KeepsValuesWithNothingToHide(t *testing.T) {
	var buf bytes.Buffer
	logger := newScrubbingLogger(captureLogger(&buf), loadedSecrets(canaryValues()))

	logger.Info("step done", "count", 3, "ok", true)

	assert.Contains(t, buf.String(), `"count":3`)
	assert.Contains(t, buf.String(), `"ok":true`)
}

func TestStepSecrets_LoadsOnceAndFailsOpen(t *testing.T) {
	calls := 0
	var buf bytes.Buffer
	failing := newStepSecrets(context.Background(), func(context.Context, string, string) ([]string, error) {
		calls++
		return nil, errors.New("config store down")
	}, "t", "a", captureLogger(&buf))

	logger := newScrubbingLogger(captureLogger(&buf), failing)
	logger.Info("first")
	logger.Info("second")
	err := scrubError(errors.New("boom"), failing)

	assert.Equal(t, 1, calls)
	assert.EqualError(t, err, "boom", "a failed load leaves the error alone, it never fails the step")
	assert.Contains(t, buf.String(), "could not load secrets")
}

func TestNewCachedSecretLoader_LoadsOncePerAccountWithinTTL(t *testing.T) {
	calls := map[string]int{}
	load := NewCachedSecretLoader(func(_ context.Context, tenantID, accountID string) ([]string, error) {
		calls[tenantID+":"+accountID]++
		return []string{accountID + "-secret"}, nil
	})

	for range 3 {
		v, err := load(context.Background(), "t", "a")
		require.NoError(t, err)
		assert.Equal(t, []string{"a-secret"}, v)
	}
	_, _ = load(context.Background(), "t", "b")

	assert.Equal(t, map[string]int{"t:a": 1, "t:b": 1}, calls)
}

func TestScrubError_PlainErrorKeepsItsSDKTypeName(t *testing.T) {
	inner := &url.Error{Op: "Get", URL: "https://api.example.com?api_key=" + scrubCanary, Err: errors.New("no such host")}
	err := scrubError(fmt.Errorf("failed to execute request: %w", inner), loadedSecrets(canaryValues()))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), scrubCanary)
	assert.Contains(t, err.Error(), "api_key=***")
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, "wrapError", appErr.Type(), "a retry policy naming the original type still matches")
	assert.False(t, appErr.NonRetryable())
	assert.Nil(t, errors.Unwrap(err), "the cause held the secret, so it is not sent")
}

func TestScrubError_ApplicationErrorKeepsTypeAndRetryability(t *testing.T) {
	orig := temporal.NewNonRetryableApplicationError("rejected token "+scrubCanary, "AuthRejected", errors.New("cause "+scrubCanary))
	err := scrubError(orig, loadedSecrets(canaryValues()))

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, "AuthRejected", appErr.Type())
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, "rejected token ***: cause ***", appErr.Message())
	assert.NotContains(t, err.Error(), scrubCanary)
}

func TestScrubError_KeepsCancellationSoAStoppedStepReadsAsCanceled(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		inner := &url.Error{Op: "Post", URL: "https://hooks.example.com/" + scrubCanary, Err: sentinel}
		err := scrubError(fmt.Errorf("failed to execute request: %w", inner), loadedSecrets(canaryValues()))

		assert.ErrorIs(t, err, sentinel)
		assert.NotContains(t, err.Error(), scrubCanary)
	}
}

func TestScrubError_ReturnsUntouched(t *testing.T) {
	secrets := loadedSecrets(canaryValues())
	clean := errors.New("nothing secret here")
	canceled := temporal.NewCanceledError()

	assert.Same(t, clean, scrubError(clean, secrets), "an error with nothing to hide is not rebuilt")
	assert.Equal(t, activity.ErrResultPending, scrubError(activity.ErrResultPending, secrets))
	assert.Equal(t, canceled, scrubError(canceled, secrets))
	assert.NoError(t, scrubError(nil, secrets))
}

func TestSDKErrorType(t *testing.T) {
	assert.Equal(t, "", sdkErrorType(errors.New("x")))
	assert.Equal(t, "wrapError", sdkErrorType(fmt.Errorf("x: %w", errors.New("y"))))
	assert.Equal(t, "Error", sdkErrorType(&url.Error{}))
}

// leakyTask logs and fails with the credential it was handed, as a task that
// wraps a library error carrying its request URL would.
type leakyTask struct{}

func (leakyTask) GetName() string             { return "k8s.pod_delete" }
func (leakyTask) GetDescription() string      { return "test double" }
func (leakyTask) GetDisplayName() string      { return "test double" }
func (leakyTask) InputSchema() *types.Schema  { return nil }
func (leakyTask) OutputSchema() *types.Schema { return nil }
func (leakyTask) Execute(ctx types.TaskContext, params map[string]any) (any, error) {
	ctx.GetLogger().Info("calling", "params", params)
	return nil, fmt.Errorf("request to %v failed", params["url"])
}

func stepParams(extra map[string]any) map[string]any {
	p := map[string]any{
		ParamTenantID:   "t",
		ParamAccountID:  "acct-a",
		ParamWorkflowID: "wf-1",
		ParamUserID:     "last-editor",
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func TestTaskWrapper_MasksSecretsInStepLogsAndErrors(t *testing.T) {
	var buf bytes.Buffer
	tw := &TaskWrapper{
		Task: leakyTask{},
		Secrets: func(_ context.Context, tenantID, accountID string) ([]string, error) {
			assert.Equal(t, "t", tenantID)
			assert.Equal(t, "acct-a", accountID)
			return canaryValues(), nil
		},
	}

	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(captureLogger(&buf))
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(tw.Execute, activity.RegisterOptions{Name: tw.Task.GetName()})
	_, err := env.ExecuteActivity(tw.Task.GetName(), stepParams(map[string]any{
		"url": "https://hooks.example.com/" + scrubCanary,
	}))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), scrubCanary)
	assert.Contains(t, err.Error(), "https://hooks.example.com/***")
	assert.Contains(t, buf.String(), `"msg":"calling"`, "the task's log line was written")
	assert.NotContains(t, buf.String(), scrubCanary)
}

func TestTaskWrapper_WithoutALoaderMasksNothing(t *testing.T) {
	tw := &TaskWrapper{Task: leakyTask{}}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.SetTestTimeout(5 * time.Second)
	env.RegisterActivityWithOptions(tw.Execute, activity.RegisterOptions{Name: tw.Task.GetName()})
	_, err := env.ExecuteActivity(tw.Task.GetName(), stepParams(map[string]any{"url": "https://h/x"}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "request to https://h/x failed")
}

// Temporal reports an application error wrapped in another error by the
// wrapper's type, retryable, so masking must not promote the inner one.
func TestScrubError_WrappedApplicationErrorKeepsTheWrappersOutcome(t *testing.T) {
	inner := temporal.NewNonRetryableApplicationError("rejected", "AuthRejected", nil)
	err := scrubError(fmt.Errorf("call with %s: %w", scrubCanary, inner), loadedSecrets(canaryValues()))

	appErr, ok := err.(*temporal.ApplicationError)
	require.True(t, ok)
	assert.Equal(t, "wrapError", appErr.Type())
	assert.False(t, appErr.NonRetryable())
	assert.NotContains(t, err.Error(), scrubCanary)
}

func TestScrubbingLogger_MasksTypedValuesNestedInMapsAndLists(t *testing.T) {
	var buf bytes.Buffer
	logger := newScrubbingLogger(captureLogger(&buf), loadedSecrets(canaryValues()))

	logger.Info("x",
		"headers", map[string]any{"Authorization": []string{"Bearer " + scrubCanary}},
		"items", []any{map[string]string{"token": scrubCanary}, errors.New("bad " + scrubCanary)},
	)

	assert.NotContains(t, buf.String(), scrubCanary)
}

// A step stopped mid-call returns its error after its context is canceled; the
// secrets that mask that error must still load.
func TestStepSecrets_LoadOutlivesACanceledStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secrets := newStepSecrets(ctx, func(loadCtx context.Context, _, _ string) ([]string, error) {
		if err := loadCtx.Err(); err != nil {
			return nil, err
		}
		return canaryValues(), nil
	}, "t", "a", temporalLog.NewStructuredLogger(slog.Default()))

	err := scrubError(fmt.Errorf("failed: %s: %w", scrubCanary, context.Canceled), secrets)

	assert.NotContains(t, err.Error(), scrubCanary)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestScrubError_BareContextErrorDoesNotWaitOnASecretLoad(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		calls := 0
		secrets := newStepSecrets(context.Background(), func(context.Context, string, string) ([]string, error) {
			calls++
			return canaryValues(), nil
		}, "t", "a", temporalLog.NewStructuredLogger(slog.Default()))

		assert.Equal(t, sentinel, scrubError(sentinel, secrets))
		assert.Zero(t, calls)
	}
}
