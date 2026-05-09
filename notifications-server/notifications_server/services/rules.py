import asyncio
import logging
import re
import uuid
from datetime import datetime, timezone

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm.attributes import flag_modified

from notifications_server.exceptions.common_exc import BeeHTTPError
from notifications_server.exceptions.exceptions import Err
from notifications_server.exceptions.notification_errors import InvalidRuleError
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventAction, EventStatus, EventActor
from notifications_server.models.models import NotificationRules, NotificationRuleMappings
from notifications_server.services.audit import create_audit_request
from notifications_server.services.cache import Cache
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

# PostgreSQL unique violation error code
UNIQUE_VIOLATION = "23505"


def is_valid_string(s):
    pattern = r"^[A-Za-z][\w\s-]*$"
    return bool(re.match(pattern, s))


def validate_fields(fields, is_update=False):
    source = fields.get("source", None)
    name = fields.get("name", None)
    if not name or not is_valid_string(name):
        raise InvalidRuleError(Err.OS0011.value[0] % "name")
    if source is None:
        raise InvalidRuleError(Err.OS0010.value[0] % "source")
    if not is_update and source != "daily_recap" and fields.get("account_id") is None:
        raise InvalidRuleError(Err.OS0010.value[0] % "account_id")
    return None


EMAIL_PATTERN = re.compile(r"^[a-zA-Z0-9._%+-]+@(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$")


def _is_valid_email(addr: str) -> bool:
    return bool(EMAIL_PATTERN.match(addr))


def _extract_emails_from_item(item) -> list[str]:
    """Extract email addresses from a string or dict item."""
    if isinstance(item, str):
        return [addr for addr in (m.strip() for m in item.split(",")) if addr]
    if isinstance(item, dict):
        return [item.get("email") or item.get("address")]
    raise InvalidRuleError(Err.OS0011.value[0] % "email channel type")


def _collect_emails_from_list(email_list: list, field_name: str) -> list[str]:
    """Collect and flatten emails from a list of items."""
    emails = []
    for mail in email_list:
        extracted = _extract_emails_from_item(mail)
        valid_emails = [e for e in extracted if e]
        if not valid_emails:
            raise InvalidRuleError(Err.OS0010.value[0] % f"{field_name} address")
        emails.extend(valid_emails)
    return emails


def _validate_email_list(email_list, field_name: str = "email"):
    """Validate a list of email items."""
    if not email_list:
        return
    emails = _collect_emails_from_list(email_list, field_name)
    for email in emails:
        if not _is_valid_email(email):
            raise InvalidRuleError(Err.OS0011.value[0] % f"{field_name} address: {email}")


def _normalize_channels_to_list(channels) -> list:
    """Normalize channels input to a list format."""
    if isinstance(channels, str):
        return [channels]
    if isinstance(channels, list):
        return channels
    raise InvalidRuleError(Err.OS0011.value[0] % "email channels format")


def validate_email_channels(channels):
    """Validate email channels in either new dict format or legacy list format."""
    # Handle new format: {"emails": [...], "exclusion_emails": [...]}
    if isinstance(channels, dict) and ("emails" in channels or "exclusion_emails" in channels):
        _validate_email_list(channels.get("emails"), "email")
        _validate_email_list(channels.get("exclusion_emails"), "exclusion email")
        return

    # Handle legacy format: array of emails or string
    channels_list = _normalize_channels_to_list(channels)
    _validate_email_list(channels_list, "email")


