"""KB capability backfill: which documents get classified, and where the labels land.

Five properties carry the weight here.

A chunk is classified iff its Qdrant payload carries ``metadata.kind`` — there is
no second store to compare against. So a fully labelled collection costs one
filtered scroll (limit 1, no payload) per run, and a second account joining a
shared integration collection, or a second run, costs no LLM call at all.

The unit is the source document: a manual KB collection is one document, an
integration collection is one per page, identified by the id the scraper stamped
(page_id / sys_id / source_id), NOT by parent_id — after an edit the unchanged
chunks of a page keep the old parent_id while the new ones carry a new one, and
splitting the page there would classify half of it on its own. One LLM call
labels every chunk of a document, in place, with set_payload (metadata MERGED).

Budget is counted in LLM calls actually made: a classification that failed still
spent one, an account with no LLM provider spent none. An exhausted budget stops
classification but not the loop — missing rollups are still rebuilt (0 LLM calls).

The rollup row the Knowledge Base tab reads is derived from the payload: one
refresh per labelled collection, and a collection that is fully labelled but has
no row gets it rebuilt. If the list of existing rollups cannot be read, nothing
is rebuilt rather than guessed.

Only one replica runs at a time (a Postgres advisory lock), and the background
worker retries a failed run with a bounded, doubling delay.
"""

import threading
from contextlib import contextmanager
from types import SimpleNamespace

import pytest
from qdrant_client import QdrantClient, models

from rag.core.documents import embedding_migration
from rag.core.documents import kb_capability_backfill as backfill
from rag.core.documents import processing
from rag.core.utils import db_query

_REAL_REFRESH = processing.refresh_kb_collection_summary

UUID_A = "1b670384-1f2f-856d-0f53-86a492158eab"
HEX_A = "1b6703841f2f856d0f5386a492158eab"
UUID_B = "2b670384-1f2f-856d-0f53-86a492158eab"
HEX_B = "2b6703841f2f856d0f5386a492158eab"
UUID_C = "3b670384-1f2f-856d-0f53-86a492158eab"
HEX_C = "3b6703841f2f856d0f5386a492158eab"

LABELS = {"kind": "sop", "shape": "decision_tree", "capabilities": ["k8s.restart"]}
OTHER_LABELS = {"kind": "knowledge", "shape": "narrative_reference", "capabilities": []}


def _uid(n):
    return f"{n:08x}-1f2f-856d-0f53-86a492158eab"


def _labelled(**extra):
    return {**LABELS, **extra}


def _fake_engine(rows_by_query_substring):
    """Minimal SQLAlchemy engine stand-in. `rows_by_query_substring` maps a
    substring expected in the SQL text to the list of row objects `execute`
    should return when that text appears in the query."""

    @contextmanager
    def connect():
        class _Conn:
            def execute(self, query, params=None):
                sql = str(query)
                for substring, rows in rows_by_query_substring.items():
                    if substring in sql:
                        return rows
                raise AssertionError(f"unexpected query: {sql}")

        yield _Conn()

    return SimpleNamespace(connect=connect)


class _Point:
    """A scrolled point, for the tests of the pure grouping helpers."""

    def __init__(self, pid, content="some chunk text", metadata=None):
        self.id = pid
        self.payload = {"page_content": content, "metadata": dict(metadata or {"source": "manual"})}


def _pt(pid, content="some chunk text", md=None):
    """A point to load into the real in-memory Qdrant; `md=None` leaves the
    payload without any `metadata` key at all."""
    payload = {"page_content": content}
    if md is not None:
        payload["metadata"] = dict(md)
    return models.PointStruct(id=pid, vector=[0.1, 0.2], payload=payload)


class _Client:
    """A real in-memory Qdrant that records every scroll and every payload write.

    Only what the backfill is allowed to use is exposed: collection_exists,
    scroll and set_payload. Anything else it might call (upsert, delete,
    overwrite_payload, retrieve) raises AttributeError, so "only set_payload is
    written" is enforced rather than asserted. Tests read the resulting state
    through `payloads` / `metadata`, which bypass the recording.
    """

    def __init__(self, collections=None, fail_for=None):
        self.inner = QdrantClient(":memory:")
        self.fail_for = fail_for or (lambda points: False)
        self.scrolls = []
        self.set_payloads = []
        for name, points in (collections or {}).items():
            self.inner.create_collection(
                name, vectors_config=models.VectorParams(size=2, distance=models.Distance.COSINE)
            )
            if points:
                self.inner.upsert(name, points=points)

    def collection_exists(self, collection_name):
        return self.inner.collection_exists(collection_name)

    def scroll(self, **kwargs):
        self.scrolls.append(kwargs)
        return self.inner.scroll(**kwargs)

    def set_payload(self, **kwargs):
        self.set_payloads.append(dict(kwargs))
        if self.fail_for(kwargs["points"]):
            raise RuntimeError("qdrant unavailable")
        return self.inner.set_payload(**kwargs)

    def payloads(self, name):
        points, _ = self.inner.scroll(name, limit=10_000, with_payload=True)
        return {str(p.id): p.payload for p in points}

    def metadata(self, name):
        return {pid: payload.get("metadata") or {} for pid, payload in self.payloads(name).items()}

    def only_cheap_probes(self):
        """True when every request the backfill made was a filtered
        scroll for one point with no payload."""
        return bool(self.scrolls) and all(s["limit"] == 1 and s["with_payload"] is False for s in self.scrolls)


def _manual(name="kb_kb-1", accounts=("acct-1",)):
    return backfill.KBCollection(name, True, list(accounts))


def _integration(name="int-9_knowledge_base", accounts=("acct-1", "acct-2")):
    return backfill.KBCollection(name, False, list(accounts))


@pytest.fixture(autouse=True)
def rollup(monkeypatch):
    """Stand-in for the Postgres rollup: records every refresh of a collection's
    row, and serves the set of collections that already have one. Autouse so no
    test can reach a real database through it."""
    state = SimpleNamespace(refreshed=[], names=set(), names_error=None, names_reads=0)

    def _names():
        state.names_reads += 1
        if state.names_error is not None:
            raise state.names_error
        return set(state.names)

    monkeypatch.setattr("rag.core.documents.processing.refresh_kb_collection_summary", state.refreshed.append)
    monkeypatch.setattr("rag.core.utils.db_query.get_kb_summary_collection_names", _names)
    return state


@pytest.fixture
def classifier(monkeypatch):
    """Stand-in for the LLM classifier: stamps labels onto every document of
    every group it is handed (or nothing, if `fail`), and records the calls. The
    return value is the number of LLM calls made, as the real one's is: 0 for an
    account whose provider cannot be resolved."""
    state = SimpleNamespace(labels=dict(LABELS), per_document=None, fail=False, unresolvable=set(), calls=[])

    def _classify(groups, account_id, collection_name):
        state.calls.append({"groups": groups, "account_id": account_id, "collection_name": collection_name})
        if account_id in state.unresolvable:
            return 0
        if not state.fail:
            for group in groups:
                labels = state.per_document(group) if state.per_document else state.labels
                for doc in group:
                    doc.metadata.update({k: v for k, v in labels.items()})
        return len(groups)

    monkeypatch.setattr("rag.core.documents.processing._classify_source_documents", _classify)
    return state


# ---------------------------------------------------------------------------
# _pending_knowledgebases


