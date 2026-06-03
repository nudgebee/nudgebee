"""Authz-helper regression tests for the benchmark controllers.

These cover the pure-function authz checks that gate every benchmark
route. They're the closest thing to "tenant-scope regression" tests
in the auth surface — without them, an authz bug in any of these
helpers would silently let a tenant-A user act on tenant-B's runs.
"""

from unittest.mock import patch

import pytest
from fastapi import HTTPException


# ---------- _verify_run_access ----------


def _patch_run_tenant(tenant_id):
    """Helper to mock run_manager.get_run_tenant_id."""
    return patch(
        "benchmark_server.controllers.agent_benchmark_controller." "run_manager.get_run_tenant_id",
        return_value=tenant_id,
    )


def test_verify_run_access_404_when_run_not_found(make_authz):
    from benchmark_server.controllers.agent_benchmark_controller import _verify_run_access

    authz = make_authz(tenants=["tenant-A"])
    with _patch_run_tenant(None):
        with pytest.raises(HTTPException) as exc:
            _verify_run_access("ghost-run", authz)
    assert exc.value.status_code == 404


def test_verify_run_access_403_for_cross_tenant(make_authz):
    """User in tenant-A attempts to read run owned by tenant-B → 403."""
    from benchmark_server.controllers.agent_benchmark_controller import _verify_run_access

    authz = make_authz(tenants=["tenant-A"])
    with _patch_run_tenant("tenant-B"):
        with pytest.raises(HTTPException) as exc:
            _verify_run_access("some-run", authz)
    assert exc.value.status_code == 403


def test_verify_run_access_super_admin_bypasses(make_authz):
    """Super_admin can reach any tenant's run."""
    from benchmark_server.controllers.agent_benchmark_controller import _verify_run_access

    authz = make_authz(is_super_admin=True)
    with _patch_run_tenant("tenant-B"):
        _verify_run_access("some-run", authz)  # no raise


def test_verify_run_access_member_passes(make_authz):
    """User who's a member of the run's tenant passes."""
    from benchmark_server.controllers.agent_benchmark_controller import _verify_run_access

    authz = make_authz(tenants=["tenant-A"])
    with _patch_run_tenant("tenant-A"):
        _verify_run_access("some-run", authz)  # no raise


# ---------- _require_admin ----------


def test_require_admin_blocks_regular_user(make_authz):
    from benchmark_server.controllers.agent_benchmark_controller import _require_admin

    authz = make_authz(tenants=[{"id": "tenant-A", "role": "user"}])
    with pytest.raises(HTTPException) as exc:
        _require_admin(authz)
    assert exc.value.status_code == 403


def test_require_admin_allows_tenant_admin(make_authz):
    from benchmark_server.controllers.agent_benchmark_controller import _require_admin

    authz = make_authz(tenants=[{"id": "tenant-A", "role": "tenant_admin"}])
    _require_admin(authz)  # no raise


def test_require_admin_allows_super_admin(make_authz):
    from benchmark_server.controllers.agent_benchmark_controller import _require_admin

    authz = make_authz(is_super_admin=True)
    _require_admin(authz)  # no raise


# ---------- _resolve_run_identity ----------


def _make_request(**fields):
    """Tiny stand-in for an AgentBenchmarkRequest."""
    defaults = {"tenant_id": None, "account_id": None, "user_id": None}
    defaults.update(fields)
    return type("R", (), defaults)


def test_resolve_run_identity_400_missing_tenant(make_authz, make_user):
    from benchmark_server.controllers.agent_benchmark_controller import _resolve_run_identity

    user = make_user()
    authz = make_authz(tenants=["tenant-A"])
    request = _make_request(account_id="acct-1")  # no tenant_id
    with pytest.raises(HTTPException) as exc:
        _resolve_run_identity(user, authz, request)
    assert exc.value.status_code == 400


def test_resolve_run_identity_400_missing_account(make_authz, make_user):
    from benchmark_server.controllers.agent_benchmark_controller import _resolve_run_identity

    user = make_user()
    authz = make_authz(tenants=["tenant-A"])
    request = _make_request(tenant_id="tenant-A")  # no account_id
    with pytest.raises(HTTPException) as exc:
        _resolve_run_identity(user, authz, request)
    assert exc.value.status_code == 400


def test_resolve_run_identity_403_when_tenant_not_in_authz(make_authz, make_user):
    """Cross-tenant: user in tenant-A tries to launch a run for tenant-B."""
    from benchmark_server.controllers.agent_benchmark_controller import _resolve_run_identity

    user = make_user()
    authz = make_authz(tenants=["tenant-A"])
    request = _make_request(tenant_id="tenant-B", account_id="acct-1")
    with pytest.raises(HTTPException) as exc:
        _resolve_run_identity(user, authz, request)
    assert exc.value.status_code == 403


def test_resolve_run_identity_403_when_account_not_in_allowed(make_authz, make_user):
    """Account scoping: user is in tenant-A but doesn't own the requested account."""
    from benchmark_server.controllers.agent_benchmark_controller import _resolve_run_identity

    user = make_user()
    authz = make_authz(tenants=[{"id": "tenant-A", "role": "user", "account_ids": ["acct-1"]}])
    request = _make_request(tenant_id="tenant-A", account_id="acct-other")
    with pytest.raises(HTTPException) as exc:
        _resolve_run_identity(user, authz, request)
    assert exc.value.status_code == 403


def test_resolve_run_identity_returns_authenticated_user_id(make_authz, make_user):
    """``request.user_id`` must be ignored — the launched run is always
    attributed to the authenticated user, never to a request-supplied
    impersonation."""
    from benchmark_server.controllers.agent_benchmark_controller import _resolve_run_identity

    user = make_user(user_id="real-user")
    authz = make_authz(tenants=[{"id": "tenant-A", "role": "user", "account_ids": ["acct-1"]}])
    request = _make_request(tenant_id="tenant-A", account_id="acct-1", user_id="impersonated-user")
    user_id, tenant_id, account_id = _resolve_run_identity(user, authz, request)
    assert user_id == "real-user"  # NOT "impersonated-user"
    assert tenant_id == "tenant-A"
    assert account_id == "acct-1"


def test_resolve_run_identity_super_admin_can_use_any_tenant(make_authz, make_user):
    from benchmark_server.controllers.agent_benchmark_controller import _resolve_run_identity

    user = make_user()
    authz = make_authz(is_super_admin=True)
    request = _make_request(tenant_id="any-tenant", account_id="any-account")
    user_id, tenant_id, account_id = _resolve_run_identity(user, authz, request)
    assert tenant_id == "any-tenant"
    assert account_id == "any-account"
