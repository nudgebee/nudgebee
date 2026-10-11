import json

from googleapiclient.errors import HttpError
from httplib2 import Response

from notifications_server.clients.google_chat_client import parse_http_error

URI = "https://chat.googleapis.com/v1/spaces/SECRET-SPACE/messages?alt=json"


def _error(status, content):
    return HttpError(Response({"status": status}), content, uri=URI)


def test_api_message_is_used_when_present():
    body = json.dumps({"error": {"status": "NOT_FOUND", "message": "Space not found."}}).encode()
    assert parse_http_error(_error(404, body)) == (404, "NOT_FOUND", "Space not found.")


def test_fallback_does_not_leak_exception_text():
    status, error_status, message = parse_http_error(_error(502, b"<html>bad gateway</html>"))
    assert (status, error_status) == (502, None)
    assert message == "Google Chat API error (HTTP 502)"
    assert "SECRET-SPACE" not in message
