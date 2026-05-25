from sqlalchemy.orm import Session

from notifications_server.repositories.user_repository import get_user_by_email


def _engine():
    # Lazy import to avoid a circular dependency at package-init time.
    from notifications_server import sync_engine

    return sync_engine


def get_user_id_by_email(email):
    with Session(_engine()) as session:
        user = get_user_by_email(session, email)
    if not user:
        return None
    return user.get("id")
