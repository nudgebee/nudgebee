package audit

import (
	"nudgebee/services/security"
	"time"
)

// ChangeInput describes a database change to be audited.
// User and tenant are extracted from the RequestContext unless overridden.
type ChangeInput struct {
	EventCategory EventCategory
	EventType     EventType
	EventAction   EventAction
	TargetID      string         // primary key of the changed record
	UserID        string         // optional override; defaults to context user
	TenantID      string         // optional override; defaults to context tenant
	AccountID     string         // optional; cloud account id if applicable
	NewData       map[string]any // row state after change (nil for DELETE)
	OldData       map[string]any // row state before change (nil for INSERT)
	TableName     string         // database table name
}

// LogChange records an audit entry for a database change.
// Errors are logged but not returned, matching the fire-and-forget
// semantics of the previous RPC webhook-based audit triggers.
func LogChange(ctx *security.RequestContext, input ChangeInput) {
	if ctx.GetSecurityContext() != nil && ctx.GetSecurityContext().IsSuperAdmin() {
		return
	}

	userId := input.UserID
	if userId == "" && ctx.GetSecurityContext() != nil {
		userId = ctx.GetSecurityContext().GetUserId()
	}

	tenantId := input.TenantID
	if tenantId == "" && ctx.GetSecurityContext() != nil {
		tenantId = ctx.GetSecurityContext().GetTenantId()
	}

	if input.TargetID == "" {
		ctx.GetLogger().Error("audit: LogChange called with empty TargetID", "table", input.TableName)
		return
	}

	auditReq := &AuditRequest{Audits: []Audit{buildChangeAudit(userId, tenantId, input)}}

	go func() {
		if err := CreateAudit(ctx, auditReq); err != nil {
			ctx.GetLogger().Error("audit: failed to log change",
				"error", err,
				"table", input.TableName,
				"target", input.TargetID,
				"op", input.EventAction,
			)
		}
	}()
}

// buildChangeAudit assembles the Audit row for a database change.
//
// EventState is the row's post-change state and is validate:"required" in
// CreateAudit. A delete has no post-change state (callers leave NewData nil and
// carry the removed row in OldData), so without special handling EventState
// would be nil, fail validation, and the audit would be silently dropped by the
// fire-and-forget goroutine in LogChange — the systemic form of NB-31417. For
// deletes we therefore record the removed row (OldData), falling back to the
// target id, so every delete is auditable. Non-delete callers that pass a nil
// NewData still fail validation on purpose: that's a caller bug worth surfacing.
func buildChangeAudit(userId, tenantId string, input ChangeInput) Audit {
	// Declared as an untyped nil any: assigning a typed nil map (input.NewData)
	// to an interface yields a non-nil interface, which would defeat downstream
	// `EventState == nil` checks. Only assign when there is a real value.
	var eventState any
	if input.NewData != nil {
		eventState = input.NewData
	} else if input.EventAction == EventActionDelete {
		if input.OldData != nil {
			eventState = input.OldData
		} else {
			eventState = map[string]any{"id": input.TargetID}
		}
	}

	return Audit{
		UserId:         userId,
		TenantId:       tenantId,
		AccountId:      input.AccountID,
		EventTime:      time.Now().UTC(),
		EventCategory:  input.EventCategory,
		EventType:      input.EventType,
		EventTarget:    input.TargetID,
		EventState:     eventState,
		EventPrevState: input.OldData,
		EventActor:     EventActorUiService,
		EventAction:    input.EventAction,
		EventStatus:    EventStatusSuccess,
		EventAttr: map[string]any{
			"table": input.TableName,
			"op":    string(input.EventAction),
		},
	}
}
