from __future__ import annotations

import logging
import asyncio
from datetime import datetime, timedelta
from typing import Dict, Any
from concurrent.futures import ThreadPoolExecutor

import numpy as np
import requests

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.strategy.strategies import BaseStrategy
from server.recommendation.vertical_rightsizing.models.result import PodsTimeData
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService

logger = logging.getLogger("krr")

# Datadog metric names
DD_CPU_METRIC = "kubernetes.cpu.usage.total"
DD_MEMORY_METRIC = "kubernetes.memory.working_set"

# History window
DD_HISTORY_DAYS = 14

# Kind-to-tag mapping for Datadog
KIND_TO_TAG: Dict[str, str] = {
    "Deployment": "kube_deployment",
    "StatefulSet": "kube_stateful_set",
    "DaemonSet": "kube_daemon_set",
    "Job": "kube_job",
    "CronJob": "kube_cron_job",
}


class DatadogMetricsService(MetricsService):
    """
    A class for fetching metrics from Datadog HTTP API.
    """

    def __init__(
        self,
        config: Config,
        account_id: str,
        executor: ThreadPoolExecutor,
        api_key: str,
        app_key: str,
        site: str,
    ) -> None:
        super().__init__(config, account_id, executor)
        self.api_key = api_key
        self.app_key = app_key
        self.site = site
        self._base_url = f"https://{site}/api/v1/query"
        self._headers = {
            "DD-API-KEY": api_key,
            "DD-APPLICATION-KEY": app_key,
        }

    def check_connection(self):
        """Verify Datadog API connectivity."""
        try:
            now = datetime.utcnow()
            resp = requests.get(
                self._base_url,
                headers=self._headers,
                params={
                    "from": int((now - timedelta(minutes=5)).timestamp()),
                    "to": int(now.timestamp()),
                    "query": "avg:system.cpu.idle{*}",
                },
                timeout=10,
            )
            resp.raise_for_status()
        except Exception as e:
            raise ConnectionError(f"Datadog API connection failed: {e}") from e

    def _query_datadog(self, query: str, from_ts: int, to_ts: int) -> dict:
        """Execute a Datadog metrics query and return raw response JSON."""
        resp = requests.get(
            self._base_url,
            headers=self._headers,
            params={
                "from": from_ts,
                "to": to_ts,
                "query": query,
            },
            timeout=60,
        )
        resp.raise_for_status()
        return resp.json()

    async def _async_query_datadog(self, query: str, from_ts: int, to_ts: int) -> dict:
        """Async wrapper around _query_datadog using the thread pool executor."""
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(
            self.executor,
            lambda: self._query_datadog(query, from_ts, to_ts),
        )

    def _build_query(self, agg: str, metric: str, namespace: str, kind: str, name: str) -> str:
        """Build a Datadog query string grouped by pod_name and kube_container_name."""
        tag_key = KIND_TO_TAG.get(kind, "kube_deployment")
        return f"{agg}:{metric}{{kube_namespace:{namespace},{tag_key}:{name}}}" f" by {{pod_name,kube_container_name}}"

    def _parse_series_by_pod_container(self, data: dict) -> Dict[str, Dict[str, list]]:
        """
        Parse Datadog response into nested dict: container -> pod -> [values].

        Each series in Datadog response has a scope like
        'pod_name:xxx,kube_container_name:yyy' and a pointlist.
        """
        result: Dict[str, Dict[str, list]] = {}
        for series in data.get("series", []):
            scope = series.get("scope", "")
            tag_map = {}
            for part in scope.split(","):
                part = part.strip()
                if ":" in part:
                    k, v = part.split(":", 1)
                    tag_map[k] = v

            pod_name = tag_map.get("pod_name", "")
            container_name = tag_map.get("kube_container_name", "")
            if not pod_name or not container_name:
                continue

            if container_name not in result:
                result[container_name] = {}
            if pod_name not in result[container_name]:
                result[container_name][pod_name] = []

            for point in series.get("pointlist", []):
                if len(point) >= 2 and point[1] is not None:
                    result[container_name][pod_name].append(
                        (point[0] / 1000.0, point[1])  # Datadog timestamps are in ms
                    )
        return result

    async def get_cluster_summary(self) -> Dict[str, Any]:
        """Datadog doesn't provide cluster-level summary the same way Prometheus does."""
        return {}

    async def load_pods(self, object: K8sObjectData, period: timedelta) -> list[PodData]:
        """
        Load pod names from Datadog metric series.

        We query a lightweight metric to discover pod names associated with the workload.
        """
        now = datetime.utcnow()
        from_ts = int((now - period).timestamp())
        to_ts = int(now.timestamp())

        tag_key = KIND_TO_TAG.get(object.kind, "kube_deployment")
        query = f"avg:{DD_CPU_METRIC}{{kube_namespace:{object.namespace}," f"{tag_key}:{object.name}}} by {{pod_name}}"

        try:
            data = await self._async_query_datadog(query, from_ts, to_ts)
        except Exception as e:
            logger.warning(f"Datadog load_pods query failed for {object}: {e}")
            return []

        pod_names = set()
        for series in data.get("series", []):
            scope = series.get("scope", "")
            for part in scope.split(","):
                part = part.strip()
                if part.startswith("pod_name:"):
                    pod_names.add(part.split(":", 1)[1])
        return [PodData(name=name, deleted=False) for name in pod_names]

    async def gather_data(
        self,
        object: K8sObjectData,
        strategy: BaseStrategy,
        period: timedelta,
        *,
        step: timedelta = timedelta(minutes=30),
    ) -> Dict[str, PodsTimeData]:
        """
        Gather all metric data from Datadog for a given K8s object.

        Returns the same MetricsPodData format the NudgebeeStrategy expects:
        {
            "cpu_percentile_99": {pod_name: np.array([[ts, val], ...])},
            "cpu_percentile_97": ...,
            "cpu_percentile_95": ...,
            "cpu_percentile_92": ...,
            "MaxMemoryLoader":   {pod_name: np.array([[ts, val], ...])},
            "CPUAmountLoader":   {pod_name: np.array([[ts, count], ...])},
            "MemoryAmountLoader": {pod_name: np.array([[ts, count], ...])},
            "MaxOOMKilledMemoryLoader": {},  # Not available from Datadog
        }
        """
        now = datetime.utcnow()
        from_ts = int((now - timedelta(days=DD_HISTORY_DAYS)).timestamp())
        to_ts = int(now.timestamp())

        cpu_query = self._build_query("avg", DD_CPU_METRIC, object.namespace, object.kind, object.name)
        mem_query = self._build_query("max", DD_MEMORY_METRIC, object.namespace, object.kind, object.name)

        # Fetch CPU and Memory in parallel
        try:
            cpu_data, mem_data = await asyncio.gather(
                self._async_query_datadog(cpu_query, from_ts, to_ts),
                self._async_query_datadog(mem_query, from_ts, to_ts),
            )
        except Exception as e:
            logger.error(f"Datadog query failed for {object}: {e}")
            return {key: {} for key in strategy.metrics}

        # Parse raw series grouped by container and pod
        cpu_by_container = self._parse_series_by_pod_container(cpu_data)
        mem_by_container = self._parse_series_by_pod_container(mem_data)

        # Filter to the specific container we're computing for
        cpu_pods = cpu_by_container.get(object.container, {})
        mem_pods = mem_by_container.get(object.container, {})

        # Build the result dict matching the strategy's expected metric keys
        result: Dict[str, PodsTimeData] = {}

        for metric_name in strategy.metrics:
            if metric_name.startswith("cpu_percentile_"):
                percentile = float(metric_name.replace("cpu_percentile_", ""))
                pods_data: PodsTimeData = {}
                for pod_name, points in cpu_pods.items():
                    if len(points) == 0:
                        continue
                    arr = np.array(points, dtype=np.float64)
                    # Datadog kubernetes.cpu.usage.total is in nanocores -> convert to cores
                    cpu_values = arr[:, 1] / 1e9
                    p_val = float(np.percentile(cpu_values, percentile))
                    # Return as single-row [[timestamp, percentile_value]]
                    pods_data[pod_name] = np.array([[arr[-1, 0], p_val]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "MaxMemoryLoader":
                pods_data = {}
                for pod_name, points in mem_pods.items():
                    if len(points) == 0:
                        continue
                    arr = np.array(points, dtype=np.float64)
                    max_val = float(np.max(arr[:, 1]))
                    # Return as single-row [[timestamp, max_memory_bytes]]
                    pods_data[pod_name] = np.array([[arr[-1, 0], max_val]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "CPUAmountLoader":
                pods_data = {}
                for pod_name, points in cpu_pods.items():
                    if len(points) == 0:
                        continue
                    arr = np.array(points, dtype=np.float64)
                    count = float(len(arr))
                    pods_data[pod_name] = np.array([[arr[-1, 0], count]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "MemoryAmountLoader":
                pods_data = {}
                for pod_name, points in mem_pods.items():
                    if len(points) == 0:
                        continue
                    arr = np.array(points, dtype=np.float64)
                    count = float(len(arr))
                    pods_data[pod_name] = np.array([[arr[-1, 0], count]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "MaxOOMKilledMemoryLoader":
                # OOM kill data is not available from Datadog metrics API
                # The strategy handles empty dicts gracefully
                result[metric_name] = {}

            else:
                logger.warning(f"Unknown metric key '{metric_name}' requested by strategy, returning empty")
                result[metric_name] = {}

        return result
