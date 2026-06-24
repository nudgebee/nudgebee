"""
Unit tests for Events._resolve_gchat_disposition — the bound-vs-unbound decision
that drives which card (or conversation) an inbound Google Chat event produces.

The resolver is the single source of truth shared by the ADDED_TO_SPACE (join)
and MESSAGE paths, so these cases pin the behavior both paths rely on:

    no Nudgebee account            -> SIGN-UP
    space bound to a user's tenant -> SERVE (scoped to that tenant)
    account, but unbound / bound elsewhere -> CONNECT
"""

from types import SimpleNamespace

from notifications_server.services import events as events_mod
from notifications_server.services.events import (
    GCHAT_CONNECT,
    GCHAT_SERVE,
    GCHAT_SIGNUP,
    Events,
)


def _events():
    # _resolve_gchat_disposition only touches self.session.get_bind(); bypass
    # __init__ so the unit test needs no live engine.
    ev = Events.__new__(Events)
    ev.session = SimpleNamespace(get_bind=lambda: None)
    return ev


def _patch(monkeypatch, *, tenants, binding):
    user = ("u1", tenants) if tenants else (None, [])
    monkeypatch.setattr(events_mod, "validate_and_get_user_tenants", lambda email: user)
    monkeypatch.setattr(events_mod, "find_google_chat_binding", lambda session, space: binding)


def test_no_email_is_signup():
    assert _events()._resolve_gchat_disposition("spaces/S", None) == (GCHAT_SIGNUP, None, None)


def test_unknown_user_is_signup(monkeypatch):
    _patch(monkeypatch, tenants=[], binding=None)
    assert _events()._resolve_gchat_disposition("spaces/S", "nobody@x.com") == (GCHAT_SIGNUP, None, None)


def test_bound_to_user_tenant_is_serve(monkeypatch):
    _patch(monkeypatch, tenants=["t1", "t2"], binding=SimpleNamespace(tenant_id="t1"))
    kind, tenant, uid = _events()._resolve_gchat_disposition("spaces/S", "u@x.com")
    assert kind == GCHAT_SERVE
    assert tenant == "t1"
    assert uid == "u1"


def test_unbound_space_known_user_is_connect(monkeypatch):
    _patch(monkeypatch, tenants=["t1"], binding=None)
    assert _events()._resolve_gchat_disposition("spaces/S", "u@x.com") == (GCHAT_CONNECT, None, "u1")


def test_bound_to_other_tenant_is_connect(monkeypatch):
    # Space bound to a tenant the user isn't in: no SERVE, prompt to connect.
    _patch(monkeypatch, tenants=["t1", "t2"], binding=SimpleNamespace(tenant_id="t9"))
    assert _events()._resolve_gchat_disposition("spaces/S", "u@x.com") == (GCHAT_CONNECT, None, "u1")
