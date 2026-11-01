"""The per-collection classification rollup (entry_type = 'kb_collection').

A chunk's Qdrant payload (`metadata.kind` / `shape` / `capabilities`) is the only
"already classified" marker. Postgres keeps ONE derived row per collection — the
union of the labels its chunks carry — that only feeds the Knowledge Base tab and
can always be rebuilt from the payload. These tests pin the row's lifecycle:

  * db_query: the upsert / delete / read functions and their failure semantics;
  * processing.refresh_kb_collection_summary: the union computed from a real
    (in-memory) Qdrant collection, and what it writes;
  * every way a collection's row is dropped (delete_collection, the KB delete
    endpoint, _setup_collection's two direct deletes) or corrected (a prune);
  * a cheap guard that the migration still describes the rollup shape.

Everything is offline: Postgres is a fake engine, Qdrant is the in-memory client.
"""

import asyncio
import inspect
import logging
import math
import re
import uuid
from contextlib import contextmanager
from pathlib import Path
from types import SimpleNamespace

import pytest
from qdrant_client import QdrantClient
from qdrant_client.models import Distance, PointStruct, VectorParams

from rag.core.documents import collection as collection_module
from rag.core.documents import processing
from rag.core.utils import db_query

COLLECTION = "kb_11111111-1111-1111-1111-111111111111"
LABEL_SELECTOR = ["metadata.kind", "metadata.shape", "metadata.capabilities"]


# ---------------------------------------------------------------------------
# Fakes


class _Result:
    """What `connection.execute(...)` returns: iterable rows plus .first()."""

    def __init__(self, rows):
        self._rows = list(rows)

    def __iter__(self):
        return iter(self._rows)

    def first(self):
        return self._rows[0] if self._rows else None


def _fake_engine(rows=(), raise_error=False, executed=None, events=None):
    """A stand-in for the SQLAlchemy engine. `executed` collects (sql, params);
    `events` records "begin" / "execute" / "commit" so a test can tell that a
    write ran inside a transaction."""

    @contextmanager
    def connect():
        class _Txn:
            def __enter__(self):
                if events is not None:
                    events.append("begin")
                return self

            def __exit__(self, *args):
                if events is not None:
                    events.append("commit")
                return False

        class _Conn:
            def begin(self):
                return _Txn()

            def execute(self, query, params=None):
                if raise_error:
                    raise RuntimeError("db unavailable")
                if executed is not None:
                    executed.append((" ".join(str(query).split()), params))
                if events is not None:
                    events.append("execute")
                return _Result(rows)

        yield _Conn()

    return SimpleNamespace(connect=connect)


def _point_uuid(i):
    return str(uuid.UUID(int=i + 1))


def _labelled(kind="sop", shape="deterministic_procedure", capabilities=None):
    return {"kind": kind, "shape": shape, "capabilities": capabilities if capabilities is not None else []}


def _seed(client, collection, metadatas):
    """One point per entry of `metadatas` (a dict, or None for a payload with no metadata)."""
    if not client.collection_exists(collection):
        client.create_collection(collection, vectors_config=VectorParams(size=2, distance=Distance.COSINE))
    points = []
    for i, metadata in enumerate(metadatas):
        payload = {"page_content": f"chunk {i}"}
        if metadata is not None:
            payload["metadata"] = metadata
        points.append(PointStruct(id=_point_uuid(i), vector=[0.1, 0.2], payload=payload))
    client.upsert(collection, points=points)


class _SpyClient:
    """Wraps the in-memory client, recording every scroll's arguments."""

    def __init__(self, inner):
        self._inner = inner
        self.scrolls = []
        self.fail_scroll_after = None  # raise on the Nth scroll (0-based) when set

    def __getattr__(self, name):
        return getattr(self._inner, name)

    def scroll(self, **kwargs):
        if self.fail_scroll_after is not None and len(self.scrolls) >= self.fail_scroll_after:
            self.scrolls.append(kwargs)
            raise RuntimeError("qdrant unavailable")
        self.scrolls.append(kwargs)
        return self._inner.scroll(**kwargs)

    def summary_scrolls(self):
        return [s for s in self.scrolls if s.get("with_payload") == LABEL_SELECTOR]


