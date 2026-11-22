import asyncio
import logging
import math
import time
from concurrent.futures import ThreadPoolExecutor
from typing import Optional, Union, Dict, Any

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.strategy.strategies import RunResult
from server.recommendation.vertical_rightsizing.models.allocations import RecommendationValue
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData
from server.recommendation.vertical_rightsizing.services.metrics_datadog_service import DatadogMetricsService
from server.recommendation.vertical_rightsizing.services.metrics_elasticsearch_service import (
    ElasticsearchMetricsService,
)
from server.recommendation.vertical_rightsizing.models.result import (
    ResourceAllocations,
    ResourceScan,
    ResourceType,
    Result,
    StrategyData,
)
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import PrometheusMetricsService
from server.recommendation.vertical_rightsizing.services.metrics_utilisation_service import UtilisationMetricsService

# from server.recommendation.vertical_rightsizing.services.cluster_service import ClusterService
from server.recommendation.vertical_rightsizing.services.nudgebee_cluster_service import ClusterService
from server.utils.utils import ApiServerMetricsConfigs

from server.recommendation.vertical_rightsizing.strategy.strateggy_nudgebee import (
    NudgebeeStrategy,
    NudgebeeStrategySettings,
)

logger = logging.getLogger("rightsizing")

# Providers whose accounts have a Prometheus the owner-chain pod walk can query. The
# empty string is the common case: no explicit provider means the account's agent, which
# ships Prometheus. `metrics_provider` is Optional and arrives as None whenever the
# trigger names no provider -- the SAME common case -- so it is normalised to "" before
# being looked up here, which keeps this a set of provider names.
PROMETHEUS_FAMILY_PROVIDERS = {"", "prometheus", "victoria_metrics", "chronosphere", "cubeapm"}


class CriticalRunnerException(Exception):
    pass


class ScanAbortedError(CriticalRunnerException):
    """The scan was stopped early because it was not going to finish usefully.

    Nothing from an aborted scan is stored: a full-account run archives every Open
    recommendation missing from its results, so saving a partial scan would archive the
    workloads it never reached.
    """


