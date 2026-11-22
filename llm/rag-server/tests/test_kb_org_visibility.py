"""Organisation knowledge bases: who may search them, and moving one between scopes.

The visibility cases live in ``fixtures/kb_visibility_cases.json`` and are shared
with llm-server's visibility helper, so the two services apply one rule.
"""

import asyncio
import json
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi import HTTPException
from qdrant_client import QdrantClient
from qdrant_client.models import Distance, PointStruct, VectorParams

from controllers import knowledgebase_controller as kb
from rag.core.cache import get_collection_list_cache
from rag.core.documents import collection as collection_module
from rag.core.llm import rag
from rag.core.metadata_cache import get_metadata_cache
from rag.core.utils.db_query import _live_kb_names_from_rows

FIXTURE = json.loads((Path(__file__).parent / "fixtures" / "kb_visibility_cases.json").read_text())
TENANT = "11111111-1111-1111-1111-111111111111"
ACCOUNT = FIXTURE["accounts"]["A"]


def _rows(case):
    accounts = FIXTURE["accounts"]
    return [
        SimpleNamespace(**{**row, "account_id": accounts[row["account_id"]] if row["account_id"] else None})
        for row in case["rows"]
    ]


@pytest.mark.parametrize("case", FIXTURE["cases"], ids=[c["name"] for c in FIXTURE["cases"]])
def test_shared_visibility_cases(case):
    account = FIXTURE["accounts"][case["account"]] if case["account"] else None
    assert _live_kb_names_from_rows(_rows(case), account) == set(case["expected"])


def test_restricted_organisation_collection_is_dropped_from_search(monkeypatch):
    # Its tenant_id metadata admits it for every account in the tenant; the gate
    # is what keeps it from an account that is not on the list.
    org = SimpleNamespace(name="kb_o1", metadata={"module": "knowledge_base", "tenant_id": TENANT, "source": "user_kb"})
    monkeypatch.setattr(rag, "get_live_kb_collection_names", lambda account_id, tenant_id: set())
    assert rag._filter_collections_for_module_and_account([org], "knowledge_base", ACCOUNT, None, TENANT) == []
    monkeypatch.setattr(rag, "get_live_kb_collection_names", lambda account_id, tenant_id: {"kb_o1"})
    assert rag._filter_collections_for_module_and_account([org], "knowledge_base", ACCOUNT, None, TENANT) == ["kb_o1"]


def test_organisation_collection_never_reaches_another_tenant(monkeypatch):
    org = SimpleNamespace(name="kb_o1", metadata={"module": "knowledge_base", "tenant_id": TENANT, "source": "user_kb"})
    monkeypatch.setattr(rag, "get_live_kb_collection_names", lambda account_id, tenant_id: {"kb_o1"})
    other_tenant = "99999999-9999-9999-9999-999999999999"
    assert rag._filter_collections_for_module_and_account([org], "knowledge_base", ACCOUNT, None, other_tenant) == []


@pytest.fixture
def qdrant(monkeypatch):
    client = QdrantClient(":memory:")
    monkeypatch.setattr(collection_module, "get_qdrant_client", lambda: client)
    get_collection_list_cache().invalidate()
    get_metadata_cache().invalidate()
    yield client
    get_collection_list_cache().invalidate()
    get_metadata_cache().invalidate()


def _metadata(client, name):
    # The Qdrant server deletes a metadata key set to None; the in-memory client
    # keeps it as None. Readers treat both the same, so compare without them.
    return {k: v for k, v in (client.get_collection(name).config.metadata or {}).items() if v is not None}


def _kb_collection(client, metadata):
    client.create_collection("kb_k1", vectors_config=VectorParams(size=4, distance=Distance.COSINE), metadata=metadata)
    client.upsert("kb_k1", [PointStruct(id=1, vector=[0.1, 0.2, 0.3, 0.4], payload={"page_content": "step 1"})])


