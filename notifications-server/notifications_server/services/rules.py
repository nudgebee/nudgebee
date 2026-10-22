import asyncio
import logging
import re
import uuid
from datetime import datetime, timezone

from sqlalchemy import delete, inspect, select, text
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.exc import IntegrityError, ProgrammingError
from sqlalchemy.orm import load_only
from sqlalchemy.orm.attributes import flag_modified, set_committed_value

from notifications_server.exceptions.common_exc import BeeHTTPError
from notifications_server.exceptions.exceptions import Err
from notifications_server.exceptions.notification_errors import InvalidRuleError
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventAction, EventStatus, EventActor
from notifications_server.models.models import (
    CloudAccount,
    NotificationDefaultPolicy,
    NotificationRuleAccounts,
    NotificationRuleMappings,
    NotificationRules,
)
from notifications_server.services.audit import create_audit_request
from notifications_server.services.cache import Cache
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

# PostgreSQL unique violation error code
UNIQUE_VIOLATION = "23505"

# Undefined column / undefined table. Pods of a new release can start before
# its migration has run, so the model may map a column the database lacks yet.
MISSING_SCHEMA_SQLSTATES = {"42703", "42P01"}

_warned_missing_schema = False

# The rule columns a save may set from its input. The rest (id, tenant_id,
# created_*, updated_*) are server-owned and never read from the request.
CLIENT_RULE_FIELDS = frozenset(
    {
        "name",
        "description",
        "source",
        "source_mode",
        "sources",
        "account_id",
        "cluster",
        "scope_mode",
        "exceptions",
        "namespace",
        "workload",
        "aggregation_key",
        "expires_at",
        "is_suppressed",
        "is_active",
        "delivery_mode",
        "frequency",
        "severity",
        "event_types",
    }
)


def _is_missing_schema(exc):
    orig = getattr(exc, "orig", None)
    return (getattr(orig, "sqlstate", None) or getattr(orig, "pgcode", None)) in MISSING_SCHEMA_SQLSTATES


async def _existing_rule_columns(session):
    result = await session.execute(
        text(
            "SELECT column_name FROM information_schema.columns "
            "WHERE table_name = :table AND table_schema = ANY (current_schemas(false))"
        ),
        {"table": NotificationRules.__tablename__},
    )
    return {row[0] for row in result}


async def _load_rules_with_existing_columns(session, tenant_id):
    """Load active rules selecting only the columns the database has. Columns the
    model maps but the database lacks are set to None on each loaded rule, so a
    newly added column must give None its pre-migration meaning in the matcher,
    never "match everything"."""
    present = await _existing_rule_columns(session)
    column_attrs = inspect(NotificationRules).column_attrs
    loadable = [attr for attr in column_attrs if attr.columns[0].name in present]
    missing = [attr.key for attr in column_attrs if attr.columns[0].name not in present]
    if not loadable:
        # The table itself does not exist yet, so there are no rules to load.
        return [], missing
    result = await session.execute(
        select(NotificationRules)
        .options(load_only(*[getattr(NotificationRules, attr.key) for attr in loadable]))
        .filter_by(tenant_id=tenant_id, is_active=True)
    )
    rules = result.scalars().all()
    for rule in rules:
        for key in missing:
            set_committed_value(rule, key, None)
        rule.missing_columns = frozenset(missing)
    return rules, missing


def is_valid_string(s):
    pattern = r"^[A-Za-z][\w\s-]*$"
    return bool(re.match(pattern, s))


# Tenant-wide categories: their notifications are aggregated across every
# account, so there is no single account_id to scope the rule to and an
# account-scoped rule could never match (#28130).
#   daily_recap        — one nightly digest per tenant
#   weekly_digest      — the weekly event-analysis digest, one per tenant-week
#   ai_cost            — the AI Cost Daily Report digest, one per tenant-day
#   llm_budget         — AI Gateway budget alerts, one per quota threshold per window
TENANT_WIDE_SOURCES = ("daily_recap", "weekly_digest", "ai_cost", "llm_budget")

# Automation and optimize messages carry an account, but a rule may cover every account.
AUTOMATION_SOURCE = "automation"
OPTIMIZE_SOURCE = "optimize"
ACCOUNT_OPTIONAL_SOURCES = TENANT_WIDE_SOURCES + (AUTOMATION_SOURCE, OPTIMIZE_SOURCE)

