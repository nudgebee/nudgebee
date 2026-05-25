import logging.config
import json
import os


def setup_logger() -> None:
    basedir = os.path.abspath(os.path.dirname(__file__))
    config_file = os.path.join(basedir, "logging.json")
    with open(config_file, "rt") as f:
        config = json.load(f)
        logging.config.dictConfig(config)
