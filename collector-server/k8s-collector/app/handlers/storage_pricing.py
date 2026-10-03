"""Storage list prices ($/GB/month) and the storage-class -> rate resolution
ladder used by the PV recommendation handlers.

Mirrors api-server/services/scan_orchestrator/storage_pricing.go and
ml-k8s-server/server/recommendation/storage_pricing.py — keep all copies in
sync when a rate changes. Ladder: StorageClass parameters (type / skuName)
-> well-known class name -> provider default -> flat fallback. Deterministic
on its inputs so concurrent producers can't disagree on the same volume.
"""

import logging
import time

from db import database

logger = logging.getLogger(__name__)

FALLBACK_STORAGE_RATE_PER_GB_MONTH = 0.10

STORAGE_RATES_PER_GB_MONTH = {
    "gcp": {
        "pd-standard": 0.04,
        "pd-balanced": 0.10,
        "pd-ssd": 0.17,
        "pd-extreme": 0.125,
        # Capacity only. Unlike pd-*, hyperdisk also bills provisioned IOPS
        # and throughput above baseline, so deleting one saves more than this.
        "hyperdisk-balanced": 0.08,
    },
    "aws": {
        "gp2": 0.10,
        "gp3": 0.08,
        "io1": 0.125,
        "io2": 0.125,
        "st1": 0.045,
        "sc1": 0.015,
        "standard": 0.05,
    },
    # Azure rates are the per-GiB fallback used when the disk size is
    # unknown; sized disks in the tiered families resolve through
    # AZURE_DISK_TIERS instead. premiumv2_lrs and ultrassd_lrs have no size
    # bands -- Azure meters them per provisioned GiB, so these are their
    # real rates (Provisioned Capacity, $/GiB/hour x 730).
    "azure": {
        "standard_lrs": 0.04,
        "standardssd_lrs": 0.075,
        "standardssd_zrs": 0.1125,
        "premium_lrs": 0.15,
        "premium_zrs": 0.225,
        "premiumv2_lrs": 0.08,
        "ultrassd_lrs": 0.12,
    },
}

# Azure managed-disk provisioned size bands. Azure bills a fixed monthly
# price per band and rounds every disk up into the next one, so a 100 GiB
# Premium disk is billed as P10 (128 GiB) -- the reason a flat $/GB rate
# understates Azure volume savings. eastus list prices from the retail
# prices API (the whole-disk "<tier> <redundancy> Disk" meter, unit
# 1/Month -- not the much cheaper "Disk Mount" shared-disk meter). Standard
# HDD is LRS-only. Premium SSD v2 and Ultra are absent on purpose: Azure
# bills those per provisioned GiB with no size bands.
AZURE_DISK_TIERS = {
    "standard_lrs": (
        (32, 1.536, "S4"),
        (64, 3.008, "S6"),
        (128, 5.888, "S10"),
        (256, 11.328, "S15"),
        (512, 21.76, "S20"),
        (1024, 40.96, "S30"),
        (2048, 77.824, "S40"),
        (4096, 143.36, "S50"),
        (8192, 262.14, "S60"),
        (16384, 491.52, "S70"),
        (32767, 953.55, "S80"),
    ),
    "standardssd_lrs": (
        (4, 0.3, "E1"),
        (8, 0.6, "E2"),
        (16, 1.2, "E3"),
        (32, 2.4, "E4"),
        (64, 4.8, "E6"),
        (128, 9.6, "E10"),
        (256, 19.2, "E15"),
        (512, 38.4, "E20"),
        (1024, 76.8, "E30"),
        (2048, 153.6, "E40"),
        (4096, 307.2, "E50"),
        (8192, 614.4, "E60"),
        (16384, 1228.8, "E70"),
        (32767, 2457.6, "E80"),
    ),
    "standardssd_zrs": (
        (4, 0.45, "E1"),
        (8, 0.9, "E2"),
        (16, 1.8, "E3"),
        (32, 3.6, "E4"),
        (64, 7.2, "E6"),
        (128, 14.4, "E10"),
        (256, 28.8, "E15"),
        (512, 57.6, "E20"),
        (1024, 115.2, "E30"),
        (2048, 230.4, "E40"),
        (4096, 460.8, "E50"),
        (8192, 921.6, "E60"),
        (16384, 1843.2, "E70"),
        (32767, 3686.4, "E80"),
    ),
    "premium_lrs": (
        (4, 0.6, "P1"),
        (8, 1.2, "P2"),
        (16, 2.4, "P3"),
        (32, 5.2795, "P4"),
        (64, 10.207, "P6"),
        (128, 19.71, "P10"),
        (256, 38.012142, "P15"),
        (512, 73.22, "P20"),
        (1024, 135.17, "P30"),
        (2048, 259.0457, "P40"),
        (4096, 495.5657, "P50"),
        (8192, 946.08, "P60"),
        (16384, 1802.06, "P70"),
        (32767, 3604.11, "P80"),
    ),
    "premium_zrs": (
        (4, 0.9, "P1"),
        (8, 1.8, "P2"),
        (16, 3.6, "P3"),
        (32, 7.919, "P4"),
        (64, 15.31, "P6"),
        (128, 29.565, "P10"),
        (256, 57.018, "P15"),
        (512, 109.81, "P20"),
        (1024, 202.73, "P30"),
        (2048, 388.57, "P40"),
        (4096, 743.35, "P50"),
        (8192, 1419.12, "P60"),
        (16384, 2703.09, "P70"),
        (32767, 5406.16, "P80"),
    ),
}


