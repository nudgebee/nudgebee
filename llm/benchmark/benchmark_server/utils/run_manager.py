"""Benchmark run lifecycle manager.

Tracks benchmark runs through their full lifecycle using PostgreSQL.
Individual test results are stored progressively so data survives restarts.

Lifecycle phases:
    INITIALIZING -> RUNNING_TESTS -> EVALUATING
        -> GENERATING_REPORT -> SENDING_REPORT -> DONE
    Any phase can transition to DONE (with state FAILED/STOPPED).
"""

import concurrent.futures
import logging
import os
import threading
import time
import uuid
from datetime import datetime, timedelta, timezone
from enum import Enum
from typing import Optional

from sqlalchemy import func

from benchmark_server.models.benchmark_run import BenchmarkRun, BenchmarkTestResult
from benchmark_server.utils.db_utils import get_db

logger = logging.getLogger(__name__)


class RunState(str, Enum):
    GATHERED = "gathered"
    RUNNING = "running"
    PAUSED = "paused"
    STOPPED = "stopped"
    COMPLETED = "completed"
    FAILED = "failed"


class RunPhase(str, Enum):
    INITIALIZING = "initializing"
    GATHERED = "gathered"
    DEPLOYING_INFRA = "deploying_infra"
    RUNNING_TESTS = "running_tests"
    EVALUATING = "evaluating"
    GENERATING_REPORT = "generating_report"
    SENDING_REPORT = "sending_report"
    DONE = "done"


# ---------------------------------------------------------------------------
# Startup recovery
# ---------------------------------------------------------------------------


def recover_orphaned_runs():
    """Reconcile-then-quarantine orphaned runs on server startup.

    The old behavior blindly marked every RUNNING/PAUSED run FAILED and all
    in-progress tests as errors. That discarded legitimate waiting followups
    whose conversations were still alive on the llm-server. The new behavior:

      1. Collect RUNNING/PAUSED runs.
      2. For each, run ``reconcile_waiting_tests(run_id)`` so tests whose
         conversations already terminated get their final answer stored.
      3. If the run has any remaining ``waiting`` tests → leave it RUNNING
         (user can still submit followups; periodic reconcile will keep up).
      4. Only then — if the run has tests stuck in ``running`` (no conv_id,
         genuinely orphaned) — mark those as error and fail the run.
    """
    # Phase 1 — snapshot orphaned run IDs (keep read transaction short).
    db = get_db()
    if not db:
        return
    try:
        orphaned = (
            db.query(BenchmarkRun)
            .filter(BenchmarkRun.state.in_([RunState.RUNNING, RunState.PAUSED]))
            .all()
        )
        orphan_ids = [r.run_id for r in orphaned]
    except Exception:
        logger.exception("Failed to list orphaned runs")
        orphan_ids = []
    finally:
        db.close()

    if not orphan_ids:
        return

    logger.info("Orphan recovery: inspecting %d run(s)", len(orphan_ids))

    # Phase 2 — reconcile each.
    for run_id in orphan_ids:
        try:
            reconcile_waiting_tests(run_id)
        except Exception:
            logger.exception("Orphan reconciliation failed for %s", run_id)

    # Phase 3 — reopen each run and decide final status.
    db = get_db()
    if not db:
        return
    # Runs whose row was bumped more recently than this are presumed alive —
    # an actively-running benchmark's orchestrator calls ``update_progress``
    # after every test completion, which refreshes ``updated_at``. This guard
    # exists because this function is now also called from the list_runs
    # endpoint (every 60s while any dashboard is open), not just at server
    # startup. Without the guard the sweep would mark in-flight runs FAILED
    # the moment they have no test row in ``running`` (e.g. between two
    # parallel batches when the executor is still dequeueing the next
    # workers' tests). 5 min covers worst-case per-test runtime on the
    # cluster's slowest cases.
    fresh_cutoff = datetime.now(timezone.utc).replace(tzinfo=None) - timedelta(minutes=5)
    try:
        for run_id in orphan_ids:
            run = _get_run(db, run_id)
            if not run or run.state not in (RunState.RUNNING, RunState.PAUSED):
                continue

            if run.updated_at and run.updated_at > fresh_cutoff:
                # Run row was touched within the freshness window — the
                # orchestrator is almost certainly still pumping progress
                # updates, so this isn't an orphan.
                continue

            waiting_left = (
                db.query(BenchmarkTestResult)
                .filter(
                    BenchmarkTestResult.run_id == run_id,
                    BenchmarkTestResult.status == "waiting",
                )
                .count()
            )
            running_left = (
                db.query(BenchmarkTestResult)
                .filter(
                    BenchmarkTestResult.run_id == run_id,
                    BenchmarkTestResult.status == "running",
                )
                .count()
            )

            if waiting_left > 0 and running_left == 0:
                # Benign orphan: server restarted while tests awaited user
                # input. Keep the run alive so the UI can accept followups.
                logger.info(
                    "Orphan recovery: run %s kept RUNNING (%d waiting, 0 running)",
                    run_id,
                    waiting_left,
                )
                continue

            # Genuine orphan — tests stuck in-flight or no progress possible.
            run.state = RunState.FAILED
            run.phase = RunPhase.DONE
            run.completed_at = datetime.now()
            if run.started_at:
                run.duration_seconds = round((datetime.now() - run.started_at).total_seconds(), 2)
            errors = list(run.errors or [])
            errors.append(
                {
                    "time": datetime.now().isoformat(),
                    "message": "Server restarted — run was orphaned",
                }
            )
            run.errors = errors
            _mark_running_tests(db, run.run_id, "error", "Server restarted — run was orphaned")
            logger.warning(
                "Orphan recovery: marked run %s FAILED (%d waiting, %d running)",
                run_id,
                waiting_left,
                running_left,
            )
        db.commit()
    except Exception:
        db.rollback()
        logger.exception("Failed to finalize orphan recovery")
    finally:
        db.close()


# ---------------------------------------------------------------------------
# Server-side API (used by FastAPI controller)
# ---------------------------------------------------------------------------


def create_run(
    agent: str,
    user_id: str = None,
    account_id: str = None,
    tenant_id: str = None,
    tool_config: str = None,
    max_tests: int = None,
    test_indices: str = None,
    test_filter: str = None,
    tag_filter: str = None,
    cc_emails: list = None,
    parallel_workers: int = None,
    run_name: str = None,
) -> str:
    """Create a new benchmark run and return its ID."""
    run_id = uuid.uuid4().hex[:12]
    if not run_name:
        from datetime import date

        run_name = f"{agent}-{date.today().strftime('%Y%m%d')}-{run_id[:8]}"
    db = get_db()
    if not db:
        logger.error("Cannot create run: database not configured")
        return run_id
    try:
        run = BenchmarkRun(
            run_id=run_id,
            agent=agent,
            state=RunState.RUNNING,
            phase=RunPhase.INITIALIZING,
            user_id=user_id,
            account_id=account_id,
            tenant_id=tenant_id,
            tool_config=tool_config,
            max_tests=max_tests,
            test_indices=test_indices,
            test_filter=test_filter,
            tag_filter=tag_filter,
            parallel_workers=parallel_workers,
            run_name=run_name,
            cc_emails=cc_emails,
            errors=[],
        )
        db.add(run)
        db.commit()
        logger.info("Created benchmark run %s for agent %s", run_id, agent)
    except Exception:
        db.rollback()
        logger.exception("Failed to create run %s", run_id)
    finally:
        db.close()
    return run_id


def _get_run(db, run_id: str) -> BenchmarkRun:
    return db.query(BenchmarkRun).filter(BenchmarkRun.run_id == run_id).first()


# ---------------------------------------------------------------------------
# Reconciliation — catch up test rows to server-side conversation status
# ---------------------------------------------------------------------------
#
# The test row's ``status`` can drift from the llm-server conversation status
# in a handful of ways (all observed in production):
#   - llm-server resumed a parent via internal bubble-up and flipped the
#     conversation to COMPLETED, but the benchmark's poll has not yet
#     returned so the test row still says ``waiting``.
#   - A followup submission succeeded at the llm-server, but the controller's
#     background task crashed/restarted before persisting the final answer.
#   - A server restart cleared ``waiting`` without knowing that the
#     conversation had already moved on.
# ``reconcile_waiting_tests`` closes that gap by doing a one-shot read of
# each waiting test's conversation and transitioning the test row if the
# server considers the work done. It's safe to call from multiple places
# (complete_run gate, orphan recovery, periodic sweep) — it's idempotent.


