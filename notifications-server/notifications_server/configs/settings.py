import json
import logging
from pathlib import Path
from typing import List, Optional, Dict
from urllib.parse import urlencode
from pydantic import AliasChoices, Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

_branding_logger = logging.getLogger(__name__)

# Bundled branding file shipped in the image — mirrors app/public/branding/default/theme.json.
# Keep the two files in sync; partners override at runtime via TENANT_BRANDING_FILE.
_BUNDLED_BRANDING_FILE = str(Path(__file__).resolve().parents[1] / "branding" / "default" / "theme.json")


class URLRoutes:
    # ==================== Route Patterns ====================
    # Kubernetes / Cluster routes
    INVESTIGATE = "/investigate"
    CLUSTER_DETAILS = "/kubernetes/details/{account_id}"

    # Events routes
    EVENTS = "/events"

    # Auto Pilot routes
    AUTO_PILOT_TASK = "/auto-pilot/task/{auto_pilot_id}"

    # Workflow / Automation routes
    WORKFLOW = "/workflow/{workflow_id}"

    # Cloud Account routes
    CLOUD_ACCOUNT_DETAILS = "/cloud-account/details/{account_id}"

    # Daily Recap
    DAILY_RECAP = "/daily-recap/{org_id}"

    # Home
    HOME = "/home"

    # ==================== Hash Fragment Anchors ====================
    # Used for in-page navigation
    class Anchors:
        # Events
        EVENTS_ANOMALY = "events/anomaly"
        EVENTS_ALL = "events/all-events"
        EVENTS_APP_ERRORS = "events/app-errors"
        EVENTS_NODE_ERRORS = "events/node-errors"
        EVENTS_POD_ERRORS = "events/pod-errors"

        # Optimization
        OPTIMIZE_SUMMARY = "optimize/summary"
        OPTIMIZE_UNUSED_VOLUME = "optimize/unused-volume"
        OPTIMIZE_RIGHT_SIZING = "optimize/right-sizing"
        OPTIMIZE_PV_RIGHTSIZING = "optimize/pv-rightsizing"
        OPTIMIZE_REPLICA_RIGHTSIZING = "optimize/replica-rightsizing"
        OPTIMIZE_ABANDONED_RESOURCES = "optimize/abandoned-resources"

        # Security
        SECURITY_IMAGE_SCAN = "security/image-scan"
        SECURITY_CLUSTER_UPGRADE = "security/cluster-upgrade"

    # ==================== UTM Source Parameters ====================
    class UTMSource:
        SLACK = "slack"
        TEAMS = "teams"
        GCHAT = "gchat"
        EMAIL = "email"


