import hashlib
from types import SimpleNamespace

import pytest
from fastapi import FastAPI, HTTPException
from fastapi.testclient import TestClient

from controllers import knowledge_document as kd


def test_discovery_bounds_body_and_metadata():
    body = "Ω reference line\n" * 200000 + "FINAL-CANARY"
    excerpt, metadata = kd.discovery_document(body, {"body": body, "title": body, "retrieval_id": "point"}, 4096)
    assert len(excerpt.encode()) <= 4096
    assert "body" not in metadata
    assert len(metadata["title"]) <= 1024
    assert metadata["content_sha256"] == hashlib.sha256(body.encode()).hexdigest()
    assert metadata["content_bytes"] == len(body.encode())
    assert metadata["completeness"] == "indexed_document"


def test_discovery_keeps_the_scrapers_document_id():
    # llm-server keys a document's Fact/SOP mark on "<collection>|<source id>";
    # without the id every hit falls back to its URL and no mark matches.
    _, confluence = kd.discovery_document(
        "steps", {"page_id": 114458625, "url": "https://wiki/p", "collection": "c"}, 4096
    )
    assert confluence["page_id"] == "114458625"
    _, servicenow = kd.discovery_document("steps", {"sys_id": "abc123", "collection": "c"}, 4096)
    assert servicenow["sys_id"] == "abc123"


def test_discovery_keeps_classification_metadata():
    # #38623's classifier stamps kind/capabilities on every chunk; llm-server's
    # KnowledgeDocumentPurpose reads them straight off this response. Caught
    # live, not by a unit test, when a real HTTP round trip through this
    # exact code path showed classification metadata vanishing.
    _, metadata = kd.discovery_document(
        "steps", {"kind": "sop", "capabilities": ["k8s.workload_restart"], "collection": "c"}, 4096
    )
    assert metadata["kind"] == "sop"
    assert metadata["capabilities"] == ["k8s.workload_restart"]


def test_discovery_omits_absent_classification_metadata():
    _, metadata = kd.discovery_document("steps", {"collection": "c"}, 4096)
    assert "kind" not in metadata
    assert "capabilities" not in metadata


def test_discovery_ignores_non_list_capabilities():
    # Defensive: capabilities is always a list from the classifier, but a
    # malformed/legacy payload should not crash discovery or leak a
    # non-list value the Go client's []any type assertion would reject anyway.
    _, metadata = kd.discovery_document("steps", {"capabilities": "not-a-list", "collection": "c"}, 4096)
    assert "capabilities" not in metadata


def test_discovery_bounds_capabilities_list():
    many_caps = [f"cap.{i}" for i in range(50)]
    _, metadata = kd.discovery_document("steps", {"capabilities": many_caps, "collection": "c"}, 4096)
    assert len(metadata["capabilities"]) == 32
    long_cap = "x" * 5000
    _, metadata = kd.discovery_document("steps", {"capabilities": [long_cap], "collection": "c"}, 4096)
    assert len(metadata["capabilities"][0]) == 256


@pytest.fixture
def endpoint(monkeypatch):
    state = {"body": "reference\n" * 200000 + "FINAL-CANARY", "live": {"kb_123"}, "account": "account"}
    monkeypatch.setattr(kd, "get_tenant_id_for_account", lambda _: "tenant")
    monkeypatch.setattr(
        kd,
        "_filter_collections_for_module_and_account",
        lambda collections, module, account, name, **kwargs: [name] if account == state["account"] else [],
    )
    monkeypatch.setattr(kd, "get_live_kb_collection_names", lambda *args: state["live"])
    monkeypatch.setattr(
        kd,
        "get_qdrant_client",
        lambda: SimpleNamespace(
            get_collection=lambda _: SimpleNamespace(config=SimpleNamespace(metadata={})),
            retrieve=lambda **kwargs: [SimpleNamespace(payload={"page_content": state["body"]})],
        ),
    )
    app = FastAPI()
    app.include_router(kd.router)
    return TestClient(app), state


