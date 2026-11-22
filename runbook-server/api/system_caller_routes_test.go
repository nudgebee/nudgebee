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

// flagRecordingBuilder records whether the handler allowed a user-less caller.
type flagRecordingBuilder struct{ allowed map[string]bool }

func (b *flagRecordingBuilder) BuildContextFromRequestPayload(_ context.Context, c *gin.Context, _ map[string]string, _ *trace.Tracer, _ *metric.Meter, _ *slog.Logger) (*security.RequestContext, error) {
	b.allowed[c.Request.URL.Path] = c.GetBool(ctxAllowSystemCaller)
	return security.NewRequestContextForTenantAccountAdmin("t", security.GetSystemUserId(), []string{"a"}), nil
}

func (b *flagRecordingBuilder) BuildContextFromPayload(context.Context, *gin.Context, *ActionRequest, *trace.Tracer, *metric.Meter, *slog.Logger) (*security.RequestContext, error) {
	return nil, nil
}

func TestReadOnlyCatalogueRoutesAcceptUserlessServiceCalls(t *testing.T) {
	// llm-server lists the task catalogue and the AI-invocable automations with
	// no user. Both are reads; everything else still refuses a missing user.
	// The handler's only job here is to set the flag; the refusal itself is
	// covered by TestRestRequestWithoutAUserIsRefused.
	ws := new(MockWorkflowService)
	ws.On("ListAllTasks", mock.Anything).Return(model.ListTaskDefinitionResponse{})
	ws.On("SearchAIInvocableWorkflows", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(model.AIWorkflowSearchResponse{}, nil)
	ws.On("ExecuteTask", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	server := NewServer(ws, new(MockConfigService))
	builder := &flagRecordingBuilder{allowed: map[string]bool{}}
	server.securityContextBuilder = builder
	server.SetWorkflowService(ws)

	serve := func(method, path string) int {
		req := httptest.NewRequest(method, path, http.NoBody)
		req.Header.Set(serviceTokenHeader, testServiceToken)
		req.Header.Set("X-Account-ID", "a")
		rec := httptest.NewRecorder()
		server.router.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, serve(http.MethodGet, "/tasks"))
	assert.Equal(t, http.StatusOK, serve(http.MethodGet, "/workflows/ai-search"))
	serve(http.MethodPost, "/tasks/core.print/execute")

	assert.True(t, builder.allowed["/tasks"], "the task catalogue may be listed without a user")
	assert.True(t, builder.allowed["/workflows/ai-search"], "AI-invocable automations may be listed without a user")
	assert.False(t, builder.allowed["/tasks/core.print/execute"], "executing a task still needs a user")
}
