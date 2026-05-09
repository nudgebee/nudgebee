"""Kubectl agent benchmark — unified nubi format.

Uses shared common utilities for fixture loading, LLM execution,
RAGAS evaluation, token/tool metrics, and report generation.

Agent: kubectl
Tool: kubectl_execute
Data: fixtures/ directory with test_case.yaml files
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "kubectl"
TOOL_NAMES = ["kubectl_execute"]
DB_TOOL_NAME = "kubectl_execute"
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
def test_kubectl_benchmark():
    """Run kubectl agent benchmark with unified nubi format."""
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
