import copy
import json
import logging
import re
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from functools import partial
from typing import Optional

import requests
from botbuilder.schema import Activity, ActivityTypes

from notifications_server.configs.settings import ACCOUNT_SECURITY_CONTEXT, URLRoutes, settings
from notifications_server.message_templates.slack.ai_cost_daily_report import (
    TopModelRow,
    TopSourceRow,
    _top_models_reply_blocks,
    _top_sources_reply_blocks,
)
from notifications_server.message_templates.slack.finding import (
    FINDING_CALLBACK_ID,
    SUPPRESS_ACTION_NAME,
)
from notifications_server.models.db_base import BaseDB
from notifications_server.services.actions import (
    Actions,
    validate_and_get_user_id,
    SkipAutoOptimizeParams,
    ApprovalParams,
)
from notifications_server.services.channel_ingest import ChannelIngestService
from notifications_server.services.common import CommonService
from notifications_server.services.events import Events, event_cache
from notifications_server.services.messaging_installations import load_installation, load_installation_by_team
from notifications_server.services.bot_messages import get_bot_joined_message
from notifications_server.utils.action_requests import ActionRequestBody, verify_action_request
from notifications_server.utils.transformer import SLACK_SIGNIN_SECRET, Transformer

USER_NOT_FOUND_MESSAGE = "Hmm, I couldn't identify your account. Mind checking your setup?"
UNABLE_TO_PROCESS_REQUEST = "Oops! I ran into a snag with that. Could you try again?"
SUPPRESS_NOT_ALLOWED_MESSAGE = (
    "Looks like you don't have permission to suppress alerts for this account — ask an admin."
)

# Mirrors the gateway permissions of event_create_triage_rule in actions.yaml;
# the internal X-ACTION-TOKEN path bypasses the gateway, so the role gate lives here.
SUPPRESS_ALLOWED_ROLES = {"tenant_admin", "account_admin"}
SUPPRESS_DURATION_LABELS = {1: "1h", 4: "4h", 24: "24h", 168: "7d"}
LOG = logging.getLogger(__name__)


class SlackActionsBaseService:
    def __init__(self, engine, slack_app, teams_app):
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.engine = engine
        self._scoped_session = BaseDB.session(self.engine)
        self.session = self._scoped_session()
        self.common_service = CommonService(engine, slack_app, teams_app)
        self.action_service = Actions(self.common_service, slack_app, teams_app, self.session)
        self.event_service = Events(self.common_service, slack_app, teams_app, self.session)

    def close(self):
        """Close and remove sessions, returning connections to the pool."""
        try:
            self._scoped_session.remove()
        except Exception:
            pass
        self.common_service.close()

    def get_user_email(self, slack_user_id, team_id):
        try:
            return None, self.common_service.get_user_info("slack", team_id, slack_user_id)
        except ValueError as e:
            return e.args[0], None

    def reply_with_error(self, channel_id, team_id, ts, message):
        return self.common_service.slack_reply_in_thread(channel_id, team_id, ts, message)