def test_exact_stream_revalidates_access_and_version(endpoint):
    client, state = endpoint
    request = dict(
        account_id="account",
        collection_name="kb_123",
        document_id="123",
        content_sha256=hashlib.sha256(state["body"].encode()).hexdigest(),
    )
    response = client.post("/knowledge/document", json=request)
    assert response.status_code == 200
    assert response.text.endswith("FINAL-CANARY")
    assert int(response.headers["content-length"]) == len(state["body"].encode())
    assert client.post("/knowledge/document", json={**request, "validate_only": True}).status_code == 204
    assert client.post("/knowledge/document", json={**request, "account_id": "other"}).status_code == 404
    state["body"] += "changed"
    assert client.post("/knowledge/document", json=request).status_code == 409
    state["live"] = set()
    assert client.post("/knowledge/document", json=request).status_code == 404
    state["live"] = None
    assert client.post("/knowledge/document", json=request).status_code == 503


def test_transfer_limit():
    with pytest.raises(HTTPException) as exc:
        kd.content_version("x" * (kd.MAX_DOCUMENT_BYTES + 1))
    assert exc.value.status_code == 413
    excerpt, metadata = kd.discovery_document("x" * (kd.MAX_DOCUMENT_BYTES + 1), {}, 4096)
    assert len(excerpt) == 4096
    assert metadata["content_bytes"] > kd.MAX_DOCUMENT_BYTES
    assert metadata["content_sha256"] == ""


# --- whole-document reads -------------------------------------------------

from rag.core.documents import collection, processing, source_store  # noqa: E402
from rag.core.types import Document  # noqa: E402


def _chunk_points(content, parent="parent", **overrides):
    """Index `content` the way ingest does: real chunker, one fake point per chunk."""
    chunks = processing._chunk_long_documents([Document(page_content=content, metadata={"parent_id": parent})])
    return [
        SimpleNamespace(id=f"p{i}", payload={"page_content": c.page_content, "metadata": {**c.metadata, **overrides}})
        for i, c in enumerate(chunks)
    ]


class _ScrollClient:
    def __init__(self, points, page=3):
        self.points, self.page = points, page

    def scroll(self, collection_name, scroll_filter, limit, with_payload, with_vectors, offset):
        wanted = scroll_filter.must[0].match.value
        matching = [p for p in self.points if p.payload["metadata"].get("parent_id") == wanted]
        start = offset or 0
        end = start + min(limit, self.page)
        return matching[start:end], (end if end < len(matching) else None)


def _paragraph_page():
    return "\n\n".join(f"Step {i}: " + ("restart the pod and check the logs " * 8) for i in range(12))


def _normalised(text):
    return "\n\n".join(p for p in text.split("\n\n") if p.strip())


def test_assemble_round_trips_paragraph_chunks():
    page = _paragraph_page()
    points = _chunk_points(page)
    assert len(points) > 3
    text, reason = kd.assemble_document(_ScrollClient(points), "c", points[2].payload)
    assert reason == ""
    assert text == _normalised(page)


def test_assemble_round_trips_hard_sliced_paragraph():
    page = "intro\n\n" + ("abcdefghij" * 120) + "\n\ntail"
    points = _chunk_points(page)
    text, reason = kd.assemble_document(_ScrollClient(points), "c", points[0].payload)
    assert (text, reason) == (_normalised(page), "")


@pytest.mark.parametrize(
    "mutate, reason",
    [
        (lambda pts: pts.pop(1), "missing_chunks"),
        (lambda pts: pts.append(SimpleNamespace(id="dup", payload=pts[1].payload)), "inconsistent_siblings"),
        (
            lambda pts: pts[1].payload["metadata"].update(chunk_count=99),
            "inconsistent_siblings",
        ),
        (lambda pts: pts[1].payload.update(page_content="unrelated text " * 20), "overlap_mismatch"),
    ],
)
def test_assemble_falls_back_on_an_unclean_chunk_set(mutate, reason):
    points = _chunk_points(_paragraph_page())
    request = SimpleNamespace(payload=dict(points[0].payload))
    mutate(points)
    text, got = kd.assemble_document(_ScrollClient(points), "c", request.payload)
    assert text is None
    assert got == reason


