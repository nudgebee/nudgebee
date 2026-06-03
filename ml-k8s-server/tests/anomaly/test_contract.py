"""
Characterization tests for the anomaly response contract and the train/eval split.

These pin the CURRENT behavior of all three algorithms so the increment-1 refactor
(schema standardization to the "data" column + a single shared train/eval split) can be
proven to change only what is intended. The external contract consumed by the api-server
(Go struct `AnomalyResponseMetrics{ Data float64 json:"data" }`) and the frontend
(`item.data`) requires the per-point value field to be named "data".

NOTE: ml-k8s-server CI runs lint/type checks only (black/flake8/mypy) and does NOT run
pytest. These tests are a local developer safety scaffold for this refactor, not a CI gate.
"""

import uuid
from datetime import timedelta
from unittest.mock import patch

import numpy as np
import pandas as pd
import pytest
from flask import Flask

from server.anomaly.anomaly_algo import get_anomaly_algo
from server.anomaly.anomaly_algo.abstract import AnomalyType, get_metric_spec
from server.controllers.anomaly import app as anomaly_blueprint
from server.metrics.prometheus_queries import MetricsInput
from tests.anomaly.conftest import AlgorithmFactory

# Per-algo expected per-point value-column key.
# All algos emit the standardized "data" column (Step 2 of the refactor flipped ZSCORE
# from its former "value" column to "data").
EXPECTED_VALUE_KEY = {
    "ISOLATION_TREE": "data",
    "DB_SCAN": "data",
    "ZSCORE": "data",
}
ALGOS = list(EXPECTED_VALUE_KEY.keys())

TOP_LEVEL_KEYS = ("has_anomaly", "stats", "insights", "training_end_time", "evaluation_period", "data")
PER_POINT_BASE_KEYS = ("timestamp", "anomaly", "anomaly_score")


def _make_spike(synthetic_generator):
    """Deterministic 10x memory spike scenario (seed 42 via fixture)."""
    return synthetic_generator.generate_spike_scenario(
        n_points=1000,
        baseline=300_000_000,  # 300 MB in bytes
        spike_magnitude=10.0,
        spike_duration_minutes=5,
        noise_level=0.05,
    )


def _configure(algo, data, train_end):
    """Set the window on the algo's config, mirroring tests/anomaly/test_algorithms.py.

    start_time/end_time exist on the base config. training_start_time/training_end_time
    exist only on IsolationTree/DBSCAN configs today; ZScoreConfig gains them in Step 3.
    Guard with hasattr so this is green on current code (pydantic v2 raises on assignment
    of an undeclared field) and after the fields are added.
    """
    algo.config.start_time = data.index.min()
    algo.config.end_time = data.index.max()
    if hasattr(algo.config, "training_start_time"):
        algo.config.training_start_time = data.index.min()
    if hasattr(algo.config, "training_end_time"):
        algo.config.training_end_time = train_end


def _metrics_input(data, namespace="test-ns", pod="test-pod"):
    """Build a MetricsInput mirroring what fetch_metrics returns for one series."""
    return MetricsInput(
        metric={"namespace": namespace, "pod": pod},
        timestamps=[int(ts.timestamp()) for ts in data.index],
        values=[str(v) for v in data.values],
    )


@pytest.fixture
def client():
    """Flask test client with ONLY the anomaly blueprint mounted.

    Avoids importing server.app, which runs heavyweight init (tracing, metrics, RabbitMQ,
    controller glob) at module load.
    """
    app = Flask(__name__)
    app.register_blueprint(anomaly_blueprint)
    app.config["TESTING"] = True
    return app.test_client()


# --- 1a. Direct get_anomaly() schema characterization -------------------------------------


@pytest.mark.parametrize("algo_name", ALGOS)
def test_get_anomaly_response_schema(algo_name, synthetic_generator):
    data, _gt, train_end, _eval_start = _make_spike(synthetic_generator)
    algo = AlgorithmFactory.create(algo_name, anomaly_type="memory", evaluation_period=timedelta(hours=1))
    _configure(algo, data, train_end)

    d = algo.get_anomaly(data, algo.config).to_dict()

    assert d["has_anomaly"] is True, f"{algo_name}: 10x spike should be flagged"
    for key in TOP_LEVEL_KEYS:
        assert key in d, f"{algo_name}: missing top-level key {key!r}"
    assert isinstance(d["data"], list) and len(d["data"]) > 0, f"{algo_name}: empty data array"

    value_key = EXPECTED_VALUE_KEY[algo_name]
    point = d["data"][0]
    for key in PER_POINT_BASE_KEYS:
        assert key in point, f"{algo_name}: missing per-point key {key!r}"
    assert value_key in point, f"{algo_name}: missing per-point value key {value_key!r}"
    # No algo should emit the legacy "value" column anymore (ZScore was standardized to "data").
    assert "value" not in point, f"{algo_name}: still emits legacy 'value' column"


