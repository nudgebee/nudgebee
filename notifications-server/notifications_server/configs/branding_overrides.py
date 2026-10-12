"""Apply the database branding row for this deployment to outbound notifications.

Branding is configured in the `tenant_branding_override` table, one row per
hostname. The web app picks the row for each request's host; this service has
no request host, so it uses the hostname of its own BASE_URL — the same host its
links already point at. One deployment therefore sends one brand.

The row's `title` becomes the product name and `assistantName` the bot's name in
every notification (email, Slack, Teams, Discord, Google Chat); its `email`
block restyles outbound email. All are applied on top of the values loaded from
the bundled theme file at startup, which stay the fallback: no row, a row
without those fields, or a missing BASE_URL all leave the bundled branding in
place.

Failure behaviour mirrors the web app. A failed refresh keeps the last good
values rather than reverting to the bundled brand mid-flight, and each refresh
recomputes from the startup baseline, so removing a field from the row takes it
back out of outbound mail on the next refresh.

Templates read `settings.urls.branding_*` when a message is built, never at
import, so updating those attributes in place is enough for the next message to
pick them up.
"""

import asyncio
import logging
from dataclasses import dataclass, fields
from typing import Any, Awaitable, Callable, Dict, Optional
from urllib.parse import urlparse

import aiohttp

from notifications_server.configs.settings import URLSettings, settings

LOG = logging.getLogger(__name__)

REFRESH_INTERVAL_SECONDS = 60
FETCH_TIMEOUT_SECONDS = 5
BRANDING_OVERRIDES_PATH = "/v1/branding/overrides"

OverrideMap = Dict[str, Dict[str, Any]]


class BrandingEndpointAbsent(Exception):
    """services-server serves no /v1/branding/overrides.

    Branding overrides are EE-only; an OSS services-server does not register the
    route. That is the expected state of an OSS install, not a failure, so it is
    logged once rather than warned about on every refresh.
    """


Fetcher = Callable[[], Awaitable[OverrideMap]]

# Row `email` field -> URLSettings attribute. Values are validated by
# services-server on write (branding.validateEmail).
_EMAIL_STRING_FIELDS = {
    "logoUrl": "branding_logo_url",
    "supportEmail": "branding_support_email",
    "primaryColor": "branding_primary_color",
    "headerBgColor": "branding_header_bg_color",
    "footerBgColor": "branding_footer_bg_color",
    "footerLinkColor": "branding_footer_link_color",
    "address": "branding_address",
}


@dataclass(frozen=True)
class Branding:
    branding_name: str
    assistant_name: str
    branding_logo_url: str
    branding_support_email: str
    branding_primary_color: str
    branding_header_bg_color: str
    branding_footer_bg_color: str
    branding_footer_link_color: str
    branding_address: str
    branding_copyright_start_year: int

    @classmethod
    def from_urls(cls, urls: URLSettings) -> "Branding":
        return cls(**{f.name: getattr(urls, f.name) for f in fields(cls)})

    def apply_to(self, urls: URLSettings) -> None:
        for f in fields(self):
            setattr(urls, f.name, getattr(self, f.name))


def normalize_host(host: Optional[str]) -> str:
    """Mirror branding.NormalizeHost / normalizeBrandHost(): lower-cased, port and
    trailing dot stripped, IPv6 brackets kept. Stored keys and this lookup must
    normalize identically or the row silently never matches."""
    first = (host or "").split(",")[0].strip().lower()
    if not first:
        return ""
    if first.startswith("["):
        end = first.find("]")
        if end < 0:
            return ""
        hostname = first[: end + 1]
    else:
        hostname = first.split(":")[0]
    # One trailing dot only, like strings.TrimSuffix on the Go side.
    return hostname[:-1] if hostname.endswith(".") else hostname


def deployment_host(base_url: str) -> str:
    """The branding key for this deployment: BASE_URL's hostname, normalized."""
    if not base_url:
        return ""
    return normalize_host(urlparse(base_url).netloc)


