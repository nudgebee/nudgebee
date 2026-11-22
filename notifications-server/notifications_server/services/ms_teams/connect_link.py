"""Connect a Microsoft Teams team to a workspace from the bot.

While a team isn't connected, the bot's welcome card in it carries a link for
the person who added the bot, and an @mention of the bot gets a link for the
person who mentioned it. Signed in to NudgeBee as an admin with that same email,
they open it and confirm, which binds the team to their workspace.

The link is bound to that person rather than to their email domain, so another
employee can't claim the team for a workspace of their own. It names them only
by a keyed hash of their sign-in name (UPN): the card sits in channel history,
readable by every member and guest. Guests, other tenants, personal accounts,
public mailbox domains and admin-only accounts get no link."""

import asyncio
import base64
import hashlib
import hmac
import json
import logging
import time
from datetime import datetime, timezone
from typing import Dict, Optional, Tuple

from notifications_server.configs.settings import settings
from notifications_server.services.cache import Cache
from notifications_server.services.ms_teams import card_actions
from notifications_server.services.ms_teams.activity import channel_data_of
from notifications_server.services.ms_teams.bot_sender import TeamRef, TeamsBotError, get_sender
from notifications_server.services.ms_teams.team_binding import bind_team
from notifications_server.utils.action_requests import ActionRequestBody, verify_action_request

LOG = logging.getLogger(__name__)

ACTION_NAME = "ms_teams_connect"
ORIGIN = "ms_teams"
# The tenant of personal Microsoft accounts (Teams free).
CONSUMER_TENANT_ID = "9188040d-6c67-4c5b-b112-36a304b66dad"
PUBLIC_MAIL_DOMAINS = frozenset(
    {
        "126.com",
        "163.com",
        "aol.com",
        "fastmail.com",
        "gmail.com",
        "gmx.com",
        "gmx.de",
        "gmx.net",
        "googlemail.com",
        "hey.com",
        "hotmail.co.uk",
        "hotmail.com",
        "hotmail.de",
        "hotmail.fr",
        "hotmail.it",
        "icloud.com",
        "live.co.uk",
        "live.com",
        "live.in",
        "mac.com",
        "mail.com",
        "mail.ru",
        "me.com",
        "msn.com",
        "naver.com",
        "outlook.com",
        "outlook.in",
        "pm.me",
        "proton.me",
        "protonmail.com",
        "qq.com",
        "rediffmail.com",
        "rocketmail.com",
        "tuta.io",
        "tutanota.com",
        "web.de",
        "yahoo.co.in",
        "yahoo.co.uk",
        "yahoo.com",
        "yahoo.de",
        "yahoo.fr",
        "yandex.com",
        "yandex.ru",
        "ymail.com",
        "zoho.com",
        "zohomail.com",
    }
)
INVALID, EXPIRED, WRONG_USER = "invalid", "expired", "wrong_user"
RATE_LIMITED = "rate_limited"
LINK_ALREADY_SENT = "Use the link I posted a few minutes ago."
# One link per person per team from @mentions in this window.
MENTION_LINK_SECONDS = 300
TEAM_FIELDS = ("team_aad_group_id", "team_thread_id", "team_name", "service_url", "aad_tenant_id", "app_id")
INSTALLER_HASH_PREFIX = "ms_teams_connect_installer:"

IDENTITY_CACHE_SECONDS = 600
IDENTITY_CACHE_SIZE = 1000
# Separate from identity's email cache: this one holds the sign-in name, and
# only this module reads it.
_identities: Dict[Tuple[str, str], Tuple[Dict[str, str], float]] = {}


def email_domain(value) -> Optional[str]:
    """The lowercased text after the "@", or None unless value is one address
    with exactly one "@", ASCII once lowercased, with no surrounding whitespace
    and no trailing dot. Domains compare as bytes: a subdomain or a parent
    domain is a different domain."""
    if not isinstance(value, str) or value != value.strip() or value.count("@") != 1:
        return None
    lowered = value.lower()
    if not lowered.isascii() or lowered.endswith("."):
        return None
    return lowered.split("@", 1)[1] or None


def unavailable_reason(activity) -> Optional[str]:
    """Why Connect from the bot is off here: key_unset without the signing key,
    not_channel outside a team channel. None when it's on. Whether the team is
    connected is the caller's check."""
    if not card_actions.buttons_enabled():
        return "key_unset"
    channel_data = channel_data_of(activity)
    if not ((channel_data.get("team") or {}).get("aadGroupId") and (channel_data.get("tenant") or {}).get("id")):
        return "not_channel"
    return None


