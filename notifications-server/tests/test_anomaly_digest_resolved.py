"""An anomaly that clears shows up in the next grouped anomaly digest, in a
"Resolved" section (DIGEST_RESOLVED_ENABLED). Covers the three moving parts:
the router queues an anomaly's `finding_resolved` next to the firing ones
instead of looking for a Slack thread the digest never recorded; each channel's
batch keeps one item per finding, its latest state; and all four platforms list
resolved anomalies after the firing ones, with their own cap.

With the flag off, or with nothing resolved in the batch, every template must
render exactly what it did before the section existed. That is pinned against
tests/golden/grouped_anomaly_before_resolved_section.json, which was captured
from the templates as they were before this change."""

import asyncio
import json
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import parse_qs, urlparse

import pytest

from notifications_server.configs.settings import settings
from notifications_server.message_templates.discord.grouped_anomaly import get_discord_grouped_anomaly_template
from notifications_server.message_templates.google_chat.grouped_anomaly_notification import (
    MAX_TEXT_LENGTH,
    get_grouped_anomaly_alerts_gchat_template,
)
from notifications_server.message_templates.ms_teams.grouped_anomaly_notification import (
    get_grouped_anomaly_alerts_ms_teams_template,
)
from notifications_server.message_templates.slack import grouped_anomaly_notification as slack_anomaly
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    get_anomaly_aggregated_message_params,
    get_grouped_anomaly_alerts_template,
)
from notifications_server.services.message import MessageService
from tests.test_delivery_split import DEFAULT, DEV, OPS, PROD, TENANT, _deliver, _flush, _service, _stop
from tests.test_grouped_alert_templates import _buttons, assert_no_blocks_in_attachments

BASE_URL = "https://app.example.com"
NOW = datetime(2026, 7, 14, 7, 0, tzinfo=timezone.utc)
RESOLVED_TS = int(datetime(2026, 7, 14, 6, 50, tzinfo=timezone.utc).timestamp())
GOLDEN_PATH = Path(__file__).parent / "golden" / "grouped_anomaly_before_resolved_section.json"

RENDERERS = {
    "slack": get_grouped_anomaly_alerts_template,
    "ms_teams": get_grouped_anomaly_alerts_ms_teams_template,
    "google_chat": get_grouped_anomaly_alerts_gchat_template,
    "discord": get_discord_grouped_anomaly_template,
}


class _FrozenDatetime(datetime):
    @classmethod
    def now(cls, tz=None):
        return NOW if tz else NOW.replace(tzinfo=None)


def pin_environment(setattr_):
    """Pin everything a rendered digest reads from its environment, so a render
    can be compared exactly. Shared with the script that captured the golden."""
    setattr_(settings, "base_url", BASE_URL)
    setattr_(settings.urls, "base_url", BASE_URL)
    setattr_(settings.notifications, "grouped_flush_delay_seconds", 3600)
    setattr_(slack_anomaly, "datetime", _FrozenDatetime)


@pytest.fixture(autouse=True)
def _pinned(monkeypatch):
    pin_environment(monkeypatch.setattr)


@pytest.fixture
def flag_on(monkeypatch):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", True)


@pytest.fixture
def flag_off(monkeypatch):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", False)


def _event(i, account="acc-1", cluster="prod-eu", **overrides):
    """One anomaly as api-server publishes it: the event row, evidences nilled.
    A firing and its resolve are the same row read at two moments."""
    title = f"CPU Anomaly detected for svc-{i} in namespace payments"
    event = {
        "id": f"e-{i}",
        "title": title,
        "description": title,
        "source": "anomaly",
        "priority": "HIGH",
        "status": "FIRING",
        "subject_name": f"svc-{i}",
        "subject_namespace": "payments",
        "subject_type": "deployment",
        "starts_at": "2026-07-14T06:20:00Z",
        "ends_at": None,
        "created_at": "2026-07-14T06:20:05.120394Z",
        "updated_at": "2026-07-14T06:20:05.120394Z",
        "finding_id": f"anomaly|{account}|CPU|svc-{i}|payments",
        "cluster": cluster,
        "cloud_account_id": account,
        "labels": {"anomaly_current": "820m", "anomaly_baseline": "140m"},
        "has_evidences": True,
    }
    event.update(overrides)
    return event


