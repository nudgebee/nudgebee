from __future__ import annotations

import asyncio
import logging
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional, Tuple

import numpy as np
import requests

from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.models.result import PodsTimeData
from server.recommendation.vertical_rightsizing.services.metric_base_service import MetricsService
from server.recommendation.vertical_rightsizing.strategy.strategies import BaseStrategy

logger = logging.getLogger("rightsizing")

# Elastic Agent / Metricbeat field paths. In this schema a metric's identity IS its
# field path, so these double as the dataset selector: only container-metricset
# documents carry them, which means an `exists` filter picks the right data stream
# without a term on `metricset.name` (a field a customer template may map as `text`,
# where a term would silently match nothing).
CONTAINER_CPU_NANOCORES = "kubernetes.container.cpu.usage.nanocores"
CONTAINER_MEMORY_WORKINGSET = "kubernetes.container.memory.workingset.bytes"

NAMESPACE_FIELD = "kubernetes.namespace"
POD_FIELD = "kubernetes.pod.name"
CONTAINER_FIELD = "kubernetes.container.name"

NANOCORES_PER_CORE = 1e9

# One workload's pods. Above this a workload is not a rightsizing candidate anyway,
# and a silently truncated terms aggregation would drop pods from the percentile.
MAX_PODS = 1000

DEFAULT_INDEX = "metrics-*"
REQUEST_TIMEOUT_SECONDS = 60


