import dataclasses
import hashlib
import json
import logging
import math
import os
import re
import threading
import uuid
from abc import ABC, abstractmethod
from datetime import datetime, timezone
from enum import Enum
from typing import List, Dict, Any, Union, Optional

import requests
from cachetools import TTLCache
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
from psycopg2 import sql, extras
from psycopg2.extras import RealDictCursor

from apis.model.runbook import ExecuteRunbookActionSubmitResponse, LLMActionResponse
from config import Configs
from db import database, clickhouse
from handlers.upgrade_handler import handle_k8s_version_upgrade_report
from middleware.utils import parse_size_to_gb, round_bytes_to_gb
from rabbitmq import rabbitmq_client
from utils.trace_analyzer import TraceAnalyzer

POPEYE_CODE_PATTERN = r"\[POP-\d+\]"

# If this amount of memory was exceeded (in the last metrics_duration_in_secs seconds), the playbook will
# report the corresponding node as the reason for the OOMKill.
CONTAINER_MEMORY_THRESHOLD = 92
# If this amount of memory was exceeded (in the last metrics_duration_in_secs seconds), the playbook will
# report the corresponding node as the reason for the OOMKill.
NODE_MEMORY_THRESHOLD = 95

RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE_PROPERTY_NAME = "RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE"
RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE = 10

HTTP_METHOD = "http.method"
HTTP_TARGET = "http.target"

logger = logging.getLogger(__name__)

# --- Fingerprint normalization for event deduplication ---
# Job events where subject_name is per-CronJob-run and needs owner-level dedup
_JOB_AGGREGATION_KEYS = {"job_failure", "KubeJobFailed", "KubeJobCompletion"}

# Generic K8s warning events (e.g. BackoffLimitExceeded) also fire with subject_type=job
# and need the same owner-level dedup as job_failure.
_JOB_WARNING_AGGREGATION_KEYS = _JOB_AGGREGATION_KEYS | {"Kubernetes Warning Event"}

# Pod events that need owner-level dedup
_POD_AGGREGATION_KEYS = {
    "report_crash_loop",
    "image_pull_backoff_reporter",
    "pod_oom_killer_enricher",
    "KubePodCrashLooping",
    "KubePodNotReady",
    "KubeContainerWaiting",
    "CPUThrottlingHigh",
    "Kubernetes Warning Event",
}

# Cache for workload owner lookups (pod_name/job_name -> owner info)
# TTL of 1 hour; workload owners rarely change
_OWNER_CACHE_TTL_SECONDS = 60 * 60
_owner_cache: TTLCache = TTLCache(maxsize=50_000, ttl=_OWNER_CACHE_TTL_SECONDS)
_owner_cache_lock = threading.Lock()

# Sentinel for "looked up but not found" to avoid repeated DB misses
_OWNER_NOT_FOUND = object()


def _get_cached_owner(cache_key: str):
    with _owner_cache_lock:
        return _owner_cache.get(cache_key)


def _set_cached_owner(cache_key: str, value):
    with _owner_cache_lock:
        _owner_cache[cache_key] = value


# CronJob-spawned Jobs are named ``{cronjob}-{schedule-unix-minutes}`` (see
# kubernetes/pkg/controller/cronjob/utils.go: ``getJobName``). The suffix is
# ``scheduled_time.Unix() / 60`` — an 8-digit value for any time between ~1990 and ~2160.
# We bound the fallback heuristic to a plausible CronJob-era range (~2015 to ~2065) so a
# standalone Job whose name happens to end in digits (e.g. ``foo-1``, ``foo-177``,
# ``nudgebee-profiler-…-17125554``) is not mis-attributed to a phantom CronJob parent.
_CRONJOB_SUFFIX_MIN_UNIX_MINUTES = 24_000_000  # ~July 2015 (before CronJob alpha)
_CRONJOB_SUFFIX_MAX_UNIX_MINUTES = 50_000_000  # ~year 2065


def _enrich_job_owner(finding: dict, tenant: str, cloud_account_id: str) -> None:
    """Enrich subject_owner for job events.

    Primary path: read the actual ``ownerReferences`` from the Job's row in
    ``k8s_workloads`` (kind='Job', meta -> job_data -> parents). When the Job is owned by a
    CronJob this is authoritative and avoids any name-based guessing.

    Fallback path: when the Job has not yet been written to ``k8s_workloads`` (workload
    discovery is periodic and lags CronJob creation by several minutes, breaking dedup for
    early runs), parse the CronJob name from the deterministic ``{cronjob}-{unix-minutes}``
    naming convention. The suffix is bounded to a plausible unix-minutes range to avoid
    mis-attributing standalone Jobs whose names happen to end in digits.

    Caching: only verified results (CronJob parent confirmed, or Job confirmed standalone)
    are cached. The fallback path leaves the cache untouched so the next event picks up the
    Job once the workload sync catches up.
    """
    subject_name = finding.get("subject_name", "")
    namespace = finding.get("subject_namespace", "")
    if not subject_name:
        return

    cache_key = f"job:{cloud_account_id}:{namespace}:{subject_name}"
    cached = _get_cached_owner(cache_key)
    if cached is not None:
        if cached is _OWNER_NOT_FOUND:
            return
        finding["subject_owner"] = cached["owner"]
        finding["subject_owner_kind"] = cached["owner_kind"]
        return

    # Primary: actual ownerReferences from the Job's synced workload row.
    # ``meta -> job_data -> parents`` ships in two historical shapes — the modern
    # ``[{kind, name}, ...]`` form and an older bare-string ``[name, ...]`` form. We
    # accept both. An empty array means the Job is standalone (no owner-level dedup).
    try:
        rows = database.run_query(
            "SELECT meta->'job_data'->'parents' FROM k8s_workloads "
            "WHERE tenant_id=%s AND cloud_account_id=%s AND name=%s "
            "AND kind='Job' AND namespace=%s LIMIT 1",
            [tenant, cloud_account_id, subject_name, namespace],
        )
        if rows:
            parents = rows[0][0] or []
            for parent in parents:
                if isinstance(parent, dict) and parent.get("kind") == "CronJob" and parent.get("name"):
                    owner = parent["name"]
                    finding["subject_owner"] = owner
                    finding["subject_owner_kind"] = "CronJob"
                    _set_cached_owner(cache_key, {"owner": owner, "owner_kind": "CronJob"})
                    return
            for parent in parents:
                if isinstance(parent, str) and parent:
                    # Older serialization: bare parent name without kind. Use it as the
                    # owner, leave kind empty (we cannot prove it is a CronJob).
                    finding["subject_owner"] = parent
                    finding["subject_owner_kind"] = ""
                    _set_cached_owner(cache_key, {"owner": parent, "owner_kind": ""})
                    return
            # Parents present but no usable entry, OR parents empty → standalone Job.
            _set_cached_owner(cache_key, _OWNER_NOT_FOUND)
            return
    except Exception:
        logger.exception("Failed to look up Job parents for %s", subject_name)
        return

    # Fallback: Job not yet in k8s_workloads. Use the CronJob naming convention,
    # bounded to a plausible unix-minutes range to avoid false positives.
    parts = subject_name.rsplit("-", 1)
    if len(parts) != 2 or not parts[1].isdigit():
        return
    suffix = int(parts[1])
    if not (_CRONJOB_SUFFIX_MIN_UNIX_MINUTES <= suffix <= _CRONJOB_SUFFIX_MAX_UNIX_MINUTES):
        return

    # Set owner with empty kind. Don't cache: a later sync should produce an authoritative
    # answer (either upgrade kind to "CronJob" or mark NOT_FOUND for standalone Jobs).
    finding["subject_owner"] = parts[0]
    finding["subject_owner_kind"] = ""


def _enrich_pod_owner(finding: dict, tenant: str, cloud_account_id: str) -> None:
    """Enrich subject_owner for pod events by looking up the workload in k8s_pods."""
    subject_name = finding.get("subject_name", "")
    namespace = finding.get("subject_namespace", "")
    if not subject_name:
        return

    cache_key = f"pod:{cloud_account_id}:{namespace}:{subject_name}"
    cached = _get_cached_owner(cache_key)
    if cached is not None:
        if cached is _OWNER_NOT_FOUND:
            return
        finding["subject_owner"] = cached["owner"]
        finding["subject_owner_kind"] = cached["owner_kind"]
        return

    try:
        rows = database.run_query(
            "SELECT workload_type, workload_name FROM k8s_pods WHERE tenant_id=%s AND cloud_account_id=%s "
            "AND name=%s AND namespace=%s LIMIT 1",
            [tenant, cloud_account_id, subject_name, namespace],
        )
        if rows and len(rows) > 0 and rows[0][1]:
            workload_type = rows[0][0] or ""
            workload_name = rows[0][1]
            owner_info = {"owner": workload_name, "owner_kind": workload_type}
            _set_cached_owner(cache_key, owner_info)
            finding["subject_owner"] = workload_name
            finding["subject_owner_kind"] = workload_type
        else:
            _set_cached_owner(cache_key, _OWNER_NOT_FOUND)
    except Exception:
        logger.exception("Failed to look up workload owner for pod %s", subject_name)


_WARNING_REASON_RE = re.compile(r"^(.+?)\s+Warning\s+for\s+", re.IGNORECASE)


def _normalize_owner_fingerprint(finding: dict) -> str:
    """Compute a normalized fingerprint based on workload owner instead of ephemeral pod/job name.

    For 'Kubernetes Warning Event', the warning reason (e.g. OOMKilling, Unhealthy, BackOff)
    is extracted from the title and included in the hash to avoid merging different warning
    types for the same workload.
    """
    aggregation_key = finding.get("aggregation_key", "")
    namespace = finding.get("subject_namespace", "")
    subject_owner = finding.get("subject_owner", "")

    # For generic warning events, differentiate by warning reason from the title
    reason = ""
    if aggregation_key == "Kubernetes Warning Event":
        title = finding.get("title", "")
        match = _WARNING_REASON_RE.match(title)
        if match:
            reason = match.group(1)

    raw = f"{aggregation_key}:{reason}:{namespace}/{subject_owner}"
    return hashlib.sha256(raw.encode()).hexdigest()


def _enrich_and_normalize_fingerprint(finding: dict, tenant: str, cloud_account_id: str) -> None:
    """Enrich subject_owner from DB when missing, then normalize fingerprint to owner level.

    This enables event_duplicates to chain recurring failures of the same workload together,
    instead of treating each pod/job instance as a separate event.
    """
    aggregation_key = finding.get("aggregation_key", "")
    subject_type = finding.get("subject_type", "")
    subject_owner = finding.get("subject_owner") or ""

    is_job_event = subject_type == "job" and aggregation_key in _JOB_WARNING_AGGREGATION_KEYS
    is_pod_event = subject_type == "pod" and aggregation_key in _POD_AGGREGATION_KEYS

    if not subject_owner:
        if is_job_event:
            _enrich_job_owner(finding, tenant, cloud_account_id)
        elif is_pod_event:
            _enrich_pod_owner(finding, tenant, cloud_account_id)

    subject_owner = finding.get("subject_owner") or ""
    if not subject_owner:
        return

    if is_job_event or is_pod_event:
        finding["fingerprint"] = _normalize_owner_fingerprint(finding)


event_handler_dir = os.path.dirname(os.path.abspath(__file__))
popeye_rules_path = os.path.join(event_handler_dir, "..", "config", "popeye-rules.json")
with open(popeye_rules_path) as f:
    POPEYE_RULES_MAP = json.load(f)

RULE_DESCRIPTION = {
    "pod_right_sizing": (
        "Right sizing recommendations are generated based on the resource utilization of the "
        "pods. It gathers pod usage data and recommends requests and limits for CPU "
        "and memory. This reduces costs and improves performance."
    ),
    "image_scan": (
        "Image scanner recommendations are generated based on the vulnerabilities found in the "
        "images. It scans the images and recommends the images that need to be updated."
    ),
    "CIS": (
        "Security recommendations are generated based on the CIS Kubernetes Benchmark."
        " It scans the cluster and recommends the security best practices."
    ),
    "certificate_expiry": (
        "Certificate scanner recommendations are generated based on the certificates found in the "
        "cluster. It scans the certificates and recommends the certificates that need to be updated."
    ),
    "k8s_api_deleted": (
        "K8s version upgrade recommendations are pre upgrade checks to validate deleted "
        "APIs from the current version to the next version."
    ),
    "k8s_api_deprecated": (
        "K8s version upgrade recommendations are pre upgrade checks to validate deleted "
        "APIs from the current version to the next version."
    ),
    "unused_pvc": (
        "Abandoned PV recommendations are generated based on the persistent volumes that are not "
        "associated with any PVC. It recommends the PVs that can be deleted."
    ),
    "pv_rightsize": (
        "Volume size analyzer recommendations are generated based on the volume size of the "
        "persistent volumes. It recommends the volumes that can be resized to save costs."
    ),
}

DEFAULT_COST = {
    "provider": "custom",
    "description": "Default prices based on GCP us-central1",
    "CPU": 0.031611,
    "spotCPU": 0.006655,
    "RAM": 0.004237,
    "spotRAM": 0.000892,
    "GPU": 0.95,
    "storage": 0.00005479452,
    "zoneNetworkEgress": 0.01,
    "regionNetworkEgress": 0.01,
    "internetNetworkEgress": 0.12,
}

# Severity escalation thresholds moved to Go service
# See: api-server/services/event/severity_thresholds.go