class URLSettings(BaseSettings):
    """
    URL configuration for all notification redirect links.

    Centralizes all URLs used across email, Slack, Teams, and Google Chat notifications.
    """

    # Base application URL (where users are redirected). Operators must set BASE_URL for absolute
    # links in emails / Slack / Teams to resolve; empty leaves links relative.
    base_url: str = Field("", validation_alias=AliasChoices("BASE_URL", "base_url"))

    # Optional Calendly (or equivalent) URL surfaced in invite emails. Empty hides the P.S. block.
    calendly_url: str = Field("", validation_alias=AliasChoices("CALENDLY_URL", "calendly_url"))

    # Path to theme.json — same env var name and JSON shape as the Next.js app's TENANT_BRANDING_FILE.
    # Defaults to the bundled file shipped in the image (kept in sync with app/public/branding/default/theme.json).
    tenant_branding_file: str = Field(
        _BUNDLED_BRANDING_FILE,
        validation_alias=AliasChoices("TENANT_BRANDING_FILE", "tenant_branding_file"),
    )

    # Branding fields populated from theme.json by load_branding_from_theme_file().
    # Operators provide values via TENANT_BRANDING_FILE; support_email is intentionally empty by default
    # so OSS deployments don't leak the upstream Nudgebee support mailbox.
    branding_name: str = "Nudgebee"
    branding_logo_url: str = "/branding/default/logo.png"
    branding_support_email: str = ""
    branding_primary_color: str = "#3470e9"
    branding_header_bg_color: str = "#004C74"
    branding_footer_bg_color: str = "#10264c"
    branding_footer_link_color: str = "#ffcc00"
    branding_address: str = "Pune"
    branding_copyright_start_year: int = 2022

    model_config = SettingsConfigDict(env_prefix="")

    @model_validator(mode="after")
    def load_branding_from_theme_file(self):
        """Populate branding_* fields from theme.json; silently fall back to defaults if the file is missing."""
        path = Path(self.tenant_branding_file)
        if not path.is_file():
            _branding_logger.info(
                "Branding theme file not found at %s; using hardcoded defaults", self.tenant_branding_file
            )
            return self
        try:
            theme = json.loads(path.read_text())
        except (OSError, json.JSONDecodeError) as exc:
            _branding_logger.warning(
                "Failed to load branding theme from %s: %s; using hardcoded defaults",
                self.tenant_branding_file,
                exc,
            )
            return self

        if isinstance(theme.get("title"), str):
            self.branding_name = theme["title"]
        email = theme.get("email") or {}
        for json_key, attr in (
            ("logoUrl", "branding_logo_url"),
            ("supportEmail", "branding_support_email"),
            ("primaryColor", "branding_primary_color"),
            ("headerBgColor", "branding_header_bg_color"),
            ("footerBgColor", "branding_footer_bg_color"),
            ("footerLinkColor", "branding_footer_link_color"),
            ("address", "branding_address"),
        ):
            if isinstance(email.get(json_key), str):
                setattr(self, attr, email[json_key])
        if isinstance(email.get("copyrightStartYear"), int):
            self.branding_copyright_start_year = email["copyrightStartYear"]
        return self

    @model_validator(mode="after")
    def resolve_relative_urls(self):
        """Resolve relative paths (starting with /) to absolute URLs using base_url."""
        if self.branding_logo_url.startswith("/"):
            self.branding_logo_url = f"{self.base_url}{self.branding_logo_url}"
        return self

    # Route patterns
    routes: URLRoutes = URLRoutes()

    def _build_url(
        self,
        path: str,
        path_params: Optional[Dict[str, str]] = None,
        query_params: Optional[Dict[str, str]] = None,
        utm_source: Optional[str] = None,
        anchor: Optional[str] = None,
    ) -> str:
        """
        Build a full URL with path parameters, query string, and optional anchor.

        Args:
            path: Route pattern (e.g., "/kubernetes/details/{account_id}")
            path_params: Values to substitute in path (e.g., {"account_id": "123"})
            query_params: Query string parameters (e.g., {"tab": "0"})
            utm_source: UTM source for tracking (slack, teams, gchat, email)
            anchor: Hash fragment for in-page navigation (e.g., "events/anomaly")

        Returns:
            Full URL string
        """
        # Substitute path parameters using str.format()
        url = path.format(**path_params) if path_params else path

        # Build query string
        params = dict(query_params) if query_params else {}
        if utm_source:
            params["utm"] = utm_source

        full_url = f"{self.base_url}{url}"
        if params:
            full_url = f"{full_url}?{urlencode(params)}"
        if anchor:
            full_url = f"{full_url}#{anchor}"

        return full_url

    # ==================== Convenience Methods ====================

    def investigate_url(
        self,
        account_id: str,
        finding_id: str,
        utm_source: Optional[str] = None,
    ) -> str:
        """Build URL for finding investigation page."""
        return self._build_url(
            URLRoutes.INVESTIGATE,
            query_params={"accountId": account_id, "id": finding_id},
            utm_source=utm_source,
        )

    def cluster_details_url(
        self,
        account_id: str,
        tab: Optional[int] = None,
        subtab: Optional[int] = None,
        utm_source: Optional[str] = None,
        anchor: Optional[str] = None,
    ) -> str:
        """Build URL for cluster/account details page."""
        query_params = {"accountId": account_id}
        if tab is not None:
            query_params["tab"] = str(tab)
        if subtab is not None:
            query_params["subtab"] = str(subtab)

        return self._build_url(
            URLRoutes.CLUSTER_DETAILS,
            path_params={"account_id": account_id},
            query_params=query_params,
            utm_source=utm_source,
            anchor=anchor,
        )

    def events_url(
        self,
        account_id: str,
        utm_source: Optional[str] = None,
    ) -> str:
        """Build URL for events page."""
        return self._build_url(
            URLRoutes.EVENTS,
            query_params={"accountId": account_id},
            utm_source=utm_source,
        )

    def auto_pilot_task_url(
        self,
        auto_pilot_id: str,
        account_id: Optional[str] = None,
        utm_source: Optional[str] = None,
    ) -> str:
        """Build URL for auto pilot task page."""
        query_params = {}
        if account_id:
            query_params["accountId"] = account_id

        return self._build_url(
            URLRoutes.AUTO_PILOT_TASK,
            path_params={"auto_pilot_id": auto_pilot_id},
            query_params=query_params if query_params else None,
            utm_source=utm_source,
        )

    def workflow_url(
        self,
        workflow_id: str,
        account_id: Optional[str] = None,
        utm_source: Optional[str] = None,
    ) -> str:
        """Build URL for the workflow/automation builder page."""
        query_params = {}
        if account_id:
            query_params["accountId"] = account_id

        return self._build_url(
            URLRoutes.WORKFLOW,
            path_params={"workflow_id": workflow_id},
            query_params=query_params if query_params else None,
            utm_source=utm_source,
        )

    def cloud_account_details_url(
        self,
        account_id: str,
        tab: int = 0,
        utm_source: Optional[str] = None,
    ) -> str:
        """Build URL for cloud account details/cost page."""
        return self._build_url(
            URLRoutes.CLOUD_ACCOUNT_DETAILS,
            path_params={"account_id": account_id},
            query_params={"tab": str(tab)},
            utm_source=utm_source,
        )

    def daily_recap_url(
        self,
        org_id: str,
        utm_source: Optional[str] = None,
    ) -> str:
        """Build URL for daily recap page."""
        return self._build_url(
            URLRoutes.DAILY_RECAP,
            path_params={"org_id": org_id},
            utm_source=utm_source,
        )

    def home_url(self, utm_source: Optional[str] = None) -> str:
        """Build URL for home page."""
        return self._build_url(URLRoutes.HOME, utm_source=utm_source)

    def branding_link(self, format_type: str = "markdown") -> str:
        if format_type == "slack":
            return f"<{self.base_url}|{self.branding_name}>"
        elif format_type == "gchat":
            return f"<{self.base_url}|{self.branding_name}>"
        else:  # markdown (Teams, email)
            return f"[{self.branding_name}]({self.base_url})"


