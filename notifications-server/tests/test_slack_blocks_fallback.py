"""post_to_slack_with_blocks_fallback (NB-41713): a template-built blocks list
(e.g. several native tables) can be rejected by Slack as a whole even though
no single block in it is individually too large - the real case is Slack's
undocumented aggregate_table_character_count_must_not_exceed_20000, confirmed
live against the real Slack API. Bisecting via send_blocks_with_fallback
recovers by splitting the send into as many messages as it takes, instead of
dropping the whole notification (see #41713).

What these pin:
  * a block-shape rejection on the full list bisects and still delivers,
    across more than one Slack message if needed
  * attachments (legacy, message-level) land on whichever send carries the
    tail of the original blocks, not necessarily the first one
  * a template with no blocks at all (e.g. slo.py's attachments-only card)
    still gets a plain single send
  * once any half has been sent, a further failure does not retry the whole
    thing (that would duplicate the half already delivered)
"""

from types import SimpleNamespace

from slack_sdk.errors import SlackApiError

from notifications_server.services import message as message_module
from notifications_server.services.message import SlackSender


class _FakeResponse:
    def __init__(self, error, errors=None, headers=None):
        self.data = {"error": error}
        if errors is not None:
            self.data["errors"] = errors
        self.headers = headers or {}


def _slack_error(error_code, errors=None, headers=None):
    return SlackApiError(message=f"error: {error_code}", response=_FakeResponse(error_code, errors, headers))


class _FakeChatClient:
    """poison(blocks) -> error_code to raise, or None to let the send through."""

    def __init__(self, poison):
        self.calls = []
        self._poison = poison

    def chat_post(self, *, token, channel_id=None, **kwargs):
        blocks = kwargs.get("blocks") or []
        error_code = self._poison(blocks)
        if error_code:
            headers = {"Retry-After": "0"} if error_code == "ratelimited" else {}
            raise _slack_error(error_code, headers=headers)
        self.calls.append(kwargs)
        return SimpleNamespace(status_code=200, data={"ok": True, "channel": channel_id, "ts": f"ts-{len(self.calls)}"})


def _sender(poison):
    sender = SlackSender.__new__(SlackSender)
    sender.slack_app = SimpleNamespace(client=_FakeChatClient(poison))
    return sender


_INSTALLATION = SimpleNamespace(to_channel='{"id": "C123"}', token="xoxb-test")
_BLOCKS = [{"type": "table", "id": i} for i in range(3)]


def _only_full_list_rejected(blocks):
    return "invalid_blocks" if len(blocks) == 3 else None


class TestBisection:
    def test_bisects_on_block_shape_rejection_and_recovers(self):
        sender = _sender(_only_full_list_rejected)
        response = sender.post_to_slack_with_blocks_fallback("t1", _INSTALLATION, text="hi", blocks=_BLOCKS)

        calls = sender.slack_app.client.calls
        assert [c["blocks"] for c in calls] == [[_BLOCKS[0]], [_BLOCKS[1], _BLOCKS[2]]]
        assert response.data["ok"] is True

    def test_attachments_land_on_the_last_bisected_send(self):
        sender = _sender(_only_full_list_rejected)
        attachment = {"color": "#2eb67d", "title": "footer"}
        sender.post_to_slack_with_blocks_fallback(
            "t1", _INSTALLATION, text="hi", blocks=_BLOCKS, attachments=[attachment]
        )

        calls = sender.slack_app.client.calls
        assert "attachments" not in calls[0]
        assert calls[1]["attachments"] == [attachment]

    def test_attachments_do_not_duplicate_when_a_block_object_is_reused(self):
        # A shared divider object (not a copy) at both bisected halves' tail -
        # object identity alone would wrongly match both halves.
        divider = {"type": "divider"}
        blocks = [{"type": "table", "id": 0}, divider, {"type": "table", "id": 1}, divider]

        def poison(block_list):
            return "invalid_blocks" if len(block_list) == 4 else None

        sender = _sender(poison)
        attachment = {"color": "#2eb67d", "title": "footer"}
        sender.post_to_slack_with_blocks_fallback(
            "t1", _INSTALLATION, text="hi", blocks=blocks, attachments=[attachment]
        )

        calls = sender.slack_app.client.calls
        assert len(calls) == 2
        assert "attachments" not in calls[0]
        assert calls[1]["attachments"] == [attachment]

    def test_no_blocks_template_still_sends_a_plain_message(self):
        sender = _sender(lambda blocks: None)
        attachment = {"color": "#E01E5A", "title": "SLO breach"}
        response = sender.post_to_slack_with_blocks_fallback("t1", _INSTALLATION, text="hi", attachments=[attachment])

        calls = sender.slack_app.client.calls
        assert len(calls) == 1
        assert "blocks" not in calls[0]
        assert calls[0]["attachments"] == [attachment]
        assert response.data["ok"] is True

    def test_partial_bisected_send_does_not_retry_and_duplicate(self):
        def poison(blocks):
            if len(blocks) == 3:
                return "invalid_blocks"
            if blocks == [_BLOCKS[1], _BLOCKS[2]]:
                return "ratelimited"
            return None

        sender = _sender(poison)
        response = sender.post_to_slack_with_blocks_fallback("t1", _INSTALLATION, text="hi", blocks=_BLOCKS)

        calls = sender.slack_app.client.calls
        assert response.data["error"] == "ratelimited"  # the real error, for the caller to classify
        assert len(calls) == 1  # the first half, delivered exactly once - not resent
        assert calls[0]["blocks"] == [_BLOCKS[0]]

    def test_retries_the_whole_send_on_rate_limit_before_anything_succeeded(self, monkeypatch):
        monkeypatch.setattr(message_module.time, "sleep", lambda *_: None)
        attempts = {"n": 0}

        def poison(blocks):
            attempts["n"] += 1
            return "ratelimited" if attempts["n"] == 1 else None

        sender = _sender(poison)
        response = sender.post_to_slack_with_blocks_fallback("t1", _INSTALLATION, text="hi", blocks=_BLOCKS)

        assert response.data["ok"] is True
        assert len(sender.slack_app.client.calls) == 1
