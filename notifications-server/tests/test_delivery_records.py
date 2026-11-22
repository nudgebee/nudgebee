"""Delivery rows in sent_notifications. The send path fills the delivery columns
of the rows threading and Teams buttons read; every other result is recorded
best effort; and every lookup finds rows written either way, including rows
from a release that predates the delivery columns."""

import asyncio
import json
import smtplib
import uuid
from datetime import timedelta
from types import SimpleNamespace
from unittest import mock

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine
from sqlalchemy.orm import Session
from sqlalchemy.pool import StaticPool

from notifications_server.configs import settings
from notifications_server.models.models import Base, SentNotifications
from notifications_server.services import common as common_module
from notifications_server.services import delivery_records as dr
from notifications_server.services.message import DiscordSender, SlackSender
from notifications_server.services.ms_teams import card_actions
from notifications_server.utils.datetime_utils import utc_now

# Hex with letters: SQLite would store an all-digit uuid as a number.
TENANT = uuid.UUID("a1111111-1111-1111-1111-111111111111")
OTHER_TENANT = uuid.UUID("b2222222-2222-2222-2222-222222222222")
OPS_RULE = {"rule_id": "r-ops", "name": "Ops", "platform": "slack", "channels": {"id": "C1", "name": "ops"}}


def _dispatch(**columns):
    return dr.Dispatch(tenant_id=TENANT, kind="finding", fingerprint="fp-1", title="CPU high", **columns)


# ------------------------------- the recorder -------------------------------


def test_every_result_the_send_path_did_not_write_becomes_a_row():
    results = [
        {"platform": "slack", "status": "success", "channel_id": "C1", "message_ts": "1.1", dr.SEND_PATH_ROW: True},
        {"platform": "slack", "status": "success", "channel_id": "C1", "message_ts": "1.2", "thread_id": "1.1"},
        {"platform": "slack_webhook", "status": "failed", "channel_id": "w-1", "reason": "webhook_gone"},
        {"platform": "system", "status": "dropped", "reason": "Resolved reply already delivered"},
    ]

    rows = dr.rows_for(_dispatch(), results)

    assert [(r.platform, r.status, r.message_id, r.thread_id, r.reason) for r in rows] == [
        ("slack", "sent", "1.2", "1.1", None),
        ("slack_webhook", "failed", None, None, "webhook_gone"),
    ]
    assert all(r.dispatch_id and r.fingerprint == "fp-1" and r.title == "CPU high" for r in rows)
    assert all(dr.SEND_PATH_ROW not in result for result in results)


@pytest.mark.parametrize(
    "status, stopped_by, reason",
    [
        ("suppressed", "r-quiet", "suppressed"),
        ("suppressed", None, "dropped_by_default"),
        ("snoozed", "r-quiet", "snoozed"),
        ("queued", None, "held_for_summary"),
        ("severity_gated", None, "digest_only"),
        ("not_sent", None, "no_destination"),
    ],
)
def test_a_message_that_was_not_sent_leaves_one_row_saying_why(status, stopped_by, reason):
    dispatch = _dispatch(stopped_by=stopped_by, matched=[{"rule_id": "r-quiet", "name": "Quiet hours"}])
    result = {"platform": "system", "status": status, "reason": "no_destination" if status == "not_sent" else "text"}

    [row] = dr.rows_for(dispatch, [result])

    assert (row.status, row.reason, row.platform) == ("not_sent", reason, None)
    assert row.routed_by == ([{"id": "r-quiet", "name": "Quiet hours"}] if stopped_by else None)


def test_an_alert_queued_for_a_summary_leaves_a_held_row():
    [row] = dr.rows_for(_dispatch(), {"status": "queued"})

    assert (row.status, row.reason) == ("not_sent", "held_for_summary")