class RuleName(Enum):
    POD_RIGHT_SIZING = "pod_right_sizing"
    IMAGE_SCAN = "image_scan"
    CIS = "CIS"
    CERTIFICATE_EXPIRY = "certificate_expiry"
    K8S_API_DELETED = "k8s_api_deleted"
    K8S_API_DEPRECATED = "k8s_api_deprecated"
    UNUSED_PVC = "unused_pvc"
    PV_RIGHTSIZE = "pv_rightsize"
    HELM_CHART_UPGRADE = "helm_chart_upgrade"
    EKS_CLUSTER_UPGRADE = "eks_cluster_upgrade"
    TRIVY_CIS_SCAN = "k8s-cis-1.23"
    K8S_HELM_COMPATIBILITY = "k8s_helm_compatibility"
    KUBE_PROXY_VERSION = "kube_proxy_version"
    EKS_ADD_ONS_VERSION = "eks_add_ons_version"


class Category(Enum):
    RIGHT_SIZING = "RightSizing"
    SECURITY = "Security"
    INFRA_UPGRADE = "InfraUpgrade"
    CONFIGURATION = "Configuration"


def get_severity(severity):
    if severity == "CRITICAL":
        return "Critical"
    if severity == "LOW":
        return "Low"
    elif severity == "HIGH":
        return "High"
    elif severity == "MEDIUM":
        return "Medium"
    return "Info"


def _call_trigger_investigation(tenant: str, events: list) -> dict:
    """Call trigger_investigation API on services-server.

    Same pattern as cloud-collector/services/svc.go:InvestigateEvent.
    Routes agent events through the standard investigation pipeline so
    server-side playbook enrichment runs before downstream processors.
    """
    url = f"{Configs.SERVICE_API_SERVER_URL}/hasura/event"
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json",
        "X-ACTION-TOKEN": Configs.ACTION_API_SERVER_TOKEN,
        "x-tenant-id": tenant,
    }
    payload = {
        "action": {"name": "trigger_investigation"},
        "input": {"events": events},
    }

    # Retry is safe: trigger_investigation deduplicates by (tenant, cloud_account_id, finding_id)
    retry_strategy = Retry(
        total=5,
        backoff_factor=1,
        status_forcelist=[429, 500, 502, 503, 504],
        allowed_methods=["POST"],
    )
    adapter = HTTPAdapter(max_retries=retry_strategy)
    session = requests.Session()
    session.mount("http://", adapter)
    session.mount("https://", adapter)

    try:
        resp = session.post(url, json=payload, headers=headers, timeout=120)
        resp.raise_for_status()
        return resp.json()
    except requests.exceptions.RequestException as e:
        logging.error(f"Failed to call trigger_investigation API at {url}: {e}")
        raise


def run_event_handler_async(content: str | dict) -> None:
    if isinstance(content, dict):
        data = content
    else:
        data = json.loads(content)
    events = []
    finding = data["finding"]
    tenant = data["tenant"]
    cloud_account_id = data["cloud_account_id"]
    account_details = database.select_data("cloud_accounts", ["id", "account_name"], {"id": cloud_account_id})
    if not account_details:
        return

    cloud_account_name = account_details[0][1]

    handlers = {
        "krr_report": handle_krr_report,
        "popeye_report": handle_popeye_report,
        "kube_bench_report": handle_kube_bench,
        "abandoned_pv_report": handle_abandoned_pv,
        "volume_size_analyzer": handle_pv_rightsize,
        "image_scanner_report": handle_image_scan,
        "k8s_version_upgrade": handle_k8s_version_upgrade_report,
        "certificate_scanner_report": handle_certificate_scanner_report,
        "helm_chart_upgrade_report": handle_helm_chart_upgrade_report,
        "trivy_cis_report": handle_trivy_cis_scan,
        "k8s_helm_compatibility": handle_k8s_helm_compatibility_report,
    }

    handler = handlers.get(finding["aggregation_key"])
    if handler:
        handler(data, tenant, cloud_account_id, cloud_account_name)
        return

    # fingerprints incase of logs, are only generated for given pod..
    # so normally conflicts with rest of the system. using owner info to make it unique for given service
    if finding["aggregation_key"] == "HighErrorCriticalLogs":
        new_fingerprint = (
            finding.get("subject_namespace", "") + "/" + finding.get("subject_owner", "") + ":" + finding["fingerprint"]
        )
        finding["fingerprint"] = hashlib.sha256(new_fingerprint.encode()).hexdigest()

    if "subject_name" not in finding or finding["subject_name"] is None:
        return

    if finding.get("subject_type"):
        finding["subject_type"] = finding["subject_type"].lower()

    _enrich_and_normalize_fingerprint(finding, tenant, cloud_account_id)

    resource_key, resources_data = get_resource_info(cloud_account_id, finding, tenant)
    if "RESOLVED" in finding["title"]:
        existing_events = database.run_query(
            "select * from events where cloud_account_id=%s and tenant=%s and "
            "fingerprint=%s and status != 'CLOSED' order by created_at desc limit 1",
            [cloud_account_id, tenant, finding["fingerprint"]],
            cursor_factory=extras.RealDictCursor,
        )
        if existing_events and len(existing_events) > 0:
            existing_event = existing_events[0]
            handle_existing_resolved_event(cloud_account_id, existing_event)
        return
    config_change_event = []
    config_change_event = get_last_config_change(config_change_event, data, finding)
    evidence = format_evidence(data["evidence"], config_change_event, finding["aggregation_key"])
    handle_triggers(tenant, cloud_account_id, evidence, finding)
    alert_labels = get_event_labels(data["evidence"])
    resources_map = {}
    for r in resources_data:
        resources_map[r[1]] = r

    event = get_event_data(cloud_account_name, data, evidence, finding, resource_key, resources_map, alert_labels)
    store_pod_profile(tenant, cloud_account_id, data["evidence"], event)
    events.append(event)

    # Build API-compatible event for trigger_investigation
    # (labels/evidences as native objects, account_id instead of cloud_account_id)
    api_event = {
        "finding_id": finding["id"],
        "aggregation_key": finding["aggregation_key"],
        "title": finding["title"],
        "description": finding.get("description", ""),
        "source": finding.get("source") or "nudgebee",
        "finding_type": finding["finding_type"],
        "priority": finding["priority"],
        "subject_type": finding["subject_type"],
        "subject_name": finding["subject_name"],
        "subject_namespace": finding["subject_namespace"],
        "subject_node": finding["subject_node"],
        "subject_owner": finding.get("subject_owner"),
        "subject_owner_kind": finding.get("subject_owner_kind"),
        "service_key": finding["service_key"],
        "cluster": cloud_account_name,
        "status": "FIRING",
        "starts_at": finding["starts_at"],
        "fingerprint": finding["fingerprint"],
        "failure": str(finding.get("failure", "")),
        "account_id": cloud_account_id,
        "tenant": tenant,
        "labels": alert_labels,
        "evidences": evidence,
    }
    if "ends_at" in finding:
        api_event["ends_at"] = finding["ends_at"]
    if "cloud_resource_id" in event:
        api_event["cloud_resource_id"] = event["cloud_resource_id"]

    try:
        _call_trigger_investigation(tenant, [api_event])
    except Exception:
        logging.exception("Failed to call trigger_investigation API for event %s", finding["id"])
        raise

    try:
        _write_events_to_clickhouse(events)
    except Exception:
        logging.exception(f"Failed to insert row in clickhouse {json.dumps(events, default=str, indent=2)}")


def generate_uuid_from_string(my_string: str) -> str:
    namespace = uuid.NAMESPACE_URL
    my_uuid = uuid.uuid5(namespace, my_string)
    return str(my_uuid)


def get_pod_data(pod_name, namespace, cloud_account_id, tenant) -> list[dict[str, Any]]:
    return database.run_query(
        """select kw.cloud_resource_id, kp.workload_type, kw.name, kw.namespace from k8s_pods kp
        inner join k8s_workloads
        kw on kp.workload_name  = kw.name  and kp."namespace"  = kw.namespace and kp.cloud_account_id
        = kw.cloud_account_id and kp.tenant_id  = kw.tenant_id where kp.name = %s and kp.namespace =%s
        and kp.cloud_account_id = %s and kp.tenant_id = %s""",
        [pod_name, namespace, cloud_account_id, tenant],
        cursor_factory=extras.RealDictCursor,
    )


def handle_non_resolved_event(finding, evidences, cloud_account_id, tenant):
    if finding.get("subject_name") != "Unresolved":
        return ""
    if finding["aggregation_key"] not in ["ApplicationAPIFailures", "HighErrorCriticalLogs"]:
        return ""
    for evidence in evidences:
        container_id = extract_container_id(evidence)
        if container_id:
            pod_info = get_pod_info(container_id, cloud_account_id, tenant)
            if pod_info:
                finding["service_key"] = format_pod_info(pod_info)
                finding["subject_name"] = pod_info[0]["name"]
                finding["subject_type"] = pod_info[0]["workload_type"].lower()
                finding["subject_namespace"] = pod_info[0]["namespace"]
                return pod_info[0]["cloud_resource_id"]
    return ""


def extract_container_id(evidence):
    if "data" in evidence and "type" in evidence and evidence["type"] == "table":
        for row in evidence["data"]["rows"]:
            if row and row[0] == "container_id":
                return row[1]
    return None


def get_pod_info(container_id, cloud_account_id, tenant):
    split = container_id.split("/")
    if len(split) >= 5:
        return get_pod_data(split[3], split[2], cloud_account_id, tenant)
    return []


def format_pod_info(pod_info):
    return f"{pod_info[0]['namespace']}/{pod_info[0]['workload_type']}/{pod_info[0]['name']}"


def get_event_data(cloud_account_name, data, evidence, finding, resource_key, resources_map, labels):
    resource_id = handle_non_resolved_event(finding, evidence, data["cloud_account_id"], data["tenant"])
    # Severity escalation is now handled by Go event processor
    event = {
        "finding_id": finding["id"],
        "priority": finding["priority"],
        "title": finding["title"],
        "description": finding["description"],
        "source": finding["source"],
        "aggregation_key": finding["aggregation_key"],
        "failure": finding["failure"],
        "finding_type": finding["finding_type"],
        "subject_type": finding["subject_type"],
        "subject_name": finding["subject_name"],
        "subject_namespace": finding["subject_namespace"],
        "subject_node": finding["subject_node"],
        "service_key": finding["service_key"],
        "cluster": cloud_account_name,
        "status": "FIRING",
        "starts_at": finding["starts_at"],
        "subject_owner": finding.get("subject_owner"),
        "subject_owner_kind": finding.get("subject_owner_kind"),
        "labels": json.dumps(labels),
    }
    if resource_id != "":
        event["cloud_resource_id"] = resource_id
    elif resource_key in resources_map:
        event["cloud_resource_id"] = resources_map[resource_key][0]
    if "ends_at" in finding:
        event["ends_at"] = finding["ends_at"]
    event["fingerprint"] = finding["fingerprint"]
    event["evidences"] = json.dumps(evidence)
    event["tenant"] = data["tenant"]
    event["cloud_account_id"] = data["cloud_account_id"]
    if "source" not in finding or not finding["source"]:
        event["source"] = "nudgebee"
    return event


def get_last_config_change(config_change_event, data, finding):
    if finding["finding_type"] != "configuration_change" and finding["service_key"]:
        select_config_change_event = sql.SQL(
            "select id, service_key, evidences,starts_at from {} where "
            "cloud_account_id = %s  and finding_type = %s and subject_namespace = %s and service_key = %s "
            "order by created_at desc limit 1"
        ).format(sql.Identifier("events"))
        values = [
            data["cloud_account_id"],
            "configuration_change",
            finding["subject_namespace"],
            finding["service_key"],
        ]
        config_change_event = database.run_query(select_config_change_event, values)
    return config_change_event


def get_resource_info(cloud_account_id, finding, tenant):
    resources_data = []
    resource_key = finding["service_key"]
    if finding["subject_type"] in ["deployment", "pod", "job", "daemonset", "statefulset"]:
        resource_select_query = sql.SQL(
            """select id,resourse_id from cloud_resourses where resourse_id = %s and tenant = %s and account = %s"""
        ).format(sql.Identifier("cloud_resourses"))
        resources_data = database.run_query(resource_select_query, [resource_key, tenant, cloud_account_id])
    elif finding["subject_type"] == "node":
        resource_key = finding["subject_name"]
        resource_select_query = sql.SQL(
            """select id,name from cloud_resourses where type = 'node' and name = %s and tenant = %s and account = %s"""
        ).format(sql.Identifier("cloud_resourses"))
        resources_data = database.run_query(resource_select_query, [finding["subject_name"], tenant, cloud_account_id])
    return resource_key, resources_data


def handle_existing_resolved_event(cloud_account_id, existing_event):
    import hashlib

    # Generate deterministic ID for history record (same logic as Go)
    event_id = existing_event["id"]
    change_type = "status"
    old_value = existing_event.get("status", "FIRING")
    new_value = "CLOSED"

    old_json = json.dumps(old_value, sort_keys=True)
    new_json = json.dumps(new_value, sort_keys=True)
    fingerprint = f"{event_id}|{change_type}|{old_json}|{new_json}"
    history_id = hashlib.sha256(fingerprint.encode()).hexdigest()[:32]

    # Insert history with resolution context before UPDATE
    # ON CONFLICT DO NOTHING prevents duplicates (webhook may also try to insert)
    history_insert = """
        INSERT INTO event_history (
            id, event_id, tenant_id, cloud_account_id,
            change_type, old_value, new_value,
            change_reason, metadata
        ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
        ON CONFLICT (id) DO NOTHING
    """

    metadata = json.dumps(
        {
            "resolution_method": "automatic",
            "trigger": "alert_title_contains_resolved",
        }
    )

    try:
        database.run_query(
            history_insert,
            [
                history_id,
                existing_event["id"],
                existing_event.get("tenant"),
                cloud_account_id,
                "status",
                json.dumps(old_value),
                json.dumps(new_value),
                "alert_resolved",
                metadata,
            ],
        )
    except Exception:
        logging.exception(f"Failed to insert event history for resolution: {existing_event['id']}")
        # Don't fail - history is not critical, continue with UPDATE

    # Then UPDATE the event (triggers webhook, but deterministic ID prevents duplicate)
    database.run_query(
        "update events set status = 'CLOSED' , ends_at = now() where cloud_account_id=%s and id=%s and"
        " status != 'CLOSED'",
        [cloud_account_id, existing_event["id"]],
    )
    try:
        existing_event["status"] = "CLOSED"
        _write_events_to_clickhouse([existing_event])
    except Exception:
        logging.exception(f"Failed to update clickhouse, failing row {existing_event}")


