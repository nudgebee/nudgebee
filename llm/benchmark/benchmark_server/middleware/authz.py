"""DB-backed authorization for the benchmark server.

The session JWT carries identity only (``user_id`` / ``email`` / ``display_name``).
This module is the source of truth for *what the user is allowed to do* —
which tenants they're a member of, what role they have in each, and which
cloud accounts they can reach within each tenant.

Authz is fetched from the api-server schema (``tenant_users``, ``user_roles``,
``user_groups`` + ``usergroup_users`` + ``group_roles``, ``cloud_accounts``)
and cached in-process per ``user_id`` so we don't pay 3 SQL queries on every
request. Cache TTL defaults to the session lifetime (24h); it can be tuned
or invalidated explicitly.

Why the JWT doesn't hold this:
  - Stale risk: RBAC changes in api-server don't propagate until the user
    re-logs in.
  - JWT bloat: a user in 9 tenants with 50 accounts each makes the cookie
    too large to fit comfortably under the 4KB browser limit.
  - Single source of truth: api-server controls RBAC; we mirror it
    read-only at request time.

Why we cache rather than re-querying every request:
  - Three SQL queries per request adds up at scale.
  - RBAC changes are rare in this internal tool — staleness is tolerable.
  - The cache is invalidated on logout and ``POST /auth/refresh-authz``;
    "log out and back in" is a natural reset for the user.
"""

import logging
import os
import threading
import time
from dataclasses import dataclass, field
from typing import Optional

from sqlalchemy import text

from benchmark_server.utils.db_utils import db_engine

logger = logging.getLogger(__name__)

ROLE_TENANT_ADMIN = "tenant_admin"
ROLE_USER = "user"

# Session-lifetime cache by default. Tunable via env var when freshness
# matters more than performance (e.g. lowered to 300 = 5 min for testing).
_CACHE_TTL = int(os.environ.get("AUTHZ_CACHE_TTL_SECONDS", "86400"))

_authz_cache: dict[str, tuple[float, "Authz"]] = {}
_cache_lock = threading.Lock()


@dataclass
class TenantAccess:
    """The user's access within a single tenant.

    ``role`` is per-tenant — a user can be ``tenant_admin`` in one tenant
    and a regular ``user`` in another. ``account_ids`` reflects what they
    can act on inside this tenant: for tenant_admin, all active accounts
    in that tenant; for regular users, only those granted via direct or
    group-based RBAC roles.
    """

    tenant_id: str
    role: str
    account_ids: list[str] = field(default_factory=list)


@dataclass
class Authz:
    """The user's full authorization context across all tenants they're in.

    ``is_super_admin`` is the system-wide override — when true, the user
    can act across every tenant in the system regardless of their explicit
    ``tenants`` membership. The dashboard's tenant dropdown for super
    admins is populated from a separate "all tenants in system" query
    (not from this object), since enumerating every tenant here would
    bloat the cache and most super-admin sessions don't need that list
    materialised.
    """

    user_id: str
    is_super_admin: bool = False
    tenants: list[TenantAccess] = field(default_factory=list)

    def tenant(self, tenant_id: str) -> Optional[TenantAccess]:
        """Return the user's access in ``tenant_id`` or ``None`` if none."""
        if not tenant_id:
            return None
        for ta in self.tenants:
            if ta.tenant_id == tenant_id:
                return ta
        return None

    def has_tenant(self, tenant_id: str) -> bool:
        """True iff the user has explicit membership in ``tenant_id``.

        Super-admins are NOT auto-true here — call sites should check
        ``is_super_admin`` separately when they want to allow super-admin
        bypass independently of explicit membership.
        """
        return self.tenant(tenant_id) is not None

    def tenant_ids(self) -> list[str]:
        """List of tenant_ids the user has explicit membership in.

        Server-side list endpoints filter their queries with
        ``WHERE tenant_id IN (...)`` against this list (super_admin
        callers bypass the filter and see everything).
        """
        return [ta.tenant_id for ta in self.tenants]


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def get_user_authz(user_id: str, *, force_refresh: bool = False) -> Authz:
    """Return the user's authorization context, hitting cache when fresh.

    Cache key is ``user_id``. Set ``force_refresh=True`` to bypass cache
    (used by ``POST /auth/refresh-authz`` and tests). On cache miss or
    forced refresh, runs ~3 SQL queries against the api-server schema
    and stores the result for ``_CACHE_TTL`` seconds.
    """
    if not user_id:
        return Authz(user_id="")

    if not force_refresh:
        with _cache_lock:
            hit = _authz_cache.get(user_id)
        if hit is not None:
            ts, authz = hit
            if time.time() - ts < _CACHE_TTL:
                return authz

    authz = _fetch_authz_from_db(user_id)
    with _cache_lock:
        _authz_cache[user_id] = (time.time(), authz)
    return authz


def invalidate_authz(user_id: str) -> None:
    """Remove the user's cached authz. Called from /auth/logout and on
    explicit refresh requests so the next call hits the DB again."""
    if not user_id:
        return
    with _cache_lock:
        _authz_cache.pop(user_id, None)


# ---------------------------------------------------------------------------
# DB resolution
# ---------------------------------------------------------------------------


