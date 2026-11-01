"""
/channels/list and /users/list validate their action payload (issue #32276).

The body is a typed HasuraActionPayload, so FastAPI rejects a missing or unknown
`platform` with a 422 before the handler runs. Every platform the service
supports (including discord) and the extra keys the action gateway sends must
still validate and reach the service.
"""

import asyncio
import json

import pytest
from pydantic import ValidationError
from starlette.requests import Request

from notifications_server.routers import common
from notifications_server.routers.common import HasuraActionPayload

GATEWAY_EXTRAS = {"action": {"name": "notifications_list_channels"}, "request_query": "query { ... }"}


class _FakeCommonService:
    def __init__(self, *args, **kwargs):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def list_users(self, platform, tenant):
        return [{"platform": platform, "tenant": tenant}]


def _payload(platform, tenant="tenant-1"):
    return {"input": {"platform": platform}, "session_variables": {"tenant_id": tenant}, **GATEWAY_EXTRAS}


def _request(headers=None):
    raw = [(k.lower().encode(), v.encode()) for k, v in (headers or {}).items()]
    return Request({"type": "http", "headers": raw})


@pytest.mark.parametrize("platform", ["slack", "ms_teams", "google_chat", "discord"])
def test_supported_platforms_validate(platform):
    assert HasuraActionPayload.model_validate(_payload(platform)).input.platform == platform


def test_missing_platform_is_rejected():
    with pytest.raises(ValidationError):
        HasuraActionPayload.model_validate({"input": {}, "session_variables": {"tenant_id": "t"}})


def test_unknown_platform_is_rejected():
    with pytest.raises(ValidationError):
        HasuraActionPayload.model_validate(_payload("telegram"))


@pytest.fixture
def stub_services(monkeypatch):
    monkeypatch.setattr(
        common, "_list_channels_sync", lambda platform, tenant: [{"platform": platform, "tenant": tenant}]
    )
    monkeypatch.setattr(common, "CommonService", _FakeCommonService)


@pytest.mark.parametrize("endpoint", [common.list_channels, common.list_users])
def test_endpoint_passes_platform_and_tenant(stub_services, endpoint):
    body = HasuraActionPayload.model_validate(_payload("discord"))
    resp = asyncio.run(endpoint(_request(), body))
    assert json.loads(resp.body) == [{"platform": "discord", "tenant": "tenant-1"}]


@pytest.mark.parametrize("endpoint", [common.list_channels, common.list_users])
def test_endpoint_falls_back_to_tenant_header(stub_services, endpoint):
    body = HasuraActionPayload.model_validate({"input": {"platform": "slack"}})
    resp = asyncio.run(endpoint(_request({"tenant": "tenant-h"}), body))
    assert json.loads(resp.body) == [{"platform": "slack", "tenant": "tenant-h"}]
