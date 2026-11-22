package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// readableAccountsBuilder is a caller who reads accounts a, b and c, and
// records whether the handler let a user-less caller through.
type readableAccountsBuilder struct{ allowedSystemCaller bool }

func (b *readableAccountsBuilder) BuildContextFromRequestPayload(_ context.Context, c *gin.Context, _ map[string]string, _ *trace.Tracer, _ *metric.Meter, _ *slog.Logger) (*security.RequestContext, error) {
	b.allowedSystemCaller = c.GetBool(ctxAllowSystemCaller)
	return security.NewRequestContextForTenantAccountAdmin("t", "u", []string{"a", "b", "c"}), nil
}

func (b *readableAccountsBuilder) BuildContextFromPayload(context.Context, *gin.Context, *ActionRequest, *trace.Tracer, *metric.Meter, *slog.Logger) (*security.RequestContext, error) {
	return nil, nil
}

func serveReadableScope(t *testing.T, ws *MockWorkflowService, builder *readableAccountsBuilder, path string) int {
	t.Helper()
	server := NewServer(ws, new(MockConfigService))
	server.securityContextBuilder = builder
	server.SetWorkflowService(ws)
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.Header.Set(serviceTokenHeader, testServiceToken)
	req.Header.Set("X-Account-ID", "a")
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)
	return rec.Code
}

func TestAISearchOfOtherAccountsNeedsAUser(t *testing.T) {
	// With no user the system user would read every account in the tenant, so
	// only the conversation's own account may be searched user-less.
	ws := new(MockWorkflowService)
	ws.On("SearchAIInvocableWorkflowsInOtherAccounts", mock.Anything, "a", "restart", 0).
		Return(model.AIWorkflowSearchResponse{}, nil)
	builder := &readableAccountsBuilder{}

	assert.Equal(t, http.StatusOK, serveReadableScope(t, ws, builder, "/workflows/ai-search?scope=readable&query=restart"))
	assert.False(t, builder.allowedSystemCaller)
	ws.AssertExpectations(t)
	ws.AssertNotCalled(t, "SearchAIInvocableWorkflows", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestListWorkflowsOfOtherAccountsLeavesOutTheRequestedOne(t *testing.T) {
	ws := new(MockWorkflowService)
	ws.On("ListWorkflows", mock.Anything, []string{"b", "c"}, mock.Anything).Return(model.ListWorkflowResponse{}, nil)

	assert.Equal(t, http.StatusOK, serveReadableScope(t, ws, &readableAccountsBuilder{}, "/workflows?scope=readable"))
	ws.AssertExpectations(t)
}

func TestListWorkflowsWithoutScopeStaysOnTheRequestedAccount(t *testing.T) {
	ws := new(MockWorkflowService)
	ws.On("ListWorkflows", mock.Anything, []string{"a"}, mock.Anything).Return(model.ListWorkflowResponse{}, nil)

	assert.Equal(t, http.StatusOK, serveReadableScope(t, ws, &readableAccountsBuilder{}, "/workflows"))
	ws.AssertExpectations(t)
}
