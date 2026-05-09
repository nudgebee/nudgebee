import logging
import threading

from flask import Blueprint, Response, abort, jsonify, request
from opentelemetry import trace
from server.anomaly.anomaly_algo import get_anomaly_algo
from server.utils.utils import get_trace
from server.exception import CancelPrediction
import datetime
import os
import pandas as pd
from dotenv import load_dotenv

# Limit concurrent anomaly detection per worker to prevent concurrent
# DBSCAN distance matrix allocations from causing OOM
_anomaly_semaphore = threading.Semaphore(1)

load_dotenv()

logger = logging.getLogger(__name__)
app = Blueprint("anomaly", __name__)
ANOMALY_ALGO = os.getenv("ANOMALY_ALGO", "ISOLATION_TREE")


def _validate_required_field(data: dict, field: str, error_msg: str | None = None) -> None:
    """Helper to validate required fields."""
    if not data.get(field):
        msg = error_msg or f"Please provide value for {field}"
        abort(400, description=msg)


def _parse_timestamp(timestamp_str: str | None, field_name: str = "timestamp") -> datetime.datetime | None:
    """Helper to parse ISO format timestamp strings."""
    if timestamp_str is None:
        return None
    try:
        return datetime.datetime.strptime(timestamp_str, "%Y-%m-%dT%H:%M:%SZ")
    except (ValueError, TypeError) as e:
        abort(400, description=f"Invalid {field_name} format. Expected ISO format: YYYY-MM-DDTHH:MM:SSZ. Error: {e}")


def _validate_time_range(analysis_start_time: datetime.datetime, analysis_end_time: datetime.datetime) -> None:
    """Validate that start time is before end time."""
    if analysis_start_time >= analysis_end_time:
        abort(400, description="analysis_start_time must be before analysis_end_time")


def _validate_data_split(
    all_data: pd.Series,
    analysis_start_time: datetime.datetime,
    analysis_end_time: datetime.datetime,
    account_id: str | None,
    deployment: str | None,
) -> tuple:
    """Validate we have data on both sides of the split."""
    training_data = all_data[all_data.index < analysis_start_time]
    evaluation_data = all_data[all_data.index >= analysis_start_time]

    if training_data.empty:
        logger.warning(
            "anomaly_detect: No training data available before analysis period",
            extra={
                "account": account_id,
                "deployment": deployment,
                "historical_start": all_data.index[0] if len(all_data) > 0 else None,
                "analysis_start": analysis_start_time,
                "total_data_points": len(all_data) if all_data is not None else 0,
            },
        )
        abort(
            400,
            description="No historical data available before analysis period. Increase historical_window_hours",
        )

    if evaluation_data is None or evaluation_data.empty:
        logger.warning(
            "anomaly_detect: No evaluation data for analysis period",
            extra={
                "account": account_id,
                "deployment": deployment,
                "analysis_start": analysis_start_time,
                "analysis_end": analysis_end_time,
                "total_data_points": len(all_data) if all_data is not None else 0,
            },
        )
        abort(400, description="No data found in the analysis time range")

    return training_data, evaluation_data


def _filter_and_mark_periods(
    response,
    analysis_start_time: datetime.datetime,
    analysis_end_time: datetime.datetime,
    include_historical_data: bool,
    account_id: str | None,
    deployment: str | None,
) -> None:
    """Separate historical and evaluation data into different response fields with labels."""
    if not response or response.df.empty:
        return

    result_df = response.df.copy()

    # Split data into historical and evaluation periods
    historical_mask = result_df.index < analysis_start_time
    evaluation_mask = (result_df.index >= analysis_start_time) & (result_df.index <= analysis_end_time)

    historical_df = result_df[historical_mask].copy()
    evaluation_df = result_df[evaluation_mask].copy()

    # Store evaluation data in response.df (main response data)
    response.df = evaluation_df

    # Store historical data as a separate attribute on the response object
    response.historical_df = historical_df if include_historical_data else pd.DataFrame()

    # Log the split
    if include_historical_data:
        logger.info(
            "anomaly_detect: Returned evaluation and historical data separately",
            extra={
                "account": account_id,
                "deployment": deployment,
                "historical_datapoints": len(historical_df),
                "evaluation_datapoints": len(evaluation_df),
                "anomalies_in_eval": int(evaluation_df["anomaly"].sum()) if not evaluation_df.empty else 0,
            },
        )
    else:
        logger.info(
            "anomaly_detect: Returned evaluation period data only",
            extra={
                "account": account_id,
                "deployment": deployment,
                "evaluation_datapoints": len(evaluation_df),
                "anomalies_in_eval": int(evaluation_df["anomaly"].sum()) if not evaluation_df.empty else 0,
            },
        )

    # Recalculate has_anomaly based on evaluation period only
    response.has_anomaly = bool(evaluation_df["anomaly"].any()) if not evaluation_df.empty else False


