import json
import logging
import logging.config
import os


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
