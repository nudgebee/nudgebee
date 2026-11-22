"""rag-server's live knowledge-base gate against a real, fully migrated Postgres.

The shared visibility cases (``fixtures/kb_visibility_cases.json``) run through
``get_live_kb_collection_names`` and its SQL, so a query change that loads the
wrong rows fails here. Each test seeds its own tenant and removes it afterwards.
CI runs this in migrations-validate against the database it has just migrated,
with only the packages the gate needs installed:

    KB_TEST_DATABASE_URL=postgres://... REQUIRE_DB_TESTS=true pytest tests/test_kb_visibility_db.py

KB_TEST_DATABASE_URL, not APP_DATABASE_URL: the latter usually points at a live
database through .env, and these tests write to the one they are given.
"""

import json
import os
import uuid
from pathlib import Path

import pytest

DSN = os.environ.get("KB_TEST_DATABASE_URL", "")
if not DSN:
    if os.environ.get("REQUIRE_DB_TESTS") == "true":
        raise RuntimeError("REQUIRE_DB_TESTS is set but KB_TEST_DATABASE_URL is not")
    pytest.skip("set KB_TEST_DATABASE_URL to a migrated database to run the KB SQL tests", allow_module_level=True)

# utils.config refuses to load without these; the gate itself needs only the database.
os.environ.setdefault("APP_DATABASE_URL", DSN)
os.environ.setdefault("RELAY_SERVER_ENDPOINT", "http://test")
os.environ.setdefault("RELAY_SERVER_SECRET_KEY", "test")

from sqlalchemy import create_engine, text  # noqa: E402

from rag.core.utils import db_query  # noqa: E402

FIXTURE = json.loads((Path(__file__).parent / "fixtures" / "kb_visibility_cases.json").read_text())


@pytest.fixture
def engine(monkeypatch):
    eng = create_engine(DSN)
    monkeypatch.setattr(db_query, "engine", eng)
    yield eng
    eng.dispose()


@pytest.fixture
def tenant(engine):
    """Accounts A and B in one tenant, account C in a second tenant."""
    s = {k: str(uuid.uuid4()) for k in ("user", "tenant", "other_tenant", "A", "B", "C")}
    try:
        with engine.begin() as c:
            c.execute(
                text("INSERT INTO users (id, username, display_name) VALUES (:id, :name, 'KB test user')"),
                {"id": s["user"], "name": "kb-test-" + s["user"]},
            )
            for t in (s["tenant"], s["other_tenant"]):
                c.execute(
                    text("INSERT INTO tenant (id, name, created_by, updated_by) VALUES (:id, :name, :u, :u)"),
                    {"id": t, "name": "kb-test-" + t, "u": s["user"]},
                )
            for acct, t in ((s["A"], s["tenant"]), (s["B"], s["tenant"]), (s["C"], s["other_tenant"])):
                c.execute(
                    text(
                        "INSERT INTO cloud_accounts (id, account_name, account_type, tenant, created_by, updated_by)"
                        " VALUES (:id, :name, 'K8s', :t, :u, :u)"
                    ),
                    {"id": acct, "name": "kb-test-" + acct, "t": t, "u": s["user"]},
                )
        yield s
    finally:
        tenants = {"t1": s["tenant"], "t2": s["other_tenant"]}
        with engine.begin() as c:
            for q in (
                "DELETE FROM llm_knowledgebases WHERE tenant_id IN (:t1, :t2)",
                "DELETE FROM integrations_cloud_accounts WHERE tenant_id IN (:t1, :t2)",
                "DELETE FROM integrations WHERE tenant_id IN (:t1, :t2)",
                "DELETE FROM cloud_accounts WHERE tenant IN (:t1, :t2)",
                "DELETE FROM tenant WHERE id IN (:t1, :t2)",
            ):
                c.execute(text(q), tenants)
            c.execute(text("DELETE FROM users WHERE id = :u"), {"u": s["user"]})


def _integration(c, tenant_id, status="enabled"):
    iid = str(uuid.uuid4())
    c.execute(
        text(
            "INSERT INTO integrations (id, tenant_id, type, source, name, status)"
            " VALUES (:id, :t, 'confluence', 'user', :name, :status)"
        ),
        {"id": iid, "t": tenant_id, "name": "Confluence " + iid[:8], "status": status},
    )
    return iid


