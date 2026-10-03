"""An SLO that recovers shows up in the next grouped SLO digest, in a "Resolved"
section (DIGEST_RESOLVED_ENABLED). services-server publishes the recovery as the
digest item it publishes while the SLO is firing, with status RESOLVED and the
same finding_id. Covers the three moving parts: the router queues that item next
to the firing ones, or drops it when the section is off; each channel's batch
keeps one item per finding, its latest state, which also folds an SLO's hourly
repeats into one line; and all four platforms list resolved SLOs after the
firing ones, with their own cap.

With the flag off, or with nothing resolved in the batch, every template must
render exactly what it did before the section existed. That is pinned against
tests/golden/grouped_slo_before_resolved_section.json, which was captured from
the templates as they were before this change."""

import asyncio
import json
import time
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace

import pytest
from pydantic import ValidationError

from notifications_server.configs.settings import settings
from notifications_server.message_templates.discord.slo import get_discord_grouped_slo_alert_template
from notifications_server.message_templates.google_chat.grouped_anomaly_notification import MAX_TEXT_LENGTH
from notifications_server.message_templates.google_chat.grouped_slo_notification import (
    get_grouped_slo_alerts_gchat_template,
)
from notifications_server.message_templates.ms_teams.grouped_slo_notification import (
    get_grouped_slo_alerts_ms_teams_template,
)
from notifications_server.message_templates.slack.grouped_slo_notification import (
    get_grouped_slo_alerts_template,
    get_slo_aggregated_message_params,
)
from notifications_server.services.delivery_split import latest_per_key
from notifications_server.services.message import GroupedMessageHandler, MessageService
from tests.test_delivery_split import DEFAULT, DEV, OPS, PROD, TENANT, _deliver, _Redis, _service, _stop
from tests.test_grouped_alert_templates import _buttons, assert_no_blocks_in_attachments

BASE_URL = "https://app.example.com"
RESOLVED_TS = int(datetime(2026, 7, 14, 6, 50, tzinfo=timezone.utc).timestamp())
GOLDEN_PATH = Path(__file__).parent / "golden" / "grouped_slo_before_resolved_section.json"
SLO_KEY = "grouped:%s:slo_alert" % TENANT

RENDERERS = {
    "slack": get_grouped_slo_alerts_template,
    "ms_teams": get_grouped_slo_alerts_ms_teams_template,
    "google_chat": get_grouped_slo_alerts_gchat_template,
    "discord": get_discord_grouped_slo_alert_template,
}


def pin_environment(monkeypatch):
    """Pin everything a rendered digest reads from its environment, so a render
    can be compared exactly. Shared with the script that captured the golden.
    The zone matters: firing_since arrives as a zoneless string, and Slack's
    date token carries the epoch the host reads it as."""
    monkeypatch.setenv("TZ", "UTC")
    time.tzset()
    monkeypatch.setattr(settings, "base_url", BASE_URL)
    monkeypatch.setattr(settings.urls, "base_url", BASE_URL)


@pytest.fixture(autouse=True)
def _pinned(monkeypatch):
    pin_environment(monkeypatch)
    yield
    monkeypatch.undo()
    time.tzset()


@pytest.fixture
def flag_on(monkeypatch):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", True)


@pytest.fixture
def flag_off(monkeypatch):
    monkeypatch.setattr(settings.notifications, "digest_resolved_enabled", False)


def _event(i, account="acc-1", account_name="prod-eu", **overrides):
    """One SLO digest item as services-server publishes it while the SLO is
    firing (api-server slo.triggerNotification), once an hour."""
    event = {
        "account_id": account,
        "namespace": "payments",
        "workload": f"svc-{i}",
        "slo_name": "availability",
        "slo_type": "availability",
        "status": "FIRING",
        "finding_id": f"slo|{account}|availability|svc-{i}|payments",
        "slo_target": 0.99,
        "current_value": "0.42",
        "firing_since": "2026-07-14 06:00:00",
        "burn_rate": 14,
        "burn_rate_window": 3600,
        "burn_rate_threshold": 14.4,
        "error_budget_remaining": 37,
        "account_name": account_name,
        "threshold": 500,
        "bad_event_count": 312,
        "good_event_count": 48200,
    }
    event.update(overrides)
    return event


