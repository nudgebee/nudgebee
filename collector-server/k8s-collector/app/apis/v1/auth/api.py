import json

from apis.base.api import BaseAuthApi


class AuthValidate(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def post(self):
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}

    def get(self):
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}
