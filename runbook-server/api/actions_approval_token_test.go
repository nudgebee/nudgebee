package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// workflow_complete_approval with a chat card's token decides as the caller.
type ActionApprovalTokenTestSuite struct {
	APITestSuite
}

func (s *ActionApprovalTokenTestSuite) call(input map[string]any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	sc := security.NewRequestContextForTenantAccountAdmin("test-tenant", "test-user", []string{"acc-1"})
	s.securityContextBuilder.On("BuildContextFromPayload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(sc, nil)

	body, _ := json.Marshal(ActionRequest{Action: ActionRequestAction{Name: "workflow_complete_approval"}, Input: map[string]any{"input": input}})
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/rpc", s.server.handleAction)
	req, _ := http.NewRequest("POST", "/rpc", bytes.NewBuffer(body))
	req.Header.Set(serviceTokenHeader, testServiceToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func (s *ActionApprovalTokenTestSuite) TestTokenDecidesAsTheCaller() {
	s.workflowService.On("CompleteApprovalTaskByToken", mock.MatchedBy(func(sc *security.RequestContext) bool {
		return sc.GetSecurityContext().GetUserId() == "test-user"
	}), "abcd:acc-1:temporal-1:exec-1:ask", "approve").Return(nil).Once()

	w := s.call(map[string]any{"account_id": "acc-1", "token": "abcd:acc-1:temporal-1:exec-1:ask", "status": "approve"})

	s.Equal(http.StatusOK, w.Code)
	s.workflowService.AssertExpectations(s.T())
	s.workflowService.AssertNotCalled(s.T(), "CompleteApprovalTaskFromUI", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ActionApprovalTokenTestSuite) TestRefusalKeepsItsCode() {
	s.workflowService.On("CompleteApprovalTaskByToken", mock.Anything, mock.Anything, mock.Anything).
		Return(common.ErrorUnauthorized("account not accessible")).Once()

	w := s.call(map[string]any{"account_id": "acc-1", "token": "abcd:acc-1:temporal-1:exec-1:ask", "status": "approve"})

	s.Equal(http.StatusForbidden, w.Code)
	s.Contains(w.Body.String(), "account not accessible")
}

func TestActionApprovalTokenTestSuite(t *testing.T) {
	suite.Run(t, new(ActionApprovalTokenTestSuite))
}
