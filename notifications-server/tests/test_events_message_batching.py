"""Tests for how Events.handle_final_response batches an answer's content
groups (one chart, one table, one text chunk) into Slack messages, and how
it degrades when Slack rejects one of those messages outright.

What these pin:
  * a prose/table/prose/table/prose answer sends as ONE Slack message
    instead of fragmenting into one message per group (was 5 pre-fix)
  * batching only splits when the next group would exceed
    SLACK_MAX_BLOCKS_PER_MESSAGE
  * a deterministic block-shape rejection (invalid_blocks and friends) gets
    bisected and retried in halves, isolating exactly which block caused it
    - only that one block degrades to plain text, every other block in the
    batch still sends natively instead of the whole batch being discarded
  * a transient/systemic failure (ratelimited, invalid_auth, a network
    error, ...) is NOT bisected - it propagates on the first attempt instead
    of retrying the same doomed request up to ~3x the block count
  * a failed nb-chart parse shows the raw JSON in a code fence, not as
    mangled plain mrkdwn
"""

import pytest
from slack_sdk.errors import SlackApiError

from notifications_server.services import events as events_module
from notifications_server.services.events import Events


class _FakeSlackResponse:
    def __init__(self, error):
        self.data = {"error": error}


def _slack_api_error(error_code):
    return SlackApiError(message=f"error: {error_code}", response=_FakeSlackResponse(error_code))


class _FakeCommonService:
    def __init__(self, poison_ids=None, error_code="invalid_blocks", raise_non_slack_error=False, always_fail=False):
        self.slack_messages = []
        self.call_count = 0
        self._poison_ids = set(poison_ids or [])
        self._error_code = error_code
        self._raise_non_slack_error = raise_non_slack_error
        self._always_fail = always_fail

    def slack_reply_in_thread(self, channel_id, team_id, thread_ts, message, transform_to_markdown=True):
        self.call_count += 1
        ids_in_message = {b.get("id") for b in message if "id" in b}
        if self._always_fail or (ids_in_message & self._poison_ids):
            if self._raise_non_slack_error:
                raise ConnectionError("simulated network blip")
            raise _slack_api_error(self._error_code)
        self.slack_messages.append(message)


class _Payload:
    def __init__(self, response):
        self.response = response


def _svc(monkeypatch, common_service=None):
    events = Events.__new__(Events)
    events.common_service = common_service or _FakeCommonService()
    monkeypatch.setattr(events_module.event_cache, "update_event_entry", lambda *a, **kw: None)
    return events


def _dummy_blocks(n):
    return [{"type": "section", "id": i, "text": {"type": "mrkdwn", "text": f"block {i}"}} for i in range(n)]


PROSE_TABLE_PROSE_TABLE_PROSE = (
    "Here is the first breakdown:\n\n"
    "| Service | Cost |\n|---|---|\n| EC2 | 100 |\n\n"
    "And here is a second one:\n\n"
    "| Service | Cost |\n|---|---|\n| RDS | 50 |\n\n"
    "Let me know if you need more detail."
)


class TestBatchSlackGroups:
    def test_merges_small_groups_into_one_message(self):
        groups = [[{"type": "section"}] for _ in range(10)]
        messages = Events._batch_slack_groups(groups)
        assert len(messages) == 1
        assert len(messages[0]) == 10

    def test_splits_only_when_cap_would_be_exceeded(self):
        groups = [[{"type": "section"}] for _ in range(70)]
        messages = Events._batch_slack_groups(groups)
        assert [len(m) for m in messages] == [32, 32, 6]

    def test_empty_input_returns_no_messages(self):
        assert Events._batch_slack_groups([]) == []

    def test_single_empty_group_still_produces_one_message(self):
        # An empty/whitespace-only response still has one group (a
        # MarkdownBlock with no text, which transforms to zero Slack
        # blocks) - the thread must still get exactly one reply, not none.
        assert Events._batch_slack_groups([[]]) == [[]]