def reconcile_waiting_tests(run_id: str, nudge_complete: bool = True) -> dict:
    """Reconcile waiting test rows against llm-server conversation state.

    For each test in ``waiting`` status with a conversation_id:
      - If conversation is COMPLETED → mark test pass (+ store final answer).
      - If conversation is FAILED/KILLED/TERMINATED → mark test fail.
      - If conversation is WAITING → refresh followup list on the row (so UI
        sees the current set of panels; e.g. after the llm-server's parent
        resume produced a new followup).
      - On network/server error → leave test untouched, log, retry later.

    Returns counts by outcome: ``{"completed": n, "failed": n, "refreshed": n, "error": n}``.
    """
    from benchmark_server.utils.llm_client import (
        fetch_conversation,
        extract_response_text,
    )
    from benchmark_server.utils.followup_events import emit as _fevent

    outcome = {"completed": 0, "failed": 0, "refreshed": 0, "error": 0, "skipped": 0}
    db = get_db()
    if not db:
        return outcome
    try:
        run = _get_run(db, run_id)
        if not run:
            return outcome
        account_id = run.account_id or ""
        tenant_id = run.tenant_id or ""
        user_id = run.user_id or ""
        waiting = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.status == "waiting",
            )
            .all()
        )
        # Parallelize the HTTP fetches: serial iteration was O(N) × 30s worst
        # case, stalling restart recovery for runs with many WAITING rows.
        # DB writes remain sequential below (single session).
        fetch_targets = [(tr, tr.conversation_id or "") for tr in waiting]
        prefetched = {}
        to_fetch = [(tr, cid) for tr, cid in fetch_targets if cid]
        if to_fetch:
            with concurrent.futures.ThreadPoolExecutor(max_workers=min(10, len(to_fetch))) as pool:
                futures = {
                    pool.submit(fetch_conversation, cid, account_id, tenant_id, user_id): tr.id
                    for tr, cid in to_fetch
                }
                for fut in concurrent.futures.as_completed(futures):
                    prefetched[futures[fut]] = fut.result()

        for tr in waiting:
            convo_id = tr.conversation_id or ""
            if not convo_id:
                outcome["skipped"] += 1
                continue
            result = prefetched.get(tr.id)
            if result is None:
                outcome["error"] += 1
                continue
            status = (result.status or "").upper()
            if status == "WAITING":
                # Refresh followups so UI shows current panel set.
                from benchmark_server.orchestrator import _wrap_followups_for_storage

                followups = list(result.followups or [])
                if followups:
                    tr.followup_request = _wrap_followups_for_storage(followups)
                    outcome["refreshed"] += 1
                elif tr.followup_request:
                    # Zombie WAITING: llm-server says WAITING but no pending
                    # followup exists (parent agent still running post-
                    # answer). Clear the stale input form from the row so
                    # the UI stops showing an already-answered question.
                    # Test stays ``waiting`` — next reconcile will catch the
                    # real terminal state.
                    tr.followup_request = None
                    outcome["refreshed"] += 1
                continue
            if status == "COMPLETED":
                answer = extract_response_text(result.data)
                tr.status = "pass" if answer else "fail"
                tr.actual_answer = answer or tr.actual_answer or ""
                tr.followup_request = None
                if not answer:
                    tr.error_message = tr.error_message or "Empty response after reconciliation"
                    tr.error_category = tr.error_category or "empty_response"
                outcome["completed"] += 1
                _fevent(
                    "reconciled",
                    run_id=run_id,
                    test_index=tr.test_index,
                    conversation_id=convo_id,
                    from_state="waiting",
                    to_state=tr.status,
                    reason="conversation_terminal",
                )
            elif status in ("FAILED", "KILLED", "TERMINATED"):
                tr.status = "fail"
                tr.followup_request = None
                tr.error_message = result.error_message or f"conversation {status}"
                tr.error_category = result.error_category or "agent_failed"
                outcome["failed"] += 1
                _fevent(
                    "reconciled",
                    run_id=run_id,
                    test_index=tr.test_index,
                    conversation_id=convo_id,
                    from_state="waiting",
                    to_state="fail",
                    reason=status.lower(),
                )
            else:
                # Non-terminal in-flight status — typically IN_PROGRESS, but
                # we don't enumerate to stay forward-compatible with any other
                # llm-server-side state name (e.g. PROCESSING, RUNNING). The
                # contract: anything that's not WAITING and not in the
                # terminal set above means the conversation is being processed
                # and we should NOT keep showing the answered followup form.
                # Clear ``followup_request`` so the UI sees row=waiting with
                # no form (rendered as "submitted, awaiting response"); the
                # next reconcile pass picks up the eventual COMPLETED/FAILED.
                # Without this branch, the row sat at waiting with the old
                # form forever — the visible "stuck on waiting" bug.
                if tr.followup_request:
                    tr.followup_request = None
                    outcome["refreshed"] += 1
        db.commit()
        if any(outcome.values()):
            logger.info("Reconciled run %s: %s", run_id, outcome)
    except Exception:
        db.rollback()
        logger.exception("Reconciliation failed for run %s", run_id)
    finally:
        db.close()

    # Skip the nudge when called from inside complete_run — that path runs
    # reconcile as a precursor to its own completion check, so a recursive
    # complete_run call here would loop indefinitely.
    if nudge_complete:
        _maybe_nudge_complete(run_id, outcome)
    return outcome


def _maybe_nudge_complete(run_id: str, outcome: dict) -> None:
    """Retry run completion if it might now be eligible.

    The orchestrator's finalize pass runs only once at end-of-run, so a run
    whose last waiting test reaches terminal state via a late followup answer
    will sit in RUNNING forever unless we re-attempt completion here. Nudge
    when either:
      - this pass transitioned a test (the last waiting one may have flipped), OR
      - every test row in the run is already in a terminal state (catches the
        missed-finalize case the sweeper rediscovers on subsequent passes).
    The terminal-state check is critical: complete_run itself only gates on
    `status='waiting'`, so a nudge issued while tests are still in `running`
    (subprocess crashed, never updated) would incorrectly flip the run to
    COMPLETED with empty results.
    """
    transitioned = outcome.get("completed", 0) or outcome.get("failed", 0)
    if not transitioned and not _run_has_all_terminal_tests(run_id):
        return
    try:
        complete_run(run_id)
    except Exception:
        logger.exception("Reconciliation: complete_run nudge failed for %s", run_id)


def remove_submitted_followup(
    run_id: str,
    test_index: int,
    agent_id: str,
    message_id: str,
) -> None:
    """Clear the answered followup from a test row's ``followup_request`` field.

    Called from ``submit_followup`` so the UI stops showing the same form for
    an already-submitted answer. Matches by (agent_id, message_id) — when the
    row has sibling followups in its ``followups`` list, only the matching
    one is removed and the rest keep showing. When the matching followup is
    the only one, ``followup_request`` is cleared entirely (UI reads an empty
    request as "submitted, processing"). Idempotent — safe to call repeatedly.
    """
    db = get_db()
    if not db:
        return
    try:
        tr = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.test_index == test_index,
            )
            .first()
        )
        if not tr or not tr.followup_request:
            return

        from benchmark_server.orchestrator import _wrap_followups_for_storage

        fr = tr.followup_request or {}
        followups = list(fr.get("followups") or fr.get("_all") or [])

        def _matches(fu: dict) -> bool:
            return str(fu.get("agent_id") or "") == (agent_id or "") and str(
                fu.get("message_id") or ""
            ) == (message_id or "")

        if not followups:
            # Legacy single-followup shape: only top-level fields, no list.
            if _matches(fr):
                tr.followup_request = None
            db.commit()
            return

        remaining = [fu for fu in followups if not _matches(fu)]
        if len(remaining) == len(followups):
            # No match (likely already removed or stale (agent_id, message_id)).
            return
        tr.followup_request = _wrap_followups_for_storage(remaining)
        db.commit()
    except Exception:
        db.rollback()
        logger.exception("remove_submitted_followup failed run=%s test=%d", run_id, test_index)
    finally:
        db.close()


def get_run_tenant_id(run_id: str):
    """Return the ``tenant_id`` of the given run, or ``None`` if missing.

    Cheap read used by route-level authorization to verify the caller's
    tenant matches the run's owning tenant before allowing mutation/read.
    Returns the empty string when the run row exists but its tenant_id is
    NULL (legacy rows from before tenant tracking was added).
    """
    db = get_db()
    if not db:
        return None
    try:
        run = _get_run(db, run_id)
        if run is None:
            return None
        return run.tenant_id or ""
    finally:
        db.close()


def _run_has_all_terminal_tests(run_id: str) -> bool:
    """True if run is still RUNNING and every planned test row is terminal.

    Terminal = not in (pending, waiting, running). Mirrors the gate in
    recover_orphaned_runs so a nudge here can't flip a run to COMPLETED while
    test rows are still pending/running (e.g. a crashed subprocess that left
    rows in 'running' without an updated_at refresh).

    Also requires the DB row count to meet the run's planned total
    (``progress_total``). The parallel orchestrator submits all test cases to
    a ThreadPoolExecutor up-front but only inserts a row when a worker thread
    actually starts the test — so during the window where the first batch has
    just finished and queued tests have not yet been dequeued, the DB
    legitimately contains only the in-flight subset, all in terminal state.
    Without the count gate, the sweeper would nudge ``complete_run`` here and
    the run would flip to COMPLETED while queued tests were still pending in
    the executor, causing them to abort via ``should_proceed`` with no row
    ever written.
    """
    db = get_db()
    if not db:
        return False
    try:
        run = _get_run(db, run_id)
        if not run or run.state != RunState.RUNNING:
            return False
        planned_total = run.progress_total or 0
        if planned_total == 0:
            # progress_total not set yet — the orchestrator publishes it once
            # fixture discovery completes (see orchestrator.run). Until then
            # we have no ground truth for "all tests accounted for", so we
            # must not nudge the run to COMPLETED on the basis of 0 visible
            # rows.
            return False
        non_terminal = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.status.in_(("pending", "waiting", "running")),
            )
            .count()
        )
        if non_terminal > 0:
            return False
        total_rows = (
            db.query(BenchmarkTestResult).filter(BenchmarkTestResult.run_id == run_id).count()
        )
        return total_rows >= planned_total
    finally:
        db.close()


# ---------------------------------------------------------------------------
# Reconciliation sweeper
# ---------------------------------------------------------------------------
#
# Periodic background loop that runs ``reconcile_waiting_tests`` against
# every active run. Catches UI/test-row drift caused by llm-server-side
# resumes (parent bubble-up after a followup answer) so users see fresh
# state without waiting for a manual refresh.
#
# Intentionally does NOT auto-expire waiting tests — "waiting for human"
# is a legitimate state, not a fault. A run stays alive as long as the
# user wants it to; the report email is sent when humans explicitly
# finish or when all tests organically terminate.


_SWEEPER_THREAD = None
_SWEEPER_STOP = threading.Event()


def _sweeper_loop():
    """Background loop: reconcile every active run's waiting tests.

    Interval configured by ``BENCHMARK_FOLLOWUP_SWEEP_SEC`` (default 60).
    Runs in a daemon thread started by ``start_followup_sweeper``.
    """
    try:
        interval = int(os.environ.get("BENCHMARK_FOLLOWUP_SWEEP_SEC", "60"))
    except (ValueError, TypeError):
        interval = 60
    if interval <= 0:
        return
    logger.info("Followup sweeper started (interval=%ds)", interval)
    while not _SWEEPER_STOP.is_set():
        try:
            # Reconcile every active run so UI catches up to llm-server state
            # without waiting for the foreground poll.
            db = get_db()
            active_ids = []
            if db:
                try:
                    active = (
                        db.query(BenchmarkRun).filter(BenchmarkRun.state == RunState.RUNNING).all()
                    )
                    active_ids = [r.run_id for r in active]
                finally:
                    db.close()
            for rid in active_ids:
                try:
                    reconcile_waiting_tests(rid)
                except Exception:
                    logger.exception("sweeper: reconcile failed for %s", rid)
        except Exception:
            logger.exception("sweeper: iteration crashed")
        # Responsive to stop signal without tight loop.
        _SWEEPER_STOP.wait(interval)


def start_followup_sweeper() -> None:
    """Start the background reconcile sweeper (idempotent)."""
    global _SWEEPER_THREAD
    if _SWEEPER_THREAD and _SWEEPER_THREAD.is_alive():
        return
    _SWEEPER_STOP.clear()
    _SWEEPER_THREAD = threading.Thread(
        target=_sweeper_loop, name="benchmark-followup-sweeper", daemon=True
    )
    _SWEEPER_THREAD.start()


def stop_followup_sweeper() -> None:
    """Stop the sweeper (used in tests and graceful shutdown)."""
    _SWEEPER_STOP.set()


def pause_run(run_id: str):
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        if run.state != RunState.RUNNING:
            raise ValueError(
                f"Cannot pause run {run_id}: state is '{run.state}' (must be 'running')"
            )
        run.state = RunState.PAUSED
        db.commit()
        logger.info("Paused benchmark run %s", run_id)
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to pause run %s", run_id)
    finally:
        db.close()


def resume_run(run_id: str):
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        if run.state != RunState.PAUSED:
            raise ValueError(
                f"Cannot resume run {run_id}: state is '{run.state}' (must be 'paused')"
            )
        run.state = RunState.RUNNING
        db.commit()
        logger.info("Resumed benchmark run %s", run_id)
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to resume run %s", run_id)
    finally:
        db.close()


