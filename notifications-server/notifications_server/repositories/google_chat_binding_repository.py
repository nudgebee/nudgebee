import logging
from dataclasses import dataclass
from typing import Optional

from sqlalchemy.orm import Session

from notifications_server.models.models import Integration, IntegrationConfigValue

LOG = logging.getLogger(__name__)

GOOGLE_CHAT_SPACE_INTEGRATION_TYPE = "google_chat_space"


@dataclass(frozen=True)
class GoogleChatBinding:
    """A resolved Google Chat space → tenant binding."""

    integration_id: str
    tenant_id: str
    space_id: str
    display_name: str


def find_google_chat_binding(session: Session, space_id: str) -> Optional[GoogleChatBinding]:
    """Look up the binding for a Google Chat space.

    Hot path — called at most once per inbound MESSAGE / ADDED_TO_SPACE event.
    No cache here: api-server's `core.InvalidateIntegrationCache()` only clears
    api-server's in-process cache; notifications-server queries Postgres
    directly on every miss. If a cache is later added, also wire an
    invalidation signal from api-server's `integrations_create_config` handler.

    Returns None only when the space genuinely has no enabled binding. The
    binding lookup itself is deliberately NOT wrapped in a try/except: a DB
    error must propagate so the caller can fail closed, instead of being
    indistinguishable from "no binding" — which would silently downgrade a
    bound (secured) space to the permissive legacy resolution path.
    """
    if not space_id:
        return None

    integration = (
        session.query(Integration)
        .filter(
            Integration.type == GOOGLE_CHAT_SPACE_INTEGRATION_TYPE,
            Integration.name == space_id,
            # Mirror api-server's read paths, which treat disabled integrations
            # as soft-removed (`status != 'disabled'`).
            Integration.status != "disabled",
        )
        .first()
    )
    if not integration:
        return None

    # display_name is a cosmetic UI label — degrade to "" if its lookup fails
    # rather than failing the whole, already-resolved binding.
    display_name = ""
    try:
        display_row = (
            session.query(IntegrationConfigValue)
            .filter(
                IntegrationConfigValue.integration_id == integration.id,
                IntegrationConfigValue.name == "display_name",
            )
            .first()
        )
        if display_row and display_row.value:
            display_name = display_row.value
    except Exception as e:
        LOG.warning("Failed to load display_name for Google Chat space %s: %s", space_id, e)

    return GoogleChatBinding(
        integration_id=str(integration.id),
        tenant_id=str(integration.tenant_id),
        space_id=integration.name,
        display_name=display_name,
    )
