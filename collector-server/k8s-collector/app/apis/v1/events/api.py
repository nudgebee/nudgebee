from flask import request
import json
from apis.base.api import BaseAuthApi, read_json_body
from controllers.event_controller import EventController


class Events(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return EventController

    def post(self):
        event_data = read_json_body()
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        self.controller.post_async(tenant, cloud_account_id, event_data)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}
