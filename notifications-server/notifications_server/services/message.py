import asyncio
import inspect
import json
import logging
import time
import uuid
from datetime import timedelta
from typing import Dict, Any, List

from notifications_server.utils.datetime_utils import utc_now
from asyncio import Lock
from collections import defaultdict

from sqlalchemy import select

from notifications_server.clients.google_chat_client import GoogleChatClient
from notifications_server.clients.ms_teams_client import MsTeamsClient
from notifications_server.configs import settings

from notifications_server.models.enums import PlatformTypes
from notifications_server.schemas.message import SendMessageRequest
from notifications_server.services.cache import Cache
from notifications_server.services.response_helpers import success_response, failed_response, system_response
from notifications_server.services.rules import NotificationRulesService
from notifications_server.services.slack.slack import retriable_slack_api_error, slack_channel_not_found_error
from notifications_server.message_templates import template_mapping
from notifications_server.message_templates.google_chat.finding import get_markdown_message_template
from notifications_server.message_templates.ms_teams.finding import get_ms_teams_finding_message_template
from notifications_server.message_templates.slack.finding import get_slack_finding_message
from notifications_server.models.db_base import BaseDB
from notifications_server.models.models import (
    SentNotifications,
    NotificationRuleMappings,
    MessagingPlatform,
)

LOG = logging.getLogger(__name__)
cache = Cache()


# ----------------------------- Utility functions -----------------------------


def get_fingerprint(type_, kwargs):
    parameters = kwargs.get("parameters", {})
    if type_ == "finding":
        return (parameters.get("finding") or {}).get("fingerprint")
    return parameters.get("fingerprint")


def is_suppressed(matched_rules: List[Dict[str, Any]]):
    return any(rule["suppressed"] for rule in matched_rules)


def is_snoozed(matched_rules: List[Dict[str, Any]]):
    # expires_at is stored as a naive datetime (UTC) in DB and cache,
    # so compare with naive UTC to avoid TypeError.
    now = utc_now()
    return any(rule["expires_at"] and rule["expires_at"] > now for rule in matched_rules)


def is_batch_delivery(matched_rules: List[Dict[str, Any]]):
    return any(rule.get("delivery_mode") == "batch" for rule in matched_rules)


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
# "optimize", "slo", "cloud" (plus "auto_pilot"). A finding's `source`, however,
# is the raw *ingestion* source (e.g. "datadog_webhook", "prometheus", "anomaly",
# "ebpf", "traces", "grafana", "newrelic"). Those never equal "troubleshoot", so
# matching them verbatim against rule.source sends every troubleshoot finding to
# the default channel (#28130). Map any source that is not itself a rule category
# to "troubleshoot"; "cloud"/"optimize"/"slo" findings keep their category.
RULE_SOURCE_CATEGORIES = {"troubleshoot", "optimize", "slo", "cloud", "auto_pilot"}


def _finding_rule_source(source):
    return source if source in RULE_SOURCE_CATEGORIES else "troubleshoot"


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

        if source in {"auto_pilot", "slo", "optimize"}:
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

        all_rules = await self.rules_service.get_notification_rules(tenant_id)
        if not all_rules:
            LOG.debug("No rules found for tenant %s (source=%s), using defaults", tenant_id, source)
            return [], source

        truly_matched_rules = [
            rule
            for rule in all_rules
            if (
                rule.source == source
                and _ids_match(rule.account_id, account_id)
                and (rule.namespace == namespace or rule.namespace is None)
                and (rule.workload == workload or rule.workload is None)
                and (rule.aggregation_key == aggregation_key or rule.aggregation_key is None)
                and rule.is_active
            )
        ]

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
                        "suppressed": rule.is_suppressed,
                        "platform": mapping.platform,
                        "channels": mapping.channels,
                        "expires_at": rule.expires_at,
                        "delivery_mode": rule.delivery_mode,
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
                        "suppressed": rule.is_suppressed,
                        "platform": None,
                        "channels": None,
                        "expires_at": rule.expires_at,
                        "delivery_mode": rule.delivery_mode,
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
        result = await session.execute(
            select(SentNotifications)
            .filter_by(slack_team_id=team_id, fingerprint=fingerprint)
            .order_by(SentNotifications.created_at.desc())
            .limit(1)
        )
        notification = result.scalars().first()

        if not notification or not notification.slack_metadata:
            return None

        try:
            slack_metadata = json.loads(notification.slack_metadata)
        except json.JSONDecodeError:
            LOG.warning(f"Failed to parse slack_metadata for fingerprint {fingerprint}")
            return None

        channel_id = slack_metadata.get("channel")
        if channels:
            normalized_channel = normalize_channel(channels)
            if normalized_channel and channel_id == normalized_channel.get("id"):
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


