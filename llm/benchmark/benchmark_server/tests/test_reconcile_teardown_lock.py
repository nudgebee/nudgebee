"""The reconciler tears a handed-off test down only while its run holds the fixture."""

import pytest

from benchmark_server.utils import run_manager
from llm.agents.common import fixture_lock, lifecycle

CTX = {
    "after_test": "kubectl delete namespace app-227-payments",
    "__path__": "/x/llm/agents/nubi/fixtures/227_x/test_case.yaml",
    "__id__": "227_x",
    "lock_holder": "run-a",
}


@pytest.fixture
def calls(monkeypatch):
    log = []
    monkeypatch.setattr(lifecycle, "run_after_test", lambda ctx: log.append("teardown"))
    monkeypatch.setattr(
        fixture_lock, "release", lambda ctx, holder: log.append(f"release:{holder}")
    )
    return log


def test_tears_down_and_releases_while_still_holding(monkeypatch, calls):
    monkeypatch.setattr(fixture_lock, "try_acquire", lambda ctx, holder: True)
    run_manager._run_after_test_teardown(dict(CTX))
    assert calls == ["teardown", "release:run-a"]


def test_skips_teardown_when_another_run_took_the_fixture(monkeypatch, calls):
    monkeypatch.setattr(fixture_lock, "try_acquire", lambda ctx, holder: False)
    run_manager._run_after_test_teardown(dict(CTX))
    assert calls == []


def test_unlocked_test_tears_down_as_before(monkeypatch, calls):
    monkeypatch.setattr(
        fixture_lock, "try_acquire", lambda ctx, holder: pytest.fail("no lock to take")
    )
    run_manager._run_after_test_teardown(
        {k: v for k, v in CTX.items() if k != "lock_holder"}
    )
    assert calls == ["teardown"]
