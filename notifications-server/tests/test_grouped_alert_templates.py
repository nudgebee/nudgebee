"""Posture-format tests for the grouped Slack alert templates: batched
findings, grouped SLO, and grouped anomaly. Layout contract per the alert
redesign: headline -> context -> capped flat items (facts + identity in a
context line, Details button) -> overflow count."""

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
from notifications_server.message_templates.slack.slo import SLOAlertParams


def _text(blocks: List[Dict[str, Any]]) -> str:
    parts: List[str] = []
    for b in blocks:
        if isinstance(b.get("text"), dict):
            parts.append(b["text"].get("text", ""))
        for e in b.get("elements", []) or []:
            if isinstance(e, dict) and isinstance(e.get("text"), str):
                parts.append(e["text"])
    return "\n".join(parts)


def _buttons(blocks: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    out = []
    for b in blocks:
        if b.get("type") == "actions":
            out.extend(b["elements"])
    return out


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


def _findings_payload(criticals: List[BatchedFinding], aggregated=None, total=None) -> BatchedFindingsPayload:
    return BatchedFindingsPayload(
        organization_id="org-1",
        organization_name="TestOrg",
        accounts={"data": {"accounts": [{"id": "acc-1", "account_name": "prod-aws"}]}},
        critical_findings=criticals,
        aggregated_findings=aggregated or {},
        total_findings_count=total if total is not None else sum(f.count for f in criticals),
        batch_start_time=datetime(2026, 7, 14, 8, 0, tzinfo=timezone.utc),
        batch_end_time=datetime(2026, 7, 14, 9, 0, tzinfo=timezone.utc),
    )


class TestBatchedFindings:
    def test_criticals_render_capped_with_details_buttons(self):
        payload = _findings_payload([_finding(i, count=10 - i) for i in range(5)], total=40)
        msg = get_batched_findings_message_template(payload)
        text = _text(msg["blocks"])

        assert "Top critical findings across 1 account" in text
        assert "Crash loop on svc-0" in text and "Crash loop on svc-2" in text
        assert "Crash loop on svc-3" not in text  # capped at 3
        buttons = _buttons(msg["blocks"])
        detail_urls = [b["url"] for b in buttons if b["text"]["text"] == "Details"]
        assert len(detail_urls) == 3
        assert "id=f-0" in detail_urls[0] and "accountId=acc-1" in detail_urls[0]

    def test_overflow_counts_events_not_items(self):
        # shown items carry 10+9+8=27 occurrences of 40 total -> +13 more
        payload = _findings_payload([_finding(i, count=10 - i) for i in range(5)], total=40)
        text = _text(get_batched_findings_message_template(payload)["blocks"])
        assert "+13 more findings in the window" in text

    def test_no_criticals_headline_uses_total(self):
        payload = _findings_payload([], aggregated={"acc-1": {"CPUThrottlingHigh": 7}}, total=7)
        text = _text(get_batched_findings_message_template(payload)["blocks"])
        assert "7 findings across 1 account" in text

    def test_also_seen_uses_display_names(self):
        payload = _findings_payload(
            [_finding(0)],
            aggregated={"acc-1": {"CPUThrottlingHigh": 7, "image_pull_backoff_reporter": 3}},
        )
        text = _text(get_batched_findings_message_template(payload)["blocks"])
        assert "Also seen: CPU throttling (7) · Image pull failure (3)" in text

    def test_single_account_footer_links_events(self):
        payload = _findings_payload([_finding(0)])
        buttons = _buttons(get_batched_findings_message_template(payload)["blocks"])
        view_all = [b for b in buttons if b["text"]["text"] == "View all findings"]
        assert len(view_all) == 1
        assert "accountId=acc-1" in view_all[0]["url"]

    def test_identity_context_line(self):
        payload = _findings_payload([_finding(0)])
        text = _text(get_batched_findings_message_template(payload)["blocks"])
        assert "High priority · svc-0 (payments) · prod-eu · Acct prod-aws" in text


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
        text = _text(get_grouped_slo_alerts_template([_slo("checkout-availability")])["blocks"])
        assert "1 SLO is burning error budget" in text
        assert "At *99.42%* against a *99.90%* target" in text
        assert "Burning budget *14× too fast* — *22%* of budget left" in text
        assert "payments/checkout-api" in text and "Acct" in text

    def test_missing_burn_fields_drop_the_line(self):
        text = _text(get_grouped_slo_alerts_template([_slo("s", burn=None, budget=None)])["blocks"])
        assert "too fast" not in text and "budget left" not in text

    def test_plural_headline_and_overflow(self):
        alerts = [_slo(f"slo-{i}") for i in range(7)]
        text = _text(get_grouped_slo_alerts_template(alerts)["blocks"])
        assert "7 SLOs are burning error budget" in text
        assert "+2 more SLOs breaching" in text


def _anomaly(i: int) -> AnomalyAlertParams:
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
    )


class TestGroupedAnomaly:
    def test_items_with_details_buttons(self):
        msg = get_grouped_anomaly_alerts_template([_anomaly(0), _anomaly(1)])
        text = _text(msg["blocks"])
        assert "2 anomalies detected across 1 account" in text
        assert "High priority" in text and "nat-gw (network)" in text and "Acct prod-aws" in text
        urls = [b["url"] for b in _buttons(msg["blocks"])]
        assert any("id=find-0" in u for u in urls) and any("id=find-1" in u for u in urls)

    def test_cap_and_overflow(self):
        text = _text(get_grouped_anomaly_alerts_template([_anomaly(i) for i in range(8)])["blocks"])
        assert "+3 more anomalies detected" in text

    def test_no_attachments_in_output(self):
        msg = get_grouped_anomaly_alerts_template([_anomaly(0)])
        assert "attachments" not in msg


class TestTimestampRobustness:
    def test_slo_millisecond_epoch_does_not_crash_render(self):
        alert = _slo("s")
        alert.firing_since = 1752480000000000  # microseconds epoch — out of fromtimestamp range
        msg = get_grouped_slo_alerts_template([alert])
        assert "burning error budget" in msg["text"]

    def test_anomaly_bad_timestamp_falls_back_to_raw(self):
        alert = _anomaly(0)
        alert.starts_at = "not-a-date"
        text = _text(get_grouped_anomaly_alerts_template([alert])["blocks"])
        assert "not-a-date" in text
