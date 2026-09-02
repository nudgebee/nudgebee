"""Repeat findings must thread under the message previously posted to the SAME
channel. The lookup keys on the channel the send targets (rule-routed or
default) and scans that fingerprint's past deliveries for it, so rule-routed
channels thread exactly like the default channel — not a fresh top-level post
per repeat."""

import asyncio
import json

from notifications_server.models.models import SentNotifications
from notifications_server.services.message import SlackSender

_TEAM = "T123"
_FP = "fp-42"


class _Result:
    def __init__(self, rows):
        self._rows = list(rows)

    def scalars(self):
        return iter(self._rows)


class _Session:
    def __init__(self, rows):
        self._rows = rows

    async def execute(self, stmt):
        return _Result(self._rows)


def _row(channel, thread_ts, metadata=None):
    row = SentNotifications()
    row.fingerprint = _FP
    row.slack_team_id = _TEAM
    row.slack_thread_id = thread_ts
    row.slack_metadata = metadata if metadata is not None else json.dumps({"channel": channel})
    return row


def _lookup(rows, channels):
    return asyncio.run(SlackSender.check_if_sent_already(_Session(rows), _FP, _TEAM, channels))


class TestChannelAwareThreadLookup:
    def test_rule_channel_threads_even_when_latest_row_is_another_channel(self):
        rows = [_row("C_DEFAULT", "111.1"), _row("C_RULE", "222.2")]
        assert _lookup(rows, [{"id": "C_RULE"}]) == "222.2"

    def test_default_channel_still_threads(self):
        rows = [_row("C_DEFAULT", "111.1")]
        assert _lookup(rows, [{"id": "C_DEFAULT"}]) == "111.1"

    def test_unseen_channel_posts_top_level(self):
        rows = [_row("C_DEFAULT", "111.1")]
        assert _lookup(rows, [{"id": "C_RULE"}]) is None

    def test_json_string_channel_config(self):
        rows = [_row("C_RULE", "222.2")]
        assert _lookup(rows, json.dumps([{"id": "C_RULE"}])) == "222.2"

    def test_unparseable_metadata_is_skipped_not_fatal(self):
        rows = [_row("C_RULE", "111.1", metadata="{not json"), _row("C_RULE", "222.2")]
        assert _lookup(rows, [{"id": "C_RULE"}]) == "222.2"

    def test_missing_channel_config_returns_none(self):
        assert _lookup([], None) is None
