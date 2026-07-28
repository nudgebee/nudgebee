"""Collect user-attached images from Slack message events for the Nubi LLM.

Slack delivers uploaded files on the message/app_mention event as a ``files``
array; each private file must be downloaded with the workspace bot token
(``Authorization: Bearer``) before it can be forwarded. The llm-server chat
endpoint accepts images as ``[{"data": <base64>, "mime_type": ...}]`` and
re-validates count/size/mime server-side, so the filtering here is a
best-effort guard to avoid pointless downloads and to surface skipped files.
"""

import base64
import logging
from urllib.parse import urlparse

import requests

LOG = logging.getLogger(__name__)

# Mirrors the llm-server allowlist. The llm-server is the source of truth and
# rejects anything else, so keep these in sync if its allowlist changes.
ALLOWED_IMAGE_MIME_TYPES = ("image/png", "image/jpeg")

_DOWNLOAD_TIMEOUT_SECONDS = 10
_ALLOWED_DOWNLOAD_HOST_SUFFIX = ".slack.com"


def select_image_files(files, max_count, max_size_mb):
    """Pick the image files worth downloading, in event order.

    Returns ``(selected, skipped)`` where ``selected`` is a list of Slack file
    dicts and ``skipped`` is a list of human-readable reasons for dropped
    *image* files. Non-image attachments (logs, PDFs, ...) are ignored silently.
    Pure — performs no I/O.
    """
    selected = []
    skipped = []
    max_size_bytes = int(max_size_mb * 1024 * 1024)

    for f in files:
        if not isinstance(f, dict):
            continue
        mimetype = (f.get("mimetype") or "").lower()
        if not mimetype.startswith("image/"):
            continue  # not an image — ignore silently
        name = f.get("name") or "image"
        if mimetype not in ALLOWED_IMAGE_MIME_TYPES:
            skipped.append(f"{name}: unsupported image type ({mimetype})")
            continue
        size = f.get("size")
        if isinstance(size, int) and size > max_size_bytes:
            skipped.append(f"{name}: larger than {max_size_mb} MB")
            continue
        if len(selected) >= max_count:
            skipped.append(f"{name}: over the {max_count}-image limit")
            continue
        selected.append(f)

    return selected, skipped


def _download_image(url, token):
    """GET a Slack private file URL with the bot token; return raw bytes or None."""
    if not url:
        return None
    host = (urlparse(url).hostname or "").lower()
    if host != "slack.com" and not host.endswith(_ALLOWED_DOWNLOAD_HOST_SUFFIX):
        LOG.warning("Refusing to download image from non-Slack host: %s", host)
        return None
    try:
        resp = requests.get(
            url,
            headers={"Authorization": f"Bearer {token}"},
            timeout=_DOWNLOAD_TIMEOUT_SECONDS,
        )
        resp.raise_for_status()
    except requests.RequestException as e:
        LOG.warning("Failed to download Slack image %s: %s", url, e)
        return None
    return resp.content


def collect_slack_images(files, token, *, max_count, max_size_mb):
    """Download and base64-encode user-attached Slack images for the LLM payload.

    Returns ``(images, skipped)`` where ``images`` is
    ``[{"data": <base64>, "mime_type": ...}]`` ready for the chat endpoint and
    ``skipped`` lists human-readable reasons for any dropped image files.
    """
    selected, skipped = select_image_files(files, max_count, max_size_mb)
    max_size_bytes = int(max_size_mb * 1024 * 1024)
    images = []

    for f in selected:
        name = f.get("name") or "image"
        # url_private_download is the direct-download variant; fall back to url_private.
        url = f.get("url_private_download") or f.get("url_private")
        content = _download_image(url, token)
        if content is None:
            skipped.append(f"{name}: could not be downloaded")
            continue
        if len(content) > max_size_bytes:
            skipped.append(f"{name}: larger than {max_size_mb} MB")
            continue
        images.append(
            {
                "data": base64.b64encode(content).decode("ascii"),
                "mime_type": (f.get("mimetype") or "").lower(),
            }
        )

    return images, skipped
