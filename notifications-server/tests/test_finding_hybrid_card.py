"""Hybrid finding card: ONE severity-striped legacy attachment with a
clickable title (title_link), evidence body, two-column fields, and all
actions inside the card (View Details, Ask Nubi, and the Silence menu).
Top-level text/blocks stay empty (a non-empty text with no blocks renders as
a duplicate heading), and no attachment may carry blocks/footer/ts (Slack's
"Added by {app}" byline triggers)."""

import json
from types import SimpleNamespace

import notifications_server.services.actions_common as actions_common
from notifications_server.message_templates.slack.finding import (
    FINDING_CALLBACK_ID,
    SILENCE_ACTION_NAME,
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
        "fingerprint": "fp-77",
        "source": "prometheus",
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
        assert att["mrkdwn_in"] == ["text", "fields"]

    def test_evidence_rides_the_text(self):
        _, _, (att,) = _render(_finding())
        lines = att["text"].split("\n")
        assert lines[0] == "*Restarts:* 14 in the last 20 minutes"
        assert lines[1] == "Last state: OOMKilled — exit code 137"

    def test_facts_render_as_fields(self):
        _, _, (att,) = _render(_finding())
        short_fields = {f["title"]: f["value"] for f in att["fields"] if f["short"]}
        assert short_fields == {
            "Cluster": "prod-eu",
            "Resource": "payments/checkout-api",
            "Priority": "High",
            "Source": "Prometheus",
        }
        (reported,) = [f for f in att["fields"] if not f["short"]]
        assert reported["title"] == ""
        assert reported["value"].startswith("reported <!date^")

    def test_no_evidence_still_renders_fields(self):
        _, _, (att,) = _render(_finding(evidences=[]))
        assert att["text"] == ""
        assert any(f["value"] == "prod-eu" for f in att["fields"])

    def test_priority_maps_to_stripe(self):
        _, _, (high,) = _render(_finding(priority="HIGH"))
        _, _, (low,) = _render(_finding(priority="LOW"))
        assert high["color"] == "#C93A36"
        assert low["color"] == "#D97A2B"

    def test_actions_view_details_primary_and_ask_nubi_value(self):
        _, _, (att,) = _render(_finding())
        assert att["callback_id"] == FINDING_CALLBACK_ID
        details, ask, _ = att["actions"]
        assert details["text"] == "View Details" and details["style"] == "primary"
        assert "id=find-77" in details["url"]
        assert ask["name"] == "ask_nubi" and "url" not in ask
        body = json.loads(ask["value"])["body"]
        assert body["action_name"] == "ask_ai"
        assert body["action_params"]["event_id"] == "find-77"
        assert body["action_params"]["tenant_id"] == "tenant-1"

    def test_silence_menu_offers_both_scopes_with_signed_values(self):
        _, _, (att,) = _render(_finding())
        silence = att["actions"][2]
        assert silence["type"] == "select" and silence["name"] == SILENCE_ACTION_NAME
        this_group, all_group = silence["option_groups"]
        assert [o["text"] for o in this_group["options"]] == ["Silence 1h", "Silence 4h", "Silence 24h", "Silence 7d"]
        assert "report_crash_loop" in all_group["text"]
        assert [o["text"] for o in all_group["options"]] == ["Silence 4h", "Silence 24h", "Silence 7d"]
        body = json.loads(this_group["options"][1]["value"])["body"]
        assert body["action_name"] == SILENCE_ACTION_NAME
        params = body["action_params"]
        assert params["scope"] == "fingerprint" and params["duration_hours"] == 4
        assert params["fingerprint"] == "fp-77" and params["alertname"] == "report_crash_loop"
        assert params["account_id"] == "acc-1" and params["tenant_id"] == "tenant-1"
        all_params = json.loads(all_group["options"][0]["value"])["body"]["action_params"]
        assert all_params["scope"] == "alertname"
        # legacy attachment option values cap at 2000 chars
        assert all(len(o["value"]) < 2000 for group in silence["option_groups"] for o in group["options"])

    def test_silence_menu_degrades_with_missing_identity(self):
        _, _, (att,) = _render({**_finding(), "fingerprint": ""})
        groups = att["actions"][2]["option_groups"]
        assert len(groups) == 1 and "report_crash_loop" in groups[0]["text"]

        _, _, (att,) = _render({**_finding(), "fingerprint": "", "aggregation_key": ""})
        assert [a.get("name") or a["text"] for a in att["actions"]] == ["View Details", "ask_nubi"]


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

    def test_legacy_menu_pick_gets_selected_option_singular(self):
        data = {
            "type": "interactive_message",
            "message_ts": "1.2",
            "original_message": {"ts": "1.2"},
            "actions": [{"type": "select", "name": SILENCE_ACTION_NAME, "selected_options": [{"value": "v1"}]}],
        }
        out = SlackInteractiveActionsService.normalize_legacy_payload(data)
        assert out["actions"][0]["selected_option"] == {"value": "v1"}


class _FakeResponse:
    def __init__(self, body):
        self._body = body

    def raise_for_status(self):
        pass

    def json(self):
        return self._body


def _silence_click_payload():
    _, _, (attachment,) = _render(_finding())
    option = attachment["actions"][2]["option_groups"][1]["options"][1]  # all alerts · 24h
    data = {
        "type": "interactive_message",
        "channel": {"id": "C1"},
        "team": {"id": "T1"},
        "user": {"id": "U1"},
        "message_ts": "111.222",
        "original_message": {"ts": "111.222", "attachments": [json.loads(json.dumps(attachment))]},
        "actions": [{"type": "select", "name": SILENCE_ACTION_NAME, "selected_options": [{"value": option["value"]}]}],
    }
    return SlackInteractiveActionsService.normalize_legacy_payload(data)


def _service_with(common_service):
    service = object.__new__(SlackInteractiveActionsService)
    service.common_service = common_service
    return service


class TestSilenceCallback:
    def test_click_creates_scoped_rule_and_updates_message(self, monkeypatch):
        posted = []

        def fake_post(url, json=None, headers=None, timeout=None):
            posted.append({"url": url, "json": json})
            if url.endswith("get_security_context"):
                return _FakeResponse({"context": {"AccountIds": ["acc-1"], "Roles": ["account_admin"]}})
            return _FakeResponse({"success": True})

        monkeypatch.setattr(actions_common, "requests", SimpleNamespace(post=fake_post))
        monkeypatch.setattr(actions_common, "validate_and_get_user_id", lambda email: "user-9")

        calls = {}
        service = _service_with(
            SimpleNamespace(
                slack_reply_in_thread=lambda ch, team, ts, msg: calls.setdefault("thread", msg),
                update_slack_message_attachments=lambda ch, team, ts, atts: calls.setdefault("update", atts),
            )
        )
        data = _silence_click_payload()
        service.handle_silence_finding("C1", "T1", "u@x.com", data["actions"][0], data)

        rule_call = posted[1]["json"]
        assert rule_call["action"] == {"name": "event_create_triage_rule"}
        assert rule_call["session_variables"] == {"tenant_id": "tenant-1", "user_id": "user-9"}
        rule_input = rule_call["input"]
        assert rule_input["rule_type"] == "suppression" and rule_input["action"] == "suppress"
        assert rule_input["match_alertname"] == "^report_crash_loop$"
        assert "match_fingerprint" not in rule_input
        assert rule_input["apply_to_existing"] is True
        assert rule_input["effective_until"].endswith("Z")

        (updated,) = calls["update"]
        assert all(a.get("name") != SILENCE_ACTION_NAME for a in updated["actions"])
        assert "Silenced" in updated["text"].splitlines()[-1]
        assert "won't notify" in calls["thread"]

    def test_click_without_permission_creates_nothing(self, monkeypatch):
        posted = []

        def fake_post(url, json=None, headers=None, timeout=None):
            posted.append(url)
            return _FakeResponse({"context": {"AccountIds": ["other"], "Roles": ["account_admin"]}})

        monkeypatch.setattr(actions_common, "requests", SimpleNamespace(post=fake_post))
        monkeypatch.setattr(actions_common, "validate_and_get_user_id", lambda email: "user-9")

        calls = {}
        service = _service_with(
            SimpleNamespace(
                slack_reply_in_thread=lambda ch, team, ts, msg: calls.setdefault("thread", msg),
                update_slack_message_attachments=lambda *args: calls.setdefault("update", args),
            )
        )
        data = _silence_click_payload()
        service.handle_silence_finding("C1", "T1", "u@x.com", data["actions"][0], data)

        assert len(posted) == 1  # authz lookup only, no rule creation
        assert "update" not in calls
        assert "permission" in calls["thread"]

    def test_tampered_payload_is_rejected_before_any_lookup(self, monkeypatch):
        posted = []
        monkeypatch.setattr(actions_common, "requests", SimpleNamespace(post=lambda *a, **k: posted.append(a)))
        monkeypatch.setattr(actions_common, "validate_and_get_user_id", lambda email: "user-9")

        calls = {}
        service = _service_with(
            SimpleNamespace(slack_reply_in_thread=lambda ch, team, ts, msg: calls.setdefault("thread", msg))
        )
        # Swap the account_id in the signed value without re-signing.
        data = _silence_click_payload()
        value = json.loads(data["actions"][0]["selected_option"]["value"])
        value["body"]["action_params"]["account_id"] = "acc-EVIL"
        data["actions"][0]["selected_option"]["value"] = json.dumps(value)

        service.handle_silence_finding("C1", "T1", "u@x.com", data["actions"][0], data)

        assert posted == []  # neither authz nor rule-create was called
        assert calls.get("thread") == actions_common.UNABLE_TO_PROCESS_REQUEST
