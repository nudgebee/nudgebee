"""Shared test runner logic for agent benchmarks.

Provides the core execution loop: call LLM, extract answer, retry failures,
run RAGAS evaluation, collect token/tool metrics, and generate the unified report.

Integrates with lifecycle management:
  - Phase tracking via run_manager (RUNNING_TESTS → EVALUATING → GENERATING_REPORT)
  - before_test/after_test hooks per fixture
  - Test results summary stored in run state

Each agent benchmark imports this module and provides:
  - agent name
  - tool names for answer extraction
  - fixtures directory
  - optional: custom answer extractor, DB-based command fetch
"""

import logging
import math
import os
import time
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

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
from .metrics import fetch_tool_command_from_db, get_execution_trace, get_planner_response, get_token_metrics

load_dotenv()

logger = logging.getLogger(__name__)


def default_answer_extractor(
    response_data: dict,
    account_id: str,
    tool_names: List[str],
    db_tool_name: Optional[str] = None,
) -> List[str]:
    """Default answer extraction: try DB fetch, then tool command, then response text.

    Args:
        response_data: Raw response from call_llm()
        account_id: Account ID for DB queries
        tool_names: Tool names to look for in agent_step_response
        db_tool_name: If set, try fetching the command from DB first
    """
    if not response_data:
        return []

    conversation_id = extract_conversation_id(response_data)

    # Try DB fetch first (for agents like kubectl, promql that store commands in DB)
    if db_tool_name and conversation_id:
        command = fetch_tool_command_from_db(conversation_id, account_id, db_tool_name)
        if command:
            return [command]

    # Try extracting tool command from response
    command = extract_tool_command(response_data, tool_names)
    if command:
        return [command]

    # Fall back to response text
    resp = extract_response_text(response_data)
    if resp:
        return [resp] if isinstance(resp, str) else resp

    return []


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
) -> tuple:
    """Execute a single LLM query and extract the answer.

    Returns:
        (docs, llm_result, elapsed) where docs is the extracted answer list,
        llm_result is the structured LLMResult, and elapsed is duration in seconds.
    """
    prefixed_query = f"@{agent} {query}"
    start_time = time.time()
    llm_result = call_llm(
        prefixed_query, account_id, tenant_id, user_id, config=llm_config
    )
    elapsed = round(time.time() - start_time, 2)

    # Extract answer from the response data (even failed conversations may have data)
    docs = extractor(llm_result.data, account_id, tool_names, db_tool_name)
    return docs, llm_result, elapsed


