package audit

import (
	"errors"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

const (
	nbAuditsExchange = "nb_audits"
)

var eventsToPublish = []EventType{}

func PublishAuditEvent(context *security.RequestContext, audit Audit) error {
	if audit.EventType == "" {
		return errors.New("audit: event type is required")
	}
	if !config.Config.AuditPublishEnabled {
		return nil
	}
	routingKey := strings.ToLower(string(audit.EventType))
	routingKey = strings.ReplaceAll(routingKey, "_", ".")
	err := common.MqPublish(nbAuditsExchange, routingKey, audit, common.MqPublishWithExchangeType("topic"))
	if err != nil {
		context.GetLogger().Error("audit: failed to publish audit event", "error", err)
		return err
	}
	return nil
}

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

	// more validation based on different source categories

	return nil
}

func valueOrNil(value any) any {
	if v, ok := value.(string); ok && v == "" {
		return nil
	}
	return value
}

func CreateAudit(context *security.RequestContext, auditRequest *AuditRequest) error {
	// Skip audit for super admin operations (defense-in-depth)
	if context.GetSecurityContext() != nil && context.GetSecurityContext().IsSuperAdmin() {
		return nil
	}

	databaseManager, err := database.GetDatabaseManager(lo.Ternary(config.Config.ClickhouseEnabled, database.Warehouse, database.Metastore))
	if err != nil {
		return err
	}

	err = validateAuditRequest(auditRequest)
	if err != nil {
		context.GetLogger().Error("audit: validation failed", "error", err)
		return err
	}

	errs := []error{}
	for _, audit := range auditRequest.Audits {
		jsonEventState, err := common.MarshalJson(audit.EventState)
		if err != nil {
			context.GetLogger().Error("audit: error marshalling event state", "error", err, "request", audit.EventState)
			errs = append(errs, err)
			continue
		}
		jsonPrevEventState, err := common.MarshalJson(audit.EventPrevState)
		if err != nil {
			context.GetLogger().Error("audit: error marshalling next event state", "error", err, "request", audit.EventPrevState)
			errs = append(errs, err)
			continue
		}

		transactionId := audit.TransactionId
		if transactionId == "" {
			transactionId = context.GetTraceId()
		}
		if transactionId == "" {
			transactionId = uuid.New().String()
		}

		jsonEventAttr, err := common.MarshalJson(audit.EventAttr)
		if err != nil {
			context.GetLogger().Error("audit: error marshalling event attr", "error", err, "request", audit.EventAttr)
			errs = append(errs, err)
			continue
		}
		sqlQuery := `INSERT INTO audit (user_id, tenant_id, account_id, event_time, event_category, event_type, event_prev_state, event_state, event_actor, event_target, event_action, event_status, transaction_id, event_attr) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		// change query to database specific dialect
		sqlQuery = databaseManager.Db.Rebind(sqlQuery)
		result, err := databaseManager.Db.Exec(sqlQuery, valueOrNil(audit.UserId), audit.TenantId, audit.AccountId, valueOrNil(audit.EventTime), audit.EventCategory, audit.EventType, string(jsonPrevEventState), string(jsonEventState), audit.EventActor, audit.EventTarget, audit.EventAction, audit.EventStatus, transactionId, string(jsonEventAttr))

		if err != nil {
			context.GetLogger().Error("audit: error creating audit", "error", err, "request", slog.AnyValue(audit))
			errs = append(errs, err)
			continue
		}
		_, err = result.RowsAffected()
		if err != nil {
			context.GetLogger().Error("audit: error creating audit", "error", err, "request", slog.AnyValue(audit))
			errs = append(errs, err)
			continue
		}

		if slices.Contains(eventsToPublish, audit.EventType) {
			err = PublishAuditEvent(context, audit)
			if err != nil {
				context.GetLogger().Error("audit: error publishing audit event", "error", err, "request", slog.AnyValue(audit))
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
