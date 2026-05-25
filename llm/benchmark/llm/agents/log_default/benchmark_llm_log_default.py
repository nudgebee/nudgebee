"""logs_default agent benchmark — unified nubi format.

Agent: logs (parent) — routes to logs_default (provider path) or kubectl_log (fallback).
Tools: query_generator, logs_execute, kubectl_log_execute
Fixtures exercise both provider-happy-path and kubectl-fallback arms.
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "logs"
TOOL_NAMES = ["query_generator", "logs_execute", "kubectl_log_execute"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
def test_logs_default_benchmark():
    """Run logs_default agent benchmark with unified nubi format."""
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
