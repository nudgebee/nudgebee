"""Rightsizing metrics by name, for every provider at once.

The three services beside this one — Prometheus, Datadog, Elasticsearch — each write
their own provider's query language for the same eight numbers. Adding a fourth
provider meant writing those eight again, and the differences between the three were
never only dialect: Datadog has no percentile over time, Elasticsearch counts
documents rather than steps, and neither could answer the OOMKill join at all, so two
of them returned ``{}`` for it and a Prometheus account quietly got better
recommendations than anyone else.

This service names the DATA instead: ``container_cpu_usage_by_pod`` with
``aggregate="p99"``, and api-server resolves the account's provider and builds the
native query. The per-provider arithmetic that the numbers actually need — the
percentile Datadog cannot express, the sample-count conversion Elasticsearch needs —
lives there, once, next to the query that produced it.

What this file still owns is the shape the strategy reads: one array per pod, one row
per array, keyed by pod name, with every declared metric present even when it is
empty. That contract is not the utilisation layer's business, and getting it wrong is
silent — a missing key is a KeyError deep inside the strategy, and a zero where there
was no measurement is a recommendation to shrink a container to nothing.
"""

from __future__ import annotations

import asyncio
import logging
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional, Set

import numpy as np

from server.metrics.api_server_metrics import (
    ApiServerMetricsError,
    query_utilisation_via_api_server,
)
from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.models.replicas import ReplicaStats
from server.recommendation.vertical_rightsizing.models.result import PodsTimeData
from server.utils.utils import safe_get
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import PrometheusMetricsService
from server.recommendation.vertical_rightsizing.strategy.strategies import BaseStrategy

logger = logging.getLogger("rightsizing")

# The utilisation keys these loaders correspond to. One key per quantity; the
# statistic is the request's `aggregate`, not part of the name, which is why four
# percentiles of CPU are one key and not four.
CPU_USAGE_KEY = "container_cpu_usage_by_pod"
MEMORY_WORKING_SET_KEY = "container_memory_working_set_by_pod"
MEMORY_MAX_USAGE_KEY = "container_memory_max_usage_by_pod"
OOMKILLED_LIMIT_KEY = "container_oomkilled_limit_by_pod"

# Which strategy metric each non-percentile loader is answered by, and with which
# collapse. Percentiles are handled separately because their aggregate is a setting.
_LOADER_KEYS: Dict[str, tuple] = {
    "MaxMemoryLoader": (MEMORY_WORKING_SET_KEY, "max"),
    "MaxUsageMemoryLoader": (MEMORY_MAX_USAGE_KEY, "max"),
    "MaxOOMKilledMemoryLoader": (OOMKILLED_LIMIT_KEY, "max"),
    "CPUAmountLoader": (CPU_USAGE_KEY, "count"),
    "MemoryAmountLoader": (MEMORY_WORKING_SET_KEY, "count"),
}

_PERCENTILE_PREFIX = "cpu_percentile_"