@app.route("/anomaly", methods=["POST"])
def predict() -> Response:
    acquired = _anomaly_semaphore.acquire(timeout=30)
    if not acquired:
        abort(503, description="Server busy processing another anomaly request. Please retry.")
    try:
        return _predict_inner()
    finally:
        _anomaly_semaphore.release()


def _predict_inner() -> Response:
    with get_trace(__name__).start_as_current_span("anomaly"):
        data = dict(request.get_json(force=True))
        _validate_required_field(data, "namespace", "Please provide value for namespace name")
        _validate_required_field(data, "deployment", "Please provide value for deployment name")
        _validate_required_field(data, "tenant", "Please provide value for tenant id")
        _validate_required_field(data, "account", "Please provide value for account id")
        _validate_required_field(data, "type", "Please provide value for anomaly type")

        current_span = trace.get_current_span()
        current_span.set_attribute("Deployment", str(data.get("deployment")))
        current_span.set_attribute("Namespace", str(data.get("namespace")))
        current_span.set_attribute("Account", str(data.get("account")))
        current_span.set_attribute("Tenant", str(data.get("tenant")))
        current_span.set_attribute("Type", str(data.get("type")))
        current_span.set_attribute("algo", str(data.get("algo")))

        start_time = _parse_timestamp(data.get("start_time"), "start_time")
        end_time = _parse_timestamp(data.get("end_time"), "end_time")
        evaluation_period = data.get("evaluation_period")
        if evaluation_period is not None:
            evaluation_period = pd.Timedelta(minutes=int(evaluation_period))
        algo: str | None = data.get("algo")
        if algo is None or not algo:
            algo = ANOMALY_ALGO

        start_execution_time = datetime.datetime.now()
        try:
            config_dict = {
                "account_id": str(data.get("account")),
                "namespace": data["namespace"],
                "deployment": data["deployment"],
                "anomaly_type": data["type"],
                "start_time": start_time,
                "end_time": end_time,
                "evaluation_period": evaluation_period,
            }

            algo_cls, algo_config_cls = get_anomaly_algo(algo_nm=algo)
            config = algo_config_cls(**config_dict)
            algo_obj = algo_cls(config=config)
            try:
                response = algo_obj.process_metrics(config=config)  # Example data
                execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()
            except CancelPrediction as e:
                execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()
                logger.warning(
                    f"anomaly, failed to process request {e}",
                    extra={
                        "account": data.get("account"),
                        "deployment": data.get("deployment"),
                        "type": data.get("type"),
                        "execution_time": execution_time,
                    },
                )
                return jsonify([])
            logger.info(
                "anomaly, successfully processed request",
                extra={
                    "account": data.get("account"),
                    "deployment": data.get("deployment"),
                    "type": data.get("type"),
                    "execution_time": execution_time,
                },
            )
            return jsonify([response.to_dict()])
        except Exception:
            execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()
            logger.exception(
                "anomaly, failed to process request",
                extra={
                    "account": data.get("account"),
                    "deployment": data.get("deployment"),
                    "type": data.get("type"),
                    "execution_time": execution_time,
                },
            )
            abort(500, description="Failed to process request")