@pytest.fixture
def qdrant(monkeypatch):
    client = _SpyClient(QdrantClient(":memory:"))
    monkeypatch.setattr(processing, "get_qdrant_client", lambda: client)
    return client


@pytest.fixture
def summary_writes(monkeypatch):
    """Records every set_kb_collection_summary call refresh makes."""
    writes = []
    monkeypatch.setattr(
        db_query,
        "set_kb_collection_summary",
        lambda collection_name, kinds, shapes, capabilities: writes.append(
            (collection_name, kinds, shapes, capabilities)
        ),
    )
    return writes


@pytest.fixture
def dropped(monkeypatch):
    """Records every delete_kb_capabilities call (the summary row's removal)."""
    calls = []
    monkeypatch.setattr(db_query, "delete_kb_capabilities", lambda name: calls.append(name))
    return calls


# ---------------------------------------------------------------------------
# (1) db_query.set_kb_collection_summary


def test_set_summary_upserts_one_row_keyed_by_collection(monkeypatch):
    executed, events = [], []
    monkeypatch.setattr(db_query, "engine", _fake_engine(executed=executed, events=events))

    db_query.set_kb_collection_summary(
        COLLECTION, ["knowledge", "sop"], ["deterministic_procedure", "narrative_reference"], ["k8s.workload_restart"]
    )

    assert len(executed) == 1
    sql, params = executed[0]
    assert sql.startswith("INSERT INTO llm_kb_capability_entries")
    assert "('kb_collection', :collection_name, :kinds, :shapes, :capabilities, NOW())" in sql
    assert "ON CONFLICT (collection_name) WHERE entry_type = 'kb_collection' DO UPDATE" in sql
    for column in ("kinds", "shapes", "capabilities"):
        assert f"{column} = EXCLUDED.{column}" in sql
    assert "updated_at = NOW()" in sql
    assert params == {
        "collection_name": COLLECTION,
        "kinds": ["knowledge", "sop"],
        "shapes": ["deterministic_procedure", "narrative_reference"],
        "capabilities": ["k8s.workload_restart"],
    }
    # The row is one account-free rollup: no per-chunk identity in the statement.
    assert "chunk_id" not in sql and "account_id" not in sql
    assert events == ["begin", "execute", "commit"]  # written inside a transaction


@pytest.mark.parametrize(
    "kinds, shapes, capabilities",
    [
        (["sop"], [], []),
        ([], ["narrative_reference"], []),
        ([], [], ["k8s.workload_restart"]),
    ],
)
def test_set_summary_with_any_label_still_upserts(monkeypatch, kinds, shapes, capabilities):
    executed = []
    monkeypatch.setattr(db_query, "engine", _fake_engine(executed=executed))

    db_query.set_kb_collection_summary(COLLECTION, kinds, shapes, capabilities)

    assert [sql.split()[0] for sql, _ in executed] == ["INSERT"]


def test_set_summary_with_nothing_labelled_deletes_the_row_instead(monkeypatch):
    executed, events = [], []
    monkeypatch.setattr(db_query, "engine", _fake_engine(executed=executed, events=events))

    db_query.set_kb_collection_summary(COLLECTION, [], [], [])

    assert len(executed) == 1  # one DELETE, and no INSERT of an empty row
    sql, params = executed[0]
    assert sql.startswith("DELETE FROM llm_kb_capability_entries")
    # platform_capability rows are global and must never be touched.
    assert "entry_type = 'kb_collection'" in sql and "collection_name = :collection_name" in sql
    assert params == {"collection_name": COLLECTION}
    assert events == ["begin", "execute", "commit"]


def test_set_summary_swallows_and_logs_a_database_error(monkeypatch, caplog):
    monkeypatch.setattr(db_query, "engine", _fake_engine(raise_error=True))

    with caplog.at_level(logging.ERROR, logger=db_query.logger.name):
        db_query.set_kb_collection_summary(COLLECTION, ["sop"], ["deterministic_procedure"], [])  # must not raise
        db_query.set_kb_collection_summary(COLLECTION, [], [], [])  # nor on the delete path

    assert len([r for r in caplog.records if COLLECTION in r.getMessage()]) == 2


