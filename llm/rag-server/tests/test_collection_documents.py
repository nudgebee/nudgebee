from types import SimpleNamespace

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from controllers import collection_controller
from rag.core.documents import collection


def _point(point_id, content, metadata):
    return SimpleNamespace(id=point_id, payload={"page_content": content, "metadata": metadata})


POINTS = [
    _point("a1", "Title: Page A\nbody", {"title": "Page A", "url": "https://wiki/a", "page_id": "1"}),
    _point("b2", "Title: KB0001 reset\nsteps", {"url": "https://sn/kb0001", "kb_number": "KB0001"}),
    _point(7, "plain note", {"source": "/tmp/x.json"}),
    _point(8, "runbook part 1", {"parent_id": "doc-r", "parent_content": "runbook part 1 runbook part 2"}),
    _point(9, "runbook part 2", {"parent_id": "doc-r", "parent_content": "runbook part 1 runbook part 2"}),
]


@pytest.fixture
def client(monkeypatch):
    calls = {"scroll": []}

    def scroll(**kwargs):
        # Mimic Qdrant: points in id order, inclusive offset, next point as cursor.
        calls["scroll"].append(kwargs)
        ids = [p.id for p in POINTS]
        start = ids.index(kwargs["offset"]) if kwargs["offset"] is not None else 0
        page = POINTS[start : start + kwargs["limit"]]
        end = start + kwargs["limit"]
        return page, (POINTS[end].id if end < len(POINTS) else None)

    def retrieve(**kwargs):
        calls["retrieve"] = kwargs
        return [p for p in POINTS if p.id in kwargs["ids"]]

    monkeypatch.setattr(collection, "get_qdrant_client", lambda: SimpleNamespace(scroll=scroll, retrieve=retrieve))
    app = FastAPI()
    app.include_router(collection_controller.router)
    return TestClient(app), calls


def test_list_documents_returns_titles_links_without_content(client):
    http, calls = client
    response = http.get("/collections/c_knowledge_base/documents", params={"limit": 50})
    assert response.status_code == 200
    data = response.json()["data"]
    assert data["next_offset"] is None
    # Chunk 9 is the second chunk of the document listed through chunk 8.
    assert data["items"] == [
        {"id": "a1", "title": "Page A", "url": "https://wiki/a", "source_id": "1"},
        {"id": "b2", "title": "KB0001 reset", "url": "https://sn/kb0001", "source_id": "KB0001"},
        {"id": "7", "title": None, "url": None, "source_id": None},
        {"id": "8", "title": None, "url": None, "source_id": None},
    ]
    assert calls["scroll"][0]["with_vectors"] is False


def test_pages_count_documents_and_never_repeat_one(client, monkeypatch):
    http, _ = client
    monkeypatch.setattr(collection, "_DOCUMENT_SCROLL_BATCH", 2)
    seen, offset = [], None
    for _ in range(10):
        params = {"limit": 2, **({"offset": offset} if offset else {})}
        data = http.get("/collections/c_knowledge_base/documents", params=params).json()["data"]
        seen += [item["id"] for item in data["items"]]
        offset = data["next_offset"]
        if offset is None:
            break
    assert seen == ["a1", "b2", "7", "8"]


def test_listing_stops_at_scan_limit_with_cursor(client, monkeypatch):
    http, _ = client
    monkeypatch.setattr(collection, "_DOCUMENT_SCROLL_BATCH", 2)
    monkeypatch.setattr(collection, "_DOCUMENT_SCAN_LIMIT", 2)
    data = http.get("/collections/c_knowledge_base/documents", params={"limit": 50}).json()["data"]
    assert [item["id"] for item in data["items"]] == ["a1", "b2"]
    assert data["next_offset"] == "7"


def test_blank_chunk_is_not_a_first_chunk():
    blank = {"page_content": "  ", "metadata": {"parent_id": "p", "parent_content": "real text"}}
    assert collection._is_listed_point(blank) is False


def test_get_document_returns_content_and_parses_numeric_id(client):
    http, calls = client
    response = http.get("/collections/kb_1/documents/7")
    assert response.status_code == 200
    assert response.json()["data"]["content"] == "plain note"
    assert calls["retrieve"]["ids"] == [7]


def test_get_chunk_returns_whole_parent_document(client):
    http, _ = client
    response = http.get("/collections/kb_1/documents/9")
    assert response.json()["data"]["content"] == "runbook part 1 runbook part 2"


def test_get_missing_document_is_404(client):
    http, _ = client
    assert http.get("/collections/kb_1/documents/gone").status_code == 404


def test_title_falls_back_to_confluence_url_slug():
    slug = {"url": "https://x.atlassian.net/wiki/spaces/NS/pages/590112/Notification+Rules"}
    assert collection._document_title(slug, "Notification Rules\nbody") == "Notification Rules"
    # A truncated slug ending in the page id is not a title.
    assert collection._document_title({"url": "https://x.atlassian.net/wiki/spaces/NS/pages/9142310/"}, "body") is None


def test_non_http_url_is_dropped():
    assert collection._document_url({"url": "javascript:alert(1)"}) is None


def test_chunk_index_marks_the_listed_chunk():
    from rag.core.documents.collection import _is_listed_point

    # One row per document, without a full copy of the document on every chunk.
    first = {"page_content": "part 1", "metadata": {"parent_id": "doc-a", "chunk_index": 0}}
    second = {"page_content": "part 2", "metadata": {"parent_id": "doc-a", "chunk_index": 1}}

    assert _is_listed_point(first) is True
    assert _is_listed_point(second) is False


def test_points_indexed_before_chunk_index_still_list():
    from rag.core.documents.collection import _is_listed_point

    # The parent_content rule stays for points written before the marker existed.
    legacy = {
        "page_content": "runbook part 1",
        "metadata": {"parent_id": "doc-r", "parent_content": "runbook part 1 runbook part 2"},
    }
    unchunked = {"page_content": "whole page", "metadata": {"url": "https://wiki/a"}}

    assert _is_listed_point(legacy) is True
    assert _is_listed_point(unchunked) is True
