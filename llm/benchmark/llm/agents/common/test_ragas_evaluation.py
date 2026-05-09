"""Unit tests for the load-bearing pieces of ragas_evaluation: the score
formula, EvalScore failure flags, and the eval_times fallback behavior.

These tests intentionally do not exercise the LLM-backed paths (which require
a live model) — they pin the deterministic plumbing so a future change can't
silently regress the score-shift, the metric-failure marker, or the
re-evaluation time anchor.
"""
import logging
import unittest
from datetime import datetime, timezone

from llm.agents.common.ragas_evaluation import (
    SCORE_SCHEMA_VERSION,
    EvalScore,
    _rubric_to_percent,
)


class RubricToPercentTest(unittest.TestCase):
    def test_linear_mapping_v2(self):
        """Score N → N*20%. Score 1 is 20%, not 0%; 0% is reserved for
        evaluator failures (signaled via similarity_failed/quality_failed)."""
        self.assertEqual(_rubric_to_percent(1), 20.0)
        self.assertEqual(_rubric_to_percent(2), 40.0)
        self.assertEqual(_rubric_to_percent(3), 60.0)
        self.assertEqual(_rubric_to_percent(4), 80.0)
        self.assertEqual(_rubric_to_percent(5), 100.0)

    def test_schema_version_is_2(self):
        # Bump in lockstep with formula changes; consumers gate cross-version
        # comparisons on this constant.
        self.assertEqual(SCORE_SCHEMA_VERSION, 2)


class EvalScoreFailureFlagsTest(unittest.TestCase):
    def test_default_score_is_not_failed(self):
        s = EvalScore()
        self.assertFalse(s.similarity_failed)
        self.assertFalse(s.quality_failed)
        self.assertEqual(s.score_schema_version, SCORE_SCHEMA_VERSION)

    def test_failure_distinct_from_score_one(self):
        """A judge crash and a Score-1 outcome must be distinguishable —
        otherwise the dashboard shows them as the same 0%/20% line."""
        crashed = EvalScore(similarity=0.0, similarity_failed=True)
        score_one = EvalScore(similarity=20.0)  # Score 1 mapped to 20%
        self.assertNotEqual(crashed.similarity_failed, score_one.similarity_failed)


class EvalTimesFallbackTest(unittest.TestCase):
    """The Warn log when eval_times is shorter than queries is the only
    signal a re-eval operator gets that some test rows fell back to
    `now()`. If this regresses, the warning vanishes silently and re-eval
    scores drift without trace."""

    def test_warns_when_eval_times_shorter_than_queries(self):
        from llm.agents.common import ragas_evaluation as mod

        # Stub _get_answer_similarity_metric / _get_answer_quality_metric
        # so we don't try to instantiate real RAGAS metrics in unit tests.
        class _FakeMetric:
            pass

            async def _ascore(self, *_a, **_k):
                return 5

        original_sim = mod._get_answer_similarity_metric
        original_quality = mod._get_answer_quality_metric
        original_call = mod._call_rubric_prompt
        mod._get_answer_similarity_metric = lambda llm: _FakeMetric()
        mod._get_answer_quality_metric = lambda llm: _FakeMetric()
        mod._call_rubric_prompt = lambda *a, **k: (100.0, "ok")
        try:
            with self.assertLogs(mod.logger, level=logging.WARNING) as cm:
                mod.evaluate_batch(
                    queries=["q1", "q2", "q3"],
                    answers=["a1", "a2", "a3"],
                    references=["r1", "r2", "r3"],
                    llm=None,
                    eval_times=[datetime.now(timezone.utc)],  # only 1 of 3
                )
            self.assertTrue(
                any("eval_times shorter than queries" in m for m in cm.output),
                f"Expected warning about short eval_times, got: {cm.output}",
            )
        finally:
            mod._get_answer_similarity_metric = original_sim
            mod._get_answer_quality_metric = original_quality
            mod._call_rubric_prompt = original_call

    def test_no_warning_when_eval_times_aligned(self):
        from llm.agents.common import ragas_evaluation as mod

        class _FakeMetric:
            pass

        original_sim = mod._get_answer_similarity_metric
        original_quality = mod._get_answer_quality_metric
        original_call = mod._call_rubric_prompt
        mod._get_answer_similarity_metric = lambda llm: _FakeMetric()
        mod._get_answer_quality_metric = lambda llm: _FakeMetric()
        mod._call_rubric_prompt = lambda *a, **k: (100.0, "ok")
        try:
            ts = datetime.now(timezone.utc)
            # Use a logger handler to capture without requiring assertLogs
            # to fire (which would itself raise if no log was produced).
            handler_records = []

            class _Capture(logging.Handler):
                def emit(self, record):
                    handler_records.append(record)

            handler = _Capture(level=logging.WARNING)
            mod.logger.addHandler(handler)
            try:
                mod.evaluate_batch(
                    queries=["q1", "q2"],
                    answers=["a1", "a2"],
                    references=["r1", "r2"],
                    llm=None,
                    eval_times=[ts, ts],
                )
            finally:
                mod.logger.removeHandler(handler)

            short_warnings = [
                r for r in handler_records if "eval_times shorter than queries" in r.getMessage()
            ]
            self.assertEqual(short_warnings, [])
        finally:
            mod._get_answer_similarity_metric = original_sim
            mod._get_answer_quality_metric = original_quality
            mod._call_rubric_prompt = original_call


if __name__ == "__main__":
    unittest.main()