class SlackInteractiveActionsService(SlackActionsBaseService):
    @staticmethod
    def normalize_legacy_payload(data):
        """Legacy attachment buttons and menus (the hybrid finding card) arrive
        as ``interactive_message`` payloads: same top-level channel/team/user
        and the same signed ``value``, but the click context lives in
        ``original_message``/``message_ts`` instead of ``message`` and menu
        picks in ``selected_options`` (plural) instead of ``selected_option``.
        Normalize so the shared routing works unchanged."""
        if data.get("type") == "interactive_message":
            if not data.get("message"):
                message = dict(data.get("original_message") or {})
                if "ts" not in message and data.get("message_ts"):
                    message["ts"] = data["message_ts"]
                data["message"] = message
            actions = data.get("actions") or []
            if actions and actions[0].get("selected_options") and not actions[0].get("selected_option"):
                actions[0]["selected_option"] = actions[0]["selected_options"][0]
        return data

    def execute_action(self, data, background_tasks=None):
        data = self.normalize_legacy_payload(data)
        channel_id = data["channel"]["id"]
        team_id = data["team"]["id"]
        slack_user_id = data["user"]["id"]
        # See SlackEventsService.execute_event for why this is set here.
        self.common_service.app_id = data.get("api_app_id")
        try:
            error_message, user_email = self.get_user_email(slack_user_id, team_id)
            if not user_email:
                message = error_message if error_message else USER_NOT_FOUND_MESSAGE
                return self.reply_with_error(channel_id, team_id, data.get("message", {}).get("ts"), message)

            action_type = data["actions"][0]["type"]
            if action_type in ("static_select", "select"):
                self.perform_select_action(channel_id, team_id, user_email, data)
            else:
                self.perform_click_action(channel_id, team_id, slack_user_id, user_email, data, background_tasks)
            return None
        except Exception as e:
            LOG.exception(f"Error processing interactive action: {e}")
            message = UNABLE_TO_PROCESS_REQUEST
            return self.reply_with_error(channel_id, team_id, data.get("message", {}).get("ts"), message)

    def perform_click_action(self, channel_id, team_id, slack_user_id, user_email, data, background_tasks=None):
        action_data = data["actions"][0]
        # Legacy attachment buttons have no action_id (only name/value).
        action_id = action_data.get("action_id") or ""

        if action_id.startswith("select_cluster_option"):
            self.event_service.update_account_for_event(
                action_id, channel_id, team_id, slack_user_id, data["message"]["thread_ts"]
            )
            return
        elif action_id.startswith("select_followup_option"):
            self.event_service.update_followup_for_event(
                action_data, channel_id, team_id, slack_user_id, data["message"]["thread_ts"]
            )
            return

        action_value = action_data.get("value")
        if not action_value:
            return

        action = json.loads(action_value).get("body", {})
        action_name = action.get("action_name")
        if not action_name:
            return

        action_methods = {
            "ask_ai": self.handle_event_analysis_call,
            "skip_auto_optimize_execution": self.handle_skip_auto_optimize,
            "ai_chat_feedback": self.handle_ai_search_feedback,
            "workflow_approval_action": self.handle_approval_action,
            "ai_cost_show_top_models": self.handle_show_ai_cost_top_models,
            "ai_cost_show_top_sources": self.handle_show_ai_cost_top_sources,
        }

        handler = action_methods.get(action_name)
        if handler:
            action_params = action.get("action_params", {})
            if isinstance(action_params, str):
                action_params = json.loads(action_params)
            if action_name == "ask_ai":
                handler(channel_id, team_id, slack_user_id, user_email, data, action_params, background_tasks)
            else:
                handler(channel_id, team_id, slack_user_id, user_email, data, action_params)

    def handle_event_analysis_call(
        self, channel_id, team_id, slack_user_id, user_email, data, action_params, background_tasks=None
    ):
        try:
            message = data.get("message", {})
            thread_ts = message.get("thread_ts") or message.get("ts")
            message_ts = message.get("ts")

            tenant_id = action_params.get("tenant_id")
            if not tenant_id:
                return self.reply_with_error(channel_id, team_id, thread_ts, "Unable to identify tenant information")

            # Legacy interactive_message payloads (this card's button type)
            # don't carry api_app_id at all — confirmed live, it's always None
            # on a real click — so self.common_service.app_id (set from
            # data.get("api_app_id") in execute_action) can't disambiguate
            # when multiple Slack apps share this team_id; every chat.update
            # below would silently fail against the wrong app's token and
            # leave the card on Slack's own "OK" fallback. Only bother
            # resolving here when api_app_id was actually missing -- a modern
            # payload that already gave us a real app_id doesn't need this.
            # tenant_id is the fix: it's exactly the installation that built
            # this card (finding.py stamps it into the signed button value
            # from the installation it's rendering for), so try that first;
            # fall back to the ambiguous team_id lookup (the same one every
            # other action already relies on) only if tenant_id comes up empty.
            if not self.common_service.app_id:
                installation = load_installation(self.session, tenant_id, "slack")
                if not (installation and installation.app_id):
                    installation = load_installation_by_team(self.session, team_id, "slack")
                if installation:
                    self.common_service.app_id = installation.app_id

            event_id = action_params.get("event_id")
            cluster_id = action_params.get("cluster_id")

            # Pin the card to "Analyzing..." before anything else — no user_id
            # lookup, cache write, or reaction first. Those are all real
            # network calls, and on the deployed network path (unlike a local
            # test) their combined latency can be enough to brush up against
            # Slack's 3s interactive-action ack budget, giving Slack's own
            # legacy fallback a chance to flash the card to a bare "OK" before
            # our own update lands a moment later and corrects it. Keeping
            # this the very first thing minimizes that window.
            #
            # The Ask Nubi button itself is never removed from the card, in
            # any state (Analyzing/Analyzed/failed) -- only the status line
            # changes, and anyone can click it again at any time, including
            # while one analysis is already showing as in progress.
            original_attachments = message.get("attachments")
            base_attachments = original_attachments if _has_finding_attachment(original_attachments) else None
            if base_attachments and message_ts:
                self.common_service.update_slack_message_attachments(
                    channel_id,
                    team_id,
                    message_ts,
                    _with_status_line(base_attachments, _analyzing_status_line()),
                )

            # Everything else — user_id resolution, the cache write, the
            # reaction, and the analysis itself — has no bound on how long it
            # takes, so it all runs after this handler returns (and Slack has
            # already been ack'd) instead of blocking the interactive-action
            # response.
            context = EventAnalysisContext(
                event_id=event_id,
                account_id=cluster_id,
                user_email=user_email,
                tenant_id=tenant_id,
                channel_id=channel_id,
                team_id=team_id,
                thread_ts=thread_ts,
                message_ts=message_ts,
                slack_user_id=slack_user_id,
                original_attachments=original_attachments,
                base_attachments=base_attachments,
                app_id=self.common_service.app_id,
            )
            run_analysis = partial(_run_event_analysis_background, self.engine, self.slack_app, self.teams_app, context)
            if background_tasks is not None:
                background_tasks.add_task(run_analysis)
            else:
                run_analysis()

            return json.dumps({"status": "investigation_started"})

        except Exception as e:
            LOG.exception(f"Error processing event analysis action: {e}")
            fallback_ts = data.get("message", {}).get("ts")
            return self.reply_with_error(channel_id, team_id, fallback_ts, UNABLE_TO_PROCESS_REQUEST)

    def handle_ai_search_feedback(self, channel_id, team_id, slack_user_id, user_email, data, action_params):
        payload = {"useful": action_params.get("useful"), "feedback": action_params.get("feedback")}
        return json.dumps(
            self.event_service.update_llm_chat_feedback(
                payload, channel_id, team_id, data.get("message").get("thread_ts")
            )
        )

    def handle_skip_auto_optimize(self, channel_id, team_id, slack_user_id, user_email, data, action_params):
        params = SkipAutoOptimizeParams(
            user_email=user_email,
            auto_optimize_id=action_params.get("auto_pilot_id"),
            channel_id=channel_id,
            message_ts=data.get("message").get("ts"),
            team_id=team_id,
            action_name="skip_playbook_execution",
        )
        return json.dumps(self.action_service.skip_auto_optimize_execution(params=params))

    def handle_show_ai_cost_top_models(self, channel_id, team_id, slack_user_id, user_email, data, action_params):
        # "Top Models" button on the AI cost digest — rows are the compact
        # arrays ai_cost_daily_report.py._compact_model_rows already put in
        # the button's own value, so this never re-queries llm-server; it
        # just re-renders the same table the digest already computed and
        # posts it as a threaded reply under the digest message.
        message = data.get("message", {})
        thread_ts = message.get("thread_ts") or message.get("ts")
        rows = [
            TopModelRow(
                model=r[0], mtd_cost_usd=r[1], call_count=r[2], p95_cost_per_call_usd=r[3], p99_cost_per_call_usd=r[4]
            )
            for r in action_params.get("rows", [])
        ]
        self.common_service.slack_reply_in_thread_as_blocks(
            channel_id, team_id, thread_ts, blocks=_top_models_reply_blocks(rows)
        )

    def handle_show_ai_cost_top_sources(self, channel_id, team_id, slack_user_id, user_email, data, action_params):
        # Mirrors handle_show_ai_cost_top_models for the source breakdown.
        message = data.get("message", {})
        thread_ts = message.get("thread_ts") or message.get("ts")
        rows = [
            TopSourceRow(
                source=r[0], mtd_cost_usd=r[1], call_count=r[2], p95_cost_per_call_usd=r[3], p99_cost_per_call_usd=r[4]
            )
            for r in action_params.get("rows", [])
        ]
        self.common_service.slack_reply_in_thread_as_blocks(
            channel_id, team_id, thread_ts, blocks=_top_sources_reply_blocks(rows)
        )

    def perform_select_action(self, channel_id, team_id, user_email, data):
        action = data.get("actions", [])[0]
        # Legacy attachment menus have no action_id (only name).
        action_id = action.get("action_id") or action.get("name") or ""
        slack_user_id = data["user"]["id"]

        if action_id == SUPPRESS_ACTION_NAME:
            self.handle_suppress_finding(channel_id, team_id, user_email, action, data)
        elif action_id == "select_followup_option_dropdown":
            self.event_service.update_followup_for_event(
                action, channel_id, team_id, slack_user_id, data["message"]["thread_ts"]
            )
        elif action_id == "select_account_dropdown":
            selected_option = action.get("selected_option", {})
            account_id = selected_option.get("value")
            if account_id:
                # Construct action_id in the same format as buttons for consistency
                self.event_service.update_account_for_event(
                    f"select_cluster_option--{account_id}",
                    channel_id,
                    team_id,
                    slack_user_id,
                    data["message"]["thread_ts"],
                )
        elif action_id == "skip_auto_optimize_action_by_minute":
            self.skip_auto_optimize_action_by_minute(user_email, action_id, action, data, channel_id, team_id)
        elif action_id.startswith("workflow_approval_action_select"):
            self.handle_workflow_approval_selection(channel_id, team_id, user_email, action, data)

    def skip_auto_optimize_action_by_minute(self, user_email, action_id, action, data, channel_id, team_id):
        selected_option = action.get("selected_option", {})
        params = SkipAutoOptimizeParams(
            channel_id=channel_id,
            team_id=team_id,
            action_name=action_id,
            message_ts=data.get("message").get("ts"),
            auto_optimize_id=selected_option.get("value"),
            minutes=selected_option.get("text", {}).get("text", ""),
            user_email=user_email,
        )
        return json.dumps(self.action_service.skip_auto_optimize_execution(params=params))

    def handle_approval_action(self, channel_id, team_id, _slack_user_id, _user_email, data, action_params):
        try:
            message = data.get("message", {})
            message_ts = message.get("ts")
            if not message_ts:
                LOG.error("Missing message timestamp in approval action")
                return self.reply_with_error(channel_id, team_id, None, UNABLE_TO_PROCESS_REQUEST)

            # Token is stored in message metadata to avoid Slack's 256 char button value limit
            token = message.get("metadata", {}).get("event_payload", {}).get("token")
            status = action_params.get("status")
            if not token or not status:
                LOG.error(f"Missing token or status in approval action: token={token}, status={status}")
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)
            params = ApprovalParams(
                channel_id=channel_id,
                team_id=team_id,
                action_name="approval_action",
                message_ts=message_ts,
                token=token,
                status=status,
            )
            self.action_service.handle_approval_action(params=params)
            return json.dumps({"status": "success"})
        except Exception as e:
            LOG.exception(f"Error in handle_approval_action: {e}")
            message_ts = data.get("message", {}).get("ts")
            return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

    def handle_workflow_approval_selection(self, channel_id, team_id, _, action, data):
        try:
            selected_option = action.get("selected_option", {})
            status = selected_option.get("value", "")
            message = data.get("message", {})
            message_ts = message.get("ts")

            if not status:
                LOG.warning("Empty selection in workflow approval dropdown")
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

            # Token is stored in message metadata to avoid Slack's 256 char value limit
            token = message.get("metadata", {}).get("event_payload", {}).get("token")

            if not token:
                LOG.warning("Missing token in message metadata for approval dropdown")
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

            params = ApprovalParams(
                channel_id=channel_id,
                team_id=team_id,
                action_name="approval_action",
                message_ts=message_ts,
                token=token,
                status=status,
            )
            self.action_service.handle_approval_action(params=params)
            return json.dumps({"status": "success"})
        except Exception as e:
            LOG.exception(f"Error in handle_workflow_approval_selection: {e}")
            message_ts = data.get("message", {}).get("ts")
            return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

    def handle_suppress_finding(self, channel_id, team_id, user_email, action, data):
        message = data.get("message", {})
        message_ts = message.get("ts")
        try:
            selected_value = (action.get("selected_option") or {}).get("value")
            if not selected_value:
                return None

            # The value round-trips through Slack; verify our HMAC before trusting
            # any of the action_params (account/tenant/scope) it carries.
            payload = json.loads(selected_value)
            body = ActionRequestBody(**(payload.get("body") or {}))
            if not verify_action_request(body, payload.get("signature", ""), SLACK_SIGNIN_SECRET):
                LOG.warning("Rejecting suppress action with invalid signature")
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

            params = body.action_params or {}
            if isinstance(params, str):
                params = json.loads(params)

            tenant_id = params.get("tenant_id")
            account_id = params.get("account_id")
            fingerprint = params.get("fingerprint")
            alertname = params.get("alertname")
            scope = params.get("scope")
            duration_hours = int(params.get("duration_hours") or 0)
            if not (tenant_id and account_id and scope and duration_hours > 0):
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

            user_id = validate_and_get_user_id(user_email)
            if not user_id:
                return self.reply_with_error(channel_id, team_id, message_ts, USER_NOT_FOUND_MESSAGE)
            if not self._can_manage_triage_rules(user_id, tenant_id, account_id):
                return self.reply_with_error(channel_id, team_id, message_ts, SUPPRESS_NOT_ALLOWED_MESSAGE)

            until = datetime.now(timezone.utc) + timedelta(hours=duration_hours)
            rule_input = {
                "cloud_account_id": account_id,
                "rule_type": "suppression",
                "action": "suppress",
                "effective_until": until.strftime("%Y-%m-%dT%H:%M:%SZ"),
                "apply_to_existing": True,
                "name": f"Suppressed via Slack by {user_email}",
            }
            if scope == "alertname" and alertname:
                # match_alertname is evaluated as a regex against aggregation_key.
                rule_input["match_alertname"] = f"^{re.escape(alertname)}$"
            elif fingerprint:
                rule_input["match_fingerprint"] = fingerprint
            else:
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

            response = requests.post(
                settings.services.api_server + "/rpc/triage",
                json={
                    "action": {"name": "event_create_triage_rule"},
                    "input": rule_input,
                    "session_variables": {"tenant_id": tenant_id, "user_id": user_id},
                },
                headers={"X-ACTION-TOKEN": settings.action_api_server_token},
                timeout=15,
            )
            response.raise_for_status()
            if not (response.json() or {}).get("success"):
                return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

            slack_user_id = data["user"]["id"]
            scope_label = "this alert" if scope == "fingerprint" else f'all "{alertname}" alerts'
            duration_label = SUPPRESS_DURATION_LABELS.get(duration_hours, f"{duration_hours}h")
            until_ts = int(until.timestamp())
            until_fallback = until.strftime("%d %b %Y %I:%M %p UTC")
            until_text = f"<!date^{until_ts}^{{date_short_pretty}} {{time}}|{until_fallback}>"

            status_line = f"Suppressed ({scope_label} · {duration_label}) by <@{slack_user_id}> · until {until_text}"
            self._mark_finding_suppressed(channel_id, team_id, message, status_line)

            # The rule lives in Triage Rules, so send people there to undo it —
            # not to the event the alert came from.
            triage_rules_url = settings.urls.cluster_details_url(
                account_id,
                utm_source=URLRoutes.UTMSource.SLACK,
                anchor=URLRoutes.Anchors.EVENTS_TRIAGE_RULES,
            )
            confirmation = (
                f"<@{slack_user_id}> suppressed {scope_label} for {duration_label} — repeat alerts won't"
                f" notify until {until_text}. <{triage_rules_url}|Open Triage Rules> to undo."
            )
            self.common_service.slack_reply_in_thread(channel_id, team_id, message_ts, confirmation)
            return json.dumps({"status": "suppressed"})
        except Exception as e:
            LOG.exception(f"Error processing suppress action: {e}")
            return self.reply_with_error(channel_id, team_id, message_ts, UNABLE_TO_PROCESS_REQUEST)

    @staticmethod
    def _can_manage_triage_rules(user_id, tenant_id, account_id):
        try:
            response = requests.post(
                settings.services.api_server + ACCOUNT_SECURITY_CONTEXT,
                json={"user_id": user_id, "tenant_id": tenant_id},
                headers={"X-ACTION-TOKEN": settings.action_api_server_token},
                timeout=10,
            )
            response.raise_for_status()
            context = (response.json() or {}).get("context") or {}
            if account_id not in (context.get("AccountIds") or []):
                return False
            return bool(set(context.get("Roles") or []) & SUPPRESS_ALLOWED_ROLES)
        except Exception as e:
            LOG.warning("Failed to check suppress permission for user %s: %s", user_id, e)
            return False

    def _mark_finding_suppressed(self, channel_id, team_id, message, status_line):
        message_ts = message.get("ts")
        attachments = message.get("attachments") or []
        updated = False
        for attachment in attachments:
            if attachment.get("callback_id") != FINDING_CALLBACK_ID:
                continue
            attachment["actions"] = [
                a for a in (attachment.get("actions") or []) if a.get("name") != SUPPRESS_ACTION_NAME
            ]
            attachment["text"] = "\n".join(part for part in (attachment.get("text"), status_line) if part)
            updated = True
        if updated and message_ts:
            self.common_service.update_slack_message_attachments(channel_id, team_id, message_ts, attachments)


