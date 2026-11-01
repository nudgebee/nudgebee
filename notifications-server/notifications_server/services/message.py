import asyncio
import inspect
import json
import logging
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Dict, Any, List, Optional

from notifications_server.utils.datetime_utils import utc_now
from asyncio import Lock
from collections import defaultdict

from sqlalchemy import select

from notifications_server.clients.google_chat_client import GoogleChatClient
from notifications_server.clients.google_chat_app_client import GoogleChatAppClient
from notifications_server.clients.ms_teams_client import MsTeamsClient
from notifications_server.clients.slack_client import SlackClient
from notifications_server.configs import settings

from notifications_server.models.enums import PlatformTypes
from notifications_server.schemas.message import SendMessageRequest
from notifications_server.services.cache import Cache
from notifications_server.services.response_helpers import success_response, failed_response, system_response
from notifications_server.services.rules import (
    DEFAULT_CHANNELS,
    DEFAULT_DELIVER,
    OPTIMIZE_EVENT_BY_MESSAGE_TYPE,
    OPTIMIZE_SOURCE,
    NotificationRulesService,
    rule_sort_key,
    rule_sources,
)
from notifications_server.services import weekly_digest_report
from notifications_server.services.delivery_split import (
    account_sections,
    has_recommendations,
    latest_per_key,
    merged_parameters,
)
from notifications_server.message_templates.slack.weekly_digest_events import digest_link, digest_verdict
from notifications_server.services.slack.slack import retriable_slack_api_error, slack_channel_not_found_error
from notifications_server.message_templates import template_mapping
from notifications_server.message_templates.google_chat.finding import get_markdown_message_template
from notifications_server.message_templates.ms_teams.finding import get_ms_teams_finding_message_template
from notifications_server.services.ms_teams import card_actions
from notifications_server.services.ms_teams.bot_sender import (
    APP_NOT_IN_TEAM,
    TeamsBotError,
    card_activity,
    get_sender,
    load_team_async,
    thread_conversation_id,
)
from notifications_server.message_templates.slack.finding import (
    get_slack_finding_message,
    get_slack_resolved_finding_reply,
)
from notifications_server.message_templates.slack.webhook_sanitize import strip_interactive_slack_elements
from notifications_server.models.db_base import BaseDB
from notifications_server.models.models import (
    SentNotifications,
    NotificationRuleMappings,
    MessagingPlatform,
    Integration,
    IntegrationConfigValue,
)
from notifications_server.services.messaging_installations import (
    MESSAGING_PLATFORMS,
    ORIGIN_INTEGRATION,
    ORIGIN_LEGACY,
    WEBHOOK_PLATFORM_TYPES,
    load_google_chat_webhook_installations_async,
    load_integration_installations_async,
    load_ms_teams_webhook_installations_async,
    load_slack_webhook_installations_async,
    persist_messaging_token_async,
)

LOG = logging.getLogger(__name__)
cache = Cache()


# ----------------------------- Utility functions -----------------------------


# Set on the session when a Teams card with buttons was added to it, so the
# caller knows to commit.
STORED_TEAMS_CARDS = "stored_teams_cards"


def _approval_account_id(params):
    """The account a workflow approval belongs to, when its params name one."""
    metadata = getattr(params, "workflow_metadata", None)
    return getattr(metadata, "account_id", None) if metadata else None


def get_fingerprint(type_, kwargs):
    parameters = kwargs.get("parameters", {})
    if type_ == "finding":
        return (parameters.get("finding") or {}).get("fingerprint")
    return parameters.get("fingerprint")


def is_batch_delivery(matched_rules: List[Dict[str, Any]]):
    return any(rule.get("delivery_mode") == "batch" for rule in matched_rules)


# Findings carry `priority` (DEBUG/INFO/LOW/MEDIUM/HIGH — see api-server
# services/event/types). Low-signal findings are digest-only by default:
# real-time delivery requires a matched rule that lists the priority
# explicitly in its severity filter.
REALTIME_GATED_PRIORITIES = {"debug", "info", "low"}


def _rule_severities(rule_severity) -> set:
    """Rule severity is a single value from the UI today; tolerate array-style or
    comma-separated strings so a future multi-select doesn't silently break."""
    if not rule_severity:
        return set()
    values = rule_severity
    if isinstance(values, str):
        stripped = values.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            values = [v.strip().strip("'").strip('"') for v in stripped[1:-1].split(",") if v.strip()]
        else:
            values = stripped.split(",")
    elif not isinstance(values, (list, tuple, set)):
        values = [values]
    return {str(v).strip().lower() for v in values if str(v).strip()}


def _severity_matches(rule_severity, finding_priority):
    severities = _rule_severities(rule_severity)
    if not severities:
        return True
    if not finding_priority:
        return False
    return str(finding_priority).strip().lower() in severities


def is_realtime_severity_gated(finding_priority, matched_rules: List[Dict[str, Any]]):
    priority = str(finding_priority or "").strip().lower()
    if priority not in REALTIME_GATED_PRIORITIES:
        return False
    return not any(priority in _rule_severities(rule.get("severity")) for rule in matched_rules)


def normalize_channel(channel):
    if channel is None:
        return None
    if isinstance(channel, str):
        try:
            channel = json.loads(channel)
        except Exception:
            return None
    if isinstance(channel, list) and channel:
        return channel[0]
    return channel


@dataclass
class RuleOutcome:
    """What the matched rules decide for one message. `response` is set when
    nothing is delivered (`stopped_by` names the rule that stopped it, if one
    did); otherwise `targets` are the mapping entries that receive the message,
    and `use_defaults` says whether platforms no target names still get the
    installation's default channel."""

    response: Optional[List[Dict[str, Any]]]
    targets: List[Dict[str, Any]]
    use_defaults: bool
    stopped_by: Any = None


def resolve_outcome(matched_rules, source=None, event_type=None, honour_batch=True, default_policy=None) -> RuleOutcome:
    """Every matching rule applies. Any matching suppress rule or active snooze
    stops the message on every channel; otherwise any matching batch rule queues
    it (email tasks, which have no digest, pass honour_batch=False); otherwise it
    goes to the channels of every matching rule. Approval requests are never
    stopped.

    When no deliver rule matched, the source's default policy decides: each
    platform's default channel (also when there is no policy), the policy's own
    channels, or nothing. When deliver rules matched, platforms they do not name
    get their default channel only under the first of those."""
    non_suppressible = event_type in NON_SUPPRESSIBLE_EVENT_TYPES
    now = utc_now()

    def snoozed(entry):
        return bool(entry["expires_at"]) and entry["expires_at"] > now

    # A suppressed rule with an expiry stops messages until then and does nothing after.
    matched_rules = [
        entry for entry in matched_rules if not (entry["suppressed"] and entry["expires_at"] and not snoozed(entry))
    ]

    if not non_suppressible:
        suppressing = next((entry for entry in matched_rules if entry["suppressed"]), None)
        if suppressing is not None:
            rule_name = suppressing.get("name")
            reason = (
                f'Suppressed by notification rule "{rule_name}"'
                if rule_name
                else "Notification suppressed by notification rule configuration"
            )
            extra = (
                {"rule_management_url": f"{settings.base_url}/user-management#notification-rules"}
                if settings.base_url
                else {}
            )
            response = system_response("suppressed", reason, **extra)
            return RuleOutcome([response], [], False, suppressing["rule_id"])
        snoozing = next((entry for entry in matched_rules if snoozed(entry)), None)
        if snoozing is not None:
            response = system_response("snoozed", f"Notification snoozed until {snoozing['expires_at']}")
            return RuleOutcome([response], [], False, snoozing["rule_id"])

    # The batch digest is built from the events table, so a queued automation
    # message would never be delivered.
    if honour_batch and not non_suppressible and source != AUTOMATION_SOURCE and is_batch_delivery(matched_rules):
        return RuleOutcome([system_response("queued", "Notification queued for batch delivery")], [], False)

    delivering = [
        entry
        for entry in matched_rules
        if not entry["suppressed"]
        and not snoozed(entry)
        and not (honour_batch and entry.get("delivery_mode") == "batch")
    ]
    targets = [entry for entry in delivering if entry.get("platform")]
    policy_action = (default_policy or {}).get("action", DEFAULT_DELIVER)
    if delivering:
        return RuleOutcome(None, targets, policy_action == DEFAULT_DELIVER)
    if policy_action == DEFAULT_DELIVER or (non_suppressible and policy_action != DEFAULT_CHANNELS):
        return RuleOutcome(None, [], True)
    if policy_action == DEFAULT_CHANNELS:
        default_targets = [
            {"rule_id": None, "platform": mapping["platform"], "channels": mapping["channels"]}
            for mapping in default_policy.get("mappings") or []
        ]
        return RuleOutcome(None, default_targets, False)
    return RuleOutcome(
        [system_response("suppressed", "No notification rule matched and the default for this source is to drop")],
        [],
        False,
    )


def _distinct_rule_channels(matched_rules, platform):
    channels = []
    seen = set()
    for rule in matched_rules:
        if rule.get("platform") != platform or not rule.get("channels"):
            continue
        key = json.dumps(rule["channels"], sort_keys=True, default=str)
        if key not in seen:
            seen.add(key)
            channels.append(rule["channels"])
    return channels


async def send_in_rounds(installed_platforms, send):
    """Deliver to every target channel. Senders read one channel per installation
    (`to_channel`), so the first channel of each platform goes in one call and
    each further channel in a later call through the same installation object."""
    responses = list(await send(installed_platforms) or [])
    round_index = 0
    while True:
        pending = [
            ip for ip in installed_platforms if round_index < len(getattr(ip, "additional_channels", None) or [])
        ]
        if not pending:
            return responses
        for ip in pending:
            ip.to_channel = ip.additional_channels[round_index]
        responses.extend(await send(pending) or [])
        round_index += 1


# ----------------------------- Rule Matcher -----------------------------