# ---------------------------------------------------------------------------
# (2) get_kb_summary_collection_names / has_kb_collection_summary / delete_kb_capabilities


def test_get_summary_collection_names_returns_the_set_of_collections_with_a_row(monkeypatch):
    executed = []
    rows = [
        SimpleNamespace(collection_name="kb_1"),
        SimpleNamespace(collection_name="kb_2"),
        SimpleNamespace(collection_name="kb_1"),
    ]
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=rows, executed=executed))

    assert db_query.get_kb_summary_collection_names() == {"kb_1", "kb_2"}
    assert "entry_type = 'kb_collection'" in executed[0][0]


def test_get_summary_collection_names_is_an_empty_set_when_there_are_no_rows(monkeypatch):
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[]))

    assert db_query.get_kb_summary_collection_names() == set()


def test_get_summary_collection_names_raises_on_a_database_error(monkeypatch):
    # An empty set means "no rollups yet"; a failed read must not be mistaken for
    # it, or the backfill would rebuild a rollup for every collection.
    monkeypatch.setattr(db_query, "engine", _fake_engine(raise_error=True))

    with pytest.raises(RuntimeError):
        db_query.get_kb_summary_collection_names()


def test_has_summary_is_true_when_the_collection_has_a_row(monkeypatch):
    executed = []
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[SimpleNamespace(one=1)], executed=executed))

    assert db_query.has_kb_collection_summary(COLLECTION) is True
    sql, params = executed[0]
    assert "entry_type = 'kb_collection'" in sql and "collection_name = :collection_name" in sql
    assert params == {"collection_name": COLLECTION}


def test_has_summary_is_false_when_the_collection_has_no_row(monkeypatch):
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[]))

    assert db_query.has_kb_collection_summary(COLLECTION) is False


def test_has_summary_is_false_on_a_database_error(monkeypatch, caplog):
    monkeypatch.setattr(db_query, "engine", _fake_engine(raise_error=True))

    with caplog.at_level(logging.ERROR, logger=db_query.logger.name):
        assert db_query.has_kb_collection_summary(COLLECTION) is False

    assert any(COLLECTION in r.getMessage() for r in caplog.records)


def test_delete_kb_capabilities_removes_only_that_collections_rollup_row(monkeypatch):
    executed, events = [], []
    monkeypatch.setattr(db_query, "engine", _fake_engine(executed=executed, events=events))

    db_query.delete_kb_capabilities(COLLECTION)

    assert len(executed) == 1
    sql, params = executed[0]
    assert sql.startswith("DELETE FROM llm_kb_capability_entries")
    # platform_capability rows are global and must never be touched by a KB delete.
    assert "entry_type = 'kb_collection'" in sql
    assert "collection_name = :collection_name" in sql
    assert "kb_chunk" not in sql
    assert params == {"collection_name": COLLECTION}
    assert events == ["begin", "execute", "commit"]


def test_delete_kb_capabilities_swallows_a_database_error(monkeypatch, caplog):
    monkeypatch.setattr(db_query, "engine", _fake_engine(raise_error=True))

    with caplog.at_level(logging.ERROR, logger=db_query.logger.name):
        db_query.delete_kb_capabilities(COLLECTION)  # must not raise: the collection is already gone

    assert any(COLLECTION in r.getMessage() for r in caplog.records)


# ---------------------------------------------------------------------------
# (3) processing.refresh_kb_collection_summary


def test_refresh_unions_kinds_shapes_and_capabilities_across_labelled_chunks(qdrant, summary_writes):
    _seed(
        qdrant,
        COLLECTION,
        [
            {"chunk_index": 0, **_labelled("sop", "deterministic_procedure", ["k8s.workload_restart"])},
            {"chunk_index": 1, **_labelled("knowledge", "narrative_reference", [])},
            _labelled("sop", "decision_tree", ["k8s.workload_restart", "aws.ec2_reboot"]),
        ],
    )

    processing.refresh_kb_collection_summary(COLLECTION)

    assert summary_writes == [
        (
            COLLECTION,
            ["knowledge", "sop"],
            ["decision_tree", "deterministic_procedure", "narrative_reference"],
            ["aws.ec2_reboot", "k8s.workload_restart"],
        )
    ]


