"""Buttons on Microsoft Teams cards: signed data, the stored card a click
updates, and Suppress."""

import asyncio
import json
import time
import uuid
from types import SimpleNamespace
from unittest import mock

import pytest

from notifications_server.configs import settings
from notifications_server.message_templates.ms_teams.finding import get_ms_teams_finding_message_template
from notifications_server.services import actions as actions_module
from notifications_server.services import actions_common
from notifications_server.services import events as events_module
from notifications_server.services import message as message_module
from notifications_server.services.ms_teams import card_actions as ca

NB_TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
OTHER_TENANT = "bbbbbbbb-2222-4222-8222-222222222222"
ACCOUNT = "cccccccc-3333-4333-8333-333333333333"
SERVICE_URL = "https://smba.example.com/teams/"
FINDING = {
    "id": "event-1",
    "title": "Pod restarting",
    "created_at": "2026-10-07T10:00:00",
    "fingerprint": "fp-1",
    "aggregation_key": "KubePodCrashLooping",
    "cloud_account_id": ACCOUNT,
}


def _ids(card):
    return [a.get("id") for a in card.get("actions") or []]


@pytest.fixture(autouse=True)
def signing_key(monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "test-signing-key")


# ------------------------------- signed data -------------------------------


def test_signed_data_round_trips_for_its_action_and_tenant():
    raw = ca.sign(ca.SUPPRESS, {"tenant_id": NB_TENANT, "scope": "fingerprint"})
    assert ca.verify(raw, ca.SUPPRESS, NB_TENANT)["scope"] == "fingerprint"
    assert json.loads(raw)["kid"] == ca.KEY_ID


def test_signed_data_is_refused_for_another_tenant_action_or_key():
    raw = ca.sign(ca.SUPPRESS, {"tenant_id": NB_TENANT})
    assert ca.verify(raw, ca.SUPPRESS, OTHER_TENANT) is None
    assert ca.verify(raw, "approve", NB_TENANT) is None
    assert ca.verify(json.dumps({**json.loads(raw), "kid": "0"}), ca.SUPPRESS, NB_TENANT) is None
    settings.ms_teams.action_signing_key = "rotated"
    assert ca.verify(raw, ca.SUPPRESS, NB_TENANT) is None


def test_tampered_or_expired_data_is_refused():
    raw = json.loads(ca.sign(ca.SUPPRESS, {"tenant_id": NB_TENANT, "duration_hours": 1}))
    raw["body"]["action_params"]["duration_hours"] = 168
    assert ca.verify(json.dumps(raw), ca.SUPPRESS, NB_TENANT) is None
    with mock.patch.object(ca.time, "time", return_value=time.time() - ca.MAX_AGE_SECONDS - 60):
        old = ca.sign(ca.SUPPRESS, {"tenant_id": NB_TENANT})
    assert ca.verify(old, ca.SUPPRESS, NB_TENANT) is None
    assert ca.verify("not json", ca.SUPPRESS, NB_TENANT) is None
    assert ca.verify(json.dumps(["not", "a", "dict"]), ca.SUPPRESS, NB_TENANT) is None
    for signature in (12345, None, {"v0": "x"}):
        forged = json.loads(ca.sign(ca.SUPPRESS, {"tenant_id": NB_TENANT}))
        forged["signature"] = signature
        assert ca.verify(json.dumps(forged), ca.SUPPRESS, NB_TENANT) is None


# ------------------------------- card content -------------------------------


def test_a_bot_finding_card_offers_suppress_with_every_choice_signed():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    suppress = card["actions"][-1]
    assert suppress["id"] == ca.SUPPRESS_ACTION_ID and ca.has_actions(card)
    choices = suppress["card"]["body"][0]["choices"]
    assert len(choices) == 7
    params = [ca.verify(c["value"], ca.SUPPRESS, NB_TENANT) for c in choices]
    assert {(p["scope"], p["duration_hours"]) for p in params} == {
        ("fingerprint", 1),
        ("fingerprint", 4),
        ("fingerprint", 24),
        ("fingerprint", 168),
        ("alertname", 4),
        ("alertname", 24),
        ("alertname", 168),
    }
    assert suppress["card"]["actions"][0]["data"] == {ca.ACTION_KEY: ca.SUPPRESS}
    assert len(json.dumps(card)) < 28_000


def test_cards_have_no_buttons_without_a_signing_key_or_for_webhooks():
    assert not ca.has_actions(get_ms_teams_finding_message_template(FINDING)), "webhook cards can't be clicked"
    settings.ms_teams.action_signing_key = ""
    assert not ca.has_actions(get_ms_teams_finding_message_template(FINDING, NB_TENANT))


def test_status_lines_replace_each_other_and_actions_can_be_removed():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    card = ca.with_status(ca.with_status(card, "Suppressing…"), "Suppressed")
    assert [b["text"] for b in card["body"] if b.get("id") == ca.STATUS_ID] == ["Suppressed"]
    assert ca.with_status(card, None)["body"][-1].get("id") != ca.STATUS_ID
    assert ca.SUPPRESS_ACTION_ID not in _ids(ca.without_action(card, ca.SUPPRESS_ACTION_ID))


# ------------------------------- stored card -------------------------------


@pytest.fixture
def store(monkeypatch):
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session
    from sqlalchemy.pool import StaticPool

    from notifications_server.models.models import Base, SentNotifications

    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__])
    monkeypatch.setattr(ca, "sync_engine", engine)
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    row = ca.card_row(uuid.UUID(NB_TENANT), ACCOUNT, "fp-1", "19:ops", "1700", card, "finding")
    row.id = uuid.uuid4()
    row.account_id = uuid.UUID(ACCOUNT)
    with Session(engine) as session:
        session.add(row)
        session.commit()

    def stored():
        with Session(engine) as session:
            return json.loads(session.query(SentNotifications).one().teams_metadata)

    return SimpleNamespace(engine=engine, stored=stored)


