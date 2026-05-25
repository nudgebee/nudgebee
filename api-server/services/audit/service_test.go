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
