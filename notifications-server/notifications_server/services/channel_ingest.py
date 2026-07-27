import logging
from datetime import datetime, timezone

from notifications_server.configs.settings import settings
from notifications_server.models.db_base import BaseDB
from notifications_server.repositories import channel_message_repository
from notifications_server.services import channel_analysis
from notifications_server.services.cache import Cache
from notifications_server.utils.secret_redaction import redact_secrets

LOG = logging.getLogger(__name__)

cache = Cache()

PLATFORM_SLACK = "slack"

# Subtypes that carry no conversation worth retaining (joins, topic changes,
# pins, bot posts). message_changed / message_deleted are handled explicitly and
# are deliberately not in this set.
_IGNORED_SUBTYPES = {
    "bot_message",
    "channel_join",
    "channel_leave",
    "channel_topic",
    "channel_purpose",
    "channel_name",
    "channel_archive",
    "channel_unarchive",
    "pinned_item",
    "unpinned_item",
    "file_comment",
    "message_replied",
}


def _slack_ts_to_datetime(ts):
    try:
        return datetime.fromtimestamp(float(ts), tz=timezone.utc).replace(tzinfo=None)
    except (TypeError, ValueError):
        return None


class ChannelIngestService:
    """Retains conversation from watched channels. Nothing here ever triggers an
    agent run — ingested content is reference material only, and only an explicit
    mention causes Nubi to act."""

    def __init__(self, engine):
        self.engine = engine
        self._scoped_session = BaseDB.session(self.engine)
        self.session = self._scoped_session()

    def close(self):
        try:
            self._scoped_session.remove()
        except Exception:
            pass

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
        return False

    def is_watched(self, team_id, channel_id, platform=PLATFORM_SLACK):
        """Fast negative filter for the hot path. The Redis mirror answers for
        the overwhelming majority of traffic; None means Redis is unavailable, so
        fall back to the registry rather than silently ingesting or dropping."""
        mirrored = cache.is_channel_watched(platform, team_id, channel_id)
        if mirrored is not None:
            return mirrored
        return bool(
            channel_message_repository.list_watching_tenants(
                self.session, platform=platform, team_id=team_id, channel_id=channel_id
            )
        )

    def handle_message_event(self, event, team_id, event_id, platform=PLATFORM_SLACK):
        """Entry point for a channel message event. Returns True when something
        was retained, changed or removed; False for the (vast majority) dropped."""
        if not settings.notifications.channel_awareness_enabled:
            return False

        channel_id = event.get("channel")
        if not channel_id:
            return False

        subtype = event.get("subtype")

        if subtype == "message_deleted":
            return self._handle_deleted(event, team_id, channel_id, platform)

        inner = event.get("message") if subtype == "message_changed" else event
        if subtype == "message_changed":
            subtype = (inner or {}).get("subtype")

        if not inner:
            return False
        if subtype in _IGNORED_SUBTYPES or inner.get("bot_id") or inner.get("subtype") == "bot_message":
            return False
        # A message edited into emptiness should remove the retained copy, not
        # leave the pre-edit text behind.
        text_value = (inner.get("text") or "").strip()
        provider_message_id = inner.get("ts")
        if not provider_message_id:
            return False

        if not self.is_watched(team_id, channel_id, platform):
            return False

        # Slack retries deliver the same event id; dedup before doing work.
        if event_id and not cache.mark_event_seen(event_id):
            return False

        if not text_value:
            return bool(
                channel_message_repository.delete_message(
                    self.session,
                    platform=platform,
                    team_id=team_id,
                    channel_id=channel_id,
                    provider_message_id=provider_message_id,
                )
            )

        if self._over_volume_limit(platform, team_id, channel_id):
            return False

        tenants = channel_message_repository.list_watching_tenants(
            self.session, platform=platform, team_id=team_id, channel_id=channel_id
        )
        if not tenants:
            return False

        redacted = redact_secrets(text_value)
        posted_at = _slack_ts_to_datetime(provider_message_id)
        if posted_at is None:
            return False
        thread_id = inner.get("thread_ts")

        # Tagged once here rather than per question: retrieval ranks and filters
        # on these, and re-deriving them at mention time would repeat the work on
        # every read. Keyword lexicons only — no model call on the ingest path.
        is_decision = channel_analysis.classify_decision(redacted)
        topic = channel_analysis.classify_topic(redacted)
        people_mentioned = channel_analysis.extract_people(redacted)

        stored = False
        for tenant_id in tenants:
            stored = (
                channel_message_repository.store_message(
                    self.session,
                    tenant_id=tenant_id,
                    platform=platform,
                    team_id=team_id,
                    channel_id=channel_id,
                    provider_message_id=provider_message_id,
                    message=redacted,
                    posted_at=posted_at,
                    thread_id=thread_id if thread_id != provider_message_id else None,
                    author_id=inner.get("user"),
                    is_decision=is_decision,
                    topic=topic,
                    people_mentioned=people_mentioned or None,
                )
                or stored
            )
        return stored

    def _handle_deleted(self, event, team_id, channel_id, platform):
        """Someone removed a message in Slack — remove every retained copy."""
        provider_message_id = event.get("deleted_ts") or (event.get("previous_message") or {}).get("ts")
        if not provider_message_id:
            return False
        return bool(
            channel_message_repository.delete_message(
                self.session,
                platform=platform,
                team_id=team_id,
                channel_id=channel_id,
                provider_message_id=provider_message_id,
            )
        )

    def _over_volume_limit(self, platform, team_id, channel_id):
        """Stops a high-volume channel (an alerts feed someone switched on)
        dominating storage. Counted per channel, not per tenant copy."""
        limit = settings.notifications.channel_ingest_max_messages_per_hour
        if limit <= 0:
            return False
        recent = channel_message_repository.count_recent_messages(
            self.session, platform=platform, team_id=team_id, channel_id=channel_id, minutes=60
        )
        if recent >= limit:
            LOG.warning(
                "Channel %s/%s exceeded ingest limit (%d/hour); dropping until the window clears",
                team_id,
                channel_id,
                limit,
            )
            return True
        return False

    def sweep_expired(self):
        """Delete messages past their channel's retention window."""
        return channel_message_repository.delete_expired_messages(self.session)
