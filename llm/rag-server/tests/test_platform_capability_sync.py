"""Covers the request/response and DB-write shape of platform_capability_sync.
The real workflow-server call is verified live instead — see the module
docstring and this PR's description.
"""

from contextlib import contextmanager
from types import SimpleNamespace
from unittest.mock import MagicMock

import httpx
import pytest

from rag.core.documents import platform_capability_sync as sync
from utils.config import Config


class _FakeConnection:
    def __init__(self):
        self.executed = []

    def execute(self, query, params=None):
        sql = str(query)
        self.executed.append((sql, params))
        if "SELECT id FROM cloud_accounts" in sql:
            return SimpleNamespace(first=lambda: ("acct-1",))
        return None


def _fake_engine(conn):
    @contextmanager
    def connect():
        yield conn

    @contextmanager
    def begin():
        yield conn

    return SimpleNamespace(connect=connect, begin=begin)


@pytest.fixture(autouse=True)
def _reset_llm_server_url(monkeypatch):
    monkeypatch.setattr(Config, "llm_server_url", "http://llm-server:8005")


def test_noop_when_llm_server_url_unset(monkeypatch):
    monkeypatch.setattr(Config, "llm_server_url", "")
    monkeypatch.setattr(httpx, "post", MagicMock(side_effect=AssertionError("must not call out")))
    sync.sync_platform_capability_catalog()  # must not raise


def test_noop_when_no_active_account(monkeypatch):
    conn = _FakeConnection()
    monkeypatch.setattr(sync, "engine", _fake_engine(conn))
    monkeypatch.setattr(httpx, "post", MagicMock(side_effect=AssertionError("must not call out")))
    conn.execute = lambda query, params=None: SimpleNamespace(first=lambda: None)
    sync.sync_platform_capability_catalog()  # must not raise


def test_empty_catalog_is_not_treated_as_wipe_everything(monkeypatch):
    conn = _FakeConnection()
    monkeypatch.setattr(sync, "engine", _fake_engine(conn))
    fake_resp = MagicMock()
    fake_resp.raise_for_status.return_value = None
    fake_resp.json.return_value = {"capabilities": []}
    monkeypatch.setattr(httpx, "post", MagicMock(return_value=fake_resp))

    sync.sync_platform_capability_catalog()

    # Only the account lookup ran — no DELETE, no INSERT — same guard
    # capability_catalog.go's SyncCapabilityCatalog has for a degenerate run.
    assert not any("DELETE" in sql or "INSERT" in sql for sql, _ in conn.executed)


def test_synced_entries_are_upserted_then_stale_ones_deleted(monkeypatch):
    conn = _FakeConnection()
    monkeypatch.setattr(sync, "engine", _fake_engine(conn))
    fake_resp = MagicMock()
    fake_resp.raise_for_status.return_value = None
    fake_resp.json.return_value = {
        "capabilities": [
            {"name": "kubectl_execute", "surface": "agent_tool", "scope": "pass_through", "description": "run kubectl"},
        ]
    }
    monkeypatch.setattr(httpx, "post", MagicMock(return_value=fake_resp))

    sync.sync_platform_capability_catalog()

    inserts = [(sql, p) for sql, p in conn.executed if "INSERT INTO llm_kb_capability_entries" in sql]
    deletes = [p for sql, p in conn.executed if "DELETE FROM llm_kb_capability_entries" in sql]
    assert len(inserts) == 1
    insert_sql, insert_params = inserts[0]
    assert "VALUES ('platform_capability', :name0" in insert_sql
    assert insert_params["name0"] == "kubectl_execute"
    assert len(deletes) == 1
    assert deletes[0]["names"] == ["kubectl_execute"]


def test_http_failure_is_caught_not_raised(monkeypatch):
    conn = _FakeConnection()
    monkeypatch.setattr(sync, "engine", _fake_engine(conn))
    monkeypatch.setattr(httpx, "post", MagicMock(side_effect=httpx.ConnectError("refused")))
    sync.sync_platform_capability_catalog()  # must not raise
    assert not any("INSERT" in sql for sql, _ in conn.executed)