def _fetch_authz_from_db(user_id: str) -> Authz:
    """Resolve the user's authz from the api-server schema. Three logical
    queries: super_admin probe, tenant memberships, per-tenant accounts.

    Schema drift safety: every query is independently try/except'd. A
    missing column or table degrades that one piece (e.g. account_ids
    becomes empty) rather than blowing up the whole authz fetch — the
    user can still log in and the failure is logged for diagnosis.
    """
    if not db_engine:
        logger.error("authz: db_engine not configured; returning empty authz")
        return Authz(user_id=user_id)

    is_super_admin = False
    tenants: list[TenantAccess] = []

    try:
        with db_engine.connect() as conn:
            # 1. Super-admin probe (system-level role grants global access).
            try:
                row = conn.execute(
                    text(
                        "SELECT 1 FROM user_roles "
                        "WHERE user_id = :uid "
                        "  AND entity_type = 'system' "
                        "  AND role = 'super_admin' "
                        "LIMIT 1"
                    ),
                    {"uid": user_id},
                ).fetchone()
                is_super_admin = bool(row)
            except Exception as e:
                logger.warning("authz: super_admin probe failed: %s", e)

            # 2. Tenant memberships from tenant_users.
            tenant_rows = []
            try:
                tenant_rows = conn.execute(
                    text("SELECT tenant::text " "FROM tenant_users " 'WHERE "user" = :uid'),
                    {"uid": user_id},
                ).fetchall()
            except Exception as e:
                logger.warning("authz: tenant_users lookup failed: %s", e)

            # 3. Per-tenant role + account access.
            for tr in tenant_rows:
                tid = str(tr[0]) if tr[0] else ""
                if not tid:
                    continue
                role = ROLE_USER
                account_ids: list[str] = []

                # 3a. Is the user tenant_admin in THIS tenant?
                try:
                    admin_row = conn.execute(
                        text(
                            "SELECT 1 FROM user_roles "
                            "WHERE user_id = :uid "
                            "  AND entity_type = 'tenant' "
                            "  AND role = 'tenant_admin' "
                            "  AND entity_id::text = :tid "
                            "LIMIT 1"
                        ),
                        {"uid": user_id, "tid": tid},
                    ).fetchone()
                    if admin_row:
                        role = ROLE_TENANT_ADMIN
                except Exception as e:
                    logger.warning(
                        "authz: tenant_admin probe failed for tenant %s: %s",
                        tid,
                        e,
                    )

                # 3b. Account access for this tenant.
                try:
                    if role == ROLE_TENANT_ADMIN:
                        # tenant_admin sees every active account in their tenant.
                        rows = conn.execute(
                            text(
                                "SELECT id::text "
                                "FROM cloud_accounts "
                                "WHERE tenant::text = :tid "
                                "  AND status = 'active' "
                                "ORDER BY account_name"
                            ),
                            {"tid": tid},
                        ).fetchall()
                    else:
                        # Regular user — explicit RBAC: direct user_roles
                        # entries with entity_type='account' OR group-based
                        # entries via user_groups + usergroup_users +
                        # group_roles. Mirrors api-server's authz query.
                        rows = conn.execute(
                            text(
                                "SELECT entity_id::text "
                                "FROM user_roles "
                                "WHERE tenant_id::text = :tid "
                                "  AND user_id = :uid "
                                "  AND entity_type = 'account' "
                                "  AND role IN ('account_admin', 'account_read_admin') "
                                "UNION "
                                "SELECT gr.entity_id::text "
                                "FROM group_roles gr "
                                "JOIN user_groups ug ON ug.id = gr.group_id "
                                'JOIN usergroup_users ugg ON ugg."group" = ug.id '
                                "WHERE ug.tenant::text = :tid "
                                '  AND ugg."user" = :uid '
                                "  AND gr.entity_type = 'account' "
                                "  AND gr.role IN ('account_admin', 'account_read_admin')"
                            ),
                            {"tid": tid, "uid": user_id},
                        ).fetchall()
                    account_ids = [str(r[0]) for r in rows if r[0]]
                except Exception as e:
                    logger.warning("authz: account fetch failed for tenant %s: %s", tid, e)

                tenants.append(
                    TenantAccess(
                        tenant_id=tid,
                        role=role,
                        account_ids=account_ids,
                    )
                )
    except Exception:
        logger.exception("authz: failed to resolve user %s", user_id)
        return Authz(user_id=user_id)

    logger.info(
        "authz: resolved user=%s super_admin=%s tenants=%d",
        user_id,
        is_super_admin,
        len(tenants),
    )
    return Authz(
        user_id=user_id,
        is_super_admin=is_super_admin,
        tenants=tenants,
    )


def list_all_active_tenants() -> list[dict]:
    """Return all active tenants (for the super-admin tenant dropdown).

    Called only by ``/dashboard/tenants`` when the caller is super_admin.
    Results aren't cached — the tenant list is small and rarely fetched.
    """
    if not db_engine:
        return []
    try:
        with db_engine.connect() as conn:
            rows = conn.execute(
                text(
                    "SELECT DISTINCT t.id::text, t.name "
                    "FROM tenant t "
                    "JOIN tenant_users tu ON tu.tenant = t.id "
                    'JOIN users u ON u.id = tu."user" '
                    "WHERE u.status = 'active' "
                    "ORDER BY t.name"
                )
            ).fetchall()
            return [{"id": str(r[0]), "name": str(r[1])} for r in rows]
    except Exception as e:
        logger.error("authz: list_all_active_tenants failed: %s", e)
        return []
