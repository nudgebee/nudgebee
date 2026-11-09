"""A platform send failure must trigger a retry, and retries must stop and land in the
DLQ after task_retry_count attempts, instead of looping forever or being swallowed. A
retry must also be scoped to the platforms that actually failed — a platform that
already succeeded must not be delivered to again (#41040 regression: a partial failure
was resending the whole notification, duplicating already-delivered Slack/Teams/etc.
messages on every retry)."""

import asyncio
import json
import smtplib

import pytest

from notifications_server.configs.settings import settings
from notifications_server.emailer.sender import EmailBatchError
from notifications_server.processors import notification as processor_module
from notifications_server.processors.notification import NotificationProcessor, NotificationSendFailed


class _FakeMessageService:
    def __init__(self, responses):
        self.responses = responses

    async def send_message(self, _parsed):
        return self.responses


class _FakeTaskProducer:
    def __init__(self):
        self.calls = []
        self.headers = None

    async def publish_delayed_message(self, message, headers=None):
        self.calls.append(("delayed", message))
        self.headers = headers

    async def publish_dlq_message(self, message, headers=None):
        self.calls.append(("dlq", message))
        self.headers = headers


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

    headers = {"nb-retry-count": settings.notifications.task_retry_count - 1}
    asyncio.run(processor.process_task(headers, task))

    assert producer.calls == [
        (
            "delayed",
            {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}, "retry_platforms": ["slack"]},
        )
    ]
    assert producer.headers == {"nb-retry-count": settings.notifications.task_retry_count}


def test_process_task_moves_to_dlq_once_the_cap_is_reached():
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {"nb-retry-count": settings.notifications.task_retry_count}
    asyncio.run(processor.process_task(headers, task))

    assert producer.calls == [
        (
            "dlq",
            {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}, "retry_platforms": ["slack"]},
        )
    ]
    assert producer.headers == {"nb-retry-count": settings.notifications.task_retry_count}


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

    headers = {"nb-retry-count": settings.notifications.task_retry_count - 1}
    asyncio.run(processor.process_task(headers, task))

    [(_, retried_task)] = producer.calls
    assert retried_task["retry_platforms"] == ["ms_teams"]


@pytest.mark.parametrize("retry_count, queue", [(0, "delayed"), (settings.notifications.task_retry_count, "dlq")])
def test_process_task_requeues_the_full_finding(retry_count, queue):
    """The finding is trimmed only for the log line: the retried task and the DLQ
    copy must still carry its description and evidences."""
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    finding = {"fingerprint": "fp-1", "description": "CPU throttled for 10 minutes", "evidences": [{"name": "cpu"}]}
    task = json.dumps(
        {"kind": "notification", "type": "finding", "tenant_id": "t1", "parameters": {"finding": finding}}
    ).encode()

    asyncio.run(processor.process_task({"nb-retry-count": retry_count}, task))

    [(published_to, published)] = producer.calls
    assert published_to == queue
    assert published["parameters"]["finding"] == finding


def test_process_task_forwards_trace_context_and_filters_other_headers():
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {
        "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        "tracestate": "congo=t61rcWkgMzE",
        "compression": "application/x-gzip",
        "x-death": [{"count": 1}],
    }
    asyncio.run(processor.process_task(headers, task))

    assert producer.headers == {
        "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        "tracestate": "congo=t61rcWkgMzE",
        "nb-retry-count": 1,
    }


def test_failing_notification_task_retries_until_dlq():
    """Regression test: inject a failing notification task, verify that nb-retry-count
    increments across retry intervals until it reaches task_retry_count,
    and confirm the task is published to the DLQ instead of looping indefinitely."""
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task_payload = {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}
    task_bytes = json.dumps(task_payload).encode()

    headers = {}
    for retry in range(settings.notifications.task_retry_count):
        producer.calls.clear()
        producer.headers = None
        asyncio.run(processor.process_task(headers, task_bytes))
        assert producer.calls == [("delayed", {**task_payload, "retry_platforms": ["slack"]})]
        headers = producer.headers
        assert headers["nb-retry-count"] == retry + 1

    producer.calls.clear()
    producer.headers = None
    asyncio.run(processor.process_task(headers, task_bytes))
    assert producer.calls == [("dlq", {**task_payload, "retry_platforms": ["slack"]})]
    assert producer.headers == {"nb-retry-count": settings.notifications.task_retry_count}


