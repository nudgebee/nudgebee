"""Every consumer callback and the telemetry POST must report handler metrics.

k8s_collector_handler_* were defined for years with no caller, so the metrics
store had nothing saying which handler was slow or failing for which account.
These pin the labels each entry point records and that a failing handler still
raises, so the consumer's retry / DLQ path is unchanged.

Runs offline with the same stubs as test_disabled_account_gate.py.
"""

import json
import unittest
from unittest import mock

import tests.test_disabled_account_gate  # noqa: F401  -- env + heavy-dep stubs
import rabbitmq.rabbitmq_consumers as consumers  # noqa: E402
from controllers import open_cost_controller, telemetry_controller  # noqa: E402
from metrics import otel_metrics  # noqa: E402

TENANT = "t-1"
ACCOUNT = "a-1"


def _message(**extra):
    return {"tenant": TENANT, "cloud_account_id": ACCOUNT, "data": [], **extra}


class TestHandlerMetrics(unittest.TestCase):
    def setUp(self):
        patches = [
            mock.patch.object(consumers.Configs, "DROP_DISABLED_ACCOUNT_MESSAGES", False),
            mock.patch.object(otel_metrics, "record_handler_processing"),
            mock.patch.object(otel_metrics, "record_handler_error"),
        ]
        for p in patches:
            p.start()
            self.addCleanup(p.stop)
        self.processing = otel_metrics.record_handler_processing
        self.error = otel_metrics.record_handler_error

    def _recorded(self):
        """(handler_type, tenant, account, action, success) of the single recorded run."""
        self.processing.assert_called_once()
        handler_type, tenant, account, action, duration, success = self.processing.call_args.args
        self.assertGreaterEqual(duration, 0)
        return handler_type, tenant, account, action, success

    def test_discovery_labels_action_by_type(self):
        with mock.patch.object(consumers, "run_async_discovery_handler"):
            consumers._discovery_callback(_message(type="Service"))
        self.assertEqual(self._recorded(), ("discovery", TENANT, ACCOUNT, "service", True))
        self.error.assert_not_called()

    def test_string_payload_is_labelled(self):
        with mock.patch.object(consumers, "run_async_discovery_handler"):
            consumers._discovery_callback(json.dumps(_message(type="node")))
        self.assertEqual(self._recorded(), ("discovery", TENANT, ACCOUNT, "node", True))

    def test_unknown_discovery_type_is_collapsed(self):
        with mock.patch.object(consumers, "run_async_discovery_handler"):
            consumers._discovery_callback(_message(type="made-up-by-agent"))
        self.assertEqual(self._recorded()[3], "other")

    def test_event_report_is_named_and_alerts_are_collapsed(self):
        with mock.patch.object(consumers, "run_event_handler_async"):
            consumers._event_callback(_message(finding={"aggregation_key": "krr_report"}))
            self.assertEqual(self._recorded()[3], "krr_report")
            self.processing.reset_mock()
            consumers._event_callback(_message(finding={"aggregation_key": "KubePodCrashLooping"}))
            self.assertEqual(self._recorded()[3], "alert")

    def test_failing_handler_records_error_and_still_raises(self):
        with mock.patch.object(consumers, "process_spend", side_effect=ValueError("bad row")):
            with self.assertRaises(ValueError):
                consumers._spend_callback(_message())
        self.assertEqual(self._recorded(), ("spend", TENANT, ACCOUNT, "spend", False))
        self.error.assert_called_once_with("spend", TENANT, ACCOUNT, "ValueError")

    def test_telemetry_post_is_tracked(self):
        with mock.patch.object(telemetry_controller, "handle_telemetry"):
            telemetry_controller.TelemetryController().post_async(TENANT, ACCOUNT, "agent-1", {"version": "1"})
        self.assertEqual(self._recorded(), ("telemetry", TENANT, ACCOUNT, "heartbeat", True))

    def test_opencost_spend_is_tracked(self):
        controller = open_cost_controller.OpenCostController()
        with mock.patch.object(open_cost_controller, "process_spend"), mock.patch.object(
            controller, "update_agent_last_synced_in_db"
        ):
            controller.store_data({}, 0, 60, ACCOUNT, TENANT)
        self.assertEqual(self._recorded(), ("spend", TENANT, ACCOUNT, "opencost", True))


if __name__ == "__main__":
    unittest.main()
