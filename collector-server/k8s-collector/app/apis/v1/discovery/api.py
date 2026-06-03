import gzip
import json

from flask import request

from apis.base.api import BaseAuthApi
from controllers.discovery_controller import DiscoveryController


class Discovery(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return DiscoveryController

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

    def get(self, resource_type: str):
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        if resource_type == "service":
            types = ["Pod", "DaemonSet", "StatefulSet", "Deployment"]
        elif resource_type == "node":
            types = ["node"]
        else:
            types = [resource_type]
        filter_params = {"type": types}
        active_resources = self.controller.get_active_resources(tenant, cloud_account_id, filter_params)
        return self.build_response(data=active_resources)