def test_assemble_leaves_unchunked_pages_alone():
    assert kd.assemble_document(None, "c", {"page_content": "short", "metadata": {}}) == (None, "not_chunked")
    one = {"page_content": "x", "metadata": {"parent_id": "p", "chunk_index": 0, "chunk_count": 1}}
    assert kd.assemble_document(None, "c", one) == (None, "single_chunk")


def test_assemble_ignores_other_pages_in_the_collection():
    page_a, page_b = _paragraph_page(), "other page\n\n".join(["z" * 300] * 6)
    points = _chunk_points(page_a, "a") + _chunk_points(page_b, "b")
    text, _ = kd.assemble_document(_ScrollClient(points), "c", points[0].payload)
    assert text == _normalised(page_a)


def test_assemble_caps_the_chunk_count(monkeypatch):
    monkeypatch.setattr(kd, "MAX_ASSEMBLED_CHUNKS", 2)
    points = _chunk_points(_paragraph_page())
    assert kd.assemble_document(_ScrollClient(points), "c", points[0].payload) == (None, "too_many_chunks")


@pytest.fixture
def chunked_endpoint(monkeypatch):
    points = _chunk_points(_paragraph_page())
    monkeypatch.setattr(kd, "get_tenant_id_for_account", lambda _: "tenant")
    monkeypatch.setattr(kd, "_filter_collections_for_module_and_account", lambda c, m, a, name, **kw: [name])
    monkeypatch.setattr(kd, "get_live_kb_collection_names", lambda *args: {"kb_123"})
    scroller = _ScrollClient(points)
    monkeypatch.setattr(
        kd,
        "get_qdrant_client",
        lambda: SimpleNamespace(
            get_collection=lambda _: SimpleNamespace(config=SimpleNamespace(metadata={})),
            retrieve=lambda **kwargs: [points[1]],
            scroll=scroller.scroll,
        ),
    )
    app = FastAPI()
    app.include_router(kd.router)
    chunk = points[1].payload["page_content"]
    request = dict(
        account_id="a",
        collection_name="kb_123",
        document_id="p1",
        content_sha256=hashlib.sha256(chunk.encode()).hexdigest(),
    )
    return TestClient(app), request, points


def test_whole_document_returns_the_page_with_its_own_hash(chunked_endpoint):
    client, request, points = chunked_endpoint
    response = client.post("/knowledge/document", json={**request, "whole_document": True})
    page = _normalised(_paragraph_page())
    assert response.status_code == 200
    assert response.text == page
    assert response.headers["x-content-scope"] == "document"
    assert response.headers["x-content-sha256"] == hashlib.sha256(page.encode()).hexdigest()
    assert int(response.headers["content-length"]) == len(page.encode())


def test_default_read_is_still_the_chunk(chunked_endpoint):
    client, request, points = chunked_endpoint
    response = client.post("/knowledge/document", json=request)
    assert response.text == points[1].payload["page_content"]
    assert response.headers["x-content-scope"] == "chunk"
    assert response.headers["x-content-sha256"] == request["content_sha256"]


def test_whole_document_still_rejects_a_changed_handle(chunked_endpoint):
    client, request, _ = chunked_endpoint
    stale = {**request, "content_sha256": "0" * 64, "whole_document": True}
    assert client.post("/knowledge/document", json=stale).status_code == 409


def test_whole_document_falls_back_to_the_chunk_on_a_broken_set(chunked_endpoint):
    client, request, points = chunked_endpoint
    points.pop(2)
    response = client.post("/knowledge/document", json={**request, "whole_document": True})
    assert response.status_code == 200
    assert response.text == points[1].payload["page_content"]
    assert response.headers["x-content-scope"] == "chunk"


def test_validate_only_stays_chunk_level(chunked_endpoint):
    client, request, _ = chunked_endpoint
    response = client.post("/knowledge/document", json={**request, "whole_document": True, "validate_only": True})
    assert response.status_code == 204
    assert response.headers["x-content-sha256"] == request["content_sha256"]


