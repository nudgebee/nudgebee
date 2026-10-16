"""Approve / Reject on an Auto Optimize approval message: the decision is made by
the workflow server's POST /autopilot/approval as the clicking user."""

import json
from types import SimpleNamespace
from unittest import mock

import pytest
import requests

from notifications_server.configs import settings
from notifications_server.services import auto_optimize_approval as aoa
from notifications_server.services.actions_common import SlackInteractiveActionsService

APPROVAL = "11111111-1111-4111-8111-111111111111"
ACCOUNT = "22222222-2222-4222-8222-222222222222"
TENANT = "33333333-3333-4333-8333-333333333333"
OTHER_TENANT = "44444444-4444-4444-8444-444444444444"
TOKEN = f"auto_optimize:{APPROVAL}:{ACCOUNT}:{TENANT}"


@pytest.fixture(autouse=True)
def service_token(monkeypatch):
    monkeypatch.setattr(settings, "workflow_server_token", "svc-token")
    monkeypatch.setattr(settings.services, "workflow_server", "http://workflow-server:8000/")


def _response(status_code, body=None):
    return SimpleNamespace(status_code=status_code, json=lambda: body, text=json.dumps(body))


def _decide(status_code=200, body=None, user=("user-1", [TENANT]), option="approve", token=TOKEN, post_error=None):
    post = mock.Mock(return_value=_response(status_code, body), side_effect=post_error)
    with (
        mock.patch.object(aoa, "get_user_tenants", return_value=user) as lookup,
        mock.patch.object(aoa.requests, "post", post),
    ):
        outcome = aoa.decide(token, option, "dana@example.com")
    return outcome, post, lookup


# ------------------------------- token -------------------------------


def test_the_token_names_the_approval_account_and_tenant():
    assert aoa.parse_token(TOKEN) == (APPROVAL, ACCOUNT, TENANT)


@pytest.mark.parametrize(
    "token",
    [
        None,
        "6465616462656566:acct-1:wf-exec-1:run-1:activity-1",  # a workflow approval token
        f"auto_optimize:{APPROVAL}:{ACCOUNT}",
        f"auto_optimize:{APPROVAL}:{ACCOUNT}:{TENANT}:extra",
        f"auto_optimize:{APPROVAL}:{ACCOUNT}:not-a-uuid",
    ],
)
def test_anything_else_is_not_an_auto_optimize_token(token):
    assert aoa.parse_token(token) is None


# ------------------------------- decide -------------------------------


def test_approving_calls_the_decision_route_as_the_clicking_user():
    outcome, post, lookup = _decide(body={"message": "Approved. Applying 2 change(s)."})
    assert outcome == aoa.Outcome("Approved. Applying 2 change(s).", True)
    lookup.assert_called_once_with(mock.ANY, "dana@example.com")
    assert post.call_args.args == ("http://workflow-server:8000/autopilot/approval",)
    assert post.call_args.kwargs["json"] == {"input": {"id": APPROVAL, "account_id": ACCOUNT, "status": "APPROVED"}}
    assert post.call_args.kwargs["headers"] == {
        "X-ACTION-TOKEN": "svc-token",
        "x-tenant-id": TENANT,
        "x-user-id": "user-1",
    }


def test_rejecting_sends_rejected():
    _, post, _ = _decide(option="reject", body={"message": "Rejected."})
    assert post.call_args.kwargs["json"]["input"]["status"] == "REJECTED"


def test_a_settled_approval_says_why_in_the_servers_words():
    expired = "This approval expired, so its changes were not applied. The next run plans afresh."
    outcome, _, _ = _decide(status_code=409, body={"message": expired})
    assert outcome == aoa.Outcome(expired, True)
    outcome, _, _ = _decide(status_code=404, body={"message": "approval not found"})
    assert outcome == aoa.Outcome(aoa.NOT_FOUND, True)


def test_a_user_without_edit_access_leaves_the_approval_open():
    for status_code in (401, 403):
        outcome, _, _ = _decide(status_code=status_code, body={"error": "unauthorized"})
        assert outcome == aoa.Outcome(aoa.NO_ACCESS, False)


