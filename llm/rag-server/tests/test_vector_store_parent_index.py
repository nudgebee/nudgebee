from types import SimpleNamespace

from rag import vector_store


class _Client:
    def __init__(self, fail=False):
        self.indexed, self.fail = [], fail

    def create_payload_index(self, collection_name, field_name, field_schema):
        if self.fail:
            raise RuntimeError("qdrant down")
        self.indexed.append((collection_name, field_name, field_schema))


def test_parent_id_is_indexed_for_the_page_read():
    client = _Client()
    vector_store._ensure_parent_id_index(client, "kb_1")
    assert client.indexed == [("kb_1", "metadata.parent_id", "keyword")]


def test_a_failed_index_does_not_fail_the_ingest():
    vector_store._ensure_parent_id_index(_Client(fail=True), "kb_1")


def _ensure(monkeypatch, metadata):
    client = _Client()
    monkeypatch.setattr("rag.qdrant.client.get_qdrant_client", lambda: client)
    monkeypatch.setattr("rag.qdrant.client.list_collections_optimized", lambda: None)
    cache = SimpleNamespace(exists=lambda name: True, add=lambda info: None)
    monkeypatch.setattr("rag.core.cache.get_collection_list_cache", lambda: cache)
    monkeypatch.setattr("rag.core.documents.embedding_migration.model_identity", lambda fn: "model")
    embeddings = SimpleNamespace(embed_query=lambda text: [0.0] * 4)
    vector_store.ensure_collection_exists(embeddings, "col", metadata)
    return client


def test_only_knowledge_base_collections_get_the_index(monkeypatch):
    assert _ensure(monkeypatch, {"module": "knowledge_base"}).indexed
    assert not _ensure(monkeypatch, {"module": "memory"}).indexed
    assert not _ensure(monkeypatch, None).indexed
