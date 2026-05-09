"""
Simple standalone script for orchestrator quality metric validation with dummy data.
This validates the metric in isolation without the full test infrastructure.

IMPORTANT: As of January 2026, Gemini 1.x models are retired (return 404).
Use Gemini 2.5+ models: gemini-2.5-flash, gemini-2.5-flash-lite, gemini-2.5-pro

Usage:
    python test_orchestrator_metric_simple.py
"""
import os
import traceback
from dotenv import load_dotenv

load_dotenv()

# Dummy test data
DUMMY_TEST_CASES = [
    {
        "user_query": "Why is my pod crashing?",
        "planner_response": """[
            {
                "id": "step1",
                "agent": "kubectl",
                "query": "kubectl get pods -n production --field-selector status.phase!=Running",
                "plan": "First, identify which pods are not running in the production namespace"
            },
            {
                "id": "step2",
                "agent": "log_analyzer",
                "query": "Check logs for OOM (Out of Memory) errors in the last 1 hour for the crashed pods",
                "plan": "Analyze logs to find the root cause of crashes"
            },
            {
                "id": "step3",
                "agent": "metric_analyzer",
                "query": "Show memory usage trends over the last 2 hours for the problematic pods",
                "plan": "Check if memory usage was increasing before the crash"
            }
        ]""",
        "expected_score": "4-5",  # Good orchestration
        "reason": "Selects right agents (kubectl, log_analyzer, metric_analyzer), clear queries with specific details (time ranges, namespaces, error types), logical investigation flow"
    },
    {
        "user_query": "Check if there are any errors",
        "planner_response": """[
            {
                "id": "step1",
                "agent": "log_analyzer",
                "query": "check logs",
                "plan": "Look at logs"
            }
        ]""",
        "expected_score": "2-3",  # Poor orchestration
        "reason": "Right agent but VERY vague query - missing: what errors? which service? what time range? which namespace?"
    },
    {
        "user_query": "Restart the database pod",
        "planner_response": """[
            {
                "id": "step1",
                "agent": "database",
                "query": "restart pod",
                "plan": "Restart it"
            }
        ]""",
        "expected_score": "1",  # Critical failure
        "reason": "WRONG agent - should use kubectl, not database agent for pod operations. No investigation before action."
    }
]


async def run_orchestrator_metric_validation():
    """Validate orchestrator metric with dummy data using Google AI."""
    print("=" * 80)
    print("Testing Orchestrator Quality Metric with Dummy Data")
    print("=" * 80)
    print()

    # Import here to see any import errors clearly
    try:
        from datasets import Dataset
        from ragas import evaluate
        from langchain_google_genai import GoogleGenerativeAI
        from ragas.llms import LangchainLLMWrapper
        from ragas.metrics import RubricsScore
        from utils.orchestrator_metric import get_orchestrator_quality_metric
    except ImportError as e:
        print(f"Import error: {e}")
        traceback.print_exc()
        return

    # Get API key
    google_api_key = os.getenv("GOOGLE_API_KEY")
    if not google_api_key:
        print("GOOGLE_API_KEY not found in environment")
        return

    # Initialize Gemini LLM
    # Note: Gemini 1.5 models are retired and return 404. Use Gemini 2.5+ models.
    print("✓ Initializing GoogleGenerativeAI (Gemini 2.5 Flash)...")
    base_llm = GoogleGenerativeAI(
        model="gemini-2.5-flash-lite",
        google_api_key=google_api_key,
        temperature=0
    )

    # Wrap it for ragas compatibility using official LangchainLLMWrapper
    print("✓ Wrapping with LangchainLLMWrapper for ragas compatibility...")
    llm = LangchainLLMWrapper(base_llm)
    print(f"  Base LLM type: {type(base_llm)}")
    print(f"  Base LLM class: {base_llm.__class__.__name__}")
    print(f"  Wrapped LLM type: {type(llm)}")

    # Check if wrapped LLM has the required method
    if hasattr(llm, 'set_run_config'):
        print(f"  ✓ Wrapped LLM has 'set_run_config' method")
    else:
        print(f" Wrapped LLM does NOT have 'set_run_config' method")

    print()

    # Initialize metric
    print("✓ Initializing Orchestrator Quality Metric...")
    try:
        metric = get_orchestrator_quality_metric(llm=llm)
        print(f"  Metric type: {type(metric)}")
        print(f"  Metric name: {metric.name if hasattr(metric, 'name') else 'N/A'}")
    except Exception as e:
        print(f"Failed to initialize metric: {e}")
        traceback.print_exc()
        return

    print()
    print("=" * 80)
    print("Running Test Cases")
    print("=" * 80)
    print()

    # Test each case
    for i, test_case in enumerate(DUMMY_TEST_CASES, 1):
        print(f"Test Case {i}: {test_case['user_query']}")
        print(f"Expected Score: {test_case['expected_score']}")
        print(f"Reason: {test_case['reason']}")
        print()

        try:
            eval_data = Dataset.from_dict({
                "user_input": [test_case["user_query"]],
                "response": [test_case["planner_response"]],
            })

            print("  Evaluating...")
            result = evaluate(
                eval_data,
                metrics=[metric],
                llm=llm,
                raise_exceptions=True,
            )

            metrics_dict = result._repr_dict
            score_raw = metrics_dict.get("orchestrator_quality", 0.0)
            score_normalized = score_raw / 5.0  # Normalize to 0-1

            print(f"  ✓ Raw Score: {score_raw:.2f}/5.0")
            print(f"  ✓ Normalized Score: {score_normalized:.4f} ({score_normalized*100:.1f}%)")
            print()

        except Exception as e:
            print(f" Evaluation failed: {e}")
            traceback.print_exc()
            print()

    print("=" * 80)
    print("Test Complete")
    print("=" * 80)


if __name__ == "__main__":
    import asyncio
    asyncio.run(run_orchestrator_metric_validation())
