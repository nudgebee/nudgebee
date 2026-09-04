"""Tests for the live-knowledge-base gate on search collection selection.

Search used to read every collection an account had ever created, including
the ones left behind by deleted or archived integrations. These cover which
collections survive that gate and — just as important — which ones are never
subject to it.
"""

from types import SimpleNamespace

import pytest

from rag.core.llm import rag
from rag.core.utils.db_query import get_live_kb_collection_names

TENANT = "11111111-1111-1111-1111-111111111111"
ACCOUNT = "22222222-2222-2222-2222-222222222222"
LIVE_INTEGRATION = "33333333-3333-3333-3333-333333333333"
DEAD_INTEGRATION = "44444444-4444-4444-4444-444444444444"
LIVE_KB = "55555555-5555-5555-5555-555555555555"
ARCHIVED_KB = "66666666-6666-6666-6666-666666666666"


def _collection(name, **metadata):
    metadata.setdefault("module", "knowledge_base")
    return SimpleNamespace(name=name, metadata=metadata)


def _integration_collection(integration_id):
    return _collection(f"{integration_id}_knowledge_base", tenant_id=TENANT, source="confluence")


def _manual_collection(kb_id):
    return _collection(f"kb_{kb_id}", account=ACCOUNT, source="user_kb")


@pytest.fixture
def live_names(monkeypatch):
    """Stub the DB lookup; mutate the returned set to shape the live scope."""
    names = {f"{LIVE_INTEGRATION}_knowledge_base", f"kb_{LIVE_KB}"}
    monkeypatch.setattr(rag, "get_live_kb_collection_names", lambda account_id, tenant_id: names)
    return names


def test_orphaned_integration_collection_is_dropped(live_names):
    collections = [_integration_collection(LIVE_INTEGRATION), _integration_collection(DEAD_INTEGRATION)]
    kept = rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT)
    assert kept == [f"{LIVE_INTEGRATION}_knowledge_base"]


def test_archived_manual_kb_collection_is_dropped(live_names):
    collections = [_manual_collection(LIVE_KB), _manual_collection(ARCHIVED_KB)]
    kept = rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT)
    assert kept == [f"kb_{LIVE_KB}"]


def test_tenant_user_kb_collection_is_never_dropped(live_names):
    # <tenant_id>_knowledge_base shares its shape with integration collections
    # but has no llm_knowledgebases row of its own.
    collections = [_collection(f"{TENANT}_knowledge_base", tenant_id=TENANT, source="user_kb")]
    kept = rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT)
    assert kept == [f"{TENANT}_knowledge_base"]


def test_legacy_per_account_collection_is_never_dropped(live_names):
    # Renamed from <account_id>_docs by module_retag_migration: same suffix as
    # an integration collection, but tagged with an account.
    collections = [_collection(f"{ACCOUNT}_knowledge_base", account=ACCOUNT)]
    kept = rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT)
    assert kept == [f"{ACCOUNT}_knowledge_base"]


def test_unrelated_collections_are_never_dropped(live_names):
    collections = [
        _collection("nudgebee_docs", account="global", source="nudgebee_docs"),
        _collection(f"{ACCOUNT}_prometheus", account=ACCOUNT, module="prometheus"),
    ]
    kept = rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT)
    assert kept == ["nudgebee_docs", f"{ACCOUNT}_prometheus"]


def test_unresolvable_scope_excludes_kbs_but_preserves_product_docs(monkeypatch):
    monkeypatch.setattr(rag, "get_live_kb_collection_names", lambda account_id, tenant_id: None)
    collections = [
        _integration_collection(DEAD_INTEGRATION),
        _manual_collection(ARCHIVED_KB),
        _collection("nudgebee_docs", account="global"),
    ]
    kept = rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT)
    assert kept == ["nudgebee_docs"]