def _resolved(i, **overrides):
    fields = {"status": "RESOLVED", "ends_at": "2026-07-14T06:50:00Z", "updated_at": "2026-07-14T06:50:00.481Z"}
    fields.update(overrides)
    return _event(i, **fields)


def _spend(i, **overrides):
    fields = {
        "title": f"Cloud Spend Anomaly: service-{i} in prod-aws increased by 38.0%",
        "description": f"Daily spend of $208.00 for service-{i} in account prod-aws exceeds baseline average of $14.00 "
        "(z-score: 4.20)",
        "subject_name": f"service-{i}",
        "subject_namespace": "",
        "subject_type": "cloud_account",
        "finding_id": f"spend-{i}",
        "labels": {"anomaly_zscore": "4.20", "anomaly_change": "+$194 (+38.0%)", "anomaly_baseline": "$14"},
    }
    fields.update(overrides)
    return _event(i, account="acc-2", cluster="prod-aws", **fields)


# What the templates rendered before the Resolved section existed. The last
# scenario carries resolved-status items on purpose: with the flag off they
# must still render as the ordinary items they were.
GOLDEN_SCENARIOS = {
    "under_caps": lambda: [_event(0), _spend(1)],
    "over_caps": lambda: [_event(i) for i in range(9)] + [_spend(9), _spend(10)],
    "truncated": lambda: [_event(i, account=f"acc-{i % 3}", cluster=f"cluster-{i % 3}") for i in range(24)],
    "resolved_status": lambda: [_event(0), _resolved(1), _resolved(2, status="closed")],
}
# Only Google Chat truncates, so only it needs the long scenario pinned.
GOLDEN_CASES = [
    (scenario, platform)
    for scenario in GOLDEN_SCENARIOS
    for platform in RENDERERS
    if scenario != "truncated" or platform == "google_chat"
]


def render(platform, events):
    return json.loads(json.dumps(RENDERERS[platform](get_anomaly_aggregated_message_params(events))))


def _golden(scenario, platform):
    return json.loads(GOLDEN_PATH.read_text())[scenario][platform]


def _slack_text(msg):
    return "\n".join(attachment.get("text", "") for attachment in msg["attachments"])


def _teams_texts(card):
    return [block["text"] for block in card["body"]]


# ----------------------------- router -----------------------------


def _router_service():
    service = MessageService.__new__(MessageService)
    service.queued, service.replied = [], []

    async def enqueue(payload):
        service.queued.append(payload)
        return {"status": "queued"}

    async def reply(tenant_id, parameters):
        service.replied.append((tenant_id, parameters))
        return [{"status": "dropped"}]

    service.group_handler = SimpleNamespace(enqueue=enqueue)
    service.send_finding_resolved_reply = reply
    return service


def _resolved_request(source):
    """`finding_resolved` as notification.ProcessEventResolved publishes it."""
    payload = {
        "kind": "notification",
        "type": "finding_resolved",
        "source": source,
        "tenant_id": TENANT,
        "parameters": {"finding": _resolved(1, source=source, fingerprint="fp-1")},
    }
    return SimpleNamespace(
        tenant_id=TENANT,
        type="finding_resolved",
        source=source,
        thread=None,
        dict=lambda **_kwargs: json.loads(json.dumps(payload)),
    )


