"""Unified Slack / MS Teams installation access across the legacy
`messaging_platforms` table and the new `integrations` storage.

Slack and MS Teams are migrating to integrations / integration_config_values with
their OAuth tokens encrypted at rest (security/secrets.py, byte-compatible with
api-server). During the migration both stores coexist: new installs write to
integrations, reads union both with the integration row winning. There is at most
one install per tenant per platform (enforced here at write time).

The objects returned are `MessagingPlatform`-shaped — decrypted token, default
destination rebuilt from the scalar default_* config values — so the existing
senders and channel-listers consume legacy and integration installs identically.
Each carries `_origin` so token refreshes persist back to the right store.
"""

import logging
import uuid
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from sqlalchemy import select

from notifications_server.models.models import (
    Integration,
    IntegrationConfigValue,
    MessagingPlatform,
)
from notifications_server.security.secrets import decrypt, encrypt
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

# Messaging integration `type` equals the platform string.
MESSAGING_PLATFORMS = ("slack", "ms_teams")

# Config-value field names. The encrypted set must mirror the IsEncrypted fields
# declared in api-server slack.go / ms_teams.go.
TOKEN_FIELD = {"slack": "bot_token", "ms_teams": "access_token"}
REFRESH_TOKEN_FIELD = "refresh_token"
_SECRET_FIELDS = {"bot_token", "access_token", "refresh_token"}

ORIGIN_LEGACY = "legacy"
ORIGIN_INTEGRATION = "integration"


# ---------------------------------------------------------------------------
# Pure helpers (no DB) — unit-testable on their own.
# ---------------------------------------------------------------------------
def _parse_dt(value: Optional[str]) -> Optional[datetime]:
    if not value:
        return None
    try:
        dt = datetime.fromisoformat(value)
    except ValueError:
        LOG.warning("messaging installation: unparseable datetime %r", value)
        return None
    # token_expires_at columns are naive UTC; normalize any timezone-aware value so
    # refresh-if-expired comparisons against naive utc_now() don't raise TypeError.
    if dt.tzinfo is not None:
        dt = dt.astimezone(timezone.utc).replace(tzinfo=None)
    return dt


def _to_str(value: Any) -> str:
    if isinstance(value, datetime):
        return value.isoformat()
    return str(value)


def _default_channel_shape(platform: str, cfg: Dict[str, str]) -> Optional[Any]:
    """Rebuild the legacy `channels` default-destination shape from scalar config
    so extract_channels (Slack) / Teams dispatch consume it unchanged."""
    channel_id = cfg.get("default_channel_id")
    if platform == "slack":
        return [{"id": channel_id}] if channel_id else None
    team_id = cfg.get("default_team_id")
    if team_id and channel_id:
        return {"team_id": team_id, "channels": [{"id": channel_id}]}
    return None


def _decrypt_config(rows: List[IntegrationConfigValue]) -> Dict[str, str]:
    cfg: Dict[str, str] = {}
    for row in rows:
        if row.is_encrypted and row.value:
            cfg[row.name] = decrypt(row.value)
        else:
            cfg[row.name] = row.value or ""
    return cfg


def build_installation(platform: str, integration: Integration, cfg: Dict[str, str]) -> MessagingPlatform:
    """Build a transient MessagingPlatform-shaped object from an integration row and
    its already-decrypted config values. Not attached to any session."""
    inst = MessagingPlatform()
    inst.id = integration.id
    inst.tenant_id = integration.tenant_id
    inst.platform = platform
    inst.team_id = integration.name
    inst.team_name = cfg.get("team_name")
    inst.client_id = cfg.get("client_id")
    inst.app_id = cfg.get("app_id")
    inst.username = cfg.get("installed_by")
    inst.scopes = cfg.get("scopes")
    inst.bot_id = cfg.get("bot_id")
    inst.token = cfg.get(TOKEN_FIELD[platform])
    inst.refresh_token = cfg.get(REFRESH_TOKEN_FIELD)
    inst.token_expires_at = _parse_dt(cfg.get("token_expires_at"))
    inst.refresh_token_expires_at = _parse_dt(cfg.get("refresh_token_expires_at"))
    inst.channels = _default_channel_shape(platform, cfg)
    inst._origin = ORIGIN_INTEGRATION
    inst._integration_id = integration.id
    return inst


def _tag_legacy(installations: List[MessagingPlatform]) -> List[MessagingPlatform]:
    for inst in installations:
        inst._origin = ORIGIN_LEGACY
    return installations


