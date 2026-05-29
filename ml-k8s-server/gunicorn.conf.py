# added a test change which does absolutely nothing

import sys

# Track whether the RabbitMQ consumer has been started across workers.
# Only the first worker to fork should start the consumer to avoid
# duplicate message processing and to isolate TensorFlow-heavy
# recommendation work from HTTP-serving workers.
_consumer_started = False


def post_fork(server, worker):
    global _consumer_started
    if not _consumer_started:
        _consumer_started = True
        worker.is_consumer_worker = True
        server.log.info(f"Starting RabbitMQ consumer in worker {worker.pid}")

        try:
            from server.message import ml_server_message_handler
            from server.utils.rabbitmq import consume_message
            from server.utils.utils import QueueConfig

            consume_message(
                QueueConfig.ML_RECOMMENDATION_EXCHANGE,
                QueueConfig.ML_RECOMMENDATION_QUEUE,
                QueueConfig.ML_RECOMMENDATION_QUEUE,
                ml_server_message_handler,
            )
        except Exception as e:
            server.log.critical(f"Failed to start RabbitMQ consumer in worker {worker.pid}: {e}", exc_info=True)
            sys.exit(1)
    else:
        server.log.info(f"Worker {worker.pid} skipping RabbitMQ consumer " "(already started in another worker)")