class TestRouter:
    def test_flag_off_anomaly_resolve_goes_to_the_thread_reply(self, flag_off):
        service = _router_service()

        result = asyncio.run(service.send_message(_resolved_request("anomaly")))

        assert result == [{"status": "dropped"}]
        assert service.queued == []
        assert [tenant for tenant, _parameters in service.replied] == [TENANT]

    def test_flag_on_anomaly_resolve_is_queued_for_the_digest(self, flag_on):
        service = _router_service()

        result = asyncio.run(service.send_message(_resolved_request("anomaly")))

        assert result == {"status": "queued"}
        assert service.replied == []
        (queued,) = service.queued
        # Same shape and same Redis list as a firing anomaly: one digest for both.
        assert {key: queued[key] for key in ("source", "type", "tenant_id")} == {
            "source": "anomaly",
            "type": "anomaly",
            "tenant_id": TENANT,
        }
        assert queued["parameters"]["status"] == "RESOLVED"
        assert queued["parameters"]["finding_id"] == "anomaly|acc-1|CPU|svc-1|payments"

    def test_flag_on_other_sources_still_go_to_the_thread_reply(self, flag_on):
        service = _router_service()

        result = asyncio.run(service.send_message(_resolved_request("prometheus")))

        assert result == [{"status": "dropped"}]
        assert service.queued == []
        assert len(service.replied) == 1

    def test_flag_on_falls_back_to_the_thread_reply_when_queueing_fails(self, flag_on):
        # enqueue() swallows a Redis error and returns None; the resolve then
        # takes today's path rather than vanishing without a response.
        service = _router_service()

        async def broken(_payload):
            return None

        service.group_handler = SimpleNamespace(enqueue=broken)

        assert asyncio.run(service.send_message(_resolved_request("anomaly"))) == [{"status": "dropped"}]
        assert len(service.replied) == 1


# ----------------------------- collapse -----------------------------


def _item(finding_id, status, updated_at=None, **extra):
    item = {"finding_id": finding_id, "status": status, **extra}
    if updated_at:
        item["updated_at"] = updated_at
    return item


class TestCollapse:
    """`latest_per_key` takes the batch as Redis returns it: newest first."""

    @staticmethod
    def _collapse(items):
        from notifications_server.services.delivery_split import latest_per_key

        return latest_per_key(items)

    def test_firing_then_resolved_is_one_resolved_item(self):
        batch = [_item("f1", "RESOLVED", "2026-07-14T06:50:00Z"), _item("f1", "FIRING", "2026-07-14T06:20:00Z")]
        assert [item["status"] for item in self._collapse(batch)] == ["RESOLVED"]

    def test_resolved_twice_is_one_item(self):
        batch = [_item("f1", "RESOLVED", "2026-07-14T06:50:00Z"), _item("f1", "RESOLVED", "2026-07-14T06:50:00Z")]
        assert len(self._collapse(batch)) == 1

    def test_resolved_then_firing_again_is_one_firing_item(self):
        batch = [_item("f1", "FIRING", "2026-07-14T07:10:00Z"), _item("f1", "RESOLVED", "2026-07-14T06:50:00Z")]
        assert [item["status"] for item in self._collapse(batch)] == ["FIRING"]

    def test_items_without_a_finding_id_are_all_kept(self):
        batch = [{"status": "FIRING", "title": "a"}, {"status": "FIRING", "title": "b"}, _item("", "FIRING")]
        assert self._collapse(batch) == batch

    def test_updated_at_beats_list_order(self):
        # The resolve overtook its firing in the queue, so the firing was pushed
        # last and sits first; the row's own updated_at says which is the state.
        batch = [
            _item("f1", "FIRING", "2026-07-14T06:20:05.120394Z"),
            _item("f1", "RESOLVED", "2026-07-14T06:50:00.481Z"),
        ]
        assert [item["status"] for item in self._collapse(batch)] == ["RESOLVED"]

    def test_list_order_decides_when_a_timestamp_is_missing_or_unreadable(self):
        for stale in (None, "not-a-date"):
            batch = [_item("f1", "RESOLVED", stale), _item("f1", "FIRING", "2026-07-14T09:00:00Z")]
            assert [item["status"] for item in self._collapse(batch)] == ["RESOLVED"]

    def test_other_findings_keep_their_place(self):
        batch = [
            _item("f1", "FIRING", "2026-07-14T06:20:00Z"),
            _item("f2", "FIRING", "2026-07-14T06:21:00Z"),
            _item("f1", "RESOLVED", "2026-07-14T06:50:00Z"),
            _item("f3", "RESOLVED", "2026-07-14T06:40:00Z"),
        ]
        collapsed = self._collapse(batch)
        assert [(item["finding_id"], item["status"]) for item in collapsed] == [
            ("f1", "RESOLVED"),
            ("f2", "FIRING"),
            ("f3", "RESOLVED"),
        ]