class DatabaseSettings(BaseSettings):
    """Database connection settings."""

    url: str = Field(
        "postgresql://postgres:password@127.0.0.1:5432/nudgebee",
        validation_alias=AliasChoices("NOTIFICATION_DB_URL", "notification_db_url"),
        description="PostgreSQL connection URL",
    )
    pool_size: int = Field(20, validation_alias=AliasChoices("DB_POOL_SIZE", "db_pool_size"))
    max_overflow: int = Field(10, validation_alias=AliasChoices("DB_MAX_OVERFLOW", "db_max_overflow"))
    sync_pool_size: int = Field(10, validation_alias=AliasChoices("DB_SYNC_POOL_SIZE", "db_sync_pool_size"))
    sync_max_overflow: int = Field(10, validation_alias=AliasChoices("DB_SYNC_MAX_OVERFLOW", "db_sync_max_overflow"))

    model_config = SettingsConfigDict(env_prefix="")


class RabbitMQSettings(BaseSettings):
    """RabbitMQ message broker settings."""

    host: str = Field("localhost", validation_alias=AliasChoices("RABBIT_MQ_HOST", "rabbit_mq_host"))
    port: int = Field(5672, validation_alias=AliasChoices("RABBIT_MQ_PORT", "rabbit_mq_port"))
    username: str = Field("user", validation_alias=AliasChoices("RABBIT_MQ_USERNAME", "rabbit_mq_username"))
    password: str = Field("password", validation_alias=AliasChoices("RABBIT_MQ_PASSWORD", "rabbit_mq_password"))
    notifications_queue: str = Field(
        "notifications", validation_alias=AliasChoices("NOTIFICATIONS_QUEUE", "notifications_queue")
    )
    prefetch_count: int = Field(20, validation_alias=AliasChoices("RABBITMQ_PREFETCH_COUNT", "rabbitmq_prefetch_count"))
    dead_letter_delay_ms: int = Field(
        5000, validation_alias=AliasChoices("RABBITMQ_DEAD_LETTER_DELAY_MS", "rabbitmq_dead_letter_delay_ms")
    )
    heartbeat: int = Field(300, validation_alias=AliasChoices("RABBITMQ_HEARTBEAT", "rabbitmq_heartbeat"))
    connection_timeout: int = Field(
        30, validation_alias=AliasChoices("RABBITMQ_CONNECTION_TIMEOUT", "rabbitmq_connection_timeout")
    )

    model_config = SettingsConfigDict(env_prefix="")


