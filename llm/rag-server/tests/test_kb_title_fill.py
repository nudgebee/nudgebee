"""Filling a ServiceNow page's title onto every chunk: additive, idempotent, no LLM, text never replaced."""

import pytest
from qdrant_client import QdrantClient, models

from rag.core.documents import kb_title_fill as fill

COLLECTION = "int-1_knowledge_base"


def _uid(n):
    return f"{n:08x}-1f2f-856d-0f53-86a492158eab"


def _client(chunks, collection=COLLECTION):
    """chunks: (text, metadata) pairs, ids 1..n."""
    client = QdrantClient(":memory:")
    client.create_collection(collection, vectors_config=models.VectorParams(size=2, distance=models.Distance.COSINE))
    client.upsert(
        collection,
        points=[
            models.PointStruct(id=_uid(i), vector=[0.1, 0.2], payload={"page_content": text, "metadata": md})
            for i, (text, md) in enumerate(chunks, start=1)
        ],
    )
    return client


def _titles(client, collection=COLLECTION):
    pts, _ = client.scroll(collection, limit=100, with_payload=True)
    return {str(p.id): (p.payload["metadata"] or {}).get("title") for p in pts}


def test_title_from_text():
    assert fill.title_from_text("Title: Disk Warning Alarm Runbook\n\nbody") == "Disk Warning Alarm Runbook"
    assert fill.title_from_text("Title: one line") == "one line"
    assert fill.title_from_text("Title:   \nbody") is None
    assert fill.title_from_text("mos DB accoun, more text") is None
    assert fill.title_from_text("") is None
    assert fill.title_from_text(None) is None


def test_every_chunk_of_a_page_gets_the_title_from_its_first_chunk():
    client = _client(
        [
            ("Title: Alarm Runbook\n\nstep one", {"sys_id": "a", "chunk_index": 0}),
            ("step two", {"sys_id": "a", "chunk_index": 1}),
            ("step three", {"sys_id": "a", "chunk_index": 2}),
            ("Title: Other page\n\nbody", {"sys_id": "b"}),
        ]
    )

    result = fill.fill_collection(client, COLLECTION)

    titles = _titles(client)
    assert [titles[_uid(i)] for i in (1, 2, 3)] == ["Alarm Runbook"] * 3
    assert titles[_uid(4)] == "Other page"
    assert result == {"pages_to_update": 2, "pages_updated": 2, "chunks_written": 4, "errors": 0}


def test_the_first_chunks_own_title_spreads_to_untitled_siblings():
    client = _client(
        [
            ("Title: Text title\n\nx", {"sys_id": "a", "chunk_index": 0, "title": "Kept"}),
            ("y", {"sys_id": "a", "chunk_index": 1}),
        ]
    )

    fill.fill_collection(client, COLLECTION)

    assert _titles(client) == {_uid(1): "Kept", _uid(2): "Kept"}


def test_page_text_is_left_alone():
    client = _client(
        [("Title: T\n\nbody", {"sys_id": "a", "chunk_index": 0}), ("tail", {"sys_id": "a", "chunk_index": 1})]
    )

    fill.fill_collection(client, COLLECTION)

    points, _ = client.scroll(COLLECTION, limit=10, with_payload=True)
    assert sorted(p.payload["page_content"] for p in points) == ["Title: T\n\nbody", "tail"]


def test_a_second_run_writes_nothing():
    client = _client(
        [("Title: T\n\nbody", {"sys_id": "a", "chunk_index": 0}), ("tail", {"sys_id": "a", "chunk_index": 1})]
    )
    fill.fill_collection(client, COLLECTION)

    again = fill.fill_collection(client, COLLECTION)

    assert again == {"pages_to_update": 0, "pages_updated": 0, "chunks_written": 0, "errors": 0}


def test_pages_without_a_title_line_are_skipped():
    # Confluence stamps its titles when it scrapes; text that does not start with "Title:" has none to read.
    client = _client([("just text", {"page_id": 9, "chunk_index": 0}), ("more", {"page_id": 9, "chunk_index": 1})])

    result = fill.fill_collection(client, COLLECTION)

    assert result == {"pages_to_update": 1, "pages_updated": 0, "chunks_written": 0, "errors": 0}
    assert _titles(client) == {_uid(1): None, _uid(2): None}


def test_chunks_of_different_pages_never_share_a_title():
    client = _client(
        [
            ("Title: First\n\na", {"sys_id": "a", "chunk_index": 0}),
            ("a2", {"sys_id": "a", "chunk_index": 1}),
            ("Title: Second\n\nb", {"sys_id": "b", "chunk_index": 0}),
            ("b2", {"sys_id": "b", "chunk_index": 1}),
        ]
    )

    fill.fill_collection(client, COLLECTION)

    assert _titles(client) == {_uid(1): "First", _uid(2): "First", _uid(3): "Second", _uid(4): "Second"}


def test_a_figure_chunk_gets_the_title_but_never_supplies_it():
    client = _client(
        [
            ("Diagram of the queue", {"sys_id": "a", "is_figure": True}),
            ("Title: Queue Runbook\n\nsteps", {"sys_id": "a", "chunk_index": 0}),
        ]
    )

    fill.fill_collection(client, COLLECTION)

    assert _titles(client) == {_uid(1): "Queue Runbook", _uid(2): "Queue Runbook"}