def _has_finding_attachment(attachments):
    """True if the finding card is actually present in `attachments`
    (defensive; the caller then leaves the message alone rather than
    guessing at its shape when it isn't)."""
    return any(a.get("callback_id") == FINDING_CALLBACK_ID for a in (attachments or []))


def _analyzing_status_line():
    # ":mag:" (shortcode), not the raw "🔍" character: legacy attachment text
    # is written and read back through Slack's mrkdwn text field, which
    # silently rewrites a raw emoji character into its shortcode form on
    # storage — confirmed live, a written "🔍" round-trips as ":mag:". Writing
    # the shortcode ourselves means what we write and what a later fetch
    # returns are byte-identical either way.
    #
    # Also used for the IN_PROGRESS/CREATED llm-server outcome, not just the
    # initial click stamp: there's no polling/follow-up mechanism to catch
    # the eventual COMPLETED result, so a still-running analysis just reads
    # the same as a fresh one -- the button stays live and re-clicking is
    # exactly how the user finds out whether it's actually done yet.
    return ":mag: Analyzing… · see thread"


def _analyzed_status_line():
    return ":white_check_mark: Analyzed · see thread"


_KNOWN_STATUS_LINES = {_analyzing_status_line(), _analyzed_status_line()}


def _with_status_line(attachments, status_line):
    """Deep-copies `attachments` and, in the finding card's text, replaces
    whichever of our known status lines is currently there (if any) with
    `status_line` (or just removes it, if `status_line` is falsy). No
    per-user text: the button is never hidden, so a different user can
    legitimately re-trigger analysis on the same card, and the status line
    reflects the card's current state rather than any one clicker. Every
    other line — including one a concurrent action (e.g. Suppress) added in
    the meantime — is left untouched."""
    updated = copy.deepcopy(attachments)
    for attachment in updated:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        lines = [line for line in (attachment.get("text") or "").split("\n") if line not in _KNOWN_STATUS_LINES]
        if status_line:
            lines.append(status_line)
        attachment["text"] = "\n".join(lines)
    return updated