def test_refresh_ignores_chunks_that_are_not_classified(qdrant, summary_writes):
    _seed(
        qdrant,
        COLLECTION,
        [
            _labelled("sop", "deterministic_procedure", ["k8s.workload_restart"]),
            None,  # a payload with no metadata at all
            {"chunk_index": 3},  # metadata without labels
            # Labels without a kind are not a classification: shape / capabilities must not leak in.
            {"shape": "decision_tree", "capabilities": ["leaked.capability"]},
            # A kind outside {sop, knowledge} does not count either.
            _labelled("runbook", "credential_access_procedure", ["leaked.other"]),
            _labelled(None, "ui_walkthrough_business_app", ["leaked.none"]),
        ],
    )

    processing.refresh_kb_collection_summary(COLLECTION)

    assert summary_writes == [(COLLECTION, ["sop"], ["deterministic_procedure"], ["k8s.workload_restart"])]


def test_refresh_keeps_only_well_formed_shapes_and_string_capabilities(qdrant, summary_writes):
    _seed(
        qdrant,
        COLLECTION,
        [
            {
                "kind": "sop",
                "shape": "sorcery",  # not one of the known shapes
                "capabilities": [1, None, {"name": "x"}, ["nested"], "k8s.workload_restart"],
            },
            {"kind": "knowledge", "shape": "narrative_reference", "capabilities": "k8s.not_a_list"},
            {"kind": "knowledge"},  # no shape, no capabilities keys at all
        ],
    )

    processing.refresh_kb_collection_summary(COLLECTION)

    assert summary_writes == [(COLLECTION, ["knowledge", "sop"], ["narrative_reference"], ["k8s.workload_restart"])]


@pytest.mark.parametrize("batch", [None, 3], ids=["default_batch", "small_batch"])
def test_refresh_reads_every_page_of_a_large_collection(qdrant, summary_writes, monkeypatch, batch):
    if batch:
        monkeypatch.setattr(processing, "_SUMMARY_SCROLL_BATCH", batch)
    page = processing._SUMMARY_SCROLL_BATCH
    total = page * 3 + 1 if batch else page + 1  # more than one page either way
    # Every chunk carries its own capability, so the union is only complete if no page was skipped.
    _seed(qdrant, COLLECTION, [_labelled("sop", "decision_tree", [f"cap.{i:05d}"]) for i in range(total)])

    processing.refresh_kb_collection_summary(COLLECTION)

    assert len(summary_writes) == 1
    _, kinds, shapes, capabilities = summary_writes[0]
    assert kinds == ["sop"] and shapes == ["decision_tree"]
    assert capabilities == [f"cap.{i:05d}" for i in range(total)]
    assert len(qdrant.summary_scrolls()) == math.ceil(total / page)


def test_refresh_reads_labels_with_a_payload_selector_and_no_vectors(qdrant, summary_writes):
    _seed(qdrant, COLLECTION, [_labelled()])

    processing.refresh_kb_collection_summary(COLLECTION)

    assert qdrant.scrolls, "the summary is computed from a scroll of the collection"
    for scroll in qdrant.scrolls:
        assert scroll["collection_name"] == COLLECTION
        assert scroll["with_payload"] == LABEL_SELECTOR  # labels only: never the chunk text
        assert scroll["with_vectors"] is False


def test_refresh_with_no_labelled_chunk_writes_an_empty_rollup(qdrant, summary_writes):
    _seed(qdrant, COLLECTION, [None, {"chunk_index": 1}])

    processing.refresh_kb_collection_summary(COLLECTION)

    # An empty union is still written: set_kb_collection_summary turns it into a DELETE.
    assert summary_writes == [(COLLECTION, [], [], [])]


def test_refresh_of_an_empty_collection_writes_an_empty_rollup(qdrant, summary_writes):
    _seed(qdrant, COLLECTION, [])

    processing.refresh_kb_collection_summary(COLLECTION)

    assert summary_writes == [(COLLECTION, [], [], [])]


