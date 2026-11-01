"""Send to Microsoft Teams as the bot, through the Bot Connector.

Every call is synchronous and does network I/O: async callers run it with
asyncio.to_thread, sync callers (pickers, test send) call it directly from a
thread-pool thread.
"""

import logging
import random
import time
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional

from botbuilder.schema import (
    Activity,
    ActivityTypes,
    Attachment,
)
from botframework.connector import ConnectorClient
from botframework.connector.auth import MicrosoftAppCredentials
from botframework.connector.teams import TeamsConnectorClient
from msrest.exceptions import HttpOperationError
from sqlalchemy import select

from notifications_server.configs import settings
from notifications_server.models.models import Integration, IntegrationConfigValue
from notifications_server.services.messaging_installations import _decrypt_config
from notifications_server.services.ms_teams.activity import TEAM_TYPE, channel_data_of

LOG = logging.getLogger(__name__)

ADAPTIVE_CARD = "application/vnd.microsoft.card.adaptive"
DEFAULT_SERVICE_URL = "https://smba.trafficmanager.net/teams/"
# A send that failed with 502/504 may still have been delivered, so only reads
# retry those; 429 and 412 mean Teams did not take the message.
RETRY_SEND_STATUSES = {412, 429}
RETRY_READ_STATUSES = {412, 429, 502, 504}
# Retries sleep on a shared thread pool (other platforms' sends use it too), so a
# throttled call gives up after this much waiting rather than holding a thread.
# The default backoff (2s, then 4s) fits two retries in that budget; a throttled
# send that still fails is reported as failed, not queued again.
MAX_ATTEMPTS = 3
MAX_RETRY_WAIT_SECONDS = 10
CHANNEL_CACHE_SECONDS = 600

APP_NOT_IN_TEAM = "The NudgeBee app isn't added to this team in Microsoft Teams"
NOT_SUPPORTED = "This action isn't supported for Microsoft Teams"
# The bot lists and posts to a team's standard channels only.
CHANNEL_NOT_IN_TEAM = (
    "This channel isn't a standard channel of the selected Microsoft Teams team; "
    "NudgeBee can post only to standard channels"
)
CHANNEL_FORBIDDEN = "Microsoft Teams refused the post; check that the NudgeBee app is still added to this team"


class TeamsBotError(Exception):
    def __init__(self, message: str, status: Optional[int] = None):
        super().__init__(message)
        self.status = status


@dataclass
class TeamRef:
    """A team with the bot installed, from its ms_teams_team integration."""

    integration_id: Any
    tenant_id: Any
    aad_group_id: str
    thread_id: str
    name: str
    service_url: str


def _team_ref(integ: Integration, cfg: Dict[str, str]) -> TeamRef:
    return TeamRef(
        integration_id=integ.id,
        tenant_id=integ.tenant_id,
        aad_group_id=integ.name,
        thread_id=cfg.get("team_thread_id") or "",
        name=cfg.get("team_name") or "",
        service_url=cfg.get("service_url") or DEFAULT_SERVICE_URL,
    )


def _team_query(tenant_id, aad_group_id: Optional[str] = None):
    query = select(Integration).where(
        Integration.type == TEAM_TYPE,
        Integration.tenant_id == tenant_id,
        Integration.status != "disabled",
    )
    if aad_group_id:
        query = query.where(Integration.name == aad_group_id)
    return query


def load_teams(session, tenant_id) -> List[TeamRef]:
    """Sync: every team with the bot installed for this tenant."""
    teams = []
    for integ in session.execute(_team_query(tenant_id).order_by(Integration.name)).scalars().all():
        rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integ.id).all()
        teams.append(_team_ref(integ, _decrypt_config(rows)))
    return teams


async def load_team_async(session, tenant_id, aad_group_id) -> Optional[TeamRef]:
    """Async: the installed team a channel mapping points at, or None."""
    if not aad_group_id:
        return None
    integ = (await session.execute(_team_query(tenant_id, aad_group_id))).scalars().first()
    if integ is None:
        return None
    rows = (
        (await session.execute(select(IntegrationConfigValue).where(IntegrationConfigValue.integration_id == integ.id)))
        .scalars()
        .all()
    )
    return _team_ref(integ, _decrypt_config(rows))


def card_activity(card: Dict[str, Any]) -> Activity:
    return Activity(type=ActivityTypes.message, attachments=[Attachment(content_type=ADAPTIVE_CARD, content=card)])


def thread_conversation_id(channel_id: str, root_message_id: str) -> str:
    """The conversation id that replies in a channel thread. A channel id that
    already names a thread is replaced, not extended."""
    return f"{channel_id.split(';messageid=')[0]};messageid={root_message_id}"


def _only_connect_retries(client):
    """_call owns retries. msrest's default policy also retries most statuses,
    and on older urllib3 even POSTs, which could post a card twice; keep only
    retries of a connection that never opened, when nothing was sent. The
    policy object is already attached to the HTTP session, so it is changed in
    place."""
    policy = client.config.retry_policy.policy
    policy.status_forcelist = frozenset()
    policy.status = 0
    policy.read = 0
    return client


