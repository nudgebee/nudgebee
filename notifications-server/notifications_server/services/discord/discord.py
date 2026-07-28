"""Discord install persistence on the integrations / integration_config_values
store (bot token AES-256-GCM-encrypted at rest), matching Slack / MS Teams.

Legacy messaging_platforms discord rows (pre-migration installs) remain readable
through the load_installations union; a successful save here deletes them so the
tenant's credential lives in exactly one place.
"""

import logging

from notifications_server.models.db_base import BaseDB
from notifications_server.models.enums import EventCategory, EventType, EventAction, EventStatus
from notifications_server.models.models import MessagingPlatform
from notifications_server.services.audit import create_audit_request
from notifications_server.services.messaging_installations import upsert_messaging_integration
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

LOG = logging.getLogger(__name__)


class DiscordService:
    def __init__(self, engine):
        self.session = BaseDB.session(engine)()

    def save_discord_installation(self, tenant_id, bot_token, bot_info, user_email):
        """Create or update the tenant's Discord integration (one per tenant).
        Re-running with a new token rotates the credential in place."""
        bot_username = bot_info.get("username") or "discord_bot"
        try:
            user_id = get_user_id_by_email(user_email) if user_email else None
            integration = upsert_messaging_integration(
                self.session,
                tenant_id,
                "discord",
                name=bot_username,
                config={
                    "bot_token": bot_token,
                    "bot_id": bot_info.get("id"),
                    "client_id": "discord_bot",
                    "installed_by": user_email or bot_username,
                    "team_name": bot_username,
                },
                created_by=user_id,
            )
            self._delete_legacy_rows(tenant_id)

            create_audit_request(
                user_id=user_id,
                tenant_id=tenant_id,
                account_id=None,
                event_time=utc_now(),
                event_category=EventCategory.INTEGRATIONS.value,
                event_type=EventType.NOTIFICATION_DISCORD_CONFIGURATION_CREATE.value,
                event_prev_state=None,
                event_state={"status": "success"},
                event_actor="discord",
                event_target="notification_configuration",
                event_action=EventAction.CREATE.value,
                event_status=EventStatus.SUCCESS.value,
                event_attr={"status": "success"},
            )
            return integration
        except Exception as exc:
            self.session.rollback()
            LOG.exception("Unable to save discord installation: %s", exc)
            raise
        finally:
            self.session.close()

    def _delete_legacy_rows(self, tenant_id):
        """Drop superseded legacy messaging_platforms rows; the integration row is
        authoritative from now on. Failure here must not fail the install."""
        try:
            deleted = (
                self.session.query(MessagingPlatform)
                .filter(MessagingPlatform.tenant_id == tenant_id, MessagingPlatform.platform == "discord")
                .delete()
            )
            if deleted:
                self.session.commit()
                LOG.info("Removed %d legacy discord messaging_platforms row(s) for tenant %s", deleted, tenant_id)
        except Exception:
            self.session.rollback()
            LOG.exception("Failed to clean up legacy discord rows for tenant %s", tenant_id)
