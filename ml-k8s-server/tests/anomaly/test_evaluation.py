"""
Integration tests for evaluating anomaly detection algorithms.
"""

import pytest
import pandas as pd
from datetime import timedelta

from tests.anomaly.conftest import SyntheticDataGenerator, AlgorithmFactory, MetricsCalculator


class TestEvaluation:
    """Integration tests for algorithm evaluation."""

    @pytest.mark.parametrize(
        "scenario_name,spike_magnitude",
        [
            ("spike_10x", 10.0),
            ("spike_5x", 5.0),
            ("spike_3x", 3.0),
        ],
    )
    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_algorithm_performance_on_spike(self, synthetic_generator, scenario_name, spike_magnitude, algorithm):
        """Test algorithm performance on different spike magnitudes."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=1000,
            baseline=300_000_000,
            spike_magnitude=spike_magnitude,
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
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        # Large spikes (10x) should always be detected
        if spike_magnitude >= 10.0:
            assert response.has_anomaly is True, f"{algorithm} should detect {scenario_name}"

        # All algorithms should maintain reasonable precision
        if response.has_anomaly:
            assert metrics.precision >= 0.1, f"{algorithm} precision too low on {scenario_name}"

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_noise_only_false_positive_rate(self, synthetic_generator, algorithm):
        """Test false positive rate on noise-only data."""
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

        # False positive rate should be low
        assert (
            metrics.false_positive_rate <= 0.10
        ), f"{algorithm} FPR too high: {metrics.false_positive_rate * 100:.1f}%"

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_multiple_spikes_detection(self, synthetic_generator, algorithm):
        """Test detection of multiple spikes."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_multiple_spikes_scenario(
            n_points=1000,
            baseline=300_000_000,
            num_spikes=3,
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
        detected = pd.Series(response.df["anomaly"].values, index=response.df.index)
        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        # Should detect at least some anomalies
        if ground_truth[ground_truth.index >= eval_start].sum() > 0:
            assert (
                metrics.true_positives > 0 or metrics.false_positives > 0
            ), f"{algorithm} should make some detections with multiple spikes"

    def test_algorithm_comparison_report(self, synthetic_generator):
        """Generate comparison report for all algorithms."""
        scenarios = [
            (
                "spike_10x",
                synthetic_generator.generate_spike_scenario(
                    n_points=500, baseline=300_000_000, spike_magnitude=10.0, noise_level=0.05
                ),
            ),
            (
                "spike_5x",
                synthetic_generator.generate_spike_scenario(
                    n_points=500, baseline=300_000_000, spike_magnitude=5.0, noise_level=0.05
                ),
            ),
            (
                "noise_only",
                synthetic_generator.generate_noise_only_scenario(n_points=500, baseline=300_000_000, noise_level=0.10),
            ),
        ]

        algorithms = ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"]
        results = []

        for scenario_name, (data, ground_truth, train_end, eval_start) in scenarios:
            for algo_name in algorithms:
                algo = AlgorithmFactory.create(
                    algo_name,
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

                results.append(
                    {
                        "scenario": scenario_name,
                        "algorithm": algo_name,
                        "precision": metrics.precision,
                        "recall": metrics.recall,
                        "f1_score": metrics.f1_score,
                        "fpr": metrics.false_positive_rate,
                    }
                )

        # Just verify we got results for all combinations
        assert len(results) == len(scenarios) * len(algorithms)


class TestEdgeCases:
    """Test edge cases and boundary conditions."""

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_constant_data_handling(self, algorithm):
        """Test that algorithms handle constant data gracefully."""
        n_points = 200
        end_time = pd.Timestamp.now()
        start_time = end_time - pd.Timedelta(minutes=n_points)
        index = pd.date_range(start=start_time, end=end_time, periods=n_points)

        # Constant data
        data = pd.Series([100_000_000] * n_points, index=index)
        split_idx = int(n_points * 0.9)
        train_end = index[split_idx - 1]

        algo = AlgorithmFactory.create(
            algorithm,
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
        assert response.has_anomaly is False, f"{algorithm} should not flag constant data"

    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_small_dataset_handling(self, algorithm):
        """Test that algorithms handle small datasets."""
        n_points = 50  # Small dataset
        end_time = pd.Timestamp.now()
        start_time = end_time - pd.Timedelta(minutes=n_points)
        index = pd.date_range(start=start_time, end=end_time, periods=n_points)

        rng = SyntheticDataGenerator(seed=42).rng
        data = pd.Series(100_000_000 + rng.normal(0, 5_000_000, n_points), index=index)

        split_idx = int(n_points * 0.8)
        train_end = index[split_idx - 1]

        algo = AlgorithmFactory.create(
            algorithm,
            anomaly_type="memory",
            evaluation_period=timedelta(minutes=10),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        # Should not raise exception
        response = algo.get_anomaly(data, algo.config)
        assert response.df is not None


class TestMetricTypes:
    """Test algorithms with different metric types."""

    @pytest.mark.parametrize("metric_type", ["memory", "cpu", "errorrate", "latency"])
    @pytest.mark.parametrize("algorithm", ["DB_SCAN", "ISOLATION_TREE", "ZSCORE"])
    def test_metric_type_handling(self, synthetic_generator, metric_type, algorithm):
        """Test that algorithms handle different metric types."""
        # Adjust baseline based on metric type
        baselines = {
            "memory": 300_000_000,  # 300MB
            "cpu": 0.3,  # 30%
            "errorrate": 0.001,  # 0.1%
            "latency": 100,  # 100ms
        }
        baseline = baselines[metric_type]

        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=500,
            baseline=baseline,
            spike_magnitude=5.0,
            spike_duration_minutes=5,
            noise_level=0.05,
        )

        algo = AlgorithmFactory.create(
            algorithm,
            anomaly_type=metric_type,
            evaluation_period=timedelta(hours=1),
        )
        algo.config.start_time = data.index.min()
        algo.config.end_time = data.index.max()
        algo.config.training_start_time = data.index.min()
        algo.config.training_end_time = train_end

        # Should not raise exception
        response = algo.get_anomaly(data, algo.config)
        assert response.df is not None, f"{algorithm} should handle {metric_type} metric"
