from typing import List, Optional, Any, Dict, Union
from dataclasses import dataclass
import logging
import traceback
from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.services.service import RecommendationService
from datetime import datetime
from collections import defaultdict
import time
import json
import math
from server.utils.utils import get_trace, DatabaseEngine
from sqlalchemy import text
from server.recommendation.vertical_rightsizing.models.result import ResourceType, scan_severity_to_priority
from server.recommendation.vertical_rightsizing.models.severity import Severity

logger = logging.getLogger("krr")
tracer = get_trace(__name__)

# Default cost constants (same as collector-server)
DEFAULT_COST = {
    "provider": "custom",
    "description": "Default prices based on GCP us-central1",
    "CPU": 0.031611,
    "spotCPU": 0.006655,
    "RAM": 0.004237,
    "spotRAM": 0.000892,
}


@dataclass
class RecommendationData:
    """Individual recommendation data for direct processing."""

    namespace: str
    name: str
    kind: str
    container: str
    priority: int
    content: List[Dict[str, Any]]


@dataclass
class NamespaceProcessingResult:
    namespace: str
    status: str  # "success", "partial", "failed"
    processed_count: int
    failed_count: int
    error_message: Optional[str]
    recommendations_stored: int
    processing_time_ms: float


@dataclass
class ProcessingMetrics:
    total_namespaces: int
    successful_namespaces: List[str]
    failed_namespaces: List[NamespaceProcessingResult]
    total_recommendations: int
    total_processing_time_ms: float
    overall_status: str
    database_stored: bool = False


@dataclass
class RecommendationProcessingResult:
    """Streamlined result object for recommendation processing."""

    tenant_id: str
    account_id: str
    processing_metrics: ProcessingMetrics
    recommendations_generated: int
    database_stored: bool
    score: str
    start_time: datetime
    end_time: datetime
    recommendations: Optional[List[Dict[str, Any]]] = None  # Include actual recommendation data


async def generate_recommendations(
    account_id: str,
    namespace: Optional[str],
    resource_names: Optional[List[str]] = None,
    max_recommendations: Optional[int] = None,
    metrics_provider: Optional[str] = None,
    datadog_api_key: Optional[str] = None,
    datadog_app_key: Optional[str] = None,
    datadog_site: Optional[str] = None,
):
    config = Config(
        namespaces=None if namespace is None else [namespace],
        resources="*",
        selector=None,
        max_workers=4,  # Reduced from 8 to limit concurrent memory usage
        cpu_min_value=10,
        memory_min_value=100,
        strategy="nudgebee",
        other_args={},
        resource_names=resource_names,
        max_recommendations_per_batch=max_recommendations,  # Use parameter value
        metrics_fetch_timeout=45,  # Increased timeout for better reliability
    )
    runner = RecommendationService(
        account_id,
        config,
        metrics_provider=metrics_provider,
        datadog_api_key=datadog_api_key,
        datadog_app_key=datadog_app_key,
        datadog_site=datadog_site,
    )
    return await runner.run()


def get_resource_minimal(resource: ResourceType) -> float:
    """Get minimal resource value following KRR conventions."""
    if resource == ResourceType.CPU:
        return 1 / 1000 * 10  # 10m CPU minimum
    elif resource == ResourceType.Memory:
        return 1024**2 * 100  # 100Mi memory minimum
    else:
        return 0


def round_resource_value(value, resource: ResourceType) -> Optional[float]:
    """Round resource values following KRR service conventions."""
    if value is None:
        return value
    if isinstance(value, str):
        try:
            value = float(value)
        except (ValueError, TypeError):
            return None
    if math.isnan(value):
        return None

    prec_power: Union[float, int]
    if resource == ResourceType.CPU:
        # NOTE: We use 10**3 as the minimal value for CPU is 10m
        prec_power = 10**3
    elif resource == ResourceType.Memory:
        # NOTE: We use 1/(1024**2) as the minimal value for memory is 1Mi
        prec_power = 1 / (1024**2)
    else:
        # NOTE: We use 1 as the minimal value for other resources
        prec_power = 1

    rounded = math.ceil(value * prec_power) / prec_power
    minimal = get_resource_minimal(resource)

    return max(rounded, minimal)


# A GOOD scan means the recommendation matches what is already allocated, so
# there is nothing to act on. Derived from the enum rather than hardcoded so it
# tracks scan_severity_to_priority.
GOOD_PRIORITY = scan_severity_to_priority(Severity.GOOD)


def is_no_change_workload(container_priorities: List[int]) -> bool:
    """True iff every container of a workload scanned GOOD.

    Evaluated per workload, never per container: one container already being
    right-sized says nothing about its siblings. A workload with no scanned
    container is not a no-change workload — there is simply nothing to judge.
    """
    return bool(container_priorities) and all(priority == GOOD_PRIORITY for priority in container_priorities)


def get_severity(priority: int) -> str:
    """Map a scan priority to a recommendation severity.

    priority is the 0-4 integer from scan_severity_to_priority (CRITICAL=4,
    WARNING=3, OK=2, GOOD=1, UNKNOWN=0), NOT a normalized 0-1 score. Every
    returned value must exist in recommendation_severity_type, which the
    severity column references via recommendation_severity_fkey.

    UNKNOWN outranks GOOD deliberately: a scan we could not compute still
    warrants a look, whereas a GOOD one is already right-sized.

    Thresholds are inequalities, not equalities, so a value off the expected
    scale degrades to the nearest higher severity instead of falling through to
    the bottom. Silently bottoming out is how the original 0-1 misreading hid.
    """
    if priority >= 4:
        return "Critical"
    elif priority >= 3:
        return "High"
    elif priority >= 2:
        return "Medium"
    elif priority >= 1:
        return "Info"
    else:
        return "Low"


