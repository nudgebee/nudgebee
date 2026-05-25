import json
import logging
from datetime import datetime
from typing import Dict, Any, List

from psycopg2 import extras

from db import database, clickhouse
from handlers.discovery_handler import generate_uuid_from_string


def get_spends_data(
    resource_id: str, tenant: str, date: datetime, cloud_id: str, k8s_data: Dict[str, Any], tags: Dict[str, Any]
) -> Dict[str, Any]:
    cloud_resource_spend = {
        "date": date.strftime("%Y-%m-%d %H:%M:%S"),
        "amount": k8s_data["totalCost"],
        "tenant": tenant,
        "cloud_account": cloud_id,
        "cloud_resource_id": resource_id,
        "tags": tags,
    }
    return cloud_resource_spend


def get_metric_data(
    resource_id: str,
    date: datetime,
    key: str,
    k8s_data: Dict[str, Any],
    tags: Dict[str, Any],
    cloud_account_id: str,
    tenant_id: str,
) -> Dict[str, Any]:
    cloud_resource_metric = None
    if k8s_data[key] > 0:
        cloud_resource_metric = {
            "cloud_resource_id": resource_id,
            "timestamp": date.strftime("%Y-%m-%d %H:%M:%S"),
            "metric": str(key),
            "metric_type": "g",
            "tags": json.dumps(tags),
            "value": k8s_data[key],
            "cloud_account_id": cloud_account_id,
            "tenant_id": tenant_id,
        }
    return cloud_resource_metric


sum_columns = [
    "cpuCores",
    "cpuCoreHours",
    "cpuCost",
    "cpuCostAdjustment",
    "gpuCount",
    "gpuHours",
    "gpuCost",
    "gpuCostAdjustment",
    "networkTransferBytes",
    "networkReceiveBytes",
    "networkCost",
    "networkCrossZoneCost",
    "networkCrossRegionCost",
    "networkInternetCost",
    "networkCostAdjustment",
    "loadBalancerCost",
    "loadBalancerCostAdjustment",
    "pvBytes",
    "pvByteHours",
    "pvCost",
    "pvCostAdjustment",
    "ramBytes",
    "ramByteHours",
    "ramCost",
    "ramCostAdjustment",
    "externalCost",
    "sharedCost",
    "totalCost",
]
avg_columns = [
    "cpuCoreRequestAverage",
    "cpuCoreUsageAverage",
    "cpuEfficiency",
    "ramByteRequestAverage",
    "ramByteUsageAverage",
    "ramEfficiency",
    "totalEfficiency",
]
metrics_avg_on_conflict = "ON CONFLICT(metric,cloud_resource_id,timestamp) DO UPDATE SET value = (EXCLUDED.value) "

metrics_sum_on_conflict = "ON CONFLICT(metric,cloud_resource_id,timestamp) DO UPDATE SET value = (EXCLUDED.value)"

spends_on_conflict = "ON CONFLICT(tenant,cloud_account,cloud_resource_id,date) DO UPDATE SET amount = EXCLUDED.amount"


def process_spend(spend: Dict[str, Any], tenant: str, cloud_account_id: str) -> None:
    if "node" in spend and len(spend["node"]) > 0:
        process_node_spend(spend["node"], tenant, cloud_account_id)
    if "service" in spend and len(spend["service"]) > 0:
        process_service_spend(spend["service"], tenant, cloud_account_id)


def process_node_spend(spend: List[Dict[str, Any]], tenant: str, cloud_account_id: str) -> None:
    node_data = []
    resource_ids = []
    for node_spend in spend:
        for node, values in node_spend.items():
            if node == "":
                continue
            external_resource_id = generate_uuid_from_string(values["name"] + cloud_account_id)
            resource_ids.append(external_resource_id)
            node_data.append(values)

    if len(resource_ids) == 0:
        return

    # Batch-fetch all node resources in one query instead of one per node
    list_resources = database.run_query(
        "select id, external_resource_id from cloud_resourses where external_resource_id = ANY(%s)",
        values=[resource_ids],
        cursor_factory=extras.RealDictCursor,
    )
    resource_ids_map = {}
    for r in list_resources:
        resource_ids_map[r.get("external_resource_id")] = r

    spend_data = []
    metrics_sum = []
    metrics_avg = []
    for node in node_data:
        date = datetime.combine(datetime.fromisoformat(node["window"]["start"]), datetime.min.time())
        external_resource_id = generate_uuid_from_string(node["name"] + cloud_account_id)

        if external_resource_id not in resource_ids_map:
            continue
        resource_id = resource_ids_map[external_resource_id].get("id")
        resource_meta = {"name": node["name"], "type": "node"}
        spend_data.append(get_spends_data(resource_id, tenant, date, cloud_account_id, node, resource_meta))
        for key in sum_columns:
            metric = get_metric_data(resource_id, date, key, node, resource_meta, cloud_account_id, tenant)
            if metric:
                metrics_sum.append(metric)
        for key in avg_columns:
            metric = get_metric_data(resource_id, date, key, node, resource_meta, cloud_account_id, tenant)
            if metric:
                metrics_avg.append(metric)

    database.insert_data(table_name="spends", data=spend_data, on_conflict=spends_on_conflict)
    database.insert_data(
        table_name="cloud_resource_metrics",
        data=metrics_avg,
        on_conflict=metrics_avg_on_conflict,
    )
    database.insert_data(
        table_name="cloud_resource_metrics",
        data=metrics_sum,
        on_conflict=metrics_sum_on_conflict,
    )

    write_to_clickhouse(spend_data=spend_data, metrics_avg=metrics_avg, metrics_sum=metrics_sum)


