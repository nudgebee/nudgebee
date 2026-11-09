from __future__ import annotations

import logging
import asyncio
from datetime import datetime, timedelta, timezone
from typing import Dict, List, Optional
from concurrent.futures import ThreadPoolExecutor

import numpy as np

from server.metrics.api_server_metrics import METRIC_PROVIDER_DATADOG, query_metrics_via_api_server

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.strategy.strategies import BaseStrategy
from server.recommendation.vertical_rightsizing.models.result import PodsTimeData
from server.utils.utils import safe_get
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService

logger = logging.getLogger("rightsizing")


def pods_from_series(entries: List[dict], live_cutoff: float) -> list[PodData]:
    """Map series to PodData, marking a pod live only when its series has a
    datapoint at or after live_cutoff. Pure, so the liveness semantics — the input
    to the estimated-savings replica multiplier — stay unit-testable.
    """
    last_seen: Dict[str, float] = {}
    for series in entries or []:
        pod_name = safe_get(series, ["metric", "pod_name"], "")
        if not pod_name:
            continue
        for ts in series.get("timestamps") or []:
            # A timestamp that cannot be read is skipped rather than raised: it
            # would otherwise take down the whole scan for one unreadable point,
            # and a pod's liveness is decided by its latest READABLE observation.
            try:
                seen = float(ts)
            except (TypeError, ValueError):
                continue
            if seen > last_seen.get(pod_name, 0.0):
                last_seen[pod_name] = seen
    return [PodData(name=name, deleted=seen < live_cutoff) for name, seen in last_seen.items()]


# The strategy's points_required floor is expressed in Prometheus steps: 50 points at
# the 75s step the loaders use is "about an hour of observed data". Datadog scales its
# rollup with the window — measured against the dev account, 5-minute buckets over 24
# hours and 2-hour buckets over the 14 days rightsizing asks for — so the same 50 would
# demand that a pod have lived for more than four days. Rather than give each provider
# its own threshold — three numbers that have to be kept in step with one setting — the
# counts Datadog reports are converted into the same unit: how many 75s steps the
# observed data covers. The floor then means the same thing on every provider.
PROMETHEUS_EQUIVALENT_STEP_SECONDS = 75.0


def bucket_seconds(arrays: Dict[str, np.ndarray]) -> float:
    """The width of one Datadog bucket, from the timestamps it actually returned.

    Taken across every pod in the response, and as a median, because a single pod's
    gaps (an interval it did not exist in is simply absent) leave jumps that a mean
    would follow. Returns 0 when there is nothing to measure from.
    """
    deltas: List[float] = []
    for arr in arrays.values():
        timestamps = arr[:, 0]
        deltas.extend(float(d) for d in np.diff(timestamps) if d > 0)
    if not deltas:
        return 0.0
    return float(np.median(deltas))