# ----------------------------- flush: routing and collapse -----------------------------


def _queued(event):
    return {"source": "anomaly", "type": "anomaly", "tenant_id": TENANT, "parameters": event}


class TestFlush:
    def test_resolved_item_is_routed_like_a_firing_one(self, monkeypatch, flag_on):
        service = _service({PROD: [_deliver(OPS)], DEV: [_stop()]}, monkeypatch)
        batch = [
            _queued(_resolved(1, account=PROD)),
            _queued(_event(2, account=PROD)),
            _queued(_resolved(3, account=DEV)),
            _queued(_resolved(4, account="acc-other")),
        ]

        result, _redis = _flush(service, batch)

        assert result == {"status": "ok"}
        # Every item, firing or resolved, is matched on its own as the finding it is.
        assert service.rule_matcher.seen == [
            ("finding", "troubleshoot", PROD),
            ("finding", "troubleshoot", PROD),
            ("finding", "troubleshoot", DEV),
            ("finding", "troubleshoot", "acc-other"),
        ]
        sent = {json.dumps(channel): params["events"] for _type, channel, params in service.sent}
        assert [(event["id"], event["status"]) for event in sent[json.dumps(OPS)]] == [
            ("e-1", "RESOLVED"),
            ("e-2", "FIRING"),
        ]
        # The rule that stops DEV's firing anomalies stops their resolves too.
        assert [event["id"] for event in sent[json.dumps(DEFAULT)]] == ["e-4"]
        assert set(sent) == {json.dumps(OPS), json.dumps(DEFAULT)}

    def test_each_channel_gets_one_item_per_finding(self, monkeypatch, flag_on):
        service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
        batch = [
            _queued(_resolved(1, account=PROD)),
            _queued(_event(2, account=PROD)),
            _queued(_event(1, account=PROD)),
        ]

        _flush(service, batch)

        ((_type, _channel, params),) = service.sent
        assert [(event["id"], event["status"]) for event in params["events"]] == [
            ("e-1", "RESOLVED"),
            ("e-2", "FIRING"),
        ]

    def test_flag_off_leaves_the_batch_as_queued(self, monkeypatch, flag_off):
        service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
        batch = [_queued(_event(1, account=PROD)), _queued(_event(1, account=PROD))]

        _flush(service, batch)

        ((_type, _channel, params),) = service.sent
        assert len(params["events"]) == 2


# ----------------------------- templates: unchanged without resolved items -----------------------------


class TestUnchangedOutput:
    @pytest.mark.parametrize("scenario,platform", [case for case in GOLDEN_CASES if case[0] != "resolved_status"])
    def test_no_resolved_items_renders_as_before(self, scenario, platform, flag_on):
        assert render(platform, GOLDEN_SCENARIOS[scenario]()) == _golden(scenario, platform)

    @pytest.mark.parametrize("scenario,platform", GOLDEN_CASES)
    def test_flag_off_renders_as_before(self, scenario, platform, flag_off):
        assert render(platform, GOLDEN_SCENARIOS[scenario]()) == _golden(scenario, platform)

    def test_the_golden_really_exercises_the_caps(self):
        assert "+6 more anomalies detected" in _slack_text(_golden("over_caps", "slack"))
        assert "…and 1 more anomalies in this account." in _golden("over_caps", "discord")["embeds"][0]["description"]
        assert _golden("truncated", "google_chat")["text"].endswith("...")
        assert len(_golden("truncated", "google_chat")["text"]) == MAX_TEXT_LENGTH


# ----------------------------- templates: the Resolved section -----------------------------

MIXED = [_event(0), _resolved(3), _event(1), _resolved(4), _event(2)]