def _mark_running_tests(db, run_id: str, new_status: str, error_msg: str = ""):
    """Mark all 'running' test results for a run as the given status.

    Called when a run is stopped, failed, or orphaned to ensure no tests
    stay stuck in 'running' state.
    """
    running_tests = (
        db.query(BenchmarkTestResult)
        .filter(
            BenchmarkTestResult.run_id == run_id,
            BenchmarkTestResult.status.in_(["running", "waiting"]),
        )
        .all()
    )
    for tr in running_tests:
        tr.status = new_status
        if error_msg:
            tr.error_message = error_msg
        logger.info(
            "Marked test %s (index %d) as %s for run %s",
            tr.test_id,
            tr.test_index,
            new_status,
            run_id,
        )


def stop_run(run_id: str):
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        if run.state not in (RunState.RUNNING, RunState.PAUSED):
            raise ValueError(
                f"Cannot stop run {run_id}: state is '{run.state}'"
                " (must be 'running' or 'paused')"
            )
        run.state = RunState.STOPPED
        run.phase = RunPhase.DONE
        run.completed_at = datetime.now()
        if run.started_at:
            run.duration_seconds = round((datetime.now() - run.started_at).total_seconds(), 2)
        # Mark any in-flight tests as stopped
        _mark_running_tests(db, run_id, "stopped", "Run stopped by user")
        db.commit()
        logger.info("Stopped benchmark run %s", run_id)
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to stop run %s", run_id)
    finally:
        db.close()


def fail_run(run_id: str, error: str = ""):
    """Mark a run as failed with an error message."""
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if not run:
            return
        run.state = RunState.FAILED
        run.phase = RunPhase.DONE
        run.completed_at = datetime.now()
        if run.started_at:
            run.duration_seconds = round((datetime.now() - run.started_at).total_seconds(), 2)
        if error:
            errors = list(run.errors or [])
            errors.append({"time": datetime.now().isoformat(), "message": error})
            run.errors = errors
        # Mark any in-flight tests as error (system failure, not test failure)
        _mark_running_tests(db, run_id, "error", error or "Run failed")
        # Assemble partial report from completed test results
        report = _assemble_report(db, run)
        if report:
            run.report_json = report
        db.commit()
        logger.info("Failed benchmark run %s: %s", run_id, error)
    except Exception:
        db.rollback()
        logger.exception("Failed to mark run %s as failed", run_id)
    finally:
        db.close()


def complete_run(run_id: str) -> bool:
    """Mark run as completed and assemble final report from test results.
    If any tests are still 'waiting' for followups, the run stays in RUNNING state
    instead of being marked completed — the report/email should not be sent yet.
    Returns True if the run was marked completed, False if tests are still waiting."""
    # Reconcile drifted test rows first — an llm-server-side internal resume
    # (parent bubble-up) may have flipped conversations to COMPLETED while the
    # test row still says ``waiting``. Without this we'd incorrectly conclude
    # the run is not ready to complete. Safe to call; idempotent.
    try:
        reconcile_waiting_tests(run_id, nudge_complete=False)
    except Exception:
        logger.exception("complete_run: reconciliation pass failed for %s", run_id)

    db = get_db()
    if not db:
        return False
    try:
        run = _get_run(db, run_id)
        if not run:
            return

        # Check for tests still waiting for followups
        waiting_count = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.status == "waiting",
            )
            .count()
        )
        if waiting_count > 0:
            logger.info(
                "Run %s has %d tests waiting for followups, not marking complete",
                run_id,
                waiting_count,
            )
            run.phase = RunPhase.RUNNING_TESTS
            db.commit()
            return False

        # Parallel-orchestrator guard: the executor queues tests beyond
        # ``parallel_workers`` and only inserts rows when a worker actually
        # starts a test. If complete_run fires during the brief window where
        # an earlier batch's rows are all terminal but queued tests have not
        # yet been dequeued, those queued tests would abort with no row and
        # the run would close at len(rows) < planned_total. Refuse to
        # complete unless the live row count has caught up.
        planned_total = run.progress_total or 0
        if planned_total > 0:
            total_rows = (
                db.query(BenchmarkTestResult).filter(BenchmarkTestResult.run_id == run_id).count()
            )
            if total_rows < planned_total:
                logger.info(
                    "Run %s has %d/%d test rows materialised; deferring completion",
                    run_id,
                    total_rows,
                    planned_total,
                )
                run.phase = RunPhase.RUNNING_TESTS
                db.commit()
                return False

        run.state = RunState.COMPLETED
        run.phase = RunPhase.DONE
        run.completed_at = datetime.now()
        run.errors = []  # Clear any stale errors (e.g. from orphan recovery race)
        if run.started_at:
            run.duration_seconds = round((datetime.now() - run.started_at).total_seconds(), 2)

        # Assemble report from test results
        report = _assemble_report(db, run)
        run.report_json = report

        db.commit()
        logger.info("Completed run %s with assembled report", run_id)
        return True
    except Exception:
        db.rollback()
        logger.exception("Failed to complete run %s", run_id)
        return False
    finally:
        db.close()


def finalize_stuck_runs(stale_minutes: int = 5) -> int:
    """Finalize runs stuck in state=running where all tests have already
    reached a terminal state.

    Renamed from ``recover_orphaned_runs`` (which collided with the
    startup-recovery function of the same name at the top of this module
    — F811). The two have unrelated semantics: the startup version
    reconciles and quarantines genuinely-orphaned RUNNING/PAUSED runs;
    this one finalizes runs whose tests are all terminal but whose
    own state never got flipped to COMPLETED.

    Race we're fixing: when a followup completes via the controller's
    ``_run_followup_task`` (last in a benchmark), the controller intentionally
    skips ``finish_single_test`` (see comment at line ~1957 in
    agent_benchmark_controller.py) so it doesn't race with the orchestrator's
    main loop. But that main loop already returned — nobody finalizes. Result:
    state=running, all tests pass, ``completed_at`` NULL, ``report_json`` NULL.
    Reproduced in d994a999a361 and 312491b55ab9.

    Sweeper criteria for "stuck":
      - state=RUNNING
      - updated_at older than ``stale_minutes``
      - 0 tests in pending/waiting/running (everything is terminal)
      - progress_total > 0 (don't finalize a run that never started)

    For each match, calls ``complete_run`` which assembles the report and
    transitions the row to COMPLETED. Errors are logged and the loop
    continues so a single bad row doesn't block recovery of others.

    Returns the number of runs recovered. Safe to call from any read endpoint.
    """
    db = get_db()
    if not db:
        return 0
    try:
        # Use UTC to match how SQLAlchemy stores naive timestamps written by
        # other paths in this module (most call `datetime.now()` and the
        # benchmark server runs in UTC). A naive local-time `datetime.now()`
        # here would skew the staleness cutoff by the API server's TZ offset.
        cutoff = datetime.now(timezone.utc).replace(tzinfo=None) - timedelta(minutes=stale_minutes)

        # Quarantine tests whose subprocess clearly died: status=running but
        # updated_at older than the cutoff. Without this, a single retry
        # subprocess crash leaves the test row in `running` forever, the
        # parent run stays at state=running, and the orphan-run sweep below
        # can never recover it because the per-run filter excludes any run
        # with a non-terminal test.
        stuck_tests = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.status == "running",
                BenchmarkTestResult.updated_at < cutoff,
            )
            .all()
        )
        for tr in stuck_tests:
            tr.status = "error"
            tr.error_message = (
                f"[orphan-recovery] test was in 'running' state with no updates "
                f"for >{stale_minutes} minutes — subprocess presumed dead"
            )
            tr.error_category = "subprocess_crashed"
        if stuck_tests:
            db.commit()
            logger.info(
                "finalize_stuck_runs: quarantined %d stuck-running test rows",
                len(stuck_tests),
            )

        candidates = (
            db.query(BenchmarkRun)
            .filter(
                BenchmarkRun.state == RunState.RUNNING,
                BenchmarkRun.updated_at < cutoff,
                BenchmarkRun.progress_total > 0,
            )
            .all()
        )
        if not candidates:
            return 0

        candidate_ids = [r.run_id for r in candidates]
        # Per-run check: any test still in pending/waiting/running disqualifies
        non_terminal_counts = dict(
            db.query(
                BenchmarkTestResult.run_id,
                func.count(BenchmarkTestResult.id),
            )
            .filter(
                BenchmarkTestResult.run_id.in_(candidate_ids),
                BenchmarkTestResult.status.in_(("pending", "waiting", "running")),
            )
            .group_by(BenchmarkTestResult.run_id)
            .all()
        )
        recoverable = [r.run_id for r in candidates if non_terminal_counts.get(r.run_id, 0) == 0]
    except Exception:
        logger.exception("finalize_stuck_runs: failed to query candidates")
        return 0
    finally:
        db.close()

    recovered = 0
    for run_id in recoverable:
        try:
            if complete_run(run_id):
                logger.info("recovered orphaned run %s (all tests terminal)", run_id)
                recovered += 1
        except Exception:
            logger.exception("finalize_stuck_runs: complete_run failed for %s", run_id)
    return recovered


def _validate_rerunnable(run, allow_completed=False):
    """Check that a run is in a terminal state and can be rerun.

    Args:
        allow_completed: If True, completed runs can be rerun (fresh rerun).
                         If False, only failed/stopped runs are allowed (resume).
    """
    if not run:
        raise ValueError("Run not found")
    allowed = {RunState.FAILED, RunState.STOPPED}
    if allow_completed:
        allowed.add(RunState.COMPLETED)
    if run.state not in allowed:
        if run.state == RunState.COMPLETED and not allow_completed:
            raise ValueError(
                f"Run {run.run_id} is already completed. "
                "Use Rerun for a fresh start instead of Resume."
            )
        raise ValueError(
            f"Cannot rerun run {run.run_id} in state '{run.state}'. "
            f"Only {', '.join(s.value for s in sorted(allowed, key=lambda x: x.value))} runs can be rerun."
        )


def _run_config(run: BenchmarkRun) -> dict:
    return {
        "run_id": run.run_id,
        "agent": run.agent,
        "user_id": run.user_id,
        "account_id": run.account_id,
        "tenant_id": run.tenant_id,
        "tool_config": run.tool_config,
        "max_tests": run.max_tests,
        "test_indices": run.test_indices,
        "test_filter": run.test_filter,
        "tag_filter": run.tag_filter,
        "parallel_workers": run.parallel_workers,
        "run_name": run.run_name,
        "cc_emails": run.cc_emails,
    }


def rerun_run(run_id: str) -> dict:
    """Fresh rerun — delete old test results and start from scratch."""
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        _validate_rerunnable(run, allow_completed=True)

        # Clear old test results
        db.query(BenchmarkTestResult).filter(BenchmarkTestResult.run_id == run_id).delete()

        # Reset run state
        run.state = RunState.RUNNING
        run.phase = RunPhase.INITIALIZING
        run.progress_current = 0
        run.progress_total = 0
        run.current_query = ""
        run.started_at = datetime.now()
        run.completed_at = None
        run.duration_seconds = 0.0
        run.errors = []
        run.report_json = None

        db.commit()
        logger.info("Reset run %s for fresh rerun", run_id)
        return _run_config(run)
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to reset run %s for rerun", run_id)
        raise
    finally:
        db.close()


