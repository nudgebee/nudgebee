package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockQueuedMessageDao struct {
	saveError error
	core.IConversationDao
	savedMessages       []core.ConversationMessage
	claimedMessages     []string
	deletedMessageIds   []string
	updatedMessages     map[string]string
	conversationStatus  map[string]core.ConversationStatus
	terminateCalledWith []string
}

func newMockQueuedMessageDao() *mockQueuedMessageDao {
	return &mockQueuedMessageDao{
		savedMessages:       make([]core.ConversationMessage, 0),
		claimedMessages:     make([]string, 0),
		deletedMessageIds:   make([]string, 0),
		updatedMessages:     make(map[string]string),
		conversationStatus:  make(map[string]core.ConversationStatus),
		terminateCalledWith: make([]string, 0),
	}
}

func (m *mockQueuedMessageDao) SaveConversationMessage(
	id, conversationId, accountID, userId string,
	role core.MessageRole,
	messageType core.MessageType,
	message, response, agentName string,
	parentAgentId uuid.UUID,
	messageConfig any,
	messageContext string,
	llmProvider, llmModel string,
	status core.ConversationStatus,
) (uuid.UUID, error) {
	if m.saveError != nil {
		return uuid.Nil, m.saveError
	}
	msgUUID := uuid.New()
	if id != "" {
		if parsed, err := uuid.Parse(id); err == nil {
			msgUUID = parsed
		}
	}
	now := time.Now()
	m.savedMessages = append(m.savedMessages, core.ConversationMessage{
		ID:             msgUUID,
		ConversationID: uuid.MustParse(conversationId),
		AccountID:      uuid.MustParse(accountID),
		UserID:         uuid.MustParse(userId),
		Role:           string(role),
		MessageType:    string(messageType),
		Message:        message,
		Status:         status,
		CreatedAt:      &now,
	})
	return msgUUID, nil
}

func (m *mockQueuedMessageDao) ClaimNextQueuedMessage(ctx context.Context, conversationID string) (*core.ConversationMessage, error) {
	convUUID, err := uuid.Parse(conversationID)
	if err != nil {
		return nil, err
	}
	for i := range m.savedMessages {
		if m.savedMessages[i].ConversationID == convUUID && m.savedMessages[i].Status == core.ConversationStatusQueued {
			m.savedMessages[i].Status = core.ConversationStatusInProgress
			m.claimedMessages = append(m.claimedMessages, m.savedMessages[i].ID.String())
			claimed := m.savedMessages[i]
			return &claimed, nil
		}
	}
	return nil, nil
}

func (m *mockQueuedMessageDao) ClaimQueuedMessageById(ctx *security.RequestContext, accountID, conversationID, messageId, userID string, isAdmin bool) (*core.ConversationMessage, error) {
	msgUUID, err := uuid.Parse(messageId)
	if err != nil {
		return nil, err
	}
	for i := range m.savedMessages {
		if m.savedMessages[i].ID == msgUUID && m.savedMessages[i].Status == core.ConversationStatusQueued {
			m.savedMessages[i].Status = core.ConversationStatusInProgress
			m.claimedMessages = append(m.claimedMessages, m.savedMessages[i].ID.String())
			claimed := m.savedMessages[i]
			return &claimed, nil
		}
	}
	return nil, nil
}

func (m *mockQueuedMessageDao) DeleteQueuedMessage(id string, accountID string, userID string, isAdmin bool) error {
	msgUUID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	foundIdx := -1
	for i, msg := range m.savedMessages {
		if msg.ID == msgUUID && msg.Status == core.ConversationStatusQueued {
			if isAdmin || msg.UserID.String() == userID {
				foundIdx = i
				break
			}
		}
	}
	if foundIdx == -1 {
		return core.ErrQueuedMessageNotFoundOrLocked
	}
	m.deletedMessageIds = append(m.deletedMessageIds, id)
	m.savedMessages = append(m.savedMessages[:foundIdx], m.savedMessages[foundIdx+1:]...)
	return nil
}

