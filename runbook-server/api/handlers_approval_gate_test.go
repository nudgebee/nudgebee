package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

const approvalTestKey = "approval-test-key"

type ApprovalGateHandlerTestSuite struct {
	APITestSuite
	savedKey string
}

func (s *ApprovalGateHandlerTestSuite) SetupTest() {
	s.APITestSuite.SetupTest()
	s.savedKey = config.Config.ApprovalSigningKey
	config.Config.ApprovalSigningKey = approvalTestKey
}

func (s *ApprovalGateHandlerTestSuite) TearDownTest() {
	config.Config.ApprovalSigningKey = s.savedKey
}

func signApproval(token, status, approver string) string {
	mac := hmac.New(sha256.New, []byte(approvalTestKey))
	mac.Write([]byte(token + "|" + status + "|" + approver))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func (s *ApprovalGateHandlerTestSuite) post(token string, body map[string]any) int {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/approvals/:token", s.server.handleApproval)
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "/approvals/"+token, bytes.NewBuffer(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w.Code
}

func signed(token, status, approver string) map[string]any {
	return map[string]any{"status": status, "approver": approver, "sig": signApproval(token, status, approver)}
}

// A signed link decides its step as the address it was sent to.
func (s *ApprovalGateHandlerTestSuite) TestASignedLinkDecidesAsItsRecipient() {
	token := "abcd:acct:temporal-1:exec-1:ask"
	s.workflowService.On("CompleteApprovalTaskFromLink", mock.Anything, token, "approve", "dana@example.com", mock.Anything).Return(nil).Once()
	s.Equal(http.StatusOK, s.post(token, signed(token, "approve", "dana@example.com")))
	s.workflowService.AssertExpectations(s.T())
}

// The token alone, or a signature that doesn't match the decision or the
// approver, decides nothing.
func (s *ApprovalGateHandlerTestSuite) TestAnUnsignedOrAlteredLinkIsRefused() {
	token := "abcd:acct:temporal-1:exec-1:ask"
	sig := signApproval(token, "approve", "dana@example.com")
	for _, body := range []map[string]any{
		{"status": "approve"},
		{"status": "approve", "approver": "dana@example.com"},
		{"status": "reject", "approver": "dana@example.com", "sig": sig},
		{"status": "approve", "approver": "eve@example.com", "sig": sig},
		{"status": "approve", "approver": "dana@example.com", "sig": "v0=deadbeef"},
	} {
		s.Equal(http.StatusForbidden, s.post(token, body), body)
	}
	s.workflowService.AssertNotCalled(s.T(), "CompleteApprovalTaskFromLink", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// With no signing key configured, no link verifies.
func (s *ApprovalGateHandlerTestSuite) TestNoSigningKeyRefusesEveryLink() {
	token := "abcd:acct:temporal-1:exec-1:ask"
	body := signed(token, "approve", "dana@example.com")
	config.Config.ApprovalSigningKey = ""
	s.Equal(http.StatusForbidden, s.post(token, body))
	s.workflowService.AssertNotCalled(s.T(), "CompleteApprovalTaskFromLink", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// A link names no NudgeBee person, so a run's approval gate is not answered
// through it, even when signed.
func (s *ApprovalGateHandlerTestSuite) TestTheRunGateIsNotAnsweredByLink() {
	token := "abcd:acct:temporal-1:exec-1:" + model.ApprovalGateTaskID
	assert.Equal(s.T(), http.StatusForbidden, s.post(token, signed(token, "approve", "dana@example.com")))
	s.workflowService.AssertNotCalled(s.T(), "CompleteApprovalTaskFromLink", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestApprovalGateHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(ApprovalGateHandlerTestSuite))
}
