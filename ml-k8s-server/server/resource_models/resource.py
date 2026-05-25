import logging
from datetime import UTC, datetime
from typing import Any, Dict
from uuid import UUID

from pydantic import BaseModel, root_validator
from server.utils.utils import DatabaseEngine
from sqlalchemy import text

logger = logging.getLogger(__name__)


DEFAULT_FORMAT = "%Y-%m-%dT%H:%M:%S.%fZ"


class ResourceDBModel(BaseModel):
    @root_validator(pre=True)
    def ensure_timezone_aware(cls, values):
        """
        Ensure all datetime fields are timezone-aware.
        """
        for field_name, value in values.items():
            if isinstance(value, datetime) and value.tzinfo is None:
                # Convert naive datetime to UTC timezone-aware datetime
                values[field_name] = value.replace(tzinfo=UTC)
        return values


class Resource(ResourceDBModel):
    id: UUID
    created_at: datetime
    created_by: UUID | None
    updated_at: datetime
    updated_by: UUID | None
    resourse_id: str
    name: str
    type: str
    status: str
    resourse_created_on: datetime | None
    account: UUID
    cloud_provider: str
    arn: str | None
    tenant: UUID
    tags: Dict[str, Any] = {}
    meta: Dict[str, Any]
    service_name: str | None
    first_seen: datetime | None
    last_seen: datetime
    is_active: bool
    external_resource_id: str | None

    def __eq__(self, value: Any) -> bool:
        if not isinstance(value, self.__class__):
            return False
        return self.resourse_id == value.resourse_id

    def __hash__(self) -> int:
        return hash(self.resourse_id)

    def __init__(self, **data: Any):
        data["tags"] = {}
        data["first_seen"] = (
            datetime.strptime(data["first_seen"], DEFAULT_FORMAT)
            if isinstance(data["first_seen"], str)
            else data["first_seen"]
        )
        data["resourse_created_on"] = (
            datetime.strptime(data["resourse_created_on"], DEFAULT_FORMAT)
            if isinstance(data["resourse_created_on"], str)
            else data["resourse_created_on"]
        )
        data["last_seen"] = (
            datetime.strptime(data["last_seen"], DEFAULT_FORMAT)
            if isinstance(data["last_seen"], str)
            else data["last_seen"]
        )
        super().__init__(**data)

    @classmethod
    def get_from_db(cls, id: UUID):
        try:
            engine = DatabaseEngine.get_engine()
            # Security: use a parameterized query (bound via SQLAlchemy) rather than
            # f-string interpolation to avoid any risk of SQL injection, even if a
            # caller ever bypasses the UUID type hint.
            SQL_QUERY = text("select * from cloud_resourses where id = :id")
            with engine.connect() as conn:
                result = conn.execute(SQL_QUERY, {"id": str(id)})
                row = result.mappings().first()
                if not row:
                    raise ValueError(f"Resource with id {id} not found")
                return cls(**row)
        except Exception as e:
            logger.exception(f"Failed to get resource from db {e}")
            raise ValueError(f"Failed to get resource from db {e}")