# Every type a rule can cover. The singletons are each managed as the tenant's
# only rule of its kind, so they are never combined with other types and an
# all-types rule leaves them out.
RULE_SOURCES = (
    "troubleshoot",
    "slo",
    "cloud",
    "optimize",
    "automation",
    "weekly_digest",
    "daily_recap",
    "ai_cost",
    "llm_budget",
)
SINGLETON_SOURCES = ("daily_recap", "ai_cost", "llm_budget")
# Delivered only where the tenant has a rule for them: with none, they drop instead of
# going to the default channel.
OPT_IN_SOURCES = ("llm_budget",)
ALL_TYPES_SOURCES = tuple(s for s in RULE_SOURCES if s not in SINGLETON_SOURCES)
SOURCE_MODE_ALL = "all"
SOURCE_MODE_LIST = "list"
# The mirror written to source for an all-types rule.
ALL_TYPES_MIRROR_SOURCE = "troubleshoot"


def rule_sources(rule):
    """The types a rule covers."""
    mode = getattr(rule, "source_mode", None)
    if mode == SOURCE_MODE_ALL:
        return ALL_TYPES_SOURCES
    if mode == SOURCE_MODE_LIST and getattr(rule, "sources", None):
        return tuple(rule.sources)
    return (rule.source,)


# The event types an automation rule may select. Kept in step with the Event*
# constants in runbook-server's notification service and the catalogue in the
# rule modal. Approval requests are absent on purpose: they are never
# suppressible, so a rule has nothing to say about them.
AUTOMATION_EVENT_TYPES = (
    "automation.task.completed",
    "automation.task.failed",
    "automation.task.progress",
    "automation.pr.created",
    "automation.pr.failed",
)

# Optimize messages are told apart by their message type; a rule selects them by
# these event ids. A rule without event types covers all of them.
OPTIMIZE_EVENT_BY_MESSAGE_TYPE = {
    "recommendation_resolution": "optimize.recommendation.applied",
    "finding": "optimize.alert.fixed",
    "recommendation_nudge_digest": "optimize.digest",
    "recommendation_proactive_nudge": "optimize.nudge",
    "security_posture_alert": "optimize.security_posture",
}
OPTIMIZE_EVENT_TYPES = tuple(OPTIMIZE_EVENT_BY_MESSAGE_TYPE.values())


SCOPE_ALL = "all"
SCOPE_ACCOUNTS = "accounts"

# What happens to a notification no rule decides; no row means DEFAULT_DELIVER.
DEFAULT_DELIVER = "deliver_defaults"
DEFAULT_CHANNELS = "deliver_channels"
DEFAULT_DROP = "drop"
DEFAULT_ACTIONS = (DEFAULT_DELIVER, DEFAULT_CHANNELS, DEFAULT_DROP)
# The row every type falls back to when it has none of its own.
DEFAULT_ALL_SOURCES = "all"
# Removes a type's own row, so it follows the all-types row again.
DEFAULT_INHERIT = "inherit"
DEFAULT_POLICY_SOURCES = (
    DEFAULT_ALL_SOURCES,
    "troubleshoot",
    "optimize",
    "slo",
    "cloud",
    "automation",
    "daily_recap",
    "weekly_digest",
)

EXCEPTION_KEYS = ("account_ids", "namespaces", "workloads", "aggregation_keys")
MAX_EXCEPTION_GROUPS = 20
MAX_EXCEPTION_VALUES = 100


def rule_sort_key(rule):
    # Matching is order-free; this only keeps channel lists deterministic.
    return (getattr(rule, "created_at", None) or datetime.min, str(rule.id))


def normalize_exceptions(raw):
    """Validate a rule's exceptions: a list of groups, each mapping keys from
    EXCEPTION_KEYS to non-empty lists of strings. Returns the cleaned list (an
    empty list clears them) or raises InvalidRuleError."""
    if raw is None:
        return []
    if not isinstance(raw, list) or len(raw) > MAX_EXCEPTION_GROUPS:
        raise InvalidRuleError(Err.OS0011.value[0] % "exceptions")
    groups = []
    for group in raw:
        if not isinstance(group, dict) or not group or set(group) - set(EXCEPTION_KEYS):
            raise InvalidRuleError(Err.OS0011.value[0] % "exceptions")
        cleaned = {}
        for key, values in group.items():
            if (
                not isinstance(values, list)
                or not values
                or len(values) > MAX_EXCEPTION_VALUES
                or not all(isinstance(v, str) and v.strip() for v in values)
            ):
                raise InvalidRuleError(Err.OS0011.value[0] % f"exceptions.{key}")
            cleaned[key] = list(dict.fromkeys(v.strip() for v in values))
        groups.append(cleaned)
    return groups


