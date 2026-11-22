"""A rightsizing request for a cloud account that is not active writes nothing.

The rightsizing upserts move an archived row back to Open, so a run for a disabled
account re-opens the recommendations archived on it. The gate sits in the queue
handlers, which every caller (api-server, runbook-server) reaches.
"""

from dataclasses import dataclass
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

import pytest
from sqlalchemy import create_engine, text
from sqlalchemy.pool import StaticPool

from server.message import VerticalRightsizingRequest, VolumeRightsizingRequest
from server.utils.utils import DatabaseEngine, is_account_active


@pytest.fixture
def accounts():
    engine = create_engine("sqlite://", poolclass=StaticPool, connect_args={"check_same_thread": False})
    with engine.begin() as conn:
        conn.execute(text("CREATE TABLE cloud_accounts (id TEXT PRIMARY KEY, status TEXT)"))
        conn.execute(text("INSERT INTO cloud_accounts VALUES ('active-1', 'active'), ('disabled-1', 'disabled')"))
    with patch.object(DatabaseEngine, "_instance", engine):
        yield


@pytest.mark.parametrize(
    "account_id, expected",
    [("active-1", True), ("disabled-1", False), ("missing-1", False)],
)
def test_is_account_active(accounts, account_id, expected):
    assert is_account_active(account_id) is expected


def _vertical(account_id):
    return VerticalRightsizingRequest(account_id=account_id, tenant_id="tenant-1", persist_recommendation=True)


def _volume(account_id):
    return VolumeRightsizingRequest(account=account_id, tenant="tenant-1", persist_recommendation=True)


def test_vertical_request_for_disabled_account_does_not_run(accounts):
    with patch("server.message.generate_and_process_recommendation", new_callable=AsyncMock) as run:
        _vertical("disabled-1").process_message()
    run.assert_not_called()


def test_vertical_request_for_active_account_runs(accounts):
    with patch(
        "server.message.generate_and_process_recommendation",
        new_callable=AsyncMock,
        return_value=SimpleNamespace(recommendations_generated=0),
    ) as run:
        _vertical("active-1").process_message()
    run.assert_awaited_once()


@dataclass
class _VolumeResult:
    recommendations_generated: int = 0


def test_volume_request_for_disabled_account_does_not_run(accounts):
    with patch("server.message.generate_volume_rightsizing_recommendations", new_callable=AsyncMock) as run:
        _volume("disabled-1").process_message()
    run.assert_not_called()


def test_volume_request_for_active_account_runs(accounts):
    with patch(
        "server.message.generate_volume_rightsizing_recommendations",
        new_callable=AsyncMock,
        return_value=_VolumeResult(),
    ) as run:
        _volume("active-1").process_message()
    run.assert_awaited_once()
