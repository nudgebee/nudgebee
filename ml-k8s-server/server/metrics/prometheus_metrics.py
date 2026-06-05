import json
import logging
from datetime import UTC, datetime, timedelta, timezone
from typing import Any, Dict, List, Optional, cast

import pandas as pd
import requests
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
    get_http_session_with_retry,
    get_prediction_duration,
    get_prediction_time_range,
    get_trace,
)

from opentelemetry import context
from opentelemetry.propagate import inject

logger = logging.getLogger(__name__)

headers = {"Content-Type": "application/json", "X-SECRET-KEY": MetricsServerConfigs.secret}


def operate_data(data: List[MetricsInput], end_time: datetime, duration_in_days: int):
    times: Dict[int, Any] = {int((end_time - timedelta(hours=i)).timestamp()): 0 for i in range(duration_in_days * 24)}
    for dat in data:
        timestamps: List[int] = list(map(int, dat.timestamps))
        values = dat.values
        for _d in [(timestamps[i], values[i]) for i in range(min(len(timestamps), len(values)))]:
            times[_d[0]] = _d[1]
    return [[i, times[i]] for i in times]


# flake8: noqa: C901
def fetch_metrics(
    endpoint: str,
    queries_obj: list,
    start_time: datetime,
    end_time: datetime,
    duration: Optional[int],
    step: str,
    account_id: str,
    instant: bool = False,
    raw: bool = False,
) -> list:
    with get_trace(__name__).start_as_current_span("fetch_metrics"):
        try:
            results = []
            start_time = start_time.replace(tzinfo=timezone.utc)
            start_time_str = start_time.strftime("%Y-%m-%d %H:%M:%S UTC")
            end_time = end_time.replace(tzinfo=timezone.utc)
            end_time_str = end_time.strftime("%Y-%m-%d %H:%M:%S UTC")

            headers_with_context = headers.copy()
            current_context = context.get_current()
            if current_context:
                inject(headers_with_context, current_context)

            for query_obj in queries_obj:
                if isinstance(query_obj, str):
                    query = query_obj
                else:
                    query = query_obj.get_query()
                # Reduce log verbosity for metrics queries
                # logger.debug(f"fetching metrics for query : {query}")
                payload = json.dumps(
                    {
                        "no_sinks": True,
                        "body": {
                            "account_id": str(account_id),
                            "action_name": "prometheus_enricher",
                            "action_params": {
                                "promql_query": query,
                                "duration": {"starts_at": start_time_str, "ends_at": end_time_str},
                                "step": step,
                                "instant": instant,
                            },
                        },
                        "cache": False,
                    }
                )
                response = get_http_session_with_retry(retry_attempts=0).request(
                    "POST", endpoint, headers=headers_with_context, data=payload, timeout=60
                )
                if response.status_code != 200:
                    if response.status_code == 400 and "agent not connected" in response.text:
                        logger.warning(f"Agent not connected for account {account_id}. Returning empty results.")
                        return []
                    msg = f"""Failed to fetch metrics from server.
                     status: {response.status_code}:
                     response: {response.text}"""
                    logger.error(msg)
                    raise ValueError(msg)
                data = response.json()
                if "data" in data:
                    findings = data["data"]
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
                        logger.error(
                            f"Failed to fetch metrics from server. findings: {findings}, query: {query}"
                            f"query_obj: {query_obj}, data: {data}"
                        )
                        raise ValueError("Failed to fetch metrics from server. response data findings is empty")
                else:
                    if raw:
                        return []
                    logger.error(f"Failed to fetch metrics from server. response: {response}")
                    raise ValueError("Failed to fetch metrics from server. response data is empty")

                data_list = []
                if data and len(data) > 0 and "data" in data[0] and isinstance(data[0]["data"], str):
                    raise Exception("prometheus query error: " + data[0]["data"])
                if (
                    data
                    and len(data) > 0
                    and "data" in data[0]
                    and data[0]["data"]["result_type"] == "vector"
                    and "vector_result" in data[0]["data"]
                ):
                    data_list = data[0]["data"]["vector_result"]
                    if raw:
                        return data_list
                elif data and len(data) > 0 and "data" in data[0] and "series_list_result" in data[0]["data"]:
                    if raw:
                        return cast(list, data[0]["data"]["series_list_result"])
                    data_list = [MetricsInput(**i) for i in data[0]["data"]["series_list_result"]]
                else:
                    if raw:
                        return []
                    logger.error(f"Failed to fetch metrics from server. data: {data}")
                    raise ValueError("Failed to fetch metrics from server. data list is empty")

                if duration is None:
                    results.extend(data_list)
                else:
                    if isinstance(query_obj, str):
                        r_list = operate_data(data=data_list, end_time=end_time, duration_in_days=duration)
                    else:
                        r_list = query_obj.get_data(data=data_list, end_time=end_time, duration_in_days=duration)
                    logger.debug(f"length of r_list query : {query} : {len(r_list)}")
                    results.append(r_list)
        except Exception as e:
            if raw:
                return []
            msg = f"Fetch metrics failed :{e}"
            logger.error(msg)
            raise ValueError(msg)
        if duration is not None:
            return merge_lists(results)
        else:
            return results


