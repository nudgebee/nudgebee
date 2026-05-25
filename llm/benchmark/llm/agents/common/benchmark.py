"""Unified benchmark test file for ALL agents.

Each agent has a config.yaml + fixtures/ directory. This single test file
handles discovery, execution, RAGAS evaluation, and DB storage for all agents.

Usage:
    BENCHMARK_AGENT_DIR=/path/to/agents/kubectl pytest llm/agents/common/benchmark.py -v

The controller sets BENCHMARK_AGENT_DIR to the agent directory before running pytest.
"""

import importlib
import logging
import math
import os
import time
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

import pytest
import yaml


# ---------------------------------------------------------------------------
# Agent-specific conftest.py loading is handled by llm/agents/conftest.py
# (pytest_configure hooks only work in conftest.py files, not test modules)
# ---------------------------------------------------------------------------

from datasets import Dataset
from dotenv import load_dotenv
from ragas import evaluate
from ragas.llms import LangchainLLMWrapper

from benchmark_server.common.llm import get_llm, get_embeddings

from benchmark_server.utils.llm_client import (
    ErrorCategory,
    LLMResult,
    call_llm,
    extract_conversation_id,
    extract_response_text,
    extract_tool_command,
)
from benchmark_server.utils.run_manager import (
    RunPhase,
    add_error,
    set_phase,
    should_proceed,
    store_test_result,
    update_progress,
)

from .fixtures import find_test_cases, load_test_case
from .lifecycle import run_after_test, run_before_test
from .metrics import (
    fetch_tool_command_from_db,
    get_planner_response,
    get_token_metrics,
    get_tool_names,
)

load_dotenv()

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Config loading
# ---------------------------------------------------------------------------


def _get_agent_dir() -> Path:
    """Get agent directory from BENCHMARK_AGENT_DIR env var."""
    agent_dir = os.getenv("BENCHMARK_AGENT_DIR")
    if not agent_dir:
        raise RuntimeError("BENCHMARK_AGENT_DIR env var not set")
    return Path(agent_dir)


def _load_config(agent_dir: Path) -> Dict[str, Any]:
    """Load config.yaml from agent directory."""
    config_path = agent_dir / "config.yaml"
    if not config_path.exists():
        raise FileNotFoundError(f"config.yaml not found in {agent_dir}")
    with open(config_path) as f:
        return yaml.safe_load(f)


def _import_callable(dotted_path: str) -> Callable:
    """Import a function from a dotted module path like 'pkg.mod.func'."""
    module_path, func_name = dotted_path.rsplit(".", 1)
    module = importlib.import_module(module_path)
    return getattr(module, func_name)


def _resolve_hooks(config: Dict[str, Any]) -> Dict[str, Optional[Callable]]:
    """Resolve callable hooks from config dotted paths."""
    hooks = {}
    for key in ("before_query", "after_query", "result_enricher", "answer_extractor"):
        path = config.get(key)
        if path:
            try:
                hooks[key] = _import_callable(path)
            except Exception as e:
                logger.error("Failed to import %s=%s: %s", key, path, e)
                hooks[key] = None
        else:
            hooks[key] = None
    return hooks


# ---------------------------------------------------------------------------
# Answer extraction (same logic as runner.py)
# ---------------------------------------------------------------------------


def _response_text_extractor(
    response_data: dict,
    account_id: str,
    tool_names: List[str],
    db_tool_name: Optional[str] = None,
) -> List[str]:
    """Extract the final response text (for rca/non-command tests)."""
    if not response_data:
        return []
    resp = extract_response_text(response_data)
    if resp:
        if isinstance(resp, str):
            return [resp]
        if isinstance(resp, list):
            return [str(r) for r in resp]
        return [str(resp)]
    return []


