import os
import subprocess
import sys
from pathlib import Path

APP_DIR = Path(__file__).parents[1]


def test_health_returns_ok():
    env = os.environ.copy()
    env.update(
        {
            "ACTION_API_SERVER_TOKEN": "",
            "CLICKHOUSE_HOST": "",
            "CLICKHOUSE_PASSWORD": "",
            "CLICKHOUSE_USER": "",
            "COLLECTOR_DB_URL": "postgresql://test:test@localhost/test",
            "COLLECTOR_MODE": "server",
            "NUDGEBEE_ENCRYPTION_KEY": "test",
            "RABBIT_MQ_HOST": "localhost",
            "RABBIT_MQ_PASSWORD": "guest",
            "RABBIT_MQ_PORT": "5672",
            "RABBIT_MQ_USERNAME": "guest",
            "REDIS_SERVER_HOST": "localhost",
            "REDIS_SERVER_PORT": "6379",
            "REDIS_USER_NAME": "",
            "REDIS_USER_PASSWORD": "",
        }
    )
    result = subprocess.run(
        [
            sys.executable,
            "-c",
            "from app import app; "
            "response = app.test_client().get('/health'); "
            "print(response.status_code); print(response.get_json())",
        ],
        cwd=APP_DIR,
        env=env,
        capture_output=True,
        check=True,
        text=True,
    )

    assert result.stdout.splitlines()[-2:] == ["200", "{'status': 'ok'}"]
