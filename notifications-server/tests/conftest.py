import os
import sys

import pytest

# A sync Cache call made on a running event loop fails the test instead of only
# logging a warning. Set through the environment so this file never imports the
# package: some tests stub notifications_server in sys.modules first.
os.environ.setdefault("NOTIFICATIONS_FORBID_EVENT_LOOP_REDIS", "1")


def _reset_redis_cool_down():
    cache = sys.modules.get("notifications_server.services.cache")
    cool_down = getattr(cache, "_cool_down", None)
    if cool_down is not None:
        cool_down.reset()


@pytest.fixture(autouse=True)
def redis_cool_down_reset():
    # Tests run without Redis, so any test can start the cool-down; don't let it
    # carry into the next one.
    _reset_redis_cool_down()
    yield
    _reset_redis_cool_down()
