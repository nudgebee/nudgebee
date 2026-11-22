"""A fixture another run holds is retried later, then skipped — never failed."""

import pytest

from benchmark_server import orchestrator
from benchmark_server.orchestrator import TestOrchestrator, TestResult


class Session:
    run_id = ""


@pytest.fixture(autouse=True)
def quiet(monkeypatch):
    monkeypatch.setattr(orchestrator, "update_progress", lambda *a, **k: None)
    monkeypatch.setattr(orchestrator, "_wait_before_retry", lambda run_id: True)


def case(test_id):
    return {"__id__": test_id, "__global_index__": int(test_id.split("_")[0])}


def rounds(script):
    """A run_round that answers busy for each id as many rounds as script says."""
    calls = []

    def run_round(cases, session, config, total, progress):
        calls.append([c["__id__"] for c in cases])
        out = []
        for c in cases:
            busy = script.get(c["__id__"], 0) > 0
            script[c["__id__"]] = script.get(c["__id__"], 0) - 1
            out.append(
                (
                    c,
                    TestResult(
                        c["__id__"], c["__global_index__"], "busy" if busy else "pass"
                    ),
                )
            )
        return out

    return run_round, calls


def test_busy_fixture_runs_after_the_rest_once_free():
    run_round, calls = rounds({"2_shared": 2})
    results = TestOrchestrator(1)._execute_with_requeue(
        [case("1_a"), case("2_shared"), case("3_b")], Session(), 3, run_round, {}
    )
    assert calls == [["1_a", "2_shared", "3_b"], ["2_shared"], ["2_shared"]]
    assert sorted((r.test_id, r.status) for r in results) == [
        ("1_a", "pass"),
        ("2_shared", "pass"),
        ("3_b", "pass"),
    ]


def test_fixture_busy_past_the_limit_is_skipped_not_failed(monkeypatch):
    monkeypatch.setattr(orchestrator, "FIXTURE_BUSY_GIVE_UP_SEC", 0)
    stored = []
    monkeypatch.setattr(
        orchestrator, "store_test_result", lambda run_id, row: stored.append(row)
    )
    run_round, _ = rounds({"2_shared": 99})
    session = Session()
    session.run_id = "run-b"
    results = TestOrchestrator(1)._execute_with_requeue(
        [case("2_shared")], session, 1, run_round, {}
    )
    assert [(r.test_id, r.status) for r in results] == [("2_shared", "skipped")]
    assert (
        stored[0]["status"] == "skipped"
        and stored[0]["error_category"] == "fixture_busy"
    )


def test_stopped_run_stops_retrying(monkeypatch):
    monkeypatch.setattr(orchestrator, "_wait_before_retry", lambda run_id: False)
    run_round, calls = rounds({"2_shared": 99})
    results = TestOrchestrator(1)._execute_with_requeue(
        [case("2_shared")], Session(), 1, run_round, {}
    )
    assert results == [] and len(calls) == 1
