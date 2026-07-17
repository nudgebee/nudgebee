"""Hybrid Datadog-style finding card: ONE severity-striped legacy attachment
with a clickable title (title_link), evidence body, trailing facts line, and
both actions inside the card. Top-level text/blocks stay empty (a non-empty
text with no blocks renders as a duplicate heading), and no attachment may
carry blocks/footer/ts (Slack's "Added by {app}" byline triggers)."""

import json
from types import SimpleNamespace

from notifications_server.message_templates.slack.finding import (
    FINDING_CALLBACK_ID,
    get_slack_finding_message,
)
from notifications_server.services.actions_common import SlackInteractiveActionsService


def _finding(priority="HIGH", evidences=None):
    return {
        "id": "find-77",
        "title": "checkout-api is crash-looping",
        "priority": priority,
        "cluster": "prod-eu",
        "subject_name": "checkout-api",
        "subject_namespace": "payments",
        "cloud_account_id": "acc-1",
        "created_at": "2026-07-15T14:07:00+00:00",
        "service_key": "k8s/payments/checkout-api",
        "aggregation_key": "report_crash_loop",
        "evidences": (
            evidences
            if evidences is not None
            else [
                {"data": {"data": "*Restarts:* 14 in the last 20 minutes"}, "type": "markdown"},
                {"data": {"data": "Last state: OOMKilled — exit code 137"}, "type": "markdown"},
            ]
        ),
    }


def _render(finding):
    installation = SimpleNamespace(tenant_id="tenant-1", token="not-used", team_id="T000")
    return get_slack_finding_message(None, installation, finding)


class TestHybridCardShape:
    def test_top_level_is_empty_and_single_attachment(self):
        message, blocks, attachments = _render(_finding())
        assert message == ""  # non-empty text + no blocks = duplicate heading
        assert blocks == []
        assert len(attachments) == 1

    def test_clickable_title_is_the_deep_link(self):
        _, _, (att,) = _render(_finding())
        assert att["title"] == "checkout-api is crash-looping"
        assert "accountId=acc-1" in att["title_link"] and "id=find-77" in att["title_link"]

    def test_no_byline_trigger_keys(self):
        _, _, (att,) = _render(_finding())
        assert "blocks" not in att and "footer" not in att and "ts" not in att
        assert att["mrkdwn_in"] == ["text"]

    def test_evidence_and_facts_ride_the_text(self):
        _, _, (att,) = _render(_finding())
        lines = att["text"].split("\n")
        assert lines[0] == "*Restarts:* 14 in the last 20 minutes"
        assert lines[1] == "Last state: OOMKilled — exit code 137"
        assert lines[2].startswith("High priority · Acct: prod-eu · payments/checkout-api · reported <!date^")

    def test_no_evidence_still_renders_facts(self):
        _, _, (att,) = _render(_finding(evidences=[]))
        assert att["text"].startswith("High priority · Acct: prod-eu")

    def test_priority_maps_to_stripe(self):
        _, _, (high,) = _render(_finding(priority="HIGH"))
        _, _, (low,) = _render(_finding(priority="LOW"))
        assert high["color"] == "#C93A36"
        assert low["color"] == "#D97A2B"

    def test_actions_view_details_primary_and_ask_nubi_value(self):
        _, _, (att,) = _render(_finding())
        assert att["callback_id"] == FINDING_CALLBACK_ID
        details, ask = att["actions"]
        assert details["text"] == "View Details" and details["style"] == "primary"
        assert "id=find-77" in details["url"]
        assert ask["name"] == "ask_nubi" and "url" not in ask
        body = json.loads(ask["value"])["body"]
        assert body["action_name"] == "ask_ai"
        assert body["action_params"]["event_id"] == "find-77"
        assert body["action_params"]["tenant_id"] == "tenant-1"


class TestLegacyPayloadNormalization:
    def test_interactive_message_gets_message_synthesized(self):
        data = {
            "type": "interactive_message",
            "callback_id": FINDING_CALLBACK_ID,
            "message_ts": "123.456",
            "original_message": {"text": "", "attachments": []},
            "actions": [{"type": "button", "name": "ask_nubi", "value": "{}"}],
        }
        out = SlackInteractiveActionsService.normalize_legacy_payload(data)
        assert out["message"]["ts"] == "123.456"

    def test_block_actions_payload_untouched(self):
        data = {
            "type": "block_actions",
            "message": {"ts": "999.000"},
            "actions": [{"type": "button", "action_id": "x", "value": "{}"}],
        }
        out = SlackInteractiveActionsService.normalize_legacy_payload(data)
        assert out["message"] == {"ts": "999.000"}

    def test_original_message_ts_wins_when_present(self):
        data = {
            "type": "interactive_message",
            "original_message": {"ts": "111.222"},
            "actions": [{"type": "button"}],
        }
        out = SlackInteractiveActionsService.normalize_legacy_payload(data)
        assert out["message"]["ts"] == "111.222"
