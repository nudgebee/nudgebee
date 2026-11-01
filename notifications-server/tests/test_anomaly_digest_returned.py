"""An anomaly that had cleared and fires again is listed in the next grouped
anomaly digest, labelled "returned". api-server publishes it as
`finding_returned` (FEATURE_ANOMALY_RETURNED_DIGEST_ENABLED, which is the only
switch: there is none on this side). Covers the three moving parts: the router
queues an anomaly's `finding_returned` next to the firing ones, marked
`returned`, and drops it for any other source; each channel's batch keeps one
item per finding, its latest state; and all four platforms label the returned
item in its own line while still counting it as a firing anomaly.

An item without the mark must render exactly as it did before the label
existed: tests/golden/grouped_anomaly_before_resolved_section.json."""

import asyncio

import pytest

from notifications_server.configs.settings import settings
from notifications_server.schemas.message import SendMessageRequest
from notifications_server.services.delivery_split import latest_per_key
from tests.test_anomaly_digest_resolved import (
    GOLDEN_CASES,
    GOLDEN_SCENARIOS,
    RENDERERS,
    _event,
    _golden,
    _item,
    _queued,
    _resolved,
    _router_service,
    _slack_text,
    _teams_texts,
    pin_environment,
    render,
)
from tests.test_delivery_split import OPS, PROD, TENANT, _deliver, _flush, _service


@pytest.fixture(autouse=True)
def _pinned(monkeypatch):
    pin_environment(monkeypatch.setattr)


@pytest.fixture
def flag_on(monkeypatch):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", True)


@pytest.fixture
def flag_off(monkeypatch):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", False)


def _returned(i, **overrides):
    """A cleared anomaly that fired again, as the router queues it: the event
    row, FIRING once more, marked `returned`."""
    fields = {"returned": True, "updated_at": "2026-07-14T06:55:00.250Z"}
    fields.update(overrides)
    return _event(i, **fields)


# The line that names a firing anomaly on each platform, as it is today and with
# the label.
PLAIN_LINE = {
    "slack": "*{title}*",
    "ms_teams": "- **{title}**",
    "google_chat": "• *{title}*",
    "discord": "• **{title}** — HIGH/FIRING on `payments/svc-{i}`",
}
RETURNED_LINE = {
    "slack": "*{title}* · *returned*",
    "ms_teams": "- **{title}** · **returned**",
    "google_chat": "• *{title}* · *returned*",
    "discord": "• **{title}** · **returned** — HIGH/FIRING on `payments/svc-{i}`",
}


def _line(lines_by_platform, platform, i):
    return lines_by_platform[platform].format(title=f"CPU Anomaly detected for svc-{i} in namespace payments", i=i)


def _lines(platform, msg):
    """Every line of text a rendered digest shows, headline first."""
    if platform == "slack":
        return [msg["text"]] + _slack_text(msg).split("\n")
    if platform == "ms_teams":
        return _teams_texts(msg)
    if platform == "google_chat":
        return msg["text"].split("\n")
    return [msg["content"]] + [line for embed in msg["embeds"] for line in embed["description"].split("\n")]


# ----------------------------- router -----------------------------


def _request(type_, source):
    """A finding as api-server publishes it: `finding_returned` from
    notification.ProcessEventReturned, `finding` from notification.ProcessEvent."""
    return SendMessageRequest(
        kind="notification",
        type=type_,
        source=source,
        tenant_id=TENANT,
        parameters={"finding": _event(1, source=source)},
    )


