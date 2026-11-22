"""Approve / Reject on an automation's approval card: the decision is recorded
by the workflow server as the clicking user, who must belong to the account's
tenant and be allowed to approve."""

import json
from types import SimpleNamespace
from unittest import mock

import pytest
import requests

from notifications_server.configs import settings
from notifications_server.services import workflow_approval as wa
from notifications_server.services.actions import Actions, ApprovalParams
from notifications_server.services.actions_common import SlackInteractiveActionsService
from notifications_server.services.workflow_approval import Decision

ACCOUNT = "22222222-2222-4222-8222-222222222222"
TENANT = "33333333-3333-4333-8333-333333333333"
OTHER_TENANT = "44444444-4444-4444-8444-444444444444"
TOKEN = f"6465616462656566:{ACCOUNT}:temporal-1:run-1:ask"


@pytest.fixture(autouse=True)
def service_token(monkeypatch):
    monkeypatch.setattr(settings, "workflow_server_token", "svc-token")
    monkeypatch.setattr(settings.services, "workflow_server", "http://workflow-server:8000/")


def _decide(status_code=200, user=("user-1", [TENANT]), account=True, token=TOKEN, post_error=None):
    response = SimpleNamespace(status_code=status_code, text=json.dumps({}))
    post = mock.Mock(return_value=response, side_effect=post_error)
    found = {"id": ACCOUNT, "account_name": "prod", "tenant": TENANT} if account else None
    with (
        mock.patch.object(wa, "get_account_by_id", return_value=found),
        mock.patch.object(wa, "get_user_tenants", return_value=user) as lookup,
        mock.patch.object(wa.requests, "post", post),
    ):
        decision = wa.decide(token, "approve", "dana@example.com")
    return decision, post, lookup


def test_a_decision_is_recorded_as_the_clicking_user():
    decision, post, lookup = _decide()
    assert decision is Decision.DECIDED
    lookup.assert_called_once_with(mock.ANY, "dana@example.com")
    assert post.call_args.args == ("http://workflow-server:8000/rpc",)
    assert post.call_args.kwargs["json"] == {
        "action": {"name": "workflow_complete_approval"},
        "input": {"input": {"account_id": ACCOUNT, "token": TOKEN, "status": "approve"}},
    }
    assert post.call_args.kwargs["headers"] == {
        "X-ACTION-TOKEN": "svc-token",
        "x-tenant-id": TENANT,
        "x-user-id": "user-1",
    }


@pytest.mark.parametrize("user", [(None, None), ("user-9", [OTHER_TENANT])])
def test_someone_outside_the_accounts_tenant_cannot_decide(user):
    decision, post, _ = _decide(user=user)
    assert decision is Decision.NOT_A_USER
    post.assert_not_called()


def test_an_account_that_cant_be_found_can_be_retried():
    decision, post, _ = _decide(account=False)
    assert decision is Decision.FAILED
    post.assert_not_called()


@pytest.mark.parametrize("token", [None, "", "abcd", "abcd:acct:temporal-1:run-1", "abcd::temporal-1:run-1:ask"])
def test_a_malformed_token_is_invalid(token):
    decision, post, _ = _decide(token=token)
    assert decision is Decision.INVALID
    post.assert_not_called()


def test_no_service_token_fails_without_calling(monkeypatch):
    monkeypatch.setattr(settings, "workflow_server_token", "")
    decision, post, _ = _decide()
    assert decision is Decision.FAILED
    post.assert_not_called()


@pytest.mark.parametrize(
    "status_code, expected",
    [
        (409, Decision.ANSWERED),
        (404, Decision.NOT_FOUND),
        (403, Decision.NO_ACCESS),
        (401, Decision.FAILED),
        (400, Decision.INVALID),
        (500, Decision.FAILED),
    ],
)
def test_the_workflow_servers_answer_is_kept(status_code, expected):
    decision, _, _ = _decide(status_code=status_code)
    assert decision is expected


def test_an_unreachable_workflow_server_fails():
    decision, _, _ = _decide(post_error=requests.ConnectionError("down"))
    assert decision is Decision.FAILED


# ------------------------------- Slack -------------------------------


def _slack_reply(decision, status="approve"):
    service = object.__new__(Actions)
    service.common_service = mock.Mock()
    service.slack_app = mock.Mock()
    params = ApprovalParams(
        channel_id="C1",
        team_id="T1",
        action_name="approval_action",
        message_ts="1700.1",
        token=TOKEN,
        status=status,
        user_email="dana@example.com",
    )
    with mock.patch.object(wa, "decide", return_value=decision) as decide:
        service.handle_approval_action(params=params)
    blocks = service.slack_app.client.reply_in_thread.call_args.kwargs["blocks"]
    return decide, json.dumps(blocks)


def test_a_slack_decision_is_made_as_the_clicker():
    decide, reply = _slack_reply(Decision.DECIDED)
    decide.assert_called_once_with(TOKEN, "approve", "dana@example.com")
    assert "Approve" in reply


def test_a_slack_click_without_access_says_so():
    _, reply = _slack_reply(Decision.NO_ACCESS)
    assert wa.no_access_message() in reply


def test_a_slack_click_from_outside_the_tenant_says_so():
    _, reply = _slack_reply(Decision.NOT_A_USER)
    assert wa.not_a_user_message() in reply


def test_the_approval_dropdown_passes_the_clicker():
    service = object.__new__(SlackInteractiveActionsService)
    service.action_service = mock.Mock()
    data = {"message": {"ts": "1700.1", "metadata": {"event_payload": {"token": TOKEN}}}}
    action = {"selected_option": {"value": "defer"}}
    service.handle_workflow_approval_selection("C1", "T1", "dana@example.com", action, data)
    params = service.action_service.handle_approval_action.call_args.kwargs["params"]
    assert (params.token, params.status, params.user_email) == (TOKEN, "defer", "dana@example.com")
