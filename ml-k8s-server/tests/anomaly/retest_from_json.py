#!/usr/bin/env python3
"""
Retest anomaly detection algorithms using saved JSON data from the UI.

Usage:
    python tests/anomaly/retest_from_json.py <json_file> [--algorithm ALGO]

Example:
    python tests/anomaly/retest_from_json.py /path/to/anomaly_data.json
    python tests/anomaly/retest_from_json.py /path/to/anomaly_data.json --algorithm DB_SCAN
    python tests/anomaly/retest_from_json.py /path/to/anomaly_data.json --all
    python tests/anomaly/retest_from_json.py /path/to/anomaly_data.json --graph

JSON Format Expected:
{
    "data": [
        {"data": 286502912, "anomaly": false, "timestamp": "2026-01-11 18:00:00", "anomaly_score": 0},
        ...
    ],
    "account": "...",
    "namespace": "...",
    "deployment": "...",
    "anomaly_type": "memory",
    "start_time": "2026-01-11T18:00:00Z",
    "end_time": "2026-01-18T18:00:00Z",
    "evaluation_period": 60,
    "has_anomaly": true
}
"""

import argparse
import json
import sys
from datetime import datetime, timedelta
from pathlib import Path
from typing import Dict, List, Optional

# Add project root to path
sys.path.insert(0, str(Path(__file__).parent.parent.parent))

import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
import matplotlib.pyplot as plt  # noqa: E402
import matplotlib.dates as mdates  # noqa: E402
from sklearn.decomposition import PCA  # noqa: E402
from sklearn.neighbors import NearestNeighbors  # noqa: E402

from server.anomaly.anomaly_algo import get_anomaly_algo  # noqa: E402
from server.anomaly.anomaly_algo.abstract import AnomalyAlgoAbstractConfig  # noqa: E402

ALGORITHMS = ["DB_SCAN"]  # , "ISOLATION_TREE", "ZSCORE"]


def load_json_data(filepath: str) -> Dict:
    """Load anomaly data from JSON file."""
    with open(filepath) as f:
        return json.load(f)


def json_to_series(json_data: Dict) -> pd.Series:
    """Convert JSON data array to pandas Series."""
    data_points = json_data["data"]

    timestamps = []
    values = []

    for point in data_points:
        # Handle different timestamp formats
        ts_str = point["timestamp"]
        try:
            # Try ISO format first
            ts = datetime.fromisoformat(ts_str.replace("Z", "+00:00"))
        except ValueError:
            # Try the format from UI: "2026-01-11 18:00:00"
            ts = datetime.strptime(ts_str, "%Y-%m-%d %H:%M:%S")

        timestamps.append(ts)
        values.append(point["data"])

    return pd.Series(values, index=pd.DatetimeIndex(timestamps))


def get_original_anomalies(json_data: Dict) -> pd.Series:
    """Extract original anomaly flags from JSON data."""
    data_points = json_data["data"]

    timestamps = []
    anomalies = []

    for point in data_points:
        ts_str = point["timestamp"]
        try:
            ts = datetime.fromisoformat(ts_str.replace("Z", "+00:00"))
        except ValueError:
            ts = datetime.strptime(ts_str, "%Y-%m-%d %H:%M:%S")

        timestamps.append(ts)
        anomalies.append(point.get("anomaly", False))

    return pd.Series(anomalies, index=pd.DatetimeIndex(timestamps))


def run_algorithm(
    algorithm_name: str,
    data: pd.Series,
    json_data: Dict,
) -> Dict:
    """Run a single algorithm on the data."""
    algo_class, _ = get_anomaly_algo(algorithm_name)

    # Extract metadata from JSON
    account_id = json_data.get("account", "test-account")
    namespace = json_data.get("namespace", "test-namespace")
    deployment = json_data.get("deployment", "test-deployment")
    anomaly_type = json_data.get("anomaly_type", "memory")
    evaluation_period_minutes = json_data.get("evaluation_period", 60)

    # Apply the same CPU unit conversion that load_and_prepare_data does in production.
    # Prometheus returns CPU in cores; the algo internally works in millicores.
    if anomaly_type.lower() == "cpu":
        data = (data * 1000).round(4)

    # Parse times
    start_time = data.index.min()
    end_time = data.index.max()

    # Create config using the abstract base — subclasses upcast internally
    config = AnomalyAlgoAbstractConfig(
        account_id=account_id,
        namespace=namespace,
        deployment=deployment,
        anomaly_type=anomaly_type,
        evaluation_period=timedelta(minutes=evaluation_period_minutes),
        start_time=start_time,
        end_time=end_time,
    )

    # Run algorithm using process_metrics which properly initializes parameters
    algo = algo_class(config=config)
    response = algo.process_metrics(config=config, data=data)

    # Extract results
    detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
    anomaly_count = detected.sum()

    return {
        "algorithm": algorithm_name,
        "has_anomaly": response.has_anomaly,
        "anomaly_count": int(anomaly_count),
        "total_points": len(response.df),
        "response": response,
        "detected": detected,
        "algo": algo,  # Expose algo instance for cluster visualization
    }