def test_pending_knowledgebases_names_manual_and_integration_collections(monkeypatch):
    rows = [
        SimpleNamespace(id="kb-1", account_id="acct-1", kb_type="manual", integration_id=None),
        SimpleNamespace(id="kb-2", account_id="acct-2", kb_type="integration", integration_id="int-9"),
    ]
    monkeypatch.setattr(db_query, "engine", _fake_engine({"FROM llm_knowledgebases": rows}))

    pending = {kb.name: kb for kb in backfill._pending_knowledgebases()}

    assert pending["kb_kb-1"] == backfill.KBCollection("kb_kb-1", True, ["acct-1"])
    assert pending["int-9_knowledge_base"] == backfill.KBCollection("int-9_knowledge_base", False, ["acct-2"])


def test_an_integration_collection_keeps_every_account_that_shares_it(monkeypatch):
    rows = [
        SimpleNamespace(id="kb-1", account_id="acct-b", kb_type="integration", integration_id="int-9"),
        SimpleNamespace(id="kb-2", account_id="acct-a", kb_type="integration", integration_id="int-9"),
        SimpleNamespace(id="kb-3", account_id="acct-a", kb_type="integration", integration_id="int-9"),
    ]
    monkeypatch.setattr(db_query, "engine", _fake_engine({"FROM llm_knowledgebases": rows}))

    pending = backfill._pending_knowledgebases()

    assert pending == [backfill.KBCollection("int-9_knowledge_base", False, ["acct-a", "acct-b"])]


def test_only_active_enabled_knowledgebases_are_listed(monkeypatch):
    # The engine stand-in only answers a query containing this predicate.
    monkeypatch.setattr(
        db_query,
        "engine",
        _fake_engine(
            {
                "status = 'active' AND enabled = true": [
                    SimpleNamespace(id="kb-1", account_id="acct-1", kb_type="manual", integration_id=None)
                ]
            }
        ),
    )

    assert [kb.name for kb in backfill._pending_knowledgebases()] == ["kb_kb-1"]


def test_an_integration_kb_row_without_an_integration_id_is_a_manual_collection(monkeypatch):
    rows = [SimpleNamespace(id="kb-1", account_id="acct-1", kb_type="integration", integration_id=None)]
    monkeypatch.setattr(db_query, "engine", _fake_engine({"FROM llm_knowledgebases": rows}))

    assert backfill._pending_knowledgebases() == [backfill.KBCollection("kb_kb-1", True, ["acct-1"])]


# ---------------------------------------------------------------------------
# The vocabulary the backfill shares with the ingest path


def test_the_label_keys_and_kinds_are_the_ones_ingest_writes():
    assert backfill._LABEL_KEYS == processing.KB_LABEL_KEYS
    assert tuple(backfill._KINDS) == processing._KB_KINDS


def test_the_filter_the_backfill_asks_qdrant_matches_exactly_the_classified_kinds():
    condition = backfill._kind_condition()

    assert condition.key == "metadata.kind"
    assert list(condition.match.any) == list(processing._KB_KINDS)


def test_a_qdrant_point_id_is_normalised_to_the_bare_hex_ingest_hashed():
    assert backfill._chunk_id(UUID_A) == HEX_A
    assert backfill._chunk_id(HEX_A) == HEX_A


# ---------------------------------------------------------------------------
# Grouping chunks back into source documents


@pytest.mark.parametrize("position_key", ["line_number", "chunk_index"])
def test_a_manual_collection_is_one_document_in_document_order(position_key):
    points = [
        _Point(UUID_A, metadata={position_key: 3}),
        _Point(UUID_B, metadata={position_key: 1}),
        _Point(UUID_C, metadata={position_key: 2}),
    ]

    groups = backfill._group_points(_manual(), points)

    assert len(groups) == 1
    assert [p.id for p in groups[0]] == [UUID_B, UUID_C, UUID_A]


def test_a_manual_collection_ignores_any_page_id_it_carries():
    points = [_Point(UUID_A, metadata={"page_id": "p1"}), _Point(UUID_B, metadata={"page_id": "p2"})]

    assert len(backfill._group_points(_manual(), points)) == 1


def test_no_points_is_no_documents():
    assert backfill._group_points(_manual(), []) == []
    assert backfill._group_points(_integration(), []) == []


@pytest.mark.parametrize("key", ["source_id", "page_id", "sys_id"])
def test_an_integration_collection_is_one_document_per_scraper_source_id(key):
    points = [
        _Point(UUID_A, metadata={key: "p1", "chunk_index": 1}),
        _Point(UUID_B, metadata={key: "p1", "chunk_index": 0}),
        _Point(UUID_C, metadata={key: "p2"}),
    ]

    groups = backfill._group_points(_integration(), points)

    assert sorted(len(g) for g in groups) == [1, 2]
    two_chunk_page = next(g for g in groups if len(g) == 2)
    assert [p.id for p in two_chunk_page] == [UUID_B, UUID_A]


def test_the_edited_page_is_one_document_even_though_its_chunks_carry_different_parent_ids():
    # After an edit the chunks that did not change keep the old parent_id (a hash
    # of the page's whole old text); the new ones carry a hash of the new text.
    points = [
        _Point(UUID_A, metadata={"page_id": "p1", "parent_id": "old-hash", "chunk_index": 0, **LABELS}),
        _Point(UUID_B, metadata={"page_id": "p1", "parent_id": "new-hash", "chunk_index": 1}),
    ]

    groups = backfill._group_points(_integration(), points)

    assert len(groups) == 1
    assert [p.id for p in groups[0]] == [UUID_A, UUID_B]


def test_two_pages_sharing_a_parent_id_are_still_two_documents():
    # parent_id is only the fallback: the scraper's own id wins when there is one.
    points = [
        _Point(UUID_A, metadata={"page_id": "p1", "parent_id": "same"}),
        _Point(UUID_B, metadata={"page_id": "p2", "parent_id": "same"}),
    ]

    assert len(backfill._group_points(_integration(), points)) == 2


def test_figure_descriptions_are_grouped_with_the_page_they_came_from():
    # Figure-description points are stamped {page_id, url, is_figure} and no
    # parent_id, so grouping on parent_id alone would leave each one on its own.
    points = [
        _Point(UUID_A, metadata={"page_id": "p1", "parent_id": "h1", "chunk_index": 0}),
        _Point(UUID_B, metadata={"page_id": "p1", "url": "https://x/fig.png", "is_figure": True}),
        _Point(UUID_C, metadata={"page_id": "p2"}),
    ]

    groups = backfill._group_points(_integration(), points)

    assert sorted(len(g) for g in groups) == [1, 2]
    page = next(g for g in groups if len(g) == 2)
    assert {p.id for p in page} == {UUID_A, UUID_B}


def test_points_with_no_scraper_id_fall_back_to_parent_id_then_to_their_own_id():
    points = [
        _Point(UUID_A, metadata={"parent_id": "h1", "chunk_index": 1}),
        _Point(UUID_B, metadata={"parent_id": "h1", "chunk_index": 0}),
        _Point(UUID_C, metadata={}),  # a page small enough to be a single point
    ]

    groups = backfill._group_points(_integration(), points)

    assert sorted(len(g) for g in groups) == [1, 2]
    split_page = next(g for g in groups if len(g) == 2)
    assert [p.id for p in split_page] == [UUID_B, UUID_A]


