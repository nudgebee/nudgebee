package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type DryRunHandlerTestSuite struct {
	APITestSuite
}

func (s *DryRunHandlerTestSuite) TestDryRunWorkflowHandler() {
	gin.SetMode(gin.TestMode)

	t := s.T()

	reqPayload := model.DryRunWorkflowRequest{
		Definition: model.WorkflowDefinition{
			Version: "v1",
			Tasks:   []model.Task{{ID: "task1", Type: "print"}},
		},
		Inputs: map[string]any{"foo": "bar"},
	}

	mockResp := model.DryRunWorkflowResponse{
		Status: model.WorkflowExecutionStatusCompleted,
		Output: map[string]any{"result": "success"},
	}

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	s.securityContextBuilder.On("BuildContextFromRequestPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sc, nil)
	s.workflowService.On("DryRunWorkflow", mock.Anything, "test-account", reqPayload).Return(mockResp, nil)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	r.POST("/workflows/dry-run", s.server.dryRunWorkflow)

	jsonValue, _ := json.Marshal(reqPayload)
	req, _ := http.NewRequest("POST", "/workflows/dry-run", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Account-ID", "test-account")

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response model.DryRunWorkflowResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, model.WorkflowExecutionStatusCompleted, response.Status)
	assert.Equal(t, map[string]any{"result": "success"}, response.Output)

	s.workflowService.AssertExpectations(t)
}

func TestDryRunHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(DryRunHandlerTestSuite))
}