def _resolve_scope(rule_data, sources, existing_rule, existing_account_ids):
    """Return (scope_mode, account_ids) for a save. Older clients send only
    account_id. A rule scoped to accounts is only widened by an explicit
    scope_mode of "all", which older clients cannot send."""
    account_ids = rule_data.get("account_ids")
    sent_account_ids = account_ids is not None
    if account_ids is None:
        account_ids = [rule_data["account_id"]] if rule_data.get("account_id") else []
    account_ids = list(dict.fromkeys(str(a) for a in account_ids if a))
    scope_mode = rule_data.get("scope_mode")

    if all(s in TENANT_WIDE_SOURCES for s in sources):
        # Tenant-wide notifications carry no account, so these rules only make sense for every account.
        return SCOPE_ALL, []

    if scope_mode not in (None, SCOPE_ALL, SCOPE_ACCOUNTS):
        raise InvalidRuleError(Err.OS0011.value[0] % "scope_mode")
    if not sent_account_ids and len(existing_account_ids) > 1 and account_ids:
        raise InvalidRuleError("This rule covers several accounts; update it with account_ids, not account_id")
    if scope_mode is None:
        if account_ids:
            scope_mode = SCOPE_ACCOUNTS
        elif existing_rule is not None and existing_rule.scope_mode == SCOPE_ACCOUNTS:
            raise InvalidRuleError(Err.OS0010.value[0] % "account_ids")
        else:
            scope_mode = SCOPE_ALL

    keeps_existing_all = existing_rule is not None and existing_rule.scope_mode == SCOPE_ALL and scope_mode == SCOPE_ALL
    # Only a client that knows about scopes sends scope_mode; older clients keep
    # needing an account on these sources.
    explicit_all = rule_data.get("scope_mode") == SCOPE_ALL
    if (
        not all(s in ACCOUNT_OPTIONAL_SOURCES for s in sources)
        and scope_mode != SCOPE_ACCOUNTS
        and not (keeps_existing_all or explicit_all)
    ):
        raise InvalidRuleError(Err.OS0010.value[0] % "account_ids")
    if scope_mode == SCOPE_ACCOUNTS and not account_ids:
        raise InvalidRuleError(Err.OS0010.value[0] % "account_ids")
    return scope_mode, (account_ids if scope_mode == SCOPE_ACCOUNTS else [])


def _resolve_sources(rule_data, existing_rule):
    """Return (source_mode, sources, mirror source) for a save. A client that
    sends neither sources nor source_mode keeps an existing rule's types and
    creates a rule for its source."""
    mode = rule_data.get("source_mode")
    sent = rule_data.get("sources")
    if mode is None and sent is None:
        if existing_rule is not None and getattr(existing_rule, "source_mode", None):
            return existing_rule.source_mode, existing_rule.sources, existing_rule.source
        source = rule_data.get("source")
        return SOURCE_MODE_LIST, [source], source
    if mode == SOURCE_MODE_ALL:
        return SOURCE_MODE_ALL, None, ALL_TYPES_MIRROR_SOURCE
    if mode not in (None, SOURCE_MODE_LIST):
        raise InvalidRuleError(Err.OS0011.value[0] % "source_mode")
    if not isinstance(sent, list) or not sent:
        raise InvalidRuleError(Err.OS0010.value[0] % "sources")
    sources = list(dict.fromkeys(sent))
    unknown = [s for s in sources if s not in RULE_SOURCES]
    if unknown:
        raise InvalidRuleError(Err.OS0011.value[0] % f"sources: {', '.join(map(str, unknown))}")
    if len(sources) > 1 and any(s in SINGLETON_SOURCES for s in sources):
        raise InvalidRuleError(Err.OS0011.value[0] % f"sources: {', '.join(SINGLETON_SOURCES)} cannot be combined")
    return SOURCE_MODE_LIST, sources, sources[0]


