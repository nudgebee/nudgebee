"""
AWS RCA (Root Cause Analysis) Quality Evaluation Metric
Evaluates the quality of the AWS debug agent's investigation using rubrics-based LLM scoring.
"""
import logging

from datasets import Dataset
from ragas import evaluate
from ragas.metrics import RubricsScore

logger = logging.getLogger(__name__)


def get_aws_rca_quality_metric(llm):
    """
    Creates and returns a generic AWS RCA Quality metric.

    Args:
        llm: The LLM instance to use for evaluation

    Returns:
        RubricsScore metric instance
    """

    rca_rubric = {
        "score1_description": (
            "Score 1: Agent failed to investigate or diagnosed wrong component. "
            "No evidence cited. Fabricated log entries or metrics. "
            "Would lead to incorrect remediation."
        ),
        "score2_description": (
            "Score 2: Agent found symptoms but not root cause. "
            "Identified WHAT is failing but not WHY or WHERE the issue originates. "
            "Checked some signals but missed critical investigation steps."
        ),
        "score3_description": (
            "Score 3: Agent found the problem area but lacked specifics. "
            "Checked multiple signal types (logs, metrics, config). "
            "Diagnosis is correct direction but missing exact location or values."
        ),
        "score4_description": (
            "Score 4: Agent correctly identified root cause with evidence. "
            "Traced symptom to source. Cited actual CLI output or log content. "
            "Provided specific remediation steps."
        ),
        "score5_description": (
            "Score 5: Expert diagnosis with complete evidence chain. "
            "Systematic three-layer investigation (Infrastructure, Network, Application). "
            "Full symptom → signal → root cause chain. "
            "Specific, actionable remediation with exact values/paths."
        )
    }

    try:
        rca_quality = RubricsScore(
            name="aws_rca_quality",
            rubrics=rca_rubric,
            llm=llm
        )
        logger.info("AWS RCA quality metric created successfully")
        return rca_quality
    except TypeError as e:
        logger.warning(f"Failed with name/rubrics params: {e}")
        try:
            rca_quality = RubricsScore(llm=llm)
            return rca_quality
        except Exception as e2:
            logger.error(f"Simplified initialization also failed: {e2}")
            raise
    except Exception as e:
        logger.error(f"Failed to create AWS RCA quality metric: {e}")
        raise


# --- Module-level enricher for unified benchmark runner (config.yaml) ---

_rca_metric = None


def aws_rca_enricher(result, test_case, llm):
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
