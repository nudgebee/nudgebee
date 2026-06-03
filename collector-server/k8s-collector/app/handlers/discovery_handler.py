import json
import logging
import uuid
from dataclasses import asdict
from datetime import datetime
from typing import Dict, Any, List

from apis.model.namespace_details import NamespaceDetails
from apis.model.node_details import NodeDetails
from apis.model.pod_details import PodDetails
from apis.model.workload_details import WorkloadDetails
from config import Configs
from db import database, clickhouse
from db.redis_client import acquire_discovery_lock, record_cleanup_done, should_skip_cleanup
from handlers.alert_rules_handler import handle_alert_rules
from handlers.kg_update import publish_kg_update
from psycopg2 import extras
from utils.datetime_utils import utc_from_epoch_millis

# Resource version condition: update only if incoming version >= existing version.
# Used as a WHERE clause on DO UPDATE so the condition is evaluated once per row,
# not once per column. If false, the row is not updated at all.


def _version_where(table_name: str) -> str:
    """Build a WHERE clause for ON CONFLICT DO UPDATE that skips stale updates."""
    return (
        f"(EXCLUDED.meta->>'resource_version' IS NOT NULL AND {table_name}.meta->>'resource_version' IS NOT NULL AND "
        f" (EXCLUDED.meta->>'resource_version')::bigint >= ({table_name}.meta->>'resource_version')::bigint) "
        f"OR (EXCLUDED.meta->>'resource_version' IS NOT NULL AND {table_name}.meta->>'resource_version' IS NULL) "
        f"OR (EXCLUDED.meta->>'resource_version' IS NULL AND {table_name}.meta->>'resource_version' IS NULL)"
    )


resources_on_conflict = (
    "ON CONFLICT(account,external_resource_id) DO UPDATE SET "
    "last_seen = EXCLUDED.last_seen, "
    "meta = EXCLUDED.meta, "
    "is_active = EXCLUDED.is_active, "
    "tags = EXCLUDED.tags, "
    "first_seen = EXCLUDED.first_seen, "
    "status = EXCLUDED.status, "
    "resourse_id = EXCLUDED.resourse_id "
    f"WHERE {_version_where('cloud_resourses')}"
)

pods_on_conflict = (
    "ON CONFLICT(cloud_account_id,tenant_id,cloud_resource_id) DO UPDATE SET "
    "last_seen = EXCLUDED.last_seen, "
    "meta = EXCLUDED.meta, "
    "is_active = EXCLUDED.is_active, "
    "labels = EXCLUDED.labels, "
    "restart_count = EXCLUDED.restart_count, "
    "node_name = EXCLUDED.node_name, "
    "creation_time = EXCLUDED.creation_time, "
    "workload_type = EXCLUDED.workload_type, "
    "workload_name = EXCLUDED.workload_name, "
    "status = EXCLUDED.status, "
    "namespace = EXCLUDED.namespace "
    f"WHERE {_version_where('k8s_pods')}"
)

workload_on_conflict = (
    "ON CONFLICT(cloud_account_id,tenant_id,cloud_resource_id) DO UPDATE SET "
    "last_seen = EXCLUDED.last_seen, "
    "meta = EXCLUDED.meta, "
    "is_active = EXCLUDED.is_active, "
    "labels = EXCLUDED.labels, "
    "total_pods = EXCLUDED.total_pods, "
    "ready_pods = EXCLUDED.ready_pods, "
    "creation_time = EXCLUDED.creation_time "
    f"WHERE {_version_where('k8s_workloads')}"
)

nodes_on_conflict = (
    "ON CONFLICT(cloud_account_id,tenant_id,cloud_resource_id) DO UPDATE SET "
    "labels = EXCLUDED.labels, "
    "meta = EXCLUDED.meta, "
    "node_flavor = EXCLUDED.node_flavor, "
    "node_region = EXCLUDED.node_region, "
    "node_type = EXCLUDED.node_type, "
    "node_zone = EXCLUDED.node_zone, "
    "node_creation_time = EXCLUDED.node_creation_time, "
    "is_active = EXCLUDED.is_active, "
    "cost = EXCLUDED.cost "
    f"WHERE {_version_where('k8s_nodes')}"
)

namespace_on_conflict = (
    "ON CONFLICT(tenant_id,name,cloud_account_id) DO UPDATE set "
    "is_active = EXCLUDED.is_active, "
    "workload_count = EXCLUDED.workload_count, "
    "pod_count = EXCLUDED.pod_count, "
    "creation_time = EXCLUDED.creation_time "
)

metrics_on_conflict = "ON CONFLICT(metric,cloud_resource_id,timestamp) DO UPDATE SET value = (EXCLUDED.value)"


def _safe_insert_cloud_resources(resources, on_conflict):
    """Insert cloud_resourses with fallback for constraint violations.

    cloud_resourses has three constraints:
    1. PK (id) - primary key
    2. (account, external_resource_id) - handled by ON CONFLICT
    3. (account, resourse_id, type, region, service_name) - secondary unique

    When PK or the secondary constraint fires, we fall back to row-by-row upsert.
    """
    try:
        database.insert_data("cloud_resourses", resources, on_conflict=on_conflict)
    except Exception as e:
        err_str = str(e)
        if "cloud_resourses_pkey" in err_str or "cloud_resourses_account_resourse_service_type_region_key" in err_str:
            constraint = "PK" if "cloud_resourses_pkey" in err_str else "secondary"
            logging.warning(
                "%s constraint hit on cloud_resourses, falling back to row-by-row upsert (%d resources)",
                constraint,
                len(resources),
            )
            for resource in resources:
                try:
                    database.run_query(
                        "DELETE FROM cloud_resourses WHERE account = %s AND resourse_id = %s "
                        "AND type = %s AND region = %s AND service_name = %s "
                        "AND external_resource_id != %s",
                        [
                            resource["account"],
                            resource["resourse_id"],
                            resource["type"],
                            resource["region"],
                            resource["service_name"],
                            resource["external_resource_id"],
                        ],
                    )
                    database.insert_data("cloud_resourses", [resource], on_conflict=on_conflict)
                except Exception:
                    logging.exception("Failed to upsert cloud_resourses resource %s", resource.get("id"))
        else:
            raise


def _deduplicate_workloads_by_identity(workloads):
    """Deduplicate workloads by (cloud_account_id, namespace, name, kind), keeping last occurrence."""
    unique = {}
    for w in workloads:
        key = (w.cloud_account_id, w.namespace, w.name, w.kind)
        unique[key] = w
    return list(unique.values())


def _clean_stale_workloads(cloud_account_id, tenant_id, workloads):
    """Delete stale rows that would conflict on secondary unique constraint
    (cloud_account_id, namespace, name, kind) but have a different cloud_resource_id.

    Uses a single batch query instead of per-workload DELETE.
    """
    if not workloads:
        return

    # Build a VALUES list of (namespace, name, kind, cloud_resource_id) tuples
    # and delete any row matching identity but with a different cloud_resource_id.
    values_params = []
    placeholders = []
    for w in workloads:
        placeholders.append("(%s, %s, %s, %s)")
        values_params.extend([w.namespace, w.name, w.kind, str(w.cloud_resource_id)])

    query = (
        "DELETE FROM k8s_workloads kw "
        "USING (VALUES " + ", ".join(placeholders) + ") AS v(namespace, name, kind, resource_id) "
        "WHERE kw.cloud_account_id = %s AND kw.tenant_id = %s "
        "AND kw.namespace = v.namespace AND kw.name = v.name AND kw.kind = v.kind "
        "AND kw.cloud_resource_id != v.resource_id::uuid"
    )
    params = values_params + [cloud_account_id, tenant_id]
    database.run_query(query, params)


def generate_uuid_from_string(my_string: str) -> str:
    namespace = uuid.NAMESPACE_URL
    my_uuid = uuid.uuid5(namespace, my_string)
    return str(my_uuid)


def run_async_discovery_handler(content) -> None:
    data = content
    if isinstance(content, str):
        data = json.loads(content)

    # Validate required fields
    required_fields = ["type", "data", "tenant", "cloud_account_id"]
    for field in required_fields:
        if field not in data:
            logging.error(f"Missing required field '{field}' in discovery data")
            return

    # Serialize processing per cloud_account_id to prevent PostgreSQL deadlocks
    # on concurrent INSERT ... ON CONFLICT into cloud_resourses for the same account.
    # Uses Redis distributed lock (works across pods); falls back to threading.Lock.
    with acquire_discovery_lock(data["cloud_account_id"], Configs):
        _process_discovery(data)