def equivalent_points(arr: np.ndarray, bucket: float) -> float:
    """How many 75s steps this pod's readings cover.

    A Datadog reading stands for its whole bucket, so N readings cover N * bucket
    seconds of observation. With no bucket to measure (one reading, one pod) the
    reading counts for itself, which keeps the old behaviour rather than inventing
    coverage that was not measured.
    """
    if bucket <= 0:
        return float(len(arr))
    return len(arr) * bucket / PROMETHEUS_EQUIVALENT_STEP_SECONDS


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
    A class for fetching metrics from Datadog, through api-server.
    """

    def __init__(
        self,
        config: Config,
        account_id: str,
        executor: ThreadPoolExecutor,
        tenant_id: str = "",
    ) -> None:
        super().__init__(config, account_id, executor)
        self.account_id = account_id
        self.tenant_id = tenant_id

    def check_connection(self):
        """api-server holds the connection; a broken one surfaces on the first query
        with its own error, the same way the Prometheus path behaves."""
        return

    def _metrics_timeout(self) -> Optional[int]:
        """The caller's own budget, so an abandoned workload does not leave api-server
        working on a request nobody will read."""
        return getattr(self._config, "metrics_fetch_timeout", None)

    def _query_datadog(self, query: str, from_ts: int, to_ts: int) -> List[dict]:
        """Run one Datadog query and return series as {metric, timestamps, values}."""
        return query_metrics_via_api_server(
            account_id=self.account_id,
            tenant_id=self.tenant_id,
            query=query,
            start_time=datetime.fromtimestamp(from_ts, tz=timezone.utc),
            end_time=datetime.fromtimestamp(to_ts, tz=timezone.utc),
            metric_provider=METRIC_PROVIDER_DATADOG,
            timeout=self._metrics_timeout(),
        )

    async def _async_query_datadog(self, query: str, from_ts: int, to_ts: int) -> List[dict]:
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

    def _parse_series_by_pod_container(self, entries: List[dict]) -> Dict[str, Dict[str, list]]:
        """
        Parse series into a nested dict: container -> pod -> [(timestamp, value)].

        Series are grouped by pod_name and kube_container_name, which the query asks
        for; anything missing either label is not a per-container reading.
        """
        result: Dict[str, Dict[str, list]] = {}
        for series in entries or []:
            pod_name = safe_get(series, ["metric", "pod_name"], "")
            container_name = safe_get(series, ["metric", "kube_container_name"], "")
            if not pod_name or not container_name:
                continue

            samples = result.setdefault(container_name, {}).setdefault(pod_name, [])
            timestamps = series.get("timestamps") or []
            values = series.get("values") or []
            for ts, value in zip(timestamps, values):
                if value is None:
                    continue
                # api-server formats values as strings; the direct path leaves them
                # numeric. Both end up in numpy arrays, so settle it here. An
                # unreadable point is dropped like a gap — one of them must not cost
                # the workload its recommendation.
                try:
                    samples.append((float(ts), float(value)))
                except (TypeError, ValueError):
                    logger.warning(f"Datadog: unreadable sample for pod {pod_name}, skipping")
        return result

    async def load_pods(self, object: K8sObjectData, period: timedelta) -> list[PodData]:
        """
        Load pod names from Datadog metric series.

        We query a lightweight metric to discover pod names associated with the workload.
        Discovery spans the whole history window, so liveness must NOT be inferred
        from mere presence in the result: marking every discovered pod deleted=False
        counted every incarnation from every deploy/restart as a live replica, and
        the estimated-savings replica multiplier scaled by dozens instead of the
        live replica count (same inflation as the Prometheus path's range-based
        liveness check).
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

        return pods_from_series(data, live_cutoff=(now - timedelta(minutes=30)).timestamp())

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

        # Pre-convert raw points to numpy arrays once per pod to avoid
        # redundant np.array() calls across the 5+ CPU and 2+ memory metrics.
        cpu_arrays: Dict[str, np.ndarray] = {}
        cpu_cores: Dict[str, np.ndarray] = {}
        for pod_name, points in cpu_pods.items():
            if len(points) == 0:
                continue
            arr = np.array(points, dtype=np.float64)
            cpu_arrays[pod_name] = arr
            cpu_cores[pod_name] = arr[:, 1] / 1e9

        mem_arrays: Dict[str, np.ndarray] = {}
        for pod_name, points in mem_pods.items():
            if len(points) == 0:
                continue
            mem_arrays[pod_name] = np.array(points, dtype=np.float64)

        # Build the result dict matching the strategy's expected metric keys
        result: Dict[str, PodsTimeData] = {}

        for metric_name in strategy.metrics:
            if metric_name.startswith("cpu_percentile_"):
                percentile = float(metric_name.replace("cpu_percentile_", ""))
                pods_data: PodsTimeData = {}
                for pod_name, arr in cpu_arrays.items():
                    p_val = float(np.percentile(cpu_cores[pod_name], percentile))
                    pods_data[pod_name] = np.array([[arr[-1, 0], p_val]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "MaxMemoryLoader":
                pods_data = {}
                for pod_name, arr in mem_arrays.items():
                    max_val = float(np.max(arr[:, 1]))
                    pods_data[pod_name] = np.array([[arr[-1, 0], max_val]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "CPUAmountLoader":
                pods_data = {}
                cpu_bucket = bucket_seconds(cpu_arrays)
                for pod_name, arr in cpu_arrays.items():
                    count = equivalent_points(arr, cpu_bucket)
                    pods_data[pod_name] = np.array([[arr[-1, 0], count]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "MemoryAmountLoader":
                pods_data = {}
                mem_bucket = bucket_seconds(mem_arrays)
                for pod_name, arr in mem_arrays.items():
                    count = equivalent_points(arr, mem_bucket)
                    pods_data[pod_name] = np.array([[arr[-1, 0], count]], dtype=np.float64)
                result[metric_name] = pods_data

            elif metric_name == "MaxOOMKilledMemoryLoader":
                result[metric_name] = {}

            else:
                logger.warning(f"Unknown metric key '{metric_name}' requested by strategy, returning empty")
                result[metric_name] = {}

        return result
