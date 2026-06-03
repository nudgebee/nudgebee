"""Centralized RAGAS evaluation utilities.

Single source of truth for answer similarity, answer quality (LLM-based),
and planner relevancy scoring. All benchmark paths should use these functions
instead of calling RAGAS directly.
"""

import asyncio
import logging
import math
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

from ragas.metrics import RubricsScore

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Answer Quality metric (LLM-based, replaces embedding-based answer_relevancy)
# ---------------------------------------------------------------------------

_ANSWER_QUALITY_INSTRUCTION = """
You are an expert evaluator reviewing an AI agent's answer to a user question.
You have the user's question, the agent's response, and a reference (expected) answer.

**Evaluate whether the agent's response correctly answers the question by comparing it to the reference.**

Scoring approach — count how many KEY FACTS from the reference appear in the response:
1. First, list the key facts/claims in the reference answer (typically 2-5 facts).
2. Then check which of those facts appear (even if worded differently) in the response.
3. Score based on the fraction of key facts found.

**Conditional / fallback expected answers (IMPORTANT):**
The reference may describe a primary answer AND an acceptable fallback when data is unavailable —
e.g. "The pod failed due to X. **If historical logs are not available** because of retention,
the agent should clearly state that data is no longer available and explain what it tried."
In this case, EITHER arm is a valid answer:
  - Response correctly identifies X (primary)  → Score 5
  - Response correctly states data unavailable, lists data sources it tried, and offers no guess
    → Score 5 (the fallback was met fully)
  - Response says "no data" but doesn't explain what was checked → Score 3
  - Response confabulates a wrong root cause when data was unavailable → Score 1-2

**Literal / exact-match expected answers (e.g. PromQL query, SQL, metric name):**
When the reference is a specific code snippet, query, or identifier (e.g. a metric name like
`foo_bar_seconds{a="b"}`), the response MUST contain that EXACT identifier or its equivalent.
A different metric name with the same general purpose is NOT a near-miss — it is wrong.
  - Same exact identifier or query = Score 5
  - Different identifier with same general intent = Score 1-2 (NOT 3 — this is a factual mismatch)

**Important rules:**
- Ignore formatting (markdown, emojis, headers) — focus only on factual content.
- "No data found / failed / couldn't investigate" with no fallback context = Score 1.
- A wrong root cause that sounds plausible but contradicts the reference = Score 2 (not 1, since it tried).
- Finding the right area but wrong specifics = Score 3 (e.g., found DB issues but wrong type).
- Required time windows, dates, IDs, counts, or named identifiers in the reference MUST appear
  in the response — missing them caps the score at 3 even if the topic is right.
- Do NOT default to extreme scores. Use 2, 3, 4 when the answer is partially right.
"""

_ANSWER_QUALITY_RUBRIC = {
    "score1_description": (
        f"{_ANSWER_QUALITY_INSTRUCTION}\n\n"
        "Score 1 - No Answer: The agent completely failed (errors, timeouts, 'no data found') "
        "OR the response contains zero key facts from the reference. "
        "Example: reference says 'DB connection pool exhaustion' but response says 'solver failed after multiple retries'."
    ),
    "score2_description": (
        "Score 2 - Wrong Answer: The response addresses the topic but identifies a WRONG root cause "
        "or misses the core finding entirely. It may mention related systems or symptoms "
        "but does not contain the actual cause/answer from the reference. "
        "Example: reference says 'slow DB queries' but response blames 'missing resource limits'."
    ),
    "score3_description": (
        "Score 3 - Partially Right: The response identifies the right general area or some correct facts "
        "but misses the specific root cause or key details from the reference. About half the key facts match. "
        "Example: reference says 'missing index on users.email causing full table scans' but response says "
        "'database performance issues detected' without identifying the missing index."
    ),
    "score4_description": (
        "Score 4 - Mostly Right: The response captures the main root cause from the reference "
        "with only minor details missing. Most key facts (>75%) are present. "
        "Example: reference says 'DEPLOY_ENV missing' and response correctly identifies the missing env var "
        "but doesn't mention the exact variable name."
    ),
    "score5_description": (
        "Score 5 - Fully Correct: All key facts from the reference are present in the response. "
        "The core answer matches completely. "
        "Example: reference says '14 pods in test-1' and response says 'There are 14 pods in the test-1 namespace'."
    ),
}


