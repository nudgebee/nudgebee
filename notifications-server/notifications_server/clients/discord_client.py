import logging
import requests
from typing import List, Dict, Any, Optional

LOG = logging.getLogger(__name__)


class DiscordClient:
    BASE_URL = "https://discord.com/api/v10"

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
    def channels_list(cls, token: str, **kwargs) -> Dict[str, Any]:
        """
        List all text channels the bot has access to across all guilds.
        Returns a structure similar to Slack's conversations.list:
        {'ok': True, 'channels': [{'id': '123', 'name': 'general'}]}
        """
        headers = cls.get_headers(token)

        try:
            # 1. Get guilds
            guilds_resp = requests.get(f"{cls.BASE_URL}/users/@me/guilds", headers=headers, timeout=10)
            if guilds_resp.status_code != 200:
                LOG.error(f"Failed to fetch Discord guilds: {guilds_resp.text}")
                return {"ok": False, "error": guilds_resp.text}

            guilds = guilds_resp.json()
            channels = []

            # 2. Get channels for each guild
            for guild in guilds:
                guild_id = guild["id"]
                guild_name = guild["name"]
                chan_resp = requests.get(f"{cls.BASE_URL}/guilds/{guild_id}/channels", headers=headers, timeout=10)
                if chan_resp.status_code == 200:
                    guild_channels = chan_resp.json()
                    for c in guild_channels:
                        # Type 0 is GUILD_TEXT, Type 5 is GUILD_ANNOUNCEMENT
                        if c.get("type") in (0, 5):
                            channels.append({"id": c["id"], "name": f"{guild_name} / {c['name']}"})

            return {"ok": True, "channels": channels}
        except Exception as e:
            LOG.exception("Error listing Discord channels")
            return {"ok": False, "error": str(e)}

    @classmethod
    def chat_post(cls, *, token: str, channel_id: str, **kwargs) -> Dict[str, Any]:
        """
        Post a message to a Discord channel.
        kwargs can contain 'content' (str) and 'embeds' (list).
        Returns {'ok': True, 'ts': 'message_id'} or {'ok': False, 'error': ...}
        """
        headers = cls.get_headers(token)

        payload = {}
        if "content" in kwargs:
            payload["content"] = kwargs["content"]
        if "embeds" in kwargs:
            payload["embeds"] = kwargs["embeds"]

        if "text" in kwargs and not payload.get("content"):
            payload["content"] = kwargs["text"]

        try:
            resp = requests.post(
                f"{cls.BASE_URL}/channels/{channel_id}/messages", headers=headers, json=payload, timeout=10
            )
            if resp.status_code in (200, 201):
                data = resp.json()
                # Return a dict containing 'data' to act similarly to Slack client responses
                return {"ok": True, "ts": data.get("id"), "data": data}
            else:
                LOG.error(f"Failed to post to Discord channel {channel_id}: {resp.text}")
                return {"ok": False, "error": resp.text}
        except Exception as e:
            LOG.exception(f"Error posting message to Discord channel {channel_id}")
            return {"ok": False, "error": str(e)}

    @classmethod
    def reply_in_thread(cls, *, token: str, channel_id: str, thread_ts: str, **kwargs) -> Dict[str, Any]:
        """
        Reply to a message via message_reference
        """
        kwargs["message_reference"] = {"message_id": thread_ts, "fail_if_not_exists": False}

        headers = cls.get_headers(token)
        payload = {}
        if "content" in kwargs:
            payload["content"] = kwargs["content"]
        if "embeds" in kwargs:
            payload["embeds"] = kwargs["embeds"]
        if "text" in kwargs and not payload.get("content"):
            payload["content"] = kwargs["text"]

        payload["message_reference"] = kwargs["message_reference"]

        try:
            resp = requests.post(
                f"{cls.BASE_URL}/channels/{channel_id}/messages", headers=headers, json=payload, timeout=10
            )
            if resp.status_code in (200, 201):
                data = resp.json()
                return {"ok": True, "ts": data.get("id"), "data": data}
            else:
                LOG.error(f"Failed to reply in Discord channel {channel_id}: {resp.text}")
                return {"ok": False, "error": resp.text}
        except Exception as e:
            LOG.exception(f"Error replying in Discord channel {channel_id}")
            return {"ok": False, "error": str(e)}

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
