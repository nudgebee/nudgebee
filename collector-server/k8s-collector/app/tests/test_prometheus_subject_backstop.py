"""Prometheus-source agent alerts must not keep the alertname as their subject.

The Go agent's alertSubject() only walks workload-level labels, so alerts whose
only workload hint is `container_id` (ApplicationAPIFailures) or a `service`
label (NBLLMLatencyP95High) arrive with subject_name=<alertname> and an empty
subject_type. The collector backstop resolves the real workload from the alert
labels; pod-scoped prometheus alerts additionally get subject_owner enrichment
without their Alertmanager fingerprint being rewritten (FIRING/RESOLVED pairs
match on it).

Runs offline: heavy deps are stubbed before importing the handler, same as
test_pod_rightsizing_classification.py.
"""

import json
import os
import sys
import unittest
from unittest import mock

_ENV_DEFAULTS = {
    "ENV": "DEV",
    "COLLECTOR_MODE": "worker",
    "COLLECTOR_DB_URL": "postgresql://u:p@localhost:5432/db?sslmode=disable",
    "NUDGEBEE_ENCRYPTION_KEY": "test-key",
    "ACTION_API_SERVER_TOKEN": "",
    "SERVICE_API_SERVER_URL": "http://localhost:8000",
    "RABBIT_MQ_HOST": "localhost",
    "RABBIT_MQ_PORT": "5672",
    "RABBIT_MQ_USERNAME": "guest",
    "RABBIT_MQ_PASSWORD": "guest",
    "REDIS_SERVER_HOST": "localhost",
    "REDIS_SERVER_PORT": "6379",
    "REDIS_USER_NAME": "",
    "REDIS_USER_PASSWORD": "",
    "CLICKHOUSE_ENABLED": "false",
    "CLICKHOUSE_HOST": "",
    "CLICKHOUSE_USER": "",
    "CLICKHOUSE_PASSWORD": "",
}
for _k, _v in _ENV_DEFAULTS.items():
    os.environ.setdefault(_k, _v)

for _mod in ("redis", "psycopg2", "psycopg2.extras", "psycopg2.pool", "clickhouse_driver"):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

import handlers.event_handler as eh  # noqa: E402

TENANT = "tenant-1"
ACCOUNT = "account-1"


def _alertname_finding(alertname, namespace=""):
    """Finding shape the Go agent sends when alertSubject() found no workload."""
    return {
        "subject_name": alertname,
        "subject_type": "",
        "subject_namespace": namespace,
        "aggregation_key": alertname,
        "service_key": f"{namespace}/{alertname}",
        "source": "prometheus",
        "fingerprint": "am-fingerprint",
    }


class TestResolvePrometheusSubject(unittest.TestCase):
    def setUp(self):
        with eh._owner_cache_lock:
            eh._owner_cache.clear()

    def test_destination_workload_label_wins(self):
        # Real ApplicationAPIFailures labels observed on test PG (2026-08-07)
        finding = _alertname_finding("ApplicationAPIFailures")
        labels = {
            "alertgroup": "custom-alerts",
            "alertname": "ApplicationAPIFailures",
            "destination_workload_name": "relay-server",
            "destination_workload_namespace": "payments",
            "severity": "critical",
        }
        with mock.patch.object(eh.database, "run_query", return_value=[("Deployment",)]):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "relay-server")
        self.assertEqual(finding["subject_type"], "deployment")
        self.assertEqual(finding["subject_namespace"], "payments")
        self.assertEqual(finding["service_key"], "payments/relay-server")

    def test_bare_kube_state_metrics_workload_is_skipped(self):
        finding = _alertname_finding("KubeDeploymentReplicasMismatch", namespace="monitoring")
        labels = {"deployment": "kube-state-metrics"}
        eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "KubeDeploymentReplicasMismatch")

    def test_real_agent_evidence_end_to_end(self):
        # Full evidence blob shape as stored by the Go agent (raw alert JSON block)
        raw_alert = {
            "startsAt": "2026-08-07T17:10:15Z",
            "fingerprint": "a4863bc8a3864cc4",
            "status": "firing",
            "labels": {
                "alertname": "ApplicationAPIFailures",
                "destination_workload_name": "relay-server",
                "destination_workload_namespace": "payments",
                "severity": "critical",
            },
            "annotations": {"summary": "High error rate for relay-server in payments"},
        }
        evidence = [{"data": json.dumps([{"type": "json", "data": json.dumps(raw_alert), "additional_info": {}}])}]
        labels = eh.get_event_labels(evidence)
        finding = _alertname_finding("ApplicationAPIFailures")
        with mock.patch.object(eh.database, "run_query", return_value=[("Deployment",)]):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "relay-server")
        self.assertEqual(finding["subject_namespace"], "payments")

    def test_container_id_resolves_workload_from_inventory(self):
        finding = _alertname_finding("ApplicationAPIFailures")
        labels = {"container_id": "/k8s/payments/relay-server-54ff84fc95-x1/relay-server"}
        pod_row = {
            "cloud_resource_id": "cr-1",
            "workload_type": "Deployment",
            "name": "relay-server",
            "namespace": "payments",
        }
        with mock.patch.object(eh.database, "run_query", return_value=[pod_row]):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "relay-server")
        self.assertEqual(finding["subject_type"], "deployment")
        self.assertEqual(finding["subject_namespace"], "payments")
        self.assertEqual(finding["service_key"], "payments/relay-server")

    def test_container_id_falls_back_to_container_segment(self):
        finding = _alertname_finding("ApplicationAPIFailures")
        labels = {"container_id": "/k8s/billing/services-server-574d6c9d65-l1/services-server"}

        def run_query(query, params, **kwargs):
            if "SELECT kind FROM k8s_workloads" in str(query):
                return [("Deployment",)]
            return []  # pod gone from k8s_pods

        with mock.patch.object(eh.database, "run_query", side_effect=run_query):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "services-server")
        self.assertEqual(finding["subject_type"], "deployment")
        self.assertEqual(finding["subject_namespace"], "billing")
        self.assertEqual(finding["service_key"], "billing/services-server")

    def test_service_label_resolves_subject(self):
        finding = _alertname_finding("NBLLMLatencyP95High", namespace="nudgebee")
        labels = {"service": "llm-server", "provider": "googleai", "model": "gemini-3.1-pro-preview"}
        with mock.patch.object(eh.database, "run_query", return_value=[("Deployment",)]):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "llm-server")
        self.assertEqual(finding["subject_type"], "deployment")
        self.assertEqual(finding["service_key"], "nudgebee/llm-server")

    def test_service_label_unknown_workload_keeps_empty_type(self):
        finding = _alertname_finding("NBLLMLatencyP95High", namespace="nudgebee")
        labels = {"service": "llm-server"}
        with mock.patch.object(eh.database, "run_query", return_value=[]):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "llm-server")
        self.assertEqual(finding["subject_type"], "")

    def test_scrape_exporter_service_is_ignored(self):
        finding = _alertname_finding("SomeAlert", namespace="kube-system")
        labels = {"service": "kube-state-metrics"}
        with mock.patch.object(eh.database, "run_query", return_value=[]) as run_query:
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_name"], "SomeAlert")
        self.assertEqual(finding["subject_type"], "")
        run_query.assert_not_called()

    def test_container_id_with_empty_segments_leaves_finding_untouched(self):
        finding = _alertname_finding("SomeAlert")
        before = dict(finding)
        labels = {"container_id": "/k8s/payments/some-pod-x1/"}  # trailing slash: empty container segment
        with mock.patch.object(eh.database, "run_query", return_value=[]):
            eh._resolve_prometheus_subject(finding, labels, TENANT, ACCOUNT)
        self.assertEqual(finding, before)

    def test_no_usable_labels_leaves_finding_untouched(self):
        finding = _alertname_finding("Watchdog")
        before = dict(finding)
        eh._resolve_prometheus_subject(finding, {"severity": "none"}, TENANT, ACCOUNT)
        self.assertEqual(finding, before)