def test_a_blank_scraper_id_does_not_glue_unrelated_points_together():
    points = [_Point(UUID_A, metadata={"page_id": ""}), _Point(UUID_B, metadata={"page_id": None})]

    assert len(backfill._group_points(_integration(), points)) == 2


def test_documents_handed_to_the_classifier_are_label_free_copies_with_hex_ids():
    point = _Point(UUID_A, "the text", {"source": "confluence", "page_id": "p1", **LABELS})

    (doc,) = backfill._to_documents([point])

    assert doc.id == HEX_A
    assert doc.page_content == "the text"
    assert doc.metadata == {"source": "confluence", "page_id": "p1"}
    doc.metadata["kind"] = "knowledge"
    assert point.payload["metadata"]["kind"] == "sop"  # a copy, the scrolled point is not touched


# ---------------------------------------------------------------------------
# _backfill_collection: the steady state


def test_a_fully_labelled_collection_costs_one_filtered_request_and_no_llm_call(classifier, rollup):
    client = _Client({"kb_kb-1": [_pt(UUID_A, md=_labelled()), _pt(UUID_B, md=_labelled(source="manual"))]})

    result = backfill._backfill_collection(client, _manual(), budget=100, summarised={"kb_kb-1"})

    assert result == backfill.CollectionResult()
    (scroll,) = client.scrolls
    assert scroll["limit"] == 1 and scroll["with_payload"] is False and scroll["with_vectors"] is False
    (condition,) = scroll["scroll_filter"].must_not  # "is there a point WITHOUT a kind?"
    assert condition.key == "metadata.kind" and list(condition.match.any) == ["sop", "knowledge"]
    assert classifier.calls == [] and client.set_payloads == [] and rollup.refreshed == []


def test_a_kind_outside_the_classified_set_counts_as_unlabelled(classifier):
    client = _Client({"kb_kb-1": [_pt(UUID_A, md={"kind": "garbage", "shape": "decision_tree"})]})

    result = backfill._backfill_collection(client, _manual(), budget=100, summarised={"kb_kb-1"})

    assert result.labelled_documents == 1
    assert client.metadata("kb_kb-1")[UUID_A]["kind"] == "sop"


def test_a_kb_with_no_collection_is_skipped_not_failed(classifier, rollup):
    # An integration KB can be "active" with nothing loaded (its scraper flips
    # the status even when it found no documents), so no collection exists.
    client = _Client({})

    result = backfill._backfill_collection(client, _integration(), budget=100, summarised=set())

    assert result == backfill.CollectionResult()
    assert client.scrolls == [] and classifier.calls == [] and rollup.refreshed == []


# ---------------------------------------------------------------------------
# _backfill_collection: classifying and labelling


def test_an_outstanding_manual_kb_is_one_llm_call_and_every_chunk_is_labelled(classifier, rollup):
    points = [
        _pt(UUID_C, "three", {"source": "manual", "chunk_index": 2}),
        _pt(UUID_A, "one", {"source": "manual", "chunk_index": 0}),
        _pt(UUID_B, "two", {"source": "manual", "chunk_index": 1}),
    ]
    client = _Client({"kb_kb-1": points})

    result = backfill._backfill_collection(client, _manual(), budget=100)

    (call,) = classifier.calls
    assert call["account_id"] == "acct-1" and call["collection_name"] == "kb_kb-1"
    (document,) = call["groups"]  # the whole manual KB is one document, in document order
    assert [(d.id, d.page_content) for d in document] == [(HEX_A, "one"), (HEX_B, "two"), (HEX_C, "three")]
    assert result == backfill.CollectionResult(attempted=1, labelled_documents=1, labelled_chunks=3, errors=0)
    (write,) = client.set_payloads
    assert write["key"] == "metadata" and write["payload"] == LABELS
    assert sorted(write["points"]) == [UUID_A, UUID_B, UUID_C]
    assert rollup.refreshed == ["kb_kb-1"]
    assert set(client.metadata("kb_kb-1")) == {UUID_A, UUID_B, UUID_C}
    assert all(md["kind"] == "sop" for md in client.metadata("kb_kb-1").values())


def test_the_labels_are_merged_into_the_points_metadata_and_the_text_is_untouched(classifier):
    client = _Client(
        {
            "kb_kb-1": [
                _pt(UUID_A, "restart it", {"source": "manual", "line_number": 1}),
                _pt(UUID_B, "no metadata at all"),
            ]
        }
    )

    result = backfill._backfill_collection(client, _manual(), budget=100)

    assert result.labelled_chunks == 2
    by_id = client.payloads("kb_kb-1")
    # Existing metadata survives, labels are added beside it...
    assert by_id[UUID_A]["metadata"] == {"source": "manual", "line_number": 1, **LABELS}
    # ...the text is untouched, and a point with no metadata gets one.
    assert by_id[UUID_A]["page_content"] == "restart it"
    assert by_id[UUID_B]["metadata"] == LABELS
    # ...and the labels are what a metadata_filter on the payload matches at query time.
    kind_is_sop = models.Filter(must=[models.FieldCondition(key="metadata.kind", match=models.MatchValue(value="sop"))])
    assert len(client.inner.scroll("kb_kb-1", scroll_filter=kind_is_sop, limit=10)[0]) == 2


def test_a_half_labelled_manual_kb_is_relabelled_as_a_whole(classifier):
    stale = {"source": "manual", "chunk_index": 0, **OTHER_LABELS}
    client = _Client(
        {"kb_kb-1": [_pt(UUID_A, "old", stale), _pt(UUID_B, "new", {"source": "manual", "chunk_index": 1})]}
    )

    result = backfill._backfill_collection(client, _manual(), budget=100)

    assert len(classifier.calls) == 1
    assert result.labelled_documents == 1 and result.labelled_chunks == 2
    assert {md["kind"] for md in client.metadata("kb_kb-1").values()} == {"sop"}  # one consistent classification


def test_stale_label_keys_are_stripped_from_what_the_classifier_sees(classifier):
    # A labelled chunk of a half-labelled document carries the previous run's
    # labels. If the classifier now fails, those must not be read back as its answer.
    classifier.fail = True
    stale = {"source": "manual", "chunk_index": 0, **OTHER_LABELS}
    client = _Client(
        {"kb_kb-1": [_pt(UUID_A, "old", stale), _pt(UUID_B, "new", {"source": "manual", "chunk_index": 1})]}
    )

    result = backfill._backfill_collection(client, _manual(), budget=100)

    handed_over = classifier.calls[0]["groups"][0]
    assert [d.metadata for d in handed_over] == [
        {"source": "manual", "chunk_index": 0},
        {"source": "manual", "chunk_index": 1},
    ]
    assert result.labelled_documents == 0 and client.set_payloads == []
    assert client.metadata("kb_kb-1")[UUID_A]["kind"] == "knowledge"  # the stored label is untouched


def test_a_document_the_classifier_could_not_label_stays_outstanding(classifier, rollup):
    classifier.fail = True
    client = _Client({"kb_kb-1": [_pt(UUID_A)]})

    result = backfill._backfill_collection(client, _manual(), budget=100)

    assert result == backfill.CollectionResult(attempted=1)  # the call was made, so it is charged
    assert client.set_payloads == [] and rollup.refreshed == []
    # Nothing marks it done, so the next run attempts it again.
    classifier.fail = False
    assert backfill._backfill_collection(client, _manual(), budget=100).labelled_documents == 1