def _write_events_to_clickhouse(events: List[Dict[str, Any]]) -> None:
    for event in events:
        event["failure"] = str(event["failure"])
        if event.get("description", None) is None:
            event["description"] = ""
        if event.get("subject_node", None) is None:
            event["subject_node"] = ""
        if event.get("subject_type", None) is None:
            event["subject_type"] = ""
        if event.get("subject_namespace", None) is None:
            event["subject_namespace"] = ""
        if event.get("category", None) is None:
            event["category"] = ""
        if event.get("subject_type", None) is None:
            event["subject_type"] = ""
        if event.get("subject_name", None) is None:
            event["subject_name"] = ""
        if event.get("service_key", None) is None:
            event["service_key"] = ""
        if event.get("fingerprint", None) is None:
            event["fingerprint"] = ""
        if event.get("cloud_resource_id", None) is None:
            event["cloud_resource_id"] = ""
        if event.get("status", None) is None:
            event["status"] = ""
        if event.get("principal", None) is None:
            event["principal"] = ""
        if event.get("subject_owner", None) is None:
            event["subject_owner"] = ""
        if event.get("subject_owner_kind", None) is None:
            event["subject_owner_kind"] = ""

        handle_event_dates(event)
        handle_event_evidence(event)
        handle_event_labels(event)

    if len(events) > 0:
        clickhouse.insert_data("events", events)


def handle_event_dates(event):
    if "starts_at" in event:
        if event["starts_at"] is None:
            del event["starts_at"]
        else:
            starts_at = event["starts_at"]
            if isinstance(starts_at, str):
                event["starts_at"] = datetime.fromisoformat(starts_at)
    if "ends_at" in event:
        if event["ends_at"] is None:
            del event["ends_at"]
        else:
            ends_at = event["ends_at"]
            if isinstance(ends_at, str):
                event["ends_at"] = datetime.fromisoformat(ends_at)


def handle_event_evidence(event):
    if "evidences" in event:
        evidences = event["evidences"]
        if not isinstance(evidences, str):
            event["evidences"] = json.dumps(evidences)


def handle_event_labels(event):
    if "labels" in event:
        labels = event["labels"]
        if not isinstance(labels, str):
            event["labels"] = json.dumps(labels)


def handle_krr_report(content: dict, tenant: str, cloud_account_id: str, account_name: str) -> None:
    logging.info("Processing krr report for tenant {} , account id {}".format(tenant, cloud_account_id))
    try:
        report = json.loads(content["evidence"][0]["data"])[0]
        score = report["score"]
        resource_list = get_existing_resource(cloud_account_id, report, tenant)
        on_conflict = (
            "ON CONFLICT (cloud_account_id, rule_name, resource_id, category,account_object_id) DO UPDATE SET "
            "recommendation =EXCLUDED.recommendation, estimated_savings =EXCLUDED.estimated_savings, "
            "status = EXCLUDED.status"
        )
        resource_map = {}
        for resource in resource_list:
            # resource[4] is containers - handle both JSON string and already-parsed JSONB
            # psycopg2 automatically parses JSONB columns into Python types (list/dict)
            try:
                containers_data = resource[4]

                # Explicitly handle different data types
                if containers_data is None:
                    containers = []
                elif isinstance(containers_data, list):
                    # Already parsed by psycopg2 - use directly
                    containers = containers_data
                elif isinstance(containers_data, str):
                    # JSON string - parse it
                    containers = json.loads(containers_data) if containers_data else []
                else:
                    # Unexpected type - log and skip
                    logging.warning(
                        f"Unexpected container data type for workload {resource[0]}: "
                        f"{type(containers_data).__name__}"
                    )
                    continue

                if not containers:
                    logging.warning(f"No containers found for workload {resource[3]} in namespace {resource[5]}")
                    continue

                for container in containers:
                    # Key format: kind/namespace/name/container_name
                    # resource[2] = controller_kind (kind)
                    # resource[5] = namespace
                    # resource[3] = controller (name)
                    # container['name'] = container name
                    resource_map[f"{resource[2]}/{resource[5]}/{resource[3]}/{container['name']}"] = resource
            except (KeyError, TypeError, IndexError) as e:
                logging.warning(f"Failed to parse container data for workload {resource[0]}: {e}")
                continue

        recommendations = generate_krr_recommendation(cloud_account_id, report, resource_map, tenant)
        try:
            handle_notification(
                account_name,
                cloud_account_id,
                list(recommendations.values()),
                tenant,
                "RightSizing",
                "pod_right_sizing",
            )
            archive_existing_recommendation(cloud_account_id, tenant)
            database.insert_data("recommendation", list(recommendations.values()), on_conflict=on_conflict)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(recommendations)}")
        handle_krr_score(cloud_account_id, score, tenant)
    except Exception:
        logging.exception(f"Failed to process report {json.dumps(content)}")


def handle_notification(
    account_name, cloud_account_id, recommendations, tenant, category, rule_name, additional_filters=None
):
    new_recommendation, archived_recommendation = get_recommendation_diff(
        tenant, cloud_account_id, recommendations, category, rule_name, additional_filters
    )
    logging.info(
        f"Account id {cloud_account_id} New recommendation {len(new_recommendation)} and archived recommendation "
        f"{len(archived_recommendation)}"
        f"for rule {rule_name} and category {category}"
    )


def generate_unique_key(data):
    return "-".join(
        str(data.get(column, ""))
        for column in ["cloud_account_id", "rule_name", "resource_id", "category", "account_object_id"]
    )


def generate_recommendation_map(recommendations):
    recommendation_map = {}
    for recommendation in recommendations:
        unique_key = generate_unique_key(recommendation)
        recommendation_map[unique_key] = recommendation
    return recommendation_map


def _find_legacy_key(recommendation, existing_map):
    """Try to match a namespace/name key against old bare-name keys in existing_map.

    Handles backward compatibility during the transition from account_object_id='name'
    to account_object_id='namespace/name'.
    """
    obj_id = recommendation.get("account_object_id", "")
    if "/" not in obj_id:
        return None
    legacy_key = generate_unique_key({**recommendation, "account_object_id": obj_id.split("/", 1)[1]})
    if legacy_key in existing_map:
        return legacy_key
    return None


def _parse_recommendation_field(rec_field):
    """Parse a recommendation field that may be a JSON string or already a dict."""
    if isinstance(rec_field, str):
        try:
            return json.loads(rec_field)
        except Exception:
            logging.warning("Failed to parse recommendation as JSON", exc_info=True)
    return rec_field


def _has_recommendation_changed(existing_rec, new_rec, rule_name):
    """Compare two recommendation objects and return True if the recommendation changed."""
    existing_rec = _parse_recommendation_field(existing_rec)
    new_rec = _parse_recommendation_field(new_rec)

    if (
        isinstance(existing_rec, dict)
        and isinstance(new_rec, dict)
        and "recommendation" in existing_rec
        and "recommendation" in new_rec
    ):
        e_nested = existing_rec["recommendation"]
        n_nested = new_rec["recommendation"]
        if isinstance(e_nested, dict) and isinstance(n_nested, dict) and rule_name == RuleName.PV_RIGHTSIZE.value:
            # For PV right sizing, ignore volatile usage data in comparison
            e_nested = {k: v for k, v in e_nested.items() if k != "usage"}
            n_nested = {k: v for k, v in n_nested.items() if k != "usage"}
        return e_nested != n_nested

    return existing_rec != new_rec


def get_recommendation_diff(
    tenant: str,
    cloud_account_id: str,
    data: list[Dict],
    category: str,
    rule_name: str,
    additional_filters: Dict[str, str] = {},
) -> tuple[list[Any], list[Any]]:
    query = (
        "select * from recommendation where tenant_id = %s and cloud_account_id = %s and status = 'Open' and "
        "category = %s and rule_name = %s"
    )
    values = [tenant, cloud_account_id, category, rule_name]
    if additional_filters:
        for key, value in additional_filters.items():
            query += f" and {key} = %s"
            values.append(value)
    existing_recommendation = database.run_query(
        query,
        values,
        cursor_factory=extras.RealDictCursor,
    )
    if len(existing_recommendation) == 0:
        return data, []
    existing_recommendation_map = generate_recommendation_map(existing_recommendation)
    new_recommendation_map = generate_recommendation_map(data)
    new = []
    archived = []

    for key, recommendation in new_recommendation_map.items():
        matched_key = (
            key if key in existing_recommendation_map else _find_legacy_key(recommendation, existing_recommendation_map)
        )
        if not matched_key:
            new.append(recommendation)
            continue

        existing = existing_recommendation_map[matched_key]
        if _has_recommendation_changed(
            existing["recommendation"], recommendation["recommendation"], recommendation.get("rule_name")
        ):
            new.append(recommendation)

    # Find archived items (present before, missing now)
    for key, recommendation in existing_recommendation_map.items():
        if key not in new_recommendation_map:
            # Backward compat: check if a new record with namespace/name matches this bare-name key
            obj_id = recommendation.get("account_object_id", "")
            matched = False
            if "/" not in obj_id:
                for new_rec in new_recommendation_map.values():
                    if _find_legacy_key(new_rec, {key: recommendation}) == key:
                        matched = True
                        break
            if not matched:
                archived.append(recommendation)

    return new, archived


def trigger_notification(
    new_recommendation: list[dict], archived_recommendation: list[dict], account_name: str
) -> None:
    if len(new_recommendation) == 0:
        return
    sorted_recommendations = sorted(
        new_recommendation,
        key=lambda x: (x["estimated_savings"] if "estimated_savings" in x else float("inf"), x["severity"]),
        reverse=True,
    )
    # filter sorted_recommendation if rule name is CIS and sorted_recommendation["recommendation"]["type"] is manual
    # or sorted_recommendation["recommendation"]["status"] is pass
    sorted_recommendations = [
        rec
        for rec in sorted_recommendations
        if rec["rule_name"] != RuleName.CIS.value
        or (
            json.loads(rec["recommendation"])["type"] != "manual"
            and json.loads(rec["recommendation"])["status"] != "PASS"
            and "Manual" not in json.loads(rec["recommendation"])["test_desc"]
        )
    ]

    if len(sorted_recommendations) == 0:
        return

    top_5_recommendations = sorted_recommendations[:5]

    affected_resource_count = len(set(rec.get("resource_id", "") for rec in new_recommendation))

    top_5_recommendation_objects = [
        {
            "id": rec.get("id"),
            "summary": generate_recommendation_summary(rec),
            "status": rec["status"],
            "savings": round(rec.get("estimated_savings", 0)),
            "severity": rec.get("severity"),
        }
        for rec in top_5_recommendations
    ]
    total_saving = round(sum(rec.get("estimated_savings", 0) for rec in new_recommendation))  # type: float

    recommendation_parameters = {
        "account_id": new_recommendation[0]["cloud_account_id"],
        "account_name": account_name,
        "category": new_recommendation[0]["category"],
        "rule_name": new_recommendation[0]["rule_name"],
        "rule_description": RULE_DESCRIPTION.get(new_recommendation[0]["rule_name"]),
        "recommendations": top_5_recommendation_objects,
        "total_estimated_savings": total_saving,
        "total_new": len(new_recommendation),
        "total_archived": len(archived_recommendation),
        "total_affected_resources": affected_resource_count,
    }
    recommendation_details = {
        "kind": "notification",
        "source": "optimize",
        "type": "k8s_recommendation",
        "tenant_id": new_recommendation[0]["tenant_id"],
        "parameters": recommendation_parameters,
    }
    logging.info(f"Sending recommendation notification {json.dumps(recommendation_details)}")
    rabbitmq_client.publish_message(Configs.NOTIFICATION_EXCHANGE, Configs.NOTIFICATION_QUEUE, recommendation_details)


def generate_recommendation_summary(recommendation: dict):
    if recommendation["rule_name"] == "pod_right_sizing":
        keys = recommendation["account_object_id"].split("/")
        return f"Rightsize the workload {keys[2]} in namespace {keys[0]}"
    if recommendation["rule_name"] == RuleName.UNUSED_PVC.value:
        return f"Delete the unused PV {recommendation['account_object_id']}"
    if recommendation["rule_name"] == RuleName.PV_RIGHTSIZE.value:
        rec = json.loads(recommendation["recommendation"])
        capacity = parse_size_to_gb(str(rec["recommendation"]["capacity"]))
        recommend_size = rec["recommendation"]["recommend_size"]
        return f"Resize the volume {recommendation['account_object_id']} from {capacity}GB to {recommend_size}GB"
    if recommendation["rule_name"] == RuleName.IMAGE_SCAN.value:
        rec = json.loads(recommendation["recommendation"])
        image = rec["image_name"]
        pkg = rec["PkgID"]
        cve = rec["VulnerabilityID"]
        return f"Found vulnerability {cve} for package {pkg} in the image {image}"
    if recommendation["rule_name"] == RuleName.CIS.value:
        rec = json.loads(recommendation["recommendation"])
        description = rec["test_desc"]
        return f"CIS-{recommendation['account_object_id']} {description}"


