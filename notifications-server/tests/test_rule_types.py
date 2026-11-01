"""
A rule covers one type, several types (source_mode "list") or every type,
including ones added later (source_mode "all"). A rule saved before these columns
existed covers [source]. Optimize rules pick optimize messages by event and can
be scoped to accounts. A suppressed rule with an expiry stops messages until then.
The all-types default applies to every type without its own, except the daily
email and the AI cost report. The route check answers with the matcher itself.
"""

import asyncio
import uuid
from datetime import datetime, timedelta
from types import SimpleNamespace

import pytest

from notifications_server.models.models import NotificationRules
from notifications_server.services import route_check as route_check_module
from notifications_server.services.message import NotificationRuleMatcher, resolve_outcome
from notifications_server.services.route_check import RouteCheckService, sample_message
from notifications_server.services.rules import (
    ALL_TYPES_MIRROR_SOURCE,
    SCOPE_ACCOUNTS,
    SCOPE_ALL,
    SOURCE_MODE_ALL,
    SOURCE_MODE_LIST,
    rule_sources,
)
from tests import test_rule_defaults as defaults
from tests.test_rule_account_scope import ACCT_A, ACCT_B, TENANT, _db_rule, _save, _service, _Session


def _rule(rule_id="r", **overrides):
    base = dict(
        id=rule_id,
        name=rule_id,
        source="troubleshoot",
        source_mode=None,
        sources=None,
        account_id=None,
        scope_mode=SCOPE_ALL,
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
        event_types=None,
        exceptions=None,
        created_at=None,
    )
    base.update(overrides)
    return SimpleNamespace(**base)


class _RulesService:
    def __init__(self, rules):
        self._rules = rules

    async def get_notification_rules(self, _tenant_id):
        return self._rules


def _matches(rule, type_, payload):
    matcher = NotificationRuleMatcher(_RulesService([rule]))
    matched, _ = asyncio.run(matcher.match_notification_rules(_Session(), TENANT, type_, payload))
    return bool(matched)


def _message(source, account=ACCT_A, event=None):
    return sample_message({"source": source, "account_id": account, "event": event})


# ----------------------------- which types a rule covers -----------------------------


def test_a_rule_saved_before_the_columns_covers_its_source():
    assert rule_sources(_rule(source="slo")) == ("slo",)


def test_a_rule_over_several_types_matches_each_of_them_only():
    rule = _rule(source_mode=SOURCE_MODE_LIST, sources=["troubleshoot", "slo"])
    assert _matches(rule, *_message("troubleshoot"))
    assert _matches(rule, *_message("slo"))
    assert not _matches(rule, *_message("cloud"))


def test_an_all_types_rule_matches_every_type_but_the_singletons():
    rule = _rule(source_mode=SOURCE_MODE_ALL)
    for source in ("troubleshoot", "slo", "cloud", "optimize", "weekly_digest"):
        assert _matches(rule, *_message(source)), source
    assert _matches(rule, *_message("automation", event="automation.task.completed"))
    assert not _matches(rule, *_message("daily_recap"))
    assert not _matches(rule, *_message("ai_cost"))


def test_an_automation_only_rule_still_needs_its_events():
    rule = _rule(source="automation", source_mode=SOURCE_MODE_LIST, sources=["automation"])
    assert not _matches(rule, *_message("automation", event="automation.task.completed"))
    rule.event_types = ["automation.task.completed"]
    assert _matches(rule, *_message("automation", event="automation.task.completed"))
    assert not _matches(rule, *_message("automation", event="automation.task.failed"))


def test_a_rule_over_several_types_covers_every_automation_event():
    rule = _rule(source_mode=SOURCE_MODE_LIST, sources=["troubleshoot", "automation"])
    assert _matches(rule, *_message("automation", event="automation.task.failed"))


def test_the_rule_cache_keeps_the_types():
    rule = NotificationRules(id=uuid.uuid4(), tenant_id=TENANT, name="r", source="slo")
    rule.source_mode = SOURCE_MODE_LIST
    rule.sources = ["slo", "cloud"]
    restored = NotificationRules.from_dict(rule.to_dict())
    assert rule_sources(restored) == ("slo", "cloud")


