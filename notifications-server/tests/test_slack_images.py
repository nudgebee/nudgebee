import base64
from unittest.mock import MagicMock, patch

import requests

from notifications_server.services.slack_images import (
    collect_slack_images,
    select_image_files,
)


def _img(name, mimetype, size=1000, url="https://files.slack.com/x/a.png"):
    return {
        "name": name,
        "mimetype": mimetype,
        "size": size,
        "url_private_download": url,
    }


class TestSelectImageFiles:
    def test_keeps_png_and_jpeg_in_order(self):
        files = [_img("a.png", "image/png"), _img("b.jpg", "image/jpeg")]
        selected, skipped = select_image_files(files, max_count=4, max_size_mb=1.5)
        assert [f["name"] for f in selected] == ["a.png", "b.jpg"]
        assert skipped == []

    def test_ignores_non_image_files_silently(self):
        files = [_img("log.txt", "text/plain"), _img("a.png", "image/png")]
        selected, skipped = select_image_files(files, max_count=4, max_size_mb=1.5)
        assert [f["name"] for f in selected] == ["a.png"]
        assert skipped == []  # non-images are ignored, not reported as skipped

    def test_reports_unsupported_image_type(self):
        selected, skipped = select_image_files([_img("a.gif", "image/gif")], max_count=4, max_size_mb=1.5)
        assert selected == []
        assert len(skipped) == 1 and "unsupported image type" in skipped[0]

    def test_reports_oversize_by_size_field(self):
        big = _img("big.png", "image/png", size=2 * 1024 * 1024)
        selected, skipped = select_image_files([big], max_count=4, max_size_mb=1.5)
        assert selected == []
        assert "larger than" in skipped[0]

    def test_caps_at_max_count(self):
        files = [_img(f"{i}.png", "image/png") for i in range(5)]
        selected, skipped = select_image_files(files, max_count=2, max_size_mb=1.5)
        assert len(selected) == 2
        assert len(skipped) == 3 and all("over the 2-image limit" in s for s in skipped)

    def test_skips_non_dict_entries(self):
        selected, skipped = select_image_files(["nope", None], max_count=4, max_size_mb=1.5)
        assert selected == [] and skipped == []


def _resp(content):
    resp = MagicMock()
    resp.content = content
    resp.raise_for_status = MagicMock()
    return resp


class TestCollectSlackImages:
    def test_downloads_and_base64_encodes(self):
        files = [_img("a.png", "image/png")]
        with patch("notifications_server.services.slack_images.requests.get") as mock_get:
            mock_get.return_value = _resp(b"PNGDATA")
            images, skipped = collect_slack_images(files, "xoxb-token", max_count=4, max_size_mb=1.5)
        assert images == [{"data": base64.b64encode(b"PNGDATA").decode("ascii"), "mime_type": "image/png"}]
        assert skipped == []
        # the workspace bot token is forwarded as bearer auth
        assert mock_get.call_args.kwargs["headers"]["Authorization"] == "Bearer xoxb-token"

    def test_refuses_non_slack_host(self):
        files = [_img("a.png", "image/png", url="https://evil.example.com/a.png")]
        with patch("notifications_server.services.slack_images.requests.get") as mock_get:
            images, skipped = collect_slack_images(files, "xoxb-token", max_count=4, max_size_mb=1.5)
        mock_get.assert_not_called()
        assert images == []
        assert "could not be downloaded" in skipped[0]

    def test_download_failure_is_skipped(self):
        files = [_img("a.png", "image/png")]
        with patch("notifications_server.services.slack_images.requests.get") as mock_get:
            mock_get.side_effect = requests.ConnectionError()
            images, skipped = collect_slack_images(files, "xoxb-token", max_count=4, max_size_mb=1.5)
        assert images == []
        assert "could not be downloaded" in skipped[0]

    def test_rejects_content_larger_than_limit(self):
        files = [_img("a.png", "image/png", size=100)]  # size field passes the pre-check
        big = b"x" * (2 * 1024 * 1024)
        with patch("notifications_server.services.slack_images.requests.get") as mock_get:
            mock_get.return_value = _resp(big)
            images, skipped = collect_slack_images(files, "xoxb-token", max_count=4, max_size_mb=1.5)
        assert images == []
        assert "larger than" in skipped[0]
