"""
Regression tests for Google Chat notification routing (#30990).

Google Chat uses the service-account model: bound spaces live in the `integrations`
table, so `get_installed_platforms` synthesizes a transient `google_chat` install
(channels=None) and routing is decided per send. The required precedence is:

    payload channel  >  matched-rule channel  >  default bound space  >  do not send

The last clause matters: when nothing addresses Google Chat and no default space is
set, the notification must NOT be delivered to any space.
"""

import asyncio
from types import SimpleNamespace

from notifications_server.models.models import MessagingPlatform
from notifications_server.services.message import MessageService


def _gchat_ip():
    ip = MessagingPlatform()
    ip.platform = "google_chat"
    ip.tenant_id = "t1"
    ip.channels = None
    return ip


def _fake_self(default_space):
    async def _resolve(_session, _tenant_id):
        return default_space

    return SimpleNamespace(_resolve_default_gchat_space=_resolve)


# ----------------------------- channel resolution -----------------------------


def test_no_rule_no_payload_leaves_no_channel():
    ip = _gchat_ip()
    MessageService.get_channels_from_request_or_defaults([ip], [], "troubleshoot", {})
    assert ip.to_channel is None


def test_rule_channel_is_used():
    ip = _gchat_ip()
    rules = [{"platform": "google_chat", "channels": {"id": "spaces/RULE"}}]
    MessageService.get_channels_from_request_or_defaults([ip], rules, "troubleshoot", {})
    assert ip.to_channel == {"id": "spaces/RULE"}


def test_payload_targeting_other_platform_excludes_gchat():
    ip = _gchat_ip()
    MessageService.get_channels_from_request_or_defaults(
        [ip], [], "troubleshoot", {"channels": {"slack": {"id": "C1"}}}
    )
    assert ip.to_channel is None


# ----------------------------- default fallback -----------------------------


def test_default_fallback_routes_to_default_when_unaddressed():
    ip = _gchat_ip()
    ip.to_channel = None
    asyncio.run(MessageService._apply_gchat_default_fallback(_fake_self("spaces/DEF"), None, "t1", [ip], {}))
    assert ip.to_channel == {"id": "spaces/DEF"}


def test_no_default_means_no_send():
    ip = _gchat_ip()
    ip.to_channel = None
    asyncio.run(MessageService._apply_gchat_default_fallback(_fake_self(None), None, "t1", [ip], {}))
    assert ip.to_channel is None


def test_payload_channels_skip_default_fallback():
    ip = _gchat_ip()
    ip.to_channel = None
    asyncio.run(
        MessageService._apply_gchat_default_fallback(
            _fake_self("spaces/DEF"), None, "t1", [ip], {"channels": {"slack": {"id": "C1"}}}
        )
    )
    assert ip.to_channel is None