def azure_tier_monthly_cost(disk_type, size_gb):
    """Monthly price Azure charges for a disk of size_gb, or None if the SKU
    family is not tiered (or the size is unknown)."""
    tiers = AZURE_DISK_TIERS.get(disk_type)
    if not tiers or not size_gb or size_gb <= 0:
        return None
    for band_gb, monthly, name in tiers:
        if size_gb <= band_gb:
            return monthly, name
    # Larger than Azure's biggest disk; bill it as the top band.
    return tiers[-1][1], tiers[-1][2]


def _apply_azure_tier_pricing(pricing: dict, size_gb: float) -> dict:
    """Rewrite the flat per-GB rate into the effective rate for the band
    Azure actually bills (tier price / requested size), so every caller's
    existing `rate * size` arithmetic yields the real monthly cost. No-op
    for non-Azure disks, untiered SKUs, or unknown size."""
    if pricing.get("provider") != "azure":
        return pricing
    tier = azure_tier_monthly_cost(pricing.get("disk_type"), size_gb)
    if tier is None:
        return pricing
    monthly, name = tier
    pricing["price_per_gb"] = monthly / size_gb
    pricing["tier"] = name
    pricing["tier_monthly_usd"] = monthly
    return pricing


# The managed-K8s default storage class's disk type per provider.
PROVIDER_DEFAULT_DISK_TYPE = {
    "gcp": "pd-balanced",
    "aws": "gp2",
    "azure": "standardssd_lrs",
}

# Default class names each managed provider ships, for when
# StorageClass.parameters are unavailable. Keyed by provider so an on-prem
# class that happens to be named "standard" is not priced as a GCP disk.
WELL_KNOWN_CLASS_DISK_TYPE = {
    "gcp": {
        "standard": "pd-standard",
        "standard-rwo": "pd-balanced",
        "premium-rwo": "pd-ssd",
    },
    "aws": {
        "gp2": "gp2",
        "gp3": "gp3",
    },
    "azure": {
        "default": "standardssd_lrs",
        "managed": "standard_lrs",
        "managed-csi": "standardssd_lrs",
        "managed-premium": "premium_lrs",
    },
}

_PROVIDER_CANON = {"eks": "aws", "gke": "gcp", "aks": "azure"}

# Report handlers fire per agent report; don't hit the agent table each time.
# Only successful non-empty lookups are cached so a transient DB error can't
# pin an account to fallback pricing for an hour. Plain dict, not cachetools:
# entries are a few bytes per account and the offline tests import this module
# without third-party deps.
_PROVIDER_CACHE_TTL_SECONDS = 3600
_provider_cache: dict = {}  # cloud_account_id -> (provider, expires_at)


