"""How a spec's series is assembled from utilisation keys, and what an absence means.

The failure this guards against is silent: a detector that cancels every run looks
exactly like a workload with nothing wrong. The latency detector (#39260) survived its
whole life that way, reading a zero-filled series from a metric that did not exist.
"""

import uuid

import pandas as pd
import pytest

from server.anomaly.anomaly_algo.abstract import (
    ERRORRATE_LAPLACE_K,
    MetricKey,
    MetricSpec,
    _errorrate_laplace,
)


def _series(values, start="2026-01-01"):
    idx = pd.date_range(start, periods=len(values), freq="2min")
    return pd.Series([float(v) for v in values], index=idx)


class _Algo:
    """Minimal stand-in exposing the two methods under test."""

    def __init__(self, loaded):
        from server.anomaly.anomaly_algo.abstract import AnomalyAlgoAbstract

        self.load_metric_series = AnomalyAlgoAbstract.load_metric_series.__get__(self)
        self._loaded = loaded

        class _Cfg:
            account_id = str(uuid.uuid4())
            namespace = "nudgebee"
            deployment = "services-server"

        self.config = _Cfg()

    def load_data_from_prometheus(self, account_id, column, start_time=None, end_time=None, step=None, tenant_id=""):
        return self._loaded.get(column.utilisation_key, (None, {}))


class _RunCfg:
    tenant_id = uuid.uuid4()
    step = "2m"


# --- single-key specs -------------------------------------------------------------


def test_single_key_returns_its_series():
    spec = MetricSpec(utilisation_keys=(MetricKey("cpu_usage"),))
    algo = _Algo({"cpu_usage": (_series([1, 2, 3]), {"pod": "p"})})
    data, labels = algo.load_metric_series(spec, _RunCfg())
    assert list(data) == [1.0, 2.0, 3.0]
    assert labels == {"pod": "p"}


def test_single_key_absent_cancels():
    """cpu/memory/replicas series exist whenever the workload does, so empty = gone."""
    spec = MetricSpec(utilisation_keys=(MetricKey("cpu_usage"),))
    data, _ = _Algo({}).load_metric_series(spec, _RunCfg())
    assert data is None, "an absent usage key must cancel, not zero-fill"


# --- the asymmetric ratio contract -------------------------------------------------


def _errorrate_spec():
    return MetricSpec(
        utilisation_keys=(
            MetricKey("workload_http_error_count", zero_when_absent=True),
            MetricKey("workload_http_request_count"),
        ),
        combine=_errorrate_laplace,
    )


def test_absent_errors_with_traffic_reads_as_zero_not_cancelled():
    """The healthy case, and the one that would silently disable 77% of workloads."""
    algo = _Algo({"workload_http_request_count": (_series([100, 200, 300]), {})})
    data, _ = algo.load_metric_series(_errorrate_spec(), _RunCfg())
    assert data is not None, "a workload that has never errored must still be detected on"
    assert list(data) == [0.0, 0.0, 0.0]


def test_absent_traffic_cancels():
    """No requests at all genuinely is no data, even though errors zero-fill."""
    algo = _Algo({"workload_http_error_count": (_series([0, 0]), {})})
    data, _ = algo.load_metric_series(_errorrate_spec(), _RunCfg())
    assert data is None


def test_ratio_is_smoothed_when_both_present():
    algo = _Algo(
        {
            "workload_http_error_count": (_series([1, 5]), {}),
            "workload_http_request_count": (_series([1, 1000]), {}),
        }
    )
    data, _ = algo.load_metric_series(_errorrate_spec(), _RunCfg())
    assert data.iloc[0] == pytest.approx(1 / (1 + ERRORRATE_LAPLACE_K))
    assert data.iloc[1] == pytest.approx(5 / (1000 + ERRORRATE_LAPLACE_K))


# --- the combine function itself ---------------------------------------------------


def test_laplace_aligns_mismatched_indexes():
    """The two counts are fetched independently and can differ in length; a ragged
    align must not produce NaN, which StandardScaler and DBSCAN both reject."""
    errors = _series([1, 2])
    requests = _series([10, 20, 30])
    out = _errorrate_laplace(errors, requests)
    assert len(out) == 3
    assert not out.isna().any()
    assert out.iloc[2] == pytest.approx(0 / (30 + ERRORRATE_LAPLACE_K))


def test_laplace_never_divides_by_zero():
    out = _errorrate_laplace(_series([3]), _series([0]))
    assert out.iloc[0] == pytest.approx(3 / ERRORRATE_LAPLACE_K)
    assert out.notna().all()


# --- PromQL specs still work -------------------------------------------------------


def test_spec_without_keys_takes_the_promql_path():
    spec = MetricSpec(query_fmt='up{{namespace="{namespace}", deployment="{deployment}"}}')
    seen = {}

    class _PromAlgo(_Algo):
        def load_data_from_prometheus(self, account_id, column, **kw):
            seen["column"] = column
            return _series([1]), {}

    data, _ = _PromAlgo({}).load_metric_series(spec, _RunCfg())
    assert isinstance(seen["column"], str), "a PromQL spec must still send a query string"
    assert 'namespace="nudgebee"' in seen["column"]
    assert data is not None


# --- LATENCY (#39260) ---------------------------------------------------------------


def test_latency_is_answered_by_key_not_promql():
    """The spec that had never fired now names a key instead of carrying PromQL."""
    from server.anomaly.anomaly_algo.abstract import get_metric_spec

    spec = get_metric_spec("latency")
    assert spec.utilisation_keys == (MetricKey("workload_http_latency_p99"),)
    assert not spec.query_fmt, "latency must no longer carry PromQL"


def test_latency_cancels_when_absent_rather_than_zero_filling():
    """No HTTP traffic means no latency to detect on -- zero-filling is what hid it."""
    from server.anomaly.anomaly_algo.abstract import get_metric_spec

    for key in get_metric_spec("latency").utilisation_keys:
        assert key.zero_when_absent is False


def test_latency_tuning_constants_are_unchanged():
    """The migration changes where the number comes from, not how it is judged."""
    from server.anomaly.anomaly_algo.abstract import get_metric_spec

    spec = get_metric_spec("latency")
    assert spec.default_threshold == 0.5
    assert spec.dbscan_eps == 0.6
    assert spec.dbscan_min_samples == 5
    # The step is the one exception: 10m since #43354, where the 2m query OOM-killed
    # Prometheus. See METRIC_SPECS for the measurement.
    assert spec.step == "10m"
