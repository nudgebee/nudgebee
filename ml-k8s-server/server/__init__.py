import logging.config
import json
import os

from opentelemetry import trace


class TraceIdFilter(logging.Filter):
    """Injects the active OTel trace_id / span_id (bare 32/16-hex, matching the
    Go services and Loki `trace_id` queries) into every LogRecord. Empty when no
    span is active."""

    def filter(self, record: logging.LogRecord) -> bool:
        ctx = trace.get_current_span().get_span_context()
        record.trace_id = format(ctx.trace_id, "032x") if ctx.trace_id else ""
        record.span_id = format(ctx.span_id, "016x") if ctx.span_id else ""
        return True


def setup_logger() -> None:
    basedir = os.path.abspath(os.path.dirname(__file__))
    config_file = os.path.join(basedir, "logging.json")
    with open(config_file, "rt") as f:
        config = json.load(f)
        logging.config.dictConfig(config)
