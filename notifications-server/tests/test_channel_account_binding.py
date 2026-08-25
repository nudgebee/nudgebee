"""
Tests for the permanent channel->account (+ investigation-conversation) binding
added on top of CommonService.join_channel.

join_channel keeps its original "add the bot to a channel" behaviour untouched;
these tests cover the new opt-in pieces:
  * _persist_channel_account_mapping — atomic INSERT ... ON CONFLICT DO UPDATE
    into notification_channel_account_mappings, with the caller's session_id
    (e.g. event.fingerprint) tucked into channel_metadata (a plain text
    column, not JSON/JSONB — hence json.dumps, not a raw dict).
  * join_channel's bind_account flag gating whether that upsert happens at all.
"""

import json

import pytest
from sqlalchemy.dialects import postgresql

from notifications_server.services import common
from notifications_server.services.common import CommonService


class _Install:
    def __init__(self, token="xoxb-test", team_id="T1"):
        self.token = token
        self.team_id = team_id


class _FakeSlackClient:
    def __init__(self):
        self.join_calls = []

    def conversations_join(self, *, token, channel_id, **kwargs):
        self.join_calls.append(channel_id)
        return {"ok": True}


class _FakeResult:
    """Stands in for the CursorResult session.execute(stmt) returns.

    rowcount mirrors what Postgres reports for `INSERT ... ON CONFLICT DO
    UPDATE ... WHERE`: 1 when the row was inserted/updated, 0 when the WHERE
    guard skipped the update because a conflicting row belongs to a different
    tenant.
    """

    def __init__(self, rowcount=1):
        self.rowcount = rowcount


class _FakeSession:
    """Stands in for the SQLAlchemy session used by _persist_channel_account_mapping.

    The real code now does a single atomic `INSERT ... ON CONFLICT DO UPDATE`
    (session.execute(stmt)) rather than select-then-insert/update, so there's
    no "existing row" branch to fake — every call just records the statement
    that would have been executed, which tests inspect via .compile().
    """

    def __init__(self, commit_error=None, rowcount=1):
        self.commit_error = commit_error
        self.rowcount = rowcount
        self.executed = []
        self.committed = False
        self.rolled_back = False

    def execute(self, stmt):
        self.executed.append(stmt)
        return _FakeResult(self.rowcount)

    def commit(self):
        if self.commit_error:
            raise self.commit_error
        self.committed = True

    def rollback(self):
        self.rolled_back = True


def _compiled_params(session):
    assert len(session.executed) == 1
    compiled = session.executed[0].compile(dialect=postgresql.dialect())
    return compiled, compiled.params


@pytest.fixture
def svc():
    service = CommonService.__new__(CommonService)
    service.slack_app = type("App", (), {"client": _FakeSlackClient()})()
    return service


