"""Unit tests for the fail-closed webhook auth gate (webhook_auth.py).

Pin that an unset token/secret rejects (not accepts) and that a malformed
Slack timestamp header is rejected rather than raising.
"""

import time

import pytest
from slack_sdk.signature import SignatureVerifier

from notifications_server.security import webhook_auth as wa


@pytest.fixture(autouse=True)
def _reset_settings(monkeypatch):
    # Default both secrets to empty; each test sets what it needs.
    monkeypatch.setattr(wa.settings, "action_api_server_token", "")
    monkeypatch.setattr(wa.settings.slack, "signing_secret", "")
    yield


# --------------------------------------------------------------------------- #
# internal_token_valid
# --------------------------------------------------------------------------- #


def test_internal_token_empty_expected_rejects(monkeypatch):
    # Fail-closed: an unconfigured ACTION_API_SERVER_TOKEN must reject even a
    # request that supplies *some* token.
    monkeypatch.setattr(wa.settings, "action_api_server_token", "")
    assert wa.internal_token_valid({"X-ACTION-TOKEN": "anything"}) is False


def test_internal_token_match_accepts(monkeypatch):
    monkeypatch.setattr(wa.settings, "action_api_server_token", "s3cr3t-token")
    assert wa.internal_token_valid({"X-ACTION-TOKEN": "s3cr3t-token"}) is True


def test_internal_token_mismatch_rejects(monkeypatch):
    monkeypatch.setattr(wa.settings, "action_api_server_token", "s3cr3t-token")
    assert wa.internal_token_valid({"X-ACTION-TOKEN": "wrong"}) is False


def test_internal_token_missing_header_rejects(monkeypatch):
    monkeypatch.setattr(wa.settings, "action_api_server_token", "s3cr3t-token")
    assert wa.internal_token_valid({}) is False


# --------------------------------------------------------------------------- #
# slack_signature_valid
# --------------------------------------------------------------------------- #


def _signed_headers(secret: str, body: bytes, timestamp: str):
    sig = SignatureVerifier(secret).generate_signature(timestamp=timestamp, body=body)
    return {"x-slack-signature": sig, "x-slack-request-timestamp": timestamp}


def test_slack_empty_secret_rejects(monkeypatch):
    # Fail-closed before a SignatureVerifier is ever constructed — an empty key
    # would otherwise produce a signature an attacker can reproduce.
    monkeypatch.setattr(wa.settings.slack, "signing_secret", "")
    body = b'{"type":"event_callback"}'
    headers = _signed_headers("", body, str(int(time.time())))
    assert wa.slack_signature_valid(body, headers) is False


def test_slack_valid_signature_accepts(monkeypatch):
    secret = "good-signing-secret"
    monkeypatch.setattr(wa.settings.slack, "signing_secret", secret)
    body = b'{"type":"event_callback","event":{}}'
    headers = _signed_headers(secret, body, str(int(time.time())))
    assert wa.slack_signature_valid(body, headers) is True


def test_slack_wrong_signature_rejects(monkeypatch):
    secret = "good-signing-secret"
    monkeypatch.setattr(wa.settings.slack, "signing_secret", secret)
    body = b'{"type":"event_callback"}'
    headers = _signed_headers("a-different-secret", body, str(int(time.time())))
    assert wa.slack_signature_valid(body, headers) is False


def test_slack_stale_timestamp_rejects(monkeypatch):
    # Outside slack_sdk's built-in 5-minute replay window.
    secret = "good-signing-secret"
    monkeypatch.setattr(wa.settings.slack, "signing_secret", secret)
    body = b'{"type":"event_callback"}'
    stale = str(int(time.time()) - 60 * 10)
    headers = _signed_headers(secret, body, stale)
    assert wa.slack_signature_valid(body, headers) is False


def test_slack_nonnumeric_timestamp_rejects_without_crashing(monkeypatch):
    # A malformed timestamp must fail closed (caught ValueError), not 500.
    secret = "good-signing-secret"
    monkeypatch.setattr(wa.settings.slack, "signing_secret", secret)
    body = b'{"type":"event_callback"}'
    headers = {"x-slack-signature": "v0=deadbeef", "x-slack-request-timestamp": "not-a-number"}
    assert wa.slack_signature_valid(body, headers) is False


def test_slack_missing_headers_rejects(monkeypatch):
    secret = "good-signing-secret"
    monkeypatch.setattr(wa.settings.slack, "signing_secret", secret)
    assert wa.slack_signature_valid(b'{"type":"event_callback"}', {}) is False
