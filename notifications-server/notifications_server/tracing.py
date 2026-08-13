"""OpenTelemetry tracing setup for notifications-server.

A real SDK ``TracerProvider`` is installed (not the API no-op) even when no
exporter is configured: Python's no-op tracer returns an all-zero span context
and does NOT preserve an extracted remote parent, so without a real provider the
``trace_id`` would log as all-zeros and inbound ``traceparent`` propagation would
break. No span exporter is attached here — trace_id propagation and trace_id
logging do not require one; a collector can be wired later via standard OTEL_*
env vars if trace export is ever wanted.
"""

import os

from opentelemetry import trace
from opentelemetry.instrumentation.aiohttp_client import AioHttpClientInstrumentor
from opentelemetry.instrumentation.requests import RequestsInstrumentor
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider


def setup_tracing() -> None:
    resource = Resource.create({"service.name": os.environ.get("OTEL_SERVICE_NAME", "notifications-server")})
    trace.set_tracer_provider(TracerProvider(resource=resource))
    # Propagate the traceparent on outbound HTTP (api-server / llm-server / chat webhooks).
    RequestsInstrumentor().instrument()
    AioHttpClientInstrumentor().instrument()