@pytest.mark.usefixtures("flag_on")
class TestSlack:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        msg = render("slack", MIXED)

        assert msg["text"] == "3 anomalies detected, 2 resolved in prod-eu"
        assert msg["blocks"][0]["text"]["text"] == msg["text"]
        assert [a["color"] for a in msg["attachments"]] == ["#D97A2B"] * 3 + ["#2C8C55"]
        resolved = msg["attachments"][-1]["text"].split("\n")
        assert resolved[0] == "*Resolved*"
        assert resolved[1] == (
            "• *CPU Anomaly detected for svc-3 in namespace payments* · payments/svc-3 · prod-eu"
            f" · resolved <!date^{RESOLVED_TS}^{{date_short_pretty}} {{time}}|14 Jul 2026 06:50 AM UTC>"
        )
        assert len(resolved) == 3
        assert_no_blocks_in_attachments(msg)

    def test_resolved_items_are_not_rendered_as_firing(self):
        msg = render("slack", MIXED)
        firing_text = "\n".join(a["text"] for a in msg["attachments"][:-1])
        assert "svc-3" not in firing_text and "svc-4" not in firing_text
        details = [b["url"] for b in _buttons(msg) if b["text"] == "Details"]
        assert len(details) == 3 and not any("id=e-3" in url or "id=e-4" in url for url in details)

    def test_only_resolved_reads_as_resolved(self):
        msg = render("slack", [_resolved(3), _resolved(4)])
        assert msg["text"] == "2 anomalies resolved in prod-eu"
        assert msg["blocks"][0]["text"]["text"] == msg["text"]
        assert [a["color"] for a in msg["attachments"]] == ["#2C8C55"]
        assert not _buttons(msg)

    def test_single_resolved_is_singular(self):
        assert render("slack", [_resolved(3)])["text"] == "1 anomaly resolved in prod-eu"
        assert render("slack", [_event(0), _resolved(3)])["text"] == "1 anomaly detected, 1 resolved in prod-eu"

    def test_resolved_cap_and_its_own_overflow_count(self):
        msg = render("slack", [_event(0)] + [_resolved(i) for i in range(10, 18)])
        lines = msg["attachments"][-1]["text"].split("\n")
        assert len([line for line in lines if line.startswith("• ")]) == 5
        assert lines[-1] == "_+3 more resolved_"
        assert "more anomalies detected" not in _slack_text(msg)
        # Something did not fit, so the link to all of them is there — once.
        assert [b["text"] for b in _buttons(msg)].count("View All Anomalies") == 1

    def test_each_section_counts_its_own_overflow(self):
        msg = render("slack", [_event(i) for i in range(7)] + [_resolved(i) for i in range(10, 18)])
        assert msg["text"] == "7 anomalies detected, 8 resolved in prod-eu"
        assert [a["color"] for a in msg["attachments"]] == ["#D97A2B"] * 5 + ["#94A3B8", "#2C8C55"]
        assert msg["attachments"][5]["text"] == "_+2 more anomalies detected_"
        assert msg["attachments"][6]["text"].endswith("_+3 more resolved_")
        assert [b["text"] for b in _buttons(msg)].count("View All Anomalies") == 1

    def test_no_overflow_has_no_view_all_button(self):
        assert "View All Anomalies" not in [b["text"] for b in _buttons(render("slack", MIXED))]

    def test_view_all_window_reaches_back_to_a_resolved_anomaly_first_seen_days_ago(self):
        # The Events page filters on created_at. A long-lived anomaly keeps its
        # first created_at while starts_at moves with every detection cycle.
        old = "2026-07-11T02:00:00Z"
        msg = render("slack", [_event(0)] + [_resolved(i, created_at=old) for i in range(10, 18)])
        url = [b["url"] for b in _buttons(msg) if b["text"] == "View All Anomalies"][0]
        params = parse_qs(urlparse(url).query)
        assert int(params["start_time"][0]) <= int(datetime(2026, 7, 11, 2, 0, tzinfo=timezone.utc).timestamp() * 1000)
        assert params["source"] == ["anomaly"] and "status" not in params

    def test_an_unset_created_at_neither_fails_the_digest_nor_narrows_the_window(self):
        # An unset timestamp is serialized as year 1, which cannot be shifted
        # back by the window buffer. It is skipped, and the window still reaches
        # back to the oldest real created_at.
        old = "2026-07-11T02:00:00Z"
        resolved = [_resolved(10, created_at="0001-01-01T00:00:00Z")]
        resolved += [_resolved(i, created_at=old) for i in range(11, 18)]
        msg = render("slack", [_event(0)] + resolved)
        url = [b["url"] for b in _buttons(msg) if b["text"] == "View All Anomalies"][0]
        start_time = int(parse_qs(urlparse(url).query)["start_time"][0])
        assert 0 < start_time <= int(datetime(2026, 7, 11, 2, 0, tzinfo=timezone.utc).timestamp() * 1000)

    def test_status_is_matched_case_insensitively(self):
        msg = render("slack", [_event(0), _resolved(3, status="resolved"), _resolved(4, status="Closed")])
        assert msg["text"] == "1 anomaly detected, 2 resolved in prod-eu"

    def test_spend_anomaly_names_its_account_and_missing_time_is_left_out(self):
        msg = render("slack", [_spend(5, status="RESOLVED", ends_at=None)])
        assert msg["attachments"][0]["text"].split("\n")[1] == (
            "• *Cloud Spend Anomaly: service-5 in prod-aws increased by 38.0%* · service-5 · prod-aws"
        )