def test_an_alert_the_summary_queue_could_not_take_leaves_a_failed_row():
    [row] = dr.rows_for(_dispatch(), {"status": "ignored"})

    assert (row.status, row.reason, row.error_code, row.platform) == (
        "failed",
        "internal_error",
        "grouping_unavailable",
        None,
    )


def test_a_resolved_slo_the_summary_queue_could_not_take_fails_without_a_retry(monkeypatch):
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    service.group_handler = SimpleNamespace(enqueue=mock.AsyncMock(return_value=None))
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", True)

    results = asyncio.run(service._maybe_handle_grouped_message("slo", {"status": "resolved"}, str(TENANT), {}))

    [result] = results
    assert (result["status"], result["reason"], result["retryable"]) == ("failed", "internal_error", False)
    assert [r.status for r in dr.rows_for(_dispatch(), results)] == ["failed"]


def test_a_resolved_reply_is_routed_as_its_alert_was():
    dispatch = dr.Dispatch(tenant_id=TENANT, kind=dr.FINDING_RESOLVED, fingerprint="fp-1", targets=[OPS_RULE])
    rule = [{"id": "r-ops", "name": "Ops"}]
    results = [
        {"platform": "slack", "status": "success", "channel_id": "C1", "message_ts": "1.9", "thread_id": "1.1"},
        {"platform": "slack", "status": "success", "channel_id": "C2", "message_ts": "2.9", "thread_id": "2.1"},
    ]
    results[0]["routed_by"] = rule

    rows = dr.rows_for(dispatch, results)

    # The second alert post predates routed_by: its reply has none, never "default".
    assert [(r.destination, r.thread_id, r.routed_by) for r in rows] == [("C1", "1.1", rule), ("C2", "2.1", None)]


def test_a_reply_in_a_thread_the_caller_named_is_routed_by_the_request():
    from notifications_server.services.message import MessageService

    service = MessageService.__new__(MessageService)
    seen = []

    async def send_threaded_reply(_tenant, _thread, _parameters, dispatch=None):
        seen.append(dr.routed_by(dispatch, "slack", "C1"))
        return []

    service.send_threaded_reply = send_threaded_reply
    request = SimpleNamespace(tenant_id=str(TENANT), type="generic", source="", thread={"message_ts": "1.1"})

    asyncio.run(service._send(request, {"parameters": {}}, _dispatch(targets=[OPS_RULE])))

    assert seen == ["requested"]


def test_routed_by_names_the_rules_that_sent_it_to_that_destination():
    hook_rule = {
        "rule_id": "r-hook",
        "name": "Hooks",
        "platform": "slack_webhook",
        "channels": {"webhook_target_id": 7},
    }
    dispatch = _dispatch(targets=[OPS_RULE, hook_rule])

    assert dr.routed_by(dispatch, "slack", "C1") == [{"id": "r-ops", "name": "Ops"}]
    assert dr.routed_by(dispatch, "slack_webhook", "7") == [{"id": "r-hook", "name": "Hooks"}]
    assert dr.routed_by(dispatch, "slack", "C9") == "default"
    dispatch.requested = True
    assert dr.routed_by(dispatch, "slack", "C1") == "requested"


def test_a_title_never_carries_an_approval_token_or_a_message_body():
    approval = {"message": "Approve?\nsecret details", "approval_token": "tok-123", "workflow_name": "Rollback api"}

    assert dr.title_of(dr.kind_of("generic", approval), approval) == "Approval for Rollback api"
    assert dr.title_of("automation", {"message": "\nStep 3 done\nthe rest of the body"}) == "Step 3 done"
    assert dr.title_of("grouped_anomaly", {"events": [{}, {}]}) == "Anomaly summary (2 alerts)"
    assert len(dr.title_of("finding", {"finding": {"title": "x" * 500}})) == dr.TITLE_LIMIT


