"""Teams events run off the main event loop, with the service built, used and
closed in one worker thread."""

import asyncio
import threading
from types import SimpleNamespace
from unittest import mock

from fastapi import BackgroundTasks

from notifications_server.routers import actions


class _RecordingService:
    def __init__(self, **_):
        self.created_on = threading.get_ident()
        self.ran_on = None
        self.closed_on = None

    async def execute_event(self, payload):
        self.ran_on = threading.get_ident()
        self.payload = payload

    def close(self):
        self.closed_on = threading.get_ident()


class _FailingService(_RecordingService):
    async def execute_event(self, payload):
        raise RuntimeError("boom")


def _recording(service_class):
    """A stand-in for MsTeamsEventsService that keeps the services it builds."""
    built = []

    def build(**kwargs):
        built.append(service_class(**kwargs))
        return built[-1]

    return build, built


def _request(payload):
    async def json():
        return payload

    return SimpleNamespace(method="POST", headers={}, json=json)


def _deliver(payload, build):
    async def deliver():
        tasks = BackgroundTasks()
        with (
            mock.patch.object(actions, "internal_token_valid", return_value=True),
            mock.patch.object(actions, "MsTeamsEventsService", build),
        ):
            response = await actions.handle_ms_teams_events(_request(payload), tasks)
            try:
                await tasks()
            except RuntimeError:
                pass
        return response, threading.get_ident()

    return asyncio.run(deliver())


def test_a_teams_event_is_handled_in_one_worker_thread():
    build, built = _recording(_RecordingService)
    payload = {"type": "message", "text": "hi"}
    response, loop_thread = _deliver(payload, build)
    assert response.status_code == 200
    (service,) = built
    assert service.payload == payload
    assert service.created_on == service.ran_on == service.closed_on
    assert service.created_on != loop_thread, "the handler must not run on the event loop's thread"


def test_the_service_is_closed_when_the_handler_fails():
    build, built = _recording(_FailingService)
    _deliver({"type": "message"}, build)
    (service,) = built
    assert service.closed_on == service.created_on