def _resolved(i, **overrides):
    """The same SLO once it recovers (api-server slo.triggerResolvedNotification):
    every field the model requires, no burn rate, and when it recovered."""
    fields = {"status": "RESOLVED", "current_value": "0.00", "bad_event_count": 0, "ends_at": "2026-07-14T06:50:00Z"}
    fields.update(overrides)
    event = _event(i, **fields)
    for key in ("burn_rate", "burn_rate_window", "burn_rate_threshold", "error_budget_remaining"):
        event.pop(key)
    return event


# What the templates rendered before the Resolved section existed. The last
# scenario carries resolved-status items on purpose: with the flag off they
# must still render as the ordinary items they were.
GOLDEN_SCENARIOS = {
    "under_caps": lambda: [_event(0), _event(1, account="acc-2", account_name="prod-us")],
    "over_caps": lambda: [_event(i) for i in range(12)]
    + [_event(20 + i, account=f"acc-{i}", account_name=f"cluster-{i}") for i in range(11)],
    "resolved_status": lambda: [_event(0), _resolved(1), _resolved(2, status="closed")],
}
GOLDEN_CASES = [(scenario, platform) for scenario in GOLDEN_SCENARIOS for platform in RENDERERS]


def render(platform, events):
    return json.loads(json.dumps(RENDERERS[platform](get_slo_aggregated_message_params(events))))


def _golden(scenario, platform):
    return json.loads(GOLDEN_PATH.read_text())[scenario][platform]


def _slack_text(msg):
    return "\n".join(attachment.get("text", "") for attachment in msg["attachments"])


def _teams_texts(card):
    return [block["text"] for block in card["body"]]


# ----------------------------- router -----------------------------


def _router_service(queueing_fails=False):
    service = MessageService.__new__(MessageService)
    service.queued = []

    async def enqueue(payload):
        service.queued.append(payload)
        return None if queueing_fails else {"status": "queued"}

    service.group_handler = SimpleNamespace(enqueue=enqueue)
    return service


def _slo_request(event):
    """`slo_alert` as services-server publishes it, firing or resolved."""
    payload = {"kind": "notification", "type": "slo_alert", "source": "slo", "tenant_id": TENANT, "parameters": event}
    return SimpleNamespace(
        tenant_id=TENANT,
        type="slo_alert",
        source="slo",
        thread=None,
        dict=lambda **_kwargs: json.loads(json.dumps(payload)),
    )


class TestRouter:
    def test_flag_off_resolved_slo_is_dropped_before_it_is_queued(self, flag_off):
        service = _router_service()

        result = asyncio.run(service.send_message(_slo_request(_resolved(1))))

        assert service.queued == []
        assert [response["status"] for response in result] == ["dropped"]

    def test_flag_off_drop_matches_the_status_case_insensitively(self, flag_off):
        for status in ("resolved", "Closed", " RESOLVED "):
            service = _router_service()
            result = asyncio.run(service.send_message(_slo_request(_resolved(1, status=status))))
            assert service.queued == [] and result[0]["status"] == "dropped"

    def test_flag_off_firing_slo_is_queued_as_before(self, flag_off):
        service = _router_service()

        result = asyncio.run(service.send_message(_slo_request(_event(1))))

        assert result == {"status": "queued"}
        assert [queued["parameters"]["status"] for queued in service.queued] == ["FIRING"]

    def test_flag_on_resolved_slo_is_queued_for_the_digest(self, flag_on):
        service = _router_service()

        result = asyncio.run(service.send_message(_slo_request(_resolved(1))))

        assert result == {"status": "queued"}
        (queued,) = service.queued
        # Same shape and same Redis list as a firing SLO: one digest for both.
        assert {key: queued[key] for key in ("source", "type", "tenant_id")} == {
            "source": "slo",
            "type": "slo_alert",
            "tenant_id": TENANT,
        }
        assert queued["parameters"]["status"] == "RESOLVED"
        assert queued["parameters"]["finding_id"] == "slo|acc-1|availability|svc-1|payments"

    def test_flag_on_resolved_slo_that_cannot_be_queued_is_not_sent_as_a_breach(self, flag_on):
        # enqueue() swallows a Redis error and returns None. A firing SLO then
        # goes out on its own; a resolved one would go out as that same breach
        # card, so it is dropped instead.
        service = _router_service(queueing_fails=True)

        result = asyncio.run(service.send_message(_slo_request(_resolved(1))))

        assert [response["status"] for response in result] == ["dropped"]


# ----------------------------- collapse -----------------------------


def _states(events):
    return [(event["workload"], event["status"]) for event in events]