# Mirrors the severity_weight CASE that the recommendation_groupings_v2 views
# apply, so "worst" here means the same thing as the UI's "Most severe" sort.
SEVERITY_WEIGHT = {"Critical": 10, "High": 8, "Medium": 5, "Low": 2, "Info": 1}


def worst_priority(container_priorities: List[int]) -> int:
    """The container priority that ranks highest in severity for the workload.

    Deliberately not max() over the raw priorities: priority is not ordered by
    severity. UNKNOWN (0) maps to Low, which outranks GOOD (1) -> Info, so a
    workload mixing a GOOD container with one we could not compute has to
    surface as Low. Ranking by the severity weight rather than a second copy of
    the priority order keeps this from drifting out of sync with get_severity.
    """
    return max(container_priorities, key=lambda priority: SEVERITY_WEIGHT[get_severity(priority)])


def _has_allocated_request(entry: Any) -> bool:
    """True iff this KRR content entry carries a real allocated request.

    None (unset), a missing key, "?" (KRR's NaN placeholder) and 0 all count as
    NOT set — matching the truthiness test calculate_container_savings uses,
    which keeps the invariant "fully unset => estimated_savings == 0".
    """
    if not isinstance(entry, dict):
        return False
    allocated = entry.get("allocated")
    if not isinstance(allocated, dict):
        return False
    request = allocated.get("request")
    if isinstance(request, bool):
        return False
    return isinstance(request, (int, float)) and request > 0


def classify_pod_right_sizing_category(merged_content: Any) -> str:
    """Category for a pod_right_sizing row, from its merged workload payload.

    merged_content is the workload-level JSONB shape {container: [entry, ...]}.
    A workload where no container has any cpu/memory request set is a
    best-practice finding ("set requests"), not a cost one, so it lands under
    Configuration; any set request keeps the row under RightSizing. Keep in
    sync with the twin in collector-server/k8s-collector event_handler.py and
    with the backfill migration's SQL predicate.
    """
    if not isinstance(merged_content, dict):
        return "RightSizing"
    saw_entry = False
    for entries in merged_content.values():
        if not isinstance(entries, list):
            continue
        for entry in entries:
            saw_entry = True
            if _has_allocated_request(entry):
                return "RightSizing"
    return "Configuration" if saw_entry else "RightSizing"


def finalize_workload_rows(
    recommendations_to_insert: Dict[Any, Dict[str, Any]],
    priorities_by_resource: Dict[Any, List[int]],
) -> int:
    """Rate and classify each merged workload row in place, dropping no-change ones.

    Both passes run on the MERGED payload rather than per container, because a
    per-container pass lets the last container processed decide the whole
    workload's category and severity. Severity takes the worst container by
    severity rank, which is not the same as the highest priority number.

    Dropped workloads must also leave the archive keep-set the caller builds from
    this dict, otherwise the rows already stored for them stay Open forever.

    Returns the number of workloads dropped.
    """
    for resource_id, row in recommendations_to_insert.items():
        row["category"] = classify_pod_right_sizing_category(json.loads(row["recommendation"]))
        row["severity"] = get_severity(worst_priority(priorities_by_resource[resource_id]))

    no_change_resource_ids = [
        resource_id
        for resource_id in recommendations_to_insert
        if is_no_change_workload(priorities_by_resource[resource_id])
    ]
    for resource_id in no_change_resource_ids:
        del recommendations_to_insert[resource_id]

    return len(no_change_resource_ids)


def calculate_container_savings(
    content: List[Dict[str, Any]], cpu_cost_per_hour: float, memory_cost_per_hour: float
) -> float:
    """Calculate estimated monthly savings for a container based on CPU and memory recommendations.

    Args:
        content: List of resource recommendations (CPU and memory)
        cpu_cost_per_hour: Cost per CPU core per hour
        memory_cost_per_hour: Cost per GB of memory per hour

    Returns:
        Estimated monthly savings in dollars
    """
    saving = 0.0

    for rec in content:
        # Skip if insufficient data
        if "info" in rec and (rec["info"] == "Not enough data" or rec["info"] == "No data"):
            continue

        # Handle missing recommended values
        if rec.get("recommended", {}).get("request") == "?":
            continue

        resource_type = rec.get("resource")
        allocated_request = rec.get("allocated", {}).get("request")
        recommended_request = rec.get("recommended", {}).get("request")

        if not allocated_request or not recommended_request:
            continue

        if resource_type == "cpu":
            # CPU costs are per core
            original_hourly_cost = allocated_request * cpu_cost_per_hour
            new_hourly_cost = recommended_request * cpu_cost_per_hour
            saving += (original_hourly_cost - new_hourly_cost) * 24 * 30  # Monthly savings

        elif resource_type == "memory":
            # Memory costs are per GB (values in bytes, convert to GB)
            original_gb = allocated_request / (1024 * 1024 * 1024)
            recommended_gb = recommended_request / (1024 * 1024 * 1024)
            original_hourly_cost = original_gb * memory_cost_per_hour
            new_hourly_cost = recommended_gb * memory_cost_per_hour
            saving += (original_hourly_cost - new_hourly_cost) * 24 * 30  # Monthly savings

    return saving