def get_k8s_provider(cloud_account_id: str) -> str:
    """Canonical provider ("aws"/"gcp"/"azure") from agent telemetry, or ""."""
    cached = _provider_cache.get(cloud_account_id)
    if cached is not None and cached[1] > time.time():
        return cached[0]
    try:
        rows = database.select_data("agent", ["k8s_provider"], {"cloud_account_id": cloud_account_id})
    except Exception as e:
        logger.warning(f"storage_pricing: k8s_provider lookup failed for {cloud_account_id}: {e}")
        return ""
    for row in rows:
        value = (row[0] or "").lower()
        if value:
            provider = _PROVIDER_CANON.get(value, value)
            _provider_cache[cloud_account_id] = (provider, time.time() + _PROVIDER_CACHE_TTL_SECONDS)
            return provider
    return ""


def _get(m, *keys):
    if not isinstance(m, dict):
        return None
    for k in keys:
        v = m.get(k)
        if v:
            return v
    return None


def _provider_from_provisioner(s) -> str:
    s = (s or "").lower()
    if s in {"pd.csi.storage.gke.io", "kubernetes.io/gce-pd"}:
        return "gcp"
    if s in {"ebs.csi.aws.com", "kubernetes.io/aws-ebs"}:
        return "aws"
    if s in {
        "disk.csi.azure.com",
        "file.csi.azure.com",
        "kubernetes.io/azure-disk",
        "kubernetes.io/azure-file",
    }:
        return "azure"
    return ""


def resolve_storage_pricing(pv, storage_classes=None, provider="", size_gb=0.0) -> dict:
    """Resolve a PV's monthly $/GB rate.

    pv is the PV object (snake_case or camelCase keys both occur across agent
    generations); storage_classes maps class name -> StorageClass object when
    the caller has them (report-driven handlers usually don't); provider is
    the account-level backstop from get_k8s_provider(). The returned dict is
    embedded into the recommendation JSONB (key "pricing") so fallback-priced
    rows are distinguishable from resolved ones.
    """
    spec = _get(pv, "spec") or {}
    class_name = _get(spec, "storage_class_name", "storageClassName") or ""
    sc = (storage_classes or {}).get(class_name) if class_name else None

    resolved = _provider_from_provisioner(_get(sc, "provisioner"))
    if not resolved:
        resolved = _provider_from_provisioner(_get(_get(spec, "csi") or {}, "driver"))
    if not resolved:
        annotations = _get(_get(pv, "metadata") or {}, "annotations") or {}
        resolved = _provider_from_provisioner(annotations.get("pv.kubernetes.io/provisioned-by"))
    if not resolved:
        resolved = (provider or "").lower()

    params = _get(sc, "parameters")
    if params and resolved:
        disk_type = (_get(params, "type", "skuName", "sku_name") or "").strip().lower()
        rate = STORAGE_RATES_PER_GB_MONTH.get(resolved, {}).get(disk_type)
        if rate is not None:
            return _apply_azure_tier_pricing(
                {"price_per_gb": rate, "disk_type": disk_type, "provider": resolved, "source": "parameters"},
                size_gb,
            )

    if resolved and class_name:
        disk_type = WELL_KNOWN_CLASS_DISK_TYPE.get(resolved, {}).get(class_name, "")
        rate = STORAGE_RATES_PER_GB_MONTH.get(resolved, {}).get(disk_type)
        if rate is not None:
            return _apply_azure_tier_pricing(
                {"price_per_gb": rate, "disk_type": disk_type, "provider": resolved, "source": "class_name"},
                size_gb,
            )

    default_type = PROVIDER_DEFAULT_DISK_TYPE.get(resolved, "")
    rate = STORAGE_RATES_PER_GB_MONTH.get(resolved, {}).get(default_type)
    if rate is not None:
        return _apply_azure_tier_pricing(
            {"price_per_gb": rate, "disk_type": default_type, "provider": resolved, "source": "provider_default"},
            size_gb,
        )

    return {"price_per_gb": FALLBACK_STORAGE_RATE_PER_GB_MONTH, "source": "fallback"}