def test_a_failed_payload_write_leaves_the_document_outstanding(classifier, rollup):
    client = _Client({"kb_kb-1": [_pt(UUID_A)]}, fail_for=lambda points: True)

    result = backfill._backfill_collection(client, _manual(), budget=100)

    assert result.errors == 1 and result.labelled_documents == 0 and result.labelled_chunks == 0
    assert rollup.refreshed == []  # nothing was labelled, so the rollup has nothing new to say
    assert "kind" not in client.metadata("kb_kb-1")[UUID_A]
    # The point is still unlabelled, so the next run retries it.
    client.fail_for = lambda points: False
    assert backfill._backfill_collection(client, _manual(), budget=100).labelled_documents == 1


def test_one_documents_failed_write_does_not_stop_the_others(classifier, rollup):
    points = [_pt(UUID_A, md={"page_id": "p1"}), _pt(UUID_B, md={"page_id": "p2"})]
    client = _Client({"int-9_knowledge_base": points}, fail_for=lambda pts: UUID_A in pts)

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    assert result.errors == 1 and result.labelled_documents == 1 and result.labelled_chunks == 1
    assert "kind" not in client.metadata("int-9_knowledge_base")[UUID_A]
    assert client.metadata("int-9_knowledge_base")[UUID_B]["kind"] == "sop"
    assert rollup.refreshed == ["int-9_knowledge_base"]


def test_an_account_with_no_llm_provider_spends_no_budget_and_writes_nothing(classifier, rollup):
    classifier.unresolvable = {"acct-1"}
    client = _Client({"int-9_knowledge_base": [_pt(UUID_A, md={"page_id": "p1"})]})

    result = backfill._backfill_collection(client, _integration(), budget=100)

    assert result == backfill.CollectionResult()  # attempted == 0: nothing was sent to an LLM
    assert client.set_payloads == [] and rollup.refreshed == []


def test_the_first_account_supplies_the_llm_and_labels_are_written_once_for_all_accounts(classifier):
    client = _Client({"int-9_knowledge_base": [_pt(UUID_A, md={"page_id": "p1"})]})

    backfill._backfill_collection(client, _integration(accounts=("acct-1", "acct-2", "acct-3")), budget=100)

    (call,) = classifier.calls
    assert call["account_id"] == "acct-1"
    assert len(client.set_payloads) == 1  # one payload, shared by every account's KB row


# ---------------------------------------------------------------------------
# _backfill_collection: integration collections, page by page


def test_each_page_of_an_integration_collection_is_one_document_and_one_llm_call(classifier, rollup):
    points = [
        _pt(UUID_A, md={"page_id": "p1", "parent_id": "h1"}),
        _pt(UUID_B, md={"page_id": "p1", "parent_id": "h1"}),
        _pt(UUID_C, md={"page_id": "p2"}),
    ]
    client = _Client({"int-9_knowledge_base": points})

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    (call,) = classifier.calls
    assert sorted(len(group) for group in call["groups"]) == [1, 2]  # two documents in one batched call
    assert result == backfill.CollectionResult(attempted=2, labelled_documents=2, labelled_chunks=3)
    assert len(client.set_payloads) == 2  # one write per document...
    assert rollup.refreshed == ["int-9_knowledge_base"]  # ...and ONE refresh for the collection
    assert all(md["kind"] == "sop" for md in client.metadata("int-9_knowledge_base").values())


def test_a_half_labelled_edited_page_is_one_document_and_every_chunk_is_relabelled(classifier):
    # The old chunk kept the labels of the page's previous text and its old
    # parent_id; the new tail chunk carries a new parent_id and no labels yet.
    classifier.labels = dict(OTHER_LABELS)
    points = [
        _pt(UUID_A, "old", _labelled(page_id="p1", parent_id="old-hash", chunk_index=0)),
        _pt(UUID_B, "new", {"page_id": "p1", "parent_id": "new-hash", "chunk_index": 1}),
        _pt(UUID_C, "another page", _labelled(page_id="p2", parent_id="q-hash")),
    ]
    client = _Client({"int-9_knowledge_base": points})

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    (call,) = classifier.calls
    (document,) = call["groups"]  # ONE document, not an old half and a new half
    assert [d.id for d in document] == [HEX_A, HEX_B]
    assert result == backfill.CollectionResult(attempted=1, labelled_documents=1, labelled_chunks=2)
    md = client.metadata("int-9_knowledge_base")
    assert {md[UUID_A]["kind"], md[UUID_B]["kind"]} == {"knowledge"}
    assert md[UUID_A]["parent_id"] == "old-hash" and md[UUID_B]["parent_id"] == "new-hash"  # merged, not replaced
    assert md[UUID_C] == _labelled(page_id="p2", parent_id="q-hash")  # the other page is left alone
    (write,) = client.set_payloads
    assert UUID_C not in write["points"]


def test_figure_descriptions_are_classified_and_labelled_with_their_page(classifier):
    points = [
        _pt(UUID_A, "page text", {"page_id": "p1", "parent_id": "h1", "chunk_index": 0}),
        _pt(UUID_B, "page text 2", {"page_id": "p1", "parent_id": "h1", "chunk_index": 1}),
        _pt(UUID_C, "a diagram of the flow", {"page_id": "p1", "url": "https://x/fig.png", "is_figure": True}),
    ]
    client = _Client({"int-9_knowledge_base": points})

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    (call,) = classifier.calls
    (document,) = call["groups"]
    assert len(document) == 3
    assert result == backfill.CollectionResult(attempted=1, labelled_documents=1, labelled_chunks=3)
    assert {md["kind"] for md in client.metadata("int-9_knowledge_base").values()} == {"sop"}


def test_pages_without_a_scraper_id_are_grouped_by_parent_id_then_by_their_own_id(classifier):
    points = [
        _pt(UUID_A, md={"parent_id": "h1"}),
        _pt(UUID_B, md={"parent_id": "h1"}),
        _pt(UUID_C, md={}),
    ]
    client = _Client({"int-9_knowledge_base": points})

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    (call,) = classifier.calls
    assert sorted(len(group) for group in call["groups"]) == [1, 2]
    assert result.labelled_documents == 2 and result.labelled_chunks == 3


def test_only_the_outstanding_pages_are_classified(classifier):
    points = [
        _pt(UUID_A, md=_labelled(page_id="done")),
        _pt(UUID_B, md={"page_id": "todo"}),
    ]
    client = _Client({"int-9_knowledge_base": points})

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    (call,) = classifier.calls
    assert [[d.id for d in group] for group in call["groups"]] == [[HEX_B]]
    assert result.labelled_documents == 1 and result.labelled_chunks == 1


# ---------------------------------------------------------------------------
# _backfill_collection: the rollup row


