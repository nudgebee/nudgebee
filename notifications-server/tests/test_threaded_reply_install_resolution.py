"""Regression test for notifications.im in-thread replies after the encrypted-
integrations migration (Slack/MS Teams moved off the legacy `messaging_platforms`
table into the encrypted integrations store, commit 05a07b10bc).

`send_threaded_reply` must resolve the install through the same union+decrypt helper
(`get_installed_platforms`) the main send path uses, not a raw `select(MessagingPlatform)`.
For a migrated tenant the legacy row is gone (or carries an undecrypted token), so the
raw lookup yielded no usable install and the reply silently failed or went out as a new
top-level message instead of landing in-thread.

Both tenant shapes are covered: legacy-only (still a `messaging_platforms` row) and
integration-migrated (only an encrypted `integrations` row). Either way the reply must
reach `reply_in_thread` with the expected `thread_ts` and a usable token.
"""

import asyncio
from types import SimpleNamespace

import pytest

from notifications_server.models.models import Integration, IntegrationConfigValue, MessagingPlatform
from notifications_server.security import secrets
from notifications_server.services import message as message_mod
from notifications_server.services.message import MessageService, SlackSender

_KEY_HEX = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
_TENANT = "t1"
_THREAD_TS = "1700000000.000100"
_CHANNEL = "C0SLACK"
_DECRYPTED_TOKEN = "xoxb-integration-decrypted"
_LEGACY_TOKEN = "xoxb-legacy-plaintext"


class _Result:
    def __init__(self, rows):
        self._rows = list(rows)

    def scalars(self):
        return self

    def all(self):
        return list(self._rows)

    def first(self):
        return self._rows[0] if self._rows else None


class _Session:
    """Async session that dispatches execute() on the queried entity, so the real
    get_installed_platforms / load_integration_installations_async run against
    scripted rows (decrypt path included) without a live DB."""

    def __init__(self, rows_by_entity):
        self._rows_by_entity = rows_by_entity

    async def execute(self, stmt):
        entity = stmt.column_descriptions[0]["entity"]
        return _Result(self._rows_by_entity.get(entity, []))

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


def _legacy_slack_row():
    ip = MessagingPlatform()
    ip.platform = "slack"
    ip.tenant_id = _TENANT
    ip.team_id = "T123"
    ip.token = _LEGACY_TOKEN
    ip.channels = [{"id": _CHANNEL}]
    return ip


def _integration_config():
    integ = Integration(id="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", name="T123", tenant_id=_TENANT)
    cfg = [
        SimpleNamespace(name="bot_token", value=secrets.encrypt(_DECRYPTED_TOKEN), is_encrypted=True),
        SimpleNamespace(name="default_channel_id", value=_CHANNEL, is_encrypted=False),
    ]
    return integ, cfg


@pytest.fixture
def enc_key(monkeypatch):
    monkeypatch.setattr(secrets.settings, "nudgebee_encryption_key", _KEY_HEX)


@pytest.mark.parametrize("origin, expected_token", [("legacy", _LEGACY_TOKEN), ("integration", _DECRYPTED_TOKEN)])
def test_send_threaded_reply_resolves_install_and_replies_in_thread(monkeypatch, enc_key, origin, expected_token):
    if origin == "legacy":
        rows = {MessagingPlatform: [_legacy_slack_row()], Integration: [], IntegrationConfigValue: []}
    else:
        integ, cfg = _integration_config()
        rows = {MessagingPlatform: [], Integration: [integ], IntegrationConfigValue: cfg}

    session = _Session(rows)
    monkeypatch.setattr(message_mod.BaseDB, "async_session", staticmethod(lambda _engine: lambda: session))

    captured = {}

    def _reply_in_thread(**kw):
        captured.update(kw)
        return SimpleNamespace(status_code=200, data={"ok": True, "ts": "1700000000.000200"})

    slack_app = SimpleNamespace(client=SimpleNamespace(reply_in_thread=_reply_in_thread))

    fake = SimpleNamespace(
        engine=object(),
        _extract_thread_params=MessageService._extract_thread_params,
        get_installed_platforms=MessageService.get_installed_platforms,
        slack_sender=SlackSender(slack_app, None),
    )
    fake._send_slack_threaded_reply = MessageService._send_slack_threaded_reply.__get__(fake)

    thread = {"message_ts": _THREAD_TS, "channel_id": _CHANNEL, "platform": "slack", "team_id": "T123"}
    result = asyncio.run(MessageService.send_threaded_reply(fake, _TENANT, thread, {"message": "deploy finished"}))

    # Went out as a thread reply (not a new top-level message) with the resolved token —
    # decrypted from the integrations store for a migrated tenant.
    assert captured.get("thread_ts") == _THREAD_TS
    assert captured.get("channel_id") == _CHANNEL
    assert captured.get("token") == expected_token
    assert result and result[0]["status"] == "success"