def compare_with_original(
    original: pd.Series,
    detected: pd.Series,
    eval_start: datetime,
) -> Dict:
    """Compare detected anomalies with original."""
    # Align indexes
    common_index = original.index.intersection(detected.index)

    if len(common_index) == 0:
        return {
            "precision": 0.0,
            "recall": 0.0,
            "f1_score": 0.0,
            "agreement": 0.0,
            "tp": 0,
            "fp": 0,
            "fn": 0,
            "tn": 0,
        }

    orig = original.reindex(common_index, fill_value=False)
    det = detected.reindex(common_index, fill_value=False)

    # Filter to evaluation period
    mask = det.index >= eval_start
    orig = orig[mask]
    det = det[mask]

    tp = int((det & orig).sum())
    fp = int((det & ~orig).sum())
    fn = int((~det & orig).sum())
    tn = int((~det & ~orig).sum())

    precision = tp / (tp + fp) if (tp + fp) > 0 else 1.0
    recall = tp / (tp + fn) if (tp + fn) > 0 else 1.0
    f1 = 2 * precision * recall / (precision + recall) if (precision + recall) > 0 else 0.0
    agreement = (tp + tn) / len(det) if len(det) > 0 else 0.0

    return {
        "precision": precision,
        "recall": recall,
        "f1_score": f1,
        "agreement": agreement,
        "tp": tp,
        "fp": fp,
        "fn": fn,
        "tn": tn,
    }


def print_data_summary(json_data: Dict, data: pd.Series):
    """Print summary of loaded data."""
    print("\n" + "=" * 80)
    print("DATA SUMMARY")
    print("=" * 80)
    print(f"Account:     {json_data.get('account', 'N/A')}")
    print(f"Namespace:   {json_data.get('namespace', 'N/A')}")
    print(f"Deployment:  {json_data.get('deployment', 'N/A')}")
    print(f"Anomaly Type: {json_data.get('anomaly_type', 'N/A')}")
    print(f"Time Range:  {data.index.min()} to {data.index.max()}")
    print(f"Data Points: {len(data)}")
    print(f"Eval Period: {json_data.get('evaluation_period', 60)} minutes")
    print(f"Original has_anomaly: {json_data.get('has_anomaly', 'N/A')}")

    if "stats" in json_data:
        stats = json_data["stats"]
        print("\nStats:")
        print(f"  Mean: {stats.get('mean', 'N/A'):,.0f}")
        print(f"  Min:  {stats.get('min', 'N/A'):,.0f}")
        print(f"  Max:  {stats.get('max', 'N/A'):,.0f}")
        print(f"  P99:  {stats.get('p99', 'N/A'):,.0f}")
    print("=" * 80)


def print_results(results: List[Dict], original_anomalies: pd.Series, eval_start: datetime):
    """Print comparison results."""
    print("\n" + "=" * 80)
    print("ALGORITHM COMPARISON RESULTS")
    print("=" * 80)
    print(
        f"{'Algorithm':<18} {'Detected':<10} {'Count':<8} {'Precision':<10} {'Recall':<10} {'F1':<10} {'Agreement':<10}"
    )
    print("-" * 80)

    for result in results:
        comparison = compare_with_original(
            original_anomalies,
            result["detected"],
            eval_start,
        )

        print(
            f"{result['algorithm']:<18} "
            f"{str(result['has_anomaly']):<10} "
            f"{result['anomaly_count']:<8} "
            f"{comparison['precision']:<10.3f} "
            f"{comparison['recall']:<10.3f} "
            f"{comparison['f1_score']:<10.3f} "
            f"{comparison['agreement']:<10.3f}"
        )

    print("=" * 80)

    # Print detailed breakdown
    print("\nDETAILED BREAKDOWN:")
    print("-" * 80)
    for result in results:
        comparison = compare_with_original(
            original_anomalies,
            result["detected"],
            eval_start,
        )
        print(f"\n{result['algorithm']}:")
        print(f"  True Positives:  {comparison['tp']}")
        print(f"  False Positives: {comparison['fp']}")
        print(f"  False Negatives: {comparison['fn']}")
        print(f"  True Negatives:  {comparison['tn']}")


def print_anomaly_timestamps(results: List[Dict], eval_start: datetime):
    """Print timestamps where anomalies were detected."""
    print("\n" + "=" * 80)
    print("ANOMALY TIMESTAMPS (Evaluation Period Only)")
    print("=" * 80)

    for result in results:
        detected = result["detected"]
        eval_detected = detected[detected.index >= eval_start]
        anomaly_times = eval_detected[eval_detected].index

        print(f"\n{result['algorithm']} ({len(anomaly_times)} anomalies):")
        if len(anomaly_times) > 0:
            for ts in anomaly_times[:20]:  # Show first 20
                print(f"  {ts}")
            if len(anomaly_times) > 20:
                print(f"  ... and {len(anomaly_times) - 20} more")
        else:
            print("  (none)")


