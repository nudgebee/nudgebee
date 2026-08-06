from unittest.mock import MagicMock

from notifications_server.services.common import CommonService


def _service():
    return CommonService(engine=MagicMock(), slack_app=MagicMock(), teams_app=MagicMock())


class TestGetMessageAttachments:
    """Regression coverage: a recurring finding is posted as a reply into an
    existing thread (message.py's check_if_sent_already path), so
    message_ts != thread_ts for it. conversations.replies always returns the
    thread PARENT first regardless of which ts is passed as thread_ts --
    fetching by message_ts alone would silently resolve to a different
    finding's card."""

    def test_picks_the_specific_message_from_the_thread_not_the_parent(self):
        service = _service()
        service.get_slack_installation = MagicMock(return_value=MagicMock(token="tok"))
        service.slack_app.client.conversations_replies.return_value = {
            "messages": [
                {"ts": "100.000", "attachments": [{"text": "parent finding"}]},
                {"ts": "200.000", "attachments": [{"text": "clicked finding"}]},
            ]
        }

        result = service.get_message_attachments("C1", "T1", thread_ts="100.000", message_ts="200.000")

        assert result == [{"text": "clicked finding"}]
        service.slack_app.client.conversations_replies.assert_called_once_with(
            token="tok", channel_id="C1", thread_ts="100.000"
        )

    def test_top_level_message_resolves_to_itself(self):
        service = _service()
        service.get_slack_installation = MagicMock(return_value=MagicMock(token="tok"))
        service.slack_app.client.conversations_replies.return_value = {
            "messages": [{"ts": "100.000", "attachments": [{"text": "the card"}]}]
        }

        result = service.get_message_attachments("C1", "T1", thread_ts="100.000", message_ts="100.000")

        assert result == [{"text": "the card"}]

    def test_returns_none_when_message_not_found(self):
        service = _service()
        service.get_slack_installation = MagicMock(return_value=MagicMock(token="tok"))
        service.slack_app.client.conversations_replies.return_value = {
            "messages": [{"ts": "100.000", "attachments": []}]
        }

        assert service.get_message_attachments("C1", "T1", thread_ts="100.000", message_ts="999.000") is None

    def test_returns_none_on_api_error(self):
        service = _service()
        service.get_slack_installation = MagicMock(return_value=MagicMock(token="tok"))
        service.slack_app.client.conversations_replies.side_effect = Exception("boom")

        assert service.get_message_attachments("C1", "T1", thread_ts="100.000", message_ts="100.000") is None