# --- rebuilding from the stored source when chunks are missing ------------


def _confluence_html(steps=14, word="restart"):
    return "".join(f"<h2>Step {i}</h2><p>{(word + ' the pod and check the logs. ') * 8}</p>" for i in range(steps))


def _confluence_points(html, title="Runbook", **extra):
    """Index a page the way the Confluence loader does, title line included."""
    from rag.core.documents.scraper import extract_content

    text = f"Title: {title}\n\n{extract_content(html)}"
    meta = {"parent_id": "parent", "page_id": "42", "source": "confluence", "title": title, **extra}
    chunks = processing._chunk_long_documents([Document(page_content=text, metadata=meta)])
    return [
        SimpleNamespace(id=f"p{i}", payload={"page_content": c.page_content, "metadata": c.metadata})
        for i, c in enumerate(chunks)
    ], text


def _with_source(monkeypatch, html, content_type="text/html"):
    monkeypatch.setattr(
        source_store,
        "get_by_source_id",
        lambda collection, source_id: (
            {"content": html, "content_type": content_type, "title": "Runbook"} if source_id == "42" else None
        ),
    )


def test_missing_chunks_are_recovered_from_the_stored_source(monkeypatch):
    html = _confluence_html()
    points, text = _confluence_points(html)
    asked = points[1].payload
    del points[2:4]  # another page's identical chunks overwrote these
    _with_source(monkeypatch, html)
    page, reason = kd.assemble_document(_ScrollClient(points), "c", asked)
    assert (page, reason) == (_normalised(text), "")


def test_a_source_that_has_moved_past_the_chunks_is_refused(monkeypatch):
    html = _confluence_html()
    points, _ = _confluence_points(html)
    asked = points[1].payload
    del points[2]
    _with_source(monkeypatch, _confluence_html(word="rollback"))  # edited since indexing
    assert kd.assemble_document(_ScrollClient(points), "c", asked) == (None, "missing_chunks")


@pytest.mark.parametrize(
    "extra, content_type",
    [
        ({"source": "servicenow"}, "text/html"),
        ({"is_figure": True}, "text/html"),
        ({}, "text/plain"),
    ],
)
def test_the_stored_source_is_only_used_for_a_confluence_page_body(monkeypatch, extra, content_type):
    html = _confluence_html()
    points, _ = _confluence_points(html, **extra)
    asked = points[1].payload
    del points[2]
    _with_source(monkeypatch, html, content_type)
    assert kd.assemble_document(_ScrollClient(points), "c", asked)[0] is None


def test_no_stored_source_means_the_chunk(monkeypatch):
    html = _confluence_html()
    points, _ = _confluence_points(html)
    asked = points[1].payload
    del points[2]
    monkeypatch.setattr(source_store, "get_by_source_id", lambda *args: None)
    assert kd.assemble_document(_ScrollClient(points), "c", asked) == (None, "missing_chunks")


def test_an_unreadable_source_store_means_the_chunk(monkeypatch):
    html = _confluence_html()
    points, _ = _confluence_points(html)
    asked = points[1].payload
    del points[2]

    def boom(*args):
        raise RuntimeError("qdrant down")

    monkeypatch.setattr(source_store, "get_by_source_id", boom)
    assert kd.assemble_document(_ScrollClient(points), "c", asked) == (None, "missing_chunks")


# --- the whole-page read never costs the caller the chunk -----------------


def test_a_failing_sibling_gather_still_returns_the_chunk(chunked_endpoint, monkeypatch):
    client, request, points = chunked_endpoint

    def boom(*args, **kwargs):
        raise RuntimeError("qdrant timeout")

    monkeypatch.setattr(kd, "assemble_document", boom)
    response = client.post("/knowledge/document", json={**request, "whole_document": True})
    assert response.status_code == 200
    assert response.text == points[1].payload["page_content"]
    assert response.headers["x-content-scope"] == "chunk"


