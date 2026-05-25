"""Custom test orchestrator — replaces pytest for benchmark execution.

Provides: test discovery, parallel execution, per-test lifecycle (before/after),
RAGAS evaluation, progressive DB storage, and infrastructure management.

All heavy lifting reuses existing modules:
- fixtures.py: test discovery + YAML loading
- lifecycle.py: before_test/after_test shell commands
- llm_client.py: LLM API calls
- ragas_evaluation.py: scoring
- run_manager.py: DB storage + progress tracking
"""

import importlib
import logging
import os
import re
import subprocess
import time
import traceback
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

import yaml
from ragas.llms import LangchainLLMWrapper

from benchmark_server.common.llm import get_embeddings, get_llm
from benchmark_server.utils.llm_client import (
    ErrorCategory,
    call_llm,
    extract_conversation_id,
    extract_response_text,
    extract_tool_command,
)
from benchmark_server.utils.run_manager import (
    RunPhase,
    add_error,
    reserve_test_rows,
    set_phase,
    should_proceed,
    store_test_result,
    update_progress,
)

from llm.agents.common.fixtures import find_test_cases, load_test_case
from llm.agents.common.lifecycle import run_before_test, run_after_test

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------


@dataclass
class SessionState:
    """Shared state for a benchmark session."""

    run_id: str
    agent: str
    account_id: str
    tenant_id: str
    user_id: str
    config: Dict[str, Any]
    llm: Any = None  # LangchainLLMWrapper
    embeddings: Any = None
    results: List[Dict] = field(default_factory=list)
    infra_deploy_duration: float = 0.0
    infra_nuke_duration: float = 0.0


@dataclass
class TestResult:
    """Result of a single test execution."""

    test_id: str
    test_index: int
    status: str  # pass, fail, error, waiting
    duration: float = 0.0
    error: str = ""


# ---------------------------------------------------------------------------
# Config helpers (extracted from benchmark.py)
# ---------------------------------------------------------------------------


def _load_agent_config(agent_dir: Path) -> Dict[str, Any]:
    """Load config.yaml from agent directory and resolve hooks."""
    config_path = agent_dir / "config.yaml"
    if not config_path.exists():
        raise FileNotFoundError(f"config.yaml not found in {agent_dir}")
    with open(config_path) as f:
        config = yaml.safe_load(f)

    config["_agent_dir"] = str(agent_dir)
    config["_fixtures_dir"] = str(agent_dir / "fixtures")

    # Resolve callable hooks
    hooks = {}
    for key in ("before_query", "after_query", "result_enricher", "answer_extractor"):
        path = config.get(key)
        if path:
            try:
                module_path, func_name = path.rsplit(".", 1)
                module = importlib.import_module(module_path)
                hooks[key] = getattr(module, func_name)
            except Exception as e:
                logger.error("Failed to import %s=%s: %s", key, path, e)
                hooks[key] = None
        else:
            hooks[key] = None
    config["_hooks"] = hooks
    return config


def _default_answer_extractor(response_data, account_id, tool_names, db_tool_name=None):
    """Extract tool command from response."""
    if not response_data:
        return []
    cmd = extract_tool_command(response_data, tool_names)
    if cmd:
        return [cmd]
    resp = extract_response_text(response_data)
    if resp:
        return (
            [resp]
            if isinstance(resp, str)
            else [str(r) for r in resp] if isinstance(resp, list) else [str(resp)]
        )
    return []


def _response_text_extractor(response_data, account_id, tool_names, db_tool_name=None):
    """Extract response text for non-command tests."""
    if not response_data:
        return []
    resp = extract_response_text(response_data)
    if resp:
        return (
            [resp]
            if isinstance(resp, str)
            else [str(r) for r in resp] if isinstance(resp, list) else [str(resp)]
        )
    return []


def _wrap_followups_for_storage(followups: list) -> Optional[dict]:
    """Wrap a unified followup list into the storage dict shape.

    Storage layout (backward-compatible):
      - When 0 followups: return None so the row's followup_request is cleared.
      - When >=1 followups: mirror the first followup's fields at the top level
        (legacy single-followup readers use these) and attach ``followups``
        (canonical) plus ``_all`` (old alias kept until all readers migrate).
    """
    if not followups:
        return None
    first = followups[0] or {}
    return {
        **first,
        "followups": followups,
        "_all": followups,  # legacy alias, to be removed once UI/controller migrate
    }


def _flatten_ground_truth(gt) -> str:
    if isinstance(gt, str):
        return gt
    if isinstance(gt, list):
        parts = []
        for item in gt:
            if isinstance(item, (list, tuple)):
                parts.append(" ".join(str(x) for x in item))
            else:
                parts.append(str(item))
        return " ".join(parts)
    return str(gt)


# ---------------------------------------------------------------------------
# Namespace labeling for benchmark cleanup
# ---------------------------------------------------------------------------


