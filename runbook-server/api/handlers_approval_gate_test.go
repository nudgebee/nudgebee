package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type ApprovalGateHandlerTestSuite struct {
	APITestSuite
}

// A link or button names no person, so a run's approval gate is not answered
// through it; every other approval is, as before.
func (s *ApprovalGateHandlerTestSuite) TestApprovalLinks() {
	gin.SetMode(gin.TestMode)
	post := func(token string) int {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)
		r.POST("/approvals/:token", s.server.handleApproval)
		req, _ := http.NewRequest("POST", "/approvals/"+token, bytes.NewBufferString(`{"status":"approve"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}

	gateToken := "abcd:acct:temporal-1:exec-1:" + model.ApprovalGateTaskID
	assert.Equal(s.T(), http.StatusForbidden, post(gateToken))
	s.workflowService.AssertNotCalled(s.T(), "CompleteApprovalTask", mock.Anything, gateToken, mock.Anything, mock.Anything)

	stepToken := "abcd:acct:temporal-1:exec-1:ask"
	s.workflowService.On("CompleteApprovalTask", mock.Anything, stepToken, "approve", mock.Anything).Return(nil).Once()
	assert.Equal(s.T(), http.StatusOK, post(stepToken))
	s.workflowService.AssertExpectations(s.T())
}

func TestApprovalGateHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(ApprovalGateHandlerTestSuite))
}
