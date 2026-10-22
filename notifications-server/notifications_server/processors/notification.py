import datetime
import json
import logging

from notifications_server.configs.settings import settings
from notifications_server.schemas import SendMessageRequest
from notifications_server.services.message import MessageService, NotificationRuleMatcher, resolve_outcome
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import ReactionTypes
from notifications_server.processors.factory import ProcessorFactory
from notifications_server.services.rules import NotificationRulesService

LOG = logging.getLogger(__name__)


class NotificationSendFailed(RuntimeError):
    """A notification send failed on at least one platform. Carries which
    platforms failed so a retry can be scoped to just those — platforms that
    already succeeded must not be delivered to again."""

    def __init__(self, message, failed_platforms):
        super().__init__(message)
        self.failed_platforms = failed_platforms


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

    async def _get_configured_email_rules(self, tenant_id: str, kwargs: dict):
        """Email recipients and exclusions from the rules that receive this task,
        and whether the task's own recipients still apply; None when a rule stops
        the task (suppress or an active snooze)."""
        source = kwargs.get("source", "")
        if not source:
            return [], [], True

        try:
            async with BaseDB.async_session(self.engine)() as session:
                matched_rules, _ = await self._rule_matcher.match_notification_rules(session, tenant_id, source, kwargs)

            default_policy = await self._rules_service.get_default_policy(tenant_id, source)
            outcome = resolve_outcome(matched_rules, source=source, honour_batch=False, default_policy=default_policy)
            if outcome.response is not None:
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

    async def send_notification(self, task):
        parsed: SendMessageRequest = self._parse_and_validate_task(task)

        responses = await self._message_service.send_message(parsed)
        failures = [r for r in (responses or []) if isinstance(r, dict) and r.get("status") == "failed"]
        if failures:
            # send_message already catches and logs platform-level exceptions, returning
            # a "failed" PlatformResponse instead of raising — so without this, a send
            # that errors out looks identical to one that succeeded and is never retried.
            failed_platforms = sorted({r.get("platform") for r in failures if r.get("platform")})
            raise NotificationSendFailed(f"Notification send failed: {failures}", failed_platforms)
        LOG.debug("Notification processed successfully")

    async def perform_email(self, email_task):
        LOG.debug("processing new email task %s", email_task)
        processor_email = ProcessorFactory.get(ReactionTypes.EMAIL)
        email_rules = await self._get_configured_email_rules(email_task.get("tenant_id"), email_task)
        if email_rules is None:
            LOG.info("Email task %s stopped by a notification rule", email_task.get("template_type"))
            return
        configured_emails, exclusion_emails, use_defaults = email_rules
        email_list = (email_task.get("email") or []) if use_defaults else []
        email_list.extend(configured_emails)
        exclusion_set = {e.strip().lower() for e in exclusion_emails}
        final_email_list = list({e.strip().lower() for e in email_list} - exclusion_set)
        await processor_email.process_email_task(final_email_list, email_task)
        LOG.info("task %s processed successfully", email_task.get("template_type"))

    @staticmethod
    def _normalize_email_task(task: dict) -> None:
        """Map unified envelope fields to legacy names that EmailProcessor expects."""
        if "template_type" not in task and "type" in task:
            task["template_type"] = task["type"]
        if "template_params" not in task and "parameters" in task:
            task["template_params"] = task["parameters"]

    async def process_task(self, headers, task):
        try:
            start_time = datetime.datetime.now()
            task = json.loads(task.decode("utf-8") if isinstance(task, bytes) else task)
            await self._route_task(task, headers)
            time_taken = datetime.datetime.now() - start_time
            if time_taken.total_seconds() > settings.notifications.slow_task_threshold_seconds:
                task.pop("parameters", None)
                LOG.warning("Task took too long to process: %s, payload: %s", time_taken, task)

        except json.JSONDecodeError as e:
            LOG.exception("Failed to decode JSON: %s from: %s", e, headers)
        except Exception as e:
            finding = (task.get("parameters") or {}).get("finding")
            if finding:
                finding.pop("description", None)
                finding.pop("evidences", None)
            LOG.exception("Failed to process task: %s from: %s", e, headers)
            LOG.debug("Payload: %s", json.dumps(task))
            failed_platforms = getattr(e, "failed_platforms", None)
            if failed_platforms:
                task["retry_platforms"] = failed_platforms
            if self._death_count(headers) >= settings.notifications.task_retry_count:
                LOG.error(
                    "Task exhausted %d retries, moving to DLQ from: %s",
                    settings.notifications.task_retry_count,
                    headers,
                )
                await self.task_producer.publish_dlq_message(task)
            else:
                await self.task_producer.publish_delayed_message(task)

    @staticmethod
    def _death_count(headers) -> int:
        # RabbitMQ appends/increments an "x-death" header entry each time a message
        # is dead-lettered back onto this queue via the delayed-retry path.
        return sum(entry.get("count", 0) for entry in (headers or {}).get("x-death") or [])

    async def _route_task(self, task, headers):
        kind = task.get("kind")
        if kind == "email":
            self._normalize_email_task(task)
            await self.perform_email(task)
        elif kind == "notification":
            await self.send_notification(task)
        elif "type" in task or ("tenant_id" in task and "parameters" in task):
            await self.send_notification(task)  # Legacy fallback
        elif "template_type" in task:
            await self.perform_email(task)  # Legacy fallback
        else:
            LOG.warning("Unknown task found with from: %s, json: %s", headers, json.dumps(task))
