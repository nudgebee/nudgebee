import json
import logging
import warnings

from flask import Blueprint, abort, request
from opentelemetry import trace
from pandas.errors import SettingWithCopyWarning
from server.metrics.prometheus_metrics import Prometheus
from server.utils.utils import get_trace

warnings.simplefilter(action="ignore", category=SettingWithCopyWarning)

logger = logging.getLogger(__name__)
app = Blueprint("metrics", __name__)


@app.route("/metrics", methods=["POST"])
def fetch_metrics():
    with get_trace(__name__).start_as_current_span("metrics"):
        data = dict(request.get_json(force=True))
        if not data.get("deployment"):
            abort(400, description="Please provide value for deployment name")
        if not data.get("namespace"):
            abort(400, description="Please provide value for namespace name")
        if not data.get("account"):
            abort(400, description="Please provide value for account id")
        current_span = trace.get_current_span()
        current_span.set_attribute("Deployment", str(data.get("deployment")))
        current_span.set_attribute("Namespace", str(data.get("namespace")))
        current_span.set_attribute("Account", str(data.get("account")))
        try:
            prometheus_instance = Prometheus(
                namespace_name=data["namespace"], deployment_name=data["deployment"], account_id=data["account"]
            )
            metrics_df = prometheus_instance.get_metrics()
            metrics_df.fillna(0, inplace=True)
            metrics_df["replicas"] = metrics_df["replicas"].apply(float).apply(int)
            metrics_df["rps"] = metrics_df["rps"].apply(float).apply(int)
            metrics_df["latency"] = metrics_df["latency"].apply(float)
            metrics_df["memory"] = metrics_df["memory"].apply(float)
            metrics_df["cpu"] = metrics_df["cpu"].apply(float)
            return {"metrics": json.loads(metrics_df.to_json(orient="records"))}
        except KeyError as e:
            logger.error(f"Key error fetching metrics: {e}", exc_info=True)
            abort(500, description=f"Configuration error: Missing expected data field. {e}")
        except TypeError as e:
            logger.error(f"Type error fetching metrics: {e}", exc_info=True)
            abort(500, description=f"Data format error: Invalid data type encountered. {e}")
        except Exception as e:
            logger.error(f"An unexpected error occurred while fetching metrics: {e}", exc_info=True)
            abort(500, description=f"An unexpected error occurred: {e}")
