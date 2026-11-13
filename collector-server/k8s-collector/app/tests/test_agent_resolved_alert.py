"""A resolved Alertmanager alert forwarded by the Go agent must close its open
events, not be stored as one more firing.

The Go agent sends a resolved alert as an ordinary finding: same title, no
RESOLVED marker, the alert itself carried verbatim as a `json` evidence block.
The collector has to read the alert's own `status` to tell the two apart.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_resolved_event_notify.py.
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

for _mod in (
    "redis",
    "psycopg2",
    "psycopg2.extras",
    "psycopg2.pool",
    "clickhouse_driver",
    "clickhouse_connect",
    "clickhouse_connect.driver",
    "clickhouse_connect.driver.client",
    "clickhouse_connect.driver.query",
    "clickhouse_connect.driver.summary",
):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

import handlers.event_handler as eh  # noqa: E402

_FINGERPRINT = "a1b2c3d4e5f60718"
_STARTS_AT = "2026-06-01T10:00:00.119Z"
_ENDS_AT = "2026-06-01T10:15:30.119Z"
_ZERO_TIME = "0001-01-01T00:00:00Z"


def _raw_alert(status, ends_at):
    return {
        "startsAt": _STARTS_AT,
        "endsAt": ends_at,
        "generatorURL": "http://prometheus.example.com/graph",
        "fingerprint": _FINGERPRINT,
        "status": status,
        "labels": {"alertname": "DatabaseDown", "namespace": "shop", "statefulset": "postgres", "severity": "critical"},
        "annotations": {"summary": "PostgreSQL is DOWN in shop (0 ready replicas)"},
    }


def _agent_evidence(raw_alert):
    """The evidence envelope the Go agent posts: blocks JSON-encoded into `data`."""
    blocks = [{"type": "json", "data": json.dumps(raw_alert), "additional_info": {}}]
    return [{"issue_id": "f-1", "file_type": "structured_data", "data": json.dumps(blocks), "account_id": "acct-1"}]


def _agent_message(status, ends_at=_ZERO_TIME, source="prometheus"):
    return {
        "tenant": "t-1",
        "cloud_account_id": "acct-1",
        "finding": {
            "id": "f-1",
            "title": "PostgreSQL is DOWN in shop (0 ready replicas)",
            "description": "StatefulSet postgres has 0 ready replicas.",
            "source": source,
            "aggregation_key": "DatabaseDown",
            "failure": False,
            "finding_type": "issue",
            "category": "",
            "priority": "HIGH",
            "subject_type": "StatefulSet",
            "subject_name": "postgres",
            "subject_namespace": "shop",
            "subject_node": "",
            "service_key": "shop/postgres",
            "starts_at": _STARTS_AT,
            "fingerprint": _FINGERPRINT,
        },
        "evidence": _agent_evidence(_raw_alert(status, ends_at)),
    }


class TestAgentAlertResolution(unittest.TestCase):
    def _resolution(self, message):
        return eh._agent_alert_resolution(message["finding"], message["evidence"])

    def test_resolved_alert_returns_its_end_time(self):
        self.assertEqual(self._resolution(_agent_message("resolved", _ENDS_AT)), (True, "2026-06-01T10:15:30.119000Z"))

    def test_firing_alert_is_not_a_resolve(self):
        self.assertEqual(self._resolution(_agent_message("firing")), (False, None))

    def test_resolved_alert_without_end_time(self):
        self.assertEqual(self._resolution(_agent_message("resolved", _ZERO_TIME)), (True, None))
        self.assertEqual(self._resolution(_agent_message("resolved", "not-a-time")), (True, None))

    def test_only_prometheus_findings_are_read(self):
        # A Kubernetes object carried the same way can have any `status` it likes.
        message = _agent_message("resolved", _ENDS_AT, source="kubernetes_api_server")
        self.assertEqual(self._resolution(message), (False, None))

    def test_evidence_without_a_raw_alert(self):
        finding = _agent_message("resolved")["finding"]
        table = [{"type": "table", "data": {"table_name": "*Alert labels*", "rows": [["status", "resolved"]]}}]
        for evidence in (
            [],
            None,
            [{"data": "{not json"}],
            [{"data": json.dumps([{"type": "json", "data": "{not json"}])}],
            [{"data": json.dumps(table)}],
            # The agent's replacement for an over-sized payload: valid JSON, no status.
            [{"data": json.dumps([{"type": "json", "data": json.dumps({"truncated": True, "bytes": 300000})}])}],
        ):
            self.assertEqual(eh._agent_alert_resolution(finding, evidence), (False, None), evidence)


class TestRunEventHandlerResolvedAlert(unittest.TestCase):
    def _run(self, message):
        with mock.patch.object(eh.database, "select_data", return_value=[("acct-1", "cluster-1")]), mock.patch.object(
            eh.database, "run_query", return_value=[]
        ), mock.patch.object(eh, "_call_trigger_investigation") as trigger, mock.patch.object(
            eh, "handle_triggers"
        ) as handle_triggers, mock.patch.object(
            eh, "get_last_config_change", return_value=[]
        ), mock.patch.object(
            eh, "store_pod_profile"
        ), mock.patch.object(
            eh, "handle_existing_resolved_event"
        ) as close_by_title, mock.patch.object(
            eh, "_write_events_to_clickhouse"
        ) as clickhouse:
            eh.run_event_handler_async(message)
        return trigger, handle_triggers, close_by_title, clickhouse

    def test_resolved_alert_closes_and_stores_nothing(self):
        trigger, handle_triggers, close_by_title, clickhouse = self._run(_agent_message("resolved", _ENDS_AT))

        trigger.assert_called_once()
        tenant, events = trigger.call_args[0]
        self.assertEqual(tenant, "t-1")
        self.assertEqual(len(events), 1)
        event = events[0]
        self.assertEqual(event["status"], "RESOLVED")
        self.assertEqual(event["fingerprint"], _FINGERPRINT)
        self.assertEqual(event["source"], "prometheus")
        self.assertEqual(event["account_id"], "acct-1")
        # The alert's own end time, not the time the resolve was processed.
        self.assertEqual(event["ends_at"], "2026-06-01T10:15:30.119000Z")
        # Everything trigger_investigation refuses a payload without.
        for required in ("finding_id", "aggregation_key", "title", "tenant", "finding_type", "priority", "cluster"):
            self.assertTrue(event[required], required)
        self.assertEqual(event["subject_name"], "postgres")

        # A resolve is not a new occurrence: no triggers, no stored copy.
        handle_triggers.assert_not_called()
        clickhouse.assert_not_called()
        close_by_title.assert_not_called()

    def test_resolved_alert_without_end_time_omits_ends_at(self):
        trigger, _, _, _ = self._run(_agent_message("resolved", _ZERO_TIME))
        event = trigger.call_args[0][1][0]
        self.assertEqual(event["status"], "RESOLVED")
        self.assertNotIn("ends_at", event)

    def test_resolved_alert_without_fingerprint_is_dropped(self):
        message = _agent_message("resolved", _ENDS_AT)
        message["finding"]["fingerprint"] = ""
        trigger, _, _, _ = self._run(message)
        trigger.assert_not_called()

    def test_firing_alert_is_still_stored_as_firing(self):
        trigger, handle_triggers, _, _ = self._run(_agent_message("firing"))

        trigger.assert_called_once()
        event = trigger.call_args[0][1][0]
        self.assertEqual(event["status"], "FIRING")
        self.assertEqual(event["fingerprint"], _FINGERPRINT)
        handle_triggers.assert_called_once()

    def test_title_marked_resolve_keeps_its_own_path(self):
        message = _agent_message("firing")
        message["finding"]["title"] = "[RESOLVED] PostgreSQL is DOWN in shop"
        open_event = {"id": "evt-1", "status": "FIRING", "tenant": "t-1"}
        with mock.patch.object(eh.database, "select_data", return_value=[("acct-1", "cluster-1")]), mock.patch.object(
            eh.database, "run_query", return_value=[open_event]
        ), mock.patch.object(eh, "get_resource_info", return_value=("", [])), mock.patch.object(
            eh, "_call_trigger_investigation"
        ) as trigger, mock.patch.object(
            eh, "handle_existing_resolved_event"
        ) as close_by_title:
            eh.run_event_handler_async(message)

        close_by_title.assert_called_once_with("acct-1", open_event)
        trigger.assert_not_called()


if __name__ == "__main__":
    unittest.main()
