"""Regression tests for ``NotificationRulesService._save_mappings_data``.

The async migration of this service (commit c689dff8d5) converted
``_save_mappings_data`` to ``async def`` but failed to ``await``
``session.delete(...)``. As a result, removing a previously-configured
channel/email mapping silently failed -- the coroutine was never awaited and
the row was never deleted, so the UI kept showing the supposedly-removed
mapping after reload.

These tests pin the behaviour:
  * removing a platform from ``new_mappings`` deletes its row (regression)
  * adding a new platform inserts a new row
  * mutating channels for an existing platform updates the row in place
  * leaving channels unchanged is a no-op (no spurious updated_at bump)
"""

import asyncio
import sys
import types
import uuid
from datetime import datetime, timezone

import pytest
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, create_async_engine

# ---------------------------------------------------------------------------
# Bypass the heavy `notifications_server/__init__.py` (which imports msal,
# slack_bolt, and instantiates a Postgres engine on import). For these unit
# tests we only need the ORM models and the rules service module, so we stub
# the package root before the real submodules are imported.
# ---------------------------------------------------------------------------
_ROOT = "notifications_server"
if _ROOT not in sys.modules:
    sys.modules[_ROOT] = types.ModuleType(_ROOT)
    sys.modules[_ROOT].__path__ = [f"{__file__.rsplit('/', 2)[0]}/{_ROOT}"]  # noqa: SLF001

from notifications_server.models.models import (  # noqa: E402
    NotificationRules,
    NotificationRuleMappings,
)
from notifications_server.services.rules import NotificationRulesService  # noqa: E402


def _now():
    return datetime.now(timezone.utc).replace(tzinfo=None)


async def _build_engine():
    engine = create_async_engine("sqlite+aiosqlite:///:memory:")
    async with engine.begin() as conn:
        await conn.run_sync(lambda c: NotificationRules.__table__.create(c))
        await conn.run_sync(lambda c: NotificationRuleMappings.__table__.create(c))
    return engine


async def _seed_rule_with_mappings(engine, mappings):
    """Insert a rule plus the supplied mappings; return (rule_id, tenant_id, user_id)."""
    rule_id = uuid.uuid4()
    tenant_id = uuid.uuid4()
    user_id = uuid.uuid4()
    now = _now()
    async with AsyncSession(engine, expire_on_commit=False) as s:
        s.add(
            NotificationRules(
                id=rule_id,
                created_at=now,
                updated_at=now,
                created_by=user_id,
                updated_by=user_id,
                tenant_id=tenant_id,
                source="troubleshoot",
                name="T2 blocker",
            )
        )
        for platform, channels in mappings:
            s.add(
                NotificationRuleMappings(
                    id=uuid.uuid4(),
                    created_at=now,
                    updated_at=now,
                    created_by=user_id,
                    updated_by=user_id,
                    tenant_id=tenant_id,
                    rule_id=rule_id,
                    platform=platform,
                    channels=channels,
                )
            )
        await s.commit()
    return rule_id, tenant_id, user_id


async def _list_mappings(engine, rule_id):
    async with AsyncSession(engine, expire_on_commit=False) as s:
        result = await s.execute(select(NotificationRuleMappings).filter_by(rule_id=rule_id))
        return {m.platform: m.channels for m in result.scalars().all()}


async def _run_save(engine, rule_id, tenant_id, user_id, new_mappings):
    async with AsyncSession(engine, expire_on_commit=False) as s:
        await NotificationRulesService._save_mappings_data(  # noqa: SLF001
            s, rule_id, True, user_id, tenant_id, new_mappings
        )
        await s.commit()


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


def test_remove_platform_deletes_row():
    """Regression test: removing a configured channel must persist.

    Reproduces the bug where ``session.delete(...)`` was called without
    ``await`` on an AsyncSession, leaving the row in place.
    """

    async def run():
        engine = await _build_engine()
        rule_id, tenant_id, user_id = await _seed_rule_with_mappings(
            engine,
            [
                ("slack", {"name": "#alerts", "id": "C1"}),
                ("email", {"emails": ["a@x.com"], "exclusion_emails": []}),
            ],
        )

        # User toggles email off; only slack remains in the new payload.
        await _run_save(
            engine,
            rule_id,
            tenant_id,
            user_id,
            new_mappings=[
                {"platform": "slack", "channels": {"name": "#alerts", "id": "C1"}},
            ],
        )

        rows = await _list_mappings(engine, rule_id)
        assert "email" not in rows, f"email mapping was not removed: {rows}"
        assert set(rows.keys()) == {"slack"}

    asyncio.run(run())


def test_add_new_platform_inserts_row():
    async def run():
        engine = await _build_engine()
        rule_id, tenant_id, user_id = await _seed_rule_with_mappings(
            engine,
            [("slack", {"name": "#alerts", "id": "C1"})],
        )

        await _run_save(
            engine,
            rule_id,
            tenant_id,
            user_id,
            new_mappings=[
                {"platform": "slack", "channels": {"name": "#alerts", "id": "C1"}},
                {
                    "platform": "email",
                    "channels": {"emails": ["new@x.com"], "exclusion_emails": []},
                },
            ],
        )

        rows = await _list_mappings(engine, rule_id)
        assert set(rows.keys()) == {"slack", "email"}
        assert rows["email"] == {"emails": ["new@x.com"], "exclusion_emails": []}

    asyncio.run(run())


def test_update_existing_platform_changes_channels():
    async def run():
        engine = await _build_engine()
        rule_id, tenant_id, user_id = await _seed_rule_with_mappings(
            engine,
            [("slack", {"name": "#alerts", "id": "C1"})],
        )

        await _run_save(
            engine,
            rule_id,
            tenant_id,
            user_id,
            new_mappings=[
                {
                    "platform": "slack",
                    "channels": {"name": "#incidents", "id": "C2"},
                }
            ],
        )

        rows = await _list_mappings(engine, rule_id)
        assert rows == {"slack": {"name": "#incidents", "id": "C2"}}

    asyncio.run(run())


def test_unchanged_channels_is_noop():
    async def run():
        engine = await _build_engine()
        rule_id, tenant_id, user_id = await _seed_rule_with_mappings(
            engine,
            [("slack", {"name": "#alerts", "id": "C1"})],
        )

        async with AsyncSession(engine, expire_on_commit=False) as s:
            r = await s.execute(select(NotificationRuleMappings).filter_by(rule_id=rule_id))
            before = r.scalars().one().updated_at

        # Same payload — should not bump updated_at or change anything.
        await _run_save(
            engine,
            rule_id,
            tenant_id,
            user_id,
            new_mappings=[{"platform": "slack", "channels": {"name": "#alerts", "id": "C1"}}],
        )

        async with AsyncSession(engine, expire_on_commit=False) as s:
            r = await s.execute(select(NotificationRuleMappings).filter_by(rule_id=rule_id))
            after = r.scalars().one().updated_at

        assert before == after

    asyncio.run(run())


if __name__ == "__main__":  # pragma: no cover
    pytest.main([__file__, "-v"])
