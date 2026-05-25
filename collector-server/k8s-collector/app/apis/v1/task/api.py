import json

from flask import request

from apis.base.api import BaseAuthApi
from controllers.task_controller import TaskController


class Task(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return TaskController

    def post(self, task_id: str):
        data = request.get_json()
        agent_id = request.agent_id
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        self.controller.post(tenant, cloud_account_id, agent_id, task_id, data)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}

    def get(self):
        agent_id = request.agent_id
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        tasks, rem_task_count = self.controller.get_agent_tasks(tenant, cloud_account_id, agent_id)
        body = {"data": tasks, "remaining_task_count": rem_task_count}

        lambda_headers = {
            "Content-Type": "application/json",
        }
        return (body, 200, lambda_headers)