def test_get_anomaly_cpu_value_key(synthetic_generator):
    """CPU variant pins only the per-point value KEY name (not magnitude) for ZScore.

    Guards against coupling the column rename (Step 2) to a CPU rescale.
    """
    data, _gt, train_end, _eval_start = _make_spike(synthetic_generator)
    algo = AlgorithmFactory.create("ZSCORE", anomaly_type="cpu", evaluation_period=timedelta(hours=1))
    _configure(algo, data, train_end)

    point = algo.get_anomaly(data, algo.config).to_dict()["data"][0]
    assert EXPECTED_VALUE_KEY["ZSCORE"] in point


# --- 1b. POST /anomaly (template path) schema over HTTP -----------------------------------


@pytest.mark.parametrize("algo_name", ["ISOLATION_TREE", "ZSCORE"])
@patch("server.anomaly.anomaly_algo.abstract.fetch_metrics")
def test_anomaly_endpoint_per_point_key(mock_fetch, algo_name, client, synthetic_generator):
    data, _gt, _train_end, _eval_start = _make_spike(synthetic_generator)
    mock_fetch.return_value = [_metrics_input(data)]

    resp = client.post(
        "/anomaly",
        json={
            "namespace": "test-ns",
            "deployment": "test-dep",
            "tenant": str(uuid.uuid4()),
            "account": str(uuid.uuid4()),
            "type": "memory",
            "evaluation_period": 60,
            "algo": algo_name,
        },
    )

    assert resp.status_code == 200, resp.get_data(as_text=True)
    body = resp.get_json()
    assert isinstance(body, list) and len(body) == 1
    points = body[0]["data"]
    assert isinstance(points, list) and len(points) > 0
    assert EXPECTED_VALUE_KEY[algo_name] in points[0]


# --- 1c. POST /anomaly/detect (custom-query path) schema over HTTP ------------------------


@patch("server.anomaly.anomaly_algo.abstract.fetch_metrics")
def test_detect_endpoint_per_point_key(mock_fetch, client, synthetic_generator):
    """Pin the harder /detect path (controller-side _filter_and_mark_periods)."""
    data, _gt, _train_end, _eval_start = _make_spike(synthetic_generator)
    mock_fetch.return_value = [_metrics_input(data)]

    analysis_end = data.index.max()
    analysis_start = analysis_end - timedelta(minutes=120)  # window includes the spike

    resp = client.post(
        "/anomaly/detect",
        json={
            "account": str(uuid.uuid4()),
            "query": "container_memory_working_set_bytes",
            "analysis_start_time": analysis_start.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "analysis_end_time": analysis_end.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "historical_window_hours": 24,
            "anomaly_type": "memory",
        },
    )

    assert resp.status_code == 200, resp.get_data(as_text=True)
    body = resp.get_json()
    assert isinstance(body, list) and len(body) == 1
    points = body[0]["data"]
    assert isinstance(points, list) and len(points) > 0
    assert "data" in points[0]


# --- 3f. Shared train/eval split characterization ----------------------------------------


@pytest.mark.parametrize("algo_name", ALGOS)
def test_split_train_eval_boundary(algo_name, synthetic_generator):
    data, _gt, _train_end, _eval_start = _make_spike(synthetic_generator)
    algo = AlgorithmFactory.create(algo_name, anomaly_type="memory", evaluation_period=timedelta(hours=1))
    algo.get_default_parameters(data=data, config=algo.config)

    train, detection = algo.split_train_eval(data, algo.config)

    tee = pd.Timestamp(algo.config.training_end_time)
    assert not train.empty and not detection.empty
    assert train.index.max() < tee <= detection.index.min()
    assert len(train) + len(detection) == len(data)