@dataclass
class EventAnalysisContext:
    """Everything `_run_event_analysis_background` needs beyond the Slack
    app handles, bundled to keep that function's signature manageable."""

    event_id: str
    account_id: str
    user_email: str
    tenant_id: str
    channel_id: str
    team_id: str
    thread_ts: str
    message_ts: Optional[str]
    slack_user_id: str
    original_attachments: Optional[list]
    base_attachments: Optional[list]
    app_id: Optional[str]


def _finalize_card_after_analysis(service, context, outcome):
    """Applies the final card state after analysis finishes, on top of the
    message's CURRENT attachments when reachable rather than the click-time
    snapshot — a background task can finish minutes after the click, and in
    that window someone may have suppressed the finding; blindly re-applying
    the snapshot would silently clobber that edit.

    `outcome` is one of "completed" (stamp Analyzed), "in_progress" (stamp
    Analyzing again — llm-server is genuinely still working asynchronously
    and there's no follow-up mechanism here to catch the eventual result),
    or "failed" (drop the status line entirely). The Ask Nubi button is
    never removed from the card in any of these states — retrying is always
    available, to any user, at any time."""
    if not context.message_ts:
        return
    new_line = {
        "completed": _analyzed_status_line(),
        "in_progress": _analyzing_status_line(),
        "failed": None,
    }[outcome]
    live_attachments = service.common_service.get_message_attachments(
        context.channel_id, context.team_id, context.thread_ts, context.message_ts
    )
    if live_attachments and _has_finding_attachment(live_attachments):
        updated = _with_status_line(live_attachments, new_line)
        service.common_service.update_slack_message_attachments(
            context.channel_id, context.team_id, context.message_ts, updated
        )
        return

    # Couldn't fetch the live message — fall back to the click-time snapshot
    # rather than leaving the card stuck on "Analyzing…" forever.
    if outcome == "failed":
        if context.original_attachments:
            service.common_service.update_slack_message_attachments(
                context.channel_id,
                context.team_id,
                context.message_ts,
                _with_status_line(context.original_attachments, None),
            )
    elif context.base_attachments:
        service.common_service.update_slack_message_attachments(
            context.channel_id,
            context.team_id,
            context.message_ts,
            _with_status_line(context.base_attachments, new_line),
        )