@app.route("/anomaly/detect", methods=["POST"])
def detect() -> Response:
    """
    Detect anomalies using custom PromQL query and custom time ranges.

    This endpoint allows you to:
    1. Provide a custom PromQL query instead of using templates
    2. Specify separate historical (training) and analysis (evaluation) time ranges
    3. Analyze any custom metrics, not just the predefined types

    Request body:
    {
        "account": "account-id",              # Required
        "query": "(custom prometheus query)", # Required
        "analysis_start_time": "2024-11-01T10:00:00Z",  # Required
        "analysis_end_time": "2024-11-01T11:00:00Z",    # Required
        "historical_window_hours": 24,        # Required
        "namespace": "default",               # Optional, defaults to "custom"
        "deployment": "my-app",               # Optional, defaults to "custom"
        "tenant": "tenant-id",                # Optional
        "anomaly_type": "memory",             # Optional, defaults to "custom"
                                              # Valid: memory, cpu, errorrate, latency, replicas, custom
                                              # If omitted, uses generic anomaly detection (IsolationForest only)
                                              # If specified, applies metric-specific filtering
        "algo": "ISOLATION_TREE",             # Optional, defaults from env
                                              # Valid: ISOLATION_TREE, DB_SCAN
        "step": "1m",                         # Optional, defaults to "1m"
        "include_historical_data": false      # Optional, defaults to false
                                              # If true, includes historical data points with period="historical"
    }
    """
    acquired = _anomaly_semaphore.acquire(timeout=30)
    if not acquired:
        abort(503, description="Server busy processing another anomaly request. Please retry.")
    try:
        return _detect_inner()
    finally:
        _anomaly_semaphore.release()


