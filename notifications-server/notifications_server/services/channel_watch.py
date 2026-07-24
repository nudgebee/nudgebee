import logging

from slack_sdk.errors import SlackApiError

from notifications_server.configs.settings import settings
from notifications_server.models.db_base import BaseDB
from notifications_server.repositories import channel_watch_repository
from notifications_server.services.cache import Cache
from notifications_server.services.messaging_installations import load_installations
from notifications_server.utils.feature_flag_utils import is_feature_enabled

LOG = logging.getLogger(__name__)

cache = Cache()

FEATURE_FLAG_ID = "CHANNEL_AWARENESS"

# Posted in-channel on every enable. Consent requirement: if this post fails,
# the enable is rolled back rather than silently watching.
DISCLOSURE_TEXT = (
    ":wave: Nubi is now following this channel so it has context when someone asks it for help. "
    "It only takes action when explicitly @mentioned — everything else is reference only. "
    "This can be turned off anytime in NudgeBee under Settings → Integrations."
)


class ChannelWatchService:
    """Registry for Nubi channel awareness: which channels the bot passively
    follows. Slack only for now; rows are platform-tagged so other messaging
    platforms can reuse the table."""

    def __init__(self, engine, slack_app):
        self.engine = engine
        self.slack_app = slack_app
        self._scoped_session = BaseDB.session(self.engine)
        self.session = self._scoped_session()

    def close(self):
        try:
            self._scoped_session.remove()
        except Exception:
            pass

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
        return False

    def _awareness_gate(self, tenant_id):
        """Error dict when channel awareness may not be enabled; None when allowed.
        Only gates enabling/listing — disabling must always work."""
        if not settings.notifications.channel_awareness_enabled:
            return {"error": {"message": "Channel awareness is disabled on this environment"}}
        if not is_feature_enabled(self.session, FEATURE_FLAG_ID, tenant_id):
            return {"error": {"message": "Channel awareness is not enabled for this tenant"}}
        return None

    def _resolve_installation(self, tenant_id, platform, team_id, unambiguous=False):
        """(installation, error) resolved strictly within this tenant's installations —
        a caller-supplied team_id must never reach another tenant's workspace. With
        unambiguous=True, refuses to guess between multiple workspaces."""
        installations = load_installations(self.session, tenant_id, platform)
        if not installations:
            return None, None
        if team_id:
            installation = next((i for i in installations if i.team_id == team_id), None)
            if not installation:
                return None, {"error": {"message": "No Slack installation for this workspace in your tenant"}}
            return installation, None
        if unambiguous and len(installations) > 1:
            return None, {"error": {"message": "Multiple Slack workspaces are connected; specify team_id"}}
        return installations[0], None

    def list_watchable_channels(self, tenant_id, platform="slack", team_id=None):
        if platform != "slack":
            return {"error": {"message": f"Platform {platform} is not supported yet. Only 'slack' is supported."}}
        gate_error = self._awareness_gate(tenant_id)
        if gate_error:
            return gate_error

        installation, error = self._resolve_installation(tenant_id, platform, team_id)
        if error:
            return error
        if not installation:
            LOG.info("No %s installation for tenant %s; nothing watchable", platform, tenant_id)
            return {"data": []}

        watches = channel_watch_repository.list_channel_watches(self.session, tenant_id)
        if watches is None:
            return {"error": {"message": "Unable to load channel watch state"}}
        watch_map = {(w["team_id"], w["channel_id"]): w for w in watches if w["platform"] == platform}
        watcher_names = channel_watch_repository.get_display_names(self.session, [w.get("created_by") for w in watches])

        channels = []
        next_cursor = None
        partial = False
        while True:
            try:
                response = self.slack_app.client.channels_list(
                    installation.token, installation.team_id, cursor=next_cursor
                )
            except SlackApiError as e:
                if e.response.headers.get("Retry-After"):
                    LOG.warning("Slack rate limited during watchable list, returning %d channels", len(channels))
                    partial = True
                    break
                raise

            for channel in response.get("channels", []):
                watch = watch_map.get((installation.team_id, channel["id"]))
                is_watched = bool(watch and watch["enabled"])
                channels.append(
                    {
                        "id": channel["id"],
                        "name": channel["name"],
                        "is_private": channel.get("is_private", False),
                        "is_member": channel.get("is_member", False),
                        "watched": is_watched,
                        "retention_days": watch["retention_days"] if watch else None,
                        # updated_at reflects the most recent (re-)enable, unlike
                        # created_at which keeps the first consent ever given.
                        "watched_since": watch["updated_at"] if is_watched else None,
                        "watched_by": watcher_names.get(watch["created_by"]) if is_watched else None,
                    }
                )

            metadata = response.get("response_metadata") or {}
            next_cursor = metadata.get("next_cursor")
            if not next_cursor:
                break

        return {"data": channels, "team_id": installation.team_id, "partial": partial}

    def enable_watch(self, tenant_id, channel_id, team_id=None, channel_name=None, created_by=None):
        platform = "slack"
        gate_error = self._awareness_gate(tenant_id)
        if gate_error:
            return gate_error

        installation, error = self._resolve_installation(tenant_id, platform, team_id, unambiguous=True)
        if error:
            return error
        if not installation:
            return {"error": {"message": f"No Slack installation found for tenant: {tenant_id}"}}
        team_id = installation.team_id

        try:
            info = self.slack_app.client.conversations_info(token=installation.token, channel_id=channel_id)
            channel_info = info.get("channel", {})
            channel_name = channel_name or channel_info.get("name")
            if not channel_info.get("is_member", False):
                if channel_info.get("is_private", False):
                    return {
                        "error": {
                            "message": "Nubi is not a member of this private channel. "
                            "Invite @Nubi in Slack first, then try again."
                        }
                    }
                self.slack_app.client.conversations_join(token=installation.token, channel_id=channel_id)
            # Disclosure BEFORE the registry write: if anything fails from here on,
            # the failure mode is a stray notice (visible, fails safe) rather than
            # watching-without-disclosure — the state this feature must never reach.
            disclosure = self.slack_app.client.chat_post(
                token=installation.token, channel_id=channel_id, text=DISCLOSURE_TEXT
            )
        except SlackApiError as e:
            slack_error = e.response.get("error", "unknown_error")
            if slack_error == "channel_not_found":
                # Private channels the bot isn't in are invisible to it, so this is
                # almost always "invite the bot first" rather than a bad channel id.
                return {
                    "error": {
                        "message": "Nubi can't see this channel. If it's private, "
                        "invite @Nubi in Slack first, then try again."
                    }
                }
            LOG.warning("Slack error enabling watch on %s/%s: %s", team_id, channel_id, slack_error)
            return {"error": {"message": f"Slack rejected the request: {slack_error}"}}

        watch = channel_watch_repository.upsert_channel_watch(
            self.session,
            tenant_id=tenant_id,
            platform=platform,
            team_id=team_id,
            channel_id=channel_id,
            channel_name=channel_name,
            created_by=created_by,
        )
        if not watch:
            # Best-effort cleanup of the notice we just posted; if this also fails
            # the leftover is an over-disclosure, never an undisclosed watch.
            try:
                self.slack_app.client.chat_delete(
                    token=installation.token, channel_id=channel_id, ts=disclosure.get("ts")
                )
            except Exception:
                LOG.warning("Could not remove disclosure after failed save in %s/%s", team_id, channel_id)
            return {"error": {"message": "Unable to save the channel watch"}}
        cache.add_watched_channel(platform, team_id, channel_id)

        return {"data": watch}

    def disable_watch(self, tenant_id, channel_id, team_id=None):
        platform = "slack"
        watch = channel_watch_repository.disable_channel_watch(
            self.session, tenant_id=tenant_id, platform=platform, channel_id=channel_id, team_id=team_id
        )
        if not watch:
            return {"error": {"message": "No channel watch found for this channel"}}
        cache.remove_watched_channel(platform, watch["team_id"], channel_id)
        return {"data": watch}