@pytest.mark.usefixtures("flag_on")
class TestMsTeams:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        texts = _teams_texts(render("ms_teams", MIXED))

        assert texts[0] == "🚨 **3 Anomalies detected, 2 resolved across 1 accounts**"
        heading = texts.index("✅ **Resolved**")
        assert max(i for i, text in enumerate(texts) if text.startswith("  Started at:")) < heading
        assert texts[heading + 1 :] == [
            "- **CPU Anomaly detected for svc-3 in namespace payments** · payments/svc-3 · prod-eu"
            " · resolved 14 Jul 2026 06:50 AM UTC",
            "- **CPU Anomaly detected for svc-4 in namespace payments** · payments/svc-4 · prod-eu"
            " · resolved 14 Jul 2026 06:50 AM UTC",
        ]

    def test_resolved_items_are_not_rendered_as_firing(self):
        texts = _teams_texts(render("ms_teams", MIXED))
        assert len([text for text in texts if text.startswith("  Priority:")]) == 3
        assert not any("Status: *RESOLVED*" in text for text in texts)

    def test_only_resolved_reads_as_resolved(self):
        texts = _teams_texts(render("ms_teams", [_resolved(3), _resolved(4)]))
        assert texts[0] == "✅ **2 Anomalies resolved across 1 accounts**"
        assert not any("Review detected anomalies" in text or text.startswith("**Account:**") for text in texts)
        assert len([text for text in texts if text.startswith("- **")]) == 2

    def test_resolved_cap_and_its_own_overflow_count(self):
        texts = _teams_texts(render("ms_teams", [_event(0)] + [_resolved(i) for i in range(10, 18)]))
        assert len([text for text in texts if " · resolved " in text]) == 5
        assert texts[-1] == "+3 more resolved"


