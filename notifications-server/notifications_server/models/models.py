from datetime import datetime, timezone
import json
from typing import Dict, Any
from uuid import UUID

from sqlalchemy import Column, Integer, String, UniqueConstraint, JSON, Boolean, DateTime, ForeignKey, Text
from sqlalchemy.dialects.postgresql import UUID as PG_UUID
from sqlalchemy import inspect
from sqlalchemy.orm import DeclarativeBase, declared_attr
from sqlalchemy.ext.hybrid import hybrid_property
from sqlalchemy.orm import relationship

from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.encode_utils import ModelEncoder, gen_id


def get_current_timestamp():
    return int(datetime.now(timezone.utc).timestamp())


class Base(DeclarativeBase):
    @declared_attr
    # pylint: disable=E0213
    def __tablename__(cls):
        return cls.__name__.lower()

    def to_dict(self):
        return {c.key: getattr(self, c.key) for c in inspect(self).mapper.column_attrs}

    def to_json(self):
        return json.dumps(self.to_dict(), cls=ModelEncoder)


class BaseModel:
    id = Column(String(36), primary_key=True, default=gen_id)
    created_at = Column(Integer, default=get_current_timestamp, nullable=False)
    deleted_at = Column(Integer, default=0, nullable=False)

    @hybrid_property
    def deleted(self):
        return self.deleted_at != 0


class MessagingPlatform(Base):
    __tablename__ = "messaging_platforms"

    id = Column(PG_UUID(as_uuid=True), primary_key=True, default=gen_id)
    created_at = Column(DateTime, default=utc_now)
    updated_at = Column(DateTime, default=utc_now)
    created_by = Column(PG_UUID(as_uuid=True), nullable=True)
    updated_by = Column(PG_UUID(as_uuid=True), nullable=True)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=False)
    platform = Column(String, nullable=False)
    username = Column(String, nullable=True)
    client_id = Column(String, nullable=False)
    app_id = Column(String, nullable=True)
    team_id = Column(String, nullable=True)
    team_name = Column(String, nullable=True)
    token = Column(String, nullable=False)
    token_expires_at = Column(DateTime, nullable=True)
    scopes = Column(String, nullable=True)
    refresh_token = Column(String, nullable=True)
    refresh_token_expires_at = Column(DateTime, nullable=True)
    bot_id = Column(String, nullable=True)
    channels = Column(JSON, nullable=True)
    _to_channels = None

    @property
    def to_channel(self):
        return self._to_channels

    @to_channel.setter
    def to_channel(self, value):
        self._to_channels = value

    def to_dict(self) -> Dict[str, Any]:
        standard_values = {
            "id": str(self.id) if isinstance(self.id, UUID) else self.id,
            "tenant_id": str(self.tenant_id) if isinstance(self.tenant_id, UUID) else self.tenant_id,
            "platform": self.platform,
            "username": self.username,
            "client_id": self.client_id,
            "team_id": self.team_id,
            "team_name": self.team_name,
            "channels": self.channels,
            "token": self.token,
            "token_expires_at": (
                int(self.token_expires_at.replace(tzinfo=timezone.utc).timestamp()) if self.token_expires_at else None
            ),
            "refresh_token": self.refresh_token,
            "refresh_token_expires_at": (
                int(self.refresh_token_expires_at.replace(tzinfo=timezone.utc).timestamp())
                if self.refresh_token_expires_at
                else None
            ),
            "bot_id": self.bot_id,
        }
        return {**standard_values}

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "MessagingPlatform":
        def parse_datetime(value, default=None):
            # to_dict serializes DateTime columns as unix UTC seconds (int).
            # Re-hydrate as naive UTC to stay consistent with how writers store them.
            if isinstance(value, str):
                parsed = datetime.fromisoformat(value)
                if parsed.tzinfo is not None:
                    parsed = parsed.astimezone(timezone.utc).replace(tzinfo=None)
                return parsed
            elif value is not None:
                return datetime.fromtimestamp(value, tz=timezone.utc).replace(tzinfo=None)
            return default

        instance = cls(
            id=data.get("id"),
            tenant_id=data.get("tenant_id"),
            platform=data.get("platform"),
            username=data.get("username"),
            client_id=data.get("client_id"),
            app_id=data.get("app_id"),
            team_id=data.get("team_id"),
            team_name=data.get("team_name"),
            token=data.get("token"),
            refresh_token=data.get("refresh_token"),
            bot_id=data.get("bot_id"),
            channels=data.get("channels"),
        )

        instance.token_expires_at = parse_datetime(data.get("token_expires_at"))
        instance.refresh_token_expires_at = parse_datetime(data.get("refresh_token_expires_at"))

        return instance


