"""
Configuration module for notifications server.

This module provides centralized configuration management using Pydantic Settings.
All configuration is loaded from environment variables with sensible defaults.

Usage:
    from notifications_server.configs import settings

    # Access configuration
    db_url = settings.database.url
    rabbitmq_host = settings.rabbitmq.host

For backward compatibility, the old Configs class is still available:
    from notifications_server.configs import Configs
"""

import json
import logging.config
import os

from notifications_server.configs.settings import (
    # Main settings instance
    settings,
    Settings,
    # Sub-settings classes
    DatabaseSettings,
    RabbitMQSettings,
    RedisSettings,
    SlackSettings,
    MSTeamsSettings,
    GoogleChatSettings,
    EmailSettings,
    ServiceURLSettings,
    NotificationSettings,
    # Legacy functions
    get_rabbitmq_connection_params,
    get_smtp_params,
    public_ip,
    # Constants
    CERTIFICATE_FOLDER,
    OBSERVE_TIMEOUT,
    SLACK_TABLE_COLUMNS_LIMIT,
    SKIP_AUTO_OPTIMIZE_ENDPOINT,
    CREATE_TICKET_ENDPOINT,
    LLM_CHAT_ENDPOINT,
    ACCOUNT_SECURITY_CONTEXT,
    RELAY_REQUEST_ENDPOINT,
    AUDIT_URL,
    DEFAULT_EVIDENCES,
)


def setup_logger() -> None:
    """Configure logging from JSON config file."""
    basedir = os.path.abspath(os.path.dirname(__file__))
    config_file = os.path.join(basedir, "logging.json")
    with open(config_file, "rt") as f:
        config = json.load(f)
        logging.config.dictConfig(config)


class HealthCheckFilter(logging.Filter):
    """Filter out health check requests from logs."""

    def filter(self, record: logging.LogRecord) -> bool:
        return record.getMessage().find("/health") == -1


# Backward compatibility: Configs as alias for settings
# This allows existing code using Configs.RABBIT_MQ_HOST to continue working
class _LegacyConfigsWrapper:
    """
    Backward compatibility wrapper for old Configs.ATTRIBUTE style access.

    Maps old attribute names to new settings structure.
    """

    # Database
    @property
    def NOTIFICATION_DB_URL(self):
        return settings.database.url

    # RabbitMQ
    @property
    def RABBIT_MQ_HOST(self):
        return settings.rabbitmq.host

    @property
    def RABBIT_MQ_PORT(self):
        return settings.rabbitmq.port

    @property
    def RABBIT_MQ_USERNAME(self):
        return settings.rabbitmq.username

    @property
    def RABBIT_MQ_PASSWORD(self):
        return settings.rabbitmq.password

    # Services
    @property
    def TICKET_SERVICE_URL(self):
        return settings.services.ticket_server

    @property
    def ML_SERVICE_URL(self):
        return settings.services.ml_server

    @property
    def SERVICE_API_SERVER_URL(self):
        return settings.services.api_server

    @property
    def AUTO_PILOT_URL(self):
        return settings.services.auto_pilot

    @property
    def RELAY_SERVER_ENDPOINT(self):
        return settings.services.relay_server

    @property
    def LLM_SERVER_URL(self):
        return settings.services.llm_server

    @property
    def MS_TEAMS_BOT_ENDPOINT(self):
        return settings.ms_teams.bot_endpoint

    @property
    def WORKFLOW_SERVER_URL(self):
        return settings.services.workflow_server

    # Common
    @property
    def ACTION_API_SERVER_TOKEN(self):
        return settings.action_api_server_token

    @property
    def DEFAULT_DATE_TIME_FORMAT(self):
        return settings.default_datetime_format

    @property
    def BASE_URL(self):
        return settings.base_url

    @property
    def NUDGEBEE_SECRET(self):
        return settings.nudgebee_secret

    @property
    def GPT_TOKEN(self):
        return settings.gpt_token

    @property
    def GOOGLE_CLIENT_ID(self):
        return settings.google_chat.client_id

    @property
    def GOOGLE_CLIENT_SECRET(self):
        return settings.google_chat.client_secret

    @property
    def MIN_CPU_CORE(self):
        return settings.min_cpu_core

    @property
    def MIN_MEMORY(self):
        return settings.min_memory

    @property
    def LOCAL_ENV(self):
        return settings.local_env


# Singleton instance for backward compatibility
Configs = _LegacyConfigsWrapper()


__all__ = [
    # New style
    "settings",
    "Settings",
    "DatabaseSettings",
    "RabbitMQSettings",
    "RedisSettings",
    "SlackSettings",
    "MSTeamsSettings",
    "GoogleChatSettings",
    "EmailSettings",
    "ServiceURLSettings",
    "NotificationSettings",
    # Utilities
    "setup_logger",
    "HealthCheckFilter",
    # Legacy
    "Configs",
    "get_rabbitmq_connection_params",
    "get_smtp_params",
    "public_ip",
    # Constants
    "CERTIFICATE_FOLDER",
    "OBSERVE_TIMEOUT",
    "SLACK_TABLE_COLUMNS_LIMIT",
    "SKIP_AUTO_OPTIMIZE_ENDPOINT",
    "CREATE_TICKET_ENDPOINT",
    "LLM_CHAT_ENDPOINT",
    "ACCOUNT_SECURITY_CONTEXT",
    "RELAY_REQUEST_ENDPOINT",
    "AUDIT_URL",
    "DEFAULT_EVIDENCES",
]
