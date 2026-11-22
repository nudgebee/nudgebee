from typing import Dict, Any

from controllers.base import BaseController
from handlers.telemetry_handler import handle_telemetry
from metrics import otel_metrics


class TelemetryController(BaseController):
    def post_async(self, tenant_id: str, cloud_account_id: str, agent_id: str, data: Dict[str, Any]) -> None:
        # Not a queue consumer: telemetry (and the playbooks it carries) is handled
        # inline on the HTTP request, so it is instrumented here instead.
        with otel_metrics.track_handler("telemetry", tenant_id, cloud_account_id, "heartbeat"):
            handle_telemetry(tenant_id, cloud_account_id, agent_id, data)