_ANSWER_SIMILARITY_INSTRUCTION = """
You are an expert evaluator measuring how semantically similar the agent's response is to the reference answer.
This is NOT about correctness — it's about whether the response TALKS ABOUT THE SAME THINGS as the reference.

**Scoring approach:**
- Compare the topics, entities, and concepts mentioned in both answers.
- A response about the right topic but wrong conclusion = high similarity (3-4).
- A response about a completely different topic = low similarity (1-2).
- An exact or near-exact match in meaning = Score 5.

**Conditional / fallback references (IMPORTANT):**
If the reference describes a primary answer AND a fallback for when data is missing
(e.g. "If historical logs are unavailable, agent should state so"), the response is
SEMANTICALLY EQUIVALENT to the reference if it covers either arm — the primary OR the
fallback (data unavailable + sources checked). Don't penalize a valid fallback response.

**Literal / exact-match references (PromQL, SQL, metric names, queries):**
When the reference is a specific code snippet or named identifier, "same topic" requires
the SAME identifier. Two different metric names that both describe latency are NOT the
same subject — they are different things.
  - Same identifier / equivalent expression = Score 4-5
  - Different identifier in the same broad domain (e.g. both PromQL latency queries but
    different metric names) = Score 1-2 (NOT 3 — they are not "same topic with different
    details", they are different subjects)
"""

_ANSWER_SIMILARITY_RUBRIC = {
    "score1_description": (
        f"{_ANSWER_SIMILARITY_INSTRUCTION}\n\n"
        "Score 1 - Unrelated: The response is about entirely different topics/entities than the reference. "
        "Or the response is just an error message with no topical overlap. "
        "Example: reference discusses 'database connection pool' but response says 'solver failed'."
    ),
    "score2_description": (
        "Score 2 - Tangentially Related: The response mentions the same system/service but discusses "
        "completely different aspects. Minimal topical overlap. "
        "Example: reference discusses 'slow DB queries due to missing index' but response discusses 'pod resource limits'."
    ),
    "score3_description": (
        "Score 3 - Same Topic, Different Details: The response addresses the same problem area "
        "and mentions some of the same entities, but the specific details and findings differ. "
        "Example: both discuss 'database performance' but reference says 'missing index' while response says 'connection timeouts'."
    ),
    "score4_description": (
        "Score 4 - Closely Aligned: The response covers the same topic with mostly the same details. "
        "Minor differences in specifics or phrasing. "
        "Example: reference says 'OOMKilled due to memory limit' and response says 'container killed because it exceeded memory'."
    ),
    "score5_description": (
        "Score 5 - Semantically Equivalent: The response conveys the same meaning as the reference, "
        "covering the same facts and conclusions, even if worded differently. "
        "Example: reference says '14 pods in test-1' and response says 'There are 14 pods in the test-1 namespace'."
    ),
}


def _get_answer_similarity_metric(llm):
    """Create the Answer Similarity RubricsScore metric (1-5)."""
    try:
        return RubricsScore(
            name="answer_similarity",
            rubrics=_ANSWER_SIMILARITY_RUBRIC,
            llm=llm,
        )
    except TypeError as e:
        logger.warning("RubricsScore init failed for similarity: %s, trying simplified", e)
        return RubricsScore(llm=llm)


def _get_answer_quality_metric(llm):
    """Create the Answer Quality RubricsScore metric (1-5)."""
    try:
        return RubricsScore(
            name="answer_quality",
            rubrics=_ANSWER_QUALITY_RUBRIC,
            llm=llm,
        )
    except TypeError as e:
        logger.warning("RubricsScore init failed for quality: %s, trying simplified", e)
        return RubricsScore(llm=llm)


def _safe_float(value, default: float = 0.0) -> float:
    """Convert a RAGAS score to float, treating NaN as default."""
    if value is None:
        return default
    f = float(value)
    return default if math.isnan(f) else f


# Score-formula schema version. Bump whenever _rubric_to_percent changes so
# stored EvalScore.score_schema_version distinguishes pre/post-formula runs;
# dashboards/reports compare runs only within the same schema.
#   v1 — `((score-1)/4)*100`  (Score 1 → 0%, Score 5 → 100%)
#   v2 — `(score/5)*100`      (Score 1 → 20%, Score 5 → 100%)
SCORE_SCHEMA_VERSION = 2


