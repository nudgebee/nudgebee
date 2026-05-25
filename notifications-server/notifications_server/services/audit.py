import json
from datetime import datetime
from typing import List, Optional, Any, Dict
import uuid
import requests
import logging
from pydantic import BaseModel, Field, ValidationError, field_validator

from notifications_server.configs import settings
from notifications_server.configs.settings import AUDIT_URL

UUID_REGEX = r"^[0-9a-fA-F-]{36}$"

ERROR_CREATING_AUDIT = "audit: error creating audit"

logger = logging.getLogger(__name__)


class Audit(BaseModel):
    id: Optional[str] = None
    user_id: Optional[str] = Field(None, pattern=UUID_REGEX)
    tenant_id: Optional[str] = Field(None, pattern=UUID_REGEX)
    account_id: Optional[str] = Field(None, pattern=UUID_REGEX)
    event_time: Optional[str] = None
    event_category: str
    event_type: str
    event_prev_state: Optional[Dict[str, Any]] = None
    event_state: Dict[str, Any]
    event_actor: str
    event_target: str
    event_action: str
    event_status: str
    transaction_id: Optional[str] = None
    event_attr: Optional[Dict[str, Any]] = None

    @field_validator("event_time", mode="before")
    @classmethod
    def set_event_time(cls, v):
        return v or datetime.now()


class AuditRequest(BaseModel):
    audits: List[Audit]


class RequestContext:
    def __init__(self, trace_id: Optional[str] = None):
        self.trace_id = trace_id

    def get_trace_id(self):
        return self.trace_id or str(uuid.uuid4())

    def get_logger(self):
        return logger


def validate_audit_request(audit_request: AuditRequest) -> None:
    if not audit_request:
        raise ValueError("audit: auditRequest is required")
    if not audit_request.audits:
        raise ValueError("audit: audits is required")
    for audit in audit_request.audits:
        try:
            audit.validate()
        except ValidationError as e:
            raise ValueError(f"Validation error: {e}")


def value_or_nil(value: Any) -> Any:
    if isinstance(value, str) and value == "":
        return None
    return value


def create_audit_request(
    user_id,
    tenant_id,
    account_id,
    event_time,
    event_category,
    event_type,
    event_prev_state,
    event_state,
    event_actor,
    event_target,
    event_action,
    event_status,
    event_attr,
):
    audit_request = AuditRequest(
        audits=[
            Audit(
                id=str(uuid.uuid4()),
                user_id=user_id,
                tenant_id=tenant_id,
                account_id=account_id,
                event_time=event_time.isoformat(),
                event_category=event_category,
                event_type=event_type,
                event_prev_state=event_prev_state,
                event_state=event_state,
                event_actor=event_actor,
                event_target=event_target,
                event_action=event_action,
                event_status=event_status,
                event_attr=event_attr,
            )
        ]
    )
    create_audit(audit_request)


def create_audit(audit_request: AuditRequest) -> None:
    for audit in audit_request.audits:
        try:
            _post_audit(audit)
        except Exception as e:
            logging.error(ERROR_CREATING_AUDIT, exc_info=e)
            return


def _post_audit(audit: Audit) -> None:
    """Synchronous audit POST — designed to be called via asyncio.to_thread or directly."""
    url = settings.services.api_server + AUDIT_URL
    headers = {"X-ACTION-TOKEN": settings.action_api_server_token}
    requests.post(url, json={"audits": [audit.model_dump()]}, headers=headers, timeout=10)
