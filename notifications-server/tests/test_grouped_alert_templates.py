"""Posture-format tests for the grouped Slack alert templates: batched
findings, grouped SLO, and grouped anomaly. Layout contract per the alert
redesign: headline in top-level blocks -> capped items as LEGACY colored
attachments (mrkdwn text + plain footer + URL buttons) -> neutral footer
attachment. Legacy (non-blocks) attachments matter: Slack stamps an
"Added by {app}" byline under any attachment carrying Block Kit blocks."""

from datetime import datetime, timezone
from typing import Any, Dict, List

from notifications_server.message_templates.slack.batched_findings import (
    BatchedFinding,
    BatchedFindingsPayload,
    get_batched_findings_message_template,
)
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    AnomalyAlertParams,
    get_grouped_anomaly_alerts_template,
)
from notifications_server.message_templates.slack.grouped_slo_notification import (
    get_grouped_slo_alerts_template,
)
from notifications_server.message_templates.slack.slo import SLOAlertParams, get_slo_alert_message_template


def _attachments(msg) -> List[Dict[str, Any]]:
    return msg.get("attachments", []) if isinstance(msg, dict) else []


def _text(msg_or_blocks) -> str:
    """All rendered text: top-level block mrkdwn plus legacy attachment
    text/footer strings."""
    parts: List[str] = []
    blocks = msg_or_blocks.get("blocks", []) if isinstance(msg_or_blocks, dict) else msg_or_blocks
    for b in blocks:
        if isinstance(b.get("text"), dict):
            parts.append(b["text"].get("text", ""))
        for e in b.get("elements", []) or []:
            if isinstance(e, dict) and isinstance(e.get("text"), str):
                parts.append(e["text"])
    for a in _attachments(msg_or_blocks):
        for key in ("pretext", "text", "footer"):
            if a.get(key):
                parts.append(a[key])
    return "\n".join(parts)


def _buttons(msg_or_blocks) -> List[Dict[str, Any]]:
    """Normalize Block Kit and legacy-attachment buttons to {text, url, style}."""
    out: List[Dict[str, Any]] = []
    blocks = msg_or_blocks.get("blocks", []) if isinstance(msg_or_blocks, dict) else msg_or_blocks
    for b in blocks:
        if b.get("type") == "actions":
            for e in b["elements"]:
                out.append(
                    {"text": e.get("text", {}).get("text", ""), "url": e.get("url", ""), "style": e.get("style")}
                )
    for a in _attachments(msg_or_blocks):
        for e in a.get("actions", []) or []:
            out.append({"text": e.get("text", ""), "url": e.get("url", ""), "style": e.get("style")})
    return out


def assert_no_blocks_in_attachments(msg):
    """Slack stamps an "Added by {app}" byline on attachments carrying Block
    Kit blocks (standalone row) or a footer/ts field (in the footer row) —
    verified live. None of those keys may ever ship."""
    for a in _attachments(msg):
        assert "blocks" not in a, f"attachment carries blocks: {a.get('fallback')}"
        assert "footer" not in a and "ts" not in a, f"attachment carries footer/ts: {a.get('fallback')}"
        assert "text" in a["mrkdwn_in"]


def _finding(i: int, count: int = 1, account_id: str = "acc-1") -> BatchedFinding:
    return BatchedFinding(
        id=f"f-{i}",
        title=f"Crash loop on svc-{i}",
        aggregation_key="report_crash_loop",
        severity="HIGH",
        count=count,
        cluster="prod-eu",
        subject_name=f"svc-{i}",
        subject_namespace="payments",
        account_id=account_id,
        created_at=datetime(2026, 7, 14, 9, 0, tzinfo=timezone.utc),
    )


