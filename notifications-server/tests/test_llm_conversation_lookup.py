from unittest.mock import MagicMock

from notifications_server.repositories.user_repository import get_llm_conversation_by_session


def test_get_llm_conversation_by_session_orders_by_updated_at_desc():
    session = MagicMock()
    query_mock = MagicMock()
    filter_mock = MagicMock()
    order_by_mock = MagicMock()

    session.query.return_value = query_mock
    query_mock.filter.return_value = filter_mock
    filter_mock.order_by.return_value = order_by_mock
    order_by_mock.first.return_value = None

    result = get_llm_conversation_by_session(session, "event-abc123")

    assert result is None
    filter_mock.order_by.assert_called_once()
    order_by_mock.first.assert_called_once()