def _is_uuid(value):
    try:
        uuid.UUID(str(value))
    except ValueError:
        return False
    return True


def _validate_automation_fields(fields):
    event_types = fields.get("event_types")
    if not isinstance(event_types, list) or not event_types:
        raise InvalidRuleError(Err.OS0010.value[0] % "event_types")
    unknown = [e for e in event_types if e not in AUTOMATION_EVENT_TYPES]
    if unknown:
        raise InvalidRuleError(Err.OS0011.value[0] % f"event_types: {', '.join(map(str, unknown))}")
    if fields.get("delivery_mode") == "batch":
        raise InvalidRuleError(Err.OS0011.value[0] % "delivery_mode: batch is not available for automation rules")


def _validate_optimize_fields(fields):
    event_types = fields.get("event_types")
    if event_types is None:
        return
    if not isinstance(event_types, list):
        raise InvalidRuleError(Err.OS0011.value[0] % "event_types")
    unknown = [e for e in event_types if e not in OPTIMIZE_EVENT_TYPES]
    if unknown:
        raise InvalidRuleError(Err.OS0011.value[0] % f"event_types: {', '.join(map(str, unknown))}")


def validate_fields(fields, sources=None):
    source = fields.get("source", None)
    name = fields.get("name", None)
    if not name or not is_valid_string(name):
        raise InvalidRuleError(Err.OS0011.value[0] % "name")
    if source is None:
        raise InvalidRuleError(Err.OS0010.value[0] % "source")
    sources = sources or [source]
    if len(sources) == 1 and sources[0] == AUTOMATION_SOURCE:
        _validate_automation_fields(fields)
    elif len(sources) == 1 and sources[0] == OPTIMIZE_SOURCE:
        _validate_optimize_fields(fields)
    elif fields.get("event_types"):
        # Events belong to one type; a rule over several types covers all of their events.
        raise InvalidRuleError(Err.OS0011.value[0] % "event_types: only for a rule of one automation or optimize type")
    if fields.get("delivery_mode") == "batch" and len(sources) > 1:
        # The batch digest only sends findings, so other types held for it would never go out.
        raise InvalidRuleError(Err.OS0011.value[0] % "delivery_mode: batch is only for a rule of one type")
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
                if action.get("name") != "notifications_upsert_rule":
                    raise BeeHTTPError(400, Err.OS0004, [])

                rule_data = kwargs["input"]["rule"]
                sess_vars = kwargs["session_variables"]
                # Empty string falls through to the BeeHTTPError below since
                # both ids are required for the upsert.
                tenant_id = sess_vars.get("tenant_id", "")
                user_id = sess_vars.get("user_id", "")

                rule_id = rule_data.get("id")
                rule_name = rule_data.get("name")
                source = rule_data.get("source") or (rule_data.get("sources") or [None])[0]
                if rule_data.get("source_mode") == SOURCE_MODE_ALL:
                    source = ALL_TYPES_MIRROR_SOURCE

                fields = {k: v for k, v in rule_data.items() if k in CLIENT_RULE_FIELDS}
                if "exceptions" in rule_data:
                    fields["exceptions"] = normalize_exceptions(rule_data["exceptions"])
                if "expires_at" in fields and isinstance(fields["expires_at"], str):
                    parsed = datetime.fromisoformat(fields["expires_at"])
                    fields["expires_at"] = (
                        parsed.astimezone(timezone.utc).replace(tzinfo=None) if parsed.tzinfo else parsed
                    )
                existing_rule = await self._get_existing_rule(session, rule_id, source, tenant_id)

                source_mode, sources, source = _resolve_sources({**rule_data, "source": source}, existing_rule)
                fields["source"] = source
                fields["source_mode"] = source_mode
                fields["sources"] = sources
                covered = list(ALL_TYPES_SOURCES) if source_mode == SOURCE_MODE_ALL else sources
                validate_fields(fields, covered)

                existing_account_ids = (
                    await self._get_rule_account_ids(session, existing_rule.id) if existing_rule else []
                )
                scope_mode, account_ids = _resolve_scope(rule_data, covered, existing_rule, existing_account_ids)
                account_names = await self._get_tenant_account_names(session, tenant_id, account_ids)
                fields["scope_mode"] = scope_mode
                exceptions = (
                    fields["exceptions"] if "exceptions" in fields else getattr(existing_rule, "exceptions", None)
                )
                await self._validate_exceptions(session, tenant_id, exceptions, scope_mode, fields)
                fields["account_id"] = account_ids[0] if account_ids else None
                fields["cluster"] = account_names[account_ids[0]] if account_ids else None

                if existing_rule:
                    self._update_existing_rule(existing_rule, fields, user_id)
                    rule_id = existing_rule.id
                else:
                    rule_id = self._create_new_rule(session, fields, tenant_id, user_id)
                await session.flush()
                await self._replace_rule_accounts(session, rule_id, tenant_id, account_ids)

                if rule_data.get("mappings") is not None:
                    await self._save_mappings_data(
                        session, rule_id, bool(existing_rule), user_id, tenant_id, rule_data["mappings"]
                    )

                # Double-invalidation: clear before commit to prevent concurrent reads
                # from re-caching stale data during the commit window
                await self._invalidate_cache(tenant_id)
                await session.commit()
                await self._invalidate_cache(tenant_id)
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
                if isinstance(e, ProgrammingError) and _is_missing_schema(e):
                    LOG.warning("Notification rule save attempted before its migration was applied: %s", e)
                    return {
                        "error": "Notification rules are being upgraded. Try again in a minute.",
                        "status_code": 503,
                    }
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
            "event_types",
        }

        for key, value in fields.items():
            setattr(existing_rule, key, value)

        for f in nullable_fields - fields.keys():
            if hasattr(existing_rule, f):
                setattr(existing_rule, f, None)

        existing_rule.updated_by = user_id
        existing_rule.updated_at = utc_now()

    @staticmethod
    def _create_new_rule(session, fields, tenant_id, user_id):
        rule_id = uuid.uuid4()
        rule = NotificationRules(
            **{"is_active": True, **fields},
            id=rule_id,
            tenant_id=tenant_id,
            created_by=user_id,
            updated_by=user_id,
            created_at=utc_now(),
            updated_at=utc_now(),
        )
        session.add(rule)
        return rule_id

    @staticmethod
    async def _get_rule_account_ids(session, rule_id):
        result = await session.execute(select(NotificationRuleAccounts.account_id).filter_by(rule_id=rule_id))
        return [str(account_id) for account_id in result.scalars().all()]

    async def _validate_exceptions(self, session, tenant_id, exceptions, scope_mode, fields):
        if not exceptions:
            return
        # A save sets delivery_mode to what it sends (an update clears one it omits).
        if fields.get("delivery_mode") == "batch":
            raise InvalidRuleError(Err.OS0011.value[0] % "exceptions: not available for batch rules")
        account_ids = [a for group in exceptions for a in group.get("account_ids", [])]
        if account_ids:
            if scope_mode != SCOPE_ALL:
                raise InvalidRuleError(
                    Err.OS0011.value[0] % "exceptions.account_ids: only a rule for all accounts can leave accounts out"
                )
            await self._get_tenant_account_names(session, tenant_id, list(dict.fromkeys(account_ids)))

    async def invalidate_cached_rules(self, tenant_id):
        await self._invalidate_cache(tenant_id)

    async def get_default_policies(self, tenant_id):
        """{source: {"action", "mappings"}} for the tenant's saved defaults. Read in
        its own session so a missing table (a release running ahead of its
        migration) cannot abort the caller's transaction; that case reads as no
        saved defaults and is not cached."""
        if self.cache and self.cache.redis_client:
            cached = await self.cache.aio.get_default_policies(tenant_id)
            if cached is not None:
                return cached
        async with BaseDB.async_session(self.engine)() as session:
            try:
                result = await session.execute(select(NotificationDefaultPolicy).filter_by(tenant_id=tenant_id))
                policies = {
                    row.source: {"action": row.action, "mappings": row.mappings or []} for row in result.scalars()
                }
            except ProgrammingError as e:
                if not _is_missing_schema(e):
                    LOG.exception("Error loading notification default policies for tenant %s: %s", tenant_id, e)
                return {}
            except Exception as e:
                LOG.exception("Error loading notification default policies for tenant %s: %s", tenant_id, e)
                return {}
        if self.cache and self.cache.redis_client:
            await self.cache.aio.cache_default_policies(tenant_id, policies)
        return policies

    async def get_default_policy(self, tenant_id, source):
        """The type's own default, else the all-types default. The singletons
        (daily email, AI cost report, budget alerts) never follow the all-types default;
        an opt-in type with no row of its own drops."""
        policies = await self.get_default_policies(tenant_id)
        if source in policies:
            return policies[source]
        if source in ALL_TYPES_SOURCES:
            return policies.get(DEFAULT_ALL_SOURCES)
        if source in OPT_IN_SOURCES:
            return {"action": DEFAULT_DROP, "mappings": []}
        return None

    async def list_default_policies(self, **kwargs):
        tenant_id = (kwargs.get("session_variables") or {}).get("tenant_id")
        if not tenant_id:
            return {"error": "tenant is required", "status_code": 400}
        policies = await self.get_default_policies(tenant_id)
        return {
            "policies": [
                {"source": source, "action": policy["action"], "mappings": policy["mappings"]}
                for source, policy in sorted(policies.items())
            ]
        }

    async def upsert_default_policy(self, **kwargs):
        action = kwargs.get("action") or {}
        if action.get("name") != "notifications_upsert_default_policy":
            return {"error": "Unsupported action", "status_code": 400}
        request = (kwargs.get("input") or {}).get("request") or {}
        sess_vars = kwargs.get("session_variables") or {}
        tenant_id = sess_vars.get("tenant_id")
        user_id = sess_vars.get("user_id") or None
        source = request.get("source")
        policy_action = request.get("action")
        mappings = request.get("mappings") or []
        if not tenant_id:
            return {"error": "tenant is required", "status_code": 400}
        if source not in DEFAULT_POLICY_SOURCES:
            return {"error": Err.OS0011.value[0] % "source", "status_code": 400}
        if policy_action == DEFAULT_INHERIT and source == DEFAULT_ALL_SOURCES:
            return {"error": Err.OS0011.value[0] % "action", "status_code": 400}
        if policy_action not in DEFAULT_ACTIONS + (DEFAULT_INHERIT,):
            return {"error": Err.OS0011.value[0] % "action", "status_code": 400}
        if policy_action == DEFAULT_CHANNELS:
            if not isinstance(mappings, list) or not mappings:
                return {"error": Err.OS0010.value[0] % "mappings", "status_code": 400}
            if any(
                not isinstance(m, dict) or not isinstance(m.get("platform"), str) or not m.get("channels")
                for m in mappings
            ):
                return {"error": Err.OS0011.value[0] % "mappings", "status_code": 400}
            mappings = [{"platform": m["platform"], "channels": m["channels"]} for m in mappings]
        else:
            mappings = []

        async with BaseDB.async_session(self.engine)() as session:
            try:
                if policy_action == DEFAULT_INHERIT:
                    await session.execute(
                        delete(NotificationDefaultPolicy).where(
                            NotificationDefaultPolicy.tenant_id == tenant_id, NotificationDefaultPolicy.source == source
                        )
                    )
                else:
                    values = {
                        "action": policy_action,
                        "mappings": mappings,
                        "updated_by": user_id,
                        "updated_at": utc_now(),
                    }
                    # One statement, so two first saves of the same default cannot collide.
                    await session.execute(
                        pg_insert(NotificationDefaultPolicy)
                        .values(tenant_id=tenant_id, source=source, **values)
                        .on_conflict_do_update(index_elements=["tenant_id", "source"], set_=values)
                    )
                await self._invalidate_cache(tenant_id)
                await session.commit()
                await self._invalidate_cache(tenant_id)
            except Exception as e:
                if isinstance(e, ProgrammingError) and _is_missing_schema(e):
                    return {
                        "error": "Notification rules are being upgraded. Try again in a minute.",
                        "status_code": 503,
                    }
                LOG.exception("Unexpected error saving notification default policy: %s", e)
                return {"error": "Unable to save the notification default", "status_code": 500}

        await self._create_audit_entry(
            source, tenant_id, user_id, {"source": source, "action": policy_action}, is_update=True
        )
        return {"source": source, "action": policy_action, "mappings": mappings}

    @staticmethod
    async def _get_tenant_account_names(session, tenant_id, account_ids):
        if not account_ids:
            return {}
        malformed = [a for a in account_ids if not _is_uuid(a)]
        if malformed:
            raise InvalidRuleError(Err.OS0011.value[0] % f"account_ids: {', '.join(malformed)}")
        result = await session.execute(
            select(CloudAccount.id, CloudAccount.account_name).filter(
                CloudAccount.id.in_(account_ids), CloudAccount.tenant == tenant_id
            )
        )
        names = {str(account_id): name for account_id, name in result.all()}
        unknown = [a for a in account_ids if a not in names]
        if unknown:
            raise InvalidRuleError(Err.OS0011.value[0] % f"account_ids: {', '.join(unknown)}")
        return names

    @staticmethod
    async def _replace_rule_accounts(session, rule_id, tenant_id, account_ids):
        existing = await session.execute(select(NotificationRuleAccounts).filter_by(rule_id=rule_id))
        for row in existing.scalars().all():
            await session.delete(row)
        await session.flush()
        for account_id in account_ids:
            session.add(NotificationRuleAccounts(rule_id=rule_id, account_id=account_id, tenant_id=tenant_id))

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

    async def _invalidate_cache(self, tenant_id):
        if self.cache and self.cache.redis_client:
            await self.cache.aio.delete_cached_notification_rules(tenant_id)
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
            cached = await self.cache.aio.get_notification_rules(tenant_id)
            if cached:
                return cached

        async with BaseDB.async_session(self.engine)() as session:
            try:
                result = await session.execute(select(NotificationRules).filter_by(tenant_id=tenant_id, is_active=True))
                rules = result.scalars().all()
            except ProgrammingError as e:
                if not _is_missing_schema(e):
                    LOG.exception("Error getting notification rules for tenant %s: %s", tenant_id, e)
                    return []
                # The failed statement aborted the transaction; nothing else runs until rollback.
                await session.rollback()
                return await self._get_rules_before_migration(session, tenant_id)
            except Exception as e:
                LOG.exception("Error getting notification rules for tenant %s: %s", tenant_id, e)
                return []
            for rule in rules:
                rule.missing_columns = frozenset()
            scope_loaded = await self._attach_account_ids(session, tenant_id, rules)
            if rules and scope_loaded and self.cache and self.cache.redis_client:
                await self.cache.aio.cache_notification_rules(tenant_id, rules)
            return rules

    @staticmethod
    async def _attach_account_ids(session, tenant_id, rules):
        """Load each rule's accounts onto rule.account_ids. On failure every rule
        gets None, which the matcher treats as no accounts, and the load must not
        be cached."""
        try:
            result = await session.execute(
                select(NotificationRuleAccounts.rule_id, NotificationRuleAccounts.account_id).filter_by(
                    tenant_id=tenant_id
                )
            )
        except Exception as e:
            LOG.exception("Error loading notification rule accounts for tenant %s: %s", tenant_id, e)
            for rule in rules:
                rule.account_ids = None
            return False
        by_rule = {}
        for rule_id, account_id in result.all():
            by_rule.setdefault(str(rule_id), []).append(str(account_id))
        for rule in rules:
            rule.account_ids = by_rule.get(str(rule.id), [])
            if rule.scope_mode == SCOPE_ACCOUNTS and rule.account_id and str(rule.account_id) not in rule.account_ids:
                LOG.warning(
                    "Notification rule %s has account_id %s outside its accounts %s; an older client edited it",
                    rule.id,
                    rule.account_id,
                    rule.account_ids,
                )
        return True

    @staticmethod
    async def _get_rules_before_migration(session, tenant_id):
        """Rules for the window between a release's pods starting and its migration
        running. Not cached: the next load after the migration reads the full shape."""
        global _warned_missing_schema
        try:
            rules, missing = await _load_rules_with_existing_columns(session, tenant_id)
        except Exception as e:
            LOG.exception("Error getting notification rules for tenant %s with the existing columns: %s", tenant_id, e)
            return []
        if not _warned_missing_schema:
            LOG.warning(
                "notification_rules lacks mapped columns %s; loading the existing columns until the migration runs",
                missing,
            )
            _warned_missing_schema = True
        if rules and "scope_mode" not in missing:
            await NotificationRulesService._attach_account_ids(session, tenant_id, rules)
        return rules
