import asyncio

import pytest

from notifications_server.configs.branding_overrides import (
    BrandingEndpointAbsent,
    BrandingOverrideRefresher,
    deployment_host,
    normalize_host,
)
from notifications_server.configs.settings import URLSettings

BASE_URL = "https://calsoft.example.com"


def make_urls(base_url: str = BASE_URL) -> URLSettings:
    # Bundled branding, as loaded from the theme file at startup. Assigned after
    # construction because URLSettings' validators reload the theme file on init.
    urls = URLSettings(base_url=base_url)
    urls.branding_name = "Nudgebee"
    urls.assistant_name = "Nubi"
    urls.branding_logo_url = f"{base_url}/branding/default/logo.png"
    urls.branding_support_email = ""
    urls.branding_primary_color = "#3470e9"
    urls.branding_header_bg_color = "#004C74"
    urls.branding_footer_bg_color = "#10264c"
    urls.branding_footer_link_color = "#ffcc00"
    urls.branding_address = "Pune"
    urls.branding_copyright_start_year = 2022
    return urls


def refresher_with(urls: URLSettings, responses):
    """A refresher whose fetch returns (or raises) each response in turn."""
    queue = list(responses)

    async def fetch():
        item = queue.pop(0)
        if isinstance(item, Exception):
            raise item
        return item

    return BrandingOverrideRefresher(urls, fetch)


def refresh(refresher):
    return asyncio.run(refresher.refresh_once())


@pytest.mark.parametrize(
    "raw,expected",
    [
        ("Calsoft.Example.com:8443", "calsoft.example.com"),
        ("calsoft.example.com.", "calsoft.example.com"),
        ("a.example.com, b.example.com", "a.example.com"),
        ("[::1]:3000", "[::1]"),
        ("[::1", ""),
        ("", ""),
        (None, ""),
    ],
)
def test_normalize_host_matches_services_server(raw, expected):
    assert normalize_host(raw) == expected


def test_deployment_host_is_base_url_hostname():
    assert deployment_host("https://Calsoft.Example.com:443/") == "calsoft.example.com"
    assert deployment_host("") == ""


def test_applies_title_and_email_block_for_base_url_host():
    urls = make_urls()
    row = {
        "title": "Calsoft",
        "assistantName": "Cal",
        "colorTokens": {"--nb-color-primary": "#0047AB"},  # web-only; ignored here
        "email": {
            "logoUrl": "/branding/calsoft/logo.png",
            "supportEmail": "support@calsoft.example.com",
            "primaryColor": "#0047AB",
            "headerBgColor": "#001f4d",
            "footerBgColor": "navy",
            "footerLinkColor": "#fc0",
            "address": "1 Calsoft Way",
            "copyrightStartYear": 2024,
        },
    }
    assert refresh(refresher_with(urls, [{"calsoft.example.com": row}])) is True

    assert urls.branding_name == "Calsoft"
    assert urls.assistant_name == "Cal"
    # Root-relative in the row, absolute in the email.
    assert urls.branding_logo_url == "https://calsoft.example.com/branding/calsoft/logo.png"
    assert urls.branding_support_email == "support@calsoft.example.com"
    assert urls.branding_primary_color == "#0047AB"
    assert urls.branding_header_bg_color == "#001f4d"
    assert urls.branding_footer_bg_color == "navy"
    assert urls.branding_footer_link_color == "#fc0"
    assert urls.branding_address == "1 Calsoft Way"
    assert urls.branding_copyright_start_year == 2024


def test_ignores_rows_for_other_hosts():
    urls = make_urls()
    refresh(refresher_with(urls, [{"other.example.com": {"title": "Other"}}]))
    assert urls.branding_name == "Nudgebee"


def test_partial_row_keeps_bundled_values_for_the_rest():
    urls = make_urls()
    refresh(refresher_with(urls, [{"calsoft.example.com": {"email": {"primaryColor": "#0047AB"}}}]))
    assert urls.branding_primary_color == "#0047AB"
    assert urls.branding_name == "Nudgebee"
    assert urls.branding_logo_url == "https://calsoft.example.com/branding/default/logo.png"
    assert urls.branding_address == "Pune"


