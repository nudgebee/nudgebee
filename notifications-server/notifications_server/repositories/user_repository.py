import logging
from typing import List, Optional, Tuple

from sqlalchemy.orm import Session

from notifications_server.models.models import User, TenantUser, LlmConversation

LOG = logging.getLogger(__name__)


def get_user_tenants(session: Session, user_email: str) -> Tuple[Optional[str], Optional[List[str]]]:
    """
    Get user ID and tenant list by email.
    Replaces RPC_FETCH_USER_TENANTS query.

    Args:
        session: SQLAlchemy session
        user_email: User's email address

    Returns:
        Tuple of (user_id, list of tenant_ids) or (None, None) if not found
    """
    try:
        tenant_users = (
            session.query(TenantUser).join(User, TenantUser.user == User.id).filter(User.username == user_email).all()
        )

        if not tenant_users:
            return None, None

        tenant_list = [str(tu.tenant) for tu in tenant_users]
        user_id = str(tenant_users[0].user)
        return user_id, tenant_list
    except Exception as e:
        LOG.error("Failed to fetch user tenants for %s: %s", user_email, e)
        return None, None


def get_user_by_email(session: Session, user_email: str) -> Optional[dict]:
    """
    Get user by email.
    Replaces RPC_FETCH_USER_BY_EMAIL query.

    Args:
        session: SQLAlchemy session
        user_email: User's email address

    Returns:
        Dict with 'id', 'username', 'display_name' keys or None
    """
    try:
        user = session.query(User).filter(User.username == user_email.strip()).first()

        if not user:
            return None

        return {
            "id": str(user.id),
            "username": user.username,
            "display_name": user.display_name,
        }
    except Exception as e:
        LOG.error("Failed to fetch user by email %s: %s", user_email, e)
        return None


def get_llm_conversation_by_session(session: Session, session_id: str) -> Optional[dict]:
    """
    Get LLM conversation by session ID.
    Replaces RPC_GET_LLM_CONVERSATION_BY_SESSION query.

    Args:
        session: SQLAlchemy session
        session_id: Session identifier

    Returns:
        Dict with conversation details or None
    """
    try:
        conversation = session.query(LlmConversation).filter(LlmConversation.session_id == session_id).first()

        if not conversation:
            return None

        return {
            "id": str(conversation.id),
            "user_id": str(conversation.user_id) if conversation.user_id else None,
            "session_id": conversation.session_id,
            "account_id": str(conversation.account_id) if conversation.account_id else None,
            "tenant_id": str(conversation.tenant_id) if conversation.tenant_id else None,
        }
    except Exception as e:
        LOG.error("Failed to fetch LLM conversation by session %s: %s", session_id, e)
        return None