def test_a_card_row_holds_the_card_and_falls_back_to_the_message_id():
    row = ca.card_row(NB_TENANT, None, None, "19:ops", "1700", {"type": "AdaptiveCard"}, "finding")
    assert row.fingerprint == "ms_teams:1700"
    assert row.teams_channel_id == "19:ops" and row.teams_message_id == "1700"
    assert json.loads(row.teams_metadata) == {"kind": "finding", "card": {"type": "AdaptiveCard"}, "actions": {}}
    assert ca.card_row(NB_TENANT, None, None, "19:ops", "", {}, "finding") is None, "a click finds its card by id"
    threaded = ca.card_row(NB_TENANT, None, None, "19:ops;messageid=1", "1700", {}, "finding")
    assert threaded.teams_channel_id == "19:ops", "stored the way a click names its channel"


CARD = (uuid.UUID(NB_TENANT), "19:ops", "1700")


def test_an_action_runs_once(store):
    card = ca.begin(CARD, ca.SUPPRESS, lambda c: ca.with_status(c, "Suppressing…"))
    assert card["body"][-1]["text"] == "Suppressing…"
    assert ca.begin(CARD, ca.SUPPRESS, lambda c: c) is ca.BUSY, "a second click while it runs"
    ca.finish(CARD, ca.SUPPRESS, ca.DONE, lambda c: ca.with_status(c, "Suppressed"))
    assert store.stored()["actions"][ca.SUPPRESS]["state"] == ca.DONE
    assert ca.begin(CARD, ca.SUPPRESS, lambda c: c) is ca.REFUSED, "a click after it finished"


def test_an_abandoned_action_can_run_again(store):
    ca.begin(CARD, ca.SUPPRESS, lambda c: ca.with_status(c, "Suppressing…"))
    later = time.time() + ca.IN_PROGRESS_STALE_SECONDS + 1
    with mock.patch.object(ca.time, "time", return_value=later):
        assert ca.begin(CARD, ca.SUPPRESS, lambda c: c) not in (None, ca.REFUSED)


def test_a_failed_action_can_be_tried_again(store):
    ca.begin(CARD, ca.SUPPRESS, lambda c: ca.with_status(c, "Suppressing…"))
    card = ca.finish(CARD, ca.SUPPRESS, None, lambda c: ca.with_status(c, None))
    assert all(b.get("id") != ca.STATUS_ID for b in card["body"])
    assert ca.begin(CARD, ca.SUPPRESS, lambda c: c) not in (None, ca.REFUSED)


def test_another_tenant_channel_or_card_has_no_stored_card(store):
    assert ca.begin((uuid.UUID(OTHER_TENANT), "19:ops", "1700"), ca.SUPPRESS, lambda c: c) is None
    assert ca.begin((uuid.UUID(NB_TENANT), "19:other", "1700"), ca.SUPPRESS, lambda c: c) is None
    assert ca.begin((uuid.UUID(NB_TENANT), "19:ops", "9999"), ca.SUPPRESS, lambda c: c) is None


def test_a_sent_finding_card_is_returned_with_its_row():
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    ip = SimpleNamespace(tenant_id=NB_TENANT)
    sent = {"status": "success", "team_id": "group-1", "channel_id": "19:ops", "message_ts": "1700"}
    with mock.patch.object(MessageService, "_send_teams_template_as_bot", mock.AsyncMock(return_value=sent)):
        response, row = asyncio.run(service._send_finding_to_teams(None, NB_TENANT, ip, FINDING, "fp-1"))
    assert response is sent
    assert (row.teams_message_id, row.fingerprint, row.teams_channel_id) == ("1700", "fp-1", "19:ops")
    assert ca.has_actions(json.loads(row.teams_metadata)["card"])

    # Without buttons the card is still stored, so a reply in its thread can read it.
    settings.ms_teams.action_signing_key = ""
    with mock.patch.object(MessageService, "_send_teams_template_as_bot", mock.AsyncMock(return_value=sent)):
        _, row = asyncio.run(service._send_finding_to_teams(None, NB_TENANT, ip, FINDING, "fp-1"))
    assert row.teams_message_id == "1700"
    assert not ca.has_actions(json.loads(row.teams_metadata)["card"])

    failed = {"status": "failed", "reason": "x"}
    with mock.patch.object(MessageService, "_send_teams_template_as_bot", mock.AsyncMock(return_value=failed)):
        assert asyncio.run(service._send_finding_to_teams(None, NB_TENANT, ip, FINDING, "fp-1"))[1] is None


# ------------------------------- Suppress -------------------------------


def _click(store_card=True):
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    choice = card["actions"][-1]["card"]["body"][0]["choices"][1]["value"]  # this alert, 4 hours
    return SimpleNamespace(
        value={ca.ACTION_KEY: ca.SUPPRESS, ca.SUPPRESS_CHOICE: choice},
        reply_to_id="1700",
        service_url=SERVICE_URL,
        conversation=SimpleNamespace(id="19:ops;messageid=1700"),
        from_property=SimpleNamespace(name="Dana"),
    )


def _run(activity, *, tenant=NB_TENANT, user=("user-1", [NB_TENANT]), allowed=True, created=True, claimed=True):
    common = SimpleNamespace(teams_reply=mock.AsyncMock())
    sender = mock.Mock()
    with (
        mock.patch.object(ca, "_tenant_for", return_value=uuid.UUID(tenant) if tenant else None),
        mock.patch.object(actions_module, "validate_and_get_user_tenants", return_value=user),
        mock.patch.object(actions_common, "can_manage_triage_rules", return_value=allowed) as permission,
        mock.patch.object(actions_common, "create_suppress_rule", return_value=created) as create,
        mock.patch.object(ca.Cache, "claim_card_action", return_value=claimed) as claim,
        mock.patch.object(ca.Cache, "release_card_action") as release,
        mock.patch.object(ca, "get_sender", return_value=sender),
    ):
        asyncio.run(ca.handle(common, activity, "dana@example.com", "aad-tenant"))
    replies = [c.args[1] for c in common.teams_reply.call_args_list]
    return SimpleNamespace(
        replies=replies, sender=sender, permission=permission, create=create, claim=claim, release=release
    )


