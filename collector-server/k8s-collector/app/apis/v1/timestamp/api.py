from flask import request
from apis.base.api import BaseAuthApi
from controllers.opencostatributes import OpenCostAtrributesController


class OpenCostAtrributes(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return OpenCostAtrributesController

    def get(self):
        account_id = request.cloud_account_id
        attributes = self.controller.get(account_id)

        return self.build_response(data=attributes)
