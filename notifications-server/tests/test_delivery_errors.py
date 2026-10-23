"""Every send reports a result for every destination it tried. A failure says
why, from the platform's own error code or HTTP status, and whether sending
again can help; one destination's error never hides another's result."""

import asyncio
import uuid
from types import SimpleNamespace
from unittest import mock

import pytest
from slack_sdk.errors import SlackApiError
from sqlalchemy import select
from sqlalchemy.exc import OperationalError, SQLAlchemyError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from notifications_server.clients import discord_client
from notifications_server.clients.discord_client import DiscordClient
from notifications_server.services import delivery_errors as de
from notifications_server.services import message as message_module
from notifications_server.models.models import MessagingPlatform
from notifications_server.services.message import MessageService, SlackSender
from notifications_server.services.ms_teams import bot_sender as bs

# ------------------------------- classifying -------------------------------


@pytest.mark.parametrize(
    "code, reason, retryable",
    [
        ("not_in_channel", "not_in_channel", False),
        ("is_archived", "channel_not_found", False),
        ("token_revoked", "auth_failed", False),
        ("missing_scope", "permission_denied", False),
        ("invalid_blocks", "message_rejected", False),
        ("access_denied", "permission_denied", False),
        ("cannot_reply_to_message", "message_rejected", False),
        ("ratelimited", "rate_limited", True),
        ("fatal_error", "platform_unavailable", True),
    ],
)
def test_a_slack_refusal_is_classified_by_slacks_code(code, reason, retryable):
    result = de.slack_failure(SimpleNamespace(status_code=200, data={"ok": False, "error": code}))

    assert (result["reason"], result["error_code"], result["retryable"]) == (reason, code, retryable)


def test_a_slack_answer_that_is_not_slacks_is_classified_by_its_status():
    # slack_sdk's stand-in body for an HTML 503
    data = {"ok": False, "error": "Received a response in a non-JSON format: <html>Service Unavailable</html>"}

    result = de.slack_failure(SimpleNamespace(status_code=503, data=data))

    assert (result["reason"], result["error_code"], result["retryable"]) == ("platform_unavailable", "503", True)


def test_a_slack_rate_limit_keeps_slacks_code():
    result = de.slack_failure(SimpleNamespace(status_code=429, data={"ok": False, "error": "ratelimited"}))

    assert (result["reason"], result["error_code"]) == ("rate_limited", "ratelimited")


@pytest.mark.parametrize(
    "status, reason",
    [
        (None, "platform_unavailable"),
        (503, "platform_unavailable"),
        (408, "platform_unavailable"),
        (429, "rate_limited"),
        (401, "auth_failed"),
        (403, "permission_denied"),
        (404, "channel_not_found"),
        (413, "message_rejected"),
    ],
)
def test_an_http_status_has_one_reason(status, reason):
    assert de.http_reason(status) == reason


@pytest.mark.parametrize(
    "status, reason",
    [(403, "webhook_gone"), (404, "webhook_gone"), (410, "webhook_gone"), (400, "message_rejected"), (502, None)],
)
def test_a_webhook_that_answers_gone_was_removed(status, reason):
    result = de.webhook_failure("slack_webhook", SimpleNamespace(status_code=status))

    assert result["reason"] == (reason or "platform_unavailable")
    assert result["error_code"] == str(status)


def test_a_webhook_that_never_answered_may_be_retried():
    assert de.webhook_failure("ms_teams_webhook", None)["retryable"] is True


@pytest.mark.parametrize(
    "error, reason",
    [
        (bs.AppNotInTeamError(), "not_in_channel"),
        (bs.ChannelNotInTeamError(), "channel_not_found"),
        (bs.TeamsBotError(bs.CHANNEL_FORBIDDEN, status=403), "permission_denied"),
        # Teams throttles with 412 as well as 429.
        (bs.TeamsBotError("Microsoft Teams returned 412", status=412), "rate_limited"),
        (bs.TeamsBotError("Microsoft Teams returned 429", status=429), "rate_limited"),
        # A message reading like a known case is not that case.
        (bs.TeamsBotError(bs.APP_NOT_IN_TEAM), "platform_unavailable"),
        (bs.TeamsBotError("Microsoft Teams returned 503", status=503), "platform_unavailable"),
        (bs.TeamsBotError("Microsoft Teams request failed: ConnectionError"), "platform_unavailable"),
    ],
)
def test_a_teams_bot_error_is_classified(error, reason):
    assert de.teams_failure(error)["reason"] == reason