class UtilisationMetricsService(MetricsService):
    """Rightsizing metrics through api-server's utilisation keys."""

    def __init__(
        self,
        config: Config,
        account_id: str,
        executor: ThreadPoolExecutor,
        tenant_id: str = "",
        metric_provider: str = "",
        metric_provider_source: str = "",
        inventory: Optional[PrometheusMetricsService] = None,
    ) -> None:
        super().__init__(config, account_id, executor)
        self.account_id = account_id
        self.tenant_id = tenant_id
        # Forwarded opaquely. api-server decides whether an account needs an override
        # at all (RightsizingMetricsHint); this service never interprets them, so a
        # provider added there does not become a case here.
        self.metric_provider = metric_provider
        self.metric_provider_source = metric_provider_source
        # Which pods a workload HAD over the window is an inventory question, not a
        # utilisation one: the Prometheus path walks kube_pod_owner to attribute even
        # pods that no longer exist. Where that walk is available it is kept, because
        # a name-prefix match is a weaker answer and this is not the PR to change it.
        self._inventory = inventory

    def check_connection(self):
        """api-server is reached per query; there is no separate connection to test."""
        return True

    async def load_pods(self, object: K8sObjectData, period: timedelta) -> List[PodData]:
        if self._inventory is not None:
            return await self._inventory.load_pods(object, period)

        end = datetime.now(timezone.utc)
        start = end - period
        try:
            # No aggregate: the point is the timestamps, and a collapsed answer has
            # exactly one, which would make every pod look equally recent.
            answered = await self._fetch(
                object, [MEMORY_WORKING_SET_KEY], aggregate="", start=start, end=end, pod_names=None
            )
        except ApiServerMetricsError as e:
            logger.warning(f"utilisation load_pods failed for {object}: {e}")
            return []

        # A pod last seen before this is gone, and only counts toward the savings
        # multiplier as a replica that no longer exists.
        live_cutoff = (end - timedelta(minutes=5)).timestamp()
        last_seen: Dict[str, float] = {}
        for series in answered.get(MEMORY_WORKING_SET_KEY) or []:
            pod = safe_get(series, ["metric", "pod"], "")
            if not pod:
                continue
            for ts in series.get("timestamps") or []:
                try:
                    seen = float(ts)
                except (TypeError, ValueError):
                    continue
                if seen > last_seen.get(pod, 0.0):
                    last_seen[pod] = seen
        return [PodData(name=name, deleted=seen < live_cutoff) for name, seen in last_seen.items()]

    async def load_replica_stats(self, object: K8sObjectData, period: timedelta) -> Optional[ReplicaStats]:
        if self._inventory is not None:
            return await self._inventory.load_replica_stats(object, period)
        # None means "use the live replica count", which is what the Datadog and
        # Elasticsearch services have always returned here.
        return None

    async def gather_data(
        self,
        object: K8sObjectData,
        strategy: BaseStrategy,
        period: timedelta,
        *,
        step: timedelta = timedelta(minutes=30),
    ) -> Dict[str, PodsTimeData]:
        """Every metric the strategy declared, keyed by pod, one row each.

        Never raises: a scan covers thousands of workloads and one unanswerable
        workload must not end it. An error becomes empty data for every metric, which
        the strategy reports as "No data" — the same outcome as a workload with no
        history, and distinguishable in the log.
        """
        requested: List[str] = [str(name) for name in strategy.metrics]
        empty: Dict[str, PodsTimeData] = {name: {} for name in requested}

        end = datetime.now(timezone.utc)
        start = end - period
        # Ask for the WORKLOAD, not for a list of pods. The request already names the
        # workload; enumerating its pods on top of that is the caller doing the layer's
        # job, and it makes the request scale with how often the workload redeploys.
        #
        # That breaks both providers, differently. On Prometheus the query exceeds
        # -search.maxQueryLen and is rejected outright (#39137). On Datadog it is
        # accepted and quietly degrades: one series per named pod blows the per-pod point
        # budget, so the rollup coarsens, and coverage -- buckets x bucket width -- stops
        # meaning anything, because a bucket holding one sample is credited with its
        # whole width.
        #
        # Workload scope returns a strict SUPERSET of this workload's pods, because the
        # layer falls back to a name prefix that a sibling can share. Verified on dev:
        # services-server returns 412 where 337 are correct, and an exact match where no
        # sibling shares the prefix. So the right pods are all present, and the wrong ones
        # are dropped below -- where the owner-chain answer already lives.
        # `is None`, not falsiness: an EMPTY list is the caller saying it resolved the
        # owner chain and found nothing, so it must keep nothing, not everything.
        wanted_pods = None if object.pods is None else {p.name for p in object.pods}

        plan = _plan_requests(requested)
        if not plan:
            return empty

        try:
            answers = await asyncio.gather(
                *[
                    self._fetch(object, keys, aggregate=agg, start=start, end=end, pod_names=None, step=step)
                    for agg, keys in plan
                ]
            )
        except ApiServerMetricsError as e:
            logger.warning(f"utilisation gather_data failed for {object}: {e}")
            return empty
        except Exception as e:  # noqa: BLE001 - see the docstring; a scan must survive
            logger.error(f"utilisation gather_data errored for {object}: {e}")
            return empty

        by_aggregate = {agg: answer for (agg, _), answer in zip(plan, answers)}

        result: Dict[str, PodsTimeData] = {}
        for name in requested:
            key_and_agg = _key_for_metric(name, strategy)
            if key_and_agg is None:
                logger.warning(f"unknown metric key '{name}' requested by strategy, returning empty")
                result[name] = {}
                continue
            key, aggregate = key_and_agg
            series = (by_aggregate.get(aggregate) or {}).get(key) or []
            result[name] = _pods_from_collapsed(series, object.container, wanted_pods)
        return result

    async def _fetch(
        self,
        object: K8sObjectData,
        keys: List[str],
        *,
        aggregate: str,
        start: datetime,
        end: datetime,
        pod_names: Optional[List[str]],
        step: Optional[Any] = None,
    ) -> Dict[str, List[dict]]:
        request = {
            "kind": (object.kind or "").lower(),
            "workload_namespace": object.namespace,
            "workload_name": object.name,
            "metrics": keys,
        }
        if object.container:
            request["container_name"] = object.container

        return await asyncio.get_running_loop().run_in_executor(
            self.executor,
            lambda: query_utilisation_via_api_server(
                account_id=self.account_id,
                tenant_id=self.tenant_id,
                request=request,
                start_time=start,
                end_time=end,
                aggregate=aggregate,
                step=step,
                pod_names=pod_names,
                metric_provider=self.metric_provider,
                metric_provider_source=self.metric_provider_source,
                timeout=getattr(self._config, "metrics_fetch_timeout", None),
            ),
        )


