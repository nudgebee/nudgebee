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

type HasuraDryRunHandlerTestSuite struct {
	APITestSuite
}

func (s *HasuraDryRunHandlerTestSuite) TestHasuraDryRunWorkflowHandler() {
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

	hasuraReq := HasuraActionRequest{
		Action: HasuraActionRequestAction{Name: "workflow_trigger_dryrun"},
		Input: map[string]any{
			"request": reqPayloadMap,
		},
	}

	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"test-account"})

	s.securityContextBuilder.On("BuildContextFromHasuraPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sc, nil)
	s.workflowService.On("DryRunWorkflowAsync", mock.Anything, "test-account", reqPayload).Return("dry-run-123", "exec-456", nil)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	r.POST("/hasura", s.server.handleHasuraAction)

	jsonValue, _ := json.Marshal(hasuraReq)
	req, _ := http.NewRequest("POST", "/hasura", bytes.NewBuffer(jsonValue))
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

func TestHasuraDryRunHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(HasuraDryRunHandlerTestSuite))
}
