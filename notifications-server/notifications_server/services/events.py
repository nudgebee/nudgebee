import asyncio
import json
import logging
import re
from typing import Any

import aiohttp
import requests
from datetime import datetime

from botbuilder.core import TurnContext
from botbuilder.schema import Activity

from sqlalchemy.orm import Session

from notifications_server.configs import settings
from notifications_server.configs.settings import ACCOUNT_SECURITY_CONTEXT, LLM_CHAT_ENDPOINT
from notifications_server.message_templates.blocks import MarkdownBlock, ContextBlock
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.models.models import ChannelAccountMapping, MessagingPlatform
from notifications_server.services.audit import create_audit_request
from notifications_server.services.cache import Cache
from notifications_server.services.actions import (
    validate_and_get_user_tenants,
    CLUSTER_NOT_FOUND,
)
from notifications_server.services.bot_messages import (
    get_account_selection_prompt,
    get_account_selected_confirmation,
    get_account_selected_with_context,
    get_account_already_selected,
    get_account_not_accessible_message,
    get_user_not_found_message,
    get_llm_busy_message,
    get_llm_offline_message,
    get_session_expired_message,
    get_empty_message_response,
    get_followup_confirmation,
    get_followup_selection_confirmation,
    get_processing_confirmation,
    get_feedback_thanks_message,
    get_exit_message,
    is_conversation_in_progress_error,
    is_budget_exceeded_error,
    get_budget_exceeded_message,
)
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.repositories.account_repository import (
    get_active_accounts_with_connected_agents,
    get_account_by_id,
)
from notifications_server.repositories.user_repository import get_llm_conversation_by_session
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)
event_cache = Cache()

# Constants
MAGNIFYING_GLASS_REACTION_ERROR = "Failed to add magnifying glass reaction: %s"
SLACK_TEXT_BLOCK_LIMIT = settings.slack.text_block_limit
FOLLOWUP_OPTIONS_THRESHOLD = settings.slack.followup_options_threshold
# Slack static_select hard-caps at 100 options; reject-payload territory beyond that.
SLACK_STATIC_SELECT_MAX_OPTIONS = 100
_SENTENCE_PATTERN = re.compile(r"(?<=[.!?])\s+")
_DELIMITER_PATTERN = re.compile(r"([,;:])\s+")


def _parse_iso(iso_str):
    if isinstance(iso_str, str):
        return datetime.fromisoformat(iso_str)
    return iso_str  # already datetime


class LLMServerError(aiohttp.ClientError):
    """
    Raised by the async LLM-server call when the server responds with a non-2xx
    status. Unlike aiohttp's ClientResponseError, this carries the parsed JSON
    error body so callers can surface specific failures (e.g. budget exceeded).
    Subclasses aiohttp.ClientError so existing `except aiohttp.ClientError`
    handlers keep catching it.
    """

    def __init__(self, status, error_response=None):
        self.status = status
        self.error_response = error_response
        super().__init__(f"LLM Server returned status {status}: {error_response}")


