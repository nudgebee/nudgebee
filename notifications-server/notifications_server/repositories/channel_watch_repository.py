import logging
from typing import List, Optional
from uuid import UUID

from sqlalchemy.orm import Session

from notifications_server.models.models import MessagingChannelWatch
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)


def _to_uuid(tenant_id):
    return UUID(tenant_id) if isinstance(tenant_id, str) else tenant_id


def _to_dict(watch: MessagingChannelWatch) -> dict:
    return {
        "id": str(watch.id),
        "tenant_id": str(watch.tenant_id),
        "platform": watch.platform,
        "team_id": watch.team_id,
        "channel_id": watch.channel_id,
        "channel_name": watch.channel_name,
        "enabled": watch.enabled,
        "retention_days": watch.retention_days,
        "created_by": watch.created_by,
        "created_at": watch.created_at.isoformat() if watch.created_at else None,
        "updated_at": watch.updated_at.isoformat() if watch.updated_at else None,
        "disabled_at": watch.disabled_at.isoformat() if watch.disabled_at else None,
    }


def list_channel_watches(session: Session, tenant_id) -> Optional[List[dict]]:
    """None on DB error — an empty list means "no watches", and callers must be
    able to tell the two apart rather than render consent state as all-off."""
    try:
        rows = (
            session.query(MessagingChannelWatch)
            .filter(MessagingChannelWatch.tenant_id == _to_uuid(tenant_id))
            .order_by(MessagingChannelWatch.created_at)
            .all()
        )
        return [_to_dict(row) for row in rows]
    except Exception as e:
        LOG.error("Failed to list channel watches for tenant %s: %s", tenant_id, e)
        return None


def upsert_channel_watch(
    session: Session,
    *,
    tenant_id,
    platform: str,
    team_id: str,
    channel_id: str,
    channel_name: Optional[str] = None,
    created_by: Optional[str] = None,
) -> Optional[dict]:
    """Create the watch row, or re-enable an existing one (clearing disabled_at)."""
    try:
        watch = (
            session.query(MessagingChannelWatch)
            .filter(
                MessagingChannelWatch.tenant_id == _to_uuid(tenant_id),
                MessagingChannelWatch.platform == platform,
                MessagingChannelWatch.team_id == team_id,
                MessagingChannelWatch.channel_id == channel_id,
            )
            .first()
        )
        if watch:
            watch.enabled = True
            watch.disabled_at = None
            watch.updated_at = utc_now()
            if channel_name:
                watch.channel_name = channel_name
        else:
            watch = MessagingChannelWatch(
                tenant_id=_to_uuid(tenant_id),
                platform=platform,
                team_id=team_id,
                channel_id=channel_id,
                channel_name=channel_name,
                created_by=created_by,
            )
            session.add(watch)
        session.commit()
        return _to_dict(watch)
    except Exception as e:
        session.rollback()
        LOG.error("Failed to upsert channel watch %s/%s for tenant %s: %s", team_id, channel_id, tenant_id, e)
        return None


def disable_channel_watch(
    session: Session,
    *,
    tenant_id,
    platform: str,
    channel_id: str,
    team_id: Optional[str] = None,
) -> Optional[dict]:
    try:
        query = session.query(MessagingChannelWatch).filter(
            MessagingChannelWatch.tenant_id == _to_uuid(tenant_id),
            MessagingChannelWatch.platform == platform,
            MessagingChannelWatch.channel_id == channel_id,
        )
        if team_id:
            query = query.filter(MessagingChannelWatch.team_id == team_id)
        watch = query.first()
        if not watch:
            return None
        watch.enabled = False
        watch.disabled_at = utc_now()
        watch.updated_at = utc_now()
        session.commit()
        return _to_dict(watch)
    except Exception as e:
        session.rollback()
        LOG.error("Failed to disable channel watch %s for tenant %s: %s", channel_id, tenant_id, e)
        return None


def list_enabled_channel_ids(session: Session, *, platform: str, team_id: str) -> List[str]:
    """Enabled channel ids for one workspace — used to (re)seed the Redis mirror."""
    try:
        rows = (
            session.query(MessagingChannelWatch.channel_id)
            .filter(
                MessagingChannelWatch.platform == platform,
                MessagingChannelWatch.team_id == team_id,
                MessagingChannelWatch.enabled.is_(True),
            )
            .all()
        )
        return [row[0] for row in rows]
    except Exception as e:
        LOG.error("Failed to list enabled channel watches for team %s: %s", team_id, e)
        return []