class SentNotifications(Base):
    __tablename__ = "sent_notifications"

    id = Column(PG_UUID(as_uuid=True), primary_key=True, default=gen_id)
    created_at = Column(DateTime, nullable=False)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=False)
    fingerprint = Column(String, nullable=False)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)
    slack_team_id = Column(String, nullable=True)
    slack_thread_id = Column(String, nullable=True)
    teams_channel_id = Column(String, nullable=True)
    teams_message_id = Column(String, nullable=True)
    slack_metadata = Column(String, nullable=True)
    teams_metadata = Column(String, nullable=True)


class NotificationRules(Base):
    __tablename__ = "notification_rules"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    created_at = Column(DateTime, nullable=False)
    updated_at = Column(DateTime, nullable=False)
    created_by = Column(PG_UUID(as_uuid=True), nullable=False)
    updated_by = Column(PG_UUID(as_uuid=True), nullable=False)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=False)
    source = Column(String, nullable=False)
    name = Column(String, nullable=False)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)
    description = Column(String, nullable=True)
    cluster = Column(String, nullable=True)
    namespace = Column(String, nullable=True)
    workload = Column(String, nullable=True)
    aggregation_key = Column(String, nullable=True)
    expires_at = Column(DateTime, nullable=True)
    is_suppressed = Column(Boolean, nullable=True)
    is_active = Column(Boolean, nullable=True)
    delivery_mode = Column(String, nullable=True)
    frequency = Column(String, nullable=True)
    severity = Column(String, nullable=True)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": str(self.id) if isinstance(self.id, UUID) else self.id,
            "expires_at": (int(self.expires_at.astimezone(timezone.utc).timestamp()) if self.expires_at else None),
            "tenant_id": str(self.tenant_id) if isinstance(self.tenant_id, UUID) else self.tenant_id,
            "source": self.source,
            "name": self.name,
            "account_id": str(self.account_id) if isinstance(self.account_id, UUID) else self.account_id,
            "cluster": self.cluster,
            "namespace": self.namespace,
            "workload": self.workload,
            "aggregation_key": self.aggregation_key,
            "is_suppressed": self.is_suppressed,
            "is_active": self.is_active,
            "delivery_mode": self.delivery_mode,
            "frequency": self.frequency,
            "severity": self.severity,
        }

    @classmethod
    def from_dict(cls, data: Dict[str, Any]) -> "NotificationRules":
        def parse_datetime(value, default=None):
            # Columns are tz-naive UTC; normalize both ISO strings and unix timestamps
            # to naive UTC so comparisons against utc_now() don't TypeError.
            if isinstance(value, str):
                parsed = datetime.fromisoformat(value)
                if parsed.tzinfo is not None:
                    parsed = parsed.astimezone(timezone.utc).replace(tzinfo=None)
                return parsed
            elif value is not None:
                return datetime.fromtimestamp(value, tz=timezone.utc).replace(tzinfo=None)
            return default

        return cls(
            id=data.get("id"),
            created_at=parse_datetime(data.get("created_at")),
            updated_at=parse_datetime(data.get("updated_at")),
            expires_at=parse_datetime(data.get("expires_at")),
            created_by=data.get("created_by"),
            updated_by=data.get("updated_by"),
            tenant_id=data.get("tenant_id"),
            source=data.get("source"),
            name=data.get("name"),
            account_id=data.get("account_id"),
            description=data.get("description"),
            cluster=data.get("cluster"),
            namespace=data.get("namespace"),
            workload=data.get("workload"),
            aggregation_key=data.get("aggregation_key"),
            is_suppressed=data.get("is_suppressed"),
            is_active=data.get("is_active"),
            delivery_mode=data.get("delivery_mode"),
            frequency=data.get("frequency"),
            severity=data.get("severity"),
        )


