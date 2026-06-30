import logging
from datetime import timedelta

from sqlalchemy.exc import IntegrityError

from notifications_server.exceptions.common_exc import WrongArgumentsException, BeeException
from notifications_server.exceptions.exceptions import Err
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.services.audit import create_audit_request
from notifications_server.services.messaging_installations import upsert_messaging_integration
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

LOG = logging.getLogger(__name__)


class MsTeamsService:
    def __init__(self, engine):
        self.session = BaseDB.session(engine)()

    def save_teams_installation(self, client_id, tenant_id, result, account, user_email):
        try:
            expires_in = result.get("expires_in")
            token_expires_at = utc_now() + timedelta(seconds=expires_in - 60) if expires_in else None
            # MS Teams installs persist to the integrations table with the Graph access
            # and refresh tokens encrypted at rest (one Teams install per tenant). The
            # integration name is the Azure AD account id (the legacy team_id).
            config = {
                "access_token": result["access_token"],
                "refresh_token": result["refresh_token"],
                "token_expires_at": token_expires_at,
                "client_id": account["local_account_id"],
                "app_id": client_id,
                "installed_by": result["id_token_claims"]["preferred_username"],
            }

            user_id = get_user_id_by_email(user_email)

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
                event_attr={"status": "success"},
            )

            integration = upsert_messaging_integration(
                self.session,
                tenant_id=tenant_id,
                platform="ms_teams",
                name=account["home_account_id"],
                config=config,
                created_by=user_id,
            )
            return integration
        except IntegrityError as exc:
            LOG.exception("Unable to save teams installation: %s", exc)
            raise WrongArgumentsException(Err.OS0009, [exc])
        except Exception as exc:
            LOG.exception("Unable to save teams installation: %s", exc)
            raise BeeException(Err.OS0009, [exc])
        finally:
            # Close on every path: the success-only close leaked the session
            # (and its pooled DB connection) whenever an exception was raised.
            self.session.close()
