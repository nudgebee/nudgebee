from server.anomaly.anomaly_algo.abstract import (
    AnomalyAlgoAbstract,
    AnomalyAlgoAbstractConfig,
    AnomalyResponse,
    get_metric_spec,
)

from typing import Dict, Any, Tuple
import os
import pandas as pd
from sklearn.cluster import DBSCAN
from sklearn.preprocessing import StandardScaler
from sklearn.neighbors import NearestNeighbors
from datetime import datetime

import numpy as np
import logging

logger = logging.getLogger(__name__)


class DBSCANConfig(AnomalyAlgoAbstractConfig):
    eps: float = 0.5
    min_samples: int = 3
    minimum_score_strength: float = 0.0  # used similar to IsolationForest logic
    training_start_time: datetime | None = None
    training_end_time: datetime | None = None
    use_temporal_features: bool = False  # Enable temporal feature engineering
    response_context_hours: float = float(
        os.getenv("RESPONSE_CONTEXT_HOURS", "6.0")
    )  # Hours of context before evaluation period; 0 = include all data


class DBScan(AnomalyAlgoAbstract):
    """
    DBSCAN (Density-Based Spatial Clustering) algorithm for anomaly detection.
    Uses density-based clustering to identify outliers as points in low-density regions.
    Outliers are points that don't belong to any cluster (label = -1).
    """

    def __init__(self, config, log_context: Dict[str, Any] | None = None):
        super().__init__(config=config, log_context=log_context)
        self.config_class = DBSCANConfig
        self.model = None
        self.scaler = None  # Store scaler for reuse

    def split_test_training_data(self, data: pd.Series, config: DBSCANConfig) -> Tuple[pd.Series, pd.Series | None]:
        """Split into training/detection datasets (delegates to the shared base method).

        Training data is used to fit DBSCAN, detection data is evaluated for anomalies.
        """
        return self.split_train_eval(data, config)

    def get_default_parameters(
        self,
        config: DBSCANConfig,
        data: pd.Series,
    ) -> None:
        """Get default parameters and configuration for anomaly detection."""
        super().get_default_parameters(data=data, config=config)

        # Apply metric-specific DBSCAN params if not explicitly set
        spec = get_metric_spec(config.anomaly_type)
        if abs(config.eps - 0.5) < 1e-9:  # Default value, apply metric-specific
            config.eps = spec.dbscan_eps if spec else 0.5
        if config.min_samples == 3:  # Default value (DBSCANConfig.min_samples = 3), apply metric-specific
            config.min_samples = spec.dbscan_min_samples if spec else 3
        # training_end_time/training_start_time are defaulted by the base get_default_parameters.

    def _create_no_anomaly_response(self, data: pd.Series, config: DBSCANConfig) -> AnomalyResponse:
        """Create response with no anomalies for edge cases."""
        no_anomaly = pd.Series(False, index=data.index)
        zero_scores = pd.Series(0.0, index=data.index)
        merged_df = self.create_response_dataframe(data, no_anomaly, zero_scores)

        # Convert CPU data back to original scale for response
        if config.anomaly_type.lower() == "cpu" and "data" in merged_df.columns:
            merged_df["data"] = (merged_df["data"] / 1000).round(6)

        return AnomalyResponse(
            df=merged_df,
            start_time=config.start_time,
            end_time=config.end_time,
            anomaly_type=config.anomaly_type,
            account=str(config.account_id),
            namespace=config.namespace,
            deployment=config.deployment,
            stats=self.generate_stats(data),
            trigger_threshold_max=None,
            training_end_time=config.training_end_time,
            scores_threshold=0,
            evaluation_period=int(config.evaluation_period.seconds / 60) if config.evaluation_period else None,
            has_anomaly=False,
            query=None,
        )

    def _compute_optimal_eps(self, data_scaled: np.ndarray, min_samples: int) -> float:
        """
        Compute optimal eps using k-distance graph elbow method.
        The elbow point in the sorted k-distances indicates the optimal eps value.
        """
        k = min(min_samples, len(data_scaled) - 1)
        if k < 1:
            return 0.5  # Default fallback for very small datasets

        # Compute k-nearest neighbor distances
        nbrs = NearestNeighbors(n_neighbors=k).fit(data_scaled)
        distances, _ = nbrs.kneighbors(data_scaled)
        k_distances = np.sort(distances[:, -1])

        if len(k_distances) < 3:
            return 0.5  # Default fallback

        # 90th-percentile of k-distances is a robust, well-known eps heuristic.
        # It captures the "elbow" region without being sensitive to curve noise,
        # unlike second-derivative methods which often misidentify the knee point.
        optimal_eps = float(np.percentile(k_distances, 90))

        # Clamp to reasonable range [0.1, 2.0]
        optimal_eps = max(0.1, min(2.0, float(optimal_eps)))

        logger.info(
            "Computed optimal eps for DBSCAN",
            extra={**self.log_context, "optimal_eps": optimal_eps, "k": k, "data_points": len(data_scaled)},
        )

        return optimal_eps

    def _add_temporal_features(self, data: pd.Series) -> np.ndarray:
        """
        Add temporal features for better anomaly detection.
        Features include rate of change, deviation from mean, and volatility.
        """
        features = pd.DataFrame(index=data.index)

        # Raw value
        features["value"] = data.values

        # Rate of change
        features["rate_of_change"] = data.diff().fillna(0)

        # Deviation from rolling mean
        rolling_mean = data.rolling(window=10, min_periods=1).mean()
        features["deviation_from_mean"] = data - rolling_mean

        # Rolling standard deviation (volatility)
        features["volatility"] = data.rolling(window=10, min_periods=1).std().fillna(0)

        return features.values

    def _apply_directional_filter(
        self,
        data: pd.Series,
        training_data: pd.Series,
        outliers: pd.Series,
        outlier_scores: pd.Series,
        anomaly_type: str,
    ) -> Tuple[pd.Series, pd.Series]:
        """
        Apply directional filtering to outliers based on metric type.

        DBSCAN identifies points in low-density regions, but for most metrics
        we only care about HIGH values (spikes), not low values.

        For example:
        - Memory: We care about memory SPIKES (high usage), not dips
        - CPU: We care about CPU SPIKES, not idle periods
        - Error rate: We care about INCREASED errors, not zero errors
        - Latency: We care about SLOW responses (high latency), not fast ones

        This filter removes outliers that are BELOW the training baseline,
        keeping only those that represent genuine spikes/increases.
        """
        anomaly_type_lower = anomaly_type.lower()

        # Metrics where we only care about HIGH values (spikes up)
        spike_up_metrics = ["memory", "cpu", "errorrate", "latency"]

        if anomaly_type_lower not in spike_up_metrics:
            # For other metrics (like replicas), keep all outliers
            return outliers, outlier_scores

        # Calculate baseline from training data.
        # Use p50 (median) as the directional threshold — anything above the median
        # can be a high-direction spike. p75 was too aggressive: if training had
        # occasional high bursts, p75 is already elevated and moderate spikes
        # (e.g. 100 MB → 140 MB) were silently filtered out.
        baseline_median = training_data.median()
        baseline_p75 = training_data.quantile(0.75)  # retained for logging only

        # For spikes, we want values that are:
        # 1. Identified as outliers by DBSCAN (low density)
        # 2. AND are ABOVE the median (high values, not low-value noise)
        above_baseline = data > baseline_median

        # Count outliers before filtering
        outliers_before = outliers.sum()

        # Apply directional filter - keep only HIGH outliers
        filtered_outliers = outliers & above_baseline

        # Zero out scores for filtered-out outliers
        filtered_scores = outlier_scores.copy()
        filtered_scores[outliers & ~above_baseline] = 0

        outliers_after = filtered_outliers.sum()

        if outliers_before > outliers_after:
            logger.info(
                "Directional filter applied - removed low-value outliers",
                extra={
                    **self.log_context,
                    "directional_filter": {
                        "metric_type": anomaly_type,
                        "baseline_median": float(baseline_median),
                        "baseline_p75": float(baseline_p75),
                        "outliers_before": int(outliers_before),
                        "outliers_after": int(outliers_after),
                        "outliers_removed": int(outliers_before - outliers_after),
                    },
                },
            )

        return filtered_outliers, filtered_scores

    def _suppress_periodic_patterns(
        self,
        data: pd.Series,
        training_data: pd.Series,
        outliers: pd.Series,
    ) -> pd.Series:
        """
        Suppress anomaly flags for recurring hour-of-day patterns.

        If a metric consistently spikes at a certain hour (e.g. daily 5am batch job),
        that spike is an expected periodic event, not an anomaly. This method builds
        per-hour p95 baselines from training data and suppresses any flagged point
        whose value falls within 120% of the historical p95 for that hour.

        Requires at least MIN_OCCURRENCES data points at a given hour in training
        to establish a reliable per-hour baseline; hours with insufficient data
        are left as-is (not suppressed).
        """
        MIN_OCCURRENCES = 3  # need data from ≥3 occurrences at this hour
        TOLERANCE = 1.20  # allow up to 20% above historical p95 before flagging

        if not outliers.any():
            return outliers

        suppressed = outliers.copy()

        # Build hour-of-day p95 baseline from training data
        hourly_p95: dict[int, float] = {}
        for hour, group in training_data.groupby(training_data.index.hour):
            if len(group) >= MIN_OCCURRENCES:
                hourly_p95[int(hour)] = float(group.quantile(0.95))

        if not hourly_p95:
            return outliers  # not enough training data for hourly baseline

        suppressed_count = 0
        for ts in outliers[outliers].index:
            hour = ts.hour
            if hour not in hourly_p95:
                continue  # no reliable baseline for this hour — keep as anomaly
            expected_ceiling = hourly_p95[hour] * TOLERANCE
            if data.loc[ts] <= expected_ceiling:
                suppressed.loc[ts] = False
                suppressed_count += 1

        if suppressed_count > 0:
            logger.info(
                "Periodic pattern suppression applied",
                extra={
                    **self.log_context,
                    "periodic_suppression": {
                        "suppressed_count": suppressed_count,
                        "remaining_outliers": int(suppressed.sum()),
                        "hourly_p95_baselines": {str(k): round(v, 4) for k, v in hourly_p95.items()},
                    },
                },
            )

        return suppressed

    def _fallback_to_zscore(self, data: pd.Series, config: DBSCANConfig) -> AnomalyResponse:
        """
        Fallback to z-score method when DBSCAN cannot find core points.
        This ensures we still provide meaningful anomaly detection.
        """
        logger.info(
            "Using z-score fallback for anomaly detection",
            extra={**self.log_context, "reason": "no_core_points"},
        )

        # Split for z-score calculation
        training_data, _ = self.split_test_training_data(data, config)

        # Handle edge case where training_data is None or empty
        if training_data is None or training_data.empty:
            training_data = data

        # Calculate z-scores using training statistics
        mean = training_data.mean()
        std = training_data.std()

        if std == 0 or np.isnan(std):
            return self._create_no_anomaly_response(data, config)

        z_scores = ((data - mean) / std).abs()

        # Default threshold of 3 sigma
        sigma_threshold = 3.0
        anomalies = z_scores > sigma_threshold

        # Normalize z-scores to [0, 1] for consistency with DBSCAN scores
        max_zscore = z_scores.max()
        normalized_scores = z_scores / max_zscore if max_zscore > 0 else z_scores

        # Apply metric-specific filtering
        significant_change_filter = self.metric_specific_spike(data=data, anomalies=anomalies)
        anomalies = anomalies & significant_change_filter

        stats = self.generate_stats(data)

        merged_df, has_anomaly = self.apply_evaluation_period_filter(
            combined_anomalies=anomalies,
            combined_data=data,
            combined_anomaly_scores=normalized_scores,
            evaluation_period=config.evaluation_period,
            training_end_time=config.training_end_time,
        )
        merged_df = self.update_anomaly_to_peak_value(merged_df)

        logger.info(
            "Z-score fallback completed",
            extra={
                **self.log_context,
                "zscore_summary": {
                    "mean": float(mean),
                    "std": float(std),
                    "threshold": sigma_threshold,
                    "anomalies_found": int(anomalies.sum()),
                },
            },
        )

        # Convert CPU data back to original scale for response
        if config.anomaly_type.lower() == "cpu" and "data" in merged_df.columns:
            merged_df["data"] = (merged_df["data"] / 1000).round(6)
            data = data / 1000

        # Generate insights from merged_df (post peak-value adjustment) to avoid
        # duplicate insights for adjacent non-peak anomaly points.
        if config.training_end_time is not None:
            eval_mask = merged_df.index >= pd.to_datetime(config.training_end_time)
            fallback_insights_df = merged_df[eval_mask]
        else:
            fallback_insights_df = merged_df
        fallback_insights = self.generate_insights(
            fallback_insights_df["data"],
            fallback_insights_df["anomaly"],
            fallback_insights_df["anomaly_score"],
            stats,
            config,
        )

        return AnomalyResponse(
            df=merged_df,
            start_time=config.start_time,
            end_time=config.end_time,
            anomaly_type=config.anomaly_type,
            account=str(config.account_id),
            namespace=config.namespace,
            deployment=config.deployment,
            stats=stats,
            trigger_threshold_max=None,
            scores_threshold=sigma_threshold,
            evaluation_period=int(config.evaluation_period.seconds / 60) if config.evaluation_period else None,
            has_anomaly=has_anomaly,
            query=None,
            training_end_time=config.training_end_time,
            insights=fallback_insights,
        )

    def apply_evaluation_period_filter(
        self,
        combined_data: pd.Series,
        combined_anomalies: pd.Series,
        combined_anomaly_scores: pd.Series,
        evaluation_period: pd.Timedelta | None,
        training_end_time: pd.Timestamp,
    ) -> Tuple[pd.DataFrame, bool]:
        """Restrict to the eval window plus response_context_hours of context, zeroing
        anomalies when none fall in the eval window (delegates to the shared base helper)."""
        _, detection_data = self.split_test_training_data(data=combined_data, config=self.config)
        return self.restrict_to_evaluation_window(
            combined_data,
            combined_anomalies,
            combined_anomaly_scores,
            evaluation_period,
            detection_data,
            training_end_time,
            context_hours=getattr(self.config, "response_context_hours", 6.0),
            zero_when_no_eval_anomaly=True,
        )

    def get_anomaly(self, data: pd.Series, config: DBSCANConfig) -> AnomalyResponse:
        """
        Apply DBSCAN to detect anomalies in time series data.

        The algorithm:
        1. Splits data into training and evaluation periods
        2. Fits DBSCAN on training data only (prevents data leakage)
        3. Computes distances to nearest core point for all data
        4. Points far from core points are flagged as anomalies

        Returns outlier scores (distance to nearest core point) normalized to [0, 1].
        """
        # Upcast to DBSCANConfig when called with the abstract base config.
        # get_default_parameters() will then fill eps/min_samples from the metric's MetricSpec.
        if not isinstance(config, DBSCANConfig):
            config = DBSCANConfig(**config.model_dump())

        # Data quality validation
        if data.empty:
            raise ValueError("Input data is empty. Please provide a valid time series data.")

        # Clean data: handle infinity and NaNs
        data = data.replace([np.inf, -np.inf], np.nan).fillna(0)

        # Check for constant values (StandardScaler will fail)
        if data.nunique() <= 1:
            logger.warning(
                "Constant data detected, returning no anomalies",
                extra={**self.log_context, "data_std": 0, "data_points": len(data)},
            )
            return self._create_no_anomaly_response(data, config)

        # Check minimum data points for DBSCAN
        min_required = config.min_samples * 2
        if len(data) < min_required:
            logger.warning(
                "Insufficient data points for DBSCAN",
                extra={**self.log_context, "data_points": len(data), "min_required": min_required},
            )
            return self._create_no_anomaly_response(data, config)

        # Split data into training and evaluation
        training_data, _ = self.split_test_training_data(data, config)

        # Handle case where training_data is empty or None
        if training_data is None or training_data.empty:
            logger.warning(
                "No training data available, using all data for training",
                extra={**self.log_context, "data_points": len(data)},
            )
            training_data = data

        # Check training data has enough points
        if len(training_data) < config.min_samples:
            logger.warning(
                "Insufficient training data points, using all data",
                extra={**self.log_context, "training_points": len(training_data), "min_samples": config.min_samples},
            )
            training_data = data

        # Prepare features based on configuration
        if config.use_temporal_features:
            training_features = self._add_temporal_features(training_data)
            all_features = self._add_temporal_features(data)
        else:
            training_features = training_data.to_numpy().reshape(-1, 1).astype(float)
            all_features = data.to_numpy().reshape(-1, 1).astype(float)

        # Scale training data and fit scaler
        scaler = StandardScaler()
        training_scaled = scaler.fit_transform(training_features)
        self.scaler = scaler  # Store for potential reuse

        # Compute optimal eps if using default value
        original_eps = config.eps
        if abs(config.eps - 0.5) < 1e-9:  # Default value - try to optimize
            config.eps = self._compute_optimal_eps(training_scaled, config.min_samples)

        # Run DBSCAN on training data only
        db = DBSCAN(eps=config.eps, min_samples=config.min_samples)
        db.fit(training_scaled)
        self.model = db  # Store model

        # Get core points from training
        core_indices = db.core_sample_indices_
        if len(core_indices) == 0:
            logger.warning(
                "DBSCAN found no core points, falling back to z-score method",
                extra={
                    **self.log_context,
                    "eps": config.eps,
                    "original_eps": original_eps,
                    "min_samples": config.min_samples,
                    "training_points": len(training_data),
                },
            )
            return self._fallback_to_zscore(data, config)

        core_points = training_scaled[core_indices]

        # Transform ALL data using the fitted scaler
        all_scaled = scaler.transform(all_features)

        # Compute distances to nearest core point for all points
        nbrs = NearestNeighbors(n_neighbors=1).fit(core_points)
        distances, _ = nbrs.kneighbors(all_scaled)
        distances = distances.flatten()

        # Predict labels for all data points
        # Points are outliers if their distance to nearest core point > eps
        labels = np.where(distances > config.eps, -1, 0)

        # Only score outliers (label == -1), non-outliers get 0
        outlier_distances = np.where(labels == -1, distances, 0)

        # Normalize distances to [0, 1] range for consistent interpretation
        max_distance = outlier_distances.max()
        if max_distance > 0:
            normalized_distances = outlier_distances / max_distance
        else:
            normalized_distances = outlier_distances

        # Create outlier scores series
        outlier_scores = pd.Series(normalized_distances, index=data.index)
        outlier_scores = outlier_scores.round(4)

        # Generate statistics from full data so response stats.max reflects eval-period spikes.
        # training_count is preserved separately so baseline insight calculations remain accurate.
        stats = self.generate_stats(data)
        stats["training_count"] = len(training_data)

        # Determine outliers based on non-zero scores
        outliers = outlier_scores > 0

        # Apply directional filtering - for most metrics we only care about HIGH values (spikes)
        # DBSCAN identifies low-density regions, but low values in low-density are not anomalies
        # we care about - only high values (spikes) matter
        outliers, outlier_scores = self._apply_directional_filter(
            data=data,
            training_data=training_data,
            outliers=outliers,
            outlier_scores=outlier_scores,
            anomaly_type=config.anomaly_type,
        )

        # Suppress recurring periodic patterns (e.g. daily 5am batch spike).
        # Must run after directional filter but before metric_specific_spike.
        outliers = self._suppress_periodic_patterns(data, training_data, outliers)
        # Zero out scores for any points suppressed by periodic filter
        outlier_scores = outlier_scores.where(outliers, other=0.0)

        # Log detection summary
        logger.info(
            "DBSCAN anomaly detection completed",
            extra={
                **self.log_context,
                "anomaly_detection_summary": {
                    "eps_used": config.eps,
                    "min_samples": config.min_samples,
                    "training_datapoints": len(training_data),
                    "total_datapoints": len(data),
                    "core_points_found": len(core_indices),
                    "raw_outliers": int(outliers.sum()),
                    "used_temporal_features": config.use_temporal_features,
                },
            },
        )

        # Apply metric-specific filtering
        significant_change_filter = self.metric_specific_spike(data=data, anomalies=outliers)
        outliers = outliers & significant_change_filter

        # Apply evaluation period filter
        merged_df, has_anomaly = self.apply_evaluation_period_filter(
            combined_anomalies=outliers,
            combined_data=data,
            combined_anomaly_scores=outlier_scores,
            evaluation_period=config.evaluation_period,
            training_end_time=config.training_end_time,
        )
        merged_df = self.update_anomaly_to_peak_value(merged_df)

        # Convert CPU data back to original scale for response
        # (was multiplied by 1000 for internal processing precision)
        if config.anomaly_type.lower() == "cpu" and "data" in merged_df.columns:
            merged_df["data"] = (merged_df["data"] / 1000).round(6)
            # Also scale data for insights generation
            data = data / 1000

        # Generate insights from merged_df (post peak-value adjustment) so that
        # insights match exactly what is shown in the chart. Using raw outliers
        # would include adjacent non-peak anomaly points that were cleared by
        # update_anomaly_to_peak_value, causing duplicate-looking insight messages.
        if config.training_end_time is not None:
            eval_mask = merged_df.index >= pd.to_datetime(config.training_end_time)
            insights_df = merged_df[eval_mask]
        else:
            insights_df = merged_df
        insights = self.generate_insights(
            insights_df["data"], insights_df["anomaly"], insights_df["anomaly_score"], stats, config
        )

        return AnomalyResponse(
            df=merged_df,
            start_time=config.start_time,
            end_time=config.end_time,
            anomaly_type=config.anomaly_type,
            account=str(config.account_id),
            namespace=config.namespace,
            deployment=config.deployment,
            stats=stats,
            trigger_threshold_max=None,
            scores_threshold=0,
            evaluation_period=int(config.evaluation_period.seconds / 60) if config.evaluation_period else None,
            has_anomaly=has_anomaly,
            query=None,
            training_end_time=config.training_end_time,
            insights=insights,
        )