class RedisSettings(BaseSettings):
    """Redis cache settings."""

    host: str = Field("localhost", validation_alias=AliasChoices("REDIS_SERVER_HOST", "redis_server_host"))
    port: int = Field(6379, validation_alias=AliasChoices("REDIS_SERVER_PORT", "redis_server_port"))
    username: str = Field("user", validation_alias=AliasChoices("REDIS_USER_NAME", "redis_user_name"))
    password: str = Field("password", validation_alias=AliasChoices("REDIS_USER_PASSWORD", "redis_user_password"))
    provider: str = Field(
        "memory",
        validation_alias=AliasChoices("CACHE_PROVIDER", "cache_provider"),
        description="Cache provider: 'redis' or 'memory'",
    )
    cache_expiration_minutes: int = Field(
        60, validation_alias=AliasChoices("CACHE_EXPIRATION_MINUTES", "cache_expiration_minutes")
    )
    conversation_cache_expiration_minutes: int = Field(
        1440,
        validation_alias=AliasChoices("CONVERSATION_CACHE_EXPIRATION_MINUTES", "conversation_cache_expiration_minutes"),
    )

    model_config = SettingsConfigDict(env_prefix="")

    @property
    def is_enabled(self) -> bool:
        """Check if Redis is enabled."""
        return self.provider.lower() == "redis"


class SlackSettings(BaseSettings):
    """Slack integration settings."""

    signing_secret: str = Field("", validation_alias=AliasChoices("SLACK_SIGNING_SECRET", "slack_signing_secret"))
    client_id: str = Field("", validation_alias=AliasChoices("SLACK_CLIENT_ID", "slack_client_id"))
    client_secret: str = Field("", validation_alias=AliasChoices("SLACK_CLIENT_SECRET", "slack_client_secret"))
    auth_type: str = Field(
        "OAuth",
        validation_alias=AliasChoices("SLACK_AUTH_TYPE", "slack_auth_type"),
        description="'OAuth' or 'BotToken'",
    )
    bot_token: str = Field(
        "",
        validation_alias=AliasChoices("SLACK_BOT_TOKEN", "slack_bot_token"),
        description="Bot token for non-OAuth auth",
    )
    channel_id: str = Field(
        "",
        validation_alias=AliasChoices("SLACK_CHANNEL_ID", "slack_channel_id"),
        description="Default channel for non-OAuth auth",
    )
    table_columns_limit: int = 3
    text_block_limit: int = Field(
        2800, validation_alias=AliasChoices("SLACK_TEXT_BLOCK_LIMIT", "slack_text_block_limit")
    )
    max_rate_limit_retries: int = Field(
        2, validation_alias=AliasChoices("SLACK_MAX_RATE_LIMIT_RETRIES", "slack_max_rate_limit_retries")
    )
    # Beyond this count, route users to the UI instead of rendering a Slack dropdown.
    # Slack static_select hard-caps at 100; dropdowns become unusable well before that.
    followup_options_threshold: int = Field(
        25, validation_alias=AliasChoices("FOLLOWUP_OPTIONS_THRESHOLD", "followup_options_threshold")
    )

    model_config = SettingsConfigDict(env_prefix="")

    @property
    def is_oauth(self) -> bool:
        """Check if using OAuth authentication."""
        return self.auth_type.lower() == "oauth"

    @property
    def is_configured(self) -> bool:
        """Check if Slack is configured (either OAuth or BotToken)."""
        if self.is_oauth:
            return bool(self.client_id and self.client_secret)
        return bool(self.bot_token)