def test_the_rollup_written_after_labelling_is_the_union_of_what_the_payload_says(monkeypatch, classifier):
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", _REAL_REFRESH)
    written = []
    monkeypatch.setattr(db_query, "set_kb_collection_summary", lambda *args: written.append(args))
    client = _Client(
        {
            "int-9_knowledge_base": [
                _pt(UUID_A, "restart the pod", {"page_id": "p1"}),
                _pt(UUID_B, "background reading", {"page_id": "p2"}),
            ]
        }
    )
    monkeypatch.setattr(processing, "get_qdrant_client", lambda: client.inner)
    classifier.per_document = lambda group: LABELS if "restart" in group[0].page_content else OTHER_LABELS

    backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)

    assert written == [
        (
            "int-9_knowledge_base",
            ["knowledge", "sop"],
            ["decision_tree", "narrative_reference"],
            ["k8s.restart"],
        )
    ]


def test_a_lost_rollup_row_is_rebuilt_from_the_payload_with_no_llm_call(classifier, rollup):
    client = _Client({"kb_kb-1": [_pt(UUID_A, md=_labelled()), _pt(UUID_B, md=_labelled())]})

    result = backfill._backfill_collection(client, _manual(), budget=100, summarised=set())

    assert result == backfill.CollectionResult(summary_rebuilt=1)
    assert rollup.refreshed == ["kb_kb-1"]
    assert classifier.calls == [] and client.set_payloads == []
    assert client.only_cheap_probes()  # still no payload read of the collection


def test_a_lost_rollup_row_is_rebuilt_from_the_real_payload(monkeypatch, classifier):
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", _REAL_REFRESH)
    written = []
    monkeypatch.setattr(db_query, "set_kb_collection_summary", lambda *args: written.append(args))
    client = _Client({"kb_kb-1": [_pt(UUID_A, md=_labelled()), _pt(UUID_B, md=_labelled())]})
    monkeypatch.setattr(processing, "get_qdrant_client", lambda: client.inner)

    backfill._backfill_collection(client, _manual(), budget=100, summarised=set())

    assert written == [("kb_kb-1", ["sop"], ["decision_tree"], ["k8s.restart"])]
    assert classifier.calls == []


def test_a_lost_rollup_is_rebuilt_even_when_another_document_will_not_classify(classifier, rollup):
    # One page is labelled, the other cannot be classified: the reconcile must not depend on
    # every document in the collection classifying, or a blank KB tab and a wasted call per run
    # would be the price of one bad page.
    classifier.fail = True
    client = _Client(
        {"int-9_knowledge_base": [_pt(UUID_A, md=_labelled(page_id="p1")), _pt(UUID_B, md={"page_id": "p2"})]}
    )

    result = backfill._backfill_collection(client, _integration(), budget=100, summarised=set())

    assert result == backfill.CollectionResult(attempted=1, summary_rebuilt=1)
    assert rollup.refreshed == ["int-9_knowledge_base"]


def test_a_failing_document_does_not_refresh_a_rollup_that_already_exists(classifier, rollup):
    classifier.fail = True
    client = _Client(
        {"int-9_knowledge_base": [_pt(UUID_A, md=_labelled(page_id="p1")), _pt(UUID_B, md={"page_id": "p2"})]}
    )

    result = backfill._backfill_collection(client, _integration(), budget=100, summarised={"int-9_knowledge_base"})

    assert result == backfill.CollectionResult(attempted=1) and rollup.refreshed == []


def test_nothing_is_rebuilt_for_a_collection_whose_only_document_will_not_classify(classifier, rollup):
    classifier.fail = True
    client = _Client({"int-9_knowledge_base": [_pt(UUID_A, md={"page_id": "p1"})]})

    result = backfill._backfill_collection(client, _integration(), budget=100, summarised=set())

    assert result == backfill.CollectionResult(attempted=1) and rollup.refreshed == []


def test_a_collection_whose_rollup_exists_is_not_rebuilt(classifier, rollup):
    client = _Client({"kb_kb-1": [_pt(UUID_A, md=_labelled())]})

    result = backfill._backfill_collection(client, _manual(), budget=100, summarised={"kb_kb-1", "kb_other"})

    assert result == backfill.CollectionResult() and rollup.refreshed == []


def test_no_rollup_is_rebuilt_when_the_list_of_rollups_could_not_be_read(classifier, rollup):
    client = _Client({"kb_kb-1": [_pt(UUID_A, md=_labelled())]})

    result = backfill._backfill_collection(client, _manual(), budget=100, summarised=None)

    assert result == backfill.CollectionResult() and rollup.refreshed == []


def test_no_rollup_is_rebuilt_for_a_collection_with_no_labelled_point(classifier, rollup):
    client = _Client({"kb_empty": []})

    result = backfill._backfill_collection(client, _manual("kb_empty"), budget=100, summarised=set())

    assert result == backfill.CollectionResult() and rollup.refreshed == []


# ---------------------------------------------------------------------------
# _backfill_collection: the budget


def test_the_budget_counts_documents_not_chunks(classifier):
    points = [
        _pt(UUID_A, md={"page_id": "p1", "parent_id": "h1"}),
        _pt(UUID_B, md={"page_id": "p1", "parent_id": "h1"}),
        _pt(UUID_C, md={"page_id": "p2"}),
        _pt(_uid(4), md={"page_id": "p3"}),
    ]
    client = _Client({"int-9_knowledge_base": points})

    result = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=2)

    assert result.attempted == 2 and result.labelled_documents == 2
    assert len(classifier.calls[0]["groups"]) == 2
    left = {md["page_id"] for md in client.metadata("int-9_knowledge_base").values() if "kind" not in md}
    assert len(left) == 1  # the third page is left for the next run, whichever two went first
    # The next run pays only for what is left.
    rest = backfill._backfill_collection(client, _integration(accounts=("acct-1",)), budget=100)
    assert rest.attempted == 1 and rest.labelled_documents == 1


@pytest.mark.parametrize("budget", [0, -3])
def test_no_budget_classifies_nothing_and_reads_no_payload(classifier, rollup, budget):
    client = _Client({"kb_kb-1": [_pt(UUID_A)]})

    result = backfill._backfill_collection(client, _manual(), budget=budget, summarised={"kb_kb-1"})

    assert result == backfill.CollectionResult()
    assert classifier.calls == [] and client.set_payloads == [] and rollup.refreshed == []
    assert all(s["with_payload"] is False for s in client.scrolls)


@pytest.mark.parametrize("budget", [0, -3])
def test_no_budget_still_rebuilds_a_lost_rollup(classifier, rollup, budget):
    client = _Client({"kb_kb-1": [_pt(UUID_A, md=_labelled())]})

    result = backfill._backfill_collection(client, _manual(), budget=budget, summarised=set())

    assert result == backfill.CollectionResult(summary_rebuilt=1)
    assert rollup.refreshed == ["kb_kb-1"] and classifier.calls == []


# ---------------------------------------------------------------------------
# _backfill_collection: batching writes


def test_a_large_document_is_labelled_in_payload_sized_batches(classifier, monkeypatch):
    monkeypatch.setattr(backfill, "PAYLOAD_BATCH", 2)
    client = _Client({"kb_kb-1": [_pt(_uid(i), md={"chunk_index": i}) for i in range(5)]})

    backfill._backfill_collection(client, _manual(), budget=100)

    assert [len(c["points"]) for c in client.set_payloads] == [2, 2, 1]