# ----------------------------- optimize -----------------------------


@pytest.mark.parametrize(
    "event",
    [
        "optimize.recommendation.applied",
        "optimize.alert.fixed",
        "optimize.digest",
        "optimize.nudge",
        "optimize.security_posture",
    ],
)
def test_an_optimize_rule_without_events_covers_every_optimize_message(event):
    assert _matches(_rule(source="optimize"), *_message("optimize", event=event))


def test_an_optimize_rule_with_events_covers_only_those():
    rule = _rule(source="optimize", event_types=["optimize.security_posture"])
    assert _matches(rule, *_message("optimize", event="optimize.security_posture"))
    assert not _matches(rule, *_message("optimize", event="optimize.digest"))


def test_an_optimize_rule_can_be_scoped_to_accounts():
    rule = _rule(source="optimize", scope_mode=SCOPE_ACCOUNTS, account_ids=[ACCT_A])
    assert _matches(rule, *_message("optimize", ACCT_A, "optimize.recommendation.applied"))
    assert not _matches(rule, *_message("optimize", ACCT_B, "optimize.recommendation.applied"))


# ----------------------------- saving -----------------------------


def _saved(session):
    return next(o for o in session.added if isinstance(o, NotificationRules))


def test_save_writes_the_types_and_the_mirror(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    result = _save(
        _service(monkeypatch, session),
        {"name": "Prod", "sources": ["slo", "troubleshoot"], "account_ids": [ACCT_A]},
    )
    assert "error" not in result, result
    rule = _saved(session)
    assert (rule.source_mode, rule.sources, rule.source) == (SOURCE_MODE_LIST, ["slo", "troubleshoot"], "slo")


def test_save_an_all_types_rule(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    result = _save(
        _service(monkeypatch, session),
        {"name": "Quiet QA", "source_mode": "all", "account_ids": [ACCT_A], "is_suppressed": True},
    )
    assert "error" not in result, result
    rule = _saved(session)
    assert (rule.source_mode, rule.sources, rule.source, rule.scope_mode) == (
        SOURCE_MODE_ALL,
        None,
        ALL_TYPES_MIRROR_SOURCE,
        SCOPE_ACCOUNTS,
    )


def test_an_older_client_saves_a_rule_for_its_source(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    _save(_service(monkeypatch, session), {"name": "Prod", "source": "cloud", "account_ids": [ACCT_A]})
    rule = _saved(session)
    assert (rule.source_mode, rule.sources) == (SOURCE_MODE_LIST, ["cloud"])


def test_an_update_without_types_keeps_them(monkeypatch):
    existing = _db_rule(SCOPE_ALL)
    existing.source_mode = SOURCE_MODE_LIST
    existing.sources = ["troubleshoot", "slo"]
    session = _Session(rules=[existing])
    result = _save(
        _service(monkeypatch, session),
        {"id": str(existing.id), "name": "Prod alerts", "source": "troubleshoot", "scope_mode": "all"},
    )
    assert "error" not in result, result
    assert existing.sources == ["troubleshoot", "slo"]


@pytest.mark.parametrize(
    "rule",
    [
        {"sources": ["troubleshoot", "daily_recap"]},
        {"sources": ["ai_cost", "slo"]},
        {"sources": ["pagerduty"]},
        {"sources": []},
        {"source_mode": "some"},
        {"sources": ["troubleshoot", "automation"], "event_types": ["automation.task.completed"]},
        {"sources": ["optimize"], "event_types": ["optimize.everything"]},
        {"sources": ["troubleshoot", "slo"], "delivery_mode": "batch", "frequency": "hourly"},
    ],
)
def test_bad_types_are_rejected(monkeypatch, rule):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    result = _save(_service(monkeypatch, session), {"name": "Bad", "scope_mode": "all", **rule})
    assert result["status_code"] == 400, result
    assert not session.committed


def test_an_optimize_rule_saves_accounts_and_events(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    result = _save(
        _service(monkeypatch, session),
        {
            "name": "Posture",
            "sources": ["optimize"],
            "account_ids": [ACCT_A],
            "event_types": ["optimize.security_posture"],
        },
    )
    assert "error" not in result, result
    rule = _saved(session)
    assert (rule.scope_mode, rule.event_types) == (SCOPE_ACCOUNTS, ["optimize.security_posture"])


def test_a_weekly_digest_rule_always_covers_every_account(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    _save(_service(monkeypatch, session), {"name": "Digest", "sources": ["weekly_digest"], "account_ids": [ACCT_A]})
    assert _saved(session).scope_mode == SCOPE_ALL


def test_a_rule_over_an_account_type_and_the_weekly_digest_keeps_its_accounts(monkeypatch):
    session = _Session(accounts={ACCT_A: "k8s-prod"})
    _save(
        _service(monkeypatch, session),
        {"name": "Prod", "sources": ["troubleshoot", "weekly_digest"], "account_ids": [ACCT_A]},
    )
    assert _saved(session).scope_mode == SCOPE_ACCOUNTS


# ----------------------------- timed don't send -----------------------------


def _stop(expires_at):
    return {
        "rule_id": "stop",
        "suppressed": True,
        "platform": None,
        "channels": None,
        "expires_at": expires_at,
        "delivery_mode": "real_time",
        "severity": None,
    }


def _send():
    return dict(_stop(None), rule_id="send", suppressed=False, platform="slack", channels={"id": "#prod"})


def test_a_timed_dont_send_stops_until_it_ends():
    outcome = resolve_outcome([_stop(datetime.utcnow() + timedelta(hours=1)), _send()])
    assert outcome.response[0]["status"] == "suppressed"


def test_an_ended_dont_send_does_nothing():
    outcome = resolve_outcome([_stop(datetime.utcnow() - timedelta(minutes=1)), _send()])
    assert outcome.response is None
    assert [t["channels"]["id"] for t in outcome.targets] == ["#prod"]


def test_an_ended_dont_send_is_not_a_matched_rule_for_the_default():
    outcome = resolve_outcome([_stop(datetime.utcnow() - timedelta(minutes=1))], default_policy=defaults.CHANNELS)
    assert [t["channels"]["id"] for t in outcome.targets] == ["#oncall"]


def test_a_permanent_dont_send_never_ends():
    assert resolve_outcome([_stop(None), _send()]).response[0]["status"] == "suppressed"


# ----------------------------- defaults -----------------------------


def _policies(monkeypatch, rows):
    return defaults._service(monkeypatch, defaults._Session(rows=rows))


def _row(source, action):
    return SimpleNamespace(source=source, action=action, mappings=[])


def test_a_type_uses_its_own_default_then_the_all_types_default(monkeypatch):
    service = _policies(monkeypatch, [_row("all", "drop"), _row("cloud", "deliver_defaults")])
    assert asyncio.run(service.get_default_policy(TENANT, "cloud"))["action"] == "deliver_defaults"
    assert asyncio.run(service.get_default_policy(TENANT, "slo"))["action"] == "drop"


def test_the_singletons_never_follow_the_all_types_default(monkeypatch):
    service = _policies(monkeypatch, [_row("all", "drop")])
    assert asyncio.run(service.get_default_policy(TENANT, "daily_recap")) is None
    assert asyncio.run(service.get_default_policy(TENANT, "ai_cost")) is None


def test_no_default_at_all_is_the_built_in_one(monkeypatch):
    assert asyncio.run(_policies(monkeypatch, []).get_default_policy(TENANT, "slo")) is None


class _DeletingSession(defaults._Session):
    def __init__(self):
        super().__init__()
        self.deleted_sql = []

    async def execute(self, statement, params=None):
        if getattr(statement, "is_delete", False):
            self.deleted_sql.append(str(statement))
            return None
        return await super().execute(statement, params)


def test_inherit_removes_a_types_own_default(monkeypatch):
    session = _DeletingSession()
    service = defaults._service(monkeypatch, session)
    result = defaults._upsert(service, {"source": "cloud", "action": "inherit"})
    assert "error" not in result, result
    assert session.deleted_sql and "notification_default_policies" in session.deleted_sql[0]
    assert session.saved == {} and session.committed


def test_the_all_types_default_cannot_inherit(monkeypatch):
    service = defaults._service(monkeypatch, defaults._Session())
    assert defaults._upsert(service, {"source": "all", "action": "inherit"})["status_code"] == 400


def test_the_all_types_default_saves(monkeypatch):
    session = defaults._Session()
    service = defaults._service(monkeypatch, session)
    assert "error" not in defaults._upsert(service, {"source": "all", "action": "drop"})
    assert session.saved[(TENANT, "all")].action == "drop"


# ----------------------------- route check -----------------------------


def _check(monkeypatch, rules, request, policy=None):
    service = RouteCheckService(engine=None)
    fake = _RulesService(rules)

    async def get_default_policy(_tenant, _source):
        return policy

    fake.get_default_policy = get_default_policy
    service.rules_service = fake
    service.matcher = NotificationRuleMatcher(fake)
    monkeypatch.setattr(route_check_module.BaseDB, "async_session", staticmethod(lambda engine: _NoSession()))
    return asyncio.run(
        service.check_route(
            action={"name": "notifications_check_route"},
            input={"request": request},
            session_variables={"tenant_id": TENANT},
        )
    )


class _NoSession:
    def __call__(self):
        return self

    async def __aenter__(self):
        return _MappingsSession(self)

    async def __aexit__(self, *exc):
        return False


class _MappingsSession:
    mappings = []

    def __init__(self, _factory):
        pass

    async def execute(self, _statement):
        mappings = _MappingsSession.mappings
        return SimpleNamespace(scalars=lambda: SimpleNamespace(all=lambda: mappings))


def test_check_names_the_rule_that_stops_it(monkeypatch):
    stop = _rule("silence", name="Silence all troubleshooting", exceptions=[{"account_ids": [ACCT_B]}])
    answer = _check(monkeypatch, [stop], {"source": "troubleshoot", "account_id": ACCT_A})
    assert answer["result"] == "not_sent"
    assert answer["stopped_by"] == {"id": "silence", "name": "Silence all troubleshooting"}

    answer = _check(monkeypatch, [stop], {"source": "troubleshoot", "account_id": ACCT_B})
    assert (answer["result"], answer["reason"], answer["default_channels"]) == ("sent", "default_policy", True)


def test_check_returns_the_rules_channels(monkeypatch):
    send = _rule("prod", name="Prod to #prod", is_suppressed=False, scope_mode=SCOPE_ACCOUNTS, account_ids=[ACCT_A])
    _MappingsSession.mappings = [SimpleNamespace(rule_id="prod", platform="slack", channels={"id": "#prod"})]
    try:
        answer = _check(monkeypatch, [send], {"source": "troubleshoot", "account_id": ACCT_A})
    finally:
        _MappingsSession.mappings = []
    assert answer["result"] == "sent"
    assert answer["channels"] == [{"platform": "slack", "channels": {"id": "#prod"}, "rule_id": "prod"}]
    assert answer["rules"] == [{"id": "prod", "name": "Prod to #prod"}]


def test_check_uses_the_default(monkeypatch):
    answer = _check(monkeypatch, [], {"source": "slo", "account_id": ACCT_A}, policy=defaults.DROP)
    assert (answer["result"], answer["reason"]) == ("not_sent", "default_policy")


def test_check_says_approvals_always_go_out(monkeypatch):
    stop = _rule("mute", source="automation", event_types=["automation.task.completed"], sources=None, source_mode=None)
    answer = _check(
        monkeypatch,
        [stop],
        {"source": "automation", "account_id": ACCT_A, "event": "automation.approval.requested"},
        policy=defaults.DROP,
    )
    assert answer["result"] == "sent"


APPROVAL = {"source": "automation", "account_id": ACCT_A, "event": "automation.approval.requested"}
TASK_FAILED = {"source": "automation", "account_id": ACCT_A, "event": "automation.task.failed"}


def _every_type(rule_id, **overrides):
    return _rule(rule_id, source_mode="all", scope_mode=SCOPE_ACCOUNTS, account_ids=[ACCT_A], **overrides)


@pytest.mark.parametrize(
    "stop",
    [
        _every_type("mute", name="Stop account A"),
        _every_type("pause", name="Pause account A", expires_at=datetime.utcnow() + timedelta(hours=1)),
    ],
)
def test_check_does_not_credit_a_dont_send_rule_for_an_approval(monkeypatch, stop):
    assert _check(monkeypatch, [stop], TASK_FAILED)["stopped_by"]["id"] == stop.id
    answer = _check(monkeypatch, [stop], APPROVAL)
    assert (answer["result"], answer["reason"], answer["rules"]) == ("sent", "always_sent", [])
    assert answer["stopped_by"] is None and answer["default_channels"] is True


def test_check_says_approvals_go_out_despite_a_dont_send_default(monkeypatch):
    answer = _check(monkeypatch, [], APPROVAL, policy=defaults.DROP)
    assert (answer["result"], answer["reason"], answer["default_channels"]) == ("sent", "always_sent", True)


def test_check_credits_a_rule_that_delivers_an_approval(monkeypatch):
    send = _every_type("ops", name="Account A to #ops", is_suppressed=False)
    answer = _check(monkeypatch, [send], APPROVAL, policy=defaults.DROP)
    assert (answer["result"], answer["reason"]) == ("sent", "rule")
    assert answer["rules"] == [{"id": "ops", "name": "Account A to #ops"}]


def test_check_credits_a_send_rule_whose_old_pause_has_ended(monkeypatch):
    send = _every_type(
        "ops", name="Account A to #ops", is_suppressed=False, expires_at=datetime.utcnow() - timedelta(hours=1)
    )
    _MappingsSession.mappings = [SimpleNamespace(rule_id="ops", platform="slack", channels={"id": "#ops"})]
    try:
        answer = _check(monkeypatch, [send], APPROVAL)
    finally:
        _MappingsSession.mappings = []
    assert (answer["result"], answer["reason"]) == ("sent", "rule")
    assert answer["rules"] == [{"id": "ops", "name": "Account A to #ops"}]


def test_check_credits_the_default_channels_for_an_approval(monkeypatch):
    answer = _check(monkeypatch, [], APPROVAL, policy=defaults.CHANNELS)
    assert (answer["result"], answer["reason"]) == ("sent", "default_policy")
    assert answer["channels"] == [{"platform": "slack", "channels": {"id": "#oncall"}, "rule_id": None}]


def test_check_reports_a_timed_dont_send_that_ended_as_nothing(monkeypatch):
    ended = _rule("maintenance", expires_at=datetime.utcnow() - timedelta(minutes=1))
    answer = _check(monkeypatch, [ended], {"source": "troubleshoot", "account_id": ACCT_A})
    assert answer["result"] == "sent" and answer["rules"] == []


@pytest.mark.parametrize(
    "request_body",
    [
        {"source": "troubleshoot", "account_id": ACCT_A, "severity": "low"},
        {"source": "optimize", "account_id": ACCT_A, "event": "optimize.alert.fixed", "severity": "low"},
    ],
)
def test_check_holds_low_findings_like_the_send_path(monkeypatch, request_body):
    answer = _check(monkeypatch, [], request_body)
    assert (answer["result"], answer["reason"]) == ("not_sent", "severity")


@pytest.mark.parametrize(
    "request_body",
    [
        {"source": "pagerduty"},
        {"source": "automation", "event": "automation.nothing"},
        {"source": "optimize", "event": "optimize.everything"},
    ],
)
def test_check_rejects_bad_input(monkeypatch, request_body):
    assert _check(monkeypatch, [], request_body)["status_code"] == 400
