import logging
import time
import requests
from typing import List, Dict, Any, Optional

LOG = logging.getLogger(__name__)


class DiscordClient:
    BASE_URL = "https://discord.com/api/v10"

    # Guild enumeration is capped so a bot in many servers can't fan out into an
    # unbounded number of sequential API calls; /users/@me/guilds returns <=200/page.
    MAX_GUILDS = 200
    GUILD_PAGE_SIZE = 200
    # Bounded retry for a single rate-limited (HTTP 429) request.
    MAX_RATE_LIMIT_RETRIES = 2
    MAX_RETRY_SLEEP_SECONDS = 5.0
    REQUEST_TIMEOUT_SECONDS = 10

    # View Channels (1024) + Send Messages (2048) — the minimal permission set the
    # notification bot needs; baked into the server-invite URL.
    INVITE_PERMISSIONS = 3072

    # Discord JSON error codes worth translating for users (permission problems).
    _PERMISSION_ERROR_HINTS = {
        50001: "Missing Access — the bot cannot see this channel/server; invite it or grant View Channels",
        50013: "Missing Permissions — grant the bot Send Messages in this channel",
    }

    @classmethod
    def invite_url(cls, client_id: str) -> str:
        """Server-invite (OAuth2 authorize) URL for adding the bot to a guild."""
        return (
            f"https://discord.com/oauth2/authorize?client_id={client_id}&scope=bot&permissions={cls.INVITE_PERMISSIONS}"
        )

    @classmethod
    def _error_message(cls, resp) -> str:
        """Human-readable error from a Discord error response, with permission
        errors translated into an actionable hint."""
        try:
            body = resp.json()
            code = body.get("code")
            hint = cls._PERMISSION_ERROR_HINTS.get(code)
            if hint:
                return f"{hint} (Discord error {code})"
            message = body.get("message")
            if message:
                return f"{message} (HTTP {resp.status_code}, Discord error {code})"
        except Exception:
            pass
        return f"HTTP {resp.status_code}: {resp.text[:300]}"

    @classmethod
    def get_headers(cls, token: str) -> Dict[str, str]:
        if not token.startswith("Bot "):
            token = f"Bot {token}"
        return {
            "Authorization": token,
            "Content-Type": "application/json",
            "User-Agent": "NudgeBee (https://nudgebee.com, 1.0.0)",
        }

    @classmethod
    def _retry_after_seconds(cls, resp) -> Optional[float]:
        """Discord's rate-limit delay, from the JSON body or the Retry-After header."""
        try:
            body = resp.json()
            if isinstance(body, dict) and body.get("retry_after") is not None:
                return float(body["retry_after"])
        except Exception:
            pass
        header = resp.headers.get("Retry-After")
        try:
            return float(header) if header else None
        except ValueError:
            return None

    @classmethod
    def _get(cls, url: str, headers: Dict[str, str], params: Optional[Dict[str, Any]] = None):
        """GET with bounded retry on HTTP 429 (respecting Retry-After). Returns the
        final Response; may raise on network errors like the underlying requests call."""
        resp = None
        for attempt in range(cls.MAX_RATE_LIMIT_RETRIES + 1):
            resp = requests.get(url, headers=headers, params=params, timeout=cls.REQUEST_TIMEOUT_SECONDS)
            if resp.status_code != 429:
                return resp
            retry_after = cls._retry_after_seconds(resp)
            if attempt >= cls.MAX_RATE_LIMIT_RETRIES or retry_after is None:
                return resp
            time.sleep(max(0.0, min(retry_after, cls.MAX_RETRY_SLEEP_SECONDS)))
        return resp

    @classmethod
    def _list_guilds(cls, headers: Dict[str, str]) -> List[Dict[str, Any]]:
        """Up to MAX_GUILDS guilds, paginating /users/@me/guilds. Raises on failure."""
        guilds: List[Dict[str, Any]] = []
        after = None
        while len(guilds) < cls.MAX_GUILDS:
            params: Dict[str, Any] = {"limit": cls.GUILD_PAGE_SIZE}
            if after:
                params["after"] = after
            resp = cls._get(f"{cls.BASE_URL}/users/@me/guilds", headers, params=params)
            if resp.status_code != 200:
                raise RuntimeError(resp.text)
            page = resp.json()
            if not page:
                break
            guilds.extend(page)
            if len(page) < cls.GUILD_PAGE_SIZE:
                break
            after = page[-1]["id"]
        return guilds[: cls.MAX_GUILDS]

    @classmethod
    def _guild_text_channels(cls, headers: Dict[str, str], guild: Dict[str, Any]) -> List[Dict[str, Any]]:
        """Text/announcement channels for one guild; [] on any failure so a single
        bad guild (network error, non-JSON body, malformed channel) is skipped rather
        than aborting the whole listing."""
        guild_id = guild.get("id")
        if not guild_id:
            return []
        guild_name = guild.get("name", "")
        try:
            resp = cls._get(f"{cls.BASE_URL}/guilds/{guild_id}/channels", headers)
            if resp.status_code != 200:
                LOG.warning("Failed to fetch channels for Discord guild %s: %s", guild_id, resp.text)
                return []
            channels = resp.json()
            if not isinstance(channels, list):
                LOG.warning("Unexpected channels payload for Discord guild %s: %s", guild_id, type(channels))
                return []
            # Type 0 is GUILD_TEXT, Type 5 is GUILD_ANNOUNCEMENT
            return [
                {"id": c["id"], "name": f"{guild_name} / {c['name']}"}
                for c in channels
                if isinstance(c, dict) and c.get("type") in (0, 5) and "id" in c and "name" in c
            ]
        except Exception:
            LOG.exception("Error fetching/parsing channels for Discord guild %s", guild_id)
            return []

    @classmethod
    def channels_list(cls, token: str, **kwargs) -> Dict[str, Any]:
        """
        List all text channels the bot has access to across all guilds. Guilds are
        paginated and capped at MAX_GUILDS; channels are fetched one guild at a time
        with bounded rate-limit retry, and a single guild's failure is skipped rather
        than aborting the whole listing. Returns a structure similar to Slack's
        conversations.list: {'ok': True, 'channels': [{'id': '123', 'name': 'general'}]}
        """
        headers = cls.get_headers(token)
        try:
            guilds = cls._list_guilds(headers)
        except Exception as e:
            LOG.exception("Failed to fetch Discord guilds")
            return {"ok": False, "error": str(e)}

        if len(guilds) >= cls.MAX_GUILDS:
            LOG.warning(
                "Discord bot is in >= %d guilds; listing channels for the first %d only",
                cls.MAX_GUILDS,
                cls.MAX_GUILDS,
            )

        channels: List[Dict[str, Any]] = []
        for guild in guilds:
            channels.extend(cls._guild_text_channels(headers, guild))
        return {"ok": True, "channels": channels, "guild_count": len(guilds)}

    @classmethod
    def guild_count(cls, token: str) -> Optional[int]:
        """Number of guilds the bot is in, or None when the lookup fails."""
        try:
            return len(cls._list_guilds(cls.get_headers(token)))
        except Exception:
            LOG.exception("Failed to count Discord guilds")
            return None

    @classmethod
    def _post_message(cls, token: str, channel_id: str, payload: Dict[str, Any]) -> Dict[str, Any]:
        """POST a message payload with embed-limit clamping and bounded retry on
        HTTP 429 (respecting Retry-After), so an oversized or rate-limited send
        cannot permanently wedge a redis-batched flush loop."""
        from notifications_server.message_templates.discord.embed_utils import clamp_embeds

        if payload.get("embeds"):
            payload["embeds"] = clamp_embeds(payload["embeds"])

        headers = cls.get_headers(token)
        url = f"{cls.BASE_URL}/channels/{channel_id}/messages"
        try:
            resp = None
            for attempt in range(cls.MAX_RATE_LIMIT_RETRIES + 1):
                resp = requests.post(url, headers=headers, json=payload, timeout=cls.REQUEST_TIMEOUT_SECONDS)
                if resp.status_code != 429:
                    break
                retry_after = cls._retry_after_seconds(resp)
                if attempt >= cls.MAX_RATE_LIMIT_RETRIES or retry_after is None:
                    break
                time.sleep(max(0.0, min(retry_after, cls.MAX_RETRY_SLEEP_SECONDS)))
            if resp is not None and resp.status_code in (200, 201):
                data = resp.json()
                # Return a dict containing 'data' to act similarly to Slack client responses
                return {"ok": True, "ts": data.get("id"), "data": data}
            error = cls._error_message(resp) if resp is not None else "no response"
            LOG.error("Failed to post to Discord channel %s: %s", channel_id, error)
            return {"ok": False, "error": error}
        except Exception as e:
            LOG.exception(f"Error posting message to Discord channel {channel_id}")
            return {"ok": False, "error": str(e)}

    @classmethod
    def _message_payload(cls, kwargs: Dict[str, Any]) -> Dict[str, Any]:
        payload: Dict[str, Any] = {}
        if "content" in kwargs:
            payload["content"] = kwargs["content"]
        if "embeds" in kwargs:
            payload["embeds"] = kwargs["embeds"]
        if "text" in kwargs and not payload.get("content"):
            payload["content"] = kwargs["text"]
        return payload

    @classmethod
    def chat_post(cls, *, token: str, channel_id: str, **kwargs) -> Dict[str, Any]:
        """
        Post a message to a Discord channel.
        kwargs can contain 'content' (str) and 'embeds' (list).
        Returns {'ok': True, 'ts': 'message_id'} or {'ok': False, 'error': ...}
        """
        return cls._post_message(token, channel_id, cls._message_payload(kwargs))

    @classmethod
    def reply_in_thread(cls, *, token: str, channel_id: str, thread_ts: str, **kwargs) -> Dict[str, Any]:
        """
        Reply to a message via message_reference
        """
        payload = cls._message_payload(kwargs)
        payload["message_reference"] = {"message_id": thread_ts, "fail_if_not_exists": False}
        return cls._post_message(token, channel_id, payload)

    @classmethod
    def validate_token(cls, token: str) -> Dict[str, Any]:
        """
        Validate a bot token by calling GET /users/@me.
        Returns {'ok': True, 'bot': {'id': ..., 'username': ...}} on success.
        """
        headers = cls.get_headers(token)
        try:
            resp = requests.get(f"{cls.BASE_URL}/users/@me", headers=headers, timeout=10)
            if resp.status_code == 200:
                data = resp.json()
                return {"ok": True, "bot": {"id": data.get("id"), "username": data.get("username")}}
            else:
                LOG.error("Discord token validation failed: %s", resp.text)
                return {"ok": False, "error": f"Invalid token (HTTP {resp.status_code})"}
        except Exception as e:
            LOG.exception("Error validating Discord token")
            return {"ok": False, "error": str(e)}

    @classmethod
    def users_list(cls, token: str, **kwargs) -> Dict[str, Any]:
        return {"ok": True, "members": []}
