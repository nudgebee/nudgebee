import json
import logging
from typing import Optional, Dict, Any

import rabbitmq.rabbitmq_client
from controllers.base import BaseController
from config import Configs
from handlers.event_handler import run_event_handler_async

logger = logging.getLogger(__name__)


class EventController(BaseController):
    # assuming account id for dev
    def __init__(self):
        super().__init__()
        self.account: Optional[str] = None
        self.data: Optional[Dict[str, Any]] = None

    def post_async(self, tenant: str, cloud_account_id: str, data: Dict[str, Any]) -> None:
        data["tenant"] = tenant
        data["cloud_account_id"] = cloud_account_id
        rabbitmq.rabbitmq_client.publish_message(Configs.RABBIT_MQ_EVENT_EXCHANGE, Configs.RABBIT_MQ_EVENT_QUEUE, data)

    def post(self, tenant: str, cloud_account_id: str, data: Dict[str, Any]) -> None:
        data["tenant"] = tenant
        data["cloud_account_id"] = cloud_account_id
        run_event_handler_async(json.dumps(data))