def test_malformed_fields_never_blank_the_brand():
    urls = make_urls()
    row = {
        "title": "   ",
        "assistantName": "   ",
        "email": {"logoUrl": "", "primaryColor": 7, "copyrightStartYear": 2024.5, "address": None},
    }
    refresh(refresher_with(urls, [{"calsoft.example.com": row}]))
    assert urls.branding_name == "Nudgebee"
    assert urls.assistant_name == "Nubi"
    assert urls.branding_logo_url == "https://calsoft.example.com/branding/default/logo.png"
    assert urls.branding_primary_color == "#3470e9"
    assert urls.branding_copyright_start_year == 2022
    assert urls.branding_address == "Pune"


def test_failed_refresh_keeps_last_good_branding():
    urls = make_urls()
    refresher = refresher_with(urls, [{"calsoft.example.com": {"title": "Calsoft"}}, RuntimeError("down")])
    refresh(refresher)
    assert refresh(refresher) is False
    assert urls.branding_name == "Calsoft"


def test_oss_services_server_without_the_endpoint_keeps_bundled_branding_and_logs_once(caplog):
    urls = make_urls()
    refresher = refresher_with(urls, [BrandingEndpointAbsent(), BrandingEndpointAbsent()])
    with caplog.at_level("INFO", logger="notifications_server.configs.branding_overrides"):
        assert refresh(refresher) is False
        assert refresh(refresher) is False
    assert urls.branding_name == "Nudgebee"
    assert [r.levelname for r in caplog.records] == ["INFO"]


def test_removing_the_row_restores_bundled_branding():
    urls = make_urls()
    refresher = refresher_with(
        urls,
        [
            {"calsoft.example.com": {"title": "Calsoft", "email": {"primaryColor": "#0047AB"}}},
            {},
        ],
    )
    refresh(refresher)
    refresh(refresher)
    assert urls.branding_name == "Nudgebee"
    assert urls.branding_primary_color == "#3470e9"


def test_removing_one_field_reverts_only_that_field():
    urls = make_urls()
    refresher = refresher_with(
        urls,
        [
            {"calsoft.example.com": {"title": "Calsoft", "email": {"address": "1 Calsoft Way"}}},
            {"calsoft.example.com": {"title": "Calsoft"}},
        ],
    )
    refresh(refresher)
    refresh(refresher)
    assert urls.branding_name == "Calsoft"
    assert urls.branding_address == "Pune"


def test_run_is_inert_without_base_url():
    urls = make_urls(base_url="")
    calls = []

    async def fetch():
        calls.append(1)
        return {}

    asyncio.run(BrandingOverrideRefresher(urls, fetch).run(interval=0))
    assert calls == []


def test_non_object_row_keeps_bundled_branding():
    urls = make_urls()
    assert refresh(refresher_with(urls, [{"calsoft.example.com": "Calsoft"}])) is True
    assert urls.branding_name == "Nudgebee"


def test_trailing_slash_base_url_does_not_double_the_logo_slash():
    urls = make_urls(base_url="https://calsoft.example.com/")
    refresh(refresher_with(urls, [{"calsoft.example.com": {"email": {"logoUrl": "/branding/calsoft/logo.png"}}}]))
    assert urls.branding_logo_url == "https://calsoft.example.com/branding/calsoft/logo.png"


def test_a_row_that_cannot_be_applied_does_not_stop_the_refresher():
    urls = make_urls()
    # float("inf") survives the type checks and then fails int(); the refresher must
    # log it, keep the last good values, and keep refreshing on later passes.
    refresher = refresher_with(
        urls,
        [
            {"calsoft.example.com": {"title": "Calsoft"}},
            {"calsoft.example.com": {"title": "Broken", "email": {"copyrightStartYear": float("inf")}}},
            {"calsoft.example.com": {"title": "Calsoft Cloud"}},
        ],
    )
    refresh(refresher)
    assert refresh(refresher) is False
    assert urls.branding_name == "Calsoft"
    assert refresh(refresher) is True
    assert urls.branding_name == "Calsoft Cloud"