class TestCollapse:
    """`latest_per_key` takes the batch as Redis returns it: newest first. An
    SLO item carries no `updated_at` — `firing_since` is when the breach began,
    the same on every repeat — so the newest queued state is the latest one."""

    def test_hourly_repeats_and_a_resolve_are_one_resolved_item(self):
        batch = [_resolved(1), _event(1), _event(1), _event(1)]
        assert _states(latest_per_key(batch)) == [("svc-1", "RESOLVED")]

    def test_hourly_repeats_are_one_firing_item(self):
        batch = [_event(1, current_value="0.61"), _event(1, current_value="0.42")]
        (kept,) = latest_per_key(batch)
        assert kept["current_value"] == "0.61"

    def test_resolved_then_firing_again_is_one_firing_item(self):
        batch = [_event(1), _resolved(1), _event(1)]
        assert _states(latest_per_key(batch)) == [("svc-1", "FIRING")]

    def test_items_without_a_finding_id_are_all_kept(self):
        # A services-server a release behind sends no finding_id.
        batch = [_event(1), _event(1), _event(2)]
        for event in batch:
            event.pop("finding_id")
        assert latest_per_key(batch) == batch

    def test_other_slos_keep_their_place(self):
        batch = [_event(1), _event(2), _resolved(3), _event(1), _event(3)]
        assert _states(latest_per_key(batch)) == [("svc-1", "FIRING"), ("svc-2", "FIRING"), ("svc-3", "RESOLVED")]


# ----------------------------- flush: routing and collapse -----------------------------


def _queued(event):
    return {"source": "slo", "type": "slo_alert", "tenant_id": TENANT, "parameters": event}


def _flush(service, items):
    service._group_tasks = {}
    service._flush_locks = defaultdict(asyncio.Lock)
    redis = _Redis([])
    redis.lists = {SLO_KEY: [json.dumps(item) for item in items]}
    handler = GroupedMessageHandler(SimpleNamespace(redis_client=redis), service)
    return asyncio.run(handler._flush_group(SLO_KEY)), redis


class TestFlush:
    def test_resolved_item_is_routed_like_a_firing_one(self, monkeypatch, flag_on):
        service = _service({PROD: [_deliver(OPS)], DEV: [_stop()]}, monkeypatch)
        batch = [
            _queued(_resolved(1, account=PROD)),
            _queued(_event(2, account=PROD)),
            _queued(_resolved(3, account=DEV)),
            _queued(_resolved(4, account="acc-other")),
        ]

        result, redis = _flush(service, batch)

        assert result == {"status": "ok"} and redis.deleted == [SLO_KEY]
        assert service.rule_matcher.seen == [
            ("slo_alert", "slo", PROD),
            ("slo_alert", "slo", PROD),
            ("slo_alert", "slo", DEV),
            ("slo_alert", "slo", "acc-other"),
        ]
        assert {notification_type for notification_type, *_ in service.sent} == {"grouped_slo_alert"}
        sent = {json.dumps(channel): params["events"] for _type, channel, params in service.sent}
        assert _states(sent[json.dumps(OPS)]) == [("svc-1", "RESOLVED"), ("svc-2", "FIRING")]
        # The rule that stops DEV's breaches stops their resolves too.
        assert _states(sent[json.dumps(DEFAULT)]) == [("svc-4", "RESOLVED")]
        assert set(sent) == {json.dumps(OPS), json.dumps(DEFAULT)}

    def test_each_channel_gets_one_item_per_slo_its_latest_state(self, monkeypatch, flag_on):
        service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
        batch = [
            _queued(_resolved(1, account=PROD)),
            _queued(_event(2, account=PROD)),
            _queued(_event(1, account=PROD)),
            _queued(_event(2, account=PROD)),
            _queued(_event(1, account=PROD)),
        ]

        _flush(service, batch)

        ((_type, _channel, params),) = service.sent
        assert _states(params["events"]) == [("svc-1", "RESOLVED"), ("svc-2", "FIRING")]

    def test_an_slo_that_fires_again_after_its_resolve_is_firing(self, monkeypatch, flag_on):
        service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
        batch = [_queued(_event(1, account=PROD)), _queued(_resolved(1, account=PROD))]

        _flush(service, batch)

        ((_type, _channel, params),) = service.sent
        assert _states(params["events"]) == [("svc-1", "FIRING")]

    def test_items_without_a_finding_id_are_all_kept(self, monkeypatch, flag_on):
        service = _service({PROD: [_deliver(OPS)]}, monkeypatch)
        batch = [_queued(_event(1, account=PROD)), _queued(_event(1, account=PROD))]
        for item in batch:
            item["parameters"].pop("finding_id")

        _flush(service, batch)

        ((_type, _channel, params),) = service.sent
        assert len(params["events"]) == 2

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
        assert "+18 more SLOs breaching" in _slack_text(_golden("over_caps", "slack"))
        discord = _golden("over_caps", "discord")
        assert len(discord["embeds"]) == 10 and "…and 4 more in this account." in discord["embeds"][0]["description"]
        # No SLO template truncates: a firing-only Google Chat digest runs past
        # the length its Resolved section is later fitted into.
        assert len(_golden("over_caps", "google_chat")["text"]) > MAX_TEXT_LENGTH

    def test_the_golden_holds_resolved_items_rendered_as_firing(self):
        # What a notifications-server without the section does with one.
        assert _golden("resolved_status", "slack")["text"] == "3 SLOs are burning error budget"
        assert "RESOLVED" in _golden("resolved_status", "discord")["embeds"][0]["description"]