def test_an_admin_suppresses_the_alert_and_the_card_says_so(store):
    result = _run(_click())
    rule_input = result.create.call_args.args[2]
    assert result.create.call_args.args[:2] == (NB_TENANT, "user-1")
    assert rule_input["match_fingerprint"] == "fp-1" and rule_input["cloud_account_id"] == ACCOUNT
    assert rule_input["name"] == "Suppressed via Microsoft Teams by dana@example.com"
    assert result.permission.call_args.args == ("user-1", NB_TENANT, ACCOUNT)

    updates = [c.args for c in result.sender.update.call_args_list]
    assert [u[:3] for u in updates] == [(SERVICE_URL, "19:ops;messageid=1700", "1700")] * 2
    first, last = (u[3].attachments[0].content for u in updates)
    assert first["body"][-1]["text"] == "Suppressing…"
    assert last["body"][-1]["text"].startswith("Suppressed (this alert · 4h) by Dana · until ")
    assert ca.SUPPRESS_ACTION_ID not in _ids(last), "the menu is gone once it's done"
    assert "Open Triage Rules" in result.replies[0]
    assert store.stored()["actions"][ca.SUPPRESS]["state"] == ca.DONE
    assert result.claim.call_args.args[0] == f"{NB_TENANT}:1700:{ca.SUPPRESS}"


def test_a_second_click_after_suppressing_says_so_and_does_nothing(store):
    _run(_click())
    again = _run(_click())
    again.create.assert_not_called()
    assert again.replies == [ca.ALREADY_SUPPRESSED]


def test_a_click_while_the_first_still_runs_says_so(store):
    ca.begin(CARD, ca.SUPPRESS, lambda c: ca.with_status(c, "Suppressing…"))
    again = _run(_click())
    again.create.assert_not_called()
    assert again.replies == [ca.STILL_SUPPRESSING]


def test_a_click_that_doesnt_name_its_card_is_refused_and_touches_nothing(store):
    activity = _click()
    activity.reply_to_id = None
    result = _run(activity)
    result.create.assert_not_called()
    result.claim.assert_not_called()
    assert result.replies == [actions_common.UNABLE_TO_PROCESS_REQUEST]
    assert store.stored()["actions"] == {}


def test_someone_who_isnt_an_admin_is_told_why(store):
    result = _run(_click(), allowed=False)
    result.create.assert_not_called()
    assert result.replies == [actions_common.SUPPRESS_NOT_ALLOWED_MESSAGE]
    result.sender.update.assert_not_called()


def test_someone_outside_the_workspace_is_refused(store):
    result = _run(_click(), user=("user-9", [OTHER_TENANT]))
    result.permission.assert_not_called()
    assert result.replies == [actions_common.USER_NOT_FOUND_MESSAGE]


def test_a_click_from_an_unconnected_or_other_microsoft_tenant_is_refused(store):
    for tenant in (None, OTHER_TENANT):
        result = _run(_click(), tenant=tenant)
        result.create.assert_not_called()
        assert result.replies == [actions_common.UNABLE_TO_PROCESS_REQUEST]


def test_no_choice_asks_for_one(store):
    activity = _click()
    activity.value.pop(ca.SUPPRESS_CHOICE)
    assert _run(activity).replies == [ca.PICK_A_DURATION]


def test_a_failed_suppression_clears_the_card_and_can_be_retried(store):
    result = _run(_click(), created=False)
    assert result.replies == [actions_common.UNABLE_TO_PROCESS_REQUEST]
    result.release.assert_called_once_with(f"{NB_TENANT}:1700:{ca.SUPPRESS}")
    last = result.sender.update.call_args_list[-1].args[3].attachments[0].content
    assert all(b.get("id") != ca.STATUS_ID for b in last["body"])
    assert store.stored()["actions"] == {}
    assert _run(_click()).create.called


def test_a_card_without_a_stored_copy_still_suppresses_and_replies(store, monkeypatch):
    from sqlalchemy.orm import Session

    from notifications_server.models.models import SentNotifications

    with Session(store.engine) as session:
        session.query(SentNotifications).delete()
        session.commit()
    result = _run(_click())
    result.create.assert_called_once()
    result.sender.update.assert_not_called()
    assert "Open Triage Rules" in result.replies[0]


def test_a_failed_card_update_still_reports_in_the_thread(store):
    from notifications_server.services.ms_teams.bot_sender import TeamsBotError

    common = SimpleNamespace(teams_reply=mock.AsyncMock())
    sender = mock.Mock()
    sender.update.side_effect = TeamsBotError("Microsoft Teams returned 404", status=404)
    with (
        mock.patch.object(ca, "_tenant_for", return_value=uuid.UUID(NB_TENANT)),
        mock.patch.object(actions_module, "validate_and_get_user_tenants", return_value=("user-1", [NB_TENANT])),
        mock.patch.object(actions_common, "can_manage_triage_rules", return_value=True),
        mock.patch.object(actions_common, "create_suppress_rule", return_value=True),
        mock.patch.object(ca.Cache, "claim_card_action", return_value=True),
        mock.patch.object(ca.Cache, "release_card_action"),
        mock.patch.object(ca, "get_sender", return_value=sender),
    ):
        asyncio.run(ca.handle(common, _click(), "dana@example.com", "aad-tenant"))
    assert "Open Triage Rules" in common.teams_reply.call_args.args[1]


# ------------------------------- routing -------------------------------


