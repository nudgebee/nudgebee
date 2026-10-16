"""Approve / Reject on an Auto Optimize "approval needed" message, on Slack and
Microsoft Teams.

The message's approval token names the approval as
``auto_optimize:<approval_id>:<account_id>:<tenant_id>``. It is not a credential:
a click is decided by the workflow server's POST /autopilot/approval as the
clicking NudgeBee user, with that user's own access, the same call the app makes."""

import logging
from dataclasses import dataclass
from typing import Optional, Tuple
from uuid import UUID

import requests
from sqlalchemy.orm import Session

from notifications_server import sync_engine
from notifications_server.configs.settings import settings
from notifications_server.repositories.user_repository import get_user_tenants

LOG = logging.getLogger(__name__)

TOKEN_PREFIX = "auto_optimize:"
DECISIONS = {"approve": "APPROVED", "reject": "REJECTED"}

NOT_A_USER = (
    "No NudgeBee user in this workspace matches your email, so this approval can't be decided from here."
    " Review it in NudgeBee."
)
NO_ACCESS = "You need edit access to this account in NudgeBee to approve or reject its changes."
NOT_FOUND = "This approval no longer exists."
FAILED = "Couldn't record the decision. Please try again, or review it in NudgeBee."


@dataclass
class Outcome:
    message: str
    # True when the approval is settled (decided now or earlier, expired, gone),
    # so its buttons should be removed.
    settled: bool


def is_auto_optimize_token(token) -> bool:
    return isinstance(token, str) and token.startswith(TOKEN_PREFIX)


def parse_token(token) -> Optional[Tuple[str, str, str]]:
    """(approval_id, account_id, tenant_id), or None when the token isn't one."""
    if not is_auto_optimize_token(token):
        return None
    parts = token[len(TOKEN_PREFIX) :].split(":")
    if len(parts) != 3:
        return None
    try:
        return tuple(str(UUID(p)) for p in parts)
    except ValueError:
        return None


def _user_in_tenant(user_email, tenant_id) -> Optional[str]:
    if not user_email:
        return None
    with Session(sync_engine) as session:
        user_id, tenants = get_user_tenants(session, user_email.strip())
    if not user_id or tenant_id not in {str(t) for t in tenants or []}:
        return None
    return str(user_id)


def decide(token, option, user_email) -> Outcome:
    """Records the clicker's decision and says what happened, in words to show them."""
    parsed = parse_token(token)
    decision = DECISIONS.get(str(option or "").lower())
    if not (parsed and decision):
        LOG.warning("Rejecting an Auto Optimize approval click with invalid data")
        return Outcome(FAILED, False)
    approval_id, account_id, tenant_id = parsed

    try:
        user_id = _user_in_tenant(user_email, tenant_id)
    except Exception as e:
        LOG.error("Looking up the user for an Auto Optimize approval failed: %s", e)
        return Outcome(FAILED, False)
    if not user_id:
        return Outcome(NOT_A_USER, False)

    if not settings.workflow_server_token:
        LOG.error("WORKFLOW_SERVER_TOKEN is not set; Auto Optimize approvals can't be decided from chat")
        return Outcome(FAILED, False)

    try:
        response = requests.post(
            f"{settings.services.workflow_server.rstrip('/')}/autopilot/approval",
            json={"input": {"id": approval_id, "account_id": account_id, "status": decision}},
            headers={
                "X-ACTION-TOKEN": settings.workflow_server_token,
                "x-tenant-id": tenant_id,
                "x-user-id": user_id,
            },
            timeout=30,
        )
    except requests.RequestException as e:
        LOG.warning("Deciding an Auto Optimize approval failed: %s", e)
        return Outcome(FAILED, False)

    try:
        body = response.json() or {}
    except ValueError:
        body = {}
    message = body.get("message") if isinstance(body, dict) else None

    if response.status_code == 200:
        return Outcome(message or "Done.", True)
    if response.status_code == 409:
        # Already decided, expired or cancelled; the server's sentence says which.
        return Outcome(message or "This approval was already decided or has expired.", True)
    if response.status_code == 404:
        return Outcome(NOT_FOUND, True)
    if response.status_code in (401, 403):
        LOG.info("Auto Optimize approval refused for user %s: %s", user_id, response.text[:200])
        return Outcome(NO_ACCESS, False)
    LOG.warning("Deciding an Auto Optimize approval returned %s: %s", response.status_code, response.text[:500])
    return Outcome(FAILED, False)
