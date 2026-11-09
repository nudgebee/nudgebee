"""Logs carry ids and sizes, never approval tokens, chat content or whole bodies."""

import asyncio
import logging
from types import SimpleNamespace
from unittest import mock

from fastapi.exceptions import RequestValidationError

from notifications_server.rabbitmq.message_consumer import Consumer
from notifications_server.services.actions import Actions, ApprovalParams
from notifications_server.services.actions_common import SlackInteractiveActionsService
from notifications_server.services.events import Events

SECRET = "approval-token-6465616462656566"
QUESTION = "why is checkout-api crashlooping in prod"
HISTORY = "alice: the db password is hunter2"


def _logged(caplog):
    return "\n".join(r.getMessage() for r in caplog.records)


def test_an_approval_with_no_status_does_not_log_the_token(caplog):
    actions = object.__new__(Actions)
    actions.common_service = mock.Mock()
    actions.slack_app = mock.Mock()
    params = ApprovalParams(
        action_name="approval",
        channel_id="C1",
        team_id="T1",
        message_ts="1700.1",
        token=SECRET,
        status="",
        user_email="dana@example.com",
    )
    with caplog.at_level(logging.INFO):
        actions.handle_approval_action(params)
    assert "token present=True" in _logged(caplog)
    assert SECRET not in _logged(caplog)


def test_a_slack_click_with_no_status_does_not_log_the_token(caplog):
    service = object.__new__(SlackInteractiveActionsService)
    service.common_service = mock.Mock()
    service.reply_with_error = mock.Mock()
    data = {"message": {"ts": "1700.1", "metadata": {"event_payload": {"token": SECRET}}}}
    with caplog.at_level(logging.INFO):
        service.handle_approval_action("C1", "T1", "U1", "dana@example.com", data, {})
    assert "token present=True" in _logged(caplog)
    assert SECRET not in _logged(caplog)


def _payload():
    return {
        "query": QUESTION,
        "session_id": "sess-1",
        "account_id": "acct-1",
        "source": "InstantNotification",
        "channel_context": HISTORY,
        "images": ["aGVsbG8=", "d29ybGQ="],
    }


def test_the_llm_request_is_logged_as_ids_and_sizes(caplog):
    with (
        mock.patch("notifications_server.services.events.requests.post") as post,
        caplog.at_level(logging.INFO),
    ):
        post.return_value.status_code = 200
        Events.query_llm_server(_payload(), {})
    logged = _logged(caplog)
    assert "session_id=sess-1" in logged
    assert f"query_len={len(QUESTION)}" in logged
    assert "images=2" in logged
    for content in (QUESTION, HISTORY, "aGVsbG8="):
        assert content not in logged


def test_the_async_llm_request_is_logged_as_ids_and_sizes(caplog):
    with (
        mock.patch("notifications_server.services.events.aiohttp.ClientSession", side_effect=RuntimeError("offline")),
        caplog.at_level(logging.INFO),
    ):
        asyncio.run(Events.async_query_llm_server(_payload(), {}))
    logged = _logged(caplog)
    assert "session_id=sess-1" in logged
    for content in (QUESTION, HISTORY, "aGVsbG8="):
        assert content not in logged


def test_a_validation_error_does_not_log_the_body_or_rejected_values():
    from notifications_server import server

    request = SimpleNamespace(method="POST", url=SimpleNamespace(path="/actions/approval"))
    exc = RequestValidationError(
        [{"type": "missing", "loc": ("body", "status"), "msg": "Field required", "input": {"token": SECRET}}]
    )
    # The server logger doesn't propagate to caplog, so read the call itself.
    with mock.patch.object(server.LOG, "error") as log_error:
        asyncio.run(server.validation_exception_handler(request, exc))
    fmt, *args = log_error.call_args.args
    logged = fmt % tuple(args)
    assert "/actions/approval" in logged
    assert "Field required" in logged
    assert SECRET not in logged


def test_a_failed_queue_message_logs_its_size_not_its_body(caplog):
    body = f'{{"token": "{SECRET}", "text": "{QUESTION}"}}'.encode()
    consumer = object.__new__(Consumer)
    consumer.consume_callback = mock.AsyncMock(side_effect=ValueError("bad message"))
    consumer._decompress_message_body = lambda raw, headers: raw

    process = mock.MagicMock()
    process.__aenter__ = mock.AsyncMock()
    process.__aexit__ = mock.AsyncMock(return_value=False)
    message = SimpleNamespace(headers={}, body=body, delivery_tag=7, process=lambda: process)

    with caplog.at_level(logging.INFO):
        asyncio.run(consumer._process_message_task(message))
    logged = _logged(caplog)
    assert f"body_len={len(body)}" in logged
    assert SECRET not in logged
    assert QUESTION not in logged
