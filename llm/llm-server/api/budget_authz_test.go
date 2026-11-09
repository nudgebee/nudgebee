package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// Regression for #37020. /v1/budget/config/* is tenant-scoped for EVERY caller,
// super admins included: their session still carries the tenant they are
// viewing, and the Usage & Limits screen reads usage back for that tenant only.
// Before the fix a super admin skipped scoping outright, so the screen listed —
// and let them edit — other tenants' budget rows, which then showed the new
// value in the config list while the usage section kept the old one.
func TestRequireBudgetAdminTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		wire       string
		wantTenant string
		wantStatus int
	}{
		{
			name:       "super admin is pinned to the tenant its session carries",
			wire:       `{"TenantId":"t1","UserId":"u1","Roles":["` + security.AUTH_SUPER_ADMIN_FULL_ROLE + `"]}`,
			wantTenant: "t1",
		},
		{
			name:       "tenant admin is pinned to its own tenant",
			wire:       `{"TenantId":"t1","UserId":"u2","Roles":["` + security.AUTH_TENANT_ADMIN_ROLE + `"]}`,
			wantTenant: "t1",
		},
		{
			name:       "super admin with no tenant in the session is refused, not unscoped",
			wire:       `{"TenantId":"","UserId":"u3","Roles":["` + security.AUTH_SUPER_ADMIN_FULL_ROLE + `"]}`,
			wantStatus: 400,
		},
		{
			name:       "non-admin is refused",
			wire:       `{"TenantId":"t1","UserId":"u4","Roles":[]}`,
			wantStatus: 403,
		},
		{
			name:       "read-only super admin is not a budget admin",
			wire:       `{"TenantId":"t1","UserId":"u5","Roles":["` + security.AUTH_SUPER_ADMIN_READONLY_ROLE + `"]}`,
			wantStatus: 403,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			got := requireBudgetAdminTenant(c, authzContext(t, tt.wire))

			if got != tt.wantTenant {
				t.Errorf("tenant = %q, want %q", got, tt.wantTenant)
			}
			if tt.wantTenant != "" {
				if rec.Code != 200 {
					t.Errorf("wrote status %d on the allow path", rec.Code)
				}
				return
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not the { message } shape RPC actions expect: %v", err)
			}
			if body["message"] == "" {
				t.Error("error response carries no message")
			}
		})
	}
}

type sessionLookupDao struct {
	core.IConversationDao
	conversation core.Conversation
	err          error
	calls        int
}

func (d *sessionLookupDao) GetConversationBySession(accountID, sessionID string) (core.Conversation, error) {
	d.calls++
	return d.conversation, d.err
}

func conversationFrom(source core.ConversationSource) core.Conversation {
	return core.Conversation{ID: uuid.New(), Source: &source}
}

func TestRepliesPastCountLimit(t *testing.T) {
	user := core.ConversationSourceUserInvestigation
	person := uuid.NewString()
	system := security.GetSystemUserId()
	tests := []struct {
		name      string
		existing  *core.Conversation
		sessionId string
		agentId   string
		caller    string
		dao       *sessionLookupDao
		want      bool
		wantCalls int
	}{
		{
			name:     "an answer to a waiting prompt in an alert investigation",
			existing: ptr(conversationFrom(core.ConversationSourceInvestigation)),
			agentId:  "agent-1",
			caller:   system,
			dao:      &sessionLookupDao{},
			want:     true,
		},
		{
			name:     "an answer to a waiting prompt in an automation",
			existing: ptr(conversationFrom(core.ConversationSourceAutomation)),
			agentId:  "agent-1",
			caller:   person,
			dao:      &sessionLookupDao{},
			want:     true,
		},
		{
			name:     "a person's follow-up in a web chat",
			existing: ptr(conversationFrom(user)),
			caller:   person,
			dao:      &sessionLookupDao{},
			want:     true,
		},
		{
			// The case the alert session used to block: a person types into the
			// investigation an alert started.
			name:      "a person's follow-up on an event session",
			sessionId: "event-abc123",
			caller:    person,
			dao:       &sessionLookupDao{conversation: conversationFrom(core.ConversationSourceInvestigation)},
			want:      true,
			wantCalls: 1,
		},
		{
			name:     "a person's follow-up in a Slack or Teams thread",
			existing: ptr(conversationFrom(core.ConversationSourceInstantNotification)),
			caller:   person,
			dao:      &sessionLookupDao{},
			want:     true,
		},
		{
			name:     "a conversation with no recorded source counts as a person's",
			existing: &core.Conversation{ID: uuid.New()},
			caller:   person,
			dao:      &sessionLookupDao{},
			want:     true,
		},
		{
			// An event re-run reuses the alert's session; it must keep the full check.
			name:      "an event re-run as the system user",
			sessionId: "event-abc123",
			caller:    system,
			dao:       &sessionLookupDao{conversation: conversationFrom(core.ConversationSourceInvestigation)},
			want:      false,
			wantCalls: 1,
		},
		{
			name:      "an event re-run with no user",
			sessionId: "event-abc123",
			dao:       &sessionLookupDao{conversation: conversationFrom(core.ConversationSourceInvestigation)},
			want:      false,
			wantCalls: 1,
		},
		{
			// The automation runner forwards the user it runs as, which may be a person.
			name:      "an automation resuming its own session as a person",
			sessionId: "automation-run-7",
			caller:    person,
			dao:       &sessionLookupDao{conversation: conversationFrom(core.ConversationSourceAutomation)},
			want:      false,
			wantCalls: 1,
		},
		{
			name:    "no conversation id and no session id",
			agentId: "agent-1",
			caller:  person,
			dao:     &sessionLookupDao{},
		},
		{
			// GetConversationBySession returns an empty conversation and no error
			// when nothing matches, so a fresh session must not read as a reply.
			name:      "a fresh session with no stored conversation",
			sessionId: "session-2",
			caller:    person,
			dao:       &sessionLookupDao{},
			wantCalls: 1,
		},
		{
			name:      "a lookup that fails",
			sessionId: "session-3",
			caller:    person,
			dao:       &sessionLookupDao{err: errors.New("db down")},
			wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := core.PeekConversationDao()
			core.SetConversationDao(tt.dao)
			t.Cleanup(func() { core.SetConversationDao(previous) })

			assert.Equal(t, tt.want, repliesPastCountLimit("acct-1", tt.sessionId, tt.agentId, tt.caller, tt.existing))
			assert.Equal(t, tt.wantCalls, tt.dao.calls)
		})
	}
}

func ptr[T any](v T) *T { return &v }