class MSTeamsSettings(BaseSettings):
    """Microsoft Teams integration settings."""

    client_id: str = Field("", validation_alias=AliasChoices("MS_TEAMS_CLIENT_ID", "ms_teams_client_id"))
    client_secret: str = Field("", validation_alias=AliasChoices("MS_TEAMS_CLIENT_SECRET", "ms_teams_client_secret"))
    authority: str = Field(
        "https://login.microsoftonline.com/common",
        validation_alias=AliasChoices("MS_TEAMS_AUTHORITY", "ms_teams_authority"),
    )
    bot_endpoint: str = Field(
        "",
        validation_alias=AliasChoices("MS_TEAMS_BOT_ENDPOINT", "ms_teams_bot_endpoint"),
    )
    redirect_path: str = "/api/integrations/callback/ms-teams"
    scopes: List[str] = [
        "User.ReadBasic.All",
        "User.Read.All",
        "Team.ReadBasic.All",
        "Channel.ReadBasic.All",
        "ChannelMessage.Send",
        "Chat.Create",
        "Chat.ReadWrite",
        "ChatMessage.Send",
    ]
    max_rate_limit_retries: int = Field(
        2, validation_alias=AliasChoices("MS_TEAMS_MAX_RATE_LIMIT_RETRIES", "ms_teams_max_rate_limit_retries")
    )

    model_config = SettingsConfigDict(env_prefix="")

    @property
    def is_configured(self) -> bool:
        """Check if MS Teams is properly configured."""
        return bool(self.client_id and self.client_secret and self.bot_endpoint)


class GoogleChatSettings(BaseSettings):
    """Google Chat integration settings."""

    client_id: str = Field("", validation_alias=AliasChoices("GOOGLE_CLIENT_ID", "google_client_id"))
    client_secret: str = Field("", validation_alias=AliasChoices("GOOGLE_CLIENT_SECRET", "google_client_secret"))
    project_number: str = Field(
        "",
        validation_alias=AliasChoices("GOOGLE_CHAT_PROJECT_NUMBER", "google_chat_project_number"),
        description="Google Cloud project number for verifying incoming webhook JWTs",
    )
    auth_url: str = Field(
        "https://accounts.google.com/o/oauth2/auth",
        validation_alias=AliasChoices("GOOGLE_AUTH_ENDPOINT", "google_auth_endpoint"),
    )
    redirect_path: str = "/api/integrations/callback/google"
    rate_limit_retries: int = Field(
        3, validation_alias=AliasChoices("GOOGLE_CHAT_RATE_LIMIT_RETRIES", "google_chat_rate_limit_retries")
    )
    rate_limit_sleep: int = Field(
        10, validation_alias=AliasChoices("GOOGLE_CHAT_RATE_LIMIT_SLEEP", "google_chat_rate_limit_sleep")
    )

    model_config = SettingsConfigDict(env_prefix="")

    @property
    def is_configured(self) -> bool:
        """Check if Google Chat is properly configured."""
        return bool(self.client_id and self.client_secret)


