"""
A message a person sends from Nubi goes to the channel they named and nowhere
else: tenant rules, snoozes and default channels never apply to it.
"""

import asyncio
from types import SimpleNamespace

from notifications_server.services import delivery_records
from notifications_server.services import message as message_module
from notifications_server.services.delivery_errors import NO_DESTINATION
from notifications_server.services.message import MessageService
from notifications_server.services.rules import NUBI_SOURCE

TENANT = "tenant-1"
TEAMS_CHANNEL = {"id": "19:ops@thread.tacv2", "team_id": "team-1"}


class _NoRules:
    """Fails the test if anything asks the rules about a Nubi message."""

    rules_service = None

    async def match_notification_rules(self, *_args):
        raise AssertionError("a Nubi message must not be matched against rules")


class _Request(SimpleNamespace):
    """What send_message reads from a SendMessageRequest."""

    def __init__(self, request, payload):
        super().__init__(**vars(request))
        self._payload = payload

    def dict(self, **_kwargs):
        return self._payload


class _Session:
    async def __aenter__(self):
        return self

    async def __aexit__(self, *_exc):
        return False


def _service(monkeypatch):
    service = MessageService.__new__(MessageService)
    service.engine = None
    service.rule_matcher = _NoRules()
    service.installs = [
        SimpleNamespace(id="slack-1", platform="slack", channels={"id": "C-DEFAULT"}, to_channel=None),
        SimpleNamespace(id="teams-1", platform="ms_teams", channels={"id": "T-DEFAULT"}, to_channel=None),
    ]
    service.sent = []

    async def installs(_session, _tenant_id):
        return service.installs

    async def trigger(_session, _fingerprint, platforms, _payload, _parameters, _rows, _tenant, _type, dispatch=None):
        service.sent.extend((ip.platform, ip.to_channel) for ip in platforms if ip.to_channel)
        return [{"status": "success"}]

    service.get_installed_platforms_from_cache_or_db = installs
    service.trigger_notification = trigger
    monkeypatch.setattr(message_module.BaseDB, "async_session", lambda _engine: lambda: _Session())
    return service


def _send(service, channels):
    payload = {"type": "generic", "source": NUBI_SOURCE, "parameters": {"message": "Summary\nDetails"}}
    if channels is not None:
        payload["channels"] = channels
    request = SimpleNamespace(tenant_id=TENANT, type="generic", source=NUBI_SOURCE, thread=None)
    dispatch = delivery_records.dispatch_for(TENANT, "generic", payload, source=NUBI_SOURCE)
    service._dispatch_for = lambda _request, _payload: dispatch
    return asyncio.run(service.send_message(_Request(request, payload))), dispatch


def test_nubi_message_goes_only_to_the_named_channel(monkeypatch):
    service = _service(monkeypatch)

    results, dispatch = _send(service, {"ms_teams": [TEAMS_CHANNEL]})

    assert results == [{"status": "success"}]
    assert service.sent == [("ms_teams", [TEAMS_CHANNEL])]
    assert dispatch.requested is True


def test_nubi_message_without_a_channel_is_not_sent_to_defaults(monkeypatch):
    service = _service(monkeypatch)

    results, _ = _send(service, None)

    assert service.sent == []
    assert results[0]["status"] == "not_sent"
    assert results[0]["reason"] == NO_DESTINATION


def test_nubi_message_to_a_platform_not_installed_is_not_sent(monkeypatch):
    service = _service(monkeypatch)

    results, _ = _send(service, {"google_chat": [{"id": "spaces/AAA"}]})

    assert service.sent == []
    assert results[0]["reason"] == NO_DESTINATION


def test_nubi_message_is_its_own_kind_in_delivery_history():
    parameters = {"message": "Summary\nDetails"}

    kind = delivery_records.kind_of("generic", parameters, NUBI_SOURCE)

    assert kind == delivery_records.NUBI_MESSAGE
    assert delivery_records.title_of(kind, parameters) == "Summary"
