import logging
from dataclasses import asdict

from flask import Blueprint, Response, jsonify, abort, request

from server.message import VerticalRightsizingRequest, VolumeRightsizingRequest
from server.controllers.models.cluster_rightsizing_model import ClusterRightsizingRequest
from server.recommendation.cluster_rightsizing_recommendation import ClusterRightSizingRecommendation
from server.utils.utils import get_trace, DBConfig

from opentelemetry import trace

app = Blueprint("rightsizing", __name__)
logger = logging.getLogger(__name__)


@app.route("/rightsizing/cluster", methods=["POST"])
def cluster_rightsizing() -> Response:
    with get_trace(__name__).start_as_current_span("metrics"):
        data = dict(request.get_json(force=True))
        if not data.get("account"):
            abort(400, description="Please provide value for account id")
        if not data.get("tenant"):
            abort(400, description="Please provide value for tenant id")
        current_span = trace.get_current_span()
        current_span.set_attribute("Account", str(data.get("account")))
        try:
            cluster_rightsizing = ClusterRightSizingRecommendation(
                account_id=data["account"],
                tenant_id=data["tenant"],
                url=DBConfig.url,
                persist_recommendation=data.get("persist_recommendation", False),
            )
            cluster_rightsizing_request = ClusterRightsizingRequest(
                region=str(data.get("region")),
                buffer_percentage=int(data.get("buffer_percentage", 10)),
                number_of_recommendations=data.get("number_of_recommendations", 1),
                min_nodes=data.get("min_nodes", 2),
                min_cpu_per_node=data.get("min_cpu_per_node", 2),
                min_memory_per_node=data.get("min_memory_per_node", 4),
                preferred_instance_groups=data.get("preferred_instance_groups", ["m", "c", "r", "t"]),
                graviton=data.get("graviton", True),
            )
            recommendation = cluster_rightsizing.generate_recommendation(cluster_rightsizing_request)
            return jsonify(asdict(recommendation))
        except Exception as e:
            logger.exception(e)
            abort(500, description=e)


@app.route("/rightsizing/vertical", methods=["POST"])
def vertical_rightsizing() -> Response:
    """Vertical rightsizing endpoint - publishes request to RabbitMQ for async processing."""
    with get_trace(__name__).start_as_current_span("vertical_rightsizing_publish"):
        data = dict(request.get_json(force=True))
        if not data.get("account_id"):
            abort(400, description="Please provide value for account id")
        if not data.get("tenant_id"):
            abort(400, description="Please provide value for tenant id")
        current_span = trace.get_current_span()
        current_span.set_attribute("Account", str(data.get("account_id")))
        try:
            # Create message and publish to RabbitMQ
            message = VerticalRightsizingRequest(
                account_id=data["account_id"],
                tenant_id=data["tenant_id"],
                namespace=data.get("namespace"),
                resource_names=data.get("resource_names"),
                persist_recommendation=data.get("persist_recommendation", False),
                batch_by_namespace=data.get("batch_by_namespace", True),
                max_recommendations=data.get("max_recommendations"),
                metrics_provider=data.get("metrics_provider"),
                datadog_api_key=data.get("datadog_api_key"),
                datadog_app_key=data.get("datadog_app_key"),
                datadog_site=data.get("datadog_site"),
            )
            message.publish()
            logger.info(f"Published vertical rightsizing request for account {data['account_id']}")
            return jsonify(
                {
                    "status": "accepted",
                    "message": "Vertical rightsizing request queued for processing",
                    "account_id": data["account_id"],
                    "tenant_id": data["tenant_id"],
                    "namespace": data.get("namespace"),
                }
            )
        except ValueError as e:
            # Pydantic's ValidationError inherits from ValueError
            # Return 400 Bad Request for invalid request data
            logger.warning(f"Invalid request data: {e}")
            abort(400, description=str(e))
        except Exception as e:
            logger.exception(e)
            abort(500, description=str(e))


@app.route("/rightsizing/volume", methods=["POST"])
def volume_rightsizing() -> Response:
    """Volume rightsizing endpoint - publishes request to RabbitMQ for async processing."""
    with get_trace(__name__).start_as_current_span("volume_rightsizing_publish"):
        data = dict(request.get_json(force=True))
        if not data.get("account"):
            abort(400, description="Please provide value for account id")
        if not data.get("tenant"):
            abort(400, description="Please provide value for tenant id")

        current_span = trace.get_current_span()
        current_span.set_attribute("Account", str(data.get("account")))
        current_span.set_attribute("Tenant", str(data.get("tenant")))

        try:
            # Create message and publish to RabbitMQ
            message = VolumeRightsizingRequest(
                account=data["account"],
                tenant=data["tenant"],
                namespace=data.get("namespace"),
                persist_recommendation=data.get("persist_recommendation", False),
                max_recommendations=data.get("max_recommendations"),
                metrics_provider=data.get("metrics_provider"),
                datadog_api_key=data.get("datadog_api_key"),
                datadog_app_key=data.get("datadog_app_key"),
                datadog_site=data.get("datadog_site"),
            )
            message.publish()
            logger.info(f"Published volume rightsizing request for account {data['account']}")
            return jsonify(
                {
                    "status": "accepted",
                    "message": "Volume rightsizing request queued for processing",
                    "account": data["account"],
                    "tenant": data["tenant"],
                    "namespace": data.get("namespace"),
                }
            )
        except ValueError as e:
            # Pydantic's ValidationError inherits from ValueError
            # Return 400 Bad Request for invalid request data
            logger.warning(f"Invalid request data: {e}")
            abort(400, description=str(e))
        except Exception as e:
            logger.exception(e)
            abort(500, description=str(e))
