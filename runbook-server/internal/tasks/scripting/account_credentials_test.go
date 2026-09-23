package scripting

import (
	"context"
	"errors"
	"testing"
	"time"

	"log/slog"
	"nudgebee/runbook/internal/tasks/scripting/executors"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/cloud"
	"nudgebee/runbook/services/security"
)

func testTaskContext(ctx context.Context) *testutils.MockTaskContext {
	return &testutils.MockTaskContext{
		Ctx:     ctx,
		Logger:  slog.Default(),
		Tenant:  "11111111-1111-1111-1111-111111111111",
		Account: "22222222-2222-2222-2222-222222222222",
		User:    "33333333-3333-3333-3333-333333333333",
	}
}

func stubCredentials(t *testing.T, creds cloud.AccountCredentials, err error) {
	t.Helper()
	original := getAccountCredentials
	getAccountCredentials = func(_ *security.RequestContext, _ string, _ time.Duration) (cloud.AccountCredentials, error) {
		return creds, err
	}
	t.Cleanup(func() { getAccountCredentials = original })
}

// A step carrying its own credentials must keep working exactly as before.
func TestAccountCredentialsDoNotOverrideStepValues(t *testing.T) {
	stubCredentials(t, cloud.AccountCredentials{Env: map[string]string{
		"AWS_ACCESS_KEY_ID":     "from-account",
		"AWS_SECRET_ACCESS_KEY": "from-account",
		"AWS_REGION":            "us-east-1",
	}}, nil)

	config := executors.ExecutionConfig{Env: map[string]string{"AWS_ACCESS_KEY_ID": "from-step"}}
	if err := applyAccountCredentials(testTaskContext(context.Background()), &config, "acct"); err != nil {
		t.Fatalf("applying credentials: %v", err)
	}

	if got := config.Env["AWS_ACCESS_KEY_ID"]; got != "from-step" {
		t.Fatalf("step value was overwritten: %q", got)
	}
	if got := config.Env["AWS_SECRET_ACCESS_KEY"]; got != "from-account" {
		t.Fatalf("account value missing: %q", got)
	}
}

// With real credentials present, falling back to the host identity can only
// mask a mistake.
func TestAccountCredentialsDisableMetadataFallback(t *testing.T) {
	stubCredentials(t, cloud.AccountCredentials{Env: map[string]string{"AWS_ACCESS_KEY_ID": "x"}}, nil)

	config := executors.ExecutionConfig{}
	if err := applyAccountCredentials(testTaskContext(context.Background()), &config, "acct"); err != nil {
		t.Fatalf("applying credentials: %v", err)
	}

	if got := config.Env["AWS_EC2_METADATA_DISABLED"]; got != "true" {
		t.Fatalf("metadata fallback not disabled: %q", got)
	}
}

// Failing is the point: running anyway means acting as Nudgebee and reporting a
// permissions error that reads like the customer's fault.
func TestAccountCredentialsFailureStopsTheStep(t *testing.T) {
	stubCredentials(t, cloud.AccountCredentials{}, errors.New("assume role AccessDenied"))

	config := executors.ExecutionConfig{}
	err := applyAccountCredentials(testTaskContext(context.Background()), &config, "acct")
	if err == nil {
		t.Fatal("expected an error when credentials cannot be resolved")
	}
	if len(config.Env) != 0 {
		t.Fatalf("environment was modified on failure: %v", config.Env)
	}
}

func TestCredentialDurationFollowsTheStepDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	got := credentialDurationFor(testTaskContext(ctx))
	if got <= 19*time.Minute || got > 20*time.Minute {
		t.Fatalf("duration = %s, want just under 20m", got)
	}

	if got := credentialDurationFor(testTaskContext(context.Background())); got != 0 {
		t.Fatalf("duration without a deadline = %s, want 0 so the provider picks", got)
	}
}