def _parse_prometheus_response(response_json: dict, promql_query: str, query_type: str = "query") -> List[dict]:
    """
    Parse common Prometheus response format from relay server.

    Args:
        response_json: The JSON response from the relay server
        promql_query: The PromQL query string for logging purposes
        query_type: Query type for logging ('query', 'instant query', etc.)

    Returns:
        List[dict]: List of Prometheus metric series
    """
    if "data" not in response_json:
        logger.warning(f"No 'data' field in response for {query_type}: {promql_query[:100]}...")
        return []

    if "findings" not in response_json["data"] or not response_json["data"]["findings"]:
        logger.warning(f"No 'findings' in response for {query_type}: {promql_query[:100]}...")
        return []

    if "evidence" not in response_json["data"]["findings"][0] or not response_json["data"]["findings"][0]["evidence"]:
        logger.warning(f"No 'evidence' in findings for {query_type}: {promql_query[:100]}...")
        return []

    if "data" not in response_json["data"]["findings"][0]["evidence"][0]:
        logger.warning(f"No 'data' in evidence for {query_type}: {promql_query[:100]}...")
        return []

    data_string = response_json["data"]["findings"][0]["evidence"][0]["data"]
    parsed_data = json.loads(data_string)

    # Handle the response format: it's a list with the actual data inside
    if isinstance(parsed_data, list) and len(parsed_data) > 0:
        prometheus_result = parsed_data[0]
        if "data" in prometheus_result and "series_list_result" in prometheus_result["data"]:
            series_list = prometheus_result["data"]["series_list_result"]
            logger.debug(f"Successfully parsed {len(series_list)} series for {query_type}: {promql_query[:100]}...")
            return series_list  # type: ignore
        else:
            logger.warning(
                f"Missing 'data.series_list_result' in parsed response for {query_type}: {promql_query[:100]}..."
            )
    else:
        logger.warning(f"Expected list response but got {type(parsed_data)} for {query_type}: {promql_query[:100]}...")

    return []


def make_prometheus_request(endpoint, promql_query, account_id):
    """
    Legacy function for backward compatibility with hardcoded 1-minute duration.

    DEPRECATED: Use prometheus_instant_query() or prometheus_range_query() instead
    for better control over query duration and type.
    """
    payload = json.dumps(
        {
            "no_sinks": True,
            "body": {
                "account_id": account_id,
                "action_name": "prometheus_enricher",
                "action_params": {"promql_query": promql_query, "duration": {"duration_minutes": 1}},
            },
            "cache": False,
        }
    )
    headers_with_context = headers.copy()
    current_context = context.get_current()
    if current_context:
        inject(headers_with_context, current_context)

    try:
        response = requests.post(endpoint, headers=headers_with_context, data=payload, timeout=10)
        response.raise_for_status()
        data = response.json()["data"]["findings"][0]["evidence"][0]["data"]
        return json.loads(data)[0]["data"]["series_list_result"][0]["metric"]
    except requests.exceptions.RequestException as e:
        msg = f"Failed to make request: {promql_query}, Error: {e}"
        logger.error(msg)
        raise ValueError(msg)