class EmailSettings(BaseSettings):
    """Email/SMTP settings."""

    server_host: str = Field("", validation_alias=AliasChoices("EMAIL_SERVER_HOST", "email_server_host"))
    server_port: int = Field(465, validation_alias=AliasChoices("EMAIL_SERVER_PORT", "email_server_port"))
    server_user: str = Field("", validation_alias=AliasChoices("EMAIL_SERVER_USER", "email_server_user"))
    server_password: str = Field("", validation_alias=AliasChoices("EMAIL_SERVER_PASSWORD", "email_server_password"))
    from_address: str = Field("", validation_alias=AliasChoices("EMAIL_FROM", "email_from"))
    max_concurrent_sends: int = Field(
        10, validation_alias=AliasChoices("EMAIL_MAX_CONCURRENT_SENDS", "email_max_concurrent_sends")
    )

    model_config = SettingsConfigDict(env_prefix="")

    @property
    def is_configured(self) -> bool:
        """Check if SMTP is configured."""
        return bool(self.server_host and self.server_user and self.server_password and self.from_address)

    def get_smtp_params(self) -> Optional[List[str]]:
        """Return SMTP params in legacy format for backward compatibility."""
        if not self.is_configured:
            return None
        return [
            str(self.server_port),
            self.server_host,
            self.server_user,
            self.server_password,
            self.from_address,
        ]


class ServiceURLSettings(BaseSettings):
    """Internal service URLs."""

    ticket_server: str = Field(
        "http://ticket-server:80", validation_alias=AliasChoices("TICKET_SERVICE_URL", "ticket_service_url")
    )
    ml_server: str = Field("http://localhost:9999", validation_alias=AliasChoices("ML_SERVICE_URL", "ml_service_url"))
    api_server: str = Field(
        "http://services-server:8000", validation_alias=AliasChoices("SERVICE_API_SERVER_URL", "service_api_server_url")
    )
    auto_pilot: str = Field(
        "http://auto-pilot-server:9988", validation_alias=AliasChoices("AUTO_PILOT_URL", "auto_pilot_url")
    )
    relay_server: str = Field(
        "http://relay-server:8080", validation_alias=AliasChoices("RELAY_SERVER_ENDPOINT", "relay_server_endpoint")
    )
    llm_server: str = Field("http://llm-server:8000", validation_alias=AliasChoices("LLM_SERVER_URL", "llm_server_url"))
    workflow_server: str = Field(
        "http://workflow-server:8000", validation_alias=AliasChoices("WORKFLOW_SERVER_URL", "workflow_server_url")
    )

    model_config = SettingsConfigDict(env_prefix="")


class NotificationSettings(BaseSettings):
    """Notification behavior settings."""

    delay_ms: int = Field(
        600000,
        validation_alias=AliasChoices("NOTIFICATIONS_DELAY_IN_SECONDS", "notifications_delay_in_seconds"),
        description="Delay in milliseconds",
    )
    enable_batched: bool = Field(
        False, validation_alias=AliasChoices("ENABLE_BATCHED_NOTIFICATIONS", "enable_batched_notifications")
    )
    enable_incident_channel_reply: bool = Field(
        True, validation_alias=AliasChoices("ENABLE_INCIDENT_CHANNEL_REPLY", "enable_incident_channel_reply")
    )
    max_detailed_evidences: int = Field(
        3, validation_alias=AliasChoices("MAX_DETAILED_EVIDENCES", "max_detailed_evidences")
    )
    task_retry_count: int = Field(5, validation_alias=AliasChoices("TASK_RETRY_COUNT", "task_retry_count"))
    slow_task_threshold_seconds: int = Field(
        10, validation_alias=AliasChoices("SLOW_TASK_THRESHOLD_SECONDS", "slow_task_threshold_seconds")
    )
    grouped_flush_delay_seconds: int = Field(
        3600, validation_alias=AliasChoices("GROUPED_FLUSH_DELAY_SECONDS", "grouped_flush_delay_seconds")
    )
    grouped_redis_ttl_seconds: int = Field(
        3900, validation_alias=AliasChoices("GROUPED_REDIS_TTL_SECONDS", "grouped_redis_ttl_seconds")
    )

    model_config = SettingsConfigDict(env_prefix="")

    @field_validator("enable_batched", "enable_incident_channel_reply", mode="before")
    @classmethod
    def parse_bool(cls, v):
        """Parse boolean values from environment variables."""
        if isinstance(v, str):
            return v.lower() in ("true", "1", "yes")
        return bool(v)


