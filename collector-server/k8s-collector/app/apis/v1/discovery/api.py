import json

from flask import request

from apis.base.api import BaseAuthApi, read_json_body
from controllers.discovery_controller import DiscoveryController


class Discovery(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return DiscoveryController

    def post(self):
        event_data = read_json_body()
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        self.controller.post_async(tenant, cloud_account_id, event_data)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}

    def get(self, resource_type: str):
        cloud_account_id = request.cloud_account_id
        tenant = request.tenant
        if resource_type == "service":
            types = ["Pod", "DaemonSet", "StatefulSet", "Deployment", "Rollout"]
        elif resource_type == "node":
            types = ["node"]
        else:
            types = [resource_type]
        filter_params = {"type": types}
        active_resources = self.controller.get_active_resources(tenant, cloud_account_id, filter_params)
        return self.build_response(data=active_resources)
