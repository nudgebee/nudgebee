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
    "azure": {
        "standard_lrs": 0.04,
        "standardssd_lrs": 0.075,
        "premium_lrs": 0.12,
        "premiumv2_lrs": 0.12,
        "ultrassd_lrs": 0.15,
    },
}

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


def resolve_storage_pricing(pv, storage_classes=None, provider="") -> dict:
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
            return {"price_per_gb": rate, "disk_type": disk_type, "provider": resolved, "source": "parameters"}

    if resolved and class_name:
        disk_type = WELL_KNOWN_CLASS_DISK_TYPE.get(resolved, {}).get(class_name, "")
        rate = STORAGE_RATES_PER_GB_MONTH.get(resolved, {}).get(disk_type)
        if rate is not None:
            return {"price_per_gb": rate, "disk_type": disk_type, "provider": resolved, "source": "class_name"}

    default_type = PROVIDER_DEFAULT_DISK_TYPE.get(resolved, "")
    rate = STORAGE_RATES_PER_GB_MONTH.get(resolved, {}).get(default_type)
    if rate is not None:
        return {"price_per_gb": rate, "disk_type": default_type, "provider": resolved, "source": "provider_default"}

    return {"price_per_gb": FALLBACK_STORAGE_RATE_PER_GB_MONTH, "source": "fallback"}