def _detect_inner() -> Response:
    with get_trace(__name__).start_as_current_span("anomaly_detect"):
        data = dict(request.get_json(force=True))

        # Validate required fields
        # Note: namespace and deployment are optional (inferred from query or defaulted)
        _validate_required_field(data, "account", "Please provide value for account id")
        _validate_required_field(data, "query", "Please provide a custom PromQL query")
        _validate_required_field(data, "analysis_start_time", "Please provide analysis_start_time")
        _validate_required_field(data, "analysis_end_time", "Please provide analysis_end_time")

        # Validate historical window
        historical_window_hours = data.get("historical_window_hours")
        if historical_window_hours is None:
            abort(400, description="Please provide historical_window_hours")

        try:
            historical_window_hours = int(historical_window_hours)
            if historical_window_hours <= 0:
                abort(400, description="historical_window_hours must be greater than 0")
        except (ValueError, TypeError):
            abort(400, description="historical_window_hours must be a valid integer")

        current_span = trace.get_current_span()
        current_span.set_attribute("Deployment", str(data.get("deployment")))
        current_span.set_attribute("Namespace", str(data.get("namespace")))
        current_span.set_attribute("Account", str(data.get("account")))
        current_span.set_attribute("query_provided", True)
        current_span.set_attribute("historical_window_hours", historical_window_hours)

        # Parse timestamps
        analysis_start_time = _parse_timestamp(data.get("analysis_start_time"), "analysis_start_time")
        analysis_end_time = _parse_timestamp(data.get("analysis_end_time"), "analysis_end_time")

        # Type guard - ensure timestamps are not None (validation happens in _parse_timestamp)
        if analysis_start_time is None or analysis_end_time is None:
            abort(400, description="Failed to parse timestamps")

        # Validate time range
        _validate_time_range(analysis_start_time, analysis_end_time)

        # Calculate historical start time
        historical_start_time = analysis_end_time - datetime.timedelta(hours=historical_window_hours)

        algo: str | None = data.get("algo")
        if algo is None or not algo:
            algo = ANOMALY_ALGO

        step: str | None = data.get("step")

        include_historical_data: bool = data.get("include_historical_data", False)

        start_execution_time = datetime.datetime.now()
        try:
            # Get anomaly type (optional for custom queries)
            # If not provided, defaults to "custom" which skips metric-specific filtering
            anomaly_type = data.get("anomaly_type")
            if not anomaly_type:
                logger.info(
                    "anomaly_detect: No anomaly_type provided, using 'custom' (no metric-specific filtering)",
                    extra={
                        "account": data.get("account"),
                        "deployment": data.get("deployment"),
                    },
                )
                anomaly_type = "custom"  # Default to custom - uses generic anomaly scoring only

            # Build configuration for anomaly detection
            # We only set evaluation_period to None to avoid the buggy filter in apply_evaluation_period_filter
            # Instead, we'll manually filter the results after detection to only show evaluation period anomalies
            config_dict = {
                "account_id": str(data.get("account")),
                "namespace": data.get("namespace", "custom"),
                "deployment": data.get("deployment", "custom"),
                "anomaly_type": anomaly_type,
                "start_time": historical_start_time,
                "end_time": analysis_end_time,  # Full time range (historical + analysis)
                "evaluation_period": None,  # Skip algorithm's evaluation period filter (has a bug)
                "step": step,
            }

            algo_cls, algo_config_cls = get_anomaly_algo(algo_nm=algo)
            config = algo_config_cls(**config_dict)
            algo_obj = algo_cls(config=config)

            # Fetch all data from historical start to analysis end in one call
            # This ensures we have continuous data for both training and evaluation periods
            logger.info(
                f"anomaly_detect: Fetching data from {historical_start_time} to {analysis_end_time}",
                extra={
                    "account": data.get("account"),
                    "deployment": data.get("deployment"),
                    "historical_window_hours": historical_window_hours,
                    "analysis_period": f"{analysis_start_time} to {analysis_end_time}",
                },
            )

            all_data_result = algo_obj.load_data_from_prometheus(
                account_id=str(data.get("account")),
                query=data["query"],
                start_time=historical_start_time,
                end_time=analysis_end_time,
                step=step,
            )

            # Unpack data and labels from the result
            all_data, promql_labels = all_data_result

            if all_data is None or all_data.empty:
                logger.warning(
                    "anomaly_detect: No data returned from Prometheus for custom query",
                    extra={
                        "account": data.get("account"),
                        "deployment": data.get("deployment"),
                        "query_provided": True,
                        "time_range": f"{historical_start_time} to {analysis_end_time}",
                    },
                )
                abort(400, description="No data found for the provided query and time range")

            # Validate we have data on both sides of the split
            training_data, evaluation_data = _validate_data_split(
                all_data, analysis_start_time, analysis_end_time, data.get("account"), data.get("deployment")
            )

            logger.info(
                "anomaly_detect: Data validated and ready for anomaly detection",
                extra={
                    "account": data.get("account"),
                    "deployment": data.get("deployment"),
                    "training_data_points": len(training_data),
                    "evaluation_data_points": len(evaluation_data),
                    "total_data_points": len(all_data),
                },
            )

            try:
                # Run anomaly detection on all data
                response = algo_obj.process_metrics(config=config, data=all_data)
                execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()

                # Manually filter to only evaluation period since algorithm's filter has a bug
                _filter_and_mark_periods(
                    response,
                    analysis_start_time,
                    analysis_end_time,
                    include_historical_data,
                    data.get("account"),
                    data.get("deployment"),
                )

                # Add the query and PromQL labels to the response for traceability
                if response:
                    response.query = data["query"]
                    response.labels = promql_labels

                logger.info(
                    "anomaly_detect: Successfully processed request",
                    extra={
                        "account": data.get("account"),
                        "deployment": data.get("deployment"),
                        "execution_time": execution_time,
                        "has_anomaly": response.has_anomaly if response else False,
                    },
                )

                return jsonify([response.to_dict()])

            except CancelPrediction as e:
                execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()
                logger.warning(
                    f"anomaly_detect: Failed to process request - {e}",
                    extra={
                        "account": data.get("account"),
                        "deployment": data.get("deployment"),
                        "execution_time": execution_time,
                    },
                )
                return jsonify([])

        except CancelPrediction as e:
            execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()
            logger.warning(
                f"anomaly_detect: Failed to fetch data - {e}",
                extra={
                    "account": data.get("account"),
                    "deployment": data.get("deployment"),
                    "execution_time": execution_time,
                },
            )
            return jsonify([])

        except Exception:
            execution_time = (datetime.datetime.now() - start_execution_time).total_seconds()
            logger.exception(
                "anomaly_detect: Unexpected error",
                extra={
                    "account": data.get("account"),
                    "deployment": data.get("deployment"),
                    "execution_time": execution_time,
                },
            )
            abort(500, description="Failed to process request")
