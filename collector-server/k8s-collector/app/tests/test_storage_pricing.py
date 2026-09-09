"""Storage-class -> $/GB/month resolution ladder.

The unused-PV / PV-rightsize handlers price savings from this ladder; a
wrong rung silently overstates savings (a 100GB pd-standard disk costs
$4/mo, not $10). Runs offline: heavy deps are stubbed before importing the
module, same as test_recommendation_archive_predicate.py.
"""

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

import handlers.storage_pricing as sp  # noqa: E402


class TestResolveStoragePricing(unittest.TestCase):
    def test_parameters_rung_wins(self):
        pv = {"spec": {"storage_class_name": "custom-ssd"}}
        classes = {
            "custom-ssd": {
                "provisioner": "pd.csi.storage.gke.io",
                "parameters": {"type": "pd-ssd"},
            }
        }
        pricing = sp.resolve_storage_pricing(pv, storage_classes=classes)
        self.assertEqual(pricing["source"], "parameters")
        self.assertEqual(pricing["price_per_gb"], 0.17)

    def test_hyperdisk_balanced_is_priced_below_pd_balanced(self):
        pv = {"spec": {"storage_class_name": "hyperdisk-balanced-rwo"}}
        classes = {
            "hyperdisk-balanced-rwo": {
                "provisioner": "pd.csi.storage.gke.io",
                "parameters": {"type": "hyperdisk-balanced"},
            }
        }
        pricing = sp.resolve_storage_pricing(pv, storage_classes=classes)
        self.assertEqual(pricing["source"], "parameters")
        self.assertEqual(pricing["price_per_gb"], 0.08)

    def test_well_known_gke_standard_is_pd_standard(self):
        pv = {"spec": {"storage_class_name": "standard"}}
        classes = {"standard": {"provisioner": "kubernetes.io/gce-pd"}}
        pricing = sp.resolve_storage_pricing(pv, storage_classes=classes)
        self.assertEqual(pricing["source"], "class_name")
        self.assertEqual(pricing["price_per_gb"], 0.04)

    def test_csi_driver_gives_provider_default(self):
        pv = {"spec": {"storage_class_name": "who-knows", "csi": {"driver": "ebs.csi.aws.com"}}}
        pricing = sp.resolve_storage_pricing(pv)
        self.assertEqual(pricing["source"], "provider_default")
        self.assertEqual(pricing["disk_type"], "gp2")

    def test_camelcase_pv_and_provisioned_by_annotation(self):
        pv = {
            "spec": {"storageClassName": "gone-class"},
            "metadata": {"annotations": {"pv.kubernetes.io/provisioned-by": "disk.csi.azure.com"}},
        }
        pricing = sp.resolve_storage_pricing(pv)
        self.assertEqual(pricing["provider"], "azure")
        self.assertEqual(pricing["source"], "provider_default")

    def test_azure_sku_name_normalizes(self):
        pv = {"spec": {"storage_class_name": "fast"}}
        classes = {"fast": {"provisioner": "disk.csi.azure.com", "parameters": {"skuName": "Premium_LRS"}}}
        pricing = sp.resolve_storage_pricing(pv, storage_classes=classes)
        self.assertEqual(pricing["source"], "parameters")
        self.assertEqual(pricing["price_per_gb"], 0.12)

    def test_account_provider_backstop(self):
        pricing = sp.resolve_storage_pricing({"spec": {}}, provider="gcp")
        self.assertEqual(pricing["source"], "provider_default")
        self.assertEqual(pricing["disk_type"], "pd-balanced")

    def test_on_prem_class_name_never_prices_as_cloud(self):
        pv = {"spec": {"storage_class_name": "standard"}}
        classes = {"standard": {"provisioner": "rancher.io/local-path"}}
        pricing = sp.resolve_storage_pricing(pv, storage_classes=classes)
        self.assertEqual(pricing["source"], "fallback")
        self.assertEqual(pricing["price_per_gb"], sp.FALLBACK_STORAGE_RATE_PER_GB_MONTH)

    def test_provisioner_domain_must_match_exactly(self):
        pv = {"spec": {"storage_class_name": "standard"}}
        classes = {"standard": {"provisioner": "attacker.example/ebs.csi.aws.com"}}
        pricing = sp.resolve_storage_pricing(pv, storage_classes=classes)
        self.assertEqual(pricing["source"], "fallback")


class TestRateMapsConsistent(unittest.TestCase):
    def test_every_emittable_disk_type_has_a_rate(self):
        for provider, classes in sp.WELL_KNOWN_CLASS_DISK_TYPE.items():
            for class_name, disk_type in classes.items():
                self.assertIn(disk_type, sp.STORAGE_RATES_PER_GB_MONTH[provider], f"{provider}/{class_name}")
        for provider, disk_type in sp.PROVIDER_DEFAULT_DISK_TYPE.items():
            self.assertIn(disk_type, sp.STORAGE_RATES_PER_GB_MONTH[provider], provider)


class TestGetK8sProvider(unittest.TestCase):
    def setUp(self):
        sp._provider_cache.clear()

    def test_canonicalizes_and_caches(self):
        with mock.patch.object(sp.database, "select_data", return_value=[("gke",)]) as select:
            self.assertEqual(sp.get_k8s_provider("acc-1"), "gcp")
            self.assertEqual(sp.get_k8s_provider("acc-1"), "gcp")
        select.assert_called_once()

    def test_db_error_returns_empty_and_is_not_cached(self):
        with mock.patch.object(sp.database, "select_data", side_effect=RuntimeError("down")):
            self.assertEqual(sp.get_k8s_provider("acc-2"), "")
        with mock.patch.object(sp.database, "select_data", return_value=[("eks",)]):
            self.assertEqual(sp.get_k8s_provider("acc-2"), "aws")


if __name__ == "__main__":
    unittest.main()