class TeamsBotSender:
    """One credential set: the bot's app id, secret and home tenant."""

    def __init__(self):
        self.app_id = settings.ms_teams.client_id
        self._credentials = MicrosoftAppCredentials(
            settings.ms_teams.client_id,
            settings.ms_teams.client_secret,
            channel_auth_tenant=settings.ms_teams.bot_tenant_id or None,
        )
        self._channel_cache: Dict[str, tuple] = {}

    def _connector(self, service_url: str) -> ConnectorClient:
        MicrosoftAppCredentials.trust_service_url(service_url)
        return _only_connect_retries(ConnectorClient(self._credentials, base_url=service_url))

    def _teams_connector(self, service_url: str) -> TeamsConnectorClient:
        MicrosoftAppCredentials.trust_service_url(service_url)
        return _only_connect_retries(TeamsConnectorClient(self._credentials, base_url=service_url))

    def _call(self, fn: Callable[[], Any], retry_statuses=RETRY_READ_STATUSES, target: str = "") -> Any:
        """Retry what Microsoft says to retry, honouring Retry-After. Every
        failure surfaces as TeamsBotError, including token errors (a bad secret
        or tenant) and network errors, so callers can report it and move on."""
        waited = 0.0
        for attempt in range(1, MAX_ATTEMPTS + 1):
            try:
                return fn()
            except HttpOperationError as e:
                status = getattr(getattr(e, "response", None), "status_code", None)
                retry_after = getattr(e.response, "headers", {}).get("Retry-After")
                delay = float(retry_after) if retry_after and str(retry_after).isdigit() else 2**attempt
                delay += random.uniform(0, 0.5)
                if status in retry_statuses and attempt < MAX_ATTEMPTS and waited + delay <= MAX_RETRY_WAIT_SECONDS:
                    time.sleep(delay)
                    waited += delay
                    continue
                if status in retry_statuses:
                    LOG.warning(
                        "Microsoft Teams still returned %s for %s after %.1fs of retries", status, target, waited
                    )
                raise TeamsBotError(f"Microsoft Teams returned {status}", status=status) from e
            except Exception as e:
                raise TeamsBotError(f"Microsoft Teams request failed: {type(e).__name__}") from e
        raise TeamsBotError("Microsoft Teams request did not complete")

    def send(self, service_url: str, conversation_id: str, activity: Activity) -> str:
        """Post to a conversation: a channel id starts a new thread, a
        "<channel>;messageid=<root>" id replies in that thread. Returns the
        activity id, which is also the Teams message id."""
        try:
            response = self._call(
                lambda: self._connector(service_url).conversations.send_to_conversation(conversation_id, activity),
                RETRY_SEND_STATUSES,
                target=f"a send to {conversation_id}",
            )
        except TeamsBotError as e:
            if e.status == 403:
                raise TeamsBotError(CHANNEL_FORBIDDEN, status=403) from e
            raise
        return getattr(response, "id", None) or ""

    def update(self, service_url: str, conversation_id: str, activity_id: str, activity: Activity) -> None:
        """Replace a message the bot posted, e.g. a card after one of its buttons
        was used. Retries like a read: sending the same update twice is harmless."""
        activity.id = activity_id
        self._call(
            lambda: self._connector(service_url).conversations.update_activity(conversation_id, activity_id, activity),
            target=f"an update of {activity_id}",
        )

    def get_member(self, service_url: str, conversation_id: str, member_id: str) -> Dict[str, str]:
        """Name and email of a member of a conversation the bot is in. Email can
        be empty: Teams doesn't always return it."""
        member = self._call(
            lambda: self._connector(service_url).conversations.get_conversation_member(conversation_id, member_id)
        )
        properties = getattr(member, "additional_properties", None) or {}
        email = (
            getattr(member, "email", None)
            or properties.get("email")
            or getattr(member, "user_principal_name", None)
            or properties.get("userPrincipalName")
            or ""
        )
        return {"name": getattr(member, "name", None) or "", "email": email}

    def list_channels(self, team: TeamRef) -> List[Dict[str, str]]:
        """The team's channels, cached per process for a few minutes."""
        if not team.thread_id:
            return []
        cached = self._channel_cache.get(team.thread_id)
        if cached and cached[0] > time.monotonic():
            return cached[1]
        result = self._call(lambda: self._teams_connector(team.service_url).teams.get_teams_channels(team.thread_id))
        channels = [
            # The General channel has no name and shares the team's thread id.
            {"id": c.id, "name": c.name or "General"}
            for c in (getattr(result, "conversations", None) or [])
        ]
        self._channel_cache[team.thread_id] = (time.monotonic() + CHANNEL_CACHE_SECONDS, channels)
        return channels

    def forget_channels(self, team_thread_id: str) -> None:
        self._channel_cache.pop(team_thread_id, None)

    def check_channel(self, team: TeamRef, channel_id: str) -> None:
        """Raise unless the channel is one of the team's. The bot's credential
        reaches every customer that installed it, so a stored or supplied
        channel id must not be able to reach another customer's team. A miss
        re-reads the list once, for a channel created since it was cached."""
        channel_id = channel_id.split(";messageid=")[0]
        for refresh in (False, True):
            if refresh:
                self.forget_channels(team.thread_id)
            if any(c["id"] == channel_id for c in self.list_channels(team)):
                return
        raise TeamsBotError(CHANNEL_NOT_IN_TEAM)


CHANNEL_CHANGE_EVENTS = {"channelCreated", "channelDeleted", "channelRenamed", "channelRestored"}


def forget_changed_channels(activity) -> None:
    """A channel was added, removed or renamed: refresh that team's channel list
    on this pod's next picker load. Other pods catch up within the cache window."""
    channel_data = channel_data_of(activity)
    if activity.type != "conversationUpdate" or channel_data.get("eventType") not in CHANNEL_CHANGE_EVENTS:
        return
    team_thread_id = (channel_data.get("team") or {}).get("id")
    if team_thread_id:
        get_sender().forget_channels(team_thread_id)


_sender: Optional[TeamsBotSender] = None


def get_sender() -> TeamsBotSender:
    global _sender
    if _sender is None:
        _sender = TeamsBotSender()
    return _sender
