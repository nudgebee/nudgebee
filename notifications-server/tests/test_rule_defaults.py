"""
When no rule matches a notification, the source's default policy decides: every
platform's default channel (also when no policy is saved), only the policy's
channels, or nothing. When rules match, the default adds no channels, and
platforms no matching rule names fall back to their default channel only under
the first. Approval requests are never
dropped. A missing policy table (a release ahead of its migration) reads as no
saved policies.
"""

import asyncio
import uuid
from types import SimpleNamespace

import pytest
from sqlalchemy.exc import ProgrammingError

from notifications_server.models.models import NotificationDefaultPolicy
from notifications_server.services import rules as rules_module
from notifications_server.services.message import resolve_outcome
from notifications_server.services.rules import NotificationRulesService
from notifications_server.services.cache import AsyncCache

TENANT = "22222222-2222-2222-2222-222222222222"
DELIVER = {"action": "deliver_defaults", "mappings": []}
CHANNELS = {"action": "deliver_channels", "mappings": [{"platform": "slack", "channels": {"id": "#oncall"}}]}
DROP = {"action": "drop", "mappings": []}


def _entry(rule_id, channel=None, platform="slack", suppressed=False):
    return {
        "rule_id": rule_id,
        "suppressed": suppressed,
        "platform": platform,
        "channels": {"id": channel} if channel else None,
        "expires_at": None,
        "delivery_mode": "real_time",
        "severity": None,
    }


def _channels(outcome):
    return [entry["channels"]["id"] for entry in outcome.targets]


@pytest.mark.parametrize("policy", [None, DELIVER])
def test_with_no_matching_rule_the_built_in_default_delivers_everywhere(policy):
    outcome = resolve_outcome([], default_policy=policy)
    assert (outcome.response, outcome.targets, outcome.use_defaults) == (None, [], True)


def test_a_channels_default_sends_only_to_its_channels():
    outcome = resolve_outcome([], default_policy=CHANNELS)
    assert _channels(outcome) == ["#oncall"]
    assert outcome.use_defaults is False


def test_a_drop_default_sends_nothing():
    outcome = resolve_outcome([], default_policy=DROP)
    assert outcome.response[0]["status"] == "suppressed"


def test_a_matching_rule_never_gets_the_defaults_channels():
    for policy, falls_back in ((None, True), (DELIVER, True), (CHANNELS, False), (DROP, False)):
        outcome = resolve_outcome([_entry("prod", "#prod"), _entry("dev", "#dev")], default_policy=policy)
        assert _channels(outcome) == ["#prod", "#dev"]
        # Under "chosen channels" or "drop" only rules decide, so platforms no rule names get nothing.
        assert outcome.use_defaults is falls_back


def test_a_matching_rule_without_channels_still_counts_as_a_match():
    outcome = resolve_outcome([_entry("prod", platform=None)], default_policy=DROP)
    assert (outcome.response, outcome.targets, outcome.use_defaults) == (None, [], False)


def test_a_silence_rule_still_stops_under_any_default():
    for policy in (None, CHANNELS, DROP):
        outcome = resolve_outcome(
            [_entry("prod", "#prod"), _entry("all", platform=None, suppressed=True)], default_policy=policy
        )
        assert outcome.response[0]["status"] == "suppressed"
        assert outcome.targets == []


def test_approvals_are_never_dropped():
    outcome = resolve_outcome([], source="automation", event_type="automation.approval.requested", default_policy=DROP)
    assert (outcome.response, outcome.use_defaults) == (None, True)
    routed = resolve_outcome(
        [], source="automation", event_type="automation.approval.requested", default_policy=CHANNELS
    )
    assert _channels(routed) == ["#oncall"]


# ----------------------------- email -----------------------------


def _email_task(monkeypatch, matched, policy):
    from notifications_server.processors import notification as processor_module
    from notifications_server.processors.notification import NotificationProcessor

    class _NoSession:
        def __call__(self):
            return self

        async def __aenter__(self):
            return None

        async def __aexit__(self, *exc):
            return False

    class _Matcher:
        async def match_notification_rules(self, *_args):
            return matched, "daily_recap"

    class _Defaults:
        async def get_default_policy(self, _tenant, _source):
            return policy

    monkeypatch.setattr(processor_module.BaseDB, "async_session", staticmethod(lambda engine: _NoSession()))
    processor = NotificationProcessor.__new__(NotificationProcessor)
    processor.engine = None
    processor._rule_matcher = _Matcher()
    processor._rules_service = _Defaults()
    return asyncio.run(processor._get_configured_email_rules(TENANT, {"source": "daily_recap"}))


