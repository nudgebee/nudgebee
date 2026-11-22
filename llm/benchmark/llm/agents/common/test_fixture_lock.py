"""Tests for the cross-run fixture lock, against a fake kubectl."""

import json
import re
import subprocess
from datetime import timedelta

import pytest

from llm.agents.common import fixture_lock


class FakeKubectl:
    """Leases in memory, with the API server's create/replace semantics."""

    def __init__(self):
        self.leases = {}
        self.version = 0
        self.can_i = "yes"
        self.fail = False
        self.before_replace = None  # hook: another run writing in between

    def __call__(self, args, stdin=None):
        def done(code=0, out="", err=""):
            return subprocess.CompletedProcess(args, code, out, err)

        if self.fail:
            return done(1, err="Unable to connect to the server")
        if args[:2] == ["auth", "can-i"]:
            return done(0 if self.can_i == "yes" else 1, out=self.can_i + "\n")
        if args[0] == "get":
            lease = self.leases.get(args[2])
            if lease is None:
                return done(
                    1, err=f'Error from server (NotFound): leases "{args[2]}" not found'
                )
            return done(out=json.dumps(lease))
        body = json.loads(stdin)
        name = body["metadata"]["name"]
        if args[0] == "create":
            if name in self.leases:
                return done(1, err="Error from server (AlreadyExists)")
            return done(out=self._store(body))
        if args[0] == "replace":
            if self.before_replace:
                hook, self.before_replace = self.before_replace, None
                hook()
            current = self.leases.get(name)
            if current is None or current["metadata"]["resourceVersion"] != body[
                "metadata"
            ].get("resourceVersion"):
                return done(
                    1,
                    err="Operation cannot be fulfilled: the object has been modified (Conflict)",
                )
            return done(out=self._store(body))
        raise AssertionError(f"unexpected kubectl {args}")

    def _store(self, body):
        self.version += 1
        body["metadata"]["resourceVersion"] = str(self.version)
        self.leases[body["metadata"]["name"]] = body
        return "ok"

    def holder(self, name):
        return (self.leases.get(name, {}).get("spec") or {}).get("holderIdentity")


@pytest.fixture
def kubectl(monkeypatch):
    fake = FakeKubectl()
    monkeypatch.setattr(fixture_lock, "_kubectl", fake)
    monkeypatch.setattr(
        fixture_lock, "_track", lambda name, holder: None
    )  # no renewer thread
    return fake


def tc(test_id="227_count_configmaps_per_namespace", agent="nubi"):
    return {
        "__id__": test_id,
        "__path__": f"/x/llm/agents/{agent}/fixtures/{test_id}/test_case.yaml",
        "before_test": "kubectl create namespace app-227-payments",
    }


def test_lease_name_is_valid_and_keyed_by_agent():
    name = fixture_lock.lease_name(tc())
    assert name == "bench-nubi-227-count-configmaps-per-namespace"
    assert fixture_lock.lease_name(tc(agent="logs")) != name
    long = fixture_lock.lease_name(tc(test_id="9_" + "very_long_fixture_name_" * 5))
    assert len(long) <= 63 and re.fullmatch(r"[a-z0-9]([a-z0-9-]*[a-z0-9])?", long)


def test_needs_lock_only_for_fixtures_with_lifecycle():
    assert fixture_lock.needs_lock({"before_test": "x"})
    assert fixture_lock.needs_lock({"after_test": "x"})
    assert not fixture_lock.needs_lock({"user_prompt": "list pods"})


def test_second_run_cannot_take_a_held_fixture(kubectl):
    assert fixture_lock.try_acquire(tc(), "run-a")
    assert not fixture_lock.try_acquire(tc(), "run-b")
    assert fixture_lock.try_acquire(tc(), "run-a")  # its own hold
    assert kubectl.holder(fixture_lock.lease_name(tc())) == "run-a"


def test_release_frees_the_fixture_only_for_its_holder(kubectl):
    name = fixture_lock.lease_name(tc())
    fixture_lock.try_acquire(tc(), "run-a")
    fixture_lock.release(tc(), "run-b")
    assert kubectl.holder(name) == "run-a"
    fixture_lock.release(tc(), "run-a")
    assert kubectl.holder(name) is None
    fixture_lock.release(tc(), "run-a")  # twice is fine
    assert fixture_lock.try_acquire(tc(), "run-b")


def test_expired_hold_is_taken_over(kubectl):
    fixture_lock.try_acquire(tc(), "run-crashed")
    lease = kubectl.leases[fixture_lock.lease_name(tc())]
    stale = fixture_lock._now() - timedelta(seconds=fixture_lock.LEASE_SECONDS + 1)
    lease["spec"]["renewTime"] = fixture_lock._micro(stale)
    assert fixture_lock.try_acquire(tc(), "run-b")
    assert kubectl.holder(fixture_lock.lease_name(tc())) == "run-b"