def test_widening_moves_the_collection_to_the_tenant_without_reindexing(qdrant):
    _kb_collection(qdrant, {"module": "knowledge_base", "account": ACCOUNT, "source": "user_kb"})
    get_metadata_cache().set("kb_k1", {"account": ACCOUNT}, points_count=1)

    assert collection_module.set_collection_scope("kb_k1", None, TENANT) is True

    assert _metadata(qdrant, "kb_k1") == {"module": "knowledge_base", "tenant_id": TENANT, "source": "user_kb"}
    assert qdrant.get_collection("kb_k1").points_count == 1
    assert get_metadata_cache().get("kb_k1") is None


def test_narrowing_moves_the_collection_back_to_one_account(qdrant):
    _kb_collection(qdrant, {"module": "knowledge_base", "tenant_id": TENANT, "source": "user_kb"})

    collection_module.set_collection_scope("kb_k1", ACCOUNT, None)

    assert _metadata(qdrant, "kb_k1") == {
        "module": "knowledge_base",
        "account": ACCOUNT,
        "source": "user_kb",
    }


def test_rescoping_a_kb_without_a_collection_is_a_no_op(qdrant):
    # A KB whose upload failed or has not run has a row but no collection.
    assert collection_module.set_collection_scope("kb_missing", None, TENANT) is False
    result = asyncio.run(kb.set_kb_scope(kb.KBScopeRequest(kb_id="missing", tenant_id=TENANT)))
    assert result == {"status": "success", "collection": "kb_missing", "updated": False}


@pytest.mark.parametrize("account_id, tenant_id", [(None, None), (ACCOUNT, TENANT)])
def test_scope_needs_exactly_one_owner(account_id, tenant_id):
    with pytest.raises(ValueError):
        collection_module.set_collection_scope("kb_k1", account_id, tenant_id)
    with pytest.raises(HTTPException) as exc:
        asyncio.run(kb.set_kb_scope(kb.KBScopeRequest(kb_id="k1", account_id=account_id, tenant_id=tenant_id)))
    assert exc.value.status_code == 400


def test_create_without_an_owner_is_rejected():
    with pytest.raises(HTTPException) as exc:
        asyncio.run(kb.create_kb(kb.KBCreateRequest(kb_id="k1", data="step 1")))
    assert exc.value.status_code == 400


def test_organisation_kb_is_indexed_for_the_tenant(monkeypatch):
    calls = {}
    monkeypatch.setattr(kb, "get_first_active_account_for_tenant", lambda tenant_id: ACCOUNT)
    monkeypatch.setattr(kb, "get_embeddings", lambda account_id: calls.setdefault("embeddings_account", account_id))
    monkeypatch.setattr(kb, "process_documents", lambda **kwargs: calls.update(kwargs) or ("kb_k1", ["d1"]))

    result = asyncio.run(kb.create_kb(kb.KBCreateRequest(tenant_id=TENANT, kb_id="k1", data="step 1")))

    assert result["status"] == "success"
    assert calls["account_id"] is None
    assert calls["tenant_id"] == TENANT
    assert calls["embeddings_account"] == ACCOUNT
    assert calls["classify_account_id"] == ACCOUNT


def test_account_kb_is_indexed_for_its_account_as_before(monkeypatch):
    calls = {}
    monkeypatch.setattr(kb, "get_embeddings", lambda account_id: calls.setdefault("embeddings_account", account_id))
    monkeypatch.setattr(kb, "process_documents", lambda **kwargs: calls.update(kwargs) or ("kb_k1", ["d1"]))

    asyncio.run(kb.create_kb(kb.KBCreateRequest(account_id=ACCOUNT, tenant_id=TENANT, kb_id="k1", data="step 1")))

    assert calls["account_id"] == ACCOUNT
    assert calls["tenant_id"] is None
    assert calls["embeddings_account"] == ACCOUNT
