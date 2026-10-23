"""Seeding per-page labels from Qdrant into Postgres: one row per labelled, keyable page; no LLM; idempotent."""

from types import SimpleNamespace

import pytest
from qdrant_client import QdrantClient, models

from rag.core.documents import kb_capability_backfill as backfill
from rag.core.documents import kb_document_seed as seed
from rag.core.utils import db_query

COLLECTION = "int-1_knowledge_base"
LABELS = {"kind": "sop", "shape": "decision_tree", "capabilities": ["k8s.restart"]}


def _uid(n):
    return f"{n:08x}-1f2f-856d-0f53-86a492158eab"


def _hex(n):
    return _uid(n).replace("-", "")


def _client(points, collection=COLLECTION):
    client = QdrantClient(":memory:")
    client.create_collection(collection, vectors_config=models.VectorParams(size=2, distance=models.Distance.COSINE))
    client.upsert(
        collection,
        points=[
            models.PointStruct(id=_uid(i), vector=[0.1, 0.2], payload={"page_content": "x", "metadata": md})
            for i, md in enumerate(points, start=1)
        ],
    )
    return client


def test_one_row_per_labelled_page_keyed_like_llm_server():
    client = _client(
        [
            {"page_id": 11, "chunk_index": 0, **LABELS},
            {"page_id": 11, "chunk_index": 1, **LABELS},
            {"sys_id": "abc", "kind": "knowledge", "shape": "narrative_reference", "capabilities": []},
        ]
    )
    rows = {r["document_key"]: r for r in seed.page_rows(client, COLLECTION)}
    assert set(rows) == {f"{COLLECTION}|11", f"{COLLECTION}|abc"}
    assert rows[f"{COLLECTION}|11"] == {
        "collection_name": COLLECTION,
        "document_key": f"{COLLECTION}|11",
        "kind": "sop",
        "shape": "decision_tree",
        "capabilities": ["k8s.restart"],
        "source_version": _hex(1),
    }
    assert rows[f"{COLLECTION}|abc"]["kind"] == "knowledge"


def test_the_first_chunk_speaks_for_a_page_whose_chunks_disagree():
    client = _client(
        [
            {"page_id": 5, "chunk_index": 1, "kind": "knowledge", "shape": "narrative_reference"},
            {"page_id": 5, "chunk_index": 0, **LABELS},
        ]
    )
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["kind"] == "sop"


def test_unlabelled_and_unkeyable_chunks_are_skipped():
    client = _client(
        [
            {"page_id": 1},  # never classified
            {**LABELS},  # labelled, but no id and no url: no key
            {"page_id": 2, "kind": "bogus"},  # a kind outside the classified set
            {"url": "https://wiki.example.com/p/9", **LABELS},  # url fallback keys it
        ]
    )
    rows = seed.page_rows(client, COLLECTION)
    assert [r["document_key"] for r in rows] == [f"{COLLECTION}|https://wiki.example.com/p/9"]


def test_the_scroll_never_asks_for_the_page_text():
    client = _client([{"page_id": 1, **LABELS}])
    seen = []
    real = client.scroll
    client.scroll = lambda **kw: (seen.append(kw), real(**kw))[1]
    seed.page_rows(client, COLLECTION)
    assert all(isinstance(kw["with_payload"], models.PayloadSelectorInclude) for kw in seen)


def test_run_seeds_every_collection_with_a_collection_and_reports(monkeypatch):
    client = _client([{"page_id": 1, **LABELS}, {"page_id": 2, **LABELS}])
    monkeypatch.setattr(
        backfill,
        "_pending_knowledgebases",
        lambda: [
            backfill.KBCollection(COLLECTION, False, ["a"]),
            backfill.KBCollection("missing_knowledge_base", False, ["a"]),
        ],
    )
    inserted = []
    monkeypatch.setattr(db_query, "seed_kb_document_labels", lambda rows: inserted.extend(rows) or len(rows))
    summary = seed.run(client)
    assert summary == {"collections_scanned": 2, "pages_seen": 2, "rows_inserted": 2, "errors": 0}
    assert {r["document_key"] for r in inserted} == {f"{COLLECTION}|1", f"{COLLECTION}|2"}


