import json
import logging
import time
from datetime import UTC, datetime, timedelta, timezone
from typing import Any, Dict, List, Optional

import pandas as pd
from opentelemetry import context
from opentelemetry.propagate import inject

from server.metrics.metrics import Metrics
from server.metrics.prometheus_queries import (
    CpuUsage,
    MemUsage,
    MetricsInput,
    ReplicasCount,
    RequestCount,
    RequestDuration,
)
from server.utils.utils import (
    MetricsServerConfigs,
    generate_hourly_dict,
    get_http_session_with_retry,
    get_prediction_time_range,
    get_trace,
)

logger = logging.getLogger(__name__)
BATCH_SIZE = 120
# Number of steps per batch
RETRY_COUNT = 5
END_TIME = int(time.time())  # Current timestamp
STEP = 3600  # Step size in seconds (e.g., 5 minutes)


headers = {"Content-Type": "application/json", "X-SECRET-KEY": MetricsServerConfigs.secret}


def operate_data(
    data: List[MetricsInput], end_time: datetime, start_time: datetime, duration_in_days: int | None = None
) -> List[List[Any]]:
    times: Dict[int, Any] = generate_hourly_dict(
        start_time=start_time, end_time=end_time, default_value=0, duration_in_days=duration_in_days
    )
    for dat in data:
        timestamps: List[int] = list(map(int, dat.timestamps))
        values = dat.values
        for _d in [(timestamps[i], values[i]) for i in range(min(len(timestamps), len(values)))]:
            times[_d[0]] = _d[1]
    return [[i, times[i]] for i in times]


def merge_lists(lists) -> list:
    with get_trace(__name__).start_as_current_span("merge_lists"):
        outer_lists = [lst for lst in lists]
        dicts = [{item[0]: item[1] for item in outer_list} for outer_list in outer_lists]
        all_keys = set(key for d in dicts for key in d)
        merged_dict = {key: [d.get(key, 0) for d in dicts] for key in all_keys}
        merged_list = [[key] + values for key, values in merged_dict.items()]
        return merged_list


def merge_dicts(dicts) -> list:
    with get_trace(__name__).start_as_current_span("merge_lists"):
        merged_list = []
        all_keys = set(d for d in dicts[0])
        for dict_ in dicts:
            merged_list.extend([dict_[key] for key in all_keys])
        return merged_list