def _ids_match(rule_id, payload_id):
    """Compare IDs accounting for UUID vs string type differences.
    A None rule_id is treated as a wildcard (matches any payload)."""
    if rule_id is None:
        return True
    if payload_id is None:
        return False
    return str(rule_id) == str(payload_id)


# Notification rules are keyed by category — the tabs in the UI: "troubleshoot",
# "optimize", "slo", "cloud". A finding's `source`, however, is the raw
# *ingestion* source (e.g. "prometheus", "anomaly", "ebpf", "traces", "grafana",
# "newrelic", or "automation" for workflow-stored events). Those never equal
# "troubleshoot", so matching them verbatim against rule.source sends every
# troubleshoot finding to the default channel (#28130). Map any source that is
# not itself a rule category to "troubleshoot"; "cloud"/"optimize"/"slo" findings
# keep their category. "automation" is deliberately absent: automation *rules*
# select non-finding automation messages by event type, while automation
# *findings* stay troubleshoot findings.
RULE_SOURCE_CATEGORIES = {"troubleshoot", "optimize", "slo", "cloud"}

# Non-finding messages from runbook-server. They carry a top-level `event_type`
# and `parameters.account_id`; rules select them by event type.
AUTOMATION_SOURCE = "automation"

# An approval prompt suspends a workflow until a person answers, so no rule may
# silence it — a dropped prompt is a run that never finishes.
NON_SUPPRESSIBLE_EVENT_TYPES = {"automation.approval.requested"}


def _scope_matches(rule, account_id):
    """Whether a message from account_id is in the rule's scope. A rule loaded
    from a database that predates scope_mode still uses account_id, which is the
    truth there; otherwise account_id is never read. Unknown scope matches nothing."""
    if "scope_mode" in getattr(rule, "missing_columns", frozenset()):
        return _ids_match(rule.account_id, account_id)
    scope_mode = getattr(rule, "scope_mode", None)
    if scope_mode == "all":
        return True
    if scope_mode == "accounts":
        account_ids = getattr(rule, "account_ids", None)
        return bool(account_ids) and account_id is not None and str(account_id) in account_ids
    return False


_EXCEPTION_FIELDS = {
    "account_ids": "account_id",
    "namespaces": "namespace",
    "workloads": "workload",
    "aggregation_keys": "aggregation_key",
}


def _excluded(rule, account_id, namespace, workload, aggregation_key):
    """Whether the rule's exceptions leave this finding out: some group lists the
    finding's value under every key the group has. Saves store exceptions as
    lists of strings; anything else is ignored so one bad row cannot break
    matching for the whole tenant."""
    exceptions = getattr(rule, "exceptions", None)
    if not isinstance(exceptions, list):
        return False
    values = {
        "account_id": account_id,
        "namespace": namespace,
        "workload": workload,
        "aggregation_key": aggregation_key,
    }
    for group in exceptions:
        if not isinstance(group, dict) or not group:
            continue
        for key, listed in group.items():
            value = values.get(_EXCEPTION_FIELDS.get(key))
            if value is None or not isinstance(listed, list) or str(value) not in listed:
                break
        else:
            return True
    return False


def _finding_rule_source(source):
    return source if source in RULE_SOURCE_CATEGORIES else "troubleshoot"


def _event_type_matches(rule, source, event_type):
    """A rule for automation alone must name the event types it covers; one with
    none matches nothing (fail closed). A rule for optimize alone may name event
    types; none covers them all. A rule over several types covers every event of
    each. Other sources ignore event types."""
    if source not in (AUTOMATION_SOURCE, OPTIMIZE_SOURCE) or rule_sources(rule) != (source,):
        return True
    event_types = getattr(rule, "event_types", None)
    if source == OPTIMIZE_SOURCE and not event_types:
        return True
    if not event_types or not event_type:
        return False
    return event_type in event_types


class NotificationRuleMatcher:
    def __init__(self, rules_service: NotificationRulesService):
        self.rules_service = rules_service

    @staticmethod
    def get_filters_from_notification_payload(type_, kwargs):
        source = kwargs.get("source", "")
        params = kwargs.get("parameters", {})

        if type_ == "finding":
            finding_params = params.get("finding") or {}
            finding_source = _finding_rule_source(source)
            return (
                finding_source,
                finding_params.get("cloud_account_id") or finding_params.get("account_id"),
                finding_params.get("subject_namespace") or finding_params.get("namespace"),
                finding_params.get("subject_owner") or finding_params.get("workload"),
                finding_params.get("aggregation_key"),
            )

        if source in {AUTOMATION_SOURCE, "slo", "optimize"}:
            return (
                source,
                params.get("account_id"),
                params.get("namespace"),
                params.get("workload"),
                None,
            )
        if source == "cloud":
            return (
                source,
                params.get("account_id"),
                None,
                None,
                None,
            )

        return source, None, None, None, None

    async def match_notification_rules(self, session, tenant_id, type_, kwargs):
        source, account_id, namespace, workload, aggregation_key = self.get_filters_from_notification_payload(
            type_, kwargs
        )

        finding_priority = None
        if type_ == "finding":
            finding_priority = ((kwargs.get("parameters") or {}).get("finding") or {}).get("priority")
        event_type = kwargs.get("event_type")
        if source == OPTIMIZE_SOURCE:
            event_type = OPTIMIZE_EVENT_BY_MESSAGE_TYPE.get(type_)

        all_rules = await self.rules_service.get_notification_rules(tenant_id)
        if not all_rules:
            LOG.debug("No rules found for tenant %s (source=%s), using defaults", tenant_id, source)
            return [], source

        truly_matched_rules = sorted(
            (
                rule
                for rule in all_rules
                if (
                    source in rule_sources(rule)
                    and _scope_matches(rule, account_id)
                    and (rule.namespace == namespace or rule.namespace is None)
                    and (rule.workload == workload or rule.workload is None)
                    and (rule.aggregation_key == aggregation_key or rule.aggregation_key is None)
                    and (type_ != "finding" or _severity_matches(rule.severity, finding_priority))
                    and _event_type_matches(rule, source, event_type)
                    and rule.is_active
                    and not _excluded(rule, account_id, namespace, workload, aggregation_key)
                )
            ),
            key=rule_sort_key,
        )

        if not truly_matched_rules:
            LOG.debug(
                "No rules matched for tenant %s (source=%s, account=%s), using defaults",
                tenant_id,
                source,
                account_id,
            )
            return [], source

        matched_rule_ids = [rule.id for rule in truly_matched_rules]
        result = await session.execute(
            select(NotificationRuleMappings).filter(NotificationRuleMappings.rule_id.in_(matched_rule_ids))
        )
        all_mappings = result.scalars().all()

        # Normalize keys to strings: rule.id may be a UUID (from DB) or string
        # (from cache deserialization), while mapping.rule_id is always a UUID
        # from the DB. Without normalization, the dict lookup silently fails and
        # notifications fall back to the default channel.
        mappings_by_rule_id = {}
        for mapping in all_mappings:
            mappings_by_rule_id.setdefault(str(mapping.rule_id), []).append(mapping)

        matched_rules = []
        for rule in truly_matched_rules:
            mappings = mappings_by_rule_id.get(str(rule.id), [])
            if mappings:
                matched_rules.extend(
                    {
                        "rule_id": rule.id,
                        "name": rule.name,
                        "suppressed": rule.is_suppressed,
                        "platform": mapping.platform,
                        "channels": mapping.channels,
                        "expires_at": rule.expires_at,
                        "delivery_mode": rule.delivery_mode,
                        "severity": rule.severity,
                    }
                    for mapping in mappings
                )
            else:
                LOG.debug(
                    "Rule %s matched but has no platform mappings — notification will use default channels",
                    rule.id,
                )
                matched_rules.append(
                    {
                        "rule_id": rule.id,
                        "name": rule.name,
                        "suppressed": rule.is_suppressed,
                        "platform": None,
                        "channels": None,
                        "expires_at": rule.expires_at,
                        "delivery_mode": rule.delivery_mode,
                        "severity": rule.severity,
                    }
                )

        if len(matched_rules) > 100:
            LOG.warning(
                "Matched %d rules for tenant %s (source=%s), truncating to 100",
                len(matched_rules),
                tenant_id,
                source,
            )
            matched_rules = matched_rules[:100]

        return matched_rules, source


# ----------------------------- Platform Senders -----------------------------


class BaseSender:
    def __init__(self, cache: Cache, slack_app=None, teams_app=None, engine=None):
        self.cache = cache
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.engine = engine

    def extract_channels(self, installation: MessagingPlatform) -> List[Dict[str, Any]]:
        channels = []
        if isinstance(installation.to_channel, str):
            try:
                channels.append(json.loads(installation.to_channel))
            except Exception:
                pass
        elif isinstance(installation.to_channel, list):
            channels.extend(installation.to_channel)
        else:
            channels.append(installation.to_channel)
        return [channel for channel in channels if channel]

    @staticmethod
    async def _refresh_token_if_expired(session, installation, platform_value, refresh_fn):
        """Common token refresh flow shared by Teams and Google Chat senders.

        refresh_fn(session, ip) is a sync or async callable that should:
          - Call the platform-specific token refresh API
          - On success: update ip.token, ip.token_expires_at (and ip.refresh_token if applicable)
          - Return the new token on success, None on failure
          - Handle platform-specific error logging/side-effects internally
        """
        if not (installation.token_expires_at and installation.token_expires_at < utc_now()):
            return installation.token

        ips = await MessageService.get_installed_platforms(
            session, tenant_id=installation.tenant_id, platform=platform_value
        )
        if not ips:
            LOG.warning("Unable to find %s installation for tenant %s", platform_value, installation.tenant_id)
            return None

        ip = ips[0]
        result = refresh_fn(session, ip)
        token = await result if inspect.isawaitable(result) else result
        if token is not None:
            await MessageService.commit_session_and_clear_cache_static(ip, session)
        return token