def test_refresh_writes_through_to_the_row_upsert_and_the_row_delete(qdrant, monkeypatch):
    executed = []
    monkeypatch.setattr(db_query, "engine", _fake_engine(executed=executed))

    _seed(qdrant, COLLECTION, [_labelled("sop", "decision_tree", ["k8s.workload_restart"])])
    processing.refresh_kb_collection_summary(COLLECTION)
    _seed(qdrant, "kb_unlabelled", [None])
    processing.refresh_kb_collection_summary("kb_unlabelled")

    (upsert_sql, upsert_params), (delete_sql, delete_params) = executed
    assert upsert_sql.startswith("INSERT INTO llm_kb_capability_entries")
    assert upsert_params == {
        "collection_name": COLLECTION,
        "kinds": ["sop"],
        "shapes": ["decision_tree"],
        "capabilities": ["k8s.workload_restart"],
    }
    assert delete_sql.startswith("DELETE FROM llm_kb_capability_entries")
    assert delete_params == {"collection_name": "kb_unlabelled"}


def test_refresh_scroll_error_writes_nothing_and_does_not_raise(qdrant, summary_writes, caplog):
    _seed(qdrant, COLLECTION, [_labelled()])
    qdrant.fail_scroll_after = 0

    with caplog.at_level(logging.ERROR, logger=processing.logger.name):
        processing.refresh_kb_collection_summary(COLLECTION)  # must not raise

    assert summary_writes == []  # a failed read must never be written as "nothing is classified"
    assert any(COLLECTION in r.getMessage() for r in caplog.records)


def test_refresh_error_on_a_later_page_writes_no_partial_union(qdrant, summary_writes, monkeypatch):
    monkeypatch.setattr(processing, "_SUMMARY_SCROLL_BATCH", 2)
    _seed(qdrant, COLLECTION, [_labelled("sop", "decision_tree", [f"cap.{i}"]) for i in range(6)])
    qdrant.fail_scroll_after = 1  # the first page reads fine, the second fails

    processing.refresh_kb_collection_summary(COLLECTION)

    assert len(qdrant.scrolls) == 2
    assert summary_writes == []


def test_refresh_of_a_missing_collection_writes_nothing_and_does_not_raise(qdrant, summary_writes):
    processing.refresh_kb_collection_summary("kb_never_created")

    assert summary_writes == []


def test_refresh_does_not_raise_when_the_write_itself_fails(qdrant, monkeypatch):
    _seed(qdrant, COLLECTION, [_labelled()])

    def boom(*args, **kwargs):
        raise RuntimeError("db unavailable")

    monkeypatch.setattr(db_query, "set_kb_collection_summary", boom)

    processing.refresh_kb_collection_summary(COLLECTION)  # best-effort


# ---------------------------------------------------------------------------
# (4) collection.delete_collection drops the rollup whenever the collection is gone


class _QdrantDeleting:
    def __init__(self, error=None, calls=None):
        self.error = error
        self.calls = calls if calls is not None else []

    def delete_collection(self, name):
        self.calls.append(("qdrant", name))
        if self.error:
            raise self.error


def _wire_delete(monkeypatch, error=None):
    calls = []
    monkeypatch.setattr(collection_module, "get_qdrant_client", lambda: _QdrantDeleting(error, calls))
    monkeypatch.setattr(db_query, "delete_kb_capabilities", lambda name: calls.append(("summary", name)))
    return calls


def test_delete_collection_drops_the_summary_after_the_qdrant_delete(monkeypatch):
    calls = _wire_delete(monkeypatch)

    collection_module.delete_collection(COLLECTION)

    assert calls == [("qdrant", COLLECTION), ("summary", COLLECTION)]


def test_delete_collection_drops_the_summary_of_a_collection_that_did_not_exist(monkeypatch):
    # A stale row can outlive its collection (e.g. deleted out of band); the delete must still clear it.
    calls = _wire_delete(monkeypatch, error=ValueError(f"Collection {COLLECTION} not found"))

    collection_module.delete_collection(COLLECTION)

    assert calls == [("qdrant", COLLECTION), ("summary", COLLECTION)]


