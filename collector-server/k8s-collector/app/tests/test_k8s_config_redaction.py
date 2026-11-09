"""Discovery must not store secret material from a workload's or pod's config:
the last-applied annotation (a full copy of the object, inline env values
included) and inline env values. meta.config lands in k8s_workloads, k8s_pods
and cloud_resourses, which every role that can read Kubernetes resources, and
the AI tools, can read."""

import copy
import json
import os
import sys
from dataclasses import asdict
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

from handlers.discovery_handler import process_service_discovery  # noqa: E402
from utils.k8s_config_redaction import REDACTED_VALUE, redact_k8s_config  # noqa: E402

LAST_APPLIED = "kubectl.kubernetes.io/last-applied-configuration"
RANCHER_APPLIED = "objectset.rio.cattle.io/applied"

# What `kubectl apply` leaves on a Deployment that sets a password inline.
_LAST_APPLIED_VALUE = json.dumps(
    {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "spec": {
            "template": {
                "spec": {"containers": [{"name": "api", "env": [{"name": "DB_PASSWORD", "value": "applied-pass"}]}]}
            }
        },
    }
)

_SECRETS = ("applied-pass", "inline-pass", "H4sIAAAAAAAA-rancher-gzip")


def _config():
    return {
        "labels": {"app": "api"},
        "annotations": {
            LAST_APPLIED: _LAST_APPLIED_VALUE,
            RANCHER_APPLIED: "H4sIAAAAAAAA-rancher-gzip",
            "deployment.kubernetes.io/revision": "4",
            "argocd.argoproj.io/tracking-id": "shop:apps/Deployment:prod/api",
        },
        "containers": [
            {
                "name": "api",
                "image": "api:1.2",
                "env": [
                    {"name": "DB_PASSWORD", "value": "inline-pass"},
                    {
                        "name": "API_KEY",
                        "value": None,
                        "value_from": {"secret_key_ref": {"name": "api-keys", "key": "key"}},
                    },
                    {"name": "EMPTY", "value": ""},
                ],
                "resources": {"requests": {"cpu": "100m"}},
            },
            {"name": "sidecar", "image": "proxy:1"},
        ],
        "owner": [],
    }


def test_drops_applied_copy_annotations_and_keeps_the_rest():
    out = redact_k8s_config(_config())

    assert out["annotations"] == {
        "deployment.kubernetes.io/revision": "4",
        "argocd.argoproj.io/tracking-id": "shop:apps/Deployment:prod/api",
    }


def test_redacts_inline_env_values_and_keeps_names_and_references():
    out = redact_k8s_config(_config())

    assert out["containers"][0]["env"] == [
        {"name": "DB_PASSWORD", "value": REDACTED_VALUE},
        {"name": "API_KEY", "value": None, "value_from": {"secret_key_ref": {"name": "api-keys", "key": "key"}}},
        {"name": "EMPTY", "value": ""},
    ]
    assert out["containers"][0]["resources"] == {"requests": {"cpu": "100m"}}
    assert out["containers"][1] == {"name": "sidecar", "image": "proxy:1"}
    assert out["labels"] == {"app": "api"}


def test_does_not_modify_its_input():
    config = _config()
    before = copy.deepcopy(config)

    redact_k8s_config(config)

    assert config == before


def test_is_idempotent():
    once = redact_k8s_config(_config())

    assert redact_k8s_config(once) == once


def test_leaves_other_shapes_alone():
    assert redact_k8s_config(None) is None
    assert redact_k8s_config({"labels": {}}) == {"labels": {}}
    assert redact_k8s_config({"annotations": None, "containers": None}) == {"annotations": None, "containers": None}
    odd = {"containers": ["not-a-dict", {"name": "x", "env": "not-a-list"}, {"name": "y", "env": ["not-a-dict"]}]}
    assert redact_k8s_config(odd) == odd


def _discover(k8s_type):
    k8s_data = {
        "service_key": f"prod/{k8s_type}/api",
        "type": k8s_type,
        "deleted": False,
        "name": "api",
        "namespace": "prod",
        "creation_time": "2024-01-01T00:00:00Z",
        "update_time": 1_700_000_000_000,
        "status": "Running",
        "total_pods": 1,
        "ready_pods": 1,
        "config": _config(),
    }
    before = copy.deepcopy(k8s_data)
    pods, resources, workloads = [], [], []
    process_service_discovery(
        "id-1", "account-1", k8s_data, pods, resources, k8s_data["service_key"], "tenant-1", workloads
    )
    assert k8s_data == before, "discovery must not modify the agent's payload"
    rows = pods + workloads
    assert len(rows) == 1 and len(resources) == 1
    return json.dumps(asdict(rows[0])["meta"]), resources[0]["meta"]


def test_discovery_stores_no_secret_material_for_workloads_and_pods():
    for k8s_type in ("Deployment", "Pod"):
        row_meta, cloud_resource_meta = _discover(k8s_type)
        for stored in (row_meta, cloud_resource_meta):
            assert LAST_APPLIED not in stored
            assert RANCHER_APPLIED not in stored
            for secret in _SECRETS:
                assert secret not in stored, f"{k8s_type}: {secret} stored"
            # What readers rely on is still there.
            config = json.loads(stored)["config"]
            assert config["annotations"]["argocd.argoproj.io/tracking-id"] == "shop:apps/Deployment:prod/api"
            assert config["annotations"]["deployment.kubernetes.io/revision"] == "4"
            assert [e["name"] for e in config["containers"][0]["env"]] == ["DB_PASSWORD", "API_KEY", "EMPTY"]
            assert config["containers"][0]["env"][1]["value_from"]["secret_key_ref"]["name"] == "api-keys"
