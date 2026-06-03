import logging
import uuid
from datetime import timedelta

from sqlalchemy.exc import IntegrityError

from notifications_server.exceptions.common_exc import WrongArgumentsException, BeeException
from notifications_server.exceptions.exceptions import Err
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.models.models import MessagingPlatform
from notifications_server.services.audit import create_audit_request
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

LOG = logging.getLogger(__name__)


class MsTeamsService:
    def __init__(self, engine):
        self.session = BaseDB.session(engine)()

    def save_teams_installation(self, client_id, tenant_id, result, account, user_email):
        try:
            teams_installation = MessagingPlatform()
            teams_installation.platform = "ms_teams"
            teams_installation.id = uuid.uuid4()
            teams_installation.created_at = utc_now()
            teams_installation.updated_at = utc_now()
            teams_installation.client_id = account["local_account_id"]
            teams_installation.team_id = account["home_account_id"]
            teams_installation.tenant_id = tenant_id
            teams_installation.token = result["access_token"]
            expires_in = result.get("expires_in")
            teams_installation.token_expires_at = utc_now() + timedelta(seconds=expires_in - 60) if expires_in else None
            teams_installation.refresh_token = result["refresh_token"]
            teams_installation.username = result["id_token_claims"]["preferred_username"]
            teams_installation.app_id = client_id

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
            self.session.add(teams_installation)
            self.session.commit()
            self.session.close()
            return teams_installation
        except IntegrityError as exc:
            LOG.exception("Unable to save teams installation: %s", exc)
            raise WrongArgumentsException(Err.OS0009, [exc])
        except Exception as exc:
            LOG.exception("Unable to save teams installation: %s", exc)
            raise BeeException(Err.OS0009, [exc])
