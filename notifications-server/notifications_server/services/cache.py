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
