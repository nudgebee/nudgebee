import json
import logging.config
import os
import signal
import sys
import time

from flask import Flask, Response
from flask_restx import Api
from prometheus_client import generate_latest, CONTENT_TYPE_LATEST

from apis import register_urls

with open("config/logging.json", "rt") as f:
    logging_config = json.load(f)

logging.config.dictConfig(logging_config)

# Determine application environment - default to Production for security
ENV_MAPPING = {
    "DEV": "Debug",
    "DEVELOPMENT": "Debug",
    "PROD": "Production",
    "PRODUCTION": "Production",
}
APP_ENV = ENV_MAPPING.get(os.getenv("ENV", "PROD").upper(), "Production")

api = Api(doc="/" if APP_ENV == "Debug" else False)

DEFAULT_PORT = 5000
DEFAULT_HOST = "0.0.0.0"


class AppConfig:
    PORT = int(os.getenv("SERVICE_PORT", DEFAULT_PORT))
    HOST = os.getenv("SERVICE_HOST", DEFAULT_HOST)


class DevConfig(AppConfig):
    DEBUG = True
    TESTING = True


class ProdConfig(AppConfig):
    DEBUG = False
    TESTING = False


config_dict = {"Production": ProdConfig, "Debug": DevConfig}


def register_extensions(flask_app):
    api.init_app(flask_app)


def register_blueprints(flask_app) -> None:
    for route in register_urls.URL_CONFIG.urls:
        api.add_resource(route.view, route.url_prefix)


def create_app():
    flask_app = Flask(__name__)
    flask_app.config.from_object(config_dict[APP_ENV])
    register_extensions(flask_app)
    register_blueprints(flask_app)

    # Add Prometheus metrics endpoint
    @flask_app.route("/metrics")
    def metrics():
        """Prometheus metrics endpoint"""
        return Response(generate_latest(), mimetype=CONTENT_TYPE_LATEST)

    return flask_app


def signal_handler_exit(sig, frame):
    logging.info("handler called with signal %s", sig)
    sys.exit(0)


def start_worker_mode():
    """Start worker mode - only consumes messages, no HTTP server"""
    logging.info("Starting worker mode - consuming messages only")

    # Register consumers explicitly
    from rabbitmq.rabbitmq_consumers import register_all_consumers

    register_all_consumers()

    # Keep worker process alive
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        logging.info("Worker mode shutting down")
        sys.exit(0)


def start_server_mode():
    """Start server mode - HTTP server only, no message consumption"""
    logging.info("Starting server mode - HTTP server only")

    flask_app = create_app()
    flask_app.run(
        host=flask_app.config.get("HOST", "0.0.0.0"),
        port=flask_app.config.get("PORT", 5000),
    )


def create_server_app():
    """Create app instance for server mode (used by gunicorn)"""
    return create_app()


def create_combined_app():
    """Create app instance with consumers for combined mode"""
    from rabbitmq.rabbitmq_consumers import register_all_consumers

    register_all_consumers()
    return create_app()


# Create the app instance for gunicorn based on COLLECTOR_MODE environment variable
collector_mode = os.getenv("COLLECTOR_MODE", "both")
if collector_mode == "server":
    app = create_server_app()  # Server mode - HTTP only
else:  # worker or both (default)
    app = create_combined_app()  # Combined mode with HTTP + message consumers

if __name__ == "__main__":
    signal.signal(signal.SIGINT, signal_handler_exit)
    signal.signal(signal.SIGTERM, signal_handler_exit)

    collector_mode = os.getenv("COLLECTOR_MODE", "both")
    if collector_mode == "server":
        # Server mode - HTTP only (no consumers)
        app = create_server_app()
        app.run(
            host=app.config.get("HOST", "0.0.0.0"),
            port=app.config.get("PORT", 5000),
        )
    elif collector_mode == "worker":
        start_worker_mode()
    else:  # both
        # Combined mode - HTTP + consumers
        app = create_combined_app()
        app.run(
            host=app.config.get("HOST", "0.0.0.0"),
            port=app.config.get("PORT", 5000),
        )