# ----------------------------- templates: the Resolved section -----------------------------

MIXED = [_event(0), _resolved(3), _event(1), _resolved(4), _event(2)]


@pytest.mark.usefixtures("flag_on")
class TestSlack:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        msg = render("slack", MIXED)

        assert msg["text"] == "3 SLOs are burning error budget, 2 resolved"
        assert msg["blocks"][0]["text"]["text"] == msg["text"]
        assert [a["color"] for a in msg["attachments"]] == ["#C93A36"] * 3 + ["#2C8C55"]
        resolved = msg["attachments"][-1]["text"].split("\n")
        assert resolved[0] == "*Resolved*"
        assert resolved[1] == (
            "• *availability* · payments/svc-3 · prod-eu"
            f" · resolved <!date^{RESOLVED_TS}^{{date_short_pretty}} {{time}}|14 Jul 2026 06:50 AM UTC>"
        )
        assert len(resolved) == 3
        assert_no_blocks_in_attachments(msg)

    def test_resolved_items_are_not_rendered_as_firing(self):
        msg = render("slack", MIXED)
        firing_text = "\n".join(a["text"] for a in msg["attachments"][:-1])
        assert "svc-3" not in firing_text and "svc-4" not in firing_text
        assert firing_text.count("BREACHING") == 3
        assert len([b for b in _buttons(msg) if b["text"] == "Details"]) == 3

    def test_only_resolved_reads_as_resolved(self):
        msg = render("slack", [_resolved(3), _resolved(4)])
        assert msg["text"] == "2 SLOs resolved"
        assert msg["blocks"][0]["text"]["text"] == msg["text"]
        assert [a["color"] for a in msg["attachments"]] == ["#2C8C55"]
        assert not _buttons(msg)

    def test_single_resolved_is_singular(self):
        assert render("slack", [_resolved(3)])["text"] == "1 SLO resolved"
        assert render("slack", [_event(0), _resolved(3)])["text"] == "1 SLO is burning error budget, 1 resolved"

    def test_resolved_cap_and_its_own_overflow_count(self):
        msg = render("slack", [_event(0)] + [_resolved(i) for i in range(10, 18)])
        lines = msg["attachments"][-1]["text"].split("\n")
        assert len([line for line in lines if line.startswith("• ")]) == 5
        assert lines[-1] == "_+3 more resolved_"
        assert "more SLOs breaching" not in _slack_text(msg)

    def test_each_section_counts_its_own_overflow(self):
        msg = render("slack", [_event(i) for i in range(7)] + [_resolved(i) for i in range(10, 18)])
        assert msg["text"] == "7 SLOs are burning error budget, 8 resolved"
        assert [a["color"] for a in msg["attachments"]] == ["#C93A36"] * 5 + ["#94A3B8", "#2C8C55"]
        assert msg["attachments"][5]["text"] == "_+2 more SLOs breaching_"
        assert msg["attachments"][6]["text"].endswith("_+3 more resolved_")

    def test_status_is_matched_case_insensitively(self):
        msg = render("slack", [_event(0), _resolved(3, status="resolved"), _resolved(4, status="Closed")])
        assert msg["text"] == "1 SLO is burning error budget, 2 resolved"

    def test_missing_recovery_time_is_left_out(self):
        msg = render("slack", [_resolved(5, ends_at=None)])
        assert msg["attachments"][0]["text"].split("\n")[1] == "• *availability* · payments/svc-5 · prod-eu"


