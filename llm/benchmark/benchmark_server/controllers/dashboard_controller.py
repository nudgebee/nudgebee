"""Dashboard API — lightweight endpoints for tenant/user/account/tool-config resolution.

These power the HTML dashboard without requiring direct DB access from the frontend.
"""

import logging
import os
from typing import Optional

import requests
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import text

from benchmark_server.middleware.auth import (
    AuthUser,
    get_authz,
    get_current_user,
)
from benchmark_server.middleware.authz import list_all_active_tenants
from benchmark_server.utils.db_utils import db_engine

router = APIRouter(prefix="/dashboard", tags=["dashboard"])
logger = logging.getLogger(__name__)

LLM_SERVER_URL = os.environ.get("LLM_SERVER_URL", "http://localhost:9999")


@router.get("/tenants")
async def list_tenants(authz=Depends(get_authz)):
    """Return tenants the caller can act on.

    super_admin: all active tenants in the system.
    Everyone else: just the tenants they're a member of (from authz).
    """
    if authz.is_super_admin:
        return {"tenants": list_all_active_tenants()}

    # Non-admin: list comes from authz (no per-request DB query needed —
    # the cache primed at login already has the membership). Names are
    # fetched once for display.
    if not db_engine or not authz.tenants:
        return {"tenants": []}
    tenant_ids = [ta.tenant_id for ta in authz.tenants]
    try:
        with db_engine.connect() as conn:
            rows = conn.execute(
                text("SELECT id::text, name FROM tenant WHERE id::text = ANY(:ids)"),
                {"ids": tenant_ids},
            ).fetchall()
            name_by_id = {str(r[0]): str(r[1]) for r in rows}
            return {
                "tenants": [
                    {"id": ta.tenant_id, "name": name_by_id.get(ta.tenant_id, ta.tenant_id)}
                    for ta in authz.tenants
                ]
            }
    except Exception as e:
        logger.error("list_tenants name resolution failed: %s", e)
        # Fall back to ID-only display if the name lookup fails.
        return {"tenants": [{"id": ta.tenant_id, "name": ta.tenant_id} for ta in authz.tenants]}


@router.get("/tenants/{tenant_id}/users")
async def list_users(tenant_id: str, authz=Depends(get_authz)):
    """Return active users for a tenant, scoped to caller's access.

    super_admin sees any tenant; everyone else must have explicit
    membership via authz. Without this guard, a tenant-A user could
    enumerate users in tenant B by passing the tenant UUID in the URL
    (cross-tenant info disclosure).
    """
    if not authz.is_super_admin and not authz.has_tenant(tenant_id):
        raise HTTPException(status_code=403, detail="You do not have access to this tenant.")
    if not db_engine:
        return {"users": [], "error": "DB not configured"}
    try:
        with db_engine.connect() as conn:
            rows = conn.execute(
                text(
                    "SELECT u.id, u.username, u.display_name "
                    "FROM users u "
                    'JOIN tenant_users tu ON tu."user" = u.id '
                    "WHERE tu.tenant = :tid AND u.status = 'active' "
                    "ORDER BY u.display_name"
                ),
                {"tid": tenant_id},
            ).fetchall()
            return {
                "users": [
                    {"id": str(r[0]), "username": str(r[1]), "display_name": str(r[2])}
                    for r in rows
                ]
            }
    except Exception as e:
        logger.error("list_users failed: %s", e)
        return {"users": [], "error": str(e)}


@router.get("/tenants/{tenant_id}/accounts")
async def list_accounts(tenant_id: str, authz=Depends(get_authz)):
    """Return cloud accounts for a tenant, scoped to caller's access.

    super_admin: all active accounts in the requested tenant.
    Other roles: must have membership in this tenant (403 otherwise);
    accounts are filtered to the user's allowed set per authz —
    tenant_admin sees all in their tenant, regular users see their
    RBAC-derived list.
    """
    if not db_engine:
        return {"accounts": [], "error": "DB not configured"}

    # Cross-tenant access guard.
    if not authz.is_super_admin and not authz.has_tenant(tenant_id):
        from fastapi import HTTPException

        raise HTTPException(
            status_code=403,
            detail="You do not have access to this tenant.",
        )

    try:
        with db_engine.connect() as conn:
            rows = conn.execute(
                text(
                    "SELECT id, account_name, cloud_provider "
                    "FROM cloud_accounts "
                    "WHERE tenant = :tid AND status = 'active' "
                    "ORDER BY account_name"
                ),
                {"tid": tenant_id},
            ).fetchall()

            all_accounts = [
                {
                    "id": str(r[0]),
                    "account_name": str(r[1]),
                    "cloud_provider": str(r[2]),
                }
                for r in rows
            ]

            if authz.is_super_admin:
                return {"accounts": all_accounts}

            ta = authz.tenant(tenant_id)
            if ta is None:
                return {"accounts": []}
            # tenant_admin's account_ids already includes the full
            # tenant; regular users have their RBAC-derived subset.
            allowed = set(ta.account_ids or [])
            return {
                "accounts": [a for a in all_accounts if a["id"] in allowed],
            }
    except Exception as e:
        logger.error("list_accounts failed: %s", e)
        return {"accounts": [], "error": str(e)}