@pytest.mark.parametrize(
    "message, title",
    [
        ("🚨 *New Incident Declared* 🚨\nSeverity: high", "New Incident Declared"),
        (":red_circle:  *CI failed*", "CI failed"),
        (
            ":rocket: *New code merged to `prod`* — <https://github.com/o/r/pull/1|#1 feat: ship it>",
            "New code merged to prod — #1 feat: ship it",
        ),
        ("New incident declared: <#C0123ABCD>", "New incident declared: #C0123ABCD"),
        ("<@U1|ana> approved it, <!here>", "@ana approved it, @here"),
        ("### Proposed fix\nRestart the pod", "Proposed fix"),
        ("**Step 3 · Restart deployment `api`**", "Step 3 · Restart deployment api"),
        ("> _Quoted_ ~old~ [the docs](https://x.io/a_b)", "Quoted old the docs"),
        ("```\nkubectl get pods\n```", "kubectl get pods"),
        ("```error: boom``` in api", "error: boom in api"),
        ("---\n- Deploy &amp; verify", "Deploy & verify"),
        ("*:rocket: Deployment started*", "Deployment started"),
        ("**:fire: Alert firing**", "Alert firing"),
        (":white_check_mark::tada: Done", "Done"),
        ("*Alert* :fire:, check it", "Alert, check it"),
        ("*🚨 Alert*", "Alert"),
        ("\U0001f468\U0000200d\U0001f469\U0000200d\U0001f467 Family day", "Family day"),
    ],
)
def test_a_title_is_the_words_without_chat_markup(message, title):
    assert dr.title_of("automation", {"message": message}) == title


@pytest.mark.parametrize(
    "text",
    [
        "ClusterRole grants verbs [*] on resources [*]",
        'Policy allows "Action": "*" on "Resource": "*"',
        "Role permits wildcard (*) verbs on (*) resources",
        "CORS allows origin '*' and header '*'",
        "Delete *.tmp,*.bak in */* failed",
        "ImportError in __init__.py",
        "node_pressure on /pool-a-02132c6e-pxll",
        "Call failure ratio >30% (provider/model-3.8)",
        "Merge PR #123 for ns:default:pod at 10:30:45, std::io::Error",
        "listen :8080: failed, handlers[0](event) raised, <default:pod> restarted",
        "CACHE_SIZE_MB is 2 * 150 in /api/*/users/*",
        "\U00000dc1\U00000dca\U0000200d\U00000dbb\U00000dd3 alert",
    ],
)
def test_text_that_only_looks_like_markup_is_kept(text):
    assert dr.title_of("automation", {"message": text}) == text


def test_an_alerts_title_and_an_emails_subject_are_stored_as_written():
    [row] = dr.email_rows(TENANT, "daily_recap", "*Daily* recap", {"a@x.io": None})

    assert dr.title_of("finding", {"finding": {"title": "Role grants * on *"}}) == "Role grants * on *"
    assert row.title == "*Daily* recap"


def test_a_title_is_capped_after_its_markup_is_gone():
    title = dr.title_of("automation", {"message": "**" + "x" * 500 + "**"})

    assert len(title) == dr.TITLE_LIMIT and "*" not in title
    assert dr.title_of("automation", {"message": ":tada:\n---"}) is None
    assert dr.title_of("automation", {"message": "---\n" * 10 + "Too far down to be the title"}) is None


def test_a_send_request_names_its_event_account_and_kind():
    payload = {
        "source": "prometheus",
        "parameters": {
            "finding": {
                "id": "33333333-3333-3333-3333-333333333333",
                "fingerprint": "fp-9",
                "title": "Disk full",
                "cloud_account_id": "44444444-4444-4444-4444-444444444444",
            }
        },
    }

    dispatch = dr.dispatch_for(str(TENANT), "finding", payload, source="troubleshoot", dispatch_id=None, attempt=2)

    assert (dispatch.kind, dispatch.source, dispatch.fingerprint, dispatch.title, dispatch.attempt) == (
        "finding",
        "troubleshoot",
        "fp-9",
        "Disk full",
        2,
    )
    assert str(dispatch.event_id) == "33333333-3333-3333-3333-333333333333"
    assert str(dispatch.account_id) == "44444444-4444-4444-4444-444444444444"


