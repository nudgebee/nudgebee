from typing import Any, Dict, List, Optional, Union
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


class ChannelInfo(BaseModel):
    """Channel information for a messaging platform"""

    id: str = Field(..., description="Channel ID")
    name: Optional[str] = Field(None, description="Channel name")
    team_id: Optional[str] = Field(None, description="Team ID (MS Teams)")

    model_config = ConfigDict(extra="allow")


class ChannelMapping(BaseModel):
    """Mapping of platforms to their channels"""

    slack: Optional[List[ChannelInfo]] = Field(None, description="Slack channels")
    ms_teams: Optional[List[ChannelInfo]] = Field(None, description="MS Teams channels", alias="ms_teams")
    google_chat: Optional[List[ChannelInfo]] = Field(None, description="Google Chat spaces", alias="google_chat")


class GenericParameters(BaseModel):
    """Parameters for generic message type"""

    message: str = Field(..., description="Message text to send", min_length=1)

    model_config = ConfigDict(extra="allow")


class FindingData(BaseModel):
    """Finding data structure for troubleshoot notifications"""

    fingerprint: Optional[str] = Field(None, description="Unique fingerprint for deduplication")
    cloud_account_id: Optional[str] = Field(None, description="Cloud account ID")
    subject_namespace: Optional[str] = Field(None, description="Kubernetes namespace")
    subject_owner: Optional[str] = Field(None, description="Resource owner/workload")
    aggregation_key: Optional[str] = Field(None, description="Aggregation key for grouping")
    source: Optional[str] = Field(None, description="Finding source (e.g., 'anomaly')")
    description: Optional[str] = Field(None, description="Finding description")
    evidences: Optional[List[Dict[str, Any]]] = Field(None, description="Evidence list")

    model_config = ConfigDict(extra="allow")


class FindingParameters(BaseModel):
    """Parameters for finding message type"""

    finding: FindingData = Field(..., description="Finding data")

    model_config = ConfigDict(extra="allow")


class TemplateParameters(BaseModel):
    """Parameters for template-based notifications (auto_pilot, slo, optimize, cloud)"""

    account_id: Optional[str] = Field(None, description="Account/cluster ID")
    namespace: Optional[str] = Field(None, description="Kubernetes namespace")
    workload: Optional[str] = Field(None, description="Workload name")
    fingerprint: Optional[str] = Field(None, description="Unique fingerprint for deduplication")

    model_config = ConfigDict(extra="allow")


class ThreadInfo(BaseModel):
    """Thread information for replying to an existing message"""

    message_ts: str = Field(..., description="Parent message timestamp/ID to reply in thread", min_length=1)
    channel_id: str = Field(..., description="Channel ID where the thread exists", min_length=1)
    platform: str = Field(..., description="Platform name (slack, ms_teams, google_chat)", min_length=1)
    team_id: Optional[str] = Field(None, description="Team/workspace ID")

    model_config = ConfigDict(extra="forbid")


class SendMessageRequest(BaseModel):
    """Request model for /api/messages/send endpoint"""

    tenant_id: str = Field(..., description="Tenant identifier", min_length=1)
    type: str = Field(
        default="generic",
        description="Message type: 'generic', 'finding', or template name",
    )
    parameters: Union[GenericParameters, FindingParameters, TemplateParameters, Dict[str, Any]] = Field(
        ..., description="Message parameters (type-specific)"
    )
    source: Optional[str] = Field(
        None,
        description="Notification source for rule matching",
    )
    platform: Optional[str] = Field(
        None,
        description="Target platform (auto-detected if not specified)",
    )
    channels: Optional[Union[ChannelMapping, Dict[str, Any]]] = Field(
        None, description="Channel configuration per platform"
    )
    thread: Optional[ThreadInfo] = Field(
        None,
        description=(
            "Thread information for replying to an existing message. When provided, the message will be sent as a reply"
            " in the specified thread."
        ),
    )

    model_config = ConfigDict(extra="allow")  # Allow extra fields for backward compatibility

    @field_validator("tenant_id")
    @classmethod
    def validate_tenant_id(cls, v):
        """Validate tenant_id is not empty"""
        if not v or not v.strip():
            raise ValueError("tenant_id cannot be empty")
        return v.strip()

    @field_validator("type")
    @classmethod
    def validate_type(cls, v):
        """Validate and normalize message type"""
        if not v or not v.strip():
            return "generic"
        return v.strip()

    @model_validator(mode="before")
    @classmethod
    def validate_parameters(cls, values):
        """Validate parameters based on message type"""
        msg_type = values.get("type", "generic")
        parameters = values.get("parameters")

        if not parameters:
            raise ValueError("parameters is required")

        params = parameters

        # Validate generic type
        if msg_type in ("generic", None) and "message" not in params:
            raise ValueError("parameters.message is required for generic message type")

        # Validate finding type
        if msg_type == "finding" and "finding" not in params:
            raise ValueError("parameters.finding is required for finding message type")

        return values


