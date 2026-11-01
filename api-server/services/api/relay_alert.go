package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/eventrule"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"nudgebee/services/triage"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// maxCapabilityRuleIDs bounds one eventrules_get_capabilities call (one Alert Manager page).
const maxCapabilityRuleIDs = 500

func handleEventRuleAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	relayRequest := actionPayload.Input

	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	auditEvent := audit.Audit{
		UserId:         ctx.GetSecurityContext().GetUserId(),
		TenantId:       ctx.GetSecurityContext().GetTenantId(),
		EventCategory:  audit.EventAlertManagerRelay,
		EventType:      audit.EventTypeAlertManagerCreate,
		EventPrevState: nil,
		EventActor:     audit.EventActorUiService,
		EventStatus:    audit.EventStatusSuccess,
		EventAttr:      map[string]any{},
	}
	switch actionPayload.Action.Name {
	case "alertmanager_create_rule":
		var relayAPIRequest eventrule.EventConfig
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}

		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		stampMetricProvider(ctx, &relayAPIRequest)
		response, err1 := eventrule.CreateEventRule(ctx, relayAPIRequest)
		defer func() {
			auditEvent.AccountId = relayAPIRequest.AccountID
			auditEvent.EventAction = audit.EventActionCreate
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = relayAPIRequest.Alert
			auditEvent.EventState = relayAPIRequest
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err1 != nil {
			c.JSON(400, common.ErrorActionBadRequest(err1.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, response)
		return

	case "alertmanager_update_rule":
		var relayAPIRequest eventrule.EventConfig
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}

		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		response, err1 := eventrule.UpdateEventRule(ctx, relayAPIRequest)
		defer func() {
			auditEvent.AccountId = relayAPIRequest.AccountID
			auditEvent.EventAction = audit.EventActionUpdate
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = relayAPIRequest.Alert
			auditEvent.EventState = relayAPIRequest
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event for update alert manager", "error", err)
			}
		}()
		if err1 != nil {
			c.JSON(400, common.ErrorActionBadRequest(err1.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, response)
		return

	case "alertmanager_disable_rule":
		var relayAPIRequest eventrule.DisableEventConfig
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}

		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		response, err1 := eventrule.DisableEventRule(ctx, relayAPIRequest)
		defer func() {
			auditEvent.AccountId = relayAPIRequest.AccountID
			auditEvent.EventAction = audit.EventActionDelete
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = relayAPIRequest.Alert
			auditEvent.EventState = relayAPIRequest
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event to disable alert manager", "error", err)
			}
		}()
		if err1 != nil {
			c.JSON(400, common.ErrorActionBadRequest(err1.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, response)
		return

	case "alertmanager_list_actions":
		var relayAPIRequest eventrule.ListActionsRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		actions, err := eventrule.ListAction(ctx, relayAPIRequest, promQLLabelNames)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		response := map[string]any{
			"actions": actions,
		}
		c.JSON(200, response)
		return

	case "list_alert_agent_playbook":
		var agentPlaybookRequest eventrule.ListAgentPlaybookRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(relayRequest, &agentPlaybookRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		actions, err := eventrule.ListAlertAgentPlaybook(ctx, agentPlaybookRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, actions)
		return

	case "eventrules_sync_provider_rules":
		var syncRequest eventrule.SyncProviderRulesRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		if err := common.UnmarshalMapToStruct(relayRequest, &syncRequest); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		results, err := eventrule.SyncProviderRulesForAccount(ctx, syncRequest)
		defer func() {
			auditEvent.AccountId = syncRequest.AccountId
			auditEvent.EventAction = audit.EventActionCreate
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = strings.Join(syncRequest.Sources, ",")
			auditEvent.EventState = syncRequest
			if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", auditErr)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, results)
		return

	case "eventrules_get_capabilities":
		var capRequest struct {
			RuleIDs []string `json:"rule_ids"`
		}
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		if err := common.UnmarshalMapToStruct(relayRequest, &capRequest); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if len(capRequest.RuleIDs) > maxCapabilityRuleIDs {
			c.JSON(400, common.ErrorActionBadRequest("too many rule_ids"))
			return
		}
		capabilities, err := eventrule.GetRuleCapabilities(ctx, capRequest.RuleIDs)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"capabilities": capabilities})
		return

	case "eventrules_update_mute":
		var muteRequest triage.UpdateRuleMuteRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		if err := common.UnmarshalMapToStruct(relayRequest, &muteRequest); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		capabilities, err := triage.UpdateRuleMute(ctx, muteRequest)
		defer func() {
			auditEvent.AccountId = muteRequest.AccountID
			auditEvent.EventAction = audit.EventActionUpdate
			auditEvent.EventTime = time.Now()
			// A rejected mute has no resolved rule; the audit target is required.
			auditEvent.EventTarget = capabilities.Alert
			if auditEvent.EventTarget == "" {
				auditEvent.EventTarget = muteRequest.RuleID
			}
			auditEvent.EventState = muteRequest
			if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
				ctx.GetLogger().Error("failed to create audit event for alert rule mute", "error", auditErr)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, capabilities)
		return

	case "eventrules_apply_rule_change":
		var changeRequest eventrule.ApplyRuleChangeRequest
		if req, ok := relayRequest["request"].(map[string]interface{}); ok {
			relayRequest = req
		}
		if err := common.UnmarshalMapToStruct(relayRequest, &changeRequest); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		result, err := eventrule.ApplyRuleChange(ctx, changeRequest)
		defer func() {
			auditEvent.AccountId = changeRequest.AccountID
			auditEvent.EventAction = audit.EventActionUpdate
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = changeRequest.RuleID
			if result != nil && result.Capabilities.Alert != "" {
				auditEvent.EventTarget = result.Capabilities.Alert
			}
			auditEvent.EventState = changeRequest
			if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
				ctx.GetLogger().Error("failed to create audit event for alert rule change", "error", auditErr)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, result)
		return

	case "eventrules_get_event_alert_controls":
		var controlsRequest triage.EventAlertControlsRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		if err := common.UnmarshalMapToStruct(relayRequest, &controlsRequest); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		controls, err := triage.GetEventAlertControls(ctx, controlsRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, controls)
		return

	case "eventrules_update_event_mute":
		var muteRequest triage.UpdateEventMuteRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		if err := common.UnmarshalMapToStruct(relayRequest, &muteRequest); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		controls, err := triage.UpdateEventMute(ctx, muteRequest)
		defer func() {
			auditEvent.AccountId = muteRequest.AccountID
			auditEvent.EventAction = audit.EventActionUpdate
			auditEvent.EventTime = time.Now()
			// A rejected mute has no resolved alert; the audit target is required.
			auditEvent.EventTarget = muteRequest.EventID
			if controls != nil && controls.Alert != "" {
				auditEvent.EventTarget = controls.Alert
			}
			auditEvent.EventState = muteRequest
			if auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); auditErr != nil {
				ctx.GetLogger().Error("failed to create audit event for event alert mute", "error", auditErr)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, controls)
		return

	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}

// stampMetricProvider fills metric_provider / metric_provider_source from the
// account's default metrics provider when a CREATE request names none and that
// provider is a direct (agentless) Prometheus. eventrule tells the ruler path apart
// from the relay path by exactly that pair, and the Create Alert form has always
// sent a bare `source: prometheus`, which on its own means the relay — so without
// this an agentless account would push its rule at an agent it does not have.
// Nothing else is touched: an account whose default provider is Datadog keeps the
// Prometheus/nudgebee path its request asked for, as before. Updates are left
// alone on purpose — they carry the source the rule was stored under, and an
// agent-written rule must keep updating its PrometheusRule CR even on an account
// that has since made a direct Prometheus its default. Resolved here rather than
// in eventrule because eventrule cannot import observability (observability →
// event → eventrule).
func stampMetricProvider(ctx *security.RequestContext, req *eventrule.EventConfig) {
	if req.MetricProvider != "" || (req.AlertType != "" && req.AlertType != "metric") {
		return
	}
	switch req.Source {
	case "", "nudgebee", "prometheus":
	default:
		return
	}
	provider, source, err := observability.GetLogsMetricsTracesProvider(ctx, req.AccountID, "", "metrics", "")
	if err != nil {
		ctx.GetLogger().Warn("alert rule: could not resolve the account's metrics provider; keeping the request's source",
			"account_id", req.AccountID, "error", err)
		return
	}
	// A user-configured Prometheus or SigNoz evaluates the rule itself; anything
	// else keeps the agent's PrometheusRule path. SigNoz resolves here only when it
	// is the account's marked metrics default.
	if (provider == "prometheus" || provider == "signoz") && source == "user" {
		req.MetricProvider = provider
		req.MetricProviderSource = source
	}
}