def _token_config(
    installation: MessagingPlatform,
    token: str,
    refresh_token: Optional[str],
    token_expires_at: Optional[datetime],
    refresh_token_expires_at: Optional[datetime],
) -> Dict[str, Any]:
    # token_expires_at is always written ("" clears it, e.g. when a dead refresh
    # token marks the install as needing re-auth); refresh fields only when given.
    config: Dict[str, Any] = {
        TOKEN_FIELD[installation.platform]: token,
        "token_expires_at": token_expires_at if token_expires_at is not None else "",
    }
    if refresh_token is not None:
        config[REFRESH_TOKEN_FIELD] = refresh_token
    if refresh_token_expires_at is not None:
        config["refresh_token_expires_at"] = refresh_token_expires_at
    return config


def _apply_token_to_instance(installation, token, refresh_token, token_expires_at, refresh_token_expires_at) -> None:
    installation.token = token
    installation.token_expires_at = token_expires_at  # may be None to force re-auth
    if refresh_token is not None:
        installation.refresh_token = refresh_token
    if refresh_token_expires_at is not None:
        installation.refresh_token_expires_at = refresh_token_expires_at


# ---------------------------------------------------------------------------
# Reads — integration-only fetchers plus a legacy-fallback union for single-platform
# callers. The send hot path (get_installed_platforms) composes its own legacy query.
# ---------------------------------------------------------------------------
def load_integration_installations(session, tenant_id, platform: str) -> List[MessagingPlatform]:
    """Sync: integration-backed installs for (tenant, platform), decrypted."""
    if not tenant_id:
        return []
    integrations = (
        session.query(Integration)
        .filter(
            Integration.tenant_id == tenant_id,
            Integration.type == platform,
            Integration.status != "disabled",
        )
        .all()
    )
    result = []
    for integ in integrations:
        rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integ.id).all()
        result.append(build_installation(platform, integ, _decrypt_config(rows)))
    return result


async def load_integration_installations_async(session, tenant_id, platform: str) -> List[MessagingPlatform]:
    """Async: integration-backed installs for (tenant, platform), decrypted."""
    if not tenant_id:
        return []
    integ_result = await session.execute(
        select(Integration).where(
            Integration.tenant_id == tenant_id,
            Integration.type == platform,
            Integration.status != "disabled",
        )
    )
    result = []
    for integ in integ_result.scalars().all():
        cfg_result = await session.execute(
            select(IntegrationConfigValue).where(IntegrationConfigValue.integration_id == integ.id)
        )
        result.append(build_installation(platform, integ, _decrypt_config(cfg_result.scalars().all())))
    return result


def load_installations(session, tenant_id, platform: str) -> List[MessagingPlatform]:
    """Sync union for (tenant, platform); integration rows win over legacy."""
    if not tenant_id:
        return []
    integration = load_integration_installations(session, tenant_id, platform)
    if integration:
        return integration
    legacy = (
        session.query(MessagingPlatform)
        .filter(MessagingPlatform.tenant_id == tenant_id, MessagingPlatform.platform == platform)
        .all()
    )
    return _tag_legacy(legacy)


def load_installation(session, tenant_id, platform: str) -> Optional[MessagingPlatform]:
    """Sync: the single install for (tenant, platform), or None (one per tenant)."""
    installs = load_installations(session, tenant_id, platform)
    return installs[0] if installs else None


def load_installation_by_team(session, team_id, platform: str, contains: bool = False) -> Optional[MessagingPlatform]:
    """Resolve a single install by its workspace/account id (integrations.name for
    integration installs, messaging_platforms.team_id for legacy). `contains` does a
    substring match (MS Teams resolves by a partial AAD account id)."""
    # Empty team_id with contains=True would be LIKE '%%' and match any tenant's row.
    if not team_id:
        return None
    query = session.query(Integration).filter(
        Integration.type == platform,
        Integration.status != "disabled",
    )
    query = query.filter(Integration.name.contains(team_id) if contains else Integration.name == team_id)
    integ = query.first()
    if integ:
        rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integ.id).all()
        return build_installation(platform, integ, _decrypt_config(rows))
    legacy_query = session.query(MessagingPlatform).filter(MessagingPlatform.platform == platform)
    legacy_query = legacy_query.filter(
        MessagingPlatform.team_id.contains(team_id) if contains else MessagingPlatform.team_id == team_id
    )
    legacy = legacy_query.first()
    if legacy:
        legacy._origin = ORIGIN_LEGACY
    return legacy


