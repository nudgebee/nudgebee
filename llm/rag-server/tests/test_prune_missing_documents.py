"""Pins sync pruning of stale points, including legacy integer point ids.

Runs handle_updated_documents against an in-memory Qdrant so a delete request
with a malformed id fails the same way it does against the real server. The
rollup refresh a prune triggers is covered in test_kb_collection_summary.py.
"""

import hashlib
import uuid

import pytest
from qdrant_client import QdrantClient
from qdrant_client.models import Distance, PointStruct, VectorParams

from rag.core.documents import processing

COLLECTION = "integration_knowledge_base"


def _hex(content):
    return hashlib.md5(content.encode()).hexdigest()


@pytest.fixture
def qdrant(monkeypatch):
    client = QdrantClient(":memory:")
    client.create_collection(COLLECTION, vectors_config=VectorParams(size=2, distance=Distance.COSINE))
    monkeypatch.setattr(processing, "get_qdrant_client", lambda: client)
    # A prune corrects the collection's classification rollup when it has one; keep that off a real database.
    client.refresh_calls = []
    monkeypatch.setattr("rag.core.utils.db_query.has_kb_collection_summary", lambda collection_name: True)
    monkeypatch.setattr(processing, "refresh_kb_collection_summary", lambda name: client.refresh_calls.append(name))
    return client


def _ids(client):
    points, _ = client.scroll(COLLECTION, limit=100, with_payload=False, with_vectors=False)
    return {point.id for point in points}


def test_prune_deletes_stale_integer_and_uuid_points_and_keeps_current(qdrant):
    current = _hex("current page")
    stale_uuid = _hex("removed page")
    stale_int = 384963714798485014  # legacy chunked points carry integer ids
    qdrant.upsert(
        COLLECTION,
        points=[
            PointStruct(id=str(uuid.UUID(current)), vector=[0.1, 0.2], payload={"page_content": "current page"}),
            PointStruct(id=str(uuid.UUID(stale_uuid)), vector=[0.1, 0.2], payload={"page_content": "removed page"}),
            PointStruct(id=stale_int, vector=[0.1, 0.2], payload={"page_content": "old chunk"}),
        ],
    )

    # The sync reports ids as plain hex, the way _generate_document_ids makes them.
    assert processing.handle_updated_documents(COLLECTION, [current]) is True

    assert _ids(qdrant) == {str(uuid.UUID(current))}
    # The rollup is recomputed once, from what is left, after every stale point is gone.
    assert qdrant.refresh_calls == [COLLECTION]


def test_prune_is_a_no_op_when_nothing_is_stale(qdrant):
    current = _hex("current page")
    qdrant.upsert(COLLECTION, points=[PointStruct(id=str(uuid.UUID(current)), vector=[0.1, 0.2], payload={})])

    assert processing.handle_updated_documents(COLLECTION, [current]) is False
    assert _ids(qdrant) == {str(uuid.UUID(current))}
    assert qdrant.refresh_calls == []  # nothing pruned, nothing to correct
