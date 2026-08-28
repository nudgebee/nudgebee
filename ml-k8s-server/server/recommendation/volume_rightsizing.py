"""
Volume rightsizing recommendation service for PVC optimization.

This module provides PVC rightsizing recommendations by analyzing volume usage patterns
and generating optimized storage capacity suggestions. It migrates the functionality
from the volume_rightsize_analyzer.py and event_handler.py to centralize recommendation
logic in the ml-k8s-server.
"""

import asyncio
import json
import logging
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any, Tuple

import requests
from server.utils.utils import DatabaseEngine, get_trace
from server.utils.utils import MetricsServerConfigs
from server.metrics.prometheus_metrics import prometheus_range_query
from server.recommendation.elasticsearch_client import ElasticsearchTransport
from server.recommendation.storage_pricing import FALLBACK_STORAGE_RATE_PER_GB_MONTH, resolve_storage_pricing
from sqlalchemy import text

logger = logging.getLogger(__name__)
tracer = get_trace(__name__)

# Elastic Agent `volume` metricset. Only PVC-backed volumes carry the claim name, so
# ES_PVC_FIELD doubles as the selector that excludes configMap/projected/emptyDir
# mounts — those report the NODE's filesystem size and would dwarf a real claim.
ES_PVC_FIELD = "kubernetes.persistentvolumeclaim.name"
ES_NAMESPACE_FIELD = "kubernetes.namespace"
ES_VOLUME_USED_FIELD = "kubernetes.volume.fs.used.bytes"
ES_VOLUME_CAPACITY_FIELD = "kubernetes.volume.fs.capacity.bytes"

# Mirrors the Prometheus pair: a recent window for "current", 7 days for the peak.
ES_CURRENT_WINDOW_MINUTES = 10
ES_PEAK_WINDOW_MINUTES = 7 * 24 * 60

ES_MAX_NAMESPACES = 1000
ES_MAX_PVCS = 1000


@dataclass
class VolumeUsageData:
    """Volume usage data structure."""

    pvc_name: str
    namespace: str
    capacity_gb: float
    current_usage_gb: float
    usage_7_days_gb: Optional[float]
    pv_name: str
    storage_class: str
    metadata: Dict[str, Any]


@dataclass
class VolumeRecommendation:
    """Volume rightsizing recommendation."""

    pvc_name: str
    namespace: str
    current_capacity_gb: float
    recommended_capacity_gb: float
    current_usage_gb: float
    usage_7_days_gb: Optional[float]
    estimated_savings: float
    recommendation_reason: str
    priority: str
    metadata: Dict[str, Any]


@dataclass
class VolumeRightsizingResult:
    """Result of volume rightsizing analysis."""

    tenant_id: str
    account_id: str
    recommendations: List[VolumeRecommendation]
    total_recommendations: int
    total_potential_savings: float
    processing_time_ms: float
    score: float
    timestamp: datetime
    database_stored: bool = False


