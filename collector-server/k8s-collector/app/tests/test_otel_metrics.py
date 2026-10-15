"""OTel metric invariants that fail silently when broken.

The prometheus_client -> OTLP migration depends on three things nothing else
checks: instruments created on the proxy meter at import must rebind once the
real MeterProvider is installed, observable gauges must report the value the
implementation stored, and the cardinality View must strip tenant_id/account_id
from the handler histogram while the counter keeps them.
"""

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
from opentelemetry.metrics import set_meter_provider
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import InMemoryMetricReader

# Imported before set_meter_provider() below -- the order app.py uses, so every
# instrument in the module is created on a _ProxyMeter.
from metrics import otel_metrics

HISTOGRAM = "k8s_collector_handler_processing_duration_seconds"
COUNTER = "k8s_collector_handler_processing_total"
ERRORS = "k8s_collector_handler_errors_total"

# Every View is matched to its instrument by name, so a rename on either side
# silently orphans the View and the histogram falls back to OTel default buckets.
BUCKETED = [
    (HISTOGRAM, otel_metrics._DURATION_BUCKETS),
    ("rabbitmq_message_processing_duration_seconds", otel_metrics._DURATION_BUCKETS),
    ("rabbitmq_message_size_bytes", otel_metrics._SIZE_BUCKETS),
]


@pytest.fixture(scope="module")
def collected():
    reader = InMemoryMetricReader()
    set_meter_provider(MeterProvider(metric_readers=[reader], views=otel_metrics.metric_views()))

    for tenant in ("t1", "t2", "t3"):
        for account in ("a1", "a2"):
            otel_metrics.record_handler_processing("discovery", tenant, account, "sync", 0.25, True)
    otel_metrics.set_consumer_health("queue-a", True)
    otel_metrics.record_message_processed("queue-a", 0.25)
    otel_metrics.record_message_received("queue-a", 2048)
    with otel_metrics.track_handler("spend", "t1", "a1", "spend"):
        pass
    with pytest.raises(KeyError):
        with otel_metrics.track_handler("spend", "t1", "a1", "spend"):
            raise KeyError("boom")

    tasks = otel_metrics.get_consumer_active_tasks_gauge("queue-a")
    tasks.inc()
    tasks.inc()
    tasks.dec()

    data = reader.get_metrics_data()
    return {m.name: m for rm in data.resource_metrics for sm in rm.scope_metrics for m in sm.metrics}


def test_proxy_instruments_emit_after_provider_install(collected):
    assert COUNTER in collected, "instruments created on the proxy meter did not rebind to the real provider"


def test_observable_gauge_reports_stored_value(collected):
    # Filtered to queue-a: this ObservableGauge is backed by a module-global
    # registry keyed by queue_name, so other tests' RabbitConsumer instances
    # (e.g. test_consumer_lifecycle.py) add their own entries to the same
    # registry when the full suite runs in one process.
    points = list(collected["rabbitmq_consumer_health"].data.data_points)
    queue_a_points = [(dict(p.attributes), p.value) for p in points if p.attributes.get("queue_name") == "queue-a"]
    assert queue_a_points == [({"queue_name": "queue-a"}, 1)]


def test_view_bounds_histogram_cardinality(collected):
    histogram = [p for p in collected[HISTOGRAM].data.data_points if p.attributes["handler_type"] == "discovery"]
    assert len(histogram) == 1, "View must collapse 3 tenants x 2 accounts into one histogram series"
    assert dict(histogram[0].attributes) == {"handler_type": "discovery", "action": "sync"}
    counter = [p for p in collected[COUNTER].data.data_points if p.attributes["handler_type"] == "discovery"]
    assert len(counter) == 6, "counter must keep tenant_id/account_id (one series per tenant/account pair)"


def test_track_handler_records_outcome_and_error(collected):
    statuses = {
        p.attributes["status"]: p.value
        for p in collected[COUNTER].data.data_points
        if p.attributes["handler_type"] == "spend"
    }
    assert statuses == {"success": 1, "failed": 1}
    errors = [(dict(p.attributes), p.value) for p in collected[ERRORS].data.data_points]
    assert errors == [({"handler_type": "spend", "tenant_id": "t1", "account_id": "a1", "error_type": "KeyError"}, 1)]


