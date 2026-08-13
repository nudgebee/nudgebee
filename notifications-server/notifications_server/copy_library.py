"""Curated display names for notification copy.

Keys are the raw identifiers notifications are built from: recommendation
``rule_name`` values and finding/event ``aggregation_key`` values. Unmapped
keys fall back to title-casing (the previous behavior) and are logged once
per process so the gap becomes the copy backlog rather than a silent leak
of internal identifiers into customer channels.
"""

import logging

LOG = logging.getLogger(__name__)

RULE_DISPLAY_NAMES = {
    # Cost / optimization
    "pod_right_sizing": "Workload rightsizing",
    "replica_right_sizing": "Replica rightsizing",
    "replica-rightsizing": "Replica rightsizing",
    "continuous_rightsizing": "Workload rightsizing",
    "pv_rightsize": "Volume rightsizing",
    "rightsize_pvc": "Volume rightsizing",
    "rightsizing_pvc": "Volume rightsizing",
    "unused_pvc": "Unused volume",
    "orphaned_volume": "Unused volume",
    "abandoned_resource": "Abandoned workload",
    "abandoned-resources": "Abandoned workload",
    "spot_instance_recommendation": "Spot eligibility",
    "Spot instance recommendation": "Spot eligibility",
    # Infra / configuration
    "eks_cluster_upgrade": "Cluster upgrade",
    "kube_proxy_version": "kube-proxy version drift",
    "helm_chart_upgrade": "Chart update",
    "certificate_expiry": "Certificate expiring",
    # Security
    "image_scan": "Vulnerable image",
    "image_scan_summary": "Vulnerable images",
    "k8s-cis-1.23": "CIS benchmark failure",
    "CIS": "CIS benchmark failure",
    "popeye_misconfigurations": "Misconfiguration",
}

EVENT_DISPLAY_NAMES = {
    "report_crash_loop": "Crash loop",
    "KubePodCrashLooping": "Crash loop",
    "image_pull_backoff_reporter": "Image pull failure",
    "pod_oom_killer_enricher": "Out of memory (OOM-killed)",
    "PodMemoryReachingLimit": "Memory near limit",
    "KubePodNotReady": "Pod not ready",
    "KubeContainerWaiting": "Container stuck waiting",
    "CPUThrottlingHigh": "CPU throttling",
    "job_failure": "Job failed",
    "KubeJobFailed": "Job failed",
    "KubeJobCompletion": "Job overdue",
    "HighErrorCriticalLogs": "Error-log spike",
    "ApplicationAPIFailures": "API failures",
    "JVMMemoryPressure": "JVM memory pressure",
    "KubeNodeNotReady": "Node not ready",
    "KubeNodeUnreachable": "Node unreachable",
    "KubePersistentVolumeFillingUp": "Volume filling up",
    "KubernetesVolumeOutOfDiskSpace": "Volume out of space",
    "Kubernetes Warning Event": "Warning events",
}

# Ingested alert aggregation_keys are arbitrary tenant strings, so cap the
# dedup set: beyond the cap, new unmapped keys neither accumulate nor log.
CATEGORY_DISPLAY_NAMES = {
    "RightSizing": "Rightsizing",
    "K8sSpotRecommendation": "Spot",
    "InfraUpgrade": "Infra upgrades",
    "Configuration": "Configuration",
    "Security": "Security",
    "CostAnomaly": "Cost anomalies",
    "Anomaly": "Anomalies",
}

_unmapped_reported: set = set()
MAX_UNMAPPED_TRACKED = 1000


def _fallback(key: str) -> str:
    return key.replace("_", " ").replace("-", " ").title()


def category_display_name(category) -> str:
    if not category:
        return ""
    return CATEGORY_DISPLAY_NAMES.get(category) or _fallback(category)


def display_name(key) -> str:
    """Human-readable name for a rule_name or aggregation_key."""
    if not key:
        return ""
    name = RULE_DISPLAY_NAMES.get(key) or EVENT_DISPLAY_NAMES.get(key)
    if name:
        return name
    if key not in _unmapped_reported and len(_unmapped_reported) < MAX_UNMAPPED_TRACKED:
        _unmapped_reported.add(key)
        LOG.info("copy library: no display name for key %r, using title-case fallback", key)
    return _fallback(key)
