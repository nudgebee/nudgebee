from pydantic import BaseModel
from typing import cast, Dict, Any
import pandas as pd
import numpy as np
from uuid import UUID
import logging
from server.metrics.prometheus_metrics import fetch_metrics
from datetime import datetime, timedelta
from server.utils.utils import MetricsServerConfigs
import json
from server.exception import CancelPrediction

logger = logging.getLogger(__name__)


TEMPLATES: Dict[str, Dict[str, Any]] = {
    "memory": {
        # PromQL query format for fetching the metric
        "query_fmt": '(max(max_over_time(container_memory_working_set_bytes{{ __CLUSTER__ namespace="{namespace}", '
        'pod=~"{deployment}.*"}}[1m])) or vector(0))',
        # Time range type used in the query: 'step' or 'window'
        "time_key": "fixed",
        # Time window size for rolling metrics (if needed)
        "time_range": "1m",
        # Raw threshold (in bytes) — value above which we care to start evaluating anomalies
        "default_threshold": 50 * 1024 * 1024,  # 50 MB
        # Minimum required anomaly score dip to treat it as meaningful (avoids false positives)
        "minimum_score_strength": 0.15,  # Balanced for practical monitoring
        # Multiplier for IQR-based thresholding (controls sensitivity)
        "iqr_multiplier": 2.5,  # More sensitive to catch meaningful spikes
        # Expected contamination fraction (fraction of outliers) used by IsolationForest
        "contamination": 0.01,  # 1% outliers - catches significant anomalies
        # DBSCAN-specific parameters
        "dbscan_eps": 0.3,  # Tighter for memory (stable patterns)
        "dbscan_min_samples": 10,
    },
    "cpu": {
        "query_fmt": '(sum(rate(container_cpu_usage_seconds_total{{  __CLUSTER__  namespace="{namespace}"'
        ', pod=~"{deployment}.*", image!="", pod!=""}}[5m])) or vector(0))',
        "time_key": "fixed",
        "default_threshold": 0.5,  # 5% CPU threshold - only flag significant usage
        "minimum_score_strength": 0.12,  # Balanced threshold for meaningful anomalies
        "iqr_multiplier": 2.0,  # More sensitive to catch CPU spikes
        "contamination": 0.015,  # 1.5% outliers - practical for CPU monitoring
        # DBSCAN-specific parameters
        "dbscan_eps": 0.4,  # Medium for CPU
        "dbscan_min_samples": 8,
    },
    "errorrate": {
        "query_fmt": '((sum(rate(container_http_requests_total{{ __CLUSTER__ destination_workload_name="{deployment}",'
        'destination_workload_namespace="{namespace}", status=~"^[45][0-9][0-9]$"}}[2m])) or '
        'vector(0))) / ((sum(rate(container_http_requests_total{{  __CLUSTER__  destination_workload_name="{'
        'deployment}", destination_workload_namespace="{namespace}"}}[2m])) or vector(1)))',
        "time_key": "fixed",
        "time_range": "2m",
        "default_threshold": 0.005,  # 0.5% error rate
        "minimum_score_strength": 0.01,  # we care about even small increases
        "iqr_multiplier": 0.5,  # tighter box for sensitivity
        "contamination": 0.02,  # slightly higher outlier expectation
        # DBSCAN-specific parameters
        "dbscan_eps": 0.8,  # Looser for sparse error data
        "dbscan_min_samples": 3,
    },
    "replicas": {
        "query_fmt": "(avg(kube_deployment_status_replicas{{ __CLUSTER__ "
        'namespace="{namespace}", deployment="{deployment}"}}) or vector(0))',
        "time_key": None,
        "default_threshold": 0.1,  # e.g., fractional change in replicas
        "minimum_score_strength": 0.2,
        "iqr_multiplier": 2.0,  # looser box, only care about big jumps
        "contamination": 0.001,
        # DBSCAN-specific parameters
        "dbscan_eps": 1.0,  # Very loose (discrete values)
        "dbscan_min_samples": 3,
    },
    "latency": {
        "query_fmt": "(histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_bucket{{"
        '  __CLUSTER__  actual_destination_workload_name="{deployment}", actual_destination_workload_namespace="{'
        'namespace}"}}[2m])) by (le)) or vector(0))',
        "time_key": "fixed",
        "time_range": "2m",
        "default_threshold": 0.5,  # seconds or milliseconds depending on system
        "minimum_score_strength": 0.02,
        "iqr_multiplier": 0.5,
        "contamination": 0.01,
        # DBSCAN-specific parameters
        "dbscan_eps": 0.6,  # Medium-tight for latency
        "dbscan_min_samples": 5,
    },
}


def build_log_context(
    account_id: str | None = None,
    namespace: str | None = None,
    deployment: str | None = None,
    anomaly_type: str | None = None,
    **kwargs,
) -> dict:
    """Build consistent logging context with common fields."""
    context = {}
    if account_id:
        context["account_id"] = account_id
    if namespace:
        context["namespace"] = namespace
    if deployment:
        context["workload"] = deployment  # Using 'workload' as more generic term
    if anomaly_type:
        context["metric_type"] = anomaly_type

    # Add any additional context
    context.update(kwargs)
    return context