def plot_results(
    data: pd.Series,
    original_anomalies: pd.Series,
    results: List[Dict],
    eval_start: datetime,
    json_data: Dict,
    save_path: Optional[str] = None,
    context_hours: float = 6,
):
    """Plot the data with anomalies highlighted for each algorithm."""
    # Filter data to show only context_hours before eval_start + evaluation period
    plot_start = eval_start - timedelta(hours=context_hours)
    data = data[data.index >= plot_start]
    original_anomalies = original_anomalies[original_anomalies.index >= plot_start]

    # Also filter the detected anomalies in results
    for result in results:
        result["detected"] = result["detected"][result["detected"].index >= plot_start]

    num_plots = len(results) + 1  # +1 for original
    fig, axes = plt.subplots(num_plots, 1, figsize=(14, 4 * num_plots), sharex=True)

    if num_plots == 1:
        axes = [axes]

    # Format y-axis values (convert bytes to MB/GB for memory)
    anomaly_type = json_data.get("anomaly_type", "memory")
    if anomaly_type == "memory":
        scale = 1e9  # Convert to GB
        unit = "GB"
    elif anomaly_type == "cpu":
        scale = 1
        unit = "cores"
    else:
        scale = 1
        unit = ""

    # Color scheme
    colors = {
        "data": "#2196F3",  # Blue
        "anomaly": "#F44336",  # Red
        "eval_region": "#FFF9C4",  # Light yellow
        "training_region": "#E3F2FD",  # Light blue
    }

    algo_colors = {
        "DB_SCAN": "#4CAF50",  # Green
        "ISOLATION_TREE": "#FF9800",  # Orange
        "ZSCORE": "#9C27B0",  # Purple
        "Original": "#F44336",  # Red
    }

    # Plot 1: Original data with original anomalies
    ax = axes[0]
    ax.set_facecolor("white")

    # Shade evaluation period
    ax.axvspan(eval_start, data.index.max(), alpha=0.3, color=colors["eval_region"], label="Eval Period")

    # Plot data line
    ax.plot(data.index, data.values / scale, color=colors["data"], linewidth=0.5, alpha=0.8, label="Data")

    # Highlight original anomalies
    orig_anomaly_mask = original_anomalies.reindex(data.index, fill_value=False)
    anomaly_points = data[orig_anomaly_mask]
    if len(anomaly_points) > 0:
        ax.scatter(
            anomaly_points.index,
            anomaly_points.values / scale,
            color=algo_colors["Original"],
            s=30,
            zorder=5,
            label=f"Original Anomalies ({len(anomaly_points)})",
        )

    ax.set_ylabel(f"Value ({unit})")
    ax.set_title(
        f"Original Data - {json_data.get('namespace', '')}/{json_data.get('deployment', '')} ({anomaly_type})",
        fontweight="bold",
    )
    ax.legend(loc="upper left")
    ax.grid(True, alpha=0.3)

    # Plot each algorithm's results
    for i, result in enumerate(results):
        ax = axes[i + 1]
        ax.set_facecolor("white")

        # Shade evaluation period
        ax.axvspan(eval_start, data.index.max(), alpha=0.3, color=colors["eval_region"])

        # Plot data line
        ax.plot(data.index, data.values / scale, color=colors["data"], linewidth=0.5, alpha=0.8, label="Data")

        # Highlight detected anomalies
        detected = result["detected"]
        detected_aligned = detected.reindex(data.index, fill_value=False)
        anomaly_points = data[detected_aligned]

        algo_name = result["algorithm"]
        algo_color = algo_colors.get(algo_name, "#000000")

        if len(anomaly_points) > 0:
            ax.scatter(
                anomaly_points.index,
                anomaly_points.values / scale,
                color=algo_color,
                s=30,
                zorder=5,
                label=f"Detected ({len(anomaly_points)})",
            )

        # Add metrics to title
        eval_detected = detected[detected.index >= eval_start]
        eval_count = eval_detected.sum()

        status = "DETECTED" if result["has_anomaly"] else "NOT DETECTED"
        ax.set_title(f"{algo_name} - {status} (Eval period: {int(eval_count)} anomalies)", fontweight="bold")
        ax.set_ylabel(f"Value ({unit})")
        ax.legend(loc="upper left")
        ax.grid(True, alpha=0.3)

    # Format x-axis
    axes[-1].set_xlabel("Time")
    for ax in axes:
        ax.xaxis.set_major_formatter(mdates.DateFormatter("%m-%d %H:%M"))
        ax.xaxis.set_major_locator(mdates.AutoDateLocator())

    plt.tight_layout()

    if save_path:
        plt.savefig(save_path, dpi=150, bbox_inches="tight")
        print(f"\nGraph saved to: {save_path}")
    else:
        plt.show()

    plt.close()


def plot_evaluation_period_detail(
    data: pd.Series,
    original_anomalies: pd.Series,
    results: List[Dict],
    eval_start: datetime,
    json_data: Dict,
    save_path: Optional[str] = None,
):
    """Plot zoomed-in view of just the evaluation period."""
    # Filter to evaluation period
    eval_data = data[data.index >= eval_start]
    eval_orig = original_anomalies[original_anomalies.index >= eval_start]

    anomaly_type = json_data.get("anomaly_type", "memory")
    if anomaly_type == "memory":
        scale = 1e9
        unit = "GB"
    elif anomaly_type == "cpu":
        scale = 1
        unit = "cores"
    else:
        scale = 1
        unit = ""

    fig, ax = plt.subplots(figsize=(14, 6))
    ax.set_facecolor("white")

    # Plot data
    ax.plot(eval_data.index, eval_data.values / scale, color="#2196F3", linewidth=1, label="Data")

    # Plot original anomalies
    orig_mask = eval_orig.reindex(eval_data.index, fill_value=False)
    orig_points = eval_data[orig_mask]
    if len(orig_points) > 0:
        ax.scatter(
            orig_points.index,
            orig_points.values / scale,
            color="#F44336",
            s=100,
            marker="o",
            zorder=5,
            label=f"Original ({len(orig_points)})",
            edgecolors="black",
            linewidths=1,
        )

    # Plot each algorithm with different markers
    markers = ["s", "^", "D", "v", "p"]
    algo_colors = ["#4CAF50", "#FF9800", "#9C27B0", "#00BCD4", "#795548"]

    for i, result in enumerate(results):
        detected = result["detected"]
        eval_detected = detected[detected.index >= eval_start]
        det_mask = eval_detected.reindex(eval_data.index, fill_value=False)
        det_points = eval_data[det_mask]

        if len(det_points) > 0:
            ax.scatter(
                det_points.index,
                det_points.values / scale,
                color=algo_colors[i % len(algo_colors)],
                s=60,
                marker=markers[i % len(markers)],
                zorder=4,
                label=f"{result['algorithm']} ({len(det_points)})",
                alpha=0.8,
            )

    ax.set_xlabel("Time")
    ax.set_ylabel(f"Value ({unit})")
    ax.set_title(
        f"Evaluation Period Detail - {json_data.get('namespace', '')}/{json_data.get('deployment', '')}",
        fontweight="bold",
    )
    ax.legend(loc="upper left")
    ax.grid(True, alpha=0.3)
    ax.xaxis.set_major_formatter(mdates.DateFormatter("%m-%d %H:%M"))

    plt.tight_layout()

    if save_path:
        # Modify save path for eval detail
        base, ext = save_path.rsplit(".", 1) if "." in save_path else (save_path, "png")
        eval_save_path = f"{base}_eval_detail.{ext}"
        plt.savefig(eval_save_path, dpi=150, bbox_inches="tight")
        print(f"Evaluation detail graph saved to: {eval_save_path}")
    else:
        plt.show()

    plt.close()


