from __future__ import annotations

import logging
import asyncio
import os
from datetime import datetime, timedelta
from typing import Dict, Any, Iterable, Optional
from concurrent.futures import ThreadPoolExecutor

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.models.replicas import ReplicaStats, replica_stats_from_running_series
from server.recommendation.vertical_rightsizing.strategy.strategies import BaseStrategy

from server.recommendation.vertical_rightsizing.models.result import PodsTimeData
from server.recommendation.vertical_rightsizing.services.batched import batched

from server.recommendation.vertical_rightsizing.metrics.base import PrometheusMetric
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService
from server.metrics.prometheus_metrics import Prometheus

# from prometheus_api_client import PrometheusConnect

logger = logging.getLogger("rightsizing")


def _tuning_env(name: str, legacy_name: str, default: str) -> str:
    """Read a tuning knob, still honouring the name it had before the rename.

    Neither knob is documented or set anywhere in this repo, but the chart renders
    `.Values.env` verbatim, so an install can set either name from a values file we do
    not control. Without this, renaming would silently drop an operator's override back
    to the default. Remove the legacy lookup once no install predates the rename.
    """
    value = os.environ.get(name)
    if value is not None:
        return value
    legacy_value = os.environ.get(legacy_name)
    if legacy_value is not None:
        logger.warning(f"{legacy_name} is deprecated and will be removed; use {name} instead")
        return legacy_value
    return default


# Resolved once at import. Neither knob can change at runtime, and reading them per call would
# repeat the deprecation warning for every workload of every scan.
# Global semaphore to limit concurrent relay server requests
# This prevents overwhelming the relay/agent when multiple ML server replicas are running
MAX_CONCURRENT_METRICS_REQUESTS = int(
    _tuning_env("RIGHTSIZING_MAX_CONCURRENT_RELAY_REQUESTS", "KRR_MAX_CONCURRENT_RELAY_REQUESTS", "10")
)
# Concurrency cap for metric queries. Named for the relay for backward compatibility:
# the queries now go through api-server, but an operator's existing override should keep
# working rather than silently reverting to the default.
# How many pod owners are packed into a single Prometheus query by load_pods
OWNER_BATCH_SIZE = int(_tuning_env("RIGHTSIZING_OWNER_BATCH_SIZE", "KRR_OWNER_BATCH_SIZE", "100"))
# Keyed by limit: one process can serve accounts on different transports, and a single
# cached semaphore would pin every later account to whichever limit was created first.
_metrics_semaphores: dict[int, asyncio.Semaphore] = {}


def _get_relay_semaphore(limit: int | None = None) -> asyncio.Semaphore:
    """Get or create the metrics semaphore for this concurrency limit."""
    # Clamped: Semaphore(0) blocks forever and a negative value raises, so a mistyped
    # override would hang every scan rather than fail at startup.
    resolved = max(1, MAX_CONCURRENT_METRICS_REQUESTS if limit is None else limit)
    semaphore = _metrics_semaphores.get(resolved)
    if semaphore is None:
        semaphore = asyncio.Semaphore(resolved)
        _metrics_semaphores[resolved] = semaphore
        logger.info(f"Initialized metrics semaphore with max {resolved} concurrent requests")
    return semaphore


