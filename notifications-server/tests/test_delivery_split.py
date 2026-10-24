"""
A message covering several accounts is matched once per account, and each
channel gets one message with only the accounts routed to it and those accounts'
totals. Grouped SLO and anomaly alerts are matched one alert at a time when the
group is flushed; an anomaly is matched as the troubleshooting finding it is.
"""

import asyncio
import fnmatch
import json
import time
from collections import defaultdict
from datetime import timedelta
from types import SimpleNamespace

import redis

from notifications_server.services import message as message_module
from notifications_server.services.delivery_split import account_sections, has_recommendations, merged_parameters
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.services.message import (
    GroupedMessageHandler,
    MessageService,
    NotificationRuleMatcher,
    grouped_unit,
)

TENANT = "tenant-1"
PROD, DEV, STAGE = "acc-prod", "acc-dev", "acc-stage"
OPS = {"id": "C-OPS"}
FINOPS = {"id": "C-FINOPS"}
DEFAULT = {"id": "C-DEFAULT"}


def _deliver(channels):
    return {"rule_id": "r", "suppressed": False, "platform": "slack", "channels": channels, "expires_at": None}


def _stop():
    return {"rule_id": "stop", "suppressed": True, "platform": None, "channels": None, "expires_at": None}


class _Matcher:
    """Answers with the entries set for the message's account, reading the source
    and account the way the real matcher does."""

    def __init__(self, by_account):
        self.by_account = by_account
        self.seen = []
        self.rules_service = SimpleNamespace(get_default_policy=self._no_policy)

    @staticmethod
    async def _no_policy(_tenant_id, _source):
        return None

    async def match_notification_rules(self, _session, _tenant_id, type_, payload):
        source, account_id, *_ = NotificationRuleMatcher.get_filters_from_notification_payload(type_, payload)
        self.seen.append((type_, source, account_id))
        return list(self.by_account.get(account_id, [])), source


class _Session:
    async def __aenter__(self):
        return self

    async def __aexit__(self, *_exc):
        return False


def _service(by_account, monkeypatch):
    service = MessageService.__new__(MessageService)
    service.engine = None
    service.rule_matcher = _Matcher(by_account)
    service.installs = [SimpleNamespace(id="slack-1", platform="slack", channels=DEFAULT, to_channel=None)]
    service.sent = []

    async def installs(_session, _tenant_id):
        return service.installs

    async def no_gchat(*_args):
        return None

    async def send(_session, _tenant, kwargs, notification_type, platforms):
        service.sent.append((notification_type, platforms[0].to_channel, kwargs["parameters"]))
        return [{"status": "success"}]

    service.get_installed_platforms_from_cache_or_db = installs
    service._apply_gchat_default_fallback = no_gchat
    service.send_template_notification = send
    monkeypatch.setattr(message_module.BaseDB, "async_session", lambda _engine: lambda: _Session())
    return service


def _section(name, savings, critical, rec_ids):
    return {
        "account_name": name,
        "recommendations": [{"id": rec_id} for rec_id in rec_ids],
        "totals": {
            "total_recoverable_savings": savings,
            "critical_count": critical,
            "new_counts": {"act_now": 1, "critical": critical, "high": 0},
            "category_savings": {"RightSizing": savings},
        },
    }


def _digest():
    return {
        "type": "recommendation_nudge_digest",
        "source": "optimize",
        "parameters": {
            "organization_name": "Acme",
            "total_recoverable_savings": 999,
            "critical_count": 99,
            "recommendations_by_account": {"tenant-wide": {}},
            "accounts": {
                PROD: _section("k8s-prod", 100.0, 2, ["p1", "p2"]),
                DEV: _section("k8s-dev", 10.5, 1, ["d1"]),
                STAGE: _section("k8s-stage", 1.0, 0, ["s1"]),
            },
        },
    }


# ----------------------------- merging accounts -----------------------------


def test_account_sections_only_for_rollups_that_carry_them():
    assert account_sections("recommendation_nudge_digest", _digest()["parameters"])
    assert account_sections("weekly_digest_events", _digest()["parameters"]) is None
    assert account_sections("recommendation_proactive_nudge", {"recommendations_by_account": {}}) is None
    assert account_sections("security_posture_alert", {"accounts": {}}) is None