def _default_answer_extractor(
    response_data: dict,
    account_id: str,
    tool_names: List[str],
    db_tool_name: Optional[str] = None,
) -> List[str]:
    """Extract answer: try DB fetch, then tool command, then response text."""
    if not response_data:
        return []

    conversation_id = extract_conversation_id(response_data)

    if db_tool_name and conversation_id:
        command = fetch_tool_command_from_db(conversation_id, account_id, db_tool_name)
        if command:
            return [command]

    command = extract_tool_command(response_data, tool_names)
    if command:
        return [command]

    resp = extract_response_text(response_data)
    if resp:
        return [resp] if isinstance(resp, str) else resp

    return []


# ---------------------------------------------------------------------------
# LLM query execution
# ---------------------------------------------------------------------------


def _execute_query(
    query: str,
    agent: str,
    account_id: str,
    tenant_id: str,
    user_id: str,
    llm_config: Optional[Dict[str, Any]],
    extractor: Callable,
    tool_names: List[str],
    db_tool_name: Optional[str],
    prefix_query: bool = True,
) -> tuple:
    """Execute a single LLM query and extract the answer."""
    final_query = f"@{agent} {query}" if prefix_query else query
    start_time = time.time()
    llm_result = call_llm(
        final_query, account_id, tenant_id, user_id, config=llm_config
    )
    elapsed = round(time.time() - start_time, 2)
    docs = extractor(llm_result.data, account_id, tool_names, db_tool_name)
    return docs, llm_result, elapsed


# ---------------------------------------------------------------------------
# RAGAS helpers
# ---------------------------------------------------------------------------


def _flatten_ground_truth(gt) -> str:
    if isinstance(gt, str):
        return gt
    if isinstance(gt, (list, tuple)):
        parts = []
        for item in gt:
            if isinstance(item, (list, tuple)):
                parts.append(" ".join(str(x) for x in item))
            else:
                parts.append(str(item))
        return " ".join(parts)
    return str(gt)


def _evaluate_planner(execution_trace: str, query: str, llm):
    """Evaluate planner/execution quality. Returns (score_0_100, reason_str).

    Args:
        execution_trace: Full execution trace (plan + agent calls + tool calls + stats).
                         Falls back to planner response text if trace is not available.
    """
    try:
        from .orchestrator_metric import get_orchestrator_quality_metric
        from .ragas_evaluation import _call_rubric_prompt

        metric = get_orchestrator_quality_metric(llm)
        score, reason = _call_rubric_prompt(metric, query, execution_trace, query)
        return score, reason
    except Exception as e:
        logger.warning("Planner evaluation failed: %s", e)
        return 0.0, ""


# ---------------------------------------------------------------------------
# Pytest fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session")
def agent_config():
    """Load agent config.yaml and resolve hooks."""
    agent_dir = _get_agent_dir()
    config = _load_config(agent_dir)
    config["_agent_dir"] = str(agent_dir)
    config["_fixtures_dir"] = str(agent_dir / "fixtures")
    config["_hooks"] = _resolve_hooks(config)
    return config


@pytest.fixture(scope="session")
def benchmark_session(agent_config):
    """Session-scoped fixture for benchmark lifecycle.

    Manages: run_id tracking, RAGAS LLM/embeddings (initialized once),
    result collection, and final report generation.
    """
    run_id = os.getenv("BENCHMARK_RUN_ID", "")
    agent = agent_config["agent"]

    # Initialize RAGAS LLM & embeddings once for the entire session
    base_llm = get_llm()
    llm = LangchainLLMWrapper(base_llm)
    embeddings = get_embeddings()

    session = {
        "run_id": run_id,
        "agent": agent,
        "config": agent_config,
        "results": [],
        "llm": llm,
        "embeddings": embeddings,
    }

    # Set phase to RUNNING_TESTS
    if run_id:
        set_phase(run_id, RunPhase.RUNNING_TESTS)

    yield session

    # --- Teardown: report generation ---
    _finalize_session(session)


