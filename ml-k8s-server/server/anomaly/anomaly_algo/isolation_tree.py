from server.anomaly.anomaly_algo.abstract import (
    AnomalyAlgoAbstract,
    AnomalyAlgoAbstractConfig,
    AnomalyResponse,
)
import pandas as pd

from typing import Dict, Any, Tuple
from sklearn.ensemble import IsolationForest
from sklearn.preprocessing import StandardScaler
from datetime import datetime

import numpy as np
import logging

logger = logging.getLogger(__name__)


class IsolationTreeConfig(AnomalyAlgoAbstractConfig):
    """
    Configuration class for Isolation Tree algorithm.
    """

    # Add specific configuration parameters if needed
    contamination: float = 0.01
    estimators: int = 100  # Reduced from 400 to 100 to save memory
    iqr_multiplier: float = 1.5
    minimum_score_strength: float = 0.1
    threshold: float | None = None
    random_state: int | None = None
    training_start_time: datetime | None = None
    training_end_time: datetime | None = None


class IsolationTree(AnomalyAlgoAbstract):
    """
    Isolation Tree algorithm for anomaly detection.
    """

    def __init__(self, config, log_context: Dict[str, Any] | None = None):
        super().__init__(config=config, log_context=log_context)
        self.config_class = IsolationTreeConfig
        self.model = None
        self.random_state = 42

    def evaluate(self, data: pd.Series) -> pd.Series:
        """
        Evaluate the model on the provided data.
        """
        # Implement evaluation logic here
        pass

    def run_detection_analysis(
        self,
        detection_data: pd.Series,
        model: IsolationForest | None,
        scaler: StandardScaler | None,
        scores_threshold: float | None,
        config: IsolationTreeConfig,
    ) -> Tuple[pd.Series, np.ndarray]:
        """Run anomaly detection on the detection window using trained model."""
        if (
            detection_data is None
            or detection_data.empty
            or scores_threshold is None
            or model is None
            or scaler is None
        ):
            return pd.Series([], dtype=bool), np.array([])

        detection_reshaped = detection_data.to_numpy().reshape(-1, 1)
        detection_scaled = scaler.transform(detection_reshaped)
        anomaly_scores_detected = model.decision_function(detection_scaled)

        score_mean = anomaly_scores_detected.mean()
        anomalies_detected = pd.Series(
            (anomaly_scores_detected < scores_threshold)
            & (anomaly_scores_detected < (score_mean - config.minimum_score_strength)),
            index=detection_data.index,
        )
        return anomalies_detected, anomaly_scores_detected

    def combine_scores(
        self,
        anomaly_scores: np.ndarray,
        anomaly_scores_detected: np.ndarray,
        data: pd.Series,
        detection_data: pd.Series,
    ) -> pd.Series:
        """Combine historical and detection anomaly scores."""
        anomaly_scores_series = pd.Series(
            anomaly_scores.flatten(), index=data.index if data is not None and not data.empty else pd.Index([])
        )

        if detection_data is None or detection_data.empty:
            return anomaly_scores_series

        scores_flat = anomaly_scores_detected.flatten()
        if len(scores_flat) == len(detection_data):
            anomaly_scores_detected_series = pd.Series(scores_flat, index=detection_data.index)
            return self.safe_concat_series([anomaly_scores_series, anomaly_scores_detected_series])
        else:
            logger.warning(
                "Anomaly scores and data length mismatch",
                extra={
                    "scores_length": len(scores_flat),
                    "data_length": len(detection_data),
                    "action": "using_placeholder_score",
                },
            )
            placeholder_score = anomaly_scores_series.mean() if not anomaly_scores_series.empty else 0.0
            anomaly_scores_detected_series = pd.Series(placeholder_score, index=detection_data.index)
            return self.safe_concat_series([anomaly_scores_series, anomaly_scores_detected_series])

    def split_test_training_data(
        self, data: pd.Series, config: IsolationTreeConfig
    ) -> Tuple[pd.Series, pd.Series | None]:
        """Split into training/detection datasets (delegates to the shared base method)."""
        return self.split_train_eval(data, config)

    def get_anomaly(self, data: pd.Series, config: IsolationTreeConfig) -> AnomalyResponse:
        """
        Get anomalies from the provided data.
        """
        # Upcast to IsolationTreeConfig when called with the abstract base config so the
        # IsolationForest-specific fields (contamination, iqr_multiplier, ...) are available.
        if not isinstance(config, IsolationTreeConfig):
            config = IsolationTreeConfig(**config.model_dump())

        data, detection_data = self.split_test_training_data(data, config)
        if data is None and detection_data is None:
            logger.debug(
                "No data found for anomaly detection",
                extra={**self.log_context, "reason": "both_training_and_evaluation_data_empty"},
            )
            raise ValueError("No data found for anomaly detection")
        anomalies, anomalies_detected, anomaly_scores, anomaly_scores_detected, scores_threshold = (
            self.execute_anomaly_detection(data=data, detection_data=detection_data, config=config)
        )

        # Combine results
        combined_data = self.safe_concat_series([data, detection_data])
        combined_anomalies = self.safe_concat_series([anomalies, anomalies_detected])
        combined_anomaly_scores = self.combine_scores(anomaly_scores, anomaly_scores_detected, data, detection_data)

        logger.info(
            "Anomaly detection completed",
            extra={
                **self.log_context,
                "anomaly_detection_summary": {
                    "training_anomalies": int(anomalies.sum()) if anomalies is not None and not anomalies.empty else 0,
                    "detection_anomalies": (
                        int(anomalies_detected.sum())
                        if anomalies_detected is not None and not anomalies_detected.empty
                        else 0
                    ),
                    "combined_anomalies": int(combined_anomalies.sum()) if not combined_anomalies.empty else 0,
                    "training_datapoints": len(data) if data is not None and not data.empty else 0,
                    "detection_datapoints": (
                        len(detection_data) if detection_data is not None and not detection_data.empty else 0
                    ),
                    "combined_datapoints": len(combined_data) if not combined_data.empty else 0,
                },
            },
        )

        # Apply evaluation period filtering and create response
        merged_df = self.apply_evaluation_period_filter(
            combined_data,
            combined_anomalies,
            combined_anomaly_scores,
            config.evaluation_period,
            detection_data,
            config.training_end_time,
        )
        # Generate statistics and log completion
        stats = self.generate_stats(data)
        self.log_detection_completion(merged_df, self.log_context, config.evaluation_period, scores_threshold, stats)

        # Convert CPU data back to original scale for response
        # (was multiplied by 1000 for internal processing precision)
        if config.anomaly_type.lower() == "cpu" and "data" in merged_df.columns:
            merged_df["data"] = (merged_df["data"] / 1000).round(6)
            # Also scale combined_data for insights generation
            combined_data = combined_data / 1000

        # Generate insights only when an evaluation window is present
        if detection_data is not None and not detection_data.empty:
            insights = self.generate_insights(
                combined_data[detection_data.index],
                combined_anomalies[detection_data.index],
                combined_anomaly_scores[detection_data.index],
                stats,
                config,
            )
        else:
            insights = []

        return AnomalyResponse(
            merged_df,
            config.start_time,
            config.end_time,
            config.anomaly_type,
            config.account_id,
            config.namespace,
            config.deployment,
            stats,
            trigger_threshold_max=None,  # set by abstract apply_default_threshold_filter
            scores_threshold=scores_threshold,
            training_end_time=config.training_end_time,
            insights=insights,
        )

    def log_detection_completion(
        self,
        merged_df: pd.DataFrame,
        context: dict,
        evaluation_period: pd.Timedelta | None,
        scores_threshold: float | None,
        stats: dict,
    ) -> None:
        """Log completion summary for anomaly detection."""
        total_anomalies = merged_df["anomaly"].sum() if not merged_df.empty else 0
        total_datapoints = len(merged_df) if not merged_df.empty else 0

        logger.info(
            "Anomaly detection completed",
            extra={
                **context,
                "detection_results": {
                    "total_anomalies": int(total_anomalies),
                    "total_datapoints": total_datapoints,
                    "anomaly_rate": round(total_anomalies / total_datapoints if total_datapoints > 0 else 0, 4),
                    "has_anomaly": bool(total_anomalies > 0),
                    "evaluation_period_only": evaluation_period is not None,
                    "scores_threshold": scores_threshold,
                    "stats": stats,
                },
            },
        )

    def apply_evaluation_period_filter(
        self,
        combined_data: pd.Series,
        combined_anomalies: pd.Series,
        combined_anomaly_scores: pd.Series,
        evaluation_period: pd.Timedelta | None,
        detection_data: pd.Series,
        training_end_time: pd.Timestamp,
    ) -> pd.DataFrame:
        """Return only the evaluation window (delegates to the shared base helper)."""
        df, _ = self.restrict_to_evaluation_window(
            combined_data,
            combined_anomalies,
            combined_anomaly_scores,
            evaluation_period,
            detection_data,
            training_end_time,
            context_hours=None,  # eval-window-only
            zero_when_no_eval_anomaly=False,
        )
        return df

    def execute_anomaly_detection(
        self,
        data: pd.Series,
        detection_data: pd.Series,
        config: IsolationTreeConfig,
    ) -> Tuple[pd.Series, pd.Series, np.ndarray, np.ndarray, float | None]:
        """Execute anomaly detection on training and evaluation data."""

        # Run on historical data
        anomalies, anomaly_scores, scores_threshold, model, scaler = self.detect_anomalies(
            data=data,
            config=config,
        )

        # Run detection analysis on detection window reusing the model and scaler
        anomalies_detected, anomaly_scores_detected = self.run_detection_analysis(
            detection_data=detection_data, model=model, scaler=scaler, scores_threshold=scores_threshold, config=config
        )

        return anomalies, anomalies_detected, anomaly_scores, anomaly_scores_detected, scores_threshold

    def detect_anomalies(
        self,
        data: pd.Series,
        config: IsolationTreeConfig,
    ) -> Tuple[pd.Series, np.ndarray, float | None, IsolationForest | None, StandardScaler | None]:
        if data is None or data.empty:
            return pd.Series(False, index=pd.Index([])), np.array([]), None, None, None

        # Check for constant data (all values the same)
        if data.nunique() <= 1:
            # For constant data, no anomalies should be detected
            # Special case for error rate and latency: 0 values are not anomalies
            if config.anomaly_type.lower() in ["errorrate", "latency"] and data.iloc[0] == 0:
                return pd.Series(False, index=data.index), np.zeros(len(data)), None, None, None
            # For other metrics with constant values, also no anomalies
            return pd.Series(False, index=data.index), np.zeros(len(data)), None, None, None

        data_reshaped = data.to_numpy().reshape(-1, 1)
        scaler = StandardScaler()
        data_scaled = scaler.fit_transform(data_reshaped)

        model = IsolationForest(
            contamination=config.contamination, n_estimators=config.estimators, random_state=self.random_state
        )
        model.fit(data_scaled)

        scores = model.decision_function(data_scaled)
        scores_series = pd.Series(scores.flatten(), index=data.index)

        if config.threshold is None:
            Q1 = scores_series.quantile(0.25)
            Q3 = scores_series.quantile(0.75)
            IQR = Q3 - Q1
            score_std = scores_series.std()
            score_mean = scores_series.mean()

            # For spike detection, we want to catch significantly low scores (true anomalies)
            # IsolationForest gives low (negative) scores to anomalies
            lower_threshold = Q1 - (config.iqr_multiplier * IQR)

            # Alternative threshold based on standard deviation
            std_threshold = score_mean - (2.0 * score_std)

            # Use the most restrictive meaningful threshold
            candidate_thresholds = [lower_threshold, std_threshold, scores_series.quantile(config.contamination)]

            # Filter out thresholds that are too close to zero or positive
            meaningful_thresholds = [t for t in candidate_thresholds if t < -config.minimum_score_strength]

            if meaningful_thresholds:
                # Use the most restrictive (lowest) meaningful threshold
                threshold = min(meaningful_thresholds)
            else:
                # Fallback: use a threshold based on the contamination rate but ensure it's meaningful
                fallback_threshold = min(score_mean - score_std, scores_series.quantile(config.contamination / 2))
                threshold = fallback_threshold if fallback_threshold < -config.minimum_score_strength else None

            context = self.log_context or {}
            logger.info(
                "Anomaly threshold calculation completed",
                extra={
                    **context,
                    "threshold_stats": {
                        "q1": round(Q1, 6),
                        "q3": round(Q3, 6),
                        "iqr": round(IQR, 6),
                        "std": round(score_std, 6),
                        "mean": round(score_mean, 6),
                        "lower_threshold": round(lower_threshold, 6),
                        "std_threshold": round(std_threshold, 6),
                        "contamination_threshold": round(scores_series.quantile(config.contamination), 6),
                        "final_threshold": threshold,
                    },
                },
            )
        if threshold is not None:
            # IsolationForest gives low (negative) scores to anomalies/spikes
            # Detect points with scores below threshold (true anomalies)
            score_mean = scores.mean()
            # For spike detection, we only want significantly low (negative) scores
            # IsolationForest assigns low scores to anomalies, so we focus on the left tail
            anomalies = pd.Series(
                (scores < threshold) & (scores < (score_mean - config.minimum_score_strength)), index=data.index
            )
            significant_change_filter = self.metric_specific_spike(data=data, anomalies=anomalies)

            anomalies = anomalies & significant_change_filter

            # Safety check: if no anomalies detected but we have obvious outliers, flag them
            if not anomalies.any():
                try:
                    # Use a more lenient approach: flag the most extreme outliers
                    extreme_threshold = np.quantile(scores, config.contamination * 2)  # More lenient threshold
                    anomalies = pd.Series(scores < extreme_threshold, index=data.index)

                    context = self.log_context or {}
                    logger.info(
                        "Anomaly safety check applied - using lenient threshold",
                        extra={
                            **context,
                            "threshold_fallback": {
                                "strict_threshold": round(threshold, 6),
                                "lenient_threshold": round(extreme_threshold, 6),
                                "anomalies_found": int(anomalies.sum()),
                            },
                        },
                    )
                except Exception as e:
                    context = self.log_context or {}
                    logger.warning("Anomaly safety check failed", extra={**context, "error": str(e)}, exc_info=True)
        else:
            # Last resort: if no threshold could be determined, use simple statistical outlier detection
            score_mean = scores.mean()
            score_std = scores.std()
            fallback_threshold = score_mean - (1.5 * score_std)
            anomalies = pd.Series(scores < fallback_threshold, index=data.index)

            # Apply the same metric-specific filtering as above
            anomalies = self.metric_specific_spike(data=data, anomalies=anomalies)
            threshold = fallback_threshold
            context = self.log_context or {}
            logger.info(
                "Using fallback statistical outlier detection",
                extra={
                    **context,
                    "fallback_detection": {
                        "threshold": round(fallback_threshold, 6),
                        "anomalies_found": int(anomalies.sum()),
                        "reason": "no_meaningful_threshold_determined",
                    },
                },
            )
        return anomalies, scores, threshold, model, scaler
