"""Orchestrator Quality Evaluation Metric.

Evaluates the full execution trace of an AI agent — plan, agent calls,
tool calls, retries, and whether the investigation was relevant to the question.
"""

import logging

from ragas.metrics import RubricsScore

logger = logging.getLogger(__name__)


def get_orchestrator_quality_metric(llm):
    """Create and return the Orchestrator Quality metric (RubricsScore 1-5).

    Evaluates the full execution trace including:
    1. Investigation relevance — did it investigate the right things for the question?
    2. Command accuracy — correct namespaces, filters, resource names?
    3. Efficiency — how many wasted steps, retries, wrong targets?
    4. Recovery — when something failed, did it adapt intelligently?
    5. Coverage — did it check enough signals (logs, events, metrics, etc.)?
    """
    instruction = """
You are a Principal SRE reviewing the FULL execution trace of an AI debugging agent.
You have:
- **user_input**: The user's original question
- **response**: The complete execution trace — plan, agent calls, tool calls with parameters/results, and stats
- **reference**: The user's question (for context)

**Evaluate the quality of the agent's INVESTIGATION PROCESS, not the final answer.**

Score based on these criteria:
1. **Investigation Relevance**: Did it investigate things related to the user's question?
   - Good: User asks about DB latency → checks DB queries, connection pools, slow query logs
   - Bad: User asks about DB latency → only checks pod resource limits

2. **Command Accuracy**: Were the tool calls correct on first attempt?
   - Good: Correct namespace, pod name, filters from the start
   - Bad: Wrong namespace for 5 steps, then finally found the right one

3. **Efficiency**: How many steps were wasted on retries or wrong targets?
   - Good: Found the resource and investigated in 5 steps
   - Bad: Took 15 steps because it searched wrong namespaces, hit errors, retried

4. **Recovery Quality**: When a tool call failed, did it adapt?
   - Good: resource_search failed → tried kubectl get → found the resource
   - Bad: Same failing command retried 3 times

5. **Coverage**: Did it check multiple signals appropriate for the problem?
   - Good: Checked logs + events + describe + metrics for a crash issue
   - Bad: Only checked pod status and nothing else
"""

    rubric = {
        "score1_description": (
            f"{instruction}\n\n"
            "Score 1 - Complete Failure: Agent crashed, hit errors on most calls, "
            "or investigated completely irrelevant things. Most tool calls failed. "
            "No useful data gathered. Example: 'solver failed after multiple retries' "
            "or all tool calls returned quota errors."
        ),
        "score2_description": (
            "Score 2 - Poor Investigation: Agent ran but was highly inefficient. "
            "Many wasted steps finding the wrong resources, wrong namespaces, or wrong filters. "
            "Investigated some relevant areas but missed the key ones for the question. "
            "Example: Spent 10+ steps finding the deployment, then only checked resource limits "
            "when the question was about slow database queries."
        ),
        "score3_description": (
            "Score 3 - Partial Investigation: Agent found the right resources and ran some relevant checks, "
            "but missed important investigation paths. Moderate inefficiency with some retries. "
            "Covered 2-3 signal types but missed a critical one. "
            "Example: Checked logs and events but never checked database performance "
            "when investigating slow DB queries."
        ),
        "score4_description": (
            "Score 4 - Good Investigation: Agent efficiently found the right resources and "
            "investigated the relevant areas for the question. Minor inefficiencies (1-2 retries). "
            "Good coverage of signal types (logs, events, metrics, describe). "
            "Commands were mostly accurate on first attempt. "
            "Example: Found the pod quickly, checked logs for errors, checked events, "
            "described the pod for config issues."
        ),
        "score5_description": (
            "Score 5 - Excellent Investigation: Highly efficient — minimal retries, correct targets "
            "from the start. Investigated all relevant areas for the specific question. "
            "Strong coverage across multiple signal types. Intelligent recovery from any failures. "
            "Example: Directly found the deployment, checked slow query logs, identified the DB connection issue, "
            "verified with metrics — all in minimal steps."
        ),
    }

    try:
        return RubricsScore(
            name="orchestrator_quality",
            rubrics=rubric,
            llm=llm,
        )
    except TypeError as e:
        logger.warning("Failed with name/rubrics params: %s, trying simplified init", e)
        try:
            return RubricsScore(llm=llm)
        except Exception as e2:
            logger.error("Simplified initialization also failed: %s", e2)
            raise
    except Exception as e:
        logger.error("Failed to create orchestrator quality metric: %s", e)
        raise
