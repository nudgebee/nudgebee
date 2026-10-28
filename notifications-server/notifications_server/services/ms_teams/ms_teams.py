import logging

from sqlalchemy.exc import IntegrityError

from notifications_server.configs.settings import settings
from notifications_server.exceptions.common_exc import WrongArgumentsException, BeeException
from notifications_server.exceptions.exceptions import Err
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.repositories.oauth_repository import find_installation_by_tenant_and_platform
from notifications_server.services.audit import create_audit_request
from notifications_server.services.messaging_installations import (
    ms_teams_aad_tenant_owner,
    upsert_messaging_integration,
)
from notifications_server.services.ms_teams.bot_installations import aad_tenant_id_from_account_id
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

LOG = logging.getLogger(__name__)

CONNECTED = "connected"
ALREADY_CONNECTED = "already_connected"
TENANT_LINKED_ELSEWHERE = "tenant_linked_elsewhere"


def aad_tenant_id_of(token_result, account) -> str:
    """Microsoft tenant id of the account that completed Connect."""
    tid = (token_result.get("id_token_claims") or {}).get("tid")
    return tid or aad_tenant_id_from_account_id(account.get("home_account_id"))


def connect_ms_teams_tenant(
    session, tenant_id, aad_tenant_id, installed_by, client_id, user_email, audit_attr=None
) -> str:
    """Connect a Microsoft tenant to a NudgeBee tenant, for both Microsoft
    sign-in Connect and the bot's connect link. One Teams connection per
    tenant, and one tenant per Microsoft tenant: ownership counts disabled
    connections, so a deleted connection can be restored by its own tenant
    only. The caller owns the session; it is neither opened nor closed here.

    Returns CONNECTED, ALREADY_CONNECTED or TENANT_LINKED_ELSEWHERE."""
    if find_installation_by_tenant_and_platform(session, tenant_id, "ms_teams"):
        return ALREADY_CONNECTED
    owner = ms_teams_aad_tenant_owner(session, aad_tenant_id)
    if owner and owner != str(tenant_id):
        return TENANT_LINKED_ELSEWHERE
    try:
        # The integration name is the Microsoft tenant id. The bot sends with its
        # own credentials, so no user token is stored. Tokens from an earlier
        # Graph connection are left as they were, unused. client_id is the
        # Microsoft account that signed in, which the bot's link doesn't have.
        config = {"client_id": client_id, "app_id": settings.ms_teams.client_id, "installed_by": installed_by}
        user_id = get_user_id_by_email(user_email)
        upsert_messaging_integration(
            session,
            tenant_id=tenant_id,
            platform="ms_teams",
            name=aad_tenant_id,
            config=config,
            created_by=user_id,
        )
        # Audit the success only after the integration is actually written, so a
        # failed upsert can't leave a "success" audit behind.
        create_audit_request(
            user_id=user_id,
            tenant_id=tenant_id,
            account_id=None,
            event_time=utc_now(),
            event_category=EventCategory.INTEGRATIONS.value,
            event_type=EventType.NOTIFICATION_MSTEAMS_CONFIGURATION_CREATE.value,
            event_prev_state=None,
            event_state={"status": "success"},
            event_actor=EventActor.MS_TEAMS.value,
            event_target="notification_configuration",
            event_action=EventAction.CREATE.value,
            event_status=EventStatus.SUCCESS.value,
            event_attr={"status": "success", **(audit_attr or {})},
        )
    except IntegrityError as exc:
        LOG.exception("Unable to save teams installation: %s", exc)
        raise WrongArgumentsException(Err.OS0009, [exc])
    except Exception as exc:
        LOG.exception("Unable to save teams installation: %s", exc)
        raise BeeException(Err.OS0009, [exc])
    return CONNECTED