@pytest.mark.usefixtures("flag_on")
class TestMsTeams:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        texts = _teams_texts(render("ms_teams", MIXED))

        assert texts[0] == "🚨 **3 SLO Breach Alerts, 2 resolved across 1 accounts**"
        heading = texts.index("✅ **Resolved**")
        assert max(i for i, text in enumerate(texts) if text.startswith("• Firing Since:")) < heading
        assert texts[heading + 1 :] == [
            "- **availability** · payments/svc-3 · prod-eu · resolved 14 Jul 2026 06:50 AM UTC",
            "- **availability** · payments/svc-4 · prod-eu · resolved 14 Jul 2026 06:50 AM UTC",
        ]

    def test_resolved_items_are_not_rendered_as_firing(self):
        texts = _teams_texts(render("ms_teams", MIXED))
        assert len([text for text in texts if text.startswith("• SLO:")]) == 3
        assert not any("svc-3" in text for text in texts if text.startswith("• Namespace:"))

    def test_only_resolved_reads_as_resolved(self):
        texts = _teams_texts(render("ms_teams", [_resolved(3), _resolved(4)]))
        assert texts[0] == "✅ **2 SLO Breach Alerts resolved across 1 accounts**"
        assert not any("Review impacted workloads" in text or text.startswith("**Account:**") for text in texts)
        assert len([text for text in texts if text.startswith("- **")]) == 2

    def test_resolved_cap_and_its_own_overflow_count(self):
        texts = _teams_texts(render("ms_teams", [_event(0)] + [_resolved(i) for i in range(10, 18)]))
        assert len([text for text in texts if " · resolved " in text]) == 5
        assert texts[-1] == "+3 more resolved"


@pytest.mark.usefixtures("flag_on")
class TestGoogleChat:
    def test_mixed_digest_counts_both_and_lists_resolved_last(self):
        text = render("google_chat", MIXED)["text"]

        assert text.startswith("🚨 3 SLO Breach Alerts, 2 resolved across 1 accounts\n")
        firing, resolved = text.split("\n\n✅ *Resolved*\n")
        assert firing.count("• Firing Since:") == 3
        assert resolved.split("\n") == [
            "• *availability* · payments/svc-3 · prod-eu · resolved 14 Jul 2026 06:50 AM UTC",
            "• *availability* · payments/svc-4 · prod-eu · resolved 14 Jul 2026 06:50 AM UTC",
        ]

    def test_resolved_items_are_not_rendered_as_firing(self):
        firing = render("google_chat", MIXED)["text"].split("\n\n✅ *Resolved*\n")[0]
        assert firing.count("• SLO:") == 3 and "svc-3" not in firing and "svc-4" not in firing

    def test_only_resolved_reads_as_resolved(self):
        text = render("google_chat", [_resolved(3), _resolved(4)])["text"]
        assert text.startswith("✅ 2 SLO Breach Alerts resolved across 1 accounts\n\n✅ *Resolved*\n• *availability*")
        assert "Review impacted workloads" not in text and "*Account:*" not in text

    def test_resolved_cap_and_its_own_overflow_count(self):
        text = render("google_chat", [_event(0)] + [_resolved(i) for i in range(10, 18)])["text"]
        assert text.count(" · resolved ") == 5
        assert text.endswith("\n+3 more resolved")

    def test_truncation_never_reaches_the_resolved_section(self):
        # Worst case for the reserve: long names, a full section, and a firing
        # part that alone runs past what Google Chat takes.
        resolved = [_resolved(i, slo_name="s" * 400, workload="w" * 200, account_name="c" * 200) for i in range(50, 58)]
        text = render("google_chat", GOLDEN_SCENARIOS["over_caps"]() + resolved)["text"]

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

        assert payload["content"] == "🚨 3 SLO alerts, 2 resolved across 1 account(s)"
        account, resolved = payload["embeds"]
        assert account["title"] == "Account: prod-eu" and account["description"].count("\n") == 2
        assert resolved["title"] == "Resolved" and resolved["color"] != account["color"]
        assert resolved["description"].split("\n") == [
            "• **availability** · payments/svc-3 · prod-eu · resolved 14 Jul 2026 06:50 AM UTC",
            "• **availability** · payments/svc-4 · prod-eu · resolved 14 Jul 2026 06:50 AM UTC",
        ]

    def test_resolved_items_are_not_rendered_as_firing(self):
        account = render("discord", MIXED)["embeds"][0]
        assert "svc-3" not in account["description"] and "RESOLVED" not in account["description"]

    def test_only_resolved_reads_as_resolved(self):
        payload = render("discord", [_resolved(3), _resolved(4)])
        assert payload["content"] == "✅ 2 SLO alerts resolved across 1 account(s)"
        assert [embed["title"] for embed in payload["embeds"]] == ["Resolved"]

    def test_resolved_cap_and_its_own_overflow_count(self):
        payload = render("discord", [_event(0)] + [_resolved(i) for i in range(10, 18)])
        lines = payload["embeds"][-1]["description"].split("\n")
        assert len(lines) == 6 and lines[-1] == "…and 3 more resolved."

    def test_resolved_embed_keeps_its_place_under_the_embed_limit(self):
        firing = [_event(i, account=f"acc-{i}", account_name=f"cluster-{i}") for i in range(12)]
        payload = render("discord", firing + [_resolved(20, account="acc-1", account_name="cluster-1")])
        assert len(payload["embeds"]) == 10
        assert payload["embeds"][-1]["title"] == "Resolved"
        assert payload["content"] == "🚨 12 SLO alerts, 1 resolved across 12 account(s), 3 account(s) not shown"

    def test_tenth_account_pushed_out_by_the_resolved_embed_is_counted(self):
        firing = [_event(i, account=f"acc-{i}", account_name=f"cluster-{i}") for i in range(10)]

        without = render("discord", firing)
        assert len(without["embeds"]) == 10 and "not shown" not in without["content"]

        payload = render("discord", firing + [_resolved(20, account="acc-20", account_name="cluster-20")])
        assert [embed["title"] for embed in payload["embeds"]][-2:] == ["Account: cluster-8", "Resolved"]
        assert payload["content"] == "🚨 10 SLO alerts, 1 resolved across 11 account(s), 1 account(s) not shown"


