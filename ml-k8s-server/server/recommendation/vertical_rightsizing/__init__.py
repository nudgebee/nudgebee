from typing import List, Optional, Any, Dict, Union
from dataclasses import dataclass
import logging
import traceback
import warnings
from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.services.service import RecommendationService
from datetime import datetime
from collections import defaultdict
import time
import json
import math
from server.utils.utils import get_trace, DatabaseEngine
from sqlalchemy import text
from server.recommendation.vertical_rightsizing.models.result import ResourceType

# Suppress pkg_resources deprecation warning
warnings.filterwarnings("ignore", message="pkg_resources is deprecated", category=UserWarning)

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
    priority: float
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


def get_severity(priority: float) -> str:
    """Convert priority to severity level."""
    if priority >= 0.8:
        return "Critical"
    elif priority >= 0.6:
        return "High"
    elif priority >= 0.4:
        return "Medium"
    else:
        return "Low"


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
    account_id: str, tenant_id: str, recommendations: List[RecommendationData]
) -> None:
    """Archive existing KRR recommendations for only the resources we're updating."""
    ctx_logger = get_contextual_logger(tenant_id, account_id)

    with tracer.start_as_current_span("archive_existing_recommendations") as span:
        try:
            if not recommendations:
                ctx_logger.info("No recommendations to process, skipping archiving")
                return

            engine = DatabaseEngine.get_engine()

            # Build list of account_object_ids that we're going to update
            account_object_ids = list(set([f"{rec.namespace}/{rec.kind}/{rec.name}" for rec in recommendations]))

            span.set_attribute("krr.target_resources", len(account_object_ids))
            ctx_logger.info(f"Archiving existing recommendations for {len(account_object_ids)} resources")

            if account_object_ids:
                # Only archive recommendations for the specific resources we're updating
                placeholders = ", ".join([":id_" + str(i) for i in range(len(account_object_ids))])
                update_query = text(f"""
                    UPDATE recommendation
                    SET status = 'Archive'
                    WHERE tenant_id = :tenant_id
                    AND cloud_account_id = :account_id
                    AND category = 'RightSizing'
                    AND rule_name = 'pod_right_sizing'
                    AND status NOT IN ('Closed', 'InProgress', 'Archive')
                    AND account_object_id IN ({placeholders})
                """)

                params = {"tenant_id": tenant_id, "account_id": account_id}

                # Add the account_object_ids to params
                for i, obj_id in enumerate(account_object_ids):
                    params[f"id_{i}"] = obj_id

                with engine.connect() as conn:
                    result = conn.execute(update_query, params)
                    conn.commit()

                    rows_updated = result.rowcount
                    span.set_attribute("krr.archived_recommendations", rows_updated)
                    ctx_logger.info(f"Archived {rows_updated} existing KRR recommendations for specific resources")

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

            # Archive existing recommendations only for resources we're updating
            # This should only happen when we're actually going to store new recommendations
            archive_existing_krr_recommendations(account_id, tenant_id, recommendations)

            engine = DatabaseEngine.get_engine()

            # Generate recommendations using same logic as collector-server
            recommendations_to_insert = {}

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

            ctx_logger.info(f"Generated {len(recommendations_to_insert)} recommendation records for database insertion")

            # Insert recommendations using same approach as collector-server
            with tracer.start_as_current_span("insert_recommendations") as insert_span:
                if recommendations_to_insert:
                    # Convert to list format for database insertion - same as collector-server
                    recommendations_list = list(recommendations_to_insert.values())

                    # Use individual INSERT statements with ON CONFLICT handling
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
                            status = EXCLUDED.status
                    """)

                    with engine.connect() as conn:
                        for rec in recommendations_list:
                            conn.execute(insert_query, rec)
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
