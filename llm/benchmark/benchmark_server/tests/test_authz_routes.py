"""Cross-tenant 403 regression tests for dashboard routes.

Round-2 review of PR #28295 found that ``GET /dashboard/tenants/{tid}/users``
let any authenticated user enumerate users in any tenant by passing a
foreign ``tenant_id`` in the URL (cross-tenant info disclosure). This
file pins the fix and the equivalent guards on the sibling routes.
"""

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient


@pytest.fixture
def dashboard_client():
    """Build a TestClient with the dashboard router and overridable
    ``get_authz`` / ``get_current_user`` dependencies. Returns
    a ``(client, set_overrides)`` pair."""
    from benchmark_server.controllers.dashboard_controller import router
    from benchmark_server.middleware.auth import get_authz, get_current_user

    app = FastAPI()
    app.include_router(router)

    def _set_overrides(authz, user):
        app.dependency_overrides[get_authz] = lambda: authz
        app.dependency_overrides[get_current_user] = lambda: user

    yield TestClient(app), _set_overrides
    app.dependency_overrides.clear()


# ---------- /dashboard/tenants/{tenant_id}/users ----------


def test_list_users_403_for_non_member(dashboard_client, make_authz, make_user):
    client, set_overrides = dashboard_client
    set_overrides(make_authz(tenants=["tenant-A"]), make_user())
    r = client.get("/dashboard/tenants/tenant-B/users")
    assert r.status_code == 403


def test_list_users_super_admin_not_403(dashboard_client, make_authz, make_user):
    """Super_admin must NOT be 403'd. (DB is stubbed so we can't assert
    200, but we verify the auth gate doesn't reject super_admin.)"""
    client, set_overrides = dashboard_client
    set_overrides(make_authz(is_super_admin=True), make_user())
    r = client.get("/dashboard/tenants/tenant-anything/users")
    assert r.status_code != 403


def test_list_users_member_not_403(dashboard_client, make_authz, make_user):
    client, set_overrides = dashboard_client
    set_overrides(make_authz(tenants=["tenant-A"]), make_user())
    r = client.get("/dashboard/tenants/tenant-A/users")
    assert r.status_code != 403


# ---------- /dashboard/tenants/{tenant_id}/accounts ----------


def test_list_accounts_403_for_non_member(dashboard_client, make_authz, make_user):
    client, set_overrides = dashboard_client
    set_overrides(make_authz(tenants=["tenant-A"]), make_user())
    r = client.get("/dashboard/tenants/tenant-B/accounts")
    assert r.status_code == 403


def test_list_accounts_super_admin_not_403(dashboard_client, make_authz, make_user):
    client, set_overrides = dashboard_client
    set_overrides(make_authz(is_super_admin=True), make_user())
    r = client.get("/dashboard/tenants/tenant-anything/accounts")
    assert r.status_code != 403


# ---------- /dashboard/tool-configs ----------


def test_tool_configs_403_for_non_member(dashboard_client, make_authz, make_user):
    """Round-1 review IDOR: caller pins a foreign tenant_id in the query
    param. Non-member must be 403'd."""
    client, set_overrides = dashboard_client
    set_overrides(make_authz(tenants=["tenant-A"]), make_user())
    r = client.get("/dashboard/tool-configs?account_id=acct-1&tenant_id=tenant-B")
    assert r.status_code == 403


def test_tool_configs_403_for_account_not_in_allowed(dashboard_client, make_authz, make_user):
    """Member of tenant-A but the account isn't in their allowed list
    (tenant_admin sees all; regular user sees an RBAC-derived subset)."""
    client, set_overrides = dashboard_client
    set_overrides(
        make_authz(tenants=[{"id": "tenant-A", "role": "user", "account_ids": ["acct-1"]}]),
        make_user(),
    )
    r = client.get("/dashboard/tool-configs?account_id=acct-other&tenant_id=tenant-A")
    assert r.status_code == 403


def test_tool_configs_super_admin_must_specify_tenant(dashboard_client, make_authz, make_user):
    """Super_admin without an explicit tenant_id should get 400 (must
    pick a tenant), NOT silently fall through to a wrong tenant."""
    client, set_overrides = dashboard_client
    set_overrides(make_authz(is_super_admin=True), make_user())
    r = client.get("/dashboard/tool-configs?account_id=acct-1")  # no tenant_id
    assert r.status_code == 400