def _process_discovery(data) -> None:
    """Process discovery data. Must be called under per-account lock."""
    data_type = data["type"].lower()
    full_load = data.get("full_load", False)
    batch_id = data.get("batch_id")
    batch_sequence = data.get("batch_sequence", 1)  # Current batch number
    total_batches = data.get("total_batches", 1)  # Total expected batches

    # Determine if this is a genuine batch operation or single resource update
    batch_keys = ["batch_id", "batch_sequence", "total_batches", "is_first_batch", "is_last_batch"]
    has_batch_metadata = any(key in data for key in batch_keys)

    logging.debug(
        f"Discovery operation - type: {data_type}, resources: {len(data['data'])}, "
        f"batch_metadata: {has_batch_metadata}, full_load: {full_load}"
    )

    # For backward compatibility and proper batch handling:
    # - If batch metadata is provided, respect the is_last_batch flag
    # - If no batch metadata and not full_load, treat as incremental update (no cleanup)
    # - If full_load is true, allow cleanup even without batch metadata
    if has_batch_metadata or full_load:
        is_last_batch = data.get("is_last_batch", batch_sequence == total_batches)
        is_first_batch = data.get("is_first_batch", batch_sequence == 1)
        logging.debug(
            f"BATCH OPERATION - first: {is_first_batch}, last: {is_last_batch}, "
            f"seq: {batch_sequence}/{total_batches}, id: {batch_id}"
        )
    else:
        # Incremental update - no cleanup operations
        is_last_batch = False
        is_first_batch = False
        logging.debug("INCREMENTAL UPDATE - cleanup operations disabled")
    # Extract metadata for resource counts to distinguish "no changes" vs "no resources"
    metadata = data.get("metadata", {})
    if len(data["data"]) > 1:
        logging.debug(f"""Got discovery data for cloud account{data["cloud_account_id"]} ,
            type {data_type}, size {len(data["data"])}, batch {batch_sequence}/{total_batches}, batch_id {batch_id}""")

    # Incremental (single) updates don't need active_resources tracking — it's only
    # consumed by cleanup on is_last_batch. Skipping avoids unnecessary DB writes.
    is_batch_operation = has_batch_metadata or full_load

    if data_type == "service":
        run_service_discovery(
            data["data"],
            data["tenant"],
            data["cloud_account_id"],
            full_load,
            is_last_batch,
            is_first_batch,
            metadata,
            is_batch_operation,
        )
    elif data_type == "node":
        run_node_discovery(
            data["data"],
            data["tenant"],
            data["cloud_account_id"],
            is_last_batch,
            is_first_batch,
            metadata,
            is_batch_operation,
        )
    elif data_type == "job":
        run_job_discovery(
            data["data"],
            data["tenant"],
            data["cloud_account_id"],
            is_last_batch,
            is_first_batch,
            metadata,
            is_batch_operation=is_batch_operation,
        )
    elif data_type == "status":
        run_status_update(data["data"], data["tenant"], data["cloud_account_id"])
    elif data_type == "resolved_services":
        run_resolved_services(data["data"], data["tenant"], data["cloud_account_id"])
    elif data_type == "resolved_node_issue":
        run_resolved_node_services(data["data"], data["tenant"], data["cloud_account_id"])
    elif data_type == "resolved_namespace":
        run_resolved_namespace(data["data"], data["tenant"], data["cloud_account_id"])
    elif data_type == "namespace":
        run_namespace_discovery(
            data["data"],
            data["tenant"],
            data["cloud_account_id"],
            full_load,
            is_last_batch,
            is_first_batch,
            metadata,
            is_batch_operation,
        )
    elif data_type == "alert_rules":
        handle_alert_rules(data["data"], data["tenant"], data["cloud_account_id"])

    # Notify KG system that discovery data has been updated for this tenant
    publish_kg_update(data["tenant"])


def run_service_discovery(
    data: List[Dict[str, Any]],
    tenant: str,
    cloud_account_id: str,
    full_load: bool = False,
    is_last_batch: bool = True,
    is_first_batch: bool = True,
    metadata: Dict[str, Any] = None,
    is_batch_operation: bool = True,
):
    resources = []
    pods = []
    workloads = []
    deleted_resources = {}
    seen_ids = set()
    for k8s_data in data:
        if k8s_data.get("type") and k8s_data.get("type").lower() == "replicaset":
            continue
        service_key = k8s_data["service_key"]
        _id = generate_uuid_from_string(service_key + cloud_account_id)

        if _id in seen_ids:
            logging.warning(f"Duplicate ID detected in batch: {_id} for service_key: {service_key}")
            continue
        seen_ids.add(_id)

        if k8s_data["deleted"]:
            deleted_resources[_id] = service_key
            continue
        process_service_discovery(_id, cloud_account_id, k8s_data, pods, resources, service_key, tenant, workloads)

    # Handle active resources tracking for batched discovery only.
    # Incremental updates (is_first_batch=False, is_last_batch=False) contain only CHANGED
    # resources, not all resources. Clearing active_resources and running cleanup on incremental
    # updates would wipe all resources not in the small delta, causing mass deactivation.
    # Only clear on first batch (start of full sync) and cleanup on last batch (end of full sync).
    should_cleanup = is_last_batch

    # Filter resources by type before clearing/storing
    pod_resources = [r for r in resources if r.get("type") == "Pod"]
    workload_resources = [r for r in resources if r.get("type") != "Pod" and r.get("type") not in ["node", "Namespace"]]

    if is_batch_operation:
        if is_first_batch:
            clear_active_resources(cloud_account_id, tenant, "Pod")
            clear_active_resources(cloud_account_id, tenant, "workload")

        # Store active resources from this batch for all batches in a sequence
        store_active_resources(cloud_account_id, tenant, pod_resources, "Pod")
        store_active_resources(cloud_account_id, tenant, workload_resources, "workload")

    logging.debug(
        f"Service discovery batch {cloud_account_id}/{tenant}: {len(pod_resources)} pods, "
        f"{len(workload_resources)} workloads, {len(deleted_resources)} deleted (first={is_first_batch},"
        f" last={is_last_batch})"
    )

    update_resources(
        cloud_account_id,
        deleted_resources,
        pods,
        resources,
        tenant,
        workloads,
        full_load,
        should_cleanup,
        is_first_batch,
        metadata,
    )


def process_service_discovery(_id, cloud_account_id, k8s_data, pods, resources, service_key, tenant, workloads):
    cloud_resource = {
        "id": _id,
        "region": "global",
        "arn": "k8s://" + k8s_data["service_key"],
        "tenant": tenant,
        "first_seen": k8s_data["creation_time"],
        "last_seen": utc_from_epoch_millis(k8s_data["update_time"]).isoformat(),
        "resourse_id": service_key,
        "name": k8s_data["name"],
        "account": cloud_account_id,
        "cloud_provider": "K8s",
        "type": k8s_data["type"],
        "service_name": "kubernetes",
        "external_resource_id": _id,
    }
    meta = {
        "config": k8s_data["config"],
        "controllerKind": k8s_data["type"],
        "namespace": k8s_data["namespace"],
        "status": k8s_data.get("status", "UNKNOWN"),
        "restart_count": k8s_data.get("restart_count"),
        "total_pods": k8s_data.get("total_pods"),
        "ready_pods": k8s_data.get("ready_pods"),
        "is_helm_release": k8s_data.get("is_helm_release"),
        "node": k8s_data.get("node_name"),
        "status_info": k8s_data.get("status_dict"),
        "resource_version": k8s_data.get("resource_version"),
    }
    config = k8s_data.get("config")
    if config and "owner" in config and config["owner"] and len(config["owner"]) > 0 and config["owner"][0] is not None:
        owner = config["owner"][0]
        meta["controller"] = owner.get("name")
        meta["controllerKind"] = owner.get("kind")
    cloud_resource["meta"] = json.dumps(meta)
    cloud_resource["tags"] = json.dumps((k8s_data.get("config", {}).get("labels", {}),))
    is_deleted = not k8s_data.get("deleted", False)
    if k8s_data.get("config", {}).get("metadata", {}).get("deletionTimestamp"):
        is_deleted = False
    if (
        k8s_data["type"] == "StatefulSet"
        and isinstance(k8s_data.get("status"), dict)
        and k8s_data.get("status", {}).get("replicas") == 0
    ):
        is_deleted = False
    if (
        k8s_data["status"]
        and k8s_data["type"] == "Pod"
        and k8s_data["status"].lower() in ["succeeded", "failed", "completed"]
    ):
        is_deleted = False
    common_details = {
        "tenant_id": tenant,
        "cloud_account_id": cloud_account_id,
        "cloud_resource_id": _id,
        "external_id": service_key,
        "namespace": k8s_data["namespace"],
        "is_active": is_deleted,
        "creation_time": k8s_data["creation_time"],
        "labels": k8s_data.get("config", {}).get("labels", {}),
        "meta": meta,
        "name": k8s_data.get("name", ""),
        "last_seen": utc_from_epoch_millis(k8s_data.get("update_time") or 0).isoformat(),
    }
    if k8s_data["type"] == "Pod":
        pod_details = {
            **common_details,
            "node_name": k8s_data.get("node_name", ""),
            "restart_count": k8s_data.get("restart_count", 0),
            "status": k8s_data.get("status") or "UNKNOWN",
        }
        config_owner = k8s_data.get("config", {}).get("owner", [])
        if config_owner and len(config_owner) > 0 and config_owner[0] is not None:
            pod_details["workload_name"] = config_owner[0].get("name", "")
            pod_details["workload_type"] = config_owner[0].get("kind", "")
        pods.append(PodDetails(**pod_details))
    else:
        workload_details = {
            **common_details,
            "total_pods": k8s_data.get("total_pods", 0),
            "ready_pods": k8s_data.get("ready_pods", 0),
            "kind": k8s_data.get("type", ""),
        }
        workloads.append(WorkloadDetails(**workload_details))
    cloud_resource["is_active"] = is_deleted
    cloud_resource["status"] = "Active" if is_deleted else "Inactive"
    resources.append(cloud_resource)