@pytest.mark.parametrize("error", [RuntimeError("qdrant unavailable"), OSError("connection reset")])
def test_delete_collection_keeps_the_summary_when_the_qdrant_delete_failed(monkeypatch, error):
    # The collection may still be there, labels and all: its rollup must not be dropped.
    calls = _wire_delete(monkeypatch, error=error)

    collection_module.delete_collection(COLLECTION)  # logged, not raised

    assert calls == [("qdrant", COLLECTION)]


class _Listed:
    def __init__(self, name, module=None):
        self.name = name
        self.metadata = {"module": module} if module else {}


def test_deleting_a_modules_collections_drops_the_rollup_of_each_one_it_deleted(monkeypatch):
    calls = _wire_delete(monkeypatch)
    monkeypatch.setattr(
        collection_module,
        "list_collections_optimized",
        lambda: [
            _Listed("kb_a", "knowledge_base"),
            _Listed("obs_b", "observability"),
            _Listed("kb_c", "knowledge_base"),
        ],
    )

    collection_module.delete_module_collections("knowledge_base")

    assert calls == [("qdrant", "kb_a"), ("summary", "kb_a"), ("qdrant", "kb_c"), ("summary", "kb_c")]


def test_deleting_every_collection_drops_every_rollup(monkeypatch):
    calls = _wire_delete(monkeypatch)
    monkeypatch.setattr(collection_module, "list_collections_optimized", lambda: [_Listed("kb_a"), _Listed("kb_b")])

    collection_module.delete_all_collections()

    assert calls == [("qdrant", "kb_a"), ("summary", "kb_a"), ("qdrant", "kb_b"), ("summary", "kb_b")]


# ---------------------------------------------------------------------------
# (5) The delete endpoints reach the summary through delete_collection


def test_kb_delete_endpoint_drops_the_summary_once_via_delete_collection(monkeypatch):
    from controllers import knowledgebase_controller as controller

    calls = _wire_delete(monkeypatch)
    monkeypatch.setattr(controller.source_store, "drop", lambda name: calls.append(("source", name)))
    monkeypatch.setattr(controller.figure_cache, "forget_collection", lambda name: calls.append(("figures", name)))

    result = asyncio.run(controller.delete_kb("acct-1", "kb-1"))

    assert result["status"] == "deleted"
    # Exactly one summary drop, made by delete_collection right after the Qdrant delete.
    assert calls == [("qdrant", "kb_kb-1"), ("summary", "kb_kb-1"), ("source", "kb_kb-1"), ("figures", "kb_kb-1")]
    # The endpoint no longer imports the drop itself (it would bypass the seam patched above).
    assert not hasattr(controller, "delete_kb_capabilities")


def test_integration_collection_delete_endpoint_also_drops_the_summary(monkeypatch):
    from controllers import collection_controller

    calls = _wire_delete(monkeypatch)

    # The endpoint is a plain `def` (it runs in the threadpool); tolerate it becoming a coroutine again.
    result = collection_controller.delete_collection("abc_knowledge_base")
    if inspect.isawaitable(result):
        result = asyncio.run(result)

    assert result == {"data": "ok"}
    assert calls == [("qdrant", "abc_knowledge_base"), ("summary", "abc_knowledge_base")]


# ---------------------------------------------------------------------------
# (6) handle_updated_documents corrects the rollup after a prune


def _hex(i):
    return _point_uuid(i).replace("-", "")


@pytest.fixture
def prune_collection(qdrant):
    """Two labelled chunks: 0 (sop, kept by the sync) and 1 (knowledge, gone from the source)."""
    _seed(
        qdrant,
        COLLECTION,
        [
            _labelled("sop", "deterministic_procedure", ["k8s.workload_restart"]),
            _labelled("knowledge", "narrative_reference", []),
        ],
    )
    return qdrant


def _has_summary(monkeypatch, answer):
    asked = []
    monkeypatch.setattr(db_query, "has_kb_collection_summary", lambda name: asked.append(name) or answer)
    return asked


