"""
A rule save takes only the client-editable fields from its input. The tenant,
id and created/updated stamps are set by the server, so a body naming another
tenant can neither move a rule there nor create one there.
"""

import uuid

from notifications_server.models.models import NotificationRules
from notifications_server.services.rules import CLIENT_RULE_FIELDS
from tests.test_rule_account_scope import TENANT, _db_rule, _save, _service, _Session

OTHER_TENANT = "33333333-3333-3333-3333-333333333333"
SERVER_OWNED = {"id", "tenant_id", "created_at", "created_by", "updated_at", "updated_by"}
FORGED = {
    "tenant_id": OTHER_TENANT,
    "created_by": str(uuid.uuid4()),
    "created_at": "2020-01-01T00:00:00",
    "updated_by": str(uuid.uuid4()),
}


def test_every_rule_column_is_either_client_editable_or_server_owned():
    columns = {c.name for c in NotificationRules.__table__.columns}

    assert CLIENT_RULE_FIELDS | SERVER_OWNED == columns
    assert not CLIENT_RULE_FIELDS & SERVER_OWNED


def test_update_cannot_move_a_rule_to_another_tenant(monkeypatch):
    existing = _db_rule("all")
    creator = uuid.uuid4()
    existing.created_by = creator
    session = _Session(rules=[existing])
    service = _service(monkeypatch, session)

    result = _save(service, {"id": str(existing.id), "name": "Mute all", "source": "troubleshoot", **FORGED})

    assert "error" not in result, result
    assert existing.tenant_id == TENANT
    assert existing.created_by == creator
    assert existing.name == "Mute all"
    assert session.committed


def test_create_ignores_a_forged_tenant(monkeypatch):
    session = _Session()
    service = _service(monkeypatch, session)

    result = _save(service, {"name": "Mute all", "source": "troubleshoot", "scope_mode": "all", **FORGED})

    assert "error" not in result, result
    [rule] = [o for o in session.added if isinstance(o, NotificationRules)]
    assert rule.tenant_id == TENANT
    assert str(rule.id) == result["id"]
    assert rule.created_by != FORGED["created_by"]


def test_update_turns_a_rule_off_and_back_on(monkeypatch):
    existing = _db_rule("all")
    session = _Session(rules=[existing])
    service = _service(monkeypatch, session)
    body = {"id": str(existing.id), "name": "Prod alerts", "source": "troubleshoot"}

    assert "error" not in _save(service, {**body, "is_active": False})
    assert existing.is_active is False
    assert "error" not in _save(service, {**body, "is_active": True})
    assert existing.is_active is True


def test_create_defaults_to_active_and_honours_an_explicit_off(monkeypatch):
    session = _Session()
    service = _service(monkeypatch, session)

    _save(service, {"name": "On", "source": "troubleshoot", "scope_mode": "all"})
    _save(service, {"name": "Off", "source": "troubleshoot", "scope_mode": "all", "is_active": False})

    rules = {o.name: o.is_active for o in session.added if isinstance(o, NotificationRules)}
    assert rules == {"On": True, "Off": False}
