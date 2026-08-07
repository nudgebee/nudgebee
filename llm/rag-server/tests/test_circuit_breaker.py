import time

import pytest

from rag.core.llm import circuit_breaker as cb


@pytest.fixture(autouse=True)
def _reset():
    cb.reset()
    yield
    cb.reset()


def test_open_close_cycle():
    p, m = "huggingface", "Qwen/Test"
    assert cb.is_open(p, m) is False  # no entry == closed

    cb.record_failure(p, m)
    assert cb.is_open(p, m) is True

    cb.record_success(p, m)
    assert cb.is_open(p, m) is False  # entry removed


def test_blank_provider_or_model_never_opens():
    cb.record_failure("", "m")
    assert cb.is_open("", "m") is False
    assert cb.is_open("p", "") is False


def test_escalating_cooldown(monkeypatch):
    now = [1000.0]
    monkeypatch.setattr(cb.time, "monotonic", lambda: now[0])
    p, m = "huggingface", "Qwen/Test"

    cb.record_failure(p, m)  # 1st: 60s
    assert cb.is_open(p, m) is True
    now[0] += 61
    assert cb.is_open(p, m) is False  # cooldown expired -> probe allowed (read-only)

    cb.record_failure(p, m)  # 2nd consecutive: 120s
    now[0] += 61
    assert cb.is_open(p, m) is True  # still within the escalated 120s window


def test_probe_lock_after_cooldown(monkeypatch):
    now = [1000.0]
    monkeypatch.setattr(cb.time, "monotonic", lambda: now[0])
    p, m = "huggingface", "Qwen/Test"
    cb.record_failure(p, m)
    now[0] += 61  # cooldown expired
    assert cb.is_open(p, m) is False  # first caller takes the probe...
    assert cb.is_open(p, m) is True  # ...and reserves it, so the next caller fails fast


def test_cooldown_capped(monkeypatch):
    now = [0.0]
    monkeypatch.setattr(cb.time, "monotonic", lambda: now[0])
    p, m = "huggingface", "Qwen/Test"
    for _ in range(20):  # would overflow without the exponent cap
        cb.record_failure(p, m)
    now[0] += cb._MAX_COOLDOWN_SECONDS + 1
    assert cb.is_open(p, m) is False  # never exceeds the max cooldown


class _Resp:
    def __init__(self, code):
        self.status_code = code


def test_is_tripping_error():
    trip = Exception("unexpected status code: 503 Service Unavailable")
    trip.response = _Resp(503)
    assert cb.is_tripping_error(trip) is True

    assert cb.is_tripping_error(Exception("dial tcp: connection refused")) is True
    assert cb.is_tripping_error(Exception("read timed out")) is True
    assert cb.is_tripping_error(Exception("llm returned empty content")) is False

    # 4xx (structured or in-message) is client-side/quota — must NOT trip, even with a marker.
    client_err = Exception("400 Bad Request")
    client_err.response = _Resp(400)
    assert cb.is_tripping_error(client_err) is False
    assert cb.is_tripping_error(Exception("unexpected status code: 429: request timed out")) is False
    assert cb.is_tripping_error(Exception("429: request timed out")) is False  # raw 4xx code + marker
    assert cb.is_tripping_error(Exception("400 timed out")) is False
    assert cb.is_tripping_error(Exception("HTTP 400 bad request: request timed out")) is False  # 4xx anywhere
    assert cb.is_tripping_error(Exception("returned 429: request timed out")) is False
    # a :443 port in a connection error must NOT be read as a 4xx status — still trips.
    assert cb.is_tripping_error(Exception("dial tcp 10.0.0.5:443: connection refused")) is True
    # a 4xx in a model name or a duration must NOT be read as a status — still trips.
    assert cb.is_tripping_error(Exception("connection refused to model claude-401")) is True
    assert cb.is_tripping_error(Exception("request failed after 400 ms: i/o timeout")) is True

    # A non-integer status must never raise (runs inside the reranker's except block).
    weird = Exception("connection refused")
    weird.response = _Resp("not-a-number")
    assert cb.is_tripping_error(weird) is True

    assert cb.is_tripping_error(Exception("")) is False
    assert time.monotonic() > 0  # sanity: monotonic clock available
