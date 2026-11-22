"""Secrets reach a provider decrypted, or not at all.

An integration's API key is stored encrypted and the row says so. Serving the stored
form authenticates with ciphertext, and the provider answers "API key not valid" on a
key that is perfectly good — with nothing in the error pointing at the real cause.
"""

from types import SimpleNamespace

import pytest
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from rag.core.utils import db_query

KEY_HEX = "00" * 32


def encrypt(plaintext: str) -> str:
    nonce = b"\x01" * 12
    return (nonce + AESGCM(bytes.fromhex(KEY_HEX)).encrypt(nonce, plaintext.encode(), None)).hex()


class FakeConnection:
    def __init__(self, rows):
        self.rows = rows

    def execute(self, *args, **kwargs):
        return self.rows

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


@pytest.fixture
def fetch(monkeypatch):
    monkeypatch.setattr(db_query.Config, "nudgebee_encryption_key", KEY_HEX)

    def run(rows):
        monkeypatch.setattr(db_query, "engine", type("E", (), {"connect": lambda self: FakeConnection(rows)})())
        return db_query.get_llm_integration_config("i1")

    return run


def row(name, value, is_encrypted=False):
    return SimpleNamespace(name=name, value=value, is_encrypted=is_encrypted)


def test_an_encrypted_secret_is_decrypted_before_it_is_served(fetch):
    config = fetch([row("llm_provider_api_key", encrypt("AIzaREALKEY"), is_encrypted=True)])

    assert config["llm_provider_api_key"] == "AIzaREALKEY"


def test_plain_values_are_untouched(fetch):
    config = fetch([row("llm_model_name", "gemini-3.7-flash"), row("llm_provider", "googleai")])

    assert config == {"llm_model_name": "gemini-3.7-flash", "llm_provider": "googleai"}


def test_a_secret_that_cannot_be_decrypted_is_left_unset(fetch):
    # Falling back to configuration is recoverable. Authenticating with ciphertext is a
    # provider error nobody can trace back to this line.
    config = fetch([row("llm_provider_api_key", "not-hex-at-all", is_encrypted=True), row("llm_provider", "googleai")])

    assert "llm_provider_api_key" not in config
    assert config["llm_provider"] == "googleai"


def test_an_empty_secret_stays_empty(fetch):
    config = fetch([row("llm_provider_api_key", "", is_encrypted=True)])

    assert config["llm_provider_api_key"] == ""


def test_the_stored_form_never_reaches_the_caller(fetch):
    stored = encrypt("AIzaREALKEY")
    config = fetch([row("llm_provider_api_key", stored, is_encrypted=True)])

    assert stored not in config.values()