class VolumeRightsizingService:
    """Service for generating PVC rightsizing recommendations."""

    # Datadog metric names for PVC volume stats
    DD_VOLUME_USED_METRIC = "kubernetes.kubelet.volume.stats.used_bytes"
    DD_VOLUME_CAPACITY_METRIC = "kubernetes.kubelet.volume.stats.capacity_bytes"

    # Kind-to-tag mapping for Datadog
    DD_KIND_TO_TAG: Dict[str, str] = {
        "Deployment": "kube_deployment",
        "StatefulSet": "kube_stateful_set",
        "DaemonSet": "kube_daemon_set",
        "Job": "kube_job",
        "CronJob": "kube_cron_job",
    }

    def __init__(
        self,
        account_id: str,
        tenant_id: str,
        metrics_provider: Optional[str] = None,
        datadog_api_key: Optional[str] = None,
        datadog_app_key: Optional[str] = None,
        datadog_site: Optional[str] = None,
        elasticsearch: Optional[dict] = None,
    ):
        self.account_id = account_id
        self.tenant_id = tenant_id
        self.engine = DatabaseEngine.get_engine()
        self.prometheus_url = MetricsServerConfigs.url
        self.metrics_provider = metrics_provider or "prometheus"
        self.datadog_api_key = datadog_api_key
        self.datadog_app_key = datadog_app_key
        self.datadog_site = datadog_site
        self.elasticsearch = elasticsearch
        self._executor = ThreadPoolExecutor(max_workers=4)

        # Configuration from original implementation
        self.overprovisioning_threshold = 0.9  # 90% usage threshold
        self.minimum_savings_threshold = 0.1  # Minimum $1 savings to recommend

    async def generate_recommendations(
        self, namespace_filter: Optional[str] = None, max_recommendations: Optional[int] = None
    ) -> VolumeRightsizingResult:
        """Generate PVC rightsizing recommendations."""
        ctx_logger = self._get_contextual_logger(namespace_filter)
        start_time = time.time()

        with tracer.start_as_current_span(
            "generate_volume_rightsizing_recommendations",
            attributes={
                "pvc.tenant_id": self.tenant_id,
                "pvc.account_id": self.account_id,
                "pvc.namespace_filter": namespace_filter or "all",
            },
        ) as main_span:
            ctx_logger.info("Starting PVC rightsizing recommendation generation")

            try:
                # Step 1: Get PVC data from cluster
                volume_data = await self._get_volume_usage_data(namespace_filter)
                main_span.set_attribute("pvc.volumes_analyzed", len(volume_data))

                # Step 2: Generate recommendations
                recommendations = await self._generate_volume_recommendations(volume_data)

                # Step 3: Filter and prioritize recommendations
                filtered_recommendations = self._filter_and_prioritize_recommendations(
                    recommendations, max_recommendations
                )

                processing_time = (time.time() - start_time) * 1000
                total_savings = sum(rec.estimated_savings for rec in filtered_recommendations)

                # Step 4: Calculate score based on potential savings
                score = min(100.0, (total_savings / 100.0) * 10)  # Scale to 0-100

                result = VolumeRightsizingResult(
                    tenant_id=self.tenant_id,
                    account_id=self.account_id,
                    recommendations=filtered_recommendations,
                    total_recommendations=len(filtered_recommendations),
                    total_potential_savings=total_savings,
                    processing_time_ms=processing_time,
                    score=score,
                    timestamp=datetime.now(),
                )

                ctx_logger.info("PVC rightsizing recommendations generated successfully")
                return result

            except Exception as e:
                main_span.set_attribute("pvc.error", str(e))
                ctx_logger.error(f"Failed to generate PVC rightsizing recommendations: {e}")
                raise

    async def _get_volume_usage_data(self, namespace_filter: Optional[str] = None) -> List[VolumeUsageData]:
        """Get volume usage data from metrics provider and Kubernetes."""
        ctx_logger = self._get_contextual_logger(namespace_filter)
        with tracer.start_as_current_span("get_volume_usage_data") as span:
            try:
                if self.metrics_provider == "datadog":
                    return await self._get_volume_usage_data_datadog(namespace_filter, ctx_logger, span)

                if self.metrics_provider == "ES":
                    return await self._get_volume_usage_data_elasticsearch(namespace_filter, ctx_logger, span)

                # Prometheus path (default)
                current_usage_query = (
                    "max by (persistentvolumeclaim, namespace) (kubelet_volume_stats_used_bytes{ __CLUSTER__ })"
                )
                usage_7_days_query = "max_over_time(kubelet_volume_stats_used_bytes{ __CLUSTER__ }[7d])"
                capacity_query = (
                    "max by (persistentvolumeclaim, namespace) (kubelet_volume_stats_capacity_bytes{ __CLUSTER__ })"
                )

                current_usage_data = await self._query_prometheus_range(current_usage_query, timedelta(minutes=10))
                usage_7_days_data = await self._query_prometheus_range(usage_7_days_query, timedelta(days=7))
                capacity_data = await self._query_prometheus_range(capacity_query, timedelta(minutes=10))

                current_usage_map = self._build_usage_map(current_usage_data)
                usage_7_days_map = self._build_usage_map(usage_7_days_data)
                capacity_map = self._build_usage_map(capacity_data)

                pvc_metadata = await self._get_pvc_metadata(namespace_filter)

                volume_data = []
                for pvc_key, metadata in pvc_metadata.items():
                    try:
                        pvc_name = metadata["name"]
                        namespace = metadata["namespace"]
                        if namespace_filter and namespace != namespace_filter:
                            continue

                        usage_key = f"{pvc_name}_{namespace}"

                        current_usage_gb = current_usage_map.get(usage_key, 0.0) / (1024**3)
                        usage_7_days_gb = (
                            (val / (1024**3)) if (val := usage_7_days_map.get(usage_key)) is not None else None
                        )
                        capacity_gb = capacity_map.get(usage_key, 0.0) / (1024**3)

                        if capacity_gb > 0:
                            volume_data.append(
                                VolumeUsageData(
                                    pvc_name=pvc_name,
                                    namespace=namespace,
                                    capacity_gb=capacity_gb,
                                    current_usage_gb=current_usage_gb,
                                    usage_7_days_gb=usage_7_days_gb,
                                    pv_name=metadata.get("pv_name", ""),
                                    storage_class=metadata.get("storage_class", ""),
                                    metadata=metadata,
                                )
                            )
                    except Exception as e:
                        ctx_logger.warning(f"Failed to process PVC {pvc_key}: {e}")
                        continue

                span.set_attribute("pvc.volume_data_count", len(volume_data))
                ctx_logger.info(f"Retrieved usage data for {len(volume_data)} volumes")
                return volume_data

            except Exception as e:
                span.set_attribute("pvc.error", str(e))
                ctx_logger.error(f"Failed to get volume usage data: {e}")
                raise

    async def _get_volume_usage_data_elasticsearch(self, namespace_filter, ctx_logger, span) -> List[VolumeUsageData]:
        """Per-PVC usage and capacity from the Elastic Agent `volume` metricset.

        The metricset emits one document per mounted volume and sets
        `kubernetes.persistentvolumeclaim.name` only on PVC-backed volumes, so an
        `exists` on that field both identifies the claim and excludes the rest. That
        exclusion is required, not tidy: configMap/projected/emptyDir mounts report the
        NODE's filesystem size, so including them makes a small claim look enormous.

        Two searches mirror the Prometheus pair — a recent window for "current" and a
        7-day window for the peak — and capacity is read from the recent one.
        """
        transport = ElasticsearchTransport.from_config(self.elasticsearch)

        async def usage_maps(minutes: int) -> Tuple[Dict[str, float], Dict[str, float]]:
            body = self._es_volume_body(minutes, namespace_filter)
            data = await transport.async_search(body, self._executor)
            return self._build_es_usage_maps(data)

        # The two windows are independent searches, so they go out together. Two is also
        # the whole fan-out — the executor backing them is already capped at 4 workers —
        # so no extra semaphore is needed to keep this off a customer's cluster.
        (current_used, capacity), (peak_used, _) = await asyncio.gather(
            usage_maps(ES_CURRENT_WINDOW_MINUTES),
            usage_maps(ES_PEAK_WINDOW_MINUTES),
        )

        pvc_metadata = await self._get_pvc_metadata(namespace_filter)

        volume_data: List[VolumeUsageData] = []
        for pvc_key, metadata in pvc_metadata.items():
            try:
                pvc_name = metadata["name"]
                namespace = metadata["namespace"]
                if namespace_filter and namespace != namespace_filter:
                    continue

                usage_key = f"{pvc_name}_{namespace}"
                capacity_gb = capacity.get(usage_key, 0.0) / (1024**3)
                if capacity_gb <= 0:
                    # No volume document for this claim — it may be unmounted, or the
                    # agent may not run on its node. Recommending against a capacity of
                    # zero would propose shrinking every such claim to nothing.
                    continue

                volume_data.append(
                    VolumeUsageData(
                        pvc_name=pvc_name,
                        namespace=namespace,
                        capacity_gb=capacity_gb,
                        current_usage_gb=current_used.get(usage_key, 0.0) / (1024**3),
                        usage_7_days_gb=((val / (1024**3)) if (val := peak_used.get(usage_key)) is not None else None),
                        pv_name=metadata.get("pv_name", ""),
                        storage_class=metadata.get("storage_class", ""),
                        metadata=metadata,
                    )
                )
            except Exception as e:
                ctx_logger.warning(f"Failed to process PVC {pvc_key}: {e}")
                continue

        span.set_attribute("pvc.volume_data_count", len(volume_data))
        ctx_logger.info(f"Retrieved usage data for {len(volume_data)} volumes from Elasticsearch")
        return volume_data

    def _es_volume_body(self, minutes: int, namespace_filter: Optional[str]) -> Dict[str, Any]:
        """One search: namespace -> claim -> max(used), max(capacity) over the window."""
        filters: List[Dict[str, Any]] = [
            {"exists": {"field": ES_PVC_FIELD}},
            {"range": {"@timestamp": {"gte": f"now-{minutes}m", "lte": "now"}}},
        ]
        if namespace_filter:
            filters.append({"term": {ES_NAMESPACE_FIELD: namespace_filter}})
        return {
            "size": 0,
            "query": {"bool": {"filter": filters}},
            "aggs": {
                "ns": {
                    "terms": {"field": ES_NAMESPACE_FIELD, "size": ES_MAX_NAMESPACES},
                    "aggs": {
                        "pvc": {
                            "terms": {"field": ES_PVC_FIELD, "size": ES_MAX_PVCS},
                            "aggs": {
                                "used": {"max": {"field": ES_VOLUME_USED_FIELD}},
                                "capacity": {"max": {"field": ES_VOLUME_CAPACITY_FIELD}},
                            },
                        }
                    },
                }
            },
        }

    def _build_es_usage_maps(self, data: Dict[str, Any]) -> Tuple[Dict[str, float], Dict[str, float]]:
        """Flatten the nested aggregation into the {pvc}_{namespace} maps used above."""
        used_map: Dict[str, float] = {}
        capacity_map: Dict[str, float] = {}
        ns_buckets = ((data or {}).get("aggregations") or {}).get("ns") or {}
        if ns_buckets.get("sum_other_doc_count"):
            # A capped terms aggregation drops whole namespaces, which would read as
            # "those claims have no usage" rather than as missing data.
            logger.warning(
                f"Elasticsearch volume aggregation truncated at {ES_MAX_NAMESPACES} namespaces; "
                f"{ns_buckets['sum_other_doc_count']} documents excluded"
            )
        # Skip, do not abort: one malformed bucket must not cost every other claim its
        # recommendation. A claim that is skipped simply has no entry, which the caller
        # already treats as "no data for this claim" rather than as a zero.
        for ns in ns_buckets.get("buckets", []) or []:
            if not isinstance(ns, dict) or not (namespace := ns.get("key")):
                continue
            for pvc in (ns.get("pvc") or {}).get("buckets") or []:
                if not isinstance(pvc, dict) or not (pvc_name := pvc.get("key")):
                    continue
                key = f"{pvc_name}_{namespace}"
                try:
                    if (used := (pvc.get("used") or {}).get("value")) is not None:
                        used_map[key] = float(used)
                    if (cap := (pvc.get("capacity") or {}).get("value")) is not None:
                        capacity_map[key] = float(cap)
                except (TypeError, ValueError):
                    logger.warning(f"Elasticsearch volume aggregation: unreadable values for {key}; skipping")
                    continue
        return used_map, capacity_map

    async def _query_prometheus_range(self, query: str, duration: timedelta) -> List[Dict]:
        """Execute Prometheus range query."""
        try:
            total_minutes = int(duration.total_seconds() / 60)
            response_data = prometheus_range_query(
                endpoint=self.prometheus_url,
                promql_query=query,
                account_id=self.account_id,
                duration_minutes=total_minutes,
                step="1m",
            )
            return response_data if response_data and isinstance(response_data, list) else []
        except Exception as e:
            logger.error(f"Prometheus query failed for query: {query[:100]}..., error: {str(e)}")
            return []

    def _build_usage_map(self, prometheus_data: List[Dict]) -> Dict[str, float]:
        """Build usage map from Prometheus data.

        The relay returns a `series_list_result` shape: each series has
        separate `timestamps: [...]` and `values: ["str", "str", ...]` arrays
        (values are scalar value strings, NOT [ts, val] pairs). For instant
        queries the agent emits `vector_result` items with `value: [ts, "str"]`
        — both shapes are handled here.
        """
        usage_map = {}
        for item in prometheus_data:
            try:
                metric = item.get("metric", {})
                pvc_name = metric.get("persistentvolumeclaim")
                namespace = metric.get("namespace")
                if not (pvc_name and namespace):
                    continue
                key = f"{pvc_name}_{namespace}"
                values = item.get("values")
                if values:
                    last = values[-1]
                    # range query — series_list_result: scalar value strings
                    # instant query path may also land here as [ts, "val"] pairs;
                    # accept both rather than guess.
                    raw = last[1] if isinstance(last, (list, tuple)) else last
                    usage_map[key] = float(raw)
                    continue
                value = item.get("value")
                if value:
                    usage_map[key] = float(value[1])
            except (ValueError, IndexError, KeyError, TypeError) as e:
                logger.warning(f"Failed to parse Prometheus metric: {item}, error: {e}")
                continue
        logger.info(f"Built usage map with {len(usage_map)} entries")
        return usage_map

    def _query_datadog(self, query: str, from_ts: int, to_ts: int) -> dict:
        """Execute a Datadog metrics query and return raw response JSON."""
        headers: dict[str, str] = {}
        if self.datadog_api_key:
            headers["DD-API-KEY"] = self.datadog_api_key
        if self.datadog_app_key:
            headers["DD-APPLICATION-KEY"] = self.datadog_app_key
        resp = requests.get(
            f"https://{self.datadog_site}/api/v1/query",
            headers=headers,
            params={
                "from": str(from_ts),
                "to": str(to_ts),
                "query": query,
            },
            timeout=60,
        )
        resp.raise_for_status()
        result: dict = resp.json()
        return result

    async def _async_query_datadog(self, query: str, from_ts: int, to_ts: int) -> dict:
        """Async wrapper around _query_datadog."""
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(
            self._executor,
            lambda: self._query_datadog(query, from_ts, to_ts),
        )

    def _build_datadog_usage_map(self, data: dict) -> Dict[str, float]:
        """Build pvc_namespace usage map from Datadog series response."""
        usage_map: Dict[str, float] = {}
        for series in data.get("series", []):
            scope = series.get("scope", "")
            tag_map = {}
            for part in scope.split(","):
                part = part.strip()
                if ":" in part:
                    k, v = part.split(":", 1)
                    tag_map[k] = v

            pvc_name = tag_map.get("persistentvolumeclaim", "")
            namespace = tag_map.get("kube_namespace", "")
            if not pvc_name or not namespace:
                continue

            key = f"{pvc_name}_{namespace}"
            pointlist = series.get("pointlist", [])
            if pointlist:
                # Take the last non-None value
                for point in reversed(pointlist):
                    if len(point) >= 2 and point[1] is not None:
                        usage_map[key] = float(point[1])
                        break

        logger.info(f"Built Datadog usage map with {len(usage_map)} entries")
        return usage_map

    async def _get_volume_usage_data_datadog(
        self, namespace_filter: Optional[str], ctx_logger, span
    ) -> List[VolumeUsageData]:
        """Get volume usage data from Datadog."""
        now = datetime.utcnow()
        from_ts = int((now - timedelta(minutes=10)).timestamp())
        to_ts = int(now.timestamp())
        from_ts_7d = int((now - timedelta(days=7)).timestamp())

        ns_filter = f"kube_namespace:{namespace_filter}," if namespace_filter else ""

        current_usage_query = (
            f"max:kubernetes.kubelet.volume.stats.used_bytes{{{ns_filter}*}}"
            f" by {{persistentvolumeclaim,kube_namespace}}"
        )
        capacity_query = (
            f"max:kubernetes.kubelet.volume.stats.capacity_bytes{{{ns_filter}*}}"
            f" by {{persistentvolumeclaim,kube_namespace}}"
        )
        usage_7_days_query = (
            f"max:kubernetes.kubelet.volume.stats.used_bytes{{{ns_filter}*}}"
            f".rollup(max, 604800)"
            f" by {{persistentvolumeclaim,kube_namespace}}"
        )

        try:
            current_data, capacity_data, usage_7d_data = await asyncio.gather(
                self._async_query_datadog(current_usage_query, from_ts, to_ts),
                self._async_query_datadog(capacity_query, from_ts, to_ts),
                self._async_query_datadog(usage_7_days_query, from_ts_7d, to_ts),
            )
        except Exception as e:
            ctx_logger.error(f"Datadog volume metrics query failed: {e}")
            return []

        current_usage_map = self._build_datadog_usage_map(current_data)
        capacity_map = self._build_datadog_usage_map(capacity_data)
        usage_7_days_map = self._build_datadog_usage_map(usage_7d_data)

        logger.info(
            f"Datadog volume maps - current_usage: {len(current_usage_map)} keys, "
            f"capacity: {len(capacity_map)} keys, usage_7d: {len(usage_7_days_map)} keys"
        )
        if current_usage_map and not usage_7_days_map:
            logger.warning(
                f"Datadog 7-day usage map is empty while current usage has data. "
                f"7d query: {usage_7_days_query}, from: {from_ts_7d}, to: {to_ts}, "
                f"7d response series count: {len(usage_7d_data.get('series', []))}, "
                f"7d response status: {usage_7d_data.get('status')}, "
                f"7d response errors: {usage_7d_data.get('errors')}"
            )
        elif usage_7_days_map and current_usage_map:
            current_keys_sample = list(current_usage_map.keys())[:3]
            usage_7d_keys_sample = list(usage_7_days_map.keys())[:3]
            logger.info(f"Datadog key samples - current: {current_keys_sample}, 7d: {usage_7d_keys_sample}")

        # Get PVC metadata from relay server (same as Prometheus path)
        pvc_metadata = await self._get_pvc_metadata(namespace_filter)

        volume_data = []
        # If we have PVC metadata from relay server, use it
        if pvc_metadata:
            for pvc_key, metadata in pvc_metadata.items():
                try:
                    pvc_name = metadata["name"]
                    namespace = metadata["namespace"]
                    if namespace_filter and namespace != namespace_filter:
                        continue
                    usage_key = f"{pvc_name}_{namespace}"
                    current_usage_gb = current_usage_map.get(usage_key, 0.0) / (1024**3)
                    usage_7_days_gb = (
                        (val / (1024**3)) if (val := usage_7_days_map.get(usage_key)) is not None else None
                    )
                    capacity_gb = capacity_map.get(usage_key, 0.0) / (1024**3)
                    if capacity_gb > 0:
                        volume_data.append(
                            VolumeUsageData(
                                pvc_name=pvc_name,
                                namespace=namespace,
                                capacity_gb=capacity_gb,
                                current_usage_gb=current_usage_gb,
                                usage_7_days_gb=usage_7_days_gb,
                                pv_name=metadata.get("pv_name", ""),
                                storage_class=metadata.get("storage_class", ""),
                                metadata=metadata,
                            )
                        )
                except Exception as e:
                    ctx_logger.warning(f"Failed to process PVC {pvc_key}: {e}")
                    continue
        else:
            # Fallback: build volume data directly from Datadog metrics (no relay server metadata)
            all_keys = set(current_usage_map.keys()) | set(capacity_map.keys())
            for key in all_keys:
                try:
                    parts = key.rsplit("_", 1)
                    if len(parts) != 2:
                        continue
                    pvc_name, namespace = parts
                    if namespace_filter and namespace != namespace_filter:
                        continue
                    current_usage_gb = current_usage_map.get(key, 0.0) / (1024**3)
                    usage_7_days_gb = (val / (1024**3)) if (val := usage_7_days_map.get(key)) is not None else None
                    capacity_gb = capacity_map.get(key, 0.0) / (1024**3)
                    if capacity_gb > 0:
                        volume_data.append(
                            VolumeUsageData(
                                pvc_name=pvc_name,
                                namespace=namespace,
                                capacity_gb=capacity_gb,
                                current_usage_gb=current_usage_gb,
                                usage_7_days_gb=usage_7_days_gb,
                                pv_name="",
                                storage_class="",
                                metadata={"name": pvc_name, "namespace": namespace},
                            )
                        )
                except Exception as e:
                    ctx_logger.warning(f"Failed to process Datadog PVC {key}: {e}")
                    continue

        span.set_attribute("pvc.volume_data_count", len(volume_data))
        ctx_logger.info(f"Retrieved usage data for {len(volume_data)} volumes from Datadog")
        return volume_data

    async def _get_pvc_metadata(self, namespace_filter: Optional[str] = None) -> Dict[str, Dict[str, Any]]:
        """Get PVC metadata from Kubernetes API via relay server."""
        try:
            from server.utils.utils import get_http_session_with_retry
            from opentelemetry import context
            from opentelemetry.propagate import inject

            headers = {"Content-Type": "application/json", "X-SECRET-KEY": MetricsServerConfigs.secret}
            headers_with_context = headers.copy()
            current_context = context.get_current()
            if current_context:
                inject(headers_with_context, current_context)

            # Prepare action parameters for get_resource action.
            # The Go agent (nudgebee-agent#34) requires `version`
            # for every get_resource call — without it the agent rejects with
            # 500 `kube: version and resource_type are required`, the response
            # has no `findings`, and we silently fall through to "No PVC data
            # found in relay server response" → 0 recommendations. PVC is core
            # K8s so version is "v1" and group is empty.
            action_params = {
                "version": "v1",
                "resource_type": "persistentvolumeclaims",
                "all_namespaces": not bool(namespace_filter),
                "namespace": [namespace_filter] if namespace_filter else [],
            }

            payload = json.dumps(
                {
                    "no_sinks": True,
                    "body": {
                        "account_id": str(self.account_id),
                        "action_name": "get_resource",
                        "action_params": action_params,
                    },
                    "cache": False,
                }
            )

            response = get_http_session_with_retry().request(
                "POST", self.prometheus_url, headers=headers_with_context, data=payload, timeout=60
            )

            if response.status_code != 200:
                msg = (
                    f"Failed to fetch PVC metadata from relay server. "
                    f"status: {response.status_code}: response: {response.text}"
                )
                logger.error(msg)
                raise ValueError(msg)

            data = response.json()

            # Parse relay server response to extract PVC data
            pvc_data = []
            if "data" in data:
                findings_data = data["data"]
                if "findings" in findings_data and len(findings_data["findings"]) > 0:
                    first_finding = findings_data["findings"][0]
                    if "evidence" in first_finding and len(first_finding["evidence"]) > 0:
                        first_evidence = first_finding["evidence"][0]
                        if "data" in first_evidence:
                            # The evidence data is a JSON string containing an array with type/data objects
                            evidence_data = json.loads(first_evidence["data"])
                            # The evidence_data is a list with objects having 'type' and 'data' fields
                            if evidence_data and len(evidence_data) > 0:
                                for item in evidence_data:
                                    if item.get("type") == "json" and "data" in item:
                                        # The actual PVC data is in the 'data' field, which is another JSON string
                                        pvc_data = json.loads(item["data"])
                                        break
                        else:
                            logger.error(
                                f"Failed to fetch PVC metadata from relay server. " f"evidence: {first_evidence}"
                            )
                            raise ValueError(
                                "Failed to fetch PVC metadata from relay server. " "response data evidence is empty"
                            )
                    else:
                        logger.error(f"Failed to fetch PVC metadata from relay server. " f"evidence: {first_finding}")
                        raise ValueError(
                            "Failed to fetch PVC metadata from relay server. " "response data findings is empty"
                        )
                else:
                    logger.warning("No PVC data found in relay server response")
                    return {}

            # Convert relay server response to expected format
            pvc_metadata = {}
            for pvc in pvc_data:
                try:
                    metadata = pvc.get("metadata", {})
                    pvc_name = metadata.get("name")
                    namespace = metadata.get("namespace")
                    spec = pvc.get("spec", {})
                    status = pvc.get("status", {})

                    if pvc_name and namespace:
                        # Skip if namespace filter doesn't match
                        if namespace_filter and namespace != namespace_filter:
                            continue

                        key = f"{pvc_name}_{namespace}"
                        pvc_metadata[key] = {
                            "id": None,  # Not available from Kubernetes API
                            "resource_id": f"{namespace}/{pvc_name}",
                            "name": pvc_name,
                            "namespace": namespace,
                            # Using snake_case from response
                            "pv_name": spec.get("volume_name", ""),
                            "storage_class": spec.get("storage_class_name", ""),
                            "capacity": status.get("capacity", {}).get("storage", ""),
                            "metadata": pvc,
                        }
                except (KeyError, TypeError) as e:
                    logger.warning(f"Failed to parse PVC metadata: {e}")
                    continue

            logger.info(f"Retrieved metadata for {len(pvc_metadata)} PVCs from relay server")
            return pvc_metadata

        except Exception as e:
            logger.error(f"Failed to get PVC metadata from relay server: {e}")
            return {}

    async def _generate_volume_recommendations(self, volume_data: List[VolumeUsageData]) -> List[VolumeRecommendation]:
        """Generate volume recommendations."""
        recommendations = []
        with tracer.start_as_current_span("generate_volume_recommendations") as span:
            storage_classes = await self._get_storage_classes()
            provider = self._get_k8s_provider()
            for volume in volume_data:
                try:
                    pvc_object = volume.metadata.get("metadata") or {
                        "spec": {"storage_class_name": volume.storage_class}
                    }
                    pricing = resolve_storage_pricing(pvc_object, storage_classes=storage_classes, provider=provider)
                    recommendation = self._analyze_single_volume(volume, pricing)
                    if recommendation:
                        recommendations.append(recommendation)
                except Exception as e:
                    logger.warning(f"Failed to analyze volume {volume.pvc_name}: {e}")
                    continue
            span.set_attribute("pvc.recommendations_generated", len(recommendations))
        return recommendations

    def _get_k8s_provider(self) -> str:
        """Canonical provider ("aws"/"gcp"/"azure") from agent telemetry, or ""."""
        query = text("""SELECT k8s_provider
               FROM agent
               WHERE cloud_account_id = :account_id
               AND k8s_provider IS NOT NULL
               AND k8s_provider != ''""")
        try:
            with self.engine.connect() as conn:
                rows = conn.execute(query, {"account_id": self.account_id}).fetchall()
        except Exception as e:
            logger.warning(f"k8s_provider lookup failed for account {self.account_id}: {e}")
            return ""
        if not rows:
            return ""
        provider = (rows[0][0] or "").lower()
        return {"eks": "aws", "gke": "gcp", "aks": "azure"}.get(provider, provider)

    async def _get_storage_classes(self) -> Dict[str, Dict[str, Any]]:
        """StorageClass objects by name via relay, for per-class pricing.

        A failure must not fail the scan — pricing degrades to provider
        defaults, so this swallows errors and returns {}.
        """
        try:
            from server.utils.utils import get_http_session_with_retry

            headers = {"Content-Type": "application/json", "X-SECRET-KEY": MetricsServerConfigs.secret}
            payload = json.dumps(
                {
                    "no_sinks": True,
                    "body": {
                        "account_id": str(self.account_id),
                        "action_name": "get_resource",
                        "action_params": {
                            "group": "storage.k8s.io",
                            "version": "v1",
                            "resource_type": "storageclasses",
                            "all_namespaces": False,
                        },
                    },
                    "cache": False,
                }
            )
            response = get_http_session_with_retry().request(
                "POST", self.prometheus_url, headers=headers, data=payload, timeout=60
            )
            if response.status_code != 200:
                logger.warning(f"storageclasses fetch failed: {response.status_code}: {response.text[:200]}")
                return {}

            sc_list: List[Dict[str, Any]] = []
            findings = response.json().get("data", {}).get("findings", [])
            if findings:
                for item in json.loads(findings[0].get("evidence", [{}])[0].get("data", "[]")):
                    if item.get("type") == "json" and "data" in item:
                        sc_list = json.loads(item["data"])
                        break
            by_name = {}
            for sc in sc_list:
                name = sc.get("metadata", {}).get("name")
                if name:
                    by_name[name] = sc
            logger.info(f"Retrieved {len(by_name)} storage classes from relay server")
            return by_name
        except Exception as e:
            logger.warning(f"storageclasses fetch failed, pricing falls back to provider defaults: {e}")
            return {}

    def _analyze_single_volume(
        self, volume: VolumeUsageData, pricing: Dict[str, Any]
    ) -> Optional[VolumeRecommendation]:
        """Analyze single volume, assuming input is already in GB."""
        pvc_id = f"{volume.namespace}/{volume.pvc_name}"
        logger.info(f"[{pvc_id}] Starting analysis...")
        try:
            capacity_gb = volume.capacity_gb
            current_usage_gb = volume.current_usage_gb
            usage_7_days_gb = volume.usage_7_days_gb

            if capacity_gb == 0:
                logger.warning(f"[{pvc_id}] SKIPPING: Analysis stopped due to zero capacity.")
                return None

            utilization = (current_usage_gb / capacity_gb) if capacity_gb > 0 else 0
            logger.info(
                f"[{pvc_id}] Capacity: {capacity_gb:.2f} GB, Current Usage: {current_usage_gb:.2f} GB, "
                f"Utilization: {utilization:.2%}"
            )

            recommended_size_gb, savings, reason, priority, algorithm = 0.0, 0.0, "", "Low", "N/A"

            price_per_gb = pricing["price_per_gb"]
            if utilization > self.overprovisioning_threshold:
                recommended_size_gb = capacity_gb * 1.2
                savings = -(capacity_gb * 0.2 * price_per_gb)
                reason, priority, algorithm = (
                    "Volume is over 90% full, recommend increasing size",
                    "High",
                    "High Utilization Threshold",
                )
                logger.info(
                    f"[{pvc_id}] High utilization detected. Recommending increase to {recommended_size_gb:.2f} GB."
                )

            elif usage_7_days_gb is not None:
                logger.info(f"[{pvc_id}] Historical data found. Analyzing for downsizing.")
                current_free_gb = capacity_gb - current_usage_gb
                free_7_days_ago_gb = capacity_gb - usage_7_days_gb

                pfss90_gb = current_free_gb + 10 * (current_free_gb - free_7_days_ago_gb)
                mfss_gb = max(0.0, min(pfss90_gb, free_7_days_ago_gb))
                recommended_size_gb = (capacity_gb - mfss_gb) * 1.3
                logger.info(
                    f"[{pvc_id}] Projected Max Free Space: {mfss_gb:.2f} GB. Recommended Size: "
                    f"{recommended_size_gb:.2f} GB."
                )

                if 0 < recommended_size_gb < capacity_gb:
                    storage_save_gb = capacity_gb - recommended_size_gb
                    savings = price_per_gb * storage_save_gb
                    logger.info(f"[{pvc_id}] Potential downsizing identified. Savings: ${savings:.2f}.")

                    if savings >= self.minimum_savings_threshold:
                        reason, algorithm = "Volume can be downsized based on usage trends (PFSS90)", "PFSS90"
                        priority = "Medium" if savings < 10 else "High"
                    else:
                        logger.info(f"[{pvc_id}] SKIPPING: Savings below threshold.")
                        return None
                else:
                    logger.info(f"[{pvc_id}] SKIPPING: Recommended size is not smaller than current capacity.")
                    return None
            else:
                logger.info(f"[{pvc_id}] SKIPPING: No historical data available.")
                return None

            if abs(recommended_size_gb - capacity_gb) < 0.1:
                logger.info(f"[{pvc_id}] SKIPPING: Calculated change is less than 100MB.")
                return None

            logger.info(f"[{pvc_id}] SUCCESS: Generating recommendation.")
            return VolumeRecommendation(
                pvc_name=volume.pvc_name,
                namespace=volume.namespace,
                current_capacity_gb=capacity_gb,
                recommended_capacity_gb=recommended_size_gb,
                current_usage_gb=current_usage_gb,
                usage_7_days_gb=usage_7_days_gb,
                estimated_savings=savings,
                recommendation_reason=reason,
                priority=priority,
                metadata={
                    "pv_name": volume.pv_name,
                    "storage_class": volume.storage_class,
                    "utilization_percent": utilization * 100,
                    "algorithm": algorithm,
                    **volume.metadata,
                    "pricing": pricing,
                },
            )
        except Exception as e:
            logger.warning(f"[{pvc_id}] FAILED analysis with an unexpected exception: {e}", exc_info=True)
            return None

    def _filter_and_prioritize_recommendations(
        self, recommendations: List[VolumeRecommendation], max_recommendations: Optional[int] = None
    ) -> List[VolumeRecommendation]:
        """Filter and prioritize recommendations."""
        priority_order = {"High": 3, "Medium": 2, "Low": 1}
        sorted_recommendations = sorted(
            recommendations, key=lambda r: (priority_order.get(r.priority, 0), r.estimated_savings), reverse=True
        )
        if max_recommendations and max_recommendations > 0:
            return sorted_recommendations[:max_recommendations]
        return sorted_recommendations

    def _gb_to_bytes(self, gb_value: Optional[float]) -> float:
        """Convert GB to bytes."""
        if gb_value is None:
            return 0.0
        return gb_value * (1024**3)

    def _get_contextual_logger(self, namespace: Optional[str] = None):
        """Get contextual logger with tenant and account info."""
        extra = {"tenant_id": self.tenant_id, "account_id": self.account_id}
        if namespace:
            extra["namespace"] = namespace
        return logging.LoggerAdapter(logger, extra)

    async def store_recommendations_to_db(
        self,
        result: VolumeRightsizingResult,
        persist_recommendation: bool = True,
        namespace_filter: Optional[str] = None,
        max_recommendations: Optional[int] = None,
    ) -> bool:
        """Store volume rightsizing recommendations to database.

        namespace_filter/max_recommendations describe what the scan actually covered; the archive
        step needs them to know whether a PVC's absence from the results means it was deleted.
        """
        if not persist_recommendation or not result.recommendations:
            return False

        ctx_logger = self._get_contextual_logger()
        with tracer.start_as_current_span("store_volume_recommendations") as span:
            try:
                await self._archive_existing_recommendations(
                    result.recommendations, namespace_filter, max_recommendations
                )

                recommendation_records = []
                for rec in result.recommendations:
                    pvc_metadata = rec.metadata.get("metadata", {})

                    usage_7_days_bytes = self._gb_to_bytes(rec.usage_7_days_gb)
                    current_usage_bytes = self._gb_to_bytes(rec.current_usage_gb)
                    current_capacity_bytes = self._gb_to_bytes(rec.current_capacity_gb)

                    recommendation_content = {
                        "kind": "PersistentVolume",
                        "spec": {
                            "capacity": {"storage": f"{int(rec.current_capacity_gb)}Gi"},
                            "claimRef": {
                                "name": rec.pvc_name,
                                "namespace": rec.namespace,
                                "kind": "PersistentVolumeClaim",
                            },
                            "storageClassName": rec.metadata.get("storage_class", ""),
                            "accessModes": ["ReadWriteOnce"],
                        },
                        "usage": {
                            "7_days": {"value": [int(time.time()), str(int(usage_7_days_bytes))]},
                            "current": {"value": [int(time.time()), str(int(current_usage_bytes))]},
                        },
                        "status": {"phase": "Bound"},
                        "metadata": {
                            "name": rec.metadata.get("pv_name", f"pvc-{rec.pvc_name}"),
                            "uid": pvc_metadata.get("metadata", {}).get("uid", ""),
                            "annotations": pvc_metadata.get("metadata", {}).get("annotations", {}),
                            "creationTimestamp": pvc_metadata.get("metadata", {}).get("creation_timestamp", ""),
                        },
                        "apiVersion": "v1",
                        "pricing": rec.metadata.get("pricing", {}),
                        "recommendation": {
                            "usage": {"current": current_usage_bytes, "before_30_days": usage_7_days_bytes},
                            "capacity": current_capacity_bytes,
                            "price_per_gb": rec.metadata.get("pricing", {}).get(
                                "price_per_gb", FALLBACK_STORAGE_RATE_PER_GB_MONTH
                            ),
                            "recommend_size": int(rec.recommended_capacity_gb),
                        },
                    }
                    recommendation_record = {
                        "cloud_account_id": self.account_id,
                        "tenant_id": self.tenant_id,
                        "resource_id": None,
                        "recommendation": json.dumps(recommendation_content),
                        "recommendation_action": "Modify",
                        "category": "RightSizing",
                        "rule_name": "pv_rightsize",
                        "severity": rec.priority,
                        "estimated_savings": rec.estimated_savings,
                        "status": "Open",
                        "account_object_id": f"{rec.namespace}/{rec.pvc_name}",
                    }
                    recommendation_records.append(recommendation_record)

                if recommendation_records:
                    await self._insert_recommendations(recommendation_records)
                    await self._store_score(result.score)
                    span.set_attributes(
                        {"pvc.recommendations_stored": len(recommendation_records), "pvc.score_stored": result.score}
                    )
                    ctx_logger.info(
                        f"Successfully stored {len(recommendation_records)} PVC rightsizing recommendations"
                    )
                    return True
                else:
                    ctx_logger.warning("No recommendations to store")
                    return False
            except Exception as e:
                span.set_attribute("pvc.error", str(e))
                ctx_logger.error(f"Failed to store recommendations: {e}")
                raise

    async def _archive_existing_recommendations(
        self,
        recommendations: List[VolumeRecommendation],
        namespace_filter: Optional[str] = None,
        max_recommendations: Optional[int] = None,
    ):
        """Archive stale PVC rightsizing recommendations that no longer correspond to a live PVC.

        A scan reports recommendations only for PVCs that still exist in the cluster. A deleted
        PVC therefore simply disappears from the scan, so the correct way to retire its
        recommendation is to archive the in-scope Open recs whose account_object_id is NOT in the
        current scan results. Archiving the *scanned* set instead — the previous behaviour — was a
        no-op: the upsert immediately re-opened those same rows, leaving deleted PVCs (and rows
        written by the retired collector-side producer, which keyed on PV name) Open forever.

        Mirrors archive_existing_rightsizing_recommendations in vertical_rightsizing/__init__.py, which
        fixed this same bug for pod_right_sizing.

        Scope rules ensure we only reconcile what the scan was exhaustive over:
          - Full account run (no filters): reconcile across the whole account.
          - Namespace-only run: reconcile within that namespace.
          - max_recommendations run: the scan is truncated to the top-N by savings, so absence
            does NOT mean deletion — fall back to refreshing just the returned PVCs.
        """
        ctx_logger = self._get_contextual_logger(namespace_filter)

        # Empty scan → skip. An empty result is far more likely a transient metrics/relay failure
        # (a Prometheus query returning no series does not raise) than "every PVC in scope was
        # deleted"; a NOT-IN archive with an empty keep-set would mass-archive every Open rec in
        # scope. The rare genuinely-empty case self-heals on the next non-empty run.
        if not recommendations:
            ctx_logger.info("No PVC recommendations to process, skipping archiving")
            return

        # Bind as a single array param (= ANY) rather than dynamic :id_N placeholders: keeps the
        # SQL text static (one cached plan) and avoids the parameter-count ceiling on large
        # full-account scans.
        kept_object_ids = list({f"{rec.namespace}/{rec.pvc_name}" for rec in recommendations})

        params = {
            "tenant_id": self.tenant_id,
            "account_id": self.account_id,
            "kept_object_ids": kept_object_ids,
        }

        if max_recommendations and max_recommendations > 0:
            # Non-exhaustive scan: the result was capped, so a PVC's absence tells us nothing
            # about whether it still exists. Restrict to refreshing just the returned PVCs.
            scope_clause = "AND account_object_id = ANY(:kept_object_ids)"
            scope_label = "max_recommendations"
        else:
            # Exhaustive over its scope → archive anything in scope that vanished.
            scope_clause = "AND NOT (account_object_id = ANY(:kept_object_ids))"
            scope_label = "account"
            if namespace_filter:
                scope_clause += " AND account_object_id LIKE :ns_prefix"
                params["ns_prefix"] = f"{namespace_filter}/%"
                scope_label = "namespace"

        query = text(f"""
            UPDATE recommendation SET status = 'Archive'
            WHERE tenant_id = :tenant_id AND cloud_account_id = :account_id
            AND category = 'RightSizing' AND rule_name = 'pv_rightsize'
            AND status = 'Open'
            {scope_clause}
        """)

        with self.engine.connect() as conn:
            result = conn.execute(query, params)
            conn.commit()
            ctx_logger.info(
                f"Archived {result.rowcount} stale PVC rightsizing recommendations "
                f"(scope={scope_label}, kept={len(kept_object_ids)})"
            )

    async def _insert_recommendations(self, recommendations: List[Dict]):
        """Insert recommendation records."""
        upsert_query = text("""
            INSERT INTO recommendation (id, created_at, updated_at, cloud_account_id, tenant_id, resource_id,
            recommendation, recommendation_action, category, rule_name, severity, estimated_savings, status,
            account_object_id, note, dismissed_reason, is_dismissed)
            VALUES (gen_random_uuid(), now(), now(), :cloud_account_id, :tenant_id, :resource_id, :recommendation,
            :recommendation_action, :category, :rule_name, :severity, :estimated_savings, :status, :account_object_id,
            '', '', false) ON CONFLICT (cloud_account_id, rule_name, resource_id, category, account_object_id) DO
            UPDATE SET recommendation = EXCLUDED.recommendation, estimated_savings = EXCLUDED.estimated_savings,
            status = CASE WHEN recommendation.status NOT IN ('Open', 'Archive')
            THEN recommendation.status ELSE EXCLUDED.status END
        """)
        with self.engine.connect() as conn:
            conn.execute(upsert_query, recommendations)
            conn.commit()

    async def _store_score(self, score: float):
        """Store rightsizing score."""
        score_query = text("""
            INSERT INTO cloud_account_score (score, source, cloud_account_id, tenant)
            VALUES (:score, :source, :cloud_account_id, :tenant)
            ON CONFLICT (cloud_account_id, tenant, source) DO UPDATE SET score = EXCLUDED.score
        """)
        with self.engine.connect() as conn:
            conn.execute(
                score_query,
                {
                    "score": score,
                    "source": "pv_rightsize",
                    "cloud_account_id": self.account_id,
                    "tenant": self.tenant_id,
                },
            )
            conn.commit()


async def generate_volume_rightsizing_recommendations(
    tenant_id: str,
    account_id: str,
    namespace: Optional[str] = None,
    persist_recommendation: bool = True,
    max_recommendations: Optional[int] = None,
    metrics_provider: Optional[str] = None,
    datadog_api_key: Optional[str] = None,
    datadog_app_key: Optional[str] = None,
    datadog_site: Optional[str] = None,
    elasticsearch: Optional[dict] = None,
) -> VolumeRightsizingResult:
    """Main entry point for generating volume rightsizing recommendations."""
    service = VolumeRightsizingService(
        account_id,
        tenant_id,
        metrics_provider=metrics_provider,
        datadog_api_key=datadog_api_key,
        datadog_app_key=datadog_app_key,
        datadog_site=datadog_site,
        elasticsearch=elasticsearch,
    )
    result = await service.generate_recommendations(namespace_filter=namespace, max_recommendations=max_recommendations)
    if persist_recommendation:
        await service.store_recommendations_to_db(
            result, namespace_filter=namespace, max_recommendations=max_recommendations
        )
        result.database_stored = True
    return result