def test_renew_time_parses_at_any_precision_and_unreadable_counts_as_held():
    now = fixture_lock._now()
    spec = {"leaseDurationSeconds": 600}
    recent = now.replace(microsecond=0)
    assert not fixture_lock._expired(
        {**spec, "renewTime": recent.strftime("%Y-%m-%dT%H:%M:%SZ")}, now
    )
    old = recent - timedelta(seconds=601)
    assert fixture_lock._expired(
        {**spec, "renewTime": old.strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"}, now
    )
    assert not fixture_lock._expired({**spec, "renewTime": "not-a-time"}, now)


def test_two_runs_racing_for_a_free_fixture_get_one_winner(kubectl):
    fixture_lock.try_acquire(tc(), "run-a")
    fixture_lock.release(tc(), "run-a")

    # run-c takes the free Lease between run-b's read and its write.
    kubectl.before_replace = lambda: kubectl._store(
        {
            "metadata": {"name": fixture_lock.lease_name(tc())},
            "spec": {
                "holderIdentity": "run-c",
                "renewTime": fixture_lock._micro(fixture_lock._now()),
            },
        }
    )
    assert not fixture_lock.try_acquire(tc(), "run-b")
    assert kubectl.holder(fixture_lock.lease_name(tc())) == "run-c"


def test_kubectl_failure_counts_as_held(kubectl):
    kubectl.fail = True
    assert not fixture_lock.try_acquire(tc(), "run-a")


def test_acquire_waiting_fails_fast_without_lease_access(kubectl, monkeypatch):
    monkeypatch.setattr(fixture_lock, "_access_ok", False)
    monkeypatch.setattr(
        fixture_lock.time, "sleep", lambda s: pytest.fail("must not wait")
    )
    kubectl.fail = True
    with pytest.raises(fixture_lock.LockAccessError, match="Unable to connect"):
        fixture_lock.acquire_waiting(tc(), "run-a", give_up_sec=1200)


def test_acquire_waiting_checks_access_once_per_process(kubectl, monkeypatch):
    monkeypatch.setattr(fixture_lock, "_access_ok", False)
    checks = []
    monkeypatch.setattr(fixture_lock, "check_access", lambda: checks.append(1))
    assert fixture_lock.acquire_waiting(tc("1_a"), "run-a", give_up_sec=60)
    assert fixture_lock.acquire_waiting(tc("2_b"), "run-a", give_up_sec=60)
    assert checks == [1]


def test_acquire_waiting_gives_up_after_the_limit(kubectl, monkeypatch):
    slept = []
    monkeypatch.setattr(fixture_lock.time, "sleep", slept.append)
    clock = iter(range(0, 10000, 30))
    monkeypatch.setattr(fixture_lock.time, "time", lambda: next(clock))
    fixture_lock.try_acquire(tc(), "run-a")
    assert not fixture_lock.acquire_waiting(
        tc(), "run-b", give_up_sec=120, retry_sec=30
    )
    assert slept and all(s == 30 for s in slept)


def test_check_access_fails_loudly_without_lease_rights(kubectl):
    fixture_lock.check_access()
    kubectl.can_i = "no"
    with pytest.raises(fixture_lock.LockAccessError, match="cannot get Leases"):
        fixture_lock.check_access()


def test_check_access_reports_kubectl_failure_not_permissions(kubectl):
    kubectl.fail = True  # e.g. no cluster reachable: empty stdout, error on stderr
    with pytest.raises(
        fixture_lock.LockAccessError, match="Unable to connect to the server"
    ):
        fixture_lock.check_access()


def test_renew_after_release_is_not_reported_as_lost(kubectl, monkeypatch, caplog):
    monkeypatch.setattr(fixture_lock, "_held", {})
    name = fixture_lock.lease_name(tc())
    fixture_lock.try_acquire(tc(), "run-a")
    fixture_lock._held[name] = "run-a"
    # The renewer snapshotted the lock, then the test released it.
    fixture_lock.release(tc(), "run-a")
    with caplog.at_level("ERROR"):
        fixture_lock._renew(name, "run-a")
    assert "was lost" not in caplog.text

    # Released while the renewer was reading the Lease: still not lost.
    fixture_lock.try_acquire(tc(), "run-a")
    fixture_lock._held[name] = "run-a"
    real_get = fixture_lock._get

    def get_then_release(n):
        lease = real_get(n)
        fixture_lock._held.pop(n, None)
        lease["spec"].pop("holderIdentity", None)
        return lease

    monkeypatch.setattr(fixture_lock, "_get", get_then_release)
    with caplog.at_level("ERROR"):
        fixture_lock._renew(name, "run-a")
    assert "was lost" not in caplog.text


def test_lock_taken_by_another_run_is_reported_as_lost(kubectl, monkeypatch, caplog):
    monkeypatch.setattr(fixture_lock, "_held", {})
    name = fixture_lock.lease_name(tc())
    fixture_lock.try_acquire(tc(), "run-a")
    fixture_lock._held[name] = "run-a"
    kubectl.leases[name]["spec"]["holderIdentity"] = "run-b"
    with caplog.at_level("ERROR"):
        fixture_lock._renew(name, "run-a")
    assert "was lost" in caplog.text and name not in fixture_lock._held
