import logging
from typing import List, Optional

from sqlalchemy.orm import Session

from notifications_server.models.models import CloudAccount, Agent

LOG = logging.getLogger(__name__)


def get_active_accounts_with_connected_agents(session: Session, tenant_ids: List[str]) -> List[dict]:
    """
    Get cloud accounts that are active and have at least one connected agent.
    Replaces RPC_ACCOUNT_LIST query.

    Args:
        session: SQLAlchemy session
        tenant_ids: List of tenant UUIDs to filter by

    Returns:
        List of dicts with 'id' and 'account_name' keys
    """
    try:
        accounts = (
            session.query(CloudAccount)
            .join(Agent, CloudAccount.id == Agent.cloud_account_id)
            .filter(
                CloudAccount.tenant.in_(tenant_ids),
                CloudAccount.status == "active",
                Agent.status == "CONNECTED",
            )
            .distinct()
            .all()
        )

        return [{"id": str(acc.id), "account_name": acc.account_name} for acc in accounts]
    except Exception as e:
        LOG.error("Failed to fetch active accounts with connected agents: %s", e)
        return []


def get_account_by_id(session: Session, account_id: str) -> Optional[dict]:
    """
    Get cloud account by ID.
    Replaces RPC_FETCH_ACCOUNT_BY_ID query.

    Args:
        session: SQLAlchemy session
        account_id: Account UUID

    Returns:
        Dict with 'id', 'account_name', 'tenant' keys or None
    """
    try:
        account = session.query(CloudAccount).filter(CloudAccount.id == account_id).first()

        if not account:
            return None

        return {
            "id": str(account.id),
            "account_name": account.account_name,
            "tenant": str(account.tenant),
        }
    except Exception as e:
        LOG.error("Failed to fetch account by id %s: %s", account_id, e)
        return None


def get_account_by_name(session: Session, account_name: str) -> Optional[dict]:
    """
    Get cloud account by name (case-insensitive like match).
    Replaces RPC_FETCH_ACCOUNT query.

    Args:
        session: SQLAlchemy session
        account_name: Account name to search for

    Returns:
        Dict with 'id', 'account_name', 'tenant' keys or None
    """
    try:
        account = session.query(CloudAccount).filter(CloudAccount.account_name.ilike(account_name)).first()

        if not account:
            return None

        return {
            "id": str(account.id),
            "account_name": account.account_name,
            "tenant": str(account.tenant),
        }
    except Exception as e:
        LOG.error("Failed to fetch account by name %s: %s", account_name, e)
        return None
