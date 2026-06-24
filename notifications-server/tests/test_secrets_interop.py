"""Locks notifications-server AES-256-GCM to api-server's on-disk format so the two
services can read each other's encrypted integration_config_values.

The vector is a deterministic (fixed all-zero 12-byte nonce) output of api-server's
common.Encrypt format — hex(nonce || ciphertext || tag) — for the key and plaintext
below. Regenerate with an equivalent Go AES-256-GCM seal if the format ever changes.
"""

from notifications_server.security import secrets

_KEY_HEX = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
_GO_VECTOR = "00000000000000000000000060c9d1b9d04ee6d825c1c7417d5efee9ff3533305cee123478c80f98ffe0457bcdb2b7b8395a01"
_PLAINTEXT = "nudgebee-interop-vector"


def test_decrypts_go_produced_vector():
    key = bytes.fromhex(_KEY_HEX)
    assert secrets._decrypt(_GO_VECTOR, key) == _PLAINTEXT


def test_round_trip():
    key = bytes.fromhex(_KEY_HEX)
    blob = secrets._encrypt("xoxb-some-secret-token", key)
    assert blob != "xoxb-some-secret-token"
    assert secrets._decrypt(blob, key) == "xoxb-some-secret-token"


def test_public_api_via_settings(monkeypatch):
    monkeypatch.setattr(secrets.settings, "nudgebee_encryption_key", _KEY_HEX)
    blob = secrets.encrypt("hello")
    assert secrets.decrypt(blob) == "hello"
    assert secrets.encrypt("") == ""
    assert secrets.decrypt("") == ""
