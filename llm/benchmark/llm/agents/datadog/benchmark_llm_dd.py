"""Datadog logs agent benchmark — unified nubi format.

Agent: datadog_logs
Tool: datadog_log_execute (fetched from DB)
Data: fixtures/ directory with test_case.yaml files
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "datadog_logs"
TOOL_NAMES = ["datadog_log_execute"]
DB_TOOL_NAME = "datadog_log_execute"
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
def test_datadog_benchmark():
    """Run datadog agent benchmark with unified nubi format."""
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        db_tool_name=DB_TOOL_NAME,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Benchmark complete")
