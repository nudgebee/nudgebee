from typing import Dict, Any

from controllers.base import BaseController
from handlers.telemetry_handler import handle_telemetry


class TelemetryController(BaseController):
    def post_async(self, tenant_id: str, cloud_account_id: str, agent_id: str, data: Dict[str, Any]) -> None:
        handle_telemetry(tenant_id, cloud_account_id, agent_id, data)