def _findings_payload(
    criticals: List[BatchedFinding], aggregated=None, total=None, critical_count=None
) -> BatchedFindingsPayload:
    return BatchedFindingsPayload(
        organization_id="org-1",
        organization_name="TestOrg",
        accounts={"data": {"accounts": [{"id": "acc-1", "account_name": "prod-aws"}]}},
        critical_findings=criticals,
        critical_count=critical_count if critical_count is not None else 0,
        aggregated_findings=aggregated or {},
        total_findings_count=total if total is not None else sum(f.count for f in criticals),
        batch_start_time=datetime(2026, 7, 14, 8, 0, tzinfo=timezone.utc),
        batch_end_time=datetime(2026, 7, 14, 9, 0, tzinfo=timezone.utc),
    )


class TestBatchedFindings:
    def test_criticals_render_capped_with_details_buttons(self):
        payload = _findings_payload([_finding(i, count=10 - i) for i in range(5)], total=40)
        msg = get_batched_findings_message_template(payload)
        text = _text(msg)

        assert "Top critical findings in prod-aws" in text
        assert "Crash loop on svc-0" in text and "Crash loop on svc-2" in text
        assert "Crash loop on svc-3" not in text  # capped at 3
        detail_urls = [b["url"] for b in _buttons(msg) if b["text"] == "Details"]
        assert len(detail_urls) == 3
        assert "id=f-0" in detail_urls[0] and "accountId=acc-1" in detail_urls[0]

    def test_overflow_counts_events_not_items(self):
        # shown items carry 10+9+8=27 occurrences of 40 total -> +13 more
        payload = _findings_payload([_finding(i, count=10 - i) for i in range(5)], total=40)
        text = _text(get_batched_findings_message_template(payload))
        assert "+13 more findings in the window" in text

    def test_no_criticals_headline_uses_total(self):
        payload = _findings_payload([], aggregated={"acc-1": {"CPUThrottlingHigh": 7}}, total=7)
        text = _text(get_batched_findings_message_template(payload))
        assert "7 findings in prod-aws" in text

    def test_critical_count_drives_exact_headline(self):
        # 8 distinct criticals but only top-3 rendered — headline states the true 8
        payload = _findings_payload([_finding(i, count=10 - i) for i in range(5)], total=60, critical_count=8)
        text = _text(get_batched_findings_message_template(payload))
        assert "8 critical findings in prod-aws" in text
        assert "Top critical findings" not in text

    def test_single_critical_headline_is_singular(self):
        payload = _findings_payload([_finding(0)], total=1, critical_count=1)
        text = _text(get_batched_findings_message_template(payload))
        assert "1 critical finding in prod-aws" in text
        assert "1 critical findings" not in text

    def test_old_payload_without_count_falls_back(self):
        # critical_count defaults to 0 -> count-free headline, unchanged behavior
        payload = _findings_payload([_finding(0)], total=1)
        text = _text(get_batched_findings_message_template(payload))
        assert "Top critical findings in prod-aws" in text

    def test_also_seen_uses_display_names(self):
        payload = _findings_payload(
            [_finding(0)],
            aggregated={"acc-1": {"CPUThrottlingHigh": 7, "image_pull_backoff_reporter": 3}},
        )
        text = _text(get_batched_findings_message_template(payload))
        assert "Also seen: CPU throttling (7) · Image pull failure (3)" in text

    def test_single_account_footer_links_events(self):
        payload = _findings_payload([_finding(0)])
        buttons = _buttons(get_batched_findings_message_template(payload))
        view_all = [b for b in buttons if b["text"] == "View all findings"]
        assert len(view_all) == 1
        assert "accountId=acc-1" in view_all[0]["url"]

    def test_identity_facts_line(self):
        payload = _findings_payload([_finding(0)])
        msg = get_batched_findings_message_template(payload)
        assert msg["attachments"][0]["text"].endswith("High priority · svc-0 (payments) · prod-eu · Acct: prod-aws")

    def test_headline_is_a_header_block(self):
        payload = _findings_payload([_finding(0)])
        msg = get_batched_findings_message_template(payload)
        assert msg["blocks"][0]["type"] == "header"

    def test_items_are_legacy_attachments(self):
        payload = _findings_payload([_finding(0)])
        msg = get_batched_findings_message_template(payload)
        assert_no_blocks_in_attachments(msg)
        assert msg["attachments"][0]["color"] == "#C93A36"
        assert msg["attachments"][-1]["color"] == "#94A3B8"  # neutral footer


