import logging
from typing import Optional, List

from sqlalchemy.orm import Session

from notifications_server.models.models import MessagingPlatform, SlackOAuthState

LOG = logging.getLogger(__name__)


def find_installation_by_tenant_and_platform(session: Session, tenant_id: str, platform: str) -> List[dict]:
    """
    Find messaging platform installations for a tenant and platform.
    Replaces RPC_FIND_INSTALLATION_BY_ID query.

    Args:
        session: SQLAlchemy session
        tenant_id: Tenant UUID
        platform: Platform type ('slack', 'ms_teams', 'google_chat')

    Returns:
        List of dicts with 'id' key for each installation
    """
    try:
        installations = (
            session.query(MessagingPlatform)
            .filter(
                MessagingPlatform.tenant_id == tenant_id,
                MessagingPlatform.platform == platform,
            )
            .all()
        )

        result = [{"id": str(inst.id)} for inst in installations]
        session.commit()
        return result
    except Exception as e:
        LOG.error("Failed to find installation for tenant %s, platform %s: %s", tenant_id, platform, e)
        session.rollback()
        return []


def get_state_tenant(session: Session, state: str) -> Optional[str]:
    """
    Get tenant_id from slack_oauth_states by state.
    Replaces RPC_GET_STATE_TENANT query.

    Args:
        session: SQLAlchemy session
        state: OAuth state string

    Returns:
        tenant_id string or None
    """
    try:
        oauth_state = session.query(SlackOAuthState).filter(SlackOAuthState.state == state).first()

        if not oauth_state:
            return None

        return str(oauth_state.tenant_id) if oauth_state.tenant_id else None
    except Exception as e:
        LOG.error("Failed to get state tenant for state %s: %s", state, e)
        return None


def update_state_tenant(session: Session, state: str, tenant_id: str) -> bool:
    """
    Update slack_oauth_states to set tenant_id for a given state.
    Replaces RPC_UPDATE_STATE_TENANT mutation.

    Args:
        session: SQLAlchemy session
        state: OAuth state string
        tenant_id: Tenant UUID to set

    Returns:
        True if update succeeded, False otherwise
    """
    try:
        result = session.query(SlackOAuthState).filter(SlackOAuthState.state == state).update({"tenant_id": tenant_id})
        session.commit()
        return result > 0
    except Exception as e:
        LOG.error("Failed to update state tenant for state %s: %s", state, e)
        session.rollback()
        return False