def generate_krr_recommendation(cloud_account_id, report, resource_map, tenant):
    recommendations = {}
    settings = get_recommendation_settings(cloud_account_id, "pod_right_sizing")
    for r in report["data"]:
        if f"{r['kind']}/{r['namespace']}/{r['name']}/{r['container']}" not in resource_map.keys():
            logging.info(f"Resource not found {r['kind']}/{r['namespace']}/{r['name']}/{r['container']}")
            continue
        resource_id = resource_map[f"{r['kind']}/{r['namespace']}/{r['name']}/{r['container']}"]
        ignore_recommendation, r_content, recommendation, saving = generate_container_recommendation(
            cloud_account_id, r, resource_id, settings, tenant
        )
        if ignore_recommendation:
            logging.info(f"Ignoring recommendation for {r['kind']}/{r['namespace']}/{r['name']}/{r['container']}")
            continue
        if resource_id[0] in recommendations:
            rec = json.loads(recommendations[resource_id[0]]["recommendation"])
            rec[r["container"]] = r_content
            recommendation["recommendation"] = json.dumps(rec)
            recommendation["estimated_savings"] = recommendations[resource_id[0]]["estimated_savings"] + saving
            recommendations[resource_id[0]] = recommendation
        else:
            recommendations[resource_id[0]] = recommendation
    return recommendations


def generate_container_recommendation(cloud_account_id, r, resource_id, settings, tenant):
    r_content = r["content"]
    cpu_cost_per_hour = DEFAULT_COST.get("CPU")
    memory_cost_per_hour = DEFAULT_COST.get("RAM")
    if resource_id[12] and resource_id[13] and resource_id[15] and str(resource_id[15]).lower() == "spot":
        spot_pricing = resource_id[12]
        az = resource_id[13]
        for spot in spot_pricing:
            if spot["az"] == az:
                cost = spot["price"]
                cpu = resource_id[16]
                memory = resource_id[17]
                cpu_cost_per_hour = (cost / ((cpu * 88) + (memory * 12)) * 88) / cpu
                memory_cost_per_hour = (cost / ((cpu * 88) + (memory * 12)) * 12) / memory
                break
    elif resource_id[6] and resource_id[7]:
        cpu_cost_per_hour = resource_id[6]
        memory_cost_per_hour = resource_id[7]

    saving, ignore_recommendation = process_resource_recommendation(
        cpu_cost_per_hour, memory_cost_per_hour, r_content, settings, resource_id[0]
    )
    content = {r["container"]: r_content}
    rightsize = json.dumps(content)
    recommendation = {
        "estimated_savings": saving,
        "cloud_account_id": cloud_account_id,
        "tenant_id": tenant,
        "resource_id": resource_id[0],
        "recommendation_action": "Modify",
        "category": "RightSizing",
        "rule_name": "pod_right_sizing",
        "recommendation": rightsize,
        "severity": get_severity(r["priority"]),
        "account_object_id": f"{r['namespace']}/{r['kind']}/{r['name']}",
        "status": "Open",
    }
    return ignore_recommendation, r_content, recommendation, saving


def process_resource_recommendation(cpu_cost_per_hour, memory_cost_per_hour, r_content, settings, resource_id):
    saving = 0
    new_cpu = 0
    original_cpu = 0
    new_memory = 0
    original_memory = 0
    ignore_recommendation = False
    for rec in r_content:
        new_hourly_cost = 0
        original_hourly_cost = 0
        if "info" in rec and (rec["info"] == "Not enough data" or rec["info"] == "No data"):
            logging.info(f"Not enough data Ignoring recommendation {resource_id}")
            ignore_recommendation = True
            continue
        if rec["recommended"]["request"] == "?":
            rec["recommended"]["request"] = rec["allocated"]["request"]
        if rec["resource"] == "cpu" and rec["allocated"]["request"]:
            new_cpu, new_hourly_cost, original_cpu, original_hourly_cost = parse_cpu_recommendation(
                cpu_cost_per_hour, rec, settings
            )
        if rec["resource"] == "memory" and rec["allocated"]["request"]:
            new_hourly_cost, new_memory, original_hourly_cost, original_memory = parse_memory_recommendation(
                memory_cost_per_hour, rec, settings
            )
        if new_hourly_cost != 0 and original_hourly_cost != 0:
            saving += (original_hourly_cost - new_hourly_cost) * 24 * 30
    if (
        new_cpu
        and original_cpu
        and new_memory
        and original_memory
        and (
            abs(new_cpu - original_cpu) / original_cpu * 100
            < settings.get(
                RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE_PROPERTY_NAME, RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE
            )
            and abs(new_memory - original_memory) / original_memory * 100
            < settings.get(
                RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE_PROPERTY_NAME, RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE
            )
        )
    ):
        ignore_recommendation = True
    return saving, ignore_recommendation


def parse_memory_recommendation(memory_cost_per_hour, rec, settings):
    original_memory = rec["allocated"]["request"]
    original_hourly_cost = (original_memory / (1024 * 1024 * 1024)) * memory_cost_per_hour
    new_memory = round_memory(rec["recommended"]["request"])
    if abs(new_memory - original_memory) / original_memory * 100 < settings.get(
        RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE_PROPERTY_NAME, RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE
    ):
        new_memory = original_memory
    if new_memory != rec["recommended"]["request"]:
        rec["recommended"]["request"] = new_memory
        rec["recommended"]["limit"] = new_memory
    new_hourly_cost = (new_memory / (1024 * 1024 * 1024)) * memory_cost_per_hour
    return new_hourly_cost, new_memory, original_hourly_cost, original_memory


def parse_cpu_recommendation(cpu_cost_per_hour, rec, settings):
    original_cpu = rec["allocated"]["request"]
    original_hourly_cost = original_cpu * cpu_cost_per_hour
    new_cpu = rec["recommended"]["request"]
    if abs(new_cpu - original_cpu) / original_cpu * 100 < settings.get(
        RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE_PROPERTY_NAME, RIGHT_SIZING_CHANGE_THRESHOLD_PERCENTAGE
    ):
        new_cpu = original_cpu
    new_hourly_cost = new_cpu * cpu_cost_per_hour
    return new_cpu, new_hourly_cost, original_cpu, original_hourly_cost


def get_existing_resource(cloud_account_id, report, tenant):
    """Get existing resource mappings using WORKLOAD cloud_resource_id - prevents duplicate recommendations."""
    resource_names = []
    for r in report["data"]:
        resource_names.append(f"{r['kind']}/{r['namespace']}/{r['name']}/{r['container']}")
    query = sql.SQL("""
        select
            kw.cloud_resource_id as id,  -- WORKLOAD's cloud_resource_id
            kw.tenant_id as tenant,
            kw.kind as controller_kind,
            kw.name as controller,
            (SELECT jsonb_agg(DISTINCT c)
             FROM k8s_pods kp,
                  jsonb_array_elements((kp.meta -> 'config' -> 'containers')::jsonb) AS c
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
            ) AS containers,
            kw.namespace,
            -- Get cost data from any pod belonging to this workload
            (SELECT (crd.resource_cost * (CAST((crd.resource_capacity ->> 'cpu_virtual') AS INTEGER) * 88) /
                    ((CAST((crd.resource_capacity ->> 'cpu_virtual') AS INTEGER) * 88) +
                     (CAST((crd.resource_capacity ->> 'memory_gb') AS DECIMAL) * 12))) /
                    CAST((crd.resource_capacity ->> 'cpu_virtual') AS INTEGER)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             LEFT JOIN cloud_resource_details crd ON
               crd.resource_type = (ksn.meta -> 'node_info' -> 'labels' ->>
                                   'node.kubernetes.io/instance-type'::text)
               AND crd.resource_region = (ksn.meta -> 'node_info' -> 'labels' ->>
                                          'topology.kubernetes.io/region'::text)
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS cpu_cost_per_unit,
            (SELECT (crd.resource_cost * (CAST((crd.resource_capacity ->> 'memory_gb') AS DECIMAL) * 12) /
                    ((CAST((crd.resource_capacity ->> 'cpu_virtual') AS INTEGER) * 88) +
                     (CAST((crd.resource_capacity ->> 'memory_gb') AS DECIMAL) * 12))) /
                    CAST((crd.resource_capacity ->> 'memory_gb') AS DECIMAL)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             LEFT JOIN cloud_resource_details crd ON
               crd.resource_type = (ksn.meta -> 'node_info' -> 'labels' ->>
                                   'node.kubernetes.io/instance-type'::text)
               AND crd.resource_region = (ksn.meta -> 'node_info' -> 'labels' ->>
                                          'topology.kubernetes.io/region'::text)
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS memory_cost_per_gb,
            (SELECT crd.resource_cost
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             LEFT JOIN cloud_resource_details crd ON
               crd.resource_type = (ksn.meta -> 'node_info' -> 'labels' ->>
                                   'node.kubernetes.io/instance-type'::text)
               AND crd.resource_region = (ksn.meta -> 'node_info' -> 'labels' ->>
                                          'topology.kubernetes.io/region'::text)
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS resource_cost,
            (SELECT (ksn.meta -> 'node_info' -> 'labels' ->> 'node.kubernetes.io/instance-type'::text)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS instance_type,
            kw.cloud_account_id as account,
            kw.cloud_resource_id as resourse_id,  -- For compatibility with existing code
            (SELECT crd.spot_pricing
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             LEFT JOIN cloud_resource_details crd ON
               crd.resource_type = (ksn.meta -> 'node_info' -> 'labels' ->>
                                   'node.kubernetes.io/instance-type'::text)
               AND crd.resource_region = (ksn.meta -> 'node_info' -> 'labels' ->>
                                          'topology.kubernetes.io/region'::text)
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS spot_pricing,
            (SELECT (ksn.meta -> 'node_info' -> 'labels' ->> 'topology.kubernetes.io/zone'::text)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS az,
            (SELECT (ksn.meta -> 'node_info' -> 'labels' ->> 'topology.kubernetes.io/region'::text)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS instance_region,
            (SELECT ksn.node_type
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS node_type,
            (SELECT CAST((crd.resource_capacity ->> 'cpu_virtual'::text) AS INTEGER)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             LEFT JOIN cloud_resource_details crd ON
               crd.resource_type = (ksn.meta -> 'node_info' -> 'labels' ->>
                                   'node.kubernetes.io/instance-type'::text)
               AND crd.resource_region = (ksn.meta -> 'node_info' -> 'labels' ->>
                                          'topology.kubernetes.io/region'::text)
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS cpu_virtual,
            (SELECT CAST((crd.resource_capacity ->> 'memory_gb'::text) AS INTEGER)
             FROM k8s_pods kp
             INNER JOIN k8s_nodes ksn ON ksn.tenant_id = kp.tenant_id
               AND kp.cloud_account_id = ksn.cloud_account_id
               AND kp.node_name = ksn.name
             LEFT JOIN cloud_resource_details crd ON
               crd.resource_type = (ksn.meta -> 'node_info' -> 'labels' ->>
                                   'node.kubernetes.io/instance-type'::text)
               AND crd.resource_region = (ksn.meta -> 'node_info' -> 'labels' ->>
                                          'topology.kubernetes.io/region'::text)
             WHERE kp.cloud_account_id = kw.cloud_account_id
               AND kp.tenant_id = kw.tenant_id
               AND kp.workload_name = kw.name
               AND kp.workload_type = kw.kind
               AND kp.namespace = kw.namespace
               AND kp.is_active = true
             LIMIT 1
            ) AS memory_gb
        from k8s_workloads as kw
        where kw.cloud_account_id = %s
          and kw.tenant_id = %s
          and kw.is_active = true
        """)
    values = [cloud_account_id, tenant]
    resource_list = database.run_query(query, values)
    logging.info(
        f"Found {len(resource_list)} workloads for account {cloud_account_id} (using WORKLOAD cloud_resource_id)"
    )
    return resource_list


def archive_existing_recommendation(cloud_account_id, tenant):
    update_to_archieve = (
        "update recommendation set status = %s where tenant_id = %s and cloud_account_id = %s and category = %s and"
        " rule_name = %s and status not in  ('Closed', 'InProgress')"
    )
    database.run_query(update_to_archieve, ["Archive", tenant, cloud_account_id, "RightSizing", "pod_right_sizing"])


def handle_krr_score(cloud_account_id, score, tenant):
    score = {"score": score, "source": "krr", "cloud_account_id": cloud_account_id, "tenant": tenant}
    on_conflict = "ON CONFLICT (cloud_account_id, tenant, source) DO UPDATE SET score = (EXCLUDED.score)"
    try:
        logging.debug("inserting score for account_id {}".format(cloud_account_id))
        database.insert_data("cloud_account_score", [score], on_conflict=on_conflict)
    except Exception:
        logging.exception(f"Failed to insert row {json.dumps(score)}")