def _label_benchmark_namespaces(test_case: Dict[str, Any], run_id: str) -> None:
    """Label any namespaces created by before_test for easy cleanup.

    Parses the before_test script for 'create namespace' or 'kubectl apply -n'
    patterns, then labels matching namespaces with benchmark=true and run-id.
    """
    before_test = test_case.get("before_test", "")
    if not before_test:
        return

    # Extract namespace names from common patterns
    namespaces = set()
    # Pattern: kubectl create namespace <name>
    for m in re.finditer(r"kubectl\s+create\s+namespace\s+(\S+)", before_test):
        ns = m.group(1).strip("\"'")
        if ns != "||":  # skip "|| true" fragments
            namespaces.add(ns)
    # Pattern: -n <name> or --namespace <name>
    for m in re.finditer(r"(?:-n|--namespace)\s+(\S+)", before_test):
        ns = m.group(1).strip("\"'")
        if ns not in ("default", "||"):
            namespaces.add(ns)

    for ns in namespaces:
        try:
            subprocess.run(
                [
                    "kubectl",
                    "label",
                    "ns",
                    ns,
                    "benchmark=true",
                    f"benchmark-run-id={run_id}",
                    "--overwrite",
                ],
                capture_output=True,
                text=True,
                timeout=10,
            )
        except Exception:
            pass  # best-effort labeling


# ---------------------------------------------------------------------------
# Single test execution
# ---------------------------------------------------------------------------


def execute_single_test(
    test_case: Dict[str, Any],
    session: SessionState,
    config: Dict[str, Any],
) -> TestResult:
    """Execute a single benchmark test case.

    Handles: before_test → LLM call → retries → RAGAS eval → store to DB → after_test.
    Thread-safe — can be called from parallel workers.
    """
    start_time = time.time()
    test_id = test_case.get("__id__", "unknown")
    global_index = test_case.get("__global_index__", 0)
    run_id = session.run_id

    logger.info("[%s] Starting test (index=%d)", test_id, global_index)

    # Check if run is still active
    if run_id and not should_proceed(run_id):
        logger.info("[%s] Benchmark stopped by user, skipping", test_id)
        return TestResult(test_id=test_id, test_index=global_index, status="stopped")

    # Mark as running in DB
    if run_id:
        store_test_result(
            run_id,
            {
                "test_id": test_id,
                "test_index": global_index,
                "status": "running",
                "query": test_case.get("user_prompt", "")[:200],
                "expected_answer": _flatten_ground_truth(test_case.get("expected_output", "")),
                "tags": test_case.get("tags", []),
            },
        )

    # --- Full lifecycle with guaranteed cleanup ---
    skip_cleanup = False
    setup_ran = False
    setup_duration = 0.0
    teardown_duration = 0.0
    try:
        # Before test
        setup_start = time.time()
        setup_result = run_before_test(test_case)
        setup_duration = round(time.time() - setup_start, 2)
        setup_ran = True
        # Label any namespaces created by before_test for cleanup tracking
        if setup_result.success and run_id:
            _label_benchmark_namespaces(test_case, run_id)
        if not setup_result.success:
            error_msg = (
                f"Setup failed: {setup_result.error_details or setup_result.stderr or 'unknown'}"
            )
            logger.error("[%s] %s", test_id, error_msg)
            # Categorize setup failures
            error_cat = "setup_failed"
            if setup_result.error_type == "timeout":
                error_cat = "setup_timeout"
            elif "Permission denied" in (setup_result.stderr or ""):
                error_cat = "setup_permission"
            elif "Missing env var" in (setup_result.stdout or "") or "Missing env var" in (
                setup_result.stderr or ""
            ):
                error_cat = "setup_env_missing"
            elif "timed out waiting" in (setup_result.stderr or ""):
                error_cat = "infra_timeout"
            _store_error(
                run_id,
                test_case,
                global_index,
                error_msg,
                error_cat,
                time.time() - start_time,
                setup_duration=setup_duration,
            )
            return TestResult(
                test_id=test_id,
                test_index=global_index,
                status="error",
                error=error_msg,
                duration=time.time() - start_time,
            )

        # Core test execution
        result = _run_test_core(
            test_case, session, config, global_index, start_time, setup_duration
        )
        if result.status == "waiting":
            skip_cleanup = True
        return result

    except Exception as e:
        error_msg = f"Test exception: {traceback.format_exc()}"
        logger.error("[%s] %s", test_id, error_msg)
        _store_error(
            run_id,
            test_case,
            global_index,
            str(e),
            "test_error",
            time.time() - start_time,
            setup_duration=setup_duration,
        )
        return TestResult(
            test_id=test_id,
            test_index=global_index,
            status="error",
            error=str(e),
            duration=time.time() - start_time,
        )

    finally:
        # --- After test — ALWAYS runs if setup ran (even on setup failure) ---
        if setup_ran and not skip_cleanup:
            try:
                teardown_start = time.time()
                teardown_result = run_after_test(test_case)
                teardown_duration = round(time.time() - teardown_start, 2)
                if not teardown_result.success:
                    logger.warning(
                        "[%s] Teardown failed: %s",
                        test_id,
                        teardown_result.error_details,
                    )
            except Exception as e:
                logger.warning("[%s] Teardown exception: %s", test_id, e)
            # Post-update teardown_duration to DB (main store already happened)
            if run_id and teardown_duration > 0:
                try:
                    store_test_result(
                        run_id,
                        {
                            "test_index": global_index,
                            "teardown_duration": teardown_duration,
                        },
                    )
                except Exception:
                    pass  # best-effort


