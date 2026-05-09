import json
import logging
from copy import deepcopy
from datetime import datetime
from typing import List, Optional, Sequence, Dict, Any

import pytz
from pydantic import BaseModel
from tabulate import tabulate
from psycopg2 import sql

from db import database, clickhouse
from handlers.discovery_handler import generate_uuid_from_string


class BaseBlock(BaseModel):
    hidden: bool = False


class RendererType:
    DATETIME = "DATETIME"


def render_value(renderer: RendererType, value):
    if renderer == RendererType.DATETIME:
        date_value = datetime.fromtimestamp(value / 1000.0)
        return date_value.astimezone(pytz.timezone("UTC")).strftime("%b %d, %Y, %I:%M:%S %p")
    raise Exception(f"Unsupported renderer type {renderer}")


class TableBlock(BaseBlock):
    """
    Table display of a list of lists.

    Note: Wider tables appears as a file attachment on Slack, because they aren't rendered properly inline

    :var column_width: Hint to sink for the portion of size each column should use. Not supported by all sinks.
        example: [1, 1, 1, 2] use twice the size for last column.
    """

    rows: List[List]
    headers: Sequence[str] = ()
    column_renderers: Dict = {}
    table_name: str = ""
    column_width: Optional[List[int]] = None

    def __init__(
        self,
        rows: List[List],
        headers: Sequence[str] = (),
        column_renderers: Dict = {},
        table_name: str = "",
        column_width: Optional[List[int]] = None,
        **kwargs,
    ):
        """
        :param rows: a list of rows. each row is a list of columns
        :param headers: names of each column
        """
        super().__init__(
            rows=rows,
            headers=headers,
            column_renderers=column_renderers,
            table_name=table_name,
            column_width=column_width,
            **kwargs,
        )

    @classmethod
    def __calc_max_width(cls, headers, rendered_rows, table_max_width: int) -> List[int]:
        # We need to make sure the total table width, doesn't exceed the max width,
        # otherwise, the table is printed corrupted
        columns_max_widths = [len(header) for header in headers]
        for row in rendered_rows:
            for idx, val in enumerate(row):
                columns_max_widths[idx] = max(len(str(val)), columns_max_widths[idx])

        if sum(columns_max_widths) > table_max_width:  # We want to limit the widest column
            largest_width = max(columns_max_widths)
            widest_column_idx = columns_max_widths.index(largest_width)
            diff = sum(columns_max_widths) - table_max_width
            columns_max_widths[widest_column_idx] = largest_width - diff
            if columns_max_widths[widest_column_idx] < 0:  # in case the diff is bigger than the largest column
                # just divide equally
                columns_max_widths = [
                    int(table_max_width / len(columns_max_widths)) for i in range(0, len(columns_max_widths))
                ]

        return columns_max_widths

    @classmethod
    def __trim_rows(cls, contents: str, max_chars: int) -> str:
        # We need to make sure that the total character count doesn't exceed max_chars,
        # but if we cut off a row in the middle then it messes up the whole table.
        # So instead remove entire rows at a time
        if len(contents) <= max_chars:
            return contents

        truncator = "\n..."
        max_chars -= len(truncator)

        lines = contents.splitlines()
        length_so_far = 0
        lines_to_include = 0
        for line in lines:
            new_length = length_so_far + len("\n") + len(line)
            if new_length > max_chars:
                break
            else:
                length_so_far = new_length
                lines_to_include += 1

        return "\n".join(lines[:lines_to_include]) + truncator

    @classmethod
    def __to_strings_rows(cls, rows):
        # This is just to assert all row column values are strings. Tabulate might fail on other types
        return [list(map(lambda column_value: str(column_value), row)) for row in rows]

    def to_markdown(self, max_chars=None, add_table_header: bool = True) -> str:
        table_header = f"{self.table_name}\n" if self.table_name else ""
        table_header = "" if not add_table_header else table_header
        prefix = f"{table_header}```\n"
        suffix = "\n```"
        table_contents = self.to_table_string()
        if max_chars is not None:
            max_chars = max_chars - len(prefix) - len(suffix)
            table_contents = self.__trim_rows(table_contents, max_chars)

        return f"{prefix}{table_contents}{suffix}"

    def to_table_string(self) -> str:
        rendered_rows = self.__to_strings_rows(self.render_rows())
        col_max_width = self.__calc_max_width(self.headers, rendered_rows, 70)
        return tabulate(
            rendered_rows,
            headers=self.headers,
            tablefmt="presto",
            maxcolwidths=col_max_width,
        )

    def render_rows(self) -> List[List]:
        if self.column_renderers is None:
            return self.rows
        new_rows = deepcopy(self.rows)
        for column_name, renderer_type in self.column_renderers.items():
            column_idx = self.headers.index(column_name)
            for row in new_rows:
                row[column_idx] = render_value(renderer_type, row[column_idx])
        return new_rows