def offerable(activity) -> bool:
    """Whether Connect from the bot is on here: a team channel, with the signing key set."""
    return unavailable_reason(activity) is None


def refusal_reason(identity: Dict[str, str], channel_tenant_id: str) -> Optional[str]:
    """Why no link may be minted for this member, or None when one may: a guest,
    someone from another tenant (a shared channel), a personal account, a public
    mailbox, or an admin-only account (corp.onmicrosoft.com), which often adds
    apps to teams but is nobody's login."""
    upn = identity.get("upn") or ""
    tenant_id = (identity.get("tenant_id") or "").lower()
    if not upn:
        return "no_upn"
    if "#EXT#" in upn.upper():
        return "guest"
    if identity.get("user_role") != "user":
        return "role"
    if not tenant_id or tenant_id != (channel_tenant_id or "").lower():
        return "cross_tenant"
    if tenant_id == CONSUMER_TENANT_ID:
        return "consumer_tenant"
    domain = email_domain(upn)
    if not domain:
        return "malformed_upn"
    if domain in PUBLIC_MAIL_DOMAINS:
        return "public_domain"
    if domain.endswith(".onmicrosoft.com"):
        return "onmicrosoft"
    return None


def installer_hash(email: str) -> str:
    """What a link carries in place of the installer's sign-in name, and what a
    login email must hash to: HMAC-SHA256 under the signing key, of the address
    stripped and lowercased."""
    message = (INSTALLER_HASH_PREFIX + email.strip().lower()).encode()
    return hmac.new(settings.ms_teams.action_signing_key.encode(), message, hashlib.sha256).hexdigest()


def _remember(key: Tuple[str, str], identity: Dict[str, str]) -> None:
    """Cache a lookup, first dropping expired ones and, at IDENTITY_CACHE_SIZE, the oldest."""
    now = time.monotonic()
    for stale in [k for k, (_, expires) in _identities.items() if expires <= now or k == key]:
        del _identities[stale]
    while len(_identities) >= IDENTITY_CACHE_SIZE:
        del _identities[next(iter(_identities))]
    _identities[key] = (identity, now + IDENTITY_CACHE_SECONDS)


async def member_identity(activity) -> Optional[Dict[str, str]]:
    """The sender's sign-in name, home tenant and role, cached per process for a
    few minutes. None when the lookup can't be made or fails."""
    sender = activity.from_property
    channel_data = channel_data_of(activity)
    team_id = (channel_data.get("team") or {}).get("id")
    if not (sender and sender.id and team_id and activity.service_url):
        return None
    key = ((channel_data.get("tenant") or {}).get("id") or "", sender.id)
    cached = _identities.get(key)
    if cached and cached[1] > time.monotonic():
        return cached[0]
    try:
        identity = await asyncio.to_thread(get_sender().get_member_identity, activity.service_url, team_id, sender.id)
    except TeamsBotError as e:
        LOG.warning("Teams member lookup for a connect link failed: %s", e)
        return None
    _remember(key, identity)
    return identity


async def _team_name(activity, team: dict) -> str:
    """When the event leaves the team name out, read it from Microsoft."""
    ref = TeamRef(
        integration_id=None,
        tenant_id=None,
        aad_group_id=team.get("aadGroupId") or "",
        thread_id=team.get("id") or "",
        name="",
        service_url=activity.service_url,
    )
    try:
        return await asyncio.to_thread(get_sender().team_name, ref)
    except TeamsBotError as e:
        LOG.warning("Reading the name of Teams team %s failed: %s", ref.aad_group_id, e)
        return ""


def mint(params: dict) -> str:
    """The signed {kid, body, signature} envelope card buttons use, base64url."""
    envelope = card_actions.sign(ACTION_NAME, params)
    return base64.urlsafe_b64encode(envelope.encode()).decode().rstrip("=")