# Keyed by the EventAnalysisResponse/TaskStatuses field names
# (events.AnalysisType* on the Go side). "detailed_response" is handled
# separately below since it also ends the poll and flips the card.
_EVENT_ANALYSIS_PROGRESS_SECTIONS = (
    ("summary", "Here's a quick summary while I keep digging :mag:"),
    ("investigation", "Here's what I'm finding on the likely root cause:"),
)
# Advances the panel only -- no content to post for this one.
_EVENT_ANALYSIS_SILENT_SECTIONS = ("log_analysis",)
_EVENT_ANALYSIS_MAX_POLL_FAILURES = 3

_active_event_analysis_pollers = 0
_event_analysis_pollers_lock = threading.Lock()

# Streaming "Thinking"-style panel (same chat.startStream/appendStream/
# stopStream mechanism as slack_progress.py). Opened with a single
# placeholder task (mirrors slack_progress.py's _INITIAL_TASK_ID): a bare
# plan_update with no task renders as plain "Thinking..." text, not the
# collapsible panel.
_EVENT_ANALYSIS_PLACEHOLDER_TASK_ID = "starting"
_EVENT_ANALYSIS_PLACEHOLDER_TITLE = "Finding the event"
_EVENT_ANALYSIS_TASK_ORDER = ("summary", "investigation", "log_analysis", "detailed_response")
_EVENT_ANALYSIS_TASK_TITLES = {
    _EVENT_ANALYSIS_PLACEHOLDER_TASK_ID: _EVENT_ANALYSIS_PLACEHOLDER_TITLE,
    "summary": "Summarizing the event",
    "investigation": "Finding the root cause",
    "log_analysis": "Digging through the logs",
    "detailed_response": "Writing up the full analysis",
}


def _open_event_analysis_stream(service, token, context):
    """Best-effort: the panel is a visual layer, never a dependency -- a
    failure here means no panel, not a broken poller or missed card flip."""
    if not token:
        return None
    try:
        response = service.common_service.slack_app.client.start_stream(
            token=token,
            channel_id=context.channel_id,
            thread_ts=context.thread_ts,
            recipient_team_id=context.team_id,
            recipient_user_id=context.slack_user_id,
            task_display_mode="plan",
            chunks=[
                {"type": "plan_update", "title": "Investigating"},
                {
                    "type": "task_update",
                    "id": _EVENT_ANALYSIS_PLACEHOLDER_TASK_ID,
                    "title": _EVENT_ANALYSIS_PLACEHOLDER_TITLE,
                    "status": "in_progress",
                },
            ],
        )
        return response["ts"]
    except Exception as e:
        LOG.debug("event analysis poller: failed to open progress panel for event %s: %s", context.event_id, e)
        return None


def _reveal_event_analysis_tasks(service, token, context, stream_ts):
    """Replaces the placeholder with the real 3-stage task list, once we know
    llm-server is still working. A same-tick COMPLETED click never calls
    this -- the placeholder alone gets closed instead."""
    if not stream_ts:
        return
    chunks = [
        {
            "type": "task_update",
            "id": _EVENT_ANALYSIS_PLACEHOLDER_TASK_ID,
            "title": _EVENT_ANALYSIS_PLACEHOLDER_TITLE,
            "status": "complete",
        }
    ]
    for i, task_id in enumerate(_EVENT_ANALYSIS_TASK_ORDER):
        chunks.append(
            {
                "type": "task_update",
                "id": task_id,
                "title": _EVENT_ANALYSIS_TASK_TITLES[task_id],
                "status": "in_progress" if i == 0 else "pending",
            }
        )
    try:
        service.common_service.slack_app.client.append_stream(
            token=token, channel_id=context.channel_id, ts=stream_ts, chunks=chunks
        )
    except Exception as e:
        LOG.debug("event analysis poller: failed to reveal tasks for event %s: %s", context.event_id, e)


def _advance_event_analysis_panel(service, token, context, stream_ts, task_id):
    if not stream_ts:
        return
    chunks = [
        {"type": "task_update", "id": task_id, "title": _EVENT_ANALYSIS_TASK_TITLES[task_id], "status": "complete"}
    ]
    idx = _EVENT_ANALYSIS_TASK_ORDER.index(task_id)
    if idx + 1 < len(_EVENT_ANALYSIS_TASK_ORDER):
        nxt = _EVENT_ANALYSIS_TASK_ORDER[idx + 1]
        chunks.append(
            {"type": "task_update", "id": nxt, "title": _EVENT_ANALYSIS_TASK_TITLES[nxt], "status": "in_progress"}
        )
    try:
        service.common_service.slack_app.client.append_stream(
            token=token, channel_id=context.channel_id, ts=stream_ts, chunks=chunks
        )
    except Exception as e:
        LOG.debug("event analysis poller: failed to update progress panel for event %s: %s", context.event_id, e)