class Events:
    def __init__(self, common_service, slack_app, teams_app, db_session):
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.session = db_session
        self.cache = event_cache
        self.common_service = common_service

    @staticmethod
    def _session_id(channel_id, thread_ts):
        return f"{channel_id}-{thread_ts}"

    @staticmethod
    def _teams_session_id(conversation_id, message_id):
        """Generate unique session ID for Teams conversations using conversation_id and first message_id"""
        return f"{conversation_id}-_-{message_id}"

    @staticmethod
    def _has_pending_text_followup(cached_entry) -> bool:
        # A free-text follow-up is "pending" when the previous turn rendered a
        # follow-up question (followup_msg_ts set) and the LLM is awaiting a
        # response keyed by agent_id/message_id. Both the button-click and the
        # @mention text-reply paths route through _submit_followup using these
        # keys; missing any of them means the next mention should start a fresh
        # chat turn instead.
        if not cached_entry:
            return False
        return all(cached_entry.get(k) for k in ("followup_msg_ts", "agent_id", "message_id"))

    @staticmethod
    def clean_slack_text(text: str) -> str:
        return re.sub(
            r"<mailto:([^|>]+)\|[^>]+>|<[^>]+>", lambda m: m.group(1) if m.group(1) else "", text or ""
        ).strip()

    def safe_add_reaction(self, channel_id, team_id, ts, reaction="mag"):
        try:
            self.common_service.add_slack_reactions(channel_id, team_id, ts, reaction)
        except Exception as e:
            LOG.debug(MAGNIFYING_GLASS_REACTION_ERROR, e)

    def reply(self, channel_id, team_id, thread_ts, message):
        try:
            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, message)
        except Exception as e:
            LOG.error("Failed to send Slack message: %s", e)

    @staticmethod
    def build_blocks(message, buttons=None, user_id=None):
        blocks = [{"type": "section", "text": {"type": "mrkdwn", "text": message}}]
        if buttons:
            blocks.append({"type": "actions", "elements": buttons})
        if user_id:
            blocks += Transformer.to_slack(ContextBlock(text=f"<@{user_id}>"))
        return blocks

    @staticmethod
    def build_llm_payload(cached_entry, query_override=None):
        return {
            "query": query_override or cached_entry["text"],
            "account_id": cached_entry["account_id"],
            "user_id": cached_entry["user_id"],
            "session_id": cached_entry["session_id"],
            "source": "InstantNotification",
            "async": True,
        }

    @staticmethod
    def _validate_context_response_for_account_ids(context_account_ids, response):
        if isinstance(response, dict) and "context" in response and isinstance(response["context"], dict):
            account_ids = response["context"].get("AccountIds", [])
            if account_ids and isinstance(account_ids, list):
                context_account_ids.update(account_ids)
        else:
            logging.warning("Unexpected response structure for context API while fetching AccountIds")

    def execute_event(
        self, team_id, event_id, event_context, user_email, channel_id, thread_ts, event_ts, event, slack_user_id
    ):
        try:
            self.safe_add_reaction(channel_id, team_id, event_ts, "mag")

            text = self.clean_slack_text(event.get("text", ""))
            if not text or text == " ":
                self._reply_error(
                    channel_id,
                    team_id,
                    thread_ts,
                    get_empty_message_response(),
                )
                return

            cached_entry = self.cache.get_event_entry(thread_ts)

            if cached_entry:
                if not cached_entry.get("user_id"):
                    user_id, _ = validate_and_get_user_tenants(user_email)
                    self.cache.update_event_entry(thread_ts, user_id=user_id)
                    cached_entry["user_id"] = user_id

                if self._has_pending_text_followup(cached_entry):
                    self._submit_followup(cached_entry, channel_id, team_id, thread_ts, slack_user_id, text)
                    return

                self._process_event(channel_id, text, team_id, thread_ts, "chat")
                return

            if self._check_history_for_conversation(
                channel_id, text, event_context, event_id, team_id, thread_ts, user_email, slack_user_id
            ):
                return

            is_thread_request = thread_ts != event_ts
            LOG.debug(f"New conversation {text} with {thread_ts}")

            self._handle_new_conversation(
                channel_id,
                text,
                event_context,
                event_id,
                team_id,
                thread_ts,
                user_email,
                is_thread_request,
                slack_user_id,
            )

        except Exception as e:
            LOG.error("Error executing event: %s", e)

    def _check_history_for_conversation(
        self, channel_id, text, context, event_id, team_id, thread_ts, user_email, slack_user_id
    ):
        session_id = self._session_id(channel_id, thread_ts)
        with Session(self.session.get_bind()) as session:
            conversation = get_llm_conversation_by_session(session, session_id)
        if not conversation:
            return False

        user_id, _ = validate_and_get_user_tenants(user_email)

        self.cache.cache_event_entry(
            thread_ts,
            {
                "event_id": event_id,
                "event_context": context,
                "text": text,
                "user_id": user_id,
                "account_id": conversation["account_id"],
                "tenant_id": conversation["tenant_id"],
                "slack_user_id": slack_user_id,
                "channel_id": channel_id,
                "team_id": team_id,
                "session_id": session_id,
                "platform": "slack",
            },
        )

        self._process_event(channel_id, text, team_id, thread_ts, "chat")
        return True

    def _handle_new_conversation(
        self, channel_id, text, context, event_id, team_id, thread_ts, user_email, is_thread, slack_user_id
    ):
        try:
            user_id, _ = validate_and_get_user_tenants(user_email)

            # Check pre-existing session created during /channels/join
            incident_channel_session = self.cache.get_channel_session_mapping(channel_id, team_id)
            has_session = incident_channel_session and incident_channel_session.get("session_id")

            if has_session:
                return self._process_incident_channel_session_event(
                    channel_id,
                    context,
                    event_id,
                    incident_channel_session,
                    is_thread,
                    slack_user_id,
                    team_id,
                    text,
                    thread_ts,
                    user_email,
                    user_id,
                )

            session_id = self._session_id(channel_id, thread_ts)
            LOG.info(f"Generated new session_id: {session_id} for channel: {channel_id}")

            event_entry = {
                "event_id": event_id,
                "event_context": context,
                "text": text,
                "user_id": user_id,
                "is_thread": is_thread,
                "slack_user_id": slack_user_id,
                "channel_id": channel_id,
                "team_id": team_id,
                "session_id": session_id,
                "platform": "slack",
            }
            self.cache.cache_event_entry(thread_ts, event_entry)
            self._request_cluster_confirmation(channel_id, team_id, thread_ts, user_email, slack_user_id)

        except Exception as e:
            LOG.error("Failed to handle new chat event: %s", e, exc_info=True)

    def _process_incident_channel_session_event(
        self,
        channel_id,
        context,
        event_id,
        incident_channel_session: dict[str, Any] | None,
        is_thread,
        slack_user_id,
        team_id,
        text,
        thread_ts,
        user_email,
        user_id: str | None,
    ):
        session_id = incident_channel_session["session_id"]
        LOG.debug(f"Using pre-existing session_id from /channels/join: {session_id} for channel: {channel_id}")

        existing_entry = self.cache.get_event_entry(session_id)

        if existing_entry:
            LOG.debug(f"Reusing cached event entry from /channels/join for session: {session_id}")

            # Update existing entry
            self.cache.update_event_entry(
                session_id,
                event_id=event_id,
                event_context=context,
                text=text,
                user_id=user_id,
                is_thread=is_thread,
                slack_user_id=slack_user_id,
                session_id=f"event-{session_id}",
            )

            # Cache updated entry under thread_ts
            updated_entry = self.cache.get_event_entry(session_id)
            if updated_entry:
                self.cache.cache_event_entry(thread_ts, updated_entry)

            # If account/tenant is known → process event
            if existing_entry.get("account_id") and existing_entry.get("tenant_id"):
                url = (
                    f"{settings.base_url}/ask-nudgebee?accountId={existing_entry['account_id']}"
                    f"&session_id=event-{session_id}"
                )
                self.common_service.slack_reply_in_thread_with_context(
                    channel_id,
                    team_id,
                    thread_ts,
                    "Got it! Let me look into that for you.",
                    f"<@{slack_user_id}> \t <{url}|{settings.base_url}>",
                )
                self._process_event(channel_id, text, team_id, thread_ts, "chat")
            else:
                # Account/tenant unknown → ask cluster confirmation
                self._request_cluster_confirmation(channel_id, team_id, thread_ts, user_email, slack_user_id)

            return

        # No existing entry for this session_id → create one
        event_entry = {
            "event_id": event_id,
            "event_context": context,
            "text": text,
            "user_id": user_id,
            "is_thread": is_thread,
            "slack_user_id": slack_user_id,
            "channel_id": channel_id,
            "team_id": team_id,
            "session_id": session_id,
            "platform": "slack",
        }
        self.cache.cache_event_entry(thread_ts, event_entry)
        self._request_cluster_confirmation(channel_id, team_id, thread_ts, user_email, slack_user_id)

    def _request_cluster_confirmation(self, channel_id, team_id, thread_ts, user_email, slack_user_id=None):
        try:
            user_id, tenants = validate_and_get_user_tenants(user_email)
            if not user_id or not tenants:
                LOG.warning("No account found for user email: %s", user_email)
                self._reply_error(
                    channel_id,
                    team_id,
                    thread_ts,
                    get_user_not_found_message(),
                )
                return

            mapped_account = self._resolve_mapped_account("slack", channel_id, user_id, tenants)
            if mapped_account:
                self._handle_selected_account(mapped_account, channel_id, team_id, thread_ts)
                return

            valid_accounts = self._get_valid_accounts(user_id, tenants)

            if len(valid_accounts) == 1:
                self._handle_selected_account(valid_accounts[0], channel_id, team_id, thread_ts)
                return

            # Get user's display name for personalized greeting
            user_display_name = None
            if slack_user_id:
                user_display_name = self.common_service.get_slack_user_display_name(team_id, slack_user_id)

            blocks, selection_prompt = self._get_cluster_confirmation_blocks(valid_accounts, user_display_name)

            selection_msg_ts = self.common_service.slack_reply_in_thread_as_blocks(
                channel_id, team_id, thread_ts, blocks
            )

            # Store the selection message timestamp and prompt for later update
            if selection_msg_ts:
                self.cache.update_event_entry(
                    thread_ts, selection_msg_ts=selection_msg_ts, selection_prompt=selection_prompt
                )

        except Exception as e:
            LOG.error("Failed to request cluster confirmation: %s", e, exc_info=True)

    def _resolve_mapped_account(self, platform, channel_id, user_id, tenants):
        for tenant_id in tenants:
            mapping = self._get_channel_account_mapping(tenant_id, platform, channel_id)
            if not mapping:
                continue

            if self._validate_user_access_to_account(user_id, tenant_id, str(mapping.account_id)):
                LOG.debug(f"Using channel mapping: channel={channel_id} -> account={mapping.account_id}")
                return {"id": str(mapping.account_id)}

            LOG.warning(f"User {user_id} does not have access to mapped account {mapping.account_id}")

        return None

    async def _resolve_mapped_account_async(self, platform, channel_id, user_id, tenants):
        for tenant_id in tenants:
            mapping = self._get_channel_account_mapping(tenant_id, platform, channel_id)
            if not mapping:
                continue

            if await self._validate_user_access_to_account_async(user_id, tenant_id, str(mapping.account_id)):
                LOG.debug(f"Using channel mapping: channel={channel_id} -> account={mapping.account_id}")
                return {"id": str(mapping.account_id)}

            LOG.warning(f"User {user_id} does not have access to mapped account {mapping.account_id}")

        return None

    def _get_valid_accounts(self, user_id, tenants):
        with Session(self.session.get_bind()) as session:
            cloud_accounts = get_active_accounts_with_connected_agents(session, tenants)
        context_account_ids = self._get_context_account_ids(user_id, tenants)
        return [acc for acc in cloud_accounts if acc["id"] in context_account_ids]

    async def _get_valid_accounts_async(self, user_id, tenants):
        with Session(self.session.get_bind()) as session:
            cloud_accounts = get_active_accounts_with_connected_agents(session, tenants)
        context_account_ids = await self._get_context_account_ids_async(user_id, tenants)
        return [acc for acc in cloud_accounts if acc["id"] in context_account_ids]

    async def _get_context_account_ids_async(self, user_id, tenants):
        context_ids = set()

        async def fetch_context_for_tenant(tenant):
            try:
                url = settings.services.api_server + ACCOUNT_SECURITY_CONTEXT
                payload = {"user_id": user_id, "tenant_id": tenant}
                headers = {"X-ACTION-TOKEN": settings.action_api_server_token}

                async with aiohttp.ClientSession() as session:
                    async with session.post(url, json=payload, headers=headers) as response:
                        response.raise_for_status()
                        data = await response.json()
                        return data
            except Exception as e:
                LOG.warning("Failed to fetch context for tenant %s: %s", tenant, e)
                return None

        results = await asyncio.gather(
            *[fetch_context_for_tenant(tenant) for tenant in tenants], return_exceptions=True
        )

        for result in results:
            if result and not isinstance(result, Exception):
                self._validate_context_response_for_account_ids(context_ids, result)

        return context_ids

    def _get_context_account_ids(self, user_id, tenants):
        return asyncio.run(self._get_context_account_ids_async(user_id, tenants))

    def _handle_selected_account(self, account, channel_id, team_id, thread_ts):
        account_name, account_id = self._fetch_and_update_account_details_by_id(
            account["id"], channel_id, team_id, thread_ts
        )
        if not (account_name and account_id):
            return

        cached_entry = self.cache.get_event_entry(thread_ts)
        if not cached_entry:
            self.reply(
                channel_id,
                team_id,
                thread_ts,
                get_session_expired_message(),
            )
            return

        url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={channel_id}-{thread_ts}"
        confirmation_message = (
            f"{get_account_selected_confirmation(account_name)}\n<{url}|View in {settings.urls.branding_name}>"
        )

        self.common_service.slack_reply_in_thread(
            channel_id,
            team_id,
            thread_ts,
            confirmation_message,
        )

        self._process_event(channel_id, cached_entry["text"], team_id, thread_ts, "chat")

    def _get_channel_account_mapping(self, tenant_id, platform, channel_id):
        try:
            with Session(self.session.get_bind()) as session:
                mapping = (
                    session.query(ChannelAccountMapping)
                    .filter_by(
                        tenant_id=tenant_id,
                        platform=platform,
                        channel_id=channel_id,
                    )
                    .first()
                )
                return mapping
        except Exception as e:
            LOG.warning(f"Failed to fetch channel mapping: {e}")
            return None

    def _validate_user_access_to_account(self, user_id, tenant_id, account_id):
        try:
            context_account_ids = self._get_context_account_ids(user_id, [tenant_id])
            return account_id in context_account_ids
        except Exception as e:
            LOG.warning(f"Failed to validate user access to account {account_id}: {e}")
            return False

    async def _validate_user_access_to_account_async(self, user_id, tenant_id, account_id):
        try:
            context_account_ids = await self._get_context_account_ids_async(user_id, [tenant_id])
            return account_id in context_account_ids
        except Exception as e:
            LOG.warning(f"Failed to validate user access to account {account_id}: {e}")
            return False

    def _get_cluster_confirmation_blocks(self, valid_accounts, user_name=None):
        """Returns (blocks, prompt_message). prompt_message is None when no accounts are available."""
        if valid_accounts:
            prompt_message = get_account_selection_prompt(user_name)

            # Use dropdown for more than 2 options, buttons for 2 or fewer
            if len(valid_accounts) > 2:
                dropdown = {
                    "type": "static_select",
                    "placeholder": {"type": "plain_text", "text": "Select an account..."},
                    "action_id": "select_account_dropdown",
                    "options": [
                        {
                            "text": {"type": "plain_text", "text": acc["account_name"]},
                            "value": acc["id"],
                        }
                        for acc in valid_accounts
                    ],
                }
                return self.build_blocks(prompt_message, [dropdown]), prompt_message
            else:
                buttons = [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": acc["account_name"]},
                        "value": acc["account_name"],
                        "action_id": f"select_cluster_option--{acc['id']}",
                    }
                    for acc in valid_accounts
                ]
                return self.build_blocks(prompt_message, buttons), prompt_message
        else:
            return self.build_blocks(get_account_not_accessible_message()), None

    def update_account_for_event(self, action_id, channel_id, team_id, slack_user_id, thread_ts):
        try:
            cached_entry = self.cache.get_event_entry(thread_ts)
            if cached_entry and cached_entry.get("account_id") is not None:
                self.common_service.slack_reply_in_thread_with_context(
                    channel_id,
                    team_id,
                    thread_ts,
                    get_account_already_selected(),
                    f"<@{slack_user_id}>",
                )
                return

            account_id = action_id.split("--")[1]
            account_name, account_id = self._fetch_and_update_account_details_by_id(
                account_id, channel_id, team_id, thread_ts
            )

            # Update the selection message instead of sending a new one
            selection_msg_ts = cached_entry.get("selection_msg_ts") if cached_entry else None
            selection_prompt = cached_entry.get("selection_prompt") if cached_entry else None
            url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={channel_id}-{thread_ts}"

            if selection_prompt:
                body = get_account_selected_with_context(selection_prompt, account_name)
            else:
                body = get_account_selected_confirmation(account_name)
            confirmation_message = f"{body}\n<{url}|View in {settings.urls.branding_name}>"

            if selection_msg_ts:
                # Replace the selection message with confirmation
                self.common_service.update_slack_message(
                    channel_id,
                    team_id,
                    selection_msg_ts,
                    confirmation_message,
                )
            else:
                # Fallback: send new message if we don't have the original ts
                self.common_service.slack_reply_in_thread(
                    channel_id,
                    team_id,
                    thread_ts,
                    confirmation_message,
                )

            self._process_event(channel_id, self.cache.get_event_entry(thread_ts)["text"], team_id, thread_ts, "chat")
        except Exception as e:
            LOG.error("Failed to update account for event: %s", e)
            self._reply_error(
                channel_id, team_id, thread_ts, "Hmm, couldn't connect to the account. Try again in a bit?"
            )

    def update_followup_for_event(self, action_data, channel_id, team_id, slack_user_id, thread_ts):
        try:
            action_id = action_data.get("action_id", "")
            if "selected_option" in action_data:
                response_option = action_data["selected_option"]["value"]
            else:
                response_option = action_id.split("--")[1]
            cached_entry = self.cache.get_event_entry(thread_ts)
            self._submit_followup(cached_entry, channel_id, team_id, thread_ts, slack_user_id, response_option)
        except Exception as e:
            LOG.error("Failed to update followup: %s", e)
            self._reply_error(channel_id, team_id, thread_ts, "Hmm, I couldn't process that. Mind trying again?")

    def _submit_followup(self, cached_entry, channel_id, team_id, thread_ts, slack_user_id, response_option):
        if not cached_entry:
            self.reply(channel_id, team_id, thread_ts, get_session_expired_message())
            return

        payload, headers = self._get_llm_request_payload(cached_entry, channel_id, thread_ts)
        payload.update(
            {
                "query": response_option,
                "agent_id": cached_entry.get("agent_id"),
                "message_id": cached_entry.get("message_id"),
            }
        )

        followup_msg_ts = cached_entry.get("followup_msg_ts") if cached_entry else None
        followup_question = cached_entry.get("followup_question") if cached_entry else None

        if followup_msg_ts and followup_question:
            replacement_message = get_followup_selection_confirmation(followup_question, response_option)
        else:
            replacement_message = get_followup_confirmation(response_option)

        if followup_msg_ts:
            confirmation_blocks = Transformer.to_slack(MarkdownBlock(text=replacement_message))
            confirmation_blocks += Transformer.to_slack(ContextBlock(text=f"<@{slack_user_id}>"))
            updated = self.common_service.update_slack_message_with_blocks(
                channel_id, team_id, followup_msg_ts, confirmation_blocks
            )
            if not updated:
                self.common_service.slack_reply_in_thread_with_context(
                    channel_id, team_id, thread_ts, replacement_message, f"<@{slack_user_id}>"
                )
        else:
            self.common_service.slack_reply_in_thread_with_context(
                channel_id, team_id, thread_ts, replacement_message, f"<@{slack_user_id}>"
            )

        # Once the follow-up is consumed, clear all four pending-followup keys so
        # the next @mention in this thread is treated as a fresh turn rather than
        # being re-routed to the same agent_id/message_id.
        self.cache.remove_event_keys(
            thread_ts,
            ["followup_msg_ts", "followup_question", "agent_id", "message_id"],
        )

        self.query_llm_server(payload, headers)

    def _fetch_and_update_account_details_by_id(self, account_id, channel_id, team_id, thread_ts):
        with Session(self.session.get_bind()) as session:
            account = get_account_by_id(session, account_id)

        if not account:
            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, CLUSTER_NOT_FOUND)
            return None, None

        self.cache.update_event_entry(thread_ts, tenant_id=account.get("tenant"), account_id=account.get("id"))
        return account.get("account_name"), account.get("id")

    def _process_event(self, channel_id, cleaned_string, team_id, thread_ts, _type):
        cached_entry = self.cache.get_event_entry(thread_ts)
        if not cached_entry:
            self.reply(channel_id, team_id, thread_ts, get_session_expired_message())
            return

        # If it's a thread: fetch full conversation and update cache
        if cached_entry.get("is_thread"):
            conversation, sent_messages = self.common_service.get_slack_conversation(
                channel_id, thread_ts, team_id, cached_entry
            )

            if not conversation:
                self.reply(channel_id, team_id, thread_ts, "Hmm, I couldn't fetch the conversation. Mind trying again?")
                return

            self.cache.update_event_entry(thread_ts, text=conversation, sent_messages=sent_messages)
            cleaned_string = conversation
            cached_entry = self.cache.get_event_entry(thread_ts)

        payload = self.build_llm_payload(cached_entry, query_override=cleaned_string)

        headers = {
            "x-tenant-id": cached_entry["tenant_id"],
            "x-user-id": cached_entry["user_id"],
        }

        try:
            self.query_llm_server(payload, headers)
            self._log_event_audit(cached_entry, cleaned_string)
        except requests.RequestException as e:
            LOG.debug(f"Query to LLM failed: {e}")
            status = getattr(getattr(e, "response", None), "status_code", None)
            error_response = self._safe_error_response(e)
            self.common_service.slack_reply_in_thread(
                channel_id, team_id, thread_ts, self._llm_error_reply(status, error_response)
            )
        except Exception as e:
            LOG.debug(f"Query to LLM failed: {e}")
            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, get_llm_offline_message())

    @staticmethod
    def _log_event_audit(entry, text):
        create_audit_request(
            user_id=entry["user_id"],
            tenant_id=entry["tenant_id"],
            account_id=entry["account_id"],
            event_time=utc_now(),
            event_category=EventCategory.CHAT_ACTIONS.value,
            event_type=EventType.SLACK_EVENT.value,
            event_prev_state=None,
            event_state={"text": text},
            event_actor=EventActor.SLACK.value,
            event_target=entry["account_id"],
            event_action=EventAction.READ.value,
            event_status=EventStatus.SUCCESS.value,
            event_attr={"text": text},
        )

    @staticmethod
    def _get_llm_request_payload(cached_entry, channel_id, thread_ts):
        payload = {
            "query": cached_entry["text"],
            "account_id": cached_entry["account_id"],
            "user_id": cached_entry["user_id"],
            "session_id": f"{channel_id}-{thread_ts}",
            "source": "InstantNotification",
            "async": True,
        }
        headers = {"x-tenant-id": cached_entry["tenant_id"], "x-user-id": cached_entry["user_id"]}
        return payload, headers

    @staticmethod
    def _with_llm_auth(headers):
        return {**headers, settings.llm_server_token_header: settings.llm_server_token}

    @staticmethod
    def query_llm_server(payload, headers):
        result = None
        try:
            LOG.info("Sending request to LLM Server: %s", payload)
            url = settings.services.llm_server + LLM_CHAT_ENDPOINT
            result = requests.post(url, headers=Events._with_llm_auth(headers), json=payload)
            result.raise_for_status()
        except requests.RequestException as e:
            LOG.error(
                "Failed to send request to LLM Server: %s, Response: %s",
                e,
                result.json() if result is not None else "No response",
            )
            raise
        except Exception as e:
            LOG.error("Unexpected error processing LLM response: %s", e)

    @staticmethod
    async def async_query_llm_server(payload, headers):
        try:
            LOG.info("Sending async request to LLM Server: %s", payload)
            url = settings.services.llm_server + LLM_CHAT_ENDPOINT
            async with aiohttp.ClientSession() as session:
                async with session.post(url, headers=Events._with_llm_auth(headers), json=payload) as response:
                    if response.status >= 400:
                        error_response = None
                        try:
                            error_response = await response.json()
                        except Exception:
                            error_response = None
                        LOG.error(
                            "Failed to send async request to LLM Server: status=%s, Response: %s",
                            response.status,
                            error_response if error_response is not None else "No response",
                        )
                        raise LLMServerError(response.status, error_response)
        except aiohttp.ClientError as e:
            LOG.error("Failed to send async request to LLM Server: %s", e)
            raise
        except Exception as e:
            LOG.error("Unexpected error processing async LLM response: %s", e)

    @staticmethod
    def _llm_error_reply(status, error_response):
        """
        Pick the user-facing reply for a failed LLM-server request.

        Budget exhaustion is detected primarily by HTTP status (429), mirroring
        the web app's ``mapUpstreamError``; the body sniff is a fallback for when
        the status code is unavailable. Otherwise: a busy notice for an
        in-progress conversation, else the generic offline fallback.
        """
        if is_conversation_in_progress_error(error_response):
            return get_llm_busy_message()
        if status == 429 or is_budget_exceeded_error(error_response):
            return get_budget_exceeded_message()
        return get_llm_offline_message()

    @staticmethod
    def _safe_error_response(e):
        """
        Best-effort parse of an LLM-server error body from a RequestException.

        Returns None when there is no response or the body is not valid JSON
        (e.g. an HTML error page from a proxy/load balancer on a 502/504). This
        must not raise: it runs inside an ``except`` block, and a JSONDecodeError
        here would escape past the outer ``except Exception`` fallback.
        """
        response = getattr(e, "response", None)
        if response is None:
            return None
        try:
            return response.json()
        except Exception:
            return None

    def update_llm_chat_feedback(self, payload, channel_id, team_id, thread_ts):
        try:
            cached_entry = self.cache.get_event_entry(thread_ts)
            if not cached_entry:
                return
            useful = payload.get("useful")
            if useful:
                notes = "User liked the Response"
            else:
                notes = "User disliked the Response"

            tenant_id = cached_entry.get("tenant_id")
            user_id = cached_entry.get("user_id")
            if not tenant_id or not user_id:
                # Without these, services-server rejects the request as unauthorized
                # (empty tenant/user can't pass HasAccountAccess). Bail before the POST
                # so we don't generate 4xx noise on a payload we know will be refused.
                LOG.warning("ai_feedback skipped: missing tenant_id or user_id in cached entry")
                return
            # AiFeedbackCreateRequest is a flat input type; do NOT wrap under "data".
            # (The earlier RPC mutation passed feedback_object={data:{...}} as the
            # $object variable, which was a long-standing schema mismatch — that path
            # silently no-op'd because RPC rejected it. Going direct gives us the
            # opportunity to send the right shape.)
            feedback_request = {
                "session_id": self._session_id(channel_id, thread_ts),
                "module": "new-investigation",
                "question": "",
                "llm_response": "",
                "user_corrected_response": "",
                "additional_notes": notes,
                "conversation_id": cached_entry["conversation_id"],
                "cloud_account_id": cached_entry["account_id"],
                "useful": useful,
            }
            # Post directly to services-server using the standard action envelope.
            # Matches the handler at api-server/services/api/actions_ai_feedback.go
            # (ai_feedback_create case), so we don't need a parallel REST endpoint
            # to be defined upstream.
            requests.post(
                settings.services.api_server + "/rpc/ai",
                json={
                    "action": {"name": "ai_feedback_create"},
                    "input": {"request": feedback_request},
                    "session_variables": {
                        "tenant_id": tenant_id,
                        "user_id": user_id,
                    },
                },
                headers={"X-ACTION-TOKEN": settings.action_api_server_token},
                timeout=10,
            )
            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, get_feedback_thanks_message())
        except Exception as e:
            LOG.error("Failed to update LLM chat feedback: %s", e)

    def cache_and_query_llm(self, payload, tenant_id, channel_id, team_id, thread_ts, slack_user_id):
        self.safe_add_reaction(channel_id, team_id, thread_ts, "mag")

        event_entry = {
            "event_id": payload.get("event_id"),
            "text": payload.get("query"),
            "user_id": payload.get("user_id"),
            "account_id": payload.get("account_id"),
            "tenant_id": tenant_id,
            "slack_user_id": slack_user_id,
            "channel_id": channel_id,
            "team_id": team_id,
            "session_id": self._session_id(channel_id, thread_ts),
            "platform": "slack",
        }

        try:
            self.cache.cache_event_entry(thread_ts=thread_ts, event_entry=event_entry)
            headers = {"x-tenant-id": tenant_id, "x-user-id": payload.get("user_id")}
            self.query_llm_server(payload, headers)
        except requests.RequestException as e:
            LOG.debug(f"Query to llm failed with {e}")
            status = getattr(getattr(e, "response", None), "status_code", None)
            error_response = self._safe_error_response(e)
            self.common_service.slack_reply_in_thread(
                channel_id, team_id, thread_ts, self._llm_error_reply(status, error_response)
            )
        except Exception as e:
            LOG.debug(f"Query to llm failed with {e}")
            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, get_llm_offline_message())

    @staticmethod
    def call_event_analysis_api(event_id, account_id, user_id, tenant_id):
        try:
            # Call LLM server's event analysis endpoint directly
            url = settings.services.llm_server + "/v1/analyze/event"

            payload = {
                "input": {
                    "event_id": event_id,
                    "account_id": account_id,
                    "user_id": user_id,
                    "source": "InstantNotification",
                    "regenerate": False,
                    "update_evidences": True,
                }
            }

            headers = Events._with_llm_auth(
                {"x-tenant-id": tenant_id, "x-user-id": user_id, "Content-Type": "application/json"}
            )

            LOG.info(f"Calling LLM server event analysis API: {url}")
            result = requests.post(url, headers=headers, json=payload)
            result.raise_for_status()

            response_data = result.json()
            return response_data.get("data", {})

        except Exception as e:
            LOG.error(f"Failed to call LLM server event analysis: {e}")
            raise

    def send_investigation_result_to_slack(self, result, channel_id, team_id, thread_ts, slack_user_id):
        try:
            analysis = result.get("analysis", "No analysis available")
            summary = result.get("summary", "")
            status = result.get("status", "UNKNOWN")

            if status == "COMPLETED":
                # Use summary if available, otherwise fall back to analysis
                content = summary if summary else analysis
                if content and content != "No analysis available":
                    message = f"Hey! Just wrapped up digging into this event for you 🔍\n\n{content}"
                else:
                    message = (
                        "Hey! Just finished looking into this event - didn't find much to report, but we've covered all"
                        " the bases."
                    )
            elif status == "IN_PROGRESS":
                message = "Still working on this one... hang tight!"
            elif status == "CREATED":
                message = "Just kicked off the investigation - I'll get back to you with what I find!"
            else:
                message = "Hmm, looks like something went sideways with the investigation. Mind if we try that again?"

            # Add user mention
            message = f"<@{slack_user_id}> {message}"

            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, message)

        except Exception as e:
            LOG.error("Failed to send investigation result to Slack %s", e)

            mention = f"<@{slack_user_id}>" if slack_user_id else "Hey"
            user_message = (
                f"{mention}! The investigation finished, but I couldn’t display the results. "
                "Please try again in a moment."
            )

            self.common_service.slack_reply_in_thread(
                channel_id=channel_id,
                team_id=team_id,
                thread_ts=thread_ts,
                message=user_message,
            )

    def _reply_error(self, channel_id, team_id, thread_ts, message):
        if not message:
            message = "Something went wrong. Please try again later."
        try:
            self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, f"{message}")
        except Exception as e:
            LOG.error("Failed to send error message to Slack: %s", e)

    @staticmethod
    def _add_user_context_to_blocks(output_blocks: list, cached_entry: dict) -> list:
        if cached_entry and cached_entry.get("slack_user_id"):
            LOG.debug(f"Adding user context block for slack_user_id: {cached_entry['slack_user_id']}")
            context_blocks = Transformer.to_slack(ContextBlock(text=f"<@{cached_entry['slack_user_id']}>"))
            output_blocks += context_blocks
        else:
            LOG.debug("No slack_user_id found; skipping user context block.")
        return output_blocks

    @staticmethod
    def split_text(text: str, max_len: int = SLACK_TEXT_BLOCK_LIMIT) -> list[str]:
        if len(text) <= max_len:
            return [text.strip()]

        sentences = re.split(_SENTENCE_PATTERN, text)
        chunks, current = [], ""

        for sentence in sentences:
            if len(sentence) > max_len:
                words = sentence.split()
                for word in words:
                    current = Events._split_chunk_on_words(chunks, current, max_len, word)
                continue

            current = Events._split_chunks(chunks, current, max_len, sentence)

        if current:
            chunks.append(current.strip())

        return chunks

    @staticmethod
    def _split_chunk_on_words(chunks, current, max_len, word):
        if len(word) > max_len:
            if current:
                chunks.append(current.strip())
            current = word
        else:
            current = Events._split_chunks(chunks, current, max_len, word)
        return current

    @staticmethod
    def _split_chunks(chunks, current, max_len, txt):
        if len(current) + len(txt) + 1 > max_len:
            if current:
                chunks.append(current.strip())
            current = txt
        else:
            current += (" " if current else "") + txt
        return current

    def handle_final_response(self, payload, cached_entry, channel_id: str, thread_ts: str, team_id: str):
        try:
            response_text = payload.response
            text_chunks = self.split_text(response_text)

            for i, chunk in enumerate(text_chunks):
                blocks = Transformer.to_slack(MarkdownBlock(text=chunk))

                if i == len(text_chunks) - 1 and cached_entry and cached_entry.get("slack_user_id"):
                    blocks += Transformer.to_slack(ContextBlock(text=f"<@{cached_entry.get('slack_user_id')}>"))

                self.common_service.slack_reply_in_thread(channel_id, team_id, thread_ts, blocks, False)

            if cached_entry:
                event_cache.update_event_entry(thread_ts, status="COMPLETED")
                LOG.debug("Conversation marked as COMPLETED.")

            if len(text_chunks) > 1:
                LOG.debug(f"Response was split into {len(text_chunks)} messages due to size limit")
        except Exception as e:
            LOG.error(f"Failed to process LLM server response: {e}")
            self.reply(
                channel_id,
                team_id,
                thread_ts,
                (
                    f"<@{cached_entry.get('slack_user_id')}> Hey! The investigation finished, but I can't show results."
                    " Try again."
                ),
            )

    @staticmethod
    def _build_followup_action_elements(followup_options, cached_entry):
        account_id = cached_entry.get("account_id") if cached_entry else None
        session_id = cached_entry.get("session_id") if cached_entry else None

        if len(followup_options) > FOLLOWUP_OPTIONS_THRESHOLD and account_id and session_id:
            url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={session_id}"
            return [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": f"Open in {settings.urls.branding_name}"},
                    "url": url,
                    "action_id": "open_followup_in_ui",
                }
            ]

        if len(followup_options) <= 3:
            return [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": option},
                    "value": option,
                    "action_id": f"select_followup_option--{option}",
                }
                for option in followup_options
            ]

        # Cap at Slack's hard limit as a safety net in case the UI-link branch
        # above was skipped (e.g., cached_entry missing account_id/session_id).
        return [
            {
                "type": "static_select",
                "placeholder": {"type": "plain_text", "text": "Select an option"},
                "options": [
                    {"text": {"type": "plain_text", "text": option}, "value": option}
                    for option in followup_options[:SLACK_STATIC_SELECT_MAX_OPTIONS]
                ],
                "action_id": "select_followup_option_dropdown",
            }
        ]

    def handle_followup_response(self, payload, cached_entry, channel_id: str, thread_ts: str, team_id: str):
        try:
            follow_up = json.loads(payload.response)
            followup_question = f"{follow_up.get('question', '')}"
            followup_options = follow_up.get("followupOptions", [])
            agent_id = follow_up.get("agent_id", "")
            message_id = follow_up.get("message_id", "")

            blocks = Transformer.to_slack(MarkdownBlock(text=followup_question))
            if followup_options:
                blocks.append(
                    {
                        "type": "actions",
                        "elements": self._build_followup_action_elements(followup_options, cached_entry),
                    }
                )
            else:
                # Free-text follow-up: Slack has no inline input field, so tell the user
                # they should reply by @mentioning the bot in this thread with their answer.
                blocks += Transformer.to_slack(
                    ContextBlock(text="_Reply by mentioning me in this thread with your answer._")
                )

            if cached_entry:
                event_cache.update_event_entry(thread_ts, agent_id=agent_id, message_id=message_id)
                if cached_entry.get("slack_user_id"):
                    blocks += Transformer.to_slack(ContextBlock(text=f"<@{cached_entry.get('slack_user_id')}>"))

            followup_msg_ts = self.common_service.slack_reply_in_thread_as_blocks(
                channel_id, team_id, thread_ts, blocks
            )

            if followup_msg_ts:
                event_cache.update_event_entry(
                    thread_ts, followup_msg_ts=followup_msg_ts, followup_question=followup_question
                )

        except (json.JSONDecodeError, KeyError) as e:
            LOG.warning(f"Failed to parse follow-up response as JSON: {e}. Using raw response.")
            self.reply(channel_id, team_id, thread_ts, payload.response)

    # ==================== Teams Response Handlers ====================

    async def handle_teams_final_response(self, payload, cached_entry, conversation_id: str):
        try:
            response_text = payload.response
            conversation_ref = cached_entry.get("conversation_reference")
            teams_account_id = cached_entry.get("teams_id")

            if not conversation_ref:
                LOG.error("No conversation reference found in cached entry for Teams response")
                return

            await self.common_service.teams_reply_from_conversation_reference(
                conversation_ref, response_text, teams_account_id
            )

            event_cache.update_event_entry(conversation_id, status="COMPLETED")
            LOG.debug("Teams conversation marked as COMPLETED.")

        except Exception as e:
            LOG.error(f"Failed to process Teams LLM response: {e}")
            if cached_entry.get("conversation_reference"):
                await self.common_service.teams_reply_from_conversation_reference(
                    cached_entry.get("conversation_reference"),
                    "Hey! The investigation finished, but I can't show results. Try again.",
                    cached_entry.get("teams_id"),
                )

    async def handle_teams_followup_response(self, payload, cached_entry, conversation_id: str):
        try:
            follow_up = json.loads(payload.response)
            followup_question = f"{follow_up.get('question', '')}"
            followup_options = follow_up.get("followupOptions", [])
            agent_id = follow_up.get("agent_id", "")
            message_id = follow_up.get("message_id", "")

            conversation_ref = cached_entry.get("conversation_reference")
            teams_account_id = cached_entry.get("teams_id")

            if not conversation_ref:
                LOG.error("No conversation reference found in cached entry for Teams followup")
                return

            if followup_options:
                # Create Adaptive Card with clickable buttons for follow-up options
                adaptive_card = self.common_service.create_teams_followup_card(followup_question, followup_options)
                await self.common_service.teams_reply_with_card_from_conversation_reference(
                    conversation_ref, adaptive_card, teams_account_id
                )
            else:
                # No options, just send the question as text
                await self.common_service.teams_reply_from_conversation_reference(
                    conversation_ref, followup_question, teams_account_id
                )

            if cached_entry:
                event_cache.update_event_entry(conversation_id, agent_id=agent_id, message_id=message_id)

        except (json.JSONDecodeError, KeyError) as e:
            LOG.warning(f"Failed to parse Teams follow-up response as JSON: {e}. Using raw response.")
            if cached_entry.get("conversation_reference"):
                await self.common_service.teams_reply_from_conversation_reference(
                    cached_entry.get("conversation_reference"), payload.response, cached_entry.get("teams_id")
                )

    # ==================== Teams Bot Messaging Methods ====================

    @staticmethod
    def _extract_activity_info(activity: Activity):
        conversation_id = activity.conversation.id
        aad_user_id = activity.from_property.aad_object_id if activity.from_property else None
        message_text = activity.text or ""
        message_id = activity.id
        channel_data = activity.channel_data or {}
        teams_account_id = (
            isinstance(channel_data, dict)
            and isinstance(channel_data.get("tenant"), dict)
            and channel_data["tenant"].get("id")
        ) or None
        return conversation_id, aad_user_id, message_text, message_id, teams_account_id

    @staticmethod
    def _is_bot_message(activity: Activity) -> bool:
        return activity.from_property and activity.from_property.role == "bot"

    @staticmethod
    def _is_cluster_selection(activity: Activity) -> bool:
        return bool(activity.value and isinstance(activity.value, dict) and "cluster" in activity.value)

    @staticmethod
    def _is_followup_selection(activity: Activity) -> bool:
        return bool(
            activity.value
            and isinstance(activity.value, dict)
            and activity.value.get("action_type") == "select_followup_option"
        )

    async def _reply_unidentified_user(self, activity, aad_user_id, teams_account_id):
        LOG.warning(f"Unable to get email for Teams user {aad_user_id}")
        await self.common_service.teams_reply(
            activity,
            get_user_not_found_message(),
            teams_account_id,
        )

    async def _reply_empty_message(self, activity, teams_account_id):
        await self.common_service.teams_reply(activity, get_empty_message_response(), teams_account_id)

    async def _handle_exit(self, activity, thread_ts, teams_account_id):
        self.cache.remove_event_entry(thread_ts)
        await self.common_service.teams_reply(activity, get_exit_message(), teams_account_id)

    async def _handle_processing_error(self, activity, exception, teams_account_id):
        LOG.exception(f"Error handling Teams message event: {exception}")
        try:
            await self.common_service.teams_reply(
                activity,
                "Oops — I ran into a hiccup processing that. \nCan you try again in a moment?",
                teams_account_id,
            )
        except Exception as reply_error:
            LOG.error(f"Failed to send error reply to Teams: {reply_error}")

    async def handle_message_event(self, activity: Activity):
        teams_account_id = None
        try:
            conversation_id, aad_user_id, message_text, message_id, teams_account_id = self._extract_activity_info(
                activity
            )

            if self._is_bot_message(activity):
                LOG.debug("Skipping bot's own message")
                return

            user_email = await self.common_service.get_teams_user_info(aad_user_id, teams_account_id)
            if not user_email:
                await self._reply_unidentified_user(activity, aad_user_id, teams_account_id)
                return

            thread_ts = conversation_id

            if self._is_cluster_selection(activity):
                await self._handle_teams_cluster_selection_and_query_llm(
                    activity, activity.value["cluster"], thread_ts, teams_account_id
                )
                return

            if self._is_followup_selection(activity):
                followup_option = activity.value.get("followup_option")
                if followup_option:
                    await self._handle_teams_followup_selection_and_query_llm(
                        activity, followup_option, thread_ts, teams_account_id
                    )
                else:
                    LOG.warning("Follow-up selection activity received without a followup_option value.")
                return

            cleaned_text = message_text.strip()
            if not cleaned_text:
                await self._reply_empty_message(activity, teams_account_id)
                return

            if cleaned_text in ["exit", "/exit"]:
                await self._handle_exit(activity, thread_ts, teams_account_id)
                return

            cached_entry = self.cache.get_event_entry(thread_ts)
            if cached_entry and cached_entry.get("account_id"):
                LOG.debug(f"Continuing existing Teams conversation for {conversation_id}")
                self.cache.update_event_entry(thread_ts, text=cleaned_text)
                await self._process_teams_message(activity, cached_entry, cleaned_text, teams_account_id)
                return

            await self._request_account_confirmation_and_cache_event(
                aad_user_id,
                activity,
                cleaned_text,
                conversation_id,
                message_id,
                teams_account_id,
                thread_ts,
                user_email,
            )

        except Exception as e:
            await self._handle_processing_error(activity, e, teams_account_id)

    async def _request_account_confirmation_and_cache_event(
        self, aad_user_id, activity, cleaned_text, conversation_id, message_id, teams_account_id, thread_ts, user_email
    ):
        user_id, tenants = validate_and_get_user_tenants(user_email)

        if not user_id or not tenants:
            LOG.warning("No Nudgebee account found for Teams user email: %s", user_email)
            await self.common_service.teams_reply(
                activity,
                get_user_not_found_message(),
                teams_account_id,
            )
            return

        conversation_ref = self._extract_conversation_reference(activity)
        session_id = self._teams_session_id(conversation_id, message_id)

        event_entry = {
            "event_id": message_id,
            "text": cleaned_text,
            "user_id": user_id,
            "aad_user_id": aad_user_id,
            "conversation_id": conversation_id,
            "teams_id": teams_account_id,
            "session_id": session_id,
            "platform": "ms_teams",
            "conversation_reference": conversation_ref,
        }
        self.cache.cache_event_entry(thread_ts, event_entry)

        mapped_account = await self._resolve_mapped_account_async("ms_teams", conversation_id, user_id, tenants)
        if mapped_account:
            await self._handle_mapped_account_teams(mapped_account, thread_ts, activity, cleaned_text, teams_account_id)
            return

        valid_accounts = await self._get_valid_accounts_async(user_id, tenants)

        if valid_accounts:
            card = self.common_service.create_teams_cluster_selection_card(valid_accounts)
            await self.common_service.teams_reply_with_card(activity, card, teams_account_id)
            LOG.debug(f"Sent account selection card with {len(valid_accounts)} options to Teams user")
        else:
            await self.common_service.teams_reply(
                activity,
                get_account_not_accessible_message(),
                teams_account_id,
            )

    @staticmethod
    def _extract_conversation_reference(activity):
        ref = TurnContext.get_conversation_reference(activity)
        return {
            "conversation_id": ref.conversation.id if ref.conversation else None,
            "service_url": ref.service_url,
            "channel_id": ref.channel_id,
            "bot_id": ref.bot.id if ref.bot else None,
            "user_id": ref.user.id if ref.user else None,
            "user_name": ref.user.name if ref.user else None,
        }

    async def _handle_mapped_account_teams(self, account, thread_ts, activity, cleaned_text, teams_account_id):
        account_id = account["id"]
        with Session(self.session.get_bind()) as session:
            account_details = get_account_by_id(session, account_id)

        if not account_details:
            await self.common_service.teams_reply(activity, CLUSTER_NOT_FOUND, teams_account_id)
            return

        account_name = account_details.get("account_name")
        mapped_tenant_id = account_details.get("tenant")

        # Update cache state
        self.cache.update_event_entry(thread_ts, tenant_id=mapped_tenant_id, account_id=account_id)
        updated_entry = self.cache.get_event_entry(thread_ts)
        session_id = updated_entry.get("session_id", thread_ts)

        # UI URL
        url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={session_id}"
        message = (
            f"Sure thing! Looking into {account_name} right away.\n\n[View in {settings.urls.branding_name}]({url})"
        )

        await self.common_service.teams_reply(activity, message, teams_account_id)

        # Process the original message
        if cleaned_text:
            await self._process_teams_message(
                activity, updated_entry, cleaned_text, teams_account_id, skip_confirmation_message=True
            )

    async def _handle_teams_cluster_selection_and_query_llm(self, activity, cluster_id, thread_ts, teams_account_id):
        try:
            cached_entry = self.cache.get_event_entry(thread_ts)

            if not cached_entry:
                LOG.warning(f"No cached entry found for thread {thread_ts}")
                await self.common_service.teams_reply(activity, get_session_expired_message(), teams_account_id)
                return

            if cached_entry.get("account_id"):
                await self.common_service.teams_reply(activity, get_account_already_selected(), teams_account_id)
                return

            # Fetch and update account details
            with Session(self.session.get_bind()) as session:
                account = get_account_by_id(session, cluster_id)

            if not account:
                await self.common_service.teams_reply(activity, CLUSTER_NOT_FOUND, teams_account_id)
                return

            account_name = account.get("account_name")
            account_id = account.get("id")
            tenant_id = account.get("tenant")

            self.cache.update_event_entry(thread_ts, tenant_id=tenant_id, account_id=account_id)

            # Get the cached entry to access session_id for UI link
            updated_entry = self.cache.get_event_entry(thread_ts)
            session_id = updated_entry.get("session_id", thread_ts)

            # Create UI link similar to Slack
            url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={session_id}"
            confirmation = get_account_selected_confirmation(account_name)
            message_with_link = f"{confirmation}\n\n[View in {settings.urls.branding_name}]({url})"

            await self.common_service.teams_reply(activity, message_with_link, teams_account_id)
            original_text = updated_entry.get("text", "")

            if original_text:
                await self._process_teams_message(
                    activity, updated_entry, original_text, teams_account_id, skip_confirmation_message=True
                )

        except Exception as e:
            LOG.error(f"Failed to handle Teams account selection: {e}")
            await self.common_service.teams_reply(
                activity, "Hmm, couldn't connect to the account. Try again in a bit?", teams_account_id
            )

    async def _handle_teams_followup_selection_and_query_llm(
        self, activity, followup_option, thread_ts, teams_account_id
    ):
        try:
            cached_entry = self.cache.get_event_entry(thread_ts)

            if not cached_entry:
                LOG.warning(f"No cached entry found for thread {thread_ts}")
                await self.common_service.teams_reply(activity, get_session_expired_message(), teams_account_id)
                return

            payload = {
                "query": followup_option,
                "account_id": cached_entry["account_id"],
                "user_id": cached_entry["user_id"],
                "session_id": cached_entry["session_id"],
                "source": "InstantNotification",
                "async": True,
                "agent_id": cached_entry.get("agent_id"),
                "message_id": cached_entry.get("message_id"),
            }

            headers = {
                "x-tenant-id": cached_entry["tenant_id"],
                "x-user-id": cached_entry["user_id"],
            }

            # Send confirmation message
            await self.common_service.teams_reply(
                activity, get_followup_confirmation(followup_option).replace("*", "**"), teams_account_id
            )

            # Query LLM server with selected option
            await self.async_query_llm_server(payload, headers)

        except Exception as e:
            LOG.error(f"Failed to handle Teams followup selection: {e}")
            await self.common_service.teams_reply(
                activity, "Hmm, I couldn't process that. Mind trying again?", teams_account_id
            )

    async def _process_teams_message(
        self, activity, cached_entry, message_text, teams_account_id, skip_confirmation_message=False
    ):
        try:
            payload = {
                "query": message_text,
                "account_id": cached_entry["account_id"],
                "user_id": cached_entry["user_id"],
                "session_id": cached_entry["session_id"],
                "source": "InstantNotification",
                "async": True,
            }
            headers = {
                "x-tenant-id": cached_entry["tenant_id"],
                "x-user-id": cached_entry["user_id"],
            }

            if not skip_confirmation_message:
                await self.common_service.teams_reply(activity, get_processing_confirmation(), teams_account_id)

            await self.async_query_llm_server(payload, headers)

        except aiohttp.ClientError as e:
            LOG.error(f"Failed to process Teams message: {e}")
            status = getattr(e, "status", None)
            error_response = getattr(e, "error_response", None)
            await self.common_service.teams_reply(
                activity, self._llm_error_reply(status, error_response), teams_account_id
            )
        except Exception as e:
            LOG.error(f"Failed to process Teams message: {e}")
            await self.common_service.teams_reply(activity, get_llm_offline_message(), teams_account_id)

    async def handle_conversation_update(self, activity: Activity):
        try:
            conversation_id = activity.conversation.id

            # Check if bot was added to conversation
            if activity.members_added:
                for member in activity.members_added:
                    if member.id == activity.recipient.id:  # Bot was added
                        LOG.info(f"Bot added to Teams conversation {conversation_id}")
                        # Send welcome message
                        await self.common_service.teams_send_welcome_message(activity)
                    else:
                        LOG.info(f"Member {member.name} added to conversation {conversation_id}")

            # Check if bot was removed from conversation
            if activity.members_removed:
                for member in activity.members_removed:
                    if member.id == activity.recipient.id:  # Bot was removed
                        LOG.info(f"Bot removed from Teams conversation {conversation_id}")
                    else:
                        LOG.info(f"Member {member.name} removed from conversation {conversation_id}")

        except Exception as e:
            LOG.exception(f"Error handling Teams conversation update: {e}")

    # ==================== Google Chat Bot Messaging Methods ====================

    async def handle_google_chat_event(self, event_data: dict):
        """Main entry point for Google Chat events. Routes by event type."""
        event_type = event_data.get("type")
        try:
            if event_type == "MESSAGE":
                await self._handle_gchat_message(event_data)
            elif event_type == "CARD_CLICKED":
                await self._handle_gchat_card_click(event_data)
            elif event_type == "ADDED_TO_SPACE":
                self._handle_gchat_added_to_space(event_data)
            elif event_type == "REMOVED_FROM_SPACE":
                LOG.info("Bot removed from Google Chat space: %s", event_data.get("space", {}).get("name"))
            else:
                LOG.debug("Unhandled Google Chat event type: %s", event_type)
        except Exception as e:
            LOG.exception("Error handling Google Chat event type=%s: %s", event_type, e)
            raise

    async def _handle_gchat_message(self, event_data: dict):
        """Process an incoming Google Chat message."""
        try:
            user_email = event_data.get("user", {}).get("email")
            message_data = event_data.get("message", {})
            space_name = event_data.get("space", {}).get("name")
            thread_name = message_data.get("thread", {}).get("name")
            message_name = message_data.get("name")
            message_text = (message_data.get("argumentText") or message_data.get("text", "")).strip()

            # Check for existing conversation in cache (handles continuing conversations
            # before any user validation, same as Slack's cached_entry check)
            cached_entry = self.cache.get_event_entry(thread_name)
            if cached_entry and cached_entry.get("account_id"):
                LOG.debug("Continuing existing Google Chat conversation for thread %s", thread_name)
                self.cache.update_event_entry(thread_name, text=message_text)
                cached_entry = self.cache.get_event_entry(thread_name)
                tenant_id = cached_entry["tenant_id"]
                await self._process_gchat_message(cached_entry, message_text, space_name, thread_name, tenant_id)
                return

            # Validate user first (like Slack resolves user email from team_id + user_id)
            if not user_email:
                LOG.warning("Google Chat event missing user email in space %s", space_name)
                return

            user_id, tenants = validate_and_get_user_tenants(user_email)
            if not user_id or not tenants:
                LOG.warning("No Nudgebee account found for Google Chat user email: %s", user_email)
                return

            # Find which of the user's tenants has a google_chat installation
            # (like Slack uses team_id from the event to find the bot installation)
            tenant_id = self._find_gchat_tenant(tenants)
            if not tenant_id:
                LOG.error("No Google Chat installation found for user %s tenants", user_email)
                return

            if not message_text:
                self.common_service.gchat_reply_in_thread(
                    space_name, thread_name, get_empty_message_response(), tenant_id
                )
                return

            if message_text.lower() in ("exit", "/exit"):
                self.cache.remove_event_entry(thread_name)
                self.common_service.gchat_reply_in_thread(space_name, thread_name, get_exit_message(), tenant_id)
                return

            # Cache the event entry
            event_entry = {
                "event_id": message_name,
                "text": message_text,
                "user_id": user_id,
                "user_email": user_email,
                "space_name": space_name,
                "thread_name": thread_name,
                "session_id": thread_name,
                "platform": "google_chat",
                "tenant_id": tenant_id,
            }
            self.cache.cache_event_entry(thread_name, event_entry)

            await self._handle_gchat_new_conversation(
                user_id, tenants, space_name, thread_name, tenant_id, message_text
            )

        except Exception as e:
            LOG.exception("Error handling Google Chat message: %s", e)
            raise

    async def _handle_gchat_new_conversation(self, user_id, tenants, space_name, thread_name, tenant_id, message_text):
        """Handle a new Google Chat conversation: resolve account and query LLM."""
        mapped_account = await self._resolve_mapped_account_async("google_chat", space_name, user_id, tenants)
        if mapped_account:
            await self._handle_gchat_mapped_account(mapped_account, thread_name, space_name, tenant_id, message_text)
            return

        valid_accounts = await self._get_valid_accounts_async(user_id, tenants)
        if not valid_accounts:
            self.common_service.gchat_reply_in_thread(
                space_name, thread_name, get_account_not_accessible_message(), tenant_id
            )
            return

        if len(valid_accounts) == 1:
            await self._handle_gchat_mapped_account(valid_accounts[0], thread_name, space_name, tenant_id, message_text)
            return

        self._request_gchat_account_confirmation(space_name, thread_name, valid_accounts, tenant_id)

    async def _handle_gchat_card_click(self, event_data: dict):
        """Process a card button click from Google Chat."""
        try:
            action = event_data.get("common", {}).get("invokedFunction")
            parameters = event_data.get("common", {}).get("parameters", {})

            space_name = event_data.get("space", {}).get("name")
            thread_name = event_data.get("message", {}).get("thread", {}).get("name")

            # Card clicks always have a cached entry from the original message
            cached_entry = self.cache.get_event_entry(thread_name)
            if not cached_entry or not cached_entry.get("tenant_id"):
                LOG.warning("No cached entry for Google Chat card click in thread %s", thread_name)
                return

            tenant_id = cached_entry["tenant_id"]

            if action == "select_account":
                await self._handle_gchat_account_selection(parameters, space_name, thread_name, tenant_id, cached_entry)
            elif action == "select_followup":
                await self._handle_gchat_followup_selection(
                    parameters, space_name, thread_name, tenant_id, cached_entry
                )
            else:
                LOG.debug("Unhandled Google Chat card action: %s", action)

        except Exception as e:
            LOG.exception("Error handling Google Chat card click: %s", e)
            raise

    async def _handle_gchat_account_selection(self, parameters, space_name, thread_name, tenant_id, cached_entry):
        """Handle account selection card click in Google Chat."""
        account_id = parameters.get("account_id")
        if not account_id:
            LOG.warning("select_account card click without account_id")
            return

        if cached_entry.get("account_id"):
            self.common_service.gchat_reply_in_thread(
                space_name, thread_name, get_account_already_selected(), tenant_id
            )
            return

        with Session(self.session.get_bind()) as session:
            account = get_account_by_id(session, account_id)

        if not account:
            self.common_service.gchat_reply_in_thread(space_name, thread_name, CLUSTER_NOT_FOUND, tenant_id)
            return

        account_name = account.get("account_name")
        account_tenant = account.get("tenant")
        self.cache.update_event_entry(thread_name, tenant_id=account_tenant, account_id=account_id)

        updated_entry = self.cache.get_event_entry(thread_name)
        session_id = updated_entry.get("session_id", thread_name)
        url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={session_id}"
        confirmation = get_account_selected_confirmation(account_name)
        message = f"{confirmation}\n<{url}|View in {settings.urls.branding_name}>"
        self.common_service.gchat_reply_in_thread(space_name, thread_name, message, account_tenant)

        original_text = updated_entry.get("text", "")
        if original_text:
            await self._process_gchat_message(updated_entry, original_text, space_name, thread_name, account_tenant)

    async def _handle_gchat_followup_selection(self, parameters, space_name, thread_name, tenant_id, cached_entry):
        """Handle follow-up option selection card click in Google Chat."""
        followup_option = parameters.get("option")
        if not followup_option:
            LOG.warning("select_followup card click without option")
            return

        self.common_service.gchat_reply_in_thread(
            space_name, thread_name, get_followup_confirmation(followup_option), tenant_id
        )

        payload = {
            "query": followup_option,
            "account_id": cached_entry["account_id"],
            "user_id": cached_entry["user_id"],
            "session_id": cached_entry["session_id"],
            "source": "InstantNotification",
            "async": True,
            "agent_id": cached_entry.get("agent_id"),
            "message_id": cached_entry.get("message_id"),
        }
        headers = {
            "x-tenant-id": cached_entry.get("tenant_id", tenant_id),
            "x-user-id": cached_entry["user_id"],
        }
        await self.async_query_llm_server(payload, headers)

    def _handle_gchat_added_to_space(self, event_data: dict):
        """Post welcome card when bot is added to a Google Chat space."""
        try:
            space_name = event_data.get("space", {}).get("name")
            user_email = event_data.get("user", {}).get("email")

            # Resolve tenant from the user who added the bot (like Slack uses team_id)
            tenant_id = None
            if user_email:
                _, tenants = validate_and_get_user_tenants(user_email)
                if tenants:
                    tenant_id = self._find_gchat_tenant(tenants)

            if not tenant_id:
                LOG.warning("Could not resolve tenant for ADDED_TO_SPACE event in space %s", space_name)
                return

            welcome_card = self.common_service.create_gchat_welcome_card()
            self.common_service.gchat_reply_with_card(space_name, None, welcome_card, tenant_id)
            LOG.info("Sent welcome card to Google Chat space %s", space_name)
        except Exception as e:
            LOG.exception("Error handling Google Chat ADDED_TO_SPACE: %s", e)
            raise

    def _request_gchat_account_confirmation(self, space_name, thread_name, valid_accounts, tenant_id):
        """Send account selection card to Google Chat thread."""
        card = self.common_service.create_gchat_cluster_selection_card(valid_accounts)
        self.common_service.gchat_reply_with_card(space_name, thread_name, card, tenant_id)
        LOG.debug("Sent account selection card with %d options to Google Chat", len(valid_accounts))

    async def _handle_gchat_mapped_account(self, account, thread_name, space_name, tenant_id, message_text):
        """Handle a pre-mapped or single-account scenario for Google Chat."""
        account_id = account["id"]
        with Session(self.session.get_bind()) as session:
            account_details = get_account_by_id(session, account_id)

        if not account_details:
            self.common_service.gchat_reply_in_thread(space_name, thread_name, CLUSTER_NOT_FOUND, tenant_id)
            return

        account_name = account_details.get("account_name")
        mapped_tenant_id = account_details.get("tenant")

        self.cache.update_event_entry(thread_name, tenant_id=mapped_tenant_id, account_id=account_id)
        updated_entry = self.cache.get_event_entry(thread_name)
        session_id = updated_entry.get("session_id", thread_name)

        url = f"{settings.base_url}/ask-nudgebee?accountId={account_id}&session_id={session_id}"
        confirmation = get_account_selected_confirmation(account_name)
        message = f"{confirmation}\n<{url}|View in {settings.urls.branding_name}>"
        self.common_service.gchat_reply_in_thread(space_name, thread_name, message, mapped_tenant_id)

        if message_text:
            await self._process_gchat_message(updated_entry, message_text, space_name, thread_name, mapped_tenant_id)

    async def _process_gchat_message(self, cached_entry, message_text, space_name, thread_name, tenant_id):
        """Send processing confirmation and query LLM for Google Chat."""
        try:
            self.common_service.gchat_reply_in_thread(space_name, thread_name, get_processing_confirmation(), tenant_id)

            payload = {
                "query": message_text,
                "account_id": cached_entry["account_id"],
                "user_id": cached_entry["user_id"],
                "session_id": cached_entry.get("session_id", thread_name),
                "source": "InstantNotification",
                "async": True,
            }
            headers = {
                "x-tenant-id": cached_entry.get("tenant_id", tenant_id),
                "x-user-id": cached_entry["user_id"],
            }
            await self.async_query_llm_server(payload, headers)

        except aiohttp.ClientError as e:
            LOG.error("Failed to process Google Chat message: %s", e)
            status = getattr(e, "status", None)
            error_response = getattr(e, "error_response", None)
            self.common_service.gchat_reply_in_thread(
                space_name, thread_name, self._llm_error_reply(status, error_response), tenant_id
            )
        except Exception as e:
            LOG.error("Failed to process Google Chat message: %s", e)
            self.common_service.gchat_reply_in_thread(space_name, thread_name, get_llm_offline_message(), tenant_id)

    def _find_gchat_tenant(self, tenants):
        """Find which of the user's tenants has a google_chat installation.

        This mirrors how Slack uses team_id from the event payload to find
        the bot installation — here we use the user's tenants to find the
        matching google_chat MessagingPlatform row.
        """
        try:
            with Session(self.session.get_bind()) as session:
                platform = (
                    session.query(MessagingPlatform)
                    .filter(
                        MessagingPlatform.platform == "google_chat",
                        MessagingPlatform.tenant_id.in_(tenants),
                    )
                    .first()
                )
                return platform.tenant_id if platform else None
        except Exception as e:
            LOG.warning("Failed to find Google Chat tenant from user tenants: %s", e)
            return None

    # ==================== Google Chat LLM Response Handlers ====================

    def handle_gchat_final_response(self, payload, cached_entry, thread_name: str):
        """Post LLM final response text to Google Chat thread."""
        try:
            response_text = payload.response
            space_name = cached_entry.get("space_name")
            tenant_id = cached_entry.get("tenant_id")

            if not space_name or not tenant_id:
                LOG.error("Missing space_name or tenant_id in cached entry for Google Chat response")
                return

            self.common_service.gchat_reply_in_thread(space_name, thread_name, response_text, tenant_id)
            event_cache.update_event_entry(thread_name, status="COMPLETED")
            LOG.debug("Google Chat conversation marked as COMPLETED.")

        except Exception as e:
            LOG.error("Failed to process Google Chat LLM response: %s", e)
            space_name = cached_entry.get("space_name")
            tenant_id = cached_entry.get("tenant_id")
            if space_name and tenant_id:
                self.common_service.gchat_reply_in_thread(
                    space_name,
                    thread_name,
                    "Hey! The investigation finished, but I can't show results. Try again.",
                    tenant_id,
                )

    def handle_gchat_followup_response(self, payload, cached_entry, thread_name: str):
        """Post follow-up card with options to Google Chat thread."""
        try:
            follow_up = json.loads(payload.response)
            followup_question = follow_up.get("question", "")
            followup_options = follow_up.get("followupOptions", [])
            agent_id = follow_up.get("agent_id", "")
            message_id = follow_up.get("message_id", "")

            space_name = cached_entry.get("space_name")
            tenant_id = cached_entry.get("tenant_id")

            if not space_name or not tenant_id:
                LOG.error("Missing space_name or tenant_id in cached entry for Google Chat followup")
                return

            if followup_options:
                card = self.common_service.create_gchat_followup_card(followup_question, followup_options)
                self.common_service.gchat_reply_with_card(space_name, thread_name, card, tenant_id)
            else:
                self.common_service.gchat_reply_in_thread(space_name, thread_name, followup_question, tenant_id)

            if cached_entry:
                event_cache.update_event_entry(thread_name, agent_id=agent_id, message_id=message_id)

        except (json.JSONDecodeError, KeyError) as e:
            LOG.warning("Failed to parse Google Chat follow-up response as JSON: %s. Using raw response.", e)
            space_name = cached_entry.get("space_name")
            tenant_id = cached_entry.get("tenant_id")
            if space_name and tenant_id:
                self.common_service.gchat_reply_in_thread(space_name, thread_name, payload.response, tenant_id)
