import glob
import logging
import os
import sys
import threading

from dotenv import load_dotenv
from flask import Flask

from server import setup_logger
from server.controllers import health
from server.utils.utils import QueueConfig, set_global_trace, set_metrics_exporter

# Early logging configuration to capture initialization errors
logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(name)s - %(levelname)s - %(message)s")
logger = logging.getLogger(__name__)

load_dotenv()

try:
    ML_SERVER_PORT = int(os.environ.get("ML_PORT", 9999))
except ValueError:
    logger.error(f"Invalid ML_PORT environment variable: {os.environ.get('ML_PORT')}. " "Defaulting to 9999.")
    ML_SERVER_PORT = 9999

# RabbitMQ consumer is started via gunicorn.conf.py post_fork hook
# so only one Gunicorn worker runs the consumer (isolating TF-heavy
# recommendation work from other HTTP-serving workers).

os.environ["TF_ENABLE_ONEDNN_OPTS"] = "0"

try:
    set_global_trace()
    setup_logger()
    set_metrics_exporter()

    app = Flask(__name__)
    app.config["TIMEOUT"] = 600

    basedir = os.path.abspath(os.path.dirname(__file__))
    controllers = glob.glob(basedir + "/controllers/*.py")
    logger.info(f"reading basedir: {basedir}")
    logger.info(f"reading controllers: {controllers}")
    for controller in controllers:
        name = controller.split("/")[-1].split(".")[0]
        if name == "health":
            continue  # Skip registering health blueprint in main app
        module = __import__(f"server.controllers.{name}", fromlist=["app"])
        app.register_blueprint(module.app)

    health_app = Flask("health_app")
    health_app.register_blueprint(health.app)

except Exception as e:
    logger.critical(f"Failed to initialize application: {e}", exc_info=True)
    sys.exit(1)


def run_health_server():
    try:
        health_app.run(port=8081, host="0.0.0.0", debug=False, use_reloader=False)
    except Exception as e:
        logger.error(f"Health server failed: {e}", exc_info=True)


if __name__ == "__main__":
    try:
        # Start RabbitMQ consumer for local dev
        # (in production, gunicorn.conf.py handles this)
        from server.message import ml_server_message_handler
        from server.utils.rabbitmq import consume_message

        consume_message(
            QueueConfig.ML_RECOMMENDATION_EXCHANGE,
            QueueConfig.ML_RECOMMENDATION_QUEUE,
            QueueConfig.ML_RECOMMENDATION_QUEUE,
            ml_server_message_handler,
        )
        # Start health check server in a separate thread
        health_thread = threading.Thread(target=run_health_server, daemon=True)
        health_thread.start()
        app.run(port=ML_SERVER_PORT, host="0.0.0.0", debug=True, use_reloader=False)
    except Exception as e:
        logger.critical(f"Application failed to start: {e}", exc_info=True)
        sys.exit(1)