class ElasticsearchMetricsService(MetricsService):
    """Rightsizing metrics from an Elastic Agent / Metricbeat Elasticsearch.

    Follows DatadogMetricsService: the connection travels with the trigger request and
    this service queries the backend directly. Before it existed, an Elasticsearch
    account fell through to PrometheusMetricsService and queried a Prometheus that does
    not exist for such a cluster, so rightsizing produced nothing at all.

    Only the two container-level fields above are read here. The full metric table that
    the api-server utilisation path carries is not duplicated: rightsizing needs usage,
    not capacity or specs, which come from the workload inventory in Postgres.
    """

    def __init__(
        self,
        config: Config,
        account_id: str,
        executor: ThreadPoolExecutor,
        url: str,
        auth_type: Optional[str] = None,
        username: Optional[str] = None,
        password: Optional[str] = None,
        api_key: Optional[str] = None,
        bearer_token: Optional[str] = None,
        metrics_index: Optional[str] = None,
        tls_skip_verify: bool = False,
    ) -> None:
        super().__init__(config, account_id, executor)
        if not url:
            raise ValueError("Elasticsearch rightsizing requires a url")
        self._url = url.rstrip("/")
        self._index = metrics_index or DEFAULT_INDEX
        self._verify = not tls_skip_verify
        self._auth: Optional[Tuple[str, str]] = None
        self._headers: Dict[str, str] = {"Content-Type": "application/json"}

        auth_type = (auth_type or "basic").lower()
        if auth_type == "cognito":
            # SigV4 signing lives on the Go side only. Refusing here is the point:
            # an unsigned request comes back 403, which the caller would read as
            # "this cluster has no metrics".
            raise ValueError("Elasticsearch rightsizing does not support cognito authentication")
        if auth_type == "api_key":
            self._headers["Authorization"] = f"ApiKey {api_key}"
        elif auth_type == "bearer_token":
            self._headers["Authorization"] = f"Bearer {bearer_token}"
        else:
            self._auth = (username or "", password or "")

    # --- transport -----------------------------------------------------------

    def _search(self, body: Dict[str, Any]) -> Dict[str, Any]:
        resp = requests.post(
            f"{self._url}/{self._index}/_search",
            json=body,
            headers=self._headers,
            auth=self._auth,
            verify=self._verify,
            timeout=REQUEST_TIMEOUT_SECONDS,
        )
        resp.raise_for_status()
        payload: Dict[str, Any] = resp.json()
        return payload

    async def _async_search(self, body: Dict[str, Any]) -> Dict[str, Any]:
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(self.executor, lambda: self._search(body))

    def check_connection(self):
        try:
            resp = requests.get(
                f"{self._url}/_cluster/health",
                headers=self._headers,
                auth=self._auth,
                verify=self._verify,
                timeout=10,
            )
            resp.raise_for_status()
        except Exception as e:
            raise ConnectionError(f"Elasticsearch connection failed: {e}") from e

    # --- query building ------------------------------------------------------

    def _scope_filters(
        self,
        object: K8sObjectData,
        period: timedelta,
        value_field: str,
        pod_names: Optional[List[str]] = None,
    ) -> List[Dict[str, Any]]:
        now = datetime.now(timezone.utc)
        filters: List[Dict[str, Any]] = [
            {"exists": {"field": value_field}},
            {
                "range": {
                    "@timestamp": {
                        "gte": (now - period).isoformat(),
                        "lte": now.isoformat(),
                    }
                }
            },
        ]
        if object.namespace:
            filters.append({"term": {NAMESPACE_FIELD: object.namespace}})
        if object.container:
            filters.append({"term": {CONTAINER_FIELD: object.container}})

        if pod_names is None:
            pod_names = [p.name for p in (object.pods or []) if p.name]
        if pod_names:
            filters.append({"terms": {POD_FIELD: pod_names[:MAX_PODS]}})
        elif object.name:
            # Pod names are {workload}-{hash}[-{hash}]; the prefix covers every pod of
            # the workload. Only used when the pod list could not be resolved.
            filters.append({"prefix": {POD_FIELD: f"{object.name}-"}})
        return filters

    def _per_pod_body(
        self, object: K8sObjectData, period: timedelta, value_field: str, aggs: Dict[str, Any]
    ) -> Dict[str, Any]:
        return {
            "size": 0,
            "query": {"bool": {"filter": self._scope_filters(object, period, value_field)}},
            "aggs": {
                "pods": {
                    "terms": {"field": POD_FIELD, "size": MAX_PODS},
                    # last_seen stamps each pod's result, so the strategy gets a real
                    # observation time rather than "now".
                    "aggs": {**aggs, "last_seen": {"max": {"field": "@timestamp"}}},
                }
            },
        }

    # --- MetricsService ------------------------------------------------------

    async def get_cluster_summary(self) -> Dict[str, Any]:
        # Cluster capacity comes from the workload inventory, not from here — same as
        # the Datadog service.
        return {}

    async def load_pods(self, object: K8sObjectData, period: timedelta) -> List[PodData]:
        body = {
            "size": 0,
            "query": {
                "bool": {
                    "filter": self._scope_filters(
                        object,
                        period,
                        CONTAINER_CPU_NANOCORES,
                        # Deliberately empty: this call is what discovers the pod list,
                        # so it must not be narrowed by one.
                        pod_names=[],
                    )
                }
            },
            "aggs": {"pods": {"terms": {"field": POD_FIELD, "size": MAX_PODS}}},
        }
        try:
            data = await self._async_search(body)
        except Exception as e:
            logger.warning(f"Elasticsearch load_pods failed for {object}: {e}")
            return []
        buckets = data.get("aggregations", {}).get("pods", {}).get("buckets", [])
        return [PodData(name=b["key"], deleted=False) for b in buckets if b.get("key")]

    async def gather_data(
        self,
        object: K8sObjectData,
        strategy: BaseStrategy,
        period: timedelta,
        *,
        step: timedelta = timedelta(minutes=30),
    ) -> Dict[str, PodsTimeData]:
        """Return the strategy's metric keys, keyed by pod.

        Percentiles are computed by Elasticsearch rather than over downloaded points:
        one aggregation per workload instead of a full series per pod.
        """
        percentiles = sorted(
            {
                float(name.replace("cpu_percentile_", ""))
                for name in strategy.metrics
                if name.startswith("cpu_percentile_")
            }
        )

        cpu_aggs: Dict[str, Any] = {"count": {"value_count": {"field": CONTAINER_CPU_NANOCORES}}}
        if percentiles:
            cpu_aggs["pct"] = {"percentiles": {"field": CONTAINER_CPU_NANOCORES, "percents": percentiles}}
        mem_aggs: Dict[str, Any] = {
            "max": {"max": {"field": CONTAINER_MEMORY_WORKINGSET}},
            "count": {"value_count": {"field": CONTAINER_MEMORY_WORKINGSET}},
        }

        try:
            cpu_data, mem_data = await asyncio.gather(
                self._async_search(self._per_pod_body(object, period, CONTAINER_CPU_NANOCORES, cpu_aggs)),
                self._async_search(self._per_pod_body(object, period, CONTAINER_MEMORY_WORKINGSET, mem_aggs)),
            )
        except Exception as e:
            logger.error(f"Elasticsearch rightsizing query failed for {object}: {e}")
            return {key: {} for key in strategy.metrics}

        cpu_pods = _buckets_by_pod(cpu_data)
        mem_pods = _buckets_by_pod(mem_data)

        result: Dict[str, PodsTimeData] = {}
        for metric_name in strategy.metrics:
            if metric_name.startswith("cpu_percentile_"):
                percentile = float(metric_name.replace("cpu_percentile_", ""))
                result[metric_name] = {
                    pod: _point(ts, value / NANOCORES_PER_CORE)
                    for pod, (bucket, ts) in cpu_pods.items()
                    for value in [_percentile_value(bucket, percentile)]
                    if value is not None
                }
            elif metric_name == "MaxMemoryLoader":
                result[metric_name] = {
                    pod: _point(ts, bucket["max"]["value"])
                    for pod, (bucket, ts) in mem_pods.items()
                    if bucket.get("max", {}).get("value") is not None
                }
            elif metric_name in ("CPUAmountLoader", "MemoryAmountLoader"):
                pods = cpu_pods if metric_name == "CPUAmountLoader" else mem_pods
                result[metric_name] = {
                    pod: _point(ts, float(bucket.get("count", {}).get("value", 0)))
                    for pod, (bucket, ts) in pods.items()
                }
            elif metric_name == "MaxUsageMemoryLoader":
                # The cgroup high-water mark (container_memory_max_usage_bytes) has no
                # kubelet-stats equivalent, so there is nothing to read. The loader is
                # declared warning_on_no_data = False precisely so this degrades to
                # using the request as the limit floor.
                result[metric_name] = {}
            elif metric_name == "MaxOOMKilledMemoryLoader":
                # Same position as the Datadog service: no OOM-kill memory series.
                result[metric_name] = {}
            else:
                logger.warning(f"Unknown metric key '{metric_name}' requested by strategy, returning empty")
                result[metric_name] = {}

        return result


