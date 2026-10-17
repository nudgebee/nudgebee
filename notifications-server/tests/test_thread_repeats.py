"""The Slack/Discord integration setting `thread_repeats`: unset or "true" replies
to repeat findings in the original thread; "false" posts every repeat as a new
message, still recording it so the latest one is what later lookups find."""

import asyncio
import json
from datetime import datetime
from types import SimpleNamespace

from notifications_server.clients.discord_client import DiscordClient
from notifications_server.message_templates.discord import finding as discord_finding
from notifications_server.models.models import Integration, MessagingPlatform, SentNotifications
from notifications_server.services import message as message_mod
from notifications_server.services import messaging_installations as mi
from notifications_server.services.message import MessageService

_FP = "fp-9"
_THREAD = "1700000000.000100"


def _integration():
    return Integration(id="44444444-4444-4444-4444-444444444444", name="T123", tenant_id="t1")


class TestSettingParse:
    def test_unset_threads(self):
        assert mi.build_installation("slack", _integration(), {}).thread_repeats is True

    def test_false_posts_new_messages(self):
        assert mi.build_installation("discord", _integration(), {"thread_repeats": "false"}).thread_repeats is False
        assert mi.build_installation("slack", _integration(), {"thread_repeats": "False"}).thread_repeats is False
        assert mi.build_installation("slack", _integration(), {"thread_repeats": False}).thread_repeats is False

    def test_true_threads(self):
        assert mi.build_installation("slack", _integration(), {"thread_repeats": "true"}).thread_repeats is True

    def test_legacy_row_threads(self):
        assert MessagingPlatform().thread_repeats is True

    def test_survives_cache_roundtrip(self):
        inst = mi.build_installation("slack", _integration(), {"thread_repeats": "false"})
        assert MessagingPlatform.from_dict(inst.to_dict()).thread_repeats is False

    def test_cache_entry_from_before_the_setting_threads(self):
        data = mi.build_installation("slack", _integration(), {}).to_dict()
        data.pop("thread_repeats")
        assert MessagingPlatform.from_dict(data).thread_repeats is True


def _slack_service(monkeypatch, existing_thread):
    monkeypatch.setattr(message_mod, "get_slack_finding_message", lambda *_: ("", [], [{"title": "x"}]))
    lookups, posts = [], []

    async def _lookup(*args):
        lookups.append(args)
        return existing_thread

    def _post(tenant_id, ip, thread_id, **kw):
        posts.append(thread_id)
        return SimpleNamespace(status_code=200, data={"ok": True, "channel": "C1", "ts": "1700000001.000200"})

    sender = SimpleNamespace(
        check_if_sent_already=_lookup,
        post_to_slack=_post,
        get_new_sent_notification=lambda *a, **k: "new-row",
    )
    fake = SimpleNamespace(slack_app=object(), slack_sender=sender)
    return MessageService._send_finding_to_slack.__get__(fake), lookups, posts


def _slack_ip(thread_repeats):
    return SimpleNamespace(team_id="T123", to_channel=[{"id": "C1"}], thread_repeats=thread_repeats)


class TestSlackSender:
    def test_threading_off_posts_top_level_and_records_it(self, monkeypatch):
        send, lookups, posts = _slack_service(monkeypatch, existing_thread=_THREAD)
        _, row = asyncio.run(send(None, "t1", _slack_ip(False), {}, _FP))
        assert lookups == []
        assert posts == [None]
        assert row == "new-row"

    def test_threading_on_replies_in_thread(self, monkeypatch):
        send, lookups, posts = _slack_service(monkeypatch, existing_thread=_THREAD)
        _, row = asyncio.run(send(None, "t1", _slack_ip(True), {}, _FP))
        assert len(lookups) == 1
        assert posts == [_THREAD]
        assert row is None


