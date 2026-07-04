package audit

import (
	"log/slog"
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestValidation(t *testing.T) {
	t.Run("TestBasicValidation", func(t *testing.T) {
		request := AuditRequest{
			Audits: []Audit{
				{
					UserId:         uuid.NewString(),
					TenantId:       uuid.NewString(),
					EventTime:      time.Now().UTC(),
					EventCategory:  EventCategoryUser,
					EventTarget:    "user",
					EventType:      EventTypeUserUpdate,
					EventState:     map[string]any{"name": "test"},
					EventPrevState: map[string]any{"name": "test1"},
					EventActor:     EventActorApiService,
					EventAction:    EventActionUpdate,
					EventStatus:    EventStatusSuccess,
					EventAttr:      map[string]interface{}{"test": "test"},
				},
			},
		}
		err := validateAuditRequest(&request)
		assert.Nil(t, err)

	})
}

// TestIntegrationDeleteAuditValidation guards #31417: integration delete audits
// were silently dropped because EventState was nil, which fails the "required"
// validation in validateAuditRequest (CreateAudit rejects the request before the
// row is ever inserted). Mirrors the audit built by core.DeleteIntegrationConfig.
func TestIntegrationDeleteAuditValidation(t *testing.T) {
	newDeleteAudit := func(eventState any) Audit {
		return Audit{
			UserId:         uuid.NewString(),
			TenantId:       uuid.NewString(),
			EventTime:      time.Now().UTC(),
			EventCategory:  EventCategoryIntegration,
			EventTarget:    "integration",
			EventType:      EventTypeIntegrationDelete,
			EventState:     eventState,
			EventPrevState: "my-integration",
			EventActor:     EventActorUiService,
			EventAction:    EventActionDelete,
			EventStatus:    EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
	}

	t.Run("nil EventState is rejected (reproduces the bug)", func(t *testing.T) {
		req := AuditRequest{Audits: []Audit{newDeleteAudit(nil)}}
		assert.Error(t, validateAuditRequest(&req))
	})

	t.Run("populated EventState passes (the fix)", func(t *testing.T) {
		req := AuditRequest{Audits: []Audit{newDeleteAudit(
			map[string]any{"type": "jira", "name": "my-integration", "source": "user"},
		)}}
		assert.NoError(t, validateAuditRequest(&req))
	})
}

func TestPublishAuditEvent(t *testing.T) {
	audit := Audit{
		UserId:        uuid.NewString(),
		TenantId:      uuid.NewString(),
		EventTime:     time.Now(),
		EventCategory: EventCategoryTenant,
		EventType:     EventTypeTenantCreate,
		EventState:    map[string]any{},
		EventActor:    EventActorApiService,
		EventTarget:   "tenant",
		EventAction:   EventActionCreate,
		EventStatus:   EventStatusSuccess,
	}
	err := PublishAuditEvent(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), audit)
	assert.Nil(t, err)
}
