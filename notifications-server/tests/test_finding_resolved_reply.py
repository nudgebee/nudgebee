"""Resolved findings post a green closure reply threaded under the original
alert message, and only there. Covers the Slack template shape and the
thread-only delivery: it fans out to every recorded thread for the fingerprint,
skips threads already stamped resolved (dedup for repeated resolve webhooks),
and drops silently when the firing message was never delivered."""

import asyncio
import json
from types import SimpleNamespace

from notifications_server.message_templates.slack.finding import get_slack_resolved_finding_reply
from notifications_server.models.models import SentNotifications
from notifications_server.services import message as message_mod
from notifications_server.services.message import MessageService

_TENANT = "tenant-1"
_ACCOUNT = "acc-1"
_FINGERPRINT = "fp-77"
_CHANNEL = "C0SLACK"
_THREAD_TS = "1700000000.000100"


class TestResolvedTemplate:
    def _finding(self, **overrides):
        finding = {
            "id": "find-77",
            "title": "checkout-api is crash-looping",
            "cloud_account_id": _ACCOUNT,
            "fingerprint": _FINGERPRINT,
            "starts_at": "2026-07-15T14:00:00+00:00",
            "ends_at": "2026-07-15T14:42:00+00:00",
        }
        finding.update(overrides)
        return finding

    def test_green_stripe_linked_title_and_duration(self):
        text, blocks, (att,) = get_slack_resolved_finding_reply(self._finding())
        assert (text, blocks) == ("", [])
        assert att["color"] == "#2C8C55"
        assert att["title"] == "Resolved: checkout-api is crash-looping"
        assert "id=find-77" in att["title_link"]
        assert att["text"].startswith("Cleared after 42m")
        # attachment-only, no byline-triggering keys
        assert "blocks" not in att and "footer" not in att and "ts" not in att

    def test_missing_start_falls_back_to_cleared_at(self):
        _, _, (att,) = get_slack_resolved_finding_reply(self._finding(starts_at=None))
        assert att["text"].startswith("Cleared at ")

    def test_multi_day_duration_is_compact(self):
        finding = self._finding(starts_at="2026-07-13T14:00:00+00:00", ends_at="2026-07-15T17:00:00+00:00")
        _, _, (att,) = get_slack_resolved_finding_reply(finding)
        assert "Cleared after 2d 3h" in att["text"]


class _Result:
    def __init__(self, rows):
        self._rows = list(rows)

    def scalars(self):
        return self

    def all(self):
        return list(self._rows)


class _Session:
    def __init__(self, rows, commits):
        self._rows = rows
        self._commits = commits

    async def execute(self, stmt):
        return _Result(self._rows)

    def add(self, row):
        pass

    async def commit(self):
        self._commits.append(True)

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


def _sent_row(metadata):
    row = SentNotifications()
    row.tenant_id = _TENANT
    row.account_id = _ACCOUNT
    row.fingerprint = _FINGERPRINT
    row.slack_team_id = "T123"
    row.slack_thread_id = _THREAD_TS
    row.slack_metadata = json.dumps(metadata)
    return row


def _finding_params(**overrides):
    finding = {
        "id": "find-77",
        "title": "checkout-api is crash-looping",
        "cloud_account_id": _ACCOUNT,
        "fingerprint": _FINGERPRINT,
        "starts_at": "2026-07-15T14:00:00+00:00",
        "ends_at": "2026-07-15T14:42:00+00:00",
    }
    finding.update(overrides)
    return {"finding": finding}


def _run_resolved(monkeypatch, rows, reply_fn=None):
    commits = []
    session = _Session(rows, commits)
    monkeypatch.setattr(message_mod.BaseDB, "async_session", staticmethod(lambda _engine: lambda: session))

    async def _fake_installs(_self, _session, tenant_id=None, platform=None):
        return [SimpleNamespace(team_id="T123", token="xoxb-token", platform="slack")]

    monkeypatch.setattr(MessageService, "get_installed_platforms", _fake_installs)

    captured = []

    def _default_reply(**kw):
        return SimpleNamespace(status_code=200, data={"ok": True, "ts": "1700000000.000200", "channel": _CHANNEL})

    def _reply_in_thread(**kw):
        captured.append(kw)
        return (reply_fn or _default_reply)(**kw)

    slack_app = SimpleNamespace(client=SimpleNamespace(reply_in_thread=_reply_in_thread))
    fake = SimpleNamespace(engine=object(), slack_app=slack_app, get_installed_platforms=None)
    fake.get_installed_platforms = MessageService.get_installed_platforms.__get__(fake)
    fake.send_finding_resolved_reply = MessageService.send_finding_resolved_reply.__get__(fake)

    result = asyncio.run(fake.send_finding_resolved_reply(_TENANT, _finding_params()))
    return result, captured, commits


class TestResolvedDelivery:
    def test_posts_thread_reply_and_stamps_dedup(self, monkeypatch):
        rows = [_sent_row({"channel": _CHANNEL})]
        result, captured, commits = _run_resolved(monkeypatch, rows)

        assert len(captured) == 1
        assert captured[0]["thread_ts"] == _THREAD_TS
        assert captured[0]["channel_id"] == _CHANNEL
        assert captured[0]["attachments"][0]["color"] == "#2C8C55"
        assert result[0]["status"] == "success"
        assert commits == [True]
        # metadata stamped so a repeat resolve webhook won't double-post
        assert json.loads(rows[0].slack_metadata)["resolved_notified"] is True

    def test_already_notified_thread_is_skipped(self, monkeypatch):
        rows = [_sent_row({"channel": _CHANNEL, "resolved_notified": True})]
        result, captured, commits = _run_resolved(monkeypatch, rows)

        assert captured == []
        assert commits == []
        assert result[0]["status"] == "dropped"

    def test_no_delivered_message_drops_silently(self, monkeypatch):
        result, captured, commits = _run_resolved(monkeypatch, [])
        assert captured == []
        assert result[0]["status"] == "dropped"

    def test_multiple_threads_each_get_a_reply(self, monkeypatch):
        rows = [_sent_row({"channel": _CHANNEL}), _sent_row({"channel": "C0OTHER"})]
        result, captured, _ = _run_resolved(monkeypatch, rows)
        assert len(captured) == 2
        assert {c["channel_id"] for c in captured} == {_CHANNEL, "C0OTHER"}
        assert all(r["status"] == "success" for r in result)

    def test_one_failed_thread_does_not_abort_the_rest(self, monkeypatch):
        rows = [_sent_row({"channel": _CHANNEL}), _sent_row({"channel": "C0OTHER"})]

        def _flaky(**kw):
            if kw["channel_id"] == _CHANNEL:
                raise RuntimeError("token_revoked")
            return SimpleNamespace(status_code=200, data={"ok": True, "ts": "1.2", "channel": "C0OTHER"})

        result, captured, commits = _run_resolved(monkeypatch, rows, reply_fn=_flaky)

        assert len(captured) == 2  # both threads attempted despite the first raising
        statuses = sorted(r["status"] for r in result)
        assert statuses == ["failed", "success"]
        assert commits == [True]  # the surviving thread still stamped + committed

    def test_non_dict_metadata_is_skipped(self, monkeypatch):
        bad = _sent_row({})
        bad.slack_metadata = json.dumps(None)
        result, captured, commits = _run_resolved(monkeypatch, [bad])
        assert captured == []
        assert commits == []
        assert result[0]["status"] == "dropped"