# ----------------------------- a resolved item never fails the digest -----------------------------

# The least a recovery can say: which SLO, where. One item failing validation
# fails its channel's whole flush, which keeps the Redis list and re-sends the
# firing digest every hour.
MINIMAL = {
    "status": "RESOLVED",
    "finding_id": "slo|acc-1|availability|svc-9|payments",
    "account_id": "acc-1",
    "namespace": "payments",
    "workload": "svc-9",
    "slo_name": "availability",
}


def _resolved_lines(platform, rendered):
    """The Resolved section's lines, without the platform's bullet and bold."""
    if platform == "slack":
        lines = rendered["attachments"][-1]["text"].split("\n")[1:]
    elif platform == "ms_teams":
        texts = _teams_texts(rendered)
        lines = texts[texts.index("✅ **Resolved**") + 1 :]
    elif platform == "google_chat":
        lines = rendered["text"].split("\n\n✅ *Resolved*\n")[1].split("\n")
    else:
        lines = rendered["embeds"][-1]["description"].split("\n")
    return [line.lstrip("•- ").replace("*", "") for line in lines]


@pytest.mark.usefixtures("flag_on")
class TestMinimalResolvedItem:
    @pytest.mark.parametrize("platform", RENDERERS)
    def test_minimal_fields_render(self, platform):
        assert _resolved_lines(platform, render(platform, [MINIMAL])) == ["availability · payments/svc-9 · acc-1"]

    @pytest.mark.parametrize("platform", RENDERERS)
    def test_minimal_item_does_not_disturb_the_firing_ones(self, platform):
        rendered = render(platform, [_event(0), MINIMAL])
        assert len(_resolved_lines(platform, rendered)) == 1
        assert "svc-0" in json.dumps(rendered)

    @pytest.mark.parametrize("platform", RENDERERS)
    def test_status_alone_renders(self, platform):
        assert _resolved_lines(platform, render(platform, [{"status": "closed"}])) == ["SLO"]

    @pytest.mark.parametrize("platform", RENDERERS)
    def test_null_fields_render(self, platform):
        item = {**MINIMAL, "firing_since": None, "bad_event_count": None, "slo_target": None, "ends_at": None}
        assert len(_resolved_lines(platform, render(platform, [item]))) == 1

    def test_flag_off_minimal_item_still_validates(self, flag_off):
        # Queued while the section was on, flushed after it was turned off.
        assert render("slack", [_event(0), MINIMAL])["text"] == "2 SLOs are burning error budget"

    def test_a_firing_item_still_needs_its_numbers(self):
        firing = _event(0)
        firing.pop("slo_target")
        with pytest.raises(ValidationError):
            get_slo_aggregated_message_params([firing])