class AnomalyResponse:
    def __init__(
        self,
        df: pd.DataFrame,
        start_time: datetime,
        end_time: datetime,
        anomaly_type: str,
        account: str,
        namespace: str,
        deployment: str,
        stats: dict | None = None,
        trigger_threshold_max: float | None = None,
        scores_threshold: float | None = None,
        evaluation_period: pd.Timedelta | None = None,
        has_anomaly: bool | None = None,
        query: str | None = None,
        training_end_time: datetime | None = None,
        insights: list[dict] | None = None,
    ):
        self.df = df
        self.historical_df: pd.DataFrame = pd.DataFrame()  # Will be set by endpoint if needed
        self.start_time = start_time
        self.end_time = end_time
        self.anomaly_type = anomaly_type
        self.account = account
        self.namespace = namespace
        self.deployment = deployment
        self.stats = stats if stats is not None else {}
        self.trigger_threshold_max = trigger_threshold_max
        self.scores_threshold = scores_threshold
        self.evaluation_period = evaluation_period
        self.query = query
        self.training_end_time = training_end_time
        self.insights = insights if insights is not None else []
        self.labels: dict = {}  # PromQL labels will be set by endpoint if available
        if has_anomaly is not None:
            self.has_anomaly = has_anomaly
        else:
            self.has_anomaly = bool(self.df["anomaly"].any()) if not self.df.empty else False
        # Log response creation with context
        context = build_log_context(account, namespace, deployment, anomaly_type)
        logger.debug(
            "AnomalyResponse created",
            extra={
                **context,
                "response_summary": {
                    "has_anomaly": self.has_anomaly,
                    "datapoints": len(self.df),
                    "evaluation_period_used": evaluation_period is not None,
                    "scores_threshold": scores_threshold,
                    "trigger_threshold": trigger_threshold_max,
                },
            },
        )

    def to_dict(self):
        # Reset index to include timestamp as a column
        if "timestamp" not in self.df.columns:
            df_with_timestamp = self.df.reset_index()
            # Rename index column to 'timestamp'
            df_with_timestamp = df_with_timestamp.rename(columns={"index": "timestamp"})
        else:
            df_with_timestamp = self.df.copy()

        # Ensure column names are unique and strings before orient='records'
        # This fixes ValueError: DataFrame columns must be unique for orient='records'
        # and ensures JSON serialization doesn't fail on non-string column names
        df_with_timestamp.columns = [str(col) for col in df_with_timestamp.columns]
        if df_with_timestamp.columns.duplicated().any():
            logger.warning(
                "Duplicate columns found in AnomalyResponse DF, de-duplicating",
                extra={"columns": list(df_with_timestamp.columns)},
            )
            df_with_timestamp = df_with_timestamp.loc[:, ~df_with_timestamp.columns.duplicated()]

        # Convert timestamp to string format
        df_with_timestamp["timestamp"] = df_with_timestamp["timestamp"].astype(str)
        # Use to_json then loads for better memory efficiency than to_dict(orient="records")
        data_json = json.loads(df_with_timestamp.to_json(orient="records", date_format="iso"))

        response_dict = {
            "data": data_json if data_json and data_json is not np.nan else [],
            "start_time": self.start_time.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "end_time": self.end_time.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "anomaly_type": self.anomaly_type,
            "account": self.account,
            "namespace": self.namespace,
            "deployment": self.deployment,
            "has_anomaly": self.has_anomaly,
            "stats": self.stats,
            "trigger_threshold_max": self.trigger_threshold_max,
            "scores_threshold": self.scores_threshold,
            "evaluation_period": self.evaluation_period,
            "training_end_time": str(self.training_end_time) if self.training_end_time else None,
            "insights": self.insights,
        }

        # Include historical data if available
        if not self.historical_df.empty:
            # Reset index for historical data as well
            hist_df_with_timestamp = self.historical_df.reset_index()
            hist_df_with_timestamp = hist_df_with_timestamp.rename(columns={"index": "timestamp"})

            # Ensure column names are unique and strings before orient='records'
            hist_df_with_timestamp.columns = [str(col) for col in hist_df_with_timestamp.columns]
            if hist_df_with_timestamp.columns.duplicated().any():
                hist_df_with_timestamp = hist_df_with_timestamp.loc[:, ~hist_df_with_timestamp.columns.duplicated()]

            hist_df_with_timestamp["timestamp"] = hist_df_with_timestamp["timestamp"].astype(str)
            response_dict["historical_data"] = json.loads(
                hist_df_with_timestamp.to_json(orient="records", date_format="iso")
            )
        else:
            response_dict["historical_data"] = []

        # Include PromQL labels if available
        if self.labels:
            response_dict["labels"] = self.labels

        # Include query if provided (useful for custom queries in /anomaly/detect endpoint)
        if self.query is not None:
            response_dict["query"] = self.query

        return response_dict

    def to_json(self):
        """Convert the response to a JSON-compatible format."""
        return json.dumps(self.to_dict(), default=str, indent=2)


class AnomalyAlgoAbstractConfig(BaseModel):
    """
    Abstract configuration class for anomaly detection algorithms.
    """

    start_time: datetime | None = None
    end_time: datetime | None = None
    account_id: UUID
    namespace: str
    deployment: str
    anomaly_type: str = "memory"
    step: str | None = None
    enabled_smoothing: bool = False
    trigger_threshold_max: float | None = None
    evaluation_period: timedelta | None = None


