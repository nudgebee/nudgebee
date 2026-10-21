"""
A CPU SLO forecast (api-server `source = "forecast"`, aggregation_key
CPUThrottlingPredicted / CPUSaturationPredicted) is published as an ordinary
finding. It routes to the "slo" rule category, so an "SLO -> #slo-alerts" rule
receives it, but it is still matched and fingerprinted as a finding: it is not
an `slo_alert`, and it joins neither the SLO digest nor the anomaly digest.
"""

import asyncio
import json
from types import SimpleNamespace

import pytest

from notifications_server.configs.settings import settings
from notifications_server.services import message as message_module
from notifications_server.services.message import NotificationRuleMatcher
from tests.test_delivery_split import TENANT, _service

SLO_CHANNEL = {"id": "C-SLO"}
TROUBLESHOOT_CHANNEL = {"id": "C-TROUBLESHOOT"}


def _finding(**overrides):
    finding = {
        "id": "evt-1",
        "source": "forecast",
        "aggregation_key": "CPUThrottlingPredicted",
        "priority": "HIGH",
        "status": "FIRING",
        "fingerprint": "fp-forecast-1",
        "cloud_account_id": "acc-1",
        "subject_namespace": "payments",
        "subject_owner": "api",
    }
    finding.update(overrides)
    return finding


def _request(type_="finding", **finding_overrides):
    payload = {
        "kind": "notification",
        "type": type_,
        "source": "forecast",
        "tenant_id": TENANT,
        "parameters": {"finding": _finding(**finding_overrides)},
    }
    return SimpleNamespace(
        tenant_id=TENANT,
        type=type_,
        source="forecast",
        thread=None,
        dict=lambda **_kwargs: json.loads(json.dumps(payload)),
    )


def _rule(rule_id, source, **overrides):
    base = dict(
        id=rule_id,
        name=rule_id,
        source=source,
        account_id=None,
        scope_mode="all",
        account_ids=[],
        missing_columns=frozenset(),
        namespace=None,
        workload=None,
        aggregation_key=None,
        severity=None,
        is_active=True,
        is_suppressed=False,
        expires_at=None,
        delivery_mode="real_time",
        event_types=None,
    )
    base.update(overrides)
    return SimpleNamespace(**base)


RULES = [_rule("rule-slo", "slo"), _rule("rule-troubleshoot", "troubleshoot")]
CHANNEL_BY_RULE = {"rule-slo": SLO_CHANNEL, "rule-troubleshoot": TROUBLESHOOT_CHANNEL}


class _RulesService:
    def __init__(self, rules):
        self._rules = rules

    async def get_notification_rules(self, _tenant_id):
        return self._rules

    async def get_default_policy(self, _tenant_id, _source):
        return None


class _Result:
    def __init__(self, rows):
        self._rows = rows

    def scalars(self):
        return self

    def all(self):
        return self._rows


class _Session:
    """Answers the rule-mapping query with each matched rule's channel."""

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_exc):
        return False

    async def execute(self, _query):
        return _Result(
            [
                SimpleNamespace(rule_id=rule_id, platform="slack", channels=channel)
                for rule_id, channel in CHANNEL_BY_RULE.items()
            ]
        )


@pytest.fixture
def service(monkeypatch):
    service = _service({}, monkeypatch)
    service.rule_matcher = NotificationRuleMatcher(_RulesService(RULES))
    monkeypatch.setattr(message_module.BaseDB, "async_session", lambda _engine: lambda: _Session())
    service.queued = []
    service.triggered = []
    service.replied = []

    async def enqueue(payload):
        service.queued.append(payload)
        return {"status": "queued"}

    async def trigger(_session, fingerprint, platforms, _kwargs, _parameters, _responses, _tenant, type_):
        service.triggered.append((type_, fingerprint, platforms[0].to_channel))
        return [{"status": "success"}]

    async def reply(tenant_id, parameters):
        service.replied.append((tenant_id, parameters))
        return [{"status": "success"}]

    service.group_handler = SimpleNamespace(enqueue=enqueue)
    service.trigger_notification = trigger
    service.send_finding_resolved_reply = reply
    return service


# ----------------------------- filters -----------------------------


def test_forecast_finding_filters_are_finding_shaped_under_slo():
    filters = NotificationRuleMatcher.get_filters_from_notification_payload(
        "finding", {"source": "forecast", "parameters": {"finding": _finding()}}
    )
    assert filters == ("slo", "acc-1", "payments", "api", "CPUThrottlingPredicted")


# ----------------------------- delivery -----------------------------


@pytest.mark.parametrize("aggregation_key", ["CPUThrottlingPredicted", "CPUSaturationPredicted"])
def test_forecast_finding_goes_to_the_slo_rule_channel(service, aggregation_key):
    result = asyncio.run(service.send_message(_request(aggregation_key=aggregation_key)))

    assert result == [{"status": "success"}]
    assert service.queued == []
    # The finding's own fingerprint, as for every finding, and only the SLO rule's channel.
    assert service.triggered == [("finding", "fp-forecast-1", SLO_CHANNEL)]


def test_slo_rule_scoped_to_a_forecast_key_matches_it(service):
    service.rule_matcher = NotificationRuleMatcher(
        _RulesService([_rule("rule-slo", "slo", aggregation_key="CPUSaturationPredicted", namespace="payments")])
    )

    asyncio.run(service.send_message(_request(aggregation_key="CPUSaturationPredicted")))
    asyncio.run(service.send_message(_request(aggregation_key="CPUThrottlingPredicted")))

    channels = [channel for _type, _fingerprint, channel in service.triggered]
    assert channels[0] == SLO_CHANNEL
    # The other key matches no rule and follows the default, not the SLO rule.
    assert channels[1] != SLO_CHANNEL


# ----------------------------- digests -----------------------------


@pytest.mark.parametrize("flag", [True, False])
def test_forecast_resolve_goes_to_the_thread_reply_not_a_digest(service, monkeypatch, flag):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", flag)

    result = asyncio.run(service.send_message(_request("finding_resolved", status="RESOLVED")))

    assert result == [{"status": "success"}]
    assert service.queued == []
    assert [tenant for tenant, _parameters in service.replied] == [TENANT]


def test_forecast_finding_is_not_queued_for_the_slo_or_anomaly_digest(service):
    parameters = {"finding": _finding()}

    assert asyncio.run(service._maybe_handle_grouped_message("forecast", parameters, TENANT, {})) is None
    assert asyncio.run(service._maybe_enqueue_anomaly(parameters, TENANT)) is None
    assert service.queued == []