def archive_existing_krr_recommendations(
    account_id: str,
    tenant_id: str,
    recommendations: List[RecommendationData],
    namespace_filter: Optional[str] = None,
    resource_names_filter: Optional[List[str]] = None,
    kept_categories_by_object_id: Optional[Dict[str, str]] = None,
    kept_object_ids: Optional[List[str]] = None,
) -> None:
    """Archive stale KRR recommendations that no longer correspond to a live workload.

    A scan reports recommendations only for workloads that still exist in the cluster.
    A workload that has been deleted therefore simply disappears from the scan, so the
    correct way to retire its recommendation is to archive the in-scope Open recs whose
    account_object_id is NOT in the current scan results (then the caller re-inserts the
    present ones as Open). Archiving the *scanned* set instead — the previous behaviour —
    left deleted workloads orphaned as Open forever.

    Scope rules ensure we only reconcile what the scan was exhaustive over:
      - Full account run (no filters): reconcile across the whole account.
      - Namespace-only run: reconcile within that namespace.
      - resource_names run: the scan only covers specific requested resources, so absence
        does NOT mean deletion — fall back to refreshing just the targeted resources.

    kept_categories_by_object_id maps each account_object_id about to be (re)written to
    its intended category. pod_right_sizing rows live under RightSizing or Configuration
    (requests-unset workloads), and category is part of the upsert conflict key — so when
    a workload's category flips between scans the insert misses the old row. A second
    sweep archives kept workloads' rows stored under the other category, in both flip
    directions.

    kept_object_ids narrows the keep-set to the workloads actually about to be written,
    which is not every workload the scan reported: a scan can legitimately cover a
    workload and still decline to emit a row for it (nothing to change). Those have to
    be archived like a deleted workload, or their stored rows stay Open forever. It
    stays distinct from the scanned set, which still defines what the scan covered —
    conflating the two would either strand no-change rows or let a partial scan
    mass-archive. Defaults to every scanned workload.
    """
    if not tenant_id or not account_id:
        raise ValueError("archive_existing_krr_recommendations: tenant_id and account_id are required")

    ctx_logger = get_contextual_logger(tenant_id, account_id)

    with tracer.start_as_current_span("archive_existing_recommendations") as span:
        try:
            # Empty scan → skip. An empty result is far more likely a transient scan/metrics
            # failure than "every workload in scope was deleted"; a NOT-IN archive with an
            # empty keep-set would mass-archive every Open rec in scope. The rare genuinely-
            # empty case self-heals on the next non-empty run.
            if not recommendations:
                ctx_logger.info("No recommendations to process, skipping archiving")
                return

            engine = DatabaseEngine.get_engine()

            # Two distinct sets: what the scan covered, and what stays Open. They differ
            # whenever the scan covered a workload but emitted no row for it.
            # Bind as a single array param (= ANY) rather than dynamic :keep_N placeholders:
            # keeps the SQL text static (one cached plan), avoids the parameter-count ceiling
            # on large full-account scans, and matches the = ANY(:namespaces) pattern used
            # elsewhere in this file.
            scanned_object_ids = list(set([f"{rec.namespace}/{rec.kind}/{rec.name}" for rec in recommendations]))
            keep_ids = scanned_object_ids if kept_object_ids is None else list(set(kept_object_ids))

            params: Dict[str, Any] = {
                "tenant_id": tenant_id,
                "account_id": account_id,
            }

            if resource_names_filter:
                # Non-exhaustive scan: only specific resources were requested, so a workload's
                # absence from the scan tells us nothing about whether it still exists. Restrict
                # to refreshing just the targeted resources (legacy archive-then-reinsert).
                # Keyed on the scanned set, not the keep-set: a targeted workload that turned
                # out to need no change must still have its stored row archived here, since the
                # insert will not re-open it.
                scope_clause = "AND account_object_id = ANY(:scanned_object_ids ::text[])"
                scope_label = "resource_names"
                params["scanned_object_ids"] = scanned_object_ids
            else:
                # Exhaustive over its scope → archive anything in scope that vanished or
                # needs no change. The keep-set can legitimately be empty here (every
                # workload already right-sized), hence the explicit cast: an untyped empty
                # array gives Postgres nothing to infer the element type from.
                scope_clause = "AND NOT (account_object_id = ANY(:kept_object_ids ::text[]))"
                scope_label = "account"
                params["kept_object_ids"] = keep_ids
                if namespace_filter:
                    scope_clause += " AND account_object_id LIKE :ns_prefix"
                    params["ns_prefix"] = f"{namespace_filter}/%"
                    scope_label = "namespace"

            span.set_attribute("krr.kept_resources", len(keep_ids))
            span.set_attribute("krr.scanned_resources", len(scanned_object_ids))
            span.set_attribute("krr.archive_scope", scope_label)

            update_query = text(f"""
                UPDATE recommendation
                SET status = 'Archive'
                WHERE tenant_id = :tenant_id
                AND cloud_account_id = :account_id
                AND category IN ('RightSizing', 'Configuration')
                AND rule_name = 'pod_right_sizing'
                AND status NOT IN ('Closed', 'InProgress', 'Archive')
                {scope_clause}
            """)

            with engine.connect() as conn:
                result = conn.execute(update_query, params)
                conn.commit()

                rows_updated = result.rowcount
                span.set_attribute("krr.archived_recommendations", rows_updated)
                ctx_logger.info(
                    f"Archived {rows_updated} stale KRR recommendations (scope={scope_label}, kept={len(keep_ids)})"
                )

            # Kept workloads whose stored category differs from the intended one:
            # the row about to be written has a different conflict key, so archive
            # the stale-category twin. Keys on explicit object ids from this scan,
            # so it is inherently scope-safe and never touches the row being
            # written (its category matches).
            if kept_categories_by_object_id:
                sweep_query = text("""
                    UPDATE recommendation r
                    SET status = 'Archive'
                    FROM unnest(:kept_ids ::text[], :kept_categories ::text[]) AS k(object_id, category)
                    WHERE r.tenant_id = :tenant_id
                    AND r.cloud_account_id = :account_id
                    AND r.rule_name = 'pod_right_sizing'
                    AND r.category IN ('RightSizing', 'Configuration')
                    AND r.status NOT IN ('Closed', 'InProgress', 'Archive')
                    AND r.account_object_id = k.object_id
                    AND r.category <> k.category
                """)
                sweep_params = {
                    "tenant_id": tenant_id,
                    "account_id": account_id,
                    "kept_ids": list(kept_categories_by_object_id.keys()),
                    "kept_categories": list(kept_categories_by_object_id.values()),
                }
                with engine.connect() as conn:
                    result = conn.execute(sweep_query, sweep_params)
                    conn.commit()
                    if result.rowcount:
                        span.set_attribute("krr.category_flipped_recommendations", result.rowcount)
                        ctx_logger.info(f"Archived {result.rowcount} KRR recommendations under a stale category")

        except Exception as e:
            span.set_attribute("krr.archive_error", str(e))
            ctx_logger.error(f"Failed to archive existing recommendations: {e}")
            raise