def test_with_recording_switched_off_nothing_is_written(monkeypatch):
    monkeypatch.setattr(dr.settings.notifications, "delivery_history_recording_enabled", False)
    opened = []
    monkeypatch.setattr(dr.BaseDB, "async_session", lambda engine: opened.append(engine))

    asyncio.run(dr.record(None, [SentNotifications()]))

    assert opened == []


def test_a_failing_insert_never_reaches_the_send(monkeypatch):
    class _Refusing:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *_exc):
            return False

        def add_all(self, _rows):
            pass

        async def commit(self):
            raise RuntimeError("database is gone")

    monkeypatch.setattr(dr.BaseDB, "async_session", lambda _engine: lambda: _Refusing())

    asyncio.run(dr.record(None, [SentNotifications()]))


# ------------------------------- send-path rows -------------------------------


def _slack_first_post(channel="C1", ts="1.1", age=timedelta(hours=1), tenant=TENANT, legacy=False):
    row = SentNotifications(
        id=uuid.uuid4(),
        created_at=utc_now() - age,
        tenant_id=tenant,
        fingerprint="fp-1",
        slack_team_id="T1",
        slack_thread_id=ts,
        slack_metadata=json.dumps({"channel": channel}),
    )
    return row if legacy else dr.fill(row, _dispatch(targets=[OPS_RULE], attempt=2))


def test_a_slack_first_posts_row_gets_its_delivery_columns_and_keeps_the_old_ones():
    row = _slack_first_post()

    assert (row.kind, row.platform, row.team_id, row.destination, row.message_id, row.status, row.attempt) == (
        "finding",
        "slack",
        "T1",
        "C1",
        "1.1",
        "sent",
        2,
    )
    assert row.thread_id is None and row.routed_by == [{"id": "r-ops", "name": "Ops"}]
    assert (row.slack_team_id, row.slack_thread_id, json.loads(row.slack_metadata)["channel"]) == ("T1", "1.1", "C1")


@pytest.mark.parametrize(
    "platform, config",
    [
        ("slack", {"default_channel_id": "C1"}),
        ("discord", {"default_channel_id": "C1"}),
        ("ms_teams", {"default_team_id": "group-1", "default_channel_id": "C1"}),
    ],
)
def test_a_send_to_an_installs_default_channel_records_its_name(platform, config):
    from notifications_server.models.models import Integration
    from notifications_server.services.message import MessageService, _named
    from notifications_server.services.messaging_installations import build_installation

    integration = Integration(id=uuid.uuid4(), name="team-1", tenant_id=TENANT)
    ip = build_installation(platform, integration, {**config, "default_channel_name": "alerts"})
    MessageService.get_channels_from_request_or_defaults([ip], [], "troubleshoot", {})
    result = {"platform": platform, "status": "success", "channel_id": "C1", "message_ts": "1.1"}

    [row] = dr.rows_for(_dispatch(), [_named(result, ip)])

    assert (row.destination, row.destination_name, row.routed_by) == ("C1", "alerts", "default")


def test_only_the_default_channel_takes_the_defaults_name():
    from notifications_server.services.delivery_errors import channel_name

    ip = SimpleNamespace(platform="discord", channels={"id": "C1"}, default_channel_name="alerts")

    assert channel_name(ip, {"id": "C1"}) == "alerts"
    assert channel_name(ip, {"id": "C1", "name": "ops"}) == "ops"
    assert channel_name(ip, {"id": "C2"}) is None