class Settings(BaseSettings):
    """
    Main settings class that aggregates all configuration sections.

    Usage:
        from notifications_server.configs.settings import settings

        # Access nested settings
        settings.database.url
        settings.rabbitmq.host
        settings.slack.client_id
        settings.redis.is_enabled

        # Check if integrations are configured
        if settings.slack.is_configured:
            # Use Slack integration
            pass
    """

    # Nested settings - use Field with default_factory for proper initialization
    database: DatabaseSettings = Field(default_factory=DatabaseSettings)
    rabbitmq: RabbitMQSettings = Field(default_factory=RabbitMQSettings)
    redis: RedisSettings = Field(default_factory=RedisSettings)
    slack: SlackSettings = Field(default_factory=SlackSettings)
    ms_teams: MSTeamsSettings = Field(default_factory=MSTeamsSettings)
    google_chat: GoogleChatSettings = Field(default_factory=GoogleChatSettings)
    email: EmailSettings = Field(default_factory=EmailSettings)
    services: ServiceURLSettings = Field(default_factory=ServiceURLSettings)
    notifications: NotificationSettings = Field(default_factory=NotificationSettings)
    urls: URLSettings = Field(default_factory=URLSettings)

    # Application settings
    base_url: str = Field("", validation_alias=AliasChoices("BASE_URL", "base_url"))
    local_env: bool = Field(False, validation_alias=AliasChoices("LOCAL_ENV", "local_env"))
    trace_incoming_requests: bool = Field(
        False, validation_alias=AliasChoices("TRACE_INCOMING_REQUESTS", "trace_incoming_requests")
    )

    # Security tokens
    nudgebee_secret: str = Field("", validation_alias=AliasChoices("NUDGEBEE_SECRET", "nudgebee_secret"))
    action_api_server_token: str = Field(
        "", validation_alias=AliasChoices("ACTION_API_SERVER_TOKEN", "action_api_server_token")
    )
    llm_server_token: str = Field("", validation_alias=AliasChoices("LLM_SERVER_TOKEN", "llm_server_token"))
    llm_server_token_header: str = Field(
        "X-ACTION-TOKEN",
        validation_alias=AliasChoices("LLM_SERVER_TOKEN_HEADER", "llm_server_token_header"),
    )
    relay_server_secret_key: str = Field(
        "secret", validation_alias=AliasChoices("RELAY_SERVER_SECRET_KEY", "relay_server_secret_key")
    )
    gpt_token: str = Field("", validation_alias=AliasChoices("GPT_TOKEN", "gpt_token"))

    # Formatting
    default_datetime_format: str = Field(
        "%Y-%m-%dT%H:%M:%S.%fZ", validation_alias=AliasChoices("DEFAULT_DATE_TIME_FORMAT", "default_date_time_format")
    )

    # Resource limits
    min_cpu_core: float = Field(0.01, validation_alias=AliasChoices("MIN_CPU_CORE", "min_cpu_core"))
    min_memory: str = Field("500Mi", validation_alias=AliasChoices("MIN_MEMORY", "min_memory"))

    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", case_sensitive=False, extra="ignore")

    @field_validator("trace_incoming_requests", "local_env", mode="before")
    @classmethod
    def parse_bool(cls, v):
        """Parse boolean values from environment variables."""
        if isinstance(v, str):
            return v.lower() in ("true", "1", "yes")
        return bool(v)

    # Computed properties (derived from other settings)
    @property
    def ms_teams_redirect_uri(self) -> str:
        """Full MS Teams OAuth redirect URI."""
        return f"{self.base_url}{self.ms_teams.redirect_path}"

    @property
    def google_chat_redirect_uri(self) -> str:
        """Full Google Chat OAuth redirect URI."""
        return f"{self.base_url}{self.google_chat.redirect_path}"