def test_an_oversized_page_still_returns_the_chunk(chunked_endpoint, monkeypatch):
    client, request, points = chunked_endpoint
    chunk = points[1].payload["page_content"]
    monkeypatch.setattr(kd, "assemble_document", lambda *args: ("x" * (len(chunk) + 1), ""))
    real = kd.content_version

    def limited(text):
        if text != chunk:
            raise HTTPException(413, "too big")
        return real(text)

    monkeypatch.setattr(kd, "content_version", limited)
    response = client.post("/knowledge/document", json={**request, "whole_document": True})
    assert response.status_code == 200
    assert response.text == chunk
    assert response.headers["x-content-scope"] == "chunk"


def test_a_converter_failure_or_huge_source_means_the_chunk(monkeypatch):
    html = _confluence_html()
    points, _ = _confluence_points(html)
    asked = points[1].payload
    del points[2]
    _with_source(monkeypatch, html)

    def boom(_html):
        raise RecursionError("nested too deep")

    monkeypatch.setattr("rag.core.documents.scraper.extract_content", boom)
    assert kd.assemble_document(_ScrollClient(points), "c", asked) == (None, "missing_chunks")
    monkeypatch.undo()
    _with_source(monkeypatch, html)
    monkeypatch.setattr(kd, "MAX_SOURCE_HTML_BYTES", 10)
    assert kd.assemble_document(_ScrollClient(points), "c", asked) == (None, "missing_chunks")


def test_nothing_is_gathered_for_an_unauthorised_collection(chunked_endpoint, monkeypatch):
    client, request, _ = chunked_endpoint
    gathered = []
    monkeypatch.setattr(kd, "assemble_document", lambda *args: gathered.append(1) or ("x", ""))
    monkeypatch.setattr(kd, "_filter_collections_for_module_and_account", lambda *a, **k: [])
    assert client.post("/knowledge/document", json={**request, "whole_document": True}).status_code == 404
    monkeypatch.setattr(kd, "_filter_collections_for_module_and_account", lambda c, m, a, name, **kw: [name])
    assert (
        client.post(
            "/knowledge/document", json={**request, "content_sha256": "0" * 64, "whole_document": True}
        ).status_code
        == 409
    )
    assert gathered == []


# --- one stored-source lookup for the Docs page and the agent's read ------


def test_stored_source_is_found_by_the_id_the_chunks_carry(monkeypatch):
    seen = []
    monkeypatch.setattr(
        source_store, "get_by_source_id", lambda col, sid: seen.append((col, sid)) or {"content": "<p>x</p>"}
    )
    assert collection.stored_source_for("c", {"page_id": 42})["content"] == "<p>x</p>"
    assert collection.stored_source_for("c", {"sys_id": "abc"})["content"] == "<p>x</p>"
    assert collection.stored_source_for("c", {"page_id": 0}) is not None  # 0 is a real id
    assert seen == [("c", "42"), ("c", "abc"), ("c", "0")]


def test_stored_source_is_none_without_an_id_or_content(monkeypatch):
    monkeypatch.setattr(source_store, "get_by_source_id", lambda col, sid: {"content": ""})
    assert collection.stored_source_for("c", {"page_id": 42}) is None
    assert collection.stored_source_for("c", {"title": "no id"}) is None
    monkeypatch.setattr(source_store, "get_by_source_id", lambda col, sid: None)
    assert collection.stored_source_for("c", {"page_id": 42}) is None


def test_the_agent_fallback_uses_the_shared_lookup(monkeypatch):
    html = _confluence_html()
    points, _ = _confluence_points(html)
    asked = points[1].payload
    del points[2]
    calls = []

    def lookup(col, metadata):
        calls.append(metadata.get("page_id"))
        return {"content": html, "content_type": "text/html"}

    monkeypatch.setattr(kd, "stored_source_for", lookup)
    page, _ = kd.assemble_document(_ScrollClient(points), "c", asked)
    assert page is not None and calls == ["42"]


def test_join_chunks_with_no_overlap_concatenates():
    # text[-0:] is the whole string, so a zero overlap must not be checked as a prefix.
    assert kd.join_chunks(["abc", "def"], overlap=0) == "abcdef"