def _rubric_to_percent(score: float) -> float:
    """Convert a 1-5 rubric score to 0-100 percentage.

    Linear mapping: Score 1 → 20, Score 2 → 40, Score 3 → 60, Score 4 → 80, Score 5 → 100.
    0% is reserved for evaluator failures (no rubric score returned), so a Score 1 result
    (lowest valid grade) is distinguishable from a metric error.

    Pre-PR runs used `((score-1)/4)*100` (Score 1 → 0%). Cross-schema
    comparisons must check ``EvalScore.score_schema_version``.
    """
    return round((score / 5) * 100, 2)


@dataclass
class EvalScore:
    """Evaluation result with scores and reasons.

    A Score-1 rubric result (= 20%) is a valid low score; a metric failure
    (judge crashed, network error) is qualitatively different. The
    `similarity_failed` / `quality_failed` flags let consumers distinguish
    the two without changing the float type and breaking every downstream
    `result.similarity / 100.0` call site.
    """
    similarity: float = 0.0          # 0-100, LLM-based semantic similarity
    quality: float = 0.0             # 0-100, LLM-based factual correctness
    reason: str = ""                 # Combined feedback from all metrics
    similarity_reason: str = ""      # LLM judge feedback for similarity
    quality_reason: str = ""         # LLM judge feedback for quality
    # True when the corresponding metric raised. Distinguishes "judge
    # crashed" from "Score 1 → 20%" so dashboards can flag failures
    # rather than report a 0%/0% line that looks like a real failure.
    similarity_failed: bool = False
    quality_failed: bool = False
    # Identifies the score formula. Bumped when _rubric_to_percent changes.
    score_schema_version: int = SCORE_SCHEMA_VERSION


import concurrent.futures

_shared_executor = concurrent.futures.ThreadPoolExecutor(max_workers=4)


def _call_rubric_prompt(
    metric, query: str, answer: str, reference: str, eval_time=None
) -> Tuple[float, str]:
    """Call a RubricsScore metric prompt directly to get score + feedback.

    Args:
        eval_time: datetime to anchor "now" in the evaluator context. For live
            evaluation right after the agent ran, ``datetime.now()`` is fine.
            For re-evaluation of a stored answer, pass the original test's
            ``created_at`` so time-sensitive checks compare the agent's stored
            answer against the time it was actually produced — not the time
            the re-eval is running.

    Returns (score_0_100, feedback_text).
    """
    from datetime import datetime, timezone
    from ragas.metrics._domain_specific_rubrics import SingleTurnInputWithoutRubric

    # Inject the evaluation-anchor time as ground truth into the reference.
    # The judge LLM doesn't know "now" — without this anchor it falls back to
    # its training-cutoff prior and miscalls correct date answers as "future".
    if eval_time is None:
        eval_time = datetime.now(timezone.utc)
    elif eval_time.tzinfo is None:
        eval_time = eval_time.replace(tzinfo=timezone.utc)
    eval_iso = eval_time.strftime("%Y-%m-%dT%H:%M:%SZ")
    reference_with_time = (
        f"{reference}\n\n"
        f"[EVALUATOR CONTEXT — not part of the reference answer itself: "
        f"the agent's response was produced at {eval_iso}. When the reference "
        f"requires the response to reflect 'current' or 'recent' time, treat "
        f"this timestamp as ground truth, not your training cutoff. Allow up "
        f"to a few minutes of clock drift between the agent's reported time "
        f"and this anchor.]"
    )

    prompt_input = SingleTurnInputWithoutRubric(
        user_input=query,
        response=answer,
        reference=reference_with_time,
    )
    try:
        loop = asyncio.get_event_loop()
        if loop.is_running():
            output = _shared_executor.submit(
                asyncio.run,
                metric.single_turn_scoring_prompt.generate(
                    data=prompt_input, llm=metric.llm, callbacks=None
                ),
            ).result()
        else:
            output = loop.run_until_complete(
                metric.single_turn_scoring_prompt.generate(
                    data=prompt_input, llm=metric.llm, callbacks=None
                )
            )
    except RuntimeError:
        output = asyncio.run(
            metric.single_turn_scoring_prompt.generate(
                data=prompt_input, llm=metric.llm, callbacks=None
            )
        )

    raw_score = _safe_float(output.score)
    feedback = getattr(output, "feedback", "") or ""
    score = _rubric_to_percent(raw_score) if raw_score > 0 else 0.0
    return score, feedback


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def evaluate_single(
    query: str,
    answer: str,
    reference: str,
    llm,
    embeddings=None,
    eval_time=None,
) -> EvalScore:
    """Evaluate a single query-answer pair using LLM-based rubrics.

    Args:
        embeddings: Unused, kept for backward compatibility.
        eval_time: datetime anchor for time-sensitive checks. Defaults to
            ``datetime.now()`` (correct for live evaluation right after the
            agent runs). Re-evaluation paths should pass the test row's
            ``created_at`` so the judge compares against when the agent
            actually produced the answer, not when re-eval is running.

    Returns:
        EvalScore with similarity (0-100), quality (0-100), and reason.
    """
    result = EvalScore()

    # LLM-based similarity
    try:
        sim_metric = _get_answer_similarity_metric(llm)
        result.similarity, result.similarity_reason = _call_rubric_prompt(
            sim_metric, query, answer, reference, eval_time=eval_time
        )
    except Exception as e:
        logger.error("Similarity evaluation failed: %s", e)
        result.similarity_failed = True
        result.similarity_reason = f"[metric_failed] {e}"

    # LLM-based answer quality
    try:
        quality_metric = _get_answer_quality_metric(llm)
        result.quality, result.quality_reason = _call_rubric_prompt(
            quality_metric, query, answer, reference, eval_time=eval_time
        )
    except Exception as e:
        logger.error("Answer quality evaluation failed: %s", e)
        result.quality_failed = True
        result.quality_reason = f"[metric_failed] {e}"

    # Combined reason
    parts = []
    if result.similarity_reason:
        parts.append(f"[Similarity] {result.similarity_reason}")
    if result.quality_reason:
        parts.append(f"[Quality] {result.quality_reason}")
    result.reason = "\n".join(parts)

    return result


