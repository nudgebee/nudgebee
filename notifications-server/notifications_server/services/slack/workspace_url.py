"""A Slack workspace's URL, kept with its integration so the delivery history can link to messages. A new
install saves it from auth.test; an install made before that gets it after its next post."""

import logging
import threading

from slack_sdk.web.client import WebClient
from sqlalchemy.orm import Session

from notifications_server.services.cache import Cache
from notifications_server.services.messaging_installations import ORIGIN_INTEGRATION, persist_team_url

LOG = logging.getLogger(__name__)

_tried = set()
_tried_lock = threading.Lock()


def remember(installation) -> None:
    """Look up and save the workspace URL of an install that has none: once per install per process, on a
    thread of its own, so a send never waits for it or fails because of it. Installs still in the legacy
    messaging_platforms table are left out."""
    integration_id = getattr(installation, "_integration_id", None)
    token = getattr(installation, "token", None)
    tenant_id = getattr(installation, "tenant_id", None)
    if (
        getattr(installation, "team_url", None)
        or getattr(installation, "_origin", None) != ORIGIN_INTEGRATION
        or not integration_id
        or not token
        or not tenant_id
    ):
        return
    with _tried_lock:
        if integration_id in _tried:
            return
        _tried.add(integration_id)
    # It runs after a post went in: raising here would report that post as failed, and a retry would repeat it.
    try:
        threading.Thread(target=_save, args=(token, integration_id, tenant_id), daemon=True).start()
    except Exception:
        LOG.warning("Could not start the Slack workspace URL lookup for integration %s", integration_id, exc_info=True)


def _save(token, integration_id, tenant_id) -> None:
    try:
        team_url = WebClient(token=token).auth_test().get("url")
        if not team_url:
            return
        from notifications_server import sync_engine

        with Session(sync_engine) as session:
            persist_team_url(session, integration_id, team_url)
            session.commit()
        Cache().delete_cached_installations(tenant_id)
    except Exception:
        LOG.warning("Saving the Slack workspace URL of integration %s failed", integration_id, exc_info=True)