def update_resources(
    cloud_account_id,
    deleted_resources,
    pods,
    resources,
    tenant,
    workloads,
    full_load: bool = False,
    should_cleanup: bool = True,
    is_first_batch: bool = True,
    metadata: Dict[str, Any] = None,
):
    try:
        logging.debug("inserting cloud_resourses for account_id {}".format(cloud_account_id))
        # Deduplicate resources by ID to avoid duplicate key violations within the same batch
        deduplicated_resources = {}
        for resource in resources:
            resource_id = resource.get("id")
            if resource_id:
                # Keep the last occurrence (most recent) of each ID
                deduplicated_resources[resource_id] = resource

        deduped_list = list(deduplicated_resources.values())
        if len(deduped_list) < len(resources):
            logging.warning(
                f"Deduplicated {len(resources) - len(deduped_list)} duplicate resources for {cloud_account_id}. "
                f"Original: {len(resources)}, After dedup: {len(deduped_list)}"
            )

        _safe_insert_cloud_resources(deduped_list, resources_on_conflict)

        # Handle immediate deletions from current batch
        process_deleted_resources(cloud_account_id, deleted_resources, tenant)

        # Handle cleanup to mark deleted resources as inactive
        # Triggers on: explicit last batch OR metadata with resource counts (periodic discovery)
        if should_cleanup:
            # Skip cleanup if one already ran recently for this account (queue backlog dedup)
            skip_pod = should_skip_cleanup(cloud_account_id, "Pod", Configs)
            skip_wl = should_skip_cleanup(cloud_account_id, "workload", Configs)
            if skip_pod and skip_wl:
                logging.info(f"Skipping all cleanup for {cloud_account_id}/{tenant} — ran recently")
            else:
                logging.info(
                    f"Processing final service cleanup for {cloud_account_id}/{tenant} "
                    f"- marking absent resources as deleted"
                )
                metadata = metadata or {}
                total_pods = metadata.get("total_pods") if isinstance(metadata.get("total_pods"), int) else None
                total_workloads = (
                    metadata.get("total_workloads") if isinstance(metadata.get("total_workloads"), int) else None
                )

                if metadata:
                    logging.info(
                        f"Using metadata for cleanup - total_pods: {total_pods}, total_workloads: {total_workloads}"
                    )

                if not skip_pod:
                    handle_active_resources_deletion(cloud_account_id, tenant, "Pod", total_pods)
                    record_cleanup_done(cloud_account_id, "Pod", Configs)
                if not skip_wl:
                    handle_active_resources_deletion(cloud_account_id, tenant, "workload", total_workloads)
                    record_cleanup_done(cloud_account_id, "workload", Configs)

        # Deduplicate pods and workloads by cloud_resource_id before insertion
        unique_pods = {}
        for pod in pods:
            pod_id = pod.cloud_resource_id
            if pod_id not in unique_pods:
                unique_pods[pod_id] = pod

        unique_workloads = {}
        for workload in workloads:
            wl_id = workload.cloud_resource_id
            if wl_id not in unique_workloads:
                unique_workloads[wl_id] = workload

        if len(unique_pods) < len(pods):
            logging.warning(
                f"Deduplicated {len(pods) - len(unique_pods)} duplicate pods for {cloud_account_id}. "
                f"Original: {len(pods)}, After dedup: {len(unique_pods)}"
            )
        if len(unique_workloads) < len(workloads):
            logging.warning(
                f"Deduplicated {len(workloads) - len(unique_workloads)} duplicate workloads for {cloud_account_id}. "
                f"Original: {len(workloads)}, After dedup: {len(unique_workloads)}"
            )

        process_services_updates(cloud_account_id, list(unique_pods.values()), list(unique_workloads.values()))
    except Exception:
        logging.exception("Failed to insert resources for account %s (%d resources)", cloud_account_id, len(resources))


def clear_active_resources(cloud_account_id, tenant, resource_type):
    """
    Clear existing active resources for a specific tenant/account/type.
    Called on first batch of full_load.
    """
    try:
        logging.warning(f"CLEARING ACTIVE RESOURCES for {cloud_account_id}/{tenant}/{resource_type}")
        database.run_query(
            "DELETE FROM active_resources " "WHERE cloud_account_id = %s AND tenant_id = %s AND resource_type = %s",
            [cloud_account_id, tenant, resource_type],
        )
        logging.info(f"Successfully cleared active resources for {cloud_account_id}/{tenant}/{resource_type}")
    except Exception:
        logging.exception(f"Failed to clear active resources for {cloud_account_id}/{tenant}/{resource_type}")


def store_active_resources(cloud_account_id, tenant, resources, resource_type):
    """
    Store active resources from current batch into permanent table.
    """
    try:
        # Prepare data for insertion
        active_resources_data = []
        for resource in resources:
            active_resources_data.append(
                {
                    "cloud_account_id": cloud_account_id,
                    "tenant_id": tenant,
                    "external_resource_id": resource["external_resource_id"],
                    "resourse_id": resource["resourse_id"],
                    "resource_type": resource_type,
                }
            )

        # Use ON CONFLICT to handle duplicates (in case of retries)
        if active_resources_data:
            on_conflict = (
                "ON CONFLICT(cloud_account_id, tenant_id, external_resource_id, resource_type) DO UPDATE SET "
                "updated_at = CURRENT_TIMESTAMP"
            )
            database.insert_data("active_resources", active_resources_data, on_conflict=on_conflict)

        logging.debug(f"Stored {len(active_resources_data)} active resources for {resource_type}")

    except Exception:
        logging.exception(f"Failed to store active resources for {resource_type}")


