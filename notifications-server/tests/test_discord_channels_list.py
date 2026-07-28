"""Tests for DiscordClient.channels_list guild pagination, cap, 429 retry, and
per-guild failure isolation — the hardening that stops a many-guild bot from
fanning out into an unbounded number of blocking calls.
"""

from unittest.mock import patch

from notifications_server.clients import discord_client
from notifications_server.clients.discord_client import DiscordClient


class _Resp:
    def __init__(self, status_code=200, json_data=None, headers=None, text=""):
        self.status_code = status_code
        self._json = json_data if json_data is not None else []
        self.headers = headers or {}
        self.text = text

    def json(self):
        return self._json


def _guild(i):
    return {"id": str(i), "name": f"g{i}"}


def _channels_payload():
    return [
        {"id": "t", "name": "text", "type": 0},  # GUILD_TEXT
        {"id": "a", "name": "ann", "type": 5},  # GUILD_ANNOUNCEMENT
        {"id": "v", "name": "voice", "type": 2},  # excluded
    ]


def _make_get(guild_pages, channel_status=None):
    """Fake requests.get: the guilds endpoint returns scripted pages in order; the
    per-guild channels endpoint returns text/announcement channels (or an error
    status from channel_status[guild_id])."""
    guild_seq = list(guild_pages)
    channel_status = channel_status or {}

    def fake_get(url, headers=None, params=None, timeout=None):
        if url.endswith("/users/@me/guilds"):
            status, data, hdrs = guild_seq.pop(0)
            return _Resp(status_code=status, json_data=data, headers=hdrs)
        gid = url.split("/guilds/")[1].split("/channels")[0]
        st = channel_status.get(gid, 200)
        if st != 200:
            return _Resp(status_code=st, text="err")
        return _Resp(status_code=200, json_data=_channels_payload())

    return fake_get


def _guild_names(result):
    return {c["name"].split(" / ")[0] for c in result["channels"]}


def test_lists_text_channels_across_guilds():
    get = _make_get([(200, [_guild(1), _guild(2)], {})])
    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"]
    # voice channel excluded; text + announcement kept, prefixed by guild name
    assert {c["name"] for c in res["channels"]} == {
        "g1 / text",
        "g1 / ann",
        "g2 / text",
        "g2 / ann",
    }


def test_paginates_and_caps_guilds(monkeypatch):
    monkeypatch.setattr(DiscordClient, "MAX_GUILDS", 3)
    monkeypatch.setattr(DiscordClient, "GUILD_PAGE_SIZE", 2)
    # A full first page (== page size) triggers a second fetch; cap stops at 3.
    get = _make_get([(200, [_guild(1), _guild(2)], {}), (200, [_guild(3), _guild(4)], {})])
    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"]
    assert _guild_names(res) == {"g1", "g2", "g3"}  # g4 dropped by the cap


def test_retries_on_rate_limit(monkeypatch):
    sleeps = []
    monkeypatch.setattr(discord_client.time, "sleep", lambda s: sleeps.append(s))
    get = _make_get([(429, {"retry_after": 0.01}, {}), (200, [_guild(1)], {})])
    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"]
    assert sleeps == [0.01]  # honored Retry-After before retrying
    assert _guild_names(res) == {"g1"}


def test_skips_guild_whose_channel_fetch_fails():
    get = _make_get([(200, [_guild(1), _guild(2)], {})], channel_status={"1": 500})
    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"]
    assert _guild_names(res) == {"g2"}  # g1 failed and was skipped, not fatal


def test_guild_fetch_error_returns_not_ok():
    get = _make_get([(403, {}, {})])
    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"] is False
    assert "error" in res


def test_skips_guild_with_malformed_channel_payload():
    # g1's channels body is not a list; the parse must be isolated so g2 still lists.
    def get(url, headers=None, params=None, timeout=None):
        if url.endswith("/users/@me/guilds"):
            return _Resp(status_code=200, json_data=[_guild(1), _guild(2)])
        gid = url.split("/guilds/")[1].split("/channels")[0]
        return _Resp(status_code=200, json_data={"bad": "payload"} if gid == "1" else _channels_payload())

    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"]
    assert _guild_names(res) == {"g2"}


def test_negative_retry_after_is_clamped_not_fatal():
    # A malformed negative retry_after must not raise from time.sleep().
    get = _make_get([(429, {"retry_after": -5}, {}), (200, [_guild(1)], {})])
    with patch.object(discord_client.requests, "get", get):
        res = DiscordClient.channels_list("Bot x")
    assert res["ok"]
    assert _guild_names(res) == {"g1"}
