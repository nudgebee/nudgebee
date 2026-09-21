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
