import math

import numpy as np
import pytest

from server.recommendation.vertical_rightsizing.models.allocations import ResourceAllocations
from server.recommendation.vertical_rightsizing.metrics.memory import MaxUsageMemoryLoader
from server.recommendation.vertical_rightsizing.services.metrics_prometheus_service import _tuning_env
from server.recommendation.vertical_rightsizing.models.objects import K8sObjectData
from server.recommendation.vertical_rightsizing.models.result import ResourceType
from server.recommendation.vertical_rightsizing.strategy.strateggy_nudgebee import (
    NudgebeeStrategy,
    NudgebeeStrategySettings,
)


def _settings() -> NudgebeeStrategySettings:
    return NudgebeeStrategySettings()


def test_calculate_cpu_proposal_empty_per_data_returns_nan():
    # An empty per-percentile pod map previously fell through to
    # `list(per_data.values())[0]` and raised IndexError. It must now short-circuit
    # to NaN.
    result = _settings().calculate_cpu_proposal({"p99": {}})
    assert math.isnan(result["p99"])


def test_calculate_cpu_proposal_single_pod():
    data = {"p99": {"pod1": np.array([[0, 1.0], [1, 2.0], [2, 3.0]])}}
    result = _settings().calculate_cpu_proposal(data)
    assert result["p99"] == 3.0


def test_calculate_cpu_proposal_multiple_pods():
    data = {"p90": {"a": np.array([[0, 1.0]]), "b": np.array([[0, 5.0]])}}
    result = _settings().calculate_cpu_proposal(data)
    assert result["p90"] == 5.0


def test_calculate_cpu_proposal_mixed_empty_and_populated():
    data = {
        "empty": {},
        "p99": {"pod1": np.array([[0, 4.0], [1, 2.0]])},
    }
    result = _settings().calculate_cpu_proposal(data)
    assert math.isnan(result["empty"])
    assert result["p99"] == 4.0


def _history(max_ws: float, max_usage: float | None, points: int = 100) -> dict:
    """Minimal history_data for NudgebeeStrategy.run() with a single pod."""
    pod = "pod1"
    history = {
        "cpu_percentile_92": {pod: np.array([[0, 0.1]])},
        "cpu_percentile_95": {pod: np.array([[0, 0.1]])},
        "cpu_percentile_97": {pod: np.array([[0, 0.1]])},
        "cpu_percentile_99": {pod: np.array([[0, 0.1]])},
        "CPUAmountLoader": {pod: np.array([[0, points]])},
        "MemoryAmountLoader": {pod: np.array([[0, points]])},
        "MaxMemoryLoader": {pod: np.array([[0, max_ws]])},
        "MaxOOMKilledMemoryLoader": {},
    }
    if max_usage is not None:
        history["MaxUsageMemoryLoader"] = {pod: np.array([[0, max_usage]])}
    return history


def _object(mem_limit: float | None = None) -> K8sObjectData:
    return K8sObjectData(
        cluster=None,
        name="app",
        container="app",
        namespace="ns",
        kind="Deployment",
        hpa=None,
        allocations=ResourceAllocations(requests={}, limits={ResourceType.Memory: mem_limit}),
    )


def test_memory_limit_is_floored_at_cgroup_high_water():
    # The sampled working-set gauge misses peaks between scrapes, so the request alone
    # can sit below what the container actually reached - and a limit equal to it OOMKills.
    result = NudgebeeStrategy(_settings()).run(_history(max_ws=100.0, max_usage=230.0), _object())
    memory = result[ResourceType.Memory]
    assert memory.request == pytest.approx(115.0)  # unchanged: sampled peak + 15%
    assert memory.limit == pytest.approx(264.5)  # floored at the high-water mark + 15%
    assert memory.config["memory_high_water_mark"] == 230.0
    assert memory.config["memory_sampling_gap_ratio"] == 2.3


def test_memory_limit_equals_request_when_high_water_is_lower():
    result = NudgebeeStrategy(_settings()).run(_history(max_ws=100.0, max_usage=90.0), _object())
    memory = result[ResourceType.Memory]
    assert memory.limit == memory.request == pytest.approx(115.0)


def test_memory_limit_falls_back_to_request_without_the_high_water_metric():
    # Older cAdvisor / cgroup versions do not export container_memory_max_usage_bytes.
    result = NudgebeeStrategy(_settings()).run(_history(max_ws=100.0, max_usage=None), _object())
    memory = result[ResourceType.Memory]
    assert memory.limit == memory.request == pytest.approx(115.0)
    assert memory.config["memory_high_water_mark"] is None


