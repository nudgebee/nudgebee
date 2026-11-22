package workflow

import (
	"strings"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
)

func TestEscapeTemporalString(t *testing.T) {
	assert.Equal(t, "plain-id", escapeTemporalString("plain-id"))
	assert.Equal(t, "it''s", escapeTemporalString("it's"))
	// Backslashes are escaped before quotes, so an escaped quote can't be
	// turned back into a bare one.
	assert.Equal(t, `a\\b`, escapeTemporalString(`a\b`))
	assert.Equal(t, `a\\''b`, escapeTemporalString(`a\'b`))
}

func TestCheckTemporalIDs(t *testing.T) {
	for _, id := range []string{
		"fb836ea7-fa0a-4044-a3d2-0375636a43c9",
		"dry-run-fb836ea7-fa0a-4044-a3d2-0375636a43c9",
		"workflow-schedule-fb836ea7-fa0a-4044-a3d2-0375636a43c9-0",
		"019db96b-d435-7737-ba87-71e82729b65e",
		"test-account",
	} {
		assert.NoError(t, checkTemporalIDs(id), id)
	}
	for _, id := range []string{"", "a'b", "a b", `a\b`, "a(b", "-leading-dash", strings.Repeat("a", 256)} {
		assert.Error(t, checkTemporalIDs(id), "%q", id)
	}
}

func newTemporalQueryTestService() (*Service, *MockTemporalClient) {
	mockTemporalClient := &MockTemporalClient{}
	dataConverter := converter.GetDefaultDataConverter()
	mockStore := new(MockWorkflowStore)
	executor := &WorkflowExecutor{temporalClient: mockTemporalClient, workflowStore: mockStore, dataConverter: dataConverter}
	service := NewService(mockTemporalClient, mockStore, dataConverter, tasks.NewInitializedTaskRegistry(), executor, new(MockConfigService))
	return service, mockTemporalClient
}

// A non-plain ID is refused before any visibility query is sent.
func TestExecutionAPIs_RefuseNonPlainIDs(t *testing.T) {
	service, mockTemporalClient := newTemporalQueryTestService()
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})
	bad := "run'1"

	_, err := service.ResolveTemporalWorkflowID(sc, "test-account", "wf-1", bad)
	assert.Error(t, err)
	_, err = service.ListWorkflowExecutions(sc, "test-account", bad, model.ListWorkflowExecutionRequest{})
	assert.Error(t, err)
	_, err = service.GetDetailedWorkflowExecution(sc, "test-account", "wf-1", bad)
	assert.Error(t, err)

	mockTemporalClient.AssertNotCalled(t, "ListWorkflow", mock.Anything, mock.Anything)
}

// The tenant condition comes first in the queries built from caller IDs.
func TestExecutionQueries_StartWithTenantCondition(t *testing.T) {
	service, mockTemporalClient := newTemporalQueryTestService()
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	var queries []string
	mockTemporalClient.On("ListWorkflow", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		queries = append(queries, args.Get(1).(*workflowservice.ListWorkflowExecutionsRequest).Query)
	}).Return(&workflowservice.ListWorkflowExecutionsResponse{}, nil)

	_, err := service.ListWorkflowExecutions(sc, "test-account", "wf-1", model.ListWorkflowExecutionRequest{})
	require.NoError(t, err)
	_, _ = service.ResolveTemporalWorkflowID(sc, "test-account", "wf-1", "run-1")

	require.NotEmpty(t, queries)
	for _, q := range queries {
		assert.True(t, strings.HasPrefix(q, model.SearchAttrTenantID+"='test-tenant' "), q)
		assert.Contains(t, q, model.SearchAttrAccountID+"='test-account'", q)
	}
}