def test_a_failing_collection_is_counted_and_does_not_stop_the_rest(monkeypatch):
    client = _client([{"page_id": 1, **LABELS}])
    monkeypatch.setattr(
        backfill,
        "_pending_knowledgebases",
        lambda: [
            backfill.KBCollection(COLLECTION, False, ["a"]),
            backfill.KBCollection("x_knowledge_base", False, ["a"]),
        ],
    )

    def boom(rows):
        raise RuntimeError("db down")

    monkeypatch.setattr(db_query, "seed_kb_document_labels", boom)
    assert seed.run(client)["errors"] >= 1


def test_listing_failure_aborts_with_an_error(monkeypatch):
    def boom():
        raise RuntimeError("db down")

    monkeypatch.setattr(backfill, "_pending_knowledgebases", boom)
    assert seed.run(_client([]))["errors"] == 1


@pytest.mark.parametrize(
    "value,expected", [("true", True), ("TRUE", True), ("false", False), ("False", False), ("", False)]
)
def test_gate_follows_an_explicit_setting(monkeypatch, value, expected):
    monkeypatch.setenv("RAG_KB_DOCUMENT_SEED_ENABLED", value)
    assert seed.is_enabled() is expected


def test_gate_defaults_on(monkeypatch):
    monkeypatch.delenv("RAG_KB_DOCUMENT_SEED_ENABLED", raising=False)
    assert seed.is_enabled() is True


def test_a_manual_kb_is_one_row_keyed_by_its_collection_with_no_version():
    client = _client(
        [{"chunk_index": 1, "kind": "knowledge", "shape": "narrative_reference"}, {"chunk_index": 0, **LABELS}],
        collection="kb_9f1c",
    )
    (row,) = seed.page_rows(client, "kb_9f1c")
    assert row["document_key"] == "kb_9f1c"
    assert row["kind"] == "sop"
    assert row["source_version"] is None


def test_a_split_page_takes_its_version_from_the_text_chunk_parent_id():
    client = _client(
        [
            {"page_id": 7, "chunk_index": 0, "parent_id": "abc123", **LABELS},
            {"page_id": 7, "chunk_index": 1, "parent_id": "abc123", **LABELS},
        ]
    )
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["source_version"] == "abc123"


def test_a_figure_chunk_never_speaks_for_a_page_that_has_a_labelled_text_chunk():
    client = _client(
        [
            {"page_id": 8, "is_figure": True, "chunk_index": 0, "kind": "knowledge", "shape": "narrative_reference"},
            {"page_id": 8, "chunk_index": 3, **LABELS},
        ]
    )
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["kind"] == "sop"
    assert row["source_version"] == _hex(2)


def test_a_page_whose_only_labelled_chunk_is_a_figure_is_seeded_with_no_version():
    client = _client([{"page_id": 9, "is_figure": True, **LABELS}])
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["source_version"] is None


def test_qdrant_collections_are_listed_once_not_probed_per_collection(monkeypatch):
    client = _client([{"page_id": 1, **LABELS}])
    calls = {"get_collections": 0}
    real = client.get_collections
    client.get_collections = lambda: (calls.__setitem__("get_collections", calls["get_collections"] + 1), real())[1]
    client.collection_exists = lambda name: (_ for _ in ()).throw(
        AssertionError("collection_exists must not be called")
    )
    monkeypatch.setattr(
        backfill,
        "_pending_knowledgebases",
        lambda: [
            backfill.KBCollection(COLLECTION, False, ["a"]),
            backfill.KBCollection("gone_knowledge_base", False, ["a"]),
        ],
    )
    monkeypatch.setattr(db_query, "seed_kb_document_labels", lambda rows: len(rows))

    assert seed.run(client)["rows_inserted"] == 1
    assert calls["get_collections"] == 1


def test_a_qdrant_listing_failure_aborts_with_an_error(monkeypatch):
    client = _client([])
    client.get_collections = lambda: (_ for _ in ()).throw(RuntimeError("qdrant down"))
    monkeypatch.setattr(backfill, "_pending_knowledgebases", lambda: [backfill.KBCollection(COLLECTION, False, ["a"])])

    assert seed.run(client)["errors"] == 1