def test_merged_parameters_keeps_only_the_channel_accounts_and_sums_them():
    merged = merged_parameters(_digest()["parameters"], [PROD, DEV])

    assert "accounts" not in merged
    assert merged["organization_name"] == "Acme"
    assert set(merged["recommendations_by_account"]) == {PROD, DEV}
    assert merged["recommendations_by_account"][DEV] == {"account_name": "k8s-dev", "recommendations": [{"id": "d1"}]}
    assert merged["total_recoverable_savings"] == 110.5
    assert merged["critical_count"] == 3
    assert merged["new_counts"] == {"act_now": 2, "critical": 3, "high": 0}
    assert merged["category_savings"] == {"RightSizing": 110.5}


def test_account_with_nothing_to_list_counts_in_the_header_but_is_not_listed():
    parameters = _digest()["parameters"]
    parameters["accounts"][STAGE] = _section("k8s-stage", 0.0, 4, [])

    merged = merged_parameters(parameters, [PROD, STAGE])

    assert set(merged["recommendations_by_account"]) == {PROD}
    assert merged["critical_count"] == 6
    assert has_recommendations(parameters, [PROD, STAGE])
    assert not has_recommendations(parameters, [STAGE])


# ----------------------------- roll-up messages -----------------------------


def test_rollup_sends_each_channel_only_its_accounts(monkeypatch):
    service = _service({PROD: [_deliver(OPS)], DEV: [_deliver(FINOPS)]}, monkeypatch)

    responses = asyncio.run(service.send_message(_request(_digest())))

    assert responses == [{"status": "success"}] * 3
    sent = {json.dumps(channel): params for _type, channel, params in service.sent}
    assert set(sent[json.dumps(OPS)]["recommendations_by_account"]) == {PROD}
    assert sent[json.dumps(OPS)]["total_recoverable_savings"] == 100.0
    assert set(sent[json.dumps(FINOPS)]["recommendations_by_account"]) == {DEV}
    # No rule covers stage: it follows the default, to the installation's own channel.
    assert set(sent[json.dumps(DEFAULT)]["recommendations_by_account"]) == {STAGE}
    assert {(t, s, a) for t, s, a in service.rule_matcher.seen} == {
        ("recommendation_nudge_digest", "optimize", account) for account in (PROD, DEV, STAGE)
    }


def test_rollup_accounts_sharing_a_channel_go_in_one_message(monkeypatch):
    service = _service({PROD: [_deliver(OPS)], DEV: [_deliver(OPS)], STAGE: [_stop()]}, monkeypatch)

    asyncio.run(service.send_message(_request(_digest())))

    assert len(service.sent) == 1
    _type, channel, params = service.sent[0]
    assert channel == OPS
    assert set(params["recommendations_by_account"]) == {PROD, DEV}
    assert params["critical_count"] == 3


def test_rollup_with_every_account_stopped_sends_nothing(monkeypatch):
    service = _service({PROD: [_stop()], DEV: [_stop()], STAGE: [_stop()]}, monkeypatch)

    responses = asyncio.run(service.send_message(_request(_digest())))

    assert service.sent == []
    assert responses[0]["status"] == "suppressed"


def test_rollup_channel_with_nothing_to_list_is_skipped(monkeypatch):
    digest = _digest()
    digest["parameters"]["accounts"][STAGE]["recommendations"] = []
    service = _service({PROD: [_deliver(OPS)], DEV: [_deliver(OPS)]}, monkeypatch)

    asyncio.run(service.send_message(_request(digest)))

    assert [channel for _type, channel, _params in service.sent] == [OPS]


def test_rollup_without_account_sections_is_sent_whole(monkeypatch):
    digest = _digest()
    del digest["parameters"]["accounts"]
    service = _service({}, monkeypatch)

    async def trigger(_session, _fingerprint, platforms, _kwargs, parameters, _responses, _tenant, type_, **_kw):
        service.sent.append((type_, platforms[0].to_channel, parameters))
        return [{"status": "success"}]

    service.trigger_notification = trigger
    asyncio.run(service.send_message(_request(digest)))

    assert len(service.sent) == 1
    assert service.sent[0][2]["recommendations_by_account"] == {"tenant-wide": {}}
    assert service.rule_matcher.seen == [("recommendation_nudge_digest", "optimize", None)]


# ----------------------------- retry scoping -----------------------------


