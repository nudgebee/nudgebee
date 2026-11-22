"""The agent audit log must not store credential headers. It used a denylist of
four names, which missed X-ACTION-TOKEN: api-server sends the shared internal
token in it on every OpenCost sync, so each sync wrote the token into
agent_audit_log."""

import json
import os
import sys
from unittest import mock

# Environment + heavy-dependency stubs — MUST run before importing app code
# (same pattern as test_discovery_shape.py).
_ENV_DEFAULTS = {
    "ENV": "DEV",
    "COLLECTOR_MODE": "worker",
    "COLLECTOR_DB_URL": "postgresql://u:p@localhost:5432/db?sslmode=disable",
    "NUDGEBEE_ENCRYPTION_KEY": "test-key",
    "ACTION_API_SERVER_TOKEN": "",
    "SERVICE_API_SERVER_URL": "http://localhost:8000",
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

for _mod in ("redis", "psycopg2", "psycopg2.extras", "psycopg2.pool", "clickhouse_driver"):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

from flask import Flask  # noqa: E402

from middleware import auth_token_middleware as mw  # noqa: E402

_CREDENTIAL_HEADERS = {
    "X-ACTION-TOKEN": "internal-shared-token",
    "Authorization": "Basic YWdlbnQ6c2VjcmV0",
    "Cookie": "session=abc",
    "X-Api-Key": "key-123",
    "X-Custom-Secret": "custom-secret-value",
}
_KEPT_HEADERS = {
    "User-Agent": "nudgebee-agent/1.2.3",
    "Content-Type": "application/json",
    "X-NB-Account-Id": "account-1",
}


def test_audit_headers_keeps_only_allowlisted_values():
    stored = mw.audit_headers({**_CREDENTIAL_HEADERS, **_KEPT_HEADERS})

    for name in _CREDENTIAL_HEADERS:
        assert stored[name] == mw.REDACTED_HEADER_VALUE, name
    for name, value in _KEPT_HEADERS.items():
        assert stored[name] == value, name


def test_audit_logger_never_writes_a_credential_header():
    app = Flask(__name__)
    with mock.patch.object(mw.BaseController, "__init__", lambda self: None):
        logger = mw.AuditLogger(lambda: ("ok", 200))
    with app.test_request_context(
        "/v1/opencost/data", method="POST", headers={**_CREDENTIAL_HEADERS, **_KEPT_HEADERS}
    ), mock.patch.object(mw.clickhouse, "insert_data") as insert:
        logger()

    insert.assert_called_once()
    table, rows = insert.call_args.args
    assert table == "agent_audit_log"
    stored = rows[0]["headers"]
    for value in _CREDENTIAL_HEADERS.values():
        assert value not in stored, value
    assert json.loads(stored)["User-Agent"] == "nudgebee-agent/1.2.3"