def test_more_than_five_hundred_points_are_written_in_batches_and_other_metadata_survives(classifier):
    total = 1300
    ids = [_uid(i) for i in range(total)]
    client = _Client(
        {"kb_kb-1": [_pt(pid, f"chunk {i}", {"source": "manual", "chunk_index": i}) for i, pid in enumerate(ids)]}
    )

    result = backfill._backfill_collection(client, _manual(), budget=100)

    assert [len(c["points"]) for c in client.set_payloads] == [500, 500, 300]
    assert all(c["key"] == "metadata" and c["payload"] == LABELS for c in client.set_payloads)
    assert [pid for c in client.set_payloads for pid in c["points"]] == ids  # each once, in document order
    assert result == backfill.CollectionResult(attempted=1, labelled_documents=1, labelled_chunks=total)
    metadata = client.metadata("kb_kb-1")
    assert len(metadata) == total
    assert all(md["kind"] == "sop" for md in metadata.values())
    assert metadata[_uid(1234)] == {"source": "manual", "chunk_index": 1234, **LABELS}  # merged, not replaced


# ---------------------------------------------------------------------------
# run


def _pending(monkeypatch, *kbs):
    monkeypatch.setattr(backfill, "_pending_knowledgebases", lambda: list(kbs))


def test_run_backfills_every_pending_collection_within_budget(monkeypatch, rollup):
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"))
    rollup.names = {"kb_x"}
    calls = []

    def _fake(client, kb, budget, summarised):
        calls.append((kb.name, budget, summarised))
        return backfill.CollectionResult(attempted=3, labelled_documents=3, labelled_chunks=9, summary_rebuilt=1)

    monkeypatch.setattr(backfill, "_backfill_collection", _fake)
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 100)

    summary = backfill.run(client=object())

    assert summary == {
        "collections_scanned": 2,
        "collections_backfilled": 2,
        "documents_labelled": 6,
        "chunks_labelled": 18,
        "summaries_rebuilt": 2,
        "errors": 0,
    }
    assert calls == [("kb_a", 100, {"kb_x"}), ("kb_b", 97, {"kb_x"})]
    assert rollup.names_reads == 1  # the existing rollups are listed once per run, not per collection


def test_run_keeps_visiting_collections_once_the_budget_is_spent(monkeypatch):
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"), _manual("kb_c"))
    calls = []

    def _fake(client, kb, budget, summarised):
        calls.append((kb.name, budget))
        return backfill.CollectionResult(attempted=budget, labelled_documents=budget, labelled_chunks=budget)

    monkeypatch.setattr(backfill, "_backfill_collection", _fake)
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 5)

    summary = backfill.run(client=object())

    # No early break: the later collections are still visited (with nothing to spend)
    # so their missing rollups can be rebuilt.
    assert calls == [("kb_a", 5), ("kb_b", 0), ("kb_c", 0)]
    assert summary["budget_exhausted"] == 1
    assert summary["documents_labelled"] == 5


def test_an_exhausted_budget_still_rebuilds_rollups_but_classifies_nothing(monkeypatch, classifier, rollup):
    client = _Client(
        {
            "kb_a": [_pt(UUID_A)],  # outstanding: spends the whole budget of one call
            "kb_b": [_pt(UUID_A, md=_labelled())],  # fully labelled, its rollup row is lost
            "kb_c": [_pt(UUID_A)],  # outstanding, but nothing is left to spend
        }
    )
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"), _manual("kb_c"))
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 1)

    summary = backfill.run(client)

    assert [c["collection_name"] for c in classifier.calls] == ["kb_a"]
    assert rollup.refreshed == ["kb_a", "kb_b"]
    assert summary == {
        "collections_scanned": 3,
        "collections_backfilled": 1,
        "documents_labelled": 1,
        "chunks_labelled": 1,
        "summaries_rebuilt": 1,
        "errors": 0,
        "budget_exhausted": 1,
    }
    assert "kind" not in client.metadata("kb_c")[UUID_A]


def test_only_llm_calls_that_were_made_spend_budget(monkeypatch, classifier, rollup):
    # An account with no resolvable provider costs nothing, so it must not
    # starve the collection after it.
    client = _Client({"kb_a": [_pt(UUID_A)], "kb_b": [_pt(UUID_A)]})
    _pending(monkeypatch, _manual("kb_a", accounts=("acct-nop",)), _manual("kb_b", accounts=("acct-1",)))
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 1)
    classifier.unresolvable = {"acct-nop"}

    summary = backfill.run(client)

    assert [c["collection_name"] for c in classifier.calls] == ["kb_a", "kb_b"]
    assert summary["documents_labelled"] == 1
    assert "budget_exhausted" not in summary
    assert "kind" not in client.metadata("kb_a")[UUID_A] and client.metadata("kb_b")[UUID_A]["kind"] == "sop"


def test_a_failed_classification_still_spends_budget(monkeypatch, classifier):
    # The LLM call was made and paid for whether or not it produced a label.
    client = _Client({"kb_a": [_pt(UUID_A)], "kb_b": [_pt(UUID_A)]})
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"))
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 1)
    classifier.fail = True

    summary = backfill.run(client)

    assert [c["collection_name"] for c in classifier.calls] == ["kb_a"]  # kb_b had no budget left
    assert summary["documents_labelled"] == 0 and summary["budget_exhausted"] == 1


def test_a_second_account_joining_a_labelled_shared_collection_costs_no_llm_call(monkeypatch, classifier, rollup):
    rows = [
        SimpleNamespace(id="kb-1", account_id="acct-a", kb_type="integration", integration_id="int-9"),
        SimpleNamespace(id="kb-2", account_id="acct-b", kb_type="integration", integration_id="int-9"),
    ]
    monkeypatch.setattr(db_query, "engine", _fake_engine({"FROM llm_knowledgebases": rows}))
    client = _Client(
        {"int-9_knowledge_base": [_pt(UUID_A, md=_labelled(page_id="p1")), _pt(UUID_B, md=_labelled(page_id="p2"))]}
    )
    rollup.names = {"int-9_knowledge_base"}

    summary = backfill.run(client)

    assert classifier.calls == []
    assert client.set_payloads == [] and rollup.refreshed == []
    assert summary["collections_scanned"] == 1 and summary["documents_labelled"] == 0
    assert client.only_cheap_probes()


def test_a_second_run_over_a_backfilled_collection_makes_no_llm_call(monkeypatch, classifier, rollup):
    client = _Client({"int-9_knowledge_base": [_pt(UUID_A, md={"page_id": "p1"}), _pt(UUID_B, md={"page_id": "p2"})]})
    _pending(monkeypatch, _integration(accounts=("acct-1",)))

    first = backfill.run(client)
    assert first["documents_labelled"] == 2 and len(classifier.calls) == 1

    rollup.names = {"int-9_knowledge_base"}  # the row the first run wrote
    scrolls_before, writes_before = len(client.scrolls), len(client.set_payloads)
    second = backfill.run(client)

    assert second["documents_labelled"] == 0 and second["collections_backfilled"] == 0
    assert len(classifier.calls) == 1
    assert len(client.set_payloads) == writes_before
    assert all(s["limit"] == 1 and s["with_payload"] is False for s in client.scrolls[scrolls_before:])


def test_run_rebuilds_only_the_rollups_that_are_missing(monkeypatch, classifier, rollup):
    client = _Client({"kb_a": [_pt(UUID_A, md=_labelled())], "kb_b": [_pt(UUID_A, md=_labelled())]})
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"))
    rollup.names = {"kb_a"}

    summary = backfill.run(client)

    assert summary["summaries_rebuilt"] == 1 and summary["collections_backfilled"] == 0
    assert rollup.refreshed == ["kb_b"] and classifier.calls == []


