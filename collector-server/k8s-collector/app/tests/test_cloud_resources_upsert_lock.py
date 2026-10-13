"""The cloud_resourses upsert must not SET a unique-index column.

ON CONFLICT DO UPDATE locks the existing row FOR UPDATE whenever the SET list
names a column of any unique index, even if the new value equals the old one.
FOR UPDATE conflicts with the FOR KEY SHARE that each foreign-key insert into
cloud_resource_metrics / spends takes on the parent row. With `resourse_id` in
the SET, the OpenCost POST and a discovery batch for the same account deadlocked
roughly every six hours, failing the cost-metric insert.

Runs offline: heavy deps are stubbed before importing the handler, same as
test_job_last_seen.py.
"""

import os
import re
import sys
import unittest
from unittest import mock

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

for _mod in (
    "redis",
    "psycopg2",
    "psycopg2.extras",
    "psycopg2.pool",
    "clickhouse_driver",
    "clickhouse_connect",
    "clickhouse_connect.driver",
    "clickhouse_connect.driver.client",
    "clickhouse_connect.driver.query",
    "clickhouse_connect.driver.summary",
):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

import handlers.discovery_handler as dh  # noqa: E402

# Columns of the three unique indexes on cloud_resourses: the primary key (id),
# (account, external_resource_id) and
# cloud_resourses_account_resourse_service_type_region_key.
UNIQUE_INDEX_COLUMNS = {"id", "account", "external_resource_id", "resourse_id", "type", "region", "service_name"}


def _set_columns(on_conflict: str) -> set:
    set_clause = re.search(r"DO UPDATE SET (.*?) WHERE ", on_conflict, re.DOTALL).group(1)
    return {assignment.split("=")[0].strip() for assignment in set_clause.split(",")}


class CloudResourcesUpsertLockTest(unittest.TestCase):
    def test_set_list_avoids_unique_index_columns(self):
        columns = _set_columns(dh.resources_on_conflict)
        self.assertIn("last_seen", columns)
        self.assertEqual(columns & UNIQUE_INDEX_COLUMNS, set())


if __name__ == "__main__":
    unittest.main()