def _close_event_analysis_stream(service, token, context, stream_ts, outcome, task_ids=_EVENT_ANALYSIS_TASK_ORDER):
    """Unconditionally re-sends every given task's final status (task_update
    is an upsert by id, so re-marking an already-complete one is harmless) --
    otherwise the panel could hang on "in_progress"/"pending". Callers
    closing out before the real 3-stage list was ever revealed pass just
    `[_EVENT_ANALYSIS_PLACEHOLDER_TASK_ID]` instead of the default."""
    if not stream_ts:
        return
    final_status = "complete" if outcome == "completed" else "error"
    chunks = [
        {"type": "task_update", "id": task_id, "title": _EVENT_ANALYSIS_TASK_TITLES[task_id], "status": final_status}
        for task_id in task_ids
    ]
    try:
        service.common_service.slack_app.client.stop_stream(
            token=token, channel_id=context.channel_id, ts=stream_ts, chunks=chunks
        )
    except Exception as e:
        LOG.debug("event analysis poller: failed to close progress panel for event %s: %s", context.event_id, e)


def _post_investigation_content(service, context, content, preamble, mention=False):
    """Returns True iff settled (nothing to post, or posted ok); False only on
    a Slack failure, so callers can retry instead of marking it sent."""
    content = (content or "").strip()
    if not content:
        return True
    text = f"{preamble}\n\n{Transformer.markdown_to_slack_markdown(content)}"
    if mention:
        text = f"{text}\n\n<@{context.slack_user_id}>"
    try:
        service.common_service.slack_reply_in_thread(context.channel_id, context.team_id, context.thread_ts, text)
    except Exception as e:
        LOG.debug("Failed to post investigation content: %s", e)
        return False
    return True


def _start_event_analysis_poller(engine, slack_app, teams_app, context, user_id, cache, service, token, stream_ts):
    """Two overlapping clicks on different occurrence cards of the same
    recurring finding must not both post summary/investigation/
    detailed_response, hence the claim check. Returns True iff a poller was
    actually started and now owns the streaming panel from here on -- on a
    False return, the caller must close the panel itself, since nothing else
    will. Mirrors slack_progress.start_progress_poller's concurrency cap."""
    global _active_event_analysis_pollers
    # A claim only ever clears itself after the full event_analysis_max_minutes
    # TTL, so every early-return past this point that leaves no poller
    # actually running must release it explicitly -- otherwise a cap hit or a
    # startup error blocks any retry on this event for that whole window.
    claimed = False
    try:
        if not cache.claim_event_analysis_poller(context.event_id, settings.slack.event_analysis_max_minutes * 60):
            LOG.info("event analysis poller: already running for event %s, skipping", context.event_id)
            return False
        claimed = True
        with _event_analysis_pollers_lock:
            if _active_event_analysis_pollers >= settings.slack.event_analysis_max_pollers:
                LOG.warning("event analysis poller: cap reached, no progressive updates for %s", context.event_id)
                cache.release_event_analysis_poller(context.event_id)
                return False
            _active_event_analysis_pollers += 1
        try:
            _reveal_event_analysis_tasks(service, token, context, stream_ts)
            threading.Thread(
                target=_run_event_analysis_poller,
                args=(engine, slack_app, teams_app, context, user_id, token, stream_ts),
                daemon=True,
                name=f"event-analysis-poll-{context.event_id}",
            ).start()
            LOG.info("event analysis poller: started for event %s", context.event_id)
            return True
        except Exception:
            with _event_analysis_pollers_lock:
                _active_event_analysis_pollers -= 1
            raise
    except Exception as e:
        LOG.warning("event analysis poller: failed to start for event %s: %s", context.event_id, e)
        if claimed:
            cache.release_event_analysis_poller(context.event_id)
        return False


def _run_event_analysis_poller(engine, slack_app, teams_app, context, user_id, token, stream_ts):
    global _active_event_analysis_pollers
    try:
        _poll_event_analysis(engine, slack_app, teams_app, context, user_id, token, stream_ts)
    except Exception as e:
        LOG.warning("event analysis poller: died for event %s: %s", context.event_id, e)
    finally:
        with _event_analysis_pollers_lock:
            _active_event_analysis_pollers -= 1


