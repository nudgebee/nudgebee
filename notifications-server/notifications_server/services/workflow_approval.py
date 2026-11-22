"""Approve / Reject on an automation's approval card, on Slack and Microsoft Teams.

The card's token (``<task token>:<account_id>:<run>:<execution>:<step>``) names
the approval; it is not a credential. A click is recorded by the workflow
server as the clicking user, who must be a user of the account's tenant and
allowed to approve automations in the app."""

import logging
from enum import Enum
from typing import Optional

import requests
from sqlalchemy.orm import Session

from notifications_server import sync_engine
from notifications_server.configs.settings import settings
from notifications_server.repositories.account_repository import get_account_by_id
from notifications_server.repositories.user_repository import get_user_tenants

LOG = logging.getLogger(__name__)


class Decision(Enum):
    DECIDED = "decided"
    # Already decided, or the run has ended.
    ANSWERED = "answered"
    NOT_FOUND = "not_found"
    INVALID = "invalid"
    NOT_A_USER = "not_a_user"
    NO_ACCESS = "no_access"
    FAILED = "failed"


def not_a_user_message() -> str:
    return (
        f"No {settings.urls.branding_name} user in this workspace matches your email, so this approval"
        f" can't be decided from here. Review it in {settings.urls.branding_name}."
    )


def no_access_message() -> str:
    return (
        f"You need edit access to this account in {settings.urls.branding_name} to approve or reject this automation."
    )


def account_of(token) -> Optional[str]:
    parts = token.split(":") if isinstance(token, str) else []
    if len(parts) != 5 or not all(parts):
        return None
    return parts[1]


def _user_in_account_tenant(user_email, account_id):
    """(user_id, tenant_id) when the user belongs to the account's tenant."""
    with Session(sync_engine) as session:
        account = get_account_by_id(session, account_id)
        if not account:
            return None, None
        user_id, tenants = get_user_tenants(session, user_email.strip())
    tenant_id = account["tenant"]
    if not user_id or tenant_id not in {str(t) for t in tenants or []}:
        return None, tenant_id
    return str(user_id), tenant_id


def decide(token, status, user_email) -> Decision:
    """Records the clicker's decision on the approval the token names."""
    account_id = account_of(token)
    if not (account_id and status):
        LOG.warning("Rejecting an approval click with invalid data")
        return Decision.INVALID
    if not user_email:
        return Decision.NOT_A_USER

    try:
        user_id, tenant_id = _user_in_account_tenant(user_email, account_id)
    except Exception as e:
        LOG.error("Looking up the user for an approval failed: %s", e)
        return Decision.FAILED
    if not tenant_id:
        # The lookup returns nothing on a database error too; a retry is the
        # safe answer, where "not found" would retire the card.
        LOG.warning("No account found for an approval click on account %s", account_id)
        return Decision.FAILED
    if not user_id:
        return Decision.NOT_A_USER

    if not settings.workflow_server_token:
        LOG.error("WORKFLOW_SERVER_TOKEN is not set; approvals can't be decided from chat")
        return Decision.FAILED

    try:
        response = requests.post(
            f"{settings.services.workflow_server.rstrip('/')}/rpc",
            json={
                "action": {"name": "workflow_complete_approval"},
                "input": {"input": {"account_id": account_id, "token": token, "status": status}},
            },
            headers={
                "X-ACTION-TOKEN": settings.workflow_server_token,
                "x-tenant-id": tenant_id,
                "x-user-id": user_id,
            },
            timeout=30,
        )
    except requests.RequestException as e:
        LOG.warning("Recording an approval decision failed: %s", e)
        return Decision.FAILED

    code = response.status_code
    if code == 200:
        return Decision.DECIDED
    if code == 409:
        return Decision.ANSWERED
    if code == 404:
        return Decision.NOT_FOUND
    if code == 403:
        LOG.info("Approval refused for user %s: %s", user_id, response.text[:200])
        return Decision.NO_ACCESS
    if code == 400:
        LOG.warning("Approval decision rejected as invalid: %s", response.text[:500])
        return Decision.INVALID
    LOG.warning("Recording an approval decision returned %s: %s", code, response.text[:500])
    return Decision.FAILED
