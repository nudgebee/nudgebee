"""Showing a whole document rather than the first chunk of it.

A chunked document indexed after ``parent_content`` was dropped carries only its own
slice, so the document view rendered ~400 characters of a page. The verbatim document
lives in source storage — when it is there it is served, and when it is not the reader is
told so rather than shown a fragment that looks whole.
"""

from types import SimpleNamespace

from qdrant_client.models import Distance, VectorParams

from rag.core.documents import collection, embedding_migration, source_store
from rag.core.types import Document


class _Embeddings:
    model = "test-model"


class _Client:
    """Enough Qdrant to exercise collection shape checks and a source lookup."""

    def __init__(self, *, size=1, points=0, exists=True):
        self.info = SimpleNamespace(
            config=SimpleNamespace(params=SimpleNamespace(vectors=SimpleNamespace(size=size))),
            points_count=points,
        )
        self.exists = exists
        self.deleted = []
        self.created = []
        self.indexed = []
        self.scrolled = []

    def collection_exists(self, name):
        return self.exists

    def get_collection(self, name):
        return self.info

    def delete_collection(self, collection_name):
        self.deleted.append(collection_name)

    def create_collection(self, collection_name, **kwargs):
        self.created.append((collection_name, kwargs))

    def create_payload_index(self, collection_name, field_name, field_schema):
        self.indexed.append((collection_name, field_name, field_schema))


def test_a_source_collection_rebuilt_at_the_embedder_dimension_is_replaced():
    client = _Client(size=384, points=0)
    source_store.ensure_collection(client, "acct_knowledge_base__source")
    assert client.deleted == ["acct_knowledge_base__source"]
    created_name, kwargs = client.created[0]
    assert created_name == "acct_knowledge_base__source"
    assert kwargs["vectors_config"].size == 1
    # Without the module tag the embedding migration stops recognising this as source
    # storage and rebuilds it at the embedder's dimension — the exact bug being fixed.
    assert kwargs["metadata"] == {"module": source_store.MODULE}
    assert kwargs["on_disk_payload"] is True
    # Both reads filter on source_id; unindexed they scan every on-disk payload.
    assert client.indexed == [("acct_knowledge_base__source", "source_id", "keyword")]


def test_a_wrong_dimension_collection_holding_points_is_left_alone():
    client = _Client(size=384, points=12)
    source_store.ensure_collection(client, "acct_knowledge_base__source")
    assert client.deleted == [], "points that are not ours must not be dropped to fix our shape"
    assert client.created == []


def test_a_correctly_shaped_collection_is_untouched():
    client = _Client(size=1, points=500)
    source_store.ensure_collection(client, "acct_knowledge_base__source")
    assert client.deleted == []
    assert client.created == []
    # But it still gets the index: a collection that predates it, or that the heal
    # declines to touch because it holds points, would otherwise scan every payload.
    assert client.indexed == [("acct_knowledge_base__source", "source_id", "keyword")]


def test_a_wrong_dimension_collection_holding_points_is_still_indexed():
    """The heal refuses to drop it, so the index is the only thing we can still fix."""
    client = _Client(size=384, points=12)
    source_store.ensure_collection(client, "acct_knowledge_base__source")
    assert client.deleted == []
    assert client.indexed == [("acct_knowledge_base__source", "source_id", "keyword")]


class _ScrollClient(_Client):
    def __init__(self, payloads):
        super().__init__()
        self._payloads = payloads

    def scroll(self, collection_name, **kwargs):
        self.scrolled.append(kwargs)
        return [SimpleNamespace(id=i, payload=p) for i, p in enumerate(self._payloads)], None


def test_the_newest_stored_version_is_the_one_served():
    client = _ScrollClient(
        [
            {"content": "older", "fetched_at": "2026-09-01T00:00:00+00:00"},
            {"content": "newest", "fetched_at": "2026-09-22T00:00:00+00:00"},
            {"content": "middle", "fetched_at": "2026-09-10T00:00:00+00:00"},
        ]
    )
    stored = source_store.get_by_source_id("parent", "page-1", client=client)
    assert stored["content"] == "newest"


def test_no_stored_version_reads_as_missing_not_as_an_error():
    assert source_store.get_by_source_id("parent", "page-1", client=_ScrollClient([])) is None


