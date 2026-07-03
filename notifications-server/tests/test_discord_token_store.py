"""Tests for the Discord bot-token at-rest encryption helper (token_store).

The bot token is a long-lived credential, so save_discord_installation encrypts it
and the send/list/test paths decrypt at use. These lock in the roundtrip, the
legacy-plaintext tolerance (installs written before encryption keep working with no
data migration), and that a corrupt ciphertext fails loud rather than silently
returning garbage.
"""

import pytest

from notifications_server.security import secrets
from notifications_server.services.discord import token_store

_KEY_HEX = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
# Discord bot tokens are '.'-separated base64url segments — never all-hex.
_RAW_TOKEN = "MTIzNDU2Nzg5MDEyMzQ1Njc4.GhAbcd.efGHijKLmnOP-qrsTUvwx_yz0123456789"


@pytest.fixture
def enc_key(monkeypatch):
    monkeypatch.setattr(secrets.settings, "nudgebee_encryption_key", _KEY_HEX)


def test_encrypt_then_decrypt_roundtrips(enc_key):
    stored = token_store.encrypt_token(_RAW_TOKEN)
    assert stored != _RAW_TOKEN
    assert token_store._looks_encrypted(stored)
    assert token_store.decrypt_token(stored) == _RAW_TOKEN


def test_legacy_plaintext_token_passes_through(enc_key):
    # A pre-encryption install stored the raw token; it must still be usable.
    assert not token_store._looks_encrypted(_RAW_TOKEN)
    assert token_store.decrypt_token(_RAW_TOKEN) == _RAW_TOKEN


def test_empty_token_returns_empty(enc_key):
    assert token_store.decrypt_token("") == ""


def test_corrupt_ciphertext_raises_rather_than_returning_garbage(enc_key):
    # Looks like ciphertext (all hex, long enough) but isn't valid → must fail loud.
    fake = "ab" * 40
    assert token_store._looks_encrypted(fake)
    with pytest.raises(Exception):
        token_store.decrypt_token(fake)
