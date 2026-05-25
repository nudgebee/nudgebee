from pydantic_settings import BaseSettings
import logging.config
import json
import os
from typing import ClassVar, Dict, List
from dotenv import load_dotenv

load_dotenv()


class Settings(BaseSettings):
    COLLECTOR_DB_URL: str
    NUDGEBEE_ENCRYPTION_KEY: str

    RABBIT_MQ_HOST: str
    RABBIT_MQ_PORT: str
    RABBIT_MQ_USERNAME: str
    RABBIT_MQ_PASSWORD: str

    RABBIT_MQ_EVENT_EXCHANGE: str = "k8s_agent_exchange"
    RABBIT_MQ_EVENT_QUEUE: str = "k8s_agent_events"

    NOTIFICATION_QUEUE: str = "notifications"
    NOTIFICATION_EXCHANGE: str = "notifications_exchange"

    RABBIT_MQ_DISCOVERY_EXCHANGE: str = "k8s_agent_exchange"
    RABBIT_MQ_DISCOVERY_QUEUE: str = "k8s_agent_discovery"
    RABBIT_MQ_SPEND_QUEUE: str = "k8s_agent_spend"

    RABBIT_MQ_KG_UPDATE_EXCHANGE: str = "kg_update_exchange"
    RABBIT_MQ_KG_UPDATE_QUEUE: str = "kg_update"

    REDIS_SERVER_HOST: str
    REDIS_SERVER_PORT: int
    REDIS_USER_NAME: str
    REDIS_USER_PASSWORD: str
    CLICKHOUSE_HOST: str
    CLICKHOUSE_PASSWORD: str
    CLICKHOUSE_USER: str
    CLICKHOUSE_DATABASE: str = "nudgebee"
    CLICKHOUSE_ENABLED: bool = False
    TASK_CLEANUP_WINDOW: int = 1800
    TASK_BATCH_LIMIT: int = 50
    AUTO_PILOT_URL: str = "http://localhost:9988"
    ACTION_API_SERVER_TOKEN: str
    SERVICE_API_SERVER_URL: str = "http://services-server:8000"
    COLLECTOR_MESSAGE_SIZE_THRESHOLD_MB: int = 100
    K8S_COLLECTOR_CONSUMER_MAX_WORKERS: int = 2
    K8S_COLLECTOR_CONSUMER_HEARTBEAT: int = 120
    LLM_SERVER_ENDPOINT: str = "http://llm-server:8000"
    EKS_VERSIONS_SUPPORT: ClassVar[Dict[str, Dict[str, str]]] = {
        "1.32": {
            "upstream_release": "2024-12-11",
            "eks_release": "2025-01-23",
            "end_of_standard_support": "2026-03-23",
            "end_of_extended_support": "2027-03-23",
        },
        "1.31": {
            "upstream_release": "2024-08-13",
            "eks_release": "2024-09-26",
            "end_of_standard_support": "2025-11-26",
            "end_of_extended_support": "2026-11-26",
        },
        "1.30": {
            "upstream_release": "2024-04-17",
            "eks_release": "2024-05-23",
            "end_of_standard_support": "2025-07-23",
            "end_of_extended_support": "2026-07-23",
        },
        "1.29": {
            "upstream_release": "2023-12-13",
            "eks_release": "2024-01-23",
            "end_of_standard_support": "2025-03-23",
            "end_of_extended_support": "2026-03-23",
        },
        "1.28": {
            "upstream_release": "2023-08-15",
            "eks_release": "2023-09-26",
            "end_of_standard_support": "2024-11-26",
            "end_of_extended_support": "2025-11-26",
        },
        "1.27": {
            "upstream_release": "2023-04-11",
            "eks_release": "2023-05-24",
            "end_of_standard_support": "2024-07-24",
            "end_of_extended_support": "2025-07-24",
        },
        "1.26": {
            "upstream_release": "2022-12-09",
            "eks_release": "2023-04-11",
            "end_of_standard_support": "2024-06-11",
            "end_of_extended_support": "2025-06-11",
        },
        "1.25": {
            "upstream_release": "2022-08-23",
            "eks_release": "2023-02-22",
            "end_of_standard_support": "2024-05-01",
            "end_of_extended_support": "2025-05-01",
        },
    }

    CORE_DNS_COMPATIBILITY: ClassVar[Dict[str, List[str]]] = {
        "1.33": ["v1.12.2"],
        "1.32": ["v1.11.4"],
        "1.31": [
            "v1.11.4",
            "v1.11.3",
            "v1.11.1",
            "v1.10.1",
        ],
        "1.30": [
            "v1.11.4",
            "v1.11.3",
            "v1.11.1",
            "v1.10.1",
        ],
        "1.29": [
            "v1.11.4",
            "v1.11.3",
            "v1.11.1",
            "v1.10.1",
        ],
        "1.28": [
            "v1.10.1",
            "v1.9.3",
        ],
        "1.27": [
            "v1.10.1",
            "v1.9.3",
        ],
    }

    VPC_CNI_COMPATIBILITY: ClassVar[Dict[str, List[str]]] = {
        "1.31": [
            "v1.19.2",
            "v1.19.0",
            "v1.18.6",
            "v1.18.5",
            "v1.18.4",
            "v1.18.3",
            "v1.18.2",
            "v1.18.1",
            "v1.17.1",
            "v1.16.4",
        ],
        "1.32": ["v1.19.2"],
        "1.30": [
            "v1.19.2",
            "v1.19.0",
            "v1.18.6",
            "v1.18.5",
            "v1.18.4",
            "v1.18.3",
            "v1.18.2",
            "v1.18.1",
            "v1.17.1",
            "v1.16.4",
            "v1.16.0",
        ],
        "1.29": [
            "v1.19.2",
            "v1.19.0",
            "v1.18.6",
            "v1.18.5",
            "v1.18.4",
            "v1.18.3",
            "v1.18.2",
            "v1.18.1",
            "v1.17.1",
            "v1.16.4",
            "v1.16.3",
            "v1.16.2",
            "v1.16.0",
            "v1.15.5",
            "v1.15.4",
            "v1.14.1",
            "v1.13.4",
        ],
        "1.28": [
            "v1.19.2",
            "v1.19.0",
            "v1.18.6",
            "v1.18.5",
            "v1.18.4",
            "v1.18.3",
            "v1.18.2",
            "v1.18.1",
            "v1.17.1",
            "v1.16.4",
            "v1.16.3",
            "v1.16.2",
            "v1.16.0",
            "v1.15.5",
            "v1.15.4",
            "v1.15.3",
            "v1.15.1",
            "v1.15.0",
            "v1.14.1",
            "v1.14.0",
            "v1.13.4",
            "v1.13.3",
        ],
        "1.27": [
            "v1.19.2",
            "v1.19.0",
            "v1.18.6",
            "v1.18.5",
            "v1.18.4",
            "v1.18.3",
            "v1.18.2",
            "v1.18.1",
            "v1.17.1",
            "v1.16.4",
            "v1.16.3",
            "v1.16.2",
            "v1.16.0",
            "v1.15.5",
            "v1.15.4",
            "v1.15.3",
            "v1.15.1",
            "v1.15.0",
            "v1.14.1",
            "v1.14.0",
            "v1.13.4",
            "v1.13.3",
        ],
    }

    EBS_DRIVER_COMPATIBILITY: ClassVar[Dict[str, List[str]]] = {
        "1.30": [
            "v1.39.0",
            "v1.38.1",
            "v1.37.0",
            "v1.36.0",
            "v1.35.0",
            "v1.34.0",
            "v1.33.0",
            "v1.32.0",
            "v1.31.0",
            "v1.30.0",
            "v1.29.1",
            "v1.28.0",
            "v1.27.0",
            "v1.26.1",
        ],
        "1.28": [
            "v1.39.0",
            "v1.38.1",
            "v1.37.0",
            "v1.36.0",
            "v1.35.0",
            "v1.34.0",
            "v1.33.0",
            "v1.32.0",
            "v1.31.0",
            "v1.30.0",
            "v1.29.1",
            "v1.28.0",
            "v1.27.0",
            "v1.26.1",
            "v1.26.0",
            "v1.25.0",
            "v1.24.1",
            "v1.24.0",
            "v1.23.2",
            "v1.23.1",
            "v1.23.0",
            "v1.22.1",
            "v1.22.0",
            "v1.21.0",
            "v1.20.0",
            "v1.19.0",
            "v1.18.0",
            "v1.17.0",
        ],
        "1.32": [
            "v1.39.0",
            "v1.38.1",
            "v1.37.0",
            "v1.36.0",
            "v1.35.0",
            "v1.34.0",
            "v1.33.0",
            "v1.32.0",
            "v1.31.0",
            "v1.30.0",
            "v1.29.1",
        ],
        "1.31": [
            "v1.39.0",
            "v1.38.1",
            "v1.37.0",
            "v1.36.0",
            "v1.35.0",
            "v1.34.0",
            "v1.33.0",
            "v1.32.0",
            "v1.31.0",
            "v1.30.0",
            "v1.29.1",
        ],
        "1.29": [
            "v1.39.0",
            "v1.38.1",
            "v1.37.0",
            "v1.36.0",
            "v1.35.0",
            "v1.34.0",
            "v1.33.0",
            "v1.32.0",
            "v1.31.0",
            "v1.30.0",
            "v1.29.1",
            "v1.28.0",
            "v1.27.0",
            "v1.26.1",
            "v1.26.0",
            "v1.25.0",
            "v1.24.1",
            "v1.24.0",
            "v1.23.2",
            "v1.23.1",
            "v1.23.0",
            "v1.22.1",
            "v1.22.0",
            "v1.21.0",
            "v1.20.0",
        ],
        "1.27": [
            "v1.39.0",
            "v1.38.1",
            "v1.37.0",
            "v1.36.0",
            "v1.35.0",
            "v1.34.0",
            "v1.33.0",
            "v1.32.0",
            "v1.31.0",
            "v1.30.0",
            "v1.29.1",
            "v1.28.0",
            "v1.27.0",
            "v1.26.1",
            "v1.26.0",
            "v1.25.0",
            "v1.24.1",
            "v1.24.0",
            "v1.23.2",
            "v1.23.1",
            "v1.23.0",
            "v1.22.1",
            "v1.22.0",
            "v1.21.0",
            "v1.20.0",
            "v1.19.0",
            "v1.18.0",
            "v1.17.0",
            "v1.16.1",
            "v1.16.0",
            "v1.15.1",
            "v1.15.0",
            "v1.14.1",
            "v1.14.0",
            "v1.13.0",
            "v1.12.1",
            "v1.11.5",
        ],
    }


def setup_logger() -> None:
    basedir = os.path.abspath(os.path.dirname(__file__))
    config_file = os.path.join(basedir, "logging.json")
    print("reading logging configs from ", config_file)
    with open(config_file, "rt") as f:
        config = json.load(f)
        logging.config.dictConfig(config)


Configs = Settings()
