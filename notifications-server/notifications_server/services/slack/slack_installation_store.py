import logging
from datetime import datetime
from typing import Optional

import sqlalchemy
from sqlalchemy import (
    Table,
    Column,
    String,
    DateTime,
    Index,
    and_,
    MetaData,
)
from sqlalchemy.dialects.postgresql import UUID as PG_UUID
from sqlalchemy.engine import Engine
from sqlalchemy.sql.sqltypes import JSON

from slack_sdk.oauth.installation_store.installation_store import InstallationStore

from notifications_server.models.enums import PlatformTypes
from notifications_server.models.models import MessagingPlatform
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
        with self.engine.begin() as conn:
            i = installation.to_dict()
            i["client_id"] = self.client_id
            i["platform"] = PlatformTypes.SLACK.value

            i_column = self.installations.c
            installations_rows = conn.execute(
                sqlalchemy.select(i_column.id)
                .where(
                    and_(
                        i_column.client_id == self.client_id,
                        i_column.team_id == installation.team_id,
                        i_column.tenant_id == installation.tenant_id,
                    )
                )
                .limit(1)
            )
            installations_row_id: Optional[str] = None
            for row in installations_rows.mappings():
                installations_row_id = row["id"]
            if installations_row_id is None:
                conn.execute(self.installations.insert(), i)
            else:
                update_statement = self.installations.update().where(i_column.id == installations_row_id).values(**i)
                conn.execute(update_statement, i)