def rerun_tests(run_id: str, test_indices: list[int]) -> dict:
    """Rerun specific test cases within an already-completed benchmark run.

    Keeps all other results intact. Resets the requested test rows to
    'running' state, then returns config with the original run params
    scoped to only the requested indices.

    Args:
        run_id: Existing benchmark run ID.
        test_indices: List of test_index values to rerun.

    Returns:
        Run config dict with ``rerun_indices`` key set.

    Raises:
        ValueError: If run not found, not in a terminal state, or
            requested indices don't exist in the run.
    """
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        _validate_rerunnable(run, allow_completed=True)

        if not test_indices:
            raise ValueError("No test indices provided")

        # Validate all requested indices exist in this run
        existing = (
            db.query(BenchmarkTestResult.test_index)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.test_index.in_(test_indices),
            )
            .all()
        )
        found = {r.test_index for r in existing}
        missing = set(test_indices) - found
        if missing:
            raise ValueError(f"Test indices {sorted(missing)} not found in run {run_id}")

        # Reset requested test rows so store_test_result can overwrite them
        db.query(BenchmarkTestResult).filter(
            BenchmarkTestResult.run_id == run_id,
            BenchmarkTestResult.test_index.in_(test_indices),
        ).update(
            {
                BenchmarkTestResult.status: "running",
                BenchmarkTestResult.actual_answer: "",
                BenchmarkTestResult.answer_similarity: 0.0,
                BenchmarkTestResult.answer_relevancy: 0.0,
                BenchmarkTestResult.planner_relevancy: 0.0,
                BenchmarkTestResult.duration_seconds: 0.0,
                BenchmarkTestResult.cost: 0.0,
                BenchmarkTestResult.total_tokens: 0,
                BenchmarkTestResult.input_tokens: 0,
                BenchmarkTestResult.output_tokens: 0,
                BenchmarkTestResult.cache_read_tokens: 0,
                BenchmarkTestResult.tool_calls_total: 0,
                BenchmarkTestResult.tool_calls_successful: 0,
                BenchmarkTestResult.tool_names: [],
                BenchmarkTestResult.model_names: [],
                BenchmarkTestResult.model_providers: [],
                BenchmarkTestResult.error_message: "",
                BenchmarkTestResult.error_category: "",
            },
            synchronize_session="fetch",
        )

        # Set run back to RUNNING
        run.state = RunState.RUNNING
        run.phase = RunPhase.RUNNING_TESTS
        run.current_query = ""
        run.completed_at = None
        run.report_json = None

        db.commit()
        logger.info(
            "Rerunning %d test(s) [%s] in run %s",
            len(test_indices),
            ",".join(str(i) for i in sorted(test_indices)),
            run_id,
        )

        config = _run_config(run)
        config["rerun_indices"] = sorted(test_indices)
        return config
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to setup rerun-tests for run %s", run_id)
        raise
    finally:
        db.close()


def restart_run(run_id: str) -> dict:
    """Resume a failed/stopped/completed run — keep existing results, return config with completed indices to skip."""
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        _validate_rerunnable(run, allow_completed=True)

        # Skip only passed tests — failed ones will be retried
        completed = (
            db.query(BenchmarkTestResult.test_index)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.status == "pass",
            )
            .all()
        )
        completed_indices = {r.test_index for r in completed}

        # Reset run state but keep test results
        run.state = RunState.RUNNING
        run.phase = RunPhase.INITIALIZING
        run.current_query = ""
        run.completed_at = None
        run.report_json = None
        # Keep progress_current as-is (reflects completed count)
        # Append resume note to errors
        errors = list(run.errors or [])
        errors.append(
            {
                "time": datetime.now().isoformat(),
                "message": f"Resumed — skipping {len(completed_indices)} completed test(s)",
            }
        )
        run.errors = errors

        db.commit()
        logger.info(
            "Resumed run %s, skipping %d completed tests",
            run_id,
            len(completed_indices),
        )

        config = _run_config(run)
        config["completed_indices"] = sorted(completed_indices)
        return config
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to resume run %s", run_id)
        raise
    finally:
        db.close()


def get_run_status(run_id: str) -> dict:
    db = get_db()
    if not db:
        return {"error": "Database not configured"}
    try:
        run = _get_run(db, run_id)
        if not run:
            return {"error": f"Run {run_id} not found"}
        return _run_to_status(run)
    finally:
        db.close()


def get_report(run_id: str) -> dict:
    """Retrieve the assembled benchmark report from the database."""
    db = get_db()
    if not db:
        return {}
    try:
        run = _get_run(db, run_id)
        if not run:
            return {}
        # If report_json exists, return it
        if run.report_json:
            report = run.report_json
        else:
            # Otherwise assemble from test results (partial report)
            report = _assemble_report(db, run)
        # Always inject live user email (not cached in report_json)
        if report and "metadata" in report:
            report["metadata"]["triggered_by"] = _get_user_email(run.user_id)
        # Backfill fields for reports cached before these changes
        if report:
            summary = report.get("summary", {})
            latency = summary.get("latency", {})
            if "max_seconds" not in latency and "details" in report:
                durations = [d.get("duration_seconds", 0) for d in report["details"]]
                latency["max_seconds"] = round(max(durations), 2) if durations else 0.0
            # Recompute overall_accuracy with current formula: (sim + rel) / 2
            sim = summary.get("answer_similarity", 0)
            rel = summary.get("answer_relevancy", 0)
            summary["overall_accuracy"] = round((sim + rel) / 2, 2)
            # Recompute per-tag overall_accuracy
            for tag_data in report.get("by_tag", {}).values():
                t_sim = tag_data.get("answer_similarity", 0)
                t_rel = tag_data.get("answer_relevancy", 0)
                tag_data["overall_accuracy"] = round((t_sim + t_rel) / 2, 2)
        return report
    finally:
        db.close()


_user_email_cache = {}
_name_cache = {}  # {("tenant", id): name, ("account", id): name}


def _resolve_name_cached(entity_type: str, entity_id: str) -> str:
    """Resolve tenant/account ID to name with caching."""
    if not entity_id:
        return ""
    key = (entity_type, entity_id)
    if key in _name_cache:
        return _name_cache[key]
    try:
        from benchmark_server.utils.db_utils import db_engine
        from sqlalchemy import text

        if not db_engine:
            return ""
        table = "tenant" if entity_type == "tenant" else "cloud_accounts"
        col = "name" if entity_type == "tenant" else "account_name"
        with db_engine.connect() as conn:
            row = conn.execute(
                text(f"SELECT {col} FROM {table} WHERE id = :id"), {"id": entity_id}
            ).fetchone()
            name = str(row[0]) if row else ""
    except Exception:
        name = ""
    _name_cache[key] = name
    return name


def _get_user_email(user_id):
    """Resolve user_id to email from DB (cached in-memory)."""
    if not user_id:
        return ""
    if user_id in _user_email_cache:
        return _user_email_cache[user_id]
    from benchmark_server.utils.db_utils import get_user_email

    email = get_user_email(user_id) or ""
    _user_email_cache[user_id] = email
    return email


def _run_to_status(run: BenchmarkRun) -> dict:
    # Compute live duration for active runs, stored duration for terminal runs
    if run.duration_seconds and run.duration_seconds > 0:
        duration = run.duration_seconds
    elif run.started_at and run.state in (RunState.RUNNING, RunState.PAUSED):
        duration = round((datetime.now() - run.started_at).total_seconds(), 2)
    else:
        duration = 0

    # ``total`` must be the planned test count, not the live row count.
    # ``add_test_result_progressive`` inserts a row only when the
    # orchestrator first records a result for a given test_index, so
    # ``len(test_results)`` grows during the run and the user sees the
    # denominator change ("4/5", then "4/6", then "5/6" — random-looking
    # to anyone watching). ``progress_total`` is set once at run creation
    # to ``len(filtered test_cases)`` and never moves, so it's the
    # correct denominator. Fall back to ``len(results)`` only if the
    # column is missing (legacy runs created before the column existed).
    results = run.test_results or []
    total = run.progress_total if (run.progress_total or 0) > 0 else len(results)
    # ``done`` includes any terminal status — including skipped — so the
    # progress bar reaches 100% on a completed run that had filtered-out
    # tests. (Without ``skipped`` here, the bar stalls at <100%
    # indefinitely on every run that uses index/tag filters.)
    done = sum(1 for r in results if r.status in ("pass", "fail", "error", "stopped", "skipped"))

    return {
        "run_id": run.run_id,
        "state": run.state or "unknown",
        "phase": run.phase or "unknown",
        "agent": run.agent or "",
        "run_name": run.run_name or "",
        "progress": f"{done}/{total}",
        "current_query": run.current_query or "",
        "created_at": run.created_at.isoformat() if run.created_at else "",
        "started_at": run.started_at.isoformat() if run.started_at else "",
        "completed_at": run.completed_at.isoformat() if run.completed_at else "",
        "duration_seconds": duration,
        "errors": run.errors or [],
        "user_id": run.user_id or "",
        "account_id": run.account_id or "",
        "tenant_id": run.tenant_id or "",
        "model_names": _get_model_info(run, "model_names"),
        "model_providers": _get_model_info(run, "model_providers"),
        "tool_config": getattr(run, "tool_config", None) or "",
        "cc_emails": run.cc_emails or [],
        "user_email": _get_user_email(run.user_id),
        "tag_filter": run.tag_filter or "",
        "test_indices": run.test_indices or "",
        "max_tests": run.max_tests or 0,
        "test_results_summary": _get_test_summary(run),
        "test_results": _get_test_results_list(run),
    }


def _get_model_info(run: BenchmarkRun, field: str) -> list:
    """Aggregate model_names or model_providers from test results."""
    results = run.test_results or []
    if not results:
        return []
    values = set()
    for r in results:
        val = getattr(r, field, None)
        if isinstance(val, list):
            values.update(v.strip() for v in val if v and v.strip())
        elif isinstance(val, str) and val:
            # Backward compat: old rows may have comma-separated strings
            values.update(v.strip() for v in val.split(",") if v.strip())
    return sorted(values)


