"""AWS agent benchmark — unified nubi format.

Supports both command queries (aws_execute tool) and RCA scenarios
(full response with rubric-based scoring).

Agent: aws / aws_debug
Tool: aws_execute
Data: fixtures/ directory with test_case.yaml files
"""

import logging
import os
from pathlib import Path

import pytest
from datasets import Dataset
from ragas import evaluate

from llm.agents.common.runner import run_benchmark

try:
    from .utils.aws_rca_metric import get_aws_rca_quality_metric
except ImportError:
    from utils.aws_rca_metric import get_aws_rca_quality_metric

logger = logging.getLogger(__name__)

AGENT = "aws"
TOOL_NAMES = ["aws_execute"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"

# Extra tool confirmations for aws_debug agent variant
EXTRA_CONFIRMATIONS = {"aws_observability": "yes", "aws": "yes"}

# Lazily initialized RCA metric
_rca_metric = None


def _aws_rca_enricher(result, test_case, llm):
    """Add AWS RCA rubric scoring for RCA-type queries."""
    if test_case.get("type") != "rca":
        return

    global _rca_metric
    if _rca_metric is None:
        try:
            _rca_metric = get_aws_rca_quality_metric(llm)
        except Exception as e:
            logger.error("Failed to init AWS RCA metric: %s", e)
            return

    try:
        rca_data = Dataset.from_dict(
            {"user_input": [result["query"]], "response": [result["answer"]]}
        )
        rca_result = evaluate(
            rca_data, metrics=[_rca_metric], llm=llm, raise_exceptions=True
        )
        raw = rca_result._repr_dict.get("aws_rca_quality", 0.0)
        result["rca_quality_score"] = round(raw, 2)
        result["rca_quality_normalized"] = round(raw / 5.0 * 100, 2)
    except Exception as e:
        logger.error("AWS RCA eval failed: %s", e)
        result["rca_quality_score"] = 0.0
        result["rca_quality_normalized"] = 0.0


@pytest.mark.benchmark
def test_aws_benchmark():
    """Run AWS agent benchmark with unified nubi format."""
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        extra_tool_confirmations=EXTRA_CONFIRMATIONS,
        result_enricher=_aws_rca_enricher,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Benchmark complete")