@router.get("/tool-configs")
async def get_tool_configs(
    account_id: str,
    tenant_id: Optional[str] = None,
    user: AuthUser = Depends(get_current_user),
    authz=Depends(get_authz),
):
    """Proxy tool configs from the LLM server.

    The caller can pin a specific tenant via the ``tenant_id`` query
    param — validated against the caller's authz tenant set so a user
    cannot read configs from a tenant they don't belong to (this used
    to be an IDOR; flagged by Gemini code-review comment 3068257362).
    For super_admin any tenant is accepted; for everyone else, the
    tenant must be in their ``authz.tenants`` list.

    ``account_id`` must also be in the caller's allowed account list
    for the resolved tenant. ``user_id`` is intentionally **never**
    accepted as a query parameter — the LLM server is told the
    authenticated user, full stop, so a tenant_admin can't impersonate
    another user via this proxy.
    """
    # Resolve target tenant.
    if tenant_id:
        if not authz.is_super_admin and not authz.has_tenant(tenant_id):
            raise HTTPException(status_code=403, detail="You do not have access to this tenant.")
        effective_tenant_id = tenant_id
    else:
        # Fall back to the caller's only / first tenant (the typical
        # single-tenant user case). Super_admin without an explicit
        # tenant gets an error — they must specify which tenant.
        if authz.is_super_admin:
            raise HTTPException(
                status_code=400,
                detail="tenant_id is required for super_admin callers.",
            )
        if not authz.tenants:
            raise HTTPException(status_code=403, detail="No tenant memberships.")
        effective_tenant_id = authz.tenants[0].tenant_id

    # Validate account access for non-super-admin callers.
    if not authz.is_super_admin:
        ta = authz.tenant(effective_tenant_id)
        if ta is None:
            raise HTTPException(status_code=403, detail="You do not have access to this tenant.")
        if ta.account_ids and account_id not in ta.account_ids:
            raise HTTPException(
                status_code=403,
                detail="You do not have access to the selected account.",
            )

    try:
        r = requests.post(
            f"{LLM_SERVER_URL}/v1/tools/configs",
            json={"account_id": account_id},
            headers={
                "x-tenant-id": effective_tenant_id,
                # Always the authenticated user — never a query-param override.
                "x-user-id": user.user_id,
            },
            timeout=30,
        )
        r.raise_for_status()
        data = r.json()
        configs = data.get("data", {}).get("configs", [])
        return {"configs": configs}
    except Exception as e:
        logger.error("get_tool_configs failed: %s", e)
        return {"configs": [], "error": str(e)}


@router.get("/resolve/tenant/{tenant_id}")
async def resolve_tenant(tenant_id: str, user: AuthUser = Depends(get_current_user)):
    """Resolve tenant UUID to name."""
    if not db_engine:
        return {"name": tenant_id}
    try:
        with db_engine.connect() as conn:
            row = conn.execute(
                text("SELECT name FROM tenant WHERE id = :tid"),
                {"tid": tenant_id},
            ).fetchone()
            return {"name": str(row[0]) if row else tenant_id}
    except Exception as e:
        logger.warning("resolve_tenant failed for %s: %s", tenant_id, e)
        return {"name": tenant_id}


@router.get("/resolve/account/{account_id}")
async def resolve_account(account_id: str, user: AuthUser = Depends(get_current_user)):
    """Resolve cloud_account UUID to name."""
    if not db_engine:
        return {"name": account_id}
    try:
        with db_engine.connect() as conn:
            row = conn.execute(
                text("SELECT account_name FROM cloud_accounts WHERE id = :aid"),
                {"aid": account_id},
            ).fetchone()
            return {"name": str(row[0]) if row else account_id}
    except Exception as e:
        logger.warning("resolve_account failed for %s: %s", account_id, e)
        return {"name": account_id}
