"""A platform send failure must trigger a retry, and retries must stop and land in the
DLQ after task_retry_count attempts, instead of looping forever or being swallowed."""

import asyncio
import json

import pytest

from notifications_server.configs.settings import settings
from notifications_server.processors.notification import NotificationProcessor


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

    assert producer.calls == [("delayed", {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}})]


def test_process_task_moves_to_dlq_once_the_cap_is_reached():
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {"x-death": [{"count": settings.notifications.task_retry_count}]}
    asyncio.run(processor.process_task(headers, task))

    assert producer.calls == [("dlq", {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}})]


def test_death_count_sums_all_x_death_entries():
    assert NotificationProcessor._death_count({"x-death": [{"count": 2}, {"count": 3}]}) == 5


def test_death_count_is_zero_without_an_x_death_header():
    assert NotificationProcessor._death_count({}) == 0
    assert NotificationProcessor._death_count(None) == 0
