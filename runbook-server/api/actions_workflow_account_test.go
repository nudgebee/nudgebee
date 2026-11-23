package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// #40216: workflow_get_account resolves the account for a link without ?accountId=.
type ActionWorkflowAccountTestSuite struct {
	APITestSuite
}

func (s *ActionWorkflowAccountTestSuite) call(action string, request map[string]any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"acc-1"})
	s.securityContextBuilder.On("BuildContextFromPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sc, nil)

	body, _ := json.Marshal(ActionRequest{Action: ActionRequestAction{Name: action}, Input: map[string]any{"request": request}})
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/rpc", s.server.handleAction)
	req, _ := http.NewRequest("POST", "/rpc", bytes.NewBuffer(body))
	req.Header.Set(serviceTokenHeader, testServiceToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func (s *ActionWorkflowAccountTestSuite) TestReturnsTheAccount() {
	s.workflowService.On("GetWorkflowAccount", mock.Anything, "wf-1").Return("acc-1", nil)

	w := s.call("workflow_get_account", map[string]any{"id": "wf-1"})

	s.Equal(http.StatusOK, w.Code)
	s.JSONEq(`{"id":"wf-1","account_id":"acc-1"}`, w.Body.String())
}

func (s *ActionWorkflowAccountTestSuite) TestNotFoundOrNotReadableIs404() {
	s.workflowService.On("GetWorkflowAccount", mock.Anything, "wf-1").Return("", sql.ErrNoRows)

	w := s.call("workflow_get_account", map[string]any{"id": "wf-1"})

	s.Equal(http.StatusNotFound, w.Code)
}

func (s *ActionWorkflowAccountTestSuite) TestMissingIdIs400() {
	w := s.call("workflow_get_account", map[string]any{})

	s.Equal(http.StatusBadRequest, w.Code)
	s.workflowService.AssertNotCalled(s.T(), "GetWorkflowAccount", mock.Anything, mock.Anything)
}

// workflow_get with an account is unchanged: it still loads by that account.
func (s *ActionWorkflowAccountTestSuite) TestWorkflowGetWithAccountUnchanged() {
	s.workflowService.On("GetWorkflow", mock.Anything, "acc-1", "wf-1").Return(&model.Workflow{ID: "wf-1", AccountID: "acc-1"}, nil)

	w := s.call("workflow_get", map[string]any{"id": "wf-1", "account_id": "acc-1"})

	s.Equal(http.StatusOK, w.Code)
	s.workflowService.AssertNotCalled(s.T(), "GetWorkflowAccount", mock.Anything, mock.Anything)
}

func TestActionWorkflowAccountTestSuite(t *testing.T) {
	suite.Run(t, new(ActionWorkflowAccountTestSuite))
}
