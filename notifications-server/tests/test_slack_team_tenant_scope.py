"""
A caller-supplied Slack team_id (automation steps, the connection test) selects
among the calling tenant's own Slack installs and never resolves another
tenant's workspace.
"""

import pytest

from notifications_server.services import common
from notifications_server.services.common import CommonService


class _Install:
    def __init__(self, team_id, token):
        self.team_id = team_id
        self.token = token


_INSTALLS = {
    "tenant-a": [_Install("T_A1", "xoxb-a1"), _Install("T_A2", "xoxb-a2")],
    "tenant-b": [_Install("T_B", "xoxb-b")],
}


@pytest.fixture
def svc(monkeypatch):
    monkeypatch.setattr(common, "load_installations", lambda _s, tenant_id, _p: list(_INSTALLS.get(tenant_id, [])))
    monkeypatch.setattr(common, "load_installation", lambda _s, tenant_id, _p: (_INSTALLS.get(tenant_id) or [None])[0])

    # Same as the real lookup: matches team_id across every tenant.
    def _by_team(_s, team_id, _p, **_k):
        return next((i for installs in _INSTALLS.values() for i in installs if i.team_id == team_id), None)

    monkeypatch.setattr(common, "load_installation_by_team", _by_team)
    service = CommonService.__new__(CommonService)
    service.session = None
    return service


def test_own_workspace_resolves(svc):
    assert svc._get_messaging_platform("tenant-a", "T_A2", "slack").token == "xoxb-a2"


def test_other_tenants_workspace_is_refused(svc):
    assert svc._get_messaging_platform("tenant-a", "T_B", "slack") is None


def test_no_team_id_uses_tenant_install(svc):
    assert svc._get_messaging_platform("tenant-a", None, "slack").token == "xoxb-a1"


def test_tenant_without_slack_is_refused(svc):
    assert svc._get_messaging_platform("tenant-c", "T_B", "slack") is None


def test_cross_tenant_reaction_never_calls_slack(svc):
    calls = []

    class _Client:
        def reactions_add(self, **kwargs):
            calls.append(kwargs)
            return {"ok": True}

    svc.slack_app = type("App", (), {"client": _Client()})()
    result = svc.add_reaction("slack", "tenant-a", "C1", "1.0", "eyes", team_id="T_B")
    assert result["success"] is False
    assert calls == []