def _allow_join(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    monkeypatch.setattr(
        common,
        "cache",
        type(
            "C",
            (),
            {
                "cache_event_entry": lambda *a, **k: None,
                "cache_channel_session_mapping": lambda *a, **k: None,
            },
        )(),
    )


# ------------------------- _persist_channel_account_mapping -------------------------


def test_persist_builds_upsert_with_session_id_in_metadata():
    svc = CommonService.__new__(CommonService)
    svc.session = _FakeSession()

    svc._persist_channel_account_mapping("slack", "T1", "C1", "acc-1", "tenant-1", "fingerprint-xyz")

    assert svc.session.committed is True
    compiled, params = _compiled_params(svc.session)
    assert params["channel_id"] == "C1"
    assert params["team_id"] == "T1"
    assert params["platform"] == "slack"
    assert params["account_id"] == "acc-1"
    assert params["tenant_id"] == "tenant-1"
    assert json.loads(params["channel_metadata"]) == {"system_managed": True, "session_id": "fingerprint-xyz"}
    assert "ON CONFLICT (platform, team_id, channel_id) DO UPDATE SET" in str(compiled)


def test_persist_without_session_id_marks_system_managed_only():
    svc = CommonService.__new__(CommonService)
    svc.session = _FakeSession()

    svc._persist_channel_account_mapping("slack", "T1", "C1", "acc-1", "tenant-1", bind_session_id=None)

    _, params = _compiled_params(svc.session)
    assert json.loads(params["channel_metadata"]) == {"system_managed": True}


def test_persist_upsert_carries_new_values_for_conflict_update():
    # The upsert is atomic — there's no Python-level "existing row" branch to
    # exercise directly. What matters is that the values that would land on a
    # conflicting row (the ON CONFLICT DO UPDATE SET clause) are the new ones.
    svc = CommonService.__new__(CommonService)
    svc.session = _FakeSession()

    svc._persist_channel_account_mapping("slack", "T1", "C1", "new-acc", "new-tenant", "fp-2")

    _, params = _compiled_params(svc.session)
    values = list(params.values())
    assert "new-acc" in values
    assert "new-tenant" in values
    assert any(
        isinstance(v, str) and v.startswith("{") and json.loads(v) == {"system_managed": True, "session_id": "fp-2"}
        for v in values
    )


def test_persist_scopes_conflict_update_to_owning_tenant():
    # Regression test: (platform, team_id, channel_id) is the unique constraint —
    # tenant_id is not part of it, and team_id is the Slack/Teams workspace id,
    # not a tenant id, so the same workspace can be connected to more than one
    # tenant. The DO UPDATE must only fire when the conflicting row already
    # belongs to this tenant, or a join under tenant B could silently repoint a
    # channel already mapped to tenant A.
    svc = CommonService.__new__(CommonService)
    svc.session = _FakeSession()

    svc._persist_channel_account_mapping("slack", "T1", "C1", "acc-1", "tenant-1", "fingerprint-xyz")

    compiled, params = _compiled_params(svc.session)
    sql = str(compiled)
    assert "DO UPDATE SET" in sql
    assert "WHERE" in sql.split("DO UPDATE SET", 1)[1]
    assert "tenant_id" not in sql.split("DO UPDATE SET", 1)[1].split("WHERE", 1)[0]
    assert "tenant-1" in params.values()


def test_persist_logs_warning_without_raising_when_conflict_owned_by_other_tenant(monkeypatch):
    # When the WHERE guard skips the update (rowcount == 0), the row already
    # belongs to a different tenant. Must not raise or attempt to adopt it —
    # just surface a warning so the "why didn't my bind_account work" case is
    # diagnosable.
    svc = CommonService.__new__(CommonService)
    svc.session = _FakeSession(rowcount=0)
    warnings = []
    monkeypatch.setattr(common.LOG, "warning", lambda msg: warnings.append(msg))

    svc._persist_channel_account_mapping("slack", "T1", "C1", "acc-1", "tenant-2", "fingerprint-xyz")

    assert svc.session.committed is True
    assert len(warnings) == 1
    assert "C1" in warnings[0]


def test_persist_swallows_errors_and_rolls_back():
    svc = CommonService.__new__(CommonService)
    svc.session = _FakeSession(commit_error=RuntimeError("db down"))

    # Must not raise — join_channel's Redis write already happened and should
    # still be usable even if this best-effort durability write fails.
    svc._persist_channel_account_mapping("slack", "T1", "C1", "acc-1", "tenant-1", "fp-3")

    assert svc.session.rolled_back is True


# ------------------------------- join_channel gating -------------------------------


def test_join_channel_bind_account_true_persists_mapping(monkeypatch, svc):
    _allow_join(monkeypatch, svc)
    svc.session = _FakeSession()

    result = svc.join_channel(
        platform="slack",
        account_id="acc-1",
        tenant_id="tenant-1",
        channel_id="C1",
        session_id="fp-abc",
        bind_session_id="fp-abc",
        bind_account=True,
    )

    assert result["success"] is True
    _, params = _compiled_params(svc.session)
    assert json.loads(params["channel_metadata"]) == {"system_managed": True, "session_id": "fp-abc"}


def test_join_channel_bind_account_true_without_bind_session_id_binds_account_only(monkeypatch, svc):
    # Regression test: bind_account=True with a plain session_id (e.g. the
    # generated-UUID fallback runbook-server's JoinChannel uses when the caller
    # didn't pass one) must still bind the channel to the account, but must NOT
    # durably bind future @mentions to that meaningless id as a conversation.
    _allow_join(monkeypatch, svc)
    svc.session = _FakeSession()

    result = svc.join_channel(
        platform="slack",
        account_id="acc-1",
        tenant_id="tenant-1",
        channel_id="C1",
        session_id="generated-uuid-not-a-real-conversation",
        bind_account=True,
    )

    assert result["success"] is True
    _, params = _compiled_params(svc.session)
    assert params["account_id"] == "acc-1"
    assert json.loads(params["channel_metadata"]) == {"system_managed": True}


def test_join_channel_bind_account_false_skips_persist(monkeypatch, svc):
    _allow_join(monkeypatch, svc)
    svc.session = _FakeSession()

    result = svc.join_channel(
        platform="slack",
        account_id="acc-1",
        tenant_id="tenant-1",
        channel_id="C1",
        session_id="fp-abc",
        # bind_account defaults to False — the plain "add the bot" case every
        # other existing caller of this task relies on.
    )

    assert result["success"] is True
    assert svc.session.executed == []
    assert svc.session.committed is False


def test_join_channel_without_session_id_skips_persist_even_if_bind_account_true(monkeypatch, svc):
    # session_id is required to build both Redis entries and the durable
    # mapping; without it there's nothing to key the binding on.
    _allow_join(monkeypatch, svc)
    svc.session = _FakeSession()

    result = svc.join_channel(
        platform="slack",
        account_id="acc-1",
        tenant_id="tenant-1",
        channel_id="C1",
        session_id=None,
        bind_account=True,
    )

    assert result["success"] is True
    assert svc.session.executed == []
