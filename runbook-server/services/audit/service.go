package audit

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
)

// auditInsertTimeout caps each audit batch's DB time. Emits are dispatched
// from fire-and-forget goroutines in internal/workflow; without a deadline a
// stuck pool checkout or hung statement would leak the caller goroutine.
const auditInsertTimeout = 10 * time.Second

func validateAuditRequest(auditRequest *AuditRequest) error {
	if auditRequest == nil {
		return common.ErrorBadRequest("audit: auditRequest is required")
	}

	if len(auditRequest.Audits) == 0 {
		return common.ErrorBadRequest("audit: audits is required")
	}
	for _, audit := range auditRequest.Audits {
		err := common.ValidateStruct(audit)
		if err != nil {
			return err
		}
	}
	return nil
}

func valueOrNil(value any) any {
	if v, ok := value.(string); ok && v == "" {
		return nil
	}
	return value
}

// CreateAudit writes one or more audit rows directly to the shared Postgres
// `audit` table. runbook-server's Metastore DB points at the same database
// as api-server (AUTO_PILOT_DATABASE_URL defaults to APP_DATABASE_URL), so
// rows written here are read by the same Audits UI query that already reads
// api-server's rows — no api-server roundtrip needed.
//
// Mirrors api-server/services/audit/service.go::CreateAudit: same column
// list, same JSON-encoding of state/prev_state/event_attr, same null-coalesce
// on UserId. Super-admin operations are skipped to match api-server policy.
//
// Per-row safety: tenant_id is required (multi-tenant defense-in-depth — an
// empty tenant_id would surface in every tenant's Audits filter and is never
// legitimate for a workflow-automation row). Rows missing tenant_id are
// counted as errors and skipped without aborting the rest of the batch.
func CreateAudit(ctx *security.RequestContext, auditRequest *AuditRequest) error {
	if ctx.GetSecurityContext() != nil && ctx.GetSecurityContext().IsSuperAdmin() {
		return nil
	}

	if err := validateAuditRequest(auditRequest); err != nil {
		ctx.GetLogger().Error("audit: validation failed", "error", err)
		return err
	}

	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}

	const sqlQuery = `INSERT INTO audit (
		user_id, tenant_id, account_id, event_time,
		event_category, event_type, event_prev_state, event_state,
		event_actor, event_target, event_action, event_status,
		transaction_id, event_attr
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rebound := dbm.Db.Rebind(sqlQuery)

	// Detach from the caller's request context — emits are fire-and-forget and
	// the request may finish before the INSERT does. Cap the wait at
	// auditInsertTimeout so a stalled DB never holds the goroutine open.
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), auditInsertTimeout)
	defer cancel()

	errs := []error{}
	for _, a := range auditRequest.Audits {
		if a.TenantId == "" {
			ctx.GetLogger().Error("audit: tenant_id is empty", "event_type", a.EventType, "target", a.EventTarget)
			errs = append(errs, errors.New("audit: tenant_id is required"))
			continue
		}

		jsonEventState, err := common.MarshalJson(a.EventState)
		if err != nil {
			ctx.GetLogger().Error("audit: marshal event state", "error", err)
			errs = append(errs, err)
			continue
		}
		jsonPrevState, err := common.MarshalJson(a.EventPrevState)
		if err != nil {
			ctx.GetLogger().Error("audit: marshal prev state", "error", err)
			errs = append(errs, err)
			continue
		}
		jsonAttrs, err := common.MarshalJson(a.EventAttr)
		if err != nil {
			ctx.GetLogger().Error("audit: marshal event attrs", "error", err)
			errs = append(errs, err)
			continue
		}

		txnId := a.TransactionId
		if txnId == "" {
			txnId = ctx.GetTraceId()
		}
		if txnId == "" {
			txnId = uuid.New().String()
		}

		_, err = dbm.Db.ExecContext(
			dbCtx,
			rebound,
			valueOrNil(a.UserId), a.TenantId, a.AccountId, valueOrNil(a.EventTime.UTC()),
			a.EventCategory, a.EventType, string(jsonPrevState), string(jsonEventState),
			a.EventActor, a.EventTarget, a.EventAction, a.EventStatus,
			txnId, string(jsonAttrs),
		)
		if err != nil {
			ctx.GetLogger().Error("audit: insert failed", "error", err, "event_type", a.EventType, "target", a.EventTarget, "request", slog.AnyValue(a))
			errs = append(errs, err)
			continue
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
