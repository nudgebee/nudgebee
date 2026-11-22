"""Routing + delivery for slack_webhook targets: a rule addresses one webhook
target by id in its channels JSON; a target with no rule pointing at it stays
silent (no tenant-wide default)."""

import asyncio
from types import SimpleNamespace
from unittest.mock import patch

from notifications_server.services.message import MessageService


def _webhook_ip(target_id, url="https://hooks.slack.com/services/T000/B000/XXXX"):
    ip = SimpleNamespace(id=target_id, platform="slack_webhook", webhook_url=url, tenant_id="t1")
    ip.to_channel = "sentinel-unset"
    return ip


def _rule(target_id):
    return {
        "rule_id": "r1",
        "platform": "slack_webhook",
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
    app_rule = {"rule_id": "r1", "platform": "slack", "channels": {"id": "C1", "name": "general"}}
    MessageService.get_channels_from_request_or_defaults([a], [app_rule], "finding", {})
    assert a.to_channel is None


def test_directed_send_silences_webhook_even_with_a_matching_rule():
    a = _webhook_ip("aaaa")
    # A request with explicit `channels` targets named platforms only; webhooks
    # are rule-routed and must not fan out on a directed send.
    MessageService.get_channels_from_request_or_defaults([a], [_rule("aaaa")], "finding", {"channels": {"slack": "C1"}})
    assert a.to_channel is None


@patch("notifications_server.services.message.SlackClient.post_to_slack_webhook")
def test_template_send_posts_to_url_and_reports_success(mock_post):
    mock_post.return_value = SimpleNamespace(status_code=200, text="ok")
    svc = MessageService.__new__(MessageService)  # no DB/redis wiring needed for this path
    ip = _webhook_ip("aaaa")

    resp = asyncio.run(svc.send_slack_webhook_template_notification(ip, lambda _p: {"text": "hello"}, None, "t1"))

    mock_post.assert_called_once()
    assert mock_post.call_args.args[0] == "https://hooks.slack.com/services/T000/B000/XXXX"
    assert resp["status"] == "success"
    assert resp["platform"] == "slack_webhook"


@patch("notifications_server.services.message.SlackClient.post_to_slack_webhook")
def test_template_send_reports_a_4xx_as_rejected(mock_post):
    mock_post.return_value = SimpleNamespace(status_code=400, text="invalid_payload")
    svc = MessageService.__new__(MessageService)
    ip = _webhook_ip("aaaa")

    resp = asyncio.run(svc.send_slack_webhook_template_notification(ip, lambda _p: {}, None, "t1"))

    assert resp["status"] == "failed"
    assert resp["reason"] == "message_rejected" and resp["error_code"] == "400"
    assert resp["retryable"] is False


@patch("notifications_server.services.message.SlackClient.post_to_slack_webhook")
def test_template_send_handles_transport_failure(mock_post):
    mock_post.return_value = None  # client swallows RequestException and returns None
    svc = MessageService.__new__(MessageService)
    ip = _webhook_ip("aaaa")

    resp = asyncio.run(svc.send_slack_webhook_template_notification(ip, lambda _p: {}, None, "t1"))

    assert resp["status"] == "failed"


@patch("notifications_server.services.message.SlackClient.post_to_slack_webhook")
def test_template_send_strips_interactive_buttons_before_posting(mock_post):
    """A template built for the bot install may carry an ask-nubi/suppress
    style button; the webhook has no interactivity endpoint to service a
    click, so only link (url) buttons should survive to the wire payload."""
    mock_post.return_value = SimpleNamespace(status_code=200, text="ok")
    svc = MessageService.__new__(MessageService)
    ip = _webhook_ip("aaaa")

    template = {
        "text": "Prod alert fired",
        "blocks": [
            {
                "type": "actions",
                "elements": [
                    {"type": "button", "text": "View Details", "url": "https://app.example.com/x"},
                    {"type": "button", "text": "Ask Nubi", "action_id": "ask_nubi", "value": "{}"},
                ],
            }
        ],
        "attachments": [
            {
                "callback_id": "nb_finding_actions",
                "actions": [
                    {"type": "button", "text": "View Details", "url": "https://app.example.com/y"},
                    {"type": "select", "name": "silence_finding", "text": "Suppress..."},
                ],
            }
        ],
    }

    asyncio.run(svc.send_slack_webhook_template_notification(ip, lambda _p: template, None, "t1"))

    sent_payload = mock_post.call_args.args[1]
    assert len(sent_payload["blocks"][0]["elements"]) == 1
    assert sent_payload["blocks"][0]["elements"][0]["url"] == "https://app.example.com/x"
    assert "callback_id" not in sent_payload["attachments"][0]
    assert len(sent_payload["attachments"][0]["actions"]) == 1
    assert sent_payload["attachments"][0]["actions"][0]["url"] == "https://app.example.com/y"
