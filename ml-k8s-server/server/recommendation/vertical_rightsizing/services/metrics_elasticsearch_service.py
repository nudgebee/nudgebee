from __future__ import annotations

import asyncio
import json
import logging
from dataclasses import dataclass, field
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional

import numpy as np

from server.metrics.api_server_metrics import METRIC_PROVIDER_ELASTICSEARCH, query_metrics_via_api_server
from server.recommendation.elasticsearch_client import ElasticsearchTransport
from server.utils.utils import safe_get
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
        tenant_id: str = "",
        via_api_server: bool = True,
        window: Optional[tuple] = None,
    ) -> None:
        super().__init__(config, account_id, executor)
        self.account_id = account_id
        self.tenant_id = tenant_id
        # The _search body is the same either way; only who holds the connection
        # differs. Through api-server the URL and password are never sent here, which
        # is the point: they used to arrive in the request body and be logged with it.
        self.via_api_server = via_api_server
        self._transport = (
            None
            if via_api_server
            # Transport (and its auth, including the cognito refusal) is shared with
            # volume rightsizing so the two cannot drift apart.
            else ElasticsearchTransport(
                url=url,
                auth_type=auth_type,
                username=username,
                password=password,
                api_key=api_key,
                bearer_token=bearer_token,
                metrics_index=metrics_index,
                tls_skip_verify=tls_skip_verify,
            )
        )

    # --- transport -----------------------------------------------------------

    def _search(self, body: Dict[str, Any]) -> List[dict]:
        """Run one _search and return its aggregations as flattened series.

        Both transports answer in the shape api-server returns for every provider:
        one entry per value, its bucket keys in ``metric``. The direct path converts
        its own response so there is one shape for the parsers to read.
        """
        if self.via_api_server:
            # The window is already a filter inside the body; api-server ANDs its own
            # range clause with it, so the two must agree. Sending the body's own
            # window keeps that a no-op.
            start, end = _body_window(body)
            return query_metrics_via_api_server(
                account_id=self.account_id,
                tenant_id=self.tenant_id,
                query=json.dumps(body),
                start_time=start,
                end_time=end,
                metric_provider=METRIC_PROVIDER_ELASTICSEARCH,
                request={"query_type": "dsl", "batch": True},
                timeout=getattr(self._config, "metrics_fetch_timeout", None),
            )
        if self._transport is None:
            # Only reachable with the rollback flag off and no connection in the
            # request. An ES account has no Prometheus to fall back to, so this has to
            # fail loudly rather than read as "this workload has no data".
            raise ValueError(f"no Elasticsearch connection was supplied for account {self.account_id}")
        return entries_from_es_aggregations(self._transport.search(body))

    async def _async_search(self, body: Dict[str, Any]) -> List[dict]:
        # Routed through _search rather than straight to the transport: _search is the
        # seam the tests substitute, and bypassing it would silently make every one of
        # them hit the network instead of their canned responses.
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(self.executor, lambda: self._search(body))

    def check_connection(self):
        if self.via_api_server:
            # api-server holds the connection; a broken one surfaces on the first query
            # with its own error, the same way the Prometheus path behaves.
            return
        # Delegated, not re-implemented: this used to read self._url/_headers/_auth/
        # _verify, which moved to the transport. Keeping a copy of those on the service
        # would let the two drift; delegating cannot.
        self._transport.check_connection()

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
            entries = await self._async_search(body)
        except Exception as e:
            logger.warning(f"Elasticsearch load_pods failed for {object}: {e}")
            return []
        names = []
        for entry in entries:
            name = safe_get(entry, ["metric", POD_LABEL])
            if name and name not in names:
                names.append(name)
        return [PodData(name=name, deleted=False) for name in names]

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

        cpu_pods = pod_aggregates(cpu_data)
        mem_pods = pod_aggregates(mem_data)

        result: Dict[str, PodsTimeData] = {}
        for metric_name in strategy.metrics:
            if metric_name.startswith("cpu_percentile_"):
                percentile = float(metric_name.replace("cpu_percentile_", ""))
                result[metric_name] = {
                    pod: _point(agg.timestamp, value / NANOCORES_PER_CORE)
                    for pod, agg in cpu_pods.items()
                    for value in [agg.percentile(percentile)]
                    if value is not None
                }
            elif metric_name == "MaxMemoryLoader":
                result[metric_name] = {
                    pod: _point(agg.timestamp, agg.max_value)
                    for pod, agg in mem_pods.items()
                    if agg.max_value is not None
                }
            elif metric_name in ("CPUAmountLoader", "MemoryAmountLoader"):
                pods = cpu_pods if metric_name == "CPUAmountLoader" else mem_pods
                result[metric_name] = {pod: _point(agg.timestamp, agg.count) for pod, agg in pods.items()}
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