class NotificationRuleMappings(Base):
    __tablename__ = "notification_rule_mappings"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    created_at = Column(DateTime, nullable=False)
    updated_at = Column(DateTime, nullable=False)
    created_by = Column(PG_UUID(as_uuid=True), nullable=False)
    updated_by = Column(PG_UUID(as_uuid=True), nullable=False)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=False)
    rule_id = Column(PG_UUID(as_uuid=True), nullable=False)
    platform = Column(String, nullable=False)
    channels = Column(JSON, nullable=False)


class ChannelAccountMapping(Base):
    __tablename__ = "notification_channel_account_mappings"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=False)
    platform = Column(String(50), nullable=False)
    team_id = Column(String(255), nullable=False)
    channel_id = Column(String(255), nullable=False)
    account_id = Column(PG_UUID(as_uuid=True), nullable=False)
    channel_metadata = Column(String, nullable=False)
    created_at = Column(DateTime, default=utc_now, nullable=False)
    updated_at = Column(DateTime, default=utc_now, onupdate=utc_now, nullable=False)
    created_by = Column(PG_UUID(as_uuid=True), nullable=True)
    updated_by = Column(PG_UUID(as_uuid=True), nullable=True)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.id,
            "tenant_id": self.tenant_id,
            "platform": self.platform,
            "team_id": self.team_id,
            "channel_id": self.channel_id,
            "account_id": self.account_id,
            "channel_metadata": self.channel_metadata,
            "created_at": int(self.created_at.astimezone(timezone.utc).timestamp()) if self.created_at else None,
            "updated_at": int(self.updated_at.astimezone(timezone.utc).timestamp()) if self.updated_at else None,
            "created_by": self.created_by,
            "updated_by": self.updated_by,
        }


class ConfigurationStore(Base):
    __tablename__ = "configuration_store"

    id = Column(PG_UUID(as_uuid=True), primary_key=True, default=gen_id)
    config_type = Column(String, nullable=False)
    key = Column(String, nullable=False)
    value = Column(String, nullable=False)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=True)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)
    is_active = Column(Boolean, default=True, nullable=False)
    created_at = Column(DateTime, default=utc_now, nullable=False)
    created_by = Column(PG_UUID(as_uuid=True), nullable=True)


class CloudAccount(Base):
    __tablename__ = "cloud_accounts"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    tenant = Column(PG_UUID(as_uuid=True), nullable=False)
    account_name = Column(String(255), nullable=False)
    account_number = Column(String(255), nullable=True)
    cloud_provider = Column(String(50), nullable=True)
    status = Column(String(50), nullable=True)
    created_at = Column(DateTime, nullable=True)
    updated_at = Column(DateTime, nullable=True)

    # Relationship to agents
    agents = relationship("Agent", back_populates="cloud_account", lazy="joined")

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": str(self.id) if isinstance(self.id, UUID) else self.id,
            "tenant": str(self.tenant) if isinstance(self.tenant, UUID) else self.tenant,
            "account_name": self.account_name,
            "account_number": self.account_number,
            "cloud_provider": self.cloud_provider,
            "status": self.status,
        }


class Agent(Base):
    __tablename__ = "agent"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    cloud_account_id = Column(PG_UUID(as_uuid=True), ForeignKey("cloud_accounts.id"), nullable=False)
    status = Column(String(50), nullable=True)
    created_at = Column(DateTime, nullable=True)
    updated_at = Column(DateTime, nullable=True)

    # Relationship back to cloud account
    cloud_account = relationship("CloudAccount", back_populates="agents", foreign_keys=[cloud_account_id])


class User(Base):
    __tablename__ = "users"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    username = Column(String(255), nullable=False)
    display_name = Column(String(255), nullable=True)
    created_at = Column(DateTime, nullable=True)
    updated_at = Column(DateTime, nullable=True)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": str(self.id),
            "username": self.username,
            "display_name": self.display_name,
        }