@pytest.mark.parametrize(
    "result, reason",
    [
        ({"reason": "PERMISSION_DENIED", "retryable": False}, "permission_denied"),
        ({"reason": "NOT_FOUND", "retryable": False}, "channel_not_found"),
        ({"reason": "ABORTED", "retryable": False}, "message_rejected"),
        ({"reason": "rate_limit_exceeded"}, "rate_limited"),
        # Classified by the code alone: a message that reads like a 403 is not one.
        ({"reason": "unexpected_error", "error": "403 Forbidden: PERMISSION_DENIED"}, "platform_unavailable"),
    ],
)
def test_a_google_chat_result_is_classified_by_its_code(result, reason):
    assert de.google_chat_failure(result)["reason"] == reason


@pytest.mark.parametrize(
    "result, reason",
    [
        ({"code": 10003, "status": 404}, "channel_not_found"),
        ({"code": 50013, "status": 403}, "permission_denied"),
        ({"code": 0, "status": 401}, "auth_failed"),
        ({"status": 429}, "rate_limited"),
        ({}, "platform_unavailable"),
    ],
)
def test_a_discord_result_is_classified(result, reason):
    assert de.discord_failure(result)["reason"] == reason


def test_the_discord_client_hands_back_the_status_and_discords_code():
    refused = SimpleNamespace(status_code=403, text="", json=lambda: {"code": 50013, "message": "Missing Permissions"})
    with mock.patch.object(discord_client.requests, "post", return_value=refused):
        result = DiscordClient.chat_post(token="bot", channel_id="D1", content="hi")

    assert (result["ok"], result["status"], result["code"]) == (False, 403, 50013)


@pytest.mark.parametrize(
    "ip, channel, channel_id",
    [
        (SimpleNamespace(id="s-1", platform="slack"), '{"id": "C1", "name": "ops"}', "C1"),
        (SimpleNamespace(id="s-1", platform="slack"), [{"id": "C2"}], "C2"),
        (SimpleNamespace(id="t-1", platform="ms_teams"), {"team_id": "g", "channels": [{"id": "19:a"}]}, "19:a"),
        (SimpleNamespace(id="w-1", platform="slack_webhook"), {"webhook_target_id": "w-1"}, "w-1"),
        (SimpleNamespace(id=None, platform="google_chat"), {"id": "spaces/AAA"}, "spaces/AAA"),
        (SimpleNamespace(id="d-1", platform="discord"), {"id": 1234567890}, "1234567890"),
    ],
)
def test_a_destination_is_the_installation_and_the_channel_it_posts_to(ip, channel, channel_id):
    assert de.retry_key(ip, channel) == (ip.platform, ip.id, channel_id)


# ------------------------------- senders -------------------------------


def _slack_finding_sender(monkeypatch, response):
    monkeypatch.setattr(message_module, "get_slack_finding_message", lambda *_: ("", [], [{"title": "x"}]))
    posts = []

    async def no_thread(*_args):
        return None

    def post(_tenant, _ip, thread_id, **_message):
        posts.append(thread_id)
        return response

    sender = SimpleNamespace(check_if_sent_already=no_thread, post_to_slack=post)
    fake = SimpleNamespace(slack_app=object(), slack_sender=sender)
    return MessageService._send_finding_to_slack.__get__(fake), posts


def _slack_ip(to_channel):
    return SimpleNamespace(id="s-1", platform="slack", team_id="T1", to_channel=to_channel, thread_repeats=True)