def _get_test_summary(run: BenchmarkRun) -> dict:
    """Compute test result counts from the relationship."""
    results = run.test_results or []
    if not results:
        return {}
    # Infra errors shouldn't count against accuracy
    INFRA_ERROR_CATEGORIES = {
        "setup_failed",
        "setup_timeout",
        "setup_permission",
        "setup_env_missing",
        "infra_timeout",
        "worker_timeout",
    }
    total = len(results)
    success = sum(1 for r in results if r.status == "pass")
    skipped = sum(1 for r in results if r.status == "skipped")
    infra_errors = sum(
        1
        for r in results
        if r.status == "error" and (r.error_category or "") in INFRA_ERROR_CATEGORIES
    )
    failed = sum(1 for r in results if r.status in ("fail", "error")) - infra_errors
    excluded = skipped + infra_errors
    scorable = total - excluded
    # Average scores across scorable tests (exclude skipped and infra errors)
    scorable_results = [
        r
        for r in results
        if r.status not in ("skipped",)
        and not (r.status == "error" and (r.error_category or "") in INFRA_ERROR_CATEGORIES)
    ]
    avg_similarity = (
        round(
            sum(r.answer_similarity or 0 for r in scorable_results) / len(scorable_results),
            2,
        )
        if scorable_results
        else 0
    )
    avg_relevancy = (
        round(
            sum(r.answer_relevancy or 0 for r in scorable_results) / len(scorable_results),
            2,
        )
        if scorable_results
        else 0
    )
    avg_planner = (
        round(
            sum(r.planner_relevancy or 0 for r in scorable_results) / len(scorable_results),
            2,
        )
        if scorable_results
        else 0
    )
    overall_accuracy = round((avg_similarity + avg_relevancy) / 2, 2) if scorable_results else 0
    return {
        "total": total,
        "success": success,
        "failed": failed,
        "skipped": skipped,
        "infra_errors": infra_errors,
        "success_rate": round(success / scorable * 100, 1) if scorable else 0,
        "overall_accuracy": overall_accuracy,
        "avg_similarity": avg_similarity,
        "avg_relevancy": avg_relevancy,
        "avg_planner": avg_planner,
    }


def _get_test_results_list(run: BenchmarkRun) -> list:
    """Return per-test result rows for the status endpoint."""
    results = run.test_results or []
    if not results:
        return []
    rows = []
    for r in results:
        # ISO-8601 timestamp of last row update. Used by the UI to render a
        # "waiting 3h" age label next to waiting badges so users can spot
        # abandoned followups at a glance. Falls back to created_at on DBs
        # where the updated_at migration hasn't applied yet.
        #
        # CRITICAL: Postgres stores the value as naive ``TIMESTAMP WITHOUT
        # TIME ZONE`` whose contents are UTC (from ``func.now()`` with the
        # session at UTC). A plain ``isoformat()`` on that naive datetime
        # produces a string with no TZ marker, and browsers parse such
        # strings as *local* time — shifting the rendered age by the host
        # TZ offset (e.g. 5h30m off on IST hosts). Tag the value as UTC
        # before emitting so the browser sees the correct absolute instant.
        last_ts = getattr(r, "updated_at", None) or getattr(r, "created_at", None)
        last_ts_iso = last_ts.replace(tzinfo=timezone.utc).isoformat() if last_ts else None
        rows.append(
            {
                "test_id": r.test_id,
                "test_index": r.test_index,
                "status": r.status,
                "query": (r.query or "")[:200],
                "answer_similarity": r.answer_similarity or 0.0,
                "answer_relevancy": r.answer_relevancy or 0.0,
                "planner_relevancy": r.planner_relevancy or 0.0,
                "score_reason": r.score_reason or "",
                "duration_seconds": r.duration_seconds or 0.0,
                "cost": r.cost or 0.0,
                "total_tokens": r.total_tokens or 0,
                "tool_calls_total": r.tool_calls_total or 0,
                "model_names": r.model_names or [],
                "error_message": (r.error_message or "")[:200],
                "followup_request": (r.followup_request if r.status == "waiting" else None),
                "conversation_id": r.conversation_id or "",
                # session_id from the LLM server. UI displays this as the
                # primary debug handle because it's what token-usage/metrics
                # APIs key on. ``conversation_id`` is still exposed above
                # for log-grep/debug workflows that want the DB UUID.
                "session_id": r.polling_conversation_id or "",
                "updated_at": last_ts_iso,
                # Truncated to keep the polled status payload small. Full
                # text is available via the report endpoint; this preview
                # is enough to eyeball the mismatch inside the live
                # dropdown without bloating every poll.
                "expected_answer": (r.expected_answer or "")[:4000],
                "actual_answer": (r.actual_answer or "")[:4000],
            }
        )
    return rows


# ---------------------------------------------------------------------------
# Test result storage (called per-test from benchmark runner)
# ---------------------------------------------------------------------------


def store_test_result(run_id: str, result: dict):
    """Store or update a single test result (upsert by run_id + test_index)."""
    if not run_id:
        return
    db = get_db()
    if not db:
        return
    try:
        test_index = result.get("test_index", 0)

        # Check for existing row (from a previous run attempt)
        tr = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.test_index == test_index,
            )
            .first()
        )

        if tr:
            # Update existing row
            new_status = result.get("status", tr.status)
            tr.test_id = result.get("test_id", tr.test_id)
            tr.status = new_status
            tr.conversation_id = result.get("conversation_id", tr.conversation_id)
            tr.polling_conversation_id = result.get(
                "polling_conversation_id", tr.polling_conversation_id
            )
            tr.query = result.get("query", tr.query)
            tr.expected_answer = result.get("expected_answer", tr.expected_answer)
            tr.actual_answer = result.get("actual_answer", tr.actual_answer)
            tr.answer_similarity = result.get("answer_similarity", tr.answer_similarity)
            tr.answer_relevancy = result.get("answer_relevancy", tr.answer_relevancy)
            tr.planner_relevancy = result.get("planner_relevancy", tr.planner_relevancy)
            tr.score_reason = result.get("score_reason", tr.score_reason)
            tr.execution_trace = result.get("execution_trace", tr.execution_trace)
            tr.duration_seconds = result.get("duration_seconds", tr.duration_seconds)
            tr.setup_duration = result.get("setup_duration", tr.setup_duration)
            tr.llm_duration = result.get("llm_duration", tr.llm_duration)
            tr.teardown_duration = result.get("teardown_duration", tr.teardown_duration)
            tr.cost = result.get("cost", tr.cost)
            tr.total_tokens = result.get("total_tokens", tr.total_tokens)
            tr.input_tokens = result.get("input_tokens", tr.input_tokens)
            tr.output_tokens = result.get("output_tokens", tr.output_tokens)
            tr.cache_read_tokens = result.get("cache_read_tokens", tr.cache_read_tokens)
            tr.tool_calls_total = result.get("tool_calls_total", tr.tool_calls_total)
            tr.tool_calls_successful = result.get("tool_calls_successful", tr.tool_calls_successful)
            tr.tool_names = result.get("tool_names", tr.tool_names)
            tr.model_names = result.get("model_names", tr.model_names)
            tr.model_providers = result.get("model_providers", tr.model_providers)
            tr.tags = result.get("tags", tr.tags)
            tr.followup_request = result.get("followup_request", tr.followup_request)

            # When re-running a test (status -> running), clear stale errors and
            # scores from the previous attempt so they don't leak into the new run
            if new_status == "running":
                tr.error_message = ""
                tr.error_category = ""
                tr.actual_answer = ""
                tr.followup_request = None
                tr.answer_similarity = 0.0
                tr.answer_relevancy = 0.0
                tr.planner_relevancy = 0.0
                tr.duration_seconds = 0.0
                tr.setup_duration = 0.0
                tr.llm_duration = 0.0
                tr.teardown_duration = 0.0
                tr.cost = 0.0
                tr.total_tokens = 0
                tr.input_tokens = 0
                tr.output_tokens = 0
                tr.cache_read_tokens = 0
                tr.tool_calls_total = 0
                tr.tool_calls_successful = 0
                tr.tool_names = []
                tr.model_names = []
                tr.model_providers = []
            else:
                tr.error_message = result.get("error_message", tr.error_message)
                tr.error_category = result.get("error_category", tr.error_category)
        else:
            # Insert new row
            tr = BenchmarkTestResult(
                run_id=run_id,
                test_id=result.get("test_id", ""),
                test_index=test_index,
                status=result.get("status", "pending"),
                conversation_id=result.get("conversation_id", ""),
                polling_conversation_id=result.get("polling_conversation_id", ""),
                query=result.get("query", ""),
                expected_answer=result.get("expected_answer", ""),
                actual_answer=result.get("actual_answer", ""),
                answer_similarity=result.get("answer_similarity", 0.0),
                answer_relevancy=result.get("answer_relevancy", 0.0),
                planner_relevancy=result.get("planner_relevancy", 0.0),
                score_reason=result.get("score_reason", ""),
                execution_trace=result.get("execution_trace", ""),
                duration_seconds=result.get("duration_seconds", 0.0),
                setup_duration=result.get("setup_duration", 0.0),
                llm_duration=result.get("llm_duration", 0.0),
                teardown_duration=result.get("teardown_duration", 0.0),
                cost=result.get("cost", 0.0),
                total_tokens=result.get("total_tokens", 0),
                input_tokens=result.get("input_tokens", 0),
                output_tokens=result.get("output_tokens", 0),
                cache_read_tokens=result.get("cache_read_tokens", 0),
                tool_calls_total=result.get("tool_calls_total", 0),
                tool_calls_successful=result.get("tool_calls_successful", 0),
                tool_names=result.get("tool_names", []),
                model_names=result.get("model_names", []),
                model_providers=result.get("model_providers", []),
                tags=result.get("tags", []),
                error_message=result.get("error_message", ""),
                error_category=result.get("error_category", ""),
                followup_request=result.get("followup_request"),
            )
            db.add(tr)
        db.commit()
    except Exception:
        db.rollback()
        logger.exception("Failed to store test result for run %s", run_id)
    finally:
        db.close()


def get_test_followup_data(run_id: str, test_index: int) -> dict:
    """Get followup data for a waiting test. Raises ValueError if invalid."""
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        tr = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.test_index == test_index,
            )
            .first()
        )
        if not tr:
            raise ValueError(f"Test {test_index} not found in run {run_id}")
        if tr.status != "waiting":
            raise ValueError(f"Test {test_index} is not waiting (status: {tr.status})")
        if not tr.followup_request or not tr.conversation_id:
            raise ValueError(f"Test {test_index} has no followup data")

        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        config = _run_config(run)
        return {
            "conversation_id": tr.conversation_id,
            "followup_request": tr.followup_request,
            "test_id": tr.test_id,
            "query": tr.query or "",
            "expected_answer": tr.expected_answer or "",
            "config": config,
        }
    finally:
        db.close()