def test_no_kb_backed_collections_skips_the_lookup(monkeypatch):
    def _fail(account_id, tenant_id):
        raise AssertionError("live-KB lookup should not run without KB-backed collections")

    monkeypatch.setattr(rag, "get_live_kb_collection_names", _fail)
    collections = [_collection(f"{ACCOUNT}_prometheus", account=ACCOUNT, module="prometheus")]
    assert rag._drop_dead_kb_collections(collections, ACCOUNT, TENANT) == [f"{ACCOUNT}_prometheus"]


def test_module_filter_runs_before_the_live_kb_gate(live_names):
    collections = [
        _integration_collection(LIVE_INTEGRATION),
        _integration_collection(DEAD_INTEGRATION),
        _collection(f"{ACCOUNT}_prometheus", account=ACCOUNT, module="prometheus"),
    ]
    names = rag._filter_collections_for_module_and_account(
        collections, "knowledge_base", ACCOUNT, None, tenant_id=TENANT
    )
    assert names == [f"{LIVE_INTEGRATION}_knowledge_base"]


def test_explicit_collection_name_cannot_bypass_the_gate(live_names):
    collections = [_integration_collection(DEAD_INTEGRATION)]
    names = rag._filter_collections_for_module_and_account(
        collections, "knowledge_base", ACCOUNT, f"{DEAD_INTEGRATION}_knowledge_base", tenant_id=TENANT
    )
    assert names == []


def test_restrict_narrows_to_the_named_collection(live_names):
    """``restrict_to_collection`` searches ONE collection instead of adding it.

    The knowledge-base retrieval probe uses this to answer "does THIS knowledge
    base answer the question?" — without it the named collection is merely
    appended and the search still spans everything.
    """
    collections = [_integration_collection(LIVE_INTEGRATION), _manual_collection(LIVE_KB)]
    names = rag._filter_collections_for_module_and_account(
        collections, "knowledge_base", ACCOUNT, f"kb_{LIVE_KB}", tenant_id=TENANT, restrict_to_collection=True
    )
    assert names == [f"kb_{LIVE_KB}"]

    # Same inputs without the flag keep the documented additive behaviour.
    additive = rag._filter_collections_for_module_and_account(
        collections, "knowledge_base", ACCOUNT, f"kb_{LIVE_KB}", tenant_id=TENANT
    )
    assert sorted(additive) == sorted([f"{LIVE_INTEGRATION}_knowledge_base", f"kb_{LIVE_KB}"])


def test_restrict_cannot_reach_a_collection_outside_the_scope(live_names):
    """Narrowing intersects with what was already visible — it never widens.

    Without the intersection, naming any collection would turn a restricted
    search into a targeted read of another tenant's knowledge base.
    """
    collections = [_manual_collection(LIVE_KB)]
    names = rag._filter_collections_for_module_and_account(
        collections,
        "knowledge_base",
        ACCOUNT,
        "kb_99999999-9999-9999-9999-999999999999",
        tenant_id=TENANT,
        restrict_to_collection=True,
    )
    assert names == []


def test_restrict_respects_the_live_kb_gate(live_names):
    """An archived KB's collection cannot be reached by naming it explicitly."""
    collections = [_integration_collection(DEAD_INTEGRATION)]
    names = rag._filter_collections_for_module_and_account(
        collections,
        "knowledge_base",
        ACCOUNT,
        f"{DEAD_INTEGRATION}_knowledge_base",
        tenant_id=TENANT,
        restrict_to_collection=True,
    )
    assert names == []


@pytest.mark.parametrize("account_id, tenant_id", [("global", None), ("", None), (None, None), ("not-a-uuid", "")])
def test_non_uuid_scope_short_circuits_before_the_query(account_id, tenant_id):
    # Reaching Postgres with these would raise "invalid input syntax for type
    # uuid"; there is no DB in this suite, so a None return proves we never got
    # that far. None means "don't filter" downstream.
    assert get_live_kb_collection_names(account_id, tenant_id) is None