def test_retry_platforms_restricts_fan_out_to_the_failed_platform(monkeypatch):
    """A retry must not resend to a platform that already succeeded last
    attempt (#41040 regression): request.retry_platforms, set by
    NotificationProcessor on a partial failure, has to narrow installed_platforms
    before trigger_notification fans the message out."""
    service = _service({}, monkeypatch)
    service.installs = [
        SimpleNamespace(id="slack-1", platform="slack", channels=DEFAULT, to_channel=None),
        SimpleNamespace(id="teams-1", platform="ms_teams", channels=DEFAULT, to_channel=None),
    ]
    sent_platforms = []

    async def trigger(_session, _fingerprint, platforms, _kwargs, _parameters, _responses, _tenant, _type, **_kw):
        sent_platforms.extend(ip.platform for ip in platforms)
        return [{"status": "success"}]

    service.trigger_notification = trigger

    request = SimpleNamespace(
        tenant_id=TENANT,
        type="generic",
        source="prometheus",
        thread=None,
        retry_platforms=["ms_teams"],
        dict=lambda **_kwargs: {"type": "generic", "source": "prometheus", "parameters": {"message": "hi"}},
    )

    asyncio.run(service.send_message(request))

    assert sent_platforms == ["ms_teams"]


def _failed_at(channel):
    return {"platform": "slack", "destination_id": "slack-1", "channel_id": channel["id"]}


def test_retry_destinations_resend_only_to_the_channels_that_failed(monkeypatch):
    service = _service({None: [_deliver(OPS), _deliver(FINOPS)]}, monkeypatch)
    sent_channels = []

    async def trigger(_session, _fingerprint, platforms, _kwargs, _parameters, _responses, _tenant, _type, **_kw):
        sent_channels.extend(ip.to_channel for ip in platforms if ip.to_channel)
        return [{"status": "success"}]

    service.trigger_notification = trigger
    request = _request({"type": "generic", "source": "prometheus", "parameters": {"message": "hi"}})
    request.retry_destinations = [_failed_at(FINOPS)]

    asyncio.run(service.send_message(request))

    assert sent_channels == [FINOPS]


def test_a_rollup_retry_resends_only_the_channels_that_failed(monkeypatch):
    service = _service({PROD: [_deliver(OPS)], DEV: [_deliver(FINOPS)]}, monkeypatch)
    request = _request(_digest())
    request.retry_destinations = [_failed_at(FINOPS)]

    asyncio.run(service.send_message(request))

    assert [(channel, set(params["recommendations_by_account"])) for _type, channel, params in service.sent] == [
        (FINOPS, {DEV})
    ]


def test_a_tenant_with_nowhere_to_send_gets_not_sent(monkeypatch):
    service = _service({}, monkeypatch)
    service.installs = []

    for payload in ({"type": "generic", "source": "prometheus", "parameters": {"message": "hi"}}, _digest()):
        responses = asyncio.run(service.send_message(_request(payload)))

        assert responses == [{"platform": "system", "status": "not_sent", "reason": "no_destination"}]


def test_an_install_a_retry_leaves_nothing_to_resend_is_not_tried(monkeypatch):
    service = _service({None: [_deliver(OPS)]}, monkeypatch)
    teams_channel = {"id": "T1"}
    service.installs = [
        SimpleNamespace(id="slack-1", platform="slack", channels=DEFAULT, to_channel=None),
        SimpleNamespace(id="teams-1", platform="ms_teams", channels=teams_channel, to_channel=None),
    ]
    tried = []

    async def send(_session, _tenant, ip, *_args):
        tried.append(ip.platform)
        return {"platform": ip.platform, "status": "success"}, None

    service._get_finding_sender = lambda _platform: send
    request = _request({"type": "finding", "source": "prometheus", "parameters": {"finding": {"priority": "HIGH"}}})
    request.retry_destinations = [{"platform": "ms_teams", "destination_id": "teams-1", "channel_id": "T1"}]

    responses = asyncio.run(service.send_message(request))

    assert tried == ["ms_teams"]
    assert responses == [{"platform": "ms_teams", "status": "success"}]


def test_installs_with_nowhere_to_post_get_not_sent(monkeypatch):
    service = _service({}, monkeypatch)
    service.installs = [SimpleNamespace(id="slack-1", platform="slack", channels=None, to_channel=None)]

    responses = asyncio.run(
        service.send_message(_request({"type": "generic", "source": "prometheus", "parameters": {"message": "hi"}}))
    )

    assert responses == [{"platform": "system", "status": "not_sent", "reason": "no_destination"}]


