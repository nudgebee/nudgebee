from types import SimpleNamespace

from notifications_server.services import events as ev


class _NullSessionCtx:
    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


def _svc(app_id):
    service = ev.Events.__new__(ev.Events)
    service.session = SimpleNamespace(get_bind=lambda: None)
    service.common_service = SimpleNamespace(app_id=app_id)
    return service


def test_slack_bot_token_disambiguates_by_app_id(monkeypatch):
    """Regression: a team_id can have more than one Slack app installed (e.g. two
    sandbox apps in the same workspace). Resolving the bot token without app_id
    can silently return a different app's installation, whose token has no
    access to files shared in a conversation only the actual app is part of —
    downloads then fail with a 403 while everything else looks fine."""
    captured = {}

    def fake_load_installation_by_team(session, team_id, platform, app_id=None):
        captured["team_id"] = team_id
        captured["platform"] = platform
        captured["app_id"] = app_id
        return SimpleNamespace(token="tok-for-" + str(app_id))

    monkeypatch.setattr(ev, "load_installation_by_team", fake_load_installation_by_team)
    monkeypatch.setattr(ev, "Session", lambda bind: _NullSessionCtx())

    service = _svc(app_id="A_NUBI_LOCAL")
    token = service._slack_bot_token("T05JWTTH3NH")

    assert token == "tok-for-A_NUBI_LOCAL"
    assert captured == {"team_id": "T05JWTTH3NH", "platform": "slack", "app_id": "A_NUBI_LOCAL"}


def test_slack_bot_token_returns_none_when_installation_missing(monkeypatch):
    monkeypatch.setattr(ev, "load_installation_by_team", lambda *a, **k: None)
    monkeypatch.setattr(ev, "Session", lambda bind: _NullSessionCtx())

    service = _svc(app_id="A_NUBI_LOCAL")
    assert service._slack_bot_token("T05JWTTH3NH") is None


def test_slack_bot_token_swallows_errors(monkeypatch):
    def boom(*a, **k):
        raise RuntimeError("db unavailable")

    monkeypatch.setattr(ev, "load_installation_by_team", boom)
    monkeypatch.setattr(ev, "Session", lambda bind: _NullSessionCtx())

    service = _svc(app_id="A_NUBI_LOCAL")
    assert service._slack_bot_token("T05JWTTH3NH") is None