def _poll_event_analysis(engine, slack_app, teams_app, context, user_id, token, stream_ts):
    """regenerate is always False here, so /v1/analyze/event is idempotent --
    this only ever reports current DB state, never dispatches new work.
    `token`/`stream_ts` come from the panel the click handler already opened
    and revealed -- this poller doesn't open its own.

    Gives up silently (no alarming message) on a polling deadline or repeated
    fetch failures -- those are limits of how long *we* watched, not a
    reported failure from llm-server."""
    try:
        service = SlackActionsBaseService(engine, slack_app, teams_app)
        service.common_service.app_id = context.app_id
        sent = set()
        failures = 0
        deadline = time.monotonic() + settings.slack.event_analysis_max_minutes * 60
        try:
            while time.monotonic() < deadline:
                try:
                    result = Events.call_event_analysis_api(
                        event_id=context.event_id,
                        account_id=context.account_id,
                        user_id=user_id,
                        tenant_id=context.tenant_id,
                        timeout=settings.slack.event_analysis_poll_timeout_seconds,
                    )
                except Exception as e:
                    LOG.debug("event analysis poll failed: %s", e)
                    result = None

                if not isinstance(result, dict):
                    failures += 1
                    if failures >= _EVENT_ANALYSIS_MAX_POLL_FAILURES:
                        LOG.warning(
                            "event analysis poller: giving up after %d consecutive failures for event %s",
                            failures,
                            context.event_id,
                        )
                        _close_event_analysis_stream(service, token, context, stream_ts, outcome="failed")
                        # We stopped watching -- llm-server didn't report a failure.
                        _finalize_card_after_analysis(service, context, outcome="in_progress")
                        return
                    time.sleep(settings.slack.event_analysis_poll_seconds)
                    continue
                failures = 0

                task_statuses = result.get("task_statuses") or {}
                for field, preamble in _EVENT_ANALYSIS_PROGRESS_SECTIONS:
                    if field not in sent and task_statuses.get(field) == "COMPLETED":
                        if _post_investigation_content(service, context, result.get(field), preamble):
                            sent.add(field)
                            LOG.info("event analysis poller: posted %s for event %s", field, context.event_id)
                            _advance_event_analysis_panel(service, token, context, stream_ts, field)

                for field in _EVENT_ANALYSIS_SILENT_SECTIONS:
                    if field not in sent and task_statuses.get(field) == "COMPLETED":
                        sent.add(field)
                        _advance_event_analysis_panel(service, token, context, stream_ts, field)

                if task_statuses.get("detailed_response") == "COMPLETED":
                    content = result.get("detailed_response") or result.get("summary")
                    if _post_investigation_content(
                        service, context, content, "Here's the full write-up:", mention=True
                    ):
                        _close_event_analysis_stream(service, token, context, stream_ts, outcome="completed")
                        _finalize_card_after_analysis(service, context, outcome="completed")
                        LOG.info("event analysis poller: finished for event %s, card marked Analyzed", context.event_id)
                        return
                    # else retry next tick -- nothing was actually posted.

                if (result.get("status") or "").upper() == "FAILED":
                    _close_event_analysis_stream(service, token, context, stream_ts, outcome="failed")
                    _finalize_card_after_analysis(service, context, outcome="failed")
                    service.common_service.slack_reply_in_thread(
                        context.channel_id,
                        context.team_id,
                        context.thread_ts,
                        (
                            f"<@{context.slack_user_id}> Hmm, something went wrong while analyzing this. "
                            "Mind trying again?"
                        ),
                    )
                    LOG.warning("event analysis poller: llm-server reported FAILED for event %s", context.event_id)
                    return

                time.sleep(settings.slack.event_analysis_poll_seconds)

            LOG.warning("event analysis poller: timed out for event %s", context.event_id)
            _close_event_analysis_stream(service, token, context, stream_ts, outcome="failed")
            _finalize_card_after_analysis(service, context, outcome="in_progress")  # same reasoning as above
        except Exception:
            # Belt-and-suspenders: any bug in the loop above that isn't already
            # one of its own handled paths must still not leave the (already
            # revealed, real 3-task) panel visibly stuck on "in_progress".
            _close_event_analysis_stream(service, token, context, stream_ts, outcome="failed")
            raise
        finally:
            service.close()
    finally:
        # Via the module-level singleton, not service.event_service.cache, so
        # this still runs even if constructing `service` itself failed.
        event_cache.release_event_analysis_poller(context.event_id)


def _run_event_analysis_background(engine, slack_app, teams_app, context: EventAnalysisContext):
    """Runs after the interactive-action response has already been sent to
    Slack, so it builds its own DB session/services rather than reusing the
    request-scoped ones (already closed by then). Also does the user_id
    lookup, cache write, and reaction here rather than synchronously before
    the ack — none of them need to complete before Slack sees a response,
    and keeping them off that path minimizes how long the card is exposed to
    Slack's own fallback before our "Analyzing..." update lands.

    Must carry `app_id` forward: when more than one Slack app is installed to
    the same team_id, install resolution is ambiguous without it, and Slack
    rejects a chat.update from any app but the one that posted the message."""
    service = SlackActionsBaseService(engine, slack_app, teams_app)
    service.common_service.app_id = context.app_id
    bot = service.common_service.get_slack_installation(context.team_id)
    token = bot.token if bot else None
    # Opened before the synchronous llm-server kickoff call below, which
    # alone can take the better part of a minute -- opening it only once the
    # background poller started (the old placement) left the click showing
    # plain "Thinking..." text for that whole window instead of the panel.
    stream_ts = _open_event_analysis_stream(service, token, context)
    poller_started = False
    try:
        # The "Analyzing..." stamp is already on the card by the time this
        # runs, so any failure from here on (user_id lookup, cache write, the
        # LLM call, or posting the result back to Slack) must fall through to
        # the same recovery: restore the card and tell the user, rather than
        # leaving it stuck on "Analyzing..." forever with nothing watching it.
        try:
            user_id = validate_and_get_user_id(context.user_email)

            event_entry = {
                "event_id": context.event_id,
                "text": f"Analysis for event with id {context.event_id}",
                "user_id": user_id,
                "account_id": context.account_id,
                "tenant_id": context.tenant_id,
                "slack_user_id": context.slack_user_id,
                "channel_id": context.channel_id,
                "team_id": context.team_id,
                "session_id": f"{context.channel_id}-{context.thread_ts}",
                # Carried so the llm-server async /llm/response webhook (no Slack
                # event of its own to read api_app_id from) still resolves the
                # same app this analysis is running as, instead of falling back
                # to ambiguous team_id-only resolution. See conversation_id-cache
                # app_id propagation in routers/llm_callbacks.py.
                "app_id": context.app_id,
            }
            service.event_service.cache.cache_event_entry(thread_ts=context.thread_ts, event_entry=event_entry)

            try:
                service.common_service.add_slack_reactions(
                    context.channel_id, context.team_id, context.thread_ts, "mag"
                )
            except Exception as e:
                LOG.debug("Failed to add magnifying glass reaction: %s", e)

            result = Events.call_event_analysis_api(
                event_id=context.event_id,
                account_id=context.account_id,
                user_id=user_id,
                tenant_id=context.tenant_id,
            )
            # llm-server's response shape isn't guaranteed on our end -- guard
            # against a malformed (non-dict) `data` payload before any .get().
            if not isinstance(result, dict):
                result = {}
            status = (result.get("status") or "").upper()
            if status == "COMPLETED":
                # A repeat click after an earlier one already finished --
                # summary/investigation were already posted then, so only the
                # write-up goes out now. The panel never got past the
                # placeholder, so that's all that needs closing.
                content = result.get("detailed_response") or result.get("summary")
                _post_investigation_content(service, context, content, "Here's the full write-up:", mention=True)
                _close_event_analysis_stream(
                    service, token, context, stream_ts, "completed", task_ids=[_EVENT_ANALYSIS_PLACEHOLDER_TASK_ID]
                )
                _finalize_card_after_analysis(service, context, outcome="completed")
            elif status in ("IN_PROGRESS", "CREATED"):
                # No separate ack message here -- the panel (already open by
                # this point) already shows "Investigating" with a spinner,
                # so a text message repeating that would just be redundant.
                poller_started = _start_event_analysis_poller(
                    engine,
                    slack_app,
                    teams_app,
                    context,
                    user_id,
                    service.event_service.cache,
                    service,
                    token,
                    stream_ts,
                )
                if not poller_started:
                    # Nothing will ever drive this panel further -- close the
                    # placeholder now instead of leaving it stuck in_progress.
                    _close_event_analysis_stream(
                        service,
                        token,
                        context,
                        stream_ts,
                        "completed",
                        task_ids=[_EVENT_ANALYSIS_PLACEHOLDER_TASK_ID],
                    )
                    _finalize_card_after_analysis(service, context, outcome="in_progress")
                # else: the poller owns finalization now -- stamping here too would race it.
            else:
                service.common_service.slack_reply_in_thread(
                    context.channel_id,
                    context.team_id,
                    context.thread_ts,
                    (
                        f"<@{context.slack_user_id}> Hmm, looks like something went sideways with the "
                        "investigation. Mind if we try that again?"
                    ),
                )
                _close_event_analysis_stream(
                    service, token, context, stream_ts, "failed", task_ids=[_EVENT_ANALYSIS_PLACEHOLDER_TASK_ID]
                )
                _finalize_card_after_analysis(service, context, outcome="failed")
        except Exception as e:
            LOG.exception("Event analysis background task failed: %s", e)
            # Best-effort: this recovery runs with no safety net above it (the
            # outer block is try/finally, not try/except), and both calls
            # below are live Slack requests that can themselves fail (e.g. a
            # transient API error). Letting that propagate would re-leak the
            # exact stuck-on-"Analyzing..."-forever failure this whole
            # function exists to prevent.
            try:
                # Only close the panel here if the poller never took it over --
                # once started, the poller's own thread owns the (revealed,
                # real 3-task) panel from here on, independent of whatever
                # else failed in this function afterward.
                if not poller_started:
                    _close_event_analysis_stream(
                        service, token, context, stream_ts, "failed", task_ids=[_EVENT_ANALYSIS_PLACEHOLDER_TASK_ID]
                    )
                _finalize_card_after_analysis(service, context, outcome="failed")
                service.common_service.slack_reply_in_thread(
                    context.channel_id,
                    context.team_id,
                    context.thread_ts,
                    f"<@{context.slack_user_id}> Hmm, something went wrong while analyzing this. Mind trying again?",
                )
            except Exception:
                LOG.exception("Recovery after analysis failure also failed")
    finally:
        service.close()