def test_an_alerts_first_post_to_discords_default_channel_records_its_name(monkeypatch):
    from notifications_server.clients.discord_client import DiscordClient
    from notifications_server.models.models import Integration
    from notifications_server.schemas.message import SendMessageRequest
    from notifications_server.services import message as message_module
    from notifications_server.services.messaging_installations import build_installation

    install = build_installation(
        "discord",
        Integration(id=uuid.uuid4(), name="guild-1", tenant_id=TENANT),
        {"bot_token": "bot", "default_channel_id": "D1", "default_channel_name": "alerts", "thread_repeats": "false"},
    )
    saved = []

    class _Session:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *_exc):
            return False

        def add_all(self, rows):
            saved.extend(rows)

        async def commit(self):
            pass

    async def no_rules(*_args):
        return [], "troubleshoot"

    service = message_module.MessageService.__new__(message_module.MessageService)
    service.engine = None
    service.discord_sender = message_module.DiscordSender(None)
    service.rule_matcher = SimpleNamespace(
        match_notification_rules=no_rules,
        rules_service=SimpleNamespace(get_default_policy=mock.AsyncMock(return_value=None)),
    )
    service.get_installed_platforms_from_cache_or_db = mock.AsyncMock(return_value=[install])
    monkeypatch.setattr(message_module.BaseDB, "async_session", lambda _engine: lambda: _Session())
    monkeypatch.setattr(DiscordClient, "chat_post", mock.Mock(return_value={"ok": True, "ts": "m-1"}))
    finding = {"id": str(uuid.uuid4()), "fingerprint": "fp-1", "title": "Disk full", "priority": "HIGH"}

    asyncio.run(
        service.send_message(SendMessageRequest(tenant_id=str(TENANT), type="finding", parameters={"finding": finding}))
    )

    [row] = saved
    assert (row.platform, row.destination, row.destination_name, row.routed_by) == (
        "discord",
        "D1",
        "alerts",
        "default",
    )


def test_an_automation_posts_row_has_a_plain_title(monkeypatch):
    recorded = []
    monkeypatch.setattr(dr, "record_sync", lambda rows, engine=None: recorded.extend(rows))
    service = common_module.CommonService.__new__(common_module.CommonService)
    service.engine = None

    service._record_automation_post(TENANT, "slack", "C1", "🚨 *New Incident Declared* 🚨\n*Severity:* high")

    assert [row.title for row in recorded] == ["New Incident Declared"]


def test_a_teams_answer_cards_row_is_a_nubi_answer():
    row = card_actions.card_row(TENANT, None, None, "19:ops", "m-1", {"body": []}, "answer")

    assert dr.fill(row).kind == dr.NUBI_ANSWER


def test_a_teams_cards_row_gets_its_team_and_thread_from_the_send():
    row = card_actions.card_row(TENANT, None, None, "19:ops", "m-2", {"body": [], "actions": []}, "approval")
    result = {
        "platform": "ms_teams",
        "status": "success",
        "channel_id": "19:ops",
        "message_ts": "m-2",
        "team_id": "group-1",
        "parent_message_id": "m-1",
    }

    dr.fill_all([row], _dispatch(), [result])

    assert (row.team_id, row.destination, row.message_id, row.thread_id) == ("group-1", "19:ops", "m-2", "m-1")


def test_every_card_a_send_stored_is_filled_even_once_flushed():
    from notifications_server.services import message as message_module

    cards = [
        card_actions.card_row(TENANT, None, None, "19:ops", message_id, {"body": [], "actions": []}, "approval")
        for message_id in ("m-1", "m-2")
    ]
    # The second post autoflushed the first card's row: only the second is still new.
    session = SimpleNamespace(info={message_module.STORED_TEAMS_CARDS: cards}, new=cards[1:], commit=mock.AsyncMock())

    asyncio.run(message_module.MessageService._save_stored_cards(session, _dispatch()))

    assert [(r.kind, r.platform, r.status, r.message_id) for r in cards] == [
        ("approval", "ms_teams", "sent", "m-1"),
        ("approval", "ms_teams", "sent", "m-2"),
    ]
    session.commit.assert_awaited_once()


# ------------------------------- lookups -------------------------------