def test_run_rebuilds_no_rollup_when_the_existing_ones_cannot_be_listed(monkeypatch, classifier, rollup):
    rollup.names_error = RuntimeError("postgres down")
    client = _Client({"kb_todo": [_pt(UUID_A)], "kb_done": [_pt(UUID_A, md=_labelled())]})
    _pending(monkeypatch, _manual("kb_todo"), _manual("kb_done"))

    summary = backfill.run(client)

    assert summary["summaries_rebuilt"] == 0
    assert rollup.refreshed == ["kb_todo"]  # labelling still refreshes its own collection; kb_done is not guessed at
    assert summary["documents_labelled"] == 1  # and classification carries on


def test_run_aborts_when_the_knowledgebases_cannot_be_listed(monkeypatch, rollup):
    def _boom():
        raise RuntimeError("postgres down")

    monkeypatch.setattr(backfill, "_pending_knowledgebases", _boom)
    monkeypatch.setattr(backfill, "_backfill_collection", lambda *a, **k: pytest.fail("nothing should run"))

    summary = backfill.run(client=object())

    assert summary["collections_scanned"] == 0 and summary["documents_labelled"] == 0
    # Counted as an error, so the background worker retries instead of taking an
    # all-zero summary for a run that finished with nothing to do.
    assert summary["errors"] == 1


def test_run_stops_early_when_the_lock_is_lost(monkeypatch):
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"))
    calls = []
    monkeypatch.setattr(
        backfill,
        "_backfill_collection",
        lambda client, kb, budget, summarised: calls.append(kb.name) or backfill.CollectionResult(attempted=1),
    )
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 100)

    still_held = iter([True, False])
    summary = backfill.run(client=object(), still_held=lambda: next(still_held))

    assert calls == ["kb_a"]
    assert summary["stopped_lock_lost"] == 1


def test_run_does_nothing_when_the_lock_was_never_held(monkeypatch):
    _pending(monkeypatch, _manual("kb_a"))
    monkeypatch.setattr(backfill, "_backfill_collection", lambda *a, **k: pytest.fail("nothing should run"))

    summary = backfill.run(client=object(), still_held=lambda: False)

    assert summary["stopped_lock_lost"] == 1 and summary["documents_labelled"] == 0


def test_run_counts_a_failing_collection_as_an_error_and_continues(monkeypatch):
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"))

    def _fake(client, kb, budget, summarised):
        if kb.name == "kb_a":
            raise RuntimeError("boom")
        return backfill.CollectionResult(attempted=1, labelled_documents=1, labelled_chunks=1)

    monkeypatch.setattr(backfill, "_backfill_collection", _fake)
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 100)

    summary = backfill.run(client=object())

    assert summary["errors"] == 1
    assert summary["collections_backfilled"] == 1  # kb_b still ran despite kb_a's failure


def test_a_failing_collection_spends_no_budget(monkeypatch):
    _pending(monkeypatch, _manual("kb_a"), _manual("kb_b"))
    budgets = []

    def _fake(client, kb, budget, summarised):
        budgets.append(budget)
        if kb.name == "kb_a":
            raise RuntimeError("boom")
        return backfill.CollectionResult()

    monkeypatch.setattr(backfill, "_backfill_collection", _fake)
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 7)

    backfill.run(client=object())

    assert budgets == [7, 7]


def test_run_surfaces_per_document_write_errors(monkeypatch):
    _pending(monkeypatch, _manual("kb_a"))
    monkeypatch.setattr(
        backfill,
        "_backfill_collection",
        lambda client, kb, budget, summarised: backfill.CollectionResult(attempted=1, errors=1),
    )
    monkeypatch.setattr(backfill, "_max_documents_per_run", lambda: 100)

    assert backfill.run(client=object())["errors"] == 1


def test_a_collection_that_was_never_created_is_not_an_error(monkeypatch, classifier):
    client = _Client({})
    _pending(monkeypatch, _integration())

    summary = backfill.run(client)

    assert summary["errors"] == 0 and summary["collections_scanned"] == 1
    assert classifier.calls == [] and client.scrolls == []


# ---------------------------------------------------------------------------
# is_enabled / budget env


@pytest.mark.parametrize(
    "value,expected",
    [(None, False), ("false", False), ("true", True), ("True", True), (" true ", True), ("garbage", False)],
)
def test_is_enabled_defaults_off(monkeypatch, value, expected):
    if value is None:
        monkeypatch.delenv("RAG_KB_CAPABILITY_BACKFILL_ENABLED", raising=False)
    else:
        monkeypatch.setenv("RAG_KB_CAPABILITY_BACKFILL_ENABLED", value)

    assert backfill.is_enabled() is expected


@pytest.mark.parametrize("value,expected", [(None, 500), ("12", 12), ("garbage", 500)])
def test_the_document_budget_reads_its_env_var(monkeypatch, value, expected):
    if value is None:
        monkeypatch.delenv("RAG_KB_CAPABILITY_BACKFILL_MAX_DOCUMENTS_PER_RUN", raising=False)
    else:
        monkeypatch.setenv("RAG_KB_CAPABILITY_BACKFILL_MAX_DOCUMENTS_PER_RUN", value)

    assert backfill._max_documents_per_run() == expected


# ---------------------------------------------------------------------------
# The cross-replica advisory lock


class _Scalar:
    def __init__(self, value):
        self.value = value

    def scalar(self):
        return self.value


def _lock_engine(monkeypatch, *, granted=True, held=True, connect_error=None, lock_error=None, check_error=None):
    """Postgres stand-in for the advisory lock: records every statement and
    answers pg_try_advisory_lock / the pg_locks re-check the way a session would."""
    state = SimpleNamespace(statements=[], held=held, closed=False, isolation=None)

    class _Conn:
        def execution_options(self, **options):
            state.isolation = options.get("isolation_level")
            return self

        def execute(self, statement, params=None):
            sql = str(statement)
            state.statements.append((sql, params))
            if "pg_try_advisory_lock" in sql:
                if lock_error is not None:
                    raise lock_error
                return _Scalar(granted)
            if "pg_locks" in sql:
                if check_error is not None:
                    raise check_error
                return _Scalar(state.held)
            return _Scalar(None)

        def close(self):
            state.closed = True

    def connect():
        if connect_error is not None:
            raise connect_error
        return _Conn()

    monkeypatch.setattr(db_query, "engine", SimpleNamespace(connect=connect))
    return state


def _sql(state):
    return [sql for sql, _ in state.statements]


def test_the_lock_key_is_its_own_and_splits_into_the_classid_and_objid_pg_locks_reports():
    assert (
        backfill.LOCK_KEY != embedding_migration.LOCK_KEY
    )  # unrelated migrations must not serialise behind each other
    assert (backfill._LOCK_CLASSID << 32) | backfill._LOCK_OBJID == backfill.LOCK_KEY


def test_one_replica_takes_the_lock_and_releases_it_afterwards(monkeypatch):
    state = _lock_engine(monkeypatch)

    with backfill._exclusive_run() as still_held:
        assert callable(still_held) and still_held()
        assert not state.closed

    assert state.isolation == "AUTOCOMMIT"  # a session-level lock must not sit inside a transaction
    assert "pg_try_advisory_lock" in _sql(state)[0] and state.statements[0][1] == {"k": backfill.LOCK_KEY}
    assert "pg_advisory_unlock" in _sql(state)[-1] and state.statements[-1][1] == {"k": backfill.LOCK_KEY}
    assert state.closed