def test_a_button_click_is_routed_to_the_card_action_not_the_empty_message_reply():
    events = events_module.Events.__new__(events_module.Events)
    events.common_service = SimpleNamespace(get_teams_user_info=mock.AsyncMock(return_value="dana@example.com"))
    activity = SimpleNamespace(
        conversation=SimpleNamespace(id="19:ops;messageid=1700"),
        from_property=SimpleNamespace(aad_object_id="user-1", role="user"),
        text=None,
        id="1701",
        channel_data={"tenant": {"id": "aad-tenant"}},
        value={ca.ACTION_KEY: ca.SUPPRESS},
    )
    with (
        mock.patch.object(ca, "handle", mock.AsyncMock()) as handle,
        mock.patch.object(events_module.Events, "_reply_empty_message", mock.AsyncMock()) as empty,
    ):
        asyncio.run(events.handle_message_event(activity))
    handle.assert_awaited_once_with(events.common_service, activity, "dana@example.com", "aad-tenant")
    empty.assert_not_awaited()


# ------------------------------- review follow-ups -------------------------------


def test_a_card_too_large_for_the_menu_is_still_sent_without_it():
    finding = {**FINDING, "evidences": [{"type": "markdown", "data": "log line " * 120} for _ in range(23)]}
    card = get_ms_teams_finding_message_template(finding, NB_TENANT)
    assert not ca.has_actions(card), "no room for the menu"
    assert card["actions"][0]["title"] == "View", "the alert itself is still built"
    assert ca.has_actions(get_ms_teams_finding_message_template(FINDING, NB_TENANT))


def test_a_click_on_a_card_older_than_its_buttons_says_so(store):
    with mock.patch.object(ca.time, "time", return_value=time.time() - ca.MAX_AGE_SECONDS - 3600):
        activity = _click()
    result = _run(activity)
    result.create.assert_not_called()
    assert result.replies == [ca.SUPPRESS_CARD_TOO_OLD]


def test_a_click_while_another_holds_the_claim_says_its_running(store):
    result = _run(_click(), claimed=False)
    result.create.assert_not_called()
    assert result.replies == [ca.STILL_SUPPRESSING]


def test_the_claim_is_released_however_the_action_ends(store):
    assert _run(_click()).release.call_count == 1, "after success"
    assert _run(_click(), created=False).release.call_count == 1, "after a failed rule"


def test_an_unexpected_error_replies_and_releases_the_claim(store):
    with mock.patch.object(ca, "begin", side_effect=RuntimeError("db down")):
        result = _run(_click())
    assert result.replies == [actions_common.UNABLE_TO_PROCESS_REQUEST]
    result.release.assert_called_once()


# ------------------------------- approvals -------------------------------


def _approval_params(options=("approve", "reject"), token="run-1:task-2"):
    from notifications_server.message_templates.slack.generic import get_generic_message_params

    return get_generic_message_params(message="Restart the API?", approval_token=token, approval_options=list(options))


def _approval_card(options=("approve", "reject")):
    from notifications_server.message_templates.ms_teams.generic import get_ms_teams_generic_message_template

    params = _approval_params(options)
    return ca.with_approval_buttons(get_ms_teams_generic_message_template(params), params, NB_TENANT)


def test_an_approval_card_gets_one_signed_button_per_option():
    card = _approval_card()
    buttons = [a for a in card["actions"] if a["id"].startswith(ca.APPROVAL_ID)]
    assert [b["title"] for b in buttons] == ["Approve", "Reject"]
    signed = [ca.verify(b["data"][ca.APPROVAL_DATA], ca.APPROVE, NB_TENANT) for b in buttons]
    assert [(p["token"], p["status"]) for p in signed] == [("run-1:task-2", "approve"), ("run-1:task-2", "reject")]
    assert ca.has_actions(card)
    assert not ca.has_actions(ca.without_approval(card))


def test_more_than_three_options_become_a_choice_set():
    card = _approval_card(("approve", "reject", "defer", "escalate"))
    (choice_set,) = [b for b in card["body"] if b.get("id") == ca.APPROVAL_CHOICE]
    assert len(choice_set["choices"]) == 4
    assert [a["title"] for a in card["actions"]] == ["Submit"]
    cleared = ca.without_approval(card)
    assert not ca.has_actions(cleared) and all(b.get("id") != ca.APPROVAL_CHOICE for b in cleared["body"])


def test_cards_without_a_token_options_or_key_get_no_approval_buttons():
    from notifications_server.message_templates.ms_teams.generic import get_ms_teams_generic_message_template
    from notifications_server.message_templates.slack.generic import get_generic_message_params

    plain = get_generic_message_params(message="FYI")
    card = get_ms_teams_generic_message_template(plain)
    assert ca.with_approval_buttons(card, plain, NB_TENANT) == card
    no_options = _approval_params(options=())
    assert not ca.has_actions(ca.with_approval_buttons(card, no_options, NB_TENANT))
    settings.ms_teams.action_signing_key = ""
    assert not ca.has_actions(_approval_card())


def test_a_template_send_with_approval_buttons_stores_its_card():
    from notifications_server.message_templates.ms_teams.generic import get_ms_teams_generic_message_template
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    session = mock.Mock(info={})
    ip = SimpleNamespace(tenant_id=NB_TENANT)
    sent = {"status": "success", "team_id": "group-1", "channel_id": "19:ops", "message_ts": "1800"}
    with mock.patch.object(MessageService, "_send_teams_template_as_bot", mock.AsyncMock(return_value=sent)) as send:
        response = asyncio.run(
            service.send_teams_template_notification(
                ip, get_ms_teams_generic_message_template, _approval_params(), session, NB_TENANT
            )
        )
    assert response is sent
    assert ca.has_actions(send.call_args.args[2]), "the buttons are on the card that was sent"
    (row,) = [c.args[0] for c in session.add.call_args_list]
    assert (row.teams_message_id, row.teams_channel_id, row.fingerprint) == ("1800", "19:ops", "ms_teams:1800")
    assert session.info[message_module.STORED_TEAMS_CARDS] is True


