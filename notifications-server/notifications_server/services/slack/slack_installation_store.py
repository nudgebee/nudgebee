import logging

import sqlalchemy
from sqlalchemy import (
    Table,
    Column,
    String,
    DateTime,
    Index,
    MetaData,
)
from sqlalchemy.dialects.postgresql import UUID as PG_UUID
from sqlalchemy.engine import Engine
from sqlalchemy.orm import Session
from sqlalchemy.sql.sqltypes import JSON

from slack_sdk.oauth.installation_store.installation_store import InstallationStore

from notifications_server.models.enums import PlatformTypes
from notifications_server.models.models import MessagingPlatform
from notifications_server.services.messaging_installations import upsert_messaging_integration
from notifications_server.utils.encode_utils import gen_id

LOG = logging.getLogger(__name__)


class SlackInstallationStore(InstallationStore):
    default_installations_table_name: str = "messaging_platforms"

    client_id: str
    engine: Engine
    metadata: MetaData
    installations: Table

    @classmethod
    def build_installations_table(cls, metadata: MetaData, table_name: str) -> Table:
        return sqlalchemy.Table(
            table_name,
            metadata,
            Column("id", PG_UUID(as_uuid=True), primary_key=True, default=gen_id),
            Column("created_at", DateTime, nullable=False),
            Column("updated_at", DateTime, nullable=False),
            Column("created_by", PG_UUID(as_uuid=True), nullable=True),
            Column("updated_by", PG_UUID(as_uuid=True), nullable=True),
            Column("tenant_id", PG_UUID(as_uuid=True), nullable=False),
            Column("username", String, nullable=True),
            Column("client_id", String, nullable=False),
            Column("app_id", String, nullable=True),
            Column("team_id", String, nullable=True),
            Column("team_name", String, nullable=True),
            Column("token", String, nullable=False),
            Column("token_expires_at", DateTime, nullable=True),
            Column("scopes", String, nullable=True),
            Column("refresh_token", String, nullable=True),
            Column("refresh_token_expires_at", DateTime, nullable=True),
            Column("bot_id", String, nullable=True),
            Column("channels", JSON, nullable=True),
            Column("platform", String, nullable=False),
            Index(f"{table_name}_idx", "client_id", "team_id", "tenant_id"),
        )

    def __init__(
        self,
        client_id: str,
        engine: Engine,
        installations_table_name: str = default_installations_table_name,
        logger=logging.getLogger(__name__),
    ):
        self.metadata = sqlalchemy.MetaData()
        self.installations = self.build_installations_table(metadata=self.metadata, table_name=installations_table_name)
        self.client_id = client_id
        self._logger = logger
        self.engine = engine

    @property
    def logger(self):
        return self._logger

    def save(self, installation: MessagingPlatform):
        # Slack installs persist to the integrations table with the bot token encrypted
        # at rest (one Slack install per tenant, enforced by the upsert). The legacy
        # messaging_platforms table stays read-only fallback until the data backfill runs.
        config = {
            "bot_token": installation.token,
            "team_name": installation.team_name,
            "bot_id": installation.bot_id,
            "app_id": installation.app_id,
            "client_id": self.client_id,
            "installed_by": installation.username,
            "scopes": installation.scopes,
            "token_expires_at": installation.token_expires_at,
        }
        with Session(self.engine) as session:
            upsert_messaging_integration(
                session,
                tenant_id=installation.tenant_id,
                platform=PlatformTypes.SLACK.value,
                name=installation.team_id,
                config=config,
            )