@pytest.mark.parametrize("algo_name", ALGOS)
def test_split_train_eval_no_split(algo_name, synthetic_generator):
    """With no evaluation_period and no training_end_time, the full series is training."""
    data, _gt, _train_end, _eval_start = _make_spike(synthetic_generator)
    algo = AlgorithmFactory.create(algo_name, anomaly_type="memory", evaluation_period=None)
    algo.config.start_time = data.index.min()
    algo.config.end_time = data.index.max()  # training_end_time left as None (the no-split path)

    train, detection = algo.split_train_eval(data, algo.config)

    assert len(train) == len(data)
    assert detection.empty


def test_zscore_baseline_excludes_eval_window(synthetic_generator):
    """ZScore's reconciled split: boundary is training_end_time (= end - eval_period),
    so a spike inside the evaluation window is excluded from the training baseline."""
    data, ground_truth, _train_end, _eval_start = _make_spike(synthetic_generator)
    # 120-min eval window comfortably covers the scenario's spike (placed ~10% from the end).
    algo = AlgorithmFactory.create("ZSCORE", anomaly_type="memory", evaluation_period=timedelta(minutes=120))
    algo.get_default_parameters(data=data, config=algo.config)

    assert algo.config.training_end_time == algo.config.end_time - timedelta(minutes=120)

    train, _detection = algo.split_train_eval(data, algo.config)
    spike_timestamps = ground_truth[ground_truth].index
    assert not train.index.isin(spike_timestamps).any(), "spike must be outside the ZScore training window"


# --- Inc2. Evaluation-period filter behavior (the two algos differ on purpose) ------------


def test_isolation_forest_returns_eval_window_only(synthetic_generator):
    """IsolationForest's response is restricted to the evaluation window (>= training_end_time)."""
    data, _gt, train_end, _eval_start = _make_spike(synthetic_generator)
    algo = AlgorithmFactory.create("ISOLATION_TREE", anomaly_type="memory", evaluation_period=timedelta(hours=1))
    _configure(algo, data, train_end)

    resp = algo.get_anomaly(data, algo.config)

    tee = pd.Timestamp(algo.config.training_end_time)
    assert not resp.df.empty
    assert pd.Timestamp(resp.df.index.min()) >= tee, "IsolationForest must return only the evaluation window"


def test_dbscan_returns_context_window_before_eval(synthetic_generator):
    """DBSCAN's response includes response_context_hours of pre-evaluation context."""
    data, _gt, train_end, _eval_start = _make_spike(synthetic_generator)
    algo = AlgorithmFactory.create("DB_SCAN", anomaly_type="memory", evaluation_period=timedelta(hours=1))
    _configure(algo, data, train_end)

    resp = algo.get_anomaly(data, algo.config)

    tee = pd.Timestamp(algo.config.training_end_time)
    assert not resp.df.empty
    # context_hours defaults to 6.0, so the response starts well before the eval boundary.
    assert pd.Timestamp(resp.df.index.min()) < tee, "DBSCAN must include pre-eval context in its response"


# --- Inc3. MetricSpec registry faithfully mirrors the former TEMPLATES/constant maps --------


def test_metric_spec_registry_values():
    """Pin the migrated per-metric config values (was TEMPLATES + the constant maps)."""
    mem = get_metric_spec("memory")
    assert mem is not None
    assert mem.default_threshold == 50 * 1024 * 1024
    assert (mem.dbscan_eps, mem.dbscan_min_samples) == (0.3, 10)
    assert mem.step == "1m" and mem.trim_leading_zeros is True and mem.real_data_threshold == 0.0

    cpu = get_metric_spec("cpu")
    assert cpu is not None
    assert (cpu.dbscan_eps, cpu.dbscan_min_samples, cpu.step) == (0.4, 8, "2m")
    assert cpu.trim_leading_zeros is True and cpu.real_data_threshold == 0.1

    # errorrate/latency/replicas are not trimmed and have a 0.0 real-data threshold.
    for name, eps, samples, step in [
        ("errorrate", 0.8, 3, "2m"),
        ("latency", 0.6, 5, "2m"),
        ("replicas", 1.0, 3, "5m"),
    ]:
        spec = get_metric_spec(name)
        assert spec is not None
        assert (spec.dbscan_eps, spec.dbscan_min_samples, spec.step) == (eps, samples, step)
        assert spec.trim_leading_zeros is False and spec.real_data_threshold == 0.0


