"""Unit tests for get_token_metrics' retry-while-empty behavior.

llm-server writes llm_conversation_token_usage rows on a background worker
pool, so a query made right after the LLM response returns can briefly see
zero rows (total_requests == 0). These tests pin the retry loop that closes
that race, without hitting a real llm-server or actually sleeping.
"""

import unittest
from unittest.mock import Mock, patch

from llm.agents.common.metrics import get_token_metrics


def _response(conversation):
    resp = Mock()
    resp.raise_for_status = Mock()
    resp.json = Mock(return_value={"data": {"conversation": conversation}})
    return resp


class GetTokenMetricsRetryTest(unittest.TestCase):
    def test_returns_immediately_when_usage_already_present(self):
        conv = {
            "total_requests": 2,
            "total_input_tokens": 100,
            "total_output_tokens": 20,
            "total_cost_usd": 0.05,
        }
        with patch("llm.agents.common.metrics.requests.post", return_value=_response(conv)) as mock_post, patch(
            "llm.agents.common.metrics.time.sleep"
        ) as mock_sleep:
            result = get_token_metrics("sess-1", "acct-1", "tenant-1", "user-1")

        self.assertEqual(mock_post.call_count, 1)
        mock_sleep.assert_not_called()
        self.assertEqual(result["input_tokens"], 100)
        self.assertEqual(result["cost"], 0.05)

    def test_retries_until_usage_lands(self):
        empty_conv = {"total_requests": 0}
        populated_conv = {
            "total_requests": 1,
            "total_input_tokens": 50,
            "total_output_tokens": 10,
            "total_cost_usd": 0.01,
        }
        responses = [
            _response(empty_conv),
            _response(empty_conv),
            _response(populated_conv),
        ]
        with patch("llm.agents.common.metrics.requests.post", side_effect=responses) as mock_post, patch(
            "llm.agents.common.metrics.time.sleep"
        ) as mock_sleep:
            result = get_token_metrics("sess-2", "acct-1", "tenant-1", "user-1")

        self.assertEqual(mock_post.call_count, 3)
        self.assertEqual(mock_sleep.call_count, 2)
        self.assertEqual(result["total_requests"], 1)
        self.assertEqual(result["cost"], 0.01)

    def test_returns_last_result_after_exhausting_retries(self):
        empty_conv = {"total_requests": 0}
        with patch(
            "llm.agents.common.metrics.requests.post",
            return_value=_response(empty_conv),
        ) as mock_post, patch("llm.agents.common.metrics.time.sleep") as mock_sleep:
            result = get_token_metrics("sess-3", "acct-1", "tenant-1", "user-1")

        # 5 attempts total, sleeping between each of the first 4.
        self.assertEqual(mock_post.call_count, 5)
        self.assertEqual(mock_sleep.call_count, 4)
        self.assertIsNotNone(result)
        self.assertEqual(result["total_requests"], 0)
        self.assertEqual(result["input_tokens"], 0)

    def test_no_retry_on_request_exception(self):
        import requests

        with patch(
            "llm.agents.common.metrics.requests.post",
            side_effect=requests.exceptions.ConnectionError("boom"),
        ) as mock_post, patch("llm.agents.common.metrics.time.sleep") as mock_sleep:
            result = get_token_metrics("sess-4", "acct-1", "tenant-1", "user-1")

        self.assertEqual(mock_post.call_count, 1)
        mock_sleep.assert_not_called()
        self.assertIsNone(result)

    def test_no_session_id_returns_none_without_request(self):
        with patch("llm.agents.common.metrics.requests.post") as mock_post:
            result = get_token_metrics("", "acct-1", "tenant-1", "user-1")

        mock_post.assert_not_called()
        self.assertIsNone(result)


if __name__ == "__main__":
    unittest.main()
