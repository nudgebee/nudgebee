from slack_sdk.web import SlackResponse
from slack_sdk.web.client import WebClient
from typing import Any, Dict, Optional
import logging
import requests

LOG = logging.getLogger(__name__)

CONTENT_TYPE_JSON = "application/json"


class SlackClient(WebClient):
    def __init__(self, installation_store, **kwargs):
        self._installation_store = installation_store
        super().__init__(**kwargs)

    def chat_post(self, *, token, channel_id=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_postMessage(channel=channel_id, **kwargs)

    def post_ephemeral(self, *, token, channel_id=None, user=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_postEphemeral(channel=channel_id, user=user, **kwargs)

    def file_upload(self, *, token, title, fname, filename, **kwargs):
        client = WebClient(token=token)
        return client.files_upload_v2(title=title, file=fname, filename=filename, **kwargs)

    def reply_in_thread(self, *, token, channel_id=None, thread_ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_postMessage(channel=channel_id, thread_ts=thread_ts, **kwargs)

    def channels_list(self, token, team_id=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_list(
            team_id=team_id,
            exclude_archived=True,
            types="public_channel,private_channel",
            # Slack's documented max page size for conversations.list — fewer round
            # trips for a workspace with hundreds of channels (was 200).
            limit=1000,
            **kwargs,
        )

    def conversations_create(self, *, token, name, is_private=False, team_id=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_create(name=name, is_private=is_private, team_id=team_id, **kwargs)

    def views_open(self, *, token, channel_id=None, trigger_id=None, view=None, **kwargs):
        client = WebClient(token=token)
        return client.views_open(trigger_id=trigger_id, view=view, **kwargs)

    def users_info(self, *, token, user=None, **kwargs):
        client = WebClient(token=token)
        return client.users_info(user=user, **kwargs)

    def reactions_add(self, *, token, channel_id=None, thread_ts=None, emoji_name=None, **kwargs):
        client = WebClient(token=token)
        return client.reactions_add(channel=channel_id, timestamp=thread_ts, name=emoji_name, **kwargs)

    def conversations_replies(self, *, token, channel_id=None, thread_ts=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_replies(channel=channel_id, ts=thread_ts, **kwargs)

    def conversations_info(self, *, token, channel_id=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_info(channel=channel_id, **kwargs)

    def team_info(self, *, token, team_id=None, **kwargs):
        client = WebClient(token=token)
        return client.team_info(team=team_id, **kwargs)

    def conversations_join(self, *, token, channel_id=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_join(channel=channel_id, **kwargs)

    def conversations_invite(self, *, token, channel_id=None, users=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_invite(channel=channel_id, users=users, **kwargs)

    def reactions_get(self, *, token, channel_id=None, timestamp=None, **kwargs):
        client = WebClient(token=token)
        return client.reactions_get(channel=channel_id, timestamp=timestamp, full=True, **kwargs)

    def users_list(self, token, team_id=None, **kwargs):
        client = WebClient(token=token)
        return client.users_list(team_id=team_id, **kwargs)

    def conversations_open(self, *, token, users=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_open(users=users, **kwargs)

    def start_stream(self, *, token, channel_id=None, thread_ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_startStream(channel=channel_id, thread_ts=thread_ts, **kwargs)

    def append_stream(self, *, token, channel_id=None, ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_appendStream(channel=channel_id, ts=ts, **kwargs)

    def stop_stream(self, *, token, channel_id=None, ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_stopStream(channel=channel_id, ts=ts, **kwargs)

    def chat_update(self, *, token, channel_id=None, ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_update(channel=channel_id, ts=ts, **kwargs)

    def chat_delete(self, *, token, channel_id=None, ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_delete(channel=channel_id, ts=ts, **kwargs)

    @staticmethod
    def post_to_slack_webhook(
        webhook_url: str,
        payload: Dict[str, Any],
        tenant: Optional[str] = None,
        timeout: int = 15,
    ) -> Optional[requests.Response]:
        """Post a Block Kit message directly to a Slack Incoming Webhook. No
        token - the URL is the secret and is bound to one channel. Unlike
        chat.postMessage, Slack answers with a plain-text "ok" body, not JSON.
        Returns the raw response, or None on a transport failure so one dead
        webhook doesn't abort the fan-out. The caller decides success (2xx)."""
        try:
            # requests.post, not a shared Session: this runs concurrently across
            # threads via asyncio.to_thread, and a Session isn't thread-safe.
            response = requests.post(
                webhook_url,
                headers={"Content-Type": CONTENT_TYPE_JSON},
                json=payload,
                timeout=timeout,
            )
        except requests.exceptions.RequestException as exc:
            LOG.warning("Slack webhook connection failed for tenant %s: %s", tenant, exc)
            return None
        if response.status_code >= 400:
            LOG.warning(
                "Slack webhook delivery failed for tenant %s: %s - %s",
                tenant,
                response.status_code,
                response.text[:512],
            )
        else:
            LOG.debug("Slack webhook notification sent for tenant %s", tenant)
        return response
