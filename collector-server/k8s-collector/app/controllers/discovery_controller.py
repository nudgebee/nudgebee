import logging
from typing import Dict, Any

from config import Configs
from controllers.base import BaseController
from handlers.discovery_handler import get_active_resources
from rabbitmq.rabbitmq_client import publish_message

logger = logging.getLogger(__name__)


class DiscoveryController(BaseController):
    def post_async(self, tenant: str, cloud_account_id: str, data: Dict[str, Any]) -> None:
        data["tenant"] = tenant
        data["cloud_account_id"] = cloud_account_id
        publish_message(Configs.RABBIT_MQ_DISCOVERY_EXCHANGE, Configs.RABBIT_MQ_DISCOVERY_QUEUE, data)

    def get_active_resources(self, tenant: str, cloud_account_id: str, filters: Dict[str, Any]):
        return get_active_resources(tenant, cloud_account_id, filters)
