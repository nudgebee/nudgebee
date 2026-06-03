import logging
from typing import Dict, Any

import rabbitmq
from config import Configs
from controllers.base import BaseController

logger = logging.getLogger(__name__)


class SpendController(BaseController):
    def post_async(self, tenant: str, cloud_account_id: str, data: Dict[str, Any]) -> None:
        logger.info(
            "got spend data for tenant %s, cloud_account_id %s",
            tenant,
            cloud_account_id,
        )
        data["tenant"] = tenant
        data["cloud_account_id"] = cloud_account_id
        rabbitmq.rabbitmq_client.publish_message(
            Configs.RABBIT_MQ_DISCOVERY_EXCHANGE, Configs.RABBIT_MQ_SPEND_QUEUE, data
        )