func (m *mockQueuedMessageDao) UpdateQueuedMessage(id string, accountID string, userID string, message string, isAdmin bool) error {
	msgUUID, err := uuid.Parse(id)
	if err != nil {
		return err
	}
	for i, msg := range m.savedMessages {
		if msg.ID == msgUUID && msg.Status == core.ConversationStatusQueued {
			if isAdmin || msg.UserID.String() == userID {
				m.savedMessages[i].Message = message
				m.updatedMessages[id] = message
				return nil
			}
		}
	}
	return core.ErrQueuedMessageNotFoundOrLocked
}

func (m *mockQueuedMessageDao) UpdateConversationStatus(conversationId string, status core.ConversationStatus) error {
	m.conversationStatus[conversationId] = status
	return nil
}

func (m *mockQueuedMessageDao) GetQueuedConversationMessages(conversationId string) ([]core.ConversationMessage, error) {
	convUUID, err := uuid.Parse(conversationId)
	if err != nil {
		return nil, err
	}
	var res []core.ConversationMessage
	for _, msg := range m.savedMessages {
		if msg.ConversationID == convUUID && msg.Status == core.ConversationStatusQueued {
			res = append(res, msg)
		}
	}
	return res, nil
}

func (m *mockQueuedMessageDao) GetConversation(conversationId string) (core.Conversation, error) {
	convUUID, err := uuid.Parse(conversationId)
	if err != nil {
		return core.Conversation{}, err
	}
	status := core.ConversationStatusInProgress
	if s, ok := m.conversationStatus[conversationId]; ok {
		status = s
	}
	return core.Conversation{
		ID:        convUUID,
		AccountID: uuid.New(),
		Status:    status,
	}, nil
}

func TestQueueConversationMessage_SavesQueuedStatus(t *testing.T) {
	mockDao := newMockQueuedMessageDao()
	core.SetConversationDao(mockDao)

	convID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()

	conversation := core.Conversation{
		ID:        convID,
		AccountID: accountID,
		Status:    core.ConversationStatusInProgress,
		SessionID: "sess-123",
	}

	req := &ConversationApiRequest{
		Query:     "How many pods are pending?",
		UserId:    userID.String(),
		AccountId: accountID.String(),
		Config:    toolcore.NBQueryConfig{},
	}

	msgID, err := queueConversationMessage(conversation, req, "k8s_debug_agent")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, msgID)

	require.Len(t, mockDao.savedMessages, 1)
	saved := mockDao.savedMessages[0]
	assert.Equal(t, core.ConversationStatusQueued, saved.Status)
	assert.Equal(t, "How many pods are pending?", saved.Message)
	assert.Equal(t, string(core.MessageRoleHuman), saved.Role)
	assert.Equal(t, string(core.MessageTypeGeneration), saved.MessageType)
}