def test_a_template_send_without_buttons_stores_nothing():
    from notifications_server.message_templates.ms_teams.generic import get_ms_teams_generic_message_template
    from notifications_server.message_templates.slack.generic import get_generic_message_params
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    session = mock.Mock(info={})
    sent = {"status": "success", "channel_id": "19:ops", "message_ts": "1800"}
    with mock.patch.object(MessageService, "_send_teams_template_as_bot", mock.AsyncMock(return_value=sent)):
        asyncio.run(
            service.send_teams_template_notification(
                SimpleNamespace(tenant_id=NB_TENANT),
                get_ms_teams_generic_message_template,
                get_generic_message_params(message="FYI"),
                session,
                NB_TENANT,
            )
        )
    session.add.assert_not_called()
    assert session.info == {}


def test_the_template_branch_commits_stored_cards():
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    session = SimpleNamespace(info={message_module.STORED_TEAMS_CARDS: True}, commit=mock.AsyncMock())
    with mock.patch.object(MessageService, "send_template_notification", mock.AsyncMock(return_value=[{"ok": 1}])):
        result = asyncio.run(service.trigger_notification(session, "fp", [], {}, {}, [], NB_TENANT, "generic"))
    assert result == [{"ok": 1}]
    session.commit.assert_awaited_once()
    assert message_module.STORED_TEAMS_CARDS not in session.info

    untouched = SimpleNamespace(info={}, commit=mock.AsyncMock())
    with mock.patch.object(MessageService, "send_template_notification", mock.AsyncMock(return_value=[])):
        asyncio.run(service.trigger_notification(untouched, "fp", [], {}, {}, [], NB_TENANT, "generic"))
    untouched.commit.assert_not_awaited()


def test_a_threaded_approval_stores_its_card():
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    session = SimpleNamespace(add=mock.Mock(), commit=mock.AsyncMock())
    with mock.patch.object(MessageService, "_post_teams_card_as_bot", mock.AsyncMock(return_value="1900")):
        response = asyncio.run(
            service._send_teams_threaded_reply(
                session, SimpleNamespace(tenant_id=NB_TENANT), "group-1", "19:ops", "1700", _approval_card()
            )
        )
    assert response[0]["status"] == "success"
    (row,) = [c.args[0] for c in session.add.call_args_list]
    assert (row.teams_message_id, row.teams_channel_id) == ("1900", "19:ops")
    session.commit.assert_awaited_once()


def test_a_threaded_approval_that_cant_be_stored_is_still_a_successful_send():
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    session = SimpleNamespace(
        add=mock.Mock(), commit=mock.AsyncMock(side_effect=RuntimeError("db down")), rollback=mock.AsyncMock()
    )
    with mock.patch.object(MessageService, "_post_teams_card_as_bot", mock.AsyncMock(return_value="1900")):
        response = asyncio.run(
            service._send_teams_threaded_reply(
                session, SimpleNamespace(tenant_id=NB_TENANT), "group-1", "19:ops", "1700", _approval_card()
            )
        )
    assert response[0]["status"] == "success" and response[0]["message_ts"] == "1900"
    session.rollback.assert_awaited_once()


@pytest.fixture
def approval_store(store):
    from sqlalchemy.orm import Session

    from notifications_server.models.models import SentNotifications

    card = _approval_card()
    row = ca.card_row(uuid.UUID(NB_TENANT), None, None, "19:ops", "1800", card, "approval")
    row.id = uuid.uuid4()
    with Session(store.engine) as session:
        session.add(row)
        session.commit()

    def stored():
        with Session(store.engine) as session:
            row = session.query(SentNotifications).filter(SentNotifications.teams_message_id == "1800").one()
            return json.loads(row.teams_metadata)

    return SimpleNamespace(card=card, stored=stored)


def _approval_click(card, option=0):
    button = [a for a in card["actions"] if a["id"].startswith(ca.APPROVAL_ID)][option]
    return SimpleNamespace(
        value=dict(button["data"]),
        reply_to_id="1800",
        service_url=SERVICE_URL,
        conversation=SimpleNamespace(id="19:ops;messageid=1800"),
        from_property=SimpleNamespace(name="Dana"),
    )


def _run_approval(activity, *, status_code=200, user=("user-1", [NB_TENANT]), claimed=True):
    common = SimpleNamespace(teams_reply=mock.AsyncMock())
    sender = mock.Mock()
    with (
        mock.patch.object(ca, "_tenant_for", return_value=uuid.UUID(NB_TENANT)),
        mock.patch.object(actions_module, "validate_and_get_user_tenants", return_value=user),
        mock.patch.object(ca, "_post_approval", return_value=status_code) as post,
        mock.patch.object(ca.Cache, "claim_card_action", return_value=claimed),
        mock.patch.object(ca.Cache, "release_card_action") as release,
        mock.patch.object(ca, "get_sender", return_value=sender),
    ):
        asyncio.run(ca.handle(common, activity, "dana@example.com", "aad-tenant"))
    replies = [c.args[1] for c in common.teams_reply.call_args_list]
    return SimpleNamespace(replies=replies, post=post, sender=sender, release=release)


def test_approving_records_the_decision_and_stamps_the_card(approval_store):
    result = _run_approval(_approval_click(approval_store.card))
    assert result.post.call_args.args == ("run-1:task-2", "approve")
    last = result.sender.update.call_args_list[-1].args[3].attachments[0].content
    assert last["body"][-1]["text"] == "Dana chose **Approve**"
    assert not ca.has_actions(last), "the buttons are gone once it's answered"
    assert result.replies == ["Dana chose **Approve**"]
    assert approval_store.stored()["actions"][ca.APPROVE]["state"] == ca.DONE