def _run_test_core(
    test_case: Dict[str, Any],
    session: SessionState,
    config: Dict[str, Any],
    global_index: int,
    start_time: float,
    setup_duration: float = 0.0,
) -> TestResult:
    """Core test logic: LLM call → retries → RAGAS eval → store."""
    test_id = test_case.get("__id__", "unknown")
    run_id = session.run_id
    hooks = config.get("_hooks", {})

    agent = test_case.get("agent", config["agent"])
    tool_names = config.get("tool_names", [])
    db_tool_name = config.get("db_tool_name")
    prefix_query = config.get("prefix_query", True)
    max_retries = config.get("max_retries", 2)

    # Build LLM config
    tool_config_str = os.getenv("TOOL_CONFIG", "")
    llm_config = None
    if tool_config_str and tool_names:
        confirmations = {name: "yes" for name in tool_names}
        extra = config.get("extra_tool_confirmations", {})
        if extra:
            confirmations.update(extra)
        llm_config = {
            "tool_configs": {name: tool_config_str for name in tool_names},
            "tool_confirmations": confirmations,
        }

    # Resolve extractor
    extractor = hooks.get("answer_extractor") or _default_answer_extractor
    test_type = test_case.get("type", "command")
    if test_type != "command" and extractor is _default_answer_extractor:
        extractor = _response_text_extractor

    # Before query hook (e.g. RCA feature flags)
    before_query = hooks.get("before_query")
    after_query = hooks.get("after_query")
    if before_query:
        try:
            before_query(test_case)
        except Exception as e:
            logger.error("[%s] before_query hook failed: %s", test_id, e)
            _store_error(
                run_id,
                test_case,
                global_index,
                f"before_query hook failed: {e}",
                "setup_failed",
                time.time() - start_time,
                setup_duration=setup_duration,
            )
            if after_query:
                try:
                    after_query(test_case)
                except Exception:
                    pass
            return TestResult(
                test_id=test_id,
                test_index=global_index,
                status="error",
                error=str(e),
                duration=time.time() - start_time,
            )

    user_prompt = test_case["user_prompt"]
    logger.info("[%s] Querying: %s...", test_id, user_prompt[:100])

    # Execute LLM call
    final_query = f"@{agent} {user_prompt}" if prefix_query else user_prompt
    llm_start = time.time()
    llm_result = call_llm(
        final_query,
        session.account_id,
        session.tenant_id,
        session.user_id,
        config=llm_config,
    )
    elapsed = round(time.time() - llm_start, 2)
    docs = extractor(llm_result.data, session.account_id, tool_names, db_tool_name)

    # Handle WAITING status
    if llm_result.status == "WAITING":
        convo_id = extract_conversation_id(llm_result.data) if llm_result.data else None
        # Pull session_id too so the UI can show it alongside conversation_id
        # without waiting for terminal state. session_id is the main handle
        # for token/metrics API queries and is more useful than convo_id for
        # most debug-copy flows.
        session_id = ""
        if llm_result.data:
            db = llm_result.data.get("data")
            if isinstance(db, dict):
                session_id = db.get("session_id") or ""
        followups_list = llm_result.followups or []
        if run_id:
            store_test_result(
                run_id,
                {
                    "test_id": test_id,
                    "test_index": global_index,
                    "status": "waiting",
                    "conversation_id": convo_id or "",
                    "polling_conversation_id": session_id,
                    "query": user_prompt,
                    "expected_answer": _flatten_ground_truth(test_case.get("expected_output", "")),
                    "duration_seconds": round(time.time() - start_time, 2),
                    # Unified shape: always a list, one entry per pending panel.
                    # First entry is also mirrored into ``followup_request`` for the
                    # legacy single-followup readers (UI fallback, older reports).
                    "followup_request": _wrap_followups_for_storage(followups_list),
                    "tags": test_case.get("tags", []),
                    "error_message": "",
                    "error_category": "",
                },
            )
        try:
            from benchmark_server.utils.followup_events import emit as _fevent

            for fu in followups_list:
                _fevent(
                    "waiting",
                    run_id=run_id or "",
                    test_index=global_index,
                    conversation_id=convo_id or "",
                    followup_id=str(fu.get("message_id") or ""),
                    agent_id=str(fu.get("agent_id") or ""),
                    followup_type=str(fu.get("followup_type") or ""),
                    from_state="running",
                    to_state="waiting",
                )
        except Exception:
            pass
        logger.info("[%s] WAITING for followup (%.1fs)", test_id, time.time() - start_time)
        return TestResult(
            test_id=test_id,
            test_index=global_index,
            status="waiting",
            duration=time.time() - start_time,
        )

    # Retry on failure
    error_category = None
    error_message = ""
    if not docs:
        error_category = llm_result.error_category
        error_message = llm_result.error_message or ""
        if max_retries > 0 and (not error_category or error_category in ErrorCategory.RETRYABLE):
            for attempt in range(1, max_retries + 1):
                time.sleep(2.0 * (2 ** (attempt - 1)))
                logger.info("[%s] Retry %d/%d", test_id, attempt, max_retries)
                llm_result = call_llm(
                    final_query,
                    session.account_id,
                    session.tenant_id,
                    session.user_id,
                    config=llm_config,
                )
                docs = extractor(llm_result.data, session.account_id, tool_names, db_tool_name)
                if docs:
                    error_category = None
                    error_message = ""
                    break
                error_category = llm_result.error_category
                error_message = llm_result.error_message or ""
                if error_category and error_category not in ErrorCategory.RETRYABLE:
                    break

    # After query hook
    if after_query:
        try:
            after_query(test_case)
        except Exception as e:
            logger.warning("[%s] after_query hook failed: %s", test_id, e)

    duration = time.time() - start_time
    answer = " ".join(str(d) for d in docs) if docs else "SYSTEM FAILURE"
    test_failed = not docs

    ground_truths = test_case.get("expected_output", "")
    if isinstance(ground_truths, str):
        ground_truths = [ground_truths]
    ground_truth_str = _flatten_ground_truth(ground_truths)

    # Token/tool metrics
    from llm.agents.common.metrics import (
        get_token_metrics,
        get_tool_names,
        get_planner_response,
        get_execution_trace,
    )

    convo_id = extract_conversation_id(llm_result.data) if llm_result.data else None
    session_id = None
    if llm_result.data:
        data_block = llm_result.data.get("data")
        if isinstance(data_block, dict):
            session_id = data_block.get("session_id")

    token_metrics = get_token_metrics(
        session_id, session.account_id, session.tenant_id, session.user_id
    )
    tool_names_data = get_tool_names(convo_id=convo_id)

    # --- RAGAS evaluation ---
    sim_score = 0.0
    rel_score = 0.0
    planner_score = 0.0
    score_reason = ""
    trace = ""

    if not test_failed and session.llm:
        try:
            from llm.agents.common.ragas_evaluation import evaluate_single

            eval_result = evaluate_single(
                user_prompt, answer, ground_truth_str, session.llm, session.embeddings
            )
            sim_score = eval_result.similarity
            rel_score = eval_result.quality
            score_reason = eval_result.reason

            # Planner evaluation
            trace = get_execution_trace(convo_id, session.account_id) if convo_id else ""
            if not trace:
                trace = get_planner_response(convo_id, session.account_id) or ""
            if trace:
                from llm.agents.common.benchmark import _evaluate_planner

                planner_score, planner_reason = _evaluate_planner(trace, user_prompt, session.llm)
                if planner_reason:
                    score_reason += f"\n[Planner] {planner_reason}"
                    score_reason = score_reason.strip()
        except Exception as e:
            logger.error("[%s] RAGAS evaluation failed: %s", test_id, e)

    # Result enricher hook (e.g. AWS/Azure RCA scoring, SignOz structural eval)
    result_enricher = hooks.get("result_enricher")
    if result_enricher and not test_failed:
        try:
            enricher_data = {
                "test_id": test_id,
                "query": user_prompt,
                "answer": answer,
                "ground_truth": ground_truth_str,
                "answer_similarity": sim_score,
                "answer_relevancy": rel_score,
            }
            result_enricher(enricher_data, test_case, session.llm)
            # Enricher may override scores
            sim_score = enricher_data.get("answer_similarity", sim_score)
            rel_score = enricher_data.get("answer_relevancy", rel_score)
        except Exception as e:
            logger.warning("[%s] result_enricher hook failed: %s", test_id, e)

    # Store to DB
    if run_id:
        store_test_result(
            run_id,
            {
                "test_id": test_id,
                "test_index": global_index,
                "status": "pass" if not test_failed else "fail",
                "conversation_id": convo_id or "",
                "polling_conversation_id": session_id or "",
                "query": user_prompt,
                "expected_answer": ground_truth_str,
                "actual_answer": answer,
                "answer_similarity": sim_score,
                "answer_relevancy": rel_score,
                "planner_relevancy": planner_score,
                "score_reason": score_reason,
                "execution_trace": trace if not test_failed else "",
                "duration_seconds": round(duration, 2),
                "setup_duration": setup_duration,
                "llm_duration": elapsed,
                "cost": token_metrics.get("cost", 0.0) if token_metrics else 0.0,
                "total_tokens": (token_metrics.get("total_tokens", 0) if token_metrics else 0),
                "input_tokens": (token_metrics.get("input_tokens", 0) if token_metrics else 0),
                "output_tokens": (
                    token_metrics.get("completion_tokens", 0) if token_metrics else 0
                ),
                "cache_read_tokens": (
                    token_metrics.get("cached_input_tokens", 0) if token_metrics else 0
                ),
                "tool_calls_total": (
                    token_metrics.get("total_tool_calls", 0) if token_metrics else 0
                ),
                "tool_calls_successful": (
                    token_metrics.get("successful_tool_calls", 0) if token_metrics else 0
                ),
                "tool_names": (tool_names_data.get("tool_names", []) if tool_names_data else []),
                "model_names": (token_metrics.get("model_names", []) if token_metrics else []),
                "model_providers": (
                    token_metrics.get("model_providers", []) if token_metrics else []
                ),
                "tags": test_case.get("tags", []),
                "error_message": error_message,
                "error_category": error_category or "",
            },
        )

    status = "pass" if not test_failed else "fail"
    logger.info(
        "[%s] %s (%.1fs) sim=%.1f rel=%.1f planner=%.1f",
        test_id,
        status.upper(),
        duration,
        sim_score,
        rel_score,
        planner_score,
    )

    return TestResult(
        test_id=test_id,
        test_index=global_index,
        status=status,
        duration=duration,
        error=error_message,
    )