def handle_active_resources_deletion(cloud_account_id, tenant, resource_type, total_resources_count=None):  # noqa: C901
    """
    Handle deleted resources detection using permanent active_resources table.
    Called on last batch of discovery operations.

    Args:
        total_resources_count: If provided, represents the total count of resources from discovery.
                              If 0, means no resources exist and allows complete cleanup.
                              If None, means incremental update and skips complete cleanup.
    """
    try:
        logging.warning(
            f"PROCESSING RESOURCE DELETIONS for {cloud_account_id}/{tenant}/{resource_type}, "
            f"total_count: {total_resources_count}"
        )

        # Get all active resources from the permanent table
        active_resources_query = """
            SELECT DISTINCT external_resource_id, resourse_id
            FROM active_resources
            WHERE cloud_account_id = %s
            AND tenant_id = %s
            AND resource_type = %s
        """

        active_resources = database.run_query(active_resources_query, [cloud_account_id, tenant, resource_type])

        if len(active_resources) > 0:
            # Create IN clauses for resources that are active
            external_ids = [r[0] for r in active_resources]
            resource_ids = [r[1] for r in active_resources]

            # Update cloud_resources based on resource type
            if resource_type == "Pod":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND external_resource_id != ALL(%s) "
                    "AND (is_active = true OR is_active IS NULL) AND type = 'Pod'",
                    [cloud_account_id, external_ids],
                )
                # Only update k8s_pods for Pod resources
                # Cast text array to uuid[] so the index on cloud_resource_id (uuid) is used
                database.run_query(
                    "UPDATE k8s_pods "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s "
                    "AND cloud_resource_id != ALL(%s::uuid[]) AND is_active = true",
                    [cloud_account_id, tenant, external_ids],
                )
            elif resource_type == "workload":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND external_resource_id != ALL(%s) "
                    "AND (is_active = true OR is_active IS NULL) "
                    "AND type NOT IN ('Pod', 'node', 'Namespace', 'External')",
                    [cloud_account_id, external_ids],
                )
                database.run_query(
                    "UPDATE k8s_workloads "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s "
                    "AND cloud_resource_id != ALL(%s::uuid[]) AND is_active = true",
                    [cloud_account_id, tenant, external_ids],
                )
            elif resource_type == "Job":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND external_resource_id != ALL(%s) "
                    "AND (is_active = true OR is_active IS NULL) AND type = 'Job'",
                    [cloud_account_id, external_ids],
                )
                database.run_query(
                    "UPDATE k8s_workloads "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s "
                    "AND cloud_resource_id != ALL(%s::uuid[]) AND is_active = true AND kind = 'Job'",
                    [cloud_account_id, tenant, external_ids],
                )
            elif resource_type == "Namespace":
                database.run_query(
                    "UPDATE k8s_namespaces "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s "
                    "AND name != ALL(%s) AND is_active = true",
                    [cloud_account_id, tenant, resource_ids],
                )
            elif resource_type == "node":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND external_resource_id != ALL(%s) "
                    "AND (is_active = true OR is_active IS NULL) AND type = 'node'",
                    [cloud_account_id, external_ids],
                )
                database.run_query(
                    "UPDATE k8s_nodes "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s "
                    "AND cloud_resource_id != ALL(%s::uuid[]) AND is_active = true",
                    [cloud_account_id, tenant, external_ids],
                )
            else:  # service or other types
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND external_resource_id != ALL(%s) "
                    "AND (is_active = true OR is_active IS NULL) AND type != 'External'",
                    [cloud_account_id, external_ids],
                )

            # Close events where the resources are NOT in the active lists
            # First get affected events to track history
            select_query = """
                SELECT id, tenant, cloud_account_id, status
                FROM events
                WHERE cloud_account_id = %s AND service_key != ALL(%s) AND status != 'CLOSED'
            """
            try:
                affected_events = database.run_query(
                    select_query, [cloud_account_id, resource_ids], cursor_factory=extras.RealDictCursor
                )

                # Insert history for each event
                for event in affected_events:
                    import hashlib

                    old_value = event.get("status", "FIRING")
                    new_value = "CLOSED"

                    old_json = json.dumps(old_value, sort_keys=True)
                    new_json = json.dumps(new_value, sort_keys=True)
                    fingerprint = f"{event['id']}|status|{old_json}|{new_json}"
                    history_id = hashlib.sha256(fingerprint.encode()).hexdigest()[:32]

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
                            "method": "discovery_cleanup",
                            "resource_type": resource_type,
                            "trigger": "resource_inactive_or_deleted",
                        }
                    )

                    try:
                        database.run_query(
                            history_insert,
                            [
                                history_id,
                                event["id"],
                                event.get("tenant"),
                                event.get("cloud_account_id"),
                                "status",
                                json.dumps(old_value),
                                json.dumps(new_value),
                                "resource_inactive",
                                metadata,
                            ],
                        )
                    except Exception:
                        logging.exception(f"Failed to insert event history for resource cleanup: {event['id']}")

                # Now close the events
                database.run_query(
                    "UPDATE events "
                    "SET status = 'CLOSED', ends_at = now() "
                    "WHERE cloud_account_id = %s AND service_key != ALL(%s) "
                    "AND status != 'CLOSED'",
                    [cloud_account_id, resource_ids],
                )

                logging.info(f"Closed {len(affected_events)} events for inactive resources ({resource_type})")
            except Exception:
                logging.exception("Failed to close events with history tracking for inactive resources")
                raise

            logging.debug(f"Processed deleted resources against {len(active_resources)} active resources")
        else:
            # No active resources found in the active_resources table
            # This is a dangerous state - we need to be very careful about bulk cleanup

            # Check if we have explicit metadata indicating zero resources
            if total_resources_count is not None and total_resources_count == 0:
                # Discovery explicitly reported 0 resources - safe to cleanup all
                logging.info(
                    f"Discovery reported 0 {resource_type} resources for {cloud_account_id}/{tenant} "
                    f"- marking all existing resources as deleted"
                )
            elif total_resources_count is None:
                # No metadata provided - this could be older data format or incremental update
                # Skip complete cleanup to avoid false positives
                logging.warning(
                    f"No resource count metadata provided for {cloud_account_id}/{tenant}/{resource_type} "
                    f"and active_resources table is empty - skipping complete cleanup to prevent "
                    f"false deletion of resources (likely older data format or incremental update)"
                )
                return
            else:
                # Discovery reported resources but active_resources table is empty - likely inconsistency
                # This could happen with older data formats or if active_resources tracking failed
                logging.warning(
                    f"Discovery reported {total_resources_count} {resource_type} resources but active_resources table "
                    f"is empty for {cloud_account_id}/{tenant} - skipping complete cleanup "
                    f"(possible data format inconsistency or tracking failure)"
                )
                return

            # Only execute bulk cleanup if we're certain it's safe based on explicit zero count metadata
            # Mark all resources of this type as inactive since discovery explicitly reported zero resources
            if resource_type == "Pod":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND (is_active = true OR is_active IS NULL) AND type = 'Pod'",
                    [cloud_account_id],
                )
                database.run_query(
                    "UPDATE k8s_pods "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s AND is_active = true",
                    [cloud_account_id, tenant],
                )
            elif resource_type == "workload":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND (is_active = true OR is_active IS NULL) "
                    "AND type NOT IN ('Pod', 'node', 'Namespace', 'External')",
                    [cloud_account_id],
                )
                database.run_query(
                    "UPDATE k8s_workloads "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s AND is_active = true",
                    [cloud_account_id, tenant],
                )
            elif resource_type == "Job":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND (is_active = true OR is_active IS NULL) AND type = 'Job'",
                    [cloud_account_id],
                )
                database.run_query(
                    "UPDATE k8s_workloads "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s AND is_active = true AND kind = 'Job'",
                    [cloud_account_id, tenant],
                )
            elif resource_type == "node":
                database.run_query(
                    "UPDATE cloud_resourses "
                    "SET is_active = false, status = 'Inactive' "
                    "WHERE account = %s AND (is_active = true OR is_active IS NULL) AND type = 'node'",
                    [cloud_account_id],
                )
                database.run_query(
                    "UPDATE k8s_nodes "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s AND is_active = true",
                    [cloud_account_id, tenant],
                )
            elif resource_type == "Namespace":
                database.run_query(
                    "UPDATE k8s_namespaces "
                    "SET is_active = false "
                    "WHERE cloud_account_id = %s AND tenant_id = %s AND is_active = true",
                    [cloud_account_id, tenant],
                )

            # Close all related events since no resources of this type exist
            # Track history for this bulk closure
            select_query = """
                SELECT id, tenant, cloud_account_id, status
                FROM events
                WHERE cloud_account_id = %s AND status != 'CLOSED'
            """
            try:
                affected_events = database.run_query(
                    select_query, [cloud_account_id], cursor_factory=extras.RealDictCursor
                )

                # Insert history for each event
                import hashlib

                for event in affected_events:
                    old_value = event.get("status", "FIRING")
                    new_value = "CLOSED"

                    old_json = json.dumps(old_value, sort_keys=True)
                    new_json = json.dumps(new_value, sort_keys=True)
                    fingerprint = f"{event['id']}|status|{old_json}|{new_json}"
                    history_id = hashlib.sha256(fingerprint.encode()).hexdigest()[:32]

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
                            "method": "discovery_cleanup",
                            "resource_type": resource_type,
                            "trigger": "all_resources_deleted",
                            "total_resources": 0,
                        }
                    )

                    try:
                        database.run_query(
                            history_insert,
                            [
                                history_id,
                                event["id"],
                                event.get("tenant"),
                                event.get("cloud_account_id"),
                                "status",
                                json.dumps(old_value),
                                json.dumps(new_value),
                                "all_resources_deleted",
                                metadata,
                            ],
                        )
                    except Exception:
                        logging.exception(f"Failed to insert event history for bulk cleanup: {event['id']}")

                # Now close all events
                database.run_query(
                    "UPDATE events SET status = 'CLOSED', ends_at = now() "
                    "WHERE cloud_account_id = %s AND status != 'CLOSED'",
                    [cloud_account_id],
                )

                logging.warning(
                    f"Closed ALL {len(affected_events)} events for {cloud_account_id} - "
                    f"no resources of type {resource_type} exist"
                )
            except Exception:
                logging.exception(f"Failed to close all events with history tracking for {cloud_account_id}")
                raise

    except Exception:
        logging.exception(f"Failed to process deleted resources for {cloud_account_id}/{tenant}/{resource_type}")


def handle_full_load_deleted_resources(cloud_account_id, active_resources, tenant):
    if len(active_resources) > 0:
        logging.debug(f"Processing inactive resources for {cloud_account_id}, {len(active_resources)}")

        # Extract resource IDs for parameterized queries
        resource_keys = list(active_resources.keys())
        service_values = list(active_resources.values())

        # Update cloud_resources where resources are not in the active list
        database.run_query(
            "UPDATE cloud_resourses "
            "SET is_active = false, status = 'Inactive' "
            "WHERE account = %s AND external_resource_id != ALL(%s) "
            "AND (is_active = true OR is_active IS NULL) AND \"type\" = 'External'",
            [cloud_account_id, resource_keys],
        )

        # Update events where the resources are not in the active list
        database.run_query(
            "UPDATE events "
            "SET status = 'CLOSED' "
            "WHERE cloud_account_id = %s AND service_key != ALL(%s) AND status != 'CLOSED'",
            [cloud_account_id, service_values],
        )

        # Update k8s_pods where resources are not in the active list
        database.run_query(
            "UPDATE k8s_pods "
            "SET is_active = false "
            "WHERE cloud_account_id = %s AND tenant_id = %s AND cloud_resource_id::text != ALL(%s) "
            "AND is_active = true",
            [cloud_account_id, tenant, resource_keys],
        )

        # Update k8s_workloads where resources are not in the active list
        database.run_query(
            "UPDATE k8s_workloads "
            "SET is_active = false "
            "WHERE cloud_account_id = %s AND tenant_id = %s AND cloud_resource_id::text != ALL(%s) "
            "AND is_active = true",
            [cloud_account_id, tenant, resource_keys],
        )


def process_services_updates(cloud_account_id, pods, workloads):
    if len(pods) > 0:
        logging.debug("inserting k8s_pods for account_id {}".format(cloud_account_id))
        database.insert_data("k8s_pods", [asdict(p) for p in pods], on_conflict=pods_on_conflict)
    if len(workloads) > 0:
        logging.debug("inserting k8s_workloads for account_id {}".format(cloud_account_id))
        deduped_workloads = _deduplicate_workloads_by_identity(workloads)
        _clean_stale_workloads(cloud_account_id, deduped_workloads[0].tenant_id, deduped_workloads)
        database.insert_data("k8s_workloads", [asdict(p) for p in deduped_workloads], on_conflict=workload_on_conflict)


def process_deleted_resources(cloud_account_id, deleted_resources, tenant):
    if len(deleted_resources) > 0:
        logging.debug(f"Deleting resources {cloud_account_id}, {len(deleted_resources)}")
        service_keys = list(deleted_resources.values())
        resource_ids = list(deleted_resources.keys())

        database.run_query(
            "UPDATE cloud_resourses SET is_active = false, status = 'Inactive' "
            "WHERE account = %s AND external_resource_id = ANY(%s)",
            [cloud_account_id, resource_ids],
        )

        # Close events with history tracking
        # Note: Don't store deleted_resources list in metadata to avoid TOAST bloat
        close_events_with_history(
            cloud_account_id=cloud_account_id,
            where_conditions="service_key = ANY(%s)",
            params=[service_keys],
            closing_reason="resource_deleted",
            metadata={
                "method": "process_deleted_resources",
                "deleted_count": len(deleted_resources),
                "trigger": "discovery_reported_deletion",
            },
        )

        database.run_query(
            "UPDATE k8s_pods SET is_active = false "
            "WHERE cloud_account_id = %s AND tenant_id = %s AND cloud_resource_id = ANY(%s::uuid[])",
            [cloud_account_id, tenant, resource_ids],
        )

        database.run_query(
            "UPDATE k8s_workloads SET is_active = false "
            "WHERE cloud_account_id = %s AND tenant_id = %s AND cloud_resource_id = ANY(%s::uuid[])",
            [cloud_account_id, tenant, resource_ids],
        )