def test_metric_spec_lookup_is_case_insensitive_and_none_for_custom():
    assert get_metric_spec("Memory") is get_metric_spec("memory")
    assert AnomalyType.parse("CPU") is AnomalyType.CPU
    assert get_metric_spec("custom") is None
    assert get_metric_spec("unknown-query-metric") is None
    assert AnomalyType.parse(None) is None


# --- Error-rate noise reduction: meaningful-floor + sustained-anomaly gate -----------------


def _errorrate_series(eval_window, n_train=600, train_noise=0.01):
    """Build an error-rate series: a low, varied (non-constant) baseline so a model trains,
    with `eval_window` values placed as the final points (the evaluation window)."""
    n = n_train + len(eval_window)
    idx = pd.date_range(end="2026-06-03 00:00:00", periods=n, freq="2min")
    rng = np.random.default_rng(1)
    vals = rng.uniform(0, train_noise, n)  # 0-1% baseline noise, max > 0 (passes new-workload guard)
    vals[-len(eval_window) :] = eval_window
    return pd.Series(vals, index=pd.DatetimeIndex(idx))


def _run(algo_name, series, atype):
    cls, cfgcls = get_anomaly_algo(algo_name)
    cfg = cfgcls(
        account_id="00000000-0000-0000-0000-000000000001",
        namespace="ns",
        deployment="d",
        anomaly_type=atype,
        evaluation_period=pd.Timedelta(minutes=60),
    )
    return cls(config=cfg).process_metrics(config=cfg, data=series.copy())


def test_errorrate_single_blip_suppressed():
    """A lone 100% point in the eval window is NOT flagged (sustained gate requires >=3)."""
    ev = [0.005] * 30
    ev[15] = 1.0  # single isolated 100% blip
    resp = _run("ISOLATION_TREE", _errorrate_series(ev), "errorrate")
    assert resp.has_anomaly is False


def test_errorrate_sustained_high_flagged():
    """>=3 consecutive high error-rate points ARE flagged (the gate doesn't over-suppress)."""
    ev = [0.005] * 30
    ev[12:18] = [0.2] * 6  # 6 consecutive points at 20% (sustained, above 5% floor)
    resp = _run("ISOLATION_TREE", _errorrate_series(ev), "errorrate")
    assert resp.has_anomaly is True


def test_errorrate_below_floor_suppressed():
    """Sustained but sub-5% error rate is NOT flagged (meaningful-floor gate)."""
    ev = [0.005] * 30
    ev[12:18] = [0.03] * 6  # 6 consecutive points at 3% (sustained but below the 5% floor)
    resp = _run("ISOLATION_TREE", _errorrate_series(ev), "errorrate")
    assert resp.has_anomaly is False


def test_memory_single_spike_still_flagged():
    """min_sustained_points defaults to 1, so memory single-point spikes are unaffected."""
    n = 630
    idx = pd.date_range(end="2026-06-03 00:00:00", periods=n, freq="1min")
    rng = np.random.default_rng(2)
    vals = 300e6 + rng.normal(0, 5e6, n)  # ~300 MB baseline
    vals[-15] = 3e9  # single 3 GB spike in the eval window
    resp = _run("ISOLATION_TREE", pd.Series(vals, index=pd.DatetimeIndex(idx)), "memory")
    assert resp.has_anomaly is True


def test_errorrate_query_is_laplace_smoothed():
    """Guard Rule 1 (can't be functionally tested offline): the query must be the smoothed form."""
    q = get_metric_spec("errorrate").query_fmt
    assert "increase(" in q and "+ 10)" in q
    assert get_metric_spec("errorrate").default_threshold == 0.05
    assert get_metric_spec("errorrate").min_sustained_points == 3


# --- Memory baseline gate: recurrence-suppress + sustained-detect --------------------------


def _mem_series(values, freq="1min"):
    idx = pd.date_range(end="2026-06-03 00:00:00", periods=len(values), freq=freq)
    return pd.Series(np.asarray(values, dtype="float64"), index=pd.DatetimeIndex(idx))