def format_evidence(evidence, config_change_event: List, aggregation_key: str):
    formatted_evidence = []
    for data in evidence:
        if "data" in data and isinstance(data["data"], str):
            data = json.loads(data["data"])
        for item in data:
            action_name = ""
            if (
                "additional_info" in item
                and isinstance(item["additional_info"], dict)
                and "action_name" in item.get("additional_info")
            ):
                action_name = item["additional_info"]["action_name"]
            insights: List[Dict[str, Any]] = []
            if item["type"] == "json" and "data" in item:
                event_data = item["data"]
                if isinstance(item["data"], str):
                    event_data = json.loads(item["data"])
                insights = handle_evidence_formatting(event_data)
            formatted_evidence.append(item)
            if item["type"] == "table":
                insights.extend(handle_events(item))
            if (
                "additional_info" in item
                and isinstance(item["additional_info"], dict)
                and "insights" in item["additional_info"]
            ):
                for insight in item["additional_info"].get("insights"):
                    insights.append(
                        {
                            "message": insight.get("message"),
                            "severity": insight.get("severity"),
                        }
                    )
            if action_name == "service_map_enricher":
                check_neighboring_workload_health(item, {})
            item["insight"] = insights
    if len(config_change_event) > 0:
        try:
            config_data = get_config_change_data(config_change_event, aggregation_key)
            formatted_evidence.append(config_data)
        except Exception:
            logging.exception("Failed to handle config change event %s", config_change_event)
    return formatted_evidence


def get_event_labels(evidence) -> Dict[str, str]:
    labels = {}
    for data in evidence:
        if "data" in data and isinstance(data["data"], str):
            data = json.loads(data["data"])
        for item in data:
            if item["type"] == "table" and item.get("data", {}).get("table_name", "") == "*Alert labels*":
                events = item["data"]
                for row in events["rows"]:
                    labels[row[0]] = row[1]
    return labels


def _parse_json_dict(data):
    """Coerce data into a dict suitable for get_change_insights.

    Accepts a string (JSON-decoded), an existing dict, or anything else
    (returned as an empty diff). Never raises on malformed input.
    """
    if isinstance(data, str):
        try:
            data = json.loads(data)
        except (json.JSONDecodeError, TypeError):
            return {"updated_paths": []}
    if isinstance(data, dict):
        return data
    return {"updated_paths": []}


def get_config_change_data(config_change_event, aggregation_key):
    # config_change_event[0] = (id, service_key, evidences, starts_at).
    # evidences is a JSONB array of evidence blocks. One historic shape was a
    # single-element [{type:"diff", data:object}]; another shape prepends a
    # raw kubewatch {type:"json", data:"<json-string>"} so the diff lives at
    # position 1+. Scan for the entry with type=="diff" rather than
    # hard-coding [0] — the indexed path raised KeyError on the kubewatch
    # JSON string and the exception was swallowed by the caller, dropping
    # the diff from the rendered evidence.
    evidences = config_change_event[0][2] or []
    diff_entry = None
    for ev in evidences:
        if isinstance(ev, dict) and ev.get("type") == "diff" and ev.get("data") is not None:
            diff_entry = ev
            break

    if diff_entry is not None:
        data = _parse_json_dict(diff_entry.get("data"))
        config_data = {
            "type": "diff",
            "data": data,
            "insight": get_change_insights(data, aggregation_key),
        }
    else:
        # Legacy fallback: evidences[0].data may be a JSON-string encoded list
        # whose first element is the diff block, or a dict with the diff fields
        # directly. Guard against missing keys and malformed JSON.
        first = evidences[0] if evidences else {}
        raw = first.get("data") if isinstance(first, dict) else None
        if isinstance(raw, str):
            try:
                decoded = json.loads(raw)
            except (json.JSONDecodeError, TypeError):
                decoded = []
            if isinstance(decoded, list) and decoded and isinstance(decoded[0], dict):
                config_data = decoded[0]
            else:
                data = _parse_json_dict(decoded if isinstance(decoded, dict) else None)
                config_data = {
                    "type": "diff",
                    "data": data,
                    "insight": get_change_insights(data, aggregation_key),
                }
        else:
            data = _parse_json_dict(raw)
            config_data = {
                "type": "diff",
                "data": data,
                "insight": get_change_insights(data, aggregation_key),
            }
    config_data["start_at"] = config_change_event[0][3].isoformat()
    return config_data


def get_change_insights(config_data, aggregation_key) -> List[Dict[str, Any]]:
    insights = []
    unique_insights = set()

    for updated_path in config_data["updated_paths"]:
        if aggregation_key == "image_pull_backoff_reporter":
            if "image" in updated_path:
                insight = {
                    "message": "Container image was updated",
                    "severity": "Critical",
                    "recommendation": "Verify pod specification and provide the correct registry",
                }
                add_unique_insight(insights, unique_insights, insight)
                break
        else:
            get_resource_update_insight(aggregation_key, insights, unique_insights, updated_path)

    return insights


def get_resource_update_insight(aggregation_key, insights, unique_insights, updated_path):
    if "resources.limits['memory']" in updated_path:
        insight = {
            "message": "Resource memory limit were updated",
            "severity": "Critical",
        }
        if aggregation_key == "pod_oom_killer_enricher":
            insight["recommendation"] = "Increase memory limit in pod specifications"
        add_unique_insight(insights, unique_insights, insight)
    elif "resources.requests['memory']" in updated_path:
        insight = {
            "message": "Resource memory request were updated",
            "severity": "Critical",
        }
        add_unique_insight(insights, unique_insights, insight)
    elif "resources.requests['cpu']" in updated_path:
        insight = {
            "message": "Resource cpu request were updated",
            "severity": "Info",
        }
        add_unique_insight(insights, unique_insights, insight)
    elif "resources.limits['cpu']" in updated_path:
        insight = {
            "message": "Resource cpu limit were updated",
            "severity": "Info",
        }
        add_unique_insight(insights, unique_insights, insight)


def add_unique_insight(insights, unique_insights, insight):
    insight_tuple = tuple(sorted(insight.items()))
    if insight_tuple not in unique_insights:
        unique_insights.add(insight_tuple)
        insights.append(insight)


def handle_evidence_formatting(event_data) -> List[Dict[str, Any]]:
    insights: List[Dict[str, Any]] = []
    if "name" in event_data and event_data["name"] == "noisy_neighbours":
        insights.extend(hande_nn_insights(event_data))
    if "name" in event_data and event_data["name"] == "container_metric":
        insights.extend(handle_container_insight(event_data, "container"))
    if "name" in event_data and event_data["name"] == "pod_metric":
        insights.extend(handle_container_insight(event_data, "pod"))
    if "name" in event_data and event_data["name"] == "api_traces_enricher":
        insights.extend(handle_traces_insight(event_data))
    return insights


def is_increasing(arr):
    return all(float(arr[i]) >= float(arr[i - 1]) for i in range(1, len(arr)))


def handle_events(event_data) -> List[Dict[str, Any]]:
    """Emit BackOff/Warning insights from a Recent-events table evidence block.

    Resolve the Reason/Type/Message columns by case-insensitive header name
    instead of hard-coding row[0]/row[1]/row[5]. The block has appeared with
    different header casings and column orderings across producers; index-
    based access silently dropped insights when either changed.
    """
    events = event_data["data"]
    insights: List[Dict[str, Any]] = []
    headers = events.get("headers") or []

    def col(name: str):
        for i, h in enumerate(headers):
            if isinstance(h, str) and h.lower() == name:
                return i
        return -1

    i_reason, i_type, i_message = col("reason"), col("type"), col("message")
    if i_reason < 0 or i_type < 0 or i_message < 0:
        return insights

    for row in events.get("rows") or []:
        if i_message >= len(row):
            continue
        type_val = row[i_type] if i_type < len(row) else ""
        reason_val = row[i_reason] if i_reason < len(row) else ""
        if type_val == "Warning" or reason_val == "BackOff":
            insights.append({"message": row[i_message], "severity": "Critical"})
    return insights


def handle_traces_insight(event_data: Dict[str, Any]) -> List[Dict[str, Any]]:
    """
    Generate insights from trace data.

    Args:
        event_data (Dict[str, Any]): Event data containing traces

    Returns:
        List[Dict[str, Any]]: List of insights
    """
    traces = event_data.get("data", [])
    insights = []
    seen_messages = set()

    # Find root cause
    for trace in traces:
        error = TraceAnalyzer.categorize_error(trace)
        if error:  # Ensure error is not None before processing
            generated_msg = TraceAnalyzer.generate_error_message(error)
            if generated_msg not in seen_messages:
                seen_messages.add(generated_msg)
                insights.append({"message": generated_msg, "severity": "Critical"})
    # Generate service flow insight
    service_flow = TraceAnalyzer.extract_service_flow(traces)
    if service_flow:
        path_message = " -> ".join([f"{service['name']} ({service['http_method']})" for service in service_flow])
        insights.append({"message": f"Trace Path: {path_message}", "severity": "Info"})

    return insights


def handle_container_insight(event_data, type: str) -> List[Dict[str, Any]]:
    insights: List[Dict[str, Any]] = []
    container_list = event_data["data"]
    for container_data in container_list:
        max_usage = max([float(value) for value in container_data["values"]])
        container_name = container_data["metric"][type]
        handle_container_limit_insight(container_data, container_name, insights, max_usage, type)
        handle_container_request_insight(container_data, container_name, insights, max_usage, type)
    return insights


def handle_container_request_insight(container_data, container_name, insights, max_usage, type):
    if (
        "requests" in container_data["metric"]
        and "memory" in container_data["metric"]["requests"]
        and float(container_data["metric"]["requests"]["memory"]) > 0
    ):
        memory_requested = float(container_data["metric"]["requests"]["memory"])
        if ((max_usage / memory_requested) * 100) > 80:
            insight = {
                "message": (
                    f"{type.title()} {container_name} is using {int((max_usage / memory_requested) * 100)}% of"
                    " requested"
                ),
                "severity": "High",
            }
            insights.append(insight)
    else:
        insight = {
            "message": f"{type.title()} {container_name} does not have memory request specified",
            "severity": "Critical",
        }
        insights.append(insight)


def handle_container_limit_insight(container_data, container_name, insights, max_usage, type):
    if (
        "limits" in container_data["metric"]
        and "memory" in container_data["metric"]["limits"]
        and float(container_data["metric"]["limits"]["memory"]) > 0
    ):
        memory_limit = float(container_data["metric"]["limits"]["memory"])

        if ((max_usage / memory_limit) * 100) > 80:
            insight = {
                "message": (
                    f"{type.title()} {container_name} is using {int((max_usage / memory_limit) * 100)}% of limit"
                ),
                "severity": "High",
            }
            if ((max_usage / memory_limit) * 100) > CONTAINER_MEMORY_THRESHOLD:
                insight["severity"] = "Critical"
                insight["recommendation"] = "Increase memory limit in pod specifications"
            insights.append(insight)
    else:
        insight = {
            "message": f"{type.title()} {container_name} does not have memory limit specified",
            "severity": "High",
        }
        insights.append(insight)


def hande_nn_insights(event_data) -> List[Dict[str, Any]]:
    insights: List[Dict[str, Any]] = []
    noisy_neighbours_details = event_data["data"]
    memory_requested = noisy_neighbours_details.get("memory_requested", 0)
    memory_allocatable = noisy_neighbours_details.get("memory_allocatable", 0)
    if memory_requested and memory_allocatable:
        percentage_used = int((memory_requested / memory_allocatable) * 100)
    else:
        percentage_used = 0
    if percentage_used > 80:
        insight = {
            "message": f"Node memory allocation is at {percentage_used}%",
            "severity": "High",
        }
        if percentage_used > NODE_MEMORY_THRESHOLD:
            insight["severity"] = "Critical"
            insight["recommendation"] = (
                "Adjust memory requests (minimal threshold) and memory limits (maximal threshold) in your containers"
            )
        insights.append(insight)
    pods_missing_request = 0
    pods_missing_limit = 0
    pods_using_more_than_request = 0
    for neighbor in noisy_neighbours_details["neighbours"]:
        if "memory_requested" not in neighbor or not neighbor["memory_requested"]:
            pods_missing_request = pods_missing_request + 1
        elif (
            "memory_used" in neighbor
            and neighbor.get("memory_used")
            and neighbor.get("memory_used", 0) > neighbor.get("memory_requested", 0)
        ):
            pods_using_more_than_request = pods_using_more_than_request + 1
        if "memory_limit" not in neighbor or not neighbor.get("memory_limit", 0):
            pods_missing_limit = pods_missing_limit + 1
        if "memory_used" in neighbor and neighbor.get("memory_used"):
            used_percentage = 0
            if memory_allocatable and memory_allocatable > 0:
                used_percentage = int(neighbor.get("memory_used", 0) / memory_allocatable * 100)
            if used_percentage > 20:
                insight = {
                    "message": (
                        f"container {neighbor['name']} of pod {neighbor['pod_name']} is using {used_percentage} of node"
                        " memory"
                    ),
                    "severity": "High",
                }
                insights.append(insight)
    if pods_using_more_than_request > 0:
        insight = {
            "message": f"{pods_using_more_than_request} pods are using more than requested",
            "severity": "High",
        }
        insights.append(insight)
    if pods_missing_limit > 0:
        insight = {
            "message": f"{pods_missing_limit} pods does not have limit specified",
            "severity": "High",
        }
        insights.append(insight)
    if pods_missing_request > 0:
        insight = {
            "message": f"{pods_missing_request} pods does not have request specified",
            "severity": "High",
        }
        insights.append(insight)
    return insights


def get_kind(kind: str):
    if kind == "deployments":
        return "Deployment"
    if kind == "statefulsets":
        return "StatefulSet"
    if kind == "daemonsets":
        return "DaemonSet"
    if kind == "pods":
        return "Pod"
    return kind


def get_popeye_severity(priority: int):
    if priority == 1:
        return "Info"
    if priority == 2:
        return "Medium"
    if priority == 3:
        return "Critical"