def run_node_discovery(  # noqa: C901
    data: List[Dict[str, Any]],
    tenant: str,
    cloud_account_id: str,
    is_last_batch: bool = True,
    is_first_batch: bool = True,
    metadata: Dict[str, Any] = None,
    is_batch_operation: bool = True,
):
    resources = []
    metrics = []
    nodes = []
    deleted_resources = []
    all_resources_ids = []

    # Batch-fetch node costs upfront: 1 query instead of 1-per-node
    flavor_region_pairs = set()
    for k8s_data in data:
        if k8s_data.get("deleted"):
            continue
        node_labels = k8s_data.get("node_info", {}).get("labels", {})
        flavor = node_labels.get("node.kubernetes.io/instance-type")
        if flavor:
            region = node_labels.get("topology.kubernetes.io/region", "us-east-1")
            flavor_region_pairs.add((flavor, region))

    cost_map = {}
    if flavor_region_pairs:
        sorted_pairs = sorted(flavor_region_pairs)
        placeholders = ",".join(["(%s,%s)"] * len(sorted_pairs))
        cost_query = (
            f"SELECT resource_type, resource_region, resource_cost"
            f" FROM cloud_resource_details"
            f" WHERE (resource_type, resource_region) IN ({placeholders})"
        )
        cost_params = [val for pair in sorted_pairs for val in pair]
        cost_results = database.run_query(cost_query, cost_params, cursor_factory=extras.RealDictCursor)
        for row in cost_results:
            cost_map[(row["resource_type"], row["resource_region"])] = row["resource_cost"]

    for k8s_data in data:
        cloud_resource_id = generate_uuid_from_string(k8s_data["name"] + cloud_account_id)
        if k8s_data.get("deleted", False):
            deleted_resources.append(cloud_resource_id)
            continue
        else:
            all_resources_ids.append(cloud_resource_id)
        node = {
            "tenant_id": tenant,
            "cloud_account_id": cloud_account_id,
            "cloud_resource_id": cloud_resource_id,
            "name": k8s_data["name"],
            "is_active": not k8s_data.get("deleted", False),
            "node_creation_time": k8s_data.get("node_creation_time"),
        }
        cloud_resource = parse_node_discovery(cloud_account_id, cloud_resource_id, k8s_data, tenant)
        meta = {
            "node_info": k8s_data["node_info"],
            "pods": k8s_data.get("pods"),
            "pods_count": k8s_data.get("pods_count"),
            "external_ip": k8s_data.get("external_ip"),
            "internal_ip": k8s_data.get("internal_ip"),
            "taints": k8s_data.get("taints"),
            "conditions": k8s_data.get("conditions"),
            "resource_version": k8s_data.get("resource_version"),
        }

        node["internal_ip"] = k8s_data.get("internal_ip")
        node["taints"] = k8s_data.get("taints")
        node["external_ip"] = k8s_data.get("external_ip")
        node["conditions"] = k8s_data.get("conditions")
        cloud_resource["tags"] = json.dumps(k8s_data.get("node_info", {}).get("labels"))
        node["labels"] = k8s_data.get("node_info", {}).get("labels")
        cloud_resource["is_active"] = not k8s_data.get("deleted", False)
        date = datetime.combine(datetime.fromtimestamp(k8s_data["updated_at"] / 1000), datetime.min.time())
        parse_node_metrics(tenant, cloud_account_id, date, k8s_data, meta, metrics)
        node["node_type"] = extract_node_type(k8s_data)
        labels = k8s_data.get("node_info", {}).get("labels", {})
        instance_type = labels.get("node.kubernetes.io/instance-type")
        if instance_type:
            node["node_flavor"] = instance_type

        node_region = labels.get("topology.kubernetes.io/region")
        if node_region:
            node["node_region"] = node_region

        if "node_flavor" in node:
            region = node.get("node_region", "us-east-1")
            cost = cost_map.get((node["node_flavor"], region))
            if cost is not None:
                node["cost"] = cost

        zone = labels.get("topology.kubernetes.io/zone")
        if zone:
            node["node_zone"] = zone
        node["memory_capacity"] = meta.get("memory_capacity")
        node["cpu_capacity"] = meta.get("cpu_capacity")
        node["memory_allocatable"] = meta.get("memory_allocatable")
        node["cpu_allocatable"] = meta.get("cpu_allocatable")
        node["cpu_limits"] = meta.get("cpu_limits")
        node["memory_limits"] = meta.get("memory_limits")
        node["meta"] = meta

        cloud_resource["meta"] = json.dumps(meta)
        resources.append(cloud_resource)
        nodes.append(NodeDetails(**node))

    # Only clear/cleanup on batched full syncs, not incremental updates (see service discovery comment).
    should_cleanup = is_last_batch

    if is_batch_operation:
        if is_first_batch:
            clear_active_resources(cloud_account_id, tenant, "node")

        # Store active resources from this batch for all batches in a sequence
        store_active_resources(cloud_account_id, tenant, resources, "node")

    logging.debug(
        f"Node discovery batch {cloud_account_id}/{tenant}: {len(nodes)} active nodes, {len(deleted_resources)} "
        f"deleted nodes (first={is_first_batch}, last={is_last_batch})"
    )

    try:
        logging.debug("inserting node cloud_resourses for account_id {}".format(cloud_account_id))
        # Deduplicate node resources by ID to avoid duplicate key violations
        deduplicated_resources = {}
        for resource in resources:
            resource_id = resource.get("id")
            if resource_id:
                deduplicated_resources[resource_id] = resource

        deduped_list = list(deduplicated_resources.values())
        if len(deduped_list) < len(resources):
            logging.warning(
                f"Deduplicated {len(resources) - len(deduped_list)} duplicate node resources for {cloud_account_id}. "
                f"Original: {len(resources)}, After dedup: {len(deduped_list)}"
            )

        _safe_insert_cloud_resources(deduped_list, resources_on_conflict)

        # Deduplicate nodes by cloud_resource_id before insertion
        if len(nodes) > 0:
            unique_nodes = {}
            for node in nodes:
                node_id = node.cloud_resource_id
                if node_id not in unique_nodes:
                    unique_nodes[node_id] = node

            if len(unique_nodes) < len(nodes):
                logging.warning(
                    f"Deduplicated {len(nodes) - len(unique_nodes)} duplicate nodes for {cloud_account_id}. "
                    f"Original: {len(nodes)}, After dedup: {len(unique_nodes)}"
                )

            database.insert_data("k8s_nodes", [asdict(n) for n in unique_nodes.values()], on_conflict=nodes_on_conflict)
        if len(metrics) > 0:
            database.insert_data(
                table_name="cloud_resource_metrics",
                data=metrics,
                on_conflict=metrics_on_conflict,
            )
            try:
                data_list = [m for m in metrics]
                for d in data_list:
                    d["timestamp"] = datetime.strptime(d["timestamp"], "%Y-%m-%d %H:%M:%S")
                    d["tags"] = "{}" if d["tags"] is None else json.dumps(d["tags"])
                clickhouse.insert_data("metrics", data_list)
            except Exception:
                logging.exception(f"Failed to insert row in clickhouse {data_list}")

        # Handle immediate deletions first (nodes explicitly marked as deleted)
        extract_disabled_nodes(cloud_account_id, deleted_resources, tenant)

        # Handle cleanup to mark deleted nodes as inactive
        if should_cleanup:
            logging.info(
                f"Processing final node cleanup for {cloud_account_id}/{tenant} " f"- marking absent nodes as deleted"
            )
            # Extract total count from metadata if available - only use if explicitly provided
            metadata = metadata or {}
            total_nodes = metadata.get("total_nodes") if isinstance(metadata.get("total_nodes"), int) else None

            if metadata:
                logging.info(f"Using metadata for node cleanup - total_nodes: {total_nodes}")

            if should_skip_cleanup(cloud_account_id, "node", Configs):
                logging.info(f"Skipping node cleanup for {cloud_account_id}/{tenant} — ran recently")
            else:
                handle_active_resources_deletion(cloud_account_id, tenant, "node", total_nodes)
                record_cleanup_done(cloud_account_id, "node", Configs)
    except Exception:
        logging.exception(f"Failed to insert row {json.dumps(resources, default=str)}")


