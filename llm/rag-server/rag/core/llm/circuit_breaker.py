"""Per-pod in-memory circuit breaker for the LLM rerank path.

Mirrors llm-server's in-memory breaker (agents/core/llm_circuit_breaker.go): fail fast
when the self-hosted endpoint is not ready so a rerank request doesn't hang on the LLM
client's retries/timeout. Process-local — no shared/cross-service state, so cooldowns use
a monotonic clock and nothing is serialized.
"""

import logging
import re
import threading
import time
from typing import Dict

logger = logging.getLogger(__name__)

_BASE_COOLDOWN_SECONDS = 60
_MAX_COOLDOWN_SECONDS = 300
# How long the first caller past an expired cooldown reserves the single probe slot
# (half-open), so concurrent callers keep failing fast instead of stampeding the endpoint.
_PROBE_LOCK_SECONDS = 15.0

# Endpoint-down / unreachable signals that open the breaker. Excludes 4xx
# (client/config/quota), matching llm-server's classifier.
_TRIP_MARKERS = (
    "503",
    "service unavailable",
    "connection refused",
    "connection reset",
    "timeout",
    "timed out",
)

# Matches a 4xx code only in HTTP-status positions — at the message start or right after
# a status keyword. Ignores bare 4xx-looking numbers elsewhere (a model name like
# "claude-401", a duration like "400 ms", or the ":443" HTTPS port) so those never
# suppress a genuine outage trip.
_FOURXX_RE = re.compile(r"(^|(?:status|code|http|resp|response|returned)\s*:?\s*)4\d{2}\b")


class _Entry:
    __slots__ = ("cooldown_until", "failure_count")

    def __init__(self) -> None:
        self.cooldown_until = 0.0
        self.failure_count = 0


_lock = threading.Lock()
_state: Dict[str, _Entry] = {}


def _key(provider: str, model: str) -> str:
    return f"{provider}:{model}"


def is_open(provider: str, model: str) -> bool:
    """True if the breaker is open (skip the model). Once the cooldown has expired the
    first caller takes the single probe slot by extending the cooldown (probe lock);
    concurrent callers keep failing fast, so a recovering endpoint isn't stampeded."""
    if not provider or not model:
        return False
    with _lock:
        entry = _state.get(_key(provider, model))
        if entry is None:
            return False
        now = time.monotonic()
        if now >= entry.cooldown_until:
            entry.cooldown_until = now + _PROBE_LOCK_SECONDS  # reserve the probe
            return False
        return True


def record_failure(provider: str, model: str) -> None:
    """Open the breaker after a trip-worthy failure, with an escalating cooldown
    (base doubled per consecutive failure, capped at max)."""
    if not provider or not model:
        return
    with _lock:
        key = _key(provider, model)
        entry = _state.get(key) or _Entry()
        entry.failure_count += 1
        exponent = min(entry.failure_count - 1, 10)  # 2**10 already dwarfs the cap
        cooldown = min(_BASE_COOLDOWN_SECONDS * (2**exponent), _MAX_COOLDOWN_SECONDS)
        entry.cooldown_until = time.monotonic() + cooldown
        _state[key] = entry
        failure_count = entry.failure_count
    # Log outside the lock so a slow log handler can't block other threads (matches Go).
    logger.warning(
        "circuit breaker opened for %s/%s (failure_count=%d, cooldown=%ds)",
        provider,
        model,
        failure_count,
        cooldown,
    )


def record_success(provider: str, model: str) -> None:
    """Close the breaker (remove the entry — absent == closed)."""
    if not provider or not model:
        return
    with _lock:
        _state.pop(_key(provider, model), None)


def is_tripping_error(exc: BaseException) -> bool:
    """Whether an exception means the endpoint is down/unreachable (open the breaker).
    Never raises — it runs inside the reranker's except block, so a bad status type here
    must not escape and crash reranking."""
    try:
        # Some SDKs nest the code under .response, others expose it directly on the exception.
        raw = getattr(getattr(exc, "response", None), "status_code", None) or getattr(exc, "status_code", None)
        try:
            status = int(raw) if raw is not None else None
        except (TypeError, ValueError):
            status = None
        if status == 503:
            return True
        if status is not None and 400 <= status < 500:
            return False  # any 4xx is client-side/quota, never endpoint-down
        msg = str(exc).lower()
        # A 4xx in an HTTP-status position is client/quota, never endpoint-down.
        if _FOURXX_RE.search(msg):
            return False
        return any(m in msg for m in _TRIP_MARKERS)
    except Exception:  # pragma: no cover - defensive: classification must never raise
        return False


def reset() -> None:
    """Clear all breaker state. Testing helper."""
    with _lock:
        _state.clear()
