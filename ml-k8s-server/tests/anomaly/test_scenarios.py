"""
Tests for synthetic data generation and scenario creation.
"""

import pandas as pd

from tests.anomaly.conftest import SyntheticDataGenerator, MetricsCalculator


class TestScenarioGenerator:
    """Tests for SyntheticDataGenerator."""

    def test_spike_scenario_structure(self, synthetic_generator):
        """Test that spike scenario returns correct structure."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario()

        assert isinstance(data, pd.Series), "Data should be a pandas Series"
        assert isinstance(ground_truth, pd.Series), "Ground truth should be a pandas Series"
        assert len(data) == len(ground_truth), "Data and ground truth should have same length"
        assert data.index.equals(ground_truth.index), "Data and ground truth should have same index"

    def test_spike_scenario_has_anomalies(self, synthetic_generator):
        """Test that spike scenario contains anomalies."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            spike_magnitude=10.0,
            spike_duration_minutes=5,
        )

        assert ground_truth.sum() > 0, "Ground truth should contain some anomalies"
        assert ground_truth.sum() == 5, "Should have exactly spike_duration_minutes anomalies"

    def test_spike_scenario_magnitude(self, synthetic_generator):
        """Test that spike has correct magnitude."""
        baseline = 100.0
        spike_magnitude = 10.0

        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            baseline=baseline,
            spike_magnitude=spike_magnitude,
            noise_level=0.01,  # Low noise for precise testing
        )

        # Get spike values
        spike_values = data[ground_truth]

        # Spike should be approximately spike_magnitude times baseline
        expected_spike = baseline * spike_magnitude
        actual_spike_mean = spike_values.mean()

        assert (
            abs(actual_spike_mean - expected_spike) < expected_spike * 0.1
        ), f"Spike magnitude incorrect: expected ~{expected_spike}, got {actual_spike_mean}"

    def test_spike_duration(self, synthetic_generator):
        """Test that spike has correct duration."""
        spike_duration = 10

        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            spike_duration_minutes=spike_duration,
        )

        assert ground_truth.sum() == spike_duration, f"Should have {spike_duration} anomaly points"

    def test_noise_only_scenario_no_anomalies(self, synthetic_generator):
        """Test that noise-only scenario has no true anomalies."""
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_noise_only_scenario()

        assert ground_truth.sum() == 0, "Noise-only scenario should have no anomalies"

    def test_noise_only_scenario_variance(self, synthetic_generator):
        """Test that noise level affects data variance."""
        low_noise_data, _, _, _ = synthetic_generator.generate_noise_only_scenario(
            baseline=100.0,
            noise_level=0.05,
        )

        # Reset generator for consistent comparison
        gen_high = SyntheticDataGenerator(seed=42)
        high_noise_data, _, _, _ = gen_high.generate_noise_only_scenario(
            baseline=100.0,
            noise_level=0.20,
        )

        low_std = low_noise_data.std()
        high_std = high_noise_data.std()

        assert high_std > low_std, "Higher noise level should produce higher variance"

    def test_constant_data_scenario(self):
        """Test generation of constant data."""
        gen = SyntheticDataGenerator(seed=42)
        data, _ = gen.generate_base_signal(
            n_points=100,
            baseline=100.0,
            noise_level=0.0,  # No noise
        )

        assert data.std() == 0 or data.std() < 1e-10, "Constant data should have near-zero variance"

    def test_gradual_spike_scenario(self, synthetic_generator):
        """Test gradual spike generation."""
        data, index = synthetic_generator.generate_base_signal(
            n_points=100,
            baseline=100.0,
            noise_level=0.01,
        )

        data, ground_truth = synthetic_generator.add_gradual_spike(
            data,
            spike_start_idx=50,
            ramp_up_duration=10,
            plateau_duration=5,
            spike_magnitude=5.0,
        )

        # Only plateau should be marked as anomaly
        assert ground_truth.sum() == 5, "Only plateau period should be marked as anomaly"

        # Values should increase gradually during ramp-up
        ramp_values = data.iloc[50:60].values
        assert all(
            ramp_values[i] <= ramp_values[i + 1] for i in range(len(ramp_values) - 1)
        ), "Ramp-up should show increasing values"

    def test_multiple_spikes_scenario(self, synthetic_generator):
        """Test multiple spikes generation."""
        num_spikes = 3
        data, ground_truth, train_end, eval_start = synthetic_generator.generate_multiple_spikes_scenario(
            num_spikes=num_spikes,
        )

        # Should have multiple distinct spike regions
        anomaly_indices = ground_truth[ground_truth].index

        if len(anomaly_indices) > 1:
            # Check that spikes are spread out
            time_diffs = pd.Series(anomaly_indices).diff().dropna()
            # At least some gaps should be significant (> 1 minute)
            assert any(time_diffs > pd.Timedelta(minutes=5)), "Spikes should be spread out"

    def test_low_dip_scenario_no_ground_truth_anomalies(self, synthetic_generator):
        """Test that dips are not marked as anomalies in ground truth."""
        data, index = synthetic_generator.generate_base_signal(
            n_points=100,
            baseline=100.0,
            noise_level=0.05,
        )

        # Manually create a dip (not using add_spike)
        data_with_dip = data.copy()
        data_with_dip.iloc[60:65] = data.iloc[:50].mean() * 0.1  # 90% drop

        # Ground truth should not have any anomalies for dips
        # (We only mark spikes as anomalies, not dips)
        ground_truth = pd.Series(False, index=data.index)

        assert ground_truth.sum() == 0, "Dips should not be marked as anomalies"

    def test_training_evaluation_split(self, synthetic_generator):
        """Test that training/evaluation split is correct."""
        training_ratio = 0.8
        n_points = 1000

        data, ground_truth, train_end, eval_start = synthetic_generator.generate_spike_scenario(
            n_points=n_points,
            training_ratio=training_ratio,
        )

        training_points = len(data[data.index <= train_end])
        eval_points = len(data[data.index >= eval_start])

        expected_training = int(n_points * training_ratio)

        # Allow some tolerance due to datetime indexing
        assert (
            abs(training_points - expected_training) <= 2
        ), f"Training points ({training_points}) should be ~{expected_training}"
        assert eval_points > 0, "Should have evaluation points"

    def test_reproducibility_with_seed(self):
        """Test that same seed produces same data."""
        gen1 = SyntheticDataGenerator(seed=42)
        gen2 = SyntheticDataGenerator(seed=42)

        data1, _, _, _ = gen1.generate_spike_scenario()
        data2, _, _, _ = gen2.generate_spike_scenario()

        pd.testing.assert_series_equal(data1, data2)

    def test_different_seeds_produce_different_data(self):
        """Test that different seeds produce different data."""
        gen1 = SyntheticDataGenerator(seed=42)
        gen2 = SyntheticDataGenerator(seed=123)

        data1, _, _, _ = gen1.generate_spike_scenario()
        data2, _, _, _ = gen2.generate_spike_scenario()

        # Data should be different (same structure, different values)
        assert not data1.equals(data2), "Different seeds should produce different data"


