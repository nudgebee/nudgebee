import html
import json
import logging
import os
import re
from datetime import datetime, timedelta

import aiohttp
import nh3
from slack_sdk.errors import SlackApiError
from sqlalchemy.sql.functions import func

from notifications_server.clients.google_chat_client import GoogleChatClient
from notifications_server.clients.ms_teams_client import MsTeamsClient
from notifications_server.configs import settings
from notifications_server.exceptions.common_exc import BeeHTTPError
from notifications_server.exceptions.exceptions import Err
from notifications_server.message_templates.blocks import MarkdownBlock, ContextBlock
from notifications_server.models.db_base import BaseDB
from notifications_server.models.models import MessagingPlatform, SentNotifications, ConfigurationStore
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.transformer import Transformer
from notifications_server.services.cache import Cache

from botbuilder.core import TurnContext, BotFrameworkAdapterSettings, BotFrameworkAdapter
from botbuilder.schema import Activity, ActivityTypes, Attachment
from botframework.connector.auth import MicrosoftAppCredentials
from botbuilder.schema import ConversationAccount, ChannelAccount, ConversationReference

cache = Cache()
LOG = logging.getLogger(__name__)

# Adaptive Card constants
ADAPTIVE_CARD_SCHEMA_KEY = "$schema"
ADAPTIVE_CARD_SCHEMA_URL = "http://adaptivecards.io/schemas/adaptive-card.json"

# Error message constants
ERR_UNKNOWN = "Unknown error"
ERR_TOKEN_REFRESH_FAILED = "Token refresh failed"
ERR_GCHAT_TOKEN_REFRESH = "Unable to refresh g chat token for installation id %s"
MSG_DM_SENT_SUCCESS = "Direct message sent successfully"


