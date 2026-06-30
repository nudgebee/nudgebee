import time
from typing import Any, ClassVar, Dict, Optional

from notifications_server.configs.settings import settings


class EventCache:
    _instance: ClassVar[Optional["EventCache"]] = None
    cache: Dict[str, Dict[str, Any]]

    def __new__(cls, *args: Any, **kwargs: Any) -> "EventCache":
        if cls._instance is None:
            cls._instance = super().__new__(cls, *args, **kwargs)
            cls._instance.cache = {}
        return cls._instance

    def add_entry(
        self,
        thread_ts: str,
        event_id: str,
        event_context: Any,
        text: str,
        user_id: str,
        tenant_id: Optional[str] = None,
        account_id: Optional[str] = None,
    ) -> None:
        self.cache[thread_ts] = {
            "event_id": event_id,
            "event_context": event_context,
            "tenant_id": tenant_id,
            "account_id": account_id,
            "conversation_id": None,
            "user_id": user_id,
            "text": text,
            "timestamp": time.time(),
        }

    def update_entry(
        self,
        thread_ts: str,
        user_id: Optional[str] = None,
        text: Optional[str] = None,
        tenant_id: Optional[str] = None,
        account_id: Optional[str] = None,
        conversation_id: Optional[str] = None,
    ) -> bool:
        if thread_ts in self.cache:
            entry = self.cache[thread_ts]
            if text:
                entry["text"] = text
            if user_id:
                entry["user_id"] = user_id
            if tenant_id:
                entry["tenant_id"] = tenant_id
            if account_id:
                entry["account_id"] = account_id
            if conversation_id:
                entry["conversation_id"] = conversation_id
        else:
            return False

        return True

    def remove_entry(self, thread_ts: str) -> bool:
        # Remove an entry from the cache
        if thread_ts in self.cache:
            del self.cache[thread_ts]
        else:
            return False

        return True

    def get_entry(self, thread_ts: str) -> Optional[Dict[str, Any]]:
        # Retrieve an entry from the cache
        if thread_ts in self.cache:
            entry = self.cache[thread_ts]
            timestamp = entry["timestamp"]
            current_time = time.time()
            if current_time - timestamp <= settings.notifications.event_cache_ttl_seconds:
                return entry
            else:
                del self.cache[thread_ts]
                return None
        else:
            return None