def _kb(c, tenant_id, *, account=None, integration=None, name="", enabled=True, visibility, status="active"):
    kid = str(uuid.uuid4())
    c.execute(
        text(
            "INSERT INTO llm_knowledgebases (id, tenant_id, account_id, name, data, data_format, data_filename,"
            " status, kb_type, kb_source, integration_id, enabled, visibility)"
            " VALUES (:id, :t, :a, :name, '', 'text', 'kb.txt', :status, :type, :source, :i, :enabled, :vis)"
        ),
        {
            "id": kid,
            "t": tenant_id,
            "a": account,
            "name": name,
            "status": status,
            "type": "integration" if integration else "manual",
            "source": "confluence" if integration else None,
            "i": integration,
            "enabled": enabled,
            "vis": visibility,
        },
    )
    return kid


def _override(c, tenant_id, kb_id, account, enabled):
    c.execute(
        text(
            "INSERT INTO llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled)"
            " VALUES (:kb, :a, :t, :enabled)"
        ),
        {"kb": kb_id, "a": account, "t": tenant_id, "enabled": enabled},
    )


@pytest.mark.parametrize("case", FIXTURE["cases"], ids=[c["name"] for c in FIXTURE["cases"]])
def test_shared_visibility_cases_through_sql(case, engine, tenant):
    accounts = {"A": tenant["A"], "B": tenant["B"]}
    account = accounts[case["account"]] if case["account"] else None
    names = {}  # seeded collection -> fixture collection
    integrations = {}
    with engine.begin() as c:
        for r in case["rows"]:
            integration = None
            if r["integration_id"]:
                if r["integration_id"] not in integrations:
                    integrations[r["integration_id"]] = _integration(c, tenant["tenant"])
                integration = integrations[r["integration_id"]]
            kid = _kb(
                c,
                tenant["tenant"],
                account=accounts[r["account_id"]] if r["account_id"] else None,
                integration=integration,
                name=r["name"] or "",
                enabled=r["enabled"],
                visibility=r["visibility"],
            )
            if r["override_enabled"] is not None:
                _override(c, tenant["tenant"], kid, account, r["override_enabled"])
            seeded = f"{integration}_knowledge_base" if integration else f"kb_{kid}"
            fixture = f"{r['integration_id']}_knowledge_base" if r["integration_id"] else f"kb_{r['id']}"
            names[seeded] = fixture

    got = db_query.get_live_kb_collection_names(account, tenant["tenant"])
    assert got is not None, "the gate query failed"
    assert {names[n] for n in got} == set(case["expected"])
    # The other tenant's account reaches none of it.
    assert db_query.get_live_kb_collection_names(tenant["C"], tenant["other_tenant"]) == set()


def test_archived_kbs_and_disabled_integrations_are_not_searchable(engine, tenant):
    # Clauses only the SQL applies: the shared cases start from rows already past them.
    t = tenant["tenant"]
    with engine.begin() as c:
        live = _integration(c, t)
        disabled = _integration(c, t, status="disabled")
        _kb(c, t, integration=live, name="Live", visibility="organisation")
        _kb(c, t, integration=disabled, name="Disabled", visibility="organisation")
        archived = _kb(c, t, name="Archived", visibility="organisation", status="archived")
        processing = _kb(c, t, account=tenant["A"], name="Loading", visibility="account", status="processing")

    got = db_query.get_live_kb_collection_names(tenant["A"], t)
    assert f"{live}_knowledge_base" in got
    assert f"{disabled}_knowledge_base" not in got, "a disabled integration's KB is not served"
    assert f"kb_{archived}" not in got, "an archived KB is not served"
    assert f"kb_{processing}" in got, "a KB that is reloading keeps serving what it has"


def test_one_accounts_switch_does_not_apply_to_another(engine, tenant):
    t, a, b = tenant["tenant"], tenant["A"], tenant["B"]
    with engine.begin() as c:
        org = _kb(c, t, name="DB failover", visibility="organisation")
        selected = _kb(c, t, name="Payments", visibility="selected_accounts")
        _override(c, t, org, b, False)
        _override(c, t, selected, a, True)

    assert db_query.get_live_kb_collection_names(a, t) == {f"kb_{org}", f"kb_{selected}"}
    assert db_query.get_live_kb_collection_names(b, t) == set()