def _discord_service(monkeypatch, existing_thread):
    monkeypatch.setattr(discord_finding, "get_discord_finding_message", lambda _f: {"embeds": []})
    lookups, calls = [], []

    async def _lookup(*args):
        lookups.append(args)
        return existing_thread

    async def _token(*_):
        return "bot-token"

    def _chat_post(cls, **kw):
        calls.append(("post", None))
        return {"ok": True, "ts": "m-2"}

    def _reply(cls, **kw):
        calls.append(("reply", kw["thread_ts"]))
        return {"ok": True, "ts": "m-3"}

    monkeypatch.setattr(DiscordClient, "chat_post", classmethod(_chat_post))
    monkeypatch.setattr(DiscordClient, "reply_in_thread", classmethod(_reply))
    sender = SimpleNamespace(
        acquire_discord_access_token=_token,
        check_if_sent_already=_lookup,
        get_new_sent_notification=lambda *a, **k: "new-row",
    )
    fake = SimpleNamespace(discord_sender=sender)
    return MessageService._send_finding_to_discord.__get__(fake), lookups, calls


def _discord_ip(thread_repeats):
    return SimpleNamespace(id="i1", to_channel={"id": "D1", "name": "alerts"}, thread_repeats=thread_repeats)


class TestDiscordSender:
    def test_threading_off_posts_new_message_and_records_it(self, monkeypatch):
        send, lookups, calls = _discord_service(monkeypatch, existing_thread="m-1")
        _, row = asyncio.run(send(None, "t1", _discord_ip(False), {}, _FP))
        assert lookups == []
        assert calls == [("post", None)]
        assert row == "new-row"

    def test_threading_on_replies_to_original(self, monkeypatch):
        send, lookups, calls = _discord_service(monkeypatch, existing_thread="m-1")
        _, row = asyncio.run(send(None, "t1", _discord_ip(True), {}, _FP))
        assert len(lookups) == 1
        assert calls == [("reply", "m-1")]
        assert row is None


class _Result:
    def __init__(self, rows):
        self._rows = list(rows)

    def scalars(self):
        return self

    def all(self):
        return list(self._rows)


class _Session:
    """Returns rows in stored order unless the query asks for newest first."""

    def __init__(self, rows):
        self._rows = rows

    async def execute(self, stmt):
        rows = self._rows
        if "ORDER BY sent_notifications.created_at DESC" in str(stmt):
            rows = sorted(rows, key=lambda row: row.created_at, reverse=True)
        return _Result(rows)

    def add(self, row):
        pass

    async def commit(self):
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


def _sent_row(thread_ts, channel="C1", **metadata):
    row = SentNotifications()
    row.created_at = datetime.fromtimestamp(float(thread_ts))
    row.tenant_id = "t1"
    row.fingerprint = _FP
    row.slack_team_id = "T123"
    row.slack_thread_id = thread_ts
    row.slack_metadata = json.dumps({"channel": channel, **metadata})
    return row


def _resolve(monkeypatch, rows):
    session = _Session(rows)
    monkeypatch.setattr(message_mod.BaseDB, "async_session", staticmethod(lambda _engine: lambda: session))
    replies = []

    def _reply_in_thread(**kw):
        replies.append((kw["channel_id"], kw["thread_ts"]))
        return SimpleNamespace(status_code=200, data={"ok": True})

    async def _installs(*_a, **_k):
        return [SimpleNamespace(team_id="T123", token="xoxb", platform="slack")]

    fake = SimpleNamespace(
        engine=object(),
        slack_app=SimpleNamespace(client=SimpleNamespace(reply_in_thread=_reply_in_thread)),
        get_installed_platforms=_installs,
    )
    finding = {"id": "f1", "title": "t", "fingerprint": _FP}
    asyncio.run(MessageService.send_finding_resolved_reply.__get__(fake)("t1", {"finding": finding}))
    return replies


class TestResolvedReplyLatestPerChannel:
    # Rows are stored oldest first; only the query's ordering puts the newest first.
    def test_one_reply_under_the_newest_repeat(self, monkeypatch):
        rows = [_sent_row("1.0"), _sent_row("2.0"), _sent_row("3.0")]
        assert _resolve(monkeypatch, rows) == [("C1", "3.0")]

    def test_repeat_resolve_does_not_fall_back_to_an_older_message(self, monkeypatch):
        rows = [_sent_row("1.0"), _sent_row("2.0"), _sent_row("3.0", resolved_notified=True)]
        assert _resolve(monkeypatch, rows) == []

    def test_each_channel_still_gets_its_own_reply(self, monkeypatch):
        rows = [_sent_row("1.0", channel="C2"), _sent_row("2.0"), _sent_row("3.0", channel="C2")]
        assert _resolve(monkeypatch, rows) == [("C2", "3.0"), ("C1", "2.0")]