@pytest.fixture(scope="session")
def total_test_count(agent_config):
    """Compute the total number of test cases for progress tracking."""
    fixtures_dir = Path(agent_config["_fixtures_dir"])
    max_tests_str = os.getenv("MAX_TESTS")
    max_tests = int(max_tests_str) if max_tests_str else None
    test_indices = os.getenv("TEST_INDICES")
    skip_indices = os.getenv("SKIP_INDICES")
    tag_filter = os.getenv("TAG_FILTER")
    cases = find_test_cases(
        fixtures_dir,
        max_tests=max_tests,
        test_indices=test_indices,
        skip_indices=skip_indices,
        tag_filter=tag_filter,
    )
    return len(cases)


@pytest.fixture
def test_case_data(request, agent_config):
    """Per-test fixture: loads test case, runs before/after hooks."""
    test_case_path, test_case_id, global_index = request.param
    test_case = load_test_case(test_case_path)
    test_case["__global_index__"] = global_index
    test_case_dir = os.path.dirname(test_case_path)

    try:
        if test_case.get("before_test"):
            result = run_before_test(test_case, cwd=test_case_dir)
            if not result.success:
                pytest.fail(f"Setup failed for {test_case_id}: {result.error_details}")
        yield test_case
    finally:
        if test_case.get("_skip_cleanup"):
            logger.info(
                "[%s] Skipping after_test cleanup (test is waiting for followup)",
                test_case_id,
            )
        elif test_case.get("after_test"):
            result = run_after_test(test_case, cwd=test_case_dir)
            if not result.success:
                logger.warning(
                    "Cleanup failed for %s: %s", test_case_id, result.error_details
                )


# ---------------------------------------------------------------------------
# Dynamic test parametrization
# ---------------------------------------------------------------------------


def pytest_generate_tests(metafunc):
    """Generate test cases dynamically from fixtures/ directory."""
    if "test_case_data" not in metafunc.fixturenames:
        return

    agent_dir = _get_agent_dir()
    fixtures_dir = agent_dir / "fixtures"

    max_tests_str = os.getenv("MAX_TESTS")
    max_tests = int(max_tests_str) if max_tests_str else None
    test_indices = os.getenv("TEST_INDICES")
    skip_indices = os.getenv("SKIP_INDICES")
    tag_filter = os.getenv("TAG_FILTER")

    test_cases = find_test_cases(
        fixtures_dir,
        max_tests=max_tests,
        test_indices=test_indices,
        skip_indices=skip_indices,
        tag_filter=tag_filter,
    )
    metafunc.parametrize(
        "test_case_data",
        test_cases,
        indirect=True,
        ids=lambda x: x[1],  # x is (file_path, test_id, global_index)
    )


# ---------------------------------------------------------------------------
# The single test function
# ---------------------------------------------------------------------------