class NotificationRulesService:
    def __init__(self, engine):
        self.engine = engine
        self.cache = Cache()

    async def save_or_update_rule(self, **kwargs):
        rule_name = None
        async with BaseDB.async_session(self.engine)() as session:
            try:
                action = kwargs.get("action")
                if action.get("name") != "notification_rule_upsert_one":
                    raise BeeHTTPError(400, Err.OS0004, [])

                rule_data = kwargs["input"]["rule"]
                sess_vars = kwargs["session_variables"]
                tenant_id, user_id = sess_vars["x-hasura-user-tenant-id"], sess_vars["x-hasura-user-id"]

                rule_id = rule_data.get("id")
                rule_name = rule_data.get("name")
                source = rule_data.get("source")

                fields = {k: v for k, v in rule_data.items() if hasattr(NotificationRules, k)}
                if "expires_at" in fields and isinstance(fields["expires_at"], str):
                    parsed = datetime.fromisoformat(fields["expires_at"])
                    fields["expires_at"] = (
                        parsed.astimezone(timezone.utc).replace(tzinfo=None) if parsed.tzinfo else parsed
                    )
                existing_rule = await self._get_existing_rule(session, rule_id, source, tenant_id)

                validate_fields(fields, is_update=existing_rule is not None)

                if existing_rule:
                    self._update_existing_rule(existing_rule, fields, user_id)
                    rule_id = existing_rule.id
                else:
                    rule_id = self._create_new_rule(session, fields, tenant_id, user_id, rule_data)
                    await session.flush()

                if rule_data.get("mappings") is not None:
                    await self._save_mappings_data(
                        session, rule_id, bool(existing_rule), user_id, tenant_id, rule_data["mappings"]
                    )

                # Double-invalidation: clear before commit to prevent concurrent reads
                # from re-caching stale data during the commit window
                self._invalidate_cache(tenant_id)
                await session.commit()
                self._invalidate_cache(tenant_id)
                await self._create_audit_entry(rule_id, tenant_id, user_id, fields, is_update=existing_rule is not None)

                return {"id": str(rule_id)}

            except IntegrityError as e:
                await session.rollback()
                error_msg = self._handle_integrity_error(e, rule_name)
                is_duplicate = "already exists" in error_msg or "unique constraint" in error_msg.lower()
                return {"error": error_msg, "status_code": 409 if is_duplicate else 500}

            except InvalidRuleError as e:
                LOG.warning("Invalid rule data: %s", e)
                return {"error": str(e), "status_code": 400}

            except BeeHTTPError as e:
                LOG.exception("BeeHTTPError while saving notification rule: %s", e)
                return {"error": str(e), "status_code": e.status_code}

            except Exception as e:
                LOG.exception("Unexpected error saving notification rule: %s", e)
                return {"error": "Unable to save or update notification rule", "status_code": 500}

    @staticmethod
    def _handle_integrity_error(e, rule_name):
        orig = getattr(e, "orig", None)
        # Check PostgreSQL error code - works with both psycopg2 (pgcode) and asyncpg (sqlstate)
        pgcode = getattr(orig, "pgcode", None) or getattr(orig, "sqlstate", None)
        if pgcode == UNIQUE_VIOLATION:
            constraint = getattr(getattr(orig, "diag", None), "constraint_name", None) or getattr(
                orig, "constraint_name", None
            )
            if constraint == "notification_rules_tenant_id_name_key":
                return f"A notification rule with the name '{rule_name}' already exists"
            return "Duplicate value violates unique constraint"
        LOG.exception("IntegrityError while saving notification rule")
        return "Unable to save notification rule, integrity constraint error"

    @staticmethod
    async def _get_existing_rule(session, rule_id, source, tenant_id):
        if source == "daily_recap":
            result = await session.execute(
                select(NotificationRules).filter_by(source=source, tenant_id=tenant_id).with_for_update()
            )
            return result.scalars().one_or_none()
        if not rule_id:
            return None
        result = await session.execute(
            select(NotificationRules).filter_by(id=rule_id, tenant_id=tenant_id).with_for_update()
        )
        return result.scalars().one_or_none()

    @staticmethod
    def _update_existing_rule(existing_rule, fields, user_id):
        nullable_fields = {
            "account_id",
            "description",
            "cluster",
            "namespace",
            "workload",
            "aggregation_key",
            "expires_at",
            "delivery_mode",
            "frequency",
            "severity",
        }

        for key, value in fields.items():
            if key != "id":
                setattr(existing_rule, key, value)

        for f in nullable_fields - fields.keys():
            if hasattr(existing_rule, f):
                setattr(existing_rule, f, None)

        existing_rule.updated_by = user_id
        existing_rule.updated_at = utc_now()

    @staticmethod
    def _create_new_rule(session, fields, tenant_id, user_id, rule_data):
        rule_id = uuid.uuid4()
        rule = NotificationRules(
            id=rule_id,
            tenant_id=tenant_id,
            created_by=user_id,
            updated_by=user_id,
            created_at=utc_now(),
            updated_at=utc_now(),
            is_active=rule_data.get("is_active", True),
            **{k: v for k, v in fields.items() if k != "id"},
        )
        session.add(rule)
        return rule_id

    @staticmethod
    async def _save_mappings_data(session, rule_id, is_update, user_id, tenant_id, new_mappings):
        existing_mappings = {}
        if is_update:
            result = await session.execute(select(NotificationRuleMappings).filter_by(rule_id=rule_id))
            existing_mappings = {m.platform: m for m in result.scalars().all()}

        new_platforms = {m["platform"] for m in new_mappings}
        now = utc_now()

        for m in new_mappings:
            platform, channels = m["platform"], m["channels"]

            if channels and platform.lower() == "email":
                validate_email_channels(channels)

            existing = existing_mappings.get(platform)

            if existing:
                if existing.channels != channels:
                    LOG.info("Updating mapping for platform: %s", platform)
                    existing.channels = channels
                    flag_modified(existing, "channels")
                    existing.updated_by = user_id
                    existing.updated_at = now
            else:
                LOG.debug("Creating new mapping for platform: %s", platform)
                session.add(
                    NotificationRuleMappings(
                        id=uuid.uuid4(),
                        rule_id=rule_id,
                        platform=platform,
                        channels=channels,
                        created_at=now,
                        updated_at=now,
                        created_by=user_id,
                        updated_by=user_id,
                        tenant_id=tenant_id,
                    )
                )

        if is_update:
            for platform in existing_mappings.keys() - new_platforms:
                LOG.debug("Removing mapping for platform: %s", platform)
                await session.delete(existing_mappings[platform])

    def _invalidate_cache(self, tenant_id):
        if self.cache and self.cache.redis_client:
            self.cache.delete_cached_notification_rules(tenant_id)
            LOG.info("Cache invalidated for tenant: %s", tenant_id)

    @staticmethod
    async def _create_audit_entry(rule_id, tenant_id, user_id, fields, is_update=False, prev_state=None):
        event_type = EventType.NOTIFICATION_RULE_UPDATE.value if is_update else EventType.NOTIFICATION_RULE_CREATE.value
        event_action = EventAction.UPDATE.value if is_update else EventAction.CREATE.value
        try:
            await asyncio.to_thread(
                create_audit_request,
                user_id=user_id,
                tenant_id=tenant_id,
                account_id=None,
                event_time=utc_now(),
                event_category=EventCategory.NOTIFICATION_RULES.value,
                event_type=event_type,
                event_prev_state=prev_state,
                event_state={"id": str(rule_id)},
                event_actor=EventActor.NOTIFICATION_SERVICE.value,
                event_target="notification_rules",
                event_action=event_action,
                event_status=EventStatus.SUCCESS.value,
                event_attr=fields,
            )
            LOG.info("Audit request created for rule id: %s", rule_id)
        except Exception as e:
            LOG.error("Failed to create audit entry for rule %s: %s", rule_id, e)

    async def get_notification_rules(self, tenant_id):
        if self.cache and self.cache.redis_client:
            cached = self.cache.get_notification_rules(tenant_id)
            if cached:
                return cached

        async with BaseDB.async_session(self.engine)() as session:
            try:
                result = await session.execute(select(NotificationRules).filter_by(tenant_id=tenant_id, is_active=True))
                rules = result.scalars().all()
                if rules and self.cache and self.cache.redis_client:
                    self.cache.cache_notification_rules(tenant_id, rules)
                return rules
            except Exception as e:
                LOG.exception("Error getting notification rules for tenant %s: %s", tenant_id, e)
                return []
