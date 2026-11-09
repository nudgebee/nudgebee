"""The Privacy and Terms links in the email footer belong to the brand named beside them.

There are none by default: the app's bundled pages name one company's legal
entity, which is not who runs a self-hosted install. A host's branding row
supplies them (`privacyUrl`, `termsUrl`) — the house brand's own hosts the same
way as a partner's. A row that renames the product without supplying one gets no
link, rather than the house page under the partner's name.
"""

import asyncio
import re
from pathlib import Path

import pytest
from jinja2 import ChainableUndefined, Environment, FileSystemLoader

from notifications_server.configs.branding_overrides import BrandingOverrideRefresher
from notifications_server.configs.settings import URLSettings, settings
from notifications_server.emailer.generator import get_templates_path
from notifications_server.emailer.template_params import get_default_template

BASE_URL = "https://ops.example.com"
HOUSE_PRIVACY = "https://www.example.com/privacy-policy"
HOUSE_TERMS = "https://www.example.com/terms"
PARTNER_PRIVACY = "https://www.contoso.example.com/legal/privacy"
PARTNER_TERMS = "https://www.contoso.example.com/legal/terms"

# Every template that inherits the shared footer.
TEMPLATES = sorted(p.stem for p in Path(get_templates_path()).glob("*.html") if not p.name.startswith("_"))


def apply(row):
    """The settings after the refresher applies `row` for this deployment's host."""
    urls = URLSettings(base_url=BASE_URL)
    urls.branding_name = "Nudgebee"

    async def fetch():
        return {"ops.example.com": row} if row else {}

    asyncio.run(BrandingOverrideRefresher(urls, fetch).refresh_once())
    return urls


def test_no_links_without_a_row():
    # BASE_URL alone must not link the bundled pages: on a self-hosted install
    # they would name a company that is not the one running it.
    assert (URLSettings(base_url=BASE_URL).branding_privacy_url, URLSettings(base_url=BASE_URL).branding_terms_url) == (
        "",
        "",
    )
    urls = apply(None)
    assert (urls.branding_privacy_url, urls.branding_terms_url) == ("", "")


def test_house_row_supplies_the_house_links():
    # Our own hosts keep the house name and set their links on the row.
    urls = apply({"privacyUrl": HOUSE_PRIVACY, "termsUrl": HOUSE_TERMS})
    assert (urls.branding_privacy_url, urls.branding_terms_url) == (HOUSE_PRIVACY, HOUSE_TERMS)


def test_row_supplies_the_partners_own_links():
    urls = apply({"title": "Contoso Ops", "privacyUrl": PARTNER_PRIVACY, "termsUrl": PARTNER_TERMS})
    assert (urls.branding_privacy_url, urls.branding_terms_url) == (PARTNER_PRIVACY, PARTNER_TERMS)


def test_renaming_row_without_links_gets_none():
    urls = apply({"title": "Contoso Ops"})
    assert (urls.branding_privacy_url, urls.branding_terms_url) == ("", "")


def test_each_link_is_decided_on_its_own():
    urls = apply({"title": "Contoso Ops", "privacyUrl": PARTNER_PRIVACY})
    assert (urls.branding_privacy_url, urls.branding_terms_url) == (PARTNER_PRIVACY, "")


def test_row_without_links_gets_none():
    urls = apply({"email": {"primaryColor": "#0047AB"}})
    assert (urls.branding_privacy_url, urls.branding_terms_url) == ("", "")


@pytest.mark.parametrize(
    "link",
    [
        "http://www.contoso.example.com/privacy",
        "javascript:alert(1)",
        "/privacy.html",
        'https://www.contoso.example.com/" onclick="x',
        "https://www.contoso.example.com/our privacy",
        "https://www.contoso.example.com/\r\nx",
        "https://",
        "https://例え.jp/privacy",
        "https://www.contoso.example.com/\u2028x",
        "https://www.contoso.example.com/a\\b",
        42,
    ],
)
def test_a_link_that_is_not_a_plain_https_url_is_not_used(link):
    # It is written into an href; the write path refuses these, a hand-edited row might not.
    assert apply({"title": "Contoso Ops", "privacyUrl": link}).branding_privacy_url == ""
    assert apply({"privacyUrl": link}).branding_privacy_url == ""


def test_links_go_when_the_row_is_removed():
    urls = URLSettings(base_url=BASE_URL)
    urls.branding_name = "Nudgebee"
    responses = [{"ops.example.com": {"privacyUrl": HOUSE_PRIVACY, "termsUrl": HOUSE_TERMS}}, {}]

    async def fetch():
        return responses.pop(0)

    refresher = BrandingOverrideRefresher(urls, fetch)
    asyncio.run(refresher.refresh_once())
    assert (urls.branding_privacy_url, urls.branding_terms_url) == (HOUSE_PRIVACY, HOUSE_TERMS)
    asyncio.run(refresher.refresh_once())
    assert (urls.branding_privacy_url, urls.branding_terms_url) == ("", "")


def _footer_links(template, monkeypatch, privacy, terms):
    monkeypatch.setattr(settings.urls, "branding_privacy_url", privacy)
    monkeypatch.setattr(settings.urls, "branding_terms_url", terms)
    env = Environment(loader=FileSystemLoader(get_templates_path()), autoescape=True, undefined=ChainableUndefined)
    html = env.get_template(f"{template}.html").render(**get_default_template())
    return dict((label, href) for href, label in re.findall(r'<a href="([^"]*)"[^>]*>(Privacy|Terms)</a>', html))


@pytest.mark.parametrize("template", TEMPLATES)
def test_footer_links_to_the_urls_it_is_given(template, monkeypatch):
    links = _footer_links(template, monkeypatch, PARTNER_PRIVACY, PARTNER_TERMS)
    assert links == {"Privacy": PARTNER_PRIVACY, "Terms": PARTNER_TERMS}


@pytest.mark.parametrize("template", TEMPLATES)
def test_footer_shows_only_the_link_that_is_set(template, monkeypatch):
    assert _footer_links(template, monkeypatch, PARTNER_PRIVACY, "") == {"Privacy": PARTNER_PRIVACY}
    assert _footer_links(template, monkeypatch, "", "") == {}