# Singleton instance - import this everywhere
settings = Settings()


# ==================== Backward Compatibility Functions ====================
# These allow gradual migration from old config style


def get_rabbitmq_connection_params() -> List[str]:
    """
    Legacy function for RabbitMQ params.

    Returns:
        List of [username, password, host, port]
    """
    return [
        settings.rabbitmq.username,
        settings.rabbitmq.password,
        settings.rabbitmq.host,
        str(settings.rabbitmq.port),
    ]


def get_smtp_params() -> Optional[List[str]]:
    """
    Legacy function for SMTP params.

    Returns:
        List of [port, host, user, password, from_address] or None if not configured
    """
    return settings.email.get_smtp_params()


def public_ip() -> str:
    """
    Legacy function for base URL.

    Returns:
        Base URL of the application
    """
    return settings.base_url


# ==================== Module-Level Constants ====================
# Re-exported for backward compatibility with old import style

# Application URLs
BASE_URL = settings.base_url
MS_TEAMS_REDIRECT_URI = settings.ms_teams_redirect_uri
GOOGLE_CHAT_REDIRECT_URI = settings.google_chat_redirect_uri
GOOGLE_AUTH_URL = settings.google_chat.auth_url
TRACE_INCOMING_REQUESTS = settings.trace_incoming_requests
RELAY_SERVER_SECRET_KEY = settings.relay_server_secret_key
SCOPE = settings.ms_teams.scopes

# Cache settings
CACHE_EXPIRATION_MINUTES = settings.redis.cache_expiration_minutes
CONVERSATION_CACHE_EXPIRATION_MINUTES = settings.redis.conversation_cache_expiration_minutes
CACHE_PROVIDER = settings.redis.provider
REDIS_SERVER_HOST = settings.redis.host
REDIS_SERVER_PORT = settings.redis.port
REDIS_USER_NAME = settings.redis.username
REDIS_USER_PASSWORD = settings.redis.password

# Notification settings
NOTIFICATIONS_DELAY_IN_SECONDS = settings.notifications.delay_ms
ENABLE_BATCHED_NOTIFICATIONS = settings.notifications.enable_batched
ENABLE_INCIDENT_CHANNEL_REPLY = settings.notifications.enable_incident_channel_reply

# Slack settings
SLACK_AUTH_TYPE = settings.slack.auth_type
SLACK_BOT_TOKEN = settings.slack.bot_token
SLACK_CHANNEL_ID = settings.slack.channel_id
SLACK_TABLE_COLUMNS_LIMIT = settings.slack.table_columns_limit

MAX_DETAILED_EVIDENCES = settings.notifications.max_detailed_evidences

# ==================== Non-Configurable Constants ====================

# File system paths
CERTIFICATE_FOLDER = "/usr/local/share/ca-certificates/"

# Timeouts
OBSERVE_TIMEOUT = 2 * 60 * 60  # 2 hours in seconds

# API endpoint paths
SKIP_AUTO_OPTIMIZE_ENDPOINT = "/autopilot/execution/skip"
CREATE_TICKET_ENDPOINT = "/tickets/create-ticket"
LLM_CHAT_ENDPOINT = "/v1/completions/chat"
ACCOUNT_SECURITY_CONTEXT = "/v1/authz/get_security_context"
RELAY_REQUEST_ENDPOINT = "/request"
AUDIT_URL = "/v1/audit"

# Default evidences for notifications
DEFAULT_EVIDENCES = [
    "actual_destination_workload_name",
    "destination_workload_kind",
    "destination_workload_name",
    "destination_workload_namespace",
    "src_workload_kind",
    "src_workload_name",
    "src_workload_namespace",
    "status",
    "path",
    "severity",
    "image",
    "sample",
    "job",
]