class TestRouter:
    @pytest.mark.parametrize("digest_resolved_enabled", [True, False])
    def test_returned_anomaly_is_queued_for_the_digest_marked_returned(self, monkeypatch, digest_resolved_enabled):
        monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", digest_resolved_enabled)
        service = _router_service()

        result = asyncio.run(service.send_message(_request("finding_returned", "anomaly")))

        assert result == {"status": "queued"}
        assert service.replied == []
        (queued,) = service.queued
        # Same shape and same Redis list as a firing anomaly: one digest for both.
        assert {key: queued[key] for key in ("source", "type", "tenant_id")} == {
            "source": "anomaly",
            "type": "anomaly",
            "tenant_id": TENANT,
        }
        assert queued["parameters"]["returned"] is True
        assert queued["parameters"]["status"] == "FIRING"
        assert queued["parameters"]["finding_id"] == "anomaly|acc-1|CPU|svc-1|payments"

    def test_returned_finding_of_another_source_is_dropped(self):
        # _router_service has no rule matcher or sender: a request that went on
        # to be sent as a message of its own would fail and come back as [].
        service = _router_service()

        result = asyncio.run(service.send_message(_request("finding_returned", "prometheus")))

        (response,) = result
        assert (response["platform"], response["status"]) == ("system", "dropped")
        assert "anomaly digest" in response["reason"]
        assert service.queued == []
        assert service.replied == []

    def test_returned_anomaly_that_cannot_be_queued_is_reported_as_failed(self):
        # GroupedMessageHandler.enqueue logs a Redis error and returns None.
        service = _router_service()

        async def enqueue(_payload):
            return None

        service.group_handler.enqueue = enqueue

        (response,) = asyncio.run(service.send_message(_request("finding_returned", "anomaly")))

        assert (response["platform"], response["status"]) == ("system", "failed")
        assert service.replied == []

    def test_first_firing_is_queued_without_the_mark(self):
        service = _router_service()

        assert asyncio.run(service.send_message(_request("finding", "anomaly"))) == {"status": "queued"}

        (queued,) = service.queued
        assert "returned" not in queued["parameters"]


# ----------------------------- collapse -----------------------------


class TestCollapse:
    """`latest_per_key` takes the batch as Redis returns it: newest first."""

    def test_resolved_then_returned_is_one_firing_item_marked_returned(self):
        returned = _item("f1", "FIRING", "2026-07-14T06:55:00Z", returned=True)
        assert latest_per_key([returned, _item("f1", "RESOLVED", "2026-07-14T06:50:00Z")]) == [returned]

    def test_returned_then_resolved_is_one_resolved_item(self):
        resolved = _item("f1", "RESOLVED", "2026-07-14T07:10:00Z")
        assert latest_per_key([resolved, _item("f1", "FIRING", "2026-07-14T06:55:00Z", returned=True)]) == [resolved]

    def test_updated_at_beats_list_order(self):
        # The return overtook the resolve before it in the queue.
        returned = _item("f1", "FIRING", "2026-07-14T06:55:00Z", returned=True)
        assert latest_per_key([_item("f1", "RESOLVED", "2026-07-14T06:50:00Z"), returned]) == [returned]


# ----------------------------- flush: one window -----------------------------


def _flushed_slack(monkeypatch, batch):
    """The Slack digest the one channel gets for a batch, newest item first."""
    service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
    _flush(service, [_queued(event) for event in batch])
    ((_type, _channel, params),) = service.sent
    return render("slack", params["events"])


class TestFlush:
    def test_resolved_then_returned_shows_one_firing_line_labelled_returned(self, monkeypatch, flag_on):
        msg = _flushed_slack(monkeypatch, [_returned(1, account=PROD), _resolved(1, account=PROD)])

        assert msg["text"] == "1 anomaly detected in prod-eu"
        assert [a["color"] for a in msg["attachments"]] == ["#D97A2B"]
        assert msg["attachments"][0]["text"].split("\n")[0] == _line(RETURNED_LINE, "slack", 1)

    def test_returned_then_resolved_shows_one_resolved_line(self, monkeypatch, flag_on):
        resolved = _resolved(1, account=PROD, ends_at="2026-07-14T07:10:00Z", updated_at="2026-07-14T07:10:00.300Z")
        msg = _flushed_slack(monkeypatch, [resolved, _returned(1, account=PROD)])

        assert msg["text"] == "1 anomaly resolved in prod-eu"
        assert [a["color"] for a in msg["attachments"]] == ["#2C8C55"]
        assert "returned" not in _slack_text(msg)

    def test_digest_resolved_off_still_lists_the_returned_anomaly(self, monkeypatch, flag_off):
        msg = _flushed_slack(monkeypatch, [_returned(1, account=PROD), _event(2, account=PROD)])

        assert msg["text"] == "2 anomalies detected in prod-eu"
        assert [a["text"].split("\n")[0] for a in msg["attachments"]] == [
            _line(RETURNED_LINE, "slack", 1),
            _line(PLAIN_LINE, "slack", 2),
        ]