@dataclass
class PodAggregate:
    """One pod's answers from a per-pod aggregation, whichever transport read them."""

    percentiles: Dict[float, float] = field(default_factory=dict)
    max_value: Optional[float] = None
    count: float = 0.0
    timestamp: float = 0.0

    def percentile(self, percentile: float) -> Optional[float]:
        """The value at a percentile, matched by number rather than by text:
        Elasticsearch keys these by the percent formatted as a float ("95.0"), which
        does not match the caller's "95".
        """
        return self.percentiles.get(float(percentile))


# Labels on the flattened series: the bucket key carries the aggregation's own name
# ("pods"), and a percentiles aggregation names the percentile in "statistic".
POD_LABEL = "pods"
NAME_LABEL = "__name__"
STATISTIC_LABEL = "statistic"


def pod_aggregates(entries: List[dict]) -> Dict[str, PodAggregate]:
    """Group flattened series into one PodAggregate per pod."""
    out: Dict[str, PodAggregate] = {}
    for entry in entries or []:
        labels = entry.get("metric") or {}
        pod = labels.get(POD_LABEL)
        if not pod:
            continue
        value = _last_value(entry)
        if value is None:
            continue
        agg = out.setdefault(pod, PodAggregate())
        name = labels.get(NAME_LABEL)
        if name == "pct":
            statistic = labels.get(STATISTIC_LABEL)
            if statistic is not None:
                try:
                    agg.percentiles[float(statistic)] = value
                except (TypeError, ValueError):
                    continue
        elif name == "max":
            agg.max_value = value
        elif name == "count":
            agg.count = value
        elif name == "last_seen":
            # `max` on a date field answers in epoch milliseconds. It stamps each pod's
            # result, so the strategy gets a real observation time rather than "now".
            agg.timestamp = value / 1000.0
    for agg in out.values():
        if not agg.timestamp:
            agg.timestamp = datetime.now(timezone.utc).timestamp()
    return out


def _last_value(entry: Dict[str, Any]) -> Optional[float]:
    """The series' value. These aggregations answer with exactly one."""
    values = entry.get("values") or []
    if not values:
        return None
    try:
        return float(values[-1])
    except (TypeError, ValueError):
        return None


def entries_from_es_aggregations(data: Dict[str, Any]) -> List[dict]:
    """An Elasticsearch response in the shape api-server returns for every provider.

    Only used by the direct-Elasticsearch fallback, and only for the body this service
    builds: a `pods` terms aggregation whose buckets carry pct / max / count /
    last_seen.
    """
    entries: List[dict] = []
    aggs = (data.get("aggregations") or {}).get("pods") or {}
    if aggs.get("sum_other_doc_count"):
        # A capped terms aggregation drops whole pods, which changes a percentile
        # without changing anything visible about the result.
        logger.warning(
            f"Elasticsearch rightsizing: pod aggregation truncated at {MAX_PODS}, "
            f"{aggs['sum_other_doc_count']} documents excluded"
        )

    def add(pod: str, name: str, value: Any, statistic: Optional[str] = None) -> None:
        if value is None:
            return
        labels = {POD_LABEL: pod, NAME_LABEL: name}
        if statistic is not None:
            labels[STATISTIC_LABEL] = statistic
        entries.append({"metric": labels, "timestamps": [0], "values": [value]})

    for bucket in aggs.get("buckets", []):
        pod = bucket.get("key")
        if not pod:
            continue
        for name in ("max", "count", "last_seen"):
            add(pod, name, (bucket.get(name) or {}).get("value"))
        for statistic, value in ((bucket.get("pct") or {}).get("values") or {}).items():
            add(pod, "pct", value, statistic=statistic)
        if not any(name in bucket for name in ("max", "count", "last_seen", "pct")):
            # A bare terms aggregation: load_pods asks only which pods exist.
            add(pod, "doc_count", bucket.get("doc_count", 0))
    return entries


def _body_window(body: Dict[str, Any]) -> tuple:
    """The time range the body already filters on, as datetimes.

    api-server ANDs its own range clause onto the body, so it has to be given the same
    window the body carries or the two would intersect to something narrower.
    """
    for clause in ((body.get("query") or {}).get("bool") or {}).get("filter") or []:
        rng = (clause.get("range") or {}).get("@timestamp") or {}
        start, end = rng.get("gte"), rng.get("lte")
        if start and end:
            return _parse_iso(start), _parse_iso(end)
    now = datetime.now(timezone.utc)
    return now - timedelta(days=14), now


def _parse_iso(value: Any) -> datetime:
    if isinstance(value, datetime):
        return value
    return datetime.fromisoformat(str(value).replace("Z", "+00:00"))
