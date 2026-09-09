"""Pins the batched existence probe that stops unchanged docs being re-embedded.

Drives the real generate_embeddings_batch against an in-memory Qdrant with a
counting embedder, so the assertions are about observed embed calls rather than
about the shape of the code.
"""

import asyncio
import hashlib

import pytest
from qdrant_client import QdrantClient

from rag.core.documents import processing
from rag.core.types import Document
from rag.qdrant import client as qclient

DIM = 8


class CountingEmbeddings:
    """Records every document handed to the embedding API."""

    def __init__(self):
        self.calls = 0  # batch-level invocations
        self.documents = 0  # documents actually embedded

    def embed_documents(self, texts):
        self.calls += 1
        self.documents += len(texts)
        return [[0.01 * (i + 1)] * DIM for i in range(len(texts))]

    def embed_query(self, text):
        return [0.01] * DIM


def _docs(n, prefix="page"):
    out = []
    for i in range(n):
        content = f"{prefix} body {i}"
        d = Document(page_content=content, metadata={"page_id": str(i)})
        d.id = hashlib.md5(content.encode()).hexdigest()
        out.append(d)
    return out


@pytest.fixture
def qdrant(monkeypatch):
    client = QdrantClient(":memory:")
    # vector_store imports get_qdrant_client lazily from rag.qdrant.client,
    # so patch it at the source as well as the name processing already bound.
    monkeypatch.setattr(processing, "get_qdrant_client", lambda: client)
    monkeypatch.setattr(qclient, "get_qdrant_client", lambda: client)
    return client


def _run(documents, embeddings, collection, verify_doc):
    asyncio.run(
        processing.generate_embeddings_batch(
            documents=documents,
            embeddings=embeddings,
            collection_name=collection,
            collection_metadata={},
            tracker=None,
            verify_doc=verify_doc,
        )
    )


def test_unchanged_documents_are_not_re_embedded(qdrant):
    """The whole point: a re-sync of unchanged pages must embed nothing."""
    emb = CountingEmbeddings()
    docs = _docs(50)

    _run(docs, emb, "kb_test", verify_doc=False)  # cold build
    assert emb.documents == 50, "cold build must embed everything"
    first_calls = emb.calls

    _run(docs, emb, "kb_test", verify_doc=True)  # re-sync, nothing changed
    assert emb.documents == 50, "re-sync must not re-embed unchanged documents"
    assert emb.calls == first_calls, "re-sync must not call the embedder at all"

    assert qdrant.count("kb_test").count == 50, "no duplicate points"


def test_only_changed_documents_are_embedded(qdrant):
    emb = CountingEmbeddings()
    docs = _docs(20)
    _run(docs, emb, "kb_mixed", verify_doc=False)
    assert emb.documents == 20

    # 5 pages edited (new content -> new md5 -> new id), 15 untouched
    changed = _docs(5, prefix="edited")
    _run(docs[:15] + changed, emb, "kb_mixed", verify_doc=True)

    assert emb.documents == 25, "only the 5 changed pages should be embedded"
    assert qdrant.count("kb_mixed").count == 25


def test_probe_is_one_request_per_batch_not_per_document(qdrant, monkeypatch):
    """Guards the regression the original removal was fixing."""
    emb = CountingEmbeddings()
    docs = _docs(100)
    _run(docs, emb, "kb_probe", verify_doc=False)

    retrieves = {"n": 0}
    real_retrieve = qdrant.retrieve

    def counting_retrieve(*a, **kw):
        retrieves["n"] += 1
        return real_retrieve(*a, **kw)

    monkeypatch.setattr(qdrant, "retrieve", counting_retrieve)
    _run(docs, emb, "kb_probe", verify_doc=True)

    # 100 ids at chunk size 256 -> exactly one round-trip, not 100
    assert retrieves["n"] == 1, f"expected 1 probe request, got {retrieves['n']}"


def test_probe_chunks_large_batches(qdrant, monkeypatch):
    emb = CountingEmbeddings()
    docs = _docs(600)
    _run(docs, emb, "kb_chunk", verify_doc=False)

    retrieves = {"n": 0}
    real_retrieve = qdrant.retrieve

    def counting_retrieve(*a, **kw):
        retrieves["n"] += 1
        return real_retrieve(*a, **kw)

    monkeypatch.setattr(qdrant, "retrieve", counting_retrieve)
    _run(docs, emb, "kb_chunk", verify_doc=True)

    assert retrieves["n"] == 3, f"600 ids / 256 = 3 chunks, got {retrieves['n']}"


def test_probe_failure_falls_open(qdrant, monkeypatch):
    """A broken probe must never block ingestion."""
    emb = CountingEmbeddings()
    docs = _docs(10)
    _run(docs, emb, "kb_failopen", verify_doc=False)
    assert emb.documents == 10

    def boom(*a, **kw):
        raise RuntimeError("qdrant unavailable")

    monkeypatch.setattr(qdrant, "retrieve", boom)
    _run(docs, emb, "kb_failopen", verify_doc=True)

    assert emb.documents == 20, "probe failure must fall back to embedding everything"


def test_verify_doc_false_skips_the_probe_entirely(qdrant, monkeypatch):
    """First build has nothing to find; the probe must not run at all."""
    emb = CountingEmbeddings()
    docs = _docs(10)

    retrieves = {"n": 0}
    monkeypatch.setattr(qdrant, "retrieve", lambda *a, **kw: retrieves.update(n=retrieves["n"] + 1) or [])

    _run(docs, emb, "kb_cold", verify_doc=False)
    assert retrieves["n"] == 0, "no probe should fire on a cold build"
    assert emb.documents == 10


def test_skip_survives_qdrant_hyphenating_point_ids(qdrant, monkeypatch):
    """Regression: the probe must match ids regardless of hyphenation.

    A real Qdrant accepts a bare 32-char hex id on write but stores and returns
    it in canonical hyphenated UUID form. The in-memory client used by the other
    tests echoes the plain hex back, so it cannot reproduce this — the first
    version of the skip shipped to prod comparing plain hex against hyphenated
    ids, matched nothing, and logged "Skipping 0" on every batch.
    """
    emb = CountingEmbeddings()
    docs = _docs(20)
    _run(docs, emb, "kb_hyphen", verify_doc=False)
    assert emb.documents == 20

    real_retrieve = qdrant.retrieve

    def hyphenating_retrieve(*a, **kw):
        points = real_retrieve(*a, **kw)
        for p in points:  # mimic a real server's canonical UUID form
            raw = str(p.id).replace("-", "")
            p.id = f"{raw[0:8]}-{raw[8:12]}-{raw[12:16]}-{raw[16:20]}-{raw[20:32]}"
        return points

    monkeypatch.setattr(qdrant, "retrieve", hyphenating_retrieve)
    _run(docs, emb, "kb_hyphen", verify_doc=True)

    assert emb.documents == 20, "hyphenated ids from Qdrant must still match and skip"
