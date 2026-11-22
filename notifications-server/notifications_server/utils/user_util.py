from typing import Optional

from sqlalchemy.engine import Engine
from sqlalchemy.orm import Session

from notifications_server.repositories.user_repository import get_user_by_email


def _engine() -> Engine:
    # Lazy import to avoid a circular dependency at package-init time.
    from notifications_server import sync_engine

    return sync_engine


def get_user_by_email_address(email: str) -> Optional[dict]:
    """The user's id, username and display name, or None."""
    with Session(_engine()) as session:
        return get_user_by_email(session, email)


def get_user_id_by_email(email: str) -> Optional[str]:
    user = get_user_by_email_address(email)
    if not user:
        return None
    return user.get("id")