def extract_node_type(k8s_data):
    node_type = "on-demand"
    if k8s_data.get("node_info", {}).get("labels", {}).get("karpenter.sh/capacity-type"):
        node_type = k8s_data.get("node_info", {}).get("labels", {}).get("karpenter.sh/capacity-type").lower()
    elif k8s_data.get("node_info", {}).get("labels", {}).get("eks.amazonaws.com/capacityType"):
        node_type = k8s_data.get("node_info", {}).get("labels", {}).get("eks.amazonaws.com/capacityType").lower()
    elif k8s_data.get("node_info", {}).get("labels", {}).get("kubernetes.azure.com/scalesetpriority"):
        node_type = k8s_data.get("node_info", {}).get("labels", {}).get("kubernetes.azure.com/scalesetpriority").lower()
    elif k8s_data.get("node_info", {}).get("labels", {}).get("cloud.google.com/gke-provisioning"):
        gke_node_type = k8s_data.get("node_info", {}).get("labels", {}).get("cloud.google.com/gke-provisioning").lower()
        if gke_node_type == "spot":
            node_type = gke_node_type
    if k8s_data.get("node_info", {}).get("labels", {}).get("spotinst.io/node-lifecycle"):
        node_type = k8s_data.get("node_info", {}).get("labels", {}).get("spotinst.io/node-lifecycle").lower()
        if node_type == "preemptible":
            node_type = "spot"
        elif node_type == "od":
            node_type = "on-demand"
    return node_type


def extract_disabled_nodes(cloud_account_id, deleted_resources, tenant):
    delete_resources(cloud_account_id, deleted_resources)
    if len(deleted_resources) > 0:
        resource_ids = list(deleted_resources)
        database.run_query(
            "UPDATE k8s_nodes SET is_active = false "
            "WHERE cloud_account_id = %s AND tenant_id = %s AND cloud_resource_id = ANY(%s::uuid[])",
            [cloud_account_id, tenant, resource_ids],
        )


def handle_full_load_node_updates(tenant, cloud_account_id, nodes_data):
    node_ids = list(nodes_data)
    database.run_query(
        "UPDATE k8s_nodes SET is_active = false "
        "WHERE cloud_account_id = %s AND tenant_id = %s "
        "AND cloud_resource_id != ALL(%s) AND is_active = true",
        [cloud_account_id, tenant, node_ids],
    )

    database.run_query(
        "UPDATE cloud_resourses SET is_active = false, status = 'Inactive' "
        "WHERE account = %s AND external_resource_id != ALL(%s) AND is_active = true",
        [cloud_account_id, node_ids],
    )


def parse_node_metrics(tenant_id, cloud_account_id, date, k8s_data, meta, metrics):
    if "memory_capacity" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "memory_capacity", cloud_account_id, k8s_data
            )
        )
        meta["memory_capacity"] = k8s_data["memory_capacity"]
    if "memory_allocatable" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "memory_allocatable", cloud_account_id, k8s_data
            )
        )
        meta["memory_allocatable"] = k8s_data["memory_allocatable"]
    if "memory_allocated" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "memory_allocated", cloud_account_id, k8s_data
            )
        )
        meta["memory_allocated"] = k8s_data["memory_allocated"]
    if "cpu_capacity" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "cpu_capacity", cloud_account_id, k8s_data
            )
        )
        meta["cpu_capacity"] = k8s_data["cpu_capacity"]
    if "cpu_allocatable" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "cpu_allocatable", cloud_account_id, k8s_data
            )
        )
        meta["cpu_allocatable"] = k8s_data["cpu_allocatable"]
    if "cpu_allocated" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "cpu_allocated", cloud_account_id, k8s_data
            )
        )
        meta["cpu_allocated"] = k8s_data["cpu_allocated"]
    if "pods_count" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "pods_count", cloud_account_id, k8s_data
            )
        )
        meta["pods_count"] = k8s_data["pods_count"]
    if "cpu_limits" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "cpu_limits", cloud_account_id, k8s_data
            )
        )
        meta["cpu_limits"] = k8s_data["cpu_limits"]
    if "memory_limits" in k8s_data:
        metrics.append(
            get_metric_data(
                tenant_id, cloud_account_id, k8s_data["name"], date, "memory_limits", cloud_account_id, k8s_data
            )
        )
        meta["memory_limits"] = k8s_data["memory_limits"]


def parse_node_discovery(cloud_account_id, cloud_resource_id, k8s_data, tenant):
    cloud_resource = {
        "id": cloud_resource_id,
        "region": "global",
        "arn": "k8s://" + k8s_data["name"],
        "tenant": tenant,
        "first_seen": k8s_data["node_creation_time"],
        "last_seen": utc_from_epoch_millis(k8s_data["updated_at"]).isoformat(),
        "resourse_id": k8s_data["name"],
        "name": k8s_data["name"],
        "account": cloud_account_id,
        "cloud_provider": "K8s",
        "type": "node",
        "service_name": "kubernetes",
        "external_resource_id": cloud_resource_id,
    }
    return cloud_resource


def delete_resources(cloud_account_id, deleted_resources):
    if len(deleted_resources) > 0:
        resource_ids = list(deleted_resources)
        database.run_query(
            "UPDATE cloud_resourses SET is_active = false, status = 'Inactive' "
            "WHERE account = %s AND external_resource_id = ANY(%s)",
            [cloud_account_id, resource_ids],
        )


def run_job_discovery(
    data: List[Dict[str, Any]],
    tenant: str,
    cloud_account_id: str,
    is_last_batch: bool = True,
    is_first_batch: bool = True,
    metadata: Dict[str, Any] = None,
    full_load: bool = False,
    is_batch_operation: bool = True,
):
    resources = []
    metrics = []
    deleted_resources = []
    jobs = []
    resources = {}
    duplicate_count = 0
    for k8s_data in data:
        _id = generate_uuid_from_string(k8s_data["service_key"] + cloud_account_id)
        if k8s_data["deleted"]:
            deleted_resources.append(_id)
            continue
        cloud_resource = {
            "id": _id,
            "region": "global",
            "arn": "k8s://" + k8s_data["service_key"],
            "tenant": tenant,
            "first_seen": k8s_data["created_at"],
            "last_seen": utc_from_epoch_millis(k8s_data["updated_at"]).isoformat(),
            "resourse_id": k8s_data["service_key"],
            "name": k8s_data["name"],
            "account": cloud_account_id,
            "cloud_provider": "K8s",
            "type": k8s_data["type"],
            "service_name": "kubernetes",
            "external_resource_id": _id,
        }

        meta = {
            "job_data": k8s_data["job_data"],
            "controllerKind": k8s_data["type"],
            "namespace": k8s_data["namespace"],
            "resource_version": k8s_data.get("resource_version"),
        }
        if "cpu_req" in k8s_data:
            meta["cpu_req"] = k8s_data["cpu_req"]
        if "mem_req" in k8s_data:
            meta["mem_req"] = k8s_data["mem_req"]
        if "labels" in k8s_data["job_data"]:
            cloud_resource["tags"] = json.dumps(k8s_data["job_data"]["labels"])
        if "status" in k8s_data:
            meta["status"] = k8s_data["status"]
        if "completions" in k8s_data:
            meta["completions"] = k8s_data["completions"]

        cloud_resource["is_active"] = not k8s_data["deleted"]
        cloud_resource["meta"] = json.dumps(meta)

        job = {
            "tenant_id": tenant,
            "cloud_account_id": cloud_account_id,
            "cloud_resource_id": _id,
            "external_id": k8s_data["service_key"],
            "namespace": k8s_data["namespace"],
            "is_active": not k8s_data.get("is_deleted", False),
            "creation_time": k8s_data.get("created_at"),
            "labels": k8s_data.get("config", {}).get("labels", {}),
            "meta": meta,
            "name": k8s_data.get("name", ""),
            "last_seen": utc_from_epoch_millis(k8s_data.get("update_time") or 0).isoformat(),
            "kind": k8s_data.get("type", ""),
        }
        if _id not in resources:
            resources[_id] = cloud_resource
            jobs.append((WorkloadDetails(**job)))
        else:
            duplicate_count += 1

    if duplicate_count > 0:
        logging.warning(
            f"Dropped {duplicate_count} duplicate jobs from agent for {cloud_account_id} "
            f"(received {len(data)}, unique {len(resources)})"
        )

    # Only clear/cleanup on batched full syncs, not incremental updates (see service discovery comment).
    should_cleanup = is_last_batch

    if is_batch_operation:
        if is_first_batch:
            clear_active_resources(cloud_account_id, tenant, "Job")

        # Store active resources from this batch for all batches in a sequence
        store_active_resources(cloud_account_id, tenant, list(resources.values()), "Job")

    logging.debug(
        f"Job discovery batch {cloud_account_id}/{tenant}: {len(jobs)} active jobs, "
        f"{len(deleted_resources)} deleted (first={is_first_batch}, last={is_last_batch})"
    )

    update_jobs(
        cloud_account_id,
        deleted_resources,
        jobs,
        metrics,
        list(resources.values()),
        tenant,
        full_load,
        should_cleanup,
        is_first_batch,
        metadata,
    )