def get_existing_resources(
    account_id: str, tenant_id: str, recommendations: List[RecommendationData]
) -> Dict[str, Any]:
    """Get existing resource mappings using WORKLOAD cloud_resource_id - prevents duplicate recommendations."""
    ctx_logger = get_contextual_logger(tenant_id, account_id)

    with tracer.start_as_current_span("get_existing_resources") as span:
        try:
            engine = DatabaseEngine.get_engine()

            # Build workload identifiers for filtering
            workload_identifiers = set()
            target_namespaces = set()
            for r in recommendations:
                workload_identifiers.add((r.namespace, r.kind, r.name))
                target_namespaces.add(r.namespace)

            span.set_attribute("krr.workload_count", len(workload_identifiers))
            span.set_attribute("krr.target_namespaces", len(target_namespaces))

            if not workload_identifiers:
                return {}

            # NEW QUERY: Join k8s_workloads with cloud_resourses to get workload-level data
            # Then join with k8s_pods to get container info and node details for cost calculation
            query = text("""
                SELECT
                    kw.cloud_resource_id AS id,
                    kw.tenant_id AS tenant,
                    kw.kind AS controller_kind,
                    kw.name AS controller,
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
                    kw.namespace AS namespace,
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
                    kw.cloud_account_id AS account,
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
                FROM k8s_workloads kw
                WHERE kw.cloud_account_id = :account_id
                AND kw.tenant_id = :tenant_id
                AND kw.is_active = true
                AND kw.namespace = ANY(:namespaces)
            """)

            params = {
                "account_id": account_id,
                "tenant_id": tenant_id,
                "namespaces": list(target_namespaces),
            }

            resource_list = []
            with engine.connect() as conn:
                result = conn.execute(query, params)
                resource_list = result.mappings().fetchall()

            # Build resource map using workload identifiers
            resource_map = {}
            for resource in resource_list:
                try:
                    # Handle both JSON string and already-parsed JSONB from SQLAlchemy
                    # psycopg2 automatically parses JSONB columns into Python types (list/dict)
                    containers_data = resource["containers"]

                    # Explicitly handle different data types
                    if containers_data is None:
                        containers = []
                    elif isinstance(containers_data, list):
                        # Already parsed by SQLAlchemy/psycopg2 - use directly
                        containers = containers_data
                    elif isinstance(containers_data, str):
                        # JSON string - parse it
                        containers = json.loads(containers_data) if containers_data else []
                    else:
                        # Unexpected type - log and skip
                        ctx_logger.warning(
                            f"Unexpected container data type for workload {resource.get('id', 'unknown')}: "
                            f"{type(containers_data).__name__}"
                        )
                        continue

                    if not containers:
                        ctx_logger.debug(
                            f"No containers found for workload {resource['controller']}/{resource['namespace']}"
                        )
                        continue

                    for container in containers:
                        # Key format: kind/namespace/name/container_name
                        key = f"{resource['controller_kind']}/{resource['namespace']}/"
                        key += f"{resource['controller']}/{container['name']}"
                        resource_map[key] = resource
                except (KeyError, TypeError, IndexError) as e:
                    ctx_logger.warning(
                        f"Failed to parse container data for workload {resource.get('id', 'unknown')}: {e}"
                    )
                    continue

            span.set_attribute("krr.resource_map_size", len(resource_map))
            ctx_logger.info(
                f"Built resource map with {len(resource_map)} entries from {len(resource_list)} "
                f"workloads (using WORKLOAD cloud_resource_id)"
            )

            return resource_map

        except Exception as e:
            span.set_attribute("krr.get_resources_error", str(e))
            ctx_logger.error(f"Failed to get existing resources: {e}")
            raise