def update_test_result_scores(
    run_id: str,
    test_index: int,
    answer_similarity: float = None,
    answer_relevancy: float = None,
    planner_relevancy: float = None,
    score_reason: str = None,
    execution_trace: str = None,
    **kwargs,
):
    """Update RAGAS scores and optional token/model metrics for a test result.

    Only updates fields that are explicitly provided (non-None).
    Extra kwargs can include: cost, total_tokens, input_tokens, output_tokens,
    cache_read_tokens, tool_calls_total, tool_calls_successful, model_names,
    model_providers.
    """
    if not run_id:
        return
    db = get_db()
    if not db:
        return
    try:
        tr = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.test_index == test_index,
            )
            .first()
        )
        if tr:
            if answer_similarity is not None:
                tr.answer_similarity = float(answer_similarity)
            if answer_relevancy is not None:
                tr.answer_relevancy = float(answer_relevancy)
            if planner_relevancy is not None:
                tr.planner_relevancy = float(planner_relevancy)
            if score_reason is not None:
                tr.score_reason = score_reason
            if execution_trace is not None:
                tr.execution_trace = execution_trace

            # Token and model metrics from re-evaluation
            _TOKEN_FIELDS = {
                "cost": float,
                "total_tokens": int,
                "input_tokens": int,
                "output_tokens": int,
                "cache_read_tokens": int,
                "tool_calls_total": int,
                "tool_calls_successful": int,
            }
            for field, cast_fn in _TOKEN_FIELDS.items():
                val = kwargs.get(field)
                if val is not None:
                    setattr(tr, field, cast_fn(val))

            # JSONB list fields
            for field in ("model_names", "model_providers"):
                val = kwargs.get(field)
                if val is not None:
                    setattr(tr, field, val)

            db.commit()
        else:
            logger.warning(
                "No test result found for run %s index %d — skipping score update",
                run_id,
                test_index,
            )
    except Exception:
        db.rollback()
        logger.exception("Failed to update scores for run %s index %d", run_id, test_index)
    finally:
        db.close()


# ---------------------------------------------------------------------------
# Report assembly
# ---------------------------------------------------------------------------


def _assemble_report(db, run: BenchmarkRun) -> dict:
    """Assemble the final report JSON from test results in the DB."""
    results = (
        db.query(BenchmarkTestResult)
        .filter(BenchmarkTestResult.run_id == run.run_id)
        .order_by(BenchmarkTestResult.test_index)
        .all()
    )

    if not results:
        return {}

    details = []
    total_tokens = 0
    tag_buckets = {}
    all_model_names = set()
    all_model_providers = set()

    for r in results:
        detail = {
            "test_id": r.test_id,
            "test_index": r.test_index,
            "status": r.status,
            "query": r.query or "",
            "expected_answer": r.expected_answer or "",
            "actual_answer": r.actual_answer or "",
            "answer_similarity": r.answer_similarity or 0.0,
            "answer_relevancy": r.answer_relevancy or 0.0,
            "planner_relevancy": r.planner_relevancy or 0.0,
            "score_reason": r.score_reason or "",
            "execution_trace": r.execution_trace or "",
            "duration_seconds": r.duration_seconds or 0.0,
            "cost": r.cost or 0.0,
            "total_tokens": r.total_tokens or 0,
            "input_tokens": r.input_tokens or 0,
            "output_tokens": r.output_tokens or 0,
            "cache_read_tokens": r.cache_read_tokens or 0,
            "tool_calls_total": r.tool_calls_total or 0,
            "tool_calls_successful": r.tool_calls_successful or 0,
            "tool_names": r.tool_names or [],
            "model_names": r.model_names or [],
            "model_providers": r.model_providers or [],
            "tags": r.tags or [],
            "error_message": r.error_message or "",
            "error_category": r.error_category or "",
        }
        details.append(detail)

        total_tokens += r.total_tokens or 0
        for name in r.model_names or []:
            if name and name.strip():
                all_model_names.add(name.strip())
        for prov in r.model_providers or []:
            if prov and prov.strip():
                all_model_providers.add(prov.strip())

        # Tag aggregation (only scorable tests)
        _INFRA_CATS = {
            "setup_failed",
            "setup_timeout",
            "setup_permission",
            "setup_env_missing",
            "infra_timeout",
            "worker_timeout",
        }
        is_scorable = r.status != "skipped" and not (
            r.status == "error" and (r.error_category or "") in _INFRA_CATS
        )
        if is_scorable:
            for tag in r.tags or []:
                if tag not in tag_buckets:
                    tag_buckets[tag] = []
                tag_buckets[tag].append(detail)

    # Exclude infra errors and skipped from scoring
    INFRA_ERROR_CATEGORIES = {
        "setup_failed",
        "setup_timeout",
        "setup_permission",
        "setup_env_missing",
        "infra_timeout",
        "worker_timeout",
    }
    scorable_results = [
        r
        for r in results
        if r.status != "skipped"
        and not (r.status == "error" and (r.error_category or "") in INFRA_ERROR_CATEGORIES)
    ]
    n = len(scorable_results) or 1  # avoid division by zero

    sorted_durations = sorted(r.duration_seconds or 0.0 for r in scorable_results)

    def percentile(data, p):
        if not data:
            return 0.0
        k = (len(data) - 1) * p / 100.0
        f = int(k)
        c = f + 1
        if c >= len(data):
            return data[-1]
        return data[f] + (k - f) * (data[c] - data[f])

    avg_sim = round(sum(r.answer_similarity or 0 for r in scorable_results) / n, 2)
    avg_rel = round(sum(r.answer_relevancy or 0 for r in scorable_results) / n, 2)
    avg_planner = round(sum(r.planner_relevancy or 0 for r in scorable_results) / n, 2)
    overall_acc = round((avg_sim + avg_rel) / 2, 2)

    avg_cache = 0.0
    for r in scorable_results:
        inp = r.input_tokens or 0
        cached = r.cache_read_tokens or 0
        if inp > 0:
            avg_cache += cached / inp * 100
    avg_cache = round(avg_cache / n, 2) if n else 0.0

    summary = {
        "overall_accuracy": overall_acc,
        "answer_similarity": avg_sim,
        "answer_relevancy": avg_rel,
        "planner_relevancy": avg_planner,
        "latency": {
            "total_seconds": round(sum(r.duration_seconds or 0 for r in scorable_results), 2),
            "avg_seconds": round(sum(r.duration_seconds or 0 for r in scorable_results) / n, 2),
            "p50_seconds": round(percentile(sorted_durations, 50), 2),
            "p95_seconds": round(percentile(sorted_durations, 95), 2),
            "p99_seconds": round(percentile(sorted_durations, 99), 2),
            "max_seconds": round(max(sorted_durations), 2) if sorted_durations else 0.0,
            # LLM-only latency (excludes setup/teardown)
            "llm_avg_seconds": round(sum(r.llm_duration or 0 for r in scorable_results) / n, 2),
            "llm_p50_seconds": round(
                percentile(sorted(r.llm_duration or 0 for r in scorable_results), 50), 2
            ),
            "llm_p95_seconds": round(
                percentile(sorted(r.llm_duration or 0 for r in scorable_results), 95), 2
            ),
            "llm_max_seconds": round(
                max((r.llm_duration or 0 for r in scorable_results), default=0.0), 2
            ),
            # Setup overhead
            "setup_avg_seconds": round(sum(r.setup_duration or 0 for r in scorable_results) / n, 2),
        },
        "tool_calls": {
            "total": sum(r.tool_calls_total or 0 for r in scorable_results),
            "successful": sum(r.tool_calls_successful or 0 for r in scorable_results),
            "avg_per_query": round(sum(r.tool_calls_total or 0 for r in scorable_results) / n, 1),
            "success_rate": (
                round(
                    sum(r.tool_calls_successful or 0 for r in scorable_results)
                    / sum(r.tool_calls_total or 0 for r in scorable_results)
                    * 100,
                    1,
                )
                if sum(r.tool_calls_total or 0 for r in scorable_results)
                else 0.0
            ),
        },
        "cost": {
            "total_usd": round(sum(r.cost or 0 for r in scorable_results), 6),
            "avg_per_query_usd": round(sum(r.cost or 0 for r in scorable_results) / n, 6),
            "accuracy_per_dollar": (
                round(overall_acc / sum(r.cost or 0 for r in scorable_results), 2)
                if sum(r.cost or 0 for r in scorable_results) > 0
                else 0.0
            ),
        },
        "tokens": {
            "total": total_tokens,
            "avg_cache_hit_rate": avg_cache,
        },
    }

    # By-tag breakdown
    by_tag = {}
    for tag, tag_details in tag_buckets.items():
        tc = len(tag_details)
        tag_sim = round(sum(d["answer_similarity"] for d in tag_details) / tc, 2)
        tag_rel = round(sum(d["answer_relevancy"] for d in tag_details) / tc, 2)
        tag_planner = round(sum(d.get("planner_relevancy", 0) for d in tag_details) / tc, 2)
        by_tag[tag] = {
            "count": tc,
            "overall_accuracy": round((tag_sim + tag_rel) / 2, 2),
            "answer_similarity": tag_sim,
            "answer_relevancy": tag_rel,
            "planner_relevancy": tag_planner,
            "avg_duration_seconds": round(sum(d["duration_seconds"] for d in tag_details) / tc, 2),
            "avg_tool_calls": round(sum(d["tool_calls_total"] for d in tag_details) / tc, 1),
            "avg_cost_usd": round(sum(d["cost"] for d in tag_details) / tc, 6),
        }

    return {
        "metadata": {
            "run_id": run.run_id,
            "agent": run.agent,
            "run_name": run.run_name or "",
            "total_queries": n,
            "model_names": sorted(all_model_names),
            "model_providers": sorted(all_model_providers),
            "triggered_by": _get_user_email(run.user_id),
            "timestamp": (
                run.completed_at.isoformat() if run.completed_at else datetime.now().isoformat()
            ),
        },
        "summary": summary,
        "by_tag": by_tag,
        "details": details,
    }


# ---------------------------------------------------------------------------
# Phase transitions (used by runner and lifecycle)
# ---------------------------------------------------------------------------


def set_phase(run_id: str, phase: str):
    """Update the current lifecycle phase of a run."""
    if not run_id:
        return
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if not run:
            return
        run.phase = phase
        if phase == RunPhase.RUNNING_TESTS and not run.started_at:
            run.started_at = datetime.now()
        db.commit()
        logger.info("Run %s phase -> %s", run_id, phase)
    except Exception:
        db.rollback()
        logger.exception("Failed to set phase for run %s", run_id)
    finally:
        db.close()


