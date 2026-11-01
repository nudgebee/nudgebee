"""Where a notification would go under a tenant's current rules, answered by the
same matcher and outcome the send path uses, without sending anything."""

import logging

from notifications_server.models.db_base import BaseDB
from notifications_server.services.message import (
    NON_SUPPRESSIBLE_EVENT_TYPES,
    NotificationRuleMatcher,
    is_realtime_severity_gated,
    resolve_outcome,
)
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.services.rules import (
    AUTOMATION_EVENT_TYPES,
    DEFAULT_CHANNELS,
    OPTIMIZE_EVENT_BY_MESSAGE_TYPE,
    RULE_SOURCES,
    NotificationRulesService,
)

LOG = logging.getLogger(__name__)

APPROVAL_EVENT = "automation.approval.requested"
_MESSAGE_TYPE_BY_OPTIMIZE_EVENT = {
    event: message_type for message_type, event in OPTIMIZE_EVENT_BY_MESSAGE_TYPE.items()
}
_RESULT_BY_STATUS = {"suppressed": "not_sent", "snoozed": "paused", "queued": "held"}


def sample_message(request):
    """The (type, payload) a producer would send for this request, shaped the way
    the matcher reads it."""
    source = request.get("source")
    event = request.get("event")
    account_id = request.get("account_id")
    if source == "troubleshoot":
        finding = {
            "cloud_account_id": account_id,
            "subject_namespace": request.get("namespace"),
            "subject_owner": request.get("workload"),
            "aggregation_key": request.get("aggregation_key"),
            "priority": request.get("severity") or "high",
        }
        return "finding", {"source": "troubleshoot", "parameters": {"finding": finding}}
    if source == "slo":
        parameters = {
            "account_id": account_id,
            "namespace": request.get("namespace"),
            "workload": request.get("workload"),
        }
        return "slo_alert", {"source": "slo", "parameters": parameters}
    if source == "cloud":
        return "cloud_cost_summary", {"source": "cloud", "parameters": {"account_id": account_id}}
    if source == "optimize":
        message_type = _MESSAGE_TYPE_BY_OPTIMIZE_EVENT.get(event, "recommendation_resolution")
        if message_type == "finding":
            finding = {"cloud_account_id": account_id, "priority": request.get("severity") or "high"}
            return "finding", {"source": "optimize", "parameters": {"finding": finding}}
        return message_type, {"source": "optimize", "parameters": {"account_id": account_id}}
    if source == "automation":
        return "generic", {"source": "automation", "event_type": event, "parameters": {"account_id": account_id}}
    return source, {"source": source, "parameters": {}}


def _validate(request):
    source = request.get("source")
    if source not in RULE_SOURCES:
        return "source"
    event = request.get("event")
    if source == "automation" and event not in AUTOMATION_EVENT_TYPES + (APPROVAL_EVENT,):
        return "event"
    if source == "optimize" and event is not None and event not in _MESSAGE_TYPE_BY_OPTIMIZE_EVENT:
        return "event"
    return None


class RouteCheckService:
    def __init__(self, engine):
        self.engine = engine
        self.rules_service = NotificationRulesService(engine)
        self.matcher = NotificationRuleMatcher(self.rules_service)

    async def check_route(self, **kwargs):
        if (kwargs.get("action") or {}).get("name") != "notifications_check_route":
            return {"error": "Unsupported action", "status_code": 400}
        tenant_id = (kwargs.get("session_variables") or {}).get("tenant_id")
        request = (kwargs.get("input") or {}).get("request") or {}
        if not tenant_id:
            return {"error": "tenant is required", "status_code": 400}
        invalid = _validate(request)
        if invalid:
            return {"error": f"Invalid {invalid}", "status_code": 400}

        type_, payload = sample_message(request)
        async with BaseDB.async_session(self.engine)() as session:
            matched, source = await self.matcher.match_notification_rules(session, tenant_id, type_, payload)
        rules = await self.rules_service.get_notification_rules(tenant_id) or []
        names = {str(rule.id): rule.name for rule in rules}
        now = utc_now()
        # An event that is never stopped ignores every rule that would hold it back,
        # so those rules must not be named as the reason it went out.
        never_stopped = payload.get("event_type") in NON_SUPPRESSIBLE_EVENT_TYPES
        matched_rules = []
        for entry in matched:
            if entry["suppressed"] and entry["expires_at"] and entry["expires_at"] <= now:
                continue
            paused = bool(entry["expires_at"]) and entry["expires_at"] > now
            if never_stopped and (entry["suppressed"] or paused or entry.get("delivery_mode") == "batch"):
                continue
            rule_id = str(entry["rule_id"])
            if rule_id not in (r["id"] for r in matched_rules):
                matched_rules.append({"id": rule_id, "name": names.get(rule_id)})

        answer = {"rules": matched_rules, "channels": [], "default_channels": False, "stopped_by": None}
        priority = ((payload.get("parameters") or {}).get("finding") or {}).get("priority")
        if type_ == "finding" and is_realtime_severity_gated(priority, matched):
            return {**answer, "result": "not_sent", "reason": "severity"}

        default_policy = await self.rules_service.get_default_policy(tenant_id, source)
        outcome = resolve_outcome(
            matched, source=source, event_type=payload.get("event_type"), default_policy=default_policy
        )
        if outcome.response is not None:
            status = (outcome.response[0] or {}).get("status") if outcome.response else None
            stopped_by = str(outcome.stopped_by) if outcome.stopped_by else None
            reason = "rule" if stopped_by else "default_policy"
            return {
                **answer,
                "result": _RESULT_BY_STATUS.get(status, "not_sent"),
                "reason": reason,
                "stopped_by": {"id": stopped_by, "name": names.get(stopped_by)} if stopped_by else None,
            }
        channels = [
            {
                "platform": target["platform"],
                "channels": target["channels"],
                "rule_id": str(target["rule_id"]) if target.get("rule_id") else None,
            }
            for target in outcome.targets
        ]
        delivering_rule = any(target.get("rule_id") for target in outcome.targets) or bool(matched_rules)
        if delivering_rule:
            reason = "rule"
        elif never_stopped and (default_policy or {}).get("action") != DEFAULT_CHANNELS:
            reason = "always_sent"
        else:
            reason = "default_policy"
        return {
            **answer,
            "result": "sent",
            "reason": reason,
            "channels": channels,
            "default_channels": outcome.use_defaults,
        }
