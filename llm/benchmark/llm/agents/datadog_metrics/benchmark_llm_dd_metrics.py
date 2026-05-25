"""Datadog metrics agent benchmark — unified nubi format.

Agent: datadog_metrics
Tool: datadog_metrics_execute (fetched from DB)
Data: fixtures/ directory with test_case.yaml files

Covers: AWS (EC2, ALB, EBS, NAT, ES, Lambda), Kubernetes, and system metrics.
"""

import json
import logging
import os
from pathlib import Path
from typing import List, Optional

import pytest

from benchmark_server.utils.llm_client import (
    extract_conversation_id,
    extract_tool_command,
)
from llm.agents.common.metrics import fetch_tool_command_from_db
from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = os.getenv("BENCHMARK_AGENT", "datadog_metrics")
TOOL_NAMES = ["datadog_metrics_execute"]
DB_TOOL_NAME = "datadog_metrics_execute"
FIXTURES_DIR = Path(__file__).parent / "fixtures"


def _extract_dd_query(
    response_data: dict,
    account_id: str,
    tool_names: List[str],
    db_tool_name: Optional[str] = None,
) -> List[str]:
    """Extract the executed Datadog query from the tool call arguments.

    Parses the JSON arguments of the datadog_metrics_execute tool call
    and returns the 'query' field instead of the raw JSON string.
    """
    if not response_data:
        return []

    conversation_id = extract_conversation_id(response_data)

    # Try DB fetch first
    if db_tool_name and conversation_id:
        command = fetch_tool_command_from_db(conversation_id, account_id, db_tool_name)
        if command:
            query = _parse_query_from_args(command)
            return [query] if query else [command]

    # Try extracting tool command from response
    command = extract_tool_command(response_data, tool_names)
    if command:
        query = _parse_query_from_args(command)
        return [query] if query else [command]

    return []


def _parse_query_from_args(args_str: str) -> Optional[str]:
    """Parse the 'query' field from a JSON arguments string."""
    try:
        args = json.loads(args_str)
        if isinstance(args, dict) and "query" in args:
            return args["query"]
    except (json.JSONDecodeError, TypeError):
        pass
    return None


@pytest.mark.benchmark
def test_datadog_metrics_benchmark():
    """Run datadog metrics agent benchmark with unified nubi format."""
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        db_tool_name=DB_TOOL_NAME,
        answer_extractor=_extract_dd_query,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Benchmark complete")