def plot_combined(  # noqa: C901
    data: pd.Series,
    original_anomalies: pd.Series,
    results: List[Dict],
    eval_start: datetime,
    json_data: Dict,
    save_path: Optional[str] = None,
    zoom_hours: Optional[float] = None,
):
    """
    Single figure combining all three views:

    Rows 0..N  (full width) — time-series: original data + one row per algorithm
    Row  N+1   (full width) — evaluation period zoomed detail
    Row  N+2   (split 1:1)  — cluster plots: value-vs-hour  |  PCA of scaled space

    zoom_hours: if set, time-series x-axis is clamped to this many hours before
                eval_start (full data still used for algo/cluster calculations).
    """
    import matplotlib.gridspec as gridspec

    anomaly_type = json_data.get("anomaly_type", "memory")
    if anomaly_type == "memory":
        scale, unit = 1e9, "GB"
    elif anomaly_type == "cpu":
        scale, unit = 1, "cores"
    else:
        scale, unit = 1, ""

    algo_colors_map = {
        "DB_SCAN": "#4CAF50",
        "ISOLATION_TREE": "#FF9800",
        "ZSCORE": "#9C27B0",
        "Original": "#F44336",
    }
    data_line_color = "#2196F3"

    # Use all data for time-series rows
    ts_data = data
    ts_orig = original_anomalies

    # Eval-period data for detail row
    eval_data = data[data.index >= eval_start]
    eval_orig = original_anomalies[original_anomalies.index >= eval_start]

    n_algos = len(results)

    # Pre-compute which results have fitted DBSCAN models — one cluster row per algo
    cluster_algos = [
        r
        for r in results
        if r.get("algo") is not None
        and r["algo"].scaler is not None
        and r["algo"].model is not None
        and len(r["algo"].model.core_sample_indices_) > 0
    ]

    n_ts_rows = 1 + n_algos  # original + each algorithm
    n_cluster_rows = len(cluster_algos)
    total_rows = n_ts_rows + 1 + n_cluster_rows  # + eval detail + cluster rows (one per algo)

    fig = plt.figure(figsize=(20, 5 * total_rows))
    gs = gridspec.GridSpec(total_rows, 2, figure=fig, hspace=0.5)

    # ------------------------------------------------------------------ #
    # Rows 0..n_ts_rows-1 : time-series views (span both columns)        #
    # ------------------------------------------------------------------ #
    ts_axes = [fig.add_subplot(gs[r, :]) for r in range(n_ts_rows)]

    eval_line_color = "#FF6F00"  # Orange for the eval segment of the data line

    def _plot_ts_line(ax, series, scl):
        """Plot data line with training (blue) and eval (orange) segments."""
        train_seg = series[series.index < eval_start]
        eval_seg = series[series.index >= eval_start]
        if len(train_seg):
            ax.plot(
                train_seg.index,
                train_seg.values / scl,
                color=data_line_color,
                linewidth=0.5,
                alpha=0.8,
                label="Training data",
            )
        if len(eval_seg):
            ax.plot(
                eval_seg.index,
                eval_seg.values / scl,
                color=eval_line_color,
                linewidth=1.2,
                alpha=0.95,
                label="Eval data",
            )

    # -- Row 0: original data with ground-truth anomalies --
    ax = ts_axes[0]
    ax.set_facecolor("white")
    ax.axvline(eval_start, color=eval_line_color, linewidth=1.2, linestyle="--", alpha=0.8)
    _plot_ts_line(ax, ts_data, scale)
    orig_mask = ts_orig.reindex(ts_data.index, fill_value=False)
    orig_pts = ts_data[orig_mask]
    if len(orig_pts) > 0:
        ax.scatter(
            orig_pts.index,
            orig_pts.values / scale,
            color=algo_colors_map["Original"],
            s=30,
            zorder=5,
            label=f"Original Anomalies ({len(orig_pts)})",
        )
    ax.set_ylabel(f"Value ({unit})")
    ax.set_title(
        f"Original Data — {json_data.get('namespace', '')}/{json_data.get('deployment', '')} ({anomaly_type})",
        fontweight="bold",
    )
    ax.legend(loc="upper left", fontsize=8)
    ax.grid(True, alpha=0.3)

    # -- Rows 1..n_algos: per-algorithm time-series --
    for i, result in enumerate(results):
        ax = ts_axes[i + 1]
        ax.set_facecolor("white")
        ax.axvline(eval_start, color=eval_line_color, linewidth=1.2, linestyle="--", alpha=0.8)
        _plot_ts_line(ax, ts_data, scale)

        detected = result["detected"].reindex(ts_data.index, fill_value=False)
        det_pts = ts_data[detected]
        algo_color = algo_colors_map.get(result["algorithm"], "#000000")
        if len(det_pts) > 0:
            ax.scatter(
                det_pts.index,
                det_pts.values / scale,
                color=algo_color,
                s=30,
                zorder=5,
                label=f"Detected ({len(det_pts)})",
            )

        eval_count = int(result["detected"][result["detected"].index >= eval_start].sum())
        status = "DETECTED" if result["has_anomaly"] else "NOT DETECTED"
        ax.set_title(f"{result['algorithm']} — {status} (Eval period: {eval_count} anomalies)", fontweight="bold")
        ax.set_ylabel(f"Value ({unit})")
        ax.legend(loc="upper left", fontsize=8)
        ax.grid(True, alpha=0.3)

    # Apply zoom: clamp x-axis to [zoom_start, data_end] if requested
    x_end = ts_data.index.max()
    if zoom_hours is not None:
        x_start = eval_start - timedelta(hours=zoom_hours)
    else:
        x_start = ts_data.index.min()

    # Shared x-axis formatting — hourly major ticks, 15-min minor ticks
    ts_axes[-1].set_xlabel("Time")
    visible_hours = (x_end - x_start).total_seconds() / 3600
    major_interval = max(1, int(visible_hours / 24))  # keep ≤24 major ticks
    for ax in ts_axes:
        ax.set_xlim(x_start, x_end)
        ax.xaxis.set_major_locator(mdates.HourLocator(interval=major_interval))
        ax.xaxis.set_minor_locator(mdates.MinuteLocator(byminute=[0, 15, 30, 45]))
        ax.xaxis.set_major_formatter(mdates.DateFormatter("%m-%d\n%H:%M"))
        ax.tick_params(axis="x", which="major", labelsize=7)
        ax.tick_params(axis="x", which="minor", length=3, color="#aaaaaa")

    # ------------------------------------------------------------------ #
    # Row n_ts_rows : evaluation period detail (span both columns)        #
    # ------------------------------------------------------------------ #
    ax_eval = fig.add_subplot(gs[n_ts_rows, :])
    ax_eval.set_facecolor("white")
    ax_eval.plot(eval_data.index, eval_data.values / scale, color=data_line_color, linewidth=1, label="Data")

    orig_eval_mask = eval_orig.reindex(eval_data.index, fill_value=False)
    orig_eval_pts = eval_data[orig_eval_mask]
    if len(orig_eval_pts) > 0:
        ax_eval.scatter(
            orig_eval_pts.index,
            orig_eval_pts.values / scale,
            color="#F44336",
            s=100,
            marker="o",
            zorder=5,
            label=f"Original ({len(orig_eval_pts)})",
            edgecolors="black",
            linewidths=1,
        )

    eval_markers = ["s", "^", "D", "v", "p"]
    eval_algo_colors = ["#4CAF50", "#FF9800", "#9C27B0", "#00BCD4", "#795548"]
    for i, result in enumerate(results):
        ev_det = result["detected"]
        ev_det = ev_det[ev_det.index >= eval_start]
        det_mask = ev_det.reindex(eval_data.index, fill_value=False)
        det_pts = eval_data[det_mask]
        if len(det_pts) > 0:
            ax_eval.scatter(
                det_pts.index,
                det_pts.values / scale,
                color=eval_algo_colors[i % len(eval_algo_colors)],
                s=60,
                marker=eval_markers[i % len(eval_markers)],
                zorder=4,
                label=f"{result['algorithm']} ({len(det_pts)})",
                alpha=0.8,
            )

    ax_eval.set_xlabel("Time")
    ax_eval.set_ylabel(f"Value ({unit})")
    ax_eval.set_title(
        f"Evaluation Period Detail — {json_data.get('namespace', '')}/{json_data.get('deployment', '')}",
        fontweight="bold",
    )
    ax_eval.legend(loc="upper left", fontsize=8)
    ax_eval.grid(True, alpha=0.3)
    ax_eval.xaxis.set_major_formatter(mdates.DateFormatter("%m-%d %H:%M"))

    # ------------------------------------------------------------------ #
    # Rows n_ts_rows+1.. : cluster plots (one row per algo with a model) #
    # ------------------------------------------------------------------ #
    training_mask = data.index < eval_start
    eval_mask = data.index >= eval_start
    hour = data.index.hour.to_numpy().astype(float)
    hour_sin = np.sin(2 * np.pi * hour / 24).reshape(-1, 1)
    hour_cos = np.cos(2 * np.pi * hour / 24).reshape(-1, 1)
    cat_style = {
        "training_normal": {"color": "#2196F3", "marker": "o", "label": "Training normal", "zorder": 2, "s": 25},
        "training_outlier": {
            "color": "#FF9800",
            "marker": "o",
            "label": "Training DBSCAN outlier (pre-filter)",
            "zorder": 3,
            "s": 40,
        },
        "eval_normal": {"color": "#4CAF50", "marker": "s", "label": "Eval normal", "zorder": 3, "s": 35},
        "eval_anomaly": {"color": "#F44336", "marker": "s", "label": "Final anomaly", "zorder": 5, "s": 60},
    }

    for ci, result in enumerate(cluster_algos):
        algo = result["algo"]
        ax_hour = fig.add_subplot(gs[n_ts_rows + 1 + ci, 0])
        ax_pca = fig.add_subplot(gs[n_ts_rows + 1 + ci, 1])

        # Build features matching what the scaler was fitted on (1 feature unless temporal)
        algo_values = (data * 1000).round(4) if anomaly_type == "cpu" else data
        if getattr(algo.config, "use_temporal_features", False):
            feat_df = pd.DataFrame({"value": algo_values.values}, index=algo_values.index)
            feat_df["rate_of_change"] = algo_values.diff().fillna(0)
            rolling_mean = algo_values.rolling(window=10, min_periods=1).mean()
            feat_df["deviation_from_mean"] = algo_values - rolling_mean
            feat_df["volatility"] = algo_values.rolling(window=10, min_periods=1).std().fillna(0)
            scaler_features = feat_df.values
        else:
            scaler_features = algo_values.to_numpy().reshape(-1, 1).astype(float)

        all_scaled = algo.scaler.transform(scaler_features)
        training_scaled = all_scaled[training_mask]
        core_indices = algo.model.core_sample_indices_
        core_points = training_scaled[core_indices]

        nbrs = NearestNeighbors(n_neighbors=1).fit(core_points)
        distances, _ = nbrs.kneighbors(all_scaled)
        distances = distances.flatten()
        raw_dbscan_outliers = pd.Series(distances > algo.config.eps, index=data.index)
        # Intersect with eval_mask so training timestamps can never become eval categories.
        eval_anomalies_mask = result["detected"].reindex(data.index, fill_value=False) & eval_mask

        # Classify ALL points (training + eval) into one of 4 categories.
        categories = pd.Series("training_normal", index=data.index)
        categories[training_mask & raw_dbscan_outliers] = "training_outlier"
        categories[eval_mask & ~eval_anomalies_mask] = "eval_normal"
        categories[eval_anomalies_mask] = "eval_anomaly"

        # Augment with hour features for PCA visualization (regardless of scaler dims)
        pca = PCA(n_components=2)
        pca_coords = pca.fit_transform(np.hstack([all_scaled, hour_sin, hour_cos]))
        var_explained = pca.explained_variance_ratio_ * 100
        core_pca = pca_coords[training_mask][core_indices]

        for ax_c, use_pca in [(ax_hour, False), (ax_pca, True)]:
            ax_c.set_facecolor("white")
            for cat, cfg in cat_style.items():
                mask = (categories == cat).values
                if not mask.any():
                    continue
                if not use_pca:
                    ax_c.scatter(
                        hour[mask],
                        data.values[mask] / scale,
                        c=cfg["color"],
                        marker=cfg["marker"],
                        s=cfg["s"],
                        alpha=0.7,
                        label=cfg["label"],
                        zorder=cfg["zorder"],
                    )
                else:
                    ax_c.scatter(
                        pca_coords[mask, 0],
                        pca_coords[mask, 1],
                        c=cfg["color"],
                        marker=cfg["marker"],
                        s=cfg["s"],
                        alpha=0.7,
                        label=cfg["label"],
                        zorder=cfg["zorder"],
                    )

        # Core-point overlay on PCA subplot
        if len(core_pca) > 0:
            ax_pca.scatter(
                core_pca[:, 0],
                core_pca[:, 1],
                c="black",
                marker="+",
                s=60,
                linewidths=1.0,
                zorder=6,
                label=f"Core points ({len(core_pca)})",
            )

        n_raw = int(raw_dbscan_outliers.sum())
        n_final = int(eval_anomalies_mask.sum())
        eps_used = algo.config.eps

        ax_hour.set_xlabel("Hour of Day (0–23)")
        ax_hour.set_ylabel(f"Value ({unit})")
        ax_hour.set_title(
            f"{result['algorithm']} — Value vs Hour-of-Day\n"
            f"eps={eps_used:.3f}  min_samples={algo.config.min_samples}  |  "
            f"Core pts: {len(core_pca)}  Raw outliers: {n_raw}  Final anomalies: {n_final}"
        )
        ax_hour.legend(loc="upper left", fontsize=8)
        ax_hour.grid(True, alpha=0.3)
        ax_hour.set_xticks(range(0, 24, 2))

        ax_pca.set_xlabel(f"PC1 ({var_explained[0]:.1f}% variance)")
        ax_pca.set_ylabel(f"PC2 ({var_explained[1]:.1f}% variance)")
        ax_pca.set_title(f"{result['algorithm']} — PCA of Scaled Feature Space\n[value, hour_sin, hour_cos] → 2D")
        ax_pca.legend(loc="upper left", fontsize=8)
        ax_pca.grid(True, alpha=0.3)

    fig.suptitle(
        f"{json_data.get('namespace', '')}/{json_data.get('deployment', '')} — {anomaly_type} anomaly analysis",
        fontsize=12,
        fontweight="bold",
        y=1.01,
    )

    plt.tight_layout()

    if save_path:
        base, ext = save_path.rsplit(".", 1) if "." in save_path else (save_path, "png")
        combined_path = f"{base}_combined.{ext}"
        plt.savefig(combined_path, dpi=150, bbox_inches="tight")
        print(f"\nCombined graph saved to: {combined_path}")
    else:
        plt.show()

    plt.close()

    # Always generate the standalone cluster 2D plot alongside the combined graph
    plot_cluster_2d(data, results, eval_start, json_data, save_path)


