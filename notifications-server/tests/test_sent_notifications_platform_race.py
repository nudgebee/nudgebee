"""Regression test: get_channel_and_ts_from_sent_notifications must pick the
current tenant's Slack row for a fingerprint -- not just whichever platform's
row is newest, and never another tenant's row.

Bug 1: a tenant with Slack, Discord, and MS Teams all enabled gets one
sent_notifications row per platform for the same finding fingerprint
(MessageService.send_finding_notification sends to every installed
platform). The query ordered by created_at DESC with no platform filter, so
it could land on the Discord/Teams row (slack_* columns all NULL) instead of
the Slack one if that row happened to be written a moment later. That made
get_channel_and_ts_from_sent_notifications return (None, None, None, None),
which /llm/response's _handle_event_conversation then turns into a 404 --
llm-server's async analysis result had nowhere to route to, and the Slack
thread sat on "Analyzing..." forever with no visible error anywhere.

Fix 1: filter for slack_thread_id IS NOT NULL before ordering/limiting, so a
newer Discord/Teams row for the same fingerprint can no longer shadow the
Slack one.

Bug 2 (caught in review before merge): fingerprints carry no tenant identity
(hashed from alert identity alone), so two tenants can collide on the same
fingerprint. The slack_thread_id filter above, without tenant scoping, could
then select a *different* tenant's Slack row instead of this tenant's own
(non-Slack, or missing) row -- posting the investigation result into a
foreign tenant's Slack thread.

Fix 2: scope by tenant_id (threaded through from payload.tenant_id at the
/llm/response call site) whenever the caller has one.
"""

import uuid
from datetime import datetime, timedelta, timezone

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import Session

from notifications_server.models.models import Base, SentNotifications
from notifications_server.services.common import CommonService


@pytest.fixture
def service_with_sqlite_session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__])
    service = CommonService.__new__(CommonService)
    service.session = Session(engine)
    yield service
    service.session.close()


def _row(fingerprint, created_at, tenant_id, **kwargs):
    defaults = dict(
        id=uuid.uuid4(),
        created_at=created_at,
        tenant_id=tenant_id,
        fingerprint=fingerprint,
        account_id=uuid.uuid4(),
    )
    defaults.update(kwargs)
    return SentNotifications(**defaults)


def test_newer_discord_row_does_not_shadow_older_slack_row(service_with_sqlite_session):
    service = service_with_sqlite_session
    fingerprint = "shared-fingerprint"
    tenant_id = uuid.uuid4()
    t0 = datetime(2026, 8, 11, 14, 33, 37, tzinfo=timezone.utc)

    slack_row = _row(
        fingerprint,
        t0,
        tenant_id,
        slack_team_id="T05JWTTH3NH",
        slack_thread_id="1786458817.386549",
        slack_metadata='{"channel": "C09G2CETZ42", "bot_id": "B05NF35UV8F"}',
    )
    # Discord's row for the same fingerprint and the same tenant, written a
    # second later -- no slack_* columns populated at all.
    discord_row = _row(
        fingerprint, t0 + timedelta(seconds=1), tenant_id, discord_channel_id="123", discord_message_id="456"
    )

    service.session.add_all([slack_row, discord_row])
    service.session.commit()

    channel_id, thread_ts, team_id, account_id = service.get_channel_and_ts_from_sent_notifications(
        f"event-{fingerprint}", tenant_id=tenant_id
    )

    assert channel_id == "C09G2CETZ42"
    assert thread_ts == "1786458817.386549"
    assert team_id == "T05JWTTH3NH"


def test_only_platform_empty_rows_returns_none(service_with_sqlite_session):
    service = service_with_sqlite_session
    fingerprint = "discord-only-fingerprint"
    tenant_id = uuid.uuid4()
    row = _row(fingerprint, datetime.now(timezone.utc), tenant_id, discord_channel_id="123", discord_message_id="456")
    service.session.add(row)
    service.session.commit()

    result = service.get_channel_and_ts_from_sent_notifications(f"event-{fingerprint}", tenant_id=tenant_id)

    assert result == (None, None, None, None)


def test_newest_of_multiple_slack_rows_wins(service_with_sqlite_session):
    """Pins the order_by(created_at.desc()) step itself: a fingerprint can
    legitimately have more than one Slack row (the same finding delivered to
    several channels/threads), and the newest one's thread_ts *and*
    account_id must both be returned -- not just whichever row the DB
    happens to return first."""
    service = service_with_sqlite_session
    fingerprint = "multi-channel-fingerprint"
    tenant_id = uuid.uuid4()
    older_account_id = uuid.uuid4()
    newer_account_id = uuid.uuid4()
    t0 = datetime(2026, 8, 11, 10, 0, 0, tzinfo=timezone.utc)

    older_row = _row(
        fingerprint,
        t0,
        tenant_id,
        account_id=older_account_id,
        slack_team_id="T05JWTTH3NH",
        slack_thread_id="1700000000.000000",
        slack_metadata='{"channel": "COLDCHANNEL", "bot_id": "B1"}',
    )
    newer_row = _row(
        fingerprint,
        t0 + timedelta(minutes=5),
        tenant_id,
        account_id=newer_account_id,
        slack_team_id="T05JWTTH3NH",
        slack_thread_id="1700000300.000000",
        slack_metadata='{"channel": "CNEWCHANNEL", "bot_id": "B1"}',
    )

    service.session.add_all([older_row, newer_row])
    service.session.commit()

    channel_id, thread_ts, team_id, account_id = service.get_channel_and_ts_from_sent_notifications(
        f"event-{fingerprint}", tenant_id=tenant_id
    )

    assert channel_id == "CNEWCHANNEL"
    assert thread_ts == "1700000300.000000"
    assert account_id == newer_account_id


def test_another_tenants_slack_row_never_wins_over_own_tenant(service_with_sqlite_session):
    """Fingerprints are hashed from alert identity alone (no tenant), so two
    tenants can collide on the same fingerprint. Scoping by tenant_id must
    stop a foreign tenant's Slack row from being selected instead of this
    tenant's own (Discord-only, unresolvable) row for the same fingerprint --
    otherwise the investigation result would post into the wrong tenant's
    Slack thread."""
    service = service_with_sqlite_session
    fingerprint = "colliding-fingerprint"
    own_tenant_id = uuid.uuid4()
    other_tenant_id = uuid.uuid4()

    own_tenant_discord_row = _row(
        fingerprint, datetime.now(timezone.utc), own_tenant_id, discord_channel_id="own-discord-channel"
    )
    other_tenant_slack_row = _row(
        fingerprint,
        datetime.now(timezone.utc),
        other_tenant_id,
        slack_team_id="T_OTHER_TENANT",
        slack_thread_id="9999999999.000000",
        slack_metadata='{"channel": "FOREIGN_CHANNEL", "bot_id": "B_OTHER"}',
    )

    service.session.add_all([own_tenant_discord_row, other_tenant_slack_row])
    service.session.commit()

    result = service.get_channel_and_ts_from_sent_notifications(f"event-{fingerprint}", tenant_id=own_tenant_id)

    # Own tenant has no usable Slack row for this fingerprint -- must return
    # nothing, never the other tenant's channel/thread.
    assert result == (None, None, None, None)