def test_a_drop_default_stops_an_unmatched_email_task(monkeypatch):
    assert _email_task(monkeypatch, [], DROP) is None


def test_a_channels_default_replaces_the_task_recipients(monkeypatch):
    policy = {"action": "deliver_channels", "mappings": [{"platform": "email", "channels": {"emails": ["ops@x"]}}]}
    assert _email_task(monkeypatch, [], policy) == (["ops@x"], [], False)


# ----------------------------- loading and saving -----------------------------


class _PgError(Exception):
    def __init__(self, sqlstate):
        super().__init__(sqlstate)
        self.sqlstate = sqlstate


class _Session:
    def __init__(self, rows=(), error=None):
        self.rows = list(rows)
        self.error = error
        self.saved = {}
        self.committed = False

    async def execute(self, statement, params=None):
        if self.error is not None:
            raise self.error
        if getattr(statement, "is_insert", False):
            values = statement.compile().params
            self.saved[(values["tenant_id"], values["source"])] = SimpleNamespace(**values)
            self.upsert_sql = str(statement)
        return SimpleNamespace(scalars=lambda: iter(self.rows))

    async def commit(self):
        self.committed = True


class _Cache:
    redis_client = object()
    aio = property(AsyncCache)

    def __init__(self):
        self.stored = None
        self.invalidated = 0

    def get_default_policies(self, _tenant):
        return self.stored

    def cache_default_policies(self, _tenant, policies):
        self.stored = policies

    def delete_cached_notification_rules(self, _tenant):
        self.invalidated += 1


def _service(monkeypatch, session):
    class _Factory:
        def __call__(self):
            return self

        async def __aenter__(self):
            return session

        async def __aexit__(self, *exc):
            return False

    monkeypatch.setattr(rules_module.BaseDB, "async_session", staticmethod(lambda engine: _Factory()))
    monkeypatch.setattr(rules_module, "create_audit_request", lambda **kwargs: None)
    service = NotificationRulesService(engine=None)
    service.cache = _Cache()
    return service


def test_saved_policies_are_read_and_cached(monkeypatch):
    row = NotificationDefaultPolicy(tenant_id=TENANT, source="troubleshoot", action="drop", mappings=[])
    service = _service(monkeypatch, _Session(rows=[row]))

    policies = asyncio.run(service.get_default_policies(TENANT))

    assert policies == {"troubleshoot": {"action": "drop", "mappings": []}}
    assert service.cache.stored == policies
    assert asyncio.run(service.get_default_policy(TENANT, "slo")) is None


def test_a_missing_table_reads_as_no_policies_and_is_not_cached(monkeypatch):
    error = ProgrammingError("SELECT ...", {}, _PgError("42P01"))
    service = _service(monkeypatch, _Session(error=error))

    assert asyncio.run(service.get_default_policies(TENANT)) == {}
    assert service.cache.stored is None


def _upsert(service, request):
    return asyncio.run(
        service.upsert_default_policy(
            action={"name": "notifications_upsert_default_policy"},
            input={"request": request},
            session_variables={"tenant_id": TENANT, "user_id": str(uuid.uuid4())},
        )
    )


@pytest.mark.parametrize(
    "request_body",
    [
        {"source": "unknown", "action": "drop"},
        {"source": "troubleshoot", "action": "silence"},
        {"source": "troubleshoot", "action": "deliver_channels"},
        {"source": "troubleshoot", "action": "deliver_channels", "mappings": [{"platform": "slack"}]},
    ],
)
def test_upsert_rejects_bad_input(monkeypatch, request_body):
    session = _Session()
    service = _service(monkeypatch, session)
    assert _upsert(service, request_body)["status_code"] == 400
    assert not session.committed


def test_upsert_saves_and_invalidates(monkeypatch):
    session = _Session()
    service = _service(monkeypatch, session)

    result = _upsert(
        service,
        {"source": "troubleshoot", "action": "deliver_channels", "mappings": CHANNELS["mappings"]},
    )

    assert result == {"source": "troubleshoot", "action": "deliver_channels", "mappings": CHANNELS["mappings"]}
    row = session.saved[(TENANT, "troubleshoot")]
    assert (row.action, row.mappings) == ("deliver_channels", CHANNELS["mappings"])
    assert "ON CONFLICT (tenant_id, source) DO UPDATE" in session.upsert_sql
    assert session.committed and service.cache.invalidated == 2


def test_a_non_channel_default_keeps_no_mappings(monkeypatch):
    session = _Session()
    service = _service(monkeypatch, session)
    _upsert(service, {"source": "troubleshoot", "action": "drop", "mappings": CHANNELS["mappings"]})
    assert session.saved[(TENANT, "troubleshoot")].mappings == []