async def _installer_link(activity) -> Tuple[Optional[str], Optional[str]]:
    """(the connect page with a link for the activity's sender, None), or
    (None, why there's no link). The sender is whoever added the bot, for the
    welcome card, or whoever mentioned it."""
    reason = unavailable_reason(activity)
    if reason:
        return None, reason
    installer = getattr(activity, "from_property", None)
    if not (installer and installer.id):
        return None, "no_installer"
    identity = await member_identity(activity)
    if not identity:
        return None, "lookup_failed"
    channel_data = channel_data_of(activity)
    aad_tenant_id = (channel_data.get("tenant") or {}).get("id") or ""
    reason = refusal_reason(identity, aad_tenant_id)
    if reason:
        return None, reason
    team = channel_data.get("team") or {}
    recipient_id = (activity.recipient.id if activity.recipient else "") or ""
    params = {
        "aad_tenant_id": aad_tenant_id,
        "team_aad_group_id": team.get("aadGroupId") or "",
        "team_thread_id": team.get("id") or "",
        "team_name": team.get("name") or await _team_name(activity, team),
        "service_url": activity.service_url or "",
        "app_id": recipient_id.split(":", 1)[-1],
        "installer": installer_hash(identity["upn"]),
    }
    return f"{settings.base_url}/integrations/ms-teams/connect#t={mint(params)}", None


async def link_url(activity) -> Optional[str]:
    """For the welcome card: the connect page with a link for the person who
    added the bot, or None when there can't be one. Why there's none is logged;
    who it was never is."""
    url, reason = await _installer_link(activity)
    if reason:
        aad_tenant_id = (channel_data_of(activity).get("tenant") or {}).get("id")
        LOG.info("No connect link on the Teams welcome card for Microsoft tenant %s: %s", aad_tenant_id, reason)
    return url


async def mention_link(activity) -> Tuple[Optional[str], Optional[str]]:
    """For an @mention in a team that isn't connected: (the connect page with a
    link for whoever mentioned the bot, None), or (None, why there's none).
    RATE_LIMITED when that person already got one in this team within
    MENTION_LINK_SECONDS."""
    url, reason = await _installer_link(activity)
    if reason:
        aad_tenant_id = (channel_data_of(activity).get("tenant") or {}).get("id")
        LOG.info("No connect link for an @mention in Microsoft tenant %s: %s", aad_tenant_id, reason)
        return None, reason
    team_id = (channel_data_of(activity).get("team") or {}).get("aadGroupId")
    sender = activity.from_property
    person = sender.aad_object_id or sender.id
    if not await Cache().aio.claim_teams_connect_link(team_id, person, MENTION_LINK_SECONDS):
        return None, RATE_LIMITED
    return url, None


def _checked(token) -> Optional[Tuple[dict, float]]:
    """(params, age in seconds) of a link we signed, or None. Like
    card_actions._checked, without its workspace check: a link is minted
    before any workspace is known."""
    try:
        payload = json.loads(base64.urlsafe_b64decode(token + "=" * (-len(token) % 4)))
        if payload.get("kid") != card_actions.KEY_ID:
            return None
        body = ActionRequestBody(**(payload.get("body") or {}))
        if body.origin != ORIGIN or body.action_name != ACTION_NAME:
            return None
        if not verify_action_request(body, payload.get("signature") or "", settings.ms_teams.action_signing_key):
            return None
    except Exception:
        return None
    return body.action_params or {}, time.time() - body.timestamp


def verify(token) -> Tuple[Optional[dict], Optional[str]]:
    """(params, None) for a link we signed within MAX_AGE_SECONDS, else (None, INVALID or EXPIRED)."""
    checked = _checked(token)
    if not checked or checked[1] < -300:
        return None, INVALID
    if checked[1] > card_actions.MAX_AGE_SECONDS:
        return None, EXPIRED
    return checked[0], None


def _login(user_email) -> Optional[str]:
    """The session's login email stripped and lowercased, or None unless it has
    exactly one "@" and is ASCII."""
    if not isinstance(user_email, str):
        return None
    login = user_email.strip().lower()
    if login.count("@") != 1 or not login.isascii():
        return None
    return login


def redeem(session, token, tenant_id, user_email: str) -> str:
    """Bind the link's team to tenant_id, as user_email, which must be the
    sign-in name of the person the link was minted for. Returns CONNECTED or a
    refusal code."""
    params, refusal = verify(token)
    if refusal:
        return refusal
    minted_at = datetime.fromtimestamp(time.time() - _checked(token)[1], tz=timezone.utc)
    installer = params.get("installer")
    team = {k: params.get(k) or "" for k in TEAM_FIELDS}
    if not (
        installer
        and isinstance(installer, str)
        and all(isinstance(team[k], str) for k in TEAM_FIELDS)
        and team["aad_tenant_id"]
        and team["team_aad_group_id"]
    ):
        return INVALID
    login = _login(user_email)
    if not login or not hmac.compare_digest(installer_hash(login).encode(), installer.encode()):
        return WRONG_USER
    return bind_team(
        session, team, tenant_id, actor=user_email, audit_attr={"confirmed_by": user_email}, minted_at=minted_at
    )