def test_a_message_type_with_no_template_is_a_failure_not_an_empty_result(monkeypatch):
    service = _service({}, monkeypatch)

    responses = asyncio.run(
        service.send_message(_request({"type": "no_such_type", "source": "prometheus", "parameters": {}}))
    )

    assert responses == [{"platform": "system", "status": "failed", "reason": "invalid_request", "retryable": False}]


def test_an_older_retry_scope_is_narrowed_by_the_newer_retry_platforms(monkeypatch):
    """A pod that predates retry_destinations retries the scope it was given
    and rewrites only retry_platforms; the next attempt honours both."""
    service = _service({None: [_deliver(OPS)]}, monkeypatch)
    teams_channel = {"id": "T1"}
    service.installs = [
        SimpleNamespace(id="slack-1", platform="slack", channels=DEFAULT, to_channel=None),
        SimpleNamespace(id="teams-1", platform="ms_teams", channels=teams_channel, to_channel=None),
    ]
    sent = []

    async def trigger(_session, _fingerprint, platforms, *_args, **_kw):
        sent.extend((ip.platform, ip.to_channel) for ip in platforms if ip.to_channel)
        return [{"status": "success"}]

    service.trigger_notification = trigger
    request = _request({"type": "generic", "source": "prometheus", "parameters": {"message": "hi"}})
    request.retry_destinations = [
        _failed_at(OPS),
        {"platform": "ms_teams", "destination_id": "teams-1", "channel_id": "T1"},
    ]
    request.retry_platforms = ["ms_teams"]

    asyncio.run(service.send_message(request))

    assert sent == [("ms_teams", teams_channel)]


# ----------------------------- grouped alerts -----------------------------


class _Redis:
    """Fake Redis for the grouped-flush claim/retry/sweep path: a dict of
    lists plus a dict of strings (for the `:due`/`:claimed_at` markers), with
    just enough of the real commands (rename raises like redis-py does on a
    missing source key)."""

    def __init__(self, items):
        self.lists = {"grouped:%s:anomaly" % TENANT: [json.dumps(item) for item in items]}
        self.strings = {}
        self.deleted = []

    def exists(self, key):
        return key in self.lists or key in self.strings

    def rename(self, src, dst):
        if src not in self.lists:
            raise redis.exceptions.ResponseError("no such key")
        self.lists[dst] = self.lists.pop(src)

    def lrange(self, key, _start, _end):
        return self.lists.get(key, [])

    def rpush(self, key, *values):
        self.lists.setdefault(key, []).extend(values)

    def llen(self, key):
        return len(self.lists.get(key, []))

    def expire(self, _key, _seconds):
        return True

    def set(self, key, value, nx=False, ex=None):
        if nx and key in self.strings:
            return False
        self.strings[key] = value
        return True

    def get(self, key):
        return self.strings.get(key)

    def delete(self, key):
        self.deleted.append(key)
        self.lists.pop(key, None)
        self.strings.pop(key, None)

    def scan(self, cursor, match=None, count=None):
        pattern = match or "*"
        keys = [k for k in {**self.lists, **self.strings} if fnmatch.fnmatch(k, pattern)]
        return 0, keys

    def pipeline(self):
        return _Pipeline(self)


class _Pipeline:
    """Queues calls against the same fake and runs them in order on execute() —
    enough to exercise the real pipeline call shape, not real atomicity."""

    def __init__(self, redis_fake):
        self._redis = redis_fake
        self._calls = []

    def __enter__(self):
        return self

    def __exit__(self, *_exc):
        return False

    def __getattr__(self, name):
        def queue(*args, **kwargs):
            self._calls.append((name, args, kwargs))
            return self

        return queue

    def execute(self):
        results = [getattr(self._redis, name)(*args, **kwargs) for name, args, kwargs in self._calls]
        self._calls = []
        return results


def _anomaly(account, title):
    return {
        "source": "anomaly",
        "type": "anomaly",
        "tenant_id": TENANT,
        "parameters": {"source": "anomaly", "cloud_account_id": account, "title": title},
    }


ANOMALY_KEY = "grouped:%s:anomaly" % TENANT
CLAIM_KEY = f"{ANOMALY_KEY}:claim"


def _flush(service, items):
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis(items)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)
    result = asyncio.run(handler._flush_group(ANOMALY_KEY))
    return result, redis_fake


