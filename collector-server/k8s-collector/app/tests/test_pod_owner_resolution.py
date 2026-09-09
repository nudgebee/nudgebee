"""Pods whose ownerReference stops at the ReplicaSet must resolve to the real
controller: ~7% of k8s_pods rows carried RS-form workload names
("postgres-78d9cffd68"), fragmenting every consumer keyed on workload_name
(alert grouping, chronic rates, service keys, UI)."""

import os
import sys
from unittest import mock

# Environment + heavy-dependency stubs — MUST run before importing app code
# (same pattern as test_discovery_shape.py).
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

from handlers.discovery_handler import _collect_replicaset_owners, _resolve_pod_owner  # noqa: E402


def _pod(owner_name, owner_kind, labels=None, namespace="shop"):
    return {
        "type": "Pod",
        "namespace": namespace,
        "config": {
            "owner": [{"name": owner_name, "kind": owner_kind}],
            "labels": labels or {},
        },
    }


def test_deployment_owner_passes_through():
    name, kind = _resolve_pod_owner(_pod("checkout", "Deployment"), {})
    assert (name, kind) == ("checkout", "Deployment")


def test_rs_owner_resolves_via_batch_replicaset_entry():
    batch = [
        {
            "type": "ReplicaSet",
            "namespace": "shop",
            "name": "checkout-78d9cffd68",
            "config": {"owner": [{"name": "checkout", "kind": "Rollout"}]},
        }
    ]
    rs_owners = _collect_replicaset_owners(batch)
    name, kind = _resolve_pod_owner(_pod("checkout-78d9cffd68", "ReplicaSet"), rs_owners)
    # Kind comes from the RS's own ownerReference — Rollouts stay Rollouts.
    assert (name, kind) == ("checkout", "Rollout")


def test_rs_owner_falls_back_to_pod_template_hash_strip():
    pod = _pod("checkout-78d9cffd68", "ReplicaSet", labels={"pod-template-hash": "78d9cffd68"})
    name, kind = _resolve_pod_owner(pod, {})
    assert (name, kind) == ("checkout", "Deployment")


def test_rs_owner_without_hash_label_is_kept_verbatim():
    # No batch entry and no template-hash label: keep the RS rather than guess.
    name, kind = _resolve_pod_owner(_pod("checkout-78d9cffd68", "ReplicaSet"), {})
    assert (name, kind) == ("checkout-78d9cffd68", "ReplicaSet")


def test_pod_without_owner_reports_none():
    pod = {"type": "Pod", "namespace": "shop", "config": {"owner": [], "labels": {}}}
    assert _resolve_pod_owner(pod, {}) == (None, None)