def prometheus_instant_query(endpoint: str, promql_query: str, account_id: str) -> List[dict]:
    """
    Execute an instant Prometheus query (single point in time).

    Args:
        endpoint: Prometheus endpoint URL
        promql_query: PromQL query string
        account_id: Account ID for the query

    Returns:
        List[dict]: List of Prometheus metric series with timestamps and values

    Raises:
        ValueError: If the request fails
    """
    payload = json.dumps(
        {
            "no_sinks": True,
            "body": {
                "account_id": account_id,
                "action_name": "prometheus_enricher",
                "action_params": {"promql_query": promql_query, "duration": {"duration_minutes": 1}, "instant": True},
            },
            "cache": False,
        }
    )
    headers_with_context = headers.copy()
    current_context = context.get_current()
    if current_context:
        inject(headers_with_context, current_context)

    try:
        response = requests.post(endpoint, headers=headers_with_context, data=payload, timeout=10)
        if response.status_code != 200:
            if response.status_code == 400 and "agent not connected" in response.text:
                logger.warning(f"Agent not connected for account {account_id}. Returning empty results.")
                return []
            msg = f"Failed to fetch metrics from server. status: {response.status_code}: response: {response.text}"
            logger.error(msg)
            raise ValueError(msg)

        response_json = response.json()
        logger.debug(f"Raw response for instant query: {promql_query[:100]}... - Status: {response.status_code}")

        return _parse_prometheus_response(response_json, promql_query, "instant query")
    except (KeyError, IndexError, json.JSONDecodeError) as e:
        msg = f"Failed to parse Prometheus response for instant query: {promql_query[:100]}..., Error: {e}"
        logger.error(msg)
        return []  # Return empty list instead of raising to make it more resilient
    except requests.exceptions.RequestException as e:
        msg = f"Failed to make instant Prometheus request: {promql_query[:100]}..., Error: {e}"
        logger.error(msg)
        raise ValueError(msg)


def prometheus_range_query(
    endpoint: str,
    promql_query: str,
    account_id: str,
    duration_minutes: Optional[int] = None,
    start_time: Optional[str] = None,
    end_time: Optional[str] = None,
    step: str = "1m",
) -> List[dict]:
    """
    Execute a Prometheus range query (time series data over a period).

    Args:
        endpoint: Prometheus endpoint URL
        promql_query: PromQL query string
        account_id: Account ID for the query
        duration_minutes: Query duration in minutes (alternative to duration_days)
        duration_days: Query duration in days (alternative to duration_minutes)
        start_time: Start time in "YYYY-MM-DD HH:MM:SS UTC" format (alternative to duration)
        end_time: End time in "YYYY-MM-DD HH:MM:SS UTC" format (requires start_time)
        step: Query resolution step (default "1m")

    Returns:
        List[dict]: List of Prometheus metric series with timestamps and values

    Raises:
        ValueError: If the request fails or invalid parameters are provided
    """
    # Build duration parameters
    duration_params: dict[str, str | int]
    if start_time and end_time:
        duration_params = {"starts_at": start_time, "ends_at": end_time}
    elif duration_minutes:
        duration_params = {"duration_minutes": duration_minutes}
    else:
        # Default to 1 minute for backward compatibility
        duration_params = {"duration_minutes": 1}

    payload = json.dumps(
        {
            "no_sinks": True,
            "body": {
                "account_id": account_id,
                "action_name": "prometheus_enricher",
                "action_params": {
                    "promql_query": promql_query,
                    "duration": duration_params,
                    "step": step,
                    "instant": False,
                },
            },
            "cache": False,
        }
    )
    headers_with_context = headers.copy()
    current_context = context.get_current()
    if current_context:
        inject(headers_with_context, current_context)

    try:
        response = requests.post(endpoint, headers=headers_with_context, data=payload, timeout=60)
        if response.status_code != 200:
            if response.status_code == 400 and "agent not connected" in response.text:
                logger.warning(f"Agent not connected for account {account_id}. Returning empty results.")
                return []
            msg = f"Failed to fetch metrics from server. status: {response.status_code}: response: {response.text}"
            logger.error(msg)
            raise ValueError(msg)

        response_json = response.json()
        logger.debug(f"Raw response for query: {promql_query[:100]}... - Status: {response.status_code}")

        return _parse_prometheus_response(response_json, promql_query, "range query")
    except (KeyError, IndexError, json.JSONDecodeError) as e:
        msg = f"Failed to parse Prometheus response for query: {promql_query[:100]}..., Error: {e}"
        logger.error(msg)
        return []  # Return empty list instead of raising to make it more resilient
    except requests.exceptions.RequestException as e:
        msg = f"Failed to make range Prometheus request: {promql_query[:100]}..., Error: {e}"
        logger.error(msg)
        raise ValueError(msg)