def run_benchmark(  # noqa: C901
    agent: str,
    tool_names: List[str],
    fixtures_dir: Path,
    db_tool_name: Optional[str] = None,
    answer_extractor: Optional[Callable] = None,
    extra_tool_confirmations: Optional[Dict[str, str]] = None,
    before_query: Optional[Callable] = None,
    after_query: Optional[Callable] = None,
    result_enricher: Optional[Callable] = None,
    max_tests: Optional[int] = None,
    test_indices: Optional[str] = None,
    max_retries: int = 2,
    retry_delay: float = 2.0,
    **_kwargs,
) -> bool:
    """Run a complete agent benchmark. Results are stored to DB progressively.

    Args:
        agent: Agent name (e.g. "kubectl", "promql")
        tool_names: Tool names for answer extraction (e.g. ["kubectl_execute"])
        fixtures_dir: Path to fixtures/ directory with test_case.yaml files
        db_tool_name: Tool name for DB-based command fetch (optional)
        answer_extractor: Custom function to extract answers from LLM response.
            Signature: (response_data, account_id, tool_names, db_tool_name) -> List[str]
        extra_tool_confirmations: Additional tool confirmations beyond tool_names
            (e.g. {"aws_observability": "yes"} for debug agents)
        before_query: Optional callback called before each query (e.g. enable feature flag).
            Signature: (test_case) -> None. Raise to skip the test.
        after_query: Optional callback called after each query (e.g. disable feature flag).
            Signature: (test_case) -> None.
        result_enricher: Optional callback to add extra metrics per result.
            Signature: (result_dict, test_case, llm) -> None
            Mutates result_dict in place (e.g. RCA rubric scoring, signal analysis).
        max_tests: Limit number of tests to run
        test_indices: Specific indices to run (e.g. "0,2,5-7")
        max_retries: Number of retry attempts for failed queries
        retry_delay: Base delay between retry rounds (doubles each attempt)

    Returns:
        True if benchmark completed with results, False otherwise
    """
    account_id = os.getenv("ACCOUNT_ID", "")
    tenant_id = os.getenv("TENANT_ID", "")
    user_id = os.getenv("USER_ID", "")
    run_id = os.getenv("BENCHMARK_RUN_ID", "")
    tool_config = os.getenv("TOOL_CONFIG", "")

    # Build LLM config: tool_configs maps each tool to the selected config,
    # tool_confirmations auto-approves tool execution during benchmarks
    llm_config: Optional[Dict[str, Any]] = None
    if tool_config and tool_names:
        confirmations = {name: "yes" for name in tool_names}
        if extra_tool_confirmations:
            confirmations.update(extra_tool_confirmations)
        llm_config = {
            "tool_configs": {name: tool_config for name in tool_names},
            "tool_confirmations": confirmations,
        }

    extractor = answer_extractor or default_answer_extractor

    # Discover test cases (skip already-completed on resume)
    skip_indices = os.getenv("SKIP_INDICES")
    tag_filter = os.getenv("TAG_FILTER")
    test_cases = find_test_cases(
        fixtures_dir, max_tests=max_tests, test_indices=test_indices,
        skip_indices=skip_indices, tag_filter=tag_filter,
    )
    if not test_cases:
        logger.warning("No test cases found in %s", fixtures_dir)
        return False

    logger.info("Running %d test cases for agent '%s'", len(test_cases), agent)

    # Load all test cases
    loaded_cases = []
    global_indices = []
    for file_path, test_id, global_index in test_cases:
        tc = load_test_case(file_path)
        loaded_cases.append(tc)
        global_indices.append(global_index)

    queries = [tc["user_prompt"] for tc in loaded_cases]
    ground_truths = [tc["expected_output"] for tc in loaded_cases]
    tags_list = [tc.get("tags", []) for tc in loaded_cases]

    # --- Phase: RUNNING_TESTS ---
    set_phase(run_id, RunPhase.RUNNING_TESTS)

    answers = [""] * len(queries)
    convo_ids = [None] * len(queries)
    session_ids = [None] * len(queries)
    durations = [0.0] * len(queries)
    error_categories = [None] * len(queries)  # ErrorCategory for each query
    error_messages = [""] * len(queries)  # Error detail for each query
    setup_results = [None] * len(queries)  # Track before/after_test results
    failed_indices = list(range(len(queries)))
    stopped = False

    for i, query in enumerate(queries):
        if run_id and not should_proceed(run_id):
            logger.info("Benchmark stopped by user at query %d", i)
            stopped = True
            break
        if run_id:
            update_progress(run_id, i + 1, len(queries), query)

        logger.info("Query %d/%d: %s", i + 1, len(queries), query[:100])

        # Run before_test hook if defined (shell commands from YAML)
        tc = loaded_cases[i]
        fixture_dir = str(Path(tc["__path__"]).parent)
        if tc.get("before_test"):
            setup_result = run_before_test(tc, cwd=fixture_dir)
            setup_results[i] = "setup_ok" if setup_result.success else "setup_failed"
            if not setup_result.success:
                logger.warning("Query %d skipped — before_test failed", i + 1)
                add_error(run_id, f"before_test failed for test {tc.get('__id__')}")
                answers[i] = "SETUP FAILURE"
                error_categories[i] = "setup_failed"
                error_messages[i] = (
                    f"before_test hook failed: {setup_result.error_details or ''}"
                )
                # Still run after_test for cleanup
                run_after_test(tc, cwd=fixture_dir)
                continue

        # Run before_query callback if provided (Python hook, e.g. feature flags)
        if before_query:
            try:
                before_query(tc)
            except Exception as e:
                logger.warning("before_query failed for test %d: %s", i + 1, e)
                answers[i] = "SETUP FAILURE"
                error_categories[i] = "setup_failed"
                error_messages[i] = f"before_query hook failed: {e}"
                if after_query:
                    try:
                        after_query(tc)
                    except Exception:
                        pass
                continue

        test_agent = tc.get("agent", agent)
        docs, llm_result, elapsed = _execute_query(
            query,
            test_agent,
            account_id,
            tenant_id,
            user_id,
            llm_config,
            extractor,
            tool_names,
            db_tool_name,
        )
        durations[i] = elapsed
        convo_ids[i] = llm_result.conversation_id
        if llm_result.data:
            session_ids[i] = llm_result.data.get("data", {}).get("session_id")

        if docs:
            answers[i] = " ".join(str(d) for d in docs)
            failed_indices.remove(i)
            logger.info("Query %d succeeded (%.1fs)", i + 1, elapsed)
        else:
            error_categories[i] = llm_result.error_category
            error_messages[i] = llm_result.error_message or ""
            logger.warning(
                "Query %d failed (%.1fs) [%s]: %s",
                i + 1,
                elapsed,
                llm_result.error_category or "extraction_failed",
                (llm_result.error_message or "")[:200],
            )

        # Run after_test / after_query hooks
        if tc.get("after_test"):
            run_after_test(tc, cwd=fixture_dir)
        if after_query:
            try:
                after_query(tc)
            except Exception as e:
                logger.warning("after_query failed for test %d: %s", i + 1, e)

    # --- Smart retry: only retry transient failures ---
    setup_failed = {
        i for i in range(len(queries)) if setup_results[i] == "setup_failed"
    }
    # Only retry queries with retryable error categories (network, timeout, server_error)
    # or where extraction failed (error_category is None but no docs)
    non_retryable = setup_failed | {
        i
        for i in failed_indices
        if error_categories[i]
        and not LLMResult(error_category=error_categories[i]).retryable
    }
    retryable = [i for i in failed_indices if i not in non_retryable]

    if non_retryable - setup_failed:
        skipped = non_retryable - setup_failed
        logger.info(
            "Skipping retry for %d queries with non-retryable errors: %s",
            len(skipped),
            {i: error_categories[i] for i in skipped},
        )

    for attempt in range(1, max_retries + 1):
        if not retryable or stopped:
            break
        if run_id and not should_proceed(run_id):
            logger.info("Benchmark stopped during retries")
            break

        logger.info("Retry attempt %d for %d failed queries", attempt, len(retryable))
        for idx in retryable[:]:
            tc = loaded_cases[idx]
            fixture_dir = str(Path(tc["__path__"]).parent)

            # Re-run before_test if it exists
            if tc.get("before_test"):
                retry_setup = run_before_test(tc, cwd=fixture_dir)
                if not retry_setup.success:
                    continue

            test_agent = tc.get("agent", agent)
            docs, llm_result, elapsed = _execute_query(
                queries[idx],
                test_agent,
                account_id,
                tenant_id,
                user_id,
                llm_config,
                extractor,
                tool_names,
                db_tool_name,
            )
            durations[idx] = elapsed
            convo_ids[idx] = llm_result.conversation_id
            if llm_result.data:
                session_ids[idx] = llm_result.data.get("data", {}).get("session_id")

            if docs:
                answers[idx] = " ".join(str(d) for d in docs)
                failed_indices.remove(idx)
                retryable.remove(idx)
                error_categories[idx] = None
                error_messages[idx] = ""
                logger.info("Retry success for index %d", idx)
            else:
                error_categories[idx] = llm_result.error_category
                error_messages[idx] = llm_result.error_message or ""
                # If retry revealed a non-retryable error, stop retrying this query
                if llm_result.error_category and not llm_result.retryable:
                    retryable.remove(idx)
                    logger.info(
                        "Index %d now non-retryable [%s], removing from retry list",
                        idx,
                        llm_result.error_category,
                    )

            # Run after_test if it exists
            if tc.get("after_test"):
                run_after_test(tc, cwd=fixture_dir)

        if retryable:
            # Exponential backoff: 2s, 4s, 8s ...
            delay = retry_delay * (2 ** (attempt - 1))
            time.sleep(delay)

    if failed_indices:
        logger.error("Queries failed after retries: %s", failed_indices)

    # Mark remaining failures
    for idx in failed_indices:
        if answers[idx] != "SETUP FAILURE":
            answers[idx] = "SYSTEM FAILURE"
            if not error_messages[idx]:
                error_messages[idx] = "No response after retries"

    # --- Phase: EVALUATING ---
    set_phase(run_id, RunPhase.EVALUATING)

    eval_indices = [i for i in range(len(queries)) if i not in failed_indices]

    if not eval_indices:
        logger.error("All queries failed. Skipping evaluation.")
        add_error(run_id, "All queries failed — no report generated")
        return False

    eval_queries = [queries[i] for i in eval_indices]
    eval_answers = [answers[i] for i in eval_indices]
    eval_references = [_flatten_ground_truth(ground_truths[i]) for i in eval_indices]

    base_llm = get_llm()
    llm = LangchainLLMWrapper(base_llm)
    embeddings = get_embeddings()

    from .ragas_evaluation import evaluate_batch

    logger.info("Running RAGAS evaluation on %d queries...", len(eval_queries))
    batch_scores = evaluate_batch(eval_queries, eval_answers, eval_references, llm, embeddings)

    # --- Collect per-query results ---
    results: List[Dict[str, Any]] = []
    eval_idx = 0

    for i in range(len(queries)):
        tc = loaded_cases[i]
        result: Dict[str, Any] = {
            "query": queries[i],
            "answer": answers[i],
            "ground_truth": _flatten_ground_truth(ground_truths[i]),
            "tags": tags_list[i],
            "duration_seconds": durations[i],
            "test_id": tc.get("__id__", ""),
            "query_type": tc.get("type", "command"),
            "conversation_id": convo_ids[i],
            "session_id": session_ids[i],
        }

        if i in failed_indices:
            result["answer_similarity"] = 0.0
            result["answer_relevancy"] = 0.0
            result["status"] = "failed"
            result["error_category"] = error_categories[i] or "unknown"
            result["error_message"] = error_messages[i]
            if setup_results[i] == "setup_failed":
                result["status"] = "setup_failed"
                result["error_category"] = "setup_failed"
        else:
            eval_score = batch_scores[eval_idx]
            result["answer_similarity"] = eval_score.similarity
            result["answer_relevancy"] = eval_score.quality
            result["score_reason"] = eval_score.reason
            result["status"] = "success"
            eval_idx += 1

        # session_id for token usage metrics, convo_id for planner/tools
        token_metrics = get_token_metrics(session_ids[i], account_id, tenant_id, user_id)
        if token_metrics:
            result.update(token_metrics)

        # Execution quality (full trace)
        trace = get_execution_trace(convo_ids[i], account_id) if convo_ids[i] else ""
        if not trace:
            trace = get_planner_response(convo_ids[i], account_id) or ""
        if trace:
            result["planner_response"] = trace
            planner_score, planner_reason = _evaluate_planner(
                trace, queries[i], llm
            )
            result["planner_relevancy"] = planner_score
            result["execution_trace"] = trace
            if planner_reason:
                result["score_reason"] = result.get("score_reason", "") + f"\n[Planner] {planner_reason}"
                result["score_reason"] = result["score_reason"].strip()
        else:
            result["planner_relevancy"] = 0.0

        # Agent-specific enrichment (e.g. RCA rubric scoring, signal analysis)
        if result_enricher and result["status"] == "success":
            try:
                result_enricher(result, tc, llm)
            except Exception as e:
                logger.warning("Result enricher failed for test %d: %s", i, e)

        results.append(result)

        # Store each test result to DB progressively (survives crashes)
        if run_id:
            store_test_result(
                run_id,
                {
                    "test_id": result.get("test_id", ""),
                    "test_index": global_indices[i],
                    "status": "pass" if result["status"] == "success" else "fail",
                    "query": queries[i],
                    "expected_answer": _flatten_ground_truth(ground_truths[i]),
                    "actual_answer": answers[i],
                    "answer_similarity": result.get("answer_similarity", 0.0),
                    "answer_relevancy": result.get("answer_relevancy", 0.0),
                    "planner_relevancy": result.get("planner_relevancy", 0.0),
                    "duration_seconds": durations[i],
                    "cost": result.get("cost", 0.0),
                    "total_tokens": result.get("total_tokens", 0),
                    "input_tokens": result.get("input_tokens", 0),
                    "output_tokens": result.get("completion_tokens", 0),
                    "cache_read_tokens": result.get("cached_input_tokens", 0),
                    "tool_calls_total": result.get("total_tool_calls", 0),
                    "tool_calls_successful": result.get("successful_tool_calls", 0),
                    "tool_names": result.get("tool_names", []),
                    "tags": tags_list[i],
                    "error_message": result.get("error_message", ""),
                    "error_category": result.get("error_category", ""),
                },
            )

    # --- Phase: GENERATING_REPORT ---
    set_phase(run_id, RunPhase.GENERATING_REPORT)

    logger.info(
        "Benchmark complete for '%s': %d results stored to DB", agent, len(results)
    )
    return True


def _flatten_ground_truth(gt) -> str:
    """Flatten ground truth (list or string) into a single string."""
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
    """Evaluate planner/execution quality. Returns (score_0_100, reason_str)."""
    try:
        from .orchestrator_metric import get_orchestrator_quality_metric
        from .ragas_evaluation import _call_rubric_prompt

        metric = get_orchestrator_quality_metric(llm)
        score, reason = _call_rubric_prompt(metric, query, execution_trace, query)
        return score, reason
    except Exception as e:
        logger.warning("Planner evaluation failed: %s", e)
        return 0.0, ""
