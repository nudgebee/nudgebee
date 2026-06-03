"""Magic-link token + callback validation tests.

Covers:
  - one-shot consume semantics (a token is unusable after first use)
  - expiry rejection
  - unknown-token rejection
  - callback's reflected-XSS shield (token shape regex must reject any
    payload outside ``[A-Za-z0-9_-]{20,80}`` before reflecting it into
    the scanner-shield interstitial HTML).
"""

import asyncio
import time
from unittest.mock import MagicMock

import pytest


# ---------- token consume semantics ----------


@pytest.fixture(autouse=True)
def _isolate_token_store():
    """Each test starts and ends with a clean magic-link token store."""
    from benchmark_server.controllers.auth_controller import _magic_lock, _magic_tokens

    with _magic_lock:
        _magic_tokens.clear()
    yield
    with _magic_lock:
        _magic_tokens.clear()


def test_consume_returns_email_for_valid_token():
    from benchmark_server.controllers.auth_controller import (
        _consume_magic_token,
        _issue_magic_token,
    )

    token = _issue_magic_token("alice@example.com")
    assert _consume_magic_token(token) == "alice@example.com"


def test_consume_is_one_shot():
    """First consume succeeds, second returns None — prevents replay."""
    from benchmark_server.controllers.auth_controller import (
        _consume_magic_token,
        _issue_magic_token,
    )

    token = _issue_magic_token("alice@example.com")
    assert _consume_magic_token(token) == "alice@example.com"
    assert _consume_magic_token(token) is None


def test_consume_returns_none_for_unknown_token():
    from benchmark_server.controllers.auth_controller import _consume_magic_token

    assert _consume_magic_token("never-issued") is None


def test_consume_returns_none_for_expired_token():
    """Manually insert an already-expired entry and confirm it's rejected."""
    from benchmark_server.controllers.auth_controller import (
        _consume_magic_token,
        _magic_lock,
        _magic_tokens,
    )

    expired_token = "expired-test-token"
    with _magic_lock:
        _magic_tokens[expired_token] = ("alice@example.com", time.time() - 1)
    assert _consume_magic_token(expired_token) is None


# ---------- callback token-shape validation (XSS) ----------


def _call_callback(token: str, confirm: str = ""):
    """Invoke the async ``email_callback`` route handler synchronously."""
    from benchmark_server.controllers.auth_controller import email_callback

    request = MagicMock()
    request.url.scheme = "http"
    return asyncio.run(email_callback(request, token=token, confirm=confirm))


@pytest.mark.parametrize(
    "malicious_token",
    [
        '";alert(1)//',  # JS-string-break + script injection
        "<script>x</script>",  # raw HTML
        "../../etc/passwd",  # path-traversal style
        "a" * 200,  # too long
        "shrt",  # too short
        "has space",  # space disallowed
        "has/slash",  # slash disallowed
    ],
)
def test_callback_rejects_malformed_token(malicious_token):
    """Any token outside [A-Za-z0-9_-]{20,80} must redirect to /signin
    BEFORE being reflected into the scanner-shield interstitial. Without
    this check, the JS-redirect HTML becomes a reflected-XSS sink."""
    response = _call_callback(malicious_token)
    assert response.status_code == 302
    assert "/signin?err=invalid_link" in response.headers["location"]


def test_callback_rejects_empty_token():
    response = _call_callback("")
    assert response.status_code == 302
    assert "/signin?err=invalid_link" in response.headers["location"]