def reserve_test_rows(run_id: str, test_cases: list) -> int:
    """Pre-insert ``pending`` rows for every planned test in the run.

    Why this exists: the standard ``create_run`` → orchestrator path inserts
    a ``BenchmarkTestResult`` row only when a worker thread actually starts
    a test (via ``execute_single_test`` → ``store_test_result``). With N
    parallel workers and >N tests, the overflow sits in the
    ``ThreadPoolExecutor`` queue without any DB row. During the brief window
    where the first batch's rows have just flipped to ``pass``/``fail`` but
    queued tests have not yet been dequeued, every "row count vs. planned
    total" predicate in this module sees an under-count and concludes the
    run is done — flipping it to COMPLETED/FAILED and causing
    ``should_proceed`` to return False for the queued workers when they
    finally start, which aborts those tests with no row ever written.

    Pre-inserting ``pending`` rows closes the gap: every gate
    (``_run_has_all_terminal_tests``, ``recover_orphaned_runs``'s
    per-run non-terminal probe, ``finish_single_test``'s pending count,
    ``complete_run``'s planned-total check) sees N rows from the moment the
    orchestrator commits its plan, so no transient under-count is possible.

    Idempotent: if a row already exists for a given ``test_index`` (e.g.
    rerun-tests path that reset specific rows to ``running``, or partial
    state from a crashed mid-run), it is left untouched.

    Args:
        run_id: Benchmark run identifier.
        test_cases: List of test-case dicts from
            ``orchestrator.discover_test_cases``. Each must carry
            ``__id__`` and ``__global_index__`` (set by the discoverer);
            ``user_prompt``, ``expected_output``, ``tags`` are also
            harvested if present.

    Returns:
        The number of rows actually inserted (0 if all rows already exist).
    """
    if not run_id or not test_cases:
        return 0
    db = get_db()
    if not db:
        return 0
    try:
        existing = {
            r.test_index
            for r in db.query(BenchmarkTestResult.test_index)
            .filter(BenchmarkTestResult.run_id == run_id)
            .all()
        }
        inserted = 0
        for tc in test_cases:
            global_index = tc.get("__global_index__")
            if global_index is None or global_index in existing:
                continue
            expected = tc.get("expected_output", "")
            if isinstance(expected, list):
                expected_str = "\n".join(str(e) for e in expected)
            else:
                expected_str = str(expected) if expected else ""
            tr = BenchmarkTestResult(
                run_id=run_id,
                test_id=tc.get("__id__", ""),
                test_index=global_index,
                status="pending",
                query=(tc.get("user_prompt") or "")[:200],
                expected_answer=expected_str,
                tags=tc.get("tags", []),
            )
            db.add(tr)
            inserted += 1
        if inserted:
            db.commit()
            logger.info(
                "Pre-inserted %d pending test row(s) for run %s",
                inserted,
                run_id,
            )
        return inserted
    except Exception:
        db.rollback()
        logger.exception("Failed to reserve test rows for run %s", run_id)
        return 0
    finally:
        db.close()


def add_error(run_id: str, error: str):
    """Append an error message to the run's error log."""
    if not run_id:
        return
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if not run:
            return
        errors = list(run.errors or [])
        errors.append({"time": datetime.now().isoformat(), "message": error})
        run.errors = errors
        db.commit()
    except Exception:
        db.rollback()
    finally:
        db.close()


# ---------------------------------------------------------------------------
# Pytest-side helpers (used inside benchmark test files)
# ---------------------------------------------------------------------------


def update_progress(run_id: str, current: int, total: int, query: str = ""):
    """Called from the benchmark loop to update progress."""
    if not run_id:
        return
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if not run:
            return
        run.progress_current = current
        run.progress_total = total
        run.current_query = query[:200]
        db.commit()
    except Exception:
        db.rollback()
    finally:
        db.close()


def should_proceed(
    run_id: str, poll_interval: float = 1.0, max_pause_seconds: float = 3600.0
) -> bool:
    """Check whether the benchmark should continue.

    - If paused, blocks until resumed or stopped (up to max_pause_seconds).
    - Returns False if stopped or pause timeout exceeded.
    - Returns True otherwise.
    - Safe to call with run_id=None (always returns True).
    """
    if not run_id:
        return True

    paused_since = None

    while True:
        db = get_db()
        if not db:
            return True
        try:
            run = _get_run(db, run_id)
            current = run.state if run else RunState.RUNNING
        finally:
            db.close()

        if current == RunState.STOPPED:
            return False
        if current in (RunState.FAILED, RunState.COMPLETED):
            return False
        if current == RunState.PAUSED:
            if paused_since is None:
                paused_since = time.time()
                logger.info("Benchmark %s is paused, waiting...", run_id)
            elif time.time() - paused_since > max_pause_seconds:
                logger.warning(
                    "Benchmark %s paused for >%ds, auto-stopping",
                    run_id,
                    int(max_pause_seconds),
                )
                stop_run(run_id)
                return False
            time.sleep(poll_interval)
            continue
        # State is RUNNING — reset pause timer and proceed
        paused_since = None
        return True


def get_test_results_for_eval(run_id: str) -> tuple:
    """Fetch test results with actual/expected answers for re-evaluation.

    Returns (account_id, list_of_dicts) with fields needed for RAGAS evaluation.
    Only returns 'pass' results (failed tests have no meaningful answer).
    """
    db = get_db()
    if not db:
        return "", "", "", []
    try:
        run = _get_run(db, run_id)
        account_id = run.account_id if run else ""
        tenant_id = run.tenant_id if run else ""
        user_id = run.user_id if run else ""

        results = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.status == "pass",
            )
            .order_by(BenchmarkTestResult.test_index)
            .all()
        )
        items = [
            {
                "test_id": r.test_id,
                "test_index": r.test_index,
                "conversation_id": r.conversation_id or "",
                "polling_conversation_id": getattr(r, "polling_conversation_id", "") or "",
                "query": r.query or "",
                "expected_answer": r.expected_answer or "",
                "actual_answer": r.actual_answer or "",
                "execution_trace": r.execution_trace or "",
                # ``created_at`` is the time the agent's answer was first
                # stored. Re-evaluation passes this to the rubric judge so
                # time-sensitive checks compare against when the answer was
                # actually produced, not when re-eval runs.
                "created_at": r.created_at,
            }
            for r in results
        ]
        return account_id, tenant_id, user_id, items
    except Exception:
        logger.exception("Failed to fetch test results for eval, run %s", run_id)
        return "", "", "", []
    finally:
        db.close()


def set_run_evaluating(run_id: str):
    """Set a completed/failed/stopped run to evaluating phase (for re-eval)."""
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        if run.state not in (RunState.COMPLETED, RunState.FAILED, RunState.STOPPED):
            raise ValueError(
                f"Cannot re-evaluate run {run_id} in state '{run.state}'. "
                "Only completed/failed/stopped runs can be re-evaluated."
            )
        run.phase = RunPhase.EVALUATING
        db.commit()
        logger.info("Run %s set to evaluating for re-eval", run_id)
        return _run_config(run)
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to set run %s to evaluating", run_id)
        raise
    finally:
        db.close()


def finish_re_evaluation(run_id: str):
    """Mark re-evaluation as done and reassemble the report."""
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if not run:
            return
        run.phase = RunPhase.DONE
        run.report_json = _assemble_report(db, run)
        db.commit()
        logger.info("Re-evaluation complete for run %s, report reassembled", run_id)
    except Exception:
        db.rollback()
        logger.exception("Failed to finish re-evaluation for run %s", run_id)
    finally:
        db.close()


# ---------------------------------------------------------------------------
# Gather mode: discover tests and create pending DB entries without running
# ---------------------------------------------------------------------------


def create_gathered_run(
    agent: str,
    test_cases: list,
    user_id: str = None,
    account_id: str = None,
    tenant_id: str = None,
    tool_config: str = None,
    max_tests: int = None,
    test_indices: str = None,
    test_filter: str = None,
    tag_filter: str = None,
    cc_emails: list = None,
) -> str:
    """Create a run in GATHERED state with pending test result rows.

    Args:
        test_cases: list of (file_path, test_id, global_index) tuples
            from fixtures.find_test_cases()
    """
    import yaml

    run_id = uuid.uuid4().hex[:12]
    db = get_db()
    if not db:
        logger.error("Cannot create gathered run: database not configured")
        return run_id
    try:
        run = BenchmarkRun(
            run_id=run_id,
            agent=agent,
            state=RunState.GATHERED,
            phase=RunPhase.GATHERED,
            user_id=user_id,
            account_id=account_id,
            tenant_id=tenant_id,
            tool_config=tool_config,
            max_tests=max_tests,
            test_indices=test_indices,
            test_filter=test_filter,
            tag_filter=tag_filter,
            cc_emails=cc_emails,
            progress_current=0,
            progress_total=len(test_cases),
            errors=[],
        )
        db.add(run)
        db.flush()

        for file_path, test_id, global_index in test_cases:
            # Load test_case.yaml to get query, expected_output, tags
            try:
                with open(file_path, "r") as f:
                    tc = yaml.safe_load(f) or {}
            except Exception:
                tc = {}

            expected = tc.get("expected_output", [])
            if isinstance(expected, str):
                expected = [expected]

            tr = BenchmarkTestResult(
                run_id=run_id,
                test_id=test_id,
                test_index=global_index,
                status="pending",
                query=tc.get("user_prompt", "")[:200] if tc.get("user_prompt") else "",
                expected_answer="\n".join(expected) if expected else "",
                tags=tc.get("tags", []),
            )
            db.add(tr)

        db.commit()
        logger.info(
            "Created gathered run %s for agent %s with %d tests",
            run_id,
            agent,
            len(test_cases),
        )
    except Exception:
        db.rollback()
        logger.exception("Failed to create gathered run %s", run_id)
    finally:
        db.close()
    return run_id


def start_gathered_run(run_id: str) -> dict:
    """Transition a gathered run to running state for full execution."""
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        if run.state != RunState.GATHERED:
            raise ValueError(
                f"Cannot start run {run_id}: state is '{run.state}' (must be 'gathered')"
            )
        # Clear pending test results — they'll be re-created during execution
        db.query(BenchmarkTestResult).filter(BenchmarkTestResult.run_id == run_id).delete()
        run.state = RunState.RUNNING
        run.phase = RunPhase.INITIALIZING
        run.started_at = datetime.now()
        run.progress_current = 0
        db.commit()
        logger.info("Started gathered run %s", run_id)
        return _run_config(run)
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to start gathered run %s", run_id)
        raise
    finally:
        db.close()


def set_test_running(run_id: str, test_index: int) -> dict:
    """Mark a single test as running.

    Works for gathered runs (pending tests) and old completed/failed/stopped
    runs (re-running an individual test).

    Returns run config dict for the caller to execute the test.
    """
    _ALLOWED = (
        RunState.GATHERED,
        RunState.RUNNING,
        RunState.COMPLETED,
        RunState.FAILED,
        RunState.STOPPED,
    )
    db = get_db()
    if not db:
        raise ValueError("Database not configured")
    try:
        run = _get_run(db, run_id)
        if not run:
            raise ValueError(f"Run {run_id} not found")
        if run.state not in _ALLOWED:
            raise ValueError(f"Cannot run test for run {run_id}: state is '{run.state}'")

        tr = (
            db.query(BenchmarkTestResult)
            .filter(
                BenchmarkTestResult.run_id == run_id,
                BenchmarkTestResult.test_index == test_index,
            )
            .with_for_update()
            .first()
        )
        if not tr:
            raise ValueError(f"Test index {test_index} not found in run {run_id}")
        if tr.status == "running":
            raise ValueError(f"Test {test_index} is already running in run {run_id}")

        # Remember the previous state so finish_single_test can restore it
        prev_state = run.state
        tr.status = "running"
        # Clear stale score/result fields so the dashboard doesn't render the
        # previous pass scores while the retry is in flight; if the subprocess
        # crashes before writing a new result, an empty/running row is the
        # honest signal.
        tr.answer_similarity = None
        tr.answer_relevancy = None
        tr.planner_relevancy = None
        tr.score_reason = None
        tr.actual_answer = None
        tr.error_message = None
        tr.error_category = None
        tr.duration_seconds = None
        tr.cost = None
        tr.total_tokens = None
        tr.input_tokens = None
        tr.output_tokens = None
        tr.cache_read_tokens = None
        tr.tool_calls_total = None
        tr.tool_calls_successful = None
        tr.tool_names = None
        tr.model_names = None
        tr.model_providers = None
        tr.execution_trace = None
        tr.followup_request = None
        tr.setup_duration = None
        tr.llm_duration = None
        tr.teardown_duration = None
        run.state = RunState.RUNNING
        run.phase = RunPhase.RUNNING_TESTS
        run.current_query = tr.query[:200] if tr.query else ""
        if not run.started_at:
            run.started_at = datetime.now()
        db.commit()
        config = _run_config(run)
        config["test_id"] = tr.test_id
        config["test_index"] = tr.test_index
        config["_prev_state"] = prev_state
        return config
    except ValueError:
        raise
    except Exception:
        db.rollback()
        logger.exception("Failed to set test running for run %s", run_id)
        raise
    finally:
        db.close()