class TestFallbackSlackBlock:
    def test_chart_block_becomes_a_text_note(self):
        block = {
            "type": "data_visualization",
            "title": "Cost by Service",
            "chart": {"type": "bar", "series": [{"name": "s", "data": [{"label": "a", "value": 1}]}]},
        }
        fallback = Events._fallback_slack_block(block)
        assert len(fallback) == 2
        assert fallback[0]["type"] == "section"
        assert "Cost by Service" in fallback[0]["text"]["text"]
        # The raw chart payload is shown in a code block, not just a notice -
        # mirrors mermaid_chart.py/nb_chart.py's own fallbacks, which always
        # show raw content so the numbers are still visible.
        assert fallback[1]["text"]["text"].startswith("```")
        assert '"type": "bar"' in fallback[1]["text"]["text"]

    def test_table_block_becomes_a_text_note_with_raw_rows(self):
        block = {"type": "table", "rows": [[{"type": "raw_text", "text": "A"}]]}
        fallback = Events._fallback_slack_block(block)
        assert len(fallback) == 2
        assert "table" in fallback[0]["text"]["text"].lower()
        assert fallback[1]["text"]["text"].startswith("```")
        assert '"text": "A"' in fallback[1]["text"]["text"]

    def test_unrecognized_block_falls_back_to_generic_notice(self):
        assert Events._fallback_slack_block({"type": "unknown_type"}) == [
            {"type": "section", "text": {"type": "mrkdwn", "text": "_Part of this response couldn't be displayed._"}}
        ]


class TestSendMessageWithFallback:
    def test_successful_send_is_sent_as_is(self, monkeypatch):
        common_service = _FakeCommonService()
        svc = _svc(monkeypatch, common_service)

        blocks = _dummy_blocks(5)
        svc._send_message_with_fallback("C1", "T1", "1.1", blocks)

        assert common_service.slack_messages == [blocks]

    def test_bisects_to_isolate_and_replace_only_the_offending_block(self, monkeypatch):
        # Block 5 (of 10) is the only one Slack rejects with invalid_blocks -
        # a deterministic, block-shape-specific error. Bisection must find
        # it, replace only it with fallback text, and still deliver every
        # other block intact - not degrade the whole batch just because one
        # block in it was bad.
        common_service = _FakeCommonService(poison_ids={5}, error_code="invalid_blocks")
        svc = _svc(monkeypatch, common_service)

        svc._send_message_with_fallback("C1", "T1", "1.1", _dummy_blocks(10))

        sent_ids = [b.get("id") for m in common_service.slack_messages for b in m if "id" in b]
        assert sorted(sent_ids) == [0, 1, 2, 3, 4, 6, 7, 8, 9]

        fallback_messages = [m for m in common_service.slack_messages if "id" not in m[0]]
        assert len(fallback_messages) == 1
        assert "couldn't be displayed" in fallback_messages[0][0]["text"]["text"]

    @pytest.mark.parametrize("error_code", ["invalid_blocks", "invalid_blocks_format", "msg_too_long"])
    def test_every_bisectable_error_code_triggers_bisection(self, monkeypatch, error_code):
        common_service = _FakeCommonService(poison_ids={0}, error_code=error_code)
        svc = _svc(monkeypatch, common_service)

        svc._send_message_with_fallback("C1", "T1", "1.1", _dummy_blocks(1))

        assert len(common_service.slack_messages) == 1
        assert "couldn't be displayed" in common_service.slack_messages[0][0]["text"]["text"]

    def test_single_offending_block_alone_falls_back_without_crashing(self, monkeypatch):
        common_service = _FakeCommonService(poison_ids={0})
        svc = _svc(monkeypatch, common_service)

        svc._send_message_with_fallback("C1", "T1", "1.1", _dummy_blocks(1))

        assert len(common_service.slack_messages) == 1
        assert "couldn't be displayed" in common_service.slack_messages[0][0]["text"]["text"]

    def test_empty_blocks_sends_nothing(self, monkeypatch):
        common_service = _FakeCommonService()
        svc = _svc(monkeypatch, common_service)

        svc._send_message_with_fallback("C1", "T1", "1.1", [])

        assert common_service.slack_messages == []