def get_severity(severity: float) -> Optional[str]:
    if severity == 0.0:
        return None
    elif severity == 1.0:
        return "INFO"
    elif severity == 2.0:
        return "INFO"
    elif severity == 3.0:
        return "Medium"
    elif severity == 4.0:
        return "Critical"
    return None


def run_event_handler_async(content: str) -> None:
    data = json.loads(content)
    events = []
    finding = data["finding"]

    tenant = data["tenant"]

    cloud_account_id = data["cloud_account_id"]

    if finding["aggregation_key"] == "krr_report":
        handle_krr_report(data, tenant, cloud_account_id)
        return

    select_filter = {
        "account": cloud_account_id,
        "tenant": tenant,
        "(meta ->> 'name' :: text)": finding["subject_name"],
        "(meta ->> 'namespace' :: text)": finding["subject_namespace"],
    }
    resource_list = database.select_data(
        "cloud_resourses",
        ["id", "tenant", "(meta ->> 'name' :: text)", "(meta ->> 'namespace' :: text)", "account"],
        select_filter,
    )
    config_change_event = []
    if finding["finding_type"] != "configuration_change":
        select_config_change_event = sql.SQL(
            "select id, service_key, evidences from {} where service_key = %s and "
            "cloud_account_id = %s  and finding_type = %s order by created_at desc "
            "limit"
            " 1"
        ).format(sql.Identifier("events"))
        values = [finding["service_key"], data["cloud_account_id"], "configuration_change"]
        config_change_event = database.run_query(select_config_change_event, values)
    evidence = format_evidence(data["evidence"], config_change_event)

    resource_map = {}
    for resource in resource_list:
        resource_map[f"{resource[2]}:{resource[3]}"] = resource

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
        "cluster": finding["cluster"],
        "starts_at": finding["starts_at"],
    }

    if f"{finding['subject_name']}:{finding['subject_namespace']}" in resource_map.keys():
        event["cloud_resource_id"] = resource_map[f"{finding['subject_name']}:{finding['subject_namespace']}"][0]

    if "ends_at" in finding:
        event["ends_at"] = finding["ends_at"]
    event["fingerprint"] = finding["fingerprint"]
    event["evidences"] = json.dumps(evidence)
    event["tenant"] = data["tenant"]
    event["cloud_account_id"] = data["cloud_account_id"]
    events.append(event)
    try:
        events_on_conflict = (
            "ON CONFLICT(tenant, cloud_account_id, finding_id) DO UPDATE SET "
            "status = EXCLUDED.status, "
            "ends_at = EXCLUDED.ends_at, "
            "evidences = EXCLUDED.evidences, "
            "priority = EXCLUDED.priority, "
            "updated_at = now()"
        )
        database.insert_data("events", events, on_conflict=events_on_conflict)
    except Exception:
        logging.exception(f"Failed to insert row {json.dumps(events)}")

    try:
        clickhouse.insert_data("events", events)
    except Exception:
        logging.exception(f"Failed to insert row in clickhouse {json.dumps(events, default=str)}")


