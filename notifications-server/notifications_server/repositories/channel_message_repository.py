import logging
from datetime import timedelta
from typing import List, Optional
from uuid import UUID

from sqlalchemy import and_, func, text
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.orm import Session

from notifications_server.models.models import MessagingChannelMessage, MessagingChannelWatch
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)


def _to_uuid(value):
    return UUID(value) if isinstance(value, str) else value


def list_watching_tenants(session: Session, *, platform: str, team_id: str, channel_id: str) -> List[str]:
    """Tenants that have consented to watching this channel. A channel can be
    watched by more than one tenant, so every one gets its own copy."""
    try:
        rows = (
            session.query(MessagingChannelWatch.tenant_id)
            .filter(
                MessagingChannelWatch.platform == platform,
                MessagingChannelWatch.team_id == team_id,
                MessagingChannelWatch.channel_id == channel_id,
                MessagingChannelWatch.enabled.is_(True),
            )
            .all()
        )
        return [str(row[0]) for row in rows]
    except Exception as e:
        LOG.error("Failed to resolve watching tenants for %s/%s: %s", team_id, channel_id, e)
        return []


def store_message(
    session: Session,
    *,
    tenant_id,
    platform: str,
    team_id: str,
    channel_id: str,
    provider_message_id: str,
    message: str,
    posted_at,
    thread_id: Optional[str] = None,
    author_id: Optional[str] = None,
    author_name: Optional[str] = None,
) -> bool:
    """Insert one retained message, or update it in place when the same provider
    message arrives again (Slack retries, and edits reuse the original id)."""
    try:
        stmt = (
            pg_insert(MessagingChannelMessage.__table__)
            .values(
                tenant_id=_to_uuid(tenant_id),
                platform=platform,
                team_id=team_id,
                channel_id=channel_id,
                thread_id=thread_id,
                provider_message_id=provider_message_id,
                author_id=author_id,
                author_name=author_name,
                message=message,
                posted_at=posted_at,
            )
            .on_conflict_do_update(
                index_elements=["tenant_id", "platform", "team_id", "channel_id", "provider_message_id"],
                set_={"message": message, "updated_at": utc_now()},
            )
        )
        session.execute(stmt)
        session.commit()
        return True
    except Exception as e:
        session.rollback()
        LOG.error("Failed to store channel message %s/%s: %s", channel_id, provider_message_id, e)
        return False


def delete_message(session: Session, *, platform: str, team_id: str, channel_id: str, provider_message_id: str) -> int:
    """Remove every tenant's copy of a message the user deleted in the provider.
    Retaining content someone explicitly removed is not acceptable, so this is
    intentionally unscoped by tenant."""
    try:
        deleted = (
            session.query(MessagingChannelMessage)
            .filter(
                MessagingChannelMessage.platform == platform,
                MessagingChannelMessage.team_id == team_id,
                MessagingChannelMessage.channel_id == channel_id,
                MessagingChannelMessage.provider_message_id == provider_message_id,
            )
            .delete(synchronize_session=False)
        )
        session.commit()
        return deleted
    except Exception as e:
        session.rollback()
        LOG.error("Failed to delete channel message %s/%s: %s", channel_id, provider_message_id, e)
        return 0


def count_recent_messages(session: Session, *, platform: str, team_id: str, channel_id: str, minutes: int) -> int:
    """Rows stored for this channel in the recent window — drives the volume
    circuit breaker that stops a firehose channel dominating the table."""
    try:
        since = utc_now() - timedelta(minutes=minutes)
        return (
            session.query(func.count(MessagingChannelMessage.id))
            .filter(
                MessagingChannelMessage.platform == platform,
                MessagingChannelMessage.team_id == team_id,
                MessagingChannelMessage.channel_id == channel_id,
                MessagingChannelMessage.created_at >= since,
            )
            .scalar()
            or 0
        )
    except Exception as e:
        LOG.error("Failed to count recent messages for %s/%s: %s", team_id, channel_id, e)
        return 0


def delete_expired_messages(session: Session, *, batch_limit: int = 5000, max_batches: int = 20) -> int:
    """Delete messages older than their own channel's retention window.

    Expiry is evaluated inside the batch selection, not applied to it: retention
    varies per channel, so the globally-oldest rows are not necessarily expired.
    Selecting oldest-first and filtering afterwards lets a long-retention channel
    fill every batch with unexpired rows and starve expired rows elsewhere
    indefinitely. Batches are committed individually and capped per run so a
    backlog drains over successive sweeps without holding a long transaction.
    """
    total_deleted = 0
    stmt = text("""
        DELETE FROM messaging_channel_message
        WHERE id IN (
            SELECT m.id
            FROM messaging_channel_message m
            JOIN messaging_channel_watch w
              ON m.tenant_id = w.tenant_id
             AND m.platform = w.platform
             AND m.team_id = w.team_id
             AND m.channel_id = w.channel_id
            WHERE m.posted_at < now() - make_interval(days => w.retention_days)
            ORDER BY m.posted_at
            LIMIT :batch_limit
        )
        """)
    try:
        for _ in range(max_batches):
            result = session.execute(stmt, {"batch_limit": batch_limit})
            session.commit()
            deleted = result.rowcount or 0
            total_deleted += deleted
            if deleted < batch_limit:
                break
        return total_deleted
    except Exception as e:
        session.rollback()
        LOG.error("Retention sweep failed after %d deletions: %s", total_deleted, e)
        return total_deleted


def delete_channel_messages(session: Session, *, tenant_id, platform: str, team_id: str, channel_id: str) -> int:
    """Forget everything retained for one channel in one tenant."""
    try:
        deleted = (
            session.query(MessagingChannelMessage)
            .filter(
                and_(
                    MessagingChannelMessage.tenant_id == _to_uuid(tenant_id),
                    MessagingChannelMessage.platform == platform,
                    MessagingChannelMessage.team_id == team_id,
                    MessagingChannelMessage.channel_id == channel_id,
                )
            )
            .delete(synchronize_session=False)
        )
        session.commit()
        return deleted
    except Exception as e:
        session.rollback()
        LOG.error("Failed to forget channel %s/%s: %s", team_id, channel_id, e)
        return 0
