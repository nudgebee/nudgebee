"""The Slack workspace URL the delivery history builds message links from: saved when Slack is connected,
filled after the next post for installs made before, and never in a send's way."""

from contextlib import nullcontext
from types import SimpleNamespace

import pytest
from slack_sdk.errors import SlackApiError

from notifications_server.models.models import Integration, MessagingPlatform
from notifications_server.services import messaging_installations as mi
from notifications_server.services.message import SlackSender
from notifications_server.services.slack import slack_installation_store, workspace_url
from notifications_server.services.slack.slack_oauth_flow import SlackOAuthFlow

INTEGRATION = "44444444-4444-4444-4444-444444444444"


def _install(team_url=None, origin=mi.ORIGIN_INTEGRATION):
    integ = Integration(id=INTEGRATION, name="T1", tenant_id="t1")
    cfg = {"bot_token": "xoxb-1"}
    if team_url:
        cfg["team_url"] = team_url
    inst = mi.build_installation("slack", integ, cfg)
    inst._origin = origin
    return inst


@pytest.fixture(autouse=True)
def fresh(monkeypatch):
    monkeypatch.setattr(workspace_url, "_tried", set())
    started = []

    class InlineThread:
        def __init__(self, target, args, daemon):
            self.target, self.args = target, args
            assert daemon, "a send must never wait for the lookup"

        def start(self):
            started.append(self.args)
            self.target(*self.args)

    monkeypatch.setattr(workspace_url.threading, "Thread", InlineThread)
    return started


def test_the_url_comes_with_the_install_and_survives_the_cache():
    inst = _install(team_url="https://acme.slack.com/")

    assert inst.team_url == "https://acme.slack.com/"
    assert MessagingPlatform.from_dict(inst.to_dict()).team_url == "https://acme.slack.com/"


def test_connecting_slack_saves_the_workspace_url(monkeypatch):
    client = SimpleNamespace(
        oauth_v2_access=lambda **_kw: {
            "team": {"id": "T1", "name": "Acme"},
            "authed_user": {"id": "U1"},
            "access_token": "xoxb",
        },
        auth_test=lambda **_kw: {"bot_id": "B1", "url": "https://acme.slack.com/"},
    )
    flow = SimpleNamespace(client=client, settings=SimpleNamespace(client_id="c", client_secret="s", redirect_uri=None))

    installation = SlackOAuthFlow.run_installation(flow, "code")

    saved = {}
    monkeypatch.setattr(slack_installation_store, "Session", lambda _engine: nullcontext())
    monkeypatch.setattr(
        slack_installation_store, "upsert_messaging_integration", lambda _s, config, **_kw: saved.update(config)
    )
    store = slack_installation_store.SlackInstallationStore.__new__(slack_installation_store.SlackInstallationStore)
    store.client_id, store.engine = "c", None
    store.save(installation)

    assert installation.team_url == "https://acme.slack.com/"
    assert saved["team_url"] == "https://acme.slack.com/"


def test_an_older_install_gets_its_url_once_after_a_post(monkeypatch, fresh):
    saved, cleared = [], []
    monkeypatch.setattr(
        workspace_url, "WebClient", lambda token: SimpleNamespace(auth_test=lambda: {"url": "https://acme.slack.com/"})
    )
    monkeypatch.setattr(workspace_url, "Session", lambda _engine: nullcontext(SimpleNamespace(commit=lambda: None)))
    monkeypatch.setattr(
        workspace_url, "persist_team_url", lambda _s, integration_id, url: saved.append((integration_id, url))
    )
    monkeypatch.setattr(workspace_url, "Cache", lambda: SimpleNamespace(delete_cached_installations=cleared.append))

    workspace_url.remember(_install())
    workspace_url.remember(_install())

    assert saved == [(INTEGRATION, "https://acme.slack.com/")]
    assert cleared == ["t1"]
    assert len(fresh) == 1, "looked up once per install per process"


@pytest.mark.parametrize(
    "inst",
    [_install(team_url="https://acme.slack.com/"), _install(origin=mi.ORIGIN_LEGACY)],
    ids=["already saved", "legacy install"],
)
def test_nothing_is_looked_up_when_it_is_not_needed(inst, fresh):
    workspace_url.remember(inst)

    assert fresh == []


def test_a_failed_lookup_is_logged_not_raised(monkeypatch):
    def boom(token):
        raise RuntimeError("invalid_auth")

    monkeypatch.setattr(workspace_url, "WebClient", boom)

    workspace_url.remember(_install())


def test_a_post_that_went_in_stays_sent_when_the_lookup_cannot_start(monkeypatch):
    class NoThreads:
        def __init__(self, **_kw):
            pass

        def start(self):
            raise RuntimeError("can't start new thread")

    monkeypatch.setattr(workspace_url.threading, "Thread", NoThreads)
    ok = SimpleNamespace(client=SimpleNamespace(chat_post=lambda **_kw: {"ok": True, "ts": "1.1"}))
    inst = _install()
    inst.to_channel = {"id": "C1"}

    assert SlackSender(ok, None).post_to_slack("t1", inst, None, text="hi") == {"ok": True, "ts": "1.1"}


def test_an_install_without_a_tenant_is_left_alone(fresh):
    inst = _install()
    inst.tenant_id = None

    workspace_url.remember(inst)

    assert fresh == []


def test_a_post_that_went_in_fills_the_url_and_a_refused_one_does_not(monkeypatch):
    remembered = []
    monkeypatch.setattr(workspace_url, "remember", remembered.append)
    ok = SimpleNamespace(client=SimpleNamespace(chat_post=lambda **_kw: {"ok": True, "ts": "1.1"}))
    inst = SimpleNamespace(to_channel={"id": "C1"}, token="xoxb")

    SlackSender(ok, None).post_to_slack("t1", inst, None, text="hi")

    refusal = SimpleNamespace(status_code=200, headers={}, data={"ok": False, "error": "channel_not_found"})

    def refused(**_kwargs):
        raise SlackApiError("channel_not_found", refusal)

    SlackSender(SimpleNamespace(client=SimpleNamespace(chat_post=refused)), None).post_to_slack(
        "t1", inst, None, text="hi"
    )

    assert remembered == [inst]
