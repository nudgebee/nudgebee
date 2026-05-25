import json
import logging
import logging.config
import os

from pythonjsonlogger.jsonlogger import JsonFormatter


class CleanJsonFormatter(JsonFormatter):
    """JSON formatter that strips uvicorn's color_message field."""

    def add_fields(self, log_record, record, message_dict):
        super().add_fields(log_record, record, message_dict)
        log_record.pop("color_message", None)


class _UvicornAccessFilter(logging.Filter):
    """Drop h11 HTTP access log lines from uvicorn.error logger."""

    def filter(self, record: logging.LogRecord) -> bool:
        return "HTTP/" not in record.getMessage()


def _apply_uvicorn_filter():
    """Apply access log filter to uvicorn.error after dictConfig (which wipes filters)."""
    uvicorn_logger = logging.getLogger("uvicorn.error")
    if not any(isinstance(f, _UvicornAccessFilter) for f in uvicorn_logger.filters):
        uvicorn_logger.addFilter(_UvicornAccessFilter())


def setup_logger() -> None:
    # Get the path to the logging config file from the environment variable
    config_file_path = os.getenv("LOGGING_CONFIG_FILE")
    print(f"LOGGING_CONFIG_FILE: {config_file_path}")  # Debugging to see the file path

    if config_file_path:
        try:
            # Open and read the JSON configuration file
            with open(config_file_path, "rt") as f:
                config = json.load(f)
                logging.config.dictConfig(config)
                logging.info("Logging configuration loaded successfully.")
        except json.JSONDecodeError as e:
            logging.warn(f"Error decoding JSON from file: {e}")
            logging.info("Loading default logging configuration.")
            load_default_config()
        except Exception as e:
            logging.warn(f"Error reading the logging configuration file: {e}")
            logging.info("Loading default logging configuration.")
            load_default_config()
    else:
        logging.warn(
            "LOGGING_CONFIG_FILE environment variable not set. Loading default configuration."
        )
        load_default_config()

    _apply_uvicorn_filter()


def load_default_config():
    # If the environment variable is not set, fallback to file-based config
    basedir = os.path.abspath(os.path.dirname(__file__))
    config_file = os.path.join(basedir, "logging.json")

    try:
        with open(config_file, "rt") as f:
            config = json.load(f)
            # Override log level from env var if set
            env_level = os.getenv("LOG_LEVEL", "").upper()
            if env_level:
                for handler in config.get("handlers", {}).values():
                    handler["level"] = env_level
                for lgr in config.get("loggers", {}).values():
                    lgr["level"] = env_level
            logging.config.dictConfig(config)
            logging.info("Default logging configuration loaded successfully.")
    except Exception as e:
        logging.basicConfig(level=logging.INFO)
        logging.warn(f"Failed to load default logging config: {e}")


def get_log_config_path():
    # Get the path to the logging config file from the environment variable
    config_file_path = os.getenv("LOGGING_CONFIG_FILE")
    if not config_file_path:
        basedir = os.path.abspath(os.path.dirname(__file__))
        config_file_path = os.path.join(basedir, "logging.json")
        logging.warn(
            f"Logging file path : {config_file_path} (default used due to env var not set)"
        )
    return config_file_path
