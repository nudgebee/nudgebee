import asyncio
import logging
import math
from concurrent.futures import ThreadPoolExecutor
from typing import Optional, Union, Dict, Any

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.strategy.strategies import RunResult
from server.recommendation.vertical_rightsizing.models.allocations import RecommendationValue
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData
from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import DatadogMetricsService
from server.recommendation.vertical_rightsizing.models.result import (
    ResourceAllocations,
    ResourceScan,
    ResourceType,
    Result,
    StrategyData,
)
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import PrometheusMetricsService

# from server.recommendation.vertical_rightsizing.services.cluster_service import ClusterService
from server.recommendation.vertical_rightsizing.services.nudgebee_cluster_service import ClusterService

from server.recommendation.vertical_rightsizing.strategy.strateggy_nudgebee import (
    NudgebeeStrategy,
    NudgebeeStrategySettings,
)

logger = logging.getLogger("krr")


class CriticalRunnerException(Exception):
    pass


class RecommendationService:
    def __init__(
        self,
        account_id: str,
        config: Config,
        metrics_provider: Optional[str] = None,
        datadog_api_key: Optional[str] = None,
        datadog_app_key: Optional[str] = None,
        datadog_site: Optional[str] = None,
    ) -> None:
        self._config = config
        self._executor = ThreadPoolExecutor(self._config.max_workers)
        self.account_id = account_id
        if metrics_provider == "datadog":
            self._metrics_service_loaders = DatadogMetricsService(
                config,
                account_id,
                self._executor,
                api_key=datadog_api_key,
                app_key=datadog_app_key,
                site=datadog_site,
            )
        else:
            self._metrics_service_loaders = PrometheusMetricsService(config, account_id, self._executor)
        self._k8s_loader = ClusterService(account_id, self._executor, config)
        self._strategy = NudgebeeStrategy(NudgebeeStrategySettings())
        self.errors: list[dict] = []

    def __get_resource_minimal(self, resource: ResourceType) -> float:
        if resource == ResourceType.CPU:
            return 1 / 1000 * self._config.cpu_min_value
        elif resource == ResourceType.Memory:
            return 1024**2 * self._config.memory_min_value
        else:
            return 0

    def _round_config(self, value_dict: Optional[Dict[str, Dict[str, Any]]], resource: ResourceType) -> Dict[str, Any]:
        if value_dict is None:
            return {}
        formatted: Dict[str, Any] = {}
        if resource == ResourceType.CPU:
            for config_name, config_value in value_dict.items():
                prec_power: Union[float, int]
                # NOTE: We use 10**3 as the minimal value for CPU is 1m
                prec_power = 10**3
                rounded = math.ceil(config_value * prec_power) / prec_power
                minimal = self.__get_resource_minimal(ResourceType.CPU)
                formatted[config_name] = max(rounded, minimal)
        elif resource == ResourceType.Memory:
            for config_name, config_value in value_dict.items():
                prec_power: Union[float, int]
                # NOTE: We use 10**3 as the minimal value for CPU is 1m
                prec_power = 1 / (1024**2)
                rounded = math.ceil(config_value * prec_power) / prec_power
                minimal = self.__get_resource_minimal(ResourceType.Memory)

                formatted[config_name] = max(rounded, minimal)
        else:
            # NOTE: We use 1 as the minimal value for other resources
            prec_power = 1

        return formatted

    def _round_value(
        self, value: Optional[float], resource: ResourceType, current_allocation: RecommendationValue
    ) -> Optional[float]:
        if value is None or math.isnan(value):
            return value

        prec_power: Union[float, int]
        if resource == ResourceType.CPU:
            # NOTE: We use 10**3 as the minimal value for CPU is 1m
            prec_power = 10**3
        elif resource == ResourceType.Memory:
            # NOTE: We use 10**6 as the minimal value for memory is 1M
            prec_power = 1 / (1024**2)
        else:
            # NOTE: We use 1 as the minimal value for other resources
            prec_power = 1

        rounded = math.ceil(value * prec_power) / prec_power

        minimal = self.__get_resource_minimal(resource)

        if (
            rounded < minimal
            and current_allocation is not None
            and current_allocation != "?"
            and current_allocation < minimal
        ):
            current_value = float(current_allocation)
            return max(rounded, current_value)

        return max(rounded, minimal)

    async def _calculate_object_recommendations(self, object: K8sObjectData) -> Optional[RunResult]:
        object.pods = await self._metrics_service_loaders.load_pods(object, self._strategy.settings.history_timedelta)
        logger.debug(f"Prometheus load_pods returned {len(object.pods)} pods for {object}")

        if object.pods == []:
            # Fallback to database (k8s_pods table)
            object.pods = await self._k8s_loader.list_pods(object)
            logger.debug(f"Database fallback returned {len(object.pods)} pods for {object}")
            # NOTE: Database returned pods, but Prometheus did not
            # This might happen with fast executing jobs or missing kube-state-metrics
            if object.pods != []:
                object.add_warning("NoPrometheusPods")
            else:
                logger.warning(f"No pods found for {object} in both Prometheus and database")

        metrics = await self._metrics_service_loaders.gather_data(
            object,
            self._strategy,
            self._strategy.settings.history_timedelta,
            step=self._strategy.settings.timeframe_timedelta,
        )

        result = self._strategy.run(metrics, object)

        logger.debug(f"Calculated recommendations for {object} (using {len(metrics)} metrics)")
        return result

    async def _gather_object_allocations(self, k8s_object: K8sObjectData) -> Optional[ResourceScan]:
        try:
            # Quick validation: Skip if workload clearly has no historical data
            # This is an optimization for one-time scans where we don't want to waste time
            # on workloads that clearly won't have metrics

            # Add timeout to prevent hanging on slow metrics
            # Increased to 90s to account for semaphore queuing when multiple workloads compete
            timeout = getattr(self._config, "metrics_fetch_timeout", 90)
            recommendation = await asyncio.wait_for(self._calculate_object_recommendations(k8s_object), timeout=timeout)

            if recommendation is None:
                logger.debug(f"No recommendation generated for {k8s_object}")
                return None

            return ResourceScan.calculate(
                k8s_object,
                ResourceAllocations(
                    requests={resource: recommendation[resource].request for resource in ResourceType},
                    limits={resource: recommendation[resource].limit for resource in ResourceType},
                    info={resource: recommendation[resource].info for resource in ResourceType},
                    config={resource: recommendation[resource].config for resource in ResourceType},
                ),
            )
        except asyncio.TimeoutError:
            logger.warning(f"Timeout processing {k8s_object} after {timeout}s - skipping")
            return None
        except Exception as e:
            logger.debug(f"Error processing {k8s_object}: {e}")
            return None

    async def _collect_result(self) -> Result:
        workloads = await self._k8s_loader.list_scannable_objects()

        # Apply batch size limits only if explicitly configured
        max_batch_size = getattr(self._config, "max_recommendations_per_batch", None)
        total_objects = len(workloads)

        if max_batch_size is not None and total_objects > max_batch_size:
            logger.warning(
                f"Limiting processing to {max_batch_size} objects out of {total_objects} "
                f"due to explicit batch size limit"
            )
            workloads = workloads[:max_batch_size]

        logger.info(f"Processing {len(workloads)} workloads")

        # Get cluster summary and process workloads concurrently
        cluster_summary_task = self._metrics_service_loaders.get_cluster_summary()

        # Batch sizing to avoid overwhelming relay server and memory
        # Each workload makes ~4-5 Prometheus queries, so smaller batches for more workloads
        # Cap at max_workers to prevent excessive concurrent memory usage
        if len(workloads) > 200:
            batch_size = max(2, self._config.max_workers // 2)  # Conservative for large workloads
        elif len(workloads) > 100:
            batch_size = self._config.max_workers  # Moderate for medium workloads
        else:
            batch_size = self._config.max_workers  # Same cap for smaller workloads

        scans = []
        logger.info(f"Using batch size: {batch_size} for {len(workloads)} workloads")

        for i in range(0, len(workloads), batch_size):
            batch_workloads = workloads[i : i + batch_size]
            # Reduce verbosity - only log every 10th batch or for small batches
            batch_num = i // batch_size + 1
            total_batches = (len(workloads) + batch_size - 1) // batch_size
            if batch_num % 10 == 0 or len(workloads) <= 50:
                logger.debug(f"Processing batch {batch_num}/{total_batches}")

            # Process current batch concurrently
            batch_tasks = [self._gather_object_allocations(k8s_object) for k8s_object in batch_workloads]
            batch_results = await asyncio.gather(*batch_tasks, return_exceptions=True)

            for result in batch_results:
                if isinstance(result, Exception):
                    logger.error(f"Error processing workload: {result}")
                elif result is not None:
                    scans.append(result)

        # Wait for cluster summary
        cluster_summary = await cluster_summary_task
        successful_scans = scans  # All scans are already non-None due to filtering above

        if len(scans) == 0:
            logger.warning("Current filters resulted in no objects available to scan.")
            logger.warning("Try to change the filters or check if there is anything available.")
            if self._config.namespaces is None or len(self._config.namespaces) == 0:
                logger.warning(
                    "Note that you are using the '*' namespace filter, which by default excludes kube-system."
                )
            raise CriticalRunnerException("No objects available to scan.")
        elif len(successful_scans) == 0:
            raise CriticalRunnerException("No successful scans were made. Check the logs for more information.")

        return Result(
            scans=scans,
            description=f"[b]{self._strategy.display_name.title()} Strategy[/b]\n\n{self._strategy.description}",
            strategy=StrategyData(
                name=str(self._strategy).lower(),
                settings=self._strategy.settings.model_dump(),
            ),
            clusterSummary=cluster_summary,
            config=self._config,
        )

    async def run(self) -> Result:
        """Run the Runner. The return value is the exit code of the program."""
        try:
            result = await self._collect_result()
            return result
        except Exception as e:
            logger.exception("an unexpected error occurred")
            raise e