class RecommendationService:
    def __init__(
        self,
        account_id: str,
        config: Config,
        metrics_provider: Optional[str] = None,
        tenant_id: str = "",
        metric_provider: str = "",
        metric_provider_source: str = "",
    ) -> None:
        self._config = config
        # Named so a leaked pool is identifiable in `py-spy dump` and in
        # /proc/<pid>/task/*/comm on a live pod, rather than being one more
        # anonymous "ThreadPoolExecutor-N_M" among the process's other pools.
        self._executor = ThreadPoolExecutor(self._config.max_workers, thread_name_prefix="rightsizing")
        self.account_id = account_id
        self.tenant_id = tenant_id
        # Annotated because there are now four branches: without it the field's type is
        # whichever service the first branch builds, and the other three read as
        # assignment errors.
        self._metrics_service_loaders: MetricsService
        # Datadog used to be excluded here. It no longer is: every reason has been
        # fixed and the numbers checked against Prometheus on the same cluster, which
        # is the only way to tell "Datadog disagrees" from "we asked it a different
        # question".
        #
        # The three original blockers were api-server's, and are gone (#39288, #39297,
        # #39301): the key loop is bounded-concurrent, one key's failure no longer
        # discards the others, and the metrics path retries 429s inside a budget.
        #
        # Two more surfaced while verifying, both measured rather than reasoned about:
        # the rollup ignored the window, and coverage counted buckets instead of
        # samples. With both fixed, Datadog matches Prometheus at equal resolution --
        # memory 100-103%, coverage 100-110%, CPU p99 94-110%.
        #
        # What remains is a property of Datadog, not a defect: it cannot be sampled
        # finer than 300s at scale, so its percentiles are a coarser-window peak than
        # the 75s one a Prometheus account gets. Recommendations there are smoother for
        # spiky workloads. That is a deliberate trade, not a regression.
        via_utilisation = ApiServerMetricsConfigs.rightsizing_via_utilisation

        if via_utilisation:
            # One service for every provider: it asks api-server for metric keys and
            # never names a provider. The account's default decides, except where the
            # trigger said otherwise because that default cannot answer these keys at
            # all (RightsizingMetricsHint, api-server side).
            #
            # Which pods a workload HAD over the window is a separate question, and on
            # a Prometheus-family account the existing walk over kube_pod_owner
            # answers it better than a pod-name prefix does. Keeping it means this
            # switch changes the numbers and not the population they are computed
            # over, which is what makes a before/after diff readable.
            # The owner-chain walk is PromQL over kube_pod_owner, so only an account
            # that HAS a Prometheus can use it. Asking it of a Datadog or ES account
            # returns no pods at all -- and a rightsizing run with no pods produces no
            # recommendation, silently. Those accounts derive the pod list from the
            # metric series instead, which UtilisationMetricsService does when it is
            # given no inventory.
            inventory = (
                PrometheusMetricsService(config, account_id, self._executor, tenant_id=tenant_id)
                if (metrics_provider or "") in PROMETHEUS_FAMILY_PROVIDERS
                else None
            )
            self._metrics_service_loaders = UtilisationMetricsService(
                config,
                account_id,
                self._executor,
                tenant_id=tenant_id,
                metric_provider=metric_provider or "",
                metric_provider_source=metric_provider_source or "",
                inventory=inventory,
            )
            logger.info(
                f"rightsizing via utilisation keys for account {account_id} "
                f"(default provider {metrics_provider or 'unset'}, "
                f"hint {metric_provider or 'none'}/{metric_provider_source or 'none'})"
            )
        elif metrics_provider == "ES":
            self._metrics_service_loaders = ElasticsearchMetricsService(
                config, account_id, self._executor, tenant_id=tenant_id
            )
        elif metrics_provider == "datadog":
            self._metrics_service_loaders = DatadogMetricsService(
                config, account_id, self._executor, tenant_id=tenant_id
            )
        else:
            self._metrics_service_loaders = PrometheusMetricsService(
                config, account_id, self._executor, tenant_id=tenant_id
            )
        self._k8s_loader = ClusterService(account_id, self._executor, config)
        self._strategy = NudgebeeStrategy(NudgebeeStrategySettings())
        self.errors: list[dict] = []
        self._timed_out_objects = 0

    def close(self) -> None:
        """Release this scan's thread pool.

        One service is built per scan and then dropped. Its worker threads park on the work
        queue instead of exiting, and each one keeps the scan's object graph -- metrics
        client, config, fetched series -- reachable. Measured on dev: a pod that consumed
        three scans held one extra thread and ~33MB per scan against a pod that consumed
        none, and the threads never came back, even for a scan that aborted in 0.35s.

        Relying on the executor being garbage collected is what failed in production:
        something still referenced it, so the weakref callback that would have drained the
        workers never fired. Shutting down explicitly does not care what holds the
        reference, which is why it is done here rather than by hunting the referrer.

        cancel_futures drops queries still queued, whose results were discarded when the
        scan ended; wait=True joins only the handful already in flight. Every callable
        handed to this pool is an HTTP request with its own timeout, so the join is bounded
        by one timeout rather than by the backlog -- keep it that way, since an untimed call
        here would stall the RabbitMQ consumer thread.
        """
        self._executor.shutdown(wait=True, cancel_futures=True)

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

        if object.pods:
            try:
                object.replica_stats = await self._metrics_service_loaders.load_replica_stats(
                    object, self._strategy.settings.history_timedelta
                )
            except Exception as e:  # the multiplier falls back to the live count
                logger.warning(f"Replica window lookup failed for {object}: {e}")
                object.replica_stats = None

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
            self._timed_out_objects += 1
            return None
        except Exception as e:
            logger.debug(f"Error processing {k8s_object}: {e}")
            return None
        finally:
            # The scan keeps every workload until it ends, including ones that failed.
            k8s_object.release_pods()

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

        # A batch counts against the scan when a workload in it timed out and none produced a
        # recommendation. An unreachable metrics source rarely times out a whole batch: the
        # other workloads tend to come back empty quickly instead. Healthy scans have never had
        # more than three batches in a row with any timeout. The deadline scales with the
        # workload count so a large, healthy cluster is not cut off as it grows.
        max_timed_out_batches = self._config.max_consecutive_timed_out_batches
        deadline_seconds = max(
            self._config.min_scan_deadline_seconds, len(workloads) * self._config.scan_seconds_per_workload
        )
        deadline = time.monotonic() + deadline_seconds
        timed_out_batches = 0

        for i in range(0, len(workloads), batch_size):
            batch_workloads = workloads[i : i + batch_size]
            # Reduce verbosity - only log every 10th batch or for small batches
            batch_num = i // batch_size + 1
            total_batches = (len(workloads) + batch_size - 1) // batch_size
            if batch_num % 10 == 0 or len(workloads) <= 50:
                logger.debug(f"Processing batch {batch_num}/{total_batches}")

            if time.monotonic() > deadline:
                logger.warning(
                    f"Aborting scan: still running after {deadline_seconds}s, " f"at batch {batch_num}/{total_batches}"
                )
                raise ScanAbortedError(f"Scan aborted: exceeded its {deadline_seconds}s deadline")

            # Process current batch concurrently
            timed_out_before = self._timed_out_objects
            batch_tasks = [self._gather_object_allocations(k8s_object) for k8s_object in batch_workloads]
            batch_results = await asyncio.gather(*batch_tasks, return_exceptions=True)

            batch_timed_out = self._timed_out_objects > timed_out_before
            batch_scanned = any(r is not None and not isinstance(r, BaseException) for r in batch_results)
            if batch_timed_out and not batch_scanned:
                timed_out_batches += 1
                if timed_out_batches >= max_timed_out_batches:
                    logger.warning(
                        f"Aborting scan: {timed_out_batches} batches in a row timed out with no recommendation, "
                        f"at batch {batch_num}/{total_batches}"
                    )
                    raise ScanAbortedError(
                        f"Scan aborted: timeouts and no recommendation in {timed_out_batches} batches in a row"
                    )
            else:
                timed_out_batches = 0

            for result in batch_results:
                if isinstance(result, Exception):
                    logger.error(f"Error processing workload: {result}")
                elif result is not None:
                    scans.append(result)

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