def test_a_batch_is_one_multi_row_insert_that_reports_rows_actually_inserted(monkeypatch):
    executed = []

    class _Result:
        rowcount = 1

    class _Conn:
        def execute(self, query, params=None):
            executed.append((str(query), params))
            return _Result()

        def begin(self):
            return self

        def __enter__(self):
            return self

        def __exit__(self, *exc):
            return False

    class _Engine:
        def connect(self):
            return _Conn()

    monkeypatch.setattr(db_query, "engine", _Engine())
    rows = [
        {
            "collection_name": "c",
            "document_key": f"c|{i}",
            "kind": "sop",
            "shape": None,
            "capabilities": ["a"],
            "source_version": "v",
        }
        for i in range(3)
    ]

    assert db_query.seed_kb_document_labels(rows) == 1  # whatever the driver says was inserted
    assert len(executed) == 1, "one statement for the whole batch"
    sql, params = executed[0]
    assert sql.count("('kb_document', ") == 3, "one VALUES tuple per row"
    assert "ON CONFLICT (collection_name, document_key) WHERE entry_type = 'kb_document' DO UPDATE" in sql
    # An existing row changes in exactly one case: its empty capabilities take non-empty ones.
    # kind, shape and source_version are never in the SET list.
    assert "SET capabilities = EXCLUDED.capabilities, updated_at = NOW()" in sql
    assert "WHERE llm_kb_capability_entries.capabilities = '{}' AND EXCLUDED.capabilities <> '{}'" in sql
    assert "kinds = " not in sql and "shapes = " not in sql and "source_version = " not in sql
    assert params["document_key_2"] == "c|2" and params["shapes_0"] == [] and params["kinds_1"] == ["sop"]
    assert db_query.seed_kb_document_labels([]) == 0


def test_an_explicit_none_chunk_index_falls_back_to_line_number():
    client = _client(
        [
            {"page_id": 4, "chunk_index": None, "line_number": 9, "kind": "knowledge", "shape": "narrative_reference"},
            {"page_id": 4, "chunk_index": None, "line_number": 2, **LABELS},
        ]
    )
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["kind"] == "sop", "line_number 2 comes before 9, so that chunk speaks for the page"


@pytest.mark.parametrize("value", ["k8s.restart", {"a": 1}, 7, None])
def test_capabilities_that_are_not_a_list_are_not_split_or_kept(value):
    client = _client([{"page_id": 6, "kind": "sop", "shape": "decision_tree", "capabilities": value}])
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["capabilities"] == []


def test_non_string_entries_of_a_capability_list_are_dropped():
    client = _client([{"page_id": 6, "kind": "sop", "capabilities": ["a", 3, None, "b"]}])
    (row,) = seed.page_rows(client, COLLECTION)
    assert row["capabilities"] == ["a", "b"]


def test_a_malformed_point_is_skipped_not_fatal_to_the_collection():
    client = QdrantClient(":memory:")
    client.create_collection(COLLECTION, vectors_config=models.VectorParams(size=2, distance=models.Distance.COSINE))
    client.upsert(
        COLLECTION,
        points=[
            models.PointStruct(id=_uid(1), vector=[0.1, 0.2], payload={"metadata": "not-a-dict"}),
            models.PointStruct(id=_uid(2), vector=[0.1, 0.2], payload={"page_content": "x"}),
            models.PointStruct(
                id=_uid(3), vector=[0.1, 0.2], payload={"metadata": {"page_id": 5, "url": 42, **LABELS}}
            ),
        ],
    )
    rows = seed.page_rows(client, COLLECTION)
    assert [r["document_key"] for r in rows] == [f"{COLLECTION}|5"], "the well-formed point is still seeded"


def test_a_batch_never_carries_the_same_key_twice(monkeypatch):
    # ON CONFLICT DO UPDATE cannot affect one row twice in a statement.
    executed = []

    class _Result:
        rowcount = 1

    class _Conn:
        def execute(self, statement, params):
            executed.append((str(statement), params))
            return _Result()

        def begin(self):
            from contextlib import nullcontext

            return nullcontext()

        def __enter__(self):
            return self

        def __exit__(self, *exc):
            return False

    monkeypatch.setattr(db_query, "engine", SimpleNamespace(connect=lambda: _Conn()))
    row = {"collection_name": "c", "document_key": "k", "kind": "sop", "shape": None, "source_version": None}
    db_query.seed_kb_document_labels(
        [{**row, "capabilities": []}, {**row, "capabilities": ["a"]}, {**row, "capabilities": ["b"]}]
    )

    ((sql, params),) = executed
    assert sql.count("('kb_document', ") == 1
    assert params["capabilities_0"] == ["a"], "the first row with capabilities wins over an empty one"
