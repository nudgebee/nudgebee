"""Routing + delivery for ms_teams_webhook targets: a rule addresses one webhook
target by id in its channels JSON; a target with no rule pointing at it stays
silent (no tenant-wide default)."""

import asyncio
from types import SimpleNamespace
from unittest.mock import patch

from notifications_server.services.message import MessageService


def _webhook_ip(target_id, url="https://x.logic.azure.com/hook"):
    ip = SimpleNamespace(id=target_id, platform="ms_teams_webhook", webhook_url=url, tenant_id="t1")
    ip.to_channel = "sentinel-unset"
    return ip


def _rule(target_id):
    return {
        "rule_id": "r1",
        "platform": "ms_teams_webhook",
        "channels": {"webhook_target_id": str(target_id), "name": "Prod alerts"},
        "suppressed": False,
        "expires_at": None,
        "delivery_mode": "realtime",
        "severity": None,
    }


def test_rule_addresses_only_its_own_webhook_target():
    a, b = _webhook_ip("aaaa"), _webhook_ip("bbbb")
    MessageService.get_channels_from_request_or_defaults([a, b], [_rule("aaaa")], "finding", {})
    assert a.to_channel == {"webhook_target_id": "aaaa", "name": "Prod alerts"}
    assert b.to_channel is None  # no rule points at B -> not delivered


def test_webhook_target_with_no_rule_stays_silent():
    a = _webhook_ip("aaaa")
    MessageService.get_channels_from_request_or_defaults([a], [], "finding", {})
    assert a.to_channel is None


def test_webhook_ignores_app_channel_rule():
    a = _webhook_ip("aaaa")
    app_rule = {"rule_id": "r1", "platform": "ms_teams", "channels": {"team_id": "T", "channels": [{"id": "C"}]}}
    MessageService.get_channels_from_request_or_defaults([a], [app_rule], "finding", {})
    assert a.to_channel is None


def test_directed_send_silences_webhook_even_with_a_matching_rule():
    a = _webhook_ip("aaaa")
    # A request with explicit `channels` targets named platforms only; webhooks
    # are rule-routed and must not fan out on a directed send.
    MessageService.get_channels_from_request_or_defaults([a], [_rule("aaaa")], "finding", {"channels": {"slack": "C1"}})
    assert a.to_channel is None


@patch("notifications_server.services.message.MsTeamsClient.post_to_ms_teams_webhook")
def test_template_send_posts_to_url_and_reports_success(mock_post):
    mock_post.return_value = SimpleNamespace(status_code=202, text="")
    svc = MessageService.__new__(MessageService)  # no DB/redis wiring needed for this path
    ip = _webhook_ip("aaaa")

    resp = asyncio.run(
        svc.send_teams_webhook_template_notification(ip, lambda _p: {"type": "AdaptiveCard"}, None, "t1")
    )

    mock_post.assert_called_once()
    assert mock_post.call_args.args[0] == "https://x.logic.azure.com/hook"
    assert resp["status"] == "success"
    assert resp["platform"] == "ms_teams_webhook"


@patch("notifications_server.services.message.MsTeamsClient.post_to_ms_teams_webhook")
def test_template_send_reports_a_4xx_as_rejected(mock_post):
    mock_post.return_value = SimpleNamespace(status_code=400, text="Bad payload")
    svc = MessageService.__new__(MessageService)
    ip = _webhook_ip("aaaa")

    resp = asyncio.run(svc.send_teams_webhook_template_notification(ip, lambda _p: {}, None, "t1"))

    assert resp["status"] == "failed"
    assert resp["reason"] == "message_rejected" and resp["error_code"] == "400"
    assert resp["retryable"] is False


@patch("notifications_server.services.message.MsTeamsClient.post_to_ms_teams_webhook")
def test_template_send_handles_transport_failure(mock_post):
    mock_post.return_value = None  # client swallows RequestException and returns None
    svc = MessageService.__new__(MessageService)
    ip = _webhook_ip("aaaa")

    resp = asyncio.run(svc.send_teams_webhook_template_notification(ip, lambda _p: {}, None, "t1"))

    assert resp["status"] == "failed"
