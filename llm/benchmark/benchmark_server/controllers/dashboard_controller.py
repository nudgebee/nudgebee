"""Dashboard API — lightweight endpoints for tenant/user/account/tool-config resolution.

These power the HTML dashboard without requiring direct DB access from the frontend.
"""

import logging
import os
from typing import Optional

import requests
from fastapi import APIRouter
from sqlalchemy import text

from benchmark_server.utils.db_utils import get_db, db_engine

router = APIRouter(prefix="/dashboard", tags=["dashboard"])
logger = logging.getLogger(__name__)

LLM_SERVER_URL = os.environ.get("LLM_SERVER_URL", "http://localhost:9999")


@router.get("/tenants")
async def list_tenants():
    """Return tenants that have at least one active user."""
    if not db_engine:
        return {"tenants": [], "error": "DB not configured"}
    try:
        with db_engine.connect() as conn:
            rows = conn.execute(
                text(
                    "SELECT DISTINCT t.id, t.name "
                    "FROM tenant t "
                    'JOIN tenant_users tu ON tu.tenant = t.id '
                    'JOIN users u ON u.id = tu."user" '
                    "WHERE u.status = 'active' "
                    "ORDER BY t.name"
                )
            ).fetchall()
            return {"tenants": [{"id": str(r[0]), "name": str(r[1])} for r in rows]}
    except Exception as e:
        logger.error("list_tenants failed: %s", e)
        return {"tenants": [], "error": str(e)}


@router.get("/tenants/{tenant_id}/users")
async def list_users(tenant_id: str):
    """Return active users for a tenant."""
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
async def list_accounts(tenant_id: str):
    """Return active cloud accounts for a tenant."""
    if not db_engine:
        return {"accounts": [], "error": "DB not configured"}
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
            return {
                "accounts": [
                    {
                        "id": str(r[0]),
                        "account_name": str(r[1]),
                        "cloud_provider": str(r[2]),
                    }
                    for r in rows
                ]
            }
    except Exception as e:
        logger.error("list_accounts failed: %s", e)
        return {"accounts": [], "error": str(e)}


@router.get("/tool-configs")
async def get_tool_configs(account_id: str, tenant_id: str, user_id: str):
    """Proxy tool configs from the LLM server."""
    try:
        r = requests.post(
            f"{LLM_SERVER_URL}/v1/tools/configs",
            json={"account_id": account_id},
            headers={"x-tenant-id": tenant_id, "x-user-id": user_id},
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
async def resolve_tenant(tenant_id: str):
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
async def resolve_account(account_id: str):
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