def test_a_slack_refusal_is_reported_for_its_channel(monkeypatch):
    refusal = SimpleNamespace(status_code=200, data={"ok": False, "error": "not_in_channel"})
    send, _ = _slack_finding_sender(monkeypatch, refusal)

    response, row = asyncio.run(send(None, "t1", _slack_ip([{"id": "C1"}]), {}, "fp"))

    assert response == {
        "platform": "slack",
        "status": "failed",
        "reason": "not_in_channel",
        "error_code": "not_in_channel",
        "retryable": False,
        "team_id": "T1",
        "destination_id": "s-1",
        "channel_id": "C1",
    }
    assert row is None


def test_a_slack_destination_without_a_channel_is_not_posted(monkeypatch):
    send, posts = _slack_finding_sender(monkeypatch, None)

    response, _ = asyncio.run(send(None, "t1", _slack_ip("[]"), {}, "fp"))

    assert response["reason"] == "not_configured" and posts == []


def test_post_to_slack_hands_back_slacks_refusal():
    refusal = SimpleNamespace(status_code=200, headers={}, data={"ok": False, "error": "channel_not_found"})

    def chat_post(**_kwargs):
        raise SlackApiError("channel_not_found", refusal)

    sender = SlackSender(SimpleNamespace(client=SimpleNamespace(chat_post=chat_post)), None)

    assert (
        sender.post_to_slack("t1", SimpleNamespace(to_channel={"id": "C1"}, token="xoxb"), None, text="hi") is refusal
    )


@pytest.mark.parametrize("status, reason", [(410, "webhook_gone"), (500, "platform_unavailable")])
def test_a_webhook_finding_failure_is_reported(status, reason):
    ip = SimpleNamespace(id="w-1", platform="ms_teams_webhook", webhook_url="https://hook", to_channel={})
    with (
        mock.patch.object(message_module, "get_ms_teams_finding_message_template", return_value={}),
        mock.patch.object(
            message_module.MsTeamsClient, "post_to_ms_teams_webhook", return_value=SimpleNamespace(status_code=status)
        ),
    ):
        response, _ = asyncio.run(
            MessageService.__new__(MessageService)._send_finding_to_teams_webhook(None, "t1", ip, {}, None)
        )

    assert (response["status"], response["reason"], response["destination_id"]) == ("failed", reason, "w-1")


def test_a_webhook_without_a_url_is_not_configured():
    ip = SimpleNamespace(id="w-1", platform="slack_webhook", webhook_url=None, to_channel={})

    response, _ = asyncio.run(
        MessageService.__new__(MessageService)._send_finding_to_slack_webhook(None, "t1", ip, {}, None)
    )

    assert response["reason"] == "not_configured" and response["retryable"] is False


# ------------------------------- isolation -------------------------------


class _Session:
    def __init__(self):
        self.rollbacks = 0

    async def rollback(self):
        self.rollbacks += 1


def _installs():
    return [
        SimpleNamespace(id="s-1", platform="slack", to_channel={"id": "C1"}),
        SimpleNamespace(id="d-1", platform="discord", to_channel={"id": "D1"}),
    ]


def _raising(error):
    async def send(*_args):
        raise error

    return send


async def _delivered(_session, _tenant, ip, *_args):
    return {"platform": ip.platform, "status": "success"}, None


def test_one_destination_raising_keeps_the_others_results():
    service = MessageService.__new__(MessageService)
    service._get_finding_sender = {"slack": _raising(RuntimeError("template bug")), "discord": _delivered}.get

    rows, responses = asyncio.run(
        service.send_finding_notification(_Session(), "t1", {"finding": {}}, "fp", _installs())
    )

    assert rows == []
    assert responses == [
        {
            "platform": "slack",
            "status": "failed",
            "reason": "internal_error",
            "retryable": True,
            "destination_id": "s-1",
            "channel_id": "C1",
        },
        {"platform": "discord", "status": "success"},
    ]