def merge_lists(*lists) -> list:
    with get_trace(__name__).start_as_current_span("merge_lists"):
        outer_lists = [items for lst in lists for items in lst]
        dicts = [{item[0]: item[1] for item in outer_list} for outer_list in outer_lists]
        all_keys = set(key for d in dicts for key in d)
        merged_dict = {key: [d.get(key, None) for d in dicts] for key in all_keys}
        merged_list = [[key] + values for key, values in merged_dict.items()]
        return merged_list


class Prometheus(Metrics):
    def __init__(self, deployment_name: Optional[str], namespace_name: Optional[str], account_id: str):
        self.prometheus_url = MetricsServerConfigs.url
        self.duration = get_prediction_duration(account_id)
        self.time_range = get_prediction_time_range(account_id)
        self.namespace_name = namespace_name
        self.deployment_name = deployment_name
        self.account_id = account_id

    def get_metrics(self) -> pd.DataFrame:
        with get_trace(__name__).start_as_current_span("get_metrics"):
            try:
                logger.debug(f"Fetching metrics with details : {self}")
                replicas_count_obj = ReplicasCount(namespace=self.namespace_name, deployment_name=self.deployment_name)
                request_count_obj = RequestCount(namespace=self.namespace_name, deployment_name=self.deployment_name)
                request_duration_obj = RequestDuration(
                    namespace=self.namespace_name, deployment_name=self.deployment_name
                )
                mem_usage_obj = MemUsage(namespace=self.namespace_name, deployment_name=self.deployment_name)
                cpu_usage_obj = CpuUsage(namespace=self.namespace_name, deployment_name=self.deployment_name)
                queries = [replicas_count_obj, request_count_obj, request_duration_obj, mem_usage_obj, cpu_usage_obj]

                end_time = datetime.now(UTC)
                start_time = end_time - timedelta(days=self.duration)

                results = fetch_metrics(
                    endpoint=self.prometheus_url,
                    queries_obj=queries,
                    start_time=start_time,
                    end_time=end_time,
                    step=self.time_range,
                    account_id=self.account_id,
                    duration=self.duration,
                )

                cols = ["timestamp", "replicas", "rps", "latency", "memory", "cpu"]
                df = pd.DataFrame(results, columns=cols)
                df.sort_values(by="timestamp", inplace=True)
                # df.to_csv("output_input.csv", index=False, encoding="utf-8")
                logger.info(f"length of df: {len(df)}")
                return df
            except Exception as e:
                msg = f"Failed to fetch metrics: {e}"
                logger.error(msg)
                raise ValueError(msg)

    def query_metrics(
        self,
        query: str,
        start_time: datetime,
        end_time: datetime,
        step: str = "",
        instant: bool = False,
        raw: bool = False,
    ) -> list:
        results = fetch_metrics(
            endpoint=self.prometheus_url,
            queries_obj=[query],
            start_time=start_time,
            end_time=end_time,
            step=step,
            account_id=self.account_id,
            duration=self.duration,
            instant=instant,
            raw=raw,
        )
        return results

    def check_prometheus_connection(self):
        # do nothing
        pass

    def custom_query(self, query: str, start_time: Optional[datetime] = None, end_time: Optional[datetime] = None):
        if end_time is None:
            end_time = datetime.now(UTC)
        if start_time is None:
            start_time = datetime.now(UTC) - timedelta(days=14)
        response = self.query_metrics(query, start_time, end_time, "", True, raw=True)
        if len(response) > 0:
            return [
                {
                    "metric": r["metric"],
                    "value": [r["value"]["timestamp"], r["value"]["value"]],
                }
                for r in response
            ]
        return response

    def custom_query_range(self, query: str, start_time: datetime, end_time: datetime, step: str):
        response = self.query_metrics(query, start_time, end_time, step, raw=True)
        return response
