"""Pins that a completed sync is recorded even when it embedded nothing.

Since the existence probe landed, "every document already current" is the
expected steady state of a healthy knowledge base. Suppressing that row would
leave the sync history stale and indistinguishable from a sync that never ran.
"""

from contextlib import contextmanager

import pytest

from rag.core.embeddings import tracker as tracker_mod
from rag.core.embeddings.tracker import EmbeddingTracker


class FakeConnection:
    def __init__(self, sink):
        self.sink = sink

    @contextmanager
    def begin(self):
        yield

    def execute(self, _query, params):
        self.sink.append(params)


class FakeEngine:
    """Captures INSERT params instead of touching a database."""

    def __init__(self):
        self.rows = []

    @contextmanager
    def connect(self):
        yield FakeConnection(self.rows)


@pytest.fixture
def engine(monkeypatch):
    fake = FakeEngine()
    monkeypatch.setattr(tracker_mod, "engine", fake)
    return fake


def _tracker(**kw):
    return EmbeddingTracker(
        account_id="acct-1",
        collection_name="kb_test",
        expected_document_count=kw.pop("expected", 139),
        **kw,
    )


def test_completed_sync_with_nothing_to_embed_is_recorded(engine):
    """The whole point: 0 embedded is a result, not a non-event."""
    t = _tracker()
    t.start_timer()  # a load really ran
    t.persist()

    assert len(engine.rows) == 1, "a completed zero-embed sync must be recorded"
    row = engine.rows[0]
    assert row["document_count"] == 0
    assert row["expected_document_count"] == 139
    assert row["request_status"] == "success"
    assert row["load_duration_seconds"] is not None, "duration proves the load ran"


def test_tracker_that_never_ran_is_not_recorded(engine):
    """Preserves the original intent — no row for a load that never started."""
    t = _tracker()
    t.persist()  # start_timer never called

    assert engine.rows == [], "a load that never started must not write a row"


def test_failure_is_recorded_even_with_no_tokens(engine):
    t = _tracker()
    t.start_timer()
    t.persist(status="failure", error_message="confluence unreachable")

    assert len(engine.rows) == 1
    assert engine.rows[0]["request_status"] == "failure"
    assert engine.rows[0]["error_message"] == "confluence unreachable"
