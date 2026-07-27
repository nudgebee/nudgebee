"""Rendering fixes to already-migrated Slack templates: (1) daily-highlight and
events-summary must not embed Block Kit `blocks` inside attachments (the
"Added by {app}" byline anti-pattern) and must use the stripe palette; (2) the
grouped SLO and grouped anomaly cards must surface the numeric stats the
producer supplies but the renderers previously dropped."""

from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_NEUTRAL,
    STRIPE_SAVINGS,
)
from notifications_server.message_templates.slack.daily_highlight import (
    Insight,
    Recommendation,
    create_account_attachment,
)
from notifications_server.message_templates.slack.events_summary import (
    get_events_summary_message_params,
    get_events_summary_message_template,
)
from notifications_server.message_templates.slack.grouped_slo_notification import (
    get_slo_aggregated_message_params,
    get_grouped_slo_alerts_template,
)
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    get_anomaly_aggregated_message_params,
    get_grouped_anomaly_alerts_template,
)


def _slo_event(**overrides):
    event = {
        "account_id": "a1",
        "account_name": "prod-eu",
        "namespace": "payments",
        "workload": "checkout-api",
        "status": "FIRING",
        "slo_name": "checkout-availability",
        "slo_type": "availability",
        "slo_target": "99.9%",
        "current_value": "99.2%",
        "firing_since": "2026-07-14 09:02:00",
        "bad_event_count": 312,
        "good_event_count": 48200,
        "threshold": "0.1%",
        "burn_rate": "14.3",
        "error_budget_remaining": "8%",
    }
    event.update(overrides)
    return event


def _anomaly_event(**overrides):
    """Mirrors what api-server actually publishes: the post-process consumer
    nils Evidences before marshalling, so a real payload has NO `evidences`
    key at all — only `has_evidences` and whatever rides on `labels`."""
    event = {
        "id": "e1",
        "title": "CPU Anomaly detected for checkout-api in namespace payments",
        "source": "anomaly",
        "priority": "HIGH",
        "status": "FIRING",
        "subject_name": "checkout-api",
        "subject_namespace": "payments",
        "starts_at": "2026-07-20T14:07:00Z",
        "finding_id": "f1",
        "cluster": "prod-eu",
        "cloud_account_id": "a1",
        "description": "CPU Anomaly detected for checkout-api in namespace payments",
        "has_evidences": True,
    }
    event.update(overrides)
    return event


class TestBylineAndPalette:
    def test_events_summary_attachment_is_text_only(self):
        payload = get_events_summary_message_params(
            title="Events",
            organization_id="o1",
            organization_name="Acme",
            accounts={"data": {"accounts": [{"id": "a1", "account_name": "prod-eu"}]}},
            event_counts={
                "data": {
                    "event_groupings": {
                        "rows": [
                            {
                                "account_id": "a1",
                                "event_count": 34,
                                "count_priority_high": 6,
                                "count_priority_medium": 12,
                                "count_priority_low": 16,
                                "count_priority_debug": 0,
                                "count_priority_info": 0,
                                "count_application_issues": 12,
                                "count_node_issues": 4,
                                "count_pod_issues": 18,
                            }
                        ]
                    }
                }
            },
            events_summarised={
                "data": {
                    "event_groupings": {
                        "rows": [
                            {
                                "account_id": "a1",
                                "max_created_at": "2026-07-20T09:00:00",
                                "event_count": 9,
                                "aggregation_key": "report_crash_loop",
                            }
                        ]
                    }
                }
            },
        )
        out = get_events_summary_message_template(payload)
        assert out["blocks"][0]["type"] == "header"
        (att,) = out["attachments"]
        assert "blocks" not in att and "footer" not in att and "ts" not in att
        assert att["color"] == STRIPE_NEUTRAL  # not the old #2196F3
        # content survived the flatten
        assert "*Total Events:* 34" in att["text"]
        assert "High Priority:" in att["text"] and "Application Issues:" in att["text"]

    def test_daily_highlight_attachment_is_text_only(self):
        att = create_account_attachment(
            "prod-eu",
            {"app": 12, "node": 4, "pod": 18},
            {"app": 10, "node": 4, "pod": 20},
            [
                Recommendation(
                    category="rightsizing",
                    rule_name="pod_rightsizing",
                    count=5,
                    account_id="a1",
                    sum_estimated_savings=84.0,
                )
            ],
            [Insight(account_id="a1", title="High CPU", type="Troubleshooting", unique_id="u1")],
        )
        assert "blocks" not in att and "footer" not in att and "ts" not in att
        assert att["color"] == STRIPE_SAVINGS  # not the old #36a64f
        assert "*Cluster: prod-eu*" in att["text"]
        assert "Recommendations:" in att["text"] and "Highlights:" in att["text"]