class PlatformResponse(BaseModel):
    """Response from a single platform after sending message"""

    platform: str = Field(..., description="Platform name (slack, ms_teams, google_chat, system)")
    team_id: Optional[str] = Field(None, description="Team/workspace ID (Slack, MS Teams)")
    channel_id: Optional[str] = Field(
        None, description="Channel ID where message was sent (includes Space ID for Google Chat)"
    )
    message_ts: Optional[str] = Field(None, description="Message timestamp/ID")
    status: str = Field(
        ...,
        description="Send status (success, failed, suppressed, snoozed, batch_delivery, queued, no_platform)",
    )
    reason: Optional[str] = Field(None, description="Additional information about the status")

    model_config = ConfigDict(extra="allow")


class SendMessageResponse(BaseModel):
    """Response model for /api/messages/send endpoint"""

    responses: List[PlatformResponse] = Field(default_factory=list, description="Array of platform-specific responses")

    @property
    def success(self) -> bool:
        """Check if all platform sends were successful"""
        return len(self.responses) > 0 and all(r.status == "success" for r in self.responses)

    @property
    def platforms_sent(self) -> List[str]:
        """List of platforms where message was sent"""
        return [r.platform for r in self.responses]

    model_config = ConfigDict(extra="allow")


class GetThreadMessagesRequest(BaseModel):
    """Request model for /api/threads/messages endpoint"""

    tenant_id: str = Field(..., description="Tenant identifier", min_length=1)
    channel_id: str = Field(..., description="Channel ID where the thread exists", min_length=1)
    thread_ts: str = Field(..., description="Thread timestamp (parent message ts)", min_length=1)
    team_id: Optional[str] = Field(None, description="Team/workspace ID (optional if tenant has single workspace)")

    @field_validator("tenant_id", "channel_id", "thread_ts")
    @classmethod
    def validate_not_empty(cls, v, info):
        if not v or not v.strip():
            raise ValueError(f"{info.field_name} cannot be empty")
        return v.strip()


class ReactionInfo(BaseModel):
    """Reaction information on a message"""

    name: str = Field(..., description="Emoji name (without colons)")
    count: int = Field(..., description="Number of users who reacted with this emoji")
    users: List[str] = Field(default_factory=list, description="List of user IDs who reacted")


class UserInfo(BaseModel):
    """User information"""

    id: str = Field(..., description="User ID")
    name: Optional[str] = Field(None, description="Username")
    real_name: Optional[str] = Field(None, description="User's real name")
    display_name: Optional[str] = Field(None, description="User's display name")
    is_bot: bool = Field(default=False, description="Whether the user is a bot")


class ThreadMessage(BaseModel):
    """A single message in a thread"""

    ts: str = Field(..., description="Message timestamp")
    text: str = Field(..., description="Message text content")
    user_id: Optional[str] = Field(None, description="User ID who sent the message")
    user: Optional[UserInfo] = Field(None, description="User information")
    reactions: List[ReactionInfo] = Field(default_factory=list, description="Reactions on this message")
    is_parent: bool = Field(default=False, description="Whether this is the parent message of the thread")
    reply_count: Optional[int] = Field(None, description="Number of replies (only on parent message)")


class GetThreadMessagesResponse(BaseModel):
    """Response model for /api/threads/messages endpoint"""

    success: bool = Field(..., description="Whether the request was successful")
    channel_id: str = Field(..., description="Channel ID")
    thread_ts: str = Field(..., description="Thread timestamp")
    messages: List[ThreadMessage] = Field(default_factory=list, description="Messages in the thread")
    has_more: bool = Field(default=False, description="Whether there are more messages to fetch")
    error: Optional[str] = Field(None, description="Error message if request failed")
