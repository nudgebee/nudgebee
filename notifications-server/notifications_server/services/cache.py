import time
import redis
import json
import logging
import uuid

from notifications_server.configs import settings
from notifications_server.models.models import MessagingPlatform

LOG = logging.getLogger(__name__)


class Cache:
    _redis_client = None

    def __init__(self):
        if settings.redis.is_enabled:
            if not Cache._redis_client:
                Cache._redis_client = self._create_redis_client()
            self.redis_client = Cache._redis_client
        else:
            self.redis_client = None

    @staticmethod
    def _create_redis_client():
        """Create a new Redis client with connection pooling and retry logic"""
        try:
            client = redis.Redis(
                host=settings.redis.host,
                port=settings.redis.port,
                username=settings.redis.username,
                password=settings.redis.password,
                decode_responses=True,
                socket_keepalive=True,
                socket_keepalive_options={},
                health_check_interval=30,
                retry_on_timeout=True,
                retry_on_error=[redis.exceptions.ConnectionError, redis.exceptions.TimeoutError],
                socket_connect_timeout=5,
                socket_timeout=5,
                retry=redis.retry.Retry(redis.backoff.ExponentialBackoff(), 3),
            )
            client.ping()
            LOG.info("Connected to Redis!")
            return client
        except Exception as e:
            LOG.exception(f"Unable to connect to Redis. {e}")
            return None

    def _ensure_connection(self):
        """Ensure Redis connection is alive, reconnect if needed"""
        if not self.redis_client:
            LOG.warning("Redis client is None. Attempting to create new connection...")
            Cache._redis_client = self._create_redis_client()
            self.redis_client = Cache._redis_client

    def cache_installations(self, tenant_id, installations):
        self._ensure_connection()
        if not self.redis_client:
            return
        key = f"notification_installations:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                installations_dict = [installation.to_dict() for installation in installations]
                pipe.set(key, json.dumps(installations_dict, default=self._json_serializable))
                pipe.expire(key, settings.redis.cache_expiration_minutes * 30)
                pipe.execute()
            except TypeError as e:
                LOG.exception(f"Error serializing installations: {e}")
            except redis.RedisError as e:
                LOG.exception(f"Error caching installations for tenant {tenant_id}: {e}")

    def get_installations(self, tenant_id):
        self._ensure_connection()
        if not self.redis_client:
            return None
        key = f"notification_installations:{tenant_id}"
        installations_json = self.redis_client.get(key)
        if installations_json:
            installations_data = json.loads(installations_json)
            return [MessagingPlatform.from_dict(data) for data in installations_data]
        return None

    def delete_cached_installations(self, tenant_id):
        self._ensure_connection()
        if not self.redis_client:
            return
        key = f"notification_installations:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                LOG.info(f"Cache entry for key '{key}' deleted successfully.")
            except redis.RedisError as e:
                LOG.exception(f"Error deleting cache entry for key '{key}': {e}")

    def cache_notification_rules(self, tenant_id, rules):
        self._ensure_connection()
        if not self.redis_client:
            return
        key = f"notification_rules:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                rules_dict = [rule.to_dict() if hasattr(rule, "to_dict") else rule for rule in rules]
                pipe.set(key, json.dumps(rules_dict, default=self._json_serializable))
                pipe.expire(key, settings.redis.cache_expiration_minutes * 60)
                pipe.execute()
            except TypeError as e:
                LOG.exception(f"Error serializing notification rules: {e}")
            except redis.RedisError as e:
                LOG.exception(f"Error caching notification rules for tenant {tenant_id}: {e}")

    def get_notification_rules(self, tenant_id):
        self._ensure_connection()
        if not self.redis_client:
            return None
        key = f"notification_rules:{tenant_id}"
        rules_json = self.redis_client.get(key)
        if rules_json:
            rules_data = json.loads(rules_json)
            from notifications_server.models.models import NotificationRules

            return [NotificationRules.from_dict(data) for data in rules_data]
        return None

    def delete_cached_notification_rules(self, tenant_id):
        self._ensure_connection()
        if not self.redis_client:
            return
        key = f"notification_rules:{tenant_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                LOG.info(f"Cache entry for key '{key}' deleted successfully.")
            except redis.RedisError as e:
                LOG.exception(f"Error deleting cache entry for key '{key}': {e}")

    def cache_event_entry(self, thread_ts, event_entry):
        self._ensure_connection()
        if not self.redis_client:
            return
        key = f"chat_event:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                event_entry["timestamp"] = time.time()
                pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            except TypeError as e:
                LOG.exception(f"Error serializing event entry: {e}")
            except redis.RedisError as e:
                LOG.exception(f"Error caching event entry {thread_ts}: {e}")

    def get_event_entry(self, thread_ts):
        self._ensure_connection()
        if not self.redis_client:
            return None
        key = f"chat_event:{thread_ts}"
        event_json = self.redis_client.get(key)
        if event_json:
            event_entry = json.loads(event_json)
            timestamp = event_entry.get("timestamp")
            if timestamp and time.time() - timestamp <= settings.redis.conversation_cache_expiration_minutes * 60:
                return event_entry
            else:
                self.redis_client.delete(key)
        return None

    def _json_serializable(self, obj):
        """Helper function to serialize non-JSON serializable objects like UUID."""
        if isinstance(obj, uuid.UUID):
            return str(obj)
        raise TypeError(f"Object of type {obj.__class__.__name__} is not JSON serializable")

    def update_event_entry(self, thread_ts, **kwargs):
        self._ensure_connection()
        if not self.redis_client:
            return False
        key = f"chat_event:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                event_json = self.redis_client.get(key)
                if event_json:
                    event_entry = json.loads(event_json)
                    event_entry.update({k: v for k, v in kwargs.items() if v is not None})
                    event_entry["timestamp"] = time.time()
                    pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                    pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                    pipe.execute()
                    return True
            except redis.RedisError as e:
                LOG.exception(f"Error updating event entry {thread_ts}: {e}")
        return False

    def remove_event_keys(self, thread_ts, keys):
        # update_event_entry can't clear a field: it drops None values so other
        # callers can do partial updates. Deleting keys needs an explicit path.
        self._ensure_connection()
        if not self.redis_client:
            return False
        key = f"chat_event:{thread_ts}"
        try:
            event_json = self.redis_client.get(key)
            if not event_json:
                return False
            event_entry = json.loads(event_json)
            for k in keys:
                event_entry.pop(k, None)
            event_entry["timestamp"] = time.time()
            with self.redis_client.pipeline() as pipe:
                pipe.set(key, json.dumps(event_entry, default=self._json_serializable))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            return True
        except (redis.RedisError, json.JSONDecodeError) as e:
            LOG.exception(f"Error removing keys from event entry {thread_ts}: {e}")
            return False

    def remove_event_entry(self, thread_ts):
        self._ensure_connection()
        if not self.redis_client:
            return False
        key = f"chat_event:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error removing event entry {thread_ts}: {e}")
                return False

    def cache_thread_images(self, thread_ts, images):
        # The current turn's images live under their own key (not the main
        # chat_event entry) so the potentially-large base64 blob isn't
        # re-serialized every time the entry's text/state is updated. Written on
        # every turn — including an empty list — so a later image-less turn does
        # not resend a prior turn's image.
        self._ensure_connection()
        if not self.redis_client:
            return
        key = f"chat_images:{thread_ts}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.set(key, json.dumps(images))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
            except (TypeError, redis.RedisError) as e:
                LOG.exception(f"Error caching thread images {thread_ts}: {e}")

    def get_thread_images(self, thread_ts):
        self._ensure_connection()
        if not self.redis_client:
            return []
        key = f"chat_images:{thread_ts}"
        try:
            images_json = self.redis_client.get(key)
        except redis.RedisError as e:
            LOG.exception(f"Error retrieving thread images {thread_ts}: {e}")
            return []
        if images_json:
            try:
                return json.loads(images_json)
            except json.JSONDecodeError:
                return []
        return []

    def cache_channel_session_mapping(self, channel_id, team_id, session_id, account_id=None, tenant_id=None):
        """Cache the mapping between channel_id and session_id from /channels/join"""
        self._ensure_connection()
        if not self.redis_client:
            return False
        key = f"channel_session:{team_id}:{channel_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                mapping_data = {
                    "session_id": session_id,
                    "account_id": account_id,
                    "tenant_id": tenant_id,
                    "timestamp": time.time(),
                }
                pipe.set(key, json.dumps(mapping_data, default=self._json_serializable))
                pipe.expire(key, settings.redis.conversation_cache_expiration_minutes * 60)
                pipe.execute()
                LOG.debug(
                    f"Cached channel session mapping: {channel_id} -> session_id={session_id}, "
                    f"account_id={account_id}, tenant_id={tenant_id}"
                )
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error caching channel session mapping for {channel_id}: {e}")
                return False

    def get_channel_session_mapping(self, channel_id, team_id):
        """Get the session_id and account details associated with a channel from /channels/join

        Returns:
            dict with keys: session_id, account_id, tenant_id (or None if not found/expired)
        """
        self._ensure_connection()
        if not self.redis_client:
            return None
        key = f"channel_session:{team_id}:{channel_id}"
        try:
            mapping_json = self.redis_client.get(key)
            if mapping_json:
                mapping_data = json.loads(mapping_json)
                timestamp = mapping_data.get("timestamp")
                if timestamp and time.time() - timestamp <= settings.redis.conversation_cache_expiration_minutes * 60:
                    return {
                        "session_id": mapping_data.get("session_id"),
                        "account_id": mapping_data.get("account_id"),
                        "tenant_id": mapping_data.get("tenant_id"),
                    }
                else:
                    self.redis_client.delete(key)
        except redis.RedisError as e:
            LOG.exception(f"Error retrieving channel session mapping for {channel_id}: {e}")
        return None

    def remove_channel_session_mapping(self, channel_id, team_id):
        """Remove the channel-to-session_id mapping"""
        self._ensure_connection()
        if not self.redis_client:
            return False
        key = f"channel_session:{team_id}:{channel_id}"
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                pipe.execute()
                LOG.info(f"Removed channel session mapping for {channel_id}")
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error removing channel session mapping for {channel_id}: {e}")
                return False

    # --- Channel-awareness watched-set mirror -------------------------------
    # Mirror of the enabled rows in messaging_channel_watch, one Redis set per
    # workspace, maintained on every toggle. No TTL: the DB is authoritative and
    # rebuild_watched_channels reseeds after a Redis flush. Readers must treat
    # None (Redis unavailable) as "unknown" and fall back to the DB.

    @staticmethod
    def _watched_channels_key(platform, team_id):
        return f"watched_channels:{platform}:{team_id}"

    def add_watched_channel(self, platform, team_id, channel_id):
        self._ensure_connection()
        if not self.redis_client:
            return False
        try:
            self.redis_client.sadd(self._watched_channels_key(platform, team_id), channel_id)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error adding watched channel {channel_id}: {e}")
            return False

    def remove_watched_channel(self, platform, team_id, channel_id):
        self._ensure_connection()
        if not self.redis_client:
            return False
        try:
            self.redis_client.srem(self._watched_channels_key(platform, team_id), channel_id)
            return True
        except redis.RedisError as e:
            LOG.exception(f"Error removing watched channel {channel_id}: {e}")
            return False

    def is_channel_watched(self, platform, team_id, channel_id):
        """True/False from the mirror, or None when Redis is unavailable so the
        caller can fall back to the messaging_channel_watch table."""
        self._ensure_connection()
        if not self.redis_client:
            return None
        try:
            return bool(self.redis_client.sismember(self._watched_channels_key(platform, team_id), channel_id))
        except redis.RedisError as e:
            LOG.exception(f"Error checking watched channel {channel_id}: {e}")
            return None

    def rebuild_watched_channels(self, platform, team_id, channel_ids):
        """Reseed one workspace's mirror from the DB truth (e.g. after a flush)."""
        self._ensure_connection()
        if not self.redis_client:
            return False
        key = self._watched_channels_key(platform, team_id)
        with self.redis_client.pipeline() as pipe:
            try:
                pipe.delete(key)
                if channel_ids:
                    pipe.sadd(key, *channel_ids)
                pipe.execute()
                return True
            except redis.RedisError as e:
                LOG.exception(f"Error rebuilding watched channels for {team_id}: {e}")
                return False
