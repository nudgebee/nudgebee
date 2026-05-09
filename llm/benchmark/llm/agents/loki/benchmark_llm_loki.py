"""Loki/LogQL agent benchmark — unified nubi format.

Agent: logql_query_generator
Tools: loki_execute, elastic_search_execute, logs_execute
Data: fixtures/ directory with test_case.yaml files
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "logql_query_generator"
TOOL_NAMES = ["loki_execute", "elastic_search_execute", "logs_execute"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
def test_loki_benchmark():
    """Run loki agent benchmark with unified nubi format."""
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
