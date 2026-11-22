"""A scan holds every workload until it ends, so each workload's pod list goes once it is scored.

The pod list carries every pod the workload had over the history window, which on a churning
workload is thousands of entries. Measured on dev, it was 17MB of the 22MB a 290-container scan
still held when it finished. Only the live pod count is read afterwards, by the savings math.

The strategy is faked; the scoring wrapper, ResourceScan.calculate and the recommendation build
run for real, so the test sees the same object the stored recommendation is built from.
"""

import asyncio
import logging
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

from server.recommendation.vertical_rightsizing import build_recommendation_data_from_scans
from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations, ResourceType
from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData, PodData
from server.recommendation.vertical_rightsizing.services.service import RecommendationService
from server.recommendation.vertical_rightsizing.strategy.strategies import ResourceRecommendation

CLUSTER_ENGINE = (
    "server.recommendation.vertical_rightsizing.services.nudgebee_cluster_service.DatabaseEngine.get_engine"
)


def _workload() -> K8sObjectData:
    # Two live replicas plus forty churned incarnations from the history window.
    pods = [PodData(name=f"web-old-{i}", deleted=True) for i in range(40)]
    pods += [PodData(name="web-live-1", deleted=False), PodData(name="web-live-2", deleted=False)]
    return K8sObjectData(
        cluster="acct",
        name="web",
        container="app",
        pods=pods,
        hpa=None,
        namespace="shop",
        kind="Deployment",
        allocations=ResourceAllocations(
            requests={ResourceType.CPU: 0.5, ResourceType.Memory: 512 * 1024 * 1024},
            limits={ResourceType.CPU: None, ResourceType.Memory: None},
        ),
    )


def _service(fetch) -> RecommendationService:
    with patch(CLUSTER_ENGINE, return_value=MagicMock()):
        svc = RecommendationService("acct", Config(strategy="nudgebee", other_args={}, max_workers=2))
    svc._calculate_object_recommendations = fetch
    return svc


def _score(svc: RecommendationService, workload: K8sObjectData):
    try:
        return asyncio.run(svc._gather_object_allocations(workload))
    finally:
        svc.close()


async def _recommend(k8s_object):
    return {
        ResourceType.CPU: ResourceRecommendation(request=0.2, limit=None),
        ResourceType.Memory: ResourceRecommendation(request=256 * 1024 * 1024, limit=256 * 1024 * 1024),
    }


def test_a_scored_workload_keeps_its_live_count_and_drops_its_pods():
    workload = _workload()

    scan = _score(_service(_recommend), workload)

    assert scan is not None
    assert scan.object.pods == [], "the scan still holds the workload's pod history"
    assert scan.object.current_pods_count == 2


def test_the_savings_multiplier_still_sees_the_live_count():
    scan = _score(_service(_recommend), _workload())
    result = SimpleNamespace(scans=[scan], resources=list(ResourceType), description="", strategy=None)

    [recommendation] = build_recommendation_data_from_scans(result, logging.getLogger(__name__))

    assert recommendation.pods_count == 2


def test_a_workload_that_fails_to_score_drops_its_pods_too():
    # The scan keeps the full workload list to the end, failed workloads included.
    async def fail(k8s_object):
        raise RuntimeError("metrics unavailable")

    workload = _workload()

    assert _score(_service(fail), workload) is None
    assert workload.pods == []
