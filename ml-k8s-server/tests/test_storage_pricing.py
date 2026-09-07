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


# Golden vectors for Azure tier pricing. The same cases are asserted in the
# other three producers (api-server, ml-k8s-server / k8s-collector,
# cost-server); if a rate moves in one copy and not the others, these diverge.
AZURE_TIER_GOLDEN = [
    ("premium_lrs", 100, "P10", 19.71),
    ("premium_lrs", 128, "P10", 19.71),
    ("premium_lrs", 129, "P15", 38.012142),
    ("premium_lrs", 1024, "P30", 135.17),
    ("premium_zrs", 100, "P10", 29.565),
    ("standardssd_lrs", 100, "E10", 9.6),
    ("standardssd_zrs", 512, "E20", 57.6),
    ("standard_lrs", 10, "S4", 1.536),
    ("standard_lrs", 40000, "S80", 953.55),  # above Azure's largest disk
]


def test_azure_tier_monthly_cost_golden():
    for disk_type, size_gb, tier, monthly in AZURE_TIER_GOLDEN:
        assert sp.azure_tier_monthly_cost(disk_type, size_gb) == (monthly, tier)


def test_azure_per_gib_skus_are_not_tiered():
    # Premium SSD v2 and Ultra bill per provisioned GiB with no size bands.
    for disk_type in ("premiumv2_lrs", "ultrassd_lrs"):
        assert sp.azure_tier_monthly_cost(disk_type, 100) is None
    assert sp.azure_tier_monthly_cost("premium_lrs", 0) is None


def test_azure_tier_effective_rate_yields_whole_disk_price():
    # A 100 GiB Premium disk is billed as P10 ($19.71/mo), so its savings
    # must be the whole tier price -- not 100 x a flat per-GB rate.
    classes = {
        "managed-premium": {
            "provisioner": "disk.csi.azure.com",
            "parameters": {"skuName": "Premium_LRS"},
        }
    }
    pricing = sp.resolve_storage_pricing(_pv("managed-premium"), storage_classes=classes, size_gb=100)
    assert pricing["tier"] == "P10"
    assert pricing["tier_monthly_usd"] == 19.71
    assert pricing["price_per_gb"] * 100 == 19.71


def test_unsized_azure_disk_keeps_the_flat_rate():
    classes = {
        "managed-premium": {
            "provisioner": "disk.csi.azure.com",
            "parameters": {"skuName": "Premium_LRS"},
        }
    }
    pricing = sp.resolve_storage_pricing(_pv("managed-premium"), storage_classes=classes)
    assert "tier" not in pricing
    assert pricing["price_per_gb"] == 0.15
