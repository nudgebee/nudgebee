"""Ingestion bodies that arrive empty, cut off or garbled answer 400, not 500.

An agent that gives up mid-upload (its HTTP client times out at 60s) leaves
the server holding an empty body. gzip.decompress(b"") is b"", json.loads of
that raised JSONDecodeError, and ErrorCatcher turned it into a 500 with an
ERROR traceback: the server was blamed for the client's abandoned upload.
"""

import gzip
import json
import os
import sys

import pytest

_ENV_DEFAULTS = {
    "ENV": "DEV",
    "COLLECTOR_MODE": "server",
    "COLLECTOR_DB_URL": "postgresql://u:p@localhost:5432/db?sslmode=disable",
    "NUDGEBEE_ENCRYPTION_KEY": "test-key",
    "ACTION_API_SERVER_TOKEN": "",
    "RABBIT_MQ_HOST": "localhost",
    "RABBIT_MQ_PORT": "5672",
    "RABBIT_MQ_USERNAME": "guest",
    "RABBIT_MQ_PASSWORD": "guest",
    "REDIS_SERVER_HOST": "localhost",
    "REDIS_SERVER_PORT": "6379",
    "REDIS_USER_NAME": "",
    "REDIS_USER_PASSWORD": "",
    "CLICKHOUSE_ENABLED": "false",
    "CLICKHOUSE_HOST": "",
    "CLICKHOUSE_USER": "",
    "CLICKHOUSE_PASSWORD": "",
}
for _k, _v in _ENV_DEFAULTS.items():
    os.environ.setdefault(_k, _v)

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

from flask import Flask  # noqa: E402

from apis.base.api import read_json_body  # noqa: E402
from exception.collector_exceptions import BadRequestError  # noqa: E402

_app = Flask(__name__)
_PAYLOAD = {"type": "Pod", "items": [{"name": "a"}]}
_GZIP = {"Content-Encoding": "gzip", "Content-Type": "application/json"}


def _read(data: bytes, headers: dict):
    with _app.test_request_context("/", method="POST", data=data, headers=headers):
        return read_json_body()


def test_gzip_json_is_parsed():
    assert _read(gzip.compress(json.dumps(_PAYLOAD).encode()), _GZIP) == _PAYLOAD


def test_plain_json_is_parsed():
    assert _read(json.dumps(_PAYLOAD).encode(), {"Content-Type": "application/json"}) == _PAYLOAD


@pytest.mark.parametrize(
    "body",
    [
        pytest.param(b"", id="empty-abandoned-upload"),
        pytest.param(gzip.compress(json.dumps(_PAYLOAD).encode())[:20], id="truncated-gzip"),
        pytest.param(b"not gzip at all", id="not-gzip"),
        pytest.param(gzip.compress(b"{not json"), id="gzip-of-non-json"),
        pytest.param(gzip.compress(b"\xff\xfe\xfa"), id="gzip-of-non-utf8"),
    ],
)
def test_unreadable_gzip_body_is_a_400(body):
    with pytest.raises(BadRequestError) as exc:
        _read(body, _GZIP)
    assert exc.value.code == 400