class AnomalyAlgoAbstract:
    """
    Abstract class for anomaly detection algorithms.
    """

    # Constants for new-workload zero trimming and baseline validation
    METRICS_TO_TRIM = {"memory", "cpu"}
    SUSTAINED_START_MIN_POINTS = 5  # consecutive non-zero required to declare real start
    LEADING_ZEROS_MIN_TRIM = 10  # only trim if prefix > 10 zero points (~10 min at 1m step)
    REAL_DATA_THRESHOLD = {
        "memory": 0.0,
        "cpu": 0.1,
    }
    MIN_ABSOLUTE_TRAINING_POINTS = 60  # ~1 hour of data at 1m step

    # Thresholds for metric_specific_spike filtering
    _MEM_CHANGE_THRESHOLD = 0.3  # 30% per-step change from rolling mean
    _MEM_SIGNIFICANT_ELEVATION_FACTOR = 1.4  # 40% above recent baseline
    _MEM_HIGH_ELEVATION_FACTOR = 2.0  # 2x baseline → always anomalous
    _CPU_CHANGE_THRESHOLD = 0.50  # 50% per-step change for CPU spikes

    def load_data_from_prometheus(
        self, account_id: str, query: str, start_time=None, end_time=None, step=None
    ) -> tuple[pd.Series | None, dict]:
        """Load data from Prometheus and return both data and labels.

        Returns:
            Tuple of (data_series, labels_dict)
        """
        if start_time is None:
            start_time = pd.Timestamp.now() - pd.Timedelta(days=14)

        if end_time is None:
            end_time = pd.Timestamp.now()

        response = fetch_metrics(
            MetricsServerConfigs.url,
            [query],
            start_time=start_time,
            end_time=end_time,
            step=step,
            account_id=account_id,
            duration=None,
        )
        if response is None or len(response) == 0:
            logger.warning(
                "fetch_metrics returned empty response",
                extra={"query": query[:200], "account_id": account_id},
            )
            return None, {}

        metrics = response[0]
        timestamps = metrics.timestamps
        values = metrics.values

        # Debug: Log raw values before conversion to identify data issues
        if values:
            # Sample first few and last few values for debugging
            sample_size = min(5, len(values))
            sample_values = values[:sample_size] + (values[-sample_size:] if len(values) > sample_size else [])
            zero_count_raw = sum(1 for v in values if v == "0" or v == 0 or v == "0.0")

            # Log at WARNING level if all zeros - this indicates query mismatch
            if zero_count_raw == len(values):
                logger.warning(
                    "ALL values from Prometheus are zeros - query likely not matching any metrics",
                    extra={
                        "query": query[:300],
                        "total_values": len(values),
                        "sample_values": sample_values[:10],
                        "account_id": account_id,
                        "hint": "Check if namespace/deployment/pod pattern matches, or if __CLUSTER__ is handled",
                    },
                )
            else:
                logger.debug(
                    "Raw values from Prometheus before conversion",
                    extra={
                        "total_values": len(values),
                        "sample_values": sample_values,
                        "zero_count_in_raw": zero_count_raw,
                        "zero_percentage_raw": round(zero_count_raw / len(values) * 100, 2) if values else 0,
                    },
                )

        time_index = pd.to_datetime(timestamps, unit="s")
        numeric_values = pd.to_numeric(values, errors="coerce")
        data = pd.Series(numeric_values, index=time_index)

        # Debug: Check for conversion issues
        nan_from_conversion = numeric_values.isna().sum() if hasattr(numeric_values, "isna") else 0
        zero_count_after = (data == 0).sum()

        if nan_from_conversion > 0 or (zero_count_after > len(data) * 0.5):
            logger.warning(
                "Potential data conversion issue detected",
                extra={
                    "total_datapoints": len(data),
                    "nan_from_conversion": int(nan_from_conversion),
                    "zeros_after_conversion": int(zero_count_after),
                    "zero_percentage": round(zero_count_after / len(data) * 100, 2) if len(data) > 0 else 0,
                    "non_zero_values": int((data != 0).sum()),
                    "data_min": float(data.min()) if len(data) > 0 else None,
                    "data_max": float(data.max()) if len(data) > 0 else None,
                    "data_mean": float(data.mean()) if len(data) > 0 else None,
                },
            )

        # Extract PromQL labels from metric metadata
        labels = dict(metrics.metric) if hasattr(metrics, "metric") else {}
        # Remove internal labels that start with __
        labels = {k: v for k, v in labels.items() if not k.startswith("__")}

        # Handle NaN values in the data
        if data.isna().any():
            nan_count = data.isna().sum()
            logger.warning(
                "Found NaN values in prometheus data",
                extra={"nan_count": nan_count, "total_datapoints": len(data), "action": "forward_backward_fill"},
            )
            # Forward fill then backward fill to handle NaN values
            data = data.ffill().bfill()
            # If still NaN (all values were NaN), fill with 0
            data = data.fillna(0.0)

        return cast(pd.Series, data), labels

    def load_and_prepare_data(
        self,
        config: AnomalyAlgoAbstractConfig,
    ) -> pd.Series:
        """
        Loads data based on anomaly type and prepares it for anomaly detection.
        Uses a lookup table to pick the PromQL template + default threshold.
        """
        # 2) Lookup and validate
        meta = TEMPLATES.get(self.config.anomaly_type)
        if meta is None:
            raise ValueError(f"Invalid anomaly type: {self.config.anomaly_type}")

        # 3) Determine the time range token - now using fixed windows for consistency
        # All queries now use fixed time windows to avoid step-dependent inconsistencies
        time_range = meta.get("time_range", "1m")

        # 4) Build the final query + threshold
        query = meta["query_fmt"].format(
            namespace=self.config.namespace,
            deployment=self.config.deployment,
            time_range=time_range,
        )

        # 6) Load historical training data only if needed
        data, _ = self.load_data_from_prometheus(
            self.config.account_id,
            query,
            start_time=config.start_time,
            end_time=config.end_time,
            step=config.step,
        )
        if data is None:
            logger.warning(f"anomaly :got no data for config {config.model_dump_json()} aborting")
            raise CancelPrediction(f"anomaly :got no data for config {config.model_dump_json()} aborting")
        if config.anomaly_type.lower() == "cpu" and data is not None:
            data = data * 1000  # Convert CPU usage to millicores for consistency
            data = data.round(4)  # Keep precision, avoid integer truncation
        return data

    def get_default_parameters(
        self,
        config: AnomalyAlgoAbstractConfig,
        data: pd.Series,
    ) -> None:
        """Get default parameters and configuration for anomaly detection."""
        if data is not None:
            config.start_time = data.index.min()
            config.end_time = data.index.max()

        if config.start_time is None:
            training_days = 14 if config.evaluation_period else 7
            if config.end_time is None:
                config.start_time = datetime.utcnow() - timedelta(days=training_days)
                config.end_time = datetime.utcnow()
            else:
                config.start_time = config.end_time - timedelta(days=training_days)
        if config.end_time is None:
            config.end_time = datetime.utcnow()
        if config.step is None:
            step_map = {"memory": "1m", "cpu": "2m", "errorrate": "2m", "latency": "2m", "replicas": "5m"}
            config.step = step_map.get(config.anomaly_type.lower(), "1m")

    def load_data(self, config: AnomalyAlgoAbstractConfig) -> pd.Series:
        """
        Get data for anomaly detection based on the configuration.
        """
        # Placeholder for actual data fetching logic
        # This should be replaced with the actual implementation to fetch data
        return pd.Series(dtype=float)

    def process_metrics(
        self, config: AnomalyAlgoAbstractConfig, data: pd.Series | None = None, smoothing: bool = False
    ) -> "AnomalyResponse":
        """
        Process anomaly data with optional smoothing.
        """
        if smoothing and config.get("enabled_smoothing", False):
            return self.apply_smoothing(data)
        # Upcast to the subclass's config type before get_default_parameters() runs.
        # Each subclass sets self.config_class = its own config type in __init__, so this is
        # safe. Without this, get_default_parameters() accesses algo-specific fields
        # (e.g. config.eps, config.training_end_time) that don't exist on AnomalyAlgoAbstractConfig.
        if not isinstance(config, self.config_class):
            config = self.config_class(**config.model_dump())
            self.config = config  # sync self.config so methods like apply_evaluation_period_filter
            # (which use self.config directly) also see the upcast instance
        self.get_default_parameters(data=data, config=config)
        if data is None or data.empty:
            data = self.load_and_prepare_data(config)
        # Trim artificial leading zeros from Prometheus `or vector(0)` for new workloads.
        # Must run after CPU unit conversion (done in load_and_prepare_data) and before
        # get_anomaly so the training/evaluation split operates on clean data.
        data = self.trim_leading_zeros_for_new_workload(data, config.anomaly_type)
        # Guard: raise CancelPrediction if insufficient real training data remains after trim.
        self.check_new_workload_and_cancel(data, config)
        self.set_context(config)
        response = self.get_anomaly(data, config)
        response = self.apply_default_threshold_filter(response, config)
        return response

    @staticmethod
    def apply_smoothing(data: pd.Series) -> pd.Series:
        """Apply smoothing to data if enabled."""
        if data is not None and not data.empty:
            return data.rolling(window=3, center=True).mean().ffill().bfill()
        return data

    def __init__(self, config: AnomalyAlgoAbstractConfig, log_context: Dict[str, Any] | None = None):
        self.config_class = AnomalyAlgoAbstractConfig
        self.end_time: datetime | None = None

        if isinstance(config, dict):
            self.config: AnomalyAlgoAbstractConfig = self.config_class(**config)
        elif isinstance(config, self.config_class):
            self.config: AnomalyAlgoAbstractConfig = config
        else:
            raise TypeError("Config must be a dictionary or an instance of AnomalyAlgoAbstractConfig")
        self.log_context = log_context if log_context is not None else {}

    def set_context(self, config: AnomalyAlgoAbstractConfig) -> None:
        """
        Set the context for logging or other purposes.
        """
        self.log_context.update(config.model_dump())

    def evaluate(self, data: pd.Series) -> pd.Series:
        """
        Evaluate the model on the provided data.
        """
        raise NotImplementedError("Subclasses should implement this method.")

    def get_anomaly(self, data: pd.Series, config: AnomalyAlgoAbstractConfig) -> "AnomalyResponse":
        """
        Get anomalies from the provided data.
        """
        raise NotImplementedError("Subclasses should implement this method.")

    def safe_concat_dataframes(self, objs: list[pd.DataFrame | None]) -> pd.DataFrame:
        """Safely concatenate pandas DataFrames, filtering out None and empty objects."""
        # Filter out None objects and empty DataFrames
        valid_objs = []
        for obj in objs:
            if obj is not None and len(obj) > 0:
                valid_objs.append(obj)

        # If no valid objects, return empty DataFrame
        if not valid_objs:
            return pd.DataFrame()

        # If only one valid object, return it directly
        if len(valid_objs) == 1:
            return valid_objs[0]

        # Safe concatenation with valid objects only (columns for response dataframe)
        return pd.concat(valid_objs, axis="columns")

    def generate_stats(self, data: pd.Series) -> Dict:
        """Generate statistics for the training data."""
        if data is None or data.empty:
            return {
                "count": 0,
                "training_count": 0,
                "min": 0.0,
                "max": 0.0,
                "mean": 0.0,
                "p99": 0.0,
                "p999": 0.0,
            }

        return {
            "count": int(len(data)),
            "training_count": int(len(data)),
            "min": float(data.min(skipna=True)) if not data.empty else 0,
            "max": float(data.max(skipna=True)) if not data.empty else 0,
            "mean": float(data.mean(skipna=True)) if not data.empty else 0,
            "p99": float(data.quantile(0.99)) if not data.empty else 0,
            "p999": float(data.quantile(0.999)) if not data.empty else 0,
        }

    def trim_leading_zeros_for_new_workload(self, data: pd.Series, anomaly_type: str) -> pd.Series:
        """
        Trim artificial leading zeros that Prometheus returns for new workloads.

        When a workload is new, PromQL queries use `or vector(0)` as a fallback,
        returning zeros for all timestamps before the workload existed. These zeros
        corrupt the training baseline and cause division-by-zero in spike calculations.

        Only applies to memory and CPU — for these metrics zero = workload not running.
        errorrate, latency, and replicas zeros are semantically valid and are not trimmed.

        Strategy:
        - Find the first "sustained start": SUSTAINED_START_MIN_POINTS consecutive
          non-zero values, to ignore isolated Prometheus scrape blips during pod init.
        - Only trims when the leading zero prefix exceeds LEADING_ZEROS_MIN_TRIM
          (absolute count), avoiding trimming minor scrape gaps in established workloads.
        """
        if anomaly_type.lower() not in self.METRICS_TO_TRIM:
            return data

        if data.empty:
            return data

        threshold = self.REAL_DATA_THRESHOLD.get(anomaly_type.lower(), 0.0)
        real_mask = (data > threshold).values

        if not real_mask.any():
            # All zeros/below-threshold: workload never ran in this window.
            # Return as-is; check_new_workload_and_cancel will raise CancelPrediction.
            return data

        # Find first sustained start: SUSTAINED_START_MIN_POINTS consecutive real points.
        # This guards against single Prometheus scrape blips during pod init
        # (e.g., [0,0,0, 8MB(blip), 0,0,0, 25MB, 26MB, ...] — starts at 25MB, not 8MB).
        sustained_start_pos = None
        n = len(real_mask)
        for i in range(n):
            if real_mask[i]:
                end = min(i + self.SUSTAINED_START_MIN_POINTS, n)
                if all(real_mask[i:end]):
                    sustained_start_pos = i
                    break

        if sustained_start_pos is None or sustained_start_pos == 0:
            # No sustained non-zero run found, or workload started from the very beginning.
            return data

        if sustained_start_pos < self.LEADING_ZEROS_MIN_TRIM:
            # Too few leading zeros to be a new-workload pattern (minor scrape gap).
            return data

        trimmed = data.iloc[sustained_start_pos:]
        logger.info(
            "New workload detected: trimming leading zeros from data",
            extra={
                **self.log_context,
                "new_workload_trim": {
                    "anomaly_type": anomaly_type,
                    "leading_zeros_trimmed": sustained_start_pos,
                    "total_original_points": n,
                    "remaining_points": len(trimmed),
                    "zero_fraction": round(sustained_start_pos / n, 3),
                    "first_real_data_time": str(trimmed.index[0]) if not trimmed.empty else "N/A",
                },
            },
        )
        return trimmed

    def check_new_workload_and_cancel(
        self,
        data: pd.Series,
        config: AnomalyAlgoAbstractConfig,
    ) -> None:
        """
        Raise CancelPrediction when a workload is too new to have a reliable training baseline.

        Called after trim_leading_zeros_for_new_workload. Validates that the training window
        contains enough real data points to fit a meaningful anomaly model. Without a baseline,
        anomaly detection would produce false positives or arithmetic errors.

        Checks:
        - memory/cpu: requires MIN_ABSOLUTE_TRAINING_POINTS non-zero points before the
          evaluation window boundary (~1 hour of data at 1m step).
        - errorrate/latency: cancels if training window has zero max (no traffic at all),
          since there is no error rate / latency baseline to compare against.
        """
        if data is None or data.empty:
            raise CancelPrediction(
                f"New workload [{config.namespace}/{config.deployment}]: no data available, "
                "cannot establish baseline for anomaly detection."
            )

        # Compute training window boundary using evaluation_period if set.
        if config.evaluation_period is not None and config.end_time is not None:
            training_end = pd.Timestamp(config.end_time) - pd.to_timedelta(config.evaluation_period)
            # Normalize both to UTC-aware for robust comparison (handles naive, aware,
            # and two aware timestamps in different timezones).
            training_end_utc = (
                training_end.tz_convert("UTC") if training_end.tzinfo is not None else training_end.tz_localize("UTC")
            )
            if data.index.tz is not None:
                data_index_utc = data.index.tz_convert("UTC")
            else:
                data_index_utc = data.index.tz_localize("UTC")
            training_data = data[data_index_utc < training_end_utc]
        else:
            training_data = data

        anomaly_type = config.anomaly_type.lower()

        if anomaly_type in ["memory", "cpu"]:
            real_threshold = self.REAL_DATA_THRESHOLD.get(anomaly_type, 0.0)
            real_training_points = int((training_data > real_threshold).sum())
            if real_training_points < self.MIN_ABSOLUTE_TRAINING_POINTS:
                raise CancelPrediction(
                    f"New workload [{config.namespace}/{config.deployment}]: only "
                    f"{real_training_points} real {anomaly_type} training points available "
                    f"(minimum required: {self.MIN_ABSOLUTE_TRAINING_POINTS}). "
                    "Skipping detection to avoid false positives from an insufficient baseline."
                )

        elif anomaly_type in ["errorrate", "latency"]:
            # No traffic in training window = no baseline to compare evaluation against.
            if training_data.empty or float(training_data.max()) == 0.0:
                raise CancelPrediction(
                    f"New workload [{config.namespace}/{config.deployment}]: "
                    f"no {anomaly_type} data in training window "
                    "(workload has not received traffic yet). "
                    "Skipping detection until a traffic baseline is established."
                )

    def update_anomaly_to_peak_value(self, merged_df):
        merged_df = merged_df.copy()
        index_list = merged_df.index.tolist()

        for i in range(len(index_list) - 1):
            tail_idx = index_list[i]
            head_idx = index_list[i + 1]

            if merged_df.loc[tail_idx, "anomaly"]:
                if merged_df.loc[head_idx, "data"] >= merged_df.loc[tail_idx, "data"]:
                    merged_df.loc[tail_idx, "anomaly"] = False
                    merged_df.loc[head_idx, "anomaly"] = True
                else:
                    merged_df.loc[head_idx, "anomaly"] = False
        return merged_df

    def create_response_dataframe(
        self, combined_data: pd.Series, combined_anomalies: pd.Series, combined_anomaly_scores: pd.Series
    ) -> pd.DataFrame:
        """Create the final merged dataframe with NaN cleanup."""
        data_df = combined_data.to_frame(name="data")
        anomalies_df = combined_anomalies.to_frame(name="anomaly")
        anomaly_scores_df = combined_anomaly_scores.to_frame(name="anomaly_score")

        if anomaly_scores_df["anomaly_score"].isna().any():
            nan_count = anomaly_scores_df["anomaly_score"].isna().sum()
            logger.warning(
                "Found NaN anomaly scores",
                extra={
                    "nan_count": nan_count,
                    "total_scores": len(anomaly_scores_df),
                    "action": "fill_with_zero",
                },
            )
            # Fill with 0 (no signal), not mean — filling with mean would give
            # non-anomalous points the same score as genuine anomalies.
            anomaly_scores_df["anomaly_score"] = anomaly_scores_df["anomaly_score"].fillna(0.0)

        merged_df = self.safe_concat_dataframes([data_df, anomalies_df, anomaly_scores_df])
        merged_df.index = pd.to_datetime(merged_df.index)
        merged_df["timestamp"] = merged_df.index.astype(str)
        merged_df["anomaly"] = merged_df["anomaly"].astype(bool)

        return merged_df

    def safe_concat_series(self, objs: list[pd.Series | None]) -> pd.Series:
        """Safely concatenate pandas Series, filtering out None and empty objects."""
        # Filter out None objects and empty Series
        valid_objs = []
        for obj in objs:
            if obj is not None and len(obj) > 0:
                valid_objs.append(obj)

        # If no valid objects, return empty Series
        if not valid_objs:
            # Try to preserve dtype from first non-None object
            for obj in objs:
                if obj is not None:
                    return pd.Series([], dtype=obj.dtype if hasattr(obj, "dtype") else "float64")
            # Fallback to empty Series
            return pd.Series([], dtype="float64")

        # If only one valid object, return it directly
        if len(valid_objs) == 1:
            return valid_objs[0]

        # Safe concatenation with valid objects only
        return pd.concat(valid_objs, axis="index")

    def metric_specific_spike(self, data: pd.Series, anomalies: pd.Series) -> pd.Series:
        # Apply metric-specific filtering
        if self.config.anomaly_type.lower() in ["errorrate", "latency"]:
            # For error rate and latency, don't flag 0 or very low values as anomalies
            min_meaningful_threshold = (
                0.001 if self.config.anomaly_type.lower() == "errorrate" else 0.01
            )  # 0.1% error rate or 10ms latency
            non_zero_filter = data >= min_meaningful_threshold
            anomalies = anomalies & non_zero_filter
        elif self.config.anomaly_type.lower() in ["memory", "cpu"]:
            # For memory and CPU, focus on sudden changes rather than sustained levels.
            # Calculate percentage change from rolling mean to identify true spikes.
            rolling_mean = data.rolling(window=10, min_periods=1).mean()
            # Safe division: suppress numpy warnings; handle zero rolling_mean explicitly.
            # - rolling_mean == 0 AND data == 0  → 0% change (no spike)
            # - rolling_mean == 0 AND data  > 0  → treat as 100% change (definite spike)
            with np.errstate(invalid="ignore", divide="ignore"):
                raw_pct = np.abs((data.values - rolling_mean.values) / rolling_mean.values)
            zero_mean = rolling_mean.values == 0
            raw_pct = np.where(zero_mean, np.where(data.values > 0, 1.0, 0.0), raw_pct)
            raw_pct = np.nan_to_num(raw_pct, nan=0.0, posinf=1.0, neginf=0.0)
            percent_change = pd.Series(raw_pct, index=data.index)

            if self.config.anomaly_type.lower() == "memory":
                # For memory: flag if highly elevated OR (sudden change AND elevated).
                # Using AND-only missed slow leaks where per-step change is small but
                # total elevation is large (e.g. memory doubling over 2 hours).
                change_threshold = self._MEM_CHANGE_THRESHOLD
                recent_baseline = data.rolling(window=30, min_periods=10).quantile(0.5)
                # Safe division: handle zero or NaN baseline (new workload, or early window).
                # - baseline == 0 or NaN AND data  > 0  → maximally elevated
                # - baseline == 0 or NaN AND data == 0  → not elevated
                with np.errstate(invalid="ignore", divide="ignore"):
                    raw_elev = data.values / recent_baseline.values
                zero_or_nan_base = (recent_baseline.values == 0) | np.isnan(recent_baseline.values)
                raw_elev = np.where(zero_or_nan_base, np.where(data.values > 0, 1e9, 0.0), raw_elev)
                raw_elev = np.nan_to_num(raw_elev, nan=0.0, posinf=1e9, neginf=0.0)
                elevation_factor = pd.Series(raw_elev, index=data.index)
                significant_elevation = elevation_factor >= self._MEM_SIGNIFICANT_ELEVATION_FACTOR
                high_elevation = elevation_factor >= self._MEM_HIGH_ELEVATION_FACTOR
                # OR: either very highly elevated, or moderate spike with elevation
                significant_change_filter = high_elevation | (
                    (percent_change >= change_threshold) & significant_elevation
                )
            else:  # CPU
                significant_change_filter = percent_change >= self._CPU_CHANGE_THRESHOLD
            anomalies = anomalies & significant_change_filter
        return anomalies

    def apply_default_threshold_filter(
        self, response: "AnomalyResponse", config: AnomalyAlgoAbstractConfig
    ) -> "AnomalyResponse":
        """Apply minimum significant-usage threshold filter to anomaly results.

        Ensures anomalies are only flagged when the metric value is above a meaningful
        baseline — e.g. no CPU anomaly when usage is effectively zero. Applied uniformly
        after every algo's get_anomaly() returns, so all algorithms are consistent.

        Thresholds (from TEMPLATES or config.trigger_threshold_max):
        - memory:    data >= threshold * 3.0  (require clearly elevated usage)
        - cpu:       data >= threshold
        - errorrate: data >= threshold
        - latency:   data >= threshold
        - replicas / custom / unknown: no filtering applied
        """
        anomaly_type = config.anomaly_type.lower()

        # Skip filtering for types without a meaningful absolute threshold
        if anomaly_type in ["custom", "generic", "unknown", "replicas"]:
            return response

        meta = TEMPLATES.get(anomaly_type)
        threshold = config.trigger_threshold_max or (meta["default_threshold"] if meta else 0.0)

        if not threshold or response.df.empty:
            return response

        # Handle column name difference: isolation_tree/dbscan use "data", zscore uses "value"
        data_col = "data" if "data" in response.df.columns else "value"
        if data_col not in response.df.columns:
            return response

        response.df = response.df.copy()
        data_values = response.df[data_col]

        if anomaly_type == "memory":
            threshold_filter = data_values >= (threshold * 3.0)
        else:
            # cpu, errorrate, latency
            threshold_filter = data_values >= threshold

        response.df["anomaly"] = response.df["anomaly"] & threshold_filter
        response.has_anomaly = bool(response.df["anomaly"].any())
        response.trigger_threshold_max = threshold
        return response

    def handle_timezone_compatibility(self, combined_data: pd.Series, evaluation_start: pd.Timestamp) -> pd.Timestamp:
        """Handle timezone compatibility for evaluation period comparison."""
        index_tz = getattr(combined_data.index, "tz", None) if hasattr(combined_data.index, "tz") else None
        start_tz = getattr(evaluation_start, "tz", None) if hasattr(evaluation_start, "tz") else None

        if index_tz is None and start_tz is not None:
            # Convert timezone-aware evaluation_start to naive
            logger.debug(
                "Converting timezone-aware evaluation_start to naive for comparison",
                extra={**self.log_context, "evaluation_start_tz": str(start_tz), "data_index_tz": "naive"},
            )
            evaluation_start = evaluation_start.tz_convert("UTC").tz_localize(None)
        elif index_tz is not None and start_tz is None:
            # Convert naive evaluation_start to timezone-aware
            logger.debug(
                "Converting naive evaluation_start to timezone-aware for comparison",
                extra={**self.log_context, "evaluation_start_tz": "naive", "data_index_tz": str(index_tz)},
            )
            evaluation_start = pd.Timestamp(evaluation_start).tz_localize("UTC")

        return evaluation_start

    def _format_metric_value(self, value: float, anomaly_type: str) -> str:
        """Format a metric value into a human-readable string."""
        atype = anomaly_type.lower()
        if atype == "memory":
            if value >= 1024**3:
                return f"{value / 1024 ** 3:.2f} GB"
            elif value >= 1024**2:
                return f"{value / 1024 ** 2:.1f} MB"
            elif value >= 1024:
                return f"{value / 1024:.1f} KB"
            else:
                return f"{value:.0f} B"
        elif atype == "cpu":
            # value is in original prometheus scale (cores); display as millicores when < 1 core
            mc = value * 1000
            if mc >= 1000:
                return f"{value:.2f} cores"
            return f"{mc:.0f}m"
        elif atype == "errorrate":
            return f"{value * 100:.2f}%"
        elif atype == "latency":
            if value >= 1.0:
                return f"{value:.2f}s"
            return f"{value * 1000:.0f}ms"
        elif atype == "replicas":
            return f"{int(round(value))} replicas"
        else:
            return f"{value:.3f}"

    def _format_cpu_consistent(self, value: float, baseline: float) -> tuple[str, str]:
        """Format CPU value and baseline using the same unit (cores or millicores)."""
        # Use the larger of the two to decide unit, so both display consistently
        ref = max(abs(value), abs(baseline))
        if ref * 1000 >= 1000:
            return f"{value:.2f} cores", f"{baseline:.2f} cores"
        return f"{value * 1000:.0f}m", f"{baseline * 1000:.0f}m"

    def _format_deviation(self, abs_dev_pct: float, direction: str) -> str:
        """Return a human-readable deviation string.

        Small deviations (< 100%) stay as percentages.
        Large deviations become a multiplier for readability
        (e.g. 131223% above → 1,313× above).
        """
        if abs_dev_pct < 100:
            return f"{abs_dev_pct:.1f}% {direction}"
        ratio = (abs_dev_pct / 100) + 1
        if ratio >= 10:
            return f"{ratio:,.0f}× {direction}"
        return f"{ratio:.1f}× {direction}"

    def _generate_insight_sentence(
        self,
        value: float,
        baseline: float,
        deviation_pct: float,
        severity: str,
        anomaly_type: str,
        comparison_window: str,
    ) -> str:
        """Build a complete human-readable insight sentence for an anomaly."""
        atype = anomaly_type.lower()
        metric_labels = {
            "memory": "Memory usage",
            "cpu": "CPU usage",
            "errorrate": "Error rate",
            "latency": "P99 latency",
            "replicas": "Pod replicas",
        }
        spike_verbs = {
            "memory": "spiked to",
            "cpu": "spiked to",
            "errorrate": "jumped to",
            "latency": "spiked to",
            "replicas": "increased to",
        }
        drop_verbs = {
            "memory": "dropped to",
            "cpu": "dropped to",
            "errorrate": "dropped to",
            "latency": "dropped to",
            "replicas": "decreased to",
        }
        metric_name = metric_labels.get(atype, atype.capitalize())
        is_spike = deviation_pct >= 0
        verb = spike_verbs.get(atype, "reached") if is_spike else drop_verbs.get(atype, "dropped to")
        formatted_value = self._format_metric_value(value, atype)

        if baseline == 0 or comparison_window == "first detection":
            return f"{metric_name} {verb} {formatted_value} — no historical baseline available ({severity})"

        # For CPU, ensure value and baseline use the same unit to avoid mixing "68m" vs "10.19 cores"
        if atype == "cpu":
            formatted_value, formatted_baseline = self._format_cpu_consistent(value, baseline)
        else:
            formatted_baseline = self._format_metric_value(baseline, atype)
        abs_dev_pct = abs(deviation_pct)
        direction = "above" if deviation_pct > 0 else "below"
        deviation_str = self._format_deviation(abs_dev_pct, direction)

        return (
            f"{metric_name} {verb} {formatted_value} — "
            f"{deviation_str} the {comparison_window} "
            f"of {formatted_baseline}"
        )

    def generate_insights(
        self,
        data: pd.Series,
        anomalies: pd.Series,
        anomaly_scores: pd.Series,
        stats: dict,
        config: AnomalyAlgoAbstractConfig,
    ) -> list[dict]:
        """Generate structured insights with a human-readable sentence for each anomalous point.

        Only generates insights for anomalous data points and deduplicates by
        (value, baseline_value, comparison_window, severity) so callers receive a
        clean, non-redundant list.
        """
        insights = []
        baseline = stats.get("mean", 0.0)

        # Fallback to p50 or median if mean is not available
        if not baseline or pd.isna(baseline):
            baseline = stats.get("p50", data.median() if not data.empty else 0.0)

        # For CPU, stats["mean"] may be in millicores (×1000 internal scale) while data
        # is in cores — or both may be in millicores. Normalise so baseline and data share
        # the same unit before formatting.
        #   • If baseline ≈ 1000× data.mean()  → baseline is in millicores, data in cores → ÷1000
        #   • If baseline ≈ data.mean()         → already the same unit → no change
        if config.anomaly_type.lower() == "cpu" and baseline and not data.empty:
            data_mean = float(data.mean())
            if data_mean > 0:
                ratio = baseline / data_mean
                if 500 < ratio < 2000:
                    # baseline is ~1000× the data values → convert millicores → cores
                    baseline = baseline / 1000

        if config.end_time is not None and config.start_time is not None:
            total_hours = (config.end_time - config.start_time).total_seconds() / 3600
            if total_hours >= 24:
                _window_label = f"{int(total_hours // 24)}-day average"
            else:
                _window_label = f"{int(total_hours)}-hour average"
        else:
            _window_label = "historical average"
        comparison_window = _window_label if baseline != 0 else "first detection"
        baseline_val = float(baseline) if baseline else 0.0

        seen: set[tuple] = set()

        for timestamp, is_anomaly in anomalies.items():
            if not is_anomaly:
                continue

            value = float(data.loc[timestamp])
            score = float(anomaly_scores.loc[timestamp])

            # Calculate deviation
            if baseline != 0 and not pd.isna(baseline):
                deviation_abs = value - baseline
                deviation_pct = (deviation_abs / baseline) * 100
            else:
                deviation_abs = 0.0
                deviation_pct = 0.0

            severity = self._calculate_severity(deviation_pct, score)

            # Deduplicate: same value + baseline + comparison_window + severity = same message
            dedup_key = (round(value, 4), round(baseline_val, 4), comparison_window, severity)
            if dedup_key in seen:
                continue
            seen.add(dedup_key)

            insights.append(
                {
                    "timestamp": timestamp.isoformat(),
                    "value": value,
                    "baseline_value": baseline_val,
                    "deviation_absolute": float(deviation_abs),
                    "deviation_percent": float(deviation_pct),
                    "severity": severity,
                    "anomaly_score": score,
                    "comparison_window": comparison_window,
                    "insight": self._generate_insight_sentence(
                        value, baseline_val, deviation_pct, severity, config.anomaly_type, comparison_window
                    ),
                }
            )

        return insights

    def _calculate_severity(self, deviation_pct: float, score: float) -> str:
        """Calculate severity based on deviation magnitude and algorithm score.

        Integrates both deviation percentage and anomaly score for more nuanced classification.
        """
        abs_dev = abs(deviation_pct)
        abs_score = abs(score)

        # Critical: Very large deviations OR extreme anomaly scores
        if abs_dev > 100:
            return "critical"
        # Consider score for borderline critical cases (80-100% deviation with high score)
        if abs_dev > 80 and abs_score > 0.5:
            return "critical"

        # High: Large deviations OR strong anomaly scores
        if abs_dev > 50:
            return "high"
        # Moderate deviation (40-50%) with strong score can be high severity
        if abs_dev > 40 and abs_score > 0.4:
            return "high"

        # Medium: Moderate deviations OR significant anomaly scores
        if abs_dev > 25:
            return "medium"
        # For small deviations, check algorithm-specific score
        # IsolationForest: very negative scores indicate strong anomalies
        if hasattr(self, "scores_threshold") and self.scores_threshold is not None:
            if score < self.scores_threshold * 1.5:
                return "medium"
        # General score-based medium classification for moderate deviations
        if abs_dev > 15 and abs_score > 0.3:
            return "medium"

        return "low"