def handle_krr_report(content: dict, tenant: str, cloud_account_id: str) -> None:
    logging.info("Processing krr report for tenant {} , account id {}", tenant, cloud_account_id)
    tenant = tenant
    json.dumps(content)
    cloud_account_id = cloud_account_id
    try:
        report = json.loads(content["evidence"][0]["data"])[0]
        score = report["score"]
        resource_names = []
        for r in report["data"]:
            resource_names.append(f"{r['container']}:{r['namespace']}")
        query = sql.SQL(
            "select cr.id , cr.tenant, (cr.meta ->> 'container' :: text), (cr.meta ->> 'namespace' :: text), "
            "resource_cost / (( cast((crd.resource_capacity ->> 'cpu_virtual'::text) as integer) * 88 ) + (cast (("
            "crd.resource_capacity ->> 'memory_gb'::text) as decimal) * 12 ) ) * 88 as cpu_cost, resource_cost / (( "
            "cast((crd.resource_capacity ->> 'cpu_virtual'::text) as integer) * 88 ) + (cast ((crd.resource_capacity "
            "->> 'memory_gb'::text) as decimal) * 12 ) ) * 12 as memory_cost, resource_cost, intnc.meta -> 'labels' "
            "->> 'node.kubernetes.io/instance-type'::text , cr.account from {} as cr inner join "
            "cloud_resourses intnc on intnc.tenant = cr.tenant and cr.account = intnc.account and intnc.type = 'node' "
            "and (cr.tags ->> 'node'::text) = (intnc.name) inner join cloud_resource_details crd on ( "
            "crd.resource_type = (intnc.meta -> 'labels' ->> 'node.kubernetes.io/instance-type'::text ) and "
            "crd.resource_region = 'us-east-1') where cr.account = %s and cr.tenant = %s"
        ).format(sql.Identifier("cloud_resourses"))
        values = [cloud_account_id, tenant]
        resource_list = database.run_query(query, values)

        on_conflict = (
            "ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id) "
            "DO UPDATE SET recommendation = EXCLUDED.recommendation, "
            "estimated_savings = EXCLUDED.estimated_savings"
        )
        resource_map = {}
        for resource in resource_list:
            resource_map[f"{resource[2]}:{resource[3]}"] = resource

        recommendations = {}

        for r in report["data"]:
            recommendation = {}
            if f"{r['container']}:{r['namespace']}" not in resource_map.keys():
                continue
            resource_id = resource_map[f"{r['container']}:{r['namespace']}"]
            rightsize = json.dumps(r["content"])
            # Calculate the original hourly cost
            cpu_cost_per_hour = resource_id[4]
            memory_cost_per_hour = resource_id[5]
            saving = 0
            original_hourly_cost = 0
            new_hourly_cost = 0
            for rec in r["content"]:
                if rec["resource"] == "cpu" and rec["allocated"]["request"]:
                    original_cpu = rec["allocated"]["request"]
                    original_hourly_cost += original_cpu * cpu_cost_per_hour
                    if rec["recommended"]["request"] and rec["recommended"]["request"] != "?":
                        new_cpu = rec["recommended"]["request"]
                        # Calculate the new hourly cost
                        new_hourly_cost += new_cpu * cpu_cost_per_hour

                if rec["resource"] == "memory" and rec["allocated"]["request"]:
                    original_memory = rec["allocated"]["request"]
                    original_hourly_cost += (original_memory / (1024 * 1024 * 1024)) * memory_cost_per_hour
                    if rec["recommended"]["request"] and rec["recommended"]["request"] != "?":
                        new_memory = rec["recommended"]["request"]
                        # Calculate the new hourly cost
                        new_hourly_cost += (new_memory / (1024 * 1024 * 1024)) * memory_cost_per_hour
            if new_hourly_cost != 0 and original_hourly_cost != 0 and new_hourly_cost < original_hourly_cost:
                # Calculate the savings
                saving = (original_hourly_cost - new_hourly_cost) * 24 * 30
            recommendation["estimated_savings"] = saving
            recommendation["cloud_account_id"] = cloud_account_id
            recommendation["tenant_id"] = tenant
            recommendation["resource_id"] = resource_id[0]
            recommendation["recommendation_action"] = "Modify"
            recommendation["category"] = "RightSizing"
            recommendation["rule_name"] = "pod_right_sizing"
            recommendation["recommendation"] = rightsize
            recommendation["severity"] = get_severity(r["priority"])
            recommendation["account_object_id"] = None
            if resource_id[0] in recommendations:
                recommendation["estimated_savings"] = saving + recommendations[resource_id[0]]["estimated_savings"]
            recommendations[resource_id[0]] = recommendation
        try:
            database.insert_data("recommendation", list(recommendations.values()), on_conflict=on_conflict)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(recommendations)}")

        score = {"score": score, "source": "krr", "cloud_account_id": cloud_account_id, "tenant": tenant}

        on_conflict = "ON CONFLICT (cloud_account_id, tenant, source) DO UPDATE SET score = (EXCLUDED.score)"
        try:
            logging.info("inserting score for account_id {}", cloud_account_id)
            database.insert_data("cloud_account_score", [score], on_conflict=on_conflict)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(score)}")

    except Exception:
        logging.exception(f"Failed to process report {json.dumps(content)}")


