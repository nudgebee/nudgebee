"""/health must keep answering while agent uploads are stalled mid-request.

Pins a liveness-restart failure: the image ran gunicorn with 2 sync workers,
and /health shares them with every request. Agent POSTs to
/v1/k8s/discovery that stalled before their body arrived each held a worker
for ~60s (the agent's HTTP client timeout). With both workers held, /health
could not answer within the probe's 1s timeout, and after 3 misses the kubelet
restarted the container.

This starts gunicorn with the exact CMD from the Dockerfile, so the test fails
if the image goes back to a worker model where a stalled request blocks /health.
"""

import json
import os
import re
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

APP_DIR = Path(__file__).parents[1]
# More stalled requests than the 2 sync workers the image used to run, and
# fewer than the threads of one worker. gthread accepts a connection even when
# all of its threads are busy, so the kernel can hand every stall to the same
# worker; with fewer stalls than its threads, each worker keeps one free.
STALLED_UPLOADS = 3

_ENV = {
    "ACTION_API_SERVER_TOKEN": "",
    "CLICKHOUSE_ENABLED": "false",
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


def dockerfile_gunicorn_args() -> list:
    """The image's CMD with the bind address dropped (the test binds its own)."""
    cmd = re.search(r"^CMD (\[.*\])$", (APP_DIR / "Dockerfile").read_text(), re.MULTILINE)
    assert cmd, "Dockerfile has no exec-form CMD"
    args = json.loads(cmd.group(1))
    assert args[0] == "gunicorn"
    return [a for a in args[1:] if not a.startswith("--bind")]


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


def _health(port: int, timeout: float) -> int:
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=timeout) as resp:
        return int(resp.status)


def _stall_upload(port: int) -> socket.socket:
    """Open an upload and stop sending partway, as a client that gives up does.

    Headers are left unfinished so the request never reaches the app (whose
    auth would need a database); the worker is held reading it either way.
    """
    s = socket.create_connection(("127.0.0.1", port))
    s.sendall(
        b"POST /v1/k8s/discovery HTTP/1.1\r\n"
        b"Host: collector\r\n"
        b"Content-Encoding: gzip\r\n"
        b"Content-Length: 1048576\r\n"
    )
    return s


def run_probe_check(gunicorn_args: list) -> tuple:
    """Start gunicorn, stall uploads, and return 3 probe-like /health outcomes plus gunicorn's output."""
    port = _free_port()
    env = {**os.environ, **_ENV}
    output = tempfile.TemporaryFile()
    server = subprocess.Popen(
        [sys.executable, "-m", "gunicorn", f"--bind=127.0.0.1:{port}", *gunicorn_args],
        cwd=APP_DIR,
        env=env,
        stdout=output,
        stderr=subprocess.STDOUT,
    )
    stalled = []
    try:
        deadline = time.monotonic() + 60
        while True:
            try:
                if _health(port, timeout=1) == 200:
                    break
            except OSError:
                pass
            if server.poll() is not None or time.monotonic() > deadline:
                return ["gunicorn did not come up"], _read(output)
            time.sleep(0.5)

        stalled = [_stall_upload(port) for _ in range(STALLED_UPLOADS)]
        time.sleep(1)

        outcomes: list = []
        for _ in range(3):
            try:
                outcomes.append(_health(port, timeout=1))
            except OSError as e:
                outcomes.append(type(e).__name__)
        return outcomes, _read(output)
    finally:
        for s in stalled:
            s.close()
        server.terminate()
        try:
            server.wait(timeout=30)
        except subprocess.TimeoutExpired:
            server.kill()
        output.close()


def _read(output) -> str:
    output.flush()
    output.seek(0)
    return str(output.read().decode(errors="replace"))


def test_health_answers_while_uploads_are_stalled():
    outcomes, log = run_probe_check(dockerfile_gunicorn_args())
    assert outcomes == [200, 200, 200], log
