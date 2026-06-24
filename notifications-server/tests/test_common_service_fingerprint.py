import pytest
from unittest.mock import MagicMock

from notifications_server.services.common import CommonService


def test_get_channel_and_ts_from_sent_notifications_no_delimiter():
    # Setup mock dependencies
    engine_mock = MagicMock()
    slack_app_mock = MagicMock()
    teams_app_mock = MagicMock()

    service = CommonService(engine=engine_mock, slack_app=slack_app_mock, teams_app=teams_app_mock)

    # Mock the database session and the SQLAlchemy query chain
    service.session = MagicMock()
    query_mock = MagicMock()
    filter_by_mock = MagicMock()
    order_by_mock = MagicMock()
    limit_mock = MagicMock()

    service.session.query.return_value = query_mock
    query_mock.filter_by.return_value = filter_by_mock
    filter_by_mock.order_by.return_value = order_by_mock
    order_by_mock.limit.return_value = limit_mock
    limit_mock.first.return_value = None  # Simulating no notification found

    # Execution
    # This should not raise an IndexError
    result = service.get_channel_and_ts_from_sent_notifications("nodelimiter")

    # Verification
    # Ensure it returns the default fallback when no notification is found
    assert result == (None, None, None, None)

    # Verify the fingerprint used was the entire string since there's no prefix delimiter
    service.session.query.assert_called_once()
    query_mock.filter_by.assert_called_once_with(fingerprint="nodelimiter")


def test_get_channel_and_ts_from_sent_notifications_with_delimiter():
    # Setup mock dependencies
    engine_mock = MagicMock()
    slack_app_mock = MagicMock()
    teams_app_mock = MagicMock()

    service = CommonService(engine=engine_mock, slack_app=slack_app_mock, teams_app=teams_app_mock)

    service.session = MagicMock()
    query_mock = MagicMock()
    filter_by_mock = MagicMock()
    order_by_mock = MagicMock()
    limit_mock = MagicMock()

    service.session.query.return_value = query_mock
    query_mock.filter_by.return_value = filter_by_mock
    filter_by_mock.order_by.return_value = order_by_mock
    order_by_mock.limit.return_value = limit_mock
    limit_mock.first.return_value = None

    # Execution
    result = service.get_channel_and_ts_from_sent_notifications("prefix-thefingerprint")

    # Verification
    assert result == (None, None, None, None)

    # Verify the prefix is split off correctly
    service.session.query.assert_called_once()
    query_mock.filter_by.assert_called_once_with(fingerprint="thefingerprint")