func TestClaimNextQueuedMessage_FIFOOrder(t *testing.T) {
	mockDao := newMockQueuedMessageDao()
	core.SetConversationDao(mockDao)

	convID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()

	conversation := core.Conversation{
		ID:        convID,
		AccountID: accountID,
		Status:    core.ConversationStatusInProgress,
	}

	id1, err := queueConversationMessage(conversation, &ConversationApiRequest{
		Query:     "First queued message",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "k8s_debug_agent")
	require.NoError(t, err)

	id2, err := queueConversationMessage(conversation, &ConversationApiRequest{
		Query:     "Second queued message",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "k8s_debug_agent")
	require.NoError(t, err)

	claimed1, err := mockDao.ClaimNextQueuedMessage(context.Background(), convID.String())
	require.NoError(t, err)
	require.NotNil(t, claimed1)
	assert.Equal(t, id1, claimed1.ID)
	assert.Equal(t, core.ConversationStatusInProgress, claimed1.Status)

	claimed2, err := mockDao.ClaimNextQueuedMessage(context.Background(), convID.String())
	require.NoError(t, err)
	require.NotNil(t, claimed2)
	assert.Equal(t, id2, claimed2.ID)
	assert.Equal(t, core.ConversationStatusInProgress, claimed2.Status)

	claimed3, err := mockDao.ClaimNextQueuedMessage(context.Background(), convID.String())
	require.NoError(t, err)
	assert.Nil(t, claimed3)
}

func TestQueuedMessage_UpdateAndDelete(t *testing.T) {
	mockDao := newMockQueuedMessageDao()
	core.SetConversationDao(mockDao)

	convID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()
	otherUserID := uuid.New()

	conversation := core.Conversation{
		ID:        convID,
		AccountID: accountID,
		Status:    core.ConversationStatusInProgress,
	}

	msgID, err := queueConversationMessage(conversation, &ConversationApiRequest{
		Query:     "Original prompt",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "k8s_debug_agent")
	require.NoError(t, err)

	// Non-admin other user cannot update
	err = mockDao.UpdateQueuedMessage(msgID.String(), accountID.String(), otherUserID.String(), "Updated prompt", false)
	assert.ErrorIs(t, err, core.ErrQueuedMessageNotFoundOrLocked)

	// Author can update
	err = mockDao.UpdateQueuedMessage(msgID.String(), accountID.String(), userID.String(), "Updated prompt by author", false)
	require.NoError(t, err)
	assert.Equal(t, "Updated prompt by author", mockDao.updatedMessages[msgID.String()])

	// Non-admin other user cannot delete
	err = mockDao.DeleteQueuedMessage(msgID.String(), accountID.String(), otherUserID.String(), false)
	assert.ErrorIs(t, err, core.ErrQueuedMessageNotFoundOrLocked)

	// Admin can delete
	err = mockDao.DeleteQueuedMessage(msgID.String(), accountID.String(), otherUserID.String(), true)
	require.NoError(t, err)
	assert.Contains(t, mockDao.deletedMessageIds, msgID.String())

	// Once deleted or claimed, deleting again fails
	err = mockDao.DeleteQueuedMessage(msgID.String(), accountID.String(), userID.String(), true)
	assert.ErrorIs(t, err, core.ErrQueuedMessageNotFoundOrLocked)
}

func TestQueuedMessage_ClaimQueuedMessageById(t *testing.T) {
	mockDao := newMockQueuedMessageDao()
	core.SetConversationDao(mockDao)

	convID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()

	conversation := core.Conversation{
		ID:        convID,
		AccountID: accountID,
		Status:    core.ConversationStatusInProgress,
	}

	msgID, err := queueConversationMessage(conversation, &ConversationApiRequest{
		Query:     "Force execution candidate",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "k8s_debug_agent")
	require.NoError(t, err)

	claimed, err := mockDao.ClaimQueuedMessageById(nil, accountID.String(), convID.String(), msgID.String(), userID.String(), false)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, msgID, claimed.ID)
	assert.Equal(t, core.ConversationStatusInProgress, claimed.Status)

	// Second claim fails since it is now IN_PROGRESS
	claimedAgain, err := mockDao.ClaimQueuedMessageById(nil, accountID.String(), convID.String(), msgID.String(), userID.String(), false)
	require.NoError(t, err)
	assert.Nil(t, claimedAgain)
}

func TestIsSecurityContextAdmin(t *testing.T) {
	assert.False(t, isSecurityContextAdmin(nil, "account-1"))

	emptySC := &security.SecurityContext{}
	assert.False(t, isSecurityContextAdmin(emptySC, "account-1"))

	superAdminSC := &security.SecurityContext{}
	superAdminSC.AddRole(security.AUTH_SUPER_ADMIN_FULL_ROLE)
	assert.True(t, isSecurityContextAdmin(superAdminSC, "account-1"))

	accountAdminSC := &security.SecurityContext{}
	accountAdminSC.AddRole(security.AUTH_ACCOUNT_ADMIN_ROLE)
	assert.False(t, isSecurityContextAdmin(accountAdminSC, "account-1"))

	tenantAdminSC := &security.SecurityContext{}
	tenantAdminSC.AddRole(security.AUTH_TENANT_ADMIN_ROLE)
	assert.True(t, isSecurityContextAdmin(tenantAdminSC, "account-1"))

	regularUserSC := &security.SecurityContext{}
	regularUserSC.AddRole("account_user")
	assert.False(t, isSecurityContextAdmin(regularUserSC, "account-1"))
}

func TestTryQueueConversationMessage_MaxLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockDao := newMockQueuedMessageDao()
	core.SetConversationDao(mockDao)

	oldLimit := config.Config.MaxQueuedMessagesPerConversation
	config.Config.MaxQueuedMessagesPerConversation = 2
	defer func() {
		config.Config.MaxQueuedMessagesPerConversation = oldLimit
	}()

	convID := uuid.New()
	accountID := uuid.New()
	userID := uuid.New()

	conversation := core.Conversation{
		ID:        convID,
		AccountID: accountID,
		Status:    core.ConversationStatusInProgress,
		SessionID: "sess-limit-test",
	}

	// 1st message queues successfully
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	handled1 := tryQueueConversationMessage(c1, conversation, &ConversationApiRequest{
		Query:     "First message",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "")
	assert.True(t, handled1)
	assert.Equal(t, http.StatusAccepted, w1.Code)

	// 2nd message queues successfully
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	handled2 := tryQueueConversationMessage(c2, conversation, &ConversationApiRequest{
		Query:     "Second message",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "")
	assert.True(t, handled2)
	assert.Equal(t, http.StatusAccepted, w2.Code)

	// The transactional DAO rejects admission when capacity is reached.
	mockDao.saveError = core.ErrQueueFull
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	handled3 := tryQueueConversationMessage(c3, conversation, &ConversationApiRequest{
		Query:     "Third message",
		UserId:    userID.String(),
		AccountId: accountID.String(),
	}, "")
	assert.True(t, handled3)
	assert.Equal(t, http.StatusConflict, w3.Code)

	var resp ApiResponse
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &resp))
	require.Len(t, resp.Errors, 1)
	assert.Contains(t, resp.Errors[0].Message, core.ErrQueueFull.Error())
}

func TestQueueOnlyRequestCannotFallThroughToForeground(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, queueOnly := range []bool{true, false} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		rejected := rejectUnfulfilledQueueRequest(c, &ConversationApiRequest{QueueOnly: queueOnly})
		assert.Equal(t, queueOnly, rejected)
		if queueOnly {
			assert.Equal(t, http.StatusConflict, w.Code)
			assert.Contains(t, w.Body.String(), "Please retry your message")
		} else {
			assert.Empty(t, w.Body.String())
		}
	}
}

func TestQueueAdmits(t *testing.T) {
	cases := []struct {
		status    core.ConversationStatus
		messageId string
		want      bool
	}{
		{core.ConversationStatusInProgress, "", true},
		{core.ConversationStatusInProgress, "message", true},
		// A turn resumed from a followup answer runs while the conversation stays WAITING.
		{core.ConversationStatusWaiting, "", true},
		{core.ConversationStatusWaitingForClientTool, "", true},
		// A named message on a waiting conversation is that turn's own retry or resume.
		{core.ConversationStatusWaiting, "message", false},
		{core.ConversationStatusCompleted, "", false},
		{core.ConversationStatusFailed, "", false},
		{core.ConversationStatusTerminated, "", false},
	}
	for _, tc := range cases {
		t.Run(string(tc.status)+"/"+tc.messageId, func(t *testing.T) {
			got := queueAdmits(core.Conversation{Status: tc.status}, &ConversationApiRequest{MessageId: tc.messageId})
			assert.Equal(t, tc.want, got)
		})
	}
}
