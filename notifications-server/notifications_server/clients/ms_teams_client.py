"""
Microsoft Teams client for Power Automate (Workflows) webhooks.

Channel messages, thread replies and DMs go through the bot
(services/ms_teams/bot_sender.py); no Microsoft Graph is used.
"""

import logging
from typing import Any, Dict, Optional

import requests

LOG = logging.getLogger(__name__)
CONTENT_TYPE_JSON = "application/json"


class MsTeamsClient:
    @staticmethod
    def post_to_ms_teams_webhook(
        webhook_url: str,
        card: Dict[str, Any],
        tenant: Optional[str] = None,
        timeout: int = 15,
    ) -> Optional[requests.Response]:
        """Post an Adaptive Card to a Power Automate Teams webhook. No token — the
        URL is the secret. Envelope differs from Graph: card as a JSON object in a
        `message` wrapper, not a stringified attachment. Returns the raw response,
        or None on a transport failure so one dead webhook doesn't abort the
        fan-out. The caller decides success (Power Automate 202, classic 200)."""
        payload = {
            "type": "message",
            "attachments": [
                {
                    "contentType": "application/vnd.microsoft.card.adaptive",
                    "content": card,
                }
            ],
        }
        try:
            response = requests.post(
                webhook_url,
                headers={"Content-Type": CONTENT_TYPE_JSON},
                json=payload,
                timeout=timeout,
            )
        except requests.exceptions.RequestException as exc:
            LOG.warning("MS Teams webhook connection failed for tenant %s: %s", tenant, exc)
            return None
        if response.status_code >= 400:
            LOG.warning(
                "MS Teams webhook delivery failed for tenant %s: %s - %s",
                tenant,
                response.status_code,
                response.text[:512],
            )
        else:
            LOG.debug("MS Teams webhook notification sent for tenant %s", tenant)
        return response