def test_memory_limit_is_not_ratcheted_above_a_limit_the_container_never_breached():
    # The high-water mark counts page cache, which expands to fill the limit. A container
    # sitting at its limit without OOMKilling must not have that limit inflated every scan.
    result = NudgebeeStrategy(_settings()).run(_history(max_ws=100.0, max_usage=230.0), _object(mem_limit=240.0))
    memory = result[ResourceType.Memory]
    assert memory.limit == pytest.approx(240.0)  # capped at the allocated limit, not 264.5
    assert memory.limit >= memory.config["memory_high_water_mark"]  # still never below the peak


def test_memory_limit_exceeds_the_allocated_limit_when_the_high_water_already_did():
    # A limit that was lowered below what the container reached gets corrected upwards.
    result = NudgebeeStrategy(_settings()).run(_history(max_ws=100.0, max_usage=230.0), _object(mem_limit=200.0))
    assert result[ResourceType.Memory].limit == pytest.approx(264.5)


def test_tuning_env_prefers_new_name_and_falls_back_to_legacy(monkeypatch):
    # The chart renders .Values.env verbatim, so an install can still set the pre-rename
    # name from a values file we do not control; dropping it would silently reset the knob.
    monkeypatch.delenv("RIGHTSIZING_OWNER_BATCH_SIZE", raising=False)
    monkeypatch.delenv("KRR_OWNER_BATCH_SIZE", raising=False)
    assert _tuning_env("RIGHTSIZING_OWNER_BATCH_SIZE", "KRR_OWNER_BATCH_SIZE", "100") == "100"

    monkeypatch.setenv("KRR_OWNER_BATCH_SIZE", "42")
    assert _tuning_env("RIGHTSIZING_OWNER_BATCH_SIZE", "KRR_OWNER_BATCH_SIZE", "100") == "42"

    monkeypatch.setenv("RIGHTSIZING_OWNER_BATCH_SIZE", "7")
    assert _tuning_env("RIGHTSIZING_OWNER_BATCH_SIZE", "KRR_OWNER_BATCH_SIZE", "100") == "7"


def test_high_water_ignores_nan_samples_from_scrape_gaps():
    # Prometheus serialises a gap as "NaN". np.max would propagate it, and max() over the
    # per-pod results is order-dependent with NaN, so the floor could vanish silently.
    history = _history(max_ws=100.0, max_usage=230.0)
    history["MaxUsageMemoryLoader"] = {
        "pod1": np.array([[0, np.nan], [1, 230.0]]),
        "pod2": np.array([[0, np.nan]]),  # only NaN - contributes nothing, must not poison
    }
    history["MemoryAmountLoader"]["pod2"] = np.array([[0, 100]])
    history["MaxMemoryLoader"]["pod2"] = np.array([[0, 100.0]])
    memory = NudgebeeStrategy(_settings()).run(history, _object())[ResourceType.Memory]
    assert memory.config["memory_high_water_mark"] == pytest.approx(230.0)
    assert memory.limit == pytest.approx(264.5)


def test_high_water_is_zero_when_every_sample_is_nan():
    history = _history(max_ws=100.0, max_usage=230.0)
    history["MaxUsageMemoryLoader"] = {"pod1": np.array([[0, np.nan]])}
    memory = NudgebeeStrategy(_settings()).run(history, _object())[ResourceType.Memory]
    assert memory.config["memory_high_water_mark"] is None
    assert memory.limit == memory.request == pytest.approx(115.0)


def test_memory_proposal_ignores_nan_samples():
    # A single scrape gap must not void the whole memory recommendation - and with the
    # high-water floor in play, max(NaN, floor) is NaN, so it would void that too.
    peak, buffered = _settings().calculate_memory_proposal({"pod1": np.array([[0, np.nan], [1, 500.0]])})
    assert peak == pytest.approx(500.0)
    assert buffered == pytest.approx(575.0)


def test_memory_proposal_is_undefined_when_every_pod_is_all_nan():
    peak, buffered = _settings().calculate_memory_proposal({"pod1": np.array([[0, np.nan]])})
    assert math.isnan(peak) and math.isnan(buffered)


def test_high_water_loader_does_not_wildcard_when_no_pods_resolved():
    # A wildcard would pull in every pod in the namespace sharing this container name; a floor
    # taken from an unrelated workload is worse than no floor.
    loader = MaxUsageMemoryLoader(prometheus=None, service_name="test")
    query = loader.get_query(_object(), "336h", "75s")
    assert ".*" not in query
    assert "__NO_PODS_RESOLVED__" in query