@pytest.mark.parametrize("user", [(None, None), ("user-9", [OTHER_TENANT])])
def test_someone_who_is_not_a_user_of_the_tenant_cannot_decide(user):
    outcome, post, _ = _decide(user=user)
    assert outcome == aoa.Outcome(aoa.NOT_A_USER, False)
    post.assert_not_called()


def test_failures_leave_the_approval_open():
    for outcome, _, _ in (
        _decide(status_code=400, body={"message": "failed to decide approval: boom"}),
        _decide(status_code=502, body=None),
        _decide(post_error=requests.ConnectionError("down")),
    ):
        assert outcome == aoa.Outcome(aoa.FAILED, False)


def test_a_failed_user_lookup_leaves_the_approval_open():
    post = mock.Mock()
    with (
        mock.patch.object(aoa, "get_user_tenants", side_effect=RuntimeError("db down")),
        mock.patch.object(aoa.requests, "post", post),
    ):
        assert aoa.decide(TOKEN, "approve", "dana@example.com") == aoa.Outcome(aoa.FAILED, False)
    post.assert_not_called()


def test_bad_data_or_a_missing_service_token_never_calls_the_server(monkeypatch):
    for kwargs in ({"option": "needs_info"}, {"token": "auto_optimize:nope"}):
        outcome, post, _ = _decide(**kwargs)
        assert outcome == aoa.Outcome(aoa.FAILED, False)
        post.assert_not_called()
    monkeypatch.setattr(settings, "workflow_server_token", "")
    outcome, post, _ = _decide()
    assert outcome == aoa.Outcome(aoa.FAILED, False)
    post.assert_not_called()


# ------------------------------- Slack -------------------------------

_BLOCKS = [
    {"type": "header", "text": {"type": "plain_text", "text": "Action required"}},
    {"type": "section", "text": {"type": "mrkdwn", "text": "> 2 change(s) planned"}},
    {"type": "actions", "elements": [{"type": "button", "action_id": "workflow_approval_approve"}]},
    {"type": "context", "elements": [{"type": "mrkdwn", "text": "footer"}]},
]


def _slack_click(token=TOKEN, option="approve", outcome=aoa.Outcome("Approved. Applying 2 change(s).", True)):
    service = object.__new__(SlackInteractiveActionsService)
    service.common_service = mock.Mock()
    service.action_service = mock.Mock()
    data = {
        "message": {
            "ts": "1700.1",
            "blocks": list(_BLOCKS),
            "metadata": {"event_type": "workflow_approval", "event_payload": {"token": token}},
        }
    }
    with mock.patch.object(aoa, "decide", return_value=outcome) as decide:
        service.handle_approval_action("C1", "T1", "U1", "dana@example.com", data, {"status": option})
    return service, decide


def test_a_slack_click_decides_as_the_clicker_and_replaces_the_buttons():
    service, decide = _slack_click()
    decide.assert_called_once_with(TOKEN, "approve", "dana@example.com")
    service.action_service.handle_approval_action.assert_not_called()
    channel, team, ts, blocks = service.common_service.update_slack_message_with_blocks.call_args.args
    assert (channel, team, ts) == ("C1", "T1", "1700.1")
    assert [b["type"] for b in blocks] == ["header", "section", "context", "context"]
    assert blocks[-1]["elements"][0]["text"] == "<@U1>: Approved. Applying 2 change(s)."
    service.common_service.slack_reply_in_thread.assert_called_once_with(
        "C1", "T1", "1700.1", "<@U1>: Approved. Applying 2 change(s)."
    )


def test_a_slack_click_that_did_not_decide_keeps_the_buttons():
    service, _ = _slack_click(outcome=aoa.Outcome(aoa.NO_ACCESS, False))
    service.common_service.update_slack_message_with_blocks.assert_not_called()
    service.common_service.slack_reply_in_thread.assert_called_once_with("C1", "T1", "1700.1", aoa.NO_ACCESS)


def test_a_workflow_approval_click_still_goes_to_the_workflow_approval():
    service, decide = _slack_click(token="6465616462656566:acct-1:wf-exec-1:run-1:activity-1")
    decide.assert_not_called()
    params = service.action_service.handle_approval_action.call_args.kwargs["params"]
    assert params.token == "6465616462656566:acct-1:wf-exec-1:run-1:activity-1"
