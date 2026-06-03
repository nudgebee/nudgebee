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

	auditReq := &AuditRequest{
		Audits: []Audit{{
			UserId:         userId,
			TenantId:       tenantId,
			AccountId:      input.AccountID,
			EventTime:      time.Now(),
			EventCategory:  input.EventCategory,
			EventType:      input.EventType,
			EventTarget:    input.TargetID,
			EventState:     input.NewData,
			EventPrevState: input.OldData,
			EventActor:     EventActorUiService,
			EventAction:    input.EventAction,
			EventStatus:    EventStatusSuccess,
			EventAttr: map[string]any{
				"table": input.TableName,
				"op":    string(input.EventAction),
			},
		}},
	}

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