def format_evidence(evidence: List, config_change_event: List) -> List:
    formatted_evidence = []
    formatted_data = []
    for data in evidence:
        if "type" in data:
            formatted_evidence.append(data)
            continue
        if isinstance(data["data"], str):
            data = json.loads(data["data"])

        for item in data:
            if item["type"] == "table":
                table_data = item["data"]
                headers = table_data["headers"]
                rows = table_data["rows"]
                column_renderers = table_data.get("column_renderers", None)
                table_block = TableBlock(
                    headers=headers,
                    rows=rows,
                    column_renderers=column_renderers,
                )
                markdown_block = {"data": table_block.to_markdown(), "type": "markdown"}
                formatted_data.append(markdown_block)
            else:
                formatted_data.append(item)

    if len(config_change_event) > 0:
        formatted_data.append(json.loads(config_change_event[0][2][0]["data"])[0])
    formatted_evidence.append({"data": json.dumps(formatted_data)})
    return formatted_evidence


def get_spends_data(
    resourceId: str, tenant: str, date: datetime, cloud_id: str, k8sData: Dict[str, Any]
) -> Dict[str, Any]:
    cloud_resource_spend = {
        "date": date.strftime("%Y-%m-%d %H:%M:%S"),
        "amount": k8sData["totalCost"],
        "tenant": tenant,
        "cloud_account": cloud_id,
        "cloud_resource_id": generate_uuid_from_string(resourceId + cloud_id),
    }
    return cloud_resource_spend


def get_metric_data(
    resourceId: str, date: datetime, key: str, cloud_id: str, k8sData: Dict[str, Any], tenant_id: str
) -> Dict[str, Any]:
    cloud_resource_metric = {
        "cloud_resource_id": generate_uuid_from_string(resourceId + cloud_id),
        "timestamp": date.strftime("%Y-%m-%d %H:%M:%S"),
        "metric": str(key),
        "metric_type": "g",
        "tags": "{}",
        "value": k8sData[key],
        "cloud_account_id": cloud_id,
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

metrics_avg_on_conflict = "ON CONFLICT(metric,cloud_resource_id,timestamp) DO UPDATE SET value = (EXCLUDED.value)"

metrics_sum_on_conflict = "ON CONFLICT(metric,cloud_resource_id,timestamp) DO UPDATE SET value = (EXCLUDED.value)"

spends_on_conflict = "ON CONFLICT(tenant,cloud_account,cloud_resource_id,date) DO UPDATE SET amount = EXCLUDED.amount "


def process_node_spend(data: str, tenant: str, cloud_account_id: str) -> None:
    spend = json.loads(data)
    node_data = []
    for node_spend in spend["data"]:
        for node, values in node_spend.items():
            if node == "":
                continue
            node_data.append(values)
    spend_data = []
    metrics_sum = []
    metrics_avg = []
    for node in node_data:
        date = datetime.combine(datetime.fromisoformat(node["window"]["end"]), datetime.min.time())
        resource_id = generate_uuid_from_string(node["name"] + cloud_account_id)
        resource = database.select_data("cloud_resourses", ["id"], {"id": resource_id})
        if len(resource) == 0:
            continue
        spend_data.append(get_spends_data(node["name"], tenant, date, cloud_account_id, node))
        for key in sum_columns:
            metrics_sum.append(
                get_metric_data(
                    node["name"],
                    date,
                    key,
                    cloud_account_id,
                    node,
                    tenant,
                )
            )
        for key in avg_columns:
            metrics_avg.append(
                get_metric_data(
                    node["name"],
                    date,
                    key,
                    cloud_account_id,
                    node,
                    tenant,
                )
            )

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

        try:
            for d in spend_data:
                d["date"] = datetime.strptime(d["date"], "%Y-%m-%d %H:%M:%S")
                d["tags"] = "{}" if d["tags"] is None else d["tags"]
            clickhouse.insert_data("spends", spend_data)
        except Exception:
            logging.exception(f"Failed to insert row {json.dumps(spend_data, default=str)}")

        try:
            data_list = metrics_avg + metrics_sum
            for d in data_list:
                d["timestamp"] = datetime.strptime(d["timestamp"], "%Y-%m-%d %H:%M:%S")
                d["tags"] = "{}" if d["tags"] is None else d["tags"]
            clickhouse.insert_data("metrics", data_list)
        except Exception:
            logging.exception(
                f"Failed to insert row in clickhouse {json.dumps((metrics_avg + metrics_sum), default=str)}"
            )
