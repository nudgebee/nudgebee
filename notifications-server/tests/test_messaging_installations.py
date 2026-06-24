"""Unit tests for the pure helpers of the messaging installation adapter
(channel-shape rebuild from scalar config, datetime parsing, installation build)."""

from datetime import datetime
from types import SimpleNamespace

from notifications_server.models.models import Integration
from notifications_server.services import messaging_installations as mi


def test_default_channel_shape_slack():
    assert mi._default_channel_shape("slack", {"default_channel_id": "C1"}) == [{"id": "C1"}]
    assert mi._default_channel_shape("slack", {}) is None


def test_default_channel_shape_teams():
    assert mi._default_channel_shape("ms_teams", {"default_team_id": "T1", "default_channel_id": "C1"}) == {
        "team_id": "T1",
        "channels": [{"id": "C1"}],
    }
    # Teams needs the compound (team_id, channel_id); missing either -> no default.
    assert mi._default_channel_shape("ms_teams", {"default_team_id": "T1"}) is None


def test_parse_dt():
    assert mi._parse_dt(None) is None
    assert mi._parse_dt("garbage") is None
    assert mi._parse_dt("2026-06-05T10:00:00") == datetime(2026, 6, 5, 10, 0, 0)


def test_decrypt_config_plaintext_only():
    rows = [
        SimpleNamespace(name="team_name", value="Eng", is_encrypted=False),
        SimpleNamespace(name="empty", value=None, is_encrypted=False),
    ]
    assert mi._decrypt_config(rows) == {"team_name": "Eng", "empty": ""}


def test_build_installation_slack():
    integ = Integration(id="11111111-1111-1111-1111-111111111111", name="T123", tenant_id="t1")
    cfg = {"bot_token": "xoxb-1", "team_name": "Eng", "default_channel_id": "C1", "bot_id": "B1"}
    inst = mi.build_installation("slack", integ, cfg)
    assert inst.platform == "slack"
    assert inst.team_id == "T123"
    assert inst.token == "xoxb-1"
    assert inst.channels == [{"id": "C1"}]
    assert inst._origin == mi.ORIGIN_INTEGRATION
    assert inst._integration_id == integ.id


def test_build_installation_teams():
    integ = Integration(id="22222222-2222-2222-2222-222222222222", name="home-acct", tenant_id="t1")
    cfg = {
        "access_token": "graph-tok",
        "refresh_token": "r1",
        "default_team_id": "T1",
        "default_channel_id": "C9",
    }
    inst = mi.build_installation("ms_teams", integ, cfg)
    assert inst.token == "graph-tok"
    assert inst.refresh_token == "r1"
    assert inst.channels == {"team_id": "T1", "channels": [{"id": "C9"}]}
