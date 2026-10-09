"""A scan whose metrics never answer must stop early instead of holding the consumer for hours.

The cluster listing and the per-workload metrics fetch are faked; the batch loop, the
per-workload timeout handling and the abort rules run for real. A fake fetch that raises
asyncio.TimeoutError goes through the same `except asyncio.TimeoutError` branch a real
wait_for expiry does, so no test has to wait out the 45s timeout. The result models are
stubbed so a fake fetch can stand for a workload that produced a recommendation.
"""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from server.recommendation import vertical_rightsizing as vertical
from server.recommendation.vertical_rightsizing.models.config import Config
from server.recommendation.vertical_rightsizing.models.result import ResourceType
from server.recommendation.vertical_rightsizing.services import service as service_module
from server.recommendation.vertical_rightsizing.services.service import (
    CriticalRunnerException,
    RecommendationService,
    ScanAbortedError,
)

CLUSTER_ENGINE = (
    "server.recommendation.vertical_rightsizing.services.nudgebee_cluster_service.DatabaseEngine.get_engine"
)
BATCH_SIZE = 2
TIMEOUT, EMPTY, SCAN = "timeout", "empty", "scan"
RECOMMENDATION = {resource: SimpleNamespace(request=1, limit=None, info=None, config=None) for resource in ResourceType}


def _service(workload_count: int, outcome, **config_overrides) -> tuple[RecommendationService, list]:
    """A service over `workload_count` workloads; `outcome(index)` decides each fetch.

    Returns the service and the list of workload indexes fetched, in order.
    """
    config = Config(strategy="nudgebee", other_args={}, max_workers=BATCH_SIZE, **config_overrides)
    with patch(CLUSTER_ENGINE, return_value=MagicMock()):
        svc = RecommendationService("acct", config, tenant_id="tenant-1")
    svc._k8s_loader.list_scannable_objects = AsyncMock(return_value=list(range(workload_count)))
    fetched: list = []

    async def fake_fetch(k8s_object):
        fetched.append(k8s_object)
        result = outcome(k8s_object)
        if result == TIMEOUT:
            raise asyncio.TimeoutError()
        return RECOMMENDATION if result == SCAN else None

    svc._calculate_object_recommendations = fake_fetch
    return svc, fetched


def _run(svc: RecommendationService):
    with patch.object(service_module, "ResourceAllocations", MagicMock()), patch.object(
        service_module.ResourceScan, "calculate", lambda obj, allocations: obj
    ), patch.object(service_module, "Result", lambda **kwargs: SimpleNamespace(**kwargs)):
        try:
            return asyncio.run(svc._collect_result())
        finally:
            svc.close()


def test_a_scan_that_times_out_everywhere_stops_after_the_streak():
    svc, fetched = _service(730, lambda i: TIMEOUT)

    with pytest.raises(ScanAbortedError, match="10 batches in a row"):
        _run(svc)

    assert len(fetched) == 10 * BATCH_SIZE, "the scan kept going past the timed-out streak"


def test_one_timeout_per_batch_with_nothing_else_answering_also_stops():
    # The prod shape: one workload per batch times out, its sibling comes back empty at once.
    svc, fetched = _service(730, lambda i: TIMEOUT if i % BATCH_SIZE == 0 else EMPTY)

    with pytest.raises(ScanAbortedError, match="10 batches in a row"):
        _run(svc)

    assert len(fetched) == 10 * BATCH_SIZE