@pytest.mark.usefixtures("flag_on")
class TestGoogleChat:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        text = render("google_chat", MIXED)["text"]

        assert text.startswith("🚨 *3 Anomalies detected, 2 resolved across 1 accounts*\n")
        firing, resolved = text.split("\n\n✅ *Resolved*\n")
        assert firing.count("  - Started at:") == 3
        assert resolved.split("\n") == [
            "• *CPU Anomaly detected for svc-3 in namespace payments* · payments/svc-3 · prod-eu"
            " · resolved 14 Jul 2026 06:50 AM UTC",
            "• *CPU Anomaly detected for svc-4 in namespace payments* · payments/svc-4 · prod-eu"
            " · resolved 14 Jul 2026 06:50 AM UTC",
        ]

    def test_resolved_items_are_not_rendered_as_firing(self):
        text = render("google_chat", MIXED)["text"]
        assert text.count("  - Priority:") == 3 and "Status: *RESOLVED*" not in text

    def test_only_resolved_reads_as_resolved(self):
        text = render("google_chat", [_resolved(3), _resolved(4)])["text"]
        assert text.startswith("✅ *2 Anomalies resolved across 1 accounts*\n\n✅ *Resolved*\n• *CPU Anomaly")
        assert "Review detected anomalies" not in text and "*Account:*" not in text

    def test_resolved_cap_and_its_own_overflow_count(self):
        text = render("google_chat", [_event(0)] + [_resolved(i) for i in range(10, 18)])["text"]
        assert text.count(" · resolved ") == 5
        assert text.endswith("\n+3 more resolved")

    def test_truncation_never_reaches_the_resolved_section(self):
        # Worst case for the reserve: long titles, long names, a full section.
        long_name = "w" * 200
        resolved = [_resolved(i, title="T" * 400, subject_name=long_name, cluster="c" * 200) for i in range(50, 58)]
        text = render("google_chat", GOLDEN_SCENARIOS["truncated"]() + resolved)["text"]

        assert len(text) <= MAX_TEXT_LENGTH
        firing, section = text.split("\n\n✅ *Resolved*\n")
        assert firing.endswith("...")
        lines = section.split("\n")
        assert len(lines) == 6 and lines[-1] == "+3 more resolved"
        assert all(line.endswith(" · resolved 14 Jul 2026 06:50 AM UTC") for line in lines[:5])


@pytest.mark.usefixtures("flag_on")
class TestDiscord:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        payload = render("discord", MIXED)

        assert payload["content"] == "⚠️ 3 anomalies detected, 2 resolved across 1 account(s)"
        account, resolved = payload["embeds"]
        assert account["title"] == "Account: prod-eu" and account["description"].count("\n") == 2
        assert resolved["title"] == "Resolved" and resolved["color"] != account["color"]
        assert resolved["description"].split("\n") == [
            "• **CPU Anomaly detected for svc-3 in namespace payments** · payments/svc-3 · prod-eu"
            " · resolved 14 Jul 2026 06:50 AM UTC",
            "• **CPU Anomaly detected for svc-4 in namespace payments** · payments/svc-4 · prod-eu"
            " · resolved 14 Jul 2026 06:50 AM UTC",
        ]

    def test_resolved_items_are_not_rendered_as_firing(self):
        account = render("discord", MIXED)["embeds"][0]
        assert "svc-3" not in account["description"] and "RESOLVED" not in account["description"]

    def test_only_resolved_reads_as_resolved(self):
        payload = render("discord", [_resolved(3), _resolved(4)])
        assert payload["content"] == "✅ 2 anomalies resolved across 1 account(s)"
        assert [embed["title"] for embed in payload["embeds"]] == ["Resolved"]

    def test_resolved_cap_and_its_own_overflow_count(self):
        payload = render("discord", [_event(0)] + [_resolved(i) for i in range(10, 18)])
        lines = payload["embeds"][-1]["description"].split("\n")
        assert len(lines) == 6 and lines[-1] == "…and 3 more resolved."

    def test_resolved_embed_keeps_its_place_under_the_embed_limit(self):
        firing = [_event(i, account=f"acc-{i}", cluster=f"cluster-{i}") for i in range(12)]
        payload = render("discord", firing + [_resolved(20)])
        assert len(payload["embeds"]) == 10
        assert payload["embeds"][-1]["title"] == "Resolved"
        assert payload["content"] == (
            "⚠️ 12 anomalies detected, 1 resolved across 12 account(s), 3 account(s) not shown"
        )

    def test_tenth_account_pushed_out_by_the_resolved_embed_is_counted(self):
        firing = [_event(i, account=f"acc-{i}", cluster=f"cluster-{i}") for i in range(10)]

        without = render("discord", firing)
        assert len(without["embeds"]) == 10 and "not shown" not in without["content"]

        payload = render("discord", firing + [_resolved(20, account="acc-20")])
        assert [embed["title"] for embed in payload["embeds"]][-2:] == ["Account: cluster-8", "Resolved"]
        assert payload["content"] == (
            "⚠️ 10 anomalies detected, 1 resolved across 11 account(s), 1 account(s) not shown"
        )
