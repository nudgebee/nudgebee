"""Unit tests for compute_per_query_delta's per-query metrics.

Pins the fields the Compare Reports UI reads per row: duration (already
followup-wait-excluded upstream), token counts, tool call success/failure
split, and the session_id used to build an "open this run" link.
"""

import unittest

from llm.agents.common.compare_reports import compute_per_query_delta


def _detail(**overrides):
    base = {
        "test_id": "t1",
        "answer_similarity": 0.8,
        "answer_relevancy": 0.8,
        "duration_seconds": 10.0,
        "tool_calls_total": 4,
        "tool_calls_successful": 3,
        "tool_names": ["get_pods"],
        "cost": 0.01,
        "input_tokens": 100,
        "output_tokens": 20,
        "session_id": "sess-base",
        "tags": ["k8s"],
    }
    base.update(overrides)
    return base


class ComputePerQueryDeltaTest(unittest.TestCase):
    def test_reports_tokens_tool_split_and_session_id_per_side(self):
        baseline = {"details": [_detail()]}
        candidate = {
            "details": [
                _detail(
                    tool_calls_total=5,
                    tool_calls_successful=5,
                    input_tokens=150,
                    output_tokens=30,
                    session_id="sess-cand",
                )
            ]
        }

        result = compute_per_query_delta(baseline, candidate, threshold=5.0)
        entry = result["per_query"][0]

        self.assertEqual(entry["input_tokens"], {"baseline": 100, "candidate": 150})
        self.assertEqual(entry["output_tokens"], {"baseline": 20, "candidate": 30})
        self.assertEqual(
            entry["tool_calls"],
            {
                "baseline": 4,
                "candidate": 5,
                "delta": 1,
                "baseline_successful": 3,
                "baseline_failed": 1,
                "candidate_successful": 5,
                "candidate_failed": 0,
            },
        )
        self.assertEqual(
            entry["session_id"], {"baseline": "sess-base", "candidate": "sess-cand"}
        )

    def test_none_values_do_not_raise(self):
        """A nullable DB column can land as an explicit None (not just a
        missing key) in an older cached report_json. `.get(key, default)`
        doesn't catch that — only `.get(key) or default` does — so a None
        here must not raise TypeError doing arithmetic on it."""
        none_detail = _detail(
            tool_calls_total=None,
            tool_calls_successful=None,
            duration_seconds=None,
            cost=None,
            input_tokens=None,
            output_tokens=None,
            session_id=None,
        )
        baseline = {"details": [none_detail]}
        candidate = {"details": [_detail()]}

        result = compute_per_query_delta(baseline, candidate, threshold=5.0)
        entry = result["per_query"][0]

        self.assertEqual(entry["input_tokens"]["baseline"], 0)
        self.assertEqual(entry["output_tokens"]["baseline"], 0)
        self.assertEqual(entry["session_id"]["baseline"], "")
        self.assertEqual(entry["tool_calls"]["baseline"], 0)
        self.assertEqual(entry["tool_calls"]["baseline_successful"], 0)
        self.assertEqual(entry["tool_calls"]["baseline_failed"], 0)
        self.assertEqual(entry["latency_seconds"]["baseline"], 0)


if __name__ == "__main__":
    unittest.main()