def test_grouped_anomalies_are_matched_as_troubleshooting_findings_one_by_one(monkeypatch):
    service = _service({PROD: [_deliver(OPS)], DEV: [_stop()]}, monkeypatch)

    result, redis_fake = _flush(
        service, [_anomaly(PROD, "cpu"), _anomaly(DEV, "mem"), _anomaly(STAGE, "disk"), _anomaly(PROD, "io")]
    )

    assert result == {"status": "ok"}
    assert redis_fake.deleted == [CLAIM_KEY, f"{CLAIM_KEY}:claimed_at", f"{ANOMALY_KEY}:due"]
    assert {entry[1] for entry in service.rule_matcher.seen} == {"troubleshoot"}
    assert [entry[0] for entry in service.rule_matcher.seen] == ["finding"] * 4
    sent = {json.dumps(channel): params for _type, channel, params in service.sent}
    assert [event["title"] for event in sent[json.dumps(OPS)]["events"]] == ["cpu", "io"]
    assert [event["title"] for event in sent[json.dumps(DEFAULT)]["events"]] == ["disk"]
    assert all(notification_type == "grouped_anomaly" for notification_type, *_ in service.sent)


def test_grouped_alerts_all_stopped_clear_the_group(monkeypatch):
    service = _service({PROD: [_stop()]}, monkeypatch)

    result, redis_fake = _flush(service, [_anomaly(PROD, "cpu")])

    assert result == {"status": "ok"}
    assert service.sent == []
    assert redis_fake.deleted == [CLAIM_KEY, f"{CLAIM_KEY}:claimed_at", f"{ANOMALY_KEY}:due"]


def test_grouped_alerts_during_a_timed_dont_send_are_dropped_not_held(monkeypatch):
    until = utc_now() + timedelta(hours=4)
    snooze = {**_stop(), "expires_at": until}
    service = _service({PROD: [snooze]}, monkeypatch)

    result, redis_fake = _flush(service, [_anomaly(PROD, "cpu")])

    assert result == {"status": "ok"}
    assert service.sent == []
    assert redis_fake.deleted == [CLAIM_KEY, f"{CLAIM_KEY}:claimed_at", f"{ANOMALY_KEY}:due"]


# ----------------------------- grouped alerts: claim race, partial failure, sweep -----------------------------


def test_a_push_that_arrives_mid_flush_is_not_lost(monkeypatch):
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    original_rename = redis_fake.rename

    def rename_then_concurrent_push(src, dst):
        original_rename(src, dst)
        redis_fake.lists[src] = [json.dumps(_anomaly(PROD, "late"))]

    redis_fake.rename = rename_then_concurrent_push
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    result = asyncio.run(handler._flush_group(ANOMALY_KEY))

    assert result == {"status": "ok"}
    assert [event["title"] for event in service.sent[0][2]["events"]] == ["cpu"]
    assert redis_fake.lists[ANOMALY_KEY] == [json.dumps(_anomaly(PROD, "late"))]


