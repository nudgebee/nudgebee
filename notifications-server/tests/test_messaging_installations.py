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


def test_the_default_channels_name_is_kept_beside_the_shape_routing_compares():
    from notifications_server.models.models import MessagingPlatform

    integ = Integration(id="44444444-4444-4444-4444-444444444444", name="guild-1", tenant_id="t1")
    inst = mi.build_installation("discord", integ, {"default_channel_id": "C1", "default_channel_name": "alerts"})

    assert inst.channels == {"id": "C1"}
    assert inst.default_channel_name == "alerts"
    assert MessagingPlatform.from_dict(inst.to_dict()).default_channel_name == "alerts"


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


def test_build_webhook_installation():
    integ = Integration(id="44444444-4444-4444-4444-444444444444", name="Prod alerts", tenant_id="t1")
    inst = mi.build_webhook_installation(
        integ, {"webhook_url": "https://x.logic.azure.com/hook", "installed_by": "a@b.c"}
    )
    assert inst.platform == mi.MS_TEAMS_WEBHOOK_TYPE
    assert inst.id == integ.id
    assert inst.team_name == "Prod alerts"  # label surfaced in rule pickers
    assert inst.webhook_url == "https://x.logic.azure.com/hook"
    assert inst.channels is None
    assert inst._origin == mi.ORIGIN_INTEGRATION


def test_build_webhook_installation_is_reusable_for_other_platforms():
    """The builder is parametrized so a future webhook platform reuses it rather
    than copying — it reads the URL from the field it is told to."""
    integ = Integration(id="66666666-6666-6666-6666-666666666666", name="Ops", tenant_id="t1")
    inst = mi.build_webhook_installation(
        integ, {"slack_url": "https://hooks.slack.com/services/x"}, platform="slack_webhook", url_field="slack_url"
    )
    assert inst.platform == "slack_webhook"
    assert inst.webhook_url == "https://hooks.slack.com/services/x"


def test_webhook_platform_types_membership():
    assert mi.MS_TEAMS_WEBHOOK_TYPE in mi.WEBHOOK_PLATFORM_TYPES
    assert "ms_teams" not in mi.WEBHOOK_PLATFORM_TYPES


def test_webhook_url_survives_cache_roundtrip():
    """The decrypted URL is the whole destination for a webhook target, so it must
    survive the installations cache round-trip (to_dict -> from_dict)."""
    from notifications_server.models.models import MessagingPlatform

    integ = Integration(id="55555555-5555-5555-5555-555555555555", name="Prod", tenant_id="t1")
    inst = mi.build_webhook_installation(integ, {"webhook_url": "https://x.logic.azure.com/hook"})

    restored = MessagingPlatform.from_dict(inst.to_dict())

    assert restored.platform == mi.MS_TEAMS_WEBHOOK_TYPE
    assert restored.webhook_url == "https://x.logic.azure.com/hook"


def test_cache_roundtrip_preserves_origin_and_integration_id():
    """A refreshed token is re-encrypted into integrations only when _origin and
    _integration_id survive the Redis cache round-trip (to_dict -> from_dict);
    otherwise persist falls back to the legacy plaintext path."""
    from notifications_server.models.models import MessagingPlatform

    integ = Integration(id="33333333-3333-3333-3333-333333333333", name="home-acct", tenant_id="t1")
    inst = mi.build_installation("ms_teams", integ, {"access_token": "graph-tok"})

    restored = MessagingPlatform.from_dict(inst.to_dict())

    assert restored._origin == mi.ORIGIN_INTEGRATION
    assert str(restored._integration_id) == "33333333-3333-3333-3333-333333333333"


def test_cache_roundtrip_legacy_origin():
    from notifications_server.models.models import MessagingPlatform

    inst = MessagingPlatform()
    inst.platform = "slack"
    inst.tenant_id = "t1"
    inst._origin = mi.ORIGIN_LEGACY

    restored = MessagingPlatform.from_dict(inst.to_dict())

    assert restored._origin == mi.ORIGIN_LEGACY
    assert restored._integration_id is None
