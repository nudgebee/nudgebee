"""Tests for Confluence Cloud vs Data Center client construction and linking.

Data Center differs from Cloud in three ways that all have to line up: personal
access tokens authenticate as bearer tokens, the REST API is not served under
``/wiki``, and page links must be built from the instance's own reported base
URL rather than reconstructed from the configured host.
"""

from types import SimpleNamespace

from rag.core.documents.scraper import (
    build_confluence_client,
    confluence_auth_type,
    confluence_page_url,
    fetch_all_spaces,
)

CLOUD_HOST = "https://acme.atlassian.net"
DC_HOST = "https://wiki.acme.internal/confluence"


def test_absent_auth_type_stays_cloud_api_token():
    # Integrations created before Data Center support carry no auth_type.
    assert confluence_auth_type(None) == "cloud_api_token"
    assert confluence_auth_type("") == "cloud_api_token"
    assert confluence_auth_type("  ") == "cloud_api_token"
    assert confluence_auth_type("carrier-pigeon") == "cloud_api_token"
    assert confluence_auth_type("datacenter_pat") == "datacenter_pat"


def test_data_center_pat_authenticates_as_bearer():
    client = build_confluence_client({"auth_type": "datacenter_pat", "host": DC_HOST, "token": "pat-value"})

    assert client._session.headers.get("Authorization") == "Bearer pat-value"
    assert client._session.auth is None, "a personal access token must not also send basic credentials"
    assert client.url == DC_HOST, "Data Center serves the API beneath its context path, without /wiki"
    assert client.cloud is False


def test_data_center_basic_authenticates_as_basic():
    client = build_confluence_client(
        {"auth_type": "datacenter_password", "host": DC_HOST, "username": "svc_nudgebee", "token": "secret"}
    )

    assert client._session.auth == ("svc_nudgebee", "secret")
    assert client.url == DC_HOST
    assert client.cloud is False


def test_cloud_api_token_targets_the_wiki_path():
    client = build_confluence_client(
        {"auth_type": "cloud_api_token", "host": CLOUD_HOST, "username": "me@acme.com", "token": "tok"}
    )

    assert client._session.auth == ("me@acme.com", "tok")
    assert client.url == f"{CLOUD_HOST}/wiki"
    assert client.cloud is True


def test_cloud_host_already_carrying_wiki_is_not_doubled():
    client = build_confluence_client(
        {"host": f"{CLOUD_HOST}/wiki/", "username": "me@acme.com", "token": "tok"},
    )

    assert client.url == f"{CLOUD_HOST}/wiki"


def test_page_url_prefers_the_instance_reported_base():
    page = {"_links": {"base": "https://wiki.acme.internal/confluence", "webui": "/display/ENG/Runbook"}}
    confluence = SimpleNamespace(url="https://ignored")

    assert confluence_page_url(confluence, page) == "https://wiki.acme.internal/confluence/display/ENG/Runbook"


def test_page_url_falls_back_to_the_client_base():
    page = {"_links": {"webui": "/display/ENG/Runbook"}}
    confluence = SimpleNamespace(url="https://wiki.acme.internal/confluence/")

    assert confluence_page_url(confluence, page) == "https://wiki.acme.internal/confluence/display/ENG/Runbook"


def test_page_url_is_empty_without_a_webui_link():
    assert confluence_page_url(SimpleNamespace(url="https://x"), {"_links": {}}) == ""
    assert confluence_page_url(SimpleNamespace(url="https://x"), {}) == ""


def test_fetch_all_spaces_follows_pagination():
    # An enterprise Data Center instance holds more spaces than one page
    # returns; a truncated list silently indexes a subset.
    pages = {
        0: {"results": [{"key": f"S{i}"} for i in range(100)]},
        100: {"results": [{"key": f"S{i}"} for i in range(100, 150)]},
    }
    calls = []

    def get_all_spaces(start, limit):
        calls.append(start)
        return pages.get(start, {"results": []})

    keys = fetch_all_spaces(SimpleNamespace(get_all_spaces=get_all_spaces))

    assert len(keys) == 150
    assert keys[0] == "S0" and keys[-1] == "S149"
    assert calls == [0, 100]


def test_fetch_all_spaces_stops_on_error():
    def get_all_spaces(start, limit):
        raise RuntimeError("connection reset")

    assert fetch_all_spaces(SimpleNamespace(get_all_spaces=get_all_spaces)) == []
