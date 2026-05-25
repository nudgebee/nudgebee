package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type ActionDryRunHandlerTestSuite struct {
	APITestSuite
}

func (s *ActionDryRunHandlerTestSuite) TestDryRunWorkflowHandler() {
	gin.SetMode(gin.TestMode)

	t := s.T()

	reqPayload := model.DryRunWorkflowRequest{
		Definition: model.WorkflowDefinition{
			Version: "v1",
			Tasks:   []model.Task{{ID: "task1", Type: "print"}},
		},
		Inputs: map[string]any{"foo": "bar"},
	}

	reqPayloadMap, err := common.StructToMap(reqPayload)
	if err != nil {
		s.T().Error("failed to convert struct to map", "error", err)
	}
	reqPayloadMap["account_id"] = "test-account"

	actionReq := ActionRequest{
		Action: ActionRequestAction{Name: "workflow_trigger_dryrun"},
		Input: map[string]any{
			"request": reqPayloadMap,
		},
	}

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	s.securityContextBuilder.On("BuildContextFromPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sc, nil)
	s.workflowService.On("DryRunWorkflowAsync", mock.Anything, "test-account", reqPayload).Return("dry-run-123", "exec-456", nil)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	r.POST("/rpc", s.server.handleAction)

	jsonValue, _ := json.Marshal(actionReq)
	req, _ := http.NewRequest("POST", "/rpc", bytes.NewBuffer(jsonValue))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "RUNNING", response["status"])
	assert.Equal(t, "dry-run-123", response["dryrun_id"])
	assert.Equal(t, "exec-456", response["execution_id"])

	s.workflowService.AssertExpectations(t)
}

func TestActionDryRunHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(ActionDryRunHandlerTestSuite))
}