def test_a_push_that_arrives_mid_flush_gets_its_own_due_time(monkeypatch):
    """The in-progress flush's due_key is still in the past when the new
    push's enqueue() runs (its SET NX no-ops against it), so a fully
    successful flush must re-arm due_key for the leftover item instead of
    deleting it — otherwise nothing is left watching to flush it."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    original_rename = redis_fake.rename

    def rename_then_concurrent_push(src, dst):
        original_rename(src, dst)
        redis_fake.lists[src] = [json.dumps(_anomaly(PROD, "late"))]

    redis_fake.rename = rename_then_concurrent_push
    redis_fake.strings[f"{ANOMALY_KEY}:due"] = str(int(time.time()) - 5)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    result = asyncio.run(handler._flush_group(ANOMALY_KEY))

    assert result == {"status": "ok"}
    assert ANOMALY_KEY in redis_fake.lists
    assert int(redis_fake.strings[f"{ANOMALY_KEY}:due"]) > int(time.time())


def test_a_push_that_arrives_while_everything_else_is_stopped_gets_its_own_due_time(monkeypatch):
    """Same stranding risk as the full-send case, but through the "every
    alert in the batch was stopped by rules" exit — the common case, not a
    rare one, since snoozes and stop rules hit it routinely."""
    service = _service({PROD: [_stop()]}, monkeypatch)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    original_rename = redis_fake.rename

    def rename_then_concurrent_push(src, dst):
        original_rename(src, dst)
        redis_fake.lists[src] = [json.dumps(_anomaly(PROD, "late"))]

    redis_fake.rename = rename_then_concurrent_push
    redis_fake.strings[f"{ANOMALY_KEY}:due"] = str(int(time.time()) - 5)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    result = asyncio.run(handler._flush_group(ANOMALY_KEY))

    assert result == {"status": "ok"}
    assert service.sent == []
    assert ANOMALY_KEY in redis_fake.lists
    assert int(redis_fake.strings[f"{ANOMALY_KEY}:due"]) > int(time.time())


def test_partial_failure_retries_only_the_platform_that_failed(monkeypatch):
    service = _service({}, monkeypatch)
    service.installs = [
        SimpleNamespace(id="slack-1", platform="slack", channels=DEFAULT, to_channel=None),
        SimpleNamespace(id="teams-1", platform="ms_teams", channels=DEFAULT, to_channel=None),
    ]
    fail_platforms = {"ms_teams"}
    attempted = []

    async def send(_session, _tenant, kwargs, notification_type, platforms):
        ip = platforms[0]
        attempted.append(ip.platform)
        if ip.platform in fail_platforms:
            return [{"status": "failed", "platform": ip.platform}]
        service.sent.append((notification_type, ip.to_channel, kwargs["parameters"]))
        return [{"status": "success", "platform": ip.platform}]

    service.send_template_notification = send
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    first = asyncio.run(handler._flush_group(ANOMALY_KEY))

    assert first["status"] == "partial_failure"
    assert sorted(attempted) == ["ms_teams", "slack"]
    assert [event["title"] for event in service.sent[0][2]["events"]] == ["cpu"]  # delivered to slack
    requeued = json.loads(redis_fake.lists[ANOMALY_KEY][0])
    assert requeued["retry_platforms"] == ["ms_teams"] and requeued["attempts"] == 1

    attempted.clear()
    service.sent.clear()
    fail_platforms.clear()  # Teams recovers

    second = asyncio.run(handler._flush_group(ANOMALY_KEY))

    assert second == {"status": "ok"}
    assert attempted == ["ms_teams"]  # Slack, already delivered, is not resent to
    assert ANOMALY_KEY not in redis_fake.lists


def test_exhausted_retries_drop_the_item_instead_of_holding_it_forever(monkeypatch):
    monkeypatch.setattr(message_module.settings.notifications, "task_retry_count", 1)
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)

    async def always_fails(*_args, **_kwargs):
        return [{"status": "failed", "platform": "slack"}]

    service.send_template_notification = always_fails
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    first = asyncio.run(handler._flush_group(ANOMALY_KEY))
    assert first["status"] == "partial_failure"
    assert json.loads(redis_fake.lists[ANOMALY_KEY][0])["attempts"] == 1

    second = asyncio.run(handler._flush_group(ANOMALY_KEY))

    assert second == {"status": "ok"}
    assert ANOMALY_KEY not in redis_fake.lists


def test_sweep_flushes_a_due_key_with_no_in_memory_timer(monkeypatch):
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    redis_fake.strings[f"{ANOMALY_KEY}:due"] = str(int(time.time()) - 5)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    asyncio.run(handler.sweep())

    assert service.sent
    assert f"{ANOMALY_KEY}:due" not in redis_fake.strings


def test_sweep_resumes_a_claim_left_over_from_a_crashed_flush(monkeypatch):
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([])
    redis_fake.lists[CLAIM_KEY] = [json.dumps(_anomaly(PROD, "cpu"))]
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    asyncio.run(handler.sweep())

    assert service.sent
    assert CLAIM_KEY not in redis_fake.lists


def test_sweep_does_not_resume_a_claim_still_being_worked_on(monkeypatch):
    """A fresh lease means a flush is still in progress elsewhere, not crashed."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([])
    redis_fake.lists[CLAIM_KEY] = [json.dumps(_anomaly(PROD, "cpu"))]
    redis_fake.strings[f"{CLAIM_KEY}:claimed_at"] = str(int(time.time()) - 5)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    asyncio.run(handler.sweep())

    assert service.sent == []
    assert CLAIM_KEY in redis_fake.lists