class PrometheusMetricsService(MetricsService):
    """
    A class for fetching metrics from Prometheus.
    """

    def __init__(self, config: Config, account_id: str, executor: ThreadPoolExecutor, tenant_id: str = "") -> None:
        super().__init__(config, account_id, executor)
        self.prometheus = Prometheus(deployment_name="", namespace_name="", account_id=account_id, tenant_id=tenant_id)
        # self.prometheus = PrometheusConnect(url="http://127.0.0.1:8429", disable_ssl=True)

    def check_connection(self):
        """
        Checks the connection to Prometheus.
        Raises:
            PrometheusNotFound: If the connection to Prometheus cannot be established.
        """
        self.prometheus.check_prometheus_connection()

    async def query(self, query: str) -> dict:
        semaphore = _get_relay_semaphore()
        async with semaphore:
            loop = asyncio.get_running_loop()
            return await loop.run_in_executor(
                self.executor,
                lambda: self.prometheus.custom_query(query=query),
            )

    async def query_range(self, query: str, start: datetime, end: datetime, step: timedelta) -> dict:
        semaphore = _get_relay_semaphore()
        async with semaphore:
            loop = asyncio.get_running_loop()
            return await loop.run_in_executor(
                self.executor,
                lambda: self.prometheus.custom_query_range(
                    query=query, start_time=start, end_time=end, step=f"{step.seconds}s"
                ),
            )

    async def get_history_range(self, history_duration: timedelta) -> tuple[datetime, datetime]:
        """
        Get the history range from Prometheus, based on container_memory_working_set_bytes.
        Returns:
            float: The first history point.
        """

        now = datetime.now()
        result = await self.query_range(
            "max(prometheus_tsdb_head_series)",
            start=now - history_duration,
            end=now,
            step=timedelta(hours=1),
        )
        try:
            values = result[0]["values"]
            start, end = values[0][0], values[-1][0]
            return datetime.fromtimestamp(start), datetime.fromtimestamp(end)
        except (KeyError, IndexError) as e:
            logger.debug(f"Returned from get_history_range: {result}")
            raise ValueError("Error while getting history range") from e

    async def _gather_data(
        self,
        object: K8sObjectData,
        LoaderClass: type[PrometheusMetric],
        period: timedelta,
        step: timedelta = timedelta(minutes=30),
    ) -> PodsTimeData:
        metric_loader = LoaderClass(self.prometheus, self.name(), self.executor, self._config)
        data = await metric_loader.load_data(object, period, step)

        if len(data) == 0:
            if "CPU" in LoaderClass.__name__:
                object.add_warning("NoPrometheusCPUMetrics")
            elif "Memory" in LoaderClass.__name__:
                object.add_warning("NoPrometheusMemoryMetrics")

            if LoaderClass.warning_on_no_data:
                logger.warning(
                    f"{metric_loader.service_name} returned no {metric_loader.__class__.__name__} metrics for {object}"
                )

        return data

    async def gather_data(
        self,
        object: K8sObjectData,
        strategy: BaseStrategy,
        period: timedelta,
        *,
        step: timedelta = timedelta(minutes=30),
    ) -> Dict[str, PodsTimeData]:
        """
        Gathers data from Prometheus for a specified object and resource.

        Args:
            object (K8sObjectData): The Kubernetes object.
            resource (ResourceType): The resource type.
            period (datetime.timedelta): The time period for which to gather data.
            step (datetime.timedelta, optional): The time step between data points. Defaults to 30 minutes.

        Returns:
            ResourceHistoryData: The gathered resource history data.
        """

        # NOTE: Pods should already be loaded by the caller (service.py)
        # Only load if not already present to avoid overwriting database fallback
        if not object.pods:
            logger.debug(f"No pods loaded for {object}, attempting to load from Prometheus")
            await self.load_pods(object, period)

        if object.pods:
            pod_names = [p.name for p in object.pods[:3]]
            logger.debug(f"Gathering metrics for {object} with {len(object.pods)} pods: {pod_names}")
        else:
            logger.warning(f"No pods found for {object}, metrics query will use wildcard selector")

        # Fetch all metrics in parallel for better performance
        if isinstance(strategy.metrics, dict):
            metric_names = list(strategy.metrics.keys())
            metric_loaders = list(strategy.metrics.values())
            results = await asyncio.gather(
                *[self._gather_data(object, loader, period, step) for loader in metric_loaders]
            )
            return dict(zip(metric_names, results))
        else:
            metric_loaders = list(strategy.metrics)
            results = await asyncio.gather(
                *[self._gather_data(object, loader, period, step) for loader in metric_loaders]
            )
            return {loader.__name__: result for loader, result in zip(metric_loaders, results)}

    async def load_pods(self, object: K8sObjectData, period: timedelta) -> list[PodData]:
        """
        List pods related to the object and add them to the object's pods list.
        Args:
            object (K8sObjectData): The Kubernetes object.
            period (timedelta): The time period for which to gather data.
        """

        logger.debug(f"Adding historic pods for {object}")
        days_literal = min(int(period.total_seconds()) // 3600 // 24, 32)
        period_literal = f"{days_literal}d"

        pod_owners: Iterable[str]
        pod_owner_kind: str
        cluster_label = self.get_prometheus_cluster_label()
        if object.kind in ["Deployment", "Rollout"]:
            replicasets = await self.query_range(
                f"""kube_replicaset_owner{{
                    owner_name="{object.name}", owner_kind="{object.kind}", namespace="{object.namespace}",
                     {cluster_label}
                }}
                """,
                start=datetime.now() - period,
                end=datetime.now(),
                step=timedelta(hours=1),
            )
            pod_owners = {replicaset["metric"]["replicaset"] for replicaset in replicasets}
            pod_owner_kind = "ReplicaSet"
            del replicasets

        elif object.kind == "DeploymentConfig":
            replication_controllers = await self.query_range(
                f"""kube_replicationcontroller_owner{{
                        owner_name="{object.name}",owner_kind="{object.kind}",namespace="{object.namespace}",
                         {cluster_label}
                    }}
                """,
                start=datetime.now() - period,
                end=datetime.now(),
                step=timedelta(hours=1),
            )
            pod_owners = {
                repl_controller["metric"]["replicationcontroller"] for repl_controller in replication_controllers
            }
            pod_owner_kind = "ReplicationController"

            del replication_controllers

        elif object.kind == "CronJob":
            jobs = await self.query_range(
                f"""
                    kube_job_owner{{
                        owner_name="{object.name}",
                        owner_kind="{object.kind}",
                        namespace="{object.namespace}",
                        {cluster_label}
                    }}
                """,
                start=datetime.now() - period,
                end=datetime.now(),
                step=timedelta(hours=1),
            )
            pod_owners = {job["metric"]["job_name"] for job in jobs}
            pod_owner_kind = "Job"

            del jobs
        else:
            pod_owners = [object.name]
            pod_owner_kind = object.kind

        related_pods_result = []
        batch_size = OWNER_BATCH_SIZE
        for owner_group in batched(pod_owners, batch_size):
            owners_regex = "|".join(owner_group)
            # One INSTANT query, one row per pod. Only the pod names are read below, and
            # last_over_time already reaches back over the whole window, so evaluating it
            # at every hour of that window as well returned each pod up to 337 times.
            # For a DaemonSet on a cluster with churning nodes (9,688 pod series in
            # 14 days) that was a 146 MB answer, which OOM-killed api-server (#39991).
            related_pods_result_item = await self.query(f"""
                    count by (pod) (
                        last_over_time(
                            kube_pod_owner{{
                                owner_name=~"{owners_regex}",
                                owner_kind="{pod_owner_kind}",
                                namespace="{object.namespace}",
                                {cluster_label}
                            }}[{period_literal}]
                        )
                    )
                """)
            related_pods_result.extend(related_pods_result_item)
        if related_pods_result == []:
            return []

        related_pods = [pod["metric"]["pod"] for pod in related_pods_result]
        current_pods_set = set()
        del related_pods_result

        # "Currently running" must be an INSTANT check. The previous query_range
        # over the whole history period marked every pod that was Running at ANY
        # step in the window as not-deleted, so current_pods_count counted every
        # pod incarnation from every deploy/restart in the last N days — and the
        # estimated-savings replica multiplier scaled by dozens instead of the
        # live replica count (a 2-replica deployment with daily rollouts
        # reported ~45 "replicas" and a ~22x inflated saving).
        # Batches are independent and the relay semaphore already bounds
        # concurrency, so they run in parallel.
        def liveness_query(group_regex: str) -> str:
            return f"""
                    kube_pod_status_phase{{
                        phase="Running",
                        pod=~"{group_regex}",
                        namespace="{object.namespace}",
                        {cluster_label}
                    }} == 1
                """

        async def instant_batch(pod_group: Iterable[str]) -> set[str]:
            result = await self.query(liveness_query("|".join(pod_group)))
            return {pod["metric"]["pod"] for pod in result}

        for batch_pods in await asyncio.gather(*(instant_batch(g) for g in batched(related_pods, 100))):
            current_pods_set |= batch_pods

        # A transient scrape gap can make the instant vector empty even though
        # pods are running; fall back to the last hour so a momentary gap
        # degrades to slightly-stale liveness rather than zero live pods.
        if not current_pods_set and related_pods:
            now = datetime.now()

            async def range_batch(pod_group: Iterable[str]) -> set[str]:
                result = await self.query_range(
                    liveness_query("|".join(pod_group)),
                    start=now - timedelta(hours=1),
                    end=now,
                    step=timedelta(minutes=15),
                )
                return {pod["metric"]["pod"] for pod in result}

            for batch_pods in await asyncio.gather(*(range_batch(g) for g in batched(related_pods, 100))):
                current_pods_set |= batch_pods

        return list({PodData(name=pod, deleted=pod not in current_pods_set) for pod in related_pods})

    async def load_replica_stats(self, object: K8sObjectData, period: timedelta) -> Optional[ReplicaStats]:
        """Time-weighted replica count over the window, from the pods load_pods discovered.

        Hourly samples of the Running phase per pod; a timestamp with no running
        pod carries no sample, so a workload younger than the window averages
        over its own lifetime. Returns None on an empty result so the caller
        falls back to the instant count.
        """
        pods = [pod.name for pod in object.pods]
        if not pods:
            return None
        cluster_label = self.get_prometheus_cluster_label()
        now = datetime.now()

        async def range_batch(pod_group: Iterable[str]) -> Any:
            # query_range is annotated dict but yields the list of series the
            # liveness check above iterates the same way.
            return await self.query_range(
                f"""
                    kube_pod_status_phase{{
                        phase="Running",
                        pod=~"{'|'.join(pod_group)}",
                        namespace="{object.namespace}",
                        {cluster_label}
                    }} == 1
                """,
                start=now - period,
                end=now,
                step=timedelta(hours=1),
            )

        series: list = []
        for batch in await asyncio.gather(*(range_batch(g) for g in batched(pods, 100))):
            series.extend(batch)
        return replica_stats_from_running_series(series, period)
