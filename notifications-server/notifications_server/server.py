import asyncio
import logging
import os
from contextlib import asynccontextmanager, suppress

import uvicorn
from fastapi import FastAPI, Request
from fastapi.concurrency import run_in_threadpool
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor

from notifications_server import engine, sync_engine, slack_app, teams_app
from notifications_server.configs import setup_logger, HealthCheckFilter, settings
from notifications_server.processors.notification import NotificationProcessor
from notifications_server.rabbitmq.message_consumer import Consumer
from notifications_server.routers import tools, common, actions, llm_callbacks
from notifications_server.services.channel_ingest import ChannelIngestService
from notifications_server.tracing import setup_tracing

setup_logger()
setup_tracing()
logging.getLogger("uvicorn.access").addFilter(HealthCheckFilter())
LOG = logging.getLogger(__name__)


def _sweep_expired_channel_messages():
    with ChannelIngestService(engine=sync_engine) as ingest:
        return ingest.sweep_expired()


async def run_retention_sweeper():
    """Enforce each channel's retention window. Retained conversation is only
    acceptable because it expires, so this runs alongside ingest rather than
    being deferred to external scheduling. The delete is offloaded to a worker
    thread — this service has previously wedged its health checks by running
    blocking work on the shared event loop."""
    interval = max(settings.notifications.channel_retention_sweep_minutes, 1) * 60
    while True:
        try:
            await asyncio.sleep(interval)
            deleted = await run_in_threadpool(_sweep_expired_channel_messages)
            if deleted:
                LOG.info("Channel retention sweep removed %d expired messages", deleted)
        except asyncio.CancelledError:
            raise
        except Exception:
            LOG.exception("Channel retention sweep failed; will retry next interval")


@asynccontextmanager
async def lifespan(_: FastAPI):
    if not settings.action_api_server_token:
        LOG.warning(
            "ACTION_API_SERVER_TOKEN is empty — all edge-forwarded webhooks "
            "(Slack interactive, MS Teams, Google Chat) will be rejected with 401."
        )
    consumer = Consumer(
        settings.rabbitmq.notifications_queue,
        settings.rabbitmq.host,
        settings.rabbitmq.port,
        settings.rabbitmq.username,
        settings.rabbitmq.password,
    )
    processor = NotificationProcessor(consumer, engine, slack_app, teams_app)
    consumer_task = asyncio.create_task(consumer.run(processor.process_task))
    LOG.info("RabbitMQ consumer started as asyncio task")
    sweeper_task = asyncio.create_task(run_retention_sweeper())
    LOG.info("Channel retention sweeper started as asyncio task")
    try:
        yield
    finally:
        await consumer.stop()
        consumer_task.cancel()
        sweeper_task.cancel()
        with suppress(asyncio.CancelledError):
            await consumer_task
        with suppress(asyncio.CancelledError):
            await sweeper_task
        LOG.info("RabbitMQ consumer and retention sweeper stopped")


app = FastAPI(lifespan=lifespan)
# Extract the inbound traceparent and start a server span so this service
# continues the caller's distributed trace instead of starting a new root.
FastAPIInstrumentor().instrument_app(app)


@app.exception_handler(RequestValidationError)
async def validation_exception_handler(request: Request, exc: RequestValidationError):
    body = None
    try:
        body = await request.json()
    except Exception:
        pass
    LOG.error(
        "Request validation error on %s %s: %s | body=%s",
        request.method,
        request.url.path,
        exc.errors(),
        body,
    )
    safe_errors = []
    for err in exc.errors():
        safe_err = {k: v.decode("utf-8", errors="replace") if isinstance(v, bytes) else v for k, v in err.items()}
        safe_errors.append(safe_err)
    return JSONResponse(status_code=422, content={"detail": safe_errors})


app.include_router(tools.router)
app.include_router(tools.slack_router)
app.include_router(common.router)
app.include_router(actions.router)
app.include_router(llm_callbacks.router)


@app.get("/health")
async def health_probe():
    # Intentionally async so the liveness probe is answered directly on the event loop,
    # with no dependency on the sync threadpool. Blocking webhook handlers are offloaded
    # to worker threads, so /health stays responsive even when Google/Slack/Teams APIs stall.
    return {"status": "ok"}


if __name__ == "__main__":
    LOG.info("Starting notifications server")
    port_env = os.getenv("PORT")
    port = 8080
    if port_env:
        try:
            port = int(port_env)
        except ValueError:
            LOG.error("Invalid PORT environment variable: %s. Defaulting to 8080.", port_env)
    uvicorn.run(app, port=port)
