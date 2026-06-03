from urllib.parse import urlparse, parse_qs

from sqlalchemy import create_engine
from sqlalchemy.ext.asyncio import create_async_engine

from notifications_server.configs import settings
from notifications_server.models.db_base import BaseDB

# Driver URL prefixes
PG_BASE = "postgresql://"
PG_PSYCOPG2 = "postgresql+psycopg2://"
PG_ASYNCPG = "postgresql+asyncpg://"


class PostgresSQLDB(BaseDB):
    def __init__(self):
        super().__init__()
        self._async_engine = None
        self._sync_engine = None

    def _get_base_url_and_sslmode(self):
        parsed_url = urlparse(settings.database.url)
        query_params = parse_qs(parsed_url.query)
        sslmode = query_params.get("sslmode", ["require"])[0]
        db_url_without_query = parsed_url._replace(query="").geturl()
        return db_url_without_query, sslmode

    def _get_engine(self):
        """Returns the async engine as the primary engine."""
        return self._get_async_engine()

    def _get_async_engine(self):
        if self._async_engine is None:
            db_url, sslmode = self._get_base_url_and_sslmode()

            if db_url.startswith(PG_BASE):
                db_url = db_url.replace(PG_BASE, PG_ASYNCPG, 1)
            elif db_url.startswith(PG_PSYCOPG2):
                db_url = db_url.replace(PG_PSYCOPG2, PG_ASYNCPG, 1)

            self._async_engine = create_async_engine(
                db_url,
                pool_recycle=120,
                pool_pre_ping=True,
                pool_size=settings.database.pool_size,
                max_overflow=settings.database.max_overflow,
                pool_timeout=30,
                connect_args={"ssl": sslmode} if sslmode and sslmode != "disable" else {},
            )
        return self._async_engine

    def _get_sync_engine(self):
        if self._sync_engine is None:
            db_url, sslmode = self._get_base_url_and_sslmode()

            if db_url.startswith(PG_BASE):
                db_url = db_url.replace(PG_BASE, PG_PSYCOPG2, 1)

            connect_args = {
                "keepalives": 1,
                "keepalives_idle": 30,
                "keepalives_interval": 10,
                "keepalives_count": 5,
                "sslmode": sslmode,
            }
            self._sync_engine = create_engine(
                db_url,
                pool_recycle=120,
                pool_pre_ping=True,
                pool_size=settings.database.sync_pool_size,
                max_overflow=settings.database.sync_max_overflow,
                pool_timeout=30,
                connect_args=connect_args,
            )
        return self._sync_engine

    @property
    def async_engine(self):
        return self._get_async_engine()

    @property
    def sync_engine(self):
        return self._get_sync_engine()
