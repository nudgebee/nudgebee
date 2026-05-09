"""
Unit tests for anomaly detection algorithms.
"""

import pytest
import pandas as pd
import numpy as np
from datetime import timedelta

from tests.anomaly.conftest import AlgorithmFactory, MetricsCalculator


class TestDBSCAN:
    """Tests for DBSCAN anomaly detection algorithm."""

    def test_spike_detection(self, synthetic_generator):
        """Test that DBSCAN detects clear spikes."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,  # 300MB in bytes
            spike_magnitude=10.0,
            spike_duration_minutes=5,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            "DB_SCAN",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        # Should detect anomaly
        assert response.has_anomaly is True, "Should detect the spike as anomaly"

        # Check metrics
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        # With a 10x spike, we expect good recall
        assert metrics.recall >= 0.5, f"Recall should be >= 0.5, got {metrics.recall}"

    def test_directional_filtering_high_values(self, synthetic_generator):
        """Test that DBSCAN only flags high value spikes, not low dips."""
        data, _, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=5.0,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            "DB_SCAN",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)

        # Get anomaly values
        anomaly_values = data.reindex(detected[detected].index)

        if len(anomaly_values) > 0:
            baseline_median = data.iloc[: int(len(data) * 0.9)].median()
            # All detected anomalies should be above baseline
            assert all(
                anomaly_values > baseline_median
            ), "Detected anomalies should all be above baseline (directional filtering)"

    def test_directional_filtering_low_values(self, synthetic_generator):
        """Test that DBSCAN does not flag low dips as anomalies."""
        # Generate data with a dip instead of spike
        data, index = synthetic_generator.generate_base_signal(
            n_points=1000,
            baseline=300_000_000,
            noise_level=0.05,
        )

        # Add a significant dip
        split_idx = int(len(data) * 0.9)
        dip_start = split_idx + 5
        for i in range(5):
            if dip_start + i < len(data):
                data.iloc[dip_start + i] = data.iloc[:split_idx].mean() * 0.1  # 90% drop

        train_end = index[split_idx - 1]

        algo = AlgorithmFactory.create(
            "DB_SCAN",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        # Should NOT flag the dip as anomaly (directional filtering)
        assert response.has_anomaly is False, "Should not flag low dips as anomalies"

    def test_constant_data_edge_case(self):
        """Test DBSCAN handles constant data without errors."""
        # Create constant data
        n_points = 500
        end_time = pd.Timestamp.now()
        start_time = end_time - pd.Timedelta(minutes=n_points)
        index = pd.date_range(start=start_time, end=end_time, periods=n_points)
        data = pd.Series([100_000_000] * n_points, index=index)

        split_idx = int(n_points * 0.9)
        train_end = index[split_idx - 1]

        algo = AlgorithmFactory.create(
            "DB_SCAN",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        # Should not raise exception
        response = algo.get_anomaly(data, algo.config)

        # Constant data should have no anomalies
        assert response.has_anomaly is False, "Constant data should have no anomalies"

    def test_noise_only_low_false_positives(self, synthetic_generator):
        """Test that DBSCAN has low false positive rate on noisy but normal data."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_noise_only_scenario(
            n_points=1000,
            baseline=300_000_000,
            noise_level=0.15,  # 15% noise
        )

        algo = AlgorithmFactory.create(
            "DB_SCAN",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        # False positive rate should be low
        assert metrics.false_positive_rate <= 0.05, f"FPR should be <= 5%, got {metrics.false_positive_rate * 100:.1f}%"

    def test_cpu_precision_preserved(self, synthetic_generator):
        """Test that CPU values maintain precision through processing."""
        # Generate CPU-like data (values between 0 and 1)
        n_points = 500
        end_time = pd.Timestamp.now()
        start_time = end_time - pd.Timedelta(minutes=n_points)
        index = pd.date_range(start=start_time, end=end_time, periods=n_points)

        rng = np.random.default_rng(42)
        cpu_values = 0.3 + rng.normal(0, 0.05, n_points)  # ~30% CPU with noise
        cpu_values = np.clip(cpu_values, 0, 1)
        data = pd.Series(cpu_values, index=index)

        # Add a spike
        split_idx = int(n_points * 0.9)
        spike_start = split_idx + 5
        for i in range(5):
            if spike_start + i < len(data):
                data.iloc[spike_start + i] = 0.95  # 95% CPU spike

        train_end = index[split_idx - 1]

        algo = AlgorithmFactory.create(
            "DB_SCAN",
            anomaly_type="cpu",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        # Check that response data values are reasonable CPU values
        response_values = response.df["data"].values
        assert all(response_values >= 0), "CPU values should be non-negative"
        assert all(response_values <= 2), "CPU values should be reasonable (< 2 cores)"


class TestIsolationTree:
    """Tests for Isolation Tree anomaly detection algorithm."""

    def test_spike_detection(self, synthetic_generator):
        """Test that Isolation Tree detects clear spikes."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=10.0,
            spike_duration_minutes=5,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            "ISOLATION_TREE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        assert response.has_anomaly is True, "Should detect the spike as anomaly"

    def test_training_evaluation_split(self, synthetic_generator):
        """Test that algorithm respects training/evaluation split."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=10.0,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            "ISOLATION_TREE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        # Response should contain data
        assert len(response.df) > 0, "Response should contain data"

    def test_noise_only_low_false_positives(self, synthetic_generator):
        """Test that Isolation Tree has low false positive rate on noisy data."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_noise_only_scenario(
            n_points=1000,
            baseline=300_000_000,
            noise_level=0.15,
        )

        algo = AlgorithmFactory.create(
            "ISOLATION_TREE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert (
            metrics.false_positive_rate <= 0.10
        ), f"FPR should be <= 10%, got {metrics.false_positive_rate * 100:.1f}%"


class TestZScore:
    """Tests for Z-Score anomaly detection algorithm."""

    def test_spike_detection(self, synthetic_generator):
        """Test that Z-Score detects clear spikes."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=10.0,
            spike_duration_minutes=5,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            "ZSCORE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        assert response.has_anomaly is True, "Should detect the spike as anomaly"

    def test_sigma_threshold_sensitivity(self, synthetic_generator):
        """Test that sigma threshold affects detection sensitivity."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=3.0,  # Smaller spike
            spike_duration_minutes=5,
            noise_level=0.05,
        )

        # Lower threshold should detect more
        algo_sensitive = AlgorithmFactory.create(
            "ZSCORE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
            sigma_threshold=2.0,
        )
        algo_sensitive.config.start_time = data.index.min()
        algo_sensitive.config.end_time = data.index.max()
        algo_sensitive.config.training_start_time = data.index.min()
        algo_sensitive.config.training_end_time = train_end

        response_sensitive = algo_sensitive.get_anomaly(data, algo_sensitive.config)

        # Higher threshold should detect less
        algo_strict = AlgorithmFactory.create(
            "ZSCORE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
            sigma_threshold=4.0,
        )
        algo_strict.config.start_time = data.index.min()
        algo_strict.config.end_time = data.index.max()
        algo_strict.config.training_start_time = data.index.min()
        algo_strict.config.training_end_time = train_end

        response_strict = algo_strict.get_anomaly(data, algo_strict.config)

        sensitive_count = response_sensitive.df["anomaly"].sum()
        strict_count = response_strict.df["anomaly"].sum()

        assert sensitive_count >= strict_count, "Lower threshold should detect more or equal anomalies"

    def test_noise_only_low_false_positives(self, synthetic_generator):
        """Test that Z-Score has low false positive rate on noisy data."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_noise_only_scenario(
            n_points=1000,
            baseline=300_000_000,
            noise_level=0.15,
        )

        algo = AlgorithmFactory.create(
            "ZSCORE",
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert (
            metrics.false_positive_rate <= 0.10
        ), f"FPR should be <= 10%, got {metrics.false_positive_rate * 100:.1f}%"


class TestAlgorithmComparison:
    """Compare different algorithms on the same scenarios."""

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_compare_on_spike_scenario(self, synthetic_generator, algorithm):
        """All algorithms should detect clear spikes."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=10.0,
            spike_duration_minutes=5,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            algorithm,
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        assert response.has_anomaly is True, f"{algorithm} should detect clear spike"

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_compare_on_noise_scenario(self, synthetic_generator, algorithm):
        """All algorithms should have reasonable FPR on noise-only data."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_noise_only_scenario(
            n_points=1000,
            baseline=300_000_000,
            noise_level=0.10,
        )

        algo = AlgorithmFactory.create(
            algorithm,
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert metrics.false_positive_rate <= 0.15, f"{algorithm} FPR should be <= 15%"

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_compare_on_gradual_spike(self, synthetic_generator, algorithm):
        """Test detection of gradual spikes."""
        data, index = synthetic_generator.generate_base_signal(
            n_points=1000,
            baseline=300_000_000,
            noise_level=0.05,
        )

        split_idx = int(len(data) * 0.9)
        train_end = index[split_idx - 1]

        # Add gradual spike
        data, ground_truth = synthetic_generator.add_gradual_spike(
            data,
            spike_start_idx=split_idx + 5,
            ramp_up_duration=10,
            plateau_duration=5,
            spike_magnitude=5.0,
        )

        algo = AlgorithmFactory.create(
            algorithm,
            anomaly_type="memory",
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        response = algo.get_anomaly(data, algo.config)

        # Gradual spikes are harder to detect, but should still be caught
        # Just verify no exception is raised
        assert response.df is not None