def test_sweep_resumes_a_claim_once_its_lease_goes_stale(monkeypatch):
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([])
    redis_fake.lists[CLAIM_KEY] = [json.dumps(_anomaly(PROD, "cpu"))]
    stale_after = message_module.settings.notifications.grouped_claim_stale_seconds
    redis_fake.strings[f"{CLAIM_KEY}:claimed_at"] = str(int(time.time()) - stale_after - 5)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    asyncio.run(handler.sweep())

    assert service.sent
    assert CLAIM_KEY not in redis_fake.lists


def test_sweep_due_scan_defers_to_the_claim_scan_for_an_already_claimed_key(monkeypatch):
    """A due key that's already claimed (even freshly, not stale) must not be
    flushed again from the due-key scan — that's the claim scan's call, gated
    on staleness. Otherwise the due scan would resend an in-progress batch."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([])
    redis_fake.lists[CLAIM_KEY] = [json.dumps(_anomaly(PROD, "cpu"))]
    redis_fake.strings[f"{CLAIM_KEY}:claimed_at"] = str(int(time.time()) - 5)
    redis_fake.strings[f"{ANOMALY_KEY}:due"] = str(int(time.time()) - 5)
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    asyncio.run(handler.sweep())

    assert service.sent == []
    assert CLAIM_KEY in redis_fake.lists


def test_claim_batch_second_caller_gets_none_while_lease_is_fresh(monkeypatch):
    """Two pods' local timers (or a timer and a sweep) can both reach
    _claim_batch for the same key — the lease (SET NX), not the RENAME, is
    what must stop the second one from resending the batch the first already
    claimed."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([_anomaly(PROD, "cpu")])
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    first = asyncio.run(handler._claim_batch(ANOMALY_KEY, CLAIM_KEY))
    second = asyncio.run(handler._claim_batch(ANOMALY_KEY, CLAIM_KEY))

    assert first == [json.dumps(_anomaly(PROD, "cpu"))]
    assert second is None


def test_claim_batch_releases_the_lease_when_nothing_is_queued(monkeypatch):
    """A lease acquired for a claim that turns out empty must not linger —
    otherwise it would block a later, real claim on the same key for up to
    grouped_claim_stale_seconds."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([])
    redis_fake.lists = {}
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)

    result = asyncio.run(handler._claim_batch(ANOMALY_KEY, CLAIM_KEY))

    assert result == []
    assert f"{CLAIM_KEY}:claimed_at" not in redis_fake.strings


def test_losing_the_stale_claim_rename_race_does_not_delete_the_winners_lease(monkeypatch):
    """Two pods both see a stale lease and both take it over; only one wins
    the RENAME. The loser's ResponseError must not delete the lease the
    winner now holds — that would let a third caller resend the same batch."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    fake_redis = _Redis([_anomaly(PROD, "cpu")])
    stale_after = message_module.settings.notifications.grouped_claim_stale_seconds
    fake_redis.strings[f"{CLAIM_KEY}:claimed_at"] = str(int(time.time()) - stale_after - 5)

    def rename_but_another_pod_already_won(src, dst):
        fake_redis.lists.pop(src, None)
        fake_redis.lists[dst] = [json.dumps(_anomaly(PROD, "cpu"))]
        raise redis.exceptions.ResponseError("no such key")

    fake_redis.rename = rename_but_another_pod_already_won
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=fake_redis), service)

    result = asyncio.run(handler._claim_batch(ANOMALY_KEY, CLAIM_KEY))

    assert result is None
    assert f"{CLAIM_KEY}:claimed_at" in fake_redis.strings


def test_grouped_slo_items_are_matched_as_queued():
    item = {"source": "slo", "type": "slo_breach", "parameters": {"account_id": PROD}}
    assert grouped_unit(3, "slo_breach", item) == (3, "slo_breach", item)


def _request(payload):
    return SimpleNamespace(
        tenant_id=TENANT,
        type=payload["type"],
        source=payload["source"],
        thread=None,
        dict=lambda **_kwargs: json.loads(json.dumps(payload)),
    )


# ----------------------------- hourly batch rules (#41422) -----------------------------


def _batch_deliver(channels):
    return {**_deliver(channels), "rule_id": "batch-r", "delivery_mode": "batch"}


def _batch_item(account, title, source="prometheus"):
    return {
        "source": source,
        "type": "batch",
        "tenant_id": TENANT,
        "parameters": {"account_id": account, "title": title},
    }


BATCH_KEY = "grouped:%s:batch" % TENANT