def test_run_skips_manual_kbs_and_missing_collections(monkeypatch):
    from types import SimpleNamespace

    client = _client([("Title: T\n\nb", {"sys_id": "a", "chunk_index": 0}), ("c", {"sys_id": "a", "chunk_index": 1})])
    kbs = [
        SimpleNamespace(name="kb_manual", is_manual=True, account_ids=["x"]),
        SimpleNamespace(name="gone_knowledge_base", is_manual=False, account_ids=["x"]),
        SimpleNamespace(name=COLLECTION, is_manual=False, account_ids=["x"]),
    ]
    from rag.core.documents import kb_capability_backfill

    monkeypatch.setattr(kb_capability_backfill, "_pending_knowledgebases", lambda: kbs)

    summary = fill.run(client)

    assert summary == {
        "collections_scanned": 2,
        "pages_to_update": 1,
        "pages_updated": 1,
        "chunks_written": 2,
        "errors": 0,
    }


def test_run_counts_a_failing_collection_and_goes_on(monkeypatch):
    from types import SimpleNamespace

    client = _client([("Title: T\n\nb", {"sys_id": "a", "chunk_index": 0})])
    kbs = [
        SimpleNamespace(name="boom_knowledge_base", is_manual=False, account_ids=["x"]),
        SimpleNamespace(name=COLLECTION, is_manual=False, account_ids=["x"]),
    ]
    from rag.core.documents import kb_capability_backfill

    monkeypatch.setattr(kb_capability_backfill, "_pending_knowledgebases", lambda: kbs)
    real = client.collection_exists

    def exists(name):
        if name == "boom_knowledge_base":
            raise RuntimeError("qdrant down")
        return real(name)

    monkeypatch.setattr(client, "collection_exists", exists)

    summary = fill.run(client)

    assert summary["errors"] == 1 and summary["pages_updated"] == 1


@pytest.mark.parametrize(
    "value,expected", [("", True), ("true", True), ("TRUE", True), ("false", False), ("False", False)]
)
def test_flag_is_on_unless_switched_off(monkeypatch, value, expected):
    if value == "":
        monkeypatch.delenv("RAG_KB_TITLE_FILL_ENABLED", raising=False)
    else:
        monkeypatch.setenv("RAG_KB_TITLE_FILL_ENABLED", value)
    assert fill.is_enabled() is expected


def test_a_renamed_page_gets_its_new_title_on_the_chunks_that_kept_the_old_one():
    # A rename changes the first line of the text, so the first chunk is new and carries the new
    # title; the unchanged chunks keep their ids, are not re-written by the scrape, and keep the old title.
    client = _client(
        [
            ("Title: New name Runbook\n\nstep one", {"sys_id": "a", "chunk_index": 0, "title": "New name Runbook"}),
            ("step two", {"sys_id": "a", "chunk_index": 1, "title": "Old name notes"}),
            ("step three", {"sys_id": "a", "chunk_index": 2, "title": "Old name notes"}),
        ]
    )

    result = fill.fill_collection(client, COLLECTION)

    assert set(_titles(client).values()) == {"New name Runbook"}
    assert result["chunks_written"] == 2 and result["pages_updated"] == 1


def test_titles_that_already_agree_are_left_alone():
    client = _client(
        [
            ("Title: Same\n\nx", {"sys_id": "a", "chunk_index": 0, "title": "Same"}),
            ("y", {"sys_id": "a", "chunk_index": 1, "title": "Same"}),
        ]
    )

    assert fill.fill_collection(client, COLLECTION)["pages_to_update"] == 0


def test_when_the_first_chunk_says_nothing_an_existing_title_on_another_chunk_is_used():
    client = _client(
        [
            ("no title line here", {"page_id": 7, "chunk_index": 0}),
            ("later chunk", {"page_id": 7, "chunk_index": 1, "title": "Kept from a later chunk"}),
        ]
    )

    fill.fill_collection(client, COLLECTION)

    assert set(_titles(client).values()) == {"Kept from a later chunk"}


def test_one_page_failing_to_write_does_not_stop_the_others(monkeypatch):
    client = _client(
        [
            ("Title: First\n\na", {"sys_id": "a", "chunk_index": 0}),
            ("a2", {"sys_id": "a", "chunk_index": 1}),
            ("Title: Second\n\nb", {"sys_id": "b", "chunk_index": 0}),
            ("b2", {"sys_id": "b", "chunk_index": 1}),
        ]
    )
    real = client.set_payload
    calls = {"n": 0}

    def flaky(**kwargs):
        calls["n"] += 1
        if calls["n"] == 1:
            raise RuntimeError("qdrant hiccup")
        return real(**kwargs)

    monkeypatch.setattr(client, "set_payload", flaky)

    result = fill.fill_collection(client, COLLECTION)

    assert result["errors"] == 1 and result["pages_updated"] == 1
    assert sorted(v for v in _titles(client).values() if v) in (["Second", "Second"], ["First", "First"])


def test_filling_a_title_keeps_the_rest_of_the_chunks_metadata():
    # set_payload(key="metadata") merges into the nested object; it must not replace it.
    client = _client(
        [
            (
                "Title: T\n\nbody",
                {"sys_id": "a", "chunk_index": 0, "kind": "sop", "parent_id": "p1", "url": "https://x/1"},
            ),
            (
                "tail",
                {
                    "sys_id": "a",
                    "chunk_index": 1,
                    "is_figure": False,
                    "shape": "decision_tree",
                    "capabilities": ["k8s.restart"],
                },
            ),
        ]
    )

    fill.fill_collection(client, COLLECTION)

    points = {str(p.id): p.payload["metadata"] for p in client.scroll(COLLECTION, limit=10, with_payload=True)[0]}
    assert points[_uid(1)] == {
        "sys_id": "a",
        "chunk_index": 0,
        "kind": "sop",
        "parent_id": "p1",
        "url": "https://x/1",
        "title": "T",
    }
    assert points[_uid(2)] == {
        "sys_id": "a",
        "chunk_index": 1,
        "is_figure": False,
        "shape": "decision_tree",
        "capabilities": ["k8s.restart"],
        "title": "T",
    }