class TestGetEventLabelsFromAgentJSON(unittest.TestCase):
    def _agent_evidence(self, alert_labels):
        raw_alert = {"status": "firing", "labels": alert_labels, "annotations": {}}
        blocks = [{"type": "json", "data": json.dumps(raw_alert), "additional_info": {}}]
        return [{"file_type": "structured_data", "data": json.dumps(blocks)}]

    def test_extracts_labels_from_raw_alert_json(self):
        labels = eh.get_event_labels(self._agent_evidence({"service": "llm-server", "severity": "warning"}))
        self.assertEqual(labels["service"], "llm-server")
        self.assertEqual(labels["severity"], "warning")

    def test_alert_labels_table_wins_over_json(self):
        raw_alert = {"labels": {"service": "from-json"}}
        blocks = [
            {"type": "json", "data": json.dumps(raw_alert), "additional_info": {}},
            {"type": "table", "data": {"table_name": "*Alert labels*", "rows": [["service", "from-table"]]}},
        ]
        labels = eh.get_event_labels([{"data": json.dumps(blocks)}])
        self.assertEqual(labels["service"], "from-table")

    def test_malformed_json_block_is_skipped(self):
        blocks = [{"type": "json", "data": "not-json", "additional_info": {}}]
        self.assertEqual(eh.get_event_labels([{"data": json.dumps(blocks)}]), {})


class TestPrometheusPodOwnerEnrichment(unittest.TestCase):
    def setUp(self):
        with eh._owner_cache_lock:
            eh._owner_cache.clear()

    def test_prometheus_pod_gets_owner_without_fingerprint_rewrite(self):
        finding = {
            "subject_name": "fraud-detection-6d55d9d885-wplf8",
            "subject_type": "pod",
            "subject_namespace": "demo",
            "aggregation_key": "HighGrpcClientErrorRate",
            "source": "prometheus",
            "fingerprint": "am-fingerprint",
            "title": "High gRPC client error rate",
        }
        with mock.patch.object(eh.database, "run_query", return_value=[("Deployment", "fraud-detection")]):
            eh._enrich_and_normalize_fingerprint(finding, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_owner"], "fraud-detection")
        self.assertEqual(finding["subject_owner_kind"], "Deployment")
        self.assertEqual(finding["fingerprint"], "am-fingerprint")

    def test_allowlisted_pod_event_still_normalizes_fingerprint(self):
        finding = {
            "subject_name": "payment-abc123-x1",
            "subject_type": "pod",
            "subject_namespace": "demo",
            "aggregation_key": "KubePodCrashLooping",
            "source": "prometheus",
            "fingerprint": "am-fingerprint",
            "title": "Pod is crash looping",
        }
        with mock.patch.object(eh.database, "run_query", return_value=[("Deployment", "payment")]):
            eh._enrich_and_normalize_fingerprint(finding, TENANT, ACCOUNT)
        self.assertEqual(finding["subject_owner"], "payment")
        self.assertNotEqual(finding["fingerprint"], "am-fingerprint")


if __name__ == "__main__":
    unittest.main()
