"""
Tests for verify_action_token's optional-token contract.

The contract being defended (issue #31313):
  - The X-ACTION-TOKEN is OPTIONAL: when NOTIFICATION_SERVER_TOKEN is not
    configured server-side, verification is disabled and every request is
    allowed.
  - Once NOTIFICATION_SERVER_TOKEN is configured, it is ENFORCED on every gated
    endpoint: a request must present a matching X-ACTION-TOKEN, otherwise it is
    rejected (a missing token and a wrong token both 401).
"""

import pytest
from fastapi import HTTPException
from starlette.requests import Request

from notifications_server.configs.settings import settings
from notifications_server.routers.common import ACTION_TOKEN_HEADER, verify_action_token


def _request_with_token(token):
    # ASGI raw header names must be lowercased bytes; Headers.get matches case-insensitively.
    raw = [(ACTION_TOKEN_HEADER.lower().encode(), token.encode())] if token is not None else []
    return Request({"type": "http", "headers": raw})


@pytest.fixture(autouse=True)
def _reset_token():
    original = settings.notification_server_token
    yield
    settings.notification_server_token = original


# --- configured → verification enforced --------------------------------------


def test_accepts_matching_token():
    settings.notification_server_token = "secret"
    verify_action_token(_request_with_token("secret"))  # no raise


def test_rejects_wrong_token_when_configured():
    settings.notification_server_token = "secret"
    with pytest.raises(HTTPException) as exc:
        verify_action_token(_request_with_token("nope"))
    assert exc.value.status_code == 401


def test_rejects_missing_token_when_configured():
    settings.notification_server_token = "secret"
    with pytest.raises(HTTPException) as exc:
        verify_action_token(_request_with_token(None))
    assert exc.value.status_code == 401


# --- not configured → verification disabled ----------------------------------


def test_allows_missing_token_when_not_configured():
    settings.notification_server_token = ""
    verify_action_token(_request_with_token(None))  # no raise


def test_allows_any_token_when_not_configured():
    settings.notification_server_token = ""
    verify_action_token(_request_with_token("anything"))  # no raise