class _PointClient:
    def __init__(self, payload):
        self._payload = payload

    def retrieve(self, **kwargs):
        return [SimpleNamespace(id="p1", payload=self._payload)]


def _document(monkeypatch, payload, stored=None):
    monkeypatch.setattr(collection, "get_qdrant_client", lambda: _PointClient(payload))
    monkeypatch.setattr(source_store, "get_by_source_id", lambda *a, **k: stored)
    return collection.get_collection_document("coll", "p1")


def test_a_chunked_document_is_served_from_its_stored_source(monkeypatch):
    payload = {
        "page_content": "the first 400 characters",
        "metadata": {"parent_id": "abc", "chunk_index": 0, "page_id": "page-1"},
    }
    document = _document(monkeypatch, payload, stored={"content": "the whole page, all of it"})
    assert document["content"] == "the whole page, all of it"
    assert document["content_source"] == "source"


def test_a_chunked_document_with_no_stored_source_says_it_is_a_fragment(monkeypatch):
    payload = {
        "page_content": "the first 400 characters",
        "metadata": {"parent_id": "abc", "chunk_index": 0, "page_id": "page-1"},
    }
    document = _document(monkeypatch, payload, stored=None)
    assert document["content"] == "the first 400 characters"
    assert document["content_source"] == "fragment", "a fragment must never look like a whole document"


def test_an_unchunked_document_is_already_whole(monkeypatch):
    payload = {"page_content": "a short page", "metadata": {"page_id": "page-2"}}
    document = _document(monkeypatch, payload, stored=None)
    assert document["content"] == "a short page"
    assert document["content_source"] == "indexed"


def test_the_embedding_migration_does_not_treat_source_storage_as_an_index():
    class _Described:
        config = SimpleNamespace(
            params=SimpleNamespace(vectors=VectorParams(size=384, distance=Distance.COSINE)),
            metadata={"module": source_store.MODULE},
        )
        points_count = 0

    client = SimpleNamespace(get_collection=lambda name: _Described())
    assert embedding_migration._describe(client, "acct_knowledge_base__source") is None


def test_servicenow_keeps_the_article_body_as_its_source():
    """The ServiceNow scrape hands the raw article body to source storage."""
    from rag.core.documents import scraper

    sources: list = []
    article = {
        "sys_id": "abc123",
        "number": "KB0010001",
        "short_description": "Reset the VPN client",
        "text": "<p>Open the console.</p><p>Click disconnect, then reconnect.</p>",
        "wiki": "",
    }
    document = scraper.process_servicenow_kb_article(article, "https://example.service-now.com", sources=sources)

    assert document is not None
    assert len(sources) == 1
    stored = sources[0]
    # Keyed on the KB number the indexed points carry, so the document view can find it
    # after a new article version gets a new sys_id.
    assert stored["source_id"] == "KB0010001"
    assert stored["title"] == "Reset the VPN client"
    assert stored["content"] == article["text"], "the body is kept as ServiceNow returned it, not converted"
    assert stored["content_type"] == "text/html"


def test_servicenow_collection_is_optional():
    """Callers that do not want sources still get documents."""
    from rag.core.documents import scraper

    article = {"sys_id": "abc123", "number": "KB1", "short_description": "T", "text": "<p>body</p>", "wiki": ""}
    assert scraper.process_servicenow_kb_article(article, "https://example.service-now.com") is not None


def test_a_servicenow_document_resolves_through_sys_id(monkeypatch):
    """ServiceNow points carry sys_id where Confluence carries page_id."""
    payload = {
        "page_content": "the first 400 characters",
        "metadata": {"parent_id": "abc", "chunk_index": 0, "sys_id": "abc123"},
    }
    asked = {}

    def _spy(collection_name, source_id, **kwargs):
        asked["source_id"] = source_id
        return {"content": "the whole article"}

    monkeypatch.setattr(collection, "get_qdrant_client", lambda: _PointClient(payload))
    monkeypatch.setattr(source_store, "get_by_source_id", _spy)
    document = collection.get_collection_document("coll", "p1")

    assert asked["source_id"] == "abc123"
    assert document["content"] == "the whole article"
    assert document["content_source"] == "source"