def handle_popeye_report(raw_content: dict, tenant: str, cloud_account_id: str, account_name: str):
    logging.info("Processing popeye report for tenant {} , account id {}".format(tenant, cloud_account_id))
    report = json.loads(raw_content["evidence"][0]["data"])[0]
    service_keys = []
    for r in report["data"]:
        service_key = f'{r["namespace"]}/{get_kind(r["kind"])}/{r["name"]}'
        service_keys.append(service_key)
    resource_select_query = sql.SQL(
        'select ksp.external_id , ksw.cloud_resource_id , ksw."name" , ksw."namespace", ksp.tenant_id , '
        "ksp.cloud_account_id from k8s_pods ksp inner join k8s_workloads ksw on ksp.tenant_id = ksw.tenant_id "
        "and ksp.cloud_account_id = ksw.cloud_account_id and ksp.workload_name = ksw.name and ksp.workload_type "
        '= ksw.kind and ksp."namespace" = ksw."namespace" where ksp.external_id in %s and ksp.tenant_id = %s and '
        'ksp.cloud_account_id = %s group by ksp.external_id , ksw.cloud_resource_id , ksw."name" , ksw."namespace"'
        ", ksp.tenant_id , ksp.cloud_account_id"
    ).format(sql.Identifier("cloud_resourses"))
    resources_data = database.run_query(resource_select_query, [tuple(service_keys), tenant, cloud_account_id])

    resources_map = {}
    for r in resources_data:
        resources_map[r[0]] = r

    general_recommendations, resource_recommendations = process_popeye_report(
        cloud_account_id, report, resources_map, tenant
    )

    recommendations = []
    for key, value in general_recommendations.items():
        if len(value) > 0 and "content" in r:
            max_severity = max(resource["level"] if "level" in resource else 0 for resource in r["content"])
            recommendation = {
                "cloud_account_id": cloud_account_id,
                "tenant_id": tenant,
                "recommendation_action": "Modify",
                "resource_id": None,
                "category": "Configuration",
                "rule_name": key + "_misconfigurations",
                "recommendation": json.dumps(value),
                "severity": get_popeye_severity(max_severity),
                "status": "Open",
                "account_object_id": key,
            }
            recommendations.append(recommendation)

    for key, value in resource_recommendations.items():
        if value["recommendation"]:
            value["recommendation"] = json.dumps(value["recommendation"])
            recommendations.append(value)

    # collect unique rule name
    rule_names = set([rec["rule_name"] for rec in recommendations])
    # archieve recommendation

    archive_existing_with_rules(cloud_account_id, tenant, "Configuration", rule_names)
    upsert_recommendations(recommendations)
    handle_popeye_score(cloud_account_id, report, tenant)


def process_popeye_report(cloud_account_id, report, resources_map, tenant):
    general_recommendations = {}
    resource_recommendations = {}
    for r in report["data"]:
        service_key = f'{r["namespace"]}/{get_kind(r["kind"])}/{r["name"]}'
        if service_key in resources_map:
            handle_existing_resource_best_practices(
                cloud_account_id, r, resource_recommendations, resources_map, service_key, tenant
            )
        else:
            if r["kind"] not in general_recommendations:
                general_recommendations[r["kind"]] = []
            for content in r["content"]:
                if content["level"] != 0:
                    code, message = get_message_code_message(content)
                    recommendation = {
                        "namespace": r["namespace"],
                        "kind": r["kind"],
                        "container": r["container"],
                        "name": r["name"],
                        "message": message,
                        "category": POPEYE_RULES_MAP.get(code, {}).get("category", ""),
                    }
                    general_recommendations[r["kind"]].append(recommendation)
    return general_recommendations, resource_recommendations


def get_message_code_message(content):
    message = content["message"]
    # extract id from the message
    code = re.search(POPEYE_CODE_PATTERN, message)
    if code:
        # extract only number
        code = code.group(0).split("-")[1].replace("]", "")
        message = re.sub(POPEYE_CODE_PATTERN, "", message).strip()
    return code, message


def handle_existing_resource_best_practices(
    cloud_account_id, r, resource_recommendations, resources_map, service_key, tenant
):
    resource = resources_map[service_key]
    max_severity = max(resource["level"] if "level" in resource else 0 for resource in r["content"])
    if service_key in resource_recommendations:
        recommendation = resource_recommendations[service_key]
    else:
        recommendation = {
            "cloud_account_id": cloud_account_id,
            "tenant_id": tenant,
            "resource_id": resource[1],
            "recommendation_action": "Modify",
            "category": "Configuration",
            "rule_name": "misconfigurations",
            "severity": get_popeye_severity(max_severity),
            "recommendation": [],
            "status": "Open",
            "account_object_id": service_key,
        }
    for content in r["content"]:
        if content["level"] != 0:
            message = content["message"]
            # extract id from the message
            code = re.search(POPEYE_CODE_PATTERN, message)
            if code:
                # extract only number not brackets
                code = code.group(0).split("-")[1].replace("]", "")

                message = re.sub(POPEYE_CODE_PATTERN, "", message).strip()
            insight = {
                "namespace": r["namespace"],
                "kind": r["kind"],
                "container": r["container"],
                "controller": resource[2],
                "controller_kind": resource[3],
                "name": r["name"],
                "message": message,
                "category": POPEYE_RULES_MAP.get(code, {}).get("category", ""),
            }
            recommendation["recommendation"].append(insight)
            resource_recommendations[service_key] = recommendation


def archive_existing_with_rule(cloud_account_id, tenant, category, rule_name):
    update_to_archieve = (
        "update recommendation set status = %s where tenant_id = %s and cloud_account_id = %s and category = %s and"
        " rule_name = %s"
    )
    database.run_query(update_to_archieve, ["Archive", tenant, cloud_account_id, category, rule_name])


def archive_existing_with_rules(cloud_account_id, tenant, category, rule_names):
    if not rule_names:
        return
    placeholders = ",".join(["%s"] * len(rule_names))
    update_to_archieve = (
        "update recommendation set status = %s where tenant_id = %s and cloud_account_id = %s and"
        f" category = %s and rule_name in ({placeholders})"
    )
    database.run_query(update_to_archieve, ["Archive", tenant, cloud_account_id, category] + list(rule_names))


def archive_existing_with_object_id(cloud_account_id, tenant, category, account_object_id):
    update_to_archieve = (
        "update recommendation set status = %s where tenant_id = %s and cloud_account_id = %s and category = %s and "
        "account_object_id = %s"
    )
    database.run_query(update_to_archieve, ["Archive", tenant, cloud_account_id, category, account_object_id])


def archive_image_scan_recommendations(cloud_account_id, tenant, image_name):
    query = (
        "update recommendation set status = %s where tenant_id = %s and cloud_account_id = %s "
        "and category = 'Security' and rule_name = 'image_scan' "
        "and recommendation->>'image_name' = %s and status != 'Archive'"
    )
    database.run_query(query, ["Archive", tenant, cloud_account_id, image_name])


def upsert_recommendations(recommendations):
    recommendations = dedup_recommendations(recommendations)
    on_conflict = (
        "ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id) DO UPDATE SET "
        "recommendation = EXCLUDED.recommendation, estimated_savings =EXCLUDED.estimated_savings, "
        "status=EXCLUDED.status"
    )
    try:
        database.insert_data("recommendation", recommendations, on_conflict=on_conflict)
    except Exception:
        logging.exception(f"Failed to insert row {json.dumps(recommendations)}")


def dedup_recommendations(recommendations):
    unique_columns = ["cloud_account_id", "rule_name", "resource_id", "category", "account_object_id"]
    deduped_recommendations = []
    seen = set()
    for rec in recommendations:
        # handle key error
        try:
            key = tuple(rec[col] for col in unique_columns)
        except KeyError:
            logging.exception(f"Key error in recommendation {rec}")
            continue
        if key not in seen:
            seen.add(key)
            deduped_recommendations.append(rec)
        else:
            logging.debug(f"Duplicate recommendation {rec}")
    return deduped_recommendations


def handle_popeye_score(cloud_account_id, report, tenant):
    score = report["score"]
    score = {"score": score, "source": "popeye", "cloud_account_id": cloud_account_id, "tenant": tenant}
    on_conflict = "ON CONFLICT (cloud_account_id, tenant, source) DO UPDATE SET score = (EXCLUDED.score)"
    try:
        logging.debug(f"inserting score for account_id {cloud_account_id}")
        database.insert_data("cloud_account_score", [score], on_conflict=on_conflict)
    except Exception:
        logging.exception(f"Failed to insert row {json.dumps(score)}")


def handle_kube_bench(raw_content: dict, tenant: str, cloud_account_id: str, account_name: str):
    logging.info("Processing kube bench report for tenant {} , account id {}".format(tenant, cloud_account_id))
    report = json.loads(raw_content["evidence"][0]["data"])[0]
    recommendations = []
    for r in report["data"]:
        for control in r.get("content", []):
            for test in control.get("tests", []):
                for result in test.get("results", []):
                    recommendation = {
                        "cloud_account_id": cloud_account_id,
                        "tenant_id": tenant,
                        "recommendation_action": "Modify",
                        "resource_id": None,
                        "category": Category.SECURITY.value,
                        "rule_name": RuleName.CIS.value,
                        "recommendation": json.dumps(result),
                        "severity": "Info",
                        "account_object_id": result["test_number"],
                        "status": "Open",
                    }
                    recommendations.append(recommendation)
    handle_notification(
        account_name, cloud_account_id, recommendations, tenant, Category.SECURITY.value, RuleName.CIS.value
    )
    archive_existing_with_rule(cloud_account_id, tenant, Category.SECURITY.value, RuleName.CIS.value)
    upsert_recommendations(recommendations)


def handle_abandoned_pv(raw_content: dict, tenant: str, cloud_account_id: str, account_name: str):
    logging.info("Processing pv report report for tenant {} , account id {}".format(tenant, cloud_account_id))
    report = json.loads(raw_content["evidence"][0]["data"])[0]
    recommendations = []
    for r in report["data"]:
        for pv in r["content"]:
            saving = 0.10 * parse_size_to_gb(pv["spec"]["capacity"]["storage"])
            recommendation = {
                "cloud_account_id": cloud_account_id,
                "tenant_id": tenant,
                "recommendation_action": "Modify",
                "resource_id": None,
                "category": "RightSizing",
                "rule_name": "unused_pvc",
                "recommendation": json.dumps(pv),
                "severity": "High",
                "account_object_id": f"{pv['metadata'].get('namespace') or ''}/{pv['metadata']['name']}",
                "estimated_savings": saving,
                "status": "Open",
            }
            recommendations.append(recommendation)
    handle_notification(
        account_name, cloud_account_id, recommendations, tenant, Category.RIGHT_SIZING.value, RuleName.UNUSED_PVC.value
    )
    archive_existing_with_rule(cloud_account_id, tenant, Category.RIGHT_SIZING.value, RuleName.UNUSED_PVC.value)
    upsert_recommendations(recommendations)


def handle_pv_rightsize(raw_content: dict, tenant: str, cloud_account_id: str, account_name: str):
    logging.info("Processing pv right sizing report for tenant {} , account id {}".format(tenant, cloud_account_id))
    report = json.loads(raw_content["evidence"][0]["data"])[0]
    recommendations = []
    for r in report["data"]:
        for pv in r["content"]:
            usage_ = pv.get("usage")
            if usage_ is None:
                continue
            if usage_.get("current") is None:
                continue
            current_usage_metric = float(
                usage_["current"]["value"][1] if usage_.get("current", {}).get("value") else 0.0
            )
            last_30_days_usage_metric = float(usage_["7_days"]["value"][1]) if usage_.get("7_days") else None
            capacity = parse_size_to_gb(pv["spec"]["capacity"]["storage"]) * 1024 * 1024 * 1024
            recommended_volume_size = 0
            if (current_usage_metric / capacity) > 0.9:
                # pvc is already 90 percent full
                recommended_volume_size = round_bytes_to_gb(capacity * 1.2)
                saving = -(round_bytes_to_gb(capacity) * 0.2 * 0.10)
            elif last_30_days_usage_metric is not None:
                current_free_storage = capacity - current_usage_metric
                free_storage_space7_days_ago = capacity - last_30_days_usage_metric

                pfss90 = current_free_storage + 10 * (current_free_storage - free_storage_space7_days_ago)

                mfss = max(0.0, min(pfss90, free_storage_space7_days_ago))

                recommended_volume_size = round_bytes_to_gb((capacity - mfss) * 1.3)

                storage_size_save = round_bytes_to_gb(capacity - recommended_volume_size)

                if recommended_volume_size <= 0:
                    continue

                saving = 0.10 * storage_size_save
            if recommended_volume_size:
                pv["recommendation"] = {
                    "capacity": capacity,
                    "usage": {"before_30_days": last_30_days_usage_metric, "current": current_usage_metric},
                    "recommend_size": recommended_volume_size,
                    "price_per_gb": 0.10,
                }
                recommendation = {
                    "cloud_account_id": cloud_account_id,
                    "tenant_id": tenant,
                    "recommendation_action": "Modify",
                    "resource_id": None,
                    "category": "RightSizing",
                    "rule_name": "pv_rightsize",
                    "recommendation": json.dumps(pv),
                    "severity": "High",
                    "account_object_id": f"{pv['metadata'].get('namespace') or ''}/{pv['metadata']['name']}",
                    "estimated_savings": saving,
                    "status": "Open",
                }
                recommendations.append(recommendation)
    handle_notification(
        account_name,
        cloud_account_id,
        recommendations,
        tenant,
        Category.RIGHT_SIZING.value,
        RuleName.PV_RIGHTSIZE.value,
    )
    archive_existing_with_rule(cloud_account_id, tenant, "RightSizing", "pv_rightsize")
    upsert_recommendations(recommendations)


