import time
from datetime import UTC, datetime
from typing import Any, Dict, List

from pydantic import BaseModel
from server.utils.utils import MetricsServerConfigs
from server.utils.utils import generate_hourly_dict

health_check_string = "|".join(MetricsServerConfigs.health_check_endpoints)

# Prometheus Query API Endpoint
PROMETHEUS_URL = "http://localhost:8429/api/v1/query_range"

# Query for replica count (modify as needed)
QUERY = "timestamp(kube_replicaset_status_replicas{ __CLUSTER__ })"

# Define time parameters
END_TIME = int(time.time())  # Current timestamp
STEP = 3600  # Step size in seconds (1 hour) for Prometheus queries
BATCH_SIZE = 10  # Number of steps per batch for fetching metrics
TRAIN_BATCH_SIZE = 3600  # Number of steps per batch
RETRY_COUNT = 5  # Number of retries for failed requests


class MetricsInput(BaseModel):
    metric: Dict[str, Any]
    timestamps: List[int]
    values: List[str]

    def __init__(self, **data: Any):
        data["timestamps"] = list(map(int, data["timestamps"]))
        super().__init__(**data)


class PrometheusQuery:
    def __init__(self, namespace, deployment_name) -> None:
        self.namespace = namespace
        self.deployment_name = deployment_name
        self.time_range = MetricsServerConfigs.time_range

    def get_data(
        self, data: List[MetricsInput], start_time: datetime, end_time: datetime, duration_in_days: int | None = None
    ):
        times: Dict[datetime, Any] = generate_hourly_dict(
            start_time=start_time, end_time=end_time, default_value=0, duration_in_days=duration_in_days
        )
        for dat in data:
            timestamps: List[int] = list(map(int, dat.timestamps))
            values = dat.values
            for _d in [(timestamps[i], values[i]) for i in range(min(len(timestamps), len(values)))]:
                times[datetime.fromtimestamp(_d[0], tz=UTC)] = _d[1]
        return [[i, times[i]] for i in times]

    def get_query(self) -> str:
        raise NotImplementedError


class ReplicasCount(PrometheusQuery):
    def get_query(self) -> str:
        replicas_count = (
            f'kube_replicaset_status_replicas{{  __CLUSTER__  replicaset=~"{self.deployment_name}-.*", '
            f'namespace="{self.namespace}"}}'
        )
        return replicas_count

    def get_data(
        self, data: List[MetricsInput], start_time: datetime, end_time: datetime, duration_in_days: int | None = None
    ):
        times: Dict[datetime, Any] = generate_hourly_dict(
            start_time=start_time, end_time=end_time, default_value=0, duration_in_days=duration_in_days
        )
        for dat in data:
            timestamps: List[int] = list(map(int, dat.timestamps))
            values = dat.values
            for _d in [(timestamps[i], values[i]) for i in range(min(len(timestamps), len(values)))]:
                timestamp = datetime.fromtimestamp(_d[0], tz=UTC)
                times[timestamp] = times.get(timestamp, 0) + int(_d[1])
        return [[i, times[i]] for i in times]


class RequestCount(PrometheusQuery):
    def get_query(self) -> str:
        request_count = (
            f"round(sum(avg_over_time(container_http_requests_total{{  __CLUSTER__  destination_workload_name="
            f'"{self.deployment_name}", path!~"{health_check_string}"}}[{self.time_range}])) '
            f"by (destination_workload_name))"
        )
        return request_count


class RequestDuration(PrometheusQuery):
    def get_query(self) -> str:
        request_duration = (
            f"round(sum(rate(container_http_requests_duration_seconds_total_sum{{ __CLUSTER__ "
            "destination_workload_name="
            f'"{self.deployment_name}", path!~"{health_check_string}"}}[{self.time_range}])>=0 /'
            f"rate(container_http_requests_duration_seconds_total_count{{ __CLUSTER__ destination_workload_name=~"
            f'"{self.deployment_name}", path!~"{health_check_string}"}}[{self.time_range}])>=0) '
            f"by (destination_workload_name),0.001)"
        )
        return request_duration


class MemUsage(PrometheusQuery):
    def get_query(self) -> str:
        mem_usage = (
            f'round(sum((avg_over_time(container_memory_working_set_bytes{{  __CLUSTER__  namespace="{self.namespace}",'
            f'pod=~"{self.deployment_name}.*"}}['
            f"{self.time_range}])/1024)/1024) by (service),0.01)"
        )
        return mem_usage


class CpuUsage(PrometheusQuery):
    def get_query(self) -> str:
        cpu_usage = (
            f'round(sum(rate(container_cpu_usage_seconds_total{{  __CLUSTER__  namespace="{self.namespace}",'
            f'pod=~"{self.deployment_name}.*"}}[{self.time_range}])) by (service),0.0001)'
        )
        return cpu_usage