def _async_lookup(rows, lookup):
    async def run():
        engine = create_async_engine("sqlite+aiosqlite:///:memory:")
        async with engine.begin() as conn:
            await conn.run_sync(lambda c: SentNotifications.__table__.create(c))
        sessions = async_sessionmaker(bind=engine, class_=AsyncSession, expire_on_commit=False)
        async with sessions() as session:
            session.add_all(rows)
            await session.commit()
        async with sessions() as session:
            found = await lookup(session)
        await engine.dispose()
        return found

    return asyncio.run(run())


def _slack_repeat(rows, channel="C1", tenant=TENANT):
    return _async_lookup(rows, lambda s: SlackSender.check_if_sent_already(s, "fp-1", "T1", {"id": channel}, tenant))


def test_a_repeat_within_a_day_replies_in_the_first_posts_thread():
    reply = dr.new_row(_dispatch(), dr.SENT, platform="slack", team_id="T1", destination="C1", message_id="1.2")
    reply.thread_id = "1.1"

    assert _slack_repeat([_slack_first_post(), reply]) == "1.1"


def test_a_repeat_after_a_day_starts_a_new_post():
    assert _slack_repeat([_slack_first_post(age=timedelta(hours=25))]) is None


def test_a_repeat_finds_a_first_post_written_before_the_delivery_columns():
    assert _slack_repeat([_slack_first_post(legacy=True)]) == "1.1"


def test_a_repeat_never_threads_under_another_channel_or_tenant():
    rows = [_slack_first_post(channel="C2"), _slack_first_post(tenant=OTHER_TENANT)]

    assert _slack_repeat(rows) is None


def test_a_discord_repeat_follows_the_same_rule():
    def first_post(age, legacy=False):
        row = DiscordSender.get_new_sent_notification(TENANT, "D1", "m-1", "fp-1")
        row.created_at = utc_now() - age
        return row if legacy else dr.fill(row, _dispatch())

    def lookup(session):
        return DiscordSender.check_if_sent_already(session, "fp-1", TENANT, "D1")

    assert _async_lookup([first_post(timedelta(hours=1))], lookup) == "m-1"
    assert _async_lookup([first_post(timedelta(hours=1), legacy=True)], lookup) == "m-1"
    assert _async_lookup([first_post(timedelta(hours=25))], lookup) is None


@pytest.fixture
def sync_db(monkeypatch):
    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__])
    monkeypatch.setattr(card_actions, "sync_engine", engine)
    return engine


def _store(engine, *rows):
    with Session(engine) as session:
        session.add_all(rows)
        session.commit()


def test_the_alerts_thread_is_its_newest_first_post_never_a_later_reply(sync_db):
    failed = dr.new_row(_dispatch(), dr.FAILED, platform="slack", destination="C1", reason="not_in_channel")
    _store(sync_db, _slack_first_post(age=timedelta(hours=2)), failed)

    with Session(sync_db) as session:
        found = common_module.latest_slack_notification(session, "fp-1", TENANT)

    assert found.slack_thread_id == "1.1"


def test_a_recorded_post_without_a_fingerprint_is_never_an_alerts_thread(sync_db):
    recorded = dr.new_row(
        dr.Dispatch(tenant_id=TENANT, kind="finding"),
        dr.SENT,
        platform="slack",
        team_id="T1",
        destination="C1",
        message_id="1.5",
    )
    _store(sync_db, recorded)

    with Session(sync_db) as session:
        found = session.query(SentNotifications).filter(dr.slack_first_post()).all()

    assert found == []


def _teams_card(message_id="m-1", age=timedelta(hours=1)):
    row = card_actions.card_row(TENANT, None, "fp-1", "19:ops", message_id, {"body": [], "actions": []}, "approval")
    row.id, row.created_at = uuid.uuid4(), utc_now() - age
    return dr.fill(row)


