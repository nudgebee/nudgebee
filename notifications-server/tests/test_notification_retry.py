"""A platform send failure must trigger a retry, and retries must stop and land in the
DLQ after task_retry_count attempts, instead of looping forever or being swallowed. A
retry must also be scoped to the platforms that actually failed — a platform that
already succeeded must not be delivered to again (#41040 regression: a partial failure
was resending the whole notification, duplicating already-delivered Slack/Teams/etc.
messages on every retry)."""

import asyncio
import json

import pytest

from notifications_server.configs.settings import settings
from notifications_server.processors.notification import NotificationProcessor, NotificationSendFailed


class _FakeMessageService:
    def __init__(self, responses):
        self.responses = responses

    async def send_message(self, _parsed):
        return self.responses


class _FakeTaskProducer:
    def __init__(self):
        self.calls = []

    async def publish_delayed_message(self, message):
        self.calls.append(("delayed", message))

    async def publish_dlq_message(self, message):
        self.calls.append(("dlq", message))


def _processor(responses, task_producer=None):
    processor = NotificationProcessor.__new__(NotificationProcessor)
    processor._message_service = _FakeMessageService(responses)
    processor.task_producer = task_producer or _FakeTaskProducer()
    return processor


def test_send_notification_raises_when_a_platform_send_fails():
    processor = _processor([{"platform": "slack", "status": "failed", "reason": "channel_not_found"}])

    with pytest.raises(RuntimeError):
        asyncio.run(processor.send_notification({"tenant_id": "t1", "parameters": {"message": "hi"}}))


def test_send_notification_raise_carries_only_the_failed_platforms():
    processor = _processor(
        [
            {"platform": "slack", "status": "success"},
            {"platform": "ms_teams", "status": "failed", "reason": "channel_not_found"},
        ]
    )

    with pytest.raises(NotificationSendFailed) as exc_info:
        asyncio.run(processor.send_notification({"tenant_id": "t1", "parameters": {"message": "hi"}}))

    assert exc_info.value.failed_platforms == ["ms_teams"]


def test_send_notification_does_not_raise_on_success_or_benign_skips():
    processor = _processor(
        [
            {"platform": "slack", "status": "success"},
            {"platform": "system", "status": "suppressed"},
        ]
    )

    asyncio.run(processor.send_notification({"tenant_id": "t1", "parameters": {"message": "hi"}}))


def test_process_task_retries_via_delayed_queue_below_the_cap():
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {"x-death": [{"count": settings.notifications.task_retry_count - 1}]}
    asyncio.run(processor.process_task(headers, task))

    assert producer.calls == [
        (
            "delayed",
            {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}, "retry_platforms": ["slack"]},
        )
    ]


def test_process_task_moves_to_dlq_once_the_cap_is_reached():
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {"x-death": [{"count": settings.notifications.task_retry_count}]}
    asyncio.run(processor.process_task(headers, task))

    assert producer.calls == [
        (
            "dlq",
            {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}, "retry_platforms": ["slack"]},
        )
    ]


def test_process_task_retry_only_targets_the_platform_that_failed():
    """The platform that already succeeded (slack) must be dropped from the
    retried task, not resent alongside the one that failed (ms_teams)."""
    producer = _FakeTaskProducer()
    processor = _processor(
        [
            {"platform": "slack", "status": "success"},
            {"platform": "ms_teams", "status": "failed", "reason": "channel_not_found"},
        ],
        producer,
    )
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {"x-death": [{"count": settings.notifications.task_retry_count - 1}]}
    asyncio.run(processor.process_task(headers, task))

    [(_, retried_task)] = producer.calls
    assert retried_task["retry_platforms"] == ["ms_teams"]


def test_death_count_sums_all_x_death_entries():
    assert NotificationProcessor._death_count({"x-death": [{"count": 2}, {"count": 3}]}) == 5


def test_death_count_is_zero_without_an_x_death_header():
    assert NotificationProcessor._death_count({}) == 0
    assert NotificationProcessor._death_count(None) == 0