class PrometheusMetricsHPA(Metrics):
    def __init__(
        self,
        deployment_name: Optional[str],
        namespace_name: Optional[str],
        account_id: str,
        from_time: datetime | None = None,
        end_time: datetime = datetime.now(UTC),
    ):
        self.prometheus_url = MetricsServerConfigs.url
        self.time_range = get_prediction_time_range(account_id)
        self.namespace_name = namespace_name
        self.deployment_name = deployment_name
        self.account_id = account_id
        # If None the the data will come till data is available
        self.start_time = from_time
        self.end_time = end_time

    @staticmethod
    def parse_evidence(evidence):
        if "data" in evidence:
            findings = evidence["data"]
            if "findings" in findings and len(findings["findings"]) > 0:
                first_finding = findings["findings"][0]
                if "evidence" in first_finding and len(first_finding["evidence"]) > 0:
                    first_evidence = first_finding["evidence"][0]
                    if "data" in first_evidence:
                        data = json.loads(first_evidence["data"])
                    else:
                        logger.error(f"Failed to fetch metrics from server. evidence: {first_evidence}")
                        raise ValueError("Failed to fetch metrics from server. response data evidence is empty")
                else:
                    logger.error(f"Failed to fetch metrics from server. evidence: {first_finding}")
                    raise ValueError("Failed to fetch metrics from server. response data findings is empty")
            else:
                logger.error(f"Failed to fetch metrics from server. findings: {findings}")
                raise ValueError("Failed to fetch metrics from server. response data findings is empty")
        else:
            logger.error(f"Failed to fetch metrics from server. response: {evidence}")
            raise ValueError("Failed to fetch metrics from server. response data is empty")
        if data and len(data) > 0 and "data" in data[0] and "series_list_result" in data[0]["data"]:
            data_list: List[MetricsInput] = [MetricsInput(**i) for i in data[0]["data"]["series_list_result"]]
        else:
            logger.error(f"Failed to fetch metrics from server. data: {data}")
            raise ValueError("Failed to fetch metrics from server. data list is empty")
        return data_list

    def get_prometheus_payload(self, start_at: datetime, end_time: datetime, query: str):
        return {
            "no_sinks": True,
            "body": {
                "account_id": self.account_id,
                "action_name": "prometheus_enricher",
                "action_params": {
                    "promql_query": query,
                    "duration": {
                        "starts_at": start_at.strftime("%Y-%m-%d %H:%M:%S UTC"),
                        "ends_at": end_time.strftime("%Y-%m-%d %H:%M:%S UTC"),
                    },
                    "step": STEP,
                },
            },
            "cache": False,
        }

    def fetch_metrics(
        self,
        queries_obj: list,
    ) -> list:
        with get_trace(__name__).start_as_current_span("fetch_metrics"):
            try:
                start_time = None
                if self.start_time is not None:
                    start_time = self.start_time.replace(tzinfo=timezone.utc, minute=0, second=0, microsecond=0)
                end_time = self.end_time.replace(tzinfo=timezone.utc, minute=0, second=0, microsecond=0)

                headers_with_context = headers.copy()
                current_context = context.get_current()
                if current_context:
                    inject(headers_with_context, current_context)
                main_data = []
                logger.info(f"executing {len(queries_obj)} queries")
                for query_obj in queries_obj:

                    current_end_time = end_time
                    current_start_time = current_end_time - timedelta(seconds=STEP * BATCH_SIZE)
                    results = []
                    while True:
                        if isinstance(query_obj, str):
                            query = query_obj
                        else:
                            query = query_obj.get_query()
                        logger.info(
                            (
                                f"fetching metrics for query : {query} for "
                                f"start_time: {current_start_time} and end_time: {current_end_time}"
                            )
                        )
                        if start_time and start_time > current_start_time:
                            # get data till start_time only
                            current_start_time = start_time
                        if current_end_time == start_time:
                            break
                        payload = json.dumps(
                            self.get_prometheus_payload(
                                start_at=current_start_time, end_time=current_end_time, query=query
                            )
                        )
                        response = get_http_session_with_retry().request(
                            "POST", self.prometheus_url, headers=headers_with_context, data=payload, timeout=10
                        )
                        if response.status_code != 200:
                            msg = f"""Failed to fetch metrics from server.
                            status: {response.status_code}:
                            response: {response.text}"""
                            logger.error(msg)
                            raise ValueError(msg)
                        data = response.json()
                        data_list = self.parse_evidence(evidence=data)
                        if not data_list:
                            break
                        else:
                            if isinstance(query_obj, str):
                                r_list = operate_data(
                                    data=data_list, end_time=current_end_time, start_time=current_start_time
                                )
                            else:
                                r_list = query_obj.get_data(
                                    data=data_list, start_time=current_start_time, end_time=current_end_time
                                )
                            logger.info(f"length  of r_list query : {query} : {len(r_list)}")
                            results.extend([(k, v) for k, v in r_list])
                        # Move to the next batch (older data)
                        current_end_time = current_start_time
                        current_start_time = current_start_time - timedelta(seconds=STEP * BATCH_SIZE)
                    main_data.append(results)
                return merge_lists(main_data)

            except Exception as e:
                msg = f"Fetch metrics failed :{e}"
                logger.exception(msg)
                raise ValueError(msg)
            else:
                return results

    def get_metrics(self) -> pd.DataFrame:
        with get_trace(__name__).start_as_current_span("get_metrics"):
            try:
                logger.info(f"Fetching metrics with details : {self}")
                replicas_count_obj = ReplicasCount(namespace=self.namespace_name, deployment_name=self.deployment_name)
                request_count_obj = RequestCount(namespace=self.namespace_name, deployment_name=self.deployment_name)
                request_duration_obj = RequestDuration(
                    namespace=self.namespace_name, deployment_name=self.deployment_name
                )
                mem_usage_obj = MemUsage(namespace=self.namespace_name, deployment_name=self.deployment_name)
                cpu_usage_obj = CpuUsage(namespace=self.namespace_name, deployment_name=self.deployment_name)
                queries = [replicas_count_obj, request_count_obj, request_duration_obj, mem_usage_obj, cpu_usage_obj]

                results = self.fetch_metrics(
                    queries_obj=queries,
                )

                cols = ["timestamp", "replicas", "rps", "latency", "memory", "cpu"]
                df = pd.DataFrame(results, columns=cols)
                df.sort_values(by="timestamp", inplace=True)
                # df.to_csv("output_input.csv", index=False, encoding="utf-8")
                logger.info(f"length of df: {len(df)}")
                return df
            except Exception as e:
                msg = f"Failed to fetch metrics: {e}"
                logger.exception(msg)
                raise ValueError(msg)