def test_a_servicenow_source_stored_under_sys_id_is_still_found(monkeypatch):
    """Sources synced before kb_number became the key are stored under the sys_id."""
    stored = {"abc123": {"content": "the whole article"}}
    monkeypatch.setattr(
        source_store, "get_by_source_id", lambda collection_name, source_id, **kw: stored.get(source_id)
    )

    found = collection.stored_source_for("coll", {"kb_number": "KB0010108", "sys_id": "abc123"})

    assert found == {"content": "the whole article"}


def test_the_source_identifier_is_looked_up_in_one_place():
    """Each scrape stamps its own key; the join knows all of them, callers know none."""
    from rag.core.documents.collection import _source_identifier

    assert _source_identifier({"page_id": "page-1"}) == "page-1"
    assert _source_identifier({"sys_id": "abc123"}) == "abc123"
    assert _source_identifier({"kb_number": "KB0010108", "sys_id": "abc123"}) == "KB0010108"
    assert _source_identifier({"source_id": "s-1"}) == "s-1"
    # Numeric ids are normalised, since source storage keys on the string form.
    assert _source_identifier({"page_id": 12345}) == "12345"
    # 0 is a real id, and falsy — it must not be mistaken for a missing one.
    assert _source_identifier({"page_id": 0}) == "0"
    # An explicit source_id wins over a scrape-specific one.
    assert _source_identifier({"source_id": "s-1", "page_id": "page-1"}) == "s-1"
    # Empty is not an id, and must not be looked up as one.
    assert _source_identifier({"page_id": ""}) is None
    assert _source_identifier({}) is None


def test_a_swallowed_store_failure_logs_its_traceback(caplog):
    """The silent failure that emptied source storage must leave a diagnosable trace."""
    import logging

    class _Exploding(_Client):
        def upsert(self, **kwargs):
            raise RuntimeError("Wrong input: Vector dimension error: expected dim: 1, got 384")

    with caplog.at_level(logging.ERROR):
        written = source_store.store(
            "parent",
            [{"source_id": "page-1", "title": "T", "content": "body", "content_type": "text/html"}],
            run_id="r1",
            client=_Exploding(),
        )

    assert written == 0, "a failed store must not claim to have written anything"
    record = next(r for r in caplog.records if "store failed" in r.message)
    assert record.exc_info is not None, "the traceback is the whole point of the log line"
    assert "Vector dimension error" in logging.Formatter().formatException(record.exc_info)


def test_a_declared_source_count_does_not_reach_the_load_ledger(monkeypatch):
    """The knowledge base counts files; the load ledger counts embedding work.

    The ledger stores a pair — embedded and submitted — and renders "8 / 1" whenever
    they differ, meaning a load dropped documents. Feeding the file count into one
    half of that pair makes a load that succeeded completely report a shortfall.
    """
    from rag.core.documents import processing

    captured = {}

    class SpyTracker:
        def __init__(self, *args, **kwargs):
            captured.update(kwargs)

        def start_timer(self):
            raise RuntimeError("stop after construction")

    monkeypatch.setattr(processing, "EmbeddingTracker", SpyTracker)
    # Eight passages built from one six-page PDF — what build_pdf_documents hands over.
    passages = [Document(page_content=f"passage {i}", metadata={}) for i in range(8)]

    try:
        processing.process_documents(
            documents=passages,
            embeddings=_Embeddings(),
            account_id=None,
            module="knowledge_base",
            collection_name="coll",
            vector_store=object(),
            source_document_count=1,
        )
    except RuntimeError:
        pass

    assert captured["expected_document_count"] == 8, "the ledger's pair stays in embedding units"


def test_without_a_declared_count_the_documents_handed_in_are_counted(monkeypatch):
    """The default is unchanged: one document per item, counted before chunking."""
    from rag.core.documents import processing

    captured = {}

    class SpyTracker:
        def __init__(self, *args, **kwargs):
            captured.update(kwargs)

        def start_timer(self):
            raise RuntimeError("stop after construction")

    monkeypatch.setattr(processing, "EmbeddingTracker", SpyTracker)
    pages = [Document(page_content="paragraph. " * 500, metadata={}) for _ in range(3)]

    try:
        processing.process_documents(
            documents=pages,
            embeddings=_Embeddings(),
            account_id=None,
            module="knowledge_base",
            collection_name="coll",
            vector_store=object(),
        )
    except RuntimeError:
        pass

    assert captured["expected_document_count"] == 3
