"""The footer every email shares states the copyright once.

The text handed to the template already reads "Copyright © <years>", so a
symbol in the template as well renders "© Copyright © <years> <brand>".
"""

from pathlib import Path

import pytest
from jinja2 import ChainableUndefined, Environment, FileSystemLoader

from notifications_server.configs.settings import settings
from notifications_server.emailer.generator import get_templates_path
from notifications_server.emailer.template_params import get_default_template
from notifications_server.utils.datetime_utils import utc_now

# Every template that inherits the shared footer.
TEMPLATES = sorted(p.stem for p in Path(get_templates_path()).glob("*.html") if not p.name.startswith("_"))


def _render(template):
    # Only the shared footer matters here, so each template's own parameters
    # are allowed to stay empty rather than be mocked field by field.
    env = Environment(
        loader=FileSystemLoader(get_templates_path()),
        autoescape=True,
        undefined=ChainableUndefined,
    )
    return env.get_template(f"{template}.html").render(**get_default_template())


def _copyright_symbols(html):
    return html.count("©") + html.count("&copy;")


@pytest.mark.parametrize("template", TEMPLATES)
def test_default_footer_has_one_copyright_symbol(template):
    assert _copyright_symbols(_render(template)) == 1


@pytest.mark.parametrize("template", TEMPLATES)
def test_branded_footer_has_one_copyright_symbol(template, monkeypatch):
    # What the database branding row sets: `title` and `email.copyrightStartYear`.
    monkeypatch.setattr(settings.urls, "branding_name", "Contoso Ops")
    monkeypatch.setattr(settings.urls, "branding_copyright_start_year", 2024)

    html = _render(template)

    assert _copyright_symbols(html) == 1
    assert f"Copyright © 2024-{utc_now().year} Contoso Ops" in html
