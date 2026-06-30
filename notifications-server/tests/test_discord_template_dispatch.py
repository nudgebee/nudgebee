"""Tests for routing template notifications to Discord.

Covers the dispatcher path added alongside the Discord templates:
`MessageService.send_discord_template_notification` (token acquire → channel
normalization → DiscordClient.chat_post → success/failed response). The Discord
API call is mocked, so this exercises the wiring without a live bot.
"""

import asyncio
from types import SimpleNamespace
from unittest.mock import patch

from notifications_server.services.message import MessageService


class _DiscordSender:
    def __init__(self, token):
        self._token = token

    async def acquire_discord_access_token(self, session, ip):
        return self._token


def _dispatch(token, chat_post_result, to_channel={"id": "C1"}):
    svc = SimpleNamespace(discord_sender=_DiscordSender(token))
    ip = SimpleNamespace(to_channel=to_channel, id="install-1", platform="discord")
    template_func = lambda _param: {"content": "hi", "embeds": [{"title": "t"}]}  # noqa: E731
    with patch(
        "notifications_server.clients.discord_client.DiscordClient.chat_post", return_value=chat_post_result
    ) as chat_post:
        resp = asyncio.run(
            MessageService.send_discord_template_notification(svc, ip, template_func, object(), None, "tenant")
        )
    return resp, chat_post


def test_dispatch_success_calls_chat_post_and_returns_success():
    resp, chat_post = _dispatch("tok", {"ok": True, "ts": "msg-123"})
    chat_post.assert_called_once()
    # the rendered template payload is forwarded as kwargs
    kwargs = chat_post.call_args.kwargs
    assert kwargs["token"] == "tok" and kwargs["channel_id"] == "C1"
    assert kwargs["content"] == "hi" and kwargs["embeds"] == [{"title": "t"}]
    assert resp["platform"] == "discord" and resp["status"] == "success"
    assert resp["channel_id"] == "C1" and resp["message_ts"] == "msg-123"


def test_dispatch_failure_surfaces_error():
    resp, _ = _dispatch("tok", {"ok": False, "error": "boom"})
    assert resp["platform"] == "discord" and resp["status"] == "failed"


def test_dispatch_without_token_fails_before_calling_api():
    svc = SimpleNamespace(discord_sender=_DiscordSender(None))
    ip = SimpleNamespace(to_channel={"id": "C1"}, id="install-1", platform="discord")
    with patch("notifications_server.clients.discord_client.DiscordClient.chat_post") as chat_post:
        resp = asyncio.run(
            MessageService.send_discord_template_notification(
                svc, ip, lambda _p: {"content": "x"}, object(), None, "tenant"
            )
        )
    chat_post.assert_not_called()
    assert resp["status"] == "failed"


def test_dispatch_without_channel_id_fails():
    resp, chat_post = _dispatch("tok", {"ok": True, "ts": "1"}, to_channel=None)
    chat_post.assert_not_called()
    assert resp["status"] == "failed"