def test_a_second_decision_on_the_same_card_is_refused(approval_store):
    _run_approval(_approval_click(approval_store.card))
    again = _run_approval(_approval_click(approval_store.card, option=1))
    again.post.assert_not_called()
    assert again.replies == [ca.APPROVAL_ANSWERED]


def test_an_answered_or_expired_approval_is_shown_as_such(approval_store):
    answered = _run_approval(_approval_click(approval_store.card), status_code=409)
    assert answered.replies == [ca.APPROVAL_ANSWERED]


def test_an_expired_approval_is_shown_as_expired(approval_store):
    expired = _run_approval(_approval_click(approval_store.card), status_code=404)
    assert expired.replies == [ca.APPROVAL_EXPIRED]
    assert not ca.has_actions(approval_store.stored()["card"])


def test_a_failed_decision_can_be_retried(approval_store):
    for status_code in (500, None):
        result = _run_approval(_approval_click(approval_store.card), status_code=status_code)
        assert result.replies == [ca.APPROVAL_FAILED]
        result.release.assert_called_once()
        assert approval_store.stored()["actions"] == {}
        assert ca.has_actions(approval_store.stored()["card"])


def test_someone_outside_the_workspace_cannot_decide(approval_store):
    result = _run_approval(_approval_click(approval_store.card), user=("user-9", [OTHER_TENANT]))
    result.post.assert_not_called()
    assert result.replies == [actions_common.USER_NOT_FOUND_MESSAGE]


def test_tampered_approval_data_is_refused(approval_store):
    activity = _approval_click(approval_store.card)
    forged = json.loads(activity.value[ca.APPROVAL_DATA])
    forged["body"]["action_params"]["token"] = "someone-elses-run:task"
    activity.value[ca.APPROVAL_DATA] = json.dumps(forged)
    result = _run_approval(activity)
    result.post.assert_not_called()
    assert result.replies == [actions_common.UNABLE_TO_PROCESS_REQUEST]


def test_a_choice_set_approval_reads_the_picked_option(approval_store):
    card = _approval_card(("approve", "reject", "defer", "escalate"))
    (choice_set,) = [b for b in card["body"] if b.get("id") == ca.APPROVAL_CHOICE]
    activity = _approval_click(card)
    activity.value = {ca.ACTION_KEY: ca.APPROVE, ca.APPROVAL_CHOICE: choice_set["choices"][2]["value"]}
    result = _run_approval(activity)
    assert result.post.call_args.args == ("run-1:task-2", "defer")


def test_an_approval_prompt_too_large_for_its_buttons_is_still_sent():
    from notifications_server.message_templates.ms_teams.generic import get_ms_teams_generic_message_template
    from notifications_server.message_templates.slack.generic import get_generic_message_params

    params = get_generic_message_params(
        message="details " * 3500, approval_token="run-1:task-2", approval_options=["approve", "reject"]
    )
    card = get_ms_teams_generic_message_template(params)
    assert ca.with_approval_buttons(card, params, NB_TENANT) == card


def test_a_decision_on_a_card_older_than_its_buttons_says_so(approval_store):
    with mock.patch.object(ca.time, "time", return_value=time.time() - ca.MAX_AGE_SECONDS - 3600):
        activity = _approval_click(_approval_card())
    result = _run_approval(activity)
    result.post.assert_not_called()
    assert result.replies == [ca.APPROVAL_CARD_TOO_OLD]


def test_a_decision_while_another_is_being_recorded_says_so(approval_store):
    result = _run_approval(_approval_click(approval_store.card), claimed=False)
    result.post.assert_not_called()
    assert result.replies == [ca.STILL_RECORDING]


def test_a_decision_releases_its_claim_and_an_unexpected_error_replies(approval_store):
    assert _run_approval(_approval_click(approval_store.card)).release.call_count == 1
    with mock.patch.object(ca, "begin", side_effect=RuntimeError("db down")):
        failed = _run_approval(_approval_click(approval_store.card))
    assert failed.replies == [ca.APPROVAL_FAILED]
    failed.release.assert_called_once()


def test_the_approval_token_is_escaped_in_the_url(monkeypatch):
    monkeypatch.setattr(settings.services, "workflow_server", "http://workflow-server/")
    with mock.patch.object(ca.requests, "post", return_value=SimpleNamespace(status_code=200)) as post:
        assert ca._post_approval("run-1:task-2", "approve") == 200
        ca._post_approval("run/../x?y#z w", "approve")
    urls = [c.args[0] for c in post.call_args_list]
    assert urls == [
        "http://workflow-server/approvals/run-1:task-2",
        "http://workflow-server/approvals/run%2F..%2Fx%3Fy%23z%20w",
    ]
    assert post.call_args.kwargs["json"] == {"status": "approve"}


# ------------------------------- Ask Nubi -------------------------------


def test_a_bot_finding_card_offers_ask_nubi_with_signed_data():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    assert _ids(card)[1:] == [ca.ASK_NUBI_ID, ca.SUPPRESS_ACTION_ID]
    ask = card["actions"][1]
    params = ca.verify(ask["data"][ca.ASK_NUBI_DATA], ca.ASK_NUBI, NB_TENANT)
    assert (params["event_id"], params["account_id"]) == ("event-1", ACCOUNT)
    assert ask["data"][ca.ACTION_KEY] == ca.ASK_NUBI


def test_a_tight_card_drops_ask_nubi_before_the_suppress_menu():
    with mock.patch.object(ca, "fits", side_effect=lambda card: ca.ASK_NUBI_ID not in _ids(card)):
        card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    assert _ids(card)[1:] == [ca.SUPPRESS_ACTION_ID]