class SlackEventsService(SlackActionsBaseService):
    def execute_event(self, data):
        team_id = data["team_id"]
        event_id = data["event_id"]
        event_context = data["event_context"]
        event = data["event"]
        event_type = event.get("type")
        channel_id = event.get("channel")
        # Disambiguates get_slack_installation when multiple apps share a
        # team_id, so replies come from the app that actually received the
        # mention rather than always preferring an Integration-backed install.
        self.common_service.app_id = data.get("api_app_id")

        if event_type == "app_mention":
            self._handle_app_mention(event, team_id, event_id, event_context, channel_id)

        elif event_type == "member_joined_channel":
            self._handle_member_joined_channel(event, team_id, channel_id)

        elif event_type == "message":
            self._handle_channel_message(event, team_id, event_id)

        else:
            LOG.warning(f"[SlackEventsService] Unsupported event type: {event_type}")

    def _handle_channel_message(self, event, team_id, event_id):
        """Passive retention for watched channels. Slack delivers every message in
        every channel the bot is in, so this must stay cheap for the overwhelming
        majority that are dropped — no Slack API calls, no user resolution.

        Retaining a message never triggers Nubi; only an explicit mention does.
        """
        with ChannelIngestService(engine=self.engine) as ingest:
            ingest.handle_message_event(event, team_id, event_id)

    def _handle_app_mention(self, event, team_id, event_id, event_context, channel_id):
        slack_user_id = event.get("user")
        thread_ts = event.get("thread_ts", event.get("ts"))
        event_ts = event.get("event_ts")

        LOG.debug(f"App mention received from user={slack_user_id}, thread={thread_ts}")

        error_message, user_email = self.get_user_email(slack_user_id, team_id)
        if error_message or not user_email:
            message = error_message or "Unable to get user info"
            self.common_service.post_slack_ephemeral_response(channel_id, team_id, slack_user_id, message)
            LOG.warning(f"Failed to resolve user email for user={slack_user_id}: {message}")
            return

        LOG.debug(f"Starting new conversation for user={slack_user_id}, thread={thread_ts}")
        self.event_service.execute_event(
            team_id=team_id,
            event_id=event_id,
            event_context=event_context,
            user_email=user_email,
            channel_id=channel_id,
            thread_ts=thread_ts,
            event_ts=event_ts,
            event=event,
            slack_user_id=slack_user_id,
        )

    def _handle_member_joined_channel(self, event, team_id, channel_id):
        user_joined = event.get("user")
        LOG.debug(f"Member joined channel: user={user_joined}, channel={channel_id}")

        try:
            bot = self.common_service.get_slack_installation(team_id)
            if not bot:
                LOG.warning(f"No Slack installation found for team={team_id}")
                return

            if bot.bot_id == user_joined:
                self.slack_app.client.chat_postMessage(
                    token=bot.token,
                    channel=channel_id,
                    text=get_bot_joined_message(),
                )
                LOG.debug(f"Bot joined channel={channel_id}, welcome message sent")
            else:
                LOG.debug(f"User={user_joined} joined channel={channel_id} (bot_id={bot.bot_id}), skipping welcome")

        except Exception as e:
            LOG.exception(f"Error processing member_joined_channel for channel={channel_id}: {e}")


class MsTeamsEventsService(SlackActionsBaseService):
    async def execute_event(self, data):
        try:
            activity = Activity().deserialize(data)

            activity_type = activity.type

            if activity_type == ActivityTypes.message:
                await self.event_service.handle_message_event(activity)
            elif activity_type == ActivityTypes.conversation_update:
                await self.event_service.handle_conversation_update(activity)
            else:
                LOG.debug(f"Unhandled activity type: {activity_type}")

        except Exception as e:
            LOG.exception(f"Error processing Teams event {e}")
            raise


class GoogleChatEventsService(SlackActionsBaseService):
    async def execute_event(self, data):
        try:
            await self.event_service.handle_google_chat_event(data)
        except Exception as e:
            LOG.exception(f"Error processing Google Chat event: {e}")
            raise