@pytest.mark.parametrize("error, rollbacks", [(SQLAlchemyError("connection reset"), 1), (KeyError("title"), 0)])
def test_only_a_database_error_rolls_the_session_back(error, rollbacks):
    service = MessageService.__new__(MessageService)
    service._get_finding_sender = {"slack": _raising(error), "discord": _delivered}.get
    session = _Session()

    asyncio.run(service.send_finding_notification(session, "t1", {"finding": {}}, "fp", _installs()))

    assert session.rollbacks == rollbacks


def test_a_template_destination_raising_keeps_the_others_results(monkeypatch):
    monkeypatch.setitem(
        message_module.template_mapping,
        "test_type",
        {"common_params": lambda **_: SimpleNamespace(), "slack": lambda _p: {}, "discord": lambda _p: {}},
    )
    service = MessageService.__new__(MessageService)

    async def send(ip, *_args):
        if ip.platform == "slack":
            raise RuntimeError("template bug")
        return {"platform": ip.platform, "status": "success"}

    service.send_template_messages_to_platforms = send

    responses = asyncio.run(service.send_template_notification(_Session(), "t1", {}, "test_type", _installs()))

    assert [(r["platform"], r["status"], r.get("reason")) for r in responses] == [
        ("slack", "failed", "internal_error"),
        ("discord", "success", None),
    ]


def test_a_message_that_cannot_be_built_is_a_failure_not_an_empty_result(monkeypatch):
    def broken(**_params):
        raise ValueError("bad parameters")

    monkeypatch.setitem(message_module.template_mapping, "test_type", {"common_params": broken})

    responses = asyncio.run(
        MessageService.__new__(MessageService).send_template_notification(None, "t1", {}, "test_type", _installs())
    )

    assert responses == [{"platform": "system", "status": "failed", "reason": "internal_error", "retryable": True}]


def test_records_of_posts_already_sent_failing_to_save_do_not_fail_the_send():
    class _BrokenSession(_Session):
        def add_all(self, _rows):
            pass

        async def commit(self):
            raise SQLAlchemyError("deadlock detected")

    session = _BrokenSession()

    assert asyncio.run(MessageService._save_after_send(session, ["row"])) is False
    assert session.rollbacks == 1


def test_a_database_error_keeps_the_other_results_when_installs_come_from_the_database():
    """A rollback expires every row the session holds; installs loaded from the
    database must not be among them, or the senders still to run fail too."""

    async def run():
        engine = create_async_engine("sqlite+aiosqlite:///:memory:")
        async with engine.begin() as conn:
            await conn.run_sync(lambda c: MessagingPlatform.__table__.create(c))
        sessions = async_sessionmaker(bind=engine, class_=AsyncSession, expire_on_commit=False)
        tenant = uuid.uuid4()
        async with sessions() as session:
            for platform in ("slack", "discord"):
                session.add(
                    MessagingPlatform(id=uuid.uuid4(), tenant_id=tenant, platform=platform, client_id="c", token="t")
                )
            await session.commit()

        async def from_db(session, tenant_id):
            return (await session.execute(select(MessagingPlatform))).scalars().all()

        service = MessageService.__new__(MessageService)
        service.cache = None
        service.get_installed_platforms = from_db
        service._get_finding_sender = {
            "slack": _raising(OperationalError("SELECT 1", {}, Exception("connection reset"))),
            "discord": _delivered,
        }.get
        async with sessions() as session:
            installs = await service.get_installed_platforms_from_cache_or_db(session, tenant)
            installs = sorted(installs, key=lambda ip: ip.platform != "slack")
            for ip in installs:
                ip.to_channel = {"id": f"{ip.platform}-channel"}
            _, responses = await service.send_finding_notification(
                session, str(tenant), {"finding": {}}, "fp", installs
            )
        await engine.dispose()
        return responses

    responses = asyncio.run(run())

    assert [(r["platform"], r["status"], r.get("reason")) for r in responses] == [
        ("slack", "failed", "internal_error"),
        ("discord", "success", None),
    ]
