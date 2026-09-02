"""Hybrid finding card: ONE severity-striped legacy attachment with a
clickable title (title_link), evidence body, two-column fields, and all
actions inside the card (View Details, Ask Nubi, and the Suppress menu).
Top-level text/blocks stay empty (a non-empty text with no blocks renders as
a duplicate heading), and no attachment may carry blocks/footer/ts (Slack's
"Added by {app}" byline triggers)."""

import json
from types import SimpleNamespace

import notifications_server.services.actions_common as actions_common
from notifications_server.message_templates.slack.finding import (
    FINDING_CALLBACK_ID,
    SUPPRESS_ACTION_NAME,
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

    def test_suppress_menu_offers_both_scopes_with_signed_values(self):
        _, _, (att,) = _render(_finding())
        suppress = att["actions"][2]
        assert suppress["type"] == "select" and suppress["name"] == SUPPRESS_ACTION_NAME
        this_group, all_group = suppress["option_groups"]
        assert [o["text"] for o in this_group["options"]] == [
            "Suppress 1h",
            "Suppress 4h",
            "Suppress 24h",
            "Suppress 7d",
        ]
        assert "report_crash_loop" in all_group["text"]
        assert [o["text"] for o in all_group["options"]] == ["Suppress 4h", "Suppress 24h", "Suppress 7d"]
        body = json.loads(this_group["options"][1]["value"])["body"]
        assert body["action_name"] == SUPPRESS_ACTION_NAME
        params = body["action_params"]
        assert params["scope"] == "fingerprint" and params["duration_hours"] == 4
        assert params["fingerprint"] == "fp-77" and params["alertname"] == "report_crash_loop"
        assert params["account_id"] == "acc-1" and params["tenant_id"] == "tenant-1"
        all_params = json.loads(all_group["options"][0]["value"])["body"]["action_params"]
        assert all_params["scope"] == "alertname"
        # legacy attachment option values cap at 2000 chars
        assert all(len(o["value"]) < 2000 for group in suppress["option_groups"] for o in group["options"])

    def test_suppress_menu_degrades_with_missing_identity(self):
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
            "actions": [{"type": "select", "name": SUPPRESS_ACTION_NAME, "selected_options": [{"value": "v1"}]}],
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


def _suppress_click_payload():
    _, _, (attachment,) = _render(_finding())
    option = attachment["actions"][2]["option_groups"][1]["options"][1]  # all alerts · 24h
    data = {
        "type": "interactive_message",
        "channel": {"id": "C1"},
        "team": {"id": "T1"},
        "user": {"id": "U1"},
        "message_ts": "111.222",
        "original_message": {"ts": "111.222", "attachments": [json.loads(json.dumps(attachment))]},
        "actions": [{"type": "select", "name": SUPPRESS_ACTION_NAME, "selected_options": [{"value": option["value"]}]}],
    }
    return SlackInteractiveActionsService.normalize_legacy_payload(data)


def _service_with(common_service):
    service = object.__new__(SlackInteractiveActionsService)
    service.common_service = common_service
    return service


class TestSuppressCallback:
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
        data = _suppress_click_payload()
        service.handle_suppress_finding("C1", "T1", "u@x.com", data["actions"][0], data)

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
        assert all(a.get("name") != SUPPRESS_ACTION_NAME for a in updated["actions"])
        status_line = updated["text"].splitlines()[-1]
        assert status_line.startswith("Suppressed")
        assert "🔕" not in status_line

        thread = calls["thread"]
        assert "won't notify" in thread
        # Active voice: the person is the subject, not a trailing agent.
        assert thread.startswith("<@U1> suppressed ")
        assert "🔕" not in thread
        # Undo belongs in Triage Rules — not on the event page the alert came from.
        assert "#events/triage-rules" in thread and "Open Triage Rules" in thread
        assert "/investigate" not in thread

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
        data = _suppress_click_payload()
        service.handle_suppress_finding("C1", "T1", "u@x.com", data["actions"][0], data)

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
        data = _suppress_click_payload()
        value = json.loads(data["actions"][0]["selected_option"]["value"])
        value["body"]["action_params"]["account_id"] = "acc-EVIL"
        data["actions"][0]["selected_option"]["value"] = json.dumps(value)

        service.handle_suppress_finding("C1", "T1", "u@x.com", data["actions"][0], data)

        assert posted == []  # neither authz nor rule-create was called
        assert calls.get("thread") == actions_common.UNABLE_TO_PROCESS_REQUEST


def _ask_nubi_service_with(common_service, event_service):
    service = object.__new__(SlackInteractiveActionsService)
    service.common_service = common_service
    service.event_service = event_service
    service.engine = "fake-engine"
    service.session = "fake-session"
    service.slack_app = None
    service.teams_app = None
    return service


def _run_background(
    monkeypatch,
    *,
    analysis_result=None,
    analysis_error=None,
    get_message_attachments=None,
    context_kwargs=None,
):
    """Drives _run_event_analysis_background with sensible defaults for the
    user_id lookup / cache write / reaction that now happen inside it (moved
    there so the interactive-action ack isn't blocked on them). Returns the
    `calls` dict recording what got invoked, plus the common_service used."""
    calls = {"update": []}
    common_service = SimpleNamespace(
        update_slack_message_attachments=lambda ch, team, ts, atts: calls["update"].append(atts)
        or calls.setdefault("update_last", atts),
        get_message_attachments=get_message_attachments or (lambda *a, **k: None),
        add_slack_reactions=lambda *a, **k: None,
        slack_reply_in_thread=lambda ch, team, ts, msg: calls.setdefault("thread", msg),
        app_id=(context_kwargs or {}).get("app_id", "A0BMNEUDJMA"),
    )
    event_service = SimpleNamespace(
        cache=SimpleNamespace(cache_event_entry=lambda **k: calls.setdefault("cached", k)),
        send_investigation_result_to_slack=lambda *a, **k: calls.setdefault("sent", True),
    )
    fake_service = SimpleNamespace(
        common_service=common_service, event_service=event_service, close=lambda: calls.setdefault("closed", True)
    )
    monkeypatch.setattr(actions_common, "SlackActionsBaseService", lambda engine, slack_app, teams_app: fake_service)
    monkeypatch.setattr(actions_common, "validate_and_get_user_id", lambda email: "user-9")
    if analysis_error is not None:
        monkeypatch.setattr(
            actions_common.Events,
            "call_event_analysis_api",
            staticmethod(lambda **k: (_ for _ in ()).throw(analysis_error)),
        )
    else:
        monkeypatch.setattr(actions_common.Events, "call_event_analysis_api", staticmethod(lambda **k: analysis_result))

    defaults = dict(
        event_id="find-77",
        account_id="acc-1",
        user_email="u@x.com",
        tenant_id="tenant-1",
        channel_id="C1",
        team_id="T1",
        thread_ts="111.222",
        message_ts="111.222",
        slack_user_id="U1",
        original_attachments=None,
        base_attachments=None,
        app_id="A0BMNEUDJMA",
    )
    defaults.update(context_kwargs or {})
    context = actions_common.EventAnalysisContext(**defaults)

    actions_common._run_event_analysis_background(None, None, None, context)
    return calls, common_service


class TestAskNubiAnalyse:
    """Regression coverage for #34756: clicking "Ask Nubi to Analyse!" used to
    leave Slack's legacy interactive_message fallback to replace the whole
    card with a bare "OK", because the handler never re-sent the card's
    content and ran the LLM call synchronously past Slack's ack window."""

    def test_click_marks_card_analyzing_and_defers_analysis(self, monkeypatch):
        # user_id lookup / cache write / reaction are deliberately NOT done
        # here (they moved into the background task) -- this handler's only
        # job before returning is pinning the card, as fast as possible.
        monkeypatch.setattr(
            actions_common, "load_installation", lambda session, tenant_id, platform: SimpleNamespace(app_id=None)
        )
        _, _, (attachment,) = _render(_finding())
        message = {"ts": "111.222", "thread_ts": "111.222", "attachments": [json.loads(json.dumps(attachment))]}
        data = {"message": message}

        calls = {}
        common_service = SimpleNamespace(
            update_slack_message_attachments=lambda ch, team, ts, atts: calls.setdefault("update", atts),
            app_id="A0BMNEUDJMA",
        )
        service = _ask_nubi_service_with(common_service, event_service=None)

        scheduled = {}
        background_tasks = SimpleNamespace(add_task=lambda fn: scheduled.setdefault("fn", fn))
        action_params = {"tenant_id": "tenant-1", "event_id": "find-77", "cluster_id": "acc-1"}

        service.handle_event_analysis_call("C1", "T1", "U1", "u@x.com", data, action_params, background_tasks)

        (updated,) = calls["update"]
        assert any(a.get("name") == "ask_nubi" for a in updated["actions"])  # button never hidden, even mid-analysis
        assert "Analyzing" in updated["text"]
        # The card is never left for Slack's default fallback to replace with "OK",
        # and the slow LLM call is deferred rather than run synchronously.
        assert "fn" in scheduled

    def test_resolves_app_id_from_tenant_id_when_click_gave_none(self, monkeypatch):
        # Regression: a real legacy interactive_message click never carries
        # api_app_id at all, so common_service.app_id (set from the click
        # payload in execute_action) is None when multiple Slack apps share
        # a team_id -- every chat.update below would then silently fail
        # against whatever the ambiguous default resolves to. The handler
        # must resolve app_id from the installation matching the signed
        # tenant_id in that case, instead of leaving it unset.
        installations = {}

        def fake_load_installation(session, tenant_id, platform):
            installations["queried"] = (tenant_id, platform)
            return SimpleNamespace(app_id="A_CORRECT_APP")

        monkeypatch.setattr(actions_common, "load_installation", fake_load_installation)
        _, _, (attachment,) = _render(_finding())
        message = {"ts": "111.222", "thread_ts": "111.222", "attachments": [json.loads(json.dumps(attachment))]}
        data = {"message": message}

        common_service = SimpleNamespace(
            update_slack_message_attachments=lambda ch, team, ts, atts: None,
            app_id=None,
        )
        service = _ask_nubi_service_with(common_service, event_service=None)
        background_tasks = SimpleNamespace(add_task=lambda fn: None)
        action_params = {"tenant_id": "tenant-1", "event_id": "find-77", "cluster_id": "acc-1"}

        service.handle_event_analysis_call("C1", "T1", "U1", "u@x.com", data, action_params, background_tasks)

        assert installations["queried"] == ("tenant-1", "slack")
        assert common_service.app_id == "A_CORRECT_APP"

    def test_falls_back_to_team_id_lookup_when_tenant_id_lookup_is_empty(self, monkeypatch):
        # If the tenant_id lookup can't resolve an app_id at all (no install
        # found, or found but app_id unset), fall back to the ambiguous
        # team_id lookup rather than leaving app_id untouched -- some
        # resolution attempt beats none.
        monkeypatch.setattr(actions_common, "load_installation", lambda session, tenant_id, platform: None)
        team_lookups = {}

        def fake_load_installation_by_team(session, team_id, platform):
            team_lookups["queried"] = (team_id, platform)
            return SimpleNamespace(app_id="A_FALLBACK_APP")

        monkeypatch.setattr(actions_common, "load_installation_by_team", fake_load_installation_by_team)
        _, _, (attachment,) = _render(_finding())
        message = {"ts": "111.222", "thread_ts": "111.222", "attachments": [json.loads(json.dumps(attachment))]}
        data = {"message": message}

        common_service = SimpleNamespace(
            update_slack_message_attachments=lambda ch, team, ts, atts: None,
            app_id=None,
        )
        service = _ask_nubi_service_with(common_service, event_service=None)
        background_tasks = SimpleNamespace(add_task=lambda fn: None)
        action_params = {"tenant_id": "tenant-1", "event_id": "find-77", "cluster_id": "acc-1"}

        service.handle_event_analysis_call("C1", "T1", "U1", "u@x.com", data, action_params, background_tasks)

        assert team_lookups["queried"] == ("T1", "slack")
        assert common_service.app_id == "A_FALLBACK_APP"

    def test_skips_installation_lookup_when_app_id_already_known(self, monkeypatch):
        # A payload that already carried a real api_app_id shouldn't pay for
        # (or risk being overridden by) the tenant_id/team_id DB lookups at
        # all -- only the ambiguous None case needs resolving.
        def fail(*a, **k):
            raise AssertionError("should not be called when app_id is already known")

        monkeypatch.setattr(actions_common, "load_installation", fail)
        monkeypatch.setattr(actions_common, "load_installation_by_team", fail)
        _, _, (attachment,) = _render(_finding())
        message = {"ts": "111.222", "thread_ts": "111.222", "attachments": [json.loads(json.dumps(attachment))]}
        data = {"message": message}

        common_service = SimpleNamespace(
            update_slack_message_attachments=lambda ch, team, ts, atts: None,
            app_id="A_ALREADY_KNOWN",
        )
        service = _ask_nubi_service_with(common_service, event_service=None)
        background_tasks = SimpleNamespace(add_task=lambda fn: None)
        action_params = {"tenant_id": "tenant-1", "event_id": "find-77", "cluster_id": "acc-1"}

        service.handle_event_analysis_call("C1", "T1", "U1", "u@x.com", data, action_params, background_tasks)

        assert common_service.app_id == "A_ALREADY_KNOWN"

    def test_background_task_resolves_user_id_caches_and_reacts(self, monkeypatch):
        # The work moved out of the synchronous handler (user_id lookup,
        # cache write, reaction) must still actually happen, just later.
        calls, _ = _run_background(monkeypatch, analysis_result={"status": "COMPLETED"})
        assert calls["cached"]["event_entry"]["user_id"] == "user-9"
        assert calls.get("sent") is True

    def test_background_task_recovers_when_user_id_lookup_fails(self, monkeypatch):
        # Regression: the "Analyzing..." stamp is already on the card by the
        # time the background task runs, so a failure BEFORE the LLM call
        # (e.g. resolving user_id) must still restore the card and reply --
        # not propagate uncaught and leave "Analyzing..." stuck forever with
        # nothing watching it.
        original_attachments = [
            {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": [{"name": "ask_nubi"}]}
        ]
        calls = {}
        common_service = SimpleNamespace(
            update_slack_message_attachments=lambda ch, team, ts, atts: calls.setdefault("update", atts),
            get_message_attachments=lambda *a, **k: None,  # live fetch unavailable -> falls back to the snapshot
            slack_reply_in_thread=lambda ch, team, ts, msg: calls.setdefault("thread", msg),
            app_id="A0BMNEUDJMA",
        )
        fake_service = SimpleNamespace(
            common_service=common_service, event_service=None, close=lambda: calls.setdefault("closed", True)
        )
        monkeypatch.setattr(
            actions_common, "SlackActionsBaseService", lambda engine, slack_app, teams_app: fake_service
        )
        monkeypatch.setattr(
            actions_common,
            "validate_and_get_user_id",
            lambda email: (_ for _ in ()).throw(RuntimeError("db unavailable")),
        )

        context = actions_common.EventAnalysisContext(
            event_id="find-77",
            account_id="acc-1",
            user_email="u@x.com",
            tenant_id="tenant-1",
            channel_id="C1",
            team_id="T1",
            thread_ts="111.222",
            message_ts="111.222",
            slack_user_id="U1",
            original_attachments=original_attachments,
            base_attachments=None,
            app_id="A0BMNEUDJMA",
        )

        actions_common._run_event_analysis_background(None, None, None, context)

        assert calls["update"] == original_attachments  # ask_nubi button restored so the user can retry
        assert "trying again" in calls["thread"].lower()
        assert calls.get("closed") is True

    def test_background_task_recovery_itself_failing_does_not_propagate(self, monkeypatch):
        # Regression: the except block that recovers from an analysis
        # failure makes its own live Slack calls (card restore, thread
        # reply). If one of those ALSO fails (e.g. a transient Slack API
        # error), that must not propagate out of the background task
        # uncaught -- that would re-leak the exact stuck-on-"Analyzing..."
        # bug this whole function exists to prevent.
        calls = {}
        common_service = SimpleNamespace(
            update_slack_message_attachments=lambda ch, team, ts, atts: calls.setdefault("update", atts),
            get_message_attachments=lambda *a, **k: None,
            slack_reply_in_thread=lambda ch, team, ts, msg: (_ for _ in ()).throw(RuntimeError("slack unavailable")),
            app_id="A0BMNEUDJMA",
        )
        fake_service = SimpleNamespace(
            common_service=common_service, event_service=None, close=lambda: calls.setdefault("closed", True)
        )
        monkeypatch.setattr(
            actions_common, "SlackActionsBaseService", lambda engine, slack_app, teams_app: fake_service
        )
        monkeypatch.setattr(
            actions_common,
            "validate_and_get_user_id",
            lambda email: (_ for _ in ()).throw(RuntimeError("db unavailable")),
        )

        context = actions_common.EventAnalysisContext(
            event_id="find-77",
            account_id="acc-1",
            user_email="u@x.com",
            tenant_id="tenant-1",
            channel_id="C1",
            team_id="T1",
            thread_ts="111.222",
            message_ts="111.222",
            slack_user_id="U1",
            original_attachments=None,
            base_attachments=None,
            app_id="A0BMNEUDJMA",
        )

        actions_common._run_event_analysis_background(None, None, None, context)  # must not raise

        assert calls.get("closed") is True

    def test_background_task_restores_card_and_replies_on_failure(self, monkeypatch):
        original_attachments = [
            {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": [{"name": "ask_nubi"}]}
        ]
        calls, common_service = _run_background(
            monkeypatch,
            analysis_error=RuntimeError("llm-server unavailable"),
            context_kwargs={"original_attachments": original_attachments, "app_id": "A0BMNEUDJMA"},
        )

        # app_id carried into the fresh background-task service so it resolves
        # the same Slack app that posted the card, not an ambiguous default.
        assert common_service.app_id == "A0BMNEUDJMA"
        assert calls["update_last"] == original_attachments  # ask_nubi button restored so the user can retry
        assert "trying again" in calls["thread"].lower()
        assert calls.get("closed") is True

    def test_background_task_marks_card_analyzed_on_success(self, monkeypatch):
        base_attachments = [
            {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": [{"name": "ask_nubi"}]}
        ]
        calls, _ = _run_background(
            monkeypatch,
            analysis_result={"status": "COMPLETED"},
            context_kwargs={"base_attachments": base_attachments},
        )

        assert calls["sent"] is True
        updated = calls["update_last"][0]
        assert "Analyzed" in updated["text"]
        assert any(a.get("name") == "ask_nubi" for a in updated["actions"])  # button never hidden, even on success

    def test_background_task_restores_card_when_result_is_not_completed(self, monkeypatch):
        # A 2xx response with status UNKNOWN is not a failure (no exception),
        # but it's also not a real in-progress status — the card must not be
        # stamped "Analyzed" with the retry button gone.
        original_attachments = [
            {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": [{"name": "ask_nubi"}]}
        ]
        calls, _ = _run_background(
            monkeypatch,
            analysis_result={"status": "SOMETHING_UNEXPECTED"},
            context_kwargs={
                "original_attachments": original_attachments,
                "base_attachments": [
                    {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": []}
                ],
            },
        )

        assert calls["sent"] is True
        assert calls["update_last"] == original_attachments  # button restored, not stamped "Analyzed"

    def test_background_task_marks_card_analyzing_again_when_in_progress(self, monkeypatch):
        # llm-server can genuinely still be working asynchronously (not a
        # failure) -- there's no follow-up mechanism here to catch the
        # eventual result (possibly not even in this thread, if the
        # in-flight run was kicked off elsewhere), so the card just reads
        # the same as a fresh click rather than stamping "Analyzed" or
        # resetting to pristine as if nothing happened. The button stays
        # available so any user can click again to check whether it's done.
        base_attachments = [
            {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": [{"name": "ask_nubi"}]}
        ]
        for status in ("IN_PROGRESS", "CREATED"):
            calls, _ = _run_background(
                monkeypatch,
                analysis_result={"status": status},
                context_kwargs={"base_attachments": base_attachments},
            )
            assert calls["sent"] is True
            updated = calls["update_last"][0]
            assert "Analyzing" in updated["text"]
            assert "Analyzed" not in updated["text"]
            assert any(a.get("name") == "ask_nubi" for a in updated["actions"])  # button never hidden

    def test_background_task_preserves_concurrent_suppress_edit_on_success(self, monkeypatch):
        # Regression guard: a suppress click landing while analysis is still
        # running must not be silently overwritten by the background task's
        # final update, which used to blindly re-apply its click-time snapshot.
        analyzing_line = actions_common._analyzing_status_line()
        live_attachments = [
            {
                "callback_id": actions_common.FINDING_CALLBACK_ID,
                "text": f"orig\n{analyzing_line}\nSuppressed (this alert · 4h) by <@U2> until <!date^1^{{date}}|later>",
                "actions": [{"name": "other"}],
            }
        ]
        calls, _ = _run_background(
            monkeypatch,
            analysis_result={"status": "COMPLETED"},
            get_message_attachments=lambda ch, team, thread_ts, ts: live_attachments,
            context_kwargs={
                "base_attachments": [{"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": []}]
            },
        )

        updated = calls["update_last"][0]
        assert "Suppressed" in updated["text"]  # concurrent edit survived
        assert analyzing_line not in updated["text"]  # replaced, not left dangling
        assert "Analyzed" in updated["text"]

    def test_background_task_preserves_concurrent_suppress_edit_on_failure(self, monkeypatch):
        analyzing_line = actions_common._analyzing_status_line()
        live_attachments = [
            {
                "callback_id": actions_common.FINDING_CALLBACK_ID,
                "text": f"orig\n{analyzing_line}\nSuppressed (this alert · 4h) by <@U2> until <!date^1^{{date}}|later>",
                "actions": [{"name": "other"}, {"name": "ask_nubi"}],
            }
        ]
        calls, _ = _run_background(
            monkeypatch,
            analysis_error=RuntimeError("llm-server unavailable"),
            get_message_attachments=lambda ch, team, thread_ts, ts: live_attachments,
        )

        updated = calls["update_last"][0]
        assert "Suppressed" in updated["text"]  # concurrent edit survived
        assert analyzing_line not in updated["text"]
        assert [a["name"] for a in updated["actions"]] == [
            "other",
            "ask_nubi",
        ]  # button never removed, suppress untouched

    def test_background_task_keeps_button_on_in_progress_via_live_fetch(self, monkeypatch):
        # Same button-availability guarantee as the failure path above, but
        # on the in_progress outcome and via the live-refetch branch
        # specifically (not just the click-time-snapshot fallback).
        analyzing_line = actions_common._analyzing_status_line()
        live_attachments = [
            {
                "callback_id": actions_common.FINDING_CALLBACK_ID,
                "text": f"orig\n{analyzing_line}",
                "actions": [{"name": "other"}, {"name": "ask_nubi"}],
            }
        ]
        calls, _ = _run_background(
            monkeypatch,
            analysis_result={"status": "IN_PROGRESS"},
            get_message_attachments=lambda ch, team, thread_ts, ts: live_attachments,
        )

        updated = calls["update_last"][0]
        assert "Analyzing" in updated["text"]
        assert [a["name"] for a in updated["actions"]] == ["other", "ask_nubi"]  # button never removed

    def test_background_task_falls_back_to_snapshot_when_live_fetch_lacks_the_analyzing_line(self, monkeypatch):
        # If the live-fetched card doesn't actually contain the analyzing
        # line (text drift, or no FINDING_CALLBACK_ID attachment at all),
        # editing it would be a silent no-op -- the card would stay stuck on
        # "Analyzing..." forever with the button gone and no way to retry.
        # Must fall through to the snapshot fallback instead of trusting it.
        live_attachments_missing_the_line = [
            {"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig (no analyzing line here)", "actions": []}
        ]
        base_attachments = [{"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig", "actions": []}]
        calls, _ = _run_background(
            monkeypatch,
            analysis_result={"status": "COMPLETED"},
            get_message_attachments=lambda ch, team, thread_ts, ts: live_attachments_missing_the_line,
            context_kwargs={"base_attachments": base_attachments},
        )

        # Fell back to the base_attachments snapshot (stamped "Analyzed"),
        # not a no-op copy of the live-fetched text.
        assert "Analyzed" in calls["update_last"][0]["text"]

    def test_analyzing_status_line_uses_shortcode_not_raw_emoji(self):
        # Root-cause guard for a real bug found via live testing: Slack
        # silently rewrites a raw emoji character written into legacy
        # attachment text into its ":shortcode:" form on storage (a written
        # "🔍" round-trips as ":mag:"), so an exact match against a line
        # built with the raw character never matches what a later live
        # fetch returns. Writing the shortcode ourselves keeps write and
        # read forms identical -- Slack renders the icon either way.
        assert "🔍" not in actions_common._analyzing_status_line()
        assert ":mag:" in actions_common._analyzing_status_line()
        assert "✅" not in actions_common._analyzed_status_line()
        assert ":white_check_mark:" in actions_common._analyzed_status_line()

    def test_has_finding_attachment_true_only_when_card_present(self):
        finding = [{"callback_id": actions_common.FINDING_CALLBACK_ID, "actions": []}]
        assert actions_common._has_finding_attachment(finding) is True
        assert actions_common._has_finding_attachment([{"callback_id": "other"}]) is False
        assert actions_common._has_finding_attachment(None) is False
        assert actions_common._has_finding_attachment([]) is False

    def test_with_status_line_appends_when_none_present(self):
        base = [{"callback_id": actions_common.FINDING_CALLBACK_ID, "text": "orig"}]
        result = actions_common._with_status_line(base, "status line")
        assert result[0]["text"] == "orig\nstatus line"
        assert base[0]["text"] == "orig"  # base left untouched for reuse across states

    def test_with_status_line_replaces_any_prior_known_status_line_instead_of_stacking(self):
        # Regression: re-clicking Ask Nubi while a status line is already
        # showing (now always possible, since the button is never hidden)
        # must swap it, not stack a second one underneath.
        for old_line in (actions_common._analyzing_status_line(), actions_common._analyzed_status_line()):
            attachments = [{"callback_id": actions_common.FINDING_CALLBACK_ID, "text": f"orig\n{old_line}\nkept"}]
            result = actions_common._with_status_line(attachments, "new status")
            assert result[0]["text"] == "orig\nkept\nnew status"

    def test_with_status_line_drops_status_line_when_falsy(self):
        attachments = [
            {
                "callback_id": actions_common.FINDING_CALLBACK_ID,
                "text": f"orig\n{actions_common._analyzing_status_line()}\nkept",
            }
        ]
        result = actions_common._with_status_line(attachments, None)
        assert result[0]["text"] == "orig\nkept"

    def test_with_status_line_preserves_unrelated_lines_including_blank_ones(self):
        # A blank line that's part of the evidence text itself (paragraph
        # spacing), and a concurrent edit like a Suppress note, must both
        # survive -- only a line that's exactly one of our known status
        # lines is ever touched.
        attachments = [
            {
                "callback_id": actions_common.FINDING_CALLBACK_ID,
                "text": f"para one\n\npara two\n{actions_common._analyzing_status_line()}\nSuppressed by <@U2>",
            }
        ]
        result = actions_common._with_status_line(attachments, None)
        assert result[0]["text"] == "para one\n\npara two\nSuppressed by <@U2>"
