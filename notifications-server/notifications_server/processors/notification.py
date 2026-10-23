import datetime
import json
import logging
import uuid

from notifications_server.configs.settings import settings
from notifications_server.schemas import SendMessageRequest
from notifications_server.services import delivery_records
from notifications_server.services.delivery_errors import NO_DESTINATION, retry_destination
from notifications_server.services.message import MessageService, NotificationRuleMatcher, resolve_outcome
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import ReactionTypes
from notifications_server.processors.factory import ProcessorFactory
from notifications_server.services.rules import NotificationRulesService

LOG = logging.getLogger(__name__)


class NotificationSendFailed(RuntimeError):
    """A notification send failed on at least one destination with an error
    worth retrying. Carries where, so the retry goes only there: destinations
    that got the message, or failed for good, must not be sent to again.
    `retry_destinations` is None when the retry can't be narrowed to channels."""

    def __init__(self, message, failed_platforms, retry_destinations=None):
        super().__init__(message)
        self.failed_platforms = failed_platforms
        self.retry_destinations = retry_destinations


class NotificationProcessor:
    def __init__(self, task_producer, engine, slack_app, teams_app):
        self.task_producer = task_producer
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.engine = engine
        self._message_service = MessageService(engine=engine, slack_app=slack_app, teams_app=teams_app)
        self._rules_service = NotificationRulesService(self.engine)
        self._rule_matcher = NotificationRuleMatcher(self._rules_service)

    @staticmethod
    def _parse_email_channels(channels) -> tuple[list[str], list[str]]:
        emails = []
        exclusion_emails = []

        if isinstance(channels, dict) and ("emails" in channels or "exclusion_emails" in channels):
            for email in channels.get("emails", []):
                emails.extend(email.split(","))
            for email in channels.get("exclusion_emails", []):
                exclusion_emails.extend(email.split(","))
        elif isinstance(channels, list):
            for email in channels:
                emails.extend(email.split(","))

        return emails, exclusion_emails

    async def _get_configured_email_rules(self, tenant_id: str, kwargs: dict, stop=None):
        """Email recipients and exclusions from the rules that receive this task,
        and whether the task's own recipients still apply; None when a rule stops
        the task (suppress or an active snooze), with why and which rule in stop."""
        source = kwargs.get("source", "")
        if not source:
            return [], [], True

        try:
            async with BaseDB.async_session(self.engine)() as session:
                matched_rules, _ = await self._rule_matcher.match_notification_rules(session, tenant_id, source, kwargs)

            default_policy = await self._rules_service.get_default_policy(tenant_id, source)
            outcome = resolve_outcome(matched_rules, source=source, honour_batch=False, default_policy=default_policy)
            if outcome.response is not None:
                if stop is not None:
                    stop.update(delivery_records.stopped(outcome, matched_rules))
                return None

            all_emails = []
            all_exclusion_emails = []
            for rule in outcome.targets:
                if rule.get("platform") != "email" or not rule.get("channels"):
                    continue
                emails, exclusions = self._parse_email_channels(rule["channels"])
                all_emails.extend(emails)
                all_exclusion_emails.extend(exclusions)

            return all_emails, all_exclusion_emails, outcome.use_defaults
        except Exception as e:
            LOG.exception("Error fetching email rules for tenant %s: %s", tenant_id, e)
            return [], [], True

    @staticmethod
    def _parse_and_validate_task(task: dict) -> SendMessageRequest:
        try:
            return SendMessageRequest(**task)
        except Exception as exc:
            LOG.error("Invalid notification payload: %s", exc)
            raise

    async def send_notification(self, task, attempt=0):
        parsed: SendMessageRequest = self._parse_and_validate_task({**task, "attempt": attempt + 1})

        responses = await self._message_service.send_message(parsed)
        failures = [r for r in (responses or []) if isinstance(r, dict) and r.get("status") == "failed"]
        permanent = [r for r in failures if r.get("retryable") is False]
        if permanent:
            LOG.warning("Notification not delivered, and sending again cannot help: %s", permanent)
        retryable = [r for r in failures if r.get("retryable") is not False]
        if retryable:
            # send_message already catches and logs platform-level exceptions, returning
            # a "failed" PlatformResponse instead of raising — so without this, a send
            # that errors out looks identical to one that succeeded and is never retried.
            # A failure outside any one destination ("system") is retried whole.
            whole = any(r.get("platform") == "system" for r in retryable)
            failed_platforms = [] if whole else sorted({r.get("platform") for r in retryable if r.get("platform")})
            retry_destinations = None
            if not whole and all(r.get("channel_id") for r in retryable):
                retry_destinations = [retry_destination(r) for r in retryable]
            raise NotificationSendFailed(f"Notification send failed: {retryable}", failed_platforms, retry_destinations)
        LOG.debug("Notification processed successfully")

    async def perform_email(self, email_task, attempt=0):
        LOG.debug("processing new email task %s", email_task)
        processor_email = ProcessorFactory.get(ReactionTypes.EMAIL)
        stop = {}
        email_rules = await self._get_configured_email_rules(email_task.get("tenant_id"), email_task, stop)
        if email_rules is None:
            LOG.info("Email task %s stopped by a notification rule", email_task.get("template_type"))
            reason = stop.get("reason") or delivery_records.SUPPRESSED
            await self._record_email(email_task, attempt, {}, reason, stop.get("routed_by"))
            return
        configured_emails, exclusion_emails, use_defaults = email_rules
        email_list = (email_task.get("email") or []) if use_defaults else []
        email_list.extend(configured_emails)
        exclusion_set = {e.strip().lower() for e in exclusion_emails}
        final_email_list = list({e.strip().lower() for e in email_list} - exclusion_set)
        retry_recipients = email_task.get("retry_recipients")
        if retry_recipients:
            # A retry only goes to the recipients the previous attempt failed to reach.
            final_email_list = [e for e in retry_recipients if e not in exclusion_set]
        outcomes = dict.fromkeys(final_email_list)
        try:
            await processor_email.process_email_task(final_email_list, email_task)
        except Exception as exc:
            failures = getattr(exc, "failures", None)
            outcomes = {to: (failures.get(to) if failures is not None else exc) for to in outcomes}
            raise
        finally:
            await self._record_email(email_task, attempt, outcomes, None if outcomes else NO_DESTINATION)
        LOG.info("task %s processed successfully", email_task.get("template_type"))

    async def _record_email(self, email_task, attempt, outcomes, not_sent_reason=None, routed_by=None):
        rows = delivery_records.email_rows(
            email_task.get("tenant_id"),
            email_task.get("template_type"),
            email_task.get("subject"),
            outcomes,
            not_sent_reason=not_sent_reason,
            dispatch_id=email_task.get("dispatch_id"),
            attempt=attempt + 1,
            source=email_task.get("source"),
            routed_by=routed_by,
        )
        await delivery_records.record(getattr(self, "engine", None), rows)

    @staticmethod
    def _normalize_email_task(task: dict) -> None:
        """Map unified envelope fields to legacy names that EmailProcessor expects."""
        if "template_type" not in task and "type" in task:
            task["template_type"] = task["type"]
        if "template_params" not in task and "parameters" in task:
            task["template_params"] = task["parameters"]

    async def process_task(self, headers, task):
        attempt = self._attempt(headers)
        try:
            start_time = datetime.datetime.now()
            task = json.loads(task.decode("utf-8") if isinstance(task, bytes) else task)
            # Every attempt of a send carries its id, so their rows group under one send.
            task.setdefault("dispatch_id", str(uuid.uuid4()))
            await self._route_task(task, headers, attempt)
            time_taken = datetime.datetime.now() - start_time
            if time_taken.total_seconds() > settings.notifications.slow_task_threshold_seconds:
                LOG.warning(
                    "Task took too long to process: %s, kind=%s, type=%s, dispatch_id=%s",
                    time_taken,
                    task.get("kind"),
                    task.get("type") or task.get("template_type"),
                    task.get("dispatch_id"),
                )

        except json.JSONDecodeError as e:
            LOG.exception("Failed to decode JSON: %s from: %s", e, headers)
        except Exception as e:
            finding = (task.get("parameters") or {}).get("finding")
            trimmed = task
            if finding:
                finding = {k: v for k, v in finding.items() if k not in ("description", "evidences")}
                trimmed = {**task, "parameters": {**task["parameters"], "finding": finding}}
            LOG.exception("Failed to process task: %s from: %s", e, headers)
            LOG.debug("Payload: %s", json.dumps(trimmed))
            # retry_platforms is kept for pods that predate retry_destinations.
            failed_platforms = getattr(e, "failed_platforms", None)
            if failed_platforms:
                task["retry_platforms"] = failed_platforms
            retry_destinations = getattr(e, "retry_destinations", None)
            if retry_destinations:
                task["retry_destinations"] = retry_destinations
            failed_recipients = getattr(e, "failures", None)
            if failed_recipients:
                task["retry_recipients"] = sorted(failed_recipients)
            out = {k: v for k, v in (headers or {}).items() if k in ("traceparent", "tracestate")}
            if attempt >= settings.notifications.task_retry_count:
                LOG.error(
                    "Task exhausted %d retries, moving to DLQ from: %s",
                    settings.notifications.task_retry_count,
                    headers,
                )
                await self.task_producer.publish_dlq_message(task, headers={**out, "nb-retry-count": attempt})
            else:
                await self.task_producer.publish_delayed_message(task, headers={**out, "nb-retry-count": attempt + 1})

    @staticmethod
    def _attempt(headers) -> int:
        """How many times the message was retried before, from its retry header."""
        try:
            return int((headers or {}).get("nb-retry-count") or 0)
        except (ValueError, TypeError):
            return 0

    async def _route_task(self, task, headers, attempt=0):
        kind = task.get("kind")
        if kind == "email":
            self._normalize_email_task(task)
            await self.perform_email(task, attempt)
        elif kind == "notification":
            await self.send_notification(task, attempt)
        elif "type" in task or ("tenant_id" in task and "parameters" in task):
            await self.send_notification(task, attempt)  # Legacy fallback
        elif "template_type" in task:
            await self.perform_email(task, attempt)  # Legacy fallback
        else:
            LOG.warning("Unknown task found with from: %s, keys: %s", headers, sorted(task))
