"""Cross-run lock on a fixture's cluster resources.

Fixtures use fixed namespace names (``app-227-*``) that their prompts and
expected answers depend on, so two benchmark runs executing the same fixture at
once delete or overwrite each other's resources: one run's before_test
pre-clean or after_test teardown removes namespaces the other run is still
testing against. Fixtures with different ids own different names, which is why
one run already executes them in parallel safely; only the same fixture id in
two runs collides.

So each fixture gets a Kubernetes Lease (``bench-<agent>-<id>``) in the cluster
the fixtures act on. A run holds it from before_test until after_test — or,
for a test handed off to the reconciler, until the reconciler's teardown — and
a run that finds it held by another run retries that fixture later instead of
running it.

The Lease is managed with kubectl, like the fixtures themselves. Every write is
pinned to the resourceVersion just read, so two runs can never both take it,
and a run only ever clears its own hold. A holder renews every RENEW_EVERY_SEC
from one thread per process; if the process dies, the Lease expires after
LEASE_SECONDS and the next run takes it over (its pre-clean then removes what
the dead run left behind).
"""

import json
import logging
import os
import re
import socket
import subprocess
import threading
import time
from datetime import datetime, timedelta, timezone
from hashlib import sha1
from pathlib import Path
from typing import Any, Dict, Optional

logger = logging.getLogger(__name__)

LOCK_NAMESPACE = os.getenv("BENCHMARK_LOCK_NAMESPACE", "default")
LEASE_SECONDS = 600
RENEW_EVERY_SEC = 60
KUBECTL_TIMEOUT_SEC = 30
LABEL = "benchmark-lock"

_held: Dict[str, str] = {}  # lease name -> holder, renewed while present
_held_mu = threading.Lock()
_renewer: Optional[threading.Thread] = None
_access_ok = False  # check_access passed once in this process


class LockAccessError(RuntimeError):
    """The benchmark cannot manage Leases, so it cannot run fixtures safely."""


def needs_lock(test_case: Dict[str, Any]) -> bool:
    """Only fixtures that set up or tear down cluster resources need the lock."""
    return bool(test_case.get("before_test") or test_case.get("after_test"))


def lease_name(test_case: Dict[str, Any]) -> str:
    """A valid Lease name for a fixture (DNS-1123, at most 63 characters).

    Keyed by agent and fixture id: ids repeat across agents
    (``.../agents/<agent>/fixtures/<id>/test_case.yaml``).
    """
    path = Path(test_case.get("__path__") or "")
    agent = path.parents[2].name if len(path.parents) > 2 else ""
    key = f"{agent}-{test_case.get('__id__', 'unknown')}"
    slug = re.sub(r"[^a-z0-9-]+", "-", key.lower()).strip("-") or "fixture"
    name = f"bench-{slug}"
    if len(name) > 63:
        name = f"{name[:54].rstrip('-')}-{sha1(key.encode()).hexdigest()[:8]}"
    return name


def holder_for(run_id: str) -> str:
    """The holder identity: the run id, or this process for runs without one."""
    return run_id or f"{socket.gethostname()}-{os.getpid()}"


def _kubectl(args, stdin: Optional[str] = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["kubectl", *args],
        input=stdin,
        capture_output=True,
        text=True,
        timeout=KUBECTL_TIMEOUT_SEC,
    )


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _micro(t: datetime) -> str:
    return t.strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def _get(name: str) -> Optional[Dict[str, Any]]:
    """The Lease, or None when it doesn't exist; raises on any other failure."""
    r = _kubectl(["get", "lease", name, "-n", LOCK_NAMESPACE, "-o", "json"])
    if r.returncode == 0:
        return json.loads(r.stdout)
    if "NotFound" in r.stderr:
        return None
    raise RuntimeError(f"kubectl get lease {name}: {r.stderr.strip()}")


def _expired(spec: Dict[str, Any], now: datetime) -> bool:
    renewed = spec.get("renewTime") or spec.get("acquireTime")
    if not renewed:
        return True
    try:
        at = datetime.fromisoformat(renewed)
    except ValueError:
        return False  # unreadable: assume still held rather than take a live lock
    if at.tzinfo is None:
        at = at.replace(tzinfo=timezone.utc)
    return now > at + timedelta(
        seconds=int(spec.get("leaseDurationSeconds") or LEASE_SECONDS)
    )


def _write(lease: Dict[str, Any], verb: str) -> bool:
    """create or replace; False when another run changed it first."""
    r = _kubectl([verb, "-f", "-"], stdin=json.dumps(lease))
    if r.returncode == 0:
        return True
    if (
        "AlreadyExists" in r.stderr
        or "Conflict" in r.stderr
        or "has been modified" in r.stderr
    ):
        return False
    raise RuntimeError(
        f"kubectl {verb} lease {lease['metadata']['name']}: {r.stderr.strip()}"
    )