def test_a_card_less_row_with_a_cards_message_id_never_wins_a_click(sync_db):
    history = dr.new_row(_dispatch(), dr.SENT, platform="ms_teams", destination="19:ops", message_id="m-1")
    _store(sync_db, _teams_card(), history)

    stored = card_actions.stored_card(TENANT, "19:ops", "m-1")

    assert stored is not None and stored["kind"] == "approval"


def test_a_teams_card_from_before_the_delivery_columns_still_updates(sync_db):
    legacy = card_actions.card_row(TENANT, None, "fp-1", "19:ops", "m-old", {"body": []}, "approval")
    legacy.id = uuid.uuid4()
    _store(sync_db, legacy)

    assert card_actions.stored_card(TENANT, "19:ops", "m-old")["kind"] == "approval"


def test_after_a_revert_the_old_lookups_find_new_first_posts_and_skip_history_rows(sync_db):
    history = dr.new_row(_dispatch(), dr.SENT, platform="slack", team_id="T1", destination="C1", message_id="1.2")
    _store(sync_db, _slack_first_post(), history)

    with Session(sync_db) as session:
        # The previous release's repeat lookup: team and fingerprint, then the channel in slack_metadata.
        old_rows = session.execute(
            select(SentNotifications).filter_by(slack_team_id="T1", fingerprint="fp-1")
        ).scalars()
        threads = [r.slack_thread_id for r in old_rows if json.loads(r.slack_metadata or "{}").get("channel") == "C1"]

    assert threads == ["1.1"]


# ------------------------------- emails -------------------------------


def test_an_email_leaves_one_row_per_recipient():
    refused = smtplib.SMTPRecipientsRefused({"b@x.io": (550, b"no such user")})

    rows = dr.email_rows(TENANT, "daily_recap", "Daily recap", {"a@x.io": None, "b@x.io": refused})

    assert [(r.kind, r.platform, r.destination, r.status, r.reason, r.error_code) for r in rows] == [
        ("email:daily_recap", "email", "a@x.io", "sent", None, None),
        ("email:daily_recap", "email", "b@x.io", "failed", "recipient_refused", "550"),
    ]


def test_an_email_a_rule_stopped_leaves_one_not_sent_row():
    [row] = dr.email_rows(TENANT, "daily_recap", "Daily recap", {}, not_sent_reason=dr.SUPPRESSED)

    assert (row.status, row.reason, row.destination) == ("not_sent", "suppressed", None)


@pytest.mark.parametrize(
    "error, reason",
    [
        (smtplib.SMTPAuthenticationError(535, b"bad credentials"), "auth_failed"),
        (smtplib.SMTPRecipientsRefused({"a@x.io": (450, b"try later")}), "platform_unavailable"),
        (smtplib.SMTPServerDisconnected("gone"), "platform_unavailable"),
        (smtplib.SMTPDataError(552, b"too big"), "message_rejected"),
        (TimeoutError(), "platform_unavailable"),
    ],
)
def test_an_email_failure_is_classified_from_the_smtp_error(error, reason):
    from notifications_server.services.delivery_errors import email_reason

    assert email_reason(error)[0] == reason


def test_the_processor_records_every_recipient_of_an_email_task(monkeypatch):
    from notifications_server.emailer.sender import EmailBatchError
    from notifications_server.processors import notification as processor_module

    recorded = []

    async def record(_engine, rows):
        recorded.extend(rows)

    class _Emailer:
        @staticmethod
        async def process_email_task(_emails, _task):
            raise EmailBatchError("1 of 2 failed", {"b@x.io": smtplib.SMTPDataError(552, b"too big")})

    processor = processor_module.NotificationProcessor.__new__(processor_module.NotificationProcessor)

    async def rules(*_args):
        return ["a@x.io", "b@x.io"], [], False

    processor._get_configured_email_rules = rules
    monkeypatch.setattr(processor_module.ProcessorFactory, "get", lambda _kind: _Emailer)
    monkeypatch.setattr(dr, "record", record)
    task = {"tenant_id": str(TENANT), "template_type": "daily_recap", "subject": "Recap", "dispatch_id": None}

    with pytest.raises(EmailBatchError):
        asyncio.run(processor.perform_email(task, attempt=1))

    assert sorted((r.destination, r.status, r.reason, r.attempt) for r in recorded) == [
        ("a@x.io", "sent", None, 2),
        ("b@x.io", "failed", "message_rejected", 2),
    ]