def test_the_lock_is_released_even_when_the_run_raises(monkeypatch):
    state = _lock_engine(monkeypatch)

    with pytest.raises(RuntimeError, match="boom"):
        with backfill._exclusive_run():
            raise RuntimeError("boom")

    assert "pg_advisory_unlock" in _sql(state)[-1] and state.closed


def test_a_replica_that_does_not_get_the_lock_yields_nothing_and_unlocks_nothing(monkeypatch):
    state = _lock_engine(monkeypatch, granted=False)

    with backfill._exclusive_run() as still_held:
        assert still_held is None

    assert not any("pg_advisory_unlock" in sql for sql in _sql(state))  # it holds nothing to release
    assert state.closed


def test_the_lock_is_reported_lost_when_postgres_says_this_session_no_longer_holds_it(monkeypatch):
    # A transparently reconnected session still answers a bare SELECT 1 while holding no lock.
    state = _lock_engine(monkeypatch)

    with backfill._exclusive_run() as still_held:
        assert still_held() is True
        state.held = False
        assert still_held() is False

    recheck = next(params for sql, params in state.statements if "pg_locks" in sql)
    assert recheck == {"classid": backfill._LOCK_CLASSID, "objid": backfill._LOCK_OBJID}


def test_a_lock_check_that_cannot_be_answered_counts_as_lost(monkeypatch):
    state = _lock_engine(monkeypatch, check_error=RuntimeError("connection reset"))

    with backfill._exclusive_run() as still_held:
        assert still_held() is False  # reported, not raised into run()

    assert state.closed


def test_an_unreachable_postgres_is_reported_as_an_unavailable_migration(monkeypatch):
    _lock_engine(monkeypatch, connect_error=RuntimeError("connection refused"))

    with pytest.raises(backfill.MigrationUnavailable, match="connection refused"):
        with backfill._exclusive_run():
            pytest.fail("the body must not run without the lock being evaluated")


def test_a_failing_lock_query_is_reported_as_an_unavailable_migration_and_closes_the_connection(monkeypatch):
    state = _lock_engine(monkeypatch, lock_error=RuntimeError("server closed the connection"))

    with pytest.raises(backfill.MigrationUnavailable):
        with backfill._exclusive_run():
            pytest.fail("the body must not run without the lock being evaluated")

    assert state.closed


def test_run_locked_does_no_work_when_another_replica_holds_the_lock(monkeypatch):
    @contextmanager
    def _not_acquired():
        yield None

    monkeypatch.setattr(backfill, "_exclusive_run", _not_acquired)
    ran = []
    monkeypatch.setattr(backfill, "run", lambda *a, **k: ran.append(1))

    summary = backfill.run_locked()

    assert summary == {"skipped_locked": 1}
    assert ran == []


def test_run_locked_runs_against_the_qdrant_client_with_the_liveness_check(monkeypatch):
    liveness = lambda: True  # noqa: E731

    @contextmanager
    def _acquired():
        yield liveness

    sentinel = object()
    monkeypatch.setattr(backfill, "_exclusive_run", _acquired)
    monkeypatch.setattr("rag.qdrant.client.get_qdrant_client", lambda: sentinel)
    seen = []
    monkeypatch.setattr(backfill, "run", lambda client, still_held: seen.append((client, still_held)) or {"errors": 0})

    assert backfill.run_locked() == {"errors": 0}
    assert seen == [(sentinel, liveness)]


# ---------------------------------------------------------------------------
# The background worker and its retry policy


def _worker(monkeypatch, outcomes):
    """Script run_locked: each outcome is a summary to return or an exception to
    raise, one per attempt. Sleeping is recorded, not done."""
    attempts, sleeps = [], []
    remaining = iter(outcomes)

    def run_locked():
        attempts.append(1)
        outcome = next(remaining)
        if isinstance(outcome, Exception):
            raise outcome
        return outcome

    monkeypatch.setattr(backfill, "run_locked", run_locked)
    monkeypatch.setattr(backfill, "time", SimpleNamespace(sleep=sleeps.append))
    return attempts, sleeps


def _join(thread):
    thread.join(timeout=5)
    assert not thread.is_alive()


def test_a_clean_run_is_not_repeated(monkeypatch):
    attempts, sleeps = _worker(monkeypatch, [{"errors": 0}])

    _join(backfill.start_background())

    assert len(attempts) == 1 and sleeps == []


def test_the_worker_retries_a_transient_failure_until_a_run_succeeds(monkeypatch):
    attempts, _ = _worker(monkeypatch, [RuntimeError("provider outage"), RuntimeError("postgres blip"), {"errors": 0}])

    _join(backfill.start_background(retry_delay=0))

    assert len(attempts) == 3


def test_an_unreachable_postgres_is_retried_like_any_transient_failure(monkeypatch):
    attempts, _ = _worker(monkeypatch, [backfill.MigrationUnavailable("no lock"), {"errors": 0}])

    _join(backfill.start_background(retry_delay=0))

    assert len(attempts) == 2


def test_the_worker_gives_up_rather_than_retrying_forever(monkeypatch):
    attempts, _ = _worker(monkeypatch, [RuntimeError("still down")] * 20)

    thread = backfill.start_background(retry_delay=0)
    _join(thread)

    assert len(attempts) == backfill.RETRY_ATTEMPTS == 5


def test_the_retry_delay_doubles_up_to_a_ceiling(monkeypatch):
    attempts, sleeps = _worker(monkeypatch, [RuntimeError("still down")] * 20)

    _join(backfill.start_background(retry_delay=600))

    assert sleeps == [600, 900, 900, 900]  # one sleep between each pair of attempts, none after the last
    assert len(attempts) == 5


def test_the_default_retry_delay_starts_at_a_minute(monkeypatch):
    _, sleeps = _worker(monkeypatch, [RuntimeError("down")] * 20)

    _join(backfill.start_background())

    assert sleeps == [60, 120, 240, 480]


def test_a_run_skipped_because_another_replica_holds_the_lock_is_not_retried(monkeypatch):
    # That is a healthy outcome: the other replica is doing the work.
    attempts, _ = _worker(monkeypatch, [{"skipped_locked": 1}])

    _join(backfill.start_background(retry_delay=0))

    assert len(attempts) == 1


def test_a_run_that_finished_with_failed_collections_is_retried(monkeypatch):
    # Every step is idempotent, so a retry only redoes what is still outstanding.
    attempts, _ = _worker(monkeypatch, [{"errors": 2}, {"errors": 0}])

    _join(backfill.start_background(retry_delay=0))

    assert len(attempts) == 2


def test_the_worker_runs_on_a_named_daemon_thread_that_does_not_block_startup(monkeypatch):
    release = threading.Event()
    monkeypatch.setattr(backfill, "run_locked", lambda: release.wait(timeout=5) and {"errors": 0})

    thread = backfill.start_background()
    try:
        assert thread.is_alive()  # start_background returned while the run is still going
        assert thread.daemon and thread.name == "kb-capability-backfill"
    finally:
        release.set()
        _join(thread)
