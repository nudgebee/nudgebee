"""
Automation notifications (runbook-server run summaries, PR follow-ups, workflow
IM steps, approval prompts) are selected by rules on `event_type`. The rules are
fail-closed, never silence an approval prompt, and leave the finding path alone:
a *finding* whose ingestion source is "automation" is still a troubleshoot finding.
"""

import asyncio
from types import SimpleNamespace

import pytest

from notifications_server.configs.settings import settings
from notifications_server.exceptions.notification_errors import InvalidRuleError
from notifications_server.services.message import (
    AUTOMATION_SOURCE,
    MessageService,
    NotificationRuleMatcher,
    _event_type_matches,
    _finding_rule_source,
    resolve_outcome,
)
from notifications_server.services.rules import AUTOMATION_EVENT_TYPES, _resolve_scope, validate_fields


def _filters(type_, **kwargs):
    return NotificationRuleMatcher.get_filters_from_notification_payload(type_, kwargs)


def _rule(**overrides):
    base = dict(
        id="rule-1",
        name="rule-1",
        source=AUTOMATION_SOURCE,
        account_id=None,
        scope_mode="all",
        account_ids=[],
        missing_columns=frozenset(),
        namespace=None,
        workload=None,
        aggregation_key=None,
        severity=None,
        is_active=True,
        is_suppressed=True,
        expires_at=None,
        delivery_mode="real_time",
        event_types=["automation.task.completed", "automation.task.progress"],
    )
    base.update(overrides)
    return SimpleNamespace(**base)


class _Result:
    def __init__(self, rows):
        self._rows = rows

    def scalars(self):
        return self

    def all(self):
        return self._rows


class _Session:
    async def execute(self, _query):
        return _Result([])


class _RulesService:
    def __init__(self, rules):
        self._rules = rules

    async def get_notification_rules(self, _tenant_id):
        return self._rules


def _automation_payload(event_type, account_id="acc-1", thread=None):
    payload = {
        "type": "generic",
        "source": AUTOMATION_SOURCE,
        "event_type": event_type,
        "parameters": {"message": "hello", "account_id": account_id},
    }
    if thread:
        payload["thread"] = thread
    return payload


# ----------------------------- filters -----------------------------


def test_automation_message_reads_account_from_parameters():
    source, account_id, namespace, workload, agg = _filters(
        "generic", **_automation_payload("automation.task.completed")
    )
    assert source == AUTOMATION_SOURCE
    assert account_id == "acc-1"
    assert namespace is None and workload is None and agg is None


def test_automation_finding_still_folds_to_troubleshoot():
    # Workflow-stored events carry source="automation" but are findings, and
    # tenants' troubleshoot rules must keep applying to them.
    assert _finding_rule_source("automation") == "troubleshoot"
    source, account_id, *_ = _filters(
        "finding",
        source="automation",
        parameters={"finding": {"cloud_account_id": "acc-7"}},
    )
    assert source == "troubleshoot"
    assert account_id == "acc-7"


# ----------------------------- event type selection -----------------------------


def test_event_type_matches_listed_type_only():
    rule = _rule()
    assert _event_type_matches(rule, AUTOMATION_SOURCE, "automation.task.completed")
    assert not _event_type_matches(rule, AUTOMATION_SOURCE, "automation.pr.created")


def test_missing_or_empty_event_types_matches_nothing():
    # Fail closed: a rule without a selection (old cache entry, bad save) is inert.
    assert not _event_type_matches(_rule(event_types=None), AUTOMATION_SOURCE, "automation.task.completed")
    assert not _event_type_matches(_rule(event_types=[]), AUTOMATION_SOURCE, "automation.task.completed")
    assert not _event_type_matches(
        SimpleNamespace(source=AUTOMATION_SOURCE), AUTOMATION_SOURCE, "automation.task.completed"
    )
    assert not _event_type_matches(_rule(), AUTOMATION_SOURCE, None)


def test_other_sources_ignore_event_types():
    assert _event_type_matches(_rule(source="troubleshoot", event_types=None), "troubleshoot", None)


# ----------------------------- matcher end to end -----------------------------


def _match(rules, payload):
    matcher = NotificationRuleMatcher(_RulesService(rules))
    return asyncio.run(matcher.match_notification_rules(_Session(), "tenant-1", "generic", payload))


def test_all_accounts_rule_matches_any_account():
    for account in ("acc-1", "acc-2"):
        matched, source = _match([_rule()], _automation_payload("automation.task.completed", account_id=account))
        assert source == AUTOMATION_SOURCE
        assert [m["rule_id"] for m in matched] == ["rule-1"]
        assert matched[0]["suppressed"] is True


def test_single_account_rule_matches_only_that_account():
    matched, _ = _match(
        [_rule(account_id="acc-1", scope_mode="accounts", account_ids=["acc-1"])],
        _automation_payload("automation.task.completed", account_id="acc-2"),
    )
    assert matched == []


def test_unlisted_event_type_matches_no_rule():
    matched, _ = _match([_rule()], _automation_payload("automation.pr.created"))
    assert matched == []


# ----------------------------- rule effects -----------------------------


def _matched(**overrides):
    row = dict(
        rule_id="rule-1",
        suppressed=True,
        platform=None,
        channels=None,
        expires_at=None,
        delivery_mode=None,
        severity=None,
    )
    row.update(overrides)
    return [row]


def _effect(rows, source=None, event_type=None):
    return resolve_outcome(rows, source=source, event_type=event_type).response


def test_suppressed_rule_drops_task_update():
    result = _effect(_matched(), source=AUTOMATION_SOURCE, event_type="automation.task.completed")
    assert result and result[0]["status"] == "suppressed"


