import logging
import uuid
from sqlite3 import IntegrityError

import google_auth_oauthlib.flow

from notifications_server.configs import settings
from notifications_server.exceptions.common_exc import WrongArgumentsException
from notifications_server.exceptions.exceptions import Err
from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.models.models import MessagingPlatform
from notifications_server.services.audit import create_audit_request
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

LOG = logging.getLogger(__name__)


class GoogleChatService:
    def __init__(self, engine):
        self.session = BaseDB.session(engine)()

    def save_google_chat_installation(self, tenant_id, result, user_email):
        try:
            g_chat_installation = MessagingPlatform()
            g_chat_installation.platform = "google_chat"
            g_chat_installation.id = uuid.uuid4()
            g_chat_installation.created_at = utc_now()
            g_chat_installation.updated_at = utc_now()
            g_chat_installation.client_id = settings.google_chat.client_id
            g_chat_installation.tenant_id = tenant_id
            g_chat_installation.token = result.token
            g_chat_installation.refresh_token = result.refresh_token
            g_chat_installation.token_expires_at = result.expiry
            g_chat_installation.scopes = ",".join(result.scopes)

            user_id = get_user_id_by_email(user_email)

            create_audit_request(
                user_id=user_id,
                tenant_id=tenant_id,
                account_id=None,
                event_time=utc_now(),
                event_category=EventCategory.INTEGRATIONS.value,
                event_type=EventType.NOTIFICATION_GCHAT_CONFIGURATION_CREATE.value,
                event_prev_state=None,
                event_state={"status": "success"},
                event_actor=EventActor.GOOGLE_CHAT.value,
                event_target="notification_configuration",
                event_action=EventAction.CREATE.value,
                event_status=EventStatus.SUCCESS.value,
                event_attr={"status": "success"},
            )
            self.session.add(g_chat_installation)
            self.session.commit()
            self.session.close()
            return g_chat_installation
        except IntegrityError as exc:
            LOG.exception("Unable to save goggle chat installation: %s", exc)
            raise WrongArgumentsException(Err.OS0009, [exc])

    @staticmethod
    def get_google_flow():
        flow = google_auth_oauthlib.flow.Flow.from_client_config(
            {
                "web": {
                    "client_id": settings.google_chat.client_id,
                    "client_secret": settings.google_chat.client_secret,
                    "redirect_uris": [],
                    "auth_uri": "https://accounts.google.com/o/oauth2/auth",
                    "token_uri": "https://oauth2.googleapis.com/token",
                }
            },
            scopes=[
                "openid",
                "https://www.googleapis.com/auth/userinfo.email",
                "https://www.googleapis.com/auth/chat.messages.create",
                "https://www.googleapis.com/auth/chat.messages.reactions.create",
                "https://www.googleapis.com/auth/chat.spaces.readonly",
                "https://www.googleapis.com/auth/userinfo.profile",
                "https://www.googleapis.com/auth/chat",
            ],
        )
        flow.redirect_uri = settings.google_chat_redirect_uri
        return flow