def test_analysis_and_suppress_lines_never_erase_each_other():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    one = ca.with_analysis_status(ca.with_status(card, "Suppressed"), "Analyzed")
    two = ca.with_status(ca.with_analysis_status(card, "Analyzed"), "Suppressed")
    for stamped in (one, two):
        texts = {b.get("id"): b.get("text") for b in stamped["body"]}
        assert texts[ca.STATUS_ID] == "Suppressed" and texts[ca.ANALYSIS_STATUS_ID] == "Analyzed"


def _ask_click():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    return SimpleNamespace(
        value=dict(card["actions"][1]["data"]),
        reply_to_id="1700",
        service_url=SERVICE_URL,
        conversation=SimpleNamespace(id="19:ops;messageid=1700"),
        from_property=SimpleNamespace(name="Dana"),
    )


def _run_ask(activity, *, user=("user-1", [NB_TENANT]), claimed=True):
    common = SimpleNamespace(teams_reply=mock.AsyncMock())
    sender = mock.Mock()
    with (
        mock.patch.object(ca, "_tenant_for", return_value=uuid.UUID(NB_TENANT)),
        mock.patch.object(actions_module, "validate_and_get_user_tenants", return_value=user),
        mock.patch.object(ca.Cache, "claim_teams_analysis", return_value=claimed),
        mock.patch.object(ca.Cache, "release_teams_analysis") as release,
        mock.patch.object(ca, "get_sender", return_value=sender),
        mock.patch.object(ca, "_start_analysis") as thread,
    ):
        asyncio.run(ca.handle(common, activity, "dana@example.com", "aad-tenant"))
    replies = [c.args[1] for c in common.teams_reply.call_args_list]
    return SimpleNamespace(replies=replies, sender=sender, thread=thread, release=release)


def test_ask_nubi_stamps_the_card_and_starts_the_analysis(store):
    result = _run_ask(_ask_click())
    (job,) = result.thread.call_args.args
    assert (job["event_id"], job["account_id"], job["user_id"]) == ("event-1", ACCOUNT, "user-1")
    shown = result.sender.update.call_args.args[3].attachments[0].content
    analysis = [b for b in shown["body"] if b.get("id") == ca.ANALYSIS_STATUS_ID]
    assert analysis[0]["text"].startswith("Analyzing")
    assert store.stored()["actions"] == {}, "Ask Nubi records nothing that refuses a click"
    result.release.assert_not_called(), "the worker owns the claim now"
    with ca._analyses_lock:
        ca._active_analyses = 0


def test_ask_nubi_while_an_analysis_runs_says_so(store):
    result = _run_ask(_ask_click(), claimed=False)
    result.thread.assert_not_called()
    assert result.replies == [ca.STILL_ANALYZING]


def test_ask_nubi_when_too_many_run_says_so_and_frees_the_event(store, monkeypatch):
    monkeypatch.setattr(settings.slack, "event_analysis_max_pollers", 0)
    result = _run_ask(_ask_click())
    result.thread.assert_not_called()
    assert result.replies == [ca.ANALYSIS_BUSY]
    assert result.release.call_args.args[0] == "event-1"


def test_ask_nubi_from_outside_the_workspace_or_with_forged_data_is_refused(store):
    outsider = _run_ask(_ask_click(), user=("user-9", [OTHER_TENANT]))
    assert outsider.replies == [actions_common.USER_NOT_FOUND_MESSAGE]
    forged = _ask_click()
    data = json.loads(forged.value[ca.ASK_NUBI_DATA])
    data["body"]["action_params"]["event_id"] = "someone-elses-event"
    forged.value[ca.ASK_NUBI_DATA] = json.dumps(data)
    assert _run_ask(forged).replies == [actions_common.UNABLE_TO_PROCESS_REQUEST]
    with mock.patch.object(ca.time, "time", return_value=time.time() - ca.MAX_AGE_SECONDS - 3600):
        old = _ask_click()
    assert _run_ask(old).replies == [ca.ASK_NUBI_CARD_TOO_OLD]


def _job():
    return {
        "activity": _ask_click(),
        "aad_tenant_id": "aad-tenant",
        "tenant_id": NB_TENANT,
        "user_id": "user-1",
        "event_id": "event-1",
        "account_id": ACCOUNT,
        "card_ref": (uuid.UUID(NB_TENANT), "19:ops", "1700"),
        "token": "worker-1",
        "deadline": time.monotonic() + 60,
    }


def _analyze(results, replies=None):
    from notifications_server.services import common as common_module

    posted = []

    async def teams_reply(activity, text, aad):
        posted.append(text)
        return True if replies is None else replies.pop(0)

    fake_service = SimpleNamespace(teams_reply=teams_reply, close=mock.Mock())
    stamps = []
    with (
        mock.patch.object(common_module, "CommonService", return_value=fake_service),
        mock.patch.object(events_module.Events, "call_event_analysis_api", side_effect=results) as call,
        mock.patch.object(ca, "_stamp_analysis", side_effect=lambda ref, text: stamps.append(text) or None),
        mock.patch.object(ca.Cache, "refresh_teams_analysis"),
        mock.patch.object(ca.asyncio, "sleep", mock.AsyncMock()),
    ):
        asyncio.run(ca._analyze(_job()))
    fake_service.close.assert_called_once()
    _analyze.timeouts = [c.args[4] for c in call.call_args_list]
    return posted, stamps


def _result(status="IN_PROGRESS", **tasks):
    return {
        "status": status,
        "task_statuses": {k: "COMPLETED" for k in tasks},
        "summary": tasks.get("summary"),
        "investigation": tasks.get("investigation"),
        "detailed_response": tasks.get("detailed_response"),
    }