class CommonService:
    def __init__(self, engine, slack_app, teams_app):
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.engine = engine
        self._scoped_session = BaseDB.session(self.engine)
        self.session = self._scoped_session()
        self.teams_adapter = None

    def close(self):
        """Close and remove the scoped session, returning the connection to the pool."""
        try:
            self._scoped_session.remove()
        except Exception:
            pass

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
        return False

    def list_channels(self, platform, tenant):
        try:
            if platform not in ["slack", "ms_teams", "google_chat"]:
                return BeeHTTPError(400, Err.OS0011, ["platform"])

            messaging_platform = (
                self.session.query(MessagingPlatform)
                .filter(MessagingPlatform.tenant_id == tenant, MessagingPlatform.platform == platform)
                .one_or_none()
            )
            if not messaging_platform:
                LOG.info("Unable to list channels for %s, no installation in tenant: %s ", platform, tenant)
                return {"data": []}

            if platform == "slack":
                return self.get_slack_channels(messaging_platform)
            elif platform == "ms_teams":
                return self.get_ms_teams_channels(messaging_platform)
            elif platform == "google_chat":
                return self.get_google_chat_channels(messaging_platform)
            return {}
        except Exception as e:
            self.session.rollback()
            LOG.critical("Unable to get channel list for %s, %s", platform, e)
            return {}

    def get_slack_channels(self, messaging_platform):
        channels = []
        next_cursor = None

        while True:
            try:
                slack_channels = self.slack_app.client.channels_list(
                    messaging_platform.token, messaging_platform.team_id, cursor=next_cursor
                )
            except SlackApiError as e:
                if e.response.headers.get("Retry-After"):
                    LOG.warning(
                        "Slack rate limited during channel list, returning %d channels fetched so far", len(channels)
                    )
                    break
                raise

            channels.extend(
                {"name": channel["name"], "id": channel["id"], "is_private": channel.get("is_private", False)}
                for channel in slack_channels.get("channels", [])
            )

            if slack_channels.get("response_metadata") and slack_channels["response_metadata"].get("next_cursor"):
                next_cursor = slack_channels["response_metadata"]["next_cursor"]
            else:
                break

        return {"data": channels}

    def list_users(self, platform, tenant):
        try:
            if platform not in ["slack", "ms_teams", "google_chat"]:
                return BeeHTTPError(400, Err.OS0011, ["platform"])

            messaging_platform = (
                self.session.query(MessagingPlatform)
                .filter(MessagingPlatform.tenant_id == tenant, MessagingPlatform.platform == platform)
                .one_or_none()
            )
            if not messaging_platform:
                LOG.info("Unable to list users for %s, no installation in tenant: %s", platform, tenant)
                return {"data": []}

            if platform == "slack":
                return self.get_slack_users(messaging_platform)
            elif platform == "ms_teams":
                return self.get_ms_teams_users(messaging_platform)
            elif platform == "google_chat":
                return self.get_google_chat_users(messaging_platform)
            return {}
        except Exception as e:
            self.session.rollback()
            LOG.critical("Unable to get user list for %s, %s", platform, e)
            return {}

    def get_slack_users(self, messaging_platform):
        users = []
        next_cursor = None

        while True:
            slack_users = self.slack_app.client.users_list(
                messaging_platform.token, messaging_platform.team_id, cursor=next_cursor
            )

            for member in slack_users.get("members", []):
                # Skip bots and deleted/deactivated users
                if member.get("is_bot") or member.get("id") == "USLACKBOT" or member.get("deleted"):
                    continue

                profile = member.get("profile", {})
                users.append(
                    {
                        "id": member["id"],
                        "name": member.get("name"),
                        "real_name": member.get("real_name") or profile.get("real_name"),
                        "display_name": profile.get("display_name"),
                        "email": profile.get("email"),
                    }
                )

            if slack_users.get("response_metadata") and slack_users["response_metadata"].get("next_cursor"):
                next_cursor = slack_users["response_metadata"]["next_cursor"]
            else:
                break

        return {"data": users}

    def get_ms_teams_users(self, messaging_platform):
        error = self._refresh_ms_teams_token(messaging_platform)
        if error:
            return {"data": []}

        users = MsTeamsClient.list_users(messaging_platform.token)
        return {"data": users}

    def get_google_chat_users(self, messaging_platform):
        error = self._refresh_google_chat_token(messaging_platform)
        if error:
            return {"data": []}

        # Google Chat can only list DM spaces that the bot is already part of
        # Return list of users from existing DM conversations
        dm_spaces = GoogleChatClient.list_dm_spaces(messaging_platform.token)
        return {"data": dm_spaces}

    def get_ms_teams_channels(self, messaging_platform):
        error = self._refresh_ms_teams_token(messaging_platform)
        if error:
            return None

        return {"data": MsTeamsClient.list_joined_teams(messaging_platform.token)}

    def get_google_chat_channels(self, messaging_platform):
        error = self._refresh_google_chat_token(messaging_platform)
        if error:
            return None

        return {"data": GoogleChatClient.list_spaces(messaging_platform.token)}

    def join_channel(self, platform, account_id, tenant_id, channel_id, session_id=None, team_id=None, text=None):
        try:
            if platform != "slack":
                return {"error": {"message": f"Platform {platform} is not supported yet"}}

            messaging_platform = self._get_messaging_platform(tenant_id, team_id, platform)
            if not messaging_platform:
                return {"error": {"message": f"No Slack installation found for tenant: {tenant_id}"}}

            try:
                join_response = self.slack_app.client.conversations_join(
                    token=messaging_platform.token, channel_id=channel_id
                )

                if not join_response.get("ok"):
                    error_msg = join_response.get("error", ERR_UNKNOWN)
                    LOG.error(f"Failed to join channel {channel_id}: {error_msg}")
                    return {"error": {"message": f"Failed to join channel: {error_msg}"}}

            except SlackApiError as e:
                error_msg = e.response.get("error", str(e))
                LOG.error(f"Slack API error joining channel {channel_id}: {error_msg}")

                return self._check_error_msg(error_msg, "channels:join")

            if session_id is not None:
                session_data = {
                    "platform": platform,
                    "account_id": account_id,
                    "tenant_id": tenant_id,
                    "team_id": team_id or messaging_platform.team_id,
                    "channel_id": channel_id,
                    "joined_at": datetime.now().isoformat(),
                }

                cache.cache_event_entry(session_id, session_data)
                LOG.info(f"Cached channel join session: {session_id} for channel: {channel_id}, text: {text}")

                channel_team_id = team_id or messaging_platform.team_id
                cache.cache_channel_session_mapping(
                    channel_id=channel_id,
                    team_id=channel_team_id,
                    session_id=session_id,
                    account_id=account_id,
                    tenant_id=tenant_id,
                )
                LOG.info(
                    f"Cached channel-to-session mapping: {channel_id} ({channel_team_id}) -> "
                    f"session_id={session_id}, account_id={account_id}, tenant_id={tenant_id}"
                )

            return {
                "success": True,
                "message": "Successfully joined channel",
                "data": {"channel_id": channel_id, "session_id": session_id, "platform": platform},
            }

        except Exception as e:
            LOG.exception(f"Error joining channel: {e}")
            return {"error": {"message": f"Unexpected error: {str(e)}"}}

    def _get_messaging_platform(self, tenant_id, team_id, platform):
        query = self.session.query(MessagingPlatform).filter(
            MessagingPlatform.tenant_id == tenant_id, MessagingPlatform.platform == platform
        )

        # Only filter by team_id for Slack where the DB column stores the
        # workspace team ID matching what callers pass.  For MS Teams the
        # column holds the Microsoft account home_account_id, and Google Chat
        # never sets team_id at all, so the filter only makes sense for Slack.
        if team_id and platform == "slack":
            query = query.filter(MessagingPlatform.team_id == team_id)

        messaging_platform = query.one_or_none()

        return messaging_platform

    def _refresh_ms_teams_token(self, messaging_platform):
        """Refresh MS Teams token if expired. Returns error string on failure, None on success."""
        if not (messaging_platform.token_expires_at and messaging_platform.token_expires_at < utc_now()):
            return None
        response = self.teams_app.acquire_token_by_refresh_token(
            messaging_platform.refresh_token,
            scopes=settings.ms_teams.scopes,
        )
        if "error" in response and response["error"]:
            LOG.error("Unable to refresh MS Teams token: %s", response.get("error_description", ERR_UNKNOWN))
            return ERR_TOKEN_REFRESH_FAILED
        messaging_platform.token = response.get("access_token")
        messaging_platform.refresh_token = response.get("refresh_token")
        expires_in = response.get("expires_in")
        messaging_platform.token_expires_at = utc_now() + timedelta(seconds=expires_in - 100) if expires_in else None
        self.session.add(messaging_platform)
        self.session.commit()
        return None

    def _refresh_google_chat_token(self, messaging_platform):
        """Refresh Google Chat token if expired. Returns error string on failure, None on success."""
        if not (messaging_platform.token_expires_at and messaging_platform.token_expires_at < utc_now()):
            return None
        response = GoogleChatClient.refresh_access_token(messaging_platform.refresh_token)
        if not response:
            LOG.warning(ERR_GCHAT_TOKEN_REFRESH, messaging_platform.id)
            return ERR_TOKEN_REFRESH_FAILED
        if "error" in response and response["error"]:
            LOG.error("Unable to refresh Google Chat token: %s", response.get("error_description", ERR_UNKNOWN))
            return ERR_TOKEN_REFRESH_FAILED
        messaging_platform.token = response.get("access_token")
        expires_in = response.get("expires_in")
        messaging_platform.token_expires_at = utc_now() + timedelta(seconds=expires_in - 100) if expires_in else None
        self.session.add(messaging_platform)
        self.session.commit()
        return None

    @staticmethod
    def _generate_welcome_message(custom_text=None):
        if custom_text:
            return f"Hey there! {custom_text}\n\nI'm here and ready to help you. Feel free to mention me anytime!"
        else:
            return (
                "Hey there!\n\nI'm here to keep you updated with important updates. Feel free to mention me anytime"
                " if you need help!"
            )

    def send_welcome_message(self, channel_id, token, custom_text=None):
        try:
            welcome_message = self._generate_welcome_message(custom_text)
            self.slack_app.client.chat_post(
                token=token,
                channel_id=channel_id,
                text=welcome_message,
                blocks=[{"type": "section", "text": {"type": "mrkdwn", "text": welcome_message}}],
            )
            LOG.info(f"Successfully sent welcome message to channel {channel_id}")
        except Exception as e:
            LOG.warning(f"Failed to send welcome message to channel {channel_id}: {e}")

    def send_channel_message(self, platform, tenant_id, channel_id, session_id, text, team_id=None):
        try:
            if platform != "slack":
                return {"error": {"message": f"Platform {platform} is not supported yet"}}

            messaging_platform = self._get_messaging_platform(tenant_id, team_id, platform)
            if not messaging_platform:
                return {"error": {"message": f"No Slack installation found for tenant: {tenant_id}"}}

            try:
                message_response = self.slack_app.client.chat_post(
                    token=messaging_platform.token,
                    channel_id=channel_id,
                    text=text,
                    blocks=[{"type": "section", "text": {"type": "mrkdwn", "text": text}}],
                )

                if not message_response.get("ok"):
                    error_msg = message_response.get("error", ERR_UNKNOWN)
                    LOG.error(f"Failed to send message to channel {channel_id}: {error_msg}")
                    return {"error": {"message": f"Failed to send message: {error_msg}"}}

                message_ts = message_response.get("ts")

                LOG.info(f"Successfully sent message to channel {channel_id} for session {session_id}")

                return {
                    "success": True,
                    "message": "Message sent successfully",
                    "data": {
                        "channel_id": channel_id,
                        "session_id": session_id,
                        "platform": platform,
                        "message_ts": message_ts,
                    },
                }

            except SlackApiError as e:
                error_msg = e.response.get("error", str(e))
                LOG.error(f"Slack API error sending message to channel {channel_id}: {error_msg}")

                return self._check_error_msg(error_msg, "chat:write")

        except Exception as e:
            LOG.exception(f"Error sending message to channel: {e}")
            return {"error": {"message": f"Unexpected error: {str(e)}"}}

    def send_direct_message(self, platform, tenant_id, user_id, text, team_id=None):
        try:
            if platform not in ["slack", "ms_teams", "google_chat"]:
                return {"error": {"message": f"Platform {platform} is not supported for direct messages"}}

            messaging_platform = self._get_messaging_platform(tenant_id, team_id, platform)
            if not messaging_platform:
                return {"error": {"message": f"No {platform} installation found for tenant: {tenant_id}"}}

            if platform == "slack":
                return self._send_slack_direct_message(messaging_platform, user_id, text)
            elif platform == "ms_teams":
                return self._send_teams_direct_message(messaging_platform, user_id, text, tenant_id)
            elif platform == "google_chat":
                return self._send_google_chat_direct_message(messaging_platform, user_id, text, tenant_id)

        except Exception as e:
            LOG.exception(f"Error sending direct message: {e}")
            return {"error": {"message": f"Unexpected error: {str(e)}"}}

    def _send_slack_direct_message(self, messaging_platform, user_id, text):
        try:
            # Open a DM channel with the user
            dm_response = self.slack_app.client.conversations_open(token=messaging_platform.token, users=user_id)

            if not dm_response.get("ok"):
                error_msg = dm_response.get("error", ERR_UNKNOWN)
                LOG.error(f"Failed to open DM channel with user {user_id}: {error_msg}")
                return {"error": {"message": f"Failed to open DM channel: {error_msg}"}}

            dm_channel_id = dm_response.get("channel", {}).get("id")
            if not dm_channel_id:
                return {"error": {"message": "Failed to get DM channel ID"}}

            # Send message to the DM channel
            message_response = self.slack_app.client.chat_post(
                token=messaging_platform.token,
                channel_id=dm_channel_id,
                text=text,
                blocks=[{"type": "section", "text": {"type": "mrkdwn", "text": text}}],
            )

            if not message_response.get("ok"):
                error_msg = message_response.get("error", ERR_UNKNOWN)
                LOG.error(f"Failed to send DM to user {user_id}: {error_msg}")
                return {"error": {"message": f"Failed to send message: {error_msg}"}}

            message_ts = message_response.get("ts")
            LOG.info(f"Successfully sent DM to user {user_id} via channel {dm_channel_id}")

            return {
                "success": True,
                "message": MSG_DM_SENT_SUCCESS,
                "data": {
                    "user_id": user_id,
                    "channel_id": dm_channel_id,
                    "message_ts": message_ts,
                    "platform": "slack",
                },
            }

        except SlackApiError as e:
            error_msg = e.response.get("error", str(e))
            LOG.error(f"Slack API error sending DM to user {user_id}: {error_msg}")
            return self._check_dm_error_msg(error_msg)

    def _send_teams_direct_message(self, messaging_platform, user_id, text, tenant_id):
        error = self._refresh_ms_teams_token(messaging_platform)
        if error:
            return {"error": {"message": error}}

        result = MsTeamsClient.create_chat_and_send_message(
            access_token=messaging_platform.token,
            user_id=user_id,
            message=text,
            tenant_id=tenant_id,
        )

        if not result.get("success"):
            return {"error": {"message": result.get("error", "Failed to send direct message")}}

        return {
            "success": True,
            "message": MSG_DM_SENT_SUCCESS,
            "data": result.get("data"),
        }

    def _send_google_chat_direct_message(self, messaging_platform, user_id, text, tenant_id):
        error = self._refresh_google_chat_token(messaging_platform)
        if error:
            return {"error": {"message": error}}

        result = GoogleChatClient.send_direct_message(
            token=messaging_platform.token,
            user_id=user_id,
            message=text,
            tenant=tenant_id,
        )

        if not result.get("success"):
            return {"error": {"message": result.get("error", "Failed to send direct message")}}

        return {
            "success": True,
            "message": MSG_DM_SENT_SUCCESS,
            "data": result.get("data"),
        }

    @staticmethod
    def _check_dm_error_msg(error_msg):
        if error_msg == "user_not_found":
            return {"error": {"message": "User not found"}}
        elif error_msg == "cannot_dm_bot":
            return {"error": {"message": "Cannot send direct message to a bot"}}
        elif error_msg == "user_disabled":
            return {"error": {"message": "Cannot send direct message to a deactivated user"}}
        elif error_msg == "missing_scope":
            return {"error": {"message": "Bot missing required permissions. Please add im:write scope."}}
        elif error_msg == "channel_not_found":
            return {"error": {"message": "Could not open DM channel with user"}}
        else:
            return {"error": {"message": f"Failed: {error_msg}"}}

    def get_incident_details_and_reply_on_channel(self, incident_uuid, payload):
        try:
            incident = cache.get_event_entry(incident_uuid)
            if not incident:
                LOG.warning(f"No cached incident found for UUID: {incident_uuid}")
                return

            platform = incident.get("platform", "slack")
            tenant_id = incident.get("tenant_id")
            team_id = incident.get("team_id")
            channel_id = incident.get("channel_id")
            message = payload.response

            if not all([tenant_id, team_id, channel_id, message]):
                LOG.error(
                    f"Missing required fields for incident {incident_uuid}: "
                    f"tenant_id={tenant_id}, team_id={team_id}, channel_id={channel_id}, has_message={bool(message)}"
                )
                return

            if platform == "slack":
                self.send_channel_message(platform, tenant_id, channel_id, incident_uuid, message, team_id=team_id)
            else:
                LOG.warning(f"Unsupported platform: {platform} for incident {incident_uuid}")

        except Exception as e:
            LOG.exception(f"Error replying to incident channel for {incident_uuid}: {e}")

    @staticmethod
    def _check_error_msg(error_msg, scope):
        if error_msg == "channel_not_found":
            return {"error": {"message": "Channel not found"}}
        elif error_msg == "is_archived":
            return {"error": {"message": "Cannot interact with archived channel"}}
        elif error_msg == "not_in_channel":
            return {"error": {"message": "Bot is not in the channel. Please join the channel first."}}
        elif error_msg == "missing_scope":
            return {"error": {"message": f"Bot missing required permissions. Please add {scope} scope."}}
        else:
            return {"error": {"message": f"Failed: {error_msg}"}}

    def get_user_info(self, platform, team_id, user_id):
        if platform == "slack":
            bot = (
                self.session.query(MessagingPlatform)
                .filter(MessagingPlatform.team_id == team_id, MessagingPlatform.platform == "slack")
                .first()
            )
            if not bot:
                LOG.info("Could not complete action for %s, no installation found for team: %s ", platform, team_id)
                return None

            try:
                user_info = self.slack_app.client.users_info(token=bot.token, user=user_id)
                user_data = user_info.data["user"]
                email = user_data["profile"].get("email", None)
                if not email:
                    raise ValueError(
                        "Unable to get user email, missing permission: users:info.email "
                        "Please allow necessary permissions to your Slack app."
                    )
                return email
            except (SlackApiError, ValueError) as e:
                if isinstance(e, SlackApiError) and e.response.data.get("error") == "missing_scope":
                    raise ValueError(
                        f"Unable to get user info, missing permissions: {e.response.data['needed']}, "
                        "Please allow necessary permissions to your Slack app."
                    )
                else:
                    raise ValueError(
                        "Unable to get user info. Please check your Slack app configuration and permissions."
                    )
            except Exception as e:
                LOG.exception(f"Unable to get user info, exception = {e}")
                raise ValueError("Unable to get user info. Please check your Slack app configuration and permissions.")
        return None

    def slack_reply_in_thread(self, channel_id, team_id, message_ts, message, transform_to_markdown=True):
        bot = self.get_slack_installation(team_id)
        if transform_to_markdown:
            output_blocks = Transformer.to_slack(MarkdownBlock(text=message))
        else:
            output_blocks = message
        self.slack_app.client.reply_in_thread(
            text="message",
            channel_id=channel_id,
            token=bot.token,
            thread_ts=message_ts,
            response_type="in_channel",
            replace_original=True,
            blocks=output_blocks,
        )

    def slack_reply_in_thread_with_context(
        self, channel_id, team_id, message_ts, message, context_message, to_markdown=True
    ):
        bot = self.get_slack_installation(team_id)
        if to_markdown:
            output_blocks = Transformer.to_slack(MarkdownBlock(text=message))
        else:
            output_blocks = message
        if context_message:
            context_blocks = Transformer.to_slack(ContextBlock(text=context_message))
            output_blocks = output_blocks + context_blocks
        self.slack_app.client.reply_in_thread(
            text="message",
            channel_id=channel_id,
            token=bot.token,
            thread_ts=message_ts,
            response_type="in_channel",
            replace_original=True,
            blocks=output_blocks,
        )

    def slack_reply_in_thread_as_blocks(self, channel_id, team_id, message_ts, blocks):
        """
        Send a message with blocks in a thread.
        Returns the message timestamp for later updates.
        """
        bot = self.get_slack_installation(team_id)
        response = self.slack_app.client.reply_in_thread(
            text="message",
            channel_id=channel_id,
            token=bot.token,
            thread_ts=message_ts,
            response_type="in_channel",
            replace_original=True,
            blocks=blocks,
        )
        return response.get("ts") if response else None

    def update_slack_message(self, channel_id, team_id, message_ts, new_text, blocks=None):
        """Update an existing Slack message with new content."""
        try:
            bot = self.get_slack_installation(team_id)
            if blocks is None:
                blocks = Transformer.to_slack(MarkdownBlock(text=new_text))
            self.slack_app.client.chat_update(
                token=bot.token,
                channel_id=channel_id,
                ts=message_ts,
                text=new_text,
                blocks=blocks,
            )
            return True
        except Exception as e:
            LOG.debug(f"Failed to update Slack message: {e}")
            return False

    def update_slack_message_with_blocks(self, channel_id, team_id, message_ts, blocks):
        """Update an existing Slack message with new blocks."""
        try:
            bot = self.get_slack_installation(team_id)
            self.slack_app.client.chat_update(
                token=bot.token,
                channel_id=channel_id,
                ts=message_ts,
                text="message",
                blocks=blocks,
            )
            return True
        except Exception as e:
            LOG.debug(f"Failed to update Slack message with blocks: {e}")
            return False

    def delete_slack_message(self, channel_id, team_id, message_ts):
        """Delete a Slack message."""
        try:
            bot = self.get_slack_installation(team_id)
            self.slack_app.client.chat_delete(
                token=bot.token,
                channel_id=channel_id,
                ts=message_ts,
            )
            return True
        except Exception as e:
            LOG.debug(f"Failed to delete Slack message: {e}")
            return False

    def add_slack_reactions(self, channel_id, team_id, message_ts, emoji_name):
        bot = self.get_slack_installation(team_id)
        self.slack_app.client.reactions_add(
            channel_id=channel_id,
            token=bot.token,
            thread_ts=message_ts,
            emoji_name=emoji_name,
        )

    def add_reaction(self, platform, tenant_id, channel_id, message_id, emoji, team_id=None):
        """
        Add a reaction to a message on any supported platform.

        Args:
            platform: Platform name ("slack", "ms_teams", "google_chat")
            tenant_id: Tenant identifier
            channel_id: Channel/Space ID
            message_id: Message timestamp/ID
            emoji: Emoji (name for Slack, unicode for Teams/GChat)
            team_id: Optional team ID (required for MS Teams)

        Returns:
            Dict with success status and details
        """
        try:
            if platform not in ["slack", "ms_teams", "google_chat"]:
                return {"success": False, "error": f"Unsupported platform: {platform}"}

            messaging_platform = self._get_messaging_platform(tenant_id, team_id, platform)
            if not messaging_platform:
                return {"success": False, "error": f"No {platform} installation found for tenant: {tenant_id}"}

            if platform == "slack":
                return self._add_slack_reaction(messaging_platform, channel_id, message_id, emoji)
            elif platform == "ms_teams":
                return self._add_teams_reaction(messaging_platform, channel_id, message_id, emoji, team_id, tenant_id)
            elif platform == "google_chat":
                return self._add_google_chat_reaction(messaging_platform, channel_id, message_id, emoji, tenant_id)

            # This should not be reached due to validation above, but handle gracefully
            return {"success": False, "error": f"Unsupported platform: {platform}"}

        except Exception as e:
            LOG.exception("Error adding reaction on %s: %s", platform, e)
            return {"success": False, "error": f"Unexpected error: {str(e)}"}

    def _add_slack_reaction(self, messaging_platform, channel_id, message_id, emoji):
        """Add a reaction to a Slack message."""
        try:
            self.slack_app.client.reactions_add(
                channel_id=channel_id,
                token=messaging_platform.token,
                thread_ts=message_id,
                emoji_name=emoji,
            )
            return {
                "success": True,
                "provider": "slack",
                "channel_id": channel_id,
                "message_id": message_id,
                "reaction": emoji,
            }
        except SlackApiError as e:
            error_msg = e.response.get("error", str(e))
            LOG.error("Slack API error adding reaction: %s", error_msg)
            return {"success": False, "provider": "slack", "error": error_msg}

    def _add_teams_reaction(self, messaging_platform, channel_id, message_id, emoji, team_id, tenant_id):
        """Add a reaction to an MS Teams message."""
        error = self._refresh_ms_teams_token(messaging_platform)
        if error:
            return {"success": False, "provider": "ms_teams", "error": error}

        # Get team_id from messaging_platform if not provided
        actual_team_id = team_id or messaging_platform.team_id
        if not actual_team_id:
            return {"success": False, "provider": "ms_teams", "error": "team_id is required for MS Teams reactions"}

        return MsTeamsClient.set_reaction(
            access_token=messaging_platform.token,
            team_id=actual_team_id,
            channel_id=channel_id,
            message_id=message_id,
            reaction_type=emoji,
            tenant_id=tenant_id,
        )

    def _add_google_chat_reaction(self, messaging_platform, space_id, message_id, emoji, tenant_id):
        """Add a reaction to a Google Chat message."""
        error = self._refresh_google_chat_token(messaging_platform)
        if error:
            return {"success": False, "provider": "google_chat", "error": error}

        return GoogleChatClient.create_reaction(
            token=messaging_platform.token,
            space_id=space_id,
            message_id=message_id,
            emoji=emoji,
            tenant=tenant_id,
        )

    async def send_email(
        self,
        recipients,
        subject,
        body=None,
        body_format=None,
        template=None,
        template_params=None,
        reply_to=None,
        cc=None,
        bcc=None,
    ):
        """
        Send an email to one or more recipients.

        Args:
            recipients: List of email addresses or single email address (To)
            subject: Email subject
            body: Email body content - used if template is not provided
            body_format: How to render body content. One of "text" (default),
                "markdown", or "html". Markdown is rendered to HTML before
                sanitization so callers can pipe LLM output directly.
            template: Template name to use instead of body
            template_params: Parameters for template rendering
            reply_to: Optional reply-to email address
            cc: Optional list (or single string) of Cc addresses
            bcc: Optional list (or single string) of Bcc addresses
                (envelope-only; never appears in headers)

        Returns:
            Dict with success status and details

        Behavior note: when only `recipients` is provided, the message is sent
        per-recipient so the To list is not exposed across recipients. Whenever
        any cc/bcc is provided, a single combined message is sent so the
        addressing semantics match standard mail clients.
        """
        try:
            validation_error = self._validate_email_inputs(recipients, subject)
            if validation_error:
                return validation_error

            recipient_list = [recipients] if isinstance(recipients, str) else recipients
            cc_list = self._normalize_address_list(cc)
            bcc_list = self._normalize_address_list(bcc)

            email_config = self._prepare_email_config(body, template, template_params, body_format)

            if cc_list or bcc_list:
                sent_to, errors = await self._send_combined_email(
                    recipient_list, cc_list, bcc_list, subject, email_config, reply_to
                )
            else:
                sent_to, errors = await self._send_emails_to_recipients(recipient_list, subject, email_config, reply_to)

            return self._build_email_response(sent_to, errors)

        except Exception as e:
            LOG.exception("Error sending email: %s", e)
            return {"success": False, "error": f"Unexpected error: {str(e)}"}

    @staticmethod
    def _normalize_address_list(addresses):
        """Normalize an address input (None / str / list) to a clean list."""
        if not addresses:
            return []
        if isinstance(addresses, str):
            return [addresses]
        return [a for a in addresses if a]

    @staticmethod
    def _validate_email_inputs(recipients, subject):
        """Validate email inputs and return error dict if invalid, None if valid."""
        if not recipients:
            return {"success": False, "error": "No recipients provided"}
        if not subject:
            return {"success": False, "error": "Subject is required"}
        return None

    @staticmethod
    def _render_body_for_format(body, body_format):
        """Convert raw body content into HTML based on the requested format.

        - "markdown": render to HTML via the markdown library so headings,
          lists, fenced code, and tables produced by upstream LLM tasks render
          correctly inside the generic email shell.
        - "html": pass through; the caller already supplied HTML.
        - "text" (default) or unknown: wrap in a <pre> block so newlines and
          monospace formatting from raw LLM/log output are preserved instead of
          collapsing into a single line.
        """
        fmt = (body_format or "text").lower()

        if fmt == "markdown":
            import markdown2

            return markdown2.markdown(
                body,
                extras=["fenced-code-blocks", "tables", "break-on-newline", "cuddled-lists"],
            )

        if fmt == "html":
            return body

        # Default / "text": preserve whitespace and line breaks.
        escaped = html.escape(body)
        return f'<pre style="white-space:pre-wrap;font-family:inherit;margin:0;">{escaped}</pre>'

    @staticmethod
    def _prepare_email_config(body, template, template_params, body_format=None):
        """Prepare email configuration including template type and params."""
        from notifications_server.configs.settings import get_smtp_params

        smtp_params = get_smtp_params()
        from_email = smtp_params[4] if smtp_params and smtp_params[4] else settings.email.from_address

        params = template_params.copy() if template_params else {}

        if body and not template:
            rendered_body = CommonService._render_body_for_format(body, body_format)
            params["message"] = nh3.clean(
                rendered_body,
                tags={
                    "a",
                    "abbr",
                    "b",
                    "br",
                    "blockquote",
                    "code",
                    "div",
                    "em",
                    "h1",
                    "h2",
                    "h3",
                    "h4",
                    "h5",
                    "h6",
                    "hr",
                    "i",
                    "img",
                    "li",
                    "ol",
                    "p",
                    "pre",
                    "span",
                    "strong",
                    "sub",
                    "sup",
                    "table",
                    "tbody",
                    "td",
                    "th",
                    "thead",
                    "tr",
                    "u",
                    "ul",
                },
                attributes={
                    "*": {"style", "class"},
                    "a": {"href", "title", "target"},
                    "img": {"src", "alt", "width", "height"},
                    "td": {"colspan", "rowspan"},
                    "th": {"colspan", "rowspan"},
                },
                url_schemes={"http", "https", "mailto"},
            )
            template_type = "generic"
        else:
            template_type = template or "default"

        return {"from_email": from_email, "template_type": template_type, "template_params": params}

    async def _send_emails_to_recipients(self, recipients, subject, email_config, reply_to):
        """Send emails to all recipients and collect results."""
        from notifications_server.emailer import generate_email, send_email_async

        sent_to = []
        errors = []

        for recipient in recipients:
            try:
                email_msg = generate_email(
                    to=recipient,
                    subject=subject,
                    template_params=email_config["template_params"],
                    template_type=email_config["template_type"],
                    reply_to_email=reply_to,
                    frm=email_config["from_email"],
                )
                await send_email_async(email_msg)
                sent_to.append(recipient)
                LOG.info("Email sent successfully to %s", recipient)
            except Exception as e:
                LOG.error("Failed to send email to %s: %s", recipient, e)
                errors.append({"recipient": recipient, "error": str(e)})

        return sent_to, errors

    async def _send_combined_email(self, to_list, cc_list, bcc_list, subject, email_config, reply_to):
        """Send a single combined email with To/Cc headers and envelope-only Bcc.

        Returns (sent_to, errors). On a successful SMTP send, sent_to is the
        full envelope list (To + Cc + Bcc).
        """
        from notifications_server.emailer import build_envelope_recipients, generate_email, send_email_async

        envelope = build_envelope_recipients(to_list, cc_list, bcc_list)
        try:
            email_msg = generate_email(
                to=to_list,
                subject=subject,
                template_params=email_config["template_params"],
                template_type=email_config["template_type"],
                reply_to_email=reply_to,
                frm=email_config["from_email"],
                cc=cc_list,
                bcc=bcc_list,
            )
            await send_email_async(email_msg, envelope_recipients=envelope)
            LOG.info(
                "Combined email sent: to=%d cc=%d bcc=%d",
                len(to_list),
                len(cc_list or []),
                len(bcc_list or []),
            )
            return envelope, []
        except Exception as e:
            LOG.error("Failed to send combined email: %s", e)
            return [], [{"recipients": envelope, "error": str(e)}]

    @staticmethod
    def _build_email_response(sent_to, errors):
        """Build the response dict based on send results."""
        if sent_to:
            return {
                "success": True,
                "sent_to": sent_to,
                "errors": errors or None,
            }
        return {
            "success": False,
            "error": "Failed to send email to any recipient",
            "errors": errors,
        }

    def post_slack_ephimeral_response(self, channel_id, team_id, user_id, message):
        bot = self.get_slack_installation(team_id)
        output_blocks = Transformer.to_slack(MarkdownBlock(text=message))
        self.slack_app.client.post_ephimeral(
            text="message",
            channel_id=channel_id,
            user=user_id,
            token=bot.token,
            response_type="in_channel",
            replace_original=True,
            blocks=output_blocks,
        )

    def get_slack_conversation(self, channel_id, thread_ts, team_id, cached_entry):
        try:
            bot = self.get_slack_installation(team_id)
            response = self.slack_app.client.conversations_replies(
                token=bot.token,
                channel_id=channel_id,
                thread_ts=thread_ts,
            )
            messages = response.get("messages", [])
            already_sent_messages = set(cached_entry.get("sent_messages", []))
            entries = self._collect_conversation_entries(messages, bot, already_sent_messages)

            # The latest user message is the query; everything else (including bot
            # replies that came after it) stays as context in chronological order.
            last_user_idx = next((i for i in range(len(entries) - 1, -1, -1) if entries[i][0] == "user"), None)
            if last_user_idx is None:
                return None, list(already_sent_messages)

            conversation = self._format_conversation(entries, last_user_idx)
            return conversation, list(already_sent_messages)

        except Exception as e:
            LOG.error("Failed to fetch or format Slack conversation: %s", e, exc_info=True)
            return None, []

    def _collect_conversation_entries(self, messages, bot, already_sent_messages):
        is_first_read = not already_sent_messages
        entries = []
        for msg in messages:
            if self._should_skip_message(msg, already_sent_messages, bot, is_first_read):
                continue

            text = self._extract_meaningful_text(msg, bot)
            if not text.strip():
                continue

            generic_markdown = Transformer.slack_markdown_to_generic_markdown(text)
            is_bot_msg = bool(msg.get("bot_id") and bot.bot_id and msg["bot_id"] == bot.bot_id)
            role = "assistant" if is_bot_msg else "user"
            entries.append((role, generic_markdown))
            already_sent_messages.add(msg["ts"])
        return entries

    @staticmethod
    def _format_conversation(entries, last_user_idx):
        last_user_text = entries[last_user_idx][1]
        if last_user_text.startswith("@"):
            last_user_text = re.sub(r"^@[^\s]+\s*", "", last_user_text)
        last_message = f"{last_user_text}\n"

        context_entries = [e for i, e in enumerate(entries) if i != last_user_idx]
        if not context_entries:
            return last_message

        context = "\n".join(f"{role}:\n{text}\n" for role, text in context_entries)
        return f"{last_message}\n\n--- context ---\n{context}"

    def _extract_meaningful_text(self, msg, bot):
        parts = []

        if msg.get("text"):
            parts.append(msg["text"])

        self._extract_text_from_attachments(msg, parts)
        self._extract_text_from_blocks(msg, parts)

        text = "\n".join(p for p in parts if p)
        text = self._replace_user_ids_with_names(text, bot)

        return text

    @staticmethod
    def _extract_text_from_blocks(msg, parts):
        if "blocks" in msg:
            for block in msg["blocks"]:
                if block.get("text"):
                    parts.append(block["text"].get("text", ""))
                CommonService._extract_text_from_elements(block, parts)

    @staticmethod
    def _extract_text_from_elements(block, parts):
        if "elements" in block:
            for el in block["elements"]:
                if "type" in el and el["type"] == "button":
                    continue
                if "text" in el:
                    parts.append(el["text"])

    @staticmethod
    def _extract_text_from_attachments(msg, parts):
        attachments = msg.get("attachments", [])
        for att in attachments:
            for key in ("pretext", "title", "text"):
                value = att.get(key)
                if value:
                    parts.append(value)

            if not att.get("title") and not att.get("text"):
                fallback = att.get("fallback")
                if fallback:
                    parts.append(fallback)

            footer = att.get("footer")
            if footer:
                parts.append(f"({footer})")

    # Marker for selection-confirmation messages ("I asked: ...") emitted by
    # get_account_selected_with_context / get_followup_selection_confirmation.
    # These are UX scaffolding, not conversation content, so skip them even on first read.
    _SELECTION_CONFIRMATION_PREFIX = "> _I asked:_"

    @classmethod
    def _should_skip_message(cls, msg, already_sent_messages, bot, is_first_read=False):
        if msg.get("ts") in already_sent_messages:
            return True

        is_own_bot_msg = bool(msg.get("bot_id") and bot.bot_id and msg["bot_id"] == bot.bot_id)
        if is_own_bot_msg:
            text = (msg.get("text") or "").lstrip()
            if text.startswith(cls._SELECTION_CONFIRMATION_PREFIX):
                return True
            if not is_first_read:
                return True

        return False

    @staticmethod
    def _extract_message_text(msg):
        parts = []

        # Include base text if present
        if "text" in msg and msg["text"]:
            parts.append(msg["text"])

        # Include attachments' title and text
        for attachment in msg.get("attachments", []):
            if "title" in attachment:
                parts.append(f"*{attachment['title']}*")
            if "text" in attachment:
                parts.append(attachment["text"])

        # Join parts and return full content
        return "\n".join(parts)

    def _replace_user_ids_with_names(self, text, bot):
        user_ids = re.findall(r"<@([UW][A-Z0-9]+)>", text)
        for user_id in set(user_ids):
            try:
                user_info = self.slack_app.client.users_info(token=bot.token, user=user_id)
                username = user_info["user"]["name"]
                text = text.replace(f"<@{user_id}>", f"@{username}")
            except Exception as e:
                LOG.warning(f"Could not resolve Slack user ID {user_id} to a username: {e}")
                # If user can't be resolved, leave it as-is
                continue
        return text

    def get_slack_installation(self, team_id):
        return self.session.query(MessagingPlatform).filter_by(team_id=team_id, platform="slack").first()

    def get_slack_user_display_name(self, team_id, user_id):
        """
        Get the display name or real name of a Slack user.

        Args:
            team_id: The Slack team/workspace ID
            user_id: The Slack user ID

        Returns:
            The user's display name, real name, or None if not found
        """
        try:
            bot = self.get_slack_installation(team_id)
            if not bot:
                return None

            user_info = self.slack_app.client.users_info(token=bot.token, user=user_id)
            if not user_info.get("ok"):
                return None

            user_data = user_info.get("user", {})
            profile = user_data.get("profile", {})

            # Prefer display_name, then real_name, then name
            return (
                profile.get("display_name")
                or profile.get("real_name")
                or user_data.get("real_name")
                or user_data.get("name")
            )
        except Exception as e:
            LOG.debug(f"Failed to get user display name: {e}")
            return None

    def get_thread_messages(self, tenant_id, channel_id, thread_ts, team_id=None):
        try:
            messaging_platform = self._get_messaging_platform(tenant_id, team_id, "slack")
            if not messaging_platform:
                return {"success": False, "error": f"No Slack installation found for tenant: {tenant_id}"}

            return self._fetch_thread_messages(messaging_platform, channel_id, thread_ts)

        except Exception as e:
            LOG.exception(f"Error fetching thread messages: {e}")
            return {"success": False, "error": f"Unexpected error: {str(e)}"}

    def _fetch_thread_messages(self, messaging_platform, channel_id, thread_ts):
        """Fetch thread messages from Slack API."""
        try:
            response = self.slack_app.client.conversations_replies(
                token=messaging_platform.token,
                channel_id=channel_id,
                thread_ts=thread_ts,
            )

            if not response.get("ok"):
                error_msg = response.get("error", ERR_UNKNOWN)
                LOG.error(f"Failed to fetch thread messages for {channel_id}/{thread_ts}: {error_msg}")
                return {"success": False, "error": f"Failed to fetch thread messages: {error_msg}"}

            raw_messages = response.get("messages", [])
            has_more = response.get("has_more", False)
            parent_reactions = self._fetch_parent_reactions(messaging_platform.token, channel_id, thread_ts)

            user_cache = {}
            messages = [
                self._process_thread_message(msg, messaging_platform.token, parent_reactions, user_cache)
                for msg in raw_messages
            ]

            return {
                "success": True,
                "channel_id": channel_id,
                "thread_ts": thread_ts,
                "messages": messages,
                "has_more": has_more,
            }

        except SlackApiError as e:
            error_msg = e.response.get("error", str(e))
            LOG.error(f"Slack API error fetching thread messages {channel_id}/{thread_ts}: {error_msg}")
            return {"success": False, "error": self._get_thread_error_message(error_msg)}

    def _fetch_parent_reactions(self, token, channel_id, thread_ts):
        """Fetch reactions for the parent message using reactions.get API."""
        parent_reactions = {}
        try:
            reactions_response = self.slack_app.client.reactions_get(
                token=token,
                channel_id=channel_id,
                timestamp=thread_ts,
            )
            if reactions_response.get("ok"):
                parent_msg = reactions_response.get("message", {})
                parent_reactions = self._parse_reactions(parent_msg.get("reactions", []))
        except SlackApiError as e:
            LOG.warning(f"Could not fetch reactions for parent message: {e.response.get('error', str(e))}")
        return parent_reactions

    def _parse_reactions(self, reactions_list):
        """Parse reactions list into a dictionary."""
        return {
            reaction.get("name", ""): {
                "name": reaction.get("name", ""),
                "count": reaction.get("count", 0),
                "users": reaction.get("users", []),
            }
            for reaction in reactions_list
        }

    def _process_thread_message(self, msg, token, parent_reactions, user_cache):
        """Process a single thread message."""
        user_id = msg.get("user")
        user_info = self._get_cached_user_info(token, user_id, user_cache) if user_id else None
        is_parent = msg.get("thread_ts") == msg.get("ts")

        # Use explicitly fetched reactions for parent message, fall back to inline reactions
        if is_parent and parent_reactions:
            reactions = list(parent_reactions.values())
        else:
            reactions = [
                {"name": r.get("name", ""), "count": r.get("count", 0), "users": r.get("users", [])}
                for r in msg.get("reactions", [])
            ]

        return {
            "ts": msg.get("ts", ""),
            "text": self._extract_message_text(msg),
            "user_id": user_id,
            "user": user_info,
            "reactions": reactions,
            "is_parent": is_parent,
            "reply_count": msg.get("reply_count") if is_parent else None,
        }

    def _get_cached_user_info(self, token, user_id, user_cache):
        """Get user info with caching."""
        if user_id not in user_cache:
            user_cache[user_id] = self._fetch_user_info(token, user_id)
        return user_cache[user_id]

    def _fetch_user_info(self, token, user_id):
        try:
            user_response = self.slack_app.client.users_info(token=token, user=user_id)
            if user_response.get("ok"):
                user_data = user_response.get("user", {})
                profile = user_data.get("profile", {})
                return {
                    "id": user_id,
                    "name": user_data.get("name"),
                    "real_name": user_data.get("real_name") or profile.get("real_name"),
                    "display_name": profile.get("display_name"),
                    "is_bot": user_data.get("is_bot", False),
                }
        except SlackApiError as e:
            LOG.warning(f"Could not fetch user info for {user_id}: {e.response.get('error', str(e))}")
        except Exception as e:
            LOG.warning(f"Could not fetch user info for {user_id}: {e}")
        return {"id": user_id, "name": None, "real_name": None, "display_name": None, "is_bot": False}

    @staticmethod
    def _get_thread_error_message(error_msg):
        error_map = {
            "channel_not_found": "Channel not found",
            "thread_not_found": "Thread not found",
            "is_archived": "Cannot read from archived channel",
            "not_in_channel": "Bot is not in the channel. Please add the bot to the channel first.",
            "missing_scope": (
                "Bot missing required permissions (channels:history, groups:history, im:history, or mpim:history)"
            ),
        }
        return error_map.get(error_msg, f"Failed to fetch thread: {error_msg}")

    def get_channel_and_ts_from_sent_notifications(self, conversation_id):
        fingerprint = conversation_id.split("-", 1)[1]
        try:
            notification = (
                self.session.query(SentNotifications)
                .filter_by(fingerprint=fingerprint)
                .order_by(SentNotifications.created_at.desc())
                .limit(1)
                .first()
            )
        except Exception as e:
            self.session.rollback()
            LOG.error(f"Unable to query sent notifications for conversation_id: {conversation_id} due to {e}")
            raise

        if not notification or not notification.slack_metadata:
            return None, None, None, None

        try:
            slack_metadata = json.loads(notification.slack_metadata)
        except json.JSONDecodeError:
            LOG.warning(f"Failed to parse slack_metadata for fingerprint {fingerprint}")
            return None, None, None, None

        channel_id = slack_metadata.get("channel")

        return channel_id, notification.slack_thread_id, notification.slack_team_id, notification.account_id

    # ==================== Teams Bot Messaging Methods ====================

    def _init_bot_framework(self, channel_auth_tenant):
        app_id = os.environ.get("MS_TEAMS_CLIENT_ID")
        app_password = os.environ.get("MS_TEAMS_CLIENT_SECRET")
        if app_id and app_password:
            LOG.debug(f"Initializing Bot Framework adapter with App ID: {app_id[:8]}...")
            settings = BotFrameworkAdapterSettings(app_id, app_password, channel_auth_tenant)
            self.teams_adapter = BotFrameworkAdapter(settings)
            self.teams_app_id = app_id
            LOG.debug("Bot Framework adapter initialized for Teams messaging")
        else:
            self.teams_adapter = None
            self.teams_app_id = None
            LOG.warning("MS Teams bot credentials not found - Teams bot messaging will not be available")

    async def get_teams_user_info(self, aad_user_id, teams_account_id):
        try:
            if not aad_user_id:
                LOG.warning("No AAD user ID provided")
                return None

            messaging_platform = (
                self.session.query(MessagingPlatform)
                .filter(MessagingPlatform.team_id.contains(teams_account_id), MessagingPlatform.platform == "ms_teams")
                .one_or_none()
            )

            if not messaging_platform:
                LOG.debug(f"No Teams messaging platform found for teams account {teams_account_id}")
                return None

            error = self._refresh_ms_teams_token(messaging_platform)
            if error:
                return None

            graph_url = f"https://graph.microsoft.com/v1.0/users/{aad_user_id}"
            headers = {"Authorization": f"Bearer {messaging_platform.token}", "Content-Type": "application/json"}

            async with aiohttp.ClientSession() as session:
                async with session.get(graph_url, headers=headers) as response:
                    response.raise_for_status()
                    user_data = await response.json()

            user_email = user_data.get("mail") or user_data.get("userPrincipalName")

            if not user_email:
                LOG.warning(f"No email found for AAD user {aad_user_id}")
                return None

            mapping = (
                self.session.query(ConfigurationStore)
                .filter(
                    ConfigurationStore.config_type == "teams_user_mapping",
                    func.lower(ConfigurationStore.key) == user_email.lower(),
                    ConfigurationStore.is_active.is_(True),
                )
                .first()
            )
            if mapping:
                LOG.debug(f"Mapped Teams email {user_email} to {mapping.value}")
                return mapping.value

            LOG.debug(f"Successfully retrieved email for AAD user {aad_user_id}")
            return user_email

        except aiohttp.ClientError as e:
            LOG.error(f"Failed to fetch user info from Graph API: {e}")
            return None
        except Exception as e:
            LOG.exception(f"Unexpected error fetching Teams user info: {e}")
            return None

    @staticmethod
    def get_teams_user_from_activity(activity: Activity) -> tuple:
        try:
            from_property = activity.from_property
            if not from_property:
                return None, None, None

            aad_object_id = getattr(from_property, "aad_object_id", None)
            user_name = getattr(from_property, "name", None)

            return None, user_name, aad_object_id

        except Exception as e:
            LOG.exception(f"Error extracting user info from activity: {e}")
            return None, None, None

    @staticmethod
    def create_teams_cluster_selection_card(valid_accounts):
        from notifications_server.services.bot_messages import get_account_selection_prompt

        return {
            "type": "AdaptiveCard",
            ADAPTIVE_CARD_SCHEMA_KEY: ADAPTIVE_CARD_SCHEMA_URL,
            "version": "1.4",
            "body": [
                {"type": "TextBlock", "text": get_account_selection_prompt(), "weight": "Bolder", "size": "Medium"},
                {
                    "type": "Input.ChoiceSet",
                    "id": "cluster",
                    "style": "compact",
                    "choices": [{"title": acc["account_name"], "value": acc["id"]} for acc in valid_accounts],
                },
            ],
            "actions": [{"type": "Action.Submit", "title": "Let's go!"}],
        }

    @staticmethod
    def create_teams_followup_card(question, followup_options):
        """Create an Adaptive Card with clickable buttons for follow-up options."""
        card = {
            "type": "AdaptiveCard",
            ADAPTIVE_CARD_SCHEMA_KEY: ADAPTIVE_CARD_SCHEMA_URL,
            "version": "1.4",
            "body": [
                {
                    "type": "TextBlock",
                    "text": question,
                    "wrap": True,
                    "size": "Medium",
                }
            ],
        }

        if followup_options:
            actions = []
            for option in followup_options:
                actions.append(
                    {
                        "type": "Action.Submit",
                        "title": option,
                        "data": {
                            "action_type": "select_followup_option",
                            "followup_option": option,
                        },
                    }
                )
            card["actions"] = actions

        return card

    @staticmethod
    def create_teams_welcome_card():
        from notifications_server.services.bot_messages import SIGNUP_URL

        branding_name = settings.urls.branding_name
        branding_logo = settings.urls.branding_logo_url

        card = {
            "type": "AdaptiveCard",
            ADAPTIVE_CARD_SCHEMA_KEY: ADAPTIVE_CARD_SCHEMA_URL,
            "version": "1.4",
            "body": [
                {
                    "type": "ColumnSet",
                    "columns": [
                        {
                            "type": "Column",
                            "width": "auto",
                            "items": [
                                {
                                    "type": "Image",
                                    "url": branding_logo,
                                    "size": "Small",
                                    "style": "Person",
                                }
                            ],
                        },
                        {
                            "type": "Column",
                            "width": "stretch",
                            "verticalContentAlignment": "Center",
                            "items": [
                                {
                                    "type": "TextBlock",
                                    "text": f"Welcome to {branding_name}!",
                                    "weight": "Bolder",
                                    "size": "Large",
                                }
                            ],
                        },
                    ],
                },
                {
                    "type": "TextBlock",
                    "text": f"Hi there! I'm your {branding_name} assistant. Here's what I can help you with:",
                    "wrap": True,
                },
                {
                    "type": "TextBlock",
                    "text": (
                        "- **Event Analysis** — investigate and analyze infrastructure events\n"
                        "- **Optimization Recommendations** — get actionable cost and performance insights\n"
                        "- **Infrastructure Q&A** — ask me anything about your clusters and services"
                    ),
                    "wrap": True,
                },
                {
                    "type": "TextBlock",
                    "text": f"Don't have an account yet? [Sign up here]({SIGNUP_URL})",
                    "wrap": True,
                    "size": "Small",
                    "isSubtle": True,
                },
            ],
            "actions": [
                {
                    "type": "Action.OpenUrl",
                    "title": "Sign Up",
                    "url": SIGNUP_URL,
                }
            ],
        }

        return card

    async def teams_reply_with_card(self, activity: Activity, adaptive_card: dict, teams_account_id) -> bool:
        if not self.teams_adapter:
            self._init_bot_framework(teams_account_id)

        try:
            # Get conversation reference from the incoming activity
            conversation_reference = TurnContext.get_conversation_reference(activity)

            # Trust the service URL for this conversation
            MicrosoftAppCredentials.trust_service_url(activity.service_url)

            # Define the callback function that sends the card
            async def send_card_callback(turn_context: TurnContext):
                card_attachment = Attachment(
                    content_type="application/vnd.microsoft.card.adaptive",
                    content=adaptive_card,
                )

                reply_activity = Activity(
                    type=ActivityTypes.message,
                    attachments=[card_attachment],
                )

                await turn_context.send_activity(reply_activity)
                LOG.info(f"Successfully sent Adaptive Card to Teams conversation {activity.conversation.id}")

            # Continue the conversation and send the card
            await self.teams_adapter.continue_conversation(
                conversation_reference, send_card_callback, self.teams_app_id
            )

            return True

        except Exception as e:
            LOG.exception(f"Error sending Adaptive Card to Teams: {e}")
            return False

    async def teams_reply(self, activity: Activity, message: str, teams_account_id) -> bool:
        if not self.teams_adapter:
            self._init_bot_framework(teams_account_id)

        if not self.teams_adapter:
            LOG.error("Teams adapter not initialized - cannot send message")
            return False

        try:
            # Get conversation reference from the incoming activity
            conversation_reference = TurnContext.get_conversation_reference(activity)

            # Trust the service URL for this conversation
            MicrosoftAppCredentials.trust_service_url(activity.service_url)

            # Define the callback function that sends the message
            async def send_message_callback(turn_context: TurnContext):
                reply_activity = Activity(
                    type=ActivityTypes.message,
                    text=message,
                )

                await turn_context.send_activity(reply_activity)

            # Continue the conversation and send the message
            await self.teams_adapter.continue_conversation(
                conversation_reference, send_message_callback, self.teams_app_id
            )

            return True

        except Exception as e:
            LOG.exception(f"Error sending text message to Teams: {e}")
            return False

    async def teams_reply_from_conversation_reference(
        self, conversation_ref_dict: dict, message: str, teams_account_id: str
    ) -> bool:
        if not self.teams_adapter:
            self._init_bot_framework(teams_account_id)

        if not self.teams_adapter:
            LOG.error("Teams adapter not initialized - cannot send message")
            return False

        try:
            conversation_reference = ConversationReference(
                conversation=ConversationAccount(id=conversation_ref_dict.get("conversation_id")),
                service_url=conversation_ref_dict.get("service_url"),
                channel_id=conversation_ref_dict.get("channel_id"),
                bot=ChannelAccount(id=conversation_ref_dict.get("bot_id")),
                user=ChannelAccount(
                    id=conversation_ref_dict.get("user_id"), name=conversation_ref_dict.get("user_name")
                ),
            )

            MicrosoftAppCredentials.trust_service_url(conversation_ref_dict.get("service_url"))

            async def send_message_callback(turn_context: TurnContext):
                reply_activity = Activity(
                    type=ActivityTypes.message,
                    text=message,
                )
                await turn_context.send_activity(reply_activity)

            await self.teams_adapter.continue_conversation(
                conversation_reference, send_message_callback, self.teams_app_id
            )

            return True

        except Exception as e:
            LOG.exception(f"Error sending text message to Teams from cached reference: {e}")
            return False

    async def teams_reply_with_card_from_conversation_reference(
        self, conversation_ref_dict: dict, adaptive_card: dict, teams_account_id: str
    ) -> bool:
        if not self.teams_adapter:
            self._init_bot_framework(teams_account_id)

        if not self.teams_adapter:
            LOG.error("Teams adapter not initialized - cannot send card")
            return False

        try:
            conversation_reference = ConversationReference(
                conversation=ConversationAccount(id=conversation_ref_dict.get("conversation_id")),
                service_url=conversation_ref_dict.get("service_url"),
                channel_id=conversation_ref_dict.get("channel_id"),
                bot=ChannelAccount(id=conversation_ref_dict.get("bot_id")),
                user=ChannelAccount(
                    id=conversation_ref_dict.get("user_id"), name=conversation_ref_dict.get("user_name")
                ),
            )

            MicrosoftAppCredentials.trust_service_url(conversation_ref_dict.get("service_url"))

            async def send_card_callback(turn_context: TurnContext):
                card_attachment = Attachment(
                    content_type="application/vnd.microsoft.card.adaptive",
                    content=adaptive_card,
                )

                reply_activity = Activity(
                    type=ActivityTypes.message,
                    attachments=[card_attachment],
                )
                await turn_context.send_activity(reply_activity)

            await self.teams_adapter.continue_conversation(
                conversation_reference, send_card_callback, self.teams_app_id
            )

            return True

        except Exception as e:
            LOG.exception(f"Error sending Adaptive Card to Teams from cached reference: {e}")
            return False

    def send_test_notification(self, platform, tenant_id, channel_id, team_id=None):
        try:
            if platform not in ["slack", "ms_teams", "google_chat"]:
                return {"success": False, "platform": platform, "error": f"Unsupported platform: {platform}"}

            messaging_platform = self._get_messaging_platform(tenant_id, team_id, platform)
            if not messaging_platform:
                return {
                    "success": False,
                    "platform": platform,
                    "error": f"No {platform} installation found for tenant: {tenant_id}",
                }

            platform_display = {"slack": "Slack", "ms_teams": "MS Teams", "google_chat": "Google Chat"}.get(
                platform, platform
            )
            test_message = (
                f"This is a test notification from Nudgebee. "
                f"Your {platform_display} integration is working correctly!"
            )

            if platform == "slack":
                return self._send_test_slack(messaging_platform, channel_id, test_message)
            elif platform == "ms_teams":
                return self._send_test_ms_teams(messaging_platform, channel_id, team_id, test_message, tenant_id)
            elif platform == "google_chat":
                return self._send_test_google_chat(messaging_platform, channel_id, test_message, tenant_id)

        except Exception as e:
            LOG.exception("Error sending test notification on %s: %s", platform, e)
            return {"success": False, "platform": platform, "error": f"Unexpected error: {str(e)}"}

    def _send_test_slack(self, messaging_platform, channel_id, message):
        try:
            response = self.slack_app.client.chat_post(
                token=messaging_platform.token,
                channel_id=channel_id,
                text=message,
                blocks=[{"type": "section", "text": {"type": "mrkdwn", "text": message}}],
            )
            if not response.get("ok"):
                error_msg = response.get("error", ERR_UNKNOWN)
                return {"success": False, "platform": "slack", "error": error_msg}
            return {"success": True, "platform": "slack"}
        except SlackApiError as e:
            error_msg = e.response.get("error", str(e))
            LOG.error("Slack API error sending test notification: %s", error_msg)
            return {"success": False, "platform": "slack", "error": error_msg}

    def _send_test_ms_teams(self, messaging_platform, channel_id, team_id, message, tenant_id):
        error = self._refresh_ms_teams_token(messaging_platform)
        if error:
            return {"success": False, "platform": "ms_teams", "error": error}

        channels_config = {"team_id": team_id, "channels": [{"id": channel_id}]}
        adaptive_card = {
            ADAPTIVE_CARD_SCHEMA_KEY: ADAPTIVE_CARD_SCHEMA_URL,
            "type": "AdaptiveCard",
            "version": "1.4",
            "body": [{"type": "TextBlock", "text": message, "wrap": True}],
        }
        try:
            resp = MsTeamsClient.post_to_ms_teams(
                access_token=messaging_platform.token,
                ms_teams_channels=channels_config,
                message=adaptive_card,
                tenant=tenant_id,
            )
            if resp and resp.status_code < 400:
                return {"success": True, "platform": "ms_teams"}
            error_detail = resp.text if resp else "No response"
            return {"success": False, "platform": "ms_teams", "error": error_detail}
        except Exception as e:
            LOG.error("MS Teams test notification error: %s", e)
            return {"success": False, "platform": "ms_teams", "error": str(e)}

    def _send_test_google_chat(self, messaging_platform, channel_id, message, tenant_id):
        error = self._refresh_google_chat_token(messaging_platform)
        if error:
            return {"success": False, "platform": "google_chat", "error": error}

        try:
            result = GoogleChatClient.post_to_google_chat(
                token=messaging_platform.token,
                space=channel_id,
                message=message,
                tenant=tenant_id,
            )
            if result and result.get("success"):
                return {"success": True, "platform": "google_chat"}
            return {"success": False, "platform": "google_chat", "error": "Failed to post message"}
        except Exception as e:
            LOG.error("Google Chat test notification error: %s", e)
            return {"success": False, "platform": "google_chat", "error": str(e)}

    async def teams_send_welcome_message(self, activity: Activity) -> bool:
        """Send welcome message when bot is added to a Teams conversation."""
        card = self.create_teams_welcome_card()
        return await self.teams_reply_with_card(activity, card, None)

    # ==================== Google Chat Bot Messaging Methods ====================

    def get_google_chat_installation(self, tenant_id):
        """Look up MessagingPlatform for google_chat and refresh token if needed."""
        messaging_platform = (
            self.session.query(MessagingPlatform)
            .filter(MessagingPlatform.tenant_id == tenant_id, MessagingPlatform.platform == "google_chat")
            .one_or_none()
        )
        if not messaging_platform:
            return None
        error = self._refresh_google_chat_token(messaging_platform)
        if error:
            return None
        return messaging_platform

    def gchat_reply_in_thread(self, space_name, thread_name, message, tenant_id):
        """Post a text reply in a Google Chat thread."""
        messaging_platform = self.get_google_chat_installation(tenant_id)
        if not messaging_platform:
            LOG.error("No Google Chat installation found for tenant %s", tenant_id)
            return None
        return GoogleChatClient.post_to_google_chat(
            token=messaging_platform.token,
            space=space_name,
            message=message,
            tenant=tenant_id,
            thread_name=thread_name,
        )

    def gchat_reply_with_card(self, space_name, thread_name, card, tenant_id):
        """Post a Cards v2 message in a Google Chat thread."""
        messaging_platform = self.get_google_chat_installation(tenant_id)
        if not messaging_platform:
            LOG.error("No Google Chat installation found for tenant %s", tenant_id)
            return None
        return GoogleChatClient.post_to_google_chat(
            token=messaging_platform.token,
            space=space_name,
            message=card,
            tenant=tenant_id,
            thread_name=thread_name,
        )

    @staticmethod
    def create_gchat_cluster_selection_card(valid_accounts):
        """Build a Cards v2 payload for account selection (like create_teams_cluster_selection_card)."""
        from notifications_server.services.bot_messages import get_account_selection_prompt

        buttons = [
            {
                "text": acc["account_name"],
                "onClick": {
                    "action": {
                        "function": "select_account",
                        "parameters": [{"key": "account_id", "value": acc["id"]}],
                    }
                },
            }
            for acc in valid_accounts
        ]

        return {
            "cardsV2": [
                {
                    "cardId": "account-selection",
                    "card": {
                        "header": {"title": "Select Account"},
                        "sections": [
                            {
                                "widgets": [
                                    {"textParagraph": {"text": get_account_selection_prompt()}},
                                    {"buttonList": {"buttons": buttons}},
                                ]
                            }
                        ],
                    },
                }
            ]
        }

    @staticmethod
    def create_gchat_followup_card(question, followup_options):
        """Build a Cards v2 payload for follow-up options (like create_teams_followup_card)."""
        buttons = [
            {
                "text": option,
                "onClick": {
                    "action": {
                        "function": "select_followup",
                        "parameters": [{"key": "option", "value": option}],
                    }
                },
            }
            for option in followup_options
        ]

        return {
            "cardsV2": [
                {
                    "cardId": "followup-options",
                    "card": {
                        "sections": [
                            {
                                "widgets": [
                                    {"textParagraph": {"text": question}},
                                    {"buttonList": {"buttons": buttons}},
                                ]
                            }
                        ],
                    },
                }
            ]
        }

    @staticmethod
    def create_gchat_welcome_card():
        """Build a Cards v2 welcome card for ADDED_TO_SPACE events."""
        from notifications_server.services.bot_messages import SIGNUP_URL

        branding_name = settings.urls.branding_name
        branding_logo = settings.urls.branding_logo_url

        return {
            "cardsV2": [
                {
                    "cardId": "welcome-card",
                    "card": {
                        "header": {
                            "title": f"Welcome to {branding_name}!",
                            "imageUrl": branding_logo,
                            "imageType": "CIRCLE",
                        },
                        "sections": [
                            {
                                "widgets": [
                                    {
                                        "textParagraph": {
                                            "text": (
                                                f"Hi there! I'm your {branding_name} assistant."
                                                " Here's what I can help you with:"
                                            )
                                        }
                                    },
                                    {
                                        "textParagraph": {
                                            "text": (
                                                "- <b>Event Analysis</b> — investigate and analyze"
                                                " infrastructure events\n"
                                                "- <b>Optimization Recommendations</b> — get actionable"
                                                " cost and performance insights\n"
                                                "- <b>Infrastructure Q&A</b> — ask me anything about"
                                                " your clusters and services"
                                            )
                                        }
                                    },
                                    {
                                        "textParagraph": {
                                            "text": (
                                                f'Don\'t have an account yet? <a href="{SIGNUP_URL}">'
                                                "Sign up here</a>"
                                            )
                                        }
                                    },
                                    {
                                        "buttonList": {
                                            "buttons": [
                                                {
                                                    "text": "Sign Up",
                                                    "onClick": {"openLink": {"url": SIGNUP_URL}},
                                                }
                                            ]
                                        }
                                    },
                                ]
                            }
                        ],
                    },
                }
            ]
        }
