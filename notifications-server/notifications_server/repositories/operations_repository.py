import logging
import uuid
from typing import List, Optional

from sqlalchemy import desc
from sqlalchemy.orm import Session

from notifications_server.models.models import (
    Insight,
    K8sPod,
    AiFeedback,
)
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)


def create_ai_feedback(
    session: Session,
    tenant_id: str,
    account_id: str,
    user_id: str,
    conversation_id: str,
    message_id: str,
    vote: str,
    feedback: str = None,
) -> bool:
    """
    Create AI feedback entry.
    Replaces RPC_CREATE_AI_FEEDBACK mutation.
    """
    try:
        feedback_entry = AiFeedback(
            id=str(uuid.uuid4()),
            tenant_id=tenant_id,
            account_id=account_id,
            user_id=user_id,
            conversation_id=conversation_id,
            message_id=message_id,
            vote=vote,
            feedback=feedback,
            created_at=utc_now(),
        )
        session.add(feedback_entry)
        session.commit()
        return True
    except Exception as e:
        LOG.error("Failed to create AI feedback: %s", e)
        session.rollback()
        return False
