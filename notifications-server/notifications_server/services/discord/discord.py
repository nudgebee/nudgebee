import logging
import uuid
from sqlalchemy.exc import IntegrityError

from notifications_server.exceptions.common_exc import WrongArgumentsException
from notifications_server.exceptions.exceptions import Err
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.models.models import MessagingPlatform
from notifications_server.services.audit import create_audit_request
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

LOG = logging.getLogger(__name__)


class DiscordService:
    def __init__(self, engine):
        self.session = BaseDB.session(engine)()

    def save_discord_installation(self, tenant_id, bot_token, bot_info, user_email):
        try:
            installation = MessagingPlatform()
            installation.id = uuid.uuid4()
            installation.platform = "discord"
            installation.client_id = "discord_bot"
            installation.tenant_id = tenant_id
            installation.token = bot_token
            installation.username = user_email or bot_info.get("username", "discord_bot")
            installation.bot_id = bot_info.get("id")
            installation.created_at = utc_now()
            installation.updated_at = utc_now()

            user_id = get_user_id_by_email(user_email) if user_email else None

            create_audit_request(
                user_id=user_id,
                tenant_id=tenant_id,
                account_id=None,
                event_time=utc_now(),
                event_category=EventCategory.INTEGRATIONS.value,
                event_type=(
                    EventType.NOTIFICATION_DISCORD_CONFIGURATION_CREATE.value
                    if hasattr(EventType, "NOTIFICATION_DISCORD_CONFIGURATION_CREATE")
                    else "notification.discord_configuration.create"
                ),
                event_prev_state=None,
                event_state={"status": "success"},
                event_actor="discord",
                event_target="notification_configuration",
                event_action=EventAction.CREATE.value,
                event_status=EventStatus.SUCCESS.value,
                event_attr={"status": "success"},
            )
            self.session.add(installation)
            self.session.commit()
            return installation
        except IntegrityError as exc:
            self.session.rollback()
            LOG.exception("Unable to save discord installation: %s", exc)
            raise WrongArgumentsException(Err.OS0009, [exc])
        except Exception as exc:
            self.session.rollback()
            LOG.exception("Unexpected error saving discord installation: %s", exc)
            raise
        finally:
            self.session.close()