def store_krr_recommendations_to_db(
    recommendations: List[RecommendationData],
    account_id: str,
    tenant_id: str,
    score: str,
    namespace_filter: Optional[str] = None,
    resource_names_filter: Optional[List[str]] = None,
    max_recommendations: Optional[int] = None,
) -> None:
    """Store KRR recommendations to database following the same pattern as collector-server."""
    ctx_logger = get_contextual_logger(tenant_id, account_id)

    with tracer.start_as_current_span("store_krr_recommendations_to_db") as main_span:
        main_span.set_attributes(
            {
                "krr.recommendations_count": len(recommendations),
                "krr.account_id": account_id,
                "krr.tenant_id": tenant_id,
                "krr.score": score,
            }
        )

        try:
            # Get existing resource mappings - same as collector-server
            resource_map = get_existing_resources(account_id, tenant_id, recommendations)

            engine = DatabaseEngine.get_engine()

            # Generate recommendations using same logic as collector-server. Built
            # before the archive pass so it can reconcile stored categories against
            # the ones about to be written.
            recommendations_to_insert = {}
            priorities_by_resource: Dict[Any, List[int]] = defaultdict(list)

            for rec in recommendations:
                resource_key = f"{rec.kind}/{rec.namespace}/{rec.name}/{rec.container}"

                if resource_key not in resource_map:
                    ctx_logger.info(f"Resource not found in cloud_resourses: {resource_key}")
                    continue

                resource_id_row = resource_map[resource_key]
                resource_id = resource_id_row["id"]

                # Build recommendation content - same format as collector-server
                recommendation_content = {rec.container: rec.content}

                # Calculate estimated savings using actual cost data
                # Extract cost information from resource_id_row (same logic as event_handler.py)
                cpu_cost_per_hour = DEFAULT_COST.get("CPU")
                memory_cost_per_hour = DEFAULT_COST.get("RAM")

                # Check for spot pricing
                if (
                    resource_id_row["spot_pricing"]
                    and resource_id_row["az"]
                    and resource_id_row["node_type"]
                    and str(resource_id_row["node_type"]).lower() == "spot"
                ):
                    spot_pricing = resource_id_row["spot_pricing"]
                    az = resource_id_row["az"]
                    # Parse spot_pricing if it's a JSON string
                    try:
                        if isinstance(spot_pricing, str):
                            spot_pricing = json.loads(spot_pricing)

                        if isinstance(spot_pricing, list):
                            for spot in spot_pricing:
                                if spot.get("az") == az:
                                    cost = spot.get("price")
                                    cpu = resource_id_row["cpu_virtual"]
                                    memory = resource_id_row["memory_gb"]
                                    if cost and cpu and memory:
                                        cpu_cost_per_hour = (cost / ((cpu * 88) + (memory * 12)) * 88) / cpu
                                        memory_cost_per_hour = (cost / ((cpu * 88) + (memory * 12)) * 12) / memory
                                    break
                    except (json.JSONDecodeError, KeyError, TypeError, ValueError) as e:
                        ctx_logger.warning(f"Failed to parse spot pricing for {resource_key}: {e}")

                # Check for regular pricing
                elif resource_id_row["cpu_cost_per_unit"] and resource_id_row["memory_cost_per_gb"]:
                    cpu_cost_per_hour = resource_id_row["cpu_cost_per_unit"]
                    memory_cost_per_hour = resource_id_row["memory_cost_per_gb"]

                # Calculate savings using the helper function
                estimated_savings = calculate_container_savings(rec.content, cpu_cost_per_hour, memory_cost_per_hour)

                # Create recommendation record same as collector-server
                recommendation = {
                    "estimated_savings": estimated_savings,
                    "cloud_account_id": account_id,
                    "tenant_id": tenant_id,
                    "resource_id": resource_id,  # Use actual resource_id from cloud_resourses
                    "recommendation_action": "Modify",
                    "category": "RightSizing",
                    "rule_name": "pod_right_sizing",
                    "recommendation": json.dumps(recommendation_content),
                    "severity": get_severity(rec.priority),
                    "account_object_id": f"{rec.namespace}/{rec.kind}/{rec.name}",
                    "status": "Open",  # All new recommendations start as "Open"
                }

                priorities_by_resource[resource_id].append(rec.priority)

                if resource_id in recommendations_to_insert:
                    # Combine with existing recommendation for same resource
                    existing_rec = json.loads(recommendations_to_insert[resource_id]["recommendation"])
                    existing_rec[rec.container] = rec.content
                    recommendation["recommendation"] = json.dumps(existing_rec)
                    old_savings = recommendations_to_insert[resource_id]["estimated_savings"]
                    recommendation["estimated_savings"] = old_savings + estimated_savings
                    recommendations_to_insert[resource_id] = recommendation
                else:
                    recommendations_to_insert[resource_id] = recommendation

            no_change_count = finalize_workload_rows(recommendations_to_insert, priorities_by_resource)
            main_span.set_attribute("krr.no_change_workloads_skipped", no_change_count)
            if no_change_count:
                ctx_logger.info(f"Skipped {no_change_count} workloads that need no resource change")

            ctx_logger.info(f"Generated {len(recommendations_to_insert)} recommendation records for database insertion")

            # Reconcile: archive stale recs for workloads that vanished from this scan,
            # scoped to what the scan was exhaustive over (account / namespace / specific
            # resources), plus kept workloads whose stored category no longer matches the
            # one about to be written. The insert below re-opens the workloads still present.
            archive_existing_krr_recommendations(
                account_id,
                tenant_id,
                recommendations,
                namespace_filter,
                resource_names_filter,
                kept_categories_by_object_id={
                    row["account_object_id"]: row["category"] for row in recommendations_to_insert.values()
                },
                kept_object_ids=[row["account_object_id"] for row in recommendations_to_insert.values()],
            )

            # Insert recommendations using same approach as collector-server
            with tracer.start_as_current_span("insert_recommendations") as insert_span:
                if recommendations_to_insert:
                    # Convert to list format for database insertion - same as collector-server
                    recommendations_list = list(recommendations_to_insert.values())

                    insert_query = text("""
                        INSERT INTO recommendation (
                            cloud_account_id, tenant_id, resource_id, recommendation,
                            recommendation_action, category, rule_name, severity,
                            estimated_savings, status, account_object_id
                        ) VALUES (
                            :cloud_account_id, :tenant_id, :resource_id, :recommendation,
                            :recommendation_action, :category, :rule_name, :severity,
                            :estimated_savings, :status, :account_object_id
                        )
                        ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id)
                        DO UPDATE SET
                            recommendation = EXCLUDED.recommendation,
                            estimated_savings = EXCLUDED.estimated_savings,
                            status = EXCLUDED.status,
                            severity = EXCLUDED.severity
                    """)

                    # Batch execute: pass entire list so SQLAlchemy sends one
                    # multi-row statement instead of N round-trips.
                    with engine.connect() as conn:
                        conn.execute(insert_query, recommendations_list)
                        conn.commit()

                    insert_span.set_attribute("krr.inserted_recommendations", len(recommendations_list))
                    ctx_logger.info(f"Successfully inserted {len(recommendations_list)} recommendation records")
                else:
                    ctx_logger.warning("No recommendations found with matching resources in cloud_resourses table")

            # Store score following collector-server pattern
            with tracer.start_as_current_span("store_score") as score_span:
                score_record = {
                    "score": float(score),
                    "source": "krr",
                    "cloud_account_id": account_id,
                    "tenant": tenant_id,
                }

                score_query = text("""
                    INSERT INTO cloud_account_score (score, source, cloud_account_id, tenant)
                    VALUES (:score, :source, :cloud_account_id, :tenant)
                    ON CONFLICT (cloud_account_id, tenant, source)
                    DO UPDATE SET score = EXCLUDED.score
                """)

                with engine.connect() as conn:
                    conn.execute(score_query, score_record)
                    conn.commit()

                score_span.set_attribute("krr.score_stored", score)
                ctx_logger.info(f"Successfully stored KRR score: {score}")

            main_span.set_attribute("krr.storage_status", "success")
            ctx_logger.info("Successfully stored all KRR recommendations to database")

        except Exception as e:
            main_span.set_attribute("krr.storage_status", "failed")
            main_span.set_attribute("krr.error", str(e))
            ctx_logger.error(f"Failed to store KRR recommendations to database: {e}")
            raise


