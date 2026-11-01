"""Populates entry_type='platform_capability' rows in llm_kb_capability_entries.

llm-server's own startup sync has no account_id, so its workflow-server call
always 401s and the catalog stays empty. rag-server always has a real
account_id at classification time, so it fetches the catalog via POST
/v1/capabilities/catalog instead. Best-effort: failures are logged, never
raised — never blocks startup.
"""

import logging
from typing import Any, Dict, List

import httpx
from sqlalchemy import text

from rag.core.utils.db_query import engine
from utils.config import Config

logger = logging.getLogger(__name__)

_TIMEOUT_SECONDS = 30.0


def _any_active_account_id() -> str | None:
    with engine.connect() as connection:
        row = connection.execute(text("SELECT id FROM cloud_accounts WHERE status = 'active' LIMIT 1")).first()
    return str(row[0]) if row else None


def _fetch_catalog(account_id: str) -> List[Dict[str, Any]]:
    url = Config.llm_server_url.rstrip("/") + "/v1/capabilities/catalog"
    resp = httpx.post(url, json={"account_id": account_id}, timeout=_TIMEOUT_SECONDS)
    resp.raise_for_status()
    body: Dict[str, Any] = resp.json()
    capabilities: List[Dict[str, Any]] = body.get("capabilities", [])
    return capabilities


def _upsert(entries: List[Dict[str, Any]]) -> None:
    if not entries:
        return
    with engine.begin() as connection:
        # One real round trip via a single multi-row VALUES statement.
        # Passing a list of dicts to execute() (SQLAlchemy's "executemany")
        # does NOT batch under psycopg2 — it still sends one INSERT per row,
        # which was a ~2.5min bottleneck at 277 entries in live testing.
        values_sql = ", ".join(
            f"('platform_capability', :name{i}, :surface{i}, :scope{i}, :description{i}, NOW())"
            for i in range(len(entries))
        )
        params: Dict[str, Any] = {}
        for i, entry in enumerate(entries):
            params[f"name{i}"] = entry.get("name")
            params[f"surface{i}"] = entry.get("surface")
            params[f"scope{i}"] = entry.get("scope")
            params[f"description{i}"] = entry.get("description", "")
        connection.execute(
            text(f"""
                INSERT INTO llm_kb_capability_entries (entry_type, name, surface, scope, description, updated_at)
                VALUES {values_sql}
                ON CONFLICT (name) WHERE entry_type = 'platform_capability' DO UPDATE
                SET surface = EXCLUDED.surface, scope = EXCLUDED.scope,
                    description = EXCLUDED.description, updated_at = NOW()
                """),
            params,
        )
        # Mirrors capability_catalog.go: removes stale tools/tasks so they
        # stop grounding new capability names. Scoped to platform_capability
        # only — never touches kb_collection or kb_document_mark rows.
        known_names = [e.get("name") for e in entries if e.get("name")]
        connection.execute(
            text(
                "DELETE FROM llm_kb_capability_entries "
                "WHERE entry_type = 'platform_capability' AND NOT (name = ANY(:names))"
            ),
            {"names": known_names},
        )


def sync_platform_capability_catalog() -> None:
    """Best-effort, called once at startup (see server.py). No-op if
    LLM_SERVER_URL is unset or no active account exists yet."""
    if not Config.llm_server_url:
        logger.info("platform_capability_sync: LLM_SERVER_URL not set, skipping")
        return
    try:
        account_id = _any_active_account_id()
        if not account_id:
            logger.info("platform_capability_sync: no active account found yet, skipping")
            return
        entries = _fetch_catalog(account_id)
        if not entries:
            logger.warning("platform_capability_sync: llm-server returned an empty catalog, not touching existing rows")
            return
        _upsert(entries)
        logger.info("platform_capability_sync: synced %d platform_capability entries", len(entries))
    except Exception:
        logger.exception("platform_capability_sync: failed, leaving existing platform_capability rows as-is")
