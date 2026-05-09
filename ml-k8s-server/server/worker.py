import logging

from server.message import ml_server_message_handler
from server.utils.rabbitmq import consume_message
from server.utils.utils import QueueConfig

logger = logging.getLogger(__name__)

consume_message(
    QueueConfig.ML_RECOMMENDATION_EXCHANGE,
    QueueConfig.ML_RECOMMENDATION_QUEUE,
    QueueConfig.ML_RECOMMENDATION_QUEUE,
    ml_server_message_handler,
)