class TeamsSender(BaseSender):
    def __init__(self, teams_app, cache: Cache, engine):
        super().__init__(cache=cache, teams_app=teams_app, engine=engine)

    async def acquire_teams_access_token(self, session, ms_teams_installation):
        async def _refresh(sess, ip):
            response = self.teams_app.acquire_token_by_refresh_token(
                ip.refresh_token,
                scopes=settings.ms_teams.scopes,
            )
            if "error" in response and response["error"]:
                if "AADSTS700082" in response.get("error_description", ""):
                    LOG.warning(f"Refresh token expired for installation id {ms_teams_installation.id}")
                    ms_teams_installation.token_expires_at = None
                    sess.add(ms_teams_installation)
                    await sess.commit()
                    return None
                LOG.exception(
                    f"Unable to get access token for teams due to {response.get('error_description')} for "
                    f"installation id {ms_teams_installation.id}"
                )
                return None

            ip.token = response.get("access_token")
            ip.refresh_token = response.get("refresh_token")
            expires_in = response.get("expires_in")
            ip.token_expires_at = utc_now() + timedelta(seconds=expires_in - 60) if expires_in else None
            return ip.token

        return await self._refresh_token_if_expired(
            session, ms_teams_installation, PlatformTypes.MS_TEAMS.value, _refresh
        )


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