def test_prune_with_a_rollup_row_refreshes_it(prune_collection, monkeypatch, summary_writes):
    asked = _has_summary(monkeypatch, True)

    assert processing.handle_updated_documents(COLLECTION, [_hex(0)]) is True

    assert asked == [COLLECTION]
    # Removing the only page carrying a kind/shape drops it from the rollup.
    assert summary_writes == [(COLLECTION, ["sop"], ["deterministic_procedure"], ["k8s.workload_restart"])]


def test_prune_of_everything_empties_the_rollup(prune_collection, monkeypatch, summary_writes):
    _has_summary(monkeypatch, True)

    assert processing.handle_updated_documents(COLLECTION, []) is True

    assert summary_writes == [(COLLECTION, [], [], [])]  # an empty rollup is written as a row DELETE


def test_prune_calls_refresh_when_the_collection_has_a_rollup_row(prune_collection, monkeypatch):
    refreshed = []
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", lambda name: refreshed.append(name))
    _has_summary(monkeypatch, True)

    processing.handle_updated_documents(COLLECTION, [_hex(0)])

    assert refreshed == [COLLECTION]


def test_prune_without_a_rollup_row_neither_scrolls_the_labels_nor_refreshes(
    prune_collection, monkeypatch, summary_writes
):
    refreshed = []
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", lambda name: refreshed.append(name))
    asked = _has_summary(monkeypatch, False)

    assert processing.handle_updated_documents(COLLECTION, [_hex(0)]) is True

    assert asked == [COLLECTION]
    assert refreshed == []
    assert prune_collection.summary_scrolls() == [] and summary_writes == []


def test_prune_without_a_rollup_row_with_the_real_refresh_still_reads_no_labels(
    prune_collection, monkeypatch, summary_writes
):
    _has_summary(monkeypatch, False)

    processing.handle_updated_documents(COLLECTION, [_hex(0)])

    assert prune_collection.summary_scrolls() == [] and summary_writes == []


def test_nothing_pruned_touches_neither_the_gate_nor_the_refresh(prune_collection, monkeypatch, summary_writes):
    refreshed = []
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", lambda name: refreshed.append(name))
    asked = _has_summary(monkeypatch, True)

    assert processing.handle_updated_documents(COLLECTION, [_hex(0), _hex(1)]) is False

    assert asked == [] and refreshed == [] and summary_writes == []


def test_a_prune_whose_delete_failed_does_not_refresh(prune_collection, monkeypatch):
    refreshed = []
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", lambda name: refreshed.append(name))
    asked = _has_summary(monkeypatch, True)
    monkeypatch.setattr(processing, "_delete_document", lambda client, doc_id, collection_name: False)

    assert processing.handle_updated_documents(COLLECTION, [_hex(0)]) is False

    assert asked == [] and refreshed == []


# ---------------------------------------------------------------------------
# (7) _setup_collection's two direct deletes forget the rollup


class _FakeMetadataCache:
    def __init__(self, entry):
        self._entry = entry

    def get(self, name):
        return self._entry


@pytest.fixture
def setup_collection(monkeypatch):
    """Drives _setup_collection with a chosen metadata-cache entry; returns the ordered call log."""

    def drive(entry):
        calls = []
        client = SimpleNamespace(delete_collection=lambda name: calls.append(("qdrant", name)))
        monkeypatch.setattr(processing, "get_qdrant_client", lambda: client)
        monkeypatch.setattr("rag.qdrant.client.list_collections_optimized", lambda: [])
        monkeypatch.setattr("rag.core.metadata_cache.get_metadata_cache", lambda: _FakeMetadataCache(entry))
        monkeypatch.setattr(db_query, "delete_kb_capabilities", lambda name: calls.append(("summary", name)))
        result = processing._setup_collection("acct-1", "knowledge_base", COLLECTION)
        return result, calls

    return drive


def test_setup_collection_recreating_a_half_built_collection_forgets_its_summary(setup_collection):
    # Cached metadata without embeddings_generated_at: the previous load never finished.
    (name, _, verify_doc), calls = setup_collection({"metadata": {"module": "knowledge_base"}})

    assert calls == [("qdrant", COLLECTION), ("summary", COLLECTION)]
    assert name == COLLECTION and verify_doc is False


