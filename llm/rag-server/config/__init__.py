import json
import logging
import logging.config
import os

from opentelemetry import trace


class TraceIdFilter(logging.Filter):
    """Inject the active OTel trace_id / span_id (bare 32/16-hex, matching the
    Go services and Loki `trace_id` queries) into every LogRecord. Empty when no
    span is active."""

    def filter(self, record: logging.LogRecord) -> bool:
        ctx = trace.get_current_span().get_span_context()
        record.trace_id = format(ctx.trace_id, "032x") if ctx.trace_id else ""
        record.span_id = format(ctx.span_id, "016x") if ctx.span_id else ""
        return True


def setup_logger() -> None:
    # Get the path to the logging config file from the environment variable
    config_file_path = os.getenv("LOGGING_CONFIG_FILE")

    if config_file_path:
        try:
            # Open and read the JSON configuration file
            with open(config_file_path, "rt") as f:
                config = json.load(f)
                logging.config.dictConfig(config)
                logging.debug("LOGGING_CONFIG_FILE: %s", config_file_path)
                logging.info("Logging configuration loaded successfully.")
        except json.JSONDecodeError as e:
            logging.warning(f"Error decoding JSON from file: {e}")
            logging.info("Loading default logging configuration.")
            load_default_config()
        except Exception as e:
            logging.warning(f"Error reading the logging configuration file: {e}")
            logging.info("Loading default logging configuration.")
            load_default_config()
    else:
        load_default_config()


def load_default_config():
    # If the environment variable is not set, fallback to file-based config
    basedir = os.path.abspath(os.path.dirname(__file__))
    config_file = os.path.join(basedir, "logging.json")

    try:
        with open(config_file, "rt") as f:
            config = json.load(f)
            logging.config.dictConfig(config)
    except Exception as e:
        logging.error(f"Error reading the logging configuration: {e}")
        raise