def update_jobs(
    cloud_account_id,
    deleted_resources,
    jobs,
    metrics,
    resources,
    tenant,
    full_load: bool = False,
    should_cleanup: bool = True,
    is_first_batch: bool = True,
    metadata: Dict[str, Any] = None,
):
    try:
        logging.debug("inserting job cloud_resourses for account_id {}".format(cloud_account_id))
        _safe_insert_cloud_resources(resources, resources_on_conflict)

        # Handle immediate deletions from current batch
        if len(deleted_resources) > 0:
            delete_resources(cloud_account_id, deleted_resources)
            resource_ids = list(deleted_resources)
            database.run_query(
                "UPDATE k8s_workloads SET is_active = false "
                "WHERE cloud_account_id = %s AND tenant_id = %s AND cloud_resource_id = ANY(%s::uuid[])",
                [cloud_account_id, tenant, resource_ids],
            )

        # Handle cleanup to mark deleted jobs as inactive
        if should_cleanup:
            logging.info(
                f"Processing final job cleanup for {cloud_account_id}/{tenant} " f"- marking absent jobs as deleted"
            )
            # Extract total count from metadata if available - only use if explicitly provided
            metadata = metadata or {}
            total_jobs = metadata.get("total_jobs") if isinstance(metadata.get("total_jobs"), int) else None

            if metadata:
                logging.info(f"Using metadata for job cleanup - total_jobs: {total_jobs}")

            if should_skip_cleanup(cloud_account_id, "Job", Configs):
                logging.info(f"Skipping job cleanup for {cloud_account_id}/{tenant} — ran recently")
            else:
                handle_active_resources_deletion(cloud_account_id, tenant, "Job", total_jobs)
                record_cleanup_done(cloud_account_id, "Job", Configs)

        if len(jobs) > 0:
            logging.debug("inserting k8s_workloads for account_id {}".format(cloud_account_id))
            # Deduplicate jobs by cloud_resource_id before insertion
            unique_jobs = {}
            for job in jobs:
                job_id = job.cloud_resource_id
                if job_id not in unique_jobs:
                    unique_jobs[job_id] = job

            if len(unique_jobs) < len(jobs):
                logging.warning(
                    f"Deduplicated {len(jobs) - len(unique_jobs)} duplicate jobs for {cloud_account_id}. "
                    f"Original: {len(jobs)}, After dedup: {len(unique_jobs)}"
                )

            # Also deduplicate by secondary constraint (namespace, name, kind) and clean stale rows
            deduped_jobs = _deduplicate_workloads_by_identity(list(unique_jobs.values()))
            _clean_stale_workloads(cloud_account_id, tenant, deduped_jobs)
            database.insert_data("k8s_workloads", [asdict(p) for p in deduped_jobs], on_conflict=workload_on_conflict)
    except Exception:
        logging.exception(
            "Failed to insert jobs for account %s (%d jobs, %d resources)", cloud_account_id, len(jobs), len(resources)
        )


def run_namespace_discovery(
    data: List[Dict[str, Any]],
    tenant: str,
    cloud_account_id: str,
    full_load: bool = False,
    is_last_batch: bool = True,
    is_first_batch: bool = True,
    metadata: Dict[str, Any] = None,
    is_batch_operation: bool = True,
):
    namespaces = []
    namespace_resources = []

    for k8s_data in data:
        namespace = {
            "name": k8s_data["name"],
            "tenant_id": tenant,
            "cloud_account_id": cloud_account_id,
            "creation_time": k8s_data["creation_time"],
            "workload_count": k8s_data["workload_count"],
            "pod_count": k8s_data["pod_count"],
            "is_active": not k8s_data.get("deleted", False),
        }

        # Create a mock resource for active resources tracking
        namespace_resource = {
            "external_resource_id": k8s_data["name"],
            "resourse_id": k8s_data["name"],
            "type": "Namespace",
        }

        namespaces.append(NamespaceDetails(**namespace))
        if not k8s_data.get("deleted", False):
            namespace_resources.append(namespace_resource)

    # Only clear/cleanup on batched full syncs, not incremental updates (see service discovery comment).
    should_cleanup = is_last_batch

    if is_batch_operation:
        if is_first_batch:
            clear_active_resources(cloud_account_id, tenant, "Namespace")

        # Store active resources from this batch for all batches in a sequence
        store_active_resources(cloud_account_id, tenant, namespace_resources, "Namespace")

    deleted_count = len([n for n in namespaces if not n.is_active])
    active_count = len(namespace_resources)
    logging.debug(
        f"Namespace discovery batch {cloud_account_id}/{tenant}: {active_count} active namespaces, "
        f"{deleted_count} deleted (first={is_first_batch}, last={is_last_batch})"
    )

    if len(namespaces) > 0:
        # Deduplicate namespaces by cloud_resource_id (using name + cloud_account_id as identifier)
        unique_namespaces = {}
        for namespace in namespaces:
            # Use a combination of tenant, cloud_account_id and name as unique key
            ns_key = f"{namespace.tenant_id}#{namespace.cloud_account_id}#{namespace.name}"
            if ns_key not in unique_namespaces:
                unique_namespaces[ns_key] = namespace

        if len(unique_namespaces) < len(namespaces):
            logging.warning(
                f"Deduplicated {len(namespaces) - len(unique_namespaces)} duplicate namespaces for {cloud_account_id}. "
                f"Original: {len(namespaces)}, After dedup: {len(unique_namespaces)}"
            )

        database.insert_data(
            "k8s_namespaces", [asdict(n) for n in unique_namespaces.values()], on_conflict=namespace_on_conflict
        )

    # Handle cleanup to mark deleted namespaces as inactive
    if should_cleanup:
        logging.info(
            f"Processing final namespace cleanup for {cloud_account_id}/{tenant} - marking absent namespaces "
            f"as deleted"
        )
        # Extract total count from metadata if available - only use if explicitly provided
        metadata = metadata or {}
        total_namespaces = (
            metadata.get("total_namespaces") if isinstance(metadata.get("total_namespaces"), int) else None
        )

        if metadata:
            logging.info(f"Using metadata for namespace cleanup - total_namespaces: {total_namespaces}")

        if should_skip_cleanup(cloud_account_id, "Namespace", Configs):
            logging.info(f"Skipping namespace cleanup for {cloud_account_id}/{tenant} — ran recently")
        else:
            handle_active_resources_deletion(cloud_account_id, tenant, "Namespace", total_namespaces)
            record_cleanup_done(cloud_account_id, "Namespace", Configs)


def get_metric_data(
    tenant_id: str, cloud_account_id: str, resource_id: str, date: datetime, key: str, cloud_id: str, data: Dict
) -> Dict[str, Any]:
    cloud_resource_metric = {
        "cloud_resource_id": generate_uuid_from_string(resource_id + cloud_id),
        "timestamp": date.strftime("%Y-%m-%d %H:%M:%S"),
        "metric": str(key),
        "metric_type": "g",
        "tags": {"name": resource_id, "type": "node"},
        "value": data[key],
        "cloud_account_id": cloud_account_id,
        "tenant_id": tenant_id,
    }
    return cloud_resource_metric


def get_active_resources(tenant: str, cloud_account_id: str, filter_params: Dict) -> list[dict[str, Any]]:
    if "type" not in filter_params:
        raise RuntimeError("Please specify resource type")
    types = filter_params["type"]
    active_resource = []
    if "events" in types:
        active_resource.extend(get_open_issues(tenant, cloud_account_id))
        return active_resource
    if "Namespace" in types:
        active_resource.extend(get_active_namespaces(tenant, cloud_account_id))
        return active_resource
    if "node" in types:
        active_resource.extend(get_active_nodes(tenant, cloud_account_id))
        return active_resource
    if "Job" in types:
        active_resource.extend(get_active_job(tenant, cloud_account_id))
        return active_resource

    active_resource.extend(get_active_pod(cloud_account_id))
    active_resource.extend(get_active_workload(tenant, cloud_account_id))
    return active_resource


def generate_service(d, meta):
    config = {}
    ready_pods = 0
    total_pods = 0
    restart_count = 0
    status = None
    if "config" in meta:
        config = meta["config"]
    if "ready_pods" in meta:
        ready_pods = meta["ready_pods"]
    if "total_pods" in meta:
        total_pods = meta["total_pods"]
    if "status" in meta:
        status = meta["status"]
    if "restart_count" in meta:
        restart_count = meta["restart_count"]
    t = {
        "name": d[0],
        "type": d[1],
        "namespace": meta["namespace"],
        "config": config,
        "ready_pods": ready_pods,
        "total_pods": total_pods,
        "service_key": d[5],
        "status": status,
        "restart_count": restart_count,
    }
    return t


def generate_node_type(name, meta):
    t = {
        "name": name,
        "conditions": meta["conditions"],
        "node_info": meta["node_info"],
        "pods_count": 0,
        "internal_ip": meta["internal_ip"],
        "external_ip": meta["external_ip"],
        "memory_capacity": 0,
        "memory_allocatable": 0,
        "memory_allocated": 0,
        "cpu_capacity": 0,
        "cpu_allocatable": 0,
        "cpu_allocated": 0,
    }
    if "taints" in meta:
        t["taints"] = meta["taints"]
    if "memory_capacity" in meta:
        t["memory_capacity"] = meta["memory_capacity"]
    if "memory_allocatable" in meta:
        t["memory_allocatable"] = meta["memory_allocatable"]
    if "memory_allocated" in meta:
        t["memory_allocated"] = meta["memory_allocated"]
    if "cpu_capacity" in meta:
        t["cpu_capacity"] = meta["cpu_capacity"]
    if "cpu_allocatable" in meta:
        t["cpu_allocatable"] = meta["cpu_allocatable"]
    if "cpu_allocated" in meta:
        t["cpu_allocated"] = meta["cpu_allocated"]
    if "pods_count" in meta:
        t["pods_count"] = meta["pods_count"]
    return t


def generate_job_type(name, service_key, meta):
    t = {"name": name, "service_key": service_key, "namespace": meta["namespace"]}
    if "job_data" in meta:
        t["job_data"] = meta["job_data"]
    if "cpu_req" in meta:
        t["cpu_req"] = meta["cpu_req"]
    if "mem_req" in meta:
        t["mem_req"] = meta["mem_req"]
    if "status" in meta:
        t["status"] = meta["status"]
    if "completions" in meta:
        t["completions"] = meta["completions"]
    return t


