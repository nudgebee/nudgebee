from notifications_server.configs.settings import settings
from notifications_server.services import channel_config


def test_no_overrides_uses_process_defaults():
    resolved = channel_config.resolve(None)
    assert resolved.lookback_minutes == settings.notifications.channel_lookback_minutes
    assert resolved.max_context_messages == settings.notifications.channel_max_context_messages


def test_per_channel_override_wins():
    assert channel_config.resolve({"lookback_minutes": 120}).lookback_minutes == 120


def test_unlisted_keys_are_ignored_not_trusted():
    resolved = channel_config.resolve({"lookback_minutes": 120, "delete_everything": True})
    assert resolved.lookback_minutes == 120
    assert not hasattr(resolved, "delete_everything")


def test_a_malformed_value_falls_back_instead_of_raising():
    resolved = channel_config.resolve({"lookback_minutes": "not a number"})
    assert resolved.lookback_minutes == settings.notifications.channel_lookback_minutes


def test_a_boolean_is_not_silently_read_as_a_number():
    resolved = channel_config.resolve({"max_context_messages": True})
    assert resolved.max_context_messages == settings.notifications.channel_max_context_messages


def test_a_window_of_zero_is_clamped_rather_than_retrieving_nothing():
    assert channel_config.resolve({"lookback_minutes": 0}).lookback_minutes == 1
    assert channel_config.resolve({"max_context_messages": -5}).max_context_messages == 1


def test_zero_weights_are_allowed_because_they_mean_ignore_this_signal():
    assert channel_config.resolve({"rank_weight_salience": 0}).rank_weight_salience == 0.0


def test_search_may_be_switched_off_per_channel():
    assert channel_config.resolve({"search_limit": 0}).search_limit == 0


def test_a_non_dict_settings_blob_is_tolerated():
    assert channel_config.resolve("garbage").lookback_minutes == settings.notifications.channel_lookback_minutes


def test_numeric_strings_are_accepted():
    assert channel_config.resolve({"lookback_minutes": "45"}).lookback_minutes == 45
