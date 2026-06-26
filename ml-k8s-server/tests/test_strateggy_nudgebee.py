import math

import numpy as np

from server.recommendation.vertical_rightsizing.strategy.strateggy_nudgebee import (
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