class SlackSender(BaseSender):
    def __init__(self, slack_app, cache: Cache):
        super().__init__(cache=cache, slack_app=slack_app)

    @staticmethod
    def get_retry_after(exc):
        try:
            retry_after = int(exc.response.headers.get("Retry-After", 15))
        except Exception:
            retry_after = 15
        LOG.info(f"Slack Rate limited. Retrying after {retry_after} seconds...")
        return retry_after

    def post_to_slack(self, tenant, installation, thread_id, **message):
        max_tries = 0
        while True:
            try:
                channels = self.extract_channels(installation)
                for channel in channels:
                    channel_id = channel.get("id")
                    if thread_id:
                        return self.slack_app.client.reply_in_thread(
                            token=installation.token, channel_id=channel_id, thread_ts=thread_id, **message
                        )
                    return self.slack_app.client.chat_post(channel_id=channel_id, token=installation.token, **message)

            except Exception as exc:
                if retriable_slack_api_error(exc) and max_tries < settings.slack.max_rate_limit_retries:
                    retry_after = self.get_retry_after(exc)
                    # time.sleep is intentional: this method runs via asyncio.to_thread,
                    # so it blocks the threadpool worker, not the event loop.
                    time.sleep(retry_after)
                    max_tries += 1
                elif slack_channel_not_found_error(exc):
                    return exc.response
                else:
                    LOG.exception(f"Failed to send message for tenant {tenant} due to: {exc}")
                    return None

    @staticmethod
    async def check_if_sent_already(session, fingerprint, team_id, channels):
        # The same finding may have been delivered to several channels (rule
        # routing vs defaults), each with its own thread — so scan the
        # fingerprint's rows for the target channel instead of comparing only
        # the latest row.
        normalized_channel = normalize_channel(channels)
        target_channel_id = normalized_channel.get("id") if isinstance(normalized_channel, dict) else None
        if not target_channel_id:
            return None

        result = await session.execute(
            select(SentNotifications)
            .filter_by(slack_team_id=team_id, fingerprint=fingerprint)
            .order_by(SentNotifications.created_at.desc())
        )
        for notification in result.scalars():
            if not notification.slack_metadata:
                continue
            try:
                slack_metadata = json.loads(notification.slack_metadata)
            except json.JSONDecodeError:
                LOG.warning(f"Failed to parse slack_metadata for fingerprint {fingerprint}")
                continue
            if slack_metadata.get("channel") == target_channel_id:
                return notification.slack_thread_id

        return None

    def get_new_sent_notification(self, team_id, tenant_id, slack_response, fingerprint, account_id=None):
        data = slack_response.data
        notification = SentNotifications(
            id=uuid.uuid4(),
            created_at=utc_now(),
            tenant_id=tenant_id,
            fingerprint=fingerprint,
            account_id=account_id,
            slack_thread_id=data.get("ts"),
            slack_team_id=team_id,
            slack_metadata=json.dumps(
                {
                    "channel": data.get("channel"),
                    "bot_id": data.get("message").get("bot_id"),
                }
            ),
        )
        return notification


class GoogleChatSender(BaseSender):
    def __init__(self, cache: Cache):
        super().__init__(cache=cache)

    async def acquire_google_chat_access_token(self, session, g_chat_installation):
        def _refresh(sess, ip):
            response = GoogleChatClient.refresh_access_token(ip.refresh_token)
            if not response:
                LOG.warning("Unable to refresh google chat token for installation id %s", g_chat_installation.id)
                return None
            if "error" in response and response["error"]:
                LOG.exception("Unable to get access token for google chat due to %s", response.get("error_description"))
                return None
            ip.token = response.get("access_token")
            expires_in = response.get("expires_in")
            ip.token_expires_at = utc_now() + timedelta(seconds=expires_in - 60) if expires_in else None
            return ip.token

        return await self._refresh_token_if_expired(
            session, g_chat_installation, PlatformTypes.GOOGLE_CHAT.value, _refresh
        )


class DiscordSender(BaseSender):
    def __init__(self, cache: Cache):
        super().__init__(cache=cache)

    async def acquire_discord_access_token(self, session, discord_installation):
        # Bot tokens are static (no refresh flow); stored encrypted, decrypt at use.
        # Isolate a decrypt failure to Discord so other platforms still deliver.
        from notifications_server.services.discord.token_store import resolve_token

        try:
            return resolve_token(discord_installation)
        except Exception as e:
            LOG.error("Failed to decrypt Discord bot token for installation %s: %s", discord_installation.id, e)
            return None

    @staticmethod
    async def check_if_sent_already(session, fingerprint, tenant_id, channel_id):
        """Message id of the original Discord post for this fingerprint+channel, or
        None — repeat findings reply to it instead of posting fresh messages."""
        if not fingerprint:
            return None
        result = await session.execute(
            select(SentNotifications)
            .filter_by(tenant_id=tenant_id, fingerprint=fingerprint, discord_channel_id=channel_id)
            .order_by(SentNotifications.created_at.desc())
            .limit(1)
        )
        notification = result.scalars().first()
        return notification.discord_message_id if notification else None

    @staticmethod
    def get_new_sent_notification(tenant_id, channel_id, message_id, fingerprint, account_id=None):
        return SentNotifications(
            id=uuid.uuid4(),
            created_at=utc_now(),
            tenant_id=tenant_id,
            fingerprint=fingerprint,
            account_id=account_id,
            discord_channel_id=channel_id,
            discord_message_id=message_id,
        )


# ----------------------------- Grouped Message Handler -----------------------------


def grouped_unit(index, group_type, item):
    """(key, message type, payload) to match one queued alert of a group. An
    anomaly is queued as its finding, so it is matched as the finding it is."""
    if group_type == "anomaly":
        return index, "finding", {"source": "anomaly", "parameters": {"finding": item.get("parameters") or {}}}
    return index, group_type, item


class GroupedMessageHandler:
    def __init__(self, cache: Cache, message_service: "MessageService"):
        self.cache = cache
        self.message_service = message_service
        # reuse MessageService's _group_tasks and locks to keep behavior
        self._group_tasks = message_service._group_tasks
        self._flush_locks = message_service._flush_locks

    async def enqueue(self, payload: Dict[str, Any]):
        try:
            tenant_id = payload["tenant_id"]
            group_type = payload["type"]
            key = f"grouped:{tenant_id}:{group_type}"
            redis = self.cache.redis_client
            if not redis:
                return {"status": "ignored"}

            await asyncio.to_thread(redis.lpush, key, json.dumps(payload, default=str))
            await asyncio.to_thread(redis.expire, key, settings.notifications.grouped_redis_ttl_seconds)

            if key not in self._group_tasks:
                delay_seconds = settings.notifications.grouped_flush_delay_seconds
                LOG.info("Scheduling flush for %s in %d seconds", key, delay_seconds)
                task = asyncio.create_task(self._delayed_flush(key, delay_seconds))
                self._group_tasks[key] = {"task": task, "scheduled_at": utc_now()}

            length = await asyncio.to_thread(redis.llen, key)
            LOG.info("Enqueued grouped msg to %s (count=%d)", key, length)
            return {"status": "queued"}
        except Exception as e:
            LOG.error(f"failed to cache messages for grouping due to {e}")

    async def _delayed_flush(self, key, delay_seconds):
        try:
            LOG.debug("Starting delayed flush timer for %s (%d seconds)", key, delay_seconds)
            await asyncio.sleep(delay_seconds)
            redis = self.cache.redis_client
            if not redis or (await asyncio.to_thread(redis.llen, key)) == 0:
                LOG.info("No messages left for key %s before scheduled flush, skipping", key)
                return

            await self._flush_group(key)

        except asyncio.CancelledError:
            LOG.info("Flush task for %s was cancelled", key)
            raise
        except Exception as e:
            LOG.exception("Unexpected error in delayed flush for %s: %s", key, e)
        finally:
            self._group_tasks.pop(key, None)
            LOG.debug("Cleanup complete for delayed flush task of %s", key)

    async def _flush_group(self, key):
        async with self._flush_locks[key]:
            redis = self.cache.redis_client
            if not redis:
                LOG.error("Redis unavailable during flush for %s", key)
                return {"status": "failed", "error": "redis_unavailable"}

            try:
                raw_list = await asyncio.to_thread(redis.lrange, key, 0, -1)
                if not raw_list:
                    LOG.info("No messages to flush for key %s", key)
                    return {"status": "ok"}
            except Exception as e:
                LOG.exception("Failed reading from Redis for %s: %s", key, e)
                return {"status": "failed", "error": "Unexpected error; see server logs"}

            LOG.info("Flushing %d messages for %s", len(raw_list), key)

            try:
                _, tenant_id, orig_type = key.split(":", 2)
                batch = [json.loads(item) for item in raw_list]

                async with BaseDB.async_session(self.message_service.engine)() as session:
                    installed_platforms = await self.message_service.get_installed_platforms_from_cache_or_db(
                        session, tenant_id
                    )
                    if not installed_platforms:
                        LOG.warning("No messaging platform installation for tenant %s", tenant_id)
                        return {"status": "ok"}

                    # Each alert is matched on its own, so every channel gets only
                    # the alerts routed to it; anomalies match as troubleshooting findings.
                    routes = await self.message_service.route_units(
                        session,
                        tenant_id,
                        [grouped_unit(index, orig_type, item) for index, item in enumerate(batch)],
                        installed_platforms,
                    )
                    if not routes:
                        LOG.debug("Nothing to send for %s: every alert was stopped by the rules", key)
                        await asyncio.to_thread(redis.delete, key)
                        return {"status": "ok"}

                    platform_responses = []
                    for ip, channel, indexes in routes:
                        self.message_service.point_at(installed_platforms, ip, channel)
                        events = [batch[index]["parameters"] for index in indexes]
                        if orig_type == "anomaly" and settings.notifications.digest_resolved_enabled:
                            # A firing anomaly and its resolve in one window show once, as resolved.
                            events = latest_per_key(events)
                        params = {"events": events}
                        platform_responses.extend(
                            await self.message_service.send_template_notification(
                                session, tenant_id, {"parameters": params}, f"grouped_{orig_type}", [ip]
                            )
                            or []
                        )

                    if platform_responses and all(
                        resp.get("status") == "success" for resp in platform_responses if isinstance(resp, dict)
                    ):
                        await asyncio.to_thread(redis.delete, key)
                        LOG.debug("Successfully flushed and deleted Redis key %s", key)
                        return {"status": "ok"}
                    else:
                        LOG.warning(
                            "Not all notifications sent successfully for %s (responses: %s), keeping messages in Redis",
                            key,
                            platform_responses,
                        )
                        return {"status": "partial_failure", "delivery_status": platform_responses}

            except Exception as e:
                LOG.exception("Error flushing group %s: %s", key, e)
                return {"status": "failed", "error": "Unexpected error; see server logs"}


