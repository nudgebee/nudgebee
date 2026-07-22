//go:build e2e

package observability

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracesMissingAllQueryInputs(t *testing.T) {
	// Empty inputs are valid by design: Execute compiles a bounded, ordered
	// default query and forwards it to services-server (no local "required"
	// validation), so this exercises the live trace backend.
	testutils.RequireEnv(t, "TEST_TENANT_ID", "TEST_OBSERVABILITY_ACCOUNT_ID", "TEST_USER_ID")
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{})
	assert.NoError(t, err)
}

func TestTracesEmptyQueryNoFilters(t *testing.T) {
	// An empty query string is valid by design (defaults are applied before
	// the backend call), so this exercises the live trace backend.
	testutils.RequireEnv(t, "TEST_TENANT_ID", "TEST_OBSERVABILITY_ACCOUNT_ID", "TEST_USER_ID")
	task := &TracesTask{}
	taskCtx := testutils.NewTestTaskContext(os.Getenv("TEST_TENANT_ID"), os.Getenv("TEST_OBSERVABILITY_ACCOUNT_ID"), os.Getenv("TEST_USER_ID"), slog.Default())

	_, err := task.Execute(taskCtx, map[string]any{"query": ""})
	assert.NoError(t, err)
}