# ----------------------------- Grouped Message Handler -----------------------------


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
                return {"status": "failed", "error": str(e)}

            LOG.info("Flushing %d messages for %s", len(raw_list), key)

            try:
                _, tenant_id, orig_type = key.split(":", 2)
                batch = [json.loads(item) for item in raw_list]

                async with BaseDB.async_session(self.message_service.engine)() as session:
                    first_msg = batch[0]
                    matched_rules, source = await self.message_service.rule_matcher.match_notification_rules(
                        session, tenant_id, orig_type, first_msg
                    )

                    if matched_rules and (
                        is_suppressed(matched_rules) or is_snoozed(matched_rules) or is_batch_delivery(matched_rules)
                    ):
                        LOG.debug("Skipping flush for %s due to suppression or batch rule", key)
                        return {"status": "ok"}

                    installed_platforms = await self.message_service.get_installed_platforms_from_cache_or_db(
                        session, tenant_id
                    )
                    if not installed_platforms:
                        LOG.warning("No messaging platform installation for tenant %s", tenant_id)
                        return {"status": "ok"}

                    self.message_service.get_channels_from_request_or_defaults(
                        installed_platforms, matched_rules, source, first_msg
                    )
                    params = {"events": [p["parameters"] for p in batch]}

                    platform_responses = await self.message_service.send_template_notification(
                        session,
                        tenant_id,
                        {"parameters": params},
                        f"grouped_{orig_type}",
                        installed_platforms,
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
                return {"status": "failed", "error": str(e)}


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
        self.teams_sender = TeamsSender(teams_app, cache, engine)
        self.gchat_sender = GoogleChatSender(cache)

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
                return await self.send_threaded_reply(tenant_id, thread, parameters)

            group_result = await self._maybe_handle_grouped_message(source, parameters, tenant_id, payload)
            if group_result is not None:
                return group_result

            async with BaseDB.async_session(self.engine)() as session:
                matched_rules, source = await self.rule_matcher.match_notification_rules(
                    session, tenant_id, type_, payload
                )

                rule_result = self._maybe_return_rule_effect(matched_rules, tenant_id)
                if rule_result is not None:
                    return rule_result

                installed_platforms = await self.get_installed_platforms_from_cache_or_db(session, tenant_id)
                if not installed_platforms:
                    return [failed_response("", reason="No messaging platform installation found for this tenant")]

                self.get_channels_from_request_or_defaults(installed_platforms, matched_rules, source, payload)

                fingerprint = get_fingerprint(type_, payload)
                platform_responses = await self.trigger_notification(
                    session,
                    fingerprint,
                    installed_platforms,
                    payload,
                    parameters,
                    [],
                    tenant_id,
                    type_,
                )

                return platform_responses

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

        finding = parameters.get("finding") or {}
        finding_source = finding.get("source", "")
        if finding_source and finding_source.lower() == "anomaly":
            anomaly = {
                "source": "anomaly",
                "type": "anomaly",
                "tenant_id": tenant_id,
                "parameters": finding,
            }
            return await self.group_handler.enqueue(anomaly)

        return None

    @staticmethod
    def _maybe_return_rule_effect(matched_rules, tenant_id):
        if not matched_rules:
            return None

        if is_suppressed(matched_rules):
            return [system_response("suppressed", "Notification suppressed by notification rule configuration")]

        if is_snoozed(matched_rules):
            snooze_rule = next((r for r in matched_rules if r.get("expires_at")), None)
            expires_at = snooze_rule.get("expires_at") if snooze_rule else None
            reason = f"Notification snoozed until {expires_at}" if expires_at else "Notification snoozed"
            return [system_response("snoozed", reason)]

        if is_batch_delivery(matched_rules):
            return [system_response("queued", "Notification queued for batch delivery")]

        return None

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
            new_sent_notification, platform_responses = await self.send_finding_notification(
                session, tenant_id, parameters, fingerprint, installed_platforms
            )
            if new_sent_notification:
                session.add(new_sent_notification)
                await session.commit()
        elif type_ in template_mapping:
            platform_responses = await self.send_template_notification(
                session, tenant_id, kwargs, type_, installed_platforms
            )
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
        return result.scalars().all()

    @staticmethod
    def get_channels_from_request_or_defaults(installed_platforms, matched_rules, source, kwargs):
        all_channels = kwargs.get("channels", {})

        for ip in installed_platforms:
            platform = ip.platform
            to_channel = all_channels.get(platform) if all_channels else None

            if all_channels and to_channel is None:
                ip.to_channel = None
                continue

            if to_channel is not None:
                ip.to_channel = MessageService.get_payload_channel(ip, matched_rules, platform, to_channel)
            else:
                rule_channel = next(
                    (
                        rule["channels"]
                        for rule in matched_rules
                        if rule["platform"] == platform and rule.get("channels")
                    ),
                    None,
                )
                if rule_channel is not None:
                    ip.to_channel = rule_channel
                else:
                    ip.to_channel = ip.channels

    @staticmethod
    def get_payload_channel(ip, matched_rules, platform, to_channel):
        if to_channel:
            return to_channel

        to_channel = next(
            (rule["channels"] for rule in matched_rules if rule["platform"] == platform and rule.get("channels")),
            ip.channels,
        )

        return to_channel

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

        new_sent_notification = None
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
                new_sent_notification = sent_notification
            if response_data:
                platform_responses.append(response_data)

        return new_sent_notification, platform_responses

    def _get_finding_sender(self, platform):
        return {
            PlatformTypes.SLACK.value: self._send_finding_to_slack,
            PlatformTypes.MS_TEAMS.value: self._send_finding_to_teams,
            PlatformTypes.GOOGLE_CHAT.value: self._send_finding_to_gchat,
        }.get(platform)

    async def _send_finding_to_slack(self, session, tenant_id, ip, finding, fingerprint):
        thread_id = await self.slack_sender.check_if_sent_already(session, fingerprint, ip.team_id, ip.channels)
        message, output_blocks, attachments = get_slack_finding_message(self.slack_app, ip, finding)

        slack_response = await asyncio.to_thread(
            self.slack_sender.post_to_slack,
            tenant_id,
            ip,
            thread_id,
            text=message,
            blocks=output_blocks,
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

    async def _send_finding_to_teams(self, session, tenant_id, ip, finding, _):
        token = await self.teams_sender.acquire_teams_access_token(session, ip)
        if not token:
            return None, None

        teams_channels = self._normalize_teams_channels(ip)

        response = await asyncio.to_thread(
            MsTeamsClient.post_to_ms_teams,
            token,
            teams_channels,
            get_ms_teams_finding_message_template(finding),
            tenant_id,
        )
        if not (response and response.status_code == 201):
            return None, None

        channels_list = teams_channels.get("channels", [])
        channel_id = channels_list[0].get("id") if channels_list else None
        message_data = response.json() if hasattr(response, "json") else {}

        response_data = success_response(
            "ms_teams",
            team_id=teams_channels.get("team_id"),
            channel_id=channel_id,
            message_ts=message_data.get("id"),
        )

        return response_data, None

    async def _send_finding_to_gchat(self, session, tenant_id, ip, finding, _):
        token = await self.gchat_sender.acquire_google_chat_access_token(session, ip)
        if not token:
            return None, None

        result = await asyncio.to_thread(
            GoogleChatClient.post_to_google_chat,
            token,
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

                template_func = template_mapping[notification_type].get(ip.platform)
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
                return await self.send_slack_template_notification(ip, template_func, param_value, tenant)
            case PlatformTypes.MS_TEAMS.value:
                return await self.send_teams_template_notification(ip, template_func, param_value, session, tenant)
            case PlatformTypes.GOOGLE_CHAT.value:
                return await self.send_google_chat_template_notification(
                    ip, template_func, param_value, session, tenant
                )
            case _:
                LOG.warning("Unable to identify platform %s with installation id %s", ip.platform, ip.id)
                return None

    async def send_slack_template_notification(self, ip, template_func, param_value, tenant):
        response = await asyncio.to_thread(
            self.slack_sender.post_to_slack, tenant, ip, None, **template_func(param_value)
        )
        if response and response.status_code == 200 and not response.data.get("error"):
            return success_response(
                "slack",
                team_id=ip.team_id,
                channel_id=response.data.get("channel"),
                message_ts=response.data.get("ts"),
            )
        return failed_response("slack", team_id=ip.team_id)

    async def send_teams_template_notification(self, ip, template_func, param_value, session, tenant):
        token = await self.teams_sender.acquire_teams_access_token(session, ip)
        if not token:
            return failed_response("ms_teams", reason="Unable to authenticate teams installation")

        teams_channels = self._normalize_teams_channels(ip)
        response = await asyncio.to_thread(
            MsTeamsClient.post_to_ms_teams, token, teams_channels, template_func(param_value), tenant
        )

        if response and response.status_code == 201:
            channels_list = teams_channels.get("channels", [])
            channel_id = channels_list[0].get("id") if channels_list else None
            message_data = response.json() if hasattr(response, "json") else {}

            return success_response(
                "ms_teams",
                team_id=teams_channels.get("team_id"),
                channel_id=channel_id,
                message_ts=message_data.get("id"),
            )

        reason = response.reason if response else "Unable to send teams notification"
        return failed_response("ms_teams", reason=reason)

    async def send_google_chat_template_notification(self, ip, template_func, param_value, session, tenant_id):
        token = await self.gchat_sender.acquire_google_chat_access_token(session, ip)
        if token:
            result = await asyncio.to_thread(
                GoogleChatClient.post_to_google_chat,
                token,
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

        return failed_response("google_chat", reason="No valid installation or token has expired")

    # ----------------------------- Token + cache helpers -----------------------------
    async def commit_session_and_clear_cache(self, ip, session):
        session.add(ip)
        await session.commit()
        if self.cache and self.cache.redis_client:
            self.cache.delete_cached_installations(ip.tenant_id)

    @staticmethod
    async def commit_session_and_clear_cache_static(ip, session):
        session.add(ip)
        await session.commit()
        if cache and cache.redis_client:
            cache.delete_cached_installations(ip.tenant_id)

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

        try:
            async with BaseDB.async_session(self.engine)() as session:
                result = await session.execute(
                    select(MessagingPlatform).filter_by(tenant_id=tenant_id, platform=platform)
                )
                ip = result.scalars().first()
                if not ip:
                    return [failed_response(platform, reason="No installation found")]

                generic_params = get_generic_message_params(message=message_text)

                template_factories = {
                    PlatformTypes.SLACK.value: get_slack_generic_message_template,
                    PlatformTypes.MS_TEAMS.value: get_ms_teams_generic_message_template,
                    PlatformTypes.GOOGLE_CHAT.value: get_google_chat_generic_message_template,
                }

                if platform not in template_factories:
                    return [failed_response(platform, reason="Unsupported platform")]

                template = template_factories[platform](generic_params)

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
        token = await self.teams_sender.acquire_teams_access_token(session, ip)
        if not token:
            return [failed_response("ms_teams", reason="Token acquisition failed")]

        # Template is the adaptive card dict
        response = await asyncio.to_thread(
            MsTeamsClient.post_reply_to_thread, token, team_id, channel_id, parent_message_id, template
        )

        if response and getattr(response, "status_code", None) == 201:
            message_data = response.json() if hasattr(response, "json") else {}
            return [
                success_response(
                    "ms_teams",
                    team_id=team_id,
                    channel_id=channel_id,
                    message_ts=message_data.get("id"),
                    parent_message_id=parent_message_id,
                )
            ]

        return [failed_response("ms_teams", reason="API call failed")]

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
        token = await self.gchat_sender.acquire_google_chat_access_token(session, ip)
        if not token:
            return [failed_response("google_chat", reason="Token acquisition failed")]

        # Convert message name to thread name for replying
        thread_name = self._convert_gchat_message_to_thread_name(message_ts, space_id)

        # Template is plain text string
        result = await asyncio.to_thread(
            GoogleChatClient.post_to_google_chat,
            token,
            {"id": space_id},
            template,
            ip.tenant_id,
            thread_name=thread_name,
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