def test_occasional_timed_out_batches_do_not_abort_a_healthy_scan():
    # Every other batch times out completely: twenty fully timed-out batches, never two in a row.
    svc, fetched = _service(80, lambda i: TIMEOUT if (i // BATCH_SIZE) % 2 else SCAN)

    result = _run(svc)

    assert len(fetched) == 80
    assert len(result.scans) == 40


def test_a_recommendation_in_the_batch_resets_the_streak():
    # Nine counted batches, then one where a workload times out but its sibling produces a
    # recommendation, then nine more counted batches: never ten in a row.
    def outcome(i):
        if i == 9 * BATCH_SIZE + 1:
            return SCAN
        return TIMEOUT if i % BATCH_SIZE == 0 else EMPTY

    svc, fetched = _service(19 * BATCH_SIZE, outcome)

    result = _run(svc)

    assert len(fetched) == 19 * BATCH_SIZE
    assert len(result.scans) == 1


def test_empty_batches_without_a_timeout_do_not_count():
    # A cluster where most workloads have nothing to recommend is not a failing scan.
    svc, fetched = _service(60, lambda i: SCAN if i == 59 else EMPTY)

    result = _run(svc)

    assert len(fetched) == 60
    assert len(result.scans) == 1


def test_a_scan_past_its_deadline_stops_even_while_making_progress():
    # 20 workloads at 1s each, floor 5s: the deadline is 20s. The clock reads 0 when the
    # deadline is set and 6, 12, 18 before batches 1-3; batch 4 reads 24 and is never fetched.
    svc, fetched = _service(20, lambda i: SCAN, scan_seconds_per_workload=1, min_scan_deadline_seconds=5)
    # Replaces the name inside the service module only; asyncio's own clock is untouched.
    clock = iter(range(0, 1000, 6))

    with patch.object(service_module, "time", SimpleNamespace(monotonic=lambda: next(clock))):
        with pytest.raises(ScanAbortedError, match="20s deadline"):
            _run(svc)

    assert len(fetched) == 3 * BATCH_SIZE


def test_the_deadline_grows_with_the_workload_count():
    # Same clock, but 200 workloads give a 200s deadline: the 20 workloads above would
    # have been cut off, these finish.
    svc, fetched = _service(200, lambda i: SCAN, scan_seconds_per_workload=1, min_scan_deadline_seconds=5)
    clock = iter(range(0, 10000, 1))

    with patch.object(service_module, "time", SimpleNamespace(monotonic=lambda: next(clock))):
        result = _run(svc)

    assert len(fetched) == 200
    assert len(result.scans) == 200


def test_an_ordinary_empty_scan_is_not_reported_as_aborted():
    svc, _ = _service(10, lambda i: EMPTY)

    with pytest.raises(CriticalRunnerException) as excinfo:
        _run(svc)

    assert not isinstance(excinfo.value, ScanAbortedError)


def test_scan_limits_can_be_set_from_the_environment(monkeypatch):
    monkeypatch.setenv("RIGHTSIZING_SCAN_MAX_TIMED_OUT_BATCHES", "4")
    monkeypatch.setenv("RIGHTSIZING_SCAN_SECONDS_PER_WORKLOAD", "20")
    monkeypatch.setenv("RIGHTSIZING_SCAN_MIN_DEADLINE_SECONDS", "3600")

    config = Config(strategy="nudgebee", other_args={}, **vertical._scan_limit_overrides())

    assert config.max_consecutive_timed_out_batches == 4
    assert config.scan_seconds_per_workload == 20
    assert config.min_scan_deadline_seconds == 3600


def test_unset_scan_limits_keep_the_defaults(monkeypatch):
    for env in vertical._SCAN_LIMIT_ENV.values():
        monkeypatch.delenv(env, raising=False)

    config = Config(strategy="nudgebee", other_args={}, **vertical._scan_limit_overrides())

    assert config.max_consecutive_timed_out_batches == 10
    assert config.scan_seconds_per_workload == 10
    assert config.min_scan_deadline_seconds == 1800


@pytest.mark.parametrize("raw", ["", "ten", "0", "-3"])
def test_a_bad_scan_limit_falls_back_to_the_default(monkeypatch, raw):
    monkeypatch.setenv("RIGHTSIZING_SCAN_MAX_TIMED_OUT_BATCHES", raw)

    assert vertical._scan_limit_overrides() == {}
