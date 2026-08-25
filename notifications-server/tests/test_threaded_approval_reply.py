"""Regression test for #35701 — a `core.approval` prompt posted into a thread
(`message_thread_id`) rendered as plain text: no "Action required" header, no
Approve/Reject buttons, no message metadata.

`send_threaded_reply` forwarded only `message` to the generic template, so
`approval_token` never arrived; the Slack template decides an approval purely
from that token, so `is_approval` was false and the actions block was dropped.
The decision travels in the button's message metadata, so a buttonless prompt is
unanswerable — the run just blocked until timeout.

The threaded path must forward the same parameter set the top-level path does
(`send_template_notification` → `common_params_func(**raw_params)`).
"""

import asyncio
from types import SimpleNamespace

from notifications_server.models.models import Integration, IntegrationConfigValue, MessagingPlatform
from notifications_server.services import message as message_mod
from notifications_server.services.message import MessageService, SlackSender

_TENANT = "t1"
_THREAD_TS = "1700000000.000100"
_CHANNEL = "C0SLACK"
_TOKEN = "xoxb-legacy-plaintext"
_APPROVAL_TOKEN = "6465616462656566:acct-1:wf-exec-1:run-1:activity-1"


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
    """Async session that dispatches execute() on the queried entity so the real
    get_installed_platforms runs against scripted rows without a live DB."""

    def __init__(self, rows_by_entity):
        self._rows_by_entity = rows_by_entity

    async def execute(self, stmt):
        entity = stmt.column_descriptions[0]["entity"]
        return _Result(self._rows_by_entity.get(entity, []))

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


def _slack_row():
    ip = MessagingPlatform()
    ip.platform = "slack"
    ip.tenant_id = _TENANT
    ip.team_id = "T123"
    ip.token = _TOKEN
    ip.channels = [{"id": _CHANNEL}]
    return ip


def _reply(monkeypatch, parameters):
    """Run send_threaded_reply for a Slack tenant and return the kwargs that
    reached the Slack client."""
    rows = {MessagingPlatform: [_slack_row()], Integration: [], IntegrationConfigValue: []}
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
    result = asyncio.run(MessageService.send_threaded_reply(fake, _TENANT, thread, parameters))

    assert result and result[0]["status"] == "success"
    assert captured.get("thread_ts") == _THREAD_TS
    return captured


def _block_types(captured):
    return [block.get("type") for block in captured.get("blocks", [])]


def test_threaded_approval_renders_actions_block_and_metadata(monkeypatch):
    # Exactly what core.approval sends for approval_type: instant_message.
    captured = _reply(
        monkeypatch,
        {
            "message": "Approve read access for alex.morgan on hdb_catalog?",
            "approval_token": _APPROVAL_TOKEN,
            "approval_options": ["approve", "reject"],
            "workflow_name": "access-request",
            "run_id": "run-1",
            "requested_at": 1754400000,
        },
    )

    assert "header" in _block_types(captured), "approval prompt lost its 'Action required' header"

    actions = [block for block in captured["blocks"] if block.get("type") == "actions"]
    assert actions, "approval prompt rendered without buttons — it can never be answered"
    assert [element["action_id"] for element in actions[0]["elements"]] == [
        "workflow_approval_approve",
        "workflow_approval_reject",
    ]

    # The decision travels in message metadata, not in the button value (Slack caps
    # button values at 256 chars). Without it the click handler can't resolve the run.
    assert captured.get("metadata") == {
        "event_type": "workflow_approval",
        "event_payload": {"token": _APPROVAL_TOKEN},
    }


def test_threaded_reply_without_approval_token_stays_a_plain_message(monkeypatch):
    captured = _reply(monkeypatch, {"message": "deploy finished"})

    assert _block_types(captured) == ["section"]
    assert "metadata" not in captured


def test_threaded_reply_renders_workflow_footer(monkeypatch):
    # notifications.im sends workflow_metadata for tracing; forwarding the full
    # parameter set means threaded replies now carry the same footer as top-level ones.
    captured = _reply(
        monkeypatch,
        {
            "message": "deploy finished",
            "workflow_metadata": {"workflow_name": "release-check", "triggered_by": "mayank"},
        },
    )

    assert _block_types(captured) == ["section", "divider", "context"]
    assert "release-check" in captured["blocks"][-1]["elements"][0]["text"]