class TestSendMessageWithFallbackDoesNotBisectTransientFailures:
    @pytest.mark.parametrize("error_code", ["ratelimited", "invalid_auth", "not_authed", "internal_error"])
    def test_non_bisectable_slack_error_propagates_on_first_attempt(self, monkeypatch, error_code):
        # These errors say nothing about this specific block shape - every
        # half would fail again for the same reason, turning one outage into
        # a request storm. Must raise immediately, with exactly one attempt.
        common_service = _FakeCommonService(poison_ids={0, 1, 2, 3}, error_code=error_code)
        svc = _svc(monkeypatch, common_service)

        with pytest.raises(SlackApiError):
            svc._send_message_with_fallback("C1", "T1", "1.1", _dummy_blocks(4))

        assert common_service.call_count == 1
        assert common_service.slack_messages == []

    def test_non_slack_exception_propagates_without_bisecting(self, monkeypatch):
        # A network blip, timeout, etc. isn't even a SlackApiError - must not
        # be caught by the bisection logic at all.
        common_service = _FakeCommonService(poison_ids={0, 1, 2, 3}, raise_non_slack_error=True)
        svc = _svc(monkeypatch, common_service)

        with pytest.raises(ConnectionError):
            svc._send_message_with_fallback("C1", "T1", "1.1", _dummy_blocks(4))

        assert common_service.call_count == 1

    def test_bisectable_error_does_not_amplify_beyond_expected_call_count(self, monkeypatch):
        # Regression guard for the request-storm failure mode: for N blocks
        # with exactly one offending block, total attempts must stay
        # bounded (~2*N), not blow up if a transient error were mistaken
        # for something bisectable.
        common_service = _FakeCommonService(poison_ids={5}, error_code="invalid_blocks")
        svc = _svc(monkeypatch, common_service)

        svc._send_message_with_fallback("C1", "T1", "1.1", _dummy_blocks(10))

        assert common_service.call_count <= 20


class TestRenderPlainTextBlocksNbChartFallback:
    def test_failed_nb_chart_parse_is_wrapped_in_a_code_fence(self, monkeypatch):
        events = Events.__new__(Events)
        # Parses as JSON with a recognized "type"/"labels" shape (so it's
        # detected as nb-chart) but has no usable series data, so
        # render_nb_chart returns [] and this falls through to the fallback.
        bad_json = '{"type":"bar","labels":[],"series":[]}'
        text = f"before\n```nb-chart\n{bad_json}\n```\nafter"

        groups = events._render_plain_text_blocks(text)
        fenced_texts = [b.text for group in groups for b in group if hasattr(b, "text") and bad_json in b.text]

        assert len(fenced_texts) == 1
        assert fenced_texts[0] == f"```\n{bad_json}\n```"


class TestHandleFinalResponseBatching:
    def test_prose_table_prose_table_prose_sends_as_one_message(self, monkeypatch):
        common_service = _FakeCommonService()
        svc = _svc(monkeypatch, common_service)

        svc.handle_final_response(_Payload(PROSE_TABLE_PROSE_TABLE_PROSE), {}, "C123", "1779952241.483549", "T123")

        # Pre-fix this was 5 separate chat.postMessage calls (prose, table,
        # prose, table, prose) - all of it fits well under the batch cap, so
        # it must collapse into exactly one Slack message.
        assert len(common_service.slack_messages) == 1
        block_types = [b.get("type") for b in common_service.slack_messages[0]]
        assert block_types.count("table") == 2
        assert block_types.count("section") == 3

    def test_mention_lands_on_the_single_batched_message(self, monkeypatch):
        common_service = _FakeCommonService()
        svc = _svc(monkeypatch, common_service)

        svc.handle_final_response(
            _Payload(PROSE_TABLE_PROSE_TABLE_PROSE),
            {"slack_user_id": "U42"},
            "C123",
            "1779952241.483549",
            "T123",
        )

        assert len(common_service.slack_messages) == 1
        last_block = common_service.slack_messages[0][-1]
        assert last_block["type"] == "context"
        assert "U42" in last_block["elements"][0]["text"]

    def test_transient_slack_failure_is_caught_by_the_outer_handler(self, monkeypatch):
        # A systemic failure during send must not crash the whole callback -
        # handle_final_response's outer except already exists for this and
        # posts the generic "can't show results" reply via self.reply.
        common_service = _FakeCommonService(always_fail=True, error_code="ratelimited")
        svc = _svc(monkeypatch, common_service)
        reply_calls = []
        svc.reply = lambda channel_id, team_id, thread_ts, message: reply_calls.append(message)

        svc.handle_final_response(_Payload("hello"), {}, "C123", "1779952241.483549", "T123")

        assert len(reply_calls) == 1
        assert "can't show results" in reply_calls[0]
