import datetime
import unittest
from unittest.mock import patch

import matplotlib.pyplot as plt
import pandas as pd

from server.anomaly.anomaly import detect_anomaly, AnomalyResponse


# python -munittest -v tests.TestAnomaly
class TestAnomaly(unittest.TestCase):

    def plot_anomalies(self, data, anomalies, title="Anomaly Detection"):
        """Plots time series data with detected anomalies highlighted."""

        plt.figure(figsize=(12, 6))
        plt.plot(data, label="Data")
        plt.scatter(data.index[anomalies], data[anomalies], color="red", label="Anomalies")
        plt.title(title)
        plt.xlabel("Time")
        plt.ylabel("Value")
        plt.legend()
        plt.grid(True)
        plt.show()

    def plot_scores(self, data, title="Anomaly Scores"):
        """Plots time series data with detected anomalies highlighted."""
        plt.figure(figsize=(12, 6))
        plt.plot(data, label="Data")
        plt.title(title)
        plt.xlabel("Time")
        plt.ylabel("Value")
        plt.legend()
        plt.grid(True)
        plt.show()

    def test_anomaly(self):
        start_time = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=1)
        end_time = datetime.datetime.now(datetime.timezone.utc)
        anomaly = detect_anomaly(
            "00000000-0000-0000-0000-000000000000",
            "example-namespace",
            "example-deployment",
            "memory",
            start_time=start_time,
            end_time=end_time,
            evaluation_period=pd.Timedelta(minutes=60),
        )
        self.assertIsNotNone(anomaly)
        print("total anomalies:", anomaly.df["anomaly"].sum(), "count", anomaly.df["anomaly"].count())
        # self.assertTrue(anomaly.df["anomaly"].any())
        self.plot_anomalies(anomaly.df["data"], anomaly.df["anomaly"], "detected anomaly")
        self.plot_scores(anomaly.df["anomaly_score"])

    @patch("server.anomaly.anomaly._load_and_prepare_data")
    def test_detect_anomaly_no_training_data(self, mock_load_prepare_data):
        # Configure the mock to simulate that training data (`data`) is None,
        # but detection_data has some minimal form. This ensures the function
        # proceeds past the initial "if data is None and detection_data is None: return None" check,
        # allowing us to test the stats calculation when `data` (training data) is None.
        mock_load_prepare_data.return_value = (
            None,
            pd.Series([1.0], index=[pd.Timestamp.utcnow()]),
            0.5,
        )  # data, detection_data, trigger_threshold_max

        response = detect_anomaly(
            account_id="test_account",
            namespace="test_namespace",
            deployment="test_deployment",
            anomaly_type="memory",
            start_time=pd.Timestamp.utcnow() - pd.Timedelta(days=1),
            end_time=pd.Timestamp.utcnow(),
            step="5m",
        )

        self.assertIsNotNone(response)
        self.assertIsInstance(response, AnomalyResponse)
        self.assertIsInstance(response.stats, dict)

        expected_stats = {
            "count": 0,
            "training_count": 0,
            "min": 0.0,
            "max": 0.0,
            "mean": 0.0,
            "p99": 0.0,
            "p999": 0.0,
        }
        self.assertEqual(response.stats, expected_stats)

        # Depending on how `merged_df` is constructed when `data` is None but `detection_data` is not:
        # `combined_data` will be `detection_data`. `anomalies` will be empty. `anomaly_scores` empty.
        # `data_df` will be from `detection_data`. `anomalies_df` from empty. `anomaly_scores_df` from empty.
        # `merged_df` will thus be based on `detection_data`.
        # `response.df.empty` might not be True if `detection_data` is present.
        # `response.has_anomaly` depends on `anomalies_detected`.

        # Check essentials for the "no training data" scenario
        self.assertFalse(response.has_anomaly)  # Expect no anomalies if training data was None
        # response.df might not be empty if detection_data was present
        # self.assertTrue(response.df.empty)

    # def test_anomaly_withsmooting(self):
    #     anomaly_df = detect_anomaly(
    #         "00000000-0000-0000-0000-000000000000",
    #         "example-namespace",
    #         "example-deployment",
    #         enabled_smoothing=True,
    #         anomaly_type="memeory",
    #     )
    #     self.assertIsNotNone(anomaly_df)
    #     plot_anomalies(anomaly_df["data"], anomaly_df["anomaly"], "detected anomaly")