def process_service_spend(spend: List[Dict[str, Any]], tenant: str, cloud_account_id: str) -> None:
    service_data = []
    resource_ids = []
    for service in spend:
        for s, values in service.items():
            if s == "" or s == "__unmounted__":
                continue
            if "namespace" not in values["properties"] or "pod" not in values["properties"]:
                logging.info("invalid data found {}".format(values))
                continue
            service_key = f"{values['properties']['namespace']}/Pod/{values['properties']['pod']}"
            external_resource_id = generate_uuid_from_string(service_key + cloud_account_id)
            resource_ids.append(external_resource_id)
            service_data.append(values)
    if len(resource_ids) > 0:
        list_resources = database.run_query(
            "select id,external_resource_id,meta,name from cloud_resourses where external_resource_id in"
            f" {tuple(resource_ids)} ",
            cursor_factory=extras.RealDictCursor,
        )
    else:
        logging.info("No valid resource ids found, skipping %s,", spend)
        return

    resource_ids_map = {}
    for r in list_resources:
        resource_ids_map[r.get("external_resource_id")] = r
    spend_data = []
    metrics_sum = []
    metrics_avg = []
    for service in service_data:
        date = datetime.combine(datetime.fromisoformat(service["window"]["start"]), datetime.min.time())
        service_key = f"{service['properties']['namespace']}/Pod/{service['properties']['pod']}"
        external_resource_id = generate_uuid_from_string(service_key + cloud_account_id)

        if external_resource_id not in resource_ids_map:
            continue
        resource_id = resource_ids_map[external_resource_id].get("id")
        meta = resource_ids_map[external_resource_id].get("meta", {})
        if not meta or "controller" not in meta:
            logging.debug("controller not found in meta %s, resource id %s", meta, external_resource_id)
            continue
        resource_meta = {
            "name": resource_ids_map[external_resource_id].get("name"),
            "namespace": meta.get("namespace"),
            "controller": meta.get("controller"),
            "controllerKind": meta.get("controllerKind"),
            "node": meta.get("node"),
            "pod_fqdn": f"{meta.get('namespace')}.{resource_ids_map[external_resource_id].get('name')}",
            "workload_fqdn": f"{meta.get('namespace')}.{meta.get('controller')}",
        }

        service_spend = get_spends_data(resource_id, tenant, date, cloud_account_id, service, resource_meta)
        service_spend["exclude_aggregate"] = True
        spend_data.append(service_spend)
        for key in sum_columns:
            metric = get_metric_data(resource_id, date, key, service, resource_meta, cloud_account_id, tenant)
            if metric:
                metrics_sum.append(metric)
        for key in avg_columns:
            metric = get_metric_data(resource_id, date, key, service, resource_meta, cloud_account_id, tenant)
            if metric:
                metrics_avg.append(metric)

    database.insert_data(table_name="spends", data=spend_data, on_conflict=spends_on_conflict)
    database.insert_data(
        table_name="cloud_resource_metrics",
        data=metrics_avg,
        on_conflict=metrics_avg_on_conflict,
    )
    database.insert_data(
        table_name="cloud_resource_metrics",
        data=metrics_sum,
        on_conflict=metrics_sum_on_conflict,
    )

    write_to_clickhouse(spend_data, metrics_avg, metrics_sum)


def write_to_clickhouse(
    spend_data: List[Dict[str, Any]], metrics_avg: List[Dict[str, Any]], metrics_sum: List[Dict[str, Any]]
) -> None:
    try:
        for d in spend_data:
            d["date"] = datetime.strptime(d["date"], "%Y-%m-%d %H:%M:%S")
            d["tags"] = "{}" if d["tags"] is None else json.dumps(d["tags"])
        clickhouse.insert_data("spends", spend_data)
    except Exception:
        logging.exception(f"Failed to insert row {json.dumps(spend_data, default=str)}")

    try:
        data_list = metrics_avg + metrics_sum
        for d in data_list:
            d["timestamp"] = datetime.strptime(d["timestamp"], "%Y-%m-%d %H:%M:%S")
            d["tags"] = "{}" if d["tags"] is None else json.dumps(d["tags"])
        clickhouse.insert_data("metrics", data_list)
    except Exception:
        logging.exception(f"Failed to insert row in clickhouse {json.dumps(data_list, default=str)}")


def refresh_views() -> None:
    t0 = datetime.now()
    views: List[str] = []
    for view in views:
        try:
            database.run_query(f"refresh materialized view concurrently {view}")
        except Exception as e:
            logging.exception("Unable to refresh view - " + view + ", " + str(e))

        logging.info(f"time taken to refresh views - {(datetime.now() - t0).total_seconds()} secs")