def _slo(name: str, current="99.42%", target="99.90%", burn=14, budget="22%") -> SLOAlertParams:
    return SLOAlertParams(
        account_id="acc-1",
        account_name="prod-aws",
        namespace="payments",
        workload="checkout-api",
        status="FIRING",
        slo_name=name,
        slo_type="availability",
        slo_target=target,
        current_value=current,
        firing_since=1752480000,
        bad_event_count=42,
        good_event_count=9000,
        threshold=0.999,
        burn_rate=burn,
        error_budget_remaining=budget,
    )


class TestGroupedSlo:
    def test_humanized_value_and_burn_lines(self):
        msg = get_grouped_slo_alerts_template([_slo("checkout-availability")])
        text = _text(msg)
        assert "1 SLO is burning error budget" in text
        assert "At *99.42%* against a *99.90%* target" in text
        assert "Burning budget *14× too fast* — *22%* of budget left" in text
        assert "payments/checkout-api" in text and "Acct: prod-aws" in text

    def test_firing_since_is_inline_date_token(self):
        msg = get_grouped_slo_alerts_template([_slo("s")])
        assert "firing since <!date^1752480000^" in msg["attachments"][0]["text"]

    def test_items_have_details_buttons(self):
        buttons = _buttons(get_grouped_slo_alerts_template([_slo("s")]))
        details = [b for b in buttons if b["text"] == "Details"]
        assert len(details) == 1
        assert "/kubernetes/details/acc-1" in details[0]["url"]
        assert details[0]["url"].endswith("#monitoring/slo")
        assert details[0]["style"] == "primary"

    def test_missing_burn_fields_drop_the_line(self):
        text = _text(get_grouped_slo_alerts_template([_slo("s", burn=None, budget=None)]))
        assert "too fast" not in text and "budget left" not in text

    def test_plural_headline_and_overflow(self):
        alerts = [_slo(f"slo-{i}") for i in range(7)]
        text = _text(get_grouped_slo_alerts_template(alerts))
        assert "7 SLOs are burning error budget" in text
        assert "+2 more SLOs breaching" in text

    def test_no_blocks_in_attachments(self):
        assert_no_blocks_in_attachments(get_grouped_slo_alerts_template([_slo("s")]))


class TestSingleSlo:
    def test_view_slo_button_links_to_slo_monitoring(self):
        buttons = _buttons(get_slo_alert_message_template(_slo("s")))
        view = [b for b in buttons if b["text"] == "View SLO"]
        assert len(view) == 1
        assert "/kubernetes/details/acc-1" in view[0]["url"]
        assert view[0]["url"].endswith("#monitoring/slo")
        assert view[0]["style"] == "primary"


def _anomaly(i: int, description: str = None) -> AnomalyAlertParams:
    return AnomalyAlertParams(
        id=f"a-{i}",
        title=f"NAT gateway data processing spike {i}",
        source="anomaly",
        priority="HIGH",
        status="FIRING",
        subject_name="nat-gw",
        subject_namespace="network",
        starts_at="2026-07-14T06:20:00Z",
        finding_id=f"find-{i}",
        cluster="prod-aws",
        cloud_account_id="acc-1",
        description=description,
    )