def test_the_analysis_posts_each_section_once_then_marks_the_card():
    posted, stamps = _analyze(
        [
            _result(summary="Pods restart on OOM"),
            _result(summary="Pods restart on OOM", investigation="Memory limit too low"),
            _result(
                "COMPLETED",
                summary="Pods restart on OOM",
                investigation="Memory limit too low",
                detailed_response="Memory limit too low\n\nRaise it to 512Mi.",
            ),
        ]
    )
    assert [p.split("\n\n")[0] for p in posted] == [
        "Here's a quick summary while I keep digging:",
        "Here's what I'm finding on the likely root cause:",
        "Here's the full write-up:",
    ]
    assert posted[2].endswith("Raise it to 512Mi."), "only what the write-up adds"
    assert stamps == ["Analyzed by Nubi. See the thread."]


def test_asking_again_after_it_finished_posts_only_the_write_up():
    posted, stamps = _analyze([_result("COMPLETED", summary="s", investigation="i", detailed_response="i\n\nmore")])
    assert posted == ["Here's the full write-up:\n\nmore"]
    assert stamps == ["Analyzed by Nubi. See the thread."]


def test_a_failed_post_is_retried_before_the_card_is_marked():
    done = _result("COMPLETED", detailed_response="the write-up")
    posted, stamps = _analyze([done, done], replies=[False, True])
    assert len(posted) == 2 and stamps == ["Analyzed by Nubi. See the thread."]


def test_a_failed_analysis_says_so_and_clears_the_card():
    posted, stamps = _analyze([_result("FAILED")])
    assert posted == [ca.ANALYSIS_FAILED]
    assert stamps == [None]


def test_an_analysis_that_cant_be_reached_stops_and_points_to_nudgebee():
    posted, stamps = _analyze([None, RuntimeError("down"), None, None])
    assert posted == []
    assert stamps == ["Still analyzing. Check the event in NudgeBee."]


def test_long_analysis_text_is_cut_by_size_with_a_link():
    text = ca._cut("x" * (ca.WRITEUP_LIMIT + 500), "event-1")
    assert len(text.encode()) < ca.WRITEUP_LIMIT + 200 and "investigate?id=event-1" in text
    wide = ca._cut("é" * ca.WRITEUP_LIMIT, "event-1")
    assert len(wide.encode()) < ca.WRITEUP_LIMIT + 200, "the limit is bytes, not characters"
    fenced = ca._cut("```\n" + "log\n" * ca.WRITEUP_LIMIT, "event-1")
    assert fenced.split("\n\n…")[0].count("```") == 2, "a cut code block is closed before the link"


def test_a_slow_start_isnt_a_failure_and_gets_a_longer_timeout():
    done = _result("COMPLETED", detailed_response="the write-up")
    posted, stamps = _analyze([None, done])
    assert stamps == ["Analyzed by Nubi. See the thread."]
    assert _analyze.timeouts == [
        ca.ANALYSIS_KICKOFF_TIMEOUT_SECONDS,
        settings.slack.event_analysis_poll_timeout_seconds,
    ]


def test_a_click_that_cant_start_the_worker_clears_the_card(store):
    with mock.patch.object(ca, "_start_analysis", side_effect=RuntimeError("no threads")):
        with pytest.raises(RuntimeError):
            _run_ask_raw(_ask_click())
    assert all(b.get("id") != ca.ANALYSIS_STATUS_ID for b in store.stored()["card"]["body"])
    assert ca._active_analyses == 0


def _run_ask_raw(activity):
    common = SimpleNamespace(teams_reply=mock.AsyncMock())
    with (
        mock.patch.object(ca, "_tenant_for", return_value=uuid.UUID(NB_TENANT)),
        mock.patch.object(actions_module, "validate_and_get_user_tenants", return_value=("user-1", [NB_TENANT])),
        mock.patch.object(ca.Cache, "claim_teams_analysis", return_value=True),
        mock.patch.object(ca.Cache, "release_teams_analysis"),
        mock.patch.object(ca, "get_sender", return_value=mock.Mock()),
    ):
        asyncio.run(ca.handle(common, activity, "dana@example.com", "aad-tenant"))


def test_a_worker_that_dies_clears_the_card_and_frees_the_event():
    with (
        mock.patch.object(ca, "_analyze", side_effect=RuntimeError("boom")),
        mock.patch.object(ca, "_clear_analysis_stamp") as clear,
        mock.patch.object(ca.Cache, "release_teams_analysis") as release,
    ):
        with ca._analyses_lock:
            ca._active_analyses = 1
        ca._run_analysis(_job())
    clear.assert_called_once()
    assert release.call_args.args == ("event-1", "worker-1")
    assert ca._active_analyses == 0


def test_teams_analysis_claims_dont_touch_slacks():
    from notifications_server.services.cache import Cache

    store = {}

    class FakeRedis:
        def set(self, key, value, nx=False, ex=None):
            if nx and key in store:
                return False
            store[key] = value
            return True

        def get(self, key):
            return store.get(key)

        def delete(self, key):
            store.pop(key, None)

    cache = Cache.__new__(Cache)
    cache.redis_client = FakeRedis()
    cache._ensure_connection = lambda: None
    teams_key = "event_analysis_poll:ms_teams:event-1"
    cache.claim_event_analysis_poller("event-1", 60)
    assert "event_analysis_poll:event-1" in store, "Slack's key is unchanged"
    assert cache.claim_teams_analysis("event-1", 50, "worker-1")
    assert not cache.claim_teams_analysis("event-1", 50, "worker-2")
    store.pop(teams_key)  # the claim lapsed
    cache.refresh_teams_analysis("event-1", 50, "worker-1")
    assert store[teams_key] == "worker-1", "a live worker re-takes a lapsed claim"
    cache.release_teams_analysis("event-1", "worker-2")
    assert store[teams_key] == "worker-1", "a worker only releases its own claim"
    store[teams_key] = "worker-2"
    cache.refresh_teams_analysis("event-1", 50, "worker-1")
    assert store[teams_key] == "worker-2", "nor takes over another's"
    cache.release_teams_analysis("event-1", "worker-2")
    assert teams_key not in store
