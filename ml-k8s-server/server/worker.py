"""Entry point for the :8081 gunicorn, which serves /health and hosts the pod's only queue consumer.

That gunicorn runs with --workers 1, which is what keeps it to one consumer, and so one scan, per pod.
The consumer starts when this module is imported, so never serve it with --preload: the thread would
start in the gunicorn master and not survive the fork into the worker.
"""

import logging

from server.app import health_app
from server.message import ml_server_message_handler
from server.utils.rabbitmq import consume_message
from server.utils.utils import QueueConfig

__all__ = ["health_app"]

logger = logging.getLogger(__name__)

consume_message(
    QueueConfig.ML_RECOMMENDATION_EXCHANGE,
    QueueConfig.ML_RECOMMENDATION_QUEUE,
    QueueConfig.ML_RECOMMENDATION_QUEUE,
    ml_server_message_handler,
)
