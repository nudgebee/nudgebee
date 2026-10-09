"""A brand with no logo shows no image, on every surface that shows the logo.

The branding row for a partner can rename the product without supplying a
logo. The bundled logo is the house mark, so it must not appear beside the
partner's name: the email footer and the Microsoft Teams and Google Chat cards
each leave the image out instead.
"""

import json
from pathlib import Path

import pytest
from jinja2 import ChainableUndefined, Environment, FileSystemLoader

from notifications_server.configs.settings import settings
from notifications_server.emailer.generator import get_templates_path
from notifications_server.emailer.template_params import get_default_template
from notifications_server.services.common import CommonService

LOGO = "https://ops.partner.example.com/branding/acme/logo.png"

# Every template that inherits the shared footer.
TEMPLATES = sorted(p.stem for p in Path(get_templates_path()).glob("*.html") if not p.name.startswith("_"))


@pytest.fixture
def brand(monkeypatch):
    def set_logo(url):
        monkeypatch.setattr(settings.urls, "branding_name", "Contoso Ops")
        monkeypatch.setattr(settings.urls, "branding_logo_url", url)

    return set_logo


def _render(template):
    env = Environment(
        loader=FileSystemLoader(get_templates_path()),
        autoescape=True,
        undefined=ChainableUndefined,
    )
    return env.get_template(f"{template}.html").render(**get_default_template())


@pytest.mark.parametrize("template", TEMPLATES)
def test_email_footer_shows_the_logo_it_is_given(template, brand):
    brand(LOGO)
    assert f'src="{LOGO}"' in _render(template)


@pytest.mark.parametrize("template", TEMPLATES)
def test_email_footer_has_no_image_without_a_logo(template, brand):
    brand("")
    html = _render(template)
    # The footer logo is the image whose alt text is the brand name.
    assert 'alt="Contoso Ops"' not in html
    assert 'src=""' not in html
    assert "Contoso Ops" in html


def _images(card):
    return [el for el in _walk(card) if isinstance(el, dict) and el.get("type") == "Image"]


def _walk(node):
    yield node
    children = node.values() if isinstance(node, dict) else node if isinstance(node, list) else ()
    for child in children:
        yield from _walk(child)


@pytest.mark.parametrize("connected", [True, False])
def test_teams_welcome_card_shows_the_logo_it_is_given(connected, brand):
    brand(LOGO)
    card = CommonService.create_teams_welcome_card(connected)
    assert [image["url"] for image in _images(card)] == [LOGO]


@pytest.mark.parametrize("connected", [True, False])
def test_teams_welcome_card_has_no_image_without_a_logo(connected, brand):
    brand("")
    card = CommonService.create_teams_welcome_card(connected)
    # An Image with an empty url is not a valid Adaptive Card element.
    assert _images(card) == []
    header = card["body"][0]
    assert header["type"] == "ColumnSet" and len(header["columns"]) == 1
    assert "Welcome to Contoso Ops!" in json.dumps(card)


def _gchat_cards():
    return {
        "connect": lambda: CommonService.create_gchat_connect_card("spaces/AAA", "Ops room"),
        "welcome": CommonService.create_gchat_welcome_card,
    }


@pytest.mark.parametrize("name", ["connect", "welcome"])
def test_gchat_card_header_shows_the_logo_it_is_given(name, brand):
    brand(LOGO)
    header = _gchat_cards()[name]()["cardsV2"][0]["card"]["header"]
    assert header["imageUrl"] == LOGO
    assert header["imageType"] == "CIRCLE"


@pytest.mark.parametrize("name", ["connect", "welcome"])
def test_gchat_card_header_has_no_image_without_a_logo(name, brand):
    brand("")
    header = _gchat_cards()[name]()["cardsV2"][0]["card"]["header"]
    assert "imageUrl" not in header and "imageType" not in header
    assert "Contoso Ops" in header["title"]
