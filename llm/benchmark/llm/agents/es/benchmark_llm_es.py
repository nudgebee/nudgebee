"""Elasticsearch agent benchmark — unified nubi format.

Agent: elastic_search_query
Tool: elastic_search_execute
Data: fixtures/ directory with test_case.yaml files

Note: This agent previously had no data file (benchmark_es.json was missing).
Test cases must be added to fixtures/ directory before running.
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "elastic_search_query"
TOOL_NAMES = ["elastic_search_execute"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
def test_es_benchmark():
    """Run elasticsearch agent benchmark with unified nubi format."""
    if not FIXTURES_DIR.exists() or not list(FIXTURES_DIR.glob("**/test_case.yaml")):
        pytest.skip("No fixtures found — add test cases to fixtures/ directory")

    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Benchmark complete")