def get_contextual_logger(tenant_id: str, account_id: str, namespace: Optional[str] = None):
    """Create a logger with tenant and account context."""
    extra = {"tenant_id": tenant_id, "account_id": account_id}
    if namespace:
        extra["namespace"] = namespace

    return logging.LoggerAdapter(logger, extra)


def build_recommendation_data_from_scans(rightsizing_recommendations, ctx_logger) -> List[RecommendationData]:
    """Build RecommendationData objects from KRR scan results."""
    recommendations = []

    with tracer.start_as_current_span("build_recommendation_data") as build_span:
        for scan in rightsizing_recommendations.scans:
            try:
                content = []
                for resource in rightsizing_recommendations.resources:
                    add_info = scan.recommended.config.get(resource, {})

                    # Get raw values
                    raw_recommended_request = scan.recommended.requests[resource].value
                    raw_recommended_limit = scan.recommended.limits[resource].value

                    # Apply proper rounding following KRR conventions
                    recommended_request = round_resource_value(raw_recommended_request, resource)
                    recommended_limit = round_resource_value(raw_recommended_limit, resource)

                    temp = {
                        "resource": resource,
                        "allocated": {
                            "request": scan.object.allocations.requests[resource],
                            "limit": scan.object.allocations.limits[resource],
                        },
                        "recommended": {
                            "request": recommended_request,
                            "limit": recommended_limit,
                        },
                        "priority": {
                            "request": scan.recommended.requests[resource].priority,
                            "limit": scan.recommended.limits[resource].priority,
                        },
                        "info": scan.recommended.info.get(resource),
                        "metric": scan.metrics.get(resource).model_dump() if scan.metrics.get(resource) else {},
                        "description": rightsizing_recommendations.description,
                        "strategy": (
                            rightsizing_recommendations.strategy.model_dump()
                            if rightsizing_recommendations.strategy
                            else None
                        ),
                        "add_info": add_info,
                    }
                    content.append(temp)

                recommendation = RecommendationData(
                    namespace=scan.object.namespace,
                    name=scan.object.name,
                    kind=scan.object.kind,
                    container=scan.object.container,
                    priority=scan.priority,
                    content=content,
                )
                recommendations.append(recommendation)

            except Exception as scan_error:
                obj = scan.object
                scan_id = f"{obj.kind}/{obj.namespace}/{obj.name}/{obj.container}"
                ctx_logger.error(
                    f"Failed to process scan {scan_id}: {scan_error}\n{traceback.format_exc()}",
                )
                continue

        build_span.set_attribute("krr.recommendations_built", len(recommendations))

    return recommendations


def process_recommendations_by_namespace(
    recommendations: List[RecommendationData], tenant_id: str, account_id: str, batch_by_namespace: bool, ctx_logger
) -> List[NamespaceProcessingResult]:
    """Group recommendations by namespace and process them in batches."""
    processing_results = []
    namespace_groups = defaultdict(list)

    # Group recommendations by namespace
    for rec in recommendations:
        namespace_groups[rec.namespace].append(rec)

    ctx_logger.info(
        "Processing namespace batches",
        extra={"total_namespaces": len(namespace_groups), "batch_by_namespace": batch_by_namespace},
    )

    # Process each namespace with tracing
    with tracer.start_as_current_span("process_namespace_batches") as batch_span:
        batch_span.set_attribute("krr.total_namespaces", len(namespace_groups))
        batch_span.set_attribute("krr.batch_by_namespace", batch_by_namespace)

        if batch_by_namespace:
            for ns, ns_recommendations in namespace_groups.items():
                result = process_namespace_batch(ns, ns_recommendations, tenant_id, account_id)
                processing_results.append(result)
        else:
            # Process all as single batch
            all_recommendations = [rec for recs in namespace_groups.values() for rec in recs]
            result = process_namespace_batch("ALL", all_recommendations, tenant_id, account_id)
            processing_results.append(result)

    return processing_results


def calculate_processing_metrics(
    processing_results: List[NamespaceProcessingResult],
    namespace_groups: Dict[str, List[RecommendationData]],
    overall_start_time: float,
    main_span,
    rightsizing_recommendations,
    ctx_logger,
) -> ProcessingMetrics:
    """Calculate processing metrics from namespace processing results."""
    # Calculate metrics
    total_processing_time = (time.time() - overall_start_time) * 1000
    successful_namespaces = [r.namespace for r in processing_results if r.status == "success"]
    failed_results = [r for r in processing_results if r.status == "failed"]
    total_recommendations = sum(r.recommendations_stored for r in processing_results)

    # Determine overall status
    if len(failed_results) == 0:
        overall_status = "success"
    elif len(successful_namespaces) > 0:
        overall_status = "partial"
    else:
        overall_status = "failed"

    # Add final span attributes
    main_span.set_attributes(
        {
            "krr.overall_status": overall_status,
            "krr.total_namespaces": len(namespace_groups),
            "krr.successful_namespaces": len(successful_namespaces),
            "krr.failed_namespaces": len(failed_results),
            "krr.total_recommendations": total_recommendations,
            "krr.total_processing_time_ms": total_processing_time,
            "krr.score": str(rightsizing_recommendations.score),
        }
    )

    # Log final metrics
    ctx_logger.info(
        "KRR processing completed",
        extra={
            "overall_status": overall_status,
            "successful_namespaces": len(successful_namespaces),
            "failed_namespaces": len(failed_results),
            "total_recommendations": total_recommendations,
            "total_processing_time_ms": total_processing_time,
        },
    )

    # Create processing metrics
    return ProcessingMetrics(
        total_namespaces=len(namespace_groups),
        successful_namespaces=successful_namespaces,
        failed_namespaces=failed_results,
        total_recommendations=total_recommendations,
        total_processing_time_ms=total_processing_time,
        overall_status=overall_status,
        database_stored=False,  # Will be updated by caller
    )


