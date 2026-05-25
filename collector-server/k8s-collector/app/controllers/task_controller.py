from typing import Dict, Any, List, Tuple

from controllers.base import BaseController
from handlers.task_handler import save_task_status, get_agent_tasks


class TaskController(BaseController):
    def post(self, tenant_id: str, cloud_account_id: str, agent_id: str, task_id: str, data: Dict[str, Any]) -> None:
        save_task_status(tenant_id, cloud_account_id, agent_id, task_id, data)

    def get_agent_tasks(self, tenant_id: str, cloud_account_id: str, agent_id: str) -> Tuple[List[Dict[str, Any]], int]:
        return get_agent_tasks(tenant_id, cloud_account_id, agent_id)