def plot_cluster_2d(
    data: pd.Series,
    results: List[Dict],
    eval_start: datetime,
    json_data: Dict,
    save_path: Optional[str] = None,
):
    """
    Plot a 2D visualization of DBSCAN clustering to understand how the algorithm
    separates normal vs anomalous points.

    Two subplots:
      1. Value vs Hour-of-Day  — interpretable raw feature space
      2. PCA projection        — the actual 3D scaled space DBSCAN operates in

    Color coding (same in both subplots):
      Blue   (o) — Training-period normal points (in-cluster)
      Orange (o) — Raw DBSCAN outliers in training (before post-filters)
      Green  (s) — Eval-period normal (not flagged as anomaly)
      Red    (s) — Final detected anomalies (after all filters)
      Black  (+) — DBSCAN core points overlay (training only)
    """
    anomaly_type = json_data.get("anomaly_type", "memory")
    if anomaly_type == "memory":
        scale = 1e9
        unit = "GB"
    elif anomaly_type == "cpu":
        scale = 1
        unit = "cores"
    else:
        scale = 1
        unit = ""

    training_mask = data.index < eval_start
    eval_mask = data.index >= eval_start

    hour = data.index.hour.to_numpy().astype(float)
    hour_sin = np.sin(2 * np.pi * hour / 24).reshape(-1, 1)
    hour_cos = np.cos(2 * np.pi * hour / 24).reshape(-1, 1)

    for result in results:
        algo = result.get("algo")
        if algo is None or algo.scaler is None or algo.model is None:
            print(f"  Skipping cluster plot for {result['algorithm']} — algo internals not available")
            continue

        # Build features matching what the scaler was fitted on (1 feature unless temporal)
        algo_values = (data * 1000).round(4) if anomaly_type == "cpu" else data
        if getattr(algo.config, "use_temporal_features", False):
            feat_df = pd.DataFrame({"value": algo_values.values}, index=algo_values.index)
            feat_df["rate_of_change"] = algo_values.diff().fillna(0)
            rolling_mean = algo_values.rolling(window=10, min_periods=1).mean()
            feat_df["deviation_from_mean"] = algo_values - rolling_mean
            feat_df["volatility"] = algo_values.rolling(window=10, min_periods=1).std().fillna(0)
            scaler_features = feat_df.values
        else:
            scaler_features = algo_values.to_numpy().reshape(-1, 1).astype(float)

        # Scale using the fitted scaler (matched to training feature shape)
        all_scaled = algo.scaler.transform(scaler_features)
        training_scaled = all_scaled[training_mask]

        # Compute raw DBSCAN outliers (before post-filters) using stored model
        core_indices = algo.model.core_sample_indices_
        if len(core_indices) == 0:
            print(f"  No core points for {result['algorithm']} — skipping cluster plot")
            continue

        core_points = training_scaled[core_indices]
        nbrs = NearestNeighbors(n_neighbors=1).fit(core_points)
        distances, _ = nbrs.kneighbors(all_scaled)
        distances = distances.flatten()
        eps_used = algo.config.eps
        raw_dbscan_outliers = pd.Series(distances > eps_used, index=data.index)

        # Final detected anomalies (after all filters) — intersect with eval_mask so
        # training-period timestamps are never accidentally classified as eval categories.
        eval_anomalies_mask = result["detected"].reindex(data.index, fill_value=False) & eval_mask

        # Classify every data point (training + eval) into one of 4 categories.
        # Priority (last write wins in the chain below):
        #   training_normal  → training period, in-cluster
        #   training_outlier → training period, distance > eps before post-filters
        #   eval_normal      → eval period, not flagged as anomaly
        #   eval_anomaly     → eval period, final detected anomaly
        categories = pd.Series("training_normal", index=data.index)
        categories[training_mask & raw_dbscan_outliers] = "training_outlier"
        categories[eval_mask & ~eval_anomalies_mask] = "eval_normal"
        categories[eval_anomalies_mask] = "eval_anomaly"

        cat_config = {
            "training_normal": {"color": "#2196F3", "marker": "o", "label": "Training normal", "zorder": 2, "s": 25},
            "training_outlier": {
                "color": "#FF9800",
                "marker": "o",
                "label": "Training DBSCAN outlier (pre-filter)",
                "zorder": 3,
                "s": 40,
            },
            "eval_normal": {"color": "#4CAF50", "marker": "s", "label": "Eval normal", "zorder": 3, "s": 35},
            "eval_anomaly": {"color": "#F44336", "marker": "s", "label": "Final anomaly", "zorder": 5, "s": 60},
        }

        # PCA for subplot 2 — augment with hour features for richer 2D projection
        pca = PCA(n_components=2)
        pca_coords = pca.fit_transform(np.hstack([all_scaled, hour_sin, hour_cos]))
        var_explained = pca.explained_variance_ratio_ * 100

        # Core point positions in PCA space
        core_pca = pca_coords[training_mask][core_indices]

        fig, axes = plt.subplots(1, 2, figsize=(16, 7))
        fig.suptitle(
            f"DBSCAN Cluster Analysis — {result['algorithm']}\n"
            f"{json_data.get('namespace', '')}/{json_data.get('deployment', '')} ({anomaly_type})\n"
            f"eps={eps_used:.3f}  min_samples={algo.config.min_samples}",
            fontweight="bold",
            fontsize=11,
        )

        # --- Subplot 1: Value vs Hour-of-Day ---
        ax1 = axes[0]
        ax1.set_facecolor("white")
        for cat, cfg in cat_config.items():
            mask = categories == cat
            if mask.any():
                ax1.scatter(
                    hour[mask],
                    data.values[mask] / scale,
                    c=cfg["color"],
                    marker=cfg["marker"],
                    s=cfg["s"],
                    alpha=0.7,
                    label=cfg["label"],
                    zorder=cfg["zorder"],
                )
        ax1.set_xlabel("Hour of Day (0–23)")
        ax1.set_ylabel(f"Value ({unit})")
        ax1.set_title("Value vs Hour-of-Day\n(interpretable feature space)")
        ax1.legend(loc="upper left", fontsize=8)
        ax1.grid(True, alpha=0.3)
        ax1.set_xticks(range(0, 24, 2))

        # --- Subplot 2: PCA of scaled feature space ---
        ax2 = axes[1]
        ax2.set_facecolor("white")
        for cat, cfg in cat_config.items():
            mask = categories == cat
            if mask.any():
                ax2.scatter(
                    pca_coords[mask, 0],
                    pca_coords[mask, 1],
                    c=cfg["color"],
                    marker=cfg["marker"],
                    s=cfg["s"],
                    alpha=0.7,
                    label=cfg["label"],
                    zorder=cfg["zorder"],
                )

        # Overlay core points
        if len(core_pca) > 0:
            ax2.scatter(
                core_pca[:, 0],
                core_pca[:, 1],
                c="black",
                marker="+",
                s=60,
                linewidths=1.0,
                zorder=6,
                label=f"Core points ({len(core_pca)})",
            )

        ax2.set_xlabel(f"PC1 ({var_explained[0]:.1f}% variance)")
        ax2.set_ylabel(f"PC2 ({var_explained[1]:.1f}% variance)")
        ax2.set_title("PCA of Scaled Feature Space\n[value_scaled, hour_sin_scaled, hour_cos_scaled] → 2D")
        ax2.legend(loc="upper left", fontsize=8)
        ax2.grid(True, alpha=0.3)

        # Stats annotation
        n_train = training_mask.sum()
        n_eval = eval_mask.sum()
        n_core = len(core_pca)
        n_raw_outliers = int(raw_dbscan_outliers.sum())
        n_final = int(eval_anomalies_mask.sum())
        stats_text = (
            f"Training pts: {n_train}  |  Eval pts: {n_eval}\n"
            f"Core pts: {n_core}  |  Raw DBSCAN outliers: {n_raw_outliers}  |  Final anomalies: {n_final}"
        )
        fig.text(0.5, 0.01, stats_text, ha="center", fontsize=9, color="#555555")

        plt.tight_layout(rect=[0, 0.05, 1, 1])

        if save_path:
            base, ext = save_path.rsplit(".", 1) if "." in save_path else (save_path, "png")
            cluster_save_path = f"{base}_cluster_{result['algorithm']}.{ext}"
            plt.savefig(cluster_save_path, dpi=150, bbox_inches="tight")
            print(f"Cluster plot saved to: {cluster_save_path}")
        else:
            plt.show()

        plt.close()


