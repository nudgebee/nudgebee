"""Storage-class -> $/GB/month resolution ladder.

Volume rightsizing prices savings from this ladder, so a wrong rung or a
wrong rate silently misstates every PVC recommendation.

Loaded from its file rather than imported as server.recommendation.*: the
module itself has no imports, but the package __init__ pulls opentelemetry
and the rest of the service in, which this has no need of.
"""

import importlib.util
import os

_MODULE_PATH = os.path.join(
    os.path.abspath(os.path.join(os.path.dirname(__file__), "..")),
    "server",
    "recommendation",
    "storage_pricing.py",
)
_spec = importlib.util.spec_from_file_location("storage_pricing_under_test", _MODULE_PATH)
sp = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(sp)


def _pv(class_name):
    return {"spec": {"storage_class_name": class_name}}


def test_parameters_rung_wins():
    classes = {
        "custom-ssd": {
            "provisioner": "pd.csi.storage.gke.io",
            "parameters": {"type": "pd-ssd"},
        }
    }
    pricing = sp.resolve_storage_pricing(_pv("custom-ssd"), storage_classes=classes)
    assert pricing["source"] == "parameters"
    assert pricing["price_per_gb"] == 0.17


def test_hyperdisk_balanced_is_priced_below_pd_balanced():
    # GKE's newer default class, and the only GCP type whose rate is not
    # shared with pd-balanced.
    classes = {
        "hyperdisk-balanced-rwo": {
            "provisioner": "pd.csi.storage.gke.io",
            "parameters": {"type": "hyperdisk-balanced"},
        }
    }
    pricing = sp.resolve_storage_pricing(_pv("hyperdisk-balanced-rwo"), storage_classes=classes)
    assert pricing["source"] == "parameters"
    assert pricing["price_per_gb"] == 0.08


def test_well_known_gke_standard_is_pd_standard():
    classes = {"standard": {"provisioner": "kubernetes.io/gce-pd"}}
    pricing = sp.resolve_storage_pricing(_pv("standard"), storage_classes=classes)
    assert pricing["source"] == "class_name"
    assert pricing["price_per_gb"] == 0.04


def test_on_prem_class_name_never_prices_as_cloud():
    classes = {"standard": {"provisioner": "rancher.io/local-path"}}
    pricing = sp.resolve_storage_pricing(_pv("standard"), storage_classes=classes)
    assert pricing["source"] == "fallback"
    assert pricing["price_per_gb"] == sp.FALLBACK_STORAGE_RATE_PER_GB_MONTH


def test_provisioner_domain_must_match_exactly():
    classes = {"standard": {"provisioner": "attacker.example/gke.io"}}
    pricing = sp.resolve_storage_pricing(_pv("standard"), storage_classes=classes)
    assert pricing["source"] == "fallback"


def test_every_emittable_disk_type_has_a_rate():
    for provider, classes in sp.WELL_KNOWN_CLASS_DISK_TYPE.items():
        for class_name, disk_type in classes.items():
            assert disk_type in sp.STORAGE_RATES_PER_GB_MONTH[provider], f"{provider}/{class_name}"
    for provider, disk_type in sp.PROVIDER_DEFAULT_DISK_TYPE.items():
        assert disk_type in sp.STORAGE_RATES_PER_GB_MONTH[provider], provider