class TestGroupedAnomaly:
    def test_items_with_details_buttons(self):
        msg = get_grouped_anomaly_alerts_template([_anomaly(0), _anomaly(1)])
        text = _text(msg)
        assert "2 anomalies detected in prod-aws" in text
        assert "High priority" in text and "nat-gw (network)" in text and "Acct: prod-aws" in text
        urls = [b["url"] for b in _buttons(msg)]
        assert any("id=a-0" in u for u in urls) and any("id=a-1" in u for u in urls)

    def test_cap_and_overflow(self):
        text = _text(get_grouped_anomaly_alerts_template([_anomaly(i) for i in range(8)]))
        assert "+3 more anomalies detected" in text

    def test_items_carry_severity_stripes(self):
        msg = get_grouped_anomaly_alerts_template([_anomaly(0)])
        colors = [a["color"] for a in msg["attachments"]]
        assert colors == ["#D97A2B"]  # item stripe; no filler footer attachment

    def test_overflow_footer_attachment_is_neutral(self):
        msg = get_grouped_anomaly_alerts_template([_anomaly(i) for i in range(8)])
        assert msg["attachments"][-1]["color"] == "#94A3B8"
        assert "+3 more anomalies detected" in msg["attachments"][-1]["text"]

    def test_started_at_is_inline_date_token(self):
        msg = get_grouped_anomaly_alerts_template([_anomaly(0)])
        ts = int(datetime(2026, 7, 14, 6, 20, tzinfo=timezone.utc).timestamp())
        assert f"started <!date^{ts}^" in msg["attachments"][0]["text"]

    def test_spend_description_bolds_stats_and_drops_zscore(self):
        desc = "Daily spend of $208.00 for account prod exceeds baseline average of $14.00 (z-score: 4.32)"
        text = _text(get_grouped_anomaly_alerts_template([_anomaly(0, description=desc)]))
        assert "Daily spend of *$208.00* for account prod exceeds baseline average of *$14.00*" in text
        assert "z-score" not in text

    def test_percent_and_multiplier_stats_are_bolded(self):
        desc = "Read units at 4.5x baseline — capacity 92% consumed"
        text = _text(get_grouped_anomaly_alerts_template([_anomaly(0, description=desc)]))
        assert "*4.5x*" in text and "*92%*" in text

    def test_description_equal_to_title_is_skipped(self):
        # k8s metric anomalies set description == title; no redundant value line
        alert = _anomaly(0)
        alert.description = alert.title
        msg = get_grouped_anomaly_alerts_template([alert])
        lines = msg["attachments"][0]["text"].split("\n")
        assert lines[0] == f"*{alert.title}*"
        assert len(lines) == 2  # title + facts line, no duplicate value line

    def test_missing_description_is_safe(self):
        text = _text(get_grouped_anomaly_alerts_template([_anomaly(0)]))
        assert "NAT gateway data processing spike 0" in text

    def test_no_blocks_in_attachments(self):
        assert_no_blocks_in_attachments(get_grouped_anomaly_alerts_template([_anomaly(0)]))


class TestTimestampRobustness:
    def test_slo_millisecond_epoch_does_not_crash_render(self):
        alert = _slo("s")
        alert.firing_since = 1752480000000000  # microseconds epoch — out of range
        msg = get_grouped_slo_alerts_template([alert])
        assert "burning error budget" in msg["text"]
        assert "firing since 1752480000000000" in msg["attachments"][0]["text"]  # raw fallback, no token

    def test_anomaly_bad_timestamp_falls_back_to_raw(self):
        alert = _anomaly(0)
        alert.starts_at = "not-a-date"
        msg = get_grouped_anomaly_alerts_template([alert])
        assert "started not-a-date" in msg["attachments"][0]["text"]


class TestSloBadges:
    def test_fast_burn_is_breaching(self):
        text = _text(get_grouped_slo_alerts_template([_slo("s", burn=14)]))
        assert "`BREACHING`" in text

    def test_slow_burn_is_degrading(self):
        text = _text(get_grouped_slo_alerts_template([_slo("s", burn=4)]))
        assert "`DEGRADING`" in text

    def test_non_numeric_burn_defaults_to_breaching(self):
        text = _text(get_grouped_slo_alerts_template([_slo("s", burn=None)]))
        assert "`BREACHING`" in text
