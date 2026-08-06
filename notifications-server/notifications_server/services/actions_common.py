import copy
import json
import logging
import re
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from functools import partial
from typing import Optional

import requests
from botbuilder.schema import Activity, ActivityTypes

from notifications_server.configs.settings import ACCOUNT_SECURITY_CONTEXT, URLRoutes, settings
from notifications_server.message_templates.slack.finding import (
    ASK_NUBI_ACTION_NAME,
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
from notifications_server.services.events import Events
from notifications_server.services.bot_messages import get_bot_joined_message
from notifications_server.utils.action_requests import ActionRequestBody, verify_action_request
from notifications_server.utils.transformer import SLACK_SIGNIN_SECRET

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

            user_id = validate_and_get_user_id(user_email)
            event_id = action_params.get("event_id")
            cluster_id = action_params.get("cluster_id")

            event_entry = {
                "event_id": event_id,
                "text": f"Analysis for event with id {event_id}",
                "user_id": user_id,
                "account_id": cluster_id,
                "tenant_id": tenant_id,
                "slack_user_id": slack_user_id,
                "channel_id": channel_id,
                "team_id": team_id,
                "session_id": f"{channel_id}-{thread_ts}",
            }
            self.event_service.cache.cache_event_entry(thread_ts=thread_ts, event_entry=event_entry)

            try:
                self.common_service.add_slack_reactions(channel_id, team_id, thread_ts, "mag")
            except Exception as e:
                LOG.debug("Failed to add magnifying glass reaction: %s", e)

            # Keep the card itself intact (mirrors how Silence rewrites its own
            # card instead of leaving Slack to decide). Without this, the LLM
            # call below runs well past Slack's 3s interactive-action ack
            # budget and Slack replaces the whole card with a bare "OK".
            original_attachments = message.get("attachments")
            base_attachments = _strip_finding_action(original_attachments, ASK_NUBI_ACTION_NAME)
            if base_attachments and message_ts:
                self.common_service.update_slack_message_attachments(
                    channel_id,
                    team_id,
                    message_ts,
                    _attachments_with_status(base_attachments, _analyzing_status_line(slack_user_id)),
                )

            # The actual analysis has no bound on how long the LLM takes, so it
            # runs after this handler returns (and Slack has already been ack'd)
            # instead of blocking the interactive-action response.
            context = EventAnalysisContext(
                event_id=event_id,
                account_id=cluster_id,
                user_id=user_id,
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


def _strip_finding_action(attachments, action_name):
    """Deep-copies `attachments` with `action_name` removed from the finding
    card's actions. Returns None if the card isn't in there (defensive; the
    caller then leaves the message alone rather than guessing at its shape)."""
    if not attachments:
        return None
    updated = copy.deepcopy(attachments)
    found = False
    for attachment in updated:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        attachment["actions"] = [a for a in (attachment.get("actions") or []) if a.get("name") != action_name]
        found = True
    return updated if found else None


def _attachments_with_status(base_attachments, status_line):
    """Deep-copies `base_attachments` (already stripped of the ask-nubi action,
    with its original evidence text) and appends `status_line`. Always starts
    from the pristine base so successive calls (Analyzing -> Analyzed) replace
    rather than stack the status line."""
    attachments = copy.deepcopy(base_attachments)
    for attachment in attachments:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        base_text = attachment.get("text") or ""
        attachment["text"] = "\n".join(part for part in (base_text, status_line) if part)
    return attachments


def _analyzing_status_line(slack_user_id):
    # ":mag:" (shortcode), not the raw "🔍" character: legacy attachment text
    # is written and read back through Slack's mrkdwn text field, which
    # silently rewrites a raw emoji character into its shortcode form on
    # storage — confirmed live, a written "🔍" round-trips as ":mag:". Writing
    # the shortcode ourselves means what we write and what a later fetch
    # returns are byte-identical, so an exact match always finds this line
    # (Slack renders the shortcode as the icon either way).
    return f":mag: Analyzing… (requested by <@{slack_user_id}>)"


def _analyzed_status_line(slack_user_id):
    return f":white_check_mark: Analyzed by <@{slack_user_id}> · see thread :mag:"


def _replace_status_line(attachments, old_line, new_line):
    """Deep-copies `attachments` and, in the finding card's text, swaps the
    exact `old_line` for `new_line` (dropping it if `new_line` is falsy).
    Every other line — including one a concurrent action (e.g. Suppress)
    added in the meantime — is left untouched."""
    updated = copy.deepcopy(attachments)
    for attachment in updated:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        lines = (attachment.get("text") or "").split("\n")
        if new_line:
            lines = [new_line if line == old_line else line for line in lines]
        else:
            lines = [line for line in lines if line != old_line]
        attachment["text"] = "\n".join(lines)
    return updated


def _restore_action(attachments, action):
    """Deep-copies `attachments` and re-inserts `action` into the finding
    card's actions if one with the same name isn't already there (e.g. the
    ask_nubi button, put back after a failed/incomplete analysis). Inserted
    right after the first action to match its original position."""
    if not action:
        return copy.deepcopy(attachments)
    updated = copy.deepcopy(attachments)
    for attachment in updated:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        actions = attachment.get("actions") or []
        if action.get("name") not in {a.get("name") for a in actions}:
            attachment["actions"] = actions[:1] + [action] + actions[1:]
    return updated


def _find_action(attachments, action_name):
    for attachment in attachments or []:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        for action in attachment.get("actions") or []:
            if action.get("name") == action_name:
                return action
    return None


@dataclass
class EventAnalysisContext:
    """Everything `_run_event_analysis_background` needs beyond the Slack
    app handles, bundled to keep that function's signature manageable."""

    event_id: str
    account_id: str
    user_id: str
    tenant_id: str
    channel_id: str
    team_id: str
    thread_ts: str
    message_ts: Optional[str]
    slack_user_id: str
    original_attachments: Optional[list]
    base_attachments: Optional[list]
    app_id: Optional[str]


def _attachment_has_line(attachments, line):
    """True only if the finding-card attachment is present in `attachments`
    AND its text contains `line` exactly. Used to decide whether a live-fetched
    card is trustworthy enough to edit: if the card wasn't found (e.g. the
    fetch returned something callback_id-guarded helpers can't recognize) or
    the analyzing line isn't there (e.g. text drift), editing it would be a
    silent no-op that leaves the card stuck on "Analyzing…" forever with no
    button — better to fall through to the snapshot fallback in that case."""
    for attachment in attachments or []:
        if attachment.get("callback_id") != FINDING_CALLBACK_ID:
            continue
        if line in (attachment.get("text") or "").split("\n"):
            return True
    return False


def _finalize_card_after_analysis(service, context, completed):
    """Applies the final card state after analysis finishes, on top of the
    message's CURRENT attachments when reachable rather than the click-time
    snapshot — a background task can finish minutes after the click, and in
    that window someone may have suppressed the finding; blindly re-applying
    the snapshot would silently clobber that edit."""
    if not context.message_ts:
        return
    old_line = _analyzing_status_line(context.slack_user_id)
    live_attachments = service.common_service.get_message_attachments(
        context.channel_id, context.team_id, context.thread_ts, context.message_ts
    )
    if live_attachments and _attachment_has_line(live_attachments, old_line):
        if completed:
            updated = _replace_status_line(live_attachments, old_line, _analyzed_status_line(context.slack_user_id))
        else:
            ask_nubi_action = _find_action(context.original_attachments, ASK_NUBI_ACTION_NAME)
            updated = _restore_action(_replace_status_line(live_attachments, old_line, None), ask_nubi_action)
        service.common_service.update_slack_message_attachments(
            context.channel_id, context.team_id, context.message_ts, updated
        )
        return

    # Couldn't fetch the live message — fall back to the click-time snapshot
    # rather than leaving the card stuck on "Analyzing…" forever.
    if completed:
        if context.base_attachments:
            service.common_service.update_slack_message_attachments(
                context.channel_id,
                context.team_id,
                context.message_ts,
                _attachments_with_status(context.base_attachments, _analyzed_status_line(context.slack_user_id)),
            )
    elif context.original_attachments:
        service.common_service.update_slack_message_attachments(
            context.channel_id, context.team_id, context.message_ts, context.original_attachments
        )


def _run_event_analysis_background(engine, slack_app, teams_app, context: EventAnalysisContext):
    """Runs after the interactive-action response has already been sent to
    Slack, so it builds its own DB session/services rather than reusing the
    request-scoped ones (already closed by then).

    Must carry `app_id` forward: when more than one Slack app is installed to
    the same team_id, install resolution is ambiguous without it, and Slack
    rejects a chat.update from any app but the one that posted the message."""
    service = SlackActionsBaseService(engine, slack_app, teams_app)
    service.common_service.app_id = context.app_id
    try:
        try:
            result = Events.call_event_analysis_api(
                event_id=context.event_id,
                account_id=context.account_id,
                user_id=context.user_id,
                tenant_id=context.tenant_id,
            )
        except Exception as e:
            LOG.exception("Event analysis API call failed: %s", e)
            _finalize_card_after_analysis(service, context, completed=False)
            service.common_service.slack_reply_in_thread(
                context.channel_id,
                context.team_id,
                context.thread_ts,
                f"<@{context.slack_user_id}> Hmm, something went wrong while analyzing this. Mind trying again?",
            )
            return

        service.event_service.send_investigation_result_to_slack(
            result, context.channel_id, context.team_id, context.thread_ts, context.slack_user_id
        )
        _finalize_card_after_analysis(service, context, completed=(result or {}).get("status") == "COMPLETED")
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