def test_memory_recurring_peak_suppressed():
    """A peak the workload hits repeatedly (>=10x in training) is NOT re-flagged."""
    rng = np.random.default_rng(3)
    n = 700
    vals = 300e6 + rng.normal(0, 8e6, n)
    # ~15 recurring spikes to 1.5 GB scattered across the training window
    for pos in range(40, 600, 38):
        vals[pos] = 1.5e9
    vals[-15] = 1.5e9  # eval-window spike at the SAME recurring level
    resp = _run("ISOLATION_TREE", _mem_series(vals), "memory")
    assert resp.has_anomaly is False


def test_memory_sustained_leak_caught():
    """A flat low baseline that steps up to a sustained plateau (a leak) IS flagged,
    even though the local rolling median would mask it."""
    rng = np.random.default_rng(4)
    n = 700
    vals = 55e6 + rng.normal(0, 1e6, n)  # ~55 MB flat baseline
    vals[-30:] = 300e6 + rng.normal(0, 2e6, 30)  # sustained ~300 MB plateau (30 consecutive)
    resp = _run("ISOLATION_TREE", _mem_series(vals), "memory")
    assert resp.has_anomaly is True


def test_memory_gate_is_noop_for_non_memory():
    """The gate must not touch non-memory responses."""
    from server.anomaly.anomaly_algo import get_anomaly_algo

    _, cfgcls = get_anomaly_algo("ISOLATION_TREE")
    cfg = cfgcls(
        account_id="00000000-0000-0000-0000-000000000001",
        namespace="ns",
        deployment="d",
        anomaly_type="cpu",
        evaluation_period=pd.Timedelta(minutes=60),
    )
    cls, _ = get_anomaly_algo("ISOLATION_TREE")
    algo = cls(config=cfg)
    idx = pd.date_range(end="2026-06-03 00:00:00", periods=5, freq="2min")
    df = pd.DataFrame(
        {
            "data": [1.0, 1.0, 9.0, 1.0, 1.0],
            "anomaly": [False, False, True, False, False],
            "anomaly_score": [0.0, 0.0, -0.5, 0.0, 0.0],
        },
        index=pd.DatetimeIndex(idx),
    )
    from server.anomaly.anomaly_algo.abstract import AnomalyResponse

    resp = AnomalyResponse(df, idx.min(), idx.max(), "cpu", "acct", "ns", "d", has_anomaly=True)
    cfg.training_end_time = idx[3]
    out = algo.apply_memory_baseline_gate(resp, _mem_series([1, 1, 9, 1, 1], freq="2min"), cfg)
    assert out.has_anomaly is True and int(out.df["anomaly"].sum()) == 1  # unchanged


def test_memory_recurring_peak_suppressed_dbscan():
    """The memory gate is algo-agnostic: DBSCAN's recurring peak is suppressed too
    (and the gate correctly handles DBSCAN's wider context-window response df)."""
    rng = np.random.default_rng(5)
    n = 700
    vals = 300e6 + rng.normal(0, 8e6, n)
    for pos in range(40, 600, 38):
        vals[pos] = 1.5e9
    vals[-15] = 1.5e9
    resp = _run("DB_SCAN", _mem_series(vals), "memory")
    assert resp.has_anomaly is False


def test_split_train_eval_handles_tz_mismatch():
    """split_train_eval aligns tz of config boundaries vs the data index (Gemini review):
    a tz-aware index with naive config timestamps must not raise and must still partition."""
    cls, cfgcls = get_anomaly_algo("ISOLATION_TREE")
    idx = pd.date_range(end="2026-06-03 00:00:00", periods=120, freq="1min", tz="UTC")  # tz-AWARE
    s = pd.Series(np.arange(120, dtype="float64"), index=idx)
    cfg = cfgcls(
        account_id="00000000-0000-0000-0000-000000000001",
        namespace="ns",
        deployment="d",
        anomaly_type="memory",
        evaluation_period=pd.Timedelta(minutes=30),
    )
    cfg.start_time = pd.Timestamp("2026-06-02 22:00:00")  # naive
    cfg.training_start_time = pd.Timestamp("2026-06-02 22:00:00")
    cfg.training_end_time = pd.Timestamp("2026-06-02 23:30:00")  # naive boundary vs UTC index
    train, det = cls(config=cfg).split_train_eval(s, cfg)  # must not raise TypeError
    assert not train.empty and not det.empty
    assert len(train) + len(det) == len(s)
    assert train.index.max() < det.index.min()