def try_acquire(test_case: Dict[str, Any], holder: str) -> bool:
    """Take the fixture's Lease if it is free, expired or already ours.

    Returns False when another run holds it (or took it first); the caller
    runs the fixture later. A kubectl failure counts as held: running without
    the lock is what this exists to prevent.
    """
    name = lease_name(test_case)
    test_id = test_case.get("__id__", "unknown")
    now = _now()
    try:
        lease = _get(name)
        if lease is None:
            ok = _write(
                {
                    "apiVersion": "coordination.k8s.io/v1",
                    "kind": "Lease",
                    "metadata": {
                        "name": name,
                        "namespace": LOCK_NAMESPACE,
                        "labels": {LABEL: "true"},
                    },
                    "spec": {
                        "holderIdentity": holder,
                        "leaseDurationSeconds": LEASE_SECONDS,
                        "acquireTime": _micro(now),
                        "renewTime": _micro(now),
                    },
                },
                "create",
            )
        else:
            spec = lease.setdefault("spec", {})
            current = spec.get("holderIdentity") or ""
            if current and current != holder and not _expired(spec, now):
                return False
            if current and current != holder:
                logger.warning(
                    "[%s] taking over expired fixture lock from %s", test_id, current
                )
            spec.update(
                holderIdentity=holder,
                leaseDurationSeconds=LEASE_SECONDS,
                acquireTime=_micro(now),
                renewTime=_micro(now),
            )
            ok = _write(lease, "replace")
    except Exception as e:  # noqa: BLE001 — any failure means "not ours"
        logger.warning("[%s] fixture lock: %s", test_id, e)
        return False
    if ok:
        _track(name, holder)
    return ok


def acquire_waiting(
    test_case: Dict[str, Any], holder: str, give_up_sec: int, retry_sec: int = 30
) -> bool:
    """try_acquire, retrying every retry_sec for up to give_up_sec.

    For runners that execute one fixture at a time and have nothing else to
    do meanwhile (the pytest path). Raises LockAccessError at once, instead of
    waiting the whole give_up_sec, when Leases can't be managed at all (no
    kubectl, no cluster, no rights); checked once per process.
    """
    global _access_ok
    if not _access_ok:
        check_access()
        _access_ok = True
    deadline = time.time() + give_up_sec
    while True:
        if try_acquire(test_case, holder):
            return True
        if time.time() + retry_sec > deadline:
            return False
        logger.info(
            "[%s] Fixture in use by another run, waiting %ds",
            test_case.get("__id__"),
            retry_sec,
        )
        time.sleep(retry_sec)


def release(test_case: Dict[str, Any], holder: str) -> None:
    """Give the Lease up if this holder still has it. Safe to call twice."""
    name = lease_name(test_case)
    test_id = test_case.get("__id__", "unknown")
    with _held_mu:
        _held.pop(name, None)
    for _ in range(3):
        try:
            lease = _get(name)
            if (
                lease is None
                or (lease.get("spec") or {}).get("holderIdentity") != holder
            ):
                return
            lease["spec"].pop("holderIdentity", None)
            if _write(lease, "replace"):
                return
        except Exception as e:  # noqa: BLE001 — it expires on its own
            logger.warning("[%s] releasing fixture lock: %s", test_id, e)
            return


def _still_tracked(name: str, holder: str) -> bool:
    with _held_mu:
        return _held.get(name) == holder


def _renew(name: str, holder: str) -> None:
    # The renewer works from a snapshot, so the lock may have been released
    # since; that is not a lost lock.
    if not _still_tracked(name, holder):
        return
    lease = _get(name)
    spec = (lease or {}).get("spec") or {}
    if lease is None or spec.get("holderIdentity") != holder:
        with _held_mu:
            if _held.get(name) != holder:
                return  # released while we read it
            del _held[name]
        logger.error(
            "fixture lock %s was lost (now held by %r)",
            name,
            spec.get("holderIdentity"),
        )
        return
    spec["renewTime"] = _micro(_now())
    if not _write(lease, "replace"):
        logger.warning(
            "fixture lock %s changed while renewing; retrying next round", name
        )


def _renew_loop() -> None:
    while True:
        time.sleep(RENEW_EVERY_SEC)
        with _held_mu:
            held = list(_held.items())
        for name, holder in held:
            try:
                _renew(name, holder)
            except Exception as e:  # noqa: BLE001 — retried next round
                logger.warning("renewing fixture lock %s: %s", name, e)


def _track(name: str, holder: str) -> None:
    global _renewer
    with _held_mu:
        _held[name] = holder
        if _renewer is None or not _renewer.is_alive():
            _renewer = threading.Thread(
                target=_renew_loop, name="fixture-lock-renewer", daemon=True
            )
            _renewer.start()


def check_access() -> None:
    """Fail fast when Leases can't be managed in LOCK_NAMESPACE."""
    for verb in ("get", "create", "update"):
        try:
            r = _kubectl(
                [
                    "auth",
                    "can-i",
                    verb,
                    "leases.coordination.k8s.io",
                    "-n",
                    LOCK_NAMESPACE,
                ]
            )
        except Exception as e:  # noqa: BLE001
            raise LockAccessError(f"cannot check Lease access: {e}") from e
        answer = r.stdout.strip()
        if answer not in ("yes", "no"):
            # kubectl couldn't answer (no cluster, bad kubeconfig, auth plugin)
            detail = r.stderr.strip() or answer or f"exit {r.returncode}"
            raise LockAccessError(f"cannot check Lease access: {detail}")
        if answer != "yes":
            raise LockAccessError(
                f"the benchmark's cluster identity cannot {verb} Leases in namespace "
                f"{LOCK_NAMESPACE!r}, which it needs to keep concurrent runs from deleting "
                f"each other's fixture resources (set BENCHMARK_LOCK_NAMESPACE to a "
                f"namespace it can use)"
            )
