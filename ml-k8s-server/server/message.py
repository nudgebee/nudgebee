import json
import logging
from dataclasses import asdict
from datetime import datetime, UTC
from typing import Any, Dict, List, Optional, Type
from uuid import UUID

from pydantic import BaseModel
from server.metrics.prometheus_metrics_hpa import PrometheusMetricsHPA
from server.recommendation.recommendation_db import DBRecommendation
from server.recommendation.vertical_rightsizing import generate_and_process_recommendation
from server.recommendation.volume_rightsizing import generate_volume_rightsizing_recommendations
import asyncio
from server.utils import rabbitmq
from server.utils.utils import DBConfig, QueueConfig, RecommendationConfig, get_trace, handle_df_for_nan
from server.exception import CancelPrediction

logger = logging.getLogger(__name__)


class MlMessageAbstract(BaseModel):
    action_name: str
    span_name: str
    exchange: str
    queue: str

    @property
    def message_body(self) -> Dict[str, Any]:
        return self.model_dump()

    def process(self) -> None:
        try:
            self.process_message()
        except CancelPrediction as e:
            logger.warning(f"Prediction for message {self.model_dump()} canceled with warning {e}")
        except Exception as e:
            logger.exception(f"Error in processing message {e}")
            raise e

    def process_message(self) -> None:
        raise NotImplementedError("process method is not implemented")

    def publish(self):
        message_body: Dict[str, Any] = self.message_body | {"action_name": self.action_name}
        rabbitmq.publish_message(
            exchange_name=self.exchange,
            routing_key=self.queue,
            message=json.loads(json.dumps(message_body, default=str)),
        )


class SyncRecommendationData(MlMessageAbstract):
    """Ml server sync message object
    sample message
    {"recommendation_id":"0fb390cd-262b-4996-a20e-0a18fd199965","action_name":"ml_recomm_sync"}
    Args:
        AutopilotMessageAbstract (_type_): _description_

    Returns:
        _type_: _description_
    """

    action_name: str = "ml_recomm_sync"
    span_name: str = "ml_recommendation_sync"
    recommendation_id: UUID
    exchange: str = QueueConfig.ML_RECOMMENDATION_QUEUE
    queue: str = QueueConfig.ML_RECOMMENDATION_EXCHANGE

    def process_message(self):
        logger.info(f"Syncing recomm {self.recommendation_id}")
        with get_trace(__name__).start_as_current_span("update_recommendations"):
            db_recommendation = DBRecommendation(
                url=DBConfig.url,
                table=RecommendationConfig.table_name,
                account_id="",
                tenant_id="",
                resource_id="",
                deployment_name="",
                namespace="",
                persist_recommendation=False,
            )
            recommendations = db_recommendation.get_recommendations(recommendation_ids=[self.recommendation_id])
            # Lazy-load HPAModel to avoid TensorFlow initialization at worker startup
            from server.model.hpa_model import HPAModel

            for row in recommendations.itertuples(index=True, name="Pandas"):
                namespace = getattr(row, "qualified_name").split("/")[0]
                deployment = getattr(row, "name")
                tenant_id = str(getattr(row, "tenant_id"))
                account_id = str(getattr(row, "account_id"))
                resource_id = str(getattr(row, "resource_id"))
                recommendation = getattr(row, "recommendation")
                model = HPAModel(
                    tenant=tenant_id,
                    account=account_id,
                    namespace=namespace,
                    deployment=deployment,
                    resource_id=resource_id,
                )
                from_timestamp = recommendation.get("recommendation", {}).get("last_timestamp")
                if from_timestamp and model.is_model_present():
                    from_timestamp = datetime.strptime(from_timestamp, "%Y-%m-%dT%H:%M:%SZ").astimezone(tz=UTC)
                else:
                    from_timestamp = None
                logger.info(
                    (
                        f"Updating recommendation for {deployment} in {namespace}, account: {account_id},"
                        f"from: {from_timestamp}"
                    )
                )
                prometheus_instance = PrometheusMetricsHPA(
                    namespace_name=namespace,
                    deployment_name=deployment,
                    account_id=account_id,
                    from_time=from_timestamp,
                )
                dt = prometheus_instance.get_metrics()
                if dt.empty:
                    logger.error(f"No data found for {deployment} in {namespace}, account: {account_id}")
                    continue
                metrics_df = handle_df_for_nan(dt)
                predictions = model.get_predictions(data=metrics_df)
                predictions = predictions.reset_index(drop=True)
                if len(metrics_df) > 24 * 14:
                    metrics_df = metrics_df.tail(24 * 14)
                original_data = metrics_df[["timestamp", "replicas"]]
                last_timestamp = metrics_df["timestamp"].max()
                evidence = metrics_df[["timestamp", "replicas", "rps", "latency", "memory", "cpu"]]
                # Filter the data to get the last 24*14 data points if present
                predictions_data = predictions[["inference_time", "replicas"]]
                predictions_data_ = predictions_data.rename(columns={"inference_time": "timestamp"})
                # predictions.to_csv("output_output.csv", index=False, encoding="utf-8")
                db_recommendation = DBRecommendation(
                    url=DBConfig.url,
                    table=RecommendationConfig.table_name,
                    account_id=account_id,
                    tenant_id=tenant_id,
                    resource_id=resource_id,
                    deployment_name=deployment,
                    namespace=namespace,
                    persist_recommendation=True,
                )
                db_recommendation.generate_recommendation(
                    original_data=original_data,
                    predictions_data=predictions_data_,
                    evidences=evidence,
                    last_timestamp=last_timestamp,
                )

    @property
    def message_body(self) -> Dict[str, Any]:
        # need only action name which will be added by parent class in publish method
        return {
            "recommendation_id": self.recommendation_id,
        }