@pytest.mark.benchmark
def test_agent_benchmark(
    test_case_data: Dict[str, Any],
    benchmark_session: Dict[str, Any],
    agent_config: Dict[str, Any],
    total_test_count: int,
) -> None:
    """Run a single agent test case.

    Calls LLM, extracts answer, collects metrics, stores raw result to DB.
    RAGAS evaluation happens in batch at session teardown.
    """
    start_time = time.time()
    test_case = test_case_data
    test_case_name = test_case.get("__id__", "unknown")
    global_index = test_case.get("__global_index__", 0)
    run_id = benchmark_session["run_id"]
    results = benchmark_session["results"]
    config = agent_config

    # Check lifecycle state
    if run_id and not should_proceed(run_id):
        pytest.skip("Benchmark stopped by user")

    if run_id:
        update_progress(
            run_id,
            len(results),
            total_test_count,
            test_case.get("user_prompt", "")[:200],
        )

    # Insert a "running" row so the UI shows the test immediately
    if run_id:
        store_test_result(
            run_id,
            {
                "test_id": test_case_name,
                "test_index": global_index,
                "status": "running",
                "query": test_case.get("user_prompt", "")[:200],
                "expected_answer": "\n".join(test_case.get("expected_output", [])),
                "tags": test_case.get("tags", []),
            },
        )

    account_id = os.getenv("ACCOUNT_ID", "")
    tenant_id = os.getenv("TENANT_ID", "")
    user_id = os.getenv("USER_ID", "")
    tool_config_str = os.getenv("TOOL_CONFIG", "")

    agent = test_case.get("agent", config["agent"])
    tool_names = config.get("tool_names", [])
    db_tool_name = config.get("db_tool_name")
    prefix_query = config.get("prefix_query", True)
    max_retries = config.get("max_retries", 2)
    hooks = config.get("_hooks", {})

    # Build LLM config
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

    extractor = hooks.get("answer_extractor") or _default_answer_extractor

    # For non-command tests (e.g. rca), use response text instead of command extraction
    test_type = test_case.get("type", "command")
    if test_type != "command" and extractor is _default_answer_extractor:
        extractor = _response_text_extractor

    # Run before_query hook (Python callback, e.g. feature flags)
    before_query = hooks.get("before_query")
    after_query = hooks.get("after_query")
    if before_query:
        try:
            before_query(test_case)
        except Exception as e:
            logger.warning("before_query failed for %s: %s", test_case_name, e)
            _store_failed_result(
                run_id,
                results,
                test_case,
                "SETUP FAILURE",
                "setup_failed",
                f"before_query hook failed: {e}",
                time.time() - start_time,
            )
            if after_query:
                try:
                    after_query(test_case)
                except Exception:
                    pass
            return

    user_prompt = test_case["user_prompt"]
    logger.info("Testing [%s]: %s...", test_case_name, user_prompt[:100])

    # Execute with retries
    docs, llm_result, elapsed = _execute_query(
        user_prompt,
        agent,
        account_id,
        tenant_id,
        user_id,
        llm_config,
        extractor,
        tool_names,
        db_tool_name,
        prefix_query,
    )

    # Handle followup (WAITING status) — do not retry, store and return
    if llm_result.status == "WAITING":
        convo_id = extract_conversation_id(llm_result.data) if llm_result.data else None
        if run_id:
            ground_truths = test_case["expected_output"]
            if isinstance(ground_truths, str):
                ground_truths = [ground_truths]
            store_test_result(
                run_id,
                {
                    "test_id": test_case.get("__id__", test_case_name),
                    "test_index": global_index,
                    "status": "waiting",
                    "conversation_id": convo_id or "",
                    "query": user_prompt,
                    "expected_answer": _flatten_ground_truth(ground_truths),
                    "duration_seconds": round(time.time() - start_time, 2),
                    "followup_request": llm_result.followup_request,
                    "tags": test_case.get("tags", []),
                },
            )
        # Signal the fixture to skip after_test cleanup — infra must stay up
        # for the followup response to work.
        test_case["_skip_cleanup"] = True
        fq = llm_result.followup_request
        logger.info(
            "[%s] WAITING for followup (%.1fs): %s (followup_request=%s)",
            test_case_name,
            time.time() - start_time,
            fq.get("question", "") if fq else "no followup data extracted",
            "present" if fq else "None",
        )
        return

    error_category = None
    error_message = ""

    if not docs:
        error_category = llm_result.error_category
        error_message = llm_result.error_message or ""

        # Smart retry for transient failures
        if max_retries > 0 and (
            not error_category or error_category in ErrorCategory.RETRYABLE
        ):
            for attempt in range(1, max_retries + 1):
                delay = 2.0 * (2 ** (attempt - 1))
                time.sleep(delay)
                logger.info("Retry %d for [%s]", attempt, test_case_name)

                docs, llm_result, elapsed = _execute_query(
                    user_prompt,
                    agent,
                    account_id,
                    tenant_id,
                    user_id,
                    llm_config,
                    extractor,
                    tool_names,
                    db_tool_name,
                    prefix_query,
                )
                if docs:
                    error_category = None
                    error_message = ""
                    break
                error_category = llm_result.error_category
                error_message = llm_result.error_message or ""
                if error_category and error_category not in ErrorCategory.RETRYABLE:
                    break

    # Run after_query hook
    if after_query:
        try:
            after_query(test_case)
        except Exception as e:
            logger.warning("after_query failed for %s: %s", test_case_name, e)

    duration = time.time() - start_time
    answer = " ".join(str(d) for d in docs) if docs else "SYSTEM FAILURE"
    test_failed = not docs

    ground_truths = test_case["expected_output"]
    if isinstance(ground_truths, str):
        ground_truths = [ground_truths]
    ground_truth_str = _flatten_ground_truth(ground_truths)

    # Token/tool metrics
    # conversation_id = DB convo ID (for planner/tools queries)
    # session_id = LLM session ID (for token usage metrics API)
    convo_id = None
    session_id = None
    if llm_result.data:
        convo_id = extract_conversation_id(llm_result.data)
        session_id = llm_result.data.get("data", {}).get("session_id")

    token_metrics = get_token_metrics(session_id, account_id, tenant_id, user_id)
    tool_names_data = get_tool_names(convo_id=convo_id)
    planner_resp = get_planner_response(convo_id, account_id)

    result_data = {
        "test_id": test_case.get("__id__", test_case_name),
        "test_index": global_index,  # stable index in full fixture list
        "tags": test_case.get("tags", []),
        "query": user_prompt,
        "answer": answer,
        "ground_truth": ground_truth_str,
        "duration_seconds": round(duration, 2),
        "status": "failed" if test_failed else "success",
        "query_type": test_case.get("type", "command"),
        "session_id": session_id,
        "convo_id": convo_id,
        "planner_response": planner_resp,
    }

    if test_failed:
        result_data["error_category"] = error_category or "unknown"
        result_data["error_message"] = error_message

    if token_metrics:
        result_data.update(token_metrics)

    if tool_names_data:
        result_data["tool_names"] = tool_names_data.get("tool_names", [])
        result_data["tool_names_failed"] = tool_names_data.get("tool_names_failed", [])

    # --- Live RAGAS evaluation (per-test, immediately after execution) ---
    sim_score = 0.0
    rel_score = 0.0
    planner_score = 0.0

    if not test_failed:
        llm = benchmark_session["llm"]
        embeddings = benchmark_session["embeddings"]

        from .ragas_evaluation import evaluate_single

        eval_result = evaluate_single(
            user_prompt, answer, ground_truth_str, llm, embeddings
        )
        sim_score = eval_result.similarity
        rel_score = eval_result.quality

        # Planner/execution quality
        from .metrics import get_execution_trace

        convo_id = result_data.get("convo_id")
        trace = get_execution_trace(convo_id, account_id) if convo_id else ""
        if not trace:
            # Fallback to planner response if trace unavailable
            trace = result_data.get("planner_response", "")
        if trace:
            planner_score, planner_reason = _evaluate_planner(trace, user_prompt, llm)
            if planner_reason:
                eval_result.reason += f"\n[Planner] {planner_reason}"
        else:
            logger.warning(
                "[%s] No execution trace or planner response found (convo_id=%s, account_id=%s)",
                test_case_name,
                convo_id,
                account_id,
            )

    score_reason = eval_result.reason.strip() if not test_failed else ""
    result_data["answer_similarity"] = sim_score
    result_data["answer_relevancy"] = rel_score
    result_data["planner_relevancy"] = planner_score
    result_data["score_reason"] = score_reason
    results.append(result_data)

    # Store result to DB with live scores
    if run_id:
        store_test_result(
            run_id,
            {
                "test_id": result_data.get("test_id", ""),
                "test_index": global_index,
                "status": "pass" if not test_failed else "fail",
                "conversation_id": result_data.get("convo_id", ""),
                "polling_conversation_id": result_data.get("session_id", ""),
                "query": user_prompt,
                "expected_answer": ground_truth_str,
                "actual_answer": answer,
                "answer_similarity": sim_score,
                "answer_relevancy": rel_score,
                "planner_relevancy": planner_score,
                "score_reason": score_reason,
                "execution_trace": trace if not test_failed else "",
                "duration_seconds": result_data.get("duration_seconds", 0.0),
                "cost": result_data.get("cost", 0.0),
                "total_tokens": result_data.get("total_tokens", 0),
                "input_tokens": result_data.get("input_tokens", 0),
                "output_tokens": result_data.get("completion_tokens", 0),
                "cache_read_tokens": result_data.get("cached_input_tokens", 0),
                "tool_calls_total": result_data.get("total_tool_calls", 0),
                "tool_calls_successful": result_data.get("successful_tool_calls", 0),
                "tool_names": result_data.get("tool_names", []),
                "model_names": result_data.get("model_names", []),
                "model_providers": result_data.get("model_providers", []),
                "tags": test_case.get("tags", []),
                "error_message": result_data.get("error_message", ""),
                "error_category": result_data.get("error_category", ""),
            },
        )

    if not test_failed:
        logger.info(
            "[%s] SUCCESS (%.1fs) sim=%.1f rel=%.1f planner=%.1f",
            test_case_name,
            duration,
            sim_score,
            rel_score,
            planner_score,
        )
    else:
        logger.info(
            "[%s] FAILED [%s] (%.1fs)",
            test_case_name,
            error_category,
            duration,
        )