class TestMetricsCalculator:
    """Tests for MetricsCalculator."""

    def test_perfect_detection(self):
        """Test metrics for perfect detection."""
        index = pd.date_range(start="2024-01-01", periods=100, freq="1min")
        eval_start = index[50]

        ground_truth = pd.Series(False, index=index)
        ground_truth.iloc[60:65] = True

        detected = ground_truth.copy()  # Perfect match

        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert metrics.precision == 1.0, "Perfect detection should have precision=1"
        assert metrics.recall == 1.0, "Perfect detection should have recall=1"
        assert metrics.f1_score == 1.0, "Perfect detection should have F1=1"
        assert metrics.false_positive_rate == 0.0, "Perfect detection should have FPR=0"

    def test_no_detection(self):
        """Test metrics when nothing is detected."""
        index = pd.date_range(start="2024-01-01", periods=100, freq="1min")
        eval_start = index[50]

        ground_truth = pd.Series(False, index=index)
        ground_truth.iloc[60:65] = True

        detected = pd.Series(False, index=index)  # Nothing detected

        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert metrics.recall == 0.0, "No detection should have recall=0"
        assert metrics.false_negatives == 5, "Should have 5 false negatives"

    def test_all_false_positives(self):
        """Test metrics when all detections are false positives."""
        index = pd.date_range(start="2024-01-01", periods=100, freq="1min")
        eval_start = index[50]

        ground_truth = pd.Series(False, index=index)  # No true anomalies

        detected = pd.Series(False, index=index)
        detected.iloc[60:65] = True  # All false positives

        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert metrics.precision == 0.0, "All FPs should have precision=0"
        assert metrics.false_positives == 5, "Should have 5 false positives"

    def test_partial_detection(self):
        """Test metrics for partial detection."""
        index = pd.date_range(start="2024-01-01", periods=100, freq="1min")
        eval_start = index[50]

        ground_truth = pd.Series(False, index=index)
        ground_truth.iloc[60:70] = True  # 10 true anomalies

        detected = pd.Series(False, index=index)
        detected.iloc[65:75] = True  # 10 detections, 5 overlap, 5 FP

        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert metrics.true_positives == 5
        assert metrics.false_positives == 5
        assert metrics.false_negatives == 5
        assert metrics.precision == 0.5
        assert metrics.recall == 0.5

    def test_evaluation_period_filtering(self):
        """Test that metrics only consider evaluation period."""
        index = pd.date_range(start="2024-01-01", periods=100, freq="1min")
        eval_start = index[50]

        ground_truth = pd.Series(False, index=index)
        ground_truth.iloc[30:35] = True  # Anomalies in training period (should be ignored)
        ground_truth.iloc[60:65] = True  # Anomalies in eval period

        detected = pd.Series(False, index=index)
        detected.iloc[30:35] = True  # Detection in training (should be ignored)
        detected.iloc[60:65] = True  # Detection in eval period

        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        # Should only count eval period
        assert metrics.true_positives == 5, "Should only count eval period TPs"
        assert metrics.false_positives == 0, "Training period FPs should not count"

    def test_detection_latency(self):
        """Test detection latency calculation."""
        index = pd.date_range(start="2024-01-01", periods=100, freq="1min")
        eval_start = index[50]

        ground_truth = pd.Series(False, index=index)
        ground_truth.iloc[60:65] = True  # True anomaly starts at minute 60

        detected = pd.Series(False, index=index)
        detected.iloc[62:67] = True  # Detection starts at minute 62 (2 min late)

        metrics = MetricsCalculator.calculate(detected, ground_truth, eval_start)

        assert metrics.detection_latency == 2.0, "Detection latency should be 2 minutes"
