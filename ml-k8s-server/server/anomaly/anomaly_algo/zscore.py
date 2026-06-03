"""Z-Score based anomaly detection algorithm."""

import pandas as pd
import numpy as np
import logging
from datetime import datetime
from typing import Dict, Any, Tuple, cast

from server.anomaly.anomaly_algo.abstract import (
    AnomalyAlgoAbstract,
    AnomalyAlgoAbstractConfig,
    AnomalyResponse,
)

logger = logging.getLogger(__name__)


class ZScoreConfig(AnomalyAlgoAbstractConfig):
    """Configuration class for Z-Score algorithm."""

    sigma_threshold: float = 3.0  # Standard deviations (3.0 = 99.7% confidence)
    window_minutes: int = 60  # Window for baseline calculation
    minimum_points: int = 10  # Minimum data points needed for baseline
    # Train/detection split boundary, shared with the other algos (set in get_default_parameters).
    training_start_time: datetime | None = None
    training_end_time: datetime | None = None


class ZScore(AnomalyAlgoAbstract):
    """Z-Score based anomaly detection algorithm.

    This algorithm detects anomalies by computing the z-score of each point
    relative to the mean and standard deviation of the historical baseline:

    z_score = (value - mean) / stddev
    anomaly = TRUE if |z_score| > sigma_threshold

    Advantages:
    - Highly interpretable (z-score tells you deviation magnitude)
    - Few parameters (just sigma_threshold)
    - Perfect for latency/P99 detection
    - Statistically grounded
    """

    def __init__(self, config, log_context: Dict[str, Any] | None = None):
        super().__init__(config=config, log_context=log_context)
        self.config_class = ZScoreConfig
        self.baseline_mean: float = 0.0
        self.baseline_stddev: float = 1.0

    def get_default_parameters(
        self,
        config: AnomalyAlgoAbstractConfig,
        data: pd.Series,
    ) -> None:
        """Get default parameters and configuration for anomaly detection."""
        config = cast(ZScoreConfig, config)
        super().get_default_parameters(data=data, config=config)
        logger.debug(
            "ZScore: Using default parameters",
            extra={
                **self.log_context,
                "sigma_threshold": config.sigma_threshold,
                "window_minutes": config.window_minutes,
                "minimum_points": config.minimum_points,
            },
        )

    def calculate_baseline(self, training_data: pd.Series) -> Tuple[float, float]:
        """Calculate mean and standard deviation from training data.

        Returns:
            Tuple of (mean, stddev). If stddev is 0, returns (mean, 1.0) to avoid division by zero.
        """
        if training_data is None or training_data.empty:
            logger.warning(
                "ZScore: No training data for baseline calculation",
                extra=self.log_context,
            )
            return 0.0, 1.0

        mean = float(training_data.mean())
        stddev = float(training_data.std())

        # Handle case where all values are the same (stddev = 0)
        if stddev == 0 or np.isnan(stddev):
            logger.debug(
                "ZScore: Zero or NaN standard deviation, using fallback",
                extra={
                    **self.log_context,
                    "mean": mean,
                    "stddev": stddev,
                    "fallback_stddev": 1.0,
                },
            )
            stddev = 1.0

        logger.info(
            "ZScore: Baseline calculated",
            extra={
                **self.log_context,
                "baseline_mean": mean,
                "baseline_stddev": stddev,
                "data_points": len(training_data),
                "min_value": float(training_data.min()),
                "max_value": float(training_data.max()),
            },
        )

        return mean, stddev

    def compute_z_scores(self, data: pd.Series, mean: float, stddev: float) -> np.ndarray:
        """Compute z-scores for all data points.

        z_score = (value - mean) / stddev
        """
        if data is None or data.empty:
            return np.array([])

        z_scores = (np.asarray(data.values) - mean) / stddev
        return cast(np.ndarray, z_scores)

    def detect_anomalies(
        self, z_scores: np.ndarray, sigma_threshold: float, data_index
    ) -> Tuple[pd.Series, np.ndarray]:
        """Detect anomalies based on z-score threshold.

        An anomaly is flagged when |z_score| > sigma_threshold.
        """
        if len(z_scores) == 0:
            return pd.Series(False, index=data_index, dtype=bool), np.array([])

        anomalies = np.abs(z_scores) > sigma_threshold
        anomaly_series = pd.Series(anomalies, index=data_index, dtype=bool)

        return anomaly_series, z_scores

    def get_anomaly(self, data: pd.Series, config: AnomalyAlgoAbstractConfig) -> AnomalyResponse:
        """Detect anomalies using Z-Score method.

        Process:
        1. Split data into training and evaluation periods
        2. Calculate baseline (mean, stddev) from training data
        3. Compute z-scores for all data points
        4. Flag anomalies where |z_score| > sigma_threshold
        """
        # Upcast to ZScoreConfig when called with the abstract base config.
        if not isinstance(config, ZScoreConfig):
            config = ZScoreConfig(**config.model_dump())

        if data is None or data.empty:
            logger.warning(
                "ZScore: No data provided for anomaly detection",
                extra=self.log_context,
            )
            raise ValueError("No data found for anomaly detection")

        # Split into training and detection using the shared boundary (training_end_time),
        # so the baseline excludes the evaluation window like the other algos.
        training_data, evaluation_data = self.split_train_eval(data, config)
        end_time = config.end_time if config.end_time is not None else pd.Timestamp.now()

        # Validate minimum points
        if len(training_data) < config.minimum_points:
            logger.warning(
                "ZScore: Insufficient training data",
                extra={
                    **self.log_context,
                    "required": config.minimum_points,
                    "available": len(training_data),
                },
            )
            raise ValueError(f"Insufficient training data: need {config.minimum_points}, got {len(training_data)}")

        # Calculate baseline from training data
        mean, stddev = self.calculate_baseline(training_data)
        self.baseline_mean = mean
        self.baseline_stddev = stddev

        # Compute z-scores for all data
        z_scores = self.compute_z_scores(data, mean, stddev)

        # Detect anomalies
        anomalies, z_scores_array = self.detect_anomalies(z_scores, config.sigma_threshold, data.index)

        # Create response dataframe via the shared builder so the per-point value column is
        # named "data" — the field the api-server (entity.go) and frontend consume. (Was an
        # inline "value" column, which neither consumer can read.)
        response_df = self.create_response_dataframe(
            pd.Series(data.values, index=data.index),
            pd.Series(anomalies.values, index=data.index),
            pd.Series(z_scores, index=data.index),
        )

        # Log anomaly detection summary
        anomaly_count = int(anomalies.sum())
        logger.info(
            "ZScore: Anomaly detection completed",
            extra={
                **self.log_context,
                "total_datapoints": len(data),
                "training_datapoints": len(training_data),
                "evaluation_datapoints": len(evaluation_data),
                "anomalies_detected": anomaly_count,
                "sigma_threshold": config.sigma_threshold,
                "baseline_mean": mean,
                "baseline_stddev": stddev,
                "max_z_score": float(np.abs(z_scores).max()) if len(z_scores) > 0 else 0,
            },
        )

        # Generate statistics
        stats = {
            "baseline_mean": mean,
            "baseline_stddev": stddev,
            "sigma_threshold": config.sigma_threshold,
            "anomalies_count": anomaly_count,
            "mean": mean,  # Add mean for generate_insights() fallback
        }

        # Ensure start_time and end_time are not None
        start_time = config.start_time if config.start_time is not None else pd.Timestamp.now()
        end_time_final = end_time if end_time is not None else pd.Timestamp.now()
        account_id = str(config.account_id) if config.account_id is not None else "unknown"

        # Convert CPU data back to original scale (cores) for the response and insights.
        # load_and_prepare_data multiplied CPU by 1000 (millicores) for processing; mirror the
        # ÷1000 the other algos apply so ZScore shares the same CPU unit contract.
        insight_data = data
        if config.anomaly_type.lower() == "cpu":
            if "data" in response_df.columns:
                response_df["data"] = (response_df["data"] / 1000).round(6)
            insight_data = data / 1000

        # Generate insights for all anomalous points
        # Convert z_scores array to Series for compatibility
        z_scores_series = pd.Series(z_scores, index=data.index)
        insights = self.generate_insights(insight_data, anomalies, z_scores_series, stats, config)

        return AnomalyResponse(
            response_df,
            start_time,
            end_time_final,
            config.anomaly_type,
            account_id,
            config.namespace,
            config.deployment,
            stats=stats,
            trigger_threshold_max=None,
            scores_threshold=config.sigma_threshold,
            training_end_time=config.training_end_time,
            insights=insights,
        )