# ---------------------------------------------------------------------------
# Writes — install upsert (sync) and token-refresh persistence (sync + async).
# ---------------------------------------------------------------------------
def _find_integration(session, tenant_id, platform: str) -> Optional[Integration]:
    return (
        session.query(Integration)
        .filter(
            Integration.tenant_id == tenant_id,
            Integration.type == platform,
            Integration.source == "user",
        )
        .first()
    )


def _write_config_values(session, integration_id, config: Dict[str, Any]) -> None:
    existing = {
        row.name: row
        for row in session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integration_id)
    }
    for field, raw in config.items():
        if raw is None:
            continue
        value = _to_str(raw)
        encrypted = field in _SECRET_FIELDS and bool(value)
        stored = encrypt(value) if encrypted else value
        if field in existing:
            existing[field].value = stored
            existing[field].is_encrypted = encrypted
        else:
            session.add(
                IntegrationConfigValue(
                    id=uuid.uuid4(),
                    integration_id=integration_id,
                    name=field,
                    value=stored,
                    is_encrypted=encrypted,
                )
            )


async def _write_config_values_async(session, integration_id, config: Dict[str, Any]) -> None:
    cfg_result = await session.execute(
        select(IntegrationConfigValue).where(IntegrationConfigValue.integration_id == integration_id)
    )
    existing = {row.name: row for row in cfg_result.scalars().all()}
    for field, raw in config.items():
        if raw is None:
            continue
        value = _to_str(raw)
        encrypted = field in _SECRET_FIELDS and bool(value)
        stored = encrypt(value) if encrypted else value
        if field in existing:
            existing[field].value = stored
            existing[field].is_encrypted = encrypted
        else:
            session.add(
                IntegrationConfigValue(
                    id=uuid.uuid4(),
                    integration_id=integration_id,
                    name=field,
                    value=stored,
                    is_encrypted=encrypted,
                )
            )


def upsert_messaging_integration(session, tenant_id, platform: str, name: str, config: Dict[str, Any], created_by=None):
    """Create or update the single (tenant, platform) integration and its config
    values, encrypting bot_token/access_token/refresh_token. One-per-tenant: an
    existing row for (tenant, type, source='user') is reused, its name updated."""
    if not tenant_id:
        raise ValueError("tenant_id must be non-empty")
    integ = _find_integration(session, tenant_id, platform)
    now = utc_now()
    if integ is None:
        integ = Integration(
            id=uuid.uuid4(),
            tenant_id=tenant_id,
            type=platform,
            source="user",
            name=name,
            status="enabled",
            labels={},
            created_at=now,
            updated_at=now,
            created_by=created_by,
            updated_by=created_by,
        )
        session.add(integ)
        session.flush()
    else:
        integ.name = name
        integ.status = "enabled"
        integ.updated_at = now
        integ.updated_by = created_by
    _write_config_values(session, integ.id, config)
    session.commit()
    return integ


def persist_messaging_token(
    session,
    installation,
    token: str,
    refresh_token: Optional[str] = None,
    token_expires_at: Optional[datetime] = None,
    refresh_token_expires_at: Optional[datetime] = None,
) -> None:
    """Persist a refreshed token to the correct store (sync). Integration-origin
    installs re-encrypt into integration_config_values; legacy installs write the
    plaintext messaging_platforms columns."""
    _apply_token_to_instance(installation, token, refresh_token, token_expires_at, refresh_token_expires_at)
    if getattr(installation, "_origin", ORIGIN_LEGACY) == ORIGIN_INTEGRATION:
        _write_config_values(
            session,
            installation._integration_id,
            _token_config(installation, token, refresh_token, token_expires_at, refresh_token_expires_at),
        )
    else:
        session.add(installation)
    session.commit()


async def persist_messaging_token_async(
    session,
    installation,
    token: str,
    refresh_token: Optional[str] = None,
    token_expires_at: Optional[datetime] = None,
    refresh_token_expires_at: Optional[datetime] = None,
) -> None:
    """Async counterpart of persist_messaging_token for the send hot path."""
    _apply_token_to_instance(installation, token, refresh_token, token_expires_at, refresh_token_expires_at)
    if getattr(installation, "_origin", ORIGIN_LEGACY) == ORIGIN_INTEGRATION:
        await _write_config_values_async(
            session,
            installation._integration_id,
            _token_config(installation, token, refresh_token, token_expires_at, refresh_token_expires_at),
        )
    else:
        session.add(installation)
    await session.commit()