def test_setup_collection_recreating_a_collection_with_empty_metadata_forgets_its_summary(setup_collection):
    (name, _, verify_doc), calls = setup_collection({"metadata": {}})

    assert calls == [("qdrant", COLLECTION), ("summary", COLLECTION)]
    assert name == COLLECTION and verify_doc is False


def test_setup_collection_keeps_the_summary_of_a_collection_it_reuses(setup_collection):
    (name, _, verify_doc), calls = setup_collection(
        {"metadata": {"module": "knowledge_base", "embeddings_generated_at": "2026-01-01 00:00:00"}}
    )

    assert calls == []  # nothing deleted, nothing forgotten: the labels on the chunks stay valid
    assert name == COLLECTION and verify_doc is True


def test_setup_collection_for_a_new_collection_deletes_nothing(setup_collection):
    (name, _, verify_doc), calls = setup_collection(None)

    assert calls == []
    assert name == COLLECTION and verify_doc is False


# ---------------------------------------------------------------------------
# The migration still describes the rollup shape


def test_migration_describes_the_collection_rollup_and_not_the_per_chunk_shape():
    migrations = Path(__file__).resolve().parents[3] / "api-server" / "migrations" / "migrations" / "app"
    files = list(migrations.glob("*_V*_create_llm_kb_capability_entries.up.sql"))
    assert (
        len(files) == 1
    ), f"expected exactly one create_llm_kb_capability_entries up migration in {migrations}, found {files}"

    # Comments legitimately recount the old per-chunk shape (and the folded-in
    # llm_kb_document_categories); only executable SQL is checked.
    sql = re.sub(r"--[^\n]*", "", files[0].read_text())

    # The old per-chunk marker (entry_type='kb_chunk', identified by chunk_id) must
    # never come back — that is the whole reason this table holds a collection-level
    # rollup instead. account_id is legitimately back in the schema for
    # entry_type='kb_document_mark' (#38623 follow-up folding in #39211's table), so
    # it is not itself stale; chunk_id and the bare 'kb_chunk' literal still are.
    for stale in ("kb_chunk", "chunk_id"):
        assert stale not in sql, f"the migration's SQL still mentions the per-chunk shape: {stale!r}"
    assert "'kb_collection'" in sql
    assert re.search(r"kinds\s+TEXT\[\]", sql) and re.search(r"shapes\s+TEXT\[\]", sql)
    assert re.search(
        r"UNIQUE INDEX IF NOT EXISTS \w+\s+ON public\.llm_kb_capability_entries\(collection_name\) "
        r"WHERE entry_type = 'kb_collection'",
        sql,
    ), "one rollup row per collection must be enforced by a partial unique index"


def test_migration_folds_in_the_document_mark_table_without_touching_it():
    migrations = Path(__file__).resolve().parents[3] / "api-server" / "migrations" / "migrations" / "app"
    (up_file,) = migrations.glob("*_V*_create_llm_kb_capability_entries.up.sql")
    sql = re.sub(r"--[^\n]*", "", up_file.read_text())

    assert "'kb_document_mark'" in sql
    assert re.search(r"document_key\s+TEXT", sql) and re.search(r"note_category\s+VARCHAR", sql)
    assert re.search(
        r"UNIQUE INDEX IF NOT EXISTS \w+\s+ON public\.llm_kb_capability_entries\(account_id, document_key\) "
        r"WHERE entry_type = 'kb_document_mark'",
        sql,
    ), "one mark per (account, document) must be enforced by a partial unique index"
    # The old table is read from, never written to or dropped — its live data (and the
    # still-running old llm-server pods reading/writing it during a rolling deploy) must
    # survive this migration untouched. See the migration's own header comment for why.
    assert "INSERT INTO" in sql and "llm_kb_document_categories" in sql
    assert not re.search(r"DROP\s+TABLE[^;]*llm_kb_document_categories", sql, re.IGNORECASE)
    assert not re.search(r"DELETE\s+FROM\s+(public\.)?llm_kb_document_categories", sql, re.IGNORECASE)
    assert not re.search(r"UPDATE\s+(public\.)?llm_kb_document_categories", sql, re.IGNORECASE)