def evaluate_batch(
    queries: List[str],
    answers: List[str],
    references: List[str],
    llm,
    embeddings=None,
    eval_times=None,
) -> List[EvalScore]:
    """Evaluate a batch of query-answer pairs using LLM-based rubrics.

    Args:
        embeddings: Unused, kept for backward compatibility.
        eval_times: optional list of per-item datetime anchors aligned with
            ``queries``. Each entry is the timestamp at which the agent
            originally produced its answer (used as ground truth for
            time-sensitive rubric checks). For re-evaluation runs the caller
            MUST supply this; otherwise time-sensitive rubrics anchor on
            the re-eval clock and produce drifted scores.

    Returns:
        List of EvalScore with similarity, quality, and reason.
    """
    n = len(queries)
    results = [EvalScore() for _ in range(n)]

    sim_metric = _get_answer_similarity_metric(llm)
    quality_metric = _get_answer_quality_metric(llm)

    if eval_times is not None and len(eval_times) < n:
        logger.warning(
            "evaluate_batch: eval_times shorter than queries (%d vs %d); "
            "missing entries default to datetime.now() and will skew "
            "time-sensitive rubric scoring on re-eval runs",
            len(eval_times),
            n,
        )

    def _time_at(i):
        if eval_times and i < len(eval_times) and eval_times[i] is not None:
            return eval_times[i]
        return None

    for i in range(n):
        # LLM-based similarity
        try:
            results[i].similarity, results[i].similarity_reason = _call_rubric_prompt(
                sim_metric, queries[i], answers[i], references[i], eval_time=_time_at(i)
            )
        except Exception as e:
            logger.error("Similarity failed for item %d: %s", i, e)
            results[i].similarity_failed = True
            results[i].similarity_reason = f"[metric_failed] {e}"

        # LLM-based quality
        try:
            results[i].quality, results[i].quality_reason = _call_rubric_prompt(
                quality_metric, queries[i], answers[i], references[i], eval_time=_time_at(i)
            )
        except Exception as e:
            logger.error("Answer quality failed for item %d: %s", i, e)
            results[i].quality_failed = True
            results[i].quality_reason = f"[metric_failed] {e}"

        # Combined reason
        parts = []
        if results[i].similarity_reason:
            parts.append(f"[Similarity] {results[i].similarity_reason}")
        if results[i].quality_reason:
            parts.append(f"[Quality] {results[i].quality_reason}")
        results[i].reason = "\n".join(parts)

    return results