class TenantUser(Base):
    __tablename__ = "tenant_users"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    user = Column(PG_UUID(as_uuid=True), nullable=False)
    tenant = Column(PG_UUID(as_uuid=True), nullable=False)
    created_at = Column(DateTime, nullable=True)
    updated_at = Column(DateTime, nullable=True)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "user": str(self.user),
            "tenant": str(self.tenant),
        }


class LlmConversation(Base):
    __tablename__ = "llm_conversations"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    user_id = Column(PG_UUID(as_uuid=True), nullable=True)
    session_id = Column(String(255), nullable=True)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=True)
    status = Column(String(50), nullable=True)
    source = Column(String(100), nullable=True)
    created_at = Column(DateTime, nullable=True)
    updated_at = Column(DateTime, nullable=True)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": str(self.id),
            "user_id": str(self.user_id) if self.user_id else None,
            "session_id": self.session_id,
            "account_id": str(self.account_id) if self.account_id else None,
            "tenant_id": str(self.tenant_id) if self.tenant_id else None,
        }


class SlackOAuthState(Base):
    __tablename__ = "slack_oauth_states"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    state = Column(Text, nullable=False, unique=True)
    expire_at = Column(DateTime, nullable=True)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=True)


class Insight(Base):
    __tablename__ = "insight"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    title = Column(String(255), nullable=True)
    status = Column(String(50), nullable=True)
    source = Column(String(100), nullable=True)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)


class K8sPod(Base):
    __tablename__ = "k8s_pods"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    cloud_resource_id = Column(PG_UUID(as_uuid=True), nullable=True)
    namespace = Column(String(255), nullable=True)
    name = Column(String(255), nullable=True)
    workload_name = Column(String(255), nullable=True)
    cloud_account_id = Column(PG_UUID(as_uuid=True), nullable=True)
    is_active = Column(Boolean, nullable=True)
    creation_time = Column(DateTime, nullable=True)


class CloudResource(Base):
    __tablename__ = "cloud_resourses"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    name = Column(String(255), nullable=True)
    meta = Column(JSON, nullable=True)
    is_active = Column(Boolean, nullable=True)
    account = Column(PG_UUID(as_uuid=True), nullable=True)
    service_name = Column(String(255), nullable=True)
    tenant = Column(PG_UUID(as_uuid=True), nullable=True)


class AiFeedback(Base):
    __tablename__ = "ai_feedback"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=True)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)
    user_id = Column(PG_UUID(as_uuid=True), nullable=True)
    conversation_id = Column(PG_UUID(as_uuid=True), nullable=True)
    message_id = Column(PG_UUID(as_uuid=True), nullable=True)
    vote = Column(String(50), nullable=True)
    feedback = Column(String, nullable=True)
    created_at = Column(DateTime, nullable=True)


class Feature(Base):
    __tablename__ = "feature"

    value = Column(Text, primary_key=True)
    description = Column(Text, nullable=True)


class FeatureFlag(Base):
    __tablename__ = "feature_flag"

    id = Column(PG_UUID(as_uuid=True), primary_key=True)
    feature_id = Column(Text, ForeignKey("feature.value", name="feature_flag_feature_fkey"), nullable=False)
    tenant_id = Column(PG_UUID(as_uuid=True), nullable=False)
    status = Column(Text, nullable=False, default="enabled")
    created_at = Column(DateTime, nullable=False)
    feature_module_id = Column(Text, nullable=True)
    account_id = Column(PG_UUID(as_uuid=True), nullable=True)

    # Relationship to feature table
    feature = relationship("Feature", foreign_keys=[feature_id])

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": str(self.id),
            "feature_id": self.feature_id,
            "tenant_id": str(self.tenant_id),
            "status": self.status,
            "created_at": int(self.created_at.astimezone(timezone.utc).timestamp()) if self.created_at else None,
            "feature_module_id": self.feature_module_id,
            "account_id": str(self.account_id) if self.account_id else None,
        }