# ----------------------------- templates -----------------------------

MIXED = [_event(0), _returned(1), _resolved(2)]
MIXED_HEADLINE = {
    "slack": "2 anomalies detected, 1 resolved in prod-eu",
    "ms_teams": "🚨 **2 Anomalies detected, 1 resolved across 1 accounts**",
    "google_chat": "🚨 *2 Anomalies detected, 1 resolved across 1 accounts*",
    "discord": "⚠️ 2 anomalies detected, 1 resolved across 1 account(s)",
}


@pytest.mark.usefixtures("flag_on")
@pytest.mark.parametrize("platform", RENDERERS)
class TestReturnedLabel:
    def test_returned_item_differs_from_a_plain_one_only_by_the_label(self, platform):
        # The headline is among the lines compared: a returned anomaly is
        # counted as the firing anomaly it is.
        plain = _lines(platform, render(platform, [_event(1)]))
        returned = _lines(platform, render(platform, [_returned(1)]))

        assert _line(PLAIN_LINE, platform, 1) in plain
        assert returned == [
            _line(RETURNED_LINE, platform, 1) if line == _line(PLAIN_LINE, platform, 1) else line for line in plain
        ]

    def test_mixed_digest_labels_only_the_returned_item(self, platform):
        lines = _lines(platform, render(platform, MIXED))

        assert lines[0] == MIXED_HEADLINE[platform]
        assert _line(PLAIN_LINE, platform, 0) in lines
        assert [line for line in lines if "returned" in line] == [_line(RETURNED_LINE, platform, 1)]
        # The resolved one is in the Resolved section, after both firing lines.
        (resolved,) = [index for index, line in enumerate(lines) if "svc-2" in line]
        assert " · resolved " in lines[resolved]
        assert resolved > lines.index(_line(RETURNED_LINE, platform, 1)) > lines.index(_line(PLAIN_LINE, platform, 0))

    def test_digest_resolved_off_labels_the_returned_item_all_the_same(self, platform, flag_off):
        lines = _lines(platform, render(platform, [_event(0), _returned(1)]))

        assert _line(PLAIN_LINE, platform, 0) in lines
        assert [line for line in lines if "returned" in line] == [_line(RETURNED_LINE, platform, 1)]


class TestUnchangedOutput:
    """The existing golden tests cover an item that carries no `returned` at
    all; an unset or false one must render the same."""

    @pytest.mark.parametrize("returned", [None, False])
    @pytest.mark.parametrize("scenario,platform", GOLDEN_CASES)
    def test_item_not_marked_returned_renders_as_before(self, scenario, platform, returned, flag_off):
        events = [dict(event, returned=returned) for event in GOLDEN_SCENARIOS[scenario]()]
        assert render(platform, events) == _golden(scenario, platform)

    @pytest.mark.parametrize("scenario,platform", [case for case in GOLDEN_CASES if case[0] != "resolved_status"])
    def test_item_not_marked_returned_renders_as_before_with_the_resolved_section_on(self, scenario, platform, flag_on):
        events = [dict(event, returned=False) for event in GOLDEN_SCENARIOS[scenario]()]
        assert render(platform, events) == _golden(scenario, platform)
