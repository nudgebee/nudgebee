"""RCA (Root Cause Analysis) agent benchmark — unified nubi format.

Uses feature flag controller to trigger K8s failure scenarios,
then evaluates the agent's investigation response quality.

Agent: k8s_debug
Data: fixtures/ directory with test_case.yaml files (scenario definitions)
"""

import logging
import os
import time
from pathlib import Path
from typing import Any, Dict, List

import pytest

from benchmark.llm.agents.rca.utils.feature_flag_controller import FeatureFlagController
from benchmark_server.utils.llm_client import extract_response_text
from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "k8s_debug"
TOOL_NAMES = []  # RCA uses full response, not tool commands
FIXTURES_DIR = Path(__file__).parent / "fixtures"

# Shared controller instance
_controller = FeatureFlagController(namespace="nudgebee-demo")


def _before_query(test_case: Dict[str, Any]):
    """Enable feature flag and wait for telemetry before each RCA scenario."""
    feature_flag = test_case.get("feature_flag")
    if not feature_flag:
        return
    flag_variant = test_case.get("flag_variant", "on")
    wait_time = test_case.get("wait_time_seconds", 60)
    _controller.enable_flag(feature_flag, flag_variant)
    _controller.wait_for_telemetry(wait_time)


def _after_query(test_case: Dict[str, Any]):
    """Disable feature flag after each RCA scenario."""
    feature_flag = test_case.get("feature_flag")
    if not feature_flag:
        return
    _controller.disable_flag(feature_flag)
    time.sleep(10)


def _rca_signal_enricher(result: Dict[str, Any], test_case: Dict[str, Any], llm):
    """Analyze which telemetry signals the agent used during investigation."""
    # Signal analysis would need access to agent_step_response from the LLM result
    # For now, this is a placeholder — signal coverage is tracked if available
    pass


@pytest.mark.benchmark
@pytest.mark.rca
def test_rca_scenarios():
    """Run RCA benchmark across all scenarios with unified nubi format."""
    agent = os.getenv("RCA_AGENT_NAME", AGENT)
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=agent,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        before_query=_before_query,
        after_query=_after_query,
        result_enricher=_rca_signal_enricher,
        max_tests=max_tests,
        test_indices=test_indices,
        max_retries=0,  # RCA scenarios with feature flags shouldn't be retried
    )

    assert success, "RCA benchmark failed — no results stored"
    logger.info("RCA benchmark complete")