def _point(ts: float, value: float) -> np.ndarray:
    return np.array([[ts, float(value)]], dtype=np.float64)


def _percentile_value(bucket: Dict[str, Any], percentile: float) -> Optional[float]:
    """Read one percentile out of a percentiles aggregation.

    Elasticsearch keys the values by the percent formatted as a float string ("95.0"),
    which does not match the caller's "95" — look the value up by number, not by text.
    """
    # `or {}` rather than a get() default: Elasticsearch can send the key with an
    # explicit null, and get(key, default) returns the null rather than the default.
    values = (bucket.get("pct") or {}).get("values") or {}
    for key, value in values.items():
        try:
            if float(key) == percentile:
                return None if value is None else float(value)
        except (TypeError, ValueError):
            continue
    return None


def _buckets_by_pod(data: Dict[str, Any]) -> Dict[str, tuple]:
    """Map pod name -> (bucket, observation timestamp in epoch seconds)."""
    out: Dict[str, tuple] = {}
    aggs = (data.get("aggregations") or {}).get("pods") or {}
    if aggs.get("sum_other_doc_count"):
        # A capped terms aggregation drops whole pods, which changes a percentile
        # without changing anything visible about the result.
        logger.warning(
            f"Elasticsearch rightsizing: pod aggregation truncated at {MAX_PODS}, "
            f"{aggs['sum_other_doc_count']} documents excluded"
        )
    for bucket in aggs.get("buckets", []):
        name = bucket.get("key")
        if not name:
            continue
        # `max` on a date field answers in epoch milliseconds.
        last_seen = bucket.get("last_seen", {}).get("value")
        ts = float(last_seen) / 1000.0 if last_seen else datetime.now(timezone.utc).timestamp()
        out[name] = (bucket, ts)
    return out