class TestGroupedSloStats:
    def test_threshold_and_event_counts_now_render(self):
        out = get_grouped_slo_alerts_template(get_slo_aggregated_message_params([_slo_event()]))
        text = out["attachments"][0]["text"]
        assert "312" in text and "48,200" in text  # bad/good, thousands-formatted
        assert "threshold *0.1%*" in text

    def test_stats_line_omitted_when_threshold_missing(self):
        out = get_grouped_slo_alerts_template(get_slo_aggregated_message_params([_slo_event(threshold="N/A")]))
        text = out["attachments"][0]["text"]
        assert "threshold" not in text
        assert "bad vs" in text  # counts still shown

    def test_stats_line_safe_when_one_count_is_none(self):
        # _stats_line must require BOTH counts before formatting with `:,` —
        # formatting a None with `:,` would raise TypeError.
        from types import SimpleNamespace
        from notifications_server.message_templates.slack.grouped_slo_notification import _stats_line

        one_missing = SimpleNamespace(bad_event_count=None, good_event_count=48200, threshold="0.1%")
        line = _stats_line(one_missing)
        assert "bad vs" not in line  # the counts clause is skipped, no crash
        assert "threshold *0.1%*" in line


class TestGroupedAnomalyStats:
    """Stats come from the producer's `labels`. An earlier attempt read them
    from `evidences`, which never survives to a notification — so every case
    here uses a payload with no `evidences` key, exactly as published."""

    def test_metric_anomaly_gets_current_vs_baseline(self):
        # Metric anomalies set description == title, so they have no value line
        # at all: without labels they render zero numbers.
        out = get_grouped_anomaly_alerts_template(
            get_anomaly_aggregated_message_params(
                [_anomaly_event(labels={"anomaly_current": "820m", "anomaly_baseline": "140m"})]
            )
        )
        assert "Current *820m* vs baseline *140m*" in out["attachments"][0]["text"]

    def test_spend_anomaly_gets_zscore_and_change(self):
        out = get_grouped_anomaly_alerts_template(
            get_anomaly_aggregated_message_params(
                [
                    _anomaly_event(
                        title="Cloud Spend Anomaly: prod-aws account spend increased by 38.0%",
                        description="Daily spend of $208 exceeds baseline average of $14 (z-score: 4.20)",
                        labels={
                            "anomaly_current": "$208",
                            "anomaly_baseline": "$14",
                            "anomaly_zscore": "4.20",
                            "anomaly_change": "+$194 (+38.0%)",
                        },
                    )
                ]
            )
        )
        text = out["attachments"][0]["text"]
        assert "Z-score *4.20*" in text
        assert "*+$194 (+38.0%)*" in text
        assert "expected *$14*" in text

    def test_current_only_when_baseline_unavailable(self):
        # Some reference shapes carry no baseline; show what we do have.
        out = get_grouped_anomaly_alerts_template(
            get_anomaly_aggregated_message_params([_anomaly_event(labels={"anomaly_current": "412.5"})])
        )
        assert "Current value *412.5*" in out["attachments"][0]["text"]

    def test_payload_without_labels_renders_without_stats(self):
        out = get_grouped_anomaly_alerts_template(get_anomaly_aggregated_message_params([_anomaly_event()]))
        text = out["attachments"][0]["text"]
        assert text.startswith("*CPU Anomaly")
        assert "Current" not in text

    def test_empty_or_null_label_values_do_not_crash(self):
        # The producer sends a JSON object or null; these are the shapes that
        # can actually arrive with nothing useful in them.
        for labels in (None, {}, {"anomaly_current": None}, {"anomaly_current": ""}):
            out = get_grouped_anomaly_alerts_template(
                get_anomaly_aggregated_message_params([_anomaly_event(labels=labels)])
            )
            text = out["attachments"][0]["text"]
            assert text.startswith("*CPU Anomaly")
            assert "Current" not in text

    def test_evidences_in_payload_are_ignored(self):
        # Guards the old approach: even if a payload somehow carried evidences,
        # stats must come from labels — evidences are not a supported source.
        out = get_grouped_anomaly_alerts_template(
            get_anomaly_aggregated_message_params(
                [_anomaly_event(evidences=[{"current_value": 0.82, "reference_value": {"mean": 0.14}}])]
            )
        )
        text = out["attachments"][0]["text"]
        assert "0.82" not in text and "Current" not in text
