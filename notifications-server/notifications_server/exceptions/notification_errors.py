from typing import Optional


class NotificationError(Exception):
    """Base exception for all notification-related errors."""

    def __init__(self, message: str, channel: Optional[str] = None, tenant_id: Optional[str] = None):
        super().__init__(message)
        self.message = message
        self.channel = channel
        self.tenant_id = tenant_id

    def __str__(self):
        parts = [self.message]
        if self.channel:
            parts.append(f"channel={self.channel}")
        if self.tenant_id:
            parts.append(f"tenant={self.tenant_id}")
        return " | ".join(parts)


# -----------------------------------------------------------------------------
# Delivery Errors - Issues when sending notifications
# -----------------------------------------------------------------------------


class DeliveryError(NotificationError):
    """Base class for notification delivery failures."""

    pass


class DeliveryFailedError(DeliveryError):
    """Notification could not be delivered after all retry attempts."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        attempts: int = 0,
        last_error: Optional[Exception] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.attempts = attempts
        self.last_error = last_error


class RateLimitedError(DeliveryError):
    """External API rate limit exceeded."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        retry_after: Optional[int] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.retry_after = retry_after  # Seconds until retry is allowed


class ChannelUnavailableError(DeliveryError):
    """Target channel is temporarily unavailable (e.g., service down)."""

    pass


# -----------------------------------------------------------------------------
# Configuration Errors - Issues with notification setup
# -----------------------------------------------------------------------------


class ConfigurationError(NotificationError):
    """Base class for configuration-related errors."""

    pass


class InvalidChannelError(ConfigurationError):
    """Channel configuration is invalid or channel does not exist."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        channel_id: Optional[str] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.channel_id = channel_id


class ChannelNotFoundError(InvalidChannelError):
    """Target channel (Slack channel, Teams channel, etc.) was not found."""

    pass


class ChannelArchivedError(InvalidChannelError):
    """Target channel has been archived and cannot receive messages."""

    pass


class InvalidRuleError(ConfigurationError):
    """Notification rule configuration is invalid."""

    pass


class MissingConfigurationError(ConfigurationError):
    """Required configuration (env vars, secrets) is missing."""

    def __init__(self, message: str, config_key: Optional[str] = None):
        super().__init__(message)
        self.config_key = config_key


# -----------------------------------------------------------------------------
# Authentication Errors - Issues with tokens and credentials
# -----------------------------------------------------------------------------


class AuthenticationError(NotificationError):
    """Base class for authentication-related errors."""

    pass


class TokenExpiredError(AuthenticationError):
    """OAuth token has expired and could not be refreshed."""

    pass


class TokenRefreshError(AuthenticationError):
    """Failed to refresh OAuth token."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        error_description: Optional[str] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.error_description = error_description


class InvalidCredentialsError(AuthenticationError):
    """Provided credentials are invalid."""

    pass


class InstallationNotFoundError(AuthenticationError):
    """No installation found for the given team/workspace."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        team_id: Optional[str] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.team_id = team_id


# -----------------------------------------------------------------------------
# Processing Errors - Issues during message processing
# -----------------------------------------------------------------------------


class ProcessingError(NotificationError):
    """Base class for message processing errors."""

    pass


class DeduplicationError(ProcessingError):
    """Error during deduplication check."""

    pass


class TemplateRenderError(ProcessingError):
    """Failed to render message template."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        template_name: Optional[str] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.template_name = template_name


class PayloadValidationError(ProcessingError):
    """Message payload failed validation."""

    pass


class MessageTooLargeError(ProcessingError):
    """Message exceeds size limits for the target channel."""

    def __init__(
        self,
        message: str,
        channel: Optional[str] = None,
        tenant_id: Optional[str] = None,
        size: Optional[int] = None,
        max_size: Optional[int] = None,
    ):
        super().__init__(message, channel, tenant_id)
        self.size = size
        self.max_size = max_size


# -----------------------------------------------------------------------------
# Channel-Specific Errors
# -----------------------------------------------------------------------------


class SlackError(NotificationError):
    """Slack-specific error."""

    def __init__(
        self,
        message: str,
        tenant_id: Optional[str] = None,
        slack_error_code: Optional[str] = None,
    ):
        super().__init__(message, channel="slack", tenant_id=tenant_id)
        self.slack_error_code = slack_error_code


class TeamsError(NotificationError):
    """Microsoft Teams-specific error."""

    def __init__(
        self,
        message: str,
        tenant_id: Optional[str] = None,
        teams_error_code: Optional[str] = None,
    ):
        super().__init__(message, channel="ms_teams", tenant_id=tenant_id)
        self.teams_error_code = teams_error_code


class GoogleChatError(NotificationError):
    """Google Chat-specific error."""

    def __init__(
        self,
        message: str,
        tenant_id: Optional[str] = None,
        google_error_code: Optional[str] = None,
    ):
        super().__init__(message, channel="google_chat", tenant_id=tenant_id)
        self.google_error_code = google_error_code


class EmailError(NotificationError):
    """Email delivery error."""

    def __init__(
        self,
        message: str,
        tenant_id: Optional[str] = None,
        smtp_code: Optional[int] = None,
    ):
        super().__init__(message, channel="email", tenant_id=tenant_id)
        self.smtp_code = smtp_code


# -----------------------------------------------------------------------------
# Retry Helpers
# -----------------------------------------------------------------------------


def is_retryable(error: Exception) -> bool:
    """Determine if an error should trigger a retry."""
    retryable_types = (
        RateLimitedError,
        ChannelUnavailableError,
        TokenExpiredError,
    )
    return isinstance(error, retryable_types)


def get_retry_delay(error: Exception, default: int = 5) -> int:
    """Get the recommended retry delay in seconds."""
    if isinstance(error, RateLimitedError) and error.retry_after:
        return error.retry_after
    return default


# -----------------------------------------------------------------------------
# Generic Retry Exceptions (moved from configs/config.py)
# -----------------------------------------------------------------------------


class RetriableException(Exception):
    """Exception that indicates the operation can be retried."""

    pass


class ConcurrencyException(RetriableException):
    """Exception for concurrency conflicts that can be retried."""

    pass


class CounterOverflowException(Exception):
    """Exception for counter overflow conditions."""

    pass
