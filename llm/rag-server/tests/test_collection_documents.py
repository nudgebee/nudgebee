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
    calls = {}

    def scroll(**kwargs):
        calls["scroll"] = kwargs
        return POINTS, "next-id"

    def retrieve(**kwargs):
        calls["retrieve"] = kwargs
        return [p for p in POINTS if p.id in kwargs["ids"]]

    monkeypatch.setattr(collection, "get_qdrant_client", lambda: SimpleNamespace(scroll=scroll, retrieve=retrieve))
    app = FastAPI()
    app.include_router(collection_controller.router)
    return TestClient(app), calls


def test_list_documents_returns_titles_links_without_content(client):
    http, calls = client
    response = http.get("/collections/c_knowledge_base/documents", params={"limit": 3, "offset": "a1"})
    assert response.status_code == 200
    data = response.json()["data"]
    assert data["next_offset"] == "next-id"
    assert data["items"] == [
        {"id": "a1", "title": "Page A", "url": "https://wiki/a", "source_id": "1"},
        {"id": "b2", "title": "KB0001 reset", "url": "https://sn/kb0001", "source_id": None},
        {"id": "7", "title": None, "url": None, "source_id": None},
        {"id": "8", "title": None, "url": None, "source_id": None},
    ]
    assert calls["scroll"]["limit"] == 3
    assert calls["scroll"]["offset"] == "a1"
    assert calls["scroll"]["with_vectors"] is False


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
