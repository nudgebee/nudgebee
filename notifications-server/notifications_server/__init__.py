import logging
import os

import msal
from slack_bolt import App
from slack_bolt.oauth.oauth_settings import OAuthSettings
from slack_sdk.oauth.state_store.sqlalchemy import SQLAlchemyOAuthStateStore

from notifications_server.clients.slack_client import SlackClient
from notifications_server.models.db_factory import DBFactory, DBType
from notifications_server.services.slack.slack_installation_store import SlackInstallationStore
from notifications_server.services.slack.slack_oauth_flow import SlackOAuthFlow

LOG = logging.getLogger(__name__)


def get_db_engine():
    db = DBFactory(DBType.PostgresSQL).db
    return db


_db = get_db_engine()
# Primary async engine for all DB operations
engine = _db.engine
# Secondary sync engine for Slack OAuth (slack_sdk requires sync)
sync_engine = _db.sync_engine


def make_slack_app():
    slack_signing_secret = os.environ.get("SLACK_SIGNING_SECRET", "signing_secret")
    slack_client_id = os.environ.get("SLACK_CLIENT_ID", "client_id")
    slack_client_secret = os.environ.get("SLACK_CLIENT_SECRET", "client_secret")

    installation_store = SlackInstallationStore(
        client_id=slack_client_id,
        engine=sync_engine,
        logger=LOG,
    )
    oauth_state_store = SQLAlchemyOAuthStateStore(
        expiration_seconds=180,
        engine=sync_engine,
        logger=LOG,
    )
    client = SlackClient(installation_store=installation_store)
    oauth_settings = OAuthSettings(
        client_id=slack_client_id,
        client_secret=slack_client_secret,
        scopes=[
            # --- Sending & managing messages ---
            "chat:write",  # Send messages as the bot
            "chat:write.public",  # Send to public channels without joining
            "reactions:write",  # Add emoji reactions
            "files:write",  # Upload files
            # --- Receiving messages & mentions ---
            "app_mentions:read",  # Receive @mention events
            "channels:history",  # Read messages in public channels the bot is in
            "groups:history",  # Read messages in private channels the bot is in
            "im:history",  # Read messages in DMs
            "im:write",  # Open and send direct messages
            "mpim:history",  # Read messages in group DMs
            # --- Reading channel & user info ---
            "channels:read",  # Get public channel info
            "groups:read",  # Get private channel info
            "users:read",  # Get basic user info
            "users:read.email",  # Get email addresses (optional, useful for mapping users)
            # --- Managing channel/group membership ---
            "groups:write",  # Join/manage private channels
            # --- Join public channels in a workspace ---
            "channels:join",
        ],
        user_scopes=["team.billing:read"],
        state_store=oauth_state_store,
        redirect_uri_path="/slack/oauth_redirect",
        install_path="/slack/install_path",
    )
    oauth_flow = SlackOAuthFlow(client=client, settings=oauth_settings, logger=LOG)
    app = App(
        logger=LOG,
        signing_secret=slack_signing_secret,
        installation_store=installation_store,
        oauth_flow=oauth_flow,
        oauth_settings=oauth_settings,
        client=client,
    )
    return app


slack_app = make_slack_app()


def make_teams_app(cache=None):
    ms_teams_client_id = os.environ.get("MS_TEAMS_CLIENT_ID", "client_id")
    ms_teams_client_secret = os.environ.get("MS_TEAMS_CLIENT_SECRET", "client_secret")
    authority = os.environ.get("MS_TEAMS_AUTHORITY", "https://login.microsoftonline.com/common")

    if not ms_teams_client_secret or ms_teams_client_secret == "client_secret":
        LOG.warning(
            "Microsoft Teams client secrets are not configured. Please set the MS_TEAMS_CLIENT_SECRET environment"
            " variables with a valid secrets."
        )

    return msal.ConfidentialClientApplication(
        ms_teams_client_id, authority=authority, client_credential=ms_teams_client_secret, token_cache=cache
    )


teams_app = make_teams_app()