def resolve_branding(baseline: Branding, override: Optional[Dict[str, Any]], base_url: str) -> Branding:
    """Merge a row over the startup baseline. Anything absent or malformed in the
    row keeps the baseline value, so a bad field can never blank the brand."""
    # A hand-edited row can hold any JSON, not just an object.
    if not isinstance(override, dict) or not override:
        return baseline
    values = {f.name: getattr(baseline, f.name) for f in fields(baseline)}

    title = override.get("title")
    if isinstance(title, str) and title.strip():
        values["branding_name"] = title

    assistant_name = override.get("assistantName")
    if isinstance(assistant_name, str) and assistant_name.strip():
        values["assistant_name"] = assistant_name

    email = override.get("email")
    if isinstance(email, dict):
        for key, attr in _EMAIL_STRING_FIELDS.items():
            value = email.get(key)
            if not isinstance(value, str):
                continue
            if key == "logoUrl":
                if not value:
                    continue
                # Email clients need an absolute URL; the row stores a root-relative
                # path, same as every other asset.
                value = f"{base_url.rstrip('/')}{value}" if value.startswith("/") else value
            values[attr] = value
        year = email.get("copyrightStartYear")
        if isinstance(year, (int, float)) and not isinstance(year, bool) and int(year) == year:
            values["branding_copyright_start_year"] = int(year)

    return Branding(**values)


class BrandingOverrideRefresher:
    def __init__(self, urls: URLSettings, fetch: Fetcher):
        self._urls = urls
        self._fetch = fetch
        self._baseline = Branding.from_urls(urls)
        self._host = deployment_host(urls.base_url)
        self._absent_logged = False

    @property
    def host(self) -> str:
        return self._host

    async def refresh_once(self) -> bool:
        """Fetch the rows and apply this deployment's. Returns whether it applied.

        Every step is inside the guard, not just the fetch: an exception while
        resolving or applying a malformed row would otherwise escape run() and end
        the background task, and branding would stop refreshing for the life of
        the process with nothing but one traceback to show for it.
        """
        try:
            overrides = await self._fetch()
            row = overrides.get(self._host) if isinstance(overrides, dict) else None
            branding = resolve_branding(self._baseline, row, self._urls.base_url)
        except BrandingEndpointAbsent:
            if not self._absent_logged:
                LOG.info(
                    "%s returned 404, so this deployment serves no branding overrides (expected on OSS); "
                    "keeping the bundled branding",
                    BRANDING_OVERRIDES_PATH,
                )
                self._absent_logged = True
            return False
        except Exception as exc:  # noqa: BLE001 — any failure keeps the last good values
            LOG.warning("Branding override refresh failed; keeping current branding: %s", exc)
            return False
        branding.apply_to(self._urls)
        self._absent_logged = False
        return True

    async def run(self, interval: float = REFRESH_INTERVAL_SECONDS) -> None:
        if not self._host:
            LOG.info("BASE_URL is not set; database branding overrides are disabled for notifications")
            return
        LOG.info("Applying database branding overrides for host %s every %ss", self._host, interval)
        while True:
            await self.refresh_once()
            await asyncio.sleep(interval)


async def fetch_overrides_from_services_server() -> OverrideMap:
    url = settings.services.api_server.rstrip("/") + BRANDING_OVERRIDES_PATH
    headers = {"Accept": "application/json"}
    if settings.action_api_server_token:
        headers["X-ACTION-TOKEN"] = settings.action_api_server_token
    timeout = aiohttp.ClientTimeout(total=FETCH_TIMEOUT_SECONDS)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        async with session.get(url, headers=headers) as resp:
            if resp.status == 404:
                raise BrandingEndpointAbsent()
            if resp.status != 200:
                raise RuntimeError(f"{BRANDING_OVERRIDES_PATH} returned {resp.status}")
            body = await resp.json()
    overrides = body.get("overrides") if isinstance(body, dict) else None
    if not isinstance(overrides, dict):
        raise RuntimeError("/v1/branding/overrides returned an unexpected shape")
    return overrides


def build_refresher() -> BrandingOverrideRefresher:
    return BrandingOverrideRefresher(settings.urls, fetch_overrides_from_services_server)