# ----------------------------- MessageService -----------------------------


class MessageService:
    def __init__(self, engine, slack_app, teams_app):
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.engine = engine
        self.cache = cache
        self.rules_service = NotificationRulesService(self.engine)
        self.rule_matcher = NotificationRuleMatcher(self.rules_service)
        self._group_tasks: Dict[str, Dict[str, Any]] = {}
        self._flush_locks: Dict[str, Lock] = defaultdict(Lock)

        # Senders
        self.slack_sender = SlackSender(slack_app, cache)
        self.gchat_sender = GoogleChatSender(cache)
        self.discord_sender = DiscordSender(cache)

        # grouped handler
        self.group_handler = GroupedMessageHandler(cache, self)

    # ----------------------------- High level send -----------------------------
    async def send_message(self, request: SendMessageRequest):
        tenant_id = request.tenant_id
        type_ = request.type
        source = request.source or ""
        thread = request.thread

        # Convert to dict once for downstream code that still uses dict access
        payload = request.dict(exclude_none=True, by_alias=True)
        parameters = payload.get("parameters", {})

        try:
            if thread:
                # Threaded replies bypass rules, except automation messages: a
                # rule that silences task chatter has to cover threaded posts too.
                if source == AUTOMATION_SOURCE:
                    async with BaseDB.async_session(self.engine)() as session:
                        matched_rules, _ = await self.rule_matcher.match_notification_rules(
                            session, tenant_id, type_, payload
                        )
                    default_policy = await self.rule_matcher.rules_service.get_default_policy(tenant_id, source)
                    outcome = resolve_outcome(
                        matched_rules,
                        source=source,
                        event_type=payload.get("event_type"),
                        default_policy=default_policy,
                    )
                    if outcome.response is not None:
                        return outcome.response
                return await self.send_threaded_reply(tenant_id, thread, parameters)

            if type_ == "finding_resolved":
                # An anomaly's firing went out in a grouped digest, which records
                # no thread to reply in; its resolve joins the next digest instead.
                if settings.notifications.digest_resolved_enabled:
                    group_result = await self._maybe_enqueue_anomaly(parameters, tenant_id)
                    if group_result is not None:
                        return group_result
                return await self.send_finding_resolved_reply(tenant_id, parameters)

            if type_ == "finding_returned":
                return await self._queue_returned_anomaly(parameters, tenant_id)

            group_result = await self._maybe_handle_grouped_message(source, parameters, tenant_id, payload)
            if group_result is not None:
                return group_result

            sections = account_sections(type_, parameters)
            if sections:
                return await self.send_rollup(tenant_id, type_, payload, sections)

            async with BaseDB.async_session(self.engine)() as session:
                matched_rules, source = await self.rule_matcher.match_notification_rules(
                    session, tenant_id, type_, payload
                )

                if type_ == "finding":
                    finding_priority = (parameters.get("finding") or {}).get("priority")
                    if is_realtime_severity_gated(finding_priority, matched_rules):
                        LOG.debug(
                            "Finding gated from real-time delivery (priority=%s, tenant=%s)",
                            finding_priority,
                            tenant_id,
                        )
                        return [
                            system_response(
                                "severity_gated",
                                "Low/informational findings are digest-only by default; "
                                "add a notification rule with this severity for real-time delivery",
                            )
                        ]

                default_policy = await self.rule_matcher.rules_service.get_default_policy(tenant_id, source)
                outcome = resolve_outcome(
                    matched_rules, source=source, event_type=payload.get("event_type"), default_policy=default_policy
                )
                if outcome.response is not None:
                    return outcome.response

                installed_platforms = await self.get_installed_platforms_from_cache_or_db(session, tenant_id)
                if not installed_platforms:
                    return [failed_response("", reason="No messaging platform installation found for this tenant")]

                self.get_channels_from_request_or_defaults(
                    installed_platforms, outcome.targets, source, payload, use_defaults=outcome.use_defaults
                )
                await self._apply_gchat_default_fallback(session, tenant_id, installed_platforms, payload)

                fingerprint = get_fingerprint(type_, payload)
                return await send_in_rounds(
                    installed_platforms,
                    lambda platforms: self.trigger_notification(
                        session, fingerprint, platforms, payload, parameters, [], tenant_id, type_
                    ),
                )

        except Exception as exc:
            parameters_copy = dict(parameters)
            if "finding" in parameters_copy and parameters_copy["finding"] is not None:
                parameters_copy["finding"].pop("description", None)
                parameters_copy["finding"].pop("evidences", None)
            LOG.exception(f"Can't send message: {exc}, message: {parameters_copy}")
            return []

    async def _maybe_handle_grouped_message(self, source, parameters, tenant_id, kwargs):
        if source and source.lower() == "slo":
            return await self.group_handler.enqueue(kwargs)

        return await self._maybe_enqueue_anomaly(parameters, tenant_id)

    async def _maybe_enqueue_anomaly(self, parameters, tenant_id, returned=False):
        """Queue an anomaly finding, firing, resolved or returned, for the
        grouped anomaly digest. None when the finding is not an anomaly."""
        finding = parameters.get("finding") or {}
        finding_source = finding.get("source", "")
        if finding_source and finding_source.lower() == "anomaly":
            if returned:
                finding = {**finding, "returned": True}
            anomaly = {
                "source": "anomaly",
                "type": "anomaly",
                "tenant_id": tenant_id,
                "parameters": finding,
            }
            return await self.group_handler.enqueue(anomaly)

        return None

    async def _queue_returned_anomaly(self, parameters, tenant_id):
        """An anomaly that had cleared and fired again joins the next digest,
        labelled. No other source has a message for a return."""
        finding_source = (parameters.get("finding") or {}).get("source") or ""
        if finding_source.lower() != "anomaly":
            return [system_response("dropped", "A returned finding is only announced in the anomaly digest")]
        group_result = await self._maybe_enqueue_anomaly(parameters, tenant_id, returned=True)
        if group_result is not None:
            return group_result
        # enqueue logs its own failure and returns None; say so rather than
        # report the anomaly as unsupported.
        return [system_response("failed", "Could not queue the returned anomaly for the digest")]

    async def trigger_notification(
        self,
        session,
        fingerprint,
        installed_platforms,
        kwargs,
        parameters,
        platform_responses,
        tenant_id,
        type_,
    ):
        if type_ == "finding":
            new_sent_notifications, platform_responses = await self.send_finding_notification(
                session, tenant_id, parameters, fingerprint, installed_platforms
            )
            if new_sent_notifications:
                session.add_all(new_sent_notifications)
                await session.commit()
        elif type_ in template_mapping:
            platform_responses = await self.send_template_notification(
                session, tenant_id, kwargs, type_, installed_platforms
            )
            if session.info.pop(STORED_TEAMS_CARDS, False):
                await session.commit()
        else:
            LOG.warning("No template found for notification type '%s' — notification not delivered", type_)
        return platform_responses

    # ----------------------------- Installation loading -----------------------------
    async def get_installed_platforms_from_cache_or_db(self, session, tenant_id):
        if self.cache and self.cache.redis_client:
            installed_platforms = self.cache.get_installations(tenant_id)
            if installed_platforms is not None:
                return installed_platforms

        installed_platforms = await self.get_installed_platforms(session, tenant_id=tenant_id)

        if installed_platforms and self.cache and self.cache.redis_client:
            self.cache.cache_installations(tenant_id, installed_platforms)

        if installed_platforms:
            return installed_platforms

        return []

    # ----------------------------- Rule and channel helpers -----------------------------
    async def match_notification_rules(self, session, tenant_id, type_, kwargs):
        return await self.rule_matcher.match_notification_rules(session, tenant_id, type_, kwargs)

    @staticmethod
    async def get_installed_platforms(session, **kwargs):
        filter_conditions = {key: value for key, value in kwargs.items() if value is not None}
        result = await session.execute(select(MessagingPlatform).filter_by(**filter_conditions))
        installs = list(result.scalars().all())
        for ip in installs:
            ip._origin = ORIGIN_LEGACY
        tenant_id = filter_conditions.get("tenant_id")
        if tenant_id is None:
            return installs
        # Slack/MS Teams migrating to integrations: an integration install (one per
        # tenant) wins over the legacy messaging_platforms row of the same platform.
        requested = filter_conditions.get("platform")
        target_platforms = [requested] if requested in MESSAGING_PLATFORMS else list(MESSAGING_PLATFORMS)
        integration_installs = []
        for platform in target_platforms:
            integration_installs += await load_integration_installations_async(session, tenant_id, platform)
        # Webhook targets: many per tenant, no legacy counterpart, routed per-target
        # in get_channels_from_request_or_defaults (never a tenant default).
        if requested is None or requested in WEBHOOK_PLATFORM_TYPES:
            integration_installs += await load_ms_teams_webhook_installations_async(session, tenant_id)
            integration_installs += await load_slack_webhook_installations_async(session, tenant_id)
            integration_installs += await load_google_chat_webhook_installations_async(session, tenant_id)
        if not integration_installs:
            return installs
        overridden = {ip.platform for ip in integration_installs}
        return [ip for ip in installs if ip.platform not in overridden] + integration_installs

    async def route_units(self, session, tenant_id, units, installed_platforms):
        """Match each unit — (key, message type, payload) for one account or one
        alert — on its own and group the keys by the channel they go to. Returns
        [(installation, channel, [keys])]; stopped units go nowhere."""
        channels = {}
        for key, type_, payload in units:
            matched_rules, source = await self.rule_matcher.match_notification_rules(session, tenant_id, type_, payload)
            default_policy = await self.rule_matcher.rules_service.get_default_policy(tenant_id, source)
            outcome = resolve_outcome(
                matched_rules, source=source, event_type=payload.get("event_type"), default_policy=default_policy
            )
            if outcome.response is not None:
                continue
            self.get_channels_from_request_or_defaults(
                installed_platforms, outcome.targets, source, payload, use_defaults=outcome.use_defaults
            )
            await self._apply_gchat_default_fallback(session, tenant_id, installed_platforms, payload)
            for ip in installed_platforms:
                for channel in [ip.to_channel] + list(getattr(ip, "additional_channels", None) or []):
                    if not channel:
                        continue
                    channel_key = (str(ip.id), ip.platform, json.dumps(channel, sort_keys=True, default=str))
                    channels.setdefault(channel_key, (ip, channel, []))[2].append(key)
        return list(channels.values())

    @staticmethod
    def point_at(installed_platforms, ip, channel):
        """Aim delivery at one channel of one installation only."""
        for other in installed_platforms:
            other.to_channel = None
            other.additional_channels = []
        ip.to_channel = channel

    async def send_rollup(self, tenant_id, type_, payload, sections):
        """Send a message covering several accounts: one message per channel, each
        with only the accounts routed to that channel."""
        parameters = payload.get("parameters") or {}
        unit = {key: payload[key] for key in ("source", "type", "channels") if payload.get(key)}
        units = [(account_id, type_, {**unit, "parameters": {"account_id": account_id}}) for account_id in sections]
        async with BaseDB.async_session(self.engine)() as session:
            installed_platforms = await self.get_installed_platforms_from_cache_or_db(session, tenant_id)
            if not installed_platforms:
                return [failed_response("", reason="No messaging platform installation found for this tenant")]
            routes = await self.route_units(session, tenant_id, units, installed_platforms)
            if not routes:
                return [system_response("suppressed", "Every account in this message was left out by the rules")]
            routes = [route for route in routes if has_recommendations(parameters, route[2])]
            if not routes:
                return [system_response("dropped", "No account routed to a channel has anything to send")]
            responses = []
            for ip, channel, account_ids in routes:
                self.point_at(installed_platforms, ip, channel)
                responses.extend(
                    await self.send_template_notification(
                        session, tenant_id, {"parameters": merged_parameters(parameters, account_ids)}, type_, [ip]
                    )
                    or []
                )
            return responses

    @staticmethod
    def get_channels_from_request_or_defaults(installed_platforms, matched_rules, source, kwargs, use_defaults=True):
        """Set each installation's target channel(s) from the rules that receive
        the message. A platform no rule names gets its default channel only when
        use_defaults; further channels on one platform go in additional_channels."""
        all_channels = kwargs.get("channels", {})

        for ip in installed_platforms:
            platform = ip.platform
            ip.additional_channels = []

            # Webhooks route only to the target a rule names by id in its channels
            # JSON; no rule -> to_channel stays None -> skipped (no tenant default).
            # A directed send (explicit `channels`) silences them like any other
            # platform it doesn't name.
            if platform in WEBHOOK_PLATFORM_TYPES:
                if all_channels:
                    ip.to_channel = None
                    continue
                ip.to_channel = next(
                    (
                        rule["channels"]
                        for rule in matched_rules
                        if rule.get("platform") == platform
                        and isinstance(rule.get("channels"), dict)
                        and rule["channels"].get("webhook_target_id") is not None
                        and str(rule["channels"]["webhook_target_id"]) == str(ip.id)
                    ),
                    None,
                )
                continue

            to_channel = all_channels.get(platform) if all_channels else None

            if all_channels and to_channel is None:
                ip.to_channel = None
                continue

            if to_channel is not None:
                ip.to_channel = MessageService.get_payload_channel(
                    ip, matched_rules, platform, to_channel, use_defaults
                )
            else:
                rule_channels = _distinct_rule_channels(matched_rules, platform)
                if rule_channels:
                    ip.to_channel = rule_channels[0]
                    ip.additional_channels = rule_channels[1:]
                else:
                    ip.to_channel = ip.channels if use_defaults else None

    @staticmethod
    def get_payload_channel(ip, matched_rules, platform, to_channel, use_defaults=True):
        if to_channel:
            return to_channel

        rule_channels = _distinct_rule_channels(matched_rules, platform)
        if rule_channels:
            return rule_channels[0]
        return ip.channels if use_defaults else None

    @staticmethod
    def _normalize_teams_channels(ip):
        """Normalize Teams channel format for API calls. Used by both finding and template senders."""
        teams_channels = normalize_channel(ip.to_channel)
        if isinstance(teams_channels, dict) and "channels" not in teams_channels:
            teams_channels = {
                "team_id": teams_channels.pop("team_id", None) or ip.team_id,
                "channels": [teams_channels],
            }
        return teams_channels

    # ----------------------------- Finding / template senders -----------------------------
    async def send_finding_notification(self, session, tenant_id, parameters, fingerprint, installed_platforms):
        finding = parameters.get("finding")

        # One row per platform that threads (Slack, Discord) — collecting only the
        # last one would break dedup for the other platform.
        new_sent_notifications = []
        platform_responses = []

        for ip in installed_platforms:
            if not ip.to_channel:
                continue

            handler = self._get_finding_sender(ip.platform)
            if not handler:
                LOG.warning("Unable to identify platform %s with installation id %s", ip.platform, ip.id)
                continue

            response_data, sent_notification = await handler(session, tenant_id, ip, finding, fingerprint)
            if sent_notification:
                new_sent_notifications.append(sent_notification)
            if response_data:
                platform_responses.append(response_data)

        return new_sent_notifications, platform_responses

    def _get_finding_sender(self, platform):
        return {
            PlatformTypes.SLACK.value: self._send_finding_to_slack,
            PlatformTypes.MS_TEAMS.value: self._send_finding_to_teams,
            PlatformTypes.MS_TEAMS_WEBHOOK.value: self._send_finding_to_teams_webhook,
            PlatformTypes.SLACK_WEBHOOK.value: self._send_finding_to_slack_webhook,
            PlatformTypes.GOOGLE_CHAT.value: self._send_finding_to_gchat,
            PlatformTypes.GOOGLE_CHAT_WEBHOOK.value: self._send_finding_to_gchat_webhook,
            PlatformTypes.DISCORD.value: self._send_finding_to_discord,
        }.get(platform)

    async def _send_finding_to_slack(self, session, tenant_id, ip, finding, fingerprint):
        # Look up the thread at the channel the message is actually posted to
        # (rule-routed channel or default), not the installation default.
        thread_id = await self.slack_sender.check_if_sent_already(session, fingerprint, ip.team_id, ip.to_channel)
        message, output_blocks, attachments = get_slack_finding_message(self.slack_app, ip, finding)

        # Empty text/blocks must be OMITTED: with no blocks Slack renders a
        # non-empty text as a duplicate heading, and slack_sdk drops None
        # kwargs cleanly (the hybrid finding card is attachment-only).
        slack_response = await asyncio.to_thread(
            self.slack_sender.post_to_slack,
            tenant_id,
            ip,
            thread_id,
            text=message or None,
            blocks=output_blocks or None,
            attachments=attachments,
            display_as_bot=True,
        )

        if not (slack_response and slack_response.status_code == 200 and slack_response.data.get("ok")):
            return None, None

        account_id = finding.get("cloud_account_id") if finding else None
        sent_notification = None

        if not thread_id and fingerprint:
            sent_notification = self.slack_sender.get_new_sent_notification(
                ip.team_id, tenant_id, slack_response, fingerprint, account_id
            )

        response_data = success_response(
            "slack",
            team_id=ip.team_id,
            channel_id=slack_response.data.get("channel"),
            message_ts=slack_response.data.get("ts"),
        )

        return response_data, sent_notification

    async def send_finding_resolved_reply(self, tenant_id, parameters):
        """Thread-only closure when a notified finding clears: green reply in
        the Slack thread(s) recorded in SentNotifications, at the channel the
        firing message actually went to. Dropped silently when the firing
        message was never sent; the resolved_notified stamp dedupes repeated
        resolve webhooks. Slack-only — other platforms keep no thread record
        usable here."""
        finding = parameters.get("finding") or {}
        fingerprint = finding.get("fingerprint")
        account_id = finding.get("cloud_account_id")
        if not (tenant_id and fingerprint):
            return [system_response("dropped", "Resolved finding lacks a fingerprint")]

        async with BaseDB.async_session(self.engine)() as session:
            query = select(SentNotifications).filter_by(tenant_id=tenant_id, fingerprint=fingerprint)
            # Only Slack rows have a thread to reply in; skip the rest in the query.
            query = query.filter(SentNotifications.slack_thread_id.isnot(None))
            if account_id:
                query = query.filter_by(account_id=account_id)
            rows = (await session.execute(query)).scalars().all()
            rows = [row for row in rows if row.slack_thread_id and row.slack_team_id and row.slack_metadata]
            if not rows:
                return [system_response("dropped", "No delivered firing message for this finding")]

            installs = await self.get_installed_platforms(
                session, tenant_id=tenant_id, platform=PlatformTypes.SLACK.value
            )
            installs_by_team = {ip.team_id: ip for ip in installs}

            responses = []
            stamped = False
            for row in rows:
                try:
                    slack_metadata = json.loads(row.slack_metadata)
                except json.JSONDecodeError:
                    continue
                if not isinstance(slack_metadata, dict):
                    continue
                channel_id = slack_metadata.get("channel")
                ip = installs_by_team.get(row.slack_team_id)
                if slack_metadata.get("resolved_notified") or not (channel_id and ip):
                    continue

                # Isolate per-thread failures (revoked token, deleted channel,
                # rate limit) so one bad thread doesn't abort the fan-out.
                _, _, attachments = get_slack_resolved_finding_reply(finding)
                try:
                    slack_response = await asyncio.to_thread(
                        self.slack_app.client.reply_in_thread,
                        token=ip.token,
                        channel_id=channel_id,
                        thread_ts=row.slack_thread_id,
                        attachments=attachments,
                        display_as_bot=True,
                    )
                except Exception as exc:
                    LOG.warning("Failed to post resolved reply to thread %s: %s", row.slack_thread_id, exc)
                    responses.append(failed_response("slack", reason="Failed to post resolved reply"))
                    continue

                if not (slack_response and slack_response.status_code == 200 and slack_response.data.get("ok")):
                    responses.append(failed_response("slack", reason="Failed to post resolved reply"))
                    continue

                row.slack_metadata = json.dumps({**slack_metadata, "resolved_notified": True})
                session.add(row)
                stamped = True
                responses.append(
                    success_response(
                        "slack",
                        team_id=row.slack_team_id,
                        channel_id=channel_id,
                        message_ts=slack_response.data.get("ts"),
                    )
                )

            if stamped:
                await session.commit()

        return responses or [system_response("dropped", "Resolved reply already delivered")]

    async def _post_teams_card_as_bot(self, session, tenant_id, team_id, channel_id, card, root_message_id=None):
        """Post a card through the bot to a channel of an installed team, or as a
        reply under root_message_id. Returns the Teams message id."""
        team = await load_team_async(session, tenant_id, team_id)
        if team is None:
            raise TeamsBotError(APP_NOT_IN_TEAM)
        sender = get_sender()
        await asyncio.to_thread(sender.check_channel, team, channel_id)
        conversation_id = thread_conversation_id(channel_id, root_message_id) if root_message_id else channel_id
        return await asyncio.to_thread(sender.send, team.service_url, conversation_id, card_activity(card))

    async def _send_teams_template_as_bot(self, session, ip, card):
        """Post to the mapping's first channel."""
        teams_channels = self._normalize_teams_channels(ip)
        channels_list = teams_channels.get("channels", []) if isinstance(teams_channels, dict) else []
        channel_id = channels_list[0].get("id") if channels_list else None
        team_id = teams_channels.get("team_id") if isinstance(teams_channels, dict) else None
        if not (team_id and channel_id):
            return failed_response("ms_teams", reason="No Microsoft Teams channel is mapped")
        try:
            message_id = await self._post_teams_card_as_bot(session, ip.tenant_id, team_id, channel_id, card)
        except TeamsBotError as e:
            LOG.warning("Teams bot send to team %s failed: %s", team_id, e)
            return failed_response("ms_teams", reason=str(e))
        return success_response("ms_teams", team_id=team_id, channel_id=channel_id, message_ts=message_id)

    async def _send_finding_to_teams(self, session, tenant_id, ip, finding, fingerprint):
        # A failure is returned too, so its reason reaches the delivery status.
        card = get_ms_teams_finding_message_template(finding, ip.tenant_id)
        response = await self._send_teams_template_as_bot(session, ip, card)
        if response.get("status") != "success":
            return response, None
        # Stored so a click can update it and a reply in its thread can read it.
        row = card_actions.card_row(
            ip.tenant_id,
            finding.get("cloud_account_id"),
            fingerprint,
            response.get("channel_id"),
            response.get("message_ts"),
            card,
            "finding",
        )
        return response, row

    async def _send_finding_to_teams_webhook(self, session, tenant_id, ip, finding, _):
        """POST a finding card to ip.webhook_url. Returns (response, None) — webhooks
        don't thread, so no sent_notification row."""
        if not getattr(ip, "webhook_url", None):
            return None, None

        response = await asyncio.to_thread(
            MsTeamsClient.post_to_ms_teams_webhook,
            ip.webhook_url,
            get_ms_teams_finding_message_template(finding),
            tenant_id,
        )
        if not (response is not None and 200 <= response.status_code < 300):
            return None, None

        return success_response("ms_teams_webhook", channel_id=str(ip.id)), None

    async def _send_finding_to_slack_webhook(self, session, tenant_id, ip, finding, _):
        """POST a finding card to ip.webhook_url. Reuses the slack finding
        template, minus anything needing the app's interactivity endpoint (see
        strip_interactive_slack_elements) - webhooks don't thread, so no
        sent_notification row."""
        if not getattr(ip, "webhook_url", None):
            return None, None

        message, output_blocks, attachments = get_slack_finding_message(self.slack_app, ip, finding)
        payload = strip_interactive_slack_elements(
            {"text": message or None, "blocks": output_blocks or None, "attachments": attachments or None}
        )
        payload = {key: value for key, value in payload.items() if value}

        response = await asyncio.to_thread(
            SlackClient.post_to_slack_webhook,
            ip.webhook_url,
            payload,
            tenant_id,
        )
        if not (response is not None and 200 <= response.status_code < 300):
            return None, None

        return success_response("slack_webhook", channel_id=str(ip.id)), None

    @staticmethod
    async def _resolve_default_gchat_space(session, tenant_id):
        """Tenant's default Google Chat space (single binding = implicit default; else
        the one flagged is_default='true'). Returns the space id or None."""
        result = await session.execute(
            select(Integration).where(
                Integration.tenant_id == tenant_id,
                Integration.type == "google_chat_space",
                Integration.status != "disabled",
            )
        )
        bindings = result.scalars().all()
        if not bindings:
            return None
        if len(bindings) == 1:
            return bindings[0].name
        by_id = {binding.id: binding.name for binding in bindings}
        # Deterministic pick if the single-default invariant is ever violated
        # (e.g. a partial set-default write left two rows flagged).
        cfg = await session.execute(
            select(IntegrationConfigValue)
            .where(
                IntegrationConfigValue.integration_id.in_(list(by_id.keys())),
                IntegrationConfigValue.name == "is_default",
                IntegrationConfigValue.value == "true",
            )
            .order_by(IntegrationConfigValue.integration_id)
        )
        row = cfg.scalars().first()
        return by_id.get(row.integration_id) if row else None

    async def _apply_gchat_default_fallback(self, session, tenant_id, installed_platforms, payload):
        """Route to the tenant's default Google Chat space when a notification names no
        destination: no matched rule and no channel in the request payload. Skips when
        the payload explicitly scopes channels (a targeted send that excludes gchat)."""
        if (payload or {}).get("channels"):
            return
        pending = [ip for ip in installed_platforms if ip.platform == "google_chat" and not ip.to_channel]
        if not pending:
            return
        default_space = await self._resolve_default_gchat_space(session, tenant_id)
        if not default_space:
            return
        for ip in pending:
            ip.to_channel = {"id": default_space}

    async def _send_finding_to_gchat(self, session, tenant_id, ip, finding, _):
        if not GoogleChatAppClient.is_enabled():
            return (
                failed_response("google_chat", reason="Google Chat service account is not configured"),
                None,
            )

        result = await asyncio.to_thread(
            GoogleChatAppClient.post_message,
            ip.to_channel,
            get_markdown_message_template(finding),
            tenant_id,
        )

        if result.get("success"):
            return (
                success_response(
                    "google_chat",
                    channel_id=result.get("channel_id"),
                    message_ts=result.get("message_ts"),
                ),
                None,
            )

        return (
            failed_response(
                "google_chat",
                reason=result.get("reason"),
                channel_id=result.get("channel_id"),
            ),
            None,
        )

    async def _send_finding_to_gchat_webhook(self, session, tenant_id, ip, finding, _):
        """POST a finding card to ip.webhook_url. Reuses the plain-text/markdown
        finding template (no interactive elements exist in it) - webhooks don't
        thread, so no sent_notification row."""
        if not getattr(ip, "webhook_url", None):
            return None, None

        response = await asyncio.to_thread(
            GoogleChatClient.post_to_google_chat_webhook,
            ip.webhook_url,
            get_markdown_message_template(finding),
            tenant_id,
        )
        if not (response is not None and 200 <= response.status_code < 300):
            return None, None

        return success_response("google_chat_webhook", channel_id=str(ip.id)), None

    async def _send_finding_to_discord(self, session, tenant_id, ip, finding, fingerprint):
        token = await self.discord_sender.acquire_discord_access_token(session, ip)
        if not token:
            return None, None

        from notifications_server.clients.discord_client import DiscordClient
        from notifications_server.message_templates.discord.finding import get_discord_finding_message

        discord_payload = get_discord_finding_message(finding)

        # ip.to_channel is a {name, id} dict set by get_channels_from_request_or_defaults;
        # normalize it to extract the bare channel ID string.
        channel = normalize_channel(ip.to_channel)
        channel_id = channel.get("id") if isinstance(channel, dict) else channel

        if not channel_id:
            LOG.error("No channel provided for Discord message")
            return failed_response("discord", reason="No channel ID provided"), None

        # Repeat findings reply to the original message (Slack-thread parity)
        # instead of posting a fresh top-level message each time.
        thread_id = await self.discord_sender.check_if_sent_already(session, fingerprint, tenant_id, channel_id)
        if thread_id:
            result = await asyncio.to_thread(
                DiscordClient.reply_in_thread,
                token=token,
                channel_id=channel_id,
                thread_ts=thread_id,
                **discord_payload,
            )
        else:
            result = await asyncio.to_thread(
                DiscordClient.chat_post, token=token, channel_id=channel_id, **discord_payload
            )

        if result and result.get("ok"):
            sent_notification = None
            if not thread_id and fingerprint:
                sent_notification = self.discord_sender.get_new_sent_notification(
                    tenant_id,
                    channel_id,
                    result.get("ts"),
                    fingerprint,
                    finding.get("cloud_account_id") if finding else None,
                )
            return (
                success_response(
                    "discord",
                    channel_id=channel_id,
                    message_ts=result.get("ts"),
                ),
                sent_notification,
            )

        return (
            failed_response(
                "discord",
                reason=result.get("error") if result else "Unknown error",
                channel_id=channel_id,
            ),
            None,
        )

    async def send_template_notification(self, session, tenant, kwargs, notification_type, installed_platforms):
        platform_responses = []

        try:
            common_params_func = template_mapping[notification_type]["common_params"]
            raw_params = kwargs.get("parameters", {})
            param_value = common_params_func(**raw_params)

            if param_value is None:
                LOG.info("Skipping %s notification — no actionable items after filtering", notification_type)
                return platform_responses

            for ip in installed_platforms:
                if not ip.to_channel:
                    continue

                # Webhooks reuse the app-install templates (only the transport, and for
                # Slack the interactivity, differ).
                if ip.platform == PlatformTypes.MS_TEAMS_WEBHOOK.value:
                    template_key = PlatformTypes.MS_TEAMS.value
                elif ip.platform == PlatformTypes.SLACK_WEBHOOK.value:
                    template_key = PlatformTypes.SLACK.value
                elif ip.platform == PlatformTypes.GOOGLE_CHAT_WEBHOOK.value:
                    template_key = PlatformTypes.GOOGLE_CHAT.value
                else:
                    template_key = ip.platform
                template_func = template_mapping[notification_type].get(template_key)
                if not template_func:
                    LOG.warning(
                        "No template found for platform %s with installation id %s for type %s",
                        ip.platform,
                        ip.id,
                        notification_type,
                    )
                    continue

                response = await self.send_template_messages_to_platforms(
                    ip, template_func, param_value, session, tenant
                )
                if response:
                    platform_responses.append(response)

        except Exception as exc:
            LOG.exception(f"Failed to send message: {exc}")
            return platform_responses

        return platform_responses

    async def send_template_messages_to_platforms(self, ip, template_func, param_value, session, tenant):
        match ip.platform:
            case PlatformTypes.SLACK.value:
                return await self.send_slack_template_notification(ip, template_func, param_value, tenant, session)
            case PlatformTypes.MS_TEAMS.value:
                return await self.send_teams_template_notification(ip, template_func, param_value, session, tenant)
            case PlatformTypes.MS_TEAMS_WEBHOOK.value:
                return await self.send_teams_webhook_template_notification(ip, template_func, param_value, tenant)
            case PlatformTypes.SLACK_WEBHOOK.value:
                return await self.send_slack_webhook_template_notification(ip, template_func, param_value, tenant)
            case PlatformTypes.GOOGLE_CHAT.value:
                return await self.send_google_chat_template_notification(
                    ip, template_func, param_value, session, tenant
                )
            case PlatformTypes.GOOGLE_CHAT_WEBHOOK.value:
                return await self.send_google_chat_webhook_template_notification(ip, template_func, param_value, tenant)
            case PlatformTypes.DISCORD.value:
                return await self.send_discord_template_notification(ip, template_func, param_value, session, tenant)
            case _:
                LOG.warning("Unable to identify platform %s with installation id %s", ip.platform, ip.id)
                return None

    async def send_slack_template_notification(self, ip, template_func, param_value, tenant, session=None):
        response = await asyncio.to_thread(
            self.slack_sender.post_to_slack, tenant, ip, None, **template_func(param_value)
        )
        if response and response.status_code == 200 and not response.data.get("error"):
            channel_id = response.data.get("channel")
            message_ts = response.data.get("ts")
            await self.attach_weekly_digest_pdf(session, ip, param_value, channel_id, message_ts)
            return success_response("slack", team_id=ip.team_id, channel_id=channel_id, message_ts=message_ts)
        return failed_response("slack", team_id=ip.team_id)

    async def attach_weekly_digest_pdf(self, session, ip, param_value, channel_id, message_ts):
        """Attach the full review as a PDF, in a thread under the summary.

        Threaded rather than posted to the channel so the weekly message stays
        one item in the channel and the report hangs off it.

        Entirely best-effort: the summary and its link have already been
        delivered by the time this runs, and a review nobody can generate a PDF
        for is still a review. Every failure path logs and returns.
        """
        # Only the weekly digest carries a period_start; every other
        # template falls straight through.
        period_start = getattr(param_value, "period_start", None)
        tenant_id = getattr(param_value, "organization_id", None)
        if not (session and period_start and tenant_id and channel_id and message_ts):
            LOG.info(
                "weekly digest: skipping PDF (session=%s period_start=%s tenant=%s channel=%s ts=%s)",
                bool(session),
                period_start,
                tenant_id,
                channel_id,
                message_ts,
            )
            return

        try:
            digest = await weekly_digest_report.fetch_digest(session, tenant_id, period_start)
            if not digest:
                LOG.info("weekly digest: no stored digest for %s/%s, skipping the PDF", tenant_id, period_start)
                return

            context = weekly_digest_report.build_context(
                digest,
                verdict=digest_verdict(param_value),
                period_label=getattr(param_value, "period_label", "") or period_start,
                accounts=getattr(param_value, "accounts_named", 0),
            )
            pdf = await asyncio.to_thread(weekly_digest_report.build_pdf, context)
            if not pdf:
                # The Slack footer promises an attachment and carries no link,
                # so a failed render would otherwise leave the review with no
                # route at all. Fall back to posting the link in the thread.
                await asyncio.to_thread(
                    self.slack_sender.post_to_slack,
                    tenant_id,
                    ip,
                    message_ts,
                    text=f"Full review: {digest_link(param_value)}",
                )
                return

            filename = weekly_digest_report.report_filename(period_start)
            # The PDF goes up as bytes rather than via a temp file: SlackClient's
            # `fname` is forwarded straight to files_upload_v2's `file`, which
            # takes a path or the content itself. The parameter keeps its name
            # because the wrapper's other caller passes a path.
            await asyncio.to_thread(
                self.slack_app.client.file_upload,
                token=ip.token,
                title=f"Weekly digest · {context['period_label']}",
                fname=pdf,
                filename=filename,
                channel=channel_id,
                thread_ts=message_ts,
                initial_comment="Full review attached.",
            )
            LOG.info("weekly digest: attached %s (%d bytes) to %s", filename, len(pdf), channel_id)
        except Exception:
            LOG.exception("weekly digest: attaching the PDF failed; the summary was delivered")

    async def send_teams_template_notification(self, ip, template_func, param_value, session, tenant):
        card = card_actions.with_approval_buttons(template_func(param_value), param_value, ip.tenant_id)
        response = await self._send_teams_template_as_bot(session, ip, card)
        # A card with buttons is stored, so a click can update it. It's committed
        # by the caller with the rest of the notification (trigger_notification).
        if session is not None and response.get("status") == "success" and card_actions.has_actions(card):
            row = card_actions.card_row(
                ip.tenant_id,
                _approval_account_id(param_value),
                None,
                response.get("channel_id"),
                response.get("message_ts"),
                card,
                "approval",
            )
            if row is not None:
                session.add(row)
                session.info[STORED_TEAMS_CARDS] = True
        return response

    async def send_teams_webhook_template_notification(self, ip, template_func, param_value, tenant):
        """Template send via webhook: same ms_teams card, POSTed to ip.webhook_url."""
        if not getattr(ip, "webhook_url", None):
            return failed_response("ms_teams_webhook", reason="Webhook target has no URL")

        response = await asyncio.to_thread(
            MsTeamsClient.post_to_ms_teams_webhook, ip.webhook_url, template_func(param_value), tenant
        )

        if response is not None and 200 <= response.status_code < 300:
            return success_response("ms_teams_webhook", channel_id=str(ip.id))

        reason = response.text[:512] if response is not None else "Unable to send teams webhook notification"
        return failed_response("ms_teams_webhook", reason=reason)

    async def send_slack_webhook_template_notification(self, ip, template_func, param_value, tenant):
        """Template send via webhook: same slack template, minus interactive
        elements the webhook has no app behind to service, POSTed to
        ip.webhook_url."""
        if not getattr(ip, "webhook_url", None):
            return failed_response("slack_webhook", reason="Webhook target has no URL")

        payload = strip_interactive_slack_elements(template_func(param_value))

        response = await asyncio.to_thread(SlackClient.post_to_slack_webhook, ip.webhook_url, payload, tenant)

        if response is not None and 200 <= response.status_code < 300:
            return success_response("slack_webhook", channel_id=str(ip.id))

        reason = response.text[:512] if response is not None else "Unable to send slack webhook notification"
        return failed_response("slack_webhook", reason=reason)

    async def send_google_chat_template_notification(self, ip, template_func, param_value, session, tenant_id):
        if not GoogleChatAppClient.is_enabled():
            return failed_response("google_chat", reason="Google Chat service account is not configured")

        result = await asyncio.to_thread(
            GoogleChatAppClient.post_message,
            normalize_channel(ip.to_channel),
            template_func(param_value),
            tenant_id,
        )

        if result.get("success"):
            return success_response(
                "google_chat",
                channel_id=result.get("channel_id"),
                message_ts=result.get("message_ts"),
            )

        return failed_response(
            "google_chat",
            reason=result.get("reason"),
            channel_id=result.get("channel_id"),
        )

    async def send_google_chat_webhook_template_notification(self, ip, template_func, param_value, tenant):
        """Template send via webhook: same google_chat template, POSTed to
        ip.webhook_url."""
        if not getattr(ip, "webhook_url", None):
            return failed_response("google_chat_webhook", reason="Webhook target has no URL")

        response = await asyncio.to_thread(
            GoogleChatClient.post_to_google_chat_webhook, ip.webhook_url, template_func(param_value), tenant
        )

        if response is not None and 200 <= response.status_code < 300:
            return success_response("google_chat_webhook", channel_id=str(ip.id))

        reason = response.text[:512] if response is not None else "Unable to send google chat webhook notification"
        return failed_response("google_chat_webhook", reason=reason)

    async def send_discord_template_notification(self, ip, template_func, param_value, session, tenant):
        token = await self.discord_sender.acquire_discord_access_token(session, ip)
        if not token:
            return failed_response("discord", reason="Unable to authenticate discord installation")

        from notifications_server.clients.discord_client import DiscordClient

        # ip.to_channel may be a {name, id} dict; normalize to the bare channel id.
        channel = normalize_channel(ip.to_channel)
        channel_id = channel.get("id") if isinstance(channel, dict) else channel
        if not channel_id:
            return failed_response("discord", reason="No channel ID provided")

        result = await asyncio.to_thread(
            DiscordClient.chat_post, token=token, channel_id=channel_id, **template_func(param_value)
        )

        if result and result.get("ok"):
            return success_response("discord", channel_id=channel_id, message_ts=result.get("ts"))

        return failed_response(
            "discord",
            reason=result.get("error") if result else "Unknown error",
            channel_id=channel_id,
        )

    # ----------------------------- Token + cache helpers -----------------------------
    async def commit_session_and_clear_cache(self, ip, session):
        await MessageService._persist_installation(ip, session)
        if self.cache and self.cache.redis_client:
            self.cache.delete_cached_installations(ip.tenant_id)

    @staticmethod
    async def commit_session_and_clear_cache_static(ip, session):
        await MessageService._persist_installation(ip, session)
        if cache and cache.redis_client:
            cache.delete_cached_installations(ip.tenant_id)

    @staticmethod
    async def _persist_installation(ip, session):
        # Integration-origin installs re-encrypt the refreshed token into
        # integration_config_values; legacy installs write messaging_platforms.
        if getattr(ip, "_origin", None) == ORIGIN_INTEGRATION:
            await persist_messaging_token_async(
                session,
                ip,
                ip.token,
                refresh_token=ip.refresh_token,
                token_expires_at=ip.token_expires_at,
                refresh_token_expires_at=ip.refresh_token_expires_at,
            )
        else:
            session.add(ip)
            await session.commit()

    # ----------------------------- Threaded Reply Methods (always generic) -----------------------------
    async def send_threaded_reply(self, tenant_id, thread, parameters):
        """Send a generic threaded reply using templates. Bypasses notification rules."""
        from notifications_server.message_templates.slack.generic import (
            get_generic_message_params,
            get_slack_generic_message_template,
        )
        from notifications_server.message_templates.ms_teams.generic import get_ms_teams_generic_message_template
        from notifications_server.message_templates.google_chat.generic import get_google_chat_generic_message_template

        message_text = parameters.get("message")
        message_ts, channel_id, platform, team_id = self._extract_thread_params(thread)

        # Check required fields (team_id is only required for MS Teams)
        missing = [
            k
            for k, v in {
                "message_text": message_text,
                "message_ts": message_ts,
                "channel_id": channel_id,
                "platform": platform,
            }.items()
            if not v
        ]

        if missing:
            return [failed_response(platform or "unknown", reason=f"Missing required fields: {', '.join(missing)}")]

        # MS Teams requires team_id
        if platform == PlatformTypes.MS_TEAMS.value and not team_id:
            return [failed_response(platform, reason="team_id is required for MS Teams threaded replies")]

        # Fail fast on an empty tenant_id: get_installed_platforms drops None filters,
        # so a missing tenant would run an unscoped cross-tenant query.
        if not tenant_id:
            return [failed_response(platform, reason="Tenant ID is required")]

        try:
            async with BaseDB.async_session(self.engine)() as session:
                installs = await self.get_installed_platforms(session, tenant_id=tenant_id, platform=platform)
                ip = next((p for p in installs if p.platform == platform), None)
                if not ip:
                    return [failed_response(platform, reason="No installation found")]

                # Forward the whole parameter set, as the top-level path does. The
                # generic template decides an approval purely from approval_token and
                # renders the buttons (and the message metadata carrying the decision)
                # from it — narrowing to `message` left threaded approvals unanswerable.
                generic_params = get_generic_message_params(**parameters)

                template_factories = {
                    PlatformTypes.SLACK.value: get_slack_generic_message_template,
                    PlatformTypes.MS_TEAMS.value: get_ms_teams_generic_message_template,
                    PlatformTypes.GOOGLE_CHAT.value: get_google_chat_generic_message_template,
                }

                if platform not in template_factories:
                    return [failed_response(platform, reason="Unsupported platform")]

                template = template_factories[platform](generic_params)
                if platform == PlatformTypes.MS_TEAMS.value:
                    template = card_actions.with_approval_buttons(template, generic_params, ip.tenant_id)

                handlers = {
                    PlatformTypes.SLACK.value: lambda: self._send_slack_threaded_reply(
                        ip, channel_id, message_ts, template
                    ),
                    PlatformTypes.MS_TEAMS.value: lambda: self._send_teams_threaded_reply(
                        session, ip, team_id, channel_id, message_ts, template
                    ),
                    PlatformTypes.GOOGLE_CHAT.value: lambda: self._send_gchat_threaded_reply(
                        session, ip, channel_id, message_ts, template
                    ),
                }

                return await handlers[platform]()

        except Exception as exc:
            LOG.exception("Failed to send threaded reply: %s", exc)
            return [failed_response(platform or "unknown", reason=str(exc))]

    @staticmethod
    def _extract_thread_params(thread):
        """Extract thread parameters from dict or object."""
        if isinstance(thread, dict):
            return thread.get("message_ts"), thread.get("channel_id"), thread.get("platform"), thread.get("team_id")
        return (
            getattr(thread, "message_ts", None),
            getattr(thread, "channel_id", None),
            getattr(thread, "platform", None),
            getattr(thread, "team_id", None),
        )

    async def _send_slack_threaded_reply(self, ip, channel_id, thread_ts, template):
        """Send Slack threaded reply using generic template."""
        ip.to_channel = [{"id": channel_id}]

        # Template contains 'text', 'blocks', and 'unfurl_links'
        response = await asyncio.to_thread(
            self.slack_sender.post_to_slack, tenant=ip.tenant_id, installation=ip, thread_id=thread_ts, **template
        )

        if response and getattr(response, "status_code", None) == 200 and response.data and response.data.get("ok"):
            return [
                success_response(
                    "slack",
                    team_id=ip.team_id,
                    channel_id=channel_id,
                    message_ts=response.data.get("ts"),
                    thread_ts=thread_ts,
                )
            ]

        error_msg = response.data.get("error") if response and getattr(response, "data", None) else "Unknown error"
        return [failed_response("slack", reason=f"API call failed: {error_msg}")]

    async def _send_teams_threaded_reply(self, session, ip, team_id, channel_id, parent_message_id, template):
        """Send MS Teams threaded reply using generic template (adaptive card)."""
        try:
            message_id = await self._post_teams_card_as_bot(
                session, ip.tenant_id, team_id, channel_id, template, root_message_id=parent_message_id
            )
        except TeamsBotError as e:
            LOG.warning("Teams bot thread reply in team %s failed: %s", team_id, e)
            return [failed_response("ms_teams", reason=str(e))]
        if card_actions.has_actions(template):
            row = card_actions.card_row(ip.tenant_id, None, None, channel_id, message_id, template, "approval")
            if row is not None:
                # The card is already posted: a failed save only means clicks on it
                # reply in the thread, so it doesn't fail the send.
                try:
                    session.add(row)
                    await session.commit()
                except Exception as e:
                    await session.rollback()
                    LOG.warning("Storing Teams approval card %s failed: %s", message_id, e)
        return [
            success_response(
                "ms_teams",
                team_id=team_id,
                channel_id=channel_id,
                message_ts=message_id,
                parent_message_id=parent_message_id,
            )
        ]

    @staticmethod
    def _convert_gchat_message_to_thread_name(message_name, space_id):
        """
        Convert a Google Chat message name to thread name.

        Message name format: spaces/{space}/messages/{thread_id}.{message_id}
        Thread name format: spaces/{space}/threads/{thread_id}

        If already a thread name, returns as-is.
        Returns None if a valid thread name cannot be constructed.
        """
        if not message_name:
            return None

        # Already a thread name
        if "/threads/" in message_name:
            return message_name

        # Convert message name to thread name
        if "/messages/" in message_name:
            # Extract: spaces/{space}/messages/{msg_id} -> spaces/{space}, {msg_id}
            parts = message_name.split("/messages/")
            space_part = parts[0]  # spaces/{space}
            msg_id = parts[1] if len(parts) > 1 else ""

            # Thread ID is the first part before the dot (e.g., "abc.xyz" -> "abc")
            thread_id = msg_id.split(".")[0] if msg_id else ""

            if thread_id and space_part:
                return f"{space_part}/threads/{thread_id}"

            # Malformed message name (e.g., ends with /messages/ or missing space part)
            return None

        # Fallback: assume it's just the thread ID, requires space_id to construct full path
        if space_id:
            return f"{space_id}/threads/{message_name}"

        # Cannot construct valid thread name without space_id
        return None

    async def _send_gchat_threaded_reply(self, session, ip, space_id, message_ts, template):
        """Send Google Chat threaded reply using generic template (plain text)."""
        if not GoogleChatAppClient.is_enabled():
            return [failed_response("google_chat", reason="Google Chat service account is not configured")]

        # Convert message name to thread name for replying
        thread_name = self._convert_gchat_message_to_thread_name(message_ts, space_id)

        # Template is plain text string
        result = await asyncio.to_thread(
            GoogleChatAppClient.post_message,
            space_id,
            template,
            ip.tenant_id,
            thread_name,
        )

        if result and result.get("success"):
            return [
                success_response(
                    "google_chat",
                    channel_id=space_id,
                    message_ts=result.get("message_ts"),
                    thread_name=thread_name,
                )
            ]

        return [failed_response("google_chat", reason=result.get("reason", "Unknown error"))]