@pytest.mark.parametrize("invalid_value", [None, "", "not-an-int", {}, []])
def test_process_task_handles_malformed_retry_count_header_defensively(invalid_value):
    producer = _FakeTaskProducer()
    processor = _processor([{"platform": "slack", "status": "failed"}], producer)
    task = json.dumps({"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}).encode()

    headers = {"nb-retry-count": invalid_value}
    asyncio.run(processor.process_task(headers, task))

    # Should default attempt to 0 and publish retry with nb-retry-count = 1
    assert producer.calls == [
        (
            "delayed",
            {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}, "retry_platforms": ["slack"]},
        )
    ]
    assert producer.headers == {"nb-retry-count": 1}


def _failed(channel_id, reason, retryable, platform="slack"):
    return {
        "platform": platform,
        "status": "failed",
        "reason": reason,
        "retryable": retryable,
        "destination_id": "slack-1",
        "channel_id": channel_id,
    }


def _retried_task(responses, task=None):
    producer = _FakeTaskProducer()
    processor = _processor(responses, producer)
    task = task or {"kind": "notification", "tenant_id": "t1", "parameters": {"message": "hi"}}
    asyncio.run(processor.process_task({}, json.dumps(task).encode()))
    return [message for _queue, message in producer.calls]


def test_a_failure_sending_again_cannot_fix_is_not_retried():
    assert _retried_task([_failed("C1", "not_in_channel", False)]) == []


def test_nowhere_to_send_is_not_retried():
    assert _retried_task([{"platform": "system", "status": "not_sent", "reason": "no_destination"}]) == []


def test_a_retry_names_only_the_channels_that_failed_with_a_temporary_error():
    [retried] = _retried_task(
        [
            {"platform": "slack", "status": "success", "destination_id": "slack-1", "channel_id": "C1"},
            _failed("C2", "not_in_channel", False),
            _failed("C3", "platform_unavailable", True),
        ]
    )

    assert retried["retry_destinations"] == [{"platform": "slack", "destination_id": "slack-1", "channel_id": "C3"}]
    assert retried["retry_platforms"] == ["slack"]


def test_a_failure_outside_any_destination_retries_the_whole_message():
    [retried] = _retried_task(
        [{"platform": "system", "status": "failed", "reason": "internal_error", "retryable": True}]
    )

    assert "retry_destinations" not in retried and "retry_platforms" not in retried


def test_a_whole_message_failure_on_a_retry_keeps_the_earlier_scope():
    scope = [{"platform": "slack", "destination_id": "slack-1", "channel_id": "C3"}]
    task = {
        "kind": "notification",
        "tenant_id": "t1",
        "parameters": {"message": "hi"},
        "retry_destinations": scope,
        "retry_platforms": ["slack"],
    }

    [retried] = _retried_task(
        [{"platform": "system", "status": "failed", "reason": "internal_error", "retryable": True}], task
    )

    assert retried["retry_destinations"] == scope and retried["retry_platforms"] == ["slack"]


class _Emailer:
    def __init__(self, error=None):
        self.error = error
        self.sent_to = []

    async def process_email_task(self, emails, _task):
        self.sent_to.append(sorted(emails))
        if self.error:
            raise self.error


def _email_retry(monkeypatch, error, task, rules=(["a@x.io", "b@x.io", "c@x.io"], [], False)):
    async def configured_rules(*_args):
        return rules

    emailer = _Emailer(error)
    monkeypatch.setattr(processor_module.ProcessorFactory, "get", lambda _kind: emailer)
    producer = _FakeTaskProducer()
    processor = _processor([], producer)
    processor._get_configured_email_rules = configured_rules
    asyncio.run(processor.process_task({}, json.dumps(task).encode()))
    return emailer.sent_to, [message for _queue, message in producer.calls]


def _throttled():
    return smtplib.SMTPDataError(454, b"Throttling failure")


def test_an_email_retry_names_only_the_recipients_that_failed(monkeypatch):
    error = EmailBatchError("2 of 3 failed", {"c@x.io": _throttled(), "a@x.io": _throttled()})
    sent_to, [retried] = _email_retry(monkeypatch, error, {"kind": "email", "tenant_id": "t1"})

    assert sent_to == [["a@x.io", "b@x.io", "c@x.io"]]
    assert retried["retry_recipients"] == ["a@x.io", "c@x.io"]


def test_an_email_retry_is_sent_only_to_the_recipients_that_failed(monkeypatch):
    error = EmailBatchError("1 of 1 failed", {"c@x.io": _throttled()})
    task = {"kind": "email", "tenant_id": "t1", "retry_recipients": ["a@x.io", "c@x.io"]}
    sent_to, [retried] = _email_retry(monkeypatch, error, task)

    assert sent_to == [["a@x.io", "c@x.io"]]
    assert retried["retry_recipients"] == ["c@x.io"]


def test_a_whole_email_failure_on_a_retry_keeps_the_earlier_recipients(monkeypatch):
    task = {"kind": "email", "tenant_id": "t1", "retry_recipients": ["c@x.io"]}
    _sent_to, [retried] = _email_retry(monkeypatch, smtplib.SMTPServerDisconnected("gone"), task)

    assert retried["retry_recipients"] == ["c@x.io"]


def test_an_email_retry_skips_a_recipient_excluded_since_the_first_attempt(monkeypatch):
    task = {"kind": "email", "tenant_id": "t1", "retry_recipients": ["a@x.io", "c@x.io"]}
    sent_to, retried = _email_retry(monkeypatch, None, task, rules=(["a@x.io", "c@x.io"], ["c@x.io"], False))

    assert sent_to == [["a@x.io"]]
    assert retried == []


def test_an_email_retry_a_rule_now_stops_is_not_sent(monkeypatch):
    task = {"kind": "email", "tenant_id": "t1", "retry_recipients": ["c@x.io"]}
    sent_to, retried = _email_retry(monkeypatch, None, task, rules=None)

    assert sent_to == []
    assert retried == []
