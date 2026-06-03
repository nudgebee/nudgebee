"""
Test fixtures and utilities for anomaly detection tests.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent.parent))

import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
from datetime import datetime, timedelta  # noqa: E402
from typing import Optional, Tuple  # noqa: E402
from dataclasses import dataclass  # noqa: E402

import pytest  # noqa: E402

from server.anomaly.anomaly_algo import get_anomaly_algo  # noqa: E402


class SyntheticDataGenerator:
    """Generate synthetic time series data for testing anomaly detection algorithms."""

    def __init__(self, seed: int = 42):
        self.rng = np.random.default_rng(seed)

    def generate_base_signal(
        self,
        n_points: int = 1000,
        baseline: float = 100.0,
        noise_level: float = 0.05,
        freq_minutes: int = 1,
    ) -> Tuple[pd.Series, pd.DatetimeIndex]:
        """Generate base signal with noise."""
        end_time = datetime.now()
        start_time = end_time - timedelta(minutes=n_points * freq_minutes)
        index = pd.date_range(start=start_time, end=end_time, periods=n_points)

        # Base signal with Gaussian noise
        noise = self.rng.normal(0, baseline * noise_level, n_points)
        values = baseline + noise

        return pd.Series(values, index=index), index

    def add_spike(
        self,
        data: pd.Series,
        spike_start_idx: int,
        spike_duration: int = 5,
        spike_magnitude: float = 10.0,
    ) -> Tuple[pd.Series, pd.Series]:
        """Add a spike to the data and return ground truth labels."""
        data = data.copy()
        ground_truth = pd.Series(False, index=data.index)

        spike_end_idx = min(spike_start_idx + spike_duration, len(data))
        baseline = data.iloc[:spike_start_idx].mean()

        # Create spike
        for i in range(spike_start_idx, spike_end_idx):
            data.iloc[i] = baseline * spike_magnitude
            ground_truth.iloc[i] = True

        return data, ground_truth

    def add_gradual_spike(
        self,
        data: pd.Series,
        spike_start_idx: int,
        ramp_up_duration: int = 10,
        plateau_duration: int = 5,
        spike_magnitude: float = 5.0,
    ) -> Tuple[pd.Series, pd.Series]:
        """Add a gradual spike with ramp-up."""
        data = data.copy()
        ground_truth = pd.Series(False, index=data.index)

        baseline = data.iloc[:spike_start_idx].mean()
        total_duration = ramp_up_duration + plateau_duration

        for i in range(total_duration):
            idx = spike_start_idx + i
            if idx >= len(data):
                break

            if i < ramp_up_duration:
                # Ramp up phase
                progress = (i + 1) / ramp_up_duration
                multiplier = 1 + (spike_magnitude - 1) * progress
            else:
                # Plateau phase
                multiplier = spike_magnitude

            data.iloc[idx] = baseline * multiplier
            # Only mark plateau as anomaly (where it's clearly elevated)
            if i >= ramp_up_duration:
                ground_truth.iloc[idx] = True

        return data, ground_truth

    def generate_spike_scenario(
        self,
        n_points: int = 1000,
        baseline: float = 100.0,
        noise_level: float = 0.05,
        spike_magnitude: float = 10.0,
        spike_duration_minutes: int = 5,
        training_ratio: float = 0.9,
    ) -> Tuple[pd.Series, pd.Series, datetime, datetime]:
        """
        Generate a complete spike scenario with training/evaluation split.

        Returns:
            data: Time series with spike
            ground_truth: Boolean series marking true anomalies
            training_end: End of training period
            eval_start: Start of evaluation period
        """
        data, index = self.generate_base_signal(n_points, baseline, noise_level)

        # Calculate split point
        split_idx = int(n_points * training_ratio)
        training_end = index[split_idx - 1]
        eval_start = index[split_idx]

        # Add spike in evaluation period
        spike_start = split_idx + 5  # 5 minutes into evaluation
        data, ground_truth = self.add_spike(data, spike_start, spike_duration_minutes, spike_magnitude)

        return data, ground_truth, training_end, eval_start

    def generate_noise_only_scenario(
        self,
        n_points: int = 1000,
        baseline: float = 100.0,
        noise_level: float = 0.10,
        training_ratio: float = 0.9,
    ) -> Tuple[pd.Series, pd.Series, datetime, datetime]:
        """Generate scenario with only noise (no true anomalies)."""
        data, index = self.generate_base_signal(n_points, baseline, noise_level)

        split_idx = int(n_points * training_ratio)
        training_end = index[split_idx - 1]
        eval_start = index[split_idx]

        ground_truth = pd.Series(False, index=data.index)

        return data, ground_truth, training_end, eval_start

    def generate_multiple_spikes_scenario(
        self,
        n_points: int = 1000,
        baseline: float = 100.0,
        noise_level: float = 0.05,
        num_spikes: int = 3,
        spike_magnitude: float = 5.0,
        training_ratio: float = 0.9,
    ) -> Tuple[pd.Series, pd.Series, datetime, datetime]:
        """Generate scenario with multiple spikes in evaluation period."""
        data, index = self.generate_base_signal(n_points, baseline, noise_level)

        split_idx = int(n_points * training_ratio)
        training_end = index[split_idx - 1]
        eval_start = index[split_idx]

        ground_truth = pd.Series(False, index=data.index)
        eval_length = n_points - split_idx

        # Distribute spikes evenly in evaluation period
        for i in range(num_spikes):
            spike_pos = split_idx + int((i + 0.5) * eval_length / num_spikes)
            data, spike_gt = self.add_spike(data, spike_pos, spike_duration=3, spike_magnitude=spike_magnitude)
            ground_truth = ground_truth | spike_gt

        return data, ground_truth, training_end, eval_start


class AlgorithmFactory:
    """Factory for creating anomaly detection algorithm instances."""

    @staticmethod
    def create(
        algorithm_name: str,
        anomaly_type: str = "memory",
        evaluation_period: timedelta = timedelta(hours=1),
        **kwargs,
    ):
        """Create an algorithm instance with default config."""
        algo_class, config_class = get_anomaly_algo(algorithm_name)

        config = config_class(
            # account_id is a UUID field; a non-UUID string fails pydantic validation.
            account_id="00000000-0000-0000-0000-000000000001",
            namespace="test-namespace",
            deployment="test-deployment",
            anomaly_type=anomaly_type,
            evaluation_period=evaluation_period,
            **kwargs,
        )

        return algo_class(config=config)


@dataclass
class MetricsResult:
    """Container for evaluation metrics."""

    precision: float
    recall: float
    f1_score: float
    false_positive_rate: float
    true_positives: int
    false_positives: int
    false_negatives: int
    true_negatives: int
    detection_latency: Optional[float] = None


class MetricsCalculator:
    """Calculate evaluation metrics for anomaly detection."""

    @staticmethod
    def calculate(
        detected: pd.Series,
        ground_truth: pd.Series,
        eval_start: datetime,
    ) -> MetricsResult:
        """Calculate metrics for detected vs ground truth anomalies."""
        # Filter to evaluation period only
        eval_mask = detected.index >= eval_start
        det = detected[eval_mask]
        gt = ground_truth.reindex(det.index, fill_value=False)

        tp = int((det & gt).sum())
        fp = int((det & ~gt).sum())
        fn = int((~det & gt).sum())
        tn = int((~det & ~gt).sum())

        precision = tp / (tp + fp) if (tp + fp) > 0 else 1.0
        recall = tp / (tp + fn) if (tp + fn) > 0 else 1.0
        f1 = 2 * precision * recall / (precision + recall) if (precision + recall) > 0 else 0.0
        fpr = fp / (fp + tn) if (fp + tn) > 0 else 0.0

        # Calculate detection latency (time from first true anomaly to first detection)
        latency = None
        if gt.any() and det.any():
            first_true = gt[gt].index.min()
            first_detected = det[det].index.min()
            if first_detected >= first_true:
                latency = (first_detected - first_true).total_seconds() / 60  # in minutes

        return MetricsResult(
            precision=precision,
            recall=recall,
            f1_score=f1,
            false_positive_rate=fpr,
            true_positives=tp,
            false_positives=fp,
            false_negatives=fn,
            true_negatives=tn,
            detection_latency=latency,
        )


@pytest.fixture
def synthetic_generator():
    """Fixture for synthetic data generator."""
    return SyntheticDataGenerator(seed=42)


@pytest.fixture
def algorithm_factory():
    """Fixture for algorithm factory."""
    return AlgorithmFactory()
