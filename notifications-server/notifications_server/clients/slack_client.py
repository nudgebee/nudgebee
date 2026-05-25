from slack_sdk.web import SlackResponse
from slack_sdk.web.client import WebClient
import logging

LOG = logging.getLogger(__name__)


class SlackClient(WebClient):
    def __init__(self, installation_store, **kwargs):
        self._installation_store = installation_store
        super().__init__(**kwargs)

    def chat_post(self, *, token, channel_id=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_postMessage(channel=channel_id, **kwargs)

    def post_ephimeral(self, *, token, channel_id=None, user=None, **kwargs):
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
            limit=200,
            **kwargs,
        )

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

    def conversations_join(self, *, token, channel_id=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_join(channel=channel_id, **kwargs)

    def reactions_get(self, *, token, channel_id=None, timestamp=None, **kwargs):
        client = WebClient(token=token)
        return client.reactions_get(channel=channel_id, timestamp=timestamp, full=True, **kwargs)

    def users_list(self, token, team_id=None, **kwargs):
        client = WebClient(token=token)
        return client.users_list(team_id=team_id, **kwargs)

    def conversations_open(self, *, token, users=None, **kwargs):
        client = WebClient(token=token)
        return client.conversations_open(users=users, **kwargs)

    def chat_update(self, *, token, channel_id=None, ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_update(channel=channel_id, ts=ts, **kwargs)

    def chat_delete(self, *, token, channel_id=None, ts=None, **kwargs):
        client = WebClient(token=token)
        return client.chat_delete(channel=channel_id, ts=ts, **kwargs)
