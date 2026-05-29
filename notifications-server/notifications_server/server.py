# added a test change which does absolutely nothing

import asyncio
import logging
from contextlib import asynccontextmanager, suppress

import uvicorn
from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

from notifications_server import engine, slack_app, teams_app
from notifications_server.configs import setup_logger, HealthCheckFilter, settings
from notifications_server.processors.notification import NotificationProcessor
from notifications_server.rabbitmq.message_consumer import Consumer
from notifications_server.routers import tools, common, actions, llm_callbacks

setup_logger()
logging.getLogger("uvicorn.access").addFilter(HealthCheckFilter())
LOG = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(_: FastAPI):
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
    try:
        yield
    finally:
        await consumer.stop()
        consumer_task.cancel()
        with suppress(asyncio.CancelledError):
            await consumer_task
        LOG.info("RabbitMQ consumer stopped")


app = FastAPI(lifespan=lifespan)


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
def health_probe():
    return {"status": "ok"}


if __name__ == "__main__":
    LOG.info("Starting notifications server")
    uvicorn.run(app, port=8080)
