"""
Regression tests for notification-rule source matching (#28130).

A finding's `source` is the raw *ingestion* source (e.g. "datadog_webhook",
"prometheus", "anomaly", "ebpf") — never the literal "troubleshoot". Notification
rules, however, are keyed by category ("troubleshoot", "optimize", "slo", "cloud").
The matcher must map non-category finding sources to "troubleshoot", or every
troubleshoot finding falls back to the default channel.
"""

from notifications_server.services.message import (
    NotificationRuleMatcher,
    _finding_rule_source,
)


def _filters(type_, **kwargs):
    return NotificationRuleMatcher.get_filters_from_notification_payload(type_, kwargs)


# ----------------------------- _finding_rule_source -----------------------------


def test_raw_ingestion_sources_map_to_troubleshoot():
    for raw in ("datadog_webhook", "prometheus", "anomaly", "ebpf", "traces", "grafana", "newrelic"):
        assert _finding_rule_source(raw) == "troubleshoot", raw


def test_empty_or_missing_source_maps_to_troubleshoot():
    assert _finding_rule_source("") == "troubleshoot"
    assert _finding_rule_source(None) == "troubleshoot"


def test_rule_category_sources_pass_through():
    for category in ("troubleshoot", "optimize", "slo", "cloud", "auto_pilot"):
        assert _finding_rule_source(category) == category


# ----------------------------- finding filter tuple -----------------------------


def test_finding_with_ingestion_source_matches_troubleshoot_category():
    source, account_id, namespace, workload, agg = _filters(
        "finding",
        source="datadog_webhook",
        parameters={"finding": {"cloud_account_id": "acc-1", "subject_namespace": "prod", "subject_owner": "api"}},
    )
    assert source == "troubleshoot"
    assert account_id == "acc-1"
    assert namespace == "prod"
    assert workload == "api"


def test_cloud_finding_keeps_cloud_category():
    source, account_id, *_ = _filters(
        "finding",
        source="cloud",
        parameters={"finding": {"cloud_account_id": "acc-9"}},
    )
    assert source == "cloud"
    assert account_id == "acc-9"


def test_optimize_finding_keeps_optimize_category():
    # event-resolution findings are published with source="optimize"
    source, *_ = _filters(
        "finding",
        source="optimize",
        parameters={"finding": {"cloud_account_id": "acc-2"}},
    )
    assert source == "optimize"


def test_finding_account_id_falls_back_to_account_id_key():
    _, account_id, namespace, workload, _ = _filters(
        "finding",
        source="prometheus",
        parameters={"finding": {"account_id": "acc-3", "namespace": "ns", "workload": "wl"}},
    )
    assert account_id == "acc-3"
    assert namespace == "ns"
    assert workload == "wl"


# ----------------------------- non-finding (template) sources -----------------------------


def test_optimize_digest_is_tenant_wide_no_account_id():
    # tenant-wide digest carries organization_id but no account_id
    source, account_id, namespace, workload, _ = _filters(
        "recommendation_nudge_digest",
        source="optimize",
        parameters={"organization_id": "org-1", "recommendations_by_account": {}},
    )
    assert source == "optimize"
    assert account_id is None
    assert namespace is None
    assert workload is None


def test_slo_template_uses_account_id():
    source, account_id, *_ = _filters(
        "slo_alert",
        source="slo",
        parameters={"account_id": "acc-4", "namespace": "ns", "workload": "wl"},
    )
    assert source == "slo"
    assert account_id == "acc-4"


def test_cloud_template_account_only():
    source, account_id, namespace, workload, _ = _filters(
        "cloud_cost_summary",
        source="cloud",
        parameters={"account_id": "acc-5"},
    )
    assert source == "cloud"
    assert account_id == "acc-5"
    assert namespace is None
    assert workload is None
