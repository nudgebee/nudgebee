# CRITICAL: Load .env BEFORE importing any modules that use Config
# Config class reads env vars at import time, so load_dotenv must come first
import os
from pathlib import Path
from dotenv import load_dotenv

load_dotenv()

import logging  # noqa: E402
import time  # noqa: E402
import traceback  # noqa: E402
from typing import Any, Dict, List  # noqa: E402

import nest_asyncio  # noqa: E402
import pytest  # noqa: E402
from datasets import Dataset  # noqa: E402
from ragas import evaluate  # noqa: E402
from ragas.llms import LangchainLLMWrapper  # noqa: E402

from benchmark_server.common.llm import get_llm, get_embeddings  # noqa: E402
from benchmark_server.utils.run_manager import (  # noqa: E402
    RunPhase,
    set_phase,
    should_proceed,
    store_test_result,
    update_progress,
)

from llm.agents.common.fixtures import find_test_cases, load_test_case  # noqa: E402
from llm.agents.common.lifecycle import run_before_test, run_after_test  # noqa: E402
from llm.agents.common.metrics import (  # noqa: E402
    get_planner_response,
    get_token_metrics,
    get_tool_names,
)
from llm.agents.common.orchestrator_metric import (  # noqa: E402
    get_orchestrator_quality_metric,
)
from .utils.llm_server import get_llm_ans  # noqa: E402

nest_asyncio.apply()

logger = logging.getLogger(__name__)

FIXTURES_DIR = Path(__file__).parent / "fixtures"

os.environ["GRPC_ENABLE_FORK_SUPPORT"] = "0"


@pytest.fixture(scope="session")
def total_test_count(request):
    """Compute the total number of test cases for progress tracking."""
    max_tests = request.config.getoption("--max-tests", default=None)
    test_indices = request.config.getoption("--test-indices", default=None)
    skip_indices = os.getenv("SKIP_INDICES")
    cases = find_test_cases(
        FIXTURES_DIR, max_tests=max_tests, test_indices=test_indices,
        skip_indices=skip_indices,
    )
    count = len(cases)

    # Set phase to RUNNING_TESTS at the start of the session
    run_id = os.getenv("BENCHMARK_RUN_ID", "")
    if run_id:
        set_phase(run_id, RunPhase.RUNNING_TESTS)

    return count


@pytest.fixture(scope="session")
def ragas_setup():
    """Initializes and provides Ragas metrics, LLM, and embeddings."""
    base_llm = get_llm()
    llm = LangchainLLMWrapper(base_llm)
    embeddings = get_embeddings()
    logger.info(
        "Ragas setup: wrapped %s with LangchainLLMWrapper",
        base_llm.__class__.__name__,
    )
    return {
        "llm": llm,
        "embeddings": embeddings,
        "base_llm": base_llm,
    }


@pytest.fixture(scope="session")
def results_aggregator():
    """A session-scoped fixture to collect test results in memory.

    Results are stored to DB progressively via store_test_result() in each test.
    This fixture only tracks count for progress updates.
    """
    results = []
    yield results
    run_id = os.getenv("BENCHMARK_RUN_ID", "")
    if run_id:
        set_phase(run_id, RunPhase.GENERATING_REPORT)
    logger.info("Benchmark complete: %d test results stored to DB", len(results))


@pytest.fixture
def test_case_runner(request) -> Any:
    test_case_path, test_case_id, global_index = request.param
    test_case = load_test_case(test_case_path)
    test_case["__global_index__"] = global_index
    test_case_dir = os.path.dirname(test_case_path)

    try:
        result = run_before_test(test_case, cwd=test_case_dir)
        if not result.success:
            pytest.fail(f"Setup failed for {test_case_id}: {result.error_details}")
        yield test_case
    finally:
        result = run_after_test(test_case, cwd=test_case_dir)
        if not result.success:
            logger.warning(
                "Cleanup failed for %s: %s",
                test_case_id,
                result.error_details,
            )