# ---------------------------------------------------------------------------
# Session finalization: enrichers + report generation
# ---------------------------------------------------------------------------


def _finalize_session(session: Dict[str, Any]):
    """Run agent-specific enrichers and trigger report generation.

    RAGAS scores are already computed per-test during execution.
    """
    run_id = session["run_id"]
    results = session["results"]
    config = session["config"]
    hooks = config.get("_hooks", {})
    llm = session.get("llm")

    if not results:
        logger.warning("No test results to finalize")
        return

    # Agent-specific enrichment
    result_enricher = hooks.get("result_enricher")
    if result_enricher:
        for r in results:
            if r.get("status") == "success":
                try:
                    fixtures_dir = Path(config["_fixtures_dir"])
                    tc_path = fixtures_dir / r["test_id"] / "test_case.yaml"
                    if tc_path.exists():
                        tc = load_test_case(str(tc_path))
                        result_enricher(r, tc, llm)
                except Exception as e:
                    logger.warning("Enricher failed for %s: %s", r.get("test_id"), e)

    # Mark report generation phase
    if run_id:
        set_phase(run_id, RunPhase.GENERATING_REPORT)

    evaluated = sum(
        1
        for r in results
        if r.get("answer_similarity", 0) > 0 or r.get("answer_relevancy", 0) > 0
    )
    logger.info(
        "Benchmark finalized: %d results (%d evaluated by RAGAS)",
        len(results),
        evaluated,
    )


