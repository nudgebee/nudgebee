import json

from apis.base.api import BaseAuthApi
from controllers.auto_pilot_controller import AutoPilotController
from flask import request


class AutopilotHandler(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return AutoPilotController

    def post(self):
        req_data = request.get_json()
        account_id = request.cloud_account_id
        tenant_id = request.tenant
        self.controller.post_async(account_id=account_id, tenant_id=tenant_id, request_data=req_data)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}