def _store_error(
    run_id, test_case, global_index, error_msg, error_cat, duration, setup_duration=0.0
):
    """Store a failed test result to DB."""
    if not run_id:
        return
    test_id = test_case.get("__id__", "unknown")
    store_test_result(
        run_id,
        {
            "test_id": test_id,
            "test_index": global_index,
            "status": "error",
            "query": test_case.get("user_prompt", "")[:200],
            "expected_answer": _flatten_ground_truth(test_case.get("expected_output", "")),
            "duration_seconds": round(duration, 2),
            "setup_duration": setup_duration,
            "error_message": error_msg[:500],
            "error_category": error_cat,
            "tags": test_case.get("tags", []),
        },
    )


# ---------------------------------------------------------------------------
# Orchestrator
# ---------------------------------------------------------------------------


class TestOrchestrator:
    """Custom test orchestrator — replaces pytest for benchmark execution.

    Usage:
        orchestrator = TestOrchestrator()
        success = orchestrator.run(
            agent_dir="/path/to/agent",
            run_id="abc123",
            account_id="...",
            tenant_id="...",
            user_id="...",
        )
    """

    def __init__(self, parallel_workers: int = None):
        self.parallel_workers = parallel_workers or int(
            os.getenv("BENCHMARK_PARALLEL_WORKERS", "1")
        )

    def run(
        self,
        agent_dir: str,
        run_id: str,
        account_id: str,
        tenant_id: str,
        user_id: str,
        max_tests: Optional[int] = None,
        test_indices: Optional[str] = None,
        tag_filter: Optional[str] = None,
        skip_indices: Optional[str] = None,
        tool_config: Optional[str] = None,
    ) -> bool:
        """Run a complete benchmark session.

        Returns True if all tests passed, False otherwise.
        """
        agent_path = Path(agent_dir)
        fixtures_dir = agent_path / "fixtures"

        # Set env vars for downstream code that reads them
        os.environ["ACCOUNT_ID"] = account_id
        os.environ["TENANT_ID"] = tenant_id
        os.environ["USER_ID"] = user_id
        os.environ["BENCHMARK_RUN_ID"] = run_id
        os.environ["BENCHMARK_AGENT_DIR"] = agent_dir
        if tool_config:
            os.environ["TOOL_CONFIG"] = tool_config
        elif "TOOL_CONFIG" in os.environ:
            del os.environ["TOOL_CONFIG"]

        logger.info(
            "Orchestrator: starting benchmark run=%s agent=%s workers=%d",
            run_id,
            agent_path.name,
            self.parallel_workers,
        )

        # 1. Load agent config
        try:
            config = _load_agent_config(agent_path)
        except Exception as e:
            logger.error("Failed to load agent config: %s", e)
            add_error(run_id, f"Config load failed: {e}")
            return False

        # 2. Discover tests
        test_cases_raw = find_test_cases(
            fixtures_dir,
            max_tests=max_tests,
            test_indices=test_indices,
            skip_indices=skip_indices,
            tag_filter=tag_filter,
        )
        if not test_cases_raw:
            logger.warning("No test cases found in %s", fixtures_dir)
            add_error(run_id, "No test cases found")
            return False

        # Load full test case data
        test_cases = []
        for file_path, test_id, global_index in test_cases_raw:
            tc = load_test_case(file_path)
            tc["__global_index__"] = global_index
            # Skip tests marked as skip
            if tc.get("skip"):
                skip_reason = tc.get("skip_reason", "marked skip")
                logger.info("Skipping test %s: %s", test_id, skip_reason)
                if run_id:
                    store_test_result(
                        run_id,
                        {
                            "test_id": test_id,
                            "test_index": global_index,
                            "status": "skipped",
                            "query": tc.get("user_prompt", "")[:200],
                            "expected_answer": _flatten_ground_truth(tc.get("expected_output", "")),
                            "error_message": skip_reason,
                            "tags": tc.get("tags", []),
                        },
                    )
                continue
            test_cases.append(tc)

        total = len(test_cases)
        logger.info(
            "Orchestrator: discovered %d tests (from %d total fixtures)",
            total,
            len(test_cases_raw),
        )

        # Set progress_total upfront so the UI's denominator is correct from
        # the moment the run is visible. Without this, the parallel executor
        # inserts ``running`` rows for the first ``parallel_workers`` tests
        # before any of them complete — and update_progress only fires AFTER
        # a test completes — so /status falls back to ``len(test_results)``
        # and shows e.g. "0/2" with parallel_workers=2 even when the actual
        # planned total is 6.
        if run_id:
            update_progress(run_id, 0, total, "")
            # Reserve a ``pending`` row for every planned test BEFORE
            # _execute_parallel submits them to the ThreadPoolExecutor. With
            # N workers and >N tests, the overflow sits in the executor queue
            # without a DB row — that under-count caused completion-gate
            # functions (sweeper's _run_has_all_terminal_tests,
            # recover_orphaned_runs' non-terminal probe, complete_run's
            # planned-total check) to fire on partial state and flip the run
            # to COMPLETED/FAILED while queued workers had not yet picked up
            # their tests. With pending rows in place from the start, every
            # gate sees ``total`` non-terminal rows until each test actually
            # runs.
            reserve_test_rows(run_id, test_cases)

        # 3. Setup session (RAGAS init + infra deploy)
        session = None
        try:
            session = self._setup_session(
                run_id,
                config,
                account_id,
                tenant_id,
                user_id,
                agent_path=agent_path,
                fixtures_dir=fixtures_dir,
                max_tests=max_tests,
                test_indices=test_indices,
                tag_filter=tag_filter,
            )
        except Exception as e:
            logger.error("Session setup failed: %s", e)
            add_error(run_id, f"Session setup failed: {e}")
            # Still nuke infra even if RAGAS init failed (deploy may have succeeded)
            self._finalize(session, run_id, agent_path=agent_path)
            return False

        # 4. Execute tests
        results = []
        try:
            if self.parallel_workers > 1:
                results = self._execute_parallel(test_cases, session, config, total)
            else:
                results = self._execute_sequential(test_cases, session, config, total)
        except Exception as e:
            logger.error("Test execution failed: %s", e)
            add_error(run_id, f"Execution failed: {e}")
        finally:
            # 5. Finalize (nuke infra + report) — ALWAYS runs
            self._finalize(session, run_id, agent_path=agent_path)

        passed = sum(1 for r in results if r.status == "pass")
        failed = sum(1 for r in results if r.status in ("fail", "error"))
        logger.info(
            "Orchestrator: complete. %d passed, %d failed, %d total",
            passed,
            failed,
            len(results),
        )

        return failed == 0

    def _setup_session(
        self,
        run_id,
        config,
        account_id,
        tenant_id,
        user_id,
        agent_path: Path = None,
        fixtures_dir: Path = None,
        max_tests=None,
        test_indices=None,
        tag_filter=None,
    ) -> SessionState:
        """Initialize RAGAS LLM/embeddings, deploy infrastructure if needed, set phase."""
        logger.info("Orchestrator: initializing session")

        base_llm = get_llm()
        llm = LangchainLLMWrapper(base_llm)
        embeddings = get_embeddings()

        session = SessionState(
            run_id=run_id,
            agent=config["agent"],
            account_id=account_id,
            tenant_id=tenant_id,
            user_id=user_id,
            config=config,
            llm=llm,
            embeddings=embeddings,
        )

        # Deploy infrastructure for AWS/Azure agents
        if agent_path and os.getenv("DEPLOY_INFRA", "").lower() in ("1", "true", "yes"):
            deploy_start = time.time()
            self._deploy_infrastructure(
                config["agent"],
                agent_path,
                fixtures_dir,
                max_tests,
                test_indices,
                tag_filter,
            )
            session.infra_deploy_duration = round(time.time() - deploy_start, 2)
            logger.info("Orchestrator: infra deploy took %.1fs", session.infra_deploy_duration)

        if run_id:
            set_phase(run_id, RunPhase.RUNNING_TESTS)

        return session

    def _deploy_infrastructure(
        self, agent_name, agent_path, fixtures_dir, max_tests, test_indices, tag_filter
    ):
        """Deploy cloud infrastructure (AWS/Azure) before running tests."""
        import subprocess as sp
        from llm.agents.common.lifecycle import resolve_scenarios_from_fixtures

        # Check for deploy scripts
        aws_test_dir = agent_path / "aws-agent-test"
        azure_test_dir = agent_path / "azure-agent-test"

        if aws_test_dir.exists() and (aws_test_dir / "scripts" / "deploy.sh").exists():
            deploy_sh = aws_test_dir / "scripts" / "deploy.sh"
            scenarios = resolve_scenarios_from_fixtures(
                fixtures_dir,
                test_indices=test_indices,
                max_tests=max_tests,
                tag_filter=tag_filter,
            )
            if not scenarios:
                scenarios = sorted(p.stem for p in (aws_test_dir / "scenarios").rglob("*.yaml"))

            logger.info("Deploying AWS infrastructure: %s", scenarios)
            sp.run(["bash", str(deploy_sh), "--bootstrap"], check=True, text=True)
            for scenario in scenarios:
                try:
                    sp.run(
                        ["bash", str(deploy_sh), scenario, "Broken"],
                        check=True,
                        text=True,
                    )
                except sp.CalledProcessError:
                    logger.error("Failed to deploy AWS scenario %s", scenario)

        elif azure_test_dir.exists() and (azure_test_dir / "scripts" / "deploy.sh").exists():
            deploy_sh = azure_test_dir / "scripts" / "deploy.sh"
            scenarios = resolve_scenarios_from_fixtures(
                fixtures_dir,
                test_indices=test_indices,
                max_tests=max_tests,
                tag_filter=tag_filter,
            )
            if not scenarios:
                scenarios = sorted(p.stem for p in (azure_test_dir / "scenarios").rglob("*.json"))

            location = os.getenv("AZURE_LOCATION", "eastus")
            subscription = os.getenv("AZURE_SUBSCRIPTION", "19e207a9-769d-4afd-b261-10bbed2d43e8")
            extra_env = {
                **os.environ,
                "AZURE_LOCATION": location,
                "AZURE_SUBSCRIPTION": subscription,
            }

            logger.info("Deploying Azure infrastructure: %s", scenarios)
            sp.run(
                ["bash", str(deploy_sh), "--bootstrap"],
                check=True,
                text=True,
                env=extra_env,
            )
            for scenario in scenarios:
                try:
                    sp.run(
                        ["bash", str(deploy_sh), scenario, "Broken"],
                        check=True,
                        text=True,
                        env=extra_env,
                    )
                except sp.CalledProcessError:
                    logger.error("Failed to deploy Azure scenario %s", scenario)

    def _execute_sequential(self, test_cases, session, config, total) -> List[TestResult]:
        """Execute tests one at a time."""
        results = []
        for i, tc in enumerate(test_cases):
            if session.run_id and not should_proceed(session.run_id):
                logger.info("Benchmark stopped by user after %d tests", i)
                break

            update_progress(session.run_id, i, total, tc.get("user_prompt", "")[:200])
            result = execute_single_test(tc, session, config)
            results.append(result)
            update_progress(session.run_id, i + 1, total, "")

        return results

    def _execute_parallel(self, test_cases, session, config, total) -> List[TestResult]:
        """Execute tests in parallel using thread pool."""
        results = []
        completed = 0

        with ThreadPoolExecutor(max_workers=self.parallel_workers) as executor:
            future_to_tc = {}
            for tc in test_cases:
                if session.run_id and not should_proceed(session.run_id):
                    break
                future = executor.submit(execute_single_test, tc, session, config)
                future_to_tc[future] = tc

            for future in as_completed(future_to_tc):
                tc = future_to_tc[future]
                test_id = tc.get("__id__", "unknown")
                try:
                    result = future.result(timeout=1800)  # 30 min max per test
                    results.append(result)
                except TimeoutError:
                    logger.error(
                        "[%s] Worker timed out after 1800s — running after_test cleanup",
                        test_id,
                    )
                    # Force cleanup for timed-out test
                    try:
                        run_after_test(tc)
                    except Exception:
                        logger.warning("[%s] Cleanup after timeout also failed", test_id)
                    _store_error(
                        session.run_id,
                        tc,
                        tc.get("__global_index__", 0),
                        "Test execution timed out after 1800s",
                        "worker_timeout",
                        1800.0,
                    )
                    results.append(
                        TestResult(
                            test_id=test_id,
                            test_index=tc.get("__global_index__", 0),
                            status="error",
                            error="Test execution timed out after 1800s",
                        )
                    )
                except Exception as e:
                    logger.error("[%s] Worker exception: %s", test_id, e)
                    _store_error(
                        session.run_id,
                        tc,
                        tc.get("__global_index__", 0),
                        str(e),
                        "test_error",
                        0.0,
                    )
                    results.append(
                        TestResult(
                            test_id=test_id,
                            test_index=tc.get("__global_index__", 0),
                            status="error",
                            error=str(e),
                        )
                    )

                completed += 1
                update_progress(session.run_id, completed, total, "")

        return results

    def _finalize(self, session: Optional[SessionState], run_id: str, agent_path: Path = None):
        """Finalize session — nuke infrastructure and set phase to report generation.

        Safe to call even if session is None (setup failed).
        """
        logger.info("Orchestrator: finalizing session")

        # Nuke infrastructure only if WE deployed it and nuke not skipped
        deployed = os.getenv("DEPLOY_INFRA", "").lower() in ("1", "true", "yes")
        skip_nuke = os.getenv("SKIP_NUKE", "").lower() in ("1", "true", "yes")
        if agent_path and deployed and not skip_nuke:
            agent_name = session.agent if session else agent_path.name
            try:
                nuke_start = time.time()
                self._nuke_infrastructure(agent_name, agent_path)
                nuke_duration = round(time.time() - nuke_start, 2)
                if session:
                    session.infra_nuke_duration = nuke_duration
                logger.info("Orchestrator: infra nuke took %.1fs", nuke_duration)
            except Exception as e:
                logger.error("Infrastructure nuke failed: %s — manual cleanup may be needed", e)

        # Safety-net: clean up any benchmark-labeled namespaces for this run
        if run_id:
            try:
                subprocess.run(
                    [
                        "kubectl",
                        "delete",
                        "ns",
                        "-l",
                        f"benchmark-run-id={run_id}",
                        "--ignore-not-found",
                        "--wait=false",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
                logger.info("Cleaned up benchmark namespaces for run %s", run_id)
            except Exception as e:
                logger.warning("Benchmark namespace cleanup failed: %s", e)

        if run_id:
            set_phase(run_id, RunPhase.GENERATING_REPORT)

    def _nuke_infrastructure(self, agent_name, agent_path):
        """Tear down cloud infrastructure (AWS/Azure) after tests."""
        import subprocess as sp

        aws_test_dir = agent_path / "aws-agent-test"
        azure_test_dir = agent_path / "azure-agent-test"

        if aws_test_dir.exists() and (aws_test_dir / "scripts" / "nuke.sh").exists():
            nuke_sh = aws_test_dir / "scripts" / "nuke.sh"
            region = os.getenv("AWS_REGION", "us-east-1")
            logger.info("Nuking AWS infrastructure")
            try:
                sp.run(
                    ["bash", str(nuke_sh)],
                    input="y\n",
                    check=True,
                    text=True,
                    env={**os.environ, "AWS_REGION": region},
                )
                logger.info("AWS infrastructure nuke complete")
            except sp.CalledProcessError as e:
                logger.error(
                    "AWS nuke failed (exit %s); manual cleanup may be needed",
                    e.returncode,
                )

        elif azure_test_dir.exists() and (azure_test_dir / "scripts" / "deploy.sh").exists():
            deploy_sh = azure_test_dir / "scripts" / "deploy.sh"
            location = os.getenv("AZURE_LOCATION", "eastus")
            subscription = os.getenv("AZURE_SUBSCRIPTION", "19e207a9-769d-4afd-b261-10bbed2d43e8")
            extra_env = {
                **os.environ,
                "AZURE_LOCATION": location,
                "AZURE_SUBSCRIPTION": subscription,
            }

            # Resolve scenarios to delete
            from llm.agents.common.lifecycle import resolve_scenarios_from_fixtures

            fixtures_dir = agent_path / "fixtures"
            scenarios = resolve_scenarios_from_fixtures(fixtures_dir)
            if not scenarios:
                scenarios = sorted(p.stem for p in (azure_test_dir / "scenarios").rglob("*.json"))

            logger.info("Deleting Azure infrastructure")
            for scenario in scenarios:
                try:
                    sp.run(
                        ["bash", str(deploy_sh), "--delete", scenario],
                        check=False,
                        text=True,
                        env=extra_env,
                    )
                except Exception as e:
                    logger.error("Error deleting Azure scenario %s: %s", scenario, e)
            try:
                sp.run(
                    ["bash", str(deploy_sh), "--delete-bootstrap"],
                    check=False,
                    text=True,
                    env=extra_env,
                )
            except Exception as e:
                logger.error("Error deleting Azure bootstrap: %s", e)