@pytest.mark.parametrize(
    "status, stopped_by, reason",
    [
        ("suppressed", "r-quiet", "suppressed"),
        ("suppressed", None, "dropped_by_default"),
        ("snoozed", "r-quiet", "snoozed"),
    ],
)
def test_an_email_a_rule_stopped_says_why_and_names_the_rule(monkeypatch, status, stopped_by, reason):
    from notifications_server.processors import notification as processor_module

    recorded = []

    async def record(_engine, rows):
        recorded.extend(rows)

    async def rules(_tenant, _task, stop=None):
        outcome = SimpleNamespace(response=[{"status": status}], stopped_by=stopped_by)
        stop.update(dr.stopped(outcome, [{"rule_id": "r-quiet", "name": "Quiet hours"}]))
        return None

    processor = processor_module.NotificationProcessor.__new__(processor_module.NotificationProcessor)
    processor._get_configured_email_rules = rules
    monkeypatch.setattr(dr, "record", record)

    asyncio.run(processor.perform_email({"tenant_id": str(TENANT), "template_type": "daily_recap"}))

    [row] = recorded
    named = [{"id": "r-quiet", "name": "Quiet hours"}] if stopped_by else None
    assert (row.status, row.reason, row.routed_by) == ("not_sent", reason, named)


def test_the_sendmail_fallback_names_only_the_recipients_that_failed(monkeypatch):
    from email.message import EmailMessage

    from notifications_server.emailer import sender

    def message(to):
        email = EmailMessage()
        email["From"], email["To"], email["Subject"] = "nb@x.io", to, "Recap"
        email.set_content("hello")
        return email

    def sendmail(email, rcpt=None):
        if email["To"] == "b@x.io":
            raise OSError("sendmail exited 75")

    monkeypatch.setattr(settings.email, "server_host", "")
    monkeypatch.setattr(sender, "_send_email_from_default_service", sendmail)

    with pytest.raises(sender.EmailBatchError) as raised:
        sender.send_email_batch([message("a@x.io"), message("b@x.io"), message("c@x.io")])

    assert list(raised.value.failures) == ["b@x.io"]


def test_an_automation_dm_leaves_a_row(monkeypatch):
    recorded = []
    monkeypatch.setattr(dr, "record_sync", lambda rows, engine=None: recorded.extend(rows))
    service = common_module.CommonService.__new__(common_module.CommonService)
    service.engine = None

    service._record_automation_post(TENANT, "slack", "U1", "Deploy finished\nlogs...", team_id="T1", message_id="9.9")

    [row] = recorded
    assert (row.kind, row.platform, row.destination, row.status, row.message_id, row.title) == (
        "automation",
        "slack",
        "U1",
        "sent",
        "9.9",
        "Deploy finished",
    )


def test_a_failed_automation_post_keeps_slacks_reason(monkeypatch):
    recorded = []
    monkeypatch.setattr(dr, "record_sync", lambda rows, engine=None: recorded.extend(rows))
    service = common_module.CommonService.__new__(common_module.CommonService)
    service.engine = None
    refusal = SimpleNamespace(status_code=200, data={"ok": False, "error": "not_in_channel"})

    service._record_automation_post(TENANT, "slack", "C1", "Run started", failed=common_module.slack_failure(refusal))

    [row] = recorded
    assert (row.status, row.reason, row.error_code) == ("failed", "not_in_channel", "not_in_channel")