def handle_image_scan(raw_content: dict, tenant: str, cloud_account_id: str, account_name: str):
    logging.info("Processing image scan report for tenant {} , account id {}".format(tenant, cloud_account_id))
    report = json.loads(raw_content["evidence"][0]["data"])[0]
    recommendations = []
    image_name = None
    image_name = parse_image_scan(cloud_account_id, image_name, recommendations, report, tenant)
    if len(recommendations) > 0:
        handle_notification(
            account_name,
            cloud_account_id,
            recommendations,
            tenant,
            Category.SECURITY.value,
            RuleName.IMAGE_SCAN.value,
            {"recommendation->>'image_name'": image_name},
        )
        archive_image_scan_recommendations(cloud_account_id, tenant, image_name)
        upsert_recommendations(recommendations)


def parse_image_scan(cloud_account_id, image_name, recommendations, report, tenant):
    for r in report["data"]:
        for image in r["content"]:
            if "Results" not in image:
                continue
            results = image["Results"]
            image_name = image["ArtifactName"]
            for result in results:
                if "Vulnerabilities" not in result:
                    continue
                vulnerabilities = result["Vulnerabilities"]
                for vulnerability in vulnerabilities:
                    vulnerability["image_name"] = image_name
                    recommendation = {
                        "cloud_account_id": cloud_account_id,
                        "tenant_id": tenant,
                        "recommendation_action": "Modify",
                        "resource_id": None,
                        "category": "Security",
                        "rule_name": "image_scan",
                        "recommendation": json.dumps(vulnerability),
                        "severity": get_severity(vulnerability["Severity"]),
                        "account_object_id": get_vulnerability_id(image_name, vulnerability),
                        "status": "Open",
                    }
                    recommendations.append(recommendation)
    return image_name


def get_vulnerability_id(image_name: str, vulnerability: dict):
    return f"""{image_name}-{vulnerability.get("PkgID")}-{vulnerability.get("VulnerabilityID")}"""


def archive_existing_recommendations_multi_rule(cloud_account_id, tenant, category, rules):
    update_to_archieve = (
        "update recommendation set status = %s where tenant_id = %s and cloud_account_id = %s and category = %s and"
        " rule_name = ANY(%s) and status not in ('Archive')"
    )
    database.run_query(update_to_archieve, ["Archive", tenant, cloud_account_id, category, rules])


def get_certificate_id(content):
    return f"""{content["name"]}-{content["namespace"]}-{content["metadata"]["managed-by"]}"""


def get_recommendation_settings(account_id, recommendation_name) -> Any:
    try:
        query = """
        SELECT name, value
        FROM cloud_account_attrs
        WHERE cloud_account_id = %s AND name LIKE %s"""
        row = database.run_query(query, [account_id, recommendation_name], cursor_factory=RealDictCursor)
        if row and len(row) > 0:
            return row[0]
    except Exception as e:
        logging.error("error getting recommendation settings", exc_info=e)
    return {}


def handle_certificate_scanner_report(content, tenant, cloud_account_id, account_name: str):
    logging.info("Processing certificate_scanner_report for tenant {}, account id {}".format(tenant, cloud_account_id))
    try:
        row = get_recommendation_settings(cloud_account_id, "certificate_expiry_recommendation")
        if row and "value" in row and row["value"] and isinstance(row["value"], int):
            cert_expiry_validate_duration = row["value"]
        else:
            cert_expiry_validate_duration = 30
        report = json.loads(content["evidence"][0]["data"])[0]
        recommendations = []

        for row in report["data"]:
            content = row["content"][0]

            for cert in content["items"]:
                metadata = cert.get("metadata", {})
                status = cert.get("status", {})

                cert_name = metadata.get("name", "")
                expiry_date = status.get("notAfter", "")

                priority = row["priority"]
                if expiry_date:
                    expiry_datetime = datetime.fromisoformat(expiry_date.replace("Z", "+00:00")).astimezone(
                        timezone.utc
                    )
                    current_datetime = datetime.now().astimezone(timezone.utc)
                    time_difference = expiry_datetime - current_datetime

                    # Check if certificate expiry is within next x days
                    if time_difference.days <= cert_expiry_validate_duration:
                        priority = "HIGH"

                    cert_content = {
                        "name": cert_name,
                        "namespace": metadata.get("namespace", ""),
                        "expiry_date": expiry_datetime.isoformat(sep=" ", timespec="seconds"),
                        "days_until_expiry": time_difference.days,
                        "metadata": get_workload_details(metadata),
                    }

                    recommendation = {
                        "cloud_account_id": cloud_account_id,
                        "tenant_id": tenant,
                        "resource_id": None,
                        "recommendation": cert_content,
                        "recommendation_action": "Modify",
                        "category": Category.CONFIGURATION.value,
                        "rule_name": RuleName.CERTIFICATE_EXPIRY.value,
                        "severity": get_severity(priority),
                        "account_object_id": get_certificate_id(cert_content),
                        "status": "Open",
                    }
                    recommendations.append(recommendation)

        # Sort recommendations by expiry_datetime in ascending order
        recommendations.sort(key=lambda r: r["recommendation"]["expiry_date"])

        on_conflict = (
            "ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id) DO UPDATE SET "
            "recommendation = EXCLUDED.recommendation, status=EXCLUDED.status"
        )

        try:
            archive_existing_with_rule(
                cloud_account_id, tenant, Category.CONFIGURATION.value, RuleName.CERTIFICATE_EXPIRY.value
            )
            database.insert_data("recommendation", recommendations, on_conflict=on_conflict)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(recommendations)}")
    except Exception:
        logging.exception(f"Failed to process report {json.dumps(content)}")


def get_workload_details(metadata):
    meta_details = {"managed-by": metadata.get("labels", {}).get("app.kubernetes.io/managed-by", ""), "owners": []}
    if "ownerReferences" in metadata:
        for owner in metadata["ownerReferences"]:
            owner_ref = {
                "name": owner.get("name", ""),
                "kind": owner.get("kind", ""),
                "api-version": owner.get("apiVersion", ""),
                "uid": owner.get("uid", ""),
            }
            meta_details["owners"].append(owner_ref)

    return meta_details


def handle_helm_chart_upgrade_report(content, tenant, cloud_account_id, account_name: str):
    logging.info("Processing helm_chart_upgrade_report for tenant {}, account id {}".format(tenant, cloud_account_id))
    try:
        report = json.loads(content["evidence"][0]["data"])[0]
        recommendations = []
        for row in report["data"]:
            content = row["content"][0]
            for item in content:
                is_outdated = item.get("outdated", False)
                if is_outdated:
                    recommendation = {
                        "cloud_account_id": cloud_account_id,
                        "tenant_id": tenant,
                        "resource_id": None,
                        "recommendation": json.dumps(item),
                        "recommendation_action": "Modify",
                        "category": Category.INFRA_UPGRADE.value,
                        "rule_name": RuleName.HELM_CHART_UPGRADE.value,
                        "severity": get_severity(row["priority"]),
                        "account_object_id": get_helm_chart_id(item),
                        "status": "Open",
                    }
                    recommendations.append(recommendation)

        on_conflict = (
            "ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id) DO UPDATE SET "
            "recommendation = EXCLUDED.recommendation, status=EXCLUDED.status"
        )

        try:
            archive_existing_with_rule(
                cloud_account_id, tenant, Category.INFRA_UPGRADE.value, RuleName.HELM_CHART_UPGRADE.value
            )
            database.insert_data("recommendation", recommendations, on_conflict=on_conflict)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(recommendations)}")

    except Exception:
        logging.exception(f"Failed to process report {json.dumps(content)}")


def get_helm_chart_id(content):
    return f"""{content["release"]}-{content["chartName"]}-{content["namespace"]}-{content["Installed"]["version"]}"""


def handle_trivy_cis_scan(raw_content: dict, tenant: str, cloud_account_id: str, account_name: str):
    logging.info("Processing trivy_cis_scan report for tenant {} , account id {}".format(tenant, cloud_account_id))
    report = json.loads(raw_content["evidence"][0]["data"])[0]
    recommendations = []
    for r in report["data"]:
        for image in r["content"]:
            results = image["Results"]
            for result in results:
                if "Results" not in result or result["Results"] is None:
                    continue
                for vulnerability in result["Results"]:
                    vulnerability["Id"] = result["ID"]
                    vulnerability["Name"] = result["Name"]
                    vulnerability["Description"] = result["Description"]
                    recommendation = {
                        "cloud_account_id": cloud_account_id,
                        "tenant_id": tenant,
                        "recommendation_action": "Modify",
                        "resource_id": None,
                        "category": "Security",
                        "rule_name": RuleName.TRIVY_CIS_SCAN.value,
                        "recommendation": json.dumps(vulnerability),
                        "severity": get_severity(result["Severity"]),
                        "account_object_id": f"""{result["ID"]}-{vulnerability["Class"]}-{vulnerability["Target"]}""",
                        "status": "Open",
                    }
                    recommendations.append(recommendation)
    handle_notification(
        account_name, cloud_account_id, recommendations, tenant, Category.SECURITY.value, RuleName.TRIVY_CIS_SCAN.value
    )
    archive_existing_with_rule(cloud_account_id, tenant, Category.SECURITY.value, RuleName.TRIVY_CIS_SCAN.value)
    upsert_recommendations(recommendations)


def round_memory(x: Union[float, int]) -> int:
    base = 1024
    if x < 1:
        return int(x * 1000)

    if x < 500:
        return int(x)

    binary_units = ["", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei"]

    x = int(x)
    for i, unit in enumerate(binary_units):
        if x < base ** (i + 1) or i == len(binary_units) - 1 or x / base ** (i + 1) < 10:
            data = x / base**i
            data = math.ceil(data / 10) * 10
            data = data * base**i
            return int(data)
    data = x / 6**i
    # instead of round , use ceil to get the next highest value
    data = math.ceil(data / 10) * 10
    data = data * base**i
    return int(data)


def handle_k8s_helm_compatibility_report(content, tenant, cloud_account_id, account_name: str):
    logging.info("Processing k8s_helm_compatibility for tenant {}, account id {}".format(tenant, cloud_account_id))
    try:
        report = json.loads(content["evidence"][0]["data"])[0]
        recommendations = []
        for row in report["data"]:
            content = row["content"][0]
            for item in content:
                recommendation = {
                    "cloud_account_id": cloud_account_id,
                    "tenant_id": tenant,
                    "resource_id": None,
                    "recommendation": json.dumps(item),
                    "recommendation_action": "Modify",
                    "category": Category.INFRA_UPGRADE.value,
                    "rule_name": RuleName.K8S_HELM_COMPATIBILITY.value,
                    "severity": get_severity(row["priority"]),
                    "account_object_id": get_helm_id(item),
                    "status": "Open",
                }
                recommendations.append(recommendation)

        on_conflict = (
            "ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id) DO UPDATE SET "
            "recommendation = EXCLUDED.recommendation, status=EXCLUDED.status"
        )

        try:
            archive_existing_with_rule(
                cloud_account_id, tenant, Category.INFRA_UPGRADE.value, RuleName.K8S_HELM_COMPATIBILITY.value
            )
            database.insert_data("recommendation", recommendations, on_conflict=on_conflict)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(recommendations)}")
    except Exception:
        logging.exception(f"Failed to process report {json.dumps(content)}")


def get_helm_id(content):
    return f"""{content["release"]}-{content["chart"]}-{content["namespace"]}"""


def store_pod_profile(tenant: str, cloud_account_id: str, evidence, event):
    try:
        for data in evidence:
            if "data" in data and isinstance(data["data"], str):
                data = json.loads(data["data"])
            for item in data:
                if (
                    "additional_info" in item
                    and item.get("additional_info")
                    and item.get("additional_info").get("action") == "pod_profiler"
                ):
                    profile = [item]
                    profile_record = {
                        "tenant_id": tenant,
                        "cloud_account_id": cloud_account_id,
                        "pod_name": event.get("subject_name"),
                        "workload_name": event.get("subject_owner"),
                        "namespace": event.get("subject_namespace"),
                        "profile": json.dumps(profile),
                        "source_id": event.get("finding_id"),
                        "source": "event",
                        "profile_type": item.get("additional_info").get("profile_type"),
                        "profile_duration": item.get("additional_info").get("profile_duration"),
                        "profile_language": item.get("additional_info").get("lang"),
                        "profile_tool": item.get("additional_info").get("profile_tool"),
                    }
                    database.insert_data("application_profile", [profile_record])
    except Exception:
        logging.exception("Failed to insert row")


def parse_json_safe(s):
    try:
        return json.loads(s)
    except (TypeError, json.JSONDecodeError):
        return None


# Extensible Trigger System

# Trigger type constants
RUNBOOK_TRIGGER_ACTION_NAME = "nudgebee_runbook_trigger_enricher"
LLM_TRIGGER_ACTION_NAME = "nubi_enricher"


class TriggerContext:
    """Context object containing all necessary data for trigger processing"""

    def __init__(
        self,
        tenant_id: str,
        cloud_account_id: str,
        evidence: List[Dict[str, Any]],
        finding: Optional[Dict[str, Any]] = None,
    ):
        self.tenant_id = tenant_id
        self.cloud_account_id = cloud_account_id
        self.evidence = evidence
        self.finding = finding