class KrrMessageHandler(MlMessageAbstract):
    """Ml server create krr message object
    sample message
    {
    "account_id": "a2a30b02-0f67-42e5-a2ab-c658230fd798",
    "tenant_id": "890cad87-c452-4aa7-b84a-742cee0454a1"
    }
    Args:
        AutopilotMessageAbstract (_type_): _description_

    Returns:
        _type_: _description_
    """

    action_name: str = "vertical_rightsize_update"
    span_name: str = "vertical_rightsize_update"
    account_id: UUID
    tenant_id: UUID
    exchange: str = QueueConfig.ML_RECOMMENDATION_QUEUE
    queue: str = QueueConfig.ML_RECOMMENDATION_EXCHANGE

    def process_message(self):
        logger.info(f"Creating KRR for account {self.account_id} and tenant {self.tenant_id}")
        with get_trace(__name__).start_as_current_span("Update_krr_recommendations"):
            asyncio.run(
                generate_and_process_recommendation(tenant_id=str(self.tenant_id), account_id=str(self.account_id))
            )

    @property
    def message_body(self) -> Dict[str, Any]:
        # need only action name which will be added by parent class in publish method
        return {
            "account_id": self.account_id,
            "tenant_id": self.tenant_id,
        }


class VerticalRightsizingRequest(MlMessageAbstract):
    """Message handler for vertical rightsizing API requests.

    This allows the API endpoint to publish a message to RabbitMQ
    and have a consumer process the request asynchronously.

    sample message:
    {
        "account_id": "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        "tenant_id": "890cad87-c452-4aa7-b84a-742cee0454a1",
        "namespace": "default",
        "resource_names": ["deployment-1", "deployment-2"],
        "persist_recommendation": true,
        "batch_by_namespace": true,
        "max_recommendations": 100
    }
    """

    action_name: str = "vertical_rightsizing_request"
    span_name: str = "vertical_rightsizing_request"
    account_id: str
    tenant_id: str
    namespace: Optional[str] = None
    resource_names: Optional[List[str]] = None
    persist_recommendation: bool = False
    batch_by_namespace: bool = True
    max_recommendations: Optional[int] = None
    metrics_provider: Optional[str] = None
    datadog_api_key: Optional[str] = None
    datadog_app_key: Optional[str] = None
    datadog_site: Optional[str] = None
    exchange: str = QueueConfig.ML_RECOMMENDATION_EXCHANGE
    queue: str = QueueConfig.ML_RECOMMENDATION_QUEUE

    def process_message(self):
        logger.info(
            f"Processing vertical rightsizing request for account {self.account_id}, "
            f"tenant {self.tenant_id}, namespace {self.namespace}"
        )
        with get_trace(__name__).start_as_current_span("vertical_rightsizing_consumer"):
            result = asyncio.run(
                generate_and_process_recommendation(
                    account_id=self.account_id,
                    tenant_id=self.tenant_id,
                    namespace=self.namespace,
                    resource_names=self.resource_names,
                    persist_recommendation=self.persist_recommendation,
                    batch_by_namespace=self.batch_by_namespace,
                    max_recommendations=self.max_recommendations,
                    metrics_provider=self.metrics_provider,
                    datadog_api_key=self.datadog_api_key,
                    datadog_app_key=self.datadog_app_key,
                    datadog_site=self.datadog_site,
                )
            )
            logger.info(
                f"Vertical rightsizing completed for account {self.account_id}: "
                f"generated {result.recommendations_generated} recommendations"
            )

    @property
    def message_body(self) -> Dict[str, Any]:
        return {
            "account_id": self.account_id,
            "tenant_id": self.tenant_id,
            "namespace": self.namespace,
            "resource_names": self.resource_names,
            "persist_recommendation": self.persist_recommendation,
            "batch_by_namespace": self.batch_by_namespace,
            "max_recommendations": self.max_recommendations,
            "metrics_provider": self.metrics_provider,
            "datadog_api_key": self.datadog_api_key,
            "datadog_app_key": self.datadog_app_key,
            "datadog_site": self.datadog_site,
        }