def create_processing_result(
    tenant_id: str,
    account_id: str,
    recommendations: List[RecommendationData],
    metrics: ProcessingMetrics,
    rightsizing_recommendations,
    start_time: datetime,
    end_time: datetime,
) -> RecommendationProcessingResult:
    """Create the final RecommendationProcessingResult object."""
    # Prepare recommendations data for response (convert to serializable format)
    recommendations_data = []
    for rec in recommendations:
        rec_data = {
            "namespace": rec.namespace,
            "name": rec.name,
            "kind": rec.kind,
            "container": rec.container,
            "priority": rec.priority,
            "content": rec.content,
        }
        recommendations_data.append(rec_data)

    # Create streamlined result object
    return RecommendationProcessingResult(
        tenant_id=tenant_id,
        account_id=account_id,
        processing_metrics=metrics,
        recommendations_generated=len(recommendations),
        database_stored=False,  # Will be updated by caller
        score=str(rightsizing_recommendations.score),
        start_time=start_time,
        end_time=end_time,
        recommendations=recommendations_data,  # Include actual recommendation data
    )


def handle_database_storage(
    persist_recommendation: bool,
    recommendations: List[RecommendationData],
    account_id: str,
    tenant_id: str,
    rightsizing_recommendations,
    namespace: Optional[str],
    resource_names: Optional[List[str]],
    max_recommendations: Optional[int],
    result: RecommendationProcessingResult,
    metrics: ProcessingMetrics,
    ctx_logger,
) -> None:
    """Handle database storage with tracing."""
    ctx_logger.info(f"handle_database_storage called with persist_recommendation={persist_recommendation}")
    if persist_recommendation:
        with tracer.start_as_current_span("store_to_database") as db_span:
            try:
                # Store recommendations to database
                store_krr_recommendations_to_db(
                    recommendations,
                    account_id,
                    tenant_id,
                    str(rightsizing_recommendations.score),
                    namespace,
                    resource_names,
                    max_recommendations,
                )
                ctx_logger.info("Database storage completed")
                result.database_stored = True
                metrics.database_stored = True
                db_span.set_attribute("krr.db_storage_status", "success")
            except Exception as db_error:
                db_span.set_attribute("krr.db_storage_status", "failed")
                db_span.set_attribute("krr.error", str(db_error))
                ctx_logger.error("Failed to store to database", extra={"error": str(db_error)})
                result.database_stored = False
                metrics.database_stored = False


def process_namespace_batch(
    namespace: str, namespace_recommendations: List[RecommendationData], tenant_id: str, account_id: str
) -> NamespaceProcessingResult:
    """Process recommendations for a single namespace."""
    ctx_logger = get_contextual_logger(tenant_id, account_id, namespace)
    start_time = time.time()

    with tracer.start_as_current_span(
        "process_namespace_batch",
        attributes={
            "krr.namespace": namespace,
            "krr.tenant_id": tenant_id,
            "krr.account_id": account_id,
            "krr.recommendation_count": len(namespace_recommendations),
        },
    ) as span:
        try:
            ctx_logger.info(
                "Starting namespace processing",
                extra={"namespace": namespace, "recommendation_count": len(namespace_recommendations)},
            )

            processed_count = 0
            failed_count = 0
            recommendations_stored = 0

            # Process each recommendation in the namespace
            for recommendation in namespace_recommendations:
                with tracer.start_as_current_span(
                    "process_individual_recommendation",
                    attributes={
                        "krr.kind": recommendation.kind,
                        "krr.name": recommendation.name,
                        "krr.container": recommendation.container,
                        "krr.priority": recommendation.priority,
                    },
                ) as rec_span:
                    try:
                        # Validate recommendation data
                        if not recommendation.content or len(recommendation.content) == 0:
                            rec_span.set_attribute("krr.skip_reason", "empty_content")
                            ctx_logger.warning(
                                "Empty content for recommendation",
                                extra={
                                    "kind": recommendation.kind,
                                    "name": recommendation.name,
                                    "container": recommendation.container,
                                },
                            )
                            failed_count += 1
                            continue

                        # Check for insufficient data
                        has_insufficient_data = any(
                            "info" in rec and (rec["info"] == "Not enough data" or rec["info"] == "No data")
                            for rec in recommendation.content
                        )

                        if has_insufficient_data:
                            rec_span.set_attribute("krr.skip_reason", "insufficient_data")
                            ctx_logger.info(
                                "Insufficient data for recommendation",
                                extra={
                                    "kind": recommendation.kind,
                                    "name": recommendation.name,
                                    "container": recommendation.container,
                                },
                            )
                            failed_count += 1
                            continue

                        # Individual recommendation processing completed
                        # Database storage happens at higher level in main function

                        rec_span.set_attribute("krr.status", "processed")
                        processed_count += 1
                        recommendations_stored += 1

                    except Exception as rec_error:
                        rec_span.set_attribute("krr.status", "failed")
                        rec_span.set_attribute("krr.error", str(rec_error))
                        ctx_logger.error(
                            "Failed to process individual recommendation",
                            extra={
                                "kind": recommendation.kind,
                                "name": recommendation.name,
                                "container": recommendation.container,
                                "error": str(rec_error),
                            },
                        )
                        failed_count += 1

            processing_time = (time.time() - start_time) * 1000  # Convert to milliseconds

            # Determine status
            if failed_count == 0:
                status = "success"
            elif processed_count > 0:
                status = "partial"
            else:
                status = "failed"

            # Add span attributes for final metrics
            span.set_attributes(
                {
                    "krr.status": status,
                    "krr.processed_count": processed_count,
                    "krr.failed_count": failed_count,
                    "krr.recommendations_stored": recommendations_stored,
                    "krr.processing_time_ms": processing_time,
                }
            )

            ctx_logger.info(
                "Namespace processing completed",
                extra={
                    "status": status,
                    "processed_count": processed_count,
                    "failed_count": failed_count,
                    "recommendations_stored": recommendations_stored,
                    "processing_time_ms": processing_time,
                },
            )

            return NamespaceProcessingResult(
                namespace=namespace,
                status=status,
                processed_count=processed_count,
                failed_count=failed_count,
                error_message=None,
                recommendations_stored=recommendations_stored,
                processing_time_ms=processing_time,
            )

        except Exception as e:
            processing_time = (time.time() - start_time) * 1000
            error_msg = str(e)

            span.set_attributes(
                {"krr.status": "failed", "krr.error": error_msg, "krr.processing_time_ms": processing_time}
            )

            ctx_logger.error(
                "Namespace processing failed completely",
                extra={"error": error_msg, "processing_time_ms": processing_time},
            )

            return NamespaceProcessingResult(
                namespace=namespace,
                status="failed",
                processed_count=0,
                failed_count=len(namespace_recommendations),
                error_message=error_msg,
                recommendations_stored=0,
                processing_time_ms=processing_time,
            )


