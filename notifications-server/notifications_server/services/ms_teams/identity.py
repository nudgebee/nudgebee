"""Who sent a Teams activity, from the bot's member lookup, with no Microsoft Graph."""

import asyncio
import logging
import time
from typing import Dict, Optional, Tuple

from botbuilder.schema import Activity

from notifications_server.services.ms_teams.activity import channel_data_of
from notifications_server.services.ms_teams.bot_sender import TeamsBotError, get_sender

LOG = logging.getLogger(__name__)

EMAIL_CACHE_SECONDS = 600
# Emails already looked up in this process, so a conversation doesn't cost a
# Bot Connector call per message.
_emails: Dict[Tuple[str, str], Tuple[str, float]] = {}


def _member_conversation_id(activity: Activity) -> Optional[str]:
    """Where to look a member up: the team for channel activity, otherwise the chat."""
    team_id = (channel_data_of(activity).get("team") or {}).get("id")
    return team_id or (activity.conversation.id if activity.conversation else None)


async def lookup_member(activity: Activity) -> Optional[Dict[str, str]]:
    """The sender's name and email from the bot's member lookup, or None when
    the lookup couldn't be made. Email can be empty when Teams doesn't return it."""
    sender = activity.from_property
    conversation_id = _member_conversation_id(activity)
    if not (sender and sender.id and conversation_id and activity.service_url):
        return None
    try:
        return await asyncio.to_thread(get_sender().get_member, activity.service_url, conversation_id, sender.id)
    except TeamsBotError as e:
        LOG.warning("Teams member lookup failed: %s", e)
        return None


async def sender_email(activity: Activity) -> Optional[str]:
    """The sender's email, cached per process for a few minutes. None when the
    lookup fails or Teams returns no email."""
    sender = activity.from_property
    aad_tenant_id = (channel_data_of(activity).get("tenant") or {}).get("id") or ""
    key = (aad_tenant_id, (sender.aad_object_id or sender.id) if sender else "")
    cached = _emails.get(key)
    if cached and cached[1] > time.monotonic():
        return cached[0]
    member = await lookup_member(activity)
    email = (member or {}).get("email") or None
    if email:
        _emails[key] = (email, time.monotonic() + EMAIL_CACHE_SECONDS)
    return email