def _store_failed_result(
    run_id: str,
    results: list,
    test_case: dict,
    answer: str,
    error_category: str,
    error_message: str,
    duration: float,
):
    """Store a failed result (setup failure, etc.)."""
    ground_truths = test_case.get("expected_output", [])
    if isinstance(ground_truths, str):
        ground_truths = [ground_truths]

    global_index = test_case.get("__global_index__", 0)
    result_data = {
        "test_id": test_case.get("__id__", "unknown"),
        "test_index": global_index,
        "tags": test_case.get("tags", []),
        "query": test_case.get("user_prompt", ""),
        "answer": answer,
        "ground_truth": _flatten_ground_truth(ground_truths),
        "duration_seconds": round(duration, 2),
        "status": "failed",
        "error_category": error_category,
        "error_message": error_message,
    }
    results.append(result_data)

    if run_id:
        store_test_result(
            run_id,
            {
                "test_id": result_data["test_id"],
                "test_index": global_index,
                "status": "fail",
                "query": result_data["query"],
                "expected_answer": result_data["ground_truth"],
                "actual_answer": answer,
                "answer_similarity": 0.0,
                "answer_relevancy": 0.0,
                "planner_relevancy": 0.0,
                "duration_seconds": result_data["duration_seconds"],
                "error_message": error_message,
                "error_category": error_category,
                "tags": test_case.get("tags", []),
            },
        )
