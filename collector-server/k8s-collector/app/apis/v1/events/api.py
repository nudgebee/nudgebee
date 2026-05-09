import gzip

from flask import request
import json
from apis.base.api import BaseAuthApi
from controllers.event_controller import EventController


class Events(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return EventController

    def post(self):
        if request.headers.get("Content-Encoding") == "gzip":
            compressed_data = request.get_data()
            decompressed_data = gzip.decompress(compressed_data)
            event_data = json.loads(decompressed_data.decode("utf-8"))
        else:
            event_data = request.get_json()
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        self.controller.post_async(tenant, cloud_account_id, event_data)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}
