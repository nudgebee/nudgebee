import gzip
import json

from flask import request
from exception.collector_exceptions import BadRequestError
from apis.base.api import BaseAuthApi
from controllers.open_cost_controller import OpenCostController


class OpenCostData(BaseAuthApi):
    def __init__(self, api=None, *args, **kwargs):
        super().__init__(api, *args, **kwargs)

    def _get_controller_class(self):
        return OpenCostController

    def post(self):
        if request.headers.get("Content-Encoding") == "gzip":
            compressed_data = request.get_data()
            decompressed_data = gzip.decompress(compressed_data)
            metric_data = json.loads(decompressed_data.decode("utf-8"))
        else:
            metric_data = request.get_json()
        start_date = request.args.get("start_date", None, type=int)
        end_date = request.args.get("end_date", None, type=int)
        account_id = request.cloud_account_id
        tenant = request.tenant
        if metric_data and metric_data["code"] == 200:
            metric_data = metric_data["data"]
        else:
            raise BadRequestError("No data received")
        self.controller.store_data(metric_data, start_date, end_date, account_id, tenant)
        return json.dumps({"success": True}), 200, {"ContentType": "application/json"}

    def get(self):
        attributes = self.controller.get()

        return self.build_response(data=attributes)