def pytest_generate_tests(metafunc):
    """Generate test cases dynamically based on --max-tests option."""
    if "test_case_runner" in metafunc.fixturenames:
        max_tests = metafunc.config.getoption("--max-tests")
        test_indices = metafunc.config.getoption("--test-indices", default=None)
        skip_indices = os.getenv("SKIP_INDICES")
        test_cases = find_test_cases(
            FIXTURES_DIR, max_tests=max_tests, test_indices=test_indices,
            skip_indices=skip_indices,
        )
        metafunc.parametrize(
            "test_case_runner", test_cases, indirect=True, ids=lambda x: x[1]
        )


def test_ask_nubi(
    test_case_runner: Dict[str, Any],
    ragas_setup: Dict[str, Any],
    results_aggregator: List[Dict[str, Any]],
    total_test_count: int,
) -> None:
    """Run a single nubi test case with RAGAS evaluation."""
    start_time = time.time()
    test_case = test_case_runner
    test_case_name = test_case.get("__id__", "unknown")
    global_index = test_case.get("__global_index__", 0)
    run_id = os.getenv("BENCHMARK_RUN_ID", "")

    # Check lifecycle state
    if run_id and not should_proceed(run_id):
        pytest.skip("Benchmark stopped by user")

    if run_id:
        update_progress(
            run_id,
            len(results_aggregator) + 1,
            total_test_count,
            test_case.get("user_prompt", "")[:200],
        )

    account_id = os.getenv("ACCOUNT_ID", os.getenv("TEST_ACCOUNT", ""))
    user_id = os.getenv("USER_ID", os.getenv("TEST_USER", ""))
    tenant_id = os.getenv("TENANT_ID", os.getenv("TEST_TENANT", ""))

    user_prompt = test_case["user_prompt"]
    logger.info("Calling LLM API: %s...", user_prompt[:100])

    llm_data = get_llm_ans(
        user_prompt,
        account_id=account_id,
        tenant_id=tenant_id,
        user_id=user_id,
    )
    api_response_raw = llm_data["response"]
    conversation_id = llm_data["conversation_id"]
    convo_id = llm_data.get("convo_id")

    # Get metrics using shared module
    token_metrics = get_token_metrics(
        conversation_id,
        account_id=account_id,
        tenant_id=tenant_id,
        user_id=user_id,
    )

    planner_response = get_planner_response(convo_id=convo_id, account_id=account_id)

    test_failed = api_response_raw == "SYSTEM_FAILURE"
    if test_failed:
        logger.error("Test '%s' failed due to API system failure.", test_case_name)

    # Prepare data for RAGAS
    if isinstance(api_response_raw, list):
        api_response_contexts = api_response_raw
    else:
        api_response_contexts = [str(api_response_raw)]

    api_response_str = "\n".join(api_response_contexts)

    ground_truths_list = test_case["expected_output"]
    if isinstance(ground_truths_list, str):
        ground_truths_list = [ground_truths_list]
    ground_truth_str = "\n".join(ground_truths_list)

    answer_similarity_score = 0.0
    answer_relevancy_score = 0.0
    planner_relevancy_score = 0.0

    try:
        from llm.agents.common.ragas_evaluation import evaluate_single

        eval_result = evaluate_single(
            user_prompt, api_response_str, ground_truth_str,
            ragas_setup["llm"], ragas_setup["embeddings"],
        )
        # evaluate_single returns 0-100, convert to 0-1 for compatibility
        answer_similarity_score = eval_result.similarity / 100.0
        answer_relevancy_score = eval_result.quality / 100.0

        # Evaluate planner orchestration quality
        if planner_response:
            try:
                orchestrator_metric = get_orchestrator_quality_metric(
                    llm=ragas_setup["llm"]
                )
                planner_eval_data = Dataset.from_dict(
                    {
                        "user_input": [user_prompt],
                        "response": [planner_response],
                    }
                )
                planner_result = evaluate(
                    planner_eval_data,
                    metrics=[orchestrator_metric],
                    llm=ragas_setup["llm"],
                    embeddings=ragas_setup["embeddings"],
                    raise_exceptions=True,
                )
                planner_metrics = planner_result._repr_dict
                orchestrator_score_raw = planner_metrics.get(
                    "orchestrator_quality", 0.0
                )
                planner_relevancy_score = orchestrator_score_raw / 5.0
            except Exception as e:
                logger.error(
                    "Planner evaluation failed for '%s': %s", test_case_name, e
                )
                logger.error("Traceback: %s", traceback.format_exc())
    except Exception as e:
        logger.error("Ragas evaluation failed for '%s': %s", test_case_name, e)
        test_failed = True

    duration = time.time() - start_time

    # Get tool names using shared module
    tool_names_data = get_tool_names(convo_id=convo_id)

    result_data = {
        "test_id": test_case.get("__id__", test_case_name),
        "tags": test_case.get("tags", []),
        "query": user_prompt,
        "answer": api_response_str,
        "ground_truth": ground_truth_str,
        "answer_similarity": round(answer_similarity_score * 100, 2),
        "answer_relevancy": round(answer_relevancy_score * 100, 2),
        "planner_relevancy": round(planner_relevancy_score * 100, 2),
        "duration_seconds": round(duration, 2),
        "status": "failed" if test_failed else "success",
    }

    if token_metrics:
        result_data.update(
            {
                "total_tokens": token_metrics.get("total_tokens", 0),
                "input_tokens": token_metrics.get("input_tokens", 0),
                "completion_tokens": token_metrics.get("completion_tokens", 0),
                "cached_input_tokens": token_metrics.get("cached_input_tokens", 0),
                "cost": token_metrics.get("cost", 0.0),
                "cache_hit_rate": token_metrics.get("cache_hit_rate", 0.0),
                "model_providers": token_metrics.get("model_providers", []),
                "model_names": token_metrics.get("model_names", []),
                "tool_calls_total": token_metrics.get("total_tool_calls", 0),
                "tool_calls_successful": token_metrics.get("successful_tool_calls", 0),
                "wall_time_seconds": token_metrics.get("wall_time_seconds"),
                "tool_time_seconds": token_metrics.get("tool_time_seconds"),
                "api_time_seconds": token_metrics.get("api_time_seconds"),
            }
        )

    if tool_names_data:
        result_data["tool_names"] = tool_names_data.get("tool_names", [])
        result_data["tool_names_failed"] = tool_names_data.get("tool_names_failed", [])

    results_aggregator.append(result_data)

    # Store test result to DB progressively (survives crashes)
    if run_id:
        store_test_result(
            run_id,
            {
                "test_id": result_data.get("test_id", ""),
                "test_index": global_index,
                "status": "pass" if result_data.get("status") == "success" else "fail",
                "query": user_prompt,
                "expected_answer": ground_truth_str,
                "actual_answer": api_response_str,
                "answer_similarity": result_data.get("answer_similarity", 0.0),
                "answer_relevancy": result_data.get("answer_relevancy", 0.0),
                "planner_relevancy": result_data.get("planner_relevancy", 0.0),
                "duration_seconds": result_data.get("duration_seconds", 0.0),
                "cost": result_data.get("cost", 0.0),
                "total_tokens": result_data.get("total_tokens", 0),
                "input_tokens": result_data.get("input_tokens", 0),
                "output_tokens": result_data.get("completion_tokens", 0),
                "cache_read_tokens": result_data.get("cached_input_tokens", 0),
                "tool_calls_total": result_data.get("tool_calls_total", 0),
                "tool_calls_successful": result_data.get("tool_calls_successful", 0),
                "tool_names": result_data.get("tool_names", []),
                "tags": test_case.get("tags", []),
                "error_message": result_data.get("error_message", ""),
            },
        )

    logger.info(
        "[%s] sim=%.2f%% rel=%.2f%% planner=%.2f%% (%.1fs)",
        test_case_name,
        result_data["answer_similarity"],
        result_data["answer_relevancy"],
        result_data["planner_relevancy"],
        duration,
    )
