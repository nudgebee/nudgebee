"""Coarse secret scrubbing for text retained from watched channels.

Deliberately conservative and deliberately limited: this catches well-known
credential shapes so they are not persisted, and nothing more. It is NOT a data
loss prevention system and must not be described as one — llm-server's
security/egressfilter does real detection at the model boundary, which is where
content is inspected before it reaches a provider.
"""

import re

PLACEHOLDER = "[redacted]"

_PATTERNS = [
    # Slack tokens (bot/user/app/refresh) and legacy webhooks.
    re.compile(r"xox[baprs]-[A-Za-z0-9-]{10,}"),
    re.compile(r"https://hooks\.slack\.com/services/[A-Za-z0-9/+_-]+"),
    # AWS access key ids and obvious secret-key assignments.
    re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b"),
    re.compile(r"(?i)\baws_secret_access_key\b\s*[:=]\s*\S+"),
    # Bearer / authorization headers and JWTs.
    re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~+/-]{20,}=*"),
    re.compile(r"\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b"),
    # PEM private key blocks.
    re.compile(r"-----BEGIN[^-]{0,40}PRIVATE KEY-----.*?-----END[^-]{0,40}PRIVATE KEY-----", re.DOTALL),
    # Connection strings carrying inline credentials.
    re.compile(r"\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:@/]+:[^\s@/]+@\S+"),
    # Generic assignments to obviously-secret names.
    re.compile(r"(?i)\b(?:api[_-]?key|secret|password|passwd|token)\b\s*[:=]\s*\S+"),
]


def redact_secrets(text: str) -> str:
    """Replace recognised credential shapes with a placeholder."""
    if not text:
        return text
    for pattern in _PATTERNS:
        text = pattern.sub(PLACEHOLDER, text)
    return text
