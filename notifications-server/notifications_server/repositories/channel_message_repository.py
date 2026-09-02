import logging
from datetime import timedelta
from typing import List, Optional
from uuid import UUID

from sqlalchemy import and_, func, or_, text
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
    is_decision: bool = False,
    topic: Optional[str] = None,
    people_mentioned: Optional[List[str]] = None,
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
                is_decision=is_decision,
                topic=topic,
                people_mentioned=people_mentioned,
            )
            .on_conflict_do_update(
                index_elements=["tenant_id", "platform", "team_id", "channel_id", "provider_message_id"],
                set_={
                    "message": message,
                    "updated_at": utc_now(),
                    # An edit can change what the message says, so its tags are
                    # re-derived from the new text rather than left stale.
                    "is_decision": is_decision,
                    "topic": topic,
                    "people_mentioned": people_mentioned,
                },
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


def _row_to_dict(row) -> dict:
    return {
        "provider_message_id": row.provider_message_id,
        "author_id": row.author_id,
        "author_name": row.author_name,
        "message": row.message,
        "posted_at": row.posted_at,
        "thread_id": row.thread_id,
        "is_decision": bool(row.is_decision),
        "topic": row.topic,
    }


def _scoped(query, tenant_id, platform, team_id, channel_id, exclude_thread_id):
    """Every read is tenant-scoped. channel_key narrows, tenant_id authorizes."""
    query = query.filter(
        MessagingChannelMessage.tenant_id == _to_uuid(tenant_id),
        MessagingChannelMessage.platform == platform,
        MessagingChannelMessage.team_id == team_id,
        MessagingChannelMessage.channel_id == channel_id,
    )
    if exclude_thread_id:
        # The mentioned thread's own transcript is fetched separately from the
        # provider; including it here would duplicate it in the prompt.
        query = query.filter(
            or_(
                MessagingChannelMessage.thread_id.is_(None),
                MessagingChannelMessage.thread_id != exclude_thread_id,
            )
        )
    return query


def list_recent_messages(
    session: Session,
    *,
    tenant_id,
    platform: str,
    team_id: str,
    channel_id: str,
    limit: int = 50,
    exclude_thread_id: Optional[str] = None,
    since=None,
) -> List[dict]:
    """Most recent retained messages, oldest-first for reading.

    ``since`` bounds the window by age. Without it a channel that was busy last
    week and quiet since would return week-old chatter under the heading "recent
    conversation" — technically the newest rows, but a lie to the reader.
    """
    try:
        query = _scoped(
            session.query(MessagingChannelMessage), tenant_id, platform, team_id, channel_id, exclude_thread_id
        )
        if since is not None:
            query = query.filter(MessagingChannelMessage.posted_at >= since)
        rows = query.order_by(MessagingChannelMessage.posted_at.desc()).limit(limit).all()
        return [_row_to_dict(row) for row in reversed(rows)]
    except Exception as e:
        LOG.error("Failed to list recent messages for %s/%s: %s", team_id, channel_id, e)
        return []


def list_thread_messages(
    session: Session,
    *,
    tenant_id,
    platform: str,
    team_id: str,
    channel_id: str,
    thread_id: str,
    limit: int = 10,
) -> List[dict]:
    """The thread root plus the newest replies, oldest-first, at most ``limit``.

    The root is fetched separately rather than left to compete in the recency
    window it cannot win: it is the oldest message in the thread, so a single
    ORDER BY posted_at DESC LIMIT would evict it from any thread longer than
    ``limit`` — exactly the message that frames what the thread is about. It is
    also stored with a null thread_id, since it is not itself a reply.

    At ``limit`` 1 the root is the one message kept; it carries more of the
    topic than any single reply does.
    """
    try:
        base = _scoped(session.query(MessagingChannelMessage), tenant_id, platform, team_id, channel_id, None)
        root = base.filter(MessagingChannelMessage.provider_message_id == thread_id).first()
        reply_budget = limit - 1 if root else limit
        replies = []
        if reply_budget > 0:
            replies = (
                base.filter(
                    MessagingChannelMessage.thread_id == thread_id,
                    MessagingChannelMessage.provider_message_id != thread_id,
                )
                .order_by(MessagingChannelMessage.posted_at.desc())
                .limit(reply_budget)
                .all()
            )
        rows = ([root] if root else []) + list(reversed(replies))
        return [_row_to_dict(row) for row in rows]
    except Exception as e:
        LOG.error("Failed to list thread messages for %s/%s: %s", team_id, thread_id, e)
        return []


def list_by_ids(
    session: Session, *, tenant_id, platform: str, team_id: str, channel_id: str, message_ids: List[str]
) -> List[dict]:
    """Re-read a specific set of messages, oldest-first. Still tenant-scoped:
    the id set came from Nubi's own prior read, but authorisation is re-checked
    rather than inherited."""
    if not message_ids:
        return []
    try:
        query = _scoped(session.query(MessagingChannelMessage), tenant_id, platform, team_id, channel_id, None).filter(
            MessagingChannelMessage.provider_message_id.in_(list(message_ids))
        )
        rows = query.order_by(MessagingChannelMessage.posted_at.asc()).all()
        return [_row_to_dict(row) for row in rows]
    except Exception as e:
        LOG.error("Failed to re-read messages for %s/%s: %s", team_id, channel_id, e)
        return []


def list_by_author(
    session: Session,
    *,
    tenant_id,
    platform: str,
    team_id: str,
    channel_id: str,
    author_ids: List[str],
    topic: Optional[str] = None,
    limit: int = 3,
    exclude_thread_id: Optional[str] = None,
) -> List[dict]:
    """Newest messages from named people, optionally narrowed to a topic.

    Answers "what did @john say about pricing". Falls back to the author's
    newest messages when the topic filter matches nothing, since the asker
    clearly wants that person's words either way.
    """
    if not author_ids:
        return []
    try:
        base = _scoped(
            session.query(MessagingChannelMessage), tenant_id, platform, team_id, channel_id, exclude_thread_id
        ).filter(MessagingChannelMessage.author_id.in_(list(author_ids)))
        rows = []
        if topic:
            rows = (
                base.filter(MessagingChannelMessage.topic == topic)
                .order_by(MessagingChannelMessage.posted_at.desc())
                .limit(limit)
                .all()
            )
        if not rows:
            rows = base.order_by(MessagingChannelMessage.posted_at.desc()).limit(limit).all()
        return [_row_to_dict(row) for row in reversed(rows)]
    except Exception as e:
        LOG.error("Failed to list author messages for %s/%s: %s", team_id, channel_id, e)
        return []


def count_replies(
    session: Session, *, tenant_id, platform: str, team_id: str, channel_id: str, thread_ids: List[str]
) -> dict:
    """Reply count per thread, for ranking. Derived on read rather than stored:
    a counter column would cost an extra write on every threaded message to
    reproduce a number this query gets in one pass."""
    wanted = [thread_id for thread_id in set(thread_ids or ()) if thread_id]
    if not wanted:
        return {}
    try:
        rows = (
            _scoped(
                session.query(MessagingChannelMessage.thread_id, func.count()),
                tenant_id,
                platform,
                team_id,
                channel_id,
                None,
            )
            .filter(MessagingChannelMessage.thread_id.in_(wanted))
            .group_by(MessagingChannelMessage.thread_id)
            .all()
        )
        return {thread_id: count for thread_id, count in rows}
    except Exception as e:
        LOG.error("Failed to count replies for %s/%s: %s", team_id, channel_id, e)
        return {}


def search_messages(
    session: Session,
    *,
    tenant_id,
    platform: str,
    team_id: str,
    channel_id: str,
    query_text: str,
    limit: int = 15,
    exclude_thread_id: Optional[str] = None,
) -> List[dict]:
    """Keyword lookup over retained conversation. Exact-word matching via Postgres
    full-text search — it does not match paraphrases, so it complements the recent
    window rather than replacing semantic search."""
    if not (query_text or "").strip():
        return []
    try:
        # Must mirror the GIN index expression exactly to use the index.
        vector = func.to_tsvector("english", MessagingChannelMessage.message)
        # websearch_to_tsquery sanitises arbitrary user text, but ANDs every term:
        # a natural question ("what did we decide about the standby") would then
        # require a message to contain all of its words, which essentially never
        # matches. Rewriting & to | gives OR semantics, and ts_rank still floats
        # the messages hitting the most terms to the top. NULLIF collapses a
        # stopword-only question to NULL so it matches nothing rather than
        # everything.
        tsquery = text(
            "NULLIF(replace(websearch_to_tsquery('english', :fts_query)::text, '&', '|'), '')::tsquery"
        ).bindparams(fts_query=query_text)
        query = _scoped(
            session.query(MessagingChannelMessage), tenant_id, platform, team_id, channel_id, exclude_thread_id
        ).filter(vector.op("@@")(tsquery))
        rows = (
            query.order_by(func.ts_rank(vector, tsquery).desc(), MessagingChannelMessage.posted_at.desc())
            .limit(limit)
            .all()
        )
        return [_row_to_dict(row) for row in sorted(rows, key=lambda r: r.posted_at)]
    except Exception as e:
        LOG.error("Keyword search failed for %s/%s: %s", team_id, channel_id, e)
        return []


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
