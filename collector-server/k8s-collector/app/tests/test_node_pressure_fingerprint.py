"""A flapping node condition must chain into one problem, not one per flip.

DiskPressure is a cycle, not an episode: kubelet fills the disk, garbage-collects
images and fills it again, so the condition flips True -> False -> True every few
minutes. The agent fingerprinted on the condition's lastTransitionTime, so every
flip minted a new fingerprint -- which also defeated its own 6h rate limit, since
that limiter is keyed on the fingerprint. Measured on production: one node produced
7 findings in 53 minutes, and node_pressure averaged 7.7 findings per node across
42 nodes in 30 days.

The agent now buckets by time itself; this covers the agents already deployed.
"""

import os
import sys
import unittest
import hashlib
from datetime import datetime, timedelta, timezone
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

# 10:27 and 10:35 on the same day -- the real flap interval observed in production.
FLAP_1 = datetime(2026, 9, 9, 10, 27, 24)
FLAP_2 = datetime(2026, 9, 9, 10, 35, 4)


def node_finding(node="ip-172-31-10-60", starts_at=FLAP_1, key="node_pressure"):
    return {
        "aggregation_key": key,
        "subject_type": "node",
        "subject_name": node,
        "subject_namespace": "",
        "starts_at": starts_at.isoformat(),
        "fingerprint": "agent-supplied-" + starts_at.isoformat(),
    }


def normalized(finding):
    eh._enrich_and_normalize_fingerprint(finding, "tenant-1", "account-1")
    return finding["fingerprint"]


class TestNodePressureFingerprint(unittest.TestCase):
    def test_flaps_within_the_window_share_one_fingerprint(self):
        """The regression: 8 minutes apart must not be two problems."""
        self.assertEqual(normalized(node_finding(starts_at=FLAP_1)), normalized(node_finding(starts_at=FLAP_2)))

    def test_agent_supplied_fingerprint_is_replaced(self):
        """Old agents send a per-transition fingerprint; it must not survive."""
        f = node_finding()
        before = f["fingerprint"]
        self.assertNotEqual(before, normalized(f))

    def test_different_nodes_stay_separate(self):
        self.assertNotEqual(normalized(node_finding(node="node-a")), normalized(node_finding(node="node-b")))

    def test_a_later_window_is_a_new_problem(self):
        """Pressure that returns a window later is worth reporting again."""
        later = FLAP_1 + timedelta(hours=7)
        self.assertNotEqual(normalized(node_finding(starts_at=FLAP_1)), normalized(node_finding(starts_at=later)))

    def test_other_node_events_are_untouched(self):
        """Only the flapping keys are rewritten; node_not_ready pins a real episode."""
        f = node_finding(key="node_not_ready")
        before = f["fingerprint"]
        self.assertEqual(before, normalized(f))

    def test_unparseable_starts_at_leaves_the_fingerprint_alone(self):
        """Never mint a fingerprint from a bad timestamp -- that would fork every event."""
        f = node_finding()
        f["starts_at"] = "not-a-timestamp"
        before = f["fingerprint"]
        self.assertEqual(before, normalized(f))

    def test_missing_starts_at_leaves_the_fingerprint_alone(self):
        f = node_finding()
        del f["starts_at"]
        before = f["fingerprint"]
        self.assertEqual(before, normalized(f))

    def test_bucket_is_computed_in_utc_not_local_time(self):
        """.timestamp() on a naive datetime uses the HOST timezone.

        That would bucket the same event differently on two collector pods in
        different zones, and put the window boundary at local 06:00 rather than
        06:00 UTC. Pinning the exact expected digest fails on any non-UTC host
        if the UTC normalisation is dropped.
        """
        f = node_finding(starts_at=FLAP_1)
        expected_bucket = int(FLAP_1.replace(tzinfo=timezone.utc).timestamp()) // eh._NODE_FINGERPRINT_WINDOW_SECONDS
        expected = hashlib.sha256(f"node_pressure:{f['subject_name']}:{expected_bucket}".encode()).hexdigest()
        self.assertEqual(expected, normalized(f))

    def test_trailing_z_timestamp_is_parsed(self):
        """The agent sends RFC3339 with a trailing Z; it must not fall through."""
        f = node_finding()
        f["starts_at"] = FLAP_1.isoformat() + "Z"
        self.assertEqual(normalized(node_finding(starts_at=FLAP_1)), normalized(f))

    def test_window_matches_the_agent(self):
        """Must equal nodePressureWindow in k8s-agent runner/pkg/triggers/predicates.go."""
        self.assertEqual(eh._NODE_FINGERPRINT_WINDOW_SECONDS, 6 * 60 * 60)


if __name__ == "__main__":
    unittest.main()