def _flush_batch(service, items):
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis_fake = _Redis([])
    redis_fake.lists = {BATCH_KEY: [json.dumps(item) for item in items]}
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis_fake), service)
    result = asyncio.run(handler._flush_group(BATCH_KEY))
    return result, redis_fake


def test_grouped_unit_batch_preserves_the_real_source():
    item = _batch_item(PROD, "cpu high", source="prometheus")
    assert grouped_unit(2, "batch", item) == (
        2,
        "finding",
        {"source": "prometheus", "parameters": {"finding": {"account_id": PROD, "title": "cpu high"}}},
    )


def test_route_units_holds_a_batch_rule_back_by_default(monkeypatch):
    """send_rollup and the SLO/anomaly flush call route_units with no
    honour_batch argument (default True) — a batch-mode rule must still be
    held back there, since those two code paths never shared a batch-mode
    rule before #41422."""
    service = _service({PROD: [_batch_deliver(OPS)]}, monkeypatch)
    unit = (0, "finding", {"source": "prometheus", "parameters": {"finding": {"account_id": PROD}}})

    async def run():
        async with message_module.BaseDB.async_session(service.engine)() as session:
            return await service.route_units(session, TENANT, [unit], service.installs)

    assert asyncio.run(run()) == []


def test_route_units_honour_batch_false_routes_the_batch_rules_channel(monkeypatch):
    service = _service({PROD: [_batch_deliver(OPS)]}, monkeypatch)
    unit = (0, "finding", {"source": "prometheus", "parameters": {"finding": {"account_id": PROD}}})

    async def run():
        async with message_module.BaseDB.async_session(service.engine)() as session:
            return await service.route_units(session, TENANT, [unit], service.installs, honour_batch=False)

    routes = asyncio.run(run())

    assert len(routes) == 1
    _ip, channel, keys = routes[0]
    assert channel == OPS and keys == [0]


def test_handle_stopped_outcome_enqueues_a_queued_finding_not_other_stops(monkeypatch):
    service = _service({}, monkeypatch)
    queued = []

    async def enqueue(payload):
        queued.append(payload)
        return {"status": "queued"}

    service.group_handler = SimpleNamespace(enqueue=enqueue)
    finding = {"account_id": PROD, "title": "cpu high"}

    queued_result = asyncio.run(
        service._handle_stopped_outcome("finding", "prometheus", TENANT, {"finding": finding}, [{"status": "queued"}])
    )
    suppressed_result = asyncio.run(
        service._handle_stopped_outcome(
            "finding", "prometheus", TENANT, {"finding": finding}, [{"status": "suppressed"}]
        )
    )

    assert queued_result == {"status": "queued"}
    assert suppressed_result == [{"status": "suppressed"}]
    assert queued == [{"source": "prometheus", "type": "batch", "tenant_id": TENANT, "parameters": finding}]


def test_a_batch_rule_finding_is_queued_through_send_message(monkeypatch):
    service = _service({PROD: [_batch_deliver(OPS)]}, monkeypatch)
    queued = []

    async def enqueue(payload):
        queued.append(payload)
        return {"status": "queued"}

    service.group_handler = SimpleNamespace(enqueue=enqueue)
    request = _request(
        {
            "type": "finding",
            "source": "prometheus",
            "parameters": {"finding": {"account_id": PROD, "title": "cpu high"}},
        }
    )

    responses = asyncio.run(service.send_message(request))

    assert responses == {"status": "queued"}
    assert queued == [
        {
            # the resolved rule-source category, not the raw "prometheus" —
            # it must re-match the same rule category when flushed.
            "source": "troubleshoot",
            "type": "batch",
            "tenant_id": TENANT,
            "parameters": {"account_id": PROD, "title": "cpu high"},
        }
    ]


def test_a_batch_rule_finding_flushes_to_its_own_rules_channel(monkeypatch):
    """Full round trip: a finding held for batch delivery, once flushed, must
    reach the channel its own rule names — not be held back again (that
    would silently drop every batch item forever, the bug route_units'
    honour_batch=False at flush time exists to close)."""
    service = _service({PROD: [_batch_deliver(OPS)]}, monkeypatch)

    result, _redis_fake = _flush_batch(service, [_batch_item(PROD, "cpu high")])

    assert result == {"status": "ok"}
    assert service.sent and service.sent[0][1] == OPS
    assert [event["title"] for event in service.sent[0][2]["events"]] == ["cpu high"]