def main():
    parser = argparse.ArgumentParser(description="Retest anomaly detection algorithms using saved JSON data")
    default_json = str(Path(__file__).parent / "sample_test_anomaly.json")
    parser.add_argument(
        "json_file",
        nargs="?",
        default=default_json,
        help="Path to JSON file with anomaly data (default: sample_test_anomaly.json)",
    )
    parser.add_argument(
        "--algorithm",
        "-a",
        choices=ALGORITHMS,
        help="Run only this algorithm (default: all)",
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Run all algorithms",
    )
    parser.add_argument(
        "--show-timestamps",
        "-t",
        action="store_true",
        help="Show anomaly timestamps",
    )
    parser.add_argument(
        "--output",
        "-o",
        help="Save results to JSON file",
    )
    parser.add_argument(
        "--graph",
        "-g",
        action="store_true",
        help="Show graph of data with anomalies",
    )
    parser.add_argument(
        "--save-graph",
        "-s",
        help="Save graph to file (e.g., output.png)",
    )
    parser.add_argument(
        "--zoom-hours",
        "-z",
        type=float,
        default=None,
        help="Zoom time-series graphs to this many hours before eval_start (e.g. -z 3)",
    )
    parser.add_argument(
        "--cluster-plot",
        "-p",
        action="store_true",
        help="Show 2D cluster visualization of DBSCAN feature space (value vs hour + PCA projection)",
    )
    parser.add_argument(
        "--save-cluster-plot",
        help="Save cluster plot to file (e.g., clusters.png)",
    )

    args = parser.parse_args()

    # Default save directory for all plots
    TEST_RESULT_DIR = Path(__file__).parent / "test_result"
    TEST_RESULT_DIR.mkdir(exist_ok=True)

    # Load data
    print(f"Loading data from: {args.json_file}")
    json_data = load_json_data(args.json_file)
    data = json_to_series(json_data)
    original_anomalies = get_original_anomalies(json_data)

    # Calculate evaluation start time
    eval_period = json_data.get("evaluation_period", 60)
    eval_start = data.index.max() - timedelta(minutes=eval_period)

    # Print summary
    print_data_summary(json_data, data)

    # Determine which algorithms to run
    if args.algorithm:
        algorithms = [args.algorithm]
    else:
        algorithms = ALGORITHMS

    # Run algorithms
    results = []
    for algo_name in algorithms:
        print(f"\nRunning {algo_name}...")
        try:
            result = run_algorithm(algo_name, data, json_data)
            results.append(result)
            print(f"  -> has_anomaly: {result['has_anomaly']}, count: {result['anomaly_count']}")
        except Exception as e:
            print(f"  -> ERROR: {e}")

    # Print results
    print_results(results, original_anomalies, eval_start)

    if args.show_timestamps:
        print_anomaly_timestamps(results, eval_start)

    # Save to JSON if requested
    if args.output:
        output_data = {
            "source_file": args.json_file,
            "algorithms": [],
        }
        for result in results:
            comparison = compare_with_original(
                original_anomalies,
                result["detected"],
                eval_start,
            )
            output_data["algorithms"].append(
                {
                    "name": result["algorithm"],
                    "has_anomaly": result["has_anomaly"],
                    "anomaly_count": result["anomaly_count"],
                    "metrics": comparison,
                }
            )

        with open(args.output, "w") as f:
            json.dump(output_data, f, indent=2)
        print(f"\nResults saved to: {args.output}")

    # Resolve save paths — default to test_result/ when no explicit path is given
    json_stem = Path(args.json_file).stem
    default_graph_path = str(TEST_RESULT_DIR / f"{json_stem}.png")
    default_cluster_path = str(TEST_RESULT_DIR / f"{json_stem}_cluster.png")

    # Generate graphs if requested
    if args.graph or args.save_graph:
        save_graph = args.save_graph or default_graph_path
        print(f"\nSaving combined graph to: {save_graph}")
        plot_combined(data, original_anomalies, results, eval_start, json_data, save_graph, zoom_hours=args.zoom_hours)

    # Generate 2D cluster visualization if requested (standalone, without time-series graphs)
    elif args.cluster_plot or args.save_cluster_plot:
        save_cluster = args.save_cluster_plot or default_cluster_path
        print(f"\nSaving cluster plot to: {save_cluster}")
        plot_cluster_2d(data, results, eval_start, json_data, save_cluster)


if __name__ == "__main__":
    main()