def test_suppressed_reason_names_the_rule():
    result = _effect(
        _matched(name="Silence automation task updates"),
        source=AUTOMATION_SOURCE,
        event_type="automation.task.completed",
    )
    assert result[0]["reason"] == 'Suppressed by notification rule "Silence automation task updates"'


def test_suppressed_reason_falls_back_without_a_rule_name():
    result = _effect(_matched(), source=AUTOMATION_SOURCE, event_type="automation.task.completed")
    assert result[0]["reason"] == "Notification suppressed by notification rule configuration"


def test_suppressed_response_links_to_notification_rules(monkeypatch):
    monkeypatch.setattr(settings, "base_url", "https://app.test")
    result = _effect(
        _matched(name="Silence automation task updates"),
        source=AUTOMATION_SOURCE,
        event_type="automation.task.completed",
    )
    assert result[0]["rule_management_url"] == "https://app.test/user-management#notification-rules"


def test_suppressed_response_has_no_link_without_a_configured_base_url(monkeypatch):
    monkeypatch.setattr(settings, "base_url", "")
    result = _effect(
        _matched(name="Silence automation task updates"),
        source=AUTOMATION_SOURCE,
        event_type="automation.task.completed",
    )
    assert "rule_management_url" not in result[0]


def test_approval_request_is_never_suppressed_or_snoozed():
    for row in (_matched(), _matched(suppressed=False, expires_at=__import__("datetime").datetime.max)):
        assert _effect(row, source=AUTOMATION_SOURCE, event_type="automation.approval.requested") is None


def test_batch_effect_is_skipped_for_automation_messages():
    row = _matched(suppressed=False, delivery_mode="batch")
    assert _effect(row, source=AUTOMATION_SOURCE, event_type="automation.task.completed") is None
    # Findings keep the batch behaviour.
    queued = _effect(row, source="troubleshoot")
    assert queued and queued[0]["status"] == "queued"


class _ThreadMatcher:
    def __init__(self, matched, policy):
        self._matched = matched
        self.rules_service = SimpleNamespace(get_default_policy=self._policy)
        self._policy_value = policy
        self.policy_sources = []

    async def _policy(self, _tenant_id, source):
        self.policy_sources.append(source)
        return self._policy_value

    async def match_notification_rules(self, *_args):
        return self._matched, AUTOMATION_SOURCE


class _ThreadSession:
    def __call__(self):
        return self

    async def __aenter__(self):
        return None

    async def __aexit__(self, *exc):
        return False


def _threaded_send(monkeypatch, matched, policy, event_type):
    from notifications_server.schemas.message import SendMessageRequest
    from notifications_server.services import message as message_module

    monkeypatch.setattr(message_module.BaseDB, "async_session", staticmethod(lambda engine: _ThreadSession()))
    service = MessageService.__new__(MessageService)
    service.engine = None
    service.rule_matcher = _ThreadMatcher(matched, policy)
    sent = []

    async def send_threaded_reply(tenant_id, thread, parameters):
        sent.append(thread)
        return [{"platform": "slack", "status": "success"}]

    service.send_threaded_reply = send_threaded_reply
    request = SendMessageRequest(
        tenant_id="tenant-1",
        source=AUTOMATION_SOURCE,
        event_type=event_type,
        parameters={"message": "run finished", "account_id": "acc-1"},
        thread={"message_ts": "1.2", "channel_id": "C1", "platform": "slack"},
    )
    return asyncio.run(service.send_message(request)), sent, service.rule_matcher.policy_sources


def test_a_threaded_task_update_follows_the_automation_default(monkeypatch):
    drop = {"action": "drop", "mappings": []}
    result, sent, sources = _threaded_send(monkeypatch, [], drop, "automation.task.progress")
    assert result[0]["status"] == "suppressed"
    assert sent == []
    assert sources == [AUTOMATION_SOURCE]


def test_a_threaded_approval_request_ignores_a_drop_default(monkeypatch):
    drop = {"action": "drop", "mappings": []}
    result, sent, _ = _threaded_send(monkeypatch, [], drop, "automation.approval.requested")
    assert result == [{"platform": "slack", "status": "success"}]
    assert len(sent) == 1


# ----------------------------- validation -----------------------------


def test_automation_rule_may_omit_account():
    validate_fields(
        {"name": "Silence chatter", "source": AUTOMATION_SOURCE, "event_types": ["automation.task.completed"]}
    )


def test_automation_rule_requires_known_event_types():
    with pytest.raises(InvalidRuleError):
        validate_fields({"name": "Silence chatter", "source": AUTOMATION_SOURCE})
    with pytest.raises(InvalidRuleError):
        validate_fields({"name": "Silence chatter", "source": AUTOMATION_SOURCE, "event_types": []})
    with pytest.raises(InvalidRuleError):
        validate_fields(
            {"name": "Silence chatter", "source": AUTOMATION_SOURCE, "event_types": ["automation.approval.requested"]}
        )
    assert "automation.approval.requested" not in AUTOMATION_EVENT_TYPES


def test_automation_rule_rejects_batch_delivery():
    with pytest.raises(InvalidRuleError):
        validate_fields(
            {
                "name": "Silence chatter",
                "source": AUTOMATION_SOURCE,
                "event_types": ["automation.task.completed"],
                "delivery_mode": "batch",
            }
        )


def test_account_scoped_sources_still_require_an_account():
    with pytest.raises(InvalidRuleError):
        _resolve_scope({"name": "Prod alerts", "source": "troubleshoot"}, "troubleshoot", None, [])