class BaseTriggerHandler(ABC):
    """Abstract base class for all trigger handlers"""

    @abstractmethod
    def can_handle(self, entry: Dict[str, Any]) -> bool:
        """Check if this handler can process the given entry"""
        pass

    @abstractmethod
    def process_entry(self, entry: Dict[str, Any], context: TriggerContext) -> None:
        """Process a single evidence entry"""
        pass

    @abstractmethod
    def get_trigger_type(self) -> str:
        """Return the type of trigger this handler manages"""
        pass


class RunbookTriggerHandler(BaseTriggerHandler):
    """Handler for runbook triggers"""

    def can_handle(self, entry: Dict[str, Any]) -> bool:
        additional_info = entry.get("additional_info", {})
        if not additional_info:
            return False
        return bool(additional_info.get("actual_action_name") == RUNBOOK_TRIGGER_ACTION_NAME)

    def process_entry(self, entry: Dict[str, Any], context: TriggerContext) -> None:
        inner_data = entry
        if not isinstance(entry, dict):
            inner_data = parse_json_safe(entry.get("data"))

        if not isinstance(inner_data, dict):
            return

        logging.info(f"Processing runbook trigger for tenant {context.tenant_id}, account {context.cloud_account_id}")

        runbook_id = inner_data.get("runbook_id")
        if not runbook_id:
            logging.warning(f"Runbook ID is missing in entry: {entry}")
            return

        response = self._execute_runbook_action(context.tenant_id, runbook_id)
        inner_data["execution_info"] = dataclasses.asdict(response)
        entry["data"] = json.dumps(inner_data)

    def get_trigger_type(self) -> str:
        return "runbook"

    def _execute_runbook_action(self, tenant_id: str, runbook_id: str) -> ExecuteRunbookActionSubmitResponse:
        """Execute runbook action"""
        self._validate_inputs(tenant_id, runbook_id)

        url = self._build_runbook_url()
        payload = self._build_payload(tenant_id, runbook_id)

        try:
            response = self._send_runbook_request(url, payload)
        except Exception as e:
            return ExecuteRunbookActionSubmitResponse(error=str(e))

        return self._parse_runbook_response(response)

    def _validate_inputs(self, tenant_id: str, runbook_id: str):
        """Validate runbook inputs"""
        if not tenant_id:
            raise ValueError("runbook: tenant id is required")
        if not runbook_id:
            raise ValueError("runbook: runbook id is required")

    def _build_runbook_url(self) -> str:
        """Build runbook URL"""
        url = Configs.AUTO_PILOT_URL
        if not url.endswith("/"):
            url += "/"
        return url + "runbook/execute"

    def _build_payload(self, tenant_id: str, runbook_id: str) -> Dict[str, Any]:
        """Build runbook request payload"""
        return {
            "session_variables": {
                "user_id": "00000000-0000-0000-0000-000000000000",
                "tenant_id": tenant_id,
            },
            "input": {
                "runbook_execute_request": {
                    "id": runbook_id,
                }
            },
        }

    def _send_runbook_request(self, url: str, payload: Dict[str, Any]):
        """Send runbook request"""
        headers = {"X-ACTION-TOKEN": Configs.ACTION_API_SERVER_TOKEN, "Content-Type": "application/json"}

        try:
            response = requests.post(url, json=payload, headers=headers, timeout=30)
            response.raise_for_status()
        except requests.RequestException as e:
            logging.info("runbook: failed to send request or got error response", exc_info=e)
            raise e

        return response

    def _parse_runbook_response(self, response) -> ExecuteRunbookActionSubmitResponse:
        """Parse runbook response"""
        try:
            resp_json = response.json()
        except Exception as e:
            logging.info("runbook: failed to decode response JSON", exc_info=e)
            raise e

        return ExecuteRunbookActionSubmitResponse(
            status=resp_json.get("status", ""),
            created_tasks=resp_json.get("created_tasks", []),
        )


class LLMTriggerHandler(BaseTriggerHandler):
    """Handler for LLM triggers"""

    def can_handle(self, entry: Dict[str, Any]) -> bool:
        additional_info = entry.get("additional_info", {})
        if not additional_info:
            return False
        return bool(additional_info.get("actual_action_name") == LLM_TRIGGER_ACTION_NAME)

    def process_entry(self, entry: Dict[str, Any], context: TriggerContext) -> None:
        inner_data = entry
        if not isinstance(entry, dict):
            inner_data = parse_json_safe(entry.get("data"))

        logging.info(f"Processing LLM trigger for tenant {context.tenant_id}, account {context.cloud_account_id}")

        query = inner_data.get("data", "")
        user_id = inner_data.get("user_id", "00000000-0000-0000-0000-000000000000")

        finding_id = context.finding.get("id", uuid.uuid4()) if context.finding else uuid.uuid4()
        response = self._execute_llm_action(context.tenant_id, context.cloud_account_id, query, user_id, finding_id)
        inner_data["llm_response"] = dataclasses.asdict(response)
        entry["data"] = json.dumps(inner_data)

    def get_trigger_type(self) -> str:
        return "llm"

    def _execute_llm_action(
        self, tenant_id: str, account_id: str, query: str, user_id: str, finding_id: str
    ) -> LLMActionResponse:
        """Execute LLM action"""
        self._validate_llm_inputs(tenant_id, query)

        url = self._build_llm_url()
        payload = self._build_llm_payload(tenant_id, account_id, query, user_id, finding_id)
        headers = self._build_llm_headers(tenant_id, user_id)

        try:
            response = self._send_llm_request(url, payload, headers)
        except Exception as e:
            return LLMActionResponse(error=str(e))

        return self._parse_llm_response(response)

    def _validate_llm_inputs(self, tenant_id: str, query: str):
        """Validate LLM inputs"""
        if not tenant_id:
            raise ValueError("llm: tenant id is required")
        if not query:
            raise ValueError("llm: query is required")

    def _build_llm_url(self) -> str:
        """Build LLM URL"""
        url = Configs.LLM_SERVER_ENDPOINT
        if not url.endswith("/"):
            url += "/"
        return url + "v1/completions/chat"

    def _build_llm_payload(self, tenant_id: str, account_id: str, query: str, user_id: str, finding_id: str) -> str:
        """Build LLM request payload"""
        payload_dict = {
            "query": query,
            "account_id": account_id,
            "tenant_id": tenant_id,
            "user_id": user_id,
            "async": True,
            "source": "Investigation",
            "session_id": finding_id,
        }
        return json.dumps(payload_dict)

    def _build_llm_headers(self, tenant_id: str, user_id: str) -> Dict[str, str]:
        """Build LLM request headers"""
        return {
            "x-tenant-id": tenant_id,
            "x-user-id": user_id,
            "Content-Type": "application/json",
        }

    def _send_llm_request(self, url: str, payload: str, headers: Dict[str, str]):
        """Send LLM request"""
        try:
            response = requests.post(url, data=payload, headers=headers, timeout=300)
            response.raise_for_status()
        except requests.RequestException as e:
            logging.info("llm: failed to send request or got error response", exc_info=e)
            raise e

        return response

    def _parse_llm_response(self, response) -> LLMActionResponse:
        """Parse LLM response"""
        try:
            resp_json = response.json()
        except Exception as e:
            logging.info("llm: failed to decode response JSON", exc_info=e)
            raise e

        if "data" in resp_json:
            resp_json = resp_json.get("data")

        return LLMActionResponse(
            response=resp_json.get("response", []),
            chain_name=resp_json.get("chain_name", ""),
            conversation_id=resp_json.get("conversation_id"),
            message_id=resp_json.get("message_id"),
            session_id=resp_json.get("session_id"),
        )


class TriggerHandlerFactory:
    """Factory class for managing trigger handlers"""

    def __init__(self):
        self._handlers: List[BaseTriggerHandler] = []
        self._register_default_handlers()

    def _register_default_handlers(self):
        """Register the default handlers"""
        self.register_handler(RunbookTriggerHandler())
        self.register_handler(LLMTriggerHandler())

    def register_handler(self, handler: BaseTriggerHandler):
        """Register a new trigger handler"""
        self._handlers.append(handler)
        logging.info(f"Registered {handler.get_trigger_type()} trigger handler")

    def get_handler_for_entry(self, entry: Dict[str, Any]) -> Optional[BaseTriggerHandler]:
        """Get the appropriate handler for an entry"""
        for handler in self._handlers:
            if handler.can_handle(entry):
                return handler
        return None

    def get_all_handlers(self) -> List[BaseTriggerHandler]:
        """Get all registered handlers"""
        return self._handlers.copy()


# Global factory instance
_trigger_factory = TriggerHandlerFactory()


def handle_triggers(
    tenant_id: str, cloud_account_id: str, evidence: List[Dict[str, Any]], finding: Optional[Dict[str, Any]] = None
):
    """Unified trigger handler using the extensible system"""
    context = TriggerContext(tenant_id, cloud_account_id, evidence, finding)

    for entry in evidence:
        handler = _trigger_factory.get_handler_for_entry(entry)
        if handler:
            try:
                handler.process_entry(entry, context)
            except Exception as e:
                logging.error(f"Error processing {handler.get_trigger_type()} trigger: {e}", exc_info=True)


# Severity escalation functions moved to Go service
# See: api-server/services/event/severity_calculator.go


def _extract_workload_identity(workload: Dict[str, Any]) -> Optional[tuple]:
    """Extract namespace, name, and kind from workload Id."""
    id_obj = workload.get("Id")
    if not isinstance(id_obj, dict):
        return None

    name = id_obj.get("name")
    if not isinstance(name, str) or not name:
        return None

    ns = id_obj.get("namespace")
    if not isinstance(ns, str):
        return None

    kind = id_obj.get("kind")
    if not isinstance(kind, str):
        return None

    return ns, name, kind


def _should_skip_workload(ns: str, name: str, config: Dict[str, str]) -> bool:
    """Check if workload should be skipped based on namespace and name."""
    return not ns or ns == "external" or (ns == config.get("Namespace") and name == config.get("WorkloadName"))


def _extract_upstream_services(workload: Dict[str, Any]) -> set:
    """Extract upstream service IDs from workload."""
    upstream_services = set()
    upstream_links = workload.get("Upstreams")
    if not isinstance(upstream_links, list):
        return upstream_services

    for link in upstream_links:
        if isinstance(link, dict):
            upstream_link_id = link.get("Id")
            if isinstance(upstream_link_id, str):
                upstream_services.add(upstream_link_id)

    return upstream_services


def _find_target_workload_upstreams(raw_data: List[Dict[str, Any]], config: Dict[str, str]) -> set:
    """Find upstream services for the target workload."""
    for workload in raw_data:
        identity = _extract_workload_identity(workload)
        if not identity:
            continue

        ns, name, _ = identity
        if ns == config.get("Namespace") and name == config.get("WorkloadName"):
            return _extract_upstream_services(workload)

    return set()


def _create_insight(ns: str, name: str, reason: str, is_upstream: bool) -> Dict[str, str]:
    """Create insight message for unhealthy workload."""
    if is_upstream:
        message = (
            f"Critical upstream service {name} in {ns} is unhealthy, reason: {reason}."
            f" This may directly impact the current workload."
        )
        severity = "Critical"
    else:
        message = f"Found {name} in {ns} as unhealthy, reason: {reason}"
        severity = "Info"

    return {"message": message, "severity": severity}


def _validate_and_parse_evidence(evidence: Dict[str, Any]) -> List[Dict[str, Any]]:
    """Validate and parse evidence data, return list of workloads."""
    if evidence.get("data") is None:
        return []

    service_map_data = json.loads(evidence.get("data"))
    if "data" not in service_map_data:
        return []

    service_map = service_map_data.get("data")
    if not isinstance(service_map, list):
        logging.warning(f"evidence does not contain a valid 'data' list: {service_map}")
        return []

    return [item for item in service_map if isinstance(item, dict)]


def _process_unhealthy_workload(workload: Dict[str, Any], upstream_services_set: set) -> Optional[Dict[str, str]]:
    """Process a single workload and return insight if unhealthy."""
    identity = _extract_workload_identity(workload)
    if not identity:
        return None

    ns, name, kind = identity

    is_healthy = workload.get("IsHealthy")
    if not isinstance(is_healthy, bool) or is_healthy:
        return None

    reason = workload.get("HealthReason", "unknown")
    if not isinstance(reason, str):
        reason = "unknown"

    expected_upstream_id = f"{ns}:{kind}:{name}"
    is_upstream = len(upstream_services_set) > 0 and expected_upstream_id in upstream_services_set

    return _create_insight(ns, name, reason, is_upstream)


def _get_relevant_workloads(raw_data: List[Dict[str, Any]], config: Dict[str, str]) -> List[Dict[str, Any]]:
    """Filter workloads to only include relevant ones for analysis."""
    relevant_workloads = []
    for workload in raw_data:
        identity = _extract_workload_identity(workload)
        if identity:
            ns, name, _ = identity
            if not _should_skip_workload(ns, name, config):
                relevant_workloads.append(workload)
    return relevant_workloads


def check_neighboring_workload_health(evidence: Dict[str, Any], config: Dict[str, str]) -> List[Dict[str, str]]:
    """Check health of neighboring workloads and generate insights."""
    raw_data = _validate_and_parse_evidence(evidence)
    if not raw_data:
        return []

    upstream_services_set = _find_target_workload_upstreams(raw_data, config)
    relevant_workloads = _get_relevant_workloads(raw_data, config)

    insights = []
    for workload in relevant_workloads:
        insight = _process_unhealthy_workload(workload, upstream_services_set)
        if insight:
            insights.append(insight)

    return insights
