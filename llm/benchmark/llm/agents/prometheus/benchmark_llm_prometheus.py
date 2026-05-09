"""Prometheus parent agent benchmark.

Targets the orchestration agent (`prometheus`) — tool routing, retry
behaviour, and PromQL execution end-to-end. The NL->PromQL sub-agent
(`promql_query`) has its own suite under ../promql/.

Evaluation layers, mapped to published standards:
  - Answer fidelity        : RAGAS answer_similarity vs expected_output PromQL
  - MetricAcc              : PromAssistant (arXiv:2503.03114) — metric coverage
  - Trajectory quality     : TRAJECT-Bench (arXiv:2510.04550) — tool selection,
                             forbidden tools, max-steps budget, loop detection
  - Response quality       : RAGAS planner rubric (shared)
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

try:
    from .utils.trajectory_metric import enrich as _trajectory_enrich
    from .utils.answer_extractor import extract as _prometheus_extract
except ImportError:
    from utils.trajectory_metric import enrich as _trajectory_enrich
    from utils.answer_extractor import extract as _prometheus_extract

logger = logging.getLogger(__name__)

AGENT = "prometheus"
TOOL_NAMES = ["prometheus_execute"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"

# Auto-confirm every tool the agent may invoke during a benchmark run.
EXTRA_CONFIRMATIONS = {
    "promql_query": "yes",
    "metrics_list": "yes",
    "metrics_labels_list": "yes",
    "resource_search": "yes",
    "visualizer": "yes",
}


def _enricher(result, test_case, llm):
    """Add trajectory metrics. Failures are non-fatal — RAGAS scores still land."""
    try:
        _trajectory_enrich(result, test_case, llm)
    except Exception as e:
        logger.warning("trajectory enrichment failed for %s: %s", result.get("test_id"), e)


@pytest.mark.benchmark
def test_prometheus_benchmark():
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        # db_tool_name omitted on purpose — the default extractor's DB path
        # returns only the most recent prometheus_execute. The custom extractor
        # below pulls every executed PromQL plus the final response.
        answer_extractor=_prometheus_extract,
        extra_tool_confirmations=EXTRA_CONFIRMATIONS,
        result_enricher=_enricher,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Benchmark complete")
