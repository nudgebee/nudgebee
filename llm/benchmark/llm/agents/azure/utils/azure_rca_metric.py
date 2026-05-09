"""
Azure RCA (Root Cause Analysis) Quality Evaluation Metric
Evaluates the quality of the Azure agent's investigation using rubrics-based LLM scoring.
"""
import logging

from datasets import Dataset
from ragas import evaluate
from ragas.metrics import RubricsScore

logger = logging.getLogger(__name__)


def get_azure_rca_quality_metric(llm):
    """
    Creates and returns a generic Azure RCA Quality metric.

    Args:
        llm: The LLM instance to use for evaluation

    Returns:
        RubricsScore metric instance
    """

    rca_rubric = {
        "score1_description": (
            "Score 1: Agent failed to retrieve or interpret Azure data. "
            "Called wrong API endpoints or fetched irrelevant resources. "
            "Response contains fabricated resource names or metrics. "
            "Would mislead the operator."
        ),
        "score2_description": (
            "Score 2: Agent retrieved some data but missed key information. "
            "Partially answered the query but omitted important fields "
            "(e.g., listed VMs but not their states, or listed costs without breakdown). "
            "Response is incomplete and needs follow-up."
        ),
        "score3_description": (
            "Score 3: Agent retrieved the correct data but presentation is lacking. "
            "Called the right Azure CLI commands or APIs. "
            "Results are accurate but not fully organized or actionable "
            "(e.g., showed metrics but no interpretation of whether values are healthy or problematic)."
        ),
        "score4_description": (
            "Score 4: Agent correctly retrieved and interpreted Azure data. "
            "Called appropriate az CLI commands or Azure Monitor APIs. "
            "Presented structured results with relevant fields. "
            "Provided actionable insights or next steps (e.g., flagged unattached disks for deletion)."
        ),
        "score5_description": (
            "Score 5: Expert-level Azure investigation with complete, actionable output. "
            "Used optimal API calls (Resource Graph, Cost Management, Monitor) efficiently. "
            "Response includes full context: resource states, metrics with thresholds, "
            "cost impact, security implications, and specific recommendations with commands."
        ),
    }

    try:
        rca_quality = RubricsScore(
            name="azure_rca_quality",
            rubrics=rca_rubric,
            llm=llm,
        )
        logger.info("Azure RCA quality metric created successfully")
        return rca_quality
    except Exception as e:
        logger.error(f"Failed to create Azure RCA quality metric: {e}")
        raise


# --- Module-level enricher for unified benchmark runner (config.yaml) ---

_rca_metric = None


def azure_rca_enricher(result, test_case, llm):
    """Add Azure RCA rubric scoring for RCA-type queries."""
    if test_case.get("type") != "rca":
        return

    global _rca_metric
    if _rca_metric is None:
        try:
            _rca_metric = get_azure_rca_quality_metric(llm)
        except Exception as e:
            logger.error("Failed to init Azure RCA metric: %s", e)
            return

    try:
        rca_data = Dataset.from_dict(
            {"user_input": [result["query"]], "response": [result["answer"]]}
        )
        rca_result = evaluate(
            rca_data, metrics=[_rca_metric], llm=llm, raise_exceptions=True
        )
        raw = rca_result._repr_dict.get("azure_rca_quality", 0.0)
        result["rca_quality_score"] = round(raw, 2)
        result["rca_quality_normalized"] = round(raw / 5.0 * 100, 2)
    except Exception as e:
        logger.error("Azure RCA eval failed: %s", e)
        result["rca_quality_score"] = 0.0
        result["rca_quality_normalized"] = 0.0