class VolumeRightsizingRequest(MlMessageAbstract):
    """Message handler for volume rightsizing API requests.

    This allows the API endpoint to publish a message to RabbitMQ
    and have a consumer process the request asynchronously.

    sample message:
    {
        "account": "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        "tenant": "890cad87-c452-4aa7-b84a-742cee0454a1",
        "namespace": "default",
        "persist_recommendation": true,
        "max_recommendations": 100
    }
    """

    action_name: str = "volume_rightsizing_request"
    span_name: str = "volume_rightsizing_request"
    account: str
    tenant: str
    namespace: Optional[str] = None
    persist_recommendation: bool = False
    max_recommendations: Optional[int] = None
    metrics_provider: Optional[str] = None
    datadog_api_key: Optional[str] = None
    datadog_app_key: Optional[str] = None
    datadog_site: Optional[str] = None
    exchange: str = QueueConfig.ML_RECOMMENDATION_EXCHANGE
    queue: str = QueueConfig.ML_RECOMMENDATION_QUEUE

    def process_message(self):
        logger.info(
            f"Processing volume rightsizing request for account {self.account}, "
            f"tenant {self.tenant}, namespace {self.namespace}"
        )
        with get_trace(__name__).start_as_current_span("volume_rightsizing_consumer"):
            result = asyncio.run(
                generate_volume_rightsizing_recommendations(
                    account_id=self.account,
                    tenant_id=self.tenant,
                    namespace=self.namespace,
                    persist_recommendation=self.persist_recommendation,
                    max_recommendations=self.max_recommendations,
                    metrics_provider=self.metrics_provider,
                    datadog_api_key=self.datadog_api_key,
                    datadog_app_key=self.datadog_app_key,
                    datadog_site=self.datadog_site,
                )
            )
            logger.info(
                f"Volume rightsizing completed for account {self.account}: "
                f"generated recommendations - {asdict(result)}"
            )

    @property
    def message_body(self) -> Dict[str, Any]:
        return {
            "account": self.account,
            "tenant": self.tenant,
            "namespace": self.namespace,
            "persist_recommendation": self.persist_recommendation,
            "max_recommendations": self.max_recommendations,
            "metrics_provider": self.metrics_provider,
            "datadog_api_key": self.datadog_api_key,
            "datadog_app_key": self.datadog_app_key,
            "datadog_site": self.datadog_site,
        }


def ml_server_message_handler(body: str) -> None:
    parsed_message: Dict[str, Any] = json.loads(body)
    message_mapping: Dict[str, Type[MlMessageAbstract]] = {
        "ml_recomm_sync": SyncRecommendationData,
        "vertical_rightsize_update": KrrMessageHandler,
        "vertical_rightsizing_request": VerticalRightsizingRequest,
        "volume_rightsizing_request": VolumeRightsizingRequest,
    }
    action_name: str | None = parsed_message.get("action_name")
    if action_name:
        msg_obj = message_mapping.get(action_name)
        if msg_obj:
            msg_obj(**parsed_message).process()
        else:
            raise ValueError(f"Invalid message {parsed_message}")
    else:
        raise ValueError(f"Invalid message action name not present {parsed_message}")
