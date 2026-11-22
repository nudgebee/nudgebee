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
import math
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Dict, List, Optional, Any

from server.utils.utils import DatabaseEngine, get_trace
from server.utils.utils import MetricsServerConfigs
from server.metrics.api_server_metrics import query_utilisation_via_api_server
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


def entries_from_es_volume_aggregations(data: Dict[str, Any]) -> List[dict]:
    """An Elasticsearch volume response in the shape api-server returns.

    Only used by the direct-Elasticsearch fallback, and only for the body this module
    builds: namespace -> claim -> max(used), max(capacity).
    """
    entries: List[dict] = []
    ns_buckets = ((data or {}).get("aggregations") or {}).get("ns") or {}
    if ns_buckets.get("sum_other_doc_count"):
        # A capped terms aggregation drops whole namespaces, which would read as
        # "those claims have no usage" rather than as missing data.
        logger.warning(
            f"Elasticsearch volume aggregation truncated at {ES_MAX_NAMESPACES} namespaces; "
            f"{ns_buckets['sum_other_doc_count']} documents excluded"
        )
    for ns in ns_buckets.get("buckets", []) or []:
        if not isinstance(ns, dict) or not (namespace := ns.get("key")):
            continue
        for pvc in (ns.get("pvc") or {}).get("buckets") or []:
            if not isinstance(pvc, dict) or not (pvc_name := pvc.get("key")):
                continue
            for name in ("used", "capacity"):
                value = (pvc.get(name) or {}).get("value")
                if value is not None:
                    entries.append(
                        {
                            "metric": {"ns": namespace, "pvc": pvc_name, "__name__": name},
                            "timestamps": [0],
                            "values": [value],
                        }
                    )
    return entries


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
    ):
        self.account_id = account_id
        self.tenant_id = tenant_id
        self.engine = DatabaseEngine.get_engine()
        self.prometheus_url = MetricsServerConfigs.url
        # Accepted and no longer read: the claim keys are answered by the account's
        # default provider.
        self.metrics_provider = metrics_provider or "prometheus"
        # Named so a leaked pool is identifiable in `py-spy dump` and in
        # /proc/<pid>/task/*/comm on a live pod.
        self._executor = ThreadPoolExecutor(max_workers=4, thread_name_prefix="pvc-rightsize")

        # Configuration from original implementation
        self.overprovisioning_threshold = 0.9  # 90% usage threshold
        self.minimum_savings_threshold = 0.1  # Minimum $1 savings to recommend

    def close(self) -> None:
        """Release this scan's thread pool.

        One service is built per scan and then dropped; without this its worker threads park
        on the work queue for the life of the gunicorn worker, holding the scan's object
        graph with them. See RecommendationService.close for the measurements.

        Note the prometheus provider path never submits to this pool, so those threads are
        created and leaked having done no work at all.
        """
        self._executor.shutdown(wait=True, cancel_futures=True)

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
                # One path for every provider. The claim keys are answered by whatever
                # the account's default metrics provider is, so this no longer asks
                # which one that is — the three branches this replaces each wrote the
                # same three questions in a different query language.
                #
                # Current and capacity are the last reading over ten minutes; the peak
                # is the max over seven days. Same key, different window and collapse,
                # which is why there are two keys rather than four.
                current_usage_map, capacity_map, usage_7_days_map = await asyncio.gather(
                    self._query_claim_metric("pvc_used_by_claim", timedelta(minutes=10), namespace_filter),
                    self._query_claim_metric("pvc_capacity_by_claim", timedelta(minutes=10), namespace_filter),
                    self._query_claim_metric("pvc_used_by_claim", timedelta(days=7), namespace_filter, aggregate="max"),
                )

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

    async def _query_claim_metric(
        self,
        key: str,
        duration: timedelta,
        namespace_filter: Optional[str] = None,
        aggregate: str = "",
    ) -> Dict[str, float]:
        """One claim metric, keyed `{pvc}_{namespace}` as the caller below expects.

        Returns {} rather than raising: a scan covering every claim in a cluster must
        not end because one window could not be fetched, and an empty map reads as
        "no data for these claims", which is what a failed query has always meant here.
        """
        # No `kind`: a claim belongs to a namespace, not to a workload, and the claim
        # keys ignore it — verified live, the same 25 claims come back for deployment,
        # statefulset or nothing at all. Sending one would imply a scope that is not there.
        request: Dict[str, Any] = {"metrics": [key]}
        if namespace_filter:
            request["workload_namespace"] = namespace_filter

        end = datetime.now(UTC)
        try:
            answered = await asyncio.get_running_loop().run_in_executor(
                None,
                lambda: query_utilisation_via_api_server(
                    account_id=str(self.account_id),
                    tenant_id=self.tenant_id,
                    request=request,
                    start_time=end - duration,
                    end_time=end,
                    aggregate=aggregate,
                ),
            )
        except Exception as e:
            logger.error(f"volume metrics query failed for {key}: {e}")
            return {}

        usage_map: Dict[str, float] = {}
        for series in answered.get(key) or []:
            metric = series.get("metric") or {}
            claim, namespace = metric.get("persistentvolumeclaim"), metric.get("namespace")
            values = series.get("values") or []
            if not (claim and namespace and values):
                continue
            # Backwards for the most recent READABLE reading, not simply the last one.
            # Raised in review of #38823: giving up on the claim because the newest
            # sample would not parse reads downstream as "this claim has no usage",
            # which shrinks or drops a volume that is actually full.
            for raw in reversed(values):
                try:
                    reading = float(raw)
                except (TypeError, ValueError):
                    continue
                # float("NaN") parses happily. A NaN usage compares false against every
                # threshold downstream, so the claim would be sized as though it were
                # empty; keep walking back for a real reading instead.
                if math.isnan(reading) or math.isinf(reading):
                    continue
                usage_map[f"{claim}_{namespace}"] = reading
                break
        return usage_map

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
                    pricing = resolve_storage_pricing(
                        pvc_object,
                        storage_classes=storage_classes,
                        provider=provider,
                        size_gb=volume.capacity_gb,
                    )
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
                recommended_size_gb = float(math.ceil(capacity_gb * 1.2))
                savings = -((recommended_size_gb - capacity_gb) * price_per_gb)
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
                # A claim is sized in whole GiB, and the record truncates to an
                # int: a fractional size below one GiB used to become 0.
                recommended_size_gb = float(max(1, math.ceil((capacity_gb - mfss_gb) * 1.3)))
                logger.info(
                    f"[{pvc_id}] Projected Max Free Space: {mfss_gb:.2f} GB. Recommended Size: "
                    f"{recommended_size_gb:.2f} GB."
                )

                if 0 < recommended_size_gb < capacity_gb:
                    storage_save_gb = capacity_gb - recommended_size_gb
                    # For tiered Azure SKUs this is approximate: the exact saving
                    # is tier(current) - tier(recommended). Kept proportional so an
                    # in-band resize still surfaces instead of costing $0.
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
) -> VolumeRightsizingResult:
    """Main entry point for generating volume rightsizing recommendations."""
    service = VolumeRightsizingService(
        account_id,
        tenant_id,
        metrics_provider=metrics_provider,
    )
    try:
        result = await service.generate_recommendations(
            namespace_filter=namespace, max_recommendations=max_recommendations
        )
        if persist_recommendation:
            await service.store_recommendations_to_db(
                result, namespace_filter=namespace, max_recommendations=max_recommendations
            )
            result.database_stored = True
        return result
    finally:
        # In a finally, not after the return: the scans that leak are the ones that raise.
        service.close()
