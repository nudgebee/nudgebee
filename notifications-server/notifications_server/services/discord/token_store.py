"""Encrypt/decrypt for the Discord bot token held in messaging_platforms.token.

Discord installs live in the legacy messaging_platforms table (unlike Slack/MS
Teams, which moved to the encrypted integrations store). The bot token is a
long-lived, non-expiring credential, so we encrypt it at rest with the shared
AES-256-GCM helpers (security/secrets.py) and decrypt only at the point of use.

decrypt_token tolerates a plaintext value so installs written before encryption
was introduced keep working without a data migration (raw SQL cannot AES-encrypt
existing rows); such installs re-encrypt the next time they are saved.
"""

import logging
import re

from notifications_server.security.secrets import decrypt, encrypt

LOG = logging.getLogger(__name__)

# encrypt() output is lowercase hex of (12-byte nonce + ciphertext + 16-byte tag),
# so the shortest possible blob is hex(12 + 16) = 56 chars. A Discord bot token
# contains '.'-separated base64url segments, so it never matches this shape.
_MIN_CIPHERTEXT_LEN = 56
_HEX_RE = re.compile(r"\A[0-9a-f]+\Z")


def _looks_encrypted(value: str) -> bool:
    return len(value) >= _MIN_CIPHERTEXT_LEN and len(value) % 2 == 0 and bool(_HEX_RE.match(value))


def encrypt_token(raw_token: str) -> str:
    """Encrypt a bot token for storage in messaging_platforms.token."""
    return encrypt(raw_token)


def decrypt_token(stored_token: str) -> str:
    """Return the usable bot token from a stored value, decrypting when it is an
    encrypt()-format blob and passing a legacy plaintext token through unchanged.
    A malformed ciphertext or misconfigured key raises rather than silently
    returning garbage."""
    if not stored_token:
        return ""
    if _looks_encrypted(stored_token):
        return decrypt(stored_token)
    LOG.warning("Discord token is not encrypted; treating as legacy plaintext (re-encrypts on next save)")
    return stored_token
