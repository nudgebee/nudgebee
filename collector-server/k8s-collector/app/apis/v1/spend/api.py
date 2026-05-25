import json

from flask import request

from apis.base.api import BaseAuthApi
from controllers.spend_controller import SpendController


class Spends(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return SpendController

    def post(self):
        event_data = request.get_json()
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        self.controller.post_async(tenant, cloud_account_id, event_data)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}
