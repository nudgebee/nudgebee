"""Fail-closed auth for the inbound /webhooks/* endpoints.

A request authenticates either by the internal ``X-ACTION-TOKEN`` stamped by the
Next.js edge, or (for the directly-routed Slack events endpoint) by a valid Slack
signature over the raw body. With neither, the request is rejected.
"""

import hmac
import logging
from typing import Mapping

from slack_sdk.signature import SignatureVerifier

from notifications_server.configs.settings import settings

LOG = logging.getLogger(__name__)

ACTION_TOKEN_HEADER = "X-ACTION-TOKEN"


def internal_token_valid(headers: Mapping[str, str]) -> bool:
    """Match X-ACTION-TOKEN against ACTION_API_SERVER_TOKEN; reject if unset."""
    expected = settings.action_api_server_token
    if not expected:
        return False
    provided = headers.get(ACTION_TOKEN_HEADER) or ""
    return hmac.compare_digest(provided, expected)


def slack_signature_valid(raw_body: bytes, headers: Mapping[str, str]) -> bool:
    """Verify Slack's signature (HMAC + 5-min replay window) over the raw body.

    Reads settings.slack.signing_secret (not slack_app.signing_secret, which
    defaults to a placeholder); rejects when unset.
    """
    secret = settings.slack.signing_secret
    if not secret:
        LOG.error("SLACK_SIGNING_SECRET is not configured; rejecting Slack webhook")
        return False
    try:
        return SignatureVerifier(secret).is_valid_request(raw_body, dict(headers))
    except (ValueError, TypeError):
        return False
