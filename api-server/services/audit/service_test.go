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

// TestBuildChangeAudit guards the systemic form of NB-31417: every delete that
// flows through audit.LogChange leaves NewData (=> EventState) nil, which fails
// the "required" validation and is silently dropped by the fire-and-forget
// goroutine. buildChangeAudit must always produce a delete audit that passes
// validateAuditRequest.
func TestBuildChangeAudit(t *testing.T) {
	deleteBase := ChangeInput{
		EventCategory: EventCategoryUser,
		EventType:     EventTypeUserDelete,
		EventAction:   EventActionDelete,
		TargetID:      "user-123",
		TableName:     "users",
	}

	t.Run("delete with OldData records OldData and passes validation", func(t *testing.T) {
		in := deleteBase
		in.OldData = map[string]any{"id": "user-123"}
		a := buildChangeAudit(uuid.NewString(), uuid.NewString(), in)
		assert.Equal(t, in.OldData, a.EventState)
		assert.NoError(t, validateAuditRequest(&AuditRequest{Audits: []Audit{a}}))
	})

	t.Run("delete without OldData falls back to target id and passes validation", func(t *testing.T) {
		a := buildChangeAudit(uuid.NewString(), uuid.NewString(), deleteBase)
		assert.Equal(t, map[string]any{"id": "user-123"}, a.EventState)
		assert.NoError(t, validateAuditRequest(&AuditRequest{Audits: []Audit{a}}))
	})

	t.Run("create records NewData and passes validation", func(t *testing.T) {
		in := ChangeInput{
			EventCategory: EventCategoryUser,
			EventType:     EventTypeUserCreate,
			EventAction:   EventActionCreate,
			TargetID:      "user-123",
			TableName:     "users",
			NewData:       map[string]any{"id": "user-123", "name": "test"},
		}
		a := buildChangeAudit(uuid.NewString(), uuid.NewString(), in)
		assert.Equal(t, in.NewData, a.EventState)
		assert.NoError(t, validateAuditRequest(&AuditRequest{Audits: []Audit{a}}))
	})

	t.Run("non-delete with nil NewData stays nil and is rejected (caller bug)", func(t *testing.T) {
		in := ChangeInput{
			EventCategory: EventCategoryUser,
			EventType:     EventTypeUserUpdate,
			EventAction:   EventActionUpdate,
			TargetID:      "user-123",
			TableName:     "users",
		}
		a := buildChangeAudit(uuid.NewString(), uuid.NewString(), in)
		// EventState must be an untyped nil, not a typed-nil map wrapped in an
		// interface, so downstream `EventState == nil` checks behave.
		assert.Nil(t, a.EventState)
		assert.Error(t, validateAuditRequest(&AuditRequest{Audits: []Audit{a}}))
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