@pytest.mark.parametrize("name,buckets", BUCKETED, ids=[n for n, _ in BUCKETED])
def test_histogram_buckets_match_prometheus_boundaries(collected, name, buckets):
    assert name in collected, f"{name} never recorded -- its View has nothing to apply to"
    point = list(collected[name].data.data_points)[0]
    assert list(point.explicit_bounds) == list(buckets), f"{name} fell back to OTel default buckets"


def test_gauge_deltas_accumulate_per_attribute_set(collected):
    # inc/inc/dec is rabbitmq_client's in-flight accounting; .get() and the
    # exported value must agree, and neither may drift from the net delta.
    assert otel_metrics.get_consumer_active_tasks_gauge("queue-a").get() == 1
    points = list(collected["rabbitmq_consumer_active_tasks"].data.data_points)
    queue_a_points = [(dict(p.attributes), p.value) for p in points if p.attributes.get("queue_name") == "queue-a"]
    assert queue_a_points == [({"queue_name": "queue-a"}, 1)]


# The fixture above builds its own MeterProvider, so it cannot catch app.py
# dropping `views=metric_views()` or crashing on a bad interval. These two run the
# real setup_telemetry() -- in a subprocess, because it installs a global provider.
APP_ENV = {
    "ACTION_API_SERVER_TOKEN": "",
    "CLICKHOUSE_HOST": "",
    "CLICKHOUSE_PASSWORD": "",
    "CLICKHOUSE_USER": "",
    "COLLECTOR_DB_URL": "postgresql://test:test@localhost/test",
    "COLLECTOR_MODE": "server",
    "NUDGEBEE_ENCRYPTION_KEY": "test",
    "RABBIT_MQ_HOST": "localhost",
    "RABBIT_MQ_PASSWORD": "guest",
    "RABBIT_MQ_PORT": "5672",
    "RABBIT_MQ_USERNAME": "guest",
    "REDIS_SERVER_HOST": "localhost",
    "REDIS_SERVER_PORT": "6379",
    "REDIS_USER_NAME": "",
    "REDIS_USER_PASSWORD": "",
    "OTEL_EXPORTER": "console",
}


def _run_app(extra_env, code):
    env = {**os.environ, **APP_ENV, **extra_env}
    return subprocess.run(
        [sys.executable, "-c", code],
        cwd=Path(__file__).parents[1],
        env=env,
        capture_output=True,
        text=True,
    )


def _console_payloads(stdout):
    """Decode the ConsoleMetricExporter blobs, skipping the single-line JSON logs.

    Each flush pretty-prints its own object, so the tail is several concatenated
    documents rather than one; they start at the first bare "{".
    """
    lines = stdout.splitlines()
    text = "\n".join(lines[lines.index("{") :])
    decoder = json.JSONDecoder()
    index = 0
    while index < len(text):
        payload, index = decoder.raw_decode(text, index)
        yield payload
        index += len(text[index:]) - len(text[index:].lstrip())


def test_app_installs_the_cardinality_views():
    result = _run_app(
        {},
        "import app; from metrics import otel_metrics; "
        "from opentelemetry.metrics import get_meter_provider; "
        "otel_metrics.record_handler_processing('discovery', 't1', 'a1', 'sync', 0.25, True); "
        "get_meter_provider().force_flush()",
    )
    assert result.returncode == 0, result.stderr
    histogram = next(
        m
        for payload in _console_payloads(result.stdout)
        for rm in payload["resource_metrics"]
        for sm in rm["scope_metrics"]
        for m in sm["metrics"]
        if m["name"] == HISTOGRAM
    )
    point = histogram["data"]["data_points"][0]
    assert set(point["attributes"]) == {"handler_type", "action"}, "app.py did not pass views=metric_views()"
    assert point["explicit_bounds"] == list(otel_metrics._DURATION_BUCKETS)


def test_bad_export_interval_does_not_crash_startup():
    result = _run_app({"OTEL_METRIC_EXPORT_INTERVAL": "not-a-number"}, "import app")
    assert result.returncode == 0, "a malformed chart value must degrade, not CrashLoopBackOff"
    assert "falling back to 60000ms" in result.stdout + result.stderr
