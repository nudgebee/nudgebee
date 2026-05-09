"""Log Analysis agent benchmark — unified nubi format.

Evaluates the loganalysis agent's ability to identify issues, root causes,
and extract file/line context from application log data.

Agent: loganalysis
Tools: none (custom planner, direct LLM calls)
Data: fixtures/ directory with test_case.yaml files containing log samples
"""

import logging
import os
from pathlib import Path

import pytest

from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "loganalysis"
TOOL_NAMES = []  # loganalysis uses custom planner, no tool calls
FIXTURES_DIR = Path(__file__).parent / "fixtures"


@pytest.mark.benchmark
@pytest.mark.log_analysis
def test_log_analysis_benchmark():
    """Run log analysis agent benchmark with unified nubi format."""
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Log analysis benchmark failed — no results stored"
    logger.info("Log analysis benchmark complete")
