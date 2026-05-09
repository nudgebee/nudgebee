import time


class EventCache:
    _instance = None

    def __new__(cls, *args, **kwargs):
        if cls._instance is None:
            cls._instance = super().__new__(cls, *args, **kwargs)
            cls._instance.cache = {}
        return cls._instance

    def add_entry(self, thread_ts, event_id, event_context, text, user_id, tenant_id=None, account_id=None):
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

    def update_entry(self, thread_ts, user_id=None, text=None, tenant_id=None, account_id=None, conversation_id=None):
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

    def remove_entry(self, thread_ts):
        # Remove an entry from the cache
        if thread_ts in self.cache:
            del self.cache[thread_ts]
        else:
            return False

        return True

    def get_entry(self, thread_ts):
        # Retrieve an entry from the cache
        if thread_ts in self.cache:
            entry = self.cache[thread_ts]
            timestamp = entry["timestamp"]
            current_time = time.time()
            if current_time - timestamp <= 7200:
                return entry
            else:
                del self.cache[thread_ts]
                return None
        else:
            return None
