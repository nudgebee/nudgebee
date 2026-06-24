"""AES-256-GCM encrypt/decrypt for integration secrets, byte-compatible with
api-server's common.Encrypt / common.Decrypt (services/common/secrets.go).

Storage format: hex(nonce[12] || ciphertext || tag[16]); the key is the shared
NUDGEBEE_ENCRYPTION_KEY (64 hex chars = 32 bytes). This lets notifications-server
read and write the same encrypted integration_config_values rows as api-server,
so Slack/MS Teams OAuth tokens live encrypted at rest.
"""

import os

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from notifications_server.configs.settings import settings

_NONCE_BYTES = 12
_MIN_LEN = _NONCE_BYTES + 16  # 12-byte nonce + 16-byte GCM tag (empty plaintext)


def _key() -> bytes:
    raw = settings.nudgebee_encryption_key
    if not raw:
        raise ValueError("NUDGEBEE_ENCRYPTION_KEY is not configured")
    return bytes.fromhex(raw)


def _encrypt(plaintext: str, key: bytes) -> str:
    nonce = os.urandom(_NONCE_BYTES)
    ciphertext = AESGCM(key).encrypt(nonce, plaintext.encode(), None)
    return (nonce + ciphertext).hex()


def _decrypt(encrypted: str, key: bytes) -> str:
    data = bytes.fromhex(encrypted)
    if len(data) < _MIN_LEN:
        raise ValueError("invalid encrypted value: too short")
    nonce, ciphertext = data[:_NONCE_BYTES], data[_NONCE_BYTES:]
    return AESGCM(key).decrypt(nonce, ciphertext, None).decode()


def encrypt(plaintext: str) -> str:
    """Encrypt a secret to api-server's storage format. Empty input returns empty
    (matches api-server, which stores empty values without encryption)."""
    if not plaintext:
        return ""
    return _encrypt(plaintext, _key())


def decrypt(encrypted: str) -> str:
    """Decrypt a value produced by encrypt() or api-server common.Encrypt. Empty
    input returns empty so callers can pass through unset secrets safely."""
    if not encrypted:
        return ""
    return _decrypt(encrypted, _key())