async def generate_and_process_recommendation(
    tenant_id: str,
    account_id: str,
    namespace: Optional[str] = None,
    resource_names: Optional[List[str]] = None,
    persist_recommendation: bool = False,
    batch_by_namespace: bool = True,
    max_recommendations: Optional[int] = None,
    metrics_provider: Optional[str] = None,
    datadog_api_key: Optional[str] = None,
    datadog_app_key: Optional[str] = None,
    datadog_site: Optional[str] = None,
) -> RecommendationProcessingResult:
    """Generate and process KRR recommendations with improved modularity."""
    ctx_logger = get_contextual_logger(tenant_id, account_id, namespace)
    ctx_logger.info(f"generate_and_process_recommendation called with persist_recommendation={persist_recommendation}")
    overall_start_time = time.time()

    with tracer.start_as_current_span(
        "generate_and_process_recommendation",
        attributes={
            "krr.tenant_id": tenant_id,
            "krr.account_id": account_id,
            "krr.namespace_filter": namespace or "all",
            "krr.resource_names_count": len(resource_names) if resource_names else 0,
            "krr.persist_recommendation": persist_recommendation,
            "krr.batch_by_namespace": batch_by_namespace,
        },
    ) as main_span:
        ctx_logger.info(
            "Starting KRR recommendation generation",
            extra={
                "namespace_filter": namespace,
                "resource_names_count": len(resource_names) if resource_names else 0,
                "persist_recommendation": persist_recommendation,
                "batch_by_namespace": batch_by_namespace,
            },
        )

        start_time = datetime.now()

        # Generate recommendations with tracing
        with tracer.start_as_current_span("generate_recommendations") as gen_span:
            try:
                rightsizing_recommendations = await generate_recommendations(
                    account_id,
                    namespace,
                    resource_names,
                    max_recommendations=max_recommendations,
                    metrics_provider=metrics_provider,
                    datadog_api_key=datadog_api_key,
                    datadog_app_key=datadog_app_key,
                    datadog_site=datadog_site,
                )
                gen_span.set_attribute("krr.recommendations_generated", len(rightsizing_recommendations.scans))
            except Exception as e:
                gen_span.set_attribute("krr.error", str(e))
                ctx_logger.error("Failed to generate rightsizing recommendations", extra={"error": str(e)})
                raise

        end_time = datetime.now()
        generation_time_ms = (end_time - start_time).total_seconds() * 1000

        main_span.set_attribute("krr.generation_time_ms", generation_time_ms)
        main_span.set_attribute("krr.scan_count", len(rightsizing_recommendations.scans))

        ctx_logger.info(
            "Rightsizing recommendations generated",
            extra={"scan_count": len(rightsizing_recommendations.scans), "generation_time_ms": generation_time_ms},
        )

        # Build recommendation data from scans
        recommendations = build_recommendation_data_from_scans(rightsizing_recommendations, ctx_logger)

        # Process recommendations by namespace
        processing_results = process_recommendations_by_namespace(
            recommendations, tenant_id, account_id, batch_by_namespace, ctx_logger
        )

        # Calculate processing metrics
        namespace_groups = {rec.namespace: [] for rec in recommendations}
        for rec in recommendations:
            namespace_groups[rec.namespace].append(rec)

        metrics = calculate_processing_metrics(
            processing_results, namespace_groups, overall_start_time, main_span, rightsizing_recommendations, ctx_logger
        )

        # Create final result object
        result = create_processing_result(
            tenant_id, account_id, recommendations, metrics, rightsizing_recommendations, start_time, end_time
        )

        # Handle database storage
        handle_database_storage(
            persist_recommendation,
            recommendations,
            account_id,
            tenant_id,
            rightsizing_recommendations,
            namespace,
            resource_names,
            max_recommendations,
            result,
            metrics,
            ctx_logger,
        )

        return result