def finish_single_test(run_id: str, prev_state: str = None):
    """After a single test completes, update progress and restore run state.

    For gathered runs: returns to GATHERED if pending tests remain, else COMPLETED.
    For old terminal runs (completed/failed/stopped): reassembles report and
    restores previous state so the run stays in its original terminal state.
    """
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if not run:
            return

        results = db.query(BenchmarkTestResult).filter(BenchmarkTestResult.run_id == run_id).all()
        total = len(results)
        done = sum(1 for r in results if r.status in ("pass", "fail", "error", "stopped"))
        waiting = sum(1 for r in results if r.status == "waiting")
        still_running = sum(1 for r in results if r.status == "running")
        pending = sum(1 for r in results if r.status == "pending")
        run.progress_current = done + waiting
        # Do NOT overwrite progress_total here — it was set at run creation
        # to the planned filtered count and must stay stable. Earlier code
        # set it to ``len(results)`` after each test, which fluctuated as
        # ``add_test_result_progressive`` inserted rows mid-run, leading
        # to the denominator visibly changing in the UI ("4/5" → "4/6").
        # Only nudge it up if the live row count exceeds the original
        # plan (defensive: shouldn't happen with current code, but keeps
        # the bar from going past 100%).
        if total > (run.progress_total or 0):
            run.progress_total = total

        # Reassemble report with updated results
        run.report_json = _assemble_report(db, run)

        # If the run was stopped/failed while this test was in flight,
        # don't override that terminal state — just update progress/report
        if run.state in (RunState.STOPPED, RunState.FAILED):
            run.current_query = ""
            logger.info("Run %s already %s, updated progress only", run_id, run.state)
        elif still_running > 0:
            # Other tests still in flight — keep run in RUNNING state
            run.current_query = ""
            logger.info("Run %s: test done but %d still running", run_id, still_running)
        elif pending == 0 and waiting == 0 and total < (run.progress_total or 0):
            # All visible rows are terminal but not every planned test has
            # materialised yet. This is the parallel-orchestrator race: with
            # N workers and >N tests, the executor queues the overflow and
            # only inserts a row when a worker actually starts the test. If
            # we transitioned to COMPLETED here, the queued tests would abort
            # via ``should_proceed`` and never write a row. Keep the run in
            # RUNNING so the remaining tests can start.
            run.current_query = ""
            logger.info(
                "Run %s: %d/%d tests materialised, %d planned not yet started",
                run_id,
                total,
                run.progress_total or 0,
                (run.progress_total or 0) - total,
            )
        elif pending == 0 and waiting == 0:
            # All tests done — mark completed
            run.state = RunState.COMPLETED
            run.phase = RunPhase.DONE
            if not run.completed_at:
                run.completed_at = datetime.now()
            if run.started_at:
                run.duration_seconds = round((datetime.now() - run.started_at).total_seconds(), 2)
            logger.info("All tests complete for run %s", run_id)
        elif prev_state in (RunState.COMPLETED, RunState.FAILED, RunState.STOPPED):
            # Old terminal run — restore to previous state with updated report
            run.state = prev_state
            run.phase = RunPhase.DONE
            run.current_query = ""
            logger.info("Run %s: test re-run complete, restored to %s", run_id, prev_state)
        elif waiting > 0 and pending == 0:
            # All tests executed but some waiting for followup — keep running
            run.state = RunState.RUNNING
            run.phase = RunPhase.RUNNING_TESTS
            run.current_query = ""
            logger.info(
                "Run %s: %d/%d tests complete, %d waiting for followup",
                run_id,
                done,
                total,
                waiting,
            )
        else:
            # Gathered run with pending tests remaining
            run.state = RunState.GATHERED
            run.phase = RunPhase.GATHERED
            run.current_query = ""
            logger.info(
                "Run %s: %d/%d tests complete, %d pending",
                run_id,
                done,
                total,
                pending,
            )

        db.commit()
    except Exception:
        db.rollback()
        logger.exception("Failed to finish single test for run %s", run_id)
    finally:
        db.close()


def cleanup_run(run_id: str):
    """Remove the run and its test results from the database."""
    if not run_id:
        return
    db = get_db()
    if not db:
        return
    try:
        run = _get_run(db, run_id)
        if run:
            db.delete(run)  # cascade deletes test_results
            db.commit()
    except Exception:
        db.rollback()
    finally:
        db.close()


def list_runs(
    page: int = 1,
    page_size: int = 20,
    agent: str = None,
    status: str = None,
    search: str = None,
    user: str = None,
    tenant_ids: Optional[list[str]] = None,
) -> dict:
    """Return a paginated list of benchmark runs with optional filters.

    Uses progress_current/progress_total columns instead of loading
    all test_results, making the query lightweight.

    ``tenant_ids`` semantics:
      - ``None``  → no filter (super_admin sees every tenant's runs).
      - ``[...]`` → restrict to runs owned by any of the given tenant_ids
                    (the user's authz tenant set — multi-tenant users see
                    their union, single-tenant users get a single-element
                    list).
      - ``[]``    → empty list, match nothing (a user with no tenants).
    """
    db = get_db()
    if not db:
        return {"runs": [], "total": 0, "page": page, "page_size": page_size}
    try:
        query = db.query(BenchmarkRun)
        if tenant_ids is not None:
            if not tenant_ids:
                # Empty list: short-circuit to no results.
                return {"runs": [], "total": 0, "page": page, "page_size": page_size}
            query = query.filter(BenchmarkRun.tenant_id.in_(tenant_ids))
        if agent:
            query = query.filter(BenchmarkRun.agent == agent)
        if status:
            query = query.filter(BenchmarkRun.state == status)
        if search:
            search_pattern = f"%{search}%"
            query = query.filter(
                BenchmarkRun.run_name.ilike(search_pattern)
                | BenchmarkRun.run_id.ilike(search_pattern)
            )
        if user:
            # Find user_ids whose email matches (from cache first, DB fallback)
            user_lower = user.lower()
            matching_ids = [
                uid for uid, email in _user_email_cache.items() if user_lower in email.lower()
            ]
            if not matching_ids:
                from benchmark_server.utils.db_utils import search_user_ids_by_email

                matching_ids = search_user_ids_by_email(user_lower)
            if matching_ids:
                query = query.filter(BenchmarkRun.user_id.in_(matching_ids))
            else:
                query = query.filter(BenchmarkRun.user_id.is_(None))  # no match
        query = query.order_by(BenchmarkRun.created_at.desc())
        total = query.count()
        runs = query.offset((page - 1) * page_size).limit(page_size).all()

        result = []
        for r in runs:
            if r.duration_seconds and r.duration_seconds > 0:
                duration = r.duration_seconds
            elif r.started_at and r.state in (RunState.RUNNING, RunState.PAUSED):
                duration = round((datetime.now() - r.started_at).total_seconds(), 2)
            else:
                duration = 0
            result.append(
                {
                    "run_id": r.run_id,
                    "state": r.state or "unknown",
                    "phase": r.phase or "unknown",
                    "agent": r.agent or "",
                    "run_name": r.run_name or "",
                    "progress": f"{r.progress_current or 0}/{r.progress_total or 0}",
                    "created_at": r.created_at.isoformat() if r.created_at else "",
                    "duration_seconds": duration,
                    "errors": len(r.errors or []),
                    "has_report": bool(r.report_json),
                    "tenant_id": r.tenant_id or "",
                    "tenant_name": _resolve_name_cached("tenant", r.tenant_id),
                    "account_id": r.account_id or "",
                    "account_name": _resolve_name_cached("account", r.account_id),
                    "user_email": _get_user_email(r.user_id),
                }
            )
        return {"runs": result, "total": total, "page": page, "page_size": page_size}
    except Exception:
        logger.exception("Failed to list runs")
        return {"runs": [], "total": 0, "page": page, "page_size": page_size}
    finally:
        db.close()


def list_run_users(tenant_ids: Optional[list[str]] = None) -> list[dict]:
    """Return the distinct users who own runs, for the runs-page user filter.

    Built from the *whole* run set within the caller's tenant scope (not a
    single page), so the "All Users" dropdown lists every contributor rather
    than only those visible on the current page.

    ``tenant_ids`` semantics mirror :func:`list_runs`:
      - ``None``  → all tenants (super_admin).
      - ``[...]`` → restrict to the given tenant_ids.
      - ``[]``    → empty list, match nothing.

    Emails resolve through the process-cached ``_get_user_email``, which is
    already warm from rendering the runs list — so this is a single DISTINCT
    query plus near-zero email lookups.
    """
    db = get_db()
    if not db:
        return []
    try:
        query = db.query(BenchmarkRun.user_id).distinct()
        if tenant_ids is not None:
            if not tenant_ids:
                return []
            query = query.filter(BenchmarkRun.tenant_id.in_(tenant_ids))
        user_ids = [row[0] for row in query.all() if row[0]]
        users = []
        for uid in user_ids:
            email = _get_user_email(uid)
            if email:
                users.append({"user_id": uid, "user_email": email})
        users.sort(key=lambda u: u["user_email"].lower())
        return users
    except Exception:
        logger.exception("Failed to list run users")
        return []
    finally:
        db.close()


def cleanup_completed_runs(tenant_ids: Optional[list[str]] = None) -> int:
    """Remove records for completed, stopped, and failed runs.

    ``tenant_ids`` semantics mirror :func:`list_runs`:
      - ``None``  → cleanup across all tenants (super_admin).
      - ``[...]`` → restrict to the given tenant_ids.
      - ``[]``    → empty list, deletes nothing (no tenants in scope).
    """
    db = get_db()
    if not db:
        return 0
    try:
        query = db.query(BenchmarkRun).filter(
            BenchmarkRun.state.in_([RunState.COMPLETED, RunState.STOPPED, RunState.FAILED])
        )
        if tenant_ids is not None:
            if not tenant_ids:
                return 0
            query = query.filter(BenchmarkRun.tenant_id.in_(tenant_ids))
        count = query.delete(synchronize_session="fetch")
        db.commit()
        return count
    except Exception:
        db.rollback()
        return 0
    finally:
        db.close()