def get_open_issues(tenant: str, cloud_account_id: str) -> list[dict[str, Any]]:
    select_sql = (
        "SELECT subject_type, CASE WHEN service_key IS NULL OR service_key = '' THEN subject_name ELSE service_key "
        "END AS service_key FROM events WHERE status != 'CLOSED' AND cloud_account_id = %s AND tenant = %s "
        "GROUP BY cloud_account_id, tenant, subject_type, service_key, subject_name"
    )
    rows = database.run_query(select_sql, [cloud_account_id, tenant])
    open_issues = []
    for row in rows:
        issue = {"type": row[0], "service_key": row[1]}
        open_issues.append(issue)
    return open_issues


def get_active_namespaces(tenant: str, cloud_account_id: str) -> list[dict[str, Any]]:
    select_sql = "SELECT name FROM k8s_namespaces WHERE is_active = true AND cloud_account_id = %s AND tenant_id = %s"
    rows = database.run_query(select_sql, [cloud_account_id, tenant])
    active_namespace = []
    for row in rows:
        namespace = {"name": row[0]}
        active_namespace.append(namespace)
    return active_namespace


def get_active_pod(cloud_account_id: str) -> list[dict[str, Any]]:
    select_sql = "SELECT external_id, name, meta FROM k8s_pods WHERE is_active = true AND cloud_account_id = %s"
    resource_type = "pod"
    rows = database.run_query(select_sql, [cloud_account_id], cursor_factory=extras.RealDictCursor)
    active_pods = []
    for row in rows:
        meta = row.get("meta", {})
        t = parse_active_service(meta, row, resource_type)
        active_pods.append(t)
    return active_pods


def get_active_job(tenant: str, cloud_account_id: str) -> list[dict[str, Any]]:
    select_sql = (
        "SELECT external_id, name, meta, kind FROM k8s_workloads "
        "WHERE is_active = true AND cloud_account_id = %s AND tenant_id = %s AND kind = 'Job'"
    )

    rows = database.run_query(select_sql, [cloud_account_id, tenant], cursor_factory=extras.RealDictCursor)
    active_workloads = []
    for row in rows:
        resource_type = row["kind"]
        meta = row.get("meta", {})
        t = parse_active_service(meta, row, resource_type)
        active_workloads.append(t)
    return active_workloads


def get_active_workload(tenant: str, cloud_account_id: str) -> list[dict[str, Any]]:
    select_sql = (
        "SELECT external_id, name, meta, kind FROM k8s_workloads "
        "WHERE is_active = true AND cloud_account_id = %s AND tenant_id = %s"
    )

    rows = database.run_query(select_sql, [cloud_account_id, tenant], cursor_factory=extras.RealDictCursor)
    active_workloads = []
    for row in rows:
        resource_type = row["kind"]
        meta = row.get("meta", {})
        t = parse_active_service(meta, row, resource_type)
        active_workloads.append(t)
    return active_workloads


def get_active_nodes(tenant: str, cloud_account_id: str) -> list[dict[str, Any]]:
    select_sql = "SELECT name, meta FROM k8s_nodes WHERE is_active = true AND cloud_account_id = %s AND tenant_id = %s"

    rows = database.run_query(select_sql, [cloud_account_id, tenant], cursor_factory=extras.RealDictCursor)
    active_nodes = []
    for row in rows:
        meta = row.get("meta", {})
        t = generate_node_type(row["name"], meta)
        active_nodes.append(t)
    return active_nodes


def parse_active_service(meta, row, resource_type):
    t = {
        "name": row.get("name"),
        "type": resource_type,
        "namespace": meta["namespace"],
        "service_key": row.get("external_id"),
    }
    if "config" in row["meta"]:
        t["config"] = meta["config"]
    if "ready_pods" in meta:
        t["ready_pods"] = meta["ready_pods"]
    if "total_pods" in meta:
        t["total_pods"] = meta["total_pods"]
    if "status" in meta:
        t["status"] = meta["status"]
    if "restart_count" in meta:
        t["restart_count"] = meta["restart_count"]
    if "job_data" in meta:
        t["job_data"] = meta["job_data"]
    if "cpu_req" in meta:
        t["cpu_req"] = meta["cpu_req"]
    if "mem_req" in meta:
        t["mem_req"] = meta["mem_req"]
    if "status" in meta:
        t["status"] = meta["status"]
    if "completions" in meta:
        t["completions"] = meta["completions"]
    return t


def close_events_with_history(
    cloud_account_id: str,
    where_conditions: str,
    params: List[Any],
    closing_reason: str,
    metadata: Dict[str, Any] = None,
):
    """
    Helper to close events and track history with rich context.

    Args:
        cloud_account_id: Cloud account ID
        where_conditions: SQL WHERE clause conditions (e.g., "service_key = ANY(%s)")
        params: Query parameters
        closing_reason: Reason for closure (e.g., "workload_deleted", "service_inactive", "node_removed")
        metadata: Additional context (e.g., {"deleted_services": [...], "method": "discovery"})
    """
    import hashlib

    # Build parameterized queries - cloud_account_id is the first param, then caller's params
    all_params = [cloud_account_id] + params

    # Get affected events before closing them
    select_query = (
        "SELECT id, tenant, cloud_account_id, status "
        f"FROM events WHERE cloud_account_id = %s AND {where_conditions} AND status != 'CLOSED'"
    )

    try:
        affected_events = database.run_query(select_query, all_params, cursor_factory=extras.RealDictCursor)

        # Batch insert history for all events at once (avoid N+1 queries)
        if affected_events:
            history_records = []
            for event in affected_events:
                old_value = event.get("status", "FIRING")
                new_value = "CLOSED"

                # Generate deterministic ID (same logic as Go)
                old_json = json.dumps(old_value, sort_keys=True)
                new_json = json.dumps(new_value, sort_keys=True)
                fingerprint = f"{event['id']}|status|{old_json}|{new_json}"
                history_id = hashlib.sha256(fingerprint.encode()).hexdigest()[:32]

                history_records.append(
                    {
                        "id": history_id,
                        "event_id": event["id"],
                        "tenant_id": event.get("tenant"),
                        "cloud_account_id": event.get("cloud_account_id"),
                        "change_type": "status",
                        "old_value": json.dumps(old_value),
                        "new_value": json.dumps(new_value),
                        "change_reason": closing_reason,
                        "metadata": json.dumps(metadata) if metadata else None,
                    }
                )

            # Batch insert all history records in one query (replaces N individual inserts)
            if history_records:
                try:
                    database.insert_data("event_history", history_records, on_conflict="ON CONFLICT (id) DO NOTHING")
                except Exception:
                    logging.exception("Failed to batch insert event history")
                    # Don't fail - history is not critical, continue with closure

        # Now close the events
        update_query = (
            f"UPDATE events SET status = 'CLOSED', ends_at = now() "
            f"WHERE cloud_account_id = %s AND {where_conditions} AND status != 'CLOSED'"
        )
        database.run_query(update_query, all_params)

        logging.debug(
            f"Closed {len(affected_events)} events with reason: {closing_reason}",
            extra={"cloud_account_id": cloud_account_id, "closing_reason": closing_reason},
        )

    except Exception:
        logging.exception(f"Failed to close events with history tracking for reason: {closing_reason}")
        raise


def run_status_update(data: Dict[str, Any], tenant: str, cloud_account_id: str):
    service_keys = []
    for value in data:
        if "new_status" in value and value["new_status"].lower() in ["succeeded", "running"]:
            service_keys.append(value["service_key"])

    if len(service_keys) == 0:
        return

    # Close events with history tracking
    close_events_with_history(
        cloud_account_id=cloud_account_id,
        where_conditions="service_key = ANY(%s) AND source != 'prometheus'",
        params=[service_keys],
        closing_reason="workload_status_resolved",
        metadata={
            "method": "discovery",
            "resolved_workloads": service_keys,
            "trigger": "workload_status_succeeded_or_running",
        },
    )


def run_resolved_services(service_keys: List[str], tenant: str, cloud_account_id: str):
    if len(service_keys) == 0:
        return

    # Close events with history tracking
    # Note: Don't store deleted_services list in metadata to avoid TOAST bloat
    close_events_with_history(
        cloud_account_id=cloud_account_id,
        where_conditions="service_key = ANY(%s) AND source != 'prometheus'",
        params=[service_keys],
        closing_reason="service_deleted",
        metadata={"method": "discovery", "deleted_count": len(service_keys), "trigger": "service_no_longer_exists"},
    )


def run_resolved_namespace(namespace: List[str], tenant: str, cloud_account_id: str):
    if len(namespace) == 0:
        return

    database.run_query(
        "UPDATE k8s_namespaces SET is_active = false "
        "WHERE cloud_account_id = %s AND name = ANY(%s) AND tenant_id = %s",
        [cloud_account_id, namespace, tenant],
    )


def run_resolved_node_services(service_keys: List[str], tenant: str, cloud_account_id: str):
    if len(service_keys) == 0:
        return

    # Close events with history tracking
    # Note: Don't store removed_nodes list in metadata to avoid TOAST bloat
    close_events_with_history(
        cloud_account_id=cloud_account_id,
        where_conditions="subject_name = ANY(%s) AND source != 'prometheus'",
        params=[service_keys],
        closing_reason="node_removed",
        metadata={"method": "discovery", "removed_count": len(service_keys), "trigger": "node_no_longer_exists"},
    )
