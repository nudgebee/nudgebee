"""Signoz log agent benchmark — advanced/complex test cases.

Tests multi-field filters, negation, ambiguous queries, nested _or/_and,
partial names, absolute time ranges, and combined container/pod/namespace.

Reuses the same answer extractor and structural JSON evaluator from
the basic signoz_logs suite.
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.signoz_logs.benchmark_llm_signoz_logs import (
    _extract_signoz_query,
    _signoz_query_enricher,
)
from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "logs"
TOOL_NAMES = ["logs_execute", "query_generator", "resource_search"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
def test_signoz_logs_advanced_benchmark():
    """Run signoz log agent advanced benchmark."""
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        answer_extractor=_extract_signoz_query,
        result_enricher=_signoz_query_enricher,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Advanced benchmark complete")