def _key_for_metric(name: str, strategy: BaseStrategy) -> Optional[tuple]:
    """The (utilisation key, aggregate) that answers one strategy metric."""
    if name in _LOADER_KEYS:
        return _LOADER_KEYS[name]
    if name.startswith(_PERCENTILE_PREFIX):
        percentile = name[len(_PERCENTILE_PREFIX) :]
        # The name carries the number the settings were rendered with, so a retuned
        # percentile reaches the query without a code change on either side.
        return CPU_USAGE_KEY, f"p{percentile}"
    return None


def _plan_requests(requested: List[str]) -> List[tuple]:
    """Group the requested metrics into one request per aggregate.

    ``aggregate`` applies to a whole request, so the grouping is forced: four CPU
    percentiles cannot share a call, while the two "max" memory keys and the two
    counts each can. For the default strategy this is six calls rather than eight.
    """
    by_aggregate: Dict[str, List[str]] = {}
    for name in requested:
        if name in _LOADER_KEYS:
            key, aggregate = _LOADER_KEYS[name]
        elif name.startswith(_PERCENTILE_PREFIX):
            key, aggregate = CPU_USAGE_KEY, f"p{name[len(_PERCENTILE_PREFIX):]}"
        else:
            continue
        keys = by_aggregate.setdefault(aggregate, [])
        if key not in keys:
            keys.append(key)
    return sorted(by_aggregate.items())


def _pods_from_collapsed(series: List[dict], container: str, wanted_pods: Optional[Set[str]] = None) -> PodsTimeData:
    """One [[timestamp, value]] row per pod, for the requested container only.

    The strategy reads ``values[0, 1]`` — a scalar per pod — so a collapsed answer is
    exactly the shape it wants and no reduction happens here. Filtering by container
    first matters because the keys are per container: a sidecar's memory under the
    application's pod name would otherwise be read as the application's.

    ``wanted_pods`` is the owner-chain answer, and it is applied AFTER the container
    filter rather than instead of it. Neither is sufficient alone: a prefix-sharing
    sibling runs a container of the same name (verified on dev, services-server-worker
    pods report ``container="services-server"``), so container alone lets the sibling in
    and pod alone lets a sidecar in.

    ``None`` means the caller resolved no pod set and every returned pod is kept. An
    EMPTY set is a different answer -- the caller looked and found none -- and keeps
    nothing.
    """
    pods: PodsTimeData = {}
    for entry in series or []:
        if container and (c := safe_get(entry, ["metric", "container"])) and c != container:
            continue
        pod = safe_get(entry, ["metric", "pod"], "")
        if not pod:
            continue
        if wanted_pods is not None and pod not in wanted_pods:
            continue
        timestamps = entry.get("timestamps") or []
        values = entry.get("values") or []
        if not timestamps or not values:
            continue
        try:
            ts = float(timestamps[-1])
            value = float(values[-1])
        except (TypeError, ValueError):
            continue
        # A pod with no measurement in the window comes back as a gap, which the
        # transport already drops. This is the second line of defence, because a NaN
        # that got through would make np.max() of the whole workload NaN and take
        # every other pod's answer with it.
        if np.isnan(value):
            continue
        pods[pod] = np.array([[ts, value]], dtype=np.float64)
    return pods
