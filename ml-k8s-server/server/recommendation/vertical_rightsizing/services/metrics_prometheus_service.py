from __future__ import annotations

import logging
import asyncio
import os
from datetime import datetime, timedelta
from typing import Dict, Any, Iterable
from concurrent.futures import ThreadPoolExecutor

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.strategy.strategies import BaseStrategy

from server.recommendation.vertical_rightsizing.models.result import PodsTimeData
from server.recommendation.vertical_rightsizing.services.batched import batched

from server.recommendation.vertical_rightsizing.metrics.base import PrometheusMetric
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService
from server.metrics.prometheus_metrics import Prometheus

# from prometheus_api_client import PrometheusConnect

logger = logging.getLogger("krr")

# Global semaphore to limit concurrent relay server requests
# This prevents overwhelming the relay/agent when multiple ML server replicas are running
MAX_CONCURRENT_RELAY_REQUESTS = int(os.environ.get("KRR_MAX_CONCURRENT_RELAY_REQUESTS", "10"))
_relay_semaphore: asyncio.Semaphore | None = None


def _get_relay_semaphore() -> asyncio.Semaphore:
    """Get or create the global relay semaphore for the current event loop."""
    global _relay_semaphore
    if _relay_semaphore is None:
        _relay_semaphore = asyncio.Semaphore(MAX_CONCURRENT_RELAY_REQUESTS)
        logger.info(f"Initialized relay semaphore with max {MAX_CONCURRENT_RELAY_REQUESTS} concurrent requests")
    return _relay_semaphore


class PrometheusMetricsService(MetricsService):
    """
    A class for fetching metrics from Prometheus.
    """

    def __init__(self, config: Config, account_id: str, executor: ThreadPoolExecutor) -> None:
        super().__init__(config, account_id, executor)
        self.prometheus = Prometheus(deployment_name="", namespace_name="", account_id=account_id)
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
    ) -> PodsTimeData:
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

    async def query_and_validate(self, prom_query) -> Any:
        result = await self.query(prom_query)
        if len(result) == 0:
            logger.debug("No results from Prometheus query (cluster summary)")
            return None

        if len(result) > 1:
            logger.debug(f"Multiple results ({len(result)}) from Prometheus query, using first")

        result_value = result[0].get("value")

        # Verify that the "value" list has exactly two elements (timestamp and value)
        if not result_value:
            logger.debug("Missing value in Prometheus result (cluster summary)")
            return None

        if len(result_value) != 2:
            logger.debug(f"Prometheus result values unexpected size: {len(result_value)}")
            return None

        return result_value[1]

    async def get_cluster_summary(self) -> Dict[str, Any]:
        cluster_label = self.get_prometheus_cluster_label()

        # use this for queries with no labels. turn ', cluster="xxx"' to 'cluster="xxx"'
        single_cluster_label = cluster_label.replace(",", "")
        memory_query = f"""
            sum(max by (instance) (machine_memory_bytes{{ {single_cluster_label} }}))
            or sum(max by (instance) (node_resources_memory_total_bytes{{ endpoint=\"http\", {cluster_label} }}))
        """
        cpu_query = f"""
            sum(max by (instance) (machine_cpu_cores{{ {single_cluster_label} }}))
            or sum(max by (instance)(node_resources_cpu_logical_cores{{endpoint=\"http\", {cluster_label} }}))
        """
        kube_system_requests_mem = f"""
        sum(max(kube_pod_container_resource_requests{{ namespace='kube-system', resource='memory', {cluster_label} }})
            by (job, pod, container) )
        """
        kube_system_requests_cpu = f"""
            sum(max(kube_pod_container_resource_requests{{ namespace='kube-system', resource='cpu', {cluster_label} }})
            by (job, pod, container) )
        """
        try:
            cluster_memory_result = await self.query_and_validate(memory_query)
            cluster_cpu_result = await self.query_and_validate(cpu_query)
            kube_system_mem_result = await self.query_and_validate(kube_system_requests_mem)
            kube_system_cpu_result = await self.query_and_validate(kube_system_requests_cpu)
            return {
                "cluster_memory": float(cluster_memory_result) if cluster_memory_result else 0,
                "cluster_cpu": float(cluster_cpu_result) if cluster_cpu_result else 0,
                "kube_system_mem_req": float(kube_system_mem_result) if kube_system_mem_result else 0,
                "kube_system_cpu_req": float(kube_system_cpu_result) if kube_system_cpu_result else 0,
            }

        except Exception as e:
            logger.error(f"Exception occurred while getting cluster summary: {e}")
            return {}

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
        batch_size = int(os.environ.get("KRR_OWNER_BATCH_SIZE", 100))
        for owner_group in batched(pod_owners, batch_size):
            owners_regex = "|".join(owner_group)
            related_pods_result_item = await self.query_range(
                f"""
                    last_over_time(
                        kube_pod_owner{{
                            owner_name=~"{owners_regex}",
                            owner_kind="{pod_owner_kind}",
                            namespace="{object.namespace}",
                            {cluster_label}
                        }}[{period_literal}]
                    )
                """,
                start=datetime.now() - period,
                end=datetime.now(),
                step=timedelta(hours=1),
            )
            related_pods_result.extend(related_pods_result_item)
        if related_pods_result == []:
            return []

        related_pods = [pod["metric"]["pod"] for pod in related_pods_result]
        current_pods_set = set()
        del related_pods_result

        for pod_group in batched(related_pods, 100):
            group_regex = "|".join(pod_group)
            pods_status_result = await self.query_range(
                f"""
                    kube_pod_status_phase{{
                        phase="Running",
                        pod=~"{group_regex}",
                        namespace="{object.namespace}",
                        {cluster_label}
                    }} == 1
                """,
                start=datetime.now() - period,
                end=datetime.now(),
                step=timedelta(hours=1),
            )
            current_pods_set |= {pod["metric"]["pod"] for pod in pods_status_result}
            del pods_status_result
        return list({PodData(name=pod, deleted=pod not in current_pods_set) for pod in related_pods})
